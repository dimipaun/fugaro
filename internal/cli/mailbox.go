package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/mailbox"
	"github.com/dimipaun/fugaro/internal/mailbox/keychain"
	"github.com/dimipaun/fugaro/internal/procgroup"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// fugaro mailbox (mobile P4.1, Task T5): the consumer side of an account's
// mailbox, as one long-lived local process the operator runs beside the
// orchestrator host. docs/mobile/mailbox-consumer-plan.md is the
// rule this file implements: §4 its surface, §6 where its state lives, §7 its
// loop, §8 the two directions of the MAC it runs, §9 and M12 its refusals and
// the exit codes they carry.
//
// **What this file is, and what it is not.** It is the command: the flags, the
// startup checks, the pass, and what it prints. Everything the pass does with a
// message it does through a merged package — the inbound gate, the outbound
// signer and the keychain are internal/mailbox's and internal/mailbox/
// keychain's (T2, T2a), the lease on the heartbeat is mailbox.Take and
// mailbox.Renew (T3), the dedupe ledger and its commit-before-acting fence are
// mailbox.Perform (T4) — so there is no second reader here and no second tag
// function. What this file decides is which node is read, in which order, and
// what a pass is reported as. It writes the outbox, the heartbeat and — for a
// registration message — the sibling push node `config/admin/push/$uid`
// (P5.2.4), all three under the operator's own ADC (C0).
//
// **The action runner is the file's, and it is where D128 is enforced (P6.7).**
// A message the ledger accepted reaches one of five answers, decided by its
// kind and by the record it carries: a typed **action record** is re-validated
// against `shared/confirm`'s rules by `mailbox.ConfirmValidateAction`, spent
// against this machine's memory and then run — cancel through this CLI's own
// cancel path, merge as D169 says, under the operator's own `gh`; an **answer**
// settles the prompt it names, and an answer to a destructive prompt is refused
// unless it arrived as an action record with a confirmation; a **message** is
// delivered to the orchestrator, unless it is destructive free text this file
// recognises, which is refused; a **registration** is applied to the sibling
// push node the phone's token lives in, and to nothing else (P5.2.4,
// runRegistration); and a `prompt` is a kind that does not travel phone to host,
// refused with this file's own code. A
// destructive command without a valid confirmation record is refused, which is
// §9's standing position and the reason M12's fourth clause exists: a pass whose
// only reading was one of those is exit 1, because the operator asked for one
// thing and nothing happened.
//
// **The one answer whose failure is not final is the registration's**, and it is
// the only place this file takes a record back out of the ledger
// (mailbox.Ledger.Retract, registrationOwed): a write the store refused or could
// not be reached for is owed by the next pass rather than consumed, because the
// act it could not make is convergent — the phone's own token, written to the
// phone's own push node — and a registration nobody applies is invisible to the
// phone that sent it.
//
// **Two things the consumer decides on its own clock**, so that neither waits on
// a process that may not run: a command is honoured only inside the six-hour
// window measured from its confirmation's own time, and a prompt expires at
// D118's TTL whether or not the owner-run sweeper (P6.13) has deleted the stored
// copy. The answer a prompt's expiry hands the orchestrator is handed over
// **exactly once**, recorded in the same restart-surviving memory the spent ids
// live in.
//
// **What the orchestrator is handed is a seam.** The consumer is run beside the
// orchestrator as one local process, and the channel between the two is not in
// this tree: `mailboxHandOff` is where it attaches, and until it does the
// hand-off is recorded by id and outcome in the pass's own document. **The
// answer's text is never printed** — §4.3's rule holds for an answer this
// consumer produces as much as for one it reads — so what a reader of the
// document sees is that a prompt was settled and how, never what it said.

// mailboxPromptTtlMs is D118's 72 hours in milliseconds: the number
// `shared/prompts` exports as PROMPT_TTL_MS (M3, §10). It is bound here rather
// than declared because a ledger's horizon is max(window, prompt TTL) and both
// are the module's numbers; TestMailboxPromptTtlIsTheModulesNumber holds this
// one to shared/prompts/index.js, so the two cannot drift. Since T7 it is
// `internal/mailbox`'s own constant rather than a second copy of the
// arithmetic: the sweeper in that package draws the same boundary, and two
// spellings of one number is one spelling too many.
const mailboxPromptTtlMs = mailbox.PromptTTLMs

// mailboxPromptRetentionMs is how long this machine's memory of a spent id
// lasts: D118's TTL plus one sweep interval, so a replay cannot outlive the
// record of it by arriving just after the window closed. It is the same pair of
// numbers the delivery ledger's horizon is built from (§6, M3), and the sweep
// interval is bound here rather than chosen because the owner-run sweeper's
// cadence is the budget sweeper's.
func mailboxPromptRetentionMs() int64 {
	return mailboxPromptTtlMs + budget.SweepEvery.Milliseconds()
}

const (
	// mailboxDefaultInterval is `--interval`'s default: how long the loop waits
	// between passes. The operator runs this beside the orchestrator, and a
	// pass is one heartbeat write and one inbox read, so five seconds is cheap.
	mailboxDefaultInterval = 5 * time.Second
	// mailboxMinInterval is the floor `--interval` is held to (§4.4): below a
	// second the loop would spin against the database, and the refusal is the
	// flag's rather than the store's.
	mailboxMinInterval = time.Second
)

// mailboxStaleReply is what a delivery the consumer consumed but performed
// nothing for is answered with (§7's stale-pending, M2): the phone is told, and
// the sentence is this command's own — no module declares one. It carries none
// of the message's text, because §4.3's rule is that an id may appear in this
// command's output and a message's text never does.
const mailboxStaleReply = "nothing was performed: the message arrived outside its window"

// mailboxStopReply is what the phone is told about a **stop request** — the
// schema's `stop` kind, which the app writes when the human says to stop what
// they are looking at (mobile plan task P5.1g, the owner's answer Q10(c)). Like
// mailboxStaleReply it is this command's own sentence — no module declares one
// — and it carries none of the message's text, because §4.3's rule is that an
// id may appear in this command's output and a message's text never does.
//
// It carries the two halves the resident orchestrator's own answer carries
// (internal/orchestrator's consumerStopRequest is that sibling pass's sentence
// for the same request): the request was read, and **nothing was stopped from
// here**, because there is nothing here to stop it with — the enforcement side
// is `config/kill/**` and `config/caps/**`, which no client writes and this
// consumer holds no grant for. It names where the stop can be made, the console
// or the laptop, where a human can act on the request the phone just made, and
// the one thing the phone can always do by itself, which is to revoke its own
// pairing. What it never does is claim a run was stopped: Q10's answer (c) is a
// request and not a phone-writable kill switch, and an answer that pretended
// otherwise would be the lie that answer exists to avoid.
//
// It is deliberately not a prompt in the outbox either, for the reason the
// resident pass's own note gives: a prompt is a question the phone reads out
// and answers, and asking the human to confirm a stop this process cannot
// perform would be exactly the confirmation of its own prompt D168 forbids the
// consumer to synthesise. Nothing raises a prompt for a stop record — no
// console or laptop code does — so the request travels to the human as this
// answer alone.
const mailboxStopReply = "nothing was stopped: this consumer read your stop request, and this installation has no phone-writable kill switch. Stop the run from the console or the laptop, or revoke this device's pairing; the phone cannot stop a run by itself."

// mailboxCodeDuplicate is the module's own word for a redelivery. §4.4 counts
// duplicates among the refusals a pass reports — the message was read and
// nothing was done with it — so a duplicate lands in refused[] beside the codes
// the module's refusal table spells, and in the pass's own duplicates counter.
const mailboxCodeDuplicate = mailbox.Code("duplicate")

// mailboxCodeActionRefused is the code a message refused by §9's fence is
// recorded with. It is D128's answer and not a protocol code: the schema and
// the mailbox module have no opinion about which command an operator's phone
// may ask for, and a message whose text reads as a destructive command is
// refused by this file's own reader rather than by a module's rules.
const mailboxCodeActionRefused = mailbox.Code("refused-action")

// mailboxCodeUnexpectedKind is the code a kind this consumer does not read out
// of an inbox is recorded with. The schema names six kinds and the direction
// decides which of them travel: a `prompt` is written by the host, so one read
// out of the host's own inbox is a message nothing here can act on — refused
// and reported rather than delivered to an orchestrator that would take it for
// a question. A `registration` used to be refused here too; since P5.2.4 it is
// applied (runRegistration), and a `stop` (P5.1g) is answered rather than
// refused, which is why this code names the one kind left.
const mailboxCodeUnexpectedKind = mailbox.Code("unexpected-kind")

// mailboxCodeRegistrationUnpaired is the code a registration whose account has
// no device node is recorded with: the design's own phrase for the state its
// rule 2 rests on ("no device node, no send"), applied one step
// earlier, at the write. The device node's existence IS the allowlist
// (§3.4, D166), and a registration that arrives after `fugaro pair revoke`
// deleted it would otherwise recreate the push node — the orphan §3.4 has the
// sender reap — for an account that is off the allowlist. Writing nothing is
// stricter than the design's own net, and it is what P5.2.4 asks for:
// revocation stays final.
//
// It is this file's own code and not the module's: the module's vocabulary is
// the schema's, and the schema has no opinion about a device node.
const mailboxCodeRegistrationUnpaired = mailbox.Code("unpaired")

// mailboxCodeUnknownPrompt is P6.5's own code for an answer that names a prompt
// the queue does not hold — or does not hold any more, which is the same answer
// to the same question. It is not in `mailbox.ConfirmCodes`: that list is
// `shared/confirm`'s, and this one is the queue module's.
const mailboxCodeUnknownPrompt = mailbox.Code("unknown-prompt")

// mailboxCodeNotExecutable is the code a record that validates but that this
// consumer does not run is recorded with. The verb it happens for is `confirm`,
// which names an action the *orchestrator* holds — the phone's read-back and its
// confirmation are one machine, and what that machine produces for a cancel or a
// merge is a record with the cancel or the merge verb. There is nothing for this
// process to execute for a bare confirmation, so it refuses and reports rather
// than handing the orchestrator a sentence it cannot check.
const mailboxCodeNotExecutable = mailbox.Code("not-executable")

// mailboxCodeActionFailed is the code an action that ran and failed is recorded
// with: the cancel path returned an error, or `gh` did. It is this file's own,
// because the failure is the machine's and not the record's — the record was
// valid, and the operator's answer to it is to look at the error the pass
// printed.
const mailboxCodeActionFailed = mailbox.Code("action-failed")

// mailboxCancelGrace, mailboxCancelFinalize and mailboxCancelPoll are the cancel
// path's own defaults (internal/cli/cancel.go's flags), which a cancel that
// arrives from the phone is run with: the operator's own command, with the
// operator's own settings. The wait is the cost of running the real thing —
// cancel lets the runner open a draft pull request before it stops an execution
// — and it is paid once per confirmed cancel, inside the pass that read it.
const (
	mailboxCancelGrace    = 3 * time.Minute
	mailboxCancelFinalize = 10 * time.Minute
	mailboxCancelPoll     = 10 * time.Second
)

// mailboxOutcomePerformed is what `acted[]` reports for a message an action
// really ran for (§4.3's document). The ledger's own word for it is `acted`,
// which is the *status* it stores; the document's field is the outcome a reader
// sees, and §4.3 names it. An action this process ran itself says which one it
// was, because "something happened" is not the fact the operator asked about.
const (
	mailboxOutcomePerformed = "performed"
	mailboxOutcomeCancelled = "cancelled"
	mailboxOutcomeMerged    = "merged"
	// mailboxOutcomeRegistered is what `acted[]` reports for a registration
	// this consumer applied to the push node (P5.2.4). A registration is
	// neither of the two things the other outcomes name — no message was
	// handed to the orchestrator and no command ran — and "performed" would
	// say the operator's phone asked for something this pass did for it.
	mailboxOutcomeRegistered = "registered"
)

// mailboxPromptRequest is one question the orchestrator asks this consumer to
// put to the owner's phone: the prompt's id, what it says, whether answering it
// would change something, and the non-destructive answer the orchestrator will
// take if the owner says nothing — which is D168's whole point, and is empty
// when the orchestrator has no answer to give, in which case the prompt's expiry
// tells it to stop.
//
// The text is the orchestrator's own and is never printed by this command: it
// travels one way, into a signed outbox message, and no reader of a pass
// document ever sees it.
type mailboxPromptRequest struct {
	ID          string
	Text        string
	Destructive bool
	Safe        string
	Agent       string
}

// mailboxPromptSource is the questions this pass is asked to put to a phone, and
// it is the **one seam** between the consumer and the orchestrator it runs
// beside: that channel is not in this tree, and a prompt's producer is the
// agent's own session. It is a variable for the same reason `mailboxHandOff` is,
// and a test drives it in place of a producer that does not exist yet.
//
// A producer that answers nil is the ordinary case today, and it is not an
// error: a consumer with no orchestrator attached announces nothing, and every
// prompt already in the queue is still expired on this consumer's own clock.
var mailboxPromptSource = func(context.Context, *mailboxConsumer, mailboxPairing) []mailboxPromptRequest {
	return nil
}

// mailboxOffer is one thing this consumer has decided and the orchestrator is
// owed: a message it read for it, the answer to a prompt, the non-destructive
// default a prompt's expiry hands over, a stop when the prompt had no default,
// or the word that its question arrived at a full queue. **The text is here and
// is never printed** — it travels one way, to the orchestrator.
//
// Outcome is why the offer exists — one of `performed`, `answered`, `expired`,
// `revoked` or `queue-full` — and it is what a pass document records: an id and
// an outcome are a message's identity and a fact about the pass, and neither is
// its text.
type mailboxOffer struct {
	// Kind is the kind of thing the offer is about: the message kind it was
	// read as, or `prompt` for everything a prompt hands over.
	Kind    string
	ID      string
	Mailbox string
	Text    string
	// Agent is the agent the offer was addressed to, when the message named
	// one; it is what the orchestrator routes on (D163), and it is covered by
	// the MAC like every other field.
	Agent string
	// Stop is true when what is handed over is not an answer: a prompt that
	// expired carrying no non-destructive answer tells the orchestrator to
	// stop rather than to carry on with an answer nobody gave (D168).
	Stop    bool
	Outcome string
}

