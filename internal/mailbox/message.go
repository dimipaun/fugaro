// Package mailbox holds the consumer side of the phone's mailbox: the Go
// binding of the contracts both ends of the wire already agree on.
//
// This is task T1 of docs/plans/2026-10-18-mobile-mailbox-consumer.md (mobile
// P4.1) and it lands the message binding and nothing else: the strict decoder,
// the validator and the canonical serialisation, each held to the merged
// shared/mailbox-schema by its committed vectors.json. The tag over those bytes
// (internal/mailbox/mac.go) is T2's, the lease is T3's, the ledger T4's, the
// command T5's — this file deliberately holds no tag function, no key and no
// keychain, so the two halves of the protocol cannot drift into two
// implementations of the same covered field set.
//
// The one rule that shapes everything here: the bytes a tag covers are computed
// from the record AS IT ARRIVED, never from a re-marshalled struct. §3.5 covers
// every field except `mac` and `acked` — including one a later protocol version
// adds — so a decoder that keeps only the fields it knows would sign different
// bytes than the phone did. That is why a message is a Record (a decoded JSON
// object) first and a Message second.
package mailbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The bounds shared/mailbox-schema owns. They are declared here because Go
// cannot import the module, and TestBoundsMatchTheSharedModule reads them back
// out of index.js so a change to either side is a failing test rather than a
// silent divergence (M9).
const (
	// IDMax bounds an id, a reply_to and the id of an action or a
	// confirmation: all of them are database keys.
	IDMax = 100
	// SenderMax bounds the mailbox a message belongs to.
	SenderMax = 200
	// TextMax bounds a message's body, in code units.
	TextMax = 4096
	// AgentMax bounds an agent id, and a boot id, which share one alphabet.
	AgentMax = 64
	// AgentNameMax bounds the spoken name of an advertised agent.
	AgentNameMax = 64
	// AgentsMax bounds how many agents one heartbeat advertises.
	AgentsMax = 32
	// OptionsMax bounds how many options a prompt offers.
	OptionsMax = 8
	// OptionMax bounds one option's label.
	OptionMax = 200
	// TargetMax bounds a run id, and a repository.
	TargetMax = 200
	// SHAMin and SHAMax are the two object-name lengths a head SHA may have:
	// short and full, and nothing in between.
	SHAMin = 40
	SHAMax = 64
	// MACMax is the length of a tag: 32 bytes of HMAC-SHA-256, lowercase hex.
	MACMax = 64
	// BootMax bounds a heartbeat's holder id.
	BootMax = 64
)

// KindMessage is the kind a message with no `kind` field is.
const KindMessage = "message"

// Kinds, Actions and Directions are the module's closed vocabularies, in the
// module's order.
var (
	// Kinds is every kind of message the schema names.
	Kinds = []string{"message", "prompt", "answer", "action"}
	// Actions is every verb an action record may carry.
	Actions = []string{"cancel", "merge", "confirm"}
	// Directions is every node a record can be stored in. It is covered as the
	// field `direction` and it is never stored: the reader supplies it.
	Directions = []string{"inbox", "outbox", "heartbeat"}
	// Fields is the exact field set of a message; any other field is refused.
	Fields = []string{
		"id", "sender", "text", "at", "mac", "acked",
		"agent", "kind", "options", "reply_to", "action",
	}
	// RequiredFields is the fields a message must have, in the order they are
	// checked.
	RequiredFields = []string{"id", "sender", "text", "at", "mac"}
)

// directionField is the name the direction is covered under, which no stored
// record may carry.
const directionField = "direction"

// Code names one family of refusal, spelled exactly as the module's REFUSALS
// spells it. The vectors require only that the Go side refuse the value —
// "the Go consumer need not reproduce the codes' wording" — so pinning the
// spelling too is deliberately more than the contract asks for: a reworded
// refusal should be a failing test here, not a code that quietly changes
// meaning in a log line.
type Code string

// The refusal codes, one per family of refusal.
const (
	// CodeNotAnObject: the value is null, an array or a primitive.
	CodeNotAnObject Code = "not-an-object"
	// CodeUnknownField: a field the schema does not name is present.
	CodeUnknownField Code = "unknown-field"
	// CodeMissingField: a required field is absent.
	CodeMissingField Code = "missing-field"
	// CodeWrongType: a field holds a value of the wrong type, including an
	// explicit null.
	CodeWrongType Code = "wrong-type"
	// CodeEmptyString: a required string is empty.
	CodeEmptyString Code = "empty-string"
	// CodeBadKey: an id contains a character a database key may not contain.
	CodeBadKey Code = "bad-key"
	// CodeTooLong: a string is longer than its bound.
	CodeTooLong Code = "too-long"
	// CodeTooMany: a list holds more entries than its bound allows.
	CodeTooMany Code = "too-many"
	// CodeTooFew: a list that is present is empty.
	CodeTooFew Code = "too-few"
	// CodeBadCharacter: a string contains a character that cannot be spoken,
	// displayed or encoded.
	CodeBadCharacter Code = "bad-character"
	// CodeBadTimestamp: a number is not a non-negative epoch-millisecond safe
	// integer.
	CodeBadTimestamp Code = "bad-timestamp"
	// CodeUnknownKind: a kind is not one of the four.
	CodeUnknownKind Code = "unknown-kind"
	// CodeUnknownVerb: an action's verb is not one of the three.
	CodeUnknownVerb Code = "unknown-verb"
	// CodeFieldNotAllowed: a field is present on a kind that does not carry it.
	CodeFieldNotAllowed Code = "field-not-allowed"
	// CodeBadTarget: a target is not one of the two shapes its verb resolves to.
	CodeBadTarget Code = "bad-target"
	// CodeBadSHA: a head SHA is not a hex object name of either length.
	CodeBadSHA Code = "bad-sha"
	// CodeBadMAC: a mac is not 64 lowercase hex characters.
	CodeBadMAC Code = "bad-mac"
	// CodeDuplicateEntry: a list names the same id or option twice.
	CodeDuplicateEntry Code = "duplicate-entry"
)

// Codes is every code this package speaks, one per family of refusal the
// module's REFUSALS declares. It is exported because it is the list the two
// ends are held to: TestEveryRefusalCodeMatchesTheModule reads REFUSALS out of
// index.js and requires the two sets to be the same, so a code added, dropped
// or reworded on one end is a failing test rather than a protocol the two ends
// disagree about.
var Codes = []Code{
	CodeNotAnObject, CodeUnknownField, CodeMissingField, CodeWrongType,
	CodeEmptyString, CodeBadKey, CodeTooLong, CodeTooMany, CodeTooFew,
	CodeBadCharacter, CodeBadTimestamp, CodeUnknownKind, CodeUnknownVerb,
	CodeFieldNotAllowed, CodeBadTarget, CodeBadSHA, CodeBadMAC,
	CodeDuplicateEntry,
}

// Refusal is the schema's answer for a value it will not accept. Field is the
// field at fault as a dotted path (`action.target.run`) when the fault is
// inside a record, and empty when the fault is the value itself.
type Refusal struct {
	Code  Code
	Field string
}

// Error spells a refusal the way a log line or a `--json` document needs it:
// the code, and the field when there is one. It is never the message's text.
func (r *Refusal) Error() string {
	if r.Field == "" {
		return string(r.Code)
	}
	return string(r.Code) + " (" + r.Field + ")"
}

// refuse builds a refusal verdict.
func refuse(code Code, field string) *Refusal { return &Refusal{Code: code, Field: field} }

