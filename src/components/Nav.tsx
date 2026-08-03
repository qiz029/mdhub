import Link from "next/link";
import {
  Files,
  Flame,
  Languages,
  Orbit,
  Sparkles,
  type LucideIcon,
} from "lucide-react";
import { SearchBar } from "./SearchBar";
import { ThemePicker } from "./ThemePicker";

type NavProps = {
  active?: "documents" | "sparks" | "translations" | "universe";
};

function tabClass(active: boolean): string {
  return `relative inline-flex min-h-11 min-w-11 items-center justify-center gap-1.5 px-3 text-sm font-medium transition-colors sm:min-h-9 sm:min-w-0 sm:px-1 ${
    active
      ? "text-stone-900 after:absolute after:inset-x-1 after:-bottom-[13px] after:h-0.5 after:rounded-full after:bg-[var(--accent)]"
      : "text-stone-400 hover:text-stone-700"
  }`;
}

function NavIcon({ icon: Icon }: { icon: LucideIcon }) {
  return <Icon size={17} strokeWidth={1.8} aria-hidden="true" />;
}

export function Nav({ active = "documents" }: NavProps) {
  return (
    <header className="border-b border-stone-200 bg-white">
      <div className="mx-auto flex max-w-[90rem] flex-wrap items-center gap-x-5 gap-y-3 px-4 py-3 sm:px-6">
        <div className="order-1 flex shrink-0 items-center gap-4">
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
        <nav
          aria-label="主视图"
          className="order-3 flex w-full shrink-0 items-center justify-between gap-2 lg:order-2 lg:w-auto lg:justify-start lg:gap-4"
        >
          <Link
            href="/"
            aria-label="Documents"
            aria-current={active === "documents" ? "page" : undefined}
            className={tabClass(active === "documents")}
          >
            <NavIcon icon={Files} />
            <span className="hidden sm:inline">Documents</span>
          </Link>
          <Link
            href="/sparks"
            aria-label="Sparks"
            aria-current={active === "sparks" ? "page" : undefined}
            className={tabClass(active === "sparks")}
          >
            <NavIcon icon={Sparkles} />
            <span className="hidden sm:inline">Sparks</span>
          </Link>
          <Link
            href="/translations"
            aria-label="Translations"
            aria-current={active === "translations" ? "page" : undefined}
            className={tabClass(active === "translations")}
          >
            <NavIcon icon={Languages} />
            <span className="hidden sm:inline">Translate</span>
          </Link>
          <Link
            href="/universe"
            aria-label="Universe"
            aria-current={active === "universe" ? "page" : undefined}
            className={tabClass(active === "universe")}
          >
            <NavIcon icon={Orbit} />
            <span className="hidden sm:inline">Universe</span>
          </Link>
        </nav>
        <div className="order-4 min-w-0 flex-1 basis-full lg:order-3 lg:ml-auto lg:min-w-[14rem] lg:basis-auto lg:max-w-sm">
          <SearchBar />
        </div>
        <ThemePicker className="order-2 ml-auto lg:order-4 lg:ml-0" />
      </div>
    </header>
  );
}
