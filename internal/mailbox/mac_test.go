package mailbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/mailbox/keychain"
)

// The tag function T1 left to T2, held to the same file T1 is held to.
//
// T1's replay proves Go's canonical bytes ARE the module's, by recomputing each
// vector's tag over them with crypto/hmac inside the test. This file proves the
// half T2 owns — that the package's own MacOf and VerifyMAC are the module's
// mac.js, over the same bytes — by replaying every case and every direction
// pair of vectors.json through them. The two are independent on purpose: a
// shared helper would make the second replay a restatement of the first.

// vectorKey is the file's own key: "a readable placeholder, not a credential"
// (its key_note). Nothing in this repository signs with a real key, and no test
// here touches a real keychain (M5).
func vectorKey(t *testing.T) string {
	t.Helper()
	return text(t, loadVectors(t)["key"], "key")
}

// otherKey is the wrong key a wrongly-signed message is signed with: a second
// placeholder, so the tag is a real HMAC over real bytes and the only thing
// wrong with it is whose key it is.
const otherKey = "another-not-a-secret-test-key-0002"

// TestTheVectorsReplayThroughTheMACFunction is M9's binding for the tag: every
// case's tag is what MacOf produces over the case's own bytes and direction,
// and every case's verdict is what VerifyMAC answers. The refused cases are the
// interesting half — a tampered body, swapped options, a renamed agent, a
// swapped target, a tag read in the wrong direction — and they are the reason
// the file exists: they are the forgeries the gate must refuse.
func TestTheVectorsReplayThroughTheMACFunction(t *testing.T) {
	vectors := loadVectors(t)
	key := []byte(vectorKey(t))
	cases := list(t, vectors["cases"], "cases")
	if len(cases) != 24 {
		t.Errorf("the vector file carries %d cases: a case is a tag to verify, not to skip", len(cases))
	}
	verified, refused := 0, 0
	for _, entry := range cases {
		vector := object(t, entry, "case")
		name := text(t, vector["name"], "name")
		t.Run(name, func(t *testing.T) {
			direction := text(t, vector["direction"], "direction")
			record := Record(object(t, vector["message"], "message"))
			want := text(t, vector["mac"], "mac")
			got, err := MacOf(record, key, direction)
			if err != nil {
				t.Fatalf("MacOf(%s): %v", direction, err)
			}
			if got != want {
				t.Errorf("the tag over the %s bytes is %s, the vector file pins %s", direction, got, want)
			}
			ok, err := VerifyMAC(record, key, direction)
			if err != nil {
				t.Fatalf("VerifyMAC(%s): %v", direction, err)
			}
			switch verdict := text(t, vector["verdict"], "verdict"); verdict {
			case "verified":
				verified++
				if !ok {
					t.Errorf("the file says this record's tag is its own, and VerifyMAC refused it")
				}
			case "refused":
				refused++
				if ok {
					t.Errorf("the file says this record's tag is NOT its own, and VerifyMAC accepted it")
				}
			default:
				t.Fatalf("the vector file's verdict is %q, which is neither verified nor refused", verdict)
			}
		})
	}
	// A file that stopped carrying both verdicts would make the loop above pass
	// while proving one half of it.
	if verified == 0 || refused == 0 {
		t.Errorf("the replay verified %d and refused %d cases: both halves are the contract", verified, refused)
	}
}

