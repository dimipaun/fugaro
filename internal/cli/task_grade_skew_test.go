package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/escalation"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// task_grade_skew_test.go is E4's half of the plan's second Convention: a task
// field a strict decoder refuses on an older runner needs its "Needs fugaro
// X.Y.Z" gate in the release that adds it. The two fields are task.Spec's own
// model_grade and the importance a follow-up carries inside follow_up_of, and
// both are judged from the task rather than from a file the run will read, which
// is followUpSince's shape and not modelGradesSince's.

// TestTaskGradeSinceIsTheReleaseTheFieldsShipIn pins taskGradeSince to the
// release modelGradesSince names, which is the invariant the constant's comment
// rests on: a task that names a grade resolves it through the project layer's
// defaults.model_grades, so the release whose task reader knows the field is the
// release whose config reader knows the key it resolves through. A release that
// moved one without the other would either launch runs that die at bootstrap on
// a grade the project has no way to set, or hold back runs an image could run.
func TestTaskGradeSinceIsTheReleaseTheFieldsShipIn(t *testing.T) {
	if taskGradeSince != modelGradesSince {
		t.Errorf("taskGradeSince = %q and modelGradesSince = %q: a task's model grade resolves through the project layer's grades, so the field and the key it resolves through ship in one release", taskGradeSince, modelGradesSince)
	}
}

// TestTaskGradeSinceHasAReleaseBullet: taskGradeSince names a release that is
// not tagged, so docs/release.md's Before-you-tag list is what holds the
// constant to the release actually cut — the job every other *Since constant's
// bullet does.
func TestTaskGradeSinceHasAReleaseBullet(t *testing.T) {
	var bullet string
	for _, line := range strings.Split(readDoc(t, "docs/release.md"), "\n") {
		if strings.HasPrefix(line, "- **Verify `taskGradeSince`") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatal("docs/release.md's Before-you-tag list has no taskGradeSince bullet: the checklist is where a release verifies the constant")
	}
	if !strings.Contains(bullet, "currently `\""+taskGradeSince+"\"`") {
		t.Errorf("docs/release.md's taskGradeSince bullet does not carry the constant's release (%q)", taskGradeSince)
	}
	// Both fields, both gates and the two directions a wrong value fails in: a
	// bullet that named the grade alone would have a release verify the constant
	// against an image whose runner reads a task's model_grade and refuses the
	// importance a filed follow-up carries.
	for _, want := range []string{"model_grade", "follow_up_of", "importance", "checkTaskGradeImage", "checkCloneTaskGradeImage", "modelGradesSince", "not tagged yet"} {
		if !strings.Contains(bullet, want) {
			t.Errorf("docs/release.md's taskGradeSince bullet does not say %q", want)
		}
	}
}

