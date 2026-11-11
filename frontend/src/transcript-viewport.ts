import { useCallback, useLayoutEffect, useRef, useState } from "react";

// The document is the transcript's only vertical scroll container. Keep space
// for the fixed composer, and follow new output only while the reader is at the end.
export function useTranscriptViewport(suspended: boolean, ready: boolean) {
  const root = useRef<HTMLElement>(null);
  const composer = useRef<HTMLFormElement>(null);
  const header = useRef<HTMLDivElement>(null);
  const followingRef = useRef(true);
  const [following, setFollowing] = useState(true);
  const latest = useCallback(() => {
    followingRef.current = true;
    setFollowing(true);
    window.scrollTo(0, document.documentElement.scrollHeight);
  }, []);

  useLayoutEffect(() => {
    let previousY = scrollY;
    const update = () => {
      const distance =
        document.documentElement.scrollHeight - innerHeight - scrollY;
      // A new/expanded row can increase scrollHeight before our resize callback
      // follows it. That is not the reader scrolling away from the bottom.
      if (distance < 100) followingRef.current = true;
      else if (scrollY < previousY - 1) followingRef.current = false;
      previousY = scrollY;
      setFollowing(followingRef.current);
    };
    window.addEventListener("scroll", update, { passive: true });

    return () => window.removeEventListener("scroll", update);
  }, []);

  useLayoutEffect(() => {
    if (!ready) {
      return;
    }

    let frame = 0;
    let composerHeight: number | undefined;
    let headerHeight: number | undefined;
    const observer = new ResizeObserver((entries) => {
      for (const entry of entries) {
        const height =
          entry.borderBoxSize[0]?.blockSize ??
          entry.target.getBoundingClientRect().height;
        if (entry.target === composer.current) composerHeight = height;
        if (entry.target === header.current) headerHeight = height;
      }
      if (frame) return;
      // Coalesce streaming, row measurements and composer resize into one frame.
      // Ordinary React renders/keystrokes do not read the document's layout.
      frame = requestAnimationFrame(() => {
        frame = 0;
        if (root.current && composerHeight !== undefined) {
          const space = `${composerHeight + 24}px`;
          if (root.current.style.paddingBottom !== space) {
            root.current.style.paddingBottom = space;
            root.current.style.setProperty("--composer-space", space);
          }
        }
        if (root.current && headerHeight !== undefined) {
          const space = `${headerHeight + 12}px`;
          if (
            root.current.style.getPropertyValue("--chat-header-space") !== space
          )
            root.current.style.setProperty("--chat-header-space", space);
        }
        if (
          followingRef.current &&
          !suspended &&
          document.documentElement.scrollHeight - innerHeight - scrollY > 1
        )
          window.scrollTo(0, document.documentElement.scrollHeight);
      });
    });
    if (composer.current) observer.observe(composer.current);

    if (header.current) observer.observe(header.current);

    if (root.current) observer.observe(root.current);

    return () => {
      observer.disconnect();
      cancelAnimationFrame(frame);
    };
  }, [suspended, ready]);

  return { root, composer, header, following, latest };
}
