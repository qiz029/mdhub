import assert from "node:assert/strict";
import test from "node:test";
import {
  bountyAgeDays,
  bountyAgeLabel,
  isOpenBounty,
} from "./play.ts";
import type { Collision } from "./sparks.ts";

const DAY = 24 * 60 * 60 * 1000;

function collision(overrides: Partial<Collision>): Collision {
  return {
    id: 42,
    slug_a: "notes/a",
    slug_b: "_sparks/20260701-100000-abcdef",
    title_a: "笔记 A",
    title_b: "碎片 B",
    score: 0.61,
    explanation: "",
    question: "两篇笔记共同预设了什么前提？",
    verdict: "new",
    created_at: 0,
    answered_by: "",
    answered_at: 0,
    ...overrides,
  };
}

test("isOpenBounty requires a question, no answer, and a non-dismissed verdict", () => {
  assert.equal(isOpenBounty(collision({})), true);
  assert.equal(isOpenBounty(collision({ question: "" })), false);
  assert.equal(isOpenBounty(collision({ question: "  " })), false);
  assert.equal(
    isOpenBounty(collision({ answered_by: "_answers/42-x" })),
    false,
  );
  assert.equal(isOpenBounty(collision({ verdict: "dismissed" })), false);
  assert.equal(isOpenBounty(collision({ verdict: "confirmed" })), true);
});

test("bountyAgeDays counts whole days and never goes negative", () => {
  const now = Date.UTC(2026, 7, 10);
  assert.equal(bountyAgeDays(now, now), 0);
  assert.equal(bountyAgeDays(now - 3 * DAY - 1, now), 3);
  assert.equal(bountyAgeDays(now + DAY, now), 0);
});

test("bountyAgeLabel renders 今天挂出 same-day and 晾了 N 天 otherwise", () => {
  const now = Date.UTC(2026, 7, 10);
  assert.equal(bountyAgeLabel(now, now), "今天挂出");
  assert.equal(bountyAgeLabel(now - 9 * DAY, now), "晾了 9 天");
});
