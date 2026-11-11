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

const agentFields =
  "id name role status parentId approverId projectId providerId modelId machineId error contextStart summary";

const messageFields = `id agentId role content wire callId toolName createdAt
    activities { name arguments content network justification }
    change { id path kind patch added removed truncated notice }
    changes { id path kind patch added removed truncated notice }`;

export const olderMessagesQuery = `query($id:String!, $beforeId:String!) {
  messages: recentMessages(agentId:$id, beforeId:$beforeId) { ${messageFields} }
}`;

export const transcriptQuery = `query($id:String!) {
  goal(agentId:$id) { id agentId objective origin status requestedStatus requestReason sourceInputIds revision continuations blockedAttempts blocker reason evidence createdAt updatedAt }
  agentInputs(agentId:$id) { id agentId projectId text mode status createdAt deliveredAt }
  agentRunState(agentId:$id) { agentId phase error hostKey { host fingerprint publicKey } }
  agent(id:$id) { ${agentFields} }
  messages: recentMessages(agentId:$id) { ${messageFields} }
  approvals(agentId:$id, limit:500) { id tool arguments allowed reason blanket createdAt }
  questions(agentId:$id, limit:500) { id title options status answer agentId }
  jobs(agentId:$id, limit:500) { id command status output error startedAt machineId agentId }
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
