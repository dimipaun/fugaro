package batch

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/backendtest"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// projectJob is the shared project job these tests launch a workflow on, and
// projectJobAccount the account the deployer creates for that job (gcp's
// derivations, internal/infra's ProjectJobs): the (profile, provider, account)
// triple is what the name hashes and cannot be read back out of it (R2-5), so
// the account is the pair a launcher records for the job.
func projectJob() string { return gcp.ProjectJobName("standard", "github", "team-a") }

func projectJobAccount() string {
	return gcp.ProjectServiceAccountID("standard", "github", "team-a") + "@" + testProject + ".iam.gserviceaccount.com"
}

// accountOf is the account the job Batch was sent runs its VMs as.
func accountOf(t *testing.T, job map[string]any) string {
	t.Helper()
	got := object(t, job, "allocationPolicy", "serviceAccount")["email"]
	email, ok := got.(string)
	if !ok {
		t.Fatalf("allocationPolicy.serviceAccount.email = %#v, want a string", got)
	}
	return email
}

// TestAProjectJobWorkflowRunsUnderThatJobsAccount is BR10's red step: before
// it, this launch was refused with "Cloud Batch has no project jobs yet"
// (F10), because the installation had one account -- its block's -- and a
// workflow the project's profiles run on a shared project job would have run
// under an identity the policy did not choose (D3103, F11).
//
// The job is created now, under the account the installation records for that
// project job, and it is the deployed job's own environment a run inside it
// reads: a Batch job is built per run from nothing, so the launcher writes the
// deployed job's values into it and names the job it stands in for (BR8, R6).
func TestAProjectJobWorkflowRunsUnderThatJobsAccount(t *testing.T) {
	ctx := context.Background()
	job, account := projectJob(), projectJobAccount()
	b, bf, _, _ := newTestBackend(t, func(o *Options) {
		o.JobAccounts = map[string]string{job: account}
	})
	s := spec()
	s.Job = job
	if _, err := b.Launch(ctx, s); err != nil {
		t.Fatalf("Launch of a workflow on the shared project job %s = %v, want a launch: this backend has project jobs now", job, err)
	}
	creates := bf.Creates()
	if len(creates) != 1 {
		t.Fatalf("jobs.create calls = %d, want 1", len(creates))
	}
	if got := accountOf(t, creates[0].Job); got != account {
		t.Errorf("the job's VMs run as %q, want %q: the account of the deployed job the run stands in for", got, account)
	}
	vars := object(t, creates[0].Job, "taskGroups", "0", "taskSpec", "environment", "variables")
	if got := vars[backend.DeployedJobEnv]; got != job {
		t.Errorf("the task's %s = %v, want %q: the project job the run stands in for, which the runner places the run by (R6)", backend.DeployedJobEnv, got, job)
	}
}

