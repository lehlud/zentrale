import { Button, Modal } from "@heroui/react";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";

import { AgentConversation } from "./agent-conversation";
import { useAPI } from "./api";
import { ChatInput } from "./chat-input";
import type { Message } from "./generated";
import { Icon } from "./icons";
import { useI18n } from "./preferences";
import { SESSION_EXPIRED } from "./session";
import { Failure } from "./ui";
import { WorkingIndicator } from "./working-indicator";

type Exchange = {
  id: string;
  question: string;
  answer: string;
  createdAt: string;
};
type State = "connecting" | "ready" | "closed";

function sideURL(agentId: string) {
  const preview =
    (globalThis as typeof globalThis & { __ZENTRALE_PREVIEW_BASE__?: string })
      .__ZENTRALE_PREVIEW_BASE__ ?? "/";
  const url = new URL(
    `${preview.replace(/\/?$/, "/")}side/${encodeURIComponent(agentId)}`,
    location.origin,
  );
  url.protocol = location.protocol === "https:" ? "wss:" : "ws:";
  return url;
}

// The modal owns the socket and all its messages. No draft, input receipt or
// transcript entry survives closing, and reconnect never replays a question.
export function SideConversation({
  agentId,
  initialQuestion,
  onClose,
}: {
  agentId: string;
  initialQuestion: string;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const api = useAPI();
  const translator = useRef(t);
  translator.current = t;
  const [state, setState] = useState<State>("connecting");
  const [question, setQuestion] = useState("");
  const [exchanges, setExchanges] = useState<Exchange[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const socket = useRef<WebSocket | null>(null);
  const activeQuestion = useRef<string | null>(null);
  const serial = useRef(0);
  const initial = useRef(initialQuestion);
  const scroll = useRef<HTMLDivElement>(null);
  const transcript = useRef<HTMLDivElement>(null);
  const followsOutput = useRef(true);
  const [following, setFollowing] = useState(true);
  const messages = useMemo(
    () =>
      exchanges.flatMap(({ id, question, answer, createdAt }) => {
        const message = (
          role: string,
          content: string,
          index: number,
        ): Message => ({
          id: `side-${id}-${index}`,
          lineageId: "",
          agentId,
          role,
          content,
          createdAt,
          activities: [],
          attachments: [],
          callId: "",
          change: null,
          changes: [],
          toolName: "",
          wire: "",
        });
        return answer
          ? [message("user", question, 0), message("assistant", answer, 1)]
          : [message("user", question, 0)];
      }),
    [agentId, exchanges],
  );
  const latest = () => {
    followsOutput.current = true;
    setFollowing(true);
    if (scroll.current) scroll.current.scrollTop = scroll.current.scrollHeight;
  };

  const submit = (text: string) => {
    if (
      !text.trim() ||
      activeQuestion.current ||
      socket.current?.readyState !== WebSocket.OPEN
    )
      return;

    if (new TextEncoder().encode(text).length > 16000) {
      setError(t("Die Nebenfrage darf höchstens 16000 Bytes lang sein."));
      return;
    }

    const id = `q${String(++serial.current).padStart(10, "0")}`;
    latest();
    activeQuestion.current = id;
    setBusy(true);
    setError("");
    setExchanges((previous) => [
      ...previous,
      { id, question: text, answer: "", createdAt: new Date().toISOString() },
    ]);
    socket.current.send(JSON.stringify({ type: "question", id, text }));
    setQuestion("");
  };
  const submitRef = useRef(submit);
  submitRef.current = submit;

  useEffect(() => {
    let active = true;
    let opened = false;
    const connection = new WebSocket(sideURL(agentId));
    const diagnosis = new AbortController();
    socket.current = connection;

    connection.onopen = () => {
      opened = true;
    };
    connection.onmessage = (event) => {
      if (!active) return;

      let message: {
        type?: string;
        id?: string;
        text?: string;
        error?: string;
      };
      try {
        message = JSON.parse(String(event.data));
      } catch {
        setError(
          translator.current("Ungültige Antwort der Nebenunterhaltung."),
        );
        return;
      }

      if (message.type === "ready") {
        setState("ready");
        if (initial.current) {
          const text = initial.current;
          initial.current = "";
          submitRef.current(text);
        }
        return;
      }

      if (message.id && message.id !== activeQuestion.current) return;

      if (
        message.type === "delta" ||
        message.type === "reset" ||
        message.type === "done"
      ) {
        const type = message.type;
        const text = typeof message.text === "string" ? message.text : "";
        setExchanges((previous) =>
          previous.map((exchange) =>
            exchange.id === message.id
              ? {
                  ...exchange,
                  answer: type === "delta" ? exchange.answer + text : text,
                }
              : exchange,
          ),
        );
      }

      if (message.type === "error")
        setError(
          typeof message.error === "string"
            ? message.error
            : translator.current("Nebenfrage fehlgeschlagen."),
        );

      if (message.type === "done" || message.type === "error") {
        activeQuestion.current = null;
        setBusy(false);
      }
    };
    connection.onclose = (event) => {
      if (!active) return;
      setState("closed");
      setBusy(false);
      activeQuestion.current = null;

      if (event.code === SESSION_EXPIRED || !opened) {
        const url = sideURL(agentId);
        url.protocol = location.protocol;
        const timer = setTimeout(() => diagnosis.abort(), 5000);
        void api
          .fetch(url, { method: "HEAD", signal: diagnosis.signal })
          .catch(() => {})
          .finally(() => clearTimeout(timer));
      }
    };

    const pageHide = () => {
      setState("closed");
      setBusy(false);
      active = false;
      connection.close();
    };
    addEventListener("pagehide", pageHide);

    return () => {
      active = false;
      diagnosis.abort();
      removeEventListener("pagehide", pageHide);
      connection.onopen = null;
      connection.onmessage = null;
      connection.onclose = null;
      connection.close();
      socket.current = null;
      activeQuestion.current = null;
    };
  }, [agentId, api]);

  useLayoutEffect(() => {
    const container = scroll.current;
    const content = transcript.current;
    if (!container || !content) return;
    const observer = new ResizeObserver(() => {
      if (followsOutput.current) container.scrollTop = container.scrollHeight;
    });
    observer.observe(container);
    observer.observe(content);
    return () => observer.disconnect();
  }, []);

  return (
    <Modal
      isOpen
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <Modal.Backdrop isDismissable={false}>
        <Modal.Container size="full" scroll="inside">
          <Modal.Dialog className="flex h-dvh max-h-dvh flex-col overflow-hidden bg-surface p-0">
            <Modal.Header className="shrink-0 border-b border-border px-4 py-3 sm:px-6 lg:px-8">
              <div className="mx-auto flex w-full max-w-[96rem] items-center justify-between gap-3">
                <div className="min-w-0">
                  <Modal.Heading>{t("Nebenfrage (/side)")}</Modal.Heading>
                  <p className="text-xs text-muted">
                    {t("Nur lesender Zugriff · temporär")}
                  </p>
                </div>
                <Button variant="ghost" className="min-h-11" onPress={onClose}>
                  {t("Schließen")}
                </Button>
              </div>
            </Modal.Header>
            <div
              ref={scroll}
              role="region"
              aria-label={t("Nebenunterhaltung")}
              tabIndex={0}
              className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-4 sm:px-6 lg:px-8"
              onScroll={(event) => {
                const { scrollHeight, scrollTop, clientHeight } =
                  event.currentTarget;
                const atEnd = scrollHeight - scrollTop - clientHeight <= 2;
                followsOutput.current = atEnd;
                setFollowing(atEnd);
              }}
            >
              <div
                ref={transcript}
                className="mx-auto min-w-0 max-w-[96rem] py-6 sm:py-8"
              >
                {messages.length > 0 ? (
                  <AgentConversation messages={messages} working={busy} />
                ) : (
                  state === "ready" && (
                    <div className="flex min-h-[calc(100dvh-19rem)] flex-col items-center justify-center px-3 py-8 text-center">
                      <h2 className="text-2xl font-medium tracking-tight">
                        {t("Was möchtest du zum Hauptthread wissen?")}
                      </h2>
                    </div>
                  )
                )}
                {busy && (
                  <WorkingIndicator
                    label={t(
                      exchanges.at(-1)?.answer
                        ? "Schreibt …"
                        : "Antwort wird vorbereitet …",
                    )}
                  />
                )}
                {state === "connecting" && (
                  <WorkingIndicator
                    label={t("Nebenunterhaltung wird geöffnet …")}
                  />
                )}
                {state === "closed" && (
                  <p role="status" className="text-sm text-muted">
                    {t(
                      "Nebenunterhaltung beendet. Für weitere Fragen das Modal neu öffnen.",
                    )}
                  </p>
                )}
              </div>
            </div>
            <form
              onSubmit={(event) => {
                event.preventDefault();
                event.stopPropagation();
                submit(question);
              }}
              className="shrink-0 bg-surface px-4 pt-4 pb-[max(0.75rem,env(safe-area-inset-bottom))] sm:px-6 lg:px-8"
            >
              <div className="relative mx-auto max-w-[96rem]">
                {!following && (
                  <Button
                    isIconOnly
                    size="sm"
                    aria-label={t("Zu neuesten Einträgen ↓")}
                    className="absolute -top-10 right-1.5 size-8 min-h-8 min-w-8 rounded-full border border-border bg-surface text-muted shadow-none"
                    variant="secondary"
                    onPress={latest}
                  >
                    <Icon name="down" className="size-3.5" />
                  </Button>
                )}
                <Failure message={error} />
                <div className="flex items-end gap-3">
                  <ChatInput
                    autoFocus
                    label={t("Frage zum Hauptthread")}
                    placeholder={t("Was möchtest du wissen?")}
                    value={question}
                    onChange={setQuestion}
                    isDisabled={state === "closed"}
                    onKeyDown={(event) => {
                      if (
                        event.key === "Enter" &&
                        !event.shiftKey &&
                        !event.nativeEvent.isComposing
                      ) {
                        event.preventDefault();
                        submit(question);
                      }
                    }}
                  />
                  <Button
                    isIconOnly
                    type="submit"
                    className="min-h-11 min-w-11 shrink-0"
                    aria-label={t("Nebenfrage senden")}
                    isDisabled={state !== "ready" || busy || !question.trim()}
                  >
                    <Icon name="arrow" />
                  </Button>
                </div>
              </div>
            </form>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
