import { expect, test, type WebSocketRoute } from "@playwright/test";
import { installPushFixture } from "./fixtures/push-fixture";
import { bootstrapData, isTranscriptQuery } from "./fixtures/bootstrap-data";
import { mkdir } from "node:fs/promises";

import {
  agent,
  agents,
  config,
  project,
  transcript,
} from "./fixtures/visual-data";
import type { LiveResponse } from "../src/live-responses";

for (const english of [false, true]) {
  test(
    "live generation and compact job controls: " +
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
      let running = true;
      let phase = "model";
      let queryCount = 0;
      let stopAgent = 0;
      let stopJob = 0;
      const messages = [
        {
          ...transcript.messages[0],
          id: "request",
          role: "user",
          content: label(
            "Prüfe die Trainingsdaten.",
            "Check the training data.",
          ),
        },
      ];
      const job = {
        id: "live-training",
        agentId: agent.id,
        machineId: agent.machineId,
        command:
          "python3 train.py --epochs 100 --checkpoint /srv/project/checkpoints/latest.pt",
        status: "running",
        output: "step 1/10\nloss: 0.42\n",
        error: "",
        startedAt: new Date().toISOString(),
      };
      const current = () => ({
        ...agent,
        status: running ? "running" : "idle",
      });
      const response = (value: LiveResponse) =>
        socket!.send(
          JSON.stringify({
            id: 0,
            type: "response",
            agentId: agent.id,
            data: JSON.stringify(value),
          }),
        );
      const resolve = async (query: string) => {
        const send = (data: unknown) =>
          bootstrapData(query, data, {
            agents: agents.map((value) =>
              value.id === agent.id ? current() : value,
            ),
          });
        if (query.includes("status{")) {
          return send({ status: { authenticated: true, needsSetup: false } });
        }

        if (query.includes("job(id:")) {
          return send({ job });
        }

        if (query.includes("stopAgent")) {
          stopAgent++;
          running = false;

          return send({ stopAgent: true });
        }

        if (query.includes("stopJob")) {
          stopJob++;
          if (stopJob === 1) {
            throw new Error(
              label(
                "Job konnte nicht beendet werden. Erneut versuchen.",
                "Could not stop the job. Try again.",
              ),
            );
          }

          job.status = "stopped";

          return send({ stopJob: true });
        }

        if (isTranscriptQuery(query)) {
          return send({
            ...transcript,
            agent: current(),
            agentRunState: { agentId: agent.id, phase, error: "", hostKey: {} },
            messages,
            recentMessages: messages,
            jobs: [job],
            questions: [],
          });
        }

        if (query.includes("agents(projectId:")) {
          return send({
            project,
            agents: agents.map((value) =>
              value.id === agent.id ? current() : value,
            ),
          });
        }

        return send(config);
      };
      const push = await installPushFixture(page, resolve, (connection) => {
        socket = connection;
      });
      const durable = () => push.push();
      await page.route("**/graphql", async (route) => {
        const { query } = route.request().postDataJSON();
        if (isTranscriptQuery(query)) queryCount++;
        try {
          const data = await resolve(query);
          if (query.trimStart().startsWith("mutation")) await push.push();
          await route.fulfill({ json: { data } });
        } catch (error) {
          await route.fulfill({
            json: { errors: [{ message: (error as Error).message }] },
          });
        }
      });
      await page.goto("/#/projects/" + project.id + "?agent=" + agent.id);

      await expect.poll(() => Boolean(socket)).toBe(true);

      const output = page.getByRole("region", {
        name: label("Agentenverlauf", "Agent transcript"),
        exact: true,
      });

      await expect(
        page.getByRole("button", {
          name: label("Agent stoppen", "Stop agent"),
          exact: true,
        }),
      ).toBeVisible();
      await expect(output.locator(".working-indicator")).toBeVisible();

      phase = "recovering_sandbox";
      await durable();
      await expect(output.getByRole("status")).toContainText(
        label("Umgebung wird wiederhergestellt …", "Restoring environment …"),
      );
      phase = "model";
      await durable();
      await expect(output.getByRole("status")).not.toContainText(
        label("Umgebung wird wiederhergestellt …", "Restoring environment …"),
      );

      const draft: LiveResponse = {
        id: "draft-1",
        agentId: agent.id,
        text: "",
        revision: 0,
        snapshot: true,
        status: "running",
        createdAt: new Date().toISOString(),
      };
      response(draft);
      response({ ...draft, snapshot: false, revision: 1, delta: "Live: " });
      const live = output.getByRole("article", {
        name: label("Live-Antwort", "Live response"),
        exact: true,
      });

      await expect(live).toContainText("Live:");

      const before = queryCount;
      let answer = "Live: ";
      for (let index = 2; index <= 30; index++) {
        const text = "token-" + index + " ";
        answer += text;
        response({ ...draft, snapshot: false, revision: index, delta: text });
      }

      await expect(live).toContainText("token-30");
      expect(queryCount).toBe(before);

      // A new socket first resets drafts, then supplies an authoritative snapshot.
      socket!.send(JSON.stringify({ id: 0, type: "response_reset" }));
      response({ ...draft, revision: 30, text: answer });

      await expect(live).toContainText("token-30");
      expect((await live.innerText()).match(/token-2\b/g)).toHaveLength(1);

      response({
        ...draft,
        revision: 32,
        snapshot: false,
        delta: "OUT_OF_ORDER",
      });

      await expect(live).not.toContainText("OUT_OF_ORDER");
      expect(queryCount).toBe(before);

      await page.emulateMedia({ reducedMotion: "reduce" });

      await expect
        .poll(() =>
          output
            .locator(".working-indicator > span")
            .first()
            .evaluate((element) => getComputedStyle(element).animationName),
        )
        .toBe("none");

      await page.emulateMedia({ reducedMotion: "no-preference" });

      const openJob = output.getByRole("button", {
        name: label("Ausgabe öffnen:", "Open output:") + " " + job.command,
      });
      await openJob.click();

      await expect(output).toContainText("step 1/10");
      await expect(page.getByRole("dialog")).toHaveCount(0);
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth - innerWidth,
        ),
      ).toBeLessThanOrEqual(1);

      await mkdir("artifacts/ui", { recursive: true });
      await page.screenshot({
        path: "artifacts/ui/live-" + variant + ".png",
        animations: "disabled",
      });
      const stopJobButton = output.getByRole("button", {
        name: label("Job beenden:", "Stop job:") + " " + job.command,
      });
      await stopJobButton.click();
      await page
        .getByRole("dialog")
        .getByRole("button", {
          name: label("Job beenden", "Stop job"),
          exact: true,
        })
        .click();

      await expect(output).toContainText(
        label("Job konnte nicht beendet werden.", "Could not stop the job."),
      );
      await expect(output).toContainText("step 1/10");

      await stopJobButton.click();
      await page
        .getByRole("dialog")
        .getByRole("button", {
          name: label("Job beenden", "Stop job"),
          exact: true,
        })
        .click();

      await expect(stopJobButton).toHaveCount(0);
      expect(stopJob).toBe(2);

      // Inspecting a job can move the reader away from the end on mobile.
      // Resume following explicitly before testing automatic live scrolling.
      const latest = page.getByRole("button", {
        name: label("Zu neuesten Einträgen ↓", "Latest entries ↓"),
      });
      if (await latest.isVisible()) await latest.click();

      await page.evaluate(() =>
        window.scrollTo(0, document.documentElement.scrollHeight),
      );

      await expect(latest).toHaveCount(0);

      const longText =
        "\n\n" +
        Array.from({ length: 70 }, (_, index) => "Output line " + index).join(
          "\n",
        );
      answer += longText;
      response({ ...draft, snapshot: false, revision: 31, delta: longText });

      await expect(live).toContainText("Output line 69");
      await expect
        .poll(() =>
          page.evaluate(
            () => document.documentElement.scrollHeight - innerHeight - scrollY,
          ),
        )
        .toBeLessThan(5);

      await page.evaluate(() => window.scrollTo(0, 0));

      await expect.poll(() => page.evaluate(() => scrollY)).toBe(0);

      answer += "\nAnother live line";
      response({
        ...draft,
        snapshot: false,
        revision: 32,
        delta: "\nAnother live line",
      });

      await expect(live).toContainText("Another live line");
      expect(await page.evaluate(() => scrollY)).toBe(0);

      await page
        .getByRole("button", {
          name: label("Zu neuesten Einträgen ↓", "Latest entries ↓"),
        })
        .click();

      messages.push({
        ...messages[0],
        id: draft.id,
        role: "assistant",
        content: answer.trim(),
        createdAt: draft.createdAt,
      });
      running = false;
      response({
        ...draft,
        revision: 33,
        status: "completed",
        text: answer.trim(),
      });
      await durable();

      await expect(live).toHaveCount(0);
      await expect(
        output.getByText(answer.trim(), { exact: true }),
      ).toHaveCount(1);
      await expect(output.locator(".working-indicator")).toHaveCount(0);

      running = true;
      await durable();
      const second: LiveResponse = {
        ...draft,
        id: "draft-2",
        revision: 0,
        text: "A partial answer",
        createdAt: new Date().toISOString(),
      };
      response(second);

      await expect(live).toContainText("A partial answer");

      const prompt = page.getByRole("textbox", {
        name: label("Aufgabe oder neue Instruktion", "Task or new instruction"),
      });
      await prompt.fill("A follow-up instruction");

      await expect(
        page.getByRole("button", {
          name: new RegExp(
            "^" + label("Instruktion senden", "Send instruction"),
          ),
        }),
      ).toBeEnabled();

      await prompt.fill("");
      await page
        .getByRole("button", {
          name: label("Agent stoppen", "Stop agent"),
          exact: true,
        })
        .click();

      expect(stopAgent).toBe(1);

      response({ ...second, revision: 1, status: "interrupted" });

      await expect(live).toContainText(
        label("Antwort unterbrochen", "Response interrupted"),
      );
      await expect(prompt).toHaveValue("");
      await expect(
        page.getByRole("button", {
          name: label("Agent stoppen", "Stop agent"),
          exact: true,
        }),
      ).toHaveCount(0);
      expect(errors).toEqual([]);
    },
  );
}
