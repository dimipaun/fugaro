package runner

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// The tests in this file are the job-identity decision's own (plan task 3.3):
// the matching, the legacy path and the halts. They call the decision on
// values — matchJobName, and jobIdentity with the memoized policy read
// pre-filled the way projectPolicy would have left it — because that is where
// the security property lives: which (profile, provider, account) a name is,
// and whether a name is a project job at all. The end-to-end half (the order
// before the token and the lease, the computed clone URL, the profile's
// admission through a real run) is in projectjob_test.go, which runs whole
// runs against the fixture.

const identityWorkflow = "app"

// identityRun is a run whose only business is its job identity: the execution
// the platform reported (Deps.Execution, internal/cli's wiring of
// backend.ExecutionFromEnv), the slug the run is bound to (FUGARO_RUN) and the
// task.json it read. The project's policy is filled in as the memoized read
// would leave it (policyRead), so these tests need no database: the read itself
// is task 1.3's own test, and the identity decision needs nothing but the
// decoded node.
func identityRun(t *testing.T, job, slug string, spec *task.Spec, p *projectpolicy.Policy, found bool) *run {
	t.Helper()
	b := memblob.OpenBucket(nil)
	t.Cleanup(func() { _ = b.Close() })
	if spec == nil {
		spec = &task.Spec{Version: 1, RunID: "20260926-221530-abcd", Repo: "acme/app", Ref: "main",
			Workflow: identityWorkflow, Task: "Add a feature"}
	}
	r := &run{
		d:    Deps{Store: runstore.Open(b, slug, spec.RunID), Log: slog.New(slog.DiscardHandler)},
		spec: spec,
	}
	if job != "" {
		r.d.Execution = identityExecution(job)
	}
	r.projPolicy = policyRead{done: true, policy: p, found: found}
	return r
}

// identityExecution is a Cloud Run execution name of job, the shape
// backend.ExecutionFromEnv returns: the platform's own output, which is the
// only thing the run reads its job's name from.
func identityExecution(job string) string {
	return "projects/aurora-fp/locations/europe-west1/jobs/" + job + "/executions/" + job + "-aaaaa"
}

// identitySlug is task.Slug for a fixture repository, failing the test rather
// than skipping: every user of it names a repository the fixture's providers
// can spell.
func identitySlug(t *testing.T, provider, repo string) string {
	t.Helper()
	slug, err := task.Slug(provider, repo)
	if err != nil {
		t.Fatal(err)
	}
	return slug
}

// profileOf is one profile of a policy fixture: the provider it pins and the
// secrets it mounts, which are the two axes the identity is computed from.
func profileOf(provider string, secrets ...string) config.JobProfile {
	return config.JobProfile{Provider: provider, Secrets: secrets}
}

// policyOf is a published policy carrying these profiles, by name. It is built
// as a value rather than decoded: what the node's decoder accepts is task
// 1.1's own test, and these tests are about which names a policy defines.
func policyOf(profiles map[string]config.JobProfile) *projectpolicy.Policy {
	out := make(map[string]projectpolicy.Profile, len(profiles))
	for name, pr := range profiles {
		out[name] = projectpolicy.Profile{JobProfile: pr}
	}
	return &projectpolicy.Policy{Version: projectpolicy.Version, Profiles: out}
}

