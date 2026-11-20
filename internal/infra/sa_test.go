package infra

// The SA authority's IAM calls (WU20) against the Resource Manager and IAM
// fakes: the roles/owner grant a link makes on a member's project and an unlink
// takes off (R10), the token-creator binding an admin holds on the workspace
// account and the owner grant R8 adds beside it, and Google's refusal of an
// owner grant by API — the one the console-invitation step answers (U8, D2825).

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	iam "google.golang.org/api/iam/v1"
	"google.golang.org/api/option"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// The SA surface is what the SA authority takes (WU20's SAProjects interface):
// the satisfaction is pinned here, in a test, for the reason the folder pin
// gives — the two packages cannot name each other's types in production, and the
// interface is what lets I1 hand *SAProjects in with no adapter.
var _ workspace.SAProjects = (*SAProjects)(nil)

// The names one test uses: the workspace and its own project are one name (A1,
// D1290), the account lives in it, and a member is another project.
const (
	wu20Workspace = "ws-aurora"
	wu20Account   = "fugaro-workspace-iam@ws-aurora.iam.gserviceaccount.com"
	// wu20AccountMember is the account as an IAM policy member, which is the
	// spelling every owner binding holds: the bare address above is the
	// principal's name, and a policy that wrote it as a member would be refused.
	wu20AccountMember = "serviceAccount:" + wu20Account
	wu20Member        = "member-a"
	wu20Admin         = "user:ada@example.com"
	wu20Second        = "user:grace@example.com"
)

// wu20Rig is the two fakes, the clients over them and the surface under test.
// The clients are the same ones the surface holds, so a test reads a policy back
// through the API rather than out of the fake's own map.
type wu20Rig struct {
	crm *gcpfake.CRM
	iam *gcpfake.IAM
	c   *crm.Service
	i   *iam.Service
	sap *SAProjects
}

func wu20Cloud(t *testing.T) *wu20Rig {
	t.Helper()
	f := newProjectFakes(t)
	r := &wu20Rig{crm: f.crm, iam: gcpfake.NewIAM(t)}
	ctx := t.Context()
	opts := func(u string) []option.ClientOption {
		return []option.ClientOption{option.WithEndpoint(u), option.WithoutAuthentication()}
	}
	var err error
	if r.c, err = crm.NewService(ctx, opts(r.crm.URL+"/")...); err != nil {
		t.Fatal(err)
	}
	if r.i, err = iam.NewService(ctx, opts(r.iam.URL+"/")...); err != nil {
		t.Fatal(err)
	}
	r.sap = NewSAProjects(r.c, r.i)
	return r
}

// member is a member project and a workspace project the fake knows, which is
// what every read here needs: the fake answers 403 for a project it does not
// have, and a policy read of one is not this test's subject.
func (r *wu20Rig) projects(t *testing.T) {
	t.Helper()
	r.crm.AddProject(wu20Member, 900000000123)
	r.crm.AddProject(wu20Workspace, 900000000456)
}

// account makes the workspace IAM account exist in the workspace's own project,
// as WU3's root creates it.
func (r *wu20Rig) account(t *testing.T) string {
	t.Helper()
	r.iam.AddServiceAccount(wu20Workspace, wu20Account, "workspace IAM account")
	return "projects/" + wu20Workspace + "/serviceAccounts/" + wu20Account
}

// owners is the roles/owner binding of project, read back through the API.
func (r *wu20Rig) owners(t *testing.T, project string) []string {
	t.Helper()
	pol, err := r.c.Projects.GetIamPolicy(project, &crm.GetIamPolicyRequest{}).Context(t.Context()).Do()
	if err != nil {
		t.Fatalf("reading the policy of %s: %v", project, err)
	}
	var out []string
	for _, b := range pol.Bindings {
		if b.Role == SAProjectOwnerRole {
			out = append(out, b.Members...)
		}
	}
	return out
}

// creators is the token-creator binding of account, read back through the API.
func (r *wu20Rig) creators(t *testing.T, account string) []string {
	t.Helper()
	pol, err := r.i.Projects.ServiceAccounts.GetIamPolicy(account).OptionsRequestedPolicyVersion(3).Context(t.Context()).Do()
	if err != nil {
		t.Fatalf("reading the policy of %s: %v", account, err)
	}
	var out []string
	for _, b := range pol.Bindings {
		if b.Role == SATokenCreatorRole {
			out = append(out, b.Members...)
		}
	}
	return out
}

