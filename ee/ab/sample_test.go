// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package ab_test

import (
	"fmt"
	"math"
	"testing"

	"github.com/dimipaun/fugaro/ee/ab"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/task"
)

// digestRecipe is a report run's recipe: a task carrying it (or the manifest
// such a run lists) is not a code task, whatever else it looks like.
const digestRecipe = `version: 1
name: run-digest
description: Writes the run reports the repository is missing.
use_when: A scheduled run review only.
steps:
  - digest: {}
  - review:
      max_rounds: 1
`

// firstRun is a task as the launcher writes one for a first run of a code
// task: the shape D558 samples, and the base every exclusion below moves one
// field of.
func firstRun() task.Spec {
	return task.Spec{
		Version:     1,
		RunID:       "20260101-000000-0001",
		Repo:        "acme/widgets",
		Ref:         "main",
		Workflow:    "go",
		Task:        "add the flag the issue asks for",
		RequestedBy: "dev",
	}
}

// sampleIDs is n run IDs of the shape task.NewRunID writes, fixed rather than
// drawn: a count over them is the same on every run, so a rate can be read
// against a number rather than against a tolerance. They are all different,
// so no ID is counted twice.
func sampleIDs(n int) []string {
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		ids = append(ids, fmt.Sprintf("20260101-%06d-%04x", i%1000000, i%65536))
	}
	return ids
}

// TestSampledIsDeterministic pins the sample to a hash of the run ID (D557):
// the same ID answers the same way every time it is read, and the four
// vectors below are what a person auditing a launch recomputes from the ID
// alone — sha256 of the ID, its first eight bytes read big-endian as a
// fraction of 2^64, sampled when that fraction is below the rate. A draw
// from a random source would answer differently on the second read of one
// launch, and no auditor could recompute which way it went.
func TestSampledIsDeterministic(t *testing.T) {
	for _, tc := range []struct {
		runID string
		frac  float64
	}{
		{"20260101-000000-0003", 0.0006342956015879335},
		{"20260101-000000-0001", 0.14790573968144263},
		{"20260301-235959-ffff", 0.6699512936546287},
		{"20260101-000000-0002", 0.9096592118112052},
	} {
		for _, rate := range []float64{0.01, 0.05, 0.1, 0.5, 0.95} {
			want := tc.frac < rate
			for i := 0; i < 3; i++ {
				if got := ab.Sampled(tc.runID, rate); got != want {
					t.Fatalf("Sampled(%q, %v) = %v, want %v: run ID %s hashes to %v of the run ID space, and the sample is that fraction below the rate — the same ID has to answer the same way every time it is read (D557)",
						tc.runID, rate, got, want, tc.runID, tc.frac)
				}
			}
		}
	}
}

// TestSampledRateZeroNever is the red half of the rule: a project that wrote
// no ab: block, or opted out of sampling with ab.rate: 0, samples nothing at
// all (D557). A stub that sampled every run ID would pass every other test
// here and fail this one, which is what the rate's default being off means.
func TestSampledRateZeroNever(t *testing.T) {
	for _, rate := range []float64{0, -1, -0.5, math.NaN(), math.Inf(-1)} {
		for _, id := range sampleIDs(2000) {
			if ab.Sampled(id, rate) {
				t.Fatalf("Sampled(%q, %v) = true: rate 0 is the default and what a repository's own ab: block may write to opt out, so it samples nothing — no rate a project layer may not write may sample anything either, and sampling nothing is the direction that spends nothing (D557)",
					id, rate)
			}
		}
	}
}

