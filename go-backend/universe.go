package main

import (
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/lib/pq"
)

const universeNeighbours = 6

type universeDocument struct {
	Slug     string
	Title    string
	Excerpt  string
	Category string
	Tags     []string
	Updated  int64
}

type universeNode struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Excerpt  string   `json:"excerpt,omitempty"`
	Category string   `json:"category,omitempty"`
	Tags     []string `json:"tags"`
	Updated  int64    `json:"updated"`
	Embedded bool     `json:"embedded"`
	Degree   int      `json:"degree"`
}

type universeEdge struct {
	Source     string  `json:"source"`
	Target     string  `json:"target"`
	Kind       string  `json:"kind"`
	Similarity float64 `json:"similarity"`
}

type universeMeta struct {
	Documents         int     `json:"documents"`
	EmbeddedDocuments int     `json:"embedded_documents"`
	Edges             int     `json:"edges"`
	Neighbours        int     `json:"neighbours"`
	MinSimilarity     float64 `json:"min_similarity"`
	MaxSimilarity     float64 `json:"max_similarity"`
}

type universeGraph struct {
	Nodes []universeNode `json:"nodes"`
	Edges []universeEdge `json:"edges"`
	Meta  universeMeta   `json:"meta"`
}

type semanticNeighbour struct {
	slug       string
	similarity float64
}

func buildSemanticUniverse(docs []universeDocument, vectors map[string][]float32, neighbours int) universeGraph {
	if neighbours < 1 {
		neighbours = 1
	}

	sortedDocs := append([]universeDocument(nil), docs...)
	sort.Slice(sortedDocs, func(i, j int) bool { return sortedDocs[i].Slug < sortedDocs[j].Slug })

	graph := universeGraph{
		Nodes: make([]universeNode, 0, len(sortedDocs)),
		Edges: []universeEdge{},
		Meta:  universeMeta{Documents: len(sortedDocs), Neighbours: neighbours},
	}
	for _, doc := range sortedDocs {
		embedded := len(vectors[doc.Slug]) > 0
		if embedded {
			graph.Meta.EmbeddedDocuments++
		}
		tags := append([]string(nil), doc.Tags...)
		if tags == nil {
			tags = []string{}
		}
		graph.Nodes = append(graph.Nodes, universeNode{
			ID: doc.Slug, Title: doc.Title, Excerpt: doc.Excerpt,
			Category: doc.Category, Tags: tags, Updated: doc.Updated,
			Embedded: embedded,
		})
	}

	nearest := make(map[string][]semanticNeighbour, len(sortedDocs))
	for i, left := range sortedDocs {
		leftVec := vectors[left.Slug]
		if len(leftVec) == 0 {
			continue
		}
		for j := i + 1; j < len(sortedDocs); j++ {
			right := sortedDocs[j]
			rightVec := vectors[right.Slug]
			if len(rightVec) == 0 {
				continue
			}
			similarity := cosine(leftVec, rightVec)
			if similarity <= 0 {
				continue
			}
			nearest[left.Slug] = append(nearest[left.Slug], semanticNeighbour{right.Slug, similarity})
			nearest[right.Slug] = append(nearest[right.Slug], semanticNeighbour{left.Slug, similarity})
		}
	}
	for slug := range nearest {
		sort.Slice(nearest[slug], func(i, j int) bool {
			if nearest[slug][i].similarity != nearest[slug][j].similarity {
				return nearest[slug][i].similarity > nearest[slug][j].similarity
			}
			return nearest[slug][i].slug < nearest[slug][j].slug
		})
		if len(nearest[slug]) > neighbours {
			nearest[slug] = nearest[slug][:neighbours]
		}
	}

	top := make(map[string]map[string]float64, len(nearest))
	for slug, candidates := range nearest {
		top[slug] = make(map[string]float64, len(candidates))
		for _, candidate := range candidates {
			top[slug][candidate.slug] = candidate.similarity
		}
	}
	for _, left := range sortedDocs {
		for right, similarity := range top[left.Slug] {
			if left.Slug >= right {
				continue
			}
			if _, mutual := top[right][left.Slug]; !mutual {
				continue
			}
			graph.Edges = append(graph.Edges, universeEdge{
				Source: left.Slug, Target: right, Kind: "semantic", Similarity: similarity,
			})
		}
	}
	edgeKeys := make(map[string]bool, len(graph.Edges))
	degree := make(map[string]int, len(sortedDocs))
	for _, edge := range graph.Edges {
		edgeKeys[edge.Source+"\x00"+edge.Target] = true
		degree[edge.Source]++
		degree[edge.Target]++
	}
	// Mutual Top-K keeps the graph sparse, but it can strand a document whose
	// nearest neighbour prefers somebody else. Give each embedded document at
	// least its strongest available relationship.
	for _, doc := range sortedDocs {
		if degree[doc.Slug] > 0 || len(nearest[doc.Slug]) == 0 {
			continue
		}
		candidate := nearest[doc.Slug][0]
		source, target := doc.Slug, candidate.slug
		if source > target {
			source, target = target, source
		}
		key := source + "\x00" + target
		if edgeKeys[key] {
			continue
		}
		graph.Edges = append(graph.Edges, universeEdge{
			Source: source, Target: target, Kind: "semantic", Similarity: candidate.similarity,
		})
		edgeKeys[key] = true
		degree[source]++
		degree[target]++
	}
	sort.Slice(graph.Edges, func(i, j int) bool {
		if graph.Edges[i].Similarity != graph.Edges[j].Similarity {
			return graph.Edges[i].Similarity > graph.Edges[j].Similarity
		}
		if graph.Edges[i].Source != graph.Edges[j].Source {
			return graph.Edges[i].Source < graph.Edges[j].Source
		}
		return graph.Edges[i].Target < graph.Edges[j].Target
	})

	byID := make(map[string]int, len(graph.Nodes))
	for i := range graph.Nodes {
		byID[graph.Nodes[i].ID] = i
	}
	for _, edge := range graph.Edges {
		graph.Nodes[byID[edge.Source]].Degree++
		graph.Nodes[byID[edge.Target]].Degree++
		if graph.Meta.MinSimilarity == 0 || edge.Similarity < graph.Meta.MinSimilarity {
			graph.Meta.MinSimilarity = edge.Similarity
		}
		if edge.Similarity > graph.Meta.MaxSimilarity {
			graph.Meta.MaxSimilarity = edge.Similarity
		}
	}
	graph.Meta.Edges = len(graph.Edges)
	return graph
}

