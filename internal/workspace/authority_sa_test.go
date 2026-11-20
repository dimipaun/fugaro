package workspace

// The SA authority (WU20), tested against a fake IAM surface: the roles/owner
// grant Link makes on a member's projects (R10) and the one Unlink takes off,
// the read Holds answers, the account's token creators that AddAdmin,
// RemoveAdmin and AdminBindings work on (R8, D2818), the console-invitation
// step Google's refusal outside an organization turns into (U8, D2825), and the
// fact that the account is only ever impersonated (D2803, F20).

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const (
	// wu20Workspace is the workspace's name, which is its own project's ID too
	// (A1, D1290): R8's grant to an admin lands on that project.
	wu20Workspace = "ws-aurora"
	// wu20Account is the workspace IAM account's address, as the workspace
	// root's output WorkspaceIAMAccountOutput carries it (WU3, D2803).
	wu20Account = "fugaro-workspace-iam@ws-aurora.iam.gserviceaccount.com"
	// wu20AccountName is that account as the IAM API names it: the form the
	// authority composes out of the workspace and the account, and the one an
	// admin's token-creator binding is on.
	wu20AccountName = "projects/ws-aurora/serviceAccounts/fugaro-workspace-iam@ws-aurora.iam.gserviceaccount.com"
	// wu20AccountMember is that account as an IAM policy member: the spelling
	// every grant and read of it uses, and the one this test checks reaches the
	// surface — the bare address above is not a member Google resolves.
	wu20AccountMember = "serviceAccount:" + wu20Account
	// wu20Admin and wu20Second are two workspace admins, so a test can see one
	// binding left alone while another goes.
	wu20Admin  = "user:ada@example.com"
	wu20Second = "user:grace@example.com"
)

// wu20IAM is the IAM surface the SA authority is tested against (WU20): one
// roles/owner list per project, one token-creator list per account, the
// projects whose owner grant Google refuses with ErrSAOwnerInvite (U8), and
// every call it was asked to make, in the order it was asked, named for the
// method that made it.
type wu20IAM struct {
	owners   map[string][]string
	creators map[string][]string
	refuse   map[string]bool
	log      []string
}

func wu20Cloud() *wu20IAM {
	return &wu20IAM{owners: map[string][]string{}, creators: map[string][]string{}, refuse: map[string]bool{}}
}

// sa is the authority under test, over this fake, with the workspace's name and
// the account WU3's root outputs.
func (f *wu20IAM) sa() *SAAuthority {
	return &SAAuthority{Workspace: wu20Workspace, Account: wu20Account, Projects: f}
}

// saWithOut is the authority with a buffer for what a command prints.
func (f *wu20IAM) saWithOut() (*SAAuthority, *bytes.Buffer) {
	var out bytes.Buffer
	a := f.sa()
	a.Out = &out
	return a, &out
}

// project adds a project the fake knows: one with no owner of its own, which is
// what a member's project is before a link.
func (f *wu20IAM) project(id string) { f.owners[id] = nil }

// creator adds a token creator of the account, so a test can set up a binding
// the authority did not write.
func (f *wu20IAM) creator(member string) {
	f.creators[wu20AccountName] = append(f.creators[wu20AccountName], member)
}

// GrantOwner refuses on a project in refuse, which is what Google answers an
// API owner grant on a project outside an organization with — and only when the
// call would ADD an owner: a member already bound there needs no write, and the
// real call's read-modify-write is then a no-op rather than a refusal (which is
// what makes AddAdmin converge once the invitation has been accepted).
func (f *wu20IAM) GrantOwner(_ context.Context, project, member string) error {
	f.log = append(f.log, fmt.Sprintf("GrantOwner %s %s", project, member))
	if slices.Contains(f.owners[project], member) {
		return nil
	}
	if f.refuse[project] {
		return fmt.Errorf("setIamPolicy on %s: %w", project, ErrSAOwnerInvite)
	}
	f.owners[project] = append(f.owners[project], member)
	return nil
}

func (f *wu20IAM) RemoveOwner(_ context.Context, project, member string) error {
	f.log = append(f.log, fmt.Sprintf("RemoveOwner %s %s", project, member))
	f.owners[project] = slices.DeleteFunc(f.owners[project], func(o string) bool { return o == member })
	return nil
}

func (f *wu20IAM) Owns(_ context.Context, project, member string) (bool, error) {
	f.log = append(f.log, fmt.Sprintf("Owns %s %s", project, member))
	return slices.Contains(f.owners[project], member), nil
}

func (f *wu20IAM) TokenCreators(_ context.Context, account string) ([]string, error) {
	f.log = append(f.log, "TokenCreators "+account)
	return slices.Clone(f.creators[account]), nil
}