// TestJobIdentityMatchesAComputedProjectJobName is the round trip the earlier
// design draft tried to get by parsing a name: the run computes
// gcp.ProjectJobName for every triple its policy defines and adopts the triple
// whose name CLOUD_RUN_JOB is, so the identity is the one the name was
// *written* from, all three axes of it (R2-11).
//
// Mutation (run, restored): make jobIdentity return the profile and provider
// and drop the account, or have projectJobCandidates drop the account segment.
// The named-account case fails alone, which is the axis the job's own service
// account and its Claude token are separated by (design §7).
func TestJobIdentityMatchesAComputedProjectJobName(t *testing.T) {
	for _, tc := range []struct {
		name     string
		profiles map[string]config.JobProfile
		triple   jobTriple
	}{
		{"an api-key profile runs as no account",
			map[string]config.JobProfile{"standard": profileOf("github")},
			jobTriple{Profile: "standard", Provider: "github", Account: ""}},
		{"a profile mounting the reserved token runs as the default account",
			map[string]config.JobProfile{"standard": profileOf("github", "claude-oauth-token", "github-app-key")},
			jobTriple{Profile: "standard", Provider: "github", Account: config.DefaultClaudeAccount}},
		{"a profile mounting a named account's token runs as that account",
			map[string]config.JobProfile{"standard": profileOf("bitbucket", "claude-oauth-token-acme-personal")},
			jobTriple{Profile: "standard", Provider: "bitbucket", Account: "acme-personal"}},
		{"a provider is part of the identity",
			map[string]config.JobProfile{"standard": profileOf("bitbucket")},
			jobTriple{Profile: "standard", Provider: "bitbucket", Account: ""}},
		{"a second profile is a second job",
			map[string]config.JobProfile{"standard": profileOf("github"), "fast": profileOf("github")},
			jobTriple{Profile: "fast", Provider: "github", Account: ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := policyOf(tc.profiles)
			job := gcp.ProjectJobName(tc.triple.Profile, tc.triple.Provider, tc.triple.Account)
			// The run's own slug is another repository's here on purpose: a
			// project job's identity does not come from the slug, and the
			// binding of the two is a later step of the admission check.
			r := identityRun(t, job, identitySlug(t, "github", "other/repo"), nil, p, true)
			profile, provider, account, err := r.jobIdentity(context.Background())
			if err != nil {
				t.Fatalf("jobIdentity(%s) = %v", job, err)
			}
			got := jobTriple{Profile: profile, Provider: provider, Account: account}
			if got != tc.triple {
				t.Fatalf("jobIdentity = %+v, want %+v (the triple %s was computed from)", got, tc.triple, job)
			}
			if r.job != tc.triple {
				t.Fatalf("the run adopted %+v, want %+v: the matched triple is the run's identity", r.job, tc.triple)
			}
		})
	}
}

