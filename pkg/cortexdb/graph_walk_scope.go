package cortexdb

import (
	"context"
	"fmt"
	"slices"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// The walks of the graph tools under a caller scope.
//
// The graph tools a confined key may call (expand_graph,
// knowledge_memory_neighbors, knowledge_memory_shortest_path,
// search_chunks_by_entities, search_paths, and
// knowledge_memory_expand_entity_context through the first and the fourth)
// used to walk the stored graph and leave out what the caller may not see
// afterwards (withoutExpiredDocuments). The walk itself was free to cross
// what the caller cannot read: to follow a relation only another collection
// states, to pass through another collection's chunk that mentions two
// entities the caller knows, or through another user's memory. Each node it
// then served was the caller's own, but that the node was reached at all,
// at that distance, with that score, before a limit cut the walk or not,
// told the caller what the hidden part of the graph holds. A key confined
// to alpha learned that beta states Kestrel Vale acquires Heron Mill by
// asking for Kestrel Vale's neighbours over acquires.
//
// Under a caller scope the walks now take only what the caller is served
// (walkAdmit): a step is taken when the node it leads to is one
// withoutExpiredDocuments serves and the edge is one servedEdges serves. It
// is the rule the graph lane of a query confined to a collection follows
// (laneScope.mayEnter, query_graph_scope.go), held to the caller's scope,
// its knowledge rule and its memory rule, instead of one collection.
//
// Filtering the walk, rather than the nodes it returns, is what makes the
// answer the one a graph holding only the served part would give: a limit
// counts served nodes, a score is that of the best served path, a shortest
// path is the shortest served one, and search_paths counts expansions over
// served edges. A filter after the walk could drop what the walk reached
// through a hidden node, but not give back what a limit, a visited set or a
// path cap lost to it. The served part is decided by the same functions
// that decide what is served at the end, so the two cannot disagree.
//
// Without a caller scope rule there is no filter, and every walk is the
// stored graph's, as before.

// walkAdmit is the step filter of a walk under the caller's scope, nil
// without a scope rule. It judges each node once per walk, so a node the
// walk meets from many sides is read once; a walk is one call, and the
// filter is not shared between calls or goroutines. When the nodes a step
// leads to cannot be read, the walk stops with the error; the checks that
// withoutExpiredDocuments and servedEdges make fail closed on their own.
func (db *DB) walkAdmit(ctx context.Context) graph.StepFilter {
	if scope, _ := callerScopeOf(ctx); scope.Knowledge == nil && scope.Memory == nil {
		return nil
	}
	served := map[string]bool{}
	return func(ctx context.Context, from string, edges []*graph.GraphEdge) ([]*graph.GraphEdge, error) {
		var unjudged []string
		for _, edge := range edges {
			if next := farEnd(edge, from); !mapHas(served, next) {
				unjudged = append(unjudged, next)
			}
		}
		if unjudged = slices.Compact(slices.Sorted(slices.Values(unjudged))); len(unjudged) > 0 {
			nodes, err := db.graph.GetNodesBatch(ctx, unjudged)
			if err != nil {
				return nil, fmt.Errorf("read the nodes of a walk: %w", err)
			}
			// A node that does not exist is not served: an edge left
			// dangling leads nowhere the caller may go.
			for _, id := range unjudged {
				served[id] = false
			}
			kept, _ := db.withoutExpiredDocuments(ctx, nodes, nil, nil)
			for _, node := range kept {
				served[node.ID] = true
			}
		}
		toServed := slices.DeleteFunc(slices.Clone(edges), func(edge *graph.GraphEdge) bool { return !served[farEnd(edge, from)] })
		return db.servedEdges(ctx, toServed), nil
	}
}

// walkNeighbors is graph.Neighbors under the caller's scope: the walk takes
// only what walkAdmit lets through. It is the walk search_chunks_by_entities
// takes outside a query.
func (db *DB) walkNeighbors(ctx context.Context, start string, opts graph.TraversalOptions) ([]*graph.GraphNode, error) {
	opts.Admit = db.walkAdmit(ctx)
	return db.graph.Neighbors(ctx, start, opts)
}

// farEnd is the node an edge leads to from the node a walk stands on.
func farEnd(edge *graph.GraphEdge, from string) string {
	if edge.FromNodeID == from {
		return edge.ToNodeID
	}
	return edge.FromNodeID
}

func mapHas[V any](m map[string]V, key string) bool {
	_, ok := m[key]
	return ok
}

// servedEdges is the edges a caller is served, as stored or as a copy that
// names less: withoutExpiredProvenance, and under a caller scope rule also
// withoutUnservedPremises. A walk follows these edges and no other
// (walkAdmit), and the subgraph a walk returns serves these (withoutExpiredDocuments).
func (db *DB) servedEdges(ctx context.Context, edges []*graph.GraphEdge) []*graph.GraphEdge {
	edges = db.withoutExpiredProvenance(ctx, edges)
	if scope, _ := callerScopeOf(ctx); scope.Knowledge == nil && scope.Memory == nil {
		return edges
	}
	return db.withoutUnservedPremises(ctx, edges)
}

// withoutUnservedPremises leaves out the edges that name no provenance of
// their own and rest on an edge the caller is not served. Inference
// (apply_inference, rules_apply) writes support_edge_ids on what it
// derives, and names a document only when every premise shares it: an edge
// derived from a relation only another collection states names nothing,
// and is that collection's statement all the same. It is served when each
// of its premises is, one level down, as laneScope.stated follows it: a
// premise that names provenance the caller is served, or one that names
// neither provenance nor premises of its own. A premise that rests on
// others in turn, or that cannot be read, is not, so an outage fails
// closed. An edge that names provenance is judged by it alone, by
// withoutExpiredProvenance, whatever it cites as support; an edge that
// names neither, as a relation written by upsert_relations without them,
// or the edge between a memory and the entity it mentions, is served.
func (db *DB) withoutUnservedPremises(ctx context.Context, edges []*graph.GraphEdge) []*graph.GraphEdge {
	var premiseIDs []string
	for _, edge := range edges {
		if restsOnPremises(edge) {
			premiseIDs = append(premiseIDs, edgeSupports(edge)...)
		}
	}
	if len(premiseIDs) == 0 {
		return edges
	}
	var premises []*graph.GraphEdge
	readable := true
	for _, batch := range laneIDBatches(premiseIDs, asOfVariables) {
		read, err := db.graph.GetEdgesBatch(ctx, batch)
		if err != nil {
			readable = false
			break
		}
		premises = append(premises, read...)
	}
	stated := map[string]bool{}
	if readable {
		named := make([]*graph.GraphEdge, 0, len(premises))
		for _, premise := range premises {
			doc, chunks := edgeProvenance(premise)
			switch {
			case doc != "" || len(chunks) > 0:
				named = append(named, premise)
			case len(edgeSupports(premise)) == 0:
				stated[premise.ID] = true
			}
		}
		for _, premise := range db.withoutExpiredProvenance(ctx, named) {
			stated[premise.ID] = true
		}
	}
	return slices.DeleteFunc(slices.Clone(edges), func(edge *graph.GraphEdge) bool {
		if !restsOnPremises(edge) {
			return false
		}
		return slices.ContainsFunc(edgeSupports(edge), func(id string) bool { return !stated[id] })
	})
}

// restsOnPremises reports whether an edge names no provenance of its own
// and rests on others: the edges withoutUnservedPremises judges by their
// premises.
func restsOnPremises(edge *graph.GraphEdge) bool {
	doc, chunks := edgeProvenance(edge)
	return doc == "" && len(chunks) == 0 && len(edgeSupports(edge)) > 0
}
