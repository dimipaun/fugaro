package cli

// The daemon's own identity at the installation's credentials service (F19, O2;
// orchestrator_identity.go): the account it mints as, the audience it mints
// for, the refusals a machine that cannot mint one gets, and the two callers
// that present the result — the label route (its own file's tests) and the
// merge mint a provider's ready and merge calls are made with (E10b).
//
// No test here runs a real gcloud or mints a real credential: the mint's two
// process seams are the person id's (personGcloudStub), because the two mints
// run the same binary through the same two functions.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// The installation these tests are about: a project, and the account the
// module creates in it, which is the one address the service's orchestrator
// class holds.
const (
	identityURL     = "https://fugaro-credentials-abc-uc.a.run.app"
	identityProject = "proj-1234"
	identityAccount = infra.OrchestratorAccountID + "@" + identityProject + ".iam.gserviceaccount.com"
)

// TestOrchestratorIDTokenIsTheDaemonsOwnImpersonatedMint is the argv rule the
// whole path rests on: the token has to be the DAEMON's and not the operator's,
// and it has to be minted for the service's own URL — the audience the class
// checks (credsvc's VerifyOrchestrator) — which is what --impersonate-service-account
// and --audiences give it, with --include-email for the email claim an
// impersonated mint leaves out by default and the service looks the caller up
// by.
func TestOrchestratorIDTokenIsTheDaemonsOwnImpersonatedMint(t *testing.T) {
	s := (&personGcloudStub{tokenOut: "the-daemons-token\n"}).use(t)
	id := orchestratorIdentity{account: identityAccount, url: identityURL}

	tok, err := orchestratorIDToken(context.Background(), id)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if tok != "the-daemons-token" {
		t.Errorf("the mint answered %q, want the trimmed token", tok)
	}
	want := []string{"auth", "print-identity-token",
		"--impersonate-service-account=" + identityAccount,
		"--audiences=" + identityURL,
		"--include-email",
		"--quiet",
	}
	if len(s.args) != 1 || strings.Join(s.args[0], " ") != strings.Join(want, " ") {
		t.Fatalf("the mint ran %q, want exactly one call: %q", s.args, want)
	}
}

// TestOrchestratorIDTokenRefusesWithoutAUsableCredential: every state in which
// the daemon cannot present its own identity is a refusal naming what to run,
// and none of them carries a value: the token is a credential and gcloud's own
// words are another writer's.
//
// The impersonation refusal is the one that matters most here: this
// installation grants nobody the right to mint a token for its daemon account
// on purpose, so the refusal has to say which grant makes it work rather than
// leaving the operator with a daemon that labels nothing.
//
// Mutation (run, restore): return the raw output where the checks refuse — a
// token that is not one header value reaches the service's Authorization
// header, which is the request a header-splitting value would corrupt.
func TestOrchestratorIDTokenRefusesWithoutAUsableCredential(t *testing.T) {
	id := orchestratorIdentity{account: identityAccount, url: identityURL}
	for _, tc := range []struct {
		name string
		stub *personGcloudStub
		want []string
	}{
		{"no gcloud on PATH", &personGcloudStub{lookPathErr: errors.New("not found")}, []string{"is not on PATH", "auth login"}},
		{"the mint fails", &personGcloudStub{tokenErr: errors.New("gcloud said no")},
			[]string{"could not be minted", identityAccount, "roles/iam.serviceAccountTokenCreator", "auth login"}},
		{"an empty answer", &personGcloudStub{tokenOut: "  \n"}, []string{"no usable identity token"}},
		{"an answer with a newline inside", &personGcloudStub{tokenOut: "one\ntwo\n"}, []string{"no usable identity token"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.stub.use(t)
			tok, err := orchestratorIDToken(context.Background(), id)
			if ExitCode(err) != ExitUserError {
				t.Fatalf("mint = %q, %v, want a user error", tok, err)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q: %v", want, err)
				}
			}
			if tok != "" {
				t.Errorf("the refused mint answered %q, want no token at all", tok)
			}
			if strings.Contains(err.Error(), "gcloud said no") {
				t.Errorf("the refusal carries the child process's own words: %v", err)
			}
		})
	}
}

