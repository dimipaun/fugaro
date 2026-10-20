package mailbox

import (
	"bytes"
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
			aBytes := canonicalOf(t, a, key)
			bBytes := canonicalOf(t, b, key)
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

// The second shared artifact both ends consume: bounds.test.js reads it on the
// JavaScript side, this test on the Go side.
const boundsVectorsPath = "../../shared/mailbox-schema/vectors.bounds.json"

// TestTheSharedBoundVectorsHold replays shared/mailbox-schema/vectors.bounds.json.
// The merged vectors.json pins no bound — no case in it sits on one — so a bound
// moved by one survived on both ends: `> TargetMax` narrowed to `>= TargetMax`
// refused a 200-code-unit repository here and in the module, and widened to
// `> TargetMax+1` accepted a 201-code-unit one, with every Go test and all 51
// node tests green. That file is the fix, and this replay is the Go half of it.
//
// Every accepts row is judged TWICE: the row itself must be accepted, and — when
// the file carries a "past" spec — the same row with one more code unit (or the
// entry `add` on the list) at the path it names must be refused with the code it
// names. That is what pins a bound rather than a region around one, and it is why
// a narrowed bound fails the first half of a row and a widened bound fails the
// second.
func TestTheSharedBoundVectorsHold(t *testing.T) {
	file := loadBoundVectors(t)
	accepts := list(t, file["accepts"], "accepts")
	refusals := list(t, file["refusals"], "refusals")
	if len(accepts) != 14 || len(refusals) != 10 {
		t.Fatalf("vectors.bounds.json carries %d accepts and %d refusals, not 14 and 10: the file, not the replay, is what changed",
			len(accepts), len(refusals))
	}

	past := 0
	for _, entry := range accepts {
		vector := object(t, entry, "accept")
		name := text(t, vector["name"], "name")
		why := text(t, vector["why"], "why")
		t.Run(name, func(t *testing.T) {
			record := boundVectorRecord(t, vector["value"])
			if refusal := boundVectorVerdict(t, vector, record); refusal != nil {
				t.Errorf("the value at the bound itself is refused %s: %s", refusal, why)
			}
			spec, carried := vector["past"]
			if !carried {
				return
			}
			past++
			grown, at, code := onePastTheBound(t, vector["value"], object(t, spec, "past"))
			refusal := boundVectorVerdict(t, vector, grown)
			if refusal == nil {
				t.Errorf("a value one step past the bound at %s is accepted, and the file refuses it %s: %s", at, code, why)
				return
			}
			if refusal.Code != code || refusal.Field != at {
				t.Errorf("the value one step past the bound at %s is refused %s, and the file pins %s at %s: %s",
					at, refusal, code, at, why)
			}
		})
	}
	if past != 13 {
		t.Errorf("the file carries %d rows with a bound to walk, not 13: the file, not the replay, is what changed", past)
	}

	for _, entry := range refusals {
		vector := object(t, entry, "refusal")
		name := text(t, vector["name"], "name")
		t.Run(name, func(t *testing.T) {
			why := text(t, vector["why"], "why")
			wantCode := Code(text(t, vector["code"], "code"))
			wantField := text(t, vector["field"], "field")
			refusal := boundVectorVerdict(t, vector, boundVectorRecord(t, vector["value"]))
			if refusal == nil {
				t.Fatalf("the value is accepted, and the vector file refuses it with %s (%s) (%s)", wantCode, wantField, why)
			}
			if refusal.Code != wantCode || refusal.Field != wantField {
				t.Errorf("the refusal is %s, the vector file pins %s (%s) (%s)", refusal, wantCode, wantField, why)
			}
		})
	}
}

// loadBoundVectors reads the bound vectors with the package's own decoder, the
// same way the merged file is read.
func loadBoundVectors(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(boundsVectorsPath)
	if err != nil {
		t.Fatalf("reading %s: %v", boundsVectorsPath, err)
	}
	record, err := Decode(data)
	if err != nil {
		t.Fatalf("decoding %s: %v", boundsVectorsPath, err)
	}
	return record
}

// boundVectorRecord reads one row's value the way the wire would: through the
// package's own decoder, so a row is judged as a decoded record and not as a Go
// literal.
func boundVectorRecord(t *testing.T, value any) Record {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshalling a vector value: %v", err)
	}
	record, err := Decode(data)
	if err != nil {
		t.Fatalf("decoding a vector value: %v", err)
	}
	return record
}

// boundVectorVerdict judges a row as the record its own "record" field names.
func boundVectorVerdict(t *testing.T, vector map[string]any, record Record) *Refusal {
	t.Helper()
	if text(t, vector["record"], "record") == "heartbeat" {
		_, refusal := ValidateHeartbeat(record)
		return refusal
	}
	_, refusal := ValidateMessage(record)
	return refusal
}

// onePastTheBound returns the row's value one step past its bound — one more
// code unit at the path the spec names, the entry `add` on the list at that
// path, or nought in the number there — with the path and the code the file says
// the result must be refused with.
func onePastTheBound(t *testing.T, value any, spec map[string]any) (Record, string, Code) {
	t.Helper()
	at := text(t, spec["at"], "past.at")
	mode := text(t, spec["mode"], "past.mode")
	code := Code(text(t, spec["code"], "past.code"))

	copy := cloneVectorValue(t, value)
	container, key, index, isIndex := vectorParent(t, copy, at)
	switch mode {
	case "grow":
		got, ok := vectorAt(t, copy, at).(string)
		if !ok {
			t.Fatalf("the row's value at %s is not a string, so it has no code unit to add", at)
		}
		vectorSet(t, container, key, index, isIndex, got+"x")
	case "zero":
		vectorSet(t, container, key, index, isIndex, json.Number("0"))
	case "append":
		entries, ok := vectorAt(t, copy, at).([]any)
		if !ok {
			t.Fatalf("the row's value at %s is not a list, so it has no entry to add", at)
		}
		add, carried := spec["add"]
		if !carried {
			t.Fatalf("the append spec at %s does not say which entry it adds", at)
		}
		vectorSet(t, container, key, index, isIndex, append(entries, add))
	default:
		t.Fatalf("the spec's mode %q is not one this replay knows", mode)
	}
	return boundVectorRecord(t, copy), at, code
}

// cloneVectorValue is a deep copy through the decoder, so a mutation cannot
// reach the fixture the next row reads.
func cloneVectorValue(t *testing.T, value any) any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshalling a vector value: %v", err)
	}
	var copy any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&copy); err != nil {
		t.Fatalf("copying a vector value: %v", err)
	}
	return copy
}

// asVectorObject is the fixture's object shape: the file's top level is a Record
// and everything under it a plain map.
func asVectorObject(value any) (map[string]any, bool) {
	switch object := value.(type) {
	case Record:
		return map[string]any(object), true
	case map[string]any:
		return object, true
	}
	return nil, false
}

// vectorStep splits one dotted path step — `agents[0]`, `repo` — into the name
// and the indices after it.
func vectorStep(t *testing.T, step string) (string, []int) {
	t.Helper()
	name := step
	var indices []int
	if open := strings.IndexByte(step, '['); open >= 0 {
		name = step[:open]
		rest := step[open:]
		for rest != "" {
			close := strings.IndexByte(rest, ']')
			if !strings.HasPrefix(rest, "[") || close < 0 {
				t.Fatalf("the path step %q has an index that does not close", step)
			}
			n, err := strconv.Atoi(rest[1:close])
			if err != nil {
				t.Fatalf("the path step %q has an index that is not a number: %v", step, err)
			}
			indices = append(indices, n)
			rest = rest[close+1:]
		}
	}
	return name, indices
}

// vectorAt walks a dotted path over the fixture's two shapes and returns what it
// names, failing loudly rather than inventing a value.
func vectorAt(t *testing.T, value any, path string) any {
	t.Helper()
	node := value
	for _, step := range strings.Split(path, ".") {
		name, indices := vectorStep(t, step)
		if name != "" {
			object, ok := asVectorObject(node)
			if !ok {
				t.Fatalf("the path %s walks into %T, which has no field %q", path, node, name)
			}
			got, carried := object[name]
			if !carried {
				t.Fatalf("the path %s names the field %q, which the fixture does not have", path, name)
			}
			node = got
		}
		for _, index := range indices {
			entries, ok := node.([]any)
			if !ok || index >= len(entries) {
				t.Fatalf("the path %s walks into %T at [%d]", path, node, index)
			}
			node = entries[index]
		}
	}
	return node
}