func (f *wu20IAM) AddTokenCreator(_ context.Context, account, member string) error {
	f.log = append(f.log, fmt.Sprintf("AddTokenCreator %s %s", account, member))
	if !slices.Contains(f.creators[account], member) {
		f.creators[account] = append(f.creators[account], member)
	}
	return nil
}

func (f *wu20IAM) RemoveTokenCreator(_ context.Context, account, member string) error {
	f.log = append(f.log, fmt.Sprintf("RemoveTokenCreator %s %s", account, member))
	f.creators[account] = slices.DeleteFunc(f.creators[account], func(c string) bool { return c == member })
	return nil
}

// wu20Member is a member with a Firebase project of its own, which both of its
// projects are granted for (F2, R10).
func wu20Member() MemberTarget {
	return MemberTarget{Project: "member-a", Firebase: "member-a-firebase"}
}

// TestSALinkGrantsOwner: a link gives the workspace IAM account roles/owner on
// the member's project, and it is that grant which makes the workspace hold the
// member: Holds is false before and true after, and a second link is not a
// second binding.
func TestSALinkGrantsOwner(t *testing.T) {
	ctx := t.Context()
	iam := wu20Cloud()
	iam.project("member-a")
	m := MemberTarget{Project: "member-a"}
	a := iam.sa()

	if held, err := a.Holds(ctx, m); err != nil || held {
		t.Fatalf("Holds before a link = %v, %v; want false, nil", held, err)
	}
	if err := a.Link(ctx, m); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if want := []string{wu20AccountMember}; !slices.Equal(iam.owners["member-a"], want) {
		t.Errorf("member-a's owners after Link = %v, want %v", iam.owners["member-a"], want)
	}
	if held, err := a.Holds(ctx, m); err != nil || !held {
		t.Errorf("Holds after a link = %v, %v; want true, nil", held, err)
	}
	if err := a.Link(ctx, m); err != nil {
		t.Fatalf("Link again: %v", err)
	}
	if want := []string{wu20AccountMember}; !slices.Equal(iam.owners["member-a"], want) {
		t.Errorf("member-a's owners after a second Link = %v, want %v: a link is not a second binding", iam.owners["member-a"], want)
	}
	// The write is the member owner's own grant and the whole of what a link
	// does: nothing here impersonates the account and nothing here reaches a key
	// (D2803, F20).
	if want := []string{
		"Owns member-a " + wu20AccountMember,
		"GrantOwner member-a " + wu20AccountMember,
		"Owns member-a " + wu20AccountMember,
		"GrantOwner member-a " + wu20AccountMember,
	}; !slices.Equal(iam.log, want) {
		t.Errorf("the calls a link made are %v, want %v", iam.log, want)
	}
}

// TestSALinkCoversTheFirebaseProject: a member whose Firebase project is a
// project of its own is granted on both, and it is held only while both are —
// the authority over a member is the two grants or neither (F2, R10) — and a
// member whose Firebase project is its GCP project is one grant.
func TestSALinkCoversTheFirebaseProject(t *testing.T) {
	ctx := t.Context()
	iam := wu20Cloud()
	iam.project("member-a")
	iam.project("member-a-firebase")
	m := wu20Member()
	a := iam.sa()

	if err := a.Link(ctx, m); err != nil {
		t.Fatalf("Link: %v", err)
	}
	for _, p := range []string{"member-a", "member-a-firebase"} {
		if want := []string{wu20AccountMember}; !slices.Equal(iam.owners[p], want) {
			t.Errorf("%s's owners after Link = %v, want %v", p, iam.owners[p], want)
		}
	}
	if want := []string{
		"GrantOwner member-a " + wu20AccountMember,
		"GrantOwner member-a-firebase " + wu20AccountMember,
	}; !slices.Equal(iam.log, want) {
		t.Errorf("the calls a link made are %v, want %v: the GCP project first, then its Firebase project", iam.log, want)
	}
	if held, err := a.Holds(ctx, m); err != nil || !held {
		t.Errorf("Holds with both projects granted = %v, %v; want true, nil", held, err)
	}

	// Half a grant is not a hold: the Firebase project's grant is the one a
	// failed second call leaves off, and a member whose Firebase side the
	// account does not own is not one this workspace holds (R11).
	iam.owners["member-a-firebase"] = nil
	if held, err := a.Holds(ctx, m); err != nil || held {
		t.Errorf("Holds with the Firebase grant gone = %v, %v; want false, nil", held, err)
	}

	// The two spellings of one project are one grant.
	one := wu20Cloud()
	one.project("member-a")
	if err := one.sa().Link(ctx, MemberTarget{Project: "member-a", Firebase: "member-a"}); err != nil {
		t.Fatalf("Link with one project spelled twice: %v", err)
	}
	if want := []string{"GrantOwner member-a " + wu20AccountMember}; !slices.Equal(one.log, want) {
		t.Errorf("Link on a member whose Firebase project is its own made %v, want %v", one.log, want)
	}
}

