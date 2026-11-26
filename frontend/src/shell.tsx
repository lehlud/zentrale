import {
  Button,
  Drawer,
  Dropdown,
  Label,
  Link,
  buttonVariants,
} from "@heroui/react";
import type { CSSProperties, ReactNode } from "react";
import { createContext, useContext, useEffect, useState } from "react";
import { Tab, TabList, TabPanel, Tabs } from "react-aria-components";

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
import { newChatHref, SidebarChatHistory } from "./short-chats";
import { SidebarProjects } from "./sidebar-projects";
import { Inspect } from "./ui";
import { useSessionUser } from "./session";

type SidebarState = {
  compact: boolean;
  collapsed: boolean;
  menuOpen: boolean;
  toggle: () => void;
  expand: () => void;
  newConversation: (projectId: string) => void;
  preferences: () => void;
};

const SidebarContext = createContext<SidebarState | null>(null);

export function useSidebar() {
  return useContext(SidebarContext);
}

export function SidebarToggle({ iconOnly = false }: { iconOnly?: boolean }) {
  const state = useSidebar();
  const { t } = useI18n();
  if (!state?.compact) {
    return null;
  }

  return (
    <Button
      id="sidebar-toggle"
      variant="ghost"
      isIconOnly={iconOnly}
      aria-label={t("Menü öffnen")}
      aria-expanded={state.menuOpen}
      aria-controls="primary-navigation"
      className="min-h-11 min-w-11 shrink-0"
      onPress={state.toggle}
    >
      <Icon name="sidebar" className="size-5" />
      {!iconOnly && <span>{t("Menü")}</span>}
    </Button>
  );
}

const administration = [
  { id: "admin-settings", label: "Admin-Einstellungen" },
  { id: "usage", label: "Verbrauch" },
  { id: "providers", label: "Provider und Modelle" },
  { id: "machines", label: "Maschinen" },
  { id: "keys", label: "Schlüssel" },
] as const;

const sidebarSections = [
  { id: "chats", label: "Unterhaltungen", icon: "chat" },
  { id: "projects", label: "Projekte", icon: "projects" },
] as const;

function Navigation({ onNavigate }: { onNavigate?: () => void }) {
  const { t } = useI18n();
  const sidebar = useSidebar()!;
  const user = useSessionUser();
  const { page, projectId } = useRoute();
  const routeSection = page === "projects" || page === "chats" ? page : null;
  const [section, setSection] = useState<string>(routeSection ?? "chats");
  const collapsed = sidebar.collapsed && !sidebar.compact;

  useEffect(() => {
    if (routeSection) setSection(routeSection);
  }, [routeSection]);

  const panelClass = `col-start-2 row-span-2 row-start-1 min-h-0 min-w-0 overflow-y-auto overscroll-contain px-2 py-2 [scrollbar-width:thin] data-[inert]:hidden ${collapsed ? "hidden" : ""}`;

  return (
    <nav aria-label={t("Hauptnavigation")} className="h-full min-h-0">
      <Tabs
        orientation="vertical"
        selectedKey={section}
        onSelectionChange={(key) => {
          setSection(String(key));
          sidebar.expand();
        }}
        className="grid h-full min-h-0 grid-cols-[3.25rem_minmax(0,1fr)] grid-rows-[auto_minmax(0,1fr)_auto] gap-0"
      >
        <div className="col-start-1 row-start-1 flex flex-col items-center gap-1 border-r border-separator py-2">
          {sidebar.compact ? (
            <Drawer.CloseTrigger
              aria-label={t("Menü schließen")}
              className="static min-h-11 min-w-11"
            />
          ) : (
            <Button
              isIconOnly
              variant="ghost"
              aria-label={t(
                collapsed ? "Seitenleiste öffnen" : "Seitenleiste schließen",
              )}
              aria-controls="sidebar-section-panels"
              aria-expanded={!collapsed}
              className="size-11 text-muted"
              onPress={sidebar.toggle}
            >
              <Icon name="sidebar" className="size-5" />
            </Button>
          )}
          <Link
            href={newChatHref}
            aria-label={t("Neue Unterhaltung")}
            aria-current={page === "chats" && !projectId ? "page" : undefined}
            onPress={() => {
              setSection("chats");
              onNavigate?.();
            }}
            className={buttonVariants({
              variant: "ghost",
              isIconOnly: true,
              className: "size-11",
            })}
          >
            <Icon name="compose" className="size-5" />
          </Link>
        </div>

        <div className="col-start-1 row-start-2 min-h-0 overflow-y-auto border-r border-separator [scrollbar-width:none]">
          <TabList
            aria-label={t("Bereiche")}
            className="flex w-full flex-col items-center gap-1 py-1"
          >
            {sidebarSections.map(({ id, label, icon }) => (
              <Tab
                key={id}
                id={id}
                aria-label={t(label)}
                onPress={sidebar.expand}
                className="size-11 shrink-0 cursor-pointer rounded-lg text-muted outline-none hover:bg-default data-[focus-visible]:ring-2 data-[focus-visible]:ring-inset data-[focus-visible]:ring-accent data-[selected]:bg-default data-[selected]:text-foreground"
              >
                <span
                  title={t(label)}
                  className="grid size-full place-items-center"
                >
                  <Icon name={icon} className="size-5" />
                </span>
              </Tab>
            ))}
          </TabList>
        </div>

        <div id="sidebar-section-panels" className="contents">
          <TabPanel id="chats" className={panelClass} shouldForceMount>
            <SidebarChatHistory onNavigate={onNavigate} />
          </TabPanel>
          <TabPanel id="projects" className={panelClass} shouldForceMount>
            <SidebarProjects
              key={user?.id}
              userId={user?.id ?? ""}
              canCreate={
                user?.role === "admin" || Boolean(user?.canCreateProjects)
              }
              onNavigate={onNavigate}
            />
          </TabPanel>
        </div>

        {!collapsed && (
          <div
            className="col-start-1 row-start-3 border-r border-separator"
            aria-hidden="true"
          />
        )}
        <NavigationMenus compact={collapsed} onNavigate={onNavigate} />
      </Tabs>
    </nav>
  );
}