// TestTheVectorsDirectionPairsReplay is the other half of the same file: two
// records with the same fields and different directions, whose tags differ. A
// tag made for one node does not verify in another, and the module's own pairs
// are where that is written down.
func TestTheVectorsDirectionPairsReplay(t *testing.T) {
	key := []byte(vectorKey(t))
	pairs := list(t, loadVectors(t)["direction_pairs"], "direction_pairs")
	if len(pairs) != 3 {
		t.Errorf("the vector file carries %d direction pairs, want 3", len(pairs))
	}
	for _, entry := range pairs {
		pair := object(t, entry, "pair")
		t.Run(text(t, pair["name"], "name"), func(t *testing.T) {
			a, b := object(t, pair["a"], "a"), object(t, pair["b"], "b")
			for _, side := range []struct {
				what string
				v    map[string]any
			}{{"a", a}, {"b", b}} {
				direction := text(t, side.v["direction"], "direction")
				record := Record(object(t, side.v["message"], "message"))
				got, err := MacOf(record, key, direction)
				if err != nil {
					t.Fatalf("MacOf(%s): %v", direction, err)
				}
				if want := text(t, side.v["mac"], "mac"); got != want {
					t.Errorf("side %s: the tag for %s is %s, the vector file pins %s", side.what, direction, got, want)
				}
				// The pair's records carry a tag that is not their own (all
				// f's): the file pins the tag its bytes produce, and the
				// records are the pair the two tags belong to.
				if ok, err := VerifyMAC(record, key, direction); err != nil || ok {
					t.Errorf("side %s: VerifyMAC(%s) = %v, %v, want false: the record does not carry its own tag", side.what, direction, ok, err)
				}
			}
			// And the two tags are two tags: one direction's tag is not the
			// other's, which is the whole point of covering the direction.
			if text(t, a["mac"], "mac") == text(t, b["mac"], "mac") {
				t.Errorf("the pair's two directions produce one tag, so the direction is not covered")
			}
		})
	}
}

// TestTheCoveredFieldSetIsEveryFieldButMacAndAcked pins the covered set the way
// a field added or dropped would be caught: every field of the schema changes
// the tag when its value changes, except the two that are deliberately outside
// it. `mac` is out because a tag cannot cover itself; `acked` is out because the
// phone writes the receipt after the tag was made, so a receipt cannot
// invalidate the message it acknowledges.
//
// The field set is read from Fields — the schema's own list, which
// TestBoundsMatchTheSharedModule holds to the module — so a field the schema
// grows is a field this test demands a decision about.
func TestTheCoveredFieldSetIsEveryFieldButMacAndAcked(t *testing.T) {
	key := []byte(vectorKey(t))
	// Four records between them carrying every field of the schema: the plain
	// message, the prompt with its options, the answer with its reply_to, and
	// the merge action with its nested record. (A heartbeat is a different
	// record — ValidateHeartbeat's, with its own field set — and its tag is
	// the same function over the same canonical form, which the vectors'
	// heartbeat cases cover.)
	records := map[string]Record{
		"the plain message":        plainMessage(t),
		"the prompt with options":  vectorCase(t, "prompt with options"),
		"the answer with reply_to": vectorCase(t, "answer"),
		"the merge action":         vectorCase(t, "action: merge a pull request"),
	}
	seen := map[string]bool{}
	for what, record := range records {
		base, err := MacOf(record, key, "inbox")
		if err != nil {
			t.Fatalf("MacOf(%s): %v", what, err)
		}
		for _, field := range sortedKeys(record) {
			seen[field] = true
			clone := cloneRecord(t, record)
			clone[field] = changedValue(clone[field])
			got, err := MacOf(clone, key, "inbox")
			if err != nil {
				t.Fatalf("MacOf(%s, %s changed): %v", what, field, err)
			}
			covered := field != "mac" && field != "acked"
			switch {
			case covered && got == base:
				t.Errorf("changing %s in %s left the tag alone, so the field is not covered", field, what)
			case !covered && got != base:
				t.Errorf("changing %s in %s changed the tag, so the field is covered", field, what)
			}
		}
	}
	// Every field of the schema is exercised above: a field this test never
	// changes a value of is a field whose coverage nobody checked.
	for _, field := range Fields {
		if !seen[field] {
			t.Errorf("no record in this test carries %s, so whether it is covered is unchecked", field)
		}
	}
	if len(seen) != len(Fields) {
		t.Errorf("the test's records carry %d fields and the schema has %d: a field the schema has and no record here carries is a field nobody checked", len(seen), len(Fields))
	}
}

