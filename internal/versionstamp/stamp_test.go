package versionstamp

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// schemaPath is the published half of this contract, read here rather than
// trusted: a schema that stopped describing the stamp the decoder reads is a
// failure in this package, not in a document nobody's test reads.
const schemaPath = "../../schemas/required-cli.schema.json"

func stampSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(schemaPath))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatalf("%s has no $id", schemaPath)
	}
	c := jsonschema.NewCompiler()
	// The format assertion is on for this test alone: set_at is an instant, and
	// a schema that only said "string" would accept a hand edit Decode refuses.
	c.AssertFormat()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	return sch
}

func validatesAgainstStampSchema(t *testing.T, sch *jsonschema.Schema, raw string) error {
	t.Helper()
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return sch.Validate(inst)
}

const goodSetAt = "2026-11-23T10:04:05Z"

// stampJSON is the node one stamp writes, for a table's raw documents.
func stampJSON(schema int, min, setAt string) string {
	return `{"schema":` + strconv.Itoa(schema) + `,"min":"` + min + `","set_at":"` + setAt + `"}`
}

// TestStampDecodesStrictly: the node is the one document a CLI below it obeys,
// so every way of reading it wrong has to be a refusal rather than a default. An
// unknown member is the one this test was written for (the plan's red step): a
// decoder that tolerated it would read a node a later release or a hand edit
// wrote as the stamp it recognises, and the fence would be a value nobody meant.
// The members are read exactly and written once, which the typed decode cannot
// do: encoding/json matches a key case-insensitively and lets the later of two
// spellings win, so `MIN` and a repeated `min` would move the fence past
// DisallowUnknownFields.
func TestStampDecodesStrictly(t *testing.T) {
	at, err := time.Parse(time.RFC3339, goodSetAt)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		raw  string
		want Stamp  // the stamp a good document reads as
		why  string // what the refusal must say, "" when the stamp is read
	}{
		{
			name: "the shape the spec pins",
			raw:  stampJSON(1, "0.29.0", goodSetAt),
			want: Stamp{Schema: 1, Min: "0.29.0", SetAt: at},
		},
		{
			name: "a min with a v is the same stamp",
			raw:  stampJSON(1, "v0.29.0", goodSetAt),
			want: Stamp{Schema: 1, Min: "0.29.0", SetAt: at},
		},
		{
			name: "an unknown member",
			raw:  `{"schema":1,"min":"0.29.0","set_at":"` + goodSetAt + `","verdict":"refuse"}`,
			why:  "unknown member",
		},
		{
			name: "a member in another case",
			raw:  `{"schema":1,"MIN":"9.9.9","set_at":"` + goodSetAt + `"}`,
			why:  "unknown member",
		},
		{
			name: "a member written twice",
			raw:  `{"schema":1,"min":"0.29.0","min":"9.9.9","set_at":"` + goodSetAt + `"}`,
			why:  "twice",
		},
		{
			name: "a schema this fugaro does not read",
			raw:  stampJSON(2, "0.29.0", goodSetAt),
			why:  "upgrade with " + updatecheck.BrewCommand,
		},
		{
			name: "the schema member in another case",
			raw:  `{"SCHEMA":2,"min":"0.29.0","set_at":"` + goodSetAt + `"}`,
			why:  "unknown member",
		},
		{
			name: "the schema member written twice",
			raw:  `{"schema":1,"schema":2,"min":"0.29.0","set_at":"` + goodSetAt + `"}`,
			why:  "twice",
		},
		{
			name: "a schema that is not a number",
			raw:  `{"schema":"1","min":"0.29.0","set_at":"` + goodSetAt + `"}`,
			why:  "not a version number",
		},
		{
			name: "no schema at all",
			raw:  `{"min":"0.29.0","set_at":"` + goodSetAt + `"}`,
			why:  "no schema member",
		},
		{
			name: "a dev build is not a release",
			raw:  stampJSON(1, "dev", goodSetAt),
			why:  "not a release version",
		},
		{
			name: "a pre-release is not a release",
			raw:  stampJSON(1, "0.29.0-rc1", goodSetAt),
			why:  "not a release version",
		},
		{
			name: "no min at all",
			raw:  `{"schema":1,"set_at":"` + goodSetAt + `"}`,
			why:  "not a release version",
		},
		{
			name: "a set_at that is not an instant",
			raw:  stampJSON(1, "0.29.0", "2026-11-23"),
			why:  "could not be read",
		},
		{
			name: "no set_at at all",
			raw:  `{"schema":1,"min":"0.29.0"}`,
			why:  "no set_at",
		},
		{
			name: "not an object",
			raw:  `["schema",1]`,
			why:  "not one JSON object",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, found, err := Decode([]byte(tc.raw))
			if tc.why == "" {
				if err != nil || !found {
					t.Fatalf("Decode(%s) = %+v, %v, %v", tc.raw, got, found, err)
				}
				if got != tc.want {
					t.Errorf("Decode(%s) = %+v, want %+v", tc.raw, got, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("Decode(%s) = %+v, %v: a stamp this code cannot read must be refused", tc.raw, got, found)
			}
			var bad *MalformedError
			if !errors.As(err, &bad) {
				t.Fatalf("Decode(%s) = %v (%T), want a *MalformedError", tc.raw, err, err)
			}
			if !strings.Contains(err.Error(), tc.why) {
				t.Errorf("Decode(%s) = %q, want a sentence naming %q", tc.raw, err, tc.why)
			}
			if !strings.Contains(err.Error(), Path) {
				t.Errorf("Decode(%s) = %q: the sentence must name the node %s", tc.raw, err, Path)
			}
		})
	}

	// A node that is not there is an installation with no fence, not a refusal.
	for _, raw := range []string{"null", "", "  null\n"} {
		got, found, err := Decode([]byte(raw))
		if err != nil || found {
			t.Errorf("Decode(%q) = %+v, %v, %v: an absent node is no stamp and not a refusal", raw, got, found, err)
		}
	}
}

