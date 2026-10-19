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
		{"negative zero", Record{"at": number("-0")}, "-0"},
		{"a number past the safe line", Record{"at": number("9007199254740993")}, "safe integer"},
		{"a stored direction", Record{"direction": "inbox"}, "the reader supplies it"},
		{"a lone separator", Record{"text": "a\u2028b"}, "U+2028"},
		{"a backspace", Record{"text": "a\bb"}, `\b`},
		{"a form feed", Record{"text": "a\fb"}, `\f`},
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

// TestBoundsMatchTheSharedModule is M9's binding for the values Go cannot
// import: every bound and every closed vocabulary this package declares is read
// back out of shared/mailbox-schema/index.js and compared, so an edit to either
// side is a failing test rather than a message the phone accepts and the
// consumer refuses.
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
}

var (
	jsBound      = regexp.MustCompile(`(?m)^export const ([A-Z_]+) = (\d+);$`)
	jsVocabulary = regexp.MustCompile(`(?s)export const ([A-Z_]+) = Object\.freeze\(\[(.*?)\]\)`)
	jsString     = regexp.MustCompile(`'([^']*)'`)
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
