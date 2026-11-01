package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rigor"
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
	// fs is the project's spend history: the fixture's budget block names a
	// Firebase project, so every reader that reaches for spendDaily reaches
	// for this fake rather than for the real Firestore.
	fs *gcpfake.Firestore
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
	return newProjectFixtureWith(t, projectProvidersYAML, extra)
}

// claudeRouteProvidersYAML is the operator's providers block with a route entry
// that claims one Claude ID — the shape the routing switch resolves through
// (docs/design/mixed-credentials.md §4: one explicit "anthropic/<id>", never a
// pattern). The claim is a second provider entry, so the rest of the fixture's
// providers are unchanged.
const claudeRouteProvidersYAML = projectProvidersYAML +
	"  openrouter-claude:\n" +
	"    kind: anthropic-compat\n" +
	"    base_url: https://openrouter.ai/api/v1\n" +
	"    auth: bearer\n" +
	"    secret: openrouter-claude-key\n" +
	"    models: [\"anthropic/claude-sonnet-5.5\"]\n" +
	"    allow_data_to: [\"*\"]\n"

// twoClaudeModelsProvidersYAML is claudeRouteProvidersYAML with the route entry
// claiming a second Claude ID. A node whose switch names that ID still decodes
// (validRouting resolves every ID the list names), which is what lets a publish
// replace a switch that was already there with the one the config writes.
const twoClaudeModelsProvidersYAML = projectProvidersYAML +
	"  openrouter-claude:\n" +
	"    kind: anthropic-compat\n" +
	"    base_url: https://openrouter.ai/api/v1\n" +
	"    auth: bearer\n" +
	"    secret: openrouter-claude-key\n" +
	"    models: [\"anthropic/claude-sonnet-5.5\", \"anthropic/claude-opus-5.5\"]\n" +
	"    allow_data_to: [\"*\"]\n"

// projectConfigText is the fixture's whole config file: the installation the
// publisher needs, this machine's providers block, two repositories and
// whatever a test adds on top.
func projectConfigText(dir, dbURL, providers, extra string) string {
	return "version: 1\n" +
		"name: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: unused-bucket\n" +
		"bucket_url: file://" + filepath.Join(dir, "runs") + "\n" +
		"user: someone@example.com\nmax_parallel: 2\n" +
		"budget: { mode: enforce, per_run_usd: 5, firebase_project: proj-1234, rtdb_url: " + dbURL + " }\n" +
		"endpoints: { no_auth: true }\n" +
		providers +
		"repos:\n  acme/app: { provider: github, workflows: [web] }\n  acme/other: { provider: bitbucket, workflows: [web] }\n" +
		extra
}

// rewriteProjectConfig replaces the fixture's config with one whose only
// difference is extra: a second publish of the same installation from a
// machine whose file says something else — which is the state a publish's
// handling of a member the file is silent about is about.
func rewriteProjectConfig(t *testing.T, f *projectFixture, extra string) {
	t.Helper()
	if err := os.WriteFile(f.path, []byte(f.configText(t, projectConfigText(f.dir, f.db.URL, projectProvidersYAML, extra))), 0o600); err != nil {
		t.Fatal(err)
	}
}

// configText is projectConfigText with the fixture's own endpoints block: the
// Firestore fake the budget block's Firebase project is read through. Every
// writer of the fixture's config goes through it, so a rewritten config keeps
// the spend history the readers reach for.
func (f *projectFixture) configText(t *testing.T, text string) string {
	t.Helper()
	return strings.Replace(text, "endpoints: { no_auth: true }", "endpoints: { firestore: "+f.fs.URL+", no_auth: true }", 1)
}