// TestSACloudOwnerGrantAddedAndRemoved: the grant a link makes is roles/owner
// on the member's project, adding it once and taking it off, with every other
// binding of the project carried through the write untouched (the fake holds a
// project's bindings and not its audit-logging configuration, so what is pinned
// here is that half; the write carries the other).
func TestSACloudOwnerGrantAddedAndRemoved(t *testing.T) {
	ctx := context.Background()
	r := wu20Cloud(t)
	r.projects(t)
	r.crm.SetPolicy(wu20Member,
		gcpfake.Binding{Role: "roles/viewer", Members: []string{"user:viewer@example.com"}},
		gcpfake.Binding{Role: SAProjectOwnerRole, Members: []string{"user:founder@example.com"}})

	for range 2 {
		if err := r.sap.GrantOwner(ctx, wu20Member, wu20AccountMember); err != nil {
			t.Fatalf("GrantOwner: %v", err)
		}
	}
	if got, want := r.owners(t, wu20Member), []string{"user:founder@example.com", wu20AccountMember}; !slices.Equal(got, want) {
		t.Errorf("the project's owners = %v, want %v: granted once, however often the call is made", got, want)
	}
	if held, err := r.sap.Owns(ctx, wu20Member, wu20AccountMember); err != nil || !held {
		t.Errorf("Owns after GrantOwner = %v, %v; want true, nil", held, err)
	}
	if err := r.sap.RemoveOwner(ctx, wu20Member, wu20AccountMember); err != nil {
		t.Fatalf("RemoveOwner: %v", err)
	}
	if got, want := r.owners(t, wu20Member), []string{"user:founder@example.com"}; !slices.Equal(got, want) {
		t.Errorf("the project's owners after RemoveOwner = %v, want %v", got, want)
	}
	if held, err := r.sap.Owns(ctx, wu20Member, wu20AccountMember); err != nil || held {
		t.Errorf("Owns after RemoveOwner = %v, %v; want false, nil", held, err)
	}
	// A removal of a member the project does not bind changes nothing and is not
	// an error: an unlink of a member the account never held is a no-op.
	if err := r.sap.RemoveOwner(ctx, wu20Member, wu20AccountMember); err != nil {
		t.Errorf("RemoveOwner of a member the project does not bind: %v", err)
	}
	pol, err := r.c.Projects.GetIamPolicy(wu20Member, &crm.GetIamPolicyRequest{}).Context(ctx).Do()
	if err != nil {
		t.Fatal(err)
	}
	if len(pol.Bindings) != 2 || pol.Bindings[0].Role != "roles/viewer" ||
		!slices.Equal(pol.Bindings[0].Members, []string{"user:viewer@example.com"}) {
		t.Errorf("the project's policy is %+v, want the viewer binding carried through the owner writes", pol.Bindings)
	}

	// A project the caller may not read is refused with the API's own words.
	r.crm.DenyPolicy(wu20Member)
	if _, err := r.sap.Owns(ctx, wu20Member, wu20AccountMember); err == nil {
		t.Error("Owns of a project whose policy cannot be read = nil, want the API's refusal")
	}
}

