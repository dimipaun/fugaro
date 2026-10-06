package infra

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
)

const fbTestProject = "fp-1234"

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
		if len(im.Notes) != 0 {
			t.Errorf("notes %v", im.Notes)
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
	if len(im.Notes) != 1 || !strings.Contains(im.Notes[0], "custom role "+fbMinterRole+" is deleted; the plan's create restores it") {
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
	for _, stranger := range []string{"serviceAccount:old@p.iam.gserviceaccount.com", "allUsers", "allAuthenticatedUsers", "user:A@x.com", "group:ops@x.com.evil", "domain:x.com"} {
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
	got = signerGrantMessage("fugaro-token-signer@fp-1234.iam.gserviceaccount.com", "fp-1234", "roles/owner", []signerGrant{{member: "user:a@x.com"}, {member: "user:b@x.com", conditional: true}})
	want = `token signer fugaro-token-signer@fp-1234.iam.gserviceaccount.com grants roles/owner, which Fugaro never grants on it, to:
  user:a@x.com
  user:b@x.com (under a condition)
Each could act as the signer and mint run tokens. Remove them, then rerun fugaro init --firebase fp-1234:
  gcloud iam service-accounts remove-iam-policy-binding fugaro-token-signer@fp-1234.iam.gserviceaccount.com --project fp-1234 --member=user:a@x.com --role=roles/owner
  gcloud iam service-accounts remove-iam-policy-binding fugaro-token-signer@fp-1234.iam.gserviceaccount.com --project fp-1234 --member=user:b@x.com --role=roles/owner --all`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}

	// A member a shell would expand is quoted in the command.
	got = foreignMinterMessage("s@p.iam.gserviceaccount.com", "p", []string{"deleted:user:x@y.com?uid=1"})
	if !strings.Contains(got, "--member='deleted:user:x@y.com?uid=1' ") {
		t.Errorf("unquoted: %s", got)
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