// Record is one decoded JSON object exactly as it arrived: a message or a
// heartbeat, with its keys spelled the way its writer spelled them, its numbers
// as json.Number (so no float64 rounding stands between the wire and the
// bytes), and every field it carried — including one the schema does not name.
//
// It is the wire form, and CanonicalBytes is computed over it rather than over
// a decoded struct (see the package comment). ValidateMessage and
// ValidateHeartbeat are what turn it into a checked message.
type Record map[string]any

// ErrNotAnObject is what Decode answers for a value that is not a JSON object.
var ErrNotAnObject = errors.New("mailbox: a record must be a JSON object")

// Decode reads one JSON object as a Record.
//
// It is the strict decoder §8.1 requires, and it is strict on purpose in three
// ways a plain `json.Unmarshal` into a map would not be:
//
//   - keys are kept exactly as they were written, so `Text` stays `Text` and is
//     refused by the validator as an unknown field rather than folded into
//     `text` (encoding/json's case-insensitive matching applies to structs, and
//     a struct would also drop the unknown fields the tag must cover);
//   - a key repeated in one object is refused rather than letting the last one
//     win, because two readers disagreeing about which of two values a record
//     holds is exactly the ambiguity this schema exists to refuse — and a tag
//     computed over the surviving one proves nothing about the other;
//   - a string that spells an unpaired surrogate with a `\u` escape is kept as
//     the fault it is — invalid UTF-8 — instead of being silently replaced by
//     U+FFFD, so the validator refuses the record with the code the field it is
//     in calls for and no tag is ever computed over the laundered bytes. See
//     unquoteKeepingLoneSurrogates.
//
// It also refuses a second JSON value after the first, which `json.Decoder`'s
// own `Decode` does not: there, a record followed by anything else is read as
// the record and the rest is left in the stream — the same ambiguity, one
// message over.
//
// Numbers are decoded as json.Number: the bytes are what is signed, and a
// float64 in between would round a value the phone wrote.
func Decode(data []byte) (Record, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	value, err := decodeValue(dec, data, 0)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return nil, errors.New("mailbox: a record carries more than one JSON value")
		}
		return nil, fmt.Errorf("mailbox: reading a record: %w", err)
	}
	object, ok := asObject(value)
	if !ok {
		return nil, ErrNotAnObject
	}
	return Record(object), nil
}

// maxDepth bounds how deeply Decode will walk a document: a message nests a
// record, an action and a target or a confirmation and no deeper, so anything
// past this is not one.
const maxDepth = 8

// decodeValue walks one JSON value with the decoder's own tokens, so that a
// duplicate key is seen rather than silently resolved. data is the document
// being read: a string is read back out of its own bytes when it spells an
// unpaired surrogate, because the decoder has already thrown that difference
// away by the time the token comes back (see unquoteKeepingLoneSurrogates).
func decodeValue(dec *json.Decoder, data []byte, depth int) (any, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("mailbox: a record nested deeper than %d levels", maxDepth)
	}
	start := dec.InputOffset()
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("mailbox: reading a record: %w", err)
	}
	end := dec.InputOffset()
	delim, isDelim := tok.(json.Delim)
	if !isDelim {
		// A string is the one token that may have to be read back out of its
		// own bytes; nil, true, false and a number are kept as they came.
		// Those four and a delimiter are the whole of what Token returns, so
		// there is no case for a token of any other type: the branch that used
		// to answer one here — `unexpected JSON token %v` — was a refusal no
		// document could reach, and dead code in a decoder is a branch a
		// reader has to decide is dead before trusting the ones beside it.
		if _, isString := tok.(string); isString && hasLoneSurrogateEscape(data[start:end]) {
			return unquoteKeepingLoneSurrogates(data[start:end])
		}
		return tok, nil
	}
	// Token answers a value position with `{` or `[`, and answers a closing
	// delimiter there with a syntax error of its own ("invalid character '}'
	// looking for beginning of value") that the branch above has already
	// returned. So the list below is the `[`, and everything else reaching here
	// is an object: the `case '{':` and the `default:` that used to spell the
	// two of them were cases for a token the decoder cannot produce, and the
	// `default:` was a refusal no document could reach.
	if delim == '[' {
		list := []any{}
		for dec.More() {
			item, err := decodeValue(dec, data, depth+1)
			if err != nil {
				return nil, err
			}
			list = append(list, item)
		}
		if _, err := dec.Token(); err != nil { // the closing bracket
			return nil, fmt.Errorf("mailbox: reading a record: %w", err)
		}
		return list, nil
	}
	object := map[string]any{}
	for dec.More() {
		key, err := readKey(dec, data)
		if err != nil {
			return nil, err
		}
		if _, duplicate := object[key]; duplicate {
			return nil, fmt.Errorf("mailbox: the key %q appears twice in one record", key)
		}
		value, err := decodeValue(dec, data, depth+1)
		if err != nil {
			return nil, err
		}
		object[key] = value
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return nil, fmt.Errorf("mailbox: reading a record: %w", err)
	}
	return object, nil
}

// readKey reads one object's key exactly as it was written.
//
// The token is always a string: json.Decoder answers an object's key with a
// string and reports every other token in that position as a syntax error of
// its own, so the assertion is written without its ok flag — a key it could
// not read would be the empty string, which the record then carries and the
// validator refuses (unknown-field, or empty-string where the key is one it
// names), never a key this function invented. The two checks that used to sit
// here were branches no document could reach.
//
// A key that spells a lone surrogate is kept as the fault it is, so the
// validator answers unknown-field for it as the module does; its error is
// returned unwrapped, because the unquoter's own message already names the
// literal's fault and the caller adds nothing but a second prefix.
func readKey(dec *json.Decoder, data []byte) (string, error) {
	start := dec.InputOffset()
	tok, err := dec.Token()
	if err != nil {
		return "", fmt.Errorf("mailbox: reading a record's key: %w", err)
	}
	end := dec.InputOffset()
	key, _ := tok.(string)
	if hasLoneSurrogateEscape(data[start:end]) {
		return unquoteKeepingLoneSurrogates(data[start:end])
	}
	return key, nil
}

// hasLoneSurrogateEscape reports whether a JSON string literal — its own bytes,
// quotes included — spells an unpaired surrogate with a `\u` escape: `"\ud800"`
// is half a character, and both ends refuse such a string (README: "a string
// the two encoders spell differently").
//
// It is asked of the literal rather than of the decoded string because the
// decoder has already thrown the difference away: encoding/json replaces an
// unpaired surrogate escape with U+FFFD and reports nothing, so the string that
// comes back from Token() is a character the phone can speak and the record
// carries no fault the validator could refuse. The scan is over the bytes,
// where the three cases are still distinct:
//
//   - `\uD83D\uDE00` is a pair and one astral character, and is not a fault;
//   - `\uD800` alone is;
//   - `\\uD800` is an escaped backslash and then six characters of ordinary
//     text — the record is about the string `\uD800`, not about a surrogate —
//     so an escaped character is skipped rather than read as an escape.
//
// span is the raw bytes of one string token as the decoder reported them, from
// InputOffset before the token to InputOffset after it, so it begins at the
// opening quote or at the punctuation before it and ends at the closing quote.
// It never spans another token, so a backslash found in it belongs to this
// string.
func hasLoneSurrogateEscape(span []byte) bool {
	open := bytes.IndexByte(span, '"')
	if open < 0 {
		return false
	}
	// The content is everything between the quotes.
	end := len(span) - 1
	for i := open + 1; i < end; {
		if span[i] != '\\' {
			i++
			continue
		}
		if i+1 >= end {
			return false // a decoder cannot produce a string ending in a lone backslash
		}
		if span[i+1] != 'u' {
			i += 2 // an escaped quote, backslash or control character
			continue
		}
		if i+6 > end {
			return false // nor an escape with no room for its four digits
		}
		first, ok := hexQuad(span[i+2 : i+6])
		if !ok {
			return false
		}
		switch {
		case first >= 0xdc00 && first <= 0xdfff:
			// A low half with nothing before it.
			return true
		case first >= 0xd800 && first <= 0xdbff:
			// A high half: it is a character only if its low half follows
			// immediately, as another `\u` escape.
			if i+12 <= end && span[i+6] == '\\' && span[i+7] == 'u' {
				if low, ok := hexQuad(span[i+8 : i+12]); ok && low >= 0xdc00 && low <= 0xdfff {
					i += 12
					continue
				}
			}
			return true
		default:
			i += 6
		}
	}
	return false
}

