import {
  Button,
  Card,
  FieldError,
  Input,
  Label,
  TextField,
} from "@heroui/react";
import { useRef, useState } from "react";
import { useAPI } from "./api";
import { PreferenceControls, useI18n } from "./preferences";
import { Failure, useAction } from "./ui";
import { CommitIdentityFields } from "./commit-identity";
import { FlowProgress } from "./form-flow";

export function Authentication({ setup }: { setup: boolean }) {
  const { t } = useI18n();

  const api = useAPI();
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [mismatch, setMismatch] = useState(false);
  const [identity, setIdentity] = useState({ name: "", email: "" });
  const [step, setStep] = useState(0);
  const confirmationField = useRef<HTMLInputElement>(null);
  const action = useAction();
  async function authenticate() {
    if (setup && password !== confirmation) {
      setMismatch(true);
      confirmationField.current?.focus();
      throw new Error(t("Die Passwörter stimmen nicht überein."));
    }
    setMismatch(false);
    try {
      await api.mutate(
        setup
          ? "mutation($password:String!,$identity:CommitIdentityInput!){setup(password:$password,commitIdentity:$identity)}"
          : "mutation($password:String!){login(password:$password)}",
        setup ? { password, identity } : { password },
      );
    } catch (failure) {
      if (setup && /password/i.test((failure as Error).message)) setStep(0);
      throw failure;
    }
  }
  return (
    <div className="flex min-h-dvh flex-col bg-background">
      <div className="flex flex-wrap justify-end gap-4 px-4 py-3 sm:px-6">
        <PreferenceControls />
      </div>
      <main className="grid flex-1 place-items-center px-4 py-10 sm:p-8">
        <div className={`w-full ${setup ? "max-w-lg" : "max-w-md"}`}>
          <Card className="border border-border">
            <Card.Content>
              <form
                className="flex flex-col gap-5"
                onSubmit={(event) => {
                  event.preventDefault();
                  if (setup && step === 0) {
                    if (password !== confirmation) {
                      setMismatch(true);
                      confirmationField.current?.focus();
                      return;
                    }
                    action.clearError();
                    setStep(1);
                    return;
                  }
                  void action.run(authenticate);
                }}
              >
                <h1 className="text-2xl font-medium tracking-tight">
                  {setup ? t("Zentrale einrichten") : t("Anmelden")}
                </h1>
                {setup && (
                  <FlowProgress
                    index={step}
                    total={2}
                    title={t(
                      step === 0
                        ? "Zugriff sichern"
                        : "Standard-Identität für Commits",
                    )}
                    description={t(
                      step === 0
                        ? "Lege das Passwort für den Zugang zur Zentrale fest."
                        : "Mit welchem Namen und welcher E-Mail sollen Agenten ihre Commits erstellen?",
                    )}
                  />
                )}
                <Failure
                  message={
                    setup && /commit (name|email)/i.test(action.error)
                      ? undefined
                      : action.error
                  }
                />
                {(!setup || step === 0) && (
                  <TextField
                    className="min-w-0 gap-2"
                    type="password"
                    autoFocus
                    isRequired
                    minLength={setup ? 10 : undefined}
                    maxLength={1024}
                    autoComplete={setup ? "new-password" : "current-password"}
                    value={password}
                    onChange={(value) => {
                      setPassword(value);
                      setMismatch(false);
                      action.clearError();
                    }}
                    isDisabled={action.busy}
                  >
                    <Label>{t("Passwort")}</Label>
                    <Input className="min-h-11" />
                    <FieldError></FieldError>
                  </TextField>
                )}
                {setup && step === 0 && (
                  <TextField
                    className="min-w-0 gap-2"
                    type="password"
                    isRequired
                    autoComplete="new-password"
                    value={confirmation}
                    isInvalid={mismatch}
                    onChange={(value) => {
                      setConfirmation(value);
                      setMismatch(false);
                    }}
                  >
                    <Label>{t("Passwort wiederholen")}</Label>
                    <Input className="min-h-11" ref={confirmationField} />
                    <FieldError>
                      {mismatch
                        ? t("Die Passwörter stimmen nicht überein.")
                        : undefined}
                    </FieldError>
                  </TextField>
                )}
                {setup && step === 1 && (
                  <section
                    className="space-y-4"
                    aria-label={t("Standard-Identität für Commits")}
                  >
                    <p className="text-sm leading-6 text-muted">
                      {t(
                        "Agenten verwenden diesen Namen und diese E-Mail als Autor und Committer. Projekte können eine eigene Identität festlegen.",
                      )}
                    </p>
                    <CommitIdentityFields
                      value={identity}
                      onChange={(identity) => {
                        action.clearError();
                        setIdentity(identity);
                      }}
                      error={action.error}
                      disabled={action.busy}
                    />
                  </section>
                )}
                <div className="flex flex-wrap justify-end gap-2">
                  {setup && step > 0 && (
                    <Button
                      variant="secondary"
                      className="min-h-11"
                      isDisabled={action.busy}
                      onPress={() => {
                        action.clearError();
                        setStep(0);
                      }}
                    >
                      {t("Zurück")}
                    </Button>
                  )}
                  <Button
                    className="min-h-11"
                    type="submit"

                    isPending={action.busy}
                  >
                    {setup
                      ? t(step === 0 ? "Weiter" : "Einrichtung abschließen")
                      : t("Anmelden")}
                  </Button>
                </div>
              </form>
            </Card.Content>
          </Card>
        </div>
      </main>
    </div>
  );
}
