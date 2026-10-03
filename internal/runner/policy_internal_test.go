package runner

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The default branch is fetched and read once per run: a later call gets
// the first answer, so the project check and the policy see the same file
// and the agent can't move it between them.
func TestDefaultBranchFileReadOnce(t *testing.T) {
	testutil.IsolateGit(t)
	files := testutil.FixtureFiles(t)
	remote := testutil.NewRemote(t, files)
	repo, err := gitops.OpenOrClone(context.Background(), filepath.Join(t.TempDir(), "w"), remote, gitops.WithVars(gitops.IdentityEnv(), nil))
	if err != nil {
		t.Fatal(err)
	}
	r := &run{repo: repo}
	ctx := context.Background()
	first, def, err := r.defaultBranchFile(ctx)
	if err != nil || def != "main" || string(first) != files["fugaro.yaml"] {
		t.Fatalf("%q, %q, %v", first, def, err)
	}
	seed := filepath.Join(t.TempDir(), "seed")
	testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", remote, seed)
	testutil.WriteFiles(t, seed, map[string]string{"fugaro.yaml": files["fugaro.yaml"] + "# moved\n"})
	testutil.Git(t, seed, "commit", "--quiet", "-am", "move")
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	second, _, err := r.defaultBranchFile(ctx)
	if err != nil || string(second) != string(first) {
		t.Fatalf("second read differs: %q, %v", second, err)
	}
}

// A tag (or local branch) named like the remote-tracking ref must not
// shadow the default branch's file.
func TestDefaultBranchFileNotShadowedByTag(t *testing.T) {
	testutil.IsolateGit(t)
	files := testutil.FixtureFiles(t)
	remote := testutil.NewRemote(t, files)
	dir := filepath.Join(t.TempDir(), "w")
	repo, err := gitops.OpenOrClone(context.Background(), dir, remote, gitops.WithVars(gitops.IdentityEnv(), nil))
	if err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "checkout", "--quiet", "-b", "evil")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": "# evil\n"})
	testutil.Git(t, dir, "commit", "--quiet", "-am", "evil")
	testutil.Git(t, dir, "tag", "origin/main")
	r := &run{repo: repo}
	got, _, err := r.defaultBranchFile(context.Background())
	if err != nil || string(got) != files["fugaro.yaml"] {
		t.Fatalf("got %q, %v", got, err)
	}
}

// FileLayer is the one construction of a fugaro.yaml's policy that the run
// and `fugaro validate` share: every policy key is in it.
func TestFileLayerCarriesEveryPolicyKey(t *testing.T) {
	cfg, ps := config.Parse([]byte("version: 1\nproject: x\ngit: { provider: github }\nagent:\n  max_run_tokens: 7\n  max_output_tokens: { coder: 11, reviewer: 13 }\n" +
		"budget: { mode: observe, per_run_usd: 3, per_day_usd: 9, allowed_models: [claude-haiku-4-5] }\nworkflows:\n  s: { base: java-services, commands: { build: make, test: make } }\n"))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	want := policy.Layer{Mode: "observe", PerRunUSD: 3, PerDayUSD: 9, MaxRunTokens: 7, MaxOutputCoder: 11, MaxOutputReviewer: 13, AllowedModels: []string{"claude-haiku-4-5"}}
	if got := FileLayer(cfg); !reflect.DeepEqual(got, want) {
		t.Errorf("FileLayer = %+v, want %+v", got, want)
	}
}

// result.json's policy object carries the day cap, and a record without one
// has no per_day_usd key.
func TestPolicyRecordCarriesPerDay(t *testing.T) {
	rec := policyRecord(policy.Effective{Layer: policy.Layer{PerDayUSD: 7.5},
		Sources: map[string]string{policy.KeyPerDayUSD: policy.SourceDefaultBranch}}, 0)
	if rec == nil || rec.Effective.PerDayUSD != 7.5 || rec.Sources["per_day_usd"] != "default-branch" {
		t.Fatalf("record = %+v", rec)
	}
	raw, err := json.Marshal(rec)
	if err != nil || !strings.Contains(string(raw), `"per_day_usd":7.5`) {
		t.Fatalf("json = %s, %v", raw, err)
	}
	other := policyRecord(policy.Effective{Layer: policy.Layer{PerRunUSD: 2}, Sources: map[string]string{policy.KeyPerRunUSD: policy.SourceCeiling}}, 0)
	if raw, _ := json.Marshal(other); strings.Contains(string(raw), "per_day_usd") {
		t.Fatalf("json = %s", raw)
	}
}
