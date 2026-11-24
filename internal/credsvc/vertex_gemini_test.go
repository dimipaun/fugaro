package credsvc

// vertex_gemini_test.go holds GV1's own tests: the translation of a Messages
// call onto Vertex's GOOGLE publisher, and the refusals it makes instead of
// dropping a member it cannot carry.
//
// What is pinned is two things and no more. First, the translation itself, as
// golden pairs: testdata/vertex_gemini holds a Messages request and the
// generateContent request it must become, and a pair is compared as the JSON
// documents they are — key order is not a fact of either wire. The pairs are
// read from disk rather than written in the test, so the shape of the request
// Vertex is sent is reviewable as a request, and a reviewer can hold it against
// the pages the file's header cites (U8: the wire is not in this repository).
// The one subtest that does not read a pair drives the real path — serveVertex,
// the real spelling and price tables, the fake upstream — because "a call of a
// Gemini model goes to the google publisher" is a fact about the SERVICE and not
// about the function, and a stubbed map would prove nothing about the routing.
//
// Second, the refusal: a member this translation has no faithful form for is
// answered with the typed reason (StatusRequestRefused, ErrorRequestRefused),
// exactly one audit line, no value of the call anywhere, and no request to
// Vertex at all. The table below holds one case per class of member the
// acceptance names — an image block, a tool that is not an ordinary function —
// and the rest of the classes a call can carry that this wire has no form for.
//
// Two deliberate drops (cache_control, metadata.user_id) are the exception and
// are argued in vertex_gemini.go; the pairs carry both, so the drop is asserted
// by the translation that carries them and not by a sentence here.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
)

// geminiPairsDir is where the golden pairs live, one file per side of a pair.
const geminiPairsDir = "testdata/vertex_gemini"

// geminiPlanted is a value this test writes INTO the member it is about, so the
// no-value rule has something to look for. It is not a credential and is not
// shaped like one, and — deliberately — it is never handed to readCall as a
// scrub: what a test plants has to reach the translation as the caller wrote it,
// or an assertion that no refusal echoes it would hold for the wrong reason.
const geminiPlanted = "a-value-the-agent-wrote-that-no-refusal-may-echo"

// geminiPair reads one side of a golden pair. side is "anthropic" (the request
// the harness sends) or "google" (the request Vertex's Google publisher is sent).
func geminiPair(t *testing.T, name, side string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(geminiPairsDir, name+"."+side+".json"))
	if err != nil {
		t.Fatalf("reading the %s side of the %q pair: %v", side, name, err)
	}
	return raw
}

// geminiDoc is one document as the value it is, for a comparison that does not
// depend on the order the keys were written in.
func geminiDoc(t *testing.T, raw []byte) any {
	t.Helper()
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("this is not one JSON document: %v (%s)", err, raw)
	}
	return doc
}