// TestSAUnlinkRemovesGrant: an unlink takes the account's owner grant off both
// of the member's projects and nothing else — the admins' token creators are the
// workspace's own bindings and go with RemoveAdmin, not with a member's unlink
// (D2822) — and an unlink of a member the account does not own is not an error.
func TestSAUnlinkRemovesGrant(t *testing.T) {
	ctx := t.Context()
	iam := wu20Cloud()
	iam.project("member-a")
	iam.project("member-a-firebase")
	iam.creator(wu20Admin)
	m := wu20Member()
	a := iam.sa()

	if err := a.Link(ctx, m); err != nil {
		t.Fatalf("Link: %v", err)
	}
	iam.log = nil
	if err := a.Unlink(ctx, m); err != nil {
		t.Fatalf("Unlink: %v", err)
	}
	for _, p := range []string{"member-a", "member-a-firebase"} {
		if got := iam.owners[p]; len(got) != 0 {
			t.Errorf("%s's owners after Unlink = %v, want none", p, got)
		}
	}
	if held, err := a.Holds(ctx, m); err != nil || held {
		t.Errorf("Holds after Unlink = %v, %v; want false, nil", held, err)
	}
	if want := []string{wu20Admin}; !slices.Equal(iam.creators[wu20AccountName], want) {
		t.Errorf("the account's token creators after an unlink = %v, want %v: an unlink drops the authority over the member and not the workspace's own admins", iam.creators[wu20AccountName], want)
	}
	if want := []string{
		"RemoveOwner member-a " + wu20AccountMember,
		"RemoveOwner member-a-firebase " + wu20AccountMember,
		"Owns member-a " + wu20AccountMember,
	}; !slices.Equal(iam.log, want) {
		t.Errorf("the calls an unlink made are %v, want %v", iam.log, want)
	}
	if err := a.Unlink(ctx, m); err != nil {
		t.Errorf("Unlink of a member the account does not own: %v; want no error, as a removal that removes nothing", err)
	}
}

// TestSAAdminTokenCreatorAddedAndRemoved: an admin is bound as a token creator
// of the workspace IAM account — the grant that lets them impersonate it — and
// as an owner of the workspace's own project, where the roster and the audit
// live (R8, D2818); AdminBindings lists the token creators, and RemoveAdmin
// takes both bindings off, the owner grant first.
func TestSAAdminTokenCreatorAddedAndRemoved(t *testing.T) {
	ctx := t.Context()
	iam := wu20Cloud()
	iam.project(wu20Workspace)
	a := iam.sa()

	if err := a.AddAdmin(ctx, wu20Admin); err != nil {
		t.Fatalf("AddAdmin: %v", err)
	}
	if err := a.AddAdmin(ctx, wu20Second); err != nil {
		t.Fatalf("AddAdmin (second): %v", err)
	}
	if want := []string{wu20Admin, wu20Second}; !slices.Equal(iam.creators[wu20AccountName], want) {
		t.Errorf("the account's token creators = %v, want %v", iam.creators[wu20AccountName], want)
	}
	if want := []string{wu20Admin, wu20Second}; !slices.Equal(iam.owners[wu20Workspace], want) {
		t.Errorf("%s's owners = %v, want %v: an admin owns the workspace's own project, where the roster and the audit live (R8)", wu20Workspace, iam.owners[wu20Workspace], want)
	}
	bound, err := a.AdminBindings(ctx)
	if err != nil {
		t.Fatalf("AdminBindings: %v", err)
	}
	if want := []string{wu20Admin, wu20Second}; !slices.Equal(bound, want) {
		t.Errorf("AdminBindings = %v, want %v: the live bindings are the account's token creators (D2823)", bound, want)
	}

	iam.log = nil
	if err := a.RemoveAdmin(ctx, wu20Admin); err != nil {
		t.Fatalf("RemoveAdmin: %v", err)
	}
	if want := []string{wu20Second}; !slices.Equal(iam.creators[wu20AccountName], want) {
		t.Errorf("the account's token creators after RemoveAdmin = %v, want %v", iam.creators[wu20AccountName], want)
	}
	if want := []string{wu20Second}; !slices.Equal(iam.owners[wu20Workspace], want) {
		t.Errorf("%s's owners after RemoveAdmin = %v, want %v", wu20Workspace, iam.owners[wu20Workspace], want)
	}
	if want := []string{
		"RemoveOwner " + wu20Workspace + " " + wu20Admin,
		"RemoveTokenCreator " + wu20AccountName + " " + wu20Admin,
	}; !slices.Equal(iam.log, want) {
		t.Errorf("the calls RemoveAdmin made are %v, want %v: the owner grant comes off first, because the owner of a project can grant themselves the token creator back", iam.log, want)
	}

	// An empty address names nobody, and nothing is written for it.
	iam.log = nil
	if err := a.AddAdmin(ctx, ""); err == nil {
		t.Error("AddAdmin with no address: want a refusal")
	}
	if err := a.RemoveAdmin(ctx, ""); err == nil {
		t.Error("RemoveAdmin with no address: want a refusal")
	}
	if len(iam.log) != 0 {
		t.Errorf("a call with no address made %v, want nothing at all", iam.log)
	}
}

