package plugin_test

// The shared skill check: a command a skill names, resolved against a CLI tree
// (design docs/design/ee-boundary-moves.md §9, D536). One binary ships, so the
// paid commands the owner moves out of the core are still names the released
// CLI answers to — as the core command today, as a registration after the move,
// as the stub a build without that code shows — and a skill naming one is
// naming a command its user has. Its flags are the tagged tree's to check
// (ee/plugincheck/skills_test.go), because the core carries only the stub after
// the move and the stub parses no flags.

import (
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/cli"
)

// skillCommand is one command a skill names, resolved against a CLI tree.
//
// paid marks a name in cli.PaidCommandNames(): it is accepted by name and by
// nothing deeper, and cmd is nil for it — the tree that answers to the name
// spells no command here the core could read a flag off. Every other name
// resolves to the command the tree holds, exactly as before the move.
type skillCommand struct {
	cmd   *cobra.Command
	paid  bool
	words []string // the command path the skill spells, flags excluded
}

// known reports whether the tree answers to the name the skill spells.
func (c skillCommand) known() bool { return c.paid || c.cmd != nil }

// resolveSkillCommand resolves the command a skill names — the words after
// `fugaro` on one line — against root. A word starting with `-` ends the
// command path: what follows is a flag and its value, not a subcommand.
func resolveSkillCommand(root *cobra.Command, words ...string) skillCommand {
	var path []string
	for _, w := range words {
		if strings.HasPrefix(w, "-") {
			break
		}
		path = append(path, w)
	}
	if len(path) == 1 && slices.Contains(cli.PaidCommandNames(), path[0]) {
		return skillCommand{paid: true, words: path}
	}
	cmd, _, err := root.Find(words)
	if err != nil || cmd == nil || cmd == root {
		return skillCommand{words: path}
	}
	// cobra hands back the deepest command it found with the rest as its
	// arguments, so a path deeper than the tree is not a name this answers to:
	// a paid command's subcommand is not invented here, and neither is a core
	// one's.
	if len(strings.Fields(cmd.CommandPath())) != len(path)+1 {
		return skillCommand{words: path}
	}
	return skillCommand{cmd: cmd, words: path}
}

// TestCoreSkillCheckAcceptsAPaidCommandByName: the core tree answers to a paid
// command by name even where it holds only the stub for it — the stub parses no
// flags, so `fugaro report --by repo` (the working skill's line,
// plugin/skills/working/SKILL.md) must pass without the core reading --by off a
// tree that cannot have it. The move must not turn a skill naming a command the
// released binary still has into a failure (F18). The other half is the guard:
// a name no build has still fails, and a paid name does not make a subcommand
// path under it resolve.
func TestCoreSkillCheckAcceptsAPaidCommandByName(t *testing.T) {
	root := &cobra.Command{Use: "fugaro"}
	root.AddCommand(&cobra.Command{
		Use:                "report",
		DisableFlagParsing: true,
		RunE:               func(*cobra.Command, []string) error { return nil },
	})
	runs := &cobra.Command{Use: "runs"}
	runs.AddCommand(&cobra.Command{Use: "stats", RunE: func(*cobra.Command, []string) error { return nil }})
	root.AddCommand(runs)

	if c := resolveSkillCommand(root, "report", "--by", "repo"); !c.known() || !c.paid {
		t.Errorf("`fugaro report --by repo` resolved to %+v, want the paid name report accepted by name: the stub parses no flags, so the core cannot check --by and must not fail the skill for it", c)
	}
	if c := resolveSkillCommand(root, "runs", "stats"); !c.known() || c.cmd == nil || c.cmd.Name() != "stats" {
		t.Errorf("`fugaro runs stats` resolved to %+v, want the stats command: a core command resolves as it always has", c)
	}
	if c := resolveSkillCommand(root, "nosuch"); c.known() {
		t.Errorf("`fugaro nosuch` resolved to %+v, want it refused: a name that is not paid and not in the tree is not a command", c)
	}
	if c := resolveSkillCommand(root, "report", "sub"); c.known() {
		t.Errorf("`fugaro report sub` resolved to %+v, want it refused: the paid name is accepted by name, and a path under it is the tagged tree's to resolve, not invented here", c)
	}
}