// unquoteKeepingLoneSurrogates decodes a JSON string literal — the raw bytes,
// quotes included — into the Go string it spells, differing from encoding/json
// in exactly one way: an unpaired surrogate escape is kept as the three WTF-8
// bytes of the code unit it names, rather than replaced by U+FFFD.
//
// Keeping it is what makes the refusal match the module's, and the reason the
// decoder does not refuse it itself. The module's rule is by FIELD — a lone
// surrogate in `id`, `reply_to`, `action.id` or `confirmation.id` is bad-key
// (keyField), in `text`, `sender`, `agent`, an option or an agent's name it is
// bad-character (stringField), and in a key that is not a field it is
// unknown-field — and a decoder that picks a code from where the string sits
// rather than what it is would answer half of them wrongly. So the decoder
// hands the fault on in the one spelling Go can carry it in, and the validator,
// which knows the field, refuses it with the module's own code; CanonicalBytes
// refuses to sign invalid UTF-8, so no tag is ever made over the laundered
// bytes either.
//
// It is called only for a literal hasLoneSurrogateEscape has already found, but
// it decodes the whole literal, so every other escape is handled too — a pair,
// a `\"`, a raw multi-byte character — and the result is the string the bytes
// spell, with the lone halves kept.
//
// A literal that is not one a JSON document can hold — no opening quote, no
// closing quote, a trailing backslash, half an escape, an escape JSON does not
// have, a `\u` escape whose four digits are not hexadecimal — is an error,
// never a partial string and never a loop that does not advance. Through Decode
// it cannot happen: the decoder refuses such a document first. It is written
// this way because the alternative the code used to have,
// a `break` for each of those cases, was wrong in both of its two shapes: the
// one in the loop body left the loop with the string only half decoded, and the
// one inside the switch never advanced i at all and spun forever.
func unquoteKeepingLoneSurrogates(literal []byte) (string, error) {
	open := bytes.IndexByte(literal, '"')
	if open < 0 {
		return "", errors.New("mailbox: a string literal has no opening quote")
	}
	end := len(literal) - 1
	if end <= open || literal[end] != '"' {
		return "", errors.New("mailbox: a string literal has no closing quote")
	}
	var b strings.Builder
	for i := open + 1; i < end; {
		c := literal[i]
		if c != '\\' {
			b.WriteByte(c)
			i++
			continue
		}
		if i+1 >= end {
			// Unreachable through Decode, which only calls this for a literal
			// the decoder accepted. It is an error rather than a `break`, which
			// here would leave the loop and hand back a half-decoded string: a
			// defensive path that cannot happen should be the loudest one, not
			// the quietest.
			return "", errors.New("mailbox: a string literal ends in a lone backslash")
		}
		switch literal[i+1] {
		case '"', '\\', '/':
			b.WriteByte(literal[i+1])
			i += 2
		case 'b':
			b.WriteByte('\b')
			i += 2
		case 'f':
			b.WriteByte('\f')
			i += 2
		case 'n':
			b.WriteByte('\n')
			i += 2
		case 'r':
			b.WriteByte('\r')
			i += 2
		case 't':
			b.WriteByte('\t')
			i += 2
		case 'u':
			if i+6 > end {
				// An error, and never a `break`: a `break` here leaves the
				// SWITCH without advancing i, so the for loop re-reads the same
				// byte forever. This is the one of the two that used to hang.
				return "", errors.New("mailbox: a string literal ends in a half-written escape")
			}
			first, ok := hexQuad(literal[i+2 : i+6])
			if !ok {
				// The ok flag is the whole point of hexQuad's pair, and
				// dropping it is not a small thing: the zero it returns with
				// false is a NUL, so `\uZZZZ` would decode to a string
				// carrying one, with no error, and the validator would then
				// answer bad-character for a character the document never
				// named. There is no canonical form for an escape JSON does
				// not have, so it is refused here.
				return "", errors.New("mailbox: a string literal carries a \\u escape whose four digits are not hexadecimal")
			}
			if isHighSurrogate(first) && i+12 <= end && literal[i+6] == '\\' && literal[i+7] == 'u' {
				if low, ok := hexQuad(literal[i+8 : i+12]); ok && isLowSurrogate(low) {
					b.WriteRune(utf16Rune(first, low))
					i += 12
					continue
				}
			}
			if isHighSurrogate(first) || isLowSurrogate(first) {
				writeWTF8(&b, first)
				i += 6
				continue
			}
			b.WriteRune(rune(first))
			i += 6
		default:
			return "", fmt.Errorf("mailbox: a string literal carries an escape the JSON grammar does not have: \\%c", literal[i+1])
		}
	}
	return b.String(), nil
}

func isHighSurrogate(code uint32) bool { return code >= 0xd800 && code <= 0xdbff }

func isLowSurrogate(code uint32) bool { return code >= 0xdc00 && code <= 0xdfff }

// utf16Rune is the character a surrogate pair spells.
func utf16Rune(high, low uint32) rune {
	return rune(0x10000 + (high-0xd800)<<10 + (low - 0xdc00))
}

// writeWTF8 writes the three bytes of an unpaired surrogate: the encoding
// UTF-8 has no room for — which is the point. The string is deliberately
// invalid UTF-8, so utf8.ValidString reports it, the validator refuses it and
// CanonicalBytes will not sign it.
func writeWTF8(b *strings.Builder, code uint32) {
	b.WriteByte(byte(0xe0 | code>>12))
	b.WriteByte(byte(0x80 | code>>6&0x3f))
	b.WriteByte(byte(0x80 | code&0x3f))
}

// hexQuad reads the four hex digits of a `\u` escape as a UTF-16 code unit.
func hexQuad(digits []byte) (uint32, bool) {
	var value uint32
	for _, digit := range digits {
		var nibble uint32
		switch {
		case digit >= '0' && digit <= '9':
			nibble = uint32(digit - '0')
		case digit >= 'a' && digit <= 'f':
			nibble = uint32(digit-'a') + 10
		case digit >= 'A' && digit <= 'F':
			nibble = uint32(digit-'A') + 10
		default:
			return 0, false
		}
		value = value<<4 | nibble
	}
	return value, true
}

// Message is a validated mailbox message: the named fields, checked, with the
// absent optional ones marked absent rather than zeroed, because "no acked" and
// "acked: false" are two different records.
type Message struct {
	ID     string
	Sender string
	Text   string
	At     int64
	MAC    string
	// Kind is the effective kind: the message's own, or KindMessage when it
	// carries none.
	Kind string
	// Agent is the agent the message names, when it names one.
	Agent    string
	HasAgent bool
	// Acked is the phone's receipt. It is written after the tag was made, so it
	// is not covered by it — and it is only meaningful with HasAcked.
	Acked    bool
	HasAcked bool
	// Options is a prompt's options, in the order they are read aloud.
	Options []string
	// ReplyTo is the prompt an answer settles.
	ReplyTo    string
	HasReplyTo bool
	// Action is the destructive command an action carries.
	Action *Action
}

// Action is D169's action record: the verb, the target it resolves to, the id
// of the action, the confirmation it was given and — for a merge — the head SHA
// the read-back named.
type Action struct {
	ID           string
	Verb         string
	Target       Target
	HeadSHA      string
	HasHeadSHA   bool
	Confirmation Confirmation
}

