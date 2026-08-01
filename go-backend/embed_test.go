package main

import (
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestEmbeddingChunksSampleTheWholeDocument(t *testing.T) {
	content := "开头" + strings.Repeat("甲", 3500) + "结尾标记"
	chunks := embeddingChunks("长文档", content)
	if len(chunks) != maxEmbeddingChunks {
		t.Fatalf("chunks = %d, want capped at %d", len(chunks), maxEmbeddingChunks)
	}
	if !strings.Contains(chunks[0], "开头") {
		t.Fatalf("first chunk does not contain document start: %q", chunks[0])
	}
	if !strings.Contains(chunks[len(chunks)-1], "结尾标记") {
		t.Fatalf("last chunk does not sample document end: %q", chunks[len(chunks)-1])
	}
	for _, chunk := range chunks {
		if !strings.HasPrefix(chunk, "长文档\n") {
			t.Fatalf("chunk does not retain title context: %q", chunk)
		}
		if len([]rune(chunk)) > embeddingChunkRunes {
			t.Fatalf("chunk length = %d, want <= %d", len([]rune(chunk)), embeddingChunkRunes)
		}
	}
}

func TestMeanEmbeddingNormalizesChunksBeforePooling(t *testing.T) {
	got, err := meanEmbedding([][]float32{{3, 0}, {0, 4}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || math.Abs(float64(got[0]-got[1])) > 1e-6 {
		t.Fatalf("mean embedding = %v, want equal contribution from both chunks", got)
	}
	if magnitude := math.Sqrt(float64(got[0]*got[0] + got[1]*got[1])); math.Abs(magnitude-1) > 1e-6 {
		t.Fatalf("mean embedding magnitude = %v, want normalized vector", magnitude)
	}
}

func TestEncodeDecodeVecRoundtrip(t *testing.T) {
	in := []float32{0, 1.5, -2.25, math.MaxFloat32, -math.MaxFloat32, 3.14}
	got := decodeVec(encodeVec(in))
	if len(got) != len(in) {
		t.Fatalf("decodeVec(encodeVec) len = %d, want %d", len(got), len(in))
	}
	for i := range in {
		if got[i] != in[i] {
			t.Errorf("vec[%d] = %v, want %v", i, got[i], in[i])
		}
	}
	if got := decodeVec(encodeVec(nil)); len(got) != 0 {
		t.Errorf("empty roundtrip = %v, want empty", got)
	}
}

func TestDecodeVecBadLength(t *testing.T) {
	for _, n := range []int{1, 2, 3, 5, 7} {
		if got := decodeVec(make([]byte, n)); got != nil {
			t.Errorf("decodeVec(%d bytes) = %v, want nil", n, got)
		}
	}
}

func TestCosine(t *testing.T) {
	if got := cosine([]float32{1, 0}, []float32{0, 1}); got != 0 {
		t.Errorf("orthogonal = %v, want 0", got)
	}
	if got := cosine([]float32{1, 2, 3}, []float32{1, 2, 3}); math.Abs(got-1) > 1e-6 {
		t.Errorf("identical = %v, want 1", got)
	}
	if got := cosine([]float32{1, 0}, []float32{-1, 0}); math.Abs(got+1) > 1e-6 {
		t.Errorf("opposite = %v, want -1", got)
	}
	if got := cosine([]float32{1, 2}, []float32{1, 2, 3}); got != 0 {
		t.Errorf("length mismatch = %v, want 0", got)
	}
	if got := cosine([]float32{0, 0}, []float32{1, 0}); got != 0 {
		t.Errorf("zero vector = %v, want 0", got)
	}
}

func TestMergeHits(t *testing.T) {
	mtimes := map[string]int64{"a": 100, "b": 200, "c": 300, "d": 400}

	t.Run("keyword only, no query vector", func(t *testing.T) {
		kw := map[string]float64{"a": 2, "b": 4}
		hits := mergeHits(kw, nil, map[string][]float32{"c": {1, 0}}, mtimes)
		if len(hits) != 2 {
			t.Fatalf("hits = %v, want 2", hits)
		}
		// scores normalized by maxKw (4): b=1.0 > a=0.5
		if hits[0].slug != "b" || hits[0].score != 1.0 || !hits[0].kw {
			t.Errorf("hits[0] = %+v, want b/1.0/kw", hits[0])
		}
		if hits[1].slug != "a" || hits[1].score != 0.5 {
			t.Errorf("hits[1] = %+v, want a/0.5", hits[1])
		}
	})

	t.Run("semantic only, threshold boundary", func(t *testing.T) {
		// cosine(q, v) for v={x, sqrt(1-x^2)} equals x
		q := []float32{1, 0}
		vecs := map[string][]float32{
			"a": {0.39, 0.9207}, // below 0.4 -> excluded
			"b": {0.4, 0.9165},  // at 0.4 boundary -> included
			"c": {0.9, 0.4359},  // above -> included
			"d": {-1, 0},        // negative -> clamped to 0, excluded
		}
		hits := mergeHits(nil, q, vecs, mtimes)
		var slugs []string
		for _, h := range hits {
			slugs = append(slugs, h.slug)
			if h.kw {
				t.Errorf("semantic hit %s must not be marked kw", h.slug)
			}
		}
		slices.Sort(slugs)
		if !slices.Equal(slugs, []string{"b", "c"}) {
			t.Errorf("slugs = %v, want [b c]", slugs)
		}
		if hits[0].slug != "c" {
			t.Errorf("hits[0] = %s, want c (highest sim)", hits[0].slug)
		}
	})

	t.Run("hybrid ordering", func(t *testing.T) {
		q := []float32{1, 0}
		kw := map[string]float64{"a": 1, "b": 2} // kwNorm: a=0.5, b=1.0
		vecs := map[string][]float32{
			"a": {0.9, 0.4359}, // sim ~0.9 -> combined ~1.4
			"b": {0, 1},        // sim 0    -> combined 1.0
			"c": {0.8, 0.6},    // sim 0.8, no kw -> combined 0.8
		}
		hits := mergeHits(kw, q, vecs, mtimes)
		if len(hits) != 3 {
			t.Fatalf("hits = %v, want 3", hits)
		}
		got := []string{hits[0].slug, hits[1].slug, hits[2].slug}
		if !slices.Equal(got, []string{"a", "b", "c"}) {
			t.Errorf("order = %v, want [a b c]", got)
		}
	})

	t.Run("maxKwScore zero edge", func(t *testing.T) {
		// empty kw map: maxKw=0, must not divide by zero
		hits := mergeHits(map[string]float64{}, []float32{1, 0},
			map[string][]float32{"a": {1, 0}}, mtimes)
		if len(hits) != 1 || hits[0].slug != "a" || hits[0].score != 1.0 || hits[0].kw {
			t.Errorf("hits = %+v, want single semantic hit a/1.0/not-kw", hits)
		}
		// no inputs at all -> empty
		if hits := mergeHits(nil, nil, nil, nil); len(hits) != 0 {
			t.Errorf("empty merge = %v, want empty", hits)
		}
	})

	t.Run("doc without vector keeps keyword rank", func(t *testing.T) {
		q := []float32{1, 0}
		kw := map[string]float64{"a": 5} // no vector for a
		hits := mergeHits(kw, q, map[string][]float32{}, mtimes)
		if len(hits) != 1 || hits[0].score != 1.0 {
			t.Errorf("hits = %+v, want a with kwNorm 1.0", hits)
		}
	})

	t.Run("tie broken by mtime", func(t *testing.T) {
		q := []float32{1, 0}
		vecs := map[string][]float32{"a": {0.5, 0.866}, "b": {0.5, 0.866}}
		hits := mergeHits(nil, q, vecs, mtimes)
		if len(hits) != 2 || hits[0].slug != "b" {
			t.Errorf("hits = %+v, want b first (newer mtime)", hits)
		}
	})

	t.Run("capped at 20", func(t *testing.T) {
		kw := map[string]float64{}
		for i := 0; i < 30; i++ {
			kw[string(rune('a'+i%26))+string(rune('a'+i/26))] = float64(i + 1)
		}
		if hits := mergeHits(kw, nil, nil, mtimes); len(hits) != 20 {
			t.Errorf("len(hits) = %d, want 20", len(hits))
		}
	})
}

func TestEmbedText(t *testing.T) {
	oldURL, oldModel := embedBaseURL, embedModel
	defer func() { embedBaseURL, embedModel = oldURL, oldModel }()
	embedModel = "test-model"

	server := func(handler http.HandlerFunc) {
		s := httptest.NewServer(handler)
		embedBaseURL = s.URL
		t.Cleanup(s.Close)
	}

	t.Run("ok", func(t *testing.T) {
		server(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/embeddings" {
				t.Errorf("path = %s, want /v1/embeddings", r.URL.Path)
			}
			w.Write([]byte(`{"data":[{"embedding":[0.5,-1.25,2]}]}`))
		})
		vec, err := embedText(&http.Client{}, "hello")
		if err != nil {
			t.Fatal(err)
		}
		if len(vec) != 3 || vec[0] != 0.5 || vec[1] != -1.25 || vec[2] != 2 {
			t.Errorf("vec = %v", vec)
		}
	})

	t.Run("non-2xx", func(t *testing.T) {
		server(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(500)
		})
		if _, err := embedText(&http.Client{}, "x"); err == nil {
			t.Error("want error for http 500")
		}
	})

	t.Run("bad json", func(t *testing.T) {
		server(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`not json`))
		})
		if _, err := embedText(&http.Client{}, "x"); err == nil {
			t.Error("want error for bad json")
		}
	})

	t.Run("empty data", func(t *testing.T) {
		server(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"data":[]}`))
		})
		if _, err := embedText(&http.Client{}, "x"); err == nil {
			t.Error("want error for empty data")
		}
	})

	t.Run("empty embedding", func(t *testing.T) {
		server(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"data":[{"embedding":[]}]}`))
		})
		if _, err := embedText(&http.Client{}, "x"); err == nil {
			t.Error("want error for empty embedding")
		}
	})
}
