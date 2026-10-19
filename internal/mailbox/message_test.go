package mailbox

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// The replay of shared/mailbox-schema/vectors.json, the committed contract both
// implementations of the canonical serialisation are held to (mobile plan P4.1,
// task T1; Ruling M9 — the file is merged and is never edited by this task).
//
// The tag itself is computed HERE, in the test, and not by the package under
// test: the plan gives the tag function to T2 (`internal/mailbox/mac.go`) and
// this task no `mac.go`, and one function is what keeps the verifying half and
// the signing half from drifting. What the replay therefore proves is the half
// T1 owns — that Go's canonical bytes ARE the module's, byte for byte — and it
// proves it the only way that is observable: by recomputing each vector's tag
// over those bytes with crypto/hmac, the same composition mac.js is, and
// comparing against the tag the file pins.
const vectorsPath = "../../shared/mailbox-schema/vectors.json"

// caseSchemaRefusal is the three vector cases whose record the schema refuses.
// The file pins a case's TAG and its bytes, not its schema verdict, so this
// map is T1's reading of the module's own rules rather than a quote of the
// file — and it is here rather than omitted because "every accepted vector's
// record validates" is only half a statement without the other half. The
// remaining 21 cases are asserted to validate.
var caseSchemaRefusal = map[string]struct {
	code Code
	why  string
}{
	"an unknown field is inside the bytes": {
		code: CodeUnknownField,
		why:  "the file's own `why` says so: `landed_at` is an unknown field",
	},
	"keys are ordered by code point, not by UTF-16 code unit": {
		code: CodeUnknownField,
		why:  "U+FFFF and U+1F600 are not fields of any kind, so they are unknown fields",
	},
	"verb swapped": {
		code: CodeBadTarget,
		why:  "the record relabels a cancel as a merge and keeps its `run` target; a merge resolves to a repository, so the target does not agree with the verb",
	},
}

func TestSchemaVectorsReplay(t *testing.T) {
	file := loadVectors(t)
	key := text(t, file["key"], "key")
	if !strings.Contains(key, "not-a-secret") {
		t.Fatalf("the vectors' key is %q: the file's own rule is a readable placeholder, never a credential", key)
	}
	cases := list(t, file["cases"], "cases")
	if len(cases) != 24 {
		t.Fatalf("vectors.json carries %d cases, not 24: the file, not the replay, is what changed", len(cases))
	}

	for _, entry := range cases {
		vector := object(t, entry, "case")
		name := text(t, vector["name"], "name")
		t.Run(name, func(t *testing.T) {
			why := text(t, vector["why"], "why")
			direction := text(t, vector["direction"], "direction")
			record := object(t, vector["message"], "message")
			wantBytes := text(t, vector["canonical"], "canonical")
			wantTag := text(t, vector["mac"], "mac")
			wantVerdict := text(t, vector["verdict"], "verdict")

			// The schema's own verdict. A heartbeart record is the one the
			// module's own dirOf calls a heartbeat: the record carrying `boot`.
			var refusal *Refusal
			if _, isHeartbeat := record["boot"]; isHeartbeat {
				_, refusal = ValidateHeartbeat(record)
			} else {
				_, refusal = ValidateMessage(record)
			}
			if refused, refusedBySchema := caseSchemaRefusal[name]; refusedBySchema {
				if refusal == nil {
					t.Errorf("the schema refuses this record (%s) and Go accepts it (%s)", refused.why, why)
				} else if refusal.Code != refused.code {
					// Which of two unknown fields is named is not pinned: the
					// module answers in JavaScript's insertion order and this
					// side answers in byte order, and the code is the answer a
					// caller branches on.
					t.Errorf("this record's schema refusal is %s, wanted %s (%s)", refusal, refused.code, refused.why)
				}
			} else if refusal != nil {
				t.Errorf("the schema accepts this record and Go refuses it %s (%s)", refusal, why)
			}

			// The bytes, byte for byte.
			gotBytes, err := CanonicalBytes(record, direction)
			if err != nil {
				t.Fatalf("CanonicalBytes: %v (%s)", err, why)
			}
			if string(gotBytes) != wantBytes {
				t.Errorf("the canonical bytes differ from the vector file:\n got %s\nwant %s\n     (%s)", gotBytes, wantBytes, why)
			}

			// The tag, over the bytes Go produced.
			gotTag := tagOf(key, string(gotBytes))
			if gotTag != wantTag {
				t.Errorf("the tag over Go's bytes is %s, the vector file pins %s (%s)", gotTag, wantTag, why)
			}

			// And whether the record carries the tag its bytes produce — which
			// is exactly what verifyMac decides, and what the file's verdict
			// says. A refused case carries a tag made for other bytes.
			carried, _ := record["mac"].(string)
			if verified := carried == gotTag; verified != (wantVerdict == "verified") {
				t.Errorf("the record verifies as %v, the vector file says %q (%s)", verified, wantVerdict, why)
			}
		})
	}
}

// nest rewrites a decoded record into the other spelling of an object this
// package has: every object, at every depth, becomes a Record — the package's
// own exported type. The wire path never produces one (Decode builds
// map[string]any), so this is the shape a caller who assembles a record in Go
// produces, and the validators must answer the same question about it.
func nest(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := Record{}
		for key, item := range v {
			out[key] = nest(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = nest(item)
		}
		return out
	default:
		return value
	}
}

// TestRecordsNestedInRecordsValidate is the other half of the object question.
// `Record` and `map[string]any` are assignable but not assertable from one
// another, so a validator that asked `value.(map[string]any)` refused a record
// this package's own CanonicalBytes serialises cleanly — a spurious refusal,
// and a disagreement between the two halves of the package about what an
// object is. Every accepted vector is replayed here in that spelling: it must
// validate exactly as before and still produce the same bytes.
func TestRecordsNestedInRecordsValidate(t *testing.T) {
	file := loadVectors(t)
	accepted := 0
	for _, entry := range list(t, file["cases"], "cases") {
		vector := object(t, entry, "case")
		name := text(t, vector["name"], "name")
		t.Run(name, func(t *testing.T) {
			record := object(t, vector["message"], "message")
			direction := text(t, vector["direction"], "direction")
			wantBytes := text(t, vector["canonical"], "canonical")

			nested, isRecord := nest(record).(Record)
			if !isRecord {
				t.Fatalf("nest produced %T, not a Record", nest(record))
			}

			var refusal *Refusal
			if _, isHeartbeat := nested["boot"]; isHeartbeat {
				_, refusal = ValidateHeartbeat(nested)
			} else {
				_, refusal = ValidateMessage(nested)
			}
			// The same verdict the decoded spelling gets, whichever it is: the
			// three cases the schema itself refuses must stay refused, and the
			// rest must stay accepted.
			if want, refusedBySchema := caseSchemaRefusal[name]; refusedBySchema {
				if refusal == nil {
					t.Errorf("the schema refuses this record (%s) and the nested spelling is accepted", want.why)
				} else if refusal.Code != want.code {
					t.Errorf("the nested spelling is refused %s, wanted %s (%s)", refusal, want.code, want.why)
				}
			} else {
				if refusal != nil {
					t.Errorf("a record whose objects are Records is refused %s, and it is the same record", refusal)
				}
				accepted++
			}

			got, err := CanonicalBytes(nested, direction)
			if err != nil {
				t.Fatalf("CanonicalBytes: %v", err)
			}
			if string(got) != wantBytes {
				t.Errorf("the nested spelling canonicalises differently:\n got %s\nwant %s", got, wantBytes)
			}
		})
	}
	if accepted != 21 {
		t.Fatalf("%d cases were accepted, not the 21 the schema accepts", accepted)
	}
}

