import { expect, test } from "@playwright/test";
import { mkdir } from "node:fs/promises";

import type {
  MachineConnectionCheck,
  PoolMachineConnectionCheck,
} from "../src/generated";
import { config } from "./fixtures/visual-data";

for (const english of [false, true]) {
  test(`pool connection sample, failures and exact-member retries: ${english ? "mobile-en" : "desktop-de"}`, async ({
    page,
  }) => {
    const label = (de: string, en: string) => (english ? en : de);
    await page.setViewportSize({ width: english ? 390 : 1440, height: 1000 });
    await page.addInitScript((english) => {
      localStorage.setItem("zentrale-locale", english ? "en" : "de");
      localStorage.setItem("zentrale-theme", english ? "dark" : "light");
    }, english);

    const pool = {
      ...config.pools[0],
      id: "sample-pool",
      name: "Sample pool",
      machines: Array.from({ length: 40 }, (_, index) => ({
        ...config.machines[0],
        id: `worker-${index}`,
        name: `Worker ${index}`,
        host: `worker-${index}.example.test`,
      })),
    };
    const successful = (id: string): MachineConnectionCheck => ({
      connected: true,
      error: "",
      hostKey: { host: "", publicKey: "", fingerprint: "" },
      sandbox: {
        machineId: id,
        mode: "auto",
        level: "bwrap",
        checkedAt: "2026-11-09T12:00:00Z",
        error: "",
      },
    });
    const member = (
      index: number,
      patch: Partial<MachineConnectionCheck> = {},
    ): PoolMachineConnectionCheck => ({
      machineId: `${pool.id}/worker-${index}`,
      name: `Worker ${index}`,
      host: `worker-${index}.example.test`,
      port: 22,
      ...successful(`${pool.id}/worker-${index}`),
      ...patch,
    });
    const members = [
      member(7),
      member(19, { connected: false, error: "SSH authentication failed" }),
      member(31, {
        connected: false,
        error: "Host key is not trusted",
        hostKey: {
          host: "worker-31.example.test:22",
          publicKey: "ssh-ed25519 TEST_POOL_HOST",
          fingerprint: "SHA256:PoolHost",
        },
      }),
      member(38, {
        sandbox: {
          ...successful("").sandbox,
          machineId: `${pool.id}/worker-38`,
          level: "",
          error: "Sandbox probe unavailable",
        },
      }),
    ];
    let poolChecks = 0;
    const retries: string[] = [];
    let trustDecisions = 0;
    let release: () => void = () => {};
    const pending = new Promise<void>((resolve) => {
      release = resolve;
    });

    await page.routeWebSocket("**/events?*", () => {});
    await page.route("**/graphql", async (route) => {
      const { query, variables = {} } = route.request().postDataJSON();
      const send = (data: unknown) => route.fulfill({ json: { data } });
      if (query.includes("status{")) {
        return send({ status: { needsSetup: false, authenticated: true } });
      }
      if (query.includes("testPool(")) {
        poolChecks++;
        expect(variables.id).toBe(pool.id);
        if (poolChecks === 1) {
          return route.fulfill({
            json: {
              errors: [{ message: "Pool check interrupted. Try again." }],
            },
          });
        }
        await pending;
        return send({
          testPool: { poolId: pool.id, totalMachines: 40, machines: members },
        });
      }
      if (query.includes("testMachine(")) {
        retries.push(variables.id);
        return send({ testMachine: successful(variables.id) });
      }
      if (query.includes("trustHostKey(")) {
        expect(variables.publicKey).toBe("ssh-ed25519 TEST_POOL_HOST");
        trustDecisions++;
        return send({ trustHostKey: true });
      }
      return send({ ...config, pools: [pool] });
    });

    await page.goto("/#/machines");
    await page.getByRole("tab", { name: "Pools", exact: true }).click();
    const card = page.locator('[data-slot="card"]').filter({
      has: page.getByRole("heading", { name: pool.name, exact: true }),
    });
    const check = card.getByRole("button", {
      name: label("Verbindung prüfen", "Test connection"),
      exact: true,
    });
    await check.click();

    await expect(card.getByRole("alert")).toContainText(
      "Pool check interrupted",
    );

    await check.click();
    await expect(
      card.getByText(
        label("Verbindungen werden geprüft …", "Checking connections …"),
        { exact: true },
      ),
    ).toBeVisible();
    release();

    const results = card.getByRole("region", {
      name: label("Pool-Verbindungsprüfung", "Pool connection check"),
    });
    const summary = (count: number) =>
      label(
        `${count} von 4 geprüften Rechnern bereit (40 im Pool).`,
        `${count} of 4 checked machines ready (40 in the pool).`,
      );
    await expect(results).toContainText(summary(1));
    await expect(results.getByRole("listitem")).toHaveCount(4);
    await expect(results).toContainText("SSH authentication failed");
    await expect(results).toContainText("Sandbox probe unavailable");
    await expect(results).toContainText("SHA256:PoolHost");

    const row = (name: string) =>
      results
        .getByRole("listitem")
        .filter({ has: page.getByRole("heading", { name, exact: true }) });
    await row("Worker 19")
      .getByRole("button", {
        name: label("Erneut versuchen", "Try again"),
        exact: true,
      })
      .click();
    await expect(results).toContainText(summary(2));
    await expect(row("Worker 19").getByRole("alert")).toHaveCount(0);

    await row("Worker 31")
      .getByRole("button", {
        name: label(
          "Schlüssel vertrauen und erneut verbinden",
          "Trust key and reconnect",
        ),
        exact: true,
      })
      .click();
    await expect(results).toContainText(summary(3));
    await expect(row("Worker 31")).not.toContainText("SHA256:PoolHost");
    expect(trustDecisions).toBe(1);

    await row("Worker 38")
      .getByRole("button", {
        name: label("Erneut versuchen", "Try again"),
        exact: true,
      })
      .click();
    await expect(results).toContainText(summary(4));
    await expect(results.getByRole("listitem")).toHaveCount(4);
    expect(poolChecks).toBe(2);
    expect(retries).toEqual([
      `${pool.id}/worker-19`,
      `${pool.id}/worker-31`,
      `${pool.id}/worker-38`,
    ]);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth - innerWidth,
      ),
    ).toBeLessThanOrEqual(1);

    await mkdir("artifacts/ui", { recursive: true });
    await page.screenshot({
      path: `artifacts/ui/pool-connection-${english ? "mobile-en" : "desktop-de"}.png`,
      fullPage: true,
      animations: "disabled",
    });
  });
}
