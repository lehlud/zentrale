import { Label, ListBox, Select, Switch } from "@heroui/react";
import type { ReactNode } from "react";
import { createContext, useContext, useEffect, useState } from "react";
import { english, germanErrors } from "./messages";

type Locale = "de" | "en";
type Theme = "light" | "dark";
type Preferences = {
  locale: Locale;
  theme: Theme;
  setLocale: (locale: Locale) => void;
  setTheme: (theme: Theme) => void;
  showDiagnostics: boolean;
  setShowDiagnostics: (show: boolean) => void;
};
const PreferencesContext = createContext<Preferences | null>(null);

function savedPreference(key: string, fallback: string) {
  try {
    return localStorage.getItem(key) ?? fallback;
  } catch {
    return fallback;
  }
}

export function PreferencesProvider({ children }: { children: ReactNode }) {
  const [locale, setLocale] = useState<Locale>(() =>
    savedPreference("zentrale-locale", "de") === "en" ? "en" : "de",
  );
  const [theme, setTheme] = useState<Theme>(() =>
    savedPreference(
      "zentrale-theme",
      matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light",
    ) === "light"
      ? "light"
      : "dark",
  );
  const [showDiagnostics, setShowDiagnostics] = useState(
    () => savedPreference("zentrale-log-diagnostics", "false") === "true",
  );
  useEffect(() => {
    document.documentElement.lang = locale;
    document.documentElement.dataset.theme = theme;
    document.documentElement.classList.toggle("dark", theme === "dark");
    document.documentElement.classList.toggle("light", theme === "light");
    try {
      localStorage.setItem("zentrale-locale", locale);
      localStorage.setItem("zentrale-theme", theme);
      localStorage.setItem("zentrale-log-diagnostics", String(showDiagnostics));
    } catch {
      /* Storage may be disabled by browser policy. */
    }
  }, [locale, theme, showDiagnostics]);
  return (
    <PreferencesContext.Provider
      value={{
        locale,
        theme,
        setLocale,
        setTheme,
        showDiagnostics,
        setShowDiagnostics,
      }}
    >
      {children}
    </PreferencesContext.Provider>
  );
}

export function LogDiagnosticsControl() {
  const { t, showDiagnostics, setShowDiagnostics } = useI18n();
  return (
    <Switch
      isSelected={showDiagnostics}
      onChange={setShowDiagnostics}
      className="mt-6"
    >
      <Switch.Content className="min-h-11">
        <Switch.Control>
          <Switch.Thumb />
        </Switch.Control>
        <Label>{t("Diagnosedetails im Verlauf")}</Label>
      </Switch.Content>
    </Switch>
  );
}

export function useI18n() {
  const preferences = useContext(PreferencesContext);
  if (!preferences) throw new Error("Preferences provider missing");
  const { locale } = preferences;
  const t = (text: string, values: Record<string, string | number> = {}) => {
    let translated =
      locale === "en" ? (english[text] ?? text) : (germanErrors[text] ?? text);
    const modelFailure = "provider did not complete the response";
    if (
      locale === "de" &&
      text.startsWith(modelFailure) &&
      !germanErrors[text]
    ) {
      translated = `Die Modellanfrage ist fehlgeschlagen${text.slice(modelFailure.length)}`;
    }
    const gitFailure = /^Git provider returned HTTP (\d+):\s*(.*)$/.exec(text);
    if (locale === "de" && gitFailure) {
      const status = Number(gitFailure[1]);
      const recovery =
        status === 401
          ? "Die Git-Anmeldung ist nicht mehr gültig. Das Konto erneut verbinden."
          : status === 403
            ? "Der Git-Dienst verweigert den Zugriff. Konto- und Organisationsberechtigungen prüfen."
            : status === 429
              ? "Der Git-Dienst begrenzt gerade die Anfragen. Kurz warten und erneut versuchen."
              : "Die Anfrage an den Git-Dienst ist fehlgeschlagen. Erneut versuchen.";
      translated = `${recovery} (HTTP ${status}) ${gitFailure[2]}`;
    }
    for (const [key, value] of Object.entries(values))
      translated = translated.replaceAll(`{${key}}`, String(value));
    return translated;
  };
  return {
    ...preferences,
    t,
    numberLocale: locale === "de" ? "de-DE" : "en-US",
  };
}

export function PreferenceControls() {
  const { locale, theme, setLocale, setTheme, t } = useI18n();
  return (
    <div className="grid grid-cols-2 gap-2">
      <Select
        className="min-w-0 gap-1.5"
        value={locale}
        onChange={(selection) => setLocale(selection === "en" ? "en" : "de")}
      >
        <Label>{t("Sprache")}</Label>
        <Select.Trigger className="min-h-11">
          <Select.Value>
            {({ selectedText, isPlaceholder }) =>
              isPlaceholder ? t("Auswählen") : selectedText
            }
          </Select.Value>
          <Select.Indicator />
        </Select.Trigger>
        <Select.Popover>
          <ListBox>
            <ListBox.Item id="de" textValue="Deutsch">
              Deutsch
              <ListBox.ItemIndicator />
            </ListBox.Item>
            <ListBox.Item id="en" textValue="English">
              English
              <ListBox.ItemIndicator />
            </ListBox.Item>
          </ListBox>
        </Select.Popover>
      </Select>
      <Select
        className="min-w-0 gap-1.5"
        value={theme}
        onChange={(selection) =>
          setTheme(selection === "light" ? "light" : "dark")
        }
      >
        <Label>{t("Design")}</Label>
        <Select.Trigger className="min-h-11">
          <Select.Value>
            {({ selectedText, isPlaceholder }) =>
              isPlaceholder ? t("Auswählen") : selectedText
            }
          </Select.Value>
          <Select.Indicator />
        </Select.Trigger>
        <Select.Popover>
          <ListBox>
            <ListBox.Item id="light" textValue={t("Hell")}>
              {t("Hell")}
              <ListBox.ItemIndicator />
            </ListBox.Item>
            <ListBox.Item id="dark" textValue={t("Dunkel")}>
              {t("Dunkel")}
              <ListBox.ItemIndicator />
            </ListBox.Item>
          </ListBox>
        </Select.Popover>
      </Select>
    </div>
  );
}
