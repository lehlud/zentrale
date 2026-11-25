import { Label, TextArea, TextField } from "@heroui/react";

import { useAPI, useQuery } from "./api";
import { useI18n } from "./preferences";
import { Editor, Failure, Inspect, Loading } from "./ui";

export function ModelPolicyDialog({
  providerId,
  onClose,
}: {
  providerId: string;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const api = useAPI();
  const { data, error } = useQuery<{
    modelPolicy: { restricted: boolean; models: string[] };
  }>("query($id:String!){modelPolicy(providerId:$id){restricted models}}", {
    id: providerId,
  });

  if (!data) {
    return (
      <Inspect title={t("Modellfreigabe")} onClose={onClose}>
        {error ? <Failure message={error} /> : <Loading />}
      </Inspect>
    );
  }

  return (
    <Editor
      title={t("Modellfreigabe")}
      initial={{
        restricted: data.modelPolicy.restricted,
        models: data.modelPolicy.models.join("\n"),
      }}
      fields={[
        {
          key: "restricted",
          label: t("Nur freigegebene Modelle verwenden"),
          type: "check",
        },
      ]}
      renderExtra={(value, patch) =>
        value.restricted ? (
          <TextField
            value={String(value.models ?? "")}
            onChange={(models) => patch({ models })}
          >
            <Label>{t("Modell-IDs aus dem Katalog, eine pro Zeile")}</Label>
            <TextArea rows={6} className="font-mono" />
          </TextField>
        ) : null
      }
      onClose={onClose}
      onSave={(value) =>
        api.mutate(
          "mutation($id:String!,$input:ModelPolicyInput!){saveModelPolicy(providerId:$id,input:$input){restricted}}",
          {
            id: providerId,
            input: {
              restricted: Boolean(value.restricted),
              models: String(value.models ?? "")
                .split("\n")
                .map((id) => id.trim())
                .filter(Boolean),
            },
          },
        )
      }
    />
  );
}
