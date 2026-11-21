import { Button } from "@heroui/react";
import { useEffect, useState } from "react";

import { useAPI } from "./api";
import type { MachineConnectionCheck } from "./generated";
import { useI18n } from "./preferences";
import { HostKeyTrust } from "./ssh-trust";
import { Failure, Inspect, Loading } from "./ui";

export function RepositoryConnection({
  agentId,
  onClose,
}: {
  agentId: string;
  onClose: () => void;
}) {
  const api = useAPI();
  const { t } = useI18n();
  const [attempt, setAttempt] = useState(0);
  const [result, setResult] = useState<MachineConnectionCheck>();
  const [error, setError] = useState("");
  const retry = () => setAttempt((value) => value + 1);

  useEffect(() => {
    const controller = new AbortController();
    setResult(undefined);
    setError("");
    void api
      .query<{ prepareAgent: MachineConnectionCheck }>(
        "mutation($agentId:String!){prepareAgent(agentId:$agentId){connected error hostKey{host fingerprint publicKey repositorySourceId previousFingerprint}}}",
        { agentId },
        controller.signal,
      )
      .then(({ prepareAgent }) => {
        if (!controller.signal.aborted) setResult(prepareAgent);
      })
      .catch((failure: Error) => {
        if (!controller.signal.aborted) setError(failure.message);
      });
    return () => controller.abort();
  }, [api, agentId, attempt]);

  return (
    <Inspect title={t("Git-Verbindung prüfen")} onClose={onClose}>
      {result?.hostKey.publicKey ? (
        <HostKeyTrust
          challenge={result.hostKey}
          onTrusted={async () => retry()}
        />
      ) : result?.connected ? (
        <div className="space-y-3">
          <p role="status">{t("Repository-Verbindung vorbereitet.")}</p>
          <Button variant="secondary" onPress={onClose}>
            {t("Fertig")}
          </Button>
        </div>
      ) : error || result?.error ? (
        <Failure message={error || result?.error} onRetry={retry} />
      ) : (
        <Loading />
      )}
    </Inspect>
  );
}