// TestSAAddAdminPrintsTheInviteStep: outside an organization Google answers an
// owner grant made by API with a refusal, and R8's grant is then an invitation
// the person accepts in the console (U8, D2825). AddAdmin prints that step, says
// the binding is not there rather than reporting a grant it did not make, and
// leaves the token creator bound — that half of an admin's authority is a
// service-account binding and goes through by API whatever the owner grant
// answers.
func TestSAAddAdminPrintsTheInviteStep(t *testing.T) {
	ctx := t.Context()
	iam := wu20Cloud()
	iam.project(wu20Workspace)
	iam.refuse[wu20Workspace] = true
	a, out := iam.saWithOut()

	err := a.AddAdmin(ctx, wu20Admin)
	if err == nil {
		t.Fatal("AddAdmin with Google refusing the owner grant: want an error, as the binding is not there")
	}
	if !errors.Is(err, ErrSAOwnerInvite) {
		t.Errorf("AddAdmin's error is %v, want one that carries ErrSAOwnerInvite: a caller has to be able to tell this refusal from another", err)
	}
	step := out.String()
	for _, want := range []string{wu20Admin, SAConsoleIAM + wu20Workspace} {
		if !strings.Contains(step, want) {
			t.Errorf("the printed invitation step is %q, want it to name %q", step, want)
		}
	}
	if want := []string{wu20Admin}; !slices.Equal(iam.creators[wu20AccountName], want) {
		t.Errorf("the account's token creators = %v, want %v: the impersonation grant is a service-account binding and it is made whatever the owner grant answers", iam.creators[wu20AccountName], want)
	}
	if got := iam.owners[wu20Workspace]; len(got) != 0 {
		t.Errorf("%s's owners = %v, want none: Google refused the grant and the print is not a binding", wu20Workspace, got)
	}

	// Once the invitation has been accepted the binding is on the project, and
	// the same call converges: the token creator is already bound, the owner
	// grant needs no write, and nothing is refused.
	iam.owners[wu20Workspace] = []string{wu20Admin}
	iam.log = nil
	if err := a.AddAdmin(ctx, wu20Admin); err != nil {
		t.Errorf("AddAdmin after the invitation was accepted: %v; want no error", err)
	}
	if want := []string{
		"AddTokenCreator " + wu20AccountName + " " + wu20Admin,
		"GrantOwner " + wu20Workspace + " " + wu20Admin,
	}; !slices.Equal(iam.log, want) {
		t.Errorf("the calls AddAdmin made are %v, want %v", iam.log, want)
	}
	if want := []string{wu20Admin}; !slices.Equal(iam.owners[wu20Workspace], want) {
		t.Errorf("%s's owners = %v, want %v", wu20Workspace, iam.owners[wu20Workspace], want)
	}
}

