package cli

// WU13's own tests (`workspace project reconcile`, D2809).
//
// They are hermetic by construction: the drift report, the member's roster and
// the audit trail are three doubles declared here, so no test reaches Google's
// IAM, a Realtime Database or Firestore, and the caller whose address the audit
// events carry is a function the test supplies rather than an authenticated
// read. The fake member seam fails on any call this command has no business
// making -- a second read of a mirror, a binding read, an offboard -- so a
// stray write a later change added is a failed test rather than a quiet one.
//
// The red step the plan names is the remove case: against a fake whose report
// is empty nothing is taken back, and TestReconcileRemovesExtra fails on the
// missing write rather than on an assertion about the printing.

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// wu13Member is the member every test reconciles: one whose Firebase project is
// its GCP project, which is the shape F2 lets a member have.
var wu13Member = workspace.MemberTarget{Project: "member-project", Firebase: "member-project"}

// wu13Deps builds the three seams this command consumes, wired to the same
// member, and holds the caller's own address and the instant still so that no
// test reads the network or the wall clock.
func wu13Deps(t *testing.T, report workspace.DriftReport, ops *wu13FakeOps, audit *wu13FakeAudit) workspaceReconcileDeps {
	t.Helper()
	oldActor, oldNow := workspaceReconcileActor, workspaceReconcileNow
	workspaceReconcileActor = func(context.Context) (string, error) { return "owner@example.com", nil }
	workspaceReconcileNow = func() time.Time { return time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { workspaceReconcileActor, workspaceReconcileNow = oldActor, oldNow })
	return workspaceReconcileDeps{
		Workspace: "workspace-alpha",
		Members: func(_ context.Context, project string) (workspace.MemberTarget, error) {
			if project != wu13Member.Project {
				t.Errorf("the command resolved %q, and the test reconciled %q", project, wu13Member.Project)
			}
			return wu13Member, nil
		},
		Drift: func(_ context.Context, m workspace.MemberTarget) (workspace.DriftReport, error) {
			if m != wu13Member {
				t.Errorf("the report was asked about %+v, want %+v", m, wu13Member)
			}
			return report, nil
		},
		Ops:   ops,
		Audit: audit,
	}
}

