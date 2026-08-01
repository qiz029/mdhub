import type { UniverseEdge, UniverseGraph, UniverseNode } from "./universe";

export type ClusterEdge = {
  sourceCluster: number;
  targetCluster: number;
  count: number;
  similarity: number;
};

export type UniverseViewTransform = { x: number; y: number; k: number };
export type EdgeDensity = "focused" | "balanced" | "full";

export type RelatedUniverseNode = {
  node: UniverseNode;
  similarity: number;
};

export type UniverseProjection = {
  localEdges: UniverseEdge[];
  clusterEdges: ClusterEdge[];
  layoutKey: string;
  renderedEdges: UniverseEdge[];
  selected: UniverseNode | null;
  related: RelatedUniverseNode[];
  searchResults: UniverseNode[];
  coverage: number;
  clusterCount: number;
};

type FitPoint = { x: number; y: number; radius: number };

function clusterByNode(nodes: UniverseNode[]): Map<string, number> {
  return new Map(nodes.map((node) => [node.id, node.cluster]));
}

function isCrossCluster(
  edge: UniverseEdge,
  clusters: Map<string, number>,
): boolean {
  const source = clusters.get(edge.source);
  const target = clusters.get(edge.target);
  return (
    source != null &&
    target != null &&
    source >= 0 &&
    target >= 0 &&
    source !== target
  );
}

function edgeKey(edge: UniverseEdge): string {
  return edge.source < edge.target
    ? `${edge.source}\u0000${edge.target}`
    : `${edge.target}\u0000${edge.source}`;
}

function semanticEdgesAtLimit(
  nodes: UniverseNode[],
  edges: UniverseEdge[],
  limit: number,
): UniverseEdge[] {
  const incident = new Map(nodes.map((node) => [node.id, [] as UniverseEdge[]]));
  for (const edge of edges) {
    incident.get(edge.source)?.push(edge);
    incident.get(edge.target)?.push(edge);
  }

  const top = new Map<string, Set<string>>();
  for (const [id, candidates] of incident) {
    const neighbours = [...candidates]
      .sort((a, b) => b.similarity - a.similarity)
      .slice(0, limit)
      .map((edge) => (edge.source === id ? edge.target : edge.source));
    top.set(id, new Set(neighbours));
  }

  const visible = edges.filter(
    (edge) =>
      top.get(edge.source)?.has(edge.target) &&
      top.get(edge.target)?.has(edge.source),
  );
  const connected = new Set(visible.flatMap((edge) => [edge.source, edge.target]));
  const keys = new Set(visible.map(edgeKey));
  for (const node of nodes) {
    if (!node.embedded || connected.has(node.id)) continue;
    const strongest = [...(incident.get(node.id) ?? [])].sort(
      (a, b) => b.similarity - a.similarity,
    )[0];
    if (!strongest || keys.has(edgeKey(strongest))) continue;
    visible.push(strongest);
    keys.add(edgeKey(strongest));
    connected.add(strongest.source);
    connected.add(strongest.target);
  }
  return visible.sort((a, b) => b.similarity - a.similarity);
}

export function localDocumentEdges(
  nodes: UniverseNode[],
  edges: UniverseEdge[],
): UniverseEdge[] {
  const clusters = clusterByNode(nodes);
  return edges.filter((edge) => !isCrossCluster(edge, clusters));
}

export function selectedCrossClusterEdges(
  nodes: UniverseNode[],
  edges: UniverseEdge[],
  selectedId: string | null,
): UniverseEdge[] {
  if (!selectedId) return [];
  const clusters = clusterByNode(nodes);
  return edges.filter(
    (edge) =>
      (edge.source === selectedId || edge.target === selectedId) &&
      isCrossCluster(edge, clusters),
  );
}

export function aggregateClusterEdges(
  nodes: UniverseNode[],
  edges: UniverseEdge[],
): ClusterEdge[] {
  const clusters = clusterByNode(nodes);
  const grouped = new Map<
    string,
    {
      sourceCluster: number;
      targetCluster: number;
      count: number;
      total: number;
    }
  >();
  for (const edge of edges) {
    if (!isCrossCluster(edge, clusters)) continue;
    const left = clusters.get(edge.source)!;
    const right = clusters.get(edge.target)!;
    const sourceCluster = Math.min(left, right);
    const targetCluster = Math.max(left, right);
    const key = `${sourceCluster}:${targetCluster}`;
    const group = grouped.get(key) ?? {
      sourceCluster,
      targetCluster,
      count: 0,
      total: 0,
    };
    group.count++;
    group.total += edge.similarity;
    grouped.set(key, group);
  }
  return [...grouped.values()]
    .map(({ sourceCluster, targetCluster, count, total }) => ({
      sourceCluster,
      targetCluster,
      count,
      similarity: total / count,
    }))
    .sort(
      (left, right) =>
        right.count - left.count ||
        right.similarity - left.similarity ||
        left.sourceCluster - right.sourceCluster ||
        left.targetCluster - right.targetCluster,
    );
}

