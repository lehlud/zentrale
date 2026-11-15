type Confirmation = { message: string; actionLabel: string };

// Keep each destructive action's explanation beside its button label. The API
// owns prompting/deduplication; this module only describes the user decision.
export function mutationConfirmation(
  query: string,
  variables: Record<string, unknown>,
): Confirmation | undefined {
  if (/\bstopEditor\b/.test(query)) {
    return {
      message:
        "Editor wirklich beenden? Nicht gespeicherte Änderungen können verloren gehen.",
      actionLabel: "Editor beenden",
    };
  }

  if (/\bstopJob\b/.test(query)) {
    return {
      message: "Diesen Job wirklich beenden?",
      actionLabel: "Job beenden",
    };
  }

  if (/\brevokeRepositoryKey\b/.test(query)) {
    return {
      message:
        "Repo-Zugriff beim Anbieter widerrufen und den Schlüssel dauerhaft löschen?",
      actionLabel: "Zugriff widerrufen",
    };
  }

  if (/\b(disconnectProvider|disconnectGitConnection)\b/.test(query)) {
    return { message: "Zugang wirklich trennen?", actionLabel: "Trennen" };
  }

  if (/\btrustHostKey\b/.test(query) && variables.trust === "distrust") {
    return {
      message: "Vertrauen wirklich entziehen?",
      actionLabel: "Vertrauen entziehen",
    };
  }

  if (/\b(deleteResource|deleteSSHKey)\b/.test(query)) {
    if (variables.kind === "agent") {
      return {
        message: "Agentenbaum und zugehörigen Verlauf dauerhaft entfernen?",
        actionLabel: "Entfernen",
      };
    }

    if (variables.kind === "project") {
      return {
        message:
          "Projekt mit allen Agenten, Verläufen und Quellen dauerhaft entfernen? Repository-Dateien bleiben erhalten.",
        actionLabel: "Entfernen",
      };
    }

    return {
      message: "Dieses Element dauerhaft entfernen?",
      actionLabel: "Entfernen",
    };
  }
}
