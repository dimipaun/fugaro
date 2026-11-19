package voicesvc

// The streaming half's tests: the answer reaches the caller as it arrives, the
// credential this service presented never appears in it, and the copy ends in
// one of the two ways the log line names.
//
// The two properties the plan asks for are here rather than only in the
// forwarding test, because both are properties of this copy loop: nothing is
// buffered (a fixed buffer, flushed after every write), and nothing is retried
// (there is no second read of a failed source anywhere in this file).

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestRedactWriterReplacesTheKey: a credential a provider echoes — in an error
// body, in a JSON field — must not reach the phone through this service, and it
// must not reach it split in two either, which is the failure a naive
// bytes.Replace on each write has.
func TestRedactWriterReplacesTheKey(t *testing.T) {
	const key = "the-key-the-call-presented"
	for _, tc := range []struct {
		name   string
		chunks []string
		want   string
	}{
		{"the key alone", []string{key}, redacted},
		{"the key inside a sentence", []string{"a " + key + " b"}, "a " + redacted + " b"},
		{"twice in one write", []string{key + " " + key}, redacted + " " + redacted},
		{"nothing to replace", []string{"the answer"}, "the answer"},
		// The split is the case the hold-back exists for, and each of these
		// puts the boundary somewhere different: after the first byte, in the
		// middle, and after the last.
		{"split after one byte", []string{key[:1], key[1:] + " tail"}, redacted + " tail"},
		{"split in the middle", []string{"a" + key[:7], key[7:] + "b"}, "a" + redacted + "b"},
		{"split at the last byte", []string{"x" + key[:len(key)-1], key[len(key)-1:] + "y"}, "x" + redacted + "y"},
		{"split into three", []string{key[:3], key[3:9], key[9:]}, redacted},
		{"the key at the end of one write", []string{"tail " + key, ""}, "tail " + redacted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			rw := newRedactWriter(&out, key)
			for _, c := range tc.chunks {
				if _, err := rw.Write([]byte(c)); err != nil {
					t.Fatalf("writing %q: %v", c, err)
				}
			}
			if err := rw.Close(); err != nil {
				t.Fatalf("closing: %v", err)
			}
			if got := out.String(); got != tc.want {
				t.Errorf("the answer is %q, want %q", got, tc.want)
			}
			if strings.Contains(out.String(), key) {
				t.Errorf("the credential survived the copy: %q", out.String())
			}
		})
	}
}

// TestRedactWriterHoldsBackOnlyWhatCouldBeAKey: the hold-back is a delay on the
// stream, so it must be at most a key's length and must not grow. An answer that
// is not the key arrives whole, and the writer's buffer never holds more than
// the key's length minus one byte of it.
func TestRedactWriterHoldsBackOnlyWhatCouldBeAKey(t *testing.T) {
	const key = "abcdefgh"
	var out bytes.Buffer
	rw := newRedactWriter(&out, key)
	for i := 0; i < 100; i++ {
		if _, err := rw.Write([]byte("z")); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		if len(rw.hold) > len(key)-1 {
			t.Fatalf("after %d writes the writer holds %d bytes back: the hold is a key's length at most, or the stream is delayed by a buffer", i, len(rw.hold))
		}
	}
	if err := rw.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}
	if got := out.String(); got != strings.Repeat("z", 100) {
		t.Errorf("the answer is %q, want the hundred bytes as they were sent", got)
	}
}

// TestRedactWriterWithNoKeysIsATransparentCopy: the scrub costs nothing when the
// call presented something else — the engine reached as the installation's own
// identity presents a token this service minted, and a copy that held bytes back
// for a key it does not have would delay a stream for nothing.
func TestRedactWriterWithNoKeysIsATransparentCopy(t *testing.T) {
	var out bytes.Buffer
	rw := newRedactWriter(&out, "")
	if _, err := rw.Write([]byte("the answer")); err != nil {
		t.Fatalf("writing: %v", err)
	}
	if len(rw.hold) != 0 {
		t.Errorf("the writer held %d bytes back with no key to look for", len(rw.hold))
	}
	if err := rw.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}
	if out.String() != "the answer" {
		t.Errorf("the answer is %q", out.String())
	}
}

// flushRecorder is a ResponseWriter that records the ORDER of writes and flushes
// through it, so a test can assert that every write is followed by a flush and
// nothing else is. failAfter makes the write after the nth fail, as a caller
// that went away mid-answer does.
type flushRecorder struct {
	*httptest.ResponseRecorder
	trace     []string
	writes    int
	failAfter int
}

func (f *flushRecorder) Write(p []byte) (int, error) {
	f.trace = append(f.trace, fmt.Sprintf("w%d", len(p)))
	f.writes++
	if f.failAfter > 0 && f.writes > f.failAfter {
		return 0, errors.New("the caller went away")
	}
	return f.ResponseRecorder.Write(p)
}

