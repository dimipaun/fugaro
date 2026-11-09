package cli

import (
	"context"
	"errors"
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
// Why the command needs it at all. What this command writes is the file that
// decides where a named account's token may be sent (`allow_data_to`) and which
// profile serves it, and a coding agent's session has always been refused it —
// --yes included, spec §2.4 — because that file is exactly the one a session
// must not choose. In a managed project the project's own gate answers that
// question per step instead: the DECLARATION is a document a plan gate can read
// (the name, the repositories, the profile's coverage), so a session that
// declares a bounded account is admitted to write it, and the owner gets a
// record of what was declared and by which rules.
//
// What the gate never admits here, and where each refusal is made:
//
//   - A widening (`--repos` naming a repository beyond the checkout this
//     command runs in, D868). The rule is the command's own — the sentence the
//     owner's own write carries — and the gate is asked whether the step may
//     pass WITHOUT that person. A session cannot answer for a widening on the
//     owner's behalf, so the gate refuses it: the token of an account declared
//     here may be sent to a repository the run cannot see, which is the one
//     thing `allow_data_to` exists to bound.
//   - A token entry. `--claude` runs a login and then reads a token, and a
//     secret entering the project is the class no plan gate admits (D1026): no
//     plan is a token, so a run carrying one is refused before the login is
//     screened. The value itself is refused at the step that would read it —
//     accountsAddTokenStep below — so a session that pipes one at the hidden
//     prompt reads nothing, stores nothing, and is told which command to run at
//     its own terminal.
//   - The reserved names, the profile's coverage and the subset rule. These are
//     not restated here and must not be: they are config.ValidateClaudeAccounts'
//     and accountsAddSubsetRule's own decisions, made before this command has a
//     document to judge, in the words the loader prints for the same file. What
//     the gate adds is that the whole of what the step would write was judged
//     and by which rules, which is what the record holds.
//
// Where the read happens, and why it is exactly where this command would ask
// (D1015, and the owner's ruling of 2026-11-01). In a coding agent's session the
// gate stands in for the refusal SD9 makes; in a person's own terminal the write
// is the owner's own, so it is read there too — and only there: a run with no
// terminal and no --ask (settingsGateReads, and settingsGateAsks) writes
// without asking anything, so it has no question for the gate to answer. A run
// that asks nothing reads nothing, which is what
// keeps a project whose marks are stale, unreadable or on another machine from
// slowing down or stopping a command that was never going to ask. Everywhere
// else a read that does not answer fails closed: readManagedGate's own error, a
// client that cannot be built and a budget database that cannot be opened all
// leave the zero Managed behind, which is this command's own behaviour exactly
// as it was before this row landed.
//
// The one class the owner's own terminal is NOT read for is the one the gate
// refuses: a widening and a token entry are the owner's own step
// (accountsAddOwnerOnly), and there the gate's answer IS the refusal it keeps
// for a session — so reading the marks would leave the declaration written only
// by --yes and never by the terminal D1552 and D1555 name as its door. It is
// the same term `settings set` holds its typed edit in (settingsGateReads),
// which is why the two commands cannot come to read the marks for a step the
// owner is the only one who may make.

// accountsAddTypedEdit is whether any edit this command makes is a Typed class
// one, and the answer is no: a widening is REFUSED rather than typed
// (accountsAddRules) and a token entry is refused at its own step
// (accountsAddTokenStep), so there is no Typed step here the gate has to leave
// to the owner's own word.
//
// Why it is named rather than written as a literal at the call below. The value
// reaches settingsGateAsks's class term, whose answer
// for a coding agent's session is "read the marks" for every document that is
// not repository-scoped, or for a typed edit — and this command's document is
// the project layer, so the parameter
// decides nothing today. What the name does is put the question where a later
// typed edit has to answer it: a command that grows one (a rotation, say)
// changes a line that says what the answer means, instead of a `false` a reader
// would have to look up.
//
// The terminal term of settingsGateReads is answered by this command's rules and
// not by this constant: a step is the owner's own here when a rule refuses it
// (accountsAddOwnerOnly), whatever its class is.
const accountsAddTypedEdit = false

// accountsAddGate is the whole of this command's call of the gate: read, judge,
// admit. It answers the value the step is decided under (the zero value is
// "not managed", which keeps today's refusal), the gate itself —
// whose recorded call the caller makes after its write, and whose zero value
// records nothing — and the gate's own refusal, in the gate's own words, when
// the gate refused the document.
//
// doc is the file as this command would write it, and it is the same bytes the
// rules above were read from and the same bytes the caller applies: the digest
// the record carries is taken from here (D1022), so the document judged and the
// document applied are one artifact and not two that happened to agree.
func accountsAddGate(ctx context.Context, cmd *cobra.Command, lc *localcfg.Config, o accountsAddOptions, name, profile string, beyond []string, doc []byte) (initflow.Managed, managedGate, error) {
	marker := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin()))
	// The rules this command's document is judged by, read here rather than
	// beside the admit below because one of their answers decides whether the
	// gate is read at all: a rule that refuses this step is a rule saying the
	// owner is the only one who may make it, and the gate's answer to such a
	// step at the owner's own terminal is a refusal the terminal may not be
	// given (accountsAddOwnerOnly). The two lists are the verdict below and the
	// record's rules as well, so the document judged, the door the step has and
	// the record of it are one reading of one document.
	applied, refused := accountsAddRules(name, profile, beyond, o.claude)
	// The read is made where this command would ask, and --ask is one more
	// such place: the flag has to speak about the marks in every session that
	// passes it (a refusal in a managed project's agent session, a sentence
	// everywhere else), so it forces the read even where the declaration
	// itself would have asked nothing (a session with no terminal at all).
	if !settingsGateAsks(marker, settingsGateReads(cmd, o.yes, o.ask, accountsAddOwnerOnly(refused)), settingsDocLocal, accountsAddTypedEdit) {
		return initflow.Managed{}, managedGate{}, nil
	}
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
	if err != nil {
		// No installation to read, a mark that did not answer, or the ownership
		// question's own refusal: the project is not one this program can prove
		// is managed, so the session keeps this command's refusal — the same
		// sentence it read before this row landed — and nothing is recorded.
		return initflow.Managed{}, managedGate{}, nil
	}
	applied, refused := accountsAddRules(name, profile, beyond, o.claude)
	conv := managedmode.ConvergeAdmitted(doc, initflow.YesOrTyped, applied)
	if len(refused) > 0 {
		conv = managedmode.ConvergeRefused(doc, initflow.YesOrTyped, refused)
	}
	// YesOrTyped is the class of the step this command makes: the ordinary edit
	// the diff above describes, and not a Typed step — the widening directions
	// D846 reserves the typed name for are money and permanent changes, and what
	// is written here is neither — and D1026's class rule reads this value rather
	// than trusting the class to be one a plan judges.
	m, err := gate.admit(ctx, initflow.YesOrTyped, conv)
	if err == nil {
		return m, gate, nil
	}
	if errors.Is(err, managedmode.ErrNotConverged) {
		// The gate's own sentence, whole and unwrapped: the session reads which
		// rule refused, and errors.Is still finds the gate's refusal inside it.
		// It is a user error and not a failure of this program — the session
		// asked for a step the project's gate does not admit — and the record of
		// the refusal was written by admit, before it returned.
		return m, gate, &ExitError{Code: ExitUserError, Err: err}
	}
	// The second read did not answer, immediately before the write: a mark that
	// cannot be read is not a permission, and the session keeps this command's
	// own refusal.
	return initflow.Managed{}, managedGate{}, nil
}

