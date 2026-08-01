import { API_URL } from "./config";

export type UniverseNode = {
  id: string;
  title: string;
  excerpt?: string;
  category?: string;
  tags: string[];
  updated: number;
  embedded: boolean;
  degree: number;
  cluster: number;
  word_count: number;
};

export type UniverseEdge = {
  source: string;
  target: string;
  kind: "semantic";
  similarity: number;
};

export type UniverseGraph = {
  nodes: UniverseNode[];
  edges: UniverseEdge[];
  meta: {
    documents: number;
    embedded_documents: number;
    edges: number;
    neighbours: number;
    min_similarity: number;
    max_similarity: number;
  };
};

export type RelatedDocument = {
  slug: string;
  title: string;
  similarity: number;
};

export async function getUniverse(): Promise<UniverseGraph | null> {
  try {
    const res = await fetch(`${API_URL}/api/universe`, { cache: "no-store" });
    if (!res.ok) return null;
    return (await res.json()) as UniverseGraph;
  } catch {
    return null;
  }
}

export async function getRelatedDocuments(
  slug: string,
): Promise<RelatedDocument[]> {
  try {
    const res = await fetch(
      `${API_URL}/api/related?slug=${encodeURIComponent(slug)}`,
      { cache: "no-store" },
    );
    if (!res.ok) return [];
    return (await res.json()) as RelatedDocument[];
  } catch {
    return [];
  }
}
