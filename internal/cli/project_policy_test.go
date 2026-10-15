package cli

import (
	"encoding/json"
	"net/http"
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

// policyConflictOnce changes the policy node in the fake just before the
// publisher's first PUT reaches it: another operator's publish landing between
// this one's read and its write. It is budget_test's conflictOnce for the
// policy node.
type policyConflictOnce struct {
	rt   http.RoundTripper
	db   *gcpfake.RTDB
	val  any
	done bool
}

func (c *policyConflictOnce) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodPut && !c.done {
		c.done = true
		c.db.Set(projectpolicy.PathShared(), c.val)
	}
	return c.rt.RoundTrip(r)
}

// TestProjectPolicyPublishRetriesAnETagConflict: the write is a
// compare-and-set, so a publish that lands in between is not overwritten with
// the values this one read before it. The publisher re-reads the node, re-diffs
// what it is about to replace, and writes again — the allow-list the other
// publish left stays what it is, instead of being re-seeded from the values
// read before the other publish existed.
func TestProjectPolicyPublishRetriesAnETagConflict(t *testing.T) {
	f := newProjectFixture(t, "")
	c := &policyConflictOnce{rt: http.DefaultTransport, db: f.db, val: map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "observe", "per_run_usd": 9},
		"allow_repos": []string{"github/acme/app"},
	}}
	budgetTransport = c
	t.Cleanup(func() { budgetTransport = nil })

	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatal(err)
	}
	if !c.done {
		t.Fatal("the conflict never happened: no write preceded the publisher's own")
	}
	// The retry's diff names the value it is actually replacing — the other
	// publish's $9, not this machine's $5 — and says why it is writing twice.
	if !strings.Contains(out, "changed by someone else meanwhile") || !strings.Contains(out, "$9") {
		t.Fatalf("the retry's output does not show the policy it re-read:\n%s", out)
	}
	p := publishedPolicy(t, f)
	if len(p.AllowRepos) != 1 || p.AllowRepos[0] != "github/acme/app" {
		t.Errorf("allow_repos = %v, want the node the other publish left: the publisher wrote the seed it computed before that node existed", p.AllowRepos)
	}
	if p.Budget.Mode != "enforce" || p.Budget.PerRunUSD != 5 {
		t.Errorf("budget = %+v, want this machine's config (the publisher's own keys are the ones it owns)", p.Budget)
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
	// An absent marker is not an old one: the refusal must not tell the
	// operator a marker is older than itself when there is no marker.
	if !strings.Contains(err.Error(), "carry no policy marker") || strings.Contains(err.Error(), "older than the marker") {
		t.Errorf("the refusal for an absent marker does not say so: %v", err)
	}
	if f.db.Value(projectpolicy.PathShared()) != nil {
		t.Error("a node was written anyway")
	}
}