// TestGeminiRequestTranslation: every member of a Messages call that this
// translation can carry reaches Vertex's Google publisher as the member of the
// generateContent request that means the same thing, and the members the path
// already answers (the model, the streaming choice) are not in the body at all.
//
// The pairs are the fact and not an illustration of it: each one is a whole
// request an agent's harness really builds (a plain conversation; a system
// prompt with a tool loop in it; every sampling member a caller sets; a forced
// tool), and the Google side is what Vertex must be sent for it.
//
// The tool loop's own round trip is pinned by the `tools` pair: the tool calls
// it replays carry the thought signature Gemini 3 checks, and the pair holds the
// id GV2 mints to the `thoughtSignature` this translation sends — including the
// base64 padding the id form drops. The subtest after the pairs is the other
// branch of that rule, a tool call whose id carries no signature at all.
//
// The `harness` pair is the one whose tool schemas are the HARNESS's own: a
// `$schema`, an `additionalProperties: false`, numeric bounds, an enum, a nested
// array of closed objects. Every one of those is a member a translation through
// the declaration's OTHER parameters member would have had to drop or refuse —
// and refusing them is refusing every tool-bearing call the harness makes, which
// is the call this whole file exists to serve. That pair is here so the shape is
// one a reviewer can read, and so a translation that starts filtering schemas
// again is red rather than silently narrow.
//
// Mutation (run, restore): carry `model`, `stream` or `anthropic_version` into
// the body, map a role or a block onto the wrong member, or drop the signature
// round trip, and the pair it belongs to is red.
func TestGeminiRequestTranslation(t *testing.T) {
	for _, name := range []string{"text", "tools", "harness", "sampling", "forced_tool"} {
		t.Run(name, func(t *testing.T) {
			sent, err := vertexGeminiBody(geminiPair(t, name, "anthropic"), false)
			if err != nil {
				t.Fatalf("the %q request was refused: %v", name, err)
			}
			want := geminiDoc(t, geminiPair(t, name, "google"))
			got := geminiDoc(t, sent)
			if reflect.DeepEqual(got, want) {
				return
			}
			t.Errorf("the translation of the %q request is not the generateContent request Vertex is sent:\ngot  %s\nwant %s",
				name, indent(t, got), indent(t, want))
		})
	}

	t.Run("a tool call whose id carries no signature is sent without one", func(t *testing.T) {
		body := `{"model":"google/gemini-3-pro","max_tokens":16,"messages":[{"role":"assistant","content":[` +
			`{"type":"tool_use","id":"toolu_01A09q90qw90lq917835lq9","name":"read_file","input":{"path":"a.txt"}}]}]}`
		sent, err := vertexGeminiBody([]byte(body), false)
		if err != nil {
			t.Fatalf("a tool call whose id a harness minted was refused: %v", err)
		}
		call := geminiDoc(t, sent).(map[string]any)["contents"].([]any)[0].(map[string]any)["parts"].([]any)[0].(map[string]any)
		if _, signed := call["thoughtSignature"]; signed {
			t.Error("a thought signature was sent for a tool call whose id carried none: this translation carries a signature when the call has one and never invents one, because a call a model answered without a signature has none to carry")
		}
		if _, called := call["functionCall"]; !called {
			t.Errorf("the tool call did not reach Vertex as a functionCall: %s", sent)
		}
	})
}

// indent is a document as one line of readable JSON, for a failure that has to
// show two whole requests.
func indent(t *testing.T, doc any) string {
	t.Helper()
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("rendering a document for a failure message: %v", err)
	}
	return string(raw)
}

// vertexGeminiIdentity is the service's own identity for a call of a GEMINI
// model: the project and region a deployment writes into its environment, and a
// token built at run time. CS4's own fixture (vertexServiceIdentity) replaces
// pricing.SpellingFor with the single answer its Claude fixture needs; a Gemini
// call must instead go through the tables as they ship, because "which publisher
// serves this model, and under which spelling" is exactly the mapping under test
// here and a stubbed map would answer it for the code.
func vertexGeminiIdentity(t *testing.T) string {
	t.Helper()
	t.Setenv(vertexProjectEnv, testVertexProject)
	t.Setenv(vertexRegionEnv, testVertexRegion)
	token := fakeCred(t, "service-vertex-gemini")
	sign := vertexTokenFor
	vertexTokenFor = func(context.Context) (oauth2.TokenSource, error) {
		return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token, TokenType: "Bearer"}), nil
	}
	t.Cleanup(func() { vertexTokenFor = sign })
	return token
}

// vertexGeminiFixture is vertexFixture for a Gemini call: the same recorder in
// front of the fixture's transport, and the real tables behind it.
func vertexGeminiFixture(t *testing.T, script ...anthropicfake.Reply) (*svc, *vertexWatch, string) {
	t.Helper()
	token := vertexGeminiIdentity(t)
	watch := &vertexWatch{}
	s := newSvcWith(t, func(_ *svc, cfg *Config) { wrapVertexClient(cfg, watch) })
	s.up.Script = script
	return s, watch, token
}

