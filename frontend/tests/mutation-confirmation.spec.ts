import { expect, test } from "@playwright/test";
import { mutationConfirmation } from "../src/mutation-confirmation";

test("destructive actions keep their explanation and label together", () => {
  for (const [field, variables, message, actionLabel] of [
    ["stopEditor", {}, "Nicht gespeicherte Änderungen", "Editor beenden"],
    [
      "resetProjectVM",
      { projectId: "project" },
      "Projektdateien bleiben erhalten",
      "Projekt-VM zurücksetzen",
    ],
    ["stopJob", {}, "Diesen Job", "Job beenden"],
    ["revokeRepositoryKey", {}, "Repo-Zugriff", "Zugriff widerrufen"],
    ["disconnectGitConnection", {}, "Zugang", "Trennen"],
    ["disconnectHuggingFace", {}, "Zugang", "Trennen"],
    ["disconnectProvider", {}, "Zugang", "Trennen"],
    ["trustHostKey", { trust: "distrust" }, "Vertrauen", "Vertrauen entziehen"],
    ["deleteResource", { kind: "agent" }, "Agentenbaum", "Entfernen"],
    [
      "deleteResource",
      { kind: "project" },
      "Repository-Dateien bleiben erhalten",
      "Entfernen",
    ],
    [
      "deleteResource",
      { kind: "machine" },
      "abhängige Verbindungen",
      "Entfernen",
    ],
    [
      "deleteResource",
      { kind: "pool" },
      "Konversationen und Dateien bleiben erhalten",
      "Entfernen",
    ],
    ["deleteSSHKey", {}, "Dieses Element", "Entfernen"],
  ] as const) {
    const confirmation = mutationConfirmation(`mutation{${field}}`, variables);
    expect(confirmation?.message).toContain(message);
    expect(confirmation?.actionLabel).toBe(actionLabel);
  }
});

test("ordinary saves and granting trust never request destructive confirmation", () => {
  expect(mutationConfirmation("mutation{saveProject}", {})).toBeUndefined();
  expect(
    mutationConfirmation("mutation{trustHostKey}", { trust: "trust" }),
  ).toBeUndefined();
  expect(mutationConfirmation("query{stopJobStatus}", {})).toBeUndefined();
});
