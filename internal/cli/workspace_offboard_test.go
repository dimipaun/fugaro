package cli

// The member half of `fugaro user offboard` (WU9, F13, D2813, D2818): the core
// that takes one person off ONE named member project -- their grants dropped
// from that member's roster, the act created in that member's own trail, and
// the secrets they stored under their own prefix swept by that member's intake
// -- and that refuses until the caller's stored grant has reached the member's
// derived admin list.
//
// The two seams the core reaches a member through are fakes of this file's
// own (C2's MemberOps and the intake it sweeps through), and the identity it
// reads is offboardCaller's. Nothing here touches a database, a service, a
// credential or a person's terminal.
//
// Its second half is the workspace half (WC10, F21): offboardWorkspacePerson
// drives that core over every member a person holds and then sweeps the
// WORKSPACE's own intake, whose containers the members' sweeps never reach
// (D2960). TestTheSweepRunsWhenAMemberRefused pins the order and the "whatever
// they answered".

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// The fixture's member and its people. The member's Firebase project is a
// project of its own, so every call that reaches the member names the pair and
// a core that dropped one of them shows (F2, R10).
var wu9Member = workspace.MemberTarget{Project: "aurora", Firebase: "aurora-firebase"}

const (
	// wu9Admin is the workspace admin the caller's credential belongs to: their
	// grant is a row of the member's roster, which is what admits them here.
	wu9Admin = "owner@example.com"
	// wu9Person is the member being taken off.
	wu9Person = "member@example.com"
)

// wu9At is the instant the fixture's audit entry is stamped with, so a test
// reads the entry rather than the second the suite ran in.
var wu9At = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

// wu9Grant is one stored grant of a fixture roster, in the canonical spelling
// the node's own encoder writes.
func wu9Grant(t *testing.T, address, role, scope string) accesslib.Grant {
	t.Helper()
	member, err := accesslib.Member(address)
	if err != nil {
		t.Fatalf("the fixture's address %q: %v", address, err)
	}
	parsed, err := accesslib.ParseScope(scope)
	if err != nil {
		t.Fatalf("the fixture's scope %q: %v", scope, err)
	}
	return accesslib.Grant{User: member, Role: accesslib.Role(role), Scope: parsed}
}

// wu9Roster is a member's grants node as its database holds it: the bytes a
// ReadGrants answers, through the format's own encoder.
func wu9Roster(t *testing.T, grants ...accesslib.Grant) []byte {
	t.Helper()
	raw, err := accesslib.Encode(grants)
	if err != nil {
		t.Fatalf("encoding the fixture roster: %v", err)
	}
	return raw
}

// wu9Write is one WriteGrants the core made: the member it named, the document
// it was conditional on, and the bytes it asked for.
type wu9Write struct {
	m   workspace.MemberTarget
	doc workspace.MemberDoc
	raw []byte
}

// wu9Audit is one Audit the core made.
type wu9Audit struct {
	m     workspace.MemberTarget
	id    string
	event []byte
}

// wu9FakeMemberOps is the member reached as C2's MemberOps: the four methods
// the core calls answer what the test seeded, and the rest answer their zero
// shapes, as C2's own stub does -- a test that made the core call one of them
// would see the zero answer and not a fake's opinion.
type wu9FakeMemberOps struct {
	t *testing.T
	// doc is what ReadGrants answers, and what a landed write replaces.
	doc workspace.MemberDoc
	// reads counts the reads, so a retry is visible.
	reads int
	// preconditions is how many writes are refused as a node another admin
	// moved, before one lands.
	preconditions int
	// writeErr, pendingErr and auditErr are the failures the test seeded.
	writeErr   error
	pendingErr error
	auditErr   error
	// pending is what PendingApply answers, and wants records what it was asked.
	pending workspace.Pending
	wants   []workspace.ApplyWant
	writes  []wu9Write
	audits  []wu9Audit
}

func (f *wu9FakeMemberOps) ReadGrants(context.Context, workspace.MemberTarget) (workspace.MemberDoc, error) {
	f.reads++
	return f.doc, nil
}

