package gateway

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

type seenEvent struct{ name, data string }

func feedAll(chunks ...string) []seenEvent {
	var got []seenEvent
	p := &sseParser{onEvent: func(name string, data []byte) {
		got = append(got, seenEvent{name, string(data)})
	}}
	for _, c := range chunks {
		p.feed([]byte(c))
	}
	return got
}

func TestSSEParserEvents(t *testing.T) {
	stream := "event: message_start\ndata: {\"a\":1}\n\n: a comment\n\nevent: ping\ndata: {}\n\ndata: {\"type\":\"x\"}\n\n"
	want := []seenEvent{{"message_start", `{"a":1}`}, {"ping", `{}`}, {"", `{"type":"x"}`}}
	// Whole, byte by byte, and in odd chunks: the same events.
	for name, chunks := range map[string][]string{
		"whole":   {stream},
		"bytes":   strings.Split(stream, ""),
		"chunked": {stream[:7], stream[7:30], stream[30:31], stream[31:]},
	} {
		got := feedAll(chunks...)
		if len(got) != len(want) {
			t.Errorf("%s: %q, want %q", name, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s: event %d = %q, want %q", name, i, got[i], want[i])
			}
		}
	}
}

func TestSSEParserCRLFAndMultiline(t *testing.T) {
	got := feedAll("event: a\r\ndata: x\r\ndata: y\r\n\r\n")
	if len(got) != 1 || got[0] != (seenEvent{"a", "x\ny"}) {
		t.Errorf("got %q", got)
	}
}

func TestSSEParserSkipsLongLines(t *testing.T) {
	long := "data: " + strings.Repeat("x", maxSSELine+10) + "\n"
	got := feedAll("event: big\n", long[:maxSSELine/2], long[maxSSELine/2:], "\n", "event: message_delta\ndata: {\"u\":1}\n\n")
	if len(got) != 1 || got[0] != (seenEvent{"message_delta", `{"u":1}`}) {
		t.Errorf("got %d events %.80q; want only the small event after the skipped one", len(got), got)
	}
}

func TestSSEParserIncompleteEventNotDispatched(t *testing.T) {
	if got := feedAll("event: message_stop\ndata: {}\n"); len(got) != 0 {
		t.Errorf("an event without its blank line was dispatched: %q", got)
	}
}

func TestUsageTeeStates(t *testing.T) {
	start := `{"type":"message_start","message":{"model":"claude-sonnet-5-5","usage":{"input_tokens":5,"cache_read_input_tokens":7,"output_tokens":1}}}`
	delta := `{"type":"message_delta","usage":{"output_tokens":9}}`
	for _, c := range []struct {
		name              string
		stream            string
		started, complete bool
		out               int64
	}{
		{"complete", "event: message_start\ndata: " + start + "\n\nevent: message_delta\ndata: " + delta + "\n\nevent: message_stop\ndata: {}\n\n", true, true, 9},
		{"cut", "event: message_start\ndata: " + start + "\n\nevent: message_delta\ndata: " + delta + "\n\n", true, false, 9},
		{"delta without start", "event: message_delta\ndata: " + delta + "\n\nevent: message_stop\ndata: {}\n\n", false, false, 0},
		{"stop without a delta", "event: message_start\ndata: " + start + "\n\nevent: message_stop\ndata: {}\n\n", true, false, 1},
		{"unreadable delta", "event: message_start\ndata: " + start + "\n\nevent: message_delta\ndata: {\"usage\":{\"output_tokens\":9.5}}\n\nevent: message_stop\ndata: {}\n\n", true, false, 1},
		{"start without usage", "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"m\"}}\n\nevent: message_stop\ndata: {}\n\n", false, false, 0},
		{"bad start", "event: message_start\ndata: {nope\n\nevent: message_stop\ndata: {}\n\n", false, false, 0},
		{"error after start", "event: message_start\ndata: " + start + "\n\nevent: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"x\"}}\n\n", true, false, 1},
	} {
		tee := newUsageTee(true)
		tee.feed([]byte(c.stream))
		tee.finish()
		if tee.started != c.started || tee.complete != c.complete || tee.acc.out.v != c.out {
			t.Errorf("%s: started %v complete %v out %d, want %v %v %d", c.name, tee.started, tee.complete, tee.acc.out.v, c.started, c.complete, c.out)
		}
	}
	tee := newUsageTee(true)
	tee.feed([]byte("event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\"}}\n\n"))
	if tee.errorType != "overloaded_error" {
		t.Errorf("error type %q", tee.errorType)
	}
}

