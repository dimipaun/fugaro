package infra

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// projectJobsOf is the project_jobs member of a tfvars document, nil when it
// carries none (the key is omitted for an empty set).
func projectJobsOf(t *testing.T, vars []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(vars, &doc); err != nil {
		t.Fatalf("the document is not a tfvars: %v\n%s", err, vars)
	}
	jobs, _ := doc[ProjectJobsKey].(map[string]any)
	return jobs
}

// jobsOfProfile is the members of a job set whose profile is name: the jobs one
// profile deployed, which is what a carry answers for.
func jobsOfProfile(t *testing.T, jobs map[string]any, name string) map[string]any {
	t.Helper()
	out := map[string]any{}
	for job, v := range jobs {
		m, _ := v.(map[string]any)
		if profile, _ := m["profile"].(string); profile == name {
			out[job] = v
		}
	}
	return out
}

// TestCarriedProjectJobsKeepsTheCodingJobAndItsReviewVariants: the carry of a
// profile the config can no longer compose keeps every job that profile
// deployed, whole, and nothing else.
//
// It is the installation of the profile `fugaro update` writes for itself (S5 of
// docs/shared-jobs-default): the profile is composed into a job from the one
// GitHub App ID the config records (projectJobGitHubAppID), and a config that
// loses it composes no job for any github profile — while the state holds the
// jobs it deployed, so the plan of every later run is their destruction, and
// RefuseProjectJobDeletes stops the whole update on it. The carry puts them back.
//
// Both keys are the point. ProjectReviewJobs derives a review-only variant per
// (profile, provider, account) of the CODING set, so a coding job the carry
// leaves out takes its own variants with it, and a document that holds the
// coding job alone is still a plan that destroys them. The second profile the
// fixture declares is what makes the carry's narrowness readable in the same
// document, and so is the installation's implicit review job, which the record
// holds under the other profile's name: a document that brings either back is a
// document that keeps a job no profile of the config declares.
//
// Mutations (each run and restored): carry project_jobs alone, and the fast
// profile's variants are missing below; carry every job the record holds rather
// than the named profile's, and the counts below see the extra jobs.
func TestCarriedProjectJobsKeepsTheCodingJobAndItsReviewVariants(t *testing.T) {
	// The installation that deployed the profile's jobs, with a second profile
	// beside it whose jobs the config below no longer composes either.
	deployed := projectReviewJobsDocument(t, implicitReviewLC()+implicitReviewProfileLCBlock)
	coding, review := projectJobsOf(t, deployed), reviewJobsOf(t, deployed)
	fastCoding, fastReview := jobsOfProfile(t, coding, "fast"), jobsOfProfile(t, review, "fast")
	if len(fastCoding) == 0 || len(fastReview) == 0 {
		t.Fatalf("the fixture's installation deploys %v and %v, want a coding job of the fast profile and at least one review variant of it",
			slices.Sorted(maps.Keys(coding)), slices.Sorted(maps.Keys(review)))
	}
	if len(review) <= len(fastReview) {
		t.Fatalf("the fixture's review jobs are %v, want a job of another profile beside the fast profile's variants: this test's narrowness assertion would hold vacuously",
			slices.Sorted(maps.Keys(review)))
	}

	// The transition: the config declares the bitbucket default alone, which is
	// the shape installSpec builds — the config's own profiles minus the one
	// the door could not compose a job for.
	fresh := projectReviewJobsDocument(t, implicitReviewLC()+
		"profiles:\n  standard: { provider: bitbucket, cpu: 2, memory: 8Gi, timeout_class: 6h }\n")
	if got := projectJobsOf(t, fresh); len(got) != 0 {
		t.Fatalf("the fixture's own document already carries %v, so this test would not be about the carry", slices.Sorted(maps.Keys(got)))
	}
	got, err := CarriedProjectJobs(fresh, deployed, []string{"fast"})
	if err != nil {
		t.Fatalf("the carry failed: %v", err)
	}

	// The document the carry must build: the one this config composes — the
	// implicit review job the installation has whatever its profiles are — with
	// the fast profile's coding job and its variants beside it, whole.
	wantCoding, wantReview := map[string]any{}, map[string]any{}
	for name, job := range projectJobsOf(t, fresh) {
		wantCoding[name] = job
	}
	for name, job := range fastCoding {
		wantCoding[name] = job
	}
	for name, job := range reviewJobsOf(t, fresh) {
		wantReview[name] = job
	}
	for name, job := range fastReview {
		wantReview[name] = job
	}
	if gotCoding := projectJobsOf(t, got); !reflect.DeepEqual(gotCoding, wantCoding) {
		t.Errorf("the carried document's project_jobs are %v, want %v: the fast profile's coding job as the record holds it, and nothing else",
			slices.Sorted(maps.Keys(gotCoding)), slices.Sorted(maps.Keys(wantCoding)))
	}
	if gotReview := reviewJobsOf(t, got); !reflect.DeepEqual(gotReview, wantReview) {
		t.Errorf("the carried document's project_review_jobs are %v, want %v: ProjectReviewJobs derives a profile's variants from the coding set, so a coding job the carry leaves out takes its own variants with it",
			slices.Sorted(maps.Keys(gotReview)), slices.Sorted(maps.Keys(wantReview)))
	}
}

