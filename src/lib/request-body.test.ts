import assert from "node:assert/strict";
import test from "node:test";

import { readLimitedJSON, RequestBodyError } from "./request-body.ts";

test("readLimitedJSON parses a bounded object", async () => {
  const request = new Request("https://mdhub.invalid/comments", {
    method: "POST",
    body: JSON.stringify({ text: "hello" }),
  });
  assert.deepEqual(await readLimitedJSON(request, 1024), { text: "hello" });
});

test("readLimitedJSON rejects a streamed body over the limit", async () => {
  const request = new Request("https://mdhub.invalid/comments", {
    method: "POST",
    body: "x".repeat(1025),
  });
  await assert.rejects(
    readLimitedJSON(request, 1024),
    (error: unknown) =>
      error instanceof RequestBodyError && error.status === 413,
  );
});

test("readLimitedJSON rejects malformed JSON", async () => {
  const request = new Request("https://mdhub.invalid/comments", {
    method: "POST",
    body: "{",
  });
  await assert.rejects(
    readLimitedJSON(request, 1024),
    (error: unknown) =>
      error instanceof RequestBodyError && error.status === 400,
  );
});