function NavigationMenus({
  compact,
  onNavigate,
}: {
  compact: boolean;
  onNavigate?: () => void;
}) {
  const { t } = useI18n();
  const api = useAPI();
  const sidebar = useSidebar()!;
  const user = useSessionUser();
  const admin = user?.role === "admin";

  return (
    <div
      className={`row-start-3 space-y-1 border-t border-separator p-1 ${compact ? "col-start-1 border-r" : "col-start-2 px-2"}`}
    >
      <Dropdown>
        <Button
          variant="ghost"
          isIconOnly={compact}
          aria-label={t("Einstellungen öffnen")}
          className={
            compact
              ? "size-11"
              : "min-h-11 w-full justify-start gap-2 px-2 text-sm"
          }
        >
          <Icon name="settings" className="size-5" />
          {!compact && (
            <>
              <span className="flex-1 text-left">{t("Einstellungen")}</span>
              <Icon name="more" />
            </>
          )}
        </Button>
        <Dropdown.Popover placement="top start">
          <Dropdown.Menu aria-label={t("Einstellungen")}>
            <Dropdown.Item
              id="settings"
              href="#/settings"
              onAction={onNavigate}
              className="min-h-11"
            >
              <Label>{t("Persönliche Einstellungen")}</Label>
            </Dropdown.Item>
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
      {admin && (
        <Dropdown>
          <Button
            variant="ghost"
            isIconOnly={compact}
            aria-label={t("Admin")}
            className={
              compact
                ? "size-11"
                : "min-h-11 w-full justify-start gap-2 px-2 text-sm"
            }
          >
            <Icon name="keys" className="size-5" />
            {!compact && (
              <>
                <span className="flex-1 text-left">{t("Admin")}</span>
                <Icon name="more" />
              </>
            )}
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
            </Dropdown.Menu>
          </Dropdown.Popover>
        </Dropdown>
      )}
    </div>
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
  const isConversation =
    page === "chats" || (page === "projects" && Boolean(projectId));

  useEffect(
    () => setMenuOpen(false),
    [page, projectId, params.get("agent"), compact],
  );

  function setPanelCollapsed(value: boolean) {
    setCollapsed(value);

    try {
      localStorage.setItem("zentrale-sidebar-collapsed", String(value));
    } catch {
      /* Preferences remain usable without storage. */
    }
  }

  const toggle = () => {
    if (compact) setMenuOpen((open) => !open);
    else setPanelCollapsed(!collapsed);
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
        expand: () => {
          if (collapsed && !compact) setPanelCollapsed(false);
        },
        newConversation: setCreatingIn,
        preferences: () => setAppearance(true),
      }}
    >
      <div
        className="min-h-dvh bg-surface"
        style={
          {
            "--sidebar-width": collapsed ? "3.25rem" : "20rem",
          } as CSSProperties
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
              className="w-80 max-w-[calc(100vw-1.5rem)]"
            >
              <Drawer.Dialog
                id="primary-navigation"
                aria-label={t("Hauptnavigation")}
                className="border-r border-separator bg-background p-0"
              >
                <Drawer.Body className="min-h-0 flex-1 overflow-hidden p-0">
                  <Navigation onNavigate={() => setMenuOpen(false)} />
                </Drawer.Body>
                {appearanceDialog}
              </Drawer.Dialog>
            </Drawer.Content>
          </Drawer.Backdrop>
        ) : (
          <aside
            id="primary-navigation"
            aria-label={t("Hauptnavigation")}
            className="fixed inset-y-0 left-0 hidden w-[var(--sidebar-width)] border-r border-separator bg-background lg:block"
          >
            <Navigation />
          </aside>
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
