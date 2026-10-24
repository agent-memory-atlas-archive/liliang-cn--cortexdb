package cortexdb

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// The walks of the graph tools under a caller scope.
//
// Each test asks a caller confined to alpha the same questions before and
// after the operator saves a document to beta, on one brain, so that the
// two answers can differ only by what beta wrote. Whatever beta wrote, a
// caller who cannot read beta must get the same answer: an answer that
// changes tells the caller what beta holds, even when every node it serves
// is one of the caller's own.

// walkScopeBrain is the alpha half of the leak shapes: two documents of
// alpha, one mentioning Kestrel Vale and one Heron Mill, and nothing in
// alpha that links the two.
func walkScopeBrain(t *testing.T) *DB {
	t.Helper()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "walk-scope.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, doc := range []KnowledgeSaveRequest{
		{KnowledgeID: "a1", Collection: "alpha", Content: "Kestrel Vale runs the alpha mill.",
			Entities: []ToolEntityInput{{Name: "Kestrel Vale", ChunkIDs: []string{graphChunkNodeID("a1", 0)}}}},
		{KnowledgeID: "a2", Collection: "alpha", Content: "Heron Mill grinds alpha grain.",
			Entities: []ToolEntityInput{{Name: "Heron Mill", ChunkIDs: []string{graphChunkNodeID("a2", 0)}}}},
	} {
		if _, err := db.SaveKnowledge(context.Background(), doc); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// betaStatesARelation is the first shape: beta states Kestrel Vale
// acquires Heron Mill, two entities alpha knows from its own documents.
var betaStatesARelation = KnowledgeSaveRequest{KnowledgeID: "b1", Collection: "beta", Content: "BETASECRET deal.",
	Entities: []ToolEntityInput{
		{Name: "Kestrel Vale", ChunkIDs: []string{graphChunkNodeID("b1", 0)}},
		{Name: "Heron Mill", ChunkIDs: []string{graphChunkNodeID("b1", 0)}},
	},
	Relations: []ToolRelationInput{{From: "Kestrel Vale", To: "Heron Mill", Type: "acquires", ChunkIDs: []string{graphChunkNodeID("b1", 0)}}},
}

// betaMentionsBoth is the second shape: one chunk of beta mentions both
// entities, and states nothing between them.
var betaMentionsBoth = KnowledgeSaveRequest{KnowledgeID: "b1", Collection: "beta", Content: "Kestrel Vale is buying Heron Mill in secret.",
	Entities: []ToolEntityInput{
		{Name: "Kestrel Vale", ChunkIDs: []string{graphChunkNodeID("b1", 0)}},
		{Name: "Heron Mill", ChunkIDs: []string{graphChunkNodeID("b1", 0)}},
	},
}

// alphaOnly is a caller confined to collection alpha, with no memory rule.
func alphaOnly() CallerScope {
	return CallerScope{Key: "alpha-key", Knowledge: func(c string) bool { return c == "alpha" }}
}

// walkCall is one call of a graph tool, its answer or its error as a port
// would serve it.
type walkCall struct {
	name string
	call func(ctx context.Context, db *DB) (any, error)
}

func (c walkCall) answer(t *testing.T, ctx context.Context, db *DB) string {
	t.Helper()
	out, err := c.call(ctx, db)
	if err != nil {
		return "error: " + err.Error()
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

var (
	kestrelVale = EntityNodeID("Kestrel Vale")
	heronMill   = EntityNodeID("Heron Mill")
)

// walkCalls is every graph tool that walks, as a confined key may call it,
// with the hops each shape needs: one past the relation, two past the
// chunk, and three for the tools whose walk is counted in chunks.
func walkCalls() []walkCall {
	tb := func(db *DB) *GraphRAGToolbox { return db.GraphRAGTools() }
	km := func(db *DB) *KnowledgeMemory { return db.KnowledgeMemory() }
	var calls []walkCall
	for _, hops := range []int{1, 2, 3} {
		hops := hops
		suffix := "/hops=" + strconv.Itoa(hops)
		calls = append(calls,
			walkCall{"expand_graph" + suffix, func(ctx context.Context, db *DB) (any, error) {
				return tb(db).ExpandGraph(ctx, ToolExpandGraphRequest{NodeIDs: []string{kestrelVale}, MaxHops: hops})
			}},
			walkCall{"expand_graph/relation_policies" + suffix, func(ctx context.Context, db *DB) (any, error) {
				return tb(db).ExpandGraph(ctx, ToolExpandGraphRequest{NodeIDs: []string{kestrelVale}, MaxHops: hops,
					RelationPolicies: graph.RelationPolicies{"mentions": {Weight: 0.5}}})
			}},
			walkCall{"expand_graph/limit" + suffix, func(ctx context.Context, db *DB) (any, error) {
				return tb(db).ExpandGraph(ctx, ToolExpandGraphRequest{NodeIDs: []string{kestrelVale}, MaxHops: hops, Limit: 2})
			}},
			walkCall{"knowledge_memory_neighbors" + suffix, func(ctx context.Context, db *DB) (any, error) {
				return km(db).Neighbors(ctx, KnowledgeMemoryNeighborsRequest{EntityName: "Kestrel Vale", MaxDepth: hops})
			}},
			walkCall{"knowledge_memory_neighbors/limit" + suffix, func(ctx context.Context, db *DB) (any, error) {
				return km(db).Neighbors(ctx, KnowledgeMemoryNeighborsRequest{EntityName: "Kestrel Vale", MaxDepth: hops, Limit: 1})
			}},
			walkCall{"search_chunks_by_entities" + suffix, func(ctx context.Context, db *DB) (any, error) {
				return tb(db).SearchChunksByEntities(ctx, ToolSearchChunksByEntitiesRequest{EntityNames: []string{"Kestrel Vale"}, MaxHops: hops})
			}},
			walkCall{"knowledge_memory_expand_entity_context" + suffix, func(ctx context.Context, db *DB) (any, error) {
				return km(db).ExpandEntityContext(ctx, KnowledgeMemoryExpandEntityContextRequest{EntityNames: []string{"Kestrel Vale"}, MaxHops: hops})
			}},
			walkCall{"search_paths" + suffix, func(ctx context.Context, db *DB) (any, error) {
				return tb(db).SearchPaths(ctx, ToolSearchPathsRequest{EntityNames: []string{"Kestrel Vale", "Heron Mill"}, MaxDepth: hops, WithText: true})
			}},
			walkCall{"search_paths/include_bookkeeping" + suffix, func(ctx context.Context, db *DB) (any, error) {
				return tb(db).SearchPaths(ctx, ToolSearchPathsRequest{EntityNames: []string{"Kestrel Vale", "Heron Mill"}, MaxDepth: hops, IncludeBookkeeping: true})
			}},
			walkCall{"search_paths/open" + suffix, func(ctx context.Context, db *DB) (any, error) {
				return tb(db).SearchPaths(ctx, ToolSearchPathsRequest{NodeIDs: []string{kestrelVale}, MaxDepth: hops, IncludeBookkeeping: true})
			}},
		)
	}
	calls = append(calls,
		walkCall{"expand_graph/edge_types", func(ctx context.Context, db *DB) (any, error) {
			return tb(db).ExpandGraph(ctx, ToolExpandGraphRequest{NodeIDs: []string{kestrelVale}, EdgeTypes: []string{"acquires"}})
		}},
		walkCall{"knowledge_memory_neighbors/edge_types", func(ctx context.Context, db *DB) (any, error) {
			return km(db).Neighbors(ctx, KnowledgeMemoryNeighborsRequest{EntityName: "Kestrel Vale", EdgeTypes: []string{"acquires"}})
		}},
		walkCall{"knowledge_memory_shortest_path", func(ctx context.Context, db *DB) (any, error) {
			return km(db).ShortestPath(ctx, KnowledgeMemoryShortestPathRequest{FromEntityName: "Kestrel Vale", ToEntityName: "Heron Mill"})
		}},
	)
	return calls
}

// answersBeforeAndAfter asks every walk under scope, saves beta's
// document, and asks again.
func answersBeforeAndAfter(t *testing.T, db *DB, ctx context.Context, beta KnowledgeSaveRequest) (before, after map[string]string) {
	t.Helper()
	before, after = map[string]string{}, map[string]string{}
	for _, c := range walkCalls() {
		before[c.name] = c.answer(t, ctx, db)
	}
	if _, err := db.SaveKnowledge(context.Background(), beta); err != nil {
		t.Fatal(err)
	}
	for _, c := range walkCalls() {
		after[c.name] = c.answer(t, ctx, db)
	}
	return before, after
}

func TestUnderACallerScopeAWalkDoesNotFollowARelationOnlyAnotherCollectionStates(t *testing.T) {
	db := walkScopeBrain(t)
	before, after := answersBeforeAndAfter(t, db, WithCallerScope(context.Background(), alphaOnly()), betaStatesARelation)
	for _, c := range walkCalls() {
		if before[c.name] != after[c.name] {
			t.Errorf("%s: alpha's answer changes once beta states Kestrel Vale acquires Heron Mill\nbefore: %s\nafter:  %s", c.name, before[c.name], after[c.name])
		}
		if strings.Contains(after[c.name], "BETASECRET") || strings.Contains(after[c.name], graphChunkNodeID("b1", 0)) {
			t.Errorf("%s serves beta's chunk to alpha: %s", c.name, after[c.name])
		}
	}
	// Without a scope the walk is the stored graph's, beta's relation
	// included, as before.
	out, err := db.GraphRAGTools().ExpandGraph(context.Background(), ToolExpandGraphRequest{NodeIDs: []string{kestrelVale}, EdgeTypes: []string{"acquires"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(answerOf(t, out, nil), heronMill) {
		t.Fatalf("without a scope expand_graph no longer follows the acquires relation: %s", answerOf(t, out, nil))
	}
}

func TestUnderACallerScopeAWalkDoesNotPassThroughAnotherCollectionsChunk(t *testing.T) {
	db := walkScopeBrain(t)
	before, after := answersBeforeAndAfter(t, db, WithCallerScope(context.Background(), alphaOnly()), betaMentionsBoth)
	for _, c := range walkCalls() {
		if before[c.name] != after[c.name] {
			t.Errorf("%s: alpha's answer changes once a chunk of beta mentions Kestrel Vale and Heron Mill\nbefore: %s\nafter:  %s", c.name, before[c.name], after[c.name])
		}
		if strings.Contains(after[c.name], "in secret") || strings.Contains(after[c.name], graphChunkNodeID("b1", 0)) {
			t.Errorf("%s serves beta's chunk to alpha: %s", c.name, after[c.name])
		}
	}
	// Without a scope the walk passes through beta's chunk, as before.
	out, err := db.GraphRAGTools().SearchChunksByEntities(context.Background(), ToolSearchChunksByEntitiesRequest{EntityNames: []string{"Kestrel Vale"}, MaxHops: 3})
	if err != nil {
		t.Fatal(err)
	}
	if answer := answerOf(t, out, nil); !strings.Contains(answer, graphChunkNodeID("a2", 0)) {
		t.Fatalf("without a scope search_chunks_by_entities no longer reaches a2 through beta's chunk: %s", answer)
	}
}

// A memory the caller may not read is not walked through either: Zeus's
// memory mentions Kestrel Vale and Heron Mill, and a caller confined to
// Hermes's memories must not reach Heron Mill's chunk through it. A scope
// with no knowledge rule serves an entity whole (scopedNode), its time of
// last write included, which Zeus's save moves: the answers are compared
// without it, since that is the entity's own field and not what the walk
// reached.
func TestUnderACallerScopeAWalkDoesNotPassThroughAnotherUsersMemory(t *testing.T) {
	withoutWriteTimes := regexp.MustCompile(`"updated_at":"[^"]*",?`)
	db := walkScopeBrain(t)
	ctx := WithCallerScope(context.Background(), CallerScope{Key: "hermes", Memory: func(b MemoryBucket) bool { return b.UserID == "hermes" }})
	before := map[string]string{}
	for _, c := range walkCalls() {
		before[c.name] = withoutWriteTimes.ReplaceAllString(c.answer(t, ctx, db), "")
	}
	if _, err := db.SaveMemory(context.Background(), MemorySaveRequest{MemoryID: "zeus-mem", UserID: "zeus", Content: "ZEUSPRIVATE: Kestrel Vale sells Heron Mill.",
		Entities: []ToolEntityInput{{Name: "Kestrel Vale"}, {Name: "Heron Mill"}}}); err != nil {
		t.Fatal(err)
	}
	for _, c := range walkCalls() {
		if after := withoutWriteTimes.ReplaceAllString(c.answer(t, ctx, db), ""); after != before[c.name] {
			t.Errorf("%s: Hermes's answer changes once Zeus's memory links Kestrel Vale and Heron Mill\nbefore: %s\nafter:  %s", c.name, before[c.name], after)
		}
	}
}

// shortest_path looks for a path the caller is served: when the shortest
// path runs over beta's relation, alpha gets its own longer path, the one
// it got before beta wrote anything, and not "no path".
func TestUnderACallerScopeShortestPathFindsTheCallersOwnPath(t *testing.T) {
	db := walkScopeBrain(t)
	for _, doc := range []KnowledgeSaveRequest{
		{KnowledgeID: "a3", Collection: "alpha", Content: "Kestrel Vale supplies Wren Yard.",
			Entities:  []ToolEntityInput{{Name: "Kestrel Vale", ChunkIDs: []string{graphChunkNodeID("a3", 0)}}, {Name: "Wren Yard", ChunkIDs: []string{graphChunkNodeID("a3", 0)}}},
			Relations: []ToolRelationInput{{From: "Kestrel Vale", To: "Wren Yard", Type: "supplies", ChunkIDs: []string{graphChunkNodeID("a3", 0)}}}},
		{KnowledgeID: "a4", Collection: "alpha", Content: "Wren Yard feeds Heron Mill.",
			Entities:  []ToolEntityInput{{Name: "Wren Yard", ChunkIDs: []string{graphChunkNodeID("a4", 0)}}, {Name: "Heron Mill", ChunkIDs: []string{graphChunkNodeID("a4", 0)}}},
			Relations: []ToolRelationInput{{From: "Wren Yard", To: "Heron Mill", Type: "feeds", ChunkIDs: []string{graphChunkNodeID("a4", 0)}}}},
	} {
		if _, err := db.SaveKnowledge(context.Background(), doc); err != nil {
			t.Fatal(err)
		}
	}
	ctx := WithCallerScope(context.Background(), alphaOnly())
	km := db.KnowledgeMemory()
	req := KnowledgeMemoryShortestPathRequest{FromEntityName: "Kestrel Vale", ToEntityName: "Heron Mill"}
	before, err := km.ShortestPath(ctx, req)
	if err != nil || before.Path.Distance != 2 {
		t.Fatalf("alpha's own path is %+v, %v, want two hops over supplies and feeds", before, err)
	}
	if _, err := db.SaveKnowledge(context.Background(), betaStatesARelation); err != nil {
		t.Fatal(err)
	}
	if open, err := km.ShortestPath(context.Background(), req); err != nil || open.Path.Distance != 1 {
		t.Fatalf("without a scope the shortest path is %+v, %v, want beta's one hop", open, err)
	}
	after, err := km.ShortestPath(ctx, req)
	if err != nil {
		t.Fatalf("under alpha's scope shortest_path finds no path once beta states a shorter one: %v", err)
	}
	if b, a := answerOf(t, before, nil), answerOf(t, after, nil); a != b {
		t.Fatalf("alpha's shortest path changes once beta states a shorter one\nbefore: %s\nafter:  %s", b, a)
	}
}

// An inferred edge that names no provenance of its own and rests on a
// relation only beta states is beta's statement too: it is not walked, nor
// served between two nodes the caller is served.
func TestUnderACallerScopeAnEdgeInferredFromAnotherCollectionsRelationIsLeftOut(t *testing.T) {
	db := walkScopeBrain(t)
	if _, err := db.SaveKnowledge(context.Background(), betaStatesARelation); err != nil {
		t.Fatal(err)
	}
	stated, err := db.graph.GetEdges(context.Background(), kestrelVale, "out")
	if err != nil {
		t.Fatal(err)
	}
	var premise string
	for _, edge := range stated {
		if edge.EdgeType == "acquires" {
			premise = edge.ID
		}
	}
	if premise == "" {
		t.Fatal("beta's acquires relation is not stored")
	}
	if err := db.graph.UpsertEdge(context.Background(), &graph.GraphEdge{ID: "inferred-owns", FromNodeID: kestrelVale, ToNodeID: heronMill, EdgeType: "owns", Weight: 1,
		Properties: map[string]any{"inferred": true, "support_edge_ids": []string{premise}}}); err != nil {
		t.Fatal(err)
	}
	ctx := WithCallerScope(context.Background(), alphaOnly())
	out, err := db.GraphRAGTools().ExpandGraph(ctx, ToolExpandGraphRequest{NodeIDs: []string{kestrelVale}, EdgeTypes: []string{"owns"}})
	if answer := answerOf(t, out, err); strings.Contains(answer, heronMill) || strings.Contains(answer, "inferred-owns") {
		t.Fatalf("expand_graph follows an edge inferred from beta's relation: %s", answer)
	}
	// Asked from both ends, the edge between them is not served either.
	out, err = db.GraphRAGTools().ExpandGraph(ctx, ToolExpandGraphRequest{NodeIDs: []string{kestrelVale, heronMill}, MaxHops: 1})
	if answer := answerOf(t, out, err); strings.Contains(answer, "inferred-owns") {
		t.Fatalf("expand_graph serves an edge inferred from beta's relation between two nodes alpha is served: %s", answer)
	}
	if open, err := db.GraphRAGTools().ExpandGraph(context.Background(), ToolExpandGraphRequest{NodeIDs: []string{kestrelVale}, EdgeTypes: []string{"owns"}}); !strings.Contains(answerOf(t, open, err), "inferred-owns") {
		t.Fatalf("without a scope expand_graph no longer follows the inferred edge: %s", answerOf(t, open, err))
	}
}
