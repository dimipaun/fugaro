package cli

// WU8's tests: `fugaro workspace user grant|revoke` against a member's own
// documents.
//
// The member is declared here (wu8FakeMemberOps) and not taken from C2's stub,
// because this file is about what the command DOES to a member: which node it
// reads, what it writes, what it files, and what it refuses. The stub answers
// the empty truth and decides nothing, so a case built on it would be green
// against a command that wrote nothing at all.
//
// The caller is stubbed (userPairAs) for the same reason `fugaro user grant`'s
// tests stub it: it is a credential's verified address on a real invocation
// (access.Caller, F21), and a test that reached Google's userinfo endpoint would
// be neither hermetic nor about the judgement.
//
// The member's mirror is written by the test in C2's document shape — an object
// with a `members` list of C1's entries — which is the shape the command's own
// reader assumes (workspaceMirrorEntries). The document's outer key is settled
// on the member side (WU4, contract-member.md); this file's writer and the
// command's reader move together if it is settled differently.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/workspace"
)

const (
	wu8Member = "member-one" // the member project every case grants in
	// wu8Caller is the credential: a project admin of the member — placed
	// nowhere in the member's grants node and named in no list this machine
	// reads, which is exactly the caller whose write is the answer (F22's
	// table). A workspace grant never asks whether they are a workspace admin,
	// because a project admin needs no grant on the workspace's project.
	wu8Caller        = "user:admin@example.com"
	wu8CallerAddress = "admin@example.com"
	wu8Alice         = "alice@example.com"
	wu8Bob           = "bob@example.com"
)

// wu8FakeMemberOps is the member this task's tests write through: the two
// documents its database holds, and the record of what the command did to them.
// Every call names its target, so a case can say which member was reached and
// that no other was.
type wu8FakeMemberOps struct {
	t *testing.T

	mirror workspace.MemberDoc
	grants workspace.MemberDoc

	// mirrorErr, readErr, writeErr and auditErr are what the member's database
	// answers instead, and staleWrites is how many writes are lost to another
	// writer between the read and the write (rtdb.ErrPrecondition).
	mirrorErr   error
	readErr     error
	writeErr    error
	auditErr    error
	staleWrites int

	mirrorReads []workspace.MemberTarget
	grantsReads []workspace.MemberTarget
	writes      []wu8Write
	audits      []wu8Audit
}

type wu8Write struct {
	target workspace.MemberTarget
	doc    workspace.MemberDoc
	raw    []byte
}

type wu8Audit struct {
	target workspace.MemberTarget
	id     string
	event  []byte
}

var _ workspace.MemberOps = (*wu8FakeMemberOps)(nil)

func (f *wu8FakeMemberOps) ReadMirror(_ context.Context, m workspace.MemberTarget) (workspace.MemberDoc, error) {
	f.mirrorReads = append(f.mirrorReads, m)
	if f.mirrorErr != nil {
		return workspace.MemberDoc{}, f.mirrorErr
	}
	return f.mirror, nil
}

func (f *wu8FakeMemberOps) ReadGrants(_ context.Context, m workspace.MemberTarget) (workspace.MemberDoc, error) {
	f.grantsReads = append(f.grantsReads, m)
	if f.readErr != nil {
		return workspace.MemberDoc{}, f.readErr
	}
	return f.grants, nil
}

// WriteGrants is the member's database: the write is conditional on the ETag it
// was read under, and one that landed is what the next read sees.
func (f *wu8FakeMemberOps) WriteGrants(_ context.Context, m workspace.MemberTarget, doc workspace.MemberDoc, raw []byte) error {
	f.writes = append(f.writes, wu8Write{target: m, doc: doc, raw: raw})
	if f.writeErr != nil {
		return f.writeErr
	}
	if f.staleWrites > 0 {
		f.staleWrites--
		return rtdb.ErrPrecondition
	}
	if doc.ETag != f.grants.ETag {
		return rtdb.ErrPrecondition
	}
	f.grants = workspace.MemberDoc{Raw: raw, ETag: fmt.Sprintf("grants-%d", len(f.writes)), Found: true}
	return nil
}

func (f *wu8FakeMemberOps) Audit(_ context.Context, m workspace.MemberTarget, id string, event []byte) error {
	f.audits = append(f.audits, wu8Audit{target: m, id: id, event: event})
	return f.auditErr
}

// The rest of MemberOps is not this task's to call: a command that reached one
// of them would be doing something else with the member, and a test that let it
// would be silent about it.
func (f *wu8FakeMemberOps) WriteMirror(context.Context, workspace.MemberTarget, workspace.MemberDoc, []byte) error {
	return f.wu8Unexpected("WriteMirror")
}

func (f *wu8FakeMemberOps) Bindings(context.Context, workspace.MemberTarget) ([]workspace.MemberBinding, error) {
	return nil, f.wu8Unexpected("Bindings")
}

