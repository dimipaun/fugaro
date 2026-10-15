package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
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

// ------------------------------------------------- the rest of task 1.6 A

// setConfig rewrites the fixture's project config: the publisher reads it on
// every run, so a test changes what it publishes by changing the file.
func setConfig(t *testing.T, f *projectFixture, mutate func(string) string) {
	t.Helper()
	b, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.path, []byte(mutate(string(b))), 0o600); err != nil {
		t.Fatal(err)
	}
}

// nodeJSON is the published node's own bytes, as a run reads them.
func nodeJSON(t *testing.T, f *projectFixture, path string) string {
	t.Helper()
	v := f.db.Value(path)
	if v == nil {
		t.Fatalf("no node at %s", path)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestProjectPolicyPublishCarriesThePriceCatalogueSwitches is the trap the
// policy's Provider type was reshaped for: config.ModelProvider owns the YAML
// decoder, and a policy Provider that respelled require_zdr or price_policy
// would decode into the carrier and leave the respelled copy at zero — a
// switch silently dropped from the node a run prices itself with.
func TestProjectPolicyPublishCarriesThePriceCatalogueSwitches(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	p := publishedPolicy(t, f)
	pv, ok := p.Providers["openrouter"]
	if !ok {
		t.Fatalf("the published policy has no openrouter entry: %v", p.Providers)
	}
	if pv.RequireZDR == nil || *pv.RequireZDR {
		t.Errorf("require_zdr = %v, want false: the operator's switch did not reach the node", pv.RequireZDR)
	}
	if pv.PricePolicy != projectpolicy.PricePolicyHighest {
		t.Errorf("price_policy = %q, want highest", pv.PricePolicy)
	}
	// The node's own bytes carry both keys: a run reads the node, not this
	// struct.
	raw := nodeJSON(t, f, projectpolicy.PathShared())
	for _, want := range []string{`"require_zdr":false`, `"price_policy":"highest"`} {
		if !strings.Contains(raw, want) {
			t.Errorf("the published node does not carry %s:\n%s", want, raw)
		}
	}
	// And both come back out of `policy show`.
	out, _, err := execute(t, "project", "policy", "show")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "require_zdr false") || !strings.Contains(out, "price_policy highest") {
		t.Errorf("policy show does not print the switches:\n%s", out)
	}
}

// TestProjectPolicyPublishNeverSeedsFromTheBucket is B3's regression test. The
// bucket holds a run of a repository the operator's config does not name, at
// the slug task.Slug gives it; the seed must not turn that into an enrollment.
// Under phase 3's broadened runs/ grant every run of the profile can write
// those prefixes, so a seed from them would be a path by which an enrolled
// repository enrolls anything it likes.
func TestProjectPolicyPublishNeverSeedsFromTheBucket(t *testing.T) {
	f := newProjectFixture(t, "")
	slug, err := task.Slug("github", "acme/evil")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(f.dir, "runs", "runs", slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := `{"version":1,"run_id":"20261015-120000-aaaa","repo":"acme/evil","ref":"main","task":"enroll me"}`
	if err := os.WriteFile(filepath.Join(dir, "task.json"), []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "project", "policy", "publish", "--seed-repos"); err != nil {
		t.Fatal(err)
	}
	got := publishedPolicy(t, f).AllowRepos
	for _, r := range got {
		if strings.Contains(r, "acme/evil") {
			t.Fatalf("allow_repos = %v: the seed came from the bucket's runs/<slug>/ prefixes, which every run of the profile can write", got)
		}
	}
	if len(got) != 2 {
		t.Fatalf("allow_repos = %v, want exactly the operator's two repositories", got)
	}
}

// TestProjectPolicyPublishSeedIsOnlyTheOperatorsRepos: the seed is the
// operator's repos: map plus the shared config's repository list, and nothing
// else (P4).
func TestProjectPolicyPublishSeedIsOnlyTheOperatorsRepos(t *testing.T) {
	f := newProjectFixture(t, "")
	writeSharedConfig(t, f, "    acme/third:\n        provider: github\n        workflows:\n            - web\n")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	got := publishedPolicy(t, f).AllowRepos
	want := []string{"bitbucket/acme/other", "github/acme/app", "github/acme/third"}
	if len(got) != len(want) {
		t.Fatalf("allow_repos = %v, want %v (the config's two plus the shared config's one)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("allow_repos = %v, want %v", got, want)
		}
	}
}

// TestProjectPolicyPublishRefusesWithoutTheRulesMarker: a policy published
// before the rules are deployed is one the runs it bounds cannot read (P3).
func TestProjectPolicyPublishRefusesWithoutTheRulesMarker(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathRulesMarker(), nil)
	_, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatal("a publish without the rules marker was allowed")
	}
	if !strings.Contains(err.Error(), "init --firebase") {
		t.Errorf("the refusal does not name the fix: %v", err)
	}
	if f.db.Value(projectpolicy.PathShared()) != nil {
		t.Error("a node was written anyway")
	}
}

