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
import { sameJSONValue } from "./stable-data";
import { uploadAttachment } from "./attachments";
import { mutationConfirmation } from "./mutation-confirmation";
import {
  AuthenticationError,
  requiresAuthentication,
  SESSION_EXPIRED,
  SESSION_QUERY,
  type GraphQLResult,
  type SessionData,
} from "./session";

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
    () => this.notifyListeners(),
  );

  readonly connection = new ConnectionMonitor(
    async () => {
      if (this.stopped) {
        // Explicit reconnect before the very first session read succeeded.
        // No authenticated socket exists yet. Retry that bootstrap once,
        // never on a timer or as an HTTP fallback for live subscriptions.
        await this.bootstrapSession();
        return;
      }
      this.reconnectNow();
      // Recovery is established by the authenticated WebSocket handshake,
      // never by repeated HTTP health/data requests.
      throw new ConnectionError("WebSocket reconnecting");
    },
    () => {
      if (!this.authenticationRequired) this.inputs.flush();
    },
    false,
  );

  confirm:
    ((message: string, actionLabel?: string) => Promise<boolean>) | undefined;
  authenticationRequired = false;
  private pendingMutations = new Map<string, Promise<unknown>>();
  private connectionGeneration = 0;
  private sessionGeneration = 0;

  private listeners = new Set<() => void>();
  private version = 0;

  private socket: WebSocket | null = null;
  private reconnect: ReturnType<typeof setTimeout> | undefined;
  private stopped = true;
  private sessionCheck: AbortController | undefined;
  private lastEvent: number | undefined;
  private nextSubscription = 0;
  private hiddenConversations = new Map<string, number>();
  private subscriptions = new Map<
    string,
    {
      query: string;
      variables: Record<string, unknown>;
      receive: (result: GraphQLResult) => void;
    }
  >();

  private conversationHidden(subscription: {
    query: string;
    variables: Record<string, unknown>;
  }) {
    return (
      this.hiddenConversations.has(
        String(subscription.variables.projectId ?? ""),
      ) && /\bconversationBootstrap\s*\(/.test(subscription.query)
    );
  }

  // A fullscreen terminal completely covers the conversation. Unsubscribe
  // instead of decoding/reconciling megabyte snapshots nobody can see. Keep
  // the last UI snapshot and restore one server-pushed snapshot on release.
  suspendConversation(projectId: string) {
    const previous = this.hiddenConversations.get(projectId) ?? 0;
    this.hiddenConversations.set(projectId, previous + 1);
    if (!previous && this.socket?.readyState === WebSocket.OPEN) {
      for (const [requestId, subscription] of this.subscriptions) {
        if (
          subscription.variables.projectId === projectId &&
          this.conversationHidden(subscription)
        ) {
          this.socket.send(JSON.stringify({ type: "unsubscribe", requestId }));
        }
      }
    }
    let released = false;
    return () => {
      if (released) return;
      released = true;
      const remaining = (this.hiddenConversations.get(projectId) ?? 1) - 1;
      if (remaining) {
        this.hiddenConversations.set(projectId, remaining);
        return;
      }
      this.hiddenConversations.delete(projectId);
      for (const [requestId, subscription] of this.subscriptions) {
        if (
          subscription.variables.projectId === projectId &&
          /\bconversationBootstrap\s*\(/.test(subscription.query)
        ) {
          this.sendSubscription(requestId, subscription);
        }
      }
    };
  }

  watch<T>(
    query: string,
    variables: Record<string, unknown>,
    receive: (value: T) => void,
    fail: (error: Error) => void,
  ) {
    const requestId = String(++this.nextSubscription);
    const subscription = {
      query,
      variables,
      receive: (result: GraphQLResult) => {
        if (requiresAuthentication(result.errors)) {
          this.expireSession();
          return;
        }
        if (result.errors?.length)
          fail(
            new Error(result.errors.map((error) => error.message).join("; ")),
          );
        else receive(result.data as T);
      },
    };
    this.subscriptions.set(requestId, subscription);
    this.sendSubscription(requestId, subscription);
    return () => {
      this.subscriptions.delete(requestId);
      if (this.socket?.readyState === WebSocket.OPEN) {
        this.socket.send(JSON.stringify({ type: "unsubscribe", requestId }));
      }
    };
  }

  private sendSubscription(
    requestId: string,
    subscription: { query: string; variables: Record<string, unknown> },
  ) {
    if (this.conversationHidden(subscription)) return;
    if (this.socket?.readyState === WebSocket.OPEN) {
      this.socket.send(
        JSON.stringify({
          type: "subscribe",
          requestId,
          query: subscription.query,
          variables: subscription.variables,
        }),
      );
    }
  }

  private reconnectNow() {
    if (this.stopped) return;
    this.disconnect();
    this.connect();
  }

  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  snapshot = () => this.version;

  private async bootstrapSession() {
    const session = await this.query<SessionData>(SESSION_QUERY);
    this.publishSession(session);
  }

  private publishSession(session: SessionData) {
    this.sessionGeneration++;
    this.authenticationRequired = !session.status.authenticated;
    if (this.authenticationRequired) {
      this.disconnect();
      this.lastEvent = undefined;
      this.responses.reset();
      this.connection.clearFailure();
    }
    for (const subscription of this.subscriptions.values()) {
      if (subscription.query === SESSION_QUERY) {
        subscription.receive({ data: session });
      }
    }
  }

  private expireSession() {
    if (this.authenticationRequired) return;
    this.publishSession({
      status: {
        needsSetup: false,
        authenticated: false,
        githubConnected: false,
      },
    });
  }

  private async request<T>(send: () => Promise<T>): Promise<T> {
    const generation = this.sessionGeneration;
    try {
      return await send();
    } catch (error) {
      // Responses started before a sign-in/out must not invalidate a newer
      // session or resurrect an old outage notification.
      if (generation === this.sessionGeneration) {
        if (error instanceof AuthenticationError) this.expireSession();
        else if (
          error instanceof ConnectionError &&
          !this.authenticationRequired
        )
          this.connection.reportFailure();
      }
      throw error;
    }
  }

  refresh = () => {
    // Explicit user-requested retry: restart the live subscription once. This
    // is never invoked from an event frame, reconnect timer or mutation result.
    if (this.socket?.readyState === WebSocket.OPEN) {
      for (const [requestId, subscription] of this.subscriptions) {
        this.socket.send(JSON.stringify({ type: "unsubscribe", requestId }));
        this.sendSubscription(requestId, subscription);
      }
    } else {
      this.reconnectNow();
    }
    this.notifyListeners();
  };

  private notifyListeners() {
    this.version++;
    this.listeners.forEach((fn) => fn());
  }

  async query<T>(
    query: string,
    variables: Record<string, unknown> = {},
    signal?: AbortSignal,
    keepalive = false,
  ): Promise<T> {
    return this.request(() =>
      fetchGraphQL<T>(query, variables, signal, keepalive),
    );
  }

  uploadAttachment(
    agentId: string,
    id: string,
    file: File,
    signal: AbortSignal,
  ) {
    return this.request(() => uploadAttachment(agentId, id, file, signal));
  }

  // Non-GraphQL callers share session handling without retrying the operation.
  fetch: typeof fetch = (input, options) =>
    this.request(async () => {
      const response = await fetch(input, options);
      if (response.status === 401) throw new AuthenticationError();
      return response;
    });

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
      const confirmation = mutationConfirmation(query, variables);
      if (
        confirmation &&
        this.confirm &&
        !(await this.confirm(confirmation.message, confirmation.actionLabel))
      ) {
        throw Object.assign(new Error("Action cancelled"), {
          name: "CancelledMutation",
        });
      }

      const result = await this.query<T>(query, variables);
      if (/\b(login|logout|setup)\b/.test(query)) {
        // A successful sign-in/out changes the cookie used by the WebSocket
        // handshake. This is a one-off session bootstrap following an explicit
        // user action, not a live-update fallback or a periodic health probe.
        await this.bootstrapSession();
      }
      if (options.refresh !== false) this.notifyListeners();

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
      let opened = false;
      socket.onopen = () => {
        if (this.stopped || generation !== this.connectionGeneration) return;
        opened = true;
        this.sessionCheck?.abort();
        this.sessionCheck = undefined;
        this.connection.reportRestored();
        for (const [requestId, subscription] of this.subscriptions) {
          this.sendSubscription(requestId, subscription);
        }
      };
      socket.onmessage = (event) => {
        if (this.stopped || generation !== this.connectionGeneration) return;
        const data = JSON.parse(event.data);
        if (data.type === "query_result") {
          const subscription = this.subscriptions.get(data.requestId);
          if (
            subscription?.query === SESSION_QUERY &&
            data.result.data?.status?.authenticated === false
          ) {
            this.publishSession(data.result.data);
            return;
          }
          if (subscription && !this.conversationHidden(subscription))
            subscription.receive(data.result);
          return;
        }
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
        }
      };
      socket.onclose = (event) => {
        if (!this.stopped && generation === this.connectionGeneration) {
          if (event.code === SESSION_EXPIRED) {
            this.expireSession();
            return;
          }
          this.connection.reportFailure();
          this.reconnect = setTimeout(open, 2000);
          // Browsers hide the HTTP status of a rejected WebSocket handshake.
          // Check only session status after that failure, never resource data
          // or mutations. Established sockets report expiry with code 4401.
          if (!opened) void this.checkSession(generation);
        }
      };
    };
    open();
  }

  private async checkSession(generation: number) {
    if (this.sessionCheck) return;
    const controller = new AbortController();
    this.sessionCheck = controller;
    const timeout = setTimeout(() => controller.abort(), 5000);
    const current = () =>
      !controller.signal.aborted &&
      !this.stopped &&
      generation === this.connectionGeneration;
    try {
      const session = await fetchGraphQL<SessionData>(
        SESSION_QUERY,
        {},
        controller.signal,
      );
      if (current() && !session.status.authenticated)
        this.publishSession(session);
    } catch (error) {
      if (current() && error instanceof AuthenticationError)
        this.expireSession();
      // An unavailable server is not evidence of an expired session. The
      // normal WebSocket retry remains responsible for network recovery.
    } finally {
      clearTimeout(timeout);
      if (this.sessionCheck === controller) this.sessionCheck = undefined;
    }
  }

  disconnect() {
    this.stopped = true;
    this.connectionGeneration++;
    clearTimeout(this.reconnect);
    this.sessionCheck?.abort();
    this.sessionCheck = undefined;
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
  options: { enabled?: boolean; initialData?: T } = {},
) {
  const api = useAPI();
  const enabled = options.enabled ?? true;
  const key = JSON.stringify(variables);
  const stableVars = useMemo(
    () => JSON.parse(key) as Record<string, unknown>,
    [key],
  );
  const requestKey = query + key;
  const [result, setResult] = useState<{ api: API; key: string; value: T }>();
  const data = !enabled
    ? options.initialData
    : result?.api === api && result.key === requestKey
      ? result.value
      : undefined;
  const [failure, setFailure] = useState<{
    api: API;
    key: string;
    message: string;
  }>();
  const error =
    enabled && failure?.api === api && failure.key === requestKey
      ? failure.message
      : "";
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController();
    let pushed = false;
    const load = () => {
      if (controller.signal.aborted) return;
      void api
        .query<T>(query, stableVars, controller.signal)
        .then((value) => {
          if (controller.signal.aborted || pushed) {
            return;
          }

          setResult((previous) =>
            previous?.api === api &&
            previous.key === requestKey &&
            sameJSONValue(previous.value, value)
              ? previous
              : { api, key: requestKey, value },
          );
          setFailure(undefined);
        })
        .catch((err) => {
          if (
            !controller.signal.aborted &&
            !pushed &&
            err.name !== "AbortError"
          )
            setFailure({ api, key: requestKey, message: String(err.message) });
        })
        .finally(() => {
          if (!controller.signal.aborted) {
            setLoading(false);
          }
        });
    };
    // One initial HTTP read. Subsequent state is pushed by the server for this
    // subscription; neither timers nor invalidations trigger repeat reads.
    const unsubscribe = api.watch<T>(
      query,
      stableVars,
      (value) => {
        if (controller.signal.aborted) return;
        // Do not let an older initial HTTP response overwrite a pushed snapshot.
        pushed = true;
        setResult((previous) =>
          previous?.api === api &&
          previous.key === requestKey &&
          sameJSONValue(previous.value, value)
            ? previous
            : { api, key: requestKey, value },
        );
        setFailure(undefined);
        setLoading(false);
      },
      (err) => {
        if (!controller.signal.aborted)
          setFailure({ api, key: requestKey, message: err.message });
      },
    );
    load();

    return () => {
      unsubscribe();
      controller.abort();
    };
  }, [api, query, stableVars, enabled]);

  return {
    data,
    error,
    loading:
      enabled &&
      !data &&
      (loading || !error || error === connectionErrorMessage),
  };
}

export const CONFIG = `query {
  sandboxStatuses { machineId mode level checkedAt error }
  machines(limit:500) {
    id name host port user keyId jumps jumpHostOnly properties sandbox singleUser containers microVMCommand workspaceRoot gpuDevices storage { id path shared description } quotaCommand
  }
  pools(limit:500) {
    id name keyId maxMachines sharedHome workspaceRoot singleUser sandbox storage { id path shared description } quotaCommand machines { id name host user gpuDevices }
  }
  providers(limit:500) {
    id name kind baseUrl authMode billingMode responsesTransport promptCacheMode oauthClientId oauthProjectId connected accountLabel catalogStatus catalogError modelCount
  }
  sshKeys { id name publicKey fingerprint purpose purposeId canDelete canRevoke usages { kind id name projectId } }
  trustedKeys { publicKey fingerprint }
  projects(limit:500) { id name directory managedWorkspace machineIds poolIds commitName commitEmail gitConnectionId repositoryId repositoryName repositorySshUrl repositorySshKeyId }
  commitIdentity { name email }
  modelDefaults { main { providerId modelId reasoningLevel } approver { providerId modelId reasoningLevel } }
  providerKinds { id name authMethods }
}`;