func TestUsageTeeJSON(t *testing.T) {
	body := `{"type":"message","model":"claude-haiku-4-5","usage":{"input_tokens":3,"cache_creation_input_tokens":4,"cache_read_input_tokens":5,"output_tokens":6}}`
	tee := newUsageTee(false)
	tee.feed([]byte(body[:10]))
	tee.feed([]byte(body[10:]))
	tee.finish()
	if !tee.complete || tee.model != "claude-haiku-4-5" || tee.acc.in.v != 3 || tee.acc.cc.v != 4 || tee.acc.cr.v != 5 || tee.acc.out.v != 6 {
		t.Errorf("tee %+v", tee)
	}
	for name, b := range map[string]string{
		"truncated":   body[:len(body)-3],
		"no usage":    `{"type":"message","model":"m"}`,
		"error body":  `{"type":"error","error":{"type":"api_error","message":"x"}}`,
		"not an obj":  `[1]`,
		"two objects": body + body,
	} {
		tee := newUsageTee(false)
		tee.feed([]byte(b))
		tee.finish()
		if tee.complete {
			t.Errorf("%s: parsed as complete", name)
		}
	}
	tee = newUsageTee(false)
	tee.feed([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow"}}`))
	tee.finish()
	if tee.errorType != "rate_limit_error" {
		t.Errorf("error type %q", tee.errorType)
	}
}

// On a provider route a later usage event never lowers a count, and a
// zero input is not usage; a Claude stream keeps the last value.
func TestUsageTeeProviderMax(t *testing.T) {
	stream := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1000,\"output_tokens\":1}}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":0,\"output_tokens\":7}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":3}}\n\n" +
		"event: message_stop\ndata: {}\n\n"
	for _, c := range []struct {
		provider bool
		in, out  int64
	}{{true, 1000, 7}, {false, 0, 3}} {
		tee := newUsageTee(true)
		tee.provider = c.provider
		tee.feed([]byte(stream))
		if !tee.complete || tee.acc.in.v != c.in || tee.acc.out.v != c.out {
			t.Errorf("provider %v: complete %v in %d out %d, want %d %d", c.provider, tee.complete, tee.acc.in.v, tee.acc.out.v, c.in, c.out)
		}
	}
	tee := newUsageTee(true)
	tee.provider = true
	tee.feed([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":0,\"output_tokens\":1}}}\n\n"))
	if tee.started {
		t.Error("a provider stream with zero input counted as started")
	}
	tee = newUsageTee(true)
	tee.provider = true
	tee.feed([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":-1,\"output_tokens\":1}}\n\n"))
	if !tee.acc.negative() || !tee.broken {
		t.Error("a negative count was hidden by the max")
	}
}

func TestUsageAccNullCostNotReported(t *testing.T) {
	var a usageAcc
	var u wireUsage
	for raw, want := range map[string]bool{`null`: false, `1e999`: false, `-1`: false, `0`: true, `0.5`: true} {
		a = usageAcc{}
		u.Cost = []byte(raw)
		a.merge(&u)
		if a.hasReported != want {
			t.Errorf("cost %s: hasReported %v, want %v", raw, a.hasReported, want)
		}
	}
}

// TestUsageTeeRealOpenRouterDeepInfraStream parses the abridged SSE
// response a live run captured from OpenRouter routing to
// deepseek/deepseek-v4.1-flash served by DeepInfra (the fixture backing
// the gap between the gateway's charge and OpenRouter's own Activity
// figure): message_start carries an all-zero usage (null cache fields)
// and the real, cumulative counts, plus the provider's own cost, arrive
// only in message_delta. Abridged, not verbatim: the thinking block's one
// "The" delta and the text block's own lack of any deltas do not by
// themselves add up to the usage object's 47 output tokens; only the
// usage fields, which this test and TestFixtureEndToEnd* check, were
// captured faithfully.
func TestUsageTeeRealOpenRouterDeepInfraStream(t *testing.T) {
	raw, err := os.ReadFile("testdata/openrouter_deepinfra_stream.sse")
	if err != nil {
		t.Fatal(err)
	}
	tee := newUsageTee(true)
	tee.provider = true
	tee.feed(raw)
	if !tee.complete || tee.broken {
		t.Fatalf("complete %v broken %v, want a parsed call", tee.complete, tee.broken)
	}
	if tee.model != "deepseek/deepseek-v4.1-flash" {
		t.Errorf("model %q", tee.model)
	}
	if tee.acc.in.v != 32 || tee.acc.cr.v != 0 || tee.acc.out.v != 47 {
		t.Errorf("in %d cr %d out %d, want 32 0 47", tee.acc.in.v, tee.acc.cr.v, tee.acc.out.v)
	}
	// The real stream's usage.cost is $0.00002422, in micros.
	if !tee.acc.hasReported || tee.acc.reported != 24 {
		t.Errorf("reported %d hasReported %v, want 24 true", tee.acc.reported, tee.acc.hasReported)
	}
}

