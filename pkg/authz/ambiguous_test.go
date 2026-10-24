package authz

import (
	"errors"
	"testing"
)

func TestABodyThatNamesAFieldTwiceIsRefused(t *testing.T) {
	refused := map[string]string{
		"the same key, the second null":  `{"knowledge_id":"s1","knowledge_id":null}`,
		"the same key twice":             `{"memory_id":"z1","memory_id":"h1"}`,
		"a case variant":                 `{"query":"SELECT ?s WHERE { ?s ?p ?o }","QUERY":"INSERT DATA { <urn:x> <urn:p> 1 }"}`,
		"a long s (U+017F) folds to s":   `{"user_id":"hermes","uſer_id":"zeus"}`,
		"a Kelvin sign (U+212A) folds":   `{"knowledge_id":"a","Knowledge_id":"b"}`,
		"an escaped spelling":            `{"memory_id":"z1","memory_id":null}`,
		"nested in an object":            `{"plan":{"filters":{"collection":"notes","Collection":"secrets"}}}`,
		"nested in an array":             `{"sentences":[{"text":"a"},{"sources":["x"],"SOURCES":["y"]}]}`,
		"deep after a closed sub-object": `{"a":{"b":1},"c":[1,{"d":2}],"e":1,"E":2}`,
	}
	for name, body := range refused {
		if err := CheckUnambiguous([]byte(body)); !errors.Is(err, ErrAmbiguousKeys) {
			t.Errorf("%s: %s was not refused (%v)", name, body, err)
		}
	}
	accepted := map[string]string{
		"one copy of each field":          `{"knowledge_id":"s1","collection":"notes","metadata":{"knowledge_id":"x"}}`,
		"the same key in sibling objects": `{"sentences":[{"text":"a"},{"text":"b"}]}`,
		"different fields":                `{"query":"q","queries":["a"]}`,
		"empty":                           ``,
		"not JSON is left to the handler": `{"a":`,
		"an array at the top":             `[{"a":1},{"a":2}]`,
	}
	for name, body := range accepted {
		if err := CheckUnambiguous([]byte(body)); err != nil {
			t.Errorf("%s: %s was refused: %v", name, body, err)
		}
	}
}

func TestOnlyKeysWhosePolicyReadsTheBodyAreChecked(t *testing.T) {
	if (Key{Clearance: ReadWrite}).ReadsBody() {
		t.Error("an unconfined read-write key is decided by name alone")
	}
	if !(Key{Clearance: ReadOnly}).ReadsBody() {
		t.Error("a read-only key's SPARQL is classified from the body")
	}
	if !(Key{Clearance: ReadWrite, Scope: Scope{Collection: "notes"}}).ReadsBody() {
		t.Error("a confined key's scope is checked against the body")
	}
}
