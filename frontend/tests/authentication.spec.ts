import { expect, test, type Page, type WebSocketRoute } from "@playwright/test";

import { API } from "../src/api";
import { AuthenticationError, SESSION_QUERY } from "../src/session";
import { installPushFixture } from "./fixtures/push-fixture";
import { config } from "./fixtures/visual-data";

const originalFetch = globalThis.fetch;
test.afterEach(() => {
  globalThis.fetch = originalFetch;
});

for (const failure of [
  "HTTP 401",
  "GraphQL UNAUTHENTICATED",
  "upload 401",
  "terminal probe 401",
]) {
  test(`${failure} invalidates the session centrally`, async () => {
    const api = new API();
    const sessions: unknown[] = [];
    api.watch(
      SESSION_QUERY,
      {},
      (session) => sessions.push(session),
      () => {},
    );
    globalThis.fetch = async () =>
      failure === "GraphQL UNAUTHENTICATED"
        ? Response.json({
            errors: [
              {
                message: "Credentials expired",
                extensions: { code: "UNAUTHENTICATED" },
              },
            ],
          })
        : new Response("Not JSON", { status: 401 });

    const request =
      failure === "upload 401"
        ? api.uploadAttachment(
            "agent",
            "file",
            new File(["test"], "test.txt"),
            new AbortController().signal,
          )
        : failure === "terminal probe 401"
          ? api.fetch("/terminal/project/agent")
          : api.query("query{projects{id}}");
    await expect(request).rejects.toBeInstanceOf(AuthenticationError);
    expect(api.authenticationRequired).toBe(true);
    expect(sessions).toEqual([
      {
        status: {
          authenticated: false,
          needsSetup: false,
          githubConnected: false,
          oidc: false,
        },
      },
    ]);
    expect(api.connection.snapshot()).toBe(false);
  });
}

test("application and permission errors do not invalidate credentials", async () => {
  const api = new API();
  for (const status of [200, 403, 500]) {
    globalThis.fetch = async () =>
      Response.json(
        { errors: [{ message: "Operation rejected" }] },
        { status },
      );
    await expect(api.query("query{projects{id}}")).rejects.toThrow(
      "Operation rejected",
    );
    expect(api.authenticationRequired).toBe(false);
    expect(api.connection.snapshot()).toBe(false);
  }
});

test("a late unauthorized response cannot invalidate a newer login", async () => {
  const api = new API();
  let completeOldRequest!: (response: Response) => void;
  globalThis.fetch = async (_url, options) => {
    const { query } = JSON.parse(options?.body as string);
    if (query.includes("projects{")) {
      return new Promise<Response>((resolve) => {
        completeOldRequest = resolve;
      });
    }
    return query === SESSION_QUERY
      ? Response.json({
          data: {
            status: {
              authenticated: true,
              needsSetup: false,
              githubConnected: false,
            },
          },
        })
      : Response.json({ data: { login: true } });
  };
  const oldRequest = api.query("query{projects{id}}");
  await api.mutate(
    'mutation{login(password:"correct password")}',
    {},
    { announce: false },
  );
  completeOldRequest(new Response("Expired old session", { status: 401 }));
  await expect(oldRequest).rejects.toBeInstanceOf(AuthenticationError);
  expect(api.authenticationRequired).toBe(false);
});

async function sessionFixture(page: Page) {
  const state = { authenticated: true, connections: 0, mutations: 0 };
  let socket: WebSocketRoute;
  const resolve = (query: string) =>
    query.includes("status{")
      ? {
          status: {
            authenticated: state.authenticated,
            needsSetup: false,
            githubConnected: false,
          },
        }
      : config;
  const push = await installPushFixture(page, resolve, (connection) => {
    socket = connection;
    state.connections++;
  });
  await page.route("**/graphql", async (route) => {
    const { query, variables } = route.request().postDataJSON();
    if (query.includes("login(")) {
      state.mutations++;
      if (variables.password !== "correct password") {
        await route.fulfill({
          json: { errors: [{ message: "invalid password" }] },
        });
        return;
      }
      state.authenticated = true;
      await route.fulfill({ json: { data: { login: true } } });
      return;
    }
    await route.fulfill({ json: { data: resolve(query) } });
  });
  return { state, push, socket: () => socket! };
}

test("revoked sessions show login automatically and can sign in again without reloading", async ({
  page,
}) => {
  const fixture = await sessionFixture(page);
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto("/#/projects");
  await expect(
    page.getByRole("heading", { name: "Projekte", exact: true }),
  ).toBeVisible();
  const connections = fixture.state.connections;
  fixture.state.authenticated = false;
  fixture.socket().close({ code: 4401, reason: "authentication required" });
  const login = page.getByRole("button", { name: "Anmelden", exact: true });
  await expect(login).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Projekte", exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByText("Verbindung unterbrochen", { exact: true }),
  ).toHaveCount(0);
  await page.waitForTimeout(2200);
  expect(fixture.state.connections).toBe(connections);
  await page.getByLabel("Passwort", { exact: true }).fill("wrong password");
  await login.click();
  await expect(page.getByRole("alert")).toContainText("Falsches Passwort");
  await expect(login).toBeVisible();
  await page.getByLabel("Passwort", { exact: true }).fill("correct password");
  await login.click();
  await expect(
    page.getByRole("heading", { name: "Projekte", exact: true }),
  ).toBeVisible();
  await expect
    .poll(() => fixture.state.connections)
    .toBeGreaterThan(connections);
  expect(fixture.state.mutations).toBe(2);
  expect(page.url()).toContain("#/projects");
  expect(errors).toEqual([]);
});

test("authentication errors in pushed snapshots show login", async ({
  page,
}) => {
  const fixture = await sessionFixture(page);
  await page.goto("/#/projects");
  await expect(
    page.getByRole("heading", { name: "Projekte", exact: true }),
  ).toBeVisible();
  await expect.poll(() => fixture.push.frames.length).toBeGreaterThan(0);
  fixture.socket().send(
    JSON.stringify({
      type: "query_result",
      requestId: fixture.push.frames[0].requestId,
      result: {
        errors: [
          {
            message: "authentication required",
            extensions: { code: "UNAUTHENTICATED" },
          },
        ],
      },
    }),
  );
  await expect(
    page.getByRole("button", { name: "Anmelden", exact: true }),
  ).toBeVisible();
});

test("a rejected WebSocket handshake rechecks credentials instead of reconnecting forever", async ({
  page,
}) => {
  // No WebSocket mock and no session cookie: the real backend rejects the
  // handshake. Simulate credentials expiring immediately after the first read.
  let statusReads = 0;
  await page.route("**/graphql", async (route) => {
    const { query } = route.request().postDataJSON();
    const data = query.includes("status{")
      ? {
          status: {
            authenticated: ++statusReads === 1,
            needsSetup: false,
            githubConnected: false,
          },
        }
      : config;
    await route.fulfill({ json: { data } });
  });
  await page.goto("/#/projects");
  await expect(
    page.getByRole("button", { name: "Anmelden", exact: true }),
  ).toBeVisible();
  expect(statusReads).toBe(2);
  await expect(
    page.getByText("Verbindung unterbrochen", { exact: true }),
  ).toHaveCount(0);
});
