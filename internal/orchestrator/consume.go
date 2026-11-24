// The cloud consumer's pass (mobile plan task P5.1f; the design is
// docs/mobile/design/always-on-orchestrator.md, its section 4.2 and its
// section 4.9).
//
// WHAT THIS FILE IS. Section 4.2's first of the resident orchestrator's two
// functions: "reads the inbox, verifies the tag, holds the lease, acts, writes
// the outbox and the heartbeat. This is the phase-1 consumer of Task P4.1
// (`fugaro mailbox`, D113c's answer), running in the cloud instead of on the
// laptop." What is here is the reading, the tag, the lease and the two writes;
// what is NOT here is the acting, and the paragraph below says why.
//
// **It is the same pass and not a second implementation of one.** Every rule
// the phase-1 consumer applies is the mailbox module's, and this pass calls
// the same functions the command calls: the same reader (`mailbox.ReadInbox`,
// which decodes each child strictly, refuses a child that is not an object and
// orders the inbox oldest-first, so the command's reporting types are the only
// thing it adds), the same verification (`Gate.Verify`
// — M4a's "the tag before the ledger", and there is no second tag function in
// this tree), the same lease (`Take`, `Renew`, `HeartbeatPath` — M1's one node
// and its compare-and-swap), and the same create-only answer (`WriteReply`,
// whose own header says the create-only rule is enforced in code). What
// differs is the identity the writes are made with and the process that holds
// them, and that difference is the whole of P5.1f's wiring: the command writes
// as the operator's admin credential, which bypasses the rules (D113b's
// honesty note), while this pass must satisfy CONSUMER (`auth.token.mc == $mb`)
// to write either node at all — so this is the first mailbox writer the rules
// can hold to "the outbox and the heartbeat and nothing else" (section 4.3).
// That claim comes from internal/budget/token's `MintConsumer`, which the
// caller exchanges for an ID token and builds a mailbox's writer with; the
// reads are the deployment's own client. The pass is handed those two
// credentials apart (ConsumerReader and ConsumerWriter) because they cannot be
// one: the rules give the consumer two writes and no read at all.
//
// **What it does not act on, and why the phone is still owed an answer.**
// Section 4.2's fourth step — "acts" — is the launcher's and the envelope's,
// and it is P5.1g's: a launch request carried as a job execution, a cancel, a
// prompt answer handed to a run, a merge raised as a prompt. Those are
// behaviours with a product decision behind them (the owner's answers of
// 2026-11-14 to the plan's P5.1-Q4, Q5 and Q10), and a pass that invented an
// action here would be the second implementation of a step this task's own
// brief puts elsewhere — the file is to read, verify, lease and write, "and
// nothing else". So every record this pass accepts is answered in the outbox
// and carried out no further: the phone learns that its message reached the
// consumer and that nothing was performed, which is the honest thing to say
// and is what M2's "a reply the ledger has not retired is a reply still owed"
// asks for. The sentence is this file's (`consumerNotCarried`), the reply is
// the module's (`mailbox.WriteReply`), and the record is answered once: the
// reply's id is derived from the message's by the module's own `mailbox.ReplyID`
// — the one spelling, which the phase-1 command and the merged simulator use
// too — so the node's create-only rule makes a second pass' attempt a
// `ReplyPresent` and never a second answer.
//
// **There is no ledger here, and that is a fact about where this pass runs.**
// The phase-1 consumer commits each delivery to a file-backed ledger before it
// acts (`mailbox.OpenLedger`, retained at least the TTL plus a sweep interval,
// section 4.9's replay row). A Cloud Run instance has a per-instance `/tmp` and
// a tick may reach a different instance, so a file is not a place a fence can
// live; and the rules grant CONSUMER exactly two writes (the outbox and the
// heartbeat), so there is no node to put one in either — design U6's "the four
// mailbox writes are the whole surface the cloud consumer needs". What keeps
// that from being a hole is what this pass does with a record: it acts on
// nothing, and its one write is create-only under an id the record's own id
// derives. A record re-read on a later tick is re-verified, re-answered as
// `ReplyPresent` and nothing else. The ledger becomes load-bearing the day an
// action does (a launch's fence is the launch claim itself, and a replayed
// action's is the id), and that day is the acting half's, not this file's.
//
// **The lease is taken or renewed, never assumed.** A tick is one pass of a
// process that keeps nothing between passes, so it asks the node who holds the
// mailbox: `Renew` first, because a heartbeat holding THIS holder's own claim
// is the common case for a service whose identity is stable — and `Take` when
// there is no claim or someone else's, which is M1's own decision and not
// this file's ("a lease is renewed by releasing it, not by re-taking it": a
// take of a fresh claim is refused to everyone, this consumer included). A
// verdict that is neither held nor taken over means **nothing is read and
// nothing is written** — not the inbox, not the outbox, not the heartbeat —
// which is what makes the lease the fence between two consumers (section 4.9,
// and the laptop waking while this one ticks).
//
// **The clock is the database's.** Every instant here — the lease's `at`, the
// reply's `at`, the gate's future tolerance — is read from the reader's own
// clock (`ServerNow`, D117), and a reader that cannot answer one reads nothing
// at all rather than deciding against a container's clock (D1230's rule, which
// the resident pass beside this file holds to as well).
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/mailbox"
	"github.com/dimipaun/fugaro/internal/mailbox/keychain"
)

