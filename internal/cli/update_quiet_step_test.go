package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// update_quiet_step_test.go is the owner's run of 2026-11-27/28, which read
// three things printed for steps that waited for no answer: a "⚠ CONFIRM"
// banner, a "⚠ Review these first" block, and the sentence saying nothing is
// typed. The rule is CLAUDE.md's ("Fugaro asks for nothing", "Be laconic"): a
// step that needs no answer prints one plain line saying what it did, with no
// CONFIRM and no warning mark.
//
// The decision is one place — initRun.asksNothing and the gate branch the
// printers share — so each test below asserts the same fact at the printer it
// reaches: a managed project's own terminal asks nothing, and a step that asks
// nothing may not read as a question. The other half of every case is the world
// where the step IS a question, or is refused: there the wording is unchanged,
// which is what keeps the fix from taking a confirmation away.

// TestAStageWriteNothingIsAskedAboutPrintsNoConfirm is the policy stage's and
// the layer stage's write on a managed project at the owner's own terminal: the
// gate stands in, nothing is answered, and the step reads as the run's own
// record of the write — one plain line naming it — instead of a banner no flag
// and no person answers.
func TestAStageWriteNothingIsAskedAboutPrintsNoConfirm(t *testing.T) {
	for _, tc := range []struct {
		name    string
		managed bool
		ask     bool
		// banner is whether this world still reads the step as a question.
		banner bool
	}{
		{"the gate stands in", true, false, false},
		{"--ask put the question back", true, true, true},
		{"a project that is not managed", false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, e, out, _ := managedTerminalUpdate(t, tc.managed, true)
			e.r.o.ask = tc.ask
			e.r.cmd.SetOut(out)

			const what = "this rewrites the published project layer from your local layer file"
			err := confirmStageWrite(e.r.cmd, &localcfg.Config{Name: initProjectName}, e.r.conditions(), e.r.gateStandsIn(), false, what)
			if err != nil {
				t.Fatalf("the write of a managed project's own terminal was refused: %v\n%s", err, out)
			}
			got := out.String()
			if strings.Contains(got, "⚠ CONFIRM") != tc.banner {
				t.Errorf("⚠ CONFIRM printed = %v, want %v:\n%s", !tc.banner, tc.banner, got)
			}
			if !strings.Contains(got, what) {
				t.Errorf("the step does not name what it writes:\n%s", got)
			}
			// A step nothing is asked about carries no warning mark and no
			// managed sentence either: there is no confirmation to account for.
			if !tc.banner {
				if strings.Contains(got, "⚠ Review these first") || strings.Contains(got, initflow.OwnerProceeded()) {
					t.Errorf("a step nothing is asked about carried a warning or the managed sentence:\n%s", got)
				}
				if strings.Contains(got, "Type ") || strings.Contains(got, "[y/N]") {
					t.Errorf("a step nothing is asked about put a question:\n%s", got)
				}
			}
		})
	}
}

// TestAPlanNobodyConfirmsAsksForNoReview is the plan view the owner read on the
// same run: the block's own heading is an instruction to a person about to
// answer ("review these first"), so a plan the gate takes carries the counts
// alone and keeps the review lines in the log with the rest of the run's
// account. A plan a person really answers keeps the block.
func TestAPlanNobodyConfirmsAsksForNoReview(t *testing.T) {
	plan := &tf.Plan{ResourceChanges: []tf.ResourceChange{{
		Address: "module.installation.google_cloud_run_v2_job.catalog_check", Mode: "managed",
		Type: "google_cloud_run_v2_job", ProviderName: "registry.terraform.io/hashicorp/google",
		Change: tf.Change{
			Actions: []string{"update"},
			Before:  map[string]any{"template": []any{map[string]any{"template": []any{map[string]any{"containers": []any{map[string]any{"image": "old"}}}}}}},
			After:   map[string]any{"template": []any{map[string]any{"template": []any{map[string]any{"containers": []any{map[string]any{"image": "new"}}}}}}},
		},
	}}}
	if !strings.Contains(tf.Brief(plan), "⚠ Review these first:") {
		t.Fatal("the fixture's plan asks for no review, so this test proves nothing")
	}
	for _, tc := range []struct {
		name    string
		managed bool
		want    bool
	}{
		{"the gate stands in", true, false},
		{"not managed: the person answers it", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, e, out, _ := managedTerminalUpdate(t, tc.managed, true)
			e.r.planBlock(plan)
			got := out.String()
			if strings.Contains(got, "⚠ Review these first:") != tc.want {
				t.Errorf("the review block printed = %v, want %v:\n%s", !tc.want, tc.want, got)
			}
			if !strings.Contains(got, "Plan: 1 to update.") {
				t.Errorf("the screen does not say what the plan holds:\n%s", got)
			}
		})
	}
}

// TestAMoneyStepNobodyIsAskedAboutPrintsNoConfirm is the same defect at the
// steps that are money or permanent (askClass): a managed project's own
// terminal answers them by being there, so what it reads is the step's note and
// not a banner nothing follows.
func TestAMoneyStepNobodyIsAskedAboutPrintsNoConfirm(t *testing.T) {
	_, e, out, _ := managedTerminalUpdate(t, true, true)
	ok, reachable, err := e.r.askCovered("builds the first image of app")
	if err != nil || !ok || !reachable {
		t.Fatalf("a managed project's own terminal did not take the step: %v %v %v\n%s", ok, reachable, err, out)
	}
	got := out.String()
	if strings.Contains(got, "⚠ CONFIRM") {
		t.Errorf("a step nothing is asked about printed the confirmation banner:\n%s", got)
	}
	if !strings.Contains(got, "builds the first image of app") {
		t.Errorf("the step's own note does not name it:\n%s", got)
	}
}
