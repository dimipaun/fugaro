package workspace

// WU12's tests: the drift comparison against the workspace node (D2813), the
// admins it trusts (D2823), the accounts it ignores (D2806, F17), the
// workspace-side bindings R13 names, and the Cloud Audit Logs cross-check
// (D2811).
//
// The fakes are this task's own (the plan's conventions): wu12Authority is the
// C2 authority, narrowed to the two things drift reads, and wu12Changes is the
// Cloud Audit Logs reader, whose only implementation in the tree is
// internal/infra/auditlogs.go. Neither is a stub: a stub would answer the empty
// truth, and a test of a comparison against the empty truth is a test of
// nothing.

import (
	"cmp"
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	// wu12Workspace is the workspace's own project, which is its name (F21).
	wu12Workspace = "ws-aurora"
	// wu12Member and wu12Firebase are a member's two projects: its GCP project
	// and a Firebase project of its own (F2, R10).
	wu12Member   = "repo-alpha"
	wu12Firebase = "repo-alpha-firebase"
	// wu12Alice is a workspace admin, wu12Bob and wu12Carol are people of the
	// workspace, and wu12Domains is the one allowed domain.
	wu12Alice   = "alice@corp.example"
	wu12Bob     = "bob@corp.example"
	wu12Carol   = "carol@corp.example"
	wu12Domains = "corp.example"
	// wu12CI, wu12Coord and wu12Stray are machines: two of them recorded by the
	// node (D2806) and one nobody recorded.
	wu12CI    = "ci@ws-aurora.iam.gserviceaccount.com"
	wu12Coord = "coordinator@ws-aurora.iam.gserviceaccount.com"
	wu12Stray = "stray@ws-aurora.iam.gserviceaccount.com"
	// wu12IAMAccount is the workspace's own IAM account (WU3, D2803), the one
	// whose owner grant is the SA mechanism itself.
	wu12IAMAccount = "fugaro-workspace-iam@ws-aurora.iam.gserviceaccount.com"
	// wu12JobAccount and wu12BuildAccount are two of the member's own Terraform
	// accounts (F17).
	wu12JobAccount   = "fugaro-job@repo-alpha.iam.gserviceaccount.com"
	wu12BuildAccount = "fugaro-build@repo-alpha.iam.gserviceaccount.com"
)

// wu12Now is what "now" is in these tests: the window the cross-check reads is
// counted back from it, so a report's dates are the same on every run.
var wu12Now = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

// wu12Target is the member every test but TestDriftExtraAndMissing reads: one
// GCP project, whose Firebase project is the same one.
var wu12Target = MemberTarget{Project: wu12Member}

// wu12Authority is the C2 authority as drift reads it: which mechanism the
// workspace holds its members by (R8) and the members the live bindings name
// (D2823).
//
// Link, Unlink, AddAdmin and RemoveAdmin fail the test rather than answering:
// drift is a read, and a comparison that linked or granted somebody while
// measuring would be a change made by a command that prints a report.
type wu12Authority struct {
	t      *testing.T
	kind   AuthorityKind
	admins []string
}

func (a wu12Authority) Kind() AuthorityKind { return a.kind }

func (a wu12Authority) AdminBindings(context.Context) ([]string, error) { return a.admins, nil }

func (a wu12Authority) Holds(context.Context, MemberTarget) (bool, error) { return false, nil }

func (a wu12Authority) Link(context.Context, MemberTarget) error { a.unwanted("Link"); return nil }

func (a wu12Authority) Unlink(context.Context, MemberTarget) error { a.unwanted("Unlink"); return nil }

func (a wu12Authority) AddAdmin(context.Context, string) error { a.unwanted("AddAdmin"); return nil }

func (a wu12Authority) RemoveAdmin(context.Context, string) error {
	a.unwanted("RemoveAdmin")
	return nil
}

func (a wu12Authority) unwanted(method string) {
	a.t.Errorf("wu12: drift called Authority.%s; it reads the mechanism and the live admin bindings and changes nothing", method)
}

// wu12Read is one read of the audit logs, so a test can pin that drift asked
// for the member's projects and for the window it should have.
type wu12Read struct {
	project string
	since   time.Time
}

// wu12Changes is the Cloud Audit Logs reader: the changes each project's
// Admin Activity log carries, the reads it was asked for, and the project whose
// read fails if a test wants one to -- a reader the caller may hold no role for
// is the ordinary case of a workspace admin reading a member's log, so a failure
// is a state drift has to answer rather than a broken fake.
type wu12Changes struct {
	changes map[string][]SetIamPolicyChange
	asked   []wu12Read
	fails   string
}

