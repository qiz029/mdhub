"use client";

import Link from "next/link";
import {
  BookOpen,
  Check,
  ExternalLink,
  Lightbulb,
  Link2,
  MessageCircleQuestion,
  Sparkles,
  X,
  type LucideIcon,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  buildEmergenceFeed,
  type EmergenceItem,
} from "@/lib/emergence";
import {
  readingSparkMarkdown,
  sparkSlug,
  viewHref,
  type Collision,
} from "@/lib/sparks";
import type { RelatedDocument } from "@/lib/universe";

const emergenceIcons: Record<EmergenceItem["kind"], LucideIcon> = {
  reflection: Lightbulb,
  question: MessageCircleQuestion,
  connection: Link2,
  related: BookOpen,
};

function currentReadingAnchor(): string {
  const blocks = Array.from(
    document.querySelectorAll<HTMLElement>(
      ".prose-md p, .prose-md li, .prose-md blockquote, .prose-md h2, .prose-md h3",
    ),
  );
  if (blocks.length === 0) return "";
  const targetY = window.innerHeight * 0.38;
  const visible = blocks.filter((block) => {
    const rect = block.getBoundingClientRect();
    return rect.bottom > 72 && rect.top < window.innerHeight;
  });
  const nearest = (visible.length > 0 ? visible : blocks).sort((left, right) => {
    const leftDistance = Math.abs(left.getBoundingClientRect().top - targetY);
    const rightDistance = Math.abs(right.getBoundingClientRect().top - targetY);
    return leftDistance - rightDistance;
  })[0];
  const text = (nearest.textContent || "").replace(/\s+/g, " ").trim();
  return [...text].slice(0, 180).join("");
}

function documentEndpoint(slug: string): string {
  return (
    "/mdhub/api/document/" +
    slug
      .split("/")
      .map((part) => encodeURIComponent(part))
      .join("/")
  );
}

