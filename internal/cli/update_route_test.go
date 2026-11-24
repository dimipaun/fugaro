package cli

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
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
