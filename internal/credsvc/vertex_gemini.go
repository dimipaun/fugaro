// vertex_gemini.go is GV1 of docs/model-credentials-routing/implementation-plan.md:
// the request half of the spec's `gemini-* → Vertex` (D3193). CS4's vertex.go
// serves the project's Vertex on this service's own identity and translates a
// Messages call onto Vertex's ANTHROPIC publisher — the partner publisher every
// wire in this repository has ever spoken, and the only one it had a spelling
// for. A Gemini model is not served there: it is served by Vertex's GOOGLE
// publisher, whose request is a different document (`contents`, `parts`,
// `generationConfig`) whose model is a segment of the path alone. This file is
// that second translation, and it is the request half of what GV2 answers and
// GV4 proves end to end.
//
// It is a translation and not a filter, and the rule that makes it safe to send
// at all is the one this file is built around: EVERY member of the call is
// either carried to Vertex with the same meaning, or refused with a typed
// reason. Nothing is dropped because this file did not know what to do with it,
// because a request Vertex answers without a member is not the call the agent
// made — a tool the agent offered but the model was never told about, a
// constraint that vanished, an image the model cannot see — and the agent would
// read a wrong answer with no error at all. Two members are the deliberate
// exceptions, both named and both argued where they are dropped (the hint
// `cache_control`, and `metadata.user_id`): a member whose absence changes no
// input the model reads.
//
// **The wire is not in this repository (U8 of the reconciliation)**, so every
// mapping below cites the page it was read from, and GV1's pull request cites
// them together for the owner:
//
//   - models.generateContent — the path, the request body's members, the
//     `systemInstruction` rule that only text belongs in its parts:
//     https://cloud.google.com/vertex-ai/generative-ai/docs/reference/rest/v1/projects.locations.publishers.models/generateContent
//   - the inference reference — Content.role (`user` / `model`), Part.text,
//     FunctionCall.name and .args, FunctionResponse.name and .response,
//     GenerationConfig.maxOutputTokens, .temperature, .topP, .topK and
//     .stopSequences (at most five):
//     https://cloud.google.com/vertex-ai/generative-ai/docs/model-reference/inference
//   - function calling — functionDeclarations, the function-name rule, the
//     calling modes AUTO/ANY/NONE with allowed_function_names, and how a
//     functionResponse is matched to its call (by name, in order, on the `v1`
//     API):
//     https://cloud.google.com/vertex-ai/generative-ai/docs/model-reference/function-calling
//   - a function declaration's parameters members — `parameters` (an OpenAPI
//     Schema Object, typed) and `parametersJsonSchema` (a whole JSON Schema, taken
//     as it is, mutually exclusive with the first), which is why a tool's
//     `input_schema` is sent as the second and nothing in it is dropped:
//     https://aiplatform.googleapis.com/$discovery/rest?version=v1 — the API's own
//     discovery document, whose GoogleCloudAiplatformV1FunctionDeclaration is the
//     authority for a nested type the docs site does not give a page of its own
//   - the FunctionResponse.response key convention (`output`, and `error` for a
//     call that failed):
//     https://ai.google.dev/api/caching
//   - models.countTokens — the counting endpoint's path and body:
//     https://cloud.google.com/vertex-ai/generative-ai/docs/reference/rest/v1/projects.locations.publishers.models/countTokens
//   - thinking — why an Anthropic `thinking` budget is NOT carried: Gemini 3
//     models take a discrete level and the budget is deprecated on them:
//     https://cloud.google.com/vertex-ai/generative-ai/docs/thinking
//
// The Anthropic half is the Messages API the agent's harness sends
// (https://platform.claude.com/docs/en/api/messages): `system`, `messages`,
// `tools` (with `input_schema` and the `cache_control` hint), `tool_choice`,
// `max_tokens`, `temperature`, `top_p`, `top_k`, `stop_sequences`, `stream`,
// `metadata` and `model`.
//
// What this file does NOT do is GV2's: the answer Vertex sends back for a
// Gemini model is a GenerateContentResponse and not a Messages answer, and
// nothing here translates it. Nothing here is reachable in production yet
// either, and not because some guard holds it back (the routing checks that
// would send a non-Claude model to Vertex are RT3's, an unlanded plan task, so
// there is no such rule in this tree to lean on): serveVertex has no production
// caller at all, and its only callers are GV4's wiring and the tests below. The
// wire is exercised for real by the owner's Check 886.
//
// The round trip GV2 completes is anchored HERE, because the request is where
// the signature lands: a tool call this file sends carries the THOUGHT SIGNATURE
// Vertex answered that call with. Gemini 3 checks the signature of every functionCall
// part it is sent back and answers a missing one with a 400 (the thinking page
// cited above), so a tool loop — the very thing this translation exists for —
// does not work unless the signature comes back, and the second call of that
// loop is the one that carries it. Where it lives on the Anthropic side is the
// tool call's ID, minted by GV2 with vertexGeminiThoughtSignatureID when Vertex
// sends a signature and read back here by vertexGeminiThoughtSignature: an ID is
// the one member of a tool call a harness echoes back verbatim, where a member
// of this service's own invention would be dropped by the SDK the harness is
// built on before the next call was ever made.

package credsvc

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/dimipaun/fugaro/internal/pricing"
)

