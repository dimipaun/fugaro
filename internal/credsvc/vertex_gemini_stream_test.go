package credsvc

// vertex_gemini_stream_test.go holds GV2's own tests: Vertex's answer for a
// Gemini model, whole or streamed, translated back into what the agent's
// harness reads.
//
// Three things are pinned and no more.
//
// First, the translation, as golden pairs: testdata/vertex_gemini_stream holds
// Vertex's answer and the Messages answer it must become, and a pair is
// compared as the JSON documents they are. The whole-answer pairs are read from
// disk rather than written in the test so that what the agent is handed is
// reviewable as an answer, and a reviewer can hold it against the pages the
// file's header cites (U8: the wire is not in this repository). The streamed
// pairs are compared as the EVENTS they are — parsed on both sides, so the
// answer is the sequence of events and not the bytes Go happened to marshal —
// because the stream's shape is what the harness's own reader switches on.
//
// Second, the refusal: an answer this translation has no faithful form for ends
// the call with a typed reason, exactly one audit line, and no value of the
// answer anywhere. The whole-answer cases are driven straight at the
// translation; the streamed one is driven through serveVertex, because "never a
// half-translated stream" is a fact about the SERVICE — the answer is
// accumulated before a byte is written, so nothing of it reaches the client.
//
// Third, the limit (U7's sibling): a 429 and Google's own quota refusal mark the
// source and hand the walk a row to move on with, whatever status carried the
// refusal, and the attempt is settled at zero rather than eating the
// workspace's month.
//
// The streamed pairs are also where the usage rule is pinned against the reader
// that prices it: the last subtest of TestGeminiStreamTranslation feeds the
// translated stream to the service's own callUsage — the reader the workspace's
// meter uses — and asserts it reads both counts, which is the whole of "the
// usage figures the gateway prices by".

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// geminiAnswersDir is where GV2's golden pairs live, one file per side of a
// pair: `<name>.google.json` is Vertex's answer, `<name>.anthropic.json` the
// Messages answer it becomes, and a streamed pair is `<name>.google.json` (the
// JSON array streamGenerateContent answers with) against
// `<name>.anthropic.sse`.
const geminiAnswersDir = "testdata/vertex_gemini_stream"

// geminiAnswerModel is the model every pair is translated for: the canonical ID
// the agent asked with, which is what the answer's `model` member carries (the
// gateway prices by it, and the pricing table is keyed by canonical IDs).
const geminiAnswerModel = "google/gemini-3-flash"

// geminiLimitBody is a Messages body naming a Gemini model, for the tests about
// the marks a limited source leaves: nothing in it is under test beyond the
// model, which is what makes the call one this service builds for the Google
// publisher and reads back the same way.
const geminiLimitBody = `{"model":"` + geminiAnswerModel + `","max_tokens":16,"stream":false,` +
	`"messages":[{"role":"user","content":"gemini-limit"}]}`

// geminiAnswerSide reads one side of a golden pair.
func geminiAnswerSide(t *testing.T, name, side string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(geminiAnswersDir, name+"."+side))
	if err != nil {
		t.Fatalf("reading the %s side of the %q pair: %v", side, name, err)
	}
	return raw
}

// geminiWhole is one whole GenerateContentResponse from the pairs, as the fake
// answers a call.
func geminiWhole(t *testing.T, name string) anthropicfake.Reply {
	t.Helper()
	return anthropicfake.Reply{Status: http.StatusOK, Body: string(geminiAnswerSide(t, name, "google.json"))}
}

// geminiStreamed is the JSON array streamGenerateContent answers with — the
// form this service reads, because it asks Vertex for no `alt` at all — as the
// fake answers a streamed call.
func geminiStreamed(t *testing.T, name string) anthropicfake.Reply {
	t.Helper()
	// The media type is Vertex's own for this method: the JSON array, not an
	// event stream. What the agent is answered is the event stream this
	// translation writes.
	return anthropicfake.Reply{Status: http.StatusOK, Body: string(geminiAnswerSide(t, name, "google.json"))}
}

