package plugin_test

// The go-to-market plan's G1.4: `fugaro estimate` reprices one run's recorded
// tokens on another model (G1.3), and the working skill is where the user's
// agent learns that the answer to "what would this have cost on Opus?" is a
// command rather than arithmetic over the run's own figure. The sentence
// lives in SKILL.md, which the agent always reads: the routing table has no
// row for a cost question, so a passage in a reference would be one no reader
// of an estimate question is ever sent to.

import (
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/cli"
)

// TestWorkingSkillNamesEstimate: the skill has to name the command, say what
// question it answers, and say the figure is an estimate priced at the named
// model's list rates. The command is resolved in the CLI's own tree, so the
// skill cannot send a user to a command the released binary does not have.
func TestWorkingSkillNamesEstimate(t *testing.T) {
	data, err := os.ReadFile("skills/working/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	skill := string(data)

	if !strings.Contains(skill, "fugaro estimate") {
		t.Fatal("skills/working/SKILL.md never names `fugaro estimate`: a user asking what a run would have cost on another model has no command, and an agent left without one does the arithmetic itself")
	}
	// The name is tied to the tree, not typed twice: --run and --model are
	// the two flags a one-run estimate cannot be written without, and the
	// plugin's own lint (TestPluginSkillsLintClean) checks every flag a skill
	// names against the same tree.
	if c := resolveSkillCommand(cli.NewRootCmd(), "estimate", "--run", "ID", "--model", "ID"); !c.known() {
		t.Fatalf("`fugaro estimate` does not resolve in the CLI's own tree (%+v): the skill names a command the released binary does not answer to", c)
	}

	for _, want := range []struct{ what, text string }{
		{"the question it answers", "would have cost on another model"},
		{"the model to price at", "--model"},
		{"that the figure is an estimate", "estimate, not"},
		{"that it is priced at the model's list rates", "list rates"},
	} {
		if !strings.Contains(skill, want.text) {
			t.Errorf("skills/working/SKILL.md never says %s (%q): an agent that is not told the figure is an estimate at another model's list rates reports it as what the run cost", want.what, want.text)
		}
	}
}