// accountsAddRules are the rules the gate judges this command's document by, in
// the words the record and the refusal carry: the rules that were applied and
// passed, and the ones that refused. They are this command's own and they live
// here rather than in internal/managed, where the SHAPE of a verdict and the
// one class rule are (D1021): a rule about account names and repositories is
// this command's to state, and the gate's job is to hold the verdict they reach
// and the class they are judged under.
//
// The repository rules are the ones accountsAddBeyond and accountsAddSubsetRule
// already decided, read back rather than re-derived: beyond is the widening
// the sentence names, and the profile's coverage was refused before this
// command had a document at all. What the gate adds is the answer an agent's
// session needs — that this step's own rules are the whole of what was judged
// — and a rule that passed is written into the record beside the ones that did.
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
		applied = append(applied, "the step carries no token entry: --claude is absent, and the token this command reads is read from a terminal")
	}
	return applied, refused
}

// accountsAddTokenStep is the one step of this command no gate admits: the
// secret. Storing the account's token project-wide is a Typed step by design
// (a secret is money or permanent, D1026), and a plan gate judges a plan — so
// the value is read by nobody in a coding agent's session, which is the half of
// spec §4.2's "no token value is ever written by either" that the declaration
// alone cannot give.
//
// It is returned rather than skipped, because a session that piped a value at
// the hidden prompt has to be told that it was not stored: a run that went
// quietly on would leave the session believing the account was ready when the
// one thing that makes it usable was never read.
//
// Why it is not conditional on the gate's answer: it is the same answer for
// every session, and it is unreachable without one. The two fences above — the
// one in applySettingsEdit and the one on the no-change path — are what put an
// agent's session here, and both of them are lifted only by an admission from a
// project the gate read as managed (initflow.CanConfirmManaged).
func accountsAddTokenStep(marker, name, command string) error {
	return userErr("refusing to read %s's token in a coding agent's session (%s is set): a token is a secret, and a secret is the class of step a managed project's gate never admits — no plan is a token — so this command stops here, nothing is read off this session's stdin and no token was stored; the declaration above is the whole of what this session may write; run this yourself at your own terminal: %s",
		pluginwire.Printable(name), marker, command)
}
