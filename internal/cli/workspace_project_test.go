package cli

// The tests of WU5's own door, `fugaro workspace project add`. Every one of them
// is hermetic: the workspace's roster, its audit trail, the member's own database
// and the authority that holds it are fakes declared here, the local config is
// the workspace family's own fixture (workspaceOwnerHome), and the command is
// built directly with its seams rather than through the root command I1 wires
// (newWorkspaceProjectAddCmd), so no test needs a cloud, a network or a home
// directory.
//
// --no-apply is passed everywhere a write is meant to land: the change's own
// steps are the workspace's own project and its published record, and the tests
// here are about what the command decided and wrote, not about the converge
// behind it (chainApply's own tests are the converge's).

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// wu5AddArgs is one `workspace project add` of the member aurora into the
// workspace workspace-alpha, carrying `member add`'s own flags with the same
// values its fixture uses: the tests that vary one flag vary one flag.
func wu5AddArgs(extra ...string) []string {
	args := []string{"aurora", "--workspace", "workspace-alpha",
		"--job-service-account", "marker-job@aurora-gcp.iam.gserviceaccount.com",
		"--job-repos", "marker-job@aurora-gcp.iam.gserviceaccount.com=marker-org/aurora",
		"--firebase-project", "aurora-fb",
		"--fp", "aurora",
		"--exchange-url", "https://exchange.example.com/v1",
		"--no-apply"}
	return append(args, extra...)
}

