import { expect, test } from "@playwright/test";
import { mkdir, writeFile } from "node:fs/promises";

import {
  agent,
  agents,
  config,
  project,
  transcript,
} from "./fixtures/visual-data";
import type { FileChange, Message } from "../src/generated";

for (const english of [false, true]) {
  test(`native tools, persistent plan and multi-file patch: ${english ? "mobile-en-dark" : "desktop-de-light"}`, async ({
    page,
  }) => {
    const label = (de: string, en: string) => (english ? en : de);
    await page.setViewportSize({ width: english ? 390 : 1440, height: 950 });
    await page.addInitScript(
      ({ english }) => {
        localStorage.setItem("zentrale-locale", english ? "en" : "de");
        localStorage.setItem("zentrale-theme", english ? "dark" : "light");
      },
      { english },
    );
    let sequence = 0;
    const message = (
      role: string,
      content: string,
      extra: Partial<Message> = {},
    ): Message => ({
      id: `native-${sequence}`,
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
        Date.UTC(2026, 10, 6, 13, 0, sequence++),
      ).toISOString(),
      ...extra,
    });
    const call = (id: string, name: string, args: unknown) =>
      message("assistant", "", {
        wire: JSON.stringify([
          {
            type: "function_call",
            call_id: id,
            name,
            arguments: JSON.stringify(args),
          },
        ]),
      });
    const change = (id: string, path: string, source: string): FileChange => ({
      id,
      messageId: "patch-result",
      agentId: agent.id,
      createdAt: "2026-11-06T13:00:03Z",
      path,
      kind: "created",
      added: 2,
      removed: 0,
      truncated: false,
      notice: "",
      patch: `--- /dev/null\n+++ b/${path}\n@@ -0,0 +1,2 @@\n${source
        .split("\n")
        .map((line) => "+" + line)
        .join("\n")}\n`,
    });
    const plan = [
      { step: "Inspect the relevant range", status: "completed" },
      { step: "Apply and verify the change", status: "in_progress" },
    ];
    const changes = [
      change("a", "answer.py", "def answer():\n    return 42"),
      change(
        "b",
        "test_answer.py",
        "from answer import answer\nassert answer() == 42",
      ),
    ];
    const messages = [
      message("user", "Implement the requested change and run the tests."),
      call("read", "read_file", { path: "existing.py", offset: 201, limit: 1 }),
      message(
        "tool",
        JSON.stringify({
          result: {
            path: "existing.py",
            content: "return existing_value\n",
            offset: 201,
            totalLines: 800,
            hasMore: true,
          },
        }),
        { callId: "read", toolName: "read_file" },
      ),
      call("plan", "update_plan", {
        explanation: "The relevant context is known.",
        plan,
      }),
      message("tool", JSON.stringify({ result: { plan } }), {
        callId: "plan",
        toolName: "update_plan",
      }),
      call("patch", "apply_patch", {
        input:
          "*** Begin Patch\n*** Add File: answer.py\n+def answer():\n+    return 42\n*** End Patch",
      }),
      message(
        "tool",
        JSON.stringify({
          result: {
            files: changes.map((item) => ({
              path: item.path,
              kind: item.kind,
            })),
          },
        }),
        {
          callId: "patch",
          toolName: "apply_patch",
          change: changes[0],
          changes,
        },
      ),
      call("verify", "exec_command", {
        cmd: "python3 -m pytest",
        yield_time_ms: 1000,
      }),
      message(
        "tool",
        JSON.stringify({
          result: {
            job: { status: "completed", output: "2 tests passed", exitCode: 0 },
          },
        }),
        { callId: "verify", toolName: "exec_command" },
      ),
      call("combined", "code_mode", {
        code: 'const read = await tools.read_file({path:"answer.py"});\nprint(read.content.includes("42"));',
      }),
      message(
        "tool",
        JSON.stringify({
          result: {
            output: "true\n",
            calls: 2,
            failures: [],
            truncated: false,
          },
        }),
        {
          callId: "combined",
          toolName: "code_mode",
          activities: [
            {
              name: "read_file",
              arguments: JSON.stringify({ path: "answer.py" }),
              content: JSON.stringify({
                result: {
                  path: "answer.py",
                  content: "def answer():\n    return 42\n",
                },
              }),
              network: false,
              justification: "",
            },
            {
              name: "exec",
              arguments: JSON.stringify({ command: "python3 -m pytest" }),
              content: JSON.stringify({
                result: {
                  job: {
                    status: "completed",
                    output: "2 tests passed",
                    exitCode: 0,
                  },
                },
              }),
              network: false,
              justification: "",
            },
          ],
        },
      ),
      message("assistant", "The change has been verified."),
    ];
    await page.routeWebSocket("**/events?*", () => {});
    await page.route("**/graphql", (route) => {
      const { query } = route.request().postDataJSON();
      const data = query.includes("status{")
        ? { status: { needsSetup: false, authenticated: true } }
        : query.includes("recentMessages")
          ? {
              ...transcript,
              messages,
              recentMessages: [],
              questions: [],
              jobs: [],
            }
          : query.includes("agents(projectId:")
            ? { project, agents }
            : config;

      return route.fulfill({ json: { data } });
    });
    await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
    const output = page.getByRole("region", {
      name: label("Agentenverlauf", "Agent transcript"),
      exact: true,
    });
    await output
      .getByRole("button", {
        name: new RegExp(label("Datei lesen", "Read file") + ".*existing.py"),
      })
      .click();

    await expect(
      output.getByText(
        label(
          "Ab Zeile 201 · 800 Zeilen insgesamt",
          "From line 201 · 800 lines in total",
        ),
        { exact: true },
      ),
    ).toBeVisible();

    await output
      .getByRole("button", {
        name: new RegExp(label("Plan aktualisieren", "Update plan")),
      })
      .click();
    const steps = output.getByRole("list", {
      name: label("Arbeitsplan", "Work plan"),
      exact: true,
    });

    await expect(steps.getByRole("listitem")).toHaveCount(2);
    await expect(steps).toContainText(label("In Arbeit", "In progress"));

    await output
      .getByRole("button", {
        name: new RegExp(label("Dateien ändern", "Edit files")),
      })
      .click();

    await expect(output.getByRole("table")).toHaveCount(2);
    await expect(
      output.getByRole("button", { name: /^answer\.py / }),
    ).toBeVisible();
    await expect(
      output.getByText("test_answer.py", { exact: true }),
    ).toBeVisible();
    await expect(
      output.locator(".syntax-highlight .hljs-keyword").first(),
    ).toBeVisible();
    await expect(
      output.getByText("*** Begin Patch", { exact: false }),
    ).toHaveCount(0);

    await output
      .getByRole("button", {
        name: new RegExp(
          label("Befehl ausführen", "Run command") + ".*python3 -m pytest",
        ),
      })
      .click();

    await expect(
      output.getByText("2 tests passed", { exact: true }).first(),
    ).toBeVisible();

    await output
      .getByRole("button", {
        name: new RegExp(label("Werkzeuge kombinieren", "Combine tools")),
      })
      .click();
    const activity = output.getByRole("list", {
      name: label("Ausgeführte Werkzeuge", "Executed tools"),
      exact: true,
    });

    await expect(activity.getByRole("listitem")).toHaveCount(2);

    const innerRead = activity.getByRole("button", {
      name: new RegExp(label("Datei lesen", "Read file") + ".*answer.py"),
    });
    await innerRead.scrollIntoViewIfNeeded();
    await innerRead.focus();
    await page.keyboard.press("Enter");

    await expect(innerRead).toBeInViewport();
    await expect(
      activity.getByText("return 42", { exact: false }),
    ).toBeVisible();
    await expect(output.getByText("true", { exact: true })).toBeVisible();

    await activity.scrollIntoViewIfNeeded();

    await expect(
      output.getByRole("button", {
        name: label("Schrittdetails", "Step details"),
        exact: true,
      }),
    ).toHaveCount(0);

    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - innerWidth,
    );

    expect(overflow).toBeLessThanOrEqual(1);

    await mkdir("artifacts/ui", { recursive: true });
    const variant = english ? "mobile-en-dark" : "desktop-de-light";
    const file = `native-harness-${variant}.png`;
    await page.screenshot({
      path: `artifacts/ui/${file}`,
      fullPage: false,
      animations: "disabled",
    });
    await writeFile(
      `artifacts/ui/native-harness-${variant}.json`,
      JSON.stringify(
        {
          captures: [
            { name: "native-tools-and-multi-file-patch", file, overflow },
          ],
          errors: [],
        },
        null,
        2,
      ),
    );
  });
}
