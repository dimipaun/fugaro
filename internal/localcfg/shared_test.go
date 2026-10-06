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
	for _, forbidden := range []string{"terraform:", "launchers", "alert_email", "user:", "endpoints:", "bucket_url", "registry:"} {
		if strings.Contains(string(out), forbidden) {
			t.Errorf("published file contains %q:\n%s", forbidden, out)
		}
	}
	golden(t, "shared.golden.yaml", out)
	if _, err := Parse(out); err != nil {
		t.Errorf("published file does not parse: %v", err)
	}
	// The receiver is untouched.
	if lc.Terraform.StateBucket == "" || lc.User == "" || lc.Bucket == "" {
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
	existing := sharedBase(t, "repos:\n  acme/web: { provider: github, base_branch: main, workflows: [web] }\n  acme/old: { provider: github, base_branch: main, workflows: [web] }\n")
	local := sharedBase(t, "repos:\n  acme/web: { provider: github, base_branch: dev, workflows: [web] }\n  acme/new: { provider: github, base_branch: main, workflows: [web] }\n")
	got := MergeShared(existing, local)
	if got.Repos["acme/web"].BaseBranch != "dev" || got.Repos["acme/old"].BaseBranch != "main" || got.Repos["acme/new"].BaseBranch != "main" || len(got.Repos) != 3 {
		t.Errorf("repos = %+v", got.Repos)
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
	for _, f := range []string{"terraform:", "user:", "endpoints:", "bucket_url", "registry:"} {
		if strings.Contains(string(out), f) {
			t.Errorf("merged output contains %q", f)
		}
	}
}
