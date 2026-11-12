import type { Page, WebSocketRoute } from "@playwright/test";

type Variables = Record<string, any>;
type Subscription = {
  type: "subscribe";
  requestId: string;
  query: string;
  variables?: Variables;
};

/** A real server-push fixture: no synthetic HTTP requests or engine-frame refetch. */
export async function installPushFixture(
  page: Page,
  resolve: (query: string, variables: Variables) => unknown | Promise<unknown>,
  onConnection?: (socket: WebSocketRoute) => void | false,
) {
  const frames: Array<
    Subscription | { type: "unsubscribe"; requestId: string }
  > = [];
  const sockets = new Map<WebSocketRoute, Map<string, Subscription>>();
  const revisions = new WeakMap<Subscription, number>();
  const send = async (socket: WebSocketRoute, subscription: Subscription) => {
    const revision = (revisions.get(subscription) ?? 0) + 1;
    revisions.set(subscription, revision);
    let result: { data?: unknown; errors?: Array<{ message: string }> };
    try {
      result = {
        data: await resolve(subscription.query, subscription.variables ?? {}),
      };
    } catch (error) {
      result = {
        errors: [
          { message: error instanceof Error ? error.message : String(error) },
        ],
      };
    }
    if (
      sockets.get(socket)?.get(subscription.requestId) !== subscription ||
      revisions.get(subscription) !== revision
    )
      return;
    socket.send(
      JSON.stringify({
        type: "query_result",
        requestId: subscription.requestId,
        result,
      }),
    );
  };
  await page.routeWebSocket("**/events?*", (socket) => {
    const subscriptions = new Map<string, Subscription>();
    sockets.set(socket, subscriptions);
    socket.onClose(() => sockets.delete(socket));
    socket.onMessage(async (message) => {
      if (!sockets.has(socket)) return;
      const frame = JSON.parse(message.toString());
      if (frame.type === "unsubscribe") {
        frames.push(frame);
        subscriptions.delete(frame.requestId);
      } else if (frame.type === "subscribe") {
        frames.push(frame);
        subscriptions.set(frame.requestId, frame);
        await send(socket, frame);
      }
    });
    if (onConnection?.(socket) === false) sockets.delete(socket);
  });
  return {
    frames,
    close: (socket: WebSocketRoute) => {
      sockets.delete(socket);
      socket.close();
    },
    push: async () => {
      await Promise.all(
        [...sockets].flatMap(([socket, subscriptions]) =>
          [...subscriptions.values()].map((subscription) =>
            send(socket, subscription),
          ),
        ),
      );
    },
  };
}
