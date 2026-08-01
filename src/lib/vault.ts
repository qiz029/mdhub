import { API_URL } from "./config";

export type PublishedEntry = {
  slug: string;
  title: string;
  source: "human" | "agent";
  publishedAt: number;
  excerpt: string;
  category: string;
  tags: string[];
};

type ApiDocumentSummary = {
  slug: string;
  title: string;
  excerpt: string;
  updated: number;
  category?: string;
  tags?: string[];
};

export async function listPublished(): Promise<PublishedEntry[]> {
  try {
    const res = await fetch(`${API_URL}/api/documents`, {
      cache: "no-store",
    });
    if (!res.ok) return [];
    const docs: ApiDocumentSummary[] = await res.json();
    return docs
      .map<PublishedEntry>((d) => ({
        slug: d.slug,
        title: d.title,
        source: d.slug.startsWith("_agent/") ? "agent" : "human",
        publishedAt: d.updated,
        excerpt: d.excerpt,
        category: typeof d.category === "string" ? d.category : "",
        tags: Array.isArray(d.tags) ? d.tags.map(String) : [],
      }))
      .sort(
        (a, b) =>
          b.publishedAt - a.publishedAt || a.slug.localeCompare(b.slug),
      );
  } catch {
    return [];
  }
}

export type PublishedFile = {
  content: string;
  publishedAt: number;
  tags: string[];
};

type ApiDocument = {
  raw_content: string;
  tags: string[];
  updated: number;
};

export async function getPublishedFile(
  slug: string,
): Promise<PublishedFile | null> {
  const encoded = slug
    .split("/")
    .map((s) => encodeURIComponent(s))
    .join("/");
  try {
    const res = await fetch(`${API_URL}/api/documents/${encoded}`, {
      cache: "no-store",
    });
    if (!res.ok) return null;
    const doc: ApiDocument = await res.json();
    return {
      content: doc.raw_content,
      publishedAt: doc.updated,
      tags: Array.isArray(doc.tags) ? doc.tags.map(String) : [],
    };
  } catch {
    return null;
  }
}
