package cli

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/routing"
	"github.com/dimipaun/fugaro/internal/task"
)

// update_route_test.go is the credential route's publish (update_route.go, WS9
// of docs/workspace-service/implementation-plan.md): the stage a MEMBER's own
// `fugaro update` runs, driven here directly. What it publishes is one record
// per repository at creds.RoutePath, and what the six tests below pin is the
// record itself — the address from the pin, the per-repository and per-kind
// answer, the project's own copy winning, the report the member reads, the
// project that is in no workspace, and the removal that leaving makes.
//
// Every test is hermetic: the gcpfake RTDB of the fixture installation, the
// fixture's own config home and Secret Manager, and no cloud.

// routeAddress is the address the member's pin carries, which is what the
// record publishes: a URL that is no credential and names no real service.
const routeAddress = "https://creds.example.com"

// routeMemberPin is the member's own half of a membership as W1 writes it: the
// membership key and the pin's four values, with the address that makes this
// project publish a route (R3).
const routeMemberPin = "workspace: workspace-alpha\n" +
	"workspace_pin:\n" +
	"  firebase_project: project-ws\n" +
	"  issuer: https://securetoken.google.com/project-ws\n" +
	"  assertion_key: marker-assertion-key\n" +
	"  service_account: marker-workspace@project-ws.iam.gserviceaccount.com\n" +
	"  service_address: " + routeAddress + "\n"

// routeCredsService is the block that makes the fixture's bundle readable at
// all: without CredentialsService there is no document to read and no route to
// derive (readCredentialsBundle's own first answer).
const routeCredsService = "credentials_service: { url: " + credentialsServiceURL + " }\n"

// routeRig is the stage, its engine and the command a run's own streams come
// from (wsUpdateRig's shape, for the MEMBER's record).
type routeRig struct {
	f   *projectFixture
	sm  *gcpfake.Secrets
	s   *routeStage
	cmd *cobra.Command
	out *syncBuf
}

// newRouteRig builds the fixture installation — a member's, with the pin and
// the credentials service's block — and the stage over it.
func newRouteRig(t *testing.T, extra string) *routeRig {
	t.Helper()
	sm := gcpfake.NewSecrets(t)
	f := newProjectFixture(t, routeMemberPin+routeCredsService+extra)
	// The fixture's endpoint, as proxySwitchFixture sets it: the bundle read
	// goes through this fake and nowhere else.
	rewriteConfigLine(t, "no_auth: true", "no_auth: true, secret_manager: "+sm.URL+"/")
	return newRouteRigOn(t, f, sm)
}

// newRouteRigOn is the stage over an installation a test already built: the
// state a second `fugaro update` reads, which is how the idempotence row below
// asks a fresh run against the same database.
func newRouteRigOn(t *testing.T, f *projectFixture, sm *gcpfake.Secrets) *routeRig {
	t.Helper()
	for _, k := range agentMarkers {
		t.Setenv(k, "")
	}
	// The one command every sentence names is the program as the user ran it
	// (selfCommand), which under `go test` is the test binary's path: pinned to
	// the bare name, so the test reads the sentence an owner reads.
	old := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = old })
	lc, err := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	out := &syncBuf{}
	cmd := NewRootCmd()
	cmd.SetOut(out)
	cmd.SetErr(&syncBuf{})
	cmd.SetIn(strings.NewReader(""))
	run := newInitRun(cmd, &initOptions{door: updateDoor.name})
	run.setProject(lc)
	return &routeRig{
		f: f, sm: sm, cmd: cmd, out: out,
		s: newCredentialsRouteStage(&initEngine{r: run, lc: lc, path: os.Getenv("FUGARO_CONFIG")}),
	}
}

func (r *routeRig) check(t *testing.T) initflow.Status {
	t.Helper()
	st, err := r.s.Check(context.Background())
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	return st
}

// planText is the plan as the stage printed it, with the terminal a person's
// run has (the stage shows its diff there and nowhere else).
func (r *routeRig) planText(t *testing.T) string {
	t.Helper()
	if _, err := r.s.Plan(context.Background(), initflow.Env{Interactive: true}); err != nil {
		t.Fatalf("plan: %v", err)
	}
	return r.out.String()
}

// apply is the write, behind the run's own answer, with the verification the
// loop makes after it.
func (r *routeRig) apply(t *testing.T) (initflow.Outcome, string) {
	t.Helper()
	oc, err := r.s.Apply(context.Background(), initflow.Env{Yes: true})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := r.s.Verify(context.Background()); err != nil {
		t.Fatalf("verify: %v", err)
	}
	return oc, r.out.String()
}

