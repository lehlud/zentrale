import { expect, test } from "@playwright/test";
import { newResourceId } from "../src/resource-id";

test("resource IDs are compact, URL-safe and independently generated", () => {
  const seen = new Set<string>();
  for (let index = 0; index < 10000; index++) {
    const id = newResourceId();
    expect(id).toMatch(/^r[A-Za-z0-9_-]{16}$/);
    expect(seen.has(id)).toBe(false);
    seen.add(id);
  }
});
