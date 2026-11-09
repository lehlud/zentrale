import { expect, test, type WebSocketRoute } from "@playwright/test";
import { mkdir, writeFile } from "node:fs/promises";

import {
  agent,
  agents,
  config,
  project,
  transcript,
} from "./fixtures/visual-data";
import type { Goal, GoalReview, Message } from "../src/generated";

for (const english of [false, true])
  test(
    "persistent goals, independent review and user controls: " +
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
      let phase = "goal_review";
      const main = { ...agent, status: "running", error: "" };
      let goal: Goal = {
        id: "PRIVATE_GOAL_ID",
        agentId: agent.id,
        objective: label(
          "Benchmark erstellen und die Ergebnisse prüfen.",
          "Create a benchmark and verify its results.",
        ),
        origin: "agent",
        status: "active",
        requestedStatus: "complete",
        requestReason: "",
        sourceInputIds: ["PRIVATE_INPUT_ID"],
        revision: 1,
        continuations: 0,
        blockedAttempts: 0,
        blocker: "",
        reason: "",
        evidence: "",
        createdAt: new Date().toISOString(),
        updatedAt: new Date().toISOString(),
      };
      const messages: Message[] = [
        {
          activities: [],
          callId: "",
          change: null,
          changes: [],
          ...transcript.messages[0],
          toolName: transcript.messages[0].toolName ?? "",
          content: goal.objective,
        },
        {
          activities: [],
          callId: "",
          change: null,
          changes: [],
          ...transcript.messages[1],
          toolName: transcript.messages[1].toolName ?? "",
          id: "candidate",
          content: label(
            "Der Benchmark ist fertig.",
            "The benchmark is finished.",
          ),
          wire: "",
        },
      ];
      const reviewMessages: Message[] = [];
      const reviews: GoalReview[] = [];
      let pauses = 0,
        resumes = 0,
        cancellations = 0;
      const verdict = (
        status: string,
        reason: string,
        evidence: string,
        nextStep = "",
      ) => {
        const id = "goal-review-" + reviews.length;
        reviews.push({
          id,
          agentId: agent.id,
          goalId: goal.id,
          approverId: agent.approverId,
          messageId: "candidate",
          status,
          reason,
          evidence,
          nextStep,
          blocker: status === "blocked" ? "missing-credential" : "",
          createdAt: new Date().toISOString(),
        });
        reviewMessages.push(
          {
            ...messages[0],
            id,
            agentId: agent.approverId,
            role: "event",
            content: JSON.stringify({
              action: "goal_completion",
              goal: { objective: goal.objective },
              arguments: { goalId: goal.id },
            }),
          },
          {
            ...messages[1],
            id: id + "-decision",
            agentId: agent.approverId,
            role: "assistant",
            content:
              "```json\n" +
              JSON.stringify({ status, reason, evidence, nextStep }) +
              "\n```",
          },
        );
      };
      await page.routeWebSocket("**/events?*", (connection) => {
        socket = connection;
      });
      const update = () =>
        socket!.send(JSON.stringify({ id: ++event, type: "goal", data: "{}" }));
      await page.route("**/graphql", async (route) => {
        const { query, variables = {} } = route.request().postDataJSON();
        const send = (data: unknown) => route.fulfill({ json: { data } });
        if (query.includes("status{")) {
          return send({ status: { authenticated: true, needsSetup: false } });
        }

        if (query.includes("stopAgent(")) {
          pauses++;
          main.status = "stopped";
          goal.status = "paused";
          goal.reason = label("Vom Nutzer pausiert", "Paused by the user");
          phase = "stopped";

          return send({ stopAgent: true });
        }

        if (query.includes("resumeInputs(")) {
          resumes++;
          main.status = "running";
          main.error = "";
          goal.status = "active";
          goal.blockedAttempts = 0;
          phase = "model";

          return send({ resumeInputs: true });
        }

        if (query.includes("cancelGoal(")) {
          cancellations++;
          goal.status = "cancelled";
          goal.reason = label("Vom Nutzer verworfen", "Cancelled by the user");

          return send({ cancelGoal: true });
        }

        if (query.includes("recentMessages")) {
          const approver = variables.id === agent.approverId;
          return send({
            ...transcript,
            agent: approver ? { ...agents[1], status: "idle" } : main,
            messages: approver ? reviewMessages : messages,
            recentMessages: [],
            questions: [],
            jobs: [],
            goal: approver ? null : goal,
            agentInputs: [],
            agentRunState: {
              agentId: variables.id,
              phase,
              error: "",
              hostKey: { publicKey: "", host: "", fingerprint: "" },
            },
          });
        }

        if (query.includes("approvals(agentId:")) {
          return send({ approvals: [], goalReviews: reviews });
        }

        if (query.includes("agents(projectId:")) {
          return send({
            project,
            agents: agents.map((item) => (item.id === agent.id ? main : item)),
          });
        }

        return send(config);
      });
      const captures: { name: string; file: string }[] = [];

      async function capture(name: string) {
        await mkdir("artifacts/ui", { recursive: true });
        const file = "goals-" + variant + "-" + name + ".png";
        await page.screenshot({
          path: "artifacts/ui/" + file,
          animations: "disabled",
        });
        captures.push({ name, file });
      }
      await page.goto("/#/projects/" + project.id + "?agent=" + agent.id);
      const panel = page.getByRole("region", {
        name: label("Zielstatus", "Goal status"),
        exact: true,
      });

      await expect(panel).toContainText(
        label("Abschluss wird geprüft", "Completion under review"),
      );
      await expect(
        page.getByText(
          label(
            "Auto-Approver prüft den Zielabschluss …",
            "Auto-approver is reviewing goal completion …",
          ),
          { exact: true },
        ),
      ).toBeVisible();
      await expect(
        panel.getByText(label("Ziel erreicht", "Goal achieved"), {
          exact: true,
        }),
      ).toHaveCount(0);

      await capture("reviewing");
      const missing = label(
        "Die CSV-Ausgabe wurde noch nicht geprüft.",
        "The CSV output has not been verified.",
      );
      const next = label(
        "CSV erzeugen und alle Messpunkte prüfen.",
        "Generate the CSV and verify all measurements.",
      );
      verdict("continue", missing, "", next);
      goal.reason = missing;
      goal.continuations = 1;
      goal.requestedStatus = "";
      phase = "tools";
      messages.push({
        ...messages[0],
        id: "feedback",
        role: "event",
        content: JSON.stringify({
          kind: "goal_feedback",
          goalId: goal.id,
          reason: missing,
          nextStep: next,
          notice: "PRIVATE_INTERNAL_NOTICE",
        }),
      });
      update();

      await expect(
        page.getByText(
          label(
            "Der Auto-Approver hat noch offene Arbeit festgestellt.",
            "The auto-approver identified remaining work.",
          ),
          { exact: true },
        ),
      ).toBeVisible();
      await expect(panel).toContainText(label("Aktiv", "Active"));
      await expect(
        page.getByText("PRIVATE_INTERNAL_NOTICE", { exact: false }),
      ).toHaveCount(0);

      await panel.getByRole("button").first().click();
      await panel
        .getByRole("button", {
          name: label("Ziel pausieren", "Pause goal"),
          exact: true,
        })
        .click();

      await expect(panel).toContainText(label("Pausiert", "Paused"));
      expect(pauses).toBe(1);

      await capture("paused");
      await panel
        .getByRole("button", {
          name: label("Ziel fortsetzen", "Resume goal"),
          exact: true,
        })
        .click();

      await expect(panel).toContainText(label("Aktiv", "Active"));
      expect(resumes).toBe(1);

      const blocked = label(
        "Ohne den Git-Zugang kann das Ergebnis nicht veröffentlicht werden.",
        "The result cannot be published without Git access.",
      );
      verdict(
        "blocked",
        blocked,
        label(
          "Die Git-Verbindung ist nicht authentifiziert.",
          "The Git connection is not authenticated.",
        ),
      );
      goal.status = "blocked";
      goal.reason = blocked;
      goal.blockedAttempts = 3;
      main.status = "blocked";
      main.error = blocked;
      phase = "blocked";
      update();

      await expect(panel).toContainText(label("Blockiert", "Blocked"));
      await expect(
        panel.getByText(blocked, { exact: true }).filter({ visible: true }),
      ).toHaveCount(1);

      await capture("blocked");
      await panel
        .getByRole("button", {
          name: label("Ziel fortsetzen", "Resume goal"),
          exact: true,
        })
        .click();

      expect(goal.blockedAttempts).toBe(0);

      const evidence = label(
        "Alle 30 CSV-Messpunkte und das erzeugte SVG wurden geprüft.",
        "All 30 CSV measurements and the generated SVG were verified.",
      );
      verdict(
        "complete",
        label("Alle Anforderungen erfüllt.", "All requirements met."),
        evidence,
      );
      goal.status = "complete";
      goal.reason = label(
        "Alle Anforderungen erfüllt.",
        "All requirements met.",
      );
      goal.evidence = evidence;
      main.status = "idle";
      phase = "idle";
      update();

      await expect(panel).toContainText(
        label("Ziel erreicht", "Goal achieved"),
      );
      await expect(panel.getByText(evidence, { exact: true })).toBeVisible();
      await expect(
        panel.getByRole("button", {
          name: label("Ziel fortsetzen", "Resume goal"),
          exact: true,
        }),
      ).toHaveCount(0);
      await expect(panel).not.toContainText("PRIVATE_");

      await capture("complete");
      await page
        .getByRole("button", {
          name: label("Projektaktionen", "Project actions"),
          exact: true,
        })
        .click();
      await page
        .getByRole("menuitem", { name: "Auto-Approver ↗", exact: true })
        .click();
      const log = page.getByRole("list", {
        name: label("Freigabeprüfungen", "Approval reviews"),
        exact: true,
      });

      await expect(log.locator(":scope > li")).toHaveCount(3);
      await expect(log).toContainText(
        label("Weitere Arbeit nötig", "More work required"),
      );
      await expect(log).toContainText(label("Ziel erreicht", "Goal achieved"));
      await expect(log).toContainText(evidence);
      await expect(
        log.getByRole("button", {
          name: label("Prüfschritte", "Inspection steps"),
          exact: true,
        }),
      ).toHaveCount(0);
      await expect(
        log.getByText(label("Genehmigt", "Approved"), { exact: true }),
      ).toHaveCount(0);

      await capture("audit");
      await page.goto("/#/projects/" + project.id + "?agent=" + agent.id);
      goal = {
        ...goal,
        id: "PRIVATE_NEXT_GOAL",
        status: "active",
        objective: label("Nächste Aufgabe", "Next task"),
        reason: "",
        evidence: "",
      };
      main.status = "running";
      phase = "model";
      update();

      await expect(panel).toContainText(goal.objective);

      await panel.getByRole("button").first().click();
      await panel
        .getByRole("button", {
          name: label("Ziel pausieren", "Pause goal"),
          exact: true,
        })
        .click();
      await panel
        .getByRole("button", {
          name: label("Ziel verwerfen", "Discard goal"),
          exact: true,
        })
        .click();

      await expect(panel).toContainText(label("Verworfen", "Discarded"));
      expect(cancellations).toBe(1);
      expect(errors).toEqual([]);

      await writeFile(
        "artifacts/ui/goals-" + variant + ".json",
        JSON.stringify({ captures }),
      );
    },
  );
