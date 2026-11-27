import { expect, test } from "@playwright/test";
import { pathToFileURL } from "node:url";
import type { Message } from "../src/generated";
import type { TranscriptArchive } from "../src/transcript-export";
import { bootstrapData, isTranscriptQuery } from "./fixtures/bootstrap-data";
import { installPushFixture } from "./fixtures/push-fixture";
import {
  adminStatus,
  agent,
  config,
  project,
  transcript,
} from "./fixtures/visual-data";

function message(
  id: string,
  agentId: string,
  role: string,
  content: string,
): Message {
  return {
    ...transcript.messages[0],
    id,
    lineageId: id,
    agentId,
    role,
    content,
    wire: "",
    callId: "",
    toolName: "",
    attachments: [],
    change: null,
    changes: [],
    activities: [],
  };
}

for (const short of [false, true]) {
  test(`exports a self-contained ${short ? "short chat" : "project thread with descendants"} from the header menu`, async ({
    page,
    browser,
  }, testInfo) => {
    await page.addInitScript(() => {
      localStorage.setItem("zentrale-locale", "de");
      localStorage.setItem("zentrale-theme", "dark");
    });

    const current = {
      ...agent,
      id: short ? "short-export" : agent.id,
      projectId: short ? "" : project.id,
      status: "idle",
      name: "Exporttest",
    };
    const literal = "Ein wörtliches </script> und <b>stehen im Chat</b>.";
    const original = message("original", current.id, "user", literal);
    original.attachments = [
      {
        id: "notes",
        agentId: current.id,
        projectId: current.projectId,
        name: "notes.txt",
        mimeType: "text/plain",
        size: 5,
        path: "",
        machineId: "",
      },
    ];
    const answer = message(
      "answer",
      current.id,
      "assistant",
      "**Offline gerendert**\n\n```js\nconsole.log('Test');\n```",
    );
    const call = message("web-call", current.id, "assistant", "");
    call.wire = JSON.stringify([
      {
        type: "function_call",
        call_id: "web-read",
        name: "web_read",
        arguments: '{"url":"https://example.org/"}',
      },
    ]);
    const tool = message(
      "tool",
      current.id,
      "tool",
      '{"result":"Text aus der Webseite"}',
    );
    tool.toolName = "web_read";
    tool.callId = "web-read";
    const archive: TranscriptArchive = {
      version: 1,
      exportedAt: "2026-01-01T00:00:00Z",
      projectId: current.projectId,
      projectName: short ? "" : project.name,
      projectDirectory: "",
      attachments: { [current.id]: { notes: "bm90ZXM=" } },
      threads: [
        {
          id: current.id,
          parentId: "",
          name: current.name,
          role: "main",
          messages: [original, call, tool, answer],
        },
      ],
    };

    if (!short) {
      archive.threads.push(
        {
          id: "child",
          parentId: current.id,
          name: "Unteragent",
          role: "subagent",
          messages: [
            message("child-message", "child", "assistant", "Erste Ebene"),
          ],
        },
        {
          id: "grandchild",
          parentId: "child",
          name: "Enkelagent",
          role: "subagent",
          messages: [
            message(
              "grandchild-message",
              "grandchild",
              "assistant",
              "Zweite Ebene",
            ),
          ],
        },
      );
    }

    const resolve = (query: string) => {
      if (query.includes("status{")) return { status: adminStatus };
      if (query.includes("shortChat(")) return { shortChat: current };
      if (query.includes("shortChatModelOptions"))
        return { shortChatModelOptions: { presets: [], defaults: {} } };
      if (isTranscriptQuery(query))
        return bootstrapData(
          query,
          { ...transcript, agent: current, messages: [] },
          { agents: [current] },
        );
      if (query.includes("agents(projectId:"))
        return { project, agents: [current] };
      return config;
    };
    await installPushFixture(page, resolve);
    await page.route("**/graphql", async (route) => {
      const { query } = route.request().postDataJSON();
      await route.fulfill({ json: { data: resolve(query) } });
    });
    await page.route(`**/transcript-export/${current.id}`, (route) =>
      route.fulfill({ json: archive }),
    );

    await page.goto(
      short
        ? `/#/chats/${current.id}`
        : `/#/projects/${project.id}?agent=${current.id}`,
    );
    await page
      .getByRole("button", {
        name: short ? "Unterhaltungsaktionen" : "Projektaktionen",
        exact: true,
      })
      .click();
    const download = page.waitForEvent("download");
    await page.getByRole("menuitem", { name: "Als HTML exportieren" }).click();
    const file = await download;
    expect(file.suggestedFilename()).toBe("Exporttest.html");
    const path = testInfo.outputPath(file.suggestedFilename());
    await file.saveAs(path);

    const offline = await browser.newContext({ offline: true });
    try {
      const viewer = await offline.newPage();
      const network: string[] = [];
      const errors: string[] = [];
      viewer.on("request", (request) => {
        if (/^https?:/.test(request.url())) network.push(request.url());
      });
      viewer.on("pageerror", (error) => errors.push(error.message));
      await viewer.goto(pathToFileURL(path).href);

      await expect(
        viewer.getByRole("heading", { name: "Exporttest" }),
      ).toBeVisible();
      await expect(viewer.locator("html")).toHaveClass("dark");
      await expect(viewer.getByText(literal)).toBeVisible();
      await expect(viewer.locator("main strong")).toHaveText(
        "Offline gerendert",
      );
      await expect(
        viewer.getByRole("link", { name: "notes.txt" }),
      ).toHaveAttribute("download", "notes.txt");
      const disclosure = viewer.getByRole("button", {
        name: /Webseite abrufen/,
      });
      await expect(disclosure).toBeVisible();
      await disclosure.click();
      await expect(viewer.getByText("Text aus der Webseite")).toBeVisible();
      await expect(
        viewer.locator('script[type="application/json"]'),
      ).toHaveCount(1);
      await expect(
        viewer.locator("script[src], link[rel=stylesheet]"),
      ).toHaveCount(0);

      if (!short) {
        await viewer.getByLabel("Thread auswählen").selectOption("2");
        await expect(
          viewer.getByRole("heading", { name: "Enkelagent" }),
        ).toBeVisible();
        await expect(viewer.getByText("Zweite Ebene")).toBeVisible();
      }
      expect(errors).toEqual([]);
      expect(network).toEqual([]);
    } finally {
      await offline.close();
    }
  });
}
