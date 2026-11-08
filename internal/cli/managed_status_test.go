package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
)

// managed_status.go's tests (CM2 — the desktop console's managed screens, F10
// to F13, D1472 to D1477).
//
// What these hold. The document is the console's whole reading of a managed
// project, so what it must NEVER do is wider than what it must do: it is a
// closed shape (a member this build does not write is not in it, and one a
// later build adds arrives under a new name), it prints a bounded subset of the
// newest audit record and never its argv (D1476), it never prints a "revoked"
// (D1475), and a read that did not answer is a state of its own rather than an
// empty day — "nothing was recorded" and "the records could not be read" are
// the two answers an operator acts differently on, and only the second is a
// reason to say so. Each of those is invisible from the code and gets a test.
//
// Every world here is hermetic and built by the managed-audit rig one file
// over: internal/gcpfake's GCS, Resource Manager, Billing and Secret Manager,
// t.TempDir() for the config, and a pinned clock — a record is filed under its
// own UTC date, so a test that let the wall clock decide would flake at
// midnight.

// newManagedStatusRig is that rig with this read's clock pinned to the same
// day: its records are filed under managedAuditTestNow, so the read that
// answers them has to be asked about that day and not about today's.
func newManagedStatusRig(t *testing.T, o managedAuditOpts) *managedAuditRig {
	t.Helper()
	r := newManagedAuditRig(t, o)
	old := managedStatusNow
	managedStatusNow = func() time.Time { return managedAuditTestNow }
	t.Cleanup(func() { managedStatusNow = old })
	return r
}

// managedStatusOf runs the argv the console spawns — `managed status --json
// --project <name>`, the task's own interface row — and decodes the document.
func managedStatusOf(t *testing.T, r *managedAuditRig, extra ...string) (managedStatusDoc, string) {
	t.Helper()
	args := append([]string{"managed", "status", "--json", "--project", "aurora"}, extra...)
	out, errOut, err := execute(t, args...)
	if err != nil {
		t.Fatalf("fugaro managed status --json: %v (stderr %q, out %q)", err, errOut, out)
	}
	return decodeManagedStatus(t, out), out
}

func decodeManagedStatus(t *testing.T, out string) managedStatusDoc {
	t.Helper()
	var doc managedStatusDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("the document is not JSON: %v (%q)", err, out)
	}
	return doc
}

