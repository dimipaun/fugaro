package infra

import (
	"bytes"
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/credsvc"
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
// profile's variants are missing below. Carry every job the record holds rather
// than the named profiles' — drop the filter — and the standard-bitbucket coding
// job, which the record holds and this document does not declare, comes back
// with it, so this test pins the filter too (as
// TestCarriedProjectJobsTakesTheNamedProfilesJobsAlone does over a record of the
// account axis).
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

	// The transition: the local config declares no coding profile, which is the
	// shape installSpec builds — the config's own profiles minus the one the
	// door could not compose a job for, whose job the installation's record
	// holds instead of the document. So the document this config composes is the
	// installation's implicit review job and nothing else, and the profile the
	// carry is asked for is one no profile of it declares.
	fresh := projectReviewJobsDocument(t, implicitReviewLC())
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

// keyCredentialCarryDeclaration is the one line keyCredentialAccountAxisLC
// (projectjobs_test.go) declares its key credential on. This file's fixtures
// take it away to write the record the release behind left: the same config,
// with acme-key not yet a key credential, which is a name the deployer composed
// a job for.
const keyCredentialCarryDeclaration = "credentials:\n  - { name: acme-key, type: openrouter }\n"

// TestCarriedProjectJobAccountsKeepsTheKeyCredentialsJob: the installation that
// deployed a job for a name its config NOW declares a key credential keeps that
// job, whole, when the carry runs — and the carry changes nothing else.
//
// MC22/D1670 taught the deployer to leave such a name out of the job set it
// composes (config.ClaudeAccountTokenNames, ProjectKeyAccountJobs naming what
// that drops), and the installation the release behind left holds the job all
// the same: a profile listing a key-typed name is written by hand — no command
// ever wrote one, and `fugaro accounts add` refuses to — and that name's
// project-wide container is what the pre-fix composer needed to build the job
// (projectAccountMount). A document rewritten without it is the plan of its
// destruction, and RefuseProjectJobDeletes stops the whole update on that plan
// with a remedy nobody can carry out: putting the name back into the profile's
// claude_accounts is what the filter drops the job for. So the job is carried,
// as the profile carries above are.
//
// The record is composed from the config with the declaration taken away, and
// it is the record the pre-fix deployer wrote byte for byte: nothing about a
// job of that axis depends on the credential's type — the difference the filter
// makes is whether the job exists at all.
//
// Mutations (each run and restored): return vars unchanged, and both the coding
// job and its variant are missing below; carry the record's jobs rather than
// the named triples (an empty name set, the call below), and the document comes
// back with the job of a triple nobody named.
func TestCarriedProjectJobAccountsKeepsTheKeyCredentialsJob(t *testing.T) {
	withKey := implicitReviewBaseLC() + keyCredentialAccountAxisLC
	before := strings.Replace(withKey, keyCredentialCarryDeclaration, "", 1)
	if before == withKey {
		t.Fatalf("the fixture no longer declares the key credential on %q, so this test would not be about the carry", keyCredentialCarryDeclaration)
	}
	deployed := projectReviewJobsDocument(t, before)
	fresh := projectReviewJobsDocument(t, withKey)
	key := gcp.ProjectJobName("standard", "github", "acme-key")
	keyReview := credsvc.ReviewJobName("standard", "github", "acme-key")
	deployedCoding, deployedReview := projectJobsOf(t, deployed), reviewJobsOf(t, deployed)
	if _, ok := deployedCoding[key]; !ok {
		t.Fatalf("the fixture's record holds %v, want the job the deployer composed for acme-key", slices.Sorted(maps.Keys(deployedCoding)))
	}
	if _, ok := deployedReview[keyReview]; !ok {
		t.Fatalf("the fixture's record holds %v, want acme-key's review variant %s", slices.Sorted(maps.Keys(deployedReview)), keyReview)
	}
	if _, ok := projectJobsOf(t, fresh)[key]; ok {
		t.Fatalf("the fixture's own document already composes %s, so this test would not be about the carry", key)
	}
	if _, ok := reviewJobsOf(t, fresh)[keyReview]; ok {
		t.Fatalf("the fixture's own document already composes %s, so this test would not be about the carry", keyReview)
	}

	accounts := ProjectKeyAccountJobs(parseLC(t, withKey), nil)
	want := []ProjectJobAccount{{Profile: "standard", Provider: "github", Account: "acme-key"}}
	if !reflect.DeepEqual(accounts, want) {
		t.Fatalf("ProjectKeyAccountJobs = %+v, want %+v: the filter's own answer, the triples the spec composes no job for", accounts, want)
	}
	// Nothing named is nothing carried: the carry is the caller's list and never
	// the record's contents.
	if got, err := CarriedProjectJobAccounts(fresh, deployed, nil); err != nil || !bytes.Equal(got, fresh) {
		t.Fatalf("a carry of no triples changed the document (err %v): the record holds a job no caller named, and it stays where the config and the state have it\n%s", err, got)
	}

	got, err := CarriedProjectJobAccounts(fresh, deployed, accounts)
	if err != nil {
		t.Fatalf("the carry failed: %v", err)
	}
	wantCoding, wantReview := map[string]any{}, map[string]any{}
	for name, job := range projectJobsOf(t, fresh) {
		wantCoding[name] = job
	}
	wantCoding[key] = deployedCoding[key]
	for name, job := range reviewJobsOf(t, fresh) {
		wantReview[name] = job
	}
	wantReview[keyReview] = deployedReview[keyReview]
	if gotCoding := projectJobsOf(t, got); !reflect.DeepEqual(gotCoding, wantCoding) {
		t.Errorf("the carried document's project_jobs are %v, want %v: acme-key's job as the record holds it, and nothing else",
			slices.Sorted(maps.Keys(gotCoding)), slices.Sorted(maps.Keys(wantCoding)))
	}
	if gotReview := reviewJobsOf(t, got); !reflect.DeepEqual(gotReview, wantReview) {
		t.Errorf("the carried document's project_review_jobs are %v, want %v: ProjectReviewJobs derives a profile's variants from the coding set, so a coding job the carry leaves out takes its own variant with it",
			slices.Sorted(maps.Keys(gotReview)), slices.Sorted(maps.Keys(wantReview)))
	}
	// The job is the record's own, and the plan that follows is a no-op because
	// of it: an entry this run recomposed — a newer image, an environment this
	// release's composer writes — would be a change the plan proposes.
	job, carried := projectJobsOf(t, got)[key].(map[string]any)
	if !carried || !reflect.DeepEqual(job, deployedCoding[key]) {
		t.Errorf("the carried document's %s is\n%v\nwant the record's own entry\n%v: a job is carried as the record holds it, so the plan that follows proposes no change to it", key, job, deployedCoding[key])
	}
}

// TestCarriedProjectJobAccountsLeavesTheSpecsOwnJobAlone: the carry fills what
// the spec cannot compose and nothing else, on an axis whose other points the
// spec composes itself.
//
// It is the shared walk's rule (carriedProjectJobs), and the reason it matters
// on this axis: the caller names the triples the filter dropped
// (ProjectKeyAccountJobs), and a document that composes one of them all the same
// is the authority on it — that run is what catches the job up with this
// release's image and environment.
func TestCarriedProjectJobAccountsLeavesTheSpecsOwnJobAlone(t *testing.T) {
	fresh := projectReviewJobsDocument(t, implicitReviewBaseLC()+keyCredentialAccountAxisLC)
	name := gcp.ProjectJobName("standard", "github", "acme-personal")
	if _, ok := projectJobsOf(t, fresh)[name]; !ok {
		t.Fatalf("the fixture composes no %s, so this test would not be about the spec's own answer", name)
	}
	older := projectJobEnvFact(t, fresh, name, providers.EnvGitHubAppID, "999999999")

	got, err := CarriedProjectJobAccounts(fresh, older, []ProjectJobAccount{{Profile: "standard", Provider: "github", Account: "acme-personal"}})
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

// slowProfileLCBlock is implicitReviewProfileLCBlock with a second github
// profile beside fast: two profiles the config can no longer compose a job for,
// so that the carry below has one to take and one to leave behind.
//
// It is the shape of the installation S5's carry is about, one act further on
// than the fixture above: `fugaro update` wrote profiles.standard, an operator
// added fast and slow on the project's GitHub repositories, and the one App ID
// the config composed every github job from is gone — while the state holds
// both profiles' jobs, and installSpec names the profiles whose job the
// installation holds and its own config can no longer compose.
const slowProfileLCBlock = `  slow: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h }
`

// TestCarriedProjectJobsTakesTheNamedProfilesJobsAlone: the carry of one
// profile leaves the record's other profiles where they are. A record holds
// every job the installation deployed, and the caller names only the profiles
// whose job this run's config can no longer compose (internal/cli's
// heldDefaultProfiles): taking the record whole would put back the job of a
// profile the config still describes — or that a person removed on purpose —
// and the plan of the run after that is that job's destruction over again.
//
// Mutations (each run and restored): drop the per-profile filter (tfvars.go's
// `if profile, _ := m["profile"].(string); !want[profile] { continue }`) and
// this test fails: the carried document holds the slow profile's coding job and
// its variants, which no profile of the config declares.
func TestCarriedProjectJobsTakesTheNamedProfilesJobsAlone(t *testing.T) {
	// The installation that deployed both github profiles' jobs, beside the
	// bitbucket default it can compose no job for.
	deployed := projectReviewJobsDocument(t, implicitReviewLC()+implicitReviewProfileLCBlock+slowProfileLCBlock)
	coding, review := projectJobsOf(t, deployed), reviewJobsOf(t, deployed)
	fastCoding, fastReview := jobsOfProfile(t, coding, "fast"), jobsOfProfile(t, review, "fast")
	slowCoding, slowReview := jobsOfProfile(t, coding, "slow"), jobsOfProfile(t, review, "slow")
	if len(fastCoding) == 0 || len(fastReview) == 0 || len(slowCoding) == 0 || len(slowReview) == 0 {
		t.Fatalf("the fixture's installation deploys %v and %v, want a coding job and a review variant of each github profile", slices.Sorted(maps.Keys(coding)), slices.Sorted(maps.Keys(review)))
	}

	// The transition: the config declares the bitbucket default alone, so the
	// set the spec composes holds no job of either github profile.
	fresh := projectReviewJobsDocument(t, implicitReviewLC()+
		"profiles:\n  standard: { provider: bitbucket, cpu: 2, memory: 8Gi, timeout_class: 6h }\n")
	got, err := CarriedProjectJobs(fresh, deployed, []string{"fast"})
	if err != nil {
		t.Fatalf("the carry failed: %v", err)
	}

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
		t.Errorf("the carried document's project_jobs are %v, want %v: the fast profile's coding job and nothing else — the slow profile is not named, and its job stays where the config and the state have it",
			slices.Sorted(maps.Keys(gotCoding)), slices.Sorted(maps.Keys(wantCoding)))
	}
	if gotReview := reviewJobsOf(t, got); !reflect.DeepEqual(gotReview, wantReview) {
		t.Errorf("the carried document's project_review_jobs are %v, want %v: a variant of the slow profile's job is a job the carry put back for a profile nobody named",
			slices.Sorted(maps.Keys(gotReview)), slices.Sorted(maps.Keys(wantReview)))
	}
	if got := jobsOfProfile(t, projectJobsOf(t, got), "slow"); len(got) != 0 {
		t.Errorf("the carry took the slow profile's jobs %v: it carries the profiles the caller names and no other", slices.Sorted(maps.Keys(got)))
	}
	if got := jobsOfProfile(t, reviewJobsOf(t, got), "slow"); len(got) != 0 {
		t.Errorf("the carry took the slow profile's variants %v: it carries the profiles the caller names and no other", slices.Sorted(maps.Keys(got)))
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

// TestCarriedProjectJobNamesPutsBackTheNamedJobsAlone: the carry of the
// instances a retirement KEEPS (issue #3686) takes the named jobs out of the
// record and nothing else, from both keys, and reports the names it answered
// for — which is what lets the caller say in one line about the names it could
// not carry, rather than passing over an instance it cannot leave whole.
//
// A job the document already composes is the spec's own answer and stays (the
// shared walk's rule): a kept instance's name is one the document does not
// carry, which is what makes it a retirement at all, so this is the carry's
// own safety net rather than a shape the caller produces.
//
// prev nil is a machine that has never planned this installation, and it is
// the one input where the caller is told nothing was taken: the instance then
// cannot be left whole, and the plan's own refusal is what says so.
func TestCarriedProjectJobNamesPutsBackTheNamedJobsAlone(t *testing.T) {
	deployed := []byte(`{
	  "project_jobs": {
	    "gone": {"profile": "standard", "provider": "github", "account": "team-a", "job": "gone"},
	    "live": {"profile": "standard", "provider": "github", "account": "team-b", "job": "live"}
	  },
	  "project_review_jobs": {
	    "gone-review": {"profile": "standard", "provider": "github", "account": "team-a-review", "job": "gone-review"}
	  }
	}`)
	fresh := []byte(`{"project_jobs": {"live": {"profile": "standard", "provider": "github", "account": "team-b", "job": "live", "cpu": "4"}}}`)

	got, carried, err := CarriedProjectJobNames(fresh, deployed, []string{"gone", "gone-review", "absent"})
	if err != nil {
		t.Fatalf("the carry failed: %v", err)
	}
	if !slices.Equal(carried, []string{"gone", "gone-review"}) {
		t.Errorf("the carry answered for %v; want the two names the record holds, sorted and without the one it does not", carried)
	}
	if _, ok := projectJobsOf(t, got)["gone"]; !ok {
		t.Errorf("the document does not carry the kept coding job: %s", got)
	}
	if _, ok := reviewJobsOf(t, got)["gone-review"]; !ok {
		t.Errorf("the document does not carry the kept instance's review variant: %s", got)
	}
	// The spec's own answer is left exactly as the spec wrote it, and a job
	// neither key of the record holds is carried as nothing.
	if cpu, _ := projectJobsOf(t, got)["live"].(map[string]any)["cpu"].(string); cpu != "4" {
		t.Errorf("the document's live job reads %v; want the spec's own, untouched", projectJobsOf(t, got)["live"])
	}
	// A job the document already carries is not one the carry took.
	if _, carried, _ := CarriedProjectJobNames(fresh, deployed, []string{"live"}); len(carried) != 0 {
		t.Errorf("the carry answered for %v; want nothing for a job the document composes itself", carried)
	}
	// A machine that has never planned this installation holds no record.
	_, carried, err = CarriedProjectJobNames(fresh, nil, []string{"gone"})
	if err != nil {
		t.Fatalf("the carry failed: %v", err)
	}
	if len(carried) != 0 {
		t.Errorf("the carry answered for %v with no record at all; want nothing, so the caller says it could not be left whole", carried)
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
		// The implicit default review job of a provider is the project's own, not
		// a variant of the carried profile: it carries that profile's name by
		// design and is composed whenever a repository of its provider exists.
		if slices.ContainsFunc(ImplicitReviewProviders, func(p string) bool { return name == ImplicitReviewJobName(p) }) {
			continue
		}
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
