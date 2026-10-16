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

	"github.com/dimipaun/fugaro/internal/backend/gcp"
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
			b := admissionRun(t, "github", "acme/app", "off", "", `{"version":1,"allow_repos":["github/acme/*"]}`)
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
			refusedAdmission(t, b, rec, err, tc.display, tc.quotedRepo, "github", "acme/app", "FUGARO_REMOTE", "not only this provider", "before any git credential")
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
		b := admissionRun(t, "github", "acme/app", "enforce", "50", `{"version":1,"allow_repos":["github/other/*"]}`)
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
