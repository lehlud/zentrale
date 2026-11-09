import { Button } from "@heroui/react";
import { useEffect, useState } from "react";

import { useI18n } from "./preferences";

type Batch<T> = { items: T[]; hasMore: boolean; showMore: () => void };

export function useBatch<T>(
  items: T[],
  filterKey = "",
  batchSize = 12,
): Batch<T> {
  const [visibleCount, setVisibleCount] = useState(batchSize);

  useEffect(() => setVisibleCount(batchSize), [filterKey, batchSize]);

  return {
    items: items.slice(0, visibleCount),
    hasMore: visibleCount < items.length,
    showMore: () => setVisibleCount((count) => count + batchSize),
  };
}

export function MoreButton({
  batch,
}: {
  batch: { hasMore: boolean; showMore: () => void };
}) {
  const { t } = useI18n();
  if (!batch.hasMore) {
    return null;
  }

  return (
    <div className="flex justify-center py-4">
      <Button className="min-h-11" variant="secondary" onPress={batch.showMore}>
        {t("Mehr anzeigen")}
      </Button>
    </div>
  );
}
