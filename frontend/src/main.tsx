import React from "react";
import { I18nProvider } from "react-aria-components";
import ReactDOM from "react-dom/client";

import App from "./App";
import { ConfirmationProvider } from "./confirmation";
import { Notifications } from "./notifications";
import { PreferencesProvider, useI18n } from "./preferences";

import { API, Services } from "./api";
import "./index.css";

const api = new API();

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <PreferencesProvider>
      <ApplicationProviders>
        <ConfirmationProvider api={api}>
          <Services api={api}>
            <Notifications />
            <App />
          </Services>
        </ConfirmationProvider>
      </ApplicationProviders>
    </PreferencesProvider>
  </React.StrictMode>,
);

// The main app uses hash routes. A forwarded preview has its own scoped worker
// and must not register a root worker on the shared HTTPS origin.
const appEntry =
  location.pathname === "/" || location.pathname === "/index.html";
if (
  appEntry &&
  window.top === window &&
  window.isSecureContext &&
  "serviceWorker" in navigator
) {
  void navigator.serviceWorker.register("/pwa-sw.js").catch((error) => {
    console.warn("Zentrale could not register its service worker", error);
  });
}

function ApplicationProviders({ children }: { children: React.ReactNode }) {
  const { locale } = useI18n();

  return <I18nProvider locale={locale}>{children}</I18nProvider>;
}
