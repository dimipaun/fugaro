package managed

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// stateDoc is a Terraform state document of the shape a state bucket holds,
// built here rather than checked in as a fixture so each test says what it is
// about: the version, and one entry per resource with its mode, type and
// attributes.
func stateDoc(version int, resources ...string) []byte {
	return []byte(fmt.Sprintf(`{"version":%d,"terraform_version":"1.9.8","serial":3,"lineage":"x","outputs":{},"resources":[%s]}`,
		version, strings.Join(resources, ",")))
}

// stateRes is one resource entry of that document: mode defaults to
// "managed", and attrs is the instance's attributes map.
func stateRes(mode, typ, name, attrs string) string {
	if mode == "" {
		mode = "managed"
	}
	return fmt.Sprintf(`{"mode":%q,"type":%q,"name":%q,"provider":"provider[\"registry.terraform.io/hashicorp/google\"]","instances":[{"schema_version":0,"attributes":%s}]}`,
		mode, typ, name, attrs)
}

// The accounting reads a state's resources in the terms the whole-project read
// answers in: the Cloud Asset type, and the name in one of the three shapes
// StateResource names. Every shape is here once, because a shape that is wrong
// is a resource that reads as foreign and falls every run back for no reason.
func TestAccountsOfStateNamesEveryShape(t *testing.T) {
	data := stateDoc(4,
		stateRes("", "google_storage_bucket", "runs", `{"id":"fugaro-runs-`+gcpProject+`","name":"fugaro-runs-`+gcpProject+`"}`),
		stateRes("", "google_secret_manager_secret", "token", `{"id":"projects/`+gcpProject+`/secrets/fugaro-token","secret_id":"fugaro-token"}`),
		stateRes("", "google_storage_bucket_object", "marker", `{"id":"fugaro-runs-`+gcpProject+`/fugaro/allow-delete.json","bucket":"fugaro-runs-`+gcpProject+`","name":"fugaro/allow-delete.json"}`),
	)
	got, err := AccountsOfState(data)
	if err != nil {
		t.Fatalf("AccountsOfState: %v", err)
	}
	want := []StateResource{
		{Type: "storage.googleapis.com/Bucket", Name: "fugaro-runs-" + gcpProject},
		{Type: "secretmanager.googleapis.com/Secret", Name: "projects/" + gcpProject + "/secrets/fugaro-token"},
		{Type: "storage.googleapis.com/Object", Name: "projects/_/buckets/fugaro-runs-" + gcpProject + "/objects/fugaro/allow-delete.json"},
	}
	if len(got) != len(want) {
		t.Fatalf("AccountsOfState answered %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// What the accounting does NOT account for, and why each is the safe
// direction: a data source is not a managed resource, a type GCP keeps inside
// another resource is not an asset of its own, and a type this build cannot
// place in the listing contributes nothing — so its resources stay foreign and
// the run keeps its confirmations (D59).
func TestAccountsOfStateLeavesOutWhatItCannotPlace(t *testing.T) {
	data := stateDoc(4,
		stateRes("data", "google_storage_bucket", "runs", `{"id":"somebody-elses"}`),
		stateRes("", "google_project_iam_member", "grant", `{"id":"projects/`+gcpProject+`/roles/x","member":"serviceAccount:a@b.c","role":"roles/x"}`),
		stateRes("", "google_compute_instance", "vm", `{"id":"projects/`+gcpProject+`/zones/us-east5-a/instances/not-in-the-table"}`),
		stateRes("", "google_storage_bucket", "runs", `{"id":"fugaro-runs-`+gcpProject+`"}`),
	)
	got, err := AccountsOfState(data)
	if err != nil {
		t.Fatalf("AccountsOfState: %v", err)
	}
	if len(got) != 1 || got[0].Name != "fugaro-runs-"+gcpProject {
		t.Errorf("AccountsOfState answered %+v, want only the bucket", got)
	}
}

// The fail-closed direction: a state this build cannot read is an error and
// never an empty list of accounts. An empty list is the answer that makes
// every listed resource foreign, so answering it for a parse failure would
// blame the project for this program's failure (Global Constraint 3).
func TestAccountsOfStateFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"not JSON", []byte("<html>error</html>"), "reading a Terraform state document"},
		{"not an object", []byte(`[]`), "reading a Terraform state document"},
		{"another format", stateDoc(3, stateRes("", "google_storage_bucket", "runs", `{"id":"x"}`)), "format version 3"},
		{"no version", []byte(`{"resources":[]}`), "format version 0"},
		{"no id", stateDoc(4, stateRes("", "google_storage_bucket", "runs", `{"name":"x"}`)), "carries no id"},
		{"id not a string", stateDoc(4, stateRes("", "google_storage_bucket", "runs", `{"id":7}`)), "is not a string"},
		{"id null", stateDoc(4, stateRes("", "google_storage_bucket", "runs", `{"id":null}`)), "is null"},
		{"id empty", stateDoc(4, stateRes("", "google_storage_bucket", "runs", `{"id":""}`)), "is empty"},
		{"id in two lines", stateDoc(4, stateRes("", "google_storage_bucket", "runs", `{"id":"a\nb"}`)), "not one line"},
		{"object with no bucket", stateDoc(4, stateRes("", "google_storage_bucket_object", "o", `{"id":"b/o","name":"o"}`)), "carries no bucket"},
		{"object with no name", stateDoc(4, stateRes("", "google_storage_bucket_object", "o", `{"id":"b/o","bucket":"b"}`)), "carries no name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := AccountsOfState(tc.data)
			if err == nil {
				t.Fatalf("AccountsOfState answered %+v and no error, want an error naming %q", got, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the error does not name %q: %v", tc.want, err)
			}
			if len(got) != 0 {
				t.Errorf("a state that could not be read accounted for %+v, want nothing", got)
			}
		})
	}
}

