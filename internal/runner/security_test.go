package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/orchestrator/sensitivity"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// listedPath is a path a floor row matches — the credentials row — and
// routinePath a path no row does: docs/ is a routine tree, and only the two
// docs/ rows the routing text's own exceptions name are on the floor.
const (
	listedPath  = "internal/creds/state.go"
	routinePath = "docs/recipes.md"
)

// securityRun builds the least a run needs to escalate: a real checkout on a
// branch of its own, the recipe and the configuration the levels are resolved
// against, and a record to write into. mode is the rigor mode the run resolved
// at bootstrap, which is what decides whether level 3 already runs.
func securityRun(t *testing.T, mode rigor.Mode) (*run, *gitops.Repo) {
	t.Helper()
	return securityRunOn(t, "main", mode)
}

// securityRunOn is securityRun with the base branch the run's diff is measured
// against spelled out, so a test can hand it one git cannot resolve.
func securityRunOn(t *testing.T, base string, mode rigor.Mode) (*run, *gitops.Repo) {
	t.Helper()
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, map[string]string{"README.md": "hello\n"})
	repo, err := gitops.OpenOrClone(context.Background(), filepath.Join(t.TempDir(), "work"), remote, gitops.IdentityEnv())
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CheckoutNewBranch(context.Background(), "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	r := &run{
		repo: repo,
		cfg:  &config.Config{Git: config.Git{BaseBranch: base}},
		spec: &task.Spec{},
		rec:  &runstore.Record{Rigor: &runstore.RigorRecord{Mode: mode, From: rigor.FromDefault, At: runstore.RigorAtBootstrap}},
		d:    Deps{Log: slog.New(slog.DiscardHandler)},
		rcp:  parseRecipe(t, "version: 1\nname: loop\nsteps:\n  - review: {}\n"),
	}
	if err := r.resolveLevels(); err != nil {
		t.Fatalf("resolving the levels of a %s run: %v", mode, err)
	}
	if err := r.buildPlan(); err != nil {
		t.Fatalf("building the plan of a %s run: %v", mode, err)
	}
	return r, repo
}

