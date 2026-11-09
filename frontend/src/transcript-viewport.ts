import { useLayoutEffect, useRef, useState } from "react";

// The document is the transcript's only vertical scroll container. Keep space
// for the fixed composer, and follow new output only while the reader is at the end.
export function useTranscriptViewport(suspended: boolean, ready: boolean) {
  const root = useRef<HTMLElement>(null);
  const composer = useRef<HTMLFormElement>(null);
  const header = useRef<HTMLDivElement>(null);
  const followingRef = useRef(true);
  const [following, setFollowing] = useState(true);
  const latest = () => {
    followingRef.current = true;
    setFollowing(true);
    window.scrollTo(0, document.documentElement.scrollHeight);
  };
  useLayoutEffect(() => {
    const update = () => {
      const distance =
        document.documentElement.scrollHeight - innerHeight - scrollY;
      followingRef.current = distance < 100;
      setFollowing(followingRef.current);
    };
    window.addEventListener("scroll", update, { passive: true });
    return () => window.removeEventListener("scroll", update);
  }, []);
  useLayoutEffect(() => {
    if (!ready) return;
    const observer = new ResizeObserver(() => {
      if (root.current && composer.current) {
        const space = `${composer.current.getBoundingClientRect().height + 24}px`;
        root.current.style.paddingBottom = space;
        root.current.style.setProperty("--composer-space", space);
      }
      if (root.current && header.current) {
        root.current.style.setProperty(
          "--chat-header-space",
          `${header.current.getBoundingClientRect().height + 12}px`,
        );
      }
      if (followingRef.current && !suspended)
        window.scrollTo(0, document.documentElement.scrollHeight);
    });
    if (composer.current) observer.observe(composer.current);
    if (header.current) observer.observe(header.current);
    if (root.current) observer.observe(root.current);
    return () => observer.disconnect();
  }, [suspended, ready]);
  useLayoutEffect(() => {
    if (followingRef.current && !suspended)
      window.scrollTo(0, document.documentElement.scrollHeight);
  });
  return { root, composer, header, following, latest };
}
