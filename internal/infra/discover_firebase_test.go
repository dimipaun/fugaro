package infra

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra/tf"
)

const (
	fbTestProject = "fp-1234"
	fbTestNumber  = 555000111222
)

var (
	fbSigner     = "fugaro-token-signer@" + fbTestProject + ".iam.gserviceaccount.com"
	fbMinterRole = "projects/" + fbTestProject + "/roles/fugaroTokenMinter"
	fbWebTargets = []string{"identitytoolkit.googleapis.com", "securetoken.googleapis.com"}
)

// fbCloud is the discovery fakes plus the Firebase project's: its
// Realtime Database management API and its API keys.
type fbCloud struct {
	*cloud
	fbdb *gcpfake.FirebaseDB
	keys *gcpfake.APIKeys
}

func newFBCloud(t *testing.T) *fbCloud {
	t.Helper()
	f := &fbCloud{cloud: newCloud(t), fbdb: gcpfake.NewFirebaseDB(t), keys: gcpfake.NewAPIKeys(t)}
	e := f.endpoints()
	e.FirebaseDatabase = f.fbdb.URL + "/"
	e.APIKeys = f.keys.URL + "/"
	c, err := NewClients(context.Background(), f.options(nil), e)
	if err != nil {
		t.Fatal(err)
	}
	f.c = c
	f.crm.AddProject(fbTestProject, fbTestNumber)
	// SERVICE_DISABLED answers name the Firebase project, by number.
	f.su.Consumer = "projects/555000111222"
	return f
}

func fbSpec() FirebaseSpec {
	return FirebaseSpec{
		Project:       fbTestProject,
		FugaroProject: "aurora",
		Names:         FirebaseNames{SignerAccountID: SignerAccountID, MinterRoleID: MinterRoleID, APIKey: APIKeyID},
		Launchers:     []string{"user:a@x.com"},
		Operators:     []string{"group:ops@x.com"},
	}
}

const fbSignerDescription = "Signs the custom tokens of the Fugaro project aurora's runs. Holds no roles."

func (f *fbCloud) addInstance(url string) {
	f.fbdb.AddInstanceFull(fbTestProject, "us-central1", fbTestProject+"-default-rtdb", "DEFAULT_DATABASE", "ACTIVE", url)
}

func (f *fbCloud) addKey() {
	f.keys.AddKey(fbTestProject, "fugaro-web", "Fugaro run sign-in", fbWebTargets, false, false)
}

// addSigner makes the signer as the module does, with the minter granted
// to the spec's launchers and operators, as an earlier apply leaves it.
func (f *fbCloud) addSigner() {
	f.iam.AddServiceAccountFull(fbTestProject, fbSigner, "Fugaro token signer", fbSignerDescription, false)
	f.iam.SetServiceAccountPolicy(fbTestProject, fbSigner, gcpfake.Binding{Role: fbMinterRole, Members: []string{"group:ops@x.com", "user:a@x.com"}})
}

func (f *fbCloud) addRole() {
	f.iam.AddRole(fbTestProject, "fugaroTokenMinter", "Fugaro token minter", false)
	f.iam.SetRolePermissions(fbMinterRole, "iam.serviceAccounts.signJwt")
}

func (f *fbCloud) addAll(url string) {
	f.addInstance(url)
	f.addKey()
	f.addSigner()
	f.addRole()
}

var fbAllFour = []Import{
	{To: "module.firebase.google_apikeys_key.web", ID: "projects/fp-1234/locations/global/keys/fugaro-web"},
	{To: "module.firebase.google_firebase_database_instance.this", ID: "projects/fp-1234/locations/us-central1/instances/fp-1234-default-rtdb"},
	{To: "module.firebase.google_project_iam_custom_role.token_minter", ID: "projects/fp-1234/roles/fugaroTokenMinter"},
	{To: "module.firebase.google_service_account.signer", ID: "projects/fp-1234/serviceAccounts/fugaro-token-signer@fp-1234.iam.gserviceaccount.com"},
}

func sortedImports(l []Import) []Import {
	out := slices.Clone(l)
	slices.SortFunc(out, func(a, b Import) int { return strings.Compare(a.To, b.To) })
	return out
}