// change commits files onto the run's branch: the diff the escalation reads.
func change(t *testing.T, repo *gitops.Repo, files map[string]string, msg string) {
	t.Helper()
	testutil.WriteFiles(t, repo.Dir, files)
	if _, err := repo.CommitAll(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
}

// hasAdversarialStep reports whether the plan carries level 3's step.
func hasAdversarialStep(plan []planStep) bool {
	return slices.ContainsFunc(plan, func(st planStep) bool { return st.Kind == recipe.StepAdversarial })
}

// TestRunEscalatesToLevelThreeOnAMatchingDiff is the escalation itself: a diff
// that touches a floor row turns level 3 on and puts the adversarial step in
// the plan, which is what the run then spends (D1507, D1508).
func TestRunEscalatesToLevelThreeOnAMatchingDiff(t *testing.T) {
	r, repo := securityRun(t, rigor.Pro)
	change(t, repo, map[string]string{listedPath: "package creds\n"}, "work on the credentials tree")
	if levelOn(r.levels, policy.LevelL3) {
		t.Fatal("level 3 runs before the escalation, so the test proves nothing")
	}
	r.escalateSecurity(context.Background(), runstore.SecurityAtImplement)
	if !levelOn(r.levels, policy.LevelL3) {
		t.Fatalf("level 3 is off after a diff that touches %s: %+v", listedPath, r.levels)
	}
	if !hasAdversarialStep(r.plan) {
		t.Fatalf("the plan carries no adversarial step: %+v", r.plan)
	}
}

// TestRunEscalationLeavesTheRigorModeAlone: the escalation is a level, not a
// mode (D1508). A mode raise would rewrite the round counts and the findings
// policy for a change whose only new fact is its paths.
func TestRunEscalationLeavesTheRigorModeAlone(t *testing.T) {
	r, repo := securityRun(t, rigor.Pro)
	change(t, repo, map[string]string{listedPath: "package creds\n"}, "work on the credentials tree")
	before := *r.rec.Rigor
	r.escalateSecurity(context.Background(), runstore.SecurityAtImplement)
	if r.rec.Security == nil {
		t.Fatal("the escalation did not fire")
	}
	if !reflect.DeepEqual(*r.rec.Rigor, before) {
		t.Fatalf("the rigor block moved: %+v, want %+v", *r.rec.Rigor, before)
	}
}

// TestRunRecordsThePatternThatEscalatedIt: the record says which row decided
// and at which moment, which is what makes "why did this run review at level
// 3?" answerable from the record rather than from the diff (D1509).
func TestRunRecordsThePatternThatEscalatedIt(t *testing.T) {
	r, repo := securityRun(t, rigor.Pro)
	change(t, repo, map[string]string{listedPath: "package creds\n"}, "work on the credentials tree")
	r.escalateSecurity(context.Background(), runstore.SecurityAtFix)
	got := r.rec.Security
	if got == nil {
		t.Fatal("the record carries no escalation")
	}
	if got.At != runstore.SecurityAtFix {
		t.Errorf("the moment = %q, want %q", got.At, runstore.SecurityAtFix)
	}
	if got.Pattern != "credentials" {
		t.Errorf("the pattern = %q, want the floor row %s matches", got.Pattern, listedPath)
	}
	if got.Category != sensitivity.Credentials {
		t.Errorf("the category = %q, want %q", got.Category, sensitivity.Credentials)
	}
}

// TestRunWithLevelThreeAlreadyAskedRecordsNoEscalation: a run whose own
// review_levels block already carries level 3 escalated to nothing, and the
// record must not claim it did (D1509).
func TestRunWithLevelThreeAlreadyAskedRecordsNoEscalation(t *testing.T) {
	r, repo := securityRun(t, rigor.Pro)
	r.spec.ReviewLevels = &config.ReviewLevels{L3: &config.LevelSetting{Model: "claude-opus-5-5"}}
	if err := r.resolveLevels(); err != nil {
		t.Fatal(err)
	}
	if !levelOn(r.levels, policy.LevelL3) {
		t.Fatal("the task's level 3 block did not turn level 3 on, so the test proves nothing")
	}
	change(t, repo, map[string]string{listedPath: "package creds\n"}, "work on the credentials tree")
	r.escalateSecurity(context.Background(), runstore.SecurityAtImplement)
	if r.rec.Security != nil {
		t.Fatalf("a run that already ran level 3 recorded an escalation: %+v", r.rec.Security)
	}
}

// TestRunEscalationTurnsLevelThreeOnOverALayersNone: the forced level is
// raise-only (D1517 — the reason SC10's `security: false` changes nothing), so
// a layer that writes level 3's own off-switch does not defeat the escalation.
// D722 refuses a floored level a layer turned off, and that refusal is the
// launcher's: it is raised at bootstrap, where the person who wrote the task
// can still be told. At this seam there is no launcher left, and the run's own
// classification decides (D1510) — without this the run would be
// indistinguishable from one whose diff matched nothing at all: level 3 off, no
// adversarial step, and no escalation in the record.
func TestRunEscalationTurnsLevelThreeOnOverALayersNone(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *config.LevelSetting
		task *config.LevelSetting
	}{
		{"the configuration's none", &config.LevelSetting{Off: true}, nil},
		{"the configuration's literal none model", &config.LevelSetting{Model: config.NoneModel}, nil},
		{"the task's none", nil, &config.LevelSetting{Off: true}},
		{"both layers", &config.LevelSetting{Off: true}, &config.LevelSetting{Off: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, repo := securityRun(t, rigor.Pro)
			r.cfg.Agent.Models = config.ModelRoles{Coder: "claude-sonnet-5", Reviewer: reviewerModel}
			r.cfg.Agent.Levels.L3 = tc.cfg
			if tc.task != nil {
				r.spec.ReviewLevels = &config.ReviewLevels{L3: tc.task}
			}
			// The ordinary resolution: level 3 is off, and Pro's own floor does
			// not require it, so the layer's `none` is a legal configuration
			// and no refusal is raised.
			if err := r.resolveLevels(); err != nil {
				t.Fatalf("resolving the levels of a run whose level 3 is off: %v", err)
			}
			if err := r.buildPlan(); err != nil {
				t.Fatal(err)
			}
			if levelOn(r.levels, policy.LevelL3) {
				t.Fatal("level 3 runs before the escalation, so the test proves nothing")
			}
			change(t, repo, map[string]string{listedPath: "package creds\n"}, "work on the credentials tree")
			r.escalateSecurity(context.Background(), runstore.SecurityAtImplement)
			if !levelOn(r.levels, policy.LevelL3) {
				t.Fatalf("a layer's none defeated the escalation: %+v", r.levels)
			}
			if got := r.levels.L3.Model; got != reviewerModel {
				t.Errorf("the escalated level runs %q, want the reviewer's own %q: the off-switch is cleared, not the level's model", got, reviewerModel)
			}
			if !hasAdversarialStep(r.plan) {
				t.Fatalf("the plan carries no adversarial step: %+v", r.plan)
			}
			if r.rec.Security == nil {
				t.Fatal("the escalation fired and the record carries nothing, so the run looks like one whose diff matched nothing")
			}
		})
	}
}

