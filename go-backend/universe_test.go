package main

import (
	"slices"
	"testing"
)

func TestBuildSemanticUniverseKeepsMutualNearestNeighbours(t *testing.T) {
	docs := []universeDocument{
		{Slug: "a", Title: "Alpha"},
		{Slug: "b", Title: "Beta"},
		{Slug: "c", Title: "Gamma"},
	}
	vectors := map[string][]float32{
		"a": {1, 0},
		"b": {0.9, 0.1},
		"c": {-1, 0},
	}

	graph := buildSemanticUniverse(docs, vectors, 1)

	if len(graph.Edges) != 1 {
		t.Fatalf("edges = %+v, want one mutual nearest-neighbour edge", graph.Edges)
	}
	edge := graph.Edges[0]
	if edge.Source != "a" || edge.Target != "b" {
		t.Fatalf("edge = %+v, want a--b", edge)
	}
	if edge.Similarity <= 0.9 {
		t.Fatalf("similarity = %v, want a strong semantic edge", edge.Similarity)
	}

	degrees := []int{graph.Nodes[0].Degree, graph.Nodes[1].Degree, graph.Nodes[2].Degree}
	slices.Sort(degrees)
	if !slices.Equal(degrees, []int{0, 1, 1}) {
		t.Fatalf("degrees = %v, want [0 1 1]", degrees)
	}
}

func TestBuildSemanticUniverseConnectsAnOtherwiseIsolatedEmbeddedDocument(t *testing.T) {
	docs := []universeDocument{
		{Slug: "a", Title: "Alpha"},
		{Slug: "b", Title: "Beta"},
		{Slug: "c", Title: "Gamma"},
		{Slug: "draft", Title: "No vector"},
	}
	vectors := map[string][]float32{
		"a": {1, 0},
		"b": {0.99, 0.1},
		"c": {0.6, 0.8},
	}

	graph := buildSemanticUniverse(docs, vectors, 1)

	if len(graph.Edges) != 2 {
		t.Fatalf("edges = %+v, want mutual a--b plus c's strongest fallback", graph.Edges)
	}
	if graph.Meta.EmbeddedDocuments != 3 || graph.Meta.Documents != 4 {
		t.Fatalf("meta = %+v, want 3/4 embedding coverage", graph.Meta)
	}
	var draft universeNode
	for _, node := range graph.Nodes {
		if node.ID == "draft" {
			draft = node
		}
	}
	if draft.Embedded || draft.Degree != 0 {
		t.Fatalf("unembedded node = %+v, want visible but disconnected", draft)
	}
}

func TestSemanticClusterAssignmentsSeparatesWeaklyConnectedCommunities(t *testing.T) {
	nodes := []universeNode{
		{ID: "a", Embedded: true},
		{ID: "b", Embedded: true},
		{ID: "c", Embedded: true},
		{ID: "d", Embedded: true},
		{ID: "missing"},
	}
	edges := []universeEdge{
		{Source: "a", Target: "b", Similarity: 0.96},
		{Source: "c", Target: "d", Similarity: 0.94},
		{Source: "b", Target: "c", Similarity: 0.08},
	}

	clusters := semanticClusterAssignments(nodes, edges)

	if clusters["a"] != clusters["b"] {
		t.Fatalf("a and b clusters = %d, %d; want the same cluster", clusters["a"], clusters["b"])
	}
	if clusters["c"] != clusters["d"] {
		t.Fatalf("c and d clusters = %d, %d; want the same cluster", clusters["c"], clusters["d"])
	}
	if clusters["a"] == clusters["c"] {
		t.Fatalf("clusters = %+v; want the weakly connected communities separated", clusters)
	}
	if clusters["missing"] != -1 {
		t.Fatalf("unembedded cluster = %d; want -1", clusters["missing"])
	}
}

func TestBuildSemanticUniverseExposesDocumentWordCount(t *testing.T) {
	docs := []universeDocument{{Slug: "long-note", Title: "Long note", WordCount: 2345}}

	graph := buildSemanticUniverse(docs, nil, 2)

	if len(graph.Nodes) != 1 || graph.Nodes[0].WordCount != 2345 {
		t.Fatalf("nodes = %+v; want document word count exposed", graph.Nodes)
	}
}

func TestRelatedDocumentsRanksSemanticCandidates(t *testing.T) {
	source := []float32{1, 0}
	candidates := []relatedCandidate{
		{slug: "alpha", title: "Alpha", vector: []float32{1, 1}},
		{slug: "beta", title: "Beta", vector: []float32{1, 0}},
		{slug: "gamma", title: "Gamma", vector: []float32{0, 1}},
	}

	related := relatedDocuments(source, candidates, 2)
	if len(related) != 2 {
		t.Fatalf("related = %+v, want two results", related)
	}
	if related[0].Slug != "beta" || related[0].Title != "Beta" || related[0].Similarity != 1 {
		t.Fatalf("first related = %+v, want Beta at 1.0", related[0])
	}
	if related[1].Slug != "alpha" || related[1].Title != "Alpha" || related[1].Similarity < 0.70 || related[1].Similarity > 0.71 {
		t.Fatalf("second related = %+v, want Alpha near 0.707", related[1])
	}
}

func TestRelatedDocumentsReturnsEmptyForDisconnectedDocument(t *testing.T) {
	related := relatedDocuments(nil, nil, 5)
	if related == nil || len(related) != 0 {
		t.Fatalf("related = %#v, want non-nil empty list", related)
	}
}

func TestUniverseCacheKeyChangesWhenWordCountChanges(t *testing.T) {
	docs := []universeDocument{{Slug: "note", Title: "Note", WordCount: 100}}
	first := universeCacheKey(docs, 1)
	docs[0].WordCount = 200

	if second := universeCacheKey(docs, 1); second == first {
		t.Fatal("cache key did not change with document word count")
	}
}

func TestCachedSemanticUniverseInvalidatesWhenVectorsChange(t *testing.T) {
	universeCache.Lock()
	universeCache.ready = false
	universeCache.Unlock()
	t.Cleanup(func() {
		universeCache.Lock()
		universeCache.ready = false
		universeCache.Unlock()
	})

	docs := []universeDocument{{Slug: "a"}, {Slug: "b"}}
	vectors := map[string][]float32{"a": {1, 0}, "b": {1, 0}}
	first := cachedSemanticUniverse(docs, vectors, 10)
	if len(first.Edges) != 1 {
		t.Fatalf("first graph edges = %+v, want one edge", first.Edges)
	}

	vectors["b"] = []float32{-1, 0}
	stale := cachedSemanticUniverse(docs, vectors, 10)
	if len(stale.Edges) != 1 {
		t.Fatal("same vector generation should reuse the cached graph")
	}
	fresh := cachedSemanticUniverse(docs, vectors, 11)
	if len(fresh.Edges) != 0 {
		t.Fatalf("new vector generation edges = %+v, want rebuilt graph", fresh.Edges)
	}
}