// wu13Run runs the command itself, with the flags the test names: the family it
// is registered in (I1) builds no part of what this file asserts, so the
// command is built directly, as WU22's own tests build the probe.
func wu13Run(t *testing.T, d workspaceReconcileDeps, args ...string) (string, error) {
	t.Helper()
	cmd := newWorkspaceProjectReconcileCmd(d)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

// wu13Grant is one row of a roster fixture. The scope is the project scope,
// which is the one a binding can come from: the member's own update derives its
// IAM lists from the project-scope grants alone.
func wu13Grant(address, role string) accesslib.Grant {
	return accesslib.Grant{User: "user:" + address, Role: accesslib.Role(role), Scope: accesslib.Scope{Kind: accesslib.ScopeProject}}
}

// wu13Grants encodes a roster fixture the way the member's own CLI writes it,
// so what the fake hands back and what the command writes are the same format.
func wu13Grants(t *testing.T, grants ...accesslib.Grant) []byte {
	t.Helper()
	raw, err := accesslib.Encode(grants)
	if err != nil {
		t.Fatalf("encoding the roster fixture: %v", err)
	}
	return raw
}

// wu13Decode reads a document back the way the member's own CLI reads it.
func wu13Decode(t *testing.T, raw []byte) []accesslib.Grant {
	t.Helper()
	grants, err := accesslib.Decode(raw)
	if err != nil {
		t.Fatalf("the document the command wrote is not one the member's CLI reads: %v", err)
	}
	return grants
}

// wu13Rows is a roster as comparable strings, so a test states the whole
// document rather than the one row it means.
func wu13Rows(grants []accesslib.Grant) []string {
	var out []string
	for _, g := range grants {
		out = append(out, g.User+" "+string(g.Role)+" "+g.Scope.String())
	}
	return out
}

// wu13FakeOps is C2's member seam as this file needs it: a roster the test
// holds, the writes it accepts, and a failure for every other call. The write
// judge is the test's, so a case can hand back rtdb.ErrPrecondition and move
// the roster under the command (C2's one answer).
type wu13FakeOps struct {
	t *testing.T
	// raw and etag are the roster as it stands now; found false is a node that
	// is not there at all.
	raw   []byte
	etag  string
	found bool
	// writes are the documents a WriteGrants really stored.
	writes [][]byte
	// writeErr is handed back once per entry, from the front, before a write
	// lands; when it is empty a write lands.
	writeErr []error
	// onWrite runs before each write is judged, so a case can move the roster
	// under the command.
	onWrite func(o *wu13FakeOps)
	// reads counts the ReadGrants calls, which is how the retry is asserted.
	reads  int
	member workspace.MemberTarget
}

func (o *wu13FakeOps) ReadGrants(_ context.Context, m workspace.MemberTarget) (workspace.MemberDoc, error) {
	o.reads++
	o.member = m
	if !o.found {
		return workspace.MemberDoc{ETag: o.etag}, nil
	}
	return workspace.MemberDoc{Raw: o.raw, ETag: o.etag, Found: true}, nil
}

func (o *wu13FakeOps) WriteGrants(_ context.Context, m workspace.MemberTarget, doc workspace.MemberDoc, raw []byte) error {
	o.member = m
	if doc.ETag != o.etag {
		o.t.Errorf("the write was made under ETag %q, and the roster stands at %q: a write not judged against what was read is the overwrite the ETag prevents", doc.ETag, o.etag)
	}
	if o.onWrite != nil {
		o.onWrite(o)
	}
	if len(o.writeErr) > 0 {
		err := o.writeErr[0]
		o.writeErr = o.writeErr[1:]
		return err
	}
	o.writes = append(o.writes, raw)
	o.raw, o.etag, o.found = raw, "wu13-etag-after-write", true
	return nil
}

// The methods this command must not reach. Each one fails the test rather than
// silently answering: an IAM write, a mirror write or an offboard from here
// would be authority taken rather than recorded (D2801, D2806).
func (o *wu13FakeOps) ReadMirror(context.Context, workspace.MemberTarget) (workspace.MemberDoc, error) {
	o.t.Error("reconcile read the member's mirror")
	return workspace.MemberDoc{}, nil
}

func (o *wu13FakeOps) WriteMirror(context.Context, workspace.MemberTarget, workspace.MemberDoc, []byte) error {
	o.t.Error("reconcile wrote the member's mirror")
	return nil
}

func (o *wu13FakeOps) Bindings(context.Context, workspace.MemberTarget) ([]workspace.MemberBinding, error) {
	o.t.Error("reconcile read the member's IAM bindings of its own: the report is the drift check's")
	return nil, nil
}

func (o *wu13FakeOps) PendingApply(context.Context, workspace.MemberTarget, workspace.ApplyWant) (workspace.Pending, error) {
	o.t.Error("reconcile read what waits for the member's own update")
	return workspace.Pending{}, nil
}

func (o *wu13FakeOps) Offboard(context.Context, workspace.MemberTarget, string) error {
	o.t.Error("reconcile offboarded somebody: offboarding is WU9's verb")
	return nil
}

func (o *wu13FakeOps) Audit(context.Context, workspace.MemberTarget, string, []byte) error {
	o.t.Error("reconcile filed its events in the member's trail: the workspace admin's changes belong in the workspace's own")
	return nil
}

// wu13FakeAudit is C1's sink: the workspace's own trail, as the documents a
// Create really stored.
type wu13FakeAudit struct {
	t      *testing.T
	ids    []string
	events []workspace.AuditEvent
	err    error
}

func (a *wu13FakeAudit) Create(_ context.Context, id string, event []byte) error {
	if a.err != nil {
		return a.err
	}
	if len(a.ids) > 0 && a.ids[len(a.ids)-1] == id {
		a.t.Errorf("the event id %q was reused: a trail is create-only, and a repeat is the entry the second write would lose", id)
	}
	decoded, err := workspace.DecodeAuditEvent(event)
	if err != nil {
		a.t.Errorf("the event filed under %q is not one this build reads: %v", id, err)
		return nil
	}
	a.ids = append(a.ids, id)
	a.events = append(a.events, decoded)
	return nil
}

// wu13Missing and wu13Extra are the two rows of a report, stated once so the
// tests differ only in what they are about.
func wu13Missing(principal, role string, kind workspace.PrincipalKind) workspace.DriftBinding {
	return workspace.DriftBinding{Project: "member-project", Principal: principal, Role: role, Kind: kind}
}

func wu13Extra(principal, role string, kind workspace.PrincipalKind) workspace.DriftBinding {
	return workspace.DriftBinding{Project: "member-project", Principal: principal, Role: role, Kind: kind}
}

// TestReconcileDryRunWritesNothing: --dry-run is the whole of "look at it
// first". The change it would make is printed -- both directions -- and neither
// the member's roster nor the workspace's trail is written (D2809).
func TestReconcileDryRunWritesNothing(t *testing.T) {
	before := wu13Grants(t, wu13Grant("alice@example.com", "operator"), wu13Grant("bob@example.com", "admin"))
	ops := &wu13FakeOps{t: t, raw: before, etag: "wu13-etag-1", found: true}
	audit := &wu13FakeAudit{t: t}
	d := wu13Deps(t, workspace.DriftReport{
		Missing: []workspace.DriftBinding{wu13Missing("carol@example.com", "operator", workspace.PrincipalPerson)},
		Extra:   []workspace.DriftBinding{wu13Extra("bob@example.com", "admin", workspace.PrincipalPerson)},
	}, ops, audit)

	out, err := wu13Run(t, d, "--dry-run", wu13Member.Project)
	if err != nil {
		t.Fatalf("workspace project reconcile --dry-run: %v", err)
	}
	if len(ops.writes) != 0 {
		t.Errorf("--dry-run wrote the member's roster %d times: %s", len(ops.writes), ops.writes)
	}
	if len(audit.ids) != 0 {
		t.Errorf("--dry-run filed %d audit events", len(audit.ids))
	}
	for _, want := range []string{"carol@example.com", "add", "bob@example.com", "remove"} {
		if !strings.Contains(out, want) {
			t.Errorf("the dry run does not print %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "--dry-run prints the change and writes nothing") {
		t.Errorf("the dry run does not say it wrote nothing:\n%s", out)
	}
}

// TestReconcileRemovesExtra: a binding the member's roster holds and the record
// does not state is taken back out of the roster -- the one row, with the rows
// the command did not touch left where they were -- and the change is filed in
// the workspace's trail as a removal. The printing says what applies it, which
// is the member's own update and no command of this machine (D2801).
//
// This is the plan's red step: with an empty report nothing is taken back and
// the write this test asks for never happens.
func TestReconcileRemovesExtra(t *testing.T) {
	ops := &wu13FakeOps{t: t,
		raw:   wu13Grants(t, wu13Grant("alice@example.com", "operator"), wu13Grant("bob@example.com", "admin")),
		etag:  "wu13-etag-1",
		found: true}
	audit := &wu13FakeAudit{t: t}
	d := wu13Deps(t, workspace.DriftReport{
		Extra: []workspace.DriftBinding{wu13Extra("bob@example.com", "admin", workspace.PrincipalPerson)},
	}, ops, audit)

	out, err := wu13Run(t, d, wu13Member.Project)
	if err != nil {
		t.Fatalf("workspace project reconcile: %v", err)
	}
	if len(ops.writes) != 1 {
		t.Fatalf("the member's roster was written %d times, want the one removal", len(ops.writes))
	}
	want := []string{"user:alice@example.com operator project"}
	if got := wu13Rows(wu13Decode(t, ops.writes[0])); !slices.Equal(got, want) {
		t.Errorf("the roster holds %v, want %v: the extra row is taken back and the rest kept", got, want)
	}
	if len(audit.events) != 1 {
		t.Fatalf("the trail holds %d events, want one per change", len(audit.events))
	}
	got := audit.events[0]
	if got.Action != workspace.ActionGrantRemove || got.Subject != "bob@example.com" || got.Project != wu13Member.Project ||
		got.Actor != "owner@example.com" || string(got.Role) != "admin" {
		t.Errorf("the trail event is %+v, want the one removal by the caller", got)
	}
	if got.ID == "" || got.Version != workspace.AuditEventVersion || got.Time.IsZero() {
		t.Errorf("the trail event is %+v, want an id, the version and the instant", got)
	}
	if !strings.Contains(out, "The member's own fugaro update applies them") {
		t.Errorf("the run does not say what applies the write:\n%s", out)
	}
	if !strings.Contains(out, "remove bob@example.com admin") {
		t.Errorf("the run does not print the removal:\n%s", out)
	}
}

// TestReconcileLeavesMachines: a machine's difference is reported and never
// written (D2806). The roster is left exactly as it was, no event is filed, and
// the printing says which difference is a machine's and why nothing here writes
// one.
func TestReconcileLeavesMachines(t *testing.T) {
	before := wu13Grants(t, wu13Grant("alice@example.com", "operator"))
	ops := &wu13FakeOps{t: t, raw: before, etag: "wu13-etag-1", found: true}
	audit := &wu13FakeAudit{t: t}
	d := wu13Deps(t, workspace.DriftReport{
		Missing: []workspace.DriftBinding{wu13Missing("ci@member-project.iam.gserviceaccount.com", "operator", workspace.PrincipalMachine)},
		Extra:   []workspace.DriftBinding{wu13Extra("builder@member-project.iam.gserviceaccount.com", "admin", workspace.PrincipalMachine)},
	}, ops, audit)

	out, err := wu13Run(t, d, wu13Member.Project)
	if err != nil {
		t.Fatalf("workspace project reconcile: %v", err)
	}
	if len(ops.writes) != 0 {
		t.Errorf("a machine's drift was written: %s", ops.writes)
	}
	if len(audit.ids) != 0 {
		t.Errorf("a machine's drift was audited: %v", audit.ids)
	}
	if !bytes.Equal(ops.raw, before) {
		t.Errorf("the roster changed under a machine's difference:\n%s", ops.raw)
	}
	for _, want := range []string{"ci@member-project.iam.gserviceaccount.com", "builder@member-project.iam.gserviceaccount.com", "a machine", "never written"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not name %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "nothing to write") {
		t.Errorf("a report of machines only reads as a clean one:\n%s", out)
	}
}

// TestReconcileAddsMissingGrant: a grant the record states and the member does
// not carry is added as a row at the project scope, which is the scope the
// member's own update derives a binding from -- and it is an addition, so the
// roster it writes is the one that was there plus the row.
func TestReconcileAddsMissingGrant(t *testing.T) {
	ops := &wu13FakeOps{t: t,
		raw:   wu13Grants(t, wu13Grant("alice@example.com", "operator")),
		etag:  "wu13-etag-1",
		found: true}
	audit := &wu13FakeAudit{t: t}
	d := wu13Deps(t, workspace.DriftReport{
		Missing: []workspace.DriftBinding{wu13Missing("carol@example.com", "admin", workspace.PrincipalPerson)},
	}, ops, audit)

	if _, err := wu13Run(t, d, wu13Member.Project); err != nil {
		t.Fatalf("workspace project reconcile: %v", err)
	}
	if len(ops.writes) != 1 {
		t.Fatalf("the member's roster was written %d times, want one", len(ops.writes))
	}
	got := wu13Decode(t, ops.writes[0])
	if len(got) != 2 || got[0].User != "user:alice@example.com" || got[1].User != "user:carol@example.com" {
		t.Fatalf("the roster holds %v, want the row that was there and the one added after it", wu13Rows(got))
	}
	if got[1].Role != accesslib.RoleAdmin || got[1].Scope.Kind != accesslib.ScopeProject || !got[1].Scope.Valid() {
		t.Errorf("the added row is %+v, want the admin role at the project scope with nothing else in it: a grant at any other scope derives no binding for the member's own update to restore", got[1])
	}
	if len(audit.events) != 1 || audit.events[0].Action != workspace.ActionGrantWrite || audit.events[0].Subject != "carol@example.com" {
		t.Fatalf("the trail holds %+v, want one grant.write for carol", audit.events)
	}
	// The id opens with the instant the event was filed at (NewAuditEventID),
	// which is what makes a listing in key order a listing in time order.
	if want := "20261121T090000Z-"; !strings.HasPrefix(audit.events[0].ID, want) {
		t.Errorf("the event id %q does not open with %q", audit.events[0].ID, want)
	}
}

// TestReconcileRefusesOnAnUnmigratedList: an absent roster node is the state an
// installation whose roles are still its own IAM lists is in, and a write there
// CREATES the node holding this change and nobody else -- every other member
// loses their role at the member's next update. The refusal is `fugaro user
// grant`'s, and the member's own update is what migrates the lists.
func TestReconcileRefusesOnAnUnmigratedList(t *testing.T) {
	ops := &wu13FakeOps{t: t, etag: "wu13-null-etag"}
	audit := &wu13FakeAudit{t: t}
	d := wu13Deps(t, workspace.DriftReport{
		Missing: []workspace.DriftBinding{wu13Missing("carol@example.com", "operator", workspace.PrincipalPerson)},
	}, ops, audit)

	_, err := wu13Run(t, d, wu13Member.Project)
	if err == nil {
		t.Fatal("the command wrote a roster node into an installation whose lists were never migrated")
	}
	for _, want := range []string{"nothing was written", accesslib.PathNode, "fugaro update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %q", err, want)
		}
	}
	if len(ops.writes) != 0 || len(audit.ids) != 0 {
		t.Errorf("the refused run wrote %d rosters and %d events", len(ops.writes), len(audit.ids))
	}
}

// TestReconcileNamesWhatItCannotReach: a difference it writes nothing for is
// named with the reason, so an operator reading an empty change list does not
// read it as "nothing to do" -- a binding the member's roster states nothing
// about cannot be taken back by a roster write, and an unplaced principal is
// not a row this command invents an address for.
func TestReconcileNamesWhatItCannotReach(t *testing.T) {
	ops := &wu13FakeOps{t: t,
		raw:   wu13Grants(t, wu13Grant("alice@example.com", "operator")),
		etag:  "wu13-etag-1",
		found: true}
	audit := &wu13FakeAudit{t: t}
	d := wu13Deps(t, workspace.DriftReport{
		Extra: []workspace.DriftBinding{
			wu13Extra("mallory@example.com", "admin", workspace.PrincipalPerson),
			wu13Extra("nobody@example.com", "operator", workspace.PrincipalUnknown),
		},
		Unmeasured:     []string{"config/admin/grants of the member's Firebase project: the read was denied"},
		ConsoleChanges: []workspace.ConsoleChange{{Project: "member-project", Principal: "eve@example.com", Role: "roles/owner", Entry: "wu13-entry", At: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)}},
	}, ops, audit)

	out, err := wu13Run(t, d, wu13Member.Project)
	if err != nil {
		t.Fatalf("workspace project reconcile: %v", err)
	}
	if len(ops.writes) != 0 {
		t.Errorf("a binding the roster states nothing about was written: %s", ops.writes)
	}
	for _, want := range []string{
		"mallory@example.com", "writes no member's IAM",
		"nobody@example.com", "cannot place",
		"not measured by the drift check",
		"wu13-entry", "Google's console",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not print %q:\n%s", want, out)
		}
	}
}

