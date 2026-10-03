package rtdb

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Tree is an in-memory copy of one streamed subtree, kept by folding the
// stream's put and patch events into it. It is pure (no I/O, no clock) and
// not safe for concurrent use. Keys are the wire keys (still budget.Key
// escaped). Like the database, it has no empty objects and no nulls: a null
// or an empty object deletes, and parents left empty are pruned.
type Tree struct {
	root any // nil, map[string]any, json.Number, string, bool or []any
}

func splitPath(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func parse(data json.RawMessage) (any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("rtdb: empty event data")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("rtdb: bad event data: %w", err)
	}
	if dec.More() {
		return nil, errors.New("rtdb: trailing data after event value")
	}
	return norm(v), nil
}

// norm drops nulls and empty objects, as the database does.
func norm(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	for k, c := range m {
		if c = norm(c); c == nil {
			delete(m, k)
		} else {
			m[k] = c
		}
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// Apply folds one event in. path is relative to the streamed root ("/" is
// the root itself). A put replaces the value at path (a put of "/" replaces
// the whole tree, so what vanished during an outage vanishes here). A patch
// merges: each key of data (itself a path) is put at path/key. The tree is
// unchanged when the data does not parse.
func (t *Tree) Apply(path string, data json.RawMessage, patch bool) error {
	base := splitPath(path)
	if !patch {
		v, err := parse(data)
		if err != nil {
			return err
		}
		t.set(base, v)
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		return errors.New("rtdb: patch data is not an object")
	}
	vals := make(map[string]any, len(raw))
	for k, r := range raw {
		v, err := parse(r)
		if err != nil {
			return err
		}
		vals[k] = v
	}
	for k, v := range vals { // all parsed: apply
		t.set(append(append([]string{}, base...), splitPath(k)...), v)
	}
	return nil
}

// set puts v (nil deletes) at the segments.
func (t *Tree) set(segs []string, v any) {
	t.root = setIn(t.root, segs, v)
}

func setIn(cur any, segs []string, v any) any {
	if len(segs) == 0 {
		return v
	}
	m, _ := cur.(map[string]any)
	if m == nil {
		if v == nil {
			return cur
		}
		m = map[string]any{}
	}
	child := setIn(m[segs[0]], segs[1:], v)
	if child == nil {
		delete(m, segs[0])
	} else {
		m[segs[0]] = child
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

func (t *Tree) node(path string) (any, bool) {
	cur := t.root
	for _, s := range splitPath(path) {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[s]; !ok {
			return nil, false
		}
	}
	return cur, cur != nil
}

// Has reports whether anything is stored at path.
func (t *Tree) Has(path string) bool { _, ok := t.node(path); return ok }

// Keys are the sorted child keys at path (none for a leaf or a missing node).
func (t *Tree) Keys(path string) []string {
	n, _ := t.node(path)
	m, _ := n.(map[string]any)
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Get is the JSON at path.
func (t *Tree) Get(path string) (json.RawMessage, bool) {
	n, ok := t.node(path)
	if !ok {
		return nil, false
	}
	b, err := json.Marshal(n)
	if err != nil {
		return nil, false
	}
	return b, true
}

// Decode unmarshals the value at path into v. found is false (v untouched)
// when nothing is stored there. A type mismatch is an error: the data is
// written by jobs, so callers treat it as an absent node.
func (t *Tree) Decode(path string, v any) (found bool, err error) {
	b, ok := t.Get(path)
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(b, v)
}

// Reset empties the tree.
func (t *Tree) Reset() { t.root = nil }
