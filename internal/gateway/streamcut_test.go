package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// A streamed response that ends before its terminal event is one fact about
// the call whatever it settles as (issue #1827): the model never finished
// its answer. These tests pin what the gateway says about it -- the stage
// report's count, which a runner reads to tell a lost stream from a failure
// about the work, and the warning that says which end ended the stream and
// how far it got, without carrying a byte of anyone's content.

// streamCutLine is the last "budget: stream ended before its terminal
// event" line, and the raw line itself (for the no-content checks).
func streamCutLine(t *testing.T, l *logBuf) (map[string]any, string) {
	t.Helper()
	var out map[string]any
	var raw string
	found := false
	for _, line := range strings.Split(strings.TrimSpace(l.String()), "\n") {
		if !strings.Contains(line, "stream ended before its terminal event") {
			continue
		}
		if err := json.Unmarshal([]byte(line), &out); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		raw, found = line, true
	}
	if !found {
		t.Fatalf("no stream-cut warning logged; log:\n%s", l.String())
	}
	return out, raw
}

// A provider stream cut mid-body is counted on the stage and logged with the
// side that ended it (the upstream, not the agent) and how far it got: the
// bytes and events the agent was sent, and the last event's own name.
func TestProviderStreamCutIsLoggedAndCounted(t *testing.T) {
	reply := anthropicfake.Compat{OmitCacheFields: true}.StreamOK(dsModel, pricing.Usage{Input: 2000, Output: 5})
	reply.CutAfter = 3
	r := feeRouted(t, 0, nil, routedStage, dsRoute, reply)
	if resp, b := r.post(msg(dsModel, 1000, `"stream":true`)); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	if rep := r.gw.EndStage(); rep.StreamsCut != 1 {
		t.Errorf("stage counted %d cut streams, want 1", rep.StreamsCut)
	}
	line, _ := streamCutLine(t, r.logs)
	if line["stage"] != routedStage.Name || line["model"] != dsModel {
		t.Errorf("stage/model = %v/%v, want %s/%s", line["stage"], line["model"], routedStage.Name, dsModel)
	}
	if line["status"] != float64(http.StatusOK) {
		t.Errorf("status = %v, want 200 (the upstream answered, then its body ended)", line["status"])
	}
	if line["terminal_event_seen"] != false {
		t.Errorf("terminal_event_seen = %v, want false: message_stop never arrived", line["terminal_event_seen"])
	}
	// StreamOK's third event is the text block's start: the stream was cut
	// after it, so three events were forwarded and that is the last one.
	if line["events"] != float64(3) || line["last_event"] != "content_block_start" {
		t.Errorf("events/last_event = %v/%v, want 3/content_block_start", line["events"], line["last_event"])
	}
	if b, _ := line["bytes"].(float64); b <= 0 {
		t.Errorf("bytes = %v, want the bytes the agent was sent", line["bytes"])
	}
	if ms, _ := line["ms"].(float64); ms < 0 {
		t.Errorf("ms = %v, want a duration", line["ms"])
	}
	if line["cut"] != cutUpstream {
		t.Errorf("cut = %v, want %q: the upstream's body ended, not the agent's read", line["cut"], cutUpstream)
	}
	// The read that failed is named from the closed set: without it the
	// line says which side ended the stream but not what the connection
	// did, which is the half an operator is looking for.
	if line["error"] != endUnexpectedEOF {
		t.Errorf("error = %v, want %q: the connection was dropped mid-body", line["error"], endUnexpectedEOF)
	}
}

// The same on a Claude call: the diagnostic is not the provider route's, it
// is every streamed call's.
func TestClaudeStreamCutIsLoggedAndCounted(t *testing.T) {
	reply := anthropicfake.StreamOK(sonnet, pricing.Usage{Input: 2000, Output: 5})
	reply.CutAfter = 2
	h := newHarness(t, reply)
	if resp, b := h.post(msg(sonnet, 1000, `"stream":true`)); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	if rep := h.gw.EndStage(); rep.StreamsCut != 1 {
		t.Errorf("stage counted %d cut streams, want 1", rep.StreamsCut)
	}
	line, _ := streamCutLine(t, h.logs)
	if line["cut"] != cutUpstream || line["last_event"] != "ping" {
		t.Errorf("cut/last_event = %v/%v, want upstream/ping", line["cut"], line["last_event"])
	}
	if line["terminal_event_seen"] != false {
		t.Error("terminal_event_seen is true for a stream cut before message_stop")
	}
	if line["error"] != endUnexpectedEOF {
		t.Errorf("error = %v, want %q", line["error"], endUnexpectedEOF)
	}
}

