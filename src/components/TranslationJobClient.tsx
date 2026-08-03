"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import {
  translationProgress,
  translationStageLabel,
  translationViewHref,
  type TranslationJobDetail,
} from "@/lib/translations";

const TERMINAL_STATES = new Set(["draft_ready", "published", "failed", "cancelled", "needs_input"]);

export function TranslationJobClient({ id }: { id: string }) {
  const [job, setJob] = useState<TranslationJobDetail | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  const [uploading, setUploading] = useState(false);

  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    async function load() {
      try {
        const response = await fetch(`/mdhub/api/translation-jobs/${encodeURIComponent(id)}`, {
          cache: "no-store",
        });
        if (!response.ok) throw new Error(`加载失败（HTTP ${response.status}）`);
        const nextJob = (await response.json()) as TranslationJobDetail;
        if (cancelled) return;
        setJob(nextJob);
        setError("");
        if (!TERMINAL_STATES.has(nextJob.state)) {
          timer = setTimeout(load, 3000);
        }
      } catch (loadError) {
        if (!cancelled) {
          setError(loadError instanceof Error ? loadError.message : "加载失败");
          timer = setTimeout(load, 5000);
        }
      }
    }
    load();
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [id]);

  async function action(name: "cancel" | "retry" | "publish") {
    if (busy) return;
    setBusy(name);
    setError("");
    try {
      const response = await fetch(
        `/mdhub/api/translation-jobs/${encodeURIComponent(id)}/${name}`,
        { method: "POST" },
      );
      const result = (await response.json().catch(() => ({}))) as
        | TranslationJobDetail
        | { error?: string };
      if (!response.ok) {
        throw new Error("error" in result && result.error ? result.error : `操作失败（HTTP ${response.status}）`);
      }
      window.location.reload();
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : "操作失败");
    } finally {
      setBusy("");
    }
  }

  async function uploadPDF(file: File) {
    if (uploading || busy) return;
    setUploading(true);
    setError("");
    try {
      const form = new FormData();
      form.set("file", file);
      const response = await fetch(
        `/mdhub/api/translation-jobs/${encodeURIComponent(id)}/source`,
        { method: "POST", body: form },
      );
      const result = (await response.json().catch(() => ({}))) as
        | TranslationJobDetail
        | { error?: string };
      if (!response.ok) {
        throw new Error("error" in result && result.error ? result.error : `上传失败（HTTP ${response.status}）`);
      }
      window.location.reload();
    } catch (uploadError) {
      setError(uploadError instanceof Error ? uploadError.message : "上传失败");
    } finally {
      setUploading(false);
    }
  }

  if (!job) {
    return <p className="py-16 text-center text-sm text-stone-400">{error || "加载任务中…"}</p>;
  }

  const progress = translationProgress(job);
  const translatedChunks = job.chunks?.filter((chunk) => chunk.translated_text.trim()) ?? [];

  return (
    <div className="space-y-8">
      <section className="rounded-2xl border border-stone-200 bg-white p-5 sm:p-6">
        <div className="flex flex-col gap-5 sm:flex-row sm:items-start sm:justify-between">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <span className="rounded-full bg-amber-100 px-2 py-0.5 text-[11px] font-semibold uppercase text-amber-700">
                {job.source.kind}
              </span>
              <span className="text-xs text-stone-400">{job.target_language}</span>
              {job.model && <span className="text-xs text-stone-400">{job.model}</span>}
            </div>
            <h1 className="mt-3 break-words text-xl font-bold text-stone-900 sm:text-2xl">
              {job.source.title || job.source.identifier || "论文翻译"}
            </h1>
            <a
              href={job.source.canonical_url}
              target="_blank"
              rel="noreferrer"
              className="mt-2 block max-w-2xl truncate text-xs text-stone-400 hover:text-stone-700"
            >
              {job.source.canonical_url}
            </a>
          </div>
          <div className="flex shrink-0 flex-wrap gap-2">
            {job.state === "draft_ready" && (
              <button
                type="button"
                onClick={() => action("publish")}
                disabled={!!busy}
                className="min-h-10 rounded-lg bg-stone-900 px-4 text-sm font-semibold text-white disabled:opacity-40"
              >
                {busy === "publish" ? "发布中…" : "发布到 MDHub"}
              </button>
            )}
            {job.state === "failed" && (
              <button
                type="button"
                onClick={() => action("retry")}
                disabled={!!busy}
                className="min-h-10 rounded-lg bg-stone-900 px-4 text-sm font-semibold text-white disabled:opacity-40"
              >
                重新执行
              </button>
            )}
            {!TERMINAL_STATES.has(job.state) && (
              <button
                type="button"
                onClick={() => action("cancel")}
                disabled={!!busy}
                className="min-h-10 rounded-lg border border-stone-300 px-4 text-sm text-stone-600 disabled:opacity-40"
              >
                取消
              </button>
            )}
          </div>
        </div>

        <div className="mt-7 flex items-center justify-between text-sm">
          <span className="font-medium text-stone-700">{translationStageLabel(job.stage)}</span>
          <span className="tabular-nums text-stone-400">
            {job.progress_total > 0
              ? `${job.progress_current} / ${job.progress_total}`
              : `${progress}%`}
          </span>
        </div>
        <div className="mt-2 h-2 overflow-hidden rounded-full bg-stone-100">
          <div
            className="h-full rounded-full bg-[var(--accent)] transition-[width] duration-500"
            style={{ width: `${progress}%` }}
          />
        </div>

        {job.error && (
          <div className="mt-5 rounded-lg border border-red-200 bg-red-50 p-3 text-sm leading-6 text-red-700">
            {job.error}
          </div>
        )}
        {job.state === "needs_input" && (
          <div className="mt-5 rounded-xl border border-amber-200 bg-amber-50 p-4">
            <p className="text-sm font-medium text-amber-900">当前地址无法直接取得论文全文。</p>
            <p className="mt-1 text-xs leading-5 text-amber-700">
              上传 PDF 后会保留这个任务，并从持久化来源重新开始完整翻译。
            </p>
            <label className="mt-3 inline-flex min-h-10 cursor-pointer items-center rounded-lg bg-stone-900 px-4 text-sm font-semibold text-white has-[:disabled]:cursor-not-allowed has-[:disabled]:opacity-40">
              {uploading ? "上传中…" : "选择 PDF 并继续"}
              <input
                type="file"
                accept="application/pdf,.pdf"
                disabled={uploading || !!busy}
                className="sr-only"
                onChange={(event) => {
                  const file = event.currentTarget.files?.[0];
                  if (file) void uploadPDF(file);
                }}
              />
            </label>
          </div>
        )}
        {error && <p className="mt-4 text-sm text-red-600">{error}</p>}

        {job.validation && (
          <div className="mt-5 rounded-lg border border-stone-200 bg-stone-50 p-3 text-sm text-stone-600">
            完整性校验：{job.validation.translated_chunks}/{job.validation.source_chunks} 个片段
            {job.validation.complete ? "，通过" : "，未通过"}
            {job.validation.artifact_hash && (
              <span className="mt-1 block font-mono text-[11px] text-stone-400">
                PDF {job.validation.artifact_hash.slice(0, 12)}
              </span>
            )}
          </div>
        )}

        {job.output_slug && (
          <Link
            href={translationViewHref(job.output_slug)}
            className="mt-5 inline-flex min-h-10 items-center rounded-lg border border-stone-300 px-4 text-sm font-medium text-stone-700 hover:bg-stone-50"
          >
            {job.state === "published" ? "阅读已发布译文" : "打开译稿"} →
          </Link>
        )}
      </section>

      {translatedChunks.length > 0 && (
        <section>
          <div>
            <h2 className="text-lg font-bold text-stone-900">双语检查</h2>
            <p className="mt-1 text-sm text-stone-400">已完成的片段会在 Agent 工作期间逐步出现。</p>
          </div>
          <div className="mt-4 space-y-4">
            {translatedChunks.map((chunk) => (
              <article key={chunk.ordinal} className="overflow-hidden rounded-xl border border-stone-200 bg-white">
                <div className="border-b border-stone-100 px-4 py-2 text-xs font-medium text-stone-400">
                  片段 {chunk.ordinal + 1}
                </div>
                <div className="grid md:grid-cols-2">
                  <pre className="whitespace-pre-wrap border-b border-stone-100 bg-stone-50 p-4 font-sans text-sm leading-7 text-stone-500 md:border-b-0 md:border-r">
                    {chunk.source_text}
                  </pre>
                  <pre className="whitespace-pre-wrap p-4 font-sans text-sm leading-7 text-stone-800">
                    {chunk.translated_text}
                  </pre>
                </div>
              </article>
            ))}
          </div>
        </section>
      )}
    </div>
  );
}