func TestSchemaVectorDirectionPairs(t *testing.T) {
	file := loadVectors(t)
	key := text(t, file["key"], "key")
	pairs := list(t, file["direction_pairs"], "direction_pairs")
	if len(pairs) != 3 {
		t.Fatalf("vectors.json carries %d direction_pairs, not 3", len(pairs))
	}
	for _, entry := range pairs {
		pair := object(t, entry, "direction_pair")
		name := text(t, pair["name"], "name")
		t.Run(name, func(t *testing.T) {
			a := object(t, pair["a"], "a")
			b := object(t, pair["b"], "b")
			aBytes := canonicalOf(t, a)
			bBytes := canonicalOf(t, b)
			// The same record, under two directions, and nothing else
			// different: the direction alone has to change the bytes and the
			// tag, or a tag made for the outbox would verify in the inbox.
			if aBytes == bBytes {
				t.Errorf("both directions produce the bytes %s: the direction is not covered", aBytes)
			}
			if tagOf(key, aBytes) == tagOf(key, bBytes) {
				t.Errorf("both directions produce the tag %s: the direction is not covered", tagOf(key, aBytes))
			}
		})
	}
}

func TestSchemaVectorRefusals(t *testing.T) {
	file := loadVectors(t)
	refusals := list(t, file["refusals"], "refusals")
	if len(refusals) != 22 {
		t.Fatalf("vectors.json carries %d refusals, not 22: the file, not the replay, is what changed", len(refusals))
	}
	for _, entry := range refusals {
		vector := object(t, entry, "refusal")
		name := text(t, vector["name"], "name")
		t.Run(name, func(t *testing.T) {
			why := text(t, vector["why"], "why")
			record := object(t, vector["value"], "value")
			wantCode := Code(text(t, vector["code"], "code"))
			wantField := text(t, vector["field"], "field")

			var refusal *Refusal
			if text(t, vector["record"], "record") == "heartbeat" {
				_, refusal = ValidateHeartbeat(record)
			} else {
				_, refusal = ValidateMessage(record)
			}
			if refusal == nil {
				t.Fatalf("the value is accepted, and the vector file refuses it %s (%s) (%s)", wantCode, wantField, why)
			}
			if refusal.Code != wantCode || refusal.Field != wantField {
				t.Errorf("the refusal is %s, the vector file pins %s (%s) (%s)", refusal, wantCode, wantField, why)
			}
		})
	}
}

// TestValidatedFields pins what the binding is FOR: the typed message a later
// task reads the action out of is the record's own fields, checked.
func TestValidatedFields(t *testing.T) {
	record := Record{
		"id":     "m-0001",
		"sender": "phone",
		"text":   "cancel the run",
		"at":     number("1700000000000"),
		"mac":    strings.Repeat("a", MACMax),
		"kind":   "action",
		"agent":  "orchestrator",
		"action": map[string]any{
			"id":           "a-0001",
			"verb":         "cancel",
			"target":       map[string]any{"run": "20261018-125859-d3c8"},
			"confirmation": map[string]any{"id": "c-0001", "at": number("1700000000000")},
		},
	}
	message, refusal := ValidateMessage(record)
	if refusal != nil {
		t.Fatalf("a well-formed action is refused: %s", refusal)
	}
	if message.Kind != "action" || !message.HasAgent || message.Agent != "orchestrator" {
		t.Fatalf("the kind and the agent did not survive validation: %+v", message)
	}
	if message.Action == nil {
		t.Fatalf("the action record did not survive validation: %+v", message)
	}
	if message.Action.Verb != "cancel" || message.Action.ID != "a-0001" {
		t.Errorf("the action is %+v, wanted the id and verb the record names", message.Action)
	}
	if message.Action.Target.Run != "20261018-125859-d3c8" || message.Action.Target.IsRepo {
		t.Errorf("the run target is %+v, wanted the run the record names", message.Action.Target)
	}
	if message.Action.HasHeadSHA {
		t.Errorf("a cancel carries a head SHA: %+v", message.Action)
	}
	if message.Action.Confirmation.ID != "c-0001" || message.Action.Confirmation.At != 1700000000000 {
		t.Errorf("the confirmation is %+v", message.Action.Confirmation)
	}
	if message.HasAcked || message.HasReplyTo || message.Options != nil {
		t.Errorf("a field the record does not carry is reported present: %+v", message)
	}
}

