package gateway

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptrace"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/klauspost/compress/zstd"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// parsed is what the gateway reads from a Messages request body.
type parsed struct {
	model     string
	maxTokens int64
	stream    bool
	cacheTTL  string // "", "5m" or "1h": see pricing.Request.CacheTTL
	images    int64  // base64 image blocks
	pdf       bool   // a base64 document block
	violation string // the first shape the budget can't bound, if any
	toolTypes string // the distinct tool types of tools, comma separated, for the log
	foreign   string // the first top-level key a provider route doesn't take, if any
}

// routedKeys are the top-level fields a request to a provider route may
// carry: the Messages API's own. Anything else (OpenRouter's plugins,
// provider, models, route, transforms, usage, web_search_options, ...) can
// bill outside token pricing or override the account's data policy. The
// Claude path is not held to this list. metadata stays, but only its
// user_id: Claude Code sends metadata.user_id on every request and the
// gateway never rewrites a body.
var routedKeys = []string{
	"model", "max_tokens", "messages", "system", "stream", "tools", "tool_choice", "temperature",
	"top_p", "top_k", "stop_sequences", "metadata", "thinking", "output_config",
}

// maxJSONDepth bounds how deeply a request body may nest.
const maxJSONDepth = 512

// probeMaxTokens is the output bound of the minimal call the credentials
// service sends when it probes a backed-off credential (C1.12's
// probeMaxTokens, respelled here because the package exports no constant for
// it: the two must agree, and the gateway is the one that prices the probe).
//
// It is what a probe is priced at (C1.16): the smallest Messages call this
// gateway can send, on the run's own model. The probe's body is a few dozen
// bytes the service built, and never the run's body -- pricing the probe by
// the run's own request would charge the run's whole context for a question
// about a credential.
const probeMaxTokens = 1

// parseRequest reads a Messages body. An error is a malformed request
// (400, not a violation); a shape the budget can't price is returned as
// parsed.violation. On Vertex the model comes from the path.
func parseRequest(body []byte, pathModel string) (parsed, error) {
	var p parsed
	if err := checkKeys(body); err != nil {
		return p, err
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return p, errors.New("the request body isn't JSON")
	}
	top, ok := v.(map[string]any)
	if !ok {
		return p, errors.New("the request body isn't a JSON object")
	}

	p.model = pathModel
	if p.model == "" {
		s, ok := top["model"].(string)
		if !ok || s == "" {
			return p, errors.New("model is missing or not a string")
		}
		p.model = s
	}
	n, ok := top["max_tokens"].(json.Number)
	if !ok {
		return p, errors.New("max_tokens is missing or not a number")
	}
	mt, err := strconv.ParseInt(string(n), 10, 64)
	if err != nil || mt <= 0 {
		return p, fmt.Errorf("max_tokens %s isn't a positive integer", logValue(string(n)))
	}
	p.maxTokens = mt
	if sv, has := top["stream"]; has {
		b, ok := sv.(bool)
		if !ok {
			return p, errors.New("stream isn't true or false")
		}
		p.stream = b
	}
	msgs, ok := top["messages"].([]any)
	if !ok {
		return p, errors.New("messages is missing or not a list")
	}

	for k := range top {
		if !slices.Contains(routedKeys, k) && (p.foreign == "" || k < p.foreign) {
			p.foreign = k
		}
	}
	if md, has := top["metadata"]; has {
		// Claude Code sends metadata.user_id and nothing else; any other
		// key in it is a foreign field like a top-level one.
		obj, _ := md.(map[string]any)
		bad := ""
		if obj == nil {
			bad = "metadata"
		}
		for k := range obj {
			if k != "user_id" && (bad == "" || "metadata."+k < bad) {
				bad = "metadata." + k
			}
		}
		if bad != "" && (p.foreign == "" || bad < p.foreign) {
			p.foreign = bad
		}
	}
	p.cacheTTL = cacheTTL(v)
	p.violation = p.shapes(top, msgs)
	return p, nil
}

// shapes finds the first part of the request the budget can't bound.
func (p *parsed) shapes(top map[string]any, msgs []any) string {
	const unpriced = " is not allowed (it isn't priced by the budget)"
	if v, has := top["speed"]; has && v != "standard" {
		return "speed " + showValue(v) + unpriced
	}
	if v, has := top["inference_geo"]; has && v != "" {
		return "inference_geo " + showValue(v) + unpriced
	}
	if v, has := top["service_tier"]; has && v != "auto" && v != "standard_only" {
		return "service_tier " + showValue(v) + unpriced
	}
	if cm, has := top["context_management"]; has {
		if v := contextManagement(cm); v != "" {
			return v
		}
	}
	for _, k := range []string{"mcp_servers", "container", "fallbacks"} {
		if _, has := top[k]; has {
			return k + " is not allowed (the request doesn't bound its cost)"
		}
	}
	if tv, has := top["tools"]; has && tv != nil {
		tools, ok := tv.([]any)
		if !ok {
			return "tools is not a list"
		}
		var seen []string
		note := func(t string) {
			if !slices.Contains(seen, t) {
				seen = append(seen, t)
			}
			p.toolTypes = strings.Join(seen, ",")
		}
		first := "" // the violation to report: the first disallowed tool, all of them logged
		for _, t := range tools {
			m, ok := t.(map[string]any)
			if !ok {
				return "a tools entry is not an object"
			}
			typ, has := m["type"]
			if !has || typ == "custom" {
				note("custom")
				continue
			}
			s, ok := typ.(string)
			if !ok {
				if first == "" {
					first = "tool type " + showValue(typ) + " is not a string"
				}
				continue
			}
			note(logValue(s))
			if first == "" {
				first = "tool type " + logValue(s) + " is not allowed (the budget allows only client tools)"
			}
		}
		if first != "" {
			return first
		}
	}
	switch sys := top["system"].(type) {
	case nil, string:
	case []any:
		if v := p.blocks(sys); v != "" {
			return v
		}
	default:
		return "system is neither text nor a list of blocks"
	}
	for _, mv := range msgs {
		m, ok := mv.(map[string]any)
		if !ok {
			return "a message is not an object"
		}
		switch c := m["content"].(type) {
		case string:
		case []any:
			if v := p.blocks(c); v != "" {
				return v
			}
		default:
			return "a message's content is neither text nor a list of blocks"
		}
	}
	return ""
}

// contextManagementPrefixes are the context edits that only drop input
// (thinking blocks, old tool results), so they can't add a model pass.
// Compaction does add one, billed outside the usage the gateway reads.
var contextManagementPrefixes = []string{"clear_thinking_", "clear_tool_uses_"}

