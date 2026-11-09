import { expect, test, type WebSocketRoute } from "@playwright/test";
import { mkdir, writeFile } from "node:fs/promises";
import {
  agent,
  agents,
  config,
  project,
  transcript,
} from "./fixtures/visual-data";
import type { AgentInput } from "../src/generated";

for (const english of [false, true]) {
  test(
    "durable submission, preparation status and Tab queue: " +
      (english ? "mobile-en-dark" : "desktop-de-light"),
    async ({ page }) => {
      const label = (de: string, en: string) => (english ? en : de);
      const variant = english ? "mobile-en-dark" : "desktop-de-light";
      const errors: string[] = [];
      page.on("pageerror", (error) => errors.push(error.message));
      await page.setViewportSize({
        width: english ? 390 : 1440,
        height: english ? 844 : 1000,
      });
      await page.addInitScript(
        ({ english }) => {
          localStorage.setItem("zentrale-locale", english ? "en" : "de");
          localStorage.setItem("zentrale-theme", english ? "dark" : "light");
        },
        { english },
      );
      let socket: WebSocketRoute | undefined;
      let event = 0;
      let phase = "model";
      let preflights = 0;
      let release: (() => void) | undefined;
      const delayed = new Promise<void>((resolve) => {
        release = resolve;
      });
      let loseResponse = true;
      const attempts: Array<{ id: string; text: string; mode: string }> = [];
      const inputs = new Map<string, AgentInput>();
      const run = { ...agent, status: "running" };
      const jobs = [
        {
          ...transcript.jobs[0],
          id: "running-job",
          command: "python3 background.py",
          status: "running",
        },
        {
          ...transcript.jobs[0],
          id: "completed-job",
          command: "python3 completed.py",
          status: "completed",
        },
      ];
      await page.routeWebSocket("**/events?*", (connection) => {
        socket = connection;
      });
      const update = () =>
        socket!.send(
          JSON.stringify({ id: ++event, type: "agent", data: "{}" }),
        );
      await page.route("**/graphql", async (route) => {
        const { query, variables = {} } = route.request().postDataJSON();
        const send = (data: unknown) => route.fulfill({ json: { data } });
        if (query.includes("status{"))
          return send({ status: { authenticated: true, needsSetup: false } });
        if (query.includes("prepareAgent")) {
          preflights++;
          return send({ prepareAgent: { connected: true } });
        }
        if (query.includes("submitInput(")) {
          attempts.push({
            id: variables.id,
            text: variables.text,
            mode: variables.mode,
          });
          if (!inputs.has(variables.id))
            inputs.set(variables.id, {
              ...variables,
              projectId: project.id,
              status: variables.mode === "queue" ? "queued" : "pending",
              createdAt: new Date().toISOString(),
              deliveredAt: "0001-01-01T00:00:00Z",
            });
          if (variables.text === "FIRST_SUBMISSION") await delayed;
          if (variables.text === "LOST_RECEIPT" && loseResponse) {
            loseResponse = false;
            return route.abort("failed");
          }
          return send({ submitInput: inputs.get(variables.id) });
        }
        if (query.includes("cancelInput(")) {
          inputs.delete(variables.id);
          return send({ cancelInput: true });
        }
        if (query.includes("recentMessages"))
          return send({
            ...transcript,
            agent: run,
            messages: [transcript.messages[0]],
            recentMessages: [],
            jobs,
            questions: [],
            agentInputs: [...inputs.values()],
            agentRunState: {
              agentId: agent.id,
              phase,
              error: "",
              hostKey: { publicKey: "", host: "", fingerprint: "" },
            },
          });
        if (query.includes("agents(projectId:"))
          return send({
            project,
            agents: agents.map((item) => (item.id === agent.id ? run : item)),
          });
        return send(config);
      });
      await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
      const input = page.getByRole("textbox", {
        name: label("Aufgabe oder neue Instruktion", "Task or new instruction"),
      });
      await input.fill("FIRST_SUBMISSION");
      await input.press("Enter");
      await expect(input).toHaveValue("");
      await expect(
        page.getByText(label("Wird gesendet …", "Sending …"), { exact: true }),
      ).toBeVisible();
      await input.fill("Next draft survives the acknowledgement");
      phase = "connecting";
      release!();
      await expect(
        page.getByText(
          label(
            "Verbindung zur Maschine wird hergestellt …",
            "Connecting to the machine …",
          ),
          { exact: true },
        ),
      ).toBeVisible();
      await expect(input).toHaveValue(
        "Next draft survives the acknowledgement",
      );
      expect(preflights).toBe(0);
      await input.fill("QUEUE_ONE");
      await input.press("Tab");
      await expect(input).toHaveValue("");
      await expect
        .poll(() =>
          attempts
            .filter((item) => item.text === "QUEUE_ONE")
            .map((item) => item.mode),
        )
        .toEqual(["queue"]);
      const queue = page.getByRole("region", {
        name: label("Nachrichtenwarteschlange", "Message queue"),
      });
      await expect(
        queue.getByText(
          label("Auf dem Server eingereiht", "Queued on the server"),
          { exact: true },
        ),
      ).toBeVisible();
      phase = "sandbox";
      update();
      await expect(
        page.getByText(
          label("Sandbox wird vorbereitet …", "Preparing sandbox …"),
          { exact: true },
        ),
      ).toBeVisible();
      await mkdir("artifacts/ui", { recursive: true });
      await page.screenshot({
        path: `artifacts/ui/input-queue-${variant}.png`,
        animations: "disabled",
      });
      await page.reload();
      await expect(queue.getByText("QUEUE_ONE", { exact: true })).toBeVisible();
      await queue
        .getByRole("button", {
          name: label("Wartende Nachricht bearbeiten", "Edit queued message"),
        })
        .click();
      await expect(input).toHaveValue("QUEUE_ONE");
      await expect(queue.getByText("QUEUE_ONE", { exact: true })).toHaveCount(
        0,
      );
      await input.press("Tab");
      await expect(input).toHaveValue("");
      await expect(
        queue.getByRole("button", {
          name: label("Wartende Nachricht entfernen", "Remove queued message"),
        }),
      ).toBeVisible();
      await queue
        .getByRole("button", {
          name: label("Wartende Nachricht entfernen", "Remove queued message"),
        })
        .click();
      await expect(queue.getByText("QUEUE_ONE", { exact: true })).toHaveCount(
        0,
      );
      await input.focus();
      const beforeEmptyTab = attempts.length;
      await input.press("Tab");
      await expect(input).not.toBeFocused();
      expect(attempts.length).toBe(beforeEmptyTab);
      await input.fill("LOST_RECEIPT");
      await input.press("Enter");
      await expect(
        page.getByText(
          label(
            "Übernahme noch nicht bestätigt. Wird erneut versucht.",
            "Receipt not confirmed yet. Will retry.",
          ),
          { exact: true },
        ),
      ).toBeVisible();
      await page.reload();
      await expect
        .poll(
          () => attempts.filter((item) => item.text === "LOST_RECEIPT").length,
        )
        .toBe(2);
      expect(
        new Set(
          attempts
            .filter((item) => item.text === "LOST_RECEIPT")
            .map((item) => item.id),
        ).size,
      ).toBe(1);
      await expect(
        queue.getByText("LOST_RECEIPT", { exact: true }),
      ).toHaveCount(1);
      await page
        .getByRole("button", {
          name: label("Projektaktionen", "Project actions"),
          exact: true,
        })
        .click();
      await page
        .getByRole("menuitem", {
          name: label("Agentendetails", "Agent details"),
          exact: true,
        })
        .click();
      const details = page.getByRole("dialog");
      await expect(
        details.getByRole("heading", { name: /Freigaben|Approvals/ }),
      ).toHaveCount(0);
      await expect(
        details.getByRole("button", {
          name: label(
            "Ausgabe öffnen: python3 background.py",
            "Open output: python3 background.py",
          ),
        }),
      ).toBeVisible();
      await expect(
        details.getByText("python3 completed.py", { exact: true }),
      ).toHaveCount(0);
      await page.screenshot({
        path: `artifacts/ui/input-queue-${variant}-details.png`,
        animations: "disabled",
      });
      await writeFile(
        `artifacts/ui/input-queue-${variant}.json`,
        JSON.stringify({
          captures: [
            { name: "input-queue", file: `input-queue-${variant}.png` },
            {
              name: "input-queue-details",
              file: `input-queue-${variant}-details.png`,
            },
          ],
        }),
      );
      expect(errors).toEqual([]);
    },
  );
}
