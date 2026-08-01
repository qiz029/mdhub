import path from "node:path";
import Link from "next/link";
import { Nav } from "@/components/Nav";
import { ViewerActions } from "@/components/ViewerActions";
import { ArticleComments } from "@/components/ArticleComments";
import { ReaderSettings } from "@/components/ReaderSettings";
import { TableOfContents } from "@/components/TableOfContents";
import { CodeCopy } from "@/components/CodeCopy";
import { getPublishedFile, listPublished, type PublishedEntry } from "@/lib/vault";
import { getComments } from "@/lib/comments";
import { extractTitle, renderMarkdown } from "@/lib/markdown";
import {
  getRelatedDocuments,
  type RelatedDocument,
} from "@/lib/universe";

export const dynamic = "force-dynamic";

function fmtDate(ms: number): string {
  return new Date(ms).toLocaleString("zh-CN", {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}

// Obsidian-style wiki-link resolution: exact slug first, then note name
// (last slug segment, case-insensitive), then title (case-insensitive).
function makeLinkResolver(
  entries: PublishedEntry[],
): (target: string) => string | null {
  const bySlug = new Map(entries.map((e) => [e.slug, e.slug]));
  const byName = new Map<string, string>();
  const byTitle = new Map<string, string>();
  for (const e of entries) {
    const name = path.posix.basename(e.slug).toLowerCase();
    if (!byName.has(name)) byName.set(name, e.slug);
    const title = e.title.toLowerCase();
    if (!byTitle.has(title)) byTitle.set(title, e.slug);
  }
  return (target) => {
    if (bySlug.has(target)) return bySlug.get(target)!;
    const lower = target.toLowerCase();
    return byName.get(lower) ?? byTitle.get(lower) ?? null;
  };
}

function viewHref(slug: string): string {
  return (
    "/view/" +
    slug
      .split("/")
      .map((s) => encodeURIComponent(s))
      .join("/")
  );
}

function RelatedDocumentLinks({ items }: { items: RelatedDocument[] }) {
  if (items.length === 0) return null;
  return (
    <section
      aria-labelledby="related-documents-title"
      className="mt-10 border-t border-stone-100 pt-6"
    >
      <h2
        id="related-documents-title"
        className="text-sm font-semibold text-stone-800"
      >
        相关文档
      </h2>
      <ul className="mt-3 space-y-1">
        {items.map((item) => (
          <li key={item.slug}>
            <Link
              href={viewHref(item.slug)}
              className="group flex min-h-10 items-center gap-3 rounded-lg px-2 text-sm text-stone-600 transition-colors hover:bg-stone-50 hover:text-stone-900"
            >
              <span className="min-w-0 flex-1 truncate">{item.title}</span>
              <span className="text-xs tabular-nums text-stone-400">
                {item.similarity.toFixed(2)}
              </span>
              <span
                aria-hidden="true"
                className="text-stone-300 group-hover:text-stone-500"
              >
                →
              </span>
            </Link>
          </li>
        ))}
      </ul>
    </section>
  );
}

export default async function ViewPage({
  params,
}: {
  params: Promise<{ slug: string[] }>;
}) {
  const { slug: segments } = await params;
  const slug = segments.map((s) => decodeURIComponent(s)).join("/");
  const file = await getPublishedFile(slug);

  if (!file) {
    return (
      <div>
        <Nav />
        <main className="mx-auto max-w-2xl px-6 py-24 text-center">
          <p className="text-xs uppercase tracking-widest text-stone-400">
            404
          </p>
          <h1 className="mt-3 text-2xl font-semibold text-stone-900">
            Not found or not published
          </h1>
          <p className="mt-3 text-stone-600">
            This page doesn&apos;t exist or its frontmatter doesn&apos;t have{" "}
            <code className="text-stone-400">publish: true</code>.
          </p>
        </main>
      </div>
    );
  }

  const slugDir = path.posix.dirname(slug);
  const fileDir = slugDir === "." ? "" : slugDir;
  const baseName = path.posix.basename(slug);
  const title = extractTitle(file.content, baseName);
  const [entries, comments, relatedDocuments] = await Promise.all([
    listPublished(),
    getComments(slug),
    getRelatedDocuments(slug),
  ]);
  const { html, toc } = await renderMarkdown(
    file.content,
    fileDir,
    makeLinkResolver(entries),
  );
  const downloadName = baseName + ".md";

  // entries are newest-first: prev = newer article, next = older article
  const idx = entries.findIndex((e) => e.slug === slug);
  const prev = idx > 0 ? entries[idx - 1] : null;
  const next = idx >= 0 && idx < entries.length - 1 ? entries[idx + 1] : null;

  return (
    <div>
      <Nav />
      <div
        className={`mx-auto grid w-full max-w-[100rem] grid-cols-1 ${
          toc.length >= 3
            ? "xl:grid-cols-[18rem_minmax(0,1fr)] xl:gap-10 xl:px-6"
            : ""
        }`}
      >
        <TableOfContents items={toc} />
        <main
          className="mx-auto w-full px-5 py-10 sm:px-6"
          style={{ maxWidth: "var(--reader-width, 42rem)" }}
        >
          <div className="mb-8 flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
            <div className="min-w-0">
              <h1 className="text-2xl sm:text-3xl font-bold text-stone-900">
                {title}
              </h1>
              <div className="mt-2.5 flex flex-wrap items-center gap-x-3 gap-y-1.5 text-xs text-stone-400">
                <time>{fmtDate(file.publishedAt)}</time>
                {file.tags.map((t) => (
                  <span
                    key={t}
                    className="rounded-full bg-stone-100 px-2 py-0.5 text-stone-500"
                  >
                    #{t}
                  </span>
                ))}
              </div>
            </div>
            <ViewerActions
              markdown={file.content}
              downloadName={downloadName}
              slug={slug}
            />
          </div>
          <ArticleComments html={html} slug={slug} threads={comments} />
          <CodeCopy />
          <RelatedDocumentLinks items={relatedDocuments} />
          <nav className="mt-10 flex items-start justify-between gap-4 border-t border-stone-100 pt-6">
            <div className="min-w-0 flex-1">
              {prev && (
                <Link
                  href={viewHref(prev.slug)}
                  className="group block text-sm text-stone-500 hover:text-stone-800"
                >
                  <span className="text-xs text-stone-400">← 上一篇</span>
                  <span className="mt-1 block truncate font-medium">
                    {prev.title}
                  </span>
                </Link>
              )}
            </div>
            <div className="min-w-0 flex-1 text-right">
              {next && (
                <Link
                  href={viewHref(next.slug)}
                  className="group block text-sm text-stone-500 hover:text-stone-800"
                >
                  <span className="text-xs text-stone-400">下一篇 →</span>
                  <span className="mt-1 block truncate font-medium">
                    {next.title}
                  </span>
                </Link>
              )}
            </div>
          </nav>
        </main>
      </div>
      <ReaderSettings />
    </div>
  );
}
