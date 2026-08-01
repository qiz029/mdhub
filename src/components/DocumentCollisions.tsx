"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { viewHref, type Collision } from "@/lib/sparks";

// Collision pairs involving the current document. Reads are public (personal
// space; auth is handled at the edge), so this renders for every visitor
// whenever the document has collisions.
export function DocumentCollisions({ slug }: { slug: string }) {
  const [items, setItems] = useState<Collision[] | null>(null);

  useEffect(() => {
    let cancelled = false;
    fetch(`/mdhub/api/collisions?slug=${encodeURIComponent(slug)}`)
      .then((res) => (res.ok ? res.json() : []))
      .then((data: Collision[]) => {
        if (!cancelled) setItems(data);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [slug]);

  if (!items || items.length === 0) return null;

  return (
    <section
      aria-labelledby="document-collisions-title"
      className="mt-10 border-t border-stone-100 pt-6"
    >
      <h2
        id="document-collisions-title"
        className="text-sm font-semibold text-stone-800"
      >
        碰撞
      </h2>
      <ul className="mt-3 space-y-3">
        {items.map((item) => {
          const other =
            item.slug_a === slug
              ? { slug: item.slug_b, title: item.title_b }
              : { slug: item.slug_a, title: item.title_a };
          return (
            <li
              key={item.id}
              className="rounded-xl border border-stone-200 bg-white p-4"
            >
              <div className="flex items-baseline gap-2 text-sm">
                <Link
                  href={viewHref(other.slug)}
                  className="min-w-0 flex-1 truncate font-medium text-stone-800 hover:underline"
                >
                  {other.title}
                </Link>
                <span className="text-xs tabular-nums text-stone-400">
                  {item.score.toFixed(2)}
                </span>
                <span className="rounded-full bg-stone-100 px-2 py-0.5 text-xs text-stone-500">
                  {item.verdict === "confirmed"
                    ? "已确认"
                    : item.verdict === "dismissed"
                      ? "已忽略"
                      : "待策展"}
                </span>
              </div>
              {item.explanation && (
                <p className="mt-2 text-sm leading-6 text-stone-600">
                  {item.explanation}
                </p>
              )}
              {item.question && (
                <p className="mt-1.5 text-sm leading-6 text-stone-500">
                  <span className="font-medium text-stone-600">问题：</span>
                  {item.question}
                </p>
              )}
            </li>
          );
        })}
      </ul>
    </section>
  );
}
