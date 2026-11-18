import { expect, test } from "@playwright/test";
import { readFile } from "node:fs/promises";

import { bootstrapData, isTranscriptQuery } from "./fixtures/bootstrap-data";
import { installPushFixture } from "./fixtures/push-fixture";
import {
  agent,
  agents,
  config,
  project,
  transcript,
} from "./fixtures/visual-data";

test("workspace documents open in a stack and use the conversation's file route", async ({
  page,
}) => {
  const workingAgent = { ...agent, status: "running" };
  const message = {
    ...transcript.messages[0],
    id: "workspace-links",
    role: "assistant",
    content:
      "[Anleitung](docs/guide.md) · [Extern](https://example.test/paper.pdf)",
    wire: "",
  };
  const resolve = (query: string) => {
    if (query.includes("status{"))
      return { status: { authenticated: true, needsSetup: false } };
    if (isTranscriptQuery(query))
      return bootstrapData(query, {
        ...transcript,
        agent: workingAgent,
        messages: [message],
        agentRunState: {
          agentId: agent.id,
          phase: "repository",
          repositoryStep: "Receiving objects",
          repositoryPercent: 43,
          hostKey: {},
        },
      });
    if (query.includes("agents(projectId:")) return { project, agents };
    return config;
  };
  await installPushFixture(page, resolve);
  await page.route("**/graphql", async (route) => {
    await route.fulfill({
      json: { data: resolve(route.request().postDataJSON().query) },
    });
  });
  const payload = Buffer.from([0, 255, 1, 2]);
  const pdf = await readFile(new URL("../../spec.pdf", import.meta.url));
  const requests: string[] = [];
  await page.route("**/workspace-file/**", async (route) => {
    const url = new URL(route.request().url());
    expect(url.pathname).toBe(`/workspace-file/${project.id}`);
    expect(url.searchParams.get("agent")).toBe(agent.id);
    const path = url.searchParams.get("path") ?? "";
    requests.push(path);
    const files: Record<string, string | Buffer> = {
      "docs/guide.md":
        "# Anleitung\n\n[Paper](../paper/main.pdf) · [Text](local%20notes.txt) · [Archiv](../data.zip)",
      "paper/main.pdf": pdf,
      "docs/local notes.txt": "Ein lokaler Text <ohne HTML>.",
      "data.zip": payload,
    };
    if (!(path in files)) return route.fulfill({ status: 404 });
    await route.fulfill({
      body: files[path],
      contentType: "application/octet-stream",
      headers: {
        "Content-Disposition": `attachment; filename="${path.split("/").at(-1)}"`,
      },
    });
  });

  await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
  await expect(
    page.getByRole("status").filter({ hasText: "Repository wird vorbereitet" }),
  ).toContainText(/\(43\s%\)/);
  await expect(page.getByRole("link", { name: "Extern" })).toHaveAttribute(
    "href",
    "https://example.test/paper.pdf",
  );
  await page.getByRole("link", { name: "Anleitung" }).click();
  const guide = page.getByRole("dialog", { name: "guide.md" });
  await expect(guide.getByRole("heading", { name: "Anleitung" })).toBeVisible();
  await guide.getByRole("link", { name: "Paper" }).click();
  const paper = page.getByRole("dialog", { name: "main.pdf" });
  const canvas = paper.getByRole("img", { name: "Seite 1" });
  await expect
    .poll(async () => {
      if (!(await canvas.count())) return false;
      return canvas.evaluate((element: HTMLCanvasElement) => {
        const pixels = element
          .getContext("2d")
          ?.getImageData(0, 0, element.width, element.height).data;
        return pixels?.some((value, index) => index % 4 === 3 && value > 0);
      });
    })
    .toBe(true);
  await paper.getByRole("button", { name: "Nächste Seite" }).click();
  await expect(paper.getByRole("img", { name: "Seite 2" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(paper).toHaveCount(0);
  await guide.getByRole("link", { name: "Text" }).click();
  const text = page.getByRole("dialog", { name: "local notes.txt" });
  await expect(text.getByText("Ein lokaler Text <ohne HTML>.")).toBeVisible();
  await text.getByRole("button", { name: "Zurück" }).click();

  await guide.getByRole("link", { name: "Archiv" }).click();
  const archive = page.getByRole("dialog", { name: "data.zip" });
  await expect(
    archive.getByText("Für diesen Dateityp gibt es keine Vorschau."),
  ).toBeVisible();
  expect(requests).not.toContain("data.zip");
  const downloaded = page.waitForEvent("download");
  await archive.getByRole("button", { name: "Herunterladen" }).click();
  const download = await downloaded;
  expect(download.suggestedFilename()).toBe("data.zip");
  expect(await readFile((await download.path())!)).toEqual(payload);
  await archive.getByRole("button", { name: "Zurück" }).click();
  await expect(guide.getByRole("heading", { name: "Anleitung" })).toBeVisible();
  expect(requests.filter((value) => value === "docs/guide.md")).toHaveLength(1);
  await guide.getByRole("button", { name: "Schließen" }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
});