// Target is a resolved target: a run, or a repository and a pull request.
type Target struct {
	Run    string
	Repo   string
	PR     int64
	IsRepo bool
}

// Confirmation is the id of a confirmation and the time it was given.
type Confirmation struct {
	ID string
	At int64
}

// Heartbeat is a validated heartbeat: D117's node, signed (P6.3, O7).
type Heartbeat struct {
	At   int64
	Boot string
	MAC  string
	// Agents is the advertised agent list, which "list agents" reads out and
	// D163 defaults to. It is empty when the heartbeat advertises none.
	Agents []Agent
}

// Agent is one advertised agent: its id, and the name that is spoken.
type Agent struct {
	ID   string
	Name string
}

// ValidateMessage decides whether r is a mailbox message.
//
// It mirrors shared/mailbox-schema's validateMessage field for field and in the
// same order, so that a value the module refuses is refused here with the same
// code and the same field at fault: the field set first (the field set is the
// version), then the five required fields, then each field's own rules, then
// the (kind, field) table and the action record. A nil Record is the one
// refusal the type cannot express, and it is refused not-an-object.
func ValidateMessage(r Record) (*Message, *Refusal) {
	if r == nil {
		return nil, refuse(CodeNotAnObject, "")
	}
	// Unknown first: a field that is not in the set is refused before any of
	// its neighbours is judged. The keys are visited in byte order rather than
	// in the map's own order, so naming the field at fault is deterministic.
	for _, key := range sortedKeys(r) {
		if !contains(Fields, key) {
			return nil, refuse(CodeUnknownField, key)
		}
	}
	for _, key := range RequiredFields {
		if !r.has(key) {
			return nil, refuse(CodeMissingField, key)
		}
	}

	id := keyField(r["id"], "id")
	if id.bad != nil {
		return nil, id.bad
	}
	sender := stringField(r["sender"], "sender", SenderMax, false)
	if sender.bad != nil {
		return nil, sender.bad
	}
	text := stringField(r["text"], "text", TextMax, true)
	if text.bad != nil {
		return nil, text.bad
	}
	at, bad := timestampField(r["at"], "at")
	if bad != nil {
		return nil, bad
	}
	mac := macField(r["mac"])
	if mac.bad != nil {
		return nil, mac.bad
	}

	message := &Message{
		ID:     id.value,
		Sender: sender.value,
		Text:   text.value,
		At:     at,
		MAC:    mac.value,
		Kind:   KindMessage,
	}

	if r.has("kind") {
		kind, bad := kindField(r["kind"])
		if bad != nil {
			return nil, bad
		}
		message.Kind = kind
	}
	if r.has("agent") {
		agent := identityField(r["agent"], "agent", AgentMax)
		if agent.bad != nil {
			return nil, agent.bad
		}
		message.Agent, message.HasAgent = agent.value, true
	}

	// The (kind, field) table, in the order that names the more useful fault
	// first: a field the kind cannot carry at all, then one it must carry and
	// does not, then the fields themselves.
	for _, field := range []string{"options", "reply_to", "action"} {
		if r.has(field) && !kindOptional[message.Kind][field] {
			return nil, refuse(CodeFieldNotAllowed, field)
		}
	}
	for _, field := range kindRequired[message.Kind] {
		if !r.has(field) {
			return nil, refuse(CodeMissingField, field)
		}
	}
	if r.has("options") {
		options := optionsField(r["options"])
		if options.bad != nil {
			return nil, options.bad
		}
		message.Options = options.value
	}
	if r.has("reply_to") {
		replyTo := keyField(r["reply_to"], "reply_to")
		if replyTo.bad != nil {
			return nil, replyTo.bad
		}
		message.ReplyTo, message.HasReplyTo = replyTo.value, true
	}
	if r.has("action") {
		action := actionField(r["action"])
		if action.bad != nil {
			return nil, action.bad
		}
		message.Action = action.value
	}
	if r.has("acked") {
		acked, isBool := r["acked"].(bool)
		if !isBool {
			return nil, refuse(CodeWrongType, "acked")
		}
		message.Acked, message.HasAcked = acked, true
	}
	return message, nil
}

// IsMessage is ValidateMessage's verdict as a boolean, for a caller that needs
// no reason — a filter, a guard, a test's expectation.
func IsMessage(r Record) bool {
	_, refusal := ValidateMessage(r)
	return refusal == nil
}

// ValidateHeartbeat decides whether r is a consumer's heartbeat: `at`, `boot`,
// an optional `agents` list, and a `mac` over all three. It is a different
// record from a message with a different field set, and the same shape of
// answer.
func ValidateHeartbeat(r Record) (*Heartbeat, *Refusal) {
	if r == nil {
		return nil, refuse(CodeNotAnObject, "")
	}
	for _, key := range sortedKeys(r) {
		if !contains(heartbeatFields, key) {
			return nil, refuse(CodeUnknownField, key)
		}
	}
	for _, key := range heartbeatRequired {
		if !r.has(key) {
			return nil, refuse(CodeMissingField, key)
		}
	}
	at, refusal := timestampField(r["at"], "at")
	if refusal != nil {
		return nil, refusal
	}
	boot := identityField(r["boot"], "boot", BootMax)
	if boot.bad != nil {
		return nil, boot.bad
	}
	mac := macField(r["mac"])
	if mac.bad != nil {
		return nil, mac.bad
	}
	heartbeat := &Heartbeat{At: at, Boot: boot.value, MAC: mac.value}
	if r.has("agents") {
		agents := agentsField(r["agents"])
		if agents.bad != nil {
			return nil, agents.bad
		}
		heartbeat.Agents = agents.value
	}
	return heartbeat, nil
}

// The kinds' field tables: what each kind may carry beyond the shared fields,
// and what it must. A field on the wrong kind is field-not-allowed, not an
// unknown field: two agents disagreeing about whether a message is an action is
// exactly the ambiguity this schema exists to refuse.
var (
	kindOptional = map[string]map[string]bool{
		"message": {},
		"prompt":  {"options": true},
		"answer":  {"reply_to": true},
		"action":  {"action": true, "reply_to": true},
	}
	kindRequired = map[string][]string{
		"message": {},
		"prompt":  {},
		"answer":  {"reply_to"},
		"action":  {"action"},
	}
)

// The action record's, the confirmation's, the heartbeat's and an advertised
// agent's fields, exactly, and the ones each must have.
var (
	actionFields       = []string{"id", "verb", "target", "head_sha", "confirmation"}
	actionRequired     = []string{"id", "verb", "target", "confirmation"}
	confirmationFields = []string{"id", "at"}
	repoTargetFields   = []string{"repo", "pr"}
	heartbeatFields    = []string{"at", "boot", "agents", "mac"}
	heartbeatRequired  = []string{"at", "boot", "mac"}
	agentFields        = []string{"id", "name"}
)

