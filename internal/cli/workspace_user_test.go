package cli

// WU7's tests: `workspace user add|rm|ls` and `workspace domains add|rm|ls`.
//
// Every fake and helper here carries the task's id (wu7…), so the level-2 tasks
// that land in this package later merge without a name clash (plan §1). The
// fakes are this file's own and never the contract's stubs: each is the
// contract's shape with the one answer a test needs, and none of them writes a
// database, a folder or a policy.
//
// What the fakes are for is the two properties the acceptance turns on and the
// real sides cannot show a test: that a change reaches the workspace's roster
// node (the store records every write and its revision), and that the same
// command reaches, or does not reach, the members and the authority beside it
// (wu7Members and wu7Authority record every call).

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/spf13/cobra"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// wu7Now is the instant every event of this file is made at: a test that reads
// an event's id or time back reads the same value on every run.
func wu7Now() time.Time { return time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC) }

// wu7Rand is the randomness NewAuditEventID ends an id with: a counter, so two
// events made in one second are two ids — the property the create-only trail
// rests on — and a test reads the same ids on every run.
type wu7Rand struct{ n byte }

func (r *wu7Rand) Read(p []byte) (int, error) {
	for i := range p {
		r.n++
		p[i] = r.n
	}
	return len(p), nil
}

// wu7Store is one workspace's roster node in memory: the bytes and the revision
// a conditional write is made under, exactly as C1's contract hands them back.
// busy makes the next write answer rtdb.ErrPrecondition, which is the ANSWER and
// not a failure — the caller reads again — so a test can drive the retry.
type wu7Store struct {
	t     *testing.T
	raw   []byte
	etag  string
	found bool
	busy  int

	writes int
}

func (s *wu7Store) Path() string { return "workspaceRosters/workspace-alpha" }

func (s *wu7Store) Read(context.Context) (workspace.Revision, []byte, error) {
	return workspace.Revision{ETag: s.etag, Found: s.found}, append([]byte(nil), s.raw...), nil
}

func (s *wu7Store) Write(_ context.Context, rev workspace.Revision, raw []byte) error {
	s.writes++
	if s.busy > 0 {
		s.busy--
		return fmt.Errorf("writing %s: %w", s.Path(), rtdb.ErrPrecondition)
	}
	if rev.ETag != s.etag || rev.Found != s.found {
		s.t.Errorf("the write was made under the revision %+v, and the node carries %q (found %v): a change is judged against the bytes that are really there", rev, s.etag, s.found)
	}
	if _, err := workspace.DecodeRoster(raw); err != nil {
		s.t.Errorf("the command wrote a roster this package's own reader refuses: %v\n%s", err, raw)
	}
	s.raw, s.etag, s.found = append([]byte(nil), raw...), fmt.Sprintf("etag-%d", s.writes), true
	return nil
}

func (s *wu7Store) roster() *workspace.Roster {
	s.t.Helper()
	r, err := workspace.DecodeRoster(s.raw)
	if err != nil {
		s.t.Fatalf("the roster the store holds does not decode: %v\n%s", err, s.raw)
	}
	return r
}

// wu7Audit is the workspace's trail: create-only, and it refuses an id a second
// time, which is the one rule D2811's writer is built on.
type wu7Audit struct {
	t      *testing.T
	ids    map[string]bool
	events []workspace.AuditEvent
}

func (a *wu7Audit) Create(_ context.Context, id string, event []byte) error {
	if a.ids[id] {
		return fmt.Errorf("the audit event %s is already there", id)
	}
	if a.ids == nil {
		a.ids = map[string]bool{}
	}
	e, err := workspace.DecodeAuditEvent(event)
	if err != nil {
		a.t.Errorf("the command filed an event this package's own reader refuses: %v\n%s", err, event)
		return nil
	}
	if e.ID != id {
		a.t.Errorf("the event was created under %s and carries the id %s", id, e.ID)
	}
	a.ids[id] = true
	a.events = append(a.events, e)
	return nil
}