func (f *wu8FakeMemberOps) PendingApply(context.Context, workspace.MemberTarget, workspace.ApplyWant) (workspace.Pending, error) {
	return workspace.Pending{}, f.wu8Unexpected("PendingApply")
}

func (f *wu8FakeMemberOps) Offboard(context.Context, workspace.MemberTarget, string) error {
	return f.wu8Unexpected("Offboard")
}

func (f *wu8FakeMemberOps) wu8Unexpected(what string) error {
	if f.t != nil {
		f.t.Errorf("the command called MemberOps.%s, and fugaro workspace user grant|revoke reaches a member through ReadMirror, ReadGrants, WriteGrants and Audit alone", what)
	}
	return fmt.Errorf("MemberOps.%s is not this task's to call", what)
}

// wu8Wrote is what the command left behind, or "" when it left nothing: no
// grants write, no audit entry, and the node still holding what it held. A
// refusal that writes is the failure every case here is about.
func (f *wu8FakeMemberOps) wu8Wrote() string {
	if len(f.writes) > 0 {
		return fmt.Sprintf("%d grants write(s) landed, the first with %s", len(f.writes), f.writes[0].raw)
	}
	if len(f.audits) > 0 {
		return fmt.Sprintf("an audit entry was filed: %s", f.audits[0].event)
	}
	return ""
}

// wu8Rig is a linked member: the mirror at PathMemberMirror carries these
// entries — a nil list is the member nothing has been mirrored into — and the
// grants node holds these pairs.
func wu8Rig(t *testing.T, members []workspace.MirrorEntry, grants ...accesslib.Grant) *wu8FakeMemberOps {
	t.Helper()
	f := &wu8FakeMemberOps{t: t}
	if members != nil {
		f.mirror = workspace.MemberDoc{Raw: wu8MirrorDoc(t, members), ETag: "mirror-1", Found: true}
	}
	if len(grants) > 0 {
		raw, err := accesslib.Encode(grants)
		if err != nil {
			t.Fatalf("the test's own grants node is not one the member's reader accepts: %v", err)
		}
		f.grants = workspace.MemberDoc{Raw: raw, ETag: "grants-1", Found: true}
	}
	return f
}

// wu8Unlinked is the member nothing has been mirrored into: C2's own answer for
// a member the workspace does not manage (R11, D2819).
func wu8Unlinked(t *testing.T, grants ...accesslib.Grant) *wu8FakeMemberOps {
	t.Helper()
	return wu8Rig(t, nil, grants...)
}

// wu8MirrorDoc is the member's mirror as its database holds it: the members
// list of C1's entries, in the spelling an entry is written in.
func wu8MirrorDoc(t *testing.T, members []workspace.MirrorEntry) []byte {
	t.Helper()
	entries := make([]map[string]any, 0, len(members))
	for _, m := range members {
		if err := m.Validate(); err != nil {
			t.Fatalf("the test's own mirror entry is not one the contract accepts: %v", err)
		}
		entries = append(entries, map[string]any{"address": m.Address, "kind": string(m.Kind), "external": m.External})
	}
	raw, err := json.Marshal(map[string]any{"version": workspaceMirrorVersion, "members": entries})
	if err != nil {
		t.Fatalf("the test's own mirror: %v", err)
	}
	return raw
}

// wu8Person is a human member of the mirror list, and wu8Machine one of kind
// `sa` (D2806).
func wu8Person(address string) workspace.MirrorEntry {
	return workspace.MirrorEntry{Address: address, Kind: workspace.KindHuman}
}

func wu8Machine(address string) workspace.MirrorEntry {
	return workspace.MirrorEntry{Address: address, Kind: workspace.KindSA}
}

// wu8Stored is one pair as the member's node holds it, built through ParseScope
// so a test's scope is the canonical spelling the command writes.
func wu8Stored(t *testing.T, address string, role accesslib.Role, scope string) accesslib.Grant {
	t.Helper()
	sc, err := accesslib.ParseScope(scope)
	if err != nil {
		t.Fatalf("the test's own scope %q is not one the node parses: %v", scope, err)
	}
	member, err := accesslib.Member(address)
	if err != nil {
		t.Fatalf("the test's own address %q is not one the node stores: %v", address, err)
	}
	return accesslib.Grant{User: member, Role: role, Scope: sc}
}

// wu8As is one address as a grants node carries it: the member form, which is
// what the node stores and what a pair line names.
func wu8As(t *testing.T, address string) string {
	t.Helper()
	member, err := accesslib.Member(address)
	if err != nil {
		t.Fatalf("the test's own address %q is not one the node stores: %v", address, err)
	}
	return member
}

// wu8Pairs is a grants node as a person reads it: the member, the role and the
// scope per pair, sorted, so a case can say what a write left behind without the
// node's own order mattering. It decodes through the member's own reader
// (access.Decode), which is what "in today's format" means: a document the
// member's `fugaro update` could not read is not a node this command may write.
func wu8Pairs(t *testing.T, raw []byte) []string {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	grants, err := accesslib.Decode(raw)
	if err != nil {
		t.Fatalf("the member's grants node is not one its own reader accepts: %v\n%s", err, raw)
	}
	out := make([]string, 0, len(grants))
	for _, g := range grants {
		out = append(out, g.User+" ("+string(g.Role)+") at "+g.Scope.String())
	}
	slices.Sort(out)
	return out
}

