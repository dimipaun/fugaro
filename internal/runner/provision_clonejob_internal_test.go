package runner

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// This file is the job-identity half of the project-job checkout: the
// predicate that decides whether a run clones its repository and installs the
// tools it declares. It sits in a file of its own rather than appended to
// projectjob_internal_test.go because that file is one this branch does not
// otherwise touch: integration-0.9.0 has its own changes to it, and a test
// added there would be a conflict for no reason (the 2026-10-19 review's
// merge-state finding).

// TestCloneJobIsTheJobIdentityOnAProjectJob (the 2026-10-18 review's high
// finding): whether a run's job has a checkout baked into its image decides
// three things at once — the blobless clone (runner.openCheckout), whether
// provision installs the checkout's mise tools (provision's early return on
// cloneJob) and whether review mode checks the base branch out (runner's
// checkReviewPRs path) — and on a shared project job the answer is a fact
// about the job, not a value in an environment.
//
// internal/infra writes FUGARO_CHECKOUT=clone into a per-repository clone
// job's environment (spec.go's ws.Env) and nothing into a project job's
// (projectJobEnv, which carries the installation's variables and the App's
// ID). It does not need to: a project job's image is the installation's base
// image by construction (ProjectJob.Image is lc.BaseImage), which bakes no
// checkout. Reading the variable alone made every project-job run a baked
// job — no blobless clone, no `mise install`, and a review run left on the
// remote's default branch instead of origin/<spec.Ref>.
//
// The predicate is kept in the job identity's own terms, computed and never
// read, for the reason design §12a items 1 and 2 give for the provider and
// the clone URL: a launcher holds run.jobs.runWithOverrides, so a variable it
// can write must not decide whether this run installs the tools its checkout
// declares.
//
// Mutation (run, restored): return r.jobEnv(CheckoutEnv) ==
// config.CheckoutClone from cloneJob() — the first case below fails.
func TestCloneJobIsTheJobIdentityOnAProjectJob(t *testing.T) {
	for _, tc := range []struct {
		name    string
		project bool
		env     []string
		want    bool
	}{
		{"a project job whose environment says otherwise", true, []string{CheckoutEnv + "=baked"}, true},
		{"a project job with no marker at all", true, []string{"PATH=/usr/bin"}, true},
		{"a per-repository clone job", false, []string{CheckoutEnv + "=" + config.CheckoutClone}, true},
		{"a per-repository baked job", false, []string{"PATH=/usr/bin"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := identityRun(t, "", "acme-app", nil, nil, false)
			r.d.Env = tc.env
			if tc.project {
				r.job = jobTriple{Profile: projectpolicy.DefaultProfileName, Provider: "github"}
			}
			if got := r.cloneJob(); got != tc.want {
				t.Errorf("cloneJob() = %v on %s, want %v", got, tc.name, tc.want)
			}
		})
	}
}