// TestUnverifiableIsRefused is T2's headline correctness test, and the one the
// plan's red step names: the inbound gate refuses an unsigned, wrongly-signed,
// tampered, wrong-direction or future-stamped message, and hands none of them to
// the ledger. The refusal carries a code of its own, and the message is nil —
// which is the mechanism, not a convention: there is no value for a caller to
// pass on.
func TestUnverifiableIsRefused(t *testing.T) {
	key := []byte(vectorKey(t))
	now := time.UnixMilli(1700000000000)
	gate := NewGate(keychain.NewKey(key), GateOptions{Now: func() time.Time { return now }})

	// honest is a fresh copy of the vector file's plain message with a
	// timestamp this clock is level with: every row below signs its own copy,
	// so no row can reach another's record.
	honest := func() Record { return withAt(plainMessage(t), now.UnixMilli()) }

	cases := []struct {
		name    string
		record  Record
		code    Code // "" means the gate accepts it
		field   string
		because string
	}{
		{
			name:    "unsigned",
			record:  vectorRefusal(t, "unsigned message"),
			code:    CodeMissingField,
			field:   "mac",
			because: "the file's own unsigned message: a tag is required on every message (§3.5)",
		},
		{
			name:    "wrongly signed",
			record:  signed(t, honest(), otherKey, "inbox"),
			code:    CodeBadMAC,
			field:   "mac",
			because: "the tag is a real one, made with a key that is not the pairing's",
		},
		{
			name:    "tampered",
			record:  vectorCase(t, "text swapped"),
			code:    CodeBadMAC,
			field:   "mac",
			because: "the file's own forgery: a body under a tag made for another body",
		},
		{
			name:    "wrong direction",
			record:  signed(t, honest(), string(key), "outbox"),
			code:    CodeBadMAC,
			field:   "mac",
			because: "the tag was made for the outbox and the message was read from the inbox",
		},
		{
			name:    "future-stamped",
			record:  signed(t, withAt(honest(), now.UnixMilli()+FutureTolerance.Milliseconds()+1), string(key), "inbox"),
			code:    CodeFutureTimestamp,
			field:   "at",
			because: "one millisecond beyond the tolerance is beyond the tolerance",
		},
		{
			name:    "a record that is not an object",
			record:  nil,
			code:    CodeNotAnObject,
			because: "the gate is handed what a node held, and a node can hold anything",
		},
		{
			name:    "a correctly signed message",
			record:  signed(t, honest(), string(key), "inbox"),
			because: "the honest message of §3.1, signed with the pairing's key and read from the inbox",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			message, refusal := gate.Verify(c.record)
			if c.code == "" {
				if refusal != nil {
					t.Fatalf("the gate refused a message it must accept: %s", refusal)
				}
				if message == nil {
					t.Fatal("the gate accepted a message and answered no message")
				}
				if message.MAC != text(t, c.record["mac"], "mac") {
					t.Errorf("the message the gate answered carries another tag: %q", message.MAC)
				}
				return
			}
			if refusal == nil {
				t.Fatalf("the gate accepted this message (%s): a message that does not verify must never be acted on", c.because)
			}
			if refusal.Code != c.code || refusal.Field != c.field {
				t.Errorf("refused %s, want %s: %s", refusal, (&Refusal{Code: c.code, Field: c.field}), c.because)
			}
			// The mechanism the plan's "never hands it to the ledger" names:
			// there is no message to hand.
			if message != nil {
				t.Errorf("the gate answered a message AND a refusal: %v", message)
			}
		})
	}
}

// TestARefusedMessageIsRefusedAgainOnTheNextPass is the cost M4a and §7 state,
// asserted rather than described: a message the schema accepts and the tag
// rejects takes no ledger slot, so it is still in the inbox on the next pass and
// is refused again, with the same code, for as long as the node exists — and
// nothing this gate can do retires it, because it writes nothing back.
//
// The alternative the plan rejected is the one this test rules out: a gate that
// acked, tombstoned or dropped what it could not attribute would make one
// unverifiable message cost one pass instead of one pass each, and would do it
// by destroying the only evidence the operator has.
func TestARefusedMessageIsRefusedAgainOnTheNextPass(t *testing.T) {
	gate := NewGate(keychain.NewKey([]byte(vectorKey(t))), GateOptions{})
	record := vectorCase(t, "text swapped")
	before := cloneRecord(t, record)

	var first *Refusal
	for pass := 1; pass <= 3; pass++ {
		message, refusal := gate.Verify(record)
		if message != nil {
			t.Fatalf("pass %d: the gate answered a message for a record it must refuse", pass)
		}
		if refusal == nil {
			t.Fatalf("pass %d: the gate accepted a tampered message", pass)
		}
		if refusal.Code != CodeBadMAC {
			t.Errorf("pass %d: refused %s, want %s", pass, refusal, CodeBadMAC)
		}
		if first == nil {
			first = refusal
		} else if *refusal != *first {
			t.Errorf("pass %d refused %s, the first pass refused %s: the same message must get the same answer", pass, refusal, first)
		}
	}
	if !reflect.DeepEqual(record, before) {
		t.Errorf("the gate wrote to the record it refused:\n now %v\n was %v", record, before)
	}
}

