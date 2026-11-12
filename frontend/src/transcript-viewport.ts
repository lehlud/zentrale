import { useCallback, useLayoutEffect, useRef, useState } from "react";

function nestedScrollConsumes(target: EventTarget | null, delta: number) {
  let element = target instanceof Element ? target : null;
  while (
    element &&
    element !== document.body &&
    element !== document.documentElement
  ) {
    if (element.scrollHeight > element.clientHeight + 1) {
      const overflow = getComputedStyle(element).overflowY;
      if (
        (overflow === "auto" || overflow === "scroll") &&
        (delta < 0
          ? element.scrollTop > 0
          : element.scrollTop + element.clientHeight < element.scrollHeight - 1)
      )
        return true;
    }
    element = element.parentElement;
  }
  return false;
}

// The document is the transcript's only vertical scroll container. Keep space
// for the fixed composer, and follow new output only while the reader is at the end.
export function useTranscriptViewport(suspended: boolean, ready: boolean) {
  const root = useRef<HTMLElement>(null);
  const composer = useRef<HTMLFormElement>(null);
  const readonlyBar = useRef<HTMLElement>(null);
  const header = useRef<HTMLDivElement>(null);
  const followingRef = useRef(true);
  const direction = useRef(0);
  const pointerDown = useRef(false);
  const position = useRef({ y: 0, height: 0 });
  const [following, setFollowing] = useState(true);
  const latest = useCallback(() => {
    direction.current = 0;
    followingRef.current = true;
    setFollowing(true);
    window.scrollTo(0, document.documentElement.scrollHeight);
    position.current = {
      y: scrollY,
      height: document.documentElement.scrollHeight,
    };
  }, []);

  useLayoutEffect(() => {
    position.current = {
      y: scrollY,
      height: document.documentElement.scrollHeight,
    };
    const update = () => {
      const height = document.documentElement.scrollHeight;
      const distance = height - innerHeight - scrollY;
      // A new/expanded row can increase scrollHeight before our resize callback
      // follows it. That is not the reader scrolling away from the bottom.
      if (
        scrollY < position.current.y - 1 &&
        (height === position.current.height ||
          scrollY === 0 ||
          direction.current < 0 ||
          pointerDown.current)
      )
        followingRef.current = false;
      else if (
        distance <= 2 &&
        direction.current >= 0 &&
        scrollY >= position.current.y
      )
        followingRef.current = true;
      position.current = { y: scrollY, height };
      setFollowing(followingRef.current);
    };
    // Latch the reader's intent BEFORE scrolling/layout/ResizeObserver callbacks.
    // Virtualized rows can change document height in the same frame; comparing
    // scroll positions alone mistakes that real upward scroll for anchoring.
    const intent = (target: EventTarget | null, delta: number) => {
      if (!delta || nestedScrollConsumes(target, delta)) return;
      direction.current = Math.sign(delta);
      if (delta < 0) {
        followingRef.current = false;
        setFollowing(false);
      }
    };
    const wheel = (event: WheelEvent) => {
      if (!event.defaultPrevented && !event.ctrlKey)
        intent(event.target, event.deltaY);
    };
    const key = (event: KeyboardEvent) => {
      if (
        event.defaultPrevented ||
        (event.target instanceof Element &&
          event.target.closest(
            "input, textarea, select, button, a, [contenteditable], [role=textbox]",
          ))
      )
        return;
      if (
        ["ArrowUp", "PageUp", "Home"].includes(event.key) ||
        (event.key === " " && event.shiftKey)
      )
        intent(event.target, -1);
      else if (["ArrowDown", "PageDown", "End", " "].includes(event.key))
        intent(event.target, 1);
    };
    let touchY: number | undefined;
    const touchStart = (event: TouchEvent) => {
      touchY = event.touches[0]?.clientY;
    };
    const touchMove = (event: TouchEvent) => {
      const y = event.touches[0]?.clientY;
      if (!event.defaultPrevented && y !== undefined && touchY !== undefined)
        intent(event.target, touchY - y);
      touchY = y;
    };
    const down = () => {
      pointerDown.current = true;
      direction.current = 0;
    };
    const up = () => {
      pointerDown.current = false;
    };
    window.addEventListener("scroll", update, { passive: true });
    window.addEventListener("wheel", wheel, { passive: true });
    window.addEventListener("keydown", key);
    window.addEventListener("touchstart", touchStart, { passive: true });
    window.addEventListener("touchmove", touchMove, { passive: true });
    window.addEventListener("pointerdown", down, { passive: true });
    window.addEventListener("pointerup", up, { passive: true });
    window.addEventListener("pointercancel", up, { passive: true });

    return () => {
      window.removeEventListener("scroll", update);
      window.removeEventListener("wheel", wheel);
      window.removeEventListener("keydown", key);
      window.removeEventListener("touchstart", touchStart);
      window.removeEventListener("touchmove", touchMove);
      window.removeEventListener("pointerdown", down);
      window.removeEventListener("pointerup", up);
      window.removeEventListener("pointercancel", up);
    };
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
        if (
          entry.target === composer.current ||
          entry.target === readonlyBar.current
        )
          composerHeight = height;
        if (entry.target === header.current) headerHeight = height;
      }
      if (frame) return;
      // Coalesce streaming, row measurements and composer resize into one frame.
      // Ordinary React renders/keystrokes do not read the document's layout.
      frame = requestAnimationFrame(() => {
        frame = 0;
        // scroll events can be delivered after this animation frame. Preserve
        // a reader's upward scroll even in that ordering, but ignore browser
        // scroll anchoring caused by a simultaneous row measurement/resize.
        const height = document.documentElement.scrollHeight;
        if (
          scrollY < position.current.y - 1 &&
          (height === position.current.height ||
            scrollY === 0 ||
            direction.current < 0 ||
            pointerDown.current)
        ) {
          followingRef.current = false;
          setFollowing(false);
        }
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
        ) {
          window.scrollTo(0, document.documentElement.scrollHeight);
          position.current = {
            y: scrollY,
            height: document.documentElement.scrollHeight,
          };
        }
      });
    });
    if (composer.current) observer.observe(composer.current);
    if (readonlyBar.current) observer.observe(readonlyBar.current);

    if (header.current) observer.observe(header.current);

    if (root.current) observer.observe(root.current);

    return () => {
      observer.disconnect();
      cancelAnimationFrame(frame);
    };
  }, [suspended, ready]);

  return { root, composer, readonlyBar, header, following, latest };
}