// TestDecodeKeepsTheKeyCase is the plan's Red step (a): fold `Text` into `text`
// — a case-insensitive decode — and this test's refusal becomes an acceptance,
// because a record whose only `text` came from folding validates perfectly.
func TestDecodeKeepsTheKeyCase(t *testing.T) {
	record, err := Decode([]byte(`{"id":"m-0001","sender":"phone","Text":"run 42 finished","at":1700000000000,"mac":"` + strings.Repeat("a", MACMax) + `"}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if _, folded := record["text"]; folded {
		t.Fatalf("the decoder folded `Text` into `text`: the keys a record was written with are the keys it is covered with")
	}
	if _, kept := record["Text"]; !kept {
		t.Fatalf("the decoder lost the key `Text`: %v", record)
	}
	_, refusal := ValidateMessage(record)
	if refusal == nil || refusal.Code != CodeUnknownField || refusal.Field != "Text" {
		t.Fatalf("`Text` is refused as %v, wanted the unknown field it is", refusal)
	}
}

func TestDecodeRefusesAmbiguousDocuments(t *testing.T) {
	for _, tc := range []struct {
		name string
		json string
		want string
	}{
		{"a repeated key", `{"id":"m-1","id":"m-2"}`, "appears twice"},
		{"a second value", `{"id":"m-1"} {"id":"m-2"}`, "more than one JSON value"},
		{"a truncated object", `{"id":"m-1"`, "unexpected end of JSON input"},
		{"nesting past the bound", `{"a":{"a":{"a":{"a":{"a":{"a":{"a":{"a":{"a":1}}}}}}}}}`, "nested deeper"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode([]byte(tc.json))
			if err == nil {
				t.Fatalf("%s is decoded, and it has no single reading", tc.json)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the error is %q, wanted it to name %q", err, tc.want)
			}
		})
	}
	for _, notAnObject := range []string{`null`, `[]`, `"a string"`, `7`} {
		if _, err := Decode([]byte(notAnObject)); err != ErrNotAnObject {
			t.Errorf("%s decodes to %v, wanted ErrNotAnObject", notAnObject, err)
		}
		if _, refusal := ValidateMessage(nil); refusal == nil || refusal.Code != CodeNotAnObject {
			t.Errorf("a nil record is refused as %v, wanted %s", refusal, CodeNotAnObject)
		}
	}
}

// TestHTMLCharactersAreRaw is the plan's Red step (b): turn Go's HTML escaping
// back on and the bytes of the vector that carries `<`, `>`, `&` and a quote
// change under the tag.
func TestHTMLCharactersAreRaw(t *testing.T) {
	record := Record{
		"id":     "m-0001",
		"sender": "phone",
		"text":   `behind <b> & "quoted"`,
		"at":     number("1700000000000"),
		"mac":    strings.Repeat("a", MACMax),
	}
	got, err := CanonicalBytes(record, "inbox")
	if err != nil {
		t.Fatalf("CanonicalBytes: %v", err)
	}
	if !strings.Contains(string(got), `"text":"behind <b> & \"quoted\""`) {
		t.Errorf("the bytes are %s: the HTML characters must be written raw, as the phone writes them", got)
	}
	for _, escaped := range []string{`\u003c`, `\u003e`, `\u0026`} {
		if strings.Contains(string(got), escaped) {
			t.Errorf("the bytes carry %s: Go's HTML escaping is on", escaped)
		}
	}
}

// TestKeysSortByByteOrder is the other half of the plan's "each asserted, not
// assumed": the two keys where JavaScript's default UTF-16 order and the
// byte order a Go map produces disagree. The module's own vector pins the same
// pair; this one asserts the reason, so a port that reached for a different
// comparison fails naming it.
func TestKeysSortByByteOrder(t *testing.T) {
	keys := []string{"\uffff", "\U0001F600"}
	if got := strings.Compare(keys[0], keys[1]); got != -1 {
		t.Fatalf("Go's byte order puts %q and %q as %d: UTF-8 byte order is code-point order, and U+FFFF is the smaller", keys[0], keys[1], got)
	}
	sorted := []string{"\U0001F600", "\uffff"}
	sort.Strings(sorted)
	if sorted[0] != "\uffff" {
		t.Fatalf("sort.Strings orders %q first, wanted U+FFFF: the canonical form's order is byte order", sorted[0])
	}
	record := Record{"\uffff": int64(1), "\U0001F600": int64(2), "id": "m-0001", "mac": strings.Repeat("a", MACMax)}
	got, err := CanonicalBytes(record, "outbox")
	if err != nil {
		t.Fatalf("CanonicalBytes: %v", err)
	}
	// The whole byte string, so the order is pinned where it is produced and
	// not only where it is asserted: "direction" and "id" are ASCII and sort
	// first, and then U+FFFF (EF BF BF) before U+1F600 (F0 9F 98 80).
	if want := "{\"direction\":\"outbox\",\"id\":\"m-0001\",\"￿\":1,\"😀\":2}"; string(got) != want {
		t.Errorf("the keys are ordered %s, wanted %s", got, want)
	}
}

// TestNegativeZeroValidates pins the split the module's README spells out: the
// schema ACCEPTS -0 as an `at` and canonicalBytes refuses to spell it, so a
// record carrying one is a valid record that can be neither tagged nor
// verified. The two halves of this same question live in two different
// functions, which is exactly why it is worth pinning here.
func TestNegativeZeroValidates(t *testing.T) {
	file := loadVectors(t)
	var plain map[string]any
	for _, entry := range list(t, file["cases"], "cases") {
		vector := object(t, entry, "case")
		if text(t, vector["name"], "name") == "plain message" {
			plain = object(t, vector["message"], "message")
		}
	}
	if plain == nil {
		t.Fatal("the vector file carries no \"plain message\" case")
	}
	record := Record{}
	for key, value := range plain {
		record[key] = value
	}
	record["at"] = number("-0")

	if _, refusal := ValidateMessage(record); refusal != nil {
		t.Errorf("the schema accepts -0 as an `at` and this package refuses it %s", refusal)
	}
	if _, err := CanonicalBytes(record, "inbox"); err == nil {
		t.Error("-0 is canonicalised, and a record carrying it can be neither tagged nor verified")
	}
}

func TestCanonicalBytesRefusesValuesWithNoCanonicalForm(t *testing.T) {
	base := func() Record {
		return Record{"id": "m-0001", "mac": strings.Repeat("a", MACMax), "at": number("1700000000000")}
	}
	for _, tc := range []struct {
		name    string
		record  Record
		wantErr string
	}{
		{"a null", Record{"id": "m-0001", "landed_at": nil}, "never null"},
		{"an explicit null where a value belongs", Record{"at": nil, "id": "m-0001"}, "never null"},
		{"a float", Record{"at": number("1700000000000.5")}, "safe integer"},
		// Every literal that spells negative zero, not just the plainest one:
		// the module refuses all of them and a canonicaliser that let `-0.0`
		// through would write "0" and tag a record the phone will not verify.
		{"negative zero", Record{"at": number("-0")}, "-0"},
		{"negative zero with a fraction", Record{"at": number("-0.0")}, "-0"},
		{"negative zero with trailing zeroes", Record{"at": number("-0.00")}, "-0"},
		{"negative zero in exponent form", Record{"at": number("-0e0")}, "-0"},
		{"negative zero in upper exponent form", Record{"at": number("-0E0")}, "-0"},
		{"negative zero with a signed exponent", Record{"at": number("-0e+0")}, "-0"},
		{"negative zero in fraction and exponent form", Record{"at": number("-0.000e-5")}, "-0"},
		{"a number past the safe line", Record{"at": number("9007199254740993")}, "safe integer"},
		{"a stored direction", Record{"direction": "inbox"}, "the reader supplies it"},
		{"a lone separator", Record{"text": "a\u2028b"}, "U+2028"},
		{"invalid UTF-8", Record{"text": "a\xffb"}, "UTF-8"},
		{"a value no message carries", Record{"at": func() {}}, "not a value a message carries"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := base()
			for key, value := range tc.record {
				record[key] = value
			}
			_, err := CanonicalBytes(record, "inbox")
			if err == nil {
				t.Fatalf("%+v has canonical bytes, and the two languages do not spell it the same way", tc.record)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("the error is %q, wanted it to name %q", err, tc.wantErr)
			}
		})
	}
	if _, err := CanonicalBytes(base(), "nowhere"); err == nil {
		t.Error("a direction outside DIRECTIONS is accepted, and a tag made for one node must not be reused for another")
	}
	if _, err := CanonicalBytes(nil, "inbox"); err != ErrNotAnObject {
		t.Errorf("a nil record is signed: %v", err)
	}
}

// TestControlCharactersBothEncodersSpellAlike is the other half of that table,
// and it is here because the first half was wrong once. canonicalString used to
// refuse `\b` and `\f`, on the belief that Go's encoder writes `\u0008` and
// `\u000c` where JSON.stringify writes `\b` and `\f`; Go 1.27's json.Encoder
// with SetEscapeHTML(false) writes the short escape for exactly the five
// characters JSON.stringify writes one for and `\u00xx` for the rest of the
// control range, so a record carrying a backspace has ONE canonical form, and
// refusing it broke the byte-parity contract from this side.
//
// The premise is a fact about a Go release, so it is pinned rather than left in
// a comment: this test reads the escapes back out of the encoder the package
// signs with, across the whole control range. If a Go release changes one of
// them this fails here, where the answer is a one-line change, rather than in
// the field, where the answer is a message the phone tags and the consumer will
// not verify.
func TestControlCharactersBothEncodersSpellAlike(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  string
	}{
		{"a backspace", "a\bb", "\"a\\bb\""},
		{"a form feed", "a\fb", "\"a\\fb\""},
		{"a tab and a newline", "a\tb\nc", "\"a\\tb\\nc\""},
		{"a carriage return", "a\rb", "\"a\\rb\""},
		{"a NUL", "a\x00b", "\"a\\u0000b\""},
		{"a vertical tab", "a\x0bb", "\"a\\u000bb\""},
		{"a delete", "a\x7fb", "\"a\x7fb\""},
		{"a quote and a backslash", `a"\b`, `"a\"\\b"`},
		{"a solidus", "a/b", "\"a/b\""},
		{"an astral character", "a\U0001F600b", "\"a\U0001F600b\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := encodeJSONString(tc.value)
			if err != nil {
				t.Fatalf("encoding %q: %v", tc.value, err)
			}
			if got != tc.want {
				t.Errorf("%q is encoded %s, wanted %s", tc.value, got, tc.want)
			}
			// And a record carrying it has canonical bytes: the same string
			// through CanonicalBytes, with nothing refused on the way.
			record := plainMessage(t)
			record["text"] = tc.value
			bytes, err := CanonicalBytes(record, "inbox")
			if err != nil {
				t.Fatalf("a message carrying %q has no canonical bytes: %v", tc.value, err)
			}
			if !strings.Contains(string(bytes), `"text":`+tc.want) {
				t.Errorf("the body is written %s, wanted the text field to carry %s", bytes, tc.want)
			}
		})
	}

	// U+2028 and U+2029 are the class the two encoders DO spell differently, and
	// they are still refused: the rows above are control characters, and these
	// two are not control characters at all.
	separator := plainMessage(t)
	separator["text"] = "a\u2028b"
	if _, err := CanonicalBytes(separator, "inbox"); err == nil {
		t.Error("a record carrying U+2028 has canonical bytes, and Go escapes it where JSON.stringify does not")
	}
}

// TestSafeIntegerLineIsTheSameForEveryGoNumericType holds the number line at
// every spelling Go can hand the validators: the json.Number the wire carries,
// and the int, int64 and float64 an `encoding/json` caller builds. The module's
// own tests pin the line — `index.test.js` declares `at: 2 ** 53` a
// bad-timestamp and `canonicalBytes` throwing for it — so a spelling that
// crosses it here is the same number with two verdicts, one per end.
func TestSafeIntegerLineIsTheSameForEveryGoNumericType(t *testing.T) {
	const max = int64(1)<<53 - 1
	for _, tc := range []struct {
		name   string
		safe   any
		unsafe []any
	}{
		{"a decoded literal", number("9007199254740991"), []any{number("9007199254740992"), number("1152921504606846976")}},
		{"an int", int(max), []any{int(1) << 53, int(1) << 60}},
		{"an int64", int64(max), []any{int64(1) << 53, int64(1) << 60}},
		{"a float64", float64(max), []any{float64(int64(1) << 53), float64(int64(1) << 60)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := plainMessage(t)
			record["at"] = tc.safe
			if _, refusal := ValidateMessage(record); refusal != nil {
				t.Errorf("the largest safe integer is refused %s, and Number.isSafeInteger accepts it", refusal)
			}
			if _, err := CanonicalBytes(record, "inbox"); err != nil {
				t.Errorf("the largest safe integer has no canonical bytes: %v", err)
			}
			for _, value := range tc.unsafe {
				record := plainMessage(t)
				record["at"] = value
				_, refusal := ValidateMessage(record)
				if refusal == nil || refusal.Code != CodeBadTimestamp {
					t.Errorf("%v (%T) is refused %v, wanted bad-timestamp: the number line is one line", value, value, refusal)
				}
				if _, err := CanonicalBytes(record, "inbox"); err == nil {
					t.Errorf("%v (%T) has canonical bytes, and the module's serialiser throws for it", value, value)
				}
			}
		})
	}
}

// TestANumberPastTheDoublesIsStillANumber keeps the refusal for a literal the
// double cannot hold on the right side of the type question: `1e400` is a JSON
// number the decoder keeps as a literal, it parses to +Infinity, and Infinity
// is a number that is not a safe integer — which is what JSON.parse gives
// JavaScript for the same bytes, and therefore what the module answers
// (bad-timestamp, not wrong-type).
func TestANumberPastTheDoublesIsStillANumber(t *testing.T) {
	record := plainMessage(t)
	record["at"] = number("1e400")
	_, refusal := ValidateMessage(record)
	if refusal == nil || refusal.Code != CodeBadTimestamp {
		t.Errorf("1e400 is refused %v, wanted bad-timestamp", refusal)
	}
	if _, err := CanonicalBytes(record, "inbox"); err == nil {
		t.Error("1e400 is canonicalised, and no end can spell the double it is")
	}
}

// TestFloatsAreRefusedInBothHalves states the type decision the two halves of
// this package share: the numeric types a record may carry are json.Number
// (the wire), int, int64 and float64 — an `encoding/json` caller's own types —
// and nothing else. float32 is not among them: its 24-bit mantissa cannot hold
// an epoch millisecond, JavaScript has no counterpart to agree with, and it is
// refused as the wrong type rather than bad-timestamp because it is not a
// number this protocol speaks. A float that is not an integer is the module's
// bad-timestamp.
func TestFloatsAreRefusedInBothHalves(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  Code
	}{
		{"a float32", float32(1700000000000), CodeWrongType},
		{"a fractional float64", 1700000000000.5, CodeBadTimestamp},
		{"a fractional literal", number("1700000000000.5"), CodeBadTimestamp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := plainMessage(t)
			record["at"] = tc.value
			_, refusal := ValidateMessage(record)
			if refusal == nil || refusal.Code != tc.want {
				t.Errorf("%v (%T) is refused %v, wanted %s", tc.value, tc.value, refusal, tc.want)
			}
			if _, err := CanonicalBytes(record, "inbox"); err == nil {
				t.Errorf("%v (%T) is canonicalised, and the other half of the package refuses it", tc.value, tc.value)
			}
		})
	}
}

// TestAnythingValidatedCanBeCanonicalised is the property the two halves owe
// each other: a record ValidateMessage accepts is a record CanonicalBytes can
// serialise. Anything else is a record with two verdicts, and the second one
// arrives only when the tag is made. Every numeric type this package accepts
// goes into every position that takes a number, because a type accepted in one
// field and refused in another is the same disagreement one field smaller.
func TestAnythingValidatedCanBeCanonicalised(t *testing.T) {
	const max = int64(1)<<53 - 1
	for _, spelling := range []struct {
		name  string
		value any
	}{
		{"a decoded literal", number("1700000000000")},
		{"an int", 1700000000000},
		{"an int64", int64(1700000000000)},
		{"a float64", float64(1700000000000)},
		{"a safe float64 past the exact line", float64(max)},
	} {
		for _, where := range []struct {
			name   string
			vector string
			put    func(Record)
		}{
			{"`at`", "plain message", func(r Record) { r["at"] = spelling.value }},
			{"the confirmation's `at`", "action: merge a pull request", func(r Record) {
				dig(t, r, "action", "confirmation")["at"] = spelling.value
			}},
			{"the target's `pr`", "action: merge a pull request", func(r Record) {
				dig(t, r, "action", "target")["pr"] = spelling.value
			}},
		} {
			t.Run(where.name+" spelled as "+spelling.name, func(t *testing.T) {
				record := vectorCase(t, where.vector)
				where.put(record)
				acceptedAndSerialisable(t, record)
			})
		}
	}
	// The strings a Go caller can build, including the one the validator must
	// refuse because no encoder would spell it: a record accepted here and
	// refused at the tag is a message that can never be sent.
	for _, spelling := range []struct {
		name  string
		value string
	}{
		{"astral characters", "a\U0001F600b"},
		{"a literal replacement character", "a�b"},
		{"a tab in a body", "a\tb"},
	} {
		t.Run("`text` carrying "+spelling.name, func(t *testing.T) {
			record := plainMessage(t)
			record["text"] = spelling.value
			acceptedAndSerialisable(t, record)
		})
	}
}

// TestTheValidatorRefusesStringsItCannotSign is the same property from the
// other side. Go cannot hold a lone surrogate as a character, so its Go
// spelling is invalid UTF-8 — the one string CanonicalBytes refuses to sign,
// because Go's encoder writes U+FFFD for it where the phone's keeps the half
// it was given. The module refuses a lone surrogate in a value as
// bad-character and in a key as bad-key, so the validator refuses it with the
// same codes rather than passing on a record that can never be tagged.
func TestTheValidatorRefusesStringsItCannotSign(t *testing.T) {
	const lone = "a\xed\xa0\x80b" // U+D800 in WTF-8: what a Go string can spell of it
	for _, tc := range []struct {
		name  string
		field string
		value any
		want  Code
	}{
		{"a lone surrogate in a body", "text", lone, CodeBadCharacter},
		{"a lone surrogate in a sender", "sender", lone, CodeBadCharacter},
		{"a lone surrogate in an id", "id", "m-\xed\xa0\x80", CodeBadKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := plainMessage(t)
			record[tc.field] = tc.value
			_, refusal := ValidateMessage(record)
			if refusal == nil || refusal.Code != tc.want {
				t.Errorf("it is refused %v, wanted %s", refusal, tc.want)
			}
			if _, err := CanonicalBytes(record, "inbox"); err == nil {
				t.Error("it is canonicalised, and no encoder can spell it")
			}
		})
	}
	// U+FFFD itself is a character the phone can speak: a record carrying a
	// literal one is not the decoder's laundering of anything.
	record := plainMessage(t)
	record["text"] = "a�b"
	acceptedAndSerialisable(t, record)
}

// TestALoneSurrogateEscapeReachesTheValidator is round 4's finding 1, and the
// replacement for round 3's decode-time refusal.
//
// encoding/json turns an unpaired surrogate escape into U+FFFD and says
// nothing, so a decoder that refuses it itself has to pick a code from where
// the string sits — bad-character in a value, bad-key in a key name — and that
// is not the module's rule. The module's rule is by FIELD: keyField answers
// bad-key for `id`, `reply_to`, `action.id` and `action.confirmation.id`
// (index.js:398, 450, 749, 888), stringField answers bad-character for the
// body, the sender, an option, an agent's name and — through identityField —
// an agent's id (index.js:625-671, 684, 712), and a key name that is not a
// field is refused unknown-field before any character in it is read
// (index.js:290-300). This package's validators take the same route, because
// they are the same question.
//
// So the decoder does not choose a code at all. It keeps the fault in the one
// spelling Go can carry it in — the three WTF-8 bytes of the escaped code unit,
// which is invalid UTF-8 — and hands it to ValidateMessage, which knows the
// field and answers with the module's own code. That is what this test pins,
// field by field, on the module's own records: each document is the vector for
// its kind with one substitution, so every other field is one the module
// already agrees with. It also pins that CanonicalBytes refuses each, so no tag
// is ever computed over a laundered string.
//
// Round 4's finding listed `agents[i].id` among the bad-key fields; the module
// says otherwise — index.js:712 sends it to identityField, which sends it to
// stringField — and this package agrees, so the row below is bad-character.
func TestALoneSurrogateEscapeReachesTheValidator(t *testing.T) {
	const (
		highHalf = "\xed\xa0\x80" // U+D800 in WTF-8: the three bytes a Go string spells of it
		lowHalf  = "\xed\xb8\x80" // U+DE00
	)
	for _, tc := range []struct {
		name        string
		caseName    string
		needle      string
		replacement string
		want        Code
		wantField   string
	}{
		{"in a body", "plain message", `"text":"run 42 finished"`, `"text":"a\uD800b"`, CodeBadCharacter, "text"},
		{"a low half in a body", "plain message", `"text":"run 42 finished"`, `"text":"\uDE00"`, CodeBadCharacter, "text"},
		{"a high half at the end of a body", "plain message", `"text":"run 42 finished"`, `"text":"ab\uD83D"`, CodeBadCharacter, "text"},
		{"a lone half spelled in lowercase hex", "plain message", `"text":"run 42 finished"`, `"text":"a\ud800b"`, CodeBadCharacter, "text"},
		{"a lone half after a pair", "plain message", `"text":"run 42 finished"`, `"text":"\ud83d\ude00\ud800"`, CodeBadCharacter, "text"},
		{"a lone half after an escaped backslash", "plain message", `"text":"run 42 finished"`, `"text":"\\\ud800"`, CodeBadCharacter, "text"},
		{"in an id, after the keys are known", "plain message", `"id":"m-0001"`, `"id":"m-\uD800"`, CodeBadKey, "id"},
		{"in a reply_to", "answer", `"reply_to":"m-0000"`, `"reply_to":"m-\uDE00"`, CodeBadKey, "reply_to"},
		{"in an action id", "action: cancel a run", `"id":"a-0001"`, `"id":"a-\uD800"`, CodeBadKey, "action.id"},
		{"in a confirmation id", "action: cancel a run", `"id":"c-0001"`, `"id":"c-\uD800"`, CodeBadKey, "action.confirmation.id"},
		{"in an option", "prompt with options", `"options":["merge it","leave it"]`, `"options":["merge it","\uD800"]`, CodeBadCharacter, "options[1]"},
		{"in a key name that is not a field", "plain message", `"id":"m-0001"`, `"\uD800":0,"id":"m-0001"`, CodeUnknownField, highHalf},
		{"in a key name inside an action", "action: cancel a run", `"verb":"cancel"`, `"verb":"cancel","\uDE00":0`, CodeUnknownField, "action." + lowHalf},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record, err := Decode(surrogateDocument(t, tc.caseName, tc.needle, tc.replacement))
			if err != nil {
				t.Fatalf("the decoder refused the document itself: %v", err)
			}
			if !carriesInvalidUTF8(record) {
				t.Fatal("the fault was laundered: nothing in the record spells the code unit the document escaped")
			}
			_, refusal := ValidateMessage(record)
			if refusal == nil {
				t.Fatal("the validator accepted a string no encoder can spell, and it would have been signed")
			}
			if refusal.Code != tc.want || refusal.Field != tc.wantField {
				t.Errorf("it is refused %s at %q, wanted %s at %q", refusal.Code, refusal.Field, tc.want, tc.wantField)
			}
			if _, err := CanonicalBytes(record, "inbox"); err == nil {
				t.Error("it is canonicalised: a tag would cover bytes of a string the phone never wrote")
			}
		})
	}

	// A heartbeat is a different record with a different field set, judged by the
	// validator that knows it: an agent's id goes through identityField, and so
	// through stringField, so the code there is bad-character too.
	t.Run("in an agent's id of a heartbeat", func(t *testing.T) {
		record, err := Decode(surrogateDocument(t, "heartbeat", `"id":"orchestrator"`, `"id":"\uD800"`))
		if err != nil {
			t.Fatalf("the decoder refused the document itself: %v", err)
		}
		if !carriesInvalidUTF8(record) {
			t.Fatal("the fault was laundered: nothing in the record spells the code unit the document escaped")
		}
		_, refusal := ValidateHeartbeat(record)
		if refusal == nil {
			t.Fatal("the validator accepted a string no encoder can spell, and it would have been signed")
		}
		if refusal.Code != CodeBadCharacter || refusal.Field != "agents[0].id" {
			t.Errorf("it is refused %s at %q, wanted %s at %q", refusal.Code, refusal.Field, CodeBadCharacter, "agents[0].id")
		}
	})

	// And the escapes that are not a fault, which is what makes the scan worth
	// having rather than a blanket refusal of `\u`: a pair is one character, an
	// escaped backslash is six characters of ordinary text, and a literal
	// U+FFFD is a character the phone can speak, not this decoder's laundering
	// of anything.
	for _, tc := range []struct {
		name     string
		document string
		field    string
		want     any
	}{
		{"a paired surrogate", `{"text":"\uD83D\ude00"}`, "text", "\U0001F600"},
		{"a pair spelled in lowercase hex", `{"text":"\ud83d\ude00"}`, "text", "\U0001F600"},
		{"two paired surrogates", `{"text":"\uD83D\ude00\uD83D\ude01"}`, "text", "\U0001F600\U0001F601"},
		{"a paired surrogate in a key", `{"\uD83D\ude00":"x"}`, "\U0001F600", "x"},
		{"a pair and then ordinary text", `{"text":"\uD83D\uDE00x"}`, "text", "\U0001F600x"},
		{"an escaped backslash before u", `{"text":"\\uD800"}`, "text", `\uD800`},
		{"two escaped backslashes and two u's", `{"text":"\\\\uD83D\\\\uDE00"}`, "text", `\\uD83D\\uDE00`},
		{"an escape that is not a surrogate", `{"text":"\u00e9"}`, "text", "\u00e9"},
		{"an escaped quote and a control character", `{"text":"a\"b\nc"}`, "text", "a\"b\nc"},
		{"a literal replacement character", `{"text":"a` + "�" + `b"}`, "text", "a�b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record, err := Decode([]byte(tc.document))
			if err != nil {
				t.Fatalf("the decoder refused it: %v", err)
			}
			if got := record[tc.field]; got != tc.want {
				t.Errorf("it decoded to %v, wanted %v", got, tc.want)
			}
			if carriesInvalidUTF8(record) {
				t.Error("it decoded to a string Go cannot encode: the unquoter invented a fault")
			}
		})
	}
}

// TestABoundCountsALoneSurrogateAsOneCodeUnit is the second finding of this
// round. utf16Len walked the string with `range`, which yields one U+FFFD per
// invalid byte, so the three WTF-8 bytes of a lone surrogate counted as three
// UTF-16 code units where the module's `.length` counts one: 34 of them in
// `id` (102 bytes) came back too-long where the module answers bad-key, and
// 1366 in `text` came back too-long where the module answers bad-character.
// Both ends refuse, so it was never a permissiveness hole — it is the code
// parity ValidateMessage's own contract names ("the same code and the same
// field at fault"), and a consumer told too-long goes looking for a length
// problem that is not there.
//
// The bounds are still applied: a string of lone surrogates past the bound in
// CODE UNITS is too-long in both languages, which is what makes this a
// counting fix rather than a second exception to the bound.
func TestABoundCountsALoneSurrogateAsOneCodeUnit(t *testing.T) {
	const lone = "\xed\xa0\x80" // U+D800 in WTF-8: one code unit, three bytes
	for _, tc := range []struct {
		name  string
		field string
		value string
		want  Code
	}{
		{"34 lone halves in an id are 34 code units", "id", strings.Repeat(lone, 34), CodeBadKey},
		{"one lone half in an id", "id", "m-" + lone, CodeBadKey},
		{"101 lone halves in an id are past the bound", "id", strings.Repeat(lone, IDMax+1), CodeTooLong},
		{"1366 lone halves in a body are 1366 code units", "text", strings.Repeat(lone, 1366), CodeBadCharacter},
		{"4097 lone halves in a body are past the bound", "text", strings.Repeat(lone, TextMax+1), CodeTooLong},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := plainMessage(t)
			record[tc.field] = tc.value
			_, refusal := ValidateMessage(record)
			if refusal == nil || refusal.Code != tc.want {
				t.Errorf("%d bytes is refused %v, wanted %s: the bound is a count of code units, not of bytes", len(tc.value), refusal, tc.want)
			}
		})
	}

	// And the same count for a string that is valid UTF-8: an astral character
	// is two code units, which is the half of utf16Len that was already right.
	record := plainMessage(t)
	record["text"] = strings.Repeat("\U0001F600", 2048) // 4096 code units, exactly the bound
	if _, refusal := ValidateMessage(record); refusal != nil {
		t.Errorf("4096 code units of astral characters are refused %s, and the module counts them 4096", refusal)
	}
	record["text"] = strings.Repeat("\U0001F600", 2048) + "x"
	if _, refusal := ValidateMessage(record); refusal == nil || refusal.Code != CodeTooLong {
		t.Errorf("4097 code units are refused %v, wanted %s", refusal, CodeTooLong)
	}
}

// TestTheUnquoterDecodesWhatItKeeps pins the one function that runs only for a
// document that spells an unpaired surrogate. The rest of such a string is
// still the module's string — a pair is one character, `\\"` is a quote, `\n` is
// a newline — and only the lone half is kept as the three WTF-8 bytes Go can
// carry. A pair written as its two halves would be a different string, and
// these are the cases where the difference is visible.
func TestTheUnquoterDecodesWhatItKeeps(t *testing.T) {
	for _, tc := range []struct {
		name    string
		literal string
		want    string
	}{
		{"a lone high half", `"\uD800"`, "\xed\xa0\x80"},
		{"a lone low half", `"\udc00"`, "\xed\xb0\x80"},
		{"a pair beside a lone half", `"\uD83D\uDE00\uD800"`, "\U0001F600\xed\xa0\x80"},
		{"a lone half beside a pair", `"\uD800\uD83D\uDE00"`, "\xed\xa0\x80\U0001F600"},
		{"a high half before an ordinary escape", `"\uD83D\u00e9"`, "\xed\xa0\xbd\xc3\xa9"},
		{"escapes beside a lone half", `"a\\b\"c\nd\uD800"`, "a\\b\"c\nd\xed\xa0\x80"},
		{"a raw character beside a lone half", `"é\uD800"`, "é\xed\xa0\x80"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := unquoteKeepingLoneSurrogates([]byte(tc.literal))
			if err != nil {
				t.Fatalf("unquoting %s: %v", tc.literal, err)
			}
			if got != tc.want {
				t.Errorf("it decoded to %q, wanted %q", got, tc.want)
			}
		})
	}

	// A literal no JSON document can hold is an error — and, the reason for the
	// error rather than the `break` that used to be here, it TERMINATES. Both
	// shapes of that bug were wrong: a `break` in the loop body left the loop
	// with the string half decoded, and a `break` inside the switch — the
	// "half an escape" row below — never advanced i and spun on the same byte
	// until the test binary timed out. Mutation-proving that one takes
	// `-timeout`, which is exactly why the code returns an error instead.
	for _, tc := range []struct {
		literal string
		wantErr string
	}{
		{`no quote at all`, "no opening quote"},
		{`"a`, "no closing quote"},
		{`"a` + "\\", "no closing quote"},
		{`"a` + "\\" + `"`, "lone backslash"},  // a backslash with only the closing quote after it
		{`"a` + "\\" + `u00"`, "half-written"}, // half an escape
		{`"a` + "\\" + `q"`, "JSON grammar"},   // an escape JSON does not have
	} {
		t.Run("refused: "+tc.literal, func(t *testing.T) {
			got, err := unquoteKeepingLoneSurrogates([]byte(tc.literal))
			if err == nil {
				t.Fatalf("%q unquoted to %q, and a document cannot hold it", tc.literal, got)
			}
			// Each of those is its own branch, so each is named: a test that
			// only asked "did it fail" would pass with three of the four checks
			// deleted, which is what a mutation proved before this table had a
			// `wantErr` column.
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%q failed with %q, wanted it to name %q", tc.literal, err, tc.wantErr)
			}
		})
	}
}

// surrogateDocument is the module's own record for a case, as JSON, with one
// substitution applied — so every field but the one under test is the module's
// own and the refusal that comes back is the one this test is about. The
// needle must still be there: if a vector moves, this test says so rather than
// quietly testing something else.
func surrogateDocument(t *testing.T, caseName, needle, replacement string) []byte {
	t.Helper()
	document, err := json.Marshal(map[string]any(vectorCase(t, caseName)))
	if err != nil {
		t.Fatalf("marshalling the %q vector: %v", caseName, err)
	}
	if !strings.Contains(string(document), needle) {
		t.Fatalf("the %q vector no longer carries %q: this test's needle is stale", caseName, needle)
	}
	return []byte(strings.Replace(string(document), needle, replacement, 1))
}

// carriesInvalidUTF8 reports whether any string in a decoded record — a value,
// a list entry or a key — is one Go cannot encode. That is the shape a lone
// surrogate escape takes once the decoder has stopped laundering it, and the
// shape ValidateMessage and CanonicalBytes both refuse.
func carriesInvalidUTF8(value any) bool {
	switch v := value.(type) {
	case string:
		return !utf8.ValidString(v)
	case Record:
		return carriesInvalidUTF8(map[string]any(v))
	case map[string]any:
		for key, entry := range v {
			if !utf8.ValidString(key) || carriesInvalidUTF8(entry) {
				return true
			}
		}
	case []any:
		for _, entry := range v {
			if carriesInvalidUTF8(entry) {
				return true
			}
		}
	}
	return false
}

// TestEveryRefusalCodeMatchesTheModule is finding 4: Code's own doc says a
// reworded refusal should be a failing test here, and eight of the eighteen
// codes were not held to anything. The five vocabularies are bound to the
// module by TestBoundsMatchTheSharedModule; the codes are bound here, by
// reading REFUSALS out of index.js and requiring the same set.
func TestEveryRefusalCodeMatchesTheModule(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "shared", "mailbox-schema", "index.js"))
	if err != nil {
		t.Fatalf("reading the module: %v", err)
	}
	block := jsRefusals.FindStringSubmatch(string(source))
	if block == nil {
		t.Fatal("the module no longer declares REFUSALS: this package's codes have nothing to be held to")
	}
	declared := map[string]string{}
	for _, m := range jsRefusal.FindAllStringSubmatch(block[1], -1) {
		declared[m[1]] = m[2]
	}
	if len(declared) != 18 {
		t.Errorf("the module declares %d refusal codes: a new one is a code to bind, not to ignore", len(declared))
	}
	if len(Codes) != 18 {
		t.Errorf("this package speaks %d codes, the module declares %d", len(Codes), len(declared))
	}
	want := make([]string, 0, len(declared))
	for _, value := range declared {
		want = append(want, value)
	}
	got := make([]string, 0, len(Codes))
	seen := map[Code]bool{}
	for _, code := range Codes {
		if seen[code] {
			t.Errorf("the code %s is listed twice in Codes", code)
		}
		seen[code] = true
		got = append(got, string(code))
	}
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("this package speaks  %v\nthe module refuses with %v", got, want)
	}
}

