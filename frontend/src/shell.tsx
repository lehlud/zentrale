import { Button, Drawer, Dropdown, Label } from "@heroui/react";
import type { CSSProperties, ReactNode } from "react";
import { createContext, useContext, useEffect, useState } from "react";

import { useAPI } from "./api";
import { useRoute } from "./navigation";
import {
  LogDiagnosticsControl,
  PreferenceControls,
  useI18n,
} from "./preferences";
import { Icon } from "./icons";
import { useMediaQuery } from "./media";
import { NewConversation } from "./new-conversation";
import { SidebarProjects } from "./sidebar-projects";
import { Inspect } from "./ui";

type SidebarState = {
  compact: boolean;
  collapsed: boolean;
  menuOpen: boolean;
  toggle: () => void;
  newConversation: (projectId?: string) => void;
  preferences: () => void;
};

const SidebarContext = createContext<SidebarState | null>(null);

export function useSidebar() {
  return useContext(SidebarContext);
}

export function SidebarToggle({ iconOnly = false }: { iconOnly?: boolean }) {
  const state = useSidebar();
  const { t } = useI18n();
  if (!state || (!state.compact && !state.collapsed)) {
    return null;
  }

  return (
    <Button
      id="sidebar-toggle"
      variant="ghost"
      isIconOnly={iconOnly}
      aria-label={t(state.compact ? "Menü öffnen" : "Seitenleiste öffnen")}
      aria-expanded={state.compact ? state.menuOpen : false}
      aria-controls="primary-navigation"
      className="min-h-11 min-w-11 shrink-0"
      onPress={state.toggle}
    >
      <Icon name="sidebar" className="size-5" />
      {!iconOnly && <span>{t(state.compact ? "Menü" : "Seitenleiste")}</span>}
    </Button>
  );
}

const administration = [
  { id: "settings", label: "Allgemeine Einstellungen" },
  { id: "providers", label: "Provider und Modelle" },
  { id: "machines", label: "Maschinen" },
  { id: "keys", label: "Schlüssel" },
] as const;

function Navigation({ onNavigate }: { onNavigate?: () => void }) {
  const { t } = useI18n();
  const { page, projectId } = useRoute();
  const api = useAPI();
  const sidebar = useSidebar()!;

  return (
    <nav
      aria-label={t("Hauptnavigation")}
      className="flex min-h-full flex-col gap-3"
    >
      {!sidebar.compact && (
        <div className="flex justify-end">
          <Button
            id="sidebar-toggle"
            isIconOnly
            variant="ghost"
            aria-label={t("Seitenleiste schließen")}
            className="min-h-11 min-w-11 text-muted"
            onPress={sidebar.toggle}
          >
            <Icon name="sidebar" className="size-5" />
          </Button>
        </div>
      )}
      <Button
        variant="ghost"
        className="min-h-11 w-full justify-start gap-3 px-3"
        onPress={() => {
          sidebar.newConversation(page === "projects" ? projectId : "");
          onNavigate?.();
        }}
      >
        <Icon name="compose" className="size-5" />
        {t("Neue Unterhaltung")}
      </Button>
      <SidebarProjects onNavigate={onNavigate} />
      <div className="mt-auto pt-3">
        <Dropdown>
          <Button
            variant="ghost"
            aria-label={t("Einstellungen öffnen")}
            className="min-h-11 w-full justify-start gap-3 px-3 text-sm"
          >
            <Icon name="settings" className="size-5" />
            <span className="flex-1 text-left">{t("Einstellungen")}</span>
            <Icon name="more" />
          </Button>
          <Dropdown.Popover placement="top start">
            <Dropdown.Menu aria-label={t("Verwaltung")}>
              {administration.map((item) => (
                <Dropdown.Item
                  id={item.id}
                  key={item.id}
                  href={`#/${item.id}`}
                  onAction={onNavigate}
                  className="min-h-11"
                >
                  <Label>{t(item.label)}</Label>
                </Dropdown.Item>
              ))}
              <Dropdown.Item
                id="appearance"
                onAction={() => {
                  sidebar.preferences();
                }}
                className="min-h-11"
              >
                <Label>{t("Darstellung und Sprache")}</Label>
              </Dropdown.Item>
              <Dropdown.Item
                id="logout"
                onAction={() =>
                  void api.mutate("mutation{logout}").catch(() => api.refresh())
                }
                className="min-h-11"
              >
                <Label>{t("Abmelden")}</Label>
              </Dropdown.Item>
            </Dropdown.Menu>
          </Dropdown.Popover>
        </Dropdown>
      </div>
    </nav>
  );
}

