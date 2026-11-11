import { expect, test } from "@playwright/test";
import { mergeMessages } from "../src/transcript-history";
import { sameJSONValue } from "../src/stable-data";
import { transcript } from "./fixtures/visual-data";
import type { Message } from "../src/generated";

function previousMessages(): Message[] {
  return transcript.messages.slice(0, 3).map((message) => ({
    ...message,
    activities: [],
    callId: "",
    change: null,
    changes: [],
    toolName: message.toolName ?? "",
  }));
}

test("unchanged transcript snapshots keep array, message and parsed-call cache identities", () => {
  const previous = previousMessages();
  const snapshot = JSON.parse(JSON.stringify(previous));
  expect(mergeMessages(previous, snapshot)).toBe(previous);
  expect(mergeMessages(previous, [])).toBe(previous);
  const added = {
    ...previous[0],
    id: "new",
    createdAt: "2099-01-01T00:00:00Z",
  };
  const merged = mergeMessages(previous, [...snapshot, added]);
  expect(merged.at(-1)).toBe(added);
  for (const message of previous)
    expect(merged.find((item) => item.id === message.id)).toBe(message);
});

test("changed message data invalidates only its identity, including nested tool updates", () => {
  const previous = previousMessages();
  for (const update of [
    { content: "corrected text" },
    { wire: "changed native response" },
    { callId: "new-call" },
    {
      activities: [
        {
          name: "exec",
          arguments: "{}",
          content: "updated",
          network: false,
          justification: "",
        },
      ],
    },
    { changes: [{ id: "edit", path: "file.ts", patch: "+updated" }] },
  ]) {
    const changed = { ...previous[0], ...update } as (typeof previous)[number];
    const merged = mergeMessages(previous, [changed]);
    expect(merged).not.toBe(previous);
    expect(merged.find((item) => item.id === changed.id)).toBe(changed);
    expect(merged.find((item) => item.id === previous[1].id)).toBe(previous[1]);
  }
});

test("JSON equality distinguishes changes, removed fields, array order and types", () => {
  expect(
    sameJSONValue({ a: [1, { b: "text" }] }, { a: [1, { b: "text" }] }),
  ).toBe(true);
  for (const [left, right] of [
    [{ a: 1 }, { a: 2 }],
    [{ a: 1 }, {}],
    [[], {}],
    [
      [1, 2],
      [2, 1],
    ],
    [null, {}],
    [1, "1"],
  ])
    expect(sameJSONValue(left, right)).toBe(false);
});
