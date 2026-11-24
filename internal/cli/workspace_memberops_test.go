package cli

// WU4's tests (docs/workspace-projects-users/implementation-plan.md).
//
// The rig is one member's world and nothing else: its GCP and Firebase projects
// in a CRM fake, its own state bucket (holding the two roots' state documents),
// its own runs bucket (where the published admin list lives) and its own
// database in an RTDB fake. The ops value under test is built as a plain struct
// literal with WU4's two seams — `cloud`, which hands back the API clients, and
// `loopback`, which is what a fake database address needs — so the member's
// database URL really is derived from the state the member's own `fugaro update`
// writes, and no test stubs it. Every fake and helper here carries the task id
// (plan §1), so the level-2 tasks landing in this package later merge without a
// name clash.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/workspace"
)

const (
	wu4Project  = "member-one"
	wu4Firebase = "member-one-fb"
	wu4Runs     = "member-one-runs"
	// The three people the fixtures use, and the machine nobody's outputs name.
	wu4Person   = "alice@example.com"
	wu4Other    = "bob@example.com"
	wu4Third    = "carol@example.com"
	wu4Outsider = "dave@outside.test"
	// The workspace's own IAM account (WU3's output), as the ops value is told
	// it, and the member's scheduler account, which is one of the member's OWN
	// Terraform accounts and is named by the member's own outputs (F17).
	wu4WorkspaceIAM = "workspace-iam@ws-project.iam.gserviceaccount.com"
	wu4MemberSA     = "fugaro-scheduler@member-one.iam.gserviceaccount.com"
	// A service account that is nobody's: not one of the member's accounts, not
	// the workspace's, and in no outputs — somebody else's binding.
	wu4ForeignSA = "ci@another-project.iam.gserviceaccount.com"
)

// wu4Rig is the member's world and the ops value under test.
type wu4Rig struct {
	t      *testing.T
	gcs    *gcpfake.GCS
	crm    *gcpfake.CRM
	db     *gcpfake.RTDB
	ops    *WorkspaceMemberOps
	m      workspace.MemberTarget
	client *rtdb.Client
}

// wu4New builds the world: both projects exist, both buckets exist, the Firebase
// root's state names the fake database, and the ops value is the real one with
// its two seams filled.
func wu4New(t *testing.T) *wu4Rig {
	t.Helper()
	g := gcpfake.NewGCS(t)
	g.AddProject(wu4Project, 42)
	g.AddBucket(managedmode.InstallationsStateBucket(wu4Project), 42, nil)
	g.AddBucket(wu4Runs, 42, nil)
	c := gcpfake.NewCRM(t)
	c.AddProject(wu4Project, 42)
	c.AddProject(wu4Firebase, 43)
	r := &wu4Rig{t: t, gcs: g, crm: c, db: gcpfake.NewRTDB(t),
		m: workspace.MemberTarget{Project: wu4Project, Firebase: wu4Firebase}}
	r.client = r.newClient()
	r.ops = &WorkspaceMemberOps{
		workspaceIAM: wu4WorkspaceIAM,
		loopback:     true,
		cloud: func(context.Context) (*infra.Clients, error) {
			return &infra.Clients{Storage: managedStorage(t, g), CRM: managedCRM(t, c), NoAuth: true}, nil
		},
	}
	r.seedFirebase(wu4FirebaseOutputs(wu4Firebase, r.db.URL))
	return r
}

// newClient is the test's own client of the member's database: the same
// derivation a command makes, used to seed and to assert what landed.
func (r *wu4Rig) newClient() *rtdb.Client {
	r.t.Helper()
	c, err := newBudgetClient(r.t.Context(), r.db.URL, true)
	if err != nil {
		r.t.Fatalf("building the member database client: %v", err)
	}
	return c
}

// seedFirebase writes the Firebase root's state into the member's state bucket.
func (r *wu4Rig) seedFirebase(outputs map[string]any) {
	r.putState(infra.StatePrefixFirebase, outputs)
}

