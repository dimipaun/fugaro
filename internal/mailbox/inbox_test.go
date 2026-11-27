package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// The inbox reader both consumers read through (internal/cli's phase-1 pass and
// internal/orchestrator's resident one, P5.1f).
//
// What is pinned here is the contract they share and neither of them owns: an
// absent node is not an empty inbox, every child is decoded strictly and an
// unreadable one is handed back as a refusal rather than as an error, the order
// is the records' own `at` and then the node key, and the id an answer is
// written under is derived from the message's. Nothing here touches a database:
// the store is a map, which is all the reader asks of one.

// inboxStore is the one read the reader makes: a path and the JSON under it. A
// path that is not there is not an error (found false), and err is the read's
// own failure rather than a missing node.
type inboxStore struct {
	nodes map[string]string
	err   error
	reads []string
}

func (s *inboxStore) Get(_ context.Context, path string, out any) (bool, error) {
	s.reads = append(s.reads, path)
	if s.err != nil {
		return false, s.err
	}
	raw, ok := s.nodes[path]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal([]byte(raw), out)
}

// inboxPath is the node the reader is expected to read, built here from the
// mailbox the tests pass rather than written out, so a test that changed the
// mailbox would move with it.
func inboxPath(t *testing.T, mailbox string) string {
	t.Helper()
	path, err := InboxPath(mailbox)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

const inboxTestMailbox = "m~AAAAAAAAAAAAAAAAAAAAAA"

// TestReadInboxDecodesOrdersAndRefuses is the reader's whole surface in one
// case: three children, one of them not an object and one of them a JSON value
// the strict decoder refuses, and the two readable ones ordered by their own
// `at` rather than by their node keys — which is the phase-1 pass' order, the
// one an offer and a prompt expiry race each other on.
func TestReadInboxDecodesOrdersAndRefuses(t *testing.T) {
	path := inboxPath(t, inboxTestMailbox)
	store := &inboxStore{nodes: map[string]string{path: `{
		"msg-b": {"id": "msg-b", "text": "second", "at": 2000},
		"msg-a": {"id": "msg-a", "text": "first", "at": 1000},
		"not-an-object": [1, 2, 3],
		"unreadable": {"id": "twice", "id": "twice"}
	}`}}

	items, err := ReadInbox(context.Background(), store, inboxTestMailbox)
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	if len(items) != 4 {
		t.Fatalf("the reader answered %d items, and the node holds four children: %v", len(items), items)
	}
	// The order: the two readable records oldest-first, then the two refusals,
	// whose `at` is zero because there is no record to read one from — and zero
	// sorts first, which is what keeps an unreadable child from displacing an
	// older delivery. Within one `at` the node key decides.
	var order []string
	for _, item := range items {
		order = append(order, item.ID)
	}
	want := []string{"not-an-object", "unreadable", "msg-a", "msg-b"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("the reader answered %v, want %v: the order is the records' own `at` and then the node key, and the node's ids decide nothing", order, want)
	}
	// The two ids a record has are not compared: the node key is what the record
	// was addressed by, and the reader hands back what was written.
	if items[2].Record["text"] != "first" || items[3].Record["text"] != "second" {
		t.Errorf("the readable items are %v and %v, and msg-a says first with msg-b second", items[2].Record, items[3].Record)
	}
	if items[2].At != 1000 || items[3].At != 2000 {
		t.Errorf("the readable items carry at %d and %d, and the records say 1000 and 2000", items[2].At, items[3].At)
	}
	// A child that is not an object is the schema's own code, and a child the
	// decoder never got as far as is this reader's — the two the pass reports.
	if got := items[0].Refusal; got == nil || got.Code != CodeNotAnObject {
		t.Errorf("the child that is not an object is refused with %v, and a value that decodes but is not an object is the module's own %q", got, CodeNotAnObject)
	}
	if got := items[1].Refusal; got == nil || got.Code != CodeUnreadableRecord {
		t.Errorf("the child the strict decoder cannot read is refused with %v, and this reader's own code for one is %q", got, CodeUnreadableRecord)
	}
	for _, item := range items {
		if (item.Record == nil) == (item.Refusal == nil) {
			t.Errorf("item %s carries record %v and refusal %v, and exactly one of them is set", item.ID, item.Record, item.Refusal)
		}
	}
}

// TestReadInboxOfAnAbsentNodeIsNoItemsAndNoError is the distinction the pass'
// own report turns on: a phone that has never spoken has no inbox, and the
// reader answers no items rather than an error — the absent node is not an
// empty one, and neither is a failure to read.
func TestReadInboxOfAnAbsentNodeIsNoItemsAndNoError(t *testing.T) {
	store := &inboxStore{nodes: map[string]string{}}
	items, err := ReadInbox(context.Background(), store, inboxTestMailbox)
	if err != nil || len(items) != 0 {
		t.Fatalf("an absent inbox read %v, %v, and a phone that never spoke is no items and no error", items, err)
	}
	if len(store.reads) != 1 || store.reads[0] != inboxPath(t, inboxTestMailbox) {
		t.Errorf("the reader read %v, and the one read it makes is the inbox node", store.reads)
	}
}

// TestReadInboxHandsBackAReadFailure is the other half: a read that FAILED is an
// error and never an empty inbox. A pass that could not tell those apart would
// report a database it cannot reach as a phone with nothing to say — and for the
// resident consumer that is also the difference between "no message" and a read
// the account was refused.
func TestReadInboxHandsBackAReadFailure(t *testing.T) {
	want := errors.New("permission denied")
	store := &inboxStore{err: want}
	items, err := ReadInbox(context.Background(), store, inboxTestMailbox)
	if !errors.Is(err, want) {
		t.Fatalf("ReadInbox answered %v, and a failed read is that failure rather than an empty inbox", err)
	}
	if len(items) != 0 {
		t.Errorf("a failed read answered %v, and it answers no items at all", items)
	}
}

// TestReadInboxRefusesAMailboxItCannotBuildAPathFrom is the fence the reader
// inherits from the path builder: a name the database will not treat as one key
// — blank, over the bound, or carrying a character the rules do not put in a key
// (a separator above all, which would name a node outside the mailbox subtree)
// — is refused before any read. What is NOT refused is a name that is merely not
// a device uid: this is a database key rule, and a caller that means a phone
// holds it to the pairing list rather than to a path (paste's and the pairing
// command's business, and the readers that check it are the rules and the uid
// check on the claim).
func TestReadInboxRefusesAMailboxItCannotBuildAPathFrom(t *testing.T) {
	store := &inboxStore{nodes: map[string]string{}}
	over := strings.Repeat("m", SenderMax+1)
	for _, name := range []string{"", "   ", "../other", "mail box\x00", over} {
		items, err := ReadInbox(context.Background(), store, name)
		if err == nil {
			t.Errorf("mailbox %q was read (%d items), and a name this package will not build a path from is no read at all", name, len(items))
		}
	}
	if len(store.reads) != 0 {
		t.Errorf("the reader read %v, and a name it cannot build a path from reaches the store in no way", store.reads)
	}
}

// TestReadInboxReadsTheNodeItIsToldTo pins the path itself: the inbox of the
// mailbox the caller named, and nothing beside it — the reader is handed one
// mailbox per pass, and a reader that read another's would be the fence between
// two phones gone.
func TestReadInboxReadsTheNodeItIsToldTo(t *testing.T) {
	const other = "m~BBBBBBBBBBBBBBBBBBBBBB"
	store := &inboxStore{nodes: map[string]string{
		inboxPath(t, other): `{"msg-1": {"id": "msg-1", "at": 1}}`,
	}}
	items, err := ReadInbox(context.Background(), store, inboxTestMailbox)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Errorf("the reader answered %v, and this mailbox's inbox holds nothing", items)
	}
	if len(store.reads) != 1 || store.reads[0] != inboxPath(t, inboxTestMailbox) {
		t.Errorf("the reader read %v, and the only node it may read is %s", store.reads, inboxPath(t, inboxTestMailbox))
	}
}