func (f *wu9FakeMemberOps) WriteGrants(_ context.Context, m workspace.MemberTarget, doc workspace.MemberDoc, raw []byte) error {
	if f.preconditions > 0 {
		f.preconditions--
		return rtdb.ErrPrecondition
	}
	if f.writeErr != nil {
		return f.writeErr
	}
	f.writes = append(f.writes, wu9Write{m: m, doc: doc, raw: raw})
	f.doc = workspace.MemberDoc{Raw: raw, ETag: "etag-2", Found: true}
	return nil
}

func (f *wu9FakeMemberOps) PendingApply(_ context.Context, _ workspace.MemberTarget, want workspace.ApplyWant) (workspace.Pending, error) {
	f.wants = append(f.wants, want)
	if f.pendingErr != nil {
		return workspace.Pending{}, f.pendingErr
	}
	return f.pending, nil
}

func (f *wu9FakeMemberOps) Audit(_ context.Context, m workspace.MemberTarget, id string, event []byte) error {
	if f.auditErr != nil {
		return f.auditErr
	}
	f.audits = append(f.audits, wu9Audit{m: m, id: id, event: event})
	return nil
}

func (f *wu9FakeMemberOps) ReadMirror(context.Context, workspace.MemberTarget) (workspace.MemberDoc, error) {
	return workspace.MemberDoc{}, nil
}

func (f *wu9FakeMemberOps) WriteMirror(context.Context, workspace.MemberTarget, workspace.MemberDoc, []byte) error {
	return nil
}

func (f *wu9FakeMemberOps) Bindings(context.Context, workspace.MemberTarget) ([]workspace.MemberBinding, error) {
	return nil, nil
}

func (f *wu9FakeMemberOps) Offboard(context.Context, workspace.MemberTarget, string) error {
	f.t.Fatal("the offboarder called its own Offboard: the core is the implementation of it and never its caller")
	return nil
}

func (f *wu9FakeMemberOps) RecordRemoval(context.Context, workspace.MemberTarget, string) error {
	return nil
}

// wu9SweepCall is one Sweep the core made.
type wu9SweepCall struct {
	m       workspace.MemberTarget
	address string
}

// wu9FakeSweeper is a member's intake as the core reaches it: the calls it
// made, and what the delete answered.
type wu9FakeSweeper struct {
	deleted []string
	err     error
	calls   []wu9SweepCall
}

func (f *wu9FakeSweeper) Sweep(_ context.Context, m workspace.MemberTarget, address string) ([]string, error) {
	f.calls = append(f.calls, wu9SweepCall{m: m, address: address})
	if f.err != nil {
		return nil, f.err
	}
	return f.deleted, nil
}

// wu9Offboarder is the core under test, with the credential's identity the
// fixture's own: offboardCaller is the seam that read goes through, and a
// test's function stands there so no test reaches Google's userinfo endpoint.
func wu9Offboarder(t *testing.T, caller string, ops workspace.MemberOps, sweep memberSweeper) *memberOffboarder {
	t.Helper()
	member, err := accesslib.Member(caller)
	if err != nil {
		t.Fatalf("the fixture's caller %q: %v", caller, err)
	}
	prev := offboardCaller
	offboardCaller = func(context.Context) (string, error) { return member, nil }
	t.Cleanup(func() { offboardCaller = prev })
	o := newMemberOffboarder(ops, sweep)
	o.at = func() time.Time { return wu9At }
	return o
}

// wu9Rig is one fixture: the member's roster with the admin's grant and the
// person's two, the fakes, and the core over them.
func wu9Rig(t *testing.T) (*memberOffboarder, *wu9FakeMemberOps, *wu9FakeSweeper) {
	t.Helper()
	ops := &wu9FakeMemberOps{t: t, doc: workspace.MemberDoc{
		Raw: wu9Roster(t,
			wu9Grant(t, wu9Admin, "admin", "project"),
			wu9Grant(t, wu9Person, "operator", "project"),
			wu9Grant(t, wu9Person, "viewer", "all-repos"),
		),
		ETag:  "etag-1",
		Found: true,
	}}
	sweep := &wu9FakeSweeper{deleted: []string{"fugaro_u_4242_github-token"}}
	return wu9Offboarder(t, wu9Admin, ops, sweep), ops, sweep
}

