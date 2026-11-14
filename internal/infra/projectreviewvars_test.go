package infra

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov/providers"
)

// projectReviewJobsDocument is the installation root's tfvars as a run of this
// release writes them for the config lcJSON: InstallationVars over the spec
// Installation composes, which is what prepare hands Terraform and what the
// carry below reads and writes.
func projectReviewJobsDocument(t *testing.T, lcJSON string) []byte {
	t.Helper()
	spec, err := Installation(parseLC(t, lcJSON), InstallOptions{BudgetBackend: true})
	if err != nil {
		t.Fatal(err)
	}
	b, err := InstallationVars(spec)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// reviewJobsOf is the project_review_jobs member of a tfvars document, nil when
// it carries none (the key is omitted for an empty set).
func reviewJobsOf(t *testing.T, vars []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(vars, &doc); err != nil {
		t.Fatalf("the document is not a tfvars: %v\n%s", err, vars)
	}
	jobs, _ := doc[ProjectReviewJobsKey].(map[string]any)
	return jobs
}

// TestCarriedProjectReviewJobsKeepsTheJobTheConfigCanNoLongerCompose is the
// report of the installation that deploys the implicit default review job and
// then loses the one fact the config composes it from.
//
// implicitReviewVariant is the job's only source, and one of its gates is the
// single GitHub App ID the config records: a second App ID added (a repository
// on another App) or the last github repository gone (no App ID recorded at
// all) takes the job out of project_review_jobs. The job is deployed all the
// same, so the next plan carries the delete of the module instance that holds
// it and RefuseProjectJobDeletes stops the whole `fugaro update` — with
// "restore the profile as it was" as the only remedy it can print, a profile an
// installation that declares none does not have. Every later run repeats it,
// and the installation never converges again.
//
// The carry is what keeps it: the job the installation's own tfvars hold is put
// back into the document the spec composes, whole, so the plan proposes no
// change to it. Both transitions are here because both are ordinary acts.
//
// Mutations (each run and restored): return vars without the job when the
// config composes none, and both transitions fail; carry every review job the
// record holds rather than the implicit one, and the fast profile's variant
// below comes back into a document that declares no such profile.
func TestCarriedProjectReviewJobsKeepsTheJobTheConfigCanNoLongerCompose(t *testing.T) {
	// The installation that has the job deployed, with a profile beside it: the
	// record holds the implicit job AND a variant the config below no longer
	// composes, so the carry's narrowness is tested by the same document.
	one := implicitReviewLC() + implicitReviewProfileLCBlock
	deployed := projectReviewJobsDocument(t, one)
	name := ImplicitReviewJobName(githubProvider)
	job, ok := reviewJobsOf(t, deployed)[name]
	if !ok {
		t.Fatalf("the installation that records one App ID deploys %v, want the implicit review job %s", reviewJobsOf(t, deployed), name)
	}
	if len(reviewJobsOf(t, deployed)) != 2 {
		t.Fatalf("the fixture's installation deploys %v, want the implicit job and the fast profile's variant", reviewJobsOf(t, deployed))
	}

	// The two transitions are configs with no profile of their own: with one
	// declared, the coding set is refused before this job is reached at all
	// (projectJobGitHubAppID), and the shapes below are the ones that reach it.
	none := implicitReviewLC()
	noApp := strings.Replace(none, `, github_app_id: "123456789"`, "", 1)
	twoApps := strings.Replace(none, projectJobRepoLC,
		projectJobRepoLC+`  acme/other: { provider: github, base_branch: main, workflows: [web], github_app_id: "987654321" }`+"\n", 1)
	if noApp == none || twoApps == none {
		t.Fatal("the two fixtures below did not change: this test would read the installation that records one App ID and composes the job")
	}
	for _, c := range []struct{ what, lc string }{
		{"the last github repository gone (no App ID recorded)", noApp},
		{"a second App ID recorded", twoApps},
	} {
		fresh := projectReviewJobsDocument(t, c.lc)
		if got := reviewJobsOf(t, fresh); got != nil {
			t.Fatalf("%s: the fixture's own document already carries %v, so this test would not be about the carry", c.what, got)
		}
		got, err := CarriedProjectReviewJobs(fresh, deployed)
		if err != nil {
			t.Fatalf("%s: %v", c.what, err)
		}
		jobs := reviewJobsOf(t, got)
		kept, ok := jobs[name]
		if !ok {
			t.Errorf("%s: the carried document holds %v, want the deployed review job %s: the next plan carries its delete, and RefuseProjectJobDeletes stops the whole update on it", c.what, jobs, name)
			continue
		}
		if !reflect.DeepEqual(kept, job) {
			t.Errorf("%s: the carried job is %v, want the deployed one %v, byte for byte: a job that differs is a job the plan proposes to change", c.what, kept, job)
		}
		// And nothing else: the variant of the profile the RECORD's installation
		// declared is the config's to compose, and a document that brings one
		// back is a document that deploys a job no profile declares.
		if len(jobs) != 1 {
			t.Errorf("%s: the carried document holds %v, want the implicit job alone", c.what, jobs)
		}
	}
}

// reviewJobAppID is the document vars with the App ID inside the job named name
// set to appID: the fixture that distinguishes two documents by a fact inside
// the job they carry.
func reviewJobAppID(t *testing.T, vars []byte, name, appID string) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(vars, &doc); err != nil {
		t.Fatal(err)
	}
	job, _ := doc[ProjectReviewJobsKey].(map[string]any)[name].(map[string]any)
	env, _ := job["env"].(map[string]any)
	if env == nil {
		t.Fatalf("the job %s carries no environment to change: %v", name, job)
	}
	env[providers.EnvGitHubAppID] = appID
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reviewJobsOf(t, b)[name]; !ok {
		t.Fatalf("the document below no longer carries the job %s: this fixture would not test what it says", name)
	}
	return b
}

