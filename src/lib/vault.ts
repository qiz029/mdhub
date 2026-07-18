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

        const excerpt = content.replace(/\s+/g, " ").trim().slice(0, 200);

        results.push({
          slug,
          filePath,
          title,
          source,
          publishedAt: fileStat.mtimeMs,
          excerpt,
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

export async function getPublishedFile(
  slug: string,
): Promise<{ filePath: string; content: string } | null> {
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

    return { filePath, content: raw };
  } catch {
    return null;
  }
}
