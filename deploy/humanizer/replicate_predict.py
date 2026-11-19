import os

from cog import BaseModel, BaseRunner

from batch_runtime import JIALIN, SMAUG, Humanizer


class Output(BaseModel):
    texts: list[str]


class Runner(BaseRunner):
    model_id = JIALIN

    def setup(self):
        os.environ.setdefault("HF_HUB_CACHE", "/opt/humanizer")
        os.environ.setdefault("HF_HUB_OFFLINE", "1")
        os.environ.setdefault("VLLM_ENABLE_V1_MULTIPROCESSING", "0")
        self.humanizer = Humanizer(self.model_id)

    def run(self, model: str, texts: list[str]) -> Output:
        return Output(**self.humanizer.rewrite(model, texts))


class SmaugRunner(Runner):
    model_id = SMAUG
