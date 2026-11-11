import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { useAPI } from "./api";
import { olderMessagesQuery } from "./agent-transcript";
import type { Message } from "./generated";
import { sameJSONValue } from "./stable-data";

export function mergeMessages(previous: Message[], incoming: Message[]) {
  const messages = new Map(previous.map((message) => [message.id, message]));
  let changed = false;
  for (const message of incoming) {
    if (sameJSONValue(messages.get(message.id), message)) continue;
    messages.set(message.id, message);
    changed = true;
  }
  // Avoid another render and retain parsed-call/row caches on polling refreshes.
  if (!changed) return previous;
  return [...messages.values()].sort(
    (left, right) =>
      left.createdAt.localeCompare(right.createdAt) ||
      left.id.localeCompare(right.id),
  );
}

export function useTranscriptHistory(
  agentId: string,
  incoming: Message[] | undefined,
  following: boolean,
) {
  const api = useAPI();
  const [messages, setMessages] = useState<Message[]>([]);
  const [oldestId, setOldestId] = useState<string>();
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const initialized = useRef(false);
  const sentinel = useRef<HTMLDivElement>(null);
  const pending = useRef(false);
  const request = useRef<AbortController | undefined>(undefined);
  const anchor = useRef<{ y: number; height: number } | undefined>(undefined);
  useEffect(() => () => request.current?.abort(), []);

  useEffect(() => {
    if (!incoming) return;
    if (!initialized.current) {
      initialized.current = true;
      setHasMore(incoming.length >= 100);
      // Preserve the server's cursor order, not the display timestamp order.
      // Delayed writes may carry an older createdAt than their insertion time.
      setOldestId(incoming.at(-1)?.id);
    }
    setMessages((previous) => mergeMessages(previous, incoming));
  }, [incoming]);

  useLayoutEffect(() => {
    if (!anchor.current) return;
    window.scrollTo(
      0,
      anchor.current.y +
        document.documentElement.scrollHeight -
        anchor.current.height,
    );
    anchor.current = undefined;
  }, [messages]);

  useEffect(() => {
    if (!hasMore || following || !sentinel.current || !oldestId) return;
    const observer = new IntersectionObserver(
      ([entry]) => {
        if (!entry.isIntersecting || pending.current || error) return;
        pending.current = true;
        const controller = new AbortController();
        request.current = controller;
        setLoading(true);
        void api
          .query<{ messages: Message[] }>(
            olderMessagesQuery,
            { id: agentId, beforeId: oldestId },
            controller.signal,
          )
          .then((page) => {
            if (controller.signal.aborted) return;
            anchor.current = {
              y: scrollY,
              height: document.documentElement.scrollHeight,
            };
            setMessages((previous) => mergeMessages(previous, page.messages));
            setOldestId(page.messages.at(-1)?.id);
            setHasMore(page.messages.length === 100);
          })
          .catch((failure) => {
            if (!controller.signal.aborted) setError(String(failure.message));
          })
          .finally(() => {
            if (!controller.signal.aborted) {
              pending.current = false;
              setLoading(false);
            }
          });
      },
      { rootMargin: "160px 0px 0px" },
    );
    observer.observe(sentinel.current);
    return () => observer.disconnect();
  }, [api, agentId, oldestId, hasMore, following, error]);

  return { messages, sentinel, loading, error, retry: () => setError("") };
}