// TestAPerRepositoryWorkflowRunsUnderItsOwnAccount: a per-repository workflow
// runs under the account of its own deployed job, and never under the block's
// single one. That account is a derivation of the job's own two facts
// (gcp.ServiceAccountID) -- the account the repository root creates for it, and
// the one the Cloud Run job of the same workflow runs as -- so nothing has to
// be recorded for it.
//
// That is not a claim that an installation whose block records no account
// launches: New still refuses an empty Options.ServiceAccount (batch.go's
// checkServiceAccount), which is F11's other half and is reported rather than
// fixed here. What this pins is only which account the job's VMs get.
//
// An installation that records an account for that job is believed over the
// derivation: Options.JobAccounts' keys are both classes' job names, and what
// the launcher knows about a job it deployed is better than what this backend
// can compute.
func TestAPerRepositoryWorkflowRunsUnderItsOwnAccount(t *testing.T) {
	ctx := context.Background()
	derived := perRepositoryJobAccount(testProject, backendtest.Slug, backendtest.Workflow)
	if derived == testSA {
		t.Fatalf("the derived account and the block's are both %q, so this test cannot tell the two apart", derived)
	}
	recorded := "fugaro-recorded-pair@" + testProject + ".iam.gserviceaccount.com"
	b, bf, _, _ := newTestBackend(t, func(o *Options) {
		o.JobAccounts = map[string]string{gcp.JobName(backendtest.Slug, backendtest.Workflow): recorded}
	})
	if _, err := b.Launch(ctx, spec()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if got := accountOf(t, bf.Creates()[0].Job); got != recorded {
		t.Errorf("the job's VMs run as %q, want the account recorded for its own job %q", got, recorded)
	}
	// Nothing recorded for the job: the account of the deployed job it stands
	// in for is what its VMs get, which is the derivation of the job's name.
	b2, bf2, _, _ := newTestBackend(t, nil)
	if _, err := b2.Launch(ctx, spec()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if got := accountOf(t, bf2.Creates()[0].Job); got != derived {
		t.Errorf("the job's VMs run as %q, want the derived account of its own job %q", got, derived)
	}
}

// TestTheBlocksAccountIsNotRead: the one account an installation's block
// carries decides nothing about a job's identity (BR10, D3103). It is a field
// of the installation rather than of a job, F11 shows it is recorded only when
// the project holds one account and that a shared project job's account is not
// among the ones it is read from, and a run placed under it would be a run of
// the wrong repository's identity on a multi-job installation.
//
// So neither a per-repository workflow nor a shared project job's runs as it,
// and the block's account is not what stands in where the installation records
// none for a project job: that launch is refused, with the job named and
// nothing created, because there is no account this backend can know that job
// runs as -- the block's one is not that job's to run under.
func TestTheBlocksAccountIsNotRead(t *testing.T) {
	ctx := context.Background()
	const block = "fugaro-block-run@" + testProject + ".iam.gserviceaccount.com"
	b, bf, _, _ := newTestBackend(t, func(o *Options) { o.ServiceAccount = block })
	if _, err := b.Launch(ctx, spec()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	blob, err := json.Marshal(bf.Creates()[0].Job)
	if err != nil {
		t.Fatalf("marshalling the created job: %v", err)
	}
	if strings.Contains(string(blob), block) {
		t.Errorf("the job Batch was sent carries the block's account %s:\n%s", block, blob)
	}
	job := projectJob()
	b2, bf2, _, _ := newTestBackend(t, func(o *Options) { o.ServiceAccount = block })
	s := spec()
	s.Job = job
	_, err = b2.Launch(ctx, s)
	if !errors.Is(err, backend.ErrRejected) {
		t.Fatalf("Launch of a project job whose account is not recorded = %v, want ErrRejected", err)
	}
	if !strings.Contains(err.Error(), job) {
		t.Errorf("the refusal %q does not name the job it cannot account for (%s)", err, job)
	}
	if got := bf2.Creates(); len(got) != 0 {
		t.Fatalf("a launch refused for want of an account created a job: %v", got)
	}
}

// TestADefaultComputeAccountRecordedForAJobIsRefused: New keeps the rule for
// the block's own field (checkServiceAccount), and it has to hold for the
// account a job is really built with too -- otherwise the record BR10 reads is
// the way around it, and a run's VMs are Editor on the whole project under an
// account that never went through New's check.
//
// Both classes of deployed job, because what the rule is about is the account
// a launch runs as rather than the field it came from; the account is named in
// the refusal and nothing is created.
func TestADefaultComputeAccountRecordedForAJobIsRefused(t *testing.T) {
	const defaultAccount = "123456789-compute@developer.gserviceaccount.com"
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		job    string
		launch func() backend.LaunchSpec
	}{
		{"a per-repository workflow", gcp.JobName(backendtest.Slug, backendtest.Workflow), spec},
		{"a workflow on a shared project job", projectJob(), func() backend.LaunchSpec {
			s := spec()
			s.Job = projectJob()
			return s
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, bf, _, _ := newTestBackend(t, func(o *Options) {
				o.JobAccounts = map[string]string{tc.job: defaultAccount}
			})
			_, err := b.Launch(ctx, tc.launch())
			if !errors.Is(err, backend.ErrRejected) {
				t.Fatalf("Launch with %s recorded for the job %s = %v, want ErrRejected", defaultAccount, tc.job, err)
			}
			if !strings.Contains(err.Error(), defaultAccount) {
				t.Errorf("the refusal %q does not name the account it refuses (%s)", err, defaultAccount)
			}
			if got := bf.Creates(); len(got) != 0 {
				t.Fatalf("a launch refused for its account created a job: %v", got)
			}
		})
	}
}
