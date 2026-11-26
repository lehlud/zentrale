import { Button } from "@heroui/react";
import { useState } from "react";

import { useI18n } from "./preferences";

const groupOptions = [
  ["userId", "Benutzer"],
  ["projectId", "Projekt"],
  ["modelId", "Modell"],
  ["providerId", "Provider"],
] as const;

type Group = (typeof groupOptions)[number][0];
type Metric = "tokens" | "cost";
type Sample = Record<Group, string> & { day: string; value: number };

const dayMS = 24 * 60 * 60 * 1000;

export function usageDays(from: string, to: string) {
  const start = Date.parse(`${from}T00:00:00Z`);
  const count = (Date.parse(`${to}T00:00:00Z`) - start) / dayMS;
  if (!Number.isInteger(count) || count <= 0 || count > 366) return [];

  return Array.from({ length: count }, (_, index) =>
    new Date(start + index * dayMS).toISOString().slice(0, 10),
  );
}

function stackedDays(days: string[], samples: Sample[], group: Group) {
  const byDay = new Map(days.map((day) => [day, new Map<string, number>()]));
  const groups = new Set<string>();

  for (const row of samples) {
    const segments = byDay.get(row.day);
    if (!segments || row.value <= 0) continue;

    const id = row[group];
    groups.add(id);
    segments.set(id, (segments.get(id) ?? 0) + row.value);
  }

  const stacks = days.map((day) => {
    const segments = byDay.get(day)!;
    const total = [...segments.values()].reduce((a, b) => a + b, 0);
    return { day, total, segments };
  });

  return { stacks, groups: [...groups] };
}

export function UsageChart({
  days,
  samples,
  groupLabel,
  metric,
  onMetricChange,
}: {
  days: string[];
  samples: Sample[];
  groupLabel: (group: Group, id: string) => string;
  metric: Metric;
  onMetricChange: (metric: Metric) => void;
}) {
  const { t, locale } = useI18n();
  const [group, setGroup] = useState<Group>("userId");
  const { stacks, groups } = stackedDays(days, samples, group);
  const highest = Math.max(0, ...stacks.map(({ total }) => total));
  const number = new Intl.NumberFormat(
    locale,
    metric === "cost"
      ? {
          style: "currency",
          currency: "USD",
          maximumFractionDigits: 6,
        }
      : undefined,
  );
  const date = new Intl.DateTimeFormat(locale, {
    month: "short",
    day: "numeric",
    timeZone: "UTC",
  });
  const colors = [
    "#2676db",
    "#10a37f",
    "#d5831a",
    "#986be7",
    "#d44e79",
    "#648239",
    "#678097",
    "#b75a39",
  ];
  const series = groups
    .map((id) => ({ id, label: groupLabel(group, id) }))
    .sort(
      (a, b) =>
        a.label.localeCompare(b.label, locale) || a.id.localeCompare(b.id),
    )
    .map((item, index) => ({ ...item, color: colors[index % colors.length] }));
  const tickStep = Math.max(1, Math.ceil(days.length / 6));

  return (
    <section
      aria-label={t("Verbrauch pro Tag")}
      className="min-w-0 space-y-4 rounded-xl border border-border p-4"
    >
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div className="flex gap-2">
          <Button
            size="sm"
            aria-pressed={metric === "tokens"}
            variant={metric === "tokens" ? "primary" : "ghost"}
            onPress={() => onMetricChange("tokens")}
          >
            {t("Tokens")}
          </Button>
          <Button
            size="sm"
            aria-pressed={metric === "cost"}
            variant={metric === "cost" ? "primary" : "ghost"}
            onPress={() => onMetricChange("cost")}
          >
            {t("Kosten")}
          </Button>
        </div>
        <label className="grid gap-1 text-sm">
          {t("Aufschlüsselung")}
          <select
            value={group}
            onChange={(event) => setGroup(event.target.value as Group)}
            className="min-h-10 rounded-lg border border-border bg-surface px-2"
          >
            {groupOptions.map(([id, label]) => (
              <option key={id} value={id}>
                {t(label)}
              </option>
            ))}
          </select>
        </label>
      </div>

      <div className="overflow-x-auto pb-1">
        <div style={{ minWidth: Math.max(240, days.length * 8) }}>
          <div className="flex h-36 items-end gap-0.5 border-b border-separator sm:gap-1">
            {stacks.map(({ day, total, segments }) => {
              const entries = series.flatMap((item) => {
                const value = segments.get(item.id);
                return value === undefined ? [] : [{ ...item, value }];
              });
              const label = [
                `${day}: ${number.format(total)}`,
                ...entries.map(
                  ({ label, value }) => `${label}: ${number.format(value)}`,
                ),
              ].join(" · ");

              return (
                <div
                  key={day}
                  role="img"
                  aria-label={label}
                  title={label}
                  data-usage-day={day}
                  className="flex h-full min-w-0 flex-1 flex-col justify-end"
                >
                  {total > 0 ? (
                    <div
                      className="flex flex-col-reverse overflow-hidden rounded-t-sm"
                      style={{ height: `${(100 * total) / highest}%` }}
                    >
                      {entries.map(({ id, color, value }) => (
                        <span
                          key={id}
                          data-usage-segment={id}
                          style={{
                            height: `${(100 * value) / total}%`,
                            backgroundColor: color,
                          }}
                        />
                      ))}
                    </div>
                  ) : (
                    <span
                      aria-hidden="true"
                      className="h-0.5 rounded bg-separator"
                    />
                  )}
                </div>
              );
            })}
          </div>
          <div
            className="flex justify-between gap-1 pt-1 text-xs text-muted"
            aria-hidden="true"
          >
            {days
              .filter(
                (_, index) =>
                  (index % tickStep === 0 && index < days.length - 1) ||
                  index === days.length - 1,
              )
              .map((day) => (
                <span key={day}>
                  {date.format(new Date(`${day}T00:00:00Z`))}
                </span>
              ))}
          </div>
        </div>
      </div>

      {series.length > 0 && (
        <ul
          aria-label={t("Legende")}
          className="flex flex-wrap gap-x-4 gap-y-2 text-xs text-muted"
        >
          {series.map(({ id, label, color }) => (
            <li
              key={id}
              className="flex min-w-0 items-center gap-1.5 break-all"
            >
              <span
                className="size-2.5 shrink-0 rounded-sm"
                style={{ backgroundColor: color }}
                aria-hidden="true"
              />
              {label}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
