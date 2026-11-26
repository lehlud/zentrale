import {
  agentFields,
  messageFields,
  type Transcript,
} from "./agent-transcript";
import type { Agent, ModelDefaults, Project, SandboxStatus } from "./generated";

const compactAgentFields = agentFields.replace(/\s+summary\b/, "");

// One authenticated request also selects the default main agent server-side.
// No job output, approvals, notes, provider catalog or usage aggregate belongs
// on the first-paint critical path. Pagination continues using recentMessages.
export const conversationBootstrapQuery = `query($projectId:String!, $agentId:String) {
  conversationBootstrap(projectId:$projectId, agentId:$agentId) {
    project { id name emoji archived archivePending ownerId directory managedWorkspace machineIds poolIds commitName commitEmail gitConnectionId repositoryId repositoryName repositorySshUrl repositorySshKeyId repositoryCheckoutPaths }
    agents { ${compactAgentFields} }
    agent { ${compactAgentFields} contextWindow contextTokens }
    messages { ${messageFields} }
    goal { id agentId objective origin status requestedStatus requestReason sourceInputIds revision continuations blockedAttempts blocker reason evidence createdAt updatedAt }
    agentInputs { id agentId projectId text mode status createdAt deliveredAt attachments { id name mimeType size path machineId } }
    agentRunState { agentId phase error repositoryStep repositoryPercent repositoryDetail hostKey { host fingerprint publicKey repositorySourceId previousFingerprint } }
    questions { id title options mode status answer agentId }
    jobs { id command status error startedAt machineId agentId timeoutMs }
    modelDefaults { main { providerId modelId reasoningLevel } approver { providerId modelId reasoningLevel } }
    sandboxMode
    sandboxStatus { machineId mode level checkedAt error }
  }
}`;

export type ConversationBootstrap = Pick<
  Transcript,
  "messages" | "goal" | "agentInputs" | "agentRunState" | "questions" | "jobs"
> & {
  project: Project;
  agents: Agent[];
  agent: Agent | null;
  modelDefaults: ModelDefaults;
  sandboxMode: string;
  sandboxStatus?: SandboxStatus | null;
};

export function bootstrapTranscript(
  value: ConversationBootstrap,
): Transcript | undefined {
  if (!value.agent) return undefined;
  return {
    agent: value.agent,
    messages: value.messages,
    goal: value.goal,
    agentInputs: value.agentInputs,
    agentRunState: value.agentRunState,
    questions: value.questions,
    jobs: value.jobs,
  };
}
