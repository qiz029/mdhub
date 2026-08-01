// Sparks: fleeting notes (`type: fleeting` frontmatter) captured quickly and
// kept private, plus the collision pairs the backend engine records between
// them and the rest of the library. Pure helpers live here so they can be
// unit-tested without a DOM.

export type Spark = {
  slug: string;
  title: string;
  excerpt: string;
  updated: number; // Unix ms
  collisions: number;
};

export type CollisionVerdict = "new" | "confirmed" | "dismissed";

export type Collision = {
  id: number;
  slug_a: string;
  slug_b: string;
  title_a: string;
  title_b: string;
  score: number;
  explanation: string;
  question: string;
  verdict: CollisionVerdict;
  created_at: number; // Unix ms
};

const DAY_MS = 24 * 60 * 60 * 1000;

function pad2(n: number): string {
  return String(n).padStart(2, "0");
}

// Spark slugs live under `_sparks/` — the underscore prefix marks a
// non-content directory, the same convention as `_comments/` etc.
export function sparkSlug(now: Date, random: () => number = Math.random): string {
  const stamp =
    `${now.getFullYear()}${pad2(now.getMonth() + 1)}${pad2(now.getDate())}` +
    `-${pad2(now.getHours())}${pad2(now.getMinutes())}${pad2(now.getSeconds())}`;
  const suffix = Math.floor(random() * 0xffffff)
    .toString(16)
    .padStart(6, "0");
  return `_sparks/${stamp}-${suffix}`;
}

function timestampTitle(now: Date): string {
  return `${now.getFullYear()}-${pad2(now.getMonth() + 1)}-${pad2(now.getDate())} ${pad2(now.getHours())}:${pad2(now.getMinutes())}`;
}

// sparkMarkdown wraps a quick capture in the frontmatter contract the backend
// parses: `type: fleeting` marks it as a spark. The title is the first line
// (truncated) so the inspiration stream stays readable; falls back to the
// capture time. JSON.stringify yields a valid YAML double-quoted scalar.
export function sparkMarkdown(text: string, now: Date): string {
  const body = text.trim();
  const firstLine =
    body
      .split("\n")
      .map((line) => line.trim())
      .find((line) => line.length > 0) ?? "";
  const cleaned = firstLine
    .replace(/^#+\s*/, "")
    .replace(/[*_`~[\]]/g, "")
    .trim();
  const runes = [...cleaned];
  const title =
    runes.length === 0
      ? timestampTitle(now)
      : runes.length > 40
        ? runes.slice(0, 40).join("") + "…"
        : cleaned;
  return `---\ntitle: ${JSON.stringify(title)}\ntype: fleeting\n---\n\n${body}\n`;
}

export function sparkAgeDays(updatedMs: number, nowMs: number): number {
  return Math.max(0, Math.floor((nowMs - updatedMs) / DAY_MS));
}

export function sparkAgeLabel(updatedMs: number, nowMs: number): string {
  const days = sparkAgeDays(updatedMs, nowMs);
  return days === 0 ? "今天" : `${days} 天`;
}

// Stranded sparks are the zero-backlog discipline made visible: captured but
// never collided with anything for over a week.
export function isStranded(
  spark: Pick<Spark, "collisions" | "updated">,
  nowMs: number,
): boolean {
  return spark.collisions === 0 && sparkAgeDays(spark.updated, nowMs) > 7;
}

export function viewHref(slug: string): string {
  return (
    "/view/" +
    slug
      .split("/")
      .map((part) => encodeURIComponent(part))
      .join("/")
  );
}
