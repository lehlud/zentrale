import { Button, Tabs } from "@heroui/react";
import { useState } from "react";

import { MachineCard } from "./machine-card";
import { MachineAccess } from "./machine-access";
import { PoolCard } from "./pool-card";
import { CONFIG, useAPI, useQuery } from "./api";
import { MoreButton, useBatch } from "./batches";
import type { Config } from "./config";
import { useI18n } from "./preferences";
import type { Field } from "./ui";
import { Editor, Empty, Failure, Loading, Title, useAction } from "./ui";
import { Icon } from "./icons";

const sandbox = [
  { key: "auto", label: "Automatisch" },
  { key: "microvm", label: "MicroVM" },
  { key: "bwrap", label: "bwrap + cgroups" },
  { key: "none", label: "Nur Auto-Approval" },
];

export function Machines() {
  const { t } = useI18n();

  const api = useAPI();
  const { data, error, loading } = useQuery<Config>(CONFIG);
  const [editing, setEditing] = useState<Record<string, unknown> | null>(null);
  const [poolEditor, setPoolEditor] = useState(false);
  const action = useAction();
  const machines = useBatch(data?.machines ?? []);
  const pools = useBatch(data?.pools ?? []);
  const fields: Field[] = [
    { key: "name", label: "Name", required: true },
    { key: "host", label: "Host", required: true },
    { key: "port", label: "SSH-Port", type: "number" },
    { key: "user", label: t("Benutzer"), required: true },
    {
      key: "workspaceRoot",
      label: t("Persistentes Projektverzeichnis"),
      required: true,
      placeholder: "~/.local/share/zentrale/projects",
      description: t(
        "Alle neuen Projekte erhalten hier automatisch einen eigenen Ordner. ~/ bezieht sich auf den SSH-Benutzer.",
      ),
    },
    {
      key: "keyId",
      label: t("SSH-Schlüssel"),
      type: "select",
      required: true,
      options: data?.sshKeys
        .filter(
          (key) =>
            key.canDelete ||
            (key.purpose === "machine" && key.purposeId === editing?.id) ||
            key.id === Number(editing?.keyId),
        )
        .map((k) => ({
          key: String(k.id),
          label: t("Schlüssel {id}", { id: k.id ?? "" }),
        })),
    },
    {
      key: "jumps",
      label: t("Jump-Hosts (in Reihenfolge)"),
      type: "multi",
      options: data?.machines
        .filter((m) => m.id !== editing?.id)
        .map((m) => ({ key: m.id, label: m.name })),
    },
    { key: "sandbox", label: "Sandbox", type: "select", options: sandbox },
    {
      key: "gpuDevicesText",
      label: t("GPU-PCI-Geräte"),
      placeholder: "0000:01:00.0, 0000:01:00.1",
      description: t(
        "Die Eigenschaft GPU aktiviert die automatische Erkennung, auch für NVIDIA ohne Root-Rechte. Explizite PCI-Adressen benötigen administrative VFIO-Freigabe und passende Gasttreiber.",
      ),
    },
    { key: "propertiesText", label: t("Eigenschaften (kommagetrennt)") },
    {
      key: "microVMCommand",
      label: t("Eigener MicroVM-Runner (optional)"),
      placeholder: "/opt/microvm-runner",
      description: t(
        "Leer lassen, um den integrierten QEMU/KVM-Runner zu verwenden.",
      ),
    },
    {
      key: "singleUser",
      label: t("Nur als alleiniger Benutzer"),
      type: "check",
    },
    {
      key: "containers",
      label: t("Container-Runtime freigeben"),
      type: "check",
    },
  ];
  const remove = (kind: string, id: string) =>
    action.run(() =>
      api.mutate(
        "mutation($kind:String!,$id:String!){deleteResource(kind:$kind,id:$id)}",
        { kind, id },
      ),
    );

  return (
    <>
      <Title
        description={t(
          "SSH-Zugänge und Ausführungsumgebungen für deine Projekte.",
        )}
        action={
          <Button
            className="min-h-11"
            onPress={() =>
              setEditing({
                name: "",
                port: 22,
                sandbox: "auto",
                jumps: [],
                properties: [],
                workspaceRoot: "~/.local/share/zentrale/projects",
              })
            }
          >
            <Icon name="plus" />
            {t("Maschine hinzufügen")}
          </Button>
        }
      >
        {t("Maschinen")}
      </Title>
      <Failure message={error || action.error} onRetry={() => api.refresh()} />
      <Tabs variant="secondary" align="start" className="gap-5">
        <Tabs.ListContainer>
          <Tabs.List aria-label={t("Maschinentyp")}>
            <Tabs.Tab
              className="min-h-11 w-auto shrink-0 whitespace-nowrap"
              id={"single"}
            >
              {t("Fernmaschinen")}
              <Tabs.Indicator />
            </Tabs.Tab>
            <Tabs.Tab
              className="min-h-11 w-auto shrink-0 whitespace-nowrap"
              id={"pools"}
            >
              {"Pools"}
              <Tabs.Indicator />
            </Tabs.Tab>
          </Tabs.List>
        </Tabs.ListContainer>
        <Tabs.Panel className="p-0" id={"single"}>
          <div className="space-y-3">
            {loading && !data ? (
              <Loading />
            ) : !data && error ? null : !data?.machines.length ? (
              <Empty>{t("Keine Fernmaschinen")}</Empty>
            ) : (
              machines.items.map((machine) => (
                <MachineCard
                  key={JSON.stringify(machine)}
                  machine={machine}
                  sandboxStatus={data?.sandboxStatuses?.find(
                    (status) => status.machineId === machine.id,
                  )}
                  onEdit={() =>
                    setEditing({
                      ...machine,
                      keyId: String(machine.keyId),
                      workspaceRoot:
                        machine.workspaceRoot ||
                        "~/.local/share/zentrale/projects",
                      propertiesText: machine.properties.join(", "),
                      gpuDevicesText: (machine.gpuDevices || []).join(", "),
                    })
                  }
                  onRemove={() => void remove("machine", machine.id)}
                />
              ))
            )}
          </div>
          <MoreButton batch={machines} />
        </Tabs.Panel>
        <Tabs.Panel className="p-0" id={"pools"}>
          <div className="space-y-3">
            <Button
              className="min-h-11"
              variant="secondary"
              onPress={() => setPoolEditor(true)}
            >
              {t("YAML importieren / aktualisieren")}
            </Button>
            {!data && error ? null : !data?.pools.length ? (
              <Empty>{t("Keine Pools")}</Empty>
            ) : (
              pools.items.map((pool) => (
                <PoolCard
                  key={JSON.stringify(pool)}
                  pool={pool}
                  keys={data?.sshKeys ?? []}
                  busy={action.busy}
                  onRemove={() => void remove("pool", pool.id)}
                />
              ))
            )}
          </div>
          <MoreButton batch={pools} />
        </Tabs.Panel>
      </Tabs>
      {editing && (
        <Editor
          title={editing.id ? t("Maschine bearbeiten") : t("Neue Fernmaschine")}
          fields={fields}
          steps={
            editing.id
              ? undefined
              : [
                  {
                    id: "connection",
                    title: t("Wie erreichen wir die Maschine?"),
                    description: t(
                      "Name und SSH-Adresse der Fernmaschine festlegen.",
                    ),
                    fields: ["name", "host", "port", "user"],
                  },
                  {
                    id: "access",
                    title: t("Wie meldet sich die Zentrale an?"),
                    description: t(
                      "Wähle einen SSH-Schlüssel und bei Bedarf Jump-Hosts.",
                    ),
                    fields: ["keyId", "jumps"],
                    showExtra: true,
                  },
                  {
                    id: "environment",
                    title: t("Wie sollen Agenten hier arbeiten?"),
                    description: t(
                      "Automatisch wählt die stärkste verfügbare Sandbox.",
                    ),
                    fields: [
                      "workspaceRoot",
                      "sandbox",
                      "singleUser",
                      "containers",
                    ],
                    advancedFields: [
                      "gpuDevicesText",
                      "propertiesText",
                      "microVMCommand",
                    ],
                  },
                ]
          }
          renderExtra={(value, patch, stepId) =>
            stepId === "access" && (
              <MachineAccess
                keys={data?.sshKeys ?? []}
                keyId={value.keyId}
                onChoose={(keyId) => patch({ keyId })}
              />
            )
          }
          initial={editing}
          onClose={() => setEditing(null)}
          onSave={(value) => {
            const { propertiesText, gpuDevicesText, ...input } = value;
            return api.mutate(
              "mutation($input:MachineInput!){saveMachine(input:$input){id}}",
              {
                input: {
                  ...input,
                  keyId: Number(input.keyId),
                  gpuDevices: String(gpuDevicesText ?? "")
                    .split(",")
                    .map((s) => s.trim())
                    .filter(Boolean),
                  properties: String(propertiesText ?? "")
                    .split(",")
                    .map((s) => s.trim())
                    .filter(Boolean),
                },
              },
            );
          }}
        />
      )}
      {poolEditor && (
        <Editor
          title={t("Pool-Konfiguration")}
          className="sm:max-w-4xl"
          fields={[
            {
              key: "yaml",
              label: "YAML",
              type: "yaml",
              required: true,
              description:
                t(
                  "Den Anmeldeschlüssel verwaltet die Zentrale automatisch. Hostschlüssel und CAs können unter known_hosts hinterlegt werden.",
                ) +
                " " +
                t(
                  "workspace_root muss auf allen Pool-Mitgliedern dieselbe persistente Freigabe bezeichnen. Bei gemeinsamem Home ist auch ein Pfad unter ~/ möglich.",
                ),
            },
          ]}
          initial={{
            yaml: "id: rechenpool\nname: Rechenpool\nmax_machines: 1\nshared_home: false\nworkspace_root: /srv/zentrale/projects\nsingle_user: true\nsandbox: auto\nmachines:\n  - id: worker-1\n    name: Worker 1\n    host: worker.example.org\n    user: user\n    properties: [GPU]\n",
          }}
          onClose={() => setPoolEditor(false)}
          onSave={(v) =>
            api.mutate("mutation($yaml:String!){savePool(yaml:$yaml){id}}", v)
          }
        />
      )}
    </>
  );
}
