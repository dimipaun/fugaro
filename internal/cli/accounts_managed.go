package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// This file is `fugaro accounts add`'s call of the managed gate: M6's ONE
// helper, read and called from here, with the rules this command's own document
// is judged by (the managed plan's §2 accounts handoff row, D1012).
//
// What this command writes is the file that decides where a named account's
// token may be sent (`allow_data_to`) and which profile serves it, and it then
// reads the token itself and stores it project-wide. The two halves have two
// different doors, and the owner's ruling of 2026-11-09 is what settles the
// second: on a managed project the declaration is a document a plan gate can
// read (the name, the repositories, the profile's coverage), so a coding
// agent's session that declares a BOUNDED account is admitted to write it
// (D1012), while the token — money or permanent, D1026 — is answered for by the
// owner, and only at the owner's own terminal.
//
// Where each door is, and which rule owns it:
//
//   - The owner's own terminal (accountsAddOwnTerminal). This is the door D1552
//     and D1555 name for a step only the owner may make, and the ONE exception
//     to "a managed project asks nothing" (the owner's rulings of 2026-11-04 and
//     2026-11-09): a secret is money or permanent (D1026), so the owner is
//     asked, and only the owner. What they answer is the one confirmation that
//     names exactly what the token may be used by — the repositories the account
//     is declared for and the sentence saying a job on them can read the token —
//     and the answer is the typed name of the project, on the seam
//     confirmTypedStrict already is for every other owner-only step: no flag
//     stands in for it, and --yes never satisfies it.
//
//   - A widening (`--repos` naming a repository beyond the checkout this command
//     runs in, D868). The rule is the command's own — the sentence the owner's
//     own confirmation carries — and the gate is asked whether the session may
//     pass WITHOUT that person. A session cannot answer for a widening on the
//     owner's behalf, so the gate refuses it: the token of an account declared
//     here may be sent to a repository the run cannot see, which is the one
//     thing `allow_data_to` exists to bound.
//
//   - A token entry (`--claude`, which runs a login and then reads a token). A
//     secret entering the project is the class no plan gate admits (D1026): no
//     plan is a token, so a session is refused it before the login is screened.
//     The value itself is refused at the step that would read it —
//     accountsAddTokenStep below — so a session that pipes one at the hidden
//     prompt reads nothing, stores nothing, and is told where to run it.
//
//   - A caller that is neither: a pipe, a run with no terminal, --json, or the
//     same command with --yes. Nothing here can be asked of a person, so on a
//     managed project the step is refused before a byte is written — which is
//     the half of this row that had no fence at all, since the hidden prompt is
//     fed by a pipe as readily as by a hand.
//
//   - The reserved names, the profile's coverage and the subset rule. These are
//     not restated here and must not be: they are config.ValidateClaudeAccounts'
//     and accountsAddSubsetRule's own decisions, made before this command has a
//     document to judge, in the words the loader prints for the same file.
//
// Where the read happens, and why it is exactly where this command would ask
// (D1015, and the owner's ruling of 2026-11-01). Every caller reads the marks
// now, and that is the ruling of 2026-11-09 rather than a leftover: the answer
// "is this project managed" is what decides whether the owner is asked at all,
// so a caller that skipped the read could not know whether it was the owner's
// step. A read that does not answer fails closed the way it always did — the
// zero Managed behind it is this command's behaviour exactly as it was before
// the gate existed — and what a failed read costs a caller is the refusal it
// would have made in a project that is not managed: the owner's terminal writes
// without a confirmation, and a session keeps the fence it always had.
//
// What this file does NOT do is record the owner's own step. The audit record
// is the GATE's (D1014, spec §6): it holds what the gate judged and which rules
// it judged it by, and a step the owner answered at their own terminal is not a
// verdict of the gate at all. The refused steps below are recorded, because the
// gate refused them.