const (
	// The three methods of Vertex's Google publisher this service reaches: the
	// two a Messages call becomes (chosen by the caller's own `stream` member,
	// as CS4 chooses rawPredict or streamRawPredict) and the counting endpoint
	// of C1's second path.
	vertexGeminiGenerateContent       = "generateContent"
	vertexGeminiStreamGenerateContent = "streamGenerateContent"
	vertexGeminiCountTokens           = "countTokens"

	// vertexGeminiMaxStopSequences is how many stop sequences Vertex's Google
	// publisher takes, from the inference reference: "Maximum 5 items in the
	// list." A call carrying more is refused rather than trimmed — the
	// sequences an agent asked for are a fact of that call, and dropping the
	// sixth would answer a different question than the one it asked.
	vertexGeminiMaxStopSequences = 5

	// vertexGeminiMaxFunctionNameBytes is the length Vertex's function-name
	// rule allows, from the function-calling page's best practices: "Function
	// names should start with a letter or an underscore and contain only
	// characters a-z, A-Z, 0-9, underscores, dots or dashes with a maximum
	// length of 64."
	vertexGeminiMaxFunctionNameBytes = 64

	// vertexGeminiClientToolType is the `type` a client tool is marked with, and
	// the only value a tool reaching this service can carry: the gateway in front
	// of it admits a tool with no `type` and one with this one, and refuses every
	// other value with "the budget allows only client tools"
	// (internal/gateway/forward.go). Anthropic's own server-side tools carry
	// their own type names and never get this far.
	vertexGeminiClientToolType = "custom"
)

// vertexGeminiThoughtSignatureID is the prefix a tool call's ID carries when
// that ID encodes the thought signature Vertex sent with the call — the one
// place on the Anthropic side where a signature lives, and the reason it is
// there rather than in a member of its own is in this file's header. GV2 mints
// the ID: the signature's bytes as URL-safe base64 with no padding, because an
// ID is not a place for `+`, `/` or `=`.
const vertexGeminiThoughtSignatureID = "toolu_vrtx_thought_"

// vertexGeminiThoughtSignature answers the thought signature a tool call's ID
// carries, and "" when it carries none — a call whose id the harness minted, or
// one the model answered without thinking. What is answered is the standard
// base64 Vertex's `bytes` member takes, re-encoded from the bytes the ID held,
// so the value Vertex sent is the value it is sent back.
//
// An ID with the prefix and nothing decodable after it is a typed refusal and
// not a call without a signature: Gemini 3 checks the signature of a replayed
// functionCall, so an unreadable one is a call that cannot be sent at all, and
// refusing it here names the member where Vertex's own 400 would name nothing an
// operator could act on.
func vertexGeminiThoughtSignature(id, where string) (string, error) {
	encoded, prefixed := strings.CutPrefix(id, vertexGeminiThoughtSignatureID)
	if !prefixed {
		return "", nil
	}
	signature, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(signature) == 0 {
		return "", untranslatableMember(where+".id",
			"this tool call's id carries the thought signature Vertex answered with, and what follows the prefix is not a signature: Gemini 3 checks the signature of a function call sent back to it and refuses a call without one, so a call whose signature cannot be read is one this translation will not send")
	}
	return base64.StdEncoding.EncodeToString(signature), nil
}

// vertexGeminiFunctionNameRE is that rule as a rule, applied to every tool the
// agent offers: a name Vertex would refuse is refused HERE, where the refusal
// can name the tool and write one line, rather than there, where the answer is
// a Google error this service has not translated (GV2).
var vertexGeminiFunctionNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

// vertexGeminiNoForm is the reason a member this translation does not know is
// refused with. It is one sentence for every such member, because what an
// operator needs is the member's path and not a taxonomy: the fix is a mapping
// for that member, and this file is where it goes.
const vertexGeminiNoForm = "Vertex's Google publisher has no member this translation can carry it as, so the call is refused rather than sent without it: the agent asked for something the model would never be told about"

// vertexGeminiUncarriedBlocks names the Anthropic content blocks this
// translation deliberately does NOT carry, and why, one sentence each. The map
// matters twice: it is the diagnosis an operator reads (the block's own
// vocabulary is Anthropic's and closed, so naming the kind names no value the
// call carried), and it is what keeps a block type this file has never heard of
// from being printed back — a caller's text must not reach a refusal.
//
// An image block is the acceptance's own example and the sharpest one: Vertex's
// inlineData member could carry the bytes, so "cannot be translated" is a
// decision and not an impossibility. It is the decision the task takes — the
// three mappings GV1 does carry are the ones the harness's own calls are made
// of (text, tool_use, tool_result), and a part this file invented a form for
// would be a part whose round trip nothing has measured. A later task that
// carries images cites the multimodal page and proves the round trip.
var vertexGeminiUncarriedBlocks = map[string]string{
	"image":             "an image block is not carried to Vertex's Google publisher: this translation has no measured mapping of an Anthropic image source onto inlineData",
	"document":          "a document block is not carried: the fileData member it would become needs a Google Cloud Storage URI the caller has no way to give, and inline document bytes are unmeasured here",
	"thinking":          "a thinking block is not carried, and neither is the `thinking` config member: Gemini 3 models take a discrete thinking level and the budget Anthropic states is deprecated on them, so the reasoning the agent asked for cannot be asked for the same way (see the thinking page cited above)",
	"redacted_thinking": "a redacted thinking block is not carried: it exists only to be replayed to the API that produced it, and its signature means nothing to Vertex",
}

// vertexUntranslatable is the typed reason one member of a call could not be
// carried: the member's PATH in the request (never its content), and the
// sentence that says why. It is what the acceptance's "refused with a typed
// reason and never dropped" is: a caller can tell this refusal (a member of
// this call) from the source being unavailable or the call being unpinned, and
// the path is what tells an operator which mapping to add.
//
// It is an error and not a wire value. The wire's `tried` reasons are a closed
// set about a SOURCE being skipped — exhausted, broken, not_admitted (C1) —
// and this is not one of them: the source was chosen, the call reached it, and
// the call is the thing that cannot be sent. It is answered as every other
// unforwardable body is (StatusRequestRefused, ErrorRequestRefused), which is
// the refusal a release-behind gateway already knows how to read (R24).
type vertexUntranslatable struct {
	Member string
	Why    string
}

func (e *vertexUntranslatable) Error() string {
	return "the request's " + e.Member + " has no faithful form on Vertex's Google publisher: " + e.Why
}

// untranslatableMember is the typed refusal above, written once.
func untranslatableMember(member, why string) error {
	return &vertexUntranslatable{Member: member, Why: why}
}

