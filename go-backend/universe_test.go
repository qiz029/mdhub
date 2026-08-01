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
