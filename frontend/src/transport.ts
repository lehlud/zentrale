import { ConnectionError } from "./connection";
import {
  AuthenticationError,
  requiresAuthentication,
  type GraphQLResult,
} from "./session";

export async function fetchGraphQL<T>(
  query: string,
  variables: Record<string, unknown> = {},
  signal?: AbortSignal,
  keepalive = false,
): Promise<T> {
  let response: Response;

  try {
    const body = JSON.stringify({ query, variables });
    response = await fetch("/graphql", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body,
      signal,
      keepalive: keepalive && new TextEncoder().encode(body).length < 60000,
    });
  } catch (error) {
    if (signal?.aborted || (error as Error).name === "AbortError") {
      throw error;
    }
    throw new ConnectionError(error);
  }

  // A proxy or middleware may return a non-JSON 401 response.
  if (response.status === 401) throw new AuthenticationError();

  let data: GraphQLResult<T>;

  try {
    data = await response.json();
  } catch (error) {
    if (signal?.aborted || (error as Error).name === "AbortError") {
      throw error;
    }

    // An interrupted response body is also a transport failure. Invalid JSON
    // and HTTP/application errors remain separate, actionable server errors.
    if (error instanceof TypeError) {
      throw new ConnectionError(error);
    }

    throw new Error(`HTTP ${response.status}: invalid server response`, {
      cause: error,
    });
  }

  if (data.errors?.length) {
    if (requiresAuthentication(data.errors)) throw new AuthenticationError();
    throw new Error(data.errors.map((error) => error.message).join("\n"));
  }

  if (!response.ok) {
    throw new Error(`HTTP ${response.status}`);
  }

  return data.data as T;
}
