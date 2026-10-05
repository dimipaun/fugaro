package infra

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	iam "google.golang.org/api/iam/v1"
	"google.golang.org/api/option"

	"github.com/dimipaun/fugaro/internal/gcpfake"
)

const (
	tsProject = "fp-1"
	tsSigner  = "fugaro-token-signer@fp-1.iam.gserviceaccount.com"
	tsSched   = "fugaro-scheduler@fp-1.iam.gserviceaccount.com"
	tsAdminSD = "firebase-adminsdk-fbsvc@fp-1.iam.gserviceaccount.com"
	tsMinter  = "projects/fp-1/roles/fugaroTokenMinter"
)

type signerRig struct {
	crm *gcpfake.CRM
	iam *gcpfake.IAM
}

// newSignerRig is a Firebase project with the signer, the scheduler and the
// Firebase Admin SDK accounts, the minter role as the module writes it, an
// owner, and nothing else.
func newSignerRig(t *testing.T) *signerRig {
	t.Helper()
	r := &signerRig{crm: gcpfake.NewCRM(t), iam: gcpfake.NewIAM(t)}
	r.crm.AddProject(tsProject, 111)
	r.crm.SetPolicy(tsProject, gcpfake.Binding{Role: "roles/owner", Members: []string{"user:owner@example.com"}})
	for _, a := range []string{tsSigner, tsSched, tsAdminSD} {
		r.iam.AddServiceAccount(tsProject, a, a)
	}
	r.iam.AddRole(tsProject, "fugaroTokenMinter", "Fugaro token minter", false)
	r.iam.SetRolePermissions(tsMinter, "iam.serviceAccounts.signJwt")
	SeedPredefinedRoles(r.iam)
	return r
}

// SeedPredefinedRoles makes the predefined roles the tests bind readable, with
// the permissions that matter here (the real roles hold many more).
func SeedPredefinedRoles(f *gcpfake.IAM) {
	f.SetRolePermissions("roles/iam.serviceAccountTokenCreator", "iam.serviceAccounts.getAccessToken", "iam.serviceAccounts.signBlob", "iam.serviceAccounts.signJwt", "iam.serviceAccounts.implicitDelegation")
	f.SetRolePermissions("roles/iam.serviceAccountKeyAdmin", "iam.serviceAccountKeys.create", "iam.serviceAccountKeys.list")
	f.SetRolePermissions("roles/iam.serviceAccountAdmin", "iam.serviceAccounts.setIamPolicy", "iam.serviceAccounts.get")
	f.SetRolePermissions("roles/iam.securityAdmin", "resourcemanager.projects.setIamPolicy", "iam.serviceAccounts.setIamPolicy")
	f.SetRolePermissions("roles/owner", "resourcemanager.projects.get", "iam.serviceAccounts.get")
	f.SetRolePermissions("roles/editor", "iam.serviceAccountKeys.create", "iam.serviceAccounts.get")
	f.SetRolePermissions("roles/viewer", "resourcemanager.projects.get")
	f.SetRolePermissions("roles/run.invoker", "run.jobs.run")
	f.SetRolePermissions("roles/iam.serviceAccountUser", "iam.serviceAccounts.actAs")
	f.SetRolePermissions("roles/firebase.sdkAdminServiceAgent", "firebase.projects.get")
}

func (r *signerRig) find(t *testing.T) ([]SignerFinding, error) {
	t.Helper()
	ctx := context.Background()
	opts := func(u string) []option.ClientOption {
		return []option.ClientOption{option.WithEndpoint(u), option.WithoutAuthentication()}
	}
	c, err := crm.NewService(ctx, opts(r.crm.URL+"/")...)
	if err != nil {
		t.Fatal(err)
	}
	i, err := iam.NewService(ctx, opts(r.iam.URL+"/")...)
	if err != nil {
		t.Fatal(err)
	}
	return TokenSigners(ctx, c, i, tsProject, tsSigner)
}

func unexpected(fs []SignerFinding) []SignerFinding {
	var out []SignerFinding
	for _, f := range fs {
		if !f.Expected {
			out = append(out, f)
		}
	}
	return out
}

