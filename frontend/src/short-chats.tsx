import { Button, Link, buttonVariants } from "@heroui/react";
import { useState } from "react";
import { ActionsMenu } from "./actions-menu";
import { AgentComposer } from "./agent-composer";
import { AgentView } from "./agent-view";
import { useAPI, useQuery } from "./api";
import { ChatModelSelect, chatModelFields } from "./chat-model-select";
import type { ModelChoice } from "./generated";
import { Icon } from "./icons";
import { modelSelectionKey, type ModelSelection } from "./model-selection";
import { useRoute } from "./navigation";
import { useI18n } from "./preferences";
import { RenameConversation } from "./rename-conversation";
import { SidebarToggle } from "./shell";
import { Failure, Loading, useAction } from "./ui";

type Chat = { id: string; name: string; status: string };
const chatPageSize = 8;
export const newChatHref = "#/chats";

function chatHref(id: string) {
  return `${newChatHref}/${encodeURIComponent(id)}`;
}

export function ShortChats({ chatId }: { chatId: string }) {
  return chatId ? <ShortChat key={chatId} chatId={chatId} /> : <NewShortChat />;
}

function NewShortChat() {
  const { t } = useI18n();
  const draftURL = location.href;
  const [choice, setChoice] = useState<ModelSelection>();
  const { data, error } = useQuery<{
    shortChatModelOptions: {
      presets: ModelChoice[] | null;
      defaults: { main: ModelChoice };
    };
  }>(`{
    shortChatModelOptions {
      presets { ${chatModelFields} contextWindow }
      defaults { main { ${chatModelFields} contextWindow } }
    }
  }`);
  const options = data?.shortChatModelOptions;
  const presets = options?.presets ?? [];
  const selected =
    choice &&
    presets.find(
      (preset) => modelSelectionKey(preset) === modelSelectionKey(choice),
    );

  return (
    <section data-short-chat-draft>
      <header className="flex min-h-16 items-center">
        <SidebarToggle iconOnly />
        {options && (
          <div className="min-w-0 max-w-[70%]">
            <ChatModelSelect
              presets={presets}
              value={selected ?? options.defaults.main}
              onChange={setChoice}
            />
          </div>
        )}
      </header>
      <Failure message={error} />
      <div className="mx-auto max-w-xl px-2 pt-[min(15vh,8rem)] pb-40 text-center">
        <h1 className="text-2xl font-medium tracking-tight">
          {t("Neue Unterhaltung")}
        </h1>
      </div>
      <AgentComposer
        agentId="draft-short-chat"
        inputs={[]}
        following
        latest={() => {}}
        autoFocus
        shortChat
        draftContextWindow={(selected || options?.defaults.main)?.contextWindow}
        initialChatModel={selected ? choice : undefined}
        onShortChatStarted={(id) => {
          if (location.href === draftURL)
            location.replace(`${chatHref(id)}?compose=1`);
        }}
      />
    </section>
  );
}

function ShortChat({ chatId }: { chatId: string }) {
  const { data, error } = useQuery<{ shortChat: Chat }>(
    "query($id:String!){shortChat(id:$id){id name}}",
    { id: chatId },
  );

  if (data?.shortChat && !error)
    return <AgentView agentId={data.shortChat.id} />;

  return (
    <section>
      <SidebarToggle iconOnly />
      {error ? <Failure message={error} /> : <Loading />}
    </section>
  );
}

export function SidebarChatHistory({
  onNavigate,
}: {
  onNavigate?: () => void;
}) {
  const { t } = useI18n();
  const { page, projectId } = useRoute();
  const activeId = page === "chats" ? projectId : "";

  return (
    <section aria-label={t("Unterhaltungen")} className="space-y-2">
      <h2 className="flex min-h-11 items-center px-2 text-sm font-semibold">
        {t("Unterhaltungen")}
      </h2>
      <ChatHistoryGroup pinned activeId={activeId} onNavigate={onNavigate} />
      <ChatHistoryGroup activeId={activeId} onNavigate={onNavigate} />
    </section>
  );
}