// A stream the agent's own end stopped is logged with that side and is not
// counted: the count (StageReport.StreamsCut) is the model's unfinished
// answer, and a reader retries a stage because of it. An agent that stopped
// reading, or a gateway that cancelled the call, ended this stream; the model
// was still answering, and nothing about it is the runner's to act on.
func TestClientCutStreamIsNotCounted(t *testing.T) {
	rep := anthropicfake.StreamOK(sonnet, pricing.Usage{Input: 2000, Output: 5})
	rep.EventDelay = 100 * time.Millisecond
	h := newHarness(t, rep)
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan struct{})
	errc := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(h.request(ctx, "/v1/messages", msg(sonnet, 1000, `"stream":true`)))
		if err != nil {
			errc <- err
			return
		}
		defer resp.Body.Close()
		br := bufio.NewReader(resp.Body)
		if _, err := br.ReadString('\n'); err != nil {
			errc <- err
			return
		}
		close(first)
		_, _ = io.Copy(io.Discard, br)
		errc <- nil
	}()
	waitFor(t, "the upstream to see the call", func() bool { return h.fake.Count() == 1 })
	<-first
	cancel() // the agent's end goes away, mid-response
	<-errc
	select {
	case <-h.fake.Ended(0):
	case <-time.After(5 * time.Second):
		t.Fatal("the upstream request wasn't cancelled")
	}
	if rep := h.gw.EndStage(); rep.StreamsCut != 0 {
		t.Errorf("stage counted %d cut streams for a stream the agent stopped reading, want 0: the count is what a runner retries a stage on", rep.StreamsCut)
	}
	line, _ := streamCutLine(t, h.logs)
	if line["cut"] != cutClient {
		t.Errorf("cut = %v, want %q: the agent's end went away, and the line still says which end it was", line["cut"], cutClient)
	}
	// Which read or write failed here is the race this test starts on
	// purpose -- the agent's cancel reaches the upstream read and this
	// gateway's write at once -- so the value is not pinned, only that it is
	// one of the closed set's names: a sentence there, or anything the
	// transport wrote, would mean the guard had let it through.
	if e, ok := line["error"].(string); !ok || !streamEndName(e) {
		t.Errorf("error = %v, want a name from the closed set", line["error"])
	}
}

// streamEndName reports whether e is one of the names the stream-cut line's
// `error` attribute may hold: the closed set, the two HTTP/2 forms with the
// peer's code appended, and the empty value a body that simply ended at an
// event boundary leaves. The set is a contract -- a reader tells a lost
// stream from a cancelled one by these words -- so the test names every
// member rather than accepting any word that looks like one.
func streamEndName(e string) bool {
	switch e {
	case "", endUnexpectedEOF, endCanceled, endDeadline, endReset, endWriteFailed, endOther:
		return true
	}
	for _, coded := range []string{endGoAway, endStreamError} {
		if code, ok := strings.CutPrefix(e, coded+":"); ok {
			return code != "" && !strings.ContainsAny(code, " \n\r\t")
		}
	}
	return false
}

// A whole stream is not a cut one: the count stays 0 and no warning is
// written, which is what makes the count mean "the model did not finish".
func TestWholeStreamIsNotACutOne(t *testing.T) {
	h := newHarness(t, anthropicfake.StreamOK(sonnet, pricing.Usage{Input: 2000, Output: 5}))
	if resp, b := h.post(msg(sonnet, 1000, `"stream":true`)); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	if rep := h.gw.EndStage(); rep.StreamsCut != 0 {
		t.Errorf("stage counted %d cut streams for a complete response, want 0", rep.StreamsCut)
	}
	if s := h.logs.String(); strings.Contains(s, "stream ended before its terminal event") {
		t.Errorf("a complete stream warned about a cut one:\n%s", s)
	}
}

