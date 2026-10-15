package runner_test

import (
	"context"
	"encoding/json"
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
func admissionRun(t *testing.T, kind, slugRepo, mode, capUSD, policy string, script ...anthropicfake.Reply) *bk {
	t.Helper()
	b := newBK(t, gwConfig(t, ""), mode, capUSD, script...)
	slug, err := task.Slug(kind, slugRepo)
	if err != nil {
		t.Fatal(err)
	}
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
// record kept as infra_error with outcome none, every want in the reason, and
// no stage run.
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
}

// countAuths counts the git credential requests the provider receives.
func countAuths(b *bk) *int {
	n := 0
	b.provider.Auth = func(time.Duration) gitprov.GitAuth {
		n++
		return gitprov.GitAuth{}
	}
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
		auths := countAuths(b)
		rec, err := b.run(t)
		refusedAdmission(t, b, rec, err, "bitbucket", "github", "--provider", "may only tighten")
		if *auths != 0 {
			t.Errorf("%d git credential requests were made before the refusal", *auths)
		}
	})
}

// The slug the run was launched for and the repository its task names must
// agree: FUGARO_RUN is what keys the locks, the cache and the budget session,
// and a job launched for one repository must not run another repository's
// work. The policy admitted this repository, so the binding is what refused.
func TestRunnerRefusesSlugMismatch(t *testing.T) {
	b := admissionRun(t, "github", "acme/other", "off", "", `{"version":1,"allow_repos":["*"]}`)
	auths := countAuths(b)
	rec, err := b.run(t)
	refusedAdmission(t, b, rec, err, b.deps.Store.Slug(), "acme/app", "provider kind")
	if *auths != 0 {
		t.Errorf("%d git credential requests were made before the refusal", *auths)
	}
}

// A published policy that does not admit the run's repository stops the run:
// the project's owner has said which repositories may run here.
func TestRunnerRefusesSlugNotInAllowList(t *testing.T) {
	b := admissionRun(t, "github", "acme/app", "off", "", `{"version":1,"allow_repos":["github/other/*","bitbucket/acme/*"]}`)
	auths := countAuths(b)
	rec, err := b.run(t)
	refusedAdmission(t, b, rec, err, "github/acme/app", "allow_repos", "github/other/*", "fugaro project repos add")
	if *auths != 0 {
		t.Errorf("%d git credential requests were made before the refusal", *auths)
	}
}

// R2-12: a run that is not admitted changes nothing. The refusal lands before
// the provider is opened (no git credential request), before the lock, and
// before the budget session exists — so no lease is reserved and no spend is
// accounted for a repository the project's policy does not admit.
func TestRunnerChecksBeforeTheTokenAndTheLease(t *testing.T) {
	b := admissionRun(t, "github", "acme/app", "enforce", "50", `{"version":1,"allow_repos":["github/other/*"]}`)
	auths := countAuths(b)
	rec, err := b.run(t)
	refusedAdmission(t, b, rec, err, "github/acme/app", "fugaro project repos add")
	slug := b.deps.Store.Slug()
	if *auths != 0 {
		t.Errorf("%d git credential requests were made before the refusal", *auths)
	}
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