// The outcomes an offer carries, spelled the way the document spells them.
const (
	mailboxOfferAnswered  = "answered"
	mailboxOfferExpired   = "expired"
	mailboxOfferRevoked   = "revoked"
	mailboxOfferQueueFull = "queue-full"
)

// mailboxKindAction, mailboxKindAnswer, mailboxKindPrompt,
// mailboxKindRegistration and mailboxKindStop are the kinds this file reads and
// writes beside the module's own KindMessage. The module declares no constant
// for any of them — its KindPrompt is the *ledger's* kind vocabulary, which
// spells one of these the same by coincidence — so they are bound here and held
// to `mailbox.Kinds` by TestMailboxInboxKindsAndVerbsAreTheSchemas: a kind the
// schema renames is a failing test rather than a branch nothing reaches.
const (
	mailboxKindAction = "action"
	mailboxKindAnswer = "answer"
	mailboxKindPrompt = "prompt"
	// mailboxKindRegistration is §3.5 (b)'s registration-and-preferences
	// message: the app's token, its platform and §3.8's switches, which this
	// consumer applies to `config/admin/push/$uid` and never to a device node
	// (P5.2.4).
	mailboxKindRegistration = "registration"
	// mailboxKindStop is the phone's stop request (the schema's `stop` kind,
	// mobile plan task P5.1g, the owner's answer Q10(c)): the app asking a
	// human to stop what they are looking at. It travels phone to host like
	// every other kind bound here, and it is answered rather than acted on —
	// the switch's own arm says why, and with what.
	mailboxKindStop = "stop"
)

// mailboxCodeHeadMoved is the code a merge refused because the pull request's
// head is no longer the head the confirmation named is recorded with (D169,
// §3.5). It is this file's own: the module's vocabulary has no word for a
// read-back that has gone stale, because checking it needs a second read of the
// pull request and the module reads nothing.
const mailboxCodeHeadMoved = mailbox.Code("head-moved")

// errMailboxHeadMoved is how the merge's own read-back tells the runner that the
// pull request moved: the code above is recorded for it rather than the generic
// action-failed, because the operator's answer to the two is different — one is
// "confirm it again against a head that has not moved", the other is "look at
// the error".
var errMailboxHeadMoved = errors.New("the pull request's head is not the head the confirmation named")

// mailboxDestructiveFreeText is M6's table as a value, for the free text that
// arrives as a plain `message`: the module's own destructive and refused verb
// sets (D128 as amended allows cancel, merge and confirm **only** with a typed
// confirmation, and refuses every other destructive verb outright), plus the
// word §9's table spells for approving a pull request, which no action record's
// verb set needs because `merge` covers it there.
//
// It is a recogniser and not a grammar, and its cost is stated rather than
// hidden: a message that merely *mentions* one of these words is refused, and
// the operator's answer is to say it again without the word. The default is
// refusal (M6), voice D47's and D128's whole point is that a misheard word must
// not merge a pull request, and a false refusal costs a second sentence where a
// false admission costs the pull request.
var mailboxDestructiveFreeText = append(
	append(append([]string{}, mailbox.ConfirmDestructiveVerbs...), mailbox.ConfirmRefusedVerbs...),
	"approve",
)

// mailboxAgents is the agent list this consumer advertises in the heartbeat
// (D161, P6.7): the agents it runs, which the phone reads out to answer "list
// agents" and to default a bare "talk to <agent>" to (D163). It is a variable
// for the same reason the other seams are — what runs beside this consumer is
// the orchestrator's to choose, and nothing in this tree supplies a list yet —
// and an empty list writes no `agents` field at all, which is the shape a
// heartbeat had before this task.
var mailboxAgents = func(context.Context, *mailboxConsumer, mailboxPairing) []mailbox.Agent {
	return nil
}

// mailboxHandOff is where the orchestrator attaches: the one call in this file
// that carries an answer out of the consumer and into the process that asked the
// question. It is a variable because that process's own channel (a pipe, a file,
// a socket — the orchestrator's to choose, and not this task's) does not exist
// in this tree; until it does, the answer is delivered here, once, and the fact
// that it was is recorded in the pass.
//
// An error from it is **not** fatal and does not un-spend the prompt: the offer
// has already been committed to the memory, so the answer was owed once and has
// been handed over once, which is the property D168 is about. A hand-off that
// fails is reported in the pass and is the operator's to fix.
var mailboxHandOff = func(context.Context, mailboxOffer) error { return nil }

// mailboxCancelRun is this CLI's own cancel path, reached from the consumer for
// a cancel record that validated and was not yet spent (D169: "cancel through
// the CLI's own code path"). It is a variable because the path it calls opens
// the operator's cloud — a backend, a bucket and the run's own record — which no
// test here has; the fake it is replaced by in tests is what pins the run id the
// pass hands over and that it is handed over exactly once.
var mailboxCancelRun = cancelFromMailbox

// cancelFromMailbox runs `fugaro cancel RUN` in-process, against the operator's
// own cloud and with cancel's own defaults, and reports what it did in the
// pass's own line rather than a second document. It opens the environment the
// cancel needs on the first confirmed cancel and keeps it for the life of the
// consumer: a cancel is rare, and re-opening a backend and a bucket per pass
// would spend the operator's calls on nothing.
func cancelFromMailbox(ctx context.Context, c *mailboxConsumer, runID string) error {
	env, err := c.cloud(ctx)
	if err != nil {
		return err
	}
	o := &cancelOptions{
		grace:        mailboxCancelGrace,
		finalizeWait: mailboxCancelFinalize,
		poll:         mailboxCancelPoll,
	}
	return cancelRun(ctx, env, o, runID, io.Discard, io.Discard, nil)
}

// mailboxGH is the `gh` the consumer runs a merge with, and it is a variable for
// the same reason every other spawn in this tree is: a test puts a fake in front
// of it on PATH and reads the argv back. D169 names the command exactly —
// `gh pr merge <n> --repo <owner/name>`, by the consumer, under the operator's
// own `gh` login, with the merge method and the confirmed head SHA on it — and
// the two subcommands this file runs are that one and the read of the head it is
// checked against.
var mailboxGH = "gh"

// mailboxKeychain is the OS keychain the pairings' MAC keys are read from (M5).
// It is a variable because every test in this repository holds keychain.Fake
// and no test touches a real keychain.
var mailboxKeychain = keychain.System

// mailboxOutboxSigner builds the signer one pairing's outbox replies are signed
// with (T2a). It is a variable because §8.2.4's per-write refusal — the key is
// loaded and the signer errors on this value — is unreachable in a shipped
// build, so the test that pins it injects a signer that fails.
var mailboxOutboxSigner = func(key keychain.Key) mailbox.ReplySigner {
	return mailbox.NewOutboxSigner(key)
}

// mailboxHeartbeatSigner builds the signer the heartbeat is written with —
// the lease itself, so a signer that fails here costs the lease (M1, M4b). It
// is a variable for the same reason as the outbox's: the per-write refusal is a
// fault path no shipped build reaches by itself.
var mailboxHeartbeatSigner = func(key keychain.Key) mailbox.LeaseSigner {
	signer := mailbox.NewHeartbeatSigner(key)
	return signer.Heartbeat
}

// mailboxNow is this process's own clock. The pass reads it for exactly two
// things: the instant a take is decided at when the database has not answered
// yet, and the next time the loop wakes. Every instant that reaches the store
// or the ledger is the database's own (D117), read from the client.
var mailboxNow = time.Now

// mailboxBoot draws the holder identity this consumer writes into the lease:
// the `boot` a heartbeat names, which is what makes two consumers of one
// mailbox tellable apart (§6). One per process — a restart is a new holder —
// and a variable so a test can pin it.
var mailboxBoot = newMailboxBootID

func newMailboxBootID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("boot-%d", mailboxNow().UnixNano())
	}
	return "boot-" + hex.EncodeToString(b[:])
}

type mailboxOptions struct {
	cloudOptions
	once        bool
	interval    time.Duration
	asJSON      bool
	noHeartbeat bool
}

func newMailboxCmd() *cobra.Command {
	o := &mailboxOptions{}
	cmd := &cobra.Command{
		Use:   "mailbox",
		Short: "Consume this installation's mailbox: read the phone's messages and answer them",
		Long: "Consume the mailbox of each phone this project is paired with: take the\n" +
			"lease on its heartbeat, read its inbox, verify each message's pairing MAC,\n" +
			"decide which deliveries are new, and sign and write the answers to its\n" +
			"outbox.\n" +
			"\n" +
			"The consumer acts on the operator's behalf, and a voice-originated message\n" +
			"may never cause a destructive command (D128). The default is refusal: a\n" +
			"message this build does not recognise is refused and reported by code, and a\n" +
			"destructive command is run only from a typed action record this consumer\n" +
			"re-validates itself — its verb in the closed set, its target resolved, a\n" +
			"confirmation present and inside the six-hour window measured from the\n" +
			"confirmation's own time, and its ids not already spent, which outlives a\n" +
			"restart. A cancel runs this CLI's own cancel path; a merge re-reads the pull\n" +
			"request's head and refuses one that moved since the confirmation read it\n" +
			"back. A launch and a read-only report are the allowance D128's amendment\n" +
			"names, and destructive free text is refused rather than acted on.\n" +
			"\n" +
			"Both directions of the wire are MAC-covered. Every inbox message is verified\n" +
			"before it is acted on, and one that does not verify is refused and never\n" +
			"acted on — and because nothing can retire it, it is read and refused again on\n" +
			"every pass until an operator removes the node with their own credential.\n" +
			"Every reply and every heartbeat is signed before it is written, so a value\n" +
			"the signer refuses is not written at all.\n" +
			"\n" +
			"A registration is the app's push token, its platform and its per-category\n" +
			"switches, and it is written to the sibling node config/admin/push/<uid> —\n" +
			"never under config/admin/devices/<uid>, which only `fugaro pair` and\n" +
			"`fugaro pair revoke` write and whose existence is what admits the device. A\n" +
			"registration whose account has no device node is refused and writes nothing,\n" +
			"so a revocation is final; a re-registration overwrites the token, the\n" +
			"platform and the time it was written and leaves the rest of the node alone.\n" +
			"The write needs roles/firebasedatabase.admin, the role a pairing needs, since\n" +
			"every node under config/admin is the operator's own credential's and no\n" +
			"client token's: where the credential does not hold it, or the database cannot\n" +
			"be reached, the pass stops and says so rather than pretending the phone\n" +
			"registered — and the delivery is not consumed, so the first pass after the\n" +
			"role is granted or the outage ends writes the node.\n" +
			"\n" +
			"The heartbeat IS the lease, and the loop renews it on every pass: it is\n" +
			"rewritten with the current instant under the same compare-and-swap a take\n" +
			"uses, which is what tells the phone this consumer is present and what keeps\n" +
			"the lease fresh. A renewal that finds the mailbox held by someone else writes\n" +
			"nothing, and the next pass takes the lease rather than acting on one it has\n" +
			"lost.\n" +
			"\n" +
			"The command stops at startup when there is no pairing key at the keychain\n" +
			"items its project config names, rather than looping over a mailbox it could\n" +
			"do nothing with. The pairing key is read from the OS keychain by item name\n" +
			"and is never printed; the item's name is not a secret and appears in a\n" +
			"diagnostic.\n" +
			"\n" +
			"The command writes the outbox, the heartbeat and the one push node a\n" +
			"registration names, with the operator's own credential, which is what makes\n" +
			"that write set a rule in this command's code rather than a fence the database\n" +
			"enforces.\n" +
			"\n" +
			"Its own state is the ledger at\n" +
			"$XDG_STATE_HOME/fugaro/mailbox/<project>/ledger.json, written 0o600 by\n" +
			"tmp+rename: it holds the deliveries this consumer has consumed, and it is\n" +
			"what stops a restart from acting on one twice. The ledger is committed before\n" +
			"an action is performed, never after. Beside it, pending.json holds the action\n" +
			"and confirmation ids this machine has spent, the questions it is holding and\n" +
			"the non-destructive answer each is owed when its time is up: an expiry is\n" +
			"decided on this consumer's own clock, and the answer is handed over once.\n" +
			"\n" +
			"Each pass prints one line, or with --json one JSON document, naming the\n" +
			"pairing, the lease, what was read and what was refused (by code, never by\n" +
			"text). A message's text, a MAC, a key and a keychain item's value are never\n" +
			"printed.\n" +
			"\n" +
			"Without --once it loops until interrupted, one pass every --interval; with\n" +
			"--once it runs one pass per configured pairing and exits, which is the\n" +
			"scriptable mode. A pass that held no lease, or read nothing, is a completed\n" +
			"pass and exits 0.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runMailbox(cmd, o) },
	}
	f := cmd.Flags()
	f.BoolVar(&o.once, "once", false, "run one pass per configured pairing and exit")
	f.DurationVar(&o.interval, "interval", mailboxDefaultInterval, "how long to wait between passes; never less than one second")
	f.BoolVar(&o.asJSON, "json", false, "one JSON document per pass instead of lines")
	f.BoolVar(&o.noHeartbeat, "no-heartbeat", false, "write no heartbeat, so no lease is taken and nothing is acted on")
	addCloudFlags(cmd, &o.cloudOptions)
	cmd.AddCommand(newMailboxSweepCmd())
	return cmd
}

// mailboxPairing is one configured pairing as this command holds it: the
// mailbox it serves — the device uid `fugaro pair` wrote, which is the node key
// under mailbox/ — the item its key was read from, and the gate and the two
// signers built on that key. **The key is not a field**: it lives inside the
// gate and the signers, which redact themselves (M5), so no printing of this
// struct can reach it.
type mailboxPairing struct {
	uid  string
	item string
	// revoked is a configured pairing whose keychain item holds no key: the
	// state a `fugaro pair revoke` leaves. It is served by no pass — its gate
	// and its signers are nil — and the pass it gets is the one that drops the
	// prompts it was holding. **Its key being gone is also what refuses every
	// message signed with it**: verification is per pairing, so a record signed
	// with the revoked key is not a message this consumer can attribute, and the
	// gate of the pairing whose inbox it sits in answers `bad-mac`.
	revoked   bool
	gate      *mailbox.Gate
	signer    mailbox.ReplySigner
	heartbeat mailbox.LeaseSigner
}