// one is the single event of an action, and it fails the test when the trail
// carries none or several: a change files one entry, and a test that read the
// first of two would pass on a command that filed the same change twice.
func (a *wu7Audit) one(t *testing.T, action workspace.Action) workspace.AuditEvent {
	t.Helper()
	var found []workspace.AuditEvent
	for _, e := range a.events {
		if e.Action == action {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("the trail carries %d %s events, want one: %+v", len(found), action, a.events)
	}
	return found[0]
}

// actions is every action the trail carries, in the order it was written.
func (a *wu7Audit) actions() []workspace.Action {
	out := make([]workspace.Action, 0, len(a.events))
	for _, e := range a.events {
		out = append(out, e.Action)
	}
	return out
}

// wu7Members is the member-operations contract over members held in memory: one
// grants document per member project, and a record of every call. Offboard drops
// the person's own grants from the member's document, which is what C2 says it
// does (their stored grants dropped), so a test can see that a removal was
// queued at that member's next update.
type wu7Members struct {
	t      *testing.T
	grants map[string][]accesslib.Grant
	etag   map[string]string

	calls      []string
	offboarded []string
	audited    []string
	writeErr   error
	offErr     error
}

func (m *wu7Members) call(what, project string) { m.calls = append(m.calls, project+" "+what) }

func (m *wu7Members) doc(project string) (workspace.MemberDoc, error) {
	raw, err := accesslib.Encode(m.grants[project])
	if err != nil {
		return workspace.MemberDoc{}, err
	}
	etag, ok := m.etag[project]
	if !ok {
		etag = "member-etag"
	}
	return workspace.MemberDoc{Raw: raw, ETag: etag, Found: true}, nil
}

func (m *wu7Members) ReadGrants(_ context.Context, t workspace.MemberTarget) (workspace.MemberDoc, error) {
	m.call("read-grants", t.Project)
	return m.doc(t.Project)
}

func (m *wu7Members) WriteGrants(_ context.Context, t workspace.MemberTarget, doc workspace.MemberDoc, raw []byte) error {
	m.call("write-grants", t.Project)
	if m.writeErr != nil {
		return m.writeErr
	}
	if doc.ETag != m.etagOr(t.Project) {
		m.t.Errorf("%s: the write was made under %q, and the node carries %q", t.Project, doc.ETag, m.etagOr(t.Project))
	}
	grants, err := accesslib.Decode(raw)
	if err != nil {
		m.t.Errorf("%s: the command wrote a grants document this package's own reader refuses: %v\n%s", t.Project, err, raw)
		return nil
	}
	m.grants[t.Project] = grants
	m.etag[t.Project] = "member-etag-2"
	return nil
}

func (m *wu7Members) etagOr(project string) string {
	if e, ok := m.etag[project]; ok {
		return e
	}
	return "member-etag"
}

func (m *wu7Members) ReadMirror(context.Context, workspace.MemberTarget) (workspace.MemberDoc, error) {
	return workspace.MemberDoc{}, nil
}

func (m *wu7Members) WriteMirror(context.Context, workspace.MemberTarget, workspace.MemberDoc, []byte) error {
	m.t.Error("the mirror was written: WU7 writes the roster node and the members' own grants, and the mirror is D2813's own propagation")
	return nil
}

func (m *wu7Members) Bindings(context.Context, workspace.MemberTarget) ([]workspace.MemberBinding, error) {
	return nil, nil
}

func (m *wu7Members) PendingApply(context.Context, workspace.MemberTarget, workspace.ApplyWant) (workspace.Pending, error) {
	return workspace.Pending{}, nil
}

func (m *wu7Members) Offboard(_ context.Context, t workspace.MemberTarget, address string) error {
	m.call("offboard", t.Project)
	if m.offErr != nil {
		return m.offErr
	}
	m.offboarded = append(m.offboarded, t.Project+" "+address)
	member, err := accesslib.Member(address)
	if err != nil {
		m.t.Fatalf("the address the command offboarded is not one a grant can name: %v", err)
	}
	kept := make([]accesslib.Grant, 0, len(m.grants[t.Project]))
	for _, g := range m.grants[t.Project] {
		if g.User != member {
			kept = append(kept, g)
		}
	}
	m.grants[t.Project] = kept
	return nil
}

func (m *wu7Members) Audit(_ context.Context, t workspace.MemberTarget, id string, _ []byte) error {
	m.call("audit", t.Project)
	m.audited = append(m.audited, t.Project+" "+id)
	return nil
}

// grantsOf is the member's own roster as it stands: what the workspace wrote
// there, and what a member's next `fugaro update` reads.
func (m *wu7Members) grantsOf(project string) []accesslib.Grant { return m.grants[project] }

// wu7Authority is the workspace's mechanism (C2's Authority) as a test holds it:
// the live bindings it lists, and a record of what was given and taken away.
type wu7Authority struct {
	kind  workspace.AuthorityKind
	bound []string

	added   []string
	removed []string
	holds   map[string]bool
	addErr  error
}

func (a *wu7Authority) Kind() workspace.AuthorityKind { return a.kind }

func (a *wu7Authority) Link(context.Context, workspace.MemberTarget) error   { return nil }
func (a *wu7Authority) Unlink(context.Context, workspace.MemberTarget) error { return nil }

func (a *wu7Authority) Holds(_ context.Context, t workspace.MemberTarget) (bool, error) {
	return a.holds[t.Project], nil
}

func (a *wu7Authority) AddAdmin(_ context.Context, admin string) error {
	if a.addErr != nil {
		return a.addErr
	}
	a.added = append(a.added, admin)
	a.bound = append(a.bound, "user:"+admin)
	return nil
}

func (a *wu7Authority) RemoveAdmin(_ context.Context, admin string) error {
	a.removed = append(a.removed, admin)
	kept := make([]string, 0, len(a.bound))
	for _, b := range a.bound {
		if b != "user:"+admin {
			kept = append(kept, b)
		}
	}
	a.bound = kept
	return nil
}

func (a *wu7Authority) AdminBindings(context.Context) ([]string, error) {
	return append([]string(nil), a.bound...), nil
}

// wu7Sweep is the WORKSPACE's own intake as `rm` reaches it (F21): the calls it
// made, the containers the delete answered it removed, and the failure a test
// seeded. It is a fake of its own and not a member's: the workspace's own
// project is the installation a person's token is stored at once (D2960), and a
// command that swept the wrong one would be invisible if both were one type.
type wu7Sweep struct {
	deleted []string
	err     error
	calls   []string
}

func (s *wu7Sweep) Sweep(_ context.Context, address string) ([]string, error) {
	s.calls = append(s.calls, address)
	if s.err != nil {
		return nil, s.err
	}
	return s.deleted, nil
}

// wu7World is one test's workspace: the roster the store holds, the fakes beside
// it, and the caller every command of the test runs as.
type wu7World struct {
	t *testing.T
	// now and rand are the two seams a command takes for the event id it mints.
	// rand is an io.Reader and not a *wu7Rand, so a test can leave the seam nil
	// and reach the fallback the deps document (a typed nil in an interface is
	// not nil, and would panic in Read rather than fall back).
	now  func() time.Time
	rand io.Reader

	store     *wu7Store
	audit     *wu7Audit
	members   *wu7Members
	authority *wu7Authority
	// sweep is the workspace's OWN intake (F21), which `rm` reaches beside the
	// members'.
	sweep  *wu7Sweep
	linked []workspace.MemberTarget
	// linkedCalls counts the resolutions of the member list. The list is a
	// loader and not a value (A7: resolving it reads the cloud), so what a
	// command asked for is as much a part of the fixture as what it wrote.
	linkedCalls int

	// caller is the verified address userCallerLookup answers, in the
	// "user:<address>" form access.Caller builds. empty is a session with no
	// credential at all.
	caller string
	// marker is what the environment answers for a coding agent's markers: the
	// empty string is a person's own terminal.
	marker string
}

// wu7New is a workspace whose roster holds r. The fixture writes every list the
// way the node holds it — sorted, each entry once — so a command that appends
// out of order is refused by C1's own reader rather than by an assertion here.
func wu7New(t *testing.T, r workspace.Roster) *wu7World {
	t.Helper()
	r.Version = workspace.RosterVersion
	if r.Mechanism == "" {
		r.Mechanism = workspace.MechanismFolder
	}
	if r.FolderID == "" {
		r.FolderID = "folders/123456"
	}
	wu7SortRoster(&r)
	raw, err := workspace.EncodeRoster(&r)
	if err != nil {
		t.Fatalf("the fixture's roster is not one this package writes: %v", err)
	}
	return &wu7World{
		t: t, now: wu7Now, rand: &wu7Rand{},
		store: &wu7Store{t: t, raw: raw, etag: "etag-0", found: true},
		audit: &wu7Audit{t: t, ids: map[string]bool{}},
		members: &wu7Members{t: t,
			grants: map[string][]accesslib.Grant{}, etag: map[string]string{}},
		authority: &wu7Authority{kind: workspace.AuthorityFolder, holds: map[string]bool{}},
		sweep:     &wu7Sweep{deleted: []string{"fugaro_u_4242_github-token"}},
	}
}

// wu7SortRoster writes the fixture's lists the way the node holds them. The
// order is the node's own rule (C1's Validate), restated for the fixture so a
// hand-written roster does not have to be sorted by hand — and a command that
// appends an entry out of order is refused by the store's own read rather than
// by an assertion here.
func wu7SortRoster(r *workspace.Roster) {
	slices.SortFunc(r.People, func(a, b workspace.Person) int { return strings.Compare(a.Address, b.Address) })
	for i := range r.People {
		roles := r.People[i].Roles
		slices.SortFunc(roles, func(a, b workspace.ProjectRole) int { return strings.Compare(a.Project, b.Project) })
	}
	slices.Sort(r.Admins)
	slices.Sort(r.Domains)
	slices.SortFunc(r.Grants, func(a, b workspace.StoredGrant) int {
		return cmp.Or(strings.Compare(a.Project, b.Project), strings.Compare(a.User, b.User))
	})
}

// wu7Run builds both command trees over w and runs one argv. The commands are
// built here and not through root.go, because the change that registers them in
// the workspace family is I1's: this file drives the constructors I1 will wrap.
func (w *wu7World) run(args ...string) (string, error) {
	w.t.Helper()
	old := userCallerLookup
	userCallerLookup = func(context.Context) (string, error) { return w.caller, nil }
	w.t.Cleanup(func() { userCallerLookup = old })

	open := func(context.Context, string) (workspaceRosterDeps, error) {
		return workspaceRosterDeps{
			Workspace: "workspace-alpha",
			Store:     w.store,
			Audit:     w.audit,
			Members:   w.members,
			Authority: w.authority,
			Sweep:     w.sweep,
			Linked: func(context.Context) ([]workspace.MemberTarget, error) {
				w.linkedCalls++
				return w.linked, nil
			},
			getenv: func(name string) string {
				if w.marker != "" && name == agentMarkers[0] {
					return w.marker
				}
				return ""
			},
			now:  w.now,
			rand: w.rand,
		}, nil
	}
	root := &cobra.Command{Use: "fugaro", SilenceUsage: true, SilenceErrors: true}
	// Both are subcommands of `workspace`: the change that registers them in the
	// family is I1's, and this stands in for it.
	parent := &cobra.Command{Use: "workspace", SilenceUsage: true, SilenceErrors: true}
	parent.AddCommand(newWorkspaceUserCmd(open), newWorkspaceDomainsCmd(open))
	root.AddCommand(parent)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

// must runs one argv and fails the test when it refuses.
func (w *wu7World) must(args ...string) string {
	w.t.Helper()
	out, err := w.run(args...)
	if err != nil {
		w.t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return out
}

// refuses runs one argv and fails the test unless it refuses with every want in
// the sentence.
func (w *wu7World) refuses(want []string, args ...string) string {
	w.t.Helper()
	out, err := w.run(args...)
	if err == nil {
		w.t.Fatalf("%v was allowed:\n%s", args, out)
	}
	for _, s := range want {
		if !strings.Contains(err.Error(), s) {
			w.t.Errorf("%v: the refusal %q does not name %q", args, err, s)
		}
	}
	return err.Error()
}

// nothingWasWritten is the state a refused write leaves: no node write, no
// event, no member call and no binding made or dropped.
func (w *wu7World) nothingWasWritten(what string) {
	w.t.Helper()
	if w.store.writes != 0 {
		w.t.Errorf("%s: the roster node was written %d times", what, w.store.writes)
	}
	if len(w.audit.events) != 0 {
		w.t.Errorf("%s: the trail carries %+v", what, w.audit.actions())
	}
	if len(w.members.calls) != 0 {
		w.t.Errorf("%s: the members were reached: %v", what, w.members.calls)
	}
	if len(w.authority.added) != 0 || len(w.authority.removed) != 0 {
		w.t.Errorf("%s: the authority was changed: added %v, removed %v", what, w.authority.added, w.authority.removed)
	}
}

func (w *wu7World) person(address string) workspace.Person {
	w.t.Helper()
	for _, p := range w.store.roster().People {
		if p.Address == address {
			return p
		}
	}
	w.t.Fatalf("the roster names no %s: %+v", address, w.store.roster().People)
	return workspace.Person{}
}

func (w *wu7World) hasPerson(address string) bool {
	for _, p := range w.store.roster().People {
		if p.Address == address {
			return true
		}
	}
	return false
}

// wu7Admin is a world whose caller is the workspace's own bound admin: what every
// write test starts from, so that the gate is not the thing each of them tests.
func wu7Admin(t *testing.T, r workspace.Roster) *wu7World {
	t.Helper()
	w := wu7New(t, r)
	w.caller = "user:founder@example.com"
	w.authority.bound = []string{"user:founder@example.com"}
	return w
}

// TestUserAddNeedsExternal: an address outside the workspace's allowed domains is
// admitted only with --external, and the mark it is admitted under is the one the
// flag states (D2812). The same test holds the other half of that rule — the mark
// is not a field an admin can set about somebody the domains already admit — and
// the fence every write of this family carries: a coding agent's session writes
// nothing (A7).
func TestUserAddNeedsExternal(t *testing.T) {
	w := wu7Admin(t, workspace.Roster{
		People:  []workspace.Person{{Address: "founder@example.com", Kind: workspace.KindHuman}},
		Admins:  []string{"founder@example.com"},
		Domains: []string{"example.com"},
	})

	err := w.refuses([]string{"--external", "ada@other.test", "example.com"},
		"workspace", "user", "add", "ada@other.test", "--workspace", "workspace-alpha")
	w.nothingWasWritten("adding an address outside the domains without --external")
	if strings.Contains(err, "are you sure") {
		t.Errorf("the refusal asks a question: %v", err)
	}

	w.must("workspace", "user", "add", "ada@other.test", "--external", "--workspace", "workspace-alpha")
	if p := w.person("ada@other.test"); p.Kind != workspace.KindHuman || !p.External {
		t.Errorf("the person added with --external is %+v, want a human marked external", p)
	}
	event := w.audit.one(t, workspace.ActionUserAdd)
	if event.Subject != "ada@other.test" || event.Actor != "founder@example.com" {
		t.Errorf("the event is %+v, want ada@other.test added by the caller", event)
	}
	if event.Time.IsZero() || event.Version != workspace.AuditEventVersion {
		t.Errorf("the event is %+v, want the build's own version and the instant it was made at", event)
	}

	// An address the domains already admit is not external, and --external is
	// refused rather than stored: the mark is a statement about a domain the
	// workspace does not list, and drift computes it from Domains, so a person
	// marked external inside a listed domain is a difference the next drift
	// check reports as tampering.
	w.refuses([]string{"--external", "example.com"},
		"workspace", "user", "add", "grace@example.com", "--external", "--workspace", "workspace-alpha")
	if w.hasPerson("grace@example.com") {
		t.Error("the refused add wrote the person")
	}

	// An address no credential could carry is refused before anything is read.
	w.refuses([]string{"not an address"}, "workspace", "user", "add", "not-an-address", "--external", "--workspace", "workspace-alpha")

	// A coding agent's session writes nothing (A7), and the refusal says where
	// to run the command instead.
	before := w.store.writes
	w.marker = "1"
	err = w.refuses([]string{"coding agent's session", "run this in your own terminal"},
		"workspace", "user", "add", "bob@other.test", "--external", "--workspace", "workspace-alpha")
	if w.store.writes != before || w.hasPerson("bob@other.test") {
		t.Errorf("a coding agent's session wrote the roster: %v", err)
	}

	// The node is written under the revision it was read at, and a node another
	// writer moved under this one is answered by reading again (C1's contract):
	// the command retries rather than overwriting what it did not see.
	w.marker = ""
	w.store.busy = 1
	w.must("workspace", "user", "add", "carol@other.test", "--external", "--workspace", "workspace-alpha")
	if !w.hasPerson("carol@other.test") {
		t.Error("the add that lost the race was not remade against the bytes that are really there")
	}
}

// TestUserAddOnlyWorkspaceAdmins: adding a member is a workspace admin's act, and
// an admin is the INTERSECTION of the roster's own list and the live binding
// (D2823) — the accounts that can rewrite the list cannot set a binding, so an
// entry no binding backs is trusted by nothing.
func TestUserAddOnlyWorkspaceAdmins(t *testing.T) {
	w := wu7New(t, workspace.Roster{
		People: []workspace.Person{
			{Address: "ada@example.com", Kind: workspace.KindHuman},
			{Address: "founder@example.com", Kind: workspace.KindHuman},
		},
		Admins:  []string{"founder@example.com"},
		Domains: []string{"example.com"},
	})
	w.authority.bound = []string{"user:founder@example.com"}

	// A person of the roster who is not an admin.
	w.caller = "user:ada@example.com"
	w.refuses([]string{"ada@example.com", "workspace admin"},
		"workspace", "user", "add", "grace@example.com", "--workspace", "workspace-alpha")
	w.nothingWasWritten("an add by a person the roster does not list as an admin")

	// A caller the roster does not name at all.
	w.caller = "user:mallory@example.com"
	w.refuses([]string{"mallory@example.com", "workspace admin"},
		"workspace", "user", "add", "grace@example.com", "--workspace", "workspace-alpha")
	w.nothingWasWritten("an add by somebody who is not a member")

	// An entry of the admin list that no live binding backs: the list was
	// rewritten by somebody who could not set the binding (D2823).
	w.caller = "user:founder@example.com"
	w.authority.bound = []string{"user:somebody-else@example.com"}
	w.refuses([]string{"founder@example.com", "binding"},
		"workspace", "user", "add", "grace@example.com", "--workspace", "workspace-alpha")
	w.nothingWasWritten("an add by an admin entry no live binding backs")

	// The bound admin's own add lands, and it reaches no member: a person is a
	// member of the workspace, and what they may do in a member is that
	// member's own roster and `workspace user grant` (WU8).
	w.authority.bound = []string{"user:founder@example.com"}
	w.must("workspace", "user", "add", "grace@example.com", "--workspace", "workspace-alpha")
	if p := w.person("grace@example.com"); p.External {
		t.Errorf("a person inside the allowed domain is marked %+v, want the mark false", p)
	}
	if len(w.members.calls) != 0 || len(w.authority.added) != 0 {
		t.Errorf("adding a member reached the members or the authority: %v, %v", w.members.calls, w.authority.added)
	}
}

// TestUserAddMachineWritesNoMember: a machine is recorded in the workspace's node
// with the roles the workspace expects it to hold, and nowhere else (D2806) —
// the member roster accepts only `user:` members, so a machine's real IAM stays
// where it is and the record is what drift and audit compare against.
func TestUserAddMachineWritesNoMember(t *testing.T) {
	w := wu7Admin(t, workspace.Roster{
		People:  []workspace.Person{{Address: "founder@example.com", Kind: workspace.KindHuman}},
		Admins:  []string{"founder@example.com"},
		Domains: []string{"example.com"},
	})
	w.linked = []workspace.MemberTarget{{Project: "aurora"}, {Project: "borealis"}}

	w.must("workspace", "user", "add", "build@ci.example.com", "--kind", "sa",
		"--role", "borealis=admin", "--role", "aurora=viewer", "--workspace", "workspace-alpha")
	machine := w.person("build@ci.example.com")
	if machine.Kind != workspace.KindSA {
		t.Fatalf("the machine is %+v, want kind sa", machine)
	}
	want := []workspace.ProjectRole{
		{Project: "aurora", Role: workspace.RoleViewer},
		{Project: "borealis", Role: workspace.RoleAdmin},
	}
	if len(machine.Roles) != len(want) {
		t.Fatalf("the machine's roles are %+v, want %+v", machine.Roles, want)
	}
	for i, r := range want {
		if machine.Roles[i] != r {
			t.Errorf("role %d is %+v, want %+v", i, machine.Roles[i], r)
		}
	}
	if len(w.members.calls) != 0 {
		t.Errorf("recording a machine reached the members: %v", w.members.calls)
	}
	if len(w.authority.added) != 0 {
		t.Errorf("recording a machine bound an admin: %v", w.authority.added)
	}
	if event := w.audit.one(t, workspace.ActionUserAdd); event.Subject != "build@ci.example.com" {
		t.Errorf("the event is %+v, want the machine's own address", event)
	}

	// The three rules a machine's shape carries: it comes from no domain, so
	// --external is refused; it is never a workspace admin; and its roles are
	// its own, so a human carries none.
	w.refuses([]string{"--external", "machine"},
		"workspace", "user", "add", "ci@example.com", "--kind", "sa", "--external", "--workspace", "workspace-alpha")
	w.refuses([]string{"--admin", "machine"},
		"workspace", "user", "add", "ci@example.com", "--kind", "sa", "--admin", "--workspace", "workspace-alpha")
	w.refuses([]string{"--role", "human"},
		"workspace", "user", "add", "grace@example.com", "--role", "aurora=viewer", "--workspace", "workspace-alpha")
	w.refuses([]string{"ci", "role"},
		"workspace", "user", "add", "ci@example.com", "--kind", "sa", "--role", "ci=owner", "--workspace", "workspace-alpha")
	w.refuses([]string{"PROJECT=ROLE"},
		"workspace", "user", "add", "ci@example.com", "--kind", "sa", "--role", "aurora", "--workspace", "workspace-alpha")
	// The two refusals above are the shape's, and neither wrote anything.
	if w.hasPerson("ci@example.com") || w.hasPerson("grace@example.com") {
		t.Error("a refused add wrote a person")
	}
}

// TestUserRmOffboardsEveryMember: removing a person from the workspace sweeps
// them out of every member the workspace holds (C2's Offboard, which is WU9's
// core), writes the node without them, and files the removal.
func TestUserRmOffboardsEveryMember(t *testing.T) {
	w := wu7Admin(t, workspace.Roster{
		People: []workspace.Person{
			{Address: "ada@example.com", Kind: workspace.KindHuman},
			{Address: "build@ci.example.com", Kind: workspace.KindSA, Roles: []workspace.ProjectRole{{Project: "aurora", Role: workspace.RoleViewer}}},
			{Address: "founder@example.com", Kind: workspace.KindHuman},
		},
		Admins:  []string{"founder@example.com"},
		Domains: []string{"example.com"},
	})
	w.linked = []workspace.MemberTarget{{Project: "aurora"}, {Project: "borealis"}}
	w.members.grants["aurora"] = []accesslib.Grant{
		{User: "user:ada@example.com", Role: accesslib.RoleOperator, Scope: accesslib.Scope{Kind: accesslib.ScopeProject}},
	}
	w.members.grants["borealis"] = []accesslib.Grant{
		{User: "user:ada@example.com", Role: accesslib.RoleViewer, Scope: accesslib.Scope{Kind: accesslib.ScopeProject}},
	}

	w.must("workspace", "user", "rm", "ada@example.com", "--workspace", "workspace-alpha")
	want := []string{"aurora ada@example.com", "borealis ada@example.com"}
	if strings.Join(w.members.offboarded, "|") != strings.Join(want, "|") {
		t.Errorf("the members offboarded are %v, want %v", w.members.offboarded, want)
	}
	for _, project := range []string{"aurora", "borealis"} {
		for _, g := range w.members.grantsOf(project) {
			if g.User == "user:ada@example.com" {
				t.Errorf("%s still holds a grant for ada@example.com: %+v", project, g)
			}
		}
	}
	if w.hasPerson("ada@example.com") {
		t.Error("the roster still names the person taken off it")
	}
	if len(w.authority.removed) != 0 {
		t.Errorf("a member who is not a workspace admin had bindings dropped: %v", w.authority.removed)
	}
	if event := w.audit.one(t, workspace.ActionUserRemove); event.Subject != "ada@example.com" {
		t.Errorf("the event is %+v, want the removal of ada@example.com", event)
	}

	// A machine is offboarded from nowhere: it holds no grant in a member's own
	// roster (D2806), so `rm` takes the node's record and reaches no member.
	before := len(w.members.calls)
	w.must("workspace", "user", "rm", "build@ci.example.com", "--workspace", "workspace-alpha")
	if len(w.members.calls) != before {
		t.Errorf("removing a machine reached the members: %v", w.members.calls[before:])
	}
	if w.hasPerson("build@ci.example.com") {
		t.Error("the machine taken off the roster is still in it")
	}

	// An address nobody holds, and a workspace admin removed without --admin:
	// the second is a refusal rather than a half-removal, because an Admins
	// entry has to name a person of the roster (C1's own rule), so a command
	// that dropped the person and left the entry would write a node its own
	// reader refuses.
	w.refuses([]string{"nobody@example.com"}, "workspace", "user", "rm", "nobody@example.com", "--workspace", "workspace-alpha")
	w.refuses([]string{"--admin", "founder@example.com"},
		"workspace", "user", "rm", "founder@example.com", "--workspace", "workspace-alpha")
	if !w.hasPerson("founder@example.com") {
		t.Error("the refused removal wrote the roster")
	}
}

// TestUserRmSweepsTheWorkspacesIntake: removing a person from the workspace
// sweeps TWO installations (F21, D2960) — every member the workspace holds, and
// then the workspace's OWN project, whose intake holds the token they stored
// once — and the report says the thing the sweep does not do: the REMOVAL halts
// a run in flight on the person's token within a minute, because the workspace's
// service re-reads the roster and refuses every call under a slip whose owner has
// left it (R3, D3146).
func TestUserRmSweepsTheWorkspacesIntake(t *testing.T) {
	w := wu7Admin(t, workspace.Roster{
		People: []workspace.Person{
			{Address: "ada@example.com", Kind: workspace.KindHuman},
			{Address: "build@ci.example.com", Kind: workspace.KindSA, Roles: []workspace.ProjectRole{{Project: "aurora", Role: workspace.RoleViewer}}},
			{Address: "founder@example.com", Kind: workspace.KindHuman},
		},
		Admins:  []string{"founder@example.com"},
		Domains: []string{"example.com"},
	})
	w.linked = []workspace.MemberTarget{{Project: "aurora"}, {Project: "borealis"}}
	w.sweep.deleted = []string{"fugaro_u_4242_github-token"}

	out := w.must("workspace", "user", "rm", "ada@example.com", "--workspace", "workspace-alpha")

	// The workspace's own intake is asked once, for the person's own address — not
	// a member's, and not a name the caller guessed.
	if len(w.sweep.calls) != 1 || w.sweep.calls[0] != "ada@example.com" {
		t.Errorf("the workspace's own intake was swept %v, want one call for ada@example.com", w.sweep.calls)
	}
	// The members are swept as they always were, and the workspace's own intake is
	// the sweep beside them.
	want := []string{"aurora ada@example.com", "borealis ada@example.com"}
	if strings.Join(w.members.offboarded, "|") != strings.Join(want, "|") {
		t.Errorf("the members offboarded are %v, want %v", w.members.offboarded, want)
	}
	// The report says the run halts within a minute (R3), and names what the
	// workspace's own intake removed.
	for _, want := range []string{"removed ada@example.com from workspace workspace-alpha", "halts within a minute", "R3", "fugaro_u_4242_github-token"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}

	// A MACHINE holds nothing at the workspace (D2806): removing one reaches no
	// member and sweeps no intake, and the report carries no halting line.
	before := len(w.sweep.calls)
	machine := w.must("workspace", "user", "rm", "build@ci.example.com", "--workspace", "workspace-alpha")
	if len(w.sweep.calls) != before {
		t.Errorf("removing a machine swept the workspace's intake: %v", w.sweep.calls[before:])
	}
	if strings.Contains(machine, "halts within a minute") {
		t.Errorf("the report of a machine's removal says a run halts within a minute:\n%s", machine)
	}
}

// TestTheWorkspaceSweepsAnswerSurvivesARefusal: the sweep of the workspace's own
// intake runs BEFORE the steps that can end the command (F21, D2960), so no later
// answer may take it down with it. A member's refusal is the one path that reports
// the sweep and stops, and both of its facts survive there: what the sweep removed
// is said, because the person's token at the workspace is gone whether or not the
// roster was written, and a sweep that failed is returned beside the refusal
// rather than behind it -- a person who reads only the refusal would re-run the
// command on containers that are still there, or believe a removal that did not
// happen had swept them.
func TestTheWorkspaceSweepsAnswerSurvivesARefusal(t *testing.T) {
	const refusal = "aurora has not applied ada@example.com's stored grant yet: its own `fugaro update` is what is pending"
	roster := func() workspace.Roster {
		return workspace.Roster{
			People: []workspace.Person{
				{Address: "ada@example.com", Kind: workspace.KindHuman},
				{Address: "founder@example.com", Kind: workspace.KindHuman},
			},
			Admins:  []string{"founder@example.com"},
			Domains: []string{"example.com"},
		}
	}

	// A sweep that failed: the refusal is returned with the sweep's own failure
	// beside it, and nothing is claimed about a removal that did not land -- not
	// the sweep's answer, and not the halting line, because the roster was not
	// written and the person's run is still going (R3).
	t.Run("a failed sweep is not swallowed", func(t *testing.T) {
		w := wu7Admin(t, roster())
		w.linked = []workspace.MemberTarget{{Project: "aurora"}}
		w.members.offErr = remote(fmt.Errorf("%s", refusal))
		w.sweep.err = remote(fmt.Errorf("the workspace's intake answered 500"))

		out, err := w.run("workspace", "user", "rm", "ada@example.com", "--workspace", "workspace-alpha")
		if err == nil {
			t.Fatalf("a member's refusal and a failed sweep were reported as a removal:\n%s", out)
		}
		for _, want := range []string{refusal, "did not sweep ada@example.com's containers", "500"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the report does not name %q, so the failed sweep is silent:\n%v", want, err)
			}
		}
		if got := ExitCode(err); got != ExitRemoteError {
			t.Errorf("the failure exits %d, want %d: both halves arrived in the class they carry", got, ExitRemoteError)
		}
		if strings.Contains(out, "halts within a minute") || strings.Contains(out, "at the workspace: ") {
			t.Errorf("a removal that did not land claimed the sweep's answer:\n%s", out)
		}
	})

	// A sweep that landed beside a refused member: what it removed is SAID, and the
	// halting line is not, because the removal is what halts a run and the removal
	// did not happen.
	t.Run("what a landed sweep removed is said", func(t *testing.T) {
		w := wu7Admin(t, roster())
		w.linked = []workspace.MemberTarget{{Project: "aurora"}}
		w.members.offErr = remote(fmt.Errorf("%s", refusal))
		w.sweep.deleted = []string{"fugaro_u_4242_github-token"}

		out, err := w.run("workspace", "user", "rm", "ada@example.com", "--workspace", "workspace-alpha")
		if err == nil {
			t.Fatalf("a member's refusal was reported as a removal:\n%s", out)
		}
		if !strings.Contains(err.Error(), refusal) || strings.Contains(err.Error(), "did not sweep") {
			t.Errorf("the refusal is not the whole of the failure, or a sweep that answered is reported as failed:\n%v", err)
		}
		for _, want := range []string{"ada@example.com", "fugaro_u_4242_github-token"} {
			if !strings.Contains(out, want) {
				t.Errorf("the report does not name %q, so what the sweep removed at the workspace is lost:\n%s", want, out)
			}
		}
		if strings.Contains(out, "halts within a minute") {
			t.Errorf("the report of a removal that did not land says a run halts within a minute:\n%s", out)
		}
		if !w.hasPerson("ada@example.com") {
			t.Error("the refused removal took the person off the roster anyway")
		}
	})
}

// TestRmAdminRemovesBindingsAndQueuesGrants is the task's red step (plan §3,
// WU7). Removing a workspace admin takes their live binding away AT ONCE — the
// folder owner or the token-creator binding, and with it their authority over
// every member — and what lags is the stored grant: it leaves the workspace's
// node now, and the member's own roster is written now, so the member's next
// `fugaro update` is what drops the resource-level bindings (D2818 as
// corrected).
func TestRmAdminRemovesBindingsAndQueuesGrants(t *testing.T) {
	w := wu7Admin(t, workspace.Roster{
		People: []workspace.Person{
			{Address: "ada@example.com", Kind: workspace.KindHuman},
			{Address: "founder@example.com", Kind: workspace.KindHuman},
		},
		Admins:  []string{"ada@example.com", "founder@example.com"},
		Domains: []string{"example.com"},
		Grants: []workspace.StoredGrant{
			{Project: "aurora", User: "ada@example.com", Role: workspace.RoleAdmin},
			{Project: "borealis", User: "ada@example.com", Role: workspace.RoleAdmin},
		},
	})
	w.linked = []workspace.MemberTarget{{Project: "aurora"}, {Project: "borealis"}}
	w.authority.bound = []string{"user:ada@example.com", "user:founder@example.com"}
	w.members.grants["aurora"] = []accesslib.Grant{
		{User: "user:ada@example.com", Role: accesslib.RoleAdmin, Scope: accesslib.Scope{Kind: accesslib.ScopeProject}},
		{User: "user:founder@example.com", Role: accesslib.RoleAdmin, Scope: accesslib.Scope{Kind: accesslib.ScopeProject}},
	}
	w.members.grants["borealis"] = []accesslib.Grant{
		{User: "user:ada@example.com", Role: accesslib.RoleAdmin, Scope: accesslib.Scope{Kind: accesslib.ScopeProject}},
	}

	w.must("workspace", "user", "rm", "ada@example.com", "--admin", "--workspace", "workspace-alpha")

	// The binding goes at once, and once.
	if len(w.authority.removed) != 1 || w.authority.removed[0] != "user:ada@example.com" {
		t.Fatalf("the bindings removed are %v, want ada@example.com's own member once", w.authority.removed)
	}
	if len(w.authority.added) != 0 {
		t.Errorf("the removal bound somebody: %v", w.authority.added)
	}
	for _, b := range w.authority.bound {
		if b == "user:ada@example.com" {
			t.Error("ada@example.com still holds the live binding")
		}
	}

	// The stored grant is queued the same way the add applied it: the member's
	// own roster no longer names her, and the workspace's own mark of what it
	// wrote is gone, so the member's next update derives an admin list without
	// her.
	want := []string{"aurora ada@example.com", "borealis ada@example.com"}
	if strings.Join(w.members.offboarded, "|") != strings.Join(want, "|") {
		t.Errorf("the members offboarded are %v, want %v", w.members.offboarded, want)
	}
	for _, project := range []string{"aurora", "borealis"} {
		for _, g := range w.members.grantsOf(project) {
			if g.User == "user:ada@example.com" {
				t.Errorf("%s still holds ada@example.com's stored grant: %+v", project, g)
			}
		}
	}
	after := w.store.roster()
	if !slices.Contains(after.Admins, "founder@example.com") || slices.Contains(after.Admins, "ada@example.com") {
		t.Errorf("the admins are %v, want founder@example.com alone", after.Admins)
	}
	if len(after.Grants) != 0 {
		t.Errorf("the workspace's own marks are %+v, want none: the grants the workspace wrote for her went with her", after.Grants)
	}
	if w.hasPerson("ada@example.com") {
		t.Error("the roster still names the admin taken off it")
	}
	actions := w.audit.actions()
	if !slices.Contains(actions, workspace.ActionUserRemove) || !slices.Contains(actions, workspace.ActionAdminRemove) {
		t.Errorf("the trail is %v, want the removal and the admin's own entry", actions)
	}

	// --admin on somebody who is not a workspace admin is refused rather than
	// silently treated as an ordinary removal: the flag is a statement about
	// the binding, and there is none to drop.
	solo := wu7Admin(t, workspace.Roster{
		People: []workspace.Person{
			{Address: "ada@example.com", Kind: workspace.KindHuman},
			{Address: "founder@example.com", Kind: workspace.KindHuman},
		},
		Admins:  []string{"founder@example.com"},
		Domains: []string{"example.com"},
	})
	solo.refuses([]string{"ada@example.com", "--admin"},
		"workspace", "user", "rm", "ada@example.com", "--admin", "--workspace", "workspace-alpha")
	solo.nothingWasWritten("--admin on a member who is not a workspace admin")

	// The "queue" is what the member has not applied yet: while a member's own
	// update is what is pending, C2's Offboard is the refusal that says so, and
	// the command reports it rather than writing a node half-changed.
	pending := wu7Admin(t, workspace.Roster{
		People: []workspace.Person{
			{Address: "ada@example.com", Kind: workspace.KindHuman},
			{Address: "founder@example.com", Kind: workspace.KindHuman},
		},
		Admins:  []string{"ada@example.com", "founder@example.com"},
		Domains: []string{"example.com"},
		Grants:  []workspace.StoredGrant{{Project: "aurora", User: "ada@example.com", Role: workspace.RoleAdmin}},
	})
	pending.linked = []workspace.MemberTarget{{Project: "aurora"}}
	pending.members.offErr = fmt.Errorf("aurora has not applied ada@example.com's stored grant yet: its own fugaro update is what is pending")
	pending.refuses([]string{"fugaro update"},
		"workspace", "user", "rm", "ada@example.com", "--admin", "--workspace", "workspace-alpha")
	if pending.hasPerson("ada@example.com") == false {
		t.Error("the refused removal wrote the roster")
	}
}

// TestLastWorkspaceAdminKept: a change that would leave the workspace with no
// admin is refused before anything is written — the roster names at least one,
// and a workspace nobody administers is one C1's own reader refuses.
func TestLastWorkspaceAdminKept(t *testing.T) {
	w := wu7Admin(t, workspace.Roster{
		People: []workspace.Person{
			{Address: "founder@example.com", Kind: workspace.KindHuman},
			{Address: "grace@example.com", Kind: workspace.KindHuman},
		},
		Admins:  []string{"founder@example.com"},
		Domains: []string{"example.com"},
	})
	w.linked = []workspace.MemberTarget{{Project: "aurora"}}

	w.refuses([]string{"last workspace admin", "founder@example.com"},
		"workspace", "user", "rm", "founder@example.com", "--admin", "--workspace", "workspace-alpha")
	w.nothingWasWritten("removing the last workspace admin")

	// With a second bound admin, the first may go — and then the one who is
	// left may not, which is the same rule read from the other side.
	second := wu7Admin(t, workspace.Roster{
		People: []workspace.Person{
			{Address: "ada@example.com", Kind: workspace.KindHuman},
			{Address: "founder@example.com", Kind: workspace.KindHuman},
		},
		Admins:  []string{"ada@example.com", "founder@example.com"},
		Domains: []string{"example.com"},
	})
	second.linked = []workspace.MemberTarget{{Project: "aurora"}}
	second.authority.bound = []string{"user:ada@example.com", "user:founder@example.com"}
	second.must("workspace", "user", "rm", "ada@example.com", "--admin", "--workspace", "workspace-alpha")
	second.refuses([]string{"last workspace admin"},
		"workspace", "user", "rm", "founder@example.com", "--admin", "--workspace", "workspace-alpha")
}

// TestUserRmSelfAdminLeavesNoUnbackedEntry: an admin may remove their OWN entry
// while another admin is left, and that command takes their own live binding away
// before the roster node is written. The gate it was admitted by therefore cannot
// be read a second time on the write path: doing so refuses the command's own
// work, and what is left behind is the shape D2823 exists for — a roster naming
// an admin no live binding backs — with the binding dropped, every member
// offboarded and no event filed.
func TestUserRmSelfAdminLeavesNoUnbackedEntry(t *testing.T) {
	w := wu7Admin(t, workspace.Roster{
		People: []workspace.Person{
			{Address: "ada@example.com", Kind: workspace.KindHuman},
			{Address: "founder@example.com", Kind: workspace.KindHuman},
		},
		Admins:  []string{"ada@example.com", "founder@example.com"},
		Domains: []string{"example.com"},
	})
	w.linked = []workspace.MemberTarget{{Project: "aurora"}}
	w.authority.bound = []string{"user:ada@example.com", "user:founder@example.com"}

	w.must("workspace", "user", "rm", "founder@example.com", "--admin", "--workspace", "workspace-alpha")

	after := w.store.roster()
	if w.hasPerson("founder@example.com") || slices.Contains(after.Admins, "founder@example.com") {
		t.Errorf("the admin who removed themselves is still recorded: people %+v, admins %v", after.People, after.Admins)
	}
	if !slices.Contains(after.Admins, "ada@example.com") {
		t.Errorf("the admins are %v, want the one who was not removed", after.Admins)
	}
	// Every entry the node still names is one a live binding backs: an entry the
	// command's own half-finished work left behind is what `status` reports as
	// tampering.
	for _, admin := range after.Admins {
		if !slices.Contains(w.authority.bound, "user:"+admin) {
			t.Errorf("the roster names %s as a workspace admin, and no live binding backs that entry", admin)
		}
	}
	if len(w.authority.removed) != 1 || w.authority.removed[0] != "user:founder@example.com" {
		t.Errorf("the bindings removed are %v, want the caller's own member once", w.authority.removed)
	}
	if strings.Join(w.members.offboarded, "|") != "aurora founder@example.com" {
		t.Errorf("the members offboarded are %v, want every one the workspace holds", w.members.offboarded)
	}
	// Both halves of the change are filed, under the caller's own name: the
	// person who acted is read from the credential and not from the node.
	for _, action := range []workspace.Action{workspace.ActionUserRemove, workspace.ActionAdminRemove} {
		if event := w.audit.one(t, action); event.Actor != "founder@example.com" {
			t.Errorf("the %s event is %+v, want the caller as its actor", action, event)
		}
	}
}

// TestUserAddMintsItsEventIDWithoutATerminal: an event id minted with no rand
// seam comes from the system's randomness, which is what the seam documents a nil
// to be. A write of this family mints an id per event on every run, so a fallback
// to the process's own stdin would block a command behind a prompt nothing prints
// — or fail it with an EOF — after the roster node had already been written.
func TestUserAddMintsItsEventIDWithoutATerminal(t *testing.T) {
	w := wu7Admin(t, workspace.Roster{
		People:  []workspace.Person{{Address: "founder@example.com", Kind: workspace.KindHuman}},
		Admins:  []string{"founder@example.com"},
		Domains: []string{"example.com"},
	})
	w.rand = nil

	w.must("workspace", "user", "add", "grace@example.com", "--workspace", "workspace-alpha")
	if !w.hasPerson("grace@example.com") {
		t.Error("the add with no rand seam wrote no member")
	}
	event := w.audit.one(t, workspace.ActionUserAdd)
	if event.ID == "" {
		t.Errorf("the event is %+v, want an id the trail can hold", event)
	}
}

// TestDomainsRoundTrip: `domains add|rm|ls` edit the workspace's allowed domains
// (D2812), each change is filed, and the list is held in the folded spelling the
// node carries. A domain a member's address sits in cannot be taken off the list:
// drift computes the external mark from Domains, so removing it would report every
// one of those members as tampering — the fix is to remove them, or to keep the
// domain.
func TestDomainsRoundTrip(t *testing.T) {
	w := wu7Admin(t, workspace.Roster{
		People: []workspace.Person{
			{Address: "ada@example.com", Kind: workspace.KindHuman},
			{Address: "founder@example.com", Kind: workspace.KindHuman},
		},
		Admins: []string{"founder@example.com"},
	})

	out := w.must("workspace", "domains", "ls", "--workspace", "workspace-alpha")
	if !strings.Contains(out, "workspace-alpha") || !strings.Contains(out, "no allowed domain") {
		t.Errorf("the listing of an empty list is:\n%s", out)
	}

	w.must("workspace", "domains", "add", "Example.COM", "--workspace", "workspace-alpha")
	domains := w.store.roster().Domains
	if len(domains) != 1 || domains[0] != "example.com" {
		t.Fatalf("the domains are %v, want the folded spelling of the one added", domains)
	}
	if event := w.audit.one(t, workspace.ActionDomainAdd); event.Subject != "example.com" {
		t.Errorf("the event is %+v, want the domain that was added", event)
	}
	w.refuses([]string{"example.com", "already"},
		"workspace", "domains", "add", "example.com", "--workspace", "workspace-alpha")
	w.refuses([]string{"domain"},
		"workspace", "domains", "add", "not a domain", "--workspace", "workspace-alpha")

	out = w.must("workspace", "domains", "ls", "--workspace", "workspace-alpha")
	if !strings.Contains(out, "example.com") {
		t.Errorf("the listing does not name the domain that is there:\n%s", out)
	}

	// A domain a member's address sits in stays while the member does.
	w.refuses([]string{"ada@example.com", "example.com"},
		"workspace", "domains", "rm", "example.com", "--workspace", "workspace-alpha")
	if len(w.store.roster().Domains) != 1 {
		t.Error("the refused removal edited the list")
	}

	// A domain nobody's address sits in goes, and a second removal of the same
	// one is a refusal — and the domain the member beside it sits in cannot go
	// while that member is there, which is the same rule read from the other
	// side.
	empty := wu7Admin(t, workspace.Roster{
		People:  []workspace.Person{{Address: "founder@example.com", Kind: workspace.KindHuman}},
		Admins:  []string{"founder@example.com"},
		Domains: []string{"example.com", "other.test"},
	})
	empty.must("workspace", "domains", "rm", "other.test", "--workspace", "workspace-alpha")
	if got := empty.store.roster().Domains; len(got) != 1 || got[0] != "example.com" {
		t.Errorf("the domains are %v, want the one nobody is in taken off and the members' own kept", got)
	}
	if event := empty.audit.one(t, workspace.ActionDomainRemove); event.Subject != "other.test" {
		t.Errorf("the event is %+v, want the domain that was removed", event)
	}
	empty.refuses([]string{"other.test"},
		"workspace", "domains", "rm", "other.test", "--workspace", "workspace-alpha")
	empty.refuses([]string{"founder@example.com", "example.com"},
		"workspace", "domains", "rm", "example.com", "--workspace", "workspace-alpha")
	if len(empty.store.roster().Domains) != 1 {
		t.Error("the refused removal edited the list")
	}

	// The gate is the same one every write of this family carries (D2823), and
	// it refuses before the list is read.
	outsider := wu7New(t, workspace.Roster{
		People: []workspace.Person{
			{Address: "ada@example.com", Kind: workspace.KindHuman},
			{Address: "founder@example.com", Kind: workspace.KindHuman},
		},
		Admins:  []string{"founder@example.com"},
		Domains: []string{"example.com"},
	})
	outsider.caller = "user:ada@example.com"
	outsider.authority.bound = []string{"user:founder@example.com"}
	outsider.refuses([]string{"workspace admin"},
		"workspace", "domains", "add", "other.test", "--workspace", "workspace-alpha")
	outsider.nothingWasWritten("a domain add by somebody who is not a workspace admin")
}

// TestUserLsJSONSchema: `workspace user ls` aggregates the members' grants —
// each linked member's own roster, read over the member contract — and its
// `--json` document is the contract a consumer reads it by.
func TestUserLsJSONSchema(t *testing.T) {
	w := wu7New(t, workspace.Roster{
		People: []workspace.Person{
			{Address: "ada@example.com", Kind: workspace.KindHuman},
			{Address: "build@ci.example.com", Kind: workspace.KindSA, Roles: []workspace.ProjectRole{{Project: "aurora", Role: workspace.RoleViewer}}},
			{Address: "founder@example.com", Kind: workspace.KindHuman},
			{Address: "zoe@other.test", Kind: workspace.KindHuman, External: true},
		},
		Admins:  []string{"founder@example.com"},
		Domains: []string{"example.com"},
	})
	w.linked = []workspace.MemberTarget{{Project: "aurora"}, {Project: "borealis"}}
	w.members.grants["aurora"] = []accesslib.Grant{
		{User: "user:ada@example.com", Role: accesslib.RoleOperator, Scope: accesslib.Scope{Kind: accesslib.ScopeProject}},
	}
	w.members.grants["borealis"] = []accesslib.Grant{
		{User: "user:ada@example.com", Role: accesslib.RoleViewer, Scope: accesslib.Scope{Kind: accesslib.ScopeProject}},
	}

	out := w.must("workspace", "user", "ls", "--workspace", "workspace-alpha", "--json")
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := doc["version"]; got != float64(workspaceUsersDocVersion) {
		t.Errorf("version = %v, want %d", got, workspaceUsersDocVersion)
	}
	if got := doc["workspace"]; got != "workspace-alpha" {
		t.Errorf("workspace = %v, want the workspace the flag named", got)
	}
	rows, _ := doc["members"].([]any)
	if len(rows) != 4 {
		t.Fatalf("the document lists %d members, want the four the node names:\n%s", len(rows), out)
	}
	byAddress := map[string]map[string]any{}
	for _, r := range rows {
		row, _ := r.(map[string]any)
		address, _ := row["address"].(string)
		byAddress[address] = row
	}
	ada := byAddress["ada@example.com"]
	if ada == nil || ada["kind"] != "human" || ada["external"] != false || ada["admin"] != false {
		t.Fatalf("ada's row is %v", ada)
	}
	grants, _ := ada["grants"].([]any)
	if len(grants) != 2 {
		t.Fatalf("ada's grants are %v, want one per member she holds a role in", ada["grants"])
	}
	first, _ := grants[0].(map[string]any)
	if first["project"] != "aurora" || first["role"] != "operator" {
		t.Errorf("ada's first grant is %v, want aurora/operator", first)
	}
	if roles, _ := ada["roles"].([]any); len(roles) != 0 {
		t.Errorf("a human's expected roles are %v: the per-project roles of the node are a machine's (D2806)", ada["roles"])
	}
	if machine := byAddress["build@ci.example.com"]; machine["kind"] != "sa" {
		t.Errorf("the machine's row is %v", machine)
	} else if roles, _ := machine["roles"].([]any); len(roles) != 1 {
		t.Errorf("the machine's expected roles are %v, want the node's one", machine["roles"])
	} else if role, _ := roles[0].(map[string]any); role["project"] != "aurora" || role["role"] != "viewer" {
		t.Errorf("the machine's role is %v, want a role the node records for it", role)
	}
	if zoe := byAddress["zoe@other.test"]; zoe["external"] != true {
		t.Errorf("the external member's row is %v, want the mark the node holds", zoe)
	}
	if founder := byAddress["founder@example.com"]; founder["admin"] != true {
		t.Errorf("the admin's row is %v, want admin true", founder)
	}
	if domains, _ := doc["domains"].([]any); len(domains) != 1 || domains[0] != "example.com" {
		t.Errorf("the document's domains are %v, want the node's list", doc["domains"])
	}

	// The schema is the document's other half, and the two state one version.
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "workspace-users.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sch map[string]any
	if err := json.Unmarshal(raw, &sch); err != nil {
		t.Fatal(err)
	}
	props, _ := sch["properties"].(map[string]any)
	version, _ := props["version"].(map[string]any)
	if got := version["const"]; got != float64(workspaceUsersDocVersion) {
		t.Errorf("schemas/workspace-users.schema.json's version const is %v, and workspaceUsersDocVersion is %d: the document and its schema state one version", got, workspaceUsersDocVersion)
	}
	wu7ValidateSchema(t, raw, out)

	// A workspace with one member and no machines, no domains and no grants
	// validates too: every array of the document is seeded rather than null, so
	// "there are none" is a fact a reader reads without a case for it.
	solo := wu7New(t, workspace.Roster{
		People: []workspace.Person{{Address: "founder@example.com", Kind: workspace.KindHuman}},
		Admins: []string{"founder@example.com"},
	})
	soloOut := solo.must("workspace", "user", "ls", "--workspace", "workspace-alpha", "--json")
	for _, want := range []string{`"members": [`, `"domains": [`, `"roles": [`, `"grants": [`} {
		if !strings.Contains(soloOut, want) {
			t.Errorf("the document of a workspace with nothing beside it does not carry %s:\n%s", want, soloOut)
		}
	}
	wu7ValidateSchema(t, raw, soloOut)

	// The listing a person reads names every member, marks the ones admitted
	// from outside and the admins, and escapes what a member typed.
	printed := solo.must("workspace", "user", "ls", "--workspace", "workspace-alpha")
	if !strings.Contains(printed, "founder@example.com") || !strings.Contains(printed, "admin") {
		t.Errorf("the listing is:\n%s", printed)
	}
}

// wu7ValidateSchema validates one document against the schema at raw.
func wu7ValidateSchema(t *testing.T, raw []byte, doc string) {
	t.Helper()
	parsed, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := parsed.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("schemas/workspace-users.schema.json has no $id")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, parsed); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("%v\n%s", err, doc)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("the document does not match schemas/workspace-users.schema.json: %v\n%s", err, doc)
	}
}
