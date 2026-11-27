import { Button } from "@heroui/react";
import { useRef, useState } from "react";
import { useAPI, useQuery } from "./api";
import type { FileShareConfig, User } from "./generated";
import { useI18n } from "./preferences";
import { Failure, Inspect, useAction } from "./ui";

const fields =
  "id name protocol endpoint root username hostKeySha256 restricted userIds";
type ShareUser = Pick<User, "id" | "name" | "username" | "disabled">;
const defaultShare: FileShareConfig = {
  id: "",
  name: "",
  protocol: "webdav",
  endpoint: "",
  root: "",
  username: "",
  hostKeySha256: "",
  restricted: false,
  userIds: [],
};
const fieldClass =
  "min-h-10 min-w-0 rounded-lg border border-border bg-surface px-3";

export function FileShareSettings() {
  const { t } = useI18n();
  const { data, error } = useQuery<{
    fileShares: FileShareConfig[];
    users: ShareUser[];
  }>(`query{fileShares{${fields}} users{id name username disabled}}`);
  const [editing, setEditing] = useState<FileShareConfig | null>(null);

  return (
    <section className="space-y-3 border-t border-border pt-6">
      <header className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="font-medium">{t("Dateifreigaben")}</h2>
        <Button variant="secondary" onPress={() => setEditing(defaultShare)}>
          {t("Freigabe hinzufügen")}
        </Button>
      </header>
      <p className="text-sm text-muted">
        {t(
          "Nur in Kurzchats. Der Zugriff ist schreibgeschützt und startet keine VM.",
        )}
      </p>
      <Failure message={error} />
      <ul className="divide-y divide-border">
        {data?.fileShares?.map((share) => (
          <li key={share.id}>
            <Button
              variant="ghost"
              className="min-h-11 w-full justify-between"
              onPress={() => setEditing(share)}
            >
              <span className="truncate">{share.name}</span>
              <span className="text-muted">
                {share.protocol} ·{" "}
                {t(share.restricted ? "Ausgewählte Benutzer" : "Alle Benutzer")}
              </span>
            </Button>
          </li>
        ))}
      </ul>
      {editing && (
        <ShareEditor
          share={editing}
          users={data?.users ?? []}
          onClose={() => setEditing(null)}
        />
      )}
    </section>
  );
}