// seedInstallation writes the installation root's state into the member's state
// bucket: where `launchers` and `operators` are read from.
func (r *wu4Rig) seedInstallation(outputs map[string]any) {
	r.putState(infra.StatePrefixInstallation, outputs)
}

func (r *wu4Rig) putState(prefix string, outputs map[string]any) {
	r.t.Helper()
	raw, err := json.Marshal(map[string]any{
		"version": 4, "terraform_version": "1.9.8", "serial": 1, "lineage": "wu4",
		"outputs": wu4StateOutputs(outputs), "resources": []any{},
	})
	if err != nil {
		r.t.Fatal(err)
	}
	r.gcs.Put(managedmode.InstallationsStateBucket(wu4Project), prefix+"/default.tfstate", raw)
}

// wu4StateOutputs wraps plain values as a state document holds outputs: one
// object per name, the value under "value".
func wu4StateOutputs(outputs map[string]any) map[string]any {
	out := make(map[string]any, len(outputs))
	for name, v := range outputs {
		out[name] = map[string]any{"value": v, "type": "string"}
	}
	return out
}

// wu4FirebaseOutputs is the Firebase root's output triple, as the member's own
// `fugaro update` leaves it: the database's URL, the signer the member's own
// check ties to the same project, and that project's name.
func wu4FirebaseOutputs(project, url string) map[string]any {
	return map[string]any{
		"rtdb_url":         url,
		"firebase_api_key": "wu4-not-a-real-key",
		"token_signer":     infra.SignerAccountID + "@" + project + ".iam.gserviceaccount.com",
		"firebase_project": project,
	}
}

// wu4Grants encodes grants with internal/access's own writer, which is the only
// spelling a member roster holds.
func wu4Grants(t *testing.T, grants ...accesslib.Grant) []byte {
	t.Helper()
	raw, err := accesslib.Encode(grants)
	if err != nil {
		t.Fatalf("encoding the fixture's grants: %v", err)
	}
	return raw
}

// wu4Mirror is the mirror document WU4 writes, through its own encoder.
func wu4Mirror(t *testing.T, people ...workspace.MirrorEntry) []byte {
	t.Helper()
	raw, err := EncodeMemberMirror(people)
	if err != nil {
		t.Fatalf("encoding the fixture's mirror: %v", err)
	}
	return raw
}

func wu4Grant(user string, role accesslib.Role, scope accesslib.Scope) accesslib.Grant {
	return accesslib.Grant{User: "user:" + user, Role: role, Scope: scope}
}

func wu4ProjectScope() accesslib.Scope { return accesslib.Scope{Kind: accesslib.ScopeProject} }

// TestMemberGrantsReadTheMembersOwnDatabase: the member's database address is
// derived from the member's own state and from nowhere else, and the derivation
// is tied to the member we asked about (F2, R10, and the reason
// managed_inventory.go derives the state bucket from the project rather than
// taking one from a flag).
func TestMemberGrantsReadTheMembersOwnDatabase(t *testing.T) {
	ctx := t.Context()
	r := wu4New(t)
	if err := accesslib.MarkMigrated(ctx, r.client); err != nil {
		t.Fatal(err)
	}

	doc, err := r.ops.ReadGrants(ctx, r.m)
	if err != nil {
		t.Fatalf("ReadGrants: %v", err)
	}
	if doc.Found || doc.ETag == "" {
		t.Errorf("an absent node reads %+v, want Found false beside the null ETag a create is made under", doc)
	}

	// A state bucket is readable by whoever holds the bucket, and a triple that
	// is well-formed and belongs to another project must not be taken as this
	// member's database: infra.CheckFirebaseOutputsFor is the check, and it is
	// what makes this a read of THE MEMBER's database.
	r.seedFirebase(wu4FirebaseOutputs("someone-else-fb", r.db.URL))
	if _, err := r.ops.ReadGrants(ctx, r.m); err == nil {
		t.Error("a Firebase state naming another project is read as this member's database")
	}

	// The Firebase project is part of the member (F2, R10): a caller naming only
	// the GCP project of a member whose Firebase project is a project of its own
	// is refused rather than read against the wrong database.
	r.seedFirebase(wu4FirebaseOutputs(wu4Firebase, r.db.URL))
	if _, err := r.ops.ReadGrants(ctx, workspace.MemberTarget{Project: wu4Project}); err == nil {
		t.Error("a member named by its GCP project alone is read while its Firebase project is a project of its own")
	}
}

