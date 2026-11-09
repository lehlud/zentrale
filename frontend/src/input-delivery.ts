import { ConnectionError } from "./connection";
import type { AgentInput } from "./generated";

export type OutgoingInput = {
  id: string;
  agentId: string;

  text: string;
  mode: "steer" | "queue";

  status: "sending" | "unconfirmed" | "failed" | "accepted";
  error: string;
};

const storageKey = "zentrale:outgoing-inputs";

const mutation =
  "mutation($agentId:String!,$id:String!,$text:String!,$mode:String!){submitInput(agentId:$agentId,id:$id,text:$text,mode:$mode){id agentId text mode status createdAt deliveredAt projectId}}";

// This delivery service outlives the composer and navigation. Only this
// idempotent mutation is replayed after a lost response, with the original ID.
export class InputDelivery {
  private inputs: OutgoingInput[] = [];
  private listeners = new Set<() => void>();

  private active = new Set<string>();
  private version = 0;

  constructor(
    private send: (
      query: string,
      variables: Record<string, unknown>,
    ) => Promise<{ submitInput: AgentInput }>,
    private refresh: () => void,
  ) {
    try {
      const saved = JSON.parse(localStorage.getItem(storageKey) ?? "[]");
      if (Array.isArray(saved))
        this.inputs = saved
          .filter(
            (input) =>
              input &&
              typeof input.id === "string" &&
              typeof input.agentId === "string" &&
              typeof input.text === "string" &&
              ["steer", "queue"].includes(input.mode),
          )
          .map((input) => ({ ...input, status: "unconfirmed", error: "" }));
    } catch {
      /* Storage may be unavailable. */
    }
  }

  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  snapshot = () => this.version;

  forAgent = (id: string) =>
    this.inputs.filter((input) => input.agentId === id);

  submit(agentId: string, text: string, mode: "steer" | "queue") {
    const input: OutgoingInput = {
      id: crypto.randomUUID(),
      agentId,
      text,
      mode,
      status: "sending",
      error: "",
    };
    this.inputs = [...this.inputs, input];
    this.changed();
    void this.deliver(input.id);
  }

  flush = () => {
    for (const input of this.inputs)
      if (input.status === "unconfirmed") void this.deliver(input.id);
  };

  acknowledge(ids: Set<string>) {
    const next = this.inputs.filter((input) => !ids.has(input.id));
    if (next.length !== this.inputs.length) {
      this.inputs = next;
      this.changed();
    }
  }

  retry = (id: string) => {
    void this.deliver(id);
  };

  private async deliver(id: string) {
    const input = this.inputs.find((input) => input.id === id);
    if (!input || this.active.has(id)) {
      return;
    }

    this.active.add(id);
    this.update(id, { status: "sending", error: "" });

    try {
      const result = await this.send(mutation, {
        agentId: input.agentId,
        id,
        text: input.text,
        mode: input.mode,
      });
      if (result.submitInput?.id !== input.id) {
        throw new Error("Server did not confirm the message receipt");
      }

      this.update(id, { status: "accepted", error: "" });
      this.refresh();
    } catch (error) {
      this.update(id, {
        status: error instanceof ConnectionError ? "unconfirmed" : "failed",
        error: (error as Error).message,
      });
    } finally {
      this.active.delete(id);
    }
  }

  private update(id: string, values: Partial<OutgoingInput>) {
    this.inputs = this.inputs.map((input) =>
      input.id === id ? { ...input, ...values } : input,
    );
    this.changed();
  }

  private changed() {
    try {
      localStorage.setItem(
        storageKey,
        JSON.stringify(
          this.inputs.filter((input) => input.status !== "accepted"),
        ),
      );
    } catch {
      /* The live request still runs if storage is unavailable. */
    }

    this.version++;
    this.listeners.forEach((listener) => listener());
  }
}
