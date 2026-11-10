import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";

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
  const pinned = useRef(false);
  useEffect(() => {
    if (!root.current) return;
    const observer = new IntersectionObserver(
      ([entry]) => {
        if (root.current?.contains(document.activeElement)) return;
        if (!entry.isIntersecting && root.current && visible)
          height.current = root.current.getBoundingClientRect().height;
        setVisible(entry.isIntersecting || pinned.current);
      },
      { rootMargin: "1200px 0px" },
    );
    observer.observe(root.current);
    return () => observer.disconnect();
  }, [visible]);
  useLayoutEffect(() => {
    if (!visible || !root.current) return;
    const observer = new ResizeObserver(() => {
      if (root.current)
        height.current = root.current.getBoundingClientRect().height;
    });
    observer.observe(root.current);
    return () => observer.disconnect();
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
