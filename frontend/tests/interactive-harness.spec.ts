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

for (const english of [false, true]) {
  test(
    "interactive process input and incremental output: " +
      (english ? "mobile-en-dark" : "desktop-de-light"),
    async ({ page }) => {
      const label = (de: string, en: string) => (english ? en : de);
      await page.setViewportSize({ width: english ? 390 : 1440, height: 950 });
      await page.addInitScript(
        ({ english }) => {
          localStorage.setItem("zentrale-locale", english ? "en" : "de");
          localStorage.setItem("zentrale-theme", english ? "dark" : "light");
          localStorage.setItem("zentrale-log-diagnostics", "false");
        },
        { english },
      );
      const messages: Message[] = [];

      function action(name: string, args: unknown, result: unknown) {
        const id = "interactive-" + messages.length;
        const base: Message = {
          id,
          agentId: agent.id,
          role: "assistant",
          content: "",
          wire: "",
          callId: "",
          toolName: "",
          change: null,
          changes: [],
          activities: [],
          attachments: [],
          createdAt: new Date(
            Date.UTC(2026, 10, 7, 14, 0, messages.length),
          ).toISOString(),
        };
        messages.push(
          {
            ...base,
            wire: JSON.stringify([
              {
                type: "function_call",
                call_id: id,
                name,
                arguments: JSON.stringify(args),
              },
            ]),
          },
          {
            ...base,
            id: id + "-result",
            role: "tool",
            callId: id,
            toolName: name,
            content: JSON.stringify({ result }),
          },
        );
      }
      action(
        "exec_command",
        { cmd: "python3 -i", tty: true },
        {
          jobId: "private-job-id",
          status: "running",
          output: ">>>",
          interactive: true,
          tty: true,
          nextCursor: "private-cursor",
        },
      );
      action(
        "write_stdin",
        { id: "private-job-id", chars: "print(2 + 5)\n" },
        {
          id: "private-job-id",
          status: "running",
          output: "7\n>>>",
          nextCursor: "private-cursor-2",
          hasMore: false,
        },
      );
      action(
        "write_stdin",
        { id: "private-job-id", chars: "\u0003" },
        {
          id: "private-job-id",
          status: "running",
          output: "KeyboardInterrupt\n>>>",
          nextCursor: "private-cursor-3",
          hasMore: false,
        },
      );
      action(
        "write_stdin",
        { id: "private-job-id", eof: true },
        {
          id: "private-job-id",
          status: "completed",
          output: "Session ended.",
          nextCursor: "private-cursor-4",
          hasMore: false,
        },
      );
      action(
        "write_stdin",
        { id: "private-job-id", chars: "" },
        {
          id: "private-job-id",
          status: "completed",
          output: "Final process output",
        },
      );
      await page.routeWebSocket("**/events?*", () => {});
      await page.route("**/graphql", (route) => {
        const { query } = route.request().postDataJSON();
        const data = query.includes("status{")
          ? { status: { needsSetup: false, authenticated: true } }
          : isTranscriptQuery(query)
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

        return route.fulfill({ json: { data: bootstrapData(query, data) } });
      });
      await page.goto("/#/projects/" + project.id + "?agent=" + agent.id);
      const log = page.getByRole("region", {
        name: label("Agentenverlauf", "Agent transcript"),
        exact: true,
      });
      const input = log.getByRole("button", {
        name: new RegExp(
          label("Prozess fortsetzen", "Continue process") + ".*print",
        ),
      });
      await input.scrollIntoViewIfNeeded();
      await input.focus();
      await page.keyboard.press("Enter");

      await expect(
        log
          .locator(".syntax-highlight")
          .filter({ hasText: "print(2 + 5)" })
          .first(),
      ).toBeVisible();
      await expect(log.getByText("7", { exact: false }).first()).toBeVisible();

      const interrupt = log.getByRole("button", {
        name: new RegExp(
          label("Prozess fortsetzen", "Continue process") + ".*Ctrl\\+C",
        ),
      });
      await interrupt.click();

      await expect(log.getByText("[Ctrl+C]", { exact: true })).toBeVisible();

      const end = log.getByRole("button", {
        name: new RegExp(
          label("Prozess fortsetzen", "Continue process") + ".*EOF",
        ),
      });
      await end.click();

      await expect(
        log.getByText(label("Eingabe beenden (EOF)", "End input (EOF)"), {
          exact: true,
        }),
      ).toBeVisible();
      await expect(
        log.getByText("Session ended.", { exact: true }),
      ).toBeVisible();
      await expect(input).not.toContainText(label("Arbeitet", "Working"));

      const poll = log.getByRole("button", {
        name: new RegExp(
          "^" + label("Prozessausgabe abrufen", "Read process output"),
        ),
      });
      await poll.click();

      await expect(
        log.getByText("Final process output", { exact: true }),
      ).toBeVisible();
      await expect(
        log.getByText("private-cursor", { exact: false }),
      ).toHaveCount(0);
      await expect(
        log.getByText("private-job-id", { exact: false }),
      ).toHaveCount(0);
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth - innerWidth,
        ),
      ).toBeLessThanOrEqual(1);

      await input.scrollIntoViewIfNeeded();
      await mkdir("artifacts/ui", { recursive: true });
      await page.screenshot({
        path:
          "artifacts/ui/interactive-harness-" +
          (english ? "mobile-en-dark" : "desktop-de-light") +
          ".png",
        fullPage: false,
        animations: "disabled",
      });
    },
  );
}