// CanonicalBytes is the one serialisation of a record that a tag covers: the
// record without `mac` and without `acked`, plus the direction, as JSON with
// keys sorted by code point, no insignificant whitespace, absent fields omitted
// (never null), strings as UTF-8 with JSON's own escaping only, and numbers as
// integers.
//
// It is the protocol's most exact surface, and the bytes here must equal
// shared/mailbox-schema's canonicalBytes byte for byte — which is what the
// vectors replay asserts, case by case, against the committed vectors.json. It
// refuses, rather than coercing, everything whose canonical form the two
// languages spell differently, because signing bytes a reader will not see is
// the failure mode that matters:
//
//   - `null`, and the number -0. A dropped field is two records with one
//     canonical form, and one byte string with one tag is what the tag exists
//     to prevent.
//   - a number that is not a safe integer. Go prints `1e-07` where JavaScript
//     prints `1e-7` for the same double.
//   - a string Go escapes and JavaScript does not: U+2028 and U+2029 (Go's
//     encoder escapes both even with SetEscapeHTML off) and invalid UTF-8.
//     Those are the two the module's own canonicalString refuses, and this
//     package refuses the same two and no more — a third refusal is a record
//     the phone tags and the consumer will not verify. The schema refuses both
//     in every string it validates, so a caller that validated first cannot
//     arrive here; this is the fence for the caller that did not.
//
// The direction is an argument, not a field of the record: the reader passes
// the node it read from, so a tag made for the outbox does not verify as inbox.
// A record that carries a `direction` of its own is refused.
func CanonicalBytes(record Record, direction string) ([]byte, error) {
	if record == nil {
		return nil, ErrNotAnObject
	}
	if !contains(Directions, direction) {
		return nil, fmt.Errorf("mailbox: the direction must be one of %s", strings.Join(Directions, ", "))
	}
	if record.has(directionField) {
		return nil, errors.New("mailbox: a record may not carry its own direction; the reader supplies it")
	}
	// A copy with the two uncovered fields dropped, so the caller's record is
	// never written to.
	covered := make(map[string]any, len(record)+1)
	for key, value := range record {
		if key == "mac" || key == "acked" {
			continue
		}
		covered[key] = value
	}
	covered[directionField] = direction
	text, err := serialiseObject(covered)
	if err != nil {
		return nil, err
	}
	return []byte(text), nil
}

// serialiseObject writes an object's canonical form: its keys sorted by code
// point, each written as a JSON string.
func serialiseObject(value map[string]any) (string, error) {
	var b strings.Builder
	b.WriteByte('{')
	// sort.Strings orders by byte, and UTF-8 byte order IS code-point order —
	// which is the order the phone's implementation produces. JavaScript's own
	// default `.sort()` compares UTF-16 code units and disagrees for exactly one
	// pair of ranges, an astral key against U+E000..U+FFFF. The vectors' "keys
	// are ordered by code point" case is what holds this to the module.
	for i, key := range sortedKeys(value) {
		if i > 0 {
			b.WriteByte(',')
		}
		encoded, err := encodeJSONString(key)
		if err != nil {
			return "", err
		}
		b.WriteString(encoded)
		b.WriteByte(':')
		item, err := serialiseValue(value[key])
		if err != nil {
			return "", err
		}
		b.WriteString(item)
	}
	b.WriteByte('}')
	return b.String(), nil
}

// serialiseValue writes one value's canonical form. Arrays keep their order —
// an option's position is what "option two" means — and every string, key or
// value, is encoded on its own, so JSON's own escaping is the only escaping.
func serialiseValue(value any) (string, error) {
	switch v := value.(type) {
	case nil:
		return "", errors.New("mailbox: absent fields are omitted and never null, so a record carrying one has no canonical form")
	case string:
		return encodeJSONString(v)
	case bool:
		if v {
			return "true", nil
		}
		return "false", nil
	case json.Number, int, int64, float64:
		// Every numeric type this package accepts, in one case, so that the
		// set canonicalisation takes is the set validation takes: a type
		// accepted by one half and refused by the other is a record that
		// validates and cannot be tagged.
		return serialiseNumber(v)
	case Record:
		return serialiseObject(v)
	case map[string]any:
		return serialiseObject(v)
	case []any:
		items := make([]string, len(v))
		for i, item := range v {
			encoded, err := serialiseValue(item)
			if err != nil {
				return "", err
			}
			items[i] = encoded
		}
		return "[" + strings.Join(items, ",") + "]", nil
	default:
		return "", fmt.Errorf("mailbox: a %T is not a value a message carries", value)
	}
}

// maxSafeInteger is JavaScript's Number.MAX_SAFE_INTEGER, the line past which a
// double no longer holds every integer, and therefore the line past which the
// two languages stop spelling a number the same way.
const maxSafeInteger = int64(1)<<53 - 1

// serialiseNumber writes one number's canonical form, refusing the values
// whose bytes the two languages disagree about. A number written in exponent
// form, or with a trailing `.0`, is accepted when it holds an integer —
// JSON.parse gives 1e3 as 1000 in JavaScript and JSON.stringify writes "1000" —
// and refused when it does not.
//
// Negative zero is refused for every spelling of it — "-0", "-0.0", "-0e0" and
// the rest — which is why the question is asked of the parsed VALUE and not of
// the text: these all parse to the one double `-0`, they are all safe integers
// to `Number.isSafeInteger`, and the module's own canonicalBytes throws for all
// of them (README: a record carrying -0 "can be neither tagged nor verified").
// Refusing only the literal "-0" would let `-0.0` through, canonicalise it to
// "0" and tag it — a byte-parity hole in the one function whose whole contract
// is byte parity.
//
// It takes every numeric type this package accepts — the json.Number the wire
// carries and the int, int64 and float64 an `encoding/json` caller builds — and
// asks the same two questions of all of them through numericValue, because the
// safe-integer line is a property of the number and not of the Go type it
// arrived in. A table that answered it for one type and not another would give
// the same number two verdicts, which is precisely what the module's own tests
// (index.test.js: `at: 2 ** 53` is bad-timestamp, and canonicalBytes throws for
// it) exist to prevent.
func serialiseNumber(value any) (string, error) {
	f, isNumber := numericValue(value)
	if !isNumber {
		return "", fmt.Errorf("mailbox: a %T is not a value a message carries", value)
	}
	// The line is safeInteger's, not a second copy of it: a number that
	// validates here has to serialise here, and two spellings of one line are
	// two places for them to drift apart.
	n, isSafe := safeInteger(f)
	if !isSafe {
		return "", errors.New("mailbox: a number must be a safe integer, so both languages spell it the same way")
	}
	// -0 passes that line — it is an integer and it is not large — and it is
	// still refused, because it has no single spelling: JavaScript writes it as
	// `0` in one place and `-0` in another, and the module refuses a record
	// carrying one outright (README: it "can be neither tagged nor verified").
	if n == 0 && math.Signbit(f) {
		return "", errors.New("mailbox: -0 is refused: it is a safe integer with no single spelling")
	}
	return strconv.FormatInt(n, 10), nil
}

