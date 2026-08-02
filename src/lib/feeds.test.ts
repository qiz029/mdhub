import assert from "node:assert/strict";
import test from "node:test";
import { feedTitle, relativeTimeLabel, rssSourceLabel } from "./feeds.ts";

test("feedTitle prefers the feed title", () => {
  assert.equal(
    feedTitle({ title: "  少数派  ", url: "https://sspai.com/feed" }),
    "少数派",
  );
});

test("feedTitle falls back to the URL host, then the raw URL", () => {
  assert.equal(
    feedTitle({ title: "", url: "https://example.com/rss.xml" }),
    "example.com",
  );
  assert.equal(feedTitle({ title: " ", url: "not a url" }), "not a url");
});

test("rssSourceLabel extracts the feed name from rss/ sources only", () => {
  assert.equal(rssSourceLabel("rss/少数派"), "少数派");
  assert.equal(rssSourceLabel("user"), null);
  assert.equal(rssSourceLabel("agent"), null);
  assert.equal(rssSourceLabel(""), null);
  assert.equal(rssSourceLabel(undefined), null);
  assert.equal(rssSourceLabel("rss/"), null);
  assert.equal(rssSourceLabel("rss/  有空白  "), "有空白");
});

test("relativeTimeLabel renders 从未 for never-fetched feeds", () => {
  const now = Date.UTC(2026, 7, 10);
  assert.equal(relativeTimeLabel(0, now), "从未");
  assert.equal(relativeTimeLabel(-5, now), "从未");
});

test("relativeTimeLabel buckets by minute, hour and day", () => {
  const minute = 60 * 1000;
  const hour = 60 * minute;
  const day = 24 * hour;
  const now = Date.UTC(2026, 7, 10);
  assert.equal(relativeTimeLabel(now, now), "刚刚");
  assert.equal(relativeTimeLabel(now - 30 * 1000, now), "刚刚");
  assert.equal(relativeTimeLabel(now - 5 * minute, now), "5 分钟前");
  assert.equal(relativeTimeLabel(now - 3 * hour, now), "3 小时前");
  assert.equal(relativeTimeLabel(now - 2 * day, now), "2 天前");
  assert.equal(relativeTimeLabel(now + hour, now), "刚刚"); // clock skew clamps
});
