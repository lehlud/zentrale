import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import {
  observeTranscriptVisibility,
  observeTranscriptHeight,
} from "./transcript-observers";

// Keep measured space in the document, but don't retain expensive Markdown,
// syntax highlighting and disclosure trees for the entire scrollback.
export function VirtualTranscriptItem({
  children,
  estimate = 140,
}: {
  children: () => ReactNode;
  estimate?: number;
}) {
  const root = useRef<HTMLLIElement>(null);
  const height = useRef(estimate);
  const [visible, setVisible] = useState(false);
  const visibleRef = useRef(visible);
  visibleRef.current = visible;
  const pinned = useRef(false);
  useEffect(() => {
    if (!root.current) return;
    return observeTranscriptVisibility(root.current, (entry) => {
      if (root.current?.contains(document.activeElement)) return;
      if (!entry.isIntersecting && visibleRef.current)
        height.current = entry.boundingClientRect.height;
      setVisible(entry.isIntersecting || pinned.current);
    });
  }, []);
  useLayoutEffect(() => {
    if (!visible || !root.current) return;
    return observeTranscriptHeight(root.current, (measured) => {
      height.current = measured;
    });
  }, [visible]);
  return (
    <li
      ref={root}
      data-transcript-item
      data-rendered={visible}
      style={visible ? undefined : { height: height.current }}
      onClick={() => {
        // Preserve a disclosure the reader explicitly opened, including its
        // local expansion/selection state when it leaves the viewport.
        queueMicrotask(() => {
          pinned.current = Boolean(
            root.current?.querySelector('[aria-expanded="true"]'),
          );
        });
      }}
    >
      {visible && (
        <ol role="presentation" className="flow-root min-w-0 list-none">
          {children()}
        </ol>
      )}
    </li>
  );
}