// wu9Stored is the member's roster as the fake holds it after the act: read
// back through the format's own decoder, so a test asserts on what a reader of
// that member's database would find.
func wu9Stored(t *testing.T, ops *wu9FakeMemberOps) []accesslib.Grant {
	t.Helper()
	grants, err := accesslib.Decode(ops.doc.Raw)
	if err != nil {
		t.Fatalf("the roster the core wrote is not one this release reads: %v", err)
	}
	return grants
}

// wu9Names reports whether any stored grant names the member.
func wu9Names(grants []accesslib.Grant, member string) bool {
	for _, g := range grants {
		if g.User == member {
			return true
		}
	}
	return false
}

// TestOffboardMemberNeedsAStoredProjectAdmin: a caller who is not a
// PROJECT-scope admin of the member's own roster is refused, and refused
// before anything on the member is touched — this check stands in front of a
// write to another project's roster and a deletion of a person's credentials,
// so what a caller it refuses finds is a member exactly as it was: no write,
// no entry in its trail, no sweep, and not even the comparison against its
// derived admin list, which is asked of a caller the roster places (F17.3,
// D1940).
//
// The four callers are the ones a plausible mistake would admit. An admin
// grant scoped to one repository, or one at all-repos, holds no project-level
// thing whatever its role says (F19), and a viewer at project scope is a
// project role below admin: none of them is an admin of the installation, and
// the member's own intake refuses them the same way.
func TestOffboardMemberNeedsAStoredProjectAdmin(t *testing.T) {
	const (
		wu9Stranger      = "stranger@example.com"
		wu9RepoAdmin     = "repoadmin@example.com"
		wu9AllRepos      = "allreposadmin@example.com"
		wu9ProjectViewer = "viewer@example.com"
	)
	cases := []struct {
		name   string
		caller string
		// role and scope are the caller's grant of the member's roster, both
		// empty for a caller its roster does not name at all.
		role, scope string
	}{
		{"no grant at all", wu9Stranger, "", ""},
		{"an admin scoped to one repository", wu9RepoAdmin, "admin", "github/acme/app"},
		{"an admin at all-repos", wu9AllRepos, "admin", "all-repos"},
		{"a viewer at project scope", wu9ProjectViewer, "viewer", "project"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var mine []accesslib.Grant
			if c.role != "" {
				mine = append(mine, wu9Grant(t, c.caller, c.role, c.scope))
			}
			ops := &wu9FakeMemberOps{t: t, doc: workspace.MemberDoc{
				Raw: wu9Roster(t, append(mine,
					wu9Grant(t, wu9Person, "operator", "project"),
					wu9Grant(t, wu9Person, "viewer", "all-repos"))...),
				ETag:  "etag-1",
				Found: true,
			}}
			sweep := &wu9FakeSweeper{deleted: []string{"fugaro_u_4242_github-token"}}
			o := wu9Offboarder(t, c.caller, ops, sweep)

			err := o.Offboard(context.Background(), wu9Member, wu9Person)
			if err == nil {
				t.Fatalf("%s took %s off %s while holding no project-scope admin grant of that member's roster", c.caller, wu9Person, wu9Member.Project)
			}
			for _, want := range []string{wu9Member.Project, "user:" + c.caller, "project-scope admin", accesslib.PathNode} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q, so the caller cannot tell what would admit them:\n%v", want, err)
				}
			}
			if len(ops.writes) != 0 || len(ops.audits) != 0 || len(sweep.calls) != 0 {
				t.Errorf("the refused offboarding acted on the member anyway: %d writes, %d audit entries, %d sweeps — the caller's own authority is asked before any of them",
					len(ops.writes), len(ops.audits), len(sweep.calls))
			}
			if len(ops.wants) != 0 {
				t.Errorf("the member's derived admin list was compared %d times for a caller its roster does not place as an admin: that comparison is about a grant the caller holds, and the check that they hold one comes first", len(ops.wants))
			}
			if !wu9Names(wu9Stored(t, ops), "user:"+wu9Person) {
				t.Error("the refused offboarding took the person off the member's roster anyway")
			}
		})
	}
}

