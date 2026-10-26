package runview

// The plan task id is display text and nothing else.
//
// Row.PlanTask is a join hint written by whoever launched the run, so it is
// attacker-controlled for anyone who can write a launch.json (a launcher,
// and on a shared job every run: SECURITY.md, D4). The plan it names is
// never read from the run, the id authorises nothing, admits nothing and
// budgets nothing (the console dependency graph plan's decision GD5 and its
// constraint C3): it is how a row is matched to a node of a plan graph the
// operator holds, no more. The only way to keep that true as the code grows
// is to fail a test the moment some other package starts reading it.
//
// This is that test, in the shape of launched_by_test.go beside it: a
// whole-module scan with an allow-list, because a rule that named the
// packages it knows about would pass the day someone adds a new one, which
// is exactly the day it would matter.

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// planTaskNaming matches the field's two spellings as words: the Go name
// alone, so that a longer identifier built on it (planTaskSince,
// PlanTaskProblem, PlanTaskElemRE) is not mistaken for a read of the field
// itself, and the JSON key.
var planTaskNaming = regexp.MustCompile(`\bPlanTask\b|plan_task`)

// planTaskReaders is the allow-list: a non-test Go file that may name
// PlanTask or the plan_task key, and why. Anything else that names it fails
// TestPlanTaskIsDisplayTextOnly.
var planTaskReaders = map[string]string{
	// The field's own definition and its shape rule.
	"internal/task/task.go": "defines task.Spec.PlanTask and PlanTaskProblem, the shape the launcher applies on write and task.Parse on read",
	// The launch record it is read from, which is the untrusted object.
	"internal/runstore/launch.go": "defines runstore.Launch.PlanTask, the launch.json field itself",
	// The one writer of both records: fugaro run stores the id in the task
	// (so --retry and the recovery path keep it) and in launch.json, and
	// gates the launch on the image whose runner can read it.
	"internal/cli/run.go": "writes the id into task.json and launch.json at launch, and gates the launch on the image that reads it",
	// A follow-up's spec carries its own --plan-task, never the followed
	// run's.
	"internal/cli/followup.go": "writes a follow-up's own --plan-task into its task",
	// Only the version floor's comment names the field: the constant beside
	// this file's allow-list value is what a launch is gated on.
	"internal/cli/recipes_skew.go": "planTaskSince, the release floor the launcher's gate judges an image by",
	// The documents that show it, and the reads that feed them.
	"internal/runview/runview.go":  "the runs ls / diagnose row's own field, sanitised from the launch record",
	"internal/watch/build.go":      "the watch row's own field",
	"internal/watch/facts.go":      "stamps the id a scan read onto the rows a frame draws",
	"internal/watch/json.go":       "the watch --json key",
	"internal/cli/watch_queued.go": "reads launch.json for the rows the watch scan produces, and carries the fact into the frame",
	// The graph joined to the runs (GV1.6): the join matches a plan's task
	// ids against the id the row already carries, and both sides are display
	// only -- the id decides nothing, and the state it produces is a word a
	// screen draws.
	"internal/plan/state.go":      "joins the id the row published to a plan's task, for the states a screen draws",
	"internal/cli/plan_status.go": "turns the read's own sanitised rows into the facts the join reads; it never opens launch.json",
}

// TestPlanTaskIsDisplayTextOnly walks every non-test Go file of the module
// and fails on any that names the plan task outside the allow-list above: a
// reader that is not the task, the launch record, the launcher, the row or a
// renderer is a reader that could be a decision.
func TestPlanTaskIsDisplayTextOnly(t *testing.T) {
	root := filepath.Join("..", "..") // internal/runview -> the module root
	var scanned int
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "testdata":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		if !planTaskNaming.Match(src) {
			return nil
		}
		if _, ok := planTaskReaders[rel]; !ok {
			t.Errorf("%s names the plan task (PlanTask/plan_task), but it is not on the display allow-list in %s: "+
				"the id is untrusted text written by whoever launched the run, and nothing may decide anything by it",
				rel, "internal/runview/plan_task_test.go")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the module: %v", err)
	}
	// A walk that read nothing would pass silently: pin that it read the tree.
	if scanned < 100 {
		t.Fatalf("only %d Go files were scanned; the walk is looking in the wrong place", scanned)
	}
	// And the other direction: an allow-list entry for a file that no longer
	// names the plan task is a stale permission, which is how an allow-list
	// quietly grows to cover code that should not be on it.
	for rel := range planTaskReaders {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("allow-list names %s: %v", rel, err)
			continue
		}
		if !planTaskNaming.Match(b) {
			t.Errorf("allow-list still names %s, which no longer mentions the plan task", rel)
		}
	}
	if len(planTaskReaders) == 0 {
		t.Fatal("the allow-list is empty")
	}
}

// TestPlanTaskAllowListNamesTheRealField keeps the allow-list from drifting
// away from the field it is about: the scan above looks for the Go name
// PlanTask, so a rename of the JSON key alone (which every reader of
// plan_task would notice, and this scan would not) is caught here.
func TestPlanTaskAllowListNamesTheRealField(t *testing.T) {
	f, ok := reflect.TypeOf(Row{}).FieldByName("PlanTask")
	if !ok {
		t.Fatal("runview.Row has no PlanTask field: the plan task is not on the row at all")
	}
	if got := strings.Split(f.Tag.Get("json"), ",")[0]; got != "plan_task" {
		t.Fatalf("the row's plan task key is %q, not plan_task: the documents that read the key, and the allow-list scan's name, are out of step", got)
	}
}