// fileWithArgv writes one admitted record whose argv is set — the field the
// document is bounded to keep out — through the same writer the commands call,
// so what the read is asked about is an object this program produced and not
// one a test hand-wrote.
func (r *managedAuditRig) fileWithArgv(t *testing.T, argv []string) managedmode.AuditRecord {
	t.Helper()
	conv := managedmode.ConvergeAdmitted([]byte(`{"version":1,"keys":{"rigor":"standard"}}`), initflow.YesOrTyped, []string{"every key is in the registry"})
	rec := managedmode.AuditRecordFor(managedmode.AuditFacts{
		Command: "fugaro update", Argv: argv, Project: "aurora", GCPProject: "proj-1234",
		Identity: "user:owner@example.com", MarkerVersion: 1, At: managedAuditTestNow.Add(-time.Hour),
	}, conv)
	if _, err := managedmode.WriteAudit(context.Background(), managedStorage(t, r.gcs), dbBucket, rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

// TestManagedStatusPrintsTheMarks: the four facts the marks' read answers
// (D1474) — whether this project is managed, the sentence saying why not when
// it is not, the creation fact's version and the object's own generation — are
// what the document is built on, and the aiming pair is the one the console
// spawns.
func TestManagedStatusPrintsTheMarks(t *testing.T) {
	r := newManagedStatusRig(t, managedAuditOpts{})
	doc, out := managedStatusOf(t, r)
	if !doc.Managed {
		t.Fatalf("managed = false for a project carrying both marks: %s", out)
	}
	if doc.Project != "aurora" || doc.GCPProject != "proj-1234" {
		t.Errorf("project/gcp_project = %q/%q, want aurora/proj-1234: a screen draws the installation it asked about", doc.Project, doc.GCPProject)
	}
	if doc.Version != 1 {
		t.Errorf("version = %d, want the creation fact's own version (the rig's marker declares 1)", doc.Version)
	}
	if n, err := strconv.ParseInt(doc.Generation, 10, 64); err != nil || n <= 0 {
		t.Errorf("generation = %q, want the creation fact object's own generation in decimal: it is what makes the same project, later, checkable (D1474)", doc.Generation)
	}
	if doc.Reason != "" {
		t.Errorf("reason = %q, want empty for a project that IS managed: the reason is the read's sentence for one that is not", doc.Reason)
	}
	if doc.Audit != nil || doc.AuditCount != 0 || doc.AuditDay != "" || doc.AuditState != "" {
		t.Errorf("audit = %+v, day %q, count %d, state %q on a project that never ran a managed apply, want no record, no day, 0 and no state (a day nothing was filed under is no records and no error)",
			doc.Audit, doc.AuditDay, doc.AuditCount, doc.AuditState)
	}
	// --config is the operator's spelling of the same installation, and it has
	// to answer the same document: the console sends --project, a person sends
	// either.
	t.Run("--config selects the same installation", func(t *testing.T) {
		out, _, err := execute(t, "managed", "status", "--json", "--config", r.cfgPath)
		if err != nil {
			t.Fatalf("fugaro managed status --json --config: %v", err)
		}
		via := decodeManagedStatus(t, out)
		if via.Project != doc.Project || via.GCPProject != doc.GCPProject || !via.Managed {
			t.Errorf("--config answered %q/%q (managed %v), want the installation --project answered (%q/%q, managed %v)",
				via.Project, via.GCPProject, via.Managed, doc.Project, doc.GCPProject, doc.Managed)
		}
	})
}

// TestManagedStatusPrintsTheBoundedRecordSubset: the trail is the NEWEST
// record of the day — the five fields that answer "who admitted what" — with
// the day's count beside them, and never the older ones. A screen shows one
// last apply, so a document that carried the others would have a member the
// console has no room for and no bound on.
func TestManagedStatusPrintsTheBoundedRecordSubset(t *testing.T) {
	r := newManagedStatusRig(t, managedAuditOpts{})
	older := r.file(t, "fugaro config publish", `{"version":1,"keys":{"rigor":"standard"}}`, managedmode.AuditRefused, managedAuditTestNow.Add(-3*time.Hour))
	newest := r.file(t, "fugaro update", `{"version":2,"add":["image"]}`, managedmode.AuditAdmitted, managedAuditTestNow.Add(-90*time.Minute))
	doc, out := managedStatusOf(t, r)
	if doc.Audit == nil {
		t.Fatalf("audit = null with two records filed today: %s", out)
	}
	if got, want := doc.Audit.At, newest.At.UTC().Format(time.RFC3339); got != want {
		t.Errorf("audit.at = %q, want the record's own time (%q)", got, want)
	}
	if doc.Audit.Command != newest.Command {
		t.Errorf("audit.command = %q, want %q", doc.Audit.Command, newest.Command)
	}
	if doc.Audit.Verdict != string(newest.Verdict) {
		t.Errorf("audit.verdict = %q, want the gate's own verdict (%q)", doc.Audit.Verdict, newest.Verdict)
	}
	if doc.Audit.Identity != newest.Identity {
		t.Errorf("audit.identity = %q, want the account the write ran as (%q)", doc.Audit.Identity, newest.Identity)
	}
	if doc.Audit.PlanDigest != newest.PlanDigest {
		t.Errorf("audit.plan_digest = %q, want the digest of the plan the gate judged (%q)", doc.Audit.PlanDigest, newest.PlanDigest)
	}
	if strings.Contains(out, older.Command) || strings.Contains(out, older.PlanDigest) {
		t.Errorf("the document names the older record (%q, digest %q), want the newest alone: the subset is one record", older.Command, older.PlanDigest)
	}
	if doc.AuditDay != managedmode.AuditDate(managedAuditTestNow) {
		t.Errorf("audit_day = %q, want the day the count is about (%q)", doc.AuditDay, managedmode.AuditDate(managedAuditTestNow))
	}
	if doc.AuditCount != 2 {
		t.Errorf("audit_count = %d, want the records filed under audit_day (2)", doc.AuditCount)
	}
	if doc.AuditState != "" {
		t.Errorf("audit_state = %q, want empty for a read that answered: the state is for one that did not", doc.AuditState)
	}
}

// TestManagedStatusNeverPrintsArgv: the subset is bounded to keep argv out
// (D1476). It is the argv the write ran as — a path, a root name or a title the
// operator did not mean to publish — on a surface anyone at the machine can
// read, so the check is on the bytes of both renderings and on the member set
// itself, and it is the mutation the task's plan names.
func TestManagedStatusNeverPrintsArgv(t *testing.T) {
	r := newManagedStatusRig(t, managedAuditOpts{})
	argv := []string{"/usr/local/bin/fugaro", "--task-file", "/work/notes/private-plan.md"}
	rec := r.fileWithArgv(t, argv)
	doc, out := managedStatusOf(t, r)
	// The record filed with an argv is the one the document shows, so a
	// document that printed nothing at all fails here rather than passing.
	if doc.Audit == nil || doc.Audit.Command != rec.Command {
		t.Fatalf("audit = %+v, want the record filed with an argv (%q): a pass here has to mean the record was shown and its argv was not", doc.Audit, rec.Command)
	}
	var raw struct {
		Audit map[string]json.RawMessage `json:"audit"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("json: %v", err)
	}
	keys := make([]string, 0, len(raw.Audit))
	for k := range raw.Audit {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if want := []string{"at", "command", "identity", "plan_digest", "verdict"}; !slices.Equal(keys, want) {
		t.Errorf("the audit member carries %v, want exactly %v: the subset is bounded, and argv is the field it is bounded to keep out (D1476)", keys, want)
	}
	for _, a := range argv {
		if strings.Contains(out, a) {
			t.Errorf("the document carries %q, which is part of the record's argv: argv holds paths and titles an operator did not mean to publish (D1476)", a)
		}
	}
	text, _, err := execute(t, "managed", "status", "--project", "aurora")
	if err != nil {
		t.Fatalf("fugaro managed status: %v", err)
	}
	for _, a := range argv {
		if strings.Contains(text, a) {
			t.Errorf("the text output carries %q, which is part of the record's argv: the bound is about the disclosure and not about one rendering of it", a)
		}
	}
}

// TestManagedStatusOnAnUnmanagedProjectSaysWhy: a project missing either mark
// is not managed, and the document says which read said so and why (D1474)
// rather than printing an empty fact — the answer a screen owes an operator who
// expected a managed project. It never says "revoked" (D1475): that is a
// run-time fact of a write gate, it is not persisted anywhere, and a project
// whose label was taken off is "not managed" and not a third state.
func TestManagedStatusOnAnUnmanagedProjectSaysWhy(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts managedAuditOpts
	}{
		{"the project carries no managed label", managedAuditOpts{noProjectLabel: true}},
		{"the creation fact is absent", managedAuditOpts{noMarker: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newManagedStatusRig(t, tc.opts)
			// A record is filed anyway and is not shown: the marks decide
			// whether this project's trail is read at all, and an object
			// somebody left in the bucket is not a fact about the project.
			r.file(t, "fugaro update", `{"version":2,"add":["image"]}`, managedmode.AuditAdmitted, managedAuditTestNow.Add(-time.Hour))
			doc, out := managedStatusOf(t, r)
			if doc.Managed {
				t.Fatalf("managed = true for a project missing one of its two marks: %s", out)
			}
			if doc.Reason == "" {
				t.Errorf("reason = \"\" for a project that is not managed: the read already computed the sentence, and a screen that could not print it would be showing a label rather than a reading (D1474)")
			}
			if doc.Version != 0 || doc.Generation != "" {
				t.Errorf("version/generation = %d/%q, want 0 and \"\": the read believed no object, and a figure invented for a fact nothing read is what the empty generation exists to avoid", doc.Version, doc.Generation)
			}
			if doc.Audit != nil || doc.AuditDay != "" || doc.AuditCount != 0 || doc.AuditState != "" {
				t.Errorf("audit = %+v, day %q, count %d, state %q: a project that is not managed files no record (D1025), so its trail is not read and there is nothing to report about it",
					doc.Audit, doc.AuditDay, doc.AuditCount, doc.AuditState)
			}
			if strings.Contains(strings.ToLower(out), "revoked") {
				t.Errorf("the document says \"revoked\" (D1475): the marks' own answer is %q, and a state the CLI does not have must not be drawn as though it did: %s", "not managed", out)
			}
		})
	}
}

// TestManagedStatusReadsTwoDaysAndSaysWhichTwo: the trail is the newest record
// of the current UTC day, and of the previous day only when today holds nothing
// (D1477) — so a session that ran an apply twenty minutes before midnight is
// still the last one an operator reads about, and the window is two days rather
// than the whole family.
func TestManagedStatusReadsTwoDaysAndSaysWhichTwo(t *testing.T) {
	t.Run("today's records answer, and the day names today", func(t *testing.T) {
		r := newManagedStatusRig(t, managedAuditOpts{})
		r.file(t, "fugaro update", `{"version":2,"add":["image"]}`, managedmode.AuditAdmitted, managedAuditTestNow.Add(-2*time.Hour))
		doc, _ := managedStatusOf(t, r)
		if doc.AuditDay != managedmode.AuditDate(managedAuditTestNow) {
			t.Errorf("audit_day = %q, want today (%q)", doc.AuditDay, managedmode.AuditDate(managedAuditTestNow))
		}
		if doc.Audit == nil || doc.AuditCount != 1 {
			t.Errorf("audit = %+v with %d records, want the day's one", doc.Audit, doc.AuditCount)
		}
	})
	t.Run("an empty today answers from yesterday, and the day names yesterday", func(t *testing.T) {
		r := newManagedStatusRig(t, managedAuditOpts{})
		yesterday := managedAuditTestNow.AddDate(0, 0, -1)
		rec := r.file(t, "fugaro update", `{"version":2,"add":["image"]}`, managedmode.AuditAdmitted, yesterday.Add(time.Hour))
		doc, _ := managedStatusOf(t, r)
		if doc.AuditDay != managedmode.AuditDate(yesterday) {
			t.Errorf("audit_day = %q, want the day that answered (%q): a record filed yesterday is still the last apply, and the day member is what says which day the count is about",
				doc.AuditDay, managedmode.AuditDate(yesterday))
		}
		if doc.Audit == nil || doc.Audit.Command != rec.Command {
			t.Errorf("audit = %+v, want the record filed yesterday (%q)", doc.Audit, rec.Command)
		}
		if doc.AuditCount != 1 {
			t.Errorf("audit_count = %d, want yesterday's one record", doc.AuditCount)
		}
	})
	t.Run("a record older than the two days is outside the window", func(t *testing.T) {
		r := newManagedStatusRig(t, managedAuditOpts{})
		r.file(t, "fugaro update", `{"version":2,"add":["image"]}`, managedmode.AuditAdmitted, managedAuditTestNow.AddDate(0, 0, -2))
		doc, out := managedStatusOf(t, r)
		if doc.Audit != nil || doc.AuditCount != 0 || doc.AuditDay != "" {
			t.Errorf("audit = %+v, day %q, count %d, want none: the read is two days, and a document that showed a record older than its own window would be a window nothing bounds it to: %s",
				doc.Audit, doc.AuditDay, doc.AuditCount, out)
		}
		if doc.AuditState != "" {
			t.Errorf("audit_state = %q, want empty: the read answered, and it answered that the window holds nothing", doc.AuditState)
		}
	})
}

// TestManagedStatusUnreadIsNotAnEmptyDay: a trail that could not be listed is
// an unknown, and an unknown printed as an empty day is the silent pass — the
// one shape that reads exactly like the clean state. The two are told apart by
// audit_state, so the control is in the same test: the same project, the same
// clock, a readable bucket.
func TestManagedStatusUnreadIsNotAnEmptyDay(t *testing.T) {
	r := newManagedStatusRig(t, managedAuditOpts{})
	r.gcs.FailListings(dbBucket)
	doc, out := managedStatusOf(t, r)
	if doc.AuditState == "" {
		t.Fatalf("audit_state = \"\" for a read that did not answer: %s", out)
	}
	if !strings.Contains(doc.AuditState, "could not") {
		t.Errorf("audit_state = %q, want the sentence saying the records could not be read", doc.AuditState)
	}
	if !doc.Managed {
		t.Errorf("managed = false when the marks' read answered and only the trail's did not: the two are separate reads, and conflating them would report a fact about the project from a failure about its records")
	}
	if doc.Audit != nil || doc.AuditDay != "" || doc.AuditCount != 0 {
		t.Errorf("audit = %+v, day %q, count %d beside a state: an unread day carries no figure, because a figure would be read as an answer", doc.Audit, doc.AuditDay, doc.AuditCount)
	}
	t.Run("the control: an empty day is not a state", func(t *testing.T) {
		empty := newManagedStatusRig(t, managedAuditOpts{})
		edoc, eout := managedStatusOf(t, empty)
		if edoc.AuditState != "" {
			t.Errorf("audit_state = %q for a bucket that answers and holds nothing: an empty day is the ordinary case, and a state here would put a warning on every clean project: %s", edoc.AuditState, eout)
		}
		if edoc.Audit != nil || edoc.AuditDay != "" || edoc.AuditCount != 0 {
			t.Errorf("audit = %+v, day %q, count %d, want none of the three", edoc.Audit, edoc.AuditDay, edoc.AuditCount)
		}
	})
}

// TestManagedStatusNamesItsFieldsInTheContract: the document is the closed
// shape the task's interface names, field for field and no more — a member that
// arrives without this list moving is a member a console would have to guess
// at, and the direction that matters is the one no compile catches: a field
// added HERE and nowhere else.
//
// The other half — that the console contract names every one of these members
// and invents none — is the Docs test that reads that document as bytes
// (TestDocsDesktopConsoleContractNamesTheOtherShapesFields carries these two
// structs in its shape list, and its sibling refuses invented names). It cannot
// be read from here: no Go file outside the boundary's allow-list may name a
// path under the console's tree (internal/cli/docs_console_boundary_test.go),
// which is why the two halves live in two files.
func TestManagedStatusNamesItsFieldsInTheContract(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(managedStatusDoc{}), reflect.TypeOf(managedStatusAudit{}),
		// The retirement half is the machine's own record of the last update,
		// carried as that record's types rather than as a second set of tags
		// (CM8): the walker follows an embedded type into its members and a
		// slice into its row shape, so the names below are the record's own and
		// a member the record gains fails here until the contract lists it.
		reflect.TypeOf(managedStatusRetirements{}),
		reflect.TypeOf(localcfg.UpdateProject{}),
		reflect.TypeOf(localcfg.UpdateRetirement{}),
	}
	got := jsonTagNames(t, namedTypes(types...), types...)
	slices.Sort(got)
	// A name two shapes carry is one member of the document's vocabulary: `at`
	// is the trail's and the record's, `version` the marks' and the record's,
	// and this list is a set of names rather than a list of positions.
	got = slices.Compact(got)
	want := []string{
		"address", "at", "audit", "audit_count", "audit_day", "audit_state",
		"cause", "command", "gcp_project", "generation", "identity", "job", "kept",
		"kind", "logical", "managed", "plan_digest", "project", "protected",
		"reason", "region", "retire", "retirements", "retirements_state",
		"runs_in_flight", "secret_id", "verdict", "version",
	}
	if !slices.Equal(got, want) {
		t.Errorf("the document's json members = %v, want %v: the shape is closed, so a member that is not one of these is one a screen has been told nothing about, and one of these that is gone is a screen reading a field no command writes", got, want)
	}
}

// TestManagedStatusCarriesTheLastUpdatesRetirement: the document carries the
// machine's own record of the project's last update — the entry CM1 writes —
// member for member, with the record's version beside it (CM8's read, F21,
// D1485). The entry is the record's own type and not a copy of it, so the check
// is a comparison against what RecordUpdate was handed: a member dropped,
// renamed or retyped on the way to the console fails here.
//
// The four members of the entry are also the whole of the entry's shape: the
// document is closed at this level too, and a consumer reading it reads these.
func TestManagedStatusCarriesTheLastUpdatesRetirement(t *testing.T) {
	r := newManagedStatusRig(t, managedAuditOpts{})
	when := managedAuditTestNow.Add(-2 * time.Hour)
	runs := 3
	up := localcfg.UpdateProject{
		At: when,
		Retire: []localcfg.UpdateRetirement{{
			Address:   "module.images.google_secret_manager_secret.gh_token",
			Kind:      "secret",
			Cause:     "the repository declares no secret of that name",
			Kept:      "the value is escrowed under the installation's own name",
			Logical:   "gh-token",
			SecretID:  "fugaro-example-cloud-gh-token",
			Job:       "fugaro-images",
			Region:    "us-east5",
			Protected: true,
		}},
		RunsInFlight: &runs,
	}
	// Written through the writer the update itself calls: what the read is
	// asked about is a document this program produced.
	if err := localcfg.RecordUpdate(os.Getenv, "aurora", up); err != nil {
		t.Fatal(err)
	}
	doc, out := managedStatusOf(t, r)
	if doc.Retirements == nil {
		t.Fatalf("retirements = null with an entry recorded for the project: %s", out)
	}
	if doc.RetirementsState != "" {
		t.Errorf("retirements_state = %q beside an entry that was read: the state is for a record that could NOT be read, and a sentence here would draw a refusal over a row that is there", doc.RetirementsState)
	}
	if doc.Retirements.Version != localcfg.UpdateRecordVersion {
		t.Errorf("retirements.version = %d, want the record's own version (%d): it is what a consumer compares against the shape it knows", doc.Retirements.Version, localcfg.UpdateRecordVersion)
	}
	if !doc.Retirements.At.Equal(when) {
		t.Errorf("retirements.at = %s, want the time the run settled its retirement (%s)", doc.Retirements.At, when)
	}
	if !slices.Equal(doc.Retirements.Retire, up.Retire) {
		t.Errorf("retirements.retire = %+v, want the record's own rows (%+v), member for member: the entry is the record's type and not a second one that can drift", doc.Retirements.Retire, up.Retire)
	}
	if doc.Retirements.RunsInFlight == nil || *doc.Retirements.RunsInFlight != runs {
		t.Errorf("retirements.runs_in_flight = %v, want the count the record holds (%d)", doc.Retirements.RunsInFlight, runs)
	}
	// The bytes, and not only the decoded struct: the entry's own member set is
	// what a console reads, and a member that arrives unannounced is one it has
	// been told nothing about.
	var raw struct {
		Retirements map[string]json.RawMessage `json:"retirements"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("json: %v", err)
	}
	keys := make([]string, 0, len(raw.Retirements))
	for k := range raw.Retirements {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if want := []string{"at", "retire", "runs_in_flight", "version"}; !slices.Equal(keys, want) {
		t.Errorf("the retirements member carries %v, want exactly %v: the record's entry plus the record's version", keys, want)
	}
}

// TestManagedStatusCarriesAnEmptySetAsAList: a run that read its retirement set
// and found it empty records `[]` and not null, and the document carries it
// that way — "the last update retired nothing" is a set that was written, and a
// null would read as the record's absence (CM1, D1484). The count the registry
// could not answer is absent rather than zero (D1483): it is a pointer for the
// same reason.
func TestManagedStatusCarriesAnEmptySetAsAList(t *testing.T) {
	r := newManagedStatusRig(t, managedAuditOpts{})
	if err := localcfg.RecordUpdate(os.Getenv, "aurora", localcfg.UpdateProject{At: managedAuditTestNow}); err != nil {
		t.Fatal(err)
	}
	doc, out := managedStatusOf(t, r)
	if doc.Retirements == nil {
		t.Fatalf("retirements = null for a run that recorded an empty set: %s", out)
	}
	if doc.Retirements.Retire == nil || len(doc.Retirements.Retire) != 0 {
		t.Errorf("retirements.retire = %#v, want an empty list: a nil set would be drawn as the record's absence, which is a different fact about the machine", doc.Retirements.Retire)
	}
	if doc.Retirements.RunsInFlight != nil {
		t.Errorf("retirements.runs_in_flight = %d, want absent for a run that did not count the runs: a number nothing read must not be drawn as one that was (D1483)", *doc.Retirements.RunsInFlight)
	}
	var raw struct {
		Retirements map[string]json.RawMessage `json:"retirements"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("json: %v", err)
	}
	if got := string(raw.Retirements["retire"]); got != "[]" {
		t.Errorf(`retire in the document = %s, want []`, got)
	}
	if _, ok := raw.Retirements["runs_in_flight"]; ok {
		t.Errorf("the document carries runs_in_flight (%s) for a run that recorded no count: the record's own spelling of an unknown number is absence", raw.Retirements["runs_in_flight"])
	}
}

// TestManagedStatusTellsNoUpdateRecordedFromAnUnreadableRecord: the three
// answers are three (D1484's rule, one subject over from the trail's). No entry
// for the project — no record file at all, or one that was read and holds
// another project — is "no update recorded yet" and no state. A record that is
// there and could not be read is the same null WITH a state, and never the
// first: a machine whose record is corrupt is not a machine that has never
// updated, and the console's Project tab draws the two differently.
//
// It is not an exit either: the record is one subject of a document whose
// others were read, so the command prints the document and exits zero, and the
// state is the answer (the audit trail's own rule).
func TestManagedStatusTellsNoUpdateRecordedFromAnUnreadableRecord(t *testing.T) {
	t.Run("a machine that has recorded no update", func(t *testing.T) {
		r := newManagedStatusRig(t, managedAuditOpts{})
		doc, out := managedStatusOf(t, r)
		if doc.Retirements != nil || doc.RetirementsState != "" {
			t.Errorf("retirements = %+v, state %q on a machine with no record file, want null and no state: no file is an empty document and not an error (LoadUpdateRecord's own rule): %s",
				doc.Retirements, doc.RetirementsState, out)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(out), &raw); err != nil {
			t.Fatalf("json: %v", err)
		}
		if got := string(raw["retirements"]); got != "null" {
			t.Errorf("retirements in the document = %s, want null: the member is always printed so that a consumer tells a document that carries no record from one that carries none for the project", got)
		}
	})
	t.Run("a record that was read and holds another project", func(t *testing.T) {
		r := newManagedStatusRig(t, managedAuditOpts{})
		if err := localcfg.RecordUpdate(os.Getenv, "vega", localcfg.UpdateProject{At: managedAuditTestNow}); err != nil {
			t.Fatal(err)
		}
		doc, out := managedStatusOf(t, r)
		if doc.Retirements != nil || doc.RetirementsState != "" {
			t.Errorf("retirements = %+v, state %q for a record that answered and holds no entry for aurora, want null and no state: this machine has recorded no update for THIS project, which is not a failure to read: %s",
				doc.Retirements, doc.RetirementsState, out)
		}
	})
	for _, tc := range []struct {
		name string
		body string
	}{
		{"a record this build cannot parse", "{ not json"},
		{"a record of a version this build does not write", `{"version": 7, "projects": {}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newManagedStatusRig(t, managedAuditOpts{})
			path, err := localcfg.UpdateRecordPath(os.Getenv)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			doc, out := managedStatusOf(t, r)
			if doc.Retirements != nil {
				t.Errorf("retirements = %+v beside a record that could not be read, want null: there is no entry, and an empty one would draw a run that never happened", doc.Retirements)
			}
			if !strings.Contains(doc.RetirementsState, "could not") {
				t.Errorf("retirements_state = %q, want the sentence saying the record could not be read: a null with no state is the claim that this machine has never updated, and this one has a file the read refused: %s",
					doc.RetirementsState, out)
			}
			if !doc.Managed {
				t.Errorf("managed = false when the marks' read answered and only the local record did not: the two are separate reads, and conflating them would report a fact about the project from a failure about this machine's state")
			}
		})
	}
}
