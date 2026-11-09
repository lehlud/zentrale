import { useAPI } from "./api";
import type { LanguageModel } from "./generated";
import { useI18n } from "./preferences";
import { Editor, type Field } from "./ui";

export function ModelMetadataEditor({
  model,
  onClose,
}: {
  model: LanguageModel;
  onClose: () => void;
}) {
  const api = useAPI();
  const { t } = useI18n();
  const fields: Field[] = [
    {
      key: "contextWindow",
      label: t("Kontextfenster (Tokens)"),
      type: "number",
      required: true,
    },
    {
      key: "inputPrice",
      label: t("Input: USD / 1 Mio. Tokens"),
      type: "number",
    },
    {
      key: "outputPrice",
      label: t("Output: USD / 1 Mio. Tokens"),
      type: "number",
    },
    {
      key: "cachedPrice",
      label: t("Cache: USD / 1 Mio. Tokens"),
      type: "number",
    },
    {
      key: "pricesKnown",
      label: t("Diese Preise für Kostenschätzungen verwenden"),
      type: "check",
    },
  ];

  return (
    <Editor
      title={`${t("Modell-Metadaten ergänzen")} · ${model.name}`}
      fields={fields}
      initial={{
        contextWindow: model.contextWindow,
        inputPrice: model.inputPrice,
        outputPrice: model.outputPrice,
        cachedPrice: model.cachedPrice,
        pricesKnown: model.pricesKnown,
      }}
      onClose={onClose}
      onSave={(input) =>
        api.mutate(
          "mutation($providerId:String!,$modelId:String!,$input:ModelMetadataInput!){saveModelMetadata(providerId:$providerId,modelId:$modelId,input:$input)}",
          { providerId: model.providerId, modelId: model.id, input },
        )
      }
    />
  );
}