// wu5Run runs the command against a set of seams with no terminal on stdin and
// no coding-agent marker read from it (the fixtures clear the markers), and
// answers what it printed and what it returned.
func wu5Run(t *testing.T, seams workspaceProjectAddSeams, args ...string) (string, error) {
	t.Helper()
	cmd := newWorkspaceProjectAddCmd(seams)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// wu5Cloud is the whole cloud side of one test: the workspace's roster node, its
// audit trail, the member's own documents and the authority, plus the order the
// writes landed in — which is how a test says that nothing was written where
// nothing should be.
type wu5Cloud struct {
	node   wu5Node
	store  *wu5Store
	audit  *wu5Audit
	auth   *wu5Authority
	member *wu5Member
	order  []string
	// regions and targets are what the member seam was opened with, so a test
	// can say the region and the two projects travelled beside the command.
	targets []workspace.MemberTarget
	regions []string
	// opened is how many times each seam was asked for, which is what
	// TestProjectAddOnlyOnTheTablesMachine reads to say none of them was.
	opened int
}

func wu5NewCloud() *wu5Cloud {
	c := &wu5Cloud{}
	c.store = &wu5Store{cloud: c}
	c.audit = &wu5Audit{cloud: c}
	c.member = &wu5Member{cloud: c}
	c.auth = &wu5Authority{}
	return c
}

func (c *wu5Cloud) seams(t *testing.T) workspaceProjectAddSeams {
	t.Helper()
	return workspaceProjectAddSeams{
		store: func(ctx context.Context, ws string) (workspace.Store, error) {
			c.opened++
			return c.store, nil
		},
		audit: func(ctx context.Context, ws string) (workspace.AuditSink, error) {
			c.opened++
			return c.audit, nil
		},
		authority: func(ctx context.Context, ws string) (workspace.Authority, error) {
			c.opened++
			return c.auth, nil
		},
		member: func(ctx context.Context, _ *localcfg.Config, m workspace.MemberTarget, region string) (workspace.MemberOps, error) {
			c.opened++
			c.targets = append(c.targets, m)
			c.regions = append(c.regions, region)
			return c.member, nil
		},
		caller: func(ctx context.Context) (string, error) { return "owner@example.com", nil },
		now:    func() time.Time { return time.Unix(1700000000, 0).UTC() },
		rand:   strings.NewReader("wu5-audit-randomness"),
	}
}

func (c *wu5Cloud) wrote(what string) { c.order = append(c.order, what) }

// wu5Node is one node of a fake database: the bytes, the ETag and whether it is
// there at all.
type wu5Node struct {
	raw   []byte
	etag  string
	found bool
}

type wu5Doc struct {
	node  wu5Node
	reads int
}

// wu5Store is the workspace's own roster node (C1's Store). Its Path is the
// roster contract's own spelling, so a sentence naming it is the real one.
type wu5Store struct {
	cloud *wu5Cloud
	node  wu5Node
	doc   wu5Doc
}

func (s *wu5Store) Path() string { return "workspaceRosters/workspace-alpha" }

func (s *wu5Store) Read(ctx context.Context) (workspace.Revision, []byte, error) {
	s.doc.reads++
	return workspace.Revision{ETag: s.node.etag, Found: s.node.found}, s.node.raw, nil
}

func (s *wu5Store) Write(ctx context.Context, rev workspace.Revision, raw []byte) error {
	s.node = wu5Node{raw: raw, etag: "etag-roster-" + time.Now().Format("150405.000000000"), found: true}
	s.cloud.wrote("roster")
	return nil
}

// wu5Audit is the workspace's own trail (C1's AuditSink): a sink that creates
// and never updates, as the contract says.
type wu5Audit struct {
	cloud  *wu5Cloud
	events map[string][]byte
	ids    []string
}

func (a *wu5Audit) Create(ctx context.Context, id string, event []byte) error {
	if a.events == nil {
		a.events = map[string][]byte{}
	}
	if _, ok := a.events[id]; ok {
		return os.ErrExist
	}
	a.events[id] = event
	a.ids = append(a.ids, id)
	a.cloud.wrote("audit")
	return nil
}

// wu5Authority is the mechanism that holds a member (C2's Authority): Holds
// answers what the test set, and AdminBindings answers the live bindings D2823
// trusts an admin entry by -- in the spelling the contract's own two mechanisms
// answer in, the `user:` member a grant names (WU2's folder owners, WU20's token
// creators), because a fixture that answers the node's bare spelling is a
// fixture that hides a comparison of two spellings of nobody. The methods this
// command does not read answer the empty truth, declared here rather than taken
// from the contract's stub, which I1 deleted (the plan's own rule: a task's
// fakes are declared in its own _test.go).
type wu5Authority struct {
	holds    bool
	bindings []string
}

// Kind is the empty kind: the contract names no value for an authority with no
// mechanism, and this command reads no kind off it.
func (a *wu5Authority) Kind() workspace.AuthorityKind { return "" }

func (a *wu5Authority) Link(context.Context, workspace.MemberTarget) error   { return nil }
func (a *wu5Authority) Unlink(context.Context, workspace.MemberTarget) error { return nil }

func (a *wu5Authority) AddAdmin(context.Context, string) error    { return nil }
func (a *wu5Authority) RemoveAdmin(context.Context, string) error { return nil }

func (a *wu5Authority) Holds(ctx context.Context, m workspace.MemberTarget) (bool, error) {
	return a.holds, nil
}

func (a *wu5Authority) AdminBindings(ctx context.Context) ([]string, error) {
	return a.bindings, nil
}

// wu5Member is the member's own database (C2's MemberOps): its grants node, its
// mirror, and what was written into them. The four methods this command never
// calls answer the empty truth, declared here rather than taken from the
// contract's stub, which I1 deleted (the plan's own rule: a task's fakes are
// declared in its own _test.go).
type wu5Member struct {
	cloud  *wu5Cloud
	grants wu5Node
	mirror wu5Node
	// writes are the bytes of each write, in order, and grantsWrites/mirrorWrites
	// count them.
	writes      []string
	grantsWrite []byte
	mirrorWrite []byte
}

func (m *wu5Member) ReadGrants(ctx context.Context, target workspace.MemberTarget) (workspace.MemberDoc, error) {
	return workspace.MemberDoc{Raw: m.grants.raw, ETag: m.grants.etag, Found: m.grants.found}, nil
}

func (m *wu5Member) WriteGrants(ctx context.Context, target workspace.MemberTarget, doc workspace.MemberDoc, raw []byte) error {
	m.grants = wu5Node{raw: raw, etag: "etag-grants-1", found: true}
	m.grantsWrite = raw
	m.writes = append(m.writes, "grants")
	m.cloud.wrote("grants")
	return nil
}

func (m *wu5Member) ReadMirror(ctx context.Context, target workspace.MemberTarget) (workspace.MemberDoc, error) {
	return workspace.MemberDoc{Raw: m.mirror.raw, ETag: m.mirror.etag, Found: m.mirror.found}, nil
}

func (m *wu5Member) WriteMirror(ctx context.Context, target workspace.MemberTarget, doc workspace.MemberDoc, raw []byte) error {
	m.mirror = wu5Node{raw: raw, etag: "etag-mirror-1", found: true}
	m.mirrorWrite = raw
	m.writes = append(m.writes, "mirror")
	m.cloud.wrote("mirror")
	return nil
}

func (m *wu5Member) Bindings(context.Context, workspace.MemberTarget) ([]workspace.MemberBinding, error) {
	return nil, nil
}

func (m *wu5Member) PendingApply(context.Context, workspace.MemberTarget, workspace.ApplyWant) (workspace.Pending, error) {
	return workspace.Pending{}, nil
}

func (m *wu5Member) Offboard(context.Context, workspace.MemberTarget, string) error { return nil }

func (m *wu5Member) Audit(context.Context, workspace.MemberTarget, string, []byte) error {
	return nil
}

// wu5RosterDoc is the workspace's own roster as a node holds it: the same
// document EncodeRoster writes, so every test's fixture is one this build would
// really read.
func wu5RosterDoc(t *testing.T, r workspace.Roster) []byte {
	t.Helper()
	r.Version = workspace.RosterVersion
	if r.Mechanism == "" {
		// The service-account fallback needs no folder id and no IAM: nothing in
		// this command reads the mechanism but a test that says so.
		r.Mechanism = workspace.MechanismSA
	}
	raw, err := workspace.EncodeRoster(&r)
	if err != nil {
		t.Fatalf("building the fixture roster: %v", err)
	}
	return raw
}

// wu5NodeDoc is a member's own document as its database holds it.
func wu5NodeDoc(t *testing.T, raw []byte) wu5Node {
	t.Helper()
	return wu5Node{raw: raw, etag: "etag-1", found: true}
}

// wu5GrantsDoc is a member's own grants node, encoded by the package that reads
// it.
func wu5GrantsDoc(t *testing.T, grants []accesslib.Grant) wu5Node {
	t.Helper()
	raw, err := accesslib.Encode(grants)
	if err != nil {
		t.Fatalf("building the fixture grants: %v", err)
	}
	return wu5NodeDoc(t, raw)
}

// wu5ProjectGrant is one project-scope grant in a member's roster.
func wu5ProjectGrant(address string, role accesslib.Role) accesslib.Grant {
	member, err := accesslib.Member(address)
	if err != nil {
		panic(err)
	}
	return accesslib.Grant{User: member, Role: role, Scope: accesslib.Scope{Kind: accesslib.ScopeProject}}
}

// wu5MemberOf reads the entry the workspace's table declares for a project.
func wu5MemberOf(t *testing.T, path, project string) localcfg.WorkspaceMember {
	t.Helper()
	lc, err := localcfg.Parse([]byte(readFileString(t, path)))
	if err != nil {
		t.Fatalf("reading the workspace's own config: %v", err)
	}
	if lc.WorkspaceMembers == nil {
		t.Fatalf("the workspace's table was not written:\n%s", readFileString(t, path))
	}
	return (*lc.WorkspaceMembers)[project]
}

// wu5Roster reads the workspace's node back the way the command wrote it.
func wu5Roster(t *testing.T, cloud *wu5Cloud) *workspace.Roster {
	t.Helper()
	if !cloud.store.node.found {
		t.Fatal("the workspace's roster node is not there")
	}
	r, err := workspace.DecodeRoster(cloud.store.node.raw)
	if err != nil {
		t.Fatalf("reading the workspace's roster back: %v", err)
	}
	return r
}

// wu5Person is one person of a roster, by address, with whether they are there.
func wu5Person(r *workspace.Roster, address string) (workspace.Person, bool) {
	for _, p := range r.People {
		if p.Address == address {
			return p, true
		}
	}
	return workspace.Person{}, false
}

// wu5Mirror reads the member's mirror back as its document.
func wu5Mirror(t *testing.T, cloud *wu5Cloud) []workspace.MirrorEntry {
	t.Helper()
	var doc struct {
		Version int                     `json:"version"`
		Members []workspace.MirrorEntry `json:"members"`
	}
	if err := json.Unmarshal(cloud.member.mirror.raw, &doc); err != nil {
		t.Fatalf("reading the member's mirror back: %v", err)
	}
	if doc.Version != workspaceMirrorVersion {
		t.Errorf("the mirror says version %d, want %d", doc.Version, workspaceMirrorVersion)
	}
	return doc.Members
}

// wu5Linked is the fixture of a member the workspace holds: a roster with the
// owner as its admin and one allowed domain, the member's own grants, and the
// authority answering that it holds the project.
func wu5Linked(t *testing.T, grants []accesslib.Grant) (*wu5Cloud, string) {
	t.Helper()
	_, _, owner := workspaceOwnerHome(t)
	cloud := wu5NewCloud()
	cloud.store.node = wu5NodeDoc(t, wu5RosterDoc(t, workspace.Roster{
		People: []workspace.Person{
			{Address: "ops@example.com", Kind: workspace.KindHuman},
			{Address: "owner@example.com", Kind: workspace.KindHuman},
		},
		Admins:  []string{"ops@example.com", "owner@example.com"},
		Domains: []string{"example.com"},
	}))
	cloud.auth.holds = true
	cloud.auth.bindings = []string{"user:owner@example.com"}
	cloud.member.grants = wu5GrantsDoc(t, grants)
	return cloud, owner
}

// TestProjectAddAdoptsGrants is the add's own half: the member's grantees become
// people of the workspace, marked external where their domain is not one of the
// workspace's allowed ones, and nothing of the member's own grants is touched
// (D2805). The workspace's admin who holds the live binding gets their stored
// grant written into the member's roster and marked in the workspace's node
// (D2818), and the member's mirror names the workspace's people (D2813).
func TestProjectAddAdoptsGrants(t *testing.T) {
	cloud, owner := wu5Linked(t, []accesslib.Grant{
		wu5ProjectGrant("alice@example.com", accesslib.RoleViewer),
		wu5ProjectGrant("guest@other.test", accesslib.RoleOperator),
	})
	before := readFileString(t, owner)

	out, err := wu5Run(t, cloud.seams(t), wu5AddArgs("--external")...)
	if err != nil {
		t.Fatalf("workspace project add: %v\n%s", err, out)
	}
	if !strings.Contains(out, "adopted 2 people") || !strings.Contains(out, "alice@example.com") || !strings.Contains(out, "guest@other.test") {
		t.Errorf("the add does not say who it adopted:\n%s", out)
	}

	roster := wu5Roster(t, cloud)
	alice, ok := wu5Person(roster, "alice@example.com")
	if !ok {
		t.Fatalf("alice@example.com is not a person of the workspace's roster:\n%+v", roster.People)
	}
	if alice.External {
		t.Error("alice@example.com is inside the workspace's allowed domains and is marked external")
	}
	if alice.Kind != workspace.KindHuman {
		t.Errorf("alice@example.com is a %q, want a person of the workspace", alice.Kind)
	}
	guest, ok := wu5Person(roster, "guest@other.test")
	if !ok {
		t.Fatalf("guest@other.test is not a person of the workspace's roster:\n%+v", roster.People)
	}
	if !guest.External {
		t.Error("guest@other.test is outside the workspace's allowed domains and is not marked external (D2812)")
	}
	// The people the node already held are still there, and the list is the
	// sorted set the node's own rules require.
	if _, ok := wu5Person(roster, "owner@example.com"); !ok {
		t.Error("the workspace's own owner left the roster")
	}
	if !slices.IsSortedFunc(roster.People, func(p, q workspace.Person) int { return strings.Compare(p.Address, q.Address) }) {
		t.Errorf("the roster's people are not sorted by address:\n%+v", roster.People)
	}

	// D2818's mark: the admin who holds the live binding, and nobody else.
	if len(roster.Grants) != 1 || roster.Grants[0].User != "owner@example.com" || roster.Grants[0].Project != "aurora" || roster.Grants[0].Role != workspace.RoleAdmin {
		t.Errorf("the workspace's marks are %+v, want one admin grant for owner@example.com on aurora", roster.Grants)
	}
	// The member's own grants survive, and the workspace's own grant joins them.
	written, err := accesslib.Decode(cloud.member.grantsWrite)
	if err != nil {
		t.Fatalf("reading the member's roster back: %v", err)
	}
	want := []string{"user:alice@example.com", "user:guest@other.test", "user:owner@example.com"}
	got := make([]string, 0, len(written))
	for _, g := range written {
		got = append(got, g.User)
	}
	if !slices.Equal(got, want) {
		t.Errorf("the member's roster holds %v, want %v (nothing of the member's own was removed, D2805)", got, want)
	}
	for _, g := range written {
		if g.User == "user:alice@example.com" && g.Role != accesslib.RoleViewer {
			t.Errorf("the member's own grant for alice@example.com is %q, want the viewer grant it held", g.Role)
		}
		if g.User == "user:owner@example.com" && (g.Role != accesslib.RoleAdmin || g.Scope.Kind != accesslib.ScopeProject) {
			t.Errorf("the workspace's grant is %+v, want admin at project scope", g)
		}
	}

	mirror := wu5Mirror(t, cloud)
	gotMirror := make([]string, 0, len(mirror))
	for _, e := range mirror {
		gotMirror = append(gotMirror, e.Address)
	}
	if !slices.Equal(gotMirror, []string{"alice@example.com", "guest@other.test", "ops@example.com", "owner@example.com"}) {
		t.Errorf("the member's mirror names %v, want the workspace's people", gotMirror)
	}
	for _, e := range mirror {
		if e.Address == "guest@other.test" && !e.External {
			t.Error("the mirror does not carry the external mark")
		}
	}

	if len(cloud.audit.ids) != 1 {
		t.Fatalf("the add created %d audit entries, want one", len(cloud.audit.ids))
	}
	event, err := workspace.DecodeAuditEvent(cloud.audit.events[cloud.audit.ids[0]])
	if err != nil {
		t.Fatalf("reading the audit entry back: %v", err)
	}
	if event.Action != workspace.ActionProjectAdd || event.Project != "aurora" || event.Actor != "owner@example.com" {
		t.Errorf("the audit entry is %+v, want project.add on aurora by owner@example.com (D2811)", event)
	}
	// The declaration is member add's own entry, with the values the flags gave.
	entry := wu5MemberOf(t, owner, "aurora")
	if entry.FP != "aurora" || entry.FirebaseProject != "aurora-fb" || entry.ExchangeURL != "https://exchange.example.com/v1" {
		t.Errorf("the table entry is %+v, want the flags' own values", entry)
	}
	if len(entry.JobAccounts) != 1 || entry.JobAccounts[0].Account != "marker-job@aurora-gcp.iam.gserviceaccount.com" || !slices.Equal(entry.JobAccounts[0].Repos, []string{"marker-org/aurora"}) {
		t.Errorf("the table entry's job service accounts are %+v, want the flags' own", entry.JobAccounts)
	}
	if readFileString(t, owner) == before {
		t.Error("the add wrote nothing into the workspace's table")
	}
	// The order is the record, then the members, then the trail.
	if want := []string{"roster", "mirror", "grants", "audit"}; !slices.Equal(cloud.order, want) {
		t.Errorf("the writes landed in the order %v, want %v", cloud.order, want)
	}
	// The region the table declares travels beside the member's two projects.
	wantTarget := workspace.MemberTarget{Project: "aurora", Firebase: "aurora-fb"}
	if len(cloud.targets) == 0 || cloud.targets[0] != wantTarget {
		t.Errorf("the member was opened as %+v, want %+v", cloud.targets, wantTarget)
	}

	// The mirror this add wrote is the workspace's own -- the people it names
	// are the people of the node it wrote them from -- so a second add of the
	// same member reads it and proceeds: R2's reading is "written by another
	// workspace", never "already there" (D2813).
	again := wu5NewCloud()
	again.store.node = cloud.store.node
	again.auth.holds = true
	again.auth.bindings = cloud.auth.bindings
	again.member.grants = cloud.member.grants
	again.member.mirror = cloud.member.mirror
	if out, err := wu5Run(t, again.seams(t), wu5AddArgs("--external")...); err != nil {
		t.Fatalf("a second add of the same member: %v\n%s", err, out)
	}
	if len(again.order) == 0 {
		t.Error("the second add wrote nothing: a re-run is what finishes a half-landed add")
	}
}

// TestProjectAddWaitsForLink is the unlinked half: the entry is declared, the
// member owner is told to link, and nothing of the workspace's is written into
// the member (R11, D2819).
func TestProjectAddWaitsForLink(t *testing.T) {
	_, _, owner := workspaceOwnerHome(t)
	cloud := wu5NewCloud()
	cloud.store.node = wu5NodeDoc(t, wu5RosterDoc(t, workspace.Roster{
		People: []workspace.Person{{Address: "owner@example.com", Kind: workspace.KindHuman}},
		Admins: []string{"owner@example.com"},
	}))

	out, err := wu5Run(t, cloud.seams(t), wu5AddArgs()...)
	if err != nil {
		t.Fatalf("workspace project add: %v\n%s", err, out)
	}
	if !strings.Contains(out, "fugaro workspace project link") {
		t.Errorf("the add does not tell the member's own owner to link:\n%s", out)
	}
	if !strings.Contains(out, "saved, not live yet") {
		t.Errorf("the add does not say the change is saved and not live yet:\n%s", out)
	}
	if len(cloud.order) != 0 {
		t.Errorf("the add wrote %v into a member the workspace does not hold", cloud.order)
	}
	// Nor is the member's own database READ: the authority over it is given at
	// `link`, so a project the workspace does not hold is one this machine has
	// no grant to read, and R2's reading of it is the authority's own refusal,
	// made on the member owner's machine (R14).
	if len(cloud.targets) != 0 {
		t.Error("the add opened the member's own database for a project the workspace does not hold (R14)")
	}
	if cloud.member.mirror.found || cloud.member.grants.found {
		t.Error("the member's own database was written to")
	}
	entry := wu5MemberOf(t, owner, "aurora")
	if entry.FP != "aurora" || len(entry.JobAccounts) != 1 {
		t.Errorf("the table entry is %+v, want the flags' own values", entry)
	}
}

// TestProjectAddRefusesOtherWorkspace is R2 and R14, judged where the reading
// can be made: the member's own mirror is readable once the workspace holds the
// member, and a list carrying somebody the workspace's own roster does not know
// was written by another workspace. The add refuses, names the command that
// removes the member there, and writes nothing anywhere.
//
// The other half of R2 — the parent folder, and a project this workspace does
// not hold at all — is not this command's to refuse: neither document is
// readable before the workspace holds the member (R14), so that refusal is the
// authority's own, made at `link` (C2's Link, WU2 and WU18).
func TestProjectAddRefusesOtherWorkspace(t *testing.T) {
	cloud, owner := wu5Linked(t, []accesslib.Grant{
		wu5ProjectGrant("alice@example.com", accesslib.RoleViewer),
	})
	cloud.member.mirror = wu5NodeDoc(t, []byte(`{"version":1,"members":[{"address":"someone@other.test","kind":"human","external":false}]}`))
	before := readFileString(t, owner)

	out, err := wu5Run(t, cloud.seams(t), wu5AddArgs()...)
	if err == nil {
		t.Fatalf("the add accepted a project whose own database carries another workspace's member list:\n%s", out)
	}
	if !strings.Contains(out, "fugaro workspace project rm") || !strings.Contains(out, "someone@other.test") {
		t.Errorf("the refusal does not name the way out and the entry that shows the workspace is another's:\n%s", out)
	}
	if after := readFileString(t, owner); after != before {
		t.Errorf("the refused add wrote into the workspace's table:\n--- before\n%s--- after\n%s", before, after)
	}
	if len(cloud.order) != 0 {
		t.Errorf("the refused add wrote %v", cloud.order)
	}
	if cloud.member.mirrorWrite != nil || cloud.member.grantsWrite != nil {
		t.Error("the refused add wrote into the member's own database")
	}
}

// TestProjectAddNeedsExternalFlag is D2812 and D2805's own refusal: a grantee
// outside the workspace's allowed domains is admitted only by --external, and
// without it the add names the flag and stops before writing anything.
func TestProjectAddNeedsExternalFlag(t *testing.T) {
	cloud, owner := wu5Linked(t, []accesslib.Grant{
		wu5ProjectGrant("alice@example.com", accesslib.RoleViewer),
		wu5ProjectGrant("guest@other.test", accesslib.RoleViewer),
	})
	before := readFileString(t, owner)

	out, err := wu5Run(t, cloud.seams(t), wu5AddArgs()...)
	if err == nil {
		t.Fatalf("the add adopted a grantee outside the allowed domains without --external:\n%s", out)
	}
	if !strings.Contains(out, "--external") || !strings.Contains(out, "guest@other.test") {
		t.Errorf("the refusal does not name --external and the address it refused:\n%s", out)
	}
	if after := readFileString(t, owner); after != before {
		t.Errorf("the refused add wrote into the workspace's table:\n--- before\n%s--- after\n%s", before, after)
	}
	if len(cloud.order) != 0 {
		t.Errorf("the refused add wrote %v", cloud.order)
	}
	// The same add with the flag adopts the address and marks it (D2812).
	out, err = wu5Run(t, cloud.seams(t), wu5AddArgs("--external")...)
	if err != nil {
		t.Fatalf("workspace project add --external: %v\n%s", err, out)
	}
	guest, ok := wu5Person(wu5Roster(t, cloud), "guest@other.test")
	if !ok || !guest.External {
		t.Errorf("--external did not adopt guest@other.test with the external mark: %+v", guest)
	}
}

// TestProjectAddKeepsMemberFlags holds this command to D2802: `workspace member
// add`'s flags are carried here unchanged, and the only two this command adds of
// its own are --workspace and --external.
//
// The list is written out because there is no longer a command to compare
// against: `workspace member add` was removed when this command took its place
// (I1, D2802), so this test is the copy D2802's claim is kept against, and a
// flag renamed or retyped here is a diff rather than a silent change. The usage
// lines are not spelled out: they are help text, the CLI reference renders them
// for the reader, and a second copy here would drift from it with nothing to
// hold it.
func TestProjectAddKeepsMemberFlags(t *testing.T) {
	mine := newWorkspaceProjectAddCmd(workspaceProjectAddSeams{})
	carried := map[string]string{
		"job-service-account": "stringArray",
		"job-repos":           "stringArray",
		"database-region":     "string",
		"firebase-project":    "string",
		"fp":                  "string",
		"exchange-url":        "string",
		"repo":                "stringArray",
		"no-apply":            "bool",
	}
	for name, kind := range carried {
		f := mine.Flags().Lookup(name)
		if f == nil {
			t.Errorf("workspace project add does not carry --%s, which workspace member add had", name)
			continue
		}
		if f.Value.Type() != kind {
			t.Errorf("--%s is a %s, and workspace member add's was a %s", name, f.Value.Type(), kind)
		}
	}
	var added []string
	mine.Flags().VisitAll(func(f *pflag.Flag) {
		if _, ok := carried[f.Name]; !ok {
			added = append(added, f.Name)
		}
	})
	slices.Sort(added)
	if want := []string{"external", "workspace"}; !slices.Equal(added, want) {
		t.Errorf("workspace project add's own flags are %v, want %v", added, want)
	}
	if mine.Use != "add <project> --workspace <name>" {
		t.Errorf("workspace project add's own usage line is %q", mine.Use)
	}
}

// TestProjectAddOnlyOnTheTablesMachine is D2821's fence: the member table lives
// in the config of the project the workspace is named after, so a machine that
// holds no config for it writes nothing and reaches no seam at all.
func TestProjectAddOnlyOnTheTablesMachine(t *testing.T) {
	workspaceWriteHome(t)
	cloud := wu5NewCloud()

	out, err := wu5Run(t, cloud.seams(t), wu5AddArgs()...)
	if err == nil {
		t.Fatalf("the add ran on a machine that holds no config for workspace-alpha's own project:\n%s", out)
	}
	if !strings.Contains(out, "holds no config for workspace workspace-alpha's own project") {
		t.Errorf("the refusal does not say whose file this is:\n%s", out)
	}
	if cloud.opened != 0 {
		t.Errorf("the refusal opened %d seams, want none", cloud.opened)
	}
}

// TestProjectAddRefusesUnderACodingAgentMarker is A7 and threat model §5: every
// write command of the family refuses in a coding agent's session, and the
// refusal covers this command's whole surface, not only the table entry.
//
// The entry alone is not enough: `applySettingsEdit` answers "no change" for an
// add the table already carries, BEFORE the fence it holds, and the add goes on
// to write the workspace's roster, the member's own roster and mirror and the
// workspace's trail — so a replay inside an agent's session reaches four cloud
// writes with a fence that never saw it. The refusal is therefore the first
// thing the command does, and no seam is opened for it.
func TestProjectAddRefusesUnderACodingAgentMarker(t *testing.T) {
	cloud, owner := wu5Linked(t, []accesslib.Grant{
		wu5ProjectGrant("alice@example.com", accesslib.RoleViewer),
	})
	before := readFileString(t, owner)
	t.Setenv(agentMarkers[0], "1")

	out, err := wu5Run(t, cloud.seams(t), wu5AddArgs()...)
	if err == nil {
		t.Fatalf("the add ran in a coding agent's session:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d (a user error: nothing was attempted)", ExitCode(err), ExitUserError)
	}
	if !strings.Contains(err.Error(), agentMarkers[0]) {
		t.Errorf("the refusal does not name the marker: %v", err)
	}
	if cloud.opened != 0 {
		t.Errorf("the refusal opened %d seams, want none", cloud.opened)
	}
	if len(cloud.order) != 0 {
		t.Errorf("the refusal wrote %v", cloud.order)
	}
	if after := readFileString(t, owner); after != before {
		t.Errorf("the refusal wrote into the workspace's table:\n--- before\n%s--- after\n%s", before, after)
	}
}

// TestProjectAddPropagatesOnlyBoundAdmins is D2823: only an entry of the
// workspace's Admins that also holds a live binding is trusted, so only those
// admins' stored grants are written into the member and marked in the node.
func TestProjectAddPropagatesOnlyBoundAdmins(t *testing.T) {
	_, _, owner := workspaceOwnerHome(t)
	cloud := wu5NewCloud()
	cloud.store.node = wu5NodeDoc(t, wu5RosterDoc(t, workspace.Roster{
		People: []workspace.Person{
			{Address: "first@example.com", Kind: workspace.KindHuman},
			{Address: "second@example.com", Kind: workspace.KindHuman},
			{Address: "third@example.com", Kind: workspace.KindHuman},
		},
		Admins:  []string{"first@example.com", "second@example.com", "third@example.com"},
		Domains: []string{"example.com"},
	}))
	cloud.auth.holds = true
	// The node's Admins names three, and only two of them hold a binding -- each
	// answered as the contract spells one, so this test compares the node's bare
	// addresses against the `user:` members the mechanism answers with.
	cloud.auth.bindings = []string{"user:first@example.com", "user:third@example.com"}
	cloud.member.grants = wu5GrantsDoc(t, []accesslib.Grant{
		wu5ProjectGrant("alice@example.com", accesslib.RoleViewer),
	})

	out, err := wu5Run(t, cloud.seams(t), wu5AddArgs()...)
	if err != nil {
		t.Fatalf("workspace project add: %v\n%s", err, out)
	}

	roster := wu5Roster(t, cloud)
	marked := make([]string, 0, len(roster.Grants))
	for _, g := range roster.Grants {
		marked = append(marked, g.User)
		if g.Role != workspace.RoleAdmin || g.Project != "aurora" {
			t.Errorf("the mark %+v is not an admin grant of aurora", g)
		}
	}
	if want := []string{"first@example.com", "third@example.com"}; !slices.Equal(marked, want) {
		t.Errorf("the workspace marked %v, want %v (D2823)", marked, want)
	}

	written, err := accesslib.Decode(cloud.member.grantsWrite)
	if err != nil {
		t.Fatalf("reading the member's roster back: %v", err)
	}
	admins := []string{}
	for _, g := range written {
		if g.Role == accesslib.RoleAdmin {
			admins = append(admins, g.User)
		}
	}
	if want := []string{"user:first@example.com", "user:third@example.com"}; !slices.Equal(admins, want) {
		t.Errorf("the member's roster holds admin grants for %v, want %v", admins, want)
	}
	if strings.Contains(string(cloud.member.grantsWrite), "second@example.com") {
		t.Errorf("a workspace admin with no live binding was written into the member:\n%s", cloud.member.grantsWrite)
	}
	if entry := wu5MemberOf(t, owner, "aurora"); entry.FP != "aurora" {
		t.Errorf("the table entry is %+v, want the flags' own values", entry)
	}
}

// TestProjectAddComparesAdminsToTheLiveBindings is D2823's comparison itself. The
// node's `Admins` holds canonical addresses (C1) and a live binding is the
// `user:` member the mechanism answers with (C2: WU2's folder owners and WU20's
// token creators both), so an admin that holds a binding is one the two
// spellings agree on once the prefix is off -- and an admin whose binding this
// command cannot read as a person is one nothing confirms, which is D2823's
// answer to an entry it cannot check.
func TestProjectAddComparesAdminsToTheLiveBindings(t *testing.T) {
	admins := []string{"bound@example.com", "unbound@example.com"}
	if got, want := workspaceProjectAddBoundAdmins(admins, []string{"user:bound@example.com"}), []string{"bound@example.com"}; !slices.Equal(got, want) {
		t.Errorf("a node naming %v against the live binding %q bounds %v, want %v: the node's Admins is bare and a binding is a `user:` member, so comparing the two as they arrive bounds nobody (D2823)", admins, "user:bound@example.com", got, want)
	}
	if got := workspaceProjectAddBoundAdmins(admins, []string{"user:somebody-else@example.com"}); len(got) != 0 {
		t.Errorf("an admin holding no binding is bounded by %v, want nobody", got)
	}
	if got := workspaceProjectAddBoundAdmins(admins, []string{"bound@example.com"}); len(got) != 0 {
		t.Errorf("a binding that names no user bounded %v, want nobody: this command trusts an entry only against the binding the contract answers with (D2823)", got)
	}
	if got, want := workspaceProjectAddBoundAdmins([]string{"bound@example.com", "unbound@example.com"}, []string{"user:unbound@example.com"}), []string{"unbound@example.com"}; !slices.Equal(got, want) {
		t.Errorf("the bound admins are %v, want %v: with one of the two bound, the node's own order is what the answer keeps", got, want)
	}
}

// TestProjectAddCorrectsAMarkThatDisagreesWithTheGrant is D2818's mark held to
// what this add writes: the member's own project-scope grant for a workspace
// admin is an admin one -- replaced where the member already holds a grant for
// them at that scope, added where it holds none -- so a mark in the workspace's
// node that says another role is the workspace's record of a grant it is not
// writing. Left standing, the record and the grant disagree and the drift check
// reads the workspace's own grant as a member's. The add corrects the mark, and
// the two agree.
func TestProjectAddCorrectsAMarkThatDisagreesWithTheGrant(t *testing.T) {
	workspaceOwnerHome(t)
	cloud := wu5NewCloud()
	cloud.store.node = wu5NodeDoc(t, wu5RosterDoc(t, workspace.Roster{
		People: []workspace.Person{
			{Address: "alice@example.com", Kind: workspace.KindHuman},
			{Address: "owner@example.com", Kind: workspace.KindHuman},
		},
		Admins:  []string{"owner@example.com"},
		Domains: []string{"example.com"},
		// The mark an earlier writer left: the same project and user this add is
		// about to write a grant for, under a role this add does not write.
		Grants: []workspace.StoredGrant{{Project: "aurora", User: "owner@example.com", Role: workspace.RoleViewer}},
	}))
	cloud.auth.holds = true
	cloud.auth.bindings = []string{"user:owner@example.com"}
	// The member already holds a grant for the workspace's admin, at project
	// scope and under another role: the pair is replaced, not added beside.
	cloud.member.grants = wu5GrantsDoc(t, []accesslib.Grant{
		wu5ProjectGrant("alice@example.com", accesslib.RoleViewer),
		wu5ProjectGrant("owner@example.com", accesslib.RoleViewer),
	})

	out, err := wu5Run(t, cloud.seams(t), wu5AddArgs()...)
	if err != nil {
		t.Fatalf("workspace project add: %v\n%s", err, out)
	}

	roster := wu5Roster(t, cloud)
	want := []workspace.StoredGrant{{Project: "aurora", User: "owner@example.com", Role: workspace.RoleAdmin}}
	if !slices.Equal(roster.Grants, want) {
		t.Errorf("the workspace's marks are %+v, want %+v: the grant this add writes into the member for an admin is an admin one (D2818), so the mark of it says the same", roster.Grants, want)
	}
	written, err := accesslib.Decode(cloud.member.grantsWrite)
	if err != nil {
		t.Fatalf("reading the member's roster back: %v", err)
	}
	for _, g := range written {
		switch g.User {
		case "user:owner@example.com":
			if g.Role != accesslib.RoleAdmin || g.Scope.Kind != accesslib.ScopeProject {
				t.Errorf("the member's grant for the workspace's admin is %+v, want admin at project scope (D2818, D2823)", g)
			}
		case "user:alice@example.com":
			if g.Role != accesslib.RoleViewer {
				t.Errorf("the member's own grant for alice@example.com is %q, want the viewer grant it held (D2805)", g.Role)
			}
		}
	}
	if len(written) != 2 {
		t.Errorf("the member's roster holds %d grants, want its own two: nothing was added for an admin it already had a grant for", len(written))
	}
}

// TestProjectAddRefreshesAMirrorThatDoesNotNameTheWorkspacesPeople is D2813's
// mirror held to the sentence this add prints about it: the mirror is written on
// a membership write, and a member's list that names fewer of the workspace's
// people than the roster holds -- written while the roster was smaller, or
// rewritten by hand, which D2813 makes advisory -- is one this add writes rather
// than one it calls already right. The second run, over the documents the first
// left, is the other half: a list that really does name them is left alone, and
// then the sentence says so.
func TestProjectAddRefreshesAMirrorThatDoesNotNameTheWorkspacesPeople(t *testing.T) {
	workspaceOwnerHome(t)
	cloud := wu5NewCloud()
	cloud.store.node = wu5NodeDoc(t, wu5RosterDoc(t, workspace.Roster{
		People: []workspace.Person{
			{Address: "alice@example.com", Kind: workspace.KindHuman},
			{Address: "ops@example.com", Kind: workspace.KindHuman},
			{Address: "owner@example.com", Kind: workspace.KindHuman},
		},
		Admins:  []string{"owner@example.com"},
		Domains: []string{"example.com"},
		// The mark is already the one this add would write, so the node does not
		// move and the mirror's own state is the whole of what it decides on.
		Grants: []workspace.StoredGrant{{Project: "aurora", User: "owner@example.com", Role: workspace.RoleAdmin}},
	}))
	cloud.auth.holds = true
	cloud.auth.bindings = []string{"user:owner@example.com"}
	cloud.member.grants = wu5GrantsDoc(t, []accesslib.Grant{wu5ProjectGrant("alice@example.com", accesslib.RoleViewer)})
	cloud.member.mirror = wu5NodeDoc(t, []byte(`{"version":1,"members":[{"address":"ops@example.com","kind":"human","external":false}]}`))

	out, err := wu5Run(t, cloud.seams(t), wu5AddArgs()...)
	if err != nil {
		t.Fatalf("workspace project add: %v\n%s", err, out)
	}
	if cloud.member.mirrorWrite == nil {
		t.Fatalf("the add left the member's own list naming one person of the workspace's three, and the roster moved for no part of it:\n%s", out)
	}
	mirror := wu5Mirror(t, cloud)
	named := make([]string, 0, len(mirror))
	for _, e := range mirror {
		named = append(named, e.Address)
	}
	if want := []string{"alice@example.com", "ops@example.com", "owner@example.com"}; !slices.Equal(named, want) {
		t.Errorf("the member's list names %v, want the workspace's people %v (D2813)", named, want)
	}
	if !strings.Contains(out, "now names 3 people") {
		t.Errorf("the add does not say the member's list now names the workspace's people:\n%s", out)
	}
	// The node itself did not move: the mark it carries is the one this add
	// would write, so the mirror is the only thing there was to repair.
	if want := []string{"mirror", "grants", "audit"}; !slices.Equal(cloud.order, want) {
		t.Errorf("the writes landed in the order %v, want %v: the workspace's own node is already the one this add would write", cloud.order, want)
	}

	// A second add over the documents the first left: the member's list names
	// the workspace's people, so it is left alone -- and the sentence says that,
	// rather than claiming a list was written.
	again := wu5NewCloud()
	again.store.node = cloud.store.node
	again.auth.holds = true
	again.auth.bindings = cloud.auth.bindings
	again.member.grants = cloud.member.grants
	again.member.mirror = cloud.member.mirror
	out, err = wu5Run(t, again.seams(t), wu5AddArgs()...)
	if err != nil {
		t.Fatalf("a second add of the same member: %v\n%s", err, out)
	}
	if again.member.mirrorWrite != nil {
		t.Error("the second add rewrote a member's list that already named the workspace's people")
	}
	if !strings.Contains(out, "already named 3 people") {
		t.Errorf("the add does not say the member's list already named the workspace's people:\n%s", out)
	}
}
