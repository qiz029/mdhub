// RSS feed subscriptions: the backend polls each enabled feed and turns new
// items into sparks (`source: rss/<feed title>`). Pure helpers live here so
// they can be unit-tested without a DOM.

export type Feed = {
  id: number;
  url: string;
  title: string;
  description: string; // user-written note on why this feed is subscribed
  enabled: boolean;
  last_fetched_at: number; // Unix ms; 0 = never fetched
  last_error: string;
  created_at: number; // Unix ms
  sparks: number; // sparks imported from this feed
};

// Display name for a feed: its title, falling back to the URL's host.
export function feedTitle(feed: Pick<Feed, "title" | "url">): string {
  const title = feed.title.trim();
  if (title) return title;
  try {
    return new URL(feed.url).host || feed.url;
  } catch {
    return feed.url;
  }
}

// rssSourceLabel extracts the feed name from a spark's source tag. Only
// "rss/<name>" gets a badge; user/agent sources stay unbadged.
export function rssSourceLabel(
  source: string | undefined | null,
): string | null {
  if (!source || !source.startsWith("rss/")) return null;
  const name = source.slice("rss/".length).trim();
  return name || null;
}

const MINUTE_MS = 60 * 1000;
const HOUR_MS = 60 * MINUTE_MS;
const DAY_MS = 24 * HOUR_MS;

// relativeTimeLabel renders "上次抓取" as a compact relative time.
export function relativeTimeLabel(ms: number, nowMs: number): string {
  if (ms <= 0) return "从未";
  const delta = Math.max(0, nowMs - ms);
  if (delta < MINUTE_MS) return "刚刚";
  if (delta < HOUR_MS) return `${Math.floor(delta / MINUTE_MS)} 分钟前`;
  if (delta < DAY_MS) return `${Math.floor(delta / HOUR_MS)} 小时前`;
  return `${Math.floor(delta / DAY_MS)} 天前`;
}
