"use client";

import { useState, useRef, useEffect } from "react";

interface Result {
  slug: string;
  title: string;
  snippet?: string;
}

export function SearchBar() {
  const [q, setQ] = useState("");
  const [results, setResults] = useState<Result[]>([]);
  const [open, setOpen] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const API = process.env.NEXT_PUBLIC_SEARCH_API || "http://localhost:10002";

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key === "k") {
        e.preventDefault();
        inputRef.current?.focus();
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  async function search(text: string): Promise<Result[]> {
    if (text.trim().length < 2) {
      setResults([]);
      setOpen(false);
      return [];
    }
    try {
      const res = await fetch(`${API}/api/search?q=${encodeURIComponent(text)}`);
      const data: Result[] = await res.json();
      setResults(data.slice(0, 8));
      setOpen(true);
      return data;
    } catch {
      setResults([]);
      return [];
    }
  }
  function go(r: Result) {
    setOpen(false);
    window.location.href = `/mdhub/view/${r.slug}`;
  }

  function onChange(text: string) {
    setQ(text);
    if (timerRef.current) clearTimeout(timerRef.current);
    timerRef.current = setTimeout(() => search(text), 200);
  }

  function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    const text = q.trim();
    if (text.length < 2) return;
    if (timerRef.current) clearTimeout(timerRef.current);
    // fetch immediately, navigate to first result
    fetch(`${API}/api/search?q=${encodeURIComponent(text)}`)
      .then((r) => r.json())
      .then((data: Result[]) => {
        if (data.length > 0) go(data[0]);
      })
      .catch(() => {});
  }

  return (
    <form onSubmit={onSubmit} className="relative w-full max-w-sm">
      <input
        ref={inputRef}
        type="text"
        value={q}
        onChange={(e) => onChange(e.target.value)}
        onFocus={() => q.length >= 2 && results.length > 0 && setOpen(true)}
        onBlur={() => setTimeout(() => setOpen(false), 150)}
        placeholder="Search… (⌘K)"
        className="w-full rounded-md border border-stone-300 bg-stone-50 px-3 py-2 text-base text-stone-800 placeholder:text-stone-400 focus:outline-none focus:border-stone-400 focus:bg-white"
      />
      {open && results.length > 0 && (
        <div className="absolute top-full mt-1 w-full rounded-lg border border-stone-200 bg-white shadow-lg z-50 max-h-96 overflow-y-auto">
          {results.map((r) => (
            <div
              key={r.slug}
              onMouseDown={(e) => { e.preventDefault(); go(r); }}
              className="block px-3 py-2 hover:bg-stone-50 border-b border-stone-100 last:border-0 cursor-pointer"
            >
              <div className="text-sm font-medium text-stone-900 truncate">
                {r.title}
              </div>
              {r.snippet && (
                <div
                  className="text-xs text-stone-500 mt-0.5 line-clamp-2"
                  dangerouslySetInnerHTML={{ __html: r.snippet }}
                />
              )}
            </div>
          ))}
        </div>
      )}
    </form>
  );
}
