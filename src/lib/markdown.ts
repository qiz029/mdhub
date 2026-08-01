import { marked } from "marked";
import path from "node:path";

export type TocItem = { id: string; text: string; level: number };

export function extractTitle(md: string, fallback: string): string {
  const match = md.match(/^\s*#\s+(.+?)\s*$/m);
  if (match) return match[1].trim();
  return fallback;
}

function stripFrontmatter(md: string): string {
  if (md.startsWith("---")) {
    const second = md.indexOf("---", 3);
    if (second !== -1) return md.slice(second + 3).trimStart();
  }
  return md;
}

function isAbsoluteUrl(href: string): boolean {
  return (
    /^([a-z][a-z0-9+.-]*:)?\/\//i.test(href) || href.startsWith("data:")
  );
}

function lazyImg(html: string): string {
  return html.replace("<img ", '<img loading="lazy" ');
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

// Obsidian-style [[wiki links]] are rewritten before marked sees the source.
// resolveLink returns the target slug, or null for unpublished notes (which
// become inert dead-link spans).
function preprocessWikiLinks(
  md: string,
  resolveLink?: (target: string) => string | null,
): string {
  if (!resolveLink) return md;
  return md.replace(/\[\[([^\]]+)\]\]/g, (_m, inner: string) => {
    const [linkPart, aliasPart] = inner.split("|");
    const target = (linkPart || "").split("#")[0].trim();
    const display = (aliasPart ?? "").trim() || target;
    if (!target) return display;
    const slug = resolveLink(target);
    if (slug) {
      const href =
        "/mdhub/view/" +
        slug
          .split("/")
          .map((s) => encodeURIComponent(s))
          .join("/");
      return `[${display}](${href})`;
    }
    return `<span class="mdhub-dead-link" title="未发布的链接">${escapeHtml(display)}</span>`;
  });
}

// slugDir is the note's directory inside the vault (e.g. "translations"),
// used to resolve relative image paths into vault-relative keys.
export async function renderMarkdown(
  md: string,
  slugDir: string,
  resolveLink?: (target: string) => string | null,
): Promise<{ html: string; toc: TocItem[] }> {
  const body = preprocessWikiLinks(stripFrontmatter(md), resolveLink);
  const renderer = new marked.Renderer();
  const toc: TocItem[] = [];
  let headingCount = 0;

  renderer.heading = ({ text, depth }: any) => {
    const body = escapeHtml(String(text ?? ""));
    if (depth === 2 || depth === 3) {
      const id = `toc-${headingCount++}`;
      toc.push({ id, text: String(text ?? ""), level: depth });
      return `<h${depth} id="${id}">${body}</h${depth}>\n`;
    }
    return `<h${depth}>${body}</h${depth}>\n`;
  };

  const origImage = renderer.image.bind(renderer);
  renderer.image = ({ href, title, text }: any) => {
    const caption = (title || text || "").trim();
    const wrap = (img: string) =>
      caption
        ? `<figure>${img}<figcaption>${escapeHtml(caption)}</figcaption></figure>`
        : img;
    if (!href) return wrap(lazyImg(origImage({ href: "", title, text } as any)));
    if (isAbsoluteUrl(href) || href.startsWith("/mdhub/api/image")) {
      return wrap(lazyImg(origImage({ href, title, text } as any)));
    }
    const key = href.startsWith("/")
      ? path.posix.normalize(href.slice(1))
      : path.posix.normalize(path.posix.join(slugDir, href));
    const url = `/mdhub/api/image?path=${encodeURIComponent(key)}`;
    return wrap(lazyImg(origImage({ href: url, title, text } as any)));
  };

  marked.setOptions({ gfm: true, breaks: false });
  const html = await marked.parse(body, { renderer, async: true });
  return { html, toc };
}
