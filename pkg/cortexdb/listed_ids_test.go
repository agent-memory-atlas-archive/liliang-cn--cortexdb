package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// The list forms of knowledge_get and memory_get serve a repeated id once,
// at its first place, and refuse a list longer than MaxListedIDs, so a small
// request cannot make the server load and serve one large row many times.
func TestTheListFormsServeEachIDOnceAndRefuseALongList(t *testing.T) {
	db := openListBrain(t)
	ctx := context.Background()
	for _, id := range []string{"k1", "k2"} {
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: id, Content: "The text of " + id + "."}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: "m1", UserID: "u", Scope: MemoryScopeUser, Content: "A memory."}); err != nil {
		t.Fatal(err)
	}

	served, missing, _, err := knowledgeGetList(t, db, ctx, `{"knowledge_ids":["k2","k1","k2","absent","k2","absent"]}`)
	if err != nil || !reflect.DeepEqual(served, []string{"k2", "k1"}) || !reflect.DeepEqual(missing, []string{"absent"}) {
		t.Errorf("knowledge: served %v, missing %v (%v); want [k2 k1], [absent]", served, missing, err)
	}
	mem, err := db.GetMemory(ctx, MemoryGetRequest{MemoryIDs: []string{"m1", "m1", "absent", "m1"}})
	if err != nil || len(mem.Memories) != 1 || !reflect.DeepEqual(mem.Missing, []string{"absent"}) {
		t.Errorf("memory: %+v (%v); want m1 once, [absent]", mem, err)
	}

	long := make([]string, MaxListedIDs+1)
	for i := range long {
		long[i] = fmt.Sprintf("k%d", i)
	}
	args, _ := json.Marshal(map[string]any{"knowledge_ids": long})
	if _, _, _, err := knowledgeGetList(t, db, ctx, string(args)); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Errorf("knowledge: %d ids were taken (%v)", len(long), err)
	}
	if _, err := db.GetMemory(ctx, MemoryGetRequest{MemoryIDs: long}); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Errorf("memory: %d ids were taken (%v)", len(long), err)
	}
	if _, err := db.GetMemory(ctx, MemoryGetRequest{MemoryIDs: long[:MaxListedIDs]}); err != nil {
		t.Errorf("memory: %d ids were refused: %v", MaxListedIDs, err)
	}
}
