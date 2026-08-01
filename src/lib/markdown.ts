import { marked } from "marked";
import path from "node:path";

export type TocItem = { id: string; text: string; level: number };

export function extractTitle(md: string, fallback: string): string {
  const match = md.match(/^\s*#\s+(.+?)\s*$/m);
  if (match) return match[1].trim();
  return fallback;
}

function stripFrontmatter(md: string): string {
  return md.replace(/^---\s*\r?\n[\s\S]*?\r?\n---\s*(?:\r?\n|$)/, "");
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

function escapeMarkdownLabel(s: string): string {
  return s.replace(/([\\\[\]])/g, "\\$1");
}

// URLs are rendered into dangerouslySetInnerHTML, so protocol validation must
// happen here rather than at individual call sites. Browsers decode numeric
// entities and discard ASCII whitespace while parsing a scheme; mirror those
// transformations before applying the allowlist.
function normalizedForProtocolCheck(href: string): string {
  return href
    .trim()
    .replace(/&#x([0-9a-f]+);?/gi, (_match, hex: string) =>
      String.fromCodePoint(Number.parseInt(hex, 16)),
    )
    .replace(/&#([0-9]+);?/g, (_match, decimal: string) =>
      String.fromCodePoint(Number.parseInt(decimal, 10)),
    )
    .replace(/&colon;?/gi, ":")
    .replace(/&(tab|newline);?/gi, "")
    .replace(/[\u0000-\u0020\u007f]+/g, "");
}

function hasAllowedProtocol(href: string, protocols: ReadonlySet<string>): boolean {
  try {
    const url = new URL(
      normalizedForProtocolCheck(href),
      "https://mdhub.invalid/",
    );
    return protocols.has(url.protocol);
  } catch {
    return false;
  }
}

const LINK_PROTOCOLS = new Set(["http:", "https:", "mailto:", "tel:"]);
const IMAGE_PROTOCOLS = new Set(["http:", "https:"]);
const SAFE_INLINE_IMAGE =
  /^data:image\/(?:png|jpeg|gif|webp|avif);base64,[a-z0-9+/=\s]+$/i;

function isSafeLinkHref(href: string): boolean {
  return hasAllowedProtocol(href, LINK_PROTOCOLS);
}

function isSafeImageHref(href: string): boolean {
  return (
    SAFE_INLINE_IMAGE.test(href.trim()) ||
    hasAllowedProtocol(href, IMAGE_PROTOCOLS)
  );
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
    const label = escapeMarkdownLabel(display);
    const slug = resolveLink(target);
    if (slug) {
      const href =
        "/mdhub/view/" +
        slug
          .split("/")
          .map((s) => encodeURIComponent(s))
          .join("/");
      return `[${label}](${href})`;
    }
    return `[${label}](mdhub-dead-link://unpublished "未发布的链接")`;
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

  renderer.html = ({ text }: any) => escapeHtml(String(text ?? ""));

  const origLink = renderer.link.bind(renderer);
  renderer.link = ({ href, title, text, tokens }: any) => {
    const linkHref = String(href ?? "");
    if (linkHref === "mdhub-dead-link://unpublished") {
      return `<span class="mdhub-dead-link" title="未发布的链接">${escapeHtml(String(text ?? ""))}</span>`;
    }
    if (!isSafeLinkHref(linkHref)) {
      return escapeHtml(String(text ?? ""));
    }
    return origLink({ href: linkHref, title, text, tokens } as any);
  };

  const origImage = renderer.image.bind(renderer);
  renderer.image = ({ href, title, text }: any) => {
    const caption = (title || text || "").trim();
    const wrap = (img: string) =>
      caption
        ? `<figure>${img}<figcaption>${escapeHtml(caption)}</figcaption></figure>`
        : img;
    if (!href) return wrap(lazyImg(origImage({ href: "", title, text } as any)));
    if (!isSafeImageHref(href)) {
      return escapeHtml(String(text ?? ""));
    }
    if (
      /^https?:\/\//i.test(href) ||
      href.startsWith("//") ||
      href.startsWith("data:") ||
      href.startsWith("/mdhub/api/image")
    ) {
      return wrap(lazyImg(origImage({ href, title, text } as any)));
    }
    const key = href.startsWith("/")
      ? path.posix.normalize(href.slice(1))
      : path.posix.normalize(path.posix.join(slugDir, href));
    const url = `/mdhub/api/image?path=${encodeURIComponent(key)}`;
    return wrap(lazyImg(origImage({ href: url, title, text } as any)));
  };

  const html = await marked.parse(body, {
    renderer,
    async: true,
    gfm: true,
    breaks: false,
  });
  return { html, toc };
}