// mailboxKeyring reads one key per configured pairing, once, at startup (M5),
// and builds the gate each mailbox's messages are verified with and the signers
// its replies and its heartbeat are signed with.
//
// A pairing whose item holds no key is **not served and is reported**: a config
// entry survives the `pair revoke` that deletes the item — revoke edits no
// config — so refusing to start over one would leave the consumer unable to
// serve any other phone on the machine. It comes back from here as a pairing of
// its own with `revoked` set, so that the pass it is given is the one that drops
// the prompts nobody can answer any more, and it is a pass **in its sorted
// place** beside the served ones rather than a footnote on another pass. When no
// configured pairing has a key there is nothing to serve at all, and that is
// §8.3's availability row: exit 1, no pass run, nothing written. A keychain that
// cannot be used at all is the keychain package's backend failure, and it is
// exit 2 (§4.4).
func mailboxKeyring(ctx context.Context, lc *localcfg.Config, now func() time.Time) ([]mailboxPairing, error) {
	m := lc.Mailbox
	if m == nil || len(m.KeychainItem) == 0 {
		return nil, userErr("project %s has no mailbox configured: its project config has no mailbox.keychain_item block, so there is no pairing whose key this command could read; run `fugaro pair` and record the item name it prints as mailbox.keychain_item[<device uid>]", lc.Name)
	}
	store := mailboxKeychain()
	uids := make([]string, 0, len(m.KeychainItem))
	for uid := range m.KeychainItem {
		uids = append(uids, uid)
	}
	// Sorted, so several pairings are served in one order on every run and a
	// document's place in the output is a fact about the config rather than
	// about Go's map order.
	sort.Strings(uids)

	pairings := make([]mailboxPairing, 0, len(uids))
	served := 0
	for _, uid := range uids {
		item, err := keychain.ParseItem(m.KeychainItem[uid], lc.Name)
		if err != nil {
			return nil, userErr("mailbox.keychain_item.%s: %v; the item's name is what the key is read from, so a pairing with no readable name is a config the operator fixes", uid, err)
		}
		key, err := store.Key(ctx, item)
		switch {
		case err == nil:
		case errors.Is(err, keychain.ErrNoKey):
			pairings = append(pairings, mailboxPairing{uid: uid, item: item.Name(), revoked: true})
			continue
		default:
			return nil, remote(fmt.Errorf("reading this machine's keychain item %s for pairing %s: %w", item.Name(), uid, err))
		}
		// The key is captured once and only inside these three values: the gate
		// holds it and asks for its bytes per verification, and each signer does
		// the same per tag.
		pairings = append(pairings, mailboxPairing{
			uid:       uid,
			item:      item.Name(),
			gate:      mailbox.NewGate(key, mailbox.GateOptions{Now: now}),
			signer:    mailboxOutboxSigner(key),
			heartbeat: mailboxHeartbeatSigner(key),
		})
		served++
	}
	if served == 0 {
		return nil, userErr("no pairing key is readable at the keychain items project %s names (%s), so there is no message this command could verify and no reply it could sign: run `fugaro pair` for the phone that is missing and record the item name it prints; the key itself is never printed and is never in the project config", lc.Name, strings.Join(mailboxRevokedNames(pairings), ", "))
	}
	return pairings, nil
}

// mailboxRevokedNames is the item names of the pairings whose key is gone: the
// names and never a value, which is M5's rule for everything this command
// prints.
func mailboxRevokedNames(pairings []mailboxPairing) []string {
	names := []string{}
	for _, pairing := range pairings {
		if pairing.revoked {
			names = append(names, pairing.item)
		}
	}
	return names
}

func runMailbox(cmd *cobra.Command, o *mailboxOptions) error {
	// The flag refusal comes before anything is opened (§4.4): a bad interval
	// is the operator's typo and it costs no round trip to say so.
	if o.interval < mailboxMinInterval {
		return userErr("--interval %s is below the one second floor: this command holds a mailbox's lease and reads its inbox once per pass, and a loop tighter than that is a spin against the database", o.interval)
	}
	ctx := cmd.Context()
	lc, db, err := openBudget(ctx, o.cloudOptions)
	if err != nil {
		return err
	}
	// This machine's mailbox state has one writer at a time (§6), and it is taken
	// before the ledger is opened: this pass keeps what it reads there in memory
	// for the life of the process, so a `fugaro mailbox sweep` that rewrote the
	// same two files under it would drop a delivery this consumer has already
	// accepted and replied to. The lock is **shared**, so a second consumer is
	// let through: two of them are the store's lease to refuse (M1, and check
	// 83's step 7 records that refusal at the store), not a file here.
	stateLock, err := mailbox.LockState(mailbox.StateLockOptions{Project: lc.Name, Holder: "fugaro mailbox", Shared: true})
	if err != nil {
		return userErr("%v", err)
	}
	defer func() { _ = stateLock.Release() }()
	pairings, err := mailboxKeyring(ctx, lc, mailboxClock(db))
	if err != nil {
		return err
	}
	ledger, err := mailbox.OpenLedger(mailbox.LedgerOptions{Project: lc.Name, PromptTtlMs: mailboxPromptTtlMs})
	if err != nil {
		return userErr("this machine's mailbox ledger could not be read (%v); it is the consumer's own dedupe memory at $XDG_STATE_HOME/fugaro/mailbox/%s/ledger.json, and acting on a delivery it could not record is the one thing this command will not do", err, lc.Name)
	}
	memory, err := mailbox.OpenConfirmMemory(mailbox.ConfirmMemoryOptions{
		Project:     lc.Name,
		RetentionMs: mailboxPromptRetentionMs(),
		MaxPrompts:  mailbox.ConfirmMaxPrompts,
	})
	if err != nil {
		return userErr("this machine's prompt memory could not be read (%v); it is the consumer's own record of the prompts it is holding and the ids it has spent, at $XDG_STATE_HOME/fugaro/mailbox/%s/pending.json, and acting on a message without it is the one thing this command will not do", err, lc.Name)
	}
	c := &mailboxConsumer{
		lc:          lc,
		db:          db,
		ledger:      ledger,
		memory:      memory,
		pairings:    pairings,
		held:        map[string]mailbox.LeaseRun{},
		holder:      mailboxBoot(),
		noHeartbeat: o.noHeartbeat,
		out:         cmd.OutOrStdout(),
		asJSON:      o.asJSON,
	}
	defer func() {
		if c.env != nil {
			c.env.Close()
		}
	}()
	for {
		verdict, err := c.cycle(ctx)
		if err != nil {
			return err
		}
		if o.once {
			return mailboxExit(verdict)
		}
		// The wait is interrupted by an interrupt, which cancels the context: a
		// loop that held a signal for the length of an interval would be one
		// that ignores Ctrl-C until the next pass.
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(o.interval):
		}
	}
}

// mailboxExit is M12's one predicate, applied to what a `--once` cycle did.
// Exit 1 iff **all four** hold: at least one message was read, nothing was
// performed (no delivery, no reply, no answer handed over, no action run), every
// message read was refused, and at least one of those refusals was an action
// refusal — D128's fence, or an action that did not run — rather than a
// per-message one.
//
// Clause 1 is what keeps a pass that held no lease and a pass over an empty
// inbox at 0, and clause 4 what keeps a duplicates-only pass at 0. The loop
// mode does not exit on this predicate: §4.4's second exit-1 row is a `--once`
// pass's, and a long-lived consumer that stopped because a phone asked for
// something it may not do would be a consumer that stopped its mailbox.
func mailboxExit(v mailboxVerdict) error {
	if v.read > 0 && v.performed() == 0 && v.refused == v.read && v.actionRefused > 0 {
		return userErr("nothing happened: the %d message(s) this pass read were all refused, and %d of those refusals was an action that did not run", v.read, v.actionRefused)
	}
	return nil
}

// mailboxVerdict is a cycle's counters, as M12's predicate reads them.
type mailboxVerdict struct {
	read      int
	delivered int
	replied   int
	// offers counts what this pass handed the orchestrator — a message, a
	// prompt's answer, the answer its expiry stands for, the word that its
	// question found a full queue — and actions what it ran itself. They are
	// the two halves of "the pass performed something" that the document's own
	// counters do not carry: §4.3's `pass` block keeps P4.1's four fields, and a
	// pass that expired a prompt and refused an action is not a pass that did
	// nothing.
	offers        int
	actions       int
	duplicates    int
	refused       int
	actionRefused int
}

// performed is clause 2: what the pass did, in the words §7's loop uses — a
// message delivered, a reply written, an answer handed over, an action run. A
// sweep is T7's and is not a pass's.
func (v mailboxVerdict) performed() int { return v.offers + v.replied + v.actions }

func (v *mailboxVerdict) add(other mailboxVerdict) {
	v.read += other.read
	v.delivered += other.delivered
	v.replied += other.replied
	v.offers += other.offers
	v.actions += other.actions
	v.duplicates += other.duplicates
	v.refused += other.refused
	v.actionRefused += other.actionRefused
}

// mailboxConsumer is the running consumer: one pass over each configured
// pairing, over one database, with one ledger and one memory.
type mailboxConsumer struct {
	lc     *localcfg.Config
	db     *rtdb.Client
	ledger *mailbox.Ledger
	// memory is the consumer's own state beside the ledger: the ids it has
	// spent — a message's, an action's, a confirmation's — and the prompts it is
	// holding, in one file that survives a restart (§6, P6.7).
	memory   *mailbox.ConfirmMemory
	pairings []mailboxPairing
	// held is the one lease this consumer holds per pairing, keyed by the
	// mailbox it was taken on: the loop takes it on its first pass and renews
	// it on every pass after that (leaseFor), which is what the module's own
	// take rule requires and what keeps the phone's presence reading fresh.
	held        map[string]mailbox.LeaseRun
	holder      string
	noHeartbeat bool
	out         io.Writer
	asJSON      bool
	// env is the operator's cloud, opened on the first confirmed action that
	// needs it and kept for the life of the consumer (cancelFromMailbox).
	env *cloudEnv
}

// cloud is the environment this consumer's own commands run against: the
// operator's own project, backend and bucket, opened once. The flag surface the
// operator started this command with is the one they are opened with — a
// consumer run against another project would cancel in that project.
func (c *mailboxConsumer) cloud(ctx context.Context) (*cloudEnv, error) {
	if c.env != nil {
		return c.env, nil
	}
	env, err := openCloudFor(ctx, c.lc)
	if err != nil {
		return nil, err
	}
	c.env = env
	return env, nil
}

// cycle is one pass over every configured pairing, reported per pairing: each
// has its own heartbeat, its own inbox and its own key, so each is a pass of
// its own and is printed as one. A pairing whose key is gone is a pass of its
// own too — the one that drops the prompts nobody can answer any more — and it
// takes its place in the same order, so a document's position in the output is
// still a fact about the config.
func (c *mailboxConsumer) cycle(ctx context.Context) (mailboxVerdict, error) {
	total := mailboxVerdict{}
	for _, pairing := range c.pairings {
		pass, err := c.pass(ctx, pairing)
		if err != nil {
			return total, err
		}
		total.add(pass.verdict)
		if err := c.report(pass); err != nil {
			return total, err
		}
	}
	return total, nil
}

// mailboxPass is one pass over one pairing: what it did, and the document it is
// reported as.
type mailboxPass struct {
	mailbox string
	at      string
	lease   mailboxLease
	verdict mailboxVerdict
	acted   []mailboxActed
	replied []mailboxReplied
	refused []mailboxRefused
	notes   []string
}