// consumerNotCarried is what the phone is told about a message this pass
// accepted and carried no further. It is written into the reply's text, so it
// is spoken by the phone and it says what happened rather than what went
// wrong: the message was read by the resident orchestrator, and no action was
// performed. It is this file's one sentence (the module owns the codes and the
// phase-1 command owns its own sentences) and it names no id, no mailbox and
// no text of the message it answers.
const consumerNotCarried = "nothing was performed: the resident orchestrator read the message and carried nothing out"

// consumerStopRequest is what the phone is told about a **stop request** — the
// schema's `stop` kind, which the app writes when the human says to stop what
// they are looking at (mobile plan task P5.1g; the owner's answer Q10(c), the
// design's section 4.8).
//
// It is a sentence of its own rather than consumerNotCarried because a stop
// request is the one record whose answer the human is waiting for: Q10's answer
// (c) is a request rather than a phone-writable kill switch, so the honest
// answer says both halves — the request was read, and nothing was stopped from
// here, because there is nothing here to stop it with (the enforcement side is
// `config/kill/**` and `config/caps/**`, which no client writes and this pass
// has no grant for). What it does not do is pretend: it names where the stop
// can be made, the console or the laptop, where a human can act on the request,
// and it names the one thing the phone can always do by itself, which is to
// revoke its own pairing.
//
// It is deliberately not a prompt in the outbox. A prompt is a question with
// options the phone reads aloud and answers, and the consumer would then be
// asking the human to confirm a stop it cannot perform — D168's own rule is
// that the resident orchestrator never treats its own prompt as answered and
// never synthesises an owner confirmation, and a prompt here would be exactly
// that. Nothing raises a prompt for a stop record — no console or laptop code
// does — so the request travels to the human as the outbox answer they will
// hear and nothing else.
const consumerStopRequest = "nothing was stopped: the resident orchestrator read your stop request, and this installation has no phone-writable kill switch. Stop the run from the console or the laptop, or revoke this device's pairing; the phone cannot stop a run by itself."

// ConsumerReader is every read one consumer pass makes, and it is the
// deployment's own database client — the service-account one, `rtdb.Auth{Source:
// ...}`, which is the client the resident service already reads its node
// through. It reads the inbox, the ETag a compare-and-swap needs on the
// heartbeat and on the outbox, and the database's clock.
//
// **Every one of those reads is an IAM read, and that is not a shortcut.** The
// rules grant the consumer no read at all under any mailbox — CONSUMER's own
// comment is that it "is what may write the outbox and the heartbeat, and
// nothing else — in particular it may read no mailbox, its own or another's" —
// so the inbox (which has no `.read` clause in the rules at all) and the two
// nodes whose `.read` is `MAILBOX`-only are all unreadable to the `mc` ID token
// the writes below use, and a pass that read with it would read nothing. What
// makes the read possible is an identity the rules do not see: section 4.3's
// table owes the `orch` account that permission and does not carry the row yet
// (the P5.1c review's finding A), which is one of the three facts the service's
// own header names as this release's reason to wire no consumer.
//
// It is deliberately NOT the `mc` credential and not a store that can write:
// this half reads, and a wiring that could write through what it reads with is
// the unfenced writer ConsumerWriter's own paragraph is about.
type ConsumerReader interface {
	Get(ctx context.Context, path string, out any) (bool, error)
	GetETag(ctx context.Context, path string, out any) (string, bool, error)
	ServerNow() (time.Time, bool)
}

