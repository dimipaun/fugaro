package runstore

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/rigor"
)

// TestRigorRecordIsTheJSONTheSchemaAndTheConsoleRead pins the block's keys
// and its two absences, by exact string: the record is read by `fugaro runs
// ls --json`, `fugaro diagnose` and the console, all through the same struct,
// so a rename here is a contract change and this is where it is caught.
func TestRigorRecordIsTheJSONTheSchemaAndTheConsoleRead(t *testing.T) {
	rec := Record{Rigor: &RigorRecord{
		Mode: rigor.Critical, From: rigor.FromArea,
		Areas:    []RigorArea{{Name: "payments", At: RigorAtBootstrap}, {Name: "docs", At: RigorAtRound}},
		At:       RigorAtFinalize,
		Revision: "origin/main",
	}}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"rigor":`) {
		t.Fatalf("the record does not carry the block under %q: %s", "rigor", data)
	}
	block, err := json.Marshal(rec.Rigor)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"mode":"critical","from":"area",` +
		`"areas":[{"name":"payments","at":"bootstrap"},{"name":"docs","at":"round"}],` +
		`"at":"finalize","revision":"origin/main"}`
	if string(block) != want {
		t.Errorf("the rigor block marshals as\n%s\nwant\n%s", block, want)
	}
	// A run that matched no rule carries neither an area list nor a revision:
	// absent means "none matched" and "the run could not name one", never an
	// empty list a reader would render as a rule that matched nothing.
	none, err := json.Marshal(&RigorRecord{Mode: rigor.Default, From: rigor.FromDefault, At: RigorAtFinalize})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"mode":"pro","from":"default","at":"finalize"}`; string(none) != want {
		t.Errorf("a block with no areas marshals as\n%s\nwant\n%s", none, want)
	}
	// And a record with no block writes no key at all, so a record from
	// before this field existed is the record an old reader saw.
	bare, err := json.Marshal(Record{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bare), "rigor") {
		t.Errorf("a record with no block carries one anyway: %s", bare)
	}
}

// TestRigorRecordFoldNeverLowers pins D22's monotone rule at the record:
// three moments compose into one block, a later moment never takes the mode
// down, and every area that ever matched is recorded with the moment it was
// first applied (D23, D24). The block's `at` is the moment that last changed
// it, so a moment that changes nothing leaves the record as it was.
func TestRigorRecordFoldNeverLowers(t *testing.T) {
	boot := Moment{Resolved: rigor.Resolved{Mode: rigor.Critical, From: rigor.FromArea, Areas: []string{"payments", "docs"}}, At: RigorAtBootstrap, Revision: "origin/main"}
	round := Moment{Resolved: rigor.Resolved{Mode: rigor.Critical, From: rigor.FromArea, Areas: []string{"web"}}, At: RigorAtRound, Revision: "origin/main"}
	final := Moment{Resolved: rigor.Resolved{Mode: rigor.Startup, From: rigor.FromRepo}, At: RigorAtFinalize, Revision: "origin/main"}

	var rec *RigorRecord
	rec = rec.Applied(boot)
	rec = rec.Applied(final)
	if rec.Mode != rigor.Critical || rec.From != rigor.FromArea {
		t.Errorf("a weaker finalize moment took the mode down: %+v", rec)
	}
	if rec.At != RigorAtBootstrap {
		t.Errorf("a moment that changed nothing moved the block's moment to %q", rec.At)
	}
	rec = rec.Applied(round)
	if got, want := areaNames(rec.Areas), "payments,docs,web"; strings.Join(got, ",") != want {
		t.Errorf("areas after three moments = %v; want %s (every area the run matched, the deciding moment's first)", got, want)
	}
	if rec.At != RigorAtRound {
		t.Errorf("the moment that added an area did not become the block's moment: %q", rec.At)
	}
	// Each area keeps the FIRST moment it was applied, so a rule re-matched
	// at finalize does not read as one the run met only at the end.
	if rec.Areas[0].At != RigorAtBootstrap || rec.Areas[2].At != RigorAtRound {
		t.Errorf("an area's moment is not the one it was first applied at: %+v", rec.Areas)
	}

	// A first moment sets the block whatever it says, including Hotfix (off
	// the axis, and ranked below every on-axis mode) and the built-in
	// default.
	for _, first := range []struct {
		mode rigor.Mode
		from rigor.Layer
	}{{rigor.Hotfix, rigor.FromRepo}, {rigor.Default, rigor.FromDefault}, {rigor.POC, rigor.FromProject}} {
		got := (*RigorRecord)(nil).Applied(Moment{Resolved: rigor.Resolved{Mode: first.mode, From: first.from}, At: RigorAtBootstrap})
		if got == nil || got.Mode != first.mode || got.From != first.from || got.At != RigorAtBootstrap {
			t.Errorf("the first moment (%q from %q) did not set the block: %+v", first.mode, first.from, got)
		}
	}

	// A moment the record cannot use changes nothing: no resolvable mode, or
	// a moment that is not one of D22's three.
	before := rec.Clone()
	for _, bad := range []Moment{
		{Resolved: rigor.Resolved{Mode: ""}, At: RigorAtRound},
		{Resolved: rigor.Resolved{Mode: "paranoid", From: rigor.FromRepo}, At: RigorAtRound},
		{Resolved: rigor.Resolved{Mode: rigor.Critical, From: rigor.FromArea, Areas: []string{"payments"}}, At: "sometime"},
	} {
		if got := rec.Applied(bad); got.At != before.At || len(got.Areas) != len(before.Areas) || got.Mode != before.Mode {
			t.Errorf("Applied(%+v) changed the block: %+v", bad, got)
		}
	}
	if got := (*RigorRecord)(nil).Applied(Moment{At: RigorAtBootstrap}); got != nil {
		t.Errorf("a moment with no mode made a block out of nothing: %+v", got)
	}

	// The revision is the one the moment read, and it is kept: D25 has the
	// record say which revision of the configuration the mode came from.
	rev := (*RigorRecord)(nil).Applied(Moment{Resolved: rigor.Resolved{Mode: rigor.Pro, From: rigor.FromRepo}, At: RigorAtBootstrap, Revision: "origin/release"})
	if rev.Revision != "origin/release" {
		t.Errorf("Revision = %q; want the revision the moment read", rev.Revision)
	}
}

// TestRigorRecordBoundedRefusesABlockItCannotRead pins the reader's half: the
// record is an object the job's service account can write, so what comes back
// is not trusted to be the block Applied wrote. A block that cannot say which
// mode, which layer or which moment is not a decision anybody made, and nil is
// the same answer the field's absence gives; everything else is bounded, so a
// tampered record cannot put a megabyte of names in a listing.
func TestRigorRecordBoundedRefusesABlockItCannotRead(t *testing.T) {
	if got := (*RigorRecord)(nil).Bounded(); got != nil {
		t.Errorf("a nil block bounded to %+v; nil means 'no record'", got)
	}
	good := &RigorRecord{Mode: rigor.Critical, From: rigor.FromArea,
		Areas: []RigorArea{{Name: "payments", At: RigorAtBootstrap}}, At: RigorAtFinalize, Revision: "origin/main"}
	if got := good.Bounded(); got == nil || got.Mode != good.Mode || got.From != good.From || got.At != good.At || len(got.Areas) != 1 {
		t.Errorf("a block Applied wrote did not survive bounding: %+v", got)
	}
	for _, tc := range []struct {
		name string
		mut  func(r *RigorRecord)
	}{
		{"a mode outside the vocabulary", func(r *RigorRecord) { r.Mode = "paranoid" }},
		{"an empty mode", func(r *RigorRecord) { r.Mode = "" }},
		{"a layer outside the vocabulary", func(r *RigorRecord) { r.From = "floor" }},
		{"an empty layer", func(r *RigorRecord) { r.From = "" }},
		{"a moment that is not one of the three", func(r *RigorRecord) { r.At = "sometime" }},
		{"an empty moment", func(r *RigorRecord) { r.At = "" }},
	} {
		bad := good.Clone()
		tc.mut(bad)
		if got := bad.Bounded(); got != nil {
			t.Errorf("%s bounded to %+v; a reader cannot show a decision it cannot read", tc.name, got)
		}
	}

	// The bounds themselves: names capped in count and in length, an area
	// whose moment is not readable dropped whole, and the revision clipped.
	var many []RigorArea
	for i := 0; i < rigor.MaxAreas+10; i++ {
		many = append(many, RigorArea{Name: "area", At: RigorAtBootstrap})
	}
	big := good.Clone()
	big.Areas = many
	big.Revision = strings.Repeat("r", MaxRigorRevision+50)
	big.Areas[0].Name = strings.Repeat("n", rigor.MaxAreaName+50)
	big.Areas[1].At = "sometime"
	got := big.Bounded()
	if got == nil {
		t.Fatal("a block with too much in it bounded to nothing")
	}
	if len(got.Areas) != rigor.MaxAreas {
		t.Errorf("bounded to %d areas; want at most %d", len(got.Areas), rigor.MaxAreas)
	}
	if n := len([]rune(got.Areas[0].Name)); n != rigor.MaxAreaName {
		t.Errorf("a name bounded to %d runes; want %d", n, rigor.MaxAreaName)
	}
	if n := len([]rune(got.Revision)); n != MaxRigorRevision {
		t.Errorf("a revision bounded to %d runes; want %d", n, MaxRigorRevision)
	}
	for _, a := range got.Areas {
		if a.At != RigorAtBootstrap {
			t.Errorf("an area whose moment is unreadable survived bounding: %+v", a)
		}
	}
}

func areaNames(as []RigorArea) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.Name)
	}
	return out
}
