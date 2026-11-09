import {
  Button,
  Card,
  Chip,
  Label,
  Link,
  ListBox,
  Select,
} from "@heroui/react";
import { useEffect, useRef, useState } from "react";

import { ActionsMenu, type MenuAction } from "./actions-menu";
import { CONFIG, useAPI, useQuery } from "./api";
import { MoreButton, useBatch } from "./batches";
import type { Config } from "./config";
import type { Provider } from "./generated";
import { ProviderModels } from "./models";
import { setRouteParameter, useRoute } from "./navigation";
import { ProviderEditor, loginLabels } from "./provider-editor";
import { ProviderSignIn } from "./provider-login";
import { useI18n } from "./preferences";
import { Empty, Failure, Loading, Title, useAction } from "./ui";
import { Icon } from "./icons";
import { useMediaQuery } from "./media";

export function Providers() {
  const { t } = useI18n();
  const api = useAPI();
  const compact = useMediaQuery("(max-width: 1279px)");
  const { data, error, loading } = useQuery<Config>(CONFIG);
  const { params } = useRoute();
  const [editing, setEditing] = useState<Record<string, unknown> | null>(null);
  const [signingIn, setSigningIn] = useState<Provider | null>(null);
  const [catalogRequest, setCatalogRequest] = useState<string>();
  const catalogHeading = useRef<HTMLHeadingElement>(null);
  const action = useAction();
  const providers = useBatch(data?.providers ?? []);
  const selected =
    data?.providers.find(
      (provider) => provider.id === params.get("provider"),
    ) ??
    data?.providers.find((provider) => provider.connected) ??
    data?.providers[0];

  useEffect(() => {
    if (!catalogRequest || selected?.id !== catalogRequest) {
      return;
    }

    // In the stacked layout the catalog can be far below the account list.
    catalogHeading.current?.scrollIntoView({ block: "start" });
    catalogHeading.current?.focus({ preventScroll: true });
    setCatalogRequest(undefined);
  }, [catalogRequest, selected?.id]);

  const create = () =>
    setEditing({
      name: "",
      kind: "openai",
      authMode: "api",
      billingMode: "metered",
    });
  const kindName = (provider: Provider) =>
    data?.providerKinds.find((kind) => kind.id === provider.kind)?.name ??
    provider.kind;
  const selectAccount = (provider: Provider) => {
    setRouteParameter("provider", provider.id);
    if (provider.authMode !== "api" && !provider.connected)
      setSigningIn(provider);
    else setCatalogRequest(provider.id);
  };

  function accountActions(provider: Provider): MenuAction[] {
    const actions: MenuAction[] = [
      {
        id: "edit",
        label: t("Bearbeiten"),
        onAction: () => setEditing({ ...provider, credential: "" }),
      },
      {
        id: "refresh",
        label: t("Modelle aktualisieren"),
        isDisabled: !provider.connected || provider.catalogStatus === "loading",
        onAction: () =>
          action.run(() =>
            api.mutate(
              "mutation($providerId:String!){refreshModels(providerId:$providerId)}",
              { providerId: provider.id },
            ),
          ),
      },
    ];
    if (provider.authMode !== "api" && provider.connected)
      actions.push({
        id: "sign-in",
        label: t("Erneut anmelden"),
        onAction: () => setSigningIn(provider),
      });

    if (provider.connected)
      actions.push({
        id: "disconnect",
        label: t("Trennen"),
        onAction: () =>
          action.run(() =>
            api.mutate(
              "mutation($providerId:String!){disconnectProvider(providerId:$providerId)}",
              { providerId: provider.id },
            ),
          ),
      });

    actions.push({
      id: "remove",
      label: t("Entfernen"),
      destructive: true,
      onAction: () =>
        action.run(() =>
          api.mutate(
            "mutation($kind:String!,$id:String!){deleteResource(kind:$kind,id:$id)}",
            { kind: "provider", id: provider.id },
          ),
        ),
    });

    return actions;
  }
  const account = (provider: Provider) => (
    <ProviderAccount
      key={provider.id}
      provider={provider}
      kind={kindName(provider)}
      selected={selected?.id === provider.id}
      actions={accountActions(provider)}
      busy={action.busy}
      compact={compact}
      onSelect={() => selectAccount(provider)}
    />
  );

  return (
    <>
      <Title
        description={t(
          "Verbinde einen Zugang, entdecke seine Modelle und wähle die Modelle für deine Agenten.",
        )}
        action={
          <Button className="min-h-11" onPress={create}>
            <Icon name="plus" />
            {t("Provider hinzufügen")}
          </Button>
        }
      >
        {t("Provider")}
      </Title>
      <Failure message={error || action.error} onRetry={() => api.refresh()} />
      {loading && !data ? (
        <Loading />
      ) : !error && !data?.providers.length ? (
        <Empty>{t("Keine Provider konfiguriert")}</Empty>
      ) : (
        <div className="grid min-w-0 items-start gap-7 xl:grid-cols-[15rem_minmax(0,1fr)]">
          <section
            aria-label={t("Provider-Zugänge")}
            className="min-w-0 space-y-3"
          >
            {!compact ? (
              <div className="space-y-2">
                {providers.items.map(account)}
                <MoreButton batch={providers} />
              </div>
            ) : (
              <div className="space-y-3">
                <Select
                  className="gap-2"
                  value={selected?.id ?? null}
                  onChange={(id) =>
                    setRouteParameter("provider", String(id ?? ""))
                  }
                >
                  <Label>{t("Provider-Zugang")}</Label>
                  <Select.Trigger className="min-h-11">
                    <Select.Value />
                    <Select.Indicator />
                  </Select.Trigger>
                  <Select.Popover>
                    <ListBox>
                      {data?.providers.map((provider) => (
                        <ListBox.Item
                          key={provider.id}
                          id={provider.id}
                          textValue={provider.name}
                          aria-label={provider.name}
                          className="min-h-11"
                        >
                          <Label>{provider.name}</Label>
                          <ListBox.ItemIndicator />
                        </ListBox.Item>
                      ))}
                    </ListBox>
                  </Select.Popover>
                </Select>
                {selected && account(selected)}
              </div>
            )}
          </section>
          {selected && (
            <section
              className="min-w-0 space-y-4"
              aria-label={t("Modelle von {name}", { name: selected.name })}
            >
              <div className="flex flex-wrap items-start justify-between gap-3 border-b border-border pb-4">
                <div className="space-y-1.5">
                  <h2
                    ref={catalogHeading}
                    tabIndex={-1}
                    className="scroll-mt-4 text-base font-medium outline-none"
                  >
                    {t("Sprachmodelle")}
                    {!compact && ` · ${selected.name}`}
                  </h2>
                  <p className="text-xs text-muted">
                    {t(loginLabels[selected.authMode] ?? selected.authMode)}
                  </p>
                </div>
                <Link href="#/settings" className="min-h-11 text-xs">
                  {t("Standardmodelle ändern")} ↗
                </Link>
              </div>
              {!selected.connected ? (
                <Empty
                  action={
                    <Button
                      className="min-h-11"
                      onPress={() =>
                        selected.authMode === "api"
                          ? setEditing({ ...selected, credential: "" })
                          : setSigningIn(selected)
                      }
                    >
                      {t(
                        selected.authMode === "api"
                          ? "Zugang konfigurieren"
                          : "Anmelden",
                      )}
                    </Button>
                  }
                >
                  {t(
                    "Diesen Provider verbinden, um verfügbare Modelle abzurufen.",
                  )}
                </Empty>
              ) : (
                <>
                  <Failure
                    message={selected.catalogError}
                    onRetry={() =>
                      action.run(() =>
                        api.mutate(
                          "mutation($providerId:String!){refreshModels(providerId:$providerId)}",
                          { providerId: selected.id },
                        ),
                      )
                    }
                  />
                  {selected.catalogStatus === "loading" && (
                    <p role="status" className="text-sm text-muted">
                      {t("Modelle werden geladen …")}
                    </p>
                  )}
                  <ProviderModels key={selected.id} provider={selected} />
                </>
              )}
            </section>
          )}
        </div>
      )}
      {editing && (
        <ProviderEditor
          initial={editing}
          kinds={data?.providerKinds ?? []}
          onClose={() => setEditing(null)}
          onSaved={(provider) => setRouteParameter("provider", provider.id)}
        />
      )}
      {signingIn && (
        <ProviderSignIn
          provider={signingIn}
          onClose={() => setSigningIn(null)}
        />
      )}
    </>
  );
}

