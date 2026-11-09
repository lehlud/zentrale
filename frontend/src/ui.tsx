import {
  Accordion,
  Alert,
  Button,
  Card,
  Checkbox,
  Description,
  FieldError,
  Input,
  Label,
  ListBox,
  Modal,
  Select,
  Spinner,
  TextArea,
  TextField,
  type ModalContainerProps,
} from "@heroui/react";
import type { FormEvent, ReactNode } from "react";
import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { useAPI } from "./api";
import { connectionErrorMessage } from "./connection";
import { fieldForServerError, validateFields } from "./form-validation";
import { FlowProgress } from "./form-flow";
import { useI18n } from "./preferences";
import { YamlEditor } from "./yaml-editor";

export function Failure({
  message,
  onRetry,
}: {
  message?: string;
  onRetry?: () => void;
}) {
  const { t } = useI18n();
  // Transport failures are announced once by Notifications, across all routes.
  if (!message || message === connectionErrorMessage) return null;
  return (
    <Alert role="alert" status="danger" className="min-w-0">
      <Alert.Indicator />
      <Alert.Content className="min-w-0">
        <Alert.Title className="whitespace-pre-wrap break-words">
          {t(message)}
        </Alert.Title>
        {onRetry && (
          <Button className="mt-2 min-h-11" variant="ghost" onPress={onRetry}>
            {t("Erneut versuchen")}
          </Button>
        )}
      </Alert.Content>
    </Alert>
  );
}

export function Loading() {
  const { t } = useI18n();
  const api = useAPI();
  const interrupted = useSyncExternalStore(
    api.connection.subscribe,
    api.connection.snapshot,
  );

  if (interrupted)
    return (
      <div className="p-12 text-center text-sm text-muted">
        {t("Warten auf Verbindung …")}
      </div>
    );

  return (
    <div className="flex min-h-40 items-center justify-center gap-3 px-6 py-12 text-sm text-muted">
      <Spinner aria-label={t("Laden")} size="sm" />
      <span aria-hidden="true">{t("Laden")} …</span>
    </div>
  );
}

export function Empty({
  children,
  action,
}: {
  children: ReactNode;
  action?: ReactNode;
}) {
  return (
    <Card variant="secondary">
      <Card.Content className="items-center gap-4 px-6 py-12 text-center text-sm leading-6 text-muted">
        <div>{children}</div>
        {action}
      </Card.Content>
    </Card>
  );
}

export function Title({
  children,
  action,
  description,
}: {
  children: ReactNode;
  action?: ReactNode;
  description?: ReactNode;
}) {
  return (
    <header className="mb-7 flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-2 pb-2">
      <h1 className="min-w-0 flex-1 basis-36 break-words text-2xl font-medium">
        {children}
      </h1>
      {action && (
        <div className="flex max-w-full flex-wrap items-center gap-2 [&_button]:max-w-full [&_button]:whitespace-normal">
          {action}
        </div>
      )}
      {description && (
        <div className="basis-full">
          <p className="max-w-2xl text-sm leading-6 text-muted">
            {description}
          </p>
        </div>
      )}
    </header>
  );
}

export type Field = {
  key: string;
  label: string;
  type?:
    | "password"
    | "email"
    | "textarea"
    | "yaml"
    | "number"
    | "check"
    | "select"
    | "multi";
  options?: { key: string; label: string }[];
  required?: boolean;
  placeholder?: string;
  description?: string;
};

