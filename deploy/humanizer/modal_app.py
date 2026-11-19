import asyncio
import json
import os
import time

import modal

from batch_runtime import JIALIN, MODEL_IDS, VLLM_REQUIREMENT, cache_weights, validate_batch

MODEL_ID = os.environ.get("ZENTRALE_HUMANIZER_MODEL", JIALIN)
if MODEL_ID not in MODEL_IDS:
    raise ValueError("ZENTRALE_HUMANIZER_MODEL must be a supported model ID.")

app = modal.App("zentrale-" + MODEL_ID.split("/")[1].lower())
image = (
    modal.Image.debian_slim(python_version="3.12")
    .pip_install(VLLM_REQUIREMENT, "fastapi", "huggingface_hub")
    .env({"HF_HUB_CACHE": "/model-cache"})
    .add_local_python_source("batch_runtime", copy=True)
    .run_function(cache_weights, kwargs={"model_id": MODEL_ID})
    .pip_install("cbor2")
    .env(
        {
            "HF_HUB_OFFLINE": "1",
            "VLLM_ENABLE_V1_MULTIPROCESSING": "0",
            "ZENTRALE_HUMANIZER_MODEL": MODEL_ID,
        }
    )
)


@app.function(
    image=image,
    gpu="L40S",
    cpu=4.0,
    min_containers=0,
    buffer_containers=0,
    max_containers=1,
    scaledown_window=2,
    timeout=135,
    retries=0,
    single_use_containers=True,
)
def run_batch(payload: dict, expires: float | None = None):
    from batch_runtime import Humanizer

    model, texts = payload["model"], payload["texts"]
    validate_batch(model, texts, MODEL_ID)
    if expires is None:
        expires = time.time() + 135
    if time.time() >= expires:
        raise TimeoutError("The Humanizer job expired before startup.")
    humanizer = Humanizer(MODEL_ID)
    if time.time() >= expires:
        raise TimeoutError("The Humanizer job expired while starting.")
    return humanizer.rewrite(model, texts)


web_image = (
    modal.Image.debian_slim(python_version="3.12")
    .pip_install("fastapi")
    .add_local_python_source("batch_runtime", copy=True)
    .env({"ZENTRALE_HUMANIZER_MODEL": MODEL_ID})
)


@app.function(image=web_image, min_containers=0, scaledown_window=2, timeout=245)
@modal.concurrent(max_inputs=16)
@modal.asgi_app(requires_proxy_auth=True)
def rewrite():
    import anyio
    from fastapi import FastAPI, HTTPException, Request
    from fastapi.responses import StreamingResponse

    api = FastAPI()

    class BatchResponse(StreamingResponse):
        async def __call__(self, scope, receive, send):
            try:
                await super().__call__(scope, receive, send)
            finally:
                # Also close on a failed send, when the generator is suspended
                # at a keepalive and Starlette exits its response loop.
                with anyio.CancelScope(shield=True):
                    await self.body_iterator.aclose()

    @api.post("/")
    async def submit(request: Request):
        try:
            payload = await request.json()
            model, texts = payload["model"], payload["texts"]
            validate_batch(model, texts, MODEL_ID)
            expires = time.time() + 235
            if deadline := request.headers.get("X-Zentrale-Inference-Deadline"):
                expires = min(expires, int(deadline))
        except (TypeError, ValueError, KeyError):
            raise HTTPException(422, "Invalid Humanizer batch or deadline.") from None
        if time.time() >= expires:
            raise HTTPException(408, "The Humanizer job expired.")

        async def stream():
            job = None
            result = None
            completed = False
            try:
                # Start the response before scheduling GPU work. Whitespace
                # keeps the owner's connection open without changing the JSON.
                yield b"\n"
                job = await run_batch.spawn.aio(payload, expires)
                result = asyncio.create_task(job.get.aio())
                while True:
                    remaining = expires - time.time()
                    if remaining <= 0:
                        yield b'{"error":"Humanizer job expired"}'
                        return
                    done, _ = await asyncio.wait({result}, timeout=min(2, remaining))
                    if done:
                        output = result.result()
                        completed = True
                        yield json.dumps(output).encode()
                        return
                    yield b"\n"
            except Exception:
                yield b'{"error":"Humanizer job failed"}'
            finally:
                with anyio.move_on_after(5, shield=True):
                    if result is not None:
                        result.cancel()
                    if job is not None and not completed:
                        await job.cancel.aio(terminate_containers=True)
                        print("Canceled unfinished Humanizer batch.", flush=True)
                    if result is not None:
                        await asyncio.gather(result, return_exceptions=True)

        return BatchResponse(stream(), media_type="application/json")

    return api
