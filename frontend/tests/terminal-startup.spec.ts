import { expect, test } from "@playwright/test";
import {
  isTerminalStartupPhase,
  terminalStartupLabels,
} from "../src/terminal-startup";

test("startup labels distinguish server-reported VM boot from terminal setup", () => {
  expect(terminalStartupLabels.starting).not.toContain("VM");
  expect(terminalStartupLabels.checking_environment).not.toContain("gestartet");
  expect(terminalStartupLabels.preparing_terminal).not.toContain("VM");
  expect(terminalStartupLabels.starting_terminal).toBe(
    "Terminal-Verbindung wird aufgebaut …",
  );
  expect(terminalStartupLabels.vm_starting).toBe("VM wird gestartet …");
  for (const phase of Object.keys(terminalStartupLabels)) {
    expect(isTerminalStartupPhase(phase)).toBe(true);
  }
  for (const value of [null, undefined, 42, "unknown", "__proto__"]) {
    expect(isTerminalStartupPhase(value)).toBe(false);
  }
});