// TestTheGateNeverPrintsTheKey is the other half of the plan's red step, and
// M5's rule for the one value that lives in this process: the key appears in no
// output, no error string, no log line and no JSON. The gate holds a
// keychain.Key, which redacts itself in every shape a printer can ask for — this
// test is what keeps that true if the field ever changes to a []byte.
func TestTheGateNeverPrintsTheKey(t *testing.T) {
	material := vectorKey(t)
	gate := NewGate(keychain.NewKey([]byte(material)), GateOptions{})
	doc, err := json.Marshal(gate)
	if err != nil {
		t.Fatalf("json.Marshal(gate): %v", err)
	}
	// A pass's own output: the message it acted on, and the refusal it
	// recorded. Both are what a log line or a `--json` document carries.
	message, refusal := gate.Verify(vectorCase(t, "text swapped"))
	if message != nil || refusal == nil {
		t.Fatalf("the tampered vector was not refused: %v, %v", message, refusal)
	}
	accepted, refused := gate.Verify(plainMessage(t))
	if accepted == nil || refused != nil {
		t.Fatalf("the plain vector was refused: %v", refused)
	}
	printings := map[string]string{
		"the gate, %v":          fmt.Sprintf("%v", gate),
		"the gate, %+v":         fmt.Sprintf("%+v", gate),
		"the gate, %#v":         fmt.Sprintf("%#v", gate),
		"the gate, %s":          fmt.Sprintf("%s", gate),
		"the gate's JSON":       string(doc),
		"the refusal":           refusal.Error(),
		"the refusal, %v":       fmt.Sprintf("%v", refusal),
		"the message":           fmt.Sprintf("%v", accepted),
		"the message, %#v":      fmt.Sprintf("%#v", accepted),
		"a log line about them": fmt.Sprintf("pass 1: refused %s, acted on %v", refusal, accepted),
		"the keychain's Key":    keychain.NewKey([]byte(material)).String(),
	}
	for what, got := range printings {
		if strings.Contains(got, material) {
			t.Errorf("%s carries the key: %s", what, got)
		}
	}
}

// TestTheFutureToleranceBoundary is §8.1.4's edge, one millisecond wide: the
// tolerance itself is inside and one millisecond beyond it is not. It is the
// module's own comparison (`at - now > futureToleranceMs`), and it matters
// because the bound is what keeps a future `at` from being an entry no window
// can evict.
func TestTheFutureToleranceBoundary(t *testing.T) {
	key := []byte(vectorKey(t))
	now := time.UnixMilli(1700000000000)
	gate := NewGate(keychain.NewKey(key), GateOptions{Now: func() time.Time { return now }})
	for _, c := range []struct {
		name   string
		ahead  time.Duration
		refuse bool
	}{
		{name: "the tolerance exactly", ahead: FutureTolerance},
		{name: "one millisecond beyond", ahead: FutureTolerance + time.Millisecond, refuse: true},
		{name: "an hour beyond", ahead: time.Hour, refuse: true},
		{name: "a second behind", ahead: -time.Second},
	} {
		t.Run(c.name, func(t *testing.T) {
			record := signed(t, withAt(plainMessage(t), now.Add(c.ahead).UnixMilli()), string(key), "inbox")
			message, refusal := gate.Verify(record)
			if c.refuse != (refusal != nil) {
				t.Fatalf("Verify = %v, %v, want refused=%v", message, refusal, c.refuse)
			}
			if c.refuse && refusal.Code != CodeFutureTimestamp {
				t.Errorf("refused %s, want %s", refusal, CodeFutureTimestamp)
			}
		})
	}
	// The gate's own default is the module's, and a gate built with a tolerance
	// of its own uses it: the two are the same comparison, one clock.
	strict := NewGate(keychain.NewKey(key), GateOptions{
		Now:             func() time.Time { return now },
		FutureTolerance: time.Second,
	})
	ahead := signed(t, withAt(plainMessage(t), now.UnixMilli()+1001), string(key), "inbox")
	if _, refusal := strict.Verify(ahead); refusal == nil || refusal.Code != CodeFutureTimestamp {
		t.Errorf("a gate with a one-second tolerance accepted a message 1001 ms ahead: %v", refusal)
	}
}