// acceptedAndSerialisable is the property itself, in one place: the record
// must validate, and the bytes must then exist.
func acceptedAndSerialisable(t *testing.T, record Record) {
	t.Helper()
	if _, refusal := ValidateMessage(record); refusal != nil {
		t.Fatalf("the record is refused %s, and the property is about the records that are accepted", refusal)
	}
	if _, err := CanonicalBytes(record, "inbox"); err != nil {
		t.Errorf("ValidateMessage accepts this record and CanonicalBytes cannot serialise it: %v", err)
	}
}

// plainMessage is the vector file's plain message, freshly decoded: a valid
// record with one field of each type.
func plainMessage(t *testing.T) Record {
	t.Helper()
	return vectorCase(t, "plain message")
}

// vectorCase is one case's record as the file carries it, decoded by the
// package's own decoder. Each call re-reads the file, so a test that mutates
// what it is given cannot reach another test's record.
func vectorCase(t *testing.T, name string) Record {
	t.Helper()
	for _, entry := range list(t, loadVectors(t)["cases"], "cases") {
		vector := object(t, entry, "case")
		if text(t, vector["name"], "name") == name {
			return Record(object(t, vector["message"], "message"))
		}
	}
	t.Fatalf("the vector file carries no %q case", name)
	return nil
}