function ProviderAccount({
  provider,
  kind,
  selected,
  actions,
  busy,
  onSelect,
  compact,
}: {
  provider: Provider;
  kind: string;
  selected: boolean;
  actions: MenuAction[];
  busy: boolean;
  onSelect: () => void;
  compact: boolean;
}) {
  const { t } = useI18n();
  const needsSignIn = provider.authMode !== "api" && !provider.connected;
  const status = (
    <div className="flex flex-wrap items-center gap-2">
      <Chip
        size="sm"
        variant="soft"
        color={provider.connected ? "success" : "default"}
      >
        {t(provider.connected ? "Verbunden" : "Nicht verbunden")}
      </Chip>
      <span className="text-xs text-muted">
        {t("{count} Sprachmodelle", { count: provider.modelCount })}
      </span>
    </div>
  );
  const menu = (
    <ActionsMenu
      label={t("Aktionen für {name}", { name: provider.name })}
      actions={actions}
      isDisabled={busy}
    />
  );
  const select = (
    <Button
      className="-ml-2 min-h-11 self-start text-xs"
      size="sm"
      aria-pressed={needsSignIn ? undefined : selected}
      variant="ghost"
      onPress={onSelect}
    >
      {t(needsSignIn ? "Anmelden" : "Modelle ansehen")}
      <Icon name="arrow" />
    </Button>
  );
  if (compact) {
    return (
      <Card variant="transparent" data-provider-account>
        <Card.Content className="gap-1 p-0">
          <h2 className="sr-only">{provider.name}</h2>
          <div className="flex items-center justify-between gap-3">
            {status}
            {menu}
          </div>
          {provider.accountLabel && (
            <p className="break-words text-xs text-muted">
              {provider.accountLabel}
            </p>
          )}
          {needsSignIn && select}
        </Card.Content>
      </Card>
    );
  }

  return (
    <Card
      className={`min-w-0 border ${selected ? "border-accent" : "border-border"}`}
      variant={selected ? "secondary" : "transparent"}
      data-provider-account
    >
      <Card.Content className="gap-2">
        <div className="flex min-w-0 items-start justify-between gap-1">
          <div className="min-w-0 space-y-1 pt-1">
            <h2 className="break-words text-sm font-medium">
              {provider.name}
              {selected && (
                <span aria-hidden="true" className="ml-2 text-accent">
                  ✓
                </span>
              )}
            </h2>
            <p className="text-xs text-muted">
              {kind}
              {provider.accountLabel ? ` · ${provider.accountLabel}` : ""}
            </p>
          </div>
          {menu}
        </div>
        {status}
        {select}
      </Card.Content>
    </Card>
  );
}