// TestFutureToleranceMatchesTheModules is M9's binding for the two numbers this
// file declares and cannot import: `shared/mailbox`'s
// DEFAULT_FUTURE_TOLERANCE_MS, which is `shared/lease`'s MAX_CLOCK_SKEW, and
// the refusal code the delivery refuses a too-future `at` with. Both are read
// back out of the modules — following the import the way a reader would — so a
// change on either side is a failing test rather than a tolerance the phone and
// the consumer disagree about.
func TestFutureToleranceMatchesTheModules(t *testing.T) {
	mailbox, err := os.ReadFile(filepath.Join("..", "..", "shared", "mailbox", "index.js"))
	if err != nil {
		t.Fatalf("reading shared/mailbox: %v", err)
	}
	if !jsFutureToleranceDefault.Match(mailbox) {
		t.Error("shared/mailbox no longer sets DEFAULT_FUTURE_TOLERANCE_MS to MAX_CLOCK_SKEW: this package's tolerance has nothing to be held to")
	}
	if m := jsFutureTimestamp.FindSubmatch(mailbox); m == nil {
		t.Error("shared/mailbox no longer declares REFUSAL.FUTURE_TIMESTAMP: this package's code has nothing to be held to")
	} else if string(m[1]) != string(CodeFutureTimestamp) {
		t.Errorf("the module refuses a too-future `at` as %q, this package as %q", m[1], CodeFutureTimestamp)
	}
	lease, err := os.ReadFile(filepath.Join("..", "..", "shared", "lease", "index.js"))
	if err != nil {
		t.Fatalf("reading shared/lease: %v", err)
	}
	m := jsClockSkew.FindSubmatch(lease)
	if m == nil {
		t.Fatal("shared/lease no longer declares MAX_CLOCK_SKEW: this package's tolerance has nothing to be held to")
	}
	ms := int64(1)
	for _, factor := range m[1:] {
		n, err := strconv.ParseInt(string(factor), 10, 64)
		if err != nil {
			t.Fatalf("reading MAX_CLOCK_SKEW from the module: %v", err)
		}
		ms *= n
	}
	if got := FutureTolerance.Milliseconds(); got != ms {
		t.Errorf("FutureTolerance is %d ms, the modules' MAX_CLOCK_SKEW is %d ms", got, ms)
	}
}

var (
	jsFutureToleranceDefault = regexp.MustCompile(`(?m)^export const DEFAULT_FUTURE_TOLERANCE_MS = MAX_CLOCK_SKEW;$`)
	jsFutureTimestamp        = regexp.MustCompile(`(?m)^\s*FUTURE_TIMESTAMP: '([^']*)',$`)
	jsClockSkew              = regexp.MustCompile(`(?m)^export const MAX_CLOCK_SKEW = (\d+) \* (\d+) \* (\d+);$`)
)

// TestMacOfRefusesAnEmptyKey: HMAC with no key is a tag anyone can compute, so
// an empty key is an error rather than a tag. A gate built with no key is
// therefore a gate that refuses everything — fail closed, which is the only
// direction a security check may fail in.
func TestMacOfRefusesAnEmptyKey(t *testing.T) {
	record := plainMessage(t)
	for _, key := range [][]byte{nil, {}} {
		tag, err := MacOf(record, key, "inbox")
		if err == nil {
			t.Errorf("MacOf with a %d-byte key answered %q", len(key), tag)
		}
		if tag != "" {
			t.Errorf("MacOf with a %d-byte key answered a tag as well as an error: %q", len(key), tag)
		}
		if ok, err := VerifyMAC(record, key, "inbox"); ok || err == nil {
			t.Errorf("VerifyMAC with a %d-byte key = %v, %v, want false and an error", len(key), ok, err)
		}
	}
	// A gate with no key, and a nil gate: both refuse every message, and
	// neither panics in a loop that must not stop (§8.1.5).
	empty := NewGate(keychain.Key{}, GateOptions{})
	var nilGate *Gate
	for what, gate := range map[string]*Gate{"no key": empty, "nil gate": nilGate} {
		message, refusal := gate.Verify(record)
		if message != nil || refusal == nil {
			t.Errorf("%s: Verify = %v, %v, want a refusal and no message", what, message, refusal)
		}
	}
}