// encodeJSONString writes one string's canonical form: JSON's own escaping and
// nothing else.
//
// The HTML escaping is OFF, and it is the trap §8.1 names: left on, Go's
// encoder turns `<`, `>` and `&` into `\u003c`, `\u003e` and `\u0026`,
// where JSON.stringify leaves all three raw — so the same record would have two
// spellings, one per language. The vectors' "html characters stay raw" case is
// the assertion that it stays off.
func encodeJSONString(value string) (string, error) {
	if err := canonicalString(value); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	// Encode's error is not read: it reports one for a writer that fails and
	// for a value it cannot marshal, and neither can happen here — buf is a
	// bytes.Buffer, whose Write cannot fail, and the value is a string
	// canonicalString has already passed. Reading an error that cannot come is
	// a branch no test can reach, and a branch no test can reach is one a
	// reader has to prove impossible before trusting the checks beside it.
	_ = enc.Encode(value)
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

// canonicalString is the check a string has to pass to have a canonical form at
// all, for a key exactly as for a value. It refuses the classes whose bytes the
// two encoders spell differently and which therefore have no single canonical
// form: invalid UTF-8 (which is what a lone surrogate is on this side of the
// wire — the three WTF-8 bytes of the code unit the document escaped, see
// unquoteKeepingLoneSurrogates — and a string Go cannot encode is not one the
// phone wrote) and U+2028 and U+2029.
//
// It refuses those and nothing else, and the "and nothing else" is the half
// that has already been wrong once: this function used to refuse `\b` and
// `\f` as well, on the belief that Go's encoder writes `\u0008` and
// `\u000c` where JSON.stringify writes `\b` and `\f`. It does not —
// `encoding/json` with SetEscapeHTML(false) writes the short escape for exactly
// the five characters JSON.stringify writes one for (\b, \t, \n, \f, \r) and
// `\u00xx` for the rest of the control range — so refusing them refused a
// record the module returns bytes for, which is the byte-parity contract
// backwards. The premise is no longer a comment: it is pinned by
// TestControlCharactersBothEncodersSpellAlike, because it is a fact about a Go
// release, and a Go release is what changes it.
func canonicalString(value string) error {
	if !utf8.ValidString(value) {
		return errors.New("mailbox: a string is not valid UTF-8, so the two encoders spell it differently")
	}
	for _, r := range value {
		if r == '\u2028' || r == '\u2029' {
			return errors.New("mailbox: a string carries U+2028 or U+2029, which Go's encoder escapes and JavaScript's does not")
		}
	}
	return nil
}

// The character classes the module fixes for the values the consumer will later
// pass to a program: a run id to `fugaro cancel`, a repository to
// `gh pr merge`, an agent or boot id, and a tag or a head SHA.
var (
	identityPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)
	runChars        = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)
	runPattern      = regexp.MustCompile(`^[A-Za-z0-9_:][A-Za-z0-9._:-]*$`)
	repoChars       = regexp.MustCompile(`^[A-Za-z0-9._:/-]+$`)
	repoPattern     = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*/[A-Za-z0-9_][A-Za-z0-9._-]*$`)
	hexPattern      = regexp.MustCompile(`^[0-9a-f]+$`)
)

// forbiddenKeyChars are the characters a database key may not contain.
var forbiddenKeyChars = map[rune]bool{'%': true, '.': true, '$': true, '#': true, '[': true, ']': true, '/': true}

// utf16Len is a string's length as JavaScript counts it: UTF-16 code units,
// where a character above U+FFFF counts two and a lone surrogate counts one.
// Every bound in this module is a JavaScript `.length`, so counting runes or
// bytes here would let a message of astral characters past a bound the module
// refuses — and counting the three WTF-8 bytes of a lone surrogate as three
// code units would refuse one the module answers bad-key for.
func utf16Len(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if isWTF8Surrogate(s[i:]) {
			// A lone surrogate that arrived as JSON is three WTF-8 bytes here
			// and one UTF-16 code unit to the module. `range` would read those
			// three bytes as three U+FFFD and count three, which is how 34 lone
			// surrogates in `id` came back too-long where the module answers
			// bad-key. It is one, in both languages, and it is counted once.
			n++
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r > 0xffff {
			n += 2
		} else {
			n++
		}
		i += size
	}
	return n
}

// isWTF8Surrogate reports whether s begins with the three-byte WTF-8 encoding of
// a UTF-16 surrogate — what writeWTF8 writes for a lone half, which no decoder
// of UTF-8 accepts and which therefore arrives as three invalid bytes rather
// than as one character.
func isWTF8Surrogate(s string) bool {
	return len(s) >= 3 && s[0] == 0xed && s[1] >= 0xa0 && s[1] <= 0xbf && s[2] >= 0x80 && s[2] <= 0xbf
}

// checked is one field's verdict: a value, or the refusal to return.
type checked[T any] struct {
	value T
	bad   *Refusal
}

func ok[T any](value T) checked[T] { return checked[T]{value: value} }

func bad[T any](code Code, field string) checked[T] { return checked[T]{bad: refuse(code, field)} }

// keyField validates an id: a non-empty string that is a database key.
func keyField(value any, field string) checked[string] {
	s, isString := value.(string)
	if !isString {
		return bad[string](CodeWrongType, field)
	}
	if s == "" {
		return bad[string](CodeEmptyString, field)
	}
	if utf16Len(s) > IDMax {
		return bad[string](CodeTooLong, field)
	}
	if !utf8.ValidString(s) {
		// Go cannot hold a lone surrogate as a character: the Go spelling of
		// one is invalid UTF-8 (WTF-8). The module refuses a lone surrogate in
		// a key as bad-key, and CanonicalBytes refuses to sign invalid UTF-8,
		// so accepting it here would pass on a record that can never be tagged.
		return bad[string](CodeBadKey, field)
	}
	for _, r := range s {
		if forbiddenKeyChars[r] || r < 0x20 || r == 0x7f || r == ' ' || r == ' ' {
			return bad[string](CodeBadKey, field)
		}
	}
	return ok(s)
}

// stringField validates a required, bounded, display-safe string: max is its
// bound, and lineBreaks says whether the string is a body (which may be several
// lines) rather than a single-line identity.
func stringField(value any, field string, max int, lineBreaks bool) checked[string] {
	s, isString := value.(string)
	if !isString {
		return bad[string](CodeWrongType, field)
	}
	if s == "" {
		return bad[string](CodeEmptyString, field)
	}
	if utf16Len(s) > max {
		return bad[string](CodeTooLong, field)
	}
	if !utf8.ValidString(s) {
		// The Go spelling of a lone surrogate is invalid UTF-8 (WTF-8): the
		// module refuses one in a value as bad-character, and CanonicalBytes
		// refuses to sign invalid UTF-8, so the validator refuses it first
		// rather than accepting a string no encoder can spell. A literal U+FFFD
		// is valid UTF-8 and stays a character the phone can speak.
		return bad[string](CodeBadCharacter, field)
	}
	for _, r := range s {
		line := lineBreaks && (r == '\t' || r == '\n' || r == '\r')
		if (r < 0x20 && !line) || r == 0x7f || r == ' ' || r == ' ' {
			return bad[string](CodeBadCharacter, field)
		}
	}
	return ok(s)
}

// identityField validates a short identity string — an agent id, an advertised
// agent's id, a boot id: non-empty, bounded, and drawn from the narrow set D117
// fixes for one. A value that cannot be spoken as an agent's id is not one.
func identityField(value any, field string, max int) checked[string] {
	text := stringField(value, field, max, false)
	if text.bad != nil {
		return bad[string](text.bad.Code, text.bad.Field)
	}
	if !identityPattern.MatchString(text.value) {
		return bad[string](CodeBadCharacter, field)
	}
	return ok(text.value)
}

// optionsField validates a prompt's options: a present list is a non-empty list
// of distinct, bounded, single-line labels. Distinct matters because the app
// answers by position and reads the labels aloud — two identical options are a
// choice with two right answers.
func optionsField(value any) checked[[]string] {
	list, isList := value.([]any)
	if !isList {
		return bad[[]string](CodeWrongType, "options")
	}
	if len(list) == 0 {
		return bad[[]string](CodeTooFew, "options")
	}
	if len(list) > OptionsMax {
		return bad[[]string](CodeTooMany, "options")
	}
	options := make([]string, 0, len(list))
	seen := map[string]bool{}
	for i, item := range list {
		option := stringField(item, indexField("options", i), OptionMax, false)
		if option.bad != nil {
			return bad[[]string](option.bad.Code, option.bad.Field)
		}
		if seen[option.value] {
			return bad[[]string](CodeDuplicateEntry, indexField("options", i))
		}
		seen[option.value] = true
		options = append(options, option.value)
	}
	return ok(options)
}

// agentsField validates a heartbeat's advertised agent list: a present list is
// a non-empty list of {id, name}, bounded in count, with distinct ids — two
// agents under one id make "talk to <agent>" ambiguous, and D163 picks the
// list's first entry by that id.
func agentsField(value any) checked[[]Agent] {
	list, isList := value.([]any)
	if !isList {
		return bad[[]Agent](CodeWrongType, "agents")
	}
	if len(list) == 0 {
		return bad[[]Agent](CodeTooFew, "agents")
	}
	if len(list) > AgentsMax {
		return bad[[]Agent](CodeTooMany, "agents")
	}
	agents := make([]Agent, 0, len(list))
	seen := map[string]bool{}
	for i, item := range list {
		where := indexField("agents", i)
		entry, isObject := asObject(item)
		if !isObject {
			return bad[[]Agent](CodeNotAnObject, where)
		}
		for _, key := range sortedKeys(entry) {
			if !contains(agentFields, key) {
				return bad[[]Agent](CodeUnknownField, where+"."+key)
			}
		}
		for _, key := range agentFields {
			if _, present := entry[key]; !present {
				return bad[[]Agent](CodeMissingField, where+"."+key)
			}
		}
		id := identityField(entry["id"], where+".id", AgentMax)
		if id.bad != nil {
			return bad[[]Agent](id.bad.Code, id.bad.Field)
		}
		name := stringField(entry["name"], where+".name", AgentNameMax, false)
		if name.bad != nil {
			return bad[[]Agent](name.bad.Code, name.bad.Field)
		}
		if seen[id.value] {
			return bad[[]Agent](CodeDuplicateEntry, where+".id")
		}
		seen[id.value] = true
		agents = append(agents, Agent{ID: id.value, Name: name.value})
	}
	return ok(agents)
}

// indexField names one entry of a list the way the module does: `options[2]`.
func indexField(list string, index int) string {
	return fmt.Sprintf("%s[%d]", list, index)
}

// kindField validates a message's kind: a string drawn from the four, refused
// as unknown-kind rather than as a wrong type, because "not a kind" and "not a
// string" are different questions.
func kindField(value any) (string, *Refusal) {
	s, isString := value.(string)
	if !isString {
		return "", refuse(CodeWrongType, "kind")
	}
	if !contains(Kinds, s) {
		return "", refuse(CodeUnknownKind, "kind")
	}
	return s, nil
}

// actionField validates D169's action record: the verb, the target it resolves
// to, the action id, the confirmation record, and — for a merge — the head SHA
// the read-back named. Every part is required because every part is executed:
// the consumer runs `fugaro cancel RUN` or `gh pr merge <n> --repo
// <owner/name>` from these fields and nothing else, and a record missing any of
// them is one that cannot be checked before it runs.
//
// The kind is not a parameter, and that is the honest signature: the (kind,
// field) table in ValidateMessage has already refused an `action` on every kind
// that does not carry one, with this same refusal — field-not-allowed at
// `action` — so the check that used to open this function guarded a record no
// caller could hand it.
func actionField(value any) checked[*Action] {
	record, isObject := asObject(value)
	if !isObject {
		return bad[*Action](CodeNotAnObject, "action")
	}
	for _, key := range sortedKeys(record) {
		if !contains(actionFields, key) {
			return bad[*Action](CodeUnknownField, "action."+key)
		}
	}
	for _, key := range actionRequired {
		if _, present := record[key]; !present {
			return bad[*Action](CodeMissingField, "action."+key)
		}
	}
	id := keyField(record["id"], "action.id")
	if id.bad != nil {
		return bad[*Action](id.bad.Code, id.bad.Field)
	}
	verb, isString := record["verb"].(string)
	if !isString {
		return bad[*Action](CodeWrongType, "action.verb")
	}
	if !contains(Actions, verb) {
		return bad[*Action](CodeUnknownVerb, "action.verb")
	}
	target := targetField(record["target"], verb)
	if target.bad != nil {
		return bad[*Action](target.bad.Code, target.bad.Field)
	}

	// A merge carries the head SHA read back at confirmation, and the consumer
	// re-reads the pull request's head and refuses the merge if it has moved
	// (D169, §3.5). A SHA on any other verb is a field the record has no
	// business carrying: a cancel names a run, and there is no head to compare.
	_, hasHead := record["head_sha"]
	if verb == "merge" && !hasHead {
		return bad[*Action](CodeMissingField, "action.head_sha")
	}
	action := &Action{ID: id.value, Verb: verb, Target: target.value}
	if hasHead {
		if verb != "merge" {
			return bad[*Action](CodeFieldNotAllowed, "action.head_sha")
		}
		sha := shaField(record["head_sha"])
		if sha.bad != nil {
			return bad[*Action](sha.bad.Code, sha.bad.Field)
		}
		action.HeadSHA, action.HasHeadSHA = sha.value, true
	}
	confirmation := confirmationField(record["confirmation"])
	if confirmation.bad != nil {
		return bad[*Action](confirmation.bad.Code, confirmation.bad.Field)
	}
	action.Confirmation = confirmation.value
	return ok(action)
}