func TestDiscoverFirebase(t *testing.T) {
	ctx := context.Background()
	t.Run("clean project imports nothing", func(t *testing.T) {
		f := newFBCloud(t)
		im, err := DiscoverFirebase(ctx, f.c, fbSpec())
		if err != nil || len(im.List) != 0 || len(im.Notes) != 0 {
			t.Fatalf("imports %v, notes %v, err %v", im.List, im.Notes, err)
		}
	})
	t.Run("the four exist and are ours", func(t *testing.T) {
		f := newFBCloud(t)
		f.addAll("https://fp-1234-default-rtdb.firebaseio.com")
		im, err := DiscoverFirebase(ctx, f.c, fbSpec())
		if err != nil {
			t.Fatal(err)
		}
		if got := sortedImports(im.List); !slices.Equal(got, fbAllFour) {
			t.Fatalf("imports\n got %v\nwant %v", got, fbAllFour)
		}
		wantNote := "the token signer " + fbSigner + " was adopted. Its own IAM policy was checked: it grants nothing but the minter role, to this installation's launchers and operators. " +
			"Roles on project fp-1234 that can sign as it are not visible here: fugaro doctor's token-signers check lists the project-level ones; folder- and organization-inherited bindings are not checked"
		if len(im.Notes) != 1 || im.Notes[0] != wantNote {
			t.Errorf("notes %v; want the adopted signer's note", im.Notes)
		}
	})
	t.Run("empty unmarked default instance is adopted", func(t *testing.T) {
		f := newFBCloud(t)
		db, rt := newDB(t, "")
		f.addInstance(rt.URL)
		if marked, err := db.Check(ctx); err != nil || marked {
			t.Fatalf("DB.Check: marked %v, err %v; want an empty, unmarked database that passes", marked, err)
		}
		im, err := DiscoverFirebase(ctx, f.c, fbSpec())
		if err != nil {
			t.Fatal(err)
		}
		want := []Import{fbAllFour[1]}
		if !slices.Equal(im.List, want) {
			t.Fatalf("imports %v, want %v", im.List, want)
		}
	})
	t.Run("api disabled reads as absent", func(t *testing.T) {
		for _, tc := range []struct {
			service string
			server  func(*fbCloud) *gcpfake.Server
			gone    []string // the addresses the disabled API hides
		}{
			{"firebasedatabase.googleapis.com", func(f *fbCloud) *gcpfake.Server { return f.fbdb.Server }, []string{fbAllFour[1].To}},
			{"apikeys.googleapis.com", func(f *fbCloud) *gcpfake.Server { return f.keys.Server }, []string{fbAllFour[0].To}},
			{"iam.googleapis.com", func(f *fbCloud) *gcpfake.Server { return f.iam.Server }, []string{fbAllFour[2].To, fbAllFour[3].To}},
		} {
			t.Run(tc.service, func(t *testing.T) {
				f := newFBCloud(t)
				f.addAll("https://fp-1234-default-rtdb.firebaseio.com")
				f.su.Disable(tc.service, tc.server(f))
				im, err := DiscoverFirebase(ctx, f.c, fbSpec())
				if err != nil {
					t.Fatal(err)
				}
				var want []Import
				for _, i := range fbAllFour {
					if !slices.Contains(tc.gone, i.To) {
						want = append(want, i)
					}
				}
				if got := sortedImports(im.List); !slices.Equal(got, want) {
					t.Fatalf("imports\n got %v\nwant %v", got, want)
				}
			})
		}
	})
}

// A 403 that is not SERVICE_DISABLED is a missing permission: an error
// naming the read, never "absent" (which would plan a create) nor a
// refusal.
func TestDiscoverFirebaseAccessDenied(t *testing.T) {
	for _, tc := range []struct {
		name   string
		server func(*fbCloud) *gcpfake.Server
		want   string
	}{
		{"database", func(f *fbCloud) *gcpfake.Server { return f.fbdb.Server }, "Realtime Database"},
		{"key", func(f *fbCloud) *gcpfake.Server { return f.keys.Server }, "API key"},
		{"iam", func(f *fbCloud) *gcpfake.Server { return f.iam.Server }, "fp-1234"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFBCloud(t)
			f.addAll("https://fp-1234-default-rtdb.firebaseio.com")
			tc.server(f).Refuse(http.StatusForbidden, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "Permission denied")
			im, err := DiscoverFirebase(context.Background(), f.c, fbSpec())
			var ue *UserError
			if err == nil || errors.As(err, &ue) || len(im.List) != 0 {
				t.Fatalf("imports %v, err %v; want a remote error", im.List, err)
			}
			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "403") {
				t.Errorf("err = %v; want it to name %q and the 403", err, tc.want)
			}
		})
	}
	// One read denied while the others pass: each is its own access
	// error, not hidden behind another.
	for _, tc := range []struct{ path, want string }{
		{"/roles/fugaroTokenMinter", "custom role " + fbMinterRole},
		{"/keys", "listing the keys of service account " + fbSigner},
	} {
		f := newFBCloud(t)
		f.addAll("https://fp-1234-default-rtdb.firebaseio.com")
		f.iam.RefusePath(tc.path, http.StatusForbidden, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "Permission denied")
		im, err := DiscoverFirebase(context.Background(), f.c, fbSpec())
		var ue *UserError
		if err == nil || errors.As(err, &ue) || len(im.List) != 0 || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "403") {
			t.Errorf("%s denied: imports %v, err %v; want an access error naming %q", tc.path, im.List, err, tc.want)
		}
	}
	// The signer's policy alone hidden: the account is readable, its
	// grants are not, which must not pass as "no grants".
	f := newFBCloud(t)
	f.addAll("https://fp-1234-default-rtdb.firebaseio.com")
	f.iam.HidePolicy("projects/" + fbTestProject + "/serviceAccounts/" + fbSigner)
	im, err := DiscoverFirebase(context.Background(), f.c, fbSpec())
	var ue *UserError
	if err == nil || errors.As(err, &ue) || len(im.List) != 0 || !strings.Contains(err.Error(), "IAM policy") {
		t.Fatalf("hidden policy: imports %v, err %v; want a remote error naming the policy", im.List, err)
	}
}

// Discovery reads, and never reads key material: no keyString call on the
// API key, no get of a signer key, and every call a GET (or the signer's
// getIamPolicy, a POST that reads).
func TestDiscoverFirebaseOnlyReads(t *testing.T) {
	f := newFBCloud(t)
	f.addAll("https://fp-1234-default-rtdb.firebaseio.com")
	if _, err := DiscoverFirebase(context.Background(), f.c, fbSpec()); err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]*gcpfake.Server{"rtdb": f.fbdb.Server, "apikeys": f.keys.Server, "iam": f.iam.Server} {
		for _, r := range s.Requests() {
			if strings.Contains(r.Path, "keyString") || strings.Contains(r.Path, "/keys/") && strings.Contains(r.Path, "serviceAccounts") {
				t.Errorf("%s: read key material: %s %s", name, r.Method, r.Path)
			}
			if r.Method != http.MethodGet && !strings.HasSuffix(r.Path, ":getIamPolicy") {
				t.Errorf("%s: %s %s is not a read", name, r.Method, r.Path)
			}
		}
	}
	if len(f.keys.Requests()) != 1 {
		t.Errorf("api keys calls: %v", f.keys.Requests())
	}
	// The signer's keys are listed by type only.
	listed := false
	for _, r := range f.iam.Requests() {
		if strings.HasSuffix(r.Path, "/keys") {
			listed = true
			if !strings.Contains(r.Query, "keyTypes=USER_MANAGED") {
				t.Errorf("keys listed without the USER_MANAGED filter: %s?%s", r.Path, r.Query)
			}
		}
	}
	if !listed {
		t.Error("the signer's user-managed keys were not listed")
	}
}

