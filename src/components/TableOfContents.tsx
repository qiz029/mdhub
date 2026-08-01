"use client";

import { useEffect, useState } from "react";
import type { TocItem } from "@/lib/markdown";

export function TableOfContents({ items }: { items: TocItem[] }) {
  const [activeId, setActiveId] = useState<string | null>(null);

  useEffect(() => {
    const headings = items
      .map((it) => document.getElementById(it.id))
      .filter((el): el is HTMLElement => el !== null);
    if (headings.length === 0) return;

    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) setActiveId(entry.target.id);
        }
      },
      { rootMargin: "-80px 0px -70% 0px" },
    );
    headings.forEach((h) => observer.observe(h));
    return () => observer.disconnect();
  }, [items]);

  if (items.length < 3) return null;

  return (
    <nav
      aria-label="目录"
      className="sticky top-8 hidden max-h-[calc(100vh-4rem)] w-72 self-start overflow-y-auto py-10 pr-4 xl:block"
    >
      <p className="mb-3 text-sm font-semibold tracking-wide text-stone-500">
        目录
      </p>
      <ul className="space-y-0.5 border-l-2 border-stone-100">
        {items.map((it) => (
          <li key={it.id}>
            <a
              href={`#${it.id}`}
              className={`block rounded-r-md py-1.5 pr-3 text-sm leading-6 transition-colors ${
                it.level === 3 ? "pl-8" : "pl-4"
              } ${
                activeId === it.id
                  ? "bg-stone-50 font-semibold text-[var(--accent)]"
                  : "text-stone-500 hover:bg-stone-50 hover:text-stone-800"
              }`}
            >
              {it.text}
            </a>
          </li>
        ))}
      </ul>
    </nav>
  );
}
