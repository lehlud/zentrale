import { Alert, Button } from "@heroui/react";

import { useAPI } from "./api";
import type { SSHHostKey } from "./generated";
import { useI18n } from "./preferences";
import { Failure, useAction } from "./ui";

export function HostKeyTrust({
  challenge,
  onTrusted,
}: {
  challenge: SSHHostKey;
  onTrusted: () => Promise<void>;
}) {
  const api = useAPI();
  const { t } = useI18n();
  const action = useAction();

  return (
    <Alert status="warning" className="min-w-0">
      <Alert.Indicator />
      <Alert.Content className="min-w-0 space-y-3">
        <Alert.Title>{t("Host-Schlüssel prüfen")}</Alert.Title>
        <p className="text-sm">
          {t(
            "Diesem Host-Schlüssel wird noch nicht vertraut. Den Fingerprint vor dem Verbinden prüfen.",
          )}
        </p>
        <dl className="space-y-2 text-sm">
          <div>
            <dt className="font-medium">Host</dt>
            <dd className="break-all">{challenge.host}</dd>
          </div>
          <div>
            <dt className="font-medium">Fingerprint</dt>
            <dd className="break-all font-mono text-xs">
              {challenge.fingerprint}
            </dd>
          </div>
        </dl>
        <details>
          <summary className="min-h-6 cursor-pointer text-sm">
            {t("Öffentlichen Schlüssel anzeigen")}
          </summary>
          <code className="mt-2 block break-all text-xs">
            {challenge.publicKey}
          </code>
        </details>
        <Failure message={action.error} />
        <Button
          className="h-auto min-h-11 max-w-full whitespace-normal py-3"
          variant="secondary"
          isPending={action.busy}
          onPress={() =>
            action.run(async () => {
              await api.mutate(
                "mutation($publicKey:String!,$trust:String!){trustHostKey(publicKey:$publicKey,trust:$trust)}",
                { publicKey: challenge.publicKey, trust: "trust" },
              );
              await onTrusted();
            })
          }
        >
          {t("Schlüssel vertrauen und erneut verbinden")}
        </Button>
      </Alert.Content>
    </Alert>
  );
}
