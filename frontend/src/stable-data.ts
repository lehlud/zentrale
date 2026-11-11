import { useRef } from "react";

// JSON snapshots often contain fresh objects for unchanged data. Compare fields
// without serializing large message/wire strings or retaining duplicate copies.
export function sameJSONValue(left: unknown, right: unknown): boolean {
  if (Object.is(left, right)) return true;
  if (!left || !right || typeof left !== "object" || typeof right !== "object")
    return false;
  if (Array.isArray(left) !== Array.isArray(right)) return false;
  const a = left as Record<string, unknown>;
  const b = right as Record<string, unknown>;
  const keys = Object.keys(a);
  return (
    keys.length === Object.keys(b).length &&
    keys.every((key) => Object.hasOwn(b, key) && sameJSONValue(a[key], b[key]))
  );
}

export function useStableValue<T>(value: T): T {
  const previous = useRef(value);
  if (!sameJSONValue(previous.current, value)) previous.current = value;
  return previous.current;
}
