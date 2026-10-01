package runner_test

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/lock"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// withProject replaces the project line of the fixture's fugaro.yaml; ""
// removes it.
func withProject(t *testing.T, yaml, project string) string {
	t.Helper()
	const line = "project: aurora\n"
	if !strings.Contains(yaml, line) {
		t.Fatalf("the fixture has no %q", line)
	}
	if project == "" {
		return strings.Replace(yaml, line, "", 1)
	}
	return strings.Replace(yaml, line, "project: "+project+"\n", 1)
}

// fixtureYAML is the fixture repository's fugaro.yaml, naming aurora.
func fixtureYAML(t *testing.T) string {
	t.Helper()
	return testutil.FixtureFiles(t)["fugaro.yaml"]
}

// pushBranch pushes a branch off main whose fugaro.yaml is yaml.
func pushBranch(t *testing.T, h *harness, branch, yaml string) {
	t.Helper()
	other := filepath.Join(t.TempDir(), "other")
	testutil.Git(t, filepath.Dir(other), "clone", "--quiet", h.remote, other)
	testutil.Git(t, other, "checkout", "--quiet", "-b", branch)
	testutil.WriteFiles(t, other, map[string]string{"fugaro.yaml": yaml})
	testutil.Git(t, other, "commit", "--quiet", "--allow-empty", "-am", "project")
	testutil.Git(t, other, "push", "--quiet", "origin", "HEAD:refs/heads/"+branch)
}

func projectHarness(t *testing.T, mainYAML string) *harness {
	t.Helper()
	h := newHarness(t, withProject(t, fixtureYAML(t), mainYAML), nil)
	h.deps.Project = "aurora"
	return h
}

func setRef(t *testing.T, h *harness, spec task.Spec) {
	t.Helper()
	spec.Version, spec.RunID, spec.Repo, spec.Task = 1, runID, "acme/app", "Add a feature"
	if err := h.store.WriteTask(context.Background(), &spec); err != nil {
		t.Fatal(err)
	}
}

// refused fails if the run was not refused before anything was claimed
// remotely, with a reason containing every want.
func refused(t *testing.T, h *harness, want ...string) {
	t.Helper()
	b := withBucket(h)
	rec, err := h.run(t) // no agent step may run
	if err == nil || rec.Status != runstore.StatusInfraError || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	for _, w := range want {
		if !strings.Contains(rec.Reason, w) {
			t.Errorf("reason %q lacks %q", rec.Reason, w)
		}
	}
	if ok, _ := b.Exists(context.Background(), lock.Key("acme-app", "fugaro/"+runID)); ok {
		t.Error("a lock object was written before the project check")
	}
	if len(h.provider.State.PRs) != 0 {
		t.Error("a refused run opened a PR")
	}
}

func TestProjectCheckPasses(t *testing.T) {
	h := projectHarness(t, "aurora")
	if rec, err := h.run(t, implement("feature"), review("ship", 0)); err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

func TestProjectCheckReadsBaseNotBranch(t *testing.T) {
	h := projectHarness(t, "borealis")
	pushBranch(t, h, "feature", withProject(t, fixtureYAML(t), "aurora"))
	setRef(t, h, task.Spec{Ref: "feature"})
	refused(t, h, "project mismatch", "main", "borealis", "aurora")
}

func TestProjectCheckRefAlsoChecked(t *testing.T) {
	h := projectHarness(t, "aurora")
	pushBranch(t, h, "feature", withProject(t, fixtureYAML(t), "borealis"))
	setRef(t, h, task.Spec{Ref: "feature"})
	refused(t, h, "project mismatch", "feature", "borealis", "aurora")
}

func TestProjectCheckBaseNamesNone(t *testing.T) {
	h := projectHarness(t, "")
	pushBranch(t, h, "feature", withProject(t, fixtureYAML(t), "aurora"))
	setRef(t, h, task.Spec{Ref: "feature"})
	refused(t, h, "names no project", "main", "aurora")
}

func TestProjectCheckFollowUpUsesBase(t *testing.T) {
	for name, mainProject := range map[string]string{"another project": "borealis", "none": ""} {
		t.Run(name, func(t *testing.T) {
			h := projectHarness(t, mainProject)
			// The PR's branch says aurora; only the base counts.
			pushBranch(t, h, "fugaro/"+runID, withProject(t, fixtureYAML(t), "aurora"))
			setRef(t, h, task.Spec{Ref: "main", Branch: "fugaro/" + runID, PR: 1, PreviousRun: "20260101-000000-0000"})
			want := "borealis"
			if mainProject == "" {
				want = "names no project"
			}
			refused(t, h, "project mismatch", "main", want, "aurora")
		})
	}
}

func TestProjectCheckMissingEnvOnCloudRun(t *testing.T) {
	h := projectHarness(t, "aurora")
	h.deps.Project, h.deps.RequireProject = "", true
	refused(t, h, "job environment lacks FUGARO_PROJECT", "fugaro init --repo")
}

func TestProjectCheckSkippedLocally(t *testing.T) {
	h := projectHarness(t, "borealis")
	h.deps.Project = ""
	var logs strings.Builder
	h.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
	if rec, err := h.run(t, implement("feature"), review("ship", 0)); err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if n := strings.Count(logs.String(), "FUGARO_PROJECT is not set"); n != 1 {
		t.Errorf("%d log lines about the skipped check, want 1:\n%s", n, logs.String())
	}
}

func TestProjectCheckBeforeLock(t *testing.T) {
	h := projectHarness(t, "borealis")
	refused(t, h, "project mismatch")
}

func TestProjectCheckBaseUnparseable(t *testing.T) {
	h := projectHarness(t, "aurora")
	// main now has a field fugaro.yaml doesn't know; the branch is fine.
	pushBranch(t, h, "feature", withProject(t, fixtureYAML(t), "aurora"))
	other := filepath.Join(t.TempDir(), "other")
	testutil.Git(t, filepath.Dir(other), "clone", "--quiet", h.remote, other)
	testutil.WriteFiles(t, other, map[string]string{"fugaro.yaml": fixtureYAML(t) + "surprise: true\n"})
	testutil.Git(t, other, "commit", "--quiet", "-am", "break")
	testutil.Git(t, other, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	setRef(t, h, task.Spec{Ref: "feature"})
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// A branch whose fugaro.yaml names itself as the base must not choose
// which file decides the project: the repository's default branch does.
func TestProjectCheckBaseIsTheDefaultBranch(t *testing.T) {
	h := projectHarness(t, "borealis")
	own := strings.Replace(withProject(t, fixtureYAML(t), "aurora"), "base_branch: main", "base_branch: feature", 1)
	if own == withProject(t, fixtureYAML(t), "aurora") {
		t.Fatal("the fixture has no base_branch: main")
	}
	pushBranch(t, h, "feature", own)
	setRef(t, h, task.Spec{Ref: "feature"})
	refused(t, h, "project mismatch", "main", "borealis", "aurora")
}
