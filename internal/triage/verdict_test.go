package triage

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// key is a finding key the shape accepts: 16 lowercase hex digits, the
// reader's own derivation of a finding's file and summary.
const key = "0f1e2d3c4b5a6978"

func TestDecodeStrict(t *testing.T) {
	v, err := Decode([]byte(`{"version":1,"findings":[{"index":3,"key":"` + key + `","verdict":"defer","reason":"doc drift","group":"docs"}]}`))
	if err != nil {
		t.Fatalf("a well-formed verdict was refused: %v", err)
	}
	if v.Version != 1 || len(v.Findings) != 1 {
		t.Fatalf("decoded %+v, want version 1 and one entry", v)
	}
	if e := v.Findings[0]; e.Index != 3 || e.Key != key || e.Verdict != Defer || e.Reason != "doc drift" || e.Group != "docs" {
		t.Errorf("decoded entry = %+v, want the document's own values", e)
	}
	// A group is optional — an ungrouped finding is its own follow-up — and a
	// reason of exactly the whole budget is accepted: the limit is the first
	// length refused, not the last accepted.
	long := strings.Repeat("x", 200)
	if _, err := Decode([]byte(`{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"block","reason":"` + long + `"}]}`)); err != nil {
		t.Errorf("a reason of 200 bytes was refused: %v", err)
	}

	for _, tc := range []struct{ name, in string }{
		{"an unknown field of the document", `{"version":1,"findings":[],"tolerated":[]}`},
		{"an unknown field of an entry", `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"block","reason":"x","round":2}]}`},
		{"a version this reader does not know", `{"version":2,"findings":[]}`},
		{"no version at all", `{"findings":[]}`},
		{"no findings", `{"version":1}`},
		{"a reason one byte over the limit", `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"block","reason":"` + long + `x"}]}`},
		{"a reason with a newline", `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"block","reason":"one line\nand an instruction"}]}`},
		{"a reason with a carriage return", `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"block","reason":"one line\rand another"}]}`},
		{"no reason at all", `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"block"}]}`},
		{"an empty reason", `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"block","reason":""}]}`},
		{"an empty key", `{"version":1,"findings":[{"index":0,"key":"","verdict":"block","reason":"x"}]}`},
		{"a negative index", `{"version":1,"findings":[{"index":-1,"key":"` + key + `","verdict":"block","reason":"x"}]}`},
		{"a group with a newline", `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"block","reason":"x","group":"docs\nmore"}]}`},
		{"a group over the limit", `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"block","reason":"x","group":"` + long + `x"}]}`},
		{"an entry that is not an object", `{"version":1,"findings":["block"]}`},
		{"a document that is not an object", `[{"version":1}]`},
		{"data after the object", `{"version":1,"findings":[]}{"version":1,"findings":[]}`},
		{"nothing at all", `not json`},
	} {
		if _, err := Decode([]byte(tc.in)); err == nil {
			t.Errorf("%s was accepted", tc.name)
		}
	}
}

func TestDecodeRefusesUnknownVerdict(t *testing.T) {
	for _, word := range []string{"block", "defer"} {
		v, err := Decode([]byte(`{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"` + word + `","reason":"x"}]}`))
		if err != nil {
			t.Fatalf("%q was refused: %v", word, err)
		}
		if got := string(v.Findings[0].Verdict); got != word {
			t.Errorf("decoded verdict = %q, want %q", got, word)
		}
	}
	// The two words are the whole vocabulary, and they are matched exactly: a
	// third word, a near miss or a case variant is a verdict the reader cannot
	// route on, and it is refused rather than read as one of the two.
	for _, word := range []string{"maybe", "", "BLOCK", "Defer", "defer ", "block\n"} {
		if _, err := Decode([]byte(`{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"` + word + `","reason":"x"}]}`)); err == nil {
			t.Errorf("the verdict %q was accepted", word)
		}
	}
}