func (c *wu12Changes) SetIamPolicyChanges(_ context.Context, project string, since time.Time) ([]SetIamPolicyChange, error) {
	c.asked = append(c.asked, wu12Read{project, since})
	if project == c.fails {
		return nil, errors.New("wu12: the caller may not read this project's log")
	}
	return c.changes[project], nil
}

// wu12Node is a workspace node these tests build by hand, held to the reader's
// own rules before drift is handed it: a literal the real reader would refuse
// fails here instead of passing as a node no workspace could hold.
func wu12Node(t *testing.T, people []Person, admins, domains []string, grants []StoredGrant) *Roster {
	t.Helper()
	node := &Roster{
		Version:   RosterVersion,
		Mechanism: MechanismFolder,
		FolderID:  "folders/1234567890",
		People:    people,
		Admins:    admins,
		Domains:   domains,
		Grants:    grants,
	}
	slices.SortFunc(node.People, func(a, b Person) int { return strings.Compare(a.Address, b.Address) })
	slices.Sort(node.Admins)
	slices.Sort(node.Domains)
	slices.SortFunc(node.Grants, func(a, b StoredGrant) int {
		return cmp.Or(strings.Compare(a.Project, b.Project), strings.Compare(a.User, b.User))
	})
	if err := node.Validate(); err != nil {
		t.Fatalf("wu12: the node this test builds is not one the reader would accept: %v", err)
	}
	return node
}

// wu12Sources is the comparison's inputs with the two sides every test gives it
// -- the node, the workspace's name, the authority and a still clock -- and
// nothing of the member's own: the tests fill in the policy, the roster, the
// mirror and the logs they are about.
func wu12Sources(node *Roster, a Authority) DriftSources {
	return DriftSources{Workspace: wu12Workspace, Node: node, Authority: a, Now: wu12Now}
}

// wu12Compute runs the comparison and refuses a report built on an error.
func wu12Compute(t *testing.T, src DriftSources, m MemberTarget) DriftReport {
	t.Helper()
	report, err := ComputeDrift(context.Background(), src, m)
	if err != nil {
		t.Fatalf("wu12: ComputeDrift: %v", err)
	}
	return report
}

// wu12Person is a stored grant for a person of the workspace, which is what
// most of these tests write into the node.
func wu12Person(project, user string, role Role) StoredGrant {
	return StoredGrant{Project: project, User: user, Role: role}
}

// wu12Bound is an IAM policy member for a person, as internal/infra's Bindings
// reports one.
func wu12Bound(project, address, role string, kind PrincipalKind) MemberBinding {
	return MemberBinding{Project: project, Principal: iamMember(address), Role: role, Kind: kind}
}

// iamMember is how IAM spells a principal: the address a node holds is inside
// the binding, and drift is what has to see the two as one person.
func iamMember(address string) string {
	if strings.Contains(address, ".gserviceaccount.com") {
		return "serviceAccount:" + address
	}
	return "user:" + address
}

