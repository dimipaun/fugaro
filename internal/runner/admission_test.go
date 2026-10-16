package runner_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/lock"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
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
	b := newBK(t, gwConfig(t, ""), mode, capUSD, script...)
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
	b.deps.Execution = "projects/aurora-fp/locations/europe-west1/jobs/fugaro/executions/" + runID
	store := runstore.Open(b.harness.bucket, slug, runID)
	spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Task: "Add a feature"}
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
// stage run — and nothing changed anywhere (R2-12). No git credential was
// requested of the provider, the branch lock was not taken, no lease was
// reserved and no outcome and no spend were written: a run that is not admitted
// for this repository must be indistinguishable from one that never started.
//
// The credential count is what makes the placement of the check observable.
// Every one of these refusals also happens further down bootstrap (the run
// halts on the same error after the clone), so the error, the reason and the
// record alone cannot tell "refused before anything happened" from "refused
// after the repository was cloned and a credential handed out".
//
// Mutation (run, restored): move the admissionCheck call in bootstrap below
// the openProvider block. Every test that comes through here fails, on this
// counter and on nothing else.
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

// A FUGARO_GIT_PROVIDER (the --provider flag's default, internal/cli/exec.go)
// that agrees with the kind the slug derives is not what the binding trusts —
// it is derived from the slug either way — and one that disagrees is refused,
// because the environment may only tighten. The environment is
// launcher-overridable (12a item 2), so a value that could change which
// repository this run is for is exactly what must not be obeyed.
func TestRunnerIgnoresAnOverriddenProviderEnvAmongAgreeingKinds(t *testing.T) {
	const policy = `{"version":1,"allow_repos":["github/acme/*"]}`
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
	b := admissionRun(t, "bitbucket", "acme/app", "off", "", `{"version":1,"allow_repos":["bitbucket/acme/*"]}`)
	b.deps.Remote = "https://github.com/acme/app.git"
	// Nothing here may reach github.com, not even if the check this test
	// covers regresses: git rewrites the URL onto a path that does not exist,
	// so a run that went on would fail its clone locally and offline
	// (internal/cli's clone test rewrites the fixture's remote the same way,
	// for the same reason).
	gitcfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(gitcfg, []byte("[url \"file:///nonexistent/fugaro-admission-guard\"]\n\tinsteadOf = https://github.com/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gitcfg)
	rec, err := b.run(t)
	refusedAdmission(t, b, rec, err, "https://github.com/acme/app.git", "github", "bitbucket", "FUGARO_REMOTE", "before any git credential")
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
	b := admissionRun(t, "github", "acme/app", "off", "", `{"version":1,"allow_repos":["github/acme/*"]}`)
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			remote := tc.remote
			b := admissionRun(t, "github", "acme/app", "off", "", `{"version":1,"allow_repos":["github/acme/*"]}`)
			b.deps.Remote = remote
			// Nothing here may reach github.com even if this check regresses:
			// the same local rewrite the sibling remote tests use.
			gitcfg := filepath.Join(t.TempDir(), "gitconfig")
			if err := os.WriteFile(gitcfg, []byte("[url \"file:///nonexistent/fugaro-admission-guard\"]\n\tinsteadOf = https://github.com/\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GIT_CONFIG_GLOBAL", gitcfg)
			rec, err := b.run(t)
			refusedAdmission(t, b, rec, err, "acme/app", "FUGARO_REMOTE", "before any git credential")
		})
	}
}

// The slug the run was launched for and the repository its task names must
// agree: FUGARO_RUN is what keys the locks, the cache and the budget session,
// and a job launched for one repository must not run another repository's
// work. The policy admitted this repository, so the binding is what refused.
func TestRunnerRefusesSlugMismatch(t *testing.T) {
	b := admissionRun(t, "github", "acme/other", "off", "", `{"version":1,"allow_repos":["*"]}`)
	rec, err := b.run(t)
	refusedAdmission(t, b, rec, err, b.deps.Store.Slug(), "acme/app", "provider kind")
}

// A published policy that does not admit the run's repository stops the run:
// the project's owner has said which repositories may run here.
func TestRunnerRefusesSlugNotInAllowList(t *testing.T) {
	b := admissionRun(t, "github", "acme/app", "off", "", `{"version":1,"allow_repos":["github/other/*","bitbucket/acme/*"]}`)
	rec, err := b.run(t)
	refusedAdmission(t, b, rec, err, "github/acme/app", "allow_repos", "github/other/*", "fugaro project repos add")
}

// R2-12: a run that is not admitted changes nothing. The refusal lands before
// the provider is opened (no git credential request), before the lock, and
// before the budget session exists — so no lease is reserved and no spend is
// accounted for a repository the project's policy does not admit. This is the
// mode-on case of the same fixture the refusal tests above use, with the whole
// ledger asserted (refusedAdmission).
func TestRunnerChecksBeforeTheTokenAndTheLease(t *testing.T) {
	b := admissionRun(t, "github", "acme/app", "enforce", "50", `{"version":1,"allow_repos":["github/other/*"]}`)
	rec, err := b.run(t)
	refusedAdmission(t, b, rec, err, "github/acme/app", "fugaro project repos add")
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
	b := admissionRun(t, "github", "acme/app", "enforce", "50", `{"version":1,"allow_repos":["github/acme/*"]}`)
	b.db.DenyRead(projectpolicy.PathShared())
	rec, err := b.run(t)
	refusedAdmission(t, b, rec, err, "could not be read", projectpolicy.PathShared(), "older than the marker", "does not start on a ceiling the policy did not set")
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
	b := admissionRun(t, "github", "acme/app", "off", "", `{"version":1,"allow_repos":[]}`)
	rec, err := b.run(t)
	refusedAdmission(t, b, rec, err, "github/acme/app", "empty", "admits no repository at all", "fugaro project repos add")
}
