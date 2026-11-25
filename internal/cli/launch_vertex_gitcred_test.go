package cli

// This file is the git half of the owner's ruling of 2026-11-24 on the IS9
// work: ONE credentials-service variable, and a `vertex` repository's git
// credentials come from the service like every other secret. The address is
// that one variable on every path (internal/gitprov/providers reads it, opens
// the provider keyless and mints every installation token at the service), so
// a vertex repository whose bundle cannot serve the credential its forge needs
// is refused AT THE LAUNCH, with the repository and the fix named, rather than
// left to stop inside the container at its first git call.
//
// The two tests go through the real provider path and not the static test
// provider: providers.FromEnv over the very environment the launch wrote, and
// github.ServiceTokens against a stand-in service. The refusal test goes
// through `fugaro run` itself, so what it holds is the launch a person gets.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// vertexGitServiceTokenPrefix is the shape of the installation token the
// stand-in service mints. The token is built at run time from this prefix and
// the test's name, never written as a token-shaped literal.
const vertexGitServiceTokenPrefix = "ghs_fixture_vertex_token_"

// vertexGitIDToken and vertexGitRunToken are the identity a mint carries: the
// job's Google ID token and the run's own Firebase one. Neither is a
// credential of any kind, and the stand-in service answers both back.
const (
	vertexGitIDToken  = "job-id-token-1"
	vertexGitRunToken = "run-id-token-1"
)

// vertexGitServiceFake is the credentials service as a vertex run's git path
// meets it: one endpoint (POST /v1/github/installation-token), the two
// identities in the contract's own headers, and an answer carrying the minted
// token. It is an https server because the address the launcher writes has to
// be the shape localcfg admits (https, a host, no path) and the one a mint will
// call: the same value is both.
type vertexGitServiceFake struct {
	t     *testing.T
	srv   *httptest.Server
	token string
	now   time.Time

	mu    sync.Mutex
	calls []vertexGitMintCall
}

type vertexGitMintCall struct {
	path, auth, run, job, body string
}

func newVertexGitServiceFake(t *testing.T) *vertexGitServiceFake {
	t.Helper()
	f := &vertexGitServiceFake{
		t:     t,
		token: vertexGitServiceTokenPrefix + strings.ReplaceAll(t.Name(), "/", "-"),
		now:   time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC),
	}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.calls = append(f.calls, vertexGitMintCall{
			path: r.URL.Path, auth: r.Header.Get("Authorization"),
			run: r.Header.Get("X-Fugaro-Run-Identity"), job: r.Header.Get("X-Fugaro-Run-Job"),
			body: string(body),
		})
		f.mu.Unlock()
		if err := json.NewEncoder(w).Encode(map[string]any{
			"token": f.token, "expires_at": f.now.Add(time.Hour), "repository": "github/acme/app",
		}); err != nil {
			t.Errorf("the stand-in service could not answer: %v", err)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *vertexGitServiceFake) mintCalls() []vertexGitMintCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]vertexGitMintCall(nil), f.calls...)
}

// markRepoEntryFields writes fields into the fixture's own repository entry —
// the acme/app line of the local config — which is where `fugaro update --repo`
// leaves the records a launch reads back: the switch
// (markRepoCredentialsServiceReady) and the git-credential reading this file is
// about. fields is spelled with its own leading comma, or empty for an entry
// no update has written anything onto.
func markRepoEntryFields(t *testing.T, fields string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const repo = "  acme/app: { provider: github, base_branch: main, workflows: [web]"
	if !strings.Contains(string(data), repo) {
		t.Fatalf("the fixture's local config no longer holds %q:\n%s", repo, data)
	}
	next := strings.Replace(string(data), repo, repo+fields, 1)
	if err := os.WriteFile(path, []byte(next), 0o600); err != nil {
		t.Fatal(err)
	}
}

// vertexLaunchFixture is the state a launch of the fixture's repository is
// judged in: an installation whose credentials service is deployed at address
// ("" for one that carries none), the checkout's fugaro.yaml — where the launch
// reads the repository's auth from — and the records the repository's own entry
// carries (fields), which is what `fugaro update --repo` leaves there.
//
// yaml is vertexLaunchYAML for the repository this file is about, and one of
// the served auths for the control rows.
func vertexLaunchFixture(t *testing.T, yaml, address, fields string) *launchBudgetFixture {
	t.Helper()
	f := newLaunchBudget(t, "enforce")
	if address != "" {
		f.appendConfig(t, "credentials_service:\n  url: "+address+"\n")
	}
	projectJobCheckoutYAML(t, yaml)
	markRepoEntryFields(t, fields)
	return f
}