// TestReplyIDIsDerivedFromTheMessage is the create-only rule's whole mechanism
// in one line: two consumers answering one message write the same key, so the
// second write is a no-op and never a second answer (M10, C0). A drawn id would
// make a re-read a second answer, and the merged simulator's `replyId` is this
// same derivation.
func TestReplyIDIsDerivedFromTheMessage(t *testing.T) {
	if got := ReplyID("msg-1"); got != "r-msg-1" {
		t.Errorf("ReplyID(\"msg-1\") = %q, and the phase-1 command, the resident consumer and the merged simulator all write an answer to a message under `r-` + its id", got)
	}
	if ReplyID("msg-1") != ReplyID("msg-1") {
		t.Error("the derivation is not the same for one id twice, and an answer owed on one pass must be owed under the same key on the next")
	}
	if ReplyID("a") == ReplyID("b") {
		t.Error("two messages derive one key, and a second answer would overwrite the first")
	}
}

// TestRecordAtReadsTheAtItCanUse covers the three shapes an `at` arrives in and
// the ones it does not: the JSON number a strict decode hands back, the float a
// plain unmarshal of an object gives, an int64 a caller built, and anything else
// — including a missing field, a negative number and a non-number — is zero, so
// the record sorts first rather than displacing an older delivery.
func TestRecordAtReadsTheAtItCanUse(t *testing.T) {
	for _, tc := range []struct {
		name string
		at   any
		want int64
	}{
		{"a json number", json.Number("1700000000000"), 1700000000000},
		{"a float", float64(1700000000000), 1700000000000},
		{"an int64", int64(1700000000000), 1700000000000},
		{"a string", "1700000000000", 0},
		{"zero", json.Number("0"), 0},
		{"negative", json.Number("-5"), 0},
		{"nothing at all", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := Record{}
			if tc.at != nil {
				record["at"] = tc.at
			}
			if got := RecordAt(record); got != tc.want {
				t.Errorf("RecordAt(%v) = %d, want %d", tc.at, got, tc.want)
			}
		})
	}
}