// TestCarriedProjectJobsLeavesTheSpecsOwnJobAlone: the carry fills what the spec
// cannot compose and nothing else. A document that composes the job itself is
// the authority on it — that run is what catches the job up with this release's
// image and environment — so a record that holds an older one does not put it
// back.
//
// It is CarriedProjectReviewJobs' own rule, and it is what keeps a job the
// config can still describe following the config.
func TestCarriedProjectJobsLeavesTheSpecsOwnJobAlone(t *testing.T) {
	fresh := projectReviewJobsDocument(t, implicitReviewLC()+implicitReviewProfileLCBlock)
	coding := jobsOfProfile(t, projectJobsOf(t, fresh), "fast")
	name, ok := firstKey(coding)
	if !ok {
		t.Fatal("the fixture composes no fast coding job, so this test would not be about the spec's own answer")
	}
	older := projectJobEnvFact(t, fresh, name, providers.EnvGitHubAppID, "999999999")

	got, err := CarriedProjectJobs(fresh, older, []string{"fast"})
	if err != nil {
		t.Fatalf("the carry failed: %v", err)
	}
	job, _ := projectJobsOf(t, got)[name].(map[string]any)
	env, _ := job["env"].(map[string]any)
	if env[providers.EnvGitHubAppID] != "123456789" {
		t.Errorf("the carried document's %s job runs on %v, want the App ID the spec composed: a record that holds an older job does not overwrite the one the config describes",
			name, env[providers.EnvGitHubAppID])
	}
}

// projectJobEnvFact is the document vars with key inside the project_jobs job
// named name set to value: the fixture that distinguishes two documents by a
// fact inside the job they carry.
func projectJobEnvFact(t *testing.T, vars []byte, name, key, value string) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(vars, &doc); err != nil {
		t.Fatal(err)
	}
	job, _ := doc[ProjectJobsKey].(map[string]any)[name].(map[string]any)
	env, _ := job["env"].(map[string]any)
	if env == nil {
		t.Fatalf("the job %s carries no environment to change: %v", name, job)
	}
	env[key] = value
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// firstKey is the name a job set holds alone: the fixtures above carry one job
// per (profile, provider, account), and the test reads the one the profile's
// jobs all share a fact in.
func firstKey(jobs map[string]any) (string, bool) {
	names := slices.Sorted(maps.Keys(jobs))
	if len(names) == 0 {
		return "", false
	}
	return names[0], true
}

// carriedProfileLCBlock is the config of the installation this file's other
// half is about, one act later: `fugaro update` wrote profiles.standard for the
// project's GitHub repositories (R1, D2310), a repository on Bitbucket got
// profiles.standard-bitbucket beside it (R8), and the GitHub fact is gone from
// the repositories — no github_app_id anywhere — so the config composes no job
// for standard while the installation holds the one it deployed.
const carriedProfileLCBlock = `profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h }
  standard-bitbucket: { provider: bitbucket, cpu: 2, memory: 8Gi, timeout_class: 6h }
`

// TestCarriedProfileKeepsThePolicyAndLosesOnlyItsJob: a profile whose job is
// carried is still the policy's, and only its job is left out of the set.
//
// It is installSpec's carry read at its own level (internal/cli): the profile a
// config can no longer compose a job for is the one `fugaro update` wrote, and
// it is named `standard` — the name projectpolicy requires whenever a config
// carries a profiles block at all, because it is the profile a workflow that
// names none runs on. A composition that dropped the profile whole would move
// the refusal from the update to every run of the project (the policy's own
// sentence), which is not a trade a carry may make; what it drops is the JOB,
// and the deployed one is put back by CarriedProjectJobs.
//
// Mutation (run, restore): compose the set from the config without the carried
// profile — the shape installSpec had before this test — and the composition
// below is refused by CheckProfiles for the missing standard.
func TestCarriedProfileKeepsThePolicyAndLosesOnlyItsJob(t *testing.T) {
	// The GitHub fact gone from the repositories, as the act leaves the config:
	// the App ID every github job would run on is not there any more.
	lc := parseLC(t, strings.Replace(implicitReviewLC()+carriedProfileLCBlock, `, github_app_id: "123456789"`, "", 1))
	if _, err := ProjectJobs(lc, "fugaro-runs-proj-1234", false); err == nil {
		t.Fatal("the fixture composes a job for a github profile whose config records no App ID: this test would not be about the carry")
	}
	jobs, err := projectJobs(lc, "fugaro-runs-proj-1234", false, []string{projectpolicy.DefaultProfileName})
	if err != nil {
		t.Fatalf("the carried composition was refused: %v", err)
	}
	if got := slotsOfProfile(t, jobs, projectpolicy.DefaultProfileName); len(got) != 0 {
		t.Errorf("the carried profile composed %v, want nothing: its job is the installation's own, and the document carries the deployed one", got)
	}
	// The profile the config declares beside the carried one is a Bitbucket one,
	// which no shared job serves yet (I1): the set is the carried profile's
	// absence and nothing else. What the assertion above pins is the refusal
	// that is NOT here — a composition with the profile dropped whole is refused
	// by CheckProfiles for the missing standard, and the carry keeps it.
	review, err := projectReviewJobs(lc, "fugaro-runs-proj-1234", false, []string{projectpolicy.DefaultProfileName})
	if err != nil {
		t.Fatalf("the carried review composition was refused: %v", err)
	}
	for _, name := range slices.Sorted(maps.Keys(review)) {
		if job := review[name]; job.Profile == projectpolicy.DefaultProfileName {
			t.Errorf("the carried profile derived a review variant (%s): its variants are the deployed ones the carry keeps", name)
		}
	}
}

// slotsOfProfile is the job names of a job set whose profile is name, whatever
// provider and account they are for.
func slotsOfProfile(t *testing.T, jobs map[string]ProjectJob, name string) []string {
	t.Helper()
	var out []string
	for _, job := range slices.Sorted(maps.Keys(jobs)) {
		if jobs[job].Profile == name {
			out = append(out, job)
		}
	}
	return out
}
