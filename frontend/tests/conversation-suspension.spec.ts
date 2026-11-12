import { expect, test } from "@playwright/test";
import { API } from "../src/api";

test("fullscreen suspension is scoped, reference counted and resumes newly registered watches once", () => {
  const original = globalThis.WebSocket;
  const frames: {
    type: string;
    requestId: string;
    variables?: { projectId: string };
  }[] = [];
  // Exercise the actual API subscription manager, without network or timers.
  globalThis.WebSocket = { OPEN: 1 } as typeof WebSocket;
  try {
    const api = new API();
    (api as unknown as { socket: unknown }).socket = {
      readyState: 1,
      send: (data: string) => frames.push(JSON.parse(data)),
    };
    const query =
      "query($projectId:String!){conversationBootstrap(projectId:$projectId){machineName}}";
    const watch = (projectId: string) =>
      api.watch(
        query,
        { projectId },
        () => {},
        () => {},
      );
    const removeFirst = watch("first");
    watch("other");
    api.watch(
      "query{status{authenticated}}",
      {},
      () => {},
      () => {},
    );
    expect(frames.map((frame) => frame.type)).toEqual([
      "subscribe",
      "subscribe",
      "subscribe",
    ]);

    const releaseFirst = api.suspendConversation("first");
    const releaseSecond = api.suspendConversation("first");
    expect(frames.at(-1)).toEqual({ type: "unsubscribe", requestId: "1" });
    expect(frames).toHaveLength(4);
    watch("first"); // Must not subscribe while the modal still covers this view.
    expect(frames).toHaveLength(4);
    releaseFirst();
    releaseFirst(); // Effect cleanup/repeated close is idempotent.
    expect(frames).toHaveLength(4);
    releaseSecond();
    expect(
      frames.slice(4).map((frame) => [frame.type, frame.requestId]),
    ).toEqual([
      ["subscribe", "1"],
      ["subscribe", "4"],
    ]);
    removeFirst();
    expect(frames.at(-1)).toEqual({ type: "unsubscribe", requestId: "1" });
  } finally {
    globalThis.WebSocket = original;
  }
});
