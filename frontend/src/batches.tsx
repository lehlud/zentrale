import { useEffect, useRef, useState } from "react";

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
  const sentinel = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!batch.hasMore || !sentinel.current) return;
    let requested = false;
    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting && !requested) {
          requested = true;
          batch.showMore();
        }
      },
      { rootMargin: "100px" },
    );
    observer.observe(sentinel.current);
    return () => observer.disconnect();
  }, [batch.hasMore, batch.showMore]);
  if (!batch.hasMore) {
    return null;
  }

  return <div ref={sentinel} aria-hidden="true" className="h-px" />;
}