function ShareEditor({
  share,
  users,
  onClose,
}: {
  share: FileShareConfig;
  users: ShareUser[];
  onClose: () => void;
}) {
  const { t } = useI18n();
  const api = useAPI();
  const action = useAction();
  const check = useAction();
  const form = useRef<HTMLFormElement>(null);
  const [draft, setDraft] = useState(share);
  const [password, setPassword] = useState("");
  const [remove, setRemove] = useState(false);
  const [testUser, setTestUser] = useState("");
  const [entryCount, setEntryCount] = useState<number | null>(null);
  const domainShare = draft.protocol === "domainshare";
  const testUsers = users.filter(
    (user) =>
      !user.disabled && (!draft.restricted || draft.userIds.includes(user.id)),
  );
  const testUserId = testUsers.some((user) => user.id === testUser)
    ? testUser
    : draft.restricted
      ? (testUsers[0]?.id ?? "")
      : "";
  const needsTestUser =
    domainShare || draft.restricted || draft.root.includes("{username}");
  const address = {
    smb: "smb://server/freigabe",
    sftp: "sftp://server:22",
    webdav: "https://server/dav",
    domainshare: "https://dateien.example.org",
  }[draft.protocol];

  function resetCheck() {
    check.clearError();
    setEntryCount(null);
  }

  function change<K extends keyof FileShareConfig>(
    key: K,
    value: FileShareConfig[K],
  ) {
    setDraft((current) => ({ ...current, [key]: value }));
    resetCheck();
  }

  function checkConnection() {
    if (!form.current?.reportValidity() || (draft.restricted && !testUserId))
      return;

    setEntryCount(null);
    void check.run(
      async () => {
        const result = await api.mutate<{
          checkFileShare: { entries: number };
        }>(
          `mutation($input:FileShareConfigInput!,$password:String,$userId:String){checkFileShare(input:$input,password:$password,userId:$userId){entries}}`,
          {
            input: draft,
            ...(password ? { password } : {}),
            ...(testUserId ? { userId: testUserId } : {}),
          },
          { announce: false, refresh: false },
        );
        setEntryCount(result.checkFileShare.entries);
      },
      "default",
      { announce: false },
    );
  }

  return (
    <Inspect
      title={t(share.id ? "Freigabe bearbeiten" : "Freigabe hinzufügen")}
      isBusy={action.busy || check.busy}
      onClose={onClose}
    >
      <form
        ref={form}
        className="grid gap-4 text-sm"
        onSubmit={(event) => {
          event.preventDefault();
          if (check.busy || (draft.restricted && !draft.userIds.length)) return;
          void action.run(async () => {
            await api.mutate(
              `mutation($input:FileShareConfigInput!,$password:String){saveFileShare(input:$input,password:$password){id}}`,
              { input: draft, ...(password ? { password } : {}) },
            );
            onClose();
          });
        }}
      >
        <Failure message={action.error} />
        <fieldset className="contents" disabled={action.busy || check.busy}>
          <label className="grid gap-1">
            {t("Name")}
            <input
              className={fieldClass}
              required
              value={draft.name}
              onChange={(e) => change("name", e.target.value)}
            />
          </label>
          <label className="grid gap-1">
            {t("Protokoll")}
            <select
              className={fieldClass}
              value={draft.protocol}
              onChange={(e) => {
                setPassword("");
                change("protocol", e.target.value);
              }}
            >
              <option value="webdav">WebDAV</option>
              <option value="smb">SMB</option>
              <option value="sftp">SFTP</option>
              <option value="domainshare">DomainShare</option>
            </select>
          </label>
          <label className="grid gap-1">
            {t("Adresse")}
            <input
              className={fieldClass}
              required
              placeholder={address}
              value={draft.endpoint}
              onChange={(e) => change("endpoint", e.target.value)}
            />
          </label>
          {!domainShare && (
            <>
              <label className="grid gap-1">
                {t("Startordner")}
                <input
                  className={fieldClass}
                  placeholder="users/{username}"
                  value={draft.root}
                  onChange={(e) => change("root", e.target.value)}
                />
                <span className="text-xs text-muted">
                  {t(
                    "Nutze `{username}` als Ordnernamen für einen eigenen Bereich pro Benutzer.",
                  )}
                </span>
              </label>
              <label className="grid gap-1">
                {t("Benutzername der Freigabe")}
                <input
                  className={fieldClass}
                  autoComplete="off"
                  value={draft.username}
                  onChange={(e) => change("username", e.target.value)}
                />
              </label>
            </>
          )}
          <label className="grid gap-1">
            {t(domainShare ? "Agenten-Token" : "Passwort")}
            <input
              className={fieldClass}
              autoComplete="new-password"
              type="password"
              required={
                domainShare &&
                (!share.id ||
                  share.protocol !== draft.protocol ||
                  share.endpoint !== draft.endpoint)
              }
              value={password}
              onChange={(e) => {
                setPassword(e.target.value);
                resetCheck();
              }}
            />
            {domainShare && (
              <span className="text-xs text-muted">
                {t(
                  "Der Zentrale-Benutzername muss dem AD-Kontonamen entsprechen. DomainShare prüft die Zugriffsrechte bei jeder Anfrage.",
                )}
              </span>
            )}
          </label>
          {draft.protocol === "sftp" && (
            <label className="grid gap-1">
              {t("SSH-Fingerabdruck")}
              <input
                className={fieldClass}
                required
                placeholder="SHA256:…"
                value={draft.hostKeySha256}
                onChange={(e) => change("hostKeySha256", e.target.value)}
              />
            </label>
          )}
          <label className="grid gap-1">
            {t("Verfügbar für")}
            <select
              className={fieldClass}
              value={draft.restricted ? "selected" : "all"}
              onChange={(event) =>
                change("restricted", event.target.value === "selected")
              }
            >
              <option value="all">{t("Alle Benutzer")}</option>
              <option value="selected">{t("Ausgewählte Benutzer")}</option>
            </select>
          </label>
          {draft.restricted && (
            <fieldset className="grid max-h-44 gap-1 overflow-y-auto rounded-lg border border-border p-2">
              <legend className="px-1">{t("Benutzer mit Zugriff")}</legend>
              {users.map((user) => (
                <label
                  key={user.id}
                  className="flex min-h-9 items-center gap-2 px-2"
                >
                  <input
                    type="checkbox"
                    checked={draft.userIds.includes(user.id)}
                    onChange={(event) =>
                      change(
                        "userIds",
                        event.target.checked
                          ? [...draft.userIds, user.id]
                          : draft.userIds.filter((id) => id !== user.id),
                      )
                    }
                  />
                  <span>{shareUserLabel(user)}</span>
                </label>
              ))}
              {!users.length && (
                <p className="text-muted">{t("Keine Benutzer vorhanden.")}</p>
              )}
            </fieldset>
          )}
          <section className="space-y-2 border-t border-border pt-3">
            {needsTestUser && (
              <label className="grid gap-1">
                {t("Verbindung prüfen als")}
                <select
                  className={fieldClass}
                  value={testUserId}
                  onChange={(event) => {
                    setTestUser(event.target.value);
                    resetCheck();
                  }}
                >
                  {!draft.restricted && (
                    <option value="">{t("Angemeldeter Benutzer")}</option>
                  )}
                  {draft.restricted && !testUsers.length && (
                    <option value="">
                      {t("Kein aktiver Benutzer ausgewählt")}
                    </option>
                  )}
                  {testUsers.map((user) => (
                    <option key={user.id} value={user.id}>
                      {shareUserLabel(user)}
                    </option>
                  ))}
                </select>
              </label>
            )}
            <Button
              type="button"
              variant="secondary"
              isPending={check.busy}
              isDisabled={action.busy || (draft.restricted && !testUserId)}
              onPress={checkConnection}
            >
              {t("Verbindung prüfen")}
            </Button>
            <Failure message={check.error} />
            {entryCount !== null && (
              <p role="status" className="text-success">
                {entryCount === 0
                  ? t(
                      "Verbindung OK. Für diesen Benutzer sind keine Einträge sichtbar.",
                    )
                  : t(
                      "Verbindung OK. {count} Einträge im Startordner sichtbar.",
                      { count: entryCount },
                    )}
              </p>
            )}
          </section>
        </fieldset>
        <div className="flex flex-wrap justify-end gap-2">
          {share.id && (
            <Button
              variant="ghost"
              isDisabled={check.busy || action.busy}
              onPress={() => {
                if (!remove) {
                  setRemove(true);
                  return;
                }
                void action.run(async () => {
                  await api.mutate(
                    "mutation($id:String!){deleteFileShare(id:$id)}",
                    { id: share.id },
                  );
                  onClose();
                });
              }}
            >
              {t(remove ? "Entfernen bestätigen" : "Entfernen")}
            </Button>
          )}
          <Button
            type="submit"
            isPending={action.busy}
            isDisabled={
              check.busy || (draft.restricted && !draft.userIds.length)
            }
          >
            {t("Speichern")}
          </Button>
        </div>
      </form>
    </Inspect>
  );
}

function shareUserLabel(user: ShareUser) {
  return user.name && user.name !== user.username
    ? `${user.name} (${user.username})`
    : user.username;
}
