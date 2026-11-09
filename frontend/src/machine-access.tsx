import { Button } from "@heroui/react";

import type { Config } from "./config";
import { useAPI } from "./api";
import { Icon } from "./icons";
import { useI18n } from "./preferences";
import { Failure, useAction } from "./ui";

export function MachineAccess({
  keys,
  keyId,
  onChoose,
}: {
  keys: Config["sshKeys"];
  keyId: unknown;
  onChoose: (id: string) => void;
}) {
  const api = useAPI();
  const { t } = useI18n();
  const action = useAction();
  const selected = keys.find((key) => key.id === Number(keyId));

  return (
    <div className="space-y-3">
      <p className="text-sm leading-6 text-muted">
        {t(
          "Hinterlege den öffentlichen Schlüssel auf der Maschine in ~/.ssh/authorized_keys.",
        )}
      </p>
      <div className="flex flex-wrap gap-2">
        {selected?.publicKey && (
          <Button
            variant="secondary"
            className="min-h-11"
            isDisabled={action.busy}
            onPress={() =>
              action.run(() =>
                navigator.clipboard.writeText(selected.publicKey ?? ""),
              )
            }
          >
            <Icon name="copy" />
            {t("Öffentlichen Schlüssel kopieren")}
          </Button>
        )}
        <Button
          variant="ghost"
          className="min-h-11"
          isPending={action.busy}
          onPress={() =>
            action.run(async () => {
              const result = await api.mutate<{
                generateSSHKey: { id: number };
              }>("mutation{generateSSHKey{id}}");
              onChoose(String(result.generateSSHKey.id));
            })
          }
        >
          <Icon name="plus" />
          {t("Neuen SSH-Schlüssel erzeugen")}
        </Button>
      </div>
      <Failure message={action.error} />
    </div>
  );
}
