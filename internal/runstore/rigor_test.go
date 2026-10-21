package runstore

import (
	"encoding/json"
	"reflect"
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
		Areas: []RigorArea{
			{Name: "payments", Mode: rigor.Critical, At: RigorAtBootstrap},
			{Name: "docs", Mode: rigor.POC, At: RigorAtRound},
		},
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
		`"areas":[{"name":"payments","mode":"critical","at":"bootstrap"},` +
		`{"name":"docs","mode":"poc","at":"round"}],` +
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
//
// The areas are pinned whole — name, mode and moment — because the order is
// D23's strictest-first and the fold is what can break it: the round moment's
// rule is *stricter* than one the bootstrap moment already recorded, so it
// belongs in front of it even though it arrived later. A reader takes the
// first entry as the rule that decided (fugaro runs ls's MODE column), so
// this is the assertion that keeps that reading honest.
func TestRigorRecordFoldNeverLowers(t *testing.T) {
	boot := Moment{Resolved: rigor.Resolved{Mode: rigor.Critical, From: rigor.FromArea, Areas: []rigor.Area{
		{Name: "payments", Match: "internal/payments/**", Mode: rigor.Critical},
		{Name: "docs", Match: "docs/**", Mode: rigor.POC},
	}}, At: RigorAtBootstrap, Revision: "origin/main"}
	round := Moment{Resolved: rigor.Resolved{Mode: rigor.Critical, From: rigor.FromArea, Areas: []rigor.Area{
		{Name: "web", Match: "web/**", Mode: rigor.Critical},
	}}, At: RigorAtRound, Revision: "origin/main"}
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
	if rec.At != RigorAtRound {
		t.Errorf("the moment that added an area did not become the block's moment: %q", rec.At)
	}
	// Every area the run matched, strictest first (D23), each with the moment
	// it was FIRST applied at — a rule re-matched later does not read as one
	// the run met only at the end.
	wantAreas := []RigorArea{
		{Name: "payments", Mode: rigor.Critical, At: RigorAtBootstrap},
		{Name: "web", Mode: rigor.Critical, At: RigorAtRound},
		{Name: "docs", Mode: rigor.POC, At: RigorAtBootstrap},
	}
	if !reflect.DeepEqual(rec.Areas, wantAreas) {
		t.Errorf("areas after three moments = %+v; want %+v", rec.Areas, wantAreas)
	}

	// The same name twice: a run's configuration is fixed, so the two layers'
	// rule of one name is one rule, and the stricter reading of it wins —
	// in place, keeping the moment it was first applied at.
	same := (*RigorRecord)(nil).Applied(Moment{Resolved: rigor.Resolved{Mode: rigor.POC, From: rigor.FromArea, Areas: []rigor.Area{
		{Name: "pay", Match: "p/**", Mode: rigor.POC},
	}}, At: RigorAtBootstrap})
	same = same.Applied(Moment{Resolved: rigor.Resolved{Mode: rigor.Critical, From: rigor.FromArea, Areas: []rigor.Area{
		{Name: "pay", Match: "p/**", Mode: rigor.Critical},
	}}, At: RigorAtRound})
	if len(same.Areas) != 1 || same.Areas[0].Mode != rigor.Critical || same.Areas[0].At != RigorAtBootstrap {
		t.Errorf("one name, two rules = %+v; want the stricter mode, first applied at", same.Areas)
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
		{Resolved: rigor.Resolved{Mode: rigor.Critical, From: rigor.FromArea, Areas: []rigor.Area{
			{Name: "payments", Match: "p/**", Mode: rigor.Critical}}}, At: "sometime"},
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
		Areas: []RigorArea{{Name: "payments", Mode: rigor.Critical, At: RigorAtBootstrap}}, At: RigorAtFinalize, Revision: "origin/main"}
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
		{"the layer only a resolution names", func(r *RigorRecord) { r.From = rigor.FromReached }},
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

	// An area the reader cannot read is dropped whole: a rule with no mode is
	// no more a decision than a block with no mode is. Without this, a
	// tampered record could put an unrankable rule at the front of the list a
	// reader takes its answer from.
	noMode := good.Clone()
	noMode.Areas = append(noMode.Areas, RigorArea{Name: "web", At: RigorAtBootstrap})
	if got := noMode.Bounded(); len(got.Areas) != 1 {
		t.Errorf("an area with no mode survived bounding: %+v", got.Areas)
	}

	// The bounds themselves: names capped in count and in length, an area
	// whose mode or moment is not readable dropped whole, the rules sorted
	// strictest first, and the revision clipped. The strict rules are written
	// LAST, so a bound that capped before it sorted would keep the weak ones
	// and throw the strict ones away — the one thing a bound must never do
	// with a run's mode.
	var many []RigorArea
	for i := 0; i < rigor.MaxAreas+10; i++ {
		many = append(many, RigorArea{Name: "area", Mode: rigor.POC, At: RigorAtBootstrap})
	}
	many[len(many)-1] = RigorArea{Name: "payments", Mode: rigor.Critical, At: RigorAtBootstrap}
	big := good.Clone()
	big.Areas = many
	big.Revision = strings.Repeat("r", MaxRigorRevision+50)
	big.Areas[0].Name = strings.Repeat("n", rigor.MaxAreaName+50)
	big.Areas[1].At = "sometime"
	big.Areas[1].Mode = "paranoid"
	got := big.Bounded()
	if got == nil {
		t.Fatal("a block with too much in it bounded to nothing")
	}
	if len(got.Areas) != rigor.MaxAreas {
		t.Errorf("bounded to %d areas; want at most %d", len(got.Areas), rigor.MaxAreas)
	}
	if got.Areas[0].Name != "payments" || got.Areas[0].Mode != rigor.Critical {
		t.Errorf("the cap threw the strictest rule away: %+v first", got.Areas[0])
	}
	if n := len([]rune(got.Areas[1].Name)); n != rigor.MaxAreaName {
		t.Errorf("a name bounded to %d runes; want %d", n, rigor.MaxAreaName)
	}
	if n := len([]rune(got.Revision)); n != MaxRigorRevision {
		t.Errorf("a revision bounded to %d runes; want %d", n, MaxRigorRevision)
	}
	for i, a := range got.Areas {
		if i > 0 && a.Mode.Rank() > got.Areas[i-1].Mode.Rank() {
			t.Errorf("a bounded block is not strictest first: %+v", got.Areas)
			break
		}
		if !rigor.Known(a.Mode) || a.At != RigorAtBootstrap {
			t.Errorf("an area the reader cannot read survived bounding: %+v", a)
		}
	}
}
