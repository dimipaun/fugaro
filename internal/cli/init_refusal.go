package cli

import (
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The refusal a person reads when the guard stops a --repo run.
//
// Guard's own text is written for a reader who acts on an address: it prints
// every refused change as "<address> (<actions>)", and in a project that is not
// managed it adds a hint per delete naming the flag that would keep it. That is
// the right text for --json, for a script, and for an owner recording an entry
// in a delete list — and it is the wrong text for the person at the terminal
// who ran `fugaro update --repo` and met, on 2026-11-24, two init banners,
// Terraform's whole plan, a "Review these first" list of attribute paths, and a
// refusal that ended by telling them to go and find the project's owner.
//
// So the standalone door prints this instead, to the person at the terminal
// alone: what the plan does in Fugaro's words, the count, five of the refused
// addresses with the log holding every one of them, and the one command that
// goes on — except in a managed project, where the block keeps Task 3.2's rule
// and names no way past itself at all. A coding agent's session and a run with
// no terminal keep Guard's own text (guard), which is what keeps --json byte
// for byte the document it was.

// refusalShown is how many refused addresses the block lists before it points
// at the log: five, the number a person reads at a glance, and the rest are one
// line away.
const refusalShown = 5

// guardRefusalBlock is this run's guard refusal as the person at the terminal
// reads it. advice is the sentence the caller's mode ends a refusal with (the
// managed one or Guard's own).
//
// namesFlags is whether this mode's refusal may name a way past itself: the
// hints DeleteHints computes (what says which flag would keep a delete) and the
// one command that goes on. A managed project's refusal names no flag at all
// (Task 3.2's rule, and its test reads every "--" out of the text), and the
// next command is a fugaro command line — so the managed modes print neither,
// and their block ends at what it refused and whose decision it is.
//
// It is built only where there is a log to keep the rest in (guard): a block
// that pointed at nothing would be the same refusal with the addresses taken
// away.
func (r *initRun) guardRefusalBlock(plan *tf.Plan, allowDelete []string, advice string, namesFlags bool) string {
	refused := tf.Refused(plan, allowDelete)
	var b strings.Builder
	fmt.Fprintf(&b, "this run would delete or replace %d %s, and nothing was applied.\n",
		len(refused), plural(len(refused), "resource", "resources"))
	if w := tf.Removals(plan, 3); w != "" {
		fmt.Fprintf(&b, "what goes: %s.\n", w)
	}
	if len(refused) > 0 {
		fmt.Fprintf(&b, "refused (%d):\n", len(refused))
		for i, a := range refused {
			if i == refusalShown {
				break
			}
			fmt.Fprintf(&b, "  %s\n", safetext.Field(a))
		}
		if n := len(refused) - refusalShown; n > 0 {
			fmt.Fprintf(&b, "  +%d more, see %s\n", n, r.repLog.Path())
		}
	}
	if namesFlags {
		for _, h := range infra.DeleteHints(plan, allowDelete) {
			fmt.Fprintf(&b, "hint: %s\n", h)
		}
	}
	if advice != "" {
		b.WriteString(advice + "\n")
	}
	if cmd := r.nextAfterRefusal(); namesFlags && cmd != "" {
		fmt.Fprintf(&b, "Next command:\n  %s\n", cmd)
	}
	return strings.TrimRight(b.String(), "\n")
}

// nextAfterRefusal is the one command that goes on after this door's refusal:
// the retirement's own command where this run holds a retirement set, which is
// the case the refusal was written for — the addresses the guard refuses are
// the ones the set predicted, and --retire is the flag that removes them from
// Terraform's state first, destroying nothing (retireNextCommand). It is ""
// where there is no set, and the block then ends at what it refused rather than
// naming a command that would not help.
func (r *initRun) nextAfterRefusal() string {
	if len(r.res.Retire) > 0 || r.retired {
		return retireNextCommand
	}
	return ""
}

// managedDeleteAdviceTerminal is initManagedDeleteAdvice for the person who
// owns the project and is standing at the terminal that ran the command.
//
// The agent's sentence — "made by the owner and not by this session", "say
// which address is refused to the person who owns the project and stop" — is an
// instruction to a reader who is neither the owner nor able to take the step,
// and it is what the owner read about their own project on 2026-11-24. The
// owner is told the fact that is true of their own run instead, in the one
// sentence a managed refusal is allowed (Task 3.2): nothing was applied and the
// decision is the owner's. It names no flag, and with namesFlags false the
// block names no way past itself at all, which is what the managed refusal says
// in every project and at every call site.
//
// The way past a managed refusal that the text withholds is still the flag:
// guard hands --allow-delete to tf.Guard in every project, managed or not, and
// TestManagedInitApprovesExactlyWhatAnyInitApproves pins that meaning. What a
// managed project changes is who may pass it: an agent's session is refused
// whatever the address came from, which is why no managed refusal says it.
const managedDeleteAdviceTerminal = "This is a managed project: deleting or replacing fugaro's own resources in it is its owner's decision, and nothing was applied."

// managedReadFailedTerminal is the sentence the refusal above ends with when
// whether the project is managed could not be read at all. It keeps the fact
// the agent's text states — this refusal names no way past it — and drops the
// instruction to report to the owner, which the owner is.
const managedReadFailedTerminal = "Whether this is a managed project could not be read, so this refusal names no way past it: nothing was applied — rerun once the read answers."

// refusalIsShort is whether this run's guard refusal is the block above rather
// than Guard's own text: the standalone --repo door, a person at a real
// terminal, and a log to keep every address the block leaves out in.
//
// Those three are the whole of the rule. A coding agent's session is refused in
// the words written for it (initManagedDeleteAdvice, which names no flag an
// agent would then pass); a run with no terminal — --json, a pipe,
// --non-interactive — keeps today's text byte for byte, so the --json document
// and a scripted run are unchanged; and the converge's own stages are neither,
// because their plans are printed and confirmed by the loop, which has its own
// account of them.
func (r *initRun) refusalIsShort() bool {
	return r.repoDoor && r.rep != nil && r.repLog != nil && initflow.PersonAtTheTerminal(r.conditions())
}
