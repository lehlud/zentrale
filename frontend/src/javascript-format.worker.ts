import { formatJavaScriptForDisplay } from "./javascript-format";

self.onmessage = async (
  event: MessageEvent<{ id: number; source: string }>,
) => {
  const { id, source } = event.data;
  self.postMessage({ id, text: await formatJavaScriptForDisplay(source) });
};
