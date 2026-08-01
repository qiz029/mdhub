package main

import (
	"fmt"
	"hash/fnv"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lib/pq"
)

const universeNeighbours = 6

var universeVectorGeneration atomic.Uint64

var universeCache = struct {
	sync.Mutex
	ready bool
	key   uint64
	graph universeGraph
}{}

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
	Cluster  int      `json:"cluster"`
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

type weightedNeighbour struct {
	index  int
	weight float64
}

// semanticClusterAssignments uses the local-moving phase of weighted Louvain
// community detection. Strong relationships pull documents into the same
// community while the modularity penalty prevents a weak bridge from merging
// otherwise distinct topics. Node and candidate ordering are deterministic so
// the API returns stable cluster IDs for the same graph.
func semanticClusterAssignments(nodes []universeNode, edges []universeEdge) map[string]int {
	assignments := make(map[string]int, len(nodes))
	indexByID := make(map[string]int, len(nodes))
	for i, node := range nodes {
		indexByID[node.ID] = i
		assignments[node.ID] = -1
	}

	adjacency := make([][]weightedNeighbour, len(nodes))
	degrees := make([]float64, len(nodes))
	for _, edge := range edges {
		left, leftOK := indexByID[edge.Source]
		right, rightOK := indexByID[edge.Target]
		if !leftOK || !rightOK || left == right || edge.Similarity <= 0 {
			continue
		}
		adjacency[left] = append(adjacency[left], weightedNeighbour{right, edge.Similarity})
		adjacency[right] = append(adjacency[right], weightedNeighbour{left, edge.Similarity})
		degrees[left] += edge.Similarity
		degrees[right] += edge.Similarity
	}

	var totalWeight float64
	for _, degree := range degrees {
		totalWeight += degree
	}
	if totalWeight == 0 {
		return assignments
	}

	communities := make([]int, len(nodes))
	communityTotals := append([]float64(nil), degrees...)
	for i := range communities {
		communities[i] = i
	}

	const epsilon = 1e-12
	for pass := 0; pass < 20; pass++ {
		moved := false
		for nodeIndex, node := range nodes {
			if !node.Embedded || degrees[nodeIndex] == 0 {
				continue
			}
			current := communities[nodeIndex]
			communityTotals[current] -= degrees[nodeIndex]

			weights := make(map[int]float64, len(adjacency[nodeIndex]))
			for _, neighbour := range adjacency[nodeIndex] {
				weights[communities[neighbour.index]] += neighbour.weight
			}
			candidates := make([]int, 0, len(weights))
			for candidate := range weights {
				candidates = append(candidates, candidate)
			}
			sort.Ints(candidates)

			best := current
			bestGain := 0.0
			for _, candidate := range candidates {
				gain := weights[candidate] - degrees[nodeIndex]*communityTotals[candidate]/totalWeight
				if gain > bestGain+epsilon ||
					(gain > epsilon && gain >= bestGain-epsilon && candidate < best) {
					best = candidate
					bestGain = gain
				}
			}

			communities[nodeIndex] = best
			communityTotals[best] += degrees[nodeIndex]
			if best != current {
				moved = true
			}
		}
		if !moved {
			break
		}
	}

	members := make(map[int][]string)
	for i, node := range nodes {
		if !node.Embedded || degrees[i] == 0 {
			continue
		}
		members[communities[i]] = append(members[communities[i]], node.ID)
	}
	type communityMembers struct {
		community int
		first     string
	}
	ordered := make([]communityMembers, 0, len(members))
	for community, ids := range members {
		sort.Strings(ids)
		ordered = append(ordered, communityMembers{community: community, first: ids[0]})
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].first < ordered[j].first })
	for cluster, group := range ordered {
		for _, id := range members[group.community] {
			assignments[id] = cluster
		}
	}
	return assignments
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
	clusters := semanticClusterAssignments(graph.Nodes, graph.Edges)
	for i := range graph.Nodes {
		graph.Nodes[i].Cluster = clusters[graph.Nodes[i].ID]
	}
	graph.Meta.Edges = len(graph.Edges)
	return graph
}

func markUniverseDirty() {
	universeVectorGeneration.Add(1)
}

func universeCacheKey(docs []universeDocument, vectorGeneration uint64) uint64 {
	hash := fnv.New64a()
	write := func(value string) {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	write(strconv.FormatUint(vectorGeneration, 10))
	for _, doc := range docs {
		write(doc.Slug)
		write(doc.Title)
		write(doc.Category)
		write(strconv.FormatInt(doc.Updated, 10))
		for _, tag := range doc.Tags {
			write(tag)
		}
	}
	return hash.Sum64()
}

func cachedSemanticUniverse(docs []universeDocument, vectors map[string][]float32, vectorGeneration uint64) universeGraph {
	key := universeCacheKey(docs, vectorGeneration)
	universeCache.Lock()
	defer universeCache.Unlock()
	if universeCache.ready && universeCache.key == key {
		return universeCache.graph
	}
	graph := buildSemanticUniverse(docs, vectors, universeNeighbours)
	universeCache.ready = true
	universeCache.key = key
	universeCache.graph = graph
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

	var vectors map[string][]float32
	var vectorGeneration uint64
	for {
		before := universeVectorGeneration.Load()
		mu.RLock()
		vectors = make(map[string][]float32, len(embedIndex))
		for slug, vector := range embedIndex {
			vectors[slug] = vector
		}
		mu.RUnlock()
		vectorGeneration = universeVectorGeneration.Load()
		if before == vectorGeneration {
			break
		}
	}

	writeJSON(w, cachedSemanticUniverse(docs, vectors, vectorGeneration))
}
