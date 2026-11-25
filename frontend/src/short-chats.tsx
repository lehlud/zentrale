import { Button } from "@heroui/react";
import { useState } from "react";
import { ActionsMenu } from "./actions-menu";
import { AgentView } from "./agent-view";
import { useAPI, useQuery } from "./api";
import { Icon } from "./icons";
import { navigate, setRouteParameter } from "./navigation";
import { useI18n } from "./preferences";
import { RenameConversation } from "./rename-conversation";
import { Failure, Loading, useAction } from "./ui";

type Chat = { id: string; name: string };
const chatPageSize = 50;

export function ShortChats({ chatId }: { chatId: string }) {
  return chatId ? (
    <ShortChat key={chatId} chatId={chatId} />
  ) : (
    <ShortChatList />
  );
}

function ShortChat({ chatId }: { chatId: string }) {
  const { data, error } = useQuery<{ shortChat: Chat }>(
    "query($id:String!){shortChat(id:$id){id name}}",
    { id: chatId },
  );

  if (error) return <Failure message={error} />;
  if (!data?.shortChat) return <Loading />;
  return <AgentView agentId={data.shortChat.id} />;
}

function ShortChatList() {
  const { t } = useI18n();
  const api = useAPI();
  const action = useAction();
  const [offset, setOffset] = useState(0);
  const [renaming, setRenaming] = useState<Chat>();
  const { data, error, loading } = useQuery<{ shortChats: Chat[] }>(
    "query($limit:Int!,$offset:Int!){shortChats(limit:$limit,offset:$offset){id name}}",
    { limit: chatPageSize + 1, offset },
  );
  const chats = data?.shortChats ?? [];

  async function create() {
    await action.run(
      async () => {
        const { createShortChat: chat } = await api.mutate<{
          createShortChat: Chat;
        }>("mutation { createShortChat { id name } }", {}, { announce: false });
        navigate("chats", chat.id);
        setRouteParameter("compose", "1");
      },
      "default",
      { announce: false },
    );
  }

  return (
    <section className="mx-auto max-w-2xl space-y-5">
      <header className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-2xl font-medium tracking-tight">
          {t("Kurzchats")}
        </h1>
        <Button isPending={action.busy} onPress={create}>
          <Icon name="compose" /> {t("Neuer Chat")}
        </Button>
      </header>
      <Failure message={error || action.error} />
      {loading && !data ? (
        <Loading />
      ) : chats.length ? (
        <ul className="divide-y divide-border">
          {chats.slice(0, chatPageSize).map((chat) => (
            <li key={chat.id} className="flex items-center gap-1">
              <a
                className="min-h-11 flex-1 truncate px-3 py-3 hover:bg-surface-secondary"
                href={`#/chats/${encodeURIComponent(chat.id)}`}
              >
                {chat.name}
              </a>
              <ActionsMenu
                label={t("Aktionen für {name}", { name: chat.name })}
                actions={[
                  {
                    id: "rename",
                    label: t("Umbenennen"),
                    onAction: () => setRenaming(chat),
                  },
                  {
                    id: "delete",
                    label: t("Entfernen"),
                    destructive: true,
                    onAction: () =>
                      void action.run(() => api.removeAgent(chat.id)),
                  },
                ]}
              />
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-sm text-muted">{t("Noch keine Kurzchats.")}</p>
      )}
      {(offset > 0 || chats.length > chatPageSize) && (
        <div className="flex justify-between gap-3">
          <Button
            variant="ghost"
            isDisabled={offset === 0}
            onPress={() => setOffset(offset - chatPageSize)}
          >
            {t("Zurück")}
          </Button>
          <Button
            variant="ghost"
            isDisabled={chats.length <= chatPageSize}
            onPress={() => setOffset(offset + chatPageSize)}
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
    </section>
  );
}
