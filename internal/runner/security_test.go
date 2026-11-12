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
	"github.com/dimipaun/fugaro/internal/failure"
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

// TestRunEscalatesOnTheRepositorysOwnRow: the runner reads the one resolution
// (SC9, D1506, D1511), so a row the repository's own file wrote escalates the
// run exactly as a floor row does. ownPath is on no floor row, so the only
// list that can match it is the one the repository's block is part of.
func TestRunEscalatesOnTheRepositorysOwnRow(t *testing.T) {
	const ownPath = "internal/payments/charge.go"
	r, repo := securityRun(t, rigor.Pro)
	r.cfg.Security = &config.Security{Patterns: []sensitivity.Pattern{
		{Name: "payments", Match: "internal/payments/**", Category: sensitivity.Credentials},
	}}
	change(t, repo, map[string]string{ownPath: "package payments\n"}, "work on the payments tree")

	r.escalateSecurity(context.Background(), runstore.SecurityAtImplement)

	if r.rec.Security == nil || r.rec.Security.Refused {
		t.Fatalf("a diff that touches the repository's own row (%s) recorded no escalation: %+v", ownPath, r.rec.Security)
	}
	if r.rec.Security.Pattern != "payments" {
		t.Errorf("the record names %q, want the repository's own row", r.rec.Security.Pattern)
	}
	if !levelOn(r.levels, policy.LevelL3) {
		t.Fatalf("level 3 is off after a diff that touches the repository's own row %s: %+v", ownPath, r.levels)
	}
}

