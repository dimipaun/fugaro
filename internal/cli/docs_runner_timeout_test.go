package cli

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// runnerLocalCommand is how internal/runner is run with a bound of its own —
// `go test`'s `-timeout` is 10 minutes per package by default, and that
// package's own suite takes about 25. It is the local half of the same fact
// the `test` job carries, and the step docs/pending-workflows/README.md asks
// the owner to add runs exactly it.
const runnerLocalCommand = "go test -race -timeout 40m ./internal/runner/..."

// runnerTimeoutClaims are the three documents that say whether CI gives
// internal/runner a bound of its own, with both forms of that claim: the
// sentence each one carries while `.github/workflows/ci.yml` does not run the
// package separately (pending), and the sentence that replaces it once it
// does (applied). TestRunnerTimeoutDocsMatchCI holds whichever form is
// present to the workflow, in both directions — the workflow is a file no run
// may edit, so these three can only ever move *after* the owner applies the
// change in docs/pending-workflows/README.md's `## ci.yml`, and the applied
// half of that has to be somewhere or the documents would be left saying CI
// does not do what it does.
var runnerTimeoutClaims = []struct {
	path             string
	pending, applied string
}{
	{
		path:    "CONTRIBUTING.md",
		pending: "CI's `test` job does not give that package the same bound yet.",
		applied: "CI's `test` job gives that package the same bound, in a step of its own.",
	},
	{
		path:    "docs/dogfooding.md",
		pending: "CI does not give that package the same bound yet",
		applied: "CI gives that package the same bound, in a step of its own",
	},
	{
		path:    "docs/pending-workflows/README.md",
		pending: "**The change outstanding now: the `internal/runner` package's test timeout",
		applied: "**Applied: the `internal/runner` package's test timeout",
	},
}

// runnerTestJobCapRE reads the `test` job's own ceiling: the job key, then the
// first `timeout-minutes:` inside it. The jobs are at two-space indentation
// and a job's own key is at four, so the capture stops at a step's.
var runnerTestJobCapRE = regexp.MustCompile(`(?m)^  test:\n(?:.*\n)*?    timeout-minutes: (\d+)\n`)

// runnerStepBoundRE reads the bound off the step that runs the package on its
// own.
var runnerStepBoundRE = regexp.MustCompile(`-timeout (\d+)m`)

// runnerCIState is what `.github/workflows/ci.yml` says about the package.
type runnerCIState struct {
	ownStep bool // a step of its own, with a bound of its own
	bound   int  // that step's -timeout, in minutes
	cap     int  // the test job's timeout-minutes, in minutes
}

// readRunnerCIState reads the `test` job out of the workflow: whether it runs
// internal/runner in a step of its own, what bound that step asks for, and
// what the job as a whole allows. A job that bounds one step and then dies
// before it is a change that looks applied and is not.
func readRunnerCIState(ci string) (runnerCIState, error) {
	var st runnerCIState
	job, err := runnerTestJob(ci)
	if err != nil {
		return st, err
	}
	if m := runnerTestJobCapRE.FindStringSubmatch(ci); m != nil {
		st.cap, _ = strconv.Atoi(m[1])
	}
	steps := 0
	for _, line := range strings.Split(job, "\n") {
		if !strings.Contains(line, "go test") {
			continue
		}
		steps++
		// The step that runs everything else: the package is named only to
		// be left out.
		if strings.Contains(line, "grep -v") && strings.Contains(line, "internal/runner") {
			continue
		}
		if !strings.Contains(line, "./internal/runner") {
			continue
		}
		m := runnerStepBoundRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		st.ownStep = true
		st.bound, _ = strconv.Atoi(m[1])
	}
	if steps == 0 {
		return st, fmt.Errorf(".github/workflows/ci.yml's `test` job has no `go test` step: the package's bound was pinned to a job that no longer runs tests")
	}
	return st, nil
}

// runnerTestJob returns the text of the workflow's `test` job, from its key to
// the next job's.
func runnerTestJob(ci string) (string, error) {
	lines := strings.Split(ci, "\n")
	start := -1
	for i, line := range lines {
		if line == "  test:" {
			start = i
			break
		}
	}
	if start < 0 {
		return "", fmt.Errorf(".github/workflows/ci.yml has no `test` job: CONTRIBUTING.md, docs/dogfooding.md and docs/pending-workflows/README.md all describe the bound that job gives internal/runner")
	}
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "  ") && !strings.HasPrefix(lines[i], "   ") && strings.HasSuffix(lines[i], ":") {
			return strings.Join(lines[start:i], "\n"), nil
		}
	}
	return strings.Join(lines[start:], "\n"), nil
}

