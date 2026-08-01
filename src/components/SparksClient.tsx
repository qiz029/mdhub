"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
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

const TOKEN_KEY = "mdhub-edit-token";

function authHeaders(token: string): HeadersInit {
  return { "X-MDHub-Edit-Token": token };
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

export function SparksClient() {
  const [ready, setReady] = useState(false);
  const [token, setToken] = useState<string | null>(null);
  const [sparks, setSparks] = useState<Spark[]>([]);
  const [collisions, setCollisions] = useState<Collision[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [draft, setDraft] = useState("");
  const [saving, setSaving] = useState(false);
  const [showDismissed, setShowDismissed] = useState(false);

  useEffect(() => {
    const stored = sessionStorage.getItem(TOKEN_KEY);
    if (stored) setToken(stored);
    setReady(true);
  }, []);

  function dropToken() {
    sessionStorage.removeItem(TOKEN_KEY);
    setToken(null);
  }

  const reloadSparks = useCallback(async (currentToken: string) => {
    const res = await fetch("/mdhub/api/sparks", {
      headers: authHeaders(currentToken),
    });
    if (res.status === 401) {
      dropToken();
      return;
    }
    if (!res.ok) throw new Error(`加载碎片失败（HTTP ${res.status}）`);
    setSparks((await res.json()) as Spark[]);
  }, []);

  useEffect(() => {
    if (!token) return;
    let cancelled = false;
    async function load() {
      setLoading(true);
      setError("");
      try {
        const [sparksRes, collisionsRes] = await Promise.all([
          fetch("/mdhub/api/sparks", { headers: authHeaders(token!) }),
          fetch("/mdhub/api/collisions", { headers: authHeaders(token!) }),
        ]);
        if (sparksRes.status === 401 || collisionsRes.status === 401) {
          if (!cancelled) dropToken();
          return;
        }
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
  }, [token]);

  function enterToken() {
    const value = window.prompt("输入 MDHub 编辑令牌") || "";
    if (!value) return;
    sessionStorage.setItem(TOKEN_KEY, value);
    setToken(value);
  }

  async function capture() {
    const text = draft.trim();
    if (!text || saving || !token) return;
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
        headers: {
          "Content-Type": "text/markdown; charset=utf-8",
          "X-MDHub-Edit-Token": token,
        },
        body: sparkMarkdown(text, now),
      });
      if (res.status === 401) {
        dropToken();
        return;
      }
      if (!res.ok) {
        const result = (await res.json().catch(() => ({}))) as {
          error?: string;
        };
        throw new Error(result.error || `保存失败（HTTP ${res.status}）`);
      }
      setDraft("");
      await reloadSparks(token);
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : "保存失败");
    } finally {
      setSaving(false);
    }
  }

  async function changeVerdict(id: number, verdict: CollisionVerdict) {
    if (!token) return;
    const previous = collisions;
    setCollisions((items) =>
      items.map((item) => (item.id === id ? { ...item, verdict } : item)),
    );
    try {
      const res = await fetch(`/mdhub/api/collisions/${id}`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-MDHub-Edit-Token": token,
        },
        body: JSON.stringify({ verdict }),
      });
      if (res.status === 401) {
        dropToken();
        return;
      }
      if (!res.ok) throw new Error(`更新失败（HTTP ${res.status}）`);
    } catch (verdictError) {
      setCollisions(previous);
      setError(
        verdictError instanceof Error ? verdictError.message : "更新失败",
      );
    }
  }

  if (!ready) return null;

  if (!token) {
    return (
      <section className="rounded-2xl border border-stone-200 bg-stone-50 px-6 py-16 text-center">
        <p className="font-medium text-stone-700">Sparks 是私密的</p>
        <p className="mt-2 text-sm text-stone-400">
          输入编辑令牌后才能查看碎片和碰撞。
        </p>
        <button
          type="button"
          onClick={enterToken}
          className="mt-5 rounded-md bg-stone-900 px-4 py-2.5 text-sm font-medium text-white hover:opacity-85"
        >
          输入编辑令牌
        </button>
      </section>
    );
  }

  const nowMs = Date.now();
  const activeCollisions = collisions.filter((c) => c.verdict !== "dismissed");
  const dismissedCollisions = collisions.filter(
    (c) => c.verdict === "dismissed",
  );

  return (
    <div className="space-y-10">
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
            保存为 _sparks/ 下的私密碎片（type: fleeting）
          </span>
        </div>
      </section>

      <section aria-labelledby="collision-stream-title">
        <h2
          id="collision-stream-title"
          className="text-sm font-semibold text-stone-800"
        >
          碰撞流
        </h2>
        {loading ? (
          <p className="mt-3 text-sm text-stone-400">加载中…</p>
        ) : activeCollisions.length === 0 ? (
          <p className="mt-3 text-sm text-stone-400">
            还没有碰撞。写入内容后，引擎会把语义相近的笔记配对送到这里。
          </p>
        ) : (
          <ul className="mt-3 space-y-3">
            {activeCollisions.map((collision) => (
              <CollisionCard
                key={collision.id}
                collision={collision}
                onVerdict={changeVerdict}
              />
            ))}
          </ul>
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

      <section aria-labelledby="spark-stream-title">
        <h2
          id="spark-stream-title"
          className="text-sm font-semibold text-stone-800"
        >
          灵感流
        </h2>
        {loading ? (
          <p className="mt-3 text-sm text-stone-400">加载中…</p>
        ) : sparks.length === 0 ? (
          <p className="mt-3 text-sm text-stone-400">
            还没有碎片。用上面的快速捕获记下第一条。
          </p>
        ) : (
          <ul className="mt-3 space-y-2">
            {sparks.map((spark) => (
              <li
                key={spark.slug}
                className="rounded-xl border border-stone-200 bg-white p-4"
              >
                <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                  <span className="min-w-0 flex-1 truncate text-sm font-medium text-stone-800">
                    {spark.title}
                  </span>
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
        )}
      </section>
    </div>
  );
}
