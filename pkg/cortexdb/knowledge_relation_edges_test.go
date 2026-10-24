package cortexdb

import (
	"context"
	"reflect"
	"slices"
	"testing"
)

// Two documents that state the same relation write two edges, each naming
// its own document: the second does not take the first's edge, and with it
// the provenance a scope reads. The same relation twice in one document is
// one edge.
func TestTwoDocumentsStatingOneRelationKeepAnEdgeEach(t *testing.T) {
	db := openListBrain(t)
	ctx := context.Background()
	for _, doc := range []struct{ id, collection, text string }{
		{"a1", "alpha", "Kestrel Vale runs Alpha Mill."},
		{"b1", "beta", "Kestrel Vale runs Alpha Mill at a loss."},
	} {
		chunk := graphChunkNodeID(doc.id, 0)
		rel := ToolRelationInput{From: "Kestrel Vale", To: "Alpha Mill", Type: "runs", ChunkIDs: []string{chunk}}
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
			KnowledgeID: doc.id, Collection: doc.collection, Content: doc.text,
			Entities:  []ToolEntityInput{{Name: "Kestrel Vale", ChunkIDs: []string{chunk}}, {Name: "Alpha Mill", ChunkIDs: []string{chunk}}},
			Relations: []ToolRelationInput{rel, rel},
		}); err != nil {
			t.Fatal(err)
		}
	}
	edges, err := db.graph.GetEdges(ctx, resolveEntityNodeID("", "Kestrel Vale"), "out")
	if err != nil {
		t.Fatal(err)
	}
	var docs []string
	for _, edge := range edges {
		if edge.EdgeType == "runs" {
			doc, _ := edge.Properties["document_id"].(string)
			docs = append(docs, doc)
		}
	}
	slices.Sort(docs)
	if !reflect.DeepEqual(docs, []string{"a1", "b1"}) {
		t.Errorf("runs edges name documents %v, want one each for a1 and b1", docs)
	}
}