// TestReconcileRetriesAPreconditionLoss: a write that lost the ETag race is C2's
// ANSWER and not a failure -- another writer landed between the read and the
// write. The command reads the roster again and judges the same report against
// what is really there, so the row the other writer added survives and the
// change is still made.
func TestReconcileRetriesAPreconditionLoss(t *testing.T) {
	ops := &wu13FakeOps{t: t,
		raw:      wu13Grants(t, wu13Grant("alice@example.com", "operator"), wu13Grant("bob@example.com", "admin")),
		etag:     "wu13-etag-1",
		found:    true,
		writeErr: []error{rtdb.ErrPrecondition}}
	ops.onWrite = func(o *wu13FakeOps) {
		if o.etag == "wu13-etag-1" {
			o.raw = wu13Grants(t, wu13Grant("alice@example.com", "operator"), wu13Grant("bob@example.com", "admin"), wu13Grant("dave@example.com", "viewer"))
			o.etag = "wu13-etag-2"
		}
	}
	audit := &wu13FakeAudit{t: t}
	d := wu13Deps(t, workspace.DriftReport{
		Extra: []workspace.DriftBinding{wu13Extra("bob@example.com", "admin", workspace.PrincipalPerson)},
	}, ops, audit)

	if _, err := wu13Run(t, d, wu13Member.Project); err != nil {
		t.Fatalf("the precondition answer was reported as a failure: %v", err)
	}
	if ops.reads != 2 {
		t.Errorf("the roster was read %d times, want the read after the loss", ops.reads)
	}
	if len(ops.writes) != 1 {
		t.Fatalf("the member's roster was written %d times, want one after the retry", len(ops.writes))
	}
	want := []string{
		"user:alice@example.com operator project",
		"user:dave@example.com viewer project",
	}
	if got := wu13Rows(wu13Decode(t, ops.writes[0])); !slices.Equal(got, want) {
		t.Errorf("the roster holds %v, want %v: the row the other writer added survives the retry", got, want)
	}
	if len(audit.events) != 1 {
		t.Errorf("the trail holds %d events, want the one change", len(audit.events))
	}
}