func TestTokenSignersNone(t *testing.T) {
	r := newSignerRig(t)
	fs, err := r.find(t)
	if err != nil || len(fs) != 0 {
		t.Fatalf("findings %+v, err %v", fs, err)
	}
}

// The designed path and the Firebase Admin SDK account are listed, as
// expected, and never as a risk.
func TestTokenSignersExpected(t *testing.T) {
	r := newSignerRig(t)
	r.iam.SetServiceAccountPolicy(tsProject, tsSigner, gcpfake.Binding{Role: tsMinter, Members: []string{"user:launcher@example.com", "group:ops@example.com"}})
	r.crm.SetPolicy(tsProject,
		gcpfake.Binding{Role: "roles/owner", Members: []string{"user:owner@example.com"}},
		gcpfake.Binding{Role: "roles/iam.serviceAccountTokenCreator", Members: []string{"serviceAccount:" + tsAdminSD}},
		gcpfake.Binding{Role: "roles/firebase.sdkAdminServiceAgent", Members: []string{"serviceAccount:" + tsAdminSD}})
	r.iam.SetServiceAccountPolicy(tsProject, tsAdminSD, gcpfake.Binding{Role: "roles/iam.serviceAccountTokenCreator", Members: []string{"serviceAccount:" + tsAdminSD}})
	fs, err := r.find(t)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 4 || len(unexpected(fs)) != 0 {
		t.Fatalf("findings %+v", fs)
	}
	for _, f := range fs {
		if f.Why == "" {
			t.Errorf("expected finding without a reason: %+v", f)
		}
	}
}

func TestTokenSignersProjectLevelUser(t *testing.T) {
	r := newSignerRig(t)
	r.crm.SetPolicy(tsProject,
		gcpfake.Binding{Role: "roles/owner", Members: []string{"user:owner@example.com"}},
		gcpfake.Binding{Role: "roles/iam.serviceAccountTokenCreator", Members: []string{"user:eve@example.com", "serviceAccount:" + tsAdminSD}})
	fs, err := r.find(t)
	if err != nil {
		t.Fatal(err)
	}
	u := unexpected(fs)
	if len(u) != 1 {
		t.Fatalf("findings %+v", fs)
	}
	f := u[0]
	if f.Kind != SignerSigns || f.Member != "user:eve@example.com" || f.Role != "roles/iam.serviceAccountTokenCreator" || f.Account != "" {
		t.Fatalf("finding %+v", f)
	}
}

func TestTokenSignersCustomRole(t *testing.T) {
	r := newSignerRig(t)
	policy := []gcpfake.Binding{{Role: "roles/owner", Members: []string{"user:owner@example.com"}}}
	for name, perms := range map[string][]string{
		"signer":     {"iam.serviceAccounts.signJwt", "storage.buckets.get"},
		"blob":       {"iam.serviceAccounts.signBlob"},
		"token":      {"iam.serviceAccounts.getAccessToken"},
		"delegation": {"iam.serviceAccounts.implicitDelegation"},
		"keys":       {"iam.serviceAccountKeys.create"},
		"granter":    {"iam.serviceAccounts.setIamPolicy"},
		"harmless":   {"storage.buckets.get", "iam.serviceAccounts.get", "iam.serviceAccountKeys.list"},
		"empty":      nil,
	} {
		r.iam.AddRole(tsProject, name, name, false)
		r.iam.SetRolePermissions("projects/"+tsProject+"/roles/"+name, perms...)
		policy = append(policy, gcpfake.Binding{Role: "projects/" + tsProject + "/roles/" + name, Members: []string{"group:" + name + "@example.com"}})
	}
	r.crm.SetPolicy(tsProject, policy...)
	fs, err := r.find(t)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]SignerKind{}
	for _, f := range unexpected(fs) {
		got[f.Member] = f.Kind
	}
	want := map[string]SignerKind{"group:signer@example.com": SignerSigns, "group:blob@example.com": SignerSigns, "group:token@example.com": SignerSigns,
		"group:delegation@example.com": SignerSigns, "group:keys@example.com": SignerSigns, "group:granter@example.com": SignerGrants}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for m, k := range want {
		if got[m] != k {
			t.Errorf("%s: %v, want %v", m, got[m], k)
		}
	}
}