// routeBundleDoc is one bundle document, whose lines are the credential entries
// the caller names: `fugaro creds set`'s own shape (readCredentialsBundle's
// parse through credsvc.ParseBundle).
func routeBundleDoc(t *testing.T, entries ...string) []byte {
	t.Helper()
	return []byte(strings.Join(entries, "\n") + "\n")
}

// routeSeedBundle stores the document as the installation's bundle, so the
// reading the stage makes is a reading of it.
func routeSeedBundle(t *testing.T, sm *gcpfake.Secrets, entries ...string) {
	t.Helper()
	seedProxyBundle(t, sm, routeBundleDoc(t, entries...))
}

// routeSeedPolicy seeds the published policy node the derivation reads: the
// allow-list, and the accounts block whose per-account allow_repos is what
// decides whether the project's own model credential serves a repository
// (repoAdmissions' own inversion). It is seeded rather than published because
// what this stage asks of the node is one reading of it, and `project policy
// publish` has its own tests.
func routeSeedPolicy(t *testing.T, f *projectFixture, accounts map[string][]string, allow []string, extra map[string]any) {
	t.Helper()
	node := map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": allow,
	}
	if len(accounts) > 0 {
		acct := map[string]any{}
		for name, repos := range accounts {
			acct[name] = map[string]any{"allow_repos": repos}
		}
		node["accounts"] = acct
	}
	for k, v := range extra {
		node[k] = v
	}
	f.db.Set(projectpolicy.PathShared(), node)
}

// routeSlug is the key one repository's record is published at (task.Slug, as
// the run's own token carries it).
func routeSlug(t *testing.T, provider, repo string) string {
	t.Helper()
	slug, err := task.Slug(provider, repo)
	if err != nil {
		t.Fatal(err)
	}
	return slug
}

