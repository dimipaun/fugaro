package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"slices"
	"strconv"

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

// takeMax is take for a provider route: a later event never lowers a
// count, so a call is never charged less than any event said. A negative
// count sticks, so the call is still seen as unreadable.
func (f *field) takeMax(p *int64) {
	switch {
	case p == nil:
	case !f.set || *p < 0:
		if !f.set || f.v >= 0 {
			f.v, f.set = *p, true
		} else {
			f.v = min(f.v, *p)
		}
	case f.v >= 0:
		f.v = max(f.v, *p)
	}
}

// usageAcc accumulates usage: message_delta's usage is cumulative, so
// the last value reported for each field wins (on a provider route, the
// largest).
type usageAcc struct {
	provider                     bool // keep the largest count of each field, not the last
	in, cc, cr, out, w5, w1, web field
	speed, tier, geo             json.RawMessage
	// reported is the cost a provider put in the usage object (OpenRouter's
	// usage.cost, in USD), in micros. It is recorded for comparison with the
	// charge and never settles a call the table priced; on a provider route
	// it is the floor under the charge for a service tier no one prices
	// (forward.go's fromUsage).
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
	take := (*field).take
	if a.provider {
		take = (*field).takeMax
	}
	take(&a.in, u.InputTokens)
	take(&a.cc, u.CacheCreationInputTokens)
	take(&a.cr, u.CacheReadInputTokens)
	take(&a.out, u.OutputTokens)
	if c := u.CacheCreation; c != nil {
		take(&a.w5, c.W5)
		take(&a.w1, c.W1)
	}
	if t := u.ServerToolUse; t != nil {
		take(&a.web, t.WebSearchRequests)
	}
	// A null cost is no cost: it must not read as a reported zero.
	if len(u.Cost) > 0 && string(bytes.TrimSpace(u.Cost)) != "null" {
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

// hasInput reports whether the usage names enough to settle on. On the
// Claude route any reported input count does, even zero. On a provider
// route input alone may legitimately be zero under heavy caching, so a
// cache read or write counts too — but only once input_tokens has been
// reported at all (set), even as an explicit 0: a provider whose usage
// never includes the input_tokens key, in any event, has left input out
// entirely, not merely reported it as zero, and OWNER RULING treats that
// the same as a response with no usage at all (the reservation), never
// mind a real cache read elsewhere in the same usage. A usage that names
// none of input, cache read or cache write is not real usage either way:
// a real request always sends some input (a system prompt, at least one
// message), so a provider that never names an input count — with or
// without the key present at zero — simply left it out, and the call
// settles like any other missing field, at the reservation, however real
// the output it produced.
func (a *usageAcc) hasInput() bool {
	if !a.provider {
		return a.in.set
	}
	return a.in.set && (a.in.v > 0 || a.cr.v > 0 || a.cc.v > 0 || a.w5.v > 0 || a.w1.v > 0)
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

// unpriced is one pricing dimension the response reported that the table
// doesn't price: a speed other than standard, an inference geography other
// than the defaults, or a service tier it has no multiplier for.
type unpriced struct {
	field string // "speed", "service_tier" or "inference_geo"
	// value is the reported value as a message shows it: the string the
	// response named, quoted, or the raw JSON text of a value that isn't a
	// string at all. It is clipped, so it is never unbounded, and it is
	// never empty either -- a response that named the empty string reads
	// as `""` -- which is why a call charged conservatively can always be
	// named on the record.
	value string
	// tier is a service tier: the tier the provider itself served the call
	// from, which the agent's request said nothing about, unlike a speed or
	// an inference geography the request chose.
	tier bool
}

// String is how the dimension reads in a message and on the ledger:
// "usage.service_tier \"priority\"", or "usage.service_tier 12" for a value
// that isn't a string.
func (u unpriced) String() string { return "usage." + u.field + " " + u.value }

// unpricedDims names every pricing dimension the response reported that the
// table doesn't price, in a fixed order (speed, service tier, geography). A
// null counts as not reported; a value that isn't even a string is reported
// rather than dropped, since the table has no price for it either way.
func (a *usageAcc) unpricedDims() []unpriced {
	var out []unpriced
	check := func(field string, raw json.RawMessage, allowed []string, tier bool) {
		if len(raw) == 0 || string(raw) == "null" {
			return
		}
		var s string
		if json.Unmarshal(raw, &s) != nil {
			out = append(out, unpriced{field: field, value: logValue(string(raw)), tier: tier})
			return
		}
		if slices.Contains(allowed, s) {
			return
		}
		out = append(out, unpriced{field: field, value: strconv.Quote(logValue(s)), tier: tier})
	}
	check("speed", a.speed, []string{"standard"}, false)
	check("service_tier", a.tier, []string{pricing.StandardTier}, true)
	check("inference_geo", a.geo, DefaultGeos, false)
	return out
}

// usageTee reads usage from a copy of the response the client already
// got: SSE events for a stream, the whole body otherwise.
type usageTee struct {
	sse      bool
	provider bool // set before reading: usage is merged conservatively
	p        sseParser

	buf      []byte
	overflow bool

	started  bool // message_start (or a whole body) with usage was read
	deltas   bool // a message_delta reporting output_tokens was read after it
	complete bool // message_stop after both, or a whole body with input and output counts
	broken   bool // usage after the start didn't parse, was negative, or the stream stopped without an output count

	// Diagnostics for a call that settles unparsed: why, from a closed set,
	// never the provider's own text.
	sawUsage       bool     // some event's usage object was read, even all zero
	malformedEvent bool     // a usage-bearing event (message_start or message_delta, or the body) didn't parse
	stopped        bool     // the stream reached its end: message_stop, or the body decoded clean
	events         []string // event type names seen, bounded; never a body

	model     string
	acc       usageAcc
	errorType string
}

// maxEventsLogged bounds the distinct event type names a tee remembers for
// its usage_unparsed diagnostic.
const maxEventsLogged = 16

// knownEventTypes are the only SSE event names, or whole-body "type"
// values, the gateway ever dispatches on: the Messages API's own stream
// events, and "message"/"error" for a non-streaming body. A closed list,
// not a pattern (unlike errorTypeValue's error.type, an open-ish set an
// upstream or a provider names): nothing the provider sends can add to it.
var knownEventTypes = map[string]bool{
	"message_start": true, "content_block_start": true, "content_block_delta": true,
	"content_block_stop": true, "message_delta": true, "message_stop": true,
	"ping": true, "message": true, "error": true,
}

// eventTypeValue keeps typ only when it is one of knownEventTypes.
func eventTypeValue(typ string) string {
	if knownEventTypes[typ] {
		return typ
	}
	return "unrecognized"
}

// noteEvent records typ for the usage_unparsed diagnostic. typ is the
// provider's own SSE "event:" name or JSON "type" field, read before
// redactWriter ever sees the response: sanitized through the closed list
// above, so a hostile name (an embedded secret, control characters, an
// unbounded length) reaches the log only as "unrecognized", never as
// itself.
func (t *usageTee) noteEvent(typ string) {
	if typ == "" || len(t.events) >= maxEventsLogged {
		return
	}
	typ = eventTypeValue(typ)
	if slices.Contains(t.events, typ) {
		return
	}
	t.events = append(t.events, typ)
}

// hasOutputCount reports whether an output count that counts as final
// ever arrived: on a stream, one a message_delta reported (message_start's
// own is only ever a placeholder, never trusted alone); on a whole body,
// the single usage object's own output_tokens.
func (t *usageTee) hasOutputCount() bool {
	if t.sse {
		return t.deltas
	}
	return t.acc.out.set
}

// unparsedReason names, from a closed set, why a 2xx call settled at its
// reservation instead of its usage. Bounded and sanitized: never the
// provider's text, so a failure explains itself in `fugaro diagnose`.
func (t *usageTee) unparsedReason() string {
	switch {
	case t.acc.negative():
		return "negative_count"
	case t.malformedEvent:
		// A message_start or message_delta's own usage object didn't
		// parse (on the provider route, where either can hold the real
		// counts): the name covers both, not just the delta.
		return "malformed_usage"
	case !t.sawUsage:
		return "no_usage_event"
	case !t.stopped:
		return "stream_incomplete"
	case t.acc.hasInput() && !t.hasOutputCount():
		// A real input (or cache) count reached the end, so it is not the
		// all-zero fail-safe: it is that no output count ever arrived. On
		// a stream that is specifically no message_delta (message_start's
		// own output is only ever a placeholder, never trusted alone); on
		// a whole body, output_tokens itself.
		return "no_output_count"
	default:
		return "input_zero_no_cache"
	}
}

// newUsageTee reads a Claude response; a provider's sets tee.provider
// before reading.
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
	t.noteEvent(typ)
	t.acc.provider = t.provider // set regardless of whether this event carries usage
	switch typ {
	case "message_start":
		var ev struct {
			Message struct {
				Model string     `json:"model"`
				Usage *wireUsage `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(data, &ev) != nil {
			// On the provider route a later delta can still set started
			// (it is not gated on message_start the way the Claude route
			// is), so an unreadable message_start must not be silently
			// ignored: it may be hiding the real input count behind a
			// shape the gateway doesn't expect, same as an unreadable
			// delta. On the Claude route message_delta is already gated
			// on started, which an unreadable message_start never sets,
			// so main's behavior (the reservation) holds either way.
			if t.provider {
				t.broken, t.malformedEvent = true, true
			}
			return
		}
		if ev.Message.Usage == nil {
			return // no usage object: the documented "didn't say" case
		}
		t.model = ev.Message.Model
		t.acc.merge(ev.Message.Usage)
		t.sawUsage = true
		if t.acc.negative() {
			// A duplicate or late message_start can merge a negative count
			// into a call that already read as complete (message_stop
			// already fired, or will): usage() floors a negative field to
			// zero, so without this the call would settle on whatever is
			// left, not at its reservation. started and complete only ever
			// check broken, never negative() directly, so this is the one
			// place that must set it.
			t.broken = true
		}
		// Partial settlement charges the reported input: it must be there.
		t.started = t.acc.hasInput() && !t.acc.negative()
	case "message_delta":
		if !t.provider && !t.started {
			return // Claude route: unparsed either way, unchanged
		}
		var ev struct {
			Usage *wireUsage `json:"usage"`
		}
		if json.Unmarshal(data, &ev) != nil || ev.Usage == nil {
			// A usage the gateway can't read may hold the output count:
			// the call must not settle as complete from what came before.
			t.broken, t.malformedEvent = true, true
			return
		}
		t.acc.merge(ev.Usage)
		t.sawUsage = true
		if t.acc.negative() {
			t.broken = true
		}
		if ev.Usage.OutputTokens != nil {
			t.deltas = true
		}
		if t.provider {
			// A provider may report input_tokens 0 at message_start (heavy
			// caching, or simply nothing to say yet) and put the real,
			// cumulative counts only here: settle from what the stream
			// says once it is done, not from message_start alone.
			t.started = t.started || (t.acc.hasInput() && !t.acc.negative())
		}
	case "message_stop":
		t.stopped = true
		if !t.provider {
			switch {
			case t.started && t.deltas && !t.broken:
				t.complete = true
			case t.started:
				// Stopped without an output count: the gateway can't tell
				// what the call produced, so it settles at its reservation.
				t.broken = true
			}
			return
		}
		// A provider may put the real counts only in a delta (message_start
		// reporting zero input is not by itself "nothing to charge"), so
		// completeness is decided from the merged total here, not from
		// message_start alone. hasInput still requires a real input, cache
		// read or cache write count somewhere: a real request always sends
		// some input, so a usage that never names one, however real its
		// output, is a provider that left the field out, not a free call.
		switch {
		case t.deltas && !t.broken && t.acc.hasInput():
			t.complete = true
		case t.sawUsage:
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
	t.noteEvent(body.Type)
	if body.Error != nil {
		t.errorType = body.Error.Type
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return
	}
	t.stopped = true
	if body.Type == "error" || body.Usage == nil {
		return
	}
	t.model = body.Model
	t.acc.provider = t.provider
	t.acc.merge(body.Usage)
	t.sawUsage = true
	if !t.acc.out.set || t.acc.negative() || !t.acc.hasInput() {
		return // no output count, negative, or no input/cache count: settled at the reservation
	}
	t.started, t.complete = true, true
}
