type MonacoLocale = "de" | "en";
const environment = globalThis as typeof globalThis & {
  _VSCODE_NLS_MESSAGES?: string[];
  _VSCODE_NLS_LANGUAGE?: string;
};
let germanMessages: string[] | undefined;

// Monaco reads translations while registering its UI. Load its own versioned
// dictionary before the editor, and retain it for later dialog openings.
export async function loadMonaco(locale: MonacoLocale) {
  if (locale === "de" && !germanMessages) {
    await import("monaco-editor/nls/lang/de");
    germanMessages = environment._VSCODE_NLS_MESSAGES;
  }
  environment._VSCODE_NLS_MESSAGES =
    locale === "de" ? germanMessages : undefined;
  environment._VSCODE_NLS_LANGUAGE = locale;
  return import("./monaco-runtime");
}
