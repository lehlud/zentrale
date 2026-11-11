// One browser observer per purpose, not one or two native observers per message.
// Entries are delivered together and React can batch the visibility changes.
const intersections = new Map<
  Element,
  (entry: IntersectionObserverEntry) => void
>();
let intersection: IntersectionObserver | undefined;
export function observeTranscriptVisibility(
  element: Element,
  callback: (entry: IntersectionObserverEntry) => void,
) {
  intersection ??= new IntersectionObserver(
    (entries) => {
      for (const entry of entries) intersections.get(entry.target)?.(entry);
    },
    { rootMargin: "1200px 0px" },
  );
  intersections.set(element, callback);
  intersection.observe(element);
  return () => {
    intersection?.unobserve(element);
    intersections.delete(element);
    if (!intersections.size) {
      intersection?.disconnect();
      intersection = undefined;
    }
  };
}

const measurements = new Map<Element, (height: number) => void>();
let resize: ResizeObserver | undefined;
export function observeTranscriptHeight(
  element: Element,
  callback: (height: number) => void,
) {
  resize ??= new ResizeObserver((entries) => {
    for (const entry of entries) {
      // borderBoxSize is already measured by the browser: no forced layout read.
      const height =
        entry.borderBoxSize[0]?.blockSize ??
        entry.target.getBoundingClientRect().height;
      measurements.get(entry.target)?.(height);
    }
  });
  measurements.set(element, callback);
  resize.observe(element);
  return () => {
    resize?.unobserve(element);
    measurements.delete(element);
    if (!measurements.size) {
      resize?.disconnect();
      resize = undefined;
    }
  };
}
