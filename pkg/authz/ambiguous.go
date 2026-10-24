package authz

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode"
	"unicode/utf8"
)

// ErrAmbiguousKeys refuses a request body that holds an object with two keys
// encoding/json reads into the same field.
var ErrAmbiguousKeys = errors.New("request names one field twice")

// ReadsBody reports whether a policy decision about k depends on what a
// request body says: the scope fields and row ids a confined key is held to,
// and the SPARQL text that tells a read-only key's read from a write. An
// unconfined read-write key is decided by name alone.
func (k Key) ReadsBody() bool {
	return !k.Scope.IsZero() || k.Clearance != ReadWrite
}

// CheckUnambiguous refuses a JSON body in which some object, at any depth,
// holds two keys that encoding/json reads into the same struct field: the
// same key twice, or two spellings equal under its case folding ("query" and
// "QUERY", or "uſer_id" and "user_id").
//
// A policy reads a body into a map, where the last copy of a key wins and only
// its exact spelling is found; the handler decodes the same body into a
// struct, where a later null leaves the first value standing and any spelling
// of a field fills it. With two copies the two readers see different values:
// {"knowledge_id":"s1","knowledge_id":null} was a call the row guard saw no id
// in and the handler read s1 from, and {"query":"SELECT …","QUERY":"INSERT …"}
// a read to the policy and an update to the executor. One copy of each field
// is the only shape in which both readers agree, so a body that holds two is
// refused before any policy reads it, whatever the field means.
//
// Bodies that are not JSON are left to the policy and the handler, which
// refuse them on their own terms.
func CheckUnambiguous(body []byte) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	// One frame per open object or array. An object frame keeps the folded
	// keys it has seen and whether its next token is a key.
	type frame struct {
		object  bool
		keys    map[string]struct{}
		wantKey bool
	}
	var stack []*frame
	// pop closes the innermost object or array; the object holding it, if
	// any, wants its next key.
	pop := func() {
		stack = stack[:len(stack)-1]
		if n := len(stack); n > 0 && stack[n-1].object {
			stack[n-1].wantKey = true
		}
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			// Not JSON, or not JSON past this point: the policy and the
			// handler refuse it on their own terms.
			return nil
		}
		var top *frame
		if len(stack) > 0 {
			top = stack[len(stack)-1]
		}
		if top != nil && top.object && top.wantKey {
			if delim, ok := tok.(json.Delim); ok && delim == '}' {
				pop()
				continue
			}
			key, _ := tok.(string)
			folded := foldKey(key)
			if _, seen := top.keys[folded]; seen {
				return fmt.Errorf("%w: %q", ErrAmbiguousKeys, key)
			}
			top.keys[folded] = struct{}{}
			top.wantKey = false
			continue
		}
		switch delim, _ := tok.(json.Delim); delim {
		case '{':
			stack = append(stack, &frame{object: true, keys: map[string]struct{}{}, wantKey: true})
		case '[':
			stack = append(stack, &frame{})
		case ']':
			pop()
		default:
			// A scalar value: the object holding it wants its next key.
			if top != nil && top.object {
				top.wantKey = true
			}
		}
	}
}

// foldKey folds a key the way encoding/json matches it to a struct field
// (bytes.EqualFold): ASCII letters to upper case, and every other rune to
// the smallest rune of its case-fold set.
func foldKey(key string) string {
	out := make([]byte, 0, len(key))
	for i := 0; i < len(key); {
		if c := key[i]; c < utf8.RuneSelf {
			if 'a' <= c && c <= 'z' {
				c -= 'a' - 'A'
			}
			out = append(out, c)
			i++
			continue
		}
		r, n := utf8.DecodeRuneInString(key[i:])
		for {
			r2 := unicode.SimpleFold(r)
			if r2 <= r {
				r = r2
				break
			}
			r = r2
		}
		out = utf8.AppendRune(out, r)
		i += n
	}
	return string(out)
}