// wu8Run runs one grant or revoke the way a person at their own terminal would:
// this member behind it, this credential, and the member's name from the
// argument. The caller is the credential's own member form, which is what
// access.Caller answers with (F21), and it is a parameter because the case
// about the last project admin has to be that admin: a caller the node places
// nowhere is judged by their write, and only a placed one meets the rule.
func wu8Run(t *testing.T, ops *wu8FakeMemberOps, caller, verb string, args ...string) (string, error) {
	t.Helper()
	noAgentSession(t)
	noTerminal(t)
	stubSelfCommand(t)
	userPairAs(t, caller)
	var cmd *cobra.Command
	switch verb {
	case "grant":
		cmd = newWorkspaceUserGrantCmd(ops)
	case "revoke":
		cmd = newWorkspaceUserRevokeCmd(ops)
	default:
		t.Fatalf("the test's own verb %q is neither grant nor revoke", verb)
	}
	out, errOut, err := userCommandOn(t, cmd, args...)
	return accessOut(out, errOut, err), err
}

// wu8Targets is every member the command named, so a case can say it reached
// one member and no other: the pair travels together, and a Firebase project
// named here would be this machine guessing at a fact of the member's own
// installation.
func wu8Targets(f *wu8FakeMemberOps) []workspace.MemberTarget {
	out := slices.Clone(f.mirrorReads)
	out = append(out, f.grantsReads...)
	for _, w := range f.writes {
		out = append(out, w.target)
	}
	for _, a := range f.audits {
		out = append(out, a.target)
	}
	return out
}