// TestProjectPolicyPublishRefusesOlderRules: the marker exists and is behind
// this Fugaro's, which is the state an older CLI's init --firebase leaves
// behind. The refusal is the other one, and names the marker it found.
func TestProjectPolicyPublishRefusesOlderRules(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion-1)
	_, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatal("a publish behind the deployed rules was allowed")
	}
	if !strings.Contains(err.Error(), "older than the marker") || !strings.Contains(err.Error(), "init --firebase") {
		t.Errorf("the refusal does not say the rules are older: %v", err)
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

// TestProjectPolicyDeleteRemovesChildrenBeforeTheSharedNode: the children are
// deleted first and the shared node last, because the shared node is what makes
// project-centric mode (P3): the state a half-finished deletion must never
// leave behind is "no shared node, a repository's child still there", which is
// a project with no policy that still carries a layer. Refusing the write to
// the shared node is what makes the order observable — the children are gone,
// the node is not, and the command reports the refusal instead of printing a
// deletion that did not happen.
func TestProjectPolicyDeleteRemovesChildrenBeforeTheSharedNode(t *testing.T) {
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
	f.db.Deny(projectpolicy.PathShared())

	out, _, err := execute(t, "project", "policy", "publish", "--delete", "--yes")
	if err == nil {
		t.Fatalf("a refused delete of the shared node was reported as a success:\n%s", out)
	}
	if f.db.Value(child) != nil {
		t.Errorf("%s is still there: the children are deleted before the shared node", child)
	}
	if f.db.Value(projectpolicy.PathShared()) == nil {
		t.Errorf("%s is gone with a child behind it: that is the half-deletion the order exists to prevent, and the shared node is deleted last", projectpolicy.PathShared())
	}
	if strings.Contains(out, "no policy is published") {
		t.Errorf("the command says the policy is gone though the node is still there:\n%s", out)
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
		// The remedy is 'repos add', which takes a canonical name: a slug
		// cannot be one, so the note asks the operator to name it instead of
		// printing a command that would be refused.
		"name it",
		"bitbucket/acme/other is on the allow-list and has never run here",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("project show does not print %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "repos add "+stranger) {
		t.Errorf("project show tells the operator to run 'repos add %s', which the command refuses: a slug is not a canonical name:\n%s", stranger, out)
	}
	// --json is the same document, for watch-style consumers.
	js, _, err := execute(t, "project", "show", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		AllowRepos        []string          `json:"allow_repos"`
		Enrolled          map[string]string `json:"enrolled"`
		Seen              []string          `json:"seen"`
		SeenNotEnrolled   []string          `json:"seen_not_enrolled"`
		EnrolledNeverSeen []string          `json:"enrolled_never_seen"`
	}
	if err := json.Unmarshal([]byte(js), &doc); err != nil {
		t.Fatalf("--json is not JSON (%v):\n%s", err, js)
	}
	if len(doc.AllowRepos) != 2 || len(doc.Seen) != 2 {
		t.Errorf("--json = %s, want two allow-list entries and two seen repositories", js)
	}
	// enrolled is the allow-list with a source each, and nothing else.
	if len(doc.Enrolled) != len(doc.AllowRepos) {
		t.Errorf("enrolled = %v, want one entry per allow-list entry %v", doc.Enrolled, doc.AllowRepos)
	}
	for repo, want := range map[string]string{"github/acme/app": "repos: acme/app", "bitbucket/acme/other": "repos: acme/other"} {
		if doc.Enrolled[repo] != want {
			t.Errorf("enrolled[%s] = %q, want %q", repo, doc.Enrolled[repo], want)
		}
	}
	if len(doc.SeenNotEnrolled) != 1 || doc.SeenNotEnrolled[0] != stranger {
		t.Errorf("seen_not_enrolled = %v, want [%s]", doc.SeenNotEnrolled, stranger)
	}
	if len(doc.EnrolledNeverSeen) != 1 || doc.EnrolledNeverSeen[0] != "bitbucket/acme/other" {
		t.Errorf("enrolled_never_seen = %v, want [bitbucket/acme/other]", doc.EnrolledNeverSeen)
	}
}

// TestNotAdmittedNoteNamesARemedyThatWorks: 'fugaro project repos add' takes a
// canonical name, and a repository the registry knows only as a slug has none —
// task.Slug is a hash, so it cannot be turned back into one. The note that
// tells the operator how to enroll such a repository must not hand the command
// an argument the command refuses.
func TestNotAdmittedNoteNamesARemedyThatWorks(t *testing.T) {
	if got := notAdmittedNote("github/acme/app"); !strings.Contains(got, "fugaro project repos add github/acme/app") {
		t.Errorf("the note for a canonical name = %q, want the command with that name", got)
	}
	const slug = "stranger-0123456789abcdef"
	got := notAdmittedNote(slug)
	if strings.Contains(got, "repos add "+slug) {
		t.Errorf("the note = %q: 'repos add %s' is refused, so it is not a remedy", got, slug)
	}
	if !strings.Contains(got, "provider/owner/name") {
		t.Errorf("the note = %q, want the shape the operator has to supply", got)
	}
}

// TestProjectShowEnrolledIsTheAllowListNotTheSeed: the seed the publisher
// computes is every repository this machine's config names, which is wider than
// the published allow-list — 'repos rm' takes a name out of the policy and
// leaves the config alone. So enrolled is built from the allow-list the node
// carries, one key per entry: reporting a repository the policy does not admit
// as enrolled would say its runs are allowed when the next one is refused.
func TestProjectShowEnrolledIsTheAllowListNotTheSeed(t *testing.T) {
	newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "project", "repos", "rm", "bitbucket/acme/other", "--yes"); err != nil {
		t.Fatal(err)
	}
	js, _, err := execute(t, "project", "show", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		AllowRepos []string          `json:"allow_repos"`
		Enrolled   map[string]string `json:"enrolled"`
	}
	if err := json.Unmarshal([]byte(js), &doc); err != nil {
		t.Fatalf("--json is not JSON (%v):\n%s", err, js)
	}
	if len(doc.AllowRepos) != 1 || doc.AllowRepos[0] != "github/acme/app" {
		t.Fatalf("allow_repos = %v, want the one entry left after the removal", doc.AllowRepos)
	}
	if _, ok := doc.Enrolled["bitbucket/acme/other"]; ok {
		t.Errorf("enrolled = %v: a repository the policy does not admit is still in this machine's seed, and it is not enrolled", doc.Enrolled)
	}
	if len(doc.Enrolled) != 1 || doc.Enrolled["github/acme/app"] != "repos: acme/app" {
		t.Errorf("enrolled = %v, want exactly github/acme/app from the operator's repos:", doc.Enrolled)
	}
}

// TestProjectShowRefusesAnUnknownPolicyKey: a key nobody reads is a policy
// that never takes effect, so the CLI refuses the node rather than showing a
// half-read policy (the runner is the tolerant side, P8). Every reader of the
// node refuses it, so no two of them disagree on the posture.
func TestProjectShowRefusesAnUnknownPolicyKey(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version": projectpolicy.Version, "budget": map[string]any{"mode": "enforce"}, "per_run_usdc": 5,
	})
	for _, args := range [][]string{
		{"project", "show"},
		{"project", "show", "--json"},
		{"project", "policy", "show"},
		{"project", "repos", "ls"},
		{"project", "repos", "add", "github/acme/app"},
	} {
		_, _, err := execute(t, args...)
		if err == nil {
			t.Errorf("fugaro %s accepted an unknown top-level key", strings.Join(args, " "))
			continue
		}
		if !strings.Contains(err.Error(), "per_run_usdc") {
			t.Errorf("fugaro %s: the refusal does not name the key: %v", strings.Join(args, " "), err)
		}
	}
}

