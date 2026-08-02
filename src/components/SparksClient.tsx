"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import {
  groupSparksBySource,
  isStranded,
  sparkAgeLabel,
  sparkMarkdown,
  sparkSlug,
  viewHref,
  type Collision,
  type CollisionVerdict,
  type Spark,
} from "@/lib/sparks";
import {
  answerMarkdown,
  answerSlug,
  bountyAgeLabel,
  isOpenBounty,
} from "@/lib/play";
import { paginate } from "@/lib/paginate";
import {
  feedTitle,
  relativeTimeLabel,
  rssSourceLabel,
  type Feed,
} from "@/lib/feeds";
import { GrowthChart } from "@/components/GrowthChart";

const PAGE_SIZE = 10;

function verdictLabel(verdict: CollisionVerdict): string {
  switch (verdict) {
    case "confirmed":
      return "已确认";
    case "dismissed":
      return "已忽略";
    default:
      return "待策展";
  }
}

function CollisionCard({
  collision,
  onVerdict,
}: {
  collision: Collision;
  onVerdict: (id: number, verdict: CollisionVerdict) => void;
}) {
  return (
    <li className="rounded-xl border border-stone-200 bg-white p-4">
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1 text-sm">
        <Link
          href={viewHref(collision.slug_a)}
          className="font-medium text-stone-800 hover:underline"
        >
          {collision.title_a}
        </Link>
        <span className="text-stone-400">↔</span>
        <Link
          href={viewHref(collision.slug_b)}
          className="font-medium text-stone-800 hover:underline"
        >
          {collision.title_b}
        </Link>
        <span className="ml-auto text-xs tabular-nums text-stone-400">
          {collision.score.toFixed(2)}
        </span>
      </div>
      {collision.explanation && (
        <p className="mt-2 text-sm leading-6 text-stone-600">
          {collision.explanation}
        </p>
      )}
      {collision.question && (
        <p className="mt-1.5 text-sm leading-6 text-stone-500">
          <span className="font-medium text-stone-600">问题：</span>
          {collision.question}
        </p>
      )}
      <div className="mt-3 flex items-center gap-2">
        <button
          type="button"
          onClick={() => onVerdict(collision.id, "confirmed")}
          disabled={collision.verdict === "confirmed"}
          className="rounded-md bg-stone-900 px-3 py-1.5 text-xs font-medium text-white hover:opacity-85 disabled:opacity-40"
        >
          确认
        </button>
        <button
          type="button"
          onClick={() => onVerdict(collision.id, "dismissed")}
          disabled={collision.verdict === "dismissed"}
          className="rounded-md border border-stone-300 bg-white px-3 py-1.5 text-xs font-medium text-stone-700 hover:bg-stone-50 disabled:opacity-40"
        >
          忽略
        </button>
        <span className="ml-auto text-xs text-stone-400">
          {verdictLabel(collision.verdict)}
        </span>
      </div>
    </li>
  );
}

function SparkListItem({
  spark,
  nowMs,
  showStranded,
}: {
  spark: Spark;
  nowMs: number;
  showStranded: boolean;
}) {
  return (
    <li className="rounded-xl border border-stone-200 bg-white p-4">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <Link
          href={viewHref(spark.slug)}
          className="min-w-0 flex-1 truncate text-sm font-medium text-stone-800 hover:underline"
        >
          {spark.title}
        </Link>
        {rssSourceLabel(spark.source) && (
          <span className="rounded-full bg-stone-100 px-2 py-0.5 text-xs text-stone-500">
            {rssSourceLabel(spark.source)}
          </span>
        )}
        <span className="rounded-full bg-stone-100 px-2 py-0.5 text-xs text-stone-500">
          {sparkAgeLabel(spark.updated, nowMs)}
        </span>
        <span className="rounded-full bg-stone-100 px-2 py-0.5 text-xs tabular-nums text-stone-500">
          {spark.collisions} 碰撞
        </span>
        {showStranded && isStranded(spark, nowMs) && (
          <span className="rounded-full bg-amber-100 px-2 py-0.5 text-xs text-amber-700">
            搁浅
          </span>
        )}
      </div>
      {spark.excerpt && (
        <p className="mt-1.5 line-clamp-2 text-sm leading-6 text-stone-500">
          {spark.excerpt}
        </p>
      )}
    </li>
  );
}

