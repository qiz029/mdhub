import assert from "node:assert/strict";
import test from "node:test";
import {
  answerMarkdown,
  answerSlug,
  bountyAgeDays,
  bountyAgeLabel,
  isOpenBounty,
} from "./play.ts";
import type { Collision } from "./sparks.ts";

const NOW = new Date(2026, 7, 1, 20, 15, 2); // 2026-08-01 20:15:02 local
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

test("answerSlug uses the _answers/ prefix with the collision id, timestamp and random suffix", () => {
  assert.equal(answerSlug(42, NOW, () => 0), "_answers/42-20260801-201502-000000");
  assert.match(
    answerSlug(7, NOW, () => 0.5),
    /^_answers\/7-20260801-201502-[0-9a-f]{6}$/,
  );
  assert.notEqual(answerSlug(7, NOW, () => 0.1), answerSlug(7, NOW, () => 0.2));
});

test("answerMarkdown prefixes the title with 答： and publishes the note", () => {
  const md = answerMarkdown(collision({}), "我的回答是……");
  assert.match(
    md,
    /^---\ntitle: "答：两篇笔记共同预设了什么前提？"\npublish: true\n---\n/,
  );
  assert.ok(md.includes("悬赏问题：两篇笔记共同预设了什么前提？"));
  assert.ok(
    md.includes("相关笔记：[[notes/a]]、[[_sparks/20260701-100000-abcdef]]"),
  );
  assert.ok(md.endsWith("我的回答是……\n"));
});

test("answerMarkdown truncates long questions in the title by code point", () => {
  const md = answerMarkdown(collision({ question: "问".repeat(50) }), "");
  assert.match(md, /^---\ntitle: "答：问{36}…"\npublish: true\n---\n/);
});

test("answerMarkdown escapes quotes in the title as a YAML double-quoted scalar", () => {
  const md = answerMarkdown(collision({ question: '什么叫 "连接"？' }), "");
  assert.match(md, /^---\ntitle: "答：什么叫 \\"连接\\"？"/);
});

test("answerMarkdown tolerates an empty answer body", () => {
  const md = answerMarkdown(collision({}), "  \n ");
  assert.ok(md.endsWith("]]\n\n\n"));
});