// TestAGeminiCallGoesToTheGooglePublisher is the routing half of GV1, driven
// through serveVertex: a call naming a Gemini model is built for Vertex's Google
// publisher — the path segment, the method, and the body the pair above pins —
// and it is still made on the SERVICE's own identity, carrying none of the
// caller's tokens.
//
// The model the body names is `google/gemini-3-flash`, which is the canonical ID
// a pin carries and NOT the ID Vertex spells (vertex_gemini.go's header): what
// appears in the path is the map's own answer, so a service that put the
// requested ID in the path — the one mistake this wire makes easy, because the
// ID `google/gemini-3-flash` almost reads as a publisher path — is red here.
//
// Mutation (run, restore): answer the Anthropic publisher for a Gemini model, or
// write the canonical ID into the path, and the URL assertions are red.
func TestAGeminiCallGoesToTheGooglePublisher(t *testing.T) {
	prefix := "/v1/projects/" + testVertexProject + "/locations/" + testVertexRegion +
		"/publishers/google/models/"

	t.Run("a messages call is a generateContent", func(t *testing.T) {
		s, watch, service := vertexGeminiFixture(t, anthropicfake.MessageOK("google/gemini-3-flash", testUsage))
		cl := vertexCall(t, MessagesPath, string(geminiPair(t, "tools", "anthropic")))

		done, tried, rec, _ := serveVertexCall(t, s, vertexIdentity(s, testProject), cl, testVertexSource())
		if !done {
			t.Fatalf("the source declined a call of a Gemini model (%+v): the checked-in tables spell one for Vertex", tried)
		}
		if tried != (Tried{}) {
			t.Errorf("the source answered %+v beside a served call, want the zero row", tried)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("the call answered %d, want 200: %s", rec.Code, rec.Body)
		}
		if got, want := watch.sawURL(t, 0), "https://"+testVertexRegion+"-aiplatform.googleapis.com"+prefix+"gemini-3-flash:generateContent"; got != want {
			t.Errorf("the call went to %q, want %q", got, want)
		}
		if got := rec.Header().Get(HeaderTier); got != TierB {
			t.Errorf("%s = %q, want %q: a Gemini model on the project's Vertex is real money, as a Claude one is", HeaderTier, got, TierB)
		}
		if n := s.up.Count(); n != 1 {
			t.Fatalf("Vertex saw %d requests, want 1", n)
		}
		if got := s.up.Seen()[0].Header.Get("Authorization"); got != "Bearer "+service {
			t.Errorf("Vertex's Authorization = %q, want the service's own credential", got)
		}
		want := geminiDoc(t, geminiPair(t, "tools", "google"))
		if got := geminiDoc(t, s.up.Body(0)); !reflect.DeepEqual(got, want) {
			t.Errorf("the body Vertex received is not the translated request:\ngot  %s\nwant %s", indent(t, got), indent(t, want))
		}
	})

	t.Run("a streamed call is a streamGenerateContent", func(t *testing.T) {
		s, watch, _ := vertexGeminiFixture(t, anthropicfake.StreamOK("google/gemini-3-flash", testUsage))
		// The sampling pair is the one that carries `"stream": true`, which is
		// the caller's own member and the whole of how this method is chosen.
		cl := vertexCall(t, MessagesPath, string(geminiPair(t, "sampling", "anthropic")))
		done, _, rec, _ := serveVertexCall(t, s, vertexIdentity(s, testProject), cl, testVertexSource())
		if !done || rec.Code != http.StatusOK {
			t.Fatalf("the streamed call is answered %d (%v), want it served", rec.Code, done)
		}
		if got, want := watch.sawURL(t, 0), "https://"+testVertexRegion+"-aiplatform.googleapis.com"+prefix+"gemini-3-flash:streamGenerateContent"; got != want {
			t.Errorf("the streamed call went to %q, want %q: the caller's own `stream` member chooses the method here as it does on the Anthropic publisher", got, want)
		}
	})

	t.Run("a token count is a countTokens", func(t *testing.T) {
		s, watch, _ := vertexGeminiFixture(t, anthropicfake.MessageOK("google/gemini-3-flash", testUsage))
		// The tools pair is the one that carries a `tool_choice`, which is what
		// makes this subtest about the COUNTING body and not only about the path:
		// Vertex's CountTokensRequest has no toolConfig member at all.
		cl := vertexCall(t, CountTokensPath, string(geminiPair(t, "tools", "anthropic")))
		done, _, rec, _ := serveVertexCall(t, s, vertexIdentity(s, testProject), cl, testVertexSource())
		if !done || rec.Code != http.StatusOK {
			t.Fatalf("the token count is answered %d (%v), want it served", rec.Code, done)
		}
		if got, want := watch.sawURL(t, 0), "https://"+testVertexRegion+"-aiplatform.googleapis.com"+prefix+"gemini-3-flash:countTokens"; got != want {
			t.Errorf("the token count went to %q, want %q: Google's counting endpoint names its model in the path, unlike the Anthropic publisher's", got, want)
		}
		counted := geminiDoc(t, s.up.Body(0)).(map[string]any)
		if _, named := counted["model"]; named {
			t.Error("the counting request names a model in its body: this publisher takes it from the path alone")
		}
		if _, sent := counted["toolConfig"]; sent {
			t.Errorf("the counting request carries a toolConfig: Vertex's CountTokensRequest has no such member, so the call is one it rejects as an unknown field, and a tool choice changes nothing about the input being counted: %s", s.up.Body(0))
		}
		if _, sent := counted["tools"]; !sent {
			t.Errorf("the counting request carries no tools: a declaration is part of the input the count is of, so it is the tool CHOICE that is left out and not the tools: %s", s.up.Body(0))
		}
	})
}

