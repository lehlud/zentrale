import { expect, test } from "@playwright/test";

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