func TestTokenSignersServiceAccountLevel(t *testing.T) {
	r := newSignerRig(t)
	r.iam.SetServiceAccountPolicy(tsProject, tsSched,
		gcpfake.Binding{Role: "roles/iam.serviceAccountTokenCreator", Members: []string{"user:eve@example.com"}},
		gcpfake.Binding{Role: "roles/iam.serviceAccountUser", Members: []string{"user:eve@example.com"}})
	fs, err := r.find(t)
	if err != nil {
		t.Fatal(err)
	}
	u := unexpected(fs)
	if len(u) != 1 || u[0].Account != tsSched || u[0].Member != "user:eve@example.com" {
		t.Fatalf("findings %+v", fs)
	}
}

// The minter role is expected on the signer only, and only while it is
// still signJwt and nothing else.
func TestTokenSignersMinterBoundary(t *testing.T) {
	m := gcpfake.Binding{Role: tsMinter, Members: []string{"user:launcher@example.com"}}
	for name, tc := range map[string]struct {
		setup      func(r *signerRig)
		unexpected int
	}{
		"on the signer":      {func(r *signerRig) { r.iam.SetServiceAccountPolicy(tsProject, tsSigner, m) }, 0},
		"on another account": {func(r *signerRig) { r.iam.SetServiceAccountPolicy(tsProject, tsSched, m) }, 1},
		"on the project": {func(r *signerRig) {
			r.crm.SetPolicy(tsProject, m)
		}, 1},
		"on the signer, widened": {func(r *signerRig) {
			r.iam.SetServiceAccountPolicy(tsProject, tsSigner, m)
			r.iam.SetRolePermissions(tsMinter, "iam.serviceAccounts.signJwt", "iam.serviceAccounts.getAccessToken")
		}, 1},
		"on the signer, emptied and refilled with keys": {func(r *signerRig) {
			r.iam.SetServiceAccountPolicy(tsProject, tsSigner, m)
			r.iam.SetRolePermissions(tsMinter, "iam.serviceAccountKeys.create")
		}, 1},
		"another project's minter on the signer": {func(r *signerRig) {
			r.iam.AddRole("other", "fugaroTokenMinter", "x", false)
			r.iam.SetRolePermissions("projects/other/roles/fugaroTokenMinter", "iam.serviceAccounts.signJwt")
			r.iam.SetServiceAccountPolicy(tsProject, tsSigner, gcpfake.Binding{Role: "projects/other/roles/fugaroTokenMinter", Members: []string{"user:launcher@example.com"}})
		}, 1},
		"a look-alike role on the signer": {func(r *signerRig) {
			r.iam.AddRole(tsProject, "fugaroTokenMinter2", "x", false)
			r.iam.SetRolePermissions("projects/fp-1/roles/fugaroTokenMinter2", "iam.serviceAccounts.signJwt")
			r.iam.SetServiceAccountPolicy(tsProject, tsSigner, gcpfake.Binding{Role: "projects/fp-1/roles/fugaroTokenMinter2", Members: []string{"user:launcher@example.com"}})
		}, 1},
	} {
		t.Run(name, func(t *testing.T) {
			r := newSignerRig(t)
			tc.setup(r)
			fs, err := r.find(t)
			if err != nil {
				t.Fatal(err)
			}
			if u := unexpected(fs); len(u) != tc.unexpected {
				t.Fatalf("unexpected %+v of %+v, want %d", u, fs, tc.unexpected)
			}
		})
	}
}