// vectorParent resolves a path to the container its last step lives in and the
// key within it, which is what a writer needs.
func vectorParent(t *testing.T, value any, path string) (any, string, int, bool) {
	t.Helper()
	parts := strings.Split(path, ".")
	leaf := parts[len(parts)-1]
	node := value
	if len(parts) > 1 {
		node = vectorAt(t, value, strings.Join(parts[:len(parts)-1], "."))
	}
	name, indices := vectorStep(t, leaf)
	if name != "" {
		if len(indices) == 0 {
			return node, name, 0, false
		}
		object, ok := asVectorObject(node)
		if !ok {
			t.Fatalf("the path %s names %q under %T", path, name, node)
		}
		got, carried := object[name]
		if !carried {
			t.Fatalf("the path %s names the field %q, which the fixture does not have", path, name)
		}
		node = got
	}
	for _, index := range indices[:len(indices)-1] {
		entries, ok := node.([]any)
		if !ok || index >= len(entries) {
			t.Fatalf("the path %s walks into %T at [%d]", path, node, index)
		}
		node = entries[index]
	}
	return node, "", indices[len(indices)-1], true
}

// vectorSet writes one value into the container vectorParent resolved.
func vectorSet(t *testing.T, container any, key string, index int, isIndex bool, value any) {
	t.Helper()
	if !isIndex {
		object, ok := asVectorObject(container)
		if !ok {
			t.Fatalf("the path's container is %T, which has no field %q to write", container, key)
		}
		object[key] = value
		return
	}
	entries, ok := container.([]any)
	if !ok || index >= len(entries) {
		t.Fatalf("the path's container is %T, which has no [%d] to write", container, index)
	}
	entries[index] = value
}

