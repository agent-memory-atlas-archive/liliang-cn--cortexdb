package cortexdb

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// upsertCollectionsDB is a store where a producer without an extractor
// writes the way such producers do: ingest_document for the text, in a
// collection, then upsert_entities naming the chunks that mention each
// entity. "Acme" is already known to secrets through knowledge_save.
func upsertCollectionsDB(t *testing.T) (*DB, map[string]string) {
	t.Helper()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "upsert-collections.db")))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	c := context.Background()
	if _, err := db.SaveKnowledge(c, KnowledgeSaveRequest{
		KnowledgeID: "secret-1", Collection: "secrets", Content: "Acme pays a hidden retainer of forty thousand.",
		Entities: []ToolEntityInput{{Name: "Acme", Type: "organization", ChunkIDs: []string{graphChunkNodeID("secret-1", 0)}}},
	}); err != nil {
		t.Fatalf("save secret-1: %v", err)
	}
	chunks := map[string]string{}
	for _, d := range []struct{ id, collection, content string }{
		{"notes-1", "notes", "Acme and Globex asked for a quote on the ledger migration."},
		{"summary-1", "summaries", "Acme is a customer of the ledger team."},
	} {
		resp, err := db.GraphRAGTools().IngestDocument(c, ToolIngestDocumentRequest{DocumentID: d.id, Collection: d.collection, Content: d.content})
		if err != nil {
			t.Fatalf("ingest %s: %v", d.id, err)
		}
		chunks[d.id] = resp.ChunkNodeIDs[0]
	}
	return db, chunks
}

func entityCollections(t *testing.T, db *DB, name string) ([]string, bool) {
	t.Helper()
	node, err := db.graph.GetNode(context.Background(), graphEntityNodeID(name))
	if err != nil {
		t.Fatalf("entity %s: %v", name, err)
	}
	value, ok := node.Properties[entityCollectionsKey]
	got := toStringSlice(value)
	slices.Sort(got)
	return got, ok
}

// upsert_entities records, on each entity, the collections of the chunks
// that mention it, added to what earlier writes recorded: the same list
// ingest and knowledge_save keep. A chunk the store has no collection for
// (a stub for a chunk never written) adds nothing, and an entity no chunk
// mentions keeps its properties as they were.
func TestUpsertEntitiesRecordsTheCollectionsOfTheChunksThatMentionAnEntity(t *testing.T) {
	db, chunks := upsertCollectionsDB(t)
	c := context.Background()
	if _, err := db.GraphRAGTools().UpsertEntities(c, ToolUpsertEntitiesRequest{
		DocumentID: "notes-1",
		Entities: []ToolEntityInput{
			{Name: "Acme", Type: "organization", ChunkIDs: []string{chunks["notes-1"], chunks["summary-1"]}},
			{Name: "Globex", Type: "organization", ChunkIDs: []string{chunks["notes-1"]}},
			{Name: "Initech", Type: "organization"},
			{Name: "Umbrella", Type: "organization", ChunkIDs: []string{"chunk:never-written:0"}},
		},
	}); err != nil {
		t.Fatalf("upsert_entities: %v", err)
	}
	for name, want := range map[string]string{"Acme": "notes,secrets,summaries", "Globex": "notes"} {
		if got, _ := entityCollections(t, db, name); strings.Join(got, ",") != want {
			t.Errorf("%s: collections %q, want %q", name, got, want)
		}
	}
	for _, name := range []string{"Initech", "Umbrella"} {
		if got, ok := entityCollections(t, db, name); ok {
			t.Errorf("%s: collections %q, want none: no stored chunk mentions it", name, got)
		}
	}

	// A later write that names no chunk keeps the list.
	if _, err := db.GraphRAGTools().UpsertEntities(c, ToolUpsertEntitiesRequest{
		Entities: []ToolEntityInput{{Name: "Globex", Type: "organization", Description: "a ledger customer"}},
	}); err != nil {
		t.Fatalf("upsert_entities again: %v", err)
	}
	if got, _ := entityCollections(t, db, "Globex"); strings.Join(got, ",") != "notes" {
		t.Errorf("Globex after a write without chunks: collections %q, want notes", got)
	}
}

