import type { UniverseEdge, UniverseNode } from "./universe";

export type ClusterEdge = {
  sourceCluster: number;
  targetCluster: number;
  count: number;
  similarity: number;
};

export type UniverseViewTransform = { x: number; y: number; k: number };

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
