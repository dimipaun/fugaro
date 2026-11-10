package cli

// report_image_test.go is CL6: `fugaro image refresh` and `fugaro image build`
// print their steps through the reporter (D1354) and keep the run in a log
// under their own command name (D1352).
//
// What the refresh owes is what this file pins: one line per step in the
// default view, the run's own lines kept and shown again under --verbose, the
// stop sentence the plugin skill quotes (states.md) kept as the error's own,
// and the full output in the log a failure points at.
//
// This file is the batch's red step: on the tree before the move the command
// prints a "step N, name:" header with the step's own lines under it, keeps no
// log at all (onlyLog finds no directory to read) and ends with its own "is
// done" line rather than the reporter's closing one.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/infra"
)

// refreshWithTheRealCheckJob runs the standalone command over a rig whose third
// step is the real one: the base step and the builds are stubbed (they need the
// cloud), while the daily image check job's own line — the stage line the
// default view stops showing — is the stage's, printed through the run's own
// output.
func refreshWithTheRealCheckJob(t *testing.T, args ...string) (string, error) {
	t.Helper()
	useVersion(t, "0.5.1")
	useSelf(t)
	r := newAnchorModeRig(t, refreshYAML, true)
	fakeTerminal(t)
	slug, label := checkJobOwner(t, "github", "acme/app")
	oldRef, newRef := managedRef("base", "0.4.0"), managedRef("base", "0.5.1")
	spec, _ := json.Marshal(infra.CheckJobSpec{Repo: "acme/app", BaseImages: map[string]string{"base": oldRef}})
	r.run.SetJob(gcp.CheckJobName(slug), map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRole: gcp.RoleCheck, gcp.LabelRepo: label}, oldRef)
	r.run.SetJobEnv(gcp.CheckJobName(slug), map[string]string{infra.CheckSpecEnv: string(spec)})
	old := newRefreshSteps
	newRefreshSteps = func(rr *initRun, e *initEngine, p *refreshPlan) refreshSteps {
		return refreshSteps{
			base: func(context.Context) error { return nil },
			reload: func(context.Context) (*refreshTarget, error) {
				lc := *p.lc
				lc.BaseImages = map[string]string{"base": newRef}
				return &refreshTarget{lc: &lc, cfg: p.cfg, kinds: []string{"base"},
					spec: infra.RepoSpec{Name: "acme/app", Slug: slug, Label: label}}, nil
			},
			checkJob: func(ctx context.Context, tg *refreshTarget) error { return rr.refreshCheckJob(ctx, tg) },
			builds:   func(context.Context, *refreshTarget) error { return nil },
		}
	}
	t.Cleanup(func() { newRefreshSteps = old })
	out, _, err := executeStdin(t, "", append([]string{"image", "refresh", "--workflow", "app"}, args...)...)
	return out, err
}

// TestImageRefreshDefaultIsOneLinePerStep: the default view is the plan, one
// line per step and the closing line (D1354). The three "step N, name:" headers
// are gone and each stage's own lines are the run's own output — kept in the
// log (D1352) and shown again under --verbose — instead of the screen's account
// of the step.
func TestImageRefreshDefaultIsOneLinePerStep(t *testing.T) {
	out, err := refreshWithTheRealCheckJob(t)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	reported(t, out, "base", "check job", "builds")
	// One line per step and no more: stepName reads a line's leading token, so
	// three lines are three steps.
	if got := strings.Join(stepNames(out), ","); got != "base,check,builds" {
		t.Errorf("the step lines are %q, want one line per step (base, check job, builds):\n%s", got, out)
	}
	for _, gone := range []string{
		"step 2, base:", "step 3, the daily image check job:", "step 4, builds:",
		"updated the daily image check job",
	} {
		if strings.Contains(out, gone) {
			t.Errorf("the default view still prints %q:\n%s", gone, out)
		}
	}
	// The plan is still printed first, and the reporter's closing line is the
	// last one: the command's own "is done" is what it replaces.
	if i, j := strings.Index(out, "fugaro image refresh of acme/app ("), strings.Index(out, markDone+" base"); i < 0 || j < 0 || i > j {
		t.Errorf("the plan is not printed before the steps (plan at %d, steps at %d):\n%s", i, j, out)
	}
	if strings.Contains(out, "is done") {
		t.Errorf("the run still ends with its own done line rather than the reporter's closing one:\n%s", out)
	}
}