// TestSACloudTokenCreatorsAddedAndRemoved: an admin is bound to the token
// creator on the account, listed, and unbound, with the account's other
// bindings carried through, and a member Google marks deleted is not listed —
// it can no longer act as the account.
func TestSACloudTokenCreatorsAddedAndRemoved(t *testing.T) {
	ctx := context.Background()
	r := wu20Cloud(t)
	account := r.account(t)
	r.iam.SetServiceAccountPolicy(wu20Workspace, wu20Account,
		gcpfake.Binding{Role: "roles/iam.serviceAccountUser", Members: []string{"user:operator@example.com"}},
		gcpfake.Binding{Role: SATokenCreatorRole, Members: []string{"user:founder@example.com", "deleted:user:gone@example.com"}})

	if got, want := r.creatorsOf(t, account), []string{"user:founder@example.com", "deleted:user:gone@example.com"}; !slices.Equal(got, want) {
		t.Errorf("the account's token creators = %v, want %v as the policy holds them", got, want)
	}
	for range 2 {
		if err := r.sap.AddTokenCreator(ctx, account, wu20Admin); err != nil {
			t.Fatalf("AddTokenCreator: %v", err)
		}
	}
	if err := r.sap.AddTokenCreator(ctx, account, wu20Second); err != nil {
		t.Fatalf("AddTokenCreator (second): %v", err)
	}
	if got, want := r.creators(t, account), []string{"user:founder@example.com", "deleted:user:gone@example.com", wu20Admin, wu20Second}; !slices.Equal(got, want) {
		t.Errorf("the account's token creators = %v, want %v", got, want)
	}
	listed, err := r.sap.TokenCreators(ctx, account)
	if err != nil {
		t.Fatalf("TokenCreators: %v", err)
	}
	if want := []string{"user:founder@example.com", wu20Admin, wu20Second}; !slices.Equal(listed, want) {
		t.Errorf("TokenCreators = %v, want %v: AdminBindings compares the roster against these, and a deleted member is not one", listed, want)
	}
	if err := r.sap.RemoveTokenCreator(ctx, account, wu20Admin); err != nil {
		t.Fatalf("RemoveTokenCreator: %v", err)
	}
	if got, want := r.creators(t, account), []string{"user:founder@example.com", "deleted:user:gone@example.com", wu20Second}; !slices.Equal(got, want) {
		t.Errorf("the account's token creators after RemoveTokenCreator = %v, want %v", got, want)
	}
	if err := r.sap.RemoveTokenCreator(ctx, account, wu20Admin); err != nil {
		t.Errorf("RemoveTokenCreator of a member the account does not bind: %v", err)
	}
	pol, err := r.i.Projects.ServiceAccounts.GetIamPolicy(account).OptionsRequestedPolicyVersion(3).Context(ctx).Do()
	if err != nil {
		t.Fatal(err)
	}
	if len(pol.Bindings) != 2 || pol.Bindings[0].Role != "roles/iam.serviceAccountUser" ||
		!slices.Equal(pol.Bindings[0].Members, []string{"user:operator@example.com"}) {
		t.Errorf("the account's policy is %+v, want the serviceAccountUser binding carried through", pol.Bindings)
	}

	// An account that does not exist is refused with the API's own words.
	if err := r.sap.AddTokenCreator(ctx, "projects/"+wu20Workspace+"/serviceAccounts/nope@"+wu20Workspace+".iam.gserviceaccount.com", wu20Admin); err == nil {
		t.Error("AddTokenCreator on an account that does not exist = nil, want the API's refusal")
	}
}

// creatorsOf is the token-creator binding as the policy holds it, deleted
// members included: what the fake was set up with, against what the surface
// reports.
func (r *wu20Rig) creatorsOf(t *testing.T, account string) []string {
	t.Helper()
	pol, err := r.i.Projects.ServiceAccounts.GetIamPolicy(account).OptionsRequestedPolicyVersion(3).Context(t.Context()).Do()
	if err != nil {
		t.Fatalf("reading the policy of %s: %v", account, err)
	}
	var out []string
	for _, b := range pol.Bindings {
		if b.Role == SATokenCreatorRole {
			out = append(out, b.Members...)
		}
	}
	return out
}

