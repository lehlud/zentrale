import os

import modal

from batch_runtime import JIALIN, MODEL_IDS, VLLM_REQUIREMENT, cache_weights

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
    single_use_containers=True,
)
@modal.fastapi_endpoint(method="POST", requires_proxy_auth=True)
def rewrite(payload: dict):
    from batch_runtime import Humanizer, validate_batch

    model = payload.get("model")
    texts = payload.get("texts")
    validate_batch(model, texts, MODEL_ID)
    return Humanizer(MODEL_ID).rewrite(model, texts)
