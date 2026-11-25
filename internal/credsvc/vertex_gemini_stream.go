// vertex_gemini_stream.go is GV2 of docs/model-credentials-routing/implementation-plan.md:
// the ANSWER half of the spec's `gemini-* → Vertex` (D3193). GV1 built the
// request half — a Messages call becomes a generateContent call on Vertex's
// GOOGLE publisher — and what comes back from there is a GenerateContentResponse
// and not the Messages answer the agent's harness reads. This file is that
// translation, whole or streamed, and it is the half that closes the round trip
// GV4 proves end to end.
//
// **The wire is not in this repository (U8 of the reconciliation)**, so every
// mapping below cites the page it was read from, as GV1's does:
//
//   - GenerateContentResponse — `candidates`, `Candidate.content`,
//     `Candidate.finishReason` and its values (STOP, MAX_TOKENS, the safety
//     family, MALFORMED_FUNCTION_CALL, OTHER), `Candidate.index`,
//     `GenerateContentResponse.promptFeedback.blockReason` and `responseId`:
//     https://cloud.google.com/vertex-ai/generative-ai/docs/reference/rest/v1/projects.locations.publishers.models/generateContent
//   - Content / Part — `Part.text`, `Part.functionCall` (name and args),
//     `Part.thoughtSignature`, `Part.thought`, and the other part kinds
//     (inlineData, fileData, executableCode, codeExecutionResult):
//     https://cloud.google.com/vertex-ai/generative-ai/docs/model-reference/inference
//   - streamGenerateContent — the streaming method GV1 chooses for a `stream`
//     call, whose REST body without `alt=sse` is a JSON ARRAY of
//     GenerateContentResponse objects, which is the one form this translation
//     reads: this service asks for no `alt` at all (vertex.go's
//     vertexDestination drops the caller's query, because the call is made on
//     the service's own identity):
//     https://cloud.google.com/vertex-ai/generative-ai/docs/reference/rest/v1/projects.locations.publishers.models/streamGenerateContent
//   - UsageMetadata — `promptTokenCount` ("When cachedContent is set, this is
//     still the total effective prompt size meaning this includes the number of
//     tokens in the cached content"), `cachedContentTokenCount`,
//     `candidatesTokenCount` and `thoughtsTokenCount`:
//     https://cloud.google.com/vertex-ai/generative-ai/docs/model-reference/inference
//     and, for why a thought token is billed as an output one, the thinking
//     page GV1's header cites:
//     https://cloud.google.com/vertex-ai/generative-ai/docs/thinking
//   - CountTokensResponse — `totalTokens`, the counting endpoint GV1 builds:
//     https://cloud.google.com/vertex-ai/generative-ai/docs/reference/rest/v1/projects.locations.publishers.models/countTokens
//   - the error model of a Google API — `error.code` and `error.status`, whose
//     `RESOURCE_EXHAUSTED` is the quota refusal a chain must move on from:
//     https://cloud.google.com/apis/design/errors
//
// The Anthropic half is the Messages answer and its stream
// (https://platform.claude.com/docs/en/api/messages and
// https://platform.claude.com/docs/en/api/messages-streaming): `content` blocks
// of type text and tool_use, `stop_reason` (`end_turn`, `max_tokens`,
// `tool_use`, `refusal`), and a `usage` object whose `input_tokens` excludes the
// cache counts, which are reported apart.
//
// Two rules shape the whole file.
//
// The first is GV1's, read for an answer: **nothing the agent reads is invented
// and nothing is half-said**. A member of Vertex's answer this translation has
// no form for — a part that is neither text nor a function call, a candidate
// beyond the first, a finish reason with no Anthropic word, an answer that
// reports no usage — ends the call with a typed reason
// (vertexAnswerUntranslatable) rather than reaching the agent as a block or a
// stop reason nobody measured. A wrong stop reason is the worst of them: it is
// what tells the harness whether to keep going or stop.
//
// The second is why the stream is translated WHOLE and not as it arrives. A
// streamed answer is accumulated across its chunks and only then written out, so
// a chunk that cannot be translated is a call that ends with a typed error and
// never a stream half of whose answer the agent has already read — the
// acceptance's own words. What that costs is latency on a long answer: the
// service holds the upstream stream and hands the agent one translated event
// stream. An incremental reader is a later task, and it would have to answer
// this rule first.
//
// The round trip GV1 anchored is closed here: a functionCall the model answered
// with a thoughtSignature becomes a tool_use block whose ID CARRIES that
// signature (vertexGeminiThoughtSignatureID), because Gemini 3 checks the
// signature of every functionCall it is sent back and an ID is the one member of
// a tool call a harness echoes back verbatim. GV1 reads it out again on the
// request side; the two are one mapping and this file is its other half.