// contextManagement checks a request's context_management: nothing but a
// list of edits of the input-dropping types.
func contextManagement(v any) string {
	const refused = "context_management is not allowed (the request doesn't bound its cost)"
	if v == nil {
		return ""
	}
	m, ok := v.(map[string]any)
	if !ok {
		return refused
	}
	for k, ev := range m {
		if k != "edits" {
			return "context_management." + logValue(k) + " is not allowed (the request doesn't bound its cost)"
		}
		edits, ok := ev.([]any)
		if !ok {
			return refused
		}
		for _, e := range edits {
			em, ok := e.(map[string]any)
			if !ok {
				return refused
			}
			typ, _ := em["type"].(string)
			if !slices.ContainsFunc(contextManagementPrefixes, func(p string) bool { return strings.HasPrefix(typ, p) }) {
				return "context_management edit " + showType(em["type"]) + " is not allowed (the request doesn't bound its cost; only clear_thinking_* and clear_tool_uses_* are)"
			}
		}
	}
	return ""
}

// blocks checks content blocks by allow-list, counting base64 images
// and PDFs; a block whose size isn't in the body (a file or URL source)
// or whose type is unknown is refused.
func (p *parsed) blocks(list []any) string {
	for _, bv := range list {
		b, ok := bv.(map[string]any)
		if !ok {
			return "a content block is not an object"
		}
		typ, _ := b["type"].(string)
		switch typ {
		case "text", "tool_use", "thinking", "redacted_thinking":
		case "image":
			switch st := sourceType(b); st {
			case "base64":
				p.images++
			default:
				return "image source " + st + " is not allowed (its size isn't in the request)"
			}
		case "document":
			switch st := sourceType(b); st {
			case "base64":
				p.pdf = true
			case "text":
			case "content":
				src, _ := b["source"].(map[string]any)
				switch c := src["content"].(type) {
				case string:
				case []any:
					if v := p.blocks(c); v != "" {
						return v
					}
				default:
					return "a document's content is neither text nor a list of blocks"
				}
			default:
				return "document source " + st + " is not allowed (its size isn't in the request)"
			}
		case "tool_result", "search_result":
			switch c := b["content"].(type) {
			case nil, string:
			case []any:
				if v := p.blocks(c); v != "" {
					return v
				}
			default:
				return "a " + typ + "'s content is neither text nor a list of blocks"
			}
		default:
			return "content block type " + showType(b["type"]) + " is not allowed"
		}
	}
	return ""
}

func sourceType(block map[string]any) string {
	src, _ := block["source"].(map[string]any)
	if s, ok := src["type"].(string); ok && s != "" {
		return logValue(s)
	}
	return "(none)"
}

func showType(v any) string {
	if s, ok := v.(string); ok {
		return logValue(s)
	}
	return showValue(v)
}

// showValue renders a request value in a message, short.
func showValue(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "(unreadable)"
	}
	return logValue(string(b))
}

var ttlRank = map[string]int{"": 0, "5m": 1, "1h": 2}

// cacheTTL is the longest cache write anything in the request can
// cause: "" with no cache_control anywhere, "5m" for one without a ttl
// (the default) or with "5m", "1h" for "1h" or anything else. It looks at
// every cache_control in the body, the top-level automatic one included;
// a field of that name elsewhere (a tool's schema) only reserves more.
func cacheTTL(v any) string {
	best := ""
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, c := range x {
				if k == "cache_control" && c != nil {
					if t := ttlOf(c); ttlRank[t] > ttlRank[best] {
						best = t
					}
				}
				walk(c)
			}
		case []any:
			for _, c := range x {
				walk(c)
			}
		}
	}
	walk(v)
	return best
}

func ttlOf(cc any) string {
	m, ok := cc.(map[string]any)
	if !ok {
		return "1h"
	}
	switch t := m["ttl"]; t {
	case nil, "5m":
		return "5m"
	}
	return "1h"
}

// checkKeys refuses what two parsers could read differently: invalid
// JSON, trailing data, deep nesting, a key twice in one object, and at
// the top level two keys that differ only in case.
func checkKeys(body []byte) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := walkJSON(dec, 0); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("the request body has data after its JSON value")
	}
	return nil
}

func walkJSON(dec *json.Decoder, depth int) error {
	if depth > maxJSONDepth {
		return errors.New("the request body nests too deeply")
	}
	tok, err := dec.Token()
	if err != nil {
		return errors.New("the request body isn't JSON")
	}
	switch tok {
	case json.Delim('{'):
		seen := map[string]bool{}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return errors.New("the request body isn't JSON")
			}
			key, _ := kt.(string)
			k := key
			if depth == 0 {
				k = strings.ToLower(key)
			}
			if seen[k] {
				return fmt.Errorf("the request body has the key %s twice", strconv.Quote(logValue(key)))
			}
			seen[k] = true
			if err := walkJSON(dec, depth+1); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil {
			return errors.New("the request body isn't JSON")
		}
	case json.Delim('['):
		for dec.More() {
			if err := walkJSON(dec, depth+1); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil {
			return errors.New("the request body isn't JSON")
		}
	}
	return nil
}

// readBody reads a request body of at most MaxRequestBytes.
func readBody(r *http.Request) ([]byte, int, string) {
	b, err := io.ReadAll(io.LimitReader(r.Body, MaxRequestBytes+1))
	if err != nil {
		return nil, http.StatusBadRequest, "fugaro: the request body couldn't be read"
	}
	if len(b) > MaxRequestBytes {
		return nil, http.StatusRequestEntityTooLarge, fmt.Sprintf("fugaro: the request body is over %d bytes", MaxRequestBytes)
	}
	return b, 0, ""
}

// check applies the stage's pins and limits and the request's shape.
func (s *Server) check(st Stage, p parsed, m pricing.Model) string {
	return checkRequest(p, m, pinOf(st), s.o.Prices, s.o.Routes)
}

// pinOf is the gateway's own RequestPin: the stage's two pins, the stage's
// own max_tokens bound, and the stage's name as the refusal names it. Where is
// spelled here so that the refusal reads exactly as it did before the check
// was shared with the credentials service: "model X is not pinned for stage
// implement".
func pinOf(st Stage) RequestPin {
	return RequestPin{
		Models:          []string{st.Model, st.Background},
		Where:           "stage " + st.Name,
		Model:           st.Model,
		MaxOutputTokens: st.MaxOutputTokens,
	}
}

