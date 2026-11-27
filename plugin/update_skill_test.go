package plugin_test

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// TestUpdateSkill: the skill checks, runs the plugin half, hands over the one
// command that converges the installation and the checkout, and stays inside
// its three commands (decision U14: the bound is in the text, not in
// frontmatter).
//
// What that one command does includes the orchestrator's unit since D2700 of
// docs/orchestrator-graph-estimates (the owner's answer of 2026-11-16, amended
// after review and corrected by F48): `fugaro update` installs the login unit
// on a machine that has none and re-renders an installed one, so the skill
// that hands the command over has to say the unit is part of the upgrade. The
// pinned half is the fence F48 kept: the write happens only where a person is
// at the terminal, and the agent reading this skill is told to hand the run
// over rather than promise the unit from a session of its own.
//
// The three commands are `fugaro version`, `fugaro update --check` and
// `fugaro update-skills` since U4 of
// docs/plugin-user-scope/implementation-plan.md retired `fugaro upgrade` and
// `--local` with it (D948, D950): the read-only mode is the converge's own, and
// the plugin half an agent may run is the plugin-only command. The skill
// itself is `update` since U6 (D953): same job, renamed one for one, so the
// plugin still carries seven skills.
func TestUpdateSkill(t *testing.T) {
	skill := readRepoFile(t, "plugin/skills/update/SKILL.md")
	states := readRepoFile(t, "plugin/skills/update/reference/states.md")
	for _, want := range []string{
		"`fugaro version`", "`fugaro update --check`", "`fugaro update-skills`",
		"```bash user-runs\nfugaro update\n```",
		"A coding agent's session cannot apply cloud changes, by design",
		"The handed-over command stays theirs.",
		"never look for a flag or another command that would let this session apply those writes",
		"Never edit `.claude/settings.json`", "brew upgrade dimipaun/tap/fugaro", "restart",
		"Exit 1 means something is stale", "you never change which marketplace this machine trusts",
		// D3340/R11 (U7a of docs/update-from-anywhere): the leftover plugin pin
		// is a notice `doctor` and `update` print, naming the line to delete —
		// never a diff this session or the run writes — and `--check` reads
		// each repository through the installation's credentials service
		// (D3349 as amended, R17), not with no credentials at all.
		"named by `doctor` and `update`, with the line to delete",
		"reads each repository through the installation's credentials service",
		// D2700: the unit is part of what the one command does, on a machine
		// that has none as well as over an installed one, and the session that
		// reads this skill is not where that write happens.
		"installs the orchestrator daemon's login unit on this machine when it has none, re-rendering the one already there",
		"the per-project lease keeps one of those daemons live and the rest on standby",
		"a run inside a coding agent's session installs no unit and says so in one line, and the user's next run of it at their own terminal installs it",
		"Nothing in this skill's frontmatter can limit which commands it runs: the three-command rule above is an instruction, not a permission, and the user's own Claude Code permission settings are what bound it.",
	} {
		if !strings.Contains(skill, want) {
			t.Errorf("SKILL.md never says %q", want)
		}
	}
	if n := strings.Count(skill, "```bash user-runs"); n != 1 {
		t.Errorf("SKILL.md hands the user %d commands, want exactly one", n)
	}
	for _, bad := range []string{"claude plugin install", "claude plugin update", "claude plugin marketplace", "--dangerously", "allowed-tools", "needs no credentials"} {
		if strings.Contains(skill+states, bad) {
			t.Errorf("the skill names %q", bad)
		}
	}
}

// TestWorkingAndSetupCarryTheSessionStartNudge: the sentence that turns a
// stale installation into a two-command onboarding ([spec.md §7](docs/plugin-user-scope/spec.md))
// opens `working` and `setup`, in the same words in both.
//
// Its second half is the `update` skill's own sentence, pinned above and in
// the `update` skill because the pinned strings travel with the sentence
// rather than being rewritten (D947): the two skills repeat it, the `plugin/`
// lint gates it, and the update skill still carries it where a reader who
// arrives there looks for it.
func TestWorkingAndSetupCarryTheSessionStartNudge(t *testing.T) {
	for _, path := range []string{"plugin/skills/working/SKILL.md", "plugin/skills/setup/SKILL.md"} {
		skill := readRepoFile(t, path)
		for _, want := range []string{
			"`fugaro doctor --offline --json`",
			"its `ok` is false, or a check is `ok: false` with a `severity` other than `info`",
			"A check `--offline` did not run comes back `skipped: true` with severity `info`, and is not one of these.",
			"one sentence",
			"```bash user-runs\nfugaro update\n```",
			"A coding agent's session cannot apply cloud changes, by design",
			"never look for a flag or another command that would let this session apply those writes",
			"never unset the marker that says this session is one",
			"say nothing and carry on",
			"a status report nobody asked for",
		} {
			if !strings.Contains(skill, want) {
				t.Errorf("%s never says %q", path, want)
			}
		}
	}
}

// TestTheNudgeOffersFugaroUpdateOnlyWhereTheFixNamesIt: with the trigger keyed
// on severity, the warning checks that reach the sentence are not all `fugaro
// update`'s. A pin newer than this binary names `brew upgrade` and says the
// update never moves a pin down, and a foreign marketplace is the user's own
// decision (`internal/pluginwire/status.go`); a login unit that could not be
// read or rendered names the shell or the file (`internal/cli/doctor.go`,
// `orchestrator-unit`). So the nudge offers `fugaro update` only where the
// check's own `fix` is its own line, and relays that check's own fix otherwise.
//
// The warning states are read, not restated: were every one of them the
// command's, the conditional would be dead and this test says so.
func TestTheNudgeOffersFugaroUpdateOnlyWhereTheFixNamesIt(t *testing.T) {
	relayed := map[string]bool{}
	for _, s := range []pluginwire.State{
		pluginwire.Outdated, pluginwire.Newer, pluginwire.Unpinned, pluginwire.Foreign, pluginwire.InstalledDiffers,
	} {
		if s.Severity() != pluginwire.SeverityWarning {
			t.Errorf("%s is no longer a warning state; the nudge keys its trigger on severity", s)
			continue
		}
		if !strings.HasPrefix(s.Fix(), "run fugaro update") {
			relayed[string(s)] = true
		}
	}
	for _, s := range []pluginwire.State{pluginwire.Newer, pluginwire.Foreign} {
		if !relayed[string(s)] {
			t.Errorf("%s's fix now runs `fugaro update`; if that is real, the nudge's conditional is dead", s)
		}
	}
	for _, path := range []string{"plugin/skills/working/SKILL.md", "plugin/skills/setup/SKILL.md"} {
		skill := readRepoFile(t, path)
		for _, want := range []string{"where that `fix` is `fugaro update`'s, the command below", "otherwise that `fix` itself, as printed"} {
			if !strings.Contains(skill, want) {
				t.Errorf("%s never says %q, so a check whose fix is not the update's own is handed a command that changes nothing", path, want)
			}
		}
	}
}
