import { expect, test, type WebSocketRoute } from "@playwright/test";
import { mkdir, writeFile } from "node:fs/promises";

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
  test(`terminal conversation, tool details, keyboard and scroll: ${variant.name}`, async ({
    page,
    context,
  }) => {
    test.setTimeout(90000);
    const english = variant.locale === "en";
    const label = (de: string, en: string) => (english ? en : de);
    await page.setViewportSize(variant);
    await page.addInitScript(({ locale, theme }) => {
      localStorage.setItem("zentrale-locale", locale);
      localStorage.setItem("zentrale-theme", theme);
    }, variant);
    let sequence = 0;
    const message = (
      role: string,
      content: string,
      extra: Partial<Message> = {},
    ): Message => ({
      id: `chat-${sequence}`,
      agentId: agent.id,
      role,
      content,
      wire: "",
      callId: "",
      toolName: "",
      change: null,
      changes: [],
      activities: [],
      createdAt: new Date(
        Date.parse("2026-11-04T11:00:00Z") + sequence++ * 1000,
      ).toISOString(),
      ...extra,
    });
    const code =
      "def quicksort(values):\n    if not values:\n        return []\n    pivot, *rest = values\n    left = [x for x in rest if x <= pivot]\n    right = [x for x in rest if x > pivot]\n    return quicksort(left) + [pivot] + quicksort(right)";
    const reply = label(
      "Das Beispiel ist erstellt und geprüft.",
      "The example is created and tested.",
    );
    const messages: Message[] = [
      message(
        "user",
        label(
          "Erstelle ein kleines Quicksort-Projekt mit Tests.",
          "Create a small Quicksort project with tests.",
        ),
      ),
      message("assistant", "", {
        wire: JSON.stringify([
          {
            type: "function_call",
            call_id: "run",
            name: "harness",
            arguments: JSON.stringify({
              action: "exec",
              arguments: JSON.stringify({ command: "python3 -m unittest -v" }),
            }),
          },
        ]),
      }),
      message(
        "tool",
        JSON.stringify({ result: { output: "Ran 8 tests. OK", exitCode: 0 } }),
        { callId: "run", toolName: "harness" },
      ),
      message("assistant", "", {
        wire: JSON.stringify([
          {
            type: "tool_use",
            id: "read",
            name: "inspect",
            input: {
              action: "read_file",
              arguments: JSON.stringify({ path: "quicksort.py" }),
            },
          },
        ]),
      }),
      message("tool", JSON.stringify({ result: { content: code } }), {
        callId: "read",
        toolName: "inspect",
      }),
      message("assistant", "", {
        wire: JSON.stringify({
          tool_calls: [
            {
              id: "write",
              function: {
                name: "harness",
                arguments: JSON.stringify({
                  action: "write_file",
                  arguments: JSON.stringify({
                    path: "README.md",
                    content: "Quicksort demo",
                  }),
                }),
              },
            },
          ],
        }),
      }),
      message("tool", JSON.stringify({ result: { written: true } }), {
        callId: "write",
        toolName: "harness",
        change: {
          id: "readme-change",
          messageId: "readme-write",
          agentId: agent.id,
          createdAt: "2026-11-04T11:00:06Z",
          path: "README.md",
          kind: "modified",
          added: 2,
          removed: 1,
          truncated: false,
          notice: "",
          patch:
            "--- a/README.md\n+++ b/README.md\n@@ -1,2 +1,3 @@\n # Quicksort\n-TODO\n+Python demo\n+Run python3 -m unittest -v\n",
        },
      }),
      message("assistant", "", {
        wire: JSON.stringify({
          parts: [
            {
              functionCall: {
                name: "harness",
                args: {
                  action: "list_files",
                  arguments: JSON.stringify({ path: "/srv/project" }),
                },
              },
            },
          ],
        }),
      }),
      message(
        "tool",
        JSON.stringify({ result: { entries: ["quicksort.py", "README.md"] } }),
        { callId: "gemini-0", toolName: "harness" },
      ),
      message(
        "assistant",
        `## Quicksort\n\n${reply}\n\n**Python**\n\n\`\`\`python\n${code}\n\`\`\`\n\n| Test | Status |\n| --- | --- |\n| unit | OK |`,
      ),
    ];
    let socket: WebSocketRoute | undefined;
    let prompts: string[] = [];
    let failPrompt = false;
    let eventId = 0;
    await page.routeWebSocket("**/events?*", (connection) => {
      socket = connection;
    });
    await page.route("**/graphql", async (route) => {
      const { query, variables = {} } = route.request().postDataJSON();
      const send = (data: unknown) => route.fulfill({ json: { data } });
      if (query.includes("prepareAgent")) {
        return send({
          prepareAgent: {
            connected: true,
            error: "",
            hostKey: { publicKey: "", host: "", fingerprint: "" },
          },
        });
      }

      if (query.includes("submitInput(agentId:")) {
        prompts.push(variables.text);
        return failPrompt
          ? route.fulfill({
              json: {
                errors: [
                  {
                    message: label(
                      "Auftrag konnte nicht gespeichert werden. Erneut versuchen.",
                      "Could not save the task. Please try again.",
                    ),
                  },
                ],
              },
            })
          : send({ submitInput: { ...variables, status: "active" } });
      }

      if (query.includes("status{")) {
        return send({
          status: {
            needsSetup: false,
            authenticated: true,
            githubConnected: false,
          },
        });
      }

      if (query.includes("recentMessages")) {
        return send({
          ...transcript,
          agent:
            agents.find((candidate) => candidate.id === variables.id) ?? agent,
          messages,
          questions: [],
          jobs: [],
          recentMessages: [],
        });
      }

      if (query.includes("agents(projectId:")) {
        return send({ project, agents });
      }

      return send(config);
    });
    await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
    const output = page.getByRole("region", {
      name: label("Agentenverlauf", "Agent transcript"),
      exact: true,
    });
    const input = page.getByRole("textbox", {
      name: label("Aufgabe oder neue Instruktion", "Task or new instruction"),
    });

    await expect(
      output.getByRole("heading", { name: "Quicksort", exact: true }),
    ).toBeVisible();
    await expect(output.locator('[data-slot="card"]')).toHaveCount(0);
    await expect(page.getByRole("main").getByRole("tablist")).toHaveCount(0);
    await expect(page.getByRole("banner")).toHaveCount(0);
    await expect(
      page.getByRole("button", { name: /← (Projekte|Projects)/ }),
    ).toHaveCount(0);
    await expect(
      page.getByText(label("Agentenansicht", "Agent view"), { exact: true }),
    ).toHaveCount(0);

    const header = page.getByRole("group", {
      name: label("Agentensteuerung", "Agent controls"),
      exact: true,
    });
    const switcher = () =>
      header.getByRole("button", {
        name: new RegExp("^" + label("Agent wechseln:", "Switch agent:")),
      });
    await input.fill("Entwurf beim Agentenwechsel erhalten");
    await switcher().press("Enter");
    const selection = page.getByRole("menu", {
      name: new RegExp("^" + label("Agent wechseln", "Switch agent")),
    });

    await expect(
      selection.getByRole("menuitemradio", { name: /Auto-Approver/ }),
    ).toHaveCount(0);
    await expect(
      selection.getByRole("menuitemradio", { name: new RegExp(agent.name) }),
    ).toHaveAttribute("aria-checked", "true");

    await page.keyboard.press("End");
    await page.keyboard.press("Enter");

    await expect(page).toHaveURL(/agent=research/);
    await expect(switcher()).toContainText("Recherche");
    await expect(input).toHaveValue("");
    await expect(switcher()).toBeFocused();

    await switcher().press("Enter");
    await page.keyboard.press("Home");
    await page.keyboard.press("Enter");

    await expect(page).toHaveURL(/agent=main/);
    await expect(input).toHaveValue("Entwurf beim Agentenwechsel erhalten");

    await input.fill("");

    await expect(
      output
        .getByRole("list", {
          name: label(
            "Nachrichten und Werkzeugaktivität",
            "Messages and tool activity",
          ),
        })
        .locator(":scope > li"),
    ).toHaveCount(6);

    const command = output.getByRole("button", {
      name: /python3 -m unittest -v/,
    });
    await command.scrollIntoViewIfNeeded();
    await command.focus();
    await page.keyboard.press("Enter");

    await expect(
      output.getByText("Ran 8 tests. OK", { exact: false }),
    ).toBeVisible();
    await expect(
      output.getByRole("button", {
        name: new RegExp(label("Datei lesen", "Read file") + ".*quicksort.py"),
      }),
    ).toBeVisible();
    await expect(
      output.getByRole("button", {
        name: new RegExp(
          label("Datei schreiben", "Write file") + ".*README.md",
        ),
      }),
    ).toBeVisible();
    await expect(
      output.getByRole("button", {
        name: new RegExp(
          label("Dateien auflisten", "List files") + ".*/srv/project",
        ),
      }),
    ).toBeVisible();

    await command.press("Enter");

    await expect(
      output.getByText("Ran 8 tests. OK", { exact: false }),
    ).not.toBeVisible();

    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    await output
      .getByRole("button", { name: label("Kopieren", "Copy"), exact: true })
      .click();

    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
      code,
    );
    await expect(
      output.getByRole("button", {
        name: label("Kopiert", "Copied"),
        exact: true,
      }),
    ).toBeVisible();

    const captures: { name: string; file: string }[] = [];

    async function capture(name: string, atEnd = false) {
      await page.evaluate(() => document.fonts.ready);
      if (!atEnd) await page.evaluate(() => window.scrollTo(0, 0));

      await mkdir("artifacts/ui", { recursive: true });
      const file = `conversation-${variant.name}-${name}.png`;
      await page.screenshot({
        path: `artifacts/ui/${file}`,
        fullPage: false,
        animations: "disabled",
      });
      captures.push({ name: `${name}-${variant.name}`, file });
    }
    await page.evaluate(() =>
      window.scrollTo(0, document.documentElement.scrollHeight),
    );
    await capture("answer", true);
    await page.evaluate(() => window.scrollTo(0, 0));
    await capture("commands");

    const diff = output.getByRole("region", {
      name: label("Dateiänderungen: README.md", "File changes: README.md"),
      exact: true,
    });
    await diff.scrollIntoViewIfNeeded();

    await expect(
      diff.getByRole("row").filter({ hasText: "TODO" }),
    ).toContainText("−");
    await expect(
      diff.getByRole("row").filter({ hasText: "Python demo" }),
    ).toContainText("+");
    await expect(
      diff
        .getByRole("row")
        .filter({ hasText: "Run python3" })
        .getByRole("cell")
        .nth(1),
    ).toHaveText("3");

    await capture("diff", true);
    const diffToggle = output.getByRole("button", {
      name: /README.md.*\+2.*−1/,
    });
    await diffToggle.press("Enter");

    await expect(diff).not.toBeVisible();

    await diffToggle.press("Enter");

    await expect(diff).toBeVisible();

    await input.fill("Erste Zeile");
    await input.press("Shift+Enter");
    await input.pressSequentially("Zweite Zeile");

    expect(prompts).toEqual([]);

    await input.press("Enter");

    await expect(input).toHaveValue("");
    await expect.poll(() => prompts).toEqual(["Erste Zeile\nZweite Zeile"]);

    failPrompt = true;
    await input.fill("Diesen Entwurf behalten");
    await input.press("Enter");

    await expect(page.getByRole("main").getByRole("alert")).toContainText(
      label(
        "Auftrag konnte nicht gespeichert werden.",
        "Could not save the task.",
      ),
    );
    await expect(input).toHaveValue("");
    await expect(input).toBeFocused();

    failPrompt = false;
    await page
      .getByRole("button", {
        name: label("Nachricht erneut senden", "Retry sending message"),
      })
      .click();

    await expect(input).toHaveValue("");
    await expect(page.getByRole("main").getByRole("alert")).toHaveCount(0);

    await expect.poll(() => Boolean(socket)).toBe(true);

    await page.evaluate(() =>
      window.scrollTo(0, document.documentElement.scrollHeight),
    );

    await expect(
      page.getByRole("button", {
        name: label("Zu neuesten Einträgen ↓", "Latest entries ↓"),
      }),
    ).toHaveCount(0);

    // Live output follows the bottom, but a reader who scrolls up keeps their place.
    for (let index = 0; index < 25; index++)
      messages.push(
        message(
          "assistant",
          `Step ${index + 1}: ${"checking project files and test results. ".repeat(5)}`,
        ),
      );

    socket!.send(JSON.stringify({ id: ++eventId }));

    await expect(output.getByText(/^Step 25:/)).toBeInViewport();
    await expect.poll(async () => (await header.boundingBox())?.y).toBe(0);
    await expect(switcher()).toBeInViewport();
    expect((await header.boundingBox())!.height).toBeLessThan(160);

    await page.evaluate(() => window.scrollTo(0, 0));
    const latest = page.getByRole("button", {
      name: label("Zu neuesten Einträgen ↓", "Latest entries ↓"),
    });

    await expect(latest).toBeVisible();

    messages.push(message("assistant", "Live update at the end."));
    socket!.send(JSON.stringify({ id: ++eventId }));

    // Offscreen entries are virtualized, not kept attached to the DOM.
    await expect(output.getByText("Live update at the end.")).toHaveCount(0);
    await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);

    await capture("latest-control");
    await latest.click();

    await expect(output.getByText("Live update at the end.")).toBeInViewport();
    await expect(input).toBeInViewport();

    await capture("sticky", true);

    const failureOutput =
      "benchmark_sorting.f90:7:84:\n    7 | character(len=9)\n      | ^\n";
    const additions = Array.from(
      { length: 120 },
      (_, index) =>
        `+print(${index + 1})${index === 0 ? " # " + "long line ".repeat(100) : ""}`,
    );
    messages.push(
      message("tool", JSON.stringify({ result: { written: true } }), {
        toolName: "write_file",
        change: {
          id: "large-change",
          messageId: "large-write",
          agentId: agent.id,
          createdAt: "2026-11-04T11:10:00Z",
          path: "long-example.py",
          kind: "created",
          added: 120,
          removed: 0,
          truncated: false,
          notice: "",
          patch: `--- /dev/null\n+++ b/long-example.py\n@@ -0,0 +1,120 @@\n${additions.join("\n")}\n`,
        },
      }),
      message(
        "tool",
        JSON.stringify({
          result: {
            job: {
              status: "failed",
              error: "Process exited with status 1\n" + failureOutput,
              output: failureOutput,
            },
          },
        }),
        { toolName: "exec" },
      ),
    );
    socket!.send(JSON.stringify({ id: ++eventId }));

    await expect(
      output.getByText("Process exited with status 1", { exact: true }),
    ).toBeHidden();

    const failureToggle = output
      .getByRole("button", { name: english ? /Failed/ : /Fehlgeschlagen/ })
      .last();
    const failedStep = failureToggle.locator("xpath=ancestor::li[1]");
    await failureToggle.click();
    const failureLog = failedStep.getByLabel(
      label("Codeblock: Ausgabe", "Code block: Output"),
      { exact: true },
    );

    await expect(failureLog).toBeVisible();
    expect(await failureLog.textContent()).toBe(failureOutput);
    expect(
      (await failedStep.innerText()).match(/benchmark_sorting\.f90:7:84:/g),
    ).toHaveLength(1);

    await failedStep.scrollIntoViewIfNeeded();
    await capture("process-error", true);
    await failureToggle.click();
    const largeDiff = output.getByRole("region", {
      name: label(
        "Dateiänderungen: long-example.py",
        "File changes: long-example.py",
      ),
      exact: true,
    });

    // Additional diff chunks load automatically as they approach the viewport.
    const more = largeDiff
      .locator("../..")
      .getByRole("button", { name: /^(Mehr anzeigen|Show more)/ });
    await expect(more).toHaveCount(0);
    await expect(largeDiff.getByRole("row")).toHaveCount(122);
    await expect(
      largeDiff.getByText("print(120)", { exact: true }),
    ).toBeVisible();

    await largeDiff.scrollIntoViewIfNeeded();
    await largeDiff.focus();

    expect(
      await largeDiff.evaluate(
        (element) => element.scrollWidth > element.clientWidth,
      ),
    ).toBe(true);

    await page.keyboard.press("ArrowRight");

    await expect
      .poll(() => largeDiff.evaluate((element) => element.scrollLeft))
      .toBeGreaterThan(0);

    if (variant.width > 1000) {
      // Half-size CSS viewport exercises the layout at the equivalent of 200% zoom.
      await page.setViewportSize({ width: 720, height: 500 });
      await expect(input).toBeInViewport();
    }

    const nestedScrolling = await page.getByRole("main").evaluate((root) =>
      Array.from(root.querySelectorAll("*"))
        .filter((element) => {
          const overflow = getComputedStyle(element).overflowY;
          return (
            element.tagName !== "TEXTAREA" &&
            /auto|scroll/.test(overflow) &&
            element.scrollHeight > element.clientHeight + 1
          );
        })
        .map((element) => element.tagName + "." + element.className),
    );

    expect(nestedScrolling).toEqual([]);

    await writeFile(
      `artifacts/ui/conversation-${variant.name}.json`,
      JSON.stringify({
        captures,
      }),
    );

    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth - innerWidth,
      ),
    ).toBeLessThanOrEqual(1);
  });
}