package credsvc

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	// vertexGeminiMaxAnswer bounds what this translation reads of one answer.
	// It is the meter's own bound (meterMaxBody), because a stream this
	// service cannot read is one it cannot price either: past it the answer
	// is refused with a typed reason rather than translated from a prefix.
	vertexGeminiMaxAnswer = meterMaxBody

	// vertexGeminiMintedCallID is the ID a tool call gets when Vertex answered
	// it without a thought signature. Vertex's `v1` functionCall carries no ID
	// of its own, so one is minted here — the call's own position in the answer
	// — and an ID is what the harness echoes back when it replays the call, so
	// the same ID comes back on the request side (GV1).
	vertexGeminiMintedCallID = "toolu_vrtx_"

	// vertexGeminiMessageID is the answer's own id when Vertex sent no
	// responseId. Nothing in this repository reads it and the harness only
	// echoes it, but the Messages answer's `id` is not optional.
	vertexGeminiMessageID = "msg_vrtx_0000000000000000"

	// vertexGeminiAnthropicIDPrefix is what a Vertex responseId is written
	// with to make the Messages answer's `id` the shape a harness expects.
	vertexGeminiAnthropicIDPrefix = "msg_"
)

// vertexAnswerUntranslatable is the typed reason an answer from Vertex's Google
// publisher could not be carried into the Messages answer the harness reads:
// the member's PATH in the answer (never its content, and never a value of the
// call) and the sentence that says why. It is the answer half of GV1's
// vertexUntranslatable, and it is answered as a refusal the caller sees: the
// call reached the source and the source answered something this wire has no
// translation for, so the call ends here rather than at another source.
type vertexAnswerUntranslatable struct {
	Member string
	Why    string
}

func (e *vertexAnswerUntranslatable) Error() string {
	return "the answer's " + e.Member + " has no form on the Messages wire: " + e.Why
}

// untranslatableAnswer is the typed refusal above, written once.
func untranslatableAnswer(member, why string) error {
	return &vertexAnswerUntranslatable{Member: member, Why: why}
}

// vertexAnswerRefusal is the sentence a call is refused with when Vertex's
// answer could not be translated, and whether the error is that refusal at all.
// Every other error out of this file is the service's own reading of the answer
// (bytes that are not the JSON document this wire is).
//
// The sentence is built from the typed reason's PATH and its own why, both of
// which this package wrote: nothing the answer carried is in it.
func vertexAnswerRefusal(err error) (string, bool) {
	var reason *vertexAnswerUntranslatable
	if !errors.As(err, &reason) {
		return "", false
	}
	return "the model's answer on Vertex's Google publisher could not be translated into the answer the agent reads, and " +
		reason.Error() + "; a member this translation cannot carry is refused rather than half-said, because an answer the agent cannot read is worse than a call that ends", true
}

// vertexGeminiTranslated translates one whole answer from Vertex's Google
// publisher: the Messages answer, the event stream, or the token count, decided
// by the path the call took (GV1's two). stream is the caller's own member, and
// it is the same fact GV1 chose the method with.
func vertexGeminiTranslated(callPath string, stream bool, body []byte, model string) ([]byte, string, error) {
	switch callPath {
	case CountTokensPath:
		out, err := vertexGeminiCount(body)
		return out, contentTypeJSON, err
	case MessagesPath:
		if stream {
			out, err := vertexGeminiStream(body, model)
			return out, "text/event-stream", err
		}
		out, err := vertexGeminiAnswer(body, model)
		return out, contentTypeJSON, err
	}
	return nil, "", untranslatableAnswer("answer", "this source answered a path it does not translate")
}