func newProjectFixtureWith(t *testing.T, providers, extra string) *projectFixture {
	t.Helper()
	dir := t.TempDir()
	db, fs := gcpfake.NewRTDB(t), gcpfake.NewFirestore(t)
	path := isolateProjects(t, dir)
	f := &projectFixture{db: db, dir: dir, path: path, fs: fs}
	if err := os.WriteFile(path, []byte(f.configText(t, projectConfigText(dir, db.URL, providers, extra))), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", path)
	db.Set("fugaro/project", "aurora")
	// The mark `fugaro update --firebase-project` writes: the database is this
	// installation's, which is what every writer into it asks before it writes
	// (requireOwnBudgetDB, a call of budgetDBUnadopted).
	db.Set(budget.PathMark, auroraMark())
	// The rules are deployed: publishing refuses without the marker.
	db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	writeRunsMarker(t, filepath.Join(dir, "runs"), "aurora", "proj-1234")
	return f
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
// writer's first write reaches it: another operator's publish landing between
// this one's read and its write. It is budget_test's conflictOnce for the
// policy node.
//
// Any write counts, not only a PUT: a writer that reached the node with an
// unconditional PATCH is exactly the mutant a compare-and-set test exists to
// catch, and the conflict has to be planted before that write for the test to
// see the overwrite instead of a "no write was attempted" sentinel.
type policyConflictOnce struct {
	rt   http.RoundTripper
	db   *gcpfake.RTDB
	val  any
	done bool
}

func (c *policyConflictOnce) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet && !c.done {
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

// policyWriteFails makes every write to the budget database fail at the
// transport: a hard write failure, not the compare-and-set conflict a retry
// handles. Reads pass through, so the publisher gets as far as its write.
type policyWriteFails struct{ rt http.RoundTripper }

func (c policyWriteFails) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet {
		return nil, errors.New("connection reset by peer")
	}
	return c.rt.RoundTrip(r)
}

// TestProjectPolicyPublishPrintsNoDiffItDidNotWrite: the diff is a claim about
// the node, so it goes out after the write that makes it true. On a write that
// failed the operator reads the failure and that the node is as it was, not a
// full diff of a policy the node does not hold — a diff printed first reads as
// a change that happened (the review finding on this change; `project repos
// edit` prints its line after its write for the same reason,
// TestProjectReposEditRefusesAConcurrentWriter).
//
// "The node is as it was" and not "nothing was written" is deliberate, and it
// is the other half of the review finding this change answers: the admissions
// are a second write, narrowed before this one, and a failure here does not
// undo them — so the sentence names what *this* write did not do, and the
// admissions are named beside it when there were any (this fixture has none;
// TestPolicyAdmissionsAreTightenedBeforeTheSharedNode is the case with them).
func TestProjectPolicyPublishPrintsNoDiffItDidNotWrite(t *testing.T) {
	f := newProjectFixture(t, "")
	budgetTransport = policyWriteFails{rt: http.DefaultTransport}
	t.Cleanup(func() { budgetTransport = nil })

	out, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatal("a write the database refused was reported as a successful publish")
	}
	if !strings.Contains(err.Error(), projectpolicy.PathShared()+" is as it was") {
		t.Errorf("err = %v, want it to say the node is as it was", err)
	}
	if strings.Contains(err.Error(), "nothing was written") {
		t.Errorf("err = %v claims nothing was written: the admissions are another write, and this message does not know they were not made", err)
	}
	for _, marker := range []string{"not published yet", "allow_repos", "set "} {
		if strings.Contains(out, marker) {
			t.Errorf("the failed publish printed %q:\n%s", marker, out)
		}
	}
	if got := f.db.Value(projectpolicy.PathShared()); got != nil {
		t.Errorf("%s = %v, want the node the failed write left alone (nothing)", projectpolicy.PathShared(), got)
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

// The delete is refused by the same check, before the branch that would
// remove it: this command may neither overwrite nor delete a node a newer
// Fugaro wrote, so the older binary's whole remedy for a node ahead of it is
// the upgrade (projectpolicy.CLISkew). Pinned because the sentence there says
// so — a reader who was told to try the delete and found this refusal would
// have been sent in a circle, which is the fault the older-node direction had.
func TestProjectPolicyPublishDeleteRefusesANewerNode(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), map[string]any{"version": projectpolicy.Version + 1, "allow_repos": []string{"github/acme/app"}})
	_, _, err := execute(t, "project", "policy", "publish", "--delete", "--yes")
	if err == nil {
		t.Fatal("deleting a policy written by a newer Fugaro was allowed")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Errorf("the refusal does not name the version skew: %v", err)
	}
	if v := f.db.Value(projectpolicy.PathShared()); v == nil {
		t.Fatal("the newer node was removed")
	}
}

// TestProjectPolicyPublishRefusesAProfileMountingAClaudeOAuthToken: publish
// copies the operator's profiles into the node verbatim, so the policy's own
// rule (projectpolicy.validProfiles) is the only thing that can refuse a
// profile here — and it must, because the converge refuses to deploy the job
// such a profile names (internal/infra's projectJobSecrets). One rule for both
// is what keeps a published profile from naming a job that does not exist: the
// launcher would compute the account segment of that job (the default account)
// where the deployer deploys the empty one, and call Cloud Run on a 404, and
// the runner would match no candidate and halt every run of the profile.
func TestProjectPolicyPublishRefusesAProfileMountingAClaudeOAuthToken(t *testing.T) {
	f := newProjectFixture(t, "profiles: { standard: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h, secrets: [claude-oauth-token] } }\n")
	_, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatal("publishing a profile mounting the reserved claude-oauth-token was allowed")
	}
	for _, want := range []string{"person-owned", "phase 5"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if f.db.Value(projectpolicy.PathShared()) != nil {
		t.Error("a node was written anyway")
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

// TestProjectPolicyPublishModelPricesRoundTrip: the model IDs the documentation
// uses carry a "/" (deepseek/deepseek-v4-flash, docs/multi-model.md) or a "."
// (deepseek/deepseek-v4.1-flash), and an RTDB key may hold neither — the write
// is refused whole, with a 400, and the fake emulates that (gcpfake.rtdb's
// firstBadKey). So a publish of a documented price list has to store the block
// under a key RTDB accepts and read it back as the model IDs the operator
// wrote, which is what this walks end to end: publish, the node's own bytes,
// the decoded policy, and both show commands.
func TestProjectPolicyPublishModelPricesRoundTrip(t *testing.T) {
	const slash, dot = "deepseek/deepseek-v4-flash", "deepseek/deepseek-v4.1-flash"
	f := newProjectFixture(t, "model_prices:\n"+
		"  "+slash+": { input_per_m: 0.28, output_per_m: 0.42 }\n"+
		"  "+dot+": { input_per_m: 1, output_per_m: 2, cache_read: 0.1 }\n")
	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatalf("publishing a documented model ID was refused: %v\n%s", err, out)
	}

	// The node's own bytes: every model_prices member name is one RTDB takes.
	raw := nodeJSON(t, f, projectpolicy.PathShared())
	var node struct {
		Prices map[string]json.RawMessage `json:"model_prices"`
	}
	if err := json.Unmarshal([]byte(raw), &node); err != nil {
		t.Fatalf("the published node is not JSON (%v):\n%s", err, raw)
	}
	if len(node.Prices) != 2 {
		t.Fatalf("the node's model_prices has %d members, want 2:\n%s", len(node.Prices), raw)
	}
	for key := range node.Prices {
		if strings.ContainsAny(key, ".$#[]/") {
			t.Errorf("the node holds the key %q, which RTDB refuses (a key may not contain . $ # [ ] /):\n%s", key, raw)
		}
	}
	if strings.Contains(raw, `"`+slash+`"`) {
		t.Errorf("the node holds the model ID itself as a key:\n%s", raw)
	}

	// And it decodes back to the IDs the operator wrote.
	p := publishedPolicy(t, f)
	for _, id := range []string{slash, dot} {
		if _, ok := p.Prices[id]; !ok {
			t.Errorf("the decoded policy has no price for %q: %v", id, p.Prices)
		}
	}
	if v := p.Prices[dot].CacheRead; v == nil || *v != 0.1 {
		t.Errorf("the dot-carrying entry did not come back whole: %+v", p.Prices[dot])
	}

	// The diff names the model, and so do both show commands: an operator
	// reads model IDs, not the node's escape.
	if !strings.Contains(out, "model_prices "+slash) {
		t.Errorf("the publish diff does not name the model it priced:\n%s", out)
	}
	for _, args := range [][]string{{"project", "show"}, {"project", "policy", "show"}} {
		out, _, err := execute(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(out, "model_prices "+slash) {
			t.Errorf("%v does not print the model ID %q:\n%s", args, slash, out)
		}
	}
	js, _, err := execute(t, "project", "show", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Policy struct {
			Prices map[string]projectpolicy.Price `json:"model_prices"`
		} `json:"policy"`
	}
	if err := json.Unmarshal([]byte(js), &doc); err != nil {
		t.Fatalf("--json is not JSON (%v):\n%s", err, js)
	}
	if _, ok := doc.Policy.Prices[slash]; !ok {
		t.Errorf("--json's policy.model_prices is not keyed by the model ID: %v", doc.Policy.Prices)
	}
}

// TestProjectPolicyPublishNeverSeedsFromTheBucket is B3's regression test. The
// bucket holds runs of repositories the operator's config does not name, at
// the slugs task.Slug gives them; the seed must not turn those into
// enrollments. Under phase 3's broadened runs/ grant every run of the profile
// can write those prefixes, so a seed from them would be a path by which an
// enrolled repository enrolls anything it likes.
//
// Both layouts are planted. The one a run writes is
// runs/<slug>/<run-id>/task.json (internal/task Spec's own doc); the flat
// runs/<slug>/task.json is what an object laid down by hand — or by anything
// else holding the bucket — looks like, and it is the one an early version of
// this test planted alone, which let a seed that walked the real layout pass.
func TestProjectPolicyPublishNeverSeedsFromTheBucket(t *testing.T) {
	f := newProjectFixture(t, "")
	plantRun := func(repo, runID, sub string) {
		t.Helper()
		slug, err := task.Slug("github", repo)
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(f.dir, "runs", "runs", slug)
		if sub != "" {
			dir = filepath.Join(dir, sub)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		spec := `{"version":1,"run_id":"` + runID + `","repo":"` + repo + `","ref":"main","task":"enroll me"}`
		if err := os.WriteFile(filepath.Join(dir, "task.json"), []byte(spec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	plantRun("acme/evil", "20261015-120000-aaaa", "")                       // the flat layout
	plantRun("acme/sneaky", "20261015-120001-bbbb", "20261015-120001-bbbb") // the real one
	if _, _, err := execute(t, "project", "policy", "publish", "--seed-repos"); err != nil {
		t.Fatal(err)
	}
	got := publishedPolicy(t, f).AllowRepos
	for _, r := range got {
		if strings.Contains(r, "acme/evil") || strings.Contains(r, "acme/sneaky") {
			t.Fatalf("allow_repos = %v: the seed came from the bucket's runs/<slug>/ prefixes, which every run of the profile can write", got)
		}
	}
	if len(got) != 2 {
		t.Fatalf("allow_repos = %v, want exactly the operator's two repositories", got)
	}
}

// TestProjectShowReadsASharedConfigOfANonDefaultImageSource: the shared config
// is read with an anchor that includes the image_source this machine records,
// so an installation that took a fork's base pin (ghcr.io/acme rather than the
// default ghcr.io/dimipaun) still reads the published document. Omitting the
// field there refused the document, and that refusal is a hard error in every
// command that seeds the allow-list — `project show`, `project repos ls` and
// the `project policy publish` that is the only CLI remedy, so there was no
// path out.
func TestProjectShowReadsASharedConfigOfANonDefaultImageSource(t *testing.T) {
	f := newProjectFixtureWith(t, "", "image_source: ghcr.io/acme\n")
	writeSharedConfigWithSource(t, f, "ghcr.io/acme", "    acme/third:\n        provider: github\n        workflows:\n            - web\n")
	// The publish writes the allow-list from the same seed, and it is the
	// only CLI remedy an operator has, so it is the first half of the path.
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatalf("project policy publish refused a shared config published by this machine's own image_source: %v", err)
	}
	got := publishedPolicy(t, f).AllowRepos
	want := []string{"bitbucket/acme/other", "github/acme/app", "github/acme/third"}
	if len(got) != len(want) {
		t.Fatalf("allow_repos = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("allow_repos = %v, want %v", got, want)
		}
	}
	js, _, err := execute(t, "project", "show", "--json")
	if err != nil {
		t.Fatalf("project show refused it too: %v", err)
	}
	var doc struct {
		Enrolled map[string]string `json:"enrolled"`
	}
	if err := json.Unmarshal([]byte(js), &doc); err != nil {
		t.Fatalf("--json is not JSON (%v):\n%s", err, js)
	}
	if doc.Enrolled["github/acme/third"] != "the shared config" {
		t.Errorf("enrolled = %v, want github/acme/third from the shared config", doc.Enrolled)
	}
}

// TestProjectPolicyPublishRefusesASharedRepoItCannotSpell: the strict seed's
// refusals are the writer's, and they are about the *seed*, not about which
// config the entry came from. The shared config's list is the other half of
// the seed, and a repository there that names no provider — or one whose name
// projectpolicy.Canonical refuses — cannot be spelled as a canonical entry, so
// the publish dropped it silently: the allow-list it wrote was missing a
// repository of the shared config, which is exactly the un-enrollment the
// local config's own refusal exists to prevent. Both loops refuse in the
// strict posture now; a reader (`project show`, `project repos ls`) still
// skips such an entry, because it only labels where an entry came from.
//
// Mutation (verified, run and restored): drop the strict refusal from the
// shared-config loop in allowSourcesOf (the shipped behaviour): the publish
// succeeds and the entry is gone from the allow-list.
func TestProjectPolicyPublishRefusesASharedRepoItCannotSpell(t *testing.T) {
	for _, tc := range []struct {
		name  string
		repos string
		want  string
	}{
		{"no provider", "    acme/third:\n        workflows:\n            - web\n", "names no git provider"},
		{"a name Canonical refuses", "    acme/..:\n        provider: github\n        workflows:\n            - web\n", "acme/.."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProjectFixture(t, "")
			writeSharedConfig(t, f, tc.repos)
			_, _, err := execute(t, "project", "policy", "publish")
			if err == nil {
				t.Fatalf("the publish seeded without the shared config's %s: allow_repos = %v", tc.name, nodeJSON(t, f, projectpolicy.PathShared()))
			}
			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "shared config") {
				t.Errorf("the refusal does not name the shared config entry it cannot spell: %v", err)
			}
			if f.db.Value(projectpolicy.PathShared()) != nil {
				t.Error("a node was written anyway")
			}
		})
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
	// The move is the plain door: `fugaro update`'s rules stage deploys this
	// binary's generation over rules whose marker is absent, in the same run,
	// so --firebase-project (the door that adopts a Firebase project) is not
	// what an installation that has one needs to run.
	if !strings.Contains(err.Error(), "fugaro update") {
		t.Errorf("the refusal does not name the fix: %v", err)
	}
	if strings.Contains(err.Error(), "--firebase-project") {
		t.Errorf("the refusal sends the operator to --firebase-project for a state fugaro update repairs: %v", err)
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

// TestProjectPolicyPublishRefusesOlderRules: the marker exists and records a
// generation below the one that carries the policy nodes, which is the state a
// pre-policy installation leaves behind. The refusal is the other one, names
// the marker it found — and is the same sentence doctor prints for the state
// (rulesMarkerPrePolicyText), because a refusal and a report that word one state
// differently disagree about where the operator is standing.
func TestProjectPolicyPublishRefusesOlderRules(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.PolicyRulesVersion-1)
	_, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatal("a publish behind the deployed rules was allowed")
	}
	// The move is plain `fugaro update`, whose rules stage deploys this
	// binary's generation over the state — not `--firebase-project`, the door
	// that adopts a Firebase project this installation already has.
	if !strings.Contains(err.Error(), "records generation "+strconv.Itoa(projectpolicy.PolicyRulesVersion-1)) ||
		!strings.Contains(err.Error(), "fugaro update") || strings.Contains(err.Error(), "--firebase-project") {
		t.Errorf("the refusal does not say the marker is below the generation a policy is read under: %v", err)
	} // Not the marker-current state's sentence: "the deployed rules are older
	// than the marker" is a claim about a document, and nothing here compared
	// one — the marker itself is the wrong value.
	if strings.Contains(err.Error(), "older than the marker") {
		t.Errorf("the refusal for a marker below the generation reuses the other state's sentence: %v", err)
	}
	if !strings.Contains(err.Error(), rulesMarkerPrePolicyText(projectpolicy.PolicyRulesVersion-1)) {
		t.Errorf("the refusal is not the sentence doctor prints for this state:\n got %v\nwant %q", err, rulesMarkerPrePolicyText(projectpolicy.PolicyRulesVersion-1))
	}
	if f.db.Value(projectpolicy.PathShared()) != nil {
		t.Error("a node was written anyway")
	}
}

// TestProjectPolicyPublishIsAllowedAtThePolicyGeneration: a marker *above* the
// generation that carries the policy nodes and below this binary's is not a
// refusal — generation 1 is a real deployment whose rules carry
// config/policy/shared and grant a run's read of it, and a policy published
// under it is read and enforced by every run. Refusing there refused a publish
// on an installation whose policy governs, worded as "no run reads a published
// policy" about rules that read one, and it is a behaviour change the bump to
// generation 2 must not make on its own (the review finding on P2.1): the bar
// is the generation that introduced the nodes, and the same predicate is Read's.
//
// Mutation (verified, run and restored): make requirePolicyRules' case
// `gen >= projectpolicy.RulesVersion` — this test fails, the publish refusing
// under a marker the rules carry the node for.
func TestProjectPolicyPublishIsAllowedAtThePolicyGeneration(t *testing.T) {
	if projectpolicy.PolicyRulesVersion >= projectpolicy.RulesVersion {
		t.Fatalf("PolicyRulesVersion = %d and RulesVersion = %d: this test is about a generation below this binary's, and the two have met", projectpolicy.PolicyRulesVersion, projectpolicy.RulesVersion)
	}
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.PolicyRulesVersion)
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatalf("publishing under generation %d was refused (%v), want it allowed: these rules carry the policy nodes and a run reads the policy they hold", projectpolicy.PolicyRulesVersion, err)
	}
	if f.db.Value(projectpolicy.PathShared()) == nil {
		t.Error("the publish was allowed but wrote nothing")
	}
}

// TestProjectPolicyPublishRefusesANegativeRulesMarker: the marker is an integer
// that no deploy writes. requirePolicyRules branched on the generation a policy
// is read under, so a hand-edited -1 was refused with rulesMarkerPrePolicyText — the sentence for
// an old deployment, which says this installation's rules predate the policy
// nodes — while projectpolicy.Read reads the same node as the MarkerError it is
// (a launcher's -1 would otherwise read as "no policy here", taking a published
// policy out of every run's reach: the fail-open that check exists to close).
// A refusal that disagrees with the reader about what the node is sends the
// operator to redeploy rules that are not what is wrong (the review finding on
// this change).
func TestProjectPolicyPublishRefusesANegativeRulesMarker(t *testing.T) {
	for _, gen := range []int{-1, -projectpolicy.RulesVersion, -1000} {
		f := newProjectFixture(t, "")
		f.db.Set(projectpolicy.PathRulesMarker(), gen)
		_, _, err := execute(t, "project", "policy", "publish")
		if err == nil {
			t.Fatalf("marker %d: a publish under a hand-edited marker was allowed", gen)
		}
		// The refusal's text is the MarkerError's own, through userErr — which
		// renders the error it is given and so drops the chain errors.As would
		// walk: the node's name and the value it holds are what the test can
		// read here.
		want := (&projectpolicy.MarkerError{Value: strconv.Itoa(gen)}).Error()
		if !strings.Contains(err.Error(), want) {
			t.Errorf("marker %d: err = %v, want the MarkerError projectpolicy.Read refuses this node as (%q)", gen, err, want)
		}
		// Not the old-deployment sentence: nothing here compared the rules
		// document, and a hand edit is not a generation a deploy left behind.
		if strings.Contains(err.Error(), "below the generation") || strings.Contains(err.Error(), "older than the marker") {
			t.Errorf("marker %d: the refusal for a hand-edited marker reuses the old-deployment sentences: %v", gen, err)
		}
		if f.db.Value(projectpolicy.PathShared()) != nil {
			t.Error("a node was written anyway")
		}
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

// TestProjectPolicyPublishRefusesAPastedKeyWithoutEchoingIt: a provider's
// .secret is the NAME of the secret that holds the key, and every model
// provider's key begins with "sk-" — a value pasted there would otherwise be
// accepted as a name and published as one. The refusal names the field and
// never the value: the value IS a key, so quoting it would print a key into
// the terminal, the job log or result.json, which is the leak the check
// exists for (the same rule internal/config's ValidateModelProviders states
// for the config file).
func TestProjectPolicyPublishRefusesAPastedKeyWithoutEchoingIt(t *testing.T) {
	f := newProjectFixture(t, "")
	const value = "sk-or-v1-0123456789abcdef0123456789abcdef"
	setConfig(t, f, func(s string) string {
		return strings.Replace(s, "secret: openrouter-key", "secret: "+value, 1)
	})
	out, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatalf("a pasted API key was accepted as a secret's name:\n%s", out)
	}
	if !strings.Contains(err.Error(), "providers.openrouter.secret") {
		t.Errorf("the refusal does not name the field: %v", err)
	}
	for _, text := range []string{err.Error(), out} {
		if strings.Contains(text, value) {
			t.Errorf("the refusal echoes the value:\n%s", text)
		}
	}
	if f.db.Value(projectpolicy.PathShared()) != nil {
		t.Error("a node was written anyway")
	}
}

// TestReadOnlyCommandsSurviveARepoWithNoProvider: a `repos:` entry does not
// have to name a provider — localcfg documents that the checkout's fugaro.yaml
// decides it, and this file's own repoBySlug and shared-config branch skip such
// an entry — so the commands that only *read* must not abort on one. The seed
// the publisher computes is strict for a reason that belongs to the writer
// alone: a canonical allow-list entry needs the provider, and seeding without a
// repository that runs today un-enrolls it. `project show` (both output forms)
// and `project repos ls` only label the allow-list's sources, so an entry they
// cannot spell costs that label and nothing else.
//
// Mutation (verified, run and restored): make allowSources call the strict
// seed again (seedAllowRepos, as before): all three commands fail with the
// publish's refusal.
func TestReadOnlyCommandsSurviveARepoWithNoProvider(t *testing.T) {
	f := newProjectFixture(t, "  acme/noprov: { workflows: [web] }\n")
	// Published by hand: with this config the publisher refuses (asserted
	// below), and the readers only need a node to read.
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app"},
	})
	for _, args := range [][]string{
		{"project", "show"},
		{"project", "show", "--json"},
		{"project", "repos", "ls"},
	} {
		if _, _, err := execute(t, args...); err != nil {
			t.Errorf("%v failed on a config whose repos entry names no provider: %v", args, err)
		}
	}
	// The writer keeps its refusal, at the one point it seeds: --seed-repos is
	// where the allow-list is rebuilt from this config, and an entry the seed
	// cannot spell is a repository that would silently drop out of it.
	if _, _, err := execute(t, "project", "policy", "publish", "--seed-repos"); err == nil || !strings.Contains(err.Error(), "names no git provider") {
		t.Errorf("the publish must keep refusing a repos entry with no provider, got %v", err)
	}
}

// policyChangedDuringDelete plants another publish's node while a --delete is
// being confirmed: the window between the node this command read and validated
// (its ETag, the version refuseNewerPolicy checked) and the write that removes
// it. It fires on the read of the child nodes, the first request after the
// shared node was read.
type policyChangedDuringDelete struct {
	rt   http.RoundTripper
	db   *gcpfake.RTDB
	val  any
	done bool
}

func (c *policyChangedDuringDelete) RoundTrip(r *http.Request) (*http.Response, error) {
	if !c.done && r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/config/policy/repos.json") {
		c.done = true
		c.db.Set(projectpolicy.PathShared(), c.val)
	}
	return c.rt.RoundTrip(r)
}

// TestProjectPolicyDeleteDeletesUnderTheETagItValidated: --delete removes the
// node it read, under the ETag it read it with — never "whatever is there when
// the write happens". The in-flight check and the typed confirmation were made
// about the node that was read, and a node rewritten to a version
// refuseNewerPolicy refuses (P8) is exactly the one this binary must not delete
// at all; reading the node again here would delete a publish nobody confirmed.
//
// Mutation (verified, run and restored): in deleteProjectPolicy, read the
// shared node again with a fresh db.GetETag and delete under that one (the
// shipped behaviour): this test fails, the node the other publish wrote is
// gone.
func TestProjectPolicyDeleteDeletesUnderTheETagItValidated(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	other := map[string]any{
		"version":     projectpolicy.Version + 1,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 1},
		"allow_repos": []string{"github/other/*"},
	}
	budgetTransport = &policyChangedDuringDelete{rt: http.DefaultTransport, db: f.db, val: other}
	t.Cleanup(func() { budgetTransport = nil })

	out, _, err := execute(t, "project", "policy", "publish", "--delete", "--yes")
	if err == nil {
		t.Fatalf("a publish that landed while the deletion was being confirmed was deleted unconfirmed:\n%s", out)
	}
	if f.db.Value(projectpolicy.PathShared()) == nil {
		t.Fatalf("the node another publish wrote was deleted, and its deletion was never confirmed:\n%s", out)
	}
	if !strings.Contains(err.Error(), projectpolicy.PathShared()) {
		t.Errorf("the refusal does not name the node: %v", err)
	}
}

// TestProjectPolicyDeleteRefusesWithoutTheRulesMarker: --delete goes through
// the same rules-marker gate as a publish (it cannot remove a policy of a
// shape it does not know, and an absent marker is an installation that never
// deployed the rules a policy belongs to), which the command's help states.
func TestProjectPolicyDeleteRefusesWithoutTheRulesMarker(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	f.db.Set(projectpolicy.PathRulesMarker(), nil)
	_, _, err := execute(t, "project", "policy", "publish", "--delete", "--yes")
	if err == nil {
		t.Fatal("--delete without the rules marker was allowed")
	}
	// The move is the plain door: `fugaro update`'s rules stage deploys this
	// binary's generation over rules whose marker is absent, in the same run,
	// so --firebase-project (the door that adopts a Firebase project) is not
	// what an installation that has one needs to run.
	if !strings.Contains(err.Error(), "fugaro update") {
		t.Errorf("the refusal does not name the fix: %v", err)
	}
	if strings.Contains(err.Error(), "--firebase-project") {
		t.Errorf("the refusal sends the operator to --firebase-project for a state fugaro update repairs: %v", err)
	}
	if f.db.Value(projectpolicy.PathShared()) == nil {
		t.Error("the policy was removed anyway")
	}
}

