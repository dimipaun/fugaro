package tf

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What a managed plan runs at apply beyond its resources' API calls: a
// provisioner, a provider binary that is not Google's, a data source. None of
// them is a resource_changes entry's type, actions or after, so a cover that
// reads only those admits all three. Under D75 the operator's shell applies by
// impersonating the deployer, so each would run with the operator's own
// credentials. These fixtures are plan JSON as `terraform show -json` writes
// it (testdata/managed_provisioners_plan.json is terraform's own output, the
// shape the hand-written ones follow), read through parsePlan as Show reads it.

const (
	googleProvider     = "registry.terraform.io/hashicorp/google"
	googleBetaProvider = "registry.terraform.io/hashicorp/google-beta"
)

// jsonPlan is a plan as terraform writes it, read through parsePlan: the
// changes as resource_changes and config as configuration, which is left out
// of the document when nil.
func jsonPlan(t *testing.T, changes []map[string]any, config map[string]any) *Plan {
	t.Helper()
	doc := map[string]any{"format_version": "1.2", "resource_changes": changes}
	if config != nil {
		doc["configuration"] = config
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	p, err := parsePlan(data)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// bucketChange is a managed bucket the plan creates, under provider.
func bucketChange(address, name, provider string) map[string]any {
	return map[string]any{"address": address, "mode": "managed", "type": "google_storage_bucket",
		"name": "b", "provider_name": provider,
		"change": map[string]any{"actions": []any{"create"}, "after": map[string]any{"name": name}, "after_unknown": map[string]any{}}}
}

// bucketConfig is the configuration's entry for the bucket, with provisioners
// when any are given.
func bucketConfig(provisioners ...map[string]any) map[string]any {
	r := map[string]any{"address": "google_storage_bucket.b", "mode": "managed", "type": "google_storage_bucket",
		"name": "b", "provider_config_key": "google"}
	if len(provisioners) > 0 {
		r["provisioners"] = provisioners
	}
	return r
}

// config is a configuration whose providers are the given full names, keyed
// by their local names, and whose root module holds resources and calls.
func config(providers map[string]string, resources []map[string]any, calls map[string]any) map[string]any {
	pc := map[string]any{}
	for name, full := range providers {
		pc[name] = map[string]any{"name": name, "full_name": full}
	}
	root := map[string]any{"resources": resources}
	if calls != nil {
		root["module_calls"] = calls
	}
	return map[string]any{"provider_config": pc, "root_module": root}
}

var localExec = map[string]any{"type": "local-exec", "expressions": map[string]any{"command": map[string]any{"constant_value": "true"}}}

func TestManagedCoverRefusesWhatRunsAtApply(t *testing.T) {
	google := map[string]string{"google": googleProvider}
	bucket := bucketChange("google_storage_bucket.b", ManagedPrefix+"b", googleProvider)
	inModule := bucketChange("module.app.module.store.google_storage_bucket.b", ManagedPrefix+"b", googleProvider)
	for name, tc := range map[string]struct {
		changes []map[string]any
		config  map[string]any
		want    []string
	}{
		"a provisioner": {[]map[string]any{bucket},
			config(google, []map[string]any{bucketConfig(localExec)}, nil),
			[]string{"google_storage_bucket.b (", "provisioner"}},
		"a provisioner two modules down": {[]map[string]any{inModule},
			config(google, nil, map[string]any{"app": map[string]any{"source": "./app", "module": map[string]any{
				"module_calls": map[string]any{"store": map[string]any{"source": "./store", "module": map[string]any{
					"resources": []map[string]any{bucketConfig(localExec)}}}}}}}),
			[]string{"module.app.module.store.google_storage_bucket.b (", "provisioner"}},
		"a provider that is not Google's": {
			[]map[string]any{bucketChange("google_storage_bucket.b", ManagedPrefix+"b", "registry.terraform.io/evil/google")},
			config(map[string]string{"google": "registry.terraform.io/evil/google"}, []map[string]any{bucketConfig()}, nil),
			[]string{"google_storage_bucket.b (", "registry.terraform.io/evil/google"}},
		"a provider the configuration requires and no change uses": {[]map[string]any{bucket},
			config(map[string]string{"google": googleProvider, "evil": "registry.terraform.io/evil/evil"}, []map[string]any{bucketConfig()}, nil),
			[]string{"registry.terraform.io/evil/evil"}},
		"a provider configured with no full name": {[]map[string]any{bucket},
			config(map[string]string{"google": ""}, []map[string]any{bucketConfig()}, nil),
			[]string{"provider"}},
		"a resource change naming no provider": {[]map[string]any{bucketChange("google_storage_bucket.b", ManagedPrefix+"b", "")},
			config(google, []map[string]any{bucketConfig()}, nil),
			[]string{"google_storage_bucket.b (", "provider"}},
		"a data source read at apply": {[]map[string]any{bucket, {"address": "data.external.x", "mode": "data", "type": "external",
			"name": "x", "provider_name": "registry.terraform.io/hashicorp/external",
			"change": map[string]any{"actions": []any{"read"}, "after": map[string]any{}, "after_unknown": map[string]any{}}}},
			config(google, []map[string]any{bucketConfig()}, nil),
			[]string{"data.external.x (", "data source"}},
		"a Google data source read at apply": {[]map[string]any{bucket, {"address": "data.google_project.p", "mode": "data",
			"type": "google_project", "name": "p", "provider_name": googleProvider,
			"change": map[string]any{"actions": []any{"read"}, "after": map[string]any{}, "after_unknown": map[string]any{}}}},
			config(google, []map[string]any{bucketConfig()}, nil),
			[]string{"data.google_project.p (", "data source"}},
		"a data source the plan already read": {[]map[string]any{bucket},
			config(google, []map[string]any{bucketConfig(), {"address": "data.google_project.p", "mode": "data",
				"type": "google_project", "name": "p", "provider_config_key": "google"}}, nil),
			[]string{"data.google_project.p (", "data source"}},
		"a change with no mode": {[]map[string]any{func() map[string]any {
			b := bucketChange("google_storage_bucket.b", ManagedPrefix+"b", googleProvider)
			delete(b, "mode")
			return b
		}()}, config(google, []map[string]any{bucketConfig()}, nil),
			[]string{"google_storage_bucket.b (", `mode ""`}},
		"no configuration at all": {[]map[string]any{bucket}, nil, []string{"configuration"}},
	} {
		t.Run(name, func(t *testing.T) {
			out := managedTestCover().NotCovered(jsonPlan(t, tc.changes, tc.config), nil)
			if !refusedWith(out, tc.want...) {
				t.Fatalf("gives %q, want a refusal carrying %q", out, tc.want)
			}
		})
	}
	// The positive controls, so the rows above cannot pass by every plan
	// being refused: Google's two providers, with and without a module.
	for name, tc := range map[string]struct {
		changes []map[string]any
		config  map[string]any
	}{
		"google": {[]map[string]any{bucket}, config(google, []map[string]any{bucketConfig()}, nil)},
		"google-beta": {[]map[string]any{bucketChange("google_storage_bucket.b", ManagedPrefix+"b", googleBetaProvider)},
			config(map[string]string{"google-beta": googleBetaProvider}, []map[string]any{bucketConfig()}, nil)},
		"in a module": {[]map[string]any{inModule}, config(google, nil, map[string]any{"app": map[string]any{"source": "./app",
			"module": map[string]any{"module_calls": map[string]any{"store": map[string]any{"source": "./store",
				"module": map[string]any{"resources": []map[string]any{bucketConfig()}}}}}}})},
		"no changes and no configuration": {nil, nil},
	} {
		t.Run("admits "+name, func(t *testing.T) {
			if out := managedTestCover().NotCovered(jsonPlan(t, tc.changes, tc.config), nil); len(out) != 0 {
				t.Fatalf("gives %q, want it covered", out)
			}
		})
	}
}

// TestManagedCoverRefusesTerraformsOwnProvisioners reads terraform's own
// output for a plan with a provisioner at the root and one in a module call:
// the shape the hand-written fixtures above follow, refused at both.
func TestManagedCoverRefusesTerraformsOwnProvisioners(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "managed_provisioners_plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := parsePlan(data)
	if err != nil {
		t.Fatal(err)
	}
	out := managedTestCover().NotCovered(p, nil)
	for _, addr := range []string{"terraform_data.a", "module.child.terraform_data.b"} {
		if !refusedWith(out, addr+" (", "provisioner") {
			t.Errorf("gives %q, want %s refused for its provisioner", out, addr)
		}
	}
}

// TestManagedCoverRefusesALocalSource: an object whose source is a local path
// uploads that file from the machine that applies — the operator's — so a
// managed object carries no source, spelled out or computed. (content =
// file(...) is read at plan time, which is outside this cover: Task 3.x.)
func TestManagedCoverRefusesALocalSource(t *testing.T) {
	obj := func(after map[string]any) ResourceChange {
		after["bucket"], after["name"] = ManagedPrefix+"bucket", "x"
		return mrc("google_storage_bucket_object", after)
	}
	for name, rc := range map[string]ResourceChange{
		"a local path":    obj(map[string]any{"source": "~/.config/gcloud/application_default_credentials.json"}),
		"a computed path": withUnknown(obj(map[string]any{}), map[string]any{"source": true}),
	} {
		t.Run(name, func(t *testing.T) {
			if out := managedTestCover().NotCovered(managedPlan(rc), nil); !refusedWith(out, rc.Address+" (", "source") {
				t.Fatalf("gives %q, want one refusal naming source", out)
			}
		})
	}
	if out := managedTestCover().NotCovered(managedPlan(obj(map[string]any{"content": "{}"})), nil); len(out) != 0 {
		t.Errorf("an object with literal content gives %q, want it covered", out)
	}
}

// TestManagedCoverRefusesAPathInAName: the provider reads an IAM resource's
// parent as an import ID when it carries one, so a name that starts with the
// prefix and goes on as a path names whatever the path does — Fugaro's own
// secret, job, repository or the runs bucket. A managed name is the resource's
// own short ID: a '/' or a ':', or any character outside [a-z0-9._-], is
// refused.
func TestManagedCoverRefusesAPathInAName(t *testing.T) {
	grant := func(typ, attr, value, role string) ResourceChange {
		return mrc(typ, map[string]any{attr: value, "member": "serviceAccount:" + managedAccount, "role": role})
	}
	for name, rc := range map[string]ResourceChange{
		"a secret by its path": grant("google_secret_manager_secret_iam_member", "secret_id",
			ManagedPrefix+"x/projects/proj-1/secrets/fugaro-github-token", "roles/secretmanager.secretAccessor"),
		"the runs bucket after the prefix": grant("google_storage_bucket_iam_member", "bucket",
			ManagedPrefix+"b/fugaro-runs-proj-1", "roles/storage.objectAdmin"),
		"an object in the runs bucket after the prefix": mrc("google_storage_bucket_object", map[string]any{
			"bucket": ManagedPrefix + "b/fugaro-runs-proj-1", "name": "x"}),
		"a job by its path": grant("google_cloud_run_v2_job_iam_member", "name",
			ManagedPrefix+"x/projects/proj-1/locations/us-east5/jobs/fugaro-workflow", "roles/run.invoker"),
		"a repository by its path": grant("google_artifact_registry_repository_iam_member", "repository",
			ManagedPrefix+"x/projects/proj-1/locations/us-east5/repositories/fugaro-base", "roles/artifactregistry.writer"),
		"a colon":   mrc("google_storage_bucket", map[string]any{"name": ManagedPrefix + "b:fugaro-runs-proj-1"}),
		"a capital": mrc("google_storage_bucket", map[string]any{"name": ManagedPrefix + "Bucket"}),
		"a space":   mrc("google_service_account", map[string]any{"account_id": ManagedPrefix + "sa x"}),
		"a scheduler's path": mrc("google_cloud_scheduler_job", map[string]any{"name": ManagedPrefix + "x/jobs/fugaro-sweep",
			"http_target": []any{map[string]any{"oauth_token": []any{map[string]any{"service_account_email": managedAccount}}}}}),
	} {
		t.Run(name, func(t *testing.T) {
			if out := managedTestCover().NotCovered(managedPlan(rc), nil); len(out) != 1 || !strings.HasPrefix(out[0], rc.Address+" (") || !strings.Contains(out[0], "[a-z0-9._-]") {
				t.Fatalf("gives %q, want one refusal naming the characters a name may carry", out)
			}
		})
	}
}

// refusedWith reports whether one refusal carries every part.
func refusedWith(out []string, parts ...string) bool {
	for _, o := range out {
		all := true
		for _, p := range parts {
			all = all && strings.Contains(o, p)
		}
		if all {
			return true
		}
	}
	return false
}