// TestGrantRefusesNonMember: the workspace's own list is the gate on a grant.
// An address the member's mirror does not carry was never admitted to the
// workspace, and a grant is not an admitting (D2813) — and the gate is
// ADVISORY: the member's own admins hold their database's write and can rewrite
// the list, which is why the fence is drift against the workspace's node
// (WU12) and not this read.
func TestGrantRefusesNonMember(t *testing.T) {
	t.Run("the list carries somebody else", func(t *testing.T) {
		ops := wu8Rig(t, []workspace.MirrorEntry{wu8Person(wu8Bob)})
		all, err := wu8Run(t, ops, wu8Caller, "grant", wu8Alice, wu8Member, "admin")
		if err == nil {
			t.Fatalf("a grant was written for an address the member's list does not carry:\n%s", all)
		}
		for _, want := range []string{wu8Alice, wu8Member, "member list", "not admitted", "D2813"} {
			if !strings.Contains(all, want) {
				t.Errorf("the refusal does not say %q:\n%s", want, all)
			}
		}
		if got := ops.wu8Wrote(); got != "" {
			t.Errorf("the refused grant wrote: %s", got)
		}
	})

	// The list is there and names nobody: a member mirrored into whose list is
	// empty is a member whose workspace has admitted nobody yet — not an
	// unlinked one, and not a member anything may be granted in.
	t.Run("the list is there and holds nobody", func(t *testing.T) {
		ops := wu8Rig(t, []workspace.MirrorEntry{})
		all, err := wu8Run(t, ops, wu8Caller, "grant", wu8Alice, wu8Member, "admin")
		if err == nil {
			t.Fatalf("a grant was written against a member whose list holds nobody:\n%s", all)
		}
		if !strings.Contains(all, wu8Alice) || !strings.Contains(all, "member list") {
			t.Errorf("the refusal does not name the address and the list it is missing from:\n%s", all)
		}
		if got := ops.wu8Wrote(); got != "" {
			t.Errorf("the refused grant wrote: %s", got)
		}
	})

	// No list at all is the other state, and the other refusal: the member is
	// not linked, and linking it is its owner's own act (D2819, R11).
	t.Run("no list at all is an unlinked member", func(t *testing.T) {
		ops := wu8Unlinked(t)
		all, err := wu8Run(t, ops, wu8Caller, "grant", wu8Alice, wu8Member, "admin")
		if err == nil {
			t.Fatalf("a grant was written against a member nothing has been mirrored into:\n%s", all)
		}
		for _, want := range []string{wu8Member, workspace.PathMemberMirror, "fugaro workspace project link"} {
			if !strings.Contains(all, want) {
				t.Errorf("the refusal does not say %q:\n%s", want, all)
			}
		}
		if got := ops.wu8Wrote(); got != "" {
			t.Errorf("the refused grant wrote: %s", got)
		}
	})

	// A machine is recorded in the workspace's node and granted nothing in a
	// member (D2806): its real IAM is its owner's own, wherever that is.
	t.Run("a machine is not granted", func(t *testing.T) {
		ops := wu8Rig(t, []workspace.MirrorEntry{wu8Person(wu8Bob), wu8Machine(wu8Alice)})
		all, err := wu8Run(t, ops, wu8Caller, "grant", wu8Alice, wu8Member, "admin")
		if err == nil {
			t.Fatalf("a machine was granted a member role:\n%s", all)
		}
		for _, want := range []string{wu8Alice, "machine", "D2806"} {
			if !strings.Contains(all, want) {
				t.Errorf("the refusal does not say %q:\n%s", want, all)
			}
		}
		if got := ops.wu8Wrote(); got != "" {
			t.Errorf("the refused grant wrote: %s", got)
		}
	})

	// A list this command cannot read is a list it cannot judge a grant
	// against, so it writes nothing over it. The node is written by several
	// parties and a member's admins can rewrite it, so a document that is not
	// the shape C1 states is refused rather than read as nobody.
	t.Run("a list that is not a document", func(t *testing.T) {
		ops := wu8Rig(t, []workspace.MirrorEntry{wu8Person(wu8Bob)})
		ops.mirror.Raw = []byte(`{"members":[{"address":"bob@example.com"}]}`)
		all, err := wu8Run(t, ops, wu8Caller, "grant", wu8Alice, wu8Member, "admin")
		if err == nil {
			t.Fatalf("a grant was written against a list this command cannot read:\n%s", all)
		}
		if !strings.Contains(all, workspace.PathMemberMirror) {
			t.Errorf("the refusal does not name the node it could not read:\n%s", all)
		}
		if got := ops.wu8Wrote(); got != "" {
			t.Errorf("the refused grant wrote: %s", got)
		}
	})

	// What is printed is escaped: the address is the agent's or the person's
	// own text, and a terminal is where it lands (the plan's escaping rule).
	t.Run("an address that is not one", func(t *testing.T) {
		ops := wu8Rig(t, []workspace.MirrorEntry{wu8Person(wu8Bob)})
		all, err := wu8Run(t, ops, wu8Caller, "grant", "alice@example.com\x1b[31m", wu8Member, "admin")
		if err == nil {
			t.Fatalf("a grant was written for an address that is not one:\n%s", all)
		}
		if strings.ContainsRune(all, 0x1b) {
			t.Errorf("the refusal carries the raw control byte it was handed:\n%q", all)
		}
		if got := ops.wu8Wrote(); got != "" {
			t.Errorf("the refused grant wrote: %s", got)
		}
	})

	// The member prefix `fugaro user grant` takes is not this command's
	// spelling: a grant names the address the workspace's own list carries.
	t.Run("the user: member form is refused", func(t *testing.T) {
		ops := wu8Rig(t, []workspace.MirrorEntry{wu8Person(wu8Alice)})
		all, err := wu8Run(t, ops, wu8Caller, "grant", "user:"+wu8Alice, wu8Member, "admin")
		if err == nil {
			t.Fatalf("a grant was written for a member form the workspace's list does not carry:\n%s", all)
		}
		if !strings.Contains(all, wu8Alice) {
			t.Errorf("the refusal does not name the address it was handed:\n%s", all)
		}
		if got := ops.wu8Wrote(); got != "" {
			t.Errorf("the refused grant wrote: %s", got)
		}
	})
}

