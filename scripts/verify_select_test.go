package scripts_test

// The verify step's own command, driven hermetically: fugaro.yaml's
// commands.test is read out of this repository's configuration and run with a
// fake `go` on PATH, so a case says which packages one change runs — and what
// runs where the selector cannot place the change at all — without running a
// test of any package.
//
// The selector's own half is scripts/ci-affected's, and what it answers for a
// diff is internal/ciselect's (SelectVerify's tests there run the real read
// map, graph and diff). Here the selector's answer is the fake `go`'s, so what
// these cases pin is the command around it: the tree invocation's package
// list, the two halves internal/cli still needs, the fallback to the whole
// tree, and the four packages this step never runs.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// verifyCommand is this repository's configured test command, out of
// fugaro.yaml. A configuration that does not decode is the test's first
// failure, the way `fugaro validate` reads the file.
func verifyCommand(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "fugaro.yaml"))
	if err != nil {
		t.Fatalf("read fugaro.yaml: %v", err)
	}
	cfg, problems := config.Parse(data)
	if len(problems) > 0 {
		t.Fatalf("fugaro.yaml does not parse: %v", problems)
	}
	wf, ok := cfg.Workflows["go"]
	if !ok {
		t.Fatal("fugaro.yaml has no `go` workflow")
	}
	if strings.TrimSpace(wf.Commands.Test) == "" {
		t.Fatal("the `go` workflow configures no commands.test")
	}
	return wf.Commands.Test
}

// verifyRun is one run of the command: what the fake `go` was asked to do, in
// order, one line per call.
type verifyRun struct {
	calls []string
}