// TestTheRefusalCodesNoRefusalVectorCarries is the other half of the replay
// above, and it is here because a code with no vector is a code with no test
// anywhere: TestSchemaVectorRefusals replays what the file carries, so a check
// whose code the file never names can be deleted from the validator with every
// test still green. Four were in exactly that state — empty-string, too-few,
// unknown-kind and duplicate-entry — which is what the review of T1 found.
//
// The four are exercised over every field they apply to rather than one apiece,
// because empty-string is answered by the FIELD: a single row for a sender would
// leave the same check unheld for a body, an option's label, an agent's spoken
// name, an id, the tag and each part of an action.
//
// The file is NOT extended with them, and that is deliberate: vectors.json is a
// merged contract of P6.3's, the plan's file table says no merged module is
// edited by this plan and that a task which appears to need one changed "is a
// finding" (§13, §14), and T1's own Files bullet calls the replay read-only.
// A consumer adding refusals to it is a contract change under a consumer's
// review, so the nine values that would carry these codes — the four fields
// empty-string is answered through, the two lists, a kind outside the four, and
// the two duplicates — are proposed to P6.3's follow-up in the pull request
// instead. Until that lands, the JavaScript end is held to these four codes by
// its own unit tests and not by any vector, which is a weaker binding than the
// other fourteen codes have, and the gap is stated rather than implied.
//
// The guard at the end is what makes this table complete rather than merely
// longer: every code the package declares must be produced by a refusal in
// vectors.json or by a row here, so a code that neither holds is a failing test,
// which is the state the four were in.
func TestTheRefusalCodesNoRefusalVectorCarries(t *testing.T) {
	message := func(over Record) Record {
		record := Record{
			"id": "m-0001", "sender": "phone", "text": "run 42 finished",
			"at": number("1700000000000"), "mac": strings.Repeat("f", MACMax),
		}
		for key, value := range over {
			record[key] = value
		}
		return record
	}
	heartbeat := func(over Record) Record {
		record := Record{
			"at": number("1700000000000"), "boot": "boot-01",
			"agents": []any{agent("orchestrator", "Orchestrator")},
			"mac":    strings.Repeat("f", MACMax),
		}
		for key, value := range over {
			record[key] = value
		}
		return record
	}
	cancel := func(over map[string]any) map[string]any {
		return action(map[string]any{
			"id": "a-0001", "verb": "cancel",
			"target":       map[string]any{"run": "20261018-125859-d3c8"},
			"confirmation": map[string]any{"id": "c-0001", "at": number("1700000000000")},
		}, over)
	}
	merge := func(over map[string]any) map[string]any {
		return action(map[string]any{
			"id": "a-0001", "verb": "merge",
			"target":       map[string]any{"repo": "dimipaun/fugaro", "pr": number("532")},
			"head_sha":     strings.Repeat("a", SHAMin),
			"confirmation": map[string]any{"id": "c-0001", "at": number("1700000000000")},
		}, over)
	}
	cancelled := func(over map[string]any) Record {
		return message(Record{"kind": "action", "action": cancel(over)})
	}
	merged := func(over map[string]any) Record {
		return message(Record{"kind": "action", "action": merge(over)})
	}
	runID := "20261018-125859-d3c8"
	repoName := "dimipaun/fugaro"

	table := []struct {
		name      string
		record    Record
		heartbeat bool
		code      Code
		field     string
		why       string
	}{
		// empty-string, on each of the four fields the module answers it for out
		// of stringField, and then on the fields that reach it another way.
		{"an empty sender", message(Record{"sender": ""}), false, CodeEmptyString, "sender",
			"a message with no sender cannot be attributed"},
		{"an empty body", message(Record{"text": ""}), false, CodeEmptyString, "text",
			"a body of no characters says nothing"},
		{"an empty option label", message(Record{"kind": "prompt", "options": []any{""}}), false, CodeEmptyString, "options[0]",
			"an option with no label cannot be read aloud or answered"},
		{"an empty agent name", heartbeat(Record{"agents": []any{agent("orchestrator", "")}}), true, CodeEmptyString, "agents[0].name",
			"an agent with no spoken name cannot be named"},
		{"an empty id", message(Record{"id": ""}), false, CodeEmptyString, "id",
			"an id is how a message is deduped on (sender, id)"},
		{"an empty reply_to", message(Record{"kind": "answer", "reply_to": ""}), false, CodeEmptyString, "reply_to",
			"an answer names the prompt it answers"},
		{"an empty mac", message(Record{"mac": ""}), false, CodeEmptyString, "mac",
			"an empty tag is not a tag, and it is not the same as an absent one"},
		{"an empty action id", cancelled(map[string]any{"id": ""}), false, CodeEmptyString, "action.id",
			"the action id is what the confirmation names"},
		{"an empty confirmation id", cancelled(map[string]any{
			"confirmation": map[string]any{"id": "", "at": number("1700000000000")}}), false, CodeEmptyString, "action.confirmation.id",
			"a confirmation with no id confirms nothing"},
		{"an empty run target", cancelled(map[string]any{
			"target": map[string]any{"run": ""}}), false, CodeEmptyString, "action.target.run",
			"the consumer runs `fugaro cancel RUN` from this field"},
		{"an empty repository target", message(Record{"kind": "action", "action": merge(map[string]any{
			"target": map[string]any{"repo": "", "pr": number("532")}})}), false, CodeEmptyString, "action.target.repo",
			"the consumer runs `gh pr merge --repo <owner/name>` from this field"},

		// too-few: a list that is present is non-empty. Absent is how a record
		// says "none"; an empty list says the writer meant to fill one.
		{"a prompt with no options", message(Record{"kind": "prompt", "options": []any{}}), false, CodeTooFew, "options",
			"present but empty is not the same as absent"},
		{"a heartbeat with no agents", heartbeat(Record{"agents": []any{}}), true, CodeTooFew, "agents",
			"an empty list is offline, which a heartbeat says by carrying no list at all"},

		// unknown-kind: the kind is a closed vocabulary of four.
		{"a kind outside the four", message(Record{"kind": "nudge"}), false, CodeUnknownKind, "kind",
			"the kinds are plain, prompt, answer and action, and nothing else"},

		// duplicate-entry: two identical entries are one entry with two answers.
		{"two identical options", message(Record{"kind": "prompt", "options": []any{"merge it", "merge it"}}), false, CodeDuplicateEntry, "options[1]",
			"the app answers a prompt by position, so two labels are two right answers"},
		{"two agents under one id", heartbeat(Record{"agents": []any{
			agent("orchestrator", "Orchestrator"), agent("orchestrator", "Shadow")}}), true, CodeDuplicateEntry, "agents[1].id",
			"two agents under one id make \"talk to <agent>\" ambiguous"},

		// The other codes vectors.json carries no refusal for. They are held by
		// other tests as well, and they are here because the guard below is only
		// a guard if every code it does not read out of the file is a row: the
		// file holds ten, so these eight are the ones that have to be here.
		{"an id that is not a database key", message(Record{"id": "m.0001"}), false, CodeBadKey, "id",
			"a dot opens a path in the database the message is stored in"},
		{"a character a body cannot carry", message(Record{"text": "a\x01b"}), false, CodeBadCharacter, "text",
			"a control character is not one the phone can display"},
		{"an agent that is not an object", heartbeat(Record{"agents": []any{"orchestrator"}}), true, CodeNotAnObject, "agents[0]",
			"an entry of the list is {id, name}, not a bare id"},
		{"a merge whose target is a run", message(Record{"kind": "action", "action": merge(map[string]any{
			"target": map[string]any{"run": "20261018-125859-d3c8"}})}), false, CodeBadTarget, "action.target",
			"a merge resolves to a repository, whatever the record says"},

		// The wrong type, field by field. It is a different refusal from the
		// same field failing its shape — a number where an id belongs is
		// wrong-type, not bad-key — and the two used to be one row apiece.
		{"an id that is not a string", message(Record{"id": number("1")}), false, CodeWrongType, "id",
			"an id is a database key, and a number is not one"},
		{"a sender that is not a string", message(Record{"sender": true}), false, CodeWrongType, "sender",
			"the mailbox a message belongs to is named, not counted"},
		{"a body that is not a string", message(Record{"text": number("1")}), false, CodeWrongType, "text",
			"a body is characters"},

		// The two separators: Go's encoder escapes U+2028 and U+2029 even
		// with SetEscapeHTML off, where JSON.stringify leaves both raw, so a
		// record carrying either has two canonical forms and can be neither
		// tagged nor verified. The schema refuses both in every string, and a
		// key answers it as bad-key where a value answers bad-character.
		{"a body carrying U+2028", message(Record{"text": "a b"}), false, CodeBadCharacter, "text",
			"Go escapes U+2028 and JavaScript does not, so its bytes are not one string"},
		{"a body carrying U+2029", message(Record{"text": "a b"}), false, CodeBadCharacter, "text",
			"U+2029 is the same fault as U+2028"},
		{"an id carrying U+2028", message(Record{"id": "a b"}), false, CodeBadKey, "id",
			"a separator in a database key is bad-key, as every other unencodable character is"},
		{"an id carrying U+2029", message(Record{"id": "a b"}), false, CodeBadKey, "id",
			"U+2029 in a key is the same refusal as U+2028"},
		// The character classes at their own boundaries, which a mutant sweep
		// found unheld: the control range is refused THROUGH U+001F (an id
		// carrying only U+001F was accepted by `r < 0x1f`), DEL is refused in a
		// key and in a value, and a line break is a character of a body and of
		// nothing else — not of a sender, an option or an agent's spoken name,
		// where it is log-line and read-aloud injection. Each of these is a
		// check whose deletion or narrowing left the suite green.
		{"an id carrying U+001F", message(Record{"id": "m\u001f1"}), false, CodeBadKey, "id",
			"U+001F is a control character like U+0001: the range ends at the range's end, not a byte short of it"},
		{"an id carrying DEL", message(Record{"id": "m\u007f1"}), false, CodeBadKey, "id",
			"DEL is refused in a database key exactly as it is in a value"},
		{"a sender carrying a line break", message(Record{"sender": "pho\nne"}), false, CodeBadCharacter, "sender",
			"a sender names a mailbox on one line, and a newline in one is log-line injection"},
		{"an option carrying a line break", message(Record{"kind": "prompt", "options": []any{"merge\nit"}}), false, CodeBadCharacter, "options[0]",
			"an option is read aloud and answered by position, so a label is a single line like every other non-body string"},
		{"an agent name carrying a line break", heartbeat(Record{"agents": []any{agent("orchestrator", "Orches\ntrator")}}), true, CodeBadCharacter, "agents[0].name",
			"a spoken name is a single line: a newline in one splits the sentence it is read out in"},
		{"a body carrying DEL", message(Record{"text": "a\u007fb"}), false, CodeBadCharacter, "text",
			"DEL is not a character the phone can display, in a body as anywhere else"},

		{"a kind that is not a string", message(Record{"kind": true}), false, CodeWrongType, "kind",
			"\"not a kind\" and \"not a string\" are different questions"},
		{"an agent that is not a string", message(Record{"agent": number("1")}), false, CodeWrongType, "agent",
			"the agent a message is addressed to is named by its id"},
		{"an agent id drawn from outside its alphabet", message(Record{"agent": "orchestrator!"}), false, CodeBadCharacter, "agent",
			"an agent id is [A-Za-z0-9._:-] and nothing else"},
		{"options that are not a list", message(Record{"kind": "prompt", "options": "merge it"}), false, CodeWrongType, "options",
			"one option is not a list of options"},
		{"agents that are not a list", heartbeat(Record{"agents": "orchestrator"}), true, CodeWrongType, "agents",
			"an advertised list is a list, not one id"},

		// A list's own bound and the entries it holds.
		{"more agents than a heartbeat advertises", heartbeat(Record{"agents": manyAgents(AgentsMax + 1)}), true, CodeTooMany, "agents",
			"a heartbeat advertises at most AgentsMax agents"},
		{"an advertised agent with a field the schema does not name", heartbeat(Record{"agents": []any{
			map[string]any{"id": "orchestrator", "name": "Orchestrator", "role": "boss"}}}), true, CodeUnknownField, "agents[0].role",
			"an advertised agent is {id, name}, exactly"},
		{"an advertised agent with no name", heartbeat(Record{"agents": []any{
			map[string]any{"id": "orchestrator"}}}), true, CodeMissingField, "agents[0].name",
			"an agent the app cannot say aloud cannot be advertised"},

		// The (kind, field) table. A field a kind cannot carry is
		// field-not-allowed and never unknown-field: two ends disagreeing about
		// whether a message is an action is the ambiguity this schema refuses.
		{"options on a kind that does not carry them", message(Record{"kind": "answer", "reply_to": "m-0000",
			"options": []any{"merge it"}}), false, CodeFieldNotAllowed, "options",
			"an answer names the prompt it settles and offers nothing"},
		{"a reply on a kind that does not carry one", message(Record{"kind": "prompt",
			"options": []any{"merge it"}, "reply_to": "m-0000"}), false, CodeFieldNotAllowed, "reply_to",
			"a prompt is answered; it does not answer"},
		{"an action on a kind that does not carry one", message(Record{"kind": "prompt",
			"options": []any{"merge it"}, "action": cancel(nil)}), false, CodeFieldNotAllowed, "action",
			"a kind carries its own fields and no others"},

		// The action record: what it is, what it must carry, and what its verb
		// lets it carry.
		{"an action that is not an object", message(Record{"kind": "action", "action": "cancel"}), false, CodeNotAnObject, "action",
			"an action is a record of its parts, not a verb on its own"},
		{"an action with no confirmation", message(Record{"kind": "action", "action": map[string]any{
			"id": "a-0001", "verb": "cancel", "target": map[string]any{"run": runID}}}), false, CodeMissingField, "action.confirmation",
			"an action with nothing confirming it is never executed"},
		{"a verb that is not a string", cancelled(map[string]any{"verb": number("1")}), false, CodeWrongType, "action.verb",
			"a verb is one of three words"},
		{"a head SHA on a verb that has no head", cancelled(map[string]any{
			"head_sha": strings.Repeat("a", SHAMin)}), false, CodeFieldNotAllowed, "action.head_sha",
			"a cancel names a run, and a run has no head to compare"},

		// The target: the shape it is, whether it is complete and well formed,
		// and only then whether it agrees with the verb. The shape rules are
		// security rules — the run id reaches `fugaro cancel` and the repository
		// `gh pr merge --repo` as ARGV elements.
		{"a target that is not an object", cancelled(map[string]any{"target": runID}), false, CodeNotAnObject, "action.target",
			"a target is a record of the one thing the action resolves to"},
		{"a target naming both a run and a repository", cancelled(map[string]any{"target": map[string]any{
			"run": runID, "repo": repoName, "pr": number("532")}}), false, CodeBadTarget, "action.target",
			"the consumer would have to guess which one the user confirmed"},
		{"a target naming neither shape", cancelled(map[string]any{"target": map[string]any{}}), false, CodeBadTarget, "action.target",
			"a target that names nothing is not a target"},
		{"a run target with a field the schema does not name", cancelled(map[string]any{"target": map[string]any{
			"run": runID, "head": "x"}}), false, CodeUnknownField, "action.target.head",
			"a run target carries the run and nothing else"},
		{"a run that is not a string", cancelled(map[string]any{"target": map[string]any{
			"run": number("1")}}), false, CodeWrongType, "action.target.run",
			"the run id is passed to a command as one argument"},
		{"a run longer than its bound", cancelled(map[string]any{"target": map[string]any{
			"run": strings.Repeat("2", TargetMax+1)}}), false, CodeTooLong, "action.target.run",
			"a run id is bounded like every other string"},
		{"a run with a character a command line cannot carry", cancelled(map[string]any{"target": map[string]any{
			"run": "20261018 125859"}}), false, CodeBadCharacter, "action.target.run",
			"a space is not a character a run id is drawn from"},
		{"a run id that begins with a flag character", cancelled(map[string]any{"target": map[string]any{
			"run": "-20261018-125859"}}), false, CodeBadTarget, "action.target.run",
			"`fugaro cancel --force` is a command line the record would choose"},
		{"a run id that begins with a dot", cancelled(map[string]any{"target": map[string]any{
			"run": ".20261018-125859"}}), false, CodeBadTarget, "action.target.run",
			"a leading dot is a path to whatever reads it"},
		{"a repository target with a field the schema does not name", merged(map[string]any{"target": map[string]any{
			"repo": repoName, "pr": number("532"), "tag": "v1"}}), false, CodeUnknownField, "action.target.tag",
			"a repository target carries the repository and the number, and nothing else"},
		{"a repository target with no pull request number", merged(map[string]any{"target": map[string]any{
			"repo": repoName}}), false, CodeMissingField, "action.target.pr",
			"a repository alone is not a pull request"},
		{"a repository that is not a string", merged(map[string]any{"target": map[string]any{
			"repo": number("1"), "pr": number("532")}}), false, CodeWrongType, "action.target.repo",
			"the repository is passed to `gh pr merge --repo` as one argument"},
		{"a repository longer than its bound", merged(map[string]any{"target": map[string]any{
			"repo": strings.Repeat("a", TargetMax) + "/b", "pr": number("532")}}), false, CodeTooLong, "action.target.repo",
			"a repository is bounded like every other string"},
		{"a repository one code unit past its bound", merged(map[string]any{"target": map[string]any{
			"repo": strings.Repeat("a", TargetMax-1) + "/b", "pr": number("532")}}), false, CodeTooLong, "action.target.repo",
			"the bound itself is the largest accepted value: one code unit past it is too-long, not the second past it"},
		{"a repository with a character a command line cannot carry", merged(map[string]any{"target": map[string]any{
			"repo": repoName + ";rm", "pr": number("532")}}), false, CodeBadCharacter, "action.target.repo",
			"a semicolon is not a character a repository is drawn from"},
		{"a repository that is one name and not two", merged(map[string]any{"target": map[string]any{
			"repo": "dimipaun", "pr": number("532")}}), false, CodeBadTarget, "action.target.repo",
			"`gh pr merge --repo dimipaun` names no repository"},
		{"a repository that begins with a flag character", merged(map[string]any{"target": map[string]any{
			"repo": "-" + repoName, "pr": number("532")}}), false, CodeBadTarget, "action.target.repo",
			"`gh pr merge --repo --foo` is a command line the record would choose"},
		{"a pull request number that is not a number", merged(map[string]any{"target": map[string]any{
			"repo": repoName, "pr": "532"}}), false, CodeWrongType, "action.target.pr",
			"a number written as a string is not a number"},
		{"a pull request number that is not whole", merged(map[string]any{"target": map[string]any{
			"repo": repoName, "pr": number("532.5")}}), false, CodeBadTarget, "action.target.pr",
			"there is no pull request 532.5"},
		{"a pull request number below one", merged(map[string]any{"target": map[string]any{
			"repo": repoName, "pr": number("0")}}), false, CodeBadTarget, "action.target.pr",
			"pull requests are numbered from one"},
		{"a cancel whose target is a repository", cancelled(map[string]any{"target": map[string]any{
			"repo": repoName, "pr": number("532")}}), false, CodeBadTarget, "action.target",
			"a cancel cancels a run (D169), whatever the record says"},

		// The confirmation, and the head SHA a merge carries.
		{"a confirmation that is not an object", cancelled(map[string]any{"confirmation": "c-0001"}), false, CodeNotAnObject, "action.confirmation",
			"a confirmation is an id and a time, not a bare id"},
		{"a confirmation with a field the schema does not name", cancelled(map[string]any{"confirmation": map[string]any{
			"id": "c-0001", "at": number("1700000000000"), "by": "phone"}}), false, CodeUnknownField, "action.confirmation.by",
			"a confirmation carries the ledger's id and the time it was given"},
		{"a confirmation with no time", cancelled(map[string]any{"confirmation": map[string]any{
			"id": "c-0001"}}), false, CodeMissingField, "action.confirmation.at",
			"the command window is measured from the time it was given"},
		{"a confirmation at a time that is not a number", cancelled(map[string]any{"confirmation": map[string]any{
			"id": "c-0001", "at": "1700000000000"}}), false, CodeWrongType, "action.confirmation.at",
			"an epoch millisecond written as a string is not one"},
		{"a head SHA that is not a string", merged(map[string]any{"head_sha": number("1")}), false, CodeBadSHA, "action.head_sha",
			"an object name is a string of hex, not a number"},
		{"a head SHA between the two lengths", merged(map[string]any{"head_sha": strings.Repeat("a", SHAMin+10)}), false, CodeBadSHA, "action.head_sha",
			"the two lengths are the two lengths: an abbreviation of some other length names no object"},

		// A time, and the flag the phone writes later.
		{"a time before the epoch", message(Record{"at": number("-1")}), false, CodeBadTimestamp, "at",
			"epoch milliseconds are not negative"},
		{"a time that is not a number at all", message(Record{"at": json.Number("soon")}), false, CodeWrongType, "at",
			"a json.Number that is not a number is the wrong type, not a bad time"},
		{"an acked flag that is not a boolean", message(Record{"acked": "yes"}), false, CodeWrongType, "acked",
			"`acked` says whether the phone has read the message, and that is true or false"},

		// The heartbeat's own three fields.
		{"a heartbeat at a time before the epoch", heartbeat(Record{"at": number("-1")}), true, CodeBadTimestamp, "at",
			"a heartbeat is stamped like every other record"},
		{"a boot id drawn from outside its alphabet", heartbeat(Record{"boot": "boot/01"}), true, CodeBadCharacter, "boot",
			"a boot id is drawn from the same alphabet an agent id is"},
		{"a heartbeat tag that is not hex", heartbeat(Record{"mac": strings.Repeat("F", MACMax)}), true, CodeBadMAC, "mac",
			"hex has one spelling here, so one tag has one string"},
	}
	for _, tc := range table {
		t.Run(tc.name, func(t *testing.T) {
			var refusal *Refusal
			if tc.heartbeat {
				_, refusal = ValidateHeartbeat(tc.record)
			} else {
				_, refusal = ValidateMessage(tc.record)
			}
			if refusal == nil {
				t.Fatalf("the record is accepted, and it must be refused %s at %s: %s", tc.code, tc.field, tc.why)
			}
			if refusal.Code != tc.code || refusal.Field != tc.field {
				t.Errorf("the refusal is %s at %q, wanted %s at %q: %s", refusal.Code, refusal.Field, tc.code, tc.field, tc.why)
			}
		})
	}

	// The guard, and the reason this table carries eight codes rather than only
	// the four the review named: every code the package declares is produced by
	// a refusal in vectors.json or by a row here, so a code that neither holds
	// is a failing test. That is exactly the state the four were in —
	// implemented, refusable, and green under the deletion of their checks. The
	// file holds ten codes and this table holds the other eight, which is what
	// makes the two of them together the whole of Codes.
	held := map[Code]string{}
	for _, entry := range list(t, loadVectors(t)["refusals"], "refusals") {
		refusal := object(t, entry, "refusal")
		held[Code(text(t, refusal["code"], "code"))] = "vectors.json"
	}
	for _, tc := range table {
		held[tc.code] = tc.name
	}
	for _, code := range Codes {
		if _, ok := held[code]; !ok {
			t.Errorf("the code %s is declared and nothing holds it: no refusal in vectors.json carries it and no row above produces it", code)
		}
	}
}