// TestDriftIgnoresFugaroAccounts is F17 and D2806 on drift's side: the member's
// own Terraform accounts and the workspace IAM account's own grants are never a
// difference. The second half of the test proves the exclusion is the one doing
// the work: the same bindings, placed as a principal nobody has placed, are a
// finding -- so the empty report above is the account kinds being ignored and
// not drift reporting nothing at all.
func TestDriftIgnoresFugaroAccounts(t *testing.T) {
	node := wu12Node(t,
		[]Person{{Address: wu12Alice, Kind: KindHuman}},
		[]string{wu12Alice}, []string{wu12Domains},
		[]StoredGrant{wu12Person(wu12Member, wu12Alice, RoleOperator)})
	bindings := []MemberBinding{
		wu12Bound(wu12Member, wu12Alice, "roles/firebasedatabase.viewer", PrincipalPerson),
		wu12Bound(wu12Member, wu12JobAccount, "roles/owner", PrincipalTerraform),
		wu12Bound(wu12Member, wu12BuildAccount, "roles/editor", PrincipalTerraform),
		wu12Bound(wu12Member, wu12IAMAccount, "roles/owner", PrincipalWorkspaceIAM),
		wu12Bound(wu12Member, wu12IAMAccount, "roles/firebasedatabase.admin", PrincipalWorkspaceIAM),
	}
	src := wu12Sources(node, wu12Authority{t: t, kind: AuthorityFolder, admins: []string{iamMember(wu12Alice)}})
	src.Bindings = bindings
	report := wu12Compute(t, src, wu12Target)
	if len(report.Extra) != 0 || len(report.Missing) != 0 {
		t.Fatalf("Fugaro's own accounts are not drift (D2806, F17): extra=%+v missing=%+v", report.Extra, report.Missing)
	}

	unplaced := wu12Sources(node, wu12Authority{t: t, kind: AuthorityFolder, admins: []string{iamMember(wu12Alice)}})
	unplaced.Bindings = slices.Clone(bindings)
	for i := range unplaced.Bindings {
		unplaced.Bindings[i].Kind = PrincipalUnknown
	}
	want := []DriftBinding{
		{Project: wu12Member, Principal: wu12BuildAccount, Role: "roles/editor", Kind: PrincipalUnknown, Implicit: true},
		{Project: wu12Member, Principal: wu12JobAccount, Role: "roles/owner", Kind: PrincipalUnknown, Implicit: true},
		{Project: wu12Member, Principal: wu12IAMAccount, Role: "roles/firebasedatabase.admin", Kind: PrincipalUnknown, Implicit: true},
		{Project: wu12Member, Principal: wu12IAMAccount, Role: "roles/owner", Kind: PrincipalUnknown, Implicit: true},
	}
	if got := wu12Compute(t, unplaced, wu12Target).Extra; !reflect.DeepEqual(got, want) {
		t.Fatalf("a binding nobody has placed is a finding (C2, §3):\n got %+v\nwant %+v", got, want)
	}
}

// TestDriftIgnoresARewrittenMirror is D2813's fence and this task's red step.
// The mirror here is at odds with the workspace node in both directions: it
// names somebody the workspace never admitted, and it has dropped the one person
// the workspace did write a grant for. Nothing in the report follows it -- the
// node is the record, and a comparison against the mirror would report alice's
// live binding as extra and mallory as a member.
func TestDriftIgnoresARewrittenMirror(t *testing.T) {
	node := wu12Node(t,
		[]Person{{Address: wu12Alice, Kind: KindHuman}, {Address: wu12Bob, Kind: KindHuman}},
		[]string{wu12Alice}, []string{wu12Domains},
		[]StoredGrant{wu12Person(wu12Member, wu12Alice, RoleOperator)})
	src := wu12Sources(node, wu12Authority{t: t, kind: AuthorityFolder, admins: []string{iamMember(wu12Alice)}})
	src.Grants = []MemberGrant{{User: wu12Alice, Role: RoleOperator}}
	src.Bindings = []MemberBinding{wu12Bound(wu12Member, wu12Alice, "roles/firebasedatabase.viewer", PrincipalPerson)}
	src.Mirror = []MirrorEntry{{Address: "mallory@evil.example", Kind: KindHuman, External: true}}
	report := wu12Compute(t, src, wu12Target)
	if len(report.Extra) != 0 || len(report.Missing) != 0 {
		t.Fatalf("the member's own admins write the mirror, so it is advisory and never the record (D2813): extra=%+v missing=%+v", report.Extra, report.Missing)
	}
	for _, f := range slices.Concat(report.Extra, report.Missing) {
		if f.Principal == "mallory@evil.example" {
			t.Fatalf("nobody the workspace never admitted is a member because the mirror says so: %+v", f)
		}
	}
}

// TestDriftTrustsOnlyBoundAdmins is D2823: an `Admins` entry is a claim about a
// binding, and only an entry that also holds the live binding is trusted. Bob is
// on the admin list and holds nothing, so his stored grant is not an expectation
// -- and the binding he does hold on the member is drift, because nobody states
// it. Carol holds the binding, so her stored grant IS an expectation and, with
// nothing applied yet, it is what waits for the member's own `fugaro update`
// (D2801, D2818).
func TestDriftTrustsOnlyBoundAdmins(t *testing.T) {
	node := wu12Node(t,
		[]Person{{Address: wu12Alice, Kind: KindHuman}, {Address: wu12Bob, Kind: KindHuman}, {Address: wu12Carol, Kind: KindHuman}},
		[]string{wu12Alice, wu12Bob, wu12Carol}, []string{wu12Domains},
		[]StoredGrant{
			wu12Person(wu12Member, wu12Alice, RoleOperator),
			wu12Person(wu12Member, wu12Bob, RoleViewer),
			wu12Person(wu12Member, wu12Carol, RoleAdmin),
		})
	src := wu12Sources(node, wu12Authority{t: t, kind: AuthorityFolder, admins: []string{iamMember(wu12Alice), iamMember(wu12Carol)}})
	src.Bindings = []MemberBinding{
		wu12Bound(wu12Member, wu12Alice, "roles/firebasedatabase.viewer", PrincipalPerson),
		wu12Bound(wu12Member, wu12Bob, "roles/owner", PrincipalPerson),
	}
	report := wu12Compute(t, src, wu12Target)
	wantMissing := []DriftBinding{{Project: wu12Member, Principal: wu12Carol, Role: string(RoleAdmin), Kind: PrincipalPerson}}
	if !reflect.DeepEqual(report.Missing, wantMissing) {
		t.Fatalf("a trusted admin's stored grant is an expectation (D2823):\n got %+v\nwant %+v", report.Missing, wantMissing)
	}
	wantExtra := []DriftBinding{{Project: wu12Member, Principal: wu12Bob, Role: "roles/owner", Kind: PrincipalPerson, Implicit: true}}
	if !reflect.DeepEqual(report.Extra, wantExtra) {
		t.Fatalf("an untrusted admin's binding is drift, and his grant is not an expectation (D2823):\n got %+v\nwant %+v", report.Extra, wantExtra)
	}
}

