package cortexdb

import "fmt"

// MaxListedIDs is the most ids the list form of knowledge_get and memory_get
// takes in one call.
//
// Each id is a row loaded and served whole, so the list is what a call
// costs: without a bound, a request of a few hundred bytes naming one large
// document a hundred times made the server build a reply of tens of
// megabytes, and a request at the transport's limit could exhaust its
// memory. A recall serves far fewer hits than this, so a caller picking ids
// from one never meets it.
const MaxListedIDs = 100

// listedIDs is the ids of a list form, each once, in the order first asked,
// refused past MaxListedIDs: a repeat is the same row and is not loaded or
// served again.
func listedIDs(ids []string) ([]string, error) {
	if len(ids) > MaxListedIDs {
		return nil, fmt.Errorf("at most %d ids per call, got %d", MaxListedIDs, len(ids))
	}
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}
