package runner_test

// D2313 (docs/shared-jobs-default/reconciliation.md, plan task M1) at the
// runner: a repository's `secrets:` is accepted and ignored, the plan leaves
// the name out of the job it deploys (internal/infra's Readiness drops the
// mount, which rebuilds SecretEnvsVar without it), and no store may be written
// for it. The runner is the last reader that could still demand one, and both
// of its paths read the workflow's own `secrets:` from the checkout: the
// declared list agent.BuildEnv validates for every run, and the values review
// mode registers for redaction. A run that went on demanding the value would
// die at the agent's own start in a job deployed exactly as the ruling
// intends — with the image already built and the job deployed, both billable —
// where the ruling's whole point is that such a repository is brought up.

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// repoSecretsOff turns D2313's door off inside one test: what every caller of
// the ruling reads the pre-ruling way, for the controls below.
func repoSecretsOff(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { config.RepoSecretsOff = true })
	config.RepoSecretsOff = false
}

// jobMounts is the job's own account of the secrets it mounts, which the plan
// that deployed it wrote: the fixture's workflow declares one secret, so a job
// the ruling deployed carries a list that does not name it (the variable is
// present and shorter by that name, which is the state the two readers of the
// list have to tell apart from an environment no plan wrote).
func jobMounts(env []string, names ...string) []string {
	return append(env, runner.SecretEnvsVar+"="+strings.Join(names, ","))
}

// A repository-level secret the job does not mount must not stop the run: the
// declared list is the deployment's, and the deployment left the name out.
//
// Mutation (make declaredSecrets return wf.Secrets, the pre-fix list): the
// first subtest fails, finding "bootstrap: building agent environment: secret
// FIXTURE_FAILS_FILE is not set in the runner environment" — the failure a
// repository the ruling brought up gets on every run, with its image built and
// its job deployed.
func TestIgnoredRepositorySecretDoesNotStopARun(t *testing.T) {
	// The state the ruling leaves an installation in: the job is deployed
	// without the mount, and the workflow's declared value is nowhere in the
	// runner's environment (no store can hold one).
	deployed := func(t *testing.T) *harness {
		t.Helper()
		h := newHarness(t, "", nil)
		h.deps.Env = jobMounts(filterEnv(h.deps.Env, "FIXTURE_FAILS_FILE"), "ANTHROPIC_API_KEY")
		return h
	}
	refused := func(t *testing.T, h *harness) *runstore.Record {
		t.Helper()
		rec, err := h.run(t)
		if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "FIXTURE_FAILS_FILE") {
			t.Fatalf("rec = %+v, err = %v", rec, err)
		}
		if len(h.agent.calls) != 0 {
			t.Fatalf("%d agent calls, want none: bootstrap must refuse before the agent starts", len(h.agent.calls))
		}
		return rec
	}
	t.Run("unmounted, the run starts", func(t *testing.T) {
		h := deployed(t)
		if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
			t.Fatalf("a run of a repository whose declared secret the ruling ignores failed: %v", err)
		}
		if len(h.agent.calls) == 0 {
			t.Fatal("the agent never started")
		}
	})
	// The other half of the same answer: the deployment decides, and a job
	// that DOES mount the name is owed its value — the pre-ruling deployment,
	// and any name a shared job mounts (the file's own declaration is not what
	// decides that one, which is why the list is read here and the file's
	// names are not asked about).
	t.Run("a job that mounts the name still requires it", func(t *testing.T) {
		h := newHarness(t, "", nil)
		h.deps.Env = jobMounts(filterEnv(h.deps.Env, "FIXTURE_FAILS_FILE"), "FIXTURE_FAILS_FILE")
		refused(t, h)
	})
	// The door off is the machinery the ruling keeps, unchanged.
	t.Run("door off, still required", func(t *testing.T) {
		repoSecretsOff(t)
		refused(t, deployed(t))
	})
	// A job carrying no list at all is not read as one mounting nothing: no
	// plan wrote that environment (a local `fugaro exec --task-file`), so the
	// pre-ruling answer stands rather than the run starting on a guess. Every
	// job this release deploys carries the list, so the ruling's own
	// deployment is never this case.
	t.Run("a job that carries no list, still required", func(t *testing.T) {
		h := newHarness(t, "", nil)
		h.deps.Env = filterEnv(h.deps.Env, "FIXTURE_FAILS_FILE")
		refused(t, h)
	})
}

// A review run's own path reads the same list: it never hands the workflow's
// secrets to the agent, and registers each value it holds for redaction
// instead — which refused a repository-level secret the job does not mount
// just as loudly, before any pull request was reviewed.
//
// Mutation (the same one as above): the first subtest fails, finding
// "bootstrap: secret FIXTURE_FAILS_FILE is not set in the runner environment"
// from review mode's registration loop rather than from agent.BuildEnv.
func TestIgnoredRepositorySecretDoesNotStopAReviewRun(t *testing.T) {
	reviewRun := func(t *testing.T) *harness {
		t.Helper()
		h := newHarness(t, "", reviewTaskSpec("reviewonly", reviewOnlyYAML, 7))
		h.deps.Env = jobMounts(filterEnv(h.deps.Env, "FIXTURE_FAILS_FILE"), "ANTHROPIC_API_KEY")
		pushReviewBranch(t, h, "feature", func(dir string) {
			testutil.WriteFiles(t, dir, map[string]string{"other.txt": "x\n"})
		})
		addPR(h, 7, "feature", "")
		return h
	}
	t.Run("unmounted, the review runs", func(t *testing.T) {
		h := reviewRun(t)
		if _, err := h.run(t, review("ship", 0)); err != nil {
			t.Fatalf("a review of a repository whose declared secret the ruling ignores failed: %v", err)
		}
		if len(h.agent.calls) == 0 {
			t.Fatal("the review agent never started")
		}
	})
	t.Run("door off, still required", func(t *testing.T) {
		repoSecretsOff(t)
		h := reviewRun(t)
		rec, err := h.run(t, review("ship", 0))
		if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "FIXTURE_FAILS_FILE") {
			t.Fatalf("rec = %+v, err = %v", rec, err)
		}
		if len(h.agent.calls) != 0 {
			t.Fatalf("%d agent calls, want none (bootstrap must fail first)", len(h.agent.calls))
		}
	})
}
