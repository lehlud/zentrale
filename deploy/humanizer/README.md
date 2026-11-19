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

Set `ZENTRALE_HUMANIZER_MODEL=abacusai/Smaug-Humanizer` when deploying the Smaug endpoint. Each authenticated Web Function call gets a single-use GPU container. Model files are downloaded at image build time. Enter the resulting `.modal.run` URL as the provider URL in Zentrale and use your Modal workspace proxy token as `TOKEN_ID.TOKEN_SECRET`. Zentrale allows up to four minutes including startup and follows the gateway's result URL for a delayed response. [Single-use containers](https://modal.com/docs/guide/functions) · [Proxy tokens](https://modal.com/docs/guide/webhook-proxy-auth) · [Request timeouts](https://modal.com/docs/guide/webhook-timeouts)

## fal.ai

```sh
fal deploy fal_app.py::HumanizerApp --auth private --strategy recreate
```

Use `fal_app.py::SmaugApp` for Smaug and enter `owner/app/rewrite` in Zentrale. Both apps use one L40 GPU, zero minimum runners, one maximum runner, and a one-second idle window. The first start downloads the weights to the persistent `/data` cache for reuse by later runners. For an existing app, deploy with `--reset-scale` to apply these scaling settings. Zentrale's queue cancellation reaches the running app, which cancels the whole batch. [Machine types](https://fal.ai/docs/documentation/deployment/machine-types) · [Scaling and idle billing](https://fal.ai/learn/serverless/serverless-inference-autoscaling) · [Queue cancellation](https://fal.ai/docs/documentation/development/handle-cancellations)

## Replicate

Create a model and give it a GPU with sufficient memory for BF16 weights. The Cog configuration uses `jialinyyzz/humanizer`. To build Smaug, change the download to `abacusai/Smaug-Humanizer` and the runner to `replicate_predict.py:SmaugRunner` in `cog.yaml`.

```sh
cog push r8.im/owner/model
```

Use the versioned `owner/model:version` target or the ID of a deployment with a minimum of zero instances and a maximum of one. Zentrale sends a server-side prediction deadline and cancels outstanding predictions when it stops waiting. Replicate controls instance teardown and charges idle time on standard private models and deployments. Use the Modal recipe when terminating the GPU container after each individual request is required. [Prediction deadlines](https://replicate.com/docs/topics/predictions/create-a-prediction/) · [Idle billing](https://replicate.com/docs/topics/billing/)

## Zentrale

Add the platform under **Provider**. Select **Einstellungen → Humanizer → GPU-Humanizer einrichten** and choose the preset deployed above. Other models and JSON input schemas can use the same connected account through `run_hosted_model`. For other endpoints you own, their GPU lifetime and idle settings are controlled by their deployments.
