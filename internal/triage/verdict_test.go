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
		// A null array is not an absent one: the member is there and holds no
		// entries, which is the one shape the schema refuses and a reader that
		// only checked for the member would accept.
		{"a findings array written null", `{"version":1,"findings":null}`},
		{"a reason one byte over the limit", `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"block","reason":"` + long + `x"}]}`},
		{"a reason with a newline", `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"block","reason":"one line\nand an instruction"}]}`},
		{"a reason with a carriage return", `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"block","reason":"one line\rand another"}]}`},
		{"no reason at all", `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"block"}]}`},
		{"no index at all, which reads as position 0", `{"version":1,"findings":[{"key":"` + key + `","verdict":"block","reason":"x"}]}`},
		{"an empty reason", `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"block","reason":""}]}`},
		{"an empty key", `{"version":1,"findings":[{"index":0,"key":"","verdict":"block","reason":"x"}]}`},
		{"a negative index", `{"version":1,"findings":[{"index":-1,"key":"` + key + `","verdict":"block","reason":"x"}]}`},
		{"a group with a newline", `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"block","reason":"x","group":"docs\nmore"}]}`},
		{"a group over the limit", `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"block","reason":"x","group":"` + long + `x"}]}`},
		{"an entry that is not an object", `{"version":1,"findings":["block"]}`},
		{"a document that is not an object", `[{"version":1}]`},
		{"data after the object", `{"version":1,"findings":[]}{"version":1,"findings":[]}`},
		{"nothing at all", `not json`},
		// encoding/json matches a member name whatever its case and keeps the
		// last of a repeated one, so without its own check the reader would take
		// these for verdicts where the schema, and any reader that takes the
		// exact name or the first one, sees something else.
		{"a member name in another case", `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"block","reason":"x","Group":"docs"}]}`},
		{"a case variant that would take the member", `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"block","reason":"x","VERDICT":"defer"}]}`},
		{"a member written twice", `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"block","reason":"x","verdict":"defer"}]}`},
		{"a document whose members are all in another case", `{"VERSION":1,"FINDINGS":[]}`},
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
// the refusals both of them make are the same refusals, but for the five the
// reader makes on its own. Three run in the second document's favour — the key's
// shape, which is the caller's to derive and so the schema's to state; a member
// name an object writes twice, which is one member by the time a parser has read
// the document; and a whole number written with a fraction or an exponent, which
// the schema reads as the number it is — and two are this document's own bounds
// stated in units the reader does not use: a length counted in characters where
// the reader counts bytes, and a minimum with nothing above it where the
// reader's integer type ends. This test pins all five divergences rather than
// hiding them, and the schema's description names them.
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

	// The second divergence, pinned the same way: a member name an object
	// writes twice is refused by the reader and invisible to the schema, since
	// a JSON object has one member of that name by the time a parser has read
	// it — and it is the last that wins, which is exactly why the reader does
	// not let the document say two things and leave the reading to whoever
	// parses it.
	twice := `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"block","reason":"x","verdict":"defer"}]}`
	if _, err := Decode([]byte(twice)); err == nil {
		t.Error("the reader accepted a member written twice, and read the last of them")
	}
	inst, err = jsonschema.UnmarshalJSON(strings.NewReader(twice))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Errorf("the schema refused a member written twice, which it cannot see: %v", err)
	}

	// The third divergence: this document reads a whole number written with a
	// fraction or an exponent as the number it is — version 1.0 is version 1,
	// index 0.0 is index 0 — so it accepts a document the reader refuses, whose
	// decoding of an integer takes the integer spelling alone.
	spelled := `{"version":1.0,"findings":[{"index":0,"key":"` + key + `","verdict":"block","reason":"x"}]}`
	if _, err := Decode([]byte(spelled)); err == nil {
		t.Error("the reader accepted a version spelled 1.0: its decoding of a whole number is the integer spelling alone")
	}
	inst, err = jsonschema.UnmarshalJSON(strings.NewReader(spelled))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Errorf("the schema refused a version spelled 1.0, which it reads as 1: %v", err)
	}

	// The fourth and fifth divergences are the two bounds this document states
	// in units the reader does not use: a length in characters where the reader
	// counts bytes, and a minimum with nothing above it where the reader's own
	// integer type ends. Both are accepted here and refused there, in the same
	// direction as the third.
	wide := `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"block","reason":"` + strings.Repeat("é", MaxLine) + `"}]}`
	if _, err := Decode([]byte(wide)); err == nil {
		t.Error("the reader accepted a reason of twice MaxLine bytes: its bound is bytes, this document's is characters")
	}
	inst, err = jsonschema.UnmarshalJSON(strings.NewReader(wide))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Errorf("the schema refused a reason of exactly MaxLine characters, which is its own bound: %v", err)
	}

	huge := `{"version":1,"findings":[{"index":99999999999999999999,"key":"` + key + `","verdict":"block","reason":"x"}]}`
	if _, err := Decode([]byte(huge)); err == nil {
		t.Error("the reader accepted an index past the largest its own integer type holds")
	}
	inst, err = jsonschema.UnmarshalJSON(strings.NewReader(huge))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Errorf("the schema refused an index past int64, which its minimum does not bound above: %v", err)
	}

	// An entry with no index is not a divergence: the schema requires the
	// member and so does the reader, which refuses an omission rather than
	// reading it as position 0.
	noIndex := `{"version":1,"findings":[{"key":"` + key + `","verdict":"block","reason":"x"}]}`
	if _, err := Decode([]byte(noIndex)); err == nil {
		t.Error("the reader accepted an entry with no index, reading it as position 0")
	}
	inst, err = jsonschema.UnmarshalJSON(strings.NewReader(noIndex))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err == nil {
		t.Error("the schema accepted an entry with no index, which it requires")
	}

	// The bounds the schema states are the reader's own: a third verdict word,
	// a reason over the limit, no reason, an empty key, a member name in
	// another case, no findings array, a findings array written null, and a
	// member the shape does not know.
	for _, tc := range []struct{ name, in string }{
		{"a third verdict word", `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"maybe","reason":"x"}]}`},
		{"a reason over the limit", `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"block","reason":"` + strings.Repeat("x", 201) + `"}]}`},
		{"no reason", `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"block"}]}`},
		{"an empty key", `{"version":1,"findings":[{"index":0,"key":"","verdict":"block","reason":"x"}]}`},
		{"no findings array", `{"version":1}`},
		{"a findings array written null", `{"version":1,"findings":null}`},
		{"another version", `{"version":2,"findings":[]}`},
		{"an unknown member", `{"version":1,"findings":[],"tolerated":[]}`},
		{"a member name in another case", `{"version":1,"findings":[{"index":0,"key":"` + key + `","verdict":"block","reason":"x","Group":"docs"}]}`},
		{"a document whose members are all in another case", `{"VERSION":1,"FINDINGS":[]}`},
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