// dig returns the object a record holds at a path, so a test can put a value
// inside a nested record.
func dig(t *testing.T, record Record, keys ...string) map[string]any {
	t.Helper()
	here := map[string]any(record)
	for i, key := range keys {
		next, ok := here[key].(map[string]any)
		if !ok {
			t.Fatalf("%s is not an object in the vector", strings.Join(keys[:i+1], "."))
		}
		here = next
	}
	return here
}

// TestBoundsMatchTheSharedModule is M9's binding for the values Go cannot
// import: every bound, every closed vocabulary, every character class and every
// refusal code this package declares is read back out of
// shared/mailbox-schema/index.js and compared, so an edit to either side is a
// failing test rather than a message the phone accepts and the consumer refuses.
//
// The character classes are here because they are the security-relevant half of
// the shared vocabulary: RUN_PATTERN and REPO_PATTERN carry the leading `-` and
// `.` refusals that keep a run id out of `fugaro cancel`'s flag position and a
// repository out of `gh pr merge --repo`'s option position, and a copy that
// drifted would be a command built from a string the module would have refused.
// The six of them are counted as well as compared, so a class the module grows
// is a class to bind.
func TestBoundsMatchTheSharedModule(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "shared", "mailbox-schema", "index.js"))
	if err != nil {
		t.Fatalf("reading the module: %v", err)
	}
	declared := map[string]int{}
	for _, m := range jsBound.FindAllStringSubmatch(string(source), -1) {
		value, err := strconv.Atoi(m[2])
		if err != nil {
			t.Fatalf("reading %s from the module: %v", m[1], err)
		}
		declared[m[1]] = value
	}
	for name, want := range map[string]int{
		"ID_MAX": IDMax, "SENDER_MAX": SenderMax, "TEXT_MAX": TextMax,
		"AGENT_MAX": AgentMax, "AGENT_NAME_MAX": AgentNameMax, "AGENTS_MAX": AgentsMax,
		"OPTIONS_MAX": OptionsMax, "OPTION_MAX": OptionMax, "TARGET_MAX": TargetMax,
		"SHA_MIN": SHAMin, "SHA_MAX": SHAMax, "MAC_MAX": MACMax, "BOOT_MAX": BootMax,
	} {
		got, ok := declared[name]
		if !ok {
			t.Errorf("the module no longer declares %s: this package's bound has nothing to be held to", name)
			continue
		}
		if got != want {
			t.Errorf("%s is %d in this package and %d in the module: the two ends disagree about a bound", name, want, got)
		}
	}
	if len(declared) != 13 {
		t.Errorf("the module declares %d numeric bounds and this test knows 13: a new one is a bound to bind, not to ignore", len(declared))
	}

	vocabulary := map[string][]string{
		"FIELDS":          Fields,
		"REQUIRED_FIELDS": RequiredFields,
		"KINDS":           Kinds,
		"ACTIONS":         Actions,
		"DIRECTIONS":      Directions,
	}
	for _, m := range jsVocabulary.FindAllStringSubmatch(string(source), -1) {
		want, known := vocabulary[m[1]]
		if !known {
			t.Errorf("the module declares the vocabulary %s, which this package does not bind", m[1])
			continue
		}
		delete(vocabulary, m[1])
		var got []string
		for _, item := range jsString.FindAllStringSubmatch(m[2], -1) {
			got = append(got, item[1])
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s is %v in the module and %v here: a vocabulary the two ends disagree about is two protocols", m[1], got, want)
		}
	}
	for name := range vocabulary {
		t.Errorf("the module no longer declares %s: this package's list has nothing to be held to", name)
	}

	// AT_MAX is not a literal the module writes down: it is
	// Number.MAX_SAFE_INTEGER, so the binding is two-sided — the module still
	// draws its line there, and this package's line is that constant's value.
	if !jsMaxSafeInteger.MatchString(string(source)) {
		t.Error("the module no longer sets AT_MAX to Number.MAX_SAFE_INTEGER: this package's number line has nothing to be held to")
	}
	if maxSafeInteger != 9007199254740991 {
		t.Errorf("maxSafeInteger is %d, and Number.MAX_SAFE_INTEGER is 9007199254740991", maxSafeInteger)
	}

	classes := map[string]string{
		"IDENTITY_PATTERN": identityPattern.String(),
		"RUN_CHARS":        runChars.String(),
		"RUN_PATTERN":      runPattern.String(),
		"REPO_CHARS":       repoChars.String(),
		"REPO_PATTERN":     repoPattern.String(),
		"HEX_PATTERN":      hexPattern.String(),
	}
	for _, m := range jsClass.FindAllStringSubmatch(string(source), -1) {
		want, known := classes[m[1]]
		if !known {
			t.Errorf("the module declares the character class %s, which this package does not bind", m[1])
			continue
		}
		delete(classes, m[1])
		// JavaScript spells a slash inside a literal as `\/`; the language
		// around it is not part of the class.
		if got := strings.ReplaceAll(m[2], `\/`, "/"); got != want {
			t.Errorf("%s is /%s/ in the module and /%s/ here: a class the two ends disagree about is a value one end runs and the other refuses", m[1], got, want)
		}
	}
	for name := range classes {
		t.Errorf("the module no longer declares the character class %s: this package's pattern has nothing to be held to", name)
	}
	if len(jsClass.FindAllStringSubmatch(string(source), -1)) != 6 {
		t.Errorf("the module declares %d character classes and this package binds 6: a new one is a class to bind, not to ignore", len(jsClass.FindAllStringSubmatch(string(source), -1)))
	}

	// The characters a key may not contain: the set is written as a Set
	// literal, so it is read the way the vocabularies are.
	set := jsSet.FindStringSubmatch(string(source))
	if set == nil {
		t.Fatal("the module no longer declares FORBIDDEN_KEY_CHARS as a Set: this package's set has nothing to be held to")
	}
	// A set has no order, so both sides are sorted before they are compared.
	var forbidden []string
	for _, item := range jsString.FindAllStringSubmatch(set[1], -1) {
		forbidden = append(forbidden, item[1])
	}
	var here []string
	for r := range forbiddenKeyChars {
		here = append(here, string(r))
	}
	sort.Strings(forbidden)
	sort.Strings(here)
	if strings.Join(forbidden, ",") != strings.Join(here, ",") {
		t.Errorf("FORBIDDEN_KEY_CHARS is %v in the module and %v here: a key one end refuses is a key the other would store", forbidden, here)
	}
}

