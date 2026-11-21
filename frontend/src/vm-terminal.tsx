import { Button, Modal } from "@heroui/react";
import { WTerm } from "@wterm/dom";
import { useEffect, useRef, useState } from "react";
import "@wterm/dom/css";

import { Icon } from "./icons";
import { useAPI } from "./api";
import { useI18n } from "./preferences";
import { RepositoryConnection } from "./repository-connection";
import {
  TerminalHeartbeat,
  TerminalInputQueue,
  terminalConnectionError,
  terminalHeartbeatInterval,
} from "./terminal-connection";
import {
  isTerminalStartupPhase,
  terminalStartupLabels,
  type TerminalStartupPhase,
} from "./terminal-startup";

type State = "starting" | "ready" | "closed" | "error";

function terminalURL(
  projectId: string,
  agentId: string,
  cols: number,
  rows: number,
) {
  const preview =
    (globalThis as typeof globalThis & { __ZENTRALE_PREVIEW_BASE__?: string })
      .__ZENTRALE_PREVIEW_BASE__ ?? "/";
  const url = new URL(
    `${preview.replace(/\/?$/, "/")}terminal/${encodeURIComponent(projectId)}`,
    location.origin,
  );
  url.protocol = location.protocol === "https:" ? "wss:" : "ws:";
  if (agentId) url.searchParams.set("agent", agentId);
  url.searchParams.set("cols", String(Math.min(500, Math.max(2, cols))));
  url.searchParams.set("rows", String(Math.min(300, Math.max(2, rows))));
  return url;
}