// ran reports whether some call carries every one of these substrings.
func (r verifyRun) ran(parts ...string) bool {
	for _, call := range r.calls {
		all := true
		for _, part := range parts {
			if !strings.Contains(call, part) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

func TestVerifyRunsThePackagesTheChangeReaches(t *testing.T) {
	cases := []struct {
		name string
		// selection is what scripts/ci-affected prints: the packages the
		// change reaches, or nothing where it cannot place the change.
		selection string
		// tree is what the tree invocation must carry, and notTree what it
		// must leave out. A case with noTree alone runs no tree invocation.
		tree    []string
		notTree []string
		noTree  bool
		// cli says the two internal/cli halves must run.
		cli bool
		// whole says the command must list the whole tree, which is the
		// fallback for a change the selector cannot place.
		whole bool
		// selectorFails makes the selector itself exit non-zero, which is what
		// a tree that does not carry scripts/ci-affected does.
		selectorFails bool
	}{
		{
			name:      "two packages and nothing else",
			selection: "./internal/config ./internal/projectpolicy",
			tree:      []string{"./internal/config", "./internal/projectpolicy"},
			notTree:   []string{"./internal/cli"},
		},
		{
			name:      "the cli package, whole, in its two halves",
			selection: "./internal/cli",
			noTree:    true,
			cli:       true,
		},
		{
			name:      "the cli package beside others",
			selection: "./internal/cli ./internal/ledger",
			tree:      []string{"./internal/ledger"},
			notTree:   []string{"./internal/cli"},
			cli:       true,
		},
		{
			// The empty answer is the doubt: R8's, a diff that could not be
			// read, a path no test is known to read. The whole tree runs.
			name:      "no answer at all is the whole tree",
			selection: "",
			// The whole tree is listed as import paths, the cli package is
			// split out of it and internal/runner left out of it.
			tree:    []string{"internal/config"},
			notTree: []string{"./internal/cli"},
			cli:     true,
			whole:   true,
		},
		{
			// The four packages this step never runs are dropped from the
			// answer and not the answer for them: a change that reaches one of
			// them usually reaches others too, and those are this step's to
			// run. This is the case that used to widen, and widening is what
			// put the whole tree back for nearly every change.
			name:      "the packages this step never runs are dropped, the rest runs",
			selection: "./internal/runner ./internal/config",
			tree:      []string{"./internal/config"},
			notTree:   []string{"./internal/runner"},
		},
		{
			// An answer that is nothing but those four is the one this step
			// cannot narrow, and the empty answer is still the whole tree.
			name:      "an answer that is only those four is the whole tree",
			selection: "./internal/runner ./images",
			tree:      []string{"internal/config"},
			notTree:   []string{"./internal/runner"},
			cli:       true,
			whole:     true,
		},
		{
			// The case this narrowing is for: a documents-only change reaches
			// the packages that read the document, and nothing else. The
			// sixteen are what docs/design/v1.md really selects today --
			// scripts/ci-affected against this repository's own read map and
			// graph, no diff involved -- ./internal/runner and ./images among
			// them, which is exactly why the guard this replaces was inert.
			name: "a documents-only change runs what reads the document",
			selection: "./images ./internal/ciselect ./internal/cli ./internal/config ./internal/digest " +
				"./internal/imagecheck ./internal/orchestrator ./internal/orchestrator/sensitivity " +
				"./internal/plan ./internal/policy ./internal/projectpolicy ./internal/rigor " +
				"./internal/runner ./internal/runstore ./internal/task ./schemas",
			// schemas is named without the "./" every other path here carries:
			// the read map's scan records a test's string literal as a path its
			// package reads, ./schemas is a directory of this checkout, and
			// ./scripts reads no schema — TestReadMapNamesEveryLiteralPath is
			// what would go red over it.
			tree: []string{"./internal/ciselect", "./internal/config", "./internal/digest",
				"./internal/imagecheck", "./internal/orchestrator", "./internal/orchestrator/sensitivity",
				"./internal/plan", "./internal/policy", "./internal/projectpolicy", "./internal/rigor",
				"./internal/runstore", "./internal/task", "schemas"},
			notTree: []string{"./internal/runner", "./images", "./internal/cli"},
			cli:     true,
		},
		{
			// The other half of the doubt: a branch cut before
			// scripts/ci-affected carries no program for `go run` to run, and a
			// follow-up or review run reads this command from the base branch
			// and checks out the pull request's own tree (internal/runner's
			// followup.go), so it lands exactly here. The step still runs its
			// tests, and it runs all of them.
			name:          "a tree with no selector at all",
			selectorFails: true,
			tree:          []string{"internal/config"},
			notTree:       []string{"./internal/cli"},
			cli:           true,
			whole:         true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := verifyDrive(t, c.selection, true, c.selectorFails)
			haveTree := run.ran("-junitfile reports/tree.xml")
			switch {
			case c.noTree && haveTree:
				t.Errorf("calls = %v, want no tree invocation when the change reaches cli alone", run.calls)
			case !c.noTree && !haveTree:
				t.Errorf("calls = %v, want a tree invocation", run.calls)
			}
			for _, pkg := range c.tree {
				if !run.ran("-junitfile reports/tree.xml", pkg) {
					t.Errorf("calls = %v, want the tree invocation to carry %s", run.calls, pkg)
				}
			}
			for _, pkg := range c.notTree {
				if run.ran("-junitfile reports/tree.xml", pkg) {
					t.Errorf("calls = %v, want the tree invocation to leave %s out", run.calls, pkg)
				}
			}
			if c.cli {
				if !run.ran("-junitfile reports/cli-update.xml", "-run ^TestU ./internal/cli/") {
					t.Errorf("calls = %v, want the cli update half", run.calls)
				}
				if !run.ran("-junitfile reports/cli-rest.xml", "-run ^Test[^U] ./internal/cli/") {
					t.Errorf("calls = %v, want the cli rest half", run.calls)
				}
			} else if run.ran("./internal/cli") {
				t.Errorf("calls = %v, want no cli invocation for a change that does not reach it", run.calls)
			}
			if c.whole != run.ranWholeTree() {
				t.Errorf("calls = %v, whole tree listed = %v, want %v", run.calls, run.ranWholeTree(), c.whole)
			}
		})
	}
}

// TestVerifyAsksTheSelectorForThisBranchAndTheBase pins the two things the
// command hands the selector: the checkout it runs in, and the base branch the
// change is measured from.
func TestVerifyAsksTheSelectorForThisBranchAndTheBase(t *testing.T) {
	run := verifyDrive(t, "./internal/config", true, false)
	if !run.ran("run ./scripts/ci-affected --base origin/main") {
		t.Errorf("calls = %v, want the selector asked for the diff against origin/main", run.calls)
	}
}

// TestVerifyOnATreeWithoutTheWrapperStillRunsTheTests: the wrapper's report is
// what `fugaro verify test` reads a run's test counts out of, and a tree too
// old to carry it still has to run its tests and exit with their own code.
func TestVerifyOnATreeWithoutTheWrapperStillRunsTheTests(t *testing.T) {
	run := verifyDrive(t, "./internal/config", false, false)
	if run.ran("run ./internal/verify/gotestjunit") {
		t.Errorf("calls = %v, want no wrapper on a tree that does not carry it", run.calls)
	}
	if !run.ran("test -timeout 15m", "./internal/config") {
		t.Errorf("calls = %v, want the tests run without the wrapper", run.calls)
	}
}

// verifyDrive runs the configured command once with a fake `go` on PATH in a
// checkout under t.TempDir(), and returns what the fake was asked to run.
// wrapper writes the tree's copy of internal/verify/gotestjunit, which is what
// selects the report-writing half of the command, and selectorFails makes the
// selector exit non-zero the way `go run` does on a tree without it.
func verifyDrive(t *testing.T, selection string, wrapper, selectorFails bool) verifyRun {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "go.log")
	fake := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"$FAKE_GO_LOG\"\n" +
		"case \"$1 $2\" in\n" +
		"  'run ./scripts/ci-affected')\n" +
		"    if [ -n \"$FAKE_GO_SELECTOR_FAILS\" ]; then\n" +
		"      printf '%s\\n' 'ci-affected: no Go files in this directory' >&2\n" +
		"      exit 1\n" +
		"    fi\n" +
		"    printf '%s\\n' \"$FAKE_GO_SELECTION\" ;;\n" +
		"  'list ./...') printf '%s\\n' \"$FAKE_GO_PACKAGES\" ;;\n" +
		"esac\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(fake), 0o755); err != nil {
		t.Fatalf("write the fake go: %v", err)
	}
	root := t.TempDir()
	if wrapper {
		path := filepath.Join(root, "internal", "verify", "gotestjunit", "main.go")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
			t.Fatalf("write the wrapper: %v", err)
		}
	}
	fails := ""
	if selectorFails {
		fails = "1"
	}
	cmd := exec.Command("sh", "-c", verifyCommand(t))
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_GO_LOG="+log,
		"FAKE_GO_SELECTION="+selection,
		"FAKE_GO_SELECTOR_FAILS="+fails,
		// What the whole tree `go list ./...` answers: the cli package is the
		// one the command has to split out of it, and internal/runner the one
		// it has to leave out.
		"FAKE_GO_PACKAGES=github.com/dimipaun/fugaro/internal/config\n"+
			"github.com/dimipaun/fugaro/internal/cli\n"+
			"github.com/dimipaun/fugaro/internal/runner",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the configured command exited %v:\n%s", err, out)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("read the fake go's log: %v", err)
	}
	var run verifyRun
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			run.calls = append(run.calls, line)
		}
	}
	if len(run.calls) == 0 {
		t.Fatalf("the command ran nothing:\n%s", out)
	}
	return run
}

// ranWholeTree reports whether the command asked for the whole tree.
func (r verifyRun) ranWholeTree() bool {
	return r.ran("list ./...")
}