// targetField validates a resolved target: a run id, or a repository and a pull
// request number, and the verb decides which. `confirm` — the ruling's third
// verb, confirming an action — takes either, because the action being confirmed
// can be a cancel or a merge.
//
// The order of the questions is deliberate: what shape is this target, is that
// shape complete and well formed, and only then does it agree with the verb.
//
// The shape rules are security rules, not tidiness ones. The consumer passes
// the run id to `fugaro cancel` and the repository to `gh pr merge --repo` as
// ARGV elements, so a value beginning with `-` or `.` is a flag — or a path —
// to whatever parser sees it. Refusing both families here is what stops a
// record from choosing the consumer's command line.
func targetField(value any, verb string) checked[Target] {
	record, isObject := asObject(value)
	if !isObject {
		return bad[Target](CodeNotAnObject, "action.target")
	}
	_, isRun := record["run"]
	_, hasRepo := record["repo"]
	_, hasPR := record["pr"]
	isRepo := hasRepo || hasPR

	// A target is one shape or the other, never both: a record naming a run and
	// a pull request is a record the consumer would have to choose between, and
	// a guess about which one the user confirmed is exactly what D169's
	// read-back exists to remove.
	if isRun && isRepo {
		return bad[Target](CodeBadTarget, "action.target")
	}
	if !isRun && !isRepo {
		return bad[Target](CodeBadTarget, "action.target")
	}

	if isRun {
		for _, key := range sortedKeys(record) {
			if key != "run" {
				return bad[Target](CodeUnknownField, "action.target."+key)
			}
		}
		run, isString := record["run"].(string)
		if !isString {
			return bad[Target](CodeWrongType, "action.target.run")
		}
		if run == "" {
			return bad[Target](CodeEmptyString, "action.target.run")
		}
		if utf16Len(run) > TargetMax {
			return bad[Target](CodeTooLong, "action.target.run")
		}
		if !runChars.MatchString(run) {
			return bad[Target](CodeBadCharacter, "action.target.run")
		}
		if !runPattern.MatchString(run) {
			return bad[Target](CodeBadTarget, "action.target.run")
		}
		if verb == "merge" {
			return bad[Target](CodeBadTarget, "action.target")
		}
		return ok(Target{Run: run})
	}

	for _, key := range sortedKeys(record) {
		if !contains(repoTargetFields, key) {
			return bad[Target](CodeUnknownField, "action.target."+key)
		}
	}
	for _, key := range repoTargetFields {
		if _, present := record[key]; !present {
			return bad[Target](CodeMissingField, "action.target."+key)
		}
	}
	repo, isString := record["repo"].(string)
	if !isString {
		return bad[Target](CodeWrongType, "action.target.repo")
	}
	if repo == "" {
		return bad[Target](CodeEmptyString, "action.target.repo")
	}
	if utf16Len(repo) > TargetMax {
		return bad[Target](CodeTooLong, "action.target.repo")
	}
	if !repoChars.MatchString(repo) {
		return bad[Target](CodeBadCharacter, "action.target.repo")
	}
	if !repoPattern.MatchString(repo) {
		return bad[Target](CodeBadTarget, "action.target.repo")
	}
	pr, refusal := positiveIntField(record["pr"], "action.target.pr")
	if refusal != nil {
		return bad[Target](refusal.Code, refusal.Field)
	}
	// A merge is the only verb that resolves to a repository (D169: the
	// ruling's "cancel" cancels a run and "merge" merges a pull request;
	// `confirm` may be either).
	if verb == "cancel" {
		return bad[Target](CodeBadTarget, "action.target")
	}
	return ok(Target{Repo: repo, PR: pr, IsRepo: true})
}