// ConsumerWriter is one mailbox's write half: the compare-and-swap write the
// two nodes CONSUMER admits are written with, and the mailbox the credential
// making that write is minted for.
//
// **It is not a database client, and a `*rtdb.Client` is not one.** The write
// is fenced by CONSUMER, `auth.token.mc != null && auth.token.mc == $mb`, and
// that claim is carried by nothing but the ID token a minted consumer token is
// exchanged into (`token.MintConsumer`, then `Exchange` with the project's web
// API key — the caller's step, because the key is a fact of the deployment).
// The client the service reads its node through is a **service-account
// bearer**, which the rules never see and therefore never fence: wiring it here
// literally would give a mailbox a writer whose every write bypasses CONSUMER,
// which is the laptop consumer's shape (D113b's honesty note) and the whole of
// what this pass exists to replace.
//
// Mailbox is how a caller states that claim, and the pass refuses a mailbox its
// writer does not name (consumeMailbox): a wiring that handed one phone's
// writer to another phone's mailbox is then a recorded refusal rather than a
// write CONSUMER turns into a 401 the pass cannot tell from an outage — and it
// is the one thing about the credential a caller can be held to in code.
type ConsumerWriter interface {
	PutIfMatch(ctx context.Context, path, etag string, v any) error
	Mailbox() string
}

// consumerCAS is the mailbox module's own two-method store (HeartbeatStore,
// OutboxStore) over this pass' two credentials: the compare-and-swap's read is
// the reader's and its write is the writer's. The module hands a caller one
// store because its other caller — the phase-1 command, P4.1 — holds one
// credential that may do both; here the rules force the split (CONSUMER admits
// the write and no read), so the two halves meet at this adapter and nowhere
// else.
//
// The fence holds across the two because an ETag is the database's answer about
// the value at a location (`X-Firebase-ETag` on the read, `If-Match` on the
// write) and not about the caller: the ETag a service-account read answers is
// the one an `mc`-authenticated write presents, and a value that moved in
// between refuses the write with ErrPrecondition — which is the whole of what
// the compare-and-swap promises.
type consumerCAS struct {
	reader ConsumerReader
	writer ConsumerWriter
}

func (c consumerCAS) GetETag(ctx context.Context, path string, out any) (string, bool, error) {
	return c.reader.GetETag(ctx, path, out)
}

func (c consumerCAS) PutIfMatch(ctx context.Context, path, etag string, v any) error {
	return c.writer.PutIfMatch(ctx, path, etag, v)
}

// ConsumerMailbox is one phone this pass may serve: the mailbox (the phone's
// own `m~` uid, which is the node the inbox, the outbox and the heartbeat all
// live under and the name CONSUMER's `$mb` compares against), the pairing's
// MAC key (section 4.2's third copy of it: P5.1e's Secret Manager container,
// read as a set by keychain.Cloud.Keys), and the writer whose credential is
// minted for exactly this mailbox.
//
// The three travel together because none is any use alone: a key with no
// mailbox verifies nothing, a mailbox with no key is a phone whose messages
// this pass would have to refuse, and a mailbox with no writer is one whose
// messages could be read and never answered. The writer is per mailbox and not
// per pass because the claim it carries names one mailbox (`mc == $mb`), so a
// deployment serving two phones holds two of them.
type ConsumerMailbox struct {
	Mailbox string
	Key     keychain.Key
	Writer  ConsumerWriter
}

// ConsumerOptions is what one pass is told beside the reader: who it is, and
// which mailboxes it serves.
type ConsumerOptions struct {
	// Holder is this consumer's name in the heartbeat — the identity the lease
	// is taken and renewed under, and the value the phone reads as its
	// consumer's presence. It is stable across ticks on purpose: a holder id
	// that changed every pass would take the lease over every pass (a take of
	// another holder's fresh claim is refused) and the phone would see a
	// consumer appear and vanish once a minute. The resident service passes the
	// installation's own orchestrator account, which is the identity that really
	// writes these nodes — not the id of the instance a tick landed on, and not
	// independently the node's own id, because two installations of one owner
	// each have their own account and a phone reads the holder for presence.
	Holder string
	// Mailboxes are the pairings this deployment holds a key for, in the order
	// the passes are made. An empty list is a pass that reaches no mailbox and
	// writes nothing: an installation whose pairing-key container is empty has
	// no phone whose messages could verify.
	Mailboxes []ConsumerMailbox
}

