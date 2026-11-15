import { Button, Label, TextArea, TextField } from "@heroui/react";
import { useEffect, useRef, useState } from "react";

import { useAPI } from "./api";
import { MessageMarkdown } from "./message-markdown";
import { useI18n } from "./preferences";
import { SESSION_EXPIRED } from "./session";
import { Failure, Inspect } from "./ui";

type Exchange = { id: string; question: string; answer: string };
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
  const end = useRef<HTMLDivElement>(null);

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

    const id = `q${++serial.current}`;
    activeQuestion.current = id;
    setBusy(true);
    setError("");
    setExchanges((previous) => [
      ...previous,
      { id, question: text, answer: "" },
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

  useEffect(() => {
    end.current?.scrollIntoView({ block: "nearest" });
  }, [exchanges]);

  return (
    <Inspect title={t("Nebenfrage (/side)")} onClose={onClose}>
      <div className="space-y-4">
        <p className="text-sm text-muted">
          {t(
            "Liest den Hauptthread und seine Unteragenten, ohne die Hauptarbeit zu verändern. Beim Schließen werden Unterhaltung und Agent beendet. Die Nutzung wird dem Hauptagenten zugerechnet.",
          )}
        </p>
        <div
          className="max-h-[45vh] space-y-5 overflow-y-auto"
          aria-label={t("Nebenunterhaltung")}
          aria-live="polite"
        >
          {exchanges.map((exchange) => (
            <div key={exchange.id} className="space-y-2">
              <p className="whitespace-pre-wrap rounded-lg bg-surface-secondary p-3 text-sm">
                {exchange.question}
              </p>
              {exchange.answer && <MessageMarkdown content={exchange.answer} />}
            </div>
          ))}
          <div ref={end} />
        </div>
        <Failure message={error} />
        <p role="status" className="text-xs text-muted">
          {state === "connecting"
            ? t("Nebenunterhaltung wird geöffnet …")
            : state === "closed"
              ? t(
                  "Nebenunterhaltung beendet. Für weitere Fragen das Modal neu öffnen.",
                )
              : busy
                ? t("Antwort wird erarbeitet …")
                : t("Nur lesender Zugriff · temporär")}
        </p>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            event.stopPropagation();
            submit(question);
          }}
          className="space-y-3"
        >
          <TextField
            value={question}
            onChange={setQuestion}
            isDisabled={state !== "ready" || busy}
            fullWidth
          >
            <Label>{t("Kurze Frage zum Hauptthread")}</Label>
            <TextArea
              autoFocus
              rows={2}
              className="min-h-20"
              onKeyDown={(event) => {
                if (
                  event.key === "Enter" &&
                  !event.shiftKey &&
                  !event.nativeEvent.isComposing
                ) {
                  event.preventDefault();
                  event.stopPropagation();
                  submit(question);
                }
              }}
            />
          </TextField>
          <Button
            type="submit"
            isDisabled={state !== "ready" || busy || !question.trim()}
          >
            {t("Nebenfrage senden")}
          </Button>
        </form>
      </div>
    </Inspect>
  );
}