// modelFor is the price row a request's model is judged and priced by, and
// whether the table knew it: the table's own row, or -- for a model the table
// does not have -- a row carrying the table's maximum, which is the
// fail-closed direction and the one the run's reservation counts. A model the
// table lacks is priced at the maximum and not refused here, because a run may
// legitimately pin one it added under model_prices.
func modelFor(prices *pricing.Table, model string) (pricing.Model, bool) {
	t := pricesOr(prices)
	if m, ok := t.Lookup(model); ok {
		return m, true
	}
	return pricing.Model{ID: model, Rates: t.Max()}, false
}

// pricesOr is the table the request rules read: prices, or the embedded table
// for a caller that has none. The credentials service passes the project's own
// table, and the embedded one is what a table-less caller gets rather than a
// nil dereference: every rule below reads a model's own bounds from it.
func pricesOr(prices *pricing.Table) *pricing.Table {
	if prices == nil {
		return pricing.Embedded()
	}
	return prices
}

// checkRequest is the gateway's request check, over the pin its caller carries
// rather than a Stage: the model pin, the variant-suffix refusal, the shape
// refusal (a server tool among them), the foreign-field refusal and the
// max_tokens bounds. "" accepts the request; anything else is the refusal's
// sentence, without a status or a prefix, which each surface adds its own way
// (the gateway answers 400 with "fugaro: " in front of it).
//
// It is the one place these rules are written. The gateway's own path reaches
// it through Server.check (which builds the pin from the stage), and the
// credentials service through RequestRefusal (export.go, which builds it from
// the project's allow-list) -- so a rule changed for one is changed for both,
// and there is no second copy to drift.
func checkRequest(p parsed, m pricing.Model, pin RequestPin, prices *pricing.Table, routes []Route) string {
	prices = pricesOr(prices)
	if len(pin.Models) > 0 && !slices.ContainsFunc(pin.Models, func(x string) bool { return sameModel(p.model, x, prices, routes) }) {
		return fmt.Sprintf("model %s is not pinned for %s", logValue(p.model), pin.Where)
	}
	if routeFor(p.model, routes) != nil && hasVariantSuffix(p.model) {
		return fmt.Sprintf("model %s carries a variant suffix, which changes the provider's routing and price: pin the plain model ID", logValue(p.model))
	}
	if p.violation != "" {
		return p.violation
	}
	if p.foreign != "" && routeFor(p.model, routes) != nil {
		return fmt.Sprintf("field %s is not allowed on a provider route (it isn't priced by the budget)", strconv.Quote(logValue(p.foreign)))
	}
	maxOut := m.MaxOutputTokens
	if maxOut <= 0 {
		maxOut = pricing.DefaultMaxOutputTokens
	}
	if p.maxTokens > maxOut {
		return fmt.Sprintf("max_tokens %d for model %s is above the model's maximum %d", p.maxTokens, logValue(p.model), maxOut)
	}
	if pin.MaxOutputTokens > 0 && sameModel(p.model, pin.Model, prices, routes) && p.maxTokens > pin.MaxOutputTokens {
		return fmt.Sprintf("max_tokens %d for model %s is above the stage's limit %d", p.maxTokens, logValue(p.model), pin.MaxOutputTokens)
	}
	// The missing field is named, not the model: an override-priced model is
	// priced — its input, output and cache rates are the owner's — and what
	// it lacks is an image rate. Saying "images aren't priced for model X"
	// sent an owner whose model_prices row is right there looking for a typo
	// in it (run 20261021-002046-31cc). p.images is the request's own count,
	// so a text-only call on the same model is never refused.
	if p.images > 0 && m.ImageTokens <= 0 {
		return fmt.Sprintf("model %s is priced without an image rate (the price table's image_tokens, a per-image token ceiling): an image block can't be reserved without one. model_prices sets token rates only", logValue(p.model))
	}
	return ""
}

// sameModel: the same ID, or two spellings the table resolves to one
// model. A prefix never matches, and a provider's model matches only
// itself: the alias rule is Claude's.
func sameModel(a, pin string, prices *pricing.Table, routes []Route) bool {
	if a == "" || pin == "" {
		return false
	}
	if a == pin {
		return true
	}
	if routeFor(a, routes) != nil || routeFor(pin, routes) != nil {
		return false
	}
	ma, okA := prices.Lookup(a)
	mp, okP := prices.Lookup(pin)
	return okA && okP && ma.ID == mp.ID
}

