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
