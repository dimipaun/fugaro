package plugin_test

import (
	"strings"
	"testing"
)

// TestUpgradeSkill: the skill checks, runs the plugin half, hands over the one
// command that converges the installation and the checkout, and stays inside
// its three commands (decision U14: the bound is in the text, not in
// frontmatter).
//
// The three commands are `fugaro version`, `fugaro update --check` and
// `fugaro update-skills` since U4 of
// docs/plugin-user-scope/implementation-plan.md retired `fugaro upgrade` and
// `--local` with it (D948, D950): the read-only mode is the converge's own, and
// the plugin half an agent may run is the plugin-only command.
func TestUpgradeSkill(t *testing.T) {
	skill := readRepoFile(t, "plugin/skills/upgrade/SKILL.md")
	states := readRepoFile(t, "plugin/skills/upgrade/reference/states.md")
	for _, want := range []string{
		"`fugaro version`", "`fugaro update --check`", "`fugaro update-skills`",
		"```bash user-runs\nfugaro update\n```",
		"A coding agent's session cannot apply cloud changes, by design",
		"The handed-over command stays theirs.",
		"never look for a flag or another command that would let this session apply those writes",
		"Never edit `.claude/settings.json`", "brew upgrade dimipaun/tap/fugaro", "restart",
		"Exit 1 means something is stale", "you never change which marketplace this machine trusts",
		"Nothing in this skill's frontmatter can limit which commands it runs: the three-command rule above is an instruction, not a permission, and the user's own Claude Code permission settings are what bound it.",
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