// What the list is for: a caller confined to notes is served the entity a
// notes chunk mentions, by name, and not one only secrets knows.
func TestACallerConfinedToACollectionIsServedTheEntitiesItsChunksMention(t *testing.T) {
	db, chunks := upsertCollectionsDB(t)
	if _, err := db.GraphRAGTools().UpsertEntities(context.Background(), ToolUpsertEntitiesRequest{
		DocumentID: "notes-1",
		Entities:   []ToolEntityInput{{Name: "Globex", Type: "organization", ChunkIDs: []string{chunks["notes-1"]}}},
	}); err != nil {
		t.Fatalf("upsert_entities: %v", err)
	}
	notes := WithCallerScope(context.Background(), CallerScope{Key: "notes", Knowledge: func(collection string) bool { return collection == "notes" }})
	resp, err := db.GraphRAGTools().GetNodes(notes, ToolGetNodesRequest{NodeIDs: []string{graphEntityNodeID("Globex"), graphEntityNodeID("Acme")}})
	if err != nil {
		t.Fatalf("get_nodes: %v", err)
	}
	served := make([]string, 0, len(resp.Nodes))
	for _, node := range resp.Nodes {
		served = append(served, node.ID+"="+fmt.Sprint(node.Properties["name"]))
	}
	if got, want := strings.Join(served, ","), graphEntityNodeID("Globex")+"=Globex"; got != want {
		t.Fatalf("served %q, want %q", got, want)
	}
}

// The collections are read from the store; a store that cannot answer fails
// the write rather than leave the entities with a list that misses one.
func TestUpsertEntitiesFailsWhenTheChunkCollectionsCannotBeRead(t *testing.T) {
	db, chunks := upsertCollectionsDB(t)
	c := context.Background()
	if _, err := db.exec(c, "ALTER TABLE embeddings RENAME COLUMN collection_id TO collection_hidden"); err != nil {
		t.Fatalf("break the store: %v", err)
	}
	_, err := db.GraphRAGTools().UpsertEntities(c, ToolUpsertEntitiesRequest{
		Entities: []ToolEntityInput{{Name: "Globex", Type: "organization", ChunkIDs: []string{chunks["notes-1"]}}},
	})
	if err == nil || !strings.Contains(err.Error(), "load mention chunk collections") {
		t.Fatalf("err %v, want the chunk collections lookup to fail", err)
	}
}

// knowledge_update with entities only writes them through upsert_entities,
// so the same rule holds there: an entity that names a chunk of the
// knowledge gets that chunk's collection, one that names no chunk gets none.
func TestUpdateKnowledgeWithEntitiesOnlyRecordsTheCollectionsOfTheChunksItNames(t *testing.T) {
	db, _ := upsertCollectionsDB(t)
	if _, err := db.UpdateKnowledge(context.Background(), KnowledgeUpdateRequest{
		KnowledgeID: "secret-1",
		Entities: []ToolEntityInput{
			{Name: "Hooli", Type: "organization", ChunkIDs: []string{graphChunkNodeID("secret-1", 0)}},
			{Name: "Pied Piper", Type: "organization"},
		},
	}); err != nil {
		t.Fatalf("knowledge_update: %v", err)
	}
	if got, _ := entityCollections(t, db, "Hooli"); strings.Join(got, ",") != "secrets" {
		t.Errorf("Hooli: collections %q, want secrets", got)
	}
	if got, ok := entityCollections(t, db, "Pied Piper"); ok {
		t.Errorf("Pied Piper: collections %q, want none: it names no chunk", got)
	}
}

