import { expect, test } from "@playwright/test";
import { bootstrapData, isTranscriptQuery } from "./fixtures/bootstrap-data";
import { mkdir, writeFile } from "node:fs/promises";

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
    "MCP actions have readable parameters and results: " +
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

      function add(
        action: string,
        args: unknown,
        result: unknown,
        error?: string,
      ) {
        const id = "mcp-" + messages.length;
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
            Date.UTC(2026, 10, 7, 13, 0, messages.length),
          ).toISOString(),
        };
        messages.push(
          {
            ...base,
            wire: JSON.stringify([
              {
                type: "function_call",
                call_id: id,
                name: "harness",
                arguments: JSON.stringify({
                  action,
                  arguments: JSON.stringify(args),
                  network: true,
                }),
              },
            ]),
          },
          {
            ...base,
            id: id + "-result",
            role: "tool",
            callId: id,
            toolName: "harness",
            content: JSON.stringify(error ? { error } : { result }),
          },
        );
      }
      const serverId = "private-server-id";
      add(
        "mcp_connect",
        { name: "Ghidra", transport: "http", url: "http://127.0.0.1:8090/mcp" },
        {
          id: serverId,
          name: "Ghidra",
          sandbox: "microvm",
          machineId: "private-machine-id",
        },
      );
      add(
        "mcp_tools",
        { serverId, query: "decompile" },
        {
          tools: [
            {
              name: "decompile",
              description: "Decompile a function at the given address.",
            },
          ],
          total: 1,
        },
      );
      add(
        "mcp_call",
        { serverId, tool: "decompile", arguments: { address: "0x401000" } },
        {
          server: "Ghidra",
          tool: "decompile",
          content: [
            {
              type: "text",
              text: "Function main\n\n~~~c\nint main(void) {\n    return 42;\n}\n~~~",
            },
          ],
        },
      );
      add(
        "mcp_call",
        { serverId, tool: "rename_function", arguments: { name: "main" } },
        null,
        "MCP tool reported an error: binary not loaded",
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
      await log
        .getByRole("button", {
          name: new RegExp(label("MCP-Server verbinden", "Connect MCP server")),
        })
        .click();

      await expect(
        log.getByText(label("MCP-Server verbunden.", "MCP server connected."), {
          exact: true,
        }),
      ).toBeVisible();

      await log
        .getByRole("button", {
          name: new RegExp(
            label("MCP-Werkzeuge entdecken", "Discover MCP tools"),
          ),
        })
        .click();

      await expect(
        log.getByText("Decompile a function at the given address.", {
          exact: true,
        }),
      ).toBeVisible();

      const call = log.getByRole("button", {
        name: new RegExp(
          label("MCP-Werkzeug ausführen", "Run MCP tool") + ".*decompile",
        ),
      });
      await call.scrollIntoViewIfNeeded();
      await call.focus();
      await page.keyboard.press("Enter");

      await expect(log.getByText("0x401000", { exact: true })).toBeVisible();
      await expect(
        log.getByText("Function main", { exact: true }),
      ).toBeVisible();
      await expect(
        log.locator(".syntax-highlight .hljs-keyword").first(),
      ).toBeVisible();

      const failure = log.getByRole("button", {
        name: new RegExp(
          label("MCP-Werkzeug ausführen", "Run MCP tool") + ".*rename_function",
        ),
      });
      await failure.click();

      await expect(
        log
          .getByText("binary not loaded", {
            exact: true,
          })
          .first(),
      ).toBeVisible();
      await expect(
        log.getByText("private-server-id", { exact: false }),
      ).toHaveCount(0);
      await expect(
        log.getByText("private-machine-id", { exact: false }),
      ).toHaveCount(0);
      await expect(log.getByText('"jsonrpc"', { exact: false })).toHaveCount(0);

      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth - innerWidth,
      );

      expect(overflow).toBeLessThanOrEqual(1);

      await call.scrollIntoViewIfNeeded();
      await mkdir("artifacts/ui", { recursive: true });
      const variant = english ? "mobile-en-dark" : "desktop-de-light";
      const file = "mcp-harness-" + variant + ".png";
      await page.screenshot({
        path: "artifacts/ui/" + file,
        animations: "disabled",
        fullPage: false,
      });
      await writeFile(
        "artifacts/ui/mcp-harness-" + variant + ".json",
        JSON.stringify(
          {
            captures: [
              { name: "MCP discovery and reviewed execution", file, overflow },
            ],
            errors: [],
          },
          null,
          2,
        ),
      );
    },
  );
}