// geminiCounted is Vertex's CountTokensResponse.
func geminiCounted(total int64) anthropicfake.Reply {
	return anthropicfake.Reply{Status: http.StatusOK, Body: `{"totalTokens":` + strconv.FormatInt(total, 10) + `}`}
}

// geminiEvent is one event of an event stream, parsed: its name and its data as
// the document it is.
type geminiEvent struct {
	Name string
	Data any
}

// geminiEvents parses an event stream into the events it carries, which is the
// comparison both sides of a streamed pair go through: the stream's answer is
// its events, and a test that compared raw bytes would pin Go's own key order
// and its spacing rather than the answer.
func geminiEvents(t *testing.T, raw []byte) []geminiEvent {
	t.Helper()
	var out []geminiEvent
	for _, block := range strings.Split(string(raw), "\n\n") {
		if strings.TrimSpace(block) == "" {
			continue
		}
		var ev geminiEvent
		var data string
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				ev.Name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			}
		}
		if data == "" {
			t.Fatalf("an event of this stream carries no data: %q", block)
		}
		if err := json.Unmarshal([]byte(data), &ev.Data); err != nil {
			t.Fatalf("an event of this stream carries data that is not one JSON document: %v (%q)", err, data)
		}
		out = append(out, ev)
	}
	return out
}