// routeOn is the record published for one repository, decoded.
func routeOn(t *testing.T, f *projectFixture, provider, repo string) creds.Route {
	t.Helper()
	path := creds.RoutePath(routeSlug(t, provider, repo))
	v := f.db.Value(path)
	if v == nil {
		t.Fatalf("no credential route is published at %s", path)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	r, found, err := creds.DecodeRoute(raw)
	if err != nil || !found {
		t.Fatalf("the record at %s does not decode (found=%v): %v", path, found, err)
	}
	return r
}

// TestUpdatePublishesTheRoute is WS9's red step: a member whose bundle holds no
// credential the route can use publishes the record all the same — the address
// from its pin, and per kind either its own copy or the workspace's service.
//
// The bundle holds one entry and it is not one of the three kinds a route
// decides (config.ReviewSigningKeySecret, which no run's call goes out on), so
// every kind either repository can use is the workspace's:
//
//   - acme/app is GitHub, so its forge kind is the GitHub App key, which the
//     bundle does not hold: the workspace's service serves it. Its Bitbucket
//     kind is the other forge's, which reaches no repository of this
//     installation: the project's own copy (which is to say, nothing at all).
//   - its model kind is no account's either: the workspace's service.
func TestUpdatePublishesTheRoute(t *testing.T) {
	r := newRouteRig(t, "")
	routeSeedBundle(t, r.sm, config.ReviewSigningKeySecret("alice")+": "+credsValue(t, "review"))
	routeSeedPolicy(t, r.f, map[string][]string{config.AnthropicKeyAccount: {"github/acme/app"}},
		[]string{"bitbucket/acme/other", "github/acme/app"}, nil)

	st := r.check(t)
	if st.State != initflow.Todo {
		t.Fatalf("check = %+v, want Todo: nothing is published for this member yet", st)
	}
	if _, out := r.apply(t); !strings.Contains(out, creds.RoutePathParent()) {
		t.Errorf("the confirmation does not name the subtree the records live in:\n%s", out)
	}

	app := routeOn(t, r.f, "github", "acme/app")
	if app.Version != creds.RouteVersion {
		t.Errorf("the record's version = %d, want %d", app.Version, creds.RouteVersion)
	}
	if app.Address != routeAddress {
		t.Errorf("the record's address = %q, want the pin's own %q (R3)", app.Address, routeAddress)
	}
	if got := app.Holder(creds.KindModel); got != creds.HolderWorkspace {
		t.Errorf("app's model kind is served by %s, want the workspace's service: the bundle holds no model credential the policy admits for it", got)
	}
	if got := app.Holder(creds.KindGitHub); got != creds.HolderWorkspace {
		t.Errorf("app's github kind is served by %s, want the workspace's service: the bundle holds no App key", got)
	}
	if got := app.Holder(creds.KindBitbucket); got != creds.HolderProject {
		t.Errorf("app's bitbucket kind is served by %s, want the project's own copy: the other forge's kind reaches no repository of this installation", got)
	}

	// The other repository has no credential of its own either, and its
	// Bitbucket kind is its own forge's — so the two records differ in exactly
	// that one kind, which is what makes the record per repository.
	other := routeOn(t, r.f, "bitbucket", "acme/other")
	if got := other.Holder(creds.KindModel); got != creds.HolderWorkspace {
		t.Errorf("other's model kind is served by %s, want the workspace's service: no account the project holds admits it", got)
	}
	if got := other.Holder(creds.KindBitbucket); got != creds.HolderWorkspace {
		t.Errorf("other's bitbucket kind is served by %s, want the workspace's service: the bundle holds no Bitbucket token", got)
	}

	// A second run over the same database finds the records it published and
	// writes nothing: the publish is conditional on the bytes.
	second := newRouteRigOn(t, r.f, r.sm)
	if st := second.check(t); st.State != initflow.Done {
		t.Errorf("a second check = %+v, want Done: the records carry what this installation's bundle and policy say", st)
	}
	if _, err := second.s.Apply(context.Background(), initflow.Env{Yes: true}); err != nil {
		t.Fatalf("the second apply: %v", err)
	}
}

// TestTheRouteIsPerRepository: two repositories of ONE project, both on the
// same provider, where only one of them is admitted by the account the project
// holds. One record per repository is what makes the difference expressible at
// all (F34) — a single "this project is on the workspace" statement would move
// both the moment one of them is uncovered.
func TestTheRouteIsPerRepository(t *testing.T) {
	// The fixture's two repositories are the comparison: one GitHub, one
	// Bitbucket, and the paid key's own admission names the Bitbucket one.
	r := newRouteRig(t, "")
	routeSeedBundle(t, r.sm, config.AnthropicSecret+": "+credsValue(t, "paid"))
	routeSeedPolicy(t, r.f, map[string][]string{config.AnthropicKeyAccount: {"bitbucket/acme/other"}},
		[]string{"bitbucket/acme/other", "github/acme/app"}, nil)

	r.apply(t)

	app := routeOn(t, r.f, "github", "acme/app")
	other := routeOn(t, r.f, "bitbucket", "acme/other")
	if got := app.Holder(creds.KindModel); got != creds.HolderWorkspace {
		t.Errorf("acme/app's model kind is served by %s, want the workspace's service: no account admits it, and one record per repository is what says so", got)
	}
	if got := other.Holder(creds.KindModel); got != creds.HolderProject {
		t.Errorf("acme/other's model kind is served by %s, want the project's own copy: the account admits this repository and not the other", got)
	}
	// The two records are otherwise the same shape: each is a record of its
	// own with its own key, which is the point (the key IS the repository's
	// slug, and the two slugs differ).
	if app.Address != routeAddress || other.Address != routeAddress {
		t.Errorf("the two records carry %q and %q, want the one address the pin names", app.Address, other.Address)
	}
}

// TestTheProjectsCopyWins: the project's own credential serves every kind it
// can, and the workspace's service only what is left (D2973). Both of this
// repository's kinds are its own — the model kind by the paid key's admission,
// the forge kind by the bundle's Bitbucket token and the block's own reach —
// so the record carries no moved holder at all, which is the canonical form C2
// encodes: a kind the record does not name is the project's, so a record
// truncated, mistyped or half-understood cannot send a call anywhere but the
// project.
func TestTheProjectsCopyWins(t *testing.T) {
	r := newRouteRig(t, "")
	// The provider key is the one model credential no caller lists under an
	// account of its own: it is reached under config.ProviderKeyAccount, which
	// is the name the accounts block holds it by — so the GitHub repository
	// below is served by the project's own copy of a credential the bundle
	// carries under a name the credential list does not.
	routeSeedBundle(t, r.sm,
		config.AnthropicSecret+": "+credsValue(t, "paid"),
		config.BitbucketSecret+": "+credsValue(t, "bb"),
		config.ProviderKeySecret+":\n  key: "+credsValue(t, "provider")+"\n  base_url: https://provider.example/api\n  auth: bearer")
	routeSeedPolicy(t, r.f, map[string][]string{
		config.AnthropicKeyAccount: {"bitbucket/acme/other"},
		config.ProviderKeyAccount:  {"github/acme/app"},
	}, []string{"bitbucket/acme/other", "github/acme/app"},
		map[string]any{"bitbucket_token": map[string]any{"allow_repos": []string{"bitbucket/acme/other"}}})

	r.apply(t)

	app := routeOn(t, r.f, "github", "acme/app")
	if got := app.Holder(creds.KindModel); got != creds.HolderProject {
		t.Errorf("app's model kind is served by %s, want the project's own copy: its calls ride the provider key, which the policy admits for this repository", got)
	}

	other := routeOn(t, r.f, "bitbucket", "acme/other")
	for _, kind := range creds.Kinds {
		if got := other.Holder(kind); got != creds.HolderProject {
			t.Errorf("%s is served by %s, want the project's own copy: the project holds a credential of that kind its policy admits for this repository", kind, got)
		}
	}
	raw, err := json.Marshal(r.f.db.Value(creds.RoutePath(routeSlug(t, "bitbucket", "acme/other"))))
	if err != nil {
		t.Fatal(err)
	}
	for _, moved := range []string{string(creds.HolderWorkspace)} {
		if strings.Contains(string(raw), moved) {
			t.Errorf("the record names %q, and nothing of this repository is moved: the encode leaves the project's holder out\n%s", moved, raw)
		}
	}
}

// TestUpdateReportsWhereEachKindIsServed: R4's report (F38) — the member reads,
// per repository and per kind, which credential a run of it is served from, and
// a kind the project serves itself says out loud that a workspace credential of
// that kind is NOT used. The member cannot read the workspace's entries at all,
// so this line is the only place the answer is legible to it.
func TestUpdateReportsWhereEachKindIsServed(t *testing.T) {
	r := newRouteRig(t, "")
	routeSeedBundle(t, r.sm, config.AnthropicSecret+": "+credsValue(t, "paid"))
	routeSeedPolicy(t, r.f, map[string][]string{config.AnthropicKeyAccount: {"github/acme/app"}},
		[]string{"bitbucket/acme/other", "github/acme/app"}, nil)

	out := r.planText(t)
	for _, want := range []string{
		creds.RoutePathParent(),
		routeAddress,
		"model: this project's own copy (a workspace credential of this kind is not used)",
		"github: the workspace's service at " + routeAddress + " (a workspace credential of this kind is used)",
		"bitbucket: the workspace's service at " + routeAddress + " (a workspace credential of this kind is used)",
		countLine(creds.Kinds[0]),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not carry %q:\n%s", want, out)
		}
	}
}

