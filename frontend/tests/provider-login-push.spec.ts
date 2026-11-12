import { expect, test } from "@playwright/test";
import { chooseProviderAccount } from "./fixtures/context-actions";
import { installPushFixture } from "./fixtures/push-fixture";
import { config, gitConnections } from "./fixtures/visual-data";

test("provider device login subscribes once and receives completion without browser polling", async ({
  page,
}) => {
  const login = {
    id: "provider-device",
    providerId: "chatgpt",
    method: "device",
    status: "pending",
    verificationUrl: "https://auth.openai.com/codex/device",
    userCode: "PUSH-ONLY",
    interval: 5,
    expiresAt: "2099-01-01T00:00:00Z",
    error: "",
  };
  const reads = (query: string, variables: Record<string, any> = {}) => {
    if (/\bstatus\s*\{/.test(query))
      return { status: { needsSetup: false, authenticated: true } };
    if (query.includes("providerLogin("))
      return { providerLogin: variables.id === login.id ? login : null };
    if (query.includes("gitConnections")) return { gitConnections };
    if (query.includes("sidebarAgents")) return { sidebarAgents: [] };
    return config;
  };
  const push = await installPushFixture(page, reads);
  let polls = 0;
  let cancels = 0;
  await page.route("**/graphql", async (route) => {
    const { query, variables = {} } = route.request().postDataJSON();
    const send = (data: unknown) => route.fulfill({ json: { data } });
    if (query.includes("beginProviderLogin"))
      return send({ beginProviderLogin: login });
    if (query.includes("cancelProviderLogin")) {
      cancels++;
      return send({ cancelProviderLogin: true });
    }
    if (query.includes("pollProviderLogin")) {
      polls++;
      return send({ pollProviderLogin: login });
    }
    return send(reads(query, variables));
  });
  await page.goto("/#/settings");
  const account = await chooseProviderAccount(page, "ChatGPT · Team");
  await account
    .getByRole("button", { name: /^(Anmelden|Sign in)$/, exact: true })
    .click();
  const dialog = page.locator('[data-slot="modal-dialog"]:not([data-exiting])');
  await expect(dialog.getByText("PUSH-ONLY", { exact: true })).toBeVisible();
  await page.clock.install();
  await page.clock.runFor(15000);
  expect(polls).toBe(0);
  expect(
    push.frames.filter(
      (frame) =>
        frame.type === "subscribe" && frame.query.includes("providerLogin("),
    ),
  ).toHaveLength(1);
  login.status = "connected";
  await push.push();
  const done = dialog.getByRole("button", {
    name: /^(Modelle ansehen|View models)$/,
    exact: true,
  });
  await expect(done).toBeVisible();
  const previousCancels = cancels;
  await done.click();
  await expect(dialog).toHaveCount(0);
  await expect.poll(() => cancels).toBeGreaterThan(previousCancels);
  expect(polls).toBe(0);
});