// accountsAddOwnTerminal is whether this run is the owner at their own
// terminal: the one session D1552 and D1555 name as the door of a step only the
// owner may make (the owner's ruling of 2026-11-09).
//
// It is read the way this tree already reads it, and never any other way: a
// real terminal on the run's input, a real terminal on its output, no coding
// agent's marker in the environment, and no --yes. A flag, a variable or a file
// is not one of the four: a session can set all three, and the terminal it
// cannot. The marker is agentenv.Session, so the Claude Code IDE extension's
// integrated terminal — CLAUDE_CODE_SSE_PORT with a real terminal and no
// agent-set marker — is a person's own terminal and is admitted, which is the
// same reading the typed confirmations already take (the T5 work of 2026-11-08).
//
// Why the output stream as well as the input. The confirmation prints the
// repositories the token may be sent to, and the person answers on the screen
// they read it on; a run whose output is a pipe has nowhere to print the
// argument, and a confirmation nobody can read is not one. --yes never
// satisfies it (the ruling is explicit): the flag makes the run a caller that
// answers nothing, which is the class the refusal below is for.
func accountsAddOwnTerminal(cmd *cobra.Command, marker string, yes bool) bool {
	return marker == "" && !yes && stdinIsTerminal(cmd.InOrStdin()) && writerIsTerminal(cmd.OutOrStdout())
}

// accountsAddTokenWhy is why this command asks at all, in the words
// confirmTypedStrict's own refusal ends with when there is no terminal to ask
// at. It is here rather than at the call below because it is the same fact the
// two lines of accountsAddRefusal state, and a reader comparing the two should
// find one reason written twice.
const accountsAddTokenWhy = "this decides where a Claude account's token may be sent, and only its owner may say that"

// accountsAddTokenConfirmation is the one confirmation the owner answers for
// this command on a managed project: the repositories the token may be sent to,
// the sentence saying what that means, and then the project's own name typed at
// the terminal (confirmTypedStrict' seam — no flag stands in for it, and --yes
// never satisfies it).
//
// Why the repositories and not a summary of them. D868's question is exactly
// "may a job on these repositories read this token", so the list is the
// argument the owner answers: it is printed whole, bounded and quoted the way
// every list this command prints is (pluginwire.Printable), and it is the list
// the file will carry rather than the --repos that was passed — re-declaring an
// account adds to the list it already has.
//
// Why the typed name and not a y/N. It is the seam every other owner-only step
// of this program already uses (orchestrator_patch, the policy publish, the
// retirement), it is the one a session cannot answer with a pipe, and it is the
// one the hidden token prompt below is deliberately NOT: reading a value is not
// answering for what that value may be used by.
func accountsAddTokenConfirmation(cmd *cobra.Command, lc *localcfg.Config, name string, allow []string) error {
	what := fmt.Sprintf("claude_accounts.%s.allow_data_to = [%s], and a job on these repositories can read this token",
		pluginwire.Printable(name), strings.Join(allow, ", "))
	return confirmTypedStrict(cmd, lc, accountsAddTokenWhy, what)
}

// accountsAddRefusal is the whole of what a caller that is not the owner's own
// terminal reads, and it is deliberately two short plain lines: what happened,
// and the one next step. It names no rule number — a person acting on it needs
// the step, not the design's vocabulary, and the rules themselves are in the
// audit record the gate wrote and in this file's own comments above (D1012 for
// the gate, D1026 for the token, D868 for the widening).
//
// Why the two shapes of the second line. A coding agent's session is refused a
// widening for a reason it can act on inside itself: the declaration is a
// document the gate admits when it is bounded (D1012), so naming only this
// checkout's repository is the step that unblocks it, and the token it then
// cannot read is what its own refusal at accountsAddTokenStep says. Every other
// caller — a pipe, a run, --yes — has no narrower step to take and no
// declaration to be admitted to: the one next step is the owner's terminal.
//
// The account's name and the repositories are bounded and quoted where they are
// printed, because both come from a command line and from a file this command
// is about to write.
func accountsAddRefusal(marker, name string, beyond []string, claude bool) error {
	what := fmt.Sprintf("This adds and stores the token of Claude account %s, and a managed project asks its owner for that.", pluginwire.Printable(name))
	if len(beyond) > 0 {
		what = fmt.Sprintf("This would send the token of Claude account %s to %s, beyond this checkout.", pluginwire.Printable(name), strings.Join(beyond, ", "))
	}
	next := "Run this in your own terminal."
	switch {
	case marker != "" && len(beyond) > 0 && !claude:
		next = "Name only this checkout's repository, or run without --repos."
	case marker != "":
		next = "Run this in your own terminal, not in an agent session."
	}
	return userErr("%s\n%s", what, next)
}

