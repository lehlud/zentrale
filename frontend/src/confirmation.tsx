import { Button, Modal } from "@heroui/react";
import type { ReactNode } from "react";
import { useEffect, useState } from "react";

import type { API } from "./api";
import { useI18n } from "./preferences";

type PendingConfirmation = {
  message: string;
  actionLabel: string;
  resolve: (confirmed: boolean) => void;
};

export function ConfirmationProvider({
  api,
  children,
}: {
  api: API;
  children: ReactNode;
}) {
  const { t } = useI18n();
  const [requests, setRequests] = useState<PendingConfirmation[]>([]);
  const pending = requests[0];

  useEffect(() => {
    api.confirm = (message, actionLabel = "Entfernen") =>
      new Promise((resolve) =>
        setRequests((current) => [
          ...current,
          { message, actionLabel, resolve },
        ]),
      );
    return () => {
      api.confirm = undefined;
    };
  }, [api]);

  function close(confirmed: boolean) {
    pending?.resolve(confirmed);
    setRequests((current) => current.slice(1));
  }

  return (
    <>
      {children}
      {pending && (
        <Modal
          isOpen
          onOpenChange={(open) => {
            if (!open) close(false);
          }}
        >
          <Modal.Backdrop>
            <Modal.Container size="sm" placement="center">
              <Modal.Dialog>
                <Modal.Header>
                  <Modal.Heading>
                    {t(
                      pending.actionLabel === "Entfernen"
                        ? "Entfernen bestätigen"
                        : "Aktion bestätigen",
                    )}
                  </Modal.Heading>
                </Modal.Header>
                <Modal.Body>
                  <p>{t(pending.message)}</p>
                </Modal.Body>
                <Modal.Footer className="flex-wrap">
                  <Button
                    className="min-h-11"
                    autoFocus
                    variant="ghost"
                    onPress={() => close(false)}
                  >
                    {t("Abbrechen")}
                  </Button>
                  <Button
                    className="min-h-11"
                    variant="danger"
                    onPress={() => close(true)}
                  >
                    {t(pending.actionLabel)}
                  </Button>
                </Modal.Footer>
              </Modal.Dialog>
            </Modal.Container>
          </Modal.Backdrop>
        </Modal>
      )}
    </>
  );
}
