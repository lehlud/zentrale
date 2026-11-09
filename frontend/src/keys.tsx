import {
  Button,
  Chip,
  FieldError,
  Label,
  Tabs,
  TextArea,
  TextField,
} from "@heroui/react";
import { useRef, useState } from "react";

import { ActionsMenu } from "./actions-menu";
import { CONFIG, useAPI, useQuery } from "./api";
import { MoreButton, useBatch } from "./batches";
import type { Config } from "./config";
import type { SSHKey } from "./generated";
import { setRouteParameter, useRoute } from "./navigation";
import { useI18n } from "./preferences";
import { Empty, Failure, Inspect, Loading, Title, useAction } from "./ui";
import { Icon } from "./icons";

function keyAlgorithm(key: SSHKey) {
  const algorithm = key.publicKey?.split(" ")[0] ?? "SSH";
  return algorithm === "ssh-ed25519"
    ? "Ed25519"
    : algorithm.startsWith("ecdsa-")
      ? "ECDSA"
      : algorithm;
}

export function Keys() {
  const { t } = useI18n();
  const api = useAPI();
  const { data, error, loading } = useQuery<Config>(CONFIG);
  const { params } = useRoute();
  const selected = params.get("type") === "hosts" ? "hosts" : "private";
  const [addingHost, setAddingHost] = useState(false);
  const action = useAction();
  const privateKeys = useBatch(data?.sshKeys ?? []);
  const hostKeys = useBatch(data?.trustedKeys ?? []);

  return (
    <>
      <Title
        description={t(
          "Eigene Schlüssel erlauben den Zugriff auf Maschinen und Repositories. Jeder Schlüssel hat einen eigenen Zweck.",
        )}
        action={
          selected === "hosts" ? (
            <Button className="min-h-11" onPress={() => setAddingHost(true)}>
              <Icon name="plus" />
              {t("Host-Schlüssel hinzufügen")}
            </Button>
          ) : (
            <Button
              className="min-h-11"
              isPending={action.busy}
              onPress={() =>
                action.run(() =>
                  api.mutate("mutation{generateSSHKey{id publicKey}}"),
                )
              }
            >
              <Icon name="plus" />
              {t("SSH-Schlüssel erzeugen")}
            </Button>
          )
        }
      >
        {t("Schlüssel")}
      </Title>
      <Failure message={error || action.error} onRetry={() => api.refresh()} />
      <Tabs
        variant="secondary"
        align="start"
        className="gap-5"
        selectedKey={selected}
        onSelectionChange={(key) => setRouteParameter("type", String(key))}
      >
        <Tabs.ListContainer>
          <Tabs.List aria-label={t("Schlüsselart")}>
            <Tabs.Tab id="private" className="min-h-11 w-auto shrink-0">
              {t("Eigene Schlüssel")}
              <Tabs.Indicator />
            </Tabs.Tab>
            <Tabs.Tab id="hosts" className="min-h-11 w-auto shrink-0">
              {t("Vertrauenswürdige Hosts")}
              <Tabs.Indicator />
            </Tabs.Tab>
          </Tabs.List>
        </Tabs.ListContainer>
        <Tabs.Panel id="private" className="p-0">
          <p className="mb-5 max-w-2xl text-sm leading-6 text-muted">
            {t(
              "Maschinenschlüssel gehören in ~/.ssh/authorized_keys. Repo-Schlüssel werden automatisch als Deploy-Keys hinterlegt. Private Schlüssel bleiben in der Zentrale.",
            )}
          </p>
          {loading && !data && <Loading />}
          {!loading && !error && !data?.sshKeys.length && (
            <Empty>{t("Keine SSH-Schlüssel")}</Empty>
          )}
          <div
            className={
              privateKeys.items.length ? "divide-y divide-separator" : ""
            }
          >
            {privateKeys.items.map((key) => {
              const usedBy = key.usages ?? [];
              const repository = usedBy.find(
                (usage) => usage?.kind === "repository",
              );
              const pool = usedBy.find((usage) => usage?.kind === "pool");
              const name =
                repository?.name ||
                pool?.name ||
                t("Schlüssel {id}", { id: key.id ?? "" });
              const projectNames = [
                ...new Set(
                  usedBy
                    .filter((usage) => usage?.projectId)
                    .map(
                      (usage) =>
                        data?.projects.find(
                          (project) => project.id === usage?.projectId,
                        )?.name ||
                        usage?.projectId ||
                        "",
                    ),
                ),
              ];

              return (
                <section
                  key={key.id}
                  className="flex min-w-0 flex-wrap items-center gap-3 py-4"
                >
                  <div className="hidden size-10 shrink-0 items-center justify-center text-muted sm:flex">
                    <Icon name="keys" className="size-5" />
                  </div>
                  <div className="min-w-0 flex-1 basis-64 space-y-2">
                    <div className="flex flex-wrap items-center gap-3">
                      <h2 className="min-w-0 break-all text-base font-medium">
                        {name}
                      </h2>
                      <Chip size="sm" variant="soft">
                        {keyAlgorithm(key)}
                      </Chip>
                      {repository && (
                        <Chip size="sm" variant="soft">
                          Deploy-Key
                        </Chip>
                      )}
                      {pool && (
                        <Chip size="sm" variant="soft">
                          {t("Pool-Schlüssel")}
                        </Chip>
                      )}
                    </div>
                    <p className="break-all font-mono text-xs text-muted">
                      {key.fingerprint}
                    </p>
                    <p className="text-xs text-muted">
                      {repository
                        ? projectNames.length
                          ? t("Projekte: {names}", {
                              names: projectNames.join(", "),
                            })
                          : t(
                              "Repo-Zugriff besteht. Aktuell keinem Projekt zugewiesen.",
                            )
                        : usedBy.length
                          ? t("Verwendet von {names}", {
                              names: [
                                ...new Set(
                                  usedBy.map((usage) => usage?.name ?? ""),
                                ),
                              ].join(", "),
                            })
                          : t("Nicht verwendet")}
                    </p>
                  </div>
                  <div className="flex items-center gap-1">
                    <Button
                      className="min-h-11"
                      aria-label={t("Öffentlichen Schlüssel kopieren")}
                      size="sm"
                      variant="secondary"
                      onPress={() =>
                        action.run(() =>
                          navigator.clipboard.writeText(key.publicKey ?? ""),
                        )
                      }
                    >
                      <Icon name="copy" />
                      {t("Kopieren")}
                    </Button>
                    <ActionsMenu
                      label={t("Aktionen für {name}", { name })}
                      isDisabled={action.busy}
                      actions={[
                        ...(key.canRevoke
                          ? [
                              {
                                id: "revoke",
                                label: t(
                                  "Repo-Zugriff widerrufen und Schlüssel löschen",
                                ),
                                destructive: true,
                                onAction: () =>
                                  action.run(() =>
                                    api.mutate(
                                      "mutation($id:Int!){revokeRepositoryKey(id:$id)}",
                                      { id: key.id },
                                    ),
                                  ),
                              },
                            ]
                          : []),
                        {
                          id: "remove",
                          label: t("Entfernen"),
                          destructive: true,
                          isDisabled: !key.canDelete,
                          onAction: () =>
                            action.run(() =>
                              api.mutate(
                                "mutation($id:Int!){deleteSSHKey(id:$id)}",
                                { id: key.id },
                              ),
                            ),
                        },
                      ]}
                    />
                  </div>
                </section>
              );
            })}
          </div>
          <MoreButton batch={privateKeys} />
        </Tabs.Panel>
        <Tabs.Panel id="hosts" className="p-0">
          <p className="mb-5 max-w-2xl text-sm leading-6 text-muted">
            {t(
              "Host-Schlüssel werden beim ersten Verbindungsaufbau geprüft. Hier lassen sich vorhandene Vertrauensbeziehungen einsehen und entziehen.",
            )}
          </p>
          {loading && !data && <Loading />}
          {!loading && !error && !data?.trustedKeys.length && (
            <Empty>{t("Noch keine vertrauenswürdigen Hosts")}</Empty>
          )}
          <div
            className={hostKeys.items.length ? "divide-y divide-separator" : ""}
          >
            {hostKeys.items.map((key) => (
              <section
                key={key.fingerprint}
                className="flex min-w-0 items-center gap-4 px-4 py-4 sm:px-5"
              >
                <div className="min-w-0 flex-1 space-y-2">
                  <h2 className="text-sm font-medium">
                    {t("Vertrauenswürdiger Host")} · {keyAlgorithm(key)}
                  </h2>
                  <p className="break-all font-mono text-xs text-muted">
                    {key.fingerprint}
                  </p>
                  <Button
                    size="sm"
                    variant="secondary"
                    className="mt-2 min-h-11"
                    aria-label={t("Öffentlichen Schlüssel kopieren")}
                    onPress={() =>
                      action.run(() =>
                        navigator.clipboard.writeText(key.publicKey ?? ""),
                      )
                    }
                  >
                    {t("Kopieren")}
                  </Button>
                </div>
                <ActionsMenu
                  label={t("Aktionen für {name}", {
                    name: key.fingerprint || t("SSH-Host"),
                  })}
                  isDisabled={action.busy}
                  actions={[
                    {
                      id: "distrust",
                      label: t("Vertrauen entziehen"),
                      destructive: true,
                      onAction: () =>
                        action.run(() =>
                          api.mutate(
                            "mutation($publicKey:String!,$trust:String!){trustHostKey(publicKey:$publicKey,trust:$trust)}",
                            { publicKey: key.publicKey, trust: "distrust" },
                          ),
                        ),
                    },
                  ]}
                />
              </section>
            ))}
          </div>
          <MoreButton batch={hostKeys} />
        </Tabs.Panel>
      </Tabs>
      {addingHost && <TrustHost onClose={() => setAddingHost(false)} />}
    </>
  );
}

