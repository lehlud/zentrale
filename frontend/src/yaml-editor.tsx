import { Description, Label, TextArea } from "@heroui/react";
import { useEffect, useId, useRef, useState } from "react";
import type { editor } from "monaco-editor/editor/editor.api";

import { useI18n } from "./preferences";
import { loadMonaco } from "./load-monaco";

type YamlEditorProps = {
  value: string;
  onChange: (value: string) => void;
  label: string;
  description?: string;
  error?: string;
  required?: boolean;
  disabled?: boolean;
};

export function YamlEditor(props: YamlEditorProps) {
  const { locale, theme, t } = useI18n();
  const id = useId();
  const host = useRef<HTMLDivElement>(null);
  const fallback = useRef<HTMLTextAreaElement>(null);
  const instance = useRef<editor.IStandaloneCodeEditor | undefined>(undefined);
  const runtime = useRef<typeof import("./monaco-runtime") | undefined>(
    undefined,
  );
  const latest = useRef({ ...props, theme, locale });
  latest.current = { ...props, theme, locale };
  const [ready, setReady] = useState(false);
  const [failed, setFailed] = useState(false);
  const descriptionID = props.description ? `${id}-description` : undefined;
  const errorID = props.error ? `${id}-error` : undefined;
  const describedBy = [descriptionID, errorID].filter(Boolean).join(" ");

  useEffect(() => {
    let disposed = false;
    let changeSubscription: { dispose(): void } | undefined;
    let codeEditor: editor.IStandaloneCodeEditor | undefined;
    // Keep the ordinary input usable during loading and after a failed import.
    // Read the latest draft when loading finishes, including edits made meanwhile.
    loadMonaco(latest.current.locale)
      .then((module) => {
        if (disposed || !host.current) {
          return;
        }

        runtime.current = module;
        const current = latest.current;
        const restoreFocus = document.activeElement === fallback.current;
        codeEditor = module.monaco.editor.create(host.current, {
          value: current.value,
          language: "yaml",
          theme: current.theme === "dark" ? "vs-dark" : "vs",
          ariaLabel: current.label,
          ariaRequired: current.required,
          readOnly: current.disabled,
          automaticLayout: true,
          minimap: { enabled: false },
          fontSize: 14,
          lineHeight: 22,
          tabSize: 2,
          insertSpaces: true,
          scrollBeyondLastLine: false,
          wordWrap: "on",
          padding: { top: 12, bottom: 12 },
          lineNumbersMinChars: 3,
          folding: true,
          tabFocusMode: true,
          editContext: false,
          contextmenu: false,
          scrollbar: { alwaysConsumeMouseWheel: false },
        });
        instance.current = codeEditor;
        changeSubscription = codeEditor.onDidChangeModelContent(() => {
          latest.current.onChange(codeEditor!.getValue());
        });
        setReady(true);
        if (restoreFocus) codeEditor.focus();
      })
      .catch(() => {
        if (!disposed) setFailed(true);
      });

    return () => {
      disposed = true;
      changeSubscription?.dispose();
      const model = codeEditor?.getModel();
      codeEditor?.dispose();
      model?.dispose();
      instance.current = undefined;
    };
  }, []);

  useEffect(() => {
    const codeEditor = instance.current;
    if (!ready || !codeEditor) {
      return;
    }

    if (codeEditor.getValue() !== props.value) codeEditor.setValue(props.value);

    codeEditor.updateOptions({
      readOnly: props.disabled,
      ariaLabel: props.label,
      ariaRequired: props.required,
    });
    runtime.current?.monaco.editor.setTheme(
      theme === "dark" ? "vs-dark" : "vs",
    );
    const input = codeEditor.getDomNode()?.querySelector("textarea");
    if (input) {
      input.id = `${id}-input`;
      input.setAttribute("aria-describedby", describedBy);
      input.setAttribute("aria-invalid", String(Boolean(props.error)));
    }
  }, [
    ready,
    props.value,
    props.disabled,
    props.label,
    props.required,
    props.error,
    theme,
    id,
    describedBy,
  ]);

  return (
    <div className="flex min-w-0 flex-col gap-2">
      <Label htmlFor={`${id}-input`}>
        {props.label}
        {props.required && (
          <span aria-hidden="true" className="text-danger">
            {" "}
            *
          </span>
        )}
      </Label>
      <div
        className={`relative h-[min(26rem,45dvh)] min-h-48 min-w-0 overflow-hidden rounded-xl border focus-within:ring-2 focus-within:ring-accent ${props.error ? "border-danger" : "border-[var(--field-border)]"}`}
      >
        <div ref={host} className="h-full w-full" />
        {!ready && (
          <TextArea
            ref={fallback}
            id={`${id}-input`}
            aria-label={props.label}
            aria-required={props.required}
            aria-describedby={describedBy}
            aria-invalid={Boolean(props.error)}
            disabled={props.disabled}
            value={props.value}
            onChange={(event) => props.onChange(event.target.value)}
            className="absolute inset-0 h-full w-full resize-none font-mono"
          />
        )}
      </div>
      {!ready && (
        <p role="status" className="text-sm text-muted">
          {t(
            failed
              ? "Der Code-Editor konnte nicht geladen werden. YAML kann weiterhin im Textfeld bearbeitet werden."
              : "Code-Editor wird geladen …",
          )}
        </p>
      )}
      {props.description && (
        <Description id={descriptionID}>{props.description}</Description>
      )}
      {props.error && (
        <p id={errorID} role="alert" className="text-sm text-danger">
          {props.error}
        </p>
      )}
    </div>
  );
}
