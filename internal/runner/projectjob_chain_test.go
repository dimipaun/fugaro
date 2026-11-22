package runner

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/task"
)

// The tests in this file are CS13's half of the model-credentials-routing plan
// for the SHARED PROJECT JOB: a job whose model credentials the credentials
// service serves holds none of its own, so the run's door on that credential
// reads neither agent.auth nor the account the job's name carries — which
// credential serves a call is the service's answer, walked from the project's
// routing table.
//
// The two facts the door is keyed on are the deployer's own statements about
// the container, and both are in the execution's environment: the fact that the
// job's credentials are served (config.CredentialsServedEnv, which
// internal/infra's ProjectJobs plans — its own test pins the planning) and the
// service's address (credentialsServiceURLEnv, which the launcher hands the
// run). Neither is a claim a run makes about itself: the door asks the
// environment it was started with.

// chainRun is a run placed in the shared project job named by job, running with
// this auth and with the execution's environment as given. The policy is the
// fixture's serving one, so the coding arm's rules have something to be about:
// an arm that reads the account has a mismatch to refuse here, and the served
// arm has nothing to read.
func chainRun(t *testing.T, job, slug string, spec *task.Spec, p *projectpolicy.Policy, auth string, env []string) *run {
	t.Helper()
	r := accountRun(t, job, slug, spec, p)
	r.cfg = &config.Config{Agent: config.Agent{Auth: auth}}
	r.d.Env = env
	return r
}

