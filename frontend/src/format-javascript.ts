type Request = {
  source: string;
  resolve: (text: string) => void;
  timer: number;
};
const pending = new Map<number, Request>();
const inFlight = new Map<string, Promise<string>>();
const cache = new Map<string, string>();
let cachedLength = 0;
let sequence = 0;
let worker: Worker | undefined;
let idle: number | undefined;

function stop() {
  worker?.terminate();
  worker = undefined;
  window.clearTimeout(idle);
  for (const { source, resolve, timer } of pending.values()) {
    window.clearTimeout(timer);
    resolve(source);
  }
  pending.clear();
}

function formatter() {
  if (worker) return worker;
  const created = new Worker(
    new URL("./javascript-format.worker.ts", import.meta.url),
    { type: "module" },
  );
  worker = created;
  created.onmessage = (event: MessageEvent<{ id: number; text: string }>) => {
    if (worker !== created) return;
    const request = pending.get(event.data.id);
    if (!request) return;
    pending.delete(event.data.id);
    window.clearTimeout(request.timer);
    request.resolve(
      typeof event.data.text === "string" ? event.data.text : request.source,
    );
    if (!pending.size) idle = window.setTimeout(stop, 10000);
  };
  created.onerror = created.onmessageerror = () => {
    if (worker === created) stop();
  };
  return created;
}

// Imported only by an expanded code_mode snippet. The shared worker and its
// input/queue/cache/deadline limits keep formatting off the chat's UI thread.
export function formatJavaScript(source: string): Promise<string> {
  if (source.length > 65536 || typeof Worker === "undefined")
    return Promise.resolve(source);
  if (cache.has(source)) return Promise.resolve(cache.get(source)!);
  const existing = inFlight.get(source);
  if (existing) return existing;
  if (pending.size >= 16) return Promise.resolve(source);
  const result = new Promise<string>((resolve) => {
    try {
      const target = formatter();
      window.clearTimeout(idle);
      const id = ++sequence;
      pending.set(id, {
        source,
        resolve,
        timer: window.setTimeout(stop, 3000),
      });
      target.postMessage({ id, source });
    } catch {
      stop();
      resolve(source);
    }
  }).then((text) => {
    inFlight.delete(source);
    cache.set(source, text);
    cachedLength += source.length + text.length;
    while (cachedLength > 262144 || cache.size > 16) {
      const first = cache.keys().next().value!;
      cachedLength -= first.length + cache.get(first)!.length;
      cache.delete(first);
    }
    return text;
  });
  inFlight.set(source, result);
  return result;
}