// The line names the call and carries no content, no header and no
// credential: neither the run's own token (the caller's, scrubbed from every
// forwarded call) nor the provider's key, and not one byte of the request or
// of the response the upstream sent.
func TestStreamCutLineCarriesNoContent(t *testing.T) {
	const nonce = "NONCE-PROMPT-f3a1c9"
	reply := anthropicfake.Compat{OmitCacheFields: true}.StreamOK(dsModel, pricing.Usage{Input: 2000, Output: 5})
	reply.CutAfter = 3
	r := feeRouted(t, 0, nil, routedStage, dsRoute, reply)
	body := `{"model":"` + dsModel + `","max_tokens":1000,"stream":true,"messages":[{"role":"user","content":"` + nonce + `"}]}`
	if resp, b := r.post(body, "authorization", "Bearer "+agentSecret); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	line, raw := streamCutLine(t, r.logs)
	if line["error"] != endUnexpectedEOF {
		t.Errorf("error = %v, want %q: the connection was dropped mid-body", line["error"], endUnexpectedEOF)
	}
	secrets := map[string]string{
		"the prompt":          nonce,
		"the provider's key":  providerKey,
		"the agent's own key": agentSecret,
		"the run's token":     r.gw.Token(),
		"the token's prefix":  r.gw.Token()[:16],
	}
	for _, bad := range []string{"x-api-key", "authorization", "anthropic-version"} {
		secrets[bad] = bad
	}
	all := r.logs.String()
	for name, s := range secrets {
		if strings.Contains(all, s) {
			t.Errorf("the log holds %s: %q", name, s)
		}
	}
	if !strings.Contains(raw, "stream ended before its terminal event") {
		t.Errorf("the line checked was not the stream-cut one: %s", raw)
	}
}

// quotingTransport answers exactly as the fake upstream would, then hands
// the gateway a body whose first read fails with an error that quotes
// everything the call carried. That is what an HTTP/2 GOAWAY does: the
// transport prints the peer's own debug data into its message, verbatim, so
// the error value itself is text the peer wrote. A test can only see that
// by making the peer write it.
type quotingTransport struct {
	base http.RoundTripper
	text func(*http.Request) string
}

func (t quotingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	resp.Body = failingBody{closer: resp.Body, err: errors.New(t.text(r))}
	return resp, nil
}

type failingBody struct {
	closer io.Closer
	err    error
}

func (b failingBody) Read([]byte) (int, error) { return 0, b.err }
func (b failingBody) Close() error             { return b.closer.Close() }

// peerMarker is a word only the peer wrote: nothing in this gateway can
// produce it, so finding it in a log line is finding the peer's text there.
const peerMarker = "PEER-WROTE-THIS-7b21"

// peerWords is what this case's GOAWAY says: the credentials the call
// carried, read back out of the request the gateway sent, and a marker of
// the peer's own. debug is %q in the real message, so it is quoted here too.
func peerWords(r *http.Request) string {
	return "http2: server sent GOAWAY and closed the connection; LastStreamID=1, ErrCode=NO_ERROR, debug=" +
		strconv.Quote(strings.Join([]string{
			keyFrom(r.Header), r.Header.Get(HeaderRunIdentity), r.Header.Get("Authorization"), peerMarker,
		}, " "))
}

// No word of the transport's message reaches the stream-cut line, on either
// route, with the credentials both of them carry quoted into that message on
// purpose. This is what the closed set buys: the line says the peer sent a
// GOAWAY and which code, and never says what else the peer wrote -- so a
// provider cannot name anything in a log line, and cannot put a key it was
// sent into one either.
func TestStreamCutLineDropsTheTransportsWords(t *testing.T) {
	quoting := func(*Options) *http.Client {
		return &http.Client{Transport: quotingTransport{base: http.DefaultTransport, text: peerWords}}
	}
	check := func(t *testing.T, h *harness) {
		t.Helper()
		line, raw := streamCutLine(t, h.logs)
		if line["error"] != endGoAway+":NO_ERROR" {
			t.Errorf("error = %v, want %q: the code is the transport's, and the debug beside it is not ours to log",
				line["error"], endGoAway+":NO_ERROR")
		}
		all := h.logs.String()
		for _, bad := range append(secrets(h, t), providerKey, testKey, peerMarker) {
			if bad != "" && strings.Contains(all, bad) {
				t.Errorf("the log carries %q: %s", bad, raw)
			}
		}
	}

	t.Run("the provider route", func(t *testing.T) {
		_, psrv := anthropicfake.New(t, anthropicfake.Compat{OmitCacheFields: true}.StreamOK(dsModel, pricing.Usage{Input: 2000, Output: 5}))
		h := newHarnessWith(t, func(o *Options) {
			o.Client = quoting(o)
			o.Routes = []Route{{
				Name: "openrouter", Models: dsRoute, BaseURL: psrv.URL + "/api", Auth: "bearer",
				Credential: func() (string, error) { return providerKey, nil },
			}}
		})
		h.gw.BeginStage(routedStage)
		if resp, b := h.post(msg(dsModel, 1000, `"stream":true`)); resp.StatusCode != http.StatusOK {
			t.Fatalf("%d %s", resp.StatusCode, b)
		}
		check(t, h)
	})

	t.Run("the service route", func(t *testing.T) {
		h, _ := newServiceStub(t, func(*http.Request, []byte) anthropicfake.Reply {
			rep := anthropicfake.StreamOK(sonnet, pricing.Usage{Input: 2000, Output: 5})
			rep.Header = http.Header{HeaderTier: []string{TierA}, HeaderCredential: []string{"repo-a"}}
			return rep
		}, func(o *Options) { o.Client = quoting(o) })
		if resp, b := h.post(msg(sonnet, 1000, `"stream":true`)); resp.StatusCode != http.StatusOK {
			t.Fatalf("%d %s", resp.StatusCode, b)
		}
		check(t, h)
	})
}