// TestProjectPolicyPublishRefusesAnEmptyBudgetMode: an unset key falls back to
// the job environment's value at run time, but a published budget block with
// no mode would take the mode from a deploy, which nobody reading the policy
// can see (H1). The decoder refuses such a node too.
func TestProjectPolicyPublishRefusesAnEmptyBudgetMode(t *testing.T) {
	f := newProjectFixture(t, "")
	setConfig(t, f, func(s string) string {
		return strings.Replace(s, "budget: { mode: enforce, per_run_usd: 5,", "budget: { per_run_usd: 5,", 1)
	})
	_, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatal("a publish with no budget.mode was allowed")
	}
	if !strings.Contains(err.Error(), "budget.mode") {
		t.Errorf("the refusal does not name budget.mode: %v", err)
	}
	if f.db.Value(projectpolicy.PathShared()) != nil {
		t.Error("a node was written anyway")
	}
}

// TestPolicyCarriesNoSecretValue: the node carries secret NAMES, never a
// value (12a item 7). The fake Secret Manager holds known bytes, and the
// publisher must not read them at all.
func TestPolicyCarriesNoSecretValue(t *testing.T) {
	f := newProjectFixture(t, "")
	sm := gcpfake.NewSecrets(t)
	const value = "sk-or-v1-0123456789abcdef0123456789abcdef"
	sm.Seed("openrouter-key", nil, []byte(value))
	setConfig(t, f, func(s string) string {
		return strings.Replace(s, "endpoints: { no_auth: true }", "endpoints: { no_auth: true, secret_manager: "+sm.URL+"/ }", 1)
	})
	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatal(err)
	}
	raw := nodeJSON(t, f, projectpolicy.PathShared())
	if strings.Contains(raw, value) {
		t.Fatalf("the published node holds a secret value:\n%s", raw)
	}
	if !strings.Contains(raw, `"secret":"openrouter-key"`) {
		t.Errorf("the published node does not carry the secret's name:\n%s", raw)
	}
	if got := sm.Accessed(); len(got) != 0 {
		t.Errorf("the publisher read the value of %v", got)
	}
	for _, cmd := range [][]string{{"project", "policy", "show"}, {"project", "show"}, {"project", "repos", "ls"}} {
		o, _, err := execute(t, cmd...)
		if err != nil {
			t.Fatalf("%v: %v", cmd, err)
		}
		if strings.Contains(o, value) || strings.Contains(out, value) {
			t.Errorf("%v printed a secret value:\n%s", cmd, o)
		}
	}
}

