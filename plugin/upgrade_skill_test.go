package plugin_test

import (
	"strings"
	"testing"
)

// TestUpgradeSkill: the skill checks, runs the local half, hands over one
// command for the cloud half, and stays inside its three commands (decision
// U14: the bound is in the text, not in frontmatter).
func TestUpgradeSkill(t *testing.T) {
	skill := readRepoFile(t, "plugin/skills/upgrade/SKILL.md")
	states := readRepoFile(t, "plugin/skills/upgrade/reference/states.md")
	for _, want := range []string{
		"`fugaro version`", "`fugaro upgrade --check`", "`fugaro upgrade --local`",
		"```bash user-runs\nfugaro upgrade --yes\n```",
		"A coding agent's session cannot apply cloud changes, by design",
		"Never edit `.claude/settings.json`", "brew upgrade dimipaun/tap/fugaro", "restart",
		"Exit 1 means something is stale", "never pass `--allow-fork`",
	} {
		if !strings.Contains(skill, want) {
			t.Errorf("SKILL.md never says %q", want)
		}
	}
	if n := strings.Count(skill, "```bash user-runs"); n != 1 {
		t.Errorf("SKILL.md hands the user %d commands, want exactly one", n)
	}
	for _, bad := range []string{"claude plugin install", "claude plugin update", "claude plugin marketplace", "image refresh --yes", "--dangerously", "allowed-tools"} {
		if strings.Contains(skill+states, bad) {
			t.Errorf("the skill names %q", bad)
		}
	}
}
