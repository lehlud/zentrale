import { Link, Tabs } from "@heroui/react";

import { useAPI, useQuery } from "./api";
import { ChatModelSelect, chatModelFields } from "./chat-model-select";
import type { ModelChoice, Provider } from "./generated";
import { PresetSelect } from "./model-presets";
import type { ModelSelection, ModelSelections } from "./model-selection";
import { ModelChoiceForm } from "./models";
import { useI18n } from "./preferences";
import { Failure, Inspect, Loading, useAction } from "./ui";

const projectModelQuery = `query($agentId:String!) {
  conversationModels(agentId:$agentId) {
    main { providerId modelId reasoningLevel }
    approver { providerId modelId reasoningLevel }
  }
  conversationModelPresets(agentId:$agentId) { providerId modelId reasoningLevel }
  modelProviders { id name }
}`;

const chatModelQuery = `query($agentId:String!) {
  conversationModels(agentId:$agentId) { main { ${chatModelFields} } }
  conversationModelPresets(agentId:$agentId) { ${chatModelFields} }
}`;

type ChatModelData = {
  conversationModels: { main: ModelChoice };
  conversationModelPresets: ModelChoice[] | null;
};

const changeModel =
  "mutation($agentId:String!,$role:String!,$choice:ModelChoiceInput!){setConversationModel(agentId:$agentId,role:$role,choice:$choice)}";

export function ShortChatModelPicker({ agentId }: { agentId: string }) {
  const api = useAPI();
  const action = useAction();
  const { data, error } = useQuery<ChatModelData>(chatModelQuery, { agentId });

  if (!data?.conversationModels)
    return error ? <Failure message={error} /> : null;

  const value = data.conversationModels.main;
  const presets =
    data.conversationModelPresets ?? (value.modelId ? [value] : []);

  return (
    <div className="min-w-0 max-w-[50%]">
      <ChatModelSelect
        value={value}
        presets={presets}
        disabled={action.busy}
        onChange={(choice) =>
          action.run(() =>
            api.mutate(changeModel, { agentId, role: "main", choice }),
          )
        }
      />
      <Failure message={action.error} />
    </div>
  );
}

export function ConversationModelDialog({
  agentId,
  shortChat = false,
  onClose,
}: {
  agentId: string;
  shortChat?: boolean;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const api = useAPI();
  const action = useAction();
  const { data, error, loading } = useQuery<{
    conversationModels: ModelSelections;
    conversationModelPresets?: ModelSelection[] | null;
    modelProviders: Pick<Provider, "id" | "name">[];
  }>(projectModelQuery, { agentId });
  const roles = ["main", "approver"] as const;
  const providers = data?.modelProviders ?? [];

  function picker(role: (typeof roles)[number]) {
    if (!data) return null;

    const choice = data.conversationModels[role];
    const presets = data.conversationModelPresets;
    const selectModel = async (choice: ModelSelection) => {
      await api.mutate(
        "mutation($agentId:String!,$role:String!,$choice:ModelChoiceInput!){setConversationModel(agentId:$agentId,role:$role,choice:$choice)}",
        { agentId, role, choice },
      );
    };

    if (presets) {
      return presets.length ? (
        <PresetSelect
          label={t("Modell")}
          placeholder={t("Modell auswählen")}
          value={choice}
          presets={presets}
          providers={providers}
          disabled={action.busy}
          allowEmpty={false}
          onChange={(choice) => action.run(() => selectModel(choice))}
        />
      ) : (
        <p className="text-sm text-muted">{t("Keine Modelle freigegeben.")}</p>
      );
    }

    return (
      <ModelChoiceForm
        providers={providers}
        role={role}
        value={choice}
        onApply={selectModel}
      />
    );
  }

  return (
    <Inspect
      title={t("Modelle der Unterhaltung")}
      onClose={onClose}
      placement="top"
    >
      <Failure
        message={error || action.error}
        onRetry={error ? () => api.refresh() : undefined}
      />
      {loading && <Loading />}
      {data &&
        (data.conversationModelPresets || providers.length ? (
          shortChat ? (
            picker("main")
          ) : (
            <Tabs variant="secondary" align="start" className="gap-4">
              <Tabs.ListContainer>
                <Tabs.List aria-label={t("Modelle der Unterhaltung")}>
                  {roles.map((role) => (
                    <Tabs.Tab key={role} id={role} className="min-h-11">
                      {t(role === "main" ? "Hauptagent" : "Auto-Approver")}
                      <Tabs.Indicator />
                    </Tabs.Tab>
                  ))}
                </Tabs.List>
              </Tabs.ListContainer>
              {roles.map((role) => (
                <Tabs.Panel key={role} id={role} className="space-y-4 p-0">
                  {picker(role)}
                </Tabs.Panel>
              ))}
            </Tabs>
          )
        ) : (
          <p className="text-sm text-muted">
            {t("Zuerst einen Provider verbinden und seine Modelle laden.")}{" "}
            <Link href="#/providers" onPress={onClose}>
              {t("Provider konfigurieren")}
            </Link>
          </p>
        ))}
    </Inspect>
  );
}
