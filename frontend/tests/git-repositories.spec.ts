import { expect, test } from "@playwright/test";
import { mkdir, writeFile } from "node:fs/promises";
import { config, gitConnections } from "./fixtures/visual-data";

for (const english of [false, true]) {
  test(`repository login, organization creation, retry and key usages: ${english ? "mobile-en-dark" : "desktop-de-light"}`, async ({
    page,
    context,
  }) => {
    page.setDefaultTimeout(10000);
    const label = (de: string, en: string) => (english ? en : de);
    await page.setViewportSize({ width: english ? 390 : 1440, height: 950 });
    await page.addInitScript(
      ({ english }) => {
        localStorage.setItem("zentrale-locale", english ? "en" : "de");
        localStorage.setItem("zentrale-theme", english ? "dark" : "light");
      },
      { english },
    );
    const connections = structuredClone(gitConnections);
    const data = structuredClone(config);
    const repos = Array.from({ length: 51 }, (_, index) => ({
      id: `team/demo-${index}`,
      identity: String(index + 1),
      name: `demo-${index}`,
      fullName: `team/demo-${index}`,
      sshUrl: `git@github.com:team/demo-${index}.git`,
      webUrl: `https://github.com/team/demo-${index}`,
      private: true,
      canManageDeployKeys: true,
    }));
    let createAttempts = 0;
    let saveAttempts = 0;
    const submittedIds: string[] = [];
    const login = {
      id: "device-login",
      connectionId: "github",
      verificationUrl: "https://github.com/login/device",
      userCode: "ABCD-EFGH",
      status: "pending",
      expiresAt: "2099-01-01T00:00:00Z",
      interval: 5,
      error: "",
    };
    await page.route("**/graphql", async (route) => {
      const { query, variables = {} } = route.request().postDataJSON();
      const send = (value: unknown) => route.fulfill({ json: { data: value } });
      if (query.includes("configureGitConnection")) {
        connections[0] = {
          ...connections[0],
          clientId: variables.input.clientId,
          configured: true,
        };
        return send({ configureGitConnection: connections[0] });
      }
      if (query.includes("beginGitLogin"))
        return send({ beginGitLogin: login });
      if (query.includes("pollGitLogin")) {
        connections[0] = {
          ...connections[0],
          connected: true,
          userName: "gunter",
        };
        return send({ pollGitLogin: { ...login, status: "connected" } });
      }
      if (query.includes("createGitRepository")) {
        expect(variables.input.namespaceId).toBe("team");
        expect(variables.input.private).toBe(true);
        createAttempts++;
        if (createAttempts === 1)
          return route.fulfill({
            json: {
              errors: [
                {
                  message:
                    "Organization policy denied repository creation. Retry after approval.",
                },
              ],
            },
          });
        const repository = {
          ...repos[0],
          id: "team/new-project",
          identity: "900",
          name: variables.input.name,
          fullName: "team/new-project",
        };
        repos.unshift(repository);
        return send({ createGitRepository: repository });
      }
      if (query.includes("saveProject")) {
        submittedIds.push(variables.input.id);
        expect(variables.input.repositoryId).toBe("team/new-project");
        saveAttempts++;
        if (saveAttempts === 1)
          return route.fulfill({
            json: {
              errors: [
                {
                  message:
                    "Deploy key registration interrupted. Retry to reuse the pending key.",
                },
              ],
            },
          });
        data.projects.push(variables.input);
        return send({ saveProject: { id: variables.input.id } });
      }
      if (query.includes("status"))
        return send({
          status: {
            needsSetup: false,
            authenticated: true,
            githubConnected: true,
          },
        });
      if (query.includes("machines(limit:500)")) return send(data);
      if (query.includes("gitConnections"))
        return send({ gitConnections: connections });
      if (query.includes("gitNamespaces"))
        return send({
          gitNamespaces: [
            { id: "gunter", name: "gunter", personal: true },
            { id: "team", name: "team", personal: false },
          ],
        });
      if (query.includes("gitRepositories")) {
        const items = repos.filter((repository) =>
          repository.fullName.includes(variables.search ?? ""),
        );
        const offset = variables.offset ?? 0;
        return send({
          gitRepositories: {
            items: items.slice(offset, offset + 50),
            hasMore: offset + 50 < items.length,
            nextOffset: Math.min(offset + 50, items.length),
          },
        });
      }
      if (query.includes("sidebarAgents")) return send({ sidebarAgents: [] });
      return send({});
    });
    await mkdir("artifacts/ui", { recursive: true });
    const captures: { name: string; file: string; overflow: number }[] = [];
    const capture = async (name: string) => {
      const file = `git-${english ? "mobile-en-dark" : "desktop-de-light"}-${name}.png`;
      await page.screenshot({
        path: `artifacts/ui/${file}`,
        animations: "disabled",
      });
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth - innerWidth,
      );
      expect(overflow).toBeLessThanOrEqual(1);
      captures.push({ name, file, overflow });
    };
    await page.goto("/#/projects");
    await page
      .getByRole("button", {
        name: label("Projekt anlegen", "Create project"),
        exact: true,
      })
      .click();
    const dialogs = page.locator(
      '[data-slot="modal-dialog"]:not([data-exiting])',
    );
    const project = dialogs.first();
    await project.getByLabel("Name", { exact: true }).fill("New project");
    await expect(
      project.getByLabel(
        label(
          "Projektverzeichnis auf der Fernmaschine",
          "Project directory on the remote machine",
        ),
      ),
    ).toHaveCount(0);
    await project
      .getByRole("button", { name: label("Weiter", "Continue"), exact: true })
      .click();
    await project
      .getByRole("button", { name: label("Fernmaschinen", "Remote machines") })
      .click();
    await page
      .getByRole("option", { name: "GPU-Worker Frankfurt", exact: true })
      .click();
    await page.keyboard.press("Escape");
    await project
      .getByRole("button", { name: label("Weiter", "Continue"), exact: true })
      .click();
    await project
      .getByRole("button", {
        name: label("GitHub verbinden", "Connect GitHub"),
        exact: true,
      })
      .click();
    await dialogs
      .last()
      .getByLabel(label("OAuth-App Client-ID", "OAuth app client ID"))
      .fill("own-oauth-app");
    await dialogs
      .last()
      .getByRole("button", {
        name: label("Weiter zur Anmeldung", "Continue to sign in"),
      })
      .click();
    const signIn = dialogs.last();
    await expect(signIn.getByText("ABCD-EFGH", { exact: true })).toBeVisible();
    await expect(
      page.getByText(label("Erfolgreich", "Success"), { exact: true }),
    ).toHaveCount(0);
    await capture("device-login");
    await expect(
      signIn.getByRole("link", {
        name: label("Anmeldeseite öffnen ↗", "Open sign-in page ↗"),
      }),
    ).toHaveAttribute("target", "_blank");
    await expect(
      signIn.getByRole("button", {
        name: label("Fertig", "Done"),
        exact: true,
      }),
    ).toBeVisible({ timeout: 12000 });
    await signIn
      .getByRole("button", { name: label("Fertig", "Done"), exact: true })
      .click();
    await expect(
      project.getByRole("heading", {
        name: label(
          "Welches Repository gehört dazu?",
          "Which repository belongs to this project?",
        ),
        exact: true,
      }),
    ).toBeVisible();
    const list = project.getByRole("list", {
      name: label("Repositories", "Repositories"),
      exact: true,
    });
    await expect(list.getByRole("button")).toHaveCount(50);
    await project
      .getByRole("button", {
        name: label("Mehr anzeigen", "Show more"),
        exact: true,
      })
      .click();
    await expect(list.getByRole("button")).toHaveCount(51);
    await project
      .getByLabel(label("Repositories suchen", "Search repositories"))
      .fill("demo-50");
    await expect(list.getByRole("button")).toHaveCount(1);
    await list.getByRole("button").click();
    await project
      .getByRole("button", {
        name: label("Repository ändern", "Change repository"),
        exact: true,
      })
      .click();
    await project
      .getByRole("button", {
        name: label("Repository erstellen", "Create repository"),
        exact: true,
      })
      .click();
    const create = dialogs.last();
    await create
      .getByLabel(label("Repository-Name", "Repository name"))
      .fill("new-project");
    await create
      .getByRole("button", {
        name: label("Konto oder Organisation", "Account or organization"),
      })
      .click();
    await page.getByRole("option", { name: "team", exact: true }).click();
    await create
      .getByRole("button", {
        name: label("Repository erstellen", "Create repository"),
        exact: true,
      })
      .click();
    await capture("create-error");
    await expect(create.getByRole("alert")).toContainText(
      "Organization policy",
    );
    await expect(
      create.getByLabel(label("Repository-Name", "Repository name")),
    ).toHaveValue("new-project");
    await create
      .getByRole("button", {
        name: label("Repository erstellen", "Create repository"),
        exact: true,
      })
      .click();
    await expect(dialogs).toHaveCount(1);
    await expect(project.getByRole("status")).toContainText("team/new-project");
    await capture("project-selection");
    await project
      .getByRole("button", { name: label("Weiter", "Continue"), exact: true })
      .click();
    await project
      .getByRole("button", {
        name: label("Projekt anlegen", "Create project"),
        exact: true,
      })
      .click();
    await expect(project.getByRole("alert")).toContainText(
      "Deploy key registration interrupted",
    );
    await project
      .getByRole("button", {
        name: label("Projekt anlegen", "Create project"),
        exact: true,
      })
      .click();
    await expect(dialogs).toHaveCount(0);
    expect(submittedIds).toHaveLength(2);
    expect(submittedIds[0]).toBe(submittedIds[1]);
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    const repositoryName = "research-team/" + "long-repository-name-".repeat(5);
    data.sshKeys.push({
      id: 3,
      publicKey: "ssh-ed25519 managed-repo-key",
      fingerprint: "SHA256:RepoKey",
      purpose: "repository",
      purposeId: "repo-binding",
      canDelete: false,
      canRevoke: false,
      usages: [
        {
          kind: "repository",
          id: "repo-binding",
          name: repositoryName,
          projectId: "",
        },
        {
          kind: "repository",
          id: "repo-binding",
          name: repositoryName,
          projectId: "forecast",
        },
        {
          kind: "repository",
          id: "repo-binding",
          name: repositoryName,
          projectId: submittedIds[0],
        },
      ],
    });
    await page.goto("/#/keys");
    await expect(
      page.getByRole("heading", { name: repositoryName, exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText(
        label(
          "Projekte: Modelltraining & Evaluation, New project",
          "Projects: Modelltraining & Evaluation, New project",
        ),
        { exact: true },
      ),
    ).toBeVisible();
    const key = page.locator("section").filter({
      has: page.getByRole("heading", {
        name: label("Schlüssel 1", "Key 1"),
        exact: true,
      }),
    });
    await key
      .getByRole("button", {
        name: label("Öffentlichen Schlüssel kopieren", "Copy public key"),
        exact: true,
      })
      .click();
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
      data.sshKeys[0].publicKey,
    );
    await key
      .getByRole("button", {
        name: label("Aktionen für Schlüssel 1", "Actions for Key 1"),
        exact: true,
      })
      .click();
    await expect(
      page.getByRole("menuitem", {
        name: label("Entfernen", "Remove"),
        exact: true,
      }),
    ).toBeDisabled();
    await expect(
      page.getByRole("menuitem", {
        name: label("Öffentlichen Schlüssel anzeigen", "View public key"),
        exact: true,
      }),
    ).toHaveCount(0);
    await page.keyboard.press("Escape");
    await mkdir("artifacts/ui", { recursive: true });
    await page.screenshot({
      path: `artifacts/ui/git-${english ? "mobile-en-dark" : "desktop-de-light"}-keys.png`,
      fullPage: true,
    });
    await capture("key-usages");
    await writeFile(
      `artifacts/ui/git-${english ? "mobile-en-dark" : "desktop-de-light"}.json`,
      JSON.stringify({ captures, errors: [] }, null, 2),
    );
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth - innerWidth,
      ),
    ).toBeLessThanOrEqual(1);
  });
}
