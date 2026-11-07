package cli

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/initflow"
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
	types := []reflect.Type{reflect.TypeOf(managedStatusDoc{}), reflect.TypeOf(managedStatusAudit{})}
	got := jsonTagNames(t, namedTypes(types...), types...)
	slices.Sort(got)
	want := []string{
		"at", "audit", "audit_count", "audit_day", "audit_state", "command",
		"gcp_project", "generation", "identity", "managed", "plan_digest",
		"project", "reason", "verdict", "version",
	}
	if !slices.Equal(got, want) {
		t.Errorf("the document's json members = %v, want %v: the shape is closed, so a member that is not one of these is one a screen has been told nothing about, and one of these that is gone is a screen reading a field no command writes", got, want)
	}
}
