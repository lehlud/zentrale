import { Button, Popover } from "@heroui/react";
import { useId, useState } from "react";

import { useQuery } from "./api";
import type { UsageSummary } from "./generated";
import { useI18n } from "./preferences";
import { CostSummary } from "./usage-summary";

export function ContextWindow({
  agentId,
  tokens,
  limit,
}: {
  agentId?: string;
  tokens?: number;
  limit?: number | null;
}) {
  const { t, numberLocale } = useI18n();
  const descriptionId = useId();
  const [open, setOpen] = useState(false);
  const used = Math.max(0, tokens ?? 0);
  const fraction = limit ? Math.min(1, used / limit) : 0;
  const percent = new Intl.NumberFormat(numberLocale, {
    style: "percent",
    maximumFractionDigits: 1,
  }).format(fraction);

  return (
    <>
      {limit ? (
        <span id={descriptionId} hidden>
          {t("{percent} belegt", { percent })}
        </span>
      ) : null}
      <Popover isOpen={open} onOpenChange={setOpen}>
        <Button
          isIconOnly
          type="button"
          variant="ghost"
          aria-label={t("Kontextfenster")}
          aria-describedby={limit ? descriptionId : undefined}
          className="min-h-11 min-w-11 shrink-0 text-muted"
        >
          <svg aria-hidden="true" width="22" height="22" viewBox="0 0 22 22">
            <circle
              cx="11"
              cy="11"
              r="8"
              fill="none"
              stroke="currentColor"
              strokeWidth="2.5"
              opacity="0.3"
            />
            {fraction > 0 && (
              <circle
                cx="11"
                cy="11"
                r="8"
                pathLength="100"
                fill="none"
                stroke="currentColor"
                strokeWidth="2.5"
                strokeDasharray={`${Math.max(1, fraction * 100)} 100`}
                transform="rotate(-90 11 11)"
                className="text-accent"
              />
            )}
          </svg>
        </Button>
        <Popover.Content
          placement="top end"
          className="w-72 max-w-[calc(100vw-2rem)]"
        >
          <Popover.Dialog className="max-h-[min(24rem,65dvh)] space-y-3 overflow-auto p-4 text-sm">
            <Popover.Heading className="font-semibold">
              {t("Kontextfenster")}
            </Popover.Heading>
            {limit ? (
              <>
                <div
                  className="h-1.5 overflow-hidden rounded-full bg-default"
                  aria-hidden="true"
                >
                  <div
                    className="h-full rounded-full bg-accent"
                    style={{ width: `${fraction * 100}%` }}
                  />
                </div>
                <dl className="grid grid-cols-[1fr_auto] gap-x-4 gap-y-1 tabular-nums">
                  <dt className="text-muted">{t("Belegt (ca.)")}</dt>
                  <dd className="text-end">
                    {used.toLocaleString(numberLocale)} · {percent}
                  </dd>
                  <dt className="text-muted">{t("Freie Tokens (ca.)")}</dt>
                  <dd className="text-end">
                    {Math.max(0, limit - used).toLocaleString(numberLocale)}
                  </dd>
                  <dt className="text-muted">{t("Limit (Tokens)")}</dt>
                  <dd className="text-end">
                    {limit.toLocaleString(numberLocale)}
                  </dd>
                </dl>
              </>
            ) : (
              <p className="text-muted">
                {t("Für dieses Modell ist keine Kontextgröße bekannt.")}
              </p>
            )}
            <p className="text-xs text-muted">
              {t("Der Kontext wird bei Bedarf automatisch verdichtet.")}
            </p>
            {open && agentId && <ConversationUsage agentId={agentId} />}
          </Popover.Dialog>
        </Popover.Content>
      </Popover>
    </>
  );
}

function ConversationUsage({ agentId }: { agentId: string }) {
  const { data } = useQuery<{ usageSummary: UsageSummary }>(
    `query($id:String!) {
      usageSummary(agentId:$id) {
        inputTokens outputTokens cacheInputTokens cacheReadTokens cacheWriteTokens
        unknownTokenCounts unknownCacheWrites realCost fictionalCost estimated unknownPrices requestCount
      }
    }`,
    { id: agentId },
  );

  return data ? (
    <div className="border-t border-border pt-2">
      <CostSummary totals={data.usageSummary} agentId={agentId} />
    </div>
  ) : null;
}