// TestSACloudConditionedBindings: a binding carrying a condition is another
// writer's. It is not counted as a hold (the answer is a gate, and a condition
// cannot be evaluated here), it is kept in the policy by every write, and a
// removal of a member it names is refused rather than reported as made.
func TestSACloudConditionedBindings(t *testing.T) {
	ctx := context.Background()
	r := wu20Cloud(t)
	r.projects(t)
	account := r.account(t)
	condition := &gcpfake.IAMCondition{Title: "on call", Expression: "request.time < timestamp('2026-12-01T00:00:00Z')"}
	r.crm.SetPolicy(wu20Member,
		gcpfake.Binding{Role: SAProjectOwnerRole, Members: []string{"user:founder@example.com"}},
		gcpfake.Binding{Role: SAProjectOwnerRole, Members: []string{wu20Second}, Condition: condition})
	r.iam.SetServiceAccountPolicy(wu20Workspace, wu20Account,
		gcpfake.Binding{Role: SATokenCreatorRole, Members: []string{wu20Second}, Condition: condition})

	if held, err := r.sap.Owns(ctx, wu20Member, wu20Second); err != nil || held {
		t.Errorf("Owns of a member a conditional binding names = %v, %v; want false, nil: the read is the workspace's authority, and a condition cannot be evaluated here", held, err)
	}
	if err := r.sap.GrantOwner(ctx, wu20Member, wu20Second); err != nil {
		t.Fatalf("GrantOwner beside a conditional binding: %v", err)
	}
	pol, err := r.c.Projects.GetIamPolicy(wu20Member, &crm.GetIamPolicyRequest{}).Context(ctx).Do()
	if err != nil {
		t.Fatal(err)
	}
	if len(pol.Bindings) != 2 || pol.Bindings[0].Condition == nil ||
		!slices.Equal(pol.Bindings[0].Members, []string{wu20Second}) ||
		!slices.Equal(pol.Bindings[1].Members, []string{"user:founder@example.com", wu20Second}) {
		t.Errorf("the project's policy is %+v, want the conditional binding kept beside the unconditional one added", pol.Bindings)
	}
	if held, err := r.sap.Owns(ctx, wu20Member, wu20Second); err != nil || !held {
		t.Errorf("Owns after the unconditional grant = %v, %v; want true, nil", held, err)
	}
	// The conditioned member cannot be reported removed: the binding this call
	// writes is not the one holding it.
	err = r.sap.RemoveOwner(ctx, wu20Member, wu20Second)
	if err == nil {
		t.Fatal("RemoveOwner of a member a conditional binding names = nil, want a refusal")
	}
	for _, want := range []string{wu20Second, wu20Member, "condition"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %q", err, want)
		}
	}
	// The member the unconditional binding holds and the conditional one does
	// not is removed as usual.
	if err := r.sap.RemoveOwner(ctx, wu20Member, "user:founder@example.com"); err != nil {
		t.Fatalf("RemoveOwner of the unconditional member: %v", err)
	}

	// The same two answers on the account side.
	listed, err := r.sap.TokenCreators(ctx, account)
	if err != nil {
		t.Fatalf("TokenCreators: %v", err)
	}
	if len(listed) != 0 {
		t.Errorf("TokenCreators = %v, want none: a member a condition binds is not trusted as an admin (D2823)", listed)
	}
	if err := r.sap.RemoveTokenCreator(ctx, account, wu20Second); err == nil {
		t.Error("RemoveTokenCreator of a member a conditional binding names = nil, want a refusal")
	}
	if err := r.sap.AddTokenCreator(ctx, account, wu20Admin); err != nil {
		t.Fatalf("AddTokenCreator beside a conditional binding: %v", err)
	}
	pol2, err := r.i.Projects.ServiceAccounts.GetIamPolicy(account).OptionsRequestedPolicyVersion(3).Context(ctx).Do()
	if err != nil {
		t.Fatal(err)
	}
	if len(pol2.Bindings) != 2 || pol2.Bindings[0].Condition == nil ||
		!slices.Equal(pol2.Bindings[0].Members, []string{wu20Second}) ||
		!slices.Equal(pol2.Bindings[1].Members, []string{wu20Admin}) {
		t.Errorf("the account's policy is %+v, want the conditional binding kept beside the unconditional one added", pol2.Bindings)
	}
}