func loadUniverseDocuments() ([]universeDocument, error) {
	rows, err := db.Query(`
		SELECT d.slug, d.title, d.excerpt, d.category_path, d.file_mtime,
			COALESCE(array_agg(dt.tag ORDER BY dt.tag) FILTER (WHERE dt.tag IS NOT NULL), '{}')
		FROM documents d
		LEFT JOIN document_tags dt ON dt.slug = d.slug
		WHERE d.published=true
		GROUP BY d.slug, d.title, d.excerpt, d.category_path, d.file_mtime
		ORDER BY d.slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	docs := []universeDocument{}
	for rows.Next() {
		var doc universeDocument
		var updated time.Time
		if err := rows.Scan(
			&doc.Slug, &doc.Title, &doc.Excerpt, &doc.Category,
			&updated, pq.Array(&doc.Tags),
		); err != nil {
			return nil, err
		}
		doc.Updated = updated.UnixMilli()
		if doc.Tags == nil {
			doc.Tags = []string{}
		}
		docs = append(docs, doc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return docs, nil
}

func handleUniverse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpError(w, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed)
		return
	}
	docs, err := loadUniverseDocuments()
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}

	mu.RLock()
	vectors := make(map[string][]float32, len(embedIndex))
	for slug, vector := range embedIndex {
		vectors[slug] = vector
	}
	mu.RUnlock()

	writeJSON(w, buildSemanticUniverse(docs, vectors, universeNeighbours))
}
