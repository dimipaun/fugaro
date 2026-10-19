package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/lock"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// admissionRun is newBK re-homed on the run a launcher creates for slugRepo
// under kind: the storage slug is task.Slug(kind, slugRepo) — the run's
// FUGARO_RUN — while task.json keeps naming the fixture's acme/app. slugRepo
// "acme/app" is the fixture's own repository (the slug is then repoSlug); any
// other value hands the run a slug that is not its own repository's, which is
// the case the binding refuses. The run is given a Cloud Run execution name
// (the binding's scope, R2-9), and the budget token, the repository caps and
// the rules marker are seeded under the slug it now carries. policy ""
// publishes no node under the marker.
//
// The fixture names the deriving kind as its --provider (FUGARO_GIT_PROVIDER),
// so the run really opens the provider unless the admission check refuses it
// first, and counts the git credential requests it receives (auths). That
// counter is the only thing that can tell a refusal from a run that went on to
// clone and halt later: both end in an error, and only one of them handed a git
// credential out first.
func admissionRun(t *testing.T, kind, slugRepo, mode, capUSD, policy string, script ...anthropicfake.Reply) *bk {
	t.Helper()
	return admissionRunCfg(t, kind, slugRepo, mode, capUSD, gwConfig(t, ""), policy, script...)
}

// admissionRunCfg is admissionRun with the repository's own fugaro.yaml given
// by the caller, for a test that needs a config the fixture does not have (a
// workflow's job_profile selection is the one so far).
func admissionRunCfg(t *testing.T, kind, slugRepo, mode, capUSD, cfg, policy string, script ...anthropicfake.Reply) *bk {
	t.Helper()
	b := newBK(t, cfg, mode, capUSD, script...)
	slug, err := task.Slug(kind, slugRepo)
	if err != nil {
		t.Fatal(err)
	}
	b.deps.ProviderKind = kind
	countAuths(b)
	// A mismatch fixture must not land on the fixture's own slug: the run would
	// be homed on its own repository and the binding would have nothing to
	// refuse.
	if slug == repoSlug && slugRepo != "acme/app" {
		t.Fatalf("task.Slug(%q, %q) is the fixture's own slug; the test would check nothing", kind, slugRepo)
	}
	// A real per-repository job name, computed from the slug the run carries
	// and the fixture's workflow — the same recomputation the run itself makes
	// (gcp.JobName of the slug and gcp.WorkflowVariant of task.json's
	// workflow, plan task 3.3 step 1). A hand-written job name is a name the
	// run cannot place, and would halt these runs before the check they are
	// about (TestJobIdentityRefusesANameItCannotPlace).
	const workflow = "app"
	b.deps.Execution = cloudRunExecution(gcp.JobName(slug, gcp.WorkflowVariant(workflow, "")))
	store := runstore.Open(b.harness.bucket, slug, runID)
	spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: workflow, Task: "Add a feature"}
	if err := store.WriteTask(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	b.deps.Store, b.harness.store = store, store
	if slug != repoSlug {
		// newBK already left the run's token under the fixture's own slug, so a
		// run homed on its own repository reuses it. Another slug is another
		// token: its slug claim is the identity the run compares with its own.
		mintUnder(t, b, slug, time.Now())
	}
	micros := func(v budget.Micros) *budget.Micros { return &v }
	b.db.Set(budget.PathCapsRepo(slug), budget.RepoCaps{DailyMicros: micros(60_000_000), PerRunMicros: micros(20_000_000), Repo: "acme/app"})
	b.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	if policy != "" {
		b.db.Set(projectpolicy.PathShared(), json.RawMessage(policy))
	}
	return b
}

// mintUnder leaves the run's budget token in the bucket under slug, as
// fugaro run does at launch: its slug claim is the run's identity, and the
// run compares it with its own.
func mintUnder(t *testing.T, b *bk, slug string, at time.Time) {
	t.Helper()
	signer, err := token.NewIAMSigner(bkSignerEmail, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "ya29.launcher"}), token.WithIAMEndpoint(b.iam.URL))
	if err != nil {
		t.Fatal(err)
	}
	tok, err := token.MintAt(context.Background(), signer, token.Claims{Slug: slug, Run: runID, FX: token.ExpiryFX(at, 3*time.Hour), FP: "aurora", RB: bkRB}, at)
	if err != nil {
		t.Fatal(err)
	}
	if err := token.PutObject(context.Background(), b.bucket, slug, runID, tok); err != nil {
		t.Fatal(err)
	}
}

// refusedAdmission fails unless the run stopped at the admission check: an
// infra error (so the process exits non-zero and the job shows failed), the
// record kept as infra_error with outcome none, every want in the reason, no
// stage run — and nothing written anywhere but its own record (R2-12). No git
// credential was requested of the provider, the branch lock was not taken, no
// lease was reserved and no outcome and no spend were written: a run that is
// not admitted for this repository leaves its own record — created by the
// claim, so views see an infra_error rather than a run that never started —
// and nothing else at all.
//
// The credential count is what makes the placement of the check observable.
// Every one of these refusals also happens further down bootstrap (the run
// halts on the same error after the clone), so the error, the reason and the
// record alone cannot tell "refused before anything happened" from "refused
// after the repository was cloned and a credential handed out".
//
// Mutation (run, restored): move the admissionCheck call in bootstrap below
// the openProvider block. Every test that comes through here fails, on this
// counter and on nothing else. Mutation (run, restored): move it above the
// claim (claimRecord/save). These tests stay green — the record the store ends
// up with is the refusal either way, because the claim is what creates it —
// and what that ordering breaks is the duplicate's precedence
// (TestRunnerTellsADuplicateApartBeforeAdmission) and, end to end, internal/cli's
// TestExecDuplicateExecutionExitsRemoteError.
func refusedAdmission(t *testing.T, b *bk, rec *runstore.Record, err error, want ...string) {
	t.Helper()
	if err == nil || rec == nil || rec.Status != runstore.StatusInfraError || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("rec = %+v, err = %v: the run was not refused by bootstrap", rec, err)
	}
	for _, w := range want {
		if !strings.Contains(rec.Reason, w) {
			t.Errorf("reason %q lacks %q", rec.Reason, w)
		}
	}
	if strings.Contains(rec.Reason, "\n") {
		t.Errorf("the reason is not one line: %q", rec.Reason)
	}
	if len(b.agent.calls) != 0 {
		t.Errorf("%d stages ran after the refusal", len(b.agent.calls))
	}
	if b.auths == nil {
		t.Fatal("the fixture is not counting git credential requests")
	}
	if *b.auths != 0 {
		t.Errorf("%d git credential requests were made before the refusal: the run was not refused before its provider was opened", *b.auths)
	}
	slug := b.deps.Store.Slug()
	if v := b.db.Value(budget.PathRun(slug, runID) + "/reserved"); v != nil {
		t.Errorf("the refused run reserved %v in its ledger", v)
	}
	if v := b.db.Value(budget.PathSpendRepo(b.day(), slug)); v != nil {
		t.Errorf("the refused run accounted %v against its repository's day", v)
	}
	if ok, err := b.bucket.Exists(context.Background(), lock.Key(slug, "fugaro/"+runID)); err != nil || ok {
		t.Errorf("the refused run took the branch lock (ok = %v, err = %v)", ok, err)
	}
	if v := b.db.Value(budget.PathOutcome(b.day(), slug, runID)); v != nil {
		t.Errorf("the refused run wrote an outcome: %v", v)
	}
	// The one trace it does leave: its own record, claimed before this check
	// and carrying the refusal — a run that is not admitted must be visible
	// as refused rather than as one that never started — and nothing else
	// under the run's prefix.
	stored, rerr := b.harness.store.ReadRecord(context.Background())
	if rerr != nil || stored.RunID != runID || stored.Execution != b.deps.Execution ||
		stored.Status != runstore.StatusInfraError || stored.Reason != rec.Reason {
		t.Errorf("stored = %+v, err = %v: want this execution's own refusal record and nothing else", stored, rerr)
	}
}