// vertexTranslationRefusal is the sentence the run is refused with when a body
// could not be translated, and whether the error is that refusal at all: every
// other error out of vertexBodyFor is the service's own reading of the body
// (bytes that are not one JSON object), which keeps the sentence it already
// had.
//
// The sentence is built from the typed reason's PATH and its own why, both of
// which this package wrote: nothing the call carried is in it.
func vertexTranslationRefusal(err error) (string, bool) {
	var reason *vertexUntranslatable
	if !errors.As(err, &reason) {
		return "", false
	}
	return requestRefused + "this model is served by Vertex's Google publisher, and " + reason.Error() +
		"; a member this translation cannot carry faithfully is refused rather than dropped, because the answer the model would give without it is not the answer the agent asked for", true
}

// vertexGeminiPath is the path one call takes on Vertex's Google publisher:
// /v1/projects/<project>/locations/<location>/publishers/google/models/<model>:<method>,
// with the method decided exactly as CS4 decides it (the caller's own `stream`
// member, and the counting endpoint for a token count).
//
// The model is a path segment here as it is on the Anthropic publisher, and it
// is the spelling the map answers (D3175) and never the ID the agent asked
// with. The counting endpoint names its model in the PATH as the generating
// endpoints do — Vertex's countTokens takes the model in the path and, unlike
// its Anthropic publisher's counting endpoint, carries no model in the body at
// all — so the same spelling goes to the same place either way.
func vertexGeminiPath(callPath, project, location, spelling string, stream bool) (string, bool) {
	prefix := "/v1/projects/" + project + "/locations/" + location + "/publishers/" + pricing.VertexPublisherGoogle + "/models/"
	switch callPath {
	case MessagesPath:
		method := vertexGeminiGenerateContent
		if stream {
			method = vertexGeminiStreamGenerateContent
		}
		return prefix + spelling + ":" + method, true
	case CountTokensPath:
		return prefix + spelling + ":" + vertexGeminiCountTokens, true
	}
	return "", false
}

// vertexGeminiBody translates one Messages body into the request Vertex's
// Google publisher is sent for a Gemini model.
//
// The members it decides for itself are the three the path already answers:
// `model` (the path names it), `stream` (the path's method is chosen by it) and
// `anthropic_version` (a member of Vertex's Anthropic publisher and of nothing
// else). Everything else is either mapped below or refused — see the file's own
// note on the two deliberate drops, `cache_control` and `metadata.user_id`.
//
// counting is the token-count path, and it is the one body this translation
// builds that is NOT a generateContent request: Vertex's CountTokensRequest has
// no `toolConfig` member at all, so a tool choice sent to it is a request Vertex
// rejects as an unknown field. The member is still READ there — a tool choice
// this translation cannot carry is refused on either path — and it is left out
// of the counting body alone, where it cannot change the input being counted.
func vertexGeminiBody(body []byte, counting bool) ([]byte, error) {
	call, err := readVertexObject(body, "")
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	generation := map[string]any{}
	messages := false
	for _, m := range call {
		switch m.Key {
		case "model", "stream", "anthropic_version":
			// The path and nothing else carries these; see the doc comment.
		case "system":
			parts, err := vertexGeminiSystem(m.Value)
			if err != nil {
				return nil, err
			}
			out["systemInstruction"] = map[string]any{"parts": parts}
		case "messages":
			contents, err := vertexGeminiContents(m.Value)
			if err != nil {
				return nil, err
			}
			out["contents"] = contents
			messages = true
		case "tools":
			declarations, err := vertexGeminiTools(m.Value)
			if err != nil {
				return nil, err
			}
			// A tools list with no tool in it is no list: Vertex's
			// functionDeclarations is a repeated field, and an empty group of
			// them is a member that says nothing a missing member does not.
			if len(declarations) > 0 {
				out["tools"] = []any{map[string]any{"functionDeclarations": declarations}}
			}
		case "tool_choice":
			config, err := vertexGeminiToolChoice(m.Value)
			if err != nil {
				return nil, err
			}
			if !counting {
				out["toolConfig"] = config
			}
		case "max_tokens":
			n, ok := vertexJSONInt(m.Value)
			if !ok || n < 1 {
				return nil, untranslatableMember(m.Key, "Vertex's maxOutputTokens is a count of tokens and this is not one")
			}
			generation["maxOutputTokens"] = n
		case "temperature":
			if _, ok := vertexJSONNumber(m.Value); !ok {
				return nil, untranslatableMember(m.Key, "Vertex's temperature is a number and this is not one")
			}
			generation["temperature"] = m.Value
		case "top_p":
			if _, ok := vertexJSONNumber(m.Value); !ok {
				return nil, untranslatableMember(m.Key, "Vertex's topP is a number and this is not one")
			}
			generation["topP"] = m.Value
		case "top_k":
			if _, ok := vertexJSONNumber(m.Value); !ok {
				return nil, untranslatableMember(m.Key, "Vertex's topK is a number and this is not one")
			}
			generation["topK"] = m.Value
		case "stop_sequences":
			sequences, err := vertexGeminiStopSequences(m.Value)
			if err != nil {
				return nil, err
			}
			generation["stopSequences"] = sequences
		case "metadata":
			if err := vertexGeminiMetadata(m.Value); err != nil {
				return nil, err
			}
		default:
			return nil, untranslatableMember(m.Key, vertexGeminiNoForm)
		}
	}
	if !messages {
		return nil, untranslatableMember("messages", "Vertex's contents is required and this call carries no messages at all")
	}
	if len(generation) > 0 {
		out["generationConfig"] = generation
	}
	return json.Marshal(out)
}