// TestOrchestratorIdentityOfIsTheInstallationsOwn records the two halves the
// daemon presents and where each comes from: the account from the project the
// installation's config names — the same derivation `fugaro doctor`'s
// orchestrator-identity check makes, because the module's account ID is one
// literal — and the URL from the block `fugaro update` publishes. Either
// missing is an incomplete identity (`none`), and never a value guessed at.
func TestOrchestratorIdentityOfIsTheInstallationsOwn(t *testing.T) {
	svc := &localcfg.CredentialsService{URL: identityURL}
	for _, tc := range []struct {
		name string
		lc   *localcfg.Config
		want orchestratorIdentity
	}{
		{"no config at all", nil, orchestratorIdentity{}},
		{"a project and a service", &localcfg.Config{GCPProject: identityProject, CredentialsService: svc},
			orchestratorIdentity{account: identityAccount, url: identityURL}},
		{"no project", &localcfg.Config{CredentialsService: svc}, orchestratorIdentity{url: identityURL}},
		{"no service", &localcfg.Config{GCPProject: identityProject}, orchestratorIdentity{account: identityAccount}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := orchestratorIdentityOf(tc.lc)
			if got != tc.want {
				t.Errorf("orchestratorIdentityOf = %+v, want %+v", got, tc.want)
			}
			if want := tc.want.account == "" || tc.want.url == ""; got.none() != want {
				t.Errorf("none() = %v for %+v, want %v", got.none(), got, want)
			}
		})
	}
}

// TestOrchestratorIdentityTokenIsNilWithoutAService: an installation that
// cannot say which account the daemon is, or which service to present it to,
// gets no mint at all rather than one whose every call would be refused — the
// state every installation without a credentials service is in, and the reason
// a daemon there builds no label seams and installs no merge mint.
func TestOrchestratorIdentityTokenIsNilWithoutAService(t *testing.T) {
	(&personGcloudStub{tokenOut: "a-token\n"}).use(t)
	for _, id := range []orchestratorIdentity{
		{},
		{account: identityAccount},
		{url: identityURL},
	} {
		if tok := orchestratorIdentityToken(id); tok != nil {
			t.Errorf("orchestratorIdentityToken(%+v) is not nil: an incomplete identity would have a caller present a token no revision answers to", id)
		}
	}
	tok := orchestratorIdentityToken(orchestratorIdentity{account: identityAccount, url: identityURL})
	if tok == nil {
		t.Fatal("orchestratorIdentityToken is nil for a whole identity")
	}
	got, err := tok(context.Background())
	if err != nil || got != "a-token" {
		t.Errorf("the seam minted %q, %v, want the mint's own token", got, err)
	}
}

// TestTheDaemonMintsForItsOwnAccountAndAudience: the label route's whole path
// runs on the identity DERIVED from the installation's own records — the
// account of the config's project and the URL of the service it names — so a
// daemon that minted as somebody else, or for another service's audience, is a
// token the service refuses whoever presents it.
func TestTheDaemonMintsForItsOwnAccountAndAudience(t *testing.T) {
	f := newLabelFixture(t, labelPR(7, ""))
	f.openRun(t, labelRunA, 7)
	running := labelItem("wi-1", orchestrator.PhaseRunning, labelRunA, "forge-labels", 1)
	f.seed(t, orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		Rev:           1,
		AgentID:       "aurora",
		Role:          orchestrator.RoleOrchestrator,
		WorkItems:     []orchestrator.WorkItem{running},
		Intents:       []orchestrator.Intent{labelAttempt(running, 1, labelRunA, "")},
	})
	d, _ := f.daemon(t)
	f.open(t, d)
	f.step(t, d)

	if len(f.mint.asked) == 0 {
		t.Fatal("the daemon wrote labels without minting an identity")
	}
	want := orchestratorIdentity{
		account: infra.OrchestratorAccountID + "@" + d.lc.GCPProject + ".iam.gserviceaccount.com",
		url:     f.service.srv.URL,
	}
	for i, got := range f.mint.asked {
		if got != want {
			t.Fatalf("mint %d was asked for %+v, want %+v", i, got, want)
		}
	}
}