// countLine is the per-kind head of the report, which is the kind's own name
// and nothing else: the test above holds the three sentences apart, and this
// keeps the kind's spelling in it.
func countLine(kind creds.Kind) string { return string(kind) + ": " }

// TestNoWorkspaceNoRoute: a project in no workspace publishes nothing and has
// nothing to remove, and that is a state with nothing to do rather than work
// left owed — a step run for this stage would be a step no machine can
// finish.
func TestNoWorkspaceNoRoute(t *testing.T) {
	sm := gcpfake.NewSecrets(t)
	f := newProjectFixture(t, routeCredsService)
	rewriteConfigLine(t, "no_auth: true", "no_auth: true, secret_manager: "+sm.URL+"/")
	r := newRouteRigOn(t, f, sm)

	st := r.check(t)
	if st.State != initflow.Skipped {
		t.Fatalf("check = %+v, want Skipped: this project is in no workspace", st)
	}
	if st.Undone {
		t.Errorf("the skip is marked undone, and a project in no workspace that published nothing owes no step: %+v", st)
	}
	if !strings.Contains(st.Detail, "is in no workspace") {
		t.Errorf("the skip does not say why: %q", st.Detail)
	}
	oc, _ := r.apply(t)
	if oc.Changed {
		t.Errorf("the apply reported a change (%+v) for a project in no workspace", oc)
	}
	if !strings.Contains(oc.Detail, "in no workspace") {
		t.Errorf("the apply does not name the state: %q", oc.Detail)
	}
	if v := r.f.db.Value(creds.RoutePathParent()); v != nil {
		t.Errorf("a record was published at %s, and this project is in no workspace: %v", creds.RoutePathParent(), v)
	}
}

