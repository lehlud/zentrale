import type {
  Agent,
  Approval,
  Job,
  Message,
  Note,
  Question,
  UsageSummary,
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

export const transcriptQuery = `query($id:String!) {
  goal(agentId:$id) { id agentId objective origin status requestedStatus requestReason sourceInputIds revision continuations blockedAttempts blocker reason evidence createdAt updatedAt }
  agentInputs(agentId:$id) { id agentId projectId text mode status createdAt deliveredAt attachments { id name mimeType size path machineId } }
  agentRunState(agentId:$id) { agentId phase error hostKey { host fingerprint publicKey } }
  agent(id:$id) { ${agentFields} }
  messages: recentMessages(agentId:$id) { ${messageFields} }
  questions(agentId:$id, limit:500) { id title options status answer agentId }
  jobs(agentId:$id, limit:100) { id command status error startedAt machineId agentId timeoutMs }
}`;

// Detail collections must not gate initial conversation rendering. In managed
// mode the parent supplies the bootstrap core; this query is opened on demand.
export const transcriptDetailsQuery = `query($id:String!) {
  agent(id:$id) { id summary }
  approvals(agentId:$id, limit:500) { id tool arguments allowed reason blanket createdAt }
  jobs(agentId:$id, limit:100) { id command status error startedAt machineId agentId timeoutMs }
  usageSummary(agentId:$id) {
    inputTokens outputTokens cachedTokens cacheInputTokens cacheReadTokens cacheWriteTokens
    unknownTokenCounts unknownCacheWrites realCost fictionalCost estimated unknownPrices requestCount
  }
  notes(agentId:$id) { id title content agentId }
}`;

export type Transcript = {
  goal?: Goal | null;
  goalHistory?: Goal[];
  agentInputs?: AgentInput[];
  agentRunState?: AgentRunState;
  agent: Agent;
  messages: Message[];
  approvals: Approval[];
  questions: Question[];
  jobs: Job[];
  usageSummary: UsageSummary;
  recentMessages?: Message[];
  notes: Note[];
};