type mailboxLease struct {
	State     string `json:"state"`
	Holder    string `json:"holder,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

type mailboxActed struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Outcome string `json:"outcome"`
}

type mailboxReplied struct {
	ID string `json:"id"`
	// For is the message the reply answers. A prompt's announcement answers
	// nothing — the host asks the question — so the field is absent there rather
	// than empty, which is what an id that names nothing would look like.
	For string `json:"for,omitempty"`
}

type mailboxRefused struct {
	ID   string `json:"id"`
	Code string `json:"code"`
}

// mailboxDocument is §4.3's document: one per pass, always with `project`. The
// `mailbox` field is this command's own addition — a project may be paired with
// several phones, each with its own mailbox, and a document that does not name
// the pairing is one a reader with two pairings cannot place (§4.3's rule is
// that a field may be added, never retyped or reused).
type mailboxDocument struct {
	Project  string           `json:"project"`
	Mailbox  string           `json:"mailbox"`
	At       string           `json:"at"`
	Lease    mailboxLease     `json:"lease"`
	Pass     mailboxPassCount `json:"pass"`
	Acted    []mailboxActed   `json:"acted"`
	Replied  []mailboxReplied `json:"replied"`
	Refused  []mailboxRefused `json:"refused"`
	Warnings []string         `json:"warnings"`
}

type mailboxPassCount struct {
	Read       int `json:"read"`
	Delivered  int `json:"delivered"`
	Duplicates int `json:"duplicates"`
	Refused    int `json:"refused"`
}

// report prints one pass, as a line or as §4.3's document. Neither carries a
// message's text, a MAC, a key or a keychain item's value: an id and a code
// only.
func (c *mailboxConsumer) report(pass mailboxPass) error {
	if c.asJSON {
		doc := mailboxDocument{
			Project: c.lc.Name,
			Mailbox: pass.mailbox,
			At:      pass.at,
			Lease:   pass.lease,
			Pass: mailboxPassCount{
				Read:       pass.verdict.read,
				Delivered:  pass.verdict.delivered,
				Duplicates: pass.verdict.duplicates,
				Refused:    pass.verdict.refused,
			},
			Acted:    pass.acted,
			Replied:  pass.replied,
			Refused:  pass.refused,
			Warnings: pass.notes,
		}
		return json.NewEncoder(c.out).Encode(doc)
	}
	fmt.Fprintf(c.out, "mailbox %s: lease %s; read %d, delivered %d, duplicates %d, refused %d, replied %d\n",
		pass.mailbox, pass.lease.line(), pass.verdict.read, pass.verdict.delivered, pass.verdict.duplicates, pass.verdict.refused, len(pass.replied))
	for _, r := range pass.refused {
		fmt.Fprintf(c.out, "  refused %s: %s\n", r.ID, r.Code)
	}
	for _, note := range pass.notes {
		fmt.Fprintf(c.out, "  warning: %s\n", note)
	}
	return nil
}

// line is the lease as §4.1's one line names it: the state in the module's own
// word, and the holder beside it when there is one.
func (l mailboxLease) line() string {
	if l.Holder == "" {
		return l.State
	}
	return l.State + " (" + l.Holder + ")"
}

// pass is §7's loop for one pairing, in §7's order. Two of its steps run on
// this consumer's own clock rather than on anything the store holds: the expiry
// of the prompts this pairing is holding is decided here, before the lease is
// even asked for, so it does not wait on a claim this consumer may not get or on
// a sweeper somebody else runs (D168 as amended); and the queries this pass puts
// to the phone are produced once the lease is held, because announcing one
// writes to the outbox and nothing writes without the lease.
func (c *mailboxConsumer) pass(ctx context.Context, pairing mailboxPairing) (mailboxPass, error) {
	pass := mailboxPass{
		mailbox: pairing.uid,
		lease:   mailboxLease{State: string(mailbox.LeaseNone)},
		acted:   []mailboxActed{},
		replied: []mailboxReplied{},
		refused: []mailboxRefused{},
		notes:   []string{},
	}
	if pairing.revoked {
		return c.drop(ctx, pairing, pass)
	}
	now := c.now()
	pass.at = time.UnixMilli(now).UTC().Format(time.RFC3339)
	// The expiry is decided before the lease is asked for, and that order is the
	// point: a prompt's answer is owed at a clock's reading and not at a claim,
	// so a pass that is refused the lease — or that takes it and loses it —
	// still expires what its own state is holding. It is also why nothing below
	// has to remember to do it.
	if err := c.expire(ctx, pairing, &pass); err != nil {
		return pass, err
	}

	run, err := c.leaseFor(ctx, pairing, now)
	switch {
	case errors.Is(err, mailbox.ErrUnsignable):
		// M4b's per-write refusal, seen here because the heartbeat write IS the
		// lease: no signed heartbeat, no write, no lease — and a consumer with
		// no lease acts on nothing. Nothing is recorded for it, so the next pass
		// tries again and the exit is 0 (§4.4).
		pass.lease = mailboxLease{State: string(mailbox.LeaseLost)}
		pass.notes = append(pass.notes, fmt.Sprintf("the heartbeat for %s could not be signed, so no lease was taken and nothing was acted on", pairing.uid))
		return pass, nil
	case errors.Is(err, mailbox.ErrLeaseContention):
		// Someone kept writing the heartbeat under this take. This consumer
		// wrote nothing, so it is a lost lease and not a failure: the next pass
		// re-reads and decides again.
		pass.lease = mailboxLease{State: string(mailbox.LeaseLost)}
		pass.notes = append(pass.notes, fmt.Sprintf("the heartbeat for %s kept changing under this pass, so no lease was taken and nothing was acted on", pairing.uid))
		return pass, nil
	case err != nil:
		return pass, dbErr(c.lc, err, true)
	}
	pass.lease = leaseDocument(run)
	// §7: a lease this consumer does not hold means it writes nothing, reads
	// nothing and acts on nothing. --no-heartbeat answers none, and a mailbox
	// another consumer holds answers lost.
	if run.Verdict != mailbox.LeaseHeld && run.Verdict != mailbox.LeaseTakenOver {
		return pass, nil
	}

	items, err := c.readInbox(ctx, pairing.uid)
	if err != nil {
		return pass, err
	}
	for _, item := range items {
		if err := c.deliver(ctx, pairing, run.Lease, &pass, item); err != nil {
			return pass, err
		}
	}
	if err := c.produce(ctx, pairing, &pass); err != nil {
		return pass, err
	}
	return pass, nil
}

// drop is a revoked pairing's pass: no lease is asked for and nothing is read,
// because there is no key to verify a message with or to sign a reply with, and
// the prompts that pairing was holding are dropped with the answer each is owed
// handed over **once**. The lease state is `none`, which is the module's own
// word for a mailbox this consumer holds nothing on — the pass really did take
// no lease — and the reason it is a note rather than a state of its own is that
// §4.3's lease block carries the module's vocabulary, not this file's.
func (c *mailboxConsumer) drop(ctx context.Context, pairing mailboxPairing, pass mailboxPass) (mailboxPass, error) {
	pass.at = time.UnixMilli(c.now()).UTC().Format(time.RFC3339)
	pass.notes = append(pass.notes, fmt.Sprintf("pairing %s has no key at keychain item %s, so no message signed with it can be verified and no reply signed with it can be written; it is not served, and the prompts it was holding are dropped", pairing.uid, pairing.item))
	dropped, err := c.memory.DropPrompts(pairing.uid, c.now())
	if err != nil {
		return pass, userErr("this machine's prompt memory could not be written (%v); the revoked pairing's prompts are left as they are and the answer they are owed is still owed", err)
	}
	for _, one := range dropped {
		c.handOff(ctx, &pass, mailboxOffer{
			Kind: mailboxKindPrompt, ID: one.Prompt.ID, Mailbox: pairing.uid,
			Text: one.Reply, Stop: one.Stop, Outcome: mailboxOfferRevoked,
		})
	}
	return pass, nil
}

// expire hands the orchestrator what each of this pairing's prompts is owed once
// its TTL has passed: the prompt's non-destructive answer, or a stop when it
// named none. **The consumer expires them on its own clock** (D168 as amended),
// so nothing here depends on when the owner runs the sweeper or on whether the
// stored message is still there — the local copy is the record, and it is
// committed as spent before anything is handed over, which is what makes the
// answer owed exactly once across a restart.
func (c *mailboxConsumer) expire(ctx context.Context, pairing mailboxPairing, pass *mailboxPass) error {
	now := c.now()
	expired, err := c.memory.ExpirePrompts(pairing.uid, now, mailboxPromptTtlMs)
	if err != nil {
		return userErr("this machine's prompt memory could not be written (%v); nothing was expired and the memory is left as it was", err)
	}
	for _, one := range expired {
		c.handOff(ctx, pass, mailboxOffer{
			Kind: mailboxKindPrompt, ID: one.Prompt.ID, Mailbox: pairing.uid,
			Text: one.Reply, Stop: one.Stop, Outcome: mailboxOfferExpired,
		})
	}
	return nil
}

// produce asks the orchestrator what it wants put to this phone and announces
// each question, once. The prompt is taken into this machine's memory **before**
// it is announced, so a prompt the phone never received still expires with the
// answer it named rather than losing it, and the cap is P6.5's: a prompt past it
// is refused and never evicts one the owner may still answer.
func (c *mailboxConsumer) produce(ctx context.Context, pairing mailboxPairing, pass *mailboxPass) error {
	requests := mailboxPromptSource(ctx, c, pairing)
	now := c.now()
	for _, request := range requests {
		taken, refusal, err := c.memory.NotePrompt(mailbox.ConfirmPrompt{
			ID:          request.ID,
			At:          now,
			Mailbox:     pairing.uid,
			Destructive: request.Destructive,
			Safe:        request.Safe,
		}, now)
		if err != nil {
			return userErr("this machine's prompt memory could not be written (%v); the prompt was not announced and is owed again on the next pass", err)
		}
		if refusal != nil {
			c.handOff(ctx, pass, mailboxOffer{
				Kind: mailboxKindPrompt, ID: request.ID, Mailbox: pairing.uid,
				Text: mailbox.ConfirmQueueFullReply, Outcome: mailboxOfferQueueFull,
			})
			continue
		}
		if !taken {
			// The id is already in the queue: the prompt was taken on an earlier
			// pass and announced then, and a producer that asks again is asking
			// the same question rather than a second one.
			continue
		}
		if err := c.announce(ctx, pairing, request, pass); err != nil {
			return err
		}
	}
	return nil
}

// announce writes one prompt to the outbox, signed (M4b): the phone reads its
// questions there, and a prompt whose text could not be signed is not written at
// all — the prompt is already in this machine's memory, so it expires with the
// answer it named, which is the fail-closed direction for a question that never
// arrived.
func (c *mailboxConsumer) announce(ctx context.Context, pairing mailboxPairing, request mailboxPromptRequest, pass *mailboxPass) error {
	run, err := mailbox.WriteReply(ctx, c.db, pairing.signer, mailbox.Reply{
		ID:     request.ID,
		Sender: pairing.uid,
		Text:   request.Text,
		At:     c.now(),
		Kind:   mailboxKindPrompt,
		Agent:  request.Agent,
	})
	if err != nil {
		return dbErr(c.lc, err, true)
	}
	switch run.Verdict {
	case mailbox.ReplyWritten, mailbox.ReplyPresent:
		pass.verdict.replied++
		pass.replied = append(pass.replied, mailboxReplied{ID: request.ID})
	case mailbox.ReplyRefused:
		pass.notes = append(pass.notes, fmt.Sprintf("the prompt %s could not be signed, so it was not announced; it is held here and will expire with the answer it named", request.ID))
	}
	return nil
}

// handOff is the one place an offer reaches the orchestrator, and the one place
// a pass records that it did. **The text is passed and never recorded**: what
// the document gets is the id, the kind and the outcome (§4.3's rule, which
// holds for an answer this consumer produced as much as for one it read).
func (c *mailboxConsumer) handOff(ctx context.Context, pass *mailboxPass, offer mailboxOffer) {
	if err := mailboxHandOff(ctx, offer); err != nil {
		pass.notes = append(pass.notes, fmt.Sprintf("the %s offer for %s could not be handed to the orchestrator (%v); it was owed once and has been produced once, so it is not repeated", offer.Outcome, offer.ID, err))
	}
	pass.verdict.offers++
	pass.acted = append(pass.acted, mailboxActed{ID: offer.ID, Kind: offer.Kind, Outcome: offer.Outcome})
}

// leaseFor is the lease this pass runs under. **A consumer takes a mailbox's
// lease once and renews it on every pass after that.**
//
// The take is once because the module's own rule refuses a take of a fresh
// claim to everyone, its holder included: `takeLease` answers `already-claimed`
// "whatever its holder string", so a pass that took again each iteration would
// be refused its own claim and would act on nothing until it went stale ten
// minutes later — a consumer that stopped serving its mailbox after its first
// pass.
//
// The renewal is every pass because the heartbeat IS the lease and it is also
// what the phone reads for presence (M1(c), §4.2): a consumer that wrote it
// once and left it alone would look offline within a minute or two of presence
// intervals, its claim would stop being fresh `ClaimTTL` after it was written,
// and the pass that followed would read a message and then stop acting on it —
// the holder's own re-read honours a fresh claim only. `mailbox.Renew` writes
// the fresh `{at, boot, mac}` under the same ETag fence a take uses, and a
// renewal that comes back not held is the truth about a mailbox this consumer
// no longer holds: the claim is dropped here, so the next pass takes it afresh
// rather than reporting a lease it has lost.
//
// Past `ActionBound(claim, ttl)` the holder re-reads the lease before it acts
// as well (deliver's own check, T3's caller obligation for a pass that outlives
// its renewal).
func (c *mailboxConsumer) leaseFor(ctx context.Context, pairing mailboxPairing, now int64) (mailbox.LeaseRun, error) {
	if _, holds := c.held[pairing.uid]; holds {
		renewed, err := mailbox.Renew(ctx, c.db, c.leaseOptions(ctx, pairing, now))
		if err != nil {
			return renewed, err
		}
		if renewed.Verdict == mailbox.LeaseHeld {
			c.held[pairing.uid] = renewed
		} else {
			delete(c.held, pairing.uid)
		}
		return renewed, nil
	}
	run, err := mailbox.Take(ctx, c.db, c.leaseOptions(ctx, pairing, now))
	if err == nil && run.Lease != nil {
		switch run.Verdict {
		case mailbox.LeaseHeld, mailbox.LeaseTakenOver:
			c.held[pairing.uid] = run
		}
	}
	return run, err
}

// release drops the claim this consumer holds for a mailbox, so the next pass
// takes it again rather than acting on a lease the store no longer names it in.
func (c *mailboxConsumer) release(uid string) { delete(c.held, uid) }

// leaseOptions is the take or re-read one pass makes: this consumer's boot id,
// one authoritative clock's reading (D117), the heartbeat signer built on the
// pairing's key, and the agent list it advertises to the phone (D161). A signer
// that fails costs the lease, which is M4b's whole point.
func (c *mailboxConsumer) leaseOptions(ctx context.Context, pairing mailboxPairing, now int64) mailbox.LeaseOptions {
	return mailbox.LeaseOptions{
		Mailbox:     pairing.uid,
		Holder:      c.holder,
		At:          now,
		Sign:        pairing.heartbeat,
		Agents:      mailboxAgents(ctx, c, pairing),
		NoHeartbeat: c.noHeartbeat,
	}
}

// now is the one instant a pass decides against: the database's own clock where
// it has answered (the Date header of the last response), and this machine's
// only until then. §6's lease and the ledger both refuse a `now` that runs
// backwards, so the reading is taken once per pairing pass and never per
// message.
func (c *mailboxConsumer) now() int64 { return mailboxClock(c.db)().UnixMilli() }

// mailboxClock is that reading as the gate can ask for it. The gate's five-minute
// future tolerance and the ledger's are the same check about the same kind of
// field, judged by one clock: the store's (D117), never this machine's. A gate
// left on `time.Now` refuses, on a machine whose own clock is slow, a message
// the store's clock reads as inside the tolerance — so a delivery that was in
// time is recorded as one dated in the future, and the phone's retry is refused
// with it.
func mailboxClock(db *rtdb.Client) func() time.Time {
	return func() time.Time {
		if t, ok := db.ServerNow(); ok {
			return t
		}
		return mailboxNow().UTC()
	}
}

// mailboxInbox is one child of the inbox node as the reader got it: the record,
// or the refusal that stands in for it.
type mailboxInbox struct {
	id      string
	record  mailbox.Record
	refusal *mailbox.Refusal
	at      int64
}

// readInbox reads `mailbox/<uid>/inbox` as this pass' own item type. What the
// read IS — the node, the strict decoder §8.1 requires (never encoding/json into
// a struct, which would fold a `Text` field into `text` and drop the unknown
// fields the tag covers), the two codes a child that cannot be read is refused
// with, and the oldest-first order §7 needs (the ledger's act step acts on the
// **oldest** accepted record, so a pass that handed it a newer one first would
// commit a record whose action belongs to another message) — is the module's,
// and it is one function for the two consumers of a mailbox rather than one per
// process: the resident orchestrator's consumer (P5.1f) reads this same node
// through it. What is added here is the reporting type, and nothing else.
func (c *mailboxConsumer) readInbox(ctx context.Context, uid string) ([]mailboxInbox, error) {
	read, err := mailbox.ReadInbox(ctx, c.db, uid)
	if err != nil {
		return nil, dbErr(c.lc, err, false)
	}
	items := make([]mailboxInbox, 0, len(read))
	for _, item := range read {
		items = append(items, mailboxInbox{id: item.ID, record: item.Record, refusal: item.Refusal, at: item.At})
	}
	return items, nil
}

// deliver is one message's turn through §7's loop: verify (§8.1), let the
// ledger decide (T4), then act — and the act is where §9's fence refuses.
func (c *mailboxConsumer) deliver(ctx context.Context, pairing mailboxPairing, lease *mailbox.Lease, pass *mailboxPass, item mailboxInbox) error {
	pass.verdict.read++
	if item.refusal != nil {
		pass.refuse(item.id, item.refusal.Code, false)
		return nil
	}
	// M4a: the MAC is verified **before** the delivery reaches the ledger, so a
	// message that fails verification takes no `(sender, id)` slot and cannot
	// deny service to the genuine message that shares its id. The stated cost
	// (§7's ordering 2) is that nothing can retire it either, so it is read and
	// refused again on every pass.
	message, refusal := pairing.gate.Verify(item.record)
	if refusal != nil {
		pass.refuse(item.id, refusal.Code, false)
		return nil
	}
	now := c.now()
	// M1's caller obligation: past the instant another claimant may already
	// hold the lease by the module's own arithmetic, the holder re-reads it
	// before it acts. A re-read that is not held ends this pass's acting.
	if lease != nil && now >= mailbox.ActionBound(*lease, mailbox.ClaimTTL) {
		again, err := mailbox.Confirm(ctx, c.db, c.leaseOptions(ctx, pairing, now))
		if err != nil {
			return dbErr(c.lc, err, true)
		}
		if again.Verdict != mailbox.LeaseHeld {
			c.release(pairing.uid)
			pass.lease = leaseDocument(again)
			pass.notes = append(pass.notes, "the lease was re-read before acting and is no longer this consumer's, so this pass stopped acting")
			return nil
		}
	}
	var runErr error
	result, err := c.ledger.Perform(message, mailbox.KindOf(message), now, func(entry mailbox.LedgerEntry) {
		// The ledger has committed the record; what runs here is the file's own
		// action runner, and it is where D128 is enforced (M11: before anything
		// is spawned). An error out of it is this machine's — a memory or a
		// store that would not take a write — and stops the pass.
		runErr = c.run(ctx, pairing, message, entry, pass)
	})
	if err != nil {
		// The ledger committed nothing, so nothing was performed — which is the
		// fence rather than a message's fault. It is this machine's own state,
		// so it is the operator's to fix, and the pass stops rather than acting
		// on a delivery it could not record.
		return userErr("this machine's mailbox ledger could not be written (%v); nothing was performed", err)
	}
	if runErr != nil {
		return runErr
	}
	if result.Refusal != nil {
		pass.refuse(message.ID, result.Refusal.Code, false)
		return nil
	}
	switch result.Outcome {
	case mailbox.OutcomeDuplicate:
		pass.verdict.duplicates++
		pass.refuse(message.ID, mailboxCodeDuplicate, false)
	case mailbox.OutcomeStalePending:
		// Consumed once and performed for nothing: the reply is still owed (M2),
		// and it says that nothing happened.
		pass.refuse(result.Entry.ID, mailbox.Code(mailbox.OutcomeStalePending), false)
		return c.reply(ctx, pairing, message, pass, mailboxStaleReply)
	case mailbox.OutcomeActed:
		// The runner above saw it: it either performed the delivery or refused
		// it, and a refusal is recorded by the runner itself.
	default:
		// The ledger answered an outcome no delivery of an accepted record can
		// have (nothing-to-act): it read a message and did nothing with it, and
		// the module's own word for that is the code it is reported under.
		pass.refuse(message.ID, mailbox.Code(result.Outcome), false)
	}
	return nil
}

// run is the action runner: what one record the ledger just took means, and the
// whole of D128's enforcement. Every case answers with either a performed
// delivery — an offer handed to the orchestrator, an action this process ran —
// or a refusal recorded against the record's own id, and never with both. The
// kind that does not travel phone to host — `prompt` — is refused here rather
// than delivered, because an orchestrator handed one out of its own inbox would
// take it for a question it must answer. One kind is neither delivered nor
// refused: a `stop` request is answered in the outbox with nothing performed,
// which is the whole of what Q10's answer (c) has this consumer do with one.
//
// The order of the checks inside a case is the rule: what the record says, then
// whether this machine has already spent it, then the run. Nothing is run
// before the record's own validity, its confirmation and its ids have all been
// answered for.
func (c *mailboxConsumer) run(ctx context.Context, pairing mailboxPairing, message *mailbox.Message, entry mailbox.LedgerEntry, pass *mailboxPass) error {
	switch message.Kind {
	case mailbox.KindMessage:
		return c.runMessage(ctx, pairing, message, entry, pass)
	case mailboxKindAnswer:
		return c.runAnswer(ctx, pairing, message, entry, pass)
	case mailboxKindAction:
		return c.runAction(ctx, pairing, message, entry, pass)
	case mailboxKindRegistration:
		return c.runRegistration(ctx, pairing, message, entry, pass)
	case mailboxKindStop:
		// The phone's stop request (P5.1g, the owner's answer Q10(c)): nothing
		// here acts on it — the enforcement side is `config/kill/**`, which no
		// client writes and this consumer has no grant for — and it is
		// **answered** rather than refused, because a record refused outright
		// is answered by nothing, and an unanswered record is one the phone's
		// own at-least-once rule keeps owing (M2), so the human's stop request
		// would sit in the app as a pending utterance with nothing to say about
		// it. That is exactly what the resident orchestrator does with one it
		// reads out of the same inbox (internal/orchestrator's consumerCarries
		// and consumerStopRequest); the sentence is this command's own.
		return c.reply(ctx, pairing, message, pass, mailboxStopReply)
	default:
		pass.refuse(entry.ID, mailboxCodeUnexpectedKind, false)
		return nil
	}
}

// runMessage is the conversation channel: free text reaches the orchestrator
// verbatim, and **destructive free text does not** (D128: no destructive command
// is recognised from a voice-originated message). What is refused here is
// refused as an action, because that is what it asked for — a pass whose only
// reading was one of these is M12's exit 1.
func (c *mailboxConsumer) runMessage(ctx context.Context, pairing mailboxPairing, message *mailbox.Message, entry mailbox.LedgerEntry, pass *mailboxPass) error {
	if mailboxReadsAsDestructive(message.Text) {
		pass.refuse(entry.ID, mailboxCodeActionRefused, true)
		return nil
	}
	pass.verdict.delivered++
	c.handOff(ctx, pass, mailboxOffer{
		Kind: mailbox.KindMessage, ID: entry.ID, Mailbox: pairing.uid,
		Text: message.Text, Agent: message.Agent, Outcome: mailboxOutcomePerformed,
	})
	return nil
}

// runAnswer settles the prompt the answer names, and it is where D128's widening
// after review round 2 is enforced: **an answer to a destructive prompt is
// accepted only as a MAC-verified action record with a confirmation**
// (D169, §3.5), never as free text. An answer to a prompt this machine is not
// holding, or is not holding any more, is the same answer to the same question
// — there is nothing left to settle — and is reported as such.
//
// An answer is free text by another name, and what is handed over is the
// answer's own words, so the fence the message kind gets is the fence this kind
// gets: without it, "yes, merge acme/app#41" reaches the orchestrator as the
// answer to any harmless question, which is D128's fence walked around through
// the one path `runMessage` does not cover.
func (c *mailboxConsumer) runAnswer(ctx context.Context, pairing mailboxPairing, message *mailbox.Message, entry mailbox.LedgerEntry, pass *mailboxPass) error {
	now := c.now()
	prompt, waiting, held := c.memory.Prompt(pairing.uid, message.ReplyTo)
	if !held || !waiting {
		pass.refuse(entry.ID, mailboxCodeUnknownPrompt, false)
		return nil
	}
	if prompt.Destructive {
		pass.refuse(entry.ID, mailbox.CodeConfirmNotAConfirmation, true)
		return nil
	}
	if mailboxReadsAsDestructive(message.Text) {
		// The prompt stays waiting: what was refused is the sentence, not the
		// question, so an answer that is an answer still settles it.
		pass.refuse(entry.ID, mailboxCodeActionRefused, true)
		return nil
	}
	expiry, settled, err := c.memory.AnswerPrompt(pairing.uid, message.ReplyTo, message.Text, now)
	if err != nil {
		return userErr("this machine's prompt memory could not be written (%v); the answer was not handed to the orchestrator and the prompt is still waiting", err)
	}
	if !settled {
		// The prompt was settled between the read above and this call, which
		// only a second answer can have done: the answer that did it stands, and
		// this one settles nothing.
		pass.refuse(entry.ID, mailboxCodeUnknownPrompt, false)
		return nil
	}
	c.handOff(ctx, pass, mailboxOffer{
		Kind: mailboxKindPrompt, ID: prompt.ID, Mailbox: pairing.uid,
		Text: expiry.Reply, Outcome: mailboxOfferAnswered,
	})
	return nil
}

// runAction is D169's typed action record: re-validated against
// `shared/confirm`'s rules by the module's own port of them, spent against this
// machine's memory, and only then run — cancel through this CLI's own cancel
// path, merge as `gh pr merge` under the operator's own `gh` login.
//
// **The spending is what makes an action at-most-once, and it happens before
// anything runs.** The confirmation's id is spent first: it is the id that
// authorises *whatever* record carries it, so a crash between the two spends
// leaves a confirmation that cannot authorise a second action — the fail-closed
// side of the two. The cost is stated: an action that fails after its id was
// spent cannot be retried from the same record, and the owner gives a new
// confirmation.
func (c *mailboxConsumer) runAction(ctx context.Context, pairing mailboxPairing, message *mailbox.Message, entry mailbox.LedgerEntry, pass *mailboxPass) error {
	now := c.now()
	record, refusal := mailbox.ConfirmValidateAction(message.Action, mailbox.ConfirmActionOptions{
		Now:               now,
		WindowMs:          mailbox.ConfirmWindowMs,
		FutureToleranceMs: mailbox.FutureTolerance.Milliseconds(),
	})
	if refusal != nil {
		pass.refuse(entry.ID, refusal.Code, true)
		return nil
	}
	if c.memory.Spent(mailbox.ConfirmLedgerConfirmation, record.Confirmation.ID) {
		pass.refuse(entry.ID, mailbox.CodeConfirmReplayedConfirmation, true)
		return nil
	}
	if c.memory.Spent(mailbox.ConfirmLedgerAction, record.ID) {
		pass.refuse(entry.ID, mailbox.CodeConfirmReplayedAction, true)
		return nil
	}
	switch record.Verb {
	case mailbox.ConfirmVerbCancel, mailbox.ConfirmVerbMerge:
	default:
		// A record that validated and that this process has nothing to run for.
		// The verb it happens for is `confirm`, whose action is the
		// orchestrator's own: there is no argv here to run it with.
		pass.refuse(entry.ID, mailboxCodeNotExecutable, true)
		return nil
	}
	for _, spend := range []struct{ kind, id string }{
		{mailbox.ConfirmLedgerConfirmation, record.Confirmation.ID},
		{mailbox.ConfirmLedgerAction, record.ID},
	} {
		if _, err := c.memory.Spend(spend.kind, spend.id, now); err != nil {
			return userErr("this machine's prompt memory could not be written (%v); nothing was run and the %s %s is not spent, so the action is still pending on the record's own terms", err, spend.kind, spend.id)
		}
	}
	outcome := mailboxOutcomeCancelled
	var runErr error
	switch record.Verb {
	case mailbox.ConfirmVerbCancel:
		runErr = mailboxCancelRun(ctx, c, record.Target.RunID)
	case mailbox.ConfirmVerbMerge:
		outcome = mailboxOutcomeMerged
		runErr = c.merge(ctx, record)
	}
	if errors.Is(runErr, errMailboxHeadMoved) {
		pass.refuse(entry.ID, mailboxCodeHeadMoved, true)
		return nil
	}
	if runErr != nil {
		pass.refuse(entry.ID, mailboxCodeActionFailed, true)
		pass.notes = append(pass.notes, fmt.Sprintf("the %s the record of %s asked for did not run: %v; its confirmation is spent, so it takes a new confirmation rather than a retry of this record", record.Verb, mailboxTargetName(record), runErr))
		return nil
	}
	pass.verdict.actions++
	pass.acted = append(pass.acted, mailboxActed{ID: entry.ID, Kind: mailboxKindAction, Outcome: outcome})
	return nil
}

// runRegistration applies §3.5 (b)'s registration-and-preferences message
// (P5.2.4): the token the phone's push service minted, the platform it was
// minted on, the instant this consumer wrote them, and §3.8's switches, into
// the node the design puts them in — `config/admin/push/$uid`, the **sibling**
// of the device node and never a child of it (§3.4).
//
// **The write is a merge and that is what makes a re-registration one.** The
// three fields the message carries are overwritten, `prefs` is replaced with
// the state the message carries when it carries one, and everything else in the
// node — the sender's `ledger` above all (§3.6, P5.2.6) — is left exactly as it
// is, because a node this consumer cannot re-derive is a node it must not
// clear. The phone's message is the whole of the state §3.8 asks for, so a
// registration that stops carrying `quiet_hours` turns quiet hours off rather
// than leaving the last window behind: `prefs` is written as one child, and a
// child written whole cannot keep a field the app has dropped.
//
// **A registration the database refuses halts this pass, and the sentence names
// the role.** The write needs the same `roles/firebasedatabase.admin` a pairing
// needs, because `config/admin/**` is client-denied and every write under it is
// an IAM write (P5.2.2); where the operator's credential does not hold it, the
// refusal is a run-level one — the class `dbErr` sends a refused write to, exit
// 1 — but it is raised with this command's own sentence rather than with the
// shared admin-denied one, because that one is about budget caps and kill
// switches and would send the operator to the wrong place. Registration is an
// admin act like pairing (Q3, §3.5 (b)), and this is what it says so.
//
// **And a write the store would not take leaves the delivery owed rather than
// lost** (`registrationOwed`), which is the one act in this file the ledger's
// at-most-once fence is taken back for: the registration is convergent — the
// same token, platform and switches written again — so the account that could
// not be registered because the role was missing or the database was
// unreachable is registered by the first pass that finds the store willing,
// instead of needing the phone to notice and speak again. It is a record taken
// back and not a fence walked around: the delivery is offered by the ledger
// exactly as a new one is, window, cap and quota included.
//
// **One check runs before the write, and it is the design's own rule read one
// step earlier.** The device node's existence IS the allowlist (§3.4, D166), so
// a registration whose account has no device node — the everyday case being a
// message the phone sent before `fugaro pair revoke` and this pass processes
// after it — writes **nothing**:
// `config/admin/devices/**` is written by `fugaro pair` and `fugaro pair
// revoke` alone, and P5.2.4's acceptance is that a registration arriving after
// the device node was deleted leaves the store as it found it, so revocation
// stays final. §3.4 permits that write and has the sender reap the orphan
// afterwards; writing nothing is the stricter half of the same rule, and the
// net above it stays where it is for the paths that do not come through here.
// That answer is a **decision** and not a failure, so it is refused in the pass
// and the delivery is consumed: a revoked account is not owed a registration,
// and a message that says otherwise is refused once.
//
// **The token is written because it was validated, not because it arrived.**
// Every field below has been through the schema's own validator in the module
// (`mailbox.Decode`, §8.1) before this runs: `token` and `platform` are
// required by the kind, non-empty, bounded (`mailbox.TokenMax`) and free of the
// characters no database key or screen may carry, and `prefs` is either absent
// or the whole of §3.8's switch state. Nothing here invents a bound of its own,
// and nothing here writes a value the message did not carry.
func (c *mailboxConsumer) runRegistration(ctx context.Context, pairing mailboxPairing, message *mailbox.Message, entry mailbox.LedgerEntry, pass *mailboxPass) error {
	paired, err := c.pairedDevice(ctx, pairing.uid)
	if err != nil {
		return c.registrationOwed(entry, err)
	}
	if !paired {
		pass.refuse(entry.ID, mailboxCodeRegistrationUnpaired, false)
		pass.notes = append(pass.notes, fmt.Sprintf("the registration %s for %s was not applied: the account has no device node at %s, so it is not paired to a phone and its push node stays as it is — a registration that arrives after `fugaro pair revoke` deletes the device node registers nothing, which is what keeps a revocation final", entry.ID, pairing.uid, rules.PathDevices+"/"+pairing.uid))
		return nil
	}
	node := map[string]any{
		"token":      message.Token,
		"platform":   message.Platform,
		"updated_at": c.now(),
	}
	if message.Prefs != nil {
		node["prefs"] = mailboxPrefsNode(message.Prefs)
	}
	if err := c.db.Patch(ctx, pairPushPath(pairing.uid), node); err != nil {
		if errors.Is(err, rtdb.ErrPermission) {
			return c.registrationOwed(entry, userErr("the registration %s was not applied: the write to %s was refused, and applying a registration writes the push node there with the roles/firebasedatabase.admin a pairing writes the device node with, which this credential does not hold on project %s's Firebase project (its owners and editors hold it, and terraform.budget_admins): %v", entry.ID, pairPushPath(pairing.uid), c.lc.Name, err))
		}
		return c.registrationOwed(entry, dbErr(c.lc, err, true))
	}
	pass.acted = append(pass.acted, mailboxActed{ID: entry.ID, Kind: mailboxKindRegistration, Outcome: mailboxOutcomeRegistered})
	return nil
}

// registrationOwed answers a registration the store would not let this pass
// apply: the delivery's record is taken back out of the ledger (Retract), so the
// next pass reads the same inbox node and applies it then, and the cause is
// returned with the sentence that says so in front of it. The classification
// survives the re-sentencing — `%w` and `remote` keep the code the cause's own
// `dbErr` or `userErr` gave it, exit 1 for a refusal and exit 2 for an outage —
// because a registration the store refused is the same failure whether or not
// the delivery is offered again.
//
// **The retrying is bounded by the module's own window and not by a counter
// here.** A registration the consumer is still holding six hours later is
// outside the command window, so the next pass reads it as the replay it is,
// refuses it and moves on; the app registers again on its next open (§3.5 (b)),
// which is the design's own answer to a lost registration.
//
// A ledger that will not take the retraction is the worse of the two failures
// and is said rather than hidden: the record stands, so the delivery is consumed
// and the account keeps whatever token the push node holds until the phone
// registers again.
func (c *mailboxConsumer) registrationOwed(entry mailbox.LedgerEntry, cause error) error {
	if err := c.ledger.Retract(entry.Sender, entry.ID); err != nil {
		return userErr("the registration %s was not applied (%v), and this machine's mailbox ledger would not take the write that offers it again (%v), so the delivery is consumed and the account keeps the push node it has until the phone registers again", entry.ID, cause, err)
	}
	return remote(fmt.Errorf("the registration %s was not applied and is not consumed, so the next pass applies it again: %w", entry.ID, cause))
}

// pairedDevice answers whether the account this pairing serves still has a
// device node: D166's allowlist read at the instant of the write rather than at
// the instant of a send. A read that is refused is the same refusal a refused
// read of the inbox is (dbErr without a write: the Viewer role, exit 1), and an
// absent node is an answer rather than an error — it is the state `fugaro pair
// revoke` leaves behind.
func (c *mailboxConsumer) pairedDevice(ctx context.Context, uid string) (bool, error) {
	var node json.RawMessage
	found, err := c.db.Get(ctx, rules.PathDevices+"/"+uid, &node)
	if err != nil {
		return false, dbErr(c.lc, err, false)
	}
	return found, nil
}

// mailboxPrefsNode is §3.8's switch state as the push node holds it, under the
// names the message already carries: one child per category, and `quiet_hours`
// with the IANA zone name and the window's two ends. The node's child is the
// message's own record rather than a second vocabulary — a sender (P5.2.6)
// reading the node and a phone writing the message must not have two names for
// one switch, and a test holds these keys to the schema's (`PrefsFields`,
// `QuietHoursFields`) for the same reason the kind constants are held to
// `mailbox.Kinds`.
//
// All five switches are written, on or off, because the schema requires them:
// the message is the whole state, so a partial one would leave a reader
// choosing a value the app never sent. The window is written only when the
// message carries one, because §3.8's default for quiet hours is off and an
// absent window is exactly that — a switch, by contrast, is never absent, so
// nothing has to read off-or-on out of its absence.
func mailboxPrefsNode(prefs *mailbox.Prefs) map[string]any {
	node := map[string]any{
		"kill_switch":      prefs.KillSwitch,
		"cap_reached":      prefs.CapReached,
		"consumer_offline": prefs.ConsumerOffline,
		"cap_near":         prefs.CapNear,
		"prompt":           prefs.Prompt,
	}
	if prefs.QuietHours != nil {
		node["quiet_hours"] = map[string]any{
			"zone":  prefs.QuietHours.Zone,
			"start": prefs.QuietHours.Start,
			"end":   prefs.QuietHours.End,
		}
	}
	return node
}

// merge is D169's merge: re-read the pull request's head, refuse if it has moved
// since the confirmation read it back, and only then merge — as
// `gh pr merge <n> --repo <owner/name> --squash --match-head-commit <head>`, by
// this consumer, under the operator's own `gh` login, with the number and the
// repository as separate argv elements and never a shell (M11).
//
// The two flags are what make that command run at all, and what make it run
// against the head the operator confirmed. `gh pr merge` refuses without one of
// `--merge`, `--squash` and `--rebase` when it is not on a terminal — and this
// one is never on a terminal (M11: a buffer, no stdin) — so a merge without one
// is refused by `gh` after the record's confirmation has already been spent, and
// the operator's answer to that is a new confirmation for a merge that would
// have worked. `--squash` is this repository's own choice for a merge it runs
// itself (`scripts/land-prs.sh`), and `--match-head-commit` is D169's read-back
// made atomic: the check above and the merge are two calls, and the SHA in the
// flag is the one the *server* compares at the merge, so a push that lands
// between them is refused by GitHub rather than merged.
func (c *mailboxConsumer) merge(ctx context.Context, record *mailbox.ConfirmRecord) error {
	number := strconv.FormatInt(record.Target.PR, 10)
	head, err := c.gh(ctx, "pr", "view", number, "--repo", record.Target.Repo, "--json", "headRefOid")
	if err != nil {
		return err
	}
	var view struct {
		HeadOID string `json:"headRefOid"`
	}
	if err := json.Unmarshal([]byte(head), &view); err != nil {
		return fmt.Errorf("reading the head of %s#%s: %w", record.Target.Repo, number, err)
	}
	if !strings.EqualFold(strings.TrimSpace(view.HeadOID), record.Target.HeadSHA) {
		return errMailboxHeadMoved
	}
	_, err = c.gh(ctx, "pr", "merge", number, "--repo", record.Target.Repo,
		"--squash", "--match-head-commit", record.Target.HeadSHA)
	return err
}

// gh runs one `gh` subcommand for a merge and answers its stdout. The output is
// capped: a `gh` that answers a megabyte where a head SHA was asked for must not
// be a consumer that grows by it, and nothing of it is printed — what a merge
// reports reaches the pass as an outcome, not as text.
func (c *mailboxConsumer) gh(ctx context.Context, args ...string) (string, error) {
	bin, err := exec.LookPath(mailboxGH)
	if err != nil {
		return "", fmt.Errorf("%s is not on this machine's PATH: a confirmed merge runs it under the operator's own login", mailboxGH)
	}
	out := &mailboxBounded{}
	code, err := procgroup.Run(ctx, procgroup.Cmd{
		Name:   bin,
		Args:   args,
		Stdout: out,
		Stderr: io.Discard,
		Grace:  mailboxGHRace,
	})
	if err != nil {
		return "", fmt.Errorf("running %s: %w", mailboxGH, err)
	}
	if code != 0 {
		return "", fmt.Errorf("`%s %s` exited %d", mailboxGH, strings.Join(args, " "), code)
	}
	return out.String(), nil
}

// mailboxGHRace is how long a `gh` this consumer spawned is given to leave after
// its context ends before its process group is killed: gh starts credential
// helpers and pagers, and killing only the process that was started would leave
// them holding the pipes.
const mailboxGHRace = 5 * time.Second

// mailboxBounded is a write-only sink that keeps at most mailboxBoundedBytes of
// what is written to it. A `gh` answering with more than a head SHA is not an
// error here — the first bytes are the answer or they are not — and the cap is
// what keeps the reading bounded.
type mailboxBounded struct {
	buf bytes.Buffer
}

const mailboxBoundedBytes = 4 << 10

func (b *mailboxBounded) Write(p []byte) (int, error) {
	if room := mailboxBoundedBytes - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *mailboxBounded) String() string { return b.buf.String() }

// mailboxReadsAsDestructive is M6's table asked about free text: does this say
// one of the words a message may not say? The words are compared whole, with the
// three regular English endings folded off — a message that says "deletes",
// "merging" or "revoked" is asking for the same thing as one that says the verb
// itself.
func mailboxReadsAsDestructive(text string) bool {
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		for _, stem := range mailboxDestructiveStems(word) {
			for _, destructive := range mailboxDestructiveFreeText {
				if stem == destructive {
					return true
				}
			}
		}
	}
	return false
}

// mailboxDestructiveStems is one word as the forms of it this recogniser
// compares: the word itself, the word with a regular ending folded off, and the
// two respellings that fold needs — "merging" is "merge" and "cancelling" is
// "cancel". It is deliberately not a stemmer: the forms above are the ones a
// spoken sentence uses, and a stem of any other shape matches nothing in the
// list, so the folding reaches into no word nobody said.
func mailboxDestructiveStems(word string) []string {
	stems := []string{word}
	for _, suffix := range []string{"ing", "ed", "es", "s"} {
		if !strings.HasSuffix(word, suffix) || len(word) <= len(suffix)+2 {
			continue
		}
		stem := word[:len(word)-len(suffix)]
		stems = append(stems, stem)
		if n := len(stem); n >= 2 && stem[n-1] == stem[n-2] {
			stems = append(stems, stem[:n-1])
		}
		if strings.HasSuffix(stem, "e") {
			stems = append(stems, stem[:len(stem)-1])
		} else {
			stems = append(stems, stem+"e")
		}
	}
	return stems
}

// mailboxTargetName is the target a record names, as a note about it can print
// it: a run id, or a repository and a pull request. It is the record's own
// target and never the message's text.
func mailboxTargetName(record *mailbox.ConfirmRecord) string {
	if record.Target.Kind == mailbox.ConfirmTargetRun {
		return record.Target.RunID
	}
	return fmt.Sprintf("%s#%d", record.Target.Repo, record.Target.PR)
}

// reply signs and writes the answer a consumed delivery is owed, create-only
// (§8.2), in the words the caller passes: mailboxStaleReply for a delivery the
// ledger consumed and performed nothing for, and mailboxStopReply for the
// phone's stop request. Both are answers of the same shape — kind `answer`,
// naming the message they answer (§8.2's `reply_to`, which the schema requires
// of that kind) — because the outbox is where a phone reads what became of what
// it sent, whatever the sentence says. A write the signer refuses is **not**
// written and is a per-write refusal of a completed pass — exit 0, M4's third
// row — and nothing is recorded for it, so the reply is owed again on the next
// pass.
func (c *mailboxConsumer) reply(ctx context.Context, pairing mailboxPairing, message *mailbox.Message, pass *mailboxPass, text string) error {
	id := mailbox.ReplyID(message.ID)
	run, err := mailbox.WriteReply(ctx, c.db, pairing.signer, mailbox.Reply{
		ID:      id,
		Sender:  pairing.uid,
		Text:    text,
		At:      c.now(),
		Kind:    "answer",
		ReplyTo: message.ID,
	})
	if err != nil {
		return dbErr(c.lc, err, true)
	}
	switch run.Verdict {
	case mailbox.ReplyWritten, mailbox.ReplyPresent:
		pass.verdict.replied++
		pass.replied = append(pass.replied, mailboxReplied{ID: id, For: message.ID})
	case mailbox.ReplyRefused:
		pass.notes = append(pass.notes, fmt.Sprintf("the reply owed for %s could not be signed, so nothing was written to the outbox and it is owed again on the next pass", message.ID))
	}
	return nil
}

// refuse records one refusal against a message: its id, and the module's own
// code — never the message's text, and never a paraphrase.
func (p *mailboxPass) refuse(id string, code mailbox.Code, action bool) {
	p.verdict.refused++
	if action {
		p.verdict.actionRefused++
	}
	p.refused = append(p.refused, mailboxRefused{ID: id, Code: string(code)})
}

// leaseDocument is a lease run as §4.3 reports it: the state in the module's
// own word, whoever holds the mailbox, and the instant the claim stops being
// fresh.
func leaseDocument(run mailbox.LeaseRun) mailboxLease {
	doc := mailboxLease{State: string(run.Verdict), Holder: run.Holder}
	if run.ExpiresAt > 0 {
		doc.ExpiresAt = time.UnixMilli(run.ExpiresAt).UTC().Format(time.RFC3339)
	}
	return doc
}

// fugaro mailbox sweep (mobile P6.13) is the D118 mailbox sweeper the owner
// runs: the only command in this file that deletes anything from the store, and
// the whole of what it deletes is **stored prompts past D118's TTL**.
//
// **A prompt is stored once, in the outbox**: `produce` (P6.7) writes each
// question the orchestrator asks as a `prompt` message under
// `mailbox/<mailbox>/outbox/<id>`, and the phone reads it there. So "past the
// TTL" is a fact about that node's own `at` — the schema has no `expires_at`,
// by D168 as amended — and the node it names is the one this sweep removes,
// under the operator's own ADC, which is the credential that reaches a delete
// (C0 of the P4.1 plan: no grant admits a token-bearing writer to one). The
// deletion is one `PATCH` of nulls per mailbox, which is the shape
// `internal/budget/sweep.go` uses and the reason a sweep is one write per
// mailbox rather than one per prompt.
//
// **The sweeper never answers the conversation.** Expiring a prompt and handing
// the orchestrator its non-destructive answer — or a stop, when it named none —
// is the consumer's, decided on its own clock and recorded in its own memory
// (`expire` above), and it is the consumer's precisely because **a sweep can run
// while the consumer is down** (D168 as amended, P6.13's reason for the split).
// So this command reads the stored state, reports what it deleted, and writes
// nothing else: no outbox answer, no heartbeat, no ledger, no offer to an
// orchestrator it never opens. A prompt whose stored copy is gone is still
// expired by the consumer, from the local copy the memory holds, exactly once —
// `TestMailboxSweepConsumerDownStillExpiresFromTheLocalCopy` is that property.
//
// **What is not in its delete set, and why each one is a report rather than an
// edit.** A stored *answer* or a stored reply, and every node under the inbox —
// P6.13's remit is "stored prompts past D118's TTL and nothing more", and the
// mailbox's wider retention is the owner's decision (D118) rather than a rule
// this command may widen on its own. A Firebase user — the `m~` rule is P6.6's
// and lives in `internal/budget/sweep.go` — and this sweep holds no user key at
// all: it lists the mailboxes from the project config and never asks for one. A
// node whose `at` a reader cannot use is kept rather than deleted, because a
// record that cannot be dated is not one this command can call expired.
//
// **Its other half is this machine's own state, and the presence verdict it
// reports** (T7, §6, §4.2). `sweep` deletes the store's expired prompts under
// the operator's credential; `local` lets go of the consumer's own expired
// state — the ledger records past its horizon and the memory's settled prompts
// and spent ids — through the same two rules the consumer applies to itself, and
// a delete set naming anything else is **refused and reported** rather than
// performed (M8). Neither half answers a conversation. And the presence verdict
// is computed from each mailbox's stored heartbeat and written nowhere: no node
// and no grant is designated for a verdict, and the sweeper holds no key to
// verify the MAC with, so it reports what the node says and moves on.
//
// **It needs no keychain and no lease.** The mailboxes it walks are the
// `mailbox.keychain_item` keys of the project config — the uids `fugaro pair`
// recorded — so a revocation that emptied an item leaves its mailbox sweepable,
// and a sweep of a mailbox no consumer is holding deletes exactly what an
// unheld mailbox should lose. The clock is the database's own (D117), like every
// other instant this file decides against.

// mailboxSweepOptions is the hidden sweeper's flag surface (§4.2 of the P4.1
// plan): `--json` for one document, `--dry-run` to print the same plan and write
// nothing.
type mailboxSweepOptions struct {
	cloudOptions
	asJSON bool
	dryRun bool
}

func newMailboxSweepCmd() *cobra.Command {
	o := &mailboxSweepOptions{}
	cmd := &cobra.Command{
		Use:   "sweep",
		Short: "Delete this project's stored mailbox prompts past their TTL (internal)",
		Long: "Delete the stored prompts of the mailboxes this project's config names, past\n" +
			"D118's 72-hour TTL — and nothing else.\n" +
			"\n" +
			"A prompt is stored once, in the outbox the phone reads it from, and its age is\n" +
			"its own `at`: the schema carries no expiry field, so the TTL is computed from\n" +
			"that instant and the database's own clock. A prompt at the TTL or older is\n" +
			"deleted; one younger than it, one whose `at` cannot be read, and every other\n" +
			"kind of message are left where they are. The deletion is one atomic write per\n" +
			"mailbox, under the operator's own credential, which is the one that reaches a\n" +
			"delete.\n" +
			"\n" +
			"The sweeper never answers a conversation. Expiring a prompt and handing its\n" +
			"non-destructive answer to the orchestrator is the consumer's, on its own clock\n" +
			"and from its own copy of what the prompt said — that is why a sweep may run\n" +
			"while the consumer is down: the consumer still expires the question, once,\n" +
			"when it comes back, whether or not the stored copy is still there. Nothing\n" +
			"this command does writes an answer, a heartbeat or a lease, and it needs\n" +
			"neither a pairing key nor a lease to run.\n" +
			"\n" +
			"The mailboxes it walks are the pairing uids in this project's\n" +
			"mailbox.keychain_item block, so a revoked pairing's mailbox is swept too. It\n" +
			"deletes no Firebase user — the `m~` rule belongs to the history sweeper — and\n" +
			"nothing outside the store nodes it is configured for and this machine's own\n" +
			"state: the ledger records and prompt entries a retention window already\n" +
			"allows, which it lets go of through the consumer's own two rules.\n" +
			"\n" +
			"This machine's state has **one writer at a time**, and this command is not a\n" +
			"second one: while the consumer that keeps those two files is running, the\n" +
			"sweep leaves them alone and says so, naming the process to stop, and the\n" +
			"consumer in turn refuses to start while a sweep is in its own half. The store\n" +
			"half is not fenced by that: a stored prompt past the TTL is deleted whether a\n" +
			"consumer is running or not.\n" +
			"\n" +
			"It also reports, for each mailbox, what the stored heartbeat says about the\n" +
			"presence of the consumer that wrote it — live, stale, offline, or a clock\n" +
			"that differs — and writes nothing for that verdict.\n" +
			"\n" +
			"It prints one line per mailbox, or with --json one document, naming each id it\n" +
			"deleted, how many prompts it kept, the presence it read and what its own half\n" +
			"let go of. With --dry-run it prints the same plan and writes nothing. A\n" +
			"message's text, a MAC and a key are never printed.",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE:   func(cmd *cobra.Command, _ []string) error { return runMailboxSweep(cmd, o) },
	}
	f := cmd.Flags()
	f.BoolVar(&o.asJSON, "json", false, "one JSON document instead of lines")
	f.BoolVar(&o.dryRun, "dry-run", false, "print what would be deleted and write nothing")
	addCloudFlags(cmd, &o.cloudOptions)
	return cmd
}

// mailboxSweepDocument is one sweep as `--json` reports it: the project, the
// instant it decided at, whether it wrote, one entry per mailbox it walked, and
// what it could not read. A deleted prompt is named by its **id**, which is its
// outbox key, and never by its text (§4.3's rule, which holds for this command's
// output as much as for a pass's).
type mailboxSweepDocument struct {
	Project string `json:"project"`
	At      string `json:"at"`
	DryRun  bool   `json:"dry_run"`
	// Local is the other half of a sweep (§6): this machine's own state, which
	// is not in the store at all and which no mailbox entry can report.
	Local     mailboxSweepLocal     `json:"local"`
	Mailboxes []mailboxSweepMailbox `json:"mailboxes"`
	Warnings  []string              `json:"warnings"`
}

// mailboxSweepLocal is the sweeper's local half — the delete set §6 gives it:
// the two files the consumer keeps under the operator's own state directory, and
// the records this sweep let go of in them. **Nothing under `mailbox/` is in it**
// and the paths are the machine's own; SweepRefusals reads the set before
// anything is deleted, and a key this command does not own (a Firebase user, a
// store node) is a refusal rather than a deletion (M8).
type mailboxSweepLocal struct {
	// Ledger and Prompts are the two files, absolute, under
	// $XDG_STATE_HOME/fugaro/mailbox/<project>/. They are reported so an
	// operator can see the sweep reached this machine's state — a path is not a
	// secret and it is not a message's text (§4.3).
	Ledger  string `json:"ledger"`
	Prompts string `json:"prompts"`
	// LedgerIDs are the `sender`/`id` pairs the ledger let go of — acked
	// deliveries past its horizon — and PromptIDs the settled prompts and spent
	// ids the memory let go of. A prompt still to be answered is in neither:
	// dropping it would lose the safe answer it is owed.
	LedgerIDs []string `json:"ledger_ids"`
	PromptIDs []string `json:"prompt_ids"`
	// Wrote says whether the two files were rewritten at all. A sweep with
	// nothing to let go of writes nothing, so a reader can tell "I swept this
	// machine" from "I rewrote it".
	Wrote bool `json:"wrote"`
	// Refused says why this machine's half was **not** performed, when another
	// writer holds the state — the consumer, or a second sweep — and is empty
	// otherwise. It is the one fact that tells "there was nothing to let go of"
	// from "this sweep was not allowed to look", which the empty id lists and
	// `wrote: false` alone cannot (the same distinction `deleted` and `kept` draw
	// for a mailbox).
	Refused string `json:"refused,omitempty"`
}

// mailboxSweepPresence is the presence verdict (§4.2, `shared/presence`): what
// the mailbox's stored heartbeat says about the consumer that wrote it, and the
// readings the rule decided it from. **It is reported and nothing is written
// for it** — no node and no grant is designated for a verdict — and the sweeper
// holds no pairing key, so it can honestly report only what the stored node
// says: the MAC is not verified here, and the lease the node stands for is the
// consumer's.
type mailboxSweepPresence struct {
	// State is one of `live`, `stale`, `offline` and `clock-differs`, or empty
	// when the stored heartbeat is not one the rule can read — `Code` then says
	// which refusal it was.
	State  string `json:"state"`
	Reason string `json:"reason"`
	// AgeMs is the heartbeat's age, and **null** when the mailbox has no
	// heartbeat at all: the module's own answer, and not the same fact as an age
	// of zero.
	AgeMs *int64 `json:"age_ms"`
	// BootID is the boot id the stored heartbeat names — `boot` on the wire, D117
	// — and empty when there is none.
	BootID string `json:"boot_id"`
	// Code is the refusal, when the node is there and is not a heartbeat.
	Code string `json:"code,omitempty"`
}

type mailboxSweepMailbox struct {
	Mailbox string `json:"mailbox"`
	// Presence is the verdict for this mailbox's stored heartbeat, reported
	// whether or not the sweep deleted anything.
	Presence mailboxSweepPresence `json:"presence"`
	// Deleted is the id of every stored prompt this sweep removed (or, with
	// `--dry-run`, would remove). It is empty rather than absent when a mailbox
	// lost nothing: an empty list and a missing field are two different facts to
	// a reader that has to tell "I swept this mailbox" from "I never got to it".
	Deleted []string `json:"deleted"`
	// Kept is how many stored prompts were left alone — younger than the TTL, or
	// carrying no `at` a reader can use.
	Kept int `json:"kept"`
}

// mailboxSweeper is one sweep: the project config it lists its mailboxes from,
// the database it deletes through, and the store's own clock.
type mailboxSweeper struct {
	lc     *localcfg.Config
	db     *rtdb.Client
	out    io.Writer
	asJSON bool
	dryRun bool
	now    func() time.Time
}

func runMailboxSweep(cmd *cobra.Command, o *mailboxSweepOptions) error {
	ctx := cmd.Context()
	lc, db, err := openBudget(ctx, o.cloudOptions)
	if err != nil {
		return err
	}
	uids, err := mailboxSweepMailboxes(lc)
	if err != nil {
		return err
	}
	s := &mailboxSweeper{lc: lc, db: db, out: cmd.OutOrStdout(), asJSON: o.asJSON, dryRun: o.dryRun, now: mailboxClock(db)}
	return s.sweep(ctx, uids)
}

// mailboxSweepMailboxes is the mailboxes one sweep walks, sorted, as a fact
// about the config rather than about Go's map order — the same order the
// consumer serves its pairings in.
//
// It reads the config and **not** the keychain: a sweep verifies nothing and
// signs nothing, so a pairing whose key is gone is a mailbox whose stored
// prompts are swept like any other. That is the direction a revocation leaves
// things, and refusing to start over it would leave exactly the prompts nobody
// can answer any more in the store for ever.
func mailboxSweepMailboxes(lc *localcfg.Config) ([]string, error) {
	m := lc.Mailbox
	if m == nil || len(m.KeychainItem) == 0 {
		return nil, userErr("project %s has no mailbox configured: its project config has no mailbox.keychain_item block, so there is no mailbox whose stored prompts this sweep could delete; the mailboxes it walks are the pairings `fugaro pair` recorded there", lc.Name)
	}
	uids := make([]string, 0, len(m.KeychainItem))
	for uid := range m.KeychainItem {
		uids = append(uids, uid)
	}
	sort.Strings(uids)
	return uids, nil
}

// sweep walks every configured mailbox once and reports what it did, in the
// order the config gave. Every mailbox is swept before anything is printed, so a
// failure part-way through is an error with nothing half-reported: the deletion
// each mailbox already took is a write that landed, and §4.2's discipline is
// that a sweep's output names what it did rather than what it meant to do.
func (s *mailboxSweeper) sweep(ctx context.Context, uids []string) error {
	now := s.now()
	doc := mailboxSweepDocument{
		Project:   s.lc.Name,
		At:        now.UTC().Format(time.RFC3339),
		DryRun:    s.dryRun,
		Mailboxes: []mailboxSweepMailbox{},
		Warnings:  []string{},
	}
	// This machine's own state first, and its fence before any store write: the
	// set this command deletes is the consumer's expired state, and a set that
	// named anything else (a Firebase user, a store node) is refused — with the
	// store untouched, so a refusal is never a half-performed sweep.
	local, err := s.local(now.UnixMilli())
	if err != nil {
		return err
	}
	if local.Refused == "" {
		if findings := mailbox.SweepRefusals(localKeys(local)); len(findings) > 0 {
			return userErr("this sweep's delete set names something it does not own, so nothing was deleted: %s", strings.Join(findings, "; "))
		}
	}
	doc.Local = local
	for _, uid := range uids {
		one, warnings, err := s.mailbox(ctx, uid, now)
		if err != nil {
			return err
		}
		doc.Mailboxes = append(doc.Mailboxes, one)
		doc.Warnings = append(doc.Warnings, warnings...)
	}
	if err := s.report(doc); err != nil {
		return err
	}
	// The refusal comes last and still exits 1: the store half is not fenced by
	// this machine's lock (§4.2 — it touches no file of the consumer's), so what
	// this sweep owed the operator is both the deletions it made and the reason
	// its own half did not happen.
	if local.Refused != "" {
		return userErr("this machine's own mailbox state was not swept: %s", local.Refused)
	}
	return nil
}

// localKeys is every key a local sweep names: the two files and the ids it let
// go of, which is the whole of what it is allowed to touch.
func localKeys(sweep mailboxSweepLocal) []string {
	keys := append([]string{}, sweep.Ledger, sweep.Prompts)
	keys = append(keys, sweep.LedgerIDs...)
	return append(keys, sweep.PromptIDs...)
}

// local is the sweeper's half for this machine: the same two files the consumer
// keeps and the same retention boundary it applies to itself (`mailbox.SweepLocal`
// calls the ledger's eviction and the memory's own prune rather than a second
// copy of either rule), so a sweep can never let go of something the consumer
// would have kept.
//
// It needs no pairing and no lease, and **it writes nothing when there is nothing
// to let go of**: a sweep that rewrote an untouched file would be a write the
// consumer's next pass reads as its own. With `--dry-run` it writes nothing at
// all and reports the same set, which is why the plan is read one pass earlier
// than the state it plans over.
//
// **It is not a second writer.** §6 names the consumer as the writer of both
// files and the consumer holds what it read in memory for the life of its
// process, so a sweep that committed a set read before a delivery the consumer
// accepted in between would drop that delivery from disk — and a restart would
// re-read the never-deleted inbox node and run the action again. The sweep
// therefore takes this machine's state lock (exclusive; the consumer holds it
// shared — `internal/mailbox`'s statelock.go has the rule) around the whole of
// its local half, and a lock somebody else holds is a refusal with the process
// to wait for rather than a sweep that writes anyway. The store half is not
// fenced by it: it touches no file of the consumer's, and P6.13's reason for it —
// the consumer expires the question from its own copy when it comes back — holds
// whether a consumer is running or not.
func (s *mailboxSweeper) local(nowMs int64) (mailboxSweepLocal, error) {
	if s.dryRun {
		// A plan writes nothing, so it takes no lock and creates no file. A plan
		// that showed a set the sweep would refuse to perform would be a plan
		// about a different thing, so the lock is read rather than taken —
		// StateLockHeld creates and writes nothing.
		holder, pid, held, err := mailbox.StateLockHeld(s.lc.Name)
		if err != nil {
			return mailboxSweepLocal{}, userErr("this machine's mailbox state could not be read for the writer that holds it (%v); nothing was written", err)
		}
		if held {
			return refusedLocal(s.lc.Name, &mailbox.StateBusyError{Project: s.lc.Name, Holder: holder, PID: pid})
		}
	} else {
		lock, err := mailbox.LockState(mailbox.StateLockOptions{Project: s.lc.Name, Holder: "fugaro mailbox sweep"})
		if err != nil {
			var busy *mailbox.StateBusyError
			if errors.As(err, &busy) {
				return refusedLocal(s.lc.Name, busy)
			}
			return mailboxSweepLocal{}, userErr("this machine's mailbox state could not be locked (%v); nothing was written", err)
		}
		defer func() { _ = lock.Release() }()
	}
	ledger, err := mailbox.OpenLedger(mailbox.LedgerOptions{Project: s.lc.Name, PromptTtlMs: mailboxPromptTtlMs})
	if err != nil {
		return mailboxSweepLocal{}, userErr("this machine's mailbox ledger could not be read (%v); it is the consumer's own dedupe memory at $XDG_STATE_HOME/fugaro/mailbox/%s/ledger.json, and a sweep that could not read it would delete out of a state it does not know", err, s.lc.Name)
	}
	memory, err := mailbox.OpenConfirmMemory(mailbox.ConfirmMemoryOptions{
		Project:     s.lc.Name,
		RetentionMs: mailboxPromptRetentionMs(),
		MaxPrompts:  mailbox.ConfirmMaxPrompts,
	})
	if err != nil {
		return mailboxSweepLocal{}, userErr("this machine's prompt memory could not be read (%v); it is the consumer's own record of the prompts it is holding and the ids it has spent, at $XDG_STATE_HOME/fugaro/mailbox/%s/pending.json, and a sweep that could not read it would let go of nothing rather than of what the window allows", err, s.lc.Name)
	}
	var sweep mailbox.LocalSweep
	if s.dryRun {
		sweep, err = mailbox.PlanLocal(ledger, memory, nowMs)
	} else {
		sweep, err = mailbox.SweepLocal(ledger, memory, nowMs)
	}
	if err != nil {
		return mailboxSweepLocal{}, userErr("this machine's mailbox state could not be swept (%v); nothing was written", err)
	}
	// The two id lists are empty rather than absent when nothing was dropped: an
	// empty list and a missing field are two different facts to a reader that has
	// to tell "this sweep had nothing to let go of" from "this sweep never
	// looked".
	ids := func(list []string) []string {
		if list == nil {
			return []string{}
		}
		return list
	}
	return mailboxSweepLocal{
		Ledger:    sweep.Ledger,
		Prompts:   sweep.Prompts,
		LedgerIDs: ids(sweep.LedgerIDs),
		PromptIDs: ids(sweep.PromptIDs),
		Wrote:     !s.dryRun && !sweep.Empty(),
	}, nil
}

// refusedLocal is this machine's half when another writer holds its state: the
// two files, named so a reader can tell "this sweep reached this machine's state
// and left it alone" from "this sweep never looked", no ids — it let go of
// nothing — and the refusal that says which process to wait for. What it carries
// is the fix: the state has one writer at a time, so stopping the process it
// names (or waiting for it) and rerunning is what makes a sweep possible again.
func refusedLocal(project string, busy error) (mailboxSweepLocal, error) {
	ledger, err := mailbox.LedgerPath(project)
	if err != nil {
		return mailboxSweepLocal{}, userErr("%v", err)
	}
	prompts, err := mailbox.ConfirmMemoryPath(project)
	if err != nil {
		return mailboxSweepLocal{}, userErr("%v", err)
	}
	return mailboxSweepLocal{
		Ledger:    ledger,
		Prompts:   prompts,
		LedgerIDs: []string{},
		PromptIDs: []string{},
		Refused:   busy.Error(),
	}, nil
}

// mailbox sweeps one mailbox: one read of its outbox, one decision per child,
// and one atomic write of the deletions — nothing at all when a `--dry-run`
// asked for the plan rather than the work.
//
// The decision is the consumer's own expiry boundary, deliberately: `expire`
// keeps a prompt while `now - at < ttl` and expires it once that difference
// reaches the TTL, and a sweeper that drew the line a millisecond away from the
// consumer's would delete a stored prompt the consumer still calls live (or keep
// one it has already answered). A prompt whose `at` is absent or unusable is
// kept: this command deletes what it can prove is past the TTL.
func (s *mailboxSweeper) mailbox(ctx context.Context, uid string, now time.Time) (mailboxSweepMailbox, []string, error) {
	out := mailboxSweepMailbox{Mailbox: uid, Deleted: []string{}}
	presence, err := s.presence(ctx, uid, now)
	if err != nil {
		return out, nil, err
	}
	out.Presence = presence
	var raw map[string]json.RawMessage
	found, err := s.db.Get(ctx, "mailbox/"+uid+"/outbox", &raw)
	if err != nil {
		return out, nil, dbErr(s.lc, err, false)
	}
	if !found {
		return out, nil, nil
	}
	nowMs := now.UnixMilli()
	updates := map[string]any{}
	warnings := []string{}
	for id, data := range raw {
		record, err := mailbox.Decode(data)
		if err != nil {
			// A node the strict decoder cannot read is not a stored prompt, so it
			// is none of this command's business to delete — but it is worth
			// saying, because it stays there and a reader who expected a sweep to
			// empty the mailbox is owed the reason it did not.
			warnings = append(warnings, fmt.Sprintf("the outbox node %s of mailbox %s could not be read as a message, so it was left alone", id, uid))
			continue
		}
		if kind, _ := record["kind"].(string); kind != mailboxKindPrompt {
			continue
		}
		if at := mailbox.RecordAt(record); at <= 0 || nowMs-at < mailboxPromptTtlMs {
			out.Kept++
			continue
		}
		path, err := mailbox.OutboxPath(uid, id)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("the outbox node %s of mailbox %s is not a path this command will build (%v), so it was left alone", id, uid, err))
			continue
		}
		out.Deleted = append(out.Deleted, id)
		updates[path] = nil
	}
	sort.Strings(out.Deleted)
	if s.dryRun || len(updates) == 0 {
		return out, warnings, nil
	}
	if err := s.db.Patch(ctx, "", updates); err != nil {
		return out, warnings, dbErr(s.lc, err, true)
	}
	return out, warnings, nil
}

// presence reads `mailbox/<uid>/heartbeat` — D117's node, `{at, boot, mac}` —
// and reports what `shared/presence` decides about it, or the refusal when the
// node is there and is not a heartbeat. An absent node is the module's own
// `offline`/`no-heartbeat`, which is a fact about the account rather than a
// refusal.
//
// **The MAC is not verified here and nothing is written for the verdict.** The
// sweeper holds no pairing key (§4.2: it needs none), so what it can honestly
// report is what the stored node says; the lease that node stands for is the
// consumer's, taken with its own key under an if-match, and a sweep that
// believed a forged heartbeat would be a sweep that could be made to report a
// consumer alive. It reports and moves on.
func (s *mailboxSweeper) presence(ctx context.Context, uid string, now time.Time) (mailboxSweepPresence, error) {
	out := mailboxSweepPresence{}
	var value any
	found, err := s.db.Get(ctx, "mailbox/"+uid+"/heartbeat", &value)
	if err != nil {
		return out, dbErr(s.lc, err, false)
	}
	var heartbeat *mailbox.PresenceHeartbeat
	if found {
		// The client decodes a node into `map[string]any` and this package's
		// record is that same map behind a name, so the reading is converted
		// rather than asserted: what is asserted is that the node is an object at
		// all, and anything else is the module's `invalid-heartbeat`.
		decoded, ok := value.(map[string]any)
		if !ok {
			out.Code = string(mailbox.CodePresenceInvalidHeartbeat)
			return out, nil
		}
		record := mailbox.Record(decoded)
		boot, _ := record["boot"].(string)
		heartbeat = &mailbox.PresenceHeartbeat{At: mailbox.RecordAt(record), BootID: boot}
	}
	answer := mailbox.EvaluatePresence(mailbox.PresenceOptions{Heartbeat: heartbeat, Now: now.UnixMilli()})
	if !answer.OK {
		out.Code = string(answer.Code)
		return out, nil
	}
	out.State, out.Reason, out.BootID = string(answer.State), answer.Reason, answer.BootID
	if answer.HasAge {
		age := answer.AgeMs
		out.AgeMs = &age
	}
	return out, nil
}

// report prints one sweep: the JSON document, or one line per mailbox with the
// ids it deleted under it, the machine's own half, and the warnings. Either way
// the text of a message never appears — an id is a key and a fact about the
// sweep, and what a prompt said is not.
func (s *mailboxSweeper) report(doc mailboxSweepDocument) error {
	if s.asJSON {
		return json.NewEncoder(s.out).Encode(doc)
	}
	verb := "delete"
	if doc.DryRun {
		verb = "would delete"
	}
	for _, one := range doc.Mailboxes {
		fmt.Fprintf(s.out, "mailbox %s: %d stored prompt(s) past the TTL, %d kept\n", one.Mailbox, len(one.Deleted), one.Kept)
		fmt.Fprintf(s.out, "  presence %s (%s)\n", presenceWord(one.Presence), presenceReason(one.Presence))
		for _, id := range one.Deleted {
			fmt.Fprintf(s.out, "  %s %s\n", verb, id)
		}
	}
	fmt.Fprintf(s.out, "local state: %d ledger record(s) and %d prompt memory entr(ies) past the retention window%s\n", len(doc.Local.LedgerIDs), len(doc.Local.PromptIDs), writtenWord(doc.Local, doc.DryRun))
	if doc.Local.Refused != "" {
		fmt.Fprintf(s.out, "  refused: %s\n", doc.Local.Refused)
	}
	for _, id := range doc.Local.LedgerIDs {
		fmt.Fprintf(s.out, "  %s %s\n", verb, id)
	}
	for _, id := range doc.Local.PromptIDs {
		fmt.Fprintf(s.out, "  %s %s\n", verb, id)
	}
	for _, warning := range doc.Warnings {
		fmt.Fprintf(s.out, "  warning: %s\n", warning)
	}
	return nil
}

// presenceWord is a verdict in one word for the line output: the state, or
// `unreadable` when the stored node is there and is not a heartbeat.
func presenceWord(p mailboxSweepPresence) string {
	if p.State == "" {
		return "unreadable"
	}
	return p.State
}

// presenceReason is why, in the rule's own words: the refusal's code when the
// node could not be read, and otherwise the module's reason.
func presenceReason(p mailboxSweepPresence) string {
	if p.Code != "" {
		return p.Code
	}
	return p.Reason
}

// writtenWord says what the local half did, which is the one thing a reader of a
// dry run must be able to tell from a sweep that wrote.
func writtenWord(l mailboxSweepLocal, dryRun bool) string {
	switch {
	case l.Refused != "":
		return " (refused: another writer holds this machine's state, so nothing was written)"
	case dryRun:
		return " (a dry run: nothing was written)"
	case l.Wrote:
		return " (rewritten)"
	default:
		return " (nothing to let go of, so nothing was written)"
	}
}
