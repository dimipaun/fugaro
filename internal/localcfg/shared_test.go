package localcfg

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files")

// golden compares got with testdata/<name>, rewriting it under -update.
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(got) {
		t.Errorf("%s differs (run with -update to rewrite):\n--- want\n%s\n--- got\n%s", path, want, got)
	}
}

const fullSample = `version: 1
name: aurora
gcp_project: my-project
region: us-central1
runs_bucket: my-runs
bucket_url: file:///somewhere/runs
registry: us-central1-docker.pkg.dev/my-project/fugaro
registry_host: us-central1-docker.pkg.dev/my-project
base_images:
  web-node: us-central1-docker.pkg.dev/my-project/fugaro-web-node/base:1
build:
  machine_type: E2_HIGHCPU_8
  service_account: fugaro-build@my-project.iam.gserviceaccount.com
terraform:
  state_bucket: my-state
  alert_email: ops@example.com
  launchers: [user:a@example.com]
  operators: [group:ops@example.com]
  budget_admins: [user:b@example.com]
  registry_cleanup: on
budget:
  mode: observe
  per_run_usd: 5
  rtdb_url: https://my-fp1-default-rtdb.firebaseio.com
  firebase_project: my-fp1
  firebase_api_key: AIzaFakeKeyForTests0123
  token_signer: signer@my-fp1.iam.gserviceaccount.com
watch:
  burn_alert_usd_per_hour: 2
user: someone@example.com
providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: ["deepseek/*"]
    allow_data_to: [acme/web]
max_parallel: 7
endpoints: { run: "http://127.0.0.1:1/", no_auth: true }
repos:
  acme/web: { provider: github, base_branch: main, workflows: [web] }
`

func fullLocalConfig(t *testing.T) *Config {
	t.Helper()
	lc, err := Parse([]byte(fullSample))
	if err != nil {
		t.Fatal(err)
	}
	return lc
}

func TestSharedSubsetNeverLeaksOwnerOrPersonalFields(t *testing.T) {
	lc := fullLocalConfig(t)
	out, err := lc.Shared().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"terraform:", "launchers", "alert_email", "user:", "endpoints:", "bucket_url", "registry:", "providers:", "openrouter", "base_branch", "service_account"} {
		if strings.Contains(string(out), forbidden) {
			t.Errorf("published file contains %q:\n%s", forbidden, out)
		}
	}
	golden(t, "shared.golden.yaml", out)
	if _, err := Parse(out); err != nil {
		t.Errorf("published file does not parse: %v", err)
	}
	// The receiver is untouched.
	if lc.Terraform.StateBucket == "" || lc.User == "" || lc.Bucket == "" || len(lc.Providers) != 1 || lc.Repos["acme/web"].BaseBranch != "main" || lc.Build.ServiceAccount == "" {
		t.Errorf("Shared modified its receiver: %+v", lc)
	}
}