// TestGeminiAnswerTranslation: Vertex's whole answer becomes the Messages
// answer the agent's harness reads — its text, its tool calls, its stop reason
// and the usage figures the gateway prices by — and an answer this translation
// has no form for ends the call with a typed reason instead.
//
// The pairs are the fact and not an illustration of it. `text` is a plain
// answer; `tool_call` is the round trip GV1 reads back, a functionCall whose
// thought signature becomes the tool_use id, with Vertex's thought tokens
// counted into the output (Vertex bills them as output, so a count that left
// them out would settle a call below what Vertex charges it); `refusal` is a
// prompt Vertex blocked, which is Anthropic's `refusal` and not a refusal of
// the call; `cached` is the cache counts read apart from the prompt, which is
// what Vertex's promptTokenCount includes and Anthropic's input_tokens does
// not.
//
// Mutation (run, restore): read `totalTokenCount` as the output count, leave the
// thought tokens out, carry the signature as it came rather than re-encoding it,
// map a safety block onto a stop reason of its own, or mint an unsigned call's id
// from its position alone, and a case here is red.
func TestGeminiAnswerTranslation(t *testing.T) {
	for _, name := range []string{"text", "tool_call", "refusal", "cached"} {
		t.Run(name, func(t *testing.T) {
			got, err := vertexGeminiAnswer(geminiAnswerSide(t, name, "google.json"), geminiAnswerModel)
			if err != nil {
				t.Fatalf("the %q answer was refused: %v", name, err)
			}
			want := geminiDoc(t, geminiAnswerSide(t, name, "anthropic.json"))
			if doc := geminiDoc(t, got); !reflect.DeepEqual(doc, want) {
				t.Errorf("the translation of the %q answer is not the Messages answer the agent reads:\ngot  %s\nwant %s",
					name, indent(t, doc), indent(t, want))
			}
		})
	}

	t.Run("a count is answered as the count the agent reads", func(t *testing.T) {
		got, err := vertexGeminiCount([]byte(`{"totalTokens":1234}`))
		if err != nil {
			t.Fatalf("Vertex's counting answer was refused: %v", err)
		}
		if want := geminiDoc(t, []byte(`{"input_tokens":1234}`)); !reflect.DeepEqual(geminiDoc(t, got), want) {
			t.Errorf("the count answer is %s, want %s: Google counts the whole input as totalTokens and Anthropic counts it as input_tokens", got, want)
		}
	})

	// The acceptance is a fact about the SERVICE and not about the
	// translation: what the agent's harness receives is the translated answer,
	// in the media type the harness reads it by, and the answer's `model` is
	// the ID the agent asked with — what the gateway's own ledger prices by —
	// and not the spelling Vertex's path carries.
	t.Run("the service hands the agent the translated answer", func(t *testing.T) {
		s, _, _ := vertexGeminiFixture(t, geminiWhole(t, "tool_call"))
		cl := vertexCall(t, MessagesPath, string(geminiPair(t, "tools", "anthropic")))
		done, _, rec, _ := serveVertexCall(t, s, vertexIdentity(s, testProject), cl, testVertexSource())
		if !done || rec.Code != http.StatusOK {
			t.Fatalf("the call is answered %d (%v), want it served: %s", rec.Code, done, rec.Body)
		}
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, contentTypeJSON) {
			t.Errorf("the served answer's media type is %q, want %q: what the service hands on is its own JSON", got, contentTypeJSON)
		}
		if want := geminiDoc(t, geminiAnswerSide(t, "tool_call", "anthropic.json")); !reflect.DeepEqual(geminiDoc(t, rec.Body.Bytes()), want) {
			t.Errorf("the answer the agent read is not the translated one:\ngot  %s\nwant %s", rec.Body.String(), indent(t, want))
		}
	})

	for _, tc := range []struct {
		name   string
		answer string
		member string
	}{{
		name:   "an answer that names no finish reason",
		answer: `{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"index":0}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1}}`,
		member: "candidates[0].finishReason",
	}, {
		name:   "a finish reason with no stop_reason that means the same",
		answer: `{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"MALFORMED_FUNCTION_CALL","index":0}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1}}`,
		member: "candidates[0].finishReason",
	}, {
		name:   "a second candidate carrying content",
		answer: `{"candidates":[{"content":{"role":"model","parts":[{"text":"one"}]},"finishReason":"STOP","index":0},{"content":{"role":"model","parts":[{"text":"two"}]},"finishReason":"STOP","index":1}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2}}`,
		member: "candidates[1].content",
	}, {
		name:   "a part of a kind this translation has no form for",
		answer: `{"candidates":[{"content":{"role":"model","parts":[{"inlineData":{"mimeType":"image/png","data":"` + geminiPlanted + `"}}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1}}`,
		member: "candidates[0].content.parts[0]",
	}, {
		name:   "a part that is a thought",
		answer: `{"candidates":[{"content":{"role":"model","parts":[{"text":"` + geminiPlanted + `","thought":true}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":5,"thoughtsTokenCount":4}}`,
		member: "candidates[0].content.parts[0]",
	}, {
		name:   "an answer that reports no usage",
		answer: `{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP","index":0}]}`,
		member: "usageMetadata",
	}, {
		name:   "a cached count above the prompt",
		answer: `{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":10,"cachedContentTokenCount":40,"candidatesTokenCount":1}}`,
		member: "usageMetadata.cachedContentTokenCount",
	}, {
		name:   "a function call whose signature cannot be read",
		answer: `{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"read_file","args":{}},"thoughtSignature":"not-base64!!"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1}}`,
		member: "candidates[0].content.parts[0].thoughtSignature",
	}, {
		name:   "a candidate that is not the model's own turn",
		answer: `{"candidates":[{"content":{"role":"user","parts":[{"text":"hi"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1}}`,
		member: "candidates[0].content.role",
	}, {
		name:   "an answer that is not one JSON object",
		answer: `{"candidates":[`,
		member: "answer",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := vertexGeminiAnswer([]byte(tc.answer), geminiAnswerModel)
			if err == nil {
				t.Fatalf("an answer this translation cannot carry was answered as %s, want a typed refusal: a member that cannot be carried is refused, never dropped", got)
			}
			reason, typed := vertexAnswerRefusal(err)
			if !typed {
				t.Fatalf("the refusal is not the typed one (%v): an operator cannot tell what this translation could not carry", err)
			}
			if !strings.Contains(reason, tc.member) {
				t.Errorf("the refusal does not name the member it is about (%s): %s", tc.member, reason)
			}
			if strings.Contains(reason, geminiPlanted) {
				t.Errorf("the refusal carries a value of the answer: %s", reason)
			}
		})
	}

	// The harness pairs a tool_result with the call it answers BY ID, and the
	// ids it sees span the whole conversation, not one answer: a model whose
	// calls carry no thought signature — every call of a model that sends none,
	// and each parallel call of a Gemini 3 answer — would otherwise mint the
	// same id in every turn, and a harness keying on it would hand one turn's
	// result to another turn's call. So a minted id carries the answer's own
	// identity (vertexGeminiMint) and two answers never mint the same one, and
	// it never opens with vertexGeminiThoughtSignatureID, which is what GV1
	// reads a thought signature off — a minted id is an unsigned call and must
	// be read back as one.
	t.Run("two answers never mint the same call id", func(t *testing.T) {
		answer := func(tail string) []byte {
			return []byte(`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"read_file","args":{"path":"a.go"}}}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":4}` + tail + `}`)
		}
		theID := func(t *testing.T, raw []byte) string {
			t.Helper()
			got, err := vertexGeminiAnswer(raw, geminiAnswerModel)
			if err != nil {
				t.Fatalf("the answer was refused: %v", err)
			}
			doc, _ := geminiDoc(t, got).(map[string]any)
			content, _ := doc["content"].([]any)
			if len(content) != 1 {
				t.Fatalf("the answer carries %d blocks, want one tool_use: %s", len(content), got)
			}
			block, _ := content[0].(map[string]any)
			if block["type"] != "tool_use" {
				t.Fatalf("the answer's one block is %v, want tool_use: %s", block["type"], got)
			}
			id, _ := block["id"].(string)
			if id == "" {
				t.Fatalf("the tool_use block carries no id: %s", got)
			}
			if strings.HasPrefix(id, vertexGeminiThoughtSignatureID) {
				t.Errorf("the minted id %q opens with the thought-signature prefix: GV1 reads a signature off every id that does, so this unsigned call would be read back as a signed one", id)
			}
			return id
		}
		one, two := theID(t, answer(`,"responseId":"answerOne"`)), theID(t, answer(`,"responseId":"answerTwo"`))
		if one == two {
			t.Errorf("two answers that name themselves differently minted the same id %q: the answer's own identity is what makes a minted id unique across the conversation's turns", one)
		}
		// The fallback for an answer that names itself with no responseId: its
		// bytes are its identity, so two answers that differ still mint
		// different ids.
		unnamed, other := theID(t, answer("")), theID(t, answer(`,"modelVersion":"gemini-3-flash-001"`))
		if unnamed == other {
			t.Errorf("two answers with no responseId minted the same id %q: an answer's own bytes are its identity when it names nothing", unnamed)
		}
		if unnamed == one {
			t.Errorf("an answer with a responseId and one without minted the same id %q", one)
		}
	})
}