// handleMessages is one model call: parse, check, reserve, forward,
// settle, log.
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request, rt route) {
	cl := callLog{
		model: rt.pathModel, pricedAs: pricedTable, settled: settledZero,
		sessionID: r.Header.Get("x-claude-code-session-id"), agentID: r.Header.Get("x-claude-code-agent-id"),
	}
	refuse := func(status int, typ, msg string) {
		writeError(w, status, typ, msg)
		cl.status = status
		if cl.stage == "" {
			cl.stage = s.stageName()
		}
		s.logCall(cl)
	}
	body, status, msg := readBody(r)
	if status != 0 {
		refuse(status, errorTypeFor(status), msg)
		return
	}
	p, err := parseRequest(body, rt.pathModel)
	cl.model, cl.stream, cl.maxTokens, cl.toolTypes = p.model, p.stream, p.maxTokens, p.toolTypes
	if err != nil {
		refuse(http.StatusBadRequest, "invalid_request_error", "fugaro: "+err.Error())
		return
	}
	st := s.enterStage()
	if st == nil {
		refuse(http.StatusBadRequest, "invalid_request_error", "fugaro: no stage is running, so no model is allowed")
		return
	}
	defer st.calls.Done()
	cl.stage = st.st.Name

	m, known := modelFor(s.o.Prices, p.model)
	if !known { // a pinned model the table lacks: priced at the table's maximum
		cl.pricedAs = pricedMax
	}
	if v := s.check(st.st, p, m); v != "" {
		s.violation(st, v)
		s.log.Warn("budget: request refused", "stage", st.st.Name, "violation", v)
		refuse(http.StatusBadRequest, "invalid_request_error", "fugaro: "+v)
		return
	}
	rt = s.withRoute(rt, p.model)
	// A model no route claims goes to the Anthropic upstream, which needs a
	// key. There may be none: the run is only required to have one when a
	// call actually needs it (§6.1).
	if rt.provider == nil && s.o.Upstream.Kind == "anthropic" && s.o.Upstream.APIKey == "" {
		s.violation(st, missingAnthropicKey)
		s.log.Warn("budget: request refused", "stage", st.st.Name, "violation", missingAnthropicKey)
		refuse(http.StatusBadRequest, "invalid_request_error", "fugaro: "+missingAnthropicKey)
		return
	}
	if rt.provider != nil {
		cl.route = rt.provider.Name
		if !known {
			// A provider's call is real money: with no price in the table
			// it would be charged a guess, so it is not sent (CheckPins
			// refuses such a pin at bootstrap; this is the backstop). A
			// model the catalog refused carries the catalog's own message,
			// which names the switch and both ways out (P8); the generic
			// one is for a model nothing prices at all.
			v := fmt.Sprintf("model %s is routed to %s but has no price: set model_prices", logValue(p.model), rt.provider.Name)
			if why := s.o.Prices.Refusal(p.model); why != "" {
				v = why
			}
			s.violation(st, v)
			s.log.Warn("budget: request refused", "stage", st.st.Name, "violation", v)
			refuse(http.StatusBadRequest, "invalid_request_error", "fugaro: "+v)
			return
		}
	}
	// The route's fee is part of what the call can cost, so the cap counts it.
	w0 := pricing.WithFee(m.WorstCase(pricing.Request{
		BodyBytes: int64(len(body)), HasPDF: p.pdf, ImageCount: p.images, MaxTokens: p.maxTokens, CacheTTL: p.cacheTTL,
	}), rt.provider.feePct())
	if msg, status := s.reserve(r.Context(), st, w0); status == http.StatusTooManyRequests {
		// Retryable: Claude Code backs off and asks again once the calls
		// holding the reservations have settled.
		w.Header().Set("retry-after", "2")
		refuse(status, "rate_limit_error", msg)
		return
	} else if status == http.StatusServiceUnavailable {
		// The budget backend is unreachable (or the lease is short): not a
		// halt; the session's grace decides, the call is retried.
		w.Header().Set("retry-after", "5")
		refuse(status, "api_error", msg)
		return
	} else if status != 0 {
		refuse(status, "permission_error", msg)
		return
	}
	cl.reserved = w0

	// Whatever happens below, the call settles exactly once. Until an
	// outcome is known it is charged its full reservation.
	out := outcome{charge: charge{amount: w0, unreconciled: w0, model: p.model, sent: true}, settled: settledReserved, pricedAs: cl.pricedAs}
	// The tiers the service probed while answering this call (C1.12), empty
	// until its answer says which they were.
	var probed []string
	defer func() {
		out.route = cl.route
		// A probe is a call the run pays for (C1.16), and it settles
		// before the call that owed it: what the run had cached is the
		// answering call's own usage (F24), this gateway never reads a
		// probe's usage, and a probe settled after its call would be the
		// last word on the cache instead of the call.
		for _, t := range probed {
			s.settleProbe(st, t, p.model, m.WorstCase(pricing.Request{MaxTokens: probeMaxTokens}))
		}
		s.settle(st, w0, out.charge)
		cl.charged, cl.settled, cl.pricedAs, cl.servingModel, cl.usage, cl.errorType, cl.reported =
			out.amount, out.settled, out.pricedAs, out.servingModel, out.usage, out.errorType, out.reported
		cl.serviceTier = out.charge.serviceTier
		s.logCall(cl)
	}()
	// The tier the service's answer carried, whatever outcome the call
	// settles as: the ledger charges by it (C1.16), and a call the service
	// served and this gateway could not price is still a call the run
	// pays for at the tier it was served from.
	var served string
	out = s.forward(w, r, rt, body, p, known, w0, &cl.status, &served, &probed)
	out.charge.tier, out.charge.agentID = served, cl.agentID
}

// outcome is how a forwarded call settles, and what its log line says.
type outcome struct {
	charge
	settled, pricedAs, servingModel, errorType string
	usage                                      pricing.Usage // reported
}

func zeroOutcome(model string, sent bool) outcome {
	return outcome{charge: charge{model: model, sent: sent}, settled: settledZero, pricedAs: pricedTable}
}

