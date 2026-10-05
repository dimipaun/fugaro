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
	return r
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
	want := []string{"allUsers roles/iam.serviceAccountTokenCreator", "domain:example.com roles/iam.serviceAccountTokenCreator",
		"user:admin@example.com roles/iam.serviceAccountAdmin", "user:keys@example.com roles/iam.serviceAccountKeyAdmin"}
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