// launchVertexRun launches that fixture the way the CLI's own `fugaro run`
// does, and returns what the backend was handed: the plain environment, the
// secret entries, and the refusal when there is one.
//
// It is launchRun with the recorder in place and the error in the caller's hand
// rather than a t.Fatal — launchWithCredentialsServiceMode's own wiring — and
// it is deliberately NOT the path that carries the launch's gates: those are
// freshLaunchGates, which `fugaro run` passes and this function does not reach
// (runVertex below is the one that does). What this one is for is the
// environment a launch writes, which is what the provider path is driven from.
func launchVertexRun(t *testing.T, f *launchBudgetFixture) (plain, secret []string, err error) {
	t.Helper()
	env := memEnv(t, f.cloudFixture)
	rec := &envRecorder{Backend: env.be}
	env.be = rec
	if _, err := launchRun(context.Background(), env, appSlug, raceSpec(t, env), time.Now()); err != nil {
		return nil, nil, err
	}
	return withoutServicesStamp(t, rec.env), rec.secretEnv, nil
}

// runVertex launches the same fixture through `fugaro run` itself, which is the
// one path a person has: it is what passes freshLaunchGates, and so what the
// refusal this file is about has to come out of.
func runVertex(t *testing.T, f *launchBudgetFixture) error {
	t.Helper()
	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", budgetRun, "Add a feature")
	return err
}

// envEntry is one KEY=VALUE pair of a launch's environment, and whether it is
// there at all.
func envEntry(env []string, key string) (string, bool) {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v, true
		}
	}
	return "", false
}

// TestAVertexRunMintsItsGitTokenAtTheService is the acceptance of the owner's
// ruling: a `vertex` repository's run is handed the credentials service's
// address, and its installation token comes from the service — not from an App
// key its job would first have to be reading, and not from a mount the ruling
// says no longer holds one.
//
// It is on the REAL provider path: the environment is the one the launch wrote
// (credentialsServiceLaunchEnv, read out of the execution's own plain entries),
// the opener is providers.FromEnv — the function a job whose address is set
// opens its provider with, which is where the keyless arm lives — and the mint
// is github.ServiceTokens, the client the runner installs on the provider. So
// what the test holds is the whole chain the ruling names, with the launcher's
// own answer at its head: an address, one variable, and a token minted at it.
//
// The entry carries the record `fugaro update --repo` writes from its reading
// of the bundle (localcfg.Repo.GitCredentialsServed, written by
// recordCredentialsServiceReady): the launch is refused without it, which the
// test beside this one holds.
func TestAVertexRunMintsItsGitTokenAtTheService(t *testing.T) {
	svc := newVertexGitServiceFake(t)
	f := vertexLaunchFixture(t, vertexLaunchYAML, svc.srv.URL, ", git_credentials_served: true")
	// The record, and not the gate: this launch's environment is what the
	// provider path below is driven from, and `fugaro run` admits the same
	// state in the sibling test's own row.
	plain, secret, err := launchVertexRun(t, f)
	if err != nil {
		t.Fatalf("a vertex repository whose bundle serves its git credential was refused: %v", err)
	}
	address, ok := envEntry(plain, envCredentialsServiceURL)
	if !ok || address != svc.srv.URL {
		t.Fatalf("the launch's plain Env = %v, want the service's address %s: a vertex repository of an installation that has the service is told where it is (D3326, F17)", plain, svc.srv.URL)
	}
	for _, kv := range secret {
		if strings.HasPrefix(kv, envCredentialsServiceURL+"=") {
			t.Errorf("the launch carried %q in its secret entries: %s is plain environment on every path", kv, envCredentialsServiceURL)
		}
	}
	// The App's ID is the job's own deployed environment value and not
	// something a launch writes (C2.1); the address is the launcher's.
	env := append([]string{providers.EnvGitHubAppID + "=1234"}, plain...)
	open := providers.FromEnv(env, svc.srv.Client(), nil)
	p, statics, err := open(context.Background(), gitprov.KindGitHub, "acme/app")
	if err != nil {
		t.Fatalf("a job handed the address could not open its github provider: %v", err)
	}
	if len(statics) != 0 {
		t.Fatalf("opening the provider returned %d static secret(s): with the address set the App's key is the service's and the job holds none (C2.2, D145)", len(statics))
	}
	setter, ok := p.(gitprov.ServiceTokenSetter)
	if !ok {
		t.Fatal("the github provider a service-held key opens is not one that takes the service's tokens: the run would have no way to install the mint (gitprov.ServiceTokenSetter, C2.2)")
	}
	setter.UseServiceTokens(&github.ServiceTokens{
		URL: address, AppID: "1234", HTTP: svc.srv.Client(),
		IDToken: func(context.Context) (string, error) { return vertexGitIDToken, nil },
		RunIDToken: func() string {
			return vertexGitRunToken
		},
		Job: "fugaro-run-job", Now: func() time.Time { return svc.now },
	})
	auth, err := p.GitAuth(context.Background(), 5*time.Minute)
	if err != nil {
		t.Fatalf("the vertex run's git credentials could not be fetched from the service: %v", err)
	}
	if auth.Token != svc.token {
		t.Errorf("the run's git token is not the one the service minted")
	}
	calls := svc.mintCalls()
	if len(calls) != 1 {
		t.Fatalf("the service answered %d mint calls, want exactly 1", len(calls))
	}
	if call := calls[0]; call.path != "/v1/github/installation-token" {
		t.Errorf("the mint was asked at %s", call.path)
	} else if call.auth != "Bearer "+vertexGitIDToken || call.run != vertexGitRunToken {
		t.Errorf("the mint did not carry the run's two identities: a mint the service verifies both of")
	} else if call.body != `{"app_id":"1234"}` {
		t.Errorf("the mint's body is %s, want the App ID alone (the key and the repository are the service's)", call.body)
	}
}