// accountsAddGate is the whole of this command's call of the gate: read, judge,
// admit — and, in the owner's own terminal, ask. It answers the value the step
// is decided under (the zero value is "not managed", which keeps today's
// refusal), the gate itself — whose recorded call the caller makes after its
// write, and whose zero value records nothing — and the refusal, in the words
// of whichever door refused this run.
//
// doc is the file as this command would write it, and it is the same bytes the
// rules above were read from and the same bytes the caller applies: the digest
// the record carries is taken from here (D1022), so the document judged and the
// document applied are one artifact and not two that happened to agree. allow
// is that document's own list for this account — the repositories the token may
// be sent to — which is what the owner's confirmation prints.
func accountsAddGate(ctx context.Context, cmd *cobra.Command, lc *localcfg.Config, o accountsAddOptions, name, profile string, beyond, allow []string, doc []byte) (initflow.Managed, managedGate, error) {
	marker := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin()))
	owner := accountsAddOwnTerminal(cmd, marker, o.yes)
	// The rules this command's document is judged by, in the words the record
	// and the gate's refusal carry. They are this command's own and they live
	// here rather than in internal/managed, where the SHAPE of a verdict and the
	// one class rule are (D1021): a rule about account names and repositories is
	// this command's to state, and the gate's job is to hold the verdict they
	// reach. The two lists are the verdict and the record's rules as well, so
	// the document judged and the record of it are one reading of one document.
	applied, refused := accountsAddRules(name, profile, beyond, o.claude)

	// The marks are read for every caller, because their answer is the
	// question this command asks first: is this project one whose owner is
	// owed the confirmation below? A read that does not answer leaves the zero
	// Managed behind, which is this command's behaviour exactly as it was
	// before this row landed: the owner's terminal writes without a
	// confirmation and a session keeps the fence it always had.
	clients, err := newInitClients(ctx, lc)
	if err != nil {
		return initflow.Managed{}, managedGate{}, nil
	}
	_, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return initflow.Managed{}, managedGate{}, nil
	}
	gate, err := readManagedGate(ctx, managedGateEnv{lc: lc, s: clients.Storage, crm: clients.CRM, db: db}, managedGateFacts{
		Subcommand: "accounts add",
		Argv:       os.Args[1:],
		// What this command would have written, for the ownership question
		// D1017 asks with the database it is about to write: an account's
		// declaration is a policy the project's own database carries, and a
		// database this installation may not write has no business clearing
		// anything.
		Declined:    "nothing was declared",
		AgentMarker: marker,
		// The whole-project inventory (D59) is `fugaro managed apply`'s read and
		// is not consulted on this path: a command that has none passes false,
		// which is the documented answer for it (managedGateFacts).
		InventoryFallback: false,
	})
	if err != nil || !gate.managed {
		// No installation to read, a mark that did not answer, the ownership
		// question's own refusal, or a project that is not managed: the step is
		// not one this project asks its owner for, so every door below is the
		// one it has always had.
		return initflow.Managed{}, managedGate{}, nil
	}

	if owner {
		// The one exception to "a managed project asks nothing": the step's
		// owner is here, and what they answer is the confirmation naming
		// exactly what the token may be used by. The gate's own verdict is not
		// asked for and could not be: a widening and a token entry are the two
		// classes the gate refuses outright (accountsAddRules), and handing the
		// person standing at the door the refusal kept for a session would
		// leave the declaration written only by --yes and never by the terminal
		// D1552 and D1555 name. Nothing is recorded either: the record is the
		// gate's, and this step is the owner's.
		if err := accountsAddTokenConfirmation(cmd, lc, name, allow); err != nil {
			return initflow.Managed{}, managedGate{}, err
		}
		// Managed and NOT admitted: the marks answered "managed" — the value
		// every sentence about this project is decided under, so --ask prints
		// its own line and asks the one question it restores instead of the
		// line for a project that is not managed — while Admitted stays false,
		// because this step was not taken on any verdict of the gate's. It is
		// the truthful pair, and it is also the one that changes nothing else:
		// GateConfirms needs the admission, so the write below proceeds on the
		// confirmation just answered and on nothing else.
		return initflow.Managed{Managed: true}, managedGate{}, nil
	}

	if marker == "" {
		// A pipe, a run with no terminal, --json, or --yes at a terminal: no
		// person is there to be asked, so the step is refused before the file
		// is touched and before the login is screened. The gate is not asked to
		// judge a document nobody may write.
		return initflow.Managed{}, managedGate{}, accountsAddRefusal(marker, name, beyond, o.claude)
	}

	// A coding agent's session: the declaration is judged, and the gate's
	// verdict is what decides it. A refused document is returned in this
	// command's own two lines rather than in the gate's, and the record of the
	// refusal was written by admit before it returned (D1014).
	conv := managedmode.ConvergeAdmitted(doc, initflow.YesOrTyped, applied)
	if len(refused) > 0 {
		conv = managedmode.ConvergeRefused(doc, initflow.YesOrTyped, refused)
	}
	m, err := gate.admit(ctx, initflow.YesOrTyped, conv)
	if err == nil {
		return m, gate, nil
	}
	if errors.Is(err, managedmode.ErrNotConverged) {
		// The gate's own sentence is replaced and never wrapped: a session acts
		// on the two lines, and the rules that refused — which are what the
		// record holds — are not what it needs to read. It is a user error and
		// not a failure of this program: the session asked for a step the
		// project's gate does not admit.
		return m, gate, accountsAddRefusal(marker, name, beyond, o.claude)
	}
	// The read did not answer, immediately before the write: a mark that cannot
	// be read is not a permission, and the session keeps this command's own
	// refusal.
	return initflow.Managed{}, managedGate{}, nil
}

