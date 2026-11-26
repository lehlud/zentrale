import { Button, FieldError, Input, Label, TextField } from "@heroui/react";
import { useState } from "react";

import { useAPI } from "./api";
import { CommitIdentitySettings } from "./commit-identity";
import { GitConnections } from "./git-connections";
import { HuggingFaceSettings } from "./huggingface-connection";
import { ModelPresetSettings } from "./model-presets";
import { HumanizerSettings } from "./humanizer-settings";
import { useI18n } from "./preferences";
import { Failure, Inspect, Title, useAction } from "./ui";
import { AccountSettings } from "./account-settings";
import { FileShareSettings } from "./file-share-settings";
import { ShortChatSettings } from "./short-chat-settings";

export function Settings({ admin }: { admin: boolean }) {
  const { t } = useI18n();
  return (
    <>
      <Title>{t("Persönliche Einstellungen")}</Title>
      <div className="max-w-4xl space-y-8">
        <GitConnections />
        <HuggingFaceSettings />
        <CommitIdentitySettings />
        {admin && <ModelPresetSettings personal />}
      </div>
    </>
  );
}

export function AdminSettings({ oidc }: { oidc: boolean }) {
  const { t } = useI18n();
  return (
    <>
      <Title>{t("Admin-Einstellungen")}</Title>
      <div className="max-w-4xl space-y-8">
        <AccountSettings oidc={oidc} />
        <ModelPresetSettings />
        <ShortChatSettings />
        <FileShareSettings />
        <HumanizerSettings />
        <GitHubAPIAccess />
      </div>
    </>
  );
}

function GitHubAPIAccess() {
  const { t } = useI18n();
  const api = useAPI();
  const [open, setOpen] = useState(false);
  const [token, setToken] = useState("");
  const action = useAction();
  const close = () => {
    setOpen(false);
    setToken("");
    action.clearError();
  };
  return (
    <div className="border-t border-separator pt-6">
      <Button
        variant="ghost"
        className="min-h-11 text-muted"
        onPress={() => setOpen(true)}
      >
        {t("Erweiterter API-Zugang")}
      </Button>
      {open && (
        <Inspect
          title={t("GitHub-API-Zugang")}
          isBusy={action.busy}
          onClose={close}
        >
          <form
            className="space-y-4"
            onSubmit={(event) => {
              event.preventDefault();
              if (action.busy || !token.trim()) return;
              void action.run(async () => {
                await api.mutate(
                  "mutation($token:String!){setGitHubToken(token:$token)}",
                  { token },
                );
                close();
              });
            }}
          >
            <p className="text-sm leading-6 text-muted">
              {t(
                "Optionaler Token für GitHub-API-Werkzeuge. Die Repo-Auswahl verwendet die Git-Verbindung.",
              )}
            </p>
            <TextField
              type="password"
              value={token}
              onChange={setToken}
              isDisabled={action.busy}
              autoComplete="off"
              className="min-w-0 gap-2"
            >
              <Label>{t("GitHub-Token")}</Label>
              <Input className="min-h-11" />
              <FieldError />
            </TextField>
            <Failure message={action.error} />
            <div className="flex flex-wrap justify-end gap-2">
              <Button
                type="button"
                variant="ghost"
                className="mr-auto min-h-11"
                isDisabled={action.busy}
                onPress={() =>
                  action.run(async () => {
                    await api.mutate(
                      "mutation($token:String!){setGitHubToken(token:$token)}",
                      { token: "" },
                    );
                    close();
                  })
                }
              >
                {t("Gespeicherten API-Token entfernen")}
              </Button>
              <Button
                type="button"
                variant="ghost"
                className="min-h-11"
                isDisabled={action.busy}
                onPress={close}
              >
                {t("Abbrechen")}
              </Button>
              <Button
                type="submit"
                className="min-h-11"
                isPending={action.busy}
                isDisabled={!token.trim() || action.busy}
              >
                {t("Speichern")}
              </Button>
            </div>
          </form>
        </Inspect>
      )}
    </div>
  );
}