func TestTokenSignersPredefinedRoles(t *testing.T) {
	r := newSignerRig(t)
	r.crm.SetPolicy(tsProject,
		gcpfake.Binding{Role: "roles/owner", Members: []string{"user:owner@example.com"}},
		gcpfake.Binding{Role: "roles/editor", Members: []string{"user:editor@example.com"}},
		gcpfake.Binding{Role: "roles/viewer", Members: []string{"user:viewer@example.com"}},
		gcpfake.Binding{Role: "roles/iam.securityAdmin", Members: []string{"user:secadmin@example.com"}},
		gcpfake.Binding{Role: "roles/iam.serviceAccountKeyAdmin", Members: []string{"user:keys@example.com"}},
		gcpfake.Binding{Role: "roles/iam.serviceAccountAdmin", Members: []string{"user:admin@example.com"}},
		gcpfake.Binding{Role: "roles/iam.serviceAccountUser", Members: []string{"user:actas@example.com"}},
		gcpfake.Binding{Role: "roles/run.invoker", Members: []string{"allUsers"}},
		gcpfake.Binding{Role: "roles/iam.serviceAccountTokenCreator", Members: []string{"allUsers", "deleted:user:gone@example.com?uid=1", "domain:example.com"}})
	fs, err := r.find(t)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range unexpected(fs) {
		got = append(got, f.Member+" "+f.Role)
	}
	slices.Sort(got)
	// What the API resolves decides, for the primitive roles too: here the
	// fake's editor holds serviceAccountKeys.create and the owner nothing.
	want := []string{"allUsers roles/iam.serviceAccountTokenCreator", "domain:example.com roles/iam.serviceAccountTokenCreator",
		"user:admin@example.com roles/iam.serviceAccountAdmin", "user:editor@example.com roles/editor", "user:keys@example.com roles/iam.serviceAccountKeyAdmin",
		"user:secadmin@example.com roles/iam.securityAdmin"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestTokenSignersConditionalBinding(t *testing.T) {
	r := newSignerRig(t)
	r.crm.SetPolicy(tsProject, gcpfake.Binding{Role: "roles/iam.serviceAccountTokenCreator", Members: []string{"user:eve@example.com"}})
	fs, err := r.find(t)
	if err != nil {
		t.Fatal(err)
	}
	if u := unexpected(fs); len(u) != 1 || u[0].Condition != nil {
		t.Fatalf("findings %+v", fs)
	}
	r.iam.SetServiceAccountPolicy(tsProject, tsSched, gcpfake.Binding{Role: "roles/iam.serviceAccountTokenCreator", Members: []string{"user:eve@example.com"},
		Condition: &gcpfake.IAMCondition{Title: "temp", Expression: "request.time < timestamp('2030-01-01T00:00:00Z')"}})
	fs, err = r.find(t)
	if err != nil {
		t.Fatal(err)
	}
	var cond *SignerFinding
	for _, f := range unexpected(fs) {
		if f.Account == tsSched {
			cond = &f
		}
	}
	if cond == nil || cond.Condition == nil || cond.Condition.Title != "temp" {
		t.Fatalf("findings %+v", fs)
	}
}

// Every service account is read, however many pages the list takes.
func TestTokenSignersPaging(t *testing.T) {
	r := newSignerRig(t)
	r.iam.PageSize = 1
	r.iam.SetServiceAccountPolicy(tsProject, tsSigner, gcpfake.Binding{Role: "roles/iam.serviceAccountTokenCreator", Members: []string{"user:last@example.com"}})
	r.iam.SetServiceAccountPolicy(tsProject, tsSched, gcpfake.Binding{Role: "roles/iam.serviceAccountTokenCreator", Members: []string{"user:first@example.com"}})
	fs, err := r.find(t)
	if err != nil {
		t.Fatal(err)
	}
	if len(unexpected(fs)) != 2 {
		t.Fatalf("findings %+v", fs)
	}
}

// What cannot be read is said, not skipped: a role whose permissions are not
// known may be the very one that signs.
func TestTokenSignersUnreadable(t *testing.T) {
	r := newSignerRig(t)
	r.crm.SetPolicy(tsProject, gcpfake.Binding{Role: "projects/fp-1/roles/mystery", Members: []string{"user:eve@example.com"}})
	fs, err := r.find(t)
	if err != nil {
		t.Fatal(err)
	}
	u := unexpected(fs)
	if len(u) != 1 || u[0].Kind != SignerUnread || u[0].Role != "projects/fp-1/roles/mystery" || u[0].Member != "user:eve@example.com" {
		t.Fatalf("findings %+v", fs)
	}

	// The project's own policy unreadable is an error, not "no signers".
	r2 := newSignerRig(t)
	ctx := context.Background()
	c, _ := crm.NewService(ctx, option.WithEndpoint(r2.crm.URL+"/"), option.WithoutAuthentication())
	i, _ := iam.NewService(ctx, option.WithEndpoint(r2.iam.URL+"/"), option.WithoutAuthentication())
	if _, err := TokenSigners(ctx, c, i, "no-such-project", tsSigner); err == nil || !strings.Contains(err.Error(), "no-such-project") {
		t.Fatalf("err = %v", err)
	}
	// A service account whose own policy cannot be read is named.
	r3 := newSignerRig(t)
	r3.iam.AddServiceAccount(tsProject, "ghost@fp-1.iam.gserviceaccount.com", "ghost")
	r3.iam.HidePolicy("projects/" + tsProject + "/serviceAccounts/ghost@fp-1.iam.gserviceaccount.com")
	fs, err = r3.find(t)
	if err != nil {
		t.Fatal(err)
	}
	if u := unexpected(fs); len(u) != 1 || u[0].Kind != SignerUnread || u[0].Account != "ghost@fp-1.iam.gserviceaccount.com" {
		t.Fatalf("findings %+v", fs)
	}
}

// Everything it calls is a read.
func TestTokenSignersReadOnly(t *testing.T) {
	r := newSignerRig(t)
	r.iam.SetServiceAccountPolicy(tsProject, tsSigner, gcpfake.Binding{Role: tsMinter, Members: []string{"user:launcher@example.com"}})
	if _, err := r.find(t); err != nil {
		t.Fatal(err)
	}
	for _, req := range append(r.crm.Requests(), r.iam.Requests()...) {
		if req.Method != http.MethodGet && !(req.Method == http.MethodPost && strings.HasSuffix(req.Path, ":getIamPolicy")) {
			t.Errorf("non-read call %s %s", req.Method, req.Path)
		}
	}
}

// Every role is read through the API: a predefined role the fake does not
// serve is unknown, never assumed harmless, and the resolved permissions that
// made a finding are on it.
func TestTokenSignersResolvesEveryRole(t *testing.T) {
	r := newSignerRig(t)
	r.crm.SetPolicy(tsProject,
		gcpfake.Binding{Role: "roles/editor", Members: []string{"user:ed@example.com"}},
		gcpfake.Binding{Role: "roles/iam.workloadIdentityUser", Members: []string{"user:wi@example.com"}},
		gcpfake.Binding{Role: "projects/fp-1/roles/custom", Members: []string{"user:c@example.com"}})
	r.iam.AddRole(tsProject, "custom", "c", false)
	r.iam.SetRolePermissions("projects/fp-1/roles/custom", "resourcemanager.projects.setIamPolicy")
	fs, err := r.find(t)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]SignerFinding{}
	for _, f := range unexpected(fs) {
		got[f.Member] = f
	}
	if f := got["user:ed@example.com"]; f.Kind != SignerSigns || !slices.Equal(f.Perms, []string{"iam.serviceAccountKeys.create"}) {
		t.Errorf("editor: %+v", f)
	}
	if f := got["user:wi@example.com"]; f.Kind != SignerUnread {
		t.Errorf("an unserved predefined role must be unknown: %+v", f)
	}
	if f := got["user:c@example.com"]; f.Kind != SignerGrants || !slices.Equal(f.Perms, []string{"resourcemanager.projects.setIamPolicy"}) {
		t.Errorf("custom projects setIamPolicy: %+v", f)
	}
}

// The designed path is for people, groups and service accounts only; a
// binding to everyone is a risk even on the signer with the real minter role.
func TestTokenSignersMinterMemberTypes(t *testing.T) {
	for member, want := range map[string]int{
		"user:a@example.com": 0, "group:g@example.com": 0, "serviceAccount:x@fp-1.iam.gserviceaccount.com": 0,
		"allUsers": 1, "allAuthenticatedUsers": 1, "domain:example.com": 1,
	} {
		r := newSignerRig(t)
		r.iam.SetServiceAccountPolicy(tsProject, tsSigner, gcpfake.Binding{Role: tsMinter, Members: []string{member}})
		fs, err := r.find(t)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(unexpected(fs)); got != want {
			t.Errorf("%s: %d unexpected, want %d (%+v)", member, got, want, fs)
		}
	}
}

// The signer's email is compared case-insensitively and exactly: a different
// account is never the signer.
func TestTokenSignersSignerComparison(t *testing.T) {
	r := newSignerRig(t)
	r.iam.SetServiceAccountPolicy(tsProject, tsSigner, gcpfake.Binding{Role: tsMinter, Members: []string{"user:a@example.com"}})
	ctx := context.Background()
	opts := func(u string) []option.ClientOption {
		return []option.ClientOption{option.WithEndpoint(u), option.WithoutAuthentication()}
	}
	c, _ := crm.NewService(ctx, opts(r.crm.URL+"/")...)
	i, _ := iam.NewService(ctx, opts(r.iam.URL+"/")...)
	for signer, want := range map[string]int{strings.ToUpper(tsSigner): 0, tsSched: 1, "x" + tsSigner: 1, tsSigner[:len(tsSigner)-1]: 1} {
		fs, err := TokenSigners(ctx, c, i, tsProject, signer)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(unexpected(fs)); got != want {
			t.Errorf("signer %s: %d unexpected, want %d", signer, got, want)
		}
	}
}

// What the Admin SDK account is expected to do is sign; a grant right is not
// expected of it, and the account's name must be the real shape.
func TestTokenSignersAdminSDKShape(t *testing.T) {
	for acct, tc := range map[string]struct {
		role string
		want int
	}{
		"firebase-adminsdk-fbsvc@fp-1.iam.gserviceaccount.com":      {"roles/iam.serviceAccountTokenCreator", 0},
		"firebase-adminsdk-fbsvc@fp-1.iam.gserviceaccount.com ":     {"roles/iam.serviceAccountTokenCreator", 1},
		"firebase-adminsdk-fbsvc@fp-1.iam.gserviceaccount.com\n":    {"roles/iam.serviceAccountTokenCreator", 1},
		"firebase-adminsdk-abcdefg@fp-1.iam.gserviceaccount.com":    {"roles/iam.serviceAccountTokenCreator", 1},
		"firebase-adminsdk-abc@fp-1.iam.gserviceaccount.com":        {"roles/iam.serviceAccountTokenCreator", 1},
		"firebase-adminsdk-evil-extra@fp-1.iam.gserviceaccount.com": {"roles/iam.serviceAccountTokenCreator", 1},
		"firebase-adminsdk-fbsvc@other.iam.gserviceaccount.com":     {"roles/iam.serviceAccountTokenCreator", 1},
		"firebase-adminsdk-fbsvc@fp-1.iam.gserviceaccount.com\t":    {"roles/iam.serviceAccountAdmin", 1},
	} {
		r := newSignerRig(t)
		r.crm.SetPolicy(tsProject, gcpfake.Binding{Role: tc.role, Members: []string{"serviceAccount:" + acct}})
		fs, err := r.find(t)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(unexpected(fs)); got != tc.want {
			t.Errorf("%q %s: %d unexpected, want %d", acct, tc.role, got, tc.want)
		}
	}
	// the grant right is never expected, even for the real account
	r := newSignerRig(t)
	r.crm.SetPolicy(tsProject, gcpfake.Binding{Role: "roles/iam.serviceAccountAdmin", Members: []string{"serviceAccount:" + tsAdminSD}})
	fs, _ := r.find(t)
	if len(unexpected(fs)) != 1 {
		t.Fatalf("%+v", fs)
	}
}