// countAuths counts the git credential requests the provider receives, from
// here on, on the fixture (b.auths) as well as in the returned counter:
// admissionRun installs it before the run, so a refusal can be told from a run
// that opened its provider first.
func countAuths(b *bk) *int {
	n := 0
	b.provider.Auth = func(time.Duration) gitprov.GitAuth {
		n++
		return gitprov.GitAuth{}
	}
	b.auths = &n
	return &n
}

// A process with no Cloud Run execution that the invocation did not name local
// is in the cloud (backend.Platform.InCloud fails closed): a platform no
// backend claims, or a job whose environment was edited so that the codec
// reports nothing. The admission used to read "no execution" as "not on Cloud
// Run" and skip every check below — the job binding, the slug, the remote and
// the allow-list — while the git credential stayed scoped to whatever remote
// that environment named (review C2; main's #453 fixed the same guard for the
// phase-1 shape). Such a run has no job name to place, so it cannot adopt or
// rule out a project job's identity, and it stops before any git credential.
//
// Mutation (run, restored): make admissionCheck's skip read Execution == ""
// alone again. The run then goes on to clone and this test fails on the
// credential counter and on the reason.
func TestAdmissionRefusesAnUnclaimedProcessWithNoExecution(t *testing.T) {
	b := admissionRun(t, "github", "acme/app", "off", "", `{"version":2,"allow_repos":["github/acme/*"]}`)
	b.deps.Execution = ""
	b.deps.Local = false
	// A cloud run carries its project (checkProject refuses one without it
	// after the clone, which would hide where this refusal happens).
	b.deps.Project = "aurora"
	rec, err := b.run(t)
	refusedAdmission(t, b, rec, err, "no Cloud Run execution", "not a local invocation", "before any git credential")
}

// A FUGARO_GIT_PROVIDER (the --provider flag's default, internal/cli/exec.go)
// that agrees with the kind the slug derives is not what the binding trusts —
// it is derived from the slug either way — and one that disagrees is refused,
// because the environment may only tighten. The environment is
// launcher-overridable (12a item 2), so a value that could change which
// repository this run is for is exactly what must not be obeyed.
func TestRunnerIgnoresAnOverriddenProviderEnvAmongAgreeingKinds(t *testing.T) {
	const policy = `{"version":2,"allow_repos":["github/acme/*"]}`
	t.Run("the agreeing kind runs", func(t *testing.T) {
		b := admissionRun(t, "github", "acme/app", "off", "", policy)
		b.deps.ProviderKind = "github"
		if rec, err := b.run(t, implement("feature"), review("ship", 0)); err != nil || rec.Status != runstore.StatusSucceeded {
			t.Fatalf("rec = %+v, err = %v: an agreeing --provider must not stop the run", rec, err)
		}
	})
	t.Run("the other kind is refused", func(t *testing.T) {
		b := admissionRun(t, "github", "acme/app", "off", "", policy)
		b.deps.ProviderKind = "bitbucket"
		rec, err := b.run(t)
		refusedAdmission(t, b, rec, err, "bitbucket", "github", "--provider", "may only tighten")
	})
	t.Run("a kind no job provider uses is refused too", func(t *testing.T) {
		// The check has no exemption to hide a value behind. "fake" is not a
		// value a real job carries (internal/cli's providerOptions maps
		// --provider fake to an empty kind) and a local run has no execution
		// to be checked at all, so this is a hand-set environment: it is
		// refused like any other kind that disagrees with the slug.
		b := admissionRun(t, "github", "acme/app", "off", "", policy)
		b.deps.ProviderKind = "fake"
		rec, err := b.run(t)
		refusedAdmission(t, b, rec, err, "fake", "github", "may only tighten")
	})
}

// The remote is where the clone comes from, and a launcher sets it: the job's
// own environment (FUGARO_REMOTE, internal/infra's clone jobs), or the origin
// of a checkout already in the work directory. A remote on another provider's
// host is another repository — the same owner and name is the case worth
// naming — and the run's slug, which keys its locks, its cache and its budget,
// does not name it. The policy admits the repository the slug names, so the
// remote is what refused, before any git credential was requested.
func TestRunnerRefusesARemoteOnAnotherProvidersHost(t *testing.T) {
	b := admissionRun(t, "bitbucket", "acme/app", "off", "", `{"version":2,"allow_repos":["bitbucket/acme/*"]}`)
	guardProviderHost(t, "https://github.com/")
	b.deps.Remote = "https://github.com/acme/app.git"
	rec, err := b.run(t)
	refusedAdmission(t, b, rec, err, "https://github.com/acme/app.git", "github", "bitbucket", "FUGARO_REMOTE", "before any git credential")
}

// The same refusal one host over, and the one the kind cannot see: the remote
// is on the kind's own host and names another repository of the same forge.
// gitprov.KindForURL reads a host and nothing else, so this remote agrees with
// the slug by kind and disagrees with it by repository — FUGARO_REMOTE is a
// launcher's to set, and the run's slug keys its locks, its cache, its budget
// session and the policy that admitted it, all of them for one repository.
// The refusal quotes the repository the remote names, so a launcher that made
// the edit can see what it named.
func TestRunnerRefusesARemoteOnAnotherRepositoryOfTheSameHost(t *testing.T) {
	b := admissionRun(t, "github", "acme/app", "off", "", `{"version":2,"allow_repos":["github/acme/*"]}`)
	guardProviderHost(t, "https://github.com/")
	b.deps.Remote = "https://github.com/acme/other.git"
	rec, err := b.run(t)
	refusedAdmission(t, b, rec, err, "https://github.com/acme/other.git", "acme/other", "FUGARO_REMOTE", "before any git credential")
}

