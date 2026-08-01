import assert from "node:assert/strict";
import test from "node:test";

import { parseSearchSnippet } from "./search-snippet.ts";

test("parseSearchSnippet preserves only mark semantics", () => {
  assert.deepEqual(
    parseSearchSnippet("before &lt;b&gt;<mark>term</mark> after"),
    [
      { text: "before <b>", highlighted: false },
      { text: "term", highlighted: true },
      { text: " after", highlighted: false },
    ],
  );
});

test("parseSearchSnippet treats injected elements as text", () => {
  assert.deepEqual(parseSearchSnippet('<img src=x onerror="alert(1)">'), [
    { text: '<img src=x onerror="alert(1)">', highlighted: false },
  ]);
});

test("parseSearchSnippet tolerates invalid numeric entities", () => {
  assert.deepEqual(parseSearchSnippet("&#99999999;"), [
    { text: "\uFFFD", highlighted: false },
  ]);
});
