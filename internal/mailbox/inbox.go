// The inbox read (mobile P4.1's reading half, P5.1f's): the one reader of
// `mailbox/<mailbox>/inbox`, its ordering, and the two ids an answer is written
// under.
//
// It lives in the module rather than beside either caller because there are two
// consumers of one mailbox and they are one pass in two processes: the phase-1
// consumer of `fugaro mailbox` (internal/cli/mailbox.go), which runs on the
// owner's laptop, and the resident orchestrator's (internal/orchestrator's
// Consume, P5.1f), which runs in the installation's own project. A second reader
// written beside the second consumer would be a second answer to "which children
// does this mailbox hold, in which order, and which of them cannot be read at
// all" — and the two would drift on the questions the protocol turns on: whether
// an absent node is an empty one (it is not), what a child that is not an object
// is refused with (the schema's own not-an-object), and what a child the strict
// decoder cannot read at all is refused with (this reader's own code below).
//
// What the reader does NOT do is decide anything about a record: it decodes,
// orders and hands back, and the caller's gate is what verifies the tag and
// refuses by the schema. The phase-1 consumer then lets its ledger decide and
// acts; the resident one carries nothing (its acting half is P5.1g's). That
// difference is the callers', and it is why this file is a reader rather than a
// pass.

package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
)

// CodeUnreadableRecord is the code the reader answers for a child its strict
// decoder cannot read at all — a duplicated key, a lone surrogate, two JSON
// values in one object. It is this reader's own code and not a protocol one: the
// schema's vocabulary is shared/mailbox-schema's, and a record the reader never
// got as far as is a fact about the store rather than about the message. A value
// that decodes but is not an object is refused with the module's own
// not-an-object, which is what that code means.
const CodeUnreadableRecord Code = "unreadable-record"

// InboxStore is the one read a consumer makes of the database for this: a plain
// get of the inbox node, with no etag and no claim — the inbox belongs to the
// phone that writes it, and the consumer's fence is the lease it holds on the
// heartbeat rather than a precondition on this read.
type InboxStore interface {
	Get(ctx context.Context, path string, out any) (found bool, err error)
}

// InboxItem is one child of an inbox: the node key it was written under, the
// record the phone wrote, or the refusal the child is instead when it cannot be
// read as a record at all, and the record's own `at` for ordering.
//
// Record is nil exactly when Refusal is not, and a caller that reads an item
// without checking first is a caller asking the gate about no record.
type InboxItem struct {
	ID      string
	Record  Record
	Refusal *Refusal
	At      int64
}

// ReplyID is the outbox key one message's answer is written under: derived from
// the message's own id rather than drawn, so that an answer owed and not written
// is owed under the same id on the next pass, and the outbox's create-only rule
// makes the second write of that id a no-op rather than a second answer (M10,
// C0). It is the one spelling both consumers use, and the merged simulator's
// `replyId` is the same derivation.
func ReplyID(messageID string) string { return "r-" + messageID }

// RecordAt is the record's own `at` for ordering, and zero when it has none a
// reader can use: a message without one is refused by the gate a moment later,
// and ordering it first is what keeps it from displacing an older delivery.
func RecordAt(record Record) int64 {
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

// ReadInbox reads one phone's inbox — `mailbox/<mailbox>/inbox` — every child
// decoded strictly and ordered oldest-first.
//
// An absent node is not an empty one and both come back as no items and no
// error: a phone that has never spoken has no inbox to read, and found is false
// for it either way (the distinction the phase-1 consumer's own report makes).
//
// The order is the records' own `at` and then the node key, and it is the
// phase-1 pass' order rather than an arbitrary one: there an offer and a prompt
// expiry race each other, so the oldest delivery is the one that must be read
// first. The resident consumer depends on no sequence (it answers each record on
// its own), and it reads through this order anyway, because two consumers of one
// mailbox whose orders differed would be two passes over the same node.
//
// A child that cannot be read is NOT an error and not a failure of the read: it
// is one item whose Refusal says why, so that a pass reports it, counts it and
// goes on to the rest of the inbox — a record nobody can read must not stop the
// records that can.
func ReadInbox(ctx context.Context, store InboxStore, mailbox string) ([]InboxItem, error) {
	path, err := InboxPath(mailbox)
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	found, err := store.Get(ctx, path, &raw)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	items := make([]InboxItem, 0, len(raw))
	for id, data := range raw {
		item := InboxItem{ID: id}
		record, err := Decode(data)
		switch {
		case err == nil:
			item.Record = record
			item.At = RecordAt(record)
		case errors.Is(err, ErrNotAnObject):
			item.Refusal = &Refusal{Code: CodeNotAnObject}
		default:
			item.Refusal = &Refusal{Code: CodeUnreadableRecord}
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].At != items[j].At {
			return items[i].At < items[j].At
		}
		return items[i].ID < items[j].ID
	})
	return items, nil
}
