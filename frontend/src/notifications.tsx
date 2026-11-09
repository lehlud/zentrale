import { Toast, ToastQueue } from "@heroui/react";
import { useEffect, useState } from "react";

import { useAPI } from "./api";
import { useI18n } from "./preferences";

export function Notifications() {
  const api = useAPI();
  const { locale, t } = useI18n();
  const [queue] = useState(() => new ToastQueue({ maxVisibleToasts: 3 }));

  useEffect(() => {
    let networkToast: string | undefined;
    let successToast: string | undefined;
    let wasInterrupted = false;

    const updateConnection = () => {
      if (api.connection.snapshot()) {
        if (wasInterrupted) {
          return;
        }

        wasInterrupted = true;
        if (successToast) queue.close(successToast);

        networkToast = queue.add(
          {
            title: t("Verbindung unterbrochen"),
            description: t(
              "Der Server ist gerade nicht erreichbar. Wir verbinden automatisch neu. Deine Eingaben bleiben erhalten.",
            ),
            variant: "warning",
            actionProps: {
              children: t("Jetzt verbinden"),
              onPress: () => void api.connection.retry(),
            },
          },
          { timeout: 0 },
        );
      } else if (wasInterrupted) {
        wasInterrupted = false;
        if (networkToast) queue.close(networkToast);

        successToast = queue.add(
          {
            title: t("Wieder verbunden"),
            description: t(
              "Die Daten werden aktualisiert. Fehlgeschlagene Aktionen bei Bedarf erneut ausführen.",
            ),
            variant: "success",
          },
          { timeout: 6000 },
        );
      }
    };
    const announceSuccess = (event: Event) => {
      if (api.connection.snapshot()) {
        return;
      }

      const content = {
        title: t((event as CustomEvent<string>).detail),
        variant: "success" as const,
      };
      // useAction and mutate may both announce one operation. Reuse its toast.
      if (
        !successToast ||
        !queue.update(successToast, content, { timeout: 4000 })
      )
        successToast = queue.add(content, { timeout: 4000 });
    };
    const offline = () => api.connection.reportFailure();
    const online = () => void api.connection.retry();
    const unsubscribe = api.connection.subscribe(updateConnection);
    api.connection.resume();
    updateConnection();
    window.addEventListener("offline", offline);
    window.addEventListener("online", online);
    window.addEventListener("zentrale:notice", announceSuccess);

    return () => {
      unsubscribe();
      api.connection.pause();
      queue.clear();
      window.removeEventListener("offline", offline);
      window.removeEventListener("online", online);
      window.removeEventListener("zentrale:notice", announceSuccess);
    };
    // Rebuild visible messages when the user changes language.
  }, [api, locale, queue]);

  return (
    <Toast.Provider
      queue={queue}
      placement="bottom end"
      width={360}
      aria-label={t("Benachrichtigungen")}
    >
      {({ toast }) => (
        <Toast
          toast={toast}
          variant={toast.content.variant}
          className="grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-2 sm:grid-cols-[auto_minmax(0,1fr)_auto]"
        >
          <Toast.Indicator variant={toast.content.variant} />
          <Toast.Content>
            <Toast.Title>{toast.content.title}</Toast.Title>
            {toast.content.description && (
              <Toast.Description className="mt-1 leading-5">
                {toast.content.description}
              </Toast.Description>
            )}
            {toast.content.actionProps && (
              <Toast.ActionButton
                {...toast.content.actionProps}
                className="mt-2 min-h-11 self-start"
                size="sm"
                variant="secondary"
              />
            )}
          </Toast.Content>
          <Toast.CloseButton
            aria-label={t("Benachrichtigung schließen")}
            className="pointer-events-auto static col-start-3 row-start-1 size-11 opacity-100"
          />
        </Toast>
      )}
    </Toast.Provider>
  );
}
