//go:build terraform

package infra

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheStoreConvergesWithTheFrontDoorsOptIn is D935's convergence test, and
// it is the migration the repository requires of a change that reaches an
// installation: the store the front door's records live in is a bucket this
// task creates, and the three things that must hold are that an update WITH
// the front door's flag ends with the bucket and its one grant, that an update
// without the flag ends with neither, and that the bucket is created rather
// than adopted — so a bucket already held under that name in another project
// (bucket names are global and `fugaro-mcpauth-<gcp-project>` is a function of
// the project alone) fails the apply instead of receiving this installation's
// token hashes.
//
// The plan below is a REAL plan of the embedded installation root — the same
// tree `fugaro update` writes into its workdir — read out of `terraform show
// -json`'s resource_changes, which is what an update applies. It is a fresh
// create plan and reads nothing from Google: the root's one data source
// (budget.tf's google_project) hangs off google_project_service.this, which
// this same plan creates, so the provider defers the read to apply
// (scripts/gen-golden-plans.sh's own recipe, which is what makes the committed
// goldens real plans of an unreachable machine). The provider block is
// overridden with a placeholder token and the three skip flags
// (real_addresses_test.go's providerOverride), and the plan runs with a fresh
// HOME and a dead proxy so nothing of the caller's environment reaches it.
//
// The two opt-in cases are one function's two calls: specWithTheFrontDoor and
// specWithoutIt differ in one field, spec.MCPServer, which is exactly the key
// `fugaro update --mcp-server` writes into the tfvars and exactly what the
// module's local.mcp_server_on reads. "An installation at the previous
// version" is this same root without that key: the store is created by
// nothing else in the tree, so a plan without it is a plan of the installation
// that existed before this task, and the third claim below is what keeps the
// create from turning into an adoption of a name somebody else holds.
func TestTheStoreConvergesWithTheFrontDoorsOptIn(t *testing.T) {
	const (
		storeBucket = "fugaro-mcpauth-proj-1234"
		storeAddr   = "module.installation.google_storage_bucket.mcpauth_store[0]"
		grantAddr   = "module.installation.google_storage_bucket_iam_member.mcpauth_store[0]"
		// The front door's own account, which the mock provider computes the
		// way the real one does: account_id at the project.
		storeMember = "serviceAccount:fugaro-mcp@proj-1234.iam.gserviceaccount.com"
	)

	t.Run("with the front door's opt-in", func(t *testing.T) {
		plan := installationPlanFor(t, specWithTheFrontDoor(t))

		bucket, ok := plan[storeAddr]
		if !ok {
			t.Fatalf("the plan of an installation that passed --mcp-server creates no %s: the store D935 names is this task's, and an update that turned the front door on without it would ship an endpoint whose records have nowhere to live", storeAddr)
		}
		for _, want := range []struct {
			what string
			got  any
			want any
		}{
			{"name", bucket.After["name"], storeBucket},
			{"uniform_bucket_level_access", bucket.After["uniform_bucket_level_access"], true},
			{"public_access_prevention", bucket.After["public_access_prevention"], "enforced"},
			{"force_destroy", bucket.After["force_destroy"], false},
		} {
			if want.got != want.want {
				t.Errorf("%s: %s is %v, want %v", storeAddr, want.what, want.got, want.want)
			}
		}

		grant, ok := plan[grantAddr]
		if !ok {
			t.Fatalf("the plan creates the store and no %s: D935's whole content is that the bucket's IAM names one member, and a bucket with no binding is a store no front door can read", grantAddr)
		}
		for _, want := range []struct {
			what string
			got  any
			want any
		}{
			{"bucket", grant.After["bucket"], storeBucket},
			{"role", grant.After["role"], "roles/storage.objectAdmin"},
			{"member", grant.After["member"], storeMember},
		} {
			if want.got != want.want {
				t.Errorf("%s: %s is %v, want %v", grantAddr, want.what, want.got, want.want)
			}
		}

		// One member, and no other row of the plan names the store's bucket:
		// the bucket itself and the one binding are the whole of it, so a
		// second grant — however it is spelled — is a member nobody reviewed.
		var naming []string
		for _, rc := range plan {
			if strings.Contains(rc.json(), storeBucket) {
				naming = append(naming, rc.Address)
			}
		}
		if len(naming) != 2 || !contains(naming, storeAddr) || !contains(naming, grantAddr) {
			t.Errorf("the plan's rows naming %s are %v, want exactly [%s %s]: the store's member list is one, and a third row is a reader D935 does not name", storeBucket, naming, storeAddr, grantAddr)
		}

		// Created and never adopted (the squatting half of D935): the bucket's
		// change is a create, carries no importing, and no data source reads a
		// bucket of this name — a create against a name that already exists
		// fails the apply, which is the fail-closed direction, where an import
		// or a read would take a bucket this installation did not create.
		for _, rc := range []plannedResource{bucket, grant} {
			if len(rc.Actions) != 1 || rc.Actions[0] != "create" {
				t.Errorf("%s: the plan's actions are %v, want [create]: the store is created by this installation with the owner's own rights, and anything else is a name taken rather than made (D935, 2026-11-05)", rc.Address, rc.Actions)
			}
			if len(rc.Importing) != 0 {
				t.Errorf("%s: the plan imports the resource (%v): a bucket that already exists under the store's name fails the apply rather than being adopted, and an import is the adoption", rc.Address, rc.Importing)
			}
		}
		for _, rc := range plan {
			if rc.Type == "google_storage_bucket" && len(rc.Actions) == 1 && rc.Actions[0] == "read" {
				t.Errorf("%s reads a bucket: nothing may read the store out of a bucket that is already there, and M2 opens the name this task publishes rather than discovering one", rc.Address)
			}
		}
	})

	t.Run("without the front door's opt-in", func(t *testing.T) {
		plan := installationPlanFor(t, specWithoutIt(t))
		for _, rc := range plan {
			if strings.Contains(rc.Address, "mcpauth_store") || strings.Contains(rc.json(), storeBucket) {
				t.Errorf("the plan of an installation that did not pass --mcp-server carries %s: the store arrives with the front door's own opt-in and with nothing else, so an installation that asked only for the coordinator's read-only view acquires neither the bucket nor its grant (D934, D935)", rc.Address)
			}
		}
	})
}

