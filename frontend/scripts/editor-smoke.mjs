import { chromium, expect } from "@playwright/test";
import { readFile, mkdir } from "node:fs/promises";
import path from "node:path";

const [baseURL, projectId, workspace, mode = "local"] = process.argv.slice(2);

const microvm = mode === "microvm";

const artifactPrefix = microvm ? "vscode-microvm" : "vscode-real-ssh";

const browser = await chromium.launch({
  executablePath: process.env.CHROMIUM_PATH,
  args: ["--no-sandbox", "--disable-dev-shm-usage"],
});

const context = await browser.newContext({
  viewport: { width: 1440, height: 1000 },
});

try {
  await context.addInitScript(() => {
    try {
      localStorage.setItem("zentrale-locale", "en");
      localStorage.setItem("zentrale-theme", "dark");
    } catch {
      /* Cross-origin VS Code webview frames intentionally lack storage access. */
    }
  });
  const page = await context.newPage();
  const status = await (
    await page.request.post(`${baseURL}/graphql`, {
      data: { query: "query{status{needsSetup}}" },
    })
  ).json();
  await page.request.post(`${baseURL}/graphql`, {
    data: {
      query: status.data.status.needsSetup
        ? 'mutation($password:String!){setup(password:$password,commitIdentity:{name:"VS Code Smoke",email:"vscode-smoke@example.test"})}'
        : "mutation($password:String!){login(password:$password)}",
      variables: { password: "isolated editor smoke password" },
    },
  });
  const readWorkspace = async (filename) => {
    if (!microvm) {
      return readFile(path.join(workspace, filename), "utf8");
    }

    const response = await page.request.get(
      `${baseURL}/_test/workspace?file=${encodeURIComponent(filename)}`,
    );

    return response.ok() ? response.text() : "";
  };
  await page.goto(`${baseURL}/#/projects/${projectId}`);
  await expect(
    page.getByRole("link", { name: "Open VS Code in a new tab ↗" }),
  ).toBeVisible();
  const opened = context.waitForEvent("page");
  await page.getByRole("link", { name: "Open VS Code in a new tab ↗" }).click();
  const editor = await opened;
  editor.on("console", (message) => {
    if (message.type() === "error") console.error(message.text());
  });
  editor.on("pageerror", (error) => console.error(error.message));
  editor.setDefaultTimeout(45000);
  await editor.waitForURL(/\/ide\//, { timeout: microvm ? 300000 : 90000 });
  await expect(editor.locator(".monaco-workbench")).toBeVisible();
  await expect(page.locator("iframe")).toHaveCount(0);
  expect(await editor.evaluate(() => window.opener === null)).toBe(true);
  const bounds = await editor.locator(".monaco-workbench").boundingBox();
  expect(bounds.width).toBe(1440);
  expect(bounds.height).toBe(1000);
  await editor.keyboard.press("Control+p");
  const quickInput = editor
    .locator(".quick-input-widget input")
    .filter({ visible: true });
  await quickInput.fill("remote.py");
  await editor
    .getByRole("option", { name: /remote.py/ })
    .first()
    .click();
  await expect(editor.locator(".view-lines").first()).toContainText(
    "remote workspace",
    { timeout: 30000 },
  );
  await editor.keyboard.press("Control+End");
  await editor.keyboard.type("\n# saved through the SSH remote agent\n");
  await editor.keyboard.press("Control+s");
  await expect
    .poll(() => readWorkspace("remote.py"))
    .toContain("saved through the SSH remote agent");
  await editor.keyboard.press("Control+Shift+p");
  await quickInput.fill(">Workspaces: Manage Workspace Trust");
  await editor
    .getByRole("option", { name: /Manage Workspace Trust/ })
    .first()
    .click();
  await editor.getByRole("button", { name: "Trust", exact: true }).click();
  await expect(
    editor.getByText("You trust this folder", { exact: true }),
  ).toBeVisible();
  await editor.keyboard.press("Escape");
  await expect(
    editor.getByText("You trust this folder", { exact: true }),
  ).not.toBeVisible();
  await editor.keyboard.press("Control+Shift+p");
  await quickInput.fill(">Terminal: Create New Terminal");
  await editor
    .getByRole("option", { name: /Create New Terminal/ })
    .first()
    .click();
  await expect(editor.locator(".xterm").first()).toBeVisible();
  await editor.locator(".xterm").first().click();
  const terminalFile = path.join(workspace, "terminal-proof.txt");
  const quote = (value) => "'" + value.replaceAll("'", "'\\''") + "'";
  await editor.keyboard.type(
    `printf 'remote terminal works' > ${quote(terminalFile)}`,
  );
  await editor.keyboard.press("Enter");
  await expect
    .poll(() => readWorkspace("terminal-proof.txt").catch(() => ""))
    .toBe("remote terminal works");
  if (microvm) {
    await editor.keyboard.type(
      `printf '%s\\n' "$ZENTRALE_SANDBOX" "$GIT_AUTHOR_NAME" "$GIT_AUTHOR_EMAIL" > sandbox-proof.txt; cat /proc/sys/kernel/random/boot_id >> sandbox-proof.txt; git init -q; git commit --allow-empty -qm 'VS Code guest commit'; git log -1 --format='%an|%ae|%cn|%ce' >> sandbox-proof.txt`,
    );
    await editor.keyboard.press("Enter");
    await expect
      .poll(() => readWorkspace("sandbox-proof.txt"), { timeout: 30000 })
      .toContain(
        "VM Project Author|vm-project@example.test|VM Project Author|vm-project@example.test",
      );
    const proof = await readWorkspace("sandbox-proof.txt");
    expect(proof).toMatch(
      /^microvm\nVM Project Author\nvm-project@example.test\n/,
    );
    console.log(
      "VS Code terminal used the Fedora guest and project commit identity.",
    );
  }

  await editor.keyboard.press("Control+p");
  await quickInput.fill("remote.ts");
  await editor
    .getByRole("option", { name: /remote.ts/ })
    .first()
    .click();
  await editor.keyboard.press("Control+Shift+m");
  await expect(
    editor
      .getByText("Type 'string' is not assignable to type 'number'.", {
        exact: false,
      })
      .first(),
  ).toBeVisible({ timeout: 30000 });
  const directory = path.resolve("../../frontend/artifacts/ui");
  await mkdir(directory, { recursive: true });
  await editor.screenshot({
    path: path.join(directory, `${artifactPrefix}-workbench.png`),
    animations: "disabled",
  });
  const germanURL = new URL(editor.url());
  germanURL.searchParams.set("locale", "de");
  germanURL.searchParams.set("theme", "light");
  await editor.goto(germanURL.toString());
  await expect(
    editor.getByRole("menuitem", { name: "Anwendungsmenü", exact: true }),
  ).toBeVisible({ timeout: 30000 });
  await editor.keyboard.press("Control+p");
  await quickInput.fill("remote.ts");
  await editor
    .getByRole("option", { name: /remote.ts/ })
    .first()
    .click();
  await expect(editor.locator(".view-lines").first()).toContainText("answer");
  await expect(editor.locator(".monaco-workbench")).not.toHaveClass(/vs-dark/);
  await editor.screenshot({
    path: path.join(directory, `${artifactPrefix}-workbench-de.png`),
    animations: "disabled",
  });
  await editor.setViewportSize({ width: 390, height: 844 });
  await editor.keyboard.press("Control+b");
  await expect(editor.locator(".monaco-workbench")).toBeVisible();
  const mobileBounds = await editor.locator(".monaco-workbench").boundingBox();
  expect(mobileBounds.width).toBe(390);
  expect(mobileBounds.height).toBe(844);
  await editor.screenshot({
    path: path.join(directory, `${artifactPrefix}-workbench-mobile.png`),
    animations: "disabled",
  });
  await import("node:fs/promises").then(({ writeFile }) =>
    writeFile(
      path.join(directory, `${artifactPrefix}.json`),
      JSON.stringify({
        captures: [
          {
            name: `${artifactPrefix}-desktop-en`,
            file: `${artifactPrefix}-workbench.png`,
          },
          {
            name: `${artifactPrefix}-desktop-de`,
            file: `${artifactPrefix}-workbench-de.png`,
          },
          {
            name: `${artifactPrefix}-mobile-de`,
            file: `${artifactPrefix}-workbench-mobile.png`,
          },
        ],
      }),
    ),
  );
  const assetsURL = new URL(editor.url());
  assetsURL.pathname += "static/out/nls.messages.js";
  assetsURL.search = "";
  await page.request.get(`${baseURL}/_test/disconnect`);
  const asset = await page.request.get(assetsURL.toString());
  expect(asset.status()).toBe(200);
  expect(await asset.text()).toContain("_VSCODE_NLS_MESSAGES");
  console.log(
    "Real VS Code: central workbench, full-tab launch, remote file save, SSH terminal, TypeScript diagnostics and local assets after tunnel loss verified.",
  );
} catch (error) {
  const lastPage = context.pages().at(-1);
  if (lastPage) {
    const directory = path.resolve("../../frontend/artifacts/ui");
    await mkdir(directory, { recursive: true });
    await lastPage
      .screenshot({
        path: path.join(directory, `${artifactPrefix}-failed.png`),
      })
      .catch(() => {});
    console.error(
      (
        await lastPage
          .locator("body")
          .innerText()
          .catch(() => "")
      ).slice(0, 8000),
    );
  }

  throw error;
} finally {
  await browser.close();
}
