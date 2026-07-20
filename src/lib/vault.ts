import { readFile, readdir, stat } from "node:fs/promises";
import path from "node:path";
import matter from "gray-matter";
import { VAULT_PATH } from "./config";

export type PublishedEntry = {
  slug: string;
  filePath: string;
  title: string;
  source: "human" | "agent";
  publishedAt: number;
  excerpt: string;
};

async function* walkDir(dir: string): AsyncGenerator<string> {
  const entries = await readdir(dir, { withFileTypes: true });
  for (const entry of entries) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (entry.name.startsWith(".") || entry.name === "node_modules") continue;
      yield* walkDir(full);
    } else if (entry.name.endsWith(".md")) {
      yield full;
    }
  }
}

// Strip markdown syntax so feed excerpts read as plain text.
function plainExcerpt(md: string, max = 200): string {
  let s = md;
  s = s.replace(/```[\s\S]*?```/g, " "); // fenced code
  s = s.replace(/!\[[^\]]*\]\([^)]+\)/g, " "); // images
  s = s.replace(/\[([^\]]*)\]\([^)]+\)/g, "$1"); // links -> text
  s = s.replace(/^\s{0,3}#{1,6}\s+/gm, ""); // heading marks
  s = s.replace(/^\s{0,3}>\s?/gm, ""); // blockquotes
  s = s.replace(/^\s*[-*+]\s+/gm, ""); // unordered lists
  s = s.replace(/^\s*\d+\.\s+/gm, ""); // ordered lists
  s = s.replace(/^\s*[-*_]{3,}\s*$/gm, " "); // hr
  s = s.replace(/\*\*([^*]+)\*\*/g, "$1"); // bold
  s = s.replace(/\*([^*]+)\*/g, "$1"); // italic
  s = s.replace(/`([^`]+)`/g, "$1"); // inline code
  s = s.replace(/\|/g, " "); // table pipes
  s = s.replace(/\s+/g, " ").trim();
  return s.slice(0, max);
}

export async function listPublished(): Promise<PublishedEntry[]> {
  const results: PublishedEntry[] = [];

  try {
    for await (const filePath of walkDir(VAULT_PATH)) {
      try {
        const raw = await readFile(filePath, "utf8");
        const { data, content } = matter(raw);

        if (data.publish !== true) continue;

        const fileStat = await stat(filePath);
        const relPath = path.relative(VAULT_PATH, filePath);
        const slug = relPath.replace(/\\/g, "/").replace(/\.md$/, "");

        let title = data.title as string | undefined;
        if (!title) {
          const match = content.match(/^\s*#\s+(.+?)\s*$/m);
          title = match ? match[1].trim() : slug;
        }

        const source = slug.startsWith("_agent/") ? "agent" : "human";

        results.push({
          slug,
          filePath,
          title,
          source,
          publishedAt: fileStat.mtimeMs,
          excerpt: plainExcerpt(content),
        });
      } catch {
        // Skip unreadable files
      }
    }
  } catch {
    // Vault directory doesn't exist — return empty
  }

  results.sort((a, b) => b.publishedAt - a.publishedAt);
  return results;
}

export type PublishedFile = {
  filePath: string;
  content: string;
  publishedAt: number;
  tags: string[];
};

export async function getPublishedFile(
  slug: string,
): Promise<PublishedFile | null> {
  const cleanSlug = slug.replace(/\.\./g, "").replace(/\\/g, "/");
  const filePath = path.resolve(VAULT_PATH, `${cleanSlug}.md`);

  if (
    !filePath.startsWith(VAULT_PATH + path.sep) &&
    filePath !== VAULT_PATH
  ) {
    return null;
  }

  try {
    const raw = await readFile(filePath, "utf8");
    const { data } = matter(raw);

    if (data.publish !== true) return null;

    const fileStat = await stat(filePath);
    const tags = Array.isArray(data.tags) ? data.tags.map(String) : [];

    return { filePath, content: raw, publishedAt: fileStat.mtimeMs, tags };
  } catch {
    return null;
  }
}
