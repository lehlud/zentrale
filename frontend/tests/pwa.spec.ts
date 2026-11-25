import { expect, test } from "@playwright/test";

test("Zentrale meets the browser install criteria and has working icons", async ({
  page,
}) => {
  const entry = await page.goto("/");
  expect(entry?.ok()).toBe(true);

  await expect(
    page.locator('link[rel="icon"][type="image/svg+xml"]'),
  ).toHaveAttribute("href", "/icons/zentrale.svg?v=3");
  await expect(page.locator('link[rel="apple-touch-icon"]')).toHaveAttribute(
    "href",
    "/icons/apple-touch-icon.png?v=3",
  );

  const sizes = await page.evaluate(async () => {
    const paths = [
      "/icons/favicon-32.png?v=3",
      "/icons/apple-touch-icon.png?v=3",
      "/icons/pwa-192.png?v=3",
      "/icons/pwa-512.png?v=3",
      "/icons/pwa-maskable-512.png?v=3",
    ];

    return Promise.all(
      paths.map(async (path) => {
        const icon = new Image();
        icon.src = path;
        await icon.decode();
        return [icon.naturalWidth, icon.naturalHeight];
      }),
    );
  });
  expect(sizes).toEqual([
    [32, 32],
    [180, 180],
    [192, 192],
    [512, 512],
    [512, 512],
  ]);

  const manifestResponse = await page.request.get("/manifest.webmanifest");
  expect(manifestResponse.ok()).toBe(true);
  expect(await manifestResponse.json()).toMatchObject({
    start_url: "./",
    scope: "./",
    display: "standalone",
    icons: expect.arrayContaining([
      expect.objectContaining({ purpose: "maskable" }),
    ]),
  });

  const browser = await page.context().newCDPSession(page);
  await expect
    .poll(async () => {
      const result = await browser.send("Page.getInstallabilityErrors");
      return result.installabilityErrors;
    })
    .toEqual([]);
});

test("the worker offers an offline page without caching app data", async ({
  page,
  context,
}) => {
  await page.goto("/");
  await expect
    .poll(() =>
      page.evaluate(() => navigator.serviceWorker.controller?.scriptURL),
    )
    .toMatch(/\/pwa-sw\.js$/);

  const privatePath = "/attachments/pwa-test/private";
  const attachmentResponse = page.waitForResponse(
    (response) => new URL(response.url()).pathname === privatePath,
  );
  const status = await page.evaluate(
    async (path) => (await fetch(path)).status,
    privatePath,
  );
  expect(status).toBe(401);
  expect((await attachmentResponse).fromServiceWorker()).toBe(false);
  expect(await page.evaluate(() => caches.keys())).toEqual([]);

  try {
    await context.setOffline(true);
    const offline = await page.reload({ waitUntil: "domcontentloaded" });
    expect(offline?.status()).toBe(503);
    expect(offline?.fromServiceWorker()).toBe(true);
    expect(offline?.headers()["cache-control"]).toBe("no-store");
    await expect(
      page.getByRole("heading", { name: "Keine Verbindung zum Server" }),
    ).toBeVisible();
    expect(await page.evaluate(() => caches.keys())).toEqual([]);
  } finally {
    await context.setOffline(false);
  }

  await page.getByRole("link", { name: "Erneut versuchen" }).click();
  await expect(page.locator('link[rel="manifest"]')).toHaveAttribute(
    "href",
    "/manifest.webmanifest",
  );
});