// TestGrantAsProjectAdminWritesOnlyTheMember: a project admin of the member —
// with no grant on the workspace's project and no binding read from it — writes
// the member's roster, files the member's audit entry, and touches nothing else.
// That the whole run reached one member is the point: F11 and D2811 say a
// project admin needs no write on the workspace's own database, and the fake
// this command is given holds no workspace node at all.
func TestGrantAsProjectAdminWritesOnlyTheMember(t *testing.T) {
	ops := wu8Rig(t, []workspace.MirrorEntry{wu8Person(wu8Bob), wu8Person(wu8Alice)})
	all, err := wu8Run(t, ops, wu8Caller, "grant", wu8Alice, wu8Member, "operator")
	if err != nil {
		t.Fatalf("a project admin's grant was refused: %v\n%s", err, all)
	}
	if len(ops.writes) != 1 || len(ops.audits) != 1 {
		t.Fatalf("the grant made %d write(s) and %d audit entr(ies), want one of each:\n%s", len(ops.writes), len(ops.audits), all)
	}
	for _, target := range wu8Targets(ops) {
		if target.Project != wu8Member || target.Firebase != "" {
			t.Errorf("the command reached %+v, want member project %s and no other", target, wu8Member)
		}
	}
	want := wu8Stored(t, wu8Alice, accesslib.RoleOperator, "project")
	if got := wu8Pairs(t, ops.writes[0].raw); !slices.Equal(got, []string{wu8As(t, wu8Alice) + " (operator) at project"}) {
		t.Errorf("the member's node holds %v, want the granted pair:\n%s", got, all)
	}
	// The bytes are the format's own: what the member's reader writes for this
	// document is what this command wrote, not a second spelling of it.
	canonical, err := accesslib.Encode([]accesslib.Grant{want})
	if err != nil {
		t.Fatalf("the test's own expected node: %v", err)
	}
	if string(ops.writes[0].raw) != string(canonical) {
		t.Errorf("the member's node is %s, want %s", ops.writes[0].raw, canonical)
	}
	if got := ops.grants.Raw; string(got) != string(ops.writes[0].raw) {
		t.Errorf("the member's node holds %s after the write, want the bytes that were written", got)
	}

	// The trail is the member's own node (D2811), and the entry is C1's event:
	// the actor's address, what it did, and to whom.
	entry, err := workspace.DecodeAuditEvent(ops.audits[0].event)
	if err != nil {
		t.Fatalf("the audit entry is not one the trail's own reader accepts: %v\n%s", err, ops.audits[0].event)
	}
	if entry.ID != ops.audits[0].id {
		t.Errorf("the entry says its id is %q and it was filed under %q", entry.ID, ops.audits[0].id)
	}
	if entry.Actor != wu8CallerAddress {
		t.Errorf("the entry's actor is %q, want the credential's address %q", entry.Actor, wu8CallerAddress)
	}
	if entry.Action != workspace.ActionGrantWrite || entry.Subject != wu8Alice || entry.Project != wu8Member || entry.Role != workspace.Role(accesslib.RoleOperator) {
		t.Errorf("the entry is %+v, want a grant.write of %s in %s at operator", entry, wu8Alice, wu8Member)
	}

	// D2812's external mark is how somebody was admitted and not a verdict: an
	// outsider the workspace admitted is a member, and the grant is written for
	// them like any other. Outsiderness is computed from the workspace's domains
	// by the drift check (C1's contract), never from this mark.
	t.Run("an external member is granted like any other", func(t *testing.T) {
		outsider := wu8Rig(t, []workspace.MirrorEntry{{Address: wu8Alice, Kind: workspace.KindHuman, External: true}})
		all, err := wu8Run(t, outsider, wu8Caller, "grant", wu8Alice, wu8Member, "operator")
		if err != nil {
			t.Fatalf("a member admitted while outside the workspace's domains was refused: %v\n%s", err, all)
		}
		if got := wu8Pairs(t, outsider.grants.Raw); !slices.Equal(got, []string{wu8As(t, wu8Alice) + " (operator) at project"}) {
			t.Errorf("the node holds %v, want the granted pair", got)
		}
	})

	// A grant the node already holds is not a change: nothing is written and
	// nothing is filed.
	t.Run("a pair the node already holds", func(t *testing.T) {
		held := wu8Rig(t, []workspace.MirrorEntry{wu8Person(wu8Alice)},
			wu8Stored(t, wu8Alice, accesslib.RoleOperator, "project"))
		all, err := wu8Run(t, held, wu8Caller, "grant", wu8Alice, wu8Member, "operator")
		if err != nil {
			t.Fatalf("a grant of a pair the node already holds failed: %v\n%s", err, all)
		}
		if got := held.wu8Wrote(); got != "" {
			t.Errorf("a grant of a pair the node already holds wrote: %s", got)
		}
		if !strings.Contains(all, "already holds") {
			t.Errorf("the run does not say the pair was already held:\n%s", all)
		}
	})
}