// The table itself, as a property: an entry is either a Cloud Asset type (the
// service GCP files the resource under, so the value is always
// "<service>.googleapis.com/<Kind>") or deliberately "", and nothing else. A
// value that is neither would be matched by type against an asset that does
// not exist — an entry that accounts for nothing while claiming to.
func TestStateAssetTypesAreEitherAnAssetTypeOrDeliberatelyNot(t *testing.T) {
	for typ, asset := range stateAssetTypes {
		if !strings.HasPrefix(typ, "google_") {
			t.Errorf("the table names %q, which is not a Terraform google provider type", typ)
		}
		if asset == "" {
			continue
		}
		service, kind, ok := strings.Cut(asset, ".googleapis.com/")
		if !ok || service == "" || kind == "" || strings.Contains(kind, "/") || strings.Contains(asset, " ") {
			t.Errorf("the table maps %s to %q, which is not a Cloud Asset type", typ, asset)
		}
	}
}

// The table's completeness, held against the modules rather than trusted: every
// resource type the tree's Terraform declares is a key of stateAssetTypes.
//
// Why this is the direction that matters: the other one (a type in the table
// that no module declares) costs nothing, because no state holds it. A type
// the modules declare and the table omits is the failure this test is for —
// AccountsOfState skips it silently (its `if !ok … continue`), so every
// resource of that type stays Foreign and Inventory.Check's answer for such a
// project can never clear: `foreign` never empties, and Fallback is true for
// as long as the project exists. The compute module's three types
// (google_compute_firewall, google_compute_router, google_compute_router_nat,
// deploy/terraform/gcp/modules/compute/network.tf) were exactly that, and the
// compute root the Batch backend's stage applies writes its state into the
// installation's own bucket, which managedAccounted reads with no prefix.
//
// The walk is the modules' own text, as tf/cover_test.go's
// TestAllowedTypesCoverTheModules walks it for init's allowlist: a resource
// block added to any module and left undecided here fails by name.
func TestStateAssetTypesCoverTheModules(t *testing.T) {
	root := filepath.Join("..", "..", "deploy", "terraform")
	resRE := regexp.MustCompile(`(?m)^resource "([a-z0-9_]+)"`)
	n := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".tf") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		n++
		for _, m := range resRE.FindAllStringSubmatch(string(b), -1) {
			typ := m[1]
			if !strings.HasPrefix(typ, "google_") {
				continue // the table translates google provider types only
			}
			if _, ok := stateAssetTypes[typ]; !ok {
				t.Errorf("%s declares %s, which stateAssetTypes does not decide: its resources would stay foreign and this project's fallback would never clear", path, typ)
			}
		}
		return nil
	})
	if err != nil || n < 10 {
		t.Fatalf("read %d module files: %v", n, err)
	}
}