func TestDiscoverFirebaseRefuses(t *testing.T) {
	const url = "https://fp-1234-default-rtdb.firebaseio.com"
	cases := []struct {
		name    string
		arrange func(*fbCloud)
		want    []string
	}{
		{"role with an extra permission", func(f *fbCloud) {
			f.iam.SetRolePermissions(fbMinterRole, "iam.serviceAccounts.signJwt", "iam.serviceAccounts.getAccessToken")
		}, []string{"fugaroTokenMinter", "iam.serviceAccounts.getAccessToken", "gcloud iam roles update fugaroTokenMinter --project fp-1234 --permissions=iam.serviceAccounts.signJwt"}},
		{"role with no permission", func(f *fbCloud) {
			f.iam.SetRolePermissions(fbMinterRole)
		}, []string{"fugaroTokenMinter", "not exactly"}},
		{"role with the permission twice and another", func(f *fbCloud) {
			f.iam.SetRolePermissions(fbMinterRole, "iam.serviceAccounts.signJwt", "iam.serviceAccounts.signJwt", "iam.serviceAccounts.signBlob")
		}, []string{"iam.serviceAccounts.signBlob"}},
		{"role with another title", func(f *fbCloud) {
			f.iam.AddRole(fbTestProject, "fugaroTokenMinter", "Token minter", false)
			f.iam.SetRolePermissions(fbMinterRole, "iam.serviceAccounts.signJwt")
		}, []string{"title", `"Token minter"`, `"Fugaro token minter"`}},
		{"deleted role with another title", func(f *fbCloud) {
			f.iam.AddRole(fbTestProject, "fugaroTokenMinter", "Token minter", true)
		}, []string{"title", `"Token minter"`}},
		{"signer with another description", func(f *fbCloud) {
			f.iam.AddServiceAccountFull(fbTestProject, fbSigner, "Fugaro token signer", "Signs the custom tokens of the Fugaro project other's runs. Holds no roles.", false)
		}, []string{"description", "project other's runs", "project aurora's runs"}},
		{"signer with no description", func(f *fbCloud) {
			f.iam.AddServiceAccountFull(fbTestProject, fbSigner, "Fugaro token signer", "", false)
		}, []string{"description"}},
		{"signer with another display name", func(f *fbCloud) {
			f.iam.AddServiceAccountFull(fbTestProject, fbSigner, "Token signer", fbSignerDescription, false)
		}, []string{"display name", `"Token signer"`}},
		{"signer with a user-managed key", func(f *fbCloud) {
			f.iam.AddUserKey(fbTestProject, fbSigner)
		}, []string{"user-managed key", "k1", "gcloud iam service-accounts keys delete k1 --iam-account " + fbSigner + " --project fp-1234"}},
		{"signer disabled", func(f *fbCloud) {
			f.iam.AddServiceAccountFull(fbTestProject, fbSigner, "Fugaro token signer", fbSignerDescription, true)
		}, []string{"disabled", "gcloud iam service-accounts enable " + fbSigner + " --project fp-1234"}},
		{"signer grants another role", func(f *fbCloud) {
			f.iam.SetServiceAccountPolicy(fbTestProject, fbSigner,
				gcpfake.Binding{Role: fbMinterRole, Members: []string{"user:a@x.com"}},
				gcpfake.Binding{Role: "roles/owner", Members: []string{"user:a@x.com"}})
		}, []string{"roles/owner", "remove-iam-policy-binding " + fbSigner + " --project fp-1234 --member=user:a@x.com --role=roles/owner"}},
		{"signer grants token creator to all users", func(f *fbCloud) {
			f.iam.SetServiceAccountPolicy(fbTestProject, fbSigner,
				gcpfake.Binding{Role: "roles/iam.serviceAccountTokenCreator", Members: []string{"allUsers", "allAuthenticatedUsers"}})
		}, []string{"roles/iam.serviceAccountTokenCreator", "--member=allUsers ", "--member=allAuthenticatedUsers "}},
		{"signer grants another project's minter role", func(f *fbCloud) {
			f.iam.SetServiceAccountPolicy(fbTestProject, fbSigner,
				gcpfake.Binding{Role: "projects/other/roles/fugaroTokenMinter", Members: []string{"user:a@x.com"}})
		}, []string{"--role=projects/other/roles/fugaroTokenMinter"}},
		{"signer grants the minter role to a stranger", func(f *fbCloud) {
			f.iam.SetServiceAccountPolicy(fbTestProject, fbSigner,
				gcpfake.Binding{Role: fbMinterRole, Members: []string{"user:a@x.com", "user:old@example.com"}})
		}, []string{"user:old@example.com", "grants fugaroTokenMinter to members who are not this installation's launchers or operators"}},
		{"signer grants the minter role to a stranger under a condition", func(f *fbCloud) {
			f.iam.SetServiceAccountPolicy(fbTestProject, fbSigner,
				gcpfake.Binding{Role: fbMinterRole, Members: []string{"user:old@example.com"}, Condition: &gcpfake.IAMCondition{Title: "t", Expression: "true"}})
		}, []string{"user:old@example.com", "--role=" + fbMinterRole + " --all"}},
		{"key with a third api target", func(f *fbCloud) {
			f.keys.AddKey(fbTestProject, "fugaro-web", "Fugaro run sign-in", append(slices.Clone(fbWebTargets), "storage.googleapis.com"), false, false)
		}, []string{"restrictions", "storage.googleapis.com"}},
		{"key with one target twice", func(f *fbCloud) {
			f.keys.AddKey(fbTestProject, "fugaro-web", "Fugaro run sign-in", []string{"identitytoolkit.googleapis.com", "identitytoolkit.googleapis.com"}, false, false)
		}, []string{"restrictions"}},
		{"key with no restriction", func(f *fbCloud) {
			f.keys.AddKey(fbTestProject, "fugaro-web", "Fugaro run sign-in", nil, false, false)
		}, []string{"restrictions", "none"}},
		{"key with a browser restriction", func(f *fbCloud) {
			f.keys.AddKey(fbTestProject, "fugaro-web", "Fugaro run sign-in", fbWebTargets, true, false)
		}, []string{"restrictions", "browser"}},
		{"key with methods on a target", func(f *fbCloud) {
			f.keys.SetTargetMethods(fbTestProject, "fugaro-web", "securetoken.googleapis.com", "GrantToken")
		}, []string{"restrictions", "methods"}},
		{"key bound to a service account", func(f *fbCloud) {
			f.keys.BindServiceAccount(fbTestProject, "fugaro-web", "x@fp-1234.iam.gserviceaccount.com")
		}, []string{"bound to service account x@fp-1234.iam.gserviceaccount.com"}},
		{"key deleted", func(f *fbCloud) {
			f.keys.AddKey(fbTestProject, "fugaro-web", "Fugaro run sign-in", fbWebTargets, false, true)
		}, []string{"undelete", "gcloud services api-keys undelete fugaro-web --project fp-1234"}},
		{"key with another display name", func(f *fbCloud) {
			f.keys.AddKey(fbTestProject, "fugaro-web", "Browser key", fbWebTargets, false, false)
		}, []string{"display name", `"Browser key"`}},
		{"default instance in another region", func(f *fbCloud) {
			f.fbdb.AddInstanceFull(fbTestProject, "europe-west1", fbTestProject+"-default-rtdb", "DEFAULT_DATABASE", "ACTIVE", url)
		}, []string{"us-central1", "europe-west1"}},
		{"default instance under another id", func(f *fbCloud) {
			f.fbdb.AddInstanceFull(fbTestProject, "us-central1", "fp-1234-other", "DEFAULT_DATABASE", "ACTIVE", url)
		}, []string{"default-rtdb", "fp-1234-other"}},
		{"our id as a user database", func(f *fbCloud) {
			f.fbdb.AddInstanceFull(fbTestProject, "us-central1", fbTestProject+"-default-rtdb", "USER_DATABASE", "ACTIVE", url)
		}, []string{"USER_DATABASE", "DEFAULT_DATABASE"}},
		{"default instance not ACTIVE", func(f *fbCloud) {
			f.fbdb.AddInstanceFull(fbTestProject, "us-central1", fbTestProject+"-default-rtdb", "DEFAULT_DATABASE", "DISABLED", url)
		}, []string{"ACTIVE", "DISABLED", "re-enable"}},
		{"default instance without a URL", func(f *fbCloud) {
			f.fbdb.AddInstanceFull(fbTestProject, "us-central1", fbTestProject+"-default-rtdb", "DEFAULT_DATABASE", "ACTIVE", "")
		}, []string{"no database URL"}},
		{"default instance deleted", func(f *fbCloud) {
			f.fbdb.AddInstanceFull(fbTestProject, "us-central1", fbTestProject+"-default-rtdb", "DEFAULT_DATABASE", "DELETED", url)
		}, []string{"ACTIVE", "DELETED", "wait"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFBCloud(t)
			f.addKey()
			f.addSigner()
			f.addRole()
			tc.arrange(f)
			im, err := DiscoverFirebase(context.Background(), f.c, fbSpec())
			var ue *UserError
			if !errors.As(err, &ue) {
				t.Fatalf("err = %v (imports %v); want a refusal", err, im.List)
			}
			if len(im.List) != 0 {
				t.Errorf("a refusal came with imports %v", im.List)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("refusal lacks %q:\n%v", w, err)
				}
			}
		})
	}

	t.Run("two bad resources are refused together", func(t *testing.T) {
		f := newFBCloud(t)
		f.addAll(url)
		f.iam.SetRolePermissions(fbMinterRole, "iam.serviceAccounts.signJwt", "iam.serviceAccounts.getAccessToken")
		f.keys.AddKey(fbTestProject, "fugaro-web", "Fugaro run sign-in", append(slices.Clone(fbWebTargets), "storage.googleapis.com"), false, false)
		_, err := DiscoverFirebase(context.Background(), f.c, fbSpec())
		var ue *UserError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), "iam.serviceAccounts.getAccessToken") || !strings.Contains(err.Error(), "storage.googleapis.com") {
			t.Fatalf("err = %v; want both refusals in one error", err)
		}
	})
}

