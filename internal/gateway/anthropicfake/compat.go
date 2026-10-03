package anthropicfake

import (
	"net/http"

	"github.com/dimipaun/fugaro/internal/pricing"
)

// The knobs below make the fake behave like a provider that speaks the
// Messages API loosely (OpenRouter, DeepSeek's Anthropic endpoint). The
// shapes are written by hand from the providers' public docs and are
// UNVERIFIED until replaced by a recording of a real call (design §13).

// Compat shapes a compat-provider reply.
type Compat struct {
	// OmitCacheFields drops the cache_* usage fields, as a provider that
	// does not report caching does.
	OmitCacheFields bool
	// OmitUsage sends no usage at all (message_start and message_delta
	// carry none).
	OmitUsage bool
	// Cost, when > 0, adds the provider-reported dollar cost to the usage
	// object, as OpenRouter's usage accounting does.
	Cost float64
	// ServedBy is the model name the reply carries when the provider
	// served another model than the one asked for ("" keeps the model).
	ServedBy string
}

func (c Compat) usage(u pricing.Usage, output int64) map[string]any {
	if c.OmitUsage {
		return nil
	}
	m := usageJSON(u, output)
	if c.OmitCacheFields {
		delete(m, "cache_creation_input_tokens")
		delete(m, "cache_read_input_tokens")
		delete(m, "cache_creation")
	}
	if c.Cost > 0 {
		m["cost"] = c.Cost
	}
	return m
}

func (c Compat) model(model string) string {
	if c.ServedBy != "" {
		return c.ServedBy
	}
	return model
}

// StreamOK is a complete stream for model, shaped by c.
func (c Compat) StreamOK(model string, u pricing.Usage) Reply {
	start := map[string]any{
		"id": "msg_fake", "type": "message", "role": "assistant", "model": c.model(model),
		"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
	}
	delta := map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
	}
	if us := c.usage(u, 1); us != nil {
		start["usage"] = us
	}
	if us := c.usage(u, u.Output); us != nil {
		delta["usage"] = us
	}
	ev := []Event{
		{Name: "message_start", Data: mustJSON(map[string]any{"type": "message_start", "message": start})},
		Ping,
	}
	ev = append(ev, TextEvents("ok")...)
	ev = append(ev, Event{Name: "message_delta", Data: mustJSON(delta)}, Stop)
	return Reply{Status: http.StatusOK, Events: ev}
}

// MessageOK is a complete non-streaming reply for model, shaped by c.
func (c Compat) MessageOK(model string, u pricing.Usage) Reply {
	body := map[string]any{
		"id": "msg_fake", "type": "message", "role": "assistant", "model": c.model(model),
		"content":     []any{map[string]any{"type": "text", "text": "ok"}},
		"stop_reason": "end_turn", "stop_sequence": nil,
	}
	if us := c.usage(u, u.Output); us != nil {
		body["usage"] = us
	}
	return Reply{Status: http.StatusOK, Body: mustJSON(body)}
}

// RequireAuth makes the fake answer 401 to any request whose header name
// does not carry value ("x-api-key" with the key, or "Authorization" with
// "Bearer <key>"). Requests are recorded either way, so a test can read
// what credential arrived.
func (f *Fake) RequireAuth(name, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authName, f.authValue = name, value
}

// Header returns header name of request i, as received ("" if absent).
func (f *Fake) Header(i int, name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seen[i].Header.Get(name)
}

// authFailsLocked reports whether r must be refused for its credential; the
// caller holds f.mu.
func (f *Fake) authFailsLocked(r *http.Request) bool {
	return r.Header.Get(f.authName) != f.authValue
}
