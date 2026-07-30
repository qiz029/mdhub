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
      className="hidden xl:block fixed right-6 top-24 w-56 max-h-[70vh] overflow-y-auto"
    >
      <p className="mb-2 text-xs font-semibold uppercase tracking-widest text-stone-400">
        目录
      </p>
      <ul className="space-y-1 border-l border-stone-200">
        {items.map((it) => (
          <li key={it.id}>
            <a
              href={`#${it.id}`}
              className={`block text-xs leading-5 transition-colors ${
                it.level === 3 ? "pl-6" : "pl-3"
              } ${
                activeId === it.id
                  ? "font-semibold text-[var(--accent)]"
                  : "text-stone-500 hover:text-stone-800"
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
