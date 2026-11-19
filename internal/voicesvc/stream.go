package voicesvc

// The streaming half: the answer goes to the caller as it arrives, and the
// credential this service presented is scrubbed back out of it on the way
// (design §4.5's "nothing is retried against a second provider" row, §8;
// mobile plan Task P5.3.5).
//
// One property is the whole of the design here, and it is the plan's own
// acceptance line: **nothing is buffered, spooled, retried or written to
// disk.** The audio direction forwards the caller's body to the provider as the
// provider reads it (provider.go hands the request's own body to the
// transport), and the answer is copied out in a fixed 32 KiB buffer that is
// flushed after every write — so a spoken sentence reaches the phone while the
// rest of the answer is still being generated, and no figure of this service's
// memory ever holds a whole clip. There is no temporary file anywhere in this
// package, and no second attempt: a call goes to one engine once.
//
// The credential scrub is the second property. §4.4 point 4 is that the key
// never leaves this service, and a key that a provider echoes — in an error
// body, in a JSON field, in a header — would leave it through this very socket
// if the answer were copied verbatim. So every byte written to the caller goes
// through a redactor that holds the credential this call presented, exactly as
// internal/gateway's provider path does for the keys it sends.

import (
	"bytes"
	"io"
	"net/http"
)

// redacted is what a credential is replaced with. It is
// internal/gateway's spelling of the same fact, so a log or a transcript of
// either service reads the same way.
const redacted = "[redacted]"

// redactWriter replaces every occurrence of every key in what is written
// through it, also when a chunk boundary splits one. It holds back only a tail
// that could still become one of the keys, so a stream is delayed by nothing
// else.
//
// It is a copy of internal/gateway's redactWriter, and the copy is deliberate
// and is the one place this package duplicates another's code: the gateway's is
// unexported, the alternative is widening that package's surface for a second
// caller that is not a gateway, and the behaviour is small enough to hold in
// one file with a test of its own. What the two must agree on is the semantics
// and only the semantics — a key split across two writes is still replaced, and
// a stream is never held back by more than a key's length.
type redactWriter struct {
	w    io.Writer
	keys [][]byte
	hold []byte
}

func newRedactWriter(w io.Writer, keys ...string) *redactWriter {
	r := &redactWriter{w: w}
	for _, k := range keys {
		if k != "" {
			r.keys = append(r.keys, []byte(k))
		}
	}
	return r
}

func (r *redactWriter) Write(p []byte) (int, error) {
	if len(r.keys) == 0 {
		return r.w.Write(p)
	}
	buf := append(r.hold, p...)
	r.hold = nil
	for _, k := range r.keys {
		buf = bytes.ReplaceAll(buf, k, []byte(redacted))
	}
	// Keep the longest suffix that is a proper prefix of any key.
	keep := 0
	for _, k := range r.keys {
		for n := min(len(k)-1, len(buf)); n > keep; n-- {
			if bytes.HasSuffix(buf, k[:n]) {
				keep = n
				break
			}
		}
	}
	r.hold = append([]byte(nil), buf[len(buf)-keep:]...)
	if _, err := r.w.Write(buf[:len(buf)-keep]); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Close writes what was held back (it was not the key after all).
func (r *redactWriter) Close() error {
	if len(r.hold) == 0 {
		return nil
	}
	_, err := r.w.Write(r.hold)
	r.hold = nil
	return err
}

// streamEnd is how the copy of an answer ended: it reached the provider's own
// end of the body, or a read or a write failed. It says nothing more than that,
// because the two failures are answered the same way and are logged as a kind
// rather than as a sentence: the read's error is the transport's text and can
// carry words the peer chose.
type streamEnd struct {
	// eof is the provider's body ending where its sender meant it to.
	eof bool
	// readErr is the provider's body failing mid-answer.
	readErr bool
	// writeErr is the write to the caller failing: the phone went away, or the
	// flush did.
	writeErr bool
}

// streamAnswer copies the provider's answer to the caller, flushing after every
// write, and answers how the copy ended and how many bytes the caller was sent.
//
// The buffer is fixed and small and is reused: the whole answer is never held,
// which is what makes "nothing is buffered" a property of this loop rather than
// a promise about it. The flush after every write is the other half — a
// provider that sends a sentence's worth of audio at a time must not have it
// sit in this service's write buffer while the phone waits for the end of the
// turn (D46's end-of-turn criterion is about exactly this).
//
// count is the bytes written to dst, and it is a NUMBER and never content: it
// goes into the one log line (server.go's callLog) and is the only figure about
// the payload any line carries.
func streamAnswer(dst io.Writer, rc *http.ResponseController, src io.Reader) (streamEnd, int64) {
	buf := make([]byte, 32<<10)
	var count int64
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return streamEnd{writeErr: true}, count
			}
			count += int64(n)
			if werr := rc.Flush(); werr != nil {
				return streamEnd{writeErr: true}, count
			}
		}
		if err == io.EOF {
			return streamEnd{eof: true}, count
		}
		if err != nil {
			return streamEnd{readErr: true}, count
		}
	}
}

// answerHeaders are the provider's response headers the caller may see.
//
// It is an ALLOW-LIST of one, and everything else stays here. The reason is the
// one internal/gateway's provider path states for its own list: a provider's
// answer headers identify the account behind the credential (a request id, a
// region, a cookie), and the phone holds no business with any of them. What the
// caller does need is the media type, because that is what says whether the
// bytes it is about to read are MP3, OGG or JSON.
var answerHeaders = map[string]bool{"Content-Type": true}

// copyAnswerHeaders copies the allow-listed headers of the provider's answer.
func copyAnswerHeaders(dst, src http.Header) {
	for k, vs := range src {
		if answerHeaders[http.CanonicalHeaderKey(k)] {
			dst[http.CanonicalHeaderKey(k)] = append([]string(nil), vs...)
		}
	}
}