// A deleted role (with our title) is not imported; a note says the plan's
// create restores it, as for the installation's roles.
func TestDiscoverFirebaseDeletedRole(t *testing.T) {
	f := newFBCloud(t)
	f.addAll("https://fp-1234-default-rtdb.firebaseio.com")
	f.iam.AddRole(fbTestProject, "fugaroTokenMinter", "Fugaro token minter", true)
	f.iam.SetRolePermissions(fbMinterRole, "iam.serviceAccounts.signJwt")
	im, err := DiscoverFirebase(context.Background(), f.c, fbSpec())
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range im.List {
		if i.To == fbAllFour[2].To {
			t.Fatalf("a deleted role is imported: %v", im.List)
		}
	}
	if len(im.List) != 3 {
		t.Errorf("imports %v; want the other three", im.List)
	}
	if !slices.ContainsFunc(im.Notes, func(n string) bool {
		return n == "custom role "+fbMinterRole+" is deleted; the plan's create restores it"
	}) || len(im.Notes) != 2 {
		t.Errorf("notes = %v", im.Notes)
	}
}

func TestDiscoverFirebaseMinterMembersAreCompared(t *testing.T) {
	ctx := context.Background()
	t.Run("exactly the launchers and operators", func(t *testing.T) {
		f := newFBCloud(t)
		f.addAll("https://fp-1234-default-rtdb.firebaseio.com")
		im, err := DiscoverFirebase(ctx, f.c, fbSpec())
		if err != nil || len(im.List) != 4 {
			t.Fatalf("imports %v, err %v", im.List, err)
		}
	})
	t.Run("some of them, or none", func(t *testing.T) {
		for _, bs := range [][]gcpfake.Binding{
			{{Role: fbMinterRole, Members: []string{"group:ops@x.com"}}},
			nil,
			// Under a condition: narrower than the module's grant, still ours.
			{{Role: fbMinterRole, Members: []string{"user:a@x.com"}, Condition: &gcpfake.IAMCondition{Title: "t", Expression: "true"}}},
		} {
			f := newFBCloud(t)
			f.addAll("https://fp-1234-default-rtdb.firebaseio.com")
			f.iam.SetServiceAccountPolicy(fbTestProject, fbSigner, bs...)
			im, err := DiscoverFirebase(ctx, f.c, fbSpec())
			if err != nil || len(im.List) != 4 {
				t.Fatalf("bindings %v: imports %v, err %v", bs, im.List, err)
			}
		}
	})
	for _, stranger := range []string{"serviceAccount:old@p.iam.gserviceaccount.com", "allUsers", "allAuthenticatedUsers", "User:a@x.com", "user:Old@example.com", "group:ops@x.com.evil", "domain:x.com", "principal://iam.googleapis.com/x/user:a@x.com", "deleted:user:a@x.com?uid=1"} {
		t.Run(stranger, func(t *testing.T) {
			f := newFBCloud(t)
			f.addAll("https://fp-1234-default-rtdb.firebaseio.com")
			f.iam.SetServiceAccountPolicy(fbTestProject, fbSigner,
				gcpfake.Binding{Role: fbMinterRole, Members: []string{"user:a@x.com", "group:ops@x.com", stranger}})
			_, err := DiscoverFirebase(ctx, f.c, fbSpec())
			var ue *UserError
			if !errors.As(err, &ue) {
				t.Fatalf("err = %v; want a refusal", err)
			}
			want := foreignMinterMessage(fbSigner, fbTestProject, []string{stranger})
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("refusal\n%v\nlacks exactly\n%s", err, want)
			}
			for _, ours := range []string{"--member=user:a@x.com", "--member=group:ops@x.com "} {
				if strings.Contains(err.Error(), ours) {
					t.Errorf("refusal names a launcher or operator (%s):\n%v", ours, err)
				}
			}
		})
	}
}

