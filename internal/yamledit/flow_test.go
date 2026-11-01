package yamledit_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/yamledit"
	"gopkg.in/yaml.v3"
)

// TestSetScalarInsideAFlowMapping holds the default project config's budget
// block — `budget: { mode: enforce, per_run_usd: 5 }`, the shape setup writes —
// to the promise every other edit gets: only the bytes of the value change, and
// every other byte of the line stays as it was.
func TestSetScalarInsideAFlowMapping(t *testing.T) {
	for _, tc := range []struct {
		name, doc, want string
		path            []string
		value           string
	}{
		{
			"the default budget block",
			"project: workspace-a\nbudget: { mode: enforce, per_run_usd: 5, per_day_usd: 20 }\n",
			"project: workspace-a\nbudget: { mode: enforce, per_run_usd: 10, per_day_usd: 20 }\n",
			[]string{"budget", "per_run_usd"}, "10",
		},
		{
			"the first member",
			"budget: { mode: enforce, per_run_usd: 5 }\n",
			"budget: { mode: observe, per_run_usd: 5 }\n",
			[]string{"budget", "mode"}, "observe",
		},
		{
			"the last member",
			"budget: { mode: enforce, per_run_usd: 5 }\n",
			"budget: { mode: enforce, per_run_usd: 9 }\n",
			[]string{"budget", "per_run_usd"}, "9",
		},
		{
			"a mapping written without spaces",
			"budget: {per_run_usd: 5}\n",
			"budget: {per_run_usd: 10}\n",
			[]string{"budget", "per_run_usd"}, "10",
		},
		{
			"a member written with a trailing comma",
			"budget: { per_run_usd: 5, }\n",
			"budget: { per_run_usd: 10, }\n",
			[]string{"budget", "per_run_usd"}, "10",
		},
		{
			"a member written with no value",
			"budget: { mode: , per_run_usd: 5 }\n",
			"budget: { mode: enforce, per_run_usd: 5 }\n",
			[]string{"budget", "mode"}, "enforce",
		},
		{
			"a value that reads as text only between braces",
			"budget: { note: -, per_run_usd: 5 }\n",
			"budget: { note: x, per_run_usd: 5 }\n",
			[]string{"budget", "note"}, "x",
		},
		{
			"a key added to a mapping that already has members",
			"budget: { per_run_usd: 5 }\n",
			"budget: { per_run_usd: 5, mode: enforce }\n",
			[]string{"budget", "mode"}, "enforce",
		},
		{
			"a key added beside a trailing comma",
			"budget: { per_run_usd: 5, }\n",
			"budget: { per_run_usd: 5, mode: enforce }\n",
			[]string{"budget", "mode"}, "enforce",
		},
		{
			"a quoted member keeps its quotes",
			"budget: { mode: 'observe', per_run_usd: 5 }\n",
			"budget: { mode: 'enforce', per_run_usd: 5 }\n",
			[]string{"budget", "mode"}, "enforce",
		},
		{
			"a value that would read as two members is quoted",
			"budget: { note: plain, per_run_usd: 5 }\n",
			"budget: { note: \"x, y\", per_run_usd: 5 }\n",
			[]string{"budget", "note"}, "x, y",
		},
		{
			"a value holding a brace is quoted",
			"budget: { note: plain, per_run_usd: 5 }\n",
			"budget: { note: \"}\", per_run_usd: 5 }\n",
			[]string{"budget", "note"}, "}",
		},
		{
			"a value holding a comma inside quotes is left alone",
			"budget: { note: 'x, y', per_run_usd: 5 }\n",
			"budget: { note: 'x, y', per_run_usd: 10 }\n",
			[]string{"budget", "per_run_usd"}, "10",
		},
		{
			"a member beside one written as a flow sequence",
			"budget: { allowed_models: [a, b], per_run_usd: 5 }\n",
			"budget: { allowed_models: [a, b], per_run_usd: 10 }\n",
			[]string{"budget", "per_run_usd"}, "10",
		},
		{
			"the root written as a flow mapping",
			"{a: 1, b: 2}\n",
			"{a: 3, b: 2}\n",
			[]string{"a"}, "3",
		},
		{
			"a document whose lines end the other way",
			"a: 1\r\nbudget: { per_run_usd: 5 }\r\n",
			"a: 1\r\nbudget: { per_run_usd: 10 }\r\n",
			[]string{"budget", "per_run_usd"}, "10",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := yamledit.SetScalar([]byte(tc.doc), tc.path, tc.value)
			if err != nil {
				t.Fatalf("SetScalar: %v", err)
			}
			if !bytes.Equal(got, []byte(tc.want)) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			reparses(t, got)
			if text, ok := textAt(t, got, tc.path...); !ok || text != tc.value {
				t.Fatalf("%v reads back as %q (%v), want %q", tc.path, text, ok, tc.value)
			}
			if changedLine(t, tc.doc, tc.want) == 0 {
				t.Fatalf("the edit moved bytes on more than one line:\n%q\n%q", tc.doc, got)
			}
		})
	}
}

