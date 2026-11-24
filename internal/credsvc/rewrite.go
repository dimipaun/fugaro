package credsvc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/routing"
)

// This file is one model, each source's spelling: CS3 of
// docs/model-credentials-routing/implementation-plan.md, D3175 of
// docs/model-credentials-routing/reconciliation.md.
//
// One model has a different ID per source. The Claude API takes the ID a pin
// carries (claude-sonnet-5-5, what the agent asked with), a provider key takes
// its route's own ID (anthropic/claude-sonnet-5.5), and Vertex takes its own
// spelling (claude-haiku-4-5@20251001 where Vertex dates the model). Which
// source serves a call is the chain walk's answer (CS1); this file is what
// happens to the call once the walk has chosen, and it is two facts and one
// edit:
//
//   - the model a source is SENT, and the model the call is PRICED by
//     (sourceModel). They are not always the same: a source entry may name a
//     model — a written downgrade, which spec.md requires to be written
//     explicitly — and even when it does not, what travels to the source is the
//     model's spelling on it. The answer's HeaderModel carries the canonical ID
//     of the model that served, because that is the model the gateway prices
//     the call by (C1); pricing by the requested model would charge another
//     model's price.
//   - the one edit the service makes to a body (rewriteModel): the `model`
//     member becomes the source's spelling on the way out and the model the
//     agent asked for on the way back, so the agent never sees a spelling that
//     is not its own. Nothing else in the body moves.
//
// **A source with no spelling for a model is never sent it** (D3175). The map
// is the only authority for a spelling (pricing.SpellingFor), and a source the
// map does not spell that model for — a provider key for a model with no
// measured route ID, a Vertex source for a model the map does not carry — is
// skipped by the walk rather than sent the ID the agent asked with, which would
// be a call to a model nobody measured on that source. sourceModel answers
// false for it and hands back no value at all, so a caller has nothing to fall
// back to; the walk treats it the way it treats a source it may not serve.
//
// **The audit line is the serving path's, not this file's.** A call this file
// refuses (a body naming no model) is refused by the walk that made the call,
// which is where every refusal of a call is recorded once, after the caller's
// identity is verified (audit.go, F12). These helpers hold no auditor and write
// no line of their own: two writers of one refusal is two lines for one
// refusal, and the rule here is exactly one.

// sourceModel answers what a source serves for a call that asked for asked:
//
//	canonical  the canonical Claude ID of the model this source will be sent.
//	           It is what the answer's HeaderModel carries and what the gateway
//	           prices the call by (C1) — the model the source entry names when it
//	           names one (a written downgrade), and asked itself otherwise.
//	spelling   how that model is written on this source, which is what the
//	           request's `model` member must become (rewriteModel).
//
// ok is false when the source has no spelling for that model, and such a source
// may not serve the call (D3175): the caller skips it, and must never send asked
// instead, which is a model nobody measured on that source.
//
// A source that names no model and a model with no spelling are both "not this
// source" and both answer ("", "", false) — no half of an answer, so there is
// nothing a caller could mistake for a decision. The reason is the walk's to
// state in its refusal (an unadmitted source, an exhausted source and a model
// this map does not spell are all "try the next one"), and an empty model is
// refused here as well as there because a body naming no model has no canonical
// ID to price.
func sourceModel(src routing.Source, asked string) (canonical, spelling string, ok bool) {
	if asked == "" {
		return "", "", false
	}
	canonical = src.Model
	if canonical == "" {
		canonical = asked
	}
	spelling, ok = pricing.SpellingFor(string(src.Kind), canonical)
	if !ok {
		return "", "", false
	}
	return canonical, spelling, true
}

// errModelMember is the answer for a body this service cannot read one model
// out of: no top-level `model` member at all, a `model` member whose value is
// not a string, or two of them naming two models. All three are a call that
// does not name one model, and the model is what the chain is resolved by and
// what the call is priced at (C1) — so the call is refused rather than served
// under a model the service picked for it.
//
// It is a sentinel so the caller can tell it from a body that could not be read
// as JSON at all, which is a different refusal with a different fix.
var errModelMember = errors.New("the request body does not name one model")

