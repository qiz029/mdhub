import { marked } from "marked";
import path from "node:path";

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

export async function renderMarkdown(
  md: string,
  fileDir: string,
): Promise<string> {
  const body = stripFrontmatter(md);
  const renderer = new marked.Renderer();

  const origImage = renderer.image.bind(renderer);
  renderer.image = ({ href, title, text }: any) => {
    if (!href) return origImage({ href: "", title, text } as any);
    if (isAbsoluteUrl(href) || href.startsWith("/api/image")) {
      return origImage({ href, title, text } as any);
    }
    const abs = path.isAbsolute(href) ? href : path.resolve(fileDir, href);
    const url = `/api/image?path=${encodeURIComponent(abs)}`;
    return origImage({ href: url, title, text } as any);
  };

  marked.setOptions({ gfm: true, breaks: false });
  return await marked.parse(body, { renderer, async: true });
}
