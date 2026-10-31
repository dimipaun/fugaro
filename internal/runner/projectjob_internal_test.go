package runner

import (
	"context"
	"log/slog"
	"slices"
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
// Claude accounts it serves, which are the two axes the identity is computed
// from (the account axis is the profile's claude_accounts field,
// config.ProjectJobAccounts — a profile's secrets: can never carry a Claude
// token, so it never names an account).
func profileOf(provider string, accounts ...string) config.JobProfile {
	return config.JobProfile{Provider: provider, ClaudeAccounts: accounts}
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
//
// The two Claude cases are the account axis as the profile states it now
// (claude_accounts), which is what internal/infra deploys the names from: a
// profile listing the default account serves it on its own no-account job, and
// a profile listing a named account gets a job of that account's name. A
// policy still cannot carry the *mount* (projectpolicy.validProfiles refuses a
// profile mounting a Claude OAuth token in secrets:), which is why the axis is
// read from the field and never from the mounts.
func TestJobIdentityMatchesAComputedProjectJobName(t *testing.T) {
	for _, tc := range []struct {
		name     string
		profiles map[string]config.JobProfile
		triple   jobTriple
	}{
		{"an api-key profile runs as no account",
			map[string]config.JobProfile{"standard": profileOf("github")},
			jobTriple{Profile: "standard", Provider: "github", Account: ""}},
		{"a profile serving the default account serves it on its no-account job",
			map[string]config.JobProfile{"standard": profileOf("github", config.DefaultClaudeAccount)},
			jobTriple{Profile: "standard", Provider: "github", Account: ""}},
		{"a profile serving a named account has that account's job",
			map[string]config.JobProfile{"standard": profileOf("bitbucket", "acme-personal")},
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

// TestRunnerCandidatesIncludeAccountJobs: the set of names the run matches
// CLOUD_RUN_JOB against holds one entry per (profile, provider, account) the
// policy defines, computed by config.ProjectJobAccounts — the same function
// internal/infra deployed the jobs with and internal/cli picks the job to
// launch with. A profile serving accounts therefore contributes its
// no-account job and one per account; a profile serving none contributes the
// one job it always had; and the default account contributes no entry of its
// own, because it rides the no-account job (D5).
//
// Mutation (run, restore): compute one candidate per profile with an empty
// account (the pre-task code), which leaves every account job's name
// uncomputed: a run in one halts with cannotPlaceJob, blaming a stale module
// version for a job the deployer built.
func TestRunnerCandidatesIncludeAccountJobs(t *testing.T) {
	p := policyOf(map[string]config.JobProfile{
		"plain":    profileOf("github"),
		"standard": profileOf("github", "acme-personal", config.DefaultClaudeAccount),
		"other":    profileOf("bitbucket", "acme-work"),
	})
	want := []jobTriple{
		{Profile: "other", Provider: "bitbucket", Account: ""},
		{Profile: "other", Provider: "bitbucket", Account: "acme-work"},
		{Profile: "plain", Provider: "github", Account: ""},
		{Profile: "standard", Provider: "github", Account: ""},
		{Profile: "standard", Provider: "github", Account: "acme-personal"},
	}
	if got := projectJobCandidates(p); !slices.Equal(got, want) {
		t.Fatalf("projectJobCandidates = %+v, want %+v", got, want)
	}
	// Every candidate's name is one the run places, and none is named for the
	// default account: that name is not one any side computes.
	for _, c := range projectJobCandidates(p) {
		if c.Account == config.DefaultClaudeAccount {
			t.Errorf("the candidate %+v carries the default account: it rides the no-account job, whose account segment is empty", c)
		}
		job := gcp.ProjectJobName(c.Profile, c.Provider, c.Account)
		if got, project, err := matchJobName(job, p, true, identitySlug(t, "github", "other/repo"), identityWorkflow, ""); err != nil || !project || got != c {
			t.Errorf("matchJobName(%s) = %+v, project %v, %v; want the candidate %+v", job, got, project, err, c)
		}
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
// profile "standard", provider "bitbucket"), an account job whose account
// segment is a named account's (a parser cannot tell which side of the last
// hyphen the account begins, and the near miss is the same profile serving a
// different account), a profile long enough that derive truncates the readable
// part, and a per-repository job of a repository whose owner is "p" — stem()
// builds "fugaro-p-<name>-<wf>", which is ProjectJobName's own prefix for a
// profile named after the owner.
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
			why:     "an account job with a named account segment",
			job:     gcp.ProjectJobName("standard", "github", "acme-personal"),
			defines: map[string]config.JobProfile{"standard": profileOf("github", "acme-personal")},
			triple:  jobTriple{Profile: "standard", Provider: "github", Account: "acme-personal"},
			// The same profile serving a different account computes a
			// different name: the account segment is what tells one account's
			// job from another's, and the name a policy does not compute is
			// not one the run may adopt.
			not: map[string]config.JobProfile{"standard": profileOf("github", "acme-work")},
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

// TestJobIdentityTellsAPolicyWithNoProfilesFromNoPolicy: the refusal's detail
// and its fix are computed from the policy the set came from, and the three
// cases are three different things. A policy that is published and defines no
// profile is not "no policy published": the rules are deployed and the policy
// is there, it just names nothing, so the fix is to publish a policy with the
// profiles (fugaro project policy publish) rather than to deploy rules again.
//
// Mutation (run, restored): drop the `else` branch, so a policy with no
// profiles reports "This project has published no policy". The second case
// below fails.
func TestJobIdentityTellsAPolicyWithNoProfilesFromNoPolicy(t *testing.T) {
	slug := identitySlug(t, "github", "acme/app")
	job := "someone-elses-job"
	for _, tc := range []struct {
		name       string
		p          *projectpolicy.Policy
		found      bool
		wantDetail string
		wantFix    string
	}{
		{"no policy published", nil, false,
			"This project has published no policy", "fugaro project policy publish"},
		{"a policy with no profiles", &projectpolicy.Policy{Version: projectpolicy.Version}, true,
			"This project's published policy defines no job profiles at all", "fugaro project policy publish"},
		{"a policy with profiles", policyOf(map[string]config.JobProfile{"standard": profileOf("github")}), true,
			"This project's published policy defines the job profiles standard", "then `fugaro update` with the current Fugaro"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := identityRun(t, job, slug, nil, tc.p, tc.found)
			_, _, _, err := r.jobIdentity(context.Background())
			if err == nil {
				t.Fatalf("jobIdentity(%s) was accepted", job)
			}
			if !strings.Contains(err.Error(), tc.wantDetail) {
				t.Errorf("the refusal %q lacks %q", err, tc.wantDetail)
			}
			if !strings.Contains(err.Error(), tc.wantFix) {
				t.Errorf("the refusal %q does not point at %q", err, tc.wantFix)
			}
			if tc.found && strings.Contains(err.Error(), "published no policy") {
				t.Errorf("a published policy was reported as no policy at all: %v", err)
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
	r.d.Local = true
	profile, provider, account, err := r.jobIdentity(context.Background())
	if err != nil || profile != "" || provider != "" || account != "" {
		t.Fatalf("jobIdentity = %q/%q/%q, %v; want no identity and no error outside Cloud Run", profile, provider, account, err)
	}
	if _, project := r.projectJob(); project {
		t.Fatal("a run with no Cloud Run execution adopted a project job identity")
	}
}

// TestJobIdentityRefusesAnUnclaimedProcess (review C2): "no Cloud Run
// execution" is a local invocation only when the invocation said so
// (Deps.Local). A process no backend claims is in the cloud, and a job name
// is the one thing its identity could rest on, so with none it halts rather
// than taking the per-repository path with no job binding at all.
func TestJobIdentityRefusesAnUnclaimedProcess(t *testing.T) {
	r := identityRun(t, "", identitySlug(t, "github", "acme/app"), nil, nil, false)
	_, _, _, err := r.jobIdentity(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not a local invocation") {
		t.Fatalf("jobIdentity of an unclaimed process = %v, want a halt", err)
	}
	if _, project := r.projectJob(); project {
		t.Fatal("an unclaimed process adopted a project job identity")
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

// TestCheckJobProfileHoldsTheRunToTheJobItIsIn: the profile a run is *in* is
// the one its Cloud Run job was built for — the matched triple of
// jobIdentity — and the profile its workflow *selects* is the one the launch
// chose the job from. checkJobProfile compares them, so a workflow edited
// after the launch, or a job chosen by hand, stops the run instead of running
// the workflow's work on a job nobody selected for it. A run in a
// per-repository job carries no profile and nothing is compared (R2-9).
//
// Mutation (run, restored): drop the comparison, so checkJobProfile checks
// only that the policy defines the selected name. The second case below runs
// on the "big" job while its workflow selects standard.
func TestCheckJobProfileHoldsTheRunToTheJobItIsIn(t *testing.T) {
	policy := policyOf(map[string]config.JobProfile{"standard": profileOf("github"), "big": profileOf("github")})
	bigJob := gcp.ProjectJobName("big", "github", "")
	slug := identitySlug(t, "github", "acme/app")
	for _, tc := range []struct {
		name    string
		job     string
		wf      config.Workflow
		wantErr bool
	}{
		{"the job of the profile the workflow selects", bigJob, config.Workflow{JobProfile: "big"}, false},
		{"a job of another profile", bigJob, config.Workflow{JobProfile: "standard"}, true},
		{"a job of another profile, selected by omission", bigJob, config.Workflow{}, true},
		{"a per-repository job, whose workflow names no profile", gcp.JobName(slug, gcp.WorkflowVariant(identityWorkflow, "")), config.Workflow{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := identityRun(t, tc.job, slug, nil, policy, true)
			if _, _, _, err := r.jobIdentity(context.Background()); err != nil {
				t.Fatalf("jobIdentity(%s) = %v", tc.job, err)
			}
			err := r.checkJobProfile(context.Background(), identityWorkflow, tc.wf)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("a workflow selecting %q ran in the job %s", tc.wf.JobProfileOrDefault(), tc.job)
				}
				for _, want := range []string{tc.job, "big", tc.wf.JobProfileOrDefault()} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the refusal %q lacks %q", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("checkJobProfile = %v, want the run to go on", err)
			}
		})
	}
}

// accountsOf is a policy's accounts block as a value: each account with the
// repositories its token may be used by (projectpolicy.Account). Built rather
// than decoded for the reason policyOf is: what the node's own decoder accepts
// is task 1's tests, and what these tests are about is the pair (profile,
// accounts) the credential check reads.
func accountsOf(repos map[string][]string) map[string]projectpolicy.Account {
	out := make(map[string]projectpolicy.Account, len(repos))
	for name, list := range repos {
		out[name] = projectpolicy.Account{AllowRepos: list}
	}
	return out
}

// servingPolicy is a published policy whose standard profile serves the named
// Claude accounts, under an accounts block giving each of them its
// repositories. The profile and the block are built together on purpose: what
// a run of an account's job is bounded by is decided between the two
// (projectpolicy's validProfileAccounts), so a fixture that wrote them apart
// could state a pair no publish would.
func servingPolicy(accounts []string, repos map[string][]string) *projectpolicy.Policy {
	p := policyOf(map[string]config.JobProfile{"standard": profileOf("github", accounts...)})
	p.Accounts = accountsOf(repos)
	return p
}

// accountRun is a run whose job identity is real and whose own task.json is
// the caller's: the platform reports an execution of the job's computed name,
// the policy is filled in as the memoized read leaves it, and the identity is
// matched the way admissionCheck matches it. It fails the test rather than
// returning an error if the name is one the policy does not define — every
// name a test passes here is gcp.ProjectJobName of a triple the policy states.
func accountRun(t *testing.T, job, slug string, spec *task.Spec, p *projectpolicy.Policy) *run {
	t.Helper()
	r := identityRun(t, job, slug, spec, p, true)
	if _, _, _, err := r.jobIdentity(context.Background()); err != nil {
		t.Fatalf("jobIdentity(%s) = %v: the fixture's job name must be one the policy defines", job, err)
	}
	return r
}

// TestRunnerRefusesUncoveredAccount is the runner's own half of D3 and D4
// (plan task 3, design §4.4 rows 8 to 10): a run of an account's job holds
// that account's token for the whole of its run, and the published policy's
// accounts block is the only thing that says which repositories may hold it.
//
// It calls the check on values rather than through a whole run, and
// deliberately: the two rules it pins are unreachable through a decoded node.
// projectpolicy's validProfileAccounts refuses a profile that admits a
// repository its accounts do not cover, and refuses a profile naming an
// account the block does not define, so no published node can state either
// — the refusals here are the fail-closed backstop for a node that reaches a
// run by another road (a hand-written node, a policy written by a newer
// Fugaro, a decoder whose rules are later relaxed), and a backstop nobody can
// reach through the front door is one only a values-level test can hold.
//
// The launcher refuses every one of these before the claim and before the
// budget token (internal/cli's projectJobAccountFor, and its own test), and
// this is the second, independent door: a task.json is a bucket object anybody
// with write access can edit, so what a run holds must be re-decided from the
// policy the run read itself, not from the selection it was handed.
//
// Mutation (run, restored): drop the AdmitRepo arm (admit once the account's
// list has been read). The first case is admitted with a token its account's
// list does not cover. Mutation (run, restored): return nil for a run that is
// not an OAuth one once the job's account matches (skip the list for the runs
// that hold the token without asking for it). The default-account case is
// admitted — a non-OAuth run in a job carrying the default account's token,
// for a repository the token's own list does not cover — and it is the only
// case that fails, which is what makes it the one that pins the rule for the
// runs whose own credential never selects an account.
// Mutation (run, restored): drop the serves arm for an
// OAuth run (admit a job that holds no token at all). The last case is
// admitted as a credential-less OAuth run. Mutation (run, restored): compare
// nothing but the job's account against the profile (drop the
// selection/account comparison). The "another account" case passes the check
// it exists for, and a non-OAuth run in an account job does too.
func TestRunnerRefusesUncoveredAccount(t *testing.T) {
	slug := identitySlug(t, "github", "acme/app")
	const (
		personal = "acme-personal"
		work     = "acme-work"
	)
	// The fixture pair most cases share: the standard profile serves both
	// accounts, each covering the fixture's repository — the state a publish
	// leaves when both accounts' allow_data_to list acme/app.
	serving := servingPolicy([]string{personal, work}, map[string][]string{
		personal: {"github/acme/app"},
		work:     {"github/acme/app"},
	})
	// A note on the job's name: it is gcp.ProjectJobName of the triple, so it
	// carries the account axis the tests below disagree with the run's own
	// selection. gcp.ProjectJobName spells the default account as "" and so
	// does the policy's profile; the accounts block spells it "default".
	job := func(account string) string { return gcp.ProjectJobName("standard", "github", account) }
	for _, tc := range []struct {
		name    string
		policy  *projectpolicy.Policy
		account string // the account axis of the job's name
		selects string // spec.OAuthToken: the account the run's own task chose
		auth    string // the run's resolved agent.auth
		want    []string
	}{
		{
			name: "the account's list does not cover this repository",
			policy: servingPolicy([]string{personal}, map[string][]string{
				personal: {"github/acme/other"},
			}),
			account: personal, selects: personal, auth: "oauth",
			want: []string{job(personal), "claude-oauth-token-" + personal, "accounts." + personal + ".allow_repos",
				"github/acme/other", "github/acme/app", "claude_accounts." + personal + ".allow_data_to"},
		},
		{
			// The default account's token rides the no-account job (D5), so a
			// profile listing it hands that token to every run of that job —
			// including one that is not an OAuth run at all. The list is the
			// token's bound whatever the run's own credential is, which is why
			// the coverage rule is asked of the job's token account and not of
			// the run's selection (and why this case is the one a check that
			// skips non-OAuth runs leaves unrefused).
			name: "a run that is not an OAuth run in the no-account job of a profile whose default list does not cover this repository",
			policy: servingPolicy([]string{personal, config.DefaultClaudeAccount}, map[string][]string{
				personal:                    {"github/acme/app"},
				config.DefaultClaudeAccount: {"github/acme/other"},
			}),
			account: "", selects: "", auth: "api-key",
			want: []string{job(""), config.ClaudeAccountSecret(config.DefaultClaudeAccount),
				"accounts." + config.DefaultClaudeAccount + ".allow_repos", "github/acme/other", "github/acme/app",
				"project_secrets." + config.ClaudeAccountSecret(config.DefaultClaudeAccount) + ".allow_data_to"},
		},
		{
			name: "the policy defines no list for the job's account",
			policy: servingPolicy([]string{personal}, map[string][]string{
				work: {"github/acme/app"},
			}),
			account: personal, selects: personal, auth: "oauth",
			want: []string{job(personal), "claude-oauth-token-" + personal, "accounts." + personal, "bounded"},
		},
		{
			name:   "the run's own credential is another account",
			policy: serving, account: personal, selects: work, auth: "oauth",
			want: []string{job(personal), "claude-oauth-token-" + personal, `"acme-personal"`, `"acme-work"`, "oauth"},
		},
		{
			name: "the run's own credential is the default account",
			policy: servingPolicy([]string{personal}, map[string][]string{
				personal:  {"github/acme/app"},
				"default": {"github/acme/app"},
			}),
			account: personal, selects: "default", auth: "oauth",
			want: []string{job(personal), "claude-oauth-token-" + personal, `"default"`, "oauth"},
		},
		{
			name:   "a run that is not an OAuth run in an account job",
			policy: serving, account: personal, selects: "", auth: "api-key",
			want: []string{job(personal), "claude-oauth-token-" + personal, `"acme-personal"`, `"api-key"`},
		},
		{
			name: "an OAuth run in a job that holds no token at all",
			policy: servingPolicy([]string{personal}, map[string][]string{
				personal: {"github/acme/app"},
			}),
			account: "", selects: personal, auth: "oauth",
			want: []string{job(""), "no Claude token", `"acme-personal"`, `"default"`, "agent.BuildEnv"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := &task.Spec{Version: 1, RunID: "20261018-101010-abcd", Repo: "acme/app", Ref: "main",
				Workflow: identityWorkflow, OAuthToken: tc.selects, Task: "Add a feature"}
			r := accountRun(t, job(tc.account), slug, spec, tc.policy)
			err := r.checkProjectJobAccount(context.Background(), tc.auth)
			if err == nil {
				t.Fatalf("a run of %s with auth %q selecting %q was admitted", job(tc.account), tc.auth, tc.selects)
			}
			for _, want := range append(tc.want, "The run stops") {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal %q lacks %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "\n") {
				t.Errorf("the refusal is not one line: %q", err)
			}
		})
	}
}

// TestRunnerAdmitsTheAccountJobItsOwnConfigSelects is the other half of the
// check: every state a publish can leave must go on running. It is what keeps
// the refusals above from being a blanket refusal of shared jobs — a check
// that admitted nothing would pass every one of them.
//
// The third case is a decision the design makes rather than one this check
// takes (design §4.3, D5): a profile that lists "default" mounts that token
// into its no-account job, and a non-OAuth run of that job is a run the design
// calls legitimate — "such a profile's runs are OAuth or Vertex runs, never
// API-key ones" — so it is documented (SECURITY.md, fugaro doctor) and not
// refused here. It is pinned as a test so the behaviour is a decision and not
// an accident, and so a later change that DOES refuse it has to come back to
// this case and to the documents.
//
// Mutation (run, restored): refuse every job whose profile lists any account
// (the blanket refusal). The first case fails. Mutation (run, restored): drop
// the selection comparison's default-account arm (treat a "default" selection
// as a named account). The second case fails.
func TestRunnerAdmitsTheAccountJobItsOwnConfigSelects(t *testing.T) {
	slug := identitySlug(t, "github", "acme/app")
	serving := servingPolicy([]string{"acme-personal"}, map[string][]string{
		"acme-personal": {"github/acme/app"},
	})
	withDefault := servingPolicy([]string{"acme-personal", "default"}, map[string][]string{
		"acme-personal": {"github/acme/app"},
		"default":       {"github/acme/app"},
	})
	for _, tc := range []struct {
		name    string
		policy  *projectpolicy.Policy
		account string
		selects string
		auth    string
	}{
		{"an OAuth run in its own account's job", serving, "acme-personal", "acme-personal", "oauth"},
		{"an OAuth run whose own credential is the default account, in the profile's no-account job", withDefault, "", "", "oauth"},
		{"the same, with the default account spelled out", withDefault, "", "default", "oauth"},
		{"a run that is not an OAuth run in the no-account job of a profile that serves no token there", serving, "", "", "api-key"},
		{"a run that is not an OAuth run in the no-account job of a profile that lists the default account", withDefault, "", "", "api-key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := &task.Spec{Version: 1, RunID: "20261018-101010-abcd", Repo: "acme/app", Ref: "main",
				Workflow: identityWorkflow, OAuthToken: tc.selects, Task: "Add a feature"}
			job := gcp.ProjectJobName("standard", "github", tc.account)
			r := accountRun(t, job, slug, spec, tc.policy)
			if err := r.checkProjectJobAccount(context.Background(), tc.auth); err != nil {
				t.Fatalf("a run of %s with auth %q selecting %q was refused: %v", job, tc.auth, tc.selects, err)
			}
		})
	}
}

// A run in a per-repository job is not this check's business (the legacy path,
// plan task 3.3): there the account is part of the job's own name
// (gcp.WorkflowVariant), the job mounts exactly the selected account's token,
// and the per-repository rules hold unchanged. The check must leave such a run
// alone whatever the policy's accounts block says — a profile's jobs and a
// per-repository job are two different things, and the second must not start
// depending on the first.
//
// Mutation (run, restored): make the check look the profile up for a run with
// no project identity (match the slug's own job name against the policy's
// profiles instead of returning early). The run below is refused.
func TestRunnerLeavesAPerRepositoryJobToTheOldRules(t *testing.T) {
	slug := identitySlug(t, "github", "acme/app")
	serving := servingPolicy([]string{"acme-personal"}, map[string][]string{
		"acme-personal": {"github/acme/other"},
	})
	spec := &task.Spec{Version: 1, RunID: "20261018-101010-abcd", Repo: "acme/app", Ref: "main",
		Workflow: identityWorkflow, OAuthToken: "acme-personal", Task: "Add a feature"}
	r := identityRun(t, gcp.JobName(slug, gcp.WorkflowVariant(identityWorkflow, "acme-personal")), slug, spec, serving, true)
	if _, _, _, err := r.jobIdentity(context.Background()); err != nil {
		t.Fatalf("jobIdentity = %v: a named per-repository job must still match its own name", err)
	}
	if _, project := r.projectJob(); project {
		t.Fatal("the run adopted a project job identity from a per-repository job's name")
	}
	if err := r.checkProjectJobAccount(context.Background(), "oauth"); err != nil {
		t.Fatalf("checkProjectJobAccount = %v, want a per-repository job to be left alone", err)
	}
}