// And the check is the repository the remote names, not the fact that it names
// a URL at all: the fixture's own repository, written the way the launcher
// writes it (internal/infra's repoURL — https://github.com/acme/app.git, on
// the URL that job's FUGARO_REMOTE carries), is admitted and the run
// completes. A guard that refused every remote on the kind's own host would
// stop every job a launcher deploys. Only the transport is local: git's own
// insteadOf rule rewrites the URL onto the fixture's bare remote.
func TestRunnerAdmitsARemoteNamingItsOwnRepository(t *testing.T) {
	b := admissionRun(t, "github", "acme/app", "off", "", `{"version":2,"allow_repos":["github/acme/*"]}`)
	guardProviderHostTo(t, "https://github.com/acme/app.git", "file://"+b.remote)
	b.deps.Remote = "https://github.com/acme/app.git"
	if rec, err := b.run(t, implement("feature"), review("ship", 0)); err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v: the run's own repository, written as its clone URL, must be admitted", rec, err)
	}
}

// The remote binds the endpoint, and not only the host and the repository: what
// a run's git credential is scoped to is scheme://host[:port]
// (internal/gitops' CredentialURL, which has no allow-list of its own), so an
// http:// remote on the provider's own host has this repository's credential
// sent in cleartext, and one naming a port sends it to an endpoint the provider
// does not serve. The launcher writes neither — internal/infra's repoURL
// refuses a repository URL unless it is https, on the provider's host, with no
// port, before it deploys anything — so a launcher-deployed job cannot carry
// one, and a job environment edited after launch (the case this whole check
// exists for) can. Both remotes below name the run's own repository on its own
// provider's host, so the kind comparison and the owner/name comparison admit
// them (gitprov.KindForURL reads the host and nothing else): without the
// endpoint check they were admitted, and the credential went where they point
// (the review finding on this change).
//
// "The scheme is not https" is only the http case: gitops scopes a credential
// to http and https alone, so the ssh spelling of this run's own remote is not
// this check's (TestRunnerAdmitsTheSshSpellingOfItsOwnRemote).
func TestRunnerRefusesARemoteOffTheProvidersEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name, remote, guard, want string
	}{
		{"the scheme is not https", "http://github.com/acme/app.git", "http://github.com/", "not https"},
		{"the authority names a port", "https://github.com:8443/acme/app.git", "https://github.com:8443/", "8443"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := admissionRun(t, "github", "acme/app", "off", "", `{"version":2,"allow_repos":["github/acme/*"]}`)
			guardProviderHost(t, tc.guard)
			b.deps.Remote = tc.remote
			rec, err := b.run(t)
			refusedAdmission(t, b, rec, err, tc.want, "FUGARO_REMOTE", "before any git credential")
		})
	}
}

// The scheme and the port are the credential's, and only where a credential is
// scoped at all: internal/gitops' CredentialURL answers "" for every scheme
// that is not http or https (its own test pins ssh://git@github.com/acme/web.git
// -> ""), so an ssh:// remote has no git credential to send anywhere — and the
// scp-like spelling of one never reached the endpoint check either, because
// remoteEndpoint answers endpoint = false for it. Refusing the URL spelling
// while admitting the scp-like one read two spellings of one remote two ways,
// on a premise ("the credential sent in cleartext") that does not hold where no
// credential is scoped (the review finding on this change). Only the transport
// is local: git's insteadOf rule rewrites the URL onto the fixture's remote.
func TestRunnerAdmitsTheSshSpellingOfItsOwnRemote(t *testing.T) {
	const remote = "ssh://git@github.com/acme/app.git"
	b := admissionRun(t, "github", "acme/app", "off", "", `{"version":2,"allow_repos":["github/acme/*"]}`)
	guardProviderHostTo(t, remote, "file://"+b.remote)
	b.deps.Remote = remote
	if rec, err := b.run(t, implement("feature"), review("ship", 0)); err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v: the scp-like spelling of this remote (git@github.com:acme/app.git) is admitted, so this spelling of it must be too", rec, err)
	}
}

// A remote that carries the credential and is refused for its endpoint must not
// quote the credential either: the refusal names the port, and the two shapes
// the port check reads (a userinfo) are exactly where a forge puts one. The
// endpoint check is what the refusal is about, so the text it adds is the text
// this test reads.
func TestRunnerRefusesAnEndpointWithoutQuotingItsCredential(t *testing.T) {
	b := admissionRun(t, "github", "acme/app", "off", "", `{"version":2,"allow_repos":["github/acme/*"]}`)
	// Every https URL in play is this remote, so the guard's prefix is the
	// scheme: a run that went on would fail its clone locally, whatever
	// endpoint the remote named.
	guardProviderHost(t, "https://")
	b.deps.Remote = "https://x-token-auth:ATBBsecret@github.com:8443/acme/app.git"
	rec, err := b.run(t)
	refusedAdmission(t, b, rec, err, "https://github.com:8443/acme/app.git", "8443", "FUGARO_REMOTE", "before any git credential")
	if strings.Contains(err.Error(), "ATBBsecret") || strings.Contains(rec.Reason, "ATBBsecret") {
		t.Errorf("the refusal quotes the remote's credential: %v", err)
	}
}

// guardProviderHost rewrites every https://github.com/ URL to a path that does
// not exist, so a run that went on would fail its clone locally and offline
// (internal/cli's clone test rewrites the fixture's remote the same way, for the
// same reason): nothing here may reach the forge, not even if the check the
// test covers regresses.
func guardProviderHost(t *testing.T, host string) {
	t.Helper()
	guardProviderHostTo(t, host, "file:///nonexistent/fugaro-admission-guard")
}