// TestMemberGrantsWriteRefusesStaleETag: both documents are written under the
// ETag they were read at, and a node another writer moved under them is a
// refusal with nothing written (D2817) — the property two admins editing one
// member rest on.
func TestMemberGrantsWriteRefusesStaleETag(t *testing.T) {
	ctx := t.Context()
	r := wu4New(t)
	if err := accesslib.MarkMigrated(ctx, r.client); err != nil {
		t.Fatal(err)
	}

	stale, err := r.ops.ReadGrants(ctx, r.m)
	if err != nil {
		t.Fatalf("ReadGrants: %v", err)
	}
	if err := r.ops.WriteGrants(ctx, r.m, stale, wu4Grants(t, wu4Grant(wu4Person, accesslib.RoleViewer, wu4ProjectScope()))); err != nil {
		t.Fatalf("the first write, under the ETag it was read at: %v", err)
	}

	moved, err := r.ops.ReadGrants(ctx, r.m)
	if err != nil {
		t.Fatal(err)
	}
	if !moved.Found || moved.ETag == stale.ETag {
		t.Fatalf("the write left the node as %+v, so it is not a node another writer moved", moved)
	}
	if err := r.ops.WriteGrants(ctx, r.m, moved, wu4Grants(t, wu4Grant(wu4Other, accesslib.RoleAdmin, wu4ProjectScope()))); err != nil {
		t.Fatalf("the other writer's write: %v", err)
	}

	err = r.ops.WriteGrants(ctx, r.m, stale, wu4Grants(t, wu4Grant(wu4Person, accesslib.RoleViewer, wu4ProjectScope())))
	if err == nil {
		t.Fatal("WriteGrants overwrote a node another writer moved under it, which is how two admins lose each other's work")
	}
	if !errors.Is(err, rtdb.ErrPrecondition) {
		t.Errorf("the refusal is %v, want rtdb.ErrPrecondition: a caller judges a lost race by it and reads the node again", err)
	}
	node, err := accesslib.Read(ctx, r.client)
	if err != nil {
		t.Fatal(err)
	}
	if len(node.Grants) != 1 || node.Grants[0].User != "user:"+wu4Other {
		t.Errorf("the refused write left the node as %+v, want the other writer's single grant", node.Grants)
	}

	// The mirror is written under its own node's ETag, with the same answer.
	staleMirror, err := r.ops.ReadMirror(ctx, r.m)
	if err != nil {
		t.Fatalf("ReadMirror: %v", err)
	}
	if err := r.ops.WriteMirror(ctx, r.m, staleMirror, wu4Mirror(t, workspace.MirrorEntry{Address: wu4Person, Kind: workspace.KindHuman})); err != nil {
		t.Fatalf("the first mirror write: %v", err)
	}
	movedMirror, err := r.ops.ReadMirror(ctx, r.m)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.ops.WriteMirror(ctx, r.m, movedMirror, wu4Mirror(t, workspace.MirrorEntry{Address: wu4Other, Kind: workspace.KindHuman})); err != nil {
		t.Fatalf("the other writer's mirror write: %v", err)
	}
	err = r.ops.WriteMirror(ctx, r.m, staleMirror, wu4Mirror(t, workspace.MirrorEntry{Address: wu4Person, Kind: workspace.KindHuman}))
	if err == nil {
		t.Fatal("WriteMirror overwrote a node another writer moved under it")
	}
	if !errors.Is(err, rtdb.ErrPrecondition) {
		t.Errorf("the mirror's refusal is %v, want rtdb.ErrPrecondition", err)
	}
	back, err := r.ops.ReadMirror(ctx, r.m)
	if err != nil {
		t.Fatal(err)
	}
	people, err := DecodeMemberMirror(back.Raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(people) != 1 || people[0].Address != wu4Other {
		t.Errorf("the refused mirror write left %+v, want the other writer's single entry", people)
	}
}

// TestMemberGrantsDecodeWithTodaysAccess: the bytes written are internal/access's
// own format, read back by its own reader, and a document that reader would
// refuse is refused here before anything is written (D2806, D2818 — every member
// write goes through WU4, so the roster's format has no other door).
func TestMemberGrantsDecodeWithTodaysAccess(t *testing.T) {
	ctx := t.Context()
	r := wu4New(t)
	if err := accesslib.MarkMigrated(ctx, r.client); err != nil {
		t.Fatal(err)
	}

	raw := wu4Grants(t,
		wu4Grant(wu4Person, accesslib.RoleAdmin, wu4ProjectScope()),
		wu4Grant(wu4Other, accesslib.RoleOperator, accesslib.Scope{Kind: accesslib.ScopeAllRepos}),
		wu4Grant(wu4Third, accesslib.RoleViewer, accesslib.Scope{Kind: accesslib.ScopeRepos, Repos: []string{"github/acme/app"}}),
	)
	doc, err := r.ops.ReadGrants(ctx, r.m)
	if err != nil {
		t.Fatalf("ReadGrants: %v", err)
	}
	if err := r.ops.WriteGrants(ctx, r.m, doc, raw); err != nil {
		t.Fatalf("WriteGrants: %v", err)
	}

	back, err := r.ops.ReadGrants(ctx, r.m)
	if err != nil {
		t.Fatalf("reading the roster back: %v", err)
	}
	if !back.Found {
		t.Fatal("the roster the write landed reads as absent")
	}
	got, err := accesslib.Decode(back.Raw)
	if err != nil {
		t.Fatalf("the member's own reader refuses the bytes WU4 wrote: %v", err)
	}
	want, err := accesslib.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the roster reads back as %+v, want %+v", got, want)
	}

	for _, tc := range []struct{ name, raw string }{
		{"a member spelled as a bare address", `{"grants":[{"user":"alice@example.com","role":"viewer","scope":"project"}]}`},
		{"a role the member's CLI does not know", `{"grants":[{"user":"user:alice@example.com","role":"superuser","scope":"project"}]}`},
		{"a field the node does not carry", `{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"project","note":"x"}]}`},
		{"no grants list at all", `{}`},
	} {
		if err := r.ops.WriteGrants(ctx, r.m, back, []byte(tc.raw)); err == nil {
			t.Errorf("WriteGrants wrote %s, and the member's own reader refuses the document", tc.name)
		}
	}
	after, err := r.ops.ReadGrants(ctx, r.m)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after.Raw, back.Raw) {
		t.Errorf("a refused write changed the node: it now holds %s, and it held %s", after.Raw, back.Raw)
	}

	// The mirror travels the same way: its own encoder's bytes, read back by its
	// own reader, entries and all.
	stale, err := r.ops.ReadMirror(ctx, r.m)
	if err != nil {
		t.Fatal(err)
	}
	entries := []workspace.MirrorEntry{
		{Address: wu4Person, Kind: workspace.KindHuman},
		{Address: wu4Outsider, Kind: workspace.KindHuman, External: true},
		{Address: wu4MemberSA, Kind: workspace.KindSA},
	}
	if err := r.ops.WriteMirror(ctx, r.m, stale, wu4Mirror(t, entries...)); err != nil {
		t.Fatalf("WriteMirror: %v", err)
	}
	mirror, err := r.ops.ReadMirror(ctx, r.m)
	if err != nil {
		t.Fatal(err)
	}
	gotEntries, err := DecodeMemberMirror(mirror.Raw)
	if err != nil {
		t.Fatalf("the mirror's own reader refuses the bytes WU4 wrote: %v", err)
	}
	if !reflect.DeepEqual(gotEntries, entries) {
		t.Errorf("the mirror reads back as %+v, want %+v", gotEntries, entries)
	}
	for _, tc := range []struct{ name, raw string }{
		{"not an object", `[]`},
		{"a key nobody writes", `{"people":[],"admins":[]}`},
		{"an entry missing its kind", `{"people":[{"address":"alice@example.com"}]}`},
		{"an entry the person grammar refuses", `{"people":[{"address":"alice","kind":"human","external":false}]}`},
	} {
		if err := r.ops.WriteMirror(ctx, r.m, mirror, []byte(tc.raw)); err == nil {
			t.Errorf("WriteMirror wrote %s", tc.name)
		}
	}
}