// TestLaunchRefusesAVertexRunWhoseBundleCannotServeGitCredentials is the other
// half of the ruling: where the service cannot serve a vertex repository's git
// credential, the launch refuses AT ONCE, with one line naming the repository
// and the fix — never a run that reaches the container and stops at its first
// git call with a provider it can neither key nor mint with.
//
// What the launch reads is localcfg.Repo.GitCredentialsServed, written by the
// repository's own `fugaro update --repo` from its one reading of the bundle.
// A nil record (no machine has judged) and a recorded false read the same way
// here, which is the fail-closed direction the field is documented with: the
// refusal prints the command that writes the record, so a person meets it once.
//
// The third row is the boundary of the gate and not of the answer: a served
// repository (an `oauth` one, whose record vouches and whose switch is on) is
// handed the very same address and is NOT refused here — its bundle reading is
// the switch's own (credentialsBundleServes folds the same App-key fact in),
// and the git credential of a repository the service serves is what that
// record is about.
func TestLaunchRefusesAVertexRunWhoseBundleCannotServeGitCredentials(t *testing.T) {
	for name, tc := range map[string]struct {
		yaml    string
		fields  string
		refused bool
	}{
		"a vertex repository no update has judged":         {vertexLaunchYAML, "", true},
		"a vertex repository whose record says no":         {vertexLaunchYAML, ", git_credentials_served: false", true},
		"a vertex repository whose bundle serves the key":  {vertexLaunchYAML, ", git_credentials_served: true", false},
		"a served repository the bundle serves git for":    {oauthLaunchYAML, ", credentials_service_ready: true", false},
		"a vertex repository of an installation with none": {vertexLaunchYAML, "", false},
	} {
		t.Run(name, func(t *testing.T) {
			// An installation with no service hands no address, and the gate
			// judges nothing there — the last row, whose address is "".
			address := launchCredsURL
			if name == "a vertex repository of an installation with none" {
				address = ""
			}
			err := runVertex(t, vertexLaunchFixture(t, tc.yaml, address, tc.fields))
			switch {
			case tc.refused && err == nil:
				t.Fatal("a vertex repository whose service cannot serve its git credential was launched: the job's address opens its git provider keyless, so the run would stop at its first git call inside the container")
			case tc.refused:
				for _, want := range []string{"acme/app", "GitHub App key", "fugaro creds set", "fugaro update --repo"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the refusal does not name %q: %v", want, err)
					}
				}
			case err != nil:
				t.Fatalf("a launch this gate does not judge was refused: %v", err)
			}
		})
	}
}

