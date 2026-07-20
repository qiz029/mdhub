"use client";

import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useRouter } from "next/navigation";
import type { CommentThread } from "@/lib/comments";

const API = "/mdhub/api/comments/";
const BLOCK_SELECTOR = "p, li, blockquote, h2, h3, h4, pre, td";

type DraftAnchor = { quote: string; prefix: string; suffix: string };

const norm = (s: string) => s.replace(/\s+/g, " ").trim();

// Locate the block element a thread is anchored to. Quote matching is
// whitespace-normalized; when several blocks contain the quote, the stored
// prefix disambiguates.
function findBlock(
  root: HTMLElement,
  t: { quote: string; prefix: string },
): Element | null {
  const q = norm(t.quote);
  if (!q) return null;
  const hits = Array.from(root.querySelectorAll(BLOCK_SELECTOR)).filter((b) =>
    norm(b.textContent || "").includes(q),
  );
  if (hits.length <= 1) return hits[0] || null;
  const pq = norm(`${t.prefix || ""} ${t.quote}`);
  return hits.find((b) => norm(b.textContent || "").includes(pq)) || hits[0];
}

function clampX(x: number): number {
  if (typeof window === "undefined") return x;
  return Math.min(Math.max(x, 152), window.innerWidth - 152);
}

function AuthorBadge({ author }: { author: string }) {
  const isAgent = author !== "用户";
  return (
    <span
      className={`rounded-full px-2 py-0.5 text-[10px] font-medium ${
        isAgent
          ? "bg-amber-100 text-amber-700"
          : "bg-stone-100 text-stone-500"
      }`}
    >
      {author}
    </span>
  );
}

