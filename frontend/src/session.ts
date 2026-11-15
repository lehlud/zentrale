import type { Status } from "./generated";

export const SESSION_QUERY =
  "query{status{needsSetup authenticated githubConnected}}";

// Application-specific WebSocket close code; HTTP handshakes still return 401.
export const SESSION_EXPIRED = 4401;

export type SessionData = { status: Status };

export class AuthenticationError extends Error {
  constructor() {
    super("authentication required");
    this.name = "AuthenticationError";
  }
}

export type GraphQLError = {
  message: string;
  extensions?: { code?: string };
};

export type GraphQLResult<T = unknown> = {
  data?: T;
  errors?: GraphQLError[];
};

export function requiresAuthentication(errors: GraphQLError[] = []) {
  return errors.some((error) => error.extensions?.code === "UNAUTHENTICATED");
}