// TestAProjectJobGoesByTheTable is CS13 for the shared project job.
//
// The run below is one the coding arm refuses and the served arm admits, and it
// is the same run in both halves: its own credential is another account than
// the one the job's name carries. That is the difference the task is about —
// the coding arm decides a run may not hold a token that is not its own, while
// a job the service serves holds no token at all, so there is nothing for the
// run's own credential to disagree with, and the credential that serves its
// calls is the service's answer.
//
// The cases are:
//
//   - the same run, refused in the job whose credentials are NOT served (the
//     red step: the door reads the account) and admitted in the one that is;
//   - a served job's door, admitted with the memoized policy read cleared, so
//     an admission is the door having asked nothing of the policy at all;
//   - a served job whose run reaches no model at all, refused with the fix —
//     the job holds no credential, so a run whose model path reaches neither
//     the service nor an identity of its own would die at its first model call;
//   - a served job's vertex run, admitted without the service: Vertex is the
//     installation's own identity and no mount, so the class never took it;
//   - an entry that carries anything but the fact's true value is not the fact,
//     which is the fail-closed reading config.CredentialsServedEnv states;
//   - and the fact over a mount the class did not drop is not the fact either:
//     a container that still carries one of the two Claude variables is
//     answered by the coding arm, while a provider's key — which the served
//     class leaves mounted — is no evidence against it;
//   - the review arm asked first, twice over: a coding run of a review job is
//     still refused while its environment carries the fact, and a review run of
//     one is admitted although it carries the fact and no address. The review
//     variant's environment is its coding job's (internal/infra's reviewVariant
//     clones it), so a served profile's review job carries the fact too, and an
//     arm asked first would answer for exactly the runs the review arm exists
//     to refuse.
func TestAProjectJobGoesByTheTable(t *testing.T) {
	ctx := context.Background()
	slug := identitySlug(t, "github", "acme/app")
	const (
		personal = "acme-personal"
		work     = "acme-work"
	)
	serving := servingPolicy([]string{personal, work}, map[string][]string{
		personal: {"github/acme/app"},
		work:     {"github/acme/app"},
	})
	job := gcp.ProjectJobName("standard", "github", personal)
	// The run's own credential is the OTHER account: the case the coding arm
	// exists to refuse, and the one that stops being the door's business once
	// the job holds no token.
	spec := func() *task.Spec {
		return &task.Spec{Version: 1, RunID: "20261122-130419-3255", Repo: "acme/app", Ref: "main",
			Workflow: identityWorkflow, OAuthToken: work, Task: "Add a feature"}
	}
	servedEnv := []string{config.CredentialsServedEnv + "=" + config.ServedValue}
	reached := append(append([]string{}, servedEnv...), credentialsServiceURLEnv+"=https://creds.example")

	t.Run("the account is read in a job that holds the token, and not in one that does not", func(t *testing.T) {
		held := chainRun(t, job, slug, spec(), serving, config.AuthOAuth, nil)
		err := held.checkProjectJobAccount(ctx, config.AuthOAuth)
		if err == nil {
			t.Fatal("a run whose own credential is another account was admitted to a job that mounts that account's token: the coding arm's rule is missing")
		}
		for _, want := range []string{job, config.ClaudeAccountSecret(personal), work} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the coding arm's refusal %q lacks %q", err, want)
			}
		}

		served := chainRun(t, job, slug, spec(), serving, config.AuthOAuth, reached)
		if err := served.checkProjectJobAccount(ctx, config.AuthOAuth); err != nil {
			t.Fatalf("a run of a served job whose own credential is another account was refused: %v — the container mounts no token, so there is no account for the run's credential to disagree with", err)
		}
	})

	t.Run("the served arm reads no policy", func(t *testing.T) {
		served := chainRun(t, job, slug, spec(), serving, config.AuthOAuth, reached)
		// The memoized read is cleared, so a door that read the policy would
		// have to read it again — and the fixture's store holds no node, which
		// the coding arm answers with its own halt. An admission here is
		// therefore the door having asked nothing of the project's policy at
		// all, which is what "the credential is no longer the policy's to
		// bound" means once the token is gone.
		served.projPolicy = policyRead{}
		if err := served.checkProjectJobAccount(ctx, config.AuthOAuth); err != nil {
			t.Fatalf("a served job's door read the published policy: %v", err)
		}
	})

	t.Run("a served run that reaches no model is refused with the fix", func(t *testing.T) {
		r := chainRun(t, job, slug, spec(), serving, config.AuthOAuth, servedEnv)
		err := r.checkProjectJobAccount(ctx, config.AuthOAuth)
		if err == nil {
			t.Fatal("a run of a job that mounts no model credential and carries no service address was admitted: it would die at its first model call")
		}
		for _, want := range []string{job, config.AuthOAuth, credentialsServiceURLEnv, "The run stops"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal %q lacks %q", err, want)
			}
		}
	})

	t.Run("a served run on the installation's own identity needs no address", func(t *testing.T) {
		r := chainRun(t, job, slug, spec(), serving, "vertex", servedEnv)
		if err := r.checkProjectJobAccount(ctx, "vertex"); err != nil {
			t.Fatalf("a vertex run of a served job was refused: %v — Vertex is the job's own GCP identity and no mount, so the served class never took it", err)
		}
	})

	t.Run("the fact over a mount the class did not drop is not the fact", func(t *testing.T) {
		// The launch override: the fact is an environment entry, so an execution
		// started with it set is a container the deployer never planned — and
		// the mount is the thing the fact is about. The run is the same one the
		// coding arm refuses, and it is refused here even though the environment
		// says the job is served: a door reading the fact alone would admit a
		// run holding the account's token with nobody to bound it.
		for _, name := range config.ModelSecretEnvs {
			env := append(append([]string{}, reached...), name+"=mounted")
			r := chainRun(t, job, slug, spec(), serving, config.AuthOAuth, env)
			err := r.checkProjectJobAccount(ctx, config.AuthOAuth)
			if err == nil {
				t.Fatalf("a job whose environment carries the fact while it still mounts %s was read as served: the run holds a credential no policy bounds, and the coding arm is the door that bounds it", name)
			}
			for _, want := range []string{job, config.ClaudeAccountSecret(personal), work} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal over the mounted %s (%q) lacks %q: it is the coding arm's refusal, not the served arm's", name, err, want)
				}
			}
		}

		// A provider's key is not one of them: the served class never took it
		// away (config.IsSharedJobServedModelEnv names the two Claude variables
		// alone, R6), so a served job keeps it and its presence is no evidence
		// against the fact.
		env := append(append([]string{}, reached...), config.ProviderKeyEnv("acme")+"=mounted")
		r := chainRun(t, job, slug, spec(), serving, config.AuthOAuth, env)
		if err := r.checkProjectJobAccount(ctx, config.AuthOAuth); err != nil {
			t.Fatalf("a served job carrying a provider's key was answered by the coding arm: %v — the served class leaves a provider's key mounted, so its presence says nothing about the fact", err)
		}
	})

	t.Run("the fact is the fact's true value and nothing else", func(t *testing.T) {
		for _, entry := range []string{
			config.CredentialsServedEnv + "=",
			config.CredentialsServedEnv + "=true",
			config.CredentialsServedEnv + "=yes",
		} {
			r := chainRun(t, job, slug, spec(), serving, config.AuthOAuth, []string{entry, credentialsServiceURLEnv + "=https://creds.example"})
			if err := r.checkProjectJobAccount(ctx, config.AuthOAuth); err == nil {
				t.Errorf("a job whose entry is %q was read as served: the fact's true value is %q alone, and an absent or other entry is not it", entry, config.ServedValue)
			}
		}
	})

	// The review job's two halves. The run is placed by jobIdentity and the
	// class it is placed as is the review one, so what the door answers is the
	// review arm's; the coding half sets the triple by hand, because a coding
	// run in a review job is refused at placement (jobIdentity's own check) and
	// the ordering this case is about is the door's.
	reviewJob := credsvc.ReviewJobName("standard", "github", config.ReviewVariantAccount(personal))

	t.Run("a coding run of a served profile's review job is still refused", func(t *testing.T) {
		r := chainRun(t, job, slug, spec(), serving, config.AuthOAuth, reached)
		r.job = jobTriple{Profile: "standard", Provider: "github", Account: config.ReviewVariantAccount(personal) + config.ReviewVariantSuffix, Class: classReview}
		err := r.checkProjectJobAccount(ctx, config.AuthOAuth)
		if err == nil {
			t.Fatal("a coding run was admitted to a review job because the job's credentials are served: the review arm is the door that keeps a coding run out of a container that holds no App key and pushes nothing")
		}
		if !strings.Contains(err.Error(), reviewJob) {
			t.Errorf("the refusal %q does not name the job it refused the run in", err)
		}
	})

	t.Run("a review run is admitted although it carries the fact and no address", func(t *testing.T) {
		p := policyOf(map[string]config.JobProfile{"standard": profileOf("github", personal)})
		r := identityRun(t, reviewJob, slug, spec(), p, true)
		r.review = true
		r.spec.Mode = recipe.ModeReview
		r.cfg = &config.Config{Agent: config.Agent{Auth: config.AuthOAuth}}
		r.d.Env = servedEnv
		if _, _, _, err := r.jobIdentity(ctx); err != nil {
			t.Fatalf("jobIdentity(%s) = %v", reviewJob, err)
		}
		if err := r.checkProjectJobAccount(ctx, config.AuthOAuth); err != nil {
			t.Fatalf("a review run of the review job was refused: %v — a review container holds no credential of a profile's, and its class is what says so", err)
		}
	})
}