// TestGrantNeedsDatabaseAdmin: the write of a member's grants node is
// roles/firebasedatabase.admin on that member, exactly as it is for
// `fugaro user grant` today — and the sentence a refused writer gets is that
// command's own, because the two commands write the same node with the same
// permission.
func TestGrantNeedsDatabaseAdmin(t *testing.T) {
	ops := wu8Rig(t, []workspace.MirrorEntry{wu8Person(wu8Alice)})
	ops.writeErr = rtdb.ErrPermission
	all, err := wu8Run(t, ops, wu8Caller, "grant", wu8Alice, wu8Member, "operator")
	if err == nil {
		t.Fatalf("a grant was written with a credential the member's database refused:\n%s", all)
	}
	for _, want := range []string{"nothing was granted", "roles/firebasedatabase.admin", "terraform.budget_admins", "runs fugaro update"} {
		if !strings.Contains(all, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, all)
		}
	}
	if len(ops.audits) != 0 {
		t.Errorf("a refused write filed an audit entry: %s", ops.audits[0].event)
	}
	if ops.grants.Found {
		t.Errorf("the refused write left a node behind: %s", ops.grants.Raw)
	}

	// A refusal the writer itself classified is not flattened into an outage:
	// WU4's concrete write refuses a member whose own lists have not been
	// migrated into its node, in that member's own words, and a caller that
	// reported it as "the database did not answer" would send a person looking
	// for an outage instead of running the member's own `fugaro update`. The
	// sentence is the layer below's, and this command passes it on.
	t.Run("a refusal from the writer keeps its own sentence and code", func(t *testing.T) {
		refused := wu8Rig(t, []workspace.MirrorEntry{wu8Person(wu8Alice)})
		refused.writeErr = userErr("nothing was granted: project %s has no grants node at %s yet, and its own lists still hold 2 member(s): the member's own fugaro update migrates them first", wu8Member, accesslib.PathNode)
		all, err := wu8Run(t, refused, wu8Caller, "grant", wu8Alice, wu8Member, "operator")
		if err == nil {
			t.Fatalf("a grant the writer refused was written:\n%s", all)
		}
		if !strings.Contains(all, "the member's own fugaro update migrates them first") {
			t.Errorf("the writer's own sentence was replaced:\n%s", all)
		}
		if got := ExitCode(err); got != ExitUserError {
			t.Errorf("the refusal exits %d, want %d: it is the member's to fix and not an outage", got, ExitUserError)
		}
		if len(refused.audits) != 0 {
			t.Errorf("a refused write filed an audit entry: %s", refused.audits[0].event)
		}
	})

	// A refused read is the credential's too, and not an outage: the read is
	// what the change is judged against, so a caller who can write but not read
	// is refused rather than judged from a read they could not make.
	t.Run("a refused read is refused, not reported as an outage", func(t *testing.T) {
		blocked := wu8Rig(t, []workspace.MirrorEntry{wu8Person(wu8Alice)})
		blocked.mirrorErr = rtdb.ErrPermission
		all, err := wu8Run(t, blocked, wu8Caller, "grant", wu8Alice, wu8Member, "operator")
		if err == nil {
			t.Fatalf("a member's list was read with a credential the database refused:\n%s", all)
		}
		if !strings.Contains(all, "roles/firebasedatabase.viewer") {
			t.Errorf("the refusal does not name the read's role:\n%s", all)
		}
		if got := ExitCode(err); got != ExitUserError {
			t.Errorf("the refused read exits %d, want the refusal code %d (an outage is the other one):\n%s", got, ExitUserError, all)
		}
		if got := blocked.wu8Wrote(); got != "" {
			t.Errorf("the refused read wrote: %s", got)
		}
	})
}

// TestGrantCarriesScope: the scope of the pair is the flag's, and the default
// is the whole member project (D2815) — the scope the member's own resolver
// reads back, in its canonical spelling.
func TestGrantCarriesScope(t *testing.T) {
	ops := wu8Rig(t, []workspace.MirrorEntry{wu8Person(wu8Alice)})
	if _, err := wu8Run(t, ops, wu8Caller, "grant", wu8Alice, wu8Member, "operator"); err != nil {
		t.Fatalf("a grant with no --scope was refused: %v", err)
	}
	if got := wu8Pairs(t, ops.writes[0].raw); !slices.Equal(got, []string{wu8As(t, wu8Alice) + " (operator) at project"}) {
		t.Errorf("a grant with no --scope wrote %v, want the whole member project (D2815)", got)
	}

	ops = wu8Rig(t, []workspace.MirrorEntry{wu8Person(wu8Alice)})
	if _, err := wu8Run(t, ops, wu8Caller, "grant", wu8Alice, wu8Member, "operator", "--scope", "github/acme/*"); err != nil {
		t.Fatalf("a repository-scoped grant was refused: %v", err)
	}
	if got := wu8Pairs(t, ops.writes[0].raw); !slices.Equal(got, []string{wu8As(t, wu8Alice) + " (operator) at github/acme/*"}) {
		t.Errorf("the node holds %v, want the scope the flag named", got)
	}

	ops = wu8Rig(t, []workspace.MirrorEntry{wu8Person(wu8Alice)})
	if _, err := wu8Run(t, ops, wu8Caller, "grant", wu8Alice, wu8Member, "operator", "--scope", "all-repos"); err != nil {
		t.Fatalf("an all-repos grant was refused: %v", err)
	}
	if got := wu8Pairs(t, ops.writes[0].raw); !slices.Equal(got, []string{wu8As(t, wu8Alice) + " (operator) at all-repos"}) {
		t.Errorf("the node holds %v, want all-repos", got)
	}

	// A scope access's own grammar refuses is refused here, before anything is
	// read: the node's reader is the one that decides what a scope is (F27's
	// fail-closed half).
	ops = wu8Rig(t, []workspace.MirrorEntry{wu8Person(wu8Alice)})
	all, err := wu8Run(t, ops, wu8Caller, "grant", wu8Alice, wu8Member, "operator", "--scope", "everything")
	if err == nil {
		t.Fatalf("a grant with a scope that is not one was written:\n%s", all)
	}
	if !strings.Contains(all, "--scope") {
		t.Errorf("the refusal does not name the flag:\n%s", all)
	}
	if got := ops.wu8Wrote(); got != "" {
		t.Errorf("the refused grant wrote: %s", got)
	}
}

