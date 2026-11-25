import { Button } from "@heroui/react";
import { useState } from "react";
import { useAPI, useQuery } from "./api";
import type { FileShareConfig } from "./generated";
import { useI18n } from "./preferences";
import { Failure, Inspect, useAction } from "./ui";

const fields = "id name protocol endpoint root username hostKeySha256";
const defaultShare: FileShareConfig = {
  id: "",
  name: "",
  protocol: "webdav",
  endpoint: "",
  root: "",
  username: "",
  hostKeySha256: "",
};
const fieldClass =
  "min-h-10 min-w-0 rounded-lg border border-border bg-surface px-3";

export function FileShareSettings() {
  const { t } = useI18n();
  const { data, error } = useQuery<{ fileShares: FileShareConfig[] }>(
    `query{fileShares{${fields}}}`,
  );
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
        {data?.fileShares.map((share) => (
          <li key={share.id}>
            <Button
              variant="ghost"
              className="min-h-11 w-full justify-between"
              onPress={() => setEditing(share)}
            >
              <span className="truncate">{share.name}</span>
              <span className="text-muted">{share.protocol}</span>
            </Button>
          </li>
        ))}
      </ul>
      {editing && (
        <ShareEditor share={editing} onClose={() => setEditing(null)} />
      )}
    </section>
  );
}

function ShareEditor({
  share,
  onClose,
}: {
  share: FileShareConfig;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const api = useAPI();
  const action = useAction();
  const [draft, setDraft] = useState(share);
  const [password, setPassword] = useState("");
  const [remove, setRemove] = useState(false);
  const address = {
    smb: "smb://server/freigabe",
    sftp: "sftp://server:22",
    webdav: "https://server/dav",
  }[draft.protocol];

  function change(key: keyof FileShareConfig, value: string) {
    setDraft((current) => ({ ...current, [key]: value }));
  }

  return (
    <Inspect
      title={t(share.id ? "Freigabe bearbeiten" : "Freigabe hinzufügen")}
      isBusy={action.busy}
      onClose={onClose}
    >
      <form
        className="grid gap-4 text-sm"
        onSubmit={(event) => {
          event.preventDefault();
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
            onChange={(e) => change("protocol", e.target.value)}
          >
            <option value="webdav">WebDAV</option>
            <option value="smb">SMB</option>
            <option value="sftp">SFTP</option>
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
        <label className="grid gap-1">
          {t("Passwort")}
          <input
            className={fieldClass}
            autoComplete="new-password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
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
        <div className="flex flex-wrap justify-end gap-2">
          {share.id && (
            <Button
              variant="ghost"
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
          <Button type="submit" isPending={action.busy}>
            {t("Speichern")}
          </Button>
        </div>
      </form>
    </Inspect>
  );
}
