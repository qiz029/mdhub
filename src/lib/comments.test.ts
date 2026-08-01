import assert from "node:assert/strict";
import test from "node:test";
import { appendComment, CommentAPIError } from "./comments.ts";

test("comment API preserves client errors and hides upstream server details", async () => {
  const originalFetch = globalThis.fetch;
  try {
    globalThis.fetch = async () =>
      Response.json({ error: "thread not found" }, { status: 400 });
    await assert.rejects(
      appendComment("note", { author: "Todd", text: "reply", reply: "missing" }),
      (error) =>
        error instanceof CommentAPIError &&
        error.status === 400 &&
        error.message === "thread not found",
    );

    globalThis.fetch = async () =>
      Response.json({ error: "pq: private database detail" }, { status: 500 });
    await assert.rejects(
      appendComment("note", { author: "Todd", text: "reply", reply: "thread" }),
      (error) =>
        error instanceof CommentAPIError &&
        error.status === 502 &&
        error.message === "comment failed (500)" &&
        !error.message.includes("private"),
    );
  } finally {
    globalThis.fetch = originalFetch;
  }
});
