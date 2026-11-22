package credsvc

import (
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/routing"
)

// The request an agent makes, as the wire carries it: members the service has
// no business touching (a tool-shaped content block, a metadata block that has
// a `model` of its own), a string with an escaped quote in it, and the model
// member in the middle. A rewrite that re-encoded the body, or that spliced the
// wrong span, changes something here.
const agentRequest = `{"max_tokens":1024,"model":"claude-sonnet-5-5",` +
	`"messages":[{"role":"user","content":[{"type":"text","text":"why is \"this\" here?"}]}],` +
	`"metadata":{"user_id":"u-1","model":"not-a-model-member"},"stream":false}`

// The same request as a provider key is sent it: the route's own ID in the
// model member and not one byte anywhere else.
const providerRequest = `{"max_tokens":1024,"model":"anthropic/claude-sonnet-5.5",` +
	`"messages":[{"role":"user","content":[{"type":"text","text":"why is \"this\" here?"}]}],` +
	`"metadata":{"user_id":"u-1","model":"not-a-model-member"},"stream":false}`

// TestRewriteTouchesOnlyTheModel is CS3's pin on the one edit the service makes
// to a body (D3175): the `model` member becomes the spelling the source needs
// and every other byte is the caller's own. The bodies below are written out in
// full on both sides on purpose — a rewrite that decoded and re-encoded the
// body would answer the same model and a different body, and only a whole-body
// comparison sees it.
//
// Hand mutations this fails on: re-encoding the body (the member order and the
// spacing move); rewriting the nested `model` in metadata instead of the
// top-level one; rewriting the spelling inside the escaped string; and reading
// the model member with a substring search, which finds the metadata one.
func TestRewriteTouchesOnlyTheModel(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		to   string
		want string
	}{
		{
			name: "a provider key's spelling",
			body: agentRequest,
			to:   "anthropic/claude-sonnet-5.5",
			want: providerRequest,
		},
		{
			name: "the Claude API's own spelling, which is the one that came in",
			body: agentRequest,
			to:   "claude-sonnet-5-5",
			want: agentRequest,
		},
		{
			name: "the model member is not the first one",
			body: `{"messages":[{"role":"user","content":"hi"}],"model":"claude-opus-5-5"}`,
			to:   "anthropic/claude-opus-5.5",
			want: `{"messages":[{"role":"user","content":"hi"}],"model":"anthropic/claude-opus-5.5"}`,
		},
		{
			// The spacing is the caller's, and it stays: a space before the
			// colon, a tab after it, two before the comma, and the object
			// spread over lines. A decode-and-encode round trip answers
			// `{"max_tokens":8,"messages":[],"model":"..."}` instead.
			name: "the caller's own spacing",
			body: "{\n\t\"max_tokens\" : 8,\n\t\"model\"\t:\t\"claude-haiku-4-5\"  ,\n\t\"messages\" : []\n}\n",
			to:   "claude-haiku-4-5@20251001",
			want: "{\n\t\"max_tokens\" : 8,\n\t\"model\"\t:\t\"claude-haiku-4-5@20251001\"  ,\n\t\"messages\" : []\n}\n",
		},
		{
			// The member the agent's harness sends after a tool result: the
			// model is last, and the body has a tool definition whose schema
			// mentions a model of its own.
			name: "a body with a tool definition",
			body: `{"tools":[{"name":"t","input_schema":{"type":"object","properties":{"model":{"type":"string"}}}}],"model":"claude-sonnet-5-5"}`,
			to:   "anthropic/claude-sonnet-5.5",
			want: `{"tools":[{"name":"t","input_schema":{"type":"object","properties":{"model":{"type":"string"}}}}],"model":"anthropic/claude-sonnet-5.5"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := rewriteModel([]byte(tc.body), tc.to)
			if err != nil {
				t.Fatalf("rewriteModel(%s, %q): %v", tc.body, tc.to, err)
			}
			if string(got) != tc.want {
				t.Errorf("rewriteModel(%s, %q) =\n%s\nwant\n%s", tc.body, tc.to, got, tc.want)
			}
			// The other direction: what the source answered is restored to what
			// went out, which is the same edit over the same span.
			back, err := rewriteModel(got, modelOf(t, tc.body))
			if err != nil {
				t.Fatalf("rewriteModel back: %v", err)
			}
			if string(back) != tc.body {
				t.Errorf("the rewrite back =\n%s\nwant\n%s", back, tc.body)
			}
		})
	}

	// A body this service cannot read one model out of is refused, and the
	// refusal names no value: a call whose model is unknown cannot be resolved
	// against the table or priced.
	for _, tc := range []struct{ name, body string }{
		{"no model member", `{"messages":[{"role":"user","content":"hi"}]}`},
		{"only a nested model", `{"metadata":{"model":"claude-opus-5-5"}}`},
		{"a model that is not a string", `{"model":null,"messages":[]}`},
		{"a model that is a number", `{"model":5}`},
		{"two models", `{"model":"claude-opus-5-5","model":"claude-sonnet-5-5"}`},
		{"not an object", `[{"model":"claude-opus-5-5"}]`},
	} {
		if got, err := rewriteModel([]byte(tc.body), "claude-opus-5-5"); !errors.Is(err, errModelMember) {
			t.Errorf("rewriteModel(%s) = (%s, %v), want errModelMember", tc.body, got, err)
		}
	}

	// A body that is not one JSON object is a read error and not
	// errModelMember: the call named a model, and what could not be read is the
	// body. Two objects, or anything after the object, is the case that matters
	// — the source would be sent all of it while the call is priced by the
	// first object's model — and an answer stream is the other one: it carries
	// its model inside message_start's message member, event by event, so a
	// caller holding one leaves it alone rather than reading events it did not
	// ask to rewrite.
	for _, body := range []string{
		``,
		`not json`,
		`{"model":"claude-opus-5-5"`,
		`{"model":"claude-opus-5-5"}{"model":"claude-sonnet-5-5"}`,
		`{"model":"claude-opus-5-5"} junk`,
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-sonnet-5-5\"}}\n\n",
	} {
		got, err := rewriteModel([]byte(body), "claude-opus-5-5")
		if err == nil {
			t.Errorf("rewriteModel(%q) = (%s, nil), want an error", body, got)
			continue
		}
		if errors.Is(err, errModelMember) {
			t.Errorf("rewriteModel(%q) = %v, want a read error rather than errModelMember", body, err)
		}
	}
}

// modelOf is the top-level model member of a body, for the round trip above:
// the fixture's own model, read through the same span the rewrite uses.
func modelOf(t *testing.T, body string) string {
	t.Helper()
	start, end, err := modelMember([]byte(body))
	if err != nil {
		t.Fatalf("modelMember(%s): %v", body, err)
	}
	return strings.Trim(string([]byte(body)[start:end]), `"`)
}

// The answer a provider key gives in one piece: its own spelling of the model,
// plus the members a Messages answer carries.
const providerAnswer = `{"id":"msg_1","type":"message","role":"assistant","model":"anthropic/claude-sonnet-5.5",` +
	`"content":[{"type":"text","text":"done"}],"stop_reason":"end_turn",` +
	`"usage":{"input_tokens":9,"output_tokens":3}}`

// The same answer as the agent reads it: the model the agent asked for, and
// nothing else moved.
const agentAnswer = `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-5-5",` +
	`"content":[{"type":"text","text":"done"}],"stop_reason":"end_turn",` +
	`"usage":{"input_tokens":9,"output_tokens":3}}`

// TestTheAnswerShowsTheModelTheAgentAsked is CS3's pin on the two directions at
// once: a call that moves to a provider key goes out in the route's own ID, and
// the answer the agent reads names the model the agent asked for — the agent
// never sees another spelling (D3175).
func TestTheAnswerShowsTheModelTheAgentAsked(t *testing.T) {
	const asked = "claude-sonnet-5-5"
	src := routing.Source{Kind: routing.KindProvider, Provider: "openrouter"}

	// What the answer's model member tells the gateway to price: the canonical
	// ID of the model that served, which here is the model the agent asked for.
	canonical, spelling, ok := sourceModel(src, asked)
	if !ok {
		t.Fatalf("sourceModel(%+v, %q) answered no spelling: a provider key with a measured route must serve it", src, asked)
	}
	if canonical != asked {
		t.Errorf("the answer would name %q, want %q: a provider key that is not a written downgrade serves the model the agent asked for", canonical, asked)
	}
	if spelling != "anthropic/claude-sonnet-5.5" {
		t.Errorf("the request would be sent %q, want the route's own ID %q", spelling, "anthropic/claude-sonnet-5.5")
	}

	// Out in the source's spelling, back in the agent's.
	sent, err := rewriteModel([]byte(agentRequest), spelling)
	if err != nil {
		t.Fatalf("spelling the request: %v", err)
	}
	if string(sent) != providerRequest {
		t.Errorf("the request the provider key is sent =\n%s\nwant\n%s", sent, providerRequest)
	}
	back, err := rewriteModel([]byte(providerAnswer), asked)
	if err != nil {
		t.Fatalf("restoring the answer: %v", err)
	}
	if string(back) != agentAnswer {
		t.Errorf("the answer the agent reads =\n%s\nwant\n%s", back, agentAnswer)
	}
	if strings.Contains(string(back), spelling) {
		t.Errorf("the answer the agent reads still carries the route's own ID %q", spelling)
	}
}

// TestAWrittenDowngradeIsPricedAsWhatServed: a source entry that names a model
// is a written downgrade, and it is the same rewrite — the model the source is
// sent is the named one, in that source's spelling — while what the call is
// PRICED by, and what the answer's model member carries, is the named model's
// canonical ID and not the model the agent asked for (C1's HeaderModel). The
// agent, though, is still shown the model it asked for: the spelling it did not
// ask for is nowhere in what it reads.
func TestAWrittenDowngradeIsPricedAsWhatServed(t *testing.T) {
	const asked = "claude-sonnet-5-5"
	for _, tc := range []struct {
		name string
		src  routing.Source
		// canonical is what the answer's model member carries: the model the
		// gateway prices the call by.
		canonical string
		// spelling is how that model is written on the source. For a provider
		// key it is the body's model member; for Vertex it is the ID the URL
		// path carries, which CS4 builds (the plan excludes Vertex's wire from
		// the body rewrite, not from the map).
		spelling string
	}{
		{
			name:      "a Vertex source written down as haiku",
			src:       routing.Source{Kind: routing.KindVertex, Model: "claude-haiku-4-5"},
			canonical: "claude-haiku-4-5",
			spelling:  "claude-haiku-4-5@20251001",
		},
		{
			name:      "a provider key written down as opus",
			src:       routing.Source{Kind: routing.KindProvider, Provider: "openrouter", Model: "claude-opus-5-5"},
			canonical: "claude-opus-5-5",
			spelling:  "anthropic/claude-opus-5.5",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			canonical, spelling, ok := sourceModel(tc.src, asked)
			if !ok {
				t.Fatalf("sourceModel(%+v, %q) answered no spelling", tc.src, asked)
			}
			if canonical != tc.canonical {
				t.Errorf("the call would be priced as %q, want %q: the source entry names the model that serves", canonical, tc.canonical)
			}
			if spelling != tc.spelling {
				t.Errorf("the source would be sent %q, want %q", spelling, tc.spelling)
			}
			// The answer is restored to the model the agent asked for, and the
			// downgraded spelling is not in it: what the agent sees is its own
			// model, and what the run is charged is the header's.
			answer := `{"type":"message","model":"` + tc.spelling + `","content":[]}`
			back, err := rewriteModel([]byte(answer), asked)
			if err != nil {
				t.Fatalf("restoring the answer: %v", err)
			}
			if want := `{"type":"message","model":"` + asked + `","content":[]}`; string(back) != want {
				t.Errorf("the answer the agent reads = %s, want %s", back, want)
			}
			if strings.Contains(string(back), tc.spelling) {
				t.Errorf("the answer the agent reads still names what served (%q)", tc.spelling)
			}
		})
	}
}

// TestAModelWithNoSpellingIsNeverSent is the fail-closed half of D3175: a
// source the map does not spell a model for is not served the call — and in
// particular is never sent the ID the agent asked with, which is a model nobody
// measured on that source. The answer carries no value at all, so a caller has
// nothing to fall back to.
func TestAModelWithNoSpellingIsNeverSent(t *testing.T) {
	for _, tc := range []struct {
		name  string
		src   routing.Source
		asked string
	}{
		{
			name:  "a provider key for a model with no measured route ID",
			src:   routing.Source{Kind: routing.KindProvider, Provider: "openrouter", Model: "claude-sonnet-5"},
			asked: "claude-sonnet-5-5",
		},
		{
			name:  "a provider key for a model the map does not carry",
			src:   routing.Source{Kind: routing.KindProvider, Provider: "openrouter"},
			asked: "claude-nova-9",
		},
		{
			name:  "a Vertex source for a model the map does not carry",
			src:   routing.Source{Kind: routing.KindVertex},
			asked: "gemini-2.5-pro",
		},
		{
			name:  "a source of a kind this build does not serve",
			src:   routing.Source{Kind: routing.Kind("not-a-kind")},
			asked: "claude-sonnet-5-5",
		},
		{
			name:  "a body that names no model",
			src:   routing.Source{Kind: routing.KindAnthropic},
			asked: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			canonical, spelling, ok := sourceModel(tc.src, tc.asked)
			if ok {
				t.Fatalf("sourceModel(%+v, %q) = (%q, %q, true), want no answer: a model the map does not spell for that source is never sent there", tc.src, tc.asked, canonical, spelling)
			}
			if canonical != "" || spelling != "" {
				t.Errorf("sourceModel(%+v, %q) answered (%q, %q) with ok false: a refusal carries no value", tc.src, tc.asked, canonical, spelling)
			}
		})
	}
}