// The foreign-minter refusal is design §3.3's text, byte for byte.
func TestForeignMinterMessage(t *testing.T) {
	got := foreignMinterMessage("fugaro-token-signer@fp-1234.iam.gserviceaccount.com", "fp-1234", []string{"user:old@example.com"})
	want := `token signer fugaro-token-signer@fp-1234.iam.gserviceaccount.com grants fugaroTokenMinter to members who are not this installation's launchers or operators:
  user:old@example.com
Each could mint run tokens. Remove them, then rerun fugaro init --firebase fp-1234:
  gcloud iam service-accounts remove-iam-policy-binding fugaro-token-signer@fp-1234.iam.gserviceaccount.com --project fp-1234 --member=user:old@example.com --role=projects/fp-1234/roles/fugaroTokenMinter
Or add them as launchers or operators if they should keep it.`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}

	// Several members: one line each, in the order given.
	got = foreignMinterMessage("s@fp-1234.iam.gserviceaccount.com", "fp-1234", []string{"allUsers", "user:old@example.com"})
	want = `token signer s@fp-1234.iam.gserviceaccount.com grants fugaroTokenMinter to members who are not this installation's launchers or operators:
  allUsers
  user:old@example.com
Each could mint run tokens. Remove them, then rerun fugaro init --firebase fp-1234:
  gcloud iam service-accounts remove-iam-policy-binding s@fp-1234.iam.gserviceaccount.com --project fp-1234 --member=allUsers --role=projects/fp-1234/roles/fugaroTokenMinter
  gcloud iam service-accounts remove-iam-policy-binding s@fp-1234.iam.gserviceaccount.com --project fp-1234 --member=user:old@example.com --role=projects/fp-1234/roles/fugaroTokenMinter
Or add them as launchers or operators if they should keep it.`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}

	// A grant of another role on the signer: refused the same way, with
	// the removal command for that role.
	got = signerGrantMessage("fugaro-token-signer@fp-1234.iam.gserviceaccount.com", "fp-1234", "roles/owner", false, []signerGrant{{member: "user:a@x.com"}, {member: "user:b@x.com", conditional: true}})
	want = `token signer fugaro-token-signer@fp-1234.iam.gserviceaccount.com grants roles/owner, which Fugaro never grants on it, to:
  user:a@x.com
  user:b@x.com (under a condition)
Each could act as the signer and mint run tokens. Remove them, then rerun fugaro init --firebase fp-1234:
  gcloud iam service-accounts remove-iam-policy-binding fugaro-token-signer@fp-1234.iam.gserviceaccount.com --project fp-1234 --member=user:a@x.com --role=roles/owner
  gcloud iam service-accounts remove-iam-policy-binding fugaro-token-signer@fp-1234.iam.gserviceaccount.com --project fp-1234 --member=user:b@x.com --role=roles/owner --all`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}

	// A member a shell would expand is quoted, in the list and in the
	// command.
	got = foreignMinterMessage("s@p.iam.gserviceaccount.com", "p", []string{"deleted:user:x@y.com?uid=1"})
	if !strings.Contains(got, "--member='deleted:user:x@y.com?uid=1' ") || !strings.Contains(got, "\n  'deleted:user:x@y.com?uid=1'\n") {
		t.Errorf("unquoted: %s", got)
	}

	// A member with a line break cannot print a line of its own: one that
	// would read as a command to copy.
	got = foreignMinterMessage("s@p.iam.gserviceaccount.com", "p", []string{"user:b@x.com\n  gcloud evil"})
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "gcloud evil") {
			t.Fatalf("a member printed its own line %q:\n%s", l, got)
		}
	}
	if lines := strings.Count(got, "\n"); lines != 4 {
		t.Errorf("%d line breaks, want 4 (five lines):\n%q", lines, got)
	}
	if !strings.Contains(got, "  $'user:b@x.com\\n  gcloud evil'\n") {
		t.Errorf("the member is not quoted with its escape:\n%s", got)
	}

	// Nothing that does not print prints raw: line and paragraph
	// separators, bidi overrides, a zero-width space.
	for r, esc := range map[rune]string{0x2028: `\u2028`, 0x2029: `\u2029`, 0x202e: `\u202e`, 0x200b: `\u200b`, 0x7f: `\x7f`, 0xa0: `\u00a0`, 0xe0001: `\U000e0001`} {
		m := "user:b@x.com" + string(r) + "x"
		got := foreignMinterMessage("s@p.iam.gserviceaccount.com", "p", []string{m})
		if strings.ContainsRune(got, r) || !strings.Contains(got, "$'user:b@x.com"+esc+"x'") {
			t.Errorf("U+%04X: %q", r, got)
		}
	}
	// A printable non-ASCII member is single-quoted, as is.
	if got := shellWord("user:jürgen@x.com"); got != "'user:jürgen@x.com'" {
		t.Errorf("shellWord = %q", got)
	}
}