// TestMemberGrantsRefuseUnmigratedLists: a write into a member whose own
// `fugaro update` has not migrated its lists is refused, as `fugaro user grant`
// refuses it, because the member's next update derives its IAM lists FROM that
// node and a node written now would hold this change and nobody else.
func TestMemberGrantsRefuseUnmigratedLists(t *testing.T) {
	ctx := t.Context()
	r := wu4New(t)

	doc, err := r.ops.ReadGrants(ctx, r.m)
	if err != nil {
		t.Fatalf("ReadGrants: %v", err)
	}
	if doc.Found {
		t.Fatal("the fixture starts with a roster node")
	}
	err = r.ops.WriteGrants(ctx, r.m, doc, wu4Grants(t, wu4Grant(wu4Person, accesslib.RoleViewer, wu4ProjectScope())))
	if err == nil {
		t.Fatal("WriteGrants wrote a roster into an installation whose lists were never migrated: the next update would derive the member's IAM from a node holding one grant")
	}
	if r.db.Value(accesslib.PathNode) != nil {
		t.Error("the refused write created the node")
	}

	// The migration marker is the whole of what tells an unmigrated installation
	// from one whose roster is nobody, and the member's own update leaves it on
	// both paths. With it, the same write lands.
	if err := accesslib.MarkMigrated(ctx, r.client); err != nil {
		t.Fatal(err)
	}
	if err := r.ops.WriteGrants(ctx, r.m, doc, wu4Grants(t, wu4Grant(wu4Person, accesslib.RoleViewer, wu4ProjectScope()))); err != nil {
		t.Fatalf("WriteGrants on a migrated installation: %v", err)
	}
	node, err := accesslib.Read(ctx, r.client)
	if err != nil {
		t.Fatal(err)
	}
	if node.ETag == doc.ETag || len(node.Grants) != 1 {
		t.Errorf("the write landed as %+v, want one grant at the node", node)
	}
}