// manyAgents is n distinct advertised agents, for the rows about the list's
// bound: distinct, so that the bound is the only thing that can refuse them.
func manyAgents(n int) []any {
	agents := make([]any, 0, n)
	for i := range n {
		agents = append(agents, agent("agent-"+strconv.Itoa(i), "Agent"))
	}
	return agents
}

// agent is one entry of a heartbeat's advertised list.
func agent(id, name string) map[string]any {
	return map[string]any{"id": id, "name": name}
}

// action is a well-formed action record with a change applied, so a row can move
// one part of it rather than restate the rest.
func action(base, over map[string]any) map[string]any {
	record := map[string]any{}
	for key, value := range base {
		record[key] = value
	}
	for key, value := range over {
		record[key] = value
	}
	return record
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

// TestTheBoundsThemselvesAreAccepted is the other half of the refusal table's
// bound rows, and it is here because a bound test that only ever pushes past a
// bound cannot tell `> max` from `>= max`: the refusal table's "one code unit
// past its bound" rows are all still refused by a bound narrowed by one, and
// the suite stayed green when the repository's bound and the key's bound were
// each narrowed that way. The bound IS the largest accepted value — the merged
// module's `.length` comparisons are inclusive in the same way — so the largest
// accepted value is what the positive half has to walk, field by field.
//
// `mac` needs no row of its own: every record here carries one of exactly
// MACMax, so the rows below pin that length between them. `head_sha` is
// different — its length is neither bounded nor one exact value but a choice of
// two — and the vector file the package replays carries only the 40-character
// one, so the 64-character length is pinned by the two rows at the end of the
// table and by nothing else. Without them `shaField` narrowed to
// `utf16Len(s) != SHAMin` refuses a length the module accepts and the whole
// suite stays green.
func TestTheBoundsThemselvesAreAccepted(t *testing.T) {
	message := func(over Record) Record {
		record := Record{
			"id": "m-0001", "sender": "phone", "text": "run 42 finished",
			"at": number("1700000000000"), "mac": strings.Repeat("a", MACMax),
		}
		for key, value := range over {
			record[key] = value
		}
		return record
	}
	cancelled := func(over map[string]any) Record {
		return message(Record{"kind": "action", "agent": "orchestrator", "action": action(map[string]any{
			"id": "a-0001", "verb": "cancel",
			"target":       map[string]any{"run": "20261018-125859-d3c8"},
			"confirmation": map[string]any{"id": "c-0001", "at": number("1700000000000")},
		}, over)})
	}
	merged := func(over map[string]any) Record {
		return message(Record{"kind": "action", "agent": "orchestrator", "action": action(map[string]any{
			"id": "a-0001", "verb": "merge",
			"target":       map[string]any{"repo": "dimipaun/fugaro", "pr": number("532")},
			"head_sha":     strings.Repeat("a", SHAMin),
			"confirmation": map[string]any{"id": "c-0001", "at": number("1700000000000")},
		}, over)})
	}
	// A repository of exactly TargetMax code units needs both of its halves:
	// repoPattern wants an owner and a name, so the bound is reached with a
	// short name and a long owner rather than with one long word.
	longRepo := strings.Repeat("a", TargetMax-2) + "/b"
	if len(longRepo) != TargetMax {
		t.Fatalf("the boundary repository is %d code units, wanted TargetMax (%d)", len(longRepo), TargetMax)
	}

	for _, tc := range []struct {
		name      string
		record    Record
		heartbeat bool
		why       string
	}{
		{"an id of exactly IDMax", message(Record{"id": strings.Repeat("a", IDMax)}), false,
			"an id is at most IDMax code units, and the bound itself is one of them"},
		{"an id of exactly IDMax code units of U+FFFF", message(Record{"id": strings.Repeat("￿", IDMax)}), false,
			"U+FFFF is the BMP's last character and one UTF-16 code unit; every astral character above it is a PAIR of code units, which is where a count of code units and a count of characters part company"},
		{"a pull request numbered one", merged(map[string]any{
			"target": map[string]any{"repo": "dimipaun/fugaro", "pr": number("1")}}), false,
			"pull requests are numbered from one, so one is the smallest number a merge may name (the refusal table holds nought)"},
		{"a sender of exactly SenderMax", message(Record{"sender": strings.Repeat("a", SenderMax)}), false,
			"a sender is bounded like every other string"},
		{"a body of exactly TextMax", message(Record{"text": strings.Repeat("a", TextMax)}), false,
			"4096 code units of body is the largest body the phone writes"},
		{"an agent of exactly AgentMax", message(Record{"agent": strings.Repeat("a", AgentMax)}), false,
			"an agent id is drawn from its alphabet AND bounded by AgentMax"},
		{"an option of exactly OptionMax", message(Record{"kind": "prompt", "options": []any{strings.Repeat("a", OptionMax)}}), false,
			"an option label is bounded like the identity it is read out beside"},
		{"a reply_to of exactly IDMax", message(Record{"kind": "answer", "reply_to": strings.Repeat("a", IDMax)}), false,
			"an answer's reply_to is a database key like an id"},
		{"an action id of exactly IDMax", cancelled(map[string]any{"id": strings.Repeat("a", IDMax)}), false,
			"the action id is a key, bounded by IDMax"},
		{"a confirmation id of exactly IDMax", cancelled(map[string]any{
			"confirmation": map[string]any{"id": strings.Repeat("a", IDMax), "at": number("1700000000000")}}), false,
			"the confirmation id is a key, bounded by IDMax"},
		{"a run of exactly TargetMax", cancelled(map[string]any{
			"target": map[string]any{"run": strings.Repeat("a", TargetMax)}}), false,
			"a run id reaches `fugaro cancel` bounded by TargetMax, and the bound itself is accepted"},
		{"a repository of exactly TargetMax", merged(map[string]any{
			"target": map[string]any{"repo": longRepo, "pr": number("532")}}), false,
			"a repository reaches `gh pr merge --repo` bounded by TargetMax, and the bound itself is accepted"},
		{"a boot of exactly BootMax", heartbeatRecord(Record{"boot": strings.Repeat("a", BootMax)}), true,
			"a boot id is a boot id up to BootMax code units"},
		{"an agent name of exactly AgentNameMax", heartbeatRecord(Record{"agents": []any{
			agent("orchestrator", strings.Repeat("a", AgentNameMax))}}), true,
			"a spoken name is bounded like the id beside it"},
		{"exactly AgentsMax agents", heartbeatRecord(Record{"agents": manyAgents(AgentsMax)}), true,
			"a heartbeat advertises at most AgentsMax agents, and AgentsMax is allowed"},
		{"exactly OptionsMax options", message(Record{"kind": "prompt", "options": manyOptions(OptionsMax)}), false,
			"a prompt offers at most OptionsMax options, and OptionsMax is allowed"},
		{"a head SHA of exactly SHAMin", merged(map[string]any{
			"head_sha": strings.Repeat("a", SHAMin)}), false,
			"a head SHA is 40 or 64 hex characters, and 40 is one of the two"},
		{"a head SHA of exactly SHAMax", merged(map[string]any{
			"head_sha": strings.Repeat("a", SHAMax)}), false,
			"64 is the other length a head SHA may be, and no vector in the file the package replays carries one"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var refusal *Refusal
			if tc.heartbeat {
				_, refusal = ValidateHeartbeat(tc.record)
			} else {
				_, refusal = ValidateMessage(tc.record)
			}
			if refusal != nil {
				t.Errorf("the value at the bound itself is refused %s: %s", refusal, tc.why)
			}
		})
	}
}