// TestDriftReportsAFolderOwnerWhoIsNotAnAdmin is R13: the workspace's own side
// of the binding is compared with the admin list, and a holder of it who is not
// on the list is a finding -- the folder's owners inherit `roles/owner` on every
// project in the folder, and the account's token creators can act as the account
// that owns them. The finding names the project the binding really lands on: the
// member itself under the folder, and the workspace's own under the SA
// mechanism, where the account and its token creators live (R8).
func TestDriftReportsAFolderOwnerWhoIsNotAnAdmin(t *testing.T) {
	node := wu12Node(t,
		[]Person{{Address: wu12Alice, Kind: KindHuman}},
		[]string{wu12Alice}, []string{wu12Domains},
		[]StoredGrant{wu12Person(wu12Member, wu12Alice, RoleOperator)})
	rogue := "rogue@elsewhere.example"
	for _, tc := range []struct {
		name string
		kind AuthorityKind
		want []DriftBinding
	}{
		{
			"the folder's owners",
			AuthorityFolder,
			[]DriftBinding{{Project: wu12Member, Principal: rogue, Role: driftFolderOwnerRole, Kind: PrincipalPerson, External: true, Implicit: true}},
		},
		{
			"the account's token creators",
			AuthoritySA,
			[]DriftBinding{{Project: wu12Workspace, Principal: rogue, Role: driftTokenCreatorRole, Kind: PrincipalPerson, External: true}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := wu12Sources(node, wu12Authority{t: t, kind: tc.kind, admins: []string{iamMember(wu12Alice), iamMember(rogue)}})
			src.Bindings = []MemberBinding{wu12Bound(wu12Member, wu12Alice, "roles/firebasedatabase.viewer", PrincipalPerson)}
			report := wu12Compute(t, src, wu12Target)
			if !reflect.DeepEqual(report.Extra, tc.want) {
				t.Fatalf("a holder of the live admin binding who is not on the admin list (D2823, R13):\n got %+v\nwant %+v", report.Extra, tc.want)
			}
			if len(report.Missing) != 0 {
				t.Fatalf("nothing the record states is missing here: %+v", report.Missing)
			}
		})
	}
}

// TestDriftChecksMachinesAgainstTheRecord is D2806: a machine's real IAM is the
// workspace owner's own, so the node records what it expects it to hold and
// drift compares that with the live binding, carrying the role the node records.
// A machine the node records and the policy does not is missing; one the policy
// carries and the node does not is extra.
func TestDriftChecksMachinesAgainstTheRecord(t *testing.T) {
	node := wu12Node(t,
		[]Person{
			{Address: wu12Alice, Kind: KindHuman},
			{Address: wu12CI, Kind: KindSA, Roles: []ProjectRole{{Project: wu12Member, Role: RoleOperator}}},
			{Address: wu12Coord, Kind: KindSA, Roles: []ProjectRole{{Project: wu12Member, Role: RoleAdmin}}},
		},
		[]string{wu12Alice}, []string{wu12Domains}, nil)
	src := wu12Sources(node, wu12Authority{t: t, kind: AuthorityFolder, admins: []string{iamMember(wu12Alice)}})
	src.Bindings = []MemberBinding{
		wu12Bound(wu12Member, wu12CI, "roles/firebasedatabase.viewer", PrincipalMachine),
		wu12Bound(wu12Member, wu12Stray, "roles/owner", PrincipalMachine),
	}
	report := wu12Compute(t, src, wu12Target)
	wantMissing := []DriftBinding{{Project: wu12Member, Principal: wu12Coord, Role: string(RoleAdmin), Kind: PrincipalMachine}}
	if !reflect.DeepEqual(report.Missing, wantMissing) {
		t.Fatalf("a machine the node records and the policy does not carry (D2806):\n got %+v\nwant %+v", report.Missing, wantMissing)
	}
	wantExtra := []DriftBinding{{Project: wu12Member, Principal: wu12Stray, Role: "roles/owner", Kind: PrincipalMachine, Implicit: true}}
	if !reflect.DeepEqual(report.Extra, wantExtra) {
		t.Fatalf("a machine the policy carries and the node does not record (D2806):\n got %+v\nwant %+v", report.Extra, wantExtra)
	}
}