// TestOffboardMemberSweepsPrefix: the core acts on the member it is NAMED and
// on no other. The member travels as its GCP project and its Firebase project,
// which are one project only sometimes (F2, R10), and both halves of the act
// name the pair: the roster written is that member's grants node, and the sweep
// is that member's intake, asked for the person's own prefix and nothing else
// (R10, DS2.4).
//
// The member is what makes this core a core and not the single installation's
// command: a workspace admin's own config carries the WORKSPACE's credentials
// service URL, so a sweep that resolved an intake from it would delete the
// wrong installation's containers. That is the failure the address below
// pins, and it is why the member is an argument at all.
func TestOffboardMemberSweepsPrefix(t *testing.T) {
	o, ops, sweep := wu9Rig(t)

	if err := o.Offboard(context.Background(), wu9Member, wu9Person); err != nil {
		t.Fatalf("offboarding %s on %s: %v", wu9Person, wu9Member.Project, err)
	}
	if len(sweep.calls) != 1 {
		t.Fatalf("the core made %d sweeps, want one: the person's own prefix on the member's intake", len(sweep.calls))
	}
	got := sweep.calls[0]
	if got.m != wu9Member {
		t.Errorf("the sweep named %+v, want the member the caller named (%+v): the member's own intake is what holds the person's prefix, and the workspace's is another installation's", got.m, wu9Member)
	}
	if got.address != wu9Person {
		t.Errorf("the sweep named the owner %q, want the person's address (%s): the containers are found by the owner annotation and never by a name the caller guessed", got.address, wu9Person)
	}
	if len(ops.writes) != 1 {
		t.Fatalf("the core wrote the member's roster %d times, want once", len(ops.writes))
	}
	if ops.writes[0].m != wu9Member {
		t.Errorf("the write named %+v, want the member the caller named (%+v)", ops.writes[0].m, wu9Member)
	}
	if ops.writes[0].doc.ETag != "etag-1" {
		t.Errorf("the write carried the ETag %q, want the one the read answered: a write under any other precondition is a write over a picture nobody saw (F23)", ops.writes[0].doc.ETag)
	}
	grants := wu9Stored(t, ops)
	if wu9Names(grants, "user:"+wu9Person) {
		t.Errorf("%s still holds a grant of the roster: EVERY grant of the address goes, whatever scope it carries:\n%v", wu9Person, grants)
	}
	if !wu9Names(grants, "user:"+wu9Admin) {
		t.Errorf("the offboarding dropped the caller's own grant too: only the member named goes:\n%v", grants)
	}
	if len(grants) != 1 {
		t.Errorf("the roster holds %d grants, want the one the member did not hold:\n%v", len(grants), grants)
	}
}

// TestOffboardMemberWaitsForTheAdminList: a workspace admin's stored grant is
// a row of the member's roster, and the member's own `fugaro update` is what
// publishes it -- as the derived admin list the member's intake checks a
// shared-prefix writer against (D2818, D1940). Until it has, the core refuses
// and names the member whose update is owed, and NOTHING is done there: no
// write to that member's roster, no entry in its trail, no sweep of the
// person's prefix. The second half is the other side of the gate: the same
// call, with the grant on the member's list, goes through.
func TestOffboardMemberWaitsForTheAdminList(t *testing.T) {
	o, ops, sweep := wu9Rig(t)
	ops.pending = workspace.Pending{Admins: []string{wu9Admin}}

	err := o.Offboard(context.Background(), wu9Member, wu9Person)
	if err == nil {
		t.Fatalf("the core offboarded on a member whose published admin list does not carry the caller yet")
	}
	for _, want := range []string{wu9Member.Project, wu9Admin, "derived admin list", "`" + selfCommand() + " update`"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q, so an admin cannot tell which member's update is owed:\n%v", want, err)
		}
	}
	if len(ops.wants) != 1 {
		t.Fatalf("PendingApply was asked %d times, want once", len(ops.wants))
	}
	// The want is the admin list the member's own roster derives: the
	// comparison is "has this member published what its roster states", and a
	// second list written here would be a second reading of D1940.
	if got := ops.wants[0].Admins; len(got) != 1 || got[0] != wu9Admin {
		t.Errorf("the comparison was made against %v, want the roster's derived admin list ([%s])", got, wu9Admin)
	}
	if len(ops.writes) != 0 || len(ops.audits) != 0 || len(sweep.calls) != 0 {
		t.Errorf("the refused offboarding did something on the member anyway: %d writes, %d audit entries, %d sweeps — a member whose update is pending is left exactly as it was",
			len(ops.writes), len(ops.audits), len(sweep.calls))
	}
	if !wu9Names(wu9Stored(t, ops), "user:"+wu9Person) {
		t.Error("the refused offboarding took the person off the member's roster anyway")
	}

	// The other side: the member's admin list carries the caller, so the grant
	// has reached it and the act lands.
	ops.pending = workspace.Pending{}
	if err := o.Offboard(context.Background(), wu9Member, wu9Person); err != nil {
		t.Fatalf("the core refused a member whose published admin list carries the caller: %v", err)
	}
	if len(ops.writes) != 1 || len(sweep.calls) != 1 || len(ops.audits) != 1 {
		t.Errorf("the act did not land once the grant had reached the list: %d writes, %d sweeps, %d audit entries",
			len(ops.writes), len(sweep.calls), len(ops.audits))
	}
}