// memory_save writes its entities through upsert_entities too, naming the
// memory's own graph node, which has no row in embeddings: no list.
func TestAMemoryEntityGetsNoCollection(t *testing.T) {
	db, _ := upsertCollectionsDB(t)
	if _, err := db.SaveMemory(context.Background(), MemorySaveRequest{
		MemoryID: "memory-1", Content: "Hooli moved its ledger to the new cluster.",
		Entities: []ToolEntityInput{{Name: "Hooli", Type: "organization"}},
	}); err != nil {
		t.Fatalf("memory_save: %v", err)
	}
	if got, ok := entityCollections(t, db, "Hooli"); ok {
		t.Errorf("Hooli: collections %q, want none: a memory node is in no collection", got)
	}
}

// A caller key "collections" in metadata can only be a string
// (map[string]string). When the write names a stored chunk, the stored list
// is kept and extended and the caller's key is dropped; when it names none,
// the key replaces the property, as any named property does.
func TestUpsertEntitiesKeepsTheStoredListOverACallerCollectionsKey(t *testing.T) {
	db, chunks := upsertCollectionsDB(t)
	c := context.Background()
	key := map[string]string{entityCollectionsKey: "elsewhere"}
	if _, err := db.GraphRAGTools().UpsertEntities(c, ToolUpsertEntitiesRequest{
		Entities: []ToolEntityInput{{Name: "Acme", Type: "organization", Metadata: key, ChunkIDs: []string{chunks["notes-1"]}}},
	}); err != nil {
		t.Fatalf("upsert_entities with a chunk: %v", err)
	}
	if got, _ := entityCollections(t, db, "Acme"); strings.Join(got, ",") != "notes,secrets" {
		t.Errorf("Acme with a stored chunk: collections %q, want notes,secrets", got)
	}
	if _, err := db.GraphRAGTools().UpsertEntities(c, ToolUpsertEntitiesRequest{
		Entities: []ToolEntityInput{{Name: "Acme", Type: "organization", Metadata: key}},
	}); err != nil {
		t.Fatalf("upsert_entities without a chunk: %v", err)
	}
	node, err := db.graph.GetNode(c, graphEntityNodeID("Acme"))
	if err != nil {
		t.Fatalf("entity node: %v", err)
	}
	if got := node.Properties[entityCollectionsKey]; got != "elsewhere" {
		t.Errorf("Acme without a chunk: collections %#v, want the caller's string", got)
	}

	// No stored list: an entity that is new, and Acme, whose stored value is
	// now the caller's string. With a stored chunk, the list comes from the
	// chunks and the key is dropped.
	for _, name := range []string{"Globex", "Acme"} {
		if _, err := db.GraphRAGTools().UpsertEntities(c, ToolUpsertEntitiesRequest{
			Entities: []ToolEntityInput{{Name: name, Type: "organization", Metadata: key, ChunkIDs: []string{chunks["notes-1"]}}},
		}); err != nil {
			t.Fatalf("upsert_entities %s with a chunk and no stored list: %v", name, err)
		}
		if got, _ := entityCollections(t, db, name); strings.Join(got, ",") != "notes" {
			t.Errorf("%s with no stored list: collections %q, want notes", name, got)
		}
	}
}

// A stored chunk whose collection_id is NULL is in no collection: the write
// succeeds and adds nothing.
func TestUpsertEntitiesAddsNothingForAChunkWithNoCollection(t *testing.T) {
	db, chunks := upsertCollectionsDB(t)
	c := context.Background()
	if _, err := db.exec(c, "UPDATE embeddings SET collection_id = NULL WHERE id = ?", chunks["notes-1"]); err != nil {
		t.Fatalf("clear the chunk's collection: %v", err)
	}
	if _, err := db.GraphRAGTools().UpsertEntities(c, ToolUpsertEntitiesRequest{
		Entities: []ToolEntityInput{{Name: "Globex", Type: "organization", ChunkIDs: []string{chunks["notes-1"]}}},
	}); err != nil {
		t.Fatalf("upsert_entities: %v", err)
	}
	if got, ok := entityCollections(t, db, "Globex"); ok {
		t.Errorf("Globex: collections %q, want none: its chunk is in no collection", got)
	}
}