// vertexGeminiStopSequences carries the agent's stop sequences, held to the
// count Vertex takes (the inference reference: "Maximum 5 items in the list.").
func vertexGeminiStopSequences(raw json.RawMessage) ([]string, error) {
	items, err := readVertexArray(raw, "stop_sequences")
	if err != nil {
		return nil, err
	}
	if len(items) > vertexGeminiMaxStopSequences {
		return nil, untranslatableMember("stop_sequences", fmt.Sprintf(
			"Vertex's stopSequences takes at most %d of them and this call carries %d, and a translation that trimmed the list would answer a different question than the call asked",
			vertexGeminiMaxStopSequences, len(items)))
	}
	sequences := make([]string, 0, len(items))
	for i, item := range items {
		sequence, ok := vertexJSONString(item)
		if !ok {
			return nil, untranslatableMember(fmt.Sprintf("stop_sequences[%d]", i), "a stop sequence is a string and this is not one")
		}
		sequences = append(sequences, sequence)
	}
	return sequences, nil
}

// vertexGeminiMetadata is the metadata member, and the second of this
// translation's two deliberate drops: Anthropic's `metadata.user_id` is a fact
// about the CALLER's bookkeeping — the end user an API integration attributes a
// request to — and Vertex's Google publisher has no per-request member for it
// (the body's members are contents, cachedContent, tools, toolConfig, labels,
// safetySettings, modelArmorConfig, generationConfig and systemInstruction).
// Nothing the model reads is in it, so dropping it changes no answer; a member
// beside it would, and is refused.
//
// It is dropped rather than refused so that the harness's own calls reach
// Vertex at all: `metadata.user_id` is a member the agent's harness sets on
// every call it makes, and a refusal for it would refuse every call there is.
// That is the test of the rule this file follows — a drop is allowed exactly
// where its absence cannot change what the model answers, and it is written
// down here so it is a decision and not a leak.
func vertexGeminiMetadata(raw json.RawMessage) error {
	members, err := readVertexObject(raw, "metadata")
	if err != nil {
		return err
	}
	for _, m := range members {
		if m.Key != "user_id" {
			return untranslatableMember("metadata."+m.Key, vertexGeminiNoForm)
		}
		if _, ok := vertexJSONString(m.Value); !ok {
			return untranslatableMember("metadata.user_id", "an end-user id is a string and this is not one")
		}
	}
	return nil
}

// vertexGeminiSystem carries the system prompt as Vertex's systemInstruction,
// whose parts are text alone: the generateContent reference says of it that
// "only text should be used in parts and content in each part will be in a
// separate paragraph".
//
// Anthropic's `system` is a string or a list of blocks, and the blocks are text
// ones in every call the harness makes; a block that is not text has no part
// here and is refused. The order is the caller's, so the paragraphs Vertex
// reads are the paragraphs the agent wrote.
func vertexGeminiSystem(raw json.RawMessage) ([]any, error) {
	if text, ok := vertexJSONString(raw); ok {
		return []any{map[string]any{"text": text}}, nil
	}
	blocks, err := readVertexArray(raw, "system")
	if err != nil {
		return nil, untranslatableMember("system", "Vertex's systemInstruction is text or a list of text blocks, and this is neither")
	}
	if len(blocks) == 0 {
		return nil, untranslatableMember("system", "a systemInstruction with no part in it is not a system prompt, and Vertex's parts is where the prompt is")
	}
	parts := make([]any, 0, len(blocks))
	for i, block := range blocks {
		where := fmt.Sprintf("system[%d]", i)
		members, err := readVertexObject(block, where)
		if err != nil {
			return nil, err
		}
		fields := map[string]json.RawMessage{}
		for _, m := range members {
			switch m.Key {
			case "type", "text":
				fields[m.Key] = m.Value
			case "cache_control":
				// Dropped; see vertexGeminiCacheHint.
			default:
				return nil, untranslatableMember(vertexMemberPath(where, m.Key), vertexGeminiNoForm)
			}
		}
		kind, typed := vertexJSONString(fields["type"])
		if !typed {
			return nil, untranslatableMember(where+".type", "a block's type is the name of the kind of block it is, and this block's is absent or is not a name")
		}
		if kind != "text" {
			return nil, untranslatableMember(where, vertexGeminiNotTextBlock(kind))
		}
		text, hasText := fields["text"]
		if !hasText {
			return nil, untranslatableMember(where+".text", "a text block with no text in it is not a paragraph")
		}
		if _, ok := vertexJSONString(text); !ok {
			return nil, untranslatableMember(where+".text", "a part's text is a string and this is not one")
		}
		parts = append(parts, map[string]any{"text": text})
	}
	return parts, nil
}

// vertexGeminiContents carries the conversation as Vertex's contents: one
// Content per message, with the role Anthropic's name for it and the parts the
// message's blocks become.
//
// The tool calls the request itself carries are collected beside the
// translation and it is what makes a tool result translatable at all. Vertex
// names a functionResponse by the FUNCTION's name (the inference reference:
// FunctionResponse.name, "a string representing the FunctionDeclaration.name"),
// while Anthropic identifies a tool result by the ID of the call it answers; the
// `v1` API matches a response to its call by name and order and not by id —
// "By id ... (supported in the v1beta1 API for Gemini 3 and later models)" — so
// the name is the only thing that can be sent, and the only place it exists is
// the tool_use block earlier in this same conversation. A tool result whose call
// this request does not carry is therefore refused: sending it under an invented
// name would be a result the model reads as an answer to something the agent
// never asked, and dropping it would leave a call unanswered.
//
// The order is preserved on both sides — the parts in a message, and the
// messages — which is what Vertex's own matching rule leans on when one turn
// answers several calls.
func vertexGeminiContents(raw json.RawMessage) ([]any, error) {
	items, err := readVertexArray(raw, "messages")
	if err != nil {
		return nil, untranslatableMember("messages", "Vertex's contents is a list of messages and this is not one")
	}
	if len(items) == 0 {
		return nil, untranslatableMember("messages", "Vertex's contents is required and this call carries no message in it")
	}
	calls := map[string]string{}
	contents := make([]any, 0, len(items))
	for i, item := range items {
		where := fmt.Sprintf("messages[%d]", i)
		members, err := readVertexObject(item, where)
		if err != nil {
			return nil, err
		}
		var role, content json.RawMessage
		for _, m := range members {
			switch m.Key {
			case "role":
				role = m.Value
			case "content":
				content = m.Value
			default:
				return nil, untranslatableMember(vertexMemberPath(where, m.Key), vertexGeminiNoForm)
			}
		}
		if role == nil {
			return nil, untranslatableMember(where+".role", "a message with no role is a message Vertex cannot place in the conversation")
		}
		sent, ok := vertexJSONString(role)
		if !ok {
			return nil, untranslatableMember(where+".role", "a role is a string and this is not one")
		}
		switch sent {
		case "user":
		case "assistant":
			// Vertex's own name for the model's turn (Content.role: "user" or
			// "model").
			sent = "model"
		default:
			return nil, untranslatableMember(where+".role", "Vertex's contents has two roles, the caller's and the model's, and this is neither")
		}
		if content == nil {
			return nil, untranslatableMember(where+".content", "a message with no content carries no part for Vertex to read")
		}
		parts, err := vertexGeminiParts(content, where+".content", calls)
		if err != nil {
			return nil, err
		}
		contents = append(contents, map[string]any{"role": sent, "parts": parts})
	}
	return contents, nil
}

