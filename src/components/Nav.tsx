import Link from "next/link";
import { Flame } from "lucide-react";
import { SearchBar } from "./SearchBar";

type NavProps = {
  active?: "documents" | "sparks" | "translations" | "universe";
};

function tabClass(active: boolean): string {
  return `relative inline-flex min-h-9 items-center px-1 text-sm font-medium transition-colors ${
    active
      ? "text-stone-900 after:absolute after:inset-x-1 after:-bottom-[13px] after:h-0.5 after:rounded-full after:bg-[var(--accent)]"
      : "text-stone-400 hover:text-stone-700"
  }`;
}

export function Nav({ active = "documents" }: NavProps) {
  return (
    <header className="border-b border-stone-200 bg-white">
      <div className="mx-auto flex max-w-[90rem] flex-wrap items-center gap-x-5 gap-y-3 px-4 py-3 sm:px-6">
        <div className="flex shrink-0 items-center gap-4">
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
            className="inline-flex items-center gap-2 text-[15px] font-semibold tracking-tight text-stone-900 transition-colors hover:text-stone-700"
          >
            <img
              src="/mdhub/mdhub-logo.svg"
              alt=""
              aria-hidden="true"
              width={28}
              height={28}
              className="size-7 shrink-0"
            />
            <span>Markdown Hub</span>
          </Link>
        </div>
        <nav aria-label="主视图" className="flex shrink-0 items-center gap-4">
          <Link
            href="/"
            aria-current={active === "documents" ? "page" : undefined}
            className={tabClass(active === "documents")}
          >
            Documents
          </Link>
          <Link
            href="/sparks"
            aria-current={active === "sparks" ? "page" : undefined}
            className={tabClass(active === "sparks")}
          >
            Sparks
          </Link>
          <Link
            href="/translations"
            aria-current={active === "translations" ? "page" : undefined}
            className={tabClass(active === "translations")}
          >
            Translate
          </Link>
          <Link
            href="/universe"
            aria-current={active === "universe" ? "page" : undefined}
            className={tabClass(active === "universe")}
          >
            Universe
          </Link>
        </nav>
        <div className="ml-auto min-w-[14rem] flex-1 sm:max-w-sm">
          <SearchBar />
        </div>
      </div>
    </header>
  );
}
