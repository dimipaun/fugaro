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
// by their local names, each with the managed project written out as its
// project, and whose root module holds resources and calls.
func config(providers map[string]string, resources []map[string]any, calls map[string]any) map[string]any {
	pc := map[string]any{}
	for name, full := range providers {
		pc[name] = map[string]any{"name": name, "full_name": full,
			"expressions": map[string]any{"project": constant("proj-1")}}
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
		// And the same plan with an empty one: "{}" is a document
		// terraform's own `show -json` never writes, and read as a
		// configuration it has nothing to refuse — the provisioner, provider
		// and data-source rules below all pass, and the plan is taken to have
		// none, which is exactly what this function refuses the absence of.
		"an empty configuration": {[]map[string]any{bucket}, map[string]any{}, []string{"configuration"}},
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

// readPlanFixture reads one of testdata's plans through parsePlan, as Show
// reads terraform's output.
func readPlanFixture(t *testing.T, name string) *Plan {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	p, err := parsePlan(data)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestManagedCoverRefusesTerraformsOwnProviderSettings reads terraform's own
// output (terraform 1.16.4 and google 8.4.0, planned offline from the plugin
// cache with the token in the environment, so no setting below is the test's
// invention): a provider block in the root that impersonates Fugaro's
// scheduler and sends the storage API — and the bearer token with it — to an
// outside address, an aliased block whose impersonated account comes from an
// ephemeral resource, and a module's own block that bills another project and
// lands its resources in it. Each setting is refused for what it carries, and
// so is the resource the module block writes: it is in proj-2, which is not
// this run's project, and Task 1.2's project rule refuses it by address — as
// it refuses the block itself for the project it lands them in, which is the
// same rule read where a resource leaves the choice to its provider.
func TestManagedCoverRefusesTerraformsOwnProviderSettings(t *testing.T) {
	out := managedTestCover().NotCovered(readPlanFixture(t, "managed_provider_settings_plan.json"), nil)
	for _, want := range [][]string{
		{"provider_config.google (", "storage_custom_endpoint"},
		{"provider_config.google (", "impersonate_service_account"},
		{"provider_config.google.other (", "alias"},
		{"provider_config.google.other (", "impersonate_service_account"},
		{"provider_config.module.m:google (", "billing_project"},
		{"provider_config.module.m:google (", "user_project_override"},
		{"provider_config.module.m:google (", "lands resources in project proj-2"},
		{"configuration (references ephemeral.google_service_account_access_token.t:"},
		{"module.m.google_project_service.s (", "in project proj-2, which is not this run's"},
	} {
		if !refusedWith(out, want...) {
			t.Errorf("gives %q, want a refusal carrying %q", out, want)
		}
	}
	for _, o := range out {
		if !strings.HasPrefix(o, "provider_config.") && !strings.HasPrefix(o, "configuration (references ephemeral.") &&
			!strings.HasPrefix(o, "module.m.google_project_service.s (") {
			t.Errorf("refuses %q, which is neither a provider setting, the ephemeral resource, nor the module's own resource in proj-2", o)
		}
		if strings.Contains(o, "setting project") || strings.Contains(o, "setting region") {
			t.Errorf("refuses %q, an allowed setting", o)
		}
	}
	// The positive control from the same terraform: project, region, zone and
	// default labels written out.
	if out := managedTestCover().NotCovered(readPlanFixture(t, "managed_provider_clean_plan.json"), nil); len(out) != 0 {
		t.Errorf("the clean plan gives %q, want it covered", out)
	}
}

// providerPlan is a plan of one managed bucket whose configuration's google
// entry carries the given fields beside its name and full name. The root
// variables are the values terraform writes for every root variable of a real
// plan, var.p among them as proj-1: a row reading var.p is refused even
// though the plan shows that value, because the plan cannot show the block
// reads the variable bare (managedProviderProject).
func providerPlan(t *testing.T, fields map[string]any) *Plan {
	t.Helper()
	pc := map[string]any{"name": "google", "full_name": googleProvider}
	for k, v := range fields {
		pc[k] = v
	}
	cfg := config(nil, []map[string]any{bucketConfig()}, nil)
	cfg["provider_config"] = map[string]any{"google": pc}
	doc := map[string]any{"format_version": "1.2",
		"resource_changes": []map[string]any{bucketChange("google_storage_bucket.b", ManagedPrefix+"b", googleProvider)},
		"configuration":    cfg,
		"variables":        map[string]any{"p": map[string]any{"value": "proj-1"}}}
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

func constant(v any) map[string]any { return map[string]any{"constant_value": v} }

func refs(r ...string) map[string]any { return map[string]any{"references": r} }

// TestManagedCoverProviderSettings: a provider block decides who applies,
// who is billed and where every request — with the applier's bearer token —
// goes. A managed plan's provider blocks, in every module, set project,
// region, zone, default labels and the attribution label alone, the project
// always and written out, the others written out or read from a variable;
// every other setting, an alias and any field of the entry the cover does not
// know is refused, and so is a block that sets no project or reads it from a
// variable.
func TestManagedCoverProviderSettings(t *testing.T) {
	refused := map[string]map[string]any{
		"credentials":                           {"expressions": map[string]any{"credentials": constant("{}")}},
		"access_token":                          {"expressions": map[string]any{"access_token": refs("var.t")}},
		"impersonate_service_account":           {"expressions": map[string]any{"impersonate_service_account": constant("fugaro-scheduler@proj-1.iam.gserviceaccount.com")}},
		"impersonate_service_account_delegates": {"expressions": map[string]any{"impersonate_service_account_delegates": constant([]any{"a"})}},
		"universe_domain":                       {"expressions": map[string]any{"universe_domain": constant("attacker.example")}},
		"billing_project":                       {"expressions": map[string]any{"billing_project": constant("proj-2")}},
		"user_project_override":                 {"expressions": map[string]any{"user_project_override": constant(true)}},
		"storage_custom_endpoint":               {"expressions": map[string]any{"storage_custom_endpoint": constant("https://attacker.example/")}},
		"a block":                               {"expressions": map[string]any{"batching": []any{map[string]any{"send_after": constant("1s")}}}},
		"project from a local":                  {"expressions": map[string]any{"project": refs("local.p")}},
		"project from a resource":               {"expressions": map[string]any{"project": refs("google_storage_bucket.b.project", "google_storage_bucket.b")}},
		"project from an ephemeral":             {"expressions": map[string]any{"project": refs("ephemeral.x.y.z", "ephemeral.x.y")}},
		"project from a variable and a local":   {"expressions": map[string]any{"project": refs("var.p", "local.q")}},
		"project from a function":               {"expressions": map[string]any{"project": map[string]any{}}},
		"labels from a data source":             {"expressions": map[string]any{"default_labels": refs("data.x.y.labels", "data.x.y")}},
		"an alias":                              {"alias": "other", "expressions": map[string]any{"project": constant("proj-1")}},
		"a field the cover does not know":       {"future_setting": map[string]any{"x": 1}, "expressions": map[string]any{"project": constant("proj-1")}},
		"no project at all":                     {},
		"the allowed settings from variables": {"expressions": map[string]any{
			"project": refs("var.p"), "region": refs("var.r"), "zone": refs("var.z"), "default_labels": refs("var.l.x", "var.l")}},
	}
	for name, fields := range refused {
		t.Run("refuses "+name, func(t *testing.T) {
			if out := managedTestCover().NotCovered(providerPlan(t, fields), nil); !refusedWith(out, "provider_config.google (") {
				t.Fatalf("gives %q, want the provider block refused", out)
			}
		})
	}
	admitted := map[string]map[string]any{
		"the project alone": {"expressions": map[string]any{"project": constant("proj-1")}},
		"the allowed settings written out": {"version_constraint": "8.4.0", "expressions": map[string]any{
			"project": constant("proj-1"), "region": constant("us-east5"), "zone": constant("us-east5-a"),
			"default_labels": constant(map[string]any{"team": "a"}), "add_terraform_attribution_label": constant(true),
			"terraform_attribution_label_addition_strategy": constant("CREATION_ONLY")}},
		"the project written out and the rest from variables": {"expressions": map[string]any{
			"project": constant("proj-1"), "region": refs("var.r"), "zone": refs("var.z"), "default_labels": refs("var.l.x", "var.l")}},
		"a module's own block": {"module_address": "module.m", "expressions": map[string]any{"project": constant("proj-1")}},
	}
	for name, fields := range admitted {
		t.Run("admits "+name, func(t *testing.T) {
			if out := managedTestCover().NotCovered(providerPlan(t, fields), nil); len(out) != 0 {
				t.Fatalf("gives %q, want it covered", out)
			}
		})
	}
}

// TestManagedCoverRefusesWhatThePlanCarriesBesideResources: Terraform's
// action blocks run a provider's action at apply (action "local_command" runs
// a command on the machine that applies) and show only in the plan's
// action_invocations and the configuration's actions; a deferred change is a
// resource the plan has not shown yet. These fixtures are hand-written from
// the documented plan JSON format (terraform 1.14+); no cached provider
// carries an action. Any key the cover does not know, at the top of the plan,
// of the configuration or of a module, is refused when it carries anything,
// rather than read as inert.
func TestManagedCoverRefusesWhatThePlanCarriesBesideResources(t *testing.T) {
	bucket := bucketChange("google_storage_bucket.b", ManagedPrefix+"b", googleProvider)
	clean := func() map[string]any {
		return map[string]any{"format_version": "1.2", "terraform_version": "1.16.4",
			"resource_changes": []any{bucket},
			"configuration":    config(map[string]string{"google": googleProvider}, []map[string]any{bucketConfig()}, nil)}
	}
	invocation := []any{map[string]any{"address": "action.local_command.x", "type": "local_command", "name": "x",
		"provider_name": "registry.terraform.io/hashicorp/local", "config_values": map[string]any{"command": "true"},
		"lifecycle_action_trigger": map[string]any{"triggering_resource_address": "google_storage_bucket.b",
			"action_trigger_block_index": 0, "actions_list_index": 0, "action_trigger_event": "AfterCreate"}}}
	actionConfig := []any{map[string]any{"address": "action.local_command.x", "type": "local_command", "name": "x",
		"provider_config_key": "local", "expressions": map[string]any{"command": constant("true")}}}
	root := func(d map[string]any) map[string]any {
		return d["configuration"].(map[string]any)["root_module"].(map[string]any)
	}
	for name, tc := range map[string]struct {
		edit func(d map[string]any)
		want string
	}{
		"action_invocations":          {func(d map[string]any) { d["action_invocations"] = invocation }, "action_invocations"},
		"deferred_action_invocations": {func(d map[string]any) { d["deferred_action_invocations"] = invocation }, "deferred_action_invocations"},
		"deferred_changes": {func(d map[string]any) {
			d["deferred_changes"] = []any{map[string]any{"reason": "provider_config_unknown", "resource_change": bucket}}
		}, "deferred_changes"},
		"an unknown top-level key": {func(d map[string]any) { d["hooks"] = map[string]any{"x": 1} }, "hooks"},
		"actions in the root":      {func(d map[string]any) { root(d)["actions"] = actionConfig }, "actions"},
		"actions in a module": {func(d map[string]any) {
			root(d)["module_calls"] = map[string]any{"app": map[string]any{"source": "./app",
				"module": map[string]any{"actions": actionConfig}}}
		}, "module.app"},
		"an unknown configuration key": {func(d map[string]any) {
			d["configuration"].(map[string]any)["hooks"] = []any{1}
		}, "hooks"},
	} {
		t.Run(name, func(t *testing.T) {
			d := clean()
			tc.edit(d)
			data, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			p, err := parsePlan(data)
			if err != nil {
				t.Fatal(err)
			}
			if out := managedTestCover().NotCovered(p, nil); !refusedWith(out, tc.want) {
				t.Fatalf("gives %q, want a refusal naming %q", out, tc.want)
			}
		})
	}
	// The positive control: the keys terraform writes for an ordinary plan,
	// each carrying something, and the action keys and an unknown key present
	// but empty.
	d := clean()
	for k, v := range map[string]any{"variables": map[string]any{"p": map[string]any{"value": "x"}},
		"planned_values": map[string]any{"root_module": map[string]any{}},
		"prior_state":    map[string]any{"format_version": "1.0", "values": map[string]any{"root_module": map[string]any{}}},
		"resource_drift": []any{bucket}, "relevant_attributes": []any{map[string]any{"resource": "google_storage_bucket.b", "attribute": []any{"name"}}},
		"output_changes": map[string]any{"o": map[string]any{"actions": []any{"create"}, "after": "x"}},
		"checks":         []any{map[string]any{"address": map[string]any{"kind": "resource"}, "status": "pass"}},
		"timestamp":      "2026-10-17T00:00:00Z", "applyable": true, "complete": true,
		"errored": false, "action_invocations": []any{}, "deferred_changes": nil, "hooks": map[string]any{}} {
		d[k] = v
	}
	root(d)["actions"] = []any{}
	root(d)["outputs"] = map[string]any{"o": map[string]any{"expression": constant("x")}}
	root(d)["variables"] = map[string]any{"p": map[string]any{"default": "x"}}
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	p, err := parsePlan(data)
	if err != nil {
		t.Fatal(err)
	}
	if out := managedTestCover().NotCovered(p, nil); len(out) != 0 {
		t.Fatalf("an ordinary plan gives %q, want it covered", out)
	}
}

// TestManagedCoverRefusesEphemeralResources: an ephemeral resource is opened
// at apply with the applier's credentials (google_service_account_access_token
// mints a token for any account the applier may impersonate). Terraform
// leaves the block itself out of the plan's configuration, so the cover
// refuses it wherever it can show: as a change of mode "ephemeral", as a
// configuration entry of that mode, and as a reference from any expression of
// the configuration — a resource's, a module call's, a provider's.
func TestManagedCoverRefusesEphemeralResources(t *testing.T) {
	google := map[string]string{"google": googleProvider}
	bucket := bucketChange("google_storage_bucket.b", ManagedPrefix+"b", googleProvider)
	ephRefs := refs("ephemeral.google_service_account_access_token.t.access_token", "ephemeral.google_service_account_access_token.t")
	withExpr := func(r map[string]any, key string, v any) map[string]any {
		r["expressions"] = map[string]any{key: v}
		return r
	}
	for name, tc := range map[string]struct {
		changes []map[string]any
		config  map[string]any
		want    []string
	}{
		"a change of mode ephemeral": {[]map[string]any{bucket, {"address": "ephemeral.google_service_account_access_token.t",
			"mode": "ephemeral", "type": "google_service_account_access_token", "name": "t", "provider_name": googleProvider,
			"change": map[string]any{"actions": []any{"open"}, "after": map[string]any{}, "after_unknown": map[string]any{}}}},
			config(google, []map[string]any{bucketConfig()}, nil),
			[]string{"ephemeral.google_service_account_access_token.t (", "ephemeral"}},
		"a configuration entry of mode ephemeral": {[]map[string]any{bucket},
			config(google, []map[string]any{bucketConfig(), {"address": "ephemeral.google_service_account_access_token.t",
				"mode": "ephemeral", "type": "google_service_account_access_token", "name": "t", "provider_config_key": "google"}}, nil),
			[]string{"ephemeral.google_service_account_access_token.t (", "ephemeral"}},
		"a resource's expression": {[]map[string]any{bucket},
			config(google, []map[string]any{withExpr(bucketConfig(), "labels", map[string]any{"t": ephRefs})}, nil),
			[]string{"ephemeral.google_service_account_access_token.t"}},
		"a module call's expression": {[]map[string]any{bucket},
			config(google, []map[string]any{bucketConfig()}, map[string]any{"app": map[string]any{"source": "./app",
				"expressions": map[string]any{"token": ephRefs}, "module": map[string]any{}}}),
			[]string{"ephemeral.google_service_account_access_token.t"}},
	} {
		t.Run(name, func(t *testing.T) {
			if out := managedTestCover().NotCovered(jsonPlan(t, tc.changes, tc.config), nil); !refusedWith(out, tc.want...) {
				t.Fatalf("gives %q, want a refusal carrying %q", out, tc.want)
			}
		})
	}
	// The positive control: a resource expression with ordinary references.
	cfg := config(google, []map[string]any{withExpr(bucketConfig(), "location", refs("var.location"))}, nil)
	if out := managedTestCover().NotCovered(jsonPlan(t, []map[string]any{bucket}, cfg), nil); len(out) != 0 {
		t.Fatalf("gives %q, want it covered", out)
	}
}