// ConsumerOutcome is what one pass came to, over every mailbox it was given.
//
// Read is the records it read out of the inboxes and Verified is the ones whose
// tag and shape the gate accepted; the difference is what a phone's message
// that this pass will not act on — an unsigned one, one reflected from the
// outbox into the inbox, one this build cannot decode — is counted as, and
// **Refused is that count per mailbox** rather than a reason: a refusal is
// recorded against the record's own id and is never answered (a reply to a
// record nothing accepted would be a write an attacker could ask for).
// Carried is the records this pass handed on, and it is zero in this release
// by construction (the header): it is counted rather than left out because the
// number is what the acting half will move, and a field that did not exist
// would have to be added to this outcome the day it does.
type ConsumerOutcome struct {
	Mailboxes int
	// Leases is how many mailboxes this pass held a lease on — held or taken
	// over — and Lost is how many it did not. The two are the whole of the
	// first decision a pass makes, they add up to Mailboxes, and a mailbox in
	// the second half was read and written in no way at all: whether the lease
	// was another consumer's, the reader or the writer refused the heartbeat, the
	// writer is minted for another mailbox, or the mailbox name is one this
	// package will not build a path from.
	Leases int
	Lost   int
	Read   int
	// Verified, Refused and Carried are counted over the records of the
	// mailboxes this pass held a lease on, and over no others.
	Verified int
	Refused  int
	Carried  int
	// Replied is how many answers reached the outbox, and Present is how many
	// were already there: both are the create-only rule's two outcomes and
	// neither is a failure (mailbox.ReplyWritten and mailbox.ReplyPresent).
	Replied int
	Present int
	// Refusals are the mailbox refusals this pass could not record as a
	// verdict — a reply the signer refused, a mailbox name the module will not
	// build a path from — one per mailbox that met one. A pass that carried on
	// past one is the point: a single phone's broken key must not stop another
	// phone's message from being answered.
	Refusals []error
}

// Consume is one pass: the lease, the inbox, the tag, and the two writes —
// once per mailbox, in the order it is given them.
//
// The two credentials arrive apart because they are two identities: `reader` is
// the deployment's own client, which makes every read (the rules grant the
// consumer none), and each mailbox's ConsumerWriter is the `mc` credential its
// two writes are made with. Nothing here can be handed one client for both,
// because a writer must name the mailbox its claim serves and a plain database
// client cannot.
//
// A reader that cannot answer the clock is the one error it returns, and nothing
// is attempted behind it: every instant below (the lease's, the reply's, the
// gate's tolerance) is that clock's, and a pass that fell back to a container's
// would be judging a phone's `at` against the wrong now. A failure on one
// mailbox — an unreachable node, a refused read, a writer minted for another
// phone — is that mailbox's own refusal, collected in the outcome and not
// fatal: the next mailbox is a different phone with a different key, and its
// message has nothing to do with the failure of the first.
func Consume(ctx context.Context, reader ConsumerReader, o ConsumerOptions) (ConsumerOutcome, error) {
	now, ok := reader.ServerNow()
	if !ok {
		return ConsumerOutcome{}, errors.New("the database's clock could not be read, so no message is read or written: a consumer that judged a phone against its own container's clock would take a lease the database disagrees with")
	}
	at := now.UTC()
	var out ConsumerOutcome
	for _, m := range o.Mailboxes {
		out.Mailboxes++
		run, err := consumeMailbox(ctx, reader, m, o.Holder, at)
		if err != nil {
			// A mailbox this pass could not even reach is a mailbox it holds no
			// lease on, so it is counted as lost as well as recorded: the two
			// numbers are then the whole of the first decision, one mailbox at
			// a time, however it went.
			out.Lost++
			out.Refusals = append(out.Refusals, err)
			continue
		}
		if !run.leased {
			out.Lost++
			continue
		}
		out.Leases++
		out.Read += run.read
		out.Verified += run.verified
		out.Refused += run.refused
		out.Carried += run.carried
		out.Replied += run.replied
		out.Present += run.present
		out.Refusals = append(out.Refusals, run.refusals...)
	}
	return out, nil
}