function CommentForm({
  submitLabel,
  onSubmit,
  onCancel,
}: {
  submitLabel: string;
  onSubmit: (text: string) => Promise<void>;
  onCancel?: () => void;
}) {
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  async function submit() {
    if (!text.trim() || busy) return;
    setBusy(true);
    setErr("");
    try {
      await onSubmit(text.trim());
      setText("");
    } catch (e) {
      setErr(e instanceof Error ? e.message : "提交失败");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-2">
      <textarea
        value={text}
        onChange={(e) => setText(e.target.value)}
        placeholder="写下你的评论…"
        rows={3}
        autoFocus
        className="w-full rounded-md border border-stone-300 bg-white px-2.5 py-2 text-base sm:text-sm text-stone-800 placeholder:text-stone-400 focus:outline-none focus:border-stone-400 resize-y"
      />
      {err && <div className="text-xs text-red-500">{err}</div>}
      <div className="flex gap-2">
        <button
          type="button"
          onClick={submit}
          disabled={busy || !text.trim()}
          className="rounded-md bg-stone-800 px-3 py-1.5 text-xs font-medium text-white hover:bg-stone-700 disabled:opacity-40"
        >
          {busy ? "…" : submitLabel}
        </button>
        {onCancel && (
          <button
            type="button"
            onClick={onCancel}
            className="rounded-md border border-stone-300 px-3 py-1.5 text-xs text-stone-600 hover:bg-stone-50"
          >
            取消
          </button>
        )}
      </div>
    </div>
  );
}

function ThreadView({
  thread,
  onReply,
}: {
  thread: CommentThread;
  onReply: (text: string) => Promise<void>;
}) {
  const [replying, setReplying] = useState(false);
  return (
    <div className="my-2 rounded-lg border border-stone-200 bg-stone-50 p-3">
      <div className="mb-2 truncate text-xs text-stone-400">
        「{thread.quote}」
      </div>
      {thread.comments.map((c, i) => (
        <div key={i} className="mb-2.5 last:mb-0">
          <span className="flex items-center gap-2">
            <AuthorBadge author={c.author} />
            <span className="text-xs text-stone-400">{c.time}</span>
          </span>
          <div className="mt-0.5 whitespace-pre-line text-sm text-stone-600">
            {c.text}
          </div>
        </div>
      ))}
      {replying ? (
        <CommentForm
          submitLabel="回复"
          onSubmit={async (t) => {
            await onReply(t);
            setReplying(false);
          }}
          onCancel={() => setReplying(false)}
        />
      ) : (
        <button
          type="button"
          onClick={() => setReplying(true)}
          className="mt-1 text-xs text-stone-400 hover:text-stone-600"
        >
          回复
        </button>
      )}
    </div>
  );
}

export function ArticleComments({
  html,
  slug,
  threads,
}: {
  html: string;
  slug: string;
  threads: CommentThread[];
}) {
  const router = useRouter();
  const ref = useRef<HTMLDivElement>(null);
  const [containers, setContainers] = useState<
    { id: string; el: HTMLElement }[]
  >([]);
  const [orphans, setOrphans] = useState<CommentThread[]>([]);
  const [openId, setOpenId] = useState<string | null>(null);
  const [selBtn, setSelBtn] = useState<{ x: number; y: number } | null>(null);
  const [draft, setDraft] = useState<{
    x: number;
    y: number;
    anchor: DraftAnchor;
  } | null>(null);
  const draftAnchor = useRef<DraftAnchor | null>(null);

  // (Re)anchor threads into the article DOM: highlight the block, append a
  // marker chip, and insert a slot element the thread portal renders into.
  useEffect(() => {
    const root = ref.current;
    if (!root) return;
    root
      .querySelectorAll(".mdhub-comment-marker, .mdhub-comment-thread")
      .forEach((n) => n.remove());
    root
      .querySelectorAll(".mdhub-has-comment")
      .forEach((n) => n.classList.remove("mdhub-has-comment"));

    const next: { id: string; el: HTMLElement }[] = [];
    const lost: CommentThread[] = [];
    for (const t of threads) {
      const block = findBlock(root, t);
      if (!block) {
        lost.push(t);
        continue;
      }
      block.classList.add("mdhub-has-comment");
      const marker = document.createElement("button");
      marker.type = "button";
      marker.className = "mdhub-comment-marker";
      marker.textContent = `评论 ${t.comments.length}`;
      marker.addEventListener("click", (e) => {
        e.stopPropagation();
        setOpenId((cur) => (cur === t.id ? null : t.id));
      });
      block.appendChild(marker);
      const slot = document.createElement("div");
      slot.className = "mdhub-comment-thread";
      block.after(slot);
      next.push({ id: t.id, el: slot });
    }
    setContainers(next);
    setOrphans(lost);

    return () => {
      root
        .querySelectorAll(".mdhub-comment-marker, .mdhub-comment-thread")
        .forEach((n) => n.remove());
    };
  }, [html, threads]);

  // Text selection → floating "评论" button (works with mouse and touch).
  useEffect(() => {
    const root = ref.current;
    if (!root) return;

    function capture(): boolean {
      const sel = window.getSelection();
      if (!sel || sel.isCollapsed || !root!.contains(sel.anchorNode)) {
        return false;
      }
      const quote = norm(sel.toString());
      if (quote.length < 2 || quote.length > 500) return false;
      const el =
        sel.anchorNode instanceof Element
          ? sel.anchorNode
          : sel.anchorNode?.parentElement;
      const blockText = norm(
        el?.closest(BLOCK_SELECTOR)?.textContent || "",
      );
      const idx = blockText.indexOf(quote);
      draftAnchor.current = {
        quote,
        prefix: idx > 0 ? blockText.slice(Math.max(0, idx - 40), idx) : "",
        suffix:
          idx >= 0
            ? blockText.slice(idx + quote.length, idx + quote.length + 40)
            : "",
      };
      const rect = sel.getRangeAt(0).getBoundingClientRect();
      setSelBtn({ x: rect.left + rect.width / 2, y: rect.bottom + 6 });
      return true;
    }

    function onMouseUp() {
      window.setTimeout(() => {
        if (!capture()) setSelBtn(null);
      }, 10);
    }
    function onTouchEnd() {
      window.setTimeout(() => {
        if (!capture()) setSelBtn(null);
      }, 350);
    }
    function onSelectionChange() {
      const sel = window.getSelection();
      if (!sel || sel.isCollapsed) setSelBtn(null);
    }

    document.addEventListener("mouseup", onMouseUp);
    document.addEventListener("touchend", onTouchEnd);
    document.addEventListener("selectionchange", onSelectionChange);
    return () => {
      document.removeEventListener("mouseup", onMouseUp);
      document.removeEventListener("touchend", onTouchEnd);
      document.removeEventListener("selectionchange", onSelectionChange);
    };
  }, []);

  async function post(body: Record<string, unknown>): Promise<string> {
    const res = await fetch(API, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`);
    router.refresh();
    return data.id;
  }

  async function submitDraft(text: string) {
    if (!draftAnchor.current) return;
    const id = await post({
      slug,
      text,
      anchor: draftAnchor.current,
    });
    setOpenId(id);
    setDraft(null);
    setSelBtn(null);
    window.getSelection()?.removeAllRanges();
  }

  const replyTo = (threadId: string) => async (text: string) => {
    await post({ slug, text, reply: threadId });
  };

  return (
    <article>
      <div
        ref={ref}
        className="prose-md text-stone-800"
        dangerouslySetInnerHTML={{ __html: html }}
      />

      {containers.map(({ id, el }) => {
        const t = threads.find((th) => th.id === id);
        if (!t) return null;
        return createPortal(
          openId === id ? <ThreadView thread={t} onReply={replyTo(id)} /> : null,
          el,
          id,
        );
      })}

      {orphans.length > 0 && (
        <div className="mt-10 border-t border-stone-200 pt-4">
          <p className="mb-2 text-xs text-stone-400">
            以下评论锚定的原文已被修改：
          </p>
          {orphans.map((t) => (
            <ThreadView key={t.id} thread={t} onReply={replyTo(t.id)} />
          ))}
        </div>
      )}

      {selBtn && !draft && (
        <button
          type="button"
          className="fixed z-50 -translate-x-1/2 rounded-full bg-stone-800 px-3 py-1.5 text-xs text-white shadow-lg"
          style={{ left: clampX(selBtn.x), top: selBtn.y }}
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => {
            if (draftAnchor.current) {
              setDraft({ ...selBtn, anchor: draftAnchor.current });
              setSelBtn(null);
            }
          }}
        >
          评论
        </button>
      )}

      {draft && (
        <div
          className="fixed z-50 w-72 -translate-x-1/2 rounded-lg border border-stone-200 bg-white p-3 shadow-xl"
          style={{ left: clampX(draft.x), top: draft.y }}
        >
          <div className="mb-2 truncate text-xs text-stone-400">
            「{draft.anchor.quote}」
          </div>
          <CommentForm
            submitLabel="发布"
            onSubmit={submitDraft}
            onCancel={() => setDraft(null)}
          />
        </div>
      )}
    </article>
  );
}
