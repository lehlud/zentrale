import { GitConnections } from "./git-connections";
import { Button, FieldError, Input, Label, TextField } from "@heroui/react";
import { useState } from "react";

import { useAPI } from "./api";
import { GlobalModelDefaults } from "./model-defaults";
import { useI18n } from "./preferences";
import { Failure, Title, useAction } from "./ui";
import { CommitIdentitySettings } from "./commit-identity";

export function Settings() {
  const { t } = useI18n();

  const api = useAPI();
  const [token, setToken] = useState("");
  const action = useAction();

  return (
    <>
      <Title
        description={t("Modelle und verbundene Dienste für deine Projekte.")}
      >
        {t("Einstellungen")}
      </Title>
      <div className="max-w-4xl space-y-10">
        <GlobalModelDefaults />
        <CommitIdentitySettings />
        <GitConnections />
        <details>
          <summary className="min-h-11 cursor-pointer text-sm text-muted">
            {t("Erweiterter API-Zugang")}
          </summary>
          <section
            className="min-w-0 border-t border-border pt-8"
            aria-labelledby="github-settings-title"
          >
            <div className="mb-5 flex items-center justify-between gap-4">
              <h2 id="github-settings-title" className="text-base font-medium">
                GitHub
              </h2>
            </div>
            <div className="max-w-xl space-y-4">
              <p className="text-sm leading-6 text-muted">
                {t(
                  "Optionaler Token für GitHub-API-Werkzeuge. Die Repo-Auswahl verwendet die Git-Verbindung.",
                )}
              </p>
              <TextField
                className="min-w-0 gap-2"
                type="password"
                value={token}
                onChange={setToken}
                autoComplete="off"
              >
                <Label>{t("GitHub-Token")}</Label>
                <Input className="min-h-11" />
                <FieldError></FieldError>
              </TextField>
              <Failure message={action.error} />
              <div className="flex gap-2">
                <Button
                  className="min-h-11"
                  isPending={action.busy}
                  isDisabled={!token.trim()}
                  onPress={() =>
                    action.run(async () => {
                      await api.mutate(
                        "mutation($token:String!){setGitHubToken(token:$token)}",
                        { token },
                      );
                      setToken("");
                    })
                  }
                >
                  {t("Speichern")}
                </Button>
                <Button
                  className="min-h-11"
                  variant="ghost"
                  onPress={() =>
                    action.run(() =>
                      api.mutate(
                        "mutation($token:String!){setGitHubToken(token:$token)}",
                        { token: "" },
                      ),
                    )
                  }
                >
                  {t("Gespeicherten API-Token entfernen")}
                </Button>
              </div>
            </div>
          </section>
        </details>
      </div>
    </>
  );
}