// TestLaunchRefusesAProxiedVertexBitbucketRunWithoutAToken is the Bitbucket
// half of the ruling, checked the way the owner asked: the same one variable,
// and a repository whose access the published node brokers reaches the service
// for its git credential instead of holding a token of its own.
//
// A proxied run's job mounts no Bitbucket token at all (G1's flip), and
// providers.Proxied refuses to open without a service to forward to (G9), so a
// bundle holding no token is a bootstrap stop there too — refused here with the
// same shape. What makes a repository proxied is the node's own answer AND the
// address (proxiedRepo), so the rows below take each half away in turn: a node
// that does not broker it leaves the repository its mounted token, and a bundle
// that holds one is served.
// gitCredentialRecord runs the repository door's own call — `fugaro update
// --repo`'s, recordCredentialsServiceReady — over the rig with doc as the
// checkout's fugaro.yaml and provider as the git provider the door is told, and
// answers the entry it left on the local config: what the launch reads back.
//
// It is providerServedRun's own shape (init_provider_served_test.go), with the
// document and the provider as parameters instead of that file's constants.
func gitCredentialRecord(t *testing.T, r *initRig, doc, provider string) localcfg.Repo {
	t.Helper()
	e := rigEngine(t, r, &initOptions{yes: true})
	cfg, problems := config.Parse([]byte(doc))
	if cfg == nil {
		t.Fatalf("the fixture's fugaro.yaml does not parse: %v", problems)
	}
	const repo = "acme/sandbox"
	e.r.recordCredentialsServiceReady(t.Context(), e.lc, switchOutputs(), cfg, providerServedPolicy(),
		repo, provider, cfg.Agent.Auth, infra.WorkflowAccounts(cfg, e.lc, repo), config.BudgetObserve)
	entry, ok := e.lc.Repos[repo]
	if !ok {
		t.Fatalf("the door left no entry for %s: %v", repo, e.lc.Repos)
	}
	return entry
}

// gitCredentialBundleDoc is a bundle document holding the model key the
// switch's readiness is read from, and then each of fragments — whole
// `name: value` lines, the shape a bundle document is written in — so the arm
// under test is the git one alone.
func gitCredentialBundleDoc(t *testing.T, fragments ...string) string {
	t.Helper()
	doc := config.AnthropicSecret + ": " + credsValue(t, "model") + "\n"
	for _, f := range fragments {
		doc += f
	}
	return doc
}

// gitCredentialAppKey is the App's private key as a bundle entry: a PEM this
// test generates (appKeyPEM), written under the name the resolver answers the
// kind by — the shape buildRouteBundleDoc uses.
func gitCredentialAppKey(t *testing.T) string {
	t.Helper()
	key := "  " + strings.ReplaceAll(strings.TrimRight(appKeyPEM(t), "\n"), "\n", "\n  ")
	return config.GitHubAppKeySecret + ": |\n" + key + "\n"
}

// gitCredentialBitbucketToken is the upstream Bitbucket token as a bundle
// entry, under the name the resolver answers that kind by.
func gitCredentialBitbucketToken(t *testing.T) string {
	t.Helper()
	return config.BitbucketSecret + ": " + credsValue(t, "git") + "\n"
}