// On a provider route the gateway settles from the usage merged by the
// time the stream ends, not from message_start alone: message_start
// reporting input_tokens 0 (OpenRouter/DeepInfra's shape, or simply
// nothing to say yet) must not block a message_delta that reports the
// real, cumulative counts, and zero input alongside a real cache read is
// a reply that did say something (heavy caching). Zero input AND zero
// cache, with only a real output count, is not: a real request always
// sends some input, so that shape is a provider that left the field out,
// not a free call, and the gateway must still fall back to the
// reservation (the HIGH finding this guards: an output-only total would
// otherwise settle such a call for its output alone).
func TestUsageTeeProviderSettlesFromFinalUsage(t *testing.T) {
	build := func(startUsage, deltaUsage string) string {
		return "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"m\",\"usage\":" + startUsage + "}}\n\n" +
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":" + deltaUsage + "}\n\n" +
			"event: message_stop\ndata: {}\n\n"
	}
	for _, c := range []struct {
		name                    string
		stream                  string
		wantIn, wantCR, wantOut int64
	}{
		{
			name:   "zero input at start, real counts in the delta",
			stream: build(`{"input_tokens":0,"output_tokens":1}`, `{"input_tokens":32,"cache_read_input_tokens":5,"output_tokens":47,"cost":0.0001}`),
			wantIn: 32, wantCR: 5, wantOut: 47,
		},
		{
			name:   "zero input throughout, heavy caching",
			stream: build(`{"input_tokens":0,"cache_read_input_tokens":900,"output_tokens":1}`, `{"input_tokens":0,"cache_read_input_tokens":900,"output_tokens":50}`),
			wantIn: 0, wantCR: 900, wantOut: 50,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			tee := newUsageTee(true)
			tee.provider = true
			tee.feed([]byte(c.stream))
			if !tee.complete || tee.broken {
				t.Fatalf("complete %v broken %v, want a parsed call", tee.complete, tee.broken)
			}
			if tee.acc.in.v != c.wantIn || tee.acc.cr.v != c.wantCR || tee.acc.out.v != c.wantOut {
				t.Errorf("in %d cr %d out %d, want %d %d %d", tee.acc.in.v, tee.acc.cr.v, tee.acc.out.v, c.wantIn, c.wantCR, c.wantOut)
			}
		})
	}
	t.Run("zero input and cache, real output", func(t *testing.T) {
		tee := newUsageTee(true)
		tee.provider = true
		tee.feed([]byte(build(`{"input_tokens":0,"output_tokens":1}`, `{"input_tokens":0,"output_tokens":2000}`)))
		if tee.complete {
			t.Fatalf("complete %v, want unparsed (the reservation): zero input and zero cache is not real usage, whatever the output", tee.complete)
		}
		if reason := tee.unparsedReason(); reason != "input_zero_no_cache" {
			t.Errorf("reason %q, want input_zero_no_cache", reason)
		}
	})
}