// vertexGeminiParts carries one message's content: a string, or the list of
// blocks the harness builds, in the order it wrote them.
func vertexGeminiParts(raw json.RawMessage, where string, calls map[string]string) ([]any, error) {
	if text, ok := vertexJSONString(raw); ok {
		return []any{map[string]any{"text": text}}, nil
	}
	blocks, err := readVertexArray(raw, where)
	if err != nil {
		return nil, untranslatableMember(where, "a message's content is text or a list of blocks, and this is neither")
	}
	if len(blocks) == 0 {
		return nil, untranslatableMember(where, "a message with no part in it is not a turn, and Vertex's parts is where the turn is")
	}
	parts := make([]any, 0, len(blocks))
	for i, block := range blocks {
		part, err := vertexGeminiPart(block, fmt.Sprintf("%s[%d]", where, i), calls)
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	return parts, nil
}

// vertexGeminiPart carries one Anthropic content block as one Vertex part. The
// three blocks the harness's calls are made of are the three this translation
// carries:
//
//   - text, as Part.text;
//   - tool_use, as Part.functionCall — the tool's name and its arguments. The
//     block's id is NOT carried: Vertex's `v1` functionCall has no id member,
//     and the id's whole use here is to find the name a later tool_result must
//     be sent under (vertexGeminiContents);
//   - tool_result, as Part.functionResponse, named for the call it answers and
//     carrying its output — under `output`, or `error` when the agent marked it
//     as a failure, which is the key convention Google's Gemini API reference
//     states for the response object.
//
// Everything else is refused, and the kind of block it is reaches the refusal
// only through this file's own vocabulary (vertexGeminiUncarriedBlocks), never
// as the text the call carried.
func vertexGeminiPart(raw json.RawMessage, where string, calls map[string]string) (any, error) {
	members, err := readVertexObject(raw, where)
	if err != nil {
		return nil, err
	}
	fields := map[string]json.RawMessage{}
	for _, m := range members {
		fields[m.Key] = m.Value
	}
	kind, typed := vertexJSONString(fields["type"])
	if !typed {
		return nil, untranslatableMember(where+".type", "a block's type is the name of the kind of block it is, and this block's is absent or is not a name")
	}
	delete(fields, "type")
	// cache_control is a hint and never a part; see vertexGeminiCacheHint.
	delete(fields, "cache_control")
	switch kind {
	case "text":
		text, ok := fields["text"]
		if !ok {
			return nil, untranslatableMember(where+".text", "a text block with no text in it is not a part")
		}
		if _, ok := vertexJSONString(text); !ok {
			return nil, untranslatableMember(where+".text", "a part's text is a string and this is not one")
		}
		if err := vertexNoOtherFields(fields, where, "text"); err != nil {
			return nil, err
		}
		return map[string]any{"text": text}, nil
	case "tool_use":
		id, ok := vertexJSONString(fields["id"])
		if !ok {
			return nil, untranslatableMember(where+".id", "a tool call's id is what its result is matched by, and this is not one")
		}
		name, ok := vertexJSONString(fields["name"])
		if !ok {
			return nil, untranslatableMember(where+".name", "Vertex names a function call by the function's name, and this is not one")
		}
		args, ok := fields["input"]
		if !ok || !vertexJSONIsObject(args) {
			return nil, untranslatableMember(where+".input", "Vertex's functionCall.args is a JSON object and this is not one")
		}
		if err := vertexNoOtherFields(fields, where, "id", "name", "input"); err != nil {
			return nil, err
		}
		calls[id] = name
		part := map[string]any{"functionCall": map[string]any{"name": name, "args": args}}
		signature, err := vertexGeminiThoughtSignature(id, where)
		if err != nil {
			return nil, err
		}
		if signature != "" {
			part["thoughtSignature"] = signature
		}
		return part, nil
	case "tool_result":
		callID, ok := vertexJSONString(fields["tool_use_id"])
		if !ok {
			return nil, untranslatableMember(where+".tool_use_id", "a tool result is matched to its call by that call's id, and this is not one")
		}
		name, answered := calls[callID]
		if !answered {
			return nil, untranslatableMember(where+".tool_use_id",
				"Vertex names a functionResponse by the function's name and matches it to a call of the same name, and this request carries no tool_use for this result, so the name it must be sent under is nowhere in the call — a result sent under an invented name would be read as an answer to something the agent never asked")
		}
		text, err := vertexGeminiToolResultContent(fields["content"], where+".content")
		if err != nil {
			return nil, err
		}
		failed := false
		if raw, ok := fields["is_error"]; ok {
			if failed, ok = vertexJSONBool(raw); !ok {
				return nil, untranslatableMember(where+".is_error", "whether a tool failed is true or false, and this is neither")
			}
		}
		if err := vertexNoOtherFields(fields, where, "tool_use_id", "content", "is_error"); err != nil {
			return nil, err
		}
		key := "output"
		if failed {
			key = "error"
		}
		return map[string]any{"functionResponse": map[string]any{
			"name":     name,
			"response": map[string]any{key: text},
		}}, nil
	}
	if why, known := vertexGeminiUncarriedBlocks[kind]; known {
		return nil, untranslatableMember(where, why)
	}
	return nil, untranslatableMember(where, "this is a content block of a kind this translation does not know, so it has no part on Vertex's Google publisher")
}

// vertexGeminiToolResultContent is what a tool result says, as the text Vertex
// receives: the result's content is a string in most calls, or a list of blocks
// when the agent returns more than one piece of it, and only text blocks have a
// form here. An absent result carries nothing, which is an answer the model may
// read as empty.
func vertexGeminiToolResultContent(raw json.RawMessage, where string) (string, error) {
	if raw == nil {
		return "", nil
	}
	if text, ok := vertexJSONString(raw); ok {
		return text, nil
	}
	blocks, err := readVertexArray(raw, where)
	if err != nil {
		return "", untranslatableMember(where, "a tool result's content is text or a list of blocks, and this is neither")
	}
	texts := make([]string, 0, len(blocks))
	for i, block := range blocks {
		at := fmt.Sprintf("%s[%d]", where, i)
		members, err := readVertexObject(block, at)
		if err != nil {
			return "", err
		}
		fields := map[string]json.RawMessage{}
		for _, m := range members {
			switch m.Key {
			case "type", "text":
				fields[m.Key] = m.Value
			case "cache_control":
				// Dropped; see vertexGeminiCacheHint.
			default:
				return "", untranslatableMember(vertexMemberPath(at, m.Key), vertexGeminiNoForm)
			}
		}
		kind, typed := vertexJSONString(fields["type"])
		if !typed {
			return "", untranslatableMember(at+".type", "a block's type is the name of the kind of block it is, and this block's is absent or is not a name")
		}
		if kind != "text" {
			if why, known := vertexGeminiUncarriedBlocks[kind]; known {
				return "", untranslatableMember(at, why)
			}
			return "", untranslatableMember(at, "this is a block of a kind this translation does not know, and a part of a tool result that is not text has no form on Vertex's Google publisher")
		}
		text, ok := vertexJSONString(fields["text"])
		if !ok {
			return "", untranslatableMember(at+".text", "a part's text is a string and this is not one")
		}
		texts = append(texts, text)
	}
	// More than one text block becomes one string, joined by a line break: the
	// model reads the result as Vertex's single response object, whose output
	// is one string, and the boundary between two consecutive text blocks is a
	// line break in every rendering of them. This is the one place this
	// translation composes bytes the caller did not write, and it composes one
	// the caller could have written (a text block holding a newline at its
	// end), rather than gluing two blocks into a word neither of them wrote.
	return strings.Join(texts, "\n"), nil
}

// vertexGeminiTools carries the agent's tools as Vertex's function
// declarations: name and description as they were written, and the Anthropic
// `input_schema` as the declaration's `parametersJsonSchema`, verbatim (see
// vertexGeminiSchema for why that member and not `parameters`).
//
// A tool that names a `type` is refused, and that is the acceptance's "unknown
// tool form": Anthropic's server tools (its web search, its code execution, its
// computer use) are a `type` and no function declaration at all — they are
// executed on Anthropic's side, and a Gemini model offered them as ordinary
// functions would call something that does not exist. Vertex's own
// server-side tools are a different wire this translation does not build, so
// the honest answer is a refusal rather than a declaration invented for one.
func vertexGeminiTools(raw json.RawMessage) ([]any, error) {
	items, err := readVertexArray(raw, "tools")
	if err != nil {
		return nil, untranslatableMember("tools", "Vertex's tools is a list, and this is not one")
	}
	declarations := make([]any, 0, len(items))
	for i, item := range items {
		where := fmt.Sprintf("tools[%d]", i)
		members, err := readVertexObject(item, where)
		if err != nil {
			return nil, err
		}
		var (
			name     string
			hasName  bool
			schema   json.RawMessage
			declared = map[string]any{}
		)
		for _, m := range members {
			switch m.Key {
			case "name":
				name, hasName = vertexJSONString(m.Value)
				if !hasName {
					return nil, untranslatableMember(where+".name", "a tool's name is a string and this is not one")
				}
			case "description":
				if _, ok := vertexJSONString(m.Value); !ok {
					return nil, untranslatableMember(where+".description", "a tool's description is a string and this is not one")
				}
				declared["description"] = m.Value
			case "input_schema":
				schema = m.Value
			case "cache_control":
				// Dropped; see vertexGeminiCacheHint.
			case "type":
				// A client tool marks itself `custom` — the only value the gateway
				// in front of this service lets through (vertexGeminiClientToolType)
				// — and the mark says nothing to a Gemini declaration, which offers
				// every tool as an ordinary function. It is dropped, as
				// `cache_control` is. Anything else IS a tool that is not an
				// ordinary function: Anthropic's server tools name themselves here
				// and have no declaration on this wire.
				if kind, ok := vertexJSONString(m.Value); ok && kind == vertexGeminiClientToolType {
					continue
				}
				return nil, untranslatableMember(where+".type",
					"a tool that names a type is not an ordinary tool the model is offered as a function — it is one of Anthropic's own server-side tools, which this wire has no declaration for — and this translation will not invent one")
			default:
				return nil, untranslatableMember(vertexMemberPath(where, m.Key), vertexGeminiNoForm)
			}
		}
		if !hasName {
			return nil, untranslatableMember(where+".name", "Vertex names a function by its name and this tool carries none")
		}
		if len(name) > vertexGeminiMaxFunctionNameBytes || !vertexGeminiFunctionNameRE.MatchString(name) {
			return nil, untranslatableMember(where+".name",
				"Vertex's function names start with a letter or an underscore and take 64 characters of letters, digits, underscores, dots and dashes, and this name is not one Vertex would accept")
		}
		if schema == nil {
			return nil, untranslatableMember(where+".input_schema", "Vertex's functionDeclaration takes a parameters schema and this tool carries none")
		}
		if err := vertexGeminiSchema(schema, where+".input_schema"); err != nil {
			return nil, err
		}
		declared["name"] = name
		declared["parametersJsonSchema"] = schema
		declarations = append(declarations, declared)
	}
	return declarations, nil
}

// vertexGeminiSchema holds a tool's `input_schema` to the one rule the wire
// states about it, and carries it untouched otherwise — which is the whole point
// of the member it is sent as.
//
// A function declaration takes its parameters as ONE OF TWO mutually exclusive
// members (the API's own discovery document, GoogleCloudAiplatformV1FunctionDeclaration):
// `parameters`, which reflects the OpenAPI 3.0 Schema Object and is typed, and
// `parametersJsonSchema`, "Describes the parameters to the function in JSON
// Schema format", which the API takes as it is (`type: any`). This translation
// sends the SECOND, and the reason is the harness it serves: an Anthropic
// `input_schema` IS a JSON Schema, and the tools Claude Code offers are written
// as one — they carry a `$schema`, an `additionalProperties: false` and numeric
// bounds — so a translation through `parameters` would have to drop or rewrite
// members the model is then no longer held to, which is the one thing this file
// does not do. The member's own documented example settles it: the API writes it
// with `additionalProperties: false` in it.
//
// So nothing here is filtered, renamed or reshaped, and an attribute this file
// has never heard of ($ref, $defs, pattern, minimum, unevaluatedProperties)
// rides through with the meaning JSON Schema already gives it, instead of being
// refused as an attribute Vertex's declarations "do not support" — a refusal
// that would have made every harness call carrying tools impossible to serve.
// The wire's own words are cited where the member is chosen, in the file header:
// https://aiplatform.googleapis.com/$discovery/rest?version=v1 publishes the
// nested types (GoogleCloudAiplatformV1FunctionDeclaration), which is the
// authority the prose page does not carry.
//
// What is still checked is the one thing the member itself states as a rule —
// "The schema must describe an object where the properties are the parameters to
// the function" — so a schema that is not a JSON object at all is refused with
// the typed reason rather than sent as something Vertex answers with an error no
// run can act on.
func vertexGeminiSchema(raw json.RawMessage, where string) error {
	if !vertexJSONIsObject(raw) {
		return untranslatableMember(where,
			"Vertex takes a tool's parameters as a JSON Schema object whose properties are the function's parameters, and this is not one")
	}
	return nil
}

// vertexGeminiToolChoice carries Anthropic's `tool_choice` as Vertex's
// functionCallingConfig, one mode for one mode:
//
//   - auto  -> AUTO   (the model decides);
//   - any   -> ANY    (a call is required);
//   - tool  -> ANY with allowed_function_names holding the one tool named, which
//     is how the function-calling page forces a single function;
//   - none  -> NONE   (calls are prohibited).
//
// `disable_parallel_tool_use` has no member on this wire, and it is refused when
// it is TRUE rather than dropped: an agent that asked for one call at a time and
// got several is an agent whose loop breaks on a turn it did not plan for. The
// false spelling says nothing the wire's default does not already do.
func vertexGeminiToolChoice(raw json.RawMessage) (map[string]any, error) {
	members, err := readVertexObject(raw, "tool_choice")
	if err != nil {
		return nil, err
	}
	var (
		mode  string
		named string
		hasTy bool
	)
	for _, m := range members {
		switch m.Key {
		case "type":
			var ok bool
			if mode, ok = vertexJSONString(m.Value); !ok {
				return nil, untranslatableMember("tool_choice.type", "a tool choice's type is a string and this is not one")
			}
			hasTy = true
		case "name":
			var ok bool
			if named, ok = vertexJSONString(m.Value); !ok {
				return nil, untranslatableMember("tool_choice.name", "a chosen tool's name is a string and this is not one")
			}
		case "disable_parallel_tool_use":
			disabled, ok := vertexJSONBool(m.Value)
			if !ok {
				return nil, untranslatableMember("tool_choice.disable_parallel_tool_use", "whether parallel calls are disabled is true or false, and this is neither")
			}
			if disabled {
				return nil, untranslatableMember("tool_choice.disable_parallel_tool_use",
					"Vertex's Google publisher has no member that forbids a model calling several functions at once, and a loop that planned for one call at a time must not be sent a turn with several")
			}
		default:
			return nil, untranslatableMember(vertexMemberPath("tool_choice", m.Key), vertexGeminiNoForm)
		}
	}
	if !hasTy {
		return nil, untranslatableMember("tool_choice.type", "a tool choice with no type names no mode this translation could send")
	}
	config := map[string]any{}
	switch mode {
	case "auto":
		config["mode"] = "AUTO"
	case "any":
		config["mode"] = "ANY"
	case "tool":
		if named == "" {
			return nil, untranslatableMember("tool_choice.name", "a tool choice that names a tool carries no name, so there is no function to allow")
		}
		config["mode"] = "ANY"
		config["allowedFunctionNames"] = []string{named}
	case "none":
		config["mode"] = "NONE"
	default:
		return nil, untranslatableMember("tool_choice.type", "Vertex's functionCallingConfig has four modes — the caller decides, a call is required, one named function is required, calls are prohibited — and this is not one of them")
	}
	return map[string]any{"functionCallingConfig": config}, nil
}

// vertexGeminiCacheHint is the first of this translation's two deliberate drops,
// and it is not written as a function because there is nothing to do: a
// `cache_control` member is dropped wherever it appears.
//
// It is a hint about PROMPT CACHING and not an input to the model: Anthropic's
// breakpoint says where a prompt may be cached so that the next call pays less
// for the same bytes, and the model's answer does not depend on it. Vertex's
// Google publisher does its own caching (its `cachedContent` member names a
// cache the CALLER creates out of band, which this service has no such name
// for), and the harness sets `cache_control` on the system prompt, on the last
// tool and on the blocks it marks, so a refusal here would refuse every call
// the harness makes. What is dropped is a cost hint; what would not be dropped
// is a member the model reads, and this translation refuses those.
//
// It is named here, once, so a reader can find every drop this file makes
// without reading for `continue` statements.

// vertexNoOtherFields refuses any member of a block this translation did not
// map, given the members it did. It exists so that each block kind states its
// own field set in one place, and a member beside it is refused rather than
// forgotten.
func vertexNoOtherFields(fields map[string]json.RawMessage, where string, carried ...string) error {
	known := map[string]bool{}
	for _, key := range carried {
		known[key] = true
	}
	for key := range fields {
		if !known[key] {
			return untranslatableMember(vertexMemberPath(where, key), vertexGeminiNoForm)
		}
	}
	return nil
}

// vertexGeminiNotTextBlock is why a block in a system prompt that is not text is
// refused: Vertex's systemInstruction takes text parts and nothing else.
func vertexGeminiNotTextBlock(kind string) string {
	if why, known := vertexGeminiUncarriedBlocks[kind]; known {
		return why
	}
	return "a system prompt holds text and nothing else (Vertex's systemInstruction takes text parts), and this is a block of a kind this translation does not know"
}

// The reading primitives below are this file's own: every object and array it
// walks is checked, so "a member is either carried or refused" holds of the
// reading too. They are strict where the wire is strict — one object and
// nothing after it, no duplicate member, no member of a JSON object that is not
// an object — and the errors they raise for a body that is not the shape this
// wire ever is are plain ones (the service's existing "not one Vertex could be
// sent" refusal), while a member this translation cannot CARRY raises the typed
// reason above.

// errVertexNotObject and errVertexNotArray are the plain errors above.
var (
	errVertexNotObject = errors.New("credsvc: the body of the call is not one JSON object")
	errVertexNotArray  = errors.New("credsvc: a list of this call is not a JSON array")
)

// vertexMember is one member of a JSON object: its name, and its value as the
// bytes it arrived as. The value is kept raw, which is what lets a member this
// translation does not rebuild reach Vertex exactly as the agent wrote it.
type vertexMember struct {
	Key   string
	Value json.RawMessage
}

// readVertexObject reads one JSON object into its members, in the order they
// were written, and refuses anything that is not exactly one object: bytes that
// are not JSON, a value that is not an object, a member named twice, and
// anything after the closing brace.
//
// The duplicate is refused because a JSON object that names a member twice has
// two answers to one question — Go's decoder would keep the last and the model
// would be sent the first or the second depending on which reader looked — and
// path is where the object sits in the request, so the refusal can name it.
func readVertexObject(raw []byte, path string) ([]vertexMember, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, errVertexNotObject
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, errVertexNotObject
	}
	var members []vertexMember
	seen := map[string]bool{}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, errVertexNotObject
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, errVertexNotObject
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, errVertexNotObject
		}
		if seen[key] {
			return nil, untranslatableMember(vertexMemberPath(path, key),
				"this member is named twice, and a request that names one member twice has two answers to one question")
		}
		seen[key] = true
		members = append(members, vertexMember{Key: key, Value: value})
	}
	if _, err := dec.Token(); err != nil {
		return nil, errVertexNotObject
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errVertexNotObject
	}
	return members, nil
}

