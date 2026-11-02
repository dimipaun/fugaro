// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package coordinator

import (
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/coordpath"
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
// Hand mutation this fails on: coordpath.DocumentID over the record as it stands, At
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
	if !strings.HasPrefix(path, coordpath.PathRepo(slug)+"/intents/") {
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

// TestReportRefusesADeclarationLongerThanARecord is P4's review follow-up, the
// size half of it: a declaration is a handful of names, and the caller writes
// every one of them, so the report path refuses a report that is a document.
// The refusal is checked on the record's own fields, after Normalise, which is
// why the list bound is asserted over a caller who passed the same name a
// hundred and one times rather than over a caller who passed a long list of
// distinct ones: dedup runs first, and a hundred and one copies of one path
// are one path, not a hundred and one.
//
// Hand mutation this fails on: dropping checkSize from Validate (all four
// reports are written, and the fake holds one document per declaration).
func TestReportRefusesADeclarationLongerThanARecord(t *testing.T) {
	r, f := testReporter(t)
	ctx := testCtx(t)
	slug := "acme-webapp"
	long := strings.Repeat("a", maxIntentName+1)
	many := make([]string, 0, maxIntentNames+1)
	for i := 0; i <= maxIntentNames; i++ {
		many = append(many, "internal/coordinator/p"+strconv.Itoa(i)+".go")
	}
	// One name over the per-name bound, one list over the per-list bound, and
	// names that are each legal but together say more than a record holds. The
	// long list's entries differ in their last digits on purpose: Normalise
	// deduplicates, so eighteen copies of one name would be one name.
	tooMuchText := make([]string, 0, maxIntentText/maxIntentName+2)
	for i := 0; i <= maxIntentText/maxIntentName+1; i++ {
		suffix := strconv.Itoa(i)
		tooMuchText = append(tooMuchText, strings.Repeat("b", maxIntentName-len(suffix))+suffix)
	}
	for _, c := range []struct {
		name string
		in   Intent
	}{
		{"a branch longer than one name", Intent{DeclaredBy: "someone@example.com", Branch: long}},
		{"more files than one record holds", Intent{DeclaredBy: "someone@example.com", Files: many}},
		{"more text than one record holds", Intent{DeclaredBy: "someone@example.com", Files: tooMuchText}},
		{"a caller whose own name is over the bound", Intent{DeclaredBy: long, Branch: "main"}},
	} {
		if _, path, err := r.Report(ctx, slug, c.in); !errors.Is(err, ErrIntentTooLarge) {
			t.Errorf("%s = %v, want ErrIntentTooLarge", c.name, err)
		} else if path != "" {
			t.Errorf("%s was refused and still returned the path %q", c.name, path)
		}
	}
	if v := f.Value(PathIntents(slug)); v != nil {
		t.Errorf("a refused report wrote %v", v)
	}
	// And a declaration that is at the bounds, not over them, is written: the
	// ceiling is a ceiling and not a target, and a report path that refused a
	// real branch name would be worse than the one this bound replaced.
	at := Intent{DeclaredBy: "someone@example.com", Branch: strings.Repeat("c", maxIntentName)}
	if _, _, err := r.Report(ctx, slug, at); err != nil {
		t.Errorf("a declaration exactly at the per-name bound = %v, want it written", err)
	}
}

// TestCleanList is cleanList's whole contract as a table, and it is a contract
// about a caller's own text rather than a tidy-up: what the record does to the
// file and service lists — drop the empty entries, collapse duplicates, sort —
// is what makes two spellings of one declaration one document, and what keeps
// a null out of a record whose empty list means "declares none".
//
// What is NOT in the table is as much the contract as what is: a blank entry, a
// space around a name and a control character are the caller's own text and are
// kept exactly as it wrote them. Trimming or refusing one would be the report
// path editing a declaration, which is advice in another costume and is the one
// thing the report path may not grow.
//
// Hand mutation this fails on: dropping the empty-entry filter (the record
// carries the empty name, and two callers who declared the same thing in
// different spellings stop sharing a document), sorting without Compact (a
// duplicate survives), trimming an entry (the two lists stop being the caller's
// own text), or sorting the caller's own slice in place (the row "the caller's
// own list is not the record's" below).
func TestCleanList(t *testing.T) {
	for _, c := range []struct {
		name string
		in   []string
		want []string
	}{
		{"a nil list answers an empty one", nil, []string{}},
		{"an empty list stays empty", []string{}, []string{}},
		{"the empty entry is dropped", []string{"", "internal/cli", ""}, []string{"internal/cli"}},
		{"a blank entry is the caller's own text and is kept", []string{"", " ", "b"}, []string{" ", "b"}},
		{"a duplicate collapses", []string{"b", "a", "b", "a"}, []string{"a", "b"}},
		{"surrounding whitespace is kept", []string{" b", "b "}, []string{" b", "b "}},
		{"a control character is kept", []string{"b", "\x01a", "a"}, []string{"\x01a", "a", "b"}},
		{"the order is the sorted one, whatever order the caller wrote", []string{"c", "a", "b"}, []string{"a", "b", "c"}},
		{"a path and its own prefix are two names in one list", []string{"internal/cli/run.go", "internal/cli"}, []string{"internal/cli", "internal/cli/run.go"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := cleanList(c.in)
			if !slices.Equal(got, c.want) {
				t.Errorf("cleanList(%q) = %q, want %q", c.in, got, c.want)
			}
			// The record's promise, and the reason the answer is a slice and
			// not nil: an empty list is "declares none", and a null is a shape
			// every reader of the record would have to interpret.
			if got == nil {
				t.Error("cleanList answered nil: the record would carry a null where it means \"declares none\"")
			}
		})
	}

	// The caller's own list is not the record's. Normalise assigns the answer
	// back onto its own copy of the Intent, so a cleanList that sorted in place
	// would reorder the list the caller still holds — and the caller's Intent
	// is its own document until it is reported.
	own := []string{"c", "", "a"}
	cleanList(own)
	if want := []string{"c", "", "a"}; !slices.Equal(own, want) {
		t.Errorf("cleanList reordered the caller's own list: %q, want %q", own, want)
	}
}

// TestIntentIDIsANodeKey: the record's ID is the node's key, and the property
// the whole record's shape rests on is that it is always a key the database
// takes — a branch, a path or a service holding a dot, a slash, a bracket or a
// control character cannot reach the key, because coordpath.DocumentID answers
// lower-case hex (intent.go's own argument, and the reason a caller who reaches
// the report path cannot write outside the intents node).
//
// The declaration below is hostile on purpose: every part of it carries a
// character a database key may not hold. The ID that comes out holds none of
// them, and the store takes the path it names without a refusal — which is the
// check, asserted on the ID rather than inferred from a successful report.
func TestIntentIDIsANodeKey(t *testing.T) {
	hostile := Intent{
		DeclaredBy: "someone@example.com",
		Branch:     "release/v0.16.0/../..",
		Release:    "0.16.0\x00[",
		Files:      []string{"../internal/cli/run.go", "a\nb.go"},
		Services:   []string{"internal/coordinator", "x.y"},
	}
	id := IntentID(hostile)
	if strings.ContainsAny(id, "./$#[]") || strings.ContainsFunc(id, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		t.Errorf("IntentID(%+v) = %q, which is not a database key", hostile, id)
	}
	s := testStore(t)
	if _, _, err := s.Read(testCtx(t), PathIntent("acme-webapp", id), nil); err != nil {
		t.Errorf("the store refuses the path the ID names, so the record could not be written: %v", err)
	}
}

// TestStoreRefusesAnIntentIDThatIsNotANodeKey: the other half of the same
// check. IntentID never produces one of these — it is hex — so the store's own
// fence is what stands between an ID that is not a key and the database, and it
// has to hold for every caller that builds a path out of an ID (PathIntent) and
// not only for this package's own.
//
// "Refused before any store read or write" is the assertion rather than a
// nicety: the fake's credentials are empty afterwards, so the refusal happened
// in this process and no request was made, and the intents node is untouched.
//
// Hand mutation this fails on: dropping validPath from internal/rtdb's get and
// put (every path here reaches the database, and the fake records a credential
// for each one).
func TestStoreRefusesAnIntentIDThatIsNotANodeKey(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	c, err := rtdb.New(f.URL, rtdb.Auth{IDToken: func() string { return "idtok" }})
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore(c)
	ctx := testCtx(t)
	slug := "acme-webapp"
	// Every ID here is one the report path can never mint, and every one of
	// them is refused for the reason named: the segment it would make is not a
	// key. The bound is internal/rtdb's own (a key of more than 768
	// characters), written here as the literal the database measures.
	for _, c := range []struct{ name, id string }{
		{"a traversal", ".."},
		{"a traversal through the intents node", "../view"},
		{"a dot anywhere in the name", "dead.beeF"},
		{"a bracket", "abcdef[]"},
		{"an empty segment between two separators", "abcdef//012345"},
		{"a control character", "abcdef\x00"},
		{"a name over the database's own key bound", strings.Repeat("c", 769)},
	} {
		path := PathIntent(slug, c.id)
		if _, _, err := s.Read(ctx, path, nil); err == nil {
			t.Errorf("%s: Read(%s) = nil, want a refusal", c.name, path)
		}
		if err := s.Put(ctx, path, "etag", map[string]any{"x": 1}); err == nil {
			t.Errorf("%s: Put(%s) = nil, want a refusal", c.name, path)
		}
		if err := s.Write(ctx, path, map[string]any{"x": 1}); err == nil {
			t.Errorf("%s: Write(%s) = nil, want a refusal", c.name, path)
		}
	}
	if got := f.Credentials(); len(got) != 0 {
		t.Errorf("a refused ID reached the database: %d requests carried a credential", len(got))
	}
	if v := f.Value(PathIntents(slug)); v != nil {
		t.Errorf("a refused ID wrote %v", v)
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
