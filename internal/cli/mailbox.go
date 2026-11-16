package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/mailbox"
	"github.com/dimipaun/fugaro/internal/mailbox/keychain"
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
// keychain's (T2, T2a), the lease on the heartbeat is mailbox.Take (T3), the
// dedupe ledger and its commit-before-acting fence are mailbox.Perform (T4) —
// so there is no second reader here and no second tag function. What this file
// decides is which node is read, in which order, and what a pass is reported
// as. It writes the outbox and the heartbeat and nothing else, under the
// operator's own ADC (C0).
//
// **The action runner is the one seam left open, and T5 refuses at it.**
// §9's allow-list and the argv shape of each action are T6's, and so is P6.7's
// confirmation record, so the consumer acts on nothing: every message the
// ledger accepted is refused with a code, and the refusal is recorded exactly
// as a per-message one is. §9 states this as the position for P4.1 — "the
// refusal is unconditional until P6.7" — and it is why a pass whose only
// reading was such a message is M12's exit 1: the operator asked for one thing
// and nothing happened.

// mailboxPromptTtlMs is D118's 72 hours in milliseconds: the number
// `shared/prompts` exports as PROMPT_TTL_MS (M3, §10). It is bound here rather
// than declared because a ledger's horizon is max(window, prompt TTL) and both
// are the module's numbers; TestMailboxPromptTtlIsTheModulesNumber holds this
// one to shared/prompts/index.js, so the two cannot drift.
const mailboxPromptTtlMs = 72 * 60 * 60 * 1_000

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
// may ask for. T6 replaces it with the refusal table's own codes.
const mailboxCodeActionRefused = mailbox.Code("refused-action")

// mailboxOutcomePerformed is what `acted[]` reports for a message an action
// really ran for (§4.3's document). The ledger's own word for it is `acted`,
// which is the *status* it stores; the document's field is the outcome a reader
// sees, and §4.3 names it.
const mailboxOutcomePerformed = "performed"

// mailboxAction is one message the ledger accepted and the consumer would act
// on: the value T6's runner is handed. It carries the message and the ledger
// record the action belongs to, and never the mailbox's key.
type mailboxAction struct {
	Message *mailbox.Message
	Entry   mailbox.LedgerEntry
}