// TestProjectPolicyDeleteRemovesChildrenThenTheSharedNode: the children go
// first and the shared node last (B5) — the shared node's existence is
// project-centric mode and, mid-delete, a launch that sees it takes the
// policy's tighter ceiling, so deleting it first would drop every launch in
// between to the looser ceiling its job's environment carries. A deletion that
// half-succeeds must not leave the mode off with a child behind either.
func TestProjectPolicyDeleteRemovesChildrenThenTheSharedNode(t *testing.T) {
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

// TestProjectPolicyDeleteRefusesTheTypedNameWhileARunIsInFlight is the same
// gate at a terminal, which is where it can actually be walked past: with a
// terminal, typing the project's name is what every other confirmation here
// accepts, and --delete must still refuse while a run is in flight — a run
// mid-flight keeps the policy until it finishes (B5). The test above cannot
// see this: with stdin that is not a terminal, confirmRemovalStrict refuses
// for its own reason, so deleting the in-flight gate there would pass it.
// --yes is the way through, because the gate is about the runs, not about
// whether the operator was asked.
func TestProjectPolicyDeleteRefusesTheTypedNameWhileARunIsInFlight(t *testing.T) {
	f := newProjectFixture(t, "")
	typed(t)
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	slug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	f.db.Set(budget.PathAgent(slug, "20261015-120000-aaaa"), map[string]any{"run_id": "20261015-120000-aaaa"})

	out, _, err := executeStdin(t, "aurora\n", "project", "policy", "publish", "--delete")
	if err == nil {
		t.Fatalf("--delete was allowed with the project's name typed while a run is in flight:\n%s", out)
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

// TestProjectShowPrintsEnrolledAndRunning: the pair an operator needs — a
// repository that is running now and is not enrolled, and an enrolled one that
// is not running now. The registry holds the runs in flight, so "not running
// now" is all either says: an enrolled repository that runs daily reads the
// same as one that never has.
func TestProjectShowPrintsEnrolledAndRunning(t *testing.T) {
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
		"repositories running now",
		stranger, // running now, and not invertible
		"not admitted",
		// The remedy is 'repos add', which takes a canonical name: a slug
		// cannot be one, so the note asks the operator to name it instead of
		// printing a command that would be refused.
		"name it",
		"bitbucket/acme/other is on the allow-list and has no run in flight here",
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
		AllowRepos         []string          `json:"allow_repos"`
		Enrolled           map[string]string `json:"enrolled"`
		Running            []string          `json:"running_now"`
		RunningNotEnrolled []string          `json:"running_not_enrolled"`
		EnrolledNotRunning []string          `json:"enrolled_not_running"`
	}
	if err := json.Unmarshal([]byte(js), &doc); err != nil {
		t.Fatalf("--json is not JSON (%v):\n%s", err, js)
	}
	if len(doc.AllowRepos) != 2 || len(doc.Running) != 2 {
		t.Errorf("--json = %s, want two allow-list entries and two repositories running now", js)
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
	if len(doc.RunningNotEnrolled) != 1 || doc.RunningNotEnrolled[0] != stranger {
		t.Errorf("running_not_enrolled = %v, want [%s]", doc.RunningNotEnrolled, stranger)
	}
	if len(doc.EnrolledNotRunning) != 1 || doc.EnrolledNotRunning[0] != "bitbucket/acme/other" {
		t.Errorf("enrolled_not_running = %v, want [bitbucket/acme/other]", doc.EnrolledNotRunning)
	}
}

// TestProjectShowDoesNotCallAPatternEntryIdleWhileItsRepositoriesRun: the run
// registry holds repository names, and the entry side of the pair compared
// each allow-list entry to them literally. A pattern entry (github/acme/*)
// never equals a repository name, so it was reported as "on the allow-list and
// has no run in flight here" while github/acme/app — the repository it admits,
// and whose run is in flight — was running, which is the one thing this pair
// of lists exists to say. Admission is the list's own matcher
// (projectpolicy.AdmitRepo, which the runner and the launcher use), and the
// question here is that matcher asked of one entry.
func TestProjectShowDoesNotCallAPatternEntryIdleWhileItsRepositoriesRun(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "project", "repos", "add", "github/acme/*"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "project", "repos", "rm", "github/acme/app", "--yes"); err != nil {
		t.Fatal(err)
	}
	// The pattern admits github/acme/app, and a run of it is in flight.
	appSlug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	f.db.Set(budget.PathAgent(appSlug, "20261015-120000-aaaa"), map[string]any{"run_id": "20261015-120000-aaaa"})

	out, _, err := execute(t, "project", "show")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "github/acme/* is on the allow-list and has no run in flight here") {
		t.Errorf("project show calls the pattern idle though the repository it admits is running now:\n%s", out)
	}
	if !strings.Contains(out, "bitbucket/acme/other is on the allow-list and has no run in flight here") {
		t.Errorf("project show no longer reports the entry nothing is running:\n%s", out)
	}
	js, _, err := execute(t, "project", "show", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		AllowRepos         []string `json:"allow_repos"`
		EnrolledNotRunning []string `json:"enrolled_not_running"`
	}
	if err := json.Unmarshal([]byte(js), &doc); err != nil {
		t.Fatalf("--json is not JSON (%v):\n%s", err, js)
	}
	if !slices.Contains(doc.AllowRepos, "github/acme/*") {
		t.Fatalf("allow_repos = %v, want the pattern the test enrolled", doc.AllowRepos)
	}
	if len(doc.EnrolledNotRunning) != 1 || doc.EnrolledNotRunning[0] != "bitbucket/acme/other" {
		t.Errorf("enrolled_not_running = %v, want [bitbucket/acme/other]: the pattern's repository is running now", doc.EnrolledNotRunning)
	}
	// A second installation, with nothing running: the same pattern is idle
	// there, and saying so is right — the matcher answers about the runs there
	// are.
	newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "project", "repos", "add", "github/acme/*"); err != nil {
		t.Fatal(err)
	}
	js, _, err = execute(t, "project", "show", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(js), &doc); err != nil {
		t.Fatalf("--json is not JSON (%v):\n%s", err, js)
	}
	if !slices.Contains(doc.EnrolledNotRunning, "github/acme/*") {
		t.Errorf("enrolled_not_running = %v, want the pattern too: nothing is running here", doc.EnrolledNotRunning)
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

// someFuturePolicy is a node carrying a key this Fugaro does not know *inside* a
// block that is otherwise this Fugaro's own: `routing.claude.future_route`. The
// block is still one this Fugaro acts on — via: auto, a named Claude ID, and a
// route entry that claims it with allow_data_to written — so Decode reads it and
// the unknown key inside the switch survives. A routing block whose member this
// Fugaro cannot name is refused instead (validRoutingMembers): a misspelt
// `claude` there is the switch itself going missing, not a newer release's extra
// tightening. The budget block, model_prices and the inside of a providers entry
// are strict for the same reason — a misspelt provider field can fail open (a
// nil allow_data_to means every repository) — so a key of theirs is not the case
// this is about.
func someFuturePolicy() map[string]any {
	return map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app"},
		"providers": map[string]any{"openrouter": map[string]any{
			"kind": "anthropic-compat", "base_url": "https://openrouter.ai/api/v1", "auth": "bearer",
			"secret": "openrouter-key", "models": []string{"deepseek/*", "anthropic/claude-sonnet-5.5"},
			"allow_data_to": []string{"acme/app"},
			"require_zdr":   false, "price_policy": "highest",
		}},
		"routing": map[string]any{"claude": map[string]any{
			"via": "auto", "models": []string{"claude-sonnet-5-5"}, "future_route": "x",
		}},
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
	if v, _ := nodeAt(f.db.Value(projectpolicy.PathShared()), "routing", "claude", "future_route"); v != "x" {
		t.Errorf("routing's unknown key is %v, want x", v)
	}
}

// TestProjectReposEditRefusesWithoutTheRulesMarker: `repos add` and `repos rm`
// write the node the runner reads, so they are publishes of the allow-list and
// take `policy publish`'s own gate (P3, a review finding): a policy edited
// into a deployment whose rules carry no marker — or one older than this
// Fugaro's — is one the runs it bounds cannot read. Before this they were the
// one way to write the shared node on a deployment `policy publish` refuses to
// touch, and the write went through.
//
// Mutation (run, restored): drop the requirePolicyRules call from
// runProjectReposEdit. Both cases below write the node and the test fails on
// the refusal it expects.
func TestProjectReposEditRefusesWithoutTheRulesMarker(t *testing.T) {
	for _, tc := range []struct {
		what   string
		marker any
		want   string
		// Where the refusal sends the operator. Both states are what
		// `fugaro update`'s own rules stage deploys over, so both sentences now
		// name that one command; the two are pinned as written, since a remedy
		// that drifts back to the `--firebase-project` door sends the operator to
		// adopt a Firebase project this installation already has.
		fix string
	}{
		{"no marker at all", nil, "carry no policy marker", "run 'fugaro update'"},
		// main's requirePolicyRules (#532) admits every marker at or above
		// PolicyRulesVersion — those rules carry the policy nodes — and words a
		// marker below it as its own case ("records generation N", the
		// pre-policy sentence doctor prints): that is the marker behind this
		// Fugaro's that still refuses the write.
		{"a marker behind this Fugaro's", projectpolicy.PolicyRulesVersion - 1, "records generation", "re-run 'fugaro update' with fugaro"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			f := newProjectFixture(t, "")
			f.db.Set(projectpolicy.PathShared(), someFuturePolicy())
			before := nodeJSON(t, f, projectpolicy.PathShared())
			f.db.Set(projectpolicy.PathRulesMarker(), tc.marker)

			for _, args := range [][]string{
				{"project", "repos", "add", "github/acme/new"},
				{"project", "repos", "rm", "github/acme/app"},
			} {
				_, _, err := execute(t, args...)
				if err == nil {
					t.Fatalf("%v was allowed with %s", args, tc.what)
				}
				if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), tc.fix) {
					t.Errorf("%v refused with %q, want it to say %q and to name the fix %q", args, err, tc.want, tc.fix)
				}
			}
			if got := nodeJSON(t, f, projectpolicy.PathShared()); got != before {
				t.Errorf("the node changed: %s, want %s", got, before)
			}
		})
	}
}

// TestProjectReposEditRefusesAMisspeltProviderKey: the other half of the
// tolerance above. An unknown key inside a providers entry is not preserved —
// it is refused, with the remedy, and the node is left exactly as it was: a
// repos edit on a node whose provider entry misspells allow_data_to would
// otherwise write back the widest data-sharing setting there is, since the
// decoded nil list means every repository.
func TestProjectReposEditRefusesAMisspeltProviderKey(t *testing.T) {
	f := newProjectFixture(t, "")
	node := someFuturePolicy()
	node["providers"].(map[string]any)["openrouter"].(map[string]any)["allow_data_tp"] = []string{"acme/app"}
	f.db.Set(projectpolicy.PathShared(), node)

	_, _, err := execute(t, "project", "repos", "add", "github/acme/new")
	if err == nil {
		t.Fatalf("the edit was accepted: %s", nodeJSON(t, f, projectpolicy.PathShared()))
	}
	for _, want := range []string{"allow_data_tp", "--delete"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	// The node as it was: the misspelt key still there, allow_repos unchanged.
	if v, _ := nodeAt(f.db.Value(projectpolicy.PathShared()), "providers", "openrouter", "allow_data_tp"); v == nil {
		t.Errorf("the refused edit rewrote the node: %s", nodeJSON(t, f, projectpolicy.PathShared()))
	}
	got, _ := nodeAt(f.db.Value(projectpolicy.PathShared()), "allow_repos")
	if list, _ := got.([]any); len(list) != 1 || list[0] != "github/acme/app" {
		t.Errorf("allow_repos = %v, want the list the node held", got)
	}
}

// allowRewritten is the one `allow_repos: A → B` line of a repos edit's
// output, so a test can assert what it says exactly: the list as the node held
// it on the left, the list that will be written on the right. Contains()
// assertions cannot tell a right-hand list repeated on both sides from a real
// diff, which is what an aliased `before` produces.
func allowRewritten(t *testing.T, out string) string {
	t.Helper()
	// Not a line prefix: `rm` prints "Type <project> to confirm: " with no
	// trailing newline before the diff, so the line can start mid-line.
	const mark = "allow_repos: ["
	if n := strings.Count(out, mark); n != 1 {
		t.Fatalf("the output has %d allow_repos line(s), want exactly one:\n%s", n, out)
	}
	line := out[strings.Index(out, mark):]
	if j := strings.IndexByte(line, '\n'); j >= 0 {
		line = line[:j]
	}
	return line
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
	// The exact line: the list of the node on the left, the list to be written
	// on the right (a `before` aliased to the edited slice prints the new list
	// twice).
	if want, got := "allow_repos: [bitbucket/acme/other github/acme/app] → [bitbucket/acme/other github/acme/app github/acme/new]", allowRewritten(t, out); got != want {
		t.Errorf("the add diff line is\n%q\nwant\n%q", got, want)
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
	// The exact line, and the removal is what is on the left: an aliased
	// `before` prints the post-removal list (plus a zeroed trailing element)
	// on both sides, which reads as though the entry were never there.
	if want, got := "allow_repos: [bitbucket/acme/other github/acme/app github/acme/new] → [bitbucket/acme/other github/acme/app]", allowRewritten(t, out); got != want {
		t.Errorf("the rm diff line is\n%q\nwant\n%q", got, want)
	}
	if got := publishedPolicy(t, f).AllowRepos; len(got) != 2 {
		t.Fatalf("allow_repos = %v, want the entry removed", got)
	}
}

// TestProjectReposEditRefusesAConcurrentWrite: the edit writes the node under
// the ETag it read, so an allow-list that changes in between — another
// operator's publish or repos edit — is refused rather than overwritten with
// the copy this one read. There is no retry here, unlike the publisher's: this
// write is the read allow-list plus one name, and re-reading after someone
// else's change would re-add a name they had just removed (the publisher's
// retry diff exists for exactly that reason).
func TestProjectReposEditRefusesAConcurrentWrite(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		stdin string
	}{
		{"add", []string{"project", "repos", "add", "github/acme/new"}, ""},
		{"rm", []string{"project", "repos", "rm", "github/acme/app"}, "aurora\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProjectFixture(t, "")
			if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
				t.Fatal(err)
			}
			typed(t)
			c := &policyConflictOnce{rt: http.DefaultTransport, db: f.db, val: map[string]any{
				"version":     projectpolicy.Version,
				"budget":      map[string]any{"mode": "observe", "per_run_usd": 7},
				"allow_repos": []string{"github/acme/app", "github/acme/other-side"},
			}}
			budgetTransport = c
			t.Cleanup(func() { budgetTransport = nil })

			out, _, err := executeStdin(t, tc.stdin, tc.args...)
			if !c.done {
				t.Fatal("no write was attempted, so the conflict never happened")
			}
			if err == nil {
				t.Fatalf("repos %s overwrote a policy that changed under it:\n%s", tc.name, out)
			}
			if !strings.Contains(err.Error(), "changed while this ran") || !strings.Contains(err.Error(), "nothing was written") {
				t.Errorf("the refusal does not say what happened: %v", err)
			}
			got := publishedPolicy(t, f)
			if len(got.AllowRepos) != 2 || got.AllowRepos[1] != "github/acme/other-side" || got.Budget.PerRunUSD != 7 {
				t.Fatalf("the node is not the one the other writer left: %+v", got)
			}
		})
	}
}

