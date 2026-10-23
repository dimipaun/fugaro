// The outbox write (mobile P4.1, Task T2a): the one value this command puts in
// front of the phone, built, signed and written in that order.
//
// §8.2 of docs/plans/2026-10-18-mobile-mailbox-consumer.md is the rule this file
// implements, and §6 is where the node lives:
// `mailbox/<mailbox>/outbox/<id>` = `{id, sender, text, at, mac}` plus whatever
// the message's kind carries (`kind`, `reply_to`, `agent`). The merged rules give
// that node a `.write` of `CONSUMER && newData.exists() && !data.exists()` — once
// per id — and an IAM credential **bypasses both the claim and the create-only
// clause** (C0 of the plan), so the create-only rule is **enforced here, in
// code**, and the merged grant is a fence on token-bearing writers rather than on
// this one. That is M10's "one write to the outbox per answer", and it is why the
// read below is not optional.
//
// **The write is signed or it does not happen** (M4b, the third row of M4's
// table): the value is built first, the tag is computed over exactly those
// fields, `mac` is added last, the signed value is validated against the schema
// (M10: every value this consumer writes is validated first), and only then does
// it reach the store. A value the signer errors on is not written, and the answer
// is the **per-write refusal** — a recorded outcome of a completed pass and exit
// 0 (§4.4), never §8.3's startup exit. Nothing is recorded for it either, so the
// reply is still owed: the next pass reads the message again and owes it again,
// which is M2's "a reply the ledger has not retired is a reply still owed".
//
// **What this file is not.** It holds no tag function and no key (those are
// mac.go's — the `Signer` the caller passes in is T2a's half of the one
// function); no ledger, so it cannot tell whether a reply is still owed (T4's);
// no command, no flag and no output (T5's, T6's); and no write to any other node
// — the heartbeat's write is the lease (lease.go, M1) and the inbox is the
// phone's (C1).
package mailbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Reply is one answer the consumer owes the phone: the value §6's outbox node
// carries, before it is signed.
//
// **The three optional fields are absent when empty**, and empty is the only
// spelling of absent here because the schema refuses an empty string for every
// one of them: `kind` is one of four words, `reply_to` is a database key and a
// database key is non-empty, and `agent` is an identity. So there is no
// `HasReply`/`HasReplyTo` pair the way Message needs one for `acked` — the wire
// has no `""` that means anything else.
type Reply struct {
	// ID is the reply's own id, and it is also its outbox node's key: a reply is
	// created once and never overwritten, so the id is the reply-once fence.
	ID string
	// Sender is the mailbox the answer is written under — the account's own key,
	// which is what the phone reads the outbox at and what every message's
	// `sender` names.
	Sender string
	// Text is what the answer says. **It is never printed**, logged, recorded or
	// put in an error: §4.3's rule is that a message's id may appear in the
	// command's output and its text never does.
	Text string
	// At is the instant the reply was written, in milliseconds since the epoch,
	// from the one authoritative clock (D117) — the server's, never this
	// process's own.
	At int64
	// Kind is the reply's kind: empty is the plain `message` a reply to a
	// message is, and a reply to a prompt is an `answer` and must carry ReplyTo.
	Kind string
	// ReplyTo is the prompt an answer settles.
	ReplyTo string
	// Agent is the agent the answer names, when it names one.
	Agent string
}

// Value is the reply as the record the wire carries: the five fields §6 names
// with `mac` left out, plus the fields the kind carries. The tag is computed
// over exactly this and `mac` is added after (§8.2.1), which is why this returns
// the value and not a signed one.
func (r Reply) Value() Record {
	value := Record{"id": r.ID, "sender": r.Sender, "text": r.Text, "at": r.At}
	if r.Kind != "" {
		value["kind"] = r.Kind
	}
	if r.ReplyTo != "" {
		value["reply_to"] = r.ReplyTo
	}
	if r.Agent != "" {
		value["agent"] = r.Agent
	}
	return value
}

// OutboxStore is the part of the database a reply write needs: a read that
// answers the node's ETag and whether the node is there at all, and a write
// conditional on that tag. `*rtdb.Client` is one, and these are the same two
// methods HeartbeatStore needs — two interfaces rather than one because they are
// two different nodes with two different rules, not because the client differs.
type OutboxStore interface {
	GetETag(ctx context.Context, path string, out any) (etag string, found bool, err error)
	PutIfMatch(ctx context.Context, path, etag string, v any) error
}

// ReplySigner signs one outbound value and answers its tag (M4b). `Signer.Tag`
// is one — the shipped implementation, with the key read once at startup (M5) —
// and a test injects one that fails, because M4's third row is unreachable in a
// shipped build: no key that loads refuses one value while accepting another
// (§8.2.4).
type ReplySigner func(value Record) (string, error)

// ReplyVerdict is what one reply write answered, in the words the command
// reports it in.
type ReplyVerdict string

const (
	// ReplyWritten: the signed value landed in the outbox.
	ReplyWritten ReplyVerdict = "written"
	// ReplyPresent: the outbox already held this reply's id, so nothing was
	// written and nothing was replaced — create-only, in code (M10, C0). It is
	// not a refusal: the answer the phone needs is already there, and a pass
	// that finds it writes nothing and has nothing to retry.
	ReplyPresent ReplyVerdict = "present"
	// ReplyRefused: the value was not written, and Refusal says with which code.
	// This is M4's third row and the schema's own refusal of a value this
	// consumer built wrong, and in both cases the reply is **still owed**.
	ReplyRefused ReplyVerdict = "refused"
)

