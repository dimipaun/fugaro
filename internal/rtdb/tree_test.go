package rtdb

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestTreePutPatchDelete(t *testing.T) {
	type step struct {
		path  string
		data  string
		patch bool
	}
	tests := []struct {
		name  string
		steps []step
		want  string // JSON of the root, "" for empty
	}{
		{"put root", []step{{"/", `{"a":{"b":1}}`, false}}, `{"a":{"b":1}}`},
		{"put leaf", []step{{"/", `{"a":1}`, false}, {"/c/d", `2`, false}}, `{"a":1,"c":{"d":2}}`},
		{"put replaces subtree", []step{{"/", `{"a":{"b":1,"c":2}}`, false}, {"/a", `{"x":3}`, false}}, `{"a":{"x":3}}`},
		{"put null deletes and prunes", []step{{"/", `{"a":{"b":1},"z":1}`, false}, {"/a/b", `null`, false}}, `{"z":1}`},
		{"put null on missing", []step{{"/", `{"a":1}`, false}, {"/q/r", `null`, false}}, `{"a":1}`},
		{"empty object is null", []step{{"/", `{"a":1}`, false}, {"/a", `{}`, false}}, ``},
		{"nulls inside a put are dropped", []step{{"/", `{"a":{"b":null,"c":1}}`, false}}, `{"a":{"c":1}}`},
		{"patch merges", []step{{"/", `{"a":{"b":1,"c":2}}`, false}, {"/a", `{"c":9,"d":4}`, true}}, `{"a":{"b":1,"c":9,"d":4}}`},
		{"patch null deletes", []step{{"/", `{"a":{"b":1,"c":2}}`, false}, {"/a", `{"b":null}`, true}}, `{"a":{"c":2}}`},
		{"patch multi-segment key", []step{{"/", `{"a":{"b":{"c":1}}}`, false}, {"/", `{"a/b/c":2,"a/x":3}`, true}}, `{"a":{"b":{"c":2},"x":3}}`},
		{"patch on empty tree", []step{{"/", `{"k":{"v":1}}`, true}}, `{"k":{"v":1}}`},
		{"put under a leaf replaces it", []step{{"/", `{"a":1}`, false}, {"/a/b", `2`, false}}, `{"a":{"b":2}}`},
		{"put root null empties", []step{{"/", `{"a":1}`, false}, {"/", `null`, false}}, ``},
		{"big ints stay exact", []step{{"/", `{"m":123456789012345678}`, false}}, `{"m":123456789012345678}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tr Tree
			for _, s := range tt.steps {
				if err := tr.Apply(s.path, json.RawMessage(s.data), s.patch); err != nil {
					t.Fatal(err)
				}
			}
			got, ok := tr.Get("/")
			if tt.want == "" {
				if ok {
					t.Fatalf("want empty, got %s", got)
				}
				return
			}
			var g, w any
			json.Unmarshal(got, &g)
			json.Unmarshal([]byte(tt.want), &w)
			if !reflect.DeepEqual(g, w) {
				t.Fatalf("got %s want %s", got, tt.want)
			}
		})
	}
}

func TestTreeBadDataLeavesTreeUnchanged(t *testing.T) {
	var tr Tree
	tr.Apply("/", json.RawMessage(`{"a":1}`), false)
	for _, c := range []struct {
		data  string
		patch bool
	}{{`{`, false}, {``, false}, {`1 2`, false}, {`5`, true}, {`[1]`, true}} {
		if err := tr.Apply("/", json.RawMessage(c.data), c.patch); err == nil {
			t.Errorf("%q patch=%v: want an error", c.data, c.patch)
		}
	}
	if b, _ := tr.Get("/"); string(b) != `{"a":1}` {
		t.Fatalf("tree changed: %s", b)
	}
}

func TestTreeAccessors(t *testing.T) {
	var tr Tree
	tr.Apply("/", json.RawMessage(`{"b":{"x":1},"a":{"x":2},"n":"s"}`), false)
	if got := tr.Keys("/"); !reflect.DeepEqual(got, []string{"a", "b", "n"}) {
		t.Fatalf("keys %v", got)
	}
	if len(tr.Keys("n")) != 0 || len(tr.Keys("zz")) != 0 {
		t.Fatal("leaf or missing node has no keys")
	}
	var v struct{ X int }
	if ok, err := tr.Decode("a", &v); !ok || err != nil || v.X != 2 {
		t.Fatalf("decode %v %v %v", ok, err, v)
	}
	if ok, _ := tr.Decode("none", &v); ok {
		t.Fatal("missing node found")
	}
	if _, err := tr.Decode("n", &v); err == nil {
		t.Fatal("type mismatch must error")
	}
	tr.Reset()
	if tr.Has("/") {
		t.Fatal("reset")
	}
}

// A put of "/" after a reconnect replaces everything: what finished during
// the outage is gone.
func TestPutAfterReconnectReplacesTree(t *testing.T) {
	var tr Tree
	tr.Apply("/", json.RawMessage(`{"s":{"r1":{"stage":"x"},"r2":{"stage":"y"}}}`), false)
	tr.Apply("/", json.RawMessage(`{"s":{"r2":{"stage":"y"}}}`), false)
	if tr.Has("s/r1") || !tr.Has("s/r2") {
		t.Fatal("r1 should be gone, r2 kept")
	}
}