// forward sends the call upstream and copies the response back without
// buffering, teeing it to the usage parser, and says how it settles.
func (s *Server) forward(w http.ResponseWriter, r *http.Request, rt route, body []byte, p parsed, known bool, w0 pricing.Micros, status *int, tier *string, probed *[]string) outcome {
	reserved := outcome{charge: charge{amount: w0, unreconciled: w0, model: p.model, sent: true}, settled: settledReserved, pricedAs: pricedTable}
	if !known {
		reserved.pricedAs = pricedMax
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(s.base, cancel) // Close cancels every call
	defer stop()
	stopHalt := context.AfterFunc(s.hctx, cancel) // so does an external halt
	defer stopHalt()

	up, sent, err := s.upstreamRequest(ctx, r, rt, body)
	if err != nil {
		if s.o.Upstream.Kind == KindService && errors.Is(err, errServiceUnidentified) {
			// C1.15 (service.go), D143: a service call this job cannot
			// present its own identity on is a run the service cannot serve
			// at all. The metadata server is the only place the job's ID
			// token comes from and the run's own token is the only identity
			// the service verifies it by, so this is a halt -- like an
			// unreachable service -- rather than one 502 per call until the
			// stage's deadline. Nothing was sent: nothing to charge.
			s.haltService(w, stateDetailUnidentified)
			return zeroOutcome(p.model, false)
		}
		writeError(w, http.StatusBadGateway, "api_error", "fugaro: "+err.Error())
		return zeroOutcome(p.model, false)
	}
	resp, err := s.client.Do(up)
	if err != nil {
		if s.o.Upstream.Kind == KindService && s.serviceUnreachable(w, s.base, r.Context()) {
			// C1.15 (service.go), D143: an unreachable credentials service
			// halts the run -- it never falls back to a credential of the
			// job's own, which this upstream has none of. Nothing reached
			// the upstream: nothing to charge. Anything did: the service may
			// have been generating, so the whole reservation.
			if !sent.Load() {
				return zeroOutcome(p.model, false)
			}
			return reserved
		}
		// No status. Nothing reached the upstream: nothing to charge.
		// Anything did: the upstream may be generating, so the whole
		// reservation.
		if r.Context().Err() == nil && s.base.Err() == nil {
			if msg, halted := s.haltedMessage(); halted && s.hctx.Err() != nil {
				writeHalt(w, msg) // cut short by an external halt
			} else {
				writeError(w, http.StatusBadGateway, "api_error", "fugaro: the upstream request failed")
			}
		}
		if !sent.Load() {
			return zeroOutcome(p.model, false)
		}
		return reserved
	}
	defer resp.Body.Close()
	if rt.provider != nil && isRedirect(resp.StatusCode) {
		// Never handed to the agent (its Location is another host's) and
		// never followed: the provider answered with nothing usable.
		*status = http.StatusBadGateway
		writeError(w, http.StatusBadGateway, "api_error", "fugaro: the provider answered with a redirect, which the gateway does not follow")
		return zeroOutcome(p.model, true)
	}
	*status = resp.StatusCode
	if s.o.Upstream.Kind == KindService {
		// C1.15 (service.go): what the service said about this call is read
		// here, where it leaves the upstream and before a byte of it reaches
		// the agent -- the tier the call settles by, the credential's name,
		// the switch, and the refusals that are the service's own answer and
		// are never relayed. A model answer (2xx, or a model error the
		// service relayed) is not touched: it goes on to the pump below.
		st, done, served, probes := s.serviceAnswer(w, resp, true)
		*tier = served // "" when the answer carried none, which is a halt or a relayed error
		*probed = probes
		if done {
			*status = st
			// A 2xx this gateway could not account by is a model answer the
			// service served and the run must still pay for: it settles at
			// its reservation, exactly as an unparsed 2xx does, rather than
			// handing the run a whole answer for nothing. Every other answer
			// `done` covers is a refusal -- the service's own, or a job its
			// front door would not admit -- and no model generated anything
			// for it.
			if resp.StatusCode/100 == 2 {
				reserved.unparsed = true
				s.logUsageUnparsed(p.model, rt.provider, "no_tier", nil)
				return reserved
			}
			return zeroOutcome(p.model, true)
		}
	}

	src, closeSrc, decoded, readable := decodeBody(resp)
	defer closeSrc()
	if rt.provider != nil && !(resp.StatusCode >= 200 && resp.StatusCode <= 299) {
		// A provider's error is never copied: it may echo the key.
		var eb []byte
		if readable {
			eb, _ = io.ReadAll(io.LimitReader(src, 64<<10))
		}
		o := zeroOutcome(p.model, true)
		o.errorType = providerError(w, resp, eb)
		return o
	}
	var out io.Writer = w
	var rw *redactWriter
	if rt.provider != nil {
		copyProviderResponseHeaders(w.Header(), resp.Header, decoded)
	} else {
		copyResponseHeaders(w.Header(), resp.Header, decoded)
	}
	if rt.provider != nil || s.o.Upstream.Kind == KindOAuth || s.o.Upstream.Kind == KindService {
		// Whatever else comes back is scrubbed of the credential that was
		// sent: a provider's key, the tier-A subscription token, or the
		// service call's job ID token (which an error body may echo). The
		// credential is the one in the request, read at request time.
		key := keyFrom(up.Header)
		redactHeaders(w.Header(), key)
		if s.o.Upstream.Kind == KindService {
			// The run's own identity travels on the service call as well
			// (HeaderRunIdentity), and it is a Firebase token for this run:
			// it is dropped from an answer that echoes it -- headers and
			// body both -- exactly as the bearer credential is. The model
			// never receives the header, so the only body that can carry it
			// is one the service itself wrote.
			run := up.Header.Get(HeaderRunIdentity)
			redactHeaders(w.Header(), run)
			rw = newRedactWriter(w, key, run)
		} else {
			rw = newRedactWriter(w, key)
		}
		out = rw
	}
	w.WriteHeader(resp.StatusCode)
	rc := http.NewResponseController(w)
	_ = rc.Flush()

	tee := newUsageTee(isSSE(resp.Header, p.stream))
	tee.provider = rt.provider != nil
	eof := pump(out, rc, src, func(b []byte) {
		if readable {
			tee.feed(b)
		}
	})
	cancel()
	if rw != nil {
		_ = rw.Close()
	}
	tee.finish()

	ok2xx := resp.StatusCode >= 200 && resp.StatusCode <= 299
	switch {
	case !ok2xx:
		o := zeroOutcome(p.model, true)
		o.errorType = tee.errorType
		return o
	case !readable:
		reserved.unparsed = true
		s.logUsageUnparsed(p.model, rt.provider, "not_readable", nil)
		return reserved
	case tee.sse && tee.complete && !tee.broken && !tee.acc.negative():
		return s.fromUsage(tee, p, rt.provider, false)
	case tee.sse && tee.started && !tee.broken && !tee.acc.negative():
		return s.fromUsage(tee, p, rt.provider, true)
	case !tee.sse && eof && tee.complete:
		return s.fromUsage(tee, p, rt.provider, false)
	}
	reserved.unparsed = true
	reserved.errorType = tee.errorType
	s.logUsageUnparsed(p.model, rt.provider, tee.unparsedReason(), tee.events)
	return reserved
}

// logUsageUnparsed warns once a 2xx call settles at its reservation
// instead of its usage: a closed-set reason and the event type names the
// gateway saw (never a body, never the provider's text), so the next
// failure explains itself in `fugaro diagnose`.
func (s *Server) logUsageUnparsed(model string, pr *Route, reason string, events []string) {
	route := ""
	if pr != nil {
		route = pr.Name
	}
	s.log.Warn("budget: usage unparsed", "stage", s.stageName(), "model", logValue(model),
		"route", route, "reason", reason, "events", strings.Join(events, ","))
}

// fromUsage prices a call from the usage the response reported: at the
// serving model's rates, the table's maximum for an unknown model, or a
// multiple of those rates when the response reported a pricing dimension the
// table has no price for -- the dearest multiple it knows for a service tier
// no provider publishes a multiplier for, and that multiple on the table's
// maximum rates for a speed or inference geography the request didn't
// allow. On a provider route (pr, else nil) a
// serving model other than the pinned one is never priced lower than the
// pin: the dearer of the two rates per dimension, or the table's maximum
// when the table doesn't know it; and the route's fee is added to the
// charge. A partial call is charged its reported input and cache tokens
// plus the reserved output.
func (s *Server) fromUsage(t *usageTee, p parsed, pr *Route, partial bool) outcome {
	serving := t.model
	if serving == "" {
		serving = p.model
	}
	o := outcome{servingModel: serving, pricedAs: pricedTable, errorType: t.errorType}
	rates := s.o.Prices.Max()
	mult := int64(1)
	if m, ok := s.o.Prices.Lookup(serving); ok {
		rates = m.Rates
		if pin, pinned := s.o.Prices.Lookup(p.model); pr != nil && pinned && pin.ID != m.ID {
			rates = pricing.MaxOf(pin.Rates, m.Rates)
			o.pricedAs = pricedMax
		}
	} else if pin, pinned := s.o.Prices.Lookup(p.model); pr != nil && pinned && pinSpelling(serving, p.model) {
		// The pin with a date or other suffix: the pin's own model.
		rates = pin.Rates
	} else {
		o.pricedAs = pricedMax
		if pr != nil {
			s.log.Warn("budget: serving model not in the price table, priced at the table maximum",
				"serving_model", logValue(serving), "pinned_model", logValue(p.model))
		}
	}
	fee := pr.feePct()
	// A dimension the table has no price for is charged a multiple of the
	// rates the call is priced at -- the serving model's own, or the table's
	// maximum for a model the table doesn't know -- never at the standard
	// price, never free.
	//
	// A service tier is the tier the provider itself served the call from:
	// the agent's request said nothing about it, and a provider can answer
	// with one at any time. It is charged the dearest multiple the table
	// knows, and recorded on the stage, in the call log and in a warning,
	// and the stage goes on: failing the stage is what turns one provider's
	// field into a dead run. Pricing it at the table's maximum rates instead
	// of its own would charge a cheap model a dearer model's money -- a
	// DeepSeek call at Claude's rates -- which inflates the record and the
	// shared day counters and, on a tight cap, halts a run that the model's
	// own rates would not have.
	//
	// A speed or an inference geography the request chose stays a violation,
	// and is the dearer case of the two: the table's maximum rates times
	// SurpriseMultiplier.
	if dims := t.acc.unpricedDims(); len(dims) > 0 {
		surprise := false
		for _, d := range dims {
			if !d.tier {
				rates, surprise = s.o.Prices.Max(), true
				mult = max(mult, int64(pricing.SurpriseMultiplier))
				s.log.Warn("budget: surprise pricing", "model", logValue(serving), "what", d.String())
				if o.violation == "" {
					o.violation = fmt.Sprintf("the response for model %s reported %s, which the budget doesn't price", logValue(serving), d)
				}
				continue
			}
			mult = max(mult, int64(pricing.ConservativeTierMultiplier))
			o.charge.serviceTier = d.value
			s.log.Warn("budget: service tier not priced", "model", logValue(serving), "service_tier", d.value,
				"charged_as", fmt.Sprintf("%dx the rates the call is priced at", mult))
		}
		// The tier is what the charge turned on only when the rates were the
		// model's own: a fallback the route served (`max`) is the more
		// useful fact to leave in the line, and it is the dearer charge of
		// the two anyway.
		if surprise {
			o.pricedAs = pricedSurprise
		} else if o.pricedAs == pricedTable {
			o.pricedAs = pricedTier
		}
	}
	u := t.acc.usage(rates, p.cacheTTL)
	reported := pricing.WithFee(satMul(rates.Cost(u), mult), fee)
	o.usage = u
	o.model = serving
	o.sent = true
	o.tokens = u.Input + u.CacheWrite5m + u.CacheWrite1h + u.CacheRead + u.Output
	// What this call had cached is what a switch after it re-reads (F24), and
	// it is known because this call's usage was read.
	o.cacheRead, o.cacheWrite, o.usageRead = u.CacheRead, u.CacheWrite5m+u.CacheWrite1h, true
	if !partial {
		o.amount, o.settled = reported, settledUsage
		if pr != nil && t.acc.hasReported {
			o.charge.reported = t.acc.reported // a record for comparison: it never prices the call
		}
		return o
	}
	charged := u
	charged.Output = max(u.Output, p.maxTokens)
	o.amount = pricing.WithFee(satMul(rates.Cost(charged), mult), fee)
	o.unreconciled = max(o.amount-reported, 0)
	o.settled = settledPartial
	return o
}

// pinSuffix is what may follow the pin in the serving model's name for it
// to be the pin's own snapshot: a date or a short revision number.
var pinSuffix = regexp.MustCompile(`^[-@:]?(\d{8}|\d{4}-\d{2}-\d{2}|v?\d{1,3}(\.\d+)*)$`)

// pinSpelling: the serving model is the pin plus a date or revision suffix,
// so it is the pin's own model. Any other name that merely shares a prefix
// (a variant such as -ultra-pro, a shorter name, the pin as the longer one)
// is a different model.
func pinSpelling(serving, pin string) bool {
	rest, ok := strings.CutPrefix(serving, pin)
	return ok && pinSuffix.MatchString(rest)
}

// countingBody marks sent once the transport reads any byte of it.
type countingBody struct {
	r    io.Reader
	sent *atomic.Bool
}

func (c *countingBody) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.sent.Store(true)
	}
	return n, err
}

