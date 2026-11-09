import { Alert, Button, Card, Chip } from "@heroui/react";
import { useRef, useState } from "react";
import { useAPI } from "./api";
import type {
  Machine,
  MachineConnectionCheck,
  SandboxStatus,
} from "./generated";
import { useI18n } from "./preferences";
import { HostKeyTrust } from "./ssh-trust";
import { Failure } from "./ui";
import { SandboxBadge } from "./sandbox-badge";
import { ActionsMenu } from "./actions-menu";
import { Icon } from "./icons";

export function MachineCard({
  machine,
  sandboxStatus,
  onEdit,
  onRemove,
}: {
  machine: Machine;
  sandboxStatus?: SandboxStatus;
  onEdit: () => void;
  onRemove: () => void;
}) {
  const api = useAPI();
  const { t } = useI18n();
  const [result, setResult] = useState<MachineConnectionCheck>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const pending = useRef(false);
  const observedSandbox = result?.connected ? result.sandbox : sandboxStatus;
  async function check() {
    if (pending.current) return;
    pending.current = true;
    setBusy(true);
    setError("");
    setResult(undefined);
    try {
      const data = await api.query<{ testMachine: MachineConnectionCheck }>(
        "mutation($id:String!){testMachine(id:$id){connected error hostKey{host fingerprint publicKey} sandbox{machineId mode level checkedAt error}}}",
        { id: machine.id },
      );
      setResult(data.testMachine);
    } catch (failure) {
      setError((failure as Error).message);
    } finally {
      pending.current = false;
      setBusy(false);
    }
  }
  return (
    <Card variant="secondary">
      <Card.Content className="gap-4">
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div className="hidden size-10 shrink-0 items-center justify-center text-muted sm:flex">
            <Icon name="machines" className="size-5" />
          </div>
          <div className="min-w-0 flex-1 basis-64">
            <h2 className="break-words font-medium">{machine.name}</h2>
            <p className="mt-1 break-all font-mono text-xs leading-5 text-muted">
              {machine.user}@{machine.host}:{machine.port}
            </p>
            <p className="mt-2 break-all text-xs leading-5 text-muted">
              {t("Projektverzeichnis")}:{" "}
              {machine.workspaceRoot || "~/.local/share/zentrale/projects"}
            </p>
            <div className="mt-3 flex flex-wrap gap-2 [&_[data-slot=chip]]:max-w-full [&_[data-slot=chip-label]]:whitespace-normal">
              <SandboxBadge
                mode={machine.sandbox}
                status={observedSandbox}
                showCheckedAt
              />
              {machine.vmType === "q35" && (
                <Chip size="sm" variant="soft">
                  q35
                </Chip>
              )}
              {machine.singleUser && (
                <Chip size="sm" variant="soft">
                  Single-User
                </Chip>
              )}
              {machine.properties.map((property) => (
                <Chip key={property} size="sm" variant="soft">
                  {property}
                </Chip>
              ))}
            </div>
          </div>
          <div className="flex items-center gap-1">
            <Button
              className="min-h-11"
              size="sm"
              variant="ghost"
              isPending={busy}
              onPress={() => void check()}
            >
              {t("Verbindung prüfen")}
            </Button>
            <ActionsMenu
              label={t("Aktionen für {name}", { name: machine.name })}
              actions={[
                { id: "edit", label: t("Bearbeiten"), onAction: onEdit },
                {
                  id: "remove",
                  label: t("Entfernen"),
                  destructive: true,
                  onAction: onRemove,
                },
              ]}
            />
          </div>
        </div>
        {result?.connected && (
          <Alert status="success" role="status">
            <Alert.Indicator />
            <Alert.Content>
              <Alert.Title>{t("Verbindung erfolgreich")}</Alert.Title>
              <p className="text-sm">
                {t("Die SSH-Anmeldung auf {machine} funktioniert.", {
                  machine: machine.name,
                })}
              </p>
            </Alert.Content>
          </Alert>
        )}
        {result?.hostKey.publicKey ? (
          <HostKeyTrust challenge={result.hostKey} onTrusted={check} />
        ) : (
          <Failure
            message={error || result?.error || observedSandbox?.error}
            onRetry={() => void check()}
          />
        )}
      </Card.Content>
    </Card>
  );
}