// TestGeminiStreamTranslation: a streamed answer becomes one Anthropic event
// stream, and the whole answer is accumulated before a byte of it is written —
// so a chunk that cannot be translated is a call that ends with a typed error
// and never a stream half of whose answer the agent has already read.
//
// `stream_text` is one text answer spread over three chunks, which must reach
// the agent as ONE text block; `stream_tool_call` is a text block and two
// function calls (one carrying a thought signature, one not), each of them
// whole inside its own chunk; `stream_same_call` is two calls to the SAME
// function in consecutive chunks, which must reach the agent as two tool_use
// blocks with distinct IDs.
//
// Mutation (run, restore): emit one text block per chunk, merge a later call
// into the one before it, or write the events as they are accumulated rather
// than after the last chunk, and a case here is red.
func TestGeminiStreamTranslation(t *testing.T) {
	for _, name := range []string{"stream_text", "stream_tool_call", "stream_same_call"} {
		t.Run(name, func(t *testing.T) {
			got, err := vertexGeminiStream(geminiAnswerSide(t, name, "google.json"), geminiAnswerModel)
			if err != nil {
				t.Fatalf("the %q stream was refused: %v", name, err)
			}
			want := geminiEvents(t, geminiAnswerSide(t, name, "anthropic.sse"))
			if events := geminiEvents(t, got); !reflect.DeepEqual(events, want) {
				t.Errorf("the translation of the %q stream is not the event stream the agent reads:\ngot  %s\nwant %s",
					name, indent(t, events), indent(t, want))
			}
		})
	}

	// Two calls to the same function are two calls, in consecutive chunks as
	// much as inside one: GV1 asks Vertex for no streamed arguments (no
	// partialArgs/willContinue, the one request shape that makes Vertex answer a
	// call in pieces), so a functionCall part is one whole call and a later
	// chunk carrying the same name is a second one, never the first continued.
	// One block where the model made two would hand the harness a call it never
	// replays, and the first call's arguments would be gone with nothing to show
	// it: no error, no line, just an action the run never takes.
	t.Run("two calls to the same function are two blocks", func(t *testing.T) {
		got, err := vertexGeminiStream(geminiAnswerSide(t, "stream_same_call", "google.json"), geminiAnswerModel)
		if err != nil {
			t.Fatalf("the stream was refused: %v", err)
		}
		var blocks []map[string]any
		for _, ev := range geminiEvents(t, got) {
			if ev.Name != "content_block_start" {
				continue
			}
			doc, ok := ev.Data.(map[string]any)
			if !ok {
				continue
			}
			block, _ := doc["content_block"].(map[string]any)
			if block["type"] == "tool_use" {
				blocks = append(blocks, block)
			}
		}
		if len(blocks) != 2 {
			t.Fatalf("the stream carries %d tool_use blocks, want 2: the model called the same function twice, and one block is one call the harness never replays", len(blocks))
		}
		for i, block := range blocks {
			if block["name"] != "read_file" {
				t.Errorf("block %d is named %v, want read_file on both: the merge this test is about is keyed on the function's name", i, block["name"])
			}
		}
		if blocks[0]["id"] == blocks[1]["id"] {
			t.Errorf("the two blocks share the ID %v: two blocks sharing one ID leave the tool_result the harness sends back answering both", blocks[0]["id"])
		}
	})

	// What the agent's own reader switches on is the media type and the shape
	// of the events, and both are this service's: Vertex answered a JSON array
	// (the media type the fake labels it with), and the agent must read an
	// event stream.
	t.Run("a served stream arrives as an event stream", func(t *testing.T) {
		s, _, _ := vertexGeminiFixture(t, geminiStreamed(t, "stream_text"))
		cl := vertexCall(t, MessagesPath, string(geminiPair(t, "sampling", "anthropic")))
		done, _, rec, _ := serveVertexCall(t, s, vertexIdentity(s, testProject), cl, testVertexSource())
		if !done || rec.Code != http.StatusOK {
			t.Fatalf("the streamed call is answered %d (%v), want it served: %s", rec.Code, done, rec.Body)
		}
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
			t.Errorf("the served stream's media type is %q, want text/event-stream: Vertex answers this method with a JSON array, and what the agent reads is an event stream", got)
		}
		want := geminiEvents(t, geminiAnswerSide(t, "stream_text", "anthropic.sse"))
		if events := geminiEvents(t, rec.Body.Bytes()); !reflect.DeepEqual(events, want) {
			t.Errorf("the stream the agent read is not the translated one:\ngot  %s\nwant %s", indent(t, events), indent(t, want))
		}
	})

	t.Run("a chunk that cannot be translated writes nothing at all", func(t *testing.T) {
		stream := `[
			{"candidates":[{"content":{"role":"model","parts":[{"text":"half an answer"}]},"index":0}],"usageMetadata":{"promptTokenCount":5}},
			{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"read_file","args":{}},"thoughtSignature":"not-base64!!"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1}}
		]`
		got, err := vertexGeminiStream([]byte(stream), geminiAnswerModel)
		if err == nil {
			t.Fatalf("a stream whose last chunk cannot be translated was answered as %s, want a typed refusal", got)
		}
		if len(got) != 0 {
			t.Errorf("the refusal wrote %d bytes of the stream: a half-translated stream is an answer the agent has already read, and the rule is that it never exists", len(got))
		}
		if _, typed := vertexAnswerRefusal(err); !typed {
			t.Errorf("the refusal is not the typed one (%v)", err)
		}
	})

	t.Run("a stream that is not one JSON array is refused", func(t *testing.T) {
		for _, stream := range []string{`{"candidates":[]}`, `[]`, `[{"candidates":[]}`, `[{"candidates":[]}] trailing`} {
			if got, err := vertexGeminiStream([]byte(stream), geminiAnswerModel); err == nil {
				t.Errorf("the answer %q was translated as %s, want a typed refusal: this method's answer is one JSON array and this is not one", stream, got)
			}
		}
	})

	// The streamed answer is the one the service's own meter reads as it goes
	// out, and this is that reading: the translated events must carry both
	// counts, or the workspace's call settles at its reservation and the run's
	// own gateway prices a figure nothing measured (C1.8, WS8).
	t.Run("the translated stream is what the meter prices", func(t *testing.T) {
		got, err := vertexGeminiStream(geminiAnswerSide(t, "stream_text", "google.json"), geminiAnswerModel)
		if err != nil {
			t.Fatalf("the stream was refused: %v", err)
		}
		use := newCallUsage(true)
		use.feed(got)
		use.finish()
		if !use.complete {
			t.Fatalf("the service's own reader did not read the translated stream as a complete answer (started=%v deltas=%v broken=%v): the gateway settles such a call at its reservation", use.started, use.deltas, use.broken)
		}
		row := wsRow(t, geminiAnswerModel)
		if got, want := use.usage(row.Rates, "").Input, int64(300); got != want {
			t.Errorf("the reader priced %d input tokens, want %d: the input counts belong to message_start", got, want)
		}
		if got, want := use.usage(row.Rates, "").Output, int64(9); got != want {
			t.Errorf("the reader priced %d output tokens, want %d: the output count belongs to message_delta, and it is the one the call settles on", got, want)
		}
	})
}