// The four import kinds' rows, with newImport's placeholders.
func TestFirebaseImportRows(t *testing.T) {
	got := []Import{
		newImport(importAPIKey, "fp-1234", "", "", "fugaro-web"),
		newImport(importFirebaseDB, "fp-1234", "us-central1", "", "fp-1234-default-rtdb"),
		newImport(importMinterRole, "fp-1234", "", "", "fugaroTokenMinter"),
		newImport(importSignerSA, "fp-1234", "", "", fbSigner),
	}
	if !slices.Equal(got, fbAllFour) {
		t.Fatalf("got %v\nwant %v", got, fbAllFour)
	}
}

// Service accounts as launchers or operators are compared like people:
// one on the list may hold the minter role, one off it is foreign.
func TestDiscoverFirebaseServiceAccountMembers(t *testing.T) {
	ctx := context.Background()
	const sa = "serviceAccount:x@p.iam.gserviceaccount.com"
	for _, tc := range []struct {
		name string
		spec func(*FirebaseSpec)
	}{
		{"launcher", func(s *FirebaseSpec) { s.Launchers = append(s.Launchers, sa) }},
		{"operator", func(s *FirebaseSpec) { s.Operators = append(s.Operators, sa) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFBCloud(t)
			f.addAll("https://fp-1234-default-rtdb.firebaseio.com")
			f.iam.SetServiceAccountPolicy(fbTestProject, fbSigner, gcpfake.Binding{Role: fbMinterRole, Members: []string{"user:a@x.com", "group:ops@x.com", sa}})
			spec := fbSpec()
			tc.spec(&spec)
			im, err := DiscoverFirebase(ctx, f.c, spec)
			if err != nil || len(im.List) != 4 {
				t.Fatalf("imports %v, err %v", im.List, err)
			}
			// The same policy against a spec without it.
			_, err = DiscoverFirebase(ctx, f.c, fbSpec())
			var ue *UserError
			if !errors.As(err, &ue) || !strings.Contains(err.Error(), foreignMinterMessage(fbSigner, fbTestProject, []string{sa})) {
				t.Fatalf("without it: err = %v; want it refused as foreign", err)
			}
		})
	}
}

// IAM keeps members' emails lower-cased: a launcher written with capitals
// matches its grant, but the member's type must match exactly.
func TestDiscoverFirebaseMemberEmailCase(t *testing.T) {
	ctx := context.Background()
	spec := fbSpec()
	spec.Launchers = []string{"user:A@X.com", "serviceAccount:Bot@p.iam.gserviceaccount.com"}
	spec.Operators = []string{"group:Ops@x.com"}

	f := newFBCloud(t)
	f.addAll("https://fp-1234-default-rtdb.firebaseio.com")
	f.iam.SetServiceAccountPolicy(fbTestProject, fbSigner, gcpfake.Binding{Role: fbMinterRole,
		Members: []string{"user:a@x.com", "serviceAccount:bot@p.iam.gserviceaccount.com", "group:ops@x.com"}})
	if im, err := DiscoverFirebase(ctx, f.c, spec); err != nil || len(im.List) != 4 {
		t.Fatalf("lower-cased grants: imports %v, err %v", im.List, err)
	}

	// Only ASCII letters fold: Google lower-cases ASCII email only, and
	// Unicode folding would turn the Kelvin sign into k and İ into i.
	spec.Launchers = append(spec.Launchers, "user:kim@x.com", "user:i@x.com", "user:jürgen@x.com", "user:Ömer@x.com")
	f = newFBCloud(t)
	f.addAll("https://fp-1234-default-rtdb.firebaseio.com")
	f.iam.SetServiceAccountPolicy(fbTestProject, fbSigner, gcpfake.Binding{Role: fbMinterRole, Members: []string{"user:jürgen@x.com", "user:Ömer@x.com"}})
	if im, err := DiscoverFirebase(ctx, f.c, spec); err != nil || len(im.List) != 4 {
		t.Fatalf("byte-identical non-ASCII members: imports %v, err %v", im.List, err)
	}

	for _, foreign := range []string{"User:a@x.com", "user:b@x.com", "user:Old@example.com", "domain:x.com", "group:a@x.com", "serviceAccount:a@x.com",
		"user:\u212aim@x.com", "user:\u0130@x.com", "user:ömer@x.com", "user:JÜRGEN@x.com"} {
		f := newFBCloud(t)
		f.addAll("https://fp-1234-default-rtdb.firebaseio.com")
		f.iam.SetServiceAccountPolicy(fbTestProject, fbSigner, gcpfake.Binding{Role: fbMinterRole, Members: []string{"user:a@x.com", foreign}})
		_, err := DiscoverFirebase(ctx, f.c, spec)
		var ue *UserError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), foreignMinterMessage(fbSigner, fbTestProject, []string{foreign})) {
			t.Errorf("%s: err = %v; want it refused as foreign", foreign, err)
		}
	}
}