// TestRevokeKeepsLastAdmin: the member's last project admin stays (F17.2).
// A project admin holds no grant on the workspace's project, so the node of the
// member itself is all there is to keep one in — a member whose node holds no
// project-scope admin has nobody who could grant a role or run its update.
func TestRevokeKeepsLastAdmin(t *testing.T) {
	t.Run("the only admin is refused", func(t *testing.T) {
		ops := wu8Rig(t, []workspace.MirrorEntry{wu8Person(wu8Alice)},
			wu8Stored(t, wu8Alice, accesslib.RoleAdmin, "project"))
		all, err := wu8Run(t, ops, wu8As(t, wu8Alice), "revoke", wu8Alice, wu8Member, "admin")
		if err == nil {
			t.Fatalf("the member's last project admin was revoked:\n%s", all)
		}
		for _, want := range []string{"nothing was written", "no admin at project scope", wu8Alice} {
			if !strings.Contains(all, want) {
				t.Errorf("the refusal does not say %q:\n%s", want, all)
			}
		}
		if got := ops.wu8Wrote(); got != "" {
			t.Errorf("the refused revoke wrote: %s", got)
		}
	})

	t.Run("another admin makes it a change", func(t *testing.T) {
		ops := wu8Rig(t, []workspace.MirrorEntry{wu8Person(wu8Alice), wu8Person(wu8Bob)},
			wu8Stored(t, wu8Alice, accesslib.RoleAdmin, "project"),
			wu8Stored(t, wu8Bob, accesslib.RoleAdmin, "project"))
		if _, err := wu8Run(t, ops, wu8As(t, wu8Alice), "revoke", wu8Alice, wu8Member, "admin"); err != nil {
			t.Fatalf("a revoke with another project admin left was refused: %v", err)
		}
		if got := wu8Pairs(t, ops.grants.Raw); !slices.Equal(got, []string{wu8As(t, wu8Bob) + " (admin) at project"}) {
			t.Errorf("the node holds %v, want the other admin alone", got)
		}
		if len(ops.audits) != 1 {
			t.Fatalf("the revoke filed %d audit entr(ies), want one", len(ops.audits))
		}
		entry, err := workspace.DecodeAuditEvent(ops.audits[0].event)
		if err != nil {
			t.Fatalf("the audit entry is not one the trail's own reader accepts: %v", err)
		}
		if entry.Action != workspace.ActionGrantRemove {
			t.Errorf("the entry is a %s, want a grant.remove", entry.Action)
		}
	})

	// A revoke names a pair the node holds, and a pair of the same member at
	// another scope is another pair: taking a role away is not taking every one
	// of them away.
	t.Run("a pair the node does not hold", func(t *testing.T) {
		ops := wu8Rig(t, []workspace.MirrorEntry{wu8Person(wu8Alice)},
			wu8Stored(t, wu8Alice, accesslib.RoleAdmin, "project"))
		all, err := wu8Run(t, ops, wu8As(t, wu8Alice), "revoke", wu8Alice, wu8Member, "operator")
		if err == nil {
			t.Fatalf("a revoke of a pair the node does not hold was written:\n%s", all)
		}
		if got := ops.wu8Wrote(); got != "" {
			t.Errorf("the refused revoke wrote: %s", got)
		}
	})
}

// TestRevokeTakesAccessAwayOffTheMirror: the member list is the gate on a GRANT
// and not on a revoke, because a revoke only takes access away — a refusal here
// could only leave the grant in place. The case that matters is the person the
// workspace no longer lists: somebody taken out of the workspace whose
// offboarding failed, or whose entry this member's own admins rewrote, keeps a
// grant that nothing else would remove once D2813's refusal lands in
// `fugaro user revoke`. The list is advisory besides, so gating a revoke on it
// would hand a member's admins a way to freeze the grants they can no longer
// justify.
func TestRevokeTakesAccessAwayOffTheMirror(t *testing.T) {
	ops := wu8Rig(t, []workspace.MirrorEntry{wu8Person(wu8Bob)},
		wu8Stored(t, wu8Alice, accesslib.RoleAdmin, "project"),
		wu8Stored(t, wu8Bob, accesslib.RoleAdmin, "project"))
	all, err := wu8Run(t, ops, wu8As(t, wu8Bob), "revoke", wu8Alice, wu8Member, "admin")
	if err != nil {
		t.Fatalf("a revoke of a person the member's list does not carry was refused: %v\n%s", err, all)
	}
	if got := wu8Pairs(t, ops.grants.Raw); !slices.Equal(got, []string{wu8As(t, wu8Bob) + " (admin) at project"}) {
		t.Errorf("the node holds %v, want the person off the list revoked", got)
	}
	if len(ops.audits) != 1 {
		t.Fatalf("the revoke filed %d audit entr(ies), want one", len(ops.audits))
	}

	// The linked member is still required: an unlinked one is not this
	// command's to write at all (R11), in either direction.
	unlinked := wu8Unlinked(t, wu8Stored(t, wu8Alice, accesslib.RoleAdmin, "project"),
		wu8Stored(t, wu8Bob, accesslib.RoleAdmin, "project"))
	all, err = wu8Run(t, unlinked, wu8As(t, wu8Bob), "revoke", wu8Alice, wu8Member, "admin")
	if err == nil {
		t.Fatalf("a revoke was written against a member nothing has been mirrored into:\n%s", all)
	}
	if !strings.Contains(all, "fugaro workspace project link") {
		t.Errorf("the refusal does not name the command that links the member:\n%s", all)
	}
	if got := unlinked.wu8Wrote(); got != "" {
		t.Errorf("the refused revoke wrote: %s", got)
	}
}

