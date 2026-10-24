package coordinator

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The report path, as P4 builds it: one intent record per declaration, its ID
// derived from the caller and the declaration, and a writer whose whole
// dependency is a write. Nothing here contacts Firebase: the database is
// internal/gcpfake's, the same one P3's state tests use.

// testReporter is a reporter over a fake database, and the fake itself, so a
// test can read what was written without going through the store.
func testReporter(t *testing.T) (*Reporter, *gcpfake.RTDB) {
	t.Helper()
	f := gcpfake.NewRTDB(t)
	c, err := rtdb.New(f.URL, rtdb.Auth{IDToken: func() string { return "idtok" }})
	if err != nil {
		t.Fatal(err)
	}
	return &Reporter{Store: NewStore(c)}, f
}

// testIntent is the declaration the tests below report: one caller, one
// branch that names a release, and the two paths and one service it is about
// to touch.
func testIntent() Intent {
	return Intent{
		DeclaredBy: "someone@example.com",
		Branch:     "release/v0.11.0",
		Files:      []string{"internal/cli/run.go", "internal/coordinator/intent.go"},
		Services:   []string{"internal/coordinator"},
	}
}

// TestReportRefusesACallerTheProjectCannotName is the part's first red step: a
// report nobody can be named for is refused, the refusal says what is missing,
// and nothing is written — a refusal that left a document behind would be a
// record the coordinator cannot attribute to anybody.
//
// Hand mutation this fails on: dropping the caller check from Validate (the
// report is written and the document exists).
func TestReportRefusesACallerTheProjectCannotName(t *testing.T) {
	r, f := testReporter(t)
	_, path, err := r.Report(testCtx(t), "acme-webapp", Intent{Branch: "fugaro/20261024-182241-f12e"})
	if !errors.Is(err, ErrUnnamedCaller) {
		t.Fatalf("a report from an unnamed caller = %v, want ErrUnnamedCaller", err)
	}
	for _, want := range []string{"who declares it", "user:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not say what is missing (%q)", err, want)
		}
	}
	if path != "" {
		t.Errorf("the refused report returned the path %q", path)
	}
	if v := f.Value(PathIntents("acme-webapp")); v != nil {
		t.Errorf("the refused report wrote %v", v)
	}
}

// TestReportRefusesADeclarationThatDeclaresNothing: the other half of the same
// refusal. A caller who names no branch, no release, no file and no service
// has said nothing the coordinator can record, and a document that says
// nothing is worse than no document — it is a record the view would have to
// read and then discard.
func TestReportRefusesADeclarationThatDeclaresNothing(t *testing.T) {
	r, f := testReporter(t)
	_, _, err := r.Report(testCtx(t), "acme-webapp", Intent{DeclaredBy: "someone@example.com"})
	if !errors.Is(err, ErrNothingDeclared) {
		t.Fatalf("an empty declaration = %v, want ErrNothingDeclared", err)
	}
	if v := f.Value(PathIntents("acme-webapp")); v != nil {
		t.Errorf("the refused report wrote %v", v)
	}
}

// TestReportWritesOneDocumentPerDeclaration is the part's second red step, and
// it is an idempotency test rather than a round-trip: the same declaration
// from the same caller, reported twice, is ONE document (Q6 A, finding #17).
// The two reports carry different clocks on purpose — that is the mutation the
// test is written for: an ID with the wall clock in it writes two documents
// and fails here — and the second report spells its paths in another order,
// because the order a caller lists them in is not part of what it declares.
//
// Hand mutation this fails on: DocumentID over the record as it stands, At
// included.
func TestReportWritesOneDocumentPerDeclaration(t *testing.T) {
	r, f := testReporter(t)
	ctx := testCtx(t)
	slug := "acme-webapp"
	at := time.Date(2026, 10, 24, 18, 22, 41, 0, time.UTC)
	r.Now = func() time.Time { return at }

	first, path, err := r.Report(ctx, slug, testIntent())
	if err != nil {
		t.Fatal(err)
	}
	// The same declaration, listed the other way round, half an hour later.
	r.Now = func() time.Time { return at.Add(30 * time.Minute) }
	in := testIntent()
	slices.Reverse(in.Files)
	second, again, err := r.Report(ctx, slug, in)
	if err != nil {
		t.Fatal(err)
	}
	if path != again {
		t.Errorf("one declaration wrote two documents: %q and %q", path, again)
	}
	if first.At.Equal(second.At) {
		t.Error("the second report did not stamp its own clock: the two are the same document by accident")
	}
	if got := children(f, PathIntents(slug)); len(got) != 1 {
		t.Fatalf("the intent node holds %d documents, want 1: %v", len(got), got)
	}

	// A different declaration from the same caller is a second document: the
	// ID is a function of the declaration and not of the caller alone.
	other := testIntent()
	other.Files = []string{"internal/cli/coordinator.go"}
	if _, path2, err := r.Report(ctx, slug, other); err != nil || path2 == path {
		t.Fatalf("a second declaration = %q, %v", path2, err)
	}
	// ... and a different caller declaring the same thing is a third.
	byOther := testIntent()
	byOther.DeclaredBy = "other@example.com"
	if _, path3, err := r.Report(ctx, slug, byOther); err != nil || path3 == path {
		t.Fatalf("the same declaration by another caller = %q, %v", path3, err)
	}
	if got := children(f, PathIntents(slug)); len(got) != 3 {
		t.Fatalf("the intent node holds %d documents, want 3: %v", len(got), got)
	}

	// The record is the one that was written, and it sits where D195 puts it.
	var back Intent
	if found, err := testClient(t, f).Get(ctx, path, &back); err != nil || !found {
		t.Fatalf("reading the document back: found=%v err=%v", found, err)
	}
	if back.DeclaredBy != first.DeclaredBy || back.Release != "0.11.0" || !back.At.Equal(second.At) {
		t.Errorf("the stored record = %+v, want the declaration with its own clock", back)
	}
	if !strings.HasPrefix(path, PathRepo(slug)+"/intents/") {
		t.Errorf("the record was written to %q, which is not under the repository's intent node", path)
	}
}

