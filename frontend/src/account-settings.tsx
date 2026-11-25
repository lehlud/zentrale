import { Button } from "@heroui/react";
import { useState } from "react";
import { useAPI, useQuery } from "./api";
import type { User } from "./generated";
import { useI18n } from "./preferences";
import { Failure, Inspect, useAction } from "./ui";

const userFields =
  "id username name role disabled canCreateProjects monthlyTokenLimit monthlyCostLimit";
const emptyUser: User = {
  id: "",
  username: "",
  name: "",
  role: "user",
  disabled: false,
  canCreateProjects: false,
  monthlyTokenLimit: 0,
  monthlyCostLimit: 0,
};
const fieldClass = "min-h-10 rounded-lg border border-border bg-surface px-3";

export function AccountSettings({ oidc }: { oidc: boolean }) {
  const { t } = useI18n();
  const { data, error } = useQuery<{ users: User[] }>(
    `query{users{${userFields}}}`,
  );
  const [editing, setEditing] = useState<User | null>(null);

  return (
    <section className="space-y-3 border-t border-border pt-6">
      <header className="flex items-center justify-between gap-3">
        <h2 className="font-medium">{t("Benutzer")}</h2>
        <Button variant="secondary" onPress={() => setEditing(emptyUser)}>
          {t("Benutzer hinzufügen")}
        </Button>
      </header>
      <Failure message={error} />
      <ul className="divide-y divide-border">
        {data?.users.map((user) => (
          <li key={user.id}>
            <Button
              variant="ghost"
              className="min-h-11 w-full justify-between px-3"
              onPress={() => setEditing(user)}
            >
              <span className="truncate">
                {user.name} <span className="text-muted">@{user.username}</span>
              </span>
              <span className="text-muted">
                {t(
                  user.disabled
                    ? "Deaktiviert"
                    : user.role === "admin"
                      ? "Admin"
                      : "Benutzer",
                )}
              </span>
            </Button>
          </li>
        ))}
      </ul>
      {editing && (
        <AccountEditor
          user={editing}
          oidc={oidc}
          onClose={() => setEditing(null)}
        />
      )}
    </section>
  );
}

function AccountEditor({
  user,
  oidc,
  onClose,
}: {
  user: User;
  oidc: boolean;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const api = useAPI();
  const action = useAction();
  const [draft, setDraft] = useState(user);
  const [credential, setCredential] = useState("");
  const editing = Boolean(user.id);
  const initialAdmin = user.id === "initial-admin";

  function change<K extends keyof User>(key: K, value: User[K]) {
    setDraft((current) => ({ ...current, [key]: value }));
  }

  return (
    <Inspect
      title={t(editing ? "Benutzer bearbeiten" : "Benutzer hinzufügen")}
      onClose={onClose}
      isBusy={action.busy}
    >
      <form
        className="grid gap-4 text-sm"
        onSubmit={(event) => {
          event.preventDefault();
          void action.run(async () => {
            await api.mutate(
              `mutation($input:UserInput!,$password:String,$oidcSubject:String){
            saveUser(input:$input,password:$password,oidcSubject:$oidcSubject){id}
          }`,
              {
                input: draft,
                ...(oidc
                  ? { oidcSubject: credential }
                  : { password: credential }),
              },
            );
            onClose();
          });
        }}
      >
        <Failure message={action.error} />
        <label className="grid gap-1">
          {t("Benutzername")}
          <input
            className={fieldClass}
            autoComplete="off"
            required
            readOnly={editing}
            value={draft.username}
            onChange={(e) => change("username", e.target.value)}
          />
        </label>
        <label className="grid gap-1">
          {t("Anzeigename")}
          <input
            className={fieldClass}
            value={draft.name}
            onChange={(e) => change("name", e.target.value)}
          />
        </label>
        {(!oidc || !editing) && (
          <label className="grid gap-1">
            {t(
              oidc
                ? "OpenID Connect Subject (sub)"
                : editing
                  ? "Neues Passwort (optional)"
                  : "Passwort",
            )}
            <input
              className={fieldClass}
              type={oidc ? "text" : "password"}
              autoComplete={oidc ? "off" : "new-password"}
              value={credential}
              required={!editing}
              onChange={(e) => setCredential(e.target.value)}
            />
          </label>
        )}
        <label className="grid gap-1">
          {t("Rolle")}
          <select
            className={fieldClass}
            disabled={initialAdmin}
            value={draft.role}
            onChange={(e) => change("role", e.target.value)}
          >
            <option value="user">{t("Benutzer")}</option>
            <option value="admin">{t("Admin")}</option>
          </select>
        </label>
        <label className="flex items-center gap-2">
          <input
            type="checkbox"
            checked={draft.canCreateProjects}
            onChange={(e) => change("canCreateProjects", e.target.checked)}
          />
          {t("Darf Projekte erstellen")}
        </label>
        {editing && !initialAdmin && (
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={draft.disabled}
              onChange={(e) => change("disabled", e.target.checked)}
            />
            {t("Konto deaktivieren")}
          </label>
        )}
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="grid gap-1">
            {t("Tokens pro Monat")}
            <input
              className={fieldClass}
              type="number"
              min="0"
              max="2147483647"
              value={draft.monthlyTokenLimit}
              onChange={(e) =>
                change("monthlyTokenLimit", Number(e.target.value))
              }
            />
          </label>
          <label className="grid gap-1">
            {t("USD pro Monat")}
            <input
              className={fieldClass}
              type="number"
              min="0"
              step="0.01"
              value={draft.monthlyCostLimit}
              onChange={(e) =>
                change("monthlyCostLimit", Number(e.target.value))
              }
            />
          </label>
        </div>
        <p className="text-xs text-muted">{t("0 bedeutet kein Limit.")}</p>
        <Button type="submit" isPending={action.busy}>
          {t("Speichern")}
        </Button>
      </form>
    </Inspect>
  );
}
