import assert from "node:assert/strict";
import test from "node:test";
import {
  CommentRequestError,
  normalizeCommentRequest,
} from "./comment-request.ts";

test("normalizes a new anchored comment", () => {
  assert.deepEqual(
    normalizeCommentRequest({
      slug: " notes/example ",
      text: " hello ",
      anchor: { quote: " quote ", prefix: "before", suffix: "after" },
    }),
    {
      slug: "notes/example",
      comment: {
        author: "用户",
        text: "hello",
        anchor: { quote: "quote", prefix: "before", suffix: "after" },
      },
    },
  );
});

test("normalizes a reply without requiring an anchor", () => {
  assert.deepEqual(
    normalizeCommentRequest({
      slug: "note",
      author: "Agent",
      text: "reply",
      reply: "thread-id",
    }),
    {
      slug: "note",
      comment: { author: "Agent", text: "reply", reply: "thread-id" },
    },
  );
});

test("rejects non-string fields instead of throwing incidental type errors", () => {
  for (const input of [
    { slug: {}, text: "hello", anchor: { quote: "quote" } },
    { slug: "note", text: 42, anchor: { quote: "quote" } },
    { slug: "note", text: "hello", anchor: { quote: 42 } },
  ]) {
    assert.throws(
      () => normalizeCommentRequest(input),
      (error) =>
        error instanceof CommentRequestError && error.status === 400,
    );
  }
});

test("applies limits by Unicode code point", () => {
  const emoji = "😀";
  assert.doesNotThrow(() =>
    normalizeCommentRequest({
      slug: "note",
      text: emoji.repeat(2_000),
      reply: "thread",
    }),
  );
  assert.throws(() =>
    normalizeCommentRequest({
      slug: "note",
      text: emoji.repeat(2_001),
      reply: "thread",
    }),
  );
});
