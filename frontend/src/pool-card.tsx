import { Alert, Button, Card } from "@heroui/react";
import { useRef, useState } from "react";

import { ActionsMenu } from "./actions-menu";
import { useAPI } from "./api";
import type { Config } from "./config";
import type {
  MachineConnectionCheck,
  Pool,
  PoolConnectionCheck,
  PoolMachineConnectionCheck,
} from "./generated";
import { PoolAccess } from "./pool-access";
import { useI18n } from "./preferences";
import { SandboxBadge } from "./sandbox-badge";
import { HostKeyTrust } from "./ssh-trust";
import { Failure } from "./ui";

const checkFields =
  "connected error hostKey{host fingerprint publicKey} sandbox{machineId mode level checkedAt error}";

function ready(check: MachineConnectionCheck) {
  return check.connected && !check.error && !check.sandbox.error;
}

export function PoolCard({
  pool,
  keys,
  busy,
  onRemove,
}: {
  pool: Pool;
  keys: Config["sshKeys"];
  busy: boolean;
  onRemove: () => void;
}) {
  const api = useAPI();
  const { t } = useI18n();
  const [result, setResult] = useState<PoolConnectionCheck>();
  const [error, setError] = useState("");
  const [checking, setChecking] = useState(false);
  const pending = useRef(false);

  async function runProbe(probe: () => Promise<void>) {
    if (pending.current) {
      return;
    }

    pending.current = true;
    setChecking(true);
    setError("");

    try {
      await probe();
    } catch (failure) {
      setError((failure as Error).message);
    } finally {
      pending.current = false;
      setChecking(false);
    }
  }

  const checkPool = () =>
    runProbe(async () => {
      setResult(undefined);
      const data = await api.query<{ testPool: PoolConnectionCheck }>(
        `mutation($id:String!){testPool(id:$id){poolId totalMachines machines{machineId name host port ${checkFields}}}}`,
        { id: pool.id },
      );
      setResult(data.testPool);
    });

  // A trust decision or retry applies to this exact member, without drawing a
  // different sample and losing the other machines' diagnostic results.
  const checkMember = (machineId: string) =>
    runProbe(async () => {
      const data = await api.query<{ testMachine: MachineConnectionCheck }>(
        `mutation($id:String!){testMachine(id:$id){${checkFields}}}`,
        { id: machineId },
      );
      setResult((current) =>
        current
          ? {
              ...current,
              machines: current.machines.map((member) =>
                member.machineId === machineId
                  ? { ...member, ...data.testMachine }
                  : member,
              ),
            }
          : current,
      );
    });

  const successful = result?.machines.filter(ready).length ?? 0;

  return (
    <Card variant="secondary">
      <Card.Content className="gap-4">
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div className="min-w-0 flex-1 basis-64">
            <h2 className="break-words font-medium">{pool.name}</h2>
            <p className="mt-1 text-sm text-muted">
              {t("{count} Maschinen · maximal {max} gleichzeitig", {
                count: pool.machines.length,
                max: pool.maxMachines,
              })}
            </p>
            <p className="break-all text-sm text-muted">
              {pool.workspaceRoot || "~/.local/share/zentrale/projects"}
            </p>
          </div>
          <div className="flex items-center gap-1">
            <Button
              className="min-h-11"
              size="sm"
              variant="ghost"
              isDisabled={busy}
              isPending={checking}
              onPress={checkPool}
            >
              {t("Verbindung prüfen")}
            </Button>
            <ActionsMenu
              label={t("Aktionen für {name}", { name: pool.name })}
              isDisabled={busy || checking}
              actions={[
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
        <p className="text-xs leading-5 text-muted">
          {t(
            "Zufällige Stichprobe: 10 % der Rechner, mindestens 3, höchstens 8. Kleine Pools werden vollständig geprüft.",
          )}
        </p>
        {checking && (
          <p role="status" className="text-sm text-muted">
            {t("Verbindungen werden geprüft …")}
          </p>
        )}
        <Failure message={error} />
        {result && (
          <section
            aria-label={t("Pool-Verbindungsprüfung")}
            className="space-y-4"
          >
            <Alert
              status={
                successful === result.machines.length ? "success" : "warning"
              }
              role="status"
            >
              <Alert.Indicator />
              <Alert.Content>
                <Alert.Title>{t("Stichprobe geprüft")}</Alert.Title>
                <p className="text-sm">
                  {t(
                    "{successful} von {checked} geprüften Rechnern bereit ({total} im Pool).",
                    {
                      successful,
                      checked: result.machines.length,
                      total: result.totalMachines,
                    },
                  )}
                </p>
              </Alert.Content>
            </Alert>
            <ul className="divide-y divide-border">
              {result.machines.map((member) => (
                <PoolCheckMember
                  key={member.machineId}
                  member={member}
                  checking={checking}
                  onRetry={() => checkMember(member.machineId)}
                />
              ))}
            </ul>
          </section>
        )}
      </Card.Content>
      <PoolAccess keys={keys} keyId={pool.keyId} />
    </Card>
  );
}

function PoolCheckMember({
  member,
  checking,
  onRetry,
}: {
  member: PoolMachineConnectionCheck;
  checking: boolean;
  onRetry: () => Promise<void>;
}) {
  const { t } = useI18n();
  const check = member;

  return (
    <li className="min-w-0 space-y-3 py-4 first:pt-0 last:pb-0">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0 space-y-1">
          <h3 className="break-words text-sm font-medium">{member.name}</h3>
          <p className="break-all font-mono text-xs text-muted">
            {member.host}:{member.port}
          </p>
          {ready(check) && (
            <p className="text-sm">{t("Verbindung erfolgreich")}</p>
          )}
        </div>
        {!ready(check) && !check.hostKey.publicKey && (
          <Button
            size="sm"
            variant="ghost"
            className="min-h-11"
            isDisabled={checking}
            onPress={onRetry}
          >
            {t("Erneut versuchen")}
          </Button>
        )}
      </div>
      {check.connected && check.sandbox.level && (
        <SandboxBadge mode={check.sandbox.mode} status={check.sandbox} />
      )}
      {check.hostKey.publicKey ? (
        <HostKeyTrust challenge={check.hostKey} onTrusted={onRetry} />
      ) : (
        <Failure message={check.error || check.sandbox.error} />
      )}
    </li>
  );
}