// TestEveryKindIsSpelled holds pricing.SpellingFor's five kinds to
// routing.Kinds. It lives here because internal/pricing may not import
// internal/routing — routing reads SpellingFor, so the import would be a cycle
// — and this package imports both. The mutation it fails on: adding a sixth
// kind to routing.Kinds (which every reader's switch then has to answer) and
// leaving the map's translation answering "no spelling" for it, so that every
// source of the new kind is silently skipped rather than served.
func TestEveryKindIsSpelled(t *testing.T) {
	const model = "claude-sonnet-5-5"
	for _, kind := range routing.Kinds {
		got, ok := pricing.SpellingFor(string(kind), model)
		if !ok || got == "" {
			t.Errorf("pricing.SpellingFor(%q, %q) = (%q, %v), want a spelling: every kind in routing.Kinds is a source the walk may serve", kind, model, got, ok)
		}
	}
	// And the two kinds that are not sources of a chain are not spelled: a
	// value that is not one of routing.Kinds answers nothing, so a caller
	// cannot invent a kind and be served.
	for _, kind := range []string{"", "gemini", "Vertex", "provider-key"} {
		if got, ok := pricing.SpellingFor(kind, model); ok {
			t.Errorf("pricing.SpellingFor(%q, %q) = (%q, true), want no answer: %q is not one of routing.Kinds", kind, model, got, kind)
		}
	}
}
