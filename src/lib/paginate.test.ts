import assert from "node:assert/strict";
import test from "node:test";
import { paginate } from "./paginate.ts";

const items = Array.from({ length: 25 }, (_, i) => i + 1); // 1..25

test("slices the requested 1-based page", () => {
  assert.deepEqual(paginate(items, 1, 10), {
    pageItems: items.slice(0, 10),
    pageCount: 3,
    page: 1,
  });
  assert.deepEqual(paginate(items, 2, 10).pageItems, items.slice(10, 20));
  assert.deepEqual(paginate(items, 3, 10).pageItems, items.slice(20, 25));
});

test("clamps pages beyond the end back to the last page", () => {
  const page = paginate(items, 99, 10);
  assert.equal(page.page, 3);
  assert.deepEqual(page.pageItems, items.slice(20, 25));
});

test("clamps pages below 1 (and non-numbers) up to the first page", () => {
  assert.equal(paginate(items, 0, 10).page, 1);
  assert.equal(paginate(items, -5, 10).page, 1);
  assert.equal(paginate(items, Number.NaN, 10).page, 1);
});

test("empty lists still report one empty page", () => {
  assert.deepEqual(paginate([], 3, 10), {
    pageItems: [],
    pageCount: 1,
    page: 1,
  });
});

test("an exact multiple produces no dangling empty page", () => {
  const page = paginate(items.slice(0, 20), 2, 10);
  assert.equal(page.pageCount, 2);
  assert.deepEqual(page.pageItems, items.slice(10, 20));
});

test("curation shrinking the list drops an out-of-range page onto the new last page", () => {
  // was on page 3 of 25 items; 20 get dismissed, 5 remain
  const page = paginate(items.slice(0, 5), 3, 10);
  assert.equal(page.page, 1);
  assert.equal(page.pageCount, 1);
  assert.deepEqual(page.pageItems, items.slice(0, 5));
});

test("invalid page sizes fall back to 1 item per page", () => {
  const page = paginate(items, 2, 0);
  assert.equal(page.pageCount, 25);
  assert.deepEqual(page.pageItems, [2]);
});