// accountsAddRules are the rules the gate judges this command's document by, in
// the words the record carries: the rules that were applied and passed, and the
// ones that refused. They are the record's vocabulary and never the refusal's —
// the two lines a refused session reads are accountsAddRefusal's, and the rule
// numbers below (D868's widening, D1026's token) live here and in this file's
// comment, not in a message.
//
// The repository rules are the ones accountsAddBeyond and accountsAddSubsetRule
// already decided, read back rather than re-derived: beyond is the widening the
// sentence names, and the profile's coverage was refused before this command
// had a document at all. What the gate adds is the answer a session needs —
// that this step's own rules are the whole of what was judged — and a rule that
// passed is written into the record beside the ones that did.
func accountsAddRules(name, profile string, beyond []string, claude bool) (applied, refused []string) {
	// The name: config.ValidateClaudeAccounts refused a reserved one and
	// "default" before this point, in the loader's own words, so a name that
	// reaches the gate is one that passed that rule. It is named here because a
	// record that says which rules were applied is what makes the admission
	// checkable later.
	applied = append(applied, "claude_accounts."+pluginwire.Printable(name)+" is not a reserved name and is not the name \"default\"")
	if profile != "" {
		applied = append(applied, "every account profiles."+pluginwire.Printable(profile)+".claude_accounts lists covers every repository its allow_repos admits")
	}
	if len(beyond) > 0 {
		refused = append(refused, "the declaration names "+strings.Join(beyond, ", ")+" beyond the checkout this command runs in, so the token of "+pluginwire.Printable(name)+" may be sent by a job on a repository this run cannot see: a launcher that may start such a run can have it print that token, and only the owner can answer for that (D868)")
	} else {
		applied = append(applied, "claude_accounts."+pluginwire.Printable(name)+".allow_data_to widens no repository beyond this checkout")
	}
	if claude {
		refused = append(refused, "--claude runs Anthropic's login and then reads a token: a secret entering the project is the class of step no plan gate admits (D1026), and no plan is a token")
	} else {
		applied = append(applied, "the step carries no token entry: --claude is absent, and the token this command reads is read at the owner's own terminal")
	}
	return applied, refused
}

// accountsAddTokenStep is the one step of this command no gate admits: the
// secret. Storing the account's token project-wide is a Typed step by design (a
// secret is money or permanent, D1026), and a plan gate judges a plan — so the
// value is read by nobody in a coding agent's session, which is the half of
// spec §4.2's "no token value is ever written by either" that the declaration
// alone cannot give.
//
// It is returned rather than skipped, because a session that piped a value at
// the hidden prompt has to be told that it was not stored: a run that went
// quietly on would leave the session believing the account was ready when the
// one thing that makes it usable was never read. The two lines are
// accountsAddRefusal's shape for the same reason: what happened, and the one
// next step. The marker is not named here — the second line is the same whoever
// set it, and the session already knows which one it is.
//
// Why it is not conditional on the gate's answer: it is the same answer for
// every session, and it is unreachable without one. The two fences above — the
// one in applySettingsEdit and the one on the no-change path — are what put an
// agent's session here, and both of them are lifted only by an admission from a
// project the gate read as managed (initflow.CanConfirmManaged).
func accountsAddTokenStep(name string) error {
	return userErr("No token was stored for Claude account %s: a session may write the declaration, and never the token.\nRun this in your own terminal, not in an agent session.",
		pluginwire.Printable(name))
}