// runnerTimeoutStateProblems returns every disagreement between what the
// workflow does for internal/runner and what the three documents say it does,
// as messages rather than as test failures: taking the workflow and the
// documents as arguments is what lets
// TestRunnerTimeoutStateRulesSeeEveryState pin all of the combinations
// against fixtures, including the applied one this repository is never in
// while an agent is working — writing .github/workflows/ci.yml is the owner's
// step, so without the fixtures the half of this check that catches a stale
// "not yet" would never run at all.
func runnerTimeoutStateProblems(ci string, doc func(path string) string) []string {
	st, err := readRunnerCIState(ci)
	if err != nil {
		return []string{err.Error()}
	}
	var problems []string
	for _, c := range runnerTimeoutClaims {
		text := doc(c.path)
		pending := strings.Contains(text, c.pending)
		applied := strings.Contains(text, c.applied)
		switch {
		case pending && applied:
			problems = append(problems, fmt.Sprintf("%s carries both %q and %q: they are the same claim in two directions, so a reader is told CI gives internal/runner a bound of its own and that it does not", c.path, c.pending, c.applied))
		case st.ownStep && !applied:
			problems = append(problems, fmt.Sprintf("%s does not say CI gives internal/runner a bound of its own, and .github/workflows/ci.yml now does (a step of its own, `-timeout %dm`): replace the pending sentence with %q in the same change that applies it", c.path, st.bound, c.applied))
		case !st.ownStep && !pending:
			problems = append(problems, fmt.Sprintf("%s does not say CI gives internal/runner no bound of its own yet, and .github/workflows/ci.yml does not (its `test` job runs one `go test` over every package): put %q back, or apply the change in docs/pending-workflows/README.md's `## ci.yml` to the workflow first", c.path, c.pending))
		}
		// The bound itself, which is what the three documents are about: the
		// local command CONTRIBUTING.md and docs/dogfooding.md give a
		// contributor, and the step the README asks the owner to add. A
		// document that names a different number names a bound that is not
		// this one.
		if !strings.Contains(text, runnerLocalCommand) {
			problems = append(problems, fmt.Sprintf("%s no longer names `%s`: that command is the bound this claim is about, and the three documents name the same one", c.path, runnerLocalCommand))
		}
	}
	// The workflow side of the same fact, once it carries the step: the step's
	// own bound can only fire if the job outlives it, and the step starts
	// after gofmt and the `go vet` runs, so a cap at or below the bound is a
	// change that is written down and cannot work — which is exactly how the
	// first draft of the README's two steps was wrong (a `-timeout 40m` step
	// under the job's `timeout-minutes: 30`).
	if st.ownStep {
		switch {
		case st.cap == 0:
			problems = append(problems, "the `test` job of .github/workflows/ci.yml has no `timeout-minutes`, so the bound the internal/runner step asks for is bounded by GitHub's default (360 minutes) rather than by anything written here")
		case st.cap <= st.bound:
			problems = append(problems, fmt.Sprintf(".github/workflows/ci.yml's `test` job is capped at %d minutes while the internal/runner step asks for `-timeout %dm`: the job is killed before that bound can fire, which leaves the package with whatever time the steps before it left", st.cap, st.bound))
		}
	}
	return problems
}

// TestRunnerTimeoutDocsMatchCI: the three documents that say whether CI gives
// internal/runner a bound of its own are held to the workflow, in both
// directions. This is the check the review of the first writing of that
// section found missing: CONTRIBUTING.md, docs/dogfooding.md and the README's
// `## ci.yml` each state the CI half of a fact that only moves when the owner
// edits a file no run may push, so without this test the day the owner
// applies it, all three say something false and nothing goes red.
func TestRunnerTimeoutDocsMatchCI(t *testing.T) {
	ci := readDoc(t, ".github/workflows/ci.yml")
	for _, p := range runnerTimeoutStateProblems(ci, func(path string) string { return readDoc(t, path) }) {
		t.Error(p)
	}
}