function ChatHistoryGroup({
  pinned = false,
  activeId,
  onNavigate,
}: {
  pinned?: boolean;
  activeId: string;
  onNavigate?: () => void;
}) {
  const { t } = useI18n();
  const api = useAPI();
  const action = useAction();
  const [offset, setOffset] = useState(0);
  const [renaming, setRenaming] = useState<Chat>();
  const { data, error, loading } = useQuery<{ shortChats: Chat[] }>(
    `query($limit:Int!,$offset:Int!,$pinned:Boolean!){
      shortChats(limit:$limit,offset:$offset,pinned:$pinned){id name status}
    }`,
    { limit: chatPageSize + 1, offset, pinned },
  );
  const chats = data?.shortChats ?? [];
  const label = t(pinned ? "Angeheftet" : "Unterhaltungsverlauf");

  if (
    pinned &&
    !error &&
    !action.error &&
    !chats.length &&
    offset === 0 &&
    !renaming
  )
    return null;

  return (
    <details open className="group/chat-group">
      <summary className="flex min-h-9 cursor-pointer list-none items-center gap-1.5 rounded-lg px-2 text-xs font-medium text-muted hover:bg-default [&::-webkit-details-marker]:hidden">
        <Icon
          name="chevron"
          className="size-3.5 -rotate-90 group-open/chat-group:rotate-0"
        />
        {label}
      </summary>
      <Failure
        message={error || action.error}
        onRetry={error ? () => api.refresh() : undefined}
      />
      {!pinned && !loading && !error && !chats.length && offset === 0 && (
        <p className="px-3 py-2 text-xs text-muted">
          {t("Noch keine Unterhaltungen")}
        </p>
      )}
      <ul aria-label={label}>
        {chats.slice(0, chatPageSize).map((chat) => (
          <li key={chat.id} className="flex min-w-0 items-center">
            <Link
              href={chatHref(chat.id)}
              onPress={onNavigate}
              aria-current={activeId === chat.id ? "page" : undefined}
              className={buttonVariants({
                variant: activeId === chat.id ? "secondary" : "ghost",
                className: "min-h-10 min-w-0 flex-1 justify-start gap-2 px-3",
              })}
            >
              <Icon
                name={pinned ? "pin" : "chat"}
                className="size-4 text-muted"
              />
              <span className="truncate" title={chat.name}>
                {chat.name}
              </span>
            </Link>
            <ActionsMenu
              label={t("Aktionen für {name}", { name: chat.name })}
              isDisabled={action.busy}
              actions={[
                {
                  id: "pin",
                  label: t(pinned ? "Loslösen" : "Anheften"),
                  onAction: () =>
                    void action.run(
                      () =>
                        api.mutate(
                          "mutation($id:String!,$pinned:Boolean!){setShortChatPinned(id:$id,pinned:$pinned)}",
                          { id: chat.id, pinned: !pinned },
                          { announce: false },
                        ),
                      "default",
                      { announce: false },
                    ),
                },
                {
                  id: "rename",
                  label: t("Umbenennen"),
                  isDisabled:
                    chat.status === "running" || chat.status === "waiting",
                  onAction: () => setRenaming(chat),
                },
                {
                  id: "delete",
                  label: t("Entfernen"),
                  destructive: true,
                  onAction: () =>
                    void action.run(async () => {
                      await api.removeAgent(chat.id);
                      if (location.hash.split("?")[0] === chatHref(chat.id))
                        location.replace(newChatHref);
                    }),
                },
              ]}
            />
          </li>
        ))}
      </ul>
      {(offset > 0 || chats.length > chatPageSize) && (
        <div className="flex justify-between gap-3">
          <Button
            variant="ghost"
            size="sm"
            isDisabled={offset === 0}
            onPress={() =>
              setOffset((value) => Math.max(0, value - chatPageSize))
            }
          >
            {t("Zurück")}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            isDisabled={chats.length <= chatPageSize}
            onPress={() => setOffset((value) => value + chatPageSize)}
          >
            {t("Weiter")}
          </Button>
        </div>
      )}
      {renaming && (
        <RenameConversation
          agent={renaming}
          onClose={() => setRenaming(undefined)}
        />
      )}
    </details>
  );
}