// TestDriftExtraAndMissing is the comparison itself: a roster grant the record
// does not state is extra (D2805's unmapped binding, which D2813 makes the reason
// the installation-level commands refuse on a linked member), an unexplained
// binding is extra, and a record's grant the policy does not carry is missing --
// on both of the member's projects (F2, R10), with an outside-domain principal
// flagged and an owner named as an implicit admin (threat-model.md §3).
func TestDriftExtraAndMissing(t *testing.T) {
	node := wu12Node(t,
		[]Person{{Address: wu12Alice, Kind: KindHuman}, {Address: wu12Bob, Kind: KindHuman}},
		[]string{wu12Alice}, []string{wu12Domains},
		[]StoredGrant{
			wu12Person(wu12Member, wu12Alice, RoleOperator),
			wu12Person(wu12Member, wu12Bob, RoleOperator),
		})
	src := wu12Sources(node, wu12Authority{t: t, kind: AuthorityFolder, admins: []string{iamMember(wu12Alice)}})
	src.Grants = []MemberGrant{
		{User: wu12Alice, Role: RoleOperator},
		{User: "mallory@corp.example", Role: RoleViewer},
	}
	src.Bindings = []MemberBinding{
		wu12Bound(wu12Member, wu12Alice, "roles/firebasedatabase.viewer", PrincipalPerson),
		wu12Bound(wu12Member, "mallory@corp.example", "roles/viewer", PrincipalPerson),
		wu12Bound(wu12Member, "casey@corp.example", "roles/browser", PrincipalPerson),
		wu12Bound(wu12Member, "outsider@elsewhere.example", "roles/editor", PrincipalPerson),
		wu12Bound(wu12Firebase, "frank@corp.example", "roles/owner", PrincipalPerson),
	}
	report := wu12Compute(t, src, MemberTarget{Project: wu12Member, Firebase: wu12Firebase})
	wantExtra := []DriftBinding{
		{Project: wu12Member, Principal: "casey@corp.example", Role: "roles/browser", Kind: PrincipalPerson},
		{Project: wu12Member, Principal: "mallory@corp.example", Role: string(RoleViewer), Kind: PrincipalPerson},
		{Project: wu12Member, Principal: "outsider@elsewhere.example", Role: "roles/editor", Kind: PrincipalPerson, External: true, Implicit: true},
		{Project: wu12Firebase, Principal: "frank@corp.example", Role: "roles/owner", Kind: PrincipalPerson, Implicit: true},
	}
	if !reflect.DeepEqual(report.Extra, wantExtra) {
		t.Fatalf("extra:\n got %+v\nwant %+v", report.Extra, wantExtra)
	}
	wantMissing := []DriftBinding{{Project: wu12Member, Principal: wu12Bob, Role: string(RoleOperator), Kind: PrincipalPerson}}
	if !reflect.DeepEqual(report.Missing, wantMissing) {
		t.Fatalf("missing:\n got %+v\nwant %+v", report.Missing, wantMissing)
	}
}