// positiveIntField validates a pull request number: a number that is a safe
// integer of at least one. A value of the wrong type and a number that is not a
// count are two different refusals.
func positiveIntField(value any, field string) (int64, *Refusal) {
	n, isInteger := intValue(value)
	if !isInteger {
		if isNumber(value) {
			return 0, refuse(CodeBadTarget, field)
		}
		return 0, refuse(CodeWrongType, field)
	}
	if n < 1 {
		return 0, refuse(CodeBadTarget, field)
	}
	return n, nil
}

// confirmationField validates the confirmation: the id of the confirmation and
// the time it was given. The id is the ledger's (the consumer refuses a reused
// one) and the time is what the command window is measured from. The timeout
// itself is the phone's, P6.2's, and deliberately not here: the pending time is
// phone-local state.
func confirmationField(value any) checked[Confirmation] {
	record, isObject := asObject(value)
	if !isObject {
		return bad[Confirmation](CodeNotAnObject, "action.confirmation")
	}
	for _, key := range sortedKeys(record) {
		if !contains(confirmationFields, key) {
			return bad[Confirmation](CodeUnknownField, "action.confirmation."+key)
		}
	}
	for _, key := range confirmationFields {
		if _, present := record[key]; !present {
			return bad[Confirmation](CodeMissingField, "action.confirmation."+key)
		}
	}
	id := keyField(record["id"], "action.confirmation.id")
	if id.bad != nil {
		return bad[Confirmation](id.bad.Code, id.bad.Field)
	}
	at, refusal := timestampField(record["at"], "action.confirmation.at")
	if refusal != nil {
		return bad[Confirmation](refusal.Code, refusal.Field)
	}
	return ok(Confirmation{ID: id.value, At: at})
}

// shaField validates a merge's head SHA: lowercase hex, at one of the two
// object-name lengths. Lowercase only, for the reason the mac is lowercase
// only — one object name has two spellings otherwise, and the consumer's
// comparison is byte for byte against what the provider returns.
func shaField(value any) checked[string] {
	s, isString := value.(string)
	if !isString {
		return bad[string](CodeBadSHA, "action.head_sha")
	}
	if utf16Len(s) != SHAMin && utf16Len(s) != SHAMax {
		return bad[string](CodeBadSHA, "action.head_sha")
	}
	if !hexPattern.MatchString(s) {
		return bad[string](CodeBadSHA, "action.head_sha")
	}
	return ok(s)
}

// macField validates a tag's shape: 64 lowercase hex characters, which is 32
// bytes of HMAC-SHA-256 (§3.5). Length and character set share one code because
// they are one question — is this a tag? — and uppercase is refused because hex
// has two spellings for one tag. Whether the tag is the RIGHT one needs a key,
// and that is T2's gate, not the schema's.
func macField(value any) checked[string] {
	s, isString := value.(string)
	if !isString {
		return bad[string](CodeWrongType, "mac")
	}
	if s == "" {
		return bad[string](CodeEmptyString, "mac")
	}
	if len(s) != MACMax || !hexPattern.MatchString(s) {
		return bad[string](CodeBadMAC, "mac")
	}
	return ok(s)
}

// timestampField validates a time: a non-negative integer below the exact-double
// line, in epoch milliseconds. A numeric string, a float, NaN and Infinity are
// each something else, and each is refused rather than rounded.
func timestampField(value any, field string) (int64, *Refusal) {
	n, isInteger := intValue(value)
	if !isInteger {
		if isNumber(value) {
			return 0, refuse(CodeBadTimestamp, field)
		}
		return 0, refuse(CodeWrongType, field)
	}
	if n < 0 {
		return 0, refuse(CodeBadTimestamp, field)
	}
	return n, nil
}

// numericValue reads any value this package accepts as a number, as the double
// it holds. It is the ONE place the accepted set is written down, so validation
// and canonicalisation cannot come to different answers about a type: the
// json.Number the wire carries, and the int, int64 and float64 an
// `encoding/json` caller builds a record with.
//
// float32 is NOT among them, and its absence is the decision rather than an
// oversight: JavaScript has no float32 to agree with, and a 24-bit mantissa
// cannot hold an epoch millisecond exactly, so a float32 is refused as the
// wrong type — a type this protocol does not speak — rather than as a number
// that is not a safe integer. int and int64 are not narrowed to a smaller
// integer type either: Go has one integer type per width, they all hold the
// same numbers, and the bound belongs to the value.
//
// A literal the double cannot hold (1e400) is a number and not a type error:
// ParseFloat reports the range, the value is Infinity, and Infinity is what
// JSON.parse gives JavaScript for the same bytes — so the safe-integer rule
// refuses it, with the code the module spells for it.
func numericValue(value any) (float64, bool) {
	switch v := value.(type) {
	case json.Number:
		f, err := strconv.ParseFloat(string(v), 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return 0, false
		}
		return f, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case float64:
		return v, true
	}
	return 0, false
}

// safeInteger is the one place the safe-integer line is drawn: an integer
// (1e3 and 1700000000000.0 are integers to Number.isSafeInteger, so they are
// integers here) and no larger than Number.MAX_SAFE_INTEGER, past which the two
// languages stop spelling a number the same way.
func safeInteger(f float64) (int64, bool) {
	if f != math.Trunc(f) || math.Abs(f) > float64(maxSafeInteger) {
		return 0, false
	}
	return int64(f), true
}

// intValue reads a value as the integer it holds, when it holds one.
func intValue(value any) (int64, bool) {
	f, isNumber := numericValue(value)
	if !isNumber {
		return 0, false
	}
	return safeInteger(f)
}

// isNumber reports whether a value is a number at all — the difference between
// a value of the wrong type and a number that is not an integer millisecond,
// which the module spells as two different codes.
func isNumber(value any) bool {
	_, isNumber := numericValue(value)
	return isNumber
}

// sortedKeys returns a record's own keys in byte order — which for UTF-8 is
// code-point order, the order the canonical form uses.
func sortedKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// asObject answers "is this value an object?" once, for every place that asks
// it: the decoder, the validators and the serialiser.
//
// It accepts both spellings of an object this package has — `map[string]any`,
// which is what Decode builds at every depth and what an encoding/json caller
// hands in, and `Record`, the package's own exported type. `Record` is defined
// as `map[string]any`, so the two are ASSIGNABLE to each other but not
// ASSERTABLE from an `any`: a type assertion to a non-interface type requires
// an identical dynamic type. Without this, a caller who builds a record with a
// nested Record gets a spurious not-an-object refusal for a record that
// CanonicalBytes serialises cleanly — the wire path is unaffected, but the two
// halves of the package would disagree about what an object is.
func asObject(value any) (map[string]any, bool) {
	switch v := value.(type) {
	case map[string]any:
		return v, true
	case Record:
		return v, true
	default:
		return nil, false
	}
}

// has reports whether a record carries a key at all, which is a different
// question from the value being empty: an explicit null is present, and is
// refused rather than treated as absent.
func (r Record) has(key string) bool {
	_, ok := r[key]
	return ok
}

// contains reports whether a list names a value.
func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