// vertexGeminiQuota reports whether an answer is Vertex's own quota refusal —
// the error model of a Google API, whose `status` is RESOURCE_EXHAUSTED and
// whose `code` is 429 — whatever HTTP status carried it. A chain must move on
// from a quota error as it does from a 429, and Vertex does not always answer
// one with 429: a project over its quota is refused with 403 and the same error
// body, which without this check would reach the agent as a permission error.
func vertexGeminiQuota(body []byte) bool {
	var doc struct {
		Error struct {
			Code   int    `json:"code"`
			Status string `json:"status"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &doc) != nil {
		return false
	}
	return doc.Error.Status == "RESOURCE_EXHAUSTED" || doc.Error.Code == 429
}

// vertexGeminiError translates one of Vertex's error answers into the error
// shape the harness reads, keeping the status the answer came with. An answer
// that is not Google's error object at all is answered as it came: it is an
// error either way, and a status this service did not choose is not one it may
// rewrite. It is the one translation here that never refuses — an error the
// agent cannot read as an Anthropic error is still an error it acts on.
func vertexGeminiError(status int, body []byte) []byte {
	var doc struct {
		Error struct {
			Code    int    `json:"code"`
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &doc) != nil || (doc.Error.Status == "" && doc.Error.Code == 0) {
		return body
	}
	out, err := json.Marshal(map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    vertexGeminiErrorType(status, doc.Error.Status),
			"message": doc.Error.Message,
		},
	})
	if err != nil {
		return body
	}
	return out
}

// vertexGeminiErrorType is Google's error named in Anthropic's own vocabulary:
// the two models agree on what went wrong and spell it differently, and the
// harness switches on the Anthropic spelling. An unknown status is api_error,
// which is Anthropic's own catch-all.
func vertexGeminiErrorType(status int, googleStatus string) string {
	switch googleStatus {
	case "INVALID_ARGUMENT", "FAILED_PRECONDITION", "OUT_OF_RANGE":
		return "invalid_request_error"
	case "UNAUTHENTICATED":
		return "authentication_error"
	case "PERMISSION_DENIED":
		return "permission_error"
	case "NOT_FOUND":
		return "not_found_error"
	case "RESOURCE_EXHAUSTED":
		return "rate_limit_error"
	case "UNAVAILABLE":
		return "overloaded_error"
	}
	switch {
	case status == 400:
		return "invalid_request_error"
	case status == 401:
		return "authentication_error"
	case status == 403:
		return "permission_error"
	case status == 404:
		return "not_found_error"
	case status == 413:
		return "request_too_large"
	case status == 429:
		return "rate_limit_error"
	case status == 503:
		return "overloaded_error"
	}
	return "api_error"
}

// vertexGeminiCount translates Vertex's CountTokensResponse into the count
// answer the harness reads. Google's counting endpoint answers `totalTokens`,
// the whole input the call would be billed for, and Anthropic's count answers
// `input_tokens`: one figure under two names, and the count is free on both
// sides, so nothing else of the answer is carried.
func vertexGeminiCount(body []byte) ([]byte, error) {
	members, err := geminiObject(body, "answer")
	if err != nil {
		return nil, err
	}
	var total *int64
	for _, m := range members {
		if m.Key != "totalTokens" {
			continue
		}
		n, ok := vertexJSONInt(m.Value)
		if !ok || n < 0 {
			return nil, untranslatableAnswer("totalTokens", "a token count is a whole number of tokens and this is not one")
		}
		total = &n
	}
	if total == nil {
		return nil, untranslatableAnswer("totalTokens", "Vertex's counting answer names no total, so there is no count to answer")
	}
	return json.Marshal(map[string]any{"input_tokens": *total})
}

// vertexGeminiAnswer translates one whole GenerateContentResponse into the
// Messages answer the harness reads.
func vertexGeminiAnswer(body []byte, model string) ([]byte, error) {
	said, err := vertexGeminiAccumulate([]json.RawMessage{body})
	if err != nil {
		return nil, err
	}
	content, err := vertexGeminiContent(said)
	if err != nil {
		return nil, err
	}
	stop, err := vertexGeminiStopReason(said)
	if err != nil {
		return nil, err
	}
	usage, err := vertexGeminiUsage(said)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"id":            said.id,
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       content,
		"stop_reason":   stop,
		"stop_sequence": nil,
		"usage":         usage,
	})
}

// vertexGeminiStream translates one whole streamed answer — the JSON array of
// GenerateContentResponse objects Vertex's streamGenerateContent answers with —
// into one Anthropic event stream.
//
// The whole answer is accumulated before a byte is written (this file's
// header): the events below are the canonical Messages stream — message_start,
// one start/delta/stop group per content block, message_delta with the stop
// reason and the output count, message_stop — and none of them is written
// unless every chunk translated.
func vertexGeminiStream(body []byte, model string) ([]byte, error) {
	chunks, err := vertexGeminiChunks(body)
	if err != nil {
		return nil, err
	}
	said, err := vertexGeminiAccumulate(chunks)
	if err != nil {
		return nil, err
	}
	stop, err := vertexGeminiStopReason(said)
	if err != nil {
		return nil, err
	}
	usage, err := vertexGeminiUsage(said)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := writeVertexSSE(&out, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": said.id, "type": "message", "role": "assistant", "model": model,
			"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
			"usage": vertexGeminiStartUsage(usage),
		},
	}); err != nil {
		return nil, err
	}
	for i, piece := range said.pieces {
		if err := writeVertexSSE(&out, "content_block_start", map[string]any{
			"type": "content_block_start", "index": i, "content_block": vertexGeminiBlockStart(piece, i),
		}); err != nil {
			return nil, err
		}
		if err := writeVertexSSE(&out, "content_block_delta", map[string]any{
			"type": "content_block_delta", "index": i, "delta": vertexGeminiBlockDelta(piece),
		}); err != nil {
			return nil, err
		}
		if err := writeVertexSSE(&out, "content_block_stop", map[string]any{
			"type": "content_block_stop", "index": i,
		}); err != nil {
			return nil, err
		}
	}
	if err := writeVertexSSE(&out, "message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": stop, "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": usage["output_tokens"]},
	}); err != nil {
		return nil, err
	}
	if err := writeVertexSSE(&out, "message_stop", map[string]any{"type": "message_stop"}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// vertexGeminiBlockStart is one block's opening event payload.
func vertexGeminiBlockStart(piece vertexGeminiPiece, index int) map[string]any {
	if piece.call == nil {
		return map[string]any{"type": "text", "text": ""}
	}
	return map[string]any{"type": "tool_use", "id": piece.call.id, "name": piece.call.name, "input": map[string]any{}}
}

// vertexGeminiBlockDelta is one block's delta. A tool call's input is streamed
// as one partial_json of the whole arguments object, which is what the Messages
// stream does when the arguments arrive at once.
func vertexGeminiBlockDelta(piece vertexGeminiPiece) map[string]any {
	if piece.call == nil {
		return map[string]any{"type": "text_delta", "text": piece.text}
	}
	return map[string]any{"type": "input_json_delta", "partial_json": string(piece.call.args)}
}

// vertexGeminiStartUsage is message_start's usage object: the counts the answer
// reported, with output_tokens at the placeholder the Messages stream opens
// with. The real output count is the message_delta's, and it is the one the
// gateway prices.
func vertexGeminiStartUsage(usage map[string]any) map[string]any {
	out := make(map[string]any, len(usage))
	for k, v := range usage {
		out[k] = v
	}
	out["output_tokens"] = 1
	return out
}

// writeVertexSSE writes one server-sent event: its name, its data and the blank
// line that ends it, which is the shape every Anthropic stream event has and
// the shape the gateway's own reader splits on.
func writeVertexSSE(out *bytes.Buffer, name string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	out.WriteString("event: ")
	out.WriteString(name)
	out.WriteString("\ndata: ")
	out.Write(raw)
	out.WriteString("\n\n")
	return nil
}

// vertexGeminiChunks reads a whole streamed answer into the chunks it is made
// of. The body is a JSON array (this file's header cites the method), and it is
// read by GV1's own strict array reader: bytes that are not one array, or one
// with a trailing comma or trailing bytes, are not a stream this service
// translates.
func vertexGeminiChunks(body []byte) ([]json.RawMessage, error) {
	items, err := readVertexArray(body, "answer")
	if err != nil {
		return nil, untranslatableAnswer("answer", "a streamed answer is one JSON array of chunks, and this is not one")
	}
	if len(items) == 0 {
		return nil, untranslatableAnswer("answer", "a streamed answer with no chunk in it is no answer")
	}
	return items, nil
}

// vertexGeminiPiece is one piece of the answer, in the order Vertex wrote it:
// text, or a function call.
type vertexGeminiPiece struct {
	text string
	call *vertexGeminiCall
}

// vertexGeminiCall is one function call the model answered with: the function's
// name, its arguments as the JSON object the Messages wire's tool_use carries,
// the thought signature the answer gave the call (standard base64, "" when it
// gave none), the ID this translation gives it, its ordinal in the answer, and
// the path it sits at in the answer — which is what a refusal about its
// signature names.
type vertexGeminiCall struct {
	name      string
	args      json.RawMessage
	signature string
	id        string
	ordinal   int
	where     string
}

// vertexGeminiCounts is the answer's usage as Vertex reports it, kept in
// Vertex's own terms until it is turned into Anthropic's at the end.
type vertexGeminiCounts struct {
	prompt     int64
	cached     int64
	candidates int64
	thoughts   int64
	seen       bool
}

// vertexGeminiSaid is one answer accumulated across the chunks it arrived in:
// the id, the pieces in order, how many function calls it answered, the finish
// reason Vertex gave, the usage it reported, and the block reason of a prompt
// Vertex refused to read at all.
//
// It is one type for a whole answer and a streamed one on purpose: a whole
// answer is a stream of one chunk, so the two translations share every rule
// about what an answer means, and the only thing that differs is how the
// accumulated answer is written out.
type vertexGeminiSaid struct {
	id      string
	pieces  []vertexGeminiPiece
	calls   int
	finish  string
	usage   vertexGeminiCounts
	blocked string
}

// vertexGeminiSaid accumulates the chunks of one answer, and it is where every
// refusal about the answer is raised: a member this translation has no form for
// ends here, with the path it sits at, and nothing of the answer reaches the
// agent.
func vertexGeminiAccumulate(chunks []json.RawMessage) (*vertexGeminiSaid, error) {
	said := &vertexGeminiSaid{id: vertexGeminiMessageID}
	for _, raw := range chunks {
		if err := vertexGeminiChunk(raw, said); err != nil {
			return nil, err
		}
	}
	return said, nil
}

// vertexGeminiChunk reads one chunk of an answer. A member Vertex sends that
// this translation does not read (modelVersion, safetyRatings, citationMetadata,
// groundingMetadata, logprobs and the rest) is ignored rather than refused: the
// rule this file follows is about what the AGENT reads, and a harness reading a
// Messages answer reads nothing of Vertex's own metadata. What is refused is a
// member whose loss or wrong reading would change the answer the agent acts on.
func vertexGeminiChunk(raw []byte, said *vertexGeminiSaid) error {
	members, err := geminiObject(raw, "answer")
	if err != nil {
		return err
	}
	for _, m := range members {
		switch m.Key {
		case "candidates":
			if err := vertexGeminiCandidates(m.Value, said); err != nil {
				return err
			}
		case "usageMetadata":
			if err := vertexGeminiUsageFrom(m.Value, said); err != nil {
				return err
			}
		case "promptFeedback":
			if err := vertexGeminiPromptFeedback(m.Value, said); err != nil {
				return err
			}
		case "responseId":
			if id, ok := vertexJSONString(m.Value); ok && said.id == vertexGeminiMessageID {
				said.id = vertexGeminiAnthropicIDPrefix + id
			}
		}
	}
	return nil
}

// vertexGeminiCandidates reads the candidates of one chunk. One candidate is
// what this wire answers for a call this service makes — GV1 sends no
// candidateCount — so a second one carrying content is refused rather than
// dropped: the Messages answer has one content list, and picking one of two
// answers for the agent would be choosing which model output it reads.
func vertexGeminiCandidates(raw []byte, said *vertexGeminiSaid) error {
	items, err := geminiArray(raw, "candidates")
	if err != nil {
		return err
	}
	for i, item := range items {
		where := fmt.Sprintf("candidates[%d]", i)
		members, err := geminiObject(item, where)
		if err != nil {
			return err
		}
		for _, m := range members {
			switch m.Key {
			case "content":
				if i != 0 {
					return untranslatableAnswer(where+".content", "this answer carries more than one candidate, and the Messages answer the agent reads has one content list")
				}
				if err := vertexGeminiCandidateContent(m.Value, where+".content", said); err != nil {
					return err
				}
			case "finishReason":
				reason, ok := vertexJSONString(m.Value)
				if !ok {
					if vertexJSONNull(m.Value) {
						continue
					}
					return untranslatableAnswer(where+".finishReason", "a finish reason is the name of the reason the model stopped and this is not one")
				}
				said.finish = reason
			}
		}
	}
	return nil
}

// vertexGeminiPromptFeedback reads the block reason of a prompt Vertex refused
// to read. A blocked prompt answers no candidate at all, and Anthropic's word
// for the model refusing is `refusal`: the answer is a refusal with no content,
// which is what the agent reads. Vertex's own reason and its message have no
// member on the Messages wire and are the one thing this translation drops —
// the answer is still a whole Anthropic answer for what happened.
func vertexGeminiPromptFeedback(raw []byte, said *vertexGeminiSaid) error {
	members, err := geminiObject(raw, "promptFeedback")
	if err != nil {
		return err
	}
	for _, m := range members {
		if m.Key != "blockReason" {
			continue
		}
		reason, ok := vertexJSONString(m.Value)
		if !ok {
			if vertexJSONNull(m.Value) {
				continue
			}
			return untranslatableAnswer("promptFeedback.blockReason", "a block reason is the name of the reason the prompt was refused and this is not one")
		}
		said.blocked = reason
	}
	return nil
}

// vertexGeminiContent reads one candidate's content: the model's turn, whose
// parts are the answer itself.
func vertexGeminiCandidateContent(raw []byte, where string, said *vertexGeminiSaid) error {
	members, err := geminiObject(raw, where)
	if err != nil {
		return err
	}
	var parts []byte
	for _, m := range members {
		switch m.Key {
		case "role":
			role, ok := vertexJSONString(m.Value)
			if !ok || role != "model" {
				return untranslatableAnswer(where+".role", "Vertex's answer is the model's own turn, and this content is not the model's")
			}
		case "parts":
			parts = m.Value
		}
	}
	if parts == nil {
		// A candidate that carries no parts is the shape of a chunk whose only
		// news is its finish reason (the last chunk of a stream, or a refusal
		// with no text): it says nothing the other members do not.
		return nil
	}
	return vertexGeminiAnswerParts(parts, where+".parts", said)
}

// vertexGeminiAnswerParts reads one content's parts, in order, as the pieces
// of the answer. The three part kinds a Gemini model answers with are the three
// this translation carries: text, a function call, and a function call's
// thought signature. Anything else ends the call.
func vertexGeminiAnswerParts(raw []byte, where string, said *vertexGeminiSaid) error {
	items, err := geminiArray(raw, where)
	if err != nil {
		return err
	}
	for i, item := range items {
		at := fmt.Sprintf("%s[%d]", where, i)
		members, err := geminiObject(item, at)
		if err != nil {
			return err
		}
		var (
			text      *string
			thought   bool
			signature string
			call      []byte
		)
		for _, m := range members {
			switch m.Key {
			case "text":
				s, ok := vertexJSONString(m.Value)
				if !ok {
					return untranslatableAnswer(at+".text", "a part's text is a string and this is not one")
				}
				text = &s
			case "thought":
				b, ok := vertexJSONBool(m.Value)
				if !ok {
					return untranslatableAnswer(at+".thought", "whether a part is a thought is true or false, and this is neither")
				}
				thought = b
			case "thoughtSignature":
				s, ok := vertexJSONString(m.Value)
				if !ok {
					return untranslatableAnswer(at+".thoughtSignature", "a thought signature is the base64 bytes the model answered a call with and this is not one")
				}
				signature = s
			case "functionCall":
				call = m.Value
			default:
				return untranslatableAnswer(at, vertexGeminiPartRefusal(m.Key))
			}
		}
		switch {
		case call != nil:
			if err := vertexGeminiCallPiece(call, signature, at, said); err != nil {
				return err
			}
		case text != nil && !thought:
			vertexGeminiTextPiece(*text, said)
		case text != nil && thought:
			return untranslatableAnswer(at, "this part is a thought the model wrote, and the reasoning Vertex answers with is not the reasoning the harness reads: GV1 asks for none, and this translation has no measured mapping of one onto a thinking block")
		default:
			return untranslatableAnswer(at, "this part of Vertex's answer is of a kind this translation has no form for, and the agent reads an Anthropic content block")
		}
	}
	return nil
}

// vertexGeminiUncarriedPart is why a part of a known kind this translation does
// not carry is refused, one sentence each. The kinds are Vertex's own
// vocabulary (Part's oneof), so naming one names no value the call carried, and
// the map is what keeps a kind this file has never heard of from being printed
// back.
var vertexGeminiUncarriedParts = map[string]string{
	"inlineData":          "this part is an inline blob (an image or a document the model answered with) and the Messages answer has no member this translation has measured onto it",
	"fileData":            "this part is a file the model answered with, and the Messages answer has no member this translation has measured onto it",
	"executableCode":      "this part is code the model asked to run, which is a Vertex-side tool call the harness cannot be handed as a content block",
	"codeExecutionResult": "this part is the result of code run on Vertex's side, and the harness reads the results of tools it ran itself",
}

func vertexGeminiPartRefusal(kind string) string {
	if why, known := vertexGeminiUncarriedParts[kind]; known {
		return why
	}
	return "this part is of a kind this translation does not know, so it has no content block on the Messages wire"
}

// vertexGeminiTextPiece appends text to the answer, joining it to the text
// before it: Vertex streams one text answer across chunks, and consecutive text
// parts are one text block on the Messages wire rather than several.
func vertexGeminiTextPiece(text string, said *vertexGeminiSaid) {
	if n := len(said.pieces); n > 0 && said.pieces[n-1].call == nil {
		said.pieces[n-1].text += text
		return
	}
	said.pieces = append(said.pieces, vertexGeminiPiece{text: text})
}

// vertexGeminiCallPiece appends one function call as its own piece, and so its
// own tool_use block.
//
// One functionCall part is one whole call: GV1 asks Vertex for no streamed
// arguments (no partialArgs/willContinue, the one request shape that makes
// Vertex answer a call in pieces), so a part's args are the call's own
// arguments, complete. Two calls to the same function are therefore two calls
// whichever chunks they arrive in, and a later chunk carrying the same name is
// never the call before it continued — merging them would answer the harness
// one call where the model made two, and the first call's arguments would be
// gone with no error to show it.
func vertexGeminiCallPiece(raw []byte, signature, at string, said *vertexGeminiSaid) error {
	call, err := vertexGeminiFunctionCall(raw, signature, at)
	if err != nil {
		return err
	}
	said.calls++
	call.ordinal = said.calls
	id, err := vertexGeminiCallID(call, at)
	if err != nil {
		return err
	}
	call.id = id
	said.pieces = append(said.pieces, vertexGeminiPiece{call: call})
	return nil
}

// vertexGeminiFunctionCall reads one functionCall: the function's name and its
// arguments, which the Messages wire's tool_use carries as `input`. A call with
// no arguments object is a function of no arguments, which is an empty object
// and not an absent member.
func vertexGeminiFunctionCall(raw []byte, signature, where string) (*vertexGeminiCall, error) {
	members, err := geminiObject(raw, where+".functionCall")
	if err != nil {
		return nil, err
	}
	call := &vertexGeminiCall{signature: signature, args: json.RawMessage("{}"), where: where}
	var named bool
	for _, m := range members {
		switch m.Key {
		case "name":
			name, ok := vertexJSONString(m.Value)
			if !ok {
				return nil, untranslatableAnswer(where+".functionCall.name", "a function is named by a string and this is not one")
			}
			call.name, named = name, true
		case "args":
			if !vertexJSONIsObject(m.Value) {
				return nil, untranslatableAnswer(where+".functionCall.args", "Vertex takes a function's arguments as a JSON object, and this is not one")
			}
			call.args = m.Value
		}
	}
	if !named {
		return nil, untranslatableAnswer(where+".functionCall.name", "Vertex names a function call by the function's name and this call carries none")
	}
	return call, nil
}

// vertexGeminiCallID is the Messages tool_use block's `id` for one call, and it
// is the round trip GV1 reads back: the thought signature Vertex answered the
// call with, as URL-safe base64 with no padding after GV1's own prefix, or a
// minted id when the answer carried no signature. The signature is re-encoded
// rather than carried as it came because an id is not a place for `+`, `/` or
// `=`, and GV1 decodes exactly this form.
//
// A minted id is the call's ordinal in the answer and never its name: two calls
// to the same function in one answer are two calls, and two blocks sharing one
// id would leave the tool_result the harness sends back answering both. The id
// is opaque to the harness — it echoes it — and GV1 reads the NAME off the call
// for a functionResponse, so only its uniqueness matters.
//
// A signature that is not base64 is a typed refusal and not a call without one:
// Gemini 3 checks the signature of a functionCall replayed to it, so an
// unreadable one is a call the harness cannot send back, and refusing it here
// names the member where a later 400 would name nothing an operator could act
// on.
func vertexGeminiCallID(call *vertexGeminiCall, where string) (string, error) {
	if call.signature == "" {
		return vertexGeminiMintedCallID + strconv.Itoa(call.ordinal), nil
	}
	raw, err := base64.StdEncoding.DecodeString(call.signature)
	if err != nil || len(raw) == 0 {
		return "", untranslatableAnswer(where+".thoughtSignature",
			"this call's signature is not the base64 Vertex answers one with, and Gemini 3 refuses a replayed call whose signature it cannot read")
	}
	return vertexGeminiThoughtSignatureID + base64.RawURLEncoding.EncodeToString(raw), nil
}

// vertexGeminiUsageFrom reads one usageMetadata object. Vertex's counts are
// cumulative across a stream, so the last one an answer reports is the
// answer's own.
func vertexGeminiUsageFrom(raw []byte, said *vertexGeminiSaid) error {
	members, err := geminiObject(raw, "usageMetadata")
	if err != nil {
		return err
	}
	for _, m := range members {
		var dst *int64
		switch m.Key {
		case "promptTokenCount":
			dst = &said.usage.prompt
		case "cachedContentTokenCount":
			dst = &said.usage.cached
		case "candidatesTokenCount":
			dst = &said.usage.candidates
		case "thoughtsTokenCount":
			dst = &said.usage.thoughts
		default:
			continue
		}
		n, ok := vertexJSONInt(m.Value)
		if !ok || n < 0 {
			return untranslatableAnswer("usageMetadata."+m.Key, "a token count is a whole number of tokens and this is not one")
		}
		*dst = n
		said.usage.seen = true
	}
	return nil
}

// vertexGeminiUsage is the answer's usage on the Messages wire, which is the
// shape the run's gateway prices by.
//
//   - input_tokens is promptTokenCount less the cached tokens, because
//     Vertex's promptTokenCount includes the cached content and Anthropic's
//     input_tokens excludes what the cache counts apart. A cached count above
//     the prompt is an answer this translation cannot read and is refused
//     rather than answered as a negative.
//   - cache_read_input_tokens is cachedContentTokenCount: Vertex's cache is
//     its context cache, which is a read of tokens the caller already paid to
//     write.
//   - output_tokens is candidatesTokenCount plus thoughtsTokenCount, because
//     Vertex bills a thought token as an output one and a count that left the
//     thoughts out would cap a call below what Vertex charges it.
//
// An answer that reports no usage at all is refused: the acceptance asks for
// the figures the gateway prices by, and an answer that names none would settle
// at the call's reservation — a figure nothing measured.
func vertexGeminiUsage(said *vertexGeminiSaid) (map[string]any, error) {
	if !said.usage.seen {
		return nil, untranslatableAnswer("usageMetadata", "Vertex's answer reports no usage, and the run's gateway prices the call by the figures the answer carries")
	}
	if said.usage.cached > said.usage.prompt {
		return nil, untranslatableAnswer("usageMetadata.cachedContentTokenCount", "Vertex reports more cached tokens than prompt tokens, so this answer's input count cannot be read")
	}
	return map[string]any{
		"input_tokens":                said.usage.prompt - said.usage.cached,
		"cache_creation_input_tokens": int64(0),
		"cache_read_input_tokens":     said.usage.cached,
		"output_tokens":               said.usage.candidates + said.usage.thoughts,
	}, nil
}

// vertexGeminiContent is the whole answer as Messages content blocks, in the
// order Vertex wrote them.
func vertexGeminiContent(said *vertexGeminiSaid) ([]any, error) {
	blocks := make([]any, 0, len(said.pieces))
	for _, piece := range said.pieces {
		if piece.call == nil {
			blocks = append(blocks, map[string]any{"type": "text", "text": piece.text})
			continue
		}
		blocks = append(blocks, map[string]any{
			"type": "tool_use", "id": piece.call.id, "name": piece.call.name, "input": piece.call.args,
		})
	}
	return blocks, nil
}

// vertexGeminiFinishReasons maps Vertex's finishReason onto Anthropic's
// stop_reason, one word for one word. The safety family is Anthropic's
// `refusal`, which is the model declining to answer; `STOP` is `end_turn`, and
// becomes `tool_use` when the answer called a function (vertexGeminiStopReason).
//
// A reason NOT on this list ends the call: Vertex's `MALFORMED_FUNCTION_CALL`,
// its `OTHER` and any name this build has never seen have no stop_reason the
// harness would act on correctly, and a wrong one — "keep going" for a turn the
// model ended, or the reverse — is the one member of an answer that cannot be
// guessed at.
var vertexGeminiFinishReasons = map[string]string{
	"STOP":               "end_turn",
	"MAX_TOKENS":         "max_tokens",
	"SAFETY":             "refusal",
	"RECITATION":         "refusal",
	"BLOCKLIST":          "refusal",
	"PROHIBITED_CONTENT": "refusal",
	"SPII":               "refusal",
	"IMAGE_SAFETY":       "refusal",
}

// vertexGeminiStopReason is the answer's stop_reason.
//
// A prompt Vertex refused to read has no finish reason at all, and it is the
// one case answered from promptFeedback: the model answered nothing, so the
// answer is a refusal. A `STOP` whose answer called a function is `tool_use`,
// which is Anthropic's own word for a turn that ended by asking for a tool; a
// `MAX_TOKENS` stays `max_tokens` even then, because the call it was writing
// was cut off and is not a call the harness may run.
func vertexGeminiStopReason(said *vertexGeminiSaid) (string, error) {
	if said.finish == "" {
		if said.blocked != "" && len(said.pieces) == 0 {
			return "refusal", nil
		}
		return "", untranslatableAnswer("candidates[0].finishReason", "Vertex's answer names no finish reason, so there is no stop reason the harness could act on")
	}
	reason, known := vertexGeminiFinishReasons[said.finish]
	if !known {
		return "", untranslatableAnswer("candidates[0].finishReason", "this finish reason has no stop_reason on the Messages wire that means the same thing, and a stop reason the harness reads wrongly is worse than a call that ends")
	}
	if reason == "end_turn" && said.calls > 0 {
		return "tool_use", nil
	}
	return reason, nil
}

// geminiObject reads one JSON object of an answer, refusing anything that is
// not one with this file's typed reason rather than GV1's: the same bytes are
// the answer here and were the request there, and a refusal has to name the
// half it is about.
func geminiObject(raw []byte, where string) ([]vertexMember, error) {
	members, err := readVertexObject(raw, where)
	if err != nil {
		return nil, untranslatableAnswer(where, "Vertex's answer is JSON objects and this member is not one")
	}
	return members, nil
}

// geminiArray is geminiObject's array half.
func geminiArray(raw []byte, where string) ([]json.RawMessage, error) {
	items, err := readVertexArray(raw, where)
	if err != nil {
		return nil, untranslatableAnswer(where, "Vertex's answer is JSON arrays and this member is not one")
	}
	return items, nil
}

// vertexJSONNull reports whether a member is JSON null, which reads as absent:
// a finish reason or a block reason Vertex left null is one it did not give.
func vertexJSONNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}
