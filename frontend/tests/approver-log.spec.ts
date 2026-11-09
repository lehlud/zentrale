import { expect, test, type WebSocketRoute } from "@playwright/test";
import { mkdir } from "node:fs/promises";

import { agents, config, project, transcript } from "./fixtures/visual-data";

for (const english of [false, true]) {
  test(`approver groups actions and shows recorded permissions: ${english ? "mobile-en-dark" : "desktop-de-light"}`, async ({
    page,
  }) => {
    page.setDefaultTimeout(10000);
    await page.setViewportSize({ width: english ? 390 : 1440, height: 950 });
    await page.addInitScript(
      ({ english }) => {
        localStorage.setItem("zentrale-locale", english ? "en" : "de");
        localStorage.setItem("zentrale-theme", english ? "dark" : "light");
      },
      { english },
    );
    const label = (de: string, en: string) => (english ? en : de);
    const approver = { ...agents[1], status: "running" };
    const message = (
      id: string,
      role: string,
      content: unknown,
      time: number,
    ) => ({
      id,
      agentId: approver.id,
      role,
      content: typeof content === "string" ? content : JSON.stringify(content),
      wire: "",
      callId: "",
      toolName: "",
      change: null,
      createdAt: `2026-11-06T12:${String(time).padStart(2, "0")}:00Z`,
    });
    const messages = [
      message(
        "request1",
        "event",
        {
          action: "exec",
          arguments: { command: "python3 -m unittest -v" },
          network: false,
          project: { id: "DO_NOT_SHOW", name: project.name },
          userInstructions: [{ content: "Run tests" }],
        },
        0,
      ),
      message(
        "decision1",
        "assistant",
        { allowed: true, reason: "", blanket: false },
        1,
      ),
      message(
        "request2",
        "event",
        {
          action: "exec",
          arguments: { command: "rm protected.txt" },
          network: false,
        },
        2,
      ),
      message(
        "decision2",
        "assistant",
        {
          allowed: false,
          reason: "Diese Datei wird weiterhin benötigt.",
          blanket: false,
        },
        3,
      ),
      message(
        "request3",
        "event",
        {
          action: "packages_install",
          arguments: { packages: ["gcc-gfortran"] },
          network: true,
        },
        4,
      ),
    ];
    const approvals = [
      {
        id: "one",
        tool: "exec",
        arguments: JSON.stringify({ command: "python3 -m unittest -v" }),
        allowed: true,
        reason: "",
        blanket: false,
        createdAt: "2026-11-06T12:01:01Z",
      },
      {
        id: "two",
        tool: "exec",
        arguments: JSON.stringify({ command: "rm protected.txt" }),
        allowed: false,
        reason: "Diese Datei wird weiterhin benötigt.",
        blanket: false,
        createdAt: "2026-11-06T12:03:01Z",
      },
    ];
    let socket: WebSocketRoute | undefined;
    let event = 0;
    await page.routeWebSocket("**/events?*", (connection) => {
      socket = connection;
    });
    await page.route("**/graphql", (route) => {
      const { query } = route.request().postDataJSON();
      const data = query.includes("status{")
        ? { status: { needsSetup: false, authenticated: true } }
        : query.includes("recentMessages")
          ? {
              ...transcript,
              agent: approver,
              messages,
              recentMessages: [],
              questions: [],
              jobs: [],
            }
          : query.startsWith("query($id:String!){approvals")
            ? { approvals }
            : query.includes("agents(projectId:")
              ? { project, agents: [agents[0], approver] }
              : config;

      return route.fulfill({ json: { data } });
    });
    await page.goto(`/#/projects/${project.id}?agent=${approver.id}`);
    await page
      .getByRole("button", {
        name: new RegExp("^" + label("Agent wechseln:", "Switch agent:")),
      })
      .click();
    const selection = page.getByRole("menu", {
      name: label("Agent wechseln", "Switch agent"),
    });

    await expect(
      selection.getByRole("menuitemradio", { name: /Auto-Approver/ }),
    ).toHaveCount(0);
    await expect(
      selection.getByRole("menuitemradio", {
        name: new RegExp(agents[0].name),
      }),
    ).toBeChecked();

    await page.keyboard.press("Escape");
    const list = page.getByRole("list", {
      name: label("Freigabeprüfungen", "Approval reviews"),
    });
    const rows = list.locator(":scope > li");

    await expect(rows).toHaveCount(3);
    await expect(rows.nth(0)).toContainText("python3 -m unittest -v");
    await expect(rows.nth(0)).toContainText(label("Genehmigt", "Approved"));
    await expect(rows.nth(1)).toContainText("rm protected.txt");
    await expect(rows.nth(1)).toContainText(label("Abgelehnt", "Denied"));
    await expect(rows.nth(1)).toContainText(
      "Diese Datei wird weiterhin benötigt.",
    );
    await expect(rows.nth(2)).toContainText(
      label("Wird geprüft", "Under review"),
    );
    await expect(
      page.getByRole("textbox", {
        name: label("Aufgabe oder neue Instruktion", "Task or new instruction"),
      }),
    ).toHaveCount(0);
    await expect(list.getByText("DO_NOT_SHOW")).toHaveCount(0);

    // A model proposal must not appear as a permission if it was not recorded.
    messages.push(
      message(
        "decision3",
        "assistant",
        { allowed: true, reason: "", blanket: false },
        5,
      ),
    );
    approver.status = "idle";

    await expect.poll(() => Boolean(socket)).toBe(true);

    socket!.send(JSON.stringify({ id: ++event }));

    await expect(rows.nth(2)).toContainText(
      label("Keine Freigabe erfasst", "No approval recorded"),
    );

    approvals.push({
      id: "three",
      tool: "packages_install",
      arguments: JSON.stringify({ packages: ["gcc-gfortran"] }),
      allowed: true,
      reason: "",
      blanket: false,
      createdAt: "2026-11-06T12:05:01Z",
    });
    socket!.send(JSON.stringify({ id: ++event }));

    await expect(rows.nth(2)).toContainText(label("Genehmigt", "Approved"));

    await rows
      .nth(2)
      .getByRole("button", {
        name: label("Anfrage ansehen", "View request"),
        exact: true,
      })
      .click();

    await expect(
      rows.nth(2).getByText("gcc-gfortran", { exact: true }).last(),
    ).toBeVisible();
    await expect(
      rows
        .nth(2)
        .getByText(label("Netzwerkzugriff", "Network access"), { exact: true }),
    ).toBeVisible();
    expect(
      (await list.locator("pre:visible").allTextContents()).join("\n"),
    ).not.toContain('"arguments"');

    await mkdir("artifacts/ui", { recursive: true });
    await page.screenshot({
      path: `artifacts/ui/approver-${english ? "mobile" : "desktop"}.png`,
      animations: "disabled",
    });
  });
}