// TestProjectPolicyDeleteRemovesSharedThenChildren: the children go first and
// the shared node last (B5) — the shared node's existence is project-centric
// mode, so a deletion that half-succeeds must not leave the mode off with a
// child behind.
func TestProjectPolicyDeleteRemovesSharedThenChildren(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	slug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	child := projectpolicy.PathRepo(slug)
	f.db.Set(child, map[string]any{"allow_repos": []string{"github/acme/app"}})

	out, _, err := execute(t, "project", "policy", "publish", "--delete", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{projectpolicy.PathShared(), child} {
		if f.db.Value(p) != nil {
			t.Errorf("%s is still there", p)
		}
		if !strings.Contains(out, p) {
			t.Errorf("the deletion does not name %s:\n%s", p, out)
		}
	}
	if !strings.Contains(out, "no policy is published") {
		t.Errorf("the deletion does not say the mode is off again:\n%s", out)
	}
}

// TestProjectPolicyDeleteRefusesWithoutYesWhileARunIsInFlight: a repository
// that is mid-run keeps the policy until it finishes (B5).
func TestProjectPolicyDeleteRefusesWithoutYesWhileARunIsInFlight(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	slug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	f.db.Set(budget.PathAgent(slug, "20261015-120000-aaaa"), map[string]any{"run_id": "20261015-120000-aaaa"})

	_, _, err = execute(t, "project", "policy", "publish", "--delete")
	if err == nil {
		t.Fatal("--delete without --yes was allowed while a run is in flight")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("the refusal does not name --yes: %v", err)
	}
	if f.db.Value(projectpolicy.PathShared()) == nil {
		t.Fatal("the policy was removed anyway")
	}
	if _, _, err := execute(t, "project", "policy", "publish", "--delete", "--yes"); err != nil {
		t.Fatalf("--yes must delete while a run is in flight: %v", err)
	}
	if f.db.Value(projectpolicy.PathShared()) != nil {
		t.Fatal("--yes did not delete the policy")
	}
}

// TestProjectPolicyDeleteRefusesANewerPolicy: P8 in the other direction — this
// binary cannot say what a policy of a later shape means, so it must not
// remove it either.
func TestProjectPolicyDeleteRefusesANewerPolicy(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), map[string]any{"version": projectpolicy.Version + 1, "allow_repos": []string{"github/acme/app"}})
	_, _, err := execute(t, "project", "policy", "publish", "--delete", "--yes")
	if err == nil {
		t.Fatal("deleting a newer policy was allowed")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Errorf("the refusal does not name the version skew: %v", err)
	}
	if f.db.Value(projectpolicy.PathShared()) == nil {
		t.Fatal("the newer node was removed")
	}
}

// TestProjectShowPrintsEnrolledAndSeen: the pair an operator needs — a
// repository that is running and is not enrolled, and an enrolled one nothing
// here has run.
func TestProjectShowPrintsEnrolledAndSeen(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	appSlug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	f.db.Set(budget.PathAgent(appSlug, "20261015-120000-aaaa"), map[string]any{"run_id": "20261015-120000-aaaa"})
	const stranger = "stranger-0123456789abcdef"
	f.db.Set(budget.PathAgent(stranger, "20261015-120000-bbbb"), map[string]any{"run_id": "20261015-120000-bbbb"})

	out, _, err := execute(t, "project", "show")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"github/acme/app", "bitbucket/acme/other",
		"repos: acme/app",                  // the source of an allow-list entry
		"caps (the installation's ceiling", // step 3: the caps, global and defaults
		"global: per-run",
		"defaults: repo per-run",
		"repositories seen",
		stranger, // seen, and not invertible
		"not admitted",
		"bitbucket/acme/other is on the allow-list and has never run here",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("project show does not print %q:\n%s", want, out)
		}
	}
	// --json is the same document, for watch-style consumers.
	js, _, err := execute(t, "project", "show", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		AllowRepos        []string `json:"allow_repos"`
		Seen              []string `json:"seen"`
		SeenNotEnrolled   []string `json:"seen_not_enrolled"`
		EnrolledNeverSeen []string `json:"enrolled_never_seen"`
	}
	if err := json.Unmarshal([]byte(js), &doc); err != nil {
		t.Fatalf("--json is not JSON (%v):\n%s", err, js)
	}
	if len(doc.AllowRepos) != 2 || len(doc.Seen) != 2 {
		t.Errorf("--json = %s, want two allow-list entries and two seen repositories", js)
	}
	if len(doc.SeenNotEnrolled) != 1 || doc.SeenNotEnrolled[0] != stranger {
		t.Errorf("seen_not_enrolled = %v, want [%s]", doc.SeenNotEnrolled, stranger)
	}
	if len(doc.EnrolledNeverSeen) != 1 || doc.EnrolledNeverSeen[0] != "bitbucket/acme/other" {
		t.Errorf("enrolled_never_seen = %v, want [bitbucket/acme/other]", doc.EnrolledNeverSeen)
	}
}

// TestProjectShowRefusesAnUnknownPolicyKey: a key nobody reads is a policy
// that never takes effect, so the CLI refuses the node rather than showing a
// half-read policy (the runner is the tolerant side, P8).
func TestProjectShowRefusesAnUnknownPolicyKey(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version": projectpolicy.Version, "budget": map[string]any{"mode": "enforce"}, "per_run_usdc": 5,
	})
	_, _, err := execute(t, "project", "show")
	if err == nil {
		t.Fatal("an unknown top-level key was accepted")
	}
	if !strings.Contains(err.Error(), "per_run_usdc") {
		t.Errorf("the refusal does not name the key: %v", err)
	}
}

