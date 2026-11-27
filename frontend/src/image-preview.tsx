import { Modal } from "@heroui/react";

import { useI18n } from "./preferences";

export function ImagePreview({
  name,
  src,
  onClose,
}: {
  name: string;
  src: string;
  onClose: () => void;
}) {
  const { t } = useI18n();
  return (
    <Modal isOpen onOpenChange={(open) => !open && onClose()}>
      <Modal.Backdrop>
        <Modal.Container size="full">
          <Modal.Dialog className="flex h-dvh max-h-dvh flex-col gap-0 overflow-hidden p-0">
            <Modal.CloseTrigger aria-label={t("Schließen")} />
            <Modal.Header className="shrink-0 border-b border-border px-4 py-3 pr-12">
              <Modal.Heading className="truncate">{name}</Modal.Heading>
            </Modal.Header>
            <div className="flex min-h-0 flex-1 items-center justify-center p-4">
              <img
                src={src}
                alt={name}
                className="max-h-full max-w-full object-contain"
              />
            </div>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
