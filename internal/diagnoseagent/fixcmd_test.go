package diagnoseagent

import (
	"errors"
	"slices"
	"testing"
)

// fakeTree is a checker with one command: `fugaro doctor --json` exists and
// nothing else does. It stands in for internal/cli's walk of the real tree,
// which is tested where the tree is.
func fakeTree(argv []string) error {
	if len(argv) < 2 || argv[1] != "doctor" {
		return errors.New("it is not a fugaro command")
	}
	for _, w := range argv[2:] {
		if w != "--json" {
			return errors.New("fugaro doctor has no " + w + " flag")
		}
	}
	return nil
}

func TestFixesReadsCommandsOutOfProse(t *testing.T) {
	const text = "The project layer refuses it. Run `fugaro doctor --json` if you doubt that.\n" +
		"\n" +
		"`fugaro doctor --json`\n" +
		"\n" +
		"```sh\n" +
		"fugaro doctor\n" +
		"```\n" +
		"\n" +
		"- `fugaro secrets ls`\n" +
		"1. fugaro settings set base_branch main\n" +
		"   a nested word, not a command\n"
	got := Fixes(text, fakeTree)
	var commands []string
	for _, f := range got {
		commands = append(commands, f.Command)
	}
	want := []string{"fugaro doctor --json", "fugaro doctor", "fugaro secrets ls", "fugaro settings set base_branch main"}
	if !slices.Equal(commands, want) {
		t.Fatalf("Fixes found %q, want %q", commands, want)
	}
	// The first line names a command inside a sentence, and the last is an
	// indented note: neither is a line a person pastes, so neither is one of
	// the four above.
	if !got[0].Checked || !got[1].Checked || got[2].Checked || got[3].Checked {
		t.Errorf("the verdicts are %+v, want the two `fugaro doctor` lines checked and the other two not", got)
	}
	if got[2].Reason != "it is not a fugaro command" {
		t.Errorf("the refusal reads %q, want the tree's own", got[2].Reason)
	}
}

func TestFixesRefuseWhatTheTreeLacks(t *testing.T) {
	got := Fixes("fugaro\nfugaro doctor --json\nfugaro doctor --nope\n", fakeTree)
	if len(got) != 3 {
		t.Fatalf("Fixes = %+v, want three", got)
	}
	if got[0].Checked || got[0].Reason != errNoSubcommand.Error() {
		t.Errorf("a bare fugaro is %+v, want the no-subcommand refusal", got[0])
	}
	if !got[1].Checked || got[1].Reason != "" {
		t.Errorf("`fugaro doctor --json` is %+v, want checked and with no reason", got[1])
	}
	if got[2].Checked || got[2].Reason == "" {
		t.Errorf("`fugaro doctor --nope` is %+v, want the tree's own refusal", got[2])
	}
}

// TestFixesWithoutACheckerRefuseEverything: a caller that forgot the checker
// must read as "not checked", never as agreement. The reason says so, so the
// person is told why the command carries the flag.
func TestFixesWithoutACheckerRefuseEverything(t *testing.T) {
	got := Fixes("fugaro doctor --json\n", nil)
	if len(got) != 1 || got[0].Checked || got[0].Reason != errNoChecker.Error() {
		t.Fatalf("Fixes with no checker = %+v, want one unchecked command naming the missing checker", got)
	}
}

func TestFixTextFlagsOnlyTheRefused(t *testing.T) {
	const text = "Read the check below.\n\nfugaro doctor --json\nfugaro doctor --nope\n"
	got := FixText(text, fakeTree)
	want := text + "  (" + UncheckedFlag + " — fugaro doctor has no --nope flag)\n"
	if got != want {
		t.Fatalf("FixText =\n%q\nwant\n%q", got, want)
	}
}

// TestFixTextLeavesTextWithoutCommandsAlone: the answer a person reads most
// often is prose, and rewriting it would be a second thing that can differ
// from what the agent wrote.
func TestFixTextLeavesTextWithoutCommandsAlone(t *testing.T) {
	const text = "The command failed because the account is not allowed here.\n\nRun it from the checkout.\n"
	if got := FixText(text, fakeTree); got != text {
		t.Fatalf("FixText rewrote prose: %q", got)
	}
}
