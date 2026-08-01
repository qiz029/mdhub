import assert from "node:assert/strict";
import test from "node:test";
import {
  isStranded,
  sparkAgeDays,
  sparkAgeLabel,
  sparkMarkdown,
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