// TestARepositoryWithNoProviderKeepsItsRecord: a repos entry that records no
// provider is one this project's local config does not say the provider of —
// the checkout's own fugaro.yaml decides, and usedProviders leaves such an
// entry out rather than guessing at it — so this run cannot build the slug that
// keys the repository's record. What that repository published before stays
// published, and so does every other record this run does not publish: the leaf
// of a repository whose provider is unknown is a name this run cannot tell from
// the leaf of one it no longer serves, so a run that removed it would be taking
// away a record its own diff reports as left alone.
func TestARepositoryWithNoProviderKeepsItsRecord(t *testing.T) {
	sm := gcpfake.NewSecrets(t)
	f := newProjectFixture(t, routeMemberPin+routeCredsService)
	rewriteConfigLine(t, "no_auth: true", "no_auth: true, secret_manager: "+sm.URL+"/")
	// The entry named its provider when the record below was published, and
	// records none now — an entry localcfg accepts, and the checkout's own
	// fugaro.yaml is what decides the provider it leaves out.
	rewriteConfigLine(t,
		"acme/other: { provider: bitbucket, workflows: [web] }",
		"acme/other: { workflows: [web] }")

	// The record acme/other published while the entry still named a provider,
	// keyed by the slug that provider gave it.
	old := creds.RoutePath(routeSlug(t, "bitbucket", "acme/other"))
	f.db.Set(old, map[string]any{
		"version": creds.RouteVersion,
		"address": routeAddress,
		"model":   string(creds.HolderWorkspace),
	})

	r := newRouteRigOn(t, f, sm)
	routeSeedBundle(t, r.sm, config.AnthropicSecret+": "+credsValue(t, "paid"))
	routeSeedPolicy(t, r.f, map[string][]string{config.AnthropicKeyAccount: {"github/acme/app"}},
		[]string{"bitbucket/acme/other", "github/acme/app"}, nil)

	if st := r.check(t); st.State != initflow.Todo {
		t.Fatalf("check = %+v, want Todo: acme/app's own record is not published yet", st)
	}
	out := r.planText(t)
	if want := "acme/other: no route is derived, so its published record (if any) is left as it is"; !strings.Contains(out, want) {
		t.Errorf("the plan does not say %q:\n%s", want, out)
	}
	if strings.Contains(out, "removed") {
		t.Errorf("the plan speaks of removals, and this run removes none: a leaf it cannot attribute is one it must not take away:\n%s", out)
	}
	oc, _ := r.apply(t)
	if !strings.Contains(oc.Detail, "published 1 credential-route record") {
		t.Errorf("the report does not name the publish: %q", oc.Detail)
	}
	if v := f.db.Value(old); v == nil {
		t.Errorf("the record at %s was removed, and the diff this run confirmed said it was left as it is", old)
	}
	routeOn(t, r.f, "github", "acme/app")
}

// TestLeavingRemovesTheRoute: the record a member published while it was in a
// workspace is removed once it is in none, so the runs of its repositories take
// every credential from its own service again. That is what makes leaving a
// workspace converge with no step of its own — a repository with no record is
// served by the project's own service (C2's own reading of an absent record).
func TestLeavingRemovesTheRoute(t *testing.T) {
	sm := gcpfake.NewSecrets(t)
	f := newProjectFixture(t, routeCredsService)
	rewriteConfigLine(t, "no_auth: true", "no_auth: true, secret_manager: "+sm.URL+"/")

	// The record a membership published before, for the repository a run of
	// this installation would have read it for. The address is the one this
	// member's pin carried then, and it is not the one the fixture's config
	// names now — the member has left, so there is no address at all.
	gone := creds.RoutePath(routeSlug(t, "github", "acme/app"))
	f.db.Set(gone, map[string]any{
		"version": creds.RouteVersion,
		"address": routeAddress,
		"model":   string(creds.HolderWorkspace),
	})

	r := newRouteRigOn(t, f, sm)
	if st := r.check(t); st.State != initflow.Todo {
		t.Fatalf("check = %+v, want Todo: a record this project published is still published", st)
	}
	if out := r.planText(t); !strings.Contains(out, "removes") {
		t.Errorf("the plan does not say the record is removed:\n%s", out)
	}
	oc, _ := r.apply(t)
	if !oc.Changed {
		t.Errorf("the apply reported no change (%+v), and it removed a record", oc)
	}
	if !strings.Contains(oc.Detail, "removed 1 credential-route record") {
		t.Errorf("the report does not say what was removed: %q", oc.Detail)
	}
	if v := f.db.Value(gone); v != nil {
		t.Errorf("the record at %s is still published after the member left: %v", gone, v)
	}
}

// routingOnlyConfig is the local config of a project whose one member that only
// the node can carry is its routing table: the project's own Anthropic key,
// answering every Claude model. It is the config a project that has never
// published a node writes when the table is the whole of what it has to say.
const routingOnlyConfig = "model_routing:\n" +
	"  - match: claude-*\n" +
	"    chain:\n" +
	"      - { kind: anthropic }\n"

// routeTableConfig is the local config of a project that has written its
// routing table (RT4): one credential of the project, the pool of it, the craft
// the table answers for, and the table itself — a pool source for the craft's
// own model and a single-credential source carrying an admission, which is
// where the admissions live (D3182).
const routeTableConfig = "credentials:\n" +
	"  - { name: acme-main, type: anthropic }\n" +
	"pools:\n" +
	"  - name: acme-pool\n" +
	"    members: [acme-main]\n" +
	"    owner: owner@example.com\n" +
	"    rule: ordered\n" +
	"    principal: { kind: project, id: aurora }\n" +
	"crafts:\n" +
	"  coder: { model: claude-opus-5-5, access: acme-pool }\n" +
	"model_routing:\n" +
	"  - match: claude-opus-*\n" +
	"    chain:\n" +
	"      - { kind: pool, pool: acme-pool }\n" +
	"  - match: claude-*\n" +
	"    chain:\n" +
	"      - { kind: anthropic, admit: [github/acme/app] }\n"