// TestReconcileMemberRefusesInAnAgentSession: the change lands in a member's
// own roster, so it is a person's step at their own terminal and an agent's
// tool call is refused before anything is read (A7). Nothing is read, written
// or filed. (The name carries "Member" because `fugaro project reconcile` has a
// fence test of its own, project_reconcile_test.go.)
func TestReconcileMemberRefusesInAnAgentSession(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	ops := &wu13FakeOps{t: t,
		raw:   wu13Grants(t, wu13Grant("alice@example.com", "operator")),
		etag:  "wu13-etag-1",
		found: true}
	audit := &wu13FakeAudit{t: t}
	d := wu13Deps(t, workspace.DriftReport{
		Extra: []workspace.DriftBinding{wu13Extra("alice@example.com", "operator", workspace.PrincipalPerson)},
	}, ops, audit)

	_, err := wu13Run(t, d, wu13Member.Project)
	if err == nil {
		t.Fatal("an agent's session reconciled a member's roster")
	}
	if !strings.Contains(err.Error(), "coding agent's session") || !strings.Contains(err.Error(), "fugaro workspace project reconcile") {
		t.Errorf("the refusal %q does not name the session and the command to run instead", err)
	}
	if ops.reads != 0 || len(ops.writes) != 0 || len(audit.ids) != 0 {
		t.Errorf("the refusal read the roster %d times, wrote %d and filed %d events", ops.reads, len(ops.writes), len(audit.ids))
	}
}

// TestReconcileReportsATrailThatDidNotLand: the roster write and the audit
// entry are two stores, and the failure to be loud about is the half that
// landed: a change nobody can account for. The error says the roster was
// written and the entry was not.
func TestReconcileReportsATrailThatDidNotLand(t *testing.T) {
	ops := &wu13FakeOps{t: t,
		raw:   wu13Grants(t, wu13Grant("alice@example.com", "operator"), wu13Grant("bob@example.com", "admin")),
		etag:  "wu13-etag-1",
		found: true}
	audit := &wu13FakeAudit{t: t, err: rtdb.ErrPrecondition}
	d := wu13Deps(t, workspace.DriftReport{
		Extra: []workspace.DriftBinding{wu13Extra("bob@example.com", "admin", workspace.PrincipalPerson)},
	}, ops, audit)

	_, err := wu13Run(t, d, wu13Member.Project)
	if err == nil {
		t.Fatal("a change whose audit entry did not land was reported as done")
	}
	for _, want := range []string{"was written", "did not land", "bob@example.com"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q does not name %q", err, want)
		}
	}
	if len(ops.writes) != 1 {
		t.Errorf("the roster was written %d times, want the one change the error is about", len(ops.writes))
	}
}
