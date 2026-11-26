import type {
  Agent,
  Job,
  Message,
  Question,
  AgentInput,
  AgentRunState,
  Goal,
} from "./generated";

export const agentFields =
  "id name role status parentId approverId projectId providerId modelId machineId error contextStart summary";

export const messageFields = `id agentId role content wire callId toolName createdAt
    attachments { id name mimeType size path machineId }
    activities { name arguments content network justification }
    change { id path kind patch added removed truncated notice }
    changes { id path kind patch added removed truncated notice }`;

export const olderMessagesQuery = `query($id:String!, $beforeId:String!) {
  messages: recentMessages(agentId:$id, beforeId:$beforeId) { ${messageFields} }
}`;

export const shortChatTranscriptQuery = `query($id:String!) {
  agentInputs(agentId:$id) { id agentId projectId text mode status createdAt deliveredAt attachments { id name mimeType size path machineId } }
  agentRunState(agentId:$id) { agentId phase error repositoryStep repositoryPercent repositoryDetail hostKey { host fingerprint publicKey repositorySourceId previousFingerprint } }
  agent(id:$id) { ${agentFields} contextWindow contextTokens }
  messages: recentMessages(agentId:$id) { ${messageFields} }
  questions(agentId:$id, limit:500) { id title options mode status answer agentId }
  jobs(agentId:$id, limit:100) { id command status error startedAt machineId agentId timeoutMs }
}`;

export type Transcript = {
  goal?: Goal | null;
  goalHistory?: Goal[];
  agentInputs?: AgentInput[];
  agentRunState?: AgentRunState;
  agent: Agent;
  messages: Message[];
  questions: Question[];
  jobs: Job[];
  recentMessages?: Message[];
};
