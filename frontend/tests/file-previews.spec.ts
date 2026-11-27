import { expect, test, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import type { Message } from "../src/generated";
import { bootstrapData, isTranscriptQuery } from "./fixtures/bootstrap-data";
import { installPushFixture } from "./fixtures/push-fixture";
import { adminStatus, agent, config, transcript } from "./fixtures/visual-data";

const chat = {
  ...agent,
  id: "file-preview-chat",
  projectId: "",
  status: "idle",
};
const pixel = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jKLsAAAAASUVORK5CYII=",
  "base64",
);

function attachment(id: string, name: string, mimeType: string, size: number) {
  return {
    id,
    agentId: chat.id,
    projectId: "",
    name,
    mimeType,
    size,
    path: "",
    machineId: "",
  };
}

async function openChat(
  page: Page,
  files: Message["attachments"],
  output?: Message["attachments"][number],
) {
  await page.addInitScript(() => {
    if (window === top) localStorage.setItem("zentrale-locale", "de");
  });
  const user: Message = {
    ...transcript.messages[0],
    id: "user-files",
    lineageId: "user-files",
    agentId: chat.id,
    role: "user",
    content: "Bitte bearbeite diese Datei.",
    toolName: "",
    callId: "",
    change: null,
    changes: [],
    activities: [],
    attachments: files,
  };
  const messages = [user];
  if (output) {
    messages.push({
      ...user,
      id: "file-call",
      role: "assistant",
      content: "",
      attachments: [],
      wire: JSON.stringify({
        type: "function_call",
        call_id: "file-call",
        name: "deliver_file",
        arguments: JSON.stringify({ path: output.name }),
      }),
    });
    messages.push({
      ...user,
      id: "file-output",
      role: "tool",
      callId: "file-call",
      toolName: "deliver_file",
      content: JSON.stringify({ result: { attachment: output } }),
      wire: "",
      attachments: [output],
    });
  }
  const resolve = (query: string) => {
    if (query.includes("status{")) return { status: adminStatus };
    if (query.includes("shortChatModelOptions"))
      return { shortChatModelOptions: { presets: [], defaults: {} } };
    if (query.includes("shortChat(")) return { shortChat: chat };
    if (isTranscriptQuery(query))
      return bootstrapData(
        query,
        { ...transcript, agent: chat, messages },
        { agents: [chat] },
      );
    return config;
  };
  await installPushFixture(page, resolve);
  await page.route("**/graphql", async (route) => {
    const { query } = route.request().postDataJSON();
    await route.fulfill({ json: { data: resolve(query) } });
  });
  await page.goto(`/#/chats/${chat.id}`);
}

test("an image attachment opens in a dialog, not a new tab", async ({
  page,
  context,
}) => {
  await page.route(`**/attachments/${chat.id}/picture`, (route) =>
    route.fulfill({ contentType: "image/png", body: pixel }),
  );
  await openChat(page, [
    attachment("picture", "bild.png", "image/png", pixel.length),
  ]);

  await page.getByRole("button", { name: "Bild öffnen: bild.png" }).click();
  const preview = page.getByRole("dialog", { name: "bild.png" });
  await expect(preview.getByRole("img", { name: "bild.png" })).toBeVisible();
  expect(context.pages()).toHaveLength(1);
  await page.keyboard.press("Escape");
  await expect(preview).toHaveCount(0);
});

const fixtureDirectory = process.env.ZENTRALE_OFFICE_FIXTURES;
for (const extension of ["xlsx", "docx", "pptx"]) {
  test(`renders ${extension} locally, including a delivered file`, async ({
    page,
    baseURL,
  }) => {
    test.skip(
      !fixtureDirectory,
      "Set ZENTRALE_OFFICE_FIXTURES to a directory with sample XLSX, DOCX and PPTX files.",
    );
    const bytes = await readFile(
      join(fixtureDirectory!, `sample.${extension}`),
    );
    const input = attachment(
      "file-input",
      `eingabe.${extension}`,
      "application/octet-stream",
      bytes.length,
    );
    const output = attachment(
      "delivered-output",
      `ergebnis.${extension}`,
      "application/octet-stream",
      bytes.length,
    );
    const outside: string[] = [];
    page.on("request", (request) => {
      if (
        request.url().startsWith("http") &&
        new URL(request.url()).origin !== new URL(baseURL!).origin
      )
        outside.push(request.url());
    });
    await page.route(`**/attachments/${chat.id}/*`, (route) =>
      route.fulfill({
        contentType: "application/octet-stream",
        headers: { "content-disposition": "attachment" },
        body: bytes,
      }),
    );
    await openChat(page, [input], output);

    if (extension === "xlsx") {
      await page.getByRole("button", { name: input.name, exact: true }).click();
      const preview = page.getByRole("dialog", { name: input.name });
      await expect(preview.locator("iframe")).toBeVisible({ timeout: 30_000 });
      await expect(
        preview
          .frameLocator("iframe")
          .getByText("Actual workbook", { exact: true })
          .filter({ visible: true })
          .first(),
      ).toBeVisible();
      await preview.getByRole("button", { name: "Schließen" }).click();
    }
    await page.getByRole("button", { name: output.name, exact: true }).click();
    const preview = page.getByRole("dialog", { name: output.name });
    await expect(preview.locator("iframe")).toBeVisible({ timeout: 30_000 });
    await expect(preview.locator("iframe")).toHaveAttribute(
      "sandbox",
      "allow-scripts",
    );
    const download = page.waitForEvent("download");
    await preview.getByRole("link", { name: "Herunterladen" }).click();
    expect((await download).suggestedFilename()).toBe(output.name);
    expect(outside).toEqual([]);
  });
}