// Only the Firebase project's own disabled API reads as absent: a
// SERVICE_DISABLED that names another project (the quota project's, say)
// is an error.
func TestDiscoverFirebaseDisabledConsumer(t *testing.T) {
	ctx := context.Background()
	for _, consumer := range []string{"projects/555000111222", "projects/fp-1234"} {
		f := newFBCloud(t)
		f.addAll("https://fp-1234-default-rtdb.firebaseio.com")
		f.su.Consumer = consumer
		f.su.Disable("apikeys.googleapis.com", f.keys.Server)
		im, err := DiscoverFirebase(ctx, f.c, fbSpec())
		if err != nil || len(im.List) != 3 {
			t.Errorf("%s: imports %v, err %v; want the key absent", consumer, im.List, err)
		}
	}
	f := newFBCloud(t)
	f.addAll("https://fp-1234-default-rtdb.firebaseio.com")
	f.su.Consumer = "projects/999"
	f.su.Disable("apikeys.googleapis.com", f.keys.Server)
	im, err := DiscoverFirebase(ctx, f.c, fbSpec())
	var ue *UserError
	if err == nil || errors.As(err, &ue) || len(im.List) != 0 || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("another project's SERVICE_DISABLED: imports %v, err %v; want an error", im.List, err)
	}

	// Without the Firebase project's number, nothing can be told apart:
	// fail closed.
	// A project number of 0 (an answer without one) would accept any
	// consumer: fail closed too.
	f = newFBCloud(t)
	f.addAll("https://fp-1234-default-rtdb.firebaseio.com")
	f.crm.AddProject(fbTestProject, 0)
	f.su.Consumer = "projects/999"
	f.su.Disable("apikeys.googleapis.com", f.keys.Server)
	if im, err := DiscoverFirebase(ctx, f.c, fbSpec()); err == nil || errors.As(err, &ue) || len(im.List) != 0 || !strings.Contains(err.Error(), "no project number") {
		t.Fatalf("project number 0: imports %v, err %v; want an error", im.List, err)
	}

	f = newFBCloud(t)
	f.crm.Refuse(http.StatusForbidden, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "Permission denied")
	if _, err := DiscoverFirebase(ctx, f.c, fbSpec()); err == nil || errors.As(err, &ue) || !strings.Contains(err.Error(), "fp-1234") {
		t.Fatalf("project number unreadable: err = %v", err)
	}
}

func TestDiscoverFirebaseNilClients(t *testing.T) {
	for name, drop := range map[string]func(*Clients){
		"Realtime Database": func(c *Clients) { c.FirebaseDB = nil },
		"API Keys":          func(c *Clients) { c.APIKeys = nil },
		"IAM":               func(c *Clients) { c.IAM = nil },
		"Resource Manager":  func(c *Clients) { c.CRM = nil },
	} {
		f := newFBCloud(t)
		c := *f.c
		drop(&c)
		if _, err := DiscoverFirebase(context.Background(), &c, fbSpec()); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("no %s client: err = %v", name, err)
		}
	}
}

func TestDiscoverFirebaseSeveralInstances(t *testing.T) {
	ctx := context.Background()
	// Another database of the project's, in another location: not ours to
	// judge, and the module plans nothing there.
	f := newFBCloud(t)
	f.addAll("https://fp-1234-default-rtdb.firebaseio.com")
	f.fbdb.AddInstanceFull(fbTestProject, "europe-west1", "fp-1234-eu", "USER_DATABASE", "ACTIVE", "https://fp-1234-eu.europe-west1.firebasedatabase.app")
	im, err := DiscoverFirebase(ctx, f.c, fbSpec())
	if err != nil || !slices.Equal(sortedImports(im.List), fbAllFour) {
		t.Fatalf("imports %v, err %v", im.List, err)
	}
	// A second default instance elsewhere is refused.
	f = newFBCloud(t)
	f.addAll("https://fp-1234-default-rtdb.firebaseio.com")
	f.fbdb.AddInstanceFull(fbTestProject, "europe-west1", "fp-1234-default-eu", "DEFAULT_DATABASE", "ACTIVE", "https://x.example")
	_, err = DiscoverFirebase(ctx, f.c, fbSpec())
	var ue *UserError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "fp-1234-default-eu") {
		t.Fatalf("err = %v; want the second default instance refused", err)
	}
}