// upstreamRequest builds the upstream call: the body unchanged, the
// client's headers but its credentials and encoding wishes, the real
// credential. sent turns true once any of it may have left: the headers
// were written or a byte of the body was read.
func (s *Server) upstreamRequest(ctx context.Context, r *http.Request, rt route, body []byte) (*http.Request, *atomic.Bool, error) {
	sent := &atomic.Bool{}
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{WroteHeaders: func() { sent.Store(true) }})
	up, err := http.NewRequestWithContext(ctx, http.MethodPost, rt.upstream, &countingBody{r: bytes.NewReader(body), sent: sent})
	if err != nil {
		return nil, nil, errors.New("the upstream URL is invalid")
	}
	up.ContentLength = int64(len(body))
	up.GetBody = nil // never replayed: the gateway never retries
	if rt.provider != nil {
		// Only what the provider needs: no cookies, no session or agent
		// IDs, no SDK telemetry.
		up.Header = providerHeaders(r.Header)
		s.scrubToken(up.Header)
		if _, err := rt.provider.attachCredential(up.Header); err != nil {
			return nil, nil, err
		}
		return up, sent, nil
	}
	if s.o.Upstream.Kind == KindOAuth {
		// Tier A (oauth.go): Check 90's header allow-list and the
		// subscription credential, and nothing else. The invariant -- an
		// OAuth token is only ever sent to Anthropic's own endpoint -- is
		// checked inside oauthUpstream, in the one place the token is
		// attached.
		h, err := s.oauthUpstream(rt, r.Header)
		if err != nil {
			return nil, nil, err
		}
		up.Header = h
		return up, sent, nil
	}
	if s.o.Upstream.Kind == KindService {
		// C1.15 (service.go): the job's Google ID token and the run's own,
		// and no credential of the job's -- which the upstream has none of
		// (validate). Both are attached inside serviceUpstream, in the one
		// place they are written.
		h, err := s.serviceUpstream(ctx, rt, r.Header)
		if err != nil {
			return nil, nil, err
		}
		up.Header = h
		return up, sent, nil
	}
	up.Header = forwardHeaders(r.Header)
	s.scrubToken(up.Header)
	switch s.o.Upstream.Kind {
	case "anthropic":
		if s.o.Upstream.APIKey == "" {
			// Defense in depth: the callers refuse a keyless Anthropic call
			// with the nicer, actionable message before they get here, but
			// this request builder must never send an empty credential --
			// x-api-key: "" is an unauthenticated call to Anthropic under
			// this run's own headers.
			return nil, nil, errors.New(config.MissingAnthropicKey)
		}
		up.Header.Set("x-api-key", s.o.Upstream.APIKey)
	case "vertex":
		tok, err := s.o.Upstream.Token.Token()
		if err != nil || tok == nil || tok.AccessToken == "" {
			return nil, nil, errors.New("the Vertex access token isn't available")
		}
		tok.SetAuthHeader(up)
	}
	return up, sent, nil
}