// TestSANeverReadsAKey: the account is impersonated and never keyed (D2803,
// F20). Two halves: the IAM surface the authority is given has no method that
// could create, list or read a service-account key — its whole account side is
// the token-creator bindings, which is how impersonation is granted — and the
// authority holds nothing of the account but its address.
func TestSANeverReadsAKey(t *testing.T) {
	ctx := t.Context()
	surface := reflect.TypeOf((*SAProjects)(nil)).Elem()
	want := []string{"AddTokenCreator", "GrantOwner", "Owns", "RemoveOwner", "RemoveTokenCreator", "TokenCreators"}
	got := make([]string, 0, surface.NumMethod())
	for i := 0; i < surface.NumMethod(); i++ {
		got = append(got, surface.Method(i).Name)
	}
	if !slices.Equal(got, want) {
		t.Errorf("workspace.SAProjects' methods are %v, want %v: these six are the whole surface the SA authority reaches a member through, and a method beside them is a call it could make", got, want)
	}
	for _, name := range got {
		if strings.Contains(strings.ToLower(name), "key") {
			t.Errorf("workspace.SAProjects carries %s: a key of the workspace IAM account is a credential that outlives every revocation this design has, and the account is only ever impersonated (D2803, F20)", name)
		}
	}

	fields := reflect.TypeOf(SAAuthority{})
	var names []string
	for i := 0; i < fields.NumField(); i++ {
		name := fields.Field(i).Name
		names = append(names, name)
		if strings.Contains(strings.ToLower(name), "key") {
			t.Errorf("SAAuthority carries the field %s: it holds the account's address and no key of it", name)
		}
	}
	if wantFields := []string{"Workspace", "Account", "Projects", "Out"}; !slices.Equal(names, wantFields) {
		t.Errorf("SAAuthority's fields are %v, want %v", names, wantFields)
	}

	// A whole lifecycle, read back call by call: every call is one of those
	// six methods, and the account is reached through the token-creator two,
	// which is impersonation granted and never a key.
	iam := wu20Cloud()
	iam.project("member-a")
	iam.project(wu20Workspace)
	iam.refuse[wu20Workspace] = true
	a, _ := iam.saWithOut()
	m := MemberTarget{Project: "member-a"}
	if err := a.Link(ctx, m); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if _, err := a.Holds(ctx, m); err != nil {
		t.Fatalf("Holds: %v", err)
	}
	if err := a.AddAdmin(ctx, wu20Admin); err == nil {
		t.Fatal("AddAdmin against a fake that refuses the owner grant: want the console step and an error")
	}
	if _, err := a.AdminBindings(ctx); err != nil {
		t.Fatalf("AdminBindings: %v", err)
	}
	if err := a.RemoveAdmin(ctx, wu20Admin); err != nil {
		t.Fatalf("RemoveAdmin: %v", err)
	}
	if err := a.Unlink(ctx, m); err != nil {
		t.Fatalf("Unlink: %v", err)
	}
	if len(iam.log) == 0 {
		t.Fatal("the lifecycle made no call at all")
	}
	for _, call := range iam.log {
		verb, _, _ := strings.Cut(call, " ")
		if !slices.Contains(want, verb) {
			t.Errorf("the lifecycle made the call %q, whose %s is not one of the six methods the surface has", call, verb)
		}
		if strings.Contains(strings.ToLower(call), "key") {
			t.Errorf("the lifecycle made the call %q: the account is impersonated, never keyed (D2803, F20)", call)
		}
	}
}

// TestSARefusesWhenNothingNamesTheAccount: a workspace whose account is unknown
// refuses every act rather than granting with an empty principal, and the
// refusal names the output the address comes from (WU3, C2's
// WorkspaceIAMAccountOutput). Nothing is called.
func TestSARefusesWhenNothingNamesTheAccount(t *testing.T) {
	ctx := t.Context()
	iam := wu20Cloud()
	iam.project("member-a")
	iam.project(wu20Workspace)
	a := &SAAuthority{Workspace: wu20Workspace, Projects: iam}
	m := MemberTarget{Project: "member-a"}
	held, holdErr := a.Holds(ctx, m)
	bound, bindingsErr := a.AdminBindings(ctx)

	refusals := []struct {
		name string
		err  error
	}{
		{"Link", a.Link(ctx, m)},
		{"Unlink", a.Unlink(ctx, m)},
		{"Holds", holdErr},
		{"AddAdmin", a.AddAdmin(ctx, wu20Admin)},
		{"RemoveAdmin", a.RemoveAdmin(ctx, wu20Admin)},
		{"AdminBindings", bindingsErr},
	}
	for _, r := range refusals {
		if r.err == nil {
			t.Errorf("%s with no account: want a refusal", r.name)
			continue
		}
		if !strings.Contains(r.err.Error(), WorkspaceIAMAccountOutput) {
			t.Errorf("%s' refusal is %v, want it to name the %s output", r.name, r.err, WorkspaceIAMAccountOutput)
		}
	}
	if held {
		t.Error("Holds with no account: want false")
	}
	if bound != nil {
		t.Errorf("AdminBindings with no account = %v, want nothing", bound)
	}
	if err := a.Link(ctx, MemberTarget{}); err == nil {
		t.Error("Link with no project: want a refusal")
	}
	if len(iam.log) != 0 {
		t.Errorf("a refusal made the calls %v, want none", iam.log)
	}
}