// The marks and names discovery checks are the Firebase module's, read
// from the embedded Terraform, and the four import rows are built from the
// same names: a constant that drifts from the module fails here.
func TestFirebaseMarksMatchModule(t *testing.T) {
	src := readDir(t, "gcp/modules/firebase")
	block := func(typ, name string) string {
		m := regexp.MustCompile(`(?s)resource "` + typ + `" "` + name + `" \{(.*?)\n\}`).FindStringSubmatch(src)
		if m == nil {
			t.Fatalf("no resource %s.%s in the Firebase module", typ, name)
		}
		return m[1]
	}
	attr := func(b, a string) string {
		m := regexp.MustCompile(`\n\s*` + a + `\s*=\s*("[^"]*"|[a-z_.]+)`).FindStringSubmatch(b)
		if m == nil {
			t.Errorf("no %s in %s", a, b)
			return ""
		}
		return strings.Trim(m[1], `"`)
	}
	quoted := regexp.MustCompile(`"([^"]+)"`)
	strs := func(s string) []string {
		var out []string
		for _, m := range quoted.FindAllStringSubmatch(s, -1) {
			out = append(out, m[1])
		}
		return out
	}
	eq := func(what, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s: the module says %q, discovery %q", what, got, want)
		}
	}

	key := block("google_apikeys_key", "web")
	eq("key display_name", attr(key, "display_name"), webKeyDisplayName)
	eq("key name", attr(key, "name"), "var.names.api_key")
	var services []string
	for _, m := range regexp.MustCompile(`service\s*=\s*"([^"]+)"`).FindAllStringSubmatch(key, -1) {
		services = append(services, m[1])
	}
	slices.Sort(services)
	if !slices.Equal(services, tf.APITargets()) {
		t.Errorf("key API targets: the module says %v, discovery %v", services, tf.APITargets())
	}

	signer := block("google_service_account", "signer")
	eq("signer display_name", attr(signer, "display_name"), signerDisplayName)
	eq("signer description", attr(signer, "description"), signerDescription("${var.fugaro_project}"))
	eq("signer account_id", attr(signer, "account_id"), "var.names.signer_account_id")

	role := block("google_project_iam_custom_role", "token_minter")
	eq("role title", attr(role, "title"), minterRoleTitle)
	eq("role role_id", attr(role, "role_id"), "var.names.minter_role_id")
	perms := regexp.MustCompile(`(?s)permissions\s*=\s*\[(.*?)\]`).FindStringSubmatch(role)
	if perms == nil || !slices.Equal(strs(perms[1]), tf.RolePermissions[MinterRoleID]) {
		t.Errorf("role permissions: the module says %v, the pin %v", perms, tf.RolePermissions[MinterRoleID])
	}

	db := block("google_firebase_database_instance", "this")
	eq("instance region", attr(db, "region"), firebaseDBRegion)
	eq("instance instance_id", attr(db, "instance_id"), firebaseDBInstanceID("${var.project}"))
	eq("instance type", attr(db, "type"), firebaseDBType)

	// The names: what fugaro init writes into names (FirebaseVars), under
	// the module's keys, are the IDs discovery reads.
	obj := regexp.MustCompile(`(?s)variable "names" \{.*?object\(\{(.*?)\}\)`).FindStringSubmatch(src)
	if obj == nil {
		t.Fatal("no names object in the module's variables")
	}
	var keys []string
	for _, m := range regexp.MustCompile(`([a-z_]+)\s*=\s*string`).FindAllStringSubmatch(obj[1], -1) {
		keys = append(keys, m[1])
	}
	slices.Sort(keys)
	spec, err := Firebase(InstallationSpec{Project: "proj-1234", FugaroProject: "aurora"}, FirebaseInputs{FP: "fp-1234"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := FirebaseVars(spec)
	if err != nil {
		t.Fatal(err)
	}
	var tfvars struct {
		Names map[string]string `json:"names"`
	}
	if err := json.Unmarshal(raw, &tfvars); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"signer_account_id": SignerAccountID, "minter_role_id": MinterRoleID, "api_key": APIKeyID}
	if !maps.Equal(tfvars.Names, want) || !slices.Equal(keys, slices.Sorted(maps.Keys(want))) {
		t.Errorf("names: tfvars %v, module keys %v; want %v", tfvars.Names, keys, want)
	}

	// The import rows: the module's addresses, and IDs from the same names.
	const p = "P"
	for _, r := range []struct {
		got    Import
		to, id string
	}{
		{newImport(importFirebaseDB, p, firebaseDBRegion, "", firebaseDBInstanceID(p)), "module.firebase.google_firebase_database_instance.this",
			"projects/P/locations/" + attr(db, "region") + "/instances/" + strings.ReplaceAll(attr(db, "instance_id"), "${var.project}", p)},
		{newImport(importAPIKey, p, "", "", spec.Names.APIKey), "module.firebase.google_apikeys_key.web", "projects/P/locations/global/keys/" + tfvars.Names["api_key"]},
		{newImport(importSignerSA, p, "", "", serviceAccountEmail(spec.Names.SignerAccountID, p)), "module.firebase.google_service_account.signer",
			"projects/P/serviceAccounts/" + tfvars.Names["signer_account_id"] + "@P.iam.gserviceaccount.com"},
		{newImport(importMinterRole, p, "", "", spec.Names.MinterRoleID), "module.firebase.google_project_iam_custom_role.token_minter", "projects/P/roles/" + tfvars.Names["minter_role_id"]},
	} {
		if r.got != (Import{To: r.to, ID: r.id}) {
			t.Errorf("import row %v, want %s %s", r.got, r.to, r.id)
		}
	}
}

// The minter role is the spec's (names.minter_role_id), not the constant:
// a stranger holding it is refused with the minter message.
func TestDiscoverFirebaseMinterIsTheSpecs(t *testing.T) {
	f := newFBCloud(t)
	f.addAll("https://fp-1234-default-rtdb.firebaseio.com")
	role := "projects/" + fbTestProject + "/roles/otherMinter"
	f.iam.SetServiceAccountPolicy(fbTestProject, fbSigner, gcpfake.Binding{Role: role, Members: []string{"user:a@x.com", "user:old@example.com"}})
	spec := fbSpec()
	spec.Names.MinterRoleID = "otherMinter"
	_, err := DiscoverFirebase(context.Background(), f.c, spec)
	var ue *UserError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v; want a refusal", err)
	}
	want := "token signer " + fbSigner + " grants otherMinter to members who are not this installation's launchers or operators:\n  user:old@example.com\n"
	if !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "--member=user:a@x.com") || !strings.Contains(err.Error(), "--role="+role) {
		t.Fatalf("refusal:\n%v\nwant the minter message for %s, naming only the stranger", err, role)
	}
}