// TestAGeminiLimitMovesOn: a limit on the project's Vertex is a limit whatever
// status carried it — Google answers its own quota refusal with 429 and, for a
// project over its quota, with 403 — so it marks the source and hands the walk
// a row to move on with, and the attempt is settled at the zero a refusal is
// worth rather than eating the workspace's month.
//
// The 403 case is the one that matters: read as a permission error, a quota
// refusal would reach the agent as a refusal of its own call and the chain
// would never try the next source.
//
// A Gemini answer is read before the marks are decided, so the refusal is
// recognised in the body and not only in the status.
func TestAGeminiLimitMovesOn(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply anthropicfake.Reply
		reset bool
		why   string
	}{{
		name: "a 429 with a reset",
		reply: func() anthropicfake.Reply {
			reply := anthropicfake.Reply{Status: http.StatusTooManyRequests,
				Body: `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"quota"}}`}
			reply.Header = http.Header{"Retry-After": []string{"120"}}
			return reply
		}(),
		reset: true,
		why:   "the answer named its reset, and the mark must not wait a window somebody invented",
	}, {
		name: "a quota refusal under a 403",
		reply: anthropicfake.Reply{Status: http.StatusForbidden,
			Body: `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"quota"}}`},
		why: "the project is over its quota: this is the same limit a 429 carries, and a 403 read as a permission error would end the call instead of moving the chain on",
	}, {
		name: "a quota refusal inside a 200",
		reply: anthropicfake.Reply{Status: http.StatusOK,
			Body: `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"quota"}}`},
		why: "the body says what the status does not, and the answer is read whole before anything decides",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			vertexGeminiIdentity(t)
			watch := &vertexWatch{}
			f := newWSMeterSvc(t, wsCapUSD, nil, func(_ *svc, cfg *Config) { wrapVertexClient(cfg, watch) })
			f.up.Script = []anthropicfake.Reply{tc.reply}
			ctx := withScope(context.Background(), wsMeterScope{
				installationScope: installationScope{s: f.server},
				meter:             f.meter,
			})

			cl := vertexCall(t, MessagesPath, geminiLimitBody)
			done, tried, rec, _ := serveVertexCallIn(t, f.svc, ctx, vertexIdentity(f.svc, wsMemberA), cl, testVertexSource())
			if done {
				t.Fatalf("the source answered a call Vertex refused with a limit: %d %s", rec.Code, rec.Body)
			}
			if want := (Tried{Name: config.CredentialVertex, Tier: TierB, Reason: ReasonExhausted}); tried != want {
				t.Errorf("the row beside the declined call = %+v, want %+v: the chain moves on, or the walk answers exhausted (%s)", tried, want, tc.why)
			}
			if n := f.up.Count(); n != 1 {
				t.Errorf("Vertex saw %d requests, want the one that was limited", n)
			}
			// The room the attempt took is what makes "settled at zero" a fact
			// and not a vacuum: the reservation went out with the call, and the
			// assertion below is that the refusal gave it back. What it bounds
			// is what goes out, which for this publisher is the translated
			// Google body — one that names no `max_tokens` at all, so the bound
			// is the model's own output ceiling (the safe direction) and not the
			// 16 the agent asked for.
			sent := f.up.Body(0)
			row := wsRow(t, geminiAnswerModel)
			want := row.WorstCase(pricing.Request{BodyBytes: int64(len(sent)), MaxTokens: row.MaxOutputTokens})
			if got := f.watch.reservedAt(t, 0); got.Reserved != want {
				t.Errorf("at the limited call the workspace held %v of room, want the translated body's own worst case %v: the room is taken before the request goes out",
					got.Reserved.USD(), want.USD())
			}
			m, ok := vertexMark(t, f.svc)
			if !ok {
				t.Fatalf("Vertex left no mark on the source: %s", tc.why)
			}
			if m.Usable(f.now, creds.Settings{}) {
				t.Errorf("the source is still usable after Vertex answered it with a limit (%+v): %s", m, tc.why)
			}
			if tc.reset {
				if want := f.now.Add(120 * time.Second); m.State != creds.StateExhausted || !m.Until.Equal(want) {
					t.Errorf("the mark is %+v, want an exhausted one until the reset the answer reported (%v): the mark must not wait a window somebody invented", m, want)
				}
			} else if m.State != creds.StateBackedOff {
				t.Errorf("the mark is %+v, want the backoff a limit with no readable reset leaves: Google states its retry delay in the refusal body, which this build does not read, so the hold is the package's own (a deferred improvement, and this assertion is where it lands)", m)
			}
			if got := f.audit.String(); got != "" {
				t.Errorf("the declined source wrote %q, want nothing: the walk writes the line for a call it could not place", got)
			}
			room := f.wsRoom(t)
			if room.Settled != 0 || room.Reserved != 0 {
				t.Errorf("the workspace holds %v settled and %v reserved after a refused call, want nothing: an attempt Vertex did not bill must not eat the workspace's month",
					room.Settled.USD(), room.Reserved.USD())
			}
		})
	}
}