// TestPendingApplyNamesUnappliedOperators: what the record says the member's IAM
// should carry is compared with what the member's own outputs say it applied, and
// the entries the member does not carry are named (D2818, D1940).
func TestPendingApplyNamesUnappliedOperators(t *testing.T) {
	ctx := t.Context()
	r := wu4New(t)
	r.seedInstallation(map[string]any{
		"runs_bucket": wu4Runs,
		"launchers":   []string{"user:" + wu4Person},
		// The outputs are IAM members, and the domain's case is folded by
		// internal/access's own member grammar: one person is not pending
		// because a state spells their domain in another case.
		"operators": []string{"user:alice@EXAMPLE.com"},
	})

	pending, err := r.ops.PendingApply(ctx, r.m, workspace.ApplyWant{
		Launchers: []string{"user:" + wu4Person},
		Operators: []string{"user:alice@EXAMPLE.com", "user:" + wu4Other},
	})
	if err != nil {
		t.Fatalf("PendingApply: %v", err)
	}
	if len(pending.Launchers) != 0 {
		t.Errorf("Launchers is %v, want nothing: the member's outputs carry every launcher the record states", pending.Launchers)
	}
	if !slices.Equal(pending.Operators, []string{"user:" + wu4Other}) {
		t.Errorf("Operators is %v, want only the operator the member's outputs do not carry (in the caller's own spelling)", pending.Operators)
	}

	// A member nothing has been applied to has applied nothing: the state bucket
	// holds no installation at all, and every entry the record states is pending.
	// It is not an error and not a clean report.
	fresh := wu4New(t)
	pending, err = fresh.ops.PendingApply(ctx, fresh.m, workspace.ApplyWant{
		Launchers: []string{"user:" + wu4Person},
		Operators: []string{"user:" + wu4Other},
	})
	if err != nil {
		t.Fatalf("PendingApply on a member with no state: %v", err)
	}
	if !slices.Equal(pending.Launchers, []string{"user:" + wu4Person}) || !slices.Equal(pending.Operators, []string{"user:" + wu4Other}) {
		t.Errorf("an unapplied member reads as %+v, want every state the record makes to be pending", pending)
	}
}

