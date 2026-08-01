"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { viewHref } from "@/lib/sparks";
import type { BlindBox as BlindBoxData } from "@/lib/play";

// Daily blind box: the backend deterministically picks one collision pair
// per day. Only side A is shown up front — guessing which note in your own
// library sits on side B is the game; 揭晓 reveals it. Renders nothing when
// the library has no eligible collisions.
export function BlindBox() {
  const [box, setBox] = useState<BlindBoxData | null>(null);
  const [revealed, setRevealed] = useState(false);

  useEffect(() => {
    let cancelled = false;
    fetch("/mdhub/api/blindbox")
      .then((res) => (res.ok ? res.json() : null))
      .then((data: BlindBoxData | null) => {
        if (!cancelled && data && typeof data.id === "number") setBox(data);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, []);

  if (!box) return null;

  return (
    <section
      aria-labelledby="blindbox-title"
      className="rounded-2xl border border-stone-200 bg-white p-5"
    >
      <div className="flex items-baseline gap-2">
        <h2 id="blindbox-title" className="text-sm font-semibold text-stone-800">
          今日盲盒
        </h2>
        <span className="text-xs text-stone-400">明天换新</span>
      </div>
      <div className="mt-3 grid gap-3 sm:grid-cols-2">
        <div className="rounded-xl bg-stone-50 p-4">
          <Link
            href={viewHref(box.slug_a)}
            className="block truncate text-sm font-medium text-stone-800 hover:underline"
          >
            {box.title_a}
          </Link>
          {box.excerpt_a && (
            <p className="mt-2 line-clamp-4 text-sm leading-6 text-stone-500">
              {box.excerpt_a}
            </p>
          )}
        </div>
        {revealed ? (
          <div className="rounded-xl bg-stone-50 p-4">
            <div className="flex items-baseline gap-2">
              <Link
                href={viewHref(box.slug_b)}
                className="min-w-0 flex-1 truncate text-sm font-medium text-stone-800 hover:underline"
              >
                {box.title_b}
              </Link>
              <span className="text-xs tabular-nums text-stone-400">
                {box.score.toFixed(2)}
              </span>
            </div>
            {box.explanation && (
              <p className="mt-2 text-sm leading-6 text-stone-600">
                {box.explanation}
              </p>
            )}
            {box.question && (
              <p className="mt-1.5 text-sm leading-6 text-stone-500">
                <span className="font-medium text-stone-600">问题：</span>
                {box.question}
              </p>
            )}
          </div>
        ) : (
          <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-stone-300 p-4 text-center">
            <span className="text-2xl font-bold text-stone-300">？</span>
            <p className="mt-2 text-sm text-stone-500">
              这篇和你库里哪篇有关？
            </p>
            <button
              type="button"
              onClick={() => setRevealed(true)}
              className="mt-3 rounded-md bg-stone-900 px-4 py-2 text-sm font-medium text-white hover:opacity-85"
            >
              揭晓
            </button>
          </div>
        )}
      </div>
    </section>
  );
}
