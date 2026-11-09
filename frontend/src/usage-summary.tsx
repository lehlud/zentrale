import type { UsageSummary } from "./generated";
import { useI18n } from "./preferences";

export function CostSummary({ totals }: { totals: UsageSummary }) {
  const { t, numberLocale } = useI18n();
  if (!totals.requestCount) return null;
  return (
    <details className="font-mono text-[11px] text-muted">
      <summary className="min-h-6 cursor-pointer py-1">
        {totals.unknownTokenCounts ? "≈ " : ""}
        {(totals.inputTokens + totals.outputTokens).toLocaleString(
          numberLocale,
        )}{" "}
        {t("Tokens")}
        {" · "}
        {totals.estimated ? "≈ " : ""}${totals.realCost.toFixed(4)}
      </summary>
      <div className="flex flex-wrap gap-x-4 gap-y-1 py-2">
        <span>
          {totals.estimated ? "≈ " : ""}${totals.realCost.toFixed(4)}{" "}
          {t("real")}
        </span>
        <span>
          {totals.estimated ? "≈ " : ""}${totals.fictionalCost.toFixed(4)}{" "}
          {t("fiktiv")}
        </span>
        <span>
          {totals.unknownTokenCounts ? "≈ " : ""}
          {(totals.inputTokens + totals.outputTokens).toLocaleString(
            numberLocale,
          )}{" "}
          {t("Tokens")}
        </span>
        {totals.cacheInputTokens > 0 ? (
          <span>
            {Math.round(
              (totals.cacheReadTokens / totals.cacheInputTokens) * 100,
            )}
            % {t("Cache gelesen (gemeldete Tokens)")}
          </span>
        ) : (
          <span>{t("Cache-Leseanteil nicht gemeldet")}</span>
        )}
        <span>
          {totals.cacheWriteTokens.toLocaleString(numberLocale)}{" "}
          {t("Cache-Tokens geschrieben")}
        </span>
        {totals.unknownTokenCounts > 0 && (
          <span>
            {t("Tokenzahlen geschätzt oder unbekannt: {count} Anfragen", {
              count: totals.unknownTokenCounts,
            })}
          </span>
        )}
        {totals.unknownCacheWrites > 0 && (
          <span>
            {t("Cache-Schreibdaten fehlen für {count} Anfragen", {
              count: totals.unknownCacheWrites,
            })}
          </span>
        )}
        {totals.unknownPrices > 0 && (
          <span>
            {t("Kosten unvollständig für {count} Anfragen", {
              count: totals.unknownPrices,
            })}
          </span>
        )}
      </div>
    </details>
  );
}