// reviewerModel is the model the levels in these tests resolve to where no
// layer names one, so a case can tell a level that runs the reviewer's model
// from one that runs nothing at all.
const reviewerModel = "claude-opus-5-5"

// TestRunEscalationSurvivesAUnitThatChangedNothing: the escalation is
// raise-only. A later round that changed nothing — an empty diff — must not put
// the levels back where they were.
func TestRunEscalationSurvivesAUnitThatChangedNothing(t *testing.T) {
	r, repo := securityRun(t, rigor.Pro)
	change(t, repo, map[string]string{listedPath: "package creds\n"}, "work on the credentials tree")
	r.escalateSecurity(context.Background(), runstore.SecurityAtImplement)
	if r.rec.Security == nil {
		t.Fatal("the escalation did not fire")
	}
	// The next unit changed nothing: the branch is back at its base, so the
	// diff the escalation reads is empty.
	if err := repo.ResetTo(context.Background(), "main"); err != nil {
		t.Fatal(err)
	}
	r.escalateSecurity(context.Background(), runstore.SecurityAtFix)
	if !levelOn(r.levels, policy.LevelL3) {
		t.Fatalf("a unit that changed nothing put level 3 back off: %+v", r.levels)
	}
	if r.rec.Security == nil || r.rec.Security.At != runstore.SecurityAtImplement {
		t.Fatalf("the escalation = %+v, want the implement one kept", r.rec.Security)
	}
}

// TestRunEscalationIsIdempotentAcrossRoundRigor: the fix seam runs roundRigor
// (D22's second moment) and then the escalation, and the two must not fight —
// the mode's re-resolution leaves the escalated levels and plan where they are.
func TestRunEscalationIsIdempotentAcrossRoundRigor(t *testing.T) {
	r, repo := securityRun(t, rigor.Pro)
	change(t, repo, map[string]string{listedPath: "package creds\n"}, "work on the credentials tree")
	r.escalateSecurity(context.Background(), runstore.SecurityAtImplement)
	if r.rec.Security == nil {
		t.Fatal("the escalation did not fire")
	}
	levels, plan := r.levels, slices.Clone(r.plan)
	r.roundRigor(context.Background())
	r.escalateSecurity(context.Background(), runstore.SecurityAtFix)
	if r.levels != levels {
		t.Fatalf("the levels moved: %+v, want %+v", r.levels, levels)
	}
	if !slices.Equal(r.plan, plan) {
		t.Fatalf("the plan moved: %+v, want %+v", r.plan, plan)
	}
	if r.rec.Security.At != runstore.SecurityAtImplement {
		t.Fatalf("the moment = %q, want the implement one", r.rec.Security.At)
	}
}

// TestRunEscalationReadsTheDiffBeforeTheRecordHasIt is the test the interface
// exists for (D1507, F5): Record.ChangedFiles is written only at finalize, so
// at the post-implement seam it is still nil. An escalation built on the record
// would classify an empty list and never fire.
func TestRunEscalationReadsTheDiffBeforeTheRecordHasIt(t *testing.T) {
	r, repo := securityRun(t, rigor.Pro)
	change(t, repo, map[string]string{listedPath: "package creds\n"}, "work on the credentials tree")
	if r.rec.ChangedFiles != nil {
		t.Fatalf("the record already carries the changed paths (%+v), so the seam this test pins is not the seam it reached", r.rec.ChangedFiles)
	}
	r.escalateSecurity(context.Background(), runstore.SecurityAtImplement)
	if r.rec.Security == nil {
		t.Fatal("no escalation fired: the read took the record's own ChangedFiles, which is nil at this moment")
	}
	if r.rec.ChangedFiles != nil {
		t.Fatal("the escalation wrote the record's changed files")
	}
}