// TestSetScalarInsideAFlowMappingIsANoOpWhenTheValueIsAlreadyThere pins that a
// splice is not written when the bytes it would write are the bytes that are
// there, in a flow mapping as in a block one.
func TestSetScalarInsideAFlowMappingIsANoOpWhenTheValueIsAlreadyThere(t *testing.T) {
	const doc = "budget: { mode: 'enforce', per_run_usd: 5 }\n"
	got, err := yamledit.SetScalar([]byte(doc), []string{"budget", "per_run_usd"}, "5")
	if err != nil {
		t.Fatalf("SetScalar: %v", err)
	}
	if string(got) != doc {
		t.Fatalf("the same value rewrote the document: %q", got)
	}
}

// TestRemoveKeyInsideAFlowMapping holds a removal inside `{...}` to the same
// rule: the member and one separator go, and every other byte stays.
func TestRemoveKeyInsideAFlowMapping(t *testing.T) {
	for _, tc := range []struct {
		name, doc, want string
		path            []string
	}{
		{
			"the first member",
			"budget: { mode: enforce, per_run_usd: 5, per_day_usd: 20 }\n",
			"budget: { per_run_usd: 5, per_day_usd: 20 }\n",
			[]string{"budget", "mode"},
		},
		{
			"a member in the middle",
			"budget: { mode: enforce, per_run_usd: 5, per_day_usd: 20 }\n",
			"budget: { mode: enforce, per_day_usd: 20 }\n",
			[]string{"budget", "per_run_usd"},
		},
		{
			"the last member",
			"budget: { mode: enforce, per_run_usd: 5 }\n",
			"budget: { mode: enforce }\n",
			[]string{"budget", "per_run_usd"},
		},
		{
			"the only member",
			"budget: { per_run_usd: 5 }\n",
			"budget: {}\n",
			[]string{"budget", "per_run_usd"},
		},
		{
			"a mapping written without spaces",
			"budget: {per_run_usd: 5, mode: enforce}\n",
			"budget: {mode: enforce}\n",
			[]string{"budget", "per_run_usd"},
		},
		{
			"a member written with a trailing comma",
			"budget: { per_run_usd: 5, }\n",
			"budget: { }\n",
			[]string{"budget", "per_run_usd"},
		},
		{
			"a member written with no value",
			"budget: { mode: , per_run_usd: 5 }\n",
			"budget: { per_run_usd: 5 }\n",
			[]string{"budget", "mode"},
		},
		{
			"the only member written with no value",
			"budget: { mode: }\n",
			"budget: {}\n",
			[]string{"budget", "mode"},
		},
		{
			"a member whose value reads as text only between braces",
			"budget: { note: -, per_run_usd: 5 }\n",
			"budget: { per_run_usd: 5 }\n",
			[]string{"budget", "note"},
		},
		{
			"a member beside one written as a flow sequence",
			"budget: { allowed_models: [a, b], per_run_usd: 5 }\n",
			"budget: { allowed_models: [a, b] }\n",
			[]string{"budget", "per_run_usd"},
		},
		{
			"the root written as a flow mapping",
			"{a: 1, b: 2}\n",
			"{b: 2}\n",
			[]string{"a"},
		},
		{
			"a key the mapping does not have",
			"budget: { per_run_usd: 5 }\n",
			"budget: { per_run_usd: 5 }\n",
			[]string{"budget", "mode"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := yamledit.RemoveKey([]byte(tc.doc), tc.path)
			if err != nil {
				t.Fatalf("RemoveKey: %v", err)
			}
			if !bytes.Equal(got, []byte(tc.want)) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			reparses(t, got)
			if text, ok := textAt(t, got, tc.path...); ok {
				t.Fatalf("%v is still in %q (%q)", tc.path, got, text)
			}
			if tc.want != tc.doc && changedLine(t, tc.doc, tc.want) == 0 {
				t.Fatalf("the edit moved bytes on more than one line:\n%q\n%q", tc.doc, got)
			}
		})
	}
}

