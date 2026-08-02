"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import {
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
import { GrowthChart } from "@/components/GrowthChart";

const TOKEN_KEY = "mdhub-edit-token";
const PAGE_SIZE = 10;

// Reads are public (personal space; auth is handled at the edge). The edit
// token is only needed for writes — capture, verdicts, bounty claims — and
// is fetched lazily: use the cached token or prompt, and on a 401 drop the
// cached token and prompt once more before giving up.
async function fetchWithEditToken(
  input: string,
  init: RequestInit,
): Promise<Response> {
  let lastRes: Response | null = null;
  for (let attempt = 0; attempt < 2; attempt++) {
    let token = sessionStorage.getItem(TOKEN_KEY) || "";
    if (!token) {
      token = window.prompt("输入 MDHub 编辑令牌") || "";
      if (!token) throw new Error("需要编辑令牌，已取消");
      sessionStorage.setItem(TOKEN_KEY, token);
    }
    lastRes = await fetch(input, {
      ...init,
      headers: { ...init.headers, "X-MDHub-Edit-Token": token },
    });
    if (lastRes.status !== 401) return lastRes;
    sessionStorage.removeItem(TOKEN_KEY);
  }
  return lastRes!;
}

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

type SparkTab = "bounties" | "collisions" | "sparks" | "growth";

const SPARK_TABS: { id: SparkTab; label: string }[] = [
  { id: "bounties", label: "悬赏" },
  { id: "collisions", label: "碰撞流" },
  { id: "sparks", label: "灵感流" },
  { id: "growth", label: "成长" },
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
      const res = await fetchWithEditToken(endpoint, {
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
      const res = await fetchWithEditToken(`/mdhub/api/collisions/${id}`, {
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

  // Claiming a bounty is writing the answer: PUT the answer note first
  // (needs the edit token), then close the bounty by posting its slug.
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
      const putRes = await fetchWithEditToken(endpoint, {
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
      const answerRes = await fetchWithEditToken(
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
  const sparkPageData = paginate(sparks, sparkPage, PAGE_SIZE);

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
            保存为 _sparks/ 下的碎片（type: fleeting），需编辑令牌
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
                <>
                  <ul className="space-y-2">
                    {sparkPageData.pageItems.map((spark) => (
                      <li
                        key={spark.slug}
                        className="rounded-xl border border-stone-200 bg-white p-4"
                      >
                        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                          <Link
                            href={viewHref(spark.slug)}
                            className="min-w-0 flex-1 truncate text-sm font-medium text-stone-800 hover:underline"
                          >
                            {spark.title}
                          </Link>
                          <span className="rounded-full bg-stone-100 px-2 py-0.5 text-xs text-stone-500">
                            {sparkAgeLabel(spark.updated, nowMs)}
                          </span>
                          <span className="rounded-full bg-stone-100 px-2 py-0.5 text-xs tabular-nums text-stone-500">
                            {spark.collisions} 碰撞
                          </span>
                          {isStranded(spark, nowMs) && (
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
                    ))}
                  </ul>
                  <Pagination
                    page={sparkPageData.page}
                    pageCount={sparkPageData.pageCount}
                    onPage={setSparkPage}
                  />
                </>
              )}
            </section>
          )}

          {tab === "growth" && <GrowthChart />}
        </div>
      </div>
    </div>
  );
}