function TrustHost({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  const api = useAPI();
  const [publicKey, setPublicKey] = useState("");
  const [invalid, setInvalid] = useState(false);
  const input = useRef<HTMLTextAreaElement>(null);
  const action = useAction();

  return (
    <Inspect title={t("Host-Schlüssel hinzufügen")} onClose={onClose}>
      <form
        className="space-y-5"
        onSubmit={(event) => {
          event.preventDefault();
          if (!publicKey.trim()) {
            setInvalid(true);
            input.current?.focus();

            return;
          }

          void action.run(async () => {
            await api.mutate(
              "mutation($publicKey:String!,$trust:String!){trustHostKey(publicKey:$publicKey,trust:$trust)}",
              { publicKey: publicKey.trim(), trust: "trust" },
            );
            onClose();
          });
        }}
      >
        <p className="text-sm leading-6 text-muted">
          {t(
            "Den Schlüssel mit einer vertrauenswürdigen Quelle vergleichen, bevor du diesem Host vertraust.",
          )}
        </p>
        <TextField
          value={publicKey}
          onChange={setPublicKey}
          isRequired
          isInvalid={invalid}
          className="gap-2"
        >
          <Label>{t("Öffentlicher Host-Schlüssel")}</Label>
          <TextArea
            ref={input}
            autoFocus
            rows={4}
            placeholder="ssh-ed25519 AAAA…"
            className="font-mono text-xs"
          />
          <FieldError>{t("Dieses Feld ist erforderlich.")}</FieldError>
        </TextField>
        <Failure message={action.error} />
        <div className="flex justify-end gap-2">
          <Button className="min-h-11" variant="ghost" onPress={onClose}>
            {t("Abbrechen")}
          </Button>
          <Button type="submit" className="min-h-11" isPending={action.busy}>
            {t("Vertrauen")}
          </Button>
        </div>
      </form>
    </Inspect>
  );
}
