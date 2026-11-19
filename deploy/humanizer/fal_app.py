import asyncio
import os

import fal
from fal.exceptions import RequestCancelledException
from fastapi import Header, HTTPException
from pydantic import BaseModel

from batch_runtime import JIALIN, SMAUG, VLLM_REQUIREMENT, Humanizer


class Batch(BaseModel):
    model: str
    texts: list[str]


class AsyncHumanizer(Humanizer):
    @staticmethod
    def create_engine(**options):
        from vllm import AsyncEngineArgs, AsyncLLMEngine

        return AsyncLLMEngine.from_engine_args(
            AsyncEngineArgs(**options), start_engine_loop=False
        )

    async def generate(self, prompt, parameters, request_id):
        result = None
        async for result in self.engine.generate(prompt, parameters, request_id):
            pass
        if result is None:
            raise RuntimeError("The Humanizer did not return a rewrite.")
        return result

    async def rewrite(self, model, texts, request_id):
        prompts, parameters = self.prepare(model, texts)
        async with asyncio.TaskGroup() as batch:
            tasks = [
                batch.create_task(self.generate(prompt, params, f"{request_id}-{index}"))
                for index, (prompt, params) in enumerate(zip(prompts, parameters))
            ]
        return self.rewrites([task.result() for task in tasks])


class HumanizerApp(fal.App):
    machine_type = "GPU-L40"
    requirements = [VLLM_REQUIREMENT, "huggingface_hub"]
    local_python_modules = ["batch_runtime"]
    min_concurrency = 0
    max_concurrency = 1
    concurrency_buffer = 0
    keep_alive = 1
    request_timeout = 105
    startup_timeout = 180
    model_id = JIALIN

    def setup(self):
        os.environ.setdefault("HF_HUB_CACHE", "/data/zentrale-humanizer")
        self.humanizer = AsyncHumanizer(self.model_id)
        self.tasks = {}

    def teardown(self):
        self.humanizer.engine.shutdown()

    @fal.endpoint("/rewrite")
    async def rewrite(self, batch: Batch, x_fal_request_id: str = Header()) -> dict[str, list[str]]:
        if x_fal_request_id in self.tasks:
            raise HTTPException(409, "The batch is already running.")
        task = asyncio.create_task(self.humanizer.rewrite(batch.model, batch.texts, x_fal_request_id))
        self.tasks[x_fal_request_id] = task
        try:
            return await task
        except asyncio.CancelledError:
            raise RequestCancelledException("Humanizer batch canceled.") from None
        finally:
            self.tasks.pop(x_fal_request_id, None)

    @fal.endpoint("/rewrite/cancel")
    async def cancel_rewrite(self, x_fal_request_id: str = Header()) -> dict[str, bool]:
        task = self.tasks.get(x_fal_request_id)
        return {"canceled": task is not None and task.cancel()}


class SmaugApp(HumanizerApp):
    model_id = SMAUG