// TestGrantNamesTheUpdateThatAppliesIt: the grant is a record and the member's
// OWN `fugaro update` is what turns it into bindings (D2801). This machine binds
// nothing and says so — applying another installation's Terraform is exactly
// what D2801's amendment forbade (F7) — and the sentence names the update a run
// would take.
func TestGrantNamesTheUpdateThatAppliesIt(t *testing.T) {
	ops := wu8Rig(t, []workspace.MirrorEntry{wu8Person(wu8Alice)})
	all, err := wu8Run(t, ops, wu8Caller, "grant", wu8Alice, wu8Member, "operator")
	if err != nil {
		t.Fatalf("the grant failed: %v\n%s", err, all)
	}
	for _, want := range []string{wu8Alice, wu8Member, "fugaro update", "the member's own"} {
		if !strings.Contains(all, want) {
			t.Errorf("the run does not say %q:\n%s", want, all)
		}
	}
	if !strings.Contains(all, workspace.PathMemberAudit) {
		t.Errorf("the run does not say where the change was filed (%s):\n%s", workspace.PathMemberAudit, all)
	}
	if strings.Contains(all, "applied") || strings.Contains(all, "terraform") {
		t.Errorf("the run claims something this command did not do:\n%s", all)
	}

	t.Run("a revoke names it too", func(t *testing.T) {
		held := wu8Rig(t, []workspace.MirrorEntry{wu8Person(wu8Alice)},
			wu8Stored(t, wu8Alice, accesslib.RoleAdmin, "project"),
			wu8Stored(t, wu8Bob, accesslib.RoleAdmin, "project"))
		out, err := wu8Run(t, held, wu8As(t, wu8Alice), "revoke", wu8Alice, wu8Member, "admin")
		if err != nil {
			t.Fatalf("the revoke failed: %v\n%s", err, out)
		}
		for _, want := range []string{wu8Alice, wu8Member, "fugaro update"} {
			if !strings.Contains(out, want) {
				t.Errorf("the revoke does not say %q:\n%s", want, out)
			}
		}
	})

	// A coding agent's session is refused (A7): this is a change to who may do
	// what, the credentials are the member admin's own, and the sentence hands
	// over the command a person runs instead — built from the agent's own argv,
	// before any of it is validated, because the refusal is about who is running.
	t.Run("an agent's session is refused", func(t *testing.T) {
		ops := wu8Rig(t, []workspace.MirrorEntry{wu8Person(wu8Alice)})
		stubSelfCommand(t)
		agentSession(t)
		userPairAs(t, wu8Caller)
		out, errOut, err := userCommandOn(t, newWorkspaceUserGrantCmd(ops), wu8Alice, wu8Member, "operator")
		all := accessOut(out, errOut, err)
		if err == nil {
			t.Fatalf("a coding agent's session granted a member role:\n%s", all)
		}
		if want := "fugaro workspace user grant " + wu8Alice + " " + wu8Member + " operator"; !strings.Contains(all, want) {
			t.Errorf("the refusal does not hand over the command (%q):\n%s", want, all)
		}
		if got := ops.wu8Wrote(); got != "" {
			t.Errorf("the refused grant wrote: %s", got)
		}
	})
}

// TestGrantRetriesOnETagMismatch is F23 in the member's path: a node another
// writer moved between the read and the write is re-read and re-judged, never
// overwritten — the same loop `fugaro user grant` makes against this machine's
// node, which is why the two share the judgement and the attempt count.
func TestGrantRetriesOnETagMismatch(t *testing.T) {
	ops := wu8Rig(t, []workspace.MirrorEntry{wu8Person(wu8Alice)})
	ops.staleWrites = 2
	all, err := wu8Run(t, ops, wu8Caller, "grant", wu8Alice, wu8Member, "operator")
	if err != nil {
		t.Fatalf("a grant that lost the node twice was not remade: %v\n%s", err, all)
	}
	if len(ops.writes) != userPairAttempts-1 {
		t.Errorf("the grant made %d write attempt(s), want the change remade once the node stopped moving", len(ops.writes))
	}
	if got := wu8Pairs(t, ops.grants.Raw); !slices.Equal(got, []string{wu8As(t, wu8Alice) + " (operator) at project"}) {
		t.Errorf("the node holds %v after the retries, want the granted pair", got)
	}
	if len(ops.audits) != 1 {
		t.Errorf("the run filed %d audit entr(ies), want one for the change that landed", len(ops.audits))
	}
}
