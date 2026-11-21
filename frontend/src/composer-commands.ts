export type ComposerCommandContext = {
  side: (question: string) => void;
  teamwork: () => void;
  resume: () => Promise<unknown>;
  stop: () => Promise<unknown>;
};

type ComposerCommand = {
  name: string;
  description: string;
  execute: (
    context: ComposerCommandContext,
    args: string,
  ) => boolean | Promise<boolean>;
};

// Add commands here: discovery, completion and dispatch use this one registry.
// Returning false preserves the complete request for the main agent.
export const composerCommands: readonly ComposerCommand[] = [
  {
    name: "side",
    description: "Nebenfrage zum aktuellen Verlauf stellen",
    execute: (context, args) => {
      context.side(args);
      return true;
    },
  },
  {
    name: "teamwork",
    description: "Teamwork-Board öffnen; mit Aufgabe Teamwork beauftragen",
    execute: (context, args) => {
      if (args) return false;

      context.teamwork();
      return true;
    },
  },
  {
    name: "continue",
    description: "An der aktuellen Aufgabe weiterarbeiten",
    execute: async (context, args) => {
      if (args) return false;

      await context.resume();
      return true;
    },
  },
  {
    name: "stop",
    description: "Den aktuellen Agenten stoppen",
    execute: async (context, args) => {
      if (args) return false;

      await context.stop();
      return true;
    },
  },
];

export function composerCommand(text: string) {
  const match = /^\/([a-z]+)(?:\s+([\s\S]*))?$/.exec(text.trim());
  if (!match) return;

  const command = composerCommands.find((entry) => entry.name === match[1]);

  return command ? { command, args: match[2]?.trim() ?? "" } : undefined;
}

export function commandSuggestions(text: string) {
  const match = /^\/([a-z]*)$/.exec(text);
  if (!match) return [];

  return composerCommands.filter((command) =>
    command.name.startsWith(match[1]),
  );
}