// TestRunEscalatesOnTheProjectLayersRow: the same, for the layer's half of the
// resolution (D1501). A path no floor row matches escalates when the project
// layer the run's task carried writes a row for it.
func TestRunEscalatesOnTheProjectLayersRow(t *testing.T) {
	const layerPath = "internal/settlements/batch.go"
	r, repo := securityRun(t, rigor.Pro)
	r.cfg.Layer = &config.ProjectLayer{Security: &config.LayerSecurity{Patterns: []sensitivity.Pattern{
		{Name: "settlements", Match: "internal/settlements/**", Category: sensitivity.Credentials},
	}}}
	change(t, repo, map[string]string{layerPath: "package settlements\n"}, "work on the settlements tree")

	r.escalateSecurity(context.Background(), runstore.SecurityAtImplement)

	if r.rec.Security == nil || r.rec.Security.Refused {
		t.Fatalf("a diff that touches the project layer's own row (%s) recorded no escalation: %+v", layerPath, r.rec.Security)
	}
	if r.rec.Security.Pattern != "settlements" {
		t.Errorf("the record names %q, want the project layer's row", r.rec.Security.Pattern)
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

// reviewOnlyRecipe is a recipe whose mode allows exactly one review step and
// nothing else: the one resolution the escalation's forced level 3 is refused
// by for a reason unrelated to a layer's own settings, so a case can reach the
// refusal without also turning the level off.
const reviewOnlyRecipe = "version: 1\nname: review-only\nmode: review\nsteps:\n  - review: { max_rounds: 1 }\n"

// TestRunRefusedEscalationRecordsTheMatchedRow: a diff that matched a floor row
// and an escalation the run could not take leaves the account a taken one does,
// with Refused set (D1509). Its refusal is a warning and the run goes on (F9),
// but without the member the run is indistinguishable from one whose diff
// matched nothing at all — level 3 off, no adversarial step, nothing recorded —
// and the only trace of a matched sensitive path is a log line, which is not an
// account the run keeps.
//
// The refusal here is the resolution's: recipe `mode: review` allows exactly
// one review step, so a level 3 the owner's list requires has no step to run in
// and is refused rather than dropped (G1, internal/config's reviewOnlyLevel).
func TestRunRefusedEscalationRecordsTheMatchedRow(t *testing.T) {
	r, repo := securityRun(t, rigor.Pro)
	// The plan and the levels the run resolved at bootstrap stay as they are:
	// only the recipe the forced resolution reads changes.
	r.rcp = parseRecipe(t, reviewOnlyRecipe)
	change(t, repo, map[string]string{listedPath: "package creds\n"}, "work on the credentials tree")
	r.escalateSecurity(context.Background(), runstore.SecurityAtImplement)
	if levelOn(r.levels, policy.LevelL3) {
		t.Fatalf("the escalation was refused and level 3 runs anyway: %+v", r.levels.L3)
	}
	if hasAdversarialStep(r.plan) {
		t.Fatalf("the escalation was refused and the plan carries an adversarial step: %+v", r.plan)
	}
	got := r.rec.Security
	if got == nil {
		t.Fatal("a refused escalation recorded nothing, so the run looks like one whose diff matched nothing")
	}
	if !got.Refused {
		t.Fatalf("the escalation was refused and the record claims it happened: %+v", got)
	}
	if got.At != runstore.SecurityAtImplement {
		t.Errorf("the moment = %q, want %q", got.At, runstore.SecurityAtImplement)
	}
	if got.Pattern != "credentials" || got.Category != sensitivity.Credentials {
		t.Errorf("the record names %q (%s), want the floor row %s matches", got.Pattern, got.Category, listedPath)
	}
}

// TestRunEscalationReplacesARefusalWhenItFires: the refusal is what the run
// records *until* the escalation happens. A member left refused beside a level
// that ran would keep a run out of ready for a refusal it no longer carries,
// and the record would say level 3 was refused in the same breath as the plan
// holding its step.
func TestRunEscalationReplacesARefusalWhenItFires(t *testing.T) {
	r, repo := securityRun(t, rigor.Pro)
	r.rcp = parseRecipe(t, reviewOnlyRecipe)
	change(t, repo, map[string]string{listedPath: "package creds\n"}, "work on the credentials tree")
	r.escalateSecurity(context.Background(), runstore.SecurityAtImplement)
	if r.rec.Security == nil || !r.rec.Security.Refused {
		t.Fatalf("the refusal was not recorded, so the second half of the test proves nothing: %+v", r.rec.Security)
	}
	r.rcp = parseRecipe(t, "version: 1\nname: loop\nsteps:\n  - review: {}\n")
	r.escalateSecurity(context.Background(), runstore.SecurityAtFix)
	if !levelOn(r.levels, policy.LevelL3) || !hasAdversarialStep(r.plan) {
		t.Fatalf("the escalation did not fire on the second seam: %+v, %+v", r.levels.L3, r.plan)
	}
	if got := r.rec.Security; got == nil || got.Refused || got.At != runstore.SecurityAtFix {
		t.Fatalf("the record still carries the refusal after the escalation ran: %+v", got)
	}
}

// TestSecurityRefusalKeepsTheRunOutOfReady: a refused escalation is not a
// finding and not a failure — the run goes on and its answer is a draft, not a
// stopped run — but it may not end ready either: the review the record shows
// did not cover a diff the owner's list called sensitive, and no answer from
// the run's own records can make that up (D1509).
func TestSecurityRefusalKeepsTheRunOutOfReady(t *testing.T) {
	rows := sensitivity.Match([]string{listedPath}, sensitivity.Floor())
	if len(rows) == 0 {
		t.Fatalf("the floor matches %s, so the record below is the shape the escalation writes", listedPath)
	}
	ran := runstore.NewSecurityEscalation(runstore.SecurityAtImplement, rows[0])
	refused := runstore.NewRefusedSecurityEscalation(runstore.SecurityAtImplement, rows[0])
	const testsFailed = "tests failing on the final commit"
	for _, tc := range []struct {
		name       string
		sec        *runstore.SecurityEscalation
		in         Decision
		wantReady  bool
		wantReason string
		wantClass  failure.Class
		wantKind   Kind
	}{
		{"a run that escalated nothing", nil, readyDecision(""), true, "", "", KindReady},
		{"a run whose escalation ran", ran, readyDecision(""), true, "", "", KindReady},
		{"a refused escalation turns a ready run into a draft", refused, readyDecision(""), false, ReasonSecurityRefused, failure.Unclassified, KindBlocked},
		{"a refused escalation replaces Decide's own refusal", refused, blockedDecision(testsFailed, failure.TestsFailed), false, ReasonSecurityRefused, failure.Unclassified, KindBlocked},
		// The kind moves with the sentence, and this is the case that matters: a
		// run no review round reached is the one Decide answers `unreviewed`
		// for, and a refusal found at finalize is a failure of its own — the
		// record may not carry the label over it (D994, D995).
		{"a refused escalation outranks the unreviewed label", refused, unreviewedDecision("no review verdict"), false, ReasonSecurityRefused, failure.Unclassified, KindBlocked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &run{rec: &runstore.Record{Security: tc.sec}}
			dec := r.applySecurityRefusal(tc.in)
			if dec.Ready != tc.wantReady || dec.Reason != tc.wantReason || dec.Class != tc.wantClass || dec.Kind != tc.wantKind {
				t.Errorf("applySecurityRefusal(%+v) = %+v, want (%v, %q, %q, %q)",
					tc.in, dec, tc.wantReady, tc.wantReason, tc.wantClass, tc.wantKind)
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

// resolveAgain is the run's own resolution taken again with whatever the test
// changed since its bootstrap: the same two calls resolvePolicy makes, in the
// same order, so what a test reads is what a run that resolved that
// configuration would have read.
func resolveAgain(t *testing.T, r *run) {
	t.Helper()
	if err := r.resolveLevels(); err != nil {
		t.Fatalf("resolving the levels: %v", err)
	}
	if err := r.buildPlan(); err != nil {
		t.Fatalf("building the plan: %v", err)
	}
}

// TestSecurityFieldForcesLevelThreeAtBootstrap: the task's own request — the
// field `fugaro run --security` writes — turns level 3 on for the run, whatever
// paths it changed, and the plan the run builds already holds the adversarial
// step (SC10, D1517). Nothing here reads the diff: the request is a resolution
// input (securityForcedLevels), not a classification, which is what separates
// it from SC6's escalation and what makes it true before the agent has run.
//
// The layer's `none` does not defeat it: the forced resolution clears the
// level's off-switch (plan.go's forcedLevelsAreRaiseOnly), the raise-only rule
// the request is held to. The last case is the one that shows the model beside
// the switch is kept: the off-switch is cleared, not the level's own settings.
func TestSecurityFieldForcesLevelThreeAtBootstrap(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *config.LevelSetting
	}{
		{"no layer setting", nil},
		{"the layer's none", &config.LevelSetting{Off: true}},
		{"the layer's literal none model", &config.LevelSetting{Model: config.NoneModel}},
		{"the layer's none beside a model", &config.LevelSetting{Model: "claude-opus-5-5"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := securityRun(t, rigor.Pro)
			r.spec.Security = true
			if tc.cfg != nil {
				r.cfg.Agent.Levels.L3 = tc.cfg
			}
			resolveAgain(t, r)
			if !levelOn(r.levels, policy.LevelL3) {
				t.Fatalf("a task that says security: true resolved without level 3: %+v", r.levels)
			}
			if !hasAdversarialStep(r.plan) {
				t.Fatalf("the plan carries no adversarial step: %+v", r.plan)
			}
			if r.rec.Security != nil {
				t.Fatalf("the run's own request was recorded as an escalation: %+v", r.rec.Security)
			}
		})
	}
}

// TestAlwaysLevelThreeRaisesEveryRun: the project layer's always_level_3 puts
// every run of the project at level 3, whatever paths it changed (SC10, D1503,
// D1517). The table is the modes that do not run level 3 by themselves, so
// every case is one the switch decided, and the run's diff here is the branch
// it was cut from — nothing on the list, which is the point: nothing is
// classified for this to hold.
func TestAlwaysLevelThreeRaisesEveryRun(t *testing.T) {
	for _, mode := range []rigor.Mode{rigor.Startup, rigor.Pro, rigor.Hotfix} {
		t.Run(string(mode), func(t *testing.T) {
			r, _ := securityRun(t, mode)
			if levelOn(r.levels, policy.LevelL3) {
				t.Fatalf("mode %s runs level 3 by itself, so the switch proves nothing", mode)
			}
			r.cfg.Layer = &config.ProjectLayer{Security: &config.LayerSecurity{AlwaysLevel3: true}}
			resolveAgain(t, r)
			if !levelOn(r.levels, policy.LevelL3) {
				t.Fatalf("a project whose layer sets always_level_3 resolved a %s run without level 3: %+v", mode, r.levels)
			}
			if !hasAdversarialStep(r.plan) {
				t.Fatalf("the plan carries no adversarial step: %+v", r.plan)
			}
			if r.rec.Security != nil {
				t.Fatalf("the project's switch was recorded as an escalation: %+v", r.rec.Security)
			}
		})
	}
}

// TestSecurityAlwaysLevelThreeRaisesARunThatTouchesNothing: the switch is not a
// classification, so a run whose diff touches no path in the list at all still
// comes out at level 3 — the case the whole setting exists for (a project that
// says everything it builds is security-sensitive, D1503), and the one an
// implementation built on the escalation would get wrong: the escalation needs
// a matched row, and this run has none to match.
func TestSecurityAlwaysLevelThreeRaisesARunThatTouchesNothing(t *testing.T) {
	r, repo := securityRun(t, rigor.Pro)
	r.cfg.Layer = &config.ProjectLayer{Security: &config.LayerSecurity{AlwaysLevel3: true}}
	resolveAgain(t, r)
	change(t, repo, map[string]string{routinePath: "routine\n"}, "work no row matches")
	r.escalateSecurity(context.Background(), runstore.SecurityAtImplement)
	if r.rec.Security != nil {
		t.Fatalf("a diff that matches no row recorded an escalation: %+v", r.rec.Security)
	}
	if !levelOn(r.levels, policy.LevelL3) {
		t.Fatalf("level 3 is off for a run the project put at level 3: %+v", r.levels)
	}
}

// TestSecurityFieldCannotLowerTheLevel: the field is a bool that only turns on,
// so `security: false` is not an exemption — it changes nothing (SC10, D1517).
// The run below asks for nothing and its diff matches a row, so the escalation
// is the only thing that can turn level 3 on and the test fails if a false is
// read as a way to ask for less.
//
// Mutation (run and restored): let `security: false` suppress the escalation
// (an early return in escalateSecurity on `!r.spec.Security`) — this case fails
// with no level and no record, which is exactly the run an exemption would
// produce.
func TestSecurityFieldCannotLowerTheLevel(t *testing.T) {
	r, repo := securityRun(t, rigor.Pro)
	r.spec.Security = false
	resolveAgain(t, r)
	if levelOn(r.levels, policy.LevelL3) {
		t.Fatal("security: false turned level 3 on, so the test proves nothing")
	}
	change(t, repo, map[string]string{listedPath: "package creds\n"}, "work on the credentials tree")
	r.escalateSecurity(context.Background(), runstore.SecurityAtImplement)
	if !levelOn(r.levels, policy.LevelL3) {
		t.Fatalf("a task that says security: false was exempt from the escalation: %+v", r.levels)
	}
	if r.rec.Security == nil || r.rec.Security.Refused {
		t.Fatalf("a task that says security: false recorded no escalation: %+v", r.rec.Security)
	}
	if !hasAdversarialStep(r.plan) {
		t.Fatalf("the plan carries no adversarial step: %+v", r.plan)
	}
}

// TestSecurityRequestIsNotHonouredByAReviewRecipe: a mode: review recipe allows
// exactly one review step and nothing else (recipe.Parse), and config's
// resolver refuses every other level of it — level 3 included — so a run whose
// request for the strongest review meets such a recipe cannot honour it. The
// run is not failed for it: the job is paid for by then, the attempt to force
// the level is what dies, and the level it asks for has nowhere to run. The
// levels the run resolved stand, and the warning says why (F9's rule for the
// escalation's own row).
//
// Both homes of the request are here, since neither is the recipe's business:
// the task's own field and the project layer's always_level_3. `fugaro review`
// is the reachable one — a review run of a project whose layer sets the switch
// — because the launcher refuses the launch first wherever it can
// (internal/cli's launchReviewLevelsAsk resolves the same force).
//
// Mutation (run and restored): drop the review-only branch from
// securityForcedLevels — the resolution refuses level 3 and this fails with
// "level 3 is required by the owner's floor for mode review", the bootstrap
// death the branch exists to prevent.
func TestSecurityRequestIsNotHonouredByAReviewRecipe(t *testing.T) {
	for _, tc := range []struct {
		name  string
		asked func(r *run)
	}{
		{"the task's field", func(r *run) { r.spec.Security = true }},
		{"the layer's always_level_3", func(r *run) {
			r.cfg.Layer = &config.ProjectLayer{Security: &config.LayerSecurity{AlwaysLevel3: true}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := securityRun(t, rigor.Pro)
			r.rcp = parseRecipe(t, "version: 1\nname: review-only\nmode: review\nsteps:\n  - review: { max_rounds: 1 }\n")
			var logs bytes.Buffer
			r.d.Log = slog.New(slog.NewTextHandler(&logs, nil))
			tc.asked(r)
			resolveAgain(t, r)
			if levelOn(r.levels, policy.LevelL3) {
				t.Fatalf("level 3 resolved on for a mode: review recipe, which allows one review step and nothing else: %+v", r.levels)
			}
			if hasAdversarialStep(r.plan) {
				t.Fatalf("the plan carries an adversarial step a review recipe has no place for: %+v", r.plan)
			}
			if want := "mode: review"; !strings.Contains(logs.String(), want) {
				t.Errorf("the dropped request was not warned about (%q absent): %s", want, logs.String())
			}
		})
	}
}

// TestSecurityRequestIsHonouredByARecipeThatKeepsTheLoop: the review-only
// tolerance above is the recipe's mode and not a general quieting of the
// request: a recipe in the implement mode is resolved with level 3 forced on,
// whatever its steps, because the level's step is one the resolver inserts
// (O7) where the recipe has none. An implementation that dropped the force
// wherever a recipe was involved would pass the test above and fail this one.
func TestSecurityRequestIsHonouredByARecipeThatKeepsTheLoop(t *testing.T) {
	r, _ := securityRun(t, rigor.Pro)
	r.rcp = parseRecipe(t, "version: 1\nname: loop\nmode: implement\nsteps:\n  - review: {}\n")
	r.spec.Security = true
	resolveAgain(t, r)
	if !levelOn(r.levels, policy.LevelL3) {
		t.Fatalf("an implement-mode recipe dropped the run's own security request: %+v", r.levels)
	}
	if !hasAdversarialStep(r.plan) {
		t.Fatalf("the plan carries no adversarial step: %+v", r.plan)
	}
}
