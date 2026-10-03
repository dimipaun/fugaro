package gateway

import (
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
