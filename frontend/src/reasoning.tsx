import { Label, ListBox, Select } from "@heroui/react";
import { useQuery } from "./api";
import type { LanguageModel, ModelChoice } from "./generated";
import { useI18n } from "./preferences";
import { Failure } from "./ui";

export function reasoningValue(levels: string[], requested: string) {
  if (!levels.length) return "default";
  if (requested === "regular" || requested === "default") requested = "medium";
  if (levels.includes(requested)) return requested;
  if (requested === "xhigh" || requested === "high") return levels.at(-1)!;
  if (requested === "medium") return levels[0];
  return "default";
}

export function ReasoningSelect({
  choice,
  onChange,
}: {
  choice: ModelChoice;
  onChange: (choice: ModelChoice) => void;
}) {
  const { t } = useI18n();
  const { data, error, loading } = useQuery<{ model: LanguageModel | null }>(
    "query($providerId:String!,$modelId:String!){model(providerId:$providerId,modelId:$modelId){id reasoningLevels}}",
    choice,
  );
  const levels = data?.model?.reasoningLevels ?? [];
  const value = reasoningValue(levels, choice.reasoningLevel || "xhigh");
  return (
    <div className="space-y-2">
      <Failure message={error} />
      <Select
        aria-label={t("Denkintensität")}
        value={value}
        isDisabled={loading || !levels.length}
        onChange={(level) =>
          onChange({ ...choice, reasoningLevel: String(level) })
        }
      >
        <Label>{t("Denkintensität")}</Label>
        <Select.Trigger className="min-h-11">
          <Select.Value />
          <Select.Indicator />
        </Select.Trigger>
        <Select.Popover>
          <ListBox>
            {["default", ...levels].map((level) => (
              <ListBox.Item
                key={level}
                id={level}
                textValue={
                  level === "default" ? t("Standard (regulär)") : level
                }
              >
                <Label>
                  {level === "default" ? t("Standard (regulär)") : level}
                </Label>
                <ListBox.ItemIndicator />
              </ListBox.Item>
            ))}
          </ListBox>
        </Select.Popover>
      </Select>
      {!loading && !levels.length && (
        <p className="text-xs text-muted">
          {t(
            "Dieses Modell verwendet die Standard-Denkintensität des Providers.",
          )}
        </p>
      )}
    </div>
  );
}