// Headers never forwarded upstream: the client's credentials (the real
// one is set instead), hop-by-hop headers, the encoding the client
// accepts (the upstream is asked for identity), and the Google headers
// that could bill or authorize another project with the run's token.
var dropRequestHeaders = []string{
	"X-Api-Key", "Authorization", "Proxy-Authorization", "X-Goog-Api-Key", "X-Goog-User-Project",
	"Accept-Encoding", "Content-Length", "Host", "Expect",
	"Connection", "Keep-Alive", "Proxy-Connection", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

func forwardHeaders(in http.Header) http.Header {
	out := in.Clone()
	for _, k := range in.Values("Connection") {
		for _, f := range strings.Split(k, ",") {
			out.Del(strings.TrimSpace(f))
		}
	}
	for _, k := range dropRequestHeaders {
		out.Del(k)
	}
	out.Set("Accept-Encoding", "identity")
	return out
}

var hopByHop = map[string]bool{
	"Connection": true, "Keep-Alive": true, "Proxy-Connection": true, "Proxy-Authenticate": true,
	"Te": true, "Trailer": true, "Transfer-Encoding": true, "Upgrade": true,
}

// dropResponseHeaders never reach the agent: they identify the
// organization behind the real key, or set state on the gateway's origin.
//
// The credentials service's answer headers are three more of them (C1.15,
// service.go), and they are dropped for every upstream kind rather than only
// under a service one: the tier is what C1.16 charges the call by, the name
// is which of the project's credentials answered, and the switch is which
// credential a call moved between. The agent is the one party that must never
// learn the set of credentials its project holds (D143), and every later
// upstream kind that answers with them would have to remember this list.
var dropResponseHeaders = map[string]bool{
	"Anthropic-Organization-Id": true, "Set-Cookie": true, "Set-Cookie2": true,
	HeaderTier: true, HeaderCredential: true, HeaderSwitch: true, HeaderProbe: true,
}

func copyResponseHeaders(dst, src http.Header, decoded bool) {
	for k, vs := range src {
		ck := http.CanonicalHeaderKey(k)
		if hopByHop[ck] || dropResponseHeaders[ck] || ck == "Content-Length" || (decoded && ck == "Content-Encoding") {
			continue
		}
		dst[ck] = append([]string(nil), vs...)
	}
}

// providerSuccessHeaders are the headers of a provider's 2xx the agent may
// see (with the prefixes below): what it needs to read the body and back off.
// Anything else (cookies, account or generation identifiers, unknown
// headers) is the provider's own and stays at the gateway.
var providerSuccessHeaders = map[string]bool{
	"Content-Type": true, "Retry-After": true, "Request-Id": true, "X-Request-Id": true, "Anthropic-Request-Id": true,
	"Cache-Control": true,
}

var providerSuccessPrefixes = []string{"X-Ratelimit-", "Ratelimit-", "Anthropic-Ratelimit-"}

// copyProviderResponseHeaders is copyResponseHeaders for a routed 2xx: an
// allowlist. Content-Encoding goes only when the body was not decoded.
func copyProviderResponseHeaders(dst, src http.Header, decoded bool) {
	for k, vs := range src {
		ck := http.CanonicalHeaderKey(k)
		ok := providerSuccessHeaders[ck] || (ck == "Content-Encoding" && !decoded)
		for _, p := range providerSuccessPrefixes {
			ok = ok || strings.HasPrefix(ck, p)
		}
		if ok {
			dst[ck] = append([]string(nil), vs...)
		}
	}
}

// decodeBody undoes gzip, deflate and zstd, which the gateway asked not
// to get; readable is false for any other encoding, whose usage the
// gateway can't read (its bytes pass through as they are).
func decodeBody(resp *http.Response) (src io.Reader, closeFn func(), decoded, readable bool) {
	encs := resp.Header.Values("Content-Encoding")
	enc := ""
	if len(encs) == 1 {
		enc = strings.ToLower(strings.TrimSpace(encs[0]))
	} else if len(encs) > 1 {
		enc = "multiple"
	}
	nop := func() {}
	switch enc {
	case "", "identity":
		return resp.Body, nop, false, true
	case "gzip", "x-gzip":
		l := &lazyReader{open: func() (io.Reader, func(), error) {
			z, err := gzip.NewReader(resp.Body)
			if err != nil {
				return nil, nil, err
			}
			return z, func() { _ = z.Close() }, nil
		}}
		return l, l.close, true, true
	case "deflate":
		l := &lazyReader{open: func() (io.Reader, func(), error) {
			z, err := zlib.NewReader(resp.Body)
			if err != nil {
				return nil, nil, err
			}
			return z, func() { _ = z.Close() }, nil
		}}
		return l, l.close, true, true
	case "zstd":
		l := &lazyReader{open: func() (io.Reader, func(), error) {
			z, err := zstd.NewReader(resp.Body, zstd.WithDecoderConcurrency(1))
			if err != nil {
				return nil, nil, err
			}
			return z, z.Close, nil
		}}
		return l, l.close, true, true
	}
	return resp.Body, nop, false, false
}

// lazyReader opens its decoder on the first Read, so a decoder that
// reads a header waits for the body like any other read.
type lazyReader struct {
	open func() (io.Reader, func(), error)
	r    io.Reader
	done func()
	err  error
}

func (l *lazyReader) Read(p []byte) (int, error) {
	if l.r == nil && l.err == nil {
		l.r, l.done, l.err = l.open()
	}
	if l.err != nil {
		return 0, l.err
	}
	return l.r.Read(p)
}

func (l *lazyReader) close() {
	if l.done != nil {
		l.done()
	}
}

func isSSE(h http.Header, requested bool) bool {
	mt, _, err := mime.ParseMediaType(h.Get("Content-Type"))
	switch {
	case err != nil:
		return requested
	case mt == "text/event-stream":
		return true
	case mt == "application/json":
		return false
	}
	return requested
}

// pump copies src to the client, flushing after every write, and hands
// each chunk to tee after it was written. It stops when src ends (eof)
// or fails, or the client goes away.
func pump(w io.Writer, rc *http.ResponseController, src io.Reader, tee func([]byte)) (eof bool) {
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			_, werr := w.Write(buf[:n])
			if werr == nil {
				werr = rc.Flush()
			}
			tee(buf[:n])
			if werr != nil {
				return false
			}
		}
		if errors.Is(err, io.EOF) {
			return true
		}
		if err != nil {
			return false
		}
	}
}