// TestMalformedSentenceNamesItsFix: the two things that can put a bad stamp on
// the node have two different repairs, and the refusal has to say which. An
// unknown schema was written by a newer release, so the person runs the upgrade;
// everything else was hand-edited, and D3262's no-lowering rule leaves the
// Firebase console as the only recovery there is (T7).
func TestMalformedSentenceNamesItsFix(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		fix  string
	}{
		{"a schema from a newer release", stampJSON(2, "0.29.0", goodSetAt), "upgrade with " + updatecheck.BrewCommand},
		{"a schema spelled another way", `{"Schema":2,"min":"0.29.0","set_at":"` + goodSetAt + `"}`, "Firebase console"},
		{"a min nobody wrote", stampJSON(1, "next", goodSetAt), "Firebase console"},
		{"a member nobody wrote", `{"schema":1,"min":"0.29.0","set_at":"` + goodSetAt + `","x":1}`, "Firebase console"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Decode([]byte(tc.raw))
			if err == nil {
				t.Fatalf("Decode(%s) was not refused", tc.raw)
			}
			if !strings.Contains(err.Error(), tc.fix) {
				t.Errorf("Decode(%s) = %q, want the fix %q", tc.raw, err, tc.fix)
			}
			if !strings.Contains(err.Error(), Path) {
				t.Errorf("Decode(%s) = %q, want the node %s named", tc.raw, err, Path)
			}
		})
	}
}

// TestEncodeWritesMinWithoutAV: the writer's bytes are what every later release
// reads, so one stamp has one spelling. A `v` a caller carries is dropped rather
// than refused, and the bytes are the canonical member set.
func TestEncodeWritesMinWithoutAV(t *testing.T) {
	at := time.Date(2026, 11, 23, 10, 4, 5, 0, time.UTC)
	for _, min := range []string{"0.29.0", "v0.29.0"} {
		raw, err := Encode(Stamp{Schema: SchemaVersion, Min: min, SetAt: at})
		if err != nil {
			t.Fatalf("Encode(%q): %v", min, err)
		}
		want := `{"schema":1,"min":"0.29.0","set_at":"2026-11-23T10:04:05Z"}`
		if string(raw) != want {
			t.Errorf("Encode(min %q) = %s, want %s", min, raw, want)
		}
		back, found, err := Decode(raw)
		if err != nil || !found {
			t.Fatalf("Decode(%s) = %+v, %v, %v", raw, back, found, err)
		}
		if back.Min != "0.29.0" || !back.SetAt.Equal(at) {
			t.Errorf("a round trip of %s = %+v", raw, back)
		}
	}
	for _, s := range []Stamp{
		{Schema: 2, Min: "0.29.0", SetAt: at},
		{Schema: SchemaVersion, Min: "dev", SetAt: at},
		{Schema: SchemaVersion, Min: "0.29.0"},
	} {
		if _, err := Encode(s); err == nil {
			t.Errorf("Encode(%+v) was accepted: a writer must refuse what no reader could use", s)
		}
	}
}