// TestProjectReposAddRefusesUnicodeLookalikes: the argument is folded ASCII
// only and a concrete name then goes through projectpolicy.Canonical — the
// form the runner matches allow_repos in, and the one task.CanonicalRepo
// produces. strings.ToLower case-folds Unicode, so a name typed with a Kelvin
// sign K (U+212A) or a dotted capital I (U+0130) used to be accepted and
// written as the ASCII letter it folds to ("k", "i"): an entry the operator
// never typed, one Canonical itself refuses to produce, and therefore one
// `repos rm` could never name back.
func TestProjectReposAddRefusesUnicodeLookalikes(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	for _, arg := range []string{
		"github/acme/apK",  // a Kelvin sign K (U+212A)
		"githuKb/acme/app", // the same, as the provider
		"github/acme/İapp", // a dotted capital I (U+0130)
	} {
		if _, _, err := execute(t, "project", "repos", "add", arg); err == nil {
			t.Errorf("%q was accepted; it is not a name Canonical can produce", arg)
		}
	}
	if got := publishedPolicy(t, f).AllowRepos; len(got) != 2 {
		t.Fatalf("allow_repos = %v: a refused argument was written", got)
	}
}

// TestProjectReposAddRefusesAProviderKindNothingCanMatch is the review finding
// on projectpolicy.ValidPattern: it held the first allow_repos segment to
// task.ValidProvider — the shape of a slug, which accepts any lower-case word a
// slug could be built from — rather than to the kinds a canonical repository can
// carry (config.Providers, the values a repository's git.provider may be). So
// `fugaro project repos add githubb/acme/app` published an entry that matches no
// repository that can exist, which is exactly the silent un-enrollment that
// function's own doc says it exists to prevent: the operator saw the entry
// published, and the repository's runs were refused at admission with nothing
// else to read.
func TestProjectReposAddRefusesAProviderKindNothingCanMatch(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	for _, arg := range []string{"githubb/acme/app", "gitlab/acme/app", "gce/acme/app"} {
		_, _, err := execute(t, "project", "repos", "add", arg)
		if err == nil {
			t.Errorf("%q was accepted; no repository's git.provider can be that kind", arg)
			continue
		}
		// The refusal names the kinds, so the fix is one word rather than a
		// guess.
		if !strings.Contains(err.Error(), "github, bitbucket") {
			t.Errorf("%q was refused without naming the kinds: %v", arg, err)
		}
	}
	if got := publishedPolicy(t, f).AllowRepos; len(got) != 2 {
		t.Fatalf("allow_repos = %v: a refused argument was written", got)
	}
	for _, arg := range []string{"github/acme/app", "bitbucket/acme/app", "*/acme/*"} {
		if _, _, err := execute(t, "project", "repos", "add", arg); err != nil {
			t.Errorf("%q is a provider kind and was refused: %v", arg, err)
		}
	}
}

// writeSharedConfig writes the project's shared config into its runs bucket,
// the way fugaro config publish does, so a seed can read its repository list.
// It is a full published config because ParseShared validates one against the
// bucket it was read from.
func writeSharedConfig(t *testing.T, f *projectFixture, repos string) {
	t.Helper()
	writeSharedConfigWithSource(t, f, "", repos)
}

