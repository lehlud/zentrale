import { Button, Modal } from "@heroui/react";
import { WTerm } from "@wterm/dom";
import { useEffect, useRef, useState } from "react";
import "@wterm/dom/css";

import { Icon } from "./icons";
import { useI18n } from "./preferences";
import { terminalConnectionError } from "./terminal-connection";
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
  onClose,
}: {
  projectId: string;
  projectName: string;
  agentId: string;
  onClose: () => void;
}) {
  const { t, theme, locale } = useI18n();
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
    let finished = false;
    const diagnosis = new AbortController();
    let socket: WebSocket | undefined;
    let resizeFrame = 0;
    const encoder = new TextEncoder();
    const fail = (message: string) => {
      if (disposed || finished) return;
      finished = true;
      ready = false;
      setError(message);
      setState("error");
      socket?.close();
    };
    const sendBytes = (bytes: Uint8Array) => {
      if (!ready || socket?.readyState !== WebSocket.OPEN) return;
      // Bound each frame, including large paste input and raw mouse reports.
      if (socket.bufferedAmount + bytes.byteLength > 1024 * 1024) {
        fail("Zu viele ausstehende Terminal-Eingaben. Verbindung beendet.");
        return;
      }
      for (let offset = 0; offset < bytes.byteLength; offset += 16384) {
        socket.send(bytes.slice(offset, offset + 16384));
      }
    };
    const sendResize = () => {
      if (disposed || !ready || socket?.readyState !== WebSocket.OPEN) return;
      socket.send(
        JSON.stringify({
          type: "resize",
          cols: Math.min(500, Math.max(2, terminal.cols)),
          rows: Math.min(300, Math.max(2, terminal.rows)),
        }),
      );
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
        const url = terminalURL(
          projectId,
          agentId,
          terminal.cols,
          terminal.rows,
        );
        socket = new WebSocket(url);
        socket.binaryType = "arraybuffer";
        socket.onmessage = (event: MessageEvent<string | ArrayBuffer>) => {
          if (disposed || finished) return;
          if (event.data instanceof ArrayBuffer) {
            terminal.write(new Uint8Array(event.data));
            return;
          }
          try {
            const message = JSON.parse(event.data);
            if (message.type === "status" && message.status === "ready") {
              ready = true;
              setState("ready");
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
              finished = true;
              ready = false;
              if (typeof message.code === "number") setExitCode(message.code);
              setState("closed");
              socket?.close();
            }
          } catch {
            fail("Ungültige Antwort der Terminal-Verbindung.");
          }
        };
        socket.onerror = () => {
          if (disposed || finished) return;
          fail("Terminal-Verbindung fehlgeschlagen.");
          void terminalConnectionError(url, diagnosis.signal).then(
            (message) => {
              if (!disposed) setError(message);
            },
          );
        };
        socket.onclose = () => {
          if (!disposed && !finished) {
            finished = true;
            ready = false;
            setState("closed");
          }
        };
      })
      .catch(() => fail("Terminal konnte nicht initialisiert werden."));
    return () => {
      disposed = true;
      diagnosis.abort();
      ready = false;
      cancelAnimationFrame(resizeFrame);
      if (socket) {
        socket.onmessage = null;
        socket.onerror = null;
        socket.onclose = null;
        socket.close();
      }
      terminal.destroy();
    };
  }, [projectId, agentId, attempt]);

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
            </div>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
