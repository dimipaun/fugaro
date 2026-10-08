package cli

import (
	"os"
	"strings"
	"testing"
)

// TestUpgradeSkillNamesEveryState: the skill's reference explains every
// state and every step fugaro upgrade prints, and the flags the skill uses
// exist.
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
	for _, s := range upgradeSteps {
		if !strings.Contains(doc, "`"+s.name+":`") {
			t.Errorf("states.md never names the step `%s:`", s.name)
		}
	}
	for _, f := range []string{"check", "local", "yes"} {
		if newUpgradeCmd().Flags().Lookup(f) == nil {
			t.Errorf("fugaro upgrade has no --%s, which the skill uses", f)
		}
	}
}