// writeSharedConfigWithSource is writeSharedConfig for an installation whose
// local config records a non-default image_source: a publisher there writes
// the same source into the document it publishes, and ParseShared accepts it
// only from a reader that records the same one.
func writeSharedConfigWithSource(t *testing.T, f *projectFixture, source, repos string) {
	t.Helper()
	sourceLine := ""
	if source != "" {
		sourceLine = "image_source: " + source + "\n"
	}
	s := `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: unused-bucket
` + sourceLine + `registry_host: us-east5-docker.pkg.dev/proj-1234
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

// TestRemovalWarningSaysWhatTheNextRunDoes: `repos rm`'s confirmation is read
// by an operator about to stop a repository's runs, so it has to be what the
// next run actually does. An entry that still admits the repository admits it
// whatever happens to the concrete entry — github/acme/* admits
// github/acme/app — and a removal that said "its runs are refused from now on"
// would be claiming a stop it does not make (the review of PR #390). The
// sentences are the runner's own matcher (projectpolicy.MatchRepo), so they are
// about admission and not about string equality.
func TestRemovalWarningSaysWhatTheNextRunDoes(t *testing.T) {
	for name, tc := range map[string]struct {
		allow     []string
		arg       string
		still     bool
		admitting string
	}{
		"concrete, a pattern remains":   {[]string{"github/acme/*", "github/acme/app"}, "github/acme/app", true, "github/acme/*"},
		"concrete, the wildcard":        {[]string{"*", "github/acme/app"}, "github/acme/app", true, "*"},
		"concrete, nothing remains":     {[]string{"github/acme/app"}, "github/acme/app", false, ""},
		"concrete, another repo only":   {[]string{"github/acme/app", "github/other/*"}, "github/acme/app", false, ""},
		"pattern, the wildcard remains": {[]string{"*", "github/acme/*"}, "github/acme/*", true, "*"},
		"pattern, nothing remains":      {[]string{"github/acme/*"}, "github/acme/*", false, ""},
		"pattern, a narrower one only":  {[]string{"github/acme/*", "github/acme/app"}, "github/acme/*", false, ""},
		// A broader pattern is not the bare wildcard and still admits every
		// repository the removed one did (code review, medium): */acme/*
		// matches github/acme/app, and canonicalRepoArg accepts the entry, so
		// claiming the runs stop here would be a stop that does not happen.
		"pattern, a broader pattern remains": {[]string{"*/acme/*", "github/acme/*"}, "github/acme/*", true, "*/acme/*"},
		// A covering entry need not be written with bare "*" segments: github/ac*
		// admits every owner whose name starts with "ac", which is every owner
		// github/acme/* admitted, so the runs are not refused either. MatchRepo
		// answers this — and coversRepo did not, which is why the sentence could
		// claim a stop the removal does not make (the review of PR #402).
		"pattern, a partial wildcard that covers it": {[]string{"github/ac*/*", "github/acme/*"}, "github/acme/*", true, "github/ac*/*"},
		"pattern, another provider's remains":        {[]string{"gitlab/acme/*", "github/acme/*"}, "github/acme/*", false, ""},
		// A removed entry is not a revocation when what remains admits every
		// repository it did — and here what remains admits the entry itself:
		// github/acme/*app is a pattern, and github/acme/* admits the string
		// "*app" as the repository it reads, so every repository the removed
		// entry admitted is still admitted. MatchRepo had the wildcard and
		// the literal the wrong way round, so this read "refused from now
		// on": the operator was told a stop happened that did not.
		"pattern, a wildcard over a literal wildcard": {[]string{"github/acme/*", "github/acme/*app"}, "github/acme/*app", true, "github/acme/*"},
		"pattern, the same width narrower":            {[]string{"github/acme/*", "github/*/*"}, "github/*/*", false, ""},
		"pattern, its own repo entry remains":         {[]string{"github/acme/app", "github/acme/*"}, "github/acme/*", false, ""},
		// The removed entry carries a "*" in its *name* (its last segment is
		// "*-web", not "*"), and github/*/* admits every repository that entry
		// admitted. MatchRepo meets that "*" as a character of the subject
		// while the pattern's own "*" is the wildcard — the case a matcher
		// that compared the two positions first got wrong, reading this as
		// "refused from now on" for a control the removal did not take away
		// (the review of this change).
		"pattern with a star in its name, a broader pattern": {[]string{"github/*/*", "github/acme/*-web"}, "github/acme/*-web", true, "github/*/*"},
		// The other direction: github/a*/app admits github/acme/app, but not
		// github/acme/anything-else, which the removed pattern did admit — so the
		// removal does refuse something and must say so.
		"pattern, a partial wildcard that does not": {[]string{"github/a*/app", "github/acme/*"}, "github/acme/*", false, ""},
		// Overlap is not coverage, in either direction. This one is not a comment
		// about MatchRepo's arguments being patterns: they are not — MatchRepo
		// reads its second argument as a repository, so MatchRepo("github/acme/*",
		// "github/*/app") is false. It is that github/*/app admits
		// github/other/app, which github/acme/* does not.
		"pattern, one that only overlaps it": {[]string{"github/acme/*", "github/*/app"}, "github/*/app", false, ""},
		// A "*" inside the removed pattern, aligned with the "*" of the entry
		// that covers it. The entry has to read that "*" as an ordinary
		// character in the name it is matched against — github/acme/a* admits
		// every expansion of github/acme/a*b — and the matcher tested the
		// literal case first, so the pattern's own "*" was consumed as a
		// literal and lost: this answered false, and the confirmation claimed
		// a stop the removal does not make.
		"pattern, a star inside the removed pattern": {[]string{"github/acme/a*", "github/acme/a*b"}, "github/acme/a*b", true, "github/acme/a*"},
	} {
		t.Run(name, func(t *testing.T) {
			got, by := admittedBy(tc.allow, tc.arg)
			if got != tc.still || by != tc.admitting {
				t.Fatalf("admittedBy(%v, %q) = (%v, %q), want (%v, %q)", tc.allow, tc.arg, got, by, tc.still, tc.admitting)
			}
			w := removalWarning(tc.allow, tc.arg)
			if !strings.Contains(w, tc.arg) {
				t.Errorf("the warning does not name the entry: %q", w)
			}
			switch {
			case tc.still && !strings.Contains(w, "still admits it"):
				t.Errorf("the warning = %q, want it to say something on the list still admits it", w)
			case tc.still:
				if !strings.Contains(w, tc.admitting) {
					t.Errorf("the warning = %q, want it to name %q", w, tc.admitting)
				}
				if strings.Contains(w, "refused from now on") {
					t.Errorf("the warning = %q: the runs are not refused, %s still admits it", w, tc.admitting)
				}
			case !tc.still && !strings.Contains(w, "refused from now on"):
				t.Errorf("the warning = %q, want it to say the runs are refused from now on", w)
			}
		})
	}
}

// TestProjectReposRmWithAPatternStillOnTheListSaysSo: the same sentence, end to
// end. github/acme/* is added by hand, then github/acme/app is removed: the
// repository's runs go on being admitted, and the operator has to be told that
// rather than told its runs stop.
func TestProjectReposRmWithAPatternStillOnTheListSaysSo(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "project", "repos", "add", "github/acme/*"); err != nil {
		t.Fatal(err)
	}
	typed(t)
	out, _, err := executeStdin(t, "aurora\n", "project", "repos", "rm", "github/acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "github/acme/* still admits it") {
		t.Errorf("the confirmation does not name the entry that still admits it:\n%s", out)
	}
	if strings.Contains(out, "refused from now on") {
		t.Errorf("the confirmation says the runs are refused though github/acme/* is on the list:\n%s", out)
	}
	if got := publishedPolicy(t, f).AllowRepos; len(got) != 2 || got[0] != "bitbucket/acme/other" || got[1] != "github/acme/*" {
		t.Fatalf("allow_repos = %v, want github/acme/app gone and the pattern still there", got)
	}
	if !projectpolicy.AdmitRepo(publishedPolicy(t, f).AllowRepos, "github/acme/app") {
		t.Error("the policy no longer admits the repository the confirmation said it still admits")
	}
}

// TestProjectReposRmOfAPatternWithAPartialWildcardLeftSaysSo: the same
// sentence, end to end, with the covering entry a partial wildcard.
// github/ac*/* admits every repository github/acme/* admitted — github/ac* is
// the owners whose name starts with "ac" — so removing github/acme/* refuses
// nothing, and the confirmation has to say that rather than claim a stop it
// does not make (the review of PR #402: coversRepo missed this, and the
// sentence read "its runs are refused from now on" while github/ac*/* was on
// the list).
//
// Mutation (verified, run and restored): put coversRepo back in admittedBy's
// pattern branch: this test fails, reading "refused from now on" while
// github/ac*/* still admits every repository the removed pattern admitted.
func TestProjectReposRmOfAPatternWithAPartialWildcardLeftSaysSo(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"github/ac*/*", "github/acme/*"} {
		if _, _, err := execute(t, "project", "repos", "add", p); err != nil {
			t.Fatal(err)
		}
	}
	typed(t)
	out, _, err := executeStdin(t, "aurora\n", "project", "repos", "rm", "github/acme/*")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "github/ac*/* still admits it") {
		t.Errorf("the confirmation does not name the partial wildcard that still admits it:\n%s", out)
	}
	if strings.Contains(out, "refused from now on") {
		t.Errorf("the confirmation says the runs are refused though github/ac*/* is on the list:\n%s", out)
	}
	allow := publishedPolicy(t, f).AllowRepos
	if slices.Contains(allow, "github/acme/*") {
		t.Errorf("allow_repos = %v, want the pattern removed", allow)
	}
	// The point of the sentence: every repository the removed pattern admitted
	// is still admitted, by the entry the confirmation named.
	for _, r := range []string{"github/acme/app", "github/acme/anything"} {
		if !projectpolicy.AdmitRepo(allow, r) {
			t.Errorf("allow_repos = %v no longer admits %s, which the confirmation said it keeps", allow, r)
		}
	}
}

// TestProjectReposRmWithoutAnythingElseAdmittingSaysItStopsRuns: the other
// side, so the sentence above is not the only one the command can produce.
func TestProjectReposRmWithoutAnythingElseAdmittingSaysItStopsRuns(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	typed(t)
	out, _, err := executeStdin(t, "aurora\n", "project", "repos", "rm", "github/acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "refused from now on") {
		t.Errorf("the confirmation does not say the runs are refused:\n%s", out)
	}
	if strings.Contains(out, "still admits") {
		t.Errorf("the confirmation claims another entry admits it, and none does:\n%s", out)
	}
	if projectpolicy.AdmitRepo(publishedPolicy(t, f).AllowRepos, "github/acme/app") {
		t.Error("the policy still admits the repository the confirmation said it stops")
	}
}

// TestProjectReposRmTheWildcardSaysWhatIsLeft: `repos rm '*'` refuses the
// repositories nothing else admits — the command's own help states that rule —
// so over a list that still holds github/acme/* the confirmation must not claim
// every run stops. The wildcard is the one entry the "still admits it" sentence
// does not fit, because admittedBy asks whether a *string* is admitted and no
// pattern matches the string "*": without the branch that reads the rest of the
// list, "*" fell through to the concrete sentence.
//
// Mutation (verified, run and restored): let the wildcard fall through to
// removalWarning's last sentence: this test fails, reading "its runs are
// refused from now on" while github/acme/* is on the list.
func TestProjectReposRmTheWildcardSaysWhatIsLeft(t *testing.T) {
	if got := removalWarning([]string{"*"}, "*"); !strings.Contains(got, "refused from now on") || !strings.Contains(got, "only entry") {
		t.Errorf("removing the only entry = %q, want it to say every run is refused", got)
	}
	if got := removalWarning([]string{"*", "github/acme/*"}, "*"); strings.Contains(got, "refused from now on") {
		t.Errorf("removing the wildcard with a pattern left = %q, want no claim that runs stop", got)
	} else if !strings.Contains(got, "github/acme/*") {
		t.Errorf("the confirmation does not name the entry that still admits its own: %q", got)
	}
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"github/acme/*", "*"} {
		if _, _, err := execute(t, "project", "repos", "add", p); err != nil {
			t.Fatal(err)
		}
	}
	typed(t)
	out, _, err := executeStdin(t, "aurora\n", "project", "repos", "rm", "*")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "refused from now on") {
		t.Errorf("the confirmation claims every run stops though github/acme/* is on the list:\n%s", out)
	}
	if !strings.Contains(out, "github/acme/*") {
		t.Errorf("the confirmation does not name the entry that still admits its own:\n%s", out)
	}
	allow := publishedPolicy(t, f).AllowRepos
	if slices.Contains(allow, "*") {
		t.Errorf("allow_repos = %v, want the wildcard removed", allow)
	}
	// The point of the sentence: a repository the remaining entry covers is
	// still admitted, which is why the runs are not refused.
	if !projectpolicy.AdmitRepo(allow, "github/acme/app") {
		t.Errorf("allow_repos = %v no longer admits github/acme/app, which the confirmation said it keeps", allow)
	}
}

// TestProjectReposEditTheWildcard: "*" is an allow-list entry like any other —
// the design's explicit every-repository choice (§4.3), which MatchRepo reads
// as admitting anything, which ValidPattern accepts, and which the rules
// template's own testdata writes — so both commands have to know it.
// canonicalRepoArg split its argument on "/" and knew only the owner/name and
// provider/owner/name forms, so "*" fell through to "write it as
// provider/owner/name": an operator could not write the widest list with
// `repos add`, and a list holding it could not be narrowed with `repos rm`
// (it refused the argument before it looked it up), leaving a full republish as
// the only way to take it off.
//
// Mutation (verified, run and restored): remove the "*" branch from
// canonicalRepoArg — add fails with "not a repository: write it as
// provider/owner/name" and rm with the same, nothing is written, and the list
// still admits the repository the removal was meant to refuse.
func TestProjectReposEditTheWildcard(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	// The list the fixture publishes names two repositories and not the
	// wildcard: what the assertions below read is this command's own write.
	if allow := publishedPolicy(t, f).AllowRepos; slices.Contains(allow, "*") {
		t.Fatalf("the fixture's published list already holds the wildcard: %v", allow)
	}
	out, _, err := execute(t, "project", "repos", "add", "*")
	if err != nil {
		t.Fatalf("repos add '*' = %v", err)
	}
	if !strings.Contains(out, "allow_repos: ") || !strings.Contains(out, "*") {
		t.Errorf("the diff does not print the entry it wrote:\n%s", out)
	}
	allow := publishedPolicy(t, f).AllowRepos
	if !slices.Contains(allow, "*") {
		t.Fatalf("the node holds %v, want the wildcard among them", allow)
	}
	// The entry admits what no other entry on the list does — which is what
	// makes removing it the widening refusal `repos rm` asks about.
	if !projectpolicy.AdmitRepo(allow, "github/anything/else") {
		t.Error("the list does not admit a repository through the wildcard it holds")
	}

	// And it comes off the same way: with the project's name typed, since
	// removing it refuses every repository the list does not otherwise admit.
	typed(t)
	out, _, err = executeStdin(t, "aurora\n", "project", "repos", "rm", "*")
	if err != nil {
		t.Fatalf("repos rm '*' = %v", err)
	}
	// What removing the wildcard does is a set, not one repository's end: the
	// repositories the rest of the list still admits keep running, so the
	// sentence is the help text's rule — "refuses every repository the rest of
	// the list does not admit" — and it names the entries that stay (the review
	// of PR #414's branch: this said "its runs are refused from now on" over a
	// list that still admitted both of them).
	if !strings.Contains(out, `removes * from the allow-list`) || !strings.Contains(out, "refused only if no other entry admits it") {
		t.Errorf("the confirmation does not say what removing the wildcard does:\n%s", out)
	}
	if !strings.Contains(out, "bitbucket/acme/other") || !strings.Contains(out, "github/acme/app") {
		t.Errorf("the confirmation does not name the entries that still admit their own:\n%s", out)
	}
	if strings.Contains(out, "refused from now on") {
		t.Errorf("the confirmation claims every run stops though concrete entries remain:\n%s", out)
	}
	allow = publishedPolicy(t, f).AllowRepos
	if slices.Contains(allow, "*") {
		t.Errorf("the node still holds the wildcard: %v", allow)
	}
	if projectpolicy.AdmitRepo(allow, "github/anything/else") {
		t.Error("the list still admits a repository only the wildcard admitted")
	}
	if !projectpolicy.AdmitRepo(allow, "github/acme/app") {
		t.Error("removing the wildcard also dropped an entry that was on the list: the command must remove one entry and nothing else")
	}
}

// TestProjectReposEditANestedRepository: a repository path may nest —
// task.CanonicalRepo admits "acme/group/app" and projectpolicy.Canonical pins
// it as valid — so provider/owner/group/name is a repository an allow-list
// holds, and both commands must read it as one. Reading the argument as
// exactly three segments refused it ("write it as provider/owner/name"), which
// with ValidPattern's own cap of three left the wildcard as the only way to
// allow-list a nested repository — an entry that does admit a repository, so
// the guard was refusing the operator's own repository rather than a typo (the
// review finding on this change).
//
// Mutation (verified, run and restored): restore canonicalRepoArg's
// `len(parts) != 3` — add fails with "is not a repository: write it as
// provider/owner/name" and nothing is written.
func TestProjectReposEditANestedRepository(t *testing.T) {
	const nested = "github/acme/team/app"
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "project", "repos", "add", nested)
	if err != nil {
		t.Fatalf("repos add %s = %v", nested, err)
	}
	if !strings.Contains(out, "allow_repos: ") || !strings.Contains(out, nested) {
		t.Errorf("the diff does not print the entry it wrote:\n%s", out)
	}
	allow := publishedPolicy(t, f).AllowRepos
	if !slices.Contains(allow, nested) {
		t.Fatalf("the node holds %v, want %s among them", allow, nested)
	}
	// The entry is the one the run's admission asks about, and it admits this
	// repository and not its siblings.
	if !projectpolicy.AdmitRepo(allow, nested) {
		t.Errorf("allow_repos = %v does not admit %s, the entry just written", allow, nested)
	}
	if projectpolicy.MatchRepo(nested, "github/acme/app") {
		t.Errorf("the entry %s admits github/acme/app, which is another repository", nested)
	}
	// And the node stays readable: Decode applies ValidPattern to every entry
	// it reads, so an entry this command can write but the decoder refuses
	// would halt every run of the project. publishedPolicy decodes the node it
	// reads back, and fails the test if it cannot.
	allow = publishedPolicy(t, f).AllowRepos
	if !slices.Contains(allow, nested) {
		t.Errorf("the node read back holds %v, want %s among them", allow, nested)
	}
	// It comes off the same way.
	typed(t)
	if _, _, err := executeStdin(t, "aurora\n", "project", "repos", "rm", nested); err != nil {
		t.Fatalf("repos rm %s = %v", nested, err)
	}
	if allow := publishedPolicy(t, f).AllowRepos; slices.Contains(allow, nested) {
		t.Errorf("the node still holds %s: %v", nested, allow)
	}
}

// TestProjectReposEditFoldsTheConfigLookupToo: the bare owner/name spelling
// takes its provider from this machine's config, and this command folds
// upper-case letters everywhere else (github/Acme/App is written
// github/acme/app). The lookup itself was case-sensitive — lc.Repos is keyed by
// the operator's own spelling — so `repos add Acme/App` was refused where
// `repos add github/Acme/App` worked, for the same repository (the review
// finding on this change). Both spellings of both shapes resolve now, and the
// entry written is the canonical one either way.
func TestProjectReposEditFoldsTheConfigLookupToo(t *testing.T) {
	// The config lists this repository with upper-case letters, so the fold has
	// to work in both directions: the argument folded to the config's spelling
	// and the config's spelling folded to the argument's.
	f := newProjectFixture(t, "  Acme/Mixed: { provider: github, workflows: [web] }\n")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	for _, arg := range []string{"Acme/Mixed", "acme/mixed", "ACME/MIXED"} {
		out, _, err := execute(t, "project", "repos", "add", arg)
		if err != nil {
			t.Fatalf("repos add %s = %v: the config lookup is folded like the rest of the argument", arg, err)
		}
		allow := publishedPolicy(t, f).AllowRepos
		if !slices.Contains(allow, "github/acme/mixed") {
			t.Fatalf("repos add %s wrote %v, want the canonical entry github/acme/mixed", arg, allow)
		}
		if arg == "Acme/Mixed" {
			// The first add is the one that writes: the line names the entry
			// the operator's spelling resolved to.
			if want := "github/acme/mixed"; !strings.Contains(out, want) {
				t.Errorf("the diff for %s does not name %s:\n%s", arg, want, out)
			}
		}
	}
	// The fully canonical spelling of the same repository is the same entry,
	// not a second one: whatever the spelling, what is written is canonical.
	if _, _, err := execute(t, "project", "repos", "add", "GITHUB/Acme/Mixed"); err != nil {
		t.Fatal(err)
	}
	allow := publishedPolicy(t, f).AllowRepos
	n := 0
	for _, e := range allow {
		if e == "github/acme/mixed" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("allow_repos = %v, want exactly one github/acme/mixed entry", allow)
	}
}

// TestProjectPolicyPublishDiffNamesProvidersAndRates: the diff is the only
// place an operator reads what a publish replaces, so it prints every value
// about money and data sharing field by field — a changed base_url, a widened
// models list, a data-sharing list, a route fee, a rate — rather than a count
// of them (the review of PR #390). The secret is its NAME: the node holds no
// value, and TestPolicyCarriesNoSecretValue is the guard.
func TestProjectPolicyPublishDiffNamesProvidersAndRates(t *testing.T) {
	f := newProjectFixture(t, "model_prices:\n  deepseek/deepseek-v4-flash: { input_per_m: 1, output_per_m: 2 }\n")
	first, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatal(err)
	}
	// The first publish prints the whole block, every provider value included:
	// an operator who is publishing a policy for the first time reads the data
	// sharing and routing-fee values the node will carry.
	for _, want := range []string{
		"+ provider openrouter.models: [deepseek/*]",
		"+ provider openrouter.allow_data_to: every repository (the key is absent)",
		"+ provider openrouter.route_fee_pct: 0",
		"+ provider openrouter.secret: openrouter-key",
		"+ model_prices deepseek/deepseek-v4-flash: input_per_m 1",
	} {
		if !strings.Contains(first, want) {
			t.Errorf("the first publish's diff does not print %q:\n%s", want, first)
		}
	}
	// The operator changes a base URL, widens the models list, turns the
	// switches on, adds a data-sharing list and a route fee, and reprices the
	// model.
	setConfig(t, f, func(s string) string {
		s = strings.Replace(s, "base_url: https://openrouter.ai/api/v1", "base_url: https://openrouter.ai/api/v2", 1)
		s = strings.Replace(s, `models: ["deepseek/*"]`, `models: ["deepseek/*", "qwen/*"]`, 1)
		s = strings.Replace(s, "require_zdr: false", "require_zdr: true", 1)
		s = strings.Replace(s, "price_policy: highest", "price_policy: cheapest", 1)
		s = strings.Replace(s, "secret: openrouter-key", "secret: openrouter-key-2", 1)
		s = strings.Replace(s, "    require_zdr:", "    allow_data_to: [\"acme/app\"]\n    route_fee_pct: 5\n    require_zdr:", 1)
		s = strings.Replace(s, "input_per_m: 1, output_per_m: 2", "input_per_m: 3, output_per_m: 4", 1)
		return s
	})
	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"~ provider openrouter.base_url: https://openrouter.ai/api/v1 → https://openrouter.ai/api/v2",
		"provider openrouter.models",
		"qwen/*",
		"~ provider openrouter.require_zdr: false → true",
		"~ provider openrouter.price_policy: highest → cheapest",
		"~ provider openrouter.allow_data_to: every repository (the key is absent) → [acme/app]",
		"~ provider openrouter.route_fee_pct: 0 → 5",
		"~ model_prices deepseek/deepseek-v4-flash: input_per_m 1",
		"input_per_m 3",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the diff does not print %q:\n%s", want, out)
		}
	}
	// A changed secret is shown by NAME (which key the provider uses): the
	// node holds the name, never a value, which is what
	// TestPolicyCarriesNoSecretValue guards.
	if !strings.Contains(out, "~ provider openrouter.secret: openrouter-key → openrouter-key-2") {
		t.Errorf("the diff does not name the secret key that changed:\n%s", out)
	}
}

// TestProjectPolicyPublishCarriesTheRoutingSwitch: the routing switch belongs
// to the operator's config (phase 1b, design §6.2), and a publish is where an
// edit to it takes effect (the plan's item 9). So a publish writes the block
// from the local config, the diff names the values that changed rather than
// keeping quiet about them, `policy show` prints them, and a publish whose
// config has no switch removes the route — printing that too, because a route
// that disappears silently is a Claude call that stops being sent where the
// owner put it.
func TestProjectPolicyPublishCarriesTheRoutingSwitch(t *testing.T) {
	f := newProjectFixture(t, "routing:\n  claude:\n    via: direct\n")
	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := nodeAt(f.db.Value(projectpolicy.PathShared()), "routing", "claude", "via"); !ok || v != "direct" {
		t.Errorf("the publish did not write the operator's routing block:\n%s", nodeJSON(t, f, projectpolicy.PathShared()))
	}
	// The operator reads the values, not a count: the diff is where an added,
	// changed or removed route is visible.
	if !strings.Contains(out, "+ routing.claude.via: direct") {
		t.Errorf("the diff does not print the routing block:\n%s", out)
	}
	// And `policy show` prints it: a node with a routing block must not look
	// like a node without one.
	shown, _, err := execute(t, "project", "policy", "show")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown, "routing: claude via direct") {
		t.Errorf("policy show does not print the routing block:\n%s", shown)
	}
	// `project show` prints the same policy, and the switch is part of the
	// picture of where runs go.
	full, _, err := execute(t, "project", "show")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(full, "routing: claude via direct") {
		t.Errorf("project show does not print the routing block:\n%s", full)
	}

	// A config with no switch does not erase one: the block the node carries is
	// kept as it is (here it is inert — via: direct — and the active case is
	// TestProjectPolicyPublishRefusesToDropARoutingBlock), and the publish
	// prints no removal of a value it never held.
	cfg, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	without := strings.Replace(string(cfg), "routing:\n  claude:\n    via: direct\n", "", 1)
	if err := os.WriteFile(f.path, []byte(without), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err = execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "routing.claude") {
		t.Errorf("a publish with no switch in the config printed a routing change:\n%s", out)
	}
	if v, ok := nodeAt(f.db.Value(projectpolicy.PathShared()), "routing", "claude", "via"); !ok || v != "direct" {
		t.Errorf("the publish dropped the node's routing block:\n%s", nodeJSON(t, f, projectpolicy.PathShared()))
	}
}

// TestProjectPolicyPublishCarriesAnActiveRoute: the switch's whole point — an
// operator names Claude IDs and the route that serves them, and a publish
// delivers exactly that to the node every run reads. The route entry is in the
// config's providers (one explicit anthropic/<id>, allow_data_to written), so
// the node is self-consistent: the switch it carries is one the same node's
// providers claim, which is what the decoder now requires (D1b) and what
// publishedPolicy proves by decoding the node.
func TestProjectPolicyPublishCarriesAnActiveRoute(t *testing.T) {
	f := newProjectFixtureWith(t, claudeRouteProvidersYAML,
		"routing:\n  claude:\n    via: openrouter\n    models: [claude-sonnet-5-5]\n")
	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"+ routing.claude.via: openrouter", "+ routing.claude.models: [claude-sonnet-5-5]"} {
		if !strings.Contains(out, want) {
			t.Errorf("the diff does not print %q:\n%s", want, out)
		}
	}
	pol := publishedPolicy(t, f)
	if pol.Routing == nil || pol.Routing.Claude == nil || pol.Routing.Claude.ViaValue() != config.RouteOpenRouter {
		t.Fatalf("the published node carries routing %+v", pol.Routing)
	}
	if !pol.Routing.Claude.Names("claude-sonnet-5-5") {
		t.Errorf("the node's switch does not name the model: %+v", pol.Routing.Claude)
	}
}

// TestProjectPolicyPublishRefusesToDropARoutingBlock: the one key a publish
// will not quietly rewrite. The node carries an active route, the config sets
// no switch, and the publish is refused rather than publishing a node with the
// route turned off — a route that goes away is a Claude call that stops being
// sent where the owner put it, and this is the same quiet loosening
// `repos add|rm` refuses to make (patchPolicyJSON). The remedy the refusal
// names is real and is exercised here: writing the switch — inert, via: direct
// — publishes it, which is how routing is turned off on purpose.
func TestProjectPolicyPublishRefusesToDropARoutingBlock(t *testing.T) {
	f := newProjectFixtureWith(t, claudeRouteProvidersYAML,
		"routing:\n  claude:\n    via: openrouter\n    models: [claude-sonnet-5-5]\n")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	before := nodeJSON(t, f, projectpolicy.PathShared())

	// The operator's config loses the switch: a publish must not turn the route
	// off on its own.
	rewriteRoutingBlock(t, f, "")

	_, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatalf("the publish went through and turned the route off:\n%s", nodeJSON(t, f, projectpolicy.PathShared()))
	}
	for _, want := range []string{"routing", "cannot account for", "claude via openrouter", "via: direct"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if now := nodeJSON(t, f, projectpolicy.PathShared()); now != before {
		t.Errorf("the refused publish changed the node:\n%s", now)
	}

	// The remedy: write the switch and publish. An inert block says the route
	// is off on purpose.
	rewriteRoutingBlock(t, f, "routing:\n  claude:\n    via: direct\n")
	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "~ routing.claude.via: openrouter → direct") {
		t.Errorf("the diff does not print the switch the operator wrote:\n%s", out)
	}
	if pol := publishedPolicy(t, f); pol.Routing == nil || pol.Routing.Claude.ViaValue() != config.RouteDirect {
		t.Errorf("the node still routes: %+v\n%s", pol.Routing, nodeJSON(t, f, projectpolicy.PathShared()))
	}
}

// TestProjectPolicyPublishRefusesToTurnTheCascadeOff: deleting routing.claude
// from the operator's config means "the cascade is off", and a publish is the
// one place that difference is settled — so it refuses, rather than keeping a
// switch the config no longer writes. The cascade is not a route (it decides
// which credential carries a Claude call, never where the call goes:
// projectpolicy.RouteCascade, O3, D150), so it is refused with a sentence
// written for the cascade — what the switch hands out, and the ways to say it
// goes on purpose.
//
// This is the independent review's major finding on 653decaf: that commit read
// "a via other than direct" as the routes only, which stopped refusing the
// cascade, and the publish then kept it in silence.
func TestProjectPolicyPublishRefusesToTurnTheCascadeOff(t *testing.T) {
	f := newProjectFixtureWith(t, projectProvidersYAML, "routing:\n  claude:\n    via: cascade\n")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	pol := publishedPolicy(t, f)
	if pol.Routing == nil || pol.Routing.Claude == nil || pol.Routing.Claude.ViaValue() != config.RouteCascade {
		t.Fatalf("the fixture did not publish the cascade this case is about: %+v\n%s", pol.Routing, nodeJSON(t, f, projectpolicy.PathShared()))
	}
	before := nodeJSON(t, f, projectpolicy.PathShared())

	// The config loses the switch: that turns the cascade off, and a publish
	// must not decide it did not mean that.
	rewriteRoutingBlock(t, f, "")
	_, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatalf("the publish went through and kept the cascade the config no longer writes:\n%s", nodeJSON(t, f, projectpolicy.PathShared()))
	}
	for _, want := range []string{"credentials cascade", "claude via cascade", "via: cascade", "via: direct", "--remove-routing"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if now := nodeJSON(t, f, projectpolicy.PathShared()); now != before {
		t.Errorf("the refused publish changed the node:\n%s", now)
	}

	// The remedy that keeps it: write the switch again.
	rewriteRoutingBlock(t, f, "routing:\n  claude:\n    via: cascade\n")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatalf("the remedy the refusal names does not work: %v", err)
	}
	if pol := publishedPolicy(t, f); pol.Routing == nil || pol.Routing.Claude.ViaValue() != config.RouteCascade {
		t.Errorf("the config's cascade did not come back: %+v", pol.Routing)
	}

	// The remedy that ends it: --remove-routing, and the diff prints the drop
	// rather than making it quietly.
	rewriteRoutingBlock(t, f, "")
	out, _, err := execute(t, "project", "policy", "publish", "--remove-routing")
	if err != nil {
		t.Fatalf("--remove-routing did not drop the cascade: %v", err)
	}
	if !strings.Contains(out, "- routing.claude.via: cascade") {
		t.Errorf("the diff does not print the removal of the cascade:\n%s", out)
	}
	if _, ok := nodeAt(f.db.Value(projectpolicy.PathShared()), "routing"); ok {
		t.Errorf("the node still carries a routing block:\n%s", nodeJSON(t, f, projectpolicy.PathShared()))
	}
}

// rewriteRoutingBlock rewrites the fixture's project config with its routing:
// block (from its key to the first line that is not indented under it) replaced
// by block; "" drops it. The block the fixture wrote is the last thing in the
// file, so "the end" is the end.
func rewriteRoutingBlock(t *testing.T, f *projectFixture, block string) {
	t.Helper()
	cfg, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	kept := []string{}
	skipping := false
	for _, line := range strings.Split(string(cfg), "\n") {
		if line == "routing:" {
			skipping = true
			continue
		}
		if skipping && (line == "" || strings.HasPrefix(line, " ")) {
			continue
		}
		skipping = false
		kept = append(kept, line)
	}
	body := strings.Join(kept, "\n")
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	if err := os.WriteFile(f.path, []byte(body+block), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestProjectPolicyPublishRefusesARoutingBlockThatSaysNothing: `routing: {}` and
// `routing: {claude:}` both decode to a *non-nil* block with no Claude entry,
// and neither is the operator saying the switch is off — they wrote a block with
// nothing in it, not via: direct. A publish that read either as "off" would turn
// an active route off and only print the removal afterwards, which is the one
// thing this command refuses to do (the review of PR #414). The refusal names
// the state, the block it would drop, and the flag that makes the removal
// deliberate; that flag is then the remedy, and it does remove it.
func TestProjectPolicyPublishRefusesARoutingBlockThatSaysNothing(t *testing.T) {
	for _, c := range []struct{ name, routing string }{
		{"an empty block", "routing: {}\n"},
		{"a claude with no value", "routing:\n  claude:\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newProjectFixtureWith(t, claudeRouteProvidersYAML,
				"routing:\n  claude:\n    via: openrouter\n    models: [claude-sonnet-5-5]\n")
			if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
				t.Fatal(err)
			}
			before := nodeJSON(t, f, projectpolicy.PathShared())

			rewriteRoutingBlock(t, f, c.routing)
			_, _, err := execute(t, "project", "policy", "publish")
			if err == nil {
				t.Fatalf("the publish went through and turned the route off:\n%s", nodeJSON(t, f, projectpolicy.PathShared()))
			}
			for _, want := range []string{"names no Claude switch", "claude via openrouter", "--remove-routing"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
			if now := nodeJSON(t, f, projectpolicy.PathShared()); now != before {
				t.Errorf("the refused publish changed the node:\n%s", now)
			}

			// The remedy the refusal names: the block goes on purpose, and the
			// diff prints the removal rather than doing it quietly.
			out, _, err := execute(t, "project", "policy", "publish", "--remove-routing")
			if err != nil {
				t.Fatalf("the remedy the refusal names does not work: %v", err)
			}
			if !strings.Contains(out, "- routing.claude.via: openrouter") {
				t.Errorf("the diff does not print the removal:\n%s", out)
			}
			if _, ok := nodeAt(f.db.Value(projectpolicy.PathShared()), "routing"); ok {
				t.Errorf("the node still carries a routing block:\n%s", nodeJSON(t, f, projectpolicy.PathShared()))
			}
		})
	}
	// --remove-routing beside a config that writes the switch is two
	// instructions that cannot both be followed, and neither is guessed at.
	newProjectFixtureWith(t, claudeRouteProvidersYAML,
		"routing:\n  claude:\n    via: openrouter\n    models: [claude-sonnet-5-5]\n")
	_, _, err := execute(t, "project", "policy", "publish", "--remove-routing")
	if err == nil {
		t.Fatal("--remove-routing published over a config that writes the switch")
	}
	for _, want := range []string{"--remove-routing", "claude via openrouter"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}

// TestProjectPolicyPublishRemovesAnInertRoutingBlockWhenAsked: without an active
// route there is nothing to refuse — the block routes nothing — but dropping it
// is still a change the operator has to ask for, and the diff says so.
func TestProjectPolicyPublishRemovesAnInertRoutingBlockWhenAsked(t *testing.T) {
	f := newProjectFixtureWith(t, claudeRouteProvidersYAML,
		"routing:\n  claude:\n    via: direct\n")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if _, ok := nodeAt(f.db.Value(projectpolicy.PathShared()), "routing"); !ok {
		t.Fatal("the publish did not write the inert block the config carries")
	}
	// The config says nothing about Claude now: the inert block is kept, not
	// refused (nothing is routed by it) and not silently dropped.
	rewriteRoutingBlock(t, f, "")
	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "routing.claude") {
		t.Errorf("the publish rewrote an inert block it did not have to:\n%s", out)
	}
	if _, ok := nodeAt(f.db.Value(projectpolicy.PathShared()), "routing"); !ok {
		t.Fatal("the publish dropped the inert block without being asked")
	}
	// Asked for: the block goes, and the diff prints it.
	out, _, err = execute(t, "project", "policy", "publish", "--remove-routing")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "- routing.claude.via: direct") {
		t.Errorf("the diff does not print the removal:\n%s", out)
	}
	if _, ok := nodeAt(f.db.Value(projectpolicy.PathShared()), "routing"); ok {
		t.Errorf("the node still carries a routing block:\n%s", nodeJSON(t, f, projectpolicy.PathShared()))
	}
}

// TestProjectPolicyPublishKeepsTheRoutingKeysThisFugaroDoesNotModel: the
// routing block is shared with releases that know keys this one does not
// (phase 1b writes it, and a later one will write more of it), and a publish
// that rebuilt the node from this Fugaro's own type would drop them — the
// quiet loosening `repos add|rm` refuses to make by patching the document
// (patchPolicyJSON) and buildProjectPolicy's comment promises cannot happen.
// The diff tells the operator the routing block is kept, so it is kept whole,
// including the members this binary cannot name.
//
// Mutation (verified, run and restored): in runProjectPolicyPublish, write the
// marshalled typed policy (`json.Marshal(pol)`) instead of
// keepOldRouting's bytes: this test fails, both unmodelled keys gone and
// routing reduced to the one this Fugaro models.
func TestProjectPolicyPublishKeepsTheRoutingKeysThisFugaroDoesNotModel(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	// A routing block written the way a release that knows more of it would:
	// one unmodelled key beside a modelled one, and one unmodelled block. The
	// modelled via is `direct` because an *active* route the config does not
	// account for is refused (buildProjectPolicy), not carried: the property
	// under test here is that the block survives whole, and a node that routes
	// nothing is the one a publish from a config with no routing may keep.
	node := f.db.Value(projectpolicy.PathShared()).(map[string]any)
	node["routing"] = map[string]any{
		"claude": map[string]any{"via": "direct", "tier": "gold"},
		"gemini": map[string]any{"via": "vertex"},
	}
	f.db.Set(projectpolicy.PathShared(), node)

	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	got := f.db.Value(projectpolicy.PathShared())
	for _, path := range [][]string{
		{"routing", "claude", "via"},
		{"routing", "claude", "tier"},
		{"routing", "gemini", "via"},
	} {
		if _, ok := nodeAt(got, path...); !ok {
			t.Errorf("the publish dropped %s, a member of the routing block it says it keeps:\n%s",
				strings.Join(path, "."), nodeJSON(t, f, projectpolicy.PathShared()))
		}
	}
}

// TestProjectPolicyPublishWritesTheSwitchAndKeepsTheRestOfRouting: the switch
// the operator's config writes is this publish's to write — and the routing
// block is *also* shared with releases that know members this one does not, so
// writing one must not take the other with it. Rebuilding `routing` from the
// decoded switch drops every member this Fugaro cannot name (a `tier` inside
// claude, a whole `gemini` block beside it), with no refusal and no diff line,
// on the one document those members exist in (the review of PR #414). So the
// members the config owns — via, models — are patched into the node's own
// `claude`, the list the config does not write is removed rather than left
// stale, everything else is left as the node had it, and the change is in the
// diff.
//
// Mutation (verified, run and restored): in mergeRoutingClaude, return
// json.Marshal(c) for a member that is there as well (the old "replace the
// member" behaviour): `routing.claude.tier` and `routing.gemini` are gone and
// this test fails.
func TestProjectPolicyPublishWritesTheSwitchAndKeepsTheRestOfRouting(t *testing.T) {
	f := newProjectFixtureWith(t, twoClaudeModelsProvidersYAML,
		"routing:\n  claude:\n    via: openrouter\n    models: [claude-sonnet-5-5]\n")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	// The node as a release that knows more of the block would have left it: a
	// member inside claude this Fugaro does not model, a whole block beside it,
	// and a switch naming the other Claude ID the route claims — so this
	// publish has a switch to change as well as members to keep.
	node := f.db.Value(projectpolicy.PathShared()).(map[string]any)
	node["routing"] = map[string]any{
		"claude": map[string]any{"via": "openrouter", "models": []any{"claude-opus-5-5"}, "tier": "gold"},
		"gemini": map[string]any{"via": "vertex"},
	}
	f.db.Set(projectpolicy.PathShared(), node)

	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatal(err)
	}

	// What the config writes is what the node now says.
	pol := publishedPolicy(t, f)
	if pol.Routing == nil || pol.Routing.Claude == nil {
		t.Fatalf("the publish wrote no switch: %s", nodeJSON(t, f, projectpolicy.PathShared()))
	}
	if got := pol.Routing.Claude.ViaValue(); got != "openrouter" {
		t.Errorf("routing.claude.via = %q, want openrouter", got)
	}
	if got := pol.Routing.Claude.Models; !slices.Equal(got, []string{"claude-sonnet-5-5"}) {
		t.Errorf("routing.claude.models = %v, want the config's list (the node's stale one replaced)", got)
	}

	// What this Fugaro does not model is still there, inside claude and beside
	// it.
	got := f.db.Value(projectpolicy.PathShared())
	for _, path := range [][]string{
		{"routing", "claude", "tier"},
		{"routing", "gemini", "via"},
	} {
		if _, ok := nodeAt(got, path...); !ok {
			t.Errorf("the publish dropped %s, a member the config does not write:\n%s",
				strings.Join(path, "."), nodeJSON(t, f, projectpolicy.PathShared()))
		}
	}
	if v, _ := nodeAt(got, "routing", "claude", "tier"); v != "gold" {
		t.Errorf("routing.claude.tier = %v, want the value the node held", v)
	}

	// And the diff says so.
	if want := "~ routing.claude.models: [claude-opus-5-5] → [claude-sonnet-5-5]"; !strings.Contains(out, want) {
		t.Errorf("the diff does not print the switch change (%q):\n%s", want, out)
	}
}

// TestPolicyNodeBytesTreatsANullRoutingMemberAsAbsent: a document can carry
// `"routing": null`, and a null member inside the block. The read path reads
// both as the absent thing they are — jsonMember returns the four bytes,
// projectpolicy's IsNull calls that absent, and validRoutingMembers skips a null
// block without a word — but the write path judged the member by its bytes, so
// policyNodeBytes refused such a node with "carries a routing block this Fugaro
// cannot read the way it is written", and mergeRoutingClaude refused the null
// `claude` inside it the same way. One member read two ways by the two halves of
// one release (review finding on this change).
//
// It is exercised where it stands rather than through the command surface, for
// the reason TestPolicyNodeBytesRefusesCaseFoldedRoutingMembers gives: RTDB
// writes a null as a delete, so the service never serves such a document, which
// is exactly why the half that only *writes* documents must not be the half that
// gets this wrong. The null is what this reader already allows for a document
// that arrived by another route (a hand-written node, an import).
func TestPolicyNodeBytesTreatsANullRoutingMemberAsAbsent(t *testing.T) {
	quiet := &projectpolicy.Policy{Version: projectpolicy.Version}
	switchOn := &projectpolicy.Policy{Version: projectpolicy.Version, Routing: &config.Routing{
		Claude: &projectpolicy.ClaudeRoute{Via: config.RouteOpenRouter, Models: []string{"claude-sonnet-5-5"}},
	}}
	for _, tc := range []struct {
		name, old string
		pol       *projectpolicy.Policy
		keep      bool
	}{
		{"a null routing member, kept", `{"version": 4, "routing": null}`, quiet, true},
		{"a null routing member, replaced by the config's switch", `{"version": 4, "routing": null}`, switchOn, false},
		{"a null claude member, replaced by the config's switch", `{"version": 4, "routing": {"claude": null}}`, switchOn, false},
	} {
		node, err := policyNodeBytes(tc.pol, []byte(tc.old), true, tc.keep)
		if err != nil {
			t.Errorf("%s: policyNodeBytes = %v, want the node written", tc.name, err)
			continue
		}
		var got map[string]any
		if err := json.Unmarshal(node, &got); err != nil {
			t.Errorf("%s: the node is not a document: %v", tc.name, err)
			continue
		}
		routing, ok := nodeAt(got, "routing")
		if !tc.keep {
			// The config's switch is what the node now says, over the null it
			// found.
			if !ok {
				t.Errorf("%s: the node carries no routing block: %s", tc.name, node)
				continue
			}
			if v, _ := nodeAt(got, "routing", "claude", "via"); v != string(config.RouteOpenRouter) {
				t.Errorf("%s: routing.claude.via = %v, want the config's %q (routing %v)",
					tc.name, v, config.RouteOpenRouter, routing)
			}
			continue
		}
		// Kept, with nothing to keep: a null member is not a block to carry
		// over, and the node is the policy the publisher built.
		if ok && routing != nil {
			t.Errorf("%s: the node carries routing %v, want none", tc.name, routing)
		}
	}
}

// TestPolicyNodeBytesRefusesCaseFoldedRoutingMembers: the publish path's own gate
// on the member names it is about to patch into. It is not reachable through the
// command surface — Decode refuses such a node first (validRoutingMembers calls
// the same check) — so it is exercised where it stands, and that is the point: a
// writer must not depend on a reader having run, and a publish that patched an
// exact `via` beside a `VIA` (jsonMember matches keys exactly) would leave a block
// whose route encoding/json decides by the order the keys were written in.
func TestPolicyNodeBytesRefusesCaseFoldedRoutingMembers(t *testing.T) {
	pol := &projectpolicy.Policy{Version: projectpolicy.Version}
	for name, old := range map[string]string{
		"a case-folded via":    `{"version": 4, "routing": {"claude": {"via": "direct", "VIA": "openrouter"}}}`,
		"models twice":         `{"version": 4, "routing": {"claude": {"via": "direct", "models": ["claude-sonnet-5-5"], "models": []}}}`,
		"a case-folded claude": `{"version": 4, "routing": {"CLAUDE": {"via": "direct"}}}`,
	} {
		if _, err := policyNodeBytes(pol, []byte(old), true, true); err == nil || !strings.Contains(err.Error(), "routing block") {
			t.Errorf("%s: policyNodeBytes = %v, want a refusal naming the block", name, err)
		}
	}
	// The other side: a block this Fugaro reads as written is patched as before.
	if _, err := policyNodeBytes(pol, []byte(`{"version": 4, "routing": {"claude": {"via": "direct"}}}`), true, true); err != nil {
		t.Errorf("a well-spelled block: policyNodeBytes = %v, want it written", err)
	}
}

// TestProjectPolicyPublishRefusesAnOlderNodeWithAWorkingRemedy: a node this
// binary cannot read as its own shape — hand-written with no version, or with
// an older one — is refused rather than replaced, but the refusal must name a
// remedy that works. "Publish again" is the one it cannot: the publish is what
// is being refused. Removing the node is the path that does.
func TestProjectPolicyPublishRefusesAnOlderNodeWithAWorkingRemedy(t *testing.T) {
	for name, node := range map[string]map[string]any{
		"no version":    {"budget": map[string]any{"mode": "enforce"}},
		"version zero":  {"version": 0, "budget": map[string]any{"mode": "enforce"}},
		"a later shape": {"version": projectpolicy.Version - 1, "budget": map[string]any{"mode": "enforce"}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newProjectFixture(t, "")
			f.db.Set(projectpolicy.PathShared(), node)
			_, _, err := execute(t, "project", "policy", "publish")
			if err == nil {
				t.Fatal("publishing over an older node was allowed")
			}
			if !strings.Contains(err.Error(), "--delete") {
				t.Errorf("the refusal names no remedy that works: %v", err)
			}
			if v := f.db.Value(projectpolicy.PathShared()); v == nil {
				t.Fatal("the node was removed by a refused publish")
			}
			// And the remedy it names works.
			if _, _, err := execute(t, "project", "policy", "publish", "--delete", "--yes"); err != nil {
				t.Fatalf("the remedy the refusal names does not work: %v", err)
			}
			if f.db.Value(projectpolicy.PathShared()) != nil {
				t.Fatal("the remedy did not remove the node")
			}
			if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
				t.Fatalf("publishing after the remedy: %v", err)
			}
		})
	}
}

// TestProjectPolicyDeleteRemovesOrphanChildrenWithoutASharedNode: a delete that
// failed on the shared node leaves the children behind, and the next --delete
// must still remove them: reporting "nothing to delete" on the strength of the
// absent shared node would leave governance text under config/policy/repos
// forever, which a later release may read (the review of PR #390).
func TestProjectPolicyDeleteRemovesOrphanChildrenWithoutASharedNode(t *testing.T) {
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
	// The half-finished delete: the shared node is gone, the child is not.
	f.db.Set(projectpolicy.PathShared(), nil)

	out, _, err := execute(t, "project", "policy", "publish", "--delete")
	if err != nil {
		t.Fatalf("--delete refused to clean up after an earlier one: %v\n%s", err, out)
	}
	if f.db.Value(child) != nil {
		t.Errorf("%s is still there: the orphan is what this delete is for", child)
	}
	if !strings.Contains(out, child) {
		t.Errorf("the output does not name the node it removed:\n%s", out)
	}
	// Nothing is left, and a second --delete says so rather than claiming to
	// have removed something.
	out, _, err = execute(t, "project", "policy", "publish", "--delete")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "nothing to delete") {
		t.Errorf("a second --delete does not say there is nothing to delete:\n%s", out)
	}
}

// TestProjectReposRmOfAPatternWithABroaderPatternLeftSaysSo: removing a
// pattern while a broader one remains refuses nothing — github/*/* admits
// every repository github/acme/* admitted — so the confirmation must not
// claim a stop it does not make (the review of PR #390).
//
// Mutation (verified, run and restored): restore admittedBy's old
// `for _, p := range rest { if p == "*" }` branch for patterns: this test
// fails, reading "its runs are refused from now on" while github/*/* still
// admits every one of them.
func TestProjectReposRmOfAPatternWithABroaderPatternLeftSaysSo(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"github/*/*", "github/acme/*"} {
		if _, _, err := execute(t, "project", "repos", "add", p); err != nil {
			t.Fatal(err)
		}
	}
	typed(t)
	out, _, err := executeStdin(t, "aurora\n", "project", "repos", "rm", "github/acme/*")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "github/*/* still admits it") {
		t.Errorf("the confirmation does not name the broader pattern that still admits it:\n%s", out)
	}
	if strings.Contains(out, "refused from now on") {
		t.Errorf("the confirmation says the runs are refused though github/*/* is on the list:\n%s", out)
	}
	allow := publishedPolicy(t, f).AllowRepos
	if slices.Contains(allow, "github/acme/*") {
		t.Errorf("allow_repos = %v, want the pattern removed", allow)
	}
	// The point of the sentence: every repository the removed pattern admitted
	// is still admitted, by the entry the confirmation named.
	for _, r := range []string{"github/acme/app", "github/acme/anything", "github/other/thing"} {
		if !projectpolicy.AdmitRepo(allow, r) {
			t.Errorf("allow_repos = %v no longer admits %s, which the confirmation said it keeps", allow, r)
		}
	}
}

// TestProjectReposRmOfAPatternWhoseNameCarriesAStarSaysSo: the same sentence
// through the command, for the entry whose *name* carries a "*" rather than
// being one (github/acme/*-web). github/*/* still admits every repository that
// entry admitted — its third segment is "*-web", which the pattern's "*"
// matches — so the confirmation must not claim otherwise.
//
// Mutation (verified, run and restored): test the two positions for equality
// before the pattern's "*" in matchSeg (the order this test was written
// against) — this fails, reading "its runs are refused from now on".
func TestProjectReposRmOfAPatternWhoseNameCarriesAStarSaysSo(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"github/*/*", "github/acme/*-web"} {
		if _, _, err := execute(t, "project", "repos", "add", p); err != nil {
			t.Fatal(err)
		}
	}
	typed(t)
	out, _, err := executeStdin(t, "aurora\n", "project", "repos", "rm", "github/acme/*-web")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "github/*/* still admits it") {
		t.Errorf("the confirmation does not name the broader pattern that still admits it:\n%s", out)
	}
	if strings.Contains(out, "refused from now on") {
		t.Errorf("the confirmation says the runs are refused though github/*/* is on the list:\n%s", out)
	}
	allow := publishedPolicy(t, f).AllowRepos
	if slices.Contains(allow, "github/acme/*-web") {
		t.Errorf("allow_repos = %v, want the entry removed", allow)
	}
	for _, r := range []string{"github/acme/app-web", "github/acme/-web", "github/other/-web"} {
		if !projectpolicy.AdmitRepo(allow, r) {
			t.Errorf("allow_repos = %v no longer admits %s, which the confirmation said it keeps", allow, r)
		}
	}
}

// TestProjectReposRmOfANameOnlyAPatternAdmitsSaysThePatternKeepsIt: the name
// is not an entry, so nothing is removed — but a pattern on the list admits
// it, so its runs keep running, and "nothing to remove" alone reads as if
// they had stopped (the review of PR #390).
//
// Mutation (verified, run and restored): put back the plain
// `userErr("%s is not on the allow-list ...: nothing to remove")` return in
// runProjectReposEdit: this test fails, finding no mention of the pattern.
func TestProjectReposRmOfANameOnlyAPatternAdmitsSaysThePatternKeepsIt(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "project", "repos", "add", "github/acme/*"); err != nil {
		t.Fatal(err)
	}
	before := publishedPolicy(t, f).AllowRepos
	_, _, err := execute(t, "project", "repos", "rm", "github/acme/extra")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error", err)
	}
	if !strings.Contains(err.Error(), "github/acme/* admits it") || !strings.Contains(err.Error(), "keep running") {
		t.Errorf("the refusal does not say the pattern keeps admitting it: %v", err)
	}
	if strings.Contains(err.Error(), "nothing to remove") {
		t.Errorf("the refusal says only that there is nothing to remove: %v", err)
	}
	if got := publishedPolicy(t, f).AllowRepos; !slices.Equal(got, before) {
		t.Errorf("allow_repos = %v, want the list unchanged (%v)", got, before)
	}
}

// TestProjectReposEditPrintsTheNewListOnlyAfterTheWrite: the edit printed
// "allow_repos: [before] → [after]" before the ETag-guarded PutIfMatch, so a
// write that failed — a policy that changed while the command ran, or a
// credential that may not write the node — was reported to the operator as if
// the list had been replaced, in the command whose whole output is that one
// line and "set <node>". The diff line is a claim about the node.
func TestProjectReposEditPrintsTheNewListOnlyAfterTheWrite(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		stdin string
	}{
		{"add", []string{"project", "repos", "add", "github/acme/new"}, ""},
		{"rm", []string{"project", "repos", "rm", "github/acme/app"}, "aurora\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProjectFixture(t, "")
			if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
				t.Fatal(err)
			}
			before := publishedPolicy(t, f).AllowRepos
			// The one write the edit ends on is refused.
			f.db.DenyNext(1)
			var out string
			var err error
			if tc.stdin != "" {
				typed(t)
				out, _, err = executeStdin(t, tc.stdin, tc.args...)
			} else {
				out, _, err = execute(t, tc.args...)
			}
			if err == nil {
				t.Fatalf("the refused write was reported as a success:\n%s", out)
			}
			if strings.Contains(out, "allow_repos:") {
				t.Errorf("the edit printed the new allow-list before the write that would have set it:\n%s", out)
			}
			if strings.Contains(out, "set "+projectpolicy.PathShared()) {
				t.Errorf("the edit says it set the node a refused write left untouched:\n%s", out)
			}
			if got := publishedPolicy(t, f).AllowRepos; !slices.Equal(got, before) {
				t.Errorf("allow_repos = %v, want the list unchanged (%v)", got, before)
			}
		})
	}
}

// TestProjectReposEditRefusesTheSharedConfig: repos add and rm write the
// published policy, so they are writers like `policy publish` and take its
// two refusals — the shared config is not an operator's own installation, and
// the database must be this project's (the review of PR #390).
//
// Mutation (verified, run and restored): call openBudget instead of
// openProjectBudget in runProjectReposEdit: this test fails, the command
// reaching the network instead of refusing.
func TestProjectReposEditRefusesTheSharedConfig(t *testing.T) {
	f := sharedEnd2End(t)
	t.Chdir(gitCheckout(t, filepath.Join(f.dir, "app"), "version: 1\nproject: aurora\ngcp_project: proj-1234\n"))
	for _, args := range [][]string{
		{"project", "repos", "add", "github/acme/app"},
		{"project", "repos", "rm", "github/acme/app"},
	} {
		_, _, err := execute(t, args...)
		if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), errSharedWrite) {
			t.Errorf("%v: err = %v, want the shared-config refusal", args, err)
		}
	}
}

// TestProjectPolicyPublishDiffDoesNotPrintAModelPricesCount: the diff names
// every price override by model (priceLines), so a summary line reading
// "model_prices: 1" says nothing an operator can use and reads like a rate
// (the review of PR #390: the line the block-level comment beside it already
// called out as useless).
//
// Mutation (verified, run and restored): put back
// `"model_prices": fmt.Sprintf("%d", len(p.Prices))` in policyLines: this
// test fails, finding "model_prices: 1" in the diff.
func TestProjectPolicyPublishDiffDoesNotPrintAModelPricesCount(t *testing.T) {
	newProjectFixture(t, "model_prices:\n  deepseek/deepseek-v4-flash: { input_per_m: 1, output_per_m: 2 }\n")
	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "model_prices: ") {
		t.Errorf("the diff prints a model_prices count line:\n%s", out)
	}
	if !strings.Contains(out, "+ model_prices deepseek/deepseek-v4-flash: input_per_m 1") {
		t.Errorf("the diff no longer names the override it has:\n%s", out)
	}
}

// TestProjectPolicyPublishKeepsCloneURLs: the clone_urls block is data the
// runner obeys (a project job's clone URL, plan task 3.3 step 3) and no command
// of this release writes one, so a publish that built the node from its own type
// would drop an operator's entry — and a dropped entry is silently a repository
// that clones from its provider's usual host instead of the mirror the project
// chose to route it through. The block is kept, the diff says so (with the URL,
// which is credential-free by the node's own validation), and `policy show`
// prints it, so a node with an entry does not read like one without.
func TestProjectPolicyPublishKeepsCloneURLs(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	// The entry an operator (or a later release) put there: this project's own
	// repository, on the provider's own host, under the escaped member name the
	// node must use (projectpolicy's CloneURLs: RTDB refuses a key with a slash
	// in it, and a canonical repository name has two).
	const (
		canonical = "github/acme/app"
		member    = "github%2Facme%2Fapp"
		url       = "https://github.com/acme/app.git"
	)
	node := f.db.Value(projectpolicy.PathShared()).(map[string]any)
	node["clone_urls"] = map[string]any{member: url}
	f.db.Set(projectpolicy.PathShared(), node)

	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatal(err)
	}
	got := f.db.Value(projectpolicy.PathShared())
	if v, ok := nodeAt(got, "clone_urls", member); !ok || v != url {
		t.Errorf("the publish dropped or changed the clone_urls block:\n%s", nodeJSON(t, f, projectpolicy.PathShared()))
	}
	// The decoded policy the rest of this binary reads still carries it: the
	// runner looks the entry up by canonical repository name.
	pol := publishedPolicy(t, f)
	if pol.CloneURL(canonical) != url {
		t.Errorf("CloneURL(%q) = %q, want %q", canonical, pol.CloneURL(canonical), url)
	}
	if !strings.Contains(out, "clone_urls "+canonical+" kept: "+url) {
		t.Errorf("the diff does not say the clone_urls entry is kept:\n%s", out)
	}
	shown, _, err := execute(t, "project", "policy", "show")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown, canonical+" → "+url) {
		t.Errorf("policy show does not print the clone_urls entry:\n%s", shown)
	}
}

// TestPolicyRefusalsEscapeTheNodesKeyNames: the top-level key names a refusal
// prints come from the node, which is a hand-writable object
// (projectpolicy.Policy.UnknownKeys is whatever the strict decode could not
// place). A member name is legal JSON whatever it holds: a bidi override
// (U+202E), a zero-width space (U+200B) or a line separator is not
// unicode.IsControl, and projectpolicy's checkTopLevelNames refuses duplicates
// and case-variants of the keys this Fugaro knows, never a name's shape —
// so a node an operator did not write can rewrite the terminal line it is
// printed in, which is exactly why the rest of this package prints a name of
// that provenance through pluginwire.Printable.
//
// All three readers of the node are checked, because each words its own
// refusal: refuseUnknownPolicyKeys (policy show, project show, repos ls),
// publishedPolicyOf (the publish, which drops the keys), and projectPolicyRead
// — the launch gate's own read, which is what refuses a `fugaro run` before
// anything of it exists.
//
// Mutation (verified, run and restored): print the keys with
// `strings.Join(p.UnknownKeys, ", ")` again — each join belongs to its own
// reader, so the half that reads through it fails with the raw bidi override
// back in the message (in project_cmd.go it takes the first two halves, in
// projectPolicyRead the launch gate's).
func TestPolicyRefusalsEscapeTheNodesKeyNames(t *testing.T) {
	f := newProjectFixture(t, "")
	// The name the node carries: a bidi override inside it and a zero-width
	// space after the visible word, which is how a key named `future_key`
	// prints as something else entirely.
	const hostile = "future‮key​"
	// The form pluginwire.Printable must produce, written out independently of
	// it: the runes as their `\u` escape text, which is what a terminal prints
	// without acting on.
	const escaped = "future\\u202ekey\\u200b"
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app"},
		hostile:       true,
	})
	for _, tc := range []struct {
		what   string
		args   []string
		wantIn string
	}{
		{"policy show, which refuses the node it cannot read whole", []string{"project", "policy", "show"}, "never takes effect"},
		{"the publish, which refuses to drop the keys it does not know", []string{"project", "policy", "publish", "--yes"}, "publishing would drop them"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			_, _, err := execute(t, tc.args...)
			if err == nil {
				t.Fatalf("%s: %v was accepted", tc.what, tc.args)
			}
			assertEscapedRefusal(t, err.Error(), tc.wantIn, hostile, escaped)
			assertNamesTheBinaryUpgrade(t, err.Error())
		})
	}
	t.Run("the launch gate, which refuses a run against the node", func(t *testing.T) {
		lc, err := localcfg.Load(f.path)
		if err != nil {
			t.Fatal(err)
		}
		// Called the way run_allowlist.go's launchPolicyGates calls it: the
		// memoized read, its error worded by policyReadErr, which is the
		// sentence a refused `fugaro run` prints.
		var once policyReadOnce
		_, _, rerr := once.read(context.Background(), lc)
		err = policyReadErr(rerr)
		if err == nil {
			t.Fatal("a launch against a node with an unknown key was accepted")
		}
		assertEscapedRefusal(t, err.Error(), "never takes effect", hostile, escaped)
		assertNamesTheBinaryUpgrade(t, err.Error())
	})
}

// assertNamesTheBinaryUpgrade: a refusal whose remedy is a newer Fugaro names
// the package manager's command, because `fugaro upgrade` never upgrades the
// binary it runs — it moves a checkout onto the installed release. The reader
// of such a refusal is on the older side, so a fix naming `fugaro upgrade`
// leaves them with no move at all.
func assertNamesTheBinaryUpgrade(t *testing.T, msg string) {
	t.Helper()
	if !strings.Contains(msg, "brew upgrade dimipaun/tap/fugaro") {
		t.Errorf("the refusal does not name the binary upgrade: %s", msg)
	}
	if strings.Contains(msg, "fugaro upgrade") {
		t.Errorf("the refusal sends the operator to fugaro upgrade, which never upgrades fugaro itself: %s", msg)
	}
}

// assertEscapedRefusal checks one refusal of the unknown policy keys: that it
// is the refusal about them (wantIn), that the node's name is printed escaped,
// and that the raw name is nowhere in the line.
func assertEscapedRefusal(t *testing.T, msg, wantIn, hostile, escaped string) {
	t.Helper()
	if !strings.Contains(msg, wantIn) {
		t.Fatalf("the refusal is not the one about the unknown key (%q): %s", wantIn, msg)
	}
	if !strings.Contains(msg, escaped) {
		t.Errorf("the node's key name is not escaped in the refusal: %q missing from %s", escaped, msg)
	}
	if strings.Contains(msg, hostile) {
		t.Errorf("the node's key name reaches the terminal raw, so the line's reading is the node's to choose: %q in %s", hostile, msg)
	}
}

// TestProjectPolicyPublishCarriesTheProjectBaseBranch: the installation
// config's top-level base_branch is published into the policy node, which is
// where a launcher whose own config sets none and the runner read it, and a
// config that sets none publishes no member at all (the node then means "no
// project default", and the value is main).
func TestProjectPolicyPublishCarriesTheProjectBaseBranch(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		f := newProjectFixture(t, "base_branch: trunk\n")
		if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
			t.Fatal(err)
		}
		if p := publishedPolicy(t, f); p.BaseBranch != "trunk" {
			t.Errorf("BaseBranch = %q, want trunk", p.BaseBranch)
		}
	})
	t.Run("unset", func(t *testing.T) {
		f := newProjectFixture(t, "")
		if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
			t.Fatal(err)
		}
		if p := publishedPolicy(t, f); p.BaseBranch != "" {
			t.Errorf("BaseBranch = %q, want \"\"", p.BaseBranch)
		}
		if v := f.db.Value(projectpolicy.PathShared()); v != nil {
			raw, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "base_branch") {
				t.Errorf("the node carries a base_branch member the config does not set:\n%s", raw)
			}
		}
	})
}

// TestReviewFloorPublishedToPolicyNode is D722's publish half: the owner's
// review-level floor travels from his project config into the policy node —
// the one object only the owner writes and the runner fails closed on — and
// nowhere else. A project that sets no floor publishes no member at all, so
// its node is byte for byte what it was before the key existed.
func TestReviewFloorPublishedToPolicyNode(t *testing.T) {
	f := newProjectFixture(t, "review_floor:\n  pro: [l2]\n  critical: [l2, l3]\n")
	publishOut, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatal(err)
	}
	// The diff warns that the member stops older readers (the plan's C1.2 skew
	// section): an older CLI refuses every launch and publish of this project,
	// and an older runner image warns that the key has no effect and runs the
	// levels anyway, so the floor does not bind there until the image is
	// upgraded (L1.2's gate on the resolved levels is what refuses that image,
	// and it is not in this change).
	if !strings.Contains(publishOut, "carries a review_floor member") {
		t.Errorf("the publish does not warn that the floor stops older readers:\n%s", publishOut)
	}
	p := publishedPolicy(t, f)
	if len(p.ReviewFloor) != 2 {
		t.Fatalf("ReviewFloor = %v, want the two modes the config writes", p.ReviewFloor)
	}
	if got, want := p.ReviewFloor[rigor.Pro], []policy.Level{policy.LevelL2}; !slices.Equal(got, want) {
		t.Errorf("ReviewFloor[pro] = %v, want %v", got, want)
	}
	if got, want := p.ReviewFloor[rigor.Critical], []policy.Level{policy.LevelL2, policy.LevelL3}; !slices.Equal(got, want) {
		t.Errorf("ReviewFloor[critical] = %v, want %v", got, want)
	}
	if raw := nodeJSON(t, f, projectpolicy.PathShared()); !strings.Contains(raw, `"review_floor"`) {
		t.Errorf("the node does not carry the floor:\n%s", raw)
	}

	// A project that writes none: no member, and no other change to the node.
	g := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if raw := nodeJSON(t, g, projectpolicy.PathShared()); strings.Contains(raw, "review_floor") {
		t.Errorf("a project that sets no floor published one:\n%s", raw)
	}
	if p := publishedPolicy(t, g); p.ReviewFloor != nil {
		t.Errorf("ReviewFloor = %v, want nil", p.ReviewFloor)
	}
}

// TestPublishKeepsReviewFloorTheConfigIsSilentAbout is the review finding this
// behaviour exists for: a publish from a machine whose config says nothing
// about the floor does not take the node's away. The floor is a minimum the
// owner set to bind the launchers of his own project, and the publish that
// would drop it is the one he did not mean — a second machine whose file
// predates the key, or a key deleted while tidying one. The diff says it was
// kept, because the value does not change and no line of the diff above would
// mention it otherwise, and an operator who deleted the key would read that
// silence as "it is gone".
func TestPublishKeepsReviewFloorTheConfigIsSilentAbout(t *testing.T) {
	f := newProjectFixture(t, "review_floor:\n  pro: [l2]\n")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	rewriteProjectConfig(t, f, "")
	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatal(err)
	}
	p := publishedPolicy(t, f)
	if len(p.ReviewFloor) != 1 || !slices.Equal(p.ReviewFloor[rigor.Pro], []policy.Level{policy.LevelL2}) {
		t.Errorf("the second publish changed the floor: ReviewFloor = %v, want pro: [l2]", p.ReviewFloor)
	}
	if !strings.Contains(out, "review_floor kept: pro: l2") {
		t.Errorf("the publish does not say the floor was kept:\n%s", out)
	}
}

// TestPublishDropsReviewFloorOnPurpose: `review_floor: {}` is how the owner
// takes a published floor away. Silence about the key keeps the node's floor
// (the test above), so the written empty object is the spelling that has to
// mean "none" — and the diff names the change like any other value's, which is
// what makes the removal a decision the operator reads rather than one that
// happened behind the diff.
func TestPublishDropsReviewFloorOnPurpose(t *testing.T) {
	f := newProjectFixture(t, "review_floor:\n  pro: [l2]\n  critical: [l2, l3]\n")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	rewriteProjectConfig(t, f, "review_floor: {}\n")
	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatal(err)
	}
	if p := publishedPolicy(t, f); p.ReviewFloor != nil {
		t.Errorf("ReviewFloor = %v, want none after a written empty object", p.ReviewFloor)
	}
	if raw := nodeJSON(t, f, projectpolicy.PathShared()); strings.Contains(raw, "review_floor") {
		t.Errorf("the node still carries the member:\n%s", raw)
	}
	if !strings.Contains(out, "~ review_floor:") || !strings.Contains(out, "→ none") {
		t.Errorf("the diff does not name the floor going away:\n%s", out)
	}
}

// TestPolicyShowPrintsReviewFloor: `policy show` is the audit surface, and the
// floor is a member a publish decides — so it is a row of that output whether
// the node carries one or not, the way the Bitbucket block's row is (the
// review of #1329). Without it the operator cannot confirm that the floor he
// published is the floor the runs read.
func TestPolicyShowPrintsReviewFloor(t *testing.T) {
	newProjectFixture(t, "review_floor:\n  pro: [l2]\n  critical: [l2, l3]\n")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "project", "policy", "show")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "review_floor: critical: l2, l3; pro: l2") {
		t.Errorf("policy show does not print the floor:\n%s", out)
	}

	newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	out, _, err = execute(t, "project", "policy", "show")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "review_floor: none (a run reviews what its rigor mode and the repository ask for)") {
		t.Errorf("policy show does not say a policy carries no floor:\n%s", out)
	}
}

// TestReviewFloorNotInShared: the floor is the owner's, and the shared config
// is the file any launcher with objectAdmin on the runs bucket can write. A
// floor published there would be the launcher the floor exists to bind,
// setting it (D722, G1, docs/design/bucket-iam.md §10 L1, L2), so the reader
// refuses the key — the way it refuses bitbucket_api_roots — and the publisher
// never writes it (localcfg.Config.Shared drops it).
func TestReviewFloorNotInShared(t *testing.T) {
	if !slices.Contains(sharedForbidden, "review_floor") {
		t.Fatal("sharedForbidden does not name review_floor, so a published floor would be read")
	}
	data := validShared() + "review_floor:\n    pro: [l2]\n"
	_, err := ParseShared([]byte(data), sharedAnchor)
	if err == nil {
		t.Fatal("a published shared config carrying review_floor: was accepted")
	}
	for _, want := range []string{"review_floor", "never published", "fugaro update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}