var (
	jsBound      = regexp.MustCompile(`(?m)^export const ([A-Z_]+) = (\d+);$`)
	jsVocabulary = regexp.MustCompile(`(?s)export const ([A-Z_]+) = Object\.freeze\(\[(.*?)\]\)`)
	jsString     = regexp.MustCompile(`'([^']*)'`)
	// REFUSALS is an object, not a list: the name is the family and the value
	// is the code the two ends speak, which is the half this package binds.
	jsRefusals = regexp.MustCompile(`(?s)export const REFUSALS = Object\.freeze\(\{(.*?)\n\}\);`)
	jsRefusal  = regexp.MustCompile(`(?m)^\s*([A-Z_]+): '([^']*)',$`)
	// The character classes are module-private consts, and AT_MAX is the one
	// bound declared as a JavaScript constant rather than a number.
	jsClass          = regexp.MustCompile(`(?m)^const ([A-Z_]+) = /(.+)/;$`)
	jsMaxSafeInteger = regexp.MustCompile(`(?m)^export const AT_MAX = Number\.MAX_SAFE_INTEGER;$`)
	jsSet            = regexp.MustCompile(`(?s)const FORBIDDEN_KEY_CHARS = new Set\(\[(.*?)\]\);`)
)

// tagOf is the tag HMAC-SHA-256 produces over canonical bytes: mac.js's own
// composition, written here rather than in the package because the package's
// tag function is T2's (see the header comment).
func tagOf(key, canonical string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}

