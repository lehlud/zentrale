export type LiveResponse = {
  id: string;
  agentId: string;
  text: string;
  delta?: string;
  revision: number;
  snapshot: boolean;
  status: "running" | "completed" | "failed" | "interrupted";
  createdAt: string;
};

// Keep token frames out of the query cache. Each mounted response subscribes
// only to its own agent, independently of the rest of the application.
export class LiveResponses {
  private responses = new Map<string, LiveResponse>();
  private listeners = new Map<string, Set<() => void>>();

  get(agentId: string) {
    return this.responses.get(agentId);
  }

  subscribe(agentId: string, listener: () => void) {
    const listeners = this.listeners.get(agentId) ?? new Set();
    listeners.add(listener);
    this.listeners.set(agentId, listeners);

    return () => {
      listeners.delete(listener);
      if (!listeners.size) this.listeners.delete(agentId);
    };
  }

  private notify(agentId: string) {
    this.listeners.get(agentId)?.forEach((listener) => listener());
  }

  receive(update: LiveResponse) {
    if (
      typeof update.id !== "string" ||
      typeof update.agentId !== "string" ||
      typeof update.text !== "string"
    ) {
      return;
    }

    const previous = this.responses.get(update.agentId);
    if (
      !update.snapshot &&
      (!previous ||
        previous.id !== update.id ||
        previous.revision + 1 !== update.revision)
    ) {
      return;
    }

    if (previous?.id === update.id && previous.revision > update.revision) {
      return;
    }

    this.responses.set(update.agentId, {
      ...update,
      text: update.snapshot
        ? update.text
        : previous!.text + (update.delta ?? ""),
    });
    this.notify(update.agentId);
  }

  acknowledge(agentId: string, messageId: string) {
    if (this.responses.get(agentId)?.id !== messageId) {
      return;
    }

    this.responses.delete(agentId);
    this.notify(agentId);
  }

  reset() {
    const agents = [...this.responses.keys()];
    this.responses.clear();
    agents.forEach((agentId) => this.notify(agentId));
  }
}