// TestRunEscalationWarnsAndKeepsTheLevelsWhenTheReadFails: the escalation never
// fails the run. A diff that cannot be read is a warning, and the levels stay
// where they are, which is roundRigor's own rule for the mode (F9).
func TestRunEscalationWarnsAndKeepsTheLevelsWhenTheReadFails(t *testing.T) {
	r, _ := securityRunOn(t, "no-such-branch", rigor.Pro)
	var logged bytes.Buffer
	r.d.Log = slog.New(slog.NewTextHandler(&logged, nil))
	levels, plan := r.levels, slices.Clone(r.plan)
	r.escalateSecurity(context.Background(), runstore.SecurityAtImplement)
	if r.rec.Security != nil {
		t.Fatalf("a failed read recorded an escalation: %+v", r.rec.Security)
	}
	if r.levels != levels || !slices.Equal(r.plan, plan) {
		t.Fatalf("a failed read moved the levels (%+v) or the plan (%+v)", r.levels, r.plan)
	}
	if !strings.Contains(logged.String(), "security escalation") {
		t.Fatalf("a failed read logged no warning: %q", logged.String())
	}
}

// TestRunEscalationFiresOnAPathOnlyALaterFixAdded: a fix round can add a path
// the implement round did not touch, which is why the post-fix seam exists
// (D1507, F9).
func TestRunEscalationFiresOnAPathOnlyALaterFixAdded(t *testing.T) {
	r, repo := securityRun(t, rigor.Pro)
	change(t, repo, map[string]string{routinePath: "a recipe\n"}, "implement")
	r.escalateSecurity(context.Background(), runstore.SecurityAtImplement)
	if r.rec.Security != nil {
		t.Fatalf("a routine diff escalated: %+v", r.rec.Security)
	}
	change(t, repo, map[string]string{listedPath: "package creds\n"}, "fix")
	r.escalateSecurity(context.Background(), runstore.SecurityAtFix)
	if r.rec.Security == nil {
		t.Fatalf("a fix that added %s did not escalate", listedPath)
	}
	if r.rec.Security.At != runstore.SecurityAtFix {
		t.Fatalf("the moment = %q, want %q", r.rec.Security.At, runstore.SecurityAtFix)
	}
	if !hasAdversarialStep(r.plan) {
		t.Fatalf("the plan carries no adversarial step: %+v", r.plan)
	}
}

// TestRunRecordGainsTheFieldAdditively: the escalation is a new member on the
// record and nothing else changes. A record carrying it keeps every member it
// already had, and a record written before the field existed decodes unchanged
// (D1509, F7).
func TestRunRecordGainsTheFieldAdditively(t *testing.T) {
	rec := runstore.Record{
		Version: 1, RunID: "20261108-000000-abcd", Repo: "acme/app",
		Status: runstore.StatusSucceeded, Stage: "finalize", Outcome: runstore.OutcomeDraft,
		Rigor:        &runstore.RigorRecord{Mode: rigor.Pro, From: rigor.FromDefault, At: runstore.RigorAtBootstrap},
		ChangedFiles: &runstore.ChangedFiles{Paths: []string{listedPath}, Total: 1},
		Security: runstore.NewSecurityEscalation(runstore.SecurityAtImplement,
			sensitivity.Pattern{Name: "credentials", Match: "internal/creds/**", Category: sensitivity.Credentials}),
		StartedAt: time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC),
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	var back runstore.Record
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, rec) {
		t.Fatalf("the round trip changed the record:\n got %+v\nwant %+v", back, rec)
	}
	var old runstore.Record
	const before = `{"version":1,"run_id":"20261107-000000-abcd","repo":"acme/app","status":"succeeded","stage":"finalize","outcome":"draft","started_at":"2026-11-07T00:00:00Z"}`
	if err := json.Unmarshal([]byte(before), &old); err != nil {
		t.Fatal(err)
	}
	if old.Security != nil {
		t.Fatalf("a record written before the field decodes with an escalation: %+v", old.Security)
	}
}
