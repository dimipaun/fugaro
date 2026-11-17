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
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/mailbox"
	"github.com/dimipaun/fugaro/internal/mailbox/keychain"
	"github.com/dimipaun/fugaro/internal/procgroup"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// fugaro mailbox (mobile P4.1, Task T5): the consumer side of an account's
// mailbox, as one long-lived local process the operator runs beside the
// orchestrator host. docs/plans/2026-10-18-mobile-mailbox-consumer.md is the
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
// what a pass is reported as. It writes the outbox and the heartbeat and
// nothing else, under the operator's own ADC (C0).
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
// recognises, which is refused; and a `prompt` or a `registration` is a kind
// that does not travel phone to host, refused with this file's own code. A
// destructive command without a valid confirmation record is refused, which is
// §9's standing position and the reason M12's fourth clause exists: a pass whose
// only reading was one of those is exit 1, because the operator asked for one
// thing and nothing happened.
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
// one to shared/prompts/index.js, so the two cannot drift.
const mailboxPromptTtlMs = 72 * 60 * 60 * 1_000

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

// mailboxCodeUnreadable is the code the inbox reader answers for a record its
// strict decoder cannot read at all — a duplicated key, a lone surrogate, two
// JSON values in one object. It is this reader's own code and not a protocol
// one: the schema's vocabulary is shared/mailbox-schema's, and a record the
// reader never got as far as is a fact about the store rather than about the
// message. A value that decodes but is not an object is refused with the
// module's own not-an-object, which is what that code means.
const mailboxCodeUnreadable = mailbox.Code("unreadable-record")

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
// of an inbox is recorded with. The schema names five kinds and the direction
// decides which of them travel: a `prompt` and a `registration` are written by
// the host, so one read out of the host's own inbox is a message nothing here
// can act on — refused and reported rather than delivered to an orchestrator
// that would take it for a question. **A registration is P5.2.4's** (the token
// node), and this file refuses it rather than applying it half-way.
const mailboxCodeUnexpectedKind = mailbox.Code("unexpected-kind")

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

// mailboxKindAction, mailboxKindAnswer and mailboxKindPrompt are the kinds this
// file reads and writes beside the module's own KindMessage. The module declares
// no constant for any of them — its KindPrompt is the *ledger's* kind
// vocabulary, which spells one of these the same by coincidence — so they are
// bound here and held to `mailbox.Kinds` by TestMailboxInboxKindsAreTheSchemas:
// a kind the schema renames is a failing test rather than a branch nothing
// reaches.
const (
	mailboxKindAction = "action"
	mailboxKindAnswer = "answer"
	mailboxKindPrompt = "prompt"
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
			"The command writes only the outbox and the heartbeat, with the operator's own\n" +
			"credential, which is what makes that write set a rule in this command's code\n" +
			"rather than a fence the database enforces.\n" +
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

// readInbox reads `mailbox/<uid>/inbox` and decodes each child with the strict
// decoder §8.1 requires — never encoding/json into a struct, which would fold a
// `Text` field into `text` and drop the unknown fields the tag covers.
//
// The order is oldest first, which is §7's: the ledger's act step acts on the
// **oldest** accepted record, so a pass that handed it a newer one first would
// commit a record whose action belongs to another message. A child whose `at`
// cannot be read is sorted first and refused by the checks that follow it.
func (c *mailboxConsumer) readInbox(ctx context.Context, uid string) ([]mailboxInbox, error) {
	var raw map[string]json.RawMessage
	found, err := c.db.Get(ctx, "mailbox/"+uid+"/inbox", &raw)
	if err != nil {
		return nil, dbErr(c.lc, err, false)
	}
	if !found {
		return nil, nil
	}
	items := make([]mailboxInbox, 0, len(raw))
	for id, data := range raw {
		item := mailboxInbox{id: id}
		record, err := mailbox.Decode(data)
		switch {
		case err == nil:
			item.record = record
			item.at = mailboxRecordAt(record)
		case errors.Is(err, mailbox.ErrNotAnObject):
			item.refusal = &mailbox.Refusal{Code: mailbox.CodeNotAnObject}
		default:
			item.refusal = &mailbox.Refusal{Code: mailboxCodeUnreadable}
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].at != items[j].at {
			return items[i].at < items[j].at
		}
		return items[i].id < items[j].id
	})
	return items, nil
}

// mailboxRecordAt is the record's own `at` for ordering, and zero when it has
// none a reader can use: a message without one is refused by the gate a moment
// later, and ordering it first is what keeps it from displacing an older
// delivery.
func mailboxRecordAt(record mailbox.Record) int64 {
	switch at := record["at"].(type) {
	case json.Number:
		if n, err := at.Int64(); err == nil && n > 0 {
			return n
		}
	case float64:
		if n := int64(at); n > 0 {
			return n
		}
	case int64:
		if at > 0 {
			return at
		}
	}
	return 0
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
		return c.reply(ctx, pairing, message, pass)
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
// kinds that do not travel phone to host are refused here rather than delivered,
// because an orchestrator handed a `prompt` or a `registration` out of its own
// inbox would take it for a question it must answer.
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
// (§8.2). A write the signer refuses is **not** written and is a per-write
// refusal of a completed pass — exit 0, M4's third row — and nothing is
// recorded for it, so the reply is owed again on the next pass.
func (c *mailboxConsumer) reply(ctx context.Context, pairing mailboxPairing, message *mailbox.Message, pass *mailboxPass) error {
	id := mailboxReplyID(message.ID)
	run, err := mailbox.WriteReply(ctx, c.db, pairing.signer, mailbox.Reply{
		ID:      id,
		Sender:  pairing.uid,
		Text:    mailboxStaleReply,
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

// mailboxReplyID is the outbox key one message's answer is written under:
// `r-` + the message's id, which is the merged simulator's own `replyId`
// (`shared/mailbox-sim`). It is derived rather than drawn so that a reply owed
// and not written is owed under the same id on the next pass, and the outbox's
// create-only rule makes the second write of that id a no-op rather than a
// second answer.
func mailboxReplyID(messageID string) string { return "r-" + messageID }

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
