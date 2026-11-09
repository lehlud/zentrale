import { useAPI } from "./api";
import type { Agent } from "./generated";
import { useI18n } from "./preferences";
import { Editor } from "./ui";

export function RenameConversation({
  agent,
  onClose,
}: {
  agent: Agent;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const api = useAPI();
  return (
    <Editor
      title={t("Unterhaltung umbenennen")}
      initial={{ name: agent.name }}
      fields={[{ key: "name", label: "Name", required: true }]}
      onClose={onClose}
      onSave={(value) =>
        api.mutate(
          "mutation($agentId:String!,$name:String!){renameAgent(agentId:$agentId,name:$name)}",
          { agentId: agent.id, name: value.name },
        )
      }
    />
  );
}
