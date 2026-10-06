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