function EmergenceCard({
  item,
  sourceSlug,
  sourceTitle,
}: {
  item: EmergenceItem;
  sourceSlug: string;
  sourceTitle: string;
}) {
  const [responding, setResponding] = useState(false);
  const [draft, setDraft] = useState("");
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState("");
  const responseTriggerRef = useRef<HTMLButtonElement>(null);
  const savedStatusRef = useRef<HTMLParagraphElement>(null);
  const Icon = emergenceIcons[item.kind];

  useEffect(() => {
    if (saved) savedStatusRef.current?.focus();
  }, [saved]);

  async function saveResponse() {
    const text = draft.trim();
    if (!text || saving) return;
    setSaving(true);
    setError("");
    try {
      const now = new Date();
      const slug = sparkSlug(now, Math.random);
      const response = await fetch(documentEndpoint(slug), {
        method: "PUT",
        headers: { "Content-Type": "text/markdown; charset=utf-8" },
        body: readingSparkMarkdown(
          text,
          {
            sourceSlug,
            sourceTitle,
            emergenceTitle: item.title,
            emergenceBody: [item.detail, item.body].filter(Boolean).join("\n\n"),
          },
          now,
        ),
      });
      if (!response.ok) {
        const result = (await response.json().catch(() => ({}))) as {
          error?: string;
        };
        throw new Error(result.error || `保存失败（HTTP ${response.status}）`);
      }
      setDraft("");
      setResponding(false);
      setSaved(true);
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : "保存失败");
    } finally {
      setSaving(false);
    }
  }

  return (
    <article className="rounded-xl border border-stone-200 bg-white p-4">
      <div className="flex items-start gap-3">
        <span className="inline-flex size-9 shrink-0 items-center justify-center rounded-lg bg-stone-100 text-[var(--accent)]">
          <Icon size={17} aria-hidden="true" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex items-center justify-between gap-2">
            <p className="text-xs font-semibold uppercase tracking-wider text-stone-400">
              {item.provenance}
            </p>
            {item.score !== undefined && (
              <span
                className="text-xs tabular-nums text-stone-400"
                title="关系强度"
              >
                {item.score.toFixed(2)}
              </span>
            )}
          </div>
          <h3 className="mt-1 text-sm font-semibold text-stone-900">
            {item.title}
          </h3>
          {item.quote && (
            <blockquote className="mt-2 line-clamp-3 border-l-2 border-stone-200 pl-3 text-xs leading-5 text-stone-500">
              {item.quote}
            </blockquote>
          )}
          {item.detail && (
            <p className="mt-2 text-sm leading-6 text-stone-500">
              {item.detail}
            </p>
          )}
          <p className="mt-2 text-sm leading-6 text-stone-600">{item.body}</p>
          {item.relatedSlug && item.relatedTitle && (
            <Link
              href={viewHref(item.relatedSlug)}
              className="mt-3 inline-flex min-h-10 items-center gap-1.5 text-sm font-medium text-stone-700 hover:text-stone-900"
            >
              {item.relatedTitle}
              <ExternalLink size={13} aria-hidden="true" />
            </Link>
          )}
          <div className="mt-3 border-t border-stone-100 pt-3">
            {saved ? (
              <p
                ref={savedStatusRef}
                role="status"
                aria-live="polite"
                tabIndex={0}
                className="inline-flex items-center gap-1.5 text-xs font-medium text-[var(--accent)]"
              >
                <Check size={14} aria-hidden="true" /> 已保存为你的 Spark
              </p>
            ) : responding ? (
              <div className="space-y-2">
                <textarea
                  value={draft}
                  onChange={(event) => setDraft(event.target.value)}
                  rows={3}
                  autoFocus
                  placeholder="写下你的判断…"
                  className="w-full resize-y rounded-lg border border-stone-300 bg-stone-50 px-3 py-2 text-base leading-6 text-stone-800 placeholder:text-stone-400 focus:bg-white sm:text-sm"
                />
                <p className="text-[11px] leading-4 text-stone-400">
                  只有你写下的文字会成为 Spark；系统线索仅作为上下文附带。
                </p>
                {error && (
                  <p role="alert" className="text-xs text-red-600">
                    {error}
                  </p>
                )}
                <div className="flex gap-2">
                  <button
                    type="button"
                    disabled={saving || !draft.trim()}
                    onClick={saveResponse}
                    className="min-h-10 rounded-lg bg-stone-900 px-3 text-xs font-medium text-white disabled:opacity-40"
                  >
                    {saving ? "保存中…" : "保存为 Spark"}
                  </button>
                  <button
                    type="button"
                    onClick={() => {
                      setResponding(false);
                      setError("");
                      requestAnimationFrame(() =>
                        responseTriggerRef.current?.focus(),
                      );
                    }}
                    className="min-h-10 rounded-lg border border-stone-300 px-3 text-xs text-stone-600 hover:bg-stone-50"
                  >
                    取消
                  </button>
                </div>
              </div>
            ) : (
              <button
                ref={responseTriggerRef}
                type="button"
                onClick={() => setResponding(true)}
                className="min-h-10 text-xs font-medium text-stone-500 hover:text-stone-900"
              >
                写下我的想法
              </button>
            )}
          </div>
        </div>
      </div>
    </article>
  );
}

