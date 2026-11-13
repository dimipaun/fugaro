package cli

import (
	"os"
	"strings"
	"testing"
)

// TestUpgradeSkillNamesEveryState: the skill's reference explains every state
// and every step the check prints, and the flags the skill uses exist.
//
// The steps are `fugaro update --check`'s (checkSteps, update_check.go): U4 of
// docs/plugin-user-scope/implementation-plan.md retired `fugaro upgrade` and
// `--local` with it, and the skill's own text — which the states table belongs
// to — is the skills task's (U6), so what is checked here is what the tree has
// now: the four steps the read-only mode prints and the two flags it reads.
// The skill is rewritten against `fugaro update` there, and this test is
// re-pointed with it.
func TestUpgradeSkillNamesEveryState(t *testing.T) {
	data, err := os.ReadFile("../../plugin/skills/upgrade/reference/states.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, s := range stepStates {
		if !strings.Contains(doc, "`"+string(s)+"`") {
			t.Errorf("states.md never explains `%s`", s)
		}
	}
	for _, s := range checkSteps {
		if !strings.Contains(doc, "`"+s.name+":`") {
			t.Errorf("states.md never names the step `%s:`", s.name)
		}
	}
	for _, f := range []string{"check", "yes"} {
		if newUpdateCmd().Flags().Lookup(f) == nil {
			t.Errorf("fugaro update has no --%s, which the skill uses", f)
		}
	}
}