// FlushError is what http.ResponseController looks for first, so a flush through
// it is recorded here.
func (f *flushRecorder) FlushError() error {
	f.trace = append(f.trace, "f")
	return nil
}

// TestStreamAnswerFlushesEveryChunk: "the audio is forwarded as it arrives" is a
// property of this loop, and the flush is what makes it true of the socket. A
// chunk that sat in this service's write buffer would be a phone waiting for the
// end of the turn instead of hearing it.
func TestStreamAnswerFlushesEveryChunk(t *testing.T) {
	w := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	rc := http.NewResponseController(w)
	src := &chunks{parts: []string{"one", "two", "three"}}
	end, n := streamAnswer(w, rc, src)
	if !end.eof {
		t.Errorf("the copy ended as %+v, want the provider's own end of the body", end)
	}
	if n != 11 {
		t.Errorf("the copy counted %d bytes, want 11", n)
	}
	if got := w.Body.String(); got != "onetwothree" {
		t.Errorf("the caller was sent %q", got)
	}
	want := []string{"w3", "f", "w3", "f", "w5", "f"}
	if strings.Join(w.trace, ",") != strings.Join(want, ",") {
		t.Errorf("the copy did %v, want each write flushed before the next read (%v): a chunk must not wait for the rest of the answer", w.trace, want)
	}
}

// TestStreamAnswerHoldsNothing: the loop's buffer is fixed and reused, so the
// whole answer is never held. This is the same property as the trace above
// seen from the source's side: the source is read again before the answer is
// complete, which is what streaming means.
func TestStreamAnswerHoldsNothing(t *testing.T) {
	src := &chunks{parts: []string{"1", "2", "3"}}
	w := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	end, n := streamAnswer(w, http.NewResponseController(w), src)
	if !end.eof || n != 3 {
		t.Fatalf("the copy ended as %+v with %d bytes", end, n)
	}
	if src.reads != 4 { // three parts and the EOF
		t.Errorf("the source was read %d times, want 4: the copy reads as it writes, so a whole answer is never held", src.reads)
	}
}

// TestStreamAnswerEndsAsItEnded: the three endings, and nothing else. The
// distinction is what the handler answers with — a provider that failed
// mid-answer aborts the response, because half a sentence played as the whole
// reply is worse than a call the app answers locally.
func TestStreamAnswerEndsAsItEnded(t *testing.T) {
	t.Run("the provider's body fails mid-answer", func(t *testing.T) {
		w := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
		src := &chunks{parts: []string{"half"}, err: errors.New("the connection reset")}
		end, n := streamAnswer(w, http.NewResponseController(w), src)
		if !end.readErr || end.eof || end.writeErr {
			t.Errorf("the copy ended as %+v, want a read failure", end)
		}
		if n != 4 || w.Body.String() != "half" {
			t.Errorf("the caller was sent %d bytes (%q), want the four that arrived", n, w.Body.String())
		}
	})
	t.Run("the caller goes away", func(t *testing.T) {
		w := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
		w.failAfter = 1
		src := &chunks{parts: []string{"one", "two"}}
		end, n := streamAnswer(w, http.NewResponseController(w), src)
		if !end.writeErr || end.eof || end.readErr {
			t.Errorf("the copy ended as %+v, want a write failure", end)
		}
		if n != 3 {
			t.Errorf("the copy counted %d bytes, want the 3 written before the caller went away", n)
		}
	})
}

// TestStreamAnswerDoesNotRetry: §4.5's row — nothing is retried against a second
// provider, and nothing is retried against the first. A source that fails once
// is read no further: a second read of a failed stream is a second request to a
// provider that has already answered.
func TestStreamAnswerDoesNotRetry(t *testing.T) {
	src := &chunks{parts: []string{"one"}, err: errors.New("the stream broke")}
	w := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	end, _ := streamAnswer(w, http.NewResponseController(w), src)
	if !end.readErr {
		t.Fatalf("the copy ended as %+v", end)
	}
	if src.reads != 2 { // the part and the failure
		t.Errorf("the source was read %d times, want 2: a failed read is not retried against a provider that has already answered", src.reads)
	}
}