// TestSACloudOwnerRefusalIsTheInvite: Google refusing the owner write is the one
// refusal the caller is told apart, because the answer to it is a console
// invitation rather than a retry (U8, D2825). Every other refusal is left as
// itself, and the API's own words stay in the message either way.
func TestSACloudOwnerRefusalIsTheInvite(t *testing.T) {
	ctx := context.Background()
	r := wu20Cloud(t)
	r.projects(t)
	r.crm.AddProject("member-b", 900000000789)

	for _, code := range []int{400, 412} {
		r.crm.RefusePath(":setIamPolicy", code, "FAILED_PRECONDITION", "FAILED_PRECONDITION", "basic roles cannot be granted here")
		err := r.sap.GrantOwner(ctx, wu20Member, wu20AccountMember)
		if err == nil {
			t.Fatalf("GrantOwner with the API answering %d = nil, want a refusal", code)
		}
		if !errors.Is(err, workspace.ErrSAOwnerInvite) {
			t.Errorf("GrantOwner with the API answering %d = %v, want it to carry ErrSAOwnerInvite", code, err)
		}
		if !strings.Contains(err.Error(), "basic roles cannot be granted here") {
			t.Errorf("the refusal %q does not carry the API's own words", err)
		}
	}
	r.crm.RefusePath(":setIamPolicy", 0, "", "", "")

	// A 403 is a caller who may not write the policy at all: it is reported as
	// itself, because sending that caller to the console would hide a permission
	// they can fix.
	r.crm.RefusePath(":setIamPolicy", 403, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "Permission 'resourcemanager.projects.setIamPolicy' denied")
	err := r.sap.GrantOwner(ctx, "member-b", wu20AccountMember)
	if err == nil {
		t.Fatal("GrantOwner with the write denied = nil, want a refusal")
	}
	if errors.Is(err, workspace.ErrSAOwnerInvite) {
		t.Errorf("GrantOwner = %v, want no ErrSAOwnerInvite for a permission the caller can be given", err)
	}
	r.crm.RefusePath(":setIamPolicy", 0, "", "", "")

	// A policy the caller may not read is refused by the read, before any write.
	r.crm.DenyPolicy("member-b")
	if _, err := r.sap.Owns(ctx, "member-b", wu20AccountMember); err == nil {
		t.Error("Owns of a project whose policy cannot be read = nil, want the API's refusal")
	}

	// A READ answered with a 400 is the read's own answer and not U8's: no
	// invitation is being extended by a policy that could not be read, so the
	// caller is not sent to the console for it.
	r.crm.RefusePath(":getIamPolicy", 400, "INVALID_ARGUMENT", "", "the request is malformed")
	err = r.sap.GrantOwner(ctx, "member-b", wu20AccountMember)
	if err == nil {
		t.Fatal("GrantOwner with the read refused = nil, want a refusal")
	}
	if errors.Is(err, workspace.ErrSAOwnerInvite) {
		t.Errorf("GrantOwner = %v, want no ErrSAOwnerInvite for a refusal of the read", err)
	}
	r.crm.RefusePath(":getIamPolicy", 0, "", "", "")

	// Removing the role is not the invite's case either: the invitation is about
	// a grant being made. The project holds the grant already — that is the state
	// the person's accepted invitation leaves it in — so the removal is a write.
	r.crm.SetPolicy(wu20Member, gcpfake.Binding{Role: SAProjectOwnerRole, Members: []string{wu20AccountMember}})
	r.crm.RefusePath(":setIamPolicy", 400, "INVALID_ARGUMENT", "", "refused")
	if err := r.sap.RemoveOwner(ctx, wu20Member, wu20AccountMember); err == nil {
		t.Error("RemoveOwner with the write refused = nil, want a refusal")
	} else if errors.Is(err, workspace.ErrSAOwnerInvite) {
		t.Errorf("RemoveOwner = %v, want no ErrSAOwnerInvite: no invitation adds the role back", err)
	}
	r.crm.RefusePath(":setIamPolicy", 0, "", "", "")

	// With the refusal lifted the calls converge: the grant is already there, so
	// GrantOwner writes nothing and adds nothing, and the removal the refusal
	// stopped goes through.
	if err := r.sap.GrantOwner(ctx, wu20Member, wu20AccountMember); err != nil {
		t.Fatalf("GrantOwner with the refusal lifted: %v", err)
	}
	if got, want := r.owners(t, wu20Member), []string{wu20AccountMember}; !slices.Equal(got, want) {
		t.Errorf("the project's owners = %v, want %v", got, want)
	}
	if err := r.sap.RemoveOwner(ctx, wu20Member, wu20AccountMember); err != nil {
		t.Fatalf("RemoveOwner with the refusal lifted: %v", err)
	}
	if got := r.owners(t, wu20Member); len(got) != 0 {
		t.Errorf("the project's owners after RemoveOwner = %v, want none", got)
	}
}

// TestSACloudFakeServesTheServiceAccountPolicyWrite: the IAM fake's
// serviceAccounts.setIamPolicy, added with this task (the plan's shared-file
// table puts iam.go with WU21, which extends it further), pinned so that the
// surface the SA authority and the drift reads lean on is exercised here.
func TestSACloudFakeServesTheServiceAccountPolicyWrite(t *testing.T) {
	ctx := context.Background()
	r := wu20Cloud(t)
	account := r.account(t)
	want := &iam.Policy{Bindings: []*iam.Binding{{Role: SATokenCreatorRole, Members: []string{wu20Admin}}}}
	if _, err := r.i.Projects.ServiceAccounts.SetIamPolicy(account, &iam.SetIamPolicyRequest{Policy: want}).Context(ctx).Do(); err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}
	got, err := r.i.Projects.ServiceAccounts.GetIamPolicy(account).OptionsRequestedPolicyVersion(3).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy: %v", err)
	}
	if len(got.Bindings) != 1 || got.Bindings[0].Role != SATokenCreatorRole ||
		!slices.Equal(got.Bindings[0].Members, []string{wu20Admin}) {
		t.Errorf("the account's policy is %+v, want the binding that was written", got.Bindings)
	}
}