// TestSchemaAgreesWithDecode: the node's schema and the decoder are one
// contract, so a stamp Decode reads is one an editor accepts and a document the
// schema refuses is one Decode refuses. A member written twice is the one
// refusal the schema cannot make — JSON Schema has no way to say a key appears
// once — so it is the decoder's alone and is asserted in the table above.
func TestSchemaAgreesWithDecode(t *testing.T) {
	sch := stampSchema(t)
	at := time.Date(2026, 11, 23, 10, 4, 5, 0, time.UTC)
	for _, s := range []Stamp{
		{Schema: SchemaVersion, Min: "0.29.0", SetAt: at},
		{Schema: SchemaVersion, Min: "1.0.0", SetAt: at.Add(time.Hour)},
	} {
		raw, err := Encode(s)
		if err != nil {
			t.Fatalf("Encode(%+v): %v", s, err)
		}
		if err := validatesAgainstStampSchema(t, sch, string(raw)); err != nil {
			t.Errorf("%s refuses %s, which Encode wrote: %v", schemaPath, raw, err)
		}
	}
	for _, bad := range []string{
		`{"schema":1,"min":"0.29.0","set_at":"` + goodSetAt + `","verdict":"refuse"}`,
		`{"schema":1,"MIN":"9.9.9","set_at":"` + goodSetAt + `"}`,
		stampJSON(2, "0.29.0", goodSetAt),
		`{"min":"0.29.0","set_at":"` + goodSetAt + `"}`,
		stampJSON(1, "dev", goodSetAt),
		stampJSON(1, "0.29.0-rc1", goodSetAt),
		`{"schema":1,"set_at":"` + goodSetAt + `"}`,
		stampJSON(1, "0.29.0", "2026-11-23"),
		`{"schema":1,"min":"0.29.0"}`,
		`{"schema":"1","min":"0.29.0","set_at":"` + goodSetAt + `"}`,
	} {
		if err := validatesAgainstStampSchema(t, sch, bad); err == nil {
			t.Errorf("%s accepts %s, which Decode refuses", schemaPath, bad)
		}
		_, found, err := Decode([]byte(bad))
		if err == nil {
			t.Errorf("Decode(%s) = %v, nil: a document %s refuses is one Decode refuses too", bad, found, schemaPath)
			continue
		}
		if !found {
			t.Errorf("Decode(%s) = false, %v: the node is there, so a refusal keeps it", bad, err)
		}
		if !errors.As(err, new(*MalformedError)) {
			t.Errorf("Decode(%s) = %v: every stamp that is there and unreadable is a *MalformedError", bad, err)
		}
	}
	// A stamp the decoder reads but the schema does not is the other drift, and
	// the one that would let a writer publish bytes an editor calls invalid.
	for _, good := range []string{
		stampJSON(1, "0.29.0", goodSetAt),
		stampJSON(1, "v0.29.0", goodSetAt),
		stampJSON(1, "0.29.0", "2026-11-23T10:04:05.123456789Z"),
		stampJSON(1, "0.29.0", "2026-11-23T12:04:05+02:00"),
	} {
		if err := validatesAgainstStampSchema(t, sch, good); err != nil {
			t.Errorf("%s refuses %s, which Decode reads: %v", schemaPath, good, err)
		}
		if _, found, err := Decode([]byte(good)); err != nil || !found {
			t.Errorf("Decode(%s) = %v, %v: the sample must be one Decode reads", good, found, err)
		}
	}
}

// TestBelowIsFalseForDev: a dev build and a pre-release are not versions, so
// they are below nothing. That is the stance cliVersionCheck already takes, and
// it is what keeps an unstamped build from being fenced by every project.
func TestBelowIsFalseForDev(t *testing.T) {
	stamp := Stamp{Schema: SchemaVersion, Min: "0.29.0", SetAt: time.Now()}
	for _, tc := range []struct {
		version string
		want    bool
	}{
		{"0.28.0", true},
		{"0.28.9", true},
		{"0.29.0", false},
		{"0.30.0", false},
		{"1.0.0", false},
		{"dev", false},
		{"0.29.0-rc1", false},
		{"", false},
	} {
		if got := Below(stamp, tc.version); got != tc.want {
			t.Errorf("Below(%s, %q) = %v, want %v", stamp.Min, tc.version, got, tc.want)
		}
	}
	// A stamp this code would have refused carries nothing to compare with, so
	// it is below nothing either: the fence is never a parse accident.
	if Below(Stamp{Min: "not-a-version"}, "0.1.0") {
		t.Error("Below with an unparseable stamp reported a version below it")
	}
}

// TestBelowErrorIsTheSpecSentence: the refusal is one sentence, on the ordinary
// error path, with the brew command as its fix. Nothing but the required release,
// the date and the running version may vary in it.
func TestBelowErrorIsTheSpecSentence(t *testing.T) {
	at := time.Date(2026, 11, 23, 15, 4, 5, 0, time.UTC)
	err := &BelowError{Stamp: Stamp{Schema: SchemaVersion, Min: "0.29.0", SetAt: at}, Version: "0.28.0"}
	want := "this project requires fugaro 0.29.0 or newer (set by fugaro update on 2026-11-23) and this is 0.28.0, so nothing was done: upgrade with brew upgrade dimipaun/tap/fugaro, then run fugaro update"
	if err.Error() != want {
		t.Errorf("BelowError.Error() = %q, want %q", err.Error(), want)
	}
	var asErr error = err
	var bad *BelowError
	if !errors.As(asErr, &bad) {
		t.Error("a *BelowError does not read back as one")
	}
}
