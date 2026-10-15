package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// projectFixture is a project config the publisher can read: the RTDB fake as
// its budget backend, a local directory as its runs bucket, a budget block
// with a mode, one provider carrying both price-catalogue switches, and two
// repositories on two providers.
type projectFixture struct {
	db   *gcpfake.RTDB
	dir  string
	path string
}

// projectProvidersYAML is the operator's providers block. require_zdr and
// price_policy are the two price-catalogue switches (#361): the publisher
// must carry both into the policy, which is the trap this file's
// TestProjectPolicyPublishCarriesThePriceCatalogueSwitches exists for.
const projectProvidersYAML = "providers:\n" +
	"  openrouter:\n" +
	"    kind: anthropic-compat\n" +
	"    base_url: https://openrouter.ai/api/v1\n" +
	"    auth: bearer\n" +
	"    secret: openrouter-key\n" +
	"    models: [\"deepseek/*\"]\n" +
	"    require_zdr: false\n" +
	"    price_policy: highest\n"

func newProjectFixture(t *testing.T, extra string) *projectFixture {
	t.Helper()
	dir := t.TempDir()
	db := gcpfake.NewRTDB(t)
	path := isolateProjects(t, dir)
	cfg := "version: 1\n" +
		"name: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: unused-bucket\n" +
		"bucket_url: file://" + filepath.Join(dir, "runs") + "\n" +
		"user: someone@example.com\nmax_parallel: 2\n" +
		"budget: { mode: enforce, per_run_usd: 5, rtdb_url: " + db.URL + " }\n" +
		"endpoints: { no_auth: true }\n" +
		projectProvidersYAML +
		"repos:\n  acme/app: { provider: github, workflows: [web] }\n  acme/other: { provider: bitbucket, workflows: [web] }\n" +
		extra
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", path)
	db.Set("fugaro/project", "aurora")
	// The rules are deployed: publishing refuses without the marker.
	db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	writeRunsMarker(t, filepath.Join(dir, "runs"), "aurora", "proj-1234")
	return &projectFixture{db: db, dir: dir, path: path}
}

// writeRunsMarker writes the runs bucket's project marker, which the
// installation's Terraform writes and a cloud command checks.
func writeRunsMarker(t *testing.T, runsDir, name, gcpProject string) {
	t.Helper()
	data, err := json.Marshal(infra.ProjectMarker{Version: 1, Name: name, GCPProject: gcpProject})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(runsDir, filepath.FromSlash(infra.ProjectMarkerObject))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// publishedPolicy is the node the publisher wrote, decoded.
func publishedPolicy(t *testing.T, f *projectFixture) *projectpolicy.Policy {
	t.Helper()
	v := f.db.Value(projectpolicy.PathShared())
	if v == nil {
		t.Fatalf("no policy node at %s", projectpolicy.PathShared())
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	p, found, err := projectpolicy.Decode(b)
	if err != nil || !found {
		t.Fatalf("the published node does not decode (found=%v): %v", found, err)
	}
	return p
}

func TestProjectPolicyPublishWritesTheNodeAndSeedsTheAllowList(t *testing.T) {
	f := newProjectFixture(t, "")
	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatal(err)
	}
	p := publishedPolicy(t, f)
	if p.Version != projectpolicy.Version {
		t.Errorf("version = %d, want %d", p.Version, projectpolicy.Version)
	}
	if p.Budget.Mode != "enforce" || p.Budget.PerRunUSD != 5 {
		t.Errorf("budget = %+v, want the operator's mode and per-run ceiling", p.Budget)
	}
	want := []string{"bitbucket/acme/other", "github/acme/app"}
	if len(p.AllowRepos) != len(want) {
		t.Fatalf("allow_repos = %v, want %v", p.AllowRepos, want)
	}
	for i, r := range want {
		if p.AllowRepos[i] != r {
			t.Fatalf("allow_repos = %v, want %v (canonical, sorted)", p.AllowRepos, want)
		}
	}
	for _, r := range want {
		if !strings.Contains(out, r) {
			t.Errorf("the seeded allow-list does not name %s:\n%s", r, out)
		}
	}
}

func TestProjectPolicyPublishDoesNotReseedARemovedRepo(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	// The operator removes one name (what `project repos rm` does).
	p := publishedPolicy(t, f)
	var kept []string
	for _, r := range p.AllowRepos {
		if r != "github/acme/app" {
			kept = append(kept, r)
		}
	}
	p.AllowRepos = kept
	put(t, f, p)

	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if got := publishedPolicy(t, f).AllowRepos; len(got) != 1 || got[0] != "bitbucket/acme/other" {
		t.Fatalf("allow_repos = %v: a publish re-added a name the operator removed", got)
	}
	if _, _, err := execute(t, "project", "policy", "publish", "--seed-repos"); err != nil {
		t.Fatal(err)
	}
	if got := publishedPolicy(t, f).AllowRepos; len(got) != 2 {
		t.Fatalf("allow_repos = %v, want the seeded pair again after --seed-repos", got)
	}
}

func TestProjectPolicyPublishRefusesAVersionItCannotWrite(t *testing.T) {
	f := newProjectFixture(t, "")
	// A node written by a newer Fugaro.
	f.db.Set(projectpolicy.PathShared(), map[string]any{"version": projectpolicy.Version + 1, "allow_repos": []string{"github/acme/app"}})
	_, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatal("publishing over a newer policy was allowed")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Errorf("the refusal does not name the version skew: %v", err)
	}
	if v := f.db.Value(projectpolicy.PathShared()); v == nil {
		t.Fatal("the newer node was removed")
	}
}

// put writes p as the policy node, the way an earlier publish did.
func put(t *testing.T, f *projectFixture, p *projectpolicy.Policy) {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	f.db.Set(projectpolicy.PathShared(), v)
}
