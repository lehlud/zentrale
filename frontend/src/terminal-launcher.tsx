import { Button } from "@heroui/react";
import { lazy, Suspense, useRef, useState } from "react";

import { Icon } from "./icons";
import { useI18n } from "./preferences";

// Keep the WASM emulator and its renderer out of the normal conversation path.
const VMTerminal = lazy(() => import("./vm-terminal"));

export function TerminalLauncher({
  projectId,
  projectName,
  agentId = "",
}: {
  projectId: string;
  projectName: string;
  agentId?: string;
}) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const close = () => {
    setOpen(false);
    requestAnimationFrame(() => trigger.current?.focus());
  };
  return (
    <>
      <Button
        ref={trigger}
        isIconOnly
        variant="ghost"
        className="min-h-11 min-w-11"
        aria-label={t("Terminal in der VM öffnen")}
        onPress={() => setOpen(true)}
      >
        <Icon name="terminal" className="size-5" />
      </Button>
      {open && (
        <Suspense
          fallback={<span role="status">{t("Terminal wird geladen …")}</span>}
        >
          <VMTerminal
            key={`${projectId}:${agentId}`}
            projectId={projectId}
            projectName={projectName}
            agentId={agentId}
            onClose={close}
          />
        </Suspense>
      )}
    </>
  );
}