// The two halves of Task 4.5 meet here: what AccountsOfState reads out of a
// state is what Inventory.Check accounts for. A state holding a bucket, a
// secret and a marker object leaves exactly those three out of the warning,
// and the fourth resource — one nothing accounts for — falls the run back.
func TestAccountsOfStateAccountsForWhatTheReadLists(t *testing.T) {
	data := stateDoc(4,
		stateRes("", "google_storage_bucket", "runs", `{"id":"fugaro-runs-`+gcpProject+`"}`),
		stateRes("", "google_secret_manager_secret", "token", `{"id":"projects/`+gcpProject+`/secrets/fugaro-token"}`),
		stateRes("", "google_storage_bucket_object", "marker", `{"id":"fugaro-runs-`+gcpProject+`/fugaro/allow-delete.json","bucket":"fugaro-runs-`+gcpProject+`","name":"fugaro/allow-delete.json"}`),
	)
	accounted, err := AccountsOfState(data)
	if err != nil {
		t.Fatalf("AccountsOfState: %v", err)
	}
	foreign := "//compute.googleapis.com/projects/" + gcpProject + "/zones/us-east5-a/instances/somebody-elses"
	a := &fakeAssets{assets: []Asset{
		listed("//storage.googleapis.com/fugaro-runs-"+gcpProject, "storage.googleapis.com/Bucket"),
		listed("//secretmanager.googleapis.com/projects/"+gcpProject+"/secrets/fugaro-token", "secretmanager.googleapis.com/Secret"),
		listed("//storage.googleapis.com/projects/_/buckets/fugaro-runs-"+gcpProject+"/objects/fugaro/allow-delete.json", "storage.googleapis.com/Object"),
		listed(foreign, "compute.googleapis.com/Instance"),
	}}
	d := inventory(a, accounted...).Check(context.Background())

	if !d.Fallback || d.Cause != CauseForeign {
		t.Fatalf("the run did not fall back over the one unaccounted resource: %+v", d)
	}
	if len(d.Foreign) != 1 || d.Foreign[0].Name != foreign {
		t.Errorf("Foreign = %+v, want only %s", d.Foreign, foreign)
	}

	// And the same listing with the instance gone is a clean project: the
	// state's own entries account for everything the read returned.
	a.assets = a.assets[:3]
	if d := inventory(a, accounted...).Check(context.Background()); d.Fallback {
		t.Errorf("a project holding only what the state accounts for decided %+v, want clean: %s", d, d.Why)
	}
}

// A state document that is valid JSON but not a state — an object with no
// resources at all — accounts for nothing and is not an error: Terraform
// writes an empty resources list for a root that has never applied, and each
// of its resources is a fallback rather than a failure to read.
func TestAccountsOfStateOfAnEmptyState(t *testing.T) {
	got, err := AccountsOfState(stateDoc(4))
	if err != nil {
		t.Fatalf("AccountsOfState: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("AccountsOfState answered %+v, want nothing", got)
	}
}

// And the document round-trips through encoding/json the way a state bucket
// holds it: the fields this build reads are the ones Terraform writes.
func TestAccountsOfStateReadsTerraformsOwnFieldNames(t *testing.T) {
	var m map[string]any
	if err := json.Unmarshal(stateDoc(4, stateRes("managed", "google_storage_bucket", "runs", `{"id":"b"}`)), &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["resources"]; !ok {
		t.Fatal("the fixture is not shaped like a state document")
	}
}
