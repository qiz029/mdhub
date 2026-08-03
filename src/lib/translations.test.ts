import assert from "node:assert/strict";
import test from "node:test";
import { translationProgress, translationViewHref } from "./translations.ts";

test("translationProgress uses persisted chunks and completes terminal drafts", () => {
  assert.equal(
    translationProgress({ state: "queued", progress_current: 0, progress_total: 0 }),
    0,
  );
  assert.equal(
    translationProgress({ state: "translating", progress_current: 3, progress_total: 4 }),
    75,
  );
  assert.equal(
    translationProgress({ state: "draft_ready", progress_current: 4, progress_total: 4 }),
    100,
  );
});

test("translationViewHref encodes every output slug segment", () => {
  assert.equal(
    translationViewHref("_translations/a b"),
    "/view/_translations/a%20b",
  );
});