export function ReadingCompanion({
  slug,
  title,
  relatedDocuments,
}: {
  slug: string;
  title: string;
  relatedDocuments: RelatedDocument[];
}) {
  const [open, setOpen] = useState(false);
  const [anchor, setAnchor] = useState("");
  const [collisions, setCollisions] = useState<Collision[]>([]);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const panelRef = useRef<HTMLElement>(null);
  const closeButtonRef = useRef<HTMLButtonElement>(null);
  const items = useMemo(
    () =>
      buildEmergenceFeed(
        { slug, title, ...(anchor ? { anchor } : {}) },
        relatedDocuments,
        collisions,
      ),
    [anchor, collisions, relatedDocuments, slug, title],
  );

  useEffect(() => {
    const controller = new AbortController();
    fetch(`/mdhub/api/collisions?slug=${encodeURIComponent(slug)}`, {
      signal: controller.signal,
    })
      .then((response) => (response.ok ? response.json() : []))
      .then((data: Collision[]) => setCollisions(data))
      .catch(() => {});
    return () => controller.abort();
  }, [slug]);

  useEffect(() => {
    if (!open) return;
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    requestAnimationFrame(() => closeButtonRef.current?.focus());
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setOpen(false);
        triggerRef.current?.focus();
      }
    };
    window.addEventListener("keydown", closeOnEscape);
    return () => {
      document.body.style.overflow = previousOverflow;
      window.removeEventListener("keydown", closeOnEscape);
    };
  }, [open]);

  function toggle() {
    if (!open) setAnchor(currentReadingAnchor());
    setOpen(!open);
  }

  function keepFocusInPanel(event: React.KeyboardEvent<HTMLElement>) {
    if (event.key !== "Tab") return;
    const focusable = Array.from(
      panelRef.current?.querySelectorAll<HTMLElement>(
        'a[href], button:not(:disabled), textarea:not(:disabled), input:not(:disabled), select:not(:disabled), [tabindex]:not([tabindex="-1"])',
      ) ?? [],
    );
    if (focusable.length === 0) return;
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }

  return (
    <>
      {open && (
        <div
          aria-hidden="true"
          className="fixed inset-0 z-[70] bg-stone-900/10 backdrop-blur-[1px]"
          onClick={() => {
            setOpen(false);
            triggerRef.current?.focus();
          }}
        />
      )}
      <div
        className={`fixed bottom-20 right-4 flex max-h-[calc(100dvh-6rem)] flex-col items-end gap-2 sm:right-5 ${open ? "z-[80]" : "z-40"}`}
      >
        {open && (
          <section
            ref={panelRef}
            id="reading-companion"
            role="dialog"
            aria-modal="true"
            aria-labelledby="reading-companion-title"
            onKeyDown={keepFocusInPanel}
            className="min-h-0 max-h-[42rem] w-[calc(100vw-2rem)] overflow-y-auto rounded-2xl border border-stone-200 bg-stone-50 p-4 shadow-2xl sm:w-96"
          >
            <div className="sticky top-0 z-10 -mx-1 -mt-1 flex items-start justify-between gap-3 bg-stone-50/95 px-1 pb-3 backdrop-blur">
              <div>
                <p className="text-xs font-semibold uppercase tracking-[0.16em] text-[var(--accent)]">
                  Reading Emergence
                </p>
                <h2
                  id="reading-companion-title"
                  className="mt-1 text-lg font-semibold text-stone-900"
                >
                  阅读中涌现
                </h2>
                <p className="mt-1 text-xs leading-5 text-stone-500">
                  来自当前阅读和资料库的少量线索，不会自动写入 Sparks。
                </p>
              </div>
              <button
                ref={closeButtonRef}
                type="button"
                aria-label="关闭阅读涌现"
                onClick={() => {
                  setOpen(false);
                  triggerRef.current?.focus();
                }}
                className="inline-flex size-10 shrink-0 items-center justify-center rounded-full text-stone-400 hover:bg-stone-100 hover:text-stone-800"
              >
                <X size={18} aria-hidden="true" />
              </button>
            </div>
            <div className="space-y-3">
              {items.map((item) => (
                <EmergenceCard
                  key={item.id}
                  item={item}
                  sourceSlug={slug}
                  sourceTitle={title}
                />
              ))}
            </div>
          </section>
        )}
        <button
          ref={triggerRef}
          type="button"
          aria-label={`阅读中涌现，${items.length} 条线索`}
          aria-expanded={open}
          aria-controls="reading-companion"
          title="阅读中涌现"
          onClick={toggle}
          className={`inline-flex min-h-11 items-center gap-2 rounded-full border px-4 text-sm font-medium shadow-lg transition-colors ${
            open
              ? "border-stone-800 bg-stone-800 text-white"
              : "border-stone-200 bg-white text-stone-700 hover:bg-stone-50"
          }`}
        >
          <Sparkles size={17} aria-hidden="true" />
          <span>涌现</span>
          <span
            aria-hidden="true"
            className={`rounded-full px-1.5 py-0.5 text-[10px] tabular-nums ${
              open ? "bg-white/15 text-white" : "bg-stone-100 text-stone-500"
            }`}
          >
            {items.length}
          </span>
        </button>
      </div>
    </>
  );
}
