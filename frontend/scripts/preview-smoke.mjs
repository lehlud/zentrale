import { chromium, expect } from "@playwright/test";

const [baseURL, project, session, port] = process.argv.slice(2);
const browser = await chromium.launch({
  executablePath: process.env.CHROMIUM_PATH,
  // The isolated TLS terminator uses httptest's self-signed certificate.
  args: [
    "--no-sandbox",
    "--disable-dev-shm-usage",
    "--ignore-certificate-errors",
  ],
});
try {
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  await context.addCookies([
    {
      name: "zentrale_session",
      value: process.env.ZENTRALE_PREVIEW_TEST_TOKEN,
      url: baseURL,
      secure: true,
      httpOnly: true,
      sameSite: "Strict",
    },
  ]);
  const page = await context.newPage();
  page.on("pageerror", (error) => console.error(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") console.error(message.text());
  });
  const localRequests = [];
  page.on("request", (request) => {
    if (/^(http|ws):\/\/(localhost|127\.0\.0\.1):/.test(request.url()))
      localRequests.push(request.url());
  });
  const prefix = `/preview/${project}/${session}/${port}/`;
  await page.goto(baseURL + prefix);
  await expect(
    page.getByRole("heading", { name: "Preview ready" }),
  ).toBeVisible({ timeout: 30000 });
  expect(await page.evaluate(() => window.previewResults)).toEqual({
    get: { value: 42 },
    omit: { value: 42 },
    post: { posted: true },
    relative: "frontend",
    xhr: { value: 42 },
    event: "live-event",
    websocket: "hot-reload",
    cookies: {
      application_session: "application",
      zentrale_session: "changed-app",
      js_cookie: "works",
    },
  });
  expect(localRequests).toEqual([]);
  expect(await page.evaluate(() => window.previewCookies)).toContain(
    "zentrale_session=changed-app",
  );
  expect(await page.evaluate(() => window.previewCookies)).not.toMatch(
    /zpreview_|hidden_cookie|application_session/,
  );
  expect(
    await page.evaluate(() => localStorage.getItem("preview-app-storage")),
  ).toBe("works");
  await page.getByRole("link", { name: "Deep route" }).click();
  await expect(page).toHaveURL(baseURL + prefix + "deep");
  await expect(
    page.getByRole("heading", { name: "Preview ready" }),
  ).toBeVisible();
  await page.evaluate(() => history.pushState({}, "", "/client-route"));
  await expect(page).toHaveURL(baseURL + prefix + "client-route");
  await page.goto(baseURL + "/");
  expect(
    await page.evaluate(() => navigator.serviceWorker.controller),
  ).toBeNull();
  console.log(
    "HTTPS preview: loopback APIs, POST, XHR, SSE, WebSocket, root imports, navigation and worker scope passed.",
  );
} finally {
  await browser.close();
}
