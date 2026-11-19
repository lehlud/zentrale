"""One model and one ordered batch per inference request."""

import json
import os
from pathlib import Path

JIALIN = "jialinyyzz/humanizer"
SMAUG = "abacusai/Smaug-Humanizer"
MODEL_IDS = (JIALIN, SMAUG)
VLLM_REQUIREMENT = "vllm==0.34.0"
MAX_CONTEXT = 8192
MAX_BATCH_ITEMS = 24
MAX_BATCH_BYTES = 64_000
MAX_ITEM_BYTES = 12_000
WEIGHT_FILES = ["*.safetensors", "*.json", "*.jinja", "*.model"]


def validate_batch(model, texts, expected_model):
    if model != expected_model or model not in MODEL_IDS:
        raise ValueError("This endpoint serves a different Humanizer model.")
    if not isinstance(texts, list) or not 1 <= len(texts) <= MAX_BATCH_ITEMS:
        raise ValueError("Provide between 1 and 24 text items.")
    total = 0
    for text in texts:
        if not isinstance(text, str) or not text.strip():
            raise ValueError("Each batch item must be nonempty text.")
        size = len(text.encode("utf-8"))
        if size > MAX_ITEM_BYTES or "\x00" in text:
            raise ValueError("Each text must fit in 12,000 bytes and contain no NUL.")
        total += size
    if total > MAX_BATCH_BYTES:
        raise ValueError("The batch must fit in 64,000 bytes.")


def cache_weights(model_id):
    from huggingface_hub import snapshot_download

    if model_id not in MODEL_IDS:
        raise ValueError("Choose a supported model preset.")
    return snapshot_download(
        model_id,
        cache_dir=os.environ.get("HF_HUB_CACHE"),
        allow_patterns=WEIGHT_FILES,
        local_files_only=os.environ.get("HF_HUB_OFFLINE") == "1",
    )


class Humanizer:
    def __init__(self, model_id):
        model_path = Path(cache_weights(model_id))
        self.model_id = model_id
        self.prompt_format = None
        if model_id == JIALIN:
            self.prompt_format = json.loads(
                (model_path / "prompt_format.json").read_text(encoding="utf-8")
            )
        self.engine = self.create_engine(
            model=str(model_path),
            dtype="bfloat16",
            max_model_len=MAX_CONTEXT,
            max_num_seqs=MAX_BATCH_ITEMS,
            limit_mm_per_prompt={"image": 0, "audio": 0, "video": 0},
            enforce_eager=True,
        )
        self.tokenizer = self.engine.get_tokenizer()

    @staticmethod
    def create_engine(**options):
        from vllm import LLM

        return LLM(**options)

    def rewrite(self, model, texts):
        prompts, parameters = self.prepare(model, texts)
        results = self.engine.generate(prompts, parameters, use_tqdm=False)
        return self.rewrites(results)

    def prepare(self, model, texts):
        from vllm import SamplingParams

        validate_batch(model, texts, self.model_id)
        prompts = []
        parameters = []
        for text in texts:
            token_ids = self.tokenizer.encode(
                self.prompt(text), add_special_tokens=self.prompt_format is not None
            )
            available = MAX_CONTEXT - len(token_ids)
            if available < 64:
                raise ValueError("Split the input into shorter passages.")
            prompts.append({"prompt_token_ids": token_ids})
            parameters.append(
                SamplingParams(
                    temperature=1.0,
                    top_p=0.95,
                    top_k=-1 if self.prompt_format else 64,
                    min_p=0.0,
                    repetition_penalty=1.0,
                    max_tokens=min(2048, available),
                    stop=None if self.prompt_format else ["<end_of_turn>"],
                )
            )
        return prompts, parameters

    @staticmethod
    def rewrites(results):
        if any(item.outputs[0].finish_reason == "length" for item in results):
            raise ValueError("A rewrite reached the output limit. Use shorter passages.")
        return {"texts": [item.outputs[0].text.strip() for item in results]}

    def prompt(self, text):
        if self.prompt_format:
            return (
                self.prompt_format["instr"]
                + "\n\n"
                + text.strip()
                + self.prompt_format["sep"]
            )
        instruction = (
            "Rewrite the following passage in natural language. Preserve its language, "
            "structure, facts, numbers, names and quotations. Return only the rewrite.\n\n"
            f"Text:\n{text}"
        )
        return self.tokenizer.apply_chat_template(
            [{"role": "user", "content": instruction}],
            tokenize=False,
            add_generation_prompt=True,
        )
