import type { ReactNode } from "react";
import { createContext, useContext, useEffect, useMemo, useState } from "react";

import {
  ConnectionError,
  ConnectionMonitor,
  connectionErrorMessage,
} from "./connection";
import { fetchGraphQL } from "./transport";
import { LiveResponses } from "./live-responses";
import { InputDelivery } from "./input-delivery";

export class API {
  readonly responses = new LiveResponses();

  readonly inputs = new InputDelivery(
    async (query, variables) => {
      const controller = new AbortController();
      const timeout = setTimeout(() => controller.abort(), 15000);

      try {
        return await this.query(query, variables, controller.signal, true);
      } catch (error) {
        if (controller.signal.aborted) {
          this.connection.reportFailure();
          throw new ConnectionError(error);
        }
        throw error;
      } finally {
        clearTimeout(timeout);
      }
    },
    () => this.refresh(),
  );

  readonly connection = new ConnectionMonitor(
    (signal) => fetchGraphQL("query{status{authenticated}}", {}, signal),
    () => {
      this.refresh();
      this.inputs.flush();
    },
  );

  confirm:
    ((message: string, actionLabel?: string) => Promise<boolean>) | undefined;
  private pendingMutations = new Map<string, Promise<unknown>>();
  private connectionGeneration = 0;

  private listeners = new Set<() => void>();
  private version = 0;

  private socket: WebSocket | null = null;
  private reconnect: ReturnType<typeof setTimeout> | undefined;
  private stopped = true;
  private lastEvent: number | undefined;

  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  snapshot = () => this.version;

  refresh = () => {
    this.version++;
    this.listeners.forEach((fn) => fn());
  };

  async query<T>(
    query: string,
    variables: Record<string, unknown> = {},
    signal?: AbortSignal,
    keepalive = false,
  ): Promise<T> {
    try {
      return await fetchGraphQL<T>(query, variables, signal, keepalive);
    } catch (error) {
      if (error instanceof ConnectionError) this.connection.reportFailure();
      throw error;
    }
  }

  async mutate<T>(
    query: string,
    variables: Record<string, unknown> = {},
    options: { announce?: boolean; refresh?: boolean } = {},
  ): Promise<T> {
    const key = JSON.stringify({ query, variables });
    const existing = this.pendingMutations.get(key);
    if (existing) {
      return existing as Promise<T>;
    }

    const operation = (async () => {
      const destructive =
        /deleteResource|deleteSSHKey|revokeRepositoryKey|disconnectGitConnection|disconnectProvider|stopJob|stopEditor/.test(
          query,
        ) ||
        (/trustHostKey/.test(query) && variables.trust === "distrust");
      if (
        destructive &&
        this.confirm &&
        !(await this.confirm(
          query.includes("stopEditor")
            ? "Editor wirklich beenden? Nicht gespeicherte Änderungen können verloren gehen."
            : query.includes("stopJob")
              ? "Diesen Job wirklich beenden?"
              : query.includes("revokeRepositoryKey")
                ? "Repo-Zugriff beim Anbieter widerrufen und den Schlüssel dauerhaft löschen?"
                : query.includes("disconnectProvider") ||
                    query.includes("disconnectGitConnection")
                  ? "Zugang wirklich trennen?"
                  : variables.kind === "agent"
                    ? "Agentenbaum und zugehörigen Verlauf dauerhaft entfernen?"
                    : variables.trust === "distrust"
                      ? "Vertrauen wirklich entziehen?"
                      : "Dieses Element dauerhaft entfernen?",
          query.includes("stopEditor")
            ? "Editor beenden"
            : query.includes("stopJob")
              ? "Job beenden"
              : query.includes("revokeRepositoryKey")
                ? "Zugriff widerrufen"
                : query.includes("disconnectProvider") ||
                    query.includes("disconnectGitConnection")
                  ? "Trennen"
                  : variables.trust === "distrust"
                    ? "Vertrauen entziehen"
                    : "Entfernen",
        ))
      ) {
        throw Object.assign(new Error("Action cancelled"), {
          name: "CancelledMutation",
        });
      }

      const result = await this.query<T>(query, variables);
      if (options.refresh !== false) this.refresh();

      if (options.announce !== false)
        window.dispatchEvent(
          new CustomEvent("zentrale:notice", { detail: "Erfolgreich" }),
        );

      return result;
    })();
    this.pendingMutations.set(key, operation);

    try {
      return await operation;
    } finally {
      this.pendingMutations.delete(key);
    }
  }

