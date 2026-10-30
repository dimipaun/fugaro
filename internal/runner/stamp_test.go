package runner_test

// The stamp a run's commits carry (A3, D601-D603): the author and the
// Fugaro-* trailers, written when the commit is made. Every test here runs a
// real run over a real checkout, because what is being pinned is what git
// stores in the commit.

import (
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// trailersOf reads the trailer lines of a commit message the way these tests
// ask about them: keyed by the lower-cased key git matches by, keeping every
// value, so a duplicate is two entries and a replacement is one.
func trailersOf(msg string) map[string][]string {
	out := map[string][]string{}
	for _, line := range strings.Split(msg, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || key == "" || strings.TrimSpace(key) != key {
			continue
		}
		if strings.ContainsFunc(key, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-'
		}) {
			continue
		}
		out[strings.ToLower(key)] = append(out[strings.ToLower(key)], strings.TrimSpace(value))
	}
	return out
}

// branchCommit is the message of the commit a run's branch tip holds.
func branchCommit(t *testing.T, h *harness) string {
	t.Helper()
	return testutil.Git(t, h.remote, "log", "-1", "--format=%B", "fugaro/"+runID)
}

// TestRunnerStampsAgentCommits: the commit the agent makes carries the run's
// trailers as it is made — the run ID and the rigor mode — and nothing
// rewrites it afterwards (D603, constraint 1).
//
// Mutation (drop the stamp from the stage's environment): the commit the
// branch tip holds carries no Fugaro-Run trailer.
func TestRunnerStampsAgentCommits(t *testing.T) {
	h := newHarness(t, "", nil)
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	got := trailersOf(branchCommit(t, h))
	if want := []string{runID}; !slices.Equal(got["fugaro-run"], want) {
		t.Errorf("fugaro-run = %v, want %v: the agent's commit is not stamped", got["fugaro-run"], want)
	}
}