// TestTheGitCredentialFactIsRecordedFromTheBundle is the writer half of the
// chain the two tests above read: `fugaro update --repo` records, on the
// repository's entry, whether the service's bundle can serve the git credential
// its provider needs — the value a launch reads back as
// localcfg.Repo.GitCredentialsServed (the field the gate refuses on).
//
// The reading is the door's own (readCredentialsBundle, setting credsAppKey and
// the two Bitbucket answers), and the fact is deliberately NOT multiplied by the
// switch: the `vertex` row below is the one state that proves it — a `vertex`
// repository has no switch at all (no release hands those a model credential),
// and the fact is the only thing a launch of it can judge its address by.
func TestTheGitCredentialFactIsRecordedFromTheBundle(t *testing.T) {
	appKey := gitCredentialBundleDoc(t, gitCredentialAppKey(t))
	plain := gitCredentialBundleDoc(t)
	for name, tc := range map[string]struct {
		doc, provider, bundle string
		served, switchOn      bool
	}{
		"a GitHub bundle holding the App key": {
			checkoutYAML(gitprov.KindGitHub, config.AuthAPIKey, "aurora", ""), gitprov.KindGitHub, appKey, true, true},
		"a GitHub bundle holding none": {
			checkoutYAML(gitprov.KindGitHub, config.AuthAPIKey, "aurora", ""), gitprov.KindGitHub, plain, false, false},
		"a vertex bundle holding the App key": {
			checkoutYAML(gitprov.KindGitHub, authVertex, "aurora", ""), gitprov.KindGitHub, appKey, true, false},
		"a Bitbucket bundle holding the token": {
			checkoutYAML(gitprov.KindBitbucket, config.AuthAPIKey, "aurora", ""), gitprov.KindBitbucket, gitCredentialBundleDoc(t, gitCredentialBitbucketToken(t)), true, true},
		"a Bitbucket bundle holding none": {
			checkoutYAML(gitprov.KindBitbucket, config.AuthAPIKey, "aurora", ""), gitprov.KindBitbucket, plain, false, true},
	} {
		t.Run(name, func(t *testing.T) {
			entry := gitCredentialRecord(t, providerServedRig(t, "", "", tc.bundle).initRig, tc.doc, tc.provider)
			if got := entry.CredentialsServiceOn(); got != tc.switchOn {
				t.Fatalf("the fixture's switch = %v, want %v (%+v): the rows are read against the state they claim", got, tc.switchOn, entry)
			}
			if got := entry.GitCredentialsServedOn(); got != tc.served {
				t.Errorf("GitCredentialsServed = %v, want %v: the fact is the door's own reading of the bundle, the value a launch reads back and refuses on", got, tc.served)
			}
		})
	}
	// The Bitbucket row whose switch is on and whose fact is off, and the
	// `vertex` row whose switch is off and whose fact is on, are the two halves
	// of "not multiplied by the switch": the fact answers for the git credential
	// the service holds, and the switch for the model credential a run would ask
	// it for, and neither stands for the other.
	//
	// The fallback arm, which no real config reaches (gitprov.Kinds holds these
	// two) and which the launch's own gate would read all the same if it did: a
	// provider whose credential this service does not serve is read as not
	// served, so a launch refuses rather than handing its job an address for a
	// credential the service cannot answer with.
	if credentialsGitCredential(&initRun{credsAppKey: true, credsBitbucket: true}, "gitlab") {
		t.Errorf("a provider kind the service does not serve was read as served: the fact is the credential this service holds, and a launch reading it wrong hands a job an address for a credential nothing there can answer")
	}
}

func TestLaunchRefusesAProxiedVertexBitbucketRunWithoutAToken(t *testing.T) {
	// releasePolicy and not proxySince: these rows publish a policy node and
	// then launch, and the launcher holds a job image to the release that
	// introduced the node version it reads (policyImageGate) — an image older
	// than that is refused there first, with its own sentence, and would hide
	// the gate this test is about.
	current := releasePolicy
	for name, tc := range map[string]struct {
		policy  string
		fields  string
		refused bool
	}{
		"a brokered vertex repository whose bundle holds no token": {proxiedPolicy, ", vertex: true", true},
		"a brokered vertex repository whose bundle holds one":      {proxiedPolicy, ", vertex: true, git_credentials_served: true", false},
		"a vertex repository the node does not broker":             {notProxiedPolicy, ", vertex: true", false},
	} {
		t.Run(name, func(t *testing.T) {
			f := newLaunchBudget(t, "enforce")
			addProxiedRepoEntry(t, f, current, proxiedBlock, true, tc.fields)
			publishPolicy(t, f, tc.policy)
			_, _, err := execute(t, "run", "--repo", proxiedRepo, "--run-id", budgetRun, "Add a feature")
			switch {
			case tc.refused && err == nil:
				t.Fatal("a brokered vertex repository whose service holds no Bitbucket token was launched: its job mounts none and its provider is opened with nothing to forward (G1, G9)")
			case tc.refused:
				for _, want := range []string{proxiedRepo, "bitbucket-token", "fugaro creds set", "fugaro update --repo"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the refusal does not name %q: %v", want, err)
					}
				}
			case err != nil:
				t.Fatalf("a launch this gate does not judge was refused: %v", err)
			}
		})
	}
}
