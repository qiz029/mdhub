import Link from "next/link";
import { Flame } from "lucide-react";
import { SearchBar } from "./SearchBar";

export function Nav() {
  return (
    <header className="border-b border-stone-200 bg-white">
      <div className="mx-auto flex max-w-5xl items-center justify-between gap-4 px-6 py-3">
        <div className="flex items-center gap-4 shrink-0">
          {process.env.NEXT_PUBLIC_HEARTH_URL && (
            <>
              <a
                href={process.env.NEXT_PUBLIC_HEARTH_URL}
                className="flex items-center gap-1.5 text-xs text-stone-400 hover:text-stone-700 transition-colors"
                title="Back to Hearth"
              >
                <Flame size={14} className="text-amber-400" />
                <span>Hearth</span>
              </a>
              <span className="text-stone-200">|</span>
            </>
          )}
          <Link
            href="/"
            className="text-sm font-semibold tracking-tight text-stone-900 hover:text-stone-700"
          >
            Markdown Hub
          </Link>
        </div>
        <SearchBar />
      </div>
    </header>
  );
}
