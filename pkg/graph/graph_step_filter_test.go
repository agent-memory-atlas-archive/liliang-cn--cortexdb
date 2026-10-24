package graph

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// withoutNode is a step filter that never enters one node, as a caller
// scope never enters a node the caller may not see.
func withoutNode(hidden string) StepFilter {
	return func(_ context.Context, from string, edges []*GraphEdge) ([]*GraphEdge, error) {
		return slices.DeleteFunc(slices.Clone(edges), func(e *GraphEdge) bool {
			return e.FromNodeID == hidden || e.ToNodeID == hidden
		}), nil
	}
}

// Every walk takes only the steps its filter admits: a node reached only
// through borealis is not reached, a score and a shortest path are those
// of the graph without it, and an expansion over an edge left out is not
// counted.
func TestAWalkTakesOnlyTheStepsItsFilterAdmits(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	buildPathFixture(t, g)
	ctx := context.Background()
	admit := withoutNode("borealis")

	reached, err := g.Neighbors(ctx, "alice", TraversalOptions{MaxDepth: 2, Admit: admit})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, n := range reached {
		ids = append(ids, n.ID)
	}
	if slices.Contains(ids, "borealis") || slices.Contains(ids, "bob") {
		t.Fatalf("Neighbors reached %v through a node its filter leaves out", ids)
	}
	if !slices.Contains(ids, "kestrel") {
		t.Fatalf("Neighbors reached %v, want kestrel over the admitted co_occurs edges", ids)
	}

	scored, err := g.ScoredNeighbors(ctx, "alice", TraversalOptions{MaxDepth: 2, Relations: RelationPolicies{"co_occurs": {Weight: 0.5}}, Admit: admit})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range scored {
		if n.NodeID == "borealis" || n.NodeID == "bob" {
			t.Fatalf("ScoredNeighbors scored %s through a node its filter leaves out", n.NodeID)
		}
		if n.NodeID == "kestrel" && n.Distance != 2 {
			t.Fatalf("kestrel scored at distance %d, want 2 over the admitted edges", n.Distance)
		}
	}

	open, err := g.ShortestPath(ctx, "alice", "kestrel")
	if err != nil || len(open.Edges) != 2 || open.Edges[0].ID != "e1" {
		t.Fatalf("without a filter the shortest path is %+v, %v, want e1 e2", open, err)
	}
	path, err := g.ShortestPathAdmitting(ctx, "alice", "kestrel", admit)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{path.Edges[0].ID, path.Edges[1].ID}; !slices.Equal(got, []string{"e3", "e4"}) {
		t.Fatalf("the admitted shortest path is %v, want e3 e4", got)
	}
	if _, err := g.ShortestPathAdmitting(ctx, "alice", "kestrel", withoutNode("noise1")); err != nil {
		t.Fatalf("a filter that leaves out noise1 still admits e1 e2: %v", err)
	}

	all, err := g.SearchPaths(ctx, []string{"alice", "kestrel"}, PathSearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := g.SearchPaths(ctx, []string{"alice", "kestrel"}, PathSearchOptions{Admit: admit})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Paths) != 1 || !slices.Equal(edgeIDs(res.Paths[0]), []string{"e3", "e4"}) {
		t.Fatalf("SearchPaths under the filter found %+v, want the co_occurs path alone", res.Paths)
	}
	if res.Expansions >= all.Expansions {
		t.Fatalf("SearchPaths under the filter examined %d edges, the whole graph %d: a left-out edge was counted", res.Expansions, all.Expansions)
	}
}

// A filter's error stops the walk and is the walk's error.
func TestAStepFilterErrorStopsTheWalk(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	buildPathFixture(t, g)
	ctx := context.Background()
	broken := errors.New("cannot judge")
	fail := StepFilter(func(context.Context, string, []*GraphEdge) ([]*GraphEdge, error) { return nil, broken })
	if _, err := g.Neighbors(ctx, "alice", TraversalOptions{Admit: fail}); !errors.Is(err, broken) {
		t.Errorf("Neighbors: %v", err)
	}
	if _, err := g.ScoredNeighbors(ctx, "alice", TraversalOptions{Admit: fail}); !errors.Is(err, broken) {
		t.Errorf("ScoredNeighbors: %v", err)
	}
	if _, err := g.ShortestPathAdmitting(ctx, "alice", "kestrel", fail); !errors.Is(err, broken) {
		t.Errorf("ShortestPathAdmitting: %v", err)
	}
	if _, err := g.SearchPaths(ctx, []string{"alice", "kestrel"}, PathSearchOptions{Admit: fail}); !errors.Is(err, broken) {
		t.Errorf("SearchPaths: %v", err)
	}
}