// canonicalOf is one side of a direction pair: the bytes the file pins, or a
// failure if Go produces others.
func canonicalOf(t *testing.T, side map[string]any) string {
	t.Helper()
	direction := text(t, side["direction"], "direction")
	record := object(t, side["message"], "message")
	got, err := CanonicalBytes(record, direction)
	if err != nil {
		t.Fatalf("CanonicalBytes(%s): %v", direction, err)
	}
	if want := text(t, side["canonical"], "canonical"); string(got) != want {
		t.Fatalf("the bytes for %s are %s, the vector file pins %s", direction, got, want)
	}
	if want := text(t, side["mac"], "mac"); tagOf("not-a-secret-test-key-0001", string(got)) != want {
		t.Fatalf("the tag for %s is not the one the vector file pins", direction)
	}
	return string(got)
}

// loadVectors reads vectors.json with the package's own decoder, so the fixture
// and the wire are read the same way.
func loadVectors(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Fatalf("reading %s: %v", vectorsPath, err)
	}
	record, err := Decode(data)
	if err != nil {
		t.Fatalf("decoding %s: %v", vectorsPath, err)
	}
	return record
}

// object, list and text are the fixture's three shapes, each failing loudly
// rather than panicking when the file is not what this test expects: a broken
// fixture must not look like a passing replay.
func object(t *testing.T, value any, where string) map[string]any {
	t.Helper()
	got, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s is %T, not an object", where, value)
	}
	return got
}

func list(t *testing.T, value any, where string) []any {
	t.Helper()
	got, ok := value.([]any)
	if !ok {
		t.Fatalf("%s is %T, not a list", where, value)
	}
	return got
}

func text(t *testing.T, value any, where string) string {
	t.Helper()
	got, ok := value.(string)
	if !ok {
		t.Fatalf("%s is %T, not a string", where, value)
	}
	return got
}

// number is a JSON number literal for a record built in Go, which is what a
// record decoded from the wire carries.
func number(literal string) any { return json.Number(literal) }
