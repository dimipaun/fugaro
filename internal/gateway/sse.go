package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"

	"github.com/dimipaun/fugaro/internal/pricing"
)

const (
	// maxSSELine bounds one SSE line the tee keeps; a longer one (a big
	// text delta) is skipped with its event. Usage events are small.
	maxSSELine = 1 << 20
	// maxJSONBody bounds a non-streaming body the tee keeps; a longer
	// one settles at its reservation.
	maxJSONBody = 16 << 20
)

// sseParser splits server-sent events. It sees only bytes already sent
// to the client.
type sseParser struct {
	onEvent func(name string, data []byte) // data is reused after the call

	line     []byte
	skipping bool // the current line is too long: drop it
	event    string
	data     []byte
	hasData  bool
	tainted  bool // a line of the current event was dropped
}

func (p *sseParser) feed(b []byte) {
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		chunk := b
		if i >= 0 {
			chunk, b = b[:i], b[i+1:]
		} else {
			b = nil
		}
		if !p.skipping {
			if len(p.line)+len(chunk) > maxSSELine {
				p.skipping, p.line = true, p.line[:0]
			} else {
				p.line = append(p.line, chunk...)
			}
		}
		if i < 0 {
			return
		}
		if p.skipping {
			p.tainted, p.skipping = true, false
		} else {
			p.processLine(bytes.TrimSuffix(p.line, []byte{'\r'}))
		}
		p.line = p.line[:0]
	}
}

func (p *sseParser) processLine(l []byte) {
	if len(l) == 0 {
		if p.hasData && !p.tainted {
			p.onEvent(p.event, p.data)
		}
		p.event, p.data, p.hasData, p.tainted = "", p.data[:0], false, false
		return
	}
	if l[0] == ':' {
		return
	}
	field, value := l, []byte(nil)
	if i := bytes.IndexByte(l, ':'); i >= 0 {
		field, value = l[:i], bytes.TrimPrefix(l[i+1:], []byte{' '})
	}
	switch string(field) {
	case "event":
		p.event = string(value)
	case "data":
		if p.hasData {
			p.data = append(p.data, '\n')
		}
		p.data = append(p.data, value...)
		p.hasData = true
		if len(p.data) > maxSSELine {
			p.tainted, p.data = true, p.data[:0]
		}
	}
}

// field is one usage count and whether any event reported it.
type field struct {
	v   int64
	set bool
}

func (f *field) take(p *int64) {
	if p != nil {
		f.v, f.set = *p, true
	}
}

// usageAcc accumulates usage: message_delta's usage is cumulative, so
// the last value reported for each field wins.
type usageAcc struct {
	in, cc, cr, out, w5, w1, web field
	speed, tier, geo             json.RawMessage
	// reported is the cost a provider put in the usage object (OpenRouter's
	// usage.cost, in USD), in micros. It is only recorded for comparison
	// with the charge and never settles a call.
	reported    pricing.Micros
	hasReported bool
}

// maxReportedUSD bounds a provider-reported cost the gateway will record;
// anything above it is a malformed field, not a price.
const maxReportedUSD = 1e6

