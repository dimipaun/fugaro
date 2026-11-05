package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/initflow"
)

// managed_ask.go is `--ask`, the symmetric half of `--yes` (the owner's rule of
// 2026-11-04): --yes is the flag of a project that is NOT managed, where the
// default is a prompt and the flag answers it, and --ask is the flag of one
// that IS managed, where the default is no prompt and the flag puts the prompts
// back. It is the whole of the flag's behaviour, in one place, because the same
// three answers are given by every command in the managed set and a second copy
// of them per command is a second thing to keep in step.
//
// The two things this file does NOT decide, and why:
//
//   - What the gate admits. --ask never reaches the gate's own read, admit or
//     record: those are made exactly where they were before (M6's helper,
//     update_managed.go, image_refresh_managed.go, settings_managed.go,
//     accounts_managed.go), and the flag is read after the admit and answers
//     only a question about prompts. A step the gate refuses is refused before
//     any of this runs, in the gate's own sentence.
//   - What the confirmation is. The flag's only effect on a step is through
//     initflow.Conditions.Ask, which GateConfirms answers false to: the step
//     falls back to the confirmation the run would have had if the project were
//     not managed, which is that step's own prompt, its own class and its own
//     words (the project's name typed, a per-build confirmation, a y/N).
//
// The refusal is a refusal and not a fallback (initflow.AskRefusal): a managed
// project's --ask where nobody can answer refuses the run, and it does NOT let
// the gate's verdict stand in instead. Falling back would be the flag's own
// contradiction — the owner asked to be asked — and it would be the one path in
// this program where a flag silently proceeds.

// askFlags is one command's two flags as they reach the gate: --ask and --yes.
// They are read together because they are one decision, and they are never
// equal (see askConflict).
type askFlags struct {
	ask, yes bool
}

// askConflict refuses the two flags together, before anything is read: they say
// opposite things about the same confirmations, and a run that silently
// honoured one would be a run whose owner cannot tell which. It is a usage
// refusal and not a decision about a project, which is why it is made here and
// not by initflow.AskDecision (that one needs the marks).
func askConflict(f askFlags) error {
	if f.ask && f.yes {
		return userErr("%s", initflow.AskAndYes())
	}
	return nil
}

// askDecide is the whole of what --ask does once the marks have been read: the
// line the run prints, or the refusal (initflow.AskDecision is the rule; this
// is its printing). managed is what the read answered — the gate helper's own
// value in each command, false for a project that is not managed, for a mark
// that could not be read and for a command that read nothing at all, which are
// one answer to every caller.
//
// A run that did not pass the flag prints nothing and decides nothing, so a
// command can call this on every path without asking whether the flag is set.
func askDecide(out io.Writer, f askFlags, managed bool, c initflow.Conditions) error {
	if !f.ask {
		return nil
	}
	line, refusal := initflow.AskDecision(c, managed)
	if refusal != "" {
		return userErr("%s", refusal)
	}
	fmt.Fprintln(out, line)
	return nil
}

// askConditions is the session a command's --ask is decided under, for a
// command that does not hold an initRun: its own stdin, its own --json and
// --non-interactive, and the marker of the session it runs in. It is the same
// set of facts initflow.Conditions holds for the converge
// (initRun.conditions), read from the command the caller already has so the two
// cannot disagree.
func askConditions(cmd *cobra.Command, json, nonInteractive bool) initflow.Conditions {
	return initflow.Conditions{
		Terminal:       stdinIsTerminal(cmd.InOrStdin()),
		JSON:           json,
		NonInteractive: nonInteractive,
		Agent:          agentMarker(os.Getenv),
	}
}