// TestLabelRouteRefusesWhenTheMintRefuses: a daemon with a service configured
// and no identity to present is a refusal the daemon REPORTS, never a cycle
// that quietly writes no label. The host is not called at all — a call this
// daemon cannot authenticate is one no host should see — and the refusal
// reaches the daemon's own warning stream.
func TestLabelRouteRefusesWhenTheMintRefuses(t *testing.T) {
	f := newLabelFixture(t, labelPR(7, ""))
	f.openRun(t, labelRunA, 7)
	running := labelItem("wi-1", orchestrator.PhaseRunning, labelRunA, "forge-labels", 1)
	f.seed(t, orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		Rev:           1,
		AgentID:       "aurora",
		Role:          orchestrator.RoleOrchestrator,
		WorkItems:     []orchestrator.WorkItem{running},
		Intents:       []orchestrator.Intent{labelAttempt(running, 1, labelRunA, "")},
	})
	d, out := f.daemon(t)
	f.open(t, d)
	f.mint.err = userErr("the daemon's own Google ID token could not be minted")

	f.step(t, d)

	if calls := labelCalls(f.host); len(calls) != 0 {
		t.Errorf("the host was called %q: a call the daemon cannot authenticate is one the service would refuse", calls)
	}
	if len(f.service.identities()) != 0 {
		t.Errorf("the service was presented %q for a mint that refused", f.service.identities())
	}
	if line := out.String(); !strings.Contains(line, "could not be minted") {
		t.Errorf("the daemon said nothing about the mint that refused:\n%s", line)
	}
}

// mintRecordingProvider is a provider that takes a merge mint. The fake host's
// own provider deliberately does not (internal/gitprov/fake implements
// gitprov.MintExempt: a double holds no credential for a host to bind a merge
// token to), so a double that answers this question is the only way to ask it.
// Its embedded interface is never called — nothing here makes a provider call.
type mintRecordingProvider struct {
	gitprov.Provider
	mint gitprov.MergeMinter
}

var _ gitprov.MergeMintSetter = (*mintRecordingProvider)(nil)

func (p *mintRecordingProvider) UseMergeMint(m gitprov.MergeMinter) { p.mint = m }

// TestTheMergeMintIsTheServicesAndPresentsTheDaemonIdentity: the credential a
// provider's ready and merge calls take is the service's mint, presented with
// the daemon's own identity and signed as the App this installation records for
// the repository — the same seam the label route uses, and the reason the two
// are wired from one identity.
func TestTheMergeMintIsTheServicesAndPresentsTheDaemonIdentity(t *testing.T) {
	var (
		path     string
		bearer   string
		body     creds.MergeMintRequest
		answered bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, bearer = r.URL.Path, r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		answered = true
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "the-merge-token",
			"expires_at": time.Now().Add(time.Hour).UTC(),
			"repository": body.Repository,
		})
	}))
	t.Cleanup(srv.Close)

	withLabelIdentity(t)
	f := newLabelFixture(t)
	d, _ := f.daemon(t)
	d.lc.CredentialsService = &localcfg.CredentialsService{URL: srv.URL}
	p := &mintRecordingProvider{}
	orchestratorUseMergeMint(p, d.env, labelRepo, orchestratorIdentityOf(d.lc))
	if p.mint == nil {
		t.Fatal("the provider was given no merge mint: a merge would refuse with ErrNoMergeMint on an installation that can mint one")
	}
	const head = "0123456789abcdef0123456789abcdef01234567"
	tok, expires, err := p.mint.MintMerge(context.Background(), labelRepo, 7, head)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if !answered {
		t.Fatal("the mint never reached the service")
	}
	if path != creds.MergeMintPath {
		t.Errorf("the mint called %s, want the route the service serves (%s)", path, creds.MergeMintPath)
	}
	if bearer != "Bearer "+labelIdentity {
		t.Errorf("the service was presented %q, want the daemon's own identity", bearer)
	}
	if body.Repository != labelRepo || body.Number != 7 || body.HeadSHA != head || body.AppID != labelAppID {
		t.Errorf("the mint's request is %+v, want %s #7 at %s signed as App %s", body, labelRepo, head, labelAppID)
	}
	if tok != "the-merge-token" || !expires.After(time.Now()) {
		t.Errorf("the mint answered %q expiring %s, want the service's own token", tok, expires)
	}
}

