import { expect, test } from "@playwright/test";
import { bootstrapData, isTranscriptQuery } from "./fixtures/bootstrap-data";
import { mkdir } from "node:fs/promises";

import type { Message } from "../src/generated";
import {
  agent,
  agents,
  config,
  project,
  transcript,
} from "./fixtures/visual-data";

for (const variant of [
  {
    name: "desktop-de-light",
    width: 1440,
    height: 1000,
    locale: "de",
    theme: "light",
  },
  {
    name: "mobile-en-dark",
    width: 390,
    height: 844,
    locale: "en",
    theme: "dark",
  },
]) {
  test(`readable activity details and approvals: ${variant.name}`, async ({
    page,
    context,
  }) => {
    const label = (de: string, en: string) =>
      variant.locale === "de" ? de : en;
    await page.setViewportSize(variant);
    await page.addInitScript(({ locale, theme }) => {
      localStorage.setItem("zentrale-locale", locale);
      localStorage.setItem("zentrale-log-diagnostics", "true");
      localStorage.setItem("zentrale-theme", theme);
    }, variant);
    let sequence = 0;
    const messages: Message[] = [];
    const add = (
      role: string,
      content: string,
      extra: Partial<Message> = {},
    ) => {
      const index = sequence++;
      messages.push({
        id: `detail-${index}`,
        agentId: agent.id,
        role,
        content,
        wire: "",
        callId: "",
        toolName: "",
        change: null,
        changes: [],
        activities: [],
        attachments: [],
        createdAt: new Date(
          Date.parse("2026-11-05T11:00:00Z") + index * 1000,
        ).toISOString(),
        ...extra,
      });
    };
    const action = (
      name: string,
      args: unknown,
      result: unknown,
      format = "responses",
    ) => {
      const id = `call-${sequence}`;
      const request = {
        action: name,
        arguments: JSON.stringify(args),
        network: name === "packages_install",
        justification: name === "exec" ? "Prüft das Beispielprogramm." : "",
      };
      const wire =
        format === "chat"
          ? {
              tool_calls: [
                {
                  id,
                  function: {
                    name: "harness",
                    arguments: JSON.stringify(request),
                  },
                },
              ],
            }
          : format === "anthropic"
            ? [
                {
                  type: "thinking",
                  thinking: "Die Projektdatei wird gelesen.",
                  signature: "opaque-signature",
                },
                { type: "tool_use", id, name: "harness", input: request },
              ]
            : format === "gemini"
              ? {
                  parts: [
                    {
                      text: "Verzeichnisinhalt prüfen.",
                      thought: true,
                      thoughtSignature: "opaque-signature",
                    },
                    { functionCall: { name: "harness", args: request } },
                  ],
                }
              : [
                  {
                    type: "reasoning",
                    summary: [
                      {
                        type: "summary_text",
                        text: "Zuerst die vorhandenen Tests prüfen.",
                      },
                    ],
                    encrypted_content: "opaque-reasoning",
                  },
                  {
                    type: "function_call",
                    call_id: id,
                    name: "harness",
                    arguments: JSON.stringify(request),
                  },
                ];
      add("assistant", "", { wire: JSON.stringify(wire) });
      add("tool", JSON.stringify(result), {
        callId: format === "gemini" ? "gemini-1" : id,
        toolName: "harness",
      });
    };
    action(
      "exec",
      { command: "python3 -m unittest -v" },
      {
        result: {
          sandbox: "microvm",
          job: {
            id: "job-1",
            status: "completed",
            output: "Ran 3 tests. OK\n",
            exitCode: 0,
          },
        },
      },
    );
    action(
      "read_file",
      { path: "src/demo.py" },
      {
        result: {
          path: "src/demo.py",
          content: "print('Hello')\n",
          sandbox: "microvm",
        },
      },
      "anthropic",
    );
    action(
      "list_files",
      { path: "src" },
      {
        result: {
          entries: Array.from({ length: 35 }, (_, index) => ({
            name: index === 0 ? "examples" : `sample-${index}.py`,
            directory: index === 0,
            symlink: false,
          })),
        },
      },
      "gemini",
    );
    action(
      "list_files",
      { path: "empty" },
      { result: { entries: [] } },
      "chat",
    );
    action(
      "packages_install",
      { packages: ["python3", "git"] },
      { result: { status: "running", jobId: "package-job" } },
      "chat",
    );
    action(
      "microvm_info",
      {},
      {
        result: {
          os: "fedora",
          version: "45",
          running: true,
          persistent: true,
          bootId: "fedora-boot-42",
        },
      },
    );
    action(
      "extension_inspect",
      { target: "training" },
      {
        result: {
          title: "Training",
          counters: { completed: 12, pending: 3 },
          options: ["small", "large"],
        },
      },
    );
    action(
      "exec",
      { command: "rm protected.txt" },
      { error: "auto-approval denied: Die Datei wird weiterhin benötigt." },
    );
    add(
      "event",
      JSON.stringify({
        action: "exec",
        arguments: { command: "python3 demo.py" },
        network: false,
        justification: "Beispiel ausführen.",
        project: { name: project.name, directory: project.directory },
      }),
    );
    await page.routeWebSocket("**/events?*", () => {});
    await page.route("**/graphql", async (route) => {
      const { query } = route.request().postDataJSON();
      const data = query.includes("status{")
        ? {
            status: {
              needsSetup: false,
              authenticated: true,
              githubConnected: false,
            },
          }
        : isTranscriptQuery(query)
          ? {
              ...transcript,
              agent,
              messages,
              recentMessages: [],
              questions: [],
              jobs: [],
              approvals: [
                {
                  id: "approval-human",
                  tool: "exec",
                  arguments: JSON.stringify({
                    command: "python3 -m unittest -v",
                    network: false,
                  }),
                  allowed: true,
                  reason: "Tests im Projekt ausführen.",
                  blanket: false,
                  createdAt: "2026-11-05T11:00:00Z",
                },
              ],
            }
          : query.includes("agents(projectId:")
            ? { project, agents }
            : config;

      return route.fulfill({ json: { data: bootstrapData(query, data) } });
    });
    await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
    const output = page.getByRole("region", {
      name: label("Agentenverlauf", "Agent transcript"),
      exact: true,
    });
    const command = output.getByRole("button", {
      name: /python3 -m unittest/,
    });
    await command.press("Enter");
    const commandCode = output
      .getByLabel(label("Codeblock: Befehl", "Code block: Command"), {
        exact: true,
      })
      .filter({ visible: true });

    await expect(commandCode).toHaveText("python3 -m unittest -v");
    await expect(
      output.getByText("Ran 3 tests. OK", { exact: false }),
    ).toBeVisible();
    await expect(
      output.getByText(label("Exit-Code", "Exit code"), { exact: false }),
    ).toBeVisible();

    for (const key of ['"result"', '"arguments"', '"command"'])
      expect(
        (await output.locator("pre:visible").allTextContents()).join("\n"),
      ).not.toContain(key);

    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    await commandCode
      .locator("..")
      .getByRole("button", { name: label("Kopieren", "Copy"), exact: true })
      .click();

    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
      "python3 -m unittest -v",
    );

    await output
      .getByRole("button", {
        name: label("Schrittdetails", "Step details"),
        exact: true,
      })
      .click();
    let dialog = page.getByRole("dialog");

    await expect(
      dialog.getByRole("heading", { name: label("Überlegungen", "Reasoning") }),
    ).toBeVisible();
    await expect(
      dialog.getByText("Zuerst die vorhandenen Tests prüfen."),
    ).toBeVisible();
    await expect(
      dialog.getByRole("heading", {
        name: label("Befehl ausführen", "Run command"),
      }),
    ).toBeVisible();
    await expect(dialog).not.toContainText("opaque-reasoning");
    await expect(dialog).not.toContainText("function_call");

    await mkdir("artifacts/ui", { recursive: true });
    await page.screenshot({
      path: `artifacts/ui/activity-${variant.name}-step.png`,
      animations: "disabled",
    });
    await page.keyboard.press("Escape");

    await expect(
      output.getByRole("button", {
        name: label("Schrittdetails", "Step details"),
        exact: true,
      }),
    ).toBeFocused();

    await command.press("Enter");

    const file = output.getByRole("button", {
      name: new RegExp(label("Datei lesen", "Read file") + ".*src/demo.py"),
    });
    await file.press("Enter");

    await expect(
      output
        .getByLabel("Codeblock: src/demo.py")
        .or(output.getByLabel("Code block: src/demo.py")),
    ).toHaveText("print('Hello')\n");

    await output
      .getByRole("button", {
        name: label("Schrittdetails", "Step details"),
        exact: true,
      })
      .click();
    dialog = page.getByRole("dialog");

    await expect(
      dialog.getByText("Die Projektdatei wird gelesen."),
    ).toBeVisible();
    await expect(dialog).not.toContainText("opaque-signature");

    await page.keyboard.press("Escape");
    await file.press("Enter");

    const files = output.getByRole("button", {
      name: new RegExp(label("Dateien auflisten", "List files") + ".*src"),
    });
    await files.press("Enter");
    const listing = output.getByRole("list", {
      name: label("Dateien", "Files"),
      exact: true,
    });

    await expect(listing.getByRole("listitem")).toHaveCount(30);

    // A large lazy result must have its final height before scrolling to its
    // end; animating a clipped panel can otherwise leave that end off screen.
    expect(
      await listing.evaluate(
        (element) =>
          getComputedStyle(element.closest("[data-slot=accordion-panel]")!)
            .transitionProperty,
      ),
    ).toBe("none");

    await listing.getByRole("listitem").last().scrollIntoViewIfNeeded();
    await expect(
      output.getByRole("button", {
        name: label("Mehr anzeigen", "Show more"),
        exact: true,
      }),
    ).toHaveCount(0);

    try {
      await expect(listing.getByRole("listitem")).toHaveCount(35);
    } catch (error) {
      const position = await listing.evaluate((element) => {
        const panel = element.closest("[data-slot=accordion-panel]");
        const box = (node: Element | null) =>
          node?.getBoundingClientRect().toJSON();
        return {
          scrollY,
          viewport: innerHeight,
          documentHeight: document.documentElement.scrollHeight,
          list: box(element),
          last: box(element.lastElementChild),
          sentinel: box(element.nextElementSibling),
          panel: box(panel),
          expanded: panel?.getAttribute("data-expanded"),
        };
      });
      throw new Error(
        `File pagination position: ${JSON.stringify(position)}\n${String(error)}`,
      );
    }
    await expect(
      listing.getByText("sample-34.py", { exact: true }),
    ).toBeVisible();

    await output
      .getByRole("button", {
        name: label("Schrittdetails", "Step details"),
        exact: true,
      })
      .click();

    await expect(
      page.getByRole("dialog").getByText("Verzeichnisinhalt prüfen."),
    ).toBeVisible();

    await page.keyboard.press("Escape");
    await page.screenshot({
      path: `artifacts/ui/activity-${variant.name}-files.png`,
      animations: "disabled",
    });
    await files.press("Enter");
    const empty = output.getByRole("button", {
      name: new RegExp(label("Dateien auflisten", "List files") + ".*empty"),
    });
    await empty.press("Enter");

    await expect(
      output.getByText(
        label("Dieses Verzeichnis ist leer.", "This directory is empty."),
      ),
    ).toBeVisible();

    await empty.press("Enter");

    const packages = output.getByRole("button", {
      name: new RegExp(label("Pakete installieren", "Install packages")),
    });
    await packages.press("Enter");

    await expect(output.getByText("python3", { exact: true })).toBeVisible();
    await expect(output.getByText("git", { exact: true })).toBeVisible();
    await expect(
      output
        .getByLabel(
          label(
            "Aktionsdetails: Pakete installieren",
            "Action details: Install packages",
          ),
          { exact: true },
        )
        .getByText(label("Netzwerkzugriff", "Network access"), { exact: true }),
    ).toBeVisible();

    await packages.press("Enter");
    await output
      .getByRole("button", {
        name: new RegExp(label("MicroVM prüfen", "Inspect MicroVM")),
      })
      .press("Enter");

    await expect(output.getByText("Fedora", { exact: true })).toBeVisible();
    await expect(
      output.getByText(label("Dauerhafte Umgebung", "Persistent environment"), {
        exact: true,
      }),
    ).toBeVisible();

    await output
      .getByRole("button", { name: /extension inspect/ })
      .press("Enter");

    await expect(output.getByText("Training", { exact: true })).toBeVisible();
    await expect(output).not.toContainText('"counters"');
    await expect(
      output.getByText("Die Datei wird weiterhin benötigt.", { exact: true }),
    ).toBeHidden();
    await output
      .getByRole("button", { name: /rm protected\.txt/ })
      .press("Enter");
    await expect(
      output.getByText("Die Datei wird weiterhin benötigt.", { exact: true }),
    ).toBeVisible();

    await output
      .getByRole("button", {
        name: label("Befehl ausführen", "Run command"),
        exact: true,
      })
      .press("Enter");

    await expect(
      output.getByText("Beispiel ausführen.", { exact: true }),
    ).toBeVisible();
    await expect(output).not.toContainText('"justification"');

    await page.screenshot({
      path: `artifacts/ui/activity-${variant.name}-environment.png`,
      animations: "disabled",
    });

    await page
      .getByRole("button", {
        name: new RegExp("^" + label("Agentendetails", "Agent details")),
      })
      .click();
    dialog = page.getByRole("dialog");

    await expect(
      dialog.getByRole("heading", { name: /Freigaben|Approvals/ }),
    ).toHaveCount(0);
    await expect(
      dialog.getByRole("heading", {
        name: label("Ausführungsumgebung", "Execution environment"),
      }),
    ).toBeVisible();
    await expect(dialog).not.toContainText('"command"');

    await page.screenshot({
      path: `artifacts/ui/activity-${variant.name}-approval.png`,
      animations: "disabled",
    });
    await page.keyboard.press("Escape");

    await expect(
      page.getByRole("button", {
        name: new RegExp("^" + label("Agentendetails", "Agent details")),
      }),
    ).toBeFocused();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth - innerWidth,
      ),
    ).toBeLessThanOrEqual(1);
  });
}
