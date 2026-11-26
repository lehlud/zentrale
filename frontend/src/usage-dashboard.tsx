import { Input, Label, TextField } from "@heroui/react";
import { useState } from "react";

import { useAPI, useQuery } from "./api";
import { MoreButton, useBatch } from "./batches";
import type { UsageReportRow } from "./generated";
import { useI18n } from "./preferences";
import { Empty, Failure, Loading, Title } from "./ui";
import { UsageChart, usageDays } from "./usage-chart";

type Usage = Pick<
  UsageReportRow,
  | "day"
  | "providerId"
  | "modelId"
  | "userId"
  | "projectId"
  | "purpose"
  | "uncachedTokens"
  | "cachedTokens"
  | "outputTokens"
  | "cacheWriteTokens"
  | "cacheWriteLongTokens"
  | "realCost"
  | "fictionalCost"
  | "unknownPrices"
  | "estimatedCosts"
  | "unknownTokenCounts"
  | "unknownCacheReads"
  | "unknownCacheWrites"
  | "requestCount"
>;

type Report = {
  usageReport: Usage[];
  users?: { id: string; name: string; username: string }[];
  projects: { id: string; name: string }[];
  modelProviders?: { id: string; name: string }[];
};

const fields = `day providerId modelId userId projectId purpose uncachedTokens cachedTokens outputTokens
  cacheWriteTokens cacheWriteLongTokens realCost fictionalCost unknownPrices estimatedCosts unknownTokenCounts
  unknownCacheReads unknownCacheWrites requestCount`;

function utcDay(offset: number) {
  const date = new Date();
  date.setUTCDate(date.getUTCDate() + offset);
  return date.toISOString().slice(0, 10);
}

function sum(rows: Usage[], field: keyof Usage) {
  return rows.reduce((total, row) => total + Number(row[field] ?? 0), 0);
}

function groupRow(groups: Map<string, Usage[]>, key: string, row: Usage) {
  const entries = groups.get(key);
  if (entries) entries.push(row);
  else groups.set(key, [row]);
}