// TestPendingApplyNamesAnUnpublishedAdmin: the admin list is compared with the
// object the member's own update publishes (access.PathAdminList, D1940), and an
// object that is not there is "nothing published yet" — every admin the record
// states is pending — while a read that failed is an error rather than a clean
// member.
func TestPendingApplyNamesAnUnpublishedAdmin(t *testing.T) {
	ctx := t.Context()
	r := wu4New(t)
	r.seedInstallation(map[string]any{"runs_bucket": wu4Runs, "launchers": []string{}, "operators": []string{}})

	want := workspace.ApplyWant{Admins: []string{wu4Person, wu4Other}}
	pending, err := r.ops.PendingApply(ctx, r.m, want)
	if err != nil {
		t.Fatalf("PendingApply: %v", err)
	}
	if !slices.Equal(pending.Admins, want.Admins) {
		t.Errorf("Admins is %v, want %v: nothing has published a list, so no admin has reached the member", pending.Admins, want.Admins)
	}

	published, err := accesslib.EncodeAdminList([]accesslib.Admin{{Email: wu4Person}})
	if err != nil {
		t.Fatal(err)
	}
	r.gcs.Put(wu4Runs, accesslib.PathAdminList, published)
	pending, err = r.ops.PendingApply(ctx, r.m, want)
	if err != nil {
		t.Fatalf("PendingApply: %v", err)
	}
	if !slices.Equal(pending.Admins, []string{wu4Other}) {
		t.Errorf("Admins is %v, want only the admin the published list does not hold", pending.Admins)
	}

	// An installation whose outputs name no runs bucket has published nothing:
	// the state is what says where the object would be.
	r.seedInstallation(map[string]any{"launchers": []string{}, "operators": []string{}})
	pending, err = r.ops.PendingApply(ctx, r.m, want)
	if err != nil {
		t.Fatalf("PendingApply: %v", err)
	}
	if !slices.Equal(pending.Admins, want.Admins) {
		t.Errorf("Admins is %v, want %v for an installation that names no runs bucket", pending.Admins, want.Admins)
	}

	// A read that is refused is not an absent object. GCS answers 403 for a
	// missing object to a caller that holds the get but not the list
	// (AnswerMissingAs403, docs/bucket-iam/spec.md), and a member reported
	// clean because a read was denied is what the fail-closed rule forbids. The
	// object really is absent here — a rig of its own, whose runs bucket never
	// held a list — because a 403 beside an object that IS there would be the
	// other case, a read that succeeds.
	denied := wu4New(t)
	denied.seedInstallation(map[string]any{"runs_bucket": wu4Runs, "launchers": []string{}, "operators": []string{}})
	denied.gcs.AnswerMissingAs403(wu4Runs, "admin/")
	if _, err := denied.ops.PendingApply(ctx, denied.m, workspace.ApplyWant{Admins: []string{"someone-nobody-published@example.com"}}); err == nil {
		t.Error("a denied read of the admin list is read as an unpublished one, and the member is reported clean")
	}
}