// TestVerifyMACRefusesWhatCannotCarryATag: mac.js's own list — a value that is
// not a record, a `mac` that is not a string, a tag of the wrong length — and
// the one thing this side answers differently: a record the canonical
// serialisation refuses is an error rather than a false, because no tag could
// have been computed over those bytes at all.
func TestVerifyMACRefusesWhatCannotCarryATag(t *testing.T) {
	key := []byte(vectorKey(t))
	cases := []struct {
		name   string
		record Record
		error  bool
	}{
		{name: "no mac at all", record: vectorRefusal(t, "unsigned message")},
		{name: "a mac that is not a string", record: Record{"id": "m-0001", "sender": "phone", "text": "x", "at": number("1700000000000"), "mac": number("1")}},
		{name: "a short tag", record: Record{"id": "m-0001", "sender": "phone", "text": "x", "at": number("1700000000000"), "mac": "abcd"}},
		{name: "an empty tag", record: Record{"id": "m-0001", "sender": "phone", "text": "x", "at": number("1700000000000"), "mac": ""}},
		{
			name:   "a value canonicalBytes refuses",
			record: Record{"id": "m-0001", "sender": "phone", "text": "x", "at": number("1700000000000"), "mac": strings.Repeat("f", 64), "landed_at": nil},
			error:  true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, err := VerifyMAC(c.record, key, "inbox")
			if ok {
				t.Errorf("VerifyMAC accepted %v", c.record)
			}
			if c.error != (err != nil) {
				t.Errorf("VerifyMAC = %v, %v, want an error=%v", ok, err, c.error)
			}
		})
	}
}

// signed is a record with the tag its own bytes produce under key in direction:
// the honest message, and the forgery, are the same record with a different tag.
func signed(t *testing.T, record Record, key, direction string) Record {
	t.Helper()
	tag, err := MacOf(record, []byte(key), direction)
	if err != nil {
		t.Fatalf("MacOf(%s): %v", direction, err)
	}
	record["mac"] = tag
	return record
}

// withAt is a copy of record with `at` replaced, so a test can move a message
// through time without touching the record another test holds.
func withAt(record Record, at int64) Record {
	out := make(Record, len(record))
	for k, v := range record {
		out[k] = v
	}
	out["at"] = number(strconv.FormatInt(at, 10))
	return out
}

// cloneRecord is a deep copy through the package's own decoder: the copy shares
// no map and no list with the original, so a test that mutates one cannot reach
// the other.
func cloneRecord(t *testing.T, record Record) Record {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	clone, err := Decode(data)
	if err != nil {
		t.Fatalf("decoding the copy of %v: %v", record, err)
	}
	return clone
}

// changedValue is a different value of the same JSON type, for the covered-set
// test: a string grows, a number grows a digit, a boolean flips, a list gains
// an entry, and an object has the value under its first key (in byte order, so
// the change is deterministic) changed in turn.
func changedValue(value any) any {
	switch v := value.(type) {
	case string:
		return v + "-changed"
	case bool:
		return !v
	case json.Number:
		return json.Number(v.String() + "1")
	case []any:
		return append(append([]any{}, v...), "changed")
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			out[k] = e
		}
		for _, k := range sortedKeys(out) {
			out[k] = changedValue(out[k])
			break
		}
		return out
	}
	return "changed"
}

// vectorRefusal is one entry of the vector file's `refusals` list: the file's
// own example of a value it refuses, as the record it holds.
func vectorRefusal(t *testing.T, name string) Record {
	t.Helper()
	for _, entry := range list(t, loadVectors(t)["refusals"], "refusals") {
		refusal := object(t, entry, "refusal")
		if text(t, refusal["name"], "name") == name {
			return Record(object(t, refusal["value"], "value"))
		}
	}
	t.Fatalf("the vector file carries no %q refusal", name)
	return nil
}