// nodeAt walks a decoded node to the value at path, and says whether it is
// there at all.
func nodeAt(v any, path ...string) (any, bool) {
	for _, k := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		if v, ok = m[k]; !ok {
			return nil, false
		}
	}
	return v, true
}

// someFuturePolicy is a node carrying keys this Fugaro does not know, in the
// two places Decode cannot list them: inside a providers entry and inside
// routing, both of which it decodes tolerantly so that a node a newer release
// wrote stays readable here (P8). The budget block and model_prices are strict
// — an unknown key there is refused rather than ignored — so a key of theirs is
// not the case this is about.
func someFuturePolicy() map[string]any {
	return map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app"},
		"providers": map[string]any{"openrouter": map[string]any{
			"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer",
			"secret": "openrouter-key", "models": []string{"deepseek/*"},
			"require_zdr": false, "price_policy": "highest", "future_zdr": true,
		}},
		"routing": map[string]any{"claude": map[string]any{"via": "auto", "future_route": "x"}},
	}
}

// TestProjectReposEditKeepsTheKeysItDoesNotKnow: `repos add|rm` changes one key
// of a document this Fugaro shares with releases that know more keys than it
// does. Decode lists the *top-level* keys it does not know (the command refuses
// those), but nothing can list the ones nested inside budget, providers.<name>
// or routing — and writing the decoded policy back drops them, which for a
// newer release's tightening key is a quiet loosening, in the one command whose
// help says only allow_repos changes.
func TestProjectReposEditKeepsTheKeysItDoesNotKnow(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), someFuturePolicy())

	// The keys this Fugaro knows and the ones it does not, all of which must
	// still be there after an edit.
	keep := [][]string{
		{"providers", "openrouter", "future_zdr"},
		{"providers", "openrouter", "require_zdr"},
		{"providers", "openrouter", "price_policy"},
		{"routing", "claude", "future_route"},
		{"routing", "claude", "via"},
	}
	check := func(what string) {
		t.Helper()
		node := f.db.Value(projectpolicy.PathShared())
		for _, path := range keep {
			if _, ok := nodeAt(node, path...); !ok {
				t.Errorf("%s dropped %s from the node: %s", what, strings.Join(path, "."), nodeJSON(t, f, projectpolicy.PathShared()))
			}
		}
	}
	check("the fixture")

	if _, _, err := execute(t, "project", "repos", "add", "github/acme/new"); err != nil {
		t.Fatal(err)
	}
	check("repos add")
	if got := publishedPolicy(t, f).AllowRepos; len(got) != 2 || got[1] != "github/acme/new" {
		t.Fatalf("allow_repos = %v, want the new entry", got)
	}

	typed(t)
	if _, _, err := executeStdin(t, "aurora\n", "project", "repos", "rm", "github/acme/new"); err != nil {
		t.Fatal(err)
	}
	check("repos rm")
	if got := publishedPolicy(t, f).AllowRepos; len(got) != 1 || got[0] != "github/acme/app" {
		t.Fatalf("allow_repos = %v, want the new entry removed", got)
	}
	// The value of the unknown key is the one that was planted, not a zero.
	if v, _ := nodeAt(f.db.Value(projectpolicy.PathShared()), "providers", "openrouter", "future_zdr"); v != true {
		t.Errorf("the provider's unknown key is %v, want true", v)
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