// TestImageRefreshVerboseUnchanged: --verbose is today's view — the stream the
// default view moved into the log reaches the terminal as well — and the steps
// are the same ones (D1354, G10).
func TestImageRefreshVerboseUnchanged(t *testing.T) {
	out, err := refreshWithTheRealCheckJob(t, "--verbose")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "updated the daily image check job") {
		t.Errorf("--verbose does not show the run's own lines:\n%s", out)
	}
	reported(t, out, "base", "check job", "builds")
}

// TestImageRefreshStoppedAtStepKeepsItsSentence: the stop message is the
// plugin skill's (states.md quotes `stopped at step N`, and its own reason
// names the steps that finished and the rerun line), so the reporter's account
// is added beside it and never in place of it: the step that failed is the
// reporter's line, the sentence is the command's error, and the run ends
// failed.
func TestImageRefreshStoppedAtStepKeepsItsSentence(t *testing.T) {
	useVersion(t, "0.5.1")
	useSelf(t)
	newAnchorModeRig(t, refreshYAML, true)
	fakeTerminal(t)
	ran := fakeSteps(t, "builds", remote(errors.New("503 from the cloud")))
	out, _, err := executeStdin(t, "", "image", "refresh", "--workflow", "app")
	if ExitCode(err) != ExitRemoteError {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	const sentence = "fugaro image refresh stopped at step 4 (builds), steps finished: preflight, base, check job; " +
		"once that is fixed, rerun fugaro image refresh --workflow app in this checkout: finished steps say No changes"
	if err == nil || !strings.Contains(err.Error(), "503 from the cloud; "+sentence) {
		t.Errorf("the stop sentence changed or was wrapped: %v", err)
	}
	if got := strings.Join(*ran, ","); got != "base,check job,builds" {
		t.Errorf("ran %s, want base,check job,builds: a step after the stop ran, or one before it did not", got)
	}
	if !strings.Contains(out, markFailed+" builds: 503 from the cloud") {
		t.Errorf("the step that failed is not the reporter's:\n%s", out)
	}
	// G8: the failure prints the log's path, and the run closes on the failure
	// rather than on a done.
	if !strings.Contains(out, "full output: ") {
		t.Errorf("the failed run points at no log:\n%s", out)
	}
	if lines := reportLines(out); !strings.HasPrefix(lines[len(lines)-1], "Failed in ") {
		t.Errorf("the run's last line is not the reporter's failure:\n%s", out)
	}
}

// TestImageRefreshWritesALog: the run keeps a log under the command's own name
// (D1352), and the log is the run's full output — the stage line the default
// view stopped showing is in it.
func TestImageRefreshWritesALog(t *testing.T) {
	out, err := refreshWithTheRealCheckJob(t)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	log := writesALog(t, "image-refresh")
	if !strings.Contains(log, "updated the daily image check job") {
		t.Errorf("the log does not hold the stage's own line:\n%s", log)
	}
}

// TestImageBuildWritesALog: `fugaro image build` keeps a log too (D1352), one
// step for the image it built, and what was the run's own progress line — the
// Cloud Build submission, which names the build's log — is in that log rather
// than on the screen.
func TestImageBuildWritesALog(t *testing.T) {
	cloudBuildCheckout(t, false)
	out, _, err := executeBuild(t, "image", "build", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	reported(t, out, "app")
	log := writesALog(t, "image-build")
	for _, want := range []string{"submitted; log:", "built "} {
		if !strings.Contains(log, want) {
			t.Errorf("the log holds no %q line:\n%s", want, log)
		}
	}
	if strings.Contains(out, "submitted; log:") {
		t.Errorf("the default view still prints the run's own progress line:\n%s", out)
	}
}