// TestSampledRateMatchesOverManyIDs reads the rate as the share of eligible
// runs a project gets (D557): over twenty thousand fixed run IDs the sample
// lands on the share asked for, and on exactly the count the hash gives.
// The counts are pinned rather than banded because the IDs are fixed — a
// hash that changed under the sampler would otherwise move every figure in
// a report without a test noticing.
func TestSampledRateMatchesOverManyIDs(t *testing.T) {
	ids := sampleIDs(20000)
	for _, tc := range []struct {
		rate float64
		want int
	}{
		{0, 0},
		{0.01, 185},
		{0.05, 1006},
		{0.1, 1981},
		{1, len(ids)},
	} {
		got := 0
		for _, id := range ids {
			if ab.Sampled(id, tc.rate) {
				got++
			}
		}
		if got != tc.want {
			t.Errorf("Sampled(_, %v) held for %d of %d run IDs, want %d: the IDs are fixed, so the count is too, and a hash that moved is a rate that no longer means what the project layer wrote", tc.rate, got, len(ids), tc.want)
		}
		if tc.rate <= 0 || tc.rate >= 1 {
			continue
		}
		if share := float64(got) / float64(len(ids)); math.Abs(share-tc.rate) > 0.01 {
			t.Errorf("Sampled(_, %v) held for %.3f of %d run IDs: the rate is the share of eligible runs a project is sampled for, so the sample has to land on it (D557)", tc.rate, share, len(ids))
		}
	}
}

// TestEligibleAllowsAFirstCodeRun is the positive half of D558: a first run
// of a code task, at POC to Critical, is what a project samples. A launch
// that resolved no recipe carries none, and the runner reads the repository's
// own file at the ref, which an implement recipe is by default.
func TestEligibleAllowsAFirstCodeRun(t *testing.T) {
	spec := firstRun()
	for _, mode := range []rigor.Mode{rigor.POC, rigor.Startup, rigor.Pro, rigor.Critical} {
		for _, recipeMode := range []string{"", recipe.ModeImplement} {
			if !ab.Eligible(spec, recipeMode, mode, ab.LaunchFlags{}) {
				t.Errorf("Eligible(a first run of a code task, recipe %q, mode %s) = false: a first run of a code task at POC to Critical is exactly what D558 samples",
					recipeMode, mode)
			}
		}
	}
}

// TestEligibleExcludesFollowUpsRetriesAttemptsReviewArtifactDigest is D558's
// other half, case by case: a follow-up continues a pull request rather than
// making a first attempt, a retry re-launches the task of the run before it,
// an attempts batch is already several runs of one task, and a review,
// artifact or report recipe is not a code task at all — a pair drawn from
// any of them compares two unlike things or counts one task twice.
func TestEligibleExcludesFollowUpsRetriesAttemptsReviewArtifactDigest(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mutate     func(*task.Spec)
		recipeMode string
		flags      ab.LaunchFlags
	}{
		{
			name:   "a follow-up on a pull request",
			mutate: func(s *task.Spec) { s.PR = 12; s.Branch = "fugaro/20260101-000000-0001" },
		},
		{
			name:   "a follow-up on a branch",
			mutate: func(s *task.Spec) { s.Branch = "fugaro/20260101-000000-0001" },
		},
		{
			name:   "a follow-up of a previous run",
			mutate: func(s *task.Spec) { s.PreviousRun = "20260101-000000-0001" },
		},
		{
			name: "a triage follow-up",
			mutate: func(s *task.Spec) {
				s.FollowUpOf = &task.FollowUpOf{Run: "20260101-000000-0001", Keys: []string{"a.go: the flag is not read"}}
			},
		},
		{
			name:  "a retry of a stored task",
			flags: ab.LaunchFlags{Retry: true},
		},
		{
			name:  "a best-of-N batch",
			flags: ab.LaunchFlags{Attempts: 2},
		},
		{
			name:  "a run ID the caller chose",
			flags: ab.LaunchFlags{ChosenRunID: true},
		},
		{
			name:       "a review recipe",
			recipeMode: recipe.ModeReview,
		},
		{
			name:   "a review run's pull requests",
			mutate: func(s *task.Spec) { s.ReviewPRs = []int{12} },
		},
		{
			name:       "an artifact recipe",
			recipeMode: recipe.ModeArtifact,
		},
		{
			name:   "an artifact run's mode in the task",
			mutate: func(s *task.Spec) { s.Mode = recipe.ModeArtifact },
		},
		{
			name:   "an artifact run's inputs",
			mutate: func(s *task.Spec) { s.Inputs = []task.InputFile{{Name: "brief.md"}} },
		},
		{
			name:   "a report run's manifest",
			mutate: func(s *task.Spec) { s.DigestRuns = []string{"20260101-000000-0001"} },
		},
		{
			name: "a report run's recipe",
			mutate: func(s *task.Spec) {
				s.Recipe = &task.Recipe{Name: "run-digest", Source: "catalog", YAML: digestRecipe}
			},
		},
	} {
		spec := firstRun()
		if tc.mutate != nil {
			tc.mutate(&spec)
		}
		recipeMode := tc.recipeMode
		if recipeMode == "" {
			recipeMode = recipe.ModeImplement
		}
		if ab.Eligible(spec, recipeMode, rigor.Pro, tc.flags) {
			t.Errorf("Eligible(%s, mode pro) = true, want false: D558 samples first runs of code tasks only, and a pair drawn from anything else compares two unlike attempts or counts one task twice",
				tc.name)
		}
	}
}