// TestTheDocSaysWhichValuesAreNotArgvSafe pins the package doc's claim about
// the alphabets, and it is the test the five-minor follow-up owes m5: the doc
// says that only `action.target.run` and `action.target.repo` (whose patterns
// refuse a leading flag character and a leading dot) are argv-safe, and that a
// key may be a single space and may begin with `-` while an identity may be `.`
// or `..` or begin with `-`. A claim that says "the validator accepts these and
// a later task must not pass them to a program" is worth no more than the test
// that holds it, because a task that TIGHTENED these alphabets would break the
// merged module's contract, and a task that tightened the doc's warning away
// would break nothing else.
//
// Every value below was checked against index.js as well as this package before
// being written down: the two ends agree on each one.
func TestTheDocSaysWhichValuesAreNotArgvSafe(t *testing.T) {
	message := func(over Record) Record {
		record := Record{
			"id": "m-0001", "sender": "phone", "text": "run 42 finished",
			"at": number("1700000000000"), "mac": strings.Repeat("a", MACMax),
		}
		for key, value := range over {
			record[key] = value
		}
		return record
	}
	prompt := func(agent string) Record {
		return message(Record{"kind": "prompt", "agent": agent, "options": []any{"merge it", "leave it"}})
	}

	for _, tc := range []struct {
		name   string
		record Record
		why    string
	}{
		{"an id that is a single space", message(Record{"id": " "}),
			"a space is not one of the seven characters a key refuses, and a key's alphabet is not an argv alphabet"},
		{"an id that begins with a flag character", message(Record{"id": "--x"}),
			"a key may begin with `-`: the leading-dash refusal belongs to the run and repository patterns, not to keyField"},
		{"an id carrying glob and shell metacharacters", message(Record{"id": "* ?"}),
			"`*` and `?` are ordinary characters to the database key rule"},
		{"an id carrying a backslash and a quote", message(Record{"id": `a\b"c'd`}),
			"quoting is the argv caller's problem, not this schema's"},
		{"an id carrying a right-to-left override", message(Record{"id": "a‮b"}),
			"U+202E is a format character and not a control character, and the merged module accepts it in a key"},
		{"an id carrying a zero-width space and a byte order mark", message(Record{"id": "a\u200bb\xef\xbb\xbfc"}),
			"U+200B and U+FEFF are the same class as U+202E"},
		{"an id carrying an astral character", message(Record{"id": "x\U0001F600y"}),
			"an astral character is two code units and stays inside the bound"},
		{"a reply_to that is a single space", message(Record{"kind": "answer", "reply_to": " "}),
			"every key field reaches keyField, so every key field admits what it admits"},
		{"an action id that is a single space", message(Record{"kind": "action", "agent": "orchestrator", "action": action(map[string]any{
			"id": " ", "verb": "cancel",
			"target":       map[string]any{"run": "20261018-125859-d3c8"},
			"confirmation": map[string]any{"id": "c-0001", "at": number("1700000000000")},
		}, nil)}),
			"the action id is a key like any other"},
		{"a confirmation id that begins with a flag character", message(Record{"kind": "action", "agent": "orchestrator", "action": action(map[string]any{
			"id": "a-0001", "verb": "cancel",
			"target":       map[string]any{"run": "20261018-125859-d3c8"},
			"confirmation": map[string]any{"id": "--x", "at": number("1700000000000")},
		}, nil)}),
			"a confirmation id is a key like any other"},

		{"an agent that begins with a flag character", prompt("--help"),
			"identityPattern is [A-Za-z0-9._:-], so `--help` is a well-formed agent id and must never be argv"},
		{"an agent that is a single dot", prompt("."),
			"the same alphabet admits `.`, which is a directory and not a program"},
		{"an agent that is two dots", prompt(".."),
			"and it admits `..`, which is the parent directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, refusal := ValidateMessage(tc.record); refusal != nil {
				t.Errorf("the value is refused %s: %s", refusal, tc.why)
			}
		})
	}

	// The identities that are not a message's `agent`: a boot id and an
	// advertised agent's id are the same alphabet in the same function.
	for _, boot := range []string{"--help", ".", "..", "-", "a:b"} {
		if _, refusal := ValidateHeartbeat(heartbeatRecord(Record{"boot": boot})); refusal != nil {
			t.Errorf("the boot id %q is refused %s: a boot id is drawn from the same alphabet an agent id is", boot, refusal)
		}
		if _, refusal := ValidateHeartbeat(heartbeatRecord(Record{"agents": []any{agent(boot, "Orchestrator")}})); refusal != nil {
			t.Errorf("the advertised agent id %q is refused %s: it is drawn from the same alphabet", boot, refusal)
		}
	}

	// And what each alphabet DOES refuse, so the test cannot be satisfied by an
	// alphabet that refuses nothing.
	for _, id := range []string{"%", ".", "$", "#", "[", "]", "/", "a\u0001b", "a\u001fb", "a\u007fb", "a b", "a b"} {
		_, refusal := ValidateMessage(message(Record{"id": id}))
		if refusal == nil || refusal.Code != CodeBadKey {
			t.Errorf("the id %q is refused %v, wanted %s: these are the characters a database key may not carry", id, refusal, CodeBadKey)
		}
	}
	for _, identity := range []string{"orchestrator!", "a b", "a/b", "a+b", "\U0001F600", "aéb"} {
		if _, refusal := ValidateMessage(prompt(identity)); refusal == nil || refusal.Code != CodeBadCharacter {
			t.Errorf("the agent %q is refused %v, wanted %s: an identity is drawn from [A-Za-z0-9._:-] and nothing else", identity, refusal, CodeBadCharacter)
		}
	}
}