// TestCopyAnswerHeadersAllowsOnlyTheMediaType: what the phone needs is the media
// type, because that is what says whether the bytes it is about to read are MP3,
// OGG or JSON. Everything else stays here: a provider's answer headers identify
// the account behind the credential.
func TestCopyAnswerHeadersAllowsOnlyTheMediaType(t *testing.T) {
	src := http.Header{
		"Content-Type":      {"audio/mpeg"},
		"X-Request-Id":      {"abc123"},
		"Set-Cookie":        {"session=1"},
		"X-Dg-Trace":        {"a-region-and-an-account"},
		"Content-Length":    {"12345"},
		"Transfer-Encoding": {"chunked"},
		"X-Ratelimit-Left":  {"42"},
	}
	dst := http.Header{}
	copyAnswerHeaders(dst, src)
	if got := dst.Get("Content-Type"); got != "audio/mpeg" {
		t.Errorf("the media type is %q, want audio/mpeg", got)
	}
	if len(dst) != 1 {
		t.Errorf("the caller was sent %d headers (%v), want only the media type: the rest identify the account behind the credential", len(dst), dst)
	}
	for _, h := range []string{"X-Request-Id", "Set-Cookie", "X-Dg-Trace", "Content-Length", "Transfer-Encoding", "X-Ratelimit-Left"} {
		if dst.Get(h) != "" {
			t.Errorf("the header %s reached the caller: %q", h, dst.Get(h))
		}
	}
	// The allow-list itself, so a later release cannot widen it by adding a
	// header to the map without this test moving too.
	if len(answerHeaders) != 1 || !answerHeaders["Content-Type"] {
		t.Errorf("the answer allow-list is %v, want Content-Type alone", answerHeaders)
	}
}

// TestCopyAnswerHeadersDoesNotAliasTheProvidersHeader: the allow-listed values
// are copied and not shared, so a later edit of one cannot reach the other.
func TestCopyAnswerHeadersDoesNotAliasTheProvidersHeader(t *testing.T) {
	src := http.Header{"Content-Type": {"audio/mpeg"}}
	dst := http.Header{}
	copyAnswerHeaders(dst, src)
	dst.Set("Content-Type", "audio/ogg")
	if got := src.Get("Content-Type"); got != "audio/mpeg" {
		t.Errorf("the provider's own header was edited through the copy: %q", got)
	}
}

// chunks is a source that hands out one part per read and then ends, and counts
// its reads. It is what makes "read as it is written" observable: the count is
// how many times this service asked the provider for more.
type chunks struct {
	parts []string
	err   error
	i     int
	reads int
}

func (c *chunks) Read(p []byte) (int, error) {
	c.reads++
	if c.i >= len(c.parts) {
		if c.err != nil {
			err := c.err
			c.err = nil
			return 0, err
		}
		return 0, io.EOF
	}
	n := copy(p, c.parts[c.i])
	c.i++
	return n, nil
}

// gated is a provider's body that will not produce its second chunk until the
// test says so. It is how "as it arrives" is observed over a real socket: the
// client cannot read the second chunk before the first one reached it, and the
// provider cannot send the second one before that either.
type gated struct {
	first, second []byte
	release       chan struct{}
	n             int
}

func (g *gated) Read(p []byte) (int, error) {
	switch g.n {
	case 0:
		g.n++
		return copy(p, g.first), nil
	case 1:
		select {
		case <-g.release:
		case <-time.After(30 * time.Second):
			// Not a flake guard but a way out: without the flush the first
			// chunk never reaches the client, the client never releases the
			// second one, and the failure above is the only way this test
			// reports anything at all.
			return 0, errors.New("the test never released the provider's second chunk")
		}
		g.n++
		return copy(p, g.second), nil
	default:
		return 0, io.EOF
	}
}

// TestStreamAnswerReachesTheCallerAsItArrives is the chunk-and-flush property
// over a real socket, which is where it is a property at all: the handler writes
// the provider's first chunk, the client reads it, and only then does the test
// let the provider produce the rest. Without the flush the first chunk would sit
// in net/http's own write buffer while the phone waited for the end of the turn
// (D46's end-of-turn criterion).
func TestStreamAnswerReachesTheCallerAsItArrives(t *testing.T) {
	first := []byte("the first sentence of the answer")
	src := &gated{first: first, second: []byte(" and the rest of it"), release: make(chan struct{})}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		streamAnswer(w, http.NewResponseController(w), src)
	}))
	defer srv.Close()
	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("the call could not be made: %v", err)
	}
	defer resp.Body.Close()
	got := make([]byte, len(first))
	done := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(resp.Body, got)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the first chunk could not be read: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the first chunk did not reach the caller while the provider still held the rest of the answer: the copy is buffering, so a phone hears a turn only once the provider has finished writing it")
	}
	if !bytes.Equal(got, first) {
		t.Errorf("the first chunk arrived as %q, want %q", got, first)
	}
	close(src.release)
	rest, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("the rest of the answer could not be read: %v", err)
	}
	if string(rest) != string(src.second) {
		t.Errorf("the rest of the answer is %q, want %q", rest, src.second)
	}
}
