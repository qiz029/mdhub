import path from "node:path";
import { Nav } from "@/components/Nav";
import { ViewerActions } from "@/components/ViewerActions";
import { ArticleComments } from "@/components/ArticleComments";
import { getPublishedFile } from "@/lib/vault";
import { getComments } from "@/lib/comments";
import { extractTitle, renderMarkdown } from "@/lib/markdown";

export const dynamic = "force-dynamic";

function fmtDate(ms: number): string {
  return new Date(ms).toLocaleString("zh-CN", {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
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

  const fileDir = path.dirname(file.filePath);
  const baseName = path.basename(slug);
  const title = extractTitle(file.content, baseName);
  const html = await renderMarkdown(file.content, fileDir);
  const downloadName = path.basename(file.filePath);
  const comments = await getComments(slug);

  return (
    <div>
      <Nav />
      <main className="mx-auto w-full max-w-2xl px-5 sm:px-6 py-10">
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
          <ViewerActions markdown={file.content} downloadName={downloadName} />
        </div>
        <ArticleComments html={html} slug={slug} threads={comments} />
      </main>
    </div>
  );
}
