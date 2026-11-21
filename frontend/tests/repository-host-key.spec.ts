import { expect, test } from "@playwright/test";
import { bootstrapData, isTranscriptQuery } from "./fixtures/bootstrap-data";
import { installPushFixture } from "./fixtures/push-fixture";
import { agent, config, project, transcript } from "./fixtures/visual-data";

test("a rotated Git host key requires explicit confirmation before retrying preparation", async ({
  page,
}) => {
  await page.addInitScript(() => localStorage.setItem("zentrale-locale", "de"));
  const challenge = {
    host: "gitea.example.test:2222",
    fingerprint: "SHA256:new-fingerprint",
    publicKey: "ecdsa-sha2-nistp256 new-public-key",
    repositorySourceId: "git-root",
    previousFingerprint: "SHA256:previous-fingerprint",
  };
  let attempts = 0;
  let confirmed = false;
  await page.routeWebSocket("**/events?*", () => {});
  await page.route("**/graphql", async (route) => {
    const { query, variables } = route.request().postDataJSON();
    const send = (data: unknown) => route.fulfill({ json: { data } });
    if (query.includes("status{")) {
      return send({ status: { needsSetup: false, authenticated: true } });
    }
    if (query.includes("startEditor(")) {
      expect(query).toContain("repositorySourceId previousFingerprint");
      attempts++;
      return send({
        startEditor: confirmed
          ? { session: { url: "/ide/git/ready/" }, hostKey: {}, error: "" }
          : {
              session: {},
              hostKey: challenge,
              error: "host key needs confirmation",
            },
      });
    }
    if (query.includes("trustRepositoryHostKey(")) {
      expect(variables).toEqual({
        sourceId: challenge.repositorySourceId,
        host: challenge.host,
        publicKey: challenge.publicKey,
        previousFingerprint: challenge.previousFingerprint,
      });
      confirmed = true;
      return send({ trustRepositoryHostKey: true });
    }
    return send({ project: { id: "git", name: "Git project" } });
  });
  await page.route("**/ide/git/ready/**", (route) =>
    route.fulfill({ contentType: "text/html", body: "<h1>Editor ready</h1>" }),
  );
  await page.goto("/#/editor/git");
  await expect(
    page.getByText("Git-Host-Schlüssel prüfen", { exact: true }),
  ).toBeVisible();
  for (const value of [
    challenge.host,
    challenge.fingerprint,
    challenge.previousFingerprint,
  ]) {
    await expect(page.getByText(value, { exact: true })).toBeVisible();
  }
  expect(attempts).toBe(1);
  expect(confirmed).toBe(false);
  await page
    .getByRole("button", { name: "Schlüssel vertrauen und erneut verbinden" })
    .click();
  await expect(
    page.getByRole("heading", { name: "Editor ready" }),
  ).toBeVisible();
  expect(attempts).toBe(2);
});

test("Git trust can be updated without closing the running VM terminal", async ({
  page,
}) => {
  const repository = {
    ...project,
    repositorySshUrl: "git@gitea.example.test:project/repo.git",
  };
  const challenge = {
    host: "gitea.example.test:22",
    fingerprint: "SHA256:offered-fingerprint",
    publicKey: "ssh-ed25519 offered-public-key",
    repositorySourceId: "git-root",
    previousFingerprint: "SHA256:previous-fingerprint",
  };
  const resolve = (query: string) =>
    query.includes("status{")
      ? { status: { authenticated: true, needsSetup: false } }
      : isTranscriptQuery(query)
        ? bootstrapData(query, transcript, { project: repository })
        : config;
  await page.addInitScript(() => localStorage.setItem("zentrale-locale", "de"));
  await installPushFixture(page, resolve);
  let confirmed = false;
  await page.route("**/graphql", async (route) => {
    const { query, variables } = route.request().postDataJSON();
    if (query.includes("prepareAgent(")) {
      expect(variables.agentId).toBe(agent.id);
      return route.fulfill({
        json: {
          data: {
            prepareAgent: {
              connected: confirmed,
              error: confirmed ? "" : "host key needs confirmation",
              hostKey: confirmed ? {} : challenge,
            },
          },
        },
      });
    }
    if (query.includes("trustRepositoryHostKey(")) {
      expect(variables).toEqual({
        sourceId: challenge.repositorySourceId,
        host: challenge.host,
        publicKey: challenge.publicKey,
        previousFingerprint: challenge.previousFingerprint,
      });
      confirmed = true;
      return route.fulfill({
        json: { data: { trustRepositoryHostKey: true } },
      });
    }
    return route.fulfill({ json: { data: resolve(query) } });
  });
  let opened = 0;
  let closed = 0;
  await page.routeWebSocket("**/terminal/**", (socket) => {
    opened++;
    socket.onClose(() => closed++);
    socket.send(JSON.stringify({ type: "status", status: "ready" }));
  });

  await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
  await page.getByRole("button", { name: "Terminal in der VM öffnen" }).click();
  const terminal = page.getByRole("dialog", { name: "VM-Terminal" });
  await expect(terminal.getByText("Verbunden", { exact: true })).toBeVisible();
  await terminal.getByRole("button", { name: "Git-Verbindung prüfen" }).click();
  const check = page.getByRole("dialog", { name: "Git-Verbindung prüfen" });
  await expect(
    check.getByText(challenge.fingerprint, { exact: true }),
  ).toBeVisible();
  expect(confirmed).toBe(false);
  await check
    .getByRole("button", { name: "Schlüssel vertrauen und erneut verbinden" })
    .click();
  await expect(
    check.getByText("Repository-Verbindung vorbereitet."),
  ).toBeVisible();
  await check.getByRole("button", { name: "Fertig" }).click();
  await expect(terminal.getByText("Verbunden", { exact: true })).toBeVisible();
  expect(opened).toBe(1);
  expect(closed).toBe(0);
});
