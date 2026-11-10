import type { Agent } from "./generated";

export type AgentBranch = { agent: Agent; children: AgentBranch[] };

export function agentTree(agents: Agent[]): AgentBranch[] {
  const conversations = agents.filter((agent) => agent.role !== "approver");
  const ids = new Set(conversations.map((agent) => agent.id));
  const children = new Map<string, Agent[]>();
  for (const agent of conversations) {
    const siblings = children.get(agent.parentId) ?? [];
    siblings.push(agent);
    children.set(agent.parentId, siblings);
  }

  const visited = new Set<string>();
  const branch = (agent: Agent): AgentBranch => {
    visited.add(agent.id);
    return {
      agent,
      children: (children.get(agent.id) ?? [])
        .filter((child) => !visited.has(child.id))
        .map(branch),
    };
  };
  const roots = conversations
    .filter((agent) => !ids.has(agent.parentId))
    .map(branch);
  // Keep an orphan or malformed cycle navigable without recursive rendering.
  for (const agent of conversations) {
    if (!visited.has(agent.id)) roots.push(branch(agent));
  }
  return roots;
}

export function branchContains(branch: AgentBranch, id: string): boolean {
  return (
    branch.agent.id === id ||
    branch.children.some((child) => branchContains(child, id))
  );
}
