package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// TestValidateReportsRoutingProblems: the project's routing table is judged by
// its own checks (RT3) against the models the file in hand would call, and a
// model no entry of it matches is a problem of this file — printed at the
// table's own block, with the fix `fugaro routing set` writes — because every
// launch of that file is refused for it (R1, R22, and the gate a launch makes
// at internal/cli's chainRoutingGate).
//
// The craft is the subject and not a pin of a role: the crafts are what a
// repository fixes about its own runs, and the model a craft resolves to is the
// one a launch of this file sends.
func TestValidateReportsRoutingProblems(t *testing.T) {
	craftFixture(t)
	// The coder's model is the one thing this world changes: the fixture's
	// table routes a DeepSeek model through the OpenRouter route, and this file
	// names a model no entry matches.
	testutil.WriteFiles(t, ".", map[string]string{"fugaro.yaml": strings.Replace(validateCraftModelYAML,
		"  crafts:\n", "  crafts:\n    coder: {model: qwen/qwen3-coder, access: openrouter-main}\n", 1)})

	out, errOut, err := execute(t, "validate", "--offline", "fugaro.yaml")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("validate accepted a craft model no entry of the table matches: exit %d\n%s%s", ExitCode(err), out, errOut)
	}
	line := problemLine(out, "model_routing")
	if !strings.Contains(line, `no entry of the table matches "qwen/qwen3-coder"`) {
		t.Errorf("validate did not report the model no entry matches:\nwant %q in a line\ngot  %q\nall:\n%s",
			"no entry of the table matches \"qwen/qwen3-coder\"", line, out)
	}
	if !strings.Contains(line, "fugaro routing set qwen/qwen3-coder") {
		t.Errorf("the problem does not name the fix that writes the entry:\n%s", line)
	}
}

// TestValidateIgnoresATableThatServesAnotherRepo: the table judges this file's
// crafts only where it serves the repository their runs are for — the question
// the launch's own gate asks first (chainRoutingGate, infra.ChainPlanFor). A
// table whose every source is admitted to another repository is not in force
// here: this checkout's runs go out the release-1 way, as on a project that
// publishes no table (O7, R25, D3196), so a craft's model no entry of it matches
// is not reported as unrouted — the release-1 rules judge that model, and they
// are what its launch is refused by.
func TestValidateIgnoresATableThatServesAnotherRepo(t *testing.T) {
	craftFixtureElsewhere(t)
	// The file is the pinned one its sibling tests use, which the release-1
	// rules serve whole: the coder's model is the fixture's DeepSeek one, which
	// no entry of the elsewhere table matches.
	testutil.WriteFiles(t, ".", map[string]string{"fugaro.yaml": validateCraftPinnedYAML})
	out, errOut, err := execute(t, "validate", "--offline", "fugaro.yaml")
	if ExitCode(err) != ExitOK {
		t.Fatalf("validate refused a file the release-1 rules serve: exit %d\n%s%s", ExitCode(err), out, errOut)
	}
	if line := problemLine(out, "model_routing"); line != "" {
		t.Errorf("validate judged this file by a table that serves another repository:\n%s", line)
	}
}