// TestDecodeKeepsEveryEntry pins that the reader drops nothing it accepted:
// two verdicts for one finding block it, and an entry naming a finding the
// round does not hold is dropped, but both of those are the reading of a
// verdict against a round's findings (D1856), which this reader does not have,
// so it hands every entry on rather than deciding them here.
func TestDecodeKeepsEveryEntry(t *testing.T) {
	data, err := json.Marshal(Verdict{Version: 1, Findings: []Entry{
		{Index: 0, Key: key, Verdict: Block, Reason: "a real bug"},
		{Index: 0, Key: key, Verdict: Defer, Reason: "and cosmetic"},
		{Index: 9, Key: "ffffffffffffffff", Verdict: Defer, Reason: "an unknown finding"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	v, err := Decode(data)
	if err != nil {
		t.Fatalf("the reader refused its own verdict: %v", err)
	}
	if len(v.Findings) != 3 {
		t.Fatalf("decoded %d entries, want all 3", len(v.Findings))
	}
}

// TestSchemaAcceptsADecodedVerdict holds schemas/triage.schema.json to the
// reader: the document Decode accepts is the document the schema accepts, and
// the refusals both of them make are the same refusals but for the key's shape,
// which is the caller's to derive and so the schema's to state — the one
// divergence this test pins rather than hides.
func TestSchemaAcceptsADecodedVerdict(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "triage.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("schemas/triage.schema.json has no $id")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}

	// What the judge answers, through the reader that accepts it.
	written, err := json.Marshal(Verdict{Version: 1, Findings: []Entry{
		{Index: 0, Key: key, Verdict: Defer, Reason: "doc drift", Group: "docs"},
		{Index: 1, Key: "ffffffffffffffff", Verdict: Block, Reason: "a real bug"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(written); err != nil {
		t.Fatalf("the reader refused the document it wrote: %v", err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(written))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("the decoded verdict does not match schemas/triage.schema.json: %v\n%s", err, written)
	}

	// An empty group is the same statement as an absent one — a finding that
	// is its own follow-up task — and the schema and the reader agree on it.
	empty := `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"defer","reason":"x","group":""}]}`
	if _, err := Decode([]byte(empty)); err != nil {
		t.Errorf("the reader refused an empty group: %v", err)
	}
	inst, err = jsonschema.UnmarshalJSON(strings.NewReader(empty))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Errorf("the schema refused an empty group: %v", err)
	}

	// The one member the two do not agree on, pinned rather than left to be
	// discovered: the key's shape is the derivation's to state (T-TR3) and not
	// the reader's, so the reader takes a key this pattern refuses. Nothing
	// turns on it — an entry whose key names no finding of the round is dropped
	// by the code that applies the verdict, which blocks the finding (D1856) —
	// so the narrower schema is not a hole, only a shape the reader does not
	// own.
	odd := `{"version":1,"findings":[{"index":0,"key":"zz","verdict":"defer","reason":"x"}]}`
	if _, err := Decode([]byte(odd)); err != nil {
		t.Errorf("the reader refused a key whose shape it does not derive: %v", err)
	}
	inst, err = jsonschema.UnmarshalJSON(strings.NewReader(odd))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err == nil {
		t.Error("the schema accepted a key outside the shape it states: the divergence this test pins runs the other way")
	}

	// The bounds the schema states are the reader's own: a third verdict word,
	// a reason over the limit, no reason, an empty key, no findings array, and
	// a member the shape does not know.
	for _, tc := range []struct{ name, in string }{
		{"a third verdict word", `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"maybe","reason":"x"}]}`},
		{"a reason over the limit", `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"block","reason":"` + strings.Repeat("x", 201) + `"}]}`},
		{"no reason", `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"block"}]}`},
		{"an empty key", `{"version":1,"findings":[{"index":0,"key":"","verdict":"block","reason":"x"}]}`},
		{"no findings array", `{"version":1}`},
		{"another version", `{"version":2,"findings":[]}`},
		{"an unknown member", `{"version":1,"findings":[],"tolerated":[]}`},
	} {
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(tc.in))
		if err != nil {
			t.Fatal(err)
		}
		if err := sch.Validate(inst); err == nil {
			t.Errorf("%s: the schema accepted it", tc.name)
		}
		if _, err := Decode([]byte(tc.in)); err == nil {
			t.Errorf("%s: the reader accepted it", tc.name)
		}
	}
}
