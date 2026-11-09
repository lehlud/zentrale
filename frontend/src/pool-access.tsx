import { Button, Card } from "@heroui/react";

import type { Config } from "./config";
import { Icon } from "./icons";
import { useI18n } from "./preferences";
import { Failure, useAction } from "./ui";

export function PoolAccess({
  keys,
  keyId,
}: {
  keys: Config["sshKeys"];
  keyId: number;
}) {
  const { t } = useI18n();
  const action = useAction();
  const key = keys.find((key) => key.id === keyId);
  if (!key?.publicKey) {
    return null;
  }

  return (
    <Card.Content className="gap-3 border-t border-separator">
      <p className="text-sm font-medium">{t("Pool-Schlüssel")}</p>
      <p className="text-sm leading-6 text-muted">
        {t(
          "Den öffentlichen Pool-Schlüssel in ~/.ssh/authorized_keys der Pool-Mitglieder hinterlegen. Bei gemeinsamem Home genügt das einmal.",
        )}
      </p>
      <code className="break-all font-mono text-xs text-muted">
        {key.fingerprint}
      </code>
      <Button
        size="sm"
        variant="secondary"
        className="min-h-11 self-start"
        isPending={action.busy}
        onPress={() =>
          action.run(() => navigator.clipboard.writeText(key.publicKey ?? ""))
        }
      >
        <Icon name="copy" />
        {t("Öffentlichen Schlüssel kopieren")}
      </Button>
      <Failure message={action.error} />
    </Card.Content>
  );
}
