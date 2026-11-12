import { expect, test, type Page } from "@playwright/test";
import { bootstrapData, isTranscriptQuery } from "./fixtures/bootstrap-data";
import {
  agent,
  agents,
  config,
  project,
  transcript,
} from "./fixtures/visual-data";
import { largeTextPaste, MAX_ATTACHMENT_BYTES } from "../src/attachments";

test("large text threshold keeps ordinary pastes inline", () => {
  expect(largeTextPaste("a".repeat(12000))).toBe(false);
  expect(largeTextPaste("a".repeat(12001))).toBe(true);
  expect(largeTextPaste("x\n".repeat(200))).toBe(true);
  expect(largeTextPaste("ordinary pasted text")).toBe(false);
});

async function setup(page: Page) {
  const submissions: Array<{ attachmentIds: string[]; text: string }> = [];
  await page.addInitScript(() => localStorage.setItem("zentrale-locale", "de"));
  await page.routeWebSocket("**/events?*", () => {});
  await page.route("**/graphql", (route) => {
    const { query, variables = {} } = route.request().postDataJSON();
    let data: unknown = config;
    if (query.includes("status{"))
      data = { status: { needsSetup: false, authenticated: true } };
    else if (query.includes("prepareAgent"))
      data = { prepareAgent: { connected: true } };
    else if (query.includes("submitInput(")) {
      submissions.push(variables);
      data = {
        submitInput: {
          ...variables,
          status: "pending",
          createdAt: new Date().toISOString(),
          deliveredAt: "0001-01-01T00:00:00Z",
          attachments: [],
        },
      };
    } else if (isTranscriptQuery(query))
      data = {
        ...transcript,
        messages: [],
        recentMessages: [],
        agentInputs: [],
        jobs: [],
        questions: [],
        goal: null,
      };
    else if (query.includes("agents(projectId:")) data = { project, agents };
    return route.fulfill({ json: { data: bootstrapData(query, data) } });
  });
  await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
  const input = page.getByRole("textbox", {
    name: "Aufgabe oder neue Instruktion",
  });
  await expect(input).toBeVisible();
  return { input, submissions, form: input.locator("xpath=ancestor::form") };
}

async function paste(page: Page, value: string, image = false) {
  await page
    .getByRole("textbox", { name: "Aufgabe oder neue Instruktion" })
    .evaluate(
      (element, { value, image }) => {
        const data = new DataTransfer();
        if (image)
          data.items.add(
            new File(
              [Uint8Array.from(atob(value), (c) => c.charCodeAt(0))],
              "clipboard.png",
              { type: "image/png" },
            ),
          );
        else data.setData("text/plain", value);
        element.dispatchEvent(
          new ClipboardEvent("paste", {
            bubbles: true,
            cancelable: true,
            clipboardData: data,
          }),
        );
      },
      { value, image },
    );
}

for (const mobile of [false, true]) {
  test(`image paste and binary drop produce previews and file-only input (${mobile ? "mobile" : "desktop"})`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: mobile ? 390 : 1440, height: 950 });
    const { input, submissions, form } = await setup(page);
    const uploads: Array<{ id: string; name: string; bytes: number }> = [];
    await page.route("**/attachments/**", async (route) => {
      const request = route.request();
      const url = new URL(request.url());
      const id = url.pathname.split("/").at(-1)!;
      const name = url.searchParams.get("name")!;
      uploads.push({ id, name, bytes: request.postDataBuffer()?.length ?? 0 });
      return route.fulfill({
        json: {
          id,
          name,
          mimeType: request.headers()["content-type"],
          size: request.postDataBuffer()?.length ?? 0,
          path: `/tmp/zentrale/attachments/${id}/${name}`,
          machineId: "vm",
        },
      });
    });
    await paste(
      page,
      "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aL1sAAAAASUVORK5CYII=",
      true,
    );
    const preview = form.getByRole("img", { name: "clipboard.png" });
    await expect(preview).toHaveAttribute("src", /^blob:/);
    await expect
      .poll(() =>
        preview.evaluate((image) => (image as HTMLImageElement).naturalWidth),
      )
      .toBe(1);
    await form.evaluate((element) => {
      const data = new DataTransfer();
      data.items.add(
        new File([new Uint8Array([0, 255, 10, 128])], "binary.dat", {
          type: "application/octet-stream",
        }),
      );
      element.dispatchEvent(
        new DragEvent("drop", {
          bubbles: true,
          cancelable: true,
          dataTransfer: data,
        }),
      );
    });
    await expect(form.getByText("binary.dat", { exact: true })).toBeVisible();
    await expect(input).toHaveValue("");
    await input.press("Enter");
    await expect.poll(() => submissions.length).toBe(1);
    expect(uploads.map((entry) => entry.name)).toEqual([
      "clipboard.png",
      "binary.dat",
    ]);
    expect(uploads[1].bytes).toBe(4);
    expect(submissions[0].attachmentIds).toEqual(
      uploads.map((entry) => entry.id),
    );
    expect(submissions[0].text).toBe("");
    await expect(
      form.getByRole("list", { name: "Anhänge", exact: true }),
    ).toHaveCount(0);
  });
}

test("large paste becomes removable text attachment; oversized files never upload", async ({
  page,
}) => {
  const { form, input } = await setup(page);
  await input.fill("Keep my draft");
  await paste(page, "pasted code\n".repeat(220));
  await expect(
    form.getByText("eingefuegter-text.txt", { exact: true }),
  ).toBeVisible();
  await expect(input).toHaveValue("Keep my draft");
  await form
    .getByRole("button", { name: "Anhang entfernen: eingefuegter-text.txt" })
    .click();
  await expect(
    form.getByRole("list", { name: "Anhänge", exact: true }),
  ).toHaveCount(0);
  await form.locator('input[type="file"]').setInputFiles({
    name: "too-big.bin",
    mimeType: "application/octet-stream",
    buffer: Buffer.alloc(MAX_ATTACHMENT_BYTES + 1),
  });
  await expect(
    form.getByText("Eine Datei darf höchstens 25 MiB groß sein.", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(input).toHaveValue("Keep my draft");
});

test("failed upload preserves draft and retries with the same stable attachment ID", async ({
  page,
}) => {
  const { form, input, submissions } = await setup(page);
  const ids: string[] = [];
  await page.route("**/attachments/**", (route) => {
    const url = new URL(route.request().url());
    const id = url.pathname.split("/").at(-1)!;
    ids.push(id);
    return ids.length === 1
      ? route.fulfill({ status: 503, json: { message: "Try again" } })
      : route.fulfill({
          json: {
            id,
            name: "empty.txt",
            mimeType: "text/plain",
            size: 0,
            path: `/tmp/zentrale/attachments/${id}/empty.txt`,
            machineId: "vm",
          },
        });
  });
  await input.fill("Retain me");
  await form.locator('input[type="file"]').setInputFiles({
    name: "empty.txt",
    mimeType: "text/plain",
    buffer: Buffer.alloc(0),
  });
  await input.press("Enter");
  await expect(
    form.getByText("Upload HTTP 503: Try again", { exact: true }),
  ).toBeVisible();
  await expect(input).toHaveValue("Retain me");
  expect(submissions).toHaveLength(0);
  await input.press("Enter");
  await expect.poll(() => submissions.length).toBe(1);
  expect(ids).toHaveLength(2);
  expect(ids[1]).toBe(ids[0]);
  await expect(input).toHaveValue("");
});
