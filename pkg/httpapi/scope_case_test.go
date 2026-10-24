package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

// encoding/json matches object keys to struct fields case-insensitively, so a
// body can carry two spellings of one field, and the scope check could read a
// different copy than the handler's decoder does. A confined key's body with
// two spellings of one field is refused before the check reads it.
func TestAConfinedKeyCannotSmuggleAnotherUserThroughADifferentFieldCase(t *testing.T) {
	srv := newPolicyServer(t, Options{Keys: hermesAndZeus(t)})

	// Zeus's memory, written by the operator.
	wantStatus(t, srv, http.MethodPost, "/v1/memory", "op-secret",
		`{"memory_id":"z1","user_id":"zeus","scope":"user","content":"zeus secret pineapple"}`, http.StatusOK)

	for _, body := range []string{
		`{"user_id":"hermes","USER_ID":"zeus","query":"pineapple","top_k":5}`,
		`{"USER_ID":"zeus","user_id":"hermes","query":"pineapple","top_k":5}`,
	} {
		status, payload := do(t, srv, http.MethodPost, "/v1/memory/search", "hermes-secret", body)
		if status == http.StatusOK && strings.Contains(string(payload), "pineapple") {
			t.Fatalf("confined key read another user's memory via %s: %s", body, payload)
		}
		if status != http.StatusBadRequest {
			t.Fatalf("body %s = %d, want 400: %s", body, status, payload)
		}
	}
}

// Its own user_id once is served; twice, even with the same value, is a body
// that names one field twice, and refused.
func TestAConfinedKeyStillWorksWithItsOwnUserID(t *testing.T) {
	srv := newPolicyServer(t, Options{Keys: hermesAndZeus(t)})
	wantStatus(t, srv, http.MethodPost, "/v1/memory", "hermes-secret",
		`{"memory_id":"h1","user_id":"hermes","scope":"user","content":"hermes note"}`, http.StatusOK)
	wantStatus(t, srv, http.MethodPost, "/v1/memory/search", "hermes-secret",
		`{"user_id":"hermes","query":"note","top_k":5}`, http.StatusOK)
	wantStatus(t, srv, http.MethodPost, "/v1/memory/search", "hermes-secret",
		`{"user_id":"hermes","USER_ID":"hermes","query":"note","top_k":5}`, http.StatusBadRequest)
}