// ReplyRun is the outcome of one reply write.
type ReplyRun struct {
	Verdict ReplyVerdict
	// Value is the value that landed, and it is nil whenever nothing did: a
	// refused reply has no value for a caller to hand on, for the same reason
	// the gate answers no Message beside a Refusal.
	Value Record
	// Refusal is why a refused value was not written. It is nil for every other
	// verdict.
	Refusal *Refusal
}

// WriteReply builds, signs, validates and writes one reply, create-only.
//
// The order is §8.2.1's and each step is why the next one is safe:
//
//  1. **Build** the value (`Reply.Value`).
//  2. **Sign** it, over exactly those fields, with the pairing's key in the
//     `outbox` direction. A signer that errors means **nothing is written** and
//     the answer is a per-write refusal (M4b) — the reply is owed again next
//     pass, because nothing here records it.
//  3. **Add `mac` last**, so what the tag covers is exactly what lands.
//  4. **Validate** the signed value against the merged schema (M10). A value the
//     schema refuses is not written either, and the refusal carries the schema's
//     own code: `too-long`, `bad-mac`, the kind/field table's own answer.
//  5. **Write it create-only**: the node is read with its ETag, an id already
//     present ends as ReplyPresent with nothing written, and the write is
//     conditional on the tag that was read — so a writer that appears between
//     the read and the write is refused by the store rather than overwritten.
//
// The error is the store's, or the path's: a transport failure, a permission
// refusal, an outage, or a mailbox name or id this package will not build a path
// from. A caller classifies those the way every cloud command does (§4.4);
// nothing this function answers with a refusal is one of them.
func WriteReply(ctx context.Context, store OutboxStore, sign ReplySigner, reply Reply) (ReplyRun, error) {
	path, err := OutboxPath(reply.Sender, reply.ID)
	if err != nil {
		return ReplyRun{}, err
	}
	// A reply with no signer at all is the same refusal as one whose signer
	// failed: unsigned is unsigned, and a caller that reached here with no signer
	// has a bug worth the same answer rather than an unsigned write.
	if sign == nil {
		return ReplyRun{Verdict: ReplyRefused, Refusal: refuse(CodeBadMAC, "mac")}, nil
	}
	value := reply.Value()
	tag, err := sign(value)
	if err != nil {
		// M4's third row, and the code is the schema's own for a tag that is not
		// a tag: the value has no `mac` it could carry, which is `bad-mac`'s
		// meaning. Nothing is written and nothing is recorded, so this reply is
		// owed again on the next pass (M2, §8.2.4).
		return ReplyRun{Verdict: ReplyRefused, Refusal: refuse(CodeBadMAC, "mac")}, nil
	}
	value["mac"] = tag
	if _, refusal := ValidateMessage(value); refusal != nil {
		return ReplyRun{Verdict: ReplyRefused, Refusal: refusal}, nil
	}
	etag, found, err := store.GetETag(ctx, path, nil)
	if err != nil {
		return ReplyRun{}, err
	}
	if found {
		return ReplyRun{Verdict: ReplyPresent}, nil
	}
	if err := store.PutIfMatch(ctx, path, etag, value); err != nil {
		return ReplyRun{}, err
	}
	return ReplyRun{Verdict: ReplyWritten, Value: value}, nil
}

// OutboxPath is the node one reply is written to: `mailbox/<mailbox>/outbox/<id>`.
// The mailbox is the account's own key and the id is the reply's, and both are
// validated as the database keys the rules will treat them as — a name carrying
// one of the characters the database refuses in a key (`.`, `$`, `#`, `[`, `]`,
// `/`, `%`, or a control character) is refused here rather than escaped or sent,
// because an escaped path would name a different node than the rules grant.
func OutboxPath(mailbox, id string) (string, error) {
	if err := mailboxKey(mailbox); err != nil {
		return "", err
	}
	if err := nodeKey("a reply's id", id, IDMax); err != nil {
		return "", err
	}
	return "mailbox/" + mailbox + "/outbox/" + id, nil
}

// mailboxKey validates a mailbox name as the node key the database will treat it
// as. It is one function because two paths read it — the heartbeat's
// (lease.go's HeartbeatPath) and the outbox's (OutboxPath above) — and a rule
// about what a mailbox name may be is a rule with one implementation or two that
// drift.
//
// The bound is SenderMax, not the schema's IDMax: a mailbox is a Firebase uid
// (at most 128 characters), and it is also the `sender` of every message written
// under it, which is the string that bound is the tree's (MaxString, 200).
func mailboxKey(mailbox string) error {
	if strings.TrimSpace(mailbox) == "" {
		return errors.New("mailbox: a mailbox name is the account's key, and it is blank")
	}
	if utf16Len(mailbox) > SenderMax {
		return fmt.Errorf("mailbox: a mailbox name is at most %d characters", SenderMax)
	}
	for _, r := range mailbox {
		if forbiddenKeyChars[r] || r < 0x20 || r == 0x7f {
			return fmt.Errorf("mailbox: a mailbox name is a database key, and it may not contain %q", r)
		}
	}
	return nil
}

// nodeKey validates one database key this package builds a path from, with the
// bound the schema puts on that field (`id` is IDMax characters, and a store key
// may be no longer than the id in it).
func nodeKey(what, value string, max int) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("mailbox: %s is a database key, and it is blank", what)
	}
	if utf16Len(value) > max {
		return fmt.Errorf("mailbox: %s is at most %d characters", what, max)
	}
	for _, r := range value {
		if forbiddenKeyChars[r] || r < 0x20 || r == 0x7f {
			return fmt.Errorf("mailbox: %s is a database key, and it may not contain %q", what, r)
		}
	}
	return nil
}