// streamEndError is the whole guard between a transport's words and a log
// line, so it is pinned on its own, value by value: the failure is named
// from the closed set, and nothing the transport or the peer wrote survives
// into it -- a GOAWAY's debug data, which is the peer's text, least of all.
func TestStreamEndErrorIsAClosedSet(t *testing.T) {
	// A GOAWAY's message is the case that matters: Go prints the peer's own
	// debug data into it, verbatim and up to the peer's own length, so the
	// debug here is what a provider could try to say in this line.
	goAway := errors.New("http2: server sent GOAWAY and closed the connection; LastStreamID=7, " +
		`ErrCode=ENHANCE_YOUR_CALM, debug="` + providerKey + ` says ` + agentSecret + `"`)
	cases := []struct {
		name string
		end  pumpEnd
		want string
	}{
		{"a body that simply ended", pumpEnd{eof: true}, ""},
		{"the body ended mid-message", pumpEnd{readErr: true, err: io.ErrUnexpectedEOF}, endUnexpectedEOF},
		{"the call was cancelled", pumpEnd{readErr: true, err: context.Canceled}, endCanceled},
		{"its clock ran out", pumpEnd{readErr: true, err: context.DeadlineExceeded}, endDeadline},
		{"the peer's end is gone", pumpEnd{writeErr: true, err: syscall.EPIPE}, endReset},
		{"a GOAWAY keeps its code and drops its debug", pumpEnd{readErr: true, err: goAway}, endGoAway + ":ENHANCE_YOUR_CALM"},
		{"a GOAWAY with no code the transport knows", pumpEnd{readErr: true, err: errors.New("http2: server sent GOAWAY and closed the connection; LastStreamID=1, ErrCode=unknown error code 0x1f, debug=\"\"")}, endGoAway},
		// A server that shuts down sends the frame while the stream is in
		// flight, and the transport then fails the read with one of these two
		// messages instead: same peer, same act, so the same name.
		{"the transport's graceful shutdown GOAWAY", pumpEnd{readErr: true, err: errors.New("http2: Transport received Server's graceful shutdown GOAWAY")}, endGoAway},
		{"a GOAWAY the transport aborted the stream with", pumpEnd{readErr: true, err: errors.New("http2: Transport received GOAWAY from server ErrCode:PROTOCOL_ERROR")}, endGoAway + ":PROTOCOL_ERROR"},
		{"a stream error and its code", pumpEnd{readErr: true, err: errors.New("stream error: stream ID 5; REFUSED_STREAM")}, endStreamError + ":REFUSED_STREAM"},
		{"a stream error whose cause is the peer's text", pumpEnd{readErr: true, err: errors.New("stream error: stream ID 5; PROTOCOL_ERROR; " + providerKey)}, endStreamError + ":PROTOCOL_ERROR"},
		{"a write nobody named", pumpEnd{writeErr: true, err: errors.New("write: " + providerKey)}, endWriteFailed},
		{"anything else", pumpEnd{readErr: true, err: errors.New("something " + providerKey)}, endOther},
	}
	for _, c := range cases {
		got := streamEndError(c.end)
		if got != c.want {
			t.Errorf("%s: streamEndError = %q, want %q", c.name, got, c.want)
		}
		for _, bad := range []string{providerKey, agentSecret, testKey, "debug"} {
			if strings.Contains(got, bad) {
				t.Errorf("%s: streamEndError kept %q: %s", c.name, bad, got)
			}
		}
	}
}