// TestJobIdentityIsNotAParser is the R2-5 regression test: a name is matched
// against the names the policy *computes*, never read as a shape. Each case
// gives a name a shape test would misread and asserts both directions — under
// a policy that defines the triple the name was computed from it matches, and
// under one that does not it is not a project job at all. The second direction
// is the one that matters: a shape test would adopt an identity from the bytes.
//
// The four names are the plan's, and each is a way the name's readable part
// lies about the triple: a hyphenated profile ("standard-bitbucket" reads as
// profile "standard", provider "bitbucket"), an api-key job with no account
// segment (a parser cannot tell "no account" from a truncated one), a profile
// long enough that derive truncates the readable part, and a per-repository job
// of a repository whose owner is "p" — stem() builds "fugaro-p-<name>-<wf>",
// which is ProjectJobName's own prefix for a profile named after the owner.
//
// Mutation (run, restored): replace the loop over projectJobCandidates with a
// parse of the stem (split on "-", take the second segment as the profile and
// the third as the provider). The legit cases below fail, the repository owner
// named "p" is adopted as a project job, and every case's second direction
// passes for the wrong reason.
func TestJobIdentityIsNotAParser(t *testing.T) {
	// 40 characters: ValidProfileName's longest, so derive's readable part is
	// what gets cut, and the two profiles below share all of the bytes a
	// truncated name would keep.
	long := strings.Repeat("standard", 5)
	longer := long[:len(long)-1] + "x"
	legacySlug := identitySlug(t, "github", "p/app")

	for _, tc := range []struct {
		why string
		job string
		// defines is the policy under which the name is a project job, and
		// triple the identity it must then have. not is a policy (or none)
		// under which the name must not be one.
		defines map[string]config.JobProfile
		triple  jobTriple
		not     map[string]config.JobProfile
	}{
		{
			why:     "a hyphenated profile",
			job:     gcp.ProjectJobName("standard-bitbucket", "bitbucket", ""),
			defines: map[string]config.JobProfile{"standard-bitbucket": profileOf("bitbucket")},
			triple:  jobTriple{Profile: "standard-bitbucket", Provider: "bitbucket", Account: ""},
			not:     map[string]config.JobProfile{"standard": profileOf("bitbucket")},
		},
		{
			why:     "an api-key job with an empty account segment",
			job:     gcp.ProjectJobName("standard", "github", ""),
			defines: map[string]config.JobProfile{"standard": profileOf("github")},
			triple:  jobTriple{Profile: "standard", Provider: "github", Account: ""},
			not:     map[string]config.JobProfile{"standard": profileOf("github", "claude-oauth-token")},
		},
		{
			why:     "a name truncated at Cloud Run's length limit",
			job:     gcp.ProjectJobName(long, "github", ""),
			defines: map[string]config.JobProfile{long: profileOf("github")},
			triple:  jobTriple{Profile: long, Provider: "github", Account: ""},
			not:     map[string]config.JobProfile{longer: profileOf("github")},
		},
		{
			why: "a per-repository job of a repository whose owner is p",
			job: gcp.JobName(legacySlug, gcp.WorkflowVariant(identityWorkflow, "")),
			// A project job of a profile named "p" would be this name's own
			// shape, so the policy that defines one is the near miss: the
			// legacy name must still take the phase-1 path, and it must not
			// be adopted as that profile's job.
			not: map[string]config.JobProfile{"p": profileOf("github")},
		},
	} {
		t.Run(tc.why, func(t *testing.T) {
			if tc.defines != nil {
				got, project, err := matchJobName(tc.job, policyOf(tc.defines), true, legacySlug, identityWorkflow, "")
				if err != nil || !project || got != tc.triple {
					t.Fatalf("matchJobName(%s) = %+v, project %v, %v; want %+v as a project job", tc.job, got, project, err, tc.triple)
				}
			}
			// The same bytes, under a policy that does not compute them.
			p, found := policyOf(tc.not), tc.not != nil
			got, project, err := matchJobName(tc.job, p, found, legacySlug, identityWorkflow, "")
			legacy := tc.defines == nil
			switch {
			case legacy && (err != nil || project):
				t.Fatalf("matchJobName(%s) = %+v, project %v, %v; want the per-repository path, unchanged", tc.job, got, project, err)
			case !legacy && (err == nil || project):
				t.Fatalf("matchJobName(%s) = %+v, project %v, %v; want a halt: this policy computes no such name", tc.job, got, project, err)
			case !legacy && !strings.Contains(err.Error(), tc.job):
				t.Errorf("the refusal does not name the job: %v", err)
			}
		})
	}
}

// TestJobIdentityRefusesAnUnknownProjectJob: a job whose name a policy no
// longer computes — one created by an older module version, or by a newer one
// whose policy this Fugaro cannot read — is a job the run cannot place, and it
// must halt rather than run with no admission at all. The refusal names the job,
// the slug the run is bound to, the workflow, and the profiles the policy does
// define, so a reader can tell which of the two is stale.
//
// Mutation (run, restored): return an empty triple and no error for a name that
// matches no candidate (the phase-1 path), which is the fail-open this test
// exists for — the run would go on with no profile admission and no provider it
// can trust.
func TestJobIdentityRefusesAnUnknownProjectJob(t *testing.T) {
	p := policyOf(map[string]config.JobProfile{"standard": profileOf("github")})
	slug := identitySlug(t, "github", "acme/app")
	job := gcp.ProjectJobName("retired-profile", "github", "")
	r := identityRun(t, job, slug, nil, p, true)
	_, _, _, err := r.jobIdentity(context.Background())
	if err == nil {
		t.Fatalf("jobIdentity(%s) was accepted: a job the policy does not define must halt", job)
	}
	for _, want := range []string{job, slug, identityWorkflow, "standard", "profiles"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q lacks %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "\n") {
		t.Errorf("the refusal is not one line: %q", err)
	}
}