// TestRemoveKeyInsideAFlowMappingDropsAnEmptyParent pins that the cascade over
// the parents a removal leaves empty reaches through a flow mapping: the key
// holding a flow mapping left with no members goes with the member removed.
func TestRemoveKeyInsideAFlowMappingDropsAnEmptyParent(t *testing.T) {
	const doc = "a: 1\nbudget: { per_run_usd: 5 }\n"
	got, err := yamledit.RemoveKeyWithOptions([]byte(doc), []string{"budget", "per_run_usd"},
		yamledit.RemoveOptions{DropEmptyParents: true})
	if err != nil {
		t.Fatalf("RemoveKeyWithOptions: %v", err)
	}
	if want := "a: 1\n"; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	reparses(t, got)
}

// TestFlowEditKeepsCommentsAndQuoting pins the other half of the promise on the
// shape most installations have: the comments above and beside the line, and
// the quoting of every member the edit does not have to rewrite, stay byte for
// byte.
func TestFlowEditKeepsCommentsAndQuoting(t *testing.T) {
	const doc = "# what this repository may spend\n" +
		"budget: { mode: 'observe', note: \"hand-written\", per_run_usd: 5 }  # keep this note\n" +
		"other: 'single'\n"

	got, err := yamledit.SetScalar([]byte(doc), []string{"budget", "per_run_usd"}, "10")
	if err != nil {
		t.Fatalf("SetScalar: %v", err)
	}
	want := strings.Replace(doc, "per_run_usd: 5", "per_run_usd: 10", 1)
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	reparses(t, got)
	if !sameLines(commentLines(doc), commentLines(string(got))) {
		t.Fatalf("a comment went missing:\n%q\n%q", doc, got)
	}

	got, err = yamledit.SetScalar([]byte(doc), []string{"budget", "mode"}, "enforce")
	if err != nil {
		t.Fatalf("SetScalar: %v", err)
	}
	want = strings.Replace(doc, "'observe'", "'enforce'", 1)
	if string(got) != want {
		t.Fatalf("a single-quoted value lost its quotes: got %q, want %q", got, want)
	}

	got, err = yamledit.RemoveKey([]byte(doc), []string{"budget", "note"})
	if err != nil {
		t.Fatalf("RemoveKey: %v", err)
	}
	want = strings.Replace(doc, ", note: \"hand-written\"", "", 1)
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	reparses(t, got)
	if !sameLines(commentLines(doc), commentLines(string(got))) {
		t.Fatalf("a comment went missing:\n%q\n%q", doc, got)
	}
}

