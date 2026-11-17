import { expect, test } from "@playwright/test";
import { bootstrapData, isTranscriptQuery } from "./fixtures/bootstrap-data";

import {
  agent,
  agents,
  config,
  project,
  transcript,
} from "./fixtures/visual-data";

test("usage shows reported cache counts separately from missing measurements", async ({
  page,
}) => {
  await page.addInitScript(() => localStorage.setItem("zentrale-locale", "de"));
  await page.routeWebSocket("**/events?*", () => {});
  await page.route("**/graphql", (route) => {
    const { query } = route.request().postDataJSON();
    if (query.includes("inferenceTraces(")) {
      return route.fulfill({
        json: {
          data: {
            inferenceTraces: [
              {
                id: "trace-1",
                purpose: "main",
                modelId: "gpt-6.1-sol",
                reasoningLevel: "xhigh",
                durationMillis: 1200,
                outcome: "completed",
                prefixChange: "append_only",
                sharedMessages: 5,
                messageCount: 6,
                estimatedSystemTokens: 100,
                estimatedToolTokens: 200,
                estimatedConversationTokens: 300,
                estimatedRuntimeTokens: 20,
                inputTokens: 620,
                outputTokens: 10,
                cachedTokens: 500,
                tokensKnown: true,
                cacheReadKnown: true,
                attempts: [
                  {
                    number: 1,
                    transport: "http",
                    outcome: "stream_interrupted",
                    durationMillis: 300,
                    sentBytes: 1000,
                    tokensKnown: false,
                  },
                  {
                    number: 2,
                    transport: "http",
                    outcome: "completed",
                    durationMillis: 800,
                    sentBytes: 1000,
                    tokensKnown: true,
                  },
                ],
              },
            ],
          },
        },
      });
    }
    if (isTranscriptQuery(query)) {
      if (!query.includes("conversationBootstrap(")) {
        expect(query).toContain("cacheInputTokens");
        expect(query).toContain("unknownTokenCounts");
      }

      return route.fulfill({
        json: {
          data: bootstrapData(query, {
            ...transcript,
            usageSummary: {
              ...transcript.usageSummary,
              unknownTokenCounts: 2,
              unknownCacheWrites: 3,
            },
          }),
        },
      });
    }

    return route.fulfill({
      json: {
        data: query.includes("status{")
          ? { status: { needsSetup: false, authenticated: true } }
          : query.includes("agents(projectId:")
            ? { project, agents }
            : config,
      },
    });
  });
  await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
  await page.getByRole("button", { name: /^Agentendetails für / }).click();
  const summary = page
    .locator("details")
    .filter({ has: page.locator("summary", { hasText: "Tokens" }) })
    .first();
  await summary.locator(":scope > summary").click();

  await expect(summary).toContainText("75% Cache gelesen (gemeldete Tokens)");
  await expect(summary).toContainText("6.000 Cache-Tokens geschrieben");
  await expect(summary).toContainText(
    "Tokenzahlen geschätzt oder unbekannt: 2 Anfragen",
  );
  await expect(summary).toContainText(
    "Cache-Schreibdaten fehlen für 3 Anfragen",
  );
  await expect(summary).toContainText("Letzte Modellrunden");
  await expect(summary.getByText(/Hauptarbeit · gpt-6.1-sol/)).toBeVisible();
  await expect(summary.locator(":scope > summary")).toContainText("≈");
  await summary.getByText(/Hauptarbeit · gpt-6.1-sol/).click();
  await expect(summary).toContainText("Verbrauch unbekannt");
  await expect(summary).toContainText("5/6 Nachrichten unverändert");
});

test("failed model rounds remain inspectable before any successful usage", async ({
  page,
}) => {
  await page.addInitScript(() => localStorage.setItem("zentrale-locale", "de"));
  await page.routeWebSocket("**/events?*", () => {});
  let diagnostics = 0;
  await page.route("**/graphql", (route) => {
    const { query } = route.request().postDataJSON();
    if (query.includes("inferenceTraces(")) {
      diagnostics++;
      return route.fulfill({
        json: {
          data: {
            inferenceTraces: [
              {
                id: "failed",
                purpose: "main",
                modelId: "gpt-6.1-sol",
                durationMillis: 100,
                outcome: "failed",
                prefixChange: "first_request",
                sharedMessages: 0,
                messageCount: 1,
                estimatedSystemTokens: 100,
                estimatedToolTokens: 200,
                estimatedConversationTokens: 300,
                estimatedRuntimeTokens: 0,
                tokensKnown: false,
                cacheReadKnown: false,
                inputTokens: 0,
                outputTokens: 0,
                cachedTokens: 0,
                attempts: [
                  {
                    number: 1,
                    transport: "http",
                    outcome: "stream_interrupted",
                    durationMillis: 100,
                    sentBytes: 1000,
                    tokensKnown: false,
                  },
                ],
              },
            ],
          },
        },
      });
    }
    if (isTranscriptQuery(query))
      return route.fulfill({
        json: {
          data: bootstrapData(query, {
            ...transcript,
            usageSummary: {
              ...transcript.usageSummary,
              requestCount: 0,
              inputTokens: 0,
              outputTokens: 0,
            },
          }),
        },
      });
    return route.fulfill({
      json: {
        data: query.includes("status{")
          ? { status: { needsSetup: false, authenticated: true } }
          : query.includes("agents(projectId:")
            ? { project, agents }
            : config,
      },
    });
  });
  await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
  await page.getByRole("button", { name: /^Agentendetails für / }).click();
  expect(diagnostics).toBe(0);
  const dialog = page.locator('[data-slot="modal-dialog"]:not([data-exiting])');
  await dialog.getByText("Modellrunden und Diagnose", { exact: true }).click();
  await dialog.getByText(/Hauptarbeit · gpt-6.1-sol/).click();
  await expect(dialog).toContainText("Tokenzahlen nicht gemeldet");
  await expect(dialog).toContainText("Verbrauch unbekannt");
  expect(diagnostics).toBe(1);
});