// readVertexArray reads one JSON array into its elements, in order, and refuses
// anything that is not exactly one array.
func readVertexArray(raw []byte, path string) ([]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, errVertexNotArray
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '[' {
		return nil, errVertexNotArray
	}
	var items []json.RawMessage
	for dec.More() {
		var item json.RawMessage
		if err := dec.Decode(&item); err != nil {
			return nil, errVertexNotArray
		}
		items = append(items, item)
	}
	if _, err := dec.Token(); err != nil {
		return nil, errVertexNotArray
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errVertexNotArray
	}
	return items, nil
}

// vertexMemberPath is the path of a member inside the object at path: the
// member's own name at the top of the request, and the object's path and the
// member's name below it. It is what a refusal names, and it is built from the
// REQUEST's shape alone — the names of the members this wire has — so a refusal
// never carries text a call wrote.
func vertexMemberPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// vertexJSONString reads a JSON string, and whether the value is one.
func vertexJSONString(raw json.RawMessage) (string, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// vertexJSONBool reads a JSON boolean, and whether the value is one.
func vertexJSONBool(raw json.RawMessage) (bool, bool) {
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return false, false
	}
	return b, true
}

// vertexJSONNumber reads a JSON number, and whether the value is one (a string
// that spells one is not one).
func vertexJSONNumber(raw json.RawMessage) (json.Number, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return "", false
	}
	number, ok := value.(json.Number)
	return number, ok
}

// vertexJSONInt reads a JSON number that is a whole one, and whether the value
// is that. Anthropic's max_tokens is a count and Vertex's maxOutputTokens is a
// count; a fractional value is neither, and is refused rather than rounded.
func vertexJSONInt(raw json.RawMessage) (int64, bool) {
	number, ok := vertexJSONNumber(raw)
	if !ok {
		return 0, false
	}
	n, err := number.Int64()
	if err != nil {
		return 0, false
	}
	return n, true
}

// vertexJSONIsObject reports whether a value is a JSON object, which is what
// Vertex's functionCall.args and a tool's input_schema must be.
func vertexJSONIsObject(raw json.RawMessage) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return false
	}
	delim, ok := tok.(json.Delim)
	return ok && delim == '{'
}
