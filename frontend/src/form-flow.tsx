import { ProgressBar } from "@heroui/react";
import { useEffect, useRef } from "react";

import { useI18n } from "./preferences";

export function FlowProgress({
  index,
  total,
  title,
  description,
}: {
  index: number;
  total: number;
  title: string;
  description?: string;
}) {
  const { t } = useI18n();
  const heading = useRef<HTMLHeadingElement>(null);
  const previous = useRef(index);

  useEffect(() => {
    if (previous.current === index) {
      return;
    }

    previous.current = index;
    const body = heading.current?.closest('[data-slot="modal-body"]');
    if (body) body.scrollTop = 0;

    heading.current?.focus({ preventScroll: true });
  }, [index]);

  const progress = t("Schritt {current} von {total}", {
    current: index + 1,
    total,
  });

  return (
    <div className="space-y-3">
      <ProgressBar
        aria-label={t("Einrichtungsschritte")}
        value={index + 1}
        maxValue={total}
        valueLabel={progress}
        size="sm"
      >
        <ProgressBar.Track>
          <ProgressBar.Fill />
        </ProgressBar.Track>
      </ProgressBar>
      <div className="space-y-1.5">
        <p className="text-xs text-muted">{progress}</p>
        <h2
          ref={heading}
          tabIndex={-1}
          className="text-base font-medium outline-none"
        >
          {title}
        </h2>
        {description && (
          <p className="text-sm leading-6 text-muted">{description}</p>
        )}
      </div>
    </div>
  );
}
