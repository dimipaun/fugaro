package runner_test

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/runner"
)

// TestPickPromptPairsNumbersWithTheirOwnDirectory: PickPrompt writes one
// "- PR #N: dir" line per attempt, nums[i] paired with dirs[i] — the only
// place a reviewing agent learns which checked-out directory is which pull
// request. nums is given out of numeric order here (9 before 7) so that a
// bug sorting one slice independently of the other, or transposing the two,
// would mispair a number with the wrong directory instead of merely
// reordering two already-correct pairs.
//
// Mutation (swap `dirs[i]` for `dirs[len(dirs)-1-i]` in PickPrompt's loop):
// this test fails, finding PR #9 paired with "/review/7" and PR #7 paired
// with "/review/9".
func TestPickPromptPairsNumbersWithTheirOwnDirectory(t *testing.T) {
	got := runner.PickPrompt("", "", []int{9, 7}, []string{"/review/9", "/review/7"})
	if !strings.Contains(got, "- PR #9: /review/9\n") {
		t.Fatalf("PR #9 not paired with its own directory: %q", got)
	}
	if !strings.Contains(got, "- PR #7: /review/7\n") {
		t.Fatalf("PR #7 not paired with its own directory: %q", got)
	}
}
