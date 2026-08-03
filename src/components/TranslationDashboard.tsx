"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import {
  translationProgress,
  translationStageLabel,
  type PaperSource,
  type TranslationJob,
} from "@/lib/translations";

function fmtDate(ms: number): string {
  return new Date(ms).toLocaleString("zh-CN", {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function TranslationDashboard() {
  const router = useRouter();
  const [sourceInput, setSourceInput] = useState("");
  const [preview, setPreview] = useState<PaperSource | null>(null);
  const [jobs, setJobs] = useState<TranslationJob[]>([]);
  const [loading, setLoading] = useState(true);
  const [resolving, setResolving] = useState(false);
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState("");

  async function loadJobs() {
    try {
      const response = await fetch("/mdhub/api/translation-jobs", {
        cache: "no-store",
      });
      if (!response.ok) throw new Error(`加载失败（HTTP ${response.status}）`);
      setJobs((await response.json()) as TranslationJob[]);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "加载失败");
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    loadJobs();
  }, []);

  async function resolveSource(event: React.FormEvent) {
    event.preventDefault();
    const source = sourceInput.trim();
    if (!source || resolving) return;
    setResolving(true);
    setPreview(null);
    setError("");
    try {
      const response = await fetch("/mdhub/api/translation-sources/resolve", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ source }),
      });
      const result = (await response.json().catch(() => ({}))) as
        | PaperSource
        | { error?: string };
      if (!response.ok || !("kind" in result)) {
        throw new Error("error" in result && result.error ? result.error : "无法识别论文地址");
      }
      setPreview(result);
    } catch (resolveError) {
      setError(resolveError instanceof Error ? resolveError.message : "无法识别论文地址");
    } finally {
      setResolving(false);
    }
  }

  async function createJob() {
    if (!preview || creating) return;
    setCreating(true);
    setError("");
    try {
      const response = await fetch("/mdhub/api/translation-jobs", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          source: preview.input,
          target_language: "zh-CN",
          profile: "paper-translate-v1",
        }),
      });
      const result = (await response.json().catch(() => ({}))) as
        | TranslationJob
        | { error?: string };
      if (!response.ok || !("id" in result)) {
        throw new Error("error" in result && result.error ? result.error : "创建任务失败");
      }
      router.push(`/translations/${result.id}`);
    } catch (createError) {
      setError(createError instanceof Error ? createError.message : "创建任务失败");
    } finally {
      setCreating(false);
    }
  }

  return (
    <div className="space-y-10">
      <section className="rounded-2xl border border-stone-200 bg-stone-50 p-4 sm:p-6">
        <form onSubmit={resolveSource} className="space-y-3">
          <label htmlFor="paper-source" className="block text-sm font-semibold text-stone-800">
            论文地址
          </label>
          <div className="flex flex-col gap-2 sm:flex-row">
            <input
              id="paper-source"
              type="text"
              value={sourceInput}
              onChange={(event) => {
                setSourceInput(event.target.value);
                setPreview(null);
              }}
              placeholder="粘贴 arXiv、论文页面或直接 PDF 地址"
              className="min-h-11 min-w-0 flex-1 rounded-lg border border-stone-300 bg-white px-3 text-base text-stone-900 placeholder:text-stone-400 focus:border-stone-500 focus:outline-none"
            />
            <button
              type="submit"
              disabled={!sourceInput.trim() || resolving}
              className="min-h-11 rounded-lg bg-stone-900 px-5 text-sm font-semibold text-white disabled:cursor-not-allowed disabled:opacity-40"
            >
              {resolving ? "识别中…" : "识别论文"}
            </button>
          </div>
          <p className="text-xs leading-5 text-stone-400">
            第一版支持 arXiv 和最终返回 PDF 的公开地址（包括带查询参数的下载链接）。普通网页、登录墙或付费来源会提示稍后上传 PDF。
          </p>
        </form>

        {preview && (
          <div className="mt-5 rounded-xl border border-stone-200 bg-white p-4">
            <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <span className="rounded-full bg-amber-100 px-2 py-0.5 text-[11px] font-semibold uppercase text-amber-700">
                    {preview.kind}
                  </span>
                  {preview.version && <span className="text-xs text-stone-400">{preview.version}</span>}
                </div>
                <p className="mt-2 break-all text-sm font-medium text-stone-800">
                  {preview.title || preview.identifier || preview.canonical_url}
                </p>
                <a
                  href={preview.canonical_url}
                  target="_blank"
                  rel="noreferrer"
                  className="mt-1 block truncate text-xs text-stone-400 hover:text-stone-700"
                >
                  {preview.canonical_url}
                </a>
              </div>
              <button
                type="button"
                onClick={createJob}
                disabled={creating}
                className="min-h-11 shrink-0 rounded-lg bg-[var(--accent)] px-5 text-sm font-semibold text-white disabled:cursor-not-allowed disabled:opacity-40"
              >
                {creating
                  ? "提交中…"
                  : preview.kind === "web"
                    ? "交给 Agent 检查并翻译"
                    : "交给 Agent 完整翻译"}
              </button>
            </div>
          </div>
        )}

        {error && <p className="mt-4 text-sm text-red-600">{error}</p>}
      </section>

      <section>
        <div className="flex items-end justify-between gap-4">
          <div>
            <h2 className="text-lg font-bold text-stone-900">翻译任务</h2>
            <p className="mt-1 text-sm text-stone-400">任务可以离开页面继续执行。</p>
          </div>
          <button type="button" onClick={loadJobs} className="text-xs text-stone-400 hover:text-stone-700">
            刷新
          </button>
        </div>
        <div className="mt-4 overflow-hidden rounded-xl border border-stone-200 bg-white">
          {loading ? (
            <p className="p-6 text-sm text-stone-400">加载中…</p>
          ) : jobs.length === 0 ? (
            <p className="p-6 text-sm text-stone-400">还没有翻译任务。</p>
          ) : (
            jobs.map((job) => {
              const progress = translationProgress(job);
              return (
                <Link
                  key={job.id}
                  href={`/translations/${job.id}`}
                  className="block border-b border-stone-100 p-4 transition-colors last:border-0 hover:bg-stone-50"
                >
                  <div className="flex items-start justify-between gap-4">
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-semibold text-stone-800">
                        {job.source.title || job.source.identifier || job.source.canonical_url}
                      </p>
                      <p className="mt-1 text-xs text-stone-400">
                        {translationStageLabel(job.stage)} · {fmtDate(job.updated_at)}
                      </p>
                      <div className="mt-3 h-1.5 overflow-hidden rounded-full bg-stone-100">
                        <div
                          className="h-full rounded-full bg-[var(--accent)] transition-[width]"
                          style={{ width: `${progress}%` }}
                        />
                      </div>
                    </div>
                    <span className="text-xs tabular-nums text-stone-400">{progress}%</span>
                  </div>
                </Link>
              );
            })
          )}
        </div>
      </section>
    </div>
  );
}