// unparsedReason is a closed, bounded set: never the provider's own text.
func TestUsageTeeUnparsedReason(t *testing.T) {
	for _, c := range []struct{ name, stream, want string }{
		{
			name: "all zero usage",
			stream: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n\n" +
				"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}\n\n" +
				"event: message_stop\ndata: {}\n\n",
			want: "input_zero_no_cache",
		},
		{
			name: "negative count",
			stream: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}\n\n" +
				"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":-1,\"output_tokens\":1}}\n\n" +
				"event: message_stop\ndata: {}\n\n",
			want: "negative_count",
		},
		{
			name: "malformed delta",
			stream: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}\n\n" +
				"event: message_delta\ndata: {\"usage\":5}\n\n" +
				"event: message_stop\ndata: {}\n\n",
			want: "malformed_usage",
		},
		{
			name:   "no usage event at all",
			stream: "event: ping\ndata: {\"type\":\"ping\"}\n\nevent: message_stop\ndata: {}\n\n",
			want:   "no_usage_event",
		},
		{
			name:   "stream never reaches message_stop",
			stream: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n\n",
			want:   "stream_incomplete",
		},
		{
			// A real input count reached message_stop, but no message_delta
			// ever named an output count: not the all-zero fail-safe (input
			// isn't zero), and not "never said anything" (something was
			// said) — the gap is specifically the output count.
			name: "message_stop with input but no output count",
			stream: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":500,\"output_tokens\":1}}}\n\n" +
				"event: message_stop\ndata: {}\n\n",
			want: "no_output_count",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			tee := newUsageTee(true)
			tee.provider = true
			tee.feed([]byte(c.stream))
			if got := tee.unparsedReason(); got != c.want {
				t.Errorf("reason %q, want %q", got, c.want)
			}
		})
	}
}

// A whole (non-streaming) body with a real input count but no
// output_tokens at all is the same gap as a stream whose message_delta
// never names one: no_output_count, not the misleading
// input_zero_no_cache (input is not zero here).
func TestUsageTeeJSONNoOutputCountReason(t *testing.T) {
	tee := newUsageTee(false)
	tee.provider = true
	tee.feed([]byte(`{"type":"message","model":"m","usage":{"input_tokens":500}}`))
	tee.finish()
	if tee.complete {
		t.Fatalf("complete %v, want unparsed: no output count", tee.complete)
	}
	if got := tee.unparsedReason(); got != "no_output_count" {
		t.Errorf("reason %q, want no_output_count", got)
	}
}

// noteEvent reads the provider's own SSE event name or JSON "type" field,
// before redactWriter ever sees the response: a hostile one (a key-looking
// string, embedded newlines, an unbounded length) must never reach the
// usage_unparsed diagnostic as itself.
func TestUsageTeeEventNamesSanitized(t *testing.T) {
	hostile := "sk-ant-api03-LEAKEDSECRETKEY\ninjected: true\n" + strings.Repeat("x", 10000)
	// A data-only frame (no "event:" line): onEvent reads typ from the
	// JSON body's "type" field, which can carry a literal embedded
	// newline an SSE "event:" line never could.
	data := "data: {\"type\":" + strconv.Quote(hostile) + "}\n\n"
	tee := newUsageTee(true)
	tee.provider = true
	tee.feed([]byte(data))
	if len(tee.events) != 1 || tee.events[0] != "unrecognized" {
		t.Fatalf("events %q, want exactly one %q", tee.events, "unrecognized")
	}
	for _, e := range tee.events {
		if strings.Contains(e, "sk-ant-api03") || strings.ContainsAny(e, "\n") || len(e) > 64 {
			t.Errorf("a hostile event name reached the tee's events: %q", e)
		}
	}
}

// noteEvent's sanitizer is a closed list of the Messages API's own event
// (and whole-body "type") names, not merely a pattern: a name that would
// pass errorTypeValue's `^[a-z][a-z0-9_]{0,63}$` check (plausible,
// lowercase, short) but isn't one the gateway actually dispatches on must
// still report as "unrecognized", not itself.
func TestUsageTeeEventNamesClosedList(t *testing.T) {
	tee := newUsageTee(true)
	tee.provider = true
	tee.feed([]byte("data: {\"type\":\"a_plausible_but_unknown_type\"}\n\n"))
	if len(tee.events) != 1 || tee.events[0] != "unrecognized" {
		t.Errorf("events %q, want exactly one %q: a pattern-only check would have kept the name", tee.events, "unrecognized")
	}
	// The known names themselves still come through, unsanitized.
	tee2 := newUsageTee(true)
	tee2.provider = true
	tee2.feed([]byte("event: message_start\ndata: {}\n\nevent: ping\ndata: {}\n\n"))
	if len(tee2.events) != 2 || tee2.events[0] != "message_start" || tee2.events[1] != "ping" {
		t.Errorf("events %q, want [message_start ping]", tee2.events)
	}
}