func isRedirect(status int) bool { return status >= 300 && status <= 399 }

// maxCountsInFlight bounds the token counts forwarded at once: they are
// free, but each spends the organization's rate limit with the real key.
const maxCountsInFlight = 4

// forwardFree forwards a token count: free, no reservation, and a log line
// with only its status and sizes.
func (s *Server) forwardFree(w http.ResponseWriter, r *http.Request, rt route) {
	select {
	case s.countSlots <- struct{}{}:
		defer func() { <-s.countSlots }()
	default:
		writeError(w, http.StatusTooManyRequests, "rate_limit_error", "fugaro: too many token counts in flight; retry shortly")
		s.log.Info("token count refused", "stage", s.stageName(), "status", http.StatusTooManyRequests)
		return
	}
	body, status, msg := readBody(r)
	if status != 0 {
		writeError(w, status, errorTypeFor(status), msg)
		return
	}
	m, err := s.countRoutedModel(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "fugaro: "+err.Error())
		s.log.Info("token count", "stage", s.stageName(), "status", http.StatusBadRequest, "request_bytes", len(body), "response_bytes", 0)
		return
	}
	if m != "" {
		// The only upstream here is Anthropic's, and a provider's model's
		// conversation is not sent there; counting is not proven on the
		// provider, so the agent estimates by itself.
		writeError(w, http.StatusNotFound, "not_found_error", "fugaro: token counting isn't available for model "+logValue(m))
		s.log.Info("token count", "stage", s.stageName(), "status", http.StatusNotFound, "request_bytes", len(body), "response_bytes", 0)
		return
	}
	if s.o.Upstream.Kind == "anthropic" && s.o.Upstream.APIKey == "" {
		// A count for a model no route claims goes to Anthropic, which needs
		// the key the run may not have (§6.1). The same refusal as a
		// messages call's -- 400 invalid_request_error, recorded as a
		// violation -- so the two paths of one missing key read alike.
		s.violationNow(missingAnthropicKey)
		writeError(w, http.StatusBadRequest, "invalid_request_error", "fugaro: "+missingAnthropicKey)
		s.log.Warn("budget: request refused", "stage", s.stageName(), "violation", missingAnthropicKey)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(s.base, cancel)
	defer stop()
	stopHalt := context.AfterFunc(s.hctx, cancel)
	defer stopHalt()
	up, _, err := s.upstreamRequest(ctx, r, rt, body)
	if err != nil {
		if s.o.Upstream.Kind == KindService && errors.Is(err, errServiceUnidentified) {
			// C1.15 (service.go), D143: as a Messages call's, a count this
			// job cannot present its identity on halts the run rather than
			// being answered 502 per call. Nothing was sent.
			s.haltService(w, stateDetailUnidentified)
			return
		}
		writeError(w, http.StatusBadGateway, "api_error", "fugaro: "+err.Error())
		return
	}
	resp, err := s.client.Do(up)
	if err != nil {
		if s.o.Upstream.Kind == KindService && s.serviceUnreachable(w, s.base, r.Context()) {
			// C1.15 (service.go), D143: as a Messages call's, an unreachable
			// service halts the run rather than being retried or fallen back
			// from. A count holds no reservation, so there is nothing to
			// settle either way.
			return
		}
		if r.Context().Err() == nil {
			writeError(w, http.StatusBadGateway, "api_error", "fugaro: the upstream request failed")
		}
		return
	}
	defer resp.Body.Close()
	if s.o.Upstream.Kind == KindService {
		// C1.15 (service.go): a count is free and settles nothing, so its
		// answer is not asked for a tier -- but a refusal is the service's
		// own answer here too, and is acted on rather than copied to the
		// agent (D143). A model answer passed through.
		if _, done, _, _ := s.serviceAnswer(w, resp, false); done {
			return
		}
	}
	src, closeSrc, decoded, _ := decodeBody(resp)
	defer closeSrc()
	copyResponseHeaders(w.Header(), resp.Header, decoded)
	var out io.Writer = w
	var rw *redactWriter
	if s.o.Upstream.Kind == KindOAuth || s.o.Upstream.Kind == KindService {
		// Tier A (oauth.go), and a service call (C1.15): a token count goes
		// upstream with a credential too, and this path copies the answer
		// through unread -- so an answer that echoes the credential is
		// scrubbed here exactly as a messages call's is. Without this a
		// count_tokens call would hand the token to the agent.
		key := keyFrom(up.Header)
		redactHeaders(w.Header(), key)
		if s.o.Upstream.Kind == KindService {
			// The run's identity is on this call too (HeaderRunIdentity),
			// and is dropped from an answer that echoes it -- headers and
			// body both, as in forward.
			run := up.Header.Get(HeaderRunIdentity)
			redactHeaders(w.Header(), run)
			rw = newRedactWriter(w, key, run)
		} else {
			rw = newRedactWriter(w, key)
		}
		out = rw
	}
	w.WriteHeader(resp.StatusCode)
	rc := http.NewResponseController(w)
	_ = rc.Flush()
	var n int64
	pump(out, rc, src, func(b []byte) { n += int64(len(b)) })
	if rw != nil {
		_ = rw.Close()
	}
	s.log.Info("token count", "stage", s.stageName(), "status", resp.StatusCode, "request_bytes", len(body), "response_bytes", n)
}

func errorTypeFor(status int) string {
	if status == http.StatusRequestEntityTooLarge {
		return "request_too_large"
	}
	return "invalid_request_error"
}

// jsonString quotes s as a JSON string.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// countRoutedModel is the model of a count_tokens body when a route claims
// it ("" when none does). With routes, a body that is not one clean JSON
// object (duplicate or odd keys, malformed) is an error: it never falls
// through to Anthropic, which may read it differently than the gateway
// did. Any spelling of the key "model" counts.
func (s *Server) countRoutedModel(body []byte) (string, error) {
	if len(s.o.Routes) == 0 {
		return "", nil
	}
	if err := checkKeys(body); err != nil {
		return "", errors.New("the request body is not acceptable: " + err.Error())
	}
	var top map[string]any
	if err := json.Unmarshal(body, &top); err != nil {
		return "", errors.New("the request body is not a JSON object")
	}
	for k, v := range top {
		if m, ok := v.(string); ok && strings.EqualFold(k, "model") && routeFor(m, s.o.Routes) != nil {
			return m, nil
		}
	}
	return "", nil
}