export function deriveUniverseProjection(
  graph: UniverseGraph,
  density: EdgeDensity,
  selectedId: string | null,
  query: string,
): UniverseProjection {
  const limit = density === "focused" ? 2 : density === "balanced" ? 4 : 6;
  const visibleEdges = semanticEdgesAtLimit(graph.nodes, graph.edges, limit);
  const localEdges = localDocumentEdges(graph.nodes, visibleEdges);
  const clusterEdges = aggregateClusterEdges(graph.nodes, visibleEdges);
  const layoutKey = JSON.stringify({
    local: localEdges.map(({ source, target, similarity }) => [
      source,
      target,
      similarity,
    ]),
    clusters: clusterEdges.map(
      ({ sourceCluster, targetCluster, count, similarity }) => [
        sourceCluster,
        targetCluster,
        count,
        similarity,
      ],
    ),
  });
  const renderedEdges = [
    ...localEdges,
    ...selectedCrossClusterEdges(graph.nodes, graph.edges, selectedId),
  ];
  const nodesByID = new Map(graph.nodes.map((node) => [node.id, node]));
  const selected = selectedId ? (nodesByID.get(selectedId) ?? null) : null;
  const related = selectedId
    ? graph.edges
        .flatMap((edge) => {
          const relatedID =
            edge.source === selectedId
              ? edge.target
              : edge.target === selectedId
                ? edge.source
                : null;
          const node = relatedID ? nodesByID.get(relatedID) : null;
          return node ? [{ node, similarity: edge.similarity }] : [];
        })
        .sort((a, b) => b.similarity - a.similarity)
    : [];
  const normalizedQuery = query.trim().toLocaleLowerCase();
  const searchResults = normalizedQuery
    ? graph.nodes
        .filter(
          (node) =>
            node.title.toLocaleLowerCase().includes(normalizedQuery) ||
            node.tags.some((tag) =>
              tag.toLocaleLowerCase().includes(normalizedQuery),
            ),
        )
        .slice(0, 8)
    : [];
  const clusterCount = new Set(
    graph.nodes.filter((node) => node.cluster >= 0).map((node) => node.cluster),
  ).size;
  const coverage = graph.meta.documents
    ? Math.round((graph.meta.embedded_documents / graph.meta.documents) * 100)
    : 0;

  return {
    localEdges,
    clusterEdges,
    layoutKey,
    renderedEdges,
    selected,
    related,
    searchResults,
    coverage,
    clusterCount,
  };
}

export function fitUniverseTransform(
  points: FitPoint[],
  width: number,
  height: number,
  padding = 64,
): UniverseViewTransform {
  const positioned = points.filter(
    (point) => Number.isFinite(point.x) && Number.isFinite(point.y),
  );
  if (positioned.length === 0 || width <= 0 || height <= 0) {
    return { x: 0, y: 0, k: 1 };
  }

  let minX = Infinity;
  let maxX = -Infinity;
  let minY = Infinity;
  let maxY = -Infinity;
  for (const point of positioned) {
    minX = Math.min(minX, point.x - point.radius);
    maxX = Math.max(maxX, point.x + point.radius);
    minY = Math.min(minY, point.y - point.radius);
    maxY = Math.max(maxY, point.y + point.radius);
  }

  const availableWidth = Math.max(1, width - padding * 2);
  const availableHeight = Math.max(1, height - padding * 2);
  const scale = Math.min(
    1.35,
    availableWidth / Math.max(1, maxX - minX),
    availableHeight / Math.max(1, maxY - minY),
  );
  const centerX = (minX + maxX) / 2;
  const centerY = (minY + maxY) / 2;
  return {
    x: width / 2 - centerX * scale,
    y: height / 2 - centerY * scale,
    k: scale,
  };
}