function EditorField({
  field,
  value,
  onChange,
  error,
  disabled = false,
}: {
  field: Field;
  value: unknown;
  onChange: (value: unknown) => void;
  error?: string;
  disabled?: boolean;
}) {
  const { t } = useI18n();
  const validation = {
    name: field.key,
    isInvalid: Boolean(error),
    isRequired: field.required,
    isDisabled: disabled,
  };
  if (field.type === "yaml") {
    return (
      <YamlEditor
        value={String(value ?? "")}
        onChange={onChange}
        label={t(field.label)}
        description={field.description ? t(field.description) : undefined}
        error={error}
        required={field.required}
        disabled={disabled}
      />
    );
  }
  if (field.type === "check") {
    return (
      <Checkbox
        className="min-h-11"
        isDisabled={disabled}
        isSelected={Boolean(value)}
        onChange={onChange}
      >
        <Checkbox.Content>
          <Checkbox.Control>
            <Checkbox.Indicator />
          </Checkbox.Control>
          <Label>{t(field.label)}</Label>
        </Checkbox.Content>
      </Checkbox>
    );
  }
  if (field.type === "select" || field.type === "multi") {
    const multiple = field.type === "multi";
    const selected = multiple
      ? ((value as string[]) ?? [])
      : value
        ? String(value)
        : null;
    return (
      <Select<object, "single" | "multiple">
        className="min-w-0 gap-2"
        {...validation}
        selectionMode={multiple ? "multiple" : "single"}
        value={selected}
        onChange={(selection) =>
          onChange(
            multiple
              ? Array.isArray(selection)
                ? selection.map(String)
                : []
              : selection == null
                ? ""
                : String(selection),
          )
        }
      >
        <Label>{t(field.label)}</Label>
        <Select.Trigger className="min-h-11">
          <Select.Value>
            {({ selectedText, isPlaceholder }) =>
              isPlaceholder ? t("Auswählen") : selectedText
            }
          </Select.Value>
          <Select.Indicator />
        </Select.Trigger>
        <Select.Popover>
          <ListBox>
            {(field.options ?? []).map((option) => (
              <ListBox.Item
                key={option.key}
                id={option.key}
                textValue={t(option.label)}
                className="min-h-11"
              >
                <Label>{t(option.label)}</Label>
                <ListBox.ItemIndicator />
              </ListBox.Item>
            ))}
          </ListBox>
        </Select.Popover>
        {field.description && <Description>{t(field.description)}</Description>}
        <FieldError>{error}</FieldError>
      </Select>
    );
  }
  return (
    <TextField
      className="min-w-0 gap-2"
      {...validation}
      value={String(value ?? "")}
      onChange={(text) =>
        onChange(field.type === "number" ? Number(text) : text)
      }
      type={field.type === "textarea" ? "text" : (field.type ?? "text")}
      autoComplete={field.type === "password" ? "new-password" : "off"}
    >
      <Label>{t(field.label)}</Label>
      {field.type === "textarea" ? (
        <TextArea
          className="min-h-36 resize-y"
          rows={6}
          placeholder={field.placeholder}
        />
      ) : (
        <Input
          className="min-h-11"
          step={field.type === "number" ? "any" : undefined}
          placeholder={field.placeholder}
        />
      )}
      {field.description && <Description>{t(field.description)}</Description>}
      <FieldError>{error}</FieldError>
    </TextField>
  );
}

export type EditorStep = {
  id: string;
  title: string;
  description?: string;
  fields: string[];
  advancedFields?: string[];
  advancedLabel?: string;
  showExtra?: boolean;
  validate?: (value: Record<string, unknown>) => Record<string, string>;
};

type EditorProps = {
  title: string;
  className?: string;
  placement?: ModalContainerProps["placement"];
  fields: Field[] | ((value: Record<string, unknown>) => Field[]);
  transform?: (
    next: Record<string, unknown>,
    previous: Record<string, unknown>,
  ) => Record<string, unknown>;
  initial: Record<string, unknown>;
  onSave: (value: Record<string, unknown>) => Promise<unknown>;
  onClose: () => void;
  renderExtra?: (
    value: Record<string, unknown>,
    patch: (changes: Record<string, unknown>) => void,
    stepId?: string,
  ) => ReactNode;
  steps?: EditorStep[];
  submitLabel?: string;
  closeOnSave?: boolean;
};