// TestPendingApplyViewersNotMeasured: no output carries a viewer or a
// repository-scope grant, so what waits for those cannot be told from what has
// applied, and the report says so on every call (D2815) — a reader must not read
// empty lists as "in sync" while this is not empty.
func TestPendingApplyViewersNotMeasured(t *testing.T) {
	ctx := t.Context()
	r := wu4New(t)
	r.seedInstallation(map[string]any{"runs_bucket": wu4Runs, "launchers": []string{}, "operators": []string{}})

	pending, err := r.ops.PendingApply(ctx, r.m, workspace.ApplyWant{})
	if err != nil {
		t.Fatalf("PendingApply: %v", err)
	}
	want := []string{workspace.NotMeasuredViewers, workspace.NotMeasuredScopes}
	if !slices.Equal(pending.NotMeasured, want) {
		t.Errorf("NotMeasured is %v, want %v", pending.NotMeasured, want)
	}
	if len(pending.Launchers)+len(pending.Operators)+len(pending.Admins) != 0 {
		t.Errorf("a record with nothing to state reads as %+v", pending)
	}
}

// TestBindingsLabelsFugaroAccounts: every principal bound on the member's two
// projects is placed, and Fugaro's own accounts — the member's own Terraform
// accounts from its outputs (F17) and the workspace's IAM account (WU3, D2803) —
// are told from a person and from a binding nobody has placed (D2806).
func TestBindingsLabelsFugaroAccounts(t *testing.T) {
	ctx := t.Context()
	r := wu4New(t)
	buildIdentity := infra.BuildIdentityEmail(wu4Project)
	signer := infra.SignerAccountID + "@" + wu4Firebase + ".iam.gserviceaccount.com"
	r.seedInstallation(map[string]any{
		"runs_bucket":               wu4Runs,
		"scheduler_service_account": wu4MemberSA,
		"history_service_account":   "fugaro-history@" + wu4Project + ".iam.gserviceaccount.com",
		"credentials_service":       map[string]any{"name": "fugaro-creds", "region": "europe-west1", "url": "https://creds.example.test", "account": "fugaro-creds@" + wu4Project + ".iam.gserviceaccount.com"},
		"launchers":                 []string{},
		"operators":                 []string{},
	})
	r.crm.SetPolicy(wu4Project,
		gcpfake.Binding{Role: "roles/owner", Members: []string{"user:" + wu4Person, "serviceAccount:" + wu4WorkspaceIAM}},
		gcpfake.Binding{Role: "roles/editor", Members: []string{"serviceAccount:" + buildIdentity}},
		// A member's Terraform accounts hold conditional grants too, and
		// MemberBinding cannot carry a condition: the binding is listed flat,
		// which is the gap WU11 and WU12 have to know about.
		gcpfake.Binding{Role: "roles/viewer", Members: []string{"serviceAccount:" + wu4MemberSA},
			Condition: &gcpfake.IAMCondition{Title: "wu4", Expression: "resource.name.startsWith(\"projects/_/buckets/\")"}},
		gcpfake.Binding{Role: "roles/run.viewer", Members: []string{"group:eng@example.com", "serviceAccount:" + wu4ForeignSA}},
	)
	r.crm.SetPolicy(wu4Firebase,
		gcpfake.Binding{Role: "roles/owner", Members: []string{"user:" + wu4Other}},
		gcpfake.Binding{Role: "roles/firebasedatabase.admin", Members: []string{"serviceAccount:" + signer}},
	)

	got, err := r.ops.Bindings(ctx, r.m)
	if err != nil {
		t.Fatalf("Bindings: %v", err)
	}
	want := []string{
		wu4Project + " group:eng@example.com roles/run.viewer " + string(workspace.PrincipalUnknown),
		wu4Project + " serviceAccount:" + wu4ForeignSA + " roles/run.viewer " + string(workspace.PrincipalUnknown),
		wu4Project + " serviceAccount:" + buildIdentity + " roles/editor " + string(workspace.PrincipalTerraform),
		wu4Project + " serviceAccount:" + wu4MemberSA + " roles/viewer " + string(workspace.PrincipalTerraform),
		wu4Project + " serviceAccount:" + wu4WorkspaceIAM + " roles/owner " + string(workspace.PrincipalWorkspaceIAM),
		wu4Project + " user:" + wu4Person + " roles/owner " + string(workspace.PrincipalPerson),
		wu4Firebase + " serviceAccount:" + signer + " roles/firebasedatabase.admin " + string(workspace.PrincipalTerraform),
		wu4Firebase + " user:" + wu4Other + " roles/owner " + string(workspace.PrincipalPerson),
	}
	if !slices.Equal(wu4RenderBindings(got), want) {
		t.Errorf("Bindings reads\n  %v\nwant\n  %v", wu4RenderBindings(got), want)
	}
}