// mailboxPerform is §9's fence as a seam: the one place a message becomes an
// action. T6 replaces this with the allow-list and the runner — a value in
// code, evaluated before any spawn (M11) — and until it does the refusal is
// unconditional, which is §9's stated position for P4.1: the confirmation
// record's consumer is P6.7's and is not built, so no real message may reach an
// action today.
//
// It is a variable so that T6 wires its table in one place, and so that this
// task's own tests can reach the refusal path without a spawn. It answers nil
// only when the action was performed.
var mailboxPerform = func(context.Context, mailboxAction) *mailbox.Refusal {
	return &mailbox.Refusal{Code: mailboxCodeActionRefused}
}

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
			"message this build does not recognise is refused, and the confirmation\n" +
			"record that would admit a destructive one is not built yet, so every message\n" +
			"that would be acted on is refused and reported by code. A launch and a\n" +
			"read-only report are the allowance D128's amendment names; it reaches a real\n" +
			"message only once the confirmation record's consumer lands.\n" +
			"\n" +
			"Both directions of the wire are MAC-covered. Every inbox message is verified\n" +
			"before it is acted on, and one that does not verify is refused and never\n" +
			"acted on — and because nothing can retire it, it is read and refused again on\n" +
			"every pass until an operator removes the node with their own credential.\n" +
			"Every reply and every heartbeat is signed before it is written, so a value\n" +
			"the signer refuses is not written at all.\n" +
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
			"an action is performed, never after.\n" +
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
	uid       string
	item      string
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
// serve any other phone on the machine. When no configured pairing has a key
// there is nothing to serve at all, and that is §8.3's availability row: exit
// 1, no pass run, nothing written. A keychain that cannot be used at all is the
// keychain package's backend failure, and it is exit 2 (§4.4).
func mailboxKeyring(ctx context.Context, lc *localcfg.Config) ([]mailboxPairing, []string, error) {
	m := lc.Mailbox
	if m == nil || len(m.KeychainItem) == 0 {
		return nil, nil, userErr("project %s has no mailbox configured: its project config has no mailbox.keychain_item block, so there is no pairing whose key this command could read; run `fugaro pair` and record the item name it prints as mailbox.keychain_item[<device uid>]", lc.Name)
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
	missing := make([]string, 0, len(uids))
	for _, uid := range uids {
		item, err := keychain.ParseItem(m.KeychainItem[uid], lc.Name)
		if err != nil {
			return nil, nil, userErr("mailbox.keychain_item.%s: %v; the item's name is what the key is read from, so a pairing with no readable name is a config the operator fixes", uid, err)
		}
		key, err := store.Key(ctx, item)
		switch {
		case err == nil:
		case errors.Is(err, keychain.ErrNoKey):
			missing = append(missing, item.Name())
			continue
		default:
			return nil, nil, remote(fmt.Errorf("reading this machine's keychain item %s for pairing %s: %w", item.Name(), uid, err))
		}
		// The key is captured once and only inside these three values: the gate
		// holds it and asks for its bytes per verification, and each signer does
		// the same per tag.
		pairings = append(pairings, mailboxPairing{
			uid:       uid,
			item:      item.Name(),
			gate:      mailbox.NewGate(key, mailbox.GateOptions{}),
			signer:    mailboxOutboxSigner(key),
			heartbeat: mailboxHeartbeatSigner(key),
		})
	}
	if len(pairings) == 0 {
		return nil, nil, userErr("no pairing key is readable at the keychain items project %s names (%s), so there is no message this command could verify and no reply it could sign: run `fugaro pair` for the phone that is missing and record the item name it prints; the key itself is never printed and is never in the project config", lc.Name, strings.Join(missing, ", "))
	}
	warnings := make([]string, 0, len(missing))
	for _, name := range missing {
		warnings = append(warnings, fmt.Sprintf("the keychain item %s holds no key, so the pairing it names is not served this run: `fugaro pair` writes it again, and `fugaro pair revoke` deletes it on purpose", name))
	}
	return pairings, warnings, nil
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
	pairings, warnings, err := mailboxKeyring(ctx, lc)
	if err != nil {
		return err
	}
	ledger, err := mailbox.OpenLedger(mailbox.LedgerOptions{Project: lc.Name, PromptTtlMs: mailboxPromptTtlMs})
	if err != nil {
		return userErr("this machine's mailbox ledger could not be read (%v); it is the consumer's own dedupe memory at $XDG_STATE_HOME/fugaro/mailbox/%s/ledger.json, and acting on a delivery it could not record is the one thing this command will not do", err, lc.Name)
	}
	c := &mailboxConsumer{
		lc:          lc,
		db:          db,
		ledger:      ledger,
		pairings:    pairings,
		held:        map[string]mailbox.LeaseRun{},
		holder:      mailboxBoot(),
		noHeartbeat: o.noHeartbeat,
		out:         cmd.OutOrStdout(),
		asJSON:      o.asJSON,
		warnings:    warnings,
	}
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
// Exit 1 iff **all four** hold: at least one message was read, no action was
// performed (nothing delivered, nothing replied), every message read was
// refused, and at least one of those refusals was an action refusal — D128's
// fence — rather than a per-message one.
//
// Clause 1 is what keeps a pass that held no lease and a pass over an empty
// inbox at 0, and clause 4 what keeps a duplicates-only pass at 0. The loop
// mode does not exit on this predicate: §4.4's second exit-1 row is a `--once`
// pass's, and a long-lived consumer that stopped because a phone asked for
// something it may not do would be a consumer that stopped its mailbox.
func mailboxExit(v mailboxVerdict) error {
	if v.read > 0 && v.performed() == 0 && v.refused == v.read && v.actionRefused > 0 {
		return userErr("nothing happened: the %d message(s) this pass read were all refused, and %d of those refusals was an action refused under D128", v.read, v.actionRefused)
	}
	return nil
}

// mailboxVerdict is a cycle's counters, as M12's predicate reads them.
type mailboxVerdict struct {
	read          int
	delivered     int
	replied       int
	duplicates    int
	refused       int
	actionRefused int
}

// performed is clause 2: what the pass did, in the words §7's loop uses — a
// message delivered, a reply written. A sweep is T7's and is not a pass's.
func (v mailboxVerdict) performed() int { return v.delivered + v.replied }

func (v *mailboxVerdict) add(other mailboxVerdict) {
	v.read += other.read
	v.delivered += other.delivered
	v.replied += other.replied
	v.duplicates += other.duplicates
	v.refused += other.refused
	v.actionRefused += other.actionRefused
}

// mailboxConsumer is the running consumer: one pass over each configured
// pairing, over one database, with one ledger.
type mailboxConsumer struct {
	lc       *localcfg.Config
	db       *rtdb.Client
	ledger   *mailbox.Ledger
	pairings []mailboxPairing
	// held is the one lease this consumer holds per pairing, keyed by the
	// mailbox it was taken on: the loop takes it on its first pass and keeps it
	// (leaseFor), which is what the module's own take rule requires.
	held        map[string]mailbox.LeaseRun
	holder      string
	noHeartbeat bool
	out         io.Writer
	asJSON      bool
	warnings    []string
}

// cycle is one pass over every configured pairing, reported per pairing: each
// has its own heartbeat, its own inbox and its own key, so each is a pass of
// its own and is printed as one.
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
	ID  string `json:"id"`
	For string `json:"for"`
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

// pass is §7's loop for one pairing, in §7's order.
func (c *mailboxConsumer) pass(ctx context.Context, pairing mailboxPairing) (mailboxPass, error) {
	pass := mailboxPass{
		mailbox: pairing.uid,
		lease:   mailboxLease{State: string(mailbox.LeaseNone)},
		acted:   []mailboxActed{},
		replied: []mailboxReplied{},
		refused: []mailboxRefused{},
		notes:   append([]string{}, c.warnings...),
	}
	now := c.now()
	pass.at = time.UnixMilli(now).UTC().Format(time.RFC3339)

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
	return pass, nil
}

// leaseFor is the lease this pass runs under. **A consumer takes a mailbox's
// lease once and holds it**, which is the module's own rule rather than a
// shortcut: `takeLease` answers `already-claimed` to a fresh claim "whatever
// its holder string — a lease is renewed by releasing it, not by re-taking
// it", and §7's loop passes over a mailbox every `--interval` (five seconds by
// default). A pass that took the lease again on every iteration would be
// refused its own claim and would act on nothing until the claim went stale ten
// minutes later — a consumer that stopped serving its mailbox after its first
// pass.
//
// So the take happens on the first pass for a pairing and every pass after it
// reuses what it holds; past `ActionBound(claim, ttl)` the holder re-reads the
// lease before it acts (deliver's own check), and a re-read that comes back not
// held drops the claim here so the next pass takes it afresh, which is the
// module's caller obligation for a stale claim.
func (c *mailboxConsumer) leaseFor(ctx context.Context, pairing mailboxPairing, now int64) (mailbox.LeaseRun, error) {
	if run, holds := c.held[pairing.uid]; holds {
		return run, nil
	}
	run, err := mailbox.Take(ctx, c.db, c.leaseOptions(pairing, now))
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
// one authoritative clock's reading (D117), and the heartbeat signer built on
// the pairing's key. A signer that fails costs the lease, which is M4b's whole
// point.
func (c *mailboxConsumer) leaseOptions(pairing mailboxPairing, now int64) mailbox.LeaseOptions {
	return mailbox.LeaseOptions{
		Mailbox:     pairing.uid,
		Holder:      c.holder,
		At:          now,
		Sign:        pairing.heartbeat,
		NoHeartbeat: c.noHeartbeat,
	}
}

// now is the one instant a pass decides against: the database's own clock where
// it has answered (the Date header of the last response), and this machine's
// only until then. §6's lease and the ledger both refuse a `now` that runs
// backwards, so the reading is taken once per pairing pass and never per
// message.
func (c *mailboxConsumer) now() int64 {
	if t, ok := c.db.ServerNow(); ok {
		return t.UnixMilli()
	}
	return mailboxNow().UTC().UnixMilli()
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
		again, err := mailbox.Confirm(ctx, c.db, c.leaseOptions(pairing, now))
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
	result, err := c.ledger.Perform(message, mailbox.KindOf(message), now, func(entry mailbox.LedgerEntry) {
		// §9's fence, evaluated before anything is spawned (M11). T5 refuses
		// everything here, so the refusal is recorded against the record the
		// ledger just committed.
		if refuse := mailboxPerform(ctx, mailboxAction{Message: message, Entry: entry}); refuse != nil {
			pass.refuse(entry.ID, refuse.Code, true)
			return
		}
		pass.verdict.delivered++
		pass.acted = append(pass.acted, mailboxActed{ID: entry.ID, Kind: entry.Kind, Outcome: mailboxOutcomePerformed})
	})
	if err != nil {
		// The ledger committed nothing, so nothing was performed — which is the
		// fence rather than a message's fault. It is this machine's own state,
		// so it is the operator's to fix, and the pass stops rather than acting
		// on a delivery it could not record.
		return userErr("this machine's mailbox ledger could not be written (%v); nothing was performed", err)
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
		// The perform callback above saw it: either the action ran, or §9's
		// fence refused it and recorded the code.
	default:
		// The ledger answered an outcome no delivery of an accepted record can
		// have (nothing-to-act): it read a message and did nothing with it, and
		// the module's own word for that is the code it is reported under.
		pass.refuse(message.ID, mailbox.Code(result.Outcome), false)
	}
	return nil
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