// TestOffboardMemberAudits: the act is created in the MEMBER's own trail, once
// and create-only (D2811) — a project admin holds no write on the workspace's
// project, which is where the workspace's own trail lives (F11, D2813) — and
// the entry is C1's own event, which is the document that trail holds and the
// shape every reader of it reads back (contract.md, "The audit event").
//
// Its name is a key of that node and not the person's address: a Realtime
// Database key carries no dot, and every address does, so the id is minted for
// the event rather than built from what it is about (C1's NewAuditEventID).
func TestOffboardMemberAudits(t *testing.T) {
	o, ops, _ := wu9Rig(t)

	if err := o.Offboard(context.Background(), wu9Member, wu9Person); err != nil {
		t.Fatalf("offboarding %s on %s: %v", wu9Person, wu9Member.Project, err)
	}
	if len(ops.audits) != 1 {
		t.Fatalf("the core created %d audit entries, want one (D2811)", len(ops.audits))
	}
	entry := ops.audits[0]
	if entry.m != wu9Member {
		t.Errorf("the entry was created on %+v, want the member the caller named (%+v): a change to a member is recorded in that member's own trail, because a project admin holds no write on the workspace's project (F11)", entry.m, wu9Member)
	}
	if entry.id == "" {
		t.Fatal("the entry was created under no name")
	}
	if strings.ContainsAny(entry.id, ".#$/[]") {
		t.Errorf("the entry's name %q carries a character the trail's node cannot hold a child under: the name is a key of a database node and never the address it is about", entry.id)
	}
	if strings.Contains(entry.id, wu9Person) {
		t.Errorf("the entry's name %q carries the person's address: an address has a dot, so a name built from it is one the node refuses or one that has to be escaped", entry.id)
	}
	// The entry is the event the member's trail holds, read back with the
	// reader that trail's readers use.
	ev, err := workspace.DecodeAuditEvent(entry.event)
	if err != nil {
		t.Fatalf("the entry is not an event this program reads back: %v\n%s", err, entry.event)
	}
	if ev.ID != entry.id {
		t.Errorf("the entry is created under %q and its own id is %q: the id is the trail's key", entry.id, ev.ID)
	}
	if ev.Action != workspace.ActionUserOffboard {
		t.Errorf("the entry names the act %q, want %q", ev.Action, workspace.ActionUserOffboard)
	}
	if ev.Actor != wu9Admin {
		t.Errorf("the entry names the actor %q, want the caller the roster admitted (%q)", ev.Actor, wu9Admin)
	}
	if ev.Project != wu9Member.Project {
		t.Errorf("the entry names the member %q, want the project it was on (%q)", ev.Project, wu9Member.Project)
	}
	if ev.Subject != wu9Person {
		t.Errorf("the entry names the person %q, want them as the address the trail spells (%q)", ev.Subject, wu9Person)
	}
	if !ev.Time.Equal(wu9At) {
		t.Errorf("the entry is stamped %s, want %s", ev.Time, wu9At)
	}
}

