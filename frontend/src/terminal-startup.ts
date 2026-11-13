// These phases are pushed by the backend at the actual preparation boundaries.
// In particular vm_starting comes from the native runner only on a new boot;
// neither a browser timer nor a generic "starting" message implies a VM boot.
export const terminalStartupLabels = {
  starting: "Terminal-Verbindung wird vorbereitet …",
  selecting_environment: "Arbeitsumgebung wird ausgewählt …",
  connecting: "SSH-Verbindung wird hergestellt …",
  checking_environment: "Arbeitsumgebung wird geprüft …",
  preparing_environment: "Arbeitsumgebung wird vorbereitet …",
  vm_starting: "VM wird gestartet …",
  preparing_sources: "Projektdateien und Zugänge werden vorbereitet …",
  waiting_runtime: "Warte auf die Arbeitsumgebung …",
  preparing_terminal: "Terminal-Aufruf wird vorbereitet …",
  starting_terminal: "Terminal-Verbindung wird aufgebaut …",
} as const;

export type TerminalStartupPhase = keyof typeof terminalStartupLabels;

export function isTerminalStartupPhase(
  value: unknown,
): value is TerminalStartupPhase {
  return (
    typeof value === "string" && Object.hasOwn(terminalStartupLabels, value)
  );
}
