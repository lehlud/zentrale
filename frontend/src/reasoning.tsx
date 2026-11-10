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
  role = "main",
}: {
  choice: ModelChoice;
  onChange: (choice: ModelChoice) => void;
  role?: "main" | "approver";
}) {
  const { t } = useI18n();
  const { data, error, loading } = useQuery<{ model: LanguageModel | null }>(
    "query($providerId:String!,$modelId:String!){model(providerId:$providerId,modelId:$modelId){id reasoningLevels}}",
    { providerId: choice.providerId, modelId: choice.modelId },
  );
  const levels = data?.model?.reasoningLevels ?? [];
  const knownLevels = levels.length > 0;
  const defaultLabel = t(
    knownLevels ? "Standard (regulär)" : "Provider-Standard",
  );
  const options = knownLevels
    ? levels
    : ["minimal", "low", "medium", "high", "xhigh"];
  const value = knownLevels
    ? reasoningValue(
        levels,
        choice.reasoningLevel || (role === "approver" ? "medium" : "xhigh"),
      )
    : choice.reasoningLevel === "regular"
      ? "medium"
      : choice.reasoningLevel || "default";
  return (
    <div className="space-y-2">
      <Failure message={error} />
      <Select
        aria-label={t("Denkintensität")}
        value={value}
        isDisabled={loading || !data?.model}
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
            {["default", ...options].map((level) => (
              <ListBox.Item
                key={level}
                id={level}
                textValue={level === "default" ? defaultLabel : level}
              >
                <Label>{level === "default" ? defaultLabel : level}</Label>
                <ListBox.ItemIndicator />
              </ListBox.Item>
            ))}
          </ListBox>
        </Select.Popover>
      </Select>
      {!loading && data?.model && !knownLevels && (
        <p className="text-xs text-muted">
          {t(
            "Die unterstützten Denkstufen dieses Modells sind nicht bekannt. Du kannst sie manuell setzen; der Provider kann nicht unterstützte Werte ablehnen.",
          )}
        </p>
      )}
    </div>
  );
}