// wu10Members is the member seam offboardWorkspacePerson drives: it records
// every member it was asked to offboard, in the order it was asked, and refuses
// the ones the test seeded. The rest of C2's methods are wu9FakeMemberOps's --
// the helper calls none of them -- and its own Offboard, which fails a test, is
// overridden here, because this helper's whole job is to be its caller.
type wu10Members struct {
	*wu9FakeMemberOps
	trace *[]string
	// errs is the failure a named member's offboarding answers, by project.
	errs map[string]error
}

func (m *wu10Members) Offboard(_ context.Context, target workspace.MemberTarget, _ string) error {
	*m.trace = append(*m.trace, "member "+target.Project)
	return m.errs[target.Project]
}

// wu10Sweep is the WORKSPACE's own intake (F21), recording where in the order
// it was reached: the point of the test below is that it is reached at all, and
// last.
type wu10Sweep struct {
	trace   *[]string
	deleted []string
	err     error
	calls   []string
}

func (s *wu10Sweep) Sweep(_ context.Context, address string) ([]string, error) {
	*s.trace = append(*s.trace, "workspace "+address)
	s.calls = append(s.calls, address)
	if s.err != nil {
		return nil, s.err
	}
	return s.deleted, nil
}

// TestTheSweepRunsWhenAMemberRefused: the workspace's own intake is swept
// whatever the members answered (F21). A person's token is stored ONCE, at the
// workspace (D2960), so it is the workspace's intake and no member's that holds
// it: a member whose own update is still pending is a refusal this command
// reports the person rather than half-removing them on, and a sweep skipped
// there would leave the token behind exactly when the person most needs it gone.
//
// Every member is attempted, so one refusal does not leave the members after it
// unswept; the FIRST refusal is the one reported, with the class it arrived in
// (the wrap is `%w`); and the workspace's own answer is returned beside it,
// because the containers that came back are the report's.
func TestTheSweepRunsWhenAMemberRefused(t *testing.T) {
	const wu10Person = "member@example.com"
	trace := &[]string{}
	members := &wu10Members{
		wu9FakeMemberOps: &wu9FakeMemberOps{t: t},
		trace:            trace,
		errs: map[string]error{
			"aurora": remote(fmt.Errorf("%s has not applied %s's stored grant yet: its own `%s update` is what is pending", "aurora", wu10Person, selfCommand())),
		},
	}
	sweep := &wu10Sweep{trace: trace, deleted: []string{"fugaro_u_4242_github-token"}}
	linked := []workspace.MemberTarget{{Project: "aurora"}, {Project: "borealis"}}

	deleted, memberErr, sweepErr := offboardWorkspacePerson(context.Background(), members, sweep, linked, wu10Person)

	if memberErr == nil {
		t.Fatal("a member that refused was reported as a clean removal")
	}
	for _, want := range []string{"aurora", wu10Person, "update`"} {
		if !strings.Contains(memberErr.Error(), want) {
			t.Errorf("the member's refusal does not name %q, so the person cannot tell which member is owed an update:\n%v", want, memberErr)
		}
	}
	if got := ExitCode(memberErr); got != ExitRemoteError {
		t.Errorf("the refusal exits %d, want %d: the member's own class survives the wrap that names it (F23)", got, ExitRemoteError)
	}
	if sweepErr != nil {
		t.Fatalf("the workspace's own intake was reported as failed: %v", sweepErr)
	}
	if len(sweep.calls) != 1 || sweep.calls[0] != wu10Person {
		t.Errorf("the workspace's own intake was swept %v, want one call for %s even though a member refused", sweep.calls, wu10Person)
	}
	if len(deleted) != 1 || deleted[0] != "fugaro_u_4242_github-token" {
		t.Errorf("the workspace's own answer is %v, want the container it deleted, which is what the report names", deleted)
	}
	// Every member is attempted, and the workspace's own intake comes AFTER all of
	// them: its containers are what the members' sweeps cannot reach (D2960), and
	// the order is the one the acceptance states.
	want := []string{"member aurora", "member borealis", "workspace " + wu10Person}
	if !slices.Equal(*trace, want) {
		t.Errorf("the sweeps were made in the order %v, want %v: every member first, whatever they answered, and the workspace's own intake last", *trace, want)
	}
}