// TestCarriedProjectReviewJobsLeavesTheSpecsOwnJobAlone: the carry fills what
// the spec cannot compose and nothing else. A document that composes the job
// itself is the authority on it — that run is what catches the job up with the
// release's image and environment — so the record never overwrites it; a record
// that holds no job of that name carries nothing; and a workdir with no tfvars
// at all (nil: a first run, or a machine that never ran this installation) is
// not a document to invent one from.
//
// Mutation (verified, run and restored): drop the "the spec's own job stands"
// early return: the fixture's fresh document comes back with the record's job
// instead of its own.
func TestCarriedProjectReviewJobsLeavesTheSpecsOwnJobAlone(t *testing.T) {
	name := ImplicitReviewJobName(githubProvider)
	composed := projectReviewJobsDocument(t, implicitReviewLC())
	if _, ok := reviewJobsOf(t, composed)[name]; !ok {
		t.Fatalf("the fixture composes no %s: %v", name, reviewJobsOf(t, composed))
	}
	// The record's job, and a document that has one of its own — the two
	// distinguished by a fact inside the job (the App ID the record names,
	// which is exactly the fact a config that composes the job again may
	// change), so that an overwrite is visible rather than an identity.
	record := projectReviewJobsDocument(t, implicitReviewLC())
	other := reviewJobAppID(t, record, name, "987654321")
	own := projectReviewJobsDocument(t, implicitReviewLC())
	if got, err := CarriedProjectReviewJobs(own, []byte(other)); err != nil {
		t.Fatal(err)
	} else if string(got) != string(own) {
		t.Errorf("a document that composes the job itself came back changed:\nwant %s\ngot  %s", own, got)
	}

	// A record that holds no job of that name — the document below still
	// carries the fast profile's variant, so it is a review set without being
	// this one — carries nothing, and neither does no document at all: a
	// machine that never ran this installation is not a record of one.
	without := projectReviewJobsDocument(t, implicitReviewLC()+implicitReviewProfileLCBlock)
	var doc map[string]any
	if err := json.Unmarshal(without, &doc); err != nil {
		t.Fatal(err)
	}
	jobs, _ := doc[ProjectReviewJobsKey].(map[string]any)
	delete(jobs, name)
	noJob, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reviewJobsOf(t, noJob)[name]; ok {
		t.Fatalf("the record below still carries %s: this fixture would not test what it says", name)
	}
	fresh := []byte(`{"project": "proj-1234"}`)
	for what, prev := range map[string][]byte{"a record with another review job": noJob, "no document at all": nil} {
		got, err := CarriedProjectReviewJobs(fresh, prev)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if string(got) != string(fresh) {
			t.Errorf("%s: the document changed: %s", what, got)
		}
	}
}

// TestCarriedProjectReviewJobsRefusesUnreadablePreviousVars: a previous
// document this program cannot read is an error and not "nothing to carry", the
// direction the other carries take for the same reason — the document that
// follows a failed read is the plan of the job's destruction.
//
// Mutation (verified, run and restored): return vars on a decode failure: this
// test fails.
func TestCarriedProjectReviewJobsRefusesUnreadablePreviousVars(t *testing.T) {
	fresh := []byte(`{"project": "proj-1234"}`)
	for _, prev := range [][]byte{[]byte("not json"), []byte(""), []byte("[1,2]")} {
		if _, err := CarriedProjectReviewJobs(fresh, prev); err == nil {
			t.Errorf("CarriedProjectReviewJobs(fresh, %q) = nil error, want a refusal", prev)
		}
	}
}