type wireUsage struct {
	InputTokens              *int64 `json:"input_tokens"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
	OutputTokens             *int64 `json:"output_tokens"`
	CacheCreation            *struct {
		W5 *int64 `json:"ephemeral_5m_input_tokens"`
		W1 *int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
	ServerToolUse *struct {
		WebSearchRequests *int64 `json:"web_search_requests"`
	} `json:"server_tool_use"`
	Cost         json.RawMessage `json:"cost"`
	Speed        json.RawMessage `json:"speed"`
	ServiceTier  json.RawMessage `json:"service_tier"`
	InferenceGeo json.RawMessage `json:"inference_geo"`
}

func (a *usageAcc) merge(u *wireUsage) {
	a.in.take(u.InputTokens)
	a.cc.take(u.CacheCreationInputTokens)
	a.cr.take(u.CacheReadInputTokens)
	a.out.take(u.OutputTokens)
	if c := u.CacheCreation; c != nil {
		a.w5.take(c.W5)
		a.w1.take(c.W1)
	}
	if t := u.ServerToolUse; t != nil {
		a.web.take(t.WebSearchRequests)
	}
	if len(u.Cost) > 0 {
		var usd float64
		if json.Unmarshal(u.Cost, &usd) == nil && usd >= 0 && usd <= maxReportedUSD {
			a.reported, a.hasReported = pricing.Micros(math.Round(usd*1e6)), true
		}
	}
	for _, x := range []struct {
		dst *json.RawMessage
		src json.RawMessage
	}{{&a.speed, u.Speed}, {&a.tier, u.ServiceTier}, {&a.geo, u.InferenceGeo}} {
		if len(x.src) > 0 {
			*x.dst = append(json.RawMessage(nil), x.src...)
		}
	}
}

// negative reports whether any reported count is below zero.
func (a *usageAcc) negative() bool {
	for _, f := range []field{a.in, a.cc, a.cr, a.out, a.w5, a.w1, a.web} {
		if f.set && f.v < 0 {
			return true
		}
	}
	return false
}

// usage is the reported usage. Cache writes the response didn't split by
// TTL are priced at the highest write the request allowed.
func (a *usageAcc) usage(r pricing.Rates, cacheTTL string) pricing.Usage {
	pos := func(f field) int64 { return max(f.v, 0) }
	u := pricing.Usage{Input: pos(a.in), CacheRead: pos(a.cr), Output: pos(a.out), WebSearches: pos(a.web)}
	rest := pos(a.cc)
	if a.w5.set || a.w1.set {
		u.CacheWrite5m, u.CacheWrite1h = pos(a.w5), pos(a.w1)
		rest -= u.CacheWrite5m + u.CacheWrite1h
	}
	if rest > 0 {
		w5, w1 := r.UnsplitCacheWrites(rest, cacheTTL)
		u.CacheWrite5m += w5
		u.CacheWrite1h += w1
	}
	return u
}

// surprise names the first pricing dimension the response reported that
// the table doesn't price: a speed or service tier other than standard,
// or an inference geography other than the defaults. A null counts as
// not reported.
func (a *usageAcc) surprise() string {
	check := func(name string, raw json.RawMessage, allowed []string) string {
		if len(raw) == 0 || string(raw) == "null" {
			return ""
		}
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return "usage." + name + " " + logValue(string(raw))
		}
		if slices.Contains(allowed, s) {
			return ""
		}
		return fmt.Sprintf("usage.%s %q", name, logValue(s))
	}
	if s := check("speed", a.speed, []string{"standard"}); s != "" {
		return s
	}
	if s := check("service_tier", a.tier, []string{"standard"}); s != "" {
		return s
	}
	return check("inference_geo", a.geo, DefaultGeos)
}

// usageTee reads usage from a copy of the response the client already
// got: SSE events for a stream, the whole body otherwise.
type usageTee struct {
	sse bool
	p   sseParser

	buf      []byte
	overflow bool

	started   bool // message_start (or a whole body) with usage was read
	deltas    bool // a message_delta reporting output_tokens was read after it
	complete  bool // message_stop after both, or a whole body with input and output counts
	broken    bool // usage after the start didn't parse, was negative, or the stream stopped without an output count
	model     string
	acc       usageAcc
	errorType string
}

func newUsageTee(sse bool) *usageTee {
	t := &usageTee{sse: sse}
	t.p.onEvent = t.onEvent
	return t
}

func (t *usageTee) feed(b []byte) {
	if t.sse {
		t.p.feed(b)
		return
	}
	if t.overflow {
		return
	}
	if len(t.buf)+len(b) > maxJSONBody {
		t.overflow, t.buf = true, nil
		return
	}
	t.buf = append(t.buf, b...)
}

// finish reads a whole body; a stream was read as it came.
func (t *usageTee) finish() {
	if !t.sse {
		t.parseJSON()
	}
}

func (t *usageTee) onEvent(name string, data []byte) {
	typ := name
	if typ == "" {
		var head struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(data, &head)
		typ = head.Type
	}
	switch typ {
	case "message_start":
		var ev struct {
			Message struct {
				Model string     `json:"model"`
				Usage *wireUsage `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(data, &ev) != nil || ev.Message.Usage == nil {
			return
		}
		t.model = ev.Message.Model
		t.acc.merge(ev.Message.Usage)
		// Partial settlement charges the reported input: it must be there.
		t.started = t.acc.in.set && !t.acc.negative()
	case "message_delta":
		if !t.started {
			return // unparsed either way: the call settles at its reservation
		}
		var ev struct {
			Usage *wireUsage `json:"usage"`
		}
		if json.Unmarshal(data, &ev) != nil || ev.Usage == nil {
			// A usage the gateway can't read may hold the output count:
			// the call must not settle as complete from what came before.
			t.broken = true
			return
		}
		t.acc.merge(ev.Usage)
		if t.acc.negative() {
			t.broken = true
		}
		if ev.Usage.OutputTokens != nil {
			t.deltas = true
		}
	case "message_stop":
		switch {
		case t.started && t.deltas && !t.broken:
			t.complete = true
		case t.started:
			// Stopped without an output count: the gateway can't tell
			// what the call produced, so it settles at its reservation.
			t.broken = true
		}
	case "error":
		var ev struct {
			Error struct {
				Type string `json:"type"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &ev) == nil {
			t.errorType = ev.Error.Type
		}
	}
}

func (t *usageTee) parseJSON() {
	if t.overflow {
		return
	}
	dec := json.NewDecoder(bytes.NewReader(t.buf))
	var body struct {
		Type  string     `json:"type"`
		Model string     `json:"model"`
		Usage *wireUsage `json:"usage"`
		Error *struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if dec.Decode(&body) != nil {
		return
	}
	if body.Error != nil {
		t.errorType = body.Error.Type
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return
	}
	if body.Type == "error" || body.Usage == nil {
		return
	}
	t.model = body.Model
	t.acc.merge(body.Usage)
	if !t.acc.in.set || !t.acc.out.set || t.acc.negative() {
		return // counts missing or negative: settled at the reservation
	}
	t.started, t.complete = true, true
}