// consumerRun is one mailbox's pass: what it read, what it verified, and what
// it wrote. It is unexported because a caller reads the outcome above; the
// fields are the same numbers under the same names.
type consumerRun struct {
	leased   bool
	read     int
	verified int
	refused  int
	carried  int
	replied  int
	present  int
	refusals []error
}

// consumeMailbox is the pass over one phone: take or renew the lease, read the
// inbox, verify each record, and answer the ones that verified. A mailbox whose
// lease this pass does not hold is returned with leased false and reads
// nothing — the inbox read below is behind that verdict and not in front of it.
//
// The first thing it asks is the writer's own name for its credential, and it
// asks before any read and any write: a mailbox whose writer is minted for
// another one is a wiring whose every write the rules would refuse — and whose
// reads would still be this phone's, which is the shape that must not be usable
// at all. It is a refusal like any other (recorded, counted lost, nothing
// attempted), not a panic and not a silent substitution.
func consumeMailbox(ctx context.Context, reader ConsumerReader, m ConsumerMailbox, holder string, at time.Time) (consumerRun, error) {
	var run consumerRun
	if m.Writer == nil {
		return run, fmt.Errorf("mailbox %s has no writer, and CONSUMER admits a write of the heartbeat or the outbox under no other credential", m.Mailbox)
	}
	if claimed := m.Writer.Mailbox(); claimed != m.Mailbox {
		return run, fmt.Errorf("the writer for mailbox %s is minted for %s, and CONSUMER (`mc == $mb`) admits a write only into the mailbox its own claim names", m.Mailbox, claimed)
	}
	// What the module's two-method stores are handed is the pair, never one
	// client: the compare-and-swap's read is the reader's and its write the
	// writer's (consumerCAS, and the rules are why).
	st := consumerCAS{reader: reader, writer: m.Writer}
	opts := mailbox.LeaseOptions{
		Mailbox: m.Mailbox,
		Holder:  holder,
		At:      at.UnixMilli(),
		Sign:    mailbox.NewHeartbeatSigner(m.Key).Heartbeat,
	}
	// Renew first: this pass holds the mailbox on every tick after the first,
	// and a take of a fresh claim is refused to its own holder (lease.go's own
	// sentence). A lost renewal is not a refusal — it is "nobody holds this
	// mailbox, or somebody else does" — so the take below decides it, which is
	// the one place M1's take-over rule lives.
	lease, err := mailbox.Renew(ctx, st, opts)
	if err != nil {
		return run, fmt.Errorf("the lease on mailbox %s could not be renewed: %w", m.Mailbox, err)
	}
	if lease.Verdict == mailbox.LeaseLost {
		lease, err = mailbox.Take(ctx, st, opts)
		if err != nil {
			return run, fmt.Errorf("the lease on mailbox %s could not be taken: %w", m.Mailbox, err)
		}
	}
	// A verdict of lost here is another consumer's fresh claim, or a heartbeat
	// this build cannot read: either way this pass reads nothing and writes
	// nothing, and the lease's own answer is reported (section 4.9's second
	// fence).
	if lease.Verdict != mailbox.LeaseHeld && lease.Verdict != mailbox.LeaseTakenOver {
		return run, nil
	}
	run.leased = true

	// The reader is the module's (mailbox.ReadInbox) and not one written here:
	// the phase-1 consumer of `fugaro mailbox` (P4.1) reads this same node
	// through this same function, and the task's own brief is that the cloud
	// consumer is that pass run by a different process rather than a second
	// implementation of it. What differs between the two is what they do with a
	// record, not how one is read: the order, the strict decode, the absent-node
	// reading and the code an unreadable child is refused with are the module's
	// and are one answer for both.
	items, err := mailbox.ReadInbox(ctx, reader, m.Mailbox)
	if err != nil {
		return run, fmt.Errorf("the inbox of mailbox %s could not be read: %w", m.Mailbox, err)
	}
	run.read = len(items)
	gate := mailbox.NewGate(m.Key, mailbox.GateOptions{Now: func() time.Time { return at }})
	signer := mailbox.NewOutboxSigner(m.Key)
	for _, item := range items {
		if item.Refusal != nil {
			// A child the reader could not read at all: recorded against the
			// record's own id and never answered, which is the phase-1 pass' own
			// reading of the same item and the reason the reader hands the
			// refusal back rather than an error.
			run.refused++
			continue
		}
		message, why := gate.Verify(item.Record)
		if why != nil {
			// Recorded against the record's own id and never answered: a
			// refusal is a record this pass will not act on, and a reply to it
			// would be a write the record's author asked for by writing an
			// inbox node (the phase-1 pass' own reading).
			run.refused++
			continue
		}
		run.verified++
		if !consumerCarries(message.Kind) {
			// A kind the phone does not send: a prompt travels host to phone
			// (the phase-1 runner refuses one here for the same reason), and an
			// unknown kind is a record the schema would have refused a moment
			// ago. Neither is owed an answer.
			run.refused++
			continue
		}
		answer, err := mailbox.WriteReply(ctx, st, signer, mailbox.Reply{
			ID:      mailbox.ReplyID(message.ID),
			Sender:  m.Mailbox,
			Text:    consumerAnswerText(message.Kind),
			At:      at.UnixMilli(),
			Kind:    "answer",
			ReplyTo: message.ID,
		})
		if err != nil {
			run.refusals = append(run.refusals, fmt.Errorf("the answer for mailbox %s could not be written: %w", m.Mailbox, err))
			continue
		}
		switch answer.Verdict {
		case mailbox.ReplyWritten:
			run.replied++
		case mailbox.ReplyPresent:
			// The outbox already held this answer: a tick that read the same
			// record again, which is what the derived id and the create-only
			// rule are for (M10, C0).
			run.present++
		case mailbox.ReplyRefused:
			// The signer or the schema refused the value, so nothing was
			// written and the answer is still owed: the next pass reads the
			// record again and owes it again (WriteReply's own header).
			run.refusals = append(run.refusals, fmt.Errorf("the answer for mailbox %s was refused, so nothing was written to the outbox and it is owed again on the next pass", m.Mailbox))
		}
	}
	return run, nil
}