// plannedResource is one entry of a plan's resource_changes, in the shape these
// tests read: the address, the type, the action, the planned values and
// whether the change imports.
type plannedResource struct {
	Address   string         `json:"address"`
	Type      string         `json:"type"`
	Actions   []string       `json:"actions"`
	After     map[string]any `json:"after"`
	Importing map[string]any `json:"importing"`
}

// json is the resource as one string, for the "no row names this bucket"
// assertions: an address is not enough, because the bucket's name reaches a row
// through a value (the grant's own bucket) rather than through its address.
func (r plannedResource) json() string {
	b, err := json.Marshal(r)
	if err != nil {
		return ""
	}
	return string(b)
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

// specWithTheFrontDoor is the installation an owner gets from
// `fugaro update --firebase-project proj-1234 --coordinator --mcp-server`:
// the coordinator's opt-in beside the front door's, which is the pair the
// option refuses without (InstallOptions.MCPServer's refusal).
func specWithTheFrontDoor(t *testing.T) InstallationSpec {
	t.Helper()
	spec := specWithoutIt(t)
	m, err := MCPServer(parseLC(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectProfilesLCBlock))
	if err != nil {
		t.Fatal(err)
	}
	spec.MCPServer = &m
	return spec
}

// specWithoutIt is the same installation one flag earlier: the coordinator's
// job and the budget backend are there, and the front door's key is not — the
// installation every golden plan in internal/infra/tf/testdata/golden is a plan
// of.
func specWithoutIt(t *testing.T) InstallationSpec {
	t.Helper()
	spec := projectJobSpec(t)
	spec.Launchers = []string{"user:launcher@example.com"}
	spec.Operators = []string{"user:operator@example.com"}
	email := "ops@example.com"
	spec.AlertEmail = &email
	h := History0(t, true)
	spec.EnableBudget, spec.History = true, &h
	cs := Coordinator0(t, true)
	spec.Coordinator = &cs
	return spec
}

// installationPlanFor plans the embedded installation root for spec and
// returns its resource_changes by address. It is real_addresses_test.go's
// generator recipe without the -update gate: the plan reads no Google API
// (the root's one data source is deferred behind a resource this same plan
// creates), so it needs no credential and no network beyond init's provider
// download.
func installationPlanFor(t *testing.T, spec InstallationSpec) map[string]plannedResource {
	t.Helper()
	bin, err := exec.LookPath("terraform")
	if err != nil {
		t.Fatalf("this test needs terraform on PATH: %v", err)
	}
	vars, err := InstallationVars(spec)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeTree(t, dir)
	root := filepath.Join(dir, "gcp", "roots", "installation")
	// The root's own terraform tests are not what this test runs.
	if err := os.RemoveAll(filepath.Join(root, "tests")); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"terraform.tfvars.json": string(vars),
		"provider_override.tf":  providerOverride,
		"backend_override.tf":   backendOverride,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tfdata := filepath.Join(dir, "tfdata")
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	// A fresh HOME and a data directory outside the tree, so nothing of the
	// developer's terraform setup (a provider mirror, a CLI config) is read and
	// no provider binary lands inside the embedded tree.
	base := []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + home, "TF_DATA_DIR=" + tfdata,
		"TF_IN_AUTOMATION=1", "TF_INPUT=0", "CHECKPOINT_DISABLE=1",
	}
	run := func(env []string, args ...string) []byte {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir = root
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("terraform %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	// init needs the provider registry (the lock file pins hashicorp/google
	// 8.4.0 and the plugin cache is not guaranteed); the plan needs nothing.
	run(base, "init", "-input=false", "-no-color", "-lockfile=readonly")
	planEnv := append(base,
		"GOOGLE_OAUTH_ACCESS_TOKEN=no-credentials-the-plan-makes-no-api-call",
		"HTTPS_PROXY=http://127.0.0.1:9", "HTTP_PROXY=http://127.0.0.1:9",
		"https_proxy=http://127.0.0.1:9", "http_proxy=http://127.0.0.1:9",
		"NO_PROXY=", "no_proxy=",
	)
	run(planEnv, "plan", "-input=false", "-no-color", "-out=plan.bin", "-var-file=terraform.tfvars.json")
	show := run(planEnv, "show", "-json", "plan.bin")
	var plan struct {
		ResourceChanges []struct {
			Address string `json:"address"`
			Type    string `json:"type"`
			Change  struct {
				Actions   []string       `json:"actions"`
				After     map[string]any `json:"after"`
				Importing map[string]any `json:"importing"`
			} `json:"change"`
		} `json:"resource_changes"`
	}
	if err := json.Unmarshal(show, &plan); err != nil {
		t.Fatal(err)
	}
	out := make(map[string]plannedResource, len(plan.ResourceChanges))
	for _, rc := range plan.ResourceChanges {
		out[rc.Address] = plannedResource{
			Address: rc.Address, Type: rc.Type, Actions: rc.Change.Actions,
			After: rc.Change.After, Importing: rc.Change.Importing,
		}
	}
	return out
}