// TestLegacyJobNameTakesThePhaseOnePath is the phase-3 regression test
// (finding 5): a per-repository job keeps working, with a policy published,
// whether or not that policy defines a profile — the run's own job is
// recomputed from its slug and the workflow in its task.json and matched, and
// nothing about a profile is adopted.
//
// Mutation (run, restored): make a non-matching name halt whenever a policy is
// published. Every per-repository job on every installation that has published
// a policy stops, including the ones this task must not break.
func TestLegacyJobNameTakesThePhaseOnePath(t *testing.T) {
	slug := identitySlug(t, "github", "acme/app")
	job := gcp.JobName(slug, gcp.WorkflowVariant(identityWorkflow, ""))
	for _, tc := range []struct {
		name  string
		p     *projectpolicy.Policy
		found bool
	}{
		{"no policy published", nil, false},
		{"a policy with no profiles", &projectpolicy.Policy{Version: projectpolicy.Version}, true},
		{"a policy with profiles", policyOf(map[string]config.JobProfile{"standard": profileOf("github")}), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := identityRun(t, job, slug, nil, tc.p, tc.found)
			profile, provider, account, err := r.jobIdentity(context.Background())
			if err != nil || profile != "" || provider != "" || account != "" {
				t.Fatalf("jobIdentity = %q/%q/%q, %v; want the per-repository path with no identity", profile, provider, account, err)
			}
			if _, project := r.projectJob(); project {
				t.Fatal("the run adopted a project job identity from a per-repository job's name")
			}
		})
	}
}

// TestLegacyJobNameOfANamedOAuthRunMatches is B2's regression test, and the
// analogue of the bug internal/cli's cloud.go documents: the deployed job of a
// named OAuth run is JobName(slug, WorkflowVariant(wf, token)), so a run that
// recomputed it with the plain workflow would fail to match its own job and
// halt. task.json carries both the workflow and the token, and both go into the
// recomputation.
//
// Mutation (run, restored): pass spec.Workflow alone to gcp.JobName. This case
// fails, and so does every named-account run of every per-repository job.
func TestLegacyJobNameOfANamedOAuthRunMatches(t *testing.T) {
	slug := identitySlug(t, "bitbucket", "acme/app")
	spec := &task.Spec{Version: 1, RunID: "20260926-221530-abcd", Repo: "acme/app", Ref: "main",
		Workflow: identityWorkflow, OAuthToken: "acme-personal", Task: "Add a feature"}
	job := gcp.JobName(slug, gcp.WorkflowVariant(identityWorkflow, "acme-personal"))
	r := identityRun(t, job, slug, spec, policyOf(map[string]config.JobProfile{"standard": profileOf("bitbucket")}), true)
	if profile, provider, account, err := r.jobIdentity(context.Background()); err != nil || profile != "" || provider != "" || account != "" {
		t.Fatalf("jobIdentity = %q/%q/%q, %v; want the per-repository path: a named OAuth run must match its own job", profile, provider, account, err)
	}
}

// TestLegacyJobNameOfAnEmptyWorkflowIsRefused: a per-repository job's name is
// computed from a workflow, and WorkflowVariant panics on one outside
// config.WorkflowNameRE — an empty string included — so a task.json naming no
// workflow is refused before the variant is computed. The launcher always
// resolves a workflow (internal/cli's resolveWorkflow), so this is a
// hand-written or edited task, and the run stops rather than guessing.
func TestLegacyJobNameOfAnEmptyWorkflowIsRefused(t *testing.T) {
	slug := identitySlug(t, "github", "acme/app")
	spec := &task.Spec{Version: 1, RunID: "20260926-221530-abcd", Repo: "acme/app", Ref: "main", Task: "Add a feature"}
	r := identityRun(t, "fugaro-acme-app-app-0123456789abcdef", slug, spec, nil, false)
	_, _, _, err := r.jobIdentity(context.Background())
	if err == nil {
		t.Fatal("a run whose task.json names no workflow was placed in a job")
	}
	for _, want := range []string{"names no workflow", slug} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q lacks %q", err, want)
		}
	}
}