// wu4RenderBindings renders bindings as sorted lines, so a report of a wrong
// answer reads as a diff and not as a struct dump.
func wu4RenderBindings(bindings []workspace.MemberBinding) []string {
	out := make([]string, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, fmt.Sprintf("%s %s %s %s", b.Project, b.Principal, b.Role, b.Kind))
	}
	slices.Sort(out)
	return out
}

// TestMemberAuditCreateOnly: an entry is created under its id and never updated
// (D2811), the entry that is there is the event the caller handed over, and a
// second create of one id is a refusal with the first entry left as it was.
func TestMemberAuditCreateOnly(t *testing.T) {
	ctx := t.Context()
	r := wu4New(t)
	event := workspace.AuditEvent{
		Version: workspace.AuditEventVersion,
		ID:      "20261121T010203Z-wu4entry",
		Time:    time.Date(2026, 11, 21, 1, 2, 3, 0, time.UTC),
		Actor:   wu4Person,
		Action:  workspace.ActionGrantWrite,
		Project: wu4Project,
		Subject: wu4Other,
		Role:    workspace.RoleViewer,
	}
	raw, err := workspace.EncodeAuditEvent(event)
	if err != nil {
		t.Fatalf("encoding the fixture's event: %v", err)
	}
	if err := r.ops.Audit(ctx, r.m, event.ID, raw); err != nil {
		t.Fatalf("Audit: %v", err)
	}

	path := workspace.PathMemberAudit + "/" + event.ID
	var stored json.RawMessage
	found, err := r.client.Get(ctx, path, &stored)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if !found {
		t.Fatalf("nothing is at %s after the create", path)
	}
	back, err := workspace.DecodeAuditEvent(stored)
	if err != nil {
		t.Fatalf("the event the trail holds is not what C1's reader reads: %v", err)
	}
	if !reflect.DeepEqual(back, event) {
		t.Errorf("the trail holds %+v, want %+v", back, event)
	}

	// A second create of one id is a refusal, and the entry that was there is
	// the one that stays: the trail says what happened and is never rewritten.
	second := event
	second.Action = workspace.ActionGrantRemove
	secondRaw, err := workspace.EncodeAuditEvent(second)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.ops.Audit(ctx, r.m, event.ID, secondRaw); err == nil {
		t.Error("a second create of one entry's id replaced the entry, and the trail is create-only (D2811)")
	}
	var after json.RawMessage
	if _, err := r.client.Get(ctx, path, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, stored) {
		t.Errorf("the refused create rewrote the entry: it holds %s, and it held %s", after, stored)
	}

	// An event created under an id that is not its own is refused before
	// anything is written: the id is the key the entry is created at, and a
	// second spelling of it would file the entry where no reader looks.
	other := event
	other.ID = "20261121T010204Z-wu4other"
	otherRaw, err := workspace.EncodeAuditEvent(other)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.ops.Audit(ctx, r.m, event.ID, otherRaw); err == nil {
		t.Error("an event whose id is not the id it is filed under was created")
	}
	// An event C1's reader refuses is refused before anything is written: the
	// entry could never be corrected afterwards (D2811).
	if err := r.ops.Audit(ctx, r.m, other.ID, append(otherRaw[:len(otherRaw)-1], []byte(`,"note":"x"}`)...)); err == nil {
		t.Error("an event carrying a field C1's reader does not know was created")
	}
	if r.db.Value(workspace.PathMemberAudit+"/"+other.ID) != nil {
		t.Error("a refused create left an entry behind")
	}
}
