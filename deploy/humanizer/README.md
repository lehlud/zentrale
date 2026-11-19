# GPU Humanizer

Zentrale sends one JSON request per approved batch. The endpoint takes a model ID and 1–24 texts and returns the same number of rewrites in input order.

```json
{
  "model": "jialinyyzz/humanizer",
  "texts": ["First passage", "Second passage"]
}
```

```json
{ "texts": ["First rewrite", "Second rewrite"] }
```

The two presets use the same batch runtime. It uses the published completion format for `jialinyyzz/humanizer` and the model chat template for `abacusai/Smaug-Humanizer`. Run the deployment commands from this directory with the platform's CLI already configured. [jialinyyzz model card](https://huggingface.co/jialinyyzz/humanizer) · [Smaug model card](https://huggingface.co/abacusai/Smaug-Humanizer)

## Modal

```sh
modal deploy modal_app.py
```

Set `ZENTRALE_HUMANIZER_MODEL=abacusai/Smaug-Humanizer` when deploying the Smaug endpoint. Model files are downloaded at image build time. The HTTP endpoint starts one single-use GPU container per batch and cancels that job when the connection closes. The GPU function also has a 135-second execution limit. Zentrale allows up to four minutes including startup. Redeploy an older Humanizer endpoint to enable cancellation on disconnect. [Single-use containers](https://modal.com/docs/guide/functions) · [Proxy tokens](https://modal.com/docs/guide/webhook-proxy-auth) · [Function timeouts](https://modal.com/docs/guide/timeouts)

Choose **Proxy-Token (Web Function)** in Zentrale for the `rewrite` URL and fill the token ID and secret separately. For **API-Token (Modal-Funktion)**, enter the API token pair, environment, deployed App (`zentrale-humanizer` or `zentrale-smaug-humanizer`) and Function `run_batch`. The API path invokes the Function synchronously so Modal cancels the input after the caller disconnects. A custom native Function accepts one JSON-compatible dictionary and returns a JSON-compatible result. [App and Function lookups](https://modal.com/docs/guide/trigger-deployed-functions) · [Synchronous cancellation](https://modal.com/docs/guide/function-invocation-methods#durability)

## fal.ai

```sh
fal deploy fal_app.py::HumanizerApp --auth private --strategy recreate
```

Use `fal_app.py::SmaugApp` for Smaug and enter `owner/app/rewrite` in Zentrale. Both apps use one L40 GPU, zero minimum runners, one maximum runner, and a one-second idle window. The first start downloads the weights to the persistent `/data` cache for reuse by later runners. For an existing app, deploy with `--reset-scale` to apply the current scaling and timeout settings. Zentrale uses a synchronous fal request so its connection loss triggers cancellation. The runner cancels the entire batch and has a 105-second request limit. [Machine types](https://fal.ai/docs/documentation/deployment/machine-types) · [Scaling and idle billing](https://fal.ai/learn/serverless/serverless-inference-autoscaling) · [Cancellation on disconnect](https://fal.ai/docs/documentation/development/handle-cancellations)

## Replicate

Create a model and give it a GPU with sufficient memory for BF16 weights. The Cog configuration uses `jialinyyzz/humanizer`. To build Smaug, change the download to `abacusai/Smaug-Humanizer` and the runner to `replicate_predict.py:SmaugRunner` in `cog.yaml`.

```sh
cog push r8.im/owner/model
```

Use the versioned `owner/model:version` target or the ID of a deployment with a minimum of zero instances and a maximum of one. Every prediction has a server-side deadline, including when Zentrale crashes before receiving the job ID. Zentrale saves IDs as soon as Replicate returns them and cancels interrupted predictions after restarting. Replicate controls instance teardown and charges idle time on standard private models and deployments. Use the Modal recipe when terminating the GPU container after each individual request is required. [Prediction deadlines](https://replicate.com/docs/topics/predictions/create-a-prediction/) · [Idle billing](https://replicate.com/docs/topics/billing/)

## Zentrale

Add the platform under **Provider**. Select **Einstellungen → Humanizer → GPU-Humanizer einrichten** and choose the preset deployed above. Other models and JSON input schemas can use the same connected account through `run_hosted_model`. Set an execution timeout and handle disconnections in custom Modal and fal endpoints you own. A Hugging Face ZeroGPU Space enforces its own `@spaces.GPU(duration=...)` limit. [ZeroGPU duration](https://huggingface.co/docs/hub/spaces-zerogpu#duration-management)
