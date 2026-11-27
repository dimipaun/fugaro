package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// dead_keys_skew_test.go is DK2's half of lift L8, the module the plan calls
// dead-keys: L13's two grade models left a repository's file with the rest of
// the lift (config.LiftedKeys), and unlike every other lifted key they are
// refused rather than noted, from release A and on the default branch alone
// (R2, R11).

// deadGradeModels is a file block writing one of L13's keys, the value being
// an ordinary model ID so nothing in this block is what a test refuses.
const deadGradeModels = "model_grades:\n  strong: { model: anthropic/claude-sonnet-5.5 }\n"

// deadKeysSpec is the launch the checks below are called with: the workflow
// l8Checkout's job carries, so the checkout is the one the gate reads.
func deadKeysSpec() *task.Spec {
	return &task.Spec{Version: 1, RunID: "20261124-100000-abcd", Repo: "acme/other",
		Ref: "main", Workflow: "svc", Task: "x"}
}

// hasMessage is has for a problem with no Path of its own: C1's sentence names
// the key it is about, so the resolution's lines carry none (LiftedKey.Why).
func hasMessage(ps []config.Problem, sub string) bool {
	for _, p := range ps {
		if strings.Contains(p.Message, sub) {
			return true
		}
	}
	return false
}

// TestModelGradesSkewReadsTheLayer is the first half of DK2's acceptance: the
// grade keys have one home — the project layer's defaults.model_grades — so a
// repository's own model_grades is no skew trigger at all. A file that writes
// the two grades and nothing else asks an older image for nothing and is
// printed no note about them, and one that writes them beside a lifted key
// still trips the gate on that key: the grades are dropped from the trigger's
// own read and not from the read itself.
//
// What the file's grades are is a refusal instead, on the default branch alone
// (TestDefaultBranchRefusesModelGrades below): the merge has dropped a
// repository's grade since G3, so no run reads the line and no image is owed
// anything for it.
func TestModelGradesSkewReadsTheLayer(t *testing.T) {
	for _, c := range []struct {
		name string
		// agent is the file's own agent block, which is what the gate's other
		// trigger is read from.
		agent string
		// trigger is whether the file is expected to ask for an image at all.
		trigger bool
	}{
		{"the grades alone", "agent: { auth: api-key }\n", false},
		{"beside a lifted key", "agent: { auth: api-key, models: { background: claude-haiku-4-5 } }\n", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newCloudFixture(t)
			l8Checkout(t, f, map[string]string{"fugaro.yaml": l8Head + c.agent + deadGradeModels + l8Workflow})
			var warn bytes.Buffer
			sk, ok := l8LaunchSkew(context.Background(), fileEnv(t, f), deadKeysSpec(), &warn)
			if ok != c.trigger {
				t.Fatalf("l8LaunchSkew = %+v, %v, want trigger %v", sk, ok, c.trigger)
			}
			if n := strings.Count(warn.String(), "model_grades."); n != 0 {
				t.Errorf("the launch named the file's grades %d times, want none: they ask no image for anything:\n%s", n, warn.String())
			}
			if !c.trigger {
				return
			}
			if sk.Since != l8Since || !strings.Contains(sk.Subject, "agent.models.background") {
				t.Errorf("l8LaunchSkew = %+v, want %s's trigger on the file's other lifted key", sk, l8Since)
			}
		})
	}
}