// TestProjectReposEditOnlyTheAllowList: add and rm rewrite allow_repos and
// nothing else, canonicalize the argument, print the change, and ask for the
// project's name to remove an entry — --yes does not stand in for it while a
// run is in flight (removing an allowed repository stops its runs).
func TestProjectReposEditOnlyTheAllowList(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	before := publishedPolicy(t, f)

	out, _, err := execute(t, "project", "repos", "add", "GitHub/Acme/New")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "github/acme/new") {
		t.Errorf("the diff does not name the canonical entry:\n%s", out)
	}
	got := publishedPolicy(t, f)
	if len(got.AllowRepos) != 3 || got.AllowRepos[2] != "github/acme/new" {
		t.Fatalf("allow_repos = %v, want the new entry appended in canonical order", got.AllowRepos)
	}
	// Only allow_repos changed: the rest of the node is what it was.
	if got.Version != before.Version || got.Budget.Mode != before.Budget.Mode || got.Budget.PerRunUSD != before.Budget.PerRunUSD ||
		len(got.Providers) != len(before.Providers) {
		t.Errorf("the edit changed more than allow_repos: %+v", got)
	}
	// A bare owner/name takes its provider from this machine's config.
	if _, _, err := execute(t, "project", "repos", "add", "acme/app"); err != nil {
		t.Fatal(err)
	}
	if got := publishedPolicy(t, f).AllowRepos; len(got) != 3 {
		t.Fatalf("allow_repos = %v: adding an entry already on the list changed it", got)
	}
	// A repository whose provider the config does not know is refused.
	if _, _, err := execute(t, "project", "repos", "add", "acme/stranger"); err == nil {
		t.Fatal("a bare owner/name the config does not list was accepted")
	}
	// rm asks for the project's name.
	if _, _, err := execute(t, "project", "repos", "rm", "github/acme/new"); err == nil {
		t.Fatal("rm without a terminal and without --yes was allowed")
	}
	if got := publishedPolicy(t, f).AllowRepos; len(got) != 3 {
		t.Fatalf("allow_repos = %v: a refused rm changed the node", got)
	}
	// While a run is in flight --yes alone is not the confirmation.
	slug, err := task.Slug("github", "acme/new")
	if err != nil {
		t.Fatal(err)
	}
	f.db.Set(budget.PathAgent(slug, "20261015-120000-aaaa"), map[string]any{"run_id": "20261015-120000-aaaa"})
	_, _, err = execute(t, "project", "repos", "rm", "github/acme/new", "--yes")
	if err == nil {
		t.Fatal("--yes removed an allowed repository while a run of it is in flight")
	}
	if !strings.Contains(err.Error(), "in flight") {
		t.Errorf("the refusal does not say why: %v", err)
	}
	if got := publishedPolicy(t, f).AllowRepos; len(got) != 3 {
		t.Fatalf("allow_repos = %v: the refused rm changed the node", got)
	}
	// Typed at a terminal, it goes.
	typed(t)
	out, _, err = executeStdin(t, "aurora\n", "project", "repos", "rm", "github/acme/new")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "github/acme/new") {
		t.Errorf("the diff does not name what it removed:\n%s", out)
	}
	if got := publishedPolicy(t, f).AllowRepos; len(got) != 2 {
		t.Fatalf("allow_repos = %v, want the entry removed", got)
	}
}

// writeSharedConfig writes the project's shared config into its runs bucket,
// the way fugaro config publish does, so a seed can read its repository list.
// It is a full published config because ParseShared validates one against the
// bucket it was read from.
func writeSharedConfig(t *testing.T, f *projectFixture, repos string) {
	t.Helper()
	s := `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: unused-bucket
registry_host: us-east5-docker.pkg.dev/proj-1234
base_images:
    base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:0.3.1
build:
    machine_type: E2_HIGHCPU_8
log_view: projects/proj-1234/locations/us-east5/buckets/fugaro-runs/views/fugaro-runs
budget:
    mode: observe
    per_run_usd: 5
    rtdb_url: https://proj-1234-default-rtdb.firebaseio.com
    firebase_project: proj-1234
    firebase_api_key: AIzaSyA0123456789abcdefghijklmnopqrstu
    token_signer: fugaro-token-signer@proj-1234.iam.gserviceaccount.com
max_parallel: 20
repos:
` + repos
	p := filepath.Join(f.dir, "runs", filepath.FromSlash(infra.SharedConfigObject))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}