  connect() {
    this.stopped = false;
    this.inputs.flush();
    const generation = ++this.connectionGeneration;
    const open = () => {
      if (this.stopped || generation !== this.connectionGeneration) {
        return;
      }

      const socket = new WebSocket(
        `${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}/events?after=${this.lastEvent ?? "latest"}`,
      );
      this.socket = socket;
      socket.onmessage = (event) => {
        const data = JSON.parse(event.data);
        if (data.type === "response_reset") {
          this.responses.reset();
          return;
        }

        if (data.type === "response") {
          this.responses.receive(JSON.parse(data.data));
          return;
        }

        if (data.type === "sync" || data.id > (this.lastEvent ?? 0)) {
          this.lastEvent = data.id;
          this.refresh();
        }
      };
      socket.onclose = () => {
        if (!this.stopped && generation === this.connectionGeneration) {
          if (!this.connection.snapshot()) this.refresh();
          this.reconnect = setTimeout(open, 2000);
        }
      };
    };
    open();
  }

  disconnect() {
    this.stopped = true;
    this.connectionGeneration++;
    clearTimeout(this.reconnect);
    this.socket?.close();
    this.socket = null;
  }
}

const APIContext = createContext<API | null>(null);

export function Services({ api, children }: { api: API; children: ReactNode }) {
  return <APIContext.Provider value={api}>{children}</APIContext.Provider>;
}

export function useAPI() {
  const api = useContext(APIContext);
  if (!api) {
    throw new Error("API service missing");
  }

  return api;
}

export function useQuery<T>(
  query: string,
  variables: Record<string, unknown> = {},
) {
  const api = useAPI();
  const key = JSON.stringify(variables);
  const stableVars = useMemo(
    () => JSON.parse(key) as Record<string, unknown>,
    [key],
  );
  const requestKey = query + key;
  const [result, setResult] = useState<{ key: string; value: T }>();
  const data = result?.key === requestKey ? result.value : undefined;
  const [failure, setFailure] = useState<{ key: string; message: string }>();
  const error = failure?.key === requestKey ? failure.message : "";
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const controller = new AbortController();
    let running = false;
    let dirty = false;
    const load = () => {
      if (controller.signal.aborted) return;
      if (running) {
        dirty = true;
        return;
      }
      running = true;
      dirty = false;
      void api
        .query<T>(query, stableVars, controller.signal)
        .then((value) => {
          if (controller.signal.aborted) {
            return;
          }

          setResult({ key: requestKey, value });
          setFailure(undefined);
        })
        .catch((err) => {
          if (!controller.signal.aborted && err.name !== "AbortError")
            setFailure({ key: requestKey, message: String(err.message) });
        })
        .finally(() => {
          running = false;
          if (!controller.signal.aborted) {
            setLoading(false);
            // Complete the current read, then fetch the newest state once.
            // Busy event streams must not starve initial loading by repeatedly
            // aborting requests before they have a chance to finish.
            if (dirty) load();
          }
        });
    };
    const unsubscribe = api.subscribe(load);
    load();

    return () => {
      unsubscribe();
      controller.abort();
    };
  }, [api, query, stableVars]);

  return {
    data,
    error,
    loading: !data && (loading || !error || error === connectionErrorMessage),
  };
}

export const CONFIG = `query {
  sandboxStatuses { machineId mode level checkedAt error }
  machines(limit:500) {
    id name host port user keyId jumps properties sandbox singleUser containers microVMCommand workspaceRoot vmType gpuDevices storage { id path shared description } quotaCommand
  }
  pools(limit:500) {
    id name keyId maxMachines sharedHome workspaceRoot singleUser sandbox vmType storage { id path shared description } quotaCommand machines { id name host user vmType gpuDevices }
  }
  providers(limit:500) {
    id name kind baseUrl authMode billingMode connected accountLabel catalogStatus catalogError modelCount
  }
  sshKeys { id publicKey fingerprint purpose purposeId canDelete canRevoke usages { kind id name projectId } }
  trustedKeys { publicKey fingerprint }
  projects(limit:500) { id name directory managedWorkspace machineIds poolIds commitName commitEmail gitConnectionId repositoryId repositoryName }
  commitIdentity { name email }
  modelDefaults { main { providerId modelId reasoningLevel } approver { providerId modelId reasoningLevel } }
  providerKinds { id name authMethods }
}`;