// guardProviderHostTo is guardProviderHost with the rewrite's target named:
// "nothing may reach the forge" and "the forge's URL reaches the fixture" are
// the same rule. from is a URL or a URL prefix — git's insteadOf replaces the
// prefix it matches, so https://github.com/ lands the rest of the path on the
// target and the full clone URL lands on the fixture's own remote — and the
// rule applies to the push as much as to the clone.
func guardProviderHostTo(t *testing.T, from, target string) {
	t.Helper()
	gitcfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(gitcfg, []byte("[url \""+target+"\"]\n\tinsteadOf = "+from+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gitcfg)
}

// The same refusal with the remote written the way a forge hands one out: a URL
// whose credential is in a query parameter (?access_token=, Bitbucket's forge
// shape) or in its userinfo. FUGARO_REMOTE is a launcher's, and the refusal
// quotes the remote, so what it quotes must carry no credential — and neither
// must anything else the run publishes: the record it leaves, and every object
// in the bucket.
func TestRunnerRefusesARemoteWithoutQuotingItsCredential(t *testing.T) {
	for _, tc := range []struct {
		name, remote, want, guard string
	}{
		{"the credential in the query", "https://github.com/acme/app.git?access_token=ATBBsecret", "https://github.com/acme/app.git", "https://github.com/"},
		{"the credential in the userinfo", "https://x:ATBBsecret@github.com/acme/app.git", "https://github.com/acme/app.git", "https://github.com/"},
		{"in the query and a fragment", "https://github.com/acme/app.git?access_token=ATBBsecret#ATBBsecret", "https://github.com/acme/app.git", "https://github.com/"},
		// An empty authority: a shape this PR newly refuses, and one url.Parse
		// reads with no host and no userinfo (u.User is nil) — so the redactor
		// had nothing to register while the refusal quoted the string as
		// written, putting the token in the error and in the run's
		// result.json both (the review finding on this change). The guard is
		// the scheme alone, not the provider's host: this spelling does not
		// literally start with "https://github.com/", so a guard keyed on the
		// host would let a regressed check reach the forge for real. The
		// uppercase scheme is the same URL to git, which lowercases it, and
		// git's prefix rewrite is case-sensitive, so its guard is the
		// uppercase prefix.
		{"the credential in an empty authority's userinfo", "https:///x:ATBBsecret@github.com/acme/app.git", "github.com/acme/app.git", "https://"},
		{"four slashes", "https:////x:ATBBsecret@github.com/acme/app.git", "github.com/acme/app.git", "https://"},
		{"an uppercase scheme", "HTTPS:///x:ATBBsecret@github.com/acme/app.git", "github.com/acme/app.git", "HTTPS://"},
		{"an empty authority naming another owner", "https:///x:ATBBsecret@github.com/attacker/malware.git", "github.com/attacker/malware.git", "https://"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := admissionRun(t, "bitbucket", "acme/app", "off", "", `{"version":2,"allow_repos":["bitbucket/acme/*"]}`)
			guardProviderHost(t, tc.guard)
			b.deps.Remote = tc.remote
			rec, err := b.run(t)
			refusedAdmission(t, b, rec, err, tc.want, "github", "bitbucket", "FUGARO_REMOTE")
			if strings.Contains(err.Error(), "ATBBsecret") {
				t.Errorf("the refusal quotes the remote's credential: %v", err)
			}
			if keys := objectsContaining(t, b.harness, "ATBBsecret"); len(keys) > 0 {
				t.Errorf("the remote's credential is in %v", keys)
			}
		})
	}
}

