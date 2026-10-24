package cortexdb

import (
	"context"
	"strings"
	"testing"
)

// The graph expansion of a search confined to a collection walks only what
// the collection may see: once beta states that Kestrel Vale acquires Heron
// Mill, a search of alpha seeded on Kestrel Vale must not reach alpha's
// Heron Mill chunk through beta's relation, with or without a caller scope.
func TestASearchOfOneCollectionDoesNotExpandThroughAnotherCollectionsRelation(t *testing.T) {
	db := walkScopeBrain(t)
	search := func(ctx context.Context) []string {
		t.Helper()
		out, err := db.GraphRAGTools().SearchGraphRAGLexical(ctx, ToolSearchGraphRAGLexicalRequest{
			Query: "Kestrel Vale", Collection: "alpha", RetrievalMode: RetrievalModeGraph,
			EntityNames: []string{"Kestrel Vale"}, MaxHops: 3, TopK: 10, MaxRelatedChunks: 10,
		})
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, c := range out.Chunks {
			ids = append(ids, c.ID)
		}
		return ids
	}
	contexts := map[string]context.Context{
		"no scope":    context.Background(),
		"alpha scope": WithCallerScope(context.Background(), alphaOnly()),
	}
	before := map[string]string{}
	for name, ctx := range contexts {
		before[name] = strings.Join(search(ctx), ",")
	}
	if _, err := db.SaveKnowledge(context.Background(), betaStatesARelation); err != nil {
		t.Fatal(err)
	}
	for name, ctx := range contexts {
		after := search(ctx)
		if got := strings.Join(after, ","); got != before[name] {
			t.Errorf("%s: alpha's search changes once beta states a relation\nbefore: %s\nafter:  %s", name, before[name], got)
		}
		if strings.Contains(strings.Join(after, ","), graphChunkNodeID("a2", 0)) {
			t.Errorf("%s: alpha's Heron Mill chunk was reached through beta's relation: %v", name, after)
		}
	}
}
