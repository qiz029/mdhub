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

const (
	universeNeighbours    = 6
	relatedDocumentsLimit = 5
)

var universeVectorGeneration atomic.Uint64

var universeCache = struct {
	sync.Mutex
	ready bool
	key   uint64
	graph universeGraph
}{}

type universeDocument struct {
	Slug      string
	Title     string
	Excerpt   string
	Category  string
	Tags      []string
	Updated   int64
	WordCount int
}

type universeNode struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Excerpt   string   `json:"excerpt,omitempty"`
	Category  string   `json:"category,omitempty"`
	Tags      []string `json:"tags"`
	Updated   int64    `json:"updated"`
	Embedded  bool     `json:"embedded"`
	Degree    int      `json:"degree"`
	Cluster   int      `json:"cluster"`
	WordCount int      `json:"word_count"`
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

type relatedDocument struct {
	Slug       string  `json:"slug"`
	Title      string  `json:"title"`
	Similarity float64 `json:"similarity"`
}

type relatedCandidate struct {
	slug   string
	title  string
	vector []float32
}

type semanticNeighbour struct {
	slug       string
	similarity float64
}

type weightedNeighbour struct {
	index  int
	weight float64
}

func relatedDocuments(source []float32, candidates []relatedCandidate, limit int) []relatedDocument {
	if len(source) == 0 || limit < 1 {
		return []relatedDocument{}
	}
	related := make([]relatedDocument, 0, min(limit, len(candidates)))
	for _, candidate := range candidates {
		if len(candidate.vector) == 0 {
			continue
		}
		similarity := cosine(source, candidate.vector)
		if similarity <= 0 {
			continue
		}
		related = append(related, relatedDocument{
			Slug: candidate.slug, Title: candidate.title, Similarity: similarity,
		})
	}
	sort.Slice(related, func(i, j int) bool {
		if related[i].Similarity != related[j].Similarity {
			return related[i].Similarity > related[j].Similarity
		}
		return related[i].Slug < related[j].Slug
	})
	if len(related) > limit {
		related = related[:limit]
	}
	return related
}

func currentRelatedDocuments(slug string, limit int) []relatedDocument {
	mu.RLock()
	source := embedIndex[slug]
	candidates := make([]relatedCandidate, 0, max(0, len(searchIndex)-1))
	for candidateSlug, entry := range searchIndex {
		if candidateSlug == slug {
			continue
		}
		candidates = append(candidates, relatedCandidate{
			slug: candidateSlug, title: entry.title, vector: embedIndex[candidateSlug],
		})
	}
	mu.RUnlock()
	return relatedDocuments(source, candidates, limit)
}

// semanticClusterAssignments uses the local-moving phase of weighted Louvain
// community detection. Strong relationships pull documents into the same
// community while the modularity penalty prevents a weak bridge from merging
// otherwise distinct topics. Node and candidate ordering are deterministic so
// the API returns stable cluster IDs for the same graph.
func semanticClusterAssignments(nodes []universeNode, edges []universeEdge) map[string]int {
	assignments := make(map[string]int, len(nodes))
	for _, node := range nodes {
		assignments[node.ID] = -1
	}
	adjacency, degrees, totalWeight := semanticAdjacency(nodes, edges)
	if totalWeight == 0 {
		return assignments
	}
	communities := moveSemanticCommunities(nodes, adjacency, degrees, totalWeight)
	assignStableClusterIDs(assignments, nodes, degrees, communities)
	return assignments
}

func semanticAdjacency(nodes []universeNode, edges []universeEdge) ([][]weightedNeighbour, []float64, float64) {
	indexByID := make(map[string]int, len(nodes))
	for i, node := range nodes {
		indexByID[node.ID] = i
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
	return adjacency, degrees, totalWeight
}

func moveSemanticCommunities(nodes []universeNode, adjacency [][]weightedNeighbour, degrees []float64, totalWeight float64) []int {
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
			current, best := bestSemanticCommunity(nodeIndex, communities, communityTotals, adjacency, degrees, totalWeight, epsilon)
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
	return communities
}

func bestSemanticCommunity(nodeIndex int, communities []int, communityTotals []float64, adjacency [][]weightedNeighbour, degrees []float64, totalWeight, epsilon float64) (int, int) {
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
	best, bestGain := current, 0.0
	for _, candidate := range candidates {
		gain := weights[candidate] - degrees[nodeIndex]*communityTotals[candidate]/totalWeight
		if gain > bestGain+epsilon ||
			(gain > epsilon && gain >= bestGain-epsilon && candidate < best) {
			best, bestGain = candidate, gain
		}
	}
	return current, best
}

func assignStableClusterIDs(assignments map[string]int, nodes []universeNode, degrees []float64, communities []int) {
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
}

func buildSemanticUniverse(docs []universeDocument, vectors map[string][]float32, neighbours int) universeGraph {
	if neighbours < 1 {
		neighbours = 1
	}

	sortedDocs := sortedUniverseDocuments(docs)
	nodes, embeddedDocuments := universeNodes(sortedDocs, vectors)
	graph := universeGraph{
		Nodes: nodes,
		Meta: universeMeta{
			Documents: len(sortedDocs), EmbeddedDocuments: embeddedDocuments, Neighbours: neighbours,
		},
	}
	nearest := nearestSemanticNeighbours(sortedDocs, vectors, neighbours)
	graph.Edges = mutualSemanticEdges(sortedDocs, nearest)
	graph.Edges = connectIsolatedSemanticDocuments(sortedDocs, nearest, graph.Edges)
	sortSemanticEdges(graph.Edges)
	decorateUniverseGraph(&graph)
	return graph
}

func sortedUniverseDocuments(docs []universeDocument) []universeDocument {
	sorted := append([]universeDocument(nil), docs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Slug < sorted[j].Slug })
	return sorted
}