// TestIntentIDIgnoresTheClock is the same rule at the function: the ID is a
// function of the caller and the declaration, and the clock is not one of
// them. It is pinned separately from the write above because it is the claim
// the plan makes about the record's shape, and a reader of IntentID should
// find it asserted here rather than inferred from a database.
func TestIntentIDIgnoresTheClock(t *testing.T) {
	a, b := testIntent(), testIntent()
	a.At = time.Date(2026, 10, 24, 18, 22, 41, 0, time.UTC)
	b.At = a.At.Add(24 * time.Hour)
	if IntentID(a) != IntentID(b) {
		t.Errorf("IntentID moved with the clock: %q and %q", IntentID(a), IntentID(b))
	}
	if IntentID(a) == IntentID(Intent{DeclaredBy: a.DeclaredBy, Branch: a.Branch}) {
		t.Error("IntentID ignores the declaration: two different ones share an ID")
	}
	// A file and a service of one name are two different declarations.
	x := Intent{DeclaredBy: "someone@example.com", Files: []string{"internal/cli"}}
	y := Intent{DeclaredBy: "someone@example.com", Services: []string{"internal/cli"}}
	if IntentID(x) == IntentID(y) {
		t.Error("IntentID does not tell a file from a service of the same name")
	}
}

// TestReportReadsNothingBack is the part's third red step, asserted against
// the writer's own dependency list rather than against a behaviour: the one
// thing the report path is handed is a write, and there is no read in the
// interface it holds. That is what makes "it takes nothing back — the command
// prints what it wrote and issues no advice" a property of the code and not a
// promise: advice needs a reader, and the writer has none.
//
// Hand mutation this fails on: adding a Read to Writer, or a reader field to
// Reporter, so that a later report could compare the declaration against what
// is in flight.
func TestReportReadsNothingBack(t *testing.T) {
	w := reflect.TypeOf((*Writer)(nil)).Elem()
	if n := w.NumMethod(); n != 1 || w.Method(0).Name != "Write" {
		names := make([]string, w.NumMethod())
		for i := range w.NumMethod() {
			names[i] = w.Method(i).Name
		}
		t.Fatalf("the report path's writer holds %v, want Write alone: the record is written and nothing is read back", names)
	}
	rt := reflect.TypeOf(Reporter{})
	var fields []string
	for i := range rt.NumField() {
		fields = append(fields, rt.Field(i).Name)
	}
	if !slices.Equal(fields, []string{"Store", "Now"}) {
		t.Errorf("Reporter's dependencies = %v, want the store and the clock alone", fields)
	}
	// The store P3 built is what the writer is handed: the report path writes
	// through the same ETag CAS every other write of the coordinator's state
	// goes through, and mints no credential of its own.
	if !reflect.TypeOf(&Store{}).Implements(w) {
		t.Error("Store is not a Writer: the report path does not write through the coordinator's own store")
	}
}

// children is the keys of a node of the fake database, sorted; nil when the
// node is not there.
func children(f *gcpfake.RTDB, path string) []string {
	m, ok := f.Value(path).(map[string]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// testClient is a client of the fake database, for the one test that reads a
// document back through the API rather than through the fake's own map.
func testClient(t *testing.T, f *gcpfake.RTDB) *rtdb.Client {
	t.Helper()
	c, err := rtdb.New(f.URL, rtdb.Auth{IDToken: func() string { return "idtok" }})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
