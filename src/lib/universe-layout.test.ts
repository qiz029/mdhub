import assert from "node:assert/strict";
import test from "node:test";
import {
  aggregateClusterEdges,
  deriveUniverseProjection,
  fitUniverseTransform,
  localDocumentEdges,
  selectedCrossClusterEdges,
} from "./universe-layout.ts";
import type { UniverseEdge, UniverseNode } from "./universe.ts";

function node(id: string, cluster: number, degree: number): UniverseNode {
  return {
    id,
    title: id.toUpperCase(),
    tags: [],
    updated: 0,
    embedded: true,
    degree,
    cluster,
    word_count: 1,
  };
}

const nodes: UniverseNode[] = [
  node("a", 0, 2),
  node("b", 0, 1),
  node("c", 1, 2),
  node("d", 1, 1),
];

const edges: UniverseEdge[] = [
  { source: "a", target: "b", kind: "semantic", similarity: 0.9 },
  { source: "c", target: "d", kind: "semantic", similarity: 0.88 },
  { source: "a", target: "c", kind: "semantic", similarity: 0.8 },
  { source: "b", target: "d", kind: "semantic", similarity: 0.7 },
];

const graph = {
  nodes,
  edges,
  meta: {
    documents: 4,
    embedded_documents: 4,
    edges: 4,
    neighbours: 4,
    min_similarity: 0.7,
    max_similarity: 0.9,
  },
};

test("default document edges stay inside clusters", () => {
  assert.deepEqual(
    localDocumentEdges(nodes, edges).map(
      (edge) => `${edge.source}-${edge.target}`,
    ),
    ["a-b", "c-d"],
  );
});

test("cross-cluster relationships aggregate to one cluster edge", () => {
  assert.deepEqual(aggregateClusterEdges(nodes, edges), [
    {
      sourceCluster: 0,
      targetCluster: 1,
      count: 2,
      similarity: 0.75,
    },
  ]);
});

test("selecting a node expands only its cross-cluster relationships", () => {
  assert.deepEqual(selectedCrossClusterEdges(nodes, edges, "a"), [edges[2]]);
  assert.deepEqual(selectedCrossClusterEdges(nodes, edges, null), []);
});

test("fit transform keeps every point inside the padded viewport", () => {
  const points = [
    { x: -100, y: -50, radius: 10 },
    { x: 300, y: 150, radius: 20 },
    { x: 80, y: 500, radius: 5 },
  ];
  const transform = fitUniverseTransform(points, 800, 600, 40);
  for (const point of points) {
    const x = transform.x + point.x * transform.k;
    const y = transform.y + point.y * transform.k;
    const radius = point.radius * transform.k;
    assert.ok(x - radius >= 40 - 1e-6);
    assert.ok(x + radius <= 760 + 1e-6);
    assert.ok(y - radius >= 40 - 1e-6);
    assert.ok(y + radius <= 560 + 1e-6);
  }
});

test("fit transform uses the viewport for a compact graph without over-zooming", () => {
  const transform = fitUniverseTransform(
    [
      { x: 100, y: 100, radius: 10 },
      { x: 300, y: 300, radius: 10 },
    ],
    1000,
    800,
    40,
  );
  assert.equal(transform.k, 1.35);
});

test("universe projection centralizes density, selection, search and metrics", () => {
  const projection = deriveUniverseProjection(graph, "balanced", "a", "D");

  assert.deepEqual(
    projection.localEdges.map((edge) => `${edge.source}-${edge.target}`),
    ["a-b", "c-d"],
  );
  assert.deepEqual(
    projection.renderedEdges.map((edge) => `${edge.source}-${edge.target}`),
    ["a-b", "c-d", "a-c"],
  );
  assert.equal(projection.selected?.id, "a");
  assert.deepEqual(
    projection.related.map(({ node }) => node.id),
    ["b", "c"],
  );
  assert.deepEqual(projection.searchResults.map((item) => item.id), ["d"]);
  assert.equal(projection.clusterCount, 2);
  assert.equal(projection.coverage, 100);
  assert.equal(
    projection.layoutKey,
    deriveUniverseProjection(graph, "balanced", "d", "other").layoutKey,
    "selection and search must not invalidate the force layout",
  );
});