func sharedBase(t *testing.T, extra string) *Config {
	t.Helper()
	c, err := Parse([]byte(`version: 1
name: aurora
gcp_project: my-project
region: us-central1
runs_bucket: my-runs
` + extra))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMergeSharedAdopterKeepsPublishedRepos(t *testing.T) {
	existing := sharedBase(t, "repos:\n  acme/web: { provider: github, base_branch: main, workflows: [web] }\n")
	local := sharedBase(t, "repos: {}\n")
	got := MergeShared(existing, local)
	if _, ok := got.Repos["acme/web"]; !ok || len(got.Repos) != 1 {
		t.Errorf("repos = %v, want the published acme/web", got.Repos)
	}
}

func TestMergeSharedLocalRepoWinsAndNewIsAdded(t *testing.T) {
	existing := sharedBase(t, "repos:\n  acme/web: { provider: github, base_branch: main, workflows: [web] }\n  acme/old: { provider: github, base_branch: main, workflows: [old] }\n")
	local := sharedBase(t, "repos:\n  acme/web: { provider: github, base_branch: dev, workflows: [api] }\n  acme/new: { provider: github, base_branch: main, workflows: [new] }\n")
	got := MergeShared(existing, local)
	if got.Repos["acme/web"].Workflows[0] != "api" || got.Repos["acme/old"].Workflows[0] != "old" || got.Repos["acme/new"].Workflows[0] != "new" || len(got.Repos) != 3 {
		t.Errorf("repos = %+v", got.Repos)
	}
	// base_branch is never published: the checkout's reviewed fugaro.yaml
	// names it. Neither side's comes through, and the inputs keep theirs.
	for k, r := range got.Repos {
		if r.BaseBranch != "" {
			t.Errorf("repos.%s.base_branch = %q was merged", k, r.BaseBranch)
		}
	}
	if local.Repos["acme/web"].BaseBranch != "dev" || existing.Repos["acme/old"].BaseBranch != "main" {
		t.Error("MergeShared cleared an input's base_branch")
	}
}

func TestMergeSharedScalars(t *testing.T) {
	existing := sharedBase(t, "log_view: projects/my-project/locations/global/buckets/bkt/views/vw\nscheduler_region: us-east4\nmax_parallel: 5\nwatch: { burn_alert_usd_per_hour: 3 }\nbase_images: { web-node: ex.example/a:1, go: ex.example/g:1 }\n")
	local := sharedBase(t, "scheduler_region: us-central1\nmax_parallel: 9\nbase_images: { web-node: ex.example/b:2 }\n")
	got := MergeShared(existing, local)
	if got.SchedulerRegion != "us-central1" || got.MaxParallel != 9 {
		t.Errorf("local non-zero must win: %q %d", got.SchedulerRegion, got.MaxParallel)
	}
	if got.LogView == "" || got.Watch == nil {
		t.Errorf("local zero must keep the existing: %q %v", got.LogView, got.Watch)
	}
	if got.BaseImages["web-node"] != "ex.example/b:2" || got.BaseImages["go"] != "ex.example/g:1" {
		t.Errorf("base_images = %v", got.BaseImages)
	}
}

func TestMergeSharedForeignAndNil(t *testing.T) {
	foreign := sharedBase(t, "repos:\n  x/y: { provider: github, base_branch: main, workflows: [web] }\n")
	foreign.GCPProject = "other-project"
	local := sharedBase(t, "")
	if got := MergeShared(foreign, local); len(got.Repos) != 0 {
		t.Errorf("a foreign object was merged: %v", got.Repos)
	}
	renamed := sharedBase(t, "repos:\n  x/y: { provider: github, base_branch: main, workflows: [web] }\n")
	renamed.Name = "other"
	if got := MergeShared(renamed, local); len(got.Repos) != 0 {
		t.Errorf("a foreign name was merged: %v", got.Repos)
	}
	if got := MergeShared(nil, local); got.Name != "aurora" {
		t.Errorf("nil existing: %+v", got)
	}
}

func TestMergeSharedDoesNotMutateInputsOrLeak(t *testing.T) {
	existing := sharedBase(t, "repos:\n  acme/web: { provider: github, base_branch: main, workflows: [web] }\n")
	local := fullLocalConfig(t)
	eBefore, _ := existing.Marshal()
	lBefore, _ := local.Marshal()
	got := MergeShared(existing, local)
	eAfter, _ := existing.Marshal()
	lAfter, _ := local.Marshal()
	if string(eBefore) != string(eAfter) || string(lBefore) != string(lAfter) {
		t.Error("MergeShared mutated an input")
	}
	if len(local.Repos) != 1 || len(existing.Repos) != 1 {
		t.Errorf("input repos changed: %v %v", local.Repos, existing.Repos)
	}
	out, _ := got.Marshal()
	for _, f := range []string{"terraform:", "user:", "endpoints:", "bucket_url", "registry:", "providers:"} {
		if strings.Contains(string(out), f) {
			t.Errorf("merged output contains %q", f)
		}
	}
}

// TestMergeSharedNeverCarriesProviders: providers are local-only (a model
// provider's base_url is where a key and code go), so neither the local
// nor the published ones reach the output.
func TestMergeSharedNeverCarriesProviders(t *testing.T) {
	block := "providers:\n  openrouter:\n    kind: anthropic-compat\n    base_url: https://openrouter.ai/api\n    auth: bearer\n    secret: openrouter-api-key\n    models: [\"deepseek/*\"]\n"
	existing := sharedBase(t, block)
	local := sharedBase(t, strings.ReplaceAll(strings.ReplaceAll(block, "openrouter", "other"), "deepseek", "qwen"))
	if len(existing.Providers) != 1 || len(local.Providers) != 1 {
		t.Fatalf("fixtures: %v %v", existing.Providers, local.Providers)
	}
	for name, got := range map[string]*Config{"merged": MergeShared(existing, local), "nil published": MergeShared(nil, local), "published only": MergeShared(existing, sharedBase(t, ""))} {
		if got.Providers != nil {
			t.Errorf("%s: providers = %v", name, got.Providers)
		}
		out, _ := got.Marshal()
		if strings.Contains(string(out), "providers") {
			t.Errorf("%s: output carries providers:\n%s", name, out)
		}
	}
	if len(local.Providers) != 1 {
		t.Error("MergeShared cleared the local providers")
	}
}

// TestSharedDropsBaseBranchAndBuildAccount: Shared() clears every repo's
// base_branch and the deprecated build.service_account, without touching
// the receiver.
func TestSharedDropsBaseBranchAndBuildAccount(t *testing.T) {
	lc := fullLocalConfig(t)
	s := lc.Shared()
	if s.Repos["acme/web"].BaseBranch != "" || s.Repos["acme/web"].Provider != "github" || s.Build.ServiceAccount != "" || s.Build.MachineType != "E2_HIGHCPU_8" {
		t.Errorf("shared = %+v %+v", s.Repos, s.Build)
	}
	if lc.Repos["acme/web"].BaseBranch != "main" || lc.Build.ServiceAccount == "" {
		t.Errorf("the receiver changed: %+v %+v", lc.Repos, lc.Build)
	}
	m := MergeShared(sharedBase(t, "build: { machine_type: E2_HIGHCPU_8 }\n"), lc)
	if m.Build.ServiceAccount != "" {
		t.Errorf("merged build = %+v", m.Build)
	}
}