// TestDriftNamesAnImplicitAdminOnAStatedPrincipal is D689 and R13 on the side
// presence alone cannot see. `roles/owner`, `roles/editor` and the budget
// admin's role are an ADMIN's roles -- docs/roles/spec.md §2 gives an admin
// `roles/owner` and `roles/editor` and the budget admin's write over /config, and
// gives a viewer or an operator none of them -- so a principal the record states
// as a viewer who holds roles/owner besides is an admin of the member that the
// record does not say it made, and the change that did it is named. Bob, whom the
// record states as an admin, holds the same role legitimately, and neither half
// reports him: that is the rule and not a hole in it.
func TestDriftNamesAnImplicitAdminOnAStatedPrincipal(t *testing.T) {
	node := wu12Node(t,
		[]Person{{Address: wu12Alice, Kind: KindHuman}, {Address: wu12Bob, Kind: KindHuman}},
		[]string{wu12Alice}, []string{wu12Domains},
		[]StoredGrant{
			wu12Person(wu12Member, wu12Alice, RoleViewer),
			wu12Person(wu12Member, wu12Bob, RoleAdmin),
		})
	at := wu12Now.Add(-time.Hour)
	src := wu12Sources(node, wu12Authority{t: t, kind: AuthorityFolder, admins: []string{iamMember(wu12Alice)}})
	src.Bindings = []MemberBinding{
		wu12Bound(wu12Member, wu12Alice, "roles/firebasedatabase.viewer", PrincipalPerson),
		wu12Bound(wu12Member, wu12Alice, "roles/owner", PrincipalPerson),
		wu12Bound(wu12Member, wu12Bob, "roles/owner", PrincipalPerson),
	}
	src.Changes = &wu12Changes{changes: map[string][]SetIamPolicyChange{
		wu12Member: {{
			Project: wu12Member, Principal: "owner@corp.example", Member: iamMember(wu12Alice),
			Role: "roles/owner", Entry: "hand-added-entry", At: at,
		}},
	}}
	report := wu12Compute(t, src, wu12Target)
	wantExtra := []DriftBinding{{Project: wu12Member, Principal: wu12Alice, Role: "roles/owner", Kind: PrincipalPerson, Implicit: true}}
	if !reflect.DeepEqual(report.Extra, wantExtra) {
		t.Fatalf("a stated viewer holding an admin's role is drift (D689, R13):\n got %+v\nwant %+v", report.Extra, wantExtra)
	}
	wantChanges := []ConsoleChange{{Project: wu12Member, Principal: "alice@corp.example", Role: "roles/owner", Entry: "hand-added-entry", At: at}}
	if !reflect.DeepEqual(report.ConsoleChanges, wantChanges) {
		t.Fatalf("the change that made a stated viewer an admin is named (D2811, R13):\n got %+v\nwant %+v", report.ConsoleChanges, wantChanges)
	}
}

// TestDriftReadsTheRecordsRoleOnBothProjects is the implicit admin rule on a
// member whose Firebase project is a project of its own. A node grant is keyed to
// the member's GCP project, so the member's own roster is the only statement the
// Firebase project has -- and the member's own admins hold that roster's write,
// which is why the record is what the rule reads (D2813). Bob's roster entry says
// admin and the record says viewer: the budget admin's role the update applied to
// the Firebase project is drift beside his own viewer grant, and the change that
// added it is named.
func TestDriftReadsTheRecordsRoleOnBothProjects(t *testing.T) {
	node := wu12Node(t,
		[]Person{{Address: wu12Alice, Kind: KindHuman}, {Address: wu12Bob, Kind: KindHuman}},
		[]string{wu12Alice}, []string{wu12Domains},
		[]StoredGrant{wu12Person(wu12Member, wu12Bob, RoleViewer)})
	at := wu12Now.Add(-time.Hour)
	src := wu12Sources(node, wu12Authority{t: t, kind: AuthorityFolder, admins: []string{iamMember(wu12Alice)}})
	src.Grants = []MemberGrant{{User: wu12Bob, Role: RoleAdmin}}
	src.Bindings = []MemberBinding{
		wu12Bound(wu12Member, wu12Bob, "roles/firebasedatabase.viewer", PrincipalPerson),
		wu12Bound(wu12Firebase, wu12Bob, "roles/firebasedatabase.admin", PrincipalPerson),
	}
	src.Changes = &wu12Changes{changes: map[string][]SetIamPolicyChange{
		wu12Firebase: {{
			Project: wu12Firebase, Principal: "owner@corp.example", Member: iamMember(wu12Bob),
			Role: "roles/firebasedatabase.admin", Entry: "budget-entry", At: at,
		}},
	}}
	report := wu12Compute(t, src, MemberTarget{Project: wu12Member, Firebase: wu12Firebase})
	wantExtra := []DriftBinding{{Project: wu12Firebase, Principal: wu12Bob, Role: "roles/firebasedatabase.admin", Kind: PrincipalPerson, Implicit: true}}
	if !reflect.DeepEqual(report.Extra, wantExtra) {
		t.Fatalf("the record's role for a person governs both of its projects (D2813, D689):\n got %+v\nwant %+v", report.Extra, wantExtra)
	}
	wantChanges := []ConsoleChange{{Project: wu12Firebase, Principal: "bob@corp.example", Role: "roles/firebasedatabase.admin", Entry: "budget-entry", At: at}}
	if !reflect.DeepEqual(report.ConsoleChanges, wantChanges) {
		t.Fatalf("the change that made a stated viewer a budget admin is named (D2811, R13):\n got %+v\nwant %+v", report.ConsoleChanges, wantChanges)
	}
}