export default function VMTerminal({
  projectId,
  projectName,
  agentId,
  hasRepository,
  onClose,
}: {
  projectId: string;
  projectName: string;
  agentId: string;
  hasRepository: boolean;
  onClose: () => void;
}) {
  const { t, theme, locale } = useI18n();
  const api = useAPI();
  const host = useRef<HTMLDivElement>(null);
  const themeRef = useRef(theme);
  themeRef.current = theme;
  const inputLabel = useRef(t("Terminal-Eingabe"));
  inputLabel.current = t("Terminal-Eingabe");
  const [attempt, setAttempt] = useState(0);
  const [state, setState] = useState<State>("starting");
  const [startupPhase, setStartupPhase] =
    useState<TerminalStartupPhase>("starting");
  const [error, setError] = useState("");
  const [exitCode, setExitCode] = useState<number>();
  const [checkingRepository, setCheckingRepository] = useState(false);

  useEffect(() => {
    host.current?.classList.toggle("theme-light", theme === "light");
    host.current
      ?.querySelector("textarea")
      ?.setAttribute("aria-label", inputLabel.current);
  }, [theme, locale]);

  useEffect(() => {
    const element = host.current;
    if (!element) return;
    let disposed = false;
    let ready = false;
    let opened = false;
    let established = false;
    let finished = false;
    const sessionId = crypto.randomUUID();
    let cursor = 0;
    const input = new TerminalInputQueue();
    const diagnosis = new AbortController();
    let socket: WebSocket | undefined;
    let resizeFrame = 0;
    let heartbeatTimer: ReturnType<typeof setInterval> | undefined;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;
    let disconnectedAt: number | undefined;
    let retryDelay = 500;
    const heartbeat = new TerminalHeartbeat(performance.now());
    const stopHeartbeat = () => clearInterval(heartbeatTimer);
    const encoder = new TextEncoder();
    const fail = (message: string) => {
      if (disposed || finished) return;
      finished = true;
      ready = false;
      stopHeartbeat();
      clearTimeout(retryTimer);
      setError(message);
      setState("error");
      socket?.close();
    };
    const reconnect = () => {
      if (disposed || finished || retryTimer) return;
      ready = false;
      stopHeartbeat();
      disconnectedAt ??= performance.now();
      if (performance.now() - disconnectedAt >= 14 * 60 * 1000) {
        fail("Terminal-Verbindung wurde unerwartet unterbrochen.");
        return;
      }
      setState("starting");
      setStartupPhase("connecting");
      retryTimer = setTimeout(() => {
        retryTimer = undefined;
        connect();
      }, retryDelay);
      retryDelay = Math.min(5000, retryDelay * 2);
    };
    const probe = () => {
      if (disposed || finished || socket?.readyState !== WebSocket.OPEN) return;
      if (!heartbeat.check(performance.now(), document.hidden)) {
        socket.close();
        reconnect();
        return;
      }
      try {
        socket.send(JSON.stringify({ type: "ping" }));
      } catch {
        socket.close();
        reconnect();
      }
    };
    const resume = () => {
      if (document.hidden) return;
      heartbeat.acknowledge(performance.now());
      probe();
    };
    document.addEventListener("visibilitychange", resume);
    window.addEventListener("pageshow", resume);
    const sendPending = () => {
      const current = socket;
      if (!ready || current?.readyState !== WebSocket.OPEN) return;
      try {
        input.send((frame) => current.send(frame));
      } catch {
        current.close();
        reconnect();
      }
    };
    const send = (frame: Record<string, unknown>) => {
      if (!established || disposed || finished) return;
      if (!input.enqueue(frame)) {
        fail("Zu viele ausstehende Terminal-Eingaben. Verbindung beendet.");
        return;
      }
      sendPending();
    };
    const sendBytes = (bytes: Uint8Array) => {
      for (
        let offset = 0;
        !finished && offset < bytes.byteLength;
        offset += 16384
      ) {
        const chunk = bytes.subarray(offset, offset + 16384);
        send({
          type: "input",
          encoding: "base64",
          data: btoa(String.fromCharCode(...chunk)),
        });
      }
    };
    const sendResize = () => {
      if (!ready || disposed) return;
      send({
        type: "resize",
        cols: Math.min(500, Math.max(2, terminal.cols)),
        rows: Math.min(300, Math.max(2, terminal.rows)),
      });
    };
    const terminal = new WTerm(element, {
      autoResize: true,
      scrollbackLines: 2000,
      webmcp: false,
      onData: (data) => sendBytes(encoder.encode(data)),
      onBinary: sendBytes,
      onResize: () => {
        cancelAnimationFrame(resizeFrame);
        resizeFrame = requestAnimationFrame(sendResize);
      },
    });
    const connect = () => {
      if (disposed || finished) return;
      const url = terminalURL(projectId, agentId, terminal.cols, terminal.rows);
      url.searchParams.set("session", sessionId);
      url.searchParams.set("cursor", String(cursor));
      const current = new WebSocket(url);
      socket = current;
      current.binaryType = "arraybuffer";
      current.onopen = () => {
        if (disposed || current !== socket) return;
        opened = true;
        heartbeat.acknowledge(performance.now());
        stopHeartbeat();
        heartbeatTimer = setInterval(probe, terminalHeartbeatInterval);
        probe();
      };
      current.onmessage = (event: MessageEvent<string | ArrayBuffer>) => {
        if (disposed || finished || current !== socket) return;
        if (event.data instanceof ArrayBuffer) {
          cursor += event.data.byteLength;
          terminal.write(new Uint8Array(event.data));
          return;
        }
        try {
          const message = JSON.parse(event.data);
          if (message.type === "pong") {
            heartbeat.acknowledge(performance.now());
          } else if (message.type === "ack") {
            if (!input.acknowledge(message.sequence)) {
              fail("Ungültige Antwort der Terminal-Verbindung.");
              return;
            }
            sendPending();
          } else if (message.type === "status" && message.status === "ready") {
            established = true;
            ready = true;
            disconnectedAt = undefined;
            retryDelay = 500;
            setState("ready");
            input.resume();
            sendPending();
            terminal.fit();
            sendResize();
            terminal.focus();
          } else if (
            message.type === "status" &&
            !ready &&
            isTerminalStartupPhase(message.status)
          ) {
            setStartupPhase(message.status);
          } else if (message.type === "error") {
            fail(
              typeof message.message === "string"
                ? message.message
                : "Terminal-Verbindung fehlgeschlagen.",
            );
          } else if (message.type === "exit") {
            if (!Number.isInteger(message.code)) {
              fail("Terminal-Verbindung wurde unerwartet unterbrochen.");
              return;
            }
            finished = true;
            ready = false;
            stopHeartbeat();
            setExitCode(message.code);
            setState("closed");
            current.close();
          }
        } catch {
          fail("Ungültige Antwort der Terminal-Verbindung.");
        }
      };
      current.onerror = () => {
        if (disposed || finished || current !== socket) return;
        if (opened) {
          current.close();
          reconnect();
          return;
        }
        fail("Terminal-Verbindung fehlgeschlagen.");
        void terminalConnectionError(url, diagnosis.signal, api.fetch).then(
          (message) => {
            if (!disposed) setError(message);
          },
        );
      };
      current.onclose = () => {
        if (!disposed && !finished && current === socket) reconnect();
      };
    };
    element.classList.toggle("theme-light", themeRef.current === "light");
    setState("starting");
    setStartupPhase("starting");
    setError("");
    setExitCode(undefined);
    void terminal
      .init()
      .then(() => {
        if (disposed) return;
        element
          .querySelector("textarea")
          ?.setAttribute("aria-label", inputLabel.current);
        terminal.fit();
        connect();
      })
      .catch(() => fail("Terminal konnte nicht initialisiert werden."));
    return () => {
      disposed = true;
      diagnosis.abort();
      stopHeartbeat();
      clearTimeout(retryTimer);
      document.removeEventListener("visibilitychange", resume);
      window.removeEventListener("pageshow", resume);
      ready = false;
      cancelAnimationFrame(resizeFrame);
      if (socket) {
        if (socket.readyState === WebSocket.OPEN)
          socket.send(JSON.stringify({ type: "close" }));
        socket.onopen = null;
        socket.onmessage = null;
        socket.onerror = null;
        socket.onclose = null;
        socket.close();
      }
      terminal.destroy();
    };
  }, [api, projectId, agentId, attempt]);

  const status =
    state === "starting"
      ? terminalStartupLabels[startupPhase]
      : state === "ready"
        ? "Verbunden"
        : state === "error"
          ? "Verbindung fehlgeschlagen"
          : "Terminal-Verbindung beendet";
  return (
    <Modal
      isOpen
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <Modal.Backdrop isDismissable={false} isKeyboardDismissDisabled>
        <Modal.Container size="full" scroll="inside">
          <Modal.Dialog className="vm-terminal-dialog">
            <div
              className="flex h-full min-h-0 flex-col"
              onKeyDownCapture={(event) => {
                if (event.altKey && event.shiftKey && event.code === "KeyT") {
                  event.preventDefault();
                  event.stopPropagation();
                  onClose();
                }
              }}
            >
              <Modal.Header className="flex shrink-0 flex-row flex-wrap items-center gap-3 border-b border-border p-3">
                <Icon name="terminal" className="size-5" />
                <div className="min-w-0 flex-1">
                  <Modal.Heading>{t("VM-Terminal")}</Modal.Heading>
                  <p className="truncate text-xs text-muted">{projectName}</p>
                </div>
                <p role="status" className="text-xs text-muted">
                  {t(status)}
                  {exitCode !== undefined && ` · ${t("Exit-Code")} ${exitCode}`}
                </p>
                {(state === "closed" || state === "error") && (
                  <Button
                    variant="secondary"
                    className="min-h-11"
                    onPress={() => setAttempt((value) => value + 1)}
                  >
                    {t("Erneut verbinden")}
                  </Button>
                )}
                {agentId && hasRepository && (
                  <Button
                    variant="ghost"
                    className="min-h-11"
                    onPress={() => setCheckingRepository(true)}
                  >
                    {t("Git-Verbindung prüfen")}
                  </Button>
                )}
                <Button
                  variant="ghost"
                  className="min-h-11"
                  aria-keyshortcuts="Alt+Shift+T"
                  onPress={onClose}
                >
                  {t("Schließen")}
                </Button>
              </Modal.Header>
              {error && (
                <p
                  role="alert"
                  className="shrink-0 break-words bg-danger-soft p-3 text-sm text-danger-soft-foreground"
                >
                  {t(error)}
                </p>
              )}
              {(state === "closed" || state === "error") && (
                <p className="shrink-0 px-3 py-2 text-xs text-muted">
                  {t(
                    "Erneut verbinden startet eine neue Shell. Eingaben werden nicht wiederholt.",
                  )}
                </p>
              )}
              <div className="relative min-h-0 flex-1 overflow-hidden">
                <div
                  ref={host}
                  role="group"
                  aria-label={t("Terminal-Ein- und Ausgabe")}
                  className="vm-terminal-screen"
                />
                {state === "starting" && (
                  <div
                    className="absolute inset-0 flex items-center justify-center bg-surface/80 p-4 text-sm"
                    aria-hidden="true"
                  >
                    {t(terminalStartupLabels[startupPhase])}
                  </div>
                )}
              </div>
              <p className="shrink-0 border-t border-border px-3 py-2 text-xs text-muted">
                {t(
                  "Esc bleibt im Terminal. Alt+Umschalt+T oder Schließen beendet diese Sitzung. Die VM wird automatisch gestoppt, wenn sie nicht mehr genutzt wird.",
                )}
              </p>
              {checkingRepository && (
                <RepositoryConnection
                  agentId={agentId}
                  onClose={() => setCheckingRepository(false)}
                />
              )}
            </div>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