// rewriteModel returns body with the value of its top-level `model` member
// replaced by model, and every other byte of the body unchanged. It is the one
// edit this service makes to a body, and it is made in both directions: a
// request's member becomes the source's spelling of the model it will be sent,
// and an answer's becomes the model the agent asked for.
//
// The body is spliced, not decoded and re-encoded. Everything else in it is the
// agent's own request or the provider's own answer — the order of the members,
// the spacing, the way a number is written, the fields this build has never
// heard of — and a decode-and-encode round trip would rewrite all of it. The
// request side is the one that matters: the harness computes over the request
// it wrote (its own cache keys and its own accounting), and a service that
// re-encoded the body would change bytes the agent never asked it to change.
//
// A body this service cannot read one model out of is errModelMember, and the
// caller refuses the call: rewriting a body that names no model would mean
// inventing the model the call is for, and no header is trusted for it (C1).
//
// **It reads one JSON object, and an answer stream is not one.** The Anthropic
// Messages answer in one piece carries `model` at the top level, which is what
// this rewrites; a streamed answer carries it inside `message_start`'s
// `message` member, event by event, so the body is not one JSON object and this
// function answers a read error for it rather than guess at the events. Which
// of the two the answer path holds, and what it does with a stream (its own
// line-by-line rewrite, or leaving the spelling where the agent's harness does
// not read it), is CS1's decision: the plan's own rule is that a limit found
// inside a streamed answer is not retried but marks the source (U7), so the
// answer path already treats a stream as its own shape.
func rewriteModel(body []byte, model string) ([]byte, error) {
	start, end, err := modelMember(body)
	if err != nil {
		return nil, err
	}
	quoted, err := json.Marshal(model)
	if err != nil {
		return nil, fmt.Errorf("spelling the model: %w", err)
	}
	out := make([]byte, 0, len(body)+len(quoted)-(end-start))
	out = append(out, body[:start]...)
	out = append(out, quoted...)
	return append(out, body[end:]...), nil
}

// modelMember answers the span of the value of body's top-level `model` member:
// the offset of its first byte and of the byte after its last. The span is a
// byte range and not a decoded value because the caller splices with it, which
// is what keeps the rest of the body byte for byte what it was.
//
// It is the top-level member and only that one: a `model` inside a tool
// definition, a metadata block or a message is part of the agent's own request
// and is left exactly as it is. Two top-level `model` members are refused
// rather than one of them chosen, because a body naming two models is a call
// the chain cannot be resolved for and the price cannot be settled at — and so
// is a body that is not one JSON object: a second object, or any text after the
// closing brace, is read as a read error rather than rewritten, because the
// source would be sent all of it while the call is priced by the model of the
// first object.
func modelMember(body []byte) (start, end int, err error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	tok, err := dec.Token()
	if err != nil {
		return 0, 0, fmt.Errorf("reading the body: %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return 0, 0, errModelMember
	}
	found := false
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return 0, 0, fmt.Errorf("reading the body: %w", err)
		}
		name, _ := key.(string)
		// The value begins after the member's colon. The decoder's offset is
		// the end of the key's token, so the colon and whatever spacing the
		// caller wrote around it are skipped here rather than guessed at.
		value := int(dec.InputOffset())
		for value < len(body) && isJSONSpace(body[value]) {
			value++
		}
		if value >= len(body) || body[value] != ':' {
			return 0, 0, fmt.Errorf("reading the body: the member %q has no value", name)
		}
		value++
		for value < len(body) && isJSONSpace(body[value]) {
			value++
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return 0, 0, fmt.Errorf("reading the body: %w", err)
		}
		if name != "model" {
			continue
		}
		if found {
			return 0, 0, errModelMember
		}
		// A model is a name and nothing else: a `model` member that is not a
		// string (null, a number, an object) names no model this build can
		// route, and splicing a string over it would turn a body nobody can
		// serve into one that looks served.
		if value >= len(body) || body[value] != '"' {
			return 0, 0, errModelMember
		}
		start, end, found = value, int(dec.InputOffset()), true
	}
	if !found {
		return 0, 0, errModelMember
	}
	// The object the members above are in, closed.
	if _, err := dec.Token(); err != nil {
		return 0, 0, fmt.Errorf("reading the body: %w", err)
	}
	// And nothing after it: the body is one JSON object, and the next token is
	// the end of the input. A second object or trailing text is a read error
	// and not a rewrite, since the whole body is what the source would be sent.
	trailing, err := dec.Token()
	if err == nil {
		return 0, 0, fmt.Errorf("reading the body: %v follows the object", trailing)
	}
	if !errors.Is(err, io.EOF) {
		return 0, 0, fmt.Errorf("reading the body: %w", err)
	}
	return start, end, nil
}

// isJSONSpace reports whether b is one of the four bytes JSON allows between
// tokens.
func isJSONSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}
