import { expect, test } from "@playwright/test";
import { mkdir } from "node:fs/promises";
import {
  agent,
  agents,
  config,
  project,
  transcript,
} from "./fixtures/visual-data";
import { setLogDiagnostics } from "./fixtures/context-actions";

for (const english of [false, true]) {
  test(`focused write/package logs, highlighting and optional diagnostics: ${english ? "mobile-en-dark" : "desktop-de-light"}`, async ({
    page,
    context,
  }) => {
    page.setDefaultTimeout(10000);
    await page.setViewportSize({ width: english ? 390 : 1440, height: 950 });
    await page.addInitScript(
      ({ english }) => {
        localStorage.setItem("zentrale-locale", english ? "en" : "de");
        localStorage.setItem("zentrale-theme", english ? "dark" : "light");
      },
      { english },
    );
    const label = (de: string, en: string) => (english ? en : de);
    const code =
      'def bubble_sort(values):\n    """Sort values\n    without changing the input.\n    """\n    return sorted(values)\n\nprint("<img src=x onerror=window.codeExecuted=true>")\n';
    const message = (
      id: string,
      role: string,
      content: string,
      extra = {},
    ) => ({
      id,
      agentId: agent.id,
      role,
      content,
      wire: "",
      callId: "",
      toolName: "",
      change: null,
      createdAt: `2026-11-06T12:00:0${id.length}Z`,
      ...extra,
    });
    const call = (id: string, action: string, args: unknown, network = false) =>
      message(id, "assistant", "", {
        wire: JSON.stringify([
          {
            type: "function_call",
            call_id: id,
            name: "harness",
            arguments: JSON.stringify({
              action,
              arguments: JSON.stringify(args),
              network,
            }),
          },
        ]),
      });
    const messages = [
      call("write", "write_file", {
        path: "test_bubblesort.py",
        content: code,
      }),
      message(
        "written",
        "tool",
        JSON.stringify({
          result: {
            written: true,
            path: "/home/user/project/test_bubblesort.py",
            machineId: "MACHINE_DIAGNOSTIC_ONLY",
            sandbox: "microvm",
          },
        }),
        {
          callId: "write",
          toolName: "harness",
          change: {
            id: "diff",
            path: "test_bubblesort.py",
            kind: "created",
            added: 8,
            removed: 0,
            truncated: false,
            notice: "",
            patch:
              "--- /dev/null\n+++ b/test_bubblesort.py\n@@ -0,0 +1,8 @@\n" +
              code
                .trimEnd()
                .split("\n")
                .map((line) => "+" + line)
                .join("\n") +
              "\n",
          },
        },
      ),
      call(
        "package",
        "packages_install",
        { packages: ["gcc-gfortran"], sandbox: "microvm" },
        true,
      ),
      message(
        "installed",
        "tool",
        JSON.stringify({
          result: {
            job: {
              status: "completed",
              id: "JOB_DIAGNOSTIC_ONLY",
              machineId: "MACHINE_DIAGNOSTIC_ONLY",
              startedAt: "2026-11-06T11:00:00Z",
              output: "Installed",
              exitCode: 0,
            },
          },
        }),
        { callId: "package", toolName: "harness" },
      ),
      message("answer", "assistant", "Done.", {
        wire: JSON.stringify({ content: "Done." }),
      }),
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
        name: new RegExp(
          label("Datei schreiben", "Write file") + ".*test_bubblesort.py",
        ),
      })
      .click();
    const source = output.getByLabel(
      label("Codeblock: test_bubblesort.py", "Code block: test_bubblesort.py"),
      { exact: true },
    );
    await expect(source).toHaveText(code);
    const keyword = source.getByText("def", { exact: true });
    await expect(keyword).toBeVisible();
    expect(await keyword.evaluate((el) => getComputedStyle(el).color)).not.toBe(
      await source.evaluate((el) => getComputedStyle(el).color),
    );
    const diff = output.getByRole("region", {
      name: label(
        "Dateiänderungen: test_bubblesort.py",
        "File changes: test_bubblesort.py",
      ),
    });
    await expect(diff.getByText("def", { exact: true })).toBeVisible();
    await expect(page.locator("img")).toHaveCount(0);
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    await source
      .locator("..")
      .getByRole("button", { name: label("Kopieren", "Copy"), exact: true })
      .click();
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
      code,
    );
    const packages = output.getByRole("button", {
      name: new RegExp(
        label("Pakete installieren", "Install packages") + ".*gcc-gfortran",
      ),
    });
    await packages.click();
    await expect(
      output.getByText("gcc-gfortran", { exact: true }).last(),
    ).toBeVisible();
    await expect(
      output.getByText(label("Netzwerkzugriff", "Network access"), {
        exact: true,
      }),
    ).toBeVisible();
    for (const text of [
      "MACHINE_DIAGNOSTIC_ONLY",
      "JOB_DIAGNOSTIC_ONLY",
      label("Datei gespeichert.", "File saved."),
      label("Installiert", "Installed"),
    ])
      await expect(output.getByText(text, { exact: true })).toHaveCount(0);
    await expect(
      output.getByRole("button", {
        name: label("Schrittdetails", "Step details"),
        exact: true,
      }),
    ).toHaveCount(0);
    await expect(
      page.getByText(
        label(
          "Enter senden · Shift+Enter Zeilenumbruch",
          "Enter to send · Shift+Enter for a new line",
        ),
        { exact: true },
      ),
    ).toHaveCount(0);
    await setLogDiagnostics(page, true);
    await expect(
      output.getByText("MACHINE_DIAGNOSTIC_ONLY", { exact: true }).first(),
    ).toBeVisible();
    await expect(
      output
        .getByRole("button", {
          name: label("Schrittdetails", "Step details"),
          exact: true,
        })
        .first(),
    ).toBeVisible();
    await setLogDiagnostics(page, false);
    await expect(
      output.getByText("MACHINE_DIAGNOSTIC_ONLY", { exact: true }),
    ).toHaveCount(0);
    await mkdir("artifacts/ui", { recursive: true });
    await page.screenshot({
      path: `artifacts/ui/focused-logs-${english ? "mobile" : "desktop"}.png`,
      animations: "disabled",
    });
  });
}