// TestAProviderWithNoAppIDTakesNoMergeMint: the installation records the App
// each repository's token is signed as, and a repository it records none for
// gets no mint rather than one that would sign as an App nobody named. The
// merge there refuses — the provider was given no credential — which is the
// truthful answer rather than a call the service refuses one round trip later.
func TestAProviderWithNoAppIDTakesNoMergeMint(t *testing.T) {
	withLabelIdentity(t)
	f := newLabelFixture(t)
	d, _ := f.daemon(t)
	repo := d.lc.Repos[labelRepo]
	repo.GitHubAppID = ""
	d.lc.Repos[labelRepo] = repo

	p := &mintRecordingProvider{}
	orchestratorUseMergeMint(p, d.env, labelRepo, orchestratorIdentityOf(d.lc))
	if p.mint != nil {
		t.Error("a repository this installation records no App ID for was given a merge mint")
	}
}

// TestAnExemptProviderIsLeftAlone: a host with no merge mint to gate keeps the
// credential it already holds (gitprov's MintExempt — Bitbucket, and the fake's
// own provider), so installing one would be handing a host a mint its merge
// path does not take.
func TestAnExemptProviderIsLeftAlone(t *testing.T) {
	withLabelIdentity(t)
	f := newLabelFixture(t)
	d, _ := f.daemon(t)
	exempt := &fake.Provider{Repo: labelRepo}
	if _, ok := gitprov.Provider(exempt).(gitprov.MergeMintSetter); ok {
		t.Fatal("the fake provider takes a merge mint, so this test's premise is wrong")
	}
	orchestratorUseMergeMint(exempt, d.env, labelRepo, orchestratorIdentityOf(d.lc))
	if err := exempt.MergeMintExemption(); !errors.Is(err, gitprov.ErrMintNotApplicable) {
		t.Errorf("the exempt provider answers %v, want ErrMintNotApplicable", err)
	}
}

// TestTheTriageInstallsTheMergeMintOnTheProviderItOpens (E10b): the provider
// the daemon's triage opens a repository with is the one a merge is made
// through, so the mint has to be on it before it is asked anything — not on a
// second provider opened somewhere else, and not left for a later cycle.
func TestTheTriageInstallsTheMergeMintOnTheProviderItOpens(t *testing.T) {
	withLabelIdentity(t)
	f := newLabelFixture(t)
	d, _ := f.daemon(t)
	rec := &mintRecordingProvider{}
	tr := &orchestratorTriage{
		env: d.env, id: d.id, identity: orchestratorIdentityOf(d.lc),
		open:   func(context.Context, string, string) (gitprov.Provider, []string, error) { return rec, nil, nil },
		opened: map[string]gitprov.Provider{}, warned: map[string]bool{},
	}
	p, err := tr.provider(context.Background(), labelRepo)
	if err != nil {
		t.Fatalf("opening the provider: %v", err)
	}
	if p != gitprov.Provider(rec) {
		t.Fatalf("the triage answered %T, want the provider it opened", p)
	}
	if rec.mint == nil {
		t.Error("the provider the triage opened took no merge mint")
	}
}