// heartbeatRecord is the refusal table's heartbeat builder, at package scope so
// the boundary and alphabet tables can use it too.
func heartbeatRecord(over Record) Record {
	record := Record{
		"at": number("1700000000000"), "boot": "boot-01",
		"agents": []any{agent("orchestrator", "Orchestrator")},
		"mac":    strings.Repeat("a", MACMax),
	}
	for key, value := range over {
		record[key] = value
	}
	return record
}

// manyOptions is n distinct option labels, for the bound row: distinct, so that
// the bound is the only thing that can refuse them.
func manyOptions(n int) []any {
	options := make([]any, 0, n)
	for i := range n {
		options = append(options, "option-"+strconv.Itoa(i))
	}
	return options
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

// TestTwoUnknownFieldsAreNamedInByteOrder pins the half of ValidateMessage's
// contract that is this side's alone to keep, and the half it cannot.
//
// A record carrying one field the schema does not name is refused
// unknown-field at that field, in both languages. A record carrying several is
// refused unknown-field at ONE of them on each side, and the two sides need not
// agree on which: the module visits Object.keys order — insertion order, with
// the integer-like keys first — and a Go map has no insertion order at all, so
// this side visits the keys in byte order and answers with the first in that
// order. Both are deterministic and both are unknown-field; the FIELD is the
// one thing about a multi-unknown-field refusal that is not a shared answer.
//
// That is why ValidateMessage's doc says "the same code, and the same field
// whenever one field is at fault" rather than "the same field at fault"
// outright, and this test is what holds this side's half of the softened claim:
// the same input answers the same field every time, and that field is the
// first key in byte order.
func TestTwoUnknownFieldsAreNamedInByteOrder(t *testing.T) {
	// `text` and `at` are present so the refusal cannot come from a missing
	// field, and the two unknown keys straddle the fields in byte order:
	// "0landed_at" < "text" < "zz_extra".
	record := Record{
		"id": "m-0001", "sender": "phone", "text": "run 42 finished",
		"at": number("1700000000000"), "mac": strings.Repeat("a", MACMax),
		"zz_extra": int64(1), "0landed_at": int64(2),
	}
	for range 5 {
		_, refusal := ValidateMessage(record)
		if refusal == nil || refusal.Code != CodeUnknownField {
			t.Fatalf("two unknown fields are refused %v, wanted %s: which of the two is named may differ, the code may not", refusal, CodeUnknownField)
		}
		if refusal.Field != "0landed_at" {
			t.Errorf("the fields at fault are named %q, wanted the first in byte order (%q): the answer is deterministic here, and it is this side's own rule", refusal.Field, "0landed_at")
		}
	}

	// And the code, not the field, is what the two ends share: the module's own
	// ordering is unreachable from Go — a map has no insertion order — so the
	// one Go-side property that can be asserted is that no unknown field is
	// ever accepted, whatever order the two arrive in.
	for _, order := range [][]string{{"aa", "bb"}, {"bb", "aa"}, {"aa", "aa"}} {
		candidate := Record{"id": "m-0001", "mac": strings.Repeat("a", MACMax)}
		for _, key := range order {
			candidate[key] = int64(1)
		}
		_, refusal := ValidateMessage(candidate)
		if refusal == nil || refusal.Code != CodeUnknownField {
			t.Errorf("a record carrying %v is refused %v, wanted %s", order, refusal, CodeUnknownField)
		}
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

// TestAnythingValidatedCanBeCanonicalisedExceptNegativeZero is the property the
// two halves owe each other: a record ValidateMessage accepts is a record
// CanonicalBytes can serialise. Anything else is a record with two verdicts, and
// the second one arrives only when the tag is made. Every numeric type this
// package accepts goes into every position that takes a number, because a type
// accepted in one field and refused in another is the same disagreement one
// field smaller.
//
// The name carries the one exception, because the property as stated outright
// is false and its exception is deliberate: `-0` in `at` or in a confirmation's
// `at` validates on both ends and can be canonicalised on neither, which
// TestNegativeZeroValidates pins and the merged module's README spells out. It
// is fail-closed — T2's MAC step can never verify such a record — and it is not
// one of the spellings this test walks: none of them is negative zero.
func TestAnythingValidatedCanBeCanonicalisedExceptNegativeZero(t *testing.T) {
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
		{"a low half at the bottom of its range", "plain message", `"text":"run 42 finished"`, `"text":"\uDC00"`, CodeBadCharacter, "text"},
		{"a high half at the top of its range", "plain message", `"text":"run 42 finished"`, `"text":"a\uDBFFb"`, CodeBadCharacter, "text"},
		{"a low half at the top of its range", "plain message", `"text":"run 42 finished"`, `"text":"a\uDFFFb"`, CodeBadCharacter, "text"},
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
	// The other edge of the same range: U+DFFF is the LAST lone half, so it
	// pins the two byte bounds inside isWTF8Surrogate (0xa0..0xbf and
	// 0x80..0xbf) and the `len(s) >= 3` that reads the last three bytes of a
	// string. Counted as three — which is what recognising none of it does —
	// IDMax-1 characters plus this one came back too-long where the module
	// answers bad-key.
	const lowLone = "\xed\xbf\xbf" // U+DFFF in WTF-8
	for _, tc := range []struct {
		name  string
		field string
		value string
		want  Code
	}{
		{"34 lone halves in an id are 34 code units", "id", strings.Repeat(lone, 34), CodeBadKey},
		{"one lone half in an id", "id", "m-" + lone, CodeBadKey},
		{"101 lone halves in an id are past the bound", "id", strings.Repeat(lone, IDMax+1), CodeTooLong},
		{"IDMax lone halves in an id are exactly IDMax code units", "id", strings.Repeat(lone, IDMax), CodeBadKey},
		{"an id of IDMax code units ending in a lone low half", "id", strings.Repeat("a", IDMax-1) + lowLone, CodeBadKey},
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
		{"a lone high half at the top of its range", `"\uDBFF"`, "\xed\xaf\xbf"},
		{"a lone low half at the top of its range", `"\uDFFF"`, "\xed\xbf\xbf"},
		{"the first character above the BMP", `"\uD800\uDC00"`, "\U00010000"},
		{"the last character, at the top of both ranges", `"\uDBFF\uDFFF"`, "\U0010FFFF"},
		{"a lowercase hex escape whose first digit is the a of a-f", `"\uabcd"`, "\uabcd"},
		{"a lowercase hex escape carrying the last letter of a-f", `"\uf0f0"`, "\uf0f0"},
		{"an uppercase hex escape carrying the last letter of A-F", `"\uF0F0"`, "\uF0F0"},
		{"an uppercase hex escape whose first digit is the A of A-F", `"\uABCD"`, "\uABCD"},
		{"a pair beside a lone half", `"\uD83D\uDE00\uD800"`, "\U0001F600\xed\xa0\x80"},
		{"a lone half beside a pair", `"\uD800\uD83D\uDE00"`, "\xed\xa0\x80\U0001F600"},
		{"a high half before an ordinary escape", `"\uD83D\u00e9"`, "\xed\xa0\xbd\xc3\xa9"},
		{"escapes beside a lone half", `"a\\b\"c\nd\uD800"`, "a\\b\"c\nd\xed\xa0\x80"},
		{"a raw character beside a lone half", `"é\uD800"`, "é\xed\xa0\x80"},
		{"a backspace beside a lone half", `"a\bb\uD800"`, "a\bb\xed\xa0\x80"},
		{"a form feed beside a lone half", `"a\fb\uD800"`, "a\fb\xed\xa0\x80"},
		{"a carriage return beside a lone half", `"a\rb\uD800"`, "a\rb\xed\xa0\x80"},
		{"a tab beside a lone half", `"a\tb\uD800"`, "a\tb\xed\xa0\x80"},
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
		{`"`, "no closing quote"}, // one quote, and nothing on either side of it
		{`"a` + "\\", "no closing quote"},
		{`"a` + "\\" + `"`, "lone backslash"},   // a backslash with only the closing quote after it
		{`"a` + "\\" + `u00"`, "half-written"},  // half an escape
		{`"a` + "\\" + `uZZZZ"`, "hexadecimal"}, // four digits, and not one of them hex
		{`"a` + "\\" + `u12g4"`, "hexadecimal"}, // the same, with the bad digit in the middle
		{`"a` + "\\" + `q"`, "JSON grammar"},    // an escape JSON does not have
	} {
		t.Run("refused: "+tc.literal, func(t *testing.T) {
			got, err := unquoteKeepingLoneSurrogates([]byte(tc.literal))
			if err == nil {
				t.Fatalf("%q unquoted to %q, and a document cannot hold it", tc.literal, got)
			}
			// Each row is its own branch, so each is named: a test that only
			// asked "did it fail" would pass with one check deleted, because
			// the next check along catches the same literal with a different
			// message — which is what a mutation proved before this table had
			// a `wantErr` column. The last two rows are the pair hexQuad
			// returns: dropping its ok flag is invisible to a row that only
			// asks whether the call failed, since a false from it is a NUL
			// with no error at all.
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%q failed with %q, wanted it to name %q", tc.literal, err, tc.wantErr)
			}
		})
	}
}

// TestTheSurrogateRangeEdgesSurviveTheWire is the decode-level half of the
// same edges. The two classification functions are written twice —
// hasLoneSurrogateEscape's switch and the unquoter's isHighSurrogate /
// isLowSurrogate pair — and the edges are what an off-by-one moves.
//
// The first two cases are a well-formed pair at each end of the range, and what
// they pin is the ordinary path: the JSON decoder combines the pair into one
// character, the validator accepts the record, and the encoder writes that
// character raw rather than as an escape. They do NOT reach the unquoter — a
// document whose only escapes are well-formed pairs leaves
// hasLoneSurrogateEscape answering false — so narrowing any of the unquoter's
// four bounds by one leaves them green. The unquoter's own edge rows are in
// TestTheUnquoterDecodesWhatItKeeps; the third group below brings the pair to
// this level too.
//
// The third group is the one that reaches the unquoter: the same pair at each
// edge with a lone half BESIDE it, in the same string, so hasLoneSurrogateEscape
// answers true and unquoteKeepingLoneSurrogates reads the whole value. Narrow
// any of the four isHighSurrogate / isLowSurrogate bounds by one and the pair
// at that edge stops being a pair: the text decodes to the two halves' WTF-8
// instead of the one character, and the decoded value below is what fails.
func TestTheSurrogateRangeEdgesSurviveTheWire(t *testing.T) {
	for _, tc := range []struct {
		name    string
		escaped string
		want    string
	}{
		{"the first character above the BMP", `\uD800\uDC00`, "\U00010000"},
		{"the last character", `\uDBFF\uDFFF`, "\U0010FFFF"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			document := surrogateDocument(t, "plain message", `"text":"run 42 finished"`, `"text":"`+tc.escaped+`"`)
			record, err := Decode(document)
			if err != nil {
				t.Fatalf("the decoder refused the document itself: %v", err)
			}
			if got, _ := record["text"].(string); got != tc.want {
				t.Fatalf("the pair decoded to %q, wanted %q: it is one character, not two lone halves", got, tc.want)
			}
			if _, refusal := ValidateMessage(record); refusal != nil {
				t.Errorf("the record is refused %s: the module accepts a well-formed pair, whatever code units it is spelled with", refusal)
			}
			bytes, err := CanonicalBytes(record, "inbox")
			if err != nil {
				t.Fatalf("CanonicalBytes: %v", err)
			}
			if !strings.Contains(string(bytes), tc.want) {
				t.Errorf("the canonical bytes are %s, wanted them to carry %q raw", bytes, tc.want)
			}
		})
	}

	// The lone half is what makes the unquoter run. It is at the OTHER end of
	// the range from the pair under test, so the edge bounds are the only thing
	// that can spoil the pair: U+DFFF's WTF-8 is ED BF BF, U+D800's is ED A0 80.
	for _, tc := range []struct {
		name    string
		escaped string
		want    string
	}{
		{"the first character above the BMP beside a lone low half", `\uD800\uDC00\uDFFF`, "\U00010000\xed\xbf\xbf"},
		{"the last character beside a lone high half", `\uDBFF\uDFFF\uD800`, "\U0010FFFF\xed\xa0\x80"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			document := surrogateDocument(t, "plain message", `"text":"run 42 finished"`, `"text":"`+tc.escaped+`"`)
			record, err := Decode(document)
			if err != nil {
				t.Fatalf("the decoder refused the document itself: %v", err)
			}
			if got, _ := record["text"].(string); got != tc.want {
				t.Fatalf("the text decoded to %q, wanted %q: the pair is one character and only the lone half is two halves", got, tc.want)
			}
			// The record is refused for the lone half and for nothing else, and
			// the field is the one the lone half is in. A decoder that read the
			// pair as two halves refuses this same record with this same code,
			// so the decoded value above is the assertion that tells them apart.
			_, refusal := ValidateMessage(record)
			if refusal == nil || refusal.Code != CodeBadCharacter || refusal.Field != "text" {
				t.Errorf("the record is answered %v, wanted the lone half refused as %s on text",
					refusal, CodeBadCharacter)
			}
		})
	}
}