// TestFlowEditRefusesNestedFlow pins what a flow mapping this package will not
// edit looks like: a collection written across lines, one holding a comment, a
// member whose own value is a collection, and an empty mapping, whose shape
// writing the first member would decide.
func TestFlowEditRefusesNestedFlow(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
		op   func([]byte) ([]byte, error)
	}{
		{
			"a path into a nested flow mapping",
			"a: {b: {c: 1}}\n",
			func(d []byte) ([]byte, error) { return yamledit.SetScalar(d, []string{"a", "b", "c"}, "2") },
		},
		{
			"a value written as a nested flow mapping",
			"a: {b: {c: 1}}\n",
			func(d []byte) ([]byte, error) { return yamledit.SetScalar(d, []string{"a", "b"}, "2") },
		},
		{
			"a member holding a nested flow mapping",
			"a: {b: {c: 1}}\n",
			func(d []byte) ([]byte, error) { return yamledit.RemoveKey(d, []string{"a", "b"}) },
		},
		{
			"a value written as a flow sequence",
			"a: {b: [1, 2]}\n",
			func(d []byte) ([]byte, error) { return yamledit.SetScalar(d, []string{"a", "b"}, "2") },
		},
		{
			"a member holding a flow sequence",
			"a: {b: [1, 2]}\n",
			func(d []byte) ([]byte, error) { return yamledit.RemoveKey(d, []string{"a", "b"}) },
		},
		{
			"a collection written across lines",
			"a: {b: 1,\n  c: 2}\n",
			func(d []byte) ([]byte, error) { return yamledit.SetScalar(d, []string{"a", "b"}, "2") },
		},
		{
			"a collection written across lines, removed from",
			"a: {b: 1,\n  c: 2}\n",
			func(d []byte) ([]byte, error) { return yamledit.RemoveKey(d, []string{"a", "b"}) },
		},
		{
			"a collection holding a comment",
			"a: {b: 1 # note\n}\n",
			func(d []byte) ([]byte, error) { return yamledit.SetScalar(d, []string{"a", "b"}, "2") },
		},
		{
			"the first member of an empty mapping",
			"a: {}\n",
			func(d []byte) ([]byte, error) { return yamledit.SetScalar(d, []string{"a", "b"}, "2") },
		},
		{
			"an item added to a sequence written as a flow sequence",
			"a: [x]\n",
			func(d []byte) ([]byte, error) { return yamledit.AddListItem(d, []string{"a"}, "y") },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.op([]byte(tc.doc))
			if r := yamledit.ReasonOf(err); r != yamledit.ReasonFlowCollection {
				t.Fatalf("reason = %q, want %q (%v)", r, yamledit.ReasonFlowCollection, err)
			}
			if !bytes.Equal(got, []byte(tc.doc)) {
				t.Fatalf("a refusal returned changed bytes: %q", got)
			}
		})
	}
}

// TestFlowEditRefusesAKeyInAStringValue pins that a member is found where the
// document writes a key, never where its text happens to appear: a
// `per_run_usd` inside a quoted value is not the member of that name, and the
// bytes that spell it are left alone. A quoted value holding the separators of
// the collection around it is the same test from the other side.
func TestFlowEditRefusesAKeyInAStringValue(t *testing.T) {
	const doc = "budget: { note: 'set per_run_usd: 5 to cap it', per_run_usd: 5 }\n"

	got, err := yamledit.SetScalar([]byte(doc), []string{"budget", "per_run_usd"}, "10")
	if err != nil {
		t.Fatalf("SetScalar: %v", err)
	}
	want := "budget: { note: 'set per_run_usd: 5 to cap it', per_run_usd: 10 }\n"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	reparses(t, got)

	got, err = yamledit.RemoveKey([]byte(doc), []string{"budget", "per_run_usd"})
	if err != nil {
		t.Fatalf("RemoveKey: %v", err)
	}
	want = "budget: { note: 'set per_run_usd: 5 to cap it' }\n"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	reparses(t, got)

	const braces = "budget: { note: \"a: b, c}\", per_run_usd: 5 }\n"
	got, err = yamledit.SetScalar([]byte(braces), []string{"budget", "per_run_usd"}, "10")
	if err != nil {
		t.Fatalf("SetScalar: %v", err)
	}
	want = "budget: { note: \"a: b, c}\", per_run_usd: 10 }\n"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	reparses(t, got)
}

// textAt returns the text of the scalar written at a path, read out of the
// document the way any reader of the file reads it.
func textAt(t *testing.T, doc []byte, path ...string) (string, bool) {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal(doc, &n); err != nil {
		t.Fatalf("the result does not parse: %v\n%s", err, doc)
	}
	if n.Kind != yaml.DocumentNode || len(n.Content) == 0 {
		return "", false
	}
	cur := n.Content[0]
	for _, key := range path {
		if cur.Kind != yaml.MappingNode {
			return "", false
		}
		var next *yaml.Node
		for i := 0; i+1 < len(cur.Content); i += 2 {
			if cur.Content[i].Value == key {
				next = cur.Content[i+1]
			}
		}
		if next == nil {
			return "", false
		}
		cur = next
	}
	if cur.Kind != yaml.ScalarNode {
		return "", false
	}
	return cur.Value, true
}

func sameLines(want, got []string) bool {
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		if want[i] != got[i] {
			return false
		}
	}
	return true
}
