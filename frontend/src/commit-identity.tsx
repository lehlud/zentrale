import { Button, FieldError, Input, Label, TextField } from "@heroui/react";
import { useEffect, useRef, useState } from "react";

import { CONFIG, useAPI, useQuery } from "./api";
import type { Config } from "./config";
import type { CommitIdentity } from "./generated";
import { useI18n } from "./preferences";
import { Failure, Loading, useAction } from "./ui";

export function CommitIdentityFields({
  value,
  onChange,
  error = "",
  disabled = false,
}: {
  value: CommitIdentity;
  onChange: (identity: CommitIdentity) => void;
  error?: string;
  disabled?: boolean;
}) {
  const { t } = useI18n();
  const name = useRef<HTMLInputElement>(null);
  const email = useRef<HTMLInputElement>(null);
  const nameError = /commit name/i.test(error) ? error : "";
  const emailError = /commit email/i.test(error) ? error : "";

  useEffect(() => {
    if (nameError) name.current?.focus();
    else if (emailError) email.current?.focus();
  }, [nameError, emailError]);

  return (
    <div className="space-y-4">
      <TextField
        value={value.name}
        onChange={(text) => onChange({ ...value, name: text })}
        isRequired
        isDisabled={disabled}
        isInvalid={Boolean(nameError)}
        className="gap-2"
        autoComplete="name"
      >
        <Label>{t("Name für Commits")}</Label>
        <Input
          ref={name}
          className="min-h-11"
          maxLength={200}
          placeholder={t("z. B. Entwicklungsteam")}
        />
        <FieldError>{nameError ? t(nameError) : undefined}</FieldError>
      </TextField>
      <TextField
        type="email"
        value={value.email}
        onChange={(text) => onChange({ ...value, email: text })}
        isRequired
        isDisabled={disabled}
        isInvalid={Boolean(emailError)}
        className="gap-2"
        autoComplete="email"
      >
        <Label>{t("E-Mail für Commits")}</Label>
        <Input
          ref={email}
          className="min-h-11"
          maxLength={254}
          placeholder="dev@example.org"
        />
        <FieldError>{emailError ? t(emailError) : undefined}</FieldError>
      </TextField>
    </div>
  );
}

export function CommitIdentitySettings() {
  const { t } = useI18n();
  const api = useAPI();
  const { data, error, loading } = useQuery<Config>(CONFIG);
  const [draft, setDraft] = useState<CommitIdentity>();
  const action = useAction();
  const saved = data?.commitIdentity;
  const value = draft ?? saved ?? { name: "", email: "" };
  const dirty = Boolean(
    draft && (draft.name !== saved?.name || draft.email !== saved?.email),
  );

  useEffect(() => {
    if (draft && saved?.name === draft.name && saved.email === draft.email)
      setDraft(undefined);
  }, [draft, saved]);

  return (
    <section
      aria-labelledby="commit-identity-title"
      className="border-t border-border pt-8"
    >
      <h2 id="commit-identity-title" className="text-base font-medium">
        {t("Standard-Identität für Commits")}
      </h2>
      <p className="mt-2 mb-5 max-w-2xl text-sm leading-6 text-muted">
        {t(
          "Agenten verwenden diesen Namen und diese E-Mail als Autor und Committer. Projekte können eine eigene Identität festlegen.",
        )}
      </p>
      {loading && !data ? (
        <Loading />
      ) : (
        <form
          className="max-w-xl space-y-4"
          onSubmit={(event) => {
            event.preventDefault();
            void action.run(async () => {
              const submitted = {
                name: value.name.trim(),
                email: value.email.trim(),
              };
              await api.mutate(
                "mutation($input:CommitIdentityInput!){setCommitIdentity(input:$input)}",
                { input: submitted },
              );
              setDraft(submitted);
            });
          }}
        >
          <CommitIdentityFields
            value={value}
            onChange={(identity) => {
              action.clearError();
              setDraft(identity);
            }}
            error={action.error}
            disabled={action.busy}
          />
          <Failure
            message={
              error ||
              (!/commit (name|email)/i.test(action.error)
                ? action.error
                : undefined)
            }
            onRetry={error ? () => api.refresh() : undefined}
          />
          <div className="flex flex-wrap items-center gap-2 pt-1">
            <Button
              type="submit"
              className="min-h-11"
              isDisabled={!dirty || action.busy}
              isPending={action.busy}
            >
              {t("Commit-Identität speichern")}
            </Button>
            {dirty && (
              <>
                <Button
                  variant="ghost"
                  className="min-h-11"
                  isDisabled={action.busy}
                  onPress={() => {
                    setDraft(undefined);
                    action.clearError();
                  }}
                >
                  {t("Verwerfen")}
                </Button>
                <p role="status" className="text-sm text-muted">
                  {t("Nicht gespeicherte Änderungen")}
                </p>
              </>
            )}
          </div>
        </form>
      )}
    </section>
  );
}