// TestTheDepthBoundIsTheBoundItself is the decoder's own off-by-one: maxDepth
// is the deepest document Decode accepts, and the module's own records are far
// shallower, so nothing else in the suite walks the edge. Narrow the bound by
// one (`depth > maxDepth` -> `depth >= maxDepth`) and a document nested exactly
// maxDepth levels deep is refused by a decoder that used to read it.
func TestTheDepthBoundIsTheBoundItself(t *testing.T) {
	document := func(levels int) []byte {
		return []byte(strings.Repeat(`{"a":`, levels) + "1" + strings.Repeat("}", levels))
	}
	if _, err := Decode(document(maxDepth)); err != nil {
		t.Errorf("a document nested exactly maxDepth (%d) levels deep is refused: %v", maxDepth, err)
	}
	_, err := Decode(document(maxDepth + 1))
	if err == nil {
		t.Fatalf("a document nested maxDepth+1 levels deep is accepted, and the bound is %d", maxDepth)
	}
	if !strings.Contains(err.Error(), "nested deeper than") {
		t.Errorf("the deeper document failed with %q, wanted the depth refusal", err)
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
// failure if Go produces others. The key is the caller's, read out of the same
// file: a literal here would fail with "the tag is not the one the vector file
// pins" for a key change that is not about this test at all.
func canonicalOf(t *testing.T, side map[string]any, key string) string {
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
	if want := text(t, side["mac"], "mac"); tagOf(key, string(got)) != want {
		t.Fatalf("the tag for %s is not the one the vector file pins, over the key it carries (%s)", direction, key)
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

// TestRefusalErrorSpellsTheCodeAndTheField pins the two spellings Refusal has
// as an error. They are the binding's log line and its `--json` document, so
// they are the same kind of contract the codes are: the code and the field,
// and never the message's text.
func TestRefusalErrorSpellsTheCodeAndTheField(t *testing.T) {
	if got, want := (&Refusal{Code: CodeBadTarget, Field: "action.target.run"}).Error(), "bad-target (action.target.run)"; got != want {
		t.Errorf("a refusal with a field spells %q, wanted %q", got, want)
	}
	if got, want := (&Refusal{Code: CodeNotAnObject}).Error(), "not-an-object"; got != want {
		t.Errorf("a refusal with no field spells %q, wanted %q", got, want)
	}
	if got, want := refuse(CodeBadMAC, "mac").Error(), "bad-mac (mac)"; got != want {
		t.Errorf("refuse spells %q, wanted %q", got, want)
	}
}

// TestDecodeRefusesDocumentsThatAreNotOneValue walks the decoder's own
// refusals: a document that ends before its record does, a token where a value
// belongs, and a second value after the first. Each is a document a plain
// json.Unmarshal would answer differently — three of them with a map and no
// error at all — so each is pinned by the message it carries rather than by
// "it failed", which is the shape of assertion a deletion survives.
func TestDecodeRefusesDocumentsThatAreNotOneValue(t *testing.T) {
	for _, tc := range []struct {
		document string
		wantErr  string
	}{
		{"", "reading a record"},
		{"{", "reading a record's key"},
		{`{"a":`, "reading a record"},
		{"[1", "reading a record"},
		{"[}", "reading a record: invalid character '}'"},
		{"{\"a\":1]", "reading a record: invalid character ']'"},
		{"[1}", "reading a record: invalid character '}'"},
		{"{} x", "reading a record: invalid character 'x'"},
		{"{} {}", "more than one JSON value"},
	} {
		t.Run(tc.document, func(t *testing.T) {
			record, err := Decode([]byte(tc.document))
			if err == nil {
				t.Fatalf("%q decoded to %v, and it is not one JSON value", tc.document, record)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%q failed with %q, wanted it to name %q", tc.document, err, tc.wantErr)
			}
		})
	}
}

// TestTheSurrogateScannerAnswersForSpansNoDocumentCanHold is the scanner's own
// edge: it is handed raw bytes, and every span it cannot read is answered false
// — "no lone surrogate here" — rather than by reading past the end of the
// literal. The spans a decoder can produce are the first four rows; the rest
// are the ones it cannot, and they are here because a scanner that panicked or
// looped on one would take the decoder with it.
func TestTheSurrogateScannerAnswersForSpansNoDocumentCanHold(t *testing.T) {
	for _, tc := range []struct {
		name string
		span string
		want bool
	}{
		{"a literal with no escape", `"plain"`, false},
		{"a lone high half", `"\uD800"`, true},
		{"a lone low half", `"\uDC00"`, true},
		{"a pair", `"\uD83D\uDE00"`, false},
		{"an escaped backslash before the escape", `"\\uD800"`, false},
		{"a high half whose pair does not follow", `"\uD83D\u0041"`, true},
		{"no quote at all", "plain", false},
		{"a literal ending in a backslash", `"a\"`, false},
		{"an escape with no room for its digits", `"\u1"`, false},
		{"an escape whose digits are not hexadecimal", `"\uZZZZ"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasLoneSurrogateEscape([]byte(tc.span)); got != tc.want {
				t.Errorf("the scanner answers %v for %s, wanted %v", got, tc.span, tc.want)
			}
		})
	}
}

// TestTheVerdictsForARecordThatIsNotThere: both validators answer nil with the
// one refusal that has no field, and IsMessage is the same verdict as a
// boolean — a filter's whole API, and a function no test called before this.
func TestTheVerdictsForARecordThatIsNotThere(t *testing.T) {
	if IsMessage(nil) {
		t.Error("IsMessage answers true for no record at all")
	}
	if _, refusal := ValidateMessage(nil); refusal == nil || refusal.Code != CodeNotAnObject {
		t.Errorf("ValidateMessage on no record answers %v, wanted not-an-object", refusal)
	}
	if _, refusal := ValidateHeartbeat(nil); refusal == nil || refusal.Code != CodeNotAnObject || refusal.Field != "" {
		t.Errorf("ValidateHeartbeat on no record answers %v, wanted not-an-object with no field", refusal)
	}

	message := plainMessage(t)
	if !IsMessage(message) {
		t.Error("IsMessage answers false for the vector file's own plain message")
	}
	if IsMessage(Record{"at": number("1700000000000")}) {
		t.Error("IsMessage answers true for a message with no tag")
	}
	beat := Record{"at": number("1700000000000"), "boot": "boot-01", "mac": strings.Repeat("f", MACMax)}
	if _, refusal := ValidateHeartbeat(beat); refusal != nil {
		t.Fatalf("the test's own heartbeat is refused %s", refusal)
	}
	if IsMessage(beat) {
		t.Error("IsMessage answers true for a heartbeat")
	}
}

// TestTheSerialiserSpellsABooleanAsItself: a boolean is a value a record
// carries, so it has a canonical form and it is `true` or `false` — not `1`,
// not `\u0000`. And serialiseNumber, which serialiseValue reaches only for a
// value it has already found to be a number, still refuses a value that is not
// one rather than printing a zero: it is the same function a later caller
// reaches with a type this package does not speak.
func TestTheSerialiserSpellsABooleanAsItself(t *testing.T) {
	got, err := CanonicalBytes(Record{"flag": true, "off": false}, "inbox")
	if err != nil {
		t.Fatalf("a record with two booleans has no canonical form: %v", err)
	}
	if want := `{"direction":"inbox","flag":true,"off":false}`; string(got) != want {
		t.Errorf("it serialised to %s, wanted %s", got, want)
	}
	if _, err := serialiseNumber("x"); err == nil {
		t.Error("serialiseNumber answers a string with no error")
	} else if !strings.Contains(err.Error(), "not a value a message carries") {
		t.Errorf("serialiseNumber failed with %q, wanted it to name a type this package does not carry", err)
	}
}