// A remote on the *derived* kind's host is still another repository when it
// names another owner or name: the host-kind check cannot see this one, and
// without the owner/name comparison the run cloned and ran attacker/malware
// under acme/app's slug, with acme/app's locks, cache, budget session and
// allow_repos admission naming work that was not the work being run. The
// launcher refuses such a URL before it deploys anything, so a run meets this
// only when the job's environment was edited after launch — the environment
// may only tighten, never change which repository this run is for.
func TestRunnerRefusesARemoteNamingAnotherRepository(t *testing.T) {
	b := admissionRun(t, "github", "acme/app", "off", "", `{"version":2,"allow_repos":["github/acme/*"]}`)
	b.deps.Remote = "https://github.com/attacker/malware.git"
	// Nothing here may reach github.com even if this check regresses: the same
	// local rewrite TestRunnerRefusesARemoteOnAnotherProvidersHost uses.
	gitcfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(gitcfg, []byte("[url \"file:///nonexistent/fugaro-admission-guard\"]\n\tinsteadOf = https://github.com/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gitcfg)
	rec, err := b.run(t)
	refusedAdmission(t, b, rec, err, "attacker/malware", "acme/app", "FUGARO_REMOTE", "before any git credential")
}

// A remote whose path is not an owner/name is refused, not skipped. Skipping it
// was the first shape of the check: repoNamedByRemote answered "names no
// repository" for any path task.CanonicalRepo refuses, so the comparison never
// ran, and git's HTTP transport resolves dot segments before it asks the
// server (RFC 3986 §5.2.4), which made
// https://github.com/acme/app.git/../../attacker/malware a clone of
// github.com/attacker/malware under acme/app's slug — its locks, its cache, its
// budget session and the policy's admission all naming a repository whose work
// was not the work being run. A remote may only tighten the run's admission,
// never change which repository it is for, so every one of these stops before
// any git credential is requested.
//
// Mutation (run, restored): make repoNamedByRemote return ok=false on
// task.CanonicalRepo's error (the shape this test was written against). Every
// case below is admitted — the run goes on to clone — and no other test fails.
func TestRunnerRefusesARemoteWhosePathIsNotAnOwnerName(t *testing.T) {
	for _, tc := range []struct{ name, remote string }{
		{"dot segments to another repository", "https://github.com/acme/app.git/../../attacker/malware"},
		{"three dot segments", "https://github.com/acme/app.git/../../../attacker/malware"},
		{"a dot segment in the owner", "https://github.com/acme/../attacker/malware.git"},
		{"a trailing dot segment", "https://github.com/acme/app/.."},
		{"an encoded separator", "https://github.com/acme%2Fapp.git"},
		{"encoded separators around an encoded dot segment", "https://github.com/acme%2F..%2Fattacker/malware.git"},
		{"an encoded dot segment", "https://github.com/%2e%2e/attacker/malware.git"},
		{"an empty segment", "https://github.com//attacker/malware.git"},
		{"a character a repository name cannot hold", "https://github.com/acme/app.git;x"},
		// The scp-like form without userinfo, which git honors: it ssh's to
		// github.com and asks for attacker/malware.git, and both halves of the
		// binding read it as "nothing to compare" — the owner/name comparison
		// because remoteRepoPath keyed the shape on "@", the host-kind half
		// because gitprov.KindForURL did (the review finding on this change).
		{"an scp-like remote naming another repository", "github.com:attacker/malware.git"},
		{"a colon-slash scp-like remote naming another repository", "github.com:/attacker/malware.git"},
		// A path url.Parse will not decode, on this run's own host: the host
		// half of the binding has nothing to refuse, so the path read off the
		// authority as written is the whole check — and it is a refusal, where
		// skipping the unreadable remote left it unasked (the review finding on
		// this change).
		{"a path url.Parse will not decode", "https://github.com/acme/app%ZZ.git"},
		{"a backslash in the authority and an unreadable path", "https://github.com\\@evil.example/acme/app%ZZ.git"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			remote := tc.remote
			b := admissionRun(t, "github", "acme/app", "off", "", `{"version":2,"allow_repos":["github/acme/*"]}`)
			b.deps.Remote = remote
			// Nothing here may reach github.com even if this check regresses:
			// the same local rewrite the sibling remote tests use, plus an ssh
			// command that fails at once for the scp-like shapes, which the
			// rewrite does not cover.
			gitcfg := filepath.Join(t.TempDir(), "gitconfig")
			if err := os.WriteFile(gitcfg, []byte("[url \"file:///nonexistent/fugaro-admission-guard\"]\n\tinsteadOf = https://github.com/\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GIT_CONFIG_GLOBAL", gitcfg)
			t.Setenv("GIT_SSH_COMMAND", "false")
			rec, err := b.run(t)
			refusedAdmission(t, b, rec, err, "acme/app", "FUGARO_REMOTE", "before any git credential")
		})
	}
}

// A remote on a host no provider kind owns is refused too, and for a reason of
// its own: the run scopes this repository's git credential to the remote's host
// (internal/gitops' CredentialURL has no allow-list of its own), so a tampered
// FUGARO_REMOTE on the attacker's host would be handed github/acme/app's
// credential and would run whatever that host serves under acme/app's slug —
// its locks, its cache, its budget session and the policy's allow_repos
// admission. The first shape of this check admitted every such host, because
// gitprov.KindForURL answers "" both for a host no kind owns and for a remote
// that names no host at all, and the two were read alike (the review finding on
// this change). A deployed job cannot carry one: the launcher writes the remote
// from the provider's own host and refuses any other URL before it deploys
// (internal/infra's repoURL, internal/backend/gcp's CheckRepoURL).
//
// Mutation (run, restored): read the two alike again — treat an unknown host as
// "no disagreement" — and every case here is admitted, the run going on to
// clone from the attacker's host.
func TestRunnerRefusesARemoteOnAHostNoProviderOwns(t *testing.T) {
	for _, tc := range []struct{ name, remote, want, host string }{
		{"another forge's host", "https://git.example.com/acme/app.git", "https://git.example.com/acme/app.git", "git.example.com"},
		{"a name that only looks like github.com", "https://github.com.evil.example/acme/app.git", "https://github.com.evil.example/acme/app.git", "github.com.evil.example"},
		{"the attacker's own domain", "https://evil.example/acme/app.git", "https://evil.example/acme/app.git", "evil.example"},
		// The scp-like spelling of the same: git ssh's to that host, so the
		// credential's scope is the same question.
		{"an scp-like remote on an unknown host", "evil.example:acme/app.git", "evil.example", "evil.example"},
		// Spellings git accepts and url.Parse refuses. git connects to the
		// host in the authority all the same — libcurl reads a backslash before
		// the "@" as the end of the userinfo, and an escape it will not decode
		// fails no clone — so "this parser cannot read the remote" must not be
		// the answer that skips the binding: it was, and both halves of the
		// check went unasked while the run scoped this repository's git
		// credential to the attacker's host (the review finding on this
		// change).
		{"a backslash before the authority's @", "https://github.com\\@evil.example/acme/app.git", "evil.example", "evil.example"},
		{"an escape url.Parse will not decode", "https://evil.example/acme/app%ZZ.git", "evil.example", "evil.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			remote := tc.remote
			b := admissionRun(t, "github", "acme/app", "off", "", `{"version":2,"allow_repos":["github/acme/*"]}`)
			b.deps.Remote = remote
			// Nothing here may reach that host even if this check regresses:
			// the https rewrite for the URL shapes, and an ssh command that
			// fails at once for the scp-like one.
			gitcfg := filepath.Join(t.TempDir(), "gitconfig")
			if err := os.WriteFile(gitcfg, []byte("[url \"file:///nonexistent/fugaro-admission-guard\"]\n\tinsteadOf = https://"+tc.host+"/\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GIT_CONFIG_GLOBAL", gitcfg)
			t.Setenv("GIT_SSH_COMMAND", "false")
			rec, err := b.run(t)
			refusedAdmission(t, b, rec, err, tc.want, tc.host, "FUGARO_REMOTE", "before any git credential")
			if !strings.Contains(err.Error(), "may only tighten") {
				t.Errorf("the refusal does not say the remote may only tighten the admission: %v", err)
			}
		})
	}
}

// The kind is not the whole question (a review finding): the derived kind's
// own host carries every repository of that provider, so a remote that agrees
// with the kind may still be another repository's, and the run would clone,
// cache, lock and account under its own slug while doing another repository's
// work. internal/infra's repoURL refuses this when it builds the job ("it must
// be this repository, not only this provider"), so it takes an edit of the
// job's environment after launch, exactly like the cross-provider case above.
// The policy admits the repository the slug names, which is why this is the
// remote's refusal and not the allow-list's.
//
// A remote whose path is not a repository name this Fugaro can read is refused
// too (a review finding — it used to be admitted as "not a repository path"):
// "acme/app/../other" and "acme//other" are resolved by the host into some
// repository, and the run cannot tell which, so admitting them would risk
// exactly the cross-repository work this check exists to stop. Nothing genuine
// is lost — the deployer writes a canonical URL (internal/infra's repoURL), so
// these arise only from a remote edited after launch, which is the case the
// check is for.
//
// Mutation (run, restored): return "" for a path CanonicalRepo refuses, as this
// function did before the fix. The two rows whose path CanonicalRepo *refuses*
// (the ".." one and the doubled slash) then fail: the run goes on and halts on
// the clone instead, with the reason the test wants nowhere in it. The tree URL
// is unaffected by that mutation and is here for the other reading — a path
// CanonicalRepo *accepts* and that is another repository, which the check
// caught before the fix and must keep catching.
func TestRunnerRefusesARemoteOfAnotherRepositoryOnTheSameHost(t *testing.T) {
	for _, tc := range []struct{ remote, display, quotedRepo string }{
		// https, the form internal/infra writes into FUGARO_REMOTE.
		{"https://github.com/evil/other.git", "https://github.com/evil/other.git", "evil/other"},
		// scp-like, which a launcher may write and which git clones just the
		// same; the refusal quotes its host alone (remoteDisplay), so the
		// repository the message names is what carries the case.
		{"git@github.com:evil/other.git", "github.com", "evil/other"},
		// The same owner, another repository: the near miss worth naming.
		{"https://github.com/acme/other.git", "https://github.com/acme/other.git", "acme/other"},
		// A path the host resolves to another repository while CanonicalRepo
		// refuses it as written (a review finding): the run cannot know which
		// repository "acme/app/../other.git" fetches, so it refuses rather
		// than admitting it. The refusal quotes the path it could not read.
		{"https://github.com/acme/app/../other.git", "https://github.com/acme/app/../other.git", "acme/app/../other.git"},
		// The same, with the empty segment a doubled slash makes: nothing in
		// the path says "acme/other", and the host is the one that decides.
		{"https://github.com/acme//other", "https://github.com/acme//other", "acme//other"},
		// A path that is not a repository at all is refused for the same
		// reason: the clone would go to the provider's host with a path the
		// run cannot match against its own repository.
		{"https://github.com/acme/app/tree/main", "https://github.com/acme/app/tree/main", "acme/app/tree/main"},
	} {
		t.Run(tc.remote, func(t *testing.T) {
			b := admissionRun(t, "github", "acme/app", "off", "", `{"version":2,"allow_repos":["github/acme/*"]}`)
			b.deps.Remote = tc.remote
			// As above: nothing here may reach github.com, not even if the
			// check this test covers regresses.
			gitcfg := filepath.Join(t.TempDir(), "gitconfig")
			if err := os.WriteFile(gitcfg, []byte("[url \"file:///nonexistent/fugaro-admission-guard\"]\n\tinsteadOf = https://github.com/\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GIT_CONFIG_GLOBAL", gitcfg)
			// The scp-like form would otherwise be carried by ssh, which is a
			// network call before it is anything else: an ssh that exits
			// immediately keeps a regressed run offline too.
			t.Setenv("GIT_SSH_COMMAND", "false")
			rec, err := b.run(t)
			// "never change which repository it is for" is main's remote binding's
			// (remoteAdmissionRefusal) wording of next-0.8.5's "it must be this
			// repository, not only this provider": the merge kept main's one check.
			refusedAdmission(t, b, rec, err, tc.display, tc.quotedRepo, "github", "acme/app", "FUGARO_REMOTE", "never change which repository it is for", "before any git credential")
		})
	}
}

// The host is the whole of "this provider" (review C3): next-0.8.5's first
// remote check (remoteOfAnotherKind, now main's remoteAdmissionRefusal) only knew the hosts gitprov.KindForURL recognises, and every other host —
// a trailing-dot or www spelling of the provider's, the provider's ssh-over-443
// host, or a host that is nobody's provider at all — used to be admitted as
// "no disagreement". The git credential is scoped to the remote's host
// (gitops.CredentialURL), so "https://evil.example/acme/app.git" would have
// handed the provider token to evil.example, and the alias spellings name a
// repository the run cannot verify. A remote that names a host is now refused
// unless that host is exactly the derived kind's own (github.com,
// bitbucket.org) with no port; a remote with no host at all (a local path)
// carries no credential and keeps the path it has.
//
// Mutation (run, restored): drop the remoteAdmissionErr check in
// admissionCheck. Every row then goes on past the admission and fails here.
func TestRunnerRefusesARemoteOffTheProvidersHost(t *testing.T) {
	// why is main's remote binding's wording (remoteAdmissionRefusal) of
	// next-0.8.5's "not github's own host": a host no provider kind owns, or a
	// port the launcher never writes.
	const offHost, withPort = "is not a host any git provider kind this Fugaro uses owns", "names the port 8443"
	for _, tc := range []struct{ remote, host, why string }{
		{"https://github.com./evil/other.git", "github.com.", offHost},
		{"https://www.github.com/evil/other.git", "www.github.com", offHost},
		{"ssh://git@ssh.github.com:443/evil/other.git", "ssh.github.com:443", offHost},
		{"https://evil.example/acme/app.git", "evil.example", offHost},
		{"git@evil.example:acme/app.git", "evil.example", offHost},
		{"https://github.com:8443/acme/app.git", "github.com:8443", withPort},
	} {
		t.Run(tc.remote, func(t *testing.T) {
			b := admissionRun(t, "github", "acme/app", "off", "", `{"version":2,"allow_repos":["github/acme/*"]}`)
			b.deps.Remote = tc.remote
			// Nothing here may reach the network, not even if the check this
			// test covers regresses: git rewrites the remote onto a path that
			// does not exist, and ssh exits at once.
			gitcfg := filepath.Join(t.TempDir(), "gitconfig")
			if err := os.WriteFile(gitcfg, []byte("[url \"file:///nonexistent/fugaro-admission-guard\"]\n\tinsteadOf = "+tc.remote+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GIT_CONFIG_GLOBAL", gitcfg)
			t.Setenv("GIT_SSH_COMMAND", "false")
			rec, err := b.run(t)
			refusedAdmission(t, b, rec, err, tc.host, tc.why, "FUGARO_REMOTE", "before any git credential")
		})
	}
}

// The slug the run was launched for and the repository its task names must
// agree: FUGARO_RUN is what keys the locks, the cache and the budget session,
// and a job launched for one repository must not run another repository's
// work. The policy admitted this repository, so the binding is what refused.
func TestRunnerRefusesSlugMismatch(t *testing.T) {
	b := admissionRun(t, "github", "acme/other", "off", "", `{"version":2,"allow_repos":["*"]}`)
	rec, err := b.run(t)
	refusedAdmission(t, b, rec, err, b.deps.Store.Slug(), "acme/app", "provider kind")
}

// A published policy that does not admit the run's repository stops the run:
// the project's owner has said which repositories may run here.
func TestRunnerRefusesSlugNotInAllowList(t *testing.T) {
	b := admissionRun(t, "github", "acme/app", "off", "", `{"version":2,"allow_repos":["github/other/*","bitbucket/acme/*"]}`)
	rec, err := b.run(t)
	refusedAdmission(t, b, rec, err, "github/acme/app", "allow_repos", "github/other/*", "fugaro project repos add")
}

// R2-12: a run that is not admitted changes nothing. The refusal lands before
// the provider is opened (no git credential request), before the lock, and
// before the budget session exists — so no lease is reserved and no spend is
// accounted for a repository the project's policy does not admit. These are the
// mode-on cases of the same fixtures the refusal tests use, with the whole
// ledger asserted (refusedAdmission).
//
// The same ordering holds on a project job (plan task 3.3 step 2: the check is
// before the call to openProvider and before the session takes a lease), and
// the project case is the one where it is not implied: the provider, the
// profile and the clone URL all come from a decision this run makes, so an
// ordering that put any of them after the credential would hand a git
// credential to a run the profile does not admit. The project fixture brings
// its host local, so a regression that went on to clone would fail locally
// rather than at github.com.
func TestRunnerChecksBeforeTheTokenAndTheLease(t *testing.T) {
	t.Run("a per-repository job", func(t *testing.T) {
		b := admissionRun(t, "github", "acme/app", "enforce", "50", `{"version":2,"allow_repos":["github/other/*"]}`)
		rec, err := b.run(t)
		refusedAdmission(t, b, rec, err, "github/acme/app", "fugaro project repos add")
	})
	t.Run("a project job", func(t *testing.T) {
		b := projectRun(t, projectJobOf(), "enforce", "50", projectPolicyFor(`["github/acme/*"]`, `["github/other/*"]`))
		projectHost(t, b)
		rec, err := b.run(t)
		refusedAdmission(t, b, rec, err, "github/acme/app", "standard", "github/other/*", "never widen")
	})
}

// A policy this run can see and may not read stops the run (R2-3). It is not
// read as "no policy here" — that would drop a mode-on run of a
// policy-enforcing project onto the environment ceiling — and it stops in the
// admission check, before the provider is opened.
//
// The error, the reason and the record cannot tell that apart from the run that
// went on: swallowed here, the same memoized error halts bootstrap a few lines
// later, after the clone. What differs is that the credential was requested
// first, so the counter is what this test rests on.
//
// Mutation (run, restored): swallow the read's error in admissionCheck
// (`if false && err != nil`). This test fails on the counter — the run opens
// its provider and only then halts — and no other test does.
func TestRunnerRefusesAnUnreadablePolicyBeforeAnyCredential(t *testing.T) {
	b := admissionRun(t, "github", "acme/app", "enforce", "50", `{"version":2,"allow_repos":["github/acme/*"]}`)
	b.db.DenyRead(projectpolicy.PathShared())
	rec, err := b.run(t)
	refusedAdmission(t, b, rec, err, "could not be read", projectpolicy.PathShared(), "older than the marker", "does not start on a ceiling the policy did not set")
}

// The other half of the ordering the claim keeps (the admissionCheck doc has
// the whole argument): a duplicate execution is told it is a duplicate whatever
// else about it would have been refused, because the claim comes before the
// admission check. This fixture is a duplicate whose repository would have been
// refused as well — the slug it was launched for is another project's — and the
// claim is still what answers it, with the owner's record left exactly as it
// was and no record of this execution's own anywhere: the claim's write is
// create-only, so a second execution never replaces what is there.
//
// Swapping the two calls fails here: the admission refusal comes back (rec is
// nil, err names the slug this execution was launched for) instead of
// ErrDuplicateExecution, and fugaro exec exits a duplicate as a remote error
// (internal/cli's TestExecDuplicateExecutionExitsRemoteError is the end-to-end
// half), never as an admission refusal. The owner's record is not written over
// in either order — nothing on this path may write it — so what the order
// decides is which answer a duplicate gets, and a duplicate a policy does not
// admit must still be told it is a duplicate.
func TestRunnerTellsADuplicateApartBeforeAdmission(t *testing.T) {
	b := admissionRun(t, "github", "acme/other", "off", "", "")
	ownerRecord(t, b.harness)
	rec, err := b.run(t)
	if !errors.Is(err, runner.ErrDuplicateExecution) {
		t.Fatalf("rec = %+v, err = %v: want ErrDuplicateExecution", rec, err)
	}
	if rec != nil {
		t.Errorf("rec = %+v, want none: this execution does not own the run", rec)
	}
	assertOwnerRecordIntact(t, b.harness)
}

// An installation that has published no policy has no allow-list, and inventing
// "absent means deny" would refuse every run of every installation on upgrade.
// The binding is still checked: it needs no policy, only the slug and the task.
func TestRunnerWithoutAPolicyChecksOnlyTheBinding(t *testing.T) {
	t.Run("no node under the marker", func(t *testing.T) {
		b := admissionRun(t, "github", "acme/app", "off", "", "")
		if rec, err := b.run(t, implement("feature"), review("ship", 0)); err != nil || rec.Status != runstore.StatusSucceeded {
			t.Fatalf("rec = %+v, err = %v: a run with no policy published must run as it always has", rec, err)
		}
	})
	t.Run("no marker at all", func(t *testing.T) {
		b := admissionRun(t, "github", "acme/app", "off", "", "")
		b.db.Set(projectpolicy.PathRulesMarker(), nil)
		b.db.DenyRead(projectpolicy.PathShared())
		if rec, err := b.run(t, implement("feature"), review("ship", 0)); err != nil || rec.Status != runstore.StatusSucceeded {
			t.Fatalf("rec = %+v, err = %v: before the rules are deployed there is no policy to enforce", rec, err)
		}
	})
	t.Run("the binding still refuses", func(t *testing.T) {
		b := admissionRun(t, "github", "acme/other", "off", "", "")
		rec, err := b.run(t)
		refusedAdmission(t, b, rec, err, "acme/app", "provider kind")
	})
}

// An allow-list inside a published policy is the owner's list, and empty means
// deny-all: the owner published a policy, and it admits no repository. That is
// not the same as an installation that has published nothing, which has no
// list to be empty (the test above).
func TestEmptyAllowListInAPublishedPolicyDeniesEverything(t *testing.T) {
	b := admissionRun(t, "github", "acme/app", "off", "", `{"version":2,"allow_repos":[]}`)
	rec, err := b.run(t)
	refusedAdmission(t, b, rec, err, "github/acme/app", "empty", "admits no repository at all", "fugaro project repos add")
}

// The fail-open this PR closes, end to end: a remote written as a URL whose
// authority yields no host this check will read names a host to git all the
// same, and the first shape of the binding answered "this remote names no host"
// — so both the host-kind half and the owner/name half were skipped while the
// run went on to clone whatever the spelling named, under this repository's
// slug and with its git credential scoped to that host. Every remote below
// either names github.com/acme/app.git to libcurl (the empty authority: libcurl
// reads its first path segment as the host) or is one libcurl refuses outright
// ("Bad hostname" for a two-"@" authority, "Malformed input" for a scheme that
// is not a scheme) — and a spelling git will not clone from is not one this
// check may admit unread either.
//
// The guard rewrites every https:// prefix, not only github.com's: an empty
// authority's URL does not literally start with "https://github.com/", so a
// guard keyed on the provider's own host would let a regressed check reach the
// forge for real.
func TestRunnerRefusesARemoteWhoseURLNamesNoHostItCanRead(t *testing.T) {
	for _, tc := range []struct{ name, remote, want string }{
		{
			"an empty authority, which libcurl reads the first path segment of as the host",
			"https:///github.com/acme/app.git", "whose host this check cannot read",
		},
		{
			"an empty authority naming an attacker's host, same reason",
			"https:///evil.example/acme/app.git", "whose host this check cannot read",
		},
		{
			"four slashes, which git refuses and this check will not admit unread",
			"https:////github.com/acme/app.git", "whose host this check cannot read",
		},
		{
			"a two-'@' authority, which libcurl refuses as a bad hostname",
			"https://a@b@github.com/acme/app.git", "whose host this check cannot read",
		},
		{
			"whitespace before the scheme, which libcurl refuses as malformed input",
			" https://github.com/acme/app.git", "whose host this check cannot read",
		},
		{
			"a NUL in the authority, which is a host no refusal may quote",
			"https://github.com\x00/acme/app.git", "whose host this check cannot read",
		},
		{
			"a file url, which names no host for a credential to go to",
			"file:///tmp/acme/app.git", "whose host this check cannot read",
		},
		{
			"a port, where the credential would be scoped to another endpoint",
			"https://github.com:8443/acme/app.git", "whose authority names the port 8443",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := admissionRun(t, "github", "acme/app", "off", "", `{"version":2,"allow_repos":["github/acme/*"]}`)
			guardProviderHost(t, "https://")
			t.Setenv("GIT_SSH_COMMAND", "false")
			b.deps.Remote = tc.remote
			rec, err := b.run(t)
			refusedAdmission(t, b, rec, err, tc.want, "FUGARO_REMOTE", "before any git credential")
		})
	}
}

// The other side of the same finding: ssh ends the userinfo at the LAST "@", so
// "a@b@github.com:acme/app.git" is github.com acme/app to the git that clones
// it. The first-"@" cut read the host as "b@github.com", which no provider owns
// — a refusal of a remote git accepts, and a false one. The rewrite lands the
// clone on the fixture's own remote, off the network.
func TestRunnerAdmitsTheScpLikeRemoteWhoseUserinfoHoldsTwoAts(t *testing.T) {
	const remote = "a@b@github.com:acme/app.git"
	b := admissionRun(t, "github", "acme/app", "off", "", `{"version":2,"allow_repos":["github/acme/*"]}`)
	guardProviderHostTo(t, remote, "file://"+b.remote)
	t.Setenv("GIT_SSH_COMMAND", "false")
	b.deps.Remote = remote
	if rec, err := b.run(t, implement("feature"), review("ship", 0)); err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v: ssh ends this remote's userinfo at the last '@', so it is github.com/acme/app.git and must be admitted", rec, err)
	}
}

// hasControlByte reports whether s holds a byte no line of plain text does:
// C0 (below 0x20), DEL, C1 (0x80-0x9f) — each one an escape a terminal, a
// logger or a JSON reader may act on rather than print.
func hasControlByte(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return r < ' ' || r == 0x7f || (r >= 0x80 && r <= 0x9f)
	})
}

// A refusal is one line of the run's record, and the record is what a person
// reads: the remote it quotes is the job's environment to write, so a control
// character in it — a newline in the host, a carriage return, an ESC — would
// write a line of the environment's own into the record, or a terminal sequence
// the reader's terminal obeys. The whole refusal goes through safetext.OneLine
// (remoteAdmissionErr), and this is that guarantee at the far end: the error
// bootstrap returns, the record it wrote, and every object in the bucket.
//
// The remotes are the shapes an edited environment writes one in: the scp-like
// host (which gitprov.SplitRemote reads as written rather than answering "no
// host", so the refusal has a host to quote), the userinfo of a URL, the
// authority of a URL whose parse fails, and a port. GIT_SSH_COMMAND=false and
// the https guard are the local walls: an scp-like remote with a newline in it
// would ssh somewhere if this check regressed, and the guard's prefix is the
// scheme alone (none of these starts with the provider's host).
func TestRunnerRefusesARemoteWhoseRefusalIsOneLine(t *testing.T) {
	for _, remote := range []string{
		"git@github.com\n:acme/app.git",
		"evil.example\r\nX: y:acme/app.git",
		"git@github.com\x1b[31m:acme/app.git",
		"git@github.com\u0085:acme/app.git",
		"git@github.com\u2028:acme/app.git",
		"https://x\n@github.com/acme/app.git",
		"https://github.com:84\n43/acme/app.git",
		"https://github.com/acme/app\n.git",
	} {
		t.Run(strconv.Quote(remote), func(t *testing.T) {
			b := admissionRun(t, "github", "acme/app", "off", "", `{"version":2,"allow_repos":["github/acme/*"]}`)
			guardProviderHost(t, "https://")
			t.Setenv("GIT_SSH_COMMAND", "false")
			b.deps.Remote = remote
			rec, err := b.run(t)
			refusedAdmission(t, b, rec, err, "FUGARO_REMOTE")
			for _, s := range []string{err.Error(), rec.Reason} {
				if strings.ContainsAny(s, "\n\r") || hasControlByte(s) {
					t.Errorf("the refusal is not one line of plain text: %s", strconv.Quote(s))
				}
			}
			// The remote as written — control bytes and all — is in no object
			// under the run's prefix: the refusal names the host's *printable*
			// text, and task.json holds the run's spec, not the environment.
			if keys := objectsContaining(t, b.harness, remote); len(keys) > 0 {
				t.Errorf("the remote as written is in %v", keys)
			}
		})
	}
}

// A remote written as a URL that names no repository at all is refused before
// anything is opened, not admitted with the owner/name comparison skipped:
// https://github.com and https://github.com?/attacker/malware.git name a host
// and no path, so git would ask that host for the repository such a path names
// — the server serves none — and the run would halt further down for a reason
// that names neither the remote nor the edit that wrote it. Nothing here may
// reach the forge even if the check regresses, so the guard is the scheme.
func TestRunnerRefusesAURLThatNamesNoRepository(t *testing.T) {
	for _, remote := range []string{
		"https://github.com",
		"https://github.com/",
		"https://github.com?/attacker/malware.git",
		"https://github.com?@evil.example/acme/app.git",
	} {
		t.Run(remote, func(t *testing.T) {
			b := admissionRun(t, "github", "acme/app", "off", "", `{"version":2,"allow_repos":["github/acme/*"]}`)
			guardProviderHost(t, "https://")
			b.deps.Remote = remote
			rec, err := b.run(t)
			refusedAdmission(t, b, rec, err, "names no repository", "FUGARO_REMOTE", "before any git credential")
		})
	}
}
