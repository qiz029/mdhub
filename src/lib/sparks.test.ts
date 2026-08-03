import assert from "node:assert/strict";
import test from "node:test";
import {
  groupSparksBySource,
  isStranded,
  sparkAgeDays,
  sparkAgeLabel,
  sparkMarkdown,
  readingSparkMarkdown,
  sparkSlug,
  viewHref,
} from "./sparks.ts";

const NOW = new Date(2026, 7, 1, 20, 15, 2); // 2026-08-01 20:15:02 local

test("sparkSlug uses the _sparks/ prefix with a timestamp and random suffix", () => {
  assert.equal(sparkSlug(NOW, () => 0), "_sparks/20260801-201502-000000");
  assert.equal(
    sparkSlug(NOW, () => 0.999999999),
    "_sparks/20260801-201502-fffffe",
  );
  assert.match(
    sparkSlug(NOW, () => 0.5),
    /^_sparks\/20260801-201502-[0-9a-f]{6}$/,
  );
});

test("sparkSlug differs across random inputs so same-second captures do not collide", () => {
  assert.notEqual(sparkSlug(NOW, () => 0.1), sparkSlug(NOW, () => 0.2));
});

test("sparkMarkdown wraps the capture in fleeting frontmatter", () => {
  const md = sparkMarkdown("一个想法：碰撞引擎应该只提问", NOW);
  assert.equal(
    md,
    '---\ntitle: "一个想法：碰撞引擎应该只提问"\ntype: fleeting\n---\n\n一个想法：碰撞引擎应该只提问\n',
  );
});

test("sparkMarkdown strips heading markers and markdown emphasis from the title", () => {
  const md = sparkMarkdown("## 关于 **RAG** 的草稿\n\n正文第二行", NOW);
  assert.match(md, /^---\ntitle: "关于 RAG 的草稿"\ntype: fleeting\n---\n/);
  assert.ok(md.endsWith("## 关于 **RAG** 的草稿\n\n正文第二行\n"));
});

test("sparkMarkdown truncates long first lines by code point", () => {
  const md = sparkMarkdown("字".repeat(50), NOW);
  assert.match(md, /^---\ntitle: "字{40}…"\ntype: fleeting\n---\n/);
});

test("sparkMarkdown escapes quotes in the title as a YAML double-quoted scalar", () => {
  const md = sparkMarkdown('他说 "你好" \\ 结束', NOW);
  assert.match(md, /^---\ntitle: "他说 \\"你好\\" \\\\ 结束"\ntype: fleeting\n---\n/);
});

test("sparkMarkdown falls back to a timestamp title for empty captures", () => {
  const md = sparkMarkdown("  \n  ", NOW);
  assert.equal(
    md,
    '---\ntitle: "2026-08-01 20:15"\ntype: fleeting\n---\n\n\n',
  );
});

test("readingSparkMarkdown keeps the user response as authorship and labels system context", () => {
  const md = readingSparkMarkdown(
    "我认为变化的是检索目标，而不是记忆本身。",
    {
      sourceSlug: "papers/memory|v2]",
      sourceTitle: "Memory | Is the New Database]",
      emergenceTitle: "值得追问",
      emergenceBody: "变化的是检索目标，还是记忆本身？",
    },
    NOW,
  );

  assert.match(md, /title: "我认为变化的是检索目标，而不是记忆本身。"/);
  assert.match(md, /source: "reading\/papers\/memory\|v2\]"/);
  assert.match(md, /tags: \[reading\]/);
  assert.match(md, /阅读来源：\[\[papers\/memory%7Cv2%5D\|Memory Is the New Database\]\]/);
  assert.match(md, /系统线索（上下文，不代表用户观点）：值得追问/);
  assert.ok(md.indexOf("我认为变化的是") < md.indexOf("阅读上下文"));
});

test("sparkAgeDays counts whole days and never goes negative", () => {
  const day = 24 * 60 * 60 * 1000;
  const now = Date.UTC(2026, 7, 10);
  assert.equal(sparkAgeDays(now, now), 0);
  assert.equal(sparkAgeDays(now - 7 * day, now), 7);
  assert.equal(sparkAgeDays(now - 7 * day - 1, now), 7);
  assert.equal(sparkAgeDays(now + day, now), 0);
});

test("sparkAgeLabel renders 今天 for same-day sparks and N 天 otherwise", () => {
  const day = 24 * 60 * 60 * 1000;
  const now = Date.UTC(2026, 7, 10);
  assert.equal(sparkAgeLabel(now, now), "今天");
  assert.equal(sparkAgeLabel(now - 12 * day, now), "12 天");
});

test("isStranded flags only old sparks with zero collisions", () => {
  const day = 24 * 60 * 60 * 1000;
  const now = Date.UTC(2026, 7, 10);
  assert.equal(isStranded({ collisions: 0, updated: now - 8 * day }, now), true);
  assert.equal(isStranded({ collisions: 0, updated: now - 7 * day }, now), false);
  assert.equal(isStranded({ collisions: 2, updated: now - 30 * day }, now), false);
});

test("viewHref encodes each slug segment", () => {
  assert.equal(viewHref("notes/我的 笔记"), "/view/notes/%E6%88%91%E7%9A%84%20%E7%AC%94%E8%AE%B0");
});

test("groupSparksBySource keeps hand-written sparks ahead of RSS imports", () => {
  const spark = (slug: string, source?: string) => ({
    slug,
    title: slug,
    excerpt: "",
    updated: 0,
    collisions: 0,
    source,
  });
  const { handwritten, rss } = groupSparksBySource([
    spark("a", "rss/少数派"),
    spark("b", "user"),
    spark("c", "agent"),
    spark("d"), // pre-RSS backend: no source field
    spark("e", "rss/另一处"),
    spark("f", ""),
  ]);
  assert.deepEqual(
    handwritten.map((s) => s.slug),
    ["b", "c", "d", "f"],
  );
  assert.deepEqual(
    rss.map((s) => s.slug),
    ["a", "e"],
  );
});

test("groupSparksBySource handles empty input", () => {
  assert.deepEqual(groupSparksBySource([]), { handwritten: [], rss: [] });
});