// TestDriftRosterExplainsTheFirebaseProject is F2 and R10 on the roster's side:
// a member whose Firebase project is a project of its own derives that project's
// bindings from the one roster it holds, so a person the roster names is not
// extra there. Without that, every healthy member of this shape reports its own
// people as rows -- and reconcile removes extras on the member's roster (WU13) --
// and the cross-check names the update that applied them as a hand-made change.
func TestDriftRosterExplainsTheFirebaseProject(t *testing.T) {
	node := wu12Node(t,
		[]Person{{Address: wu12Alice, Kind: KindHuman}},
		[]string{wu12Alice}, []string{wu12Domains},
		[]StoredGrant{wu12Person(wu12Member, wu12Alice, RoleOperator)})
	at := wu12Now.Add(-time.Hour)
	src := wu12Sources(node, wu12Authority{t: t, kind: AuthorityFolder, admins: []string{iamMember(wu12Alice)}})
	src.Grants = []MemberGrant{{User: wu12Alice, Role: RoleOperator}}
	src.Bindings = []MemberBinding{
		wu12Bound(wu12Member, wu12Alice, "roles/firebasedatabase.viewer", PrincipalPerson),
		// The three the member's Firebase root derives from the roster.
		wu12Bound(wu12Firebase, wu12Alice, "roles/datastore.viewer", PrincipalPerson),
		wu12Bound(wu12Firebase, wu12Alice, "roles/firebasedatabase.viewer", PrincipalPerson),
		wu12Bound(wu12Firebase, wu12Alice, "roles/serviceusage.serviceUsageConsumer", PrincipalPerson),
	}
	src.Changes = &wu12Changes{changes: map[string][]SetIamPolicyChange{
		wu12Firebase: {{
			Project: wu12Firebase, Principal: "owner@corp.example", Member: iamMember(wu12Alice),
			Role: "roles/firebasedatabase.viewer", Entry: "firebase-entry", At: at,
		}},
	}}
	report := wu12Compute(t, src, MemberTarget{Project: wu12Member, Firebase: wu12Firebase})
	if len(report.Extra) != 0 || len(report.Missing) != 0 {
		t.Fatalf("the member's own roster explains its Firebase project:\n extra %+v\n missing %+v", report.Extra, report.Missing)
	}
	if len(report.ConsoleChanges) != 0 {
		t.Fatalf("the member's own update on its Firebase project is not a change made in the console: %+v", report.ConsoleChanges)
	}
}