// geminiRefused is one case of the table below: a name, and a whole request that
// carries ONE member this translation has no faithful form for.
type geminiRefused struct {
	name string
	body string
	// member is the path of the member in the request, which the refusal must
	// name and which is the field and never its content.
	member string
}

// TestAnUntranslatableMemberIsRefused: the acceptance's second half. A member
// this translation cannot carry faithfully is refused with a typed reason — the
// run is answered 400 request_refused, which is a refusal about the CALL and not
// about the source, so the chain does not try another one for it — and it is
// never dropped: nothing is sent to Vertex, exactly one audit line is written,
// and neither that line nor the answer carries a value of the call.
//
// Every member here is one a harness really sends. The classes are the
// acceptance's own two (an image block, a tool that is not an ordinary function)
// and the rest of what a Messages call can carry that generateContent has no
// member for: a member of the envelope this build has never mapped, a tool
// result whose call is not in the request — the name a functionResponse must be
// sent under is then nowhere in the call — an `input_schema` that is not a JSON
// Schema object at all (the one thing the parameters member's own rule states:
// it must describe an object whose properties are the function's parameters),
// a thinking block (Gemini 3 takes a discrete level, not Anthropic's budget),
// more stop sequences than Vertex takes, and a tool choice that forbids parallel
// calls, which this wire cannot say.
//
// A schema's ATTRIBUTES are not on this list and must never be: the schema is
// carried whole as `parametersJsonSchema` (vertex_gemini.go), so `$schema`,
// `additionalProperties` and the numeric bounds the harness writes are sent as
// they are rather than refused — the `harness` pair is where that is pinned.
//
// Mutation (run, restore): drop the member instead of refusing it, and the audit
// count, the status and the upstream count below are red.
func TestAnUntranslatableMemberIsRefused(t *testing.T) {
	const (
		head = `{"model":"google/gemini-3-pro","max_tokens":16,`
		tail = `"messages":[{"role":"user","content":"hello"}]}`
	)
	for _, tc := range []geminiRefused{{
		name: "an image block",
		body: `{"model":"google/gemini-3-pro","max_tokens":16,"messages":[{"role":"user","content":[` +
			`{"type":"text","text":"look at this"},` +
			`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + geminiPlanted + `"}}]}]}`,
		member: "messages[0].content[1]",
	}, {
		name:   "a tool that is not an ordinary function",
		body:   head + `"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":5}],` + tail,
		member: "tools[0].type",
	}, {
		name:   "a member of the envelope this translation has never mapped",
		body:   head + `"container":"` + geminiPlanted + `",` + tail,
		member: "container",
	}, {
		name: "a tool result whose call is not in the request",
		body: `{"model":"google/gemini-3-pro","max_tokens":16,"messages":[{"role":"user","content":[` +
			`{"type":"tool_result","tool_use_id":"` + geminiPlanted + `","content":"ok"}]}]}`,
		member: "messages[0].content[0].tool_use_id",
	}, {
		name:   "a tool whose input_schema is not a schema at all",
		body:   head + `"tools":[{"name":"read_file","input_schema":"` + geminiPlanted + `"}],` + tail,
		member: "tools[0].input_schema",
	}, {
		name: "a tool call whose signature cannot be read",
		body: `{"model":"google/gemini-3-pro","max_tokens":16,"messages":[{"role":"assistant",` +
			`"content":[{"type":"tool_use","id":"` + vertexGeminiThoughtSignatureID + `not-a-signature!!","name":"read_file","input":{"path":"a.txt"}}]}]}`,
		member: "messages[0].content[0].id",
	}, {
		name: "a thinking block",
		body: `{"model":"google/gemini-3-pro","max_tokens":16,"messages":[{"role":"assistant",` +
			`"content":[{"type":"thinking","thinking":"` + geminiPlanted + `","signature":"sig"}]}]}`,
		member: "messages[0].content[0]",
	}, {
		name:   "more stop sequences than Vertex takes",
		body:   head + `"stop_sequences":["one","two","three","four","five","` + geminiPlanted + `"],` + tail,
		member: "stop_sequences",
	}, {
		name:   "a tool choice that forbids parallel calls",
		body:   head + `"tool_choice":{"type":"auto","disable_parallel_tool_use":true},` + tail,
		member: "tool_choice.disable_parallel_tool_use",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			s, auditLog := refusalSvc(t, nil)
			token := vertexGeminiIdentity(t)
			cl := vertexCall(t, MessagesPath, tc.body)

			done, tried, rec, _ := serveVertexCall(t, s, vertexIdentity(s, testProject), cl, testVertexSource())
			if !done {
				t.Fatalf("the source declined the call instead of refusing it (%+v): a member this translation cannot carry is the CALL's own refusal, and no other source would serve it either", tried)
			}
			if tried != (Tried{}) {
				t.Errorf("the source answered %+v beside its own refusal, want the zero row", tried)
			}
			if rec.Code != StatusRequestRefused {
				t.Fatalf("the call is answered %d, want %d: the wire's own request refusal", rec.Code, StatusRequestRefused)
			}
			if n := s.up.Count(); n != 0 {
				t.Errorf("Vertex saw %d requests, want none: a member that cannot be carried faithfully is refused, never sent without it", n)
			}

			lines := auditLines(t, auditLog)
			if len(lines) != 1 {
				t.Fatalf("the audit holds %d lines, want exactly 1: %v", len(lines), lines)
			}
			if got := lines[0]["status"]; got != ErrorRequestRefused {
				t.Errorf("the audit line's status is %v, want %q: the run is refused for the call it made", got, ErrorRequestRefused)
			}

			// The refusal NAMES the member — that is what tells an operator which
			// mapping to add — and carries no value of it.
			answer := rec.Body.String()
			if !strings.Contains(answer, tc.member) {
				t.Errorf("the refusal does not name the member it is about (%s): %s", tc.member, answer)
			}
			if !strings.Contains(answer, ErrorRequestRefused) {
				t.Errorf("the refusal is not typed as %q: %s", ErrorRequestRefused, answer)
			}
			for what, raw := range map[string]string{"audit": auditLog.String(), "answer": answer} {
				for _, value := range []string{geminiPlanted, token} {
					if strings.Contains(raw, value) {
						t.Errorf("the %s carries a value it must never carry: %s", what, raw)
					}
				}
			}
		})
	}
}
