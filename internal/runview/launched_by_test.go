package runview

// The launcher identity is display text and nothing else.
//
// Row.LaunchedBy is written by whoever launched the run, so it is attacker-
// controlled for anyone who can write a launch.json (a launcher, and on a
// shared job every run: SECURITY.md, D4). It is therefore never an input to
// any decision — no policy, no admission, no budget rule, no grant, no
// sweep — and the only way to keep that true as the code grows is to fail a
// test the moment some other package starts reading it.
//
// This is that test. It is deliberately a whole-module scan with an
// allow-list rather than a list of "policy packages": a rule that named the
// packages it knows about would pass the day someone adds a new one, which
// is exactly the day it would matter. The list below is every file that may
// name the field, each with the one reason it may.

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// launchedByReaders is the allow-list: a non-test Go file that may name
// LaunchedBy or the launched_by key, and why. Anything else that names it
// fails TestLaunchedByIsDisplayTextOnly.
var launchedByReaders = map[string]string{
	// The field's own definition, and the launch record it is read from.
	"internal/runstore/launch.go": "defines runstore.Launch.LaunchedBy, the launch.json field itself",
	// The one writer: fugaro run records the identity it launched as.
	"internal/cli/run.go": "writes the launcher into launch.json at launch",
	// The two documents that show it, and the read that feeds them.
	"internal/runview/runview.go":  "the runs ls / diagnose row's own field",
	"internal/watch/build.go":      "the watch row's own field",
	"internal/watch/queued.go":     "a queued row carries the launcher its launch.json recorded",
	"internal/watch/finished.go":   "a finished row carries the launcher its launch.json recorded",
	"internal/watch/json.go":       "the watch --json key",
	"internal/watch/facts.go":      "stamps the launcher a scan read onto the rows a frame draws",
	"internal/cli/watch_queued.go": "reads launch.json for the rows the watch scan produces",
	"internal/cli/runs.go":         "the runs ls text column",
	// runs stats groups by the launcher and prints it: the group key IS the
	// identity, sanitised and capped by internal/userstats before it reaches a
	// row, and read for nothing but the row it names. The view the caller is
	// shown is decided by the local config's own lists (internal/cli/roles.go),
	// never by this text.
	"internal/cli/runs_stats.go": "the runs stats by-person group key, sanitised display text",
	"internal/watch/plain.go":    "the watch text column",
	"internal/watch/render.go":   "the interactive screen's expanded run detail",
	// The coordinator's view (plan P1), which moved to ee/ with its command
	// (ee-reorg E2.1b). The launcher is the ONE thing the view says about who
	// did the work, and it says it as what it is: the string the run's own
	// launch record wrote about itself, carried as declared_by, sanitised
	// before it reaches a cell, and read for nothing but the row it names.
	// Nothing in ee/coordinator decides anything by it -- the view is advisory
	// (spec §3) and holds no rule that could -- and its own test refuses an
	// identity-shaped name for the field (ee/coordinator/view_test.go).
	"ee/coordinator/view.go":            "the view's declared_by field: the launcher as a declaration, never an identity",
	"ee/coordinator/cli/coordinator.go": "reads launch.json for the view's DECLARED BY column",
}

// TestLaunchedByIsDisplayTextOnly walks every non-test Go file of the module
// and fails on any that names the launcher outside the allow-list above: a
// reader that is not the launcher, the row or a renderer is a reader that
// could be a decision.
func TestLaunchedByIsDisplayTextOnly(t *testing.T) {
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
		if !strings.Contains(string(src), "LaunchedBy") && !strings.Contains(string(src), "launched_by") {
			return nil
		}
		if _, ok := launchedByReaders[rel]; !ok {
			t.Errorf("%s names the launcher (LaunchedBy/launched_by), but it is not on the display allow-list in %s: "+
				"the launcher is untrusted text written by whoever launched the run, and nothing may decide anything by it",
				rel, "internal/runview/launched_by_test.go")
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
	// names the launcher is a stale permission, which is how an allow-list
	// quietly grows to cover code that should not be on it. The ee/ entries are
	// checked only when ee/ is there: the "core without ee/" workflow deletes it
	// and runs this test, and the walk above cannot see those files then.
	_, eeErr := os.Stat(filepath.Join(root, "ee"))
	eeGone := os.IsNotExist(eeErr)
	for rel := range launchedByReaders {
		if eeGone && strings.HasPrefix(rel, "ee/") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("allow-list names %s: %v", rel, err)
			continue
		}
		if !strings.Contains(string(b), "LaunchedBy") && !strings.Contains(string(b), "launched_by") {
			t.Errorf("allow-list still names %s, which no longer mentions the launcher", rel)
		}
	}
	if len(launchedByReaders) == 0 {
		t.Fatal("the allow-list is empty")
	}
}

// TestLaunchedByAllowListNamesTheRealField keeps the allow-list from drifting
// away from the field it is about: the scan above looks for the Go name
// LaunchedBy, so a rename of the JSON key alone (which every reader of
// launched_by would notice, and this scan would not) is caught here.
func TestLaunchedByAllowListNamesTheRealField(t *testing.T) {
	f, ok := reflect.TypeOf(Row{}).FieldByName("LaunchedBy")
	if !ok {
		t.Fatal("runview.Row has no LaunchedBy field: the launcher is not on the row at all")
	}
	if got := strings.Split(f.Tag.Get("json"), ",")[0]; got != "launched_by" {
		t.Fatalf("the row's launcher key is %q, not launched_by: the documents that read the key, and the allow-list scan's name, are out of step", got)
	}
}