// The kinds the module's closed vocabulary names and does not export a constant
// for: `mailbox.Kinds` is the list a schema reader checks against, and it spells
// these as `message`, `answer`, `action`, `registration` and `stop`
// (`KindMessage` is the one it exports, because a message with no `kind` field
// is one). The phase-1 command spells the same words as its `mailboxKindAnswer`
// and its neighbours, and a third spelling here is the module's list read out
// rather than a vocabulary of this file's own.
const (
	consumerKindAnswer       = "answer"
	consumerKindAction       = "action"
	consumerKindRegistration = "registration"
	// consumerKindStop is the phone's stop request (mobile plan task P5.1g, the
	// owner's answer Q10(c)): the app asking a human to stop what they are
	// looking at. It is a kind a phone sends, so this pass carries it — see
	// consumerCarries — and it is answered with consumerStopRequest.
	consumerKindStop = "stop"
)

// consumerCarries reports whether this pass' acting half carries a record of
// this kind. It is false for every kind in this release (the header), and the
// five it names are exactly the kinds a phone sends: `message`, `answer`,
// `action`, `registration` and `stop`. A `prompt` is deliberately not one of
// them — it travels host to phone, and a pass that answered one out of its own
// inbox would be answering its own question — and neither is a kind this build
// does not know, which the schema has refused by the time this is asked.
//
// A `stop` is carried although nothing here acts on it, and that is the point
// rather than an oversight: a record this pass refused outright would never be
// answered, and an unanswered record is one the phone's own at-least-once rule
// keeps owing (M2) — so the human's stop request would sit in the app as a
// pending utterance with nothing to say about it. What the answer says is
// consumerStopRequest: it was read, and nothing was stopped from here.
func consumerCarries(kind string) bool {
	switch kind {
	case mailbox.KindMessage, consumerKindAnswer, consumerKindAction, consumerKindRegistration, consumerKindStop:
		return true
	}
	return false
}

// consumerAnswerText is what the outbox says about a record of this kind: the
// stop request's own sentence, and consumerNotCarried for every other kind this
// pass carries (the header's paragraph on why nothing is carried out).
func consumerAnswerText(kind string) string {
	if kind == consumerKindStop {
		return consumerStopRequest
	}
	return consumerNotCarried
}
