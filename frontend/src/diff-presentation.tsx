export const diffTone = {
  added: "bg-success-soft text-success-soft-foreground",
  removed: "bg-danger-soft text-danger-soft-foreground",
  hunk: "bg-accent/5 text-accent",
  note: "text-muted",
  context: "text-foreground",
};

export function DiffStats({
  added,
  removed,
}: {
  added: number;
  removed: number;
}) {
  return (
    <span className="inline-flex shrink-0 items-center gap-2">
      <span className={`rounded px-1.5 py-0.5 ${diffTone.added}`}>
        +{added}
      </span>
      <span className={`rounded px-1.5 py-0.5 ${diffTone.removed}`}>
        −{removed}
      </span>
    </span>
  );
}