// TestCheckTaskGradeImage is taskGradeSince's gate for a built job image and for
// a checkout: clone workflow: either field the task carries raises the image the
// launch needs, a task carrying neither is never gated, and the boundary is the
// constant's own — one release below is refused, the constant and one above
// pass.
func TestCheckTaskGradeImage(t *testing.T) {
	f := newCloudFixture(t)
	env := fileEnv(t, f)
	below, above := shiftRelease(t, taskGradeSince, -1), shiftRelease(t, taskGradeSince, 1)
	spec := func(grade escalation.Grade, importance task.Importance) *task.Spec {
		s := &task.Spec{Version: 1, RunID: "20261007-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x", ModelGrade: grade}
		if importance != "" {
			s.FollowUpOf = &task.FollowUpOf{Run: "20261006-090000-1234", Keys: []string{"9f2a1c"}, Importance: importance}
		}
		return s
	}
	check := func(imageRef string, s *task.Spec, clone bool) (string, error) {
		var warn bytes.Buffer
		if clone {
			lc := *env.lc
			lc.BaseImages = map[string]string{"base": imageRef}
			err := checkCloneTaskGradeImage(&lc, s, &warn)
			return warn.String(), err
		}
		writeBuildRecord(t, f, appSlug, "web", imageRef)
		err := checkTaskGradeImage(context.Background(), env, appSlug, s, "base", &warn)
		return warn.String(), err
	}
	// image is the base image a release names, which is what both gates judge:
	// the release number alone is not a base image and cannot be dated.
	image := func(rel string) string { return "ghcr.io/dimipaun/fugaro-base:" + rel }
	for _, c := range []struct {
		name     string
		spec     *task.Spec
		ref      string
		refusals []string
		refused  bool
	}{
		{name: "a model grade", spec: spec(escalation.Strong, ""), ref: image(below), refused: true,
			refusals: []string{"a task carrying a model grade needs a job image whose runner knows a task's model_grade (fugaro " + taskGradeSince + " or later)", "acme/app workflow web", "release " + below}},
		{name: "an importance", spec: spec("", task.ImportanceImportant), ref: image(below), refused: true,
			refusals: []string{"a task carrying a follow-up importance needs a job image whose runner knows a task's follow_up_of.importance", "release " + below}},
		{name: "both", spec: spec(escalation.Strongest, task.ImportanceMinor), ref: image(below), refused: true,
			refusals: []string{"a task carrying a model grade and a follow-up importance needs a job image whose runner knows a task's model_grade and follow_up_of.importance"}},
		{name: "neither", spec: spec("", ""), ref: image(below), refused: false},
		{name: "the constant itself", spec: spec(escalation.Strong, ""), ref: image(taskGradeSince), refused: false},
		{name: "a newer release", spec: spec(escalation.Strong, ""), ref: image(above), refused: false},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, clone := range []bool{false, true} {
				warn, err := check(c.ref, c.spec, clone)
				if !c.refused {
					if err != nil {
						t.Fatalf("clone = %v, release %s: err = %v, want the launch admitted", clone, c.ref, err)
					}
					if warn != "" {
						t.Errorf("clone = %v, release %s: warning %q, want none", clone, c.ref, warn)
					}
					continue
				}
				if ExitCode(err) != ExitUserError {
					t.Fatalf("clone = %v, release %s: err = %v, want the launch refused", clone, c.ref, err)
				}
				wants := append(c.refusals, c.ref)
				if !clone {
					// The built image's remedy is the other half of the pair:
					// a checkout: clone workflow's base image is moved by
					// fugaro update --base base, which its own refusal names.
					wants = append(wants, imageGateRemedy)
				}
				for _, want := range wants {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("clone = %v, release %s: %v lacks %q", clone, c.ref, err, want)
					}
				}
			}
		})
	}
	// A base image that is not a release cannot be dated, so the gate warns and
	// lets the launch through, the shape every other *Since gate has.
	s, err := check("us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-1a2b3c", spec(escalation.Strong, ""), false)
	if err != nil {
		t.Fatalf("dev base: %v", err)
	}
	if !strings.Contains(s, "not a release base image") {
		t.Errorf("dev base: warning %q, want the not-a-release note", s)
	}
}

// TestRunRefusesATaskGradeOnAnOlderImage is taskGradeSince's gate where it
// matters: wired into the launch. A task is not written with a grade by any
// flag — the ladder's own launches file one (E10a) — so the launch that reaches
// this gate is a --retry replaying a stored task, which is the path that would
// otherwise put a spec on a job whose runner refuses it at bootstrap.
//
// Mutation (verified, run and restored): drop checkTaskGradeImage from
// freshLaunchGates — this fails, because the retry launches on the image below.
func TestRunRefusesATaskGradeOnAnOlderImage(t *testing.T) {
	f := newCloudFixture(t)
	t.Chdir(t.TempDir())
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, taskGradeSince, -1))
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	spec := &task.Spec{Version: 1, RunID: "20261112-120000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web",
		Task: "x", ModelGrade: escalation.Strong}
	if err := runstore.Open(b, appSlug, spec.RunID).CreateTask(ctx, spec); err != nil {
		t.Fatal(err)
	}
	wantRefused(t, f, "a task carrying a model grade needs a job image whose runner knows a task's model_grade (fugaro "+taskGradeSince+" or later)",
		"run", "--retry", spec.RunID)
	// At the constant the same retry launches, so the gate refuses the release
	// below and not the field.
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+taskGradeSince)
	if _, _, err := execute(t, "run", "--retry", spec.RunID); err != nil {
		t.Fatalf("a task grade on release %s: %v", taskGradeSince, err)
	}
}