function savedCollapsed() {
  try {
    return localStorage.getItem("zentrale-sidebar-collapsed") === "true";
  } catch {
    return false;
  }
}

export function ApplicationShell({ children }: { children: ReactNode }) {
  const { t } = useI18n();
  const compact = useMediaQuery("(max-width: 1023px)");
  const [collapsed, setCollapsed] = useState(savedCollapsed);
  const [menuOpen, setMenuOpen] = useState(false);
  const [creatingIn, setCreatingIn] = useState<string>();
  const [appearance, setAppearance] = useState(false);
  const { page, projectId, params } = useRoute();
  const isConversation = page === "projects" && Boolean(projectId);

  useEffect(
    () => setMenuOpen(false),
    [page, projectId, params.get("agent"), compact],
  );

  const toggle = () => {
    if (compact) {
      setMenuOpen(!menuOpen);
      return;
    }

    const value = !collapsed;
    setCollapsed(value);

    try {
      localStorage.setItem("zentrale-sidebar-collapsed", String(value));
    } catch {
      /* Preferences remain usable without storage. */
    }

    requestAnimationFrame(() =>
      document.getElementById("sidebar-toggle")?.focus(),
    );
  };
  const appearanceDialog = appearance && (
    <Inspect
      title={t("Darstellung und Sprache")}
      onClose={() => {
        setAppearance(false);
        if (compact) setMenuOpen(false);
      }}
    >
      <PreferenceControls />
      <LogDiagnosticsControl />
    </Inspect>
  );

  return (
    <SidebarContext.Provider
      value={{
        compact,
        collapsed,
        menuOpen,
        toggle,
        newConversation: (id = "") => setCreatingIn(id),
        preferences: () => setAppearance(true),
      }}
    >
      <div
        className="min-h-dvh bg-surface"
        style={
          { "--sidebar-width": collapsed ? "0px" : "260px" } as CSSProperties
        }
      >
        <a
          href="#main-content"
          onClick={(event) => {
            event.preventDefault();
            document.getElementById("main-content")?.focus();
          }}
          className="sr-only z-50 rounded-lg bg-accent px-4 py-3 text-accent-foreground focus:not-sr-only focus:fixed focus:left-4 focus:top-2"
        >
          {t("Zum Inhalt springen")}
        </a>
        {compact ? (
          <Drawer.Backdrop isOpen={menuOpen} onOpenChange={setMenuOpen}>
            <Drawer.Content
              placement="left"
              className="w-80 max-w-[calc(100vw-3rem)]"
            >
              <Drawer.Dialog
                id="primary-navigation"
                aria-label={t("Hauptnavigation")}
                className="border-r border-separator bg-background p-0"
              >
                <Drawer.Header className="min-h-16 shrink-0 flex-row items-center justify-between px-4 py-2">
                  <Drawer.Heading className="sr-only">
                    {t("Hauptnavigation")}
                  </Drawer.Heading>
                  <Drawer.CloseTrigger
                    aria-label={t("Menü schließen")}
                    className="min-h-11 min-w-11"
                  />
                </Drawer.Header>
                <Drawer.Body className="flex-1 px-3 pb-4 pt-0">
                  <Navigation onNavigate={() => setMenuOpen(false)} />
                </Drawer.Body>
                {appearanceDialog}
              </Drawer.Dialog>
            </Drawer.Content>
          </Drawer.Backdrop>
        ) : (
          !collapsed && (
            <aside
              id="primary-navigation"
              aria-label={t("Hauptnavigation")}
              className="fixed inset-y-0 left-0 hidden w-[260px] overflow-y-auto border-r border-separator bg-background px-3 py-3 lg:block"
            >
              <Navigation />
            </aside>
          )
        )}
        <div className="lg:pl-[var(--sidebar-width)]">
          <main
            id="main-content"
            tabIndex={-1}
            className={`mx-auto min-w-0 px-4 outline-none sm:px-6 ${isConversation ? "pt-0 lg:px-8" : "max-w-6xl pb-12 pt-5 lg:px-10 lg:pt-10"}`}
          >
            {!isConversation && (
              <div className="mb-3">
                <SidebarToggle iconOnly />
              </div>
            )}
            {children}
          </main>
        </div>
      </div>
      {creatingIn !== undefined && (
        <NewConversation
          key={creatingIn}
          projectId={creatingIn}
          onClose={() => setCreatingIn(undefined)}
        />
      )}
      {!(compact && menuOpen) && appearanceDialog}
    </SidebarContext.Provider>
  );
}