export function UsageDashboard({ admin }: { admin: boolean }) {
  const { t } = useI18n();
  const api = useAPI();
  const [from, setFrom] = useState(() => utcDay(-29));
  const [to, setTo] = useState(() => utcDay(1));
  const [userId, setUserId] = useState("");
  const [projectId, setProjectId] = useState("");
  const [metric, setMetric] = useState<"tokens" | "cost">("tokens");
  const days = usageDays(from, to);
  const valid = days.length > 0;

  const { data, error, loading } = useQuery<Report>(
    `query($from:String,$to:String,$userId:ID,$projectId:ID,$admin:Boolean!){
      usageReport(from:$from,to:$to,userId:$userId,projectId:$projectId){${fields}}
      users @include(if:$admin){id name username}
      projects(limit:500){id name}
      modelProviders{id name}
    }`,
    { from, to, userId: userId || null, projectId: projectId || null, admin },
    { enabled: valid },
  );

  const rows = data?.usageReport ?? [];
  const groups = new Map<string, Usage[]>();

  for (const row of rows) {
    const key = JSON.stringify([
      row.userId,
      row.projectId,
      row.providerId,
      row.modelId,
      row.purpose,
    ]);
    groupRow(groups, key, row);
  }

  const measure = (row: Usage) =>
    metric === "cost"
      ? row.realCost
      : row.uncachedTokens + row.cachedTokens + row.outputTokens;
  const total = (items: Usage[]) =>
    items.reduce((value, row) => value + measure(row), 0);
  const breakdown = useBatch(
    [...groups].sort(([, a], [, b]) => total(b) - total(a)),
    `${from}:${to}:${userId}:${projectId}`,
  );
  const number = new Intl.NumberFormat();
  const usd = new Intl.NumberFormat(undefined, {
    style: "currency",
    currency: "USD",
    maximumFractionDigits: 6,
  });
  const unknownCosts = sum(rows, "unknownPrices");
  const fictionalCost = sum(rows, "fictionalCost");
  const unknownCache =
    sum(rows, "unknownCacheReads") + sum(rows, "unknownCacheWrites");
  const names = {
    userId: new Map(
      data?.users?.map((user) => [user.id, user.name || user.username]),
    ),
    projectId: new Map(
      data?.projects?.map((project) => [project.id, project.name]),
    ),
    providerId: new Map(
      data?.modelProviders?.map((provider) => [provider.id, provider.name]),
    ),
  };

  return (
    <div className="space-y-5">
      <Title
        description={t(
          "Modellnutzung nach Benutzer, Projekt und Zweck. Projekte zählen für ihren Besitzer.",
        )}
      >
        {t("Verbrauch")}
      </Title>
      <div className="flex flex-wrap items-end gap-3">
        <TextField value={from} onChange={setFrom}>
          <Label>{t("Von (UTC)")}</Label>
          <Input type="date" />
        </TextField>
        <TextField value={to} onChange={setTo}>
          <Label>{t("Bis (exklusiv)")}</Label>
          <Input type="date" />
        </TextField>
        {admin && (
          <label className="grid gap-1 text-sm">
            {t("Benutzer")}
            <select
              className="min-h-10 rounded-lg border border-border bg-surface px-2"
              value={userId}
              onChange={(event) => setUserId(event.target.value)}
            >
              <option value="">{t("Alle")}</option>
              {data?.users?.map((user) => (
                <option key={user.id} value={user.id}>
                  {user.name || user.username}
                </option>
              ))}
            </select>
          </label>
        )}
        <label className="grid gap-1 text-sm">
          {t("Projekt")}
          <select
            className="min-h-10 rounded-lg border border-border bg-surface px-2"
            value={projectId}
            onChange={(event) => setProjectId(event.target.value)}
          >
            <option value="">{t("Alle")}</option>
            {data?.projects.map((project) => (
              <option key={project.id} value={project.id}>
                {project.name}
              </option>
            ))}
          </select>
        </label>
      </div>
      <Failure
        message={valid ? error : t("Wähle einen gültigen Zeitraum.")}
        onRetry={() => api.refresh()}
      />
      {loading && <Loading />}
      {valid && data && !rows.length && (
        <Empty>{t("Für diesen Zeitraum gibt es keine Nutzung.")}</Empty>
      )}
      {valid && data && (
        <>
          <div className="grid gap-3 sm:grid-cols-3">
            <Summary
              label={t("Anfragen")}
              value={number.format(sum(rows, "requestCount"))}
            />
            <Summary
              label={t("Cache-Treffer (Tokens)")}
              value={number.format(sum(rows, "cachedTokens"))}
            />
            <Summary
              label={t("Bekannte Kosten")}
              value={usd.format(sum(rows, "realCost"))}
            />
          </div>
          {fictionalCost > 0 && (
            <p className="text-sm text-muted">
              {t(
                "Vergleichswert für Flatrate-Modelle (nicht berechnet): {cost}",
                { cost: usd.format(fictionalCost) },
              )}
            </p>
          )}
          {unknownCache > 0 && (
            <p className="text-sm text-muted">
              {t("Nicht gemeldete Cache-Zähler: {count}", {
                count: number.format(unknownCache),
              })}
            </p>
          )}
          {(unknownCosts > 0 ||
            sum(rows, "unknownTokenCounts") > 0 ||
            sum(rows, "estimatedCosts") > 0) && (
            <p className="text-sm text-muted">
              {t(
                "Unbekannte Preise: {prices} · geschätzte Kosten: {estimates} · unvollständige Tokenzählung: {tokens}",
                {
                  prices: number.format(unknownCosts),
                  estimates: number.format(sum(rows, "estimatedCosts")),
                  tokens: number.format(sum(rows, "unknownTokenCounts")),
                },
              )}
            </p>
          )}
          <UsageChart
            days={days}
            samples={rows.map((row) => ({ ...row, value: measure(row) }))}
            metric={metric}
            onMetricChange={setMetric}
            groupLabel={(group, id) => {
              if (group === "userId" && !admin) return t("Du");
              if (group === "projectId" && !id) return t("Ohne Projekt");
              return group === "modelId" ? id : names[group].get(id) || id;
            }}
          />
          <div className="overflow-x-auto" hidden={!rows.length}>
            <table className="w-full text-left text-sm">
              <thead>
                <tr className="border-b border-border text-muted">
                  {[
                    "Modell / Zweck",
                    "Benutzer / Projekt",
                    "Input",
                    "Cache gelesen",
                    "Cache geschrieben",
                    "Output",
                    "Kosten",
                  ].map((label) => (
                    <th
                      key={label}
                      scope="col"
                      className="px-2 py-2 font-medium"
                    >
                      {t(label)}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {breakdown.items.map(([id, items]) => {
                  const row = items[0];
                  const user = data?.users?.find(
                    (candidate) => candidate.id === row.userId,
                  );
                  const project = data?.projects.find(
                    (candidate) => candidate.id === row.projectId,
                  );
                  return (
                    <tr key={id} className="border-b border-separator">
                      <td className="px-2 py-3">
                        {row.modelId}
                        <div className="text-xs text-muted">
                          {row.providerId} · {row.purpose}
                        </div>
                      </td>
                      <td className="px-2 py-3">
                        {user?.name ||
                          user?.username ||
                          (admin ? row.userId : t("Du"))}
                        <div className="text-xs text-muted">
                          {project?.name || row.projectId || t("Ohne Projekt")}
                        </div>
                      </td>
                      <td className="px-2 py-3 tabular-nums">
                        {number.format(sum(items, "uncachedTokens"))}
                      </td>
                      <td className="px-2 py-3 tabular-nums">
                        {number.format(sum(items, "cachedTokens"))}
                      </td>
                      <td className="px-2 py-3 tabular-nums">
                        {number.format(
                          sum(items, "cacheWriteTokens") +
                            sum(items, "cacheWriteLongTokens"),
                        )}
                      </td>
                      <td className="px-2 py-3 tabular-nums">
                        {number.format(sum(items, "outputTokens"))}
                      </td>
                      <td className="px-2 py-3 tabular-nums">
                        {sum(items, "unknownPrices") ===
                        sum(items, "requestCount")
                          ? "—"
                          : usd.format(sum(items, "realCost"))}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          <MoreButton batch={breakdown} />
        </>
      )}
    </div>
  );
}

function Summary({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-xl border border-border p-4">
      <p className="text-sm text-muted">{label}</p>
      <p className="text-xl font-medium tabular-nums">{value}</p>
    </div>
  );
}