// TestRunnerTimeoutStateRulesSeeEveryState pins the rules above against the
// combinations that matter, including the one this repository cannot be in:
// applied in the workflow (the owner's step) with the documents updated, and
// applied with the documents left behind. Each case asserts the problems *and*
// that a correct pair produces none, so a rule that failed everything would
// fail here rather than looking strict.
func TestRunnerTimeoutStateRulesSeeEveryState(t *testing.T) {
	const (
		// The workflow as it is today: one step over every package.
		ciPending = "  test:\n    timeout-minutes: 30\n    steps:\n      - run: gofmt\n      - run: go test -race -timeout 20m ./...\n"
		// The change the README asks for, with the cap raised with it.
		ciApplied = "  test:\n    timeout-minutes: 60\n    steps:\n" +
			"      - run: go test -race -timeout 20m $(go list ./... | grep -v /internal/runner)\n" +
			"      - run: go test -race -timeout 40m ./internal/runner/...\n"
		// ... and the same steps under the cap the job has today, which is
		// the way this change goes wrong quietly.
		ciAppliedUnderTheOldCap = "  test:\n    timeout-minutes: 30\n    steps:\n" +
			"      - run: go test -race -timeout 20m $(go list ./... | grep -v /internal/runner)\n" +
			"      - run: go test -race -timeout 40m ./internal/runner/...\n"
	)
	// docsIn answers with the real sentences of whichever form is asked for,
	// so a case fails on the rules and not on a fixture that misquotes them.
	docsIn := func(state string) func(path string) string {
		return func(path string) string {
			for _, c := range runnerTimeoutClaims {
				if c.path == path {
					switch state {
					case "pending":
						return "some prose. " + c.pending + " " + runnerLocalCommand + "\n"
					case "applied":
						return "some prose. " + c.applied + " " + runnerLocalCommand + "\n"
					}
				}
			}
			return "some prose about something else\n"
		}
	}
	for _, tc := range []struct {
		name     string
		ci       string
		docs     func(path string) string
		wantNone bool
		want     []string // one substring per expected problem
	}{
		{"the state this branch is in: pending in the workflow and in the documents", ciPending, docsIn("pending"), true, nil},
		{"applied, with the documents moved with it", ciApplied, docsIn("applied"), true, nil},
		{"applied, the documents left behind", ciApplied, docsIn("pending"), false,
			[]string{"CONTRIBUTING.md does not say CI gives", "docs/dogfooding.md does not say CI gives", "docs/pending-workflows/README.md does not say CI gives"}},
		{"the documents moved, the workflow left behind", ciPending, docsIn("applied"), false,
			[]string{"CONTRIBUTING.md does not say CI gives internal/runner no bound", "docs/dogfooding.md does not say CI gives internal/runner no bound", "docs/pending-workflows/README.md does not say CI gives internal/runner no bound"}},
		{"applied, but under the cap the job has today", ciAppliedUnderTheOldCap, docsIn("applied"), false,
			[]string{"is capped at 30 minutes while the internal/runner step asks for `-timeout 40m`"}},
		{"both sentences in one document", ciPending, func(path string) string {
			if path == "CONTRIBUTING.md" {
				return runnerTimeoutClaims[0].pending + runnerTimeoutClaims[0].applied + runnerLocalCommand
			}
			return docsIn("pending")(path)
		}, false, []string{"CONTRIBUTING.md carries both"}},
		{"a document that names another bound", ciPending, func(path string) string {
			if path == "docs/dogfooding.md" {
				return runnerTimeoutClaims[1].pending
			}
			return docsIn("pending")(path)
		}, false, []string{"docs/dogfooding.md no longer names"}},
		{"no test job at all", "  build:\n    steps:\n      - run: true\n", docsIn("pending"), false,
			[]string{"has no `test` job"}},
		{"a test job with no test step", "  test:\n    timeout-minutes: 30\n    steps:\n      - run: gofmt\n", docsIn("pending"), false,
			[]string{"has no `go test` step"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problems := runnerTimeoutStateProblems(tc.ci, tc.docs)
			if tc.wantNone {
				if len(problems) != 0 {
					t.Fatalf("problems = %q, want none", problems)
				}
				return
			}
			if len(problems) == 0 {
				t.Fatalf("no problems at all, want %d (%q)", len(tc.want), tc.want)
			}
			for _, want := range tc.want {
				if !slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, want) }) {
					t.Errorf("no problem names %q; got %q", want, problems)
				}
			}
		})
	}
}
