import { expect, test } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { join } from "node:path";

import { bootstrapData, isTranscriptQuery } from "./fixtures/bootstrap-data";
import { installPushFixture } from "./fixtures/push-fixture";
import {
  agent,
  agents,
  config,
  project,
  transcript,
} from "./fixtures/visual-data";

test("workspace Office links reuse a warmed editor and read the current file", async ({
  page,
}) => {
  const fixtures = process.env.ZENTRALE_OFFICE_FIXTURES;
  test.skip(!fixtures, "Office fixture directory is required.");
  const workbook = await readFile(join(fixtures!, "sample.xlsx"));
  const resolve = (query: string) => {
    if (query.includes("status{"))
      return { status: { authenticated: true, needsSetup: false } };
    if (isTranscriptQuery(query))
      return bootstrapData(query, {
        ...transcript,
        messages: [
          {
            ...transcript.messages[0],
            id: "office-link",
            role: "assistant",
            content: "[Anleitung](docs/guide.md)",
            wire: "",
          },
        ],
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
  let downloads = 0;
  await page.route("**/workspace-file/**", async (route) => {
    const query = new URL(route.request().url()).searchParams;
    if (query.get("path") === "docs/guide.md")
      return route.fulfill({ body: "# Anleitung\n\n[Tabelle](prices.xlsx)" });
    expect(query.get("path")).toBe("docs/prices.xlsx");
    expect(query.get("preview")).toBe("office");
    downloads++;
    return route.fulfill({
      contentType: "application/octet-stream",
      body: workbook,
    });
  });

  await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
  await page.getByRole("link", { name: "Anleitung" }).click();
  const guide = page.getByRole("dialog", { name: "guide.md" });
  const prices = guide.getByRole("link", { name: "Tabelle" });
  await prices.hover();
  const spare = page.locator('iframe[title="Euro-Office"]');
  await expect(spare).toHaveAttribute("src", /spreadsheeteditor/);
  const warmFrame = await (await spare.elementHandle()).contentFrame();
  expect(downloads).toBe(0);
  await prices.click();
  const preview = page.getByRole("dialog", { name: "prices.xlsx" });
  const frame = preview.locator("iframe");
  await expect(frame).toBeVisible({ timeout: 30_000 });
  expect(await (await frame.elementHandle()).contentFrame()).toBe(warmFrame);

  await preview.getByRole("button", { name: "Zurück" }).click();
  await expect(guide).toBeVisible();
  await prices.click();
  await expect(frame).toBeVisible({ timeout: 30_000 });
  expect(downloads).toBe(2);
  await page.keyboard.press("Escape");
  await expect(preview).not.toBeVisible();
  await expect(guide).toBeVisible();
});

function twoPagePDF() {
  const drawing = "0 0 0 rg 32 48 128 64 re f\n";
  const objects = [
    "<< /Type /Catalog /Pages 2 0 R >>",
    "<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 /MediaBox [0 0 360 480] >>",
    "<< /Type /Page /Parent 2 0 R /Contents 5 0 R /Resources << >> >>",
    "<< /Type /Page /Parent 2 0 R /Contents 5 0 R /Resources << >> >>",
    `<< /Length ${drawing.length} >>\nstream\n${drawing}endstream`,
  ];
  let pdf = "%PDF-1.4\n";
  const offsets = objects.map((object, index) => {
    const offset = pdf.length;
    pdf += `${index + 1} 0 obj\n${object}\nendobj\n`;
    return `${String(offset).padStart(10, "0")} 00000 n \n`;
  });
  const xref = pdf.length;
  pdf += `xref\n0 6\n0000000000 65535 f \n${offsets.join("")}trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
  return Buffer.from(pdf, "ascii");
}

test("workspace documents open in a stack and use the conversation's file route", async ({
  page,
}) => {
  const workingAgent = { ...agent, status: "running" };
  let progress = {
    repositoryStep: "Receiving objects",
    repositoryPercent: 43,
    repositoryDetail: "1.72 GiB | 8.00 MiB/s",
  };
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
          ...progress,
          hostKey: {},
        },
      });
    if (query.includes("agents(projectId:")) return { project, agents };
    return config;
  };
  const push = await installPushFixture(page, resolve);
  await page.route("**/graphql", async (route) => {
    await route.fulfill({
      json: { data: resolve(route.request().postDataJSON().query) },
    });
  });
  const payload = Buffer.from([0, 255, 1, 2]);
  const pdf = twoPagePDF();
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
  const repositoryStatus = page
    .getByRole("status")
    .filter({ hasText: "Repository wird vorbereitet" });
  await expect(repositoryStatus).toContainText(/\(43\s%\)/);
  await expect(repositoryStatus).toContainText(
    "Objekte werden empfangen · 1.72 GiB | 8.00 MiB/s",
  );
  progress = {
    repositoryStep: "Writing checkout data",
    repositoryPercent: -1,
    repositoryDetail: "203.38 MiB",
  };
  await push.push();
  await expect(repositoryStatus).toContainText(
    "Git schreibt Daten für den Checkout · 203.38 MiB",
  );
  await expect(page.getByRole("link", { name: "Extern" })).toHaveAttribute(
    "href",
    "https://example.test/paper.pdf",
  );
  await page.getByRole("link", { name: "Anleitung" }).click();
  const guide = page.getByRole("dialog", { name: "guide.md" });
  await expect(guide.getByRole("heading", { name: "Anleitung" })).toBeVisible();
  await guide.getByRole("link", { name: "Paper" }).click();
  const paper = page.getByRole("dialog", { name: "main.pdf" });
  const viewer = paper.frameLocator("iframe");
  const expectPaintedPage = async (number: number) => {
    const canvas = viewer
      .locator(`.page[data-page-number="${number}"] canvas`)
      .first();
    await expect
      .poll(async () => {
        if (!(await canvas.count())) return false;
        return canvas.evaluate((element: HTMLCanvasElement) => {
          const pixels = element
            .getContext("2d")
            ?.getImageData(0, 0, element.width, element.height).data;
          return pixels?.some(
            (value, index) =>
              index % 4 === 0 && value < 64 && pixels[index + 3] > 0,
          );
        });
      })
      .toBe(true);
  };
  await expectPaintedPage(1);
  await expect(viewer.locator("#zoomInButton")).toBeVisible();
  await expect(viewer.locator("#downloadButton")).toBeVisible();
  await viewer.locator("#next").click();
  await expect(viewer.locator("#pageNumber")).toHaveValue("2");
  await expectPaintedPage(2);
  const savingPDF = page.waitForEvent("download");
  await viewer.locator("#downloadButton").click();
  const savedPDF = await savingPDF;
  expect(savedPDF.suggestedFilename()).toBe("main.pdf");
  expect(await readFile((await savedPDF.path())!)).toEqual(pdf);
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