// TestUpdatePublishesModelRouting is RT4's own read of what the update writes:
// one top-level key, model_routing, holding the table the installation's config
// states — with each source's own admit list — and the project's pools in the
// shape PL1 gives them, while craft_pools is left as the reader of the release
// behind decodes it (R21, F49).
//
// The pools are the assertion that matters most: the three members a pool gained
// (rule, principal, threshold) may not be written inside craft_pools, whose
// block is decoded strictly by every job image of the installation, so a release
// behind this one would refuse the WHOLE node for a key it does not know — and
// the node is re-read while runs are in flight
// (docs/design/update-in-flight.md).
func TestUpdatePublishesModelRouting(t *testing.T) {
	f := newProjectFixture(t, routeTableConfig)
	f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
	h := newMigrationRig(t, f, &initOptions{})
	if out, err := h.apply(t, true); err != nil {
		t.Fatalf("the update refused the publish: %v\n%s", err, out)
	}

	p := publishedPolicy(t, f)
	if len(p.UnknownKeys) != 0 {
		t.Errorf("the node the update published lists UnknownKeys = %v, want none", p.UnknownKeys)
	}
	if p.ModelRouting == nil {
		t.Fatal("the node the update published carries no model_routing, so the service reads the project as unmigrated (PL3)")
	}
	if len(p.ModelRouting.Entries) != 2 {
		t.Fatalf("the table the node carries is %+v, want the config's two entries", p.ModelRouting.Entries)
	}
	entry := p.ModelRouting.Entries[0]
	if entry.Match != "claude-opus-*" || len(entry.Chain) != 1 ||
		entry.Chain[0].Kind != routing.KindPool || entry.Chain[0].Pool != "acme-pool" {
		t.Errorf("the first entry = %+v, want the pool the config names", entry)
	}
	if got := p.ModelRouting.Entries[1].Chain[0].Admit; !slices.Equal(got, []string{"github/acme/app"}) {
		t.Errorf("the second entry's admit list = %v, want the repositories the config admits: the admissions travel on the entry's own source (D3182)", got)
	}

	if len(p.ModelRouting.Pools) != 1 {
		t.Fatalf("the pools the node carries are %+v, want the config's one", p.ModelRouting.Pools)
	}
	pool := p.ModelRouting.Pools[0]
	if pool.Name != "acme-pool" || !slices.Equal(pool.Members, []string{"acme-main"}) {
		t.Errorf("the pool in its new shape lost its name or its members: %+v", pool)
	}
	if pool.Rule != config.PoolRuleOrdered {
		t.Errorf("the pool's rule = %q, want the config's %q", pool.Rule, config.PoolRuleOrdered)
	}
	if pool.Principal == nil || pool.Principal.Kind != config.PrincipalProject || pool.Principal.ID != "aurora" {
		t.Errorf("the pool's principal = %+v, want the config's own", pool.Principal)
	}

	// And craft_pools carries the members a release-behind reader knows and no
	// others: the pool is published there as the block has always been written.
	got := publishedPoolMemberNames(t, f)
	if len(got) != 1 || got[0] != "members,name,owner" {
		t.Errorf("the node's craft_pools names %v, want the one pool with the members a release behind decodes (members,name,owner): a new member there is a node that reader refuses whole", got)
	}
}

// publishedPoolMemberNames are the member names of each pool of the node's
// craft_pools block, sorted and joined, read off the published document rather
// than off a decoded value: it is the document a job image of the release
// behind decodes strictly, and what this test is about is exactly which keys it
// finds there.
func publishedPoolMemberNames(t *testing.T, f *projectFixture) []string {
	t.Helper()
	var doc struct {
		CraftPools []map[string]json.RawMessage `json:"craft_pools"`
	}
	if err := json.Unmarshal([]byte(nodeJSON(t, f, projectpolicy.PathShared())), &doc); err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(doc.CraftPools))
	for _, pool := range doc.CraftPools {
		names := make([]string, 0, len(pool))
		for name := range pool {
			names = append(names, name)
		}
		slices.Sort(names)
		out = append(out, strings.Join(names, ","))
	}
	return out
}

