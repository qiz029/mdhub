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
