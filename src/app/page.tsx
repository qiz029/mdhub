import { listPublished, type PublishedEntry } from "@/lib/vault";
import Link from "next/link";
import { Nav } from "@/components/Nav";

export const dynamic = "force-dynamic";

function fmtDate(ms: number): string {
  return new Date(ms).toLocaleString("zh-CN", {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function Card({ entry }: { entry: PublishedEntry }) {
  return (
    <Link href={`/view/${entry.slug}`} className="block group">
      <article className="border-b border-stone-100 py-6 transition-colors hover:bg-stone-50/50 -mx-3 px-3 rounded-lg">
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2 mb-1.5">
              <span className="text-sm font-semibold tracking-tight text-stone-900 group-hover:text-stone-700">
                {entry.title}
              </span>
              <span
                className={`shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium ${
                  entry.source === "agent"
                    ? "bg-amber-100 text-amber-700"
                    : "bg-stone-100 text-stone-500"
                }`}
              >
                {entry.source === "agent" ? "Agent" : "Todd"}
              </span>
            </div>
            <p className="text-sm text-stone-500 line-clamp-2">
              {entry.excerpt || "No preview"}
            </p>
          </div>
          <time className="shrink-0 text-xs text-stone-400 mt-0.5">
            {fmtDate(entry.publishedAt)}
          </time>
        </div>
      </article>
    </Link>
  );
}

export default async function HomePage() {
  const entries = await listPublished();

  return (
    <div>
      <Nav />
      <main className="mx-auto max-w-4xl xl:max-w-5xl px-6 py-10 space-y-8">
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-stone-900">
            Published
          </h1>
          <p className="mt-1 text-sm text-stone-400">
            Notes and reports from the family vault
          </p>
        </div>

        {entries.length === 0 ? (
          <div className="py-24 text-center">
            <p className="text-sm text-stone-400">Nothing published yet.</p>
            <p className="mt-1 text-xs text-stone-300">
              Add <code className="text-stone-400">publish: true</code> to a
              note&apos;s frontmatter to see it here.
            </p>
          </div>
        ) : (
          <div className="overflow-hidden">
            {entries.map((entry) => (
              <Card key={entry.slug} entry={entry} />
            ))}
          </div>
        )}
      </main>
    </div>
  );
}