// TestEligibleExcludesHotfix pins D558's one excluded mode: a hotfix is rare,
// time-bound, and its quality bar ("the smallest safe change") is not what a
// pair measures, so the group it would fill is one that may never reach a
// minimum sample. The other four modes are the axis a pair is scored on.
func TestEligibleExcludesHotfix(t *testing.T) {
	spec := firstRun()
	if ab.Eligible(spec, recipe.ModeImplement, rigor.Hotfix, ab.LaunchFlags{}) {
		t.Errorf("Eligible(a first run of a code task, mode hotfix) = true, want false: hotfix is off the rigor axis and D558 excludes it — its bar is the smallest safe change, which is not what a pair measures")
	}
	for _, mode := range []rigor.Mode{"", "bogus"} {
		if ab.Eligible(spec, recipe.ModeImplement, mode, ab.LaunchFlags{}) {
			t.Errorf("Eligible(a first run of a code task, mode %q) = true, want false: a mode outside the vocabulary is not one of the four D558 samples, and a run that resolved none is not a launch this build may spend on", mode)
		}
	}
}

// TestEligibleExcludesChosenRunID is the reason the run ID is a parameter at
// all: the sample is a hash of it (Sampled), so a launch whose ID the caller
// named — fugaro run --run-id — is one a developer could steer onto the
// sampled side by trying IDs until one landed under the rate. The task
// carries the ID alone, and a chosen one looks exactly like a generated one
// there, so the launcher says which it is.
func TestEligibleExcludesChosenRunID(t *testing.T) {
	spec := firstRun()
	if !ab.Eligible(spec, recipe.ModeImplement, rigor.Pro, ab.LaunchFlags{}) {
		t.Fatalf("the base task is not eligible at all, so this test would prove nothing about a chosen run ID")
	}
	if ab.Eligible(spec, recipe.ModeImplement, rigor.Pro, ab.LaunchFlags{ChosenRunID: true}) {
		t.Errorf("Eligible(_, _, _, LaunchFlags{ChosenRunID: true}) = true, want false: the sample is a hash of the run ID, so an ID the caller may choose is an ID the caller may choose to land under the rate (D557, D558)")
	}
}

// TestUnderDailyCount pins the soft cap (D557): a repository is sampled for
// at most ab.max_pairs_per_day pairs a day, counted by the launcher from
// today's challenger records, and the count is the caller's read — the cap
// reserves nothing, so two launches at once can both pass it. A count at the
// cap stops sampling; a cap of 0 or less is no room at all, which is the
// direction that spends nothing.
func TestUnderDailyCount(t *testing.T) {
	for _, tc := range []struct {
		n, max int
		want   bool
	}{
		{0, 5, true},
		{1, 5, true},
		{4, 5, true},
		{5, 5, false},
		{6, 5, false},
		{0, 1, true},
		{1, 1, false},
		{0, 0, false},
		{0, -1, false},
		{100, 5, false},
	} {
		if got := ab.UnderDailyCount(tc.n, tc.max); got != tc.want {
			t.Errorf("UnderDailyCount(%d, %d) = %v, want %v: the cap is reached when today's challenger records have reached it, and a cap of 0 or less is no room at all",
				tc.n, tc.max, got, tc.want)
		}
	}
}