type SparkTab = "bounties" | "collisions" | "sparks" | "growth" | "feeds";

const SPARK_TABS: { id: SparkTab; label: string }[] = [
  { id: "bounties", label: "悬赏" },
  { id: "collisions", label: "碰撞流" },
  { id: "sparks", label: "灵感流" },
  { id: "growth", label: "成长" },
  { id: "feeds", label: "订阅" },
];

function tabFromHash(hash: string): SparkTab {
  const id = hash.replace(/^#/, "");
  return SPARK_TABS.some((tab) => tab.id === id)
    ? (id as SparkTab)
    : "bounties";
}

// Same visual language as the main Nav tabs.
function tabClass(active: boolean): string {
  return `relative inline-flex min-h-9 items-center gap-1.5 px-1 text-sm font-medium transition-colors ${
    active
      ? "text-stone-900 after:absolute after:inset-x-1 after:-bottom-px after:h-0.5 after:rounded-full after:bg-[var(--accent)]"
      : "text-stone-400 hover:text-stone-700"
  }`;
}

function CountBadge({ count }: { count: number }) {
  if (count === 0) return null;
  return (
    <span className="rounded-full bg-stone-100 px-1.5 py-0.5 text-xs tabular-nums text-stone-500">
      {count}
    </span>
  );
}

function Pagination({
  page,
  pageCount,
  onPage,
}: {
  page: number;
  pageCount: number;
  onPage: (page: number) => void;
}) {
  if (pageCount <= 1) return null;
  return (
    <div className="mt-4 flex items-center justify-center gap-3 text-xs text-stone-500">
      <button
        type="button"
        onClick={() => onPage(page - 1)}
        disabled={page <= 1}
        className="rounded-md border border-stone-300 bg-white px-2.5 py-1 font-medium text-stone-700 hover:bg-stone-50 disabled:opacity-40"
      >
        上一页
      </button>
      <span className="tabular-nums">
        第 {page} / {pageCount} 页
      </span>
      <button
        type="button"
        onClick={() => onPage(page + 1)}
        disabled={page >= pageCount}
        className="rounded-md border border-stone-300 bg-white px-2.5 py-1 font-medium text-stone-700 hover:bg-stone-50 disabled:opacity-40"
      >
        下一页
      </button>
    </div>
  );
}

export function SparksClient() {
  const [tab, setTab] = useState<SparkTab>("bounties");
  const [sparks, setSparks] = useState<Spark[]>([]);
  const [collisions, setCollisions] = useState<Collision[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [draft, setDraft] = useState("");
  const [saving, setSaving] = useState(false);
  const [showDismissed, setShowDismissed] = useState(false);
  const [claimingId, setClaimingId] = useState<number | null>(null);
  const [claimDraft, setClaimDraft] = useState("");
  const [claiming, setClaiming] = useState(false);
  const [showAnswered, setShowAnswered] = useState(false);
  const [collisionPage, setCollisionPage] = useState(1);
  const [sparkPage, setSparkPage] = useState(1);
  const [rssPage, setRssPage] = useState(1);
  const [showRss, setShowRss] = useState(false);
  const [feeds, setFeeds] = useState<Feed[]>([]);
  const [feedsLoading, setFeedsLoading] = useState(false);
  const [feedUrl, setFeedUrl] = useState("");
  const [feedDescription, setFeedDescription] = useState("");
  const [feedError, setFeedError] = useState("");
  const [subscribing, setSubscribing] = useState(false);
  const [busyFeedId, setBusyFeedId] = useState<number | null>(null);
  const [editingFeedId, setEditingFeedId] = useState<number | null>(null);
  const [descriptionDraft, setDescriptionDraft] = useState("");

  // Keep the active tab in the URL hash so a refresh lands back on it.
  useEffect(() => {
    setTab(tabFromHash(window.location.hash));
    const onHashChange = () => setTab(tabFromHash(window.location.hash));
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);

  function selectTab(id: SparkTab) {
    setTab(id);
    window.history.replaceState(null, "", `#${id}`);
  }

  useEffect(() => {
    let cancelled = false;
    async function load() {
      setLoading(true);
      setError("");
      try {
        const [sparksRes, collisionsRes] = await Promise.all([
          fetch("/mdhub/api/sparks"),
          fetch("/mdhub/api/collisions"),
        ]);
        if (!sparksRes.ok || !collisionsRes.ok) {
          throw new Error(
            `加载失败（HTTP ${sparksRes.status}/${collisionsRes.status}）`,
          );
        }
        const [sparkData, collisionData] = await Promise.all([
          sparksRes.json(),
          collisionsRes.json(),
        ]);
        if (!cancelled) {
          setSparks(sparkData as Spark[]);
          setCollisions(collisionData as Collision[]);
        }
      } catch (loadError) {
        if (!cancelled) {
          setError(
            loadError instanceof Error ? loadError.message : "加载失败",
          );
        }
      } finally {
        if (!cancelled) setLoading(false);
      }
    }
    load();
    return () => {
      cancelled = true;
    };
  }, []);

  async function loadFeeds() {
    setFeedsLoading(true);
    try {
      const res = await fetch("/mdhub/api/feeds");
      if (!res.ok) throw new Error(`加载订阅失败（HTTP ${res.status}）`);
      setFeeds((await res.json()) as Feed[]);
    } catch (loadError) {
      setFeedError(
        loadError instanceof Error ? loadError.message : "加载订阅失败",
      );
    } finally {
      setFeedsLoading(false);
    }
  }

  // The feeds tab loads on entry.
  useEffect(() => {
    if (tab === "feeds") {
      setFeedError("");
      loadFeeds();
    }
  }, [tab]);

  async function subscribe() {
    const url = feedUrl.trim();
    if (!url || subscribing) return;
    setSubscribing(true);
    setFeedError("");
    try {
      const res = await fetch("/mdhub/api/feeds", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ url, description: feedDescription.trim() }),
      });
      if (!res.ok) {
        const result = (await res.json().catch(() => ({}))) as {
          error?: string;
        };
        throw new Error(result.error || `订阅失败（HTTP ${res.status}）`);
      }
      setFeedUrl("");
      setFeedDescription("");
      await loadFeeds();
    } catch (subscribeError) {
      setFeedError(
        subscribeError instanceof Error ? subscribeError.message : "订阅失败",
      );
    } finally {
      setSubscribing(false);
    }
  }

  async function toggleFeed(feed: Feed) {
    if (busyFeedId !== null) return;
    setBusyFeedId(feed.id);
    setFeedError("");
    const previous = feeds;
    setFeeds((items) =>
      items.map((item) =>
        item.id === feed.id ? { ...item, enabled: !feed.enabled } : item,
      ),
    );
    try {
      const res = await fetch(`/mdhub/api/feeds/${feed.id}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ enabled: !feed.enabled }),
      });
      if (!res.ok) throw new Error(`操作失败（HTTP ${res.status}）`);
    } catch (toggleError) {
      setFeeds(previous);
      setFeedError(
        toggleError instanceof Error ? toggleError.message : "操作失败",
      );
    } finally {
      setBusyFeedId(null);
    }
  }

  async function pollFeedNow(feed: Feed) {
    if (busyFeedId !== null) return;
    setBusyFeedId(feed.id);
    setFeedError("");
    try {
      const res = await fetch(
        `/mdhub/api/feeds/${feed.id}/poll`,
        { method: "POST" },
      );
      if (!res.ok) throw new Error(`抓取失败（HTTP ${res.status}）`);
      await loadFeeds();
    } catch (pollError) {
      setFeedError(
        pollError instanceof Error ? pollError.message : "抓取失败",
      );
    } finally {
      setBusyFeedId(null);
    }
  }

  async function saveDescription(feed: Feed) {
    if (busyFeedId !== null) return;
    setBusyFeedId(feed.id);
    setFeedError("");
    const description = descriptionDraft.trim();
    try {
      const res = await fetch(`/mdhub/api/feeds/${feed.id}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ description }),
      });
      if (!res.ok) throw new Error(`保存失败（HTTP ${res.status}）`);
      setFeeds((items) =>
        items.map((item) =>
          item.id === feed.id ? { ...item, description } : item,
        ),
      );
      setEditingFeedId(null);
    } catch (saveError) {
      setFeedError(
        saveError instanceof Error ? saveError.message : "保存失败",
      );
    } finally {
      setBusyFeedId(null);
    }
  }

  async function deleteFeed(feed: Feed) {
    if (busyFeedId !== null) return;
    if (!window.confirm(`退订 ${feedTitle(feed)}？已导入的碎片会保留。`)) {
      return;
    }
    setBusyFeedId(feed.id);
    setFeedError("");
    try {
      const res = await fetch(`/mdhub/api/feeds/${feed.id}`, {
        method: "DELETE",
      });
      if (!res.ok) throw new Error(`删除失败（HTTP ${res.status}）`);
      setFeeds((items) => items.filter((item) => item.id !== feed.id));
    } catch (deleteError) {
      setFeedError(
        deleteError instanceof Error ? deleteError.message : "删除失败",
      );
    } finally {
      setBusyFeedId(null);
    }
  }

  async function capture() {
    const text = draft.trim();
    if (!text || saving) return;
    setSaving(true);
    setError("");
    try {
      const now = new Date();
      const slug = sparkSlug(now, Math.random);
      const endpoint =
        "/mdhub/api/document/" +
        slug
          .split("/")
          .map((part) => encodeURIComponent(part))
          .join("/");
      const res = await fetch(endpoint, {
        method: "PUT",
        headers: { "Content-Type": "text/markdown; charset=utf-8" },
        body: sparkMarkdown(text, now),
      });
      if (!res.ok) {
        const result = (await res.json().catch(() => ({}))) as {
          error?: string;
        };
        throw new Error(result.error || `保存失败（HTTP ${res.status}）`);
      }
      setDraft("");
      const sparksRes = await fetch("/mdhub/api/sparks");
      if (sparksRes.ok) setSparks((await sparksRes.json()) as Spark[]);
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : "保存失败");
    } finally {
      setSaving(false);
    }
  }

  async function changeVerdict(id: number, verdict: CollisionVerdict) {
    const previous = collisions;
    setCollisions((items) =>
      items.map((item) => (item.id === id ? { ...item, verdict } : item)),
    );
    try {
      const res = await fetch(`/mdhub/api/collisions/${id}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ verdict }),
      });
      if (!res.ok) throw new Error(`更新失败（HTTP ${res.status}）`);
    } catch (verdictError) {
      setCollisions(previous);
      setError(
        verdictError instanceof Error ? verdictError.message : "更新失败",
      );
    }
  }

  // Claiming a bounty is writing the answer: PUT the answer note first,
  // then close the bounty by posting its slug.
  async function submitClaim(collision: Collision) {
    if (claiming) return;
    setClaiming(true);
    setError("");
    try {
      const now = new Date();
      const slug = answerSlug(collision.id, now, Math.random);
      const endpoint =
        "/mdhub/api/document/" +
        slug
          .split("/")
          .map((part) => encodeURIComponent(part))
          .join("/");
      const putRes = await fetch(endpoint, {
        method: "PUT",
        headers: { "Content-Type": "text/markdown; charset=utf-8" },
        body: answerMarkdown(collision, claimDraft),
      });
      if (!putRes.ok) {
        const result = (await putRes.json().catch(() => ({}))) as {
          error?: string;
        };
        throw new Error(result.error || `保存回答失败（HTTP ${putRes.status}）`);
      }
      const answerRes = await fetch(
        `/mdhub/api/collisions/${collision.id}/answer`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ slug }),
        },
      );
      if (!answerRes.ok) {
        throw new Error(`认领失败（HTTP ${answerRes.status}）`);
      }
      setClaimingId(null);
      setClaimDraft("");
      const collisionsRes = await fetch("/mdhub/api/collisions");
      if (collisionsRes.ok) {
        setCollisions((await collisionsRes.json()) as Collision[]);
      }
    } catch (claimError) {
      setError(claimError instanceof Error ? claimError.message : "认领失败");
    } finally {
      setClaiming(false);
    }
  }

  const nowMs = Date.now();
  const openBounties = collisions.filter(isOpenBounty);
  const answeredBounties = collisions.filter((c) => c.answered_by !== "");
  const activeCollisions = collisions.filter((c) => c.verdict !== "dismissed");
  const dismissedCollisions = collisions.filter(
    (c) => c.verdict === "dismissed",
  );
  const collisionPageData = paginate(activeCollisions, collisionPage, PAGE_SIZE);
  // Hand-written sparks always stay in view; RSS imports collapse into
  // their own group behind them.
  const { handwritten, rss } = groupSparksBySource(sparks);
  const sparkPageData = paginate(handwritten, sparkPage, PAGE_SIZE);
  const rssPageData = paginate(rss, rssPage, PAGE_SIZE);

  const tabCounts: Partial<Record<SparkTab, number>> = {
    bounties: openBounties.length,
    collisions: activeCollisions.length,
    sparks: sparks.length,
  };

  return (
    <div className="space-y-8">
      {error && (
        <p className="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700">
          {error}
        </p>
      )}

      <section aria-labelledby="quick-capture-title">
        <h2
          id="quick-capture-title"
          className="text-sm font-semibold text-stone-800"
        >
          快速捕获
        </h2>
        <textarea
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          rows={3}
          placeholder="记下一个碎片想法…"
          className="mt-3 w-full rounded-xl border border-stone-300 bg-white px-3 py-2.5 text-sm leading-6 text-stone-800 placeholder:text-stone-400 focus:border-stone-500 focus:outline-none"
        />
        <div className="mt-2 flex items-center gap-3">
          <button
            type="button"
            onClick={capture}
            disabled={saving || !draft.trim()}
            className="rounded-md bg-stone-900 px-4 py-2 text-sm font-medium text-white hover:opacity-85 disabled:opacity-40"
          >
            {saving ? "保存中…" : "捕获"}
          </button>
          <span className="text-xs text-stone-400">
            保存为 _sparks/ 下的碎片（type: fleeting）
          </span>
        </div>
      </section>

      <div>
        <div
          role="tablist"
          aria-label="Sparks 板块"
          className="flex items-center gap-4 border-b border-stone-200"
        >
          {SPARK_TABS.map((sparkTab) => (
            <button
              key={sparkTab.id}
              type="button"
              role="tab"
              aria-selected={tab === sparkTab.id}
              onClick={() => selectTab(sparkTab.id)}
              className={tabClass(tab === sparkTab.id)}
            >
              {sparkTab.label}
              {tabCounts[sparkTab.id] !== undefined && (
                <CountBadge count={tabCounts[sparkTab.id]!} />
              )}
            </button>
          ))}
        </div>

        <div className="mt-6">
          {tab === "bounties" && (
            <section aria-label="悬赏">
              {loading ? (
                <p className="text-sm text-stone-400">加载中…</p>
              ) : openBounties.length === 0 ? (
                <p className="text-sm text-stone-400">
                  没有开放悬赏。碰撞产出的开放问题会挂在这里等你认领。
                </p>
              ) : (
                <ul className="space-y-3">
                  {openBounties.map((bounty) => (
                    <li
                      key={bounty.id}
                      className="rounded-xl border border-stone-200 bg-white p-4"
                    >
                      <p className="text-sm font-medium leading-6 text-stone-800">
                        {bounty.question}
                      </p>
                      <div className="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
                        <Link
                          href={viewHref(bounty.slug_a)}
                          className="text-stone-600 hover:underline"
                        >
                          {bounty.title_a}
                        </Link>
                        <span className="text-stone-400">↔</span>
                        <Link
                          href={viewHref(bounty.slug_b)}
                          className="text-stone-600 hover:underline"
                        >
                          {bounty.title_b}
                        </Link>
                        <span className="ml-auto rounded-full bg-amber-100 px-2 py-0.5 text-xs text-amber-700">
                          {bountyAgeLabel(bounty.created_at, nowMs)}
                        </span>
                      </div>
                      {claimingId === bounty.id ? (
                        <div className="mt-3">
                          <textarea
                            value={claimDraft}
                            onChange={(event) =>
                              setClaimDraft(event.target.value)
                            }
                            rows={4}
                            placeholder="写下你的回答（会保存为一篇正式笔记）…"
                            className="w-full rounded-xl border border-stone-300 bg-white px-3 py-2.5 text-sm leading-6 text-stone-800 placeholder:text-stone-400 focus:border-stone-500 focus:outline-none"
                          />
                          <div className="mt-2 flex items-center gap-2">
                            <button
                              type="button"
                              onClick={() => submitClaim(bounty)}
                              disabled={claiming}
                              className="rounded-md bg-stone-900 px-3 py-1.5 text-xs font-medium text-white hover:opacity-85 disabled:opacity-40"
                            >
                              {claiming ? "提交中…" : "提交回答"}
                            </button>
                            <button
                              type="button"
                              onClick={() => {
                                setClaimingId(null);
                                setClaimDraft("");
                              }}
                              disabled={claiming}
                              className="rounded-md border border-stone-300 bg-white px-3 py-1.5 text-xs font-medium text-stone-700 hover:bg-stone-50 disabled:opacity-40"
                            >
                              取消
                            </button>
                          </div>
                        </div>
                      ) : (
                        <button
                          type="button"
                          onClick={() => {
                            setClaimingId(bounty.id);
                            setClaimDraft("");
                          }}
                          className="mt-3 rounded-md border border-stone-300 bg-white px-3 py-1.5 text-xs font-medium text-stone-700 hover:bg-stone-50"
                        >
                          认领
                        </button>
                      )}
                    </li>
                  ))}
                </ul>
              )}
              {answeredBounties.length > 0 && (
                <div className="mt-3">
                  <button
                    type="button"
                    onClick={() => setShowAnswered((value) => !value)}
                    className="text-xs text-stone-400 hover:text-stone-600"
                  >
                    {showAnswered
                      ? "收起已回答"
                      : `已回答（${answeredBounties.length}）`}
                  </button>
                  {showAnswered && (
                    <ul className="mt-3 space-y-2 opacity-80">
                      {answeredBounties.map((bounty) => (
                        <li
                          key={bounty.id}
                          className="rounded-xl border border-stone-200 bg-white p-4"
                        >
                          <p className="line-clamp-2 text-sm leading-6 text-stone-600">
                            {bounty.question}
                          </p>
                          <div className="mt-1.5 text-sm">
                            <Link
                              href={viewHref(bounty.answered_by)}
                              className="font-medium text-stone-800 hover:underline"
                            >
                              回答：{bounty.answered_by.split("/").pop()}
                            </Link>
                          </div>
                        </li>
                      ))}
                    </ul>
                  )}
                </div>
              )}
            </section>
          )}

          {tab === "collisions" && (
            <section aria-label="碰撞流">
              {loading ? (
                <p className="text-sm text-stone-400">加载中…</p>
              ) : activeCollisions.length === 0 ? (
                <p className="text-sm text-stone-400">
                  还没有碰撞。写入内容后，引擎会把语义相近的笔记配对送到这里。
                </p>
              ) : (
                <>
                  <ul className="space-y-3">
                    {collisionPageData.pageItems.map((collision) => (
                      <CollisionCard
                        key={collision.id}
                        collision={collision}
                        onVerdict={changeVerdict}
                      />
                    ))}
                  </ul>
                  <Pagination
                    page={collisionPageData.page}
                    pageCount={collisionPageData.pageCount}
                    onPage={setCollisionPage}
                  />
                </>
              )}
              {dismissedCollisions.length > 0 && (
                <div className="mt-3">
                  <button
                    type="button"
                    onClick={() => setShowDismissed((value) => !value)}
                    className="text-xs text-stone-400 hover:text-stone-600"
                  >
                    {showDismissed
                      ? "收起已忽略"
                      : `展开已忽略（${dismissedCollisions.length}）`}
                  </button>
                  {showDismissed && (
                    <ul className="mt-3 space-y-3 opacity-70">
                      {dismissedCollisions.map((collision) => (
                        <CollisionCard
                          key={collision.id}
                          collision={collision}
                          onVerdict={changeVerdict}
                        />
                      ))}
                    </ul>
                  )}
                </div>
              )}
            </section>
          )}

          {tab === "sparks" && (
            <section aria-label="灵感流">
              {loading ? (
                <p className="text-sm text-stone-400">加载中…</p>
              ) : sparks.length === 0 ? (
                <p className="text-sm text-stone-400">
                  还没有碎片。用上面的快速捕获记下第一条。
                </p>
              ) : (
                <div className="space-y-6">
                  <div>
                    <h3 className="flex items-center gap-1.5 text-sm font-semibold text-stone-800">
                      手写
                      <CountBadge count={handwritten.length} />
                    </h3>
                    {handwritten.length === 0 ? (
                      <p className="mt-2 text-sm text-stone-400">
                        还没有手写碎片。用上面的快速捕获记下第一条。
                      </p>
                    ) : (
                      <>
                        <ul className="mt-2 space-y-2">
                          {sparkPageData.pageItems.map((spark) => (
                            <SparkListItem
                              key={spark.slug}
                              spark={spark}
                              nowMs={nowMs}
                              showStranded
                            />
                          ))}
                        </ul>
                        <Pagination
                          page={sparkPageData.page}
                          pageCount={sparkPageData.pageCount}
                          onPage={setSparkPage}
                        />
                      </>
                    )}
                  </div>
                  {rss.length > 0 && (
                    <div>
                      <h3 className="flex items-center gap-1.5 text-sm font-semibold text-stone-800">
                        RSS
                        <CountBadge count={rss.length} />
                      </h3>
                      <div className="mt-2">
                        <button
                          type="button"
                          onClick={() => setShowRss((value) => !value)}
                          className="text-xs text-stone-400 hover:text-stone-600"
                        >
                          {showRss
                            ? "收起 RSS 碎片"
                            : `展开 RSS 碎片（${rss.length}）`}
                        </button>
                        {showRss && (
                          <>
                            <ul className="mt-2 space-y-2">
                              {rssPageData.pageItems.map((spark) => (
                                <SparkListItem
                                  key={spark.slug}
                                  spark={spark}
                                  nowMs={nowMs}
                                  showStranded={false}
                                />
                              ))}
                            </ul>
                            <Pagination
                              page={rssPageData.page}
                              pageCount={rssPageData.pageCount}
                              onPage={setRssPage}
                            />
                          </>
                        )}
                      </div>
                    </div>
                  )}
                </div>
              )}
            </section>
          )}

          {tab === "growth" && <GrowthChart />}

          {tab === "feeds" && (
            <section aria-label="订阅">
              <div className="flex items-center gap-2">
                <input
                  type="url"
                  value={feedUrl}
                  onChange={(event) => setFeedUrl(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter") subscribe();
                  }}
                  placeholder="https://example.com/feed.xml"
                  className="min-w-0 flex-1 rounded-md border border-stone-300 bg-white px-3 py-2 text-sm text-stone-800 placeholder:text-stone-400 focus:border-stone-500 focus:outline-none"
                />
                <button
                  type="button"
                  onClick={subscribe}
                  disabled={subscribing || !feedUrl.trim()}
                  className="shrink-0 rounded-md bg-stone-900 px-4 py-2 text-sm font-medium text-white hover:opacity-85 disabled:opacity-40"
                >
                  {subscribing ? "订阅中…" : "订阅"}
                </button>
              </div>
              <input
                type="text"
                value={feedDescription}
                onChange={(event) => setFeedDescription(event.target.value)}
                placeholder="你为什么订这个站？会帮助碎片和你的笔记碰撞（可选）"
                className="mt-2 w-full rounded-md border border-stone-300 bg-white px-3 py-2 text-sm text-stone-800 placeholder:text-stone-400 focus:border-stone-500 focus:outline-none"
              />
              <p className="mt-1.5 text-xs text-stone-400">
                新条目会自动变成碎片，进入碰撞和灵感流。
              </p>
              {feedError && (
                <p className="mt-2 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700">
                  {feedError}
                </p>
              )}
              {feedsLoading ? (
                <p className="mt-4 text-sm text-stone-400">加载中…</p>
              ) : feeds.length === 0 ? (
                <p className="mt-4 text-sm text-stone-400">
                  还没有订阅。贴上 RSS/Atom 地址试试。
                </p>
              ) : (
                <ul className="mt-4 space-y-2">
                  {feeds.map((feed) => (
                    <li
                      key={feed.id}
                      className={`rounded-xl border border-stone-200 bg-white p-4 ${
                        feed.enabled ? "" : "opacity-60"
                      }`}
                    >
                      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                        <a
                          href={feed.url}
                          target="_blank"
                          rel="noreferrer"
                          className="min-w-0 flex-1 truncate text-sm font-medium text-stone-800 hover:underline"
                        >
                          {feedTitle(feed)}
                        </a>
                        <span className="rounded-full bg-stone-100 px-2 py-0.5 text-xs tabular-nums text-stone-500">
                          {feed.sparks} 碎片
                        </span>
                        {!feed.enabled && (
                          <span className="rounded-full bg-stone-100 px-2 py-0.5 text-xs text-stone-500">
                            已停用
                          </span>
                        )}
                      </div>
                      {editingFeedId === feed.id ? (
                        <div className="mt-2 flex items-center gap-2">
                          <input
                            type="text"
                            value={descriptionDraft}
                            onChange={(event) =>
                              setDescriptionDraft(event.target.value)
                            }
                            onKeyDown={(event) => {
                              if (event.key === "Enter") saveDescription(feed);
                              if (event.key === "Escape") setEditingFeedId(null);
                            }}
                            placeholder="你为什么订这个站？"
                            className="min-w-0 flex-1 rounded-md border border-stone-300 bg-white px-2.5 py-1.5 text-xs text-stone-800 placeholder:text-stone-400 focus:border-stone-500 focus:outline-none"
                          />
                          <button
                            type="button"
                            onClick={() => saveDescription(feed)}
                            disabled={busyFeedId !== null}
                            className="shrink-0 rounded-md bg-stone-900 px-2.5 py-1.5 text-xs font-medium text-white hover:opacity-85 disabled:opacity-40"
                          >
                            保存
                          </button>
                          <button
                            type="button"
                            onClick={() => setEditingFeedId(null)}
                            disabled={busyFeedId !== null}
                            className="shrink-0 rounded-md border border-stone-300 bg-white px-2.5 py-1.5 text-xs font-medium text-stone-700 hover:bg-stone-50 disabled:opacity-40"
                          >
                            取消
                          </button>
                        </div>
                      ) : (
                        feed.description && (
                          <p className="mt-1 truncate text-xs text-stone-500">
                            {feed.description}
                          </p>
                        )
                      )}
                      <p className="mt-1 text-xs text-stone-400">
                        上次抓取：
                        {relativeTimeLabel(feed.last_fetched_at, nowMs)}
                      </p>
                      {feed.last_error && (
                        <p className="mt-1 text-xs text-red-600">
                          {feed.last_error}
                        </p>
                      )}
                      <div className="mt-3 flex items-center gap-2">
                        <button
                          type="button"
                          onClick={() => toggleFeed(feed)}
                          disabled={busyFeedId !== null}
                          className="rounded-md border border-stone-300 bg-white px-3 py-1.5 text-xs font-medium text-stone-700 hover:bg-stone-50 disabled:opacity-40"
                        >
                          {feed.enabled ? "停用" : "启用"}
                        </button>
                        <button
                          type="button"
                          onClick={() => pollFeedNow(feed)}
                          disabled={busyFeedId !== null}
                          className="rounded-md border border-stone-300 bg-white px-3 py-1.5 text-xs font-medium text-stone-700 hover:bg-stone-50 disabled:opacity-40"
                        >
                          {busyFeedId === feed.id ? "抓取中…" : "立即抓取"}
                        </button>
                        <button
                          type="button"
                          onClick={() => {
                            setEditingFeedId(feed.id);
                            setDescriptionDraft(feed.description);
                          }}
                          disabled={busyFeedId !== null}
                          className="rounded-md border border-stone-300 bg-white px-3 py-1.5 text-xs font-medium text-stone-700 hover:bg-stone-50 disabled:opacity-40"
                        >
                          编辑描述
                        </button>
                        <button
                          type="button"
                          onClick={() => deleteFeed(feed)}
                          disabled={busyFeedId !== null}
                          className="rounded-md border border-red-200 bg-white px-3 py-1.5 text-xs font-medium text-red-600 hover:bg-red-50 disabled:opacity-40"
                        >
                          删除
                        </button>
                      </div>
                    </li>
                  ))}
                </ul>
              )}
            </section>
          )}
        </div>
      </div>
    </div>
  );
}
