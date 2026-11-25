import { Link, Tabs } from "@heroui/react";

import { useAPI, useQuery } from "./api";
import type { ModelDefaults, Provider } from "./generated";
import { ModelChoiceForm } from "./models";
import { useI18n } from "./preferences";
import { Failure, Inspect, Loading } from "./ui";

const query = `query($agentId:String!) {
  conversationModels(agentId:$agentId) {
    main { providerId modelId reasoningLevel }
    approver { providerId modelId reasoningLevel }
  }
  modelProviders { id name }
}`;

export function ConversationModelDialog({
  agentId,
  onClose,
}: {
  agentId: string;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const api = useAPI();
  const { data, error, loading } = useQuery<{
    conversationModels: ModelDefaults;
    modelProviders: Pick<Provider, "id" | "name">[];
  }>(query, { agentId });
  const roles = ["main", "approver"] as const;
  const providers = data?.modelProviders ?? [];
  return (
    <Inspect
      title={t("Modelle der Unterhaltung")}
      onClose={onClose}
      placement="top"
    >
      <Failure message={error} onRetry={() => api.refresh()} />
      {loading && <Loading />}
      {data &&
        (providers.length ? (
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
            {roles.map((role) => {
              const choice = data.conversationModels[role];
              return (
                <Tabs.Panel key={role} id={role} className="space-y-4 p-0">
                  {choice.modelId && (
                    <p className="break-all font-mono text-xs text-muted">
                      {choice.modelId}
                    </p>
                  )}
                  <ModelChoiceForm
                    providers={providers}
                    role={role}
                    value={choice}
                    onApply={async (choice) => {
                      await api.mutate(
                        "mutation($agentId:String!,$role:String!,$choice:ModelChoiceInput!){setConversationModel(agentId:$agentId,role:$role,choice:$choice)}",
                        { agentId, role, choice },
                      );
                    }}
                  />
                </Tabs.Panel>
              );
            })}
          </Tabs>
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
