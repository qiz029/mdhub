// Play layer on top of the collision engine: open bounties (unanswered
// collision questions), the daily blind box, and the growth chart. Pure
// helpers live here so they can be unit-tested without a DOM.

import type { Collision } from "./sparks";

// GET /api/blindbox returns a full collisionItem plus both sides' excerpts.
export type BlindBox = Collision & {
  excerpt_a: string;
  excerpt_b: string;
};

export type GrowthDay = {
  date: string; // YYYY-MM-DD
  sparks_new: number;
  sparks_total: number;
  collisions_new: number;
  collisions_total: number;
  confirmed_total: number;
  answered_total: number;
  notes_total: number;
};

export type GrowthResponse = {
  days: GrowthDay[];
  totals: {
    sparks: number;
    collisions: number;
    confirmed: number;
    answered: number;
    notes: number;
  };
};

const DAY_MS = 24 * 60 * 60 * 1000;

// An open bounty is a collision whose open question nobody has answered yet
// and that hasn't been dismissed (backend contract; no separate table).
export function isOpenBounty(c: Collision): boolean {
  return (
    c.question.trim() !== "" &&
    c.answered_by === "" &&
    c.verdict !== "dismissed"
  );
}

export function bountyAgeDays(createdAtMs: number, nowMs: number): number {
  return Math.max(0, Math.floor((nowMs - createdAtMs) / DAY_MS));
}

export function bountyAgeLabel(createdAtMs: number, nowMs: number): string {
  const days = bountyAgeDays(createdAtMs, nowMs);
  return days === 0 ? "今天挂出" : `晾了 ${days} 天`;
}

function pad2(n: number): string {
  return String(n).padStart(2, "0");
}

// Answer notes live under `_answers/` — the underscore prefix marks a
// non-content directory, same convention as `_sparks/`.
export function answerSlug(
  collisionId: number,
  now: Date,
  random: () => number = Math.random,
): string {
  const stamp =
    `${now.getFullYear()}${pad2(now.getMonth() + 1)}${pad2(now.getDate())}` +
    `-${pad2(now.getHours())}${pad2(now.getMinutes())}${pad2(now.getSeconds())}`;
  const suffix = Math.floor(random() * 0xffffff)
    .toString(16)
    .padStart(6, "0");
  return `_answers/${collisionId}-${stamp}-${suffix}`;
}

// answerMarkdown builds the note that claims a bounty: the title is the
// question (truncated), the body seeds wiki-links to both collision sides so
// backlinks interlink the three notes automatically. Answers publish
// directly — the claim flow links them from the Sparks page, and an
// unpublished note would 404 for readers.
export function answerMarkdown(
  collision: Pick<Collision, "slug_a" | "slug_b" | "question">,
  text: string,
): string {
  const body = text.trim();
  const question = collision.question.trim();
  const runes = [...question];
  const title =
    "答：" + (runes.length > 36 ? runes.slice(0, 36).join("") + "…" : question);
  return (
    `---\ntitle: ${JSON.stringify(title)}\npublish: true\n---\n\n` +
    `悬赏问题：${question}\n\n` +
    `相关笔记：[[${collision.slug_a}]]、[[${collision.slug_b}]]\n\n` +
    `${body}\n`
  );
}