// TestUpdateConvergesFromAPolicyWithoutRouting: an installation whose node
// carries no model_routing — every installation before this release, and the
// node a release-1 runner of this one is served by — converges in one update,
// and the run after it writes nothing at all.
//
// The node the second publish builds is compared byte for byte with the first,
// and the two are the same node: the version did not move for this key (R25,
// F63 — a bump would stop the release-1 images a paused run resumes on) and the
// key is additive, so what the update writes is the key and nothing else.
func TestUpdateConvergesFromAPolicyWithoutRouting(t *testing.T) {
	f := newProjectFixture(t, routeTableConfig)
	f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
	// The node this release publishes, written by the explicit door, because
	// the state this test starts from is the one the release BEHIND leaves: a
	// node with no table, a file recording that node's generation, and the
	// update then being the one thing that closes the gap between them.
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	published, err := json.Marshal(f.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}

	// The node the release behind left: this one with the key taken out. It is
	// built from the published document rather than written by hand, so the
	// only difference between the two is what this test is about, and the
	// version does not move with the key (R25, F63), which is what makes the
	// pair the two releases' nodes.
	var without map[string]any
	if err := json.Unmarshal(published, &without); err != nil {
		t.Fatal(err)
	}
	delete(without, "model_routing")
	f.db.Set(projectpolicy.PathShared(), without)
	// The record moves with the node, as it does in nodeWithCloneURLs: the file
	// an installation at the previous release holds records the generation of
	// THAT node, and a record still naming the one the first publish wrote is
	// D854's other state — the node has moved past this file — whose answer is
	// a re-seed of the file rather than the publish this test is about.
	recorded := recordedGeneration(t, f)
	setConfig(t, f, func(s string) string {
		out := strings.Replace(s, "policy_generation: "+recorded, "policy_generation: "+nodeETag(t, f), 1)
		if out == s {
			t.Fatalf("the fixture's config does not record %q:\n%s", recorded, s)
		}
		return out
	})

	h := newMigrationRig(t, f, &initOptions{})
	st, err := h.s.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Todo {
		t.Fatalf("the check says %s %q, want a pending publish: the node carries no table", st.State, st.Detail)
	}
	if out, err := h.apply(t, true); err != nil {
		t.Fatalf("an installation whose node carries no table did not converge: %v\n%s", err, out)
	}
	after, err := json.Marshal(f.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(published) {
		t.Errorf("the node an installation without the key converged to is not the one it publishes with it:\nconverged %s\nfirst     %s", after, published)
	}

	h.s.forget()
	st, err = h.s.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Done {
		t.Errorf("the converged installation is %s %q, want nothing to do", st.State, st.Detail)
	}
	if _, err := h.apply(t, true); err != nil {
		t.Fatal(err)
	}
	settled, err := json.Marshal(f.db.Value(projectpolicy.PathShared()))
	if err != nil {
		t.Fatal(err)
	}
	if string(settled) != string(after) {
		t.Errorf("a second update rewrote the converged node:\nbefore %s\nafter  %s", after, settled)
	}
}

// TestUpdateFirstPublishesARoutingTableOnlyConfig is the first publish's own
// case for the key (RT4, R21, F49): a project that has never published a node
// and whose config states the routing table, and nothing else only the node can
// carry, has to get that table onto the node from `fugaro update` alone. The
// table is read off the node and nowhere else — a run's calls are served from
// it and the credentials service reads the key's presence as the marker that
// the installation is migrated (PL3) — so a stage that reported nothing to do
// here would leave the owner's routing in a file no run reads, with
// `fugaro project policy publish` as the second command the HARD RULE forbids.
func TestUpdateFirstPublishesARoutingTableOnlyConfig(t *testing.T) {
	f := newProjectFixture(t, routingOnlyConfig)
	h := newMigrationRig(t, f, &initOptions{})
	// A project's first node is the owner's to write, so the write is the one
	// the person at their own terminal confirms by typing the project's name
	// (the fixture's name, as every other first-publish test does).
	fakeTerminal(t)
	h.cmd.SetIn(strings.NewReader("aurora\n"))

	st, err := h.s.Check(t.Context())
	if err != nil {
		t.Fatalf("the check refused a config whose one node-only member is the routing table: %v", err)
	}
	if st.State != initflow.Todo {
		t.Fatalf("the check says %s %q, want the project's first publish: the table reaches a run from the node and nowhere else", st.State, st.Detail)
	}
	if !strings.Contains(st.Detail, "the routing table") {
		t.Errorf("the line does not name the routing table among the members only the node carries: %q", st.Detail)
	}
	if out, err := h.apply(t, false); err != nil {
		t.Fatalf("the update published nothing: %v\n%s", err, out)
	}
	p := publishedPolicy(t, f)
	if p.ModelRouting == nil {
		t.Fatal("the node the update published carries no model_routing, so no run is served by the owner's table and the service reads the project as unmigrated (PL3)")
	}
	if len(p.ModelRouting.Entries) != 1 || p.ModelRouting.Entries[0].Match != "claude-*" ||
		len(p.ModelRouting.Entries[0].Chain) != 1 || p.ModelRouting.Entries[0].Chain[0].Kind != routing.KindAnthropic {
		t.Errorf("the table the node carries is %+v, want the config's own entry", p.ModelRouting.Entries)
	}
}

// TestUpdateRefusesANodeCarryingAMemberItDoesNotModel is the read every writer
// of the node makes, asked of this stage (projectpolicy.DecodeStrict): the
// contract lets a later release grow model_routing inside the key without a
// version bump (R21, R25), and the publish builds the key from the typed
// members rather than patching it — so the update refuses such a node and
// leaves it exactly as it was, rather than silently dropping the member it
// cannot write back.
func TestUpdateRefusesANodeCarryingAMemberItDoesNotModel(t *testing.T) {
	f := newProjectFixture(t, routingOnlyConfig)
	node := staleBudgetNode()
	node["model_routing"] = map[string]any{
		"entries": []any{map[string]any{
			"match": "claude-*",
			"chain": []any{map[string]any{"kind": "anthropic"}},
			// The growth this test is about: a member only a later release
			// writes, which this one models nowhere.
			"retired_at": "2027-01-01",
		}},
	}
	f.db.Set(projectpolicy.PathShared(), node)
	before := nodeJSON(t, f, projectpolicy.PathShared())
	h := newMigrationRig(t, f, &initOptions{})

	out, err := h.apply(t, true)
	if err == nil {
		t.Fatalf("the update published over a node carrying a member it does not model:\n%s", out)
	}
	if !strings.Contains(err.Error(), "model_routing") || !strings.Contains(err.Error(), "retired_at") {
		t.Errorf("the refusal names neither the key nor the member: %v", err)
	}
	if after := nodeJSON(t, f, projectpolicy.PathShared()); after != before {
		t.Errorf("the node moved under a refusal:\nbefore %s\nafter  %s", before, after)
	}
}

// TestUpdateMovesBothPoolBlocksTogether: one publish writes a pool twice — the
// entry `craft_pools` carries, which every release behind this one decodes, and
// the same pool in the key's own list (RT4), which is what the credentials
// service resolves a source of kind pool against. An edit to a pool's members,
// its owner or its allowed users is one edit to one project, so both entries
// move with it and neither is left as the copy the node held before.
func TestUpdateMovesBothPoolBlocksTogether(t *testing.T) {
	f := newProjectFixture(t, routeTableConfig)
	f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
	// The node an installation at the previous release holds: the one this
	// release publishes, written by the explicit door, so the only difference
	// this test introduces is the operator's edit to the pool.
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	setConfig(t, f, func(s string) string {
		out := strings.Replace(s,
			"  - { name: acme-main, type: anthropic }\n",
			"  - { name: acme-main, type: anthropic }\n  - { name: acme-second, type: anthropic }\n", 1)
		return strings.Replace(out,
			"    members: [acme-main]\n    owner: owner@example.com\n",
			"    members: [acme-main, acme-second]\n    owner: new@example.com\n    allowed_users: [mate@example.com]\n", 1)
	})

	h := newMigrationRig(t, f, &initOptions{})
	if out, err := h.apply(t, true); err != nil {
		t.Fatalf("the update refused the publish: %v\n%s", err, out)
	}

	p := publishedPolicy(t, f)
	if len(p.ModelRouting.Pools) != 1 || len(p.CraftPools) != 1 {
		t.Fatalf("the node carries %d pool(s) in model_routing and %d in craft_pools, want the config's one in both",
			len(p.ModelRouting.Pools), len(p.CraftPools))
	}
	key, old := p.ModelRouting.Pools[0], p.CraftPools[0]
	if !slices.Equal(key.Members, []string{"acme-main", "acme-second"}) {
		t.Errorf("the key's pool members = %v, want the config's edit: the service resolves a pool source against this list", key.Members)
	}
	if key.Owner != "new@example.com" || !slices.Equal(key.AllowedUsers, []string{"mate@example.com"}) {
		t.Errorf("the key's pool = owner %q, allowed users %v, want the config's edit", key.Owner, key.AllowedUsers)
	}
	// The two blocks describe one pool: a publish that wrote them differently
	// would leave the service serving a member the craft block no longer names.
	if !slices.Equal(key.Members, old.Members) || key.Owner != old.Owner || !slices.Equal(key.AllowedUsers, old.AllowedUsers) {
		t.Errorf("the two blocks disagree about the pool:\nmodel_routing %+v\ncraft_pools   %+v", key, old)
	}
	// And the three members craft_pools may not carry survive the edit, taken
	// from the config where it states them.
	if key.Rule != config.PoolRuleOrdered || key.Principal == nil || key.Principal.ID != "aurora" {
		t.Errorf("the pool in its new shape lost the members craft_pools cannot carry: %+v", key)
	}
}