export function Editor({
  title,
  className,
  placement = "center",
  fields,
  initial,
  onSave,
  onClose,
  transform,
  renderExtra,
  steps,
  submitLabel,
  closeOnSave = true,
}: EditorProps) {
  const { t } = useI18n();

  const [value, setValue] = useState(initial);
  const visibleFields = typeof fields === "function" ? fields(value) : fields;
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const submitting = useRef(false);
  const form = useRef<HTMLFormElement>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [stepIndex, setStepIndex] = useState(0);
  const [advanced, setAdvanced] = useState(false);
  const [focusField, setFocusField] = useState<string>();
  const step = steps?.[stepIndex];
  const lastStep = !steps || stepIndex === steps.length - 1;
  const currentFields = step
    ? visibleFields.filter((field) => step.fields.includes(field.key))
    : visibleFields;
  const advancedFields = visibleFields.filter((field) =>
    step?.advancedFields?.includes(field.key),
  );

  useEffect(() => {
    if (!focusField) return;
    const control = form.current?.querySelector<HTMLElement>(
      `[data-field="${focusField}"] input,[data-field="${focusField}"] textarea,[data-field="${focusField}"] button`,
    );
    if (control) {
      control.focus();
      setFocusField(undefined);
    }
  }, [focusField, stepIndex, advanced]);

  function showInvalid(invalid: Record<string, string>) {
    setFieldErrors(
      Object.fromEntries(
        Object.entries(invalid).map(([key, message]) => [key, t(message)]),
      ),
    );
    const first = Object.keys(invalid)[0];
    if (!first) return false;
    const targetStep = steps?.findIndex(
      (entry) =>
        entry.fields.includes(first) || entry.advancedFields?.includes(first),
    );
    if (targetStep !== undefined && targetStep >= 0) {
      setStepIndex(targetStep);
      setAdvanced(Boolean(steps?.[targetStep].advancedFields?.includes(first)));
    }
    setFocusField(first);
    return true;
  }

  function updateValue(patch: Record<string, unknown>) {
    setError("");
    setFieldErrors((errors) =>
      Object.fromEntries(
        Object.entries(errors).filter(([key]) => !(key in patch)),
      ),
    );
    setValue((current) => {
      const updated = { ...current, ...patch };
      return transform ? transform(updated, current) : updated;
    });
  }

  function renderField(field: Field) {
    return (
      <div key={field.key} data-field={field.key}>
        <EditorField
          field={field}
          value={value[field.key]}
          error={fieldErrors[field.key]}
          disabled={busy}
          onChange={(next) => updateValue({ [field.key]: next })}
        />
      </div>
    );
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    // React submit events cross portal boundaries. Nested editors must not
    // submit the project form that opened them.
    event.stopPropagation();
    if (submitting.current) return;
    const invalid = validateFields(
      lastStep ? visibleFields : [...currentFields, ...advancedFields],
      value,
    );
    for (const entry of lastStep ? (steps ?? []) : step ? [step] : [])
      Object.assign(invalid, entry.validate?.(value));
    if (showInvalid(invalid)) return;
    if (!lastStep) {
      setAdvanced(false);
      setStepIndex(stepIndex + 1);
      return;
    }
    submitting.current = true;
    setBusy(true);
    setError("");
    try {
      await onSave(value);
      if (closeOnSave) onClose();
    } catch (error) {
      const message = (error as Error).message;
      const field = fieldForServerError(message, visibleFields);
      if (field) {
        showInvalid({ [field]: message });
      } else setError(message);
    } finally {
      submitting.current = false;
      setBusy(false);
    }
  }

  return (
    <Modal
      isOpen
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <Modal.Backdrop isDismissable={!busy} isKeyboardDismissDisabled={busy}>
        <Modal.Container size="lg" placement={placement} scroll="inside">
          <Modal.Dialog className={className}>
            <Modal.CloseTrigger
              className="size-11"
              aria-label={t("Schließen")}
              isDisabled={busy}
            />
            <form
              ref={form}
              noValidate
              onSubmit={submit}
              className="flex min-h-0 flex-1 flex-col"
            >
              <Modal.Header className="shrink-0 pr-12">
                <Modal.Heading>{title}</Modal.Heading>
              </Modal.Header>
              <Modal.Body className="flex flex-col gap-5 text-foreground">
                {step && steps && (
                  <FlowProgress
                    index={stepIndex}
                    total={steps.length}
                    title={step.title}
                    description={step.description}
                  />
                )}
                <Failure message={error} />
                {error && (
                  <p className="text-sm text-muted">
                    {t("Eingaben prüfen und erneut speichern.")}
                  </p>
                )}
                {currentFields.map(renderField)}
                {advancedFields.length > 0 && (
                  <Accordion
                    hideSeparator
                    expandedKeys={new Set(advanced ? ["advanced"] : [])}
                    onExpandedChange={(keys) =>
                      setAdvanced(keys.has("advanced"))
                    }
                  >
                    <Accordion.Item id="advanced">
                      <Accordion.Heading>
                        <Accordion.Trigger className="min-h-11 text-sm">
                          <span className="flex-1 text-left">
                            {step?.advancedLabel ??
                              t("Erweiterte Einstellungen")}
                          </span>
                          <Accordion.Indicator />
                        </Accordion.Trigger>
                      </Accordion.Heading>
                      <Accordion.Panel>
                        <Accordion.Body className="space-y-5 pb-2">
                          {advancedFields.map(renderField)}
                        </Accordion.Body>
                      </Accordion.Panel>
                    </Accordion.Item>
                  </Accordion>
                )}
                {(!step || step.showExtra) &&
                  renderExtra?.(value, updateValue, step?.id)}
              </Modal.Body>
              <Modal.Footer className="shrink-0 flex-wrap">
                <Button
                  className="min-h-11"
                  variant="ghost"
                  isDisabled={busy}
                  onPress={onClose}
                >
                  {t("Abbrechen")}
                </Button>
                {stepIndex > 0 && (
                  <Button
                    className="min-h-11"
                    variant="secondary"
                    isDisabled={busy}
                    onPress={() => {
                      setAdvanced(false);
                      setStepIndex(stepIndex - 1);
                    }}
                  >
                    {t("Zurück")}
                  </Button>
                )}
                <Button
                  className="min-h-11"
                  type="submit"
                  variant="primary"
                  isPending={busy}
                >
                  {lastStep ? (submitLabel ?? t("Speichern")) : t("Weiter")}
                </Button>
              </Modal.Footer>
            </form>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}

export function Inspect({
  title,
  children,
  onClose,
  isBusy = false,
  placement,
}: {
  title: string;
  children: ReactNode;
  onClose: () => void;
  isBusy?: boolean;
  placement?: ModalContainerProps["placement"];
}) {
  const { t } = useI18n();
  return (
    <Modal
      isOpen
      onOpenChange={(open) => {
        if (!open && !isBusy) onClose();
      }}
    >
      <Modal.Backdrop
        isDismissable={!isBusy}
        isKeyboardDismissDisabled={isBusy}
      >
        <Modal.Container size="lg" scroll="inside" placement={placement}>
          <Modal.Dialog>
            <Modal.CloseTrigger
              className="size-11"
              aria-label={t("Schließen")}
              isDisabled={isBusy}
            />
            <Modal.Header className="shrink-0 pr-12">
              <Modal.Heading>{title}</Modal.Heading>
            </Modal.Header>
            <Modal.Body className="text-foreground">{children}</Modal.Body>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}

export function useAction() {
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const pending = useRef(false);
  async function run(action: () => Promise<unknown>) {
    if (pending.current) return;
    pending.current = true;
    setBusy(true);
    setError("");
    try {
      await action();
      window.dispatchEvent(
        new CustomEvent("zentrale:notice", { detail: "Erfolgreich" }),
      );
    } catch (error) {
      if ((error as Error).name !== "CancelledMutation")
        setError((error as Error).message);
    } finally {
      pending.current = false;
      setBusy(false);
    }
  }
  return { error, busy, run, clearError: () => setError("") };
}