// TestLegacyJobNameOfAnUnshapeableWorkflowIsRefused: task.Spec.Workflow is
// never shape-checked (config.WorkflowNameRE constrains fugaro.yaml's keys, not
// what reaches the spec), and gcp.WorkflowVariant panics on a name outside it.
// The run refuses instead of crashing — internal/cli's jobWorkflow is the
// shipped precedent — and the recover() is what makes that true for every
// value, not only the two spelled here.
func TestLegacyJobNameOfAnUnshapeableWorkflowIsRefused(t *testing.T) {
	slug := identitySlug(t, "github", "acme/app")
	for _, workflow := range []string{"Not A Workflow", "app/../other", "APP"} {
		spec := &task.Spec{Version: 1, RunID: "20260926-221530-abcd", Repo: "acme/app", Ref: "main",
			Workflow: workflow, Task: "Add a feature"}
		r := identityRun(t, "fugaro-acme-app-app-0123456789abcdef", slug, spec, nil, false)
		_, _, _, err := r.jobIdentity(context.Background())
		if err == nil {
			t.Fatalf("the workflow %q was accepted: it is not a name a job could be built from", workflow)
		}
		if !strings.Contains(err.Error(), "workflow") {
			t.Errorf("the refusal of %q does not say what is wrong: %v", workflow, err)
		}
	}
}

// TestJobIdentityRefusesANameItCannotPlace: a non-empty CLOUD_RUN_JOB that is
// neither a computed project job nor this run's own per-repository job. The
// name may be another installation's or one created by hand; either way the run
// adopts no identity from it, and the refusal names the job, the slug and the
// workflow.
//
// Mutation (run, restored): treat a name that matches nothing as a legacy job.
// The run goes on and clones from a job it cannot place.
func TestJobIdentityRefusesANameItCannotPlace(t *testing.T) {
	slug := identitySlug(t, "github", "acme/app")
	for _, job := range []string{
		"someone-elses-job",
		gcp.JobName(identitySlug(t, "github", "other/repo"), gcp.WorkflowVariant(identityWorkflow, "")),
		gcp.JobName(slug, gcp.WorkflowVariant("other-workflow", "")),
	} {
		t.Run(job, func(t *testing.T) {
			r := identityRun(t, job, slug, nil, policyOf(map[string]config.JobProfile{"standard": profileOf("github")}), true)
			_, _, _, err := r.jobIdentity(context.Background())
			if err == nil {
				t.Fatalf("jobIdentity(%s) was accepted", job)
			}
			for _, want := range []string{job, slug, identityWorkflow} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal %q lacks %q", err, want)
				}
			}
		})
	}
}

// TestJobIdentityIsSkippedOutsideCloudRun (R2-9): a run with no Cloud Run
// execution has no job identity to check, so the step does not run at all —
// which is why a docker run, a local invocation and `fugaro exec --provider
// fake` keep the path they have always had, and why the fake provider needs no
// exemption from any of this. A task.json naming no workflow is the sharpest
// form of it: it is refused on Cloud Run (above) and must be ignored here.
func TestJobIdentityIsSkippedOutsideCloudRun(t *testing.T) {
	spec := &task.Spec{Version: 1, RunID: "20260926-221530-abcd", Repo: "acme/app", Ref: "main", Task: "Add a feature"}
	r := identityRun(t, "", identitySlug(t, "github", "acme/app"), spec, nil, false)
	profile, provider, account, err := r.jobIdentity(context.Background())
	if err != nil || profile != "" || provider != "" || account != "" {
		t.Fatalf("jobIdentity = %q/%q/%q, %v; want no identity and no error outside Cloud Run", profile, provider, account, err)
	}
	if _, project := r.projectJob(); project {
		t.Fatal("a run with no Cloud Run execution adopted a project job identity")
	}
}

// TestJobIdentityRefusesAnUnreadableExecutionName: Deps.Execution is the
// platform's own report, so a value no codec of this build parses is not a job
// name at all. The run halts rather than treating it as "no Cloud Run
// execution", which would skip the identity it is supposed to bind to.
func TestJobIdentityRefusesAnUnreadableExecutionName(t *testing.T) {
	r := identityRun(t, "", identitySlug(t, "github", "acme/app"), nil, nil, false)
	r.d.Execution = "fugaro-acme-app-app-aaaaa" // a bare name, not a resource name
	if _, _, _, err := r.jobIdentity(context.Background()); err == nil {
		t.Fatal("an execution name no backend parses was accepted")
	}
}