// TestDriftNamesConsoleChangeNotUpdate is the cross-check (D2811, R13): a
// SetIamPolicy whose resulting binding is drift and which no trail of Fugaro's
// holds is named, and a member's own `fugaro update` never is -- the binding it
// applied is the record's, so it is not drift in the first place. The second
// change below added somebody nobody states and no trail records, and it is the
// one named; the third added somebody nobody states and a trail of Fugaro's
// holds, and it is not; the last two added Fugaro's own accounts, which are
// dropped from the live side before anything is compared and are therefore
// named by neither side -- asking the live bindings about them would answer
// with silence and call the member's own plumbing a console change.
//
// A read that fails is the same kind of state: a window nobody saw, which the
// report carries as Unmeasured and which does not take the Extra with it.
func TestDriftNamesConsoleChangeNotUpdate(t *testing.T) {
	node := wu12Node(t,
		[]Person{{Address: wu12Alice, Kind: KindHuman}},
		[]string{wu12Alice}, []string{wu12Domains},
		[]StoredGrant{wu12Person(wu12Member, wu12Alice, RoleOperator)})
	owner := "owner@corp.example"
	at := wu12Now.Add(-2 * time.Hour)
	changes := &wu12Changes{changes: map[string][]SetIamPolicyChange{
		wu12Member: {
			// The member's own update: the record's grant, applied.
			{Project: wu12Member, Principal: owner, Member: iamMember(wu12Alice), Role: "roles/firebasedatabase.viewer", Entry: "update-entry", At: at},
			// A change in Google's console that added somebody nobody states.
			{Project: wu12Member, Principal: owner, Member: "user:mallory@corp.example", Role: "roles/editor", Entry: "console-entry", At: at.Add(time.Minute)},
			// One this workspace's own trail recorded, so no cross-check is owed.
			{Project: wu12Member, Principal: owner, Member: "user:dave@corp.example", Role: "roles/viewer", Entry: "trailed-entry", At: at.Add(2 * time.Minute)},
			// Fugaro's own accounts, which the record states for nobody because
			// the update that writes them is Fugaro's own Terraform.
			{Project: wu12Member, Principal: owner, Member: iamMember(wu12JobAccount), Role: "roles/owner", Entry: "job-entry", At: at.Add(3 * time.Minute)},
			{Project: wu12Member, Principal: owner, Member: iamMember(wu12IAMAccount), Role: "roles/owner", Entry: "iam-entry", At: at.Add(4 * time.Minute)},
		},
	}}
	src := wu12Sources(node, wu12Authority{t: t, kind: AuthorityFolder, admins: []string{iamMember(wu12Alice)}})
	src.Changes = changes
	src.Bindings = []MemberBinding{
		wu12Bound(wu12Member, wu12Alice, "roles/firebasedatabase.viewer", PrincipalPerson),
		wu12Bound(wu12Member, "dave@corp.example", "roles/viewer", PrincipalPerson),
		wu12Bound(wu12Member, wu12JobAccount, "roles/owner", PrincipalTerraform),
		wu12Bound(wu12Member, wu12IAMAccount, "roles/owner", PrincipalWorkspaceIAM),
	}
	src.Trail = []AuditEvent{{
		Version: AuditEventVersion, ID: "evt-1", Time: at.Add(2 * time.Minute), Actor: owner,
		Action: ActionGrantWrite, Project: wu12Member, Subject: "dave@corp.example", Role: RoleViewer,
	}}
	report := wu12Compute(t, src, wu12Target)
	want := []ConsoleChange{{Project: wu12Member, Principal: "mallory@corp.example", Role: "roles/editor", Entry: "console-entry", At: at.Add(time.Minute)}}
	if !reflect.DeepEqual(report.ConsoleChanges, want) {
		t.Fatalf("a change no trail of Fugaro's holds, whose binding is drift (D2811, R13):\n got %+v\nwant %+v", report.ConsoleChanges, want)
	}
	if got, want := changes.asked, []wu12Read{{wu12Member, wu12Now.Add(-DriftAuditWindow)}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the cross-check reads the member's own projects for the window it was given:\n got %+v\nwant %+v", got, want)
	}

	// A caller with no reader: the cross-check was not made, and the report says
	// so rather than leaving an empty list to read as clean.
	blind := wu12Sources(node, wu12Authority{t: t, kind: AuthorityFolder, admins: []string{iamMember(wu12Alice)}})
	blind.Bindings = src.Bindings
	blindReport := wu12Compute(t, blind, wu12Target)
	if len(blindReport.ConsoleChanges) != 0 || len(blindReport.Unmeasured) == 0 {
		t.Fatalf("no reader means the cross-check is unmeasured, not clean: %+v", blindReport)
	}

	// A reader whose read fails on one project: the window nobody could read is
	// named, one entry for the project, and the report keeps what it did measure
	// -- the read is a sample of the window, so losing Extra and Missing to it
	// would be losing the comparison the caller asked for.
	unread := wu12Sources(node, wu12Authority{t: t, kind: AuthorityFolder, admins: []string{iamMember(wu12Alice)}})
	unread.Bindings = src.Bindings
	unread.Changes = &wu12Changes{fails: wu12Member}
	unreadReport := wu12Compute(t, unread, wu12Target)
	if len(unreadReport.ConsoleChanges) != 0 {
		t.Fatalf("a project whose log could not be read names no change: %+v", unreadReport.ConsoleChanges)
	}
	if len(unreadReport.Unmeasured) != 1 || !strings.Contains(unreadReport.Unmeasured[0], wu12Member) {
		t.Fatalf("a failed read is unmeasured, named by the project it was for:\n got %+v", unreadReport.Unmeasured)
	}
	wantExtra := []DriftBinding{{Project: wu12Member, Principal: "dave@corp.example", Role: "roles/viewer", Kind: PrincipalPerson}}
	if !reflect.DeepEqual(unreadReport.Extra, wantExtra) {
		t.Fatalf("a failed read does not take the comparison with it:\n got %+v\nwant %+v", unreadReport.Extra, wantExtra)
	}
}