// TestDefaultBranchRefusesModelGrades is the rest of DK2's acceptance (R2,
// R11): a repository's file that still writes one of L13's grade models is
// refused on the default branch — at a launch whatever the job image, and at
// `fugaro validate` — with the sentence the key's own row carries, and every
// other ref keeps the drop and the note the lift has made since G3, so the
// same file on a branch is named and not refused.
func TestDefaultBranchRefusesModelGrades(t *testing.T) {
	const want = "model_grades.strong.model is the project's (D3237)"
	const fix = "remove it from fugaro.yaml"
	const file = l8Head + "agent: { auth: api-key }\n" + deadGradeModels + l8Workflow

	t.Run("a launch", func(t *testing.T) {
		// Whatever the image: the key is refused before any image is asked
		// what it knows, because no runner's reading of it decides anything.
		for _, rel := range []string{shiftRelease(t, l8Since, -1), shiftRelease(t, l8Since, 1)} {
			t.Run(rel, func(t *testing.T) {
				f := newCloudFixture(t)
				l8Checkout(t, f, map[string]string{"fugaro.yaml": file})
				writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", l8Base(rel))
				_, _, err := execute(t, "run", "--run-id", "20261124-100000-abcd", "A task")
				if ExitCode(err) != ExitUserError {
					t.Fatalf("err = %v, want the launch refused", err)
				}
				for _, w := range []string{want, fix, "defaults.model_grades.<grade>.model", "the default branch of acme/other"} {
					if !strings.Contains(err.Error(), w) {
						t.Errorf("%v lacks %q", err, w)
					}
				}
				for _, no := range []string{"needs a job image", "fugaro update --base", l8Since} {
					if strings.Contains(err.Error(), no) {
						t.Errorf("the refusal is the image gate's, and no image is owed the line: %v", err)
					}
				}
				if n := len(f.run.Executions()); n != 0 {
					t.Errorf("%d executions, want none", n)
				}
			})
		}
	})

	t.Run("a launch outside a checkout", func(t *testing.T) {
		// The gate reads the default branch's file, which a launch from
		// outside a checkout has not got: what the task carries is all it can
		// be judged on, so this refuses nothing (every other trigger of the
		// gate reads the same way).
		f := newCloudFixture(t)
		isolateCache(t)
		writeBuildRecord(t, f, appSlug, "web", l8Base(shiftRelease(t, l8Since, 1)))
		_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261124-100000-abcd", "A task")
		if err != nil {
			t.Fatalf("err = %v, want the launch: the grades gate reads a checkout", err)
		}
	})

	t.Run("validate on the default branch", func(t *testing.T) {
		f := newCloudFixture(t)
		isolateCache(t)
		isolateProjects(t, t.TempDir())
		l8Checkout(t, f, map[string]string{"fugaro.yaml": file})
		// The working branch is what validate judges: l8Checkout's own init
		// leaves whatever git's default is, so the checkout is put on the
		// name its default branch's remote-tracking ref carries.
		testutil.Git(t, ".", "branch", "-M", "main")
		doc, err := validateHere(t)
		if ExitCode(err) != ExitUserError {
			t.Fatalf("err = %v, want the file refused", err)
		}
		if !hasMessage(doc.Problems, want) || !hasMessage(doc.Problems, fix) {
			t.Errorf("problems = %+v, want the key's own sentence", doc.Problems)
		}
		if hasMessage(doc.Notes, "model_grades.strong.model") {
			t.Errorf("notes = %+v, want the key refused and not noted", doc.Notes)
		}
	})

	t.Run("another branch only names it", func(t *testing.T) {
		f := newCloudFixture(t)
		isolateCache(t)
		isolateProjects(t, t.TempDir())
		l8Checkout(t, f, map[string]string{"fugaro.yaml": file})
		testutil.Git(t, ".", "branch", "-M", "main")
		testutil.Git(t, ".", "switch", "-c", "feature")
		doc, err := validateHere(t)
		if err != nil {
			t.Fatalf("err = %v, want the file valid: another ref keeps the drop and the note", err)
		}
		if hasMessage(doc.Problems, "model_grades.strong.model") {
			t.Errorf("problems = %+v, want none: only the default branch refuses the key", doc.Problems)
		}
		if !hasMessage(doc.Notes, want) || !hasMessage(doc.Notes, fix) {
			t.Errorf("notes = %+v, want the key named on a branch", doc.Notes)
		}
	})

	t.Run("validate a file that is no checkout's", func(t *testing.T) {
		// The same answer doctor gives: no branch is judged, so the key is
		// named and nothing is refused.
		doc, err := validateGrades(t, deadGradeModels)
		if err != nil {
			t.Fatalf("err = %v, want the file valid", err)
		}
		if hasMessage(doc.Problems, "model_grades.strong.model") {
			t.Errorf("problems = %+v, want none", doc.Problems)
		}
		if !hasMessage(doc.Notes, want) {
			t.Errorf("notes = %+v, want the key named", doc.Notes)
		}
	})
}

// validateHere runs `fugaro validate --json` on the working directory's own
// fugaro.yaml and parses what it printed: the checkout is the subject, which is
// what decides whether the file is the default branch's own.
func validateHere(t *testing.T) (validateOutput, error) {
	t.Helper()
	out, _, err := execute(t, "validate", "--json")
	var doc validateOutput
	if jerr := json.Unmarshal([]byte(out), &doc); jerr != nil {
		t.Fatalf("output %q: %v", out, jerr)
	}
	return doc, err
}