func universeNodes(docs []universeDocument, vectors map[string][]float32) ([]universeNode, int) {
	nodes := make([]universeNode, 0, len(docs))
	embeddedDocuments := 0
	for _, doc := range docs {
		embedded := len(vectors[doc.Slug]) > 0
		if embedded {
			embeddedDocuments++
		}
		tags := append([]string(nil), doc.Tags...)
		if tags == nil {
			tags = []string{}
		}
		nodes = append(nodes, universeNode{
			ID: doc.Slug, Title: doc.Title, Excerpt: doc.Excerpt,
			Category: doc.Category, Tags: tags, Updated: doc.Updated,
			Embedded: embedded, WordCount: doc.WordCount,
		})
	}
	return nodes, embeddedDocuments
}

func nearestSemanticNeighbours(docs []universeDocument, vectors map[string][]float32, limit int) map[string][]semanticNeighbour {
	nearest := make(map[string][]semanticNeighbour, len(docs))
	for i, left := range docs {
		leftVec := vectors[left.Slug]
		if len(leftVec) == 0 {
			continue
		}
		for j := i + 1; j < len(docs); j++ {
			right := docs[j]
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
		if len(nearest[slug]) > limit {
			nearest[slug] = nearest[slug][:limit]
		}
	}
	return nearest
}

func mutualSemanticEdges(docs []universeDocument, nearest map[string][]semanticNeighbour) []universeEdge {
	top := make(map[string]map[string]float64, len(nearest))
	for slug, candidates := range nearest {
		top[slug] = make(map[string]float64, len(candidates))
		for _, candidate := range candidates {
			top[slug][candidate.slug] = candidate.similarity
		}
	}
	edges := make([]universeEdge, 0)
	for _, left := range docs {
		for right, similarity := range top[left.Slug] {
			if left.Slug >= right {
				continue
			}
			if _, mutual := top[right][left.Slug]; !mutual {
				continue
			}
			edges = append(edges, universeEdge{
				Source: left.Slug, Target: right, Kind: "semantic", Similarity: similarity,
			})
		}
	}
	return edges
}

func connectIsolatedSemanticDocuments(docs []universeDocument, nearest map[string][]semanticNeighbour, edges []universeEdge) []universeEdge {
	edgeKeys := make(map[string]bool, len(edges))
	degree := make(map[string]int, len(docs))
	for _, edge := range edges {
		edgeKeys[edge.Source+"\x00"+edge.Target] = true
		degree[edge.Source]++
		degree[edge.Target]++
	}
	// Mutual Top-K keeps the graph sparse, but it can strand a document whose
	// nearest neighbour prefers somebody else. Give each embedded document at
	// least its strongest available relationship.
	for _, doc := range docs {
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
		edges = append(edges, universeEdge{
			Source: source, Target: target, Kind: "semantic", Similarity: candidate.similarity,
		})
		edgeKeys[key] = true
		degree[source]++
		degree[target]++
	}
	return edges
}

func sortSemanticEdges(edges []universeEdge) {
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Similarity != edges[j].Similarity {
			return edges[i].Similarity > edges[j].Similarity
		}
		if edges[i].Source != edges[j].Source {
			return edges[i].Source < edges[j].Source
		}
		return edges[i].Target < edges[j].Target
	})
}

func decorateUniverseGraph(graph *universeGraph) {
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
		write(strconv.Itoa(doc.WordCount))
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
		SELECT d.slug, d.title, d.excerpt, d.category_path, d.file_mtime, d.word_count,
			COALESCE(array_agg(dt.tag ORDER BY dt.tag) FILTER (WHERE dt.tag IS NOT NULL), '{}')
		FROM documents d
		LEFT JOIN document_tags dt ON dt.slug = d.slug
		WHERE d.published=true
		GROUP BY d.slug, d.title, d.excerpt, d.category_path, d.file_mtime, d.word_count
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
			&updated, &doc.WordCount, pq.Array(&doc.Tags),
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

func currentSemanticUniverse() (universeGraph, error) {
	docs, err := loadUniverseDocuments()
	if err != nil {
		return universeGraph{}, err
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

	return cachedSemanticUniverse(docs, vectors, vectorGeneration), nil
}

func handleUniverse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpError(w, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed)
		return
	}
	graph, err := currentSemanticUniverse()
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, graph)
}

func handleRelatedDocuments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpError(w, fmt.Errorf("method not allowed"), http.StatusMethodNotAllowed)
		return
	}
	slug := r.URL.Query().Get("slug")
	if slug == "" {
		httpError(w, fmt.Errorf("missing slug"), http.StatusBadRequest)
		return
	}
	writeJSON(w, currentRelatedDocuments(slug, relatedDocumentsLimit))
}
