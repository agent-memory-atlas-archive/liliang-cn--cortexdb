package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/authz"
)

// POST /v1/graph/sparql runs updates as well as queries, so it is a write
// unless the executor's parser says the query does not mutate: a read-only
// key may SELECT and ASK over it, and is refused INSERT DATA and DELETE
// DATA, as it is on the tool route and over gRPC QuerySparql. A read-write
// key may do both.
func TestOverRESTAReadOnlyKeyCannotRunASPARQLUpdate(t *testing.T) {
	ks, err := authz.NewKeySet([]authz.Key{
		{ID: "operator", Secret: "op", Clearance: authz.ReadWrite},
		{ID: "reader", Secret: "r", Clearance: authz.ReadOnly},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := newPolicyServer(t, Options{Keys: ks})
	wantStatus(t, srv, http.MethodPost, "/v1/graph/sparql", "op", `{"query":"INSERT DATA { <urn:a> <urn:p> \"kept\" }"}`, http.StatusOK)

	for _, update := range []string{
		`{"query":"INSERT DATA { <urn:x> <urn:p> \"pwned\" }"}`,
		`{"query":"DELETE DATA { <urn:a> <urn:p> \"kept\" }"}`,
		`{"query":"  insert data { <urn:x> <urn:p> \"pwned\" }"}`,
	} {
		wantStatus(t, srv, http.MethodPost, "/v1/graph/sparql", "r", update, http.StatusForbidden)
	}
	_, body := do(t, srv, http.MethodPost, "/v1/graph/sparql", "r", `{"query":"SELECT ?o WHERE { ?s <urn:p> ?o }"}`)
	if !strings.Contains(string(body), "kept") || strings.Contains(string(body), "pwned") {
		t.Fatalf("a read-only key's SELECT: %s; want the operator's triple and none of its own", body)
	}
	wantStatus(t, srv, http.MethodPost, "/v1/graph/sparql", "r", `{"query":"ASK { <urn:a> <urn:p> ?o }"}`, http.StatusOK)
}
