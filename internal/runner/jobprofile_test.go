package runner_test

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// jobProfileConfig is the fixture's fugaro.yaml with the selection in question,
// and jobProfilePolicy is a published policy defining those profiles. The name
// "big" is the one the fixture's workflow selects in the tests below.
const (
	jobProfileSelection = "  app:\n    job_profile: big\n"

	jobProfilePolicy = `{"version":2,"allow_repos":["github/acme/*"],"profiles":{` +
		`"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h"},` +
		`"big":{"provider":"github","cpu":4,"memory":"8Gi","timeout_class":"12h"}}}`

	jobProfilePolicyWithoutBig = `{"version":2,"allow_repos":["github/acme/*"],"profiles":{` +
		`"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h"}}}`
)

// selectedConfig is the fixture config with the workflow's job_profile set.
func selectedConfig(t *testing.T) string {
	t.Helper()
	cfg := gwConfig(t, "")
	out := strings.Replace(cfg, "  app:\n", jobProfileSelection, 1)
	if out == cfg {
		t.Fatal("the fixture has no app: workflow to put a job_profile under")
	}
	return out
}

// refusedJobProfile fails unless the run stopped on the job profile check: an
// infra error, no stage run, and a one-line reason holding every want. The
// check runs after the clone (the workflow is only known once the config is
// resolved), so — unlike refusedAdmission — this does not assert that no
// credential was requested; what matters is that nothing of the run's own work
// started.
func refusedJobProfile(t *testing.T, b *bk, rec *runstore.Record, err error, want ...string) {
	t.Helper()
	if err == nil || rec == nil || rec.Status != runstore.StatusInfraError || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("rec = %+v, err = %v: the run was not refused", rec, err)
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

// TestRunnerRefusesAJobProfileThePolicyDoesNotDefine: plan task 3.1 step 1's
// runner-start half. A workflow that names a job profile the published policy
// does not define halts the run — it is never run on another profile, which
// would be a job shape, a secret set and an admission list its owner did not
// choose. The mutation is dropping the checkJobProfile call from bootstrap: the
// first two runs below reach success on the default profile.
func TestRunnerRefusesAJobProfileThePolicyDoesNotDefine(t *testing.T) {
	t.Run("the policy defines profiles but not this one", func(t *testing.T) {
		b := admissionRunCfg(t, "github", "acme/app", "off", "", selectedConfig(t), jobProfilePolicyWithoutBig)
		rec, err := b.run(t)
		refusedJobProfile(t, b, rec, err, "big", "does not define the job profile", "standard")
	})
	t.Run("no policy is published", func(t *testing.T) {
		b := admissionRunCfg(t, "github", "acme/app", "off", "", selectedConfig(t), "")
		rec, err := b.run(t)
		refusedJobProfile(t, b, rec, err, "big", "published no policy")
	})
	t.Run("the policy defines it", func(t *testing.T) {
		// The control: the same config, the same selection, a policy that
		// defines the profile — the run proceeds, so the two tests above rest
		// on the profile and not on the config being rejected wholesale.
		b := admissionRunCfg(t, "github", "acme/app", "off", "", selectedConfig(t), jobProfilePolicy)
		rec, err := b.run(t, implement("feature"), review("ship", 0))
		if err != nil || rec.Status != runstore.StatusSucceeded {
			t.Fatalf("rec = %+v, err = %v: a selection the policy defines must run", rec, err)
		}
	})
}

// TestRunnerRunsAWorkflowThatNamesNoJobProfile: the default. A policy that
// carries a profiles block must define standard, and one that carries none
// defines nothing this release applies — so a workflow that names no profile is
// not refused in either case. The mutation is checking the *default* instead of
// the written selection (ProfileFor(wf.JobProfileOrDefault()) unconditionally):
// every run of an installation whose policy has no profiles block then halts,
// which is a policy published for its allow-list alone.
func TestRunnerRunsAWorkflowThatNamesNoJobProfile(t *testing.T) {
	t.Run("a policy with profiles", func(t *testing.T) {
		b := admissionRun(t, "github", "acme/app", "off", "", jobProfilePolicy)
		if rec, err := b.run(t, implement("feature"), review("ship", 0)); err != nil || rec.Status != runstore.StatusSucceeded {
			t.Fatalf("rec = %+v, err = %v: the default profile is defined", rec, err)
		}
	})
	t.Run("a policy with no profiles block", func(t *testing.T) {
		b := admissionRun(t, "github", "acme/app", "off", "", `{"version":2,"allow_repos":["github/acme/*"]}`)
		if rec, err := b.run(t, implement("feature"), review("ship", 0)); err != nil || rec.Status != runstore.StatusSucceeded {
			t.Fatalf("rec = %+v, err = %v: a policy that sets no profile refuses nothing", rec, err)
		}
	})
	t.Run("no policy at all", func(t *testing.T) {
		b := admissionRun(t, "github", "acme/app", "off", "", "")
		if rec, err := b.run(t, implement("feature"), review("ship", 0)); err != nil || rec.Status != runstore.StatusSucceeded {
			t.Fatalf("rec = %+v, err = %v: a workflow that names no profile must run as it always has", rec, err)
		}
	})
}

// TestRunnerRunsAnExplicitStandardJobProfileLikeTheDefault: a workflow that
// writes `job_profile: standard` says what omitting the key says
// (docs/project-layer.md: a workflow that names none runs on standard), so it
// runs wherever the omission runs — with no policy, and with a policy that
// carries no profiles block. The mutation is checking every written name: the
// two runs below then halt on a selection identical to the default.
func TestRunnerRunsAnExplicitStandardJobProfileLikeTheDefault(t *testing.T) {
	cfg := gwConfig(t, "")
	standard := strings.Replace(cfg, "  app:\n", "  app:\n    job_profile: standard\n", 1)
	if standard == cfg {
		t.Fatal("the fixture has no app: workflow to put a job_profile under")
	}
	for _, tc := range []struct{ what, policy string }{
		{"no policy at all", ""},
		{"a policy with no profiles block", `{"version":2,"allow_repos":["github/acme/*"]}`},
		{"a policy with profiles", jobProfilePolicy},
	} {
		t.Run(tc.what, func(t *testing.T) {
			b := admissionRunCfg(t, "github", "acme/app", "off", "", standard, tc.policy)
			if rec, err := b.run(t, implement("feature"), review("ship", 0)); err != nil || rec.Status != runstore.StatusSucceeded {
				t.Fatalf("rec = %+v, err = %v: an explicit standard must run where the default runs", rec, err)
			}
		})
	}
}
