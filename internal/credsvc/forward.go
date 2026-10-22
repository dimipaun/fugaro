package credsvc

// The upstream call (C1.11): one attempt, built from C1.8's exported helpers,
// with the bounds and the refusals this service adds.
//
// Two things here are the service's own and not the gateway's, because the
// gateway never has them: the request body is held whole (an attempt that
// consumed it could not be repeated), and an answer that has begun to stream
// is scanned for the upstream's own usage-limit error, which arrives inside a
// 200 stream and not as a status.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway"
)

// upstreamTimeout bounds the whole attempt, headers and body. It is generous
// because a Messages stream is long by design (the SDK's own read timeout is
// minutes) and it exists only so that a connection nothing ever answers is
// released rather than held for the life of the instance.
const upstreamTimeout = 30 * time.Minute

// upstreamClient is the client every upstream attempt is made with, whether it
// was built here or handed to NewServer: it never follows a redirect and never
// keeps a cookie jar.
//
// A redirect is the one answer that would take the credential somewhere nobody
// chose. A 307 or 308 re-sends the request — body and headers — to whatever
// host the Location names, and Go's own client drops Authorization across hosts
// but not x-api-key, so a followed redirect is a tier-B paid key (or, on the
// tier-A path, the subscription token D133 keeps to Anthropic's own endpoint)
// sent to a host the policy never admitted. The answer itself is still the
// client's (a 3xx is neither a usage limit nor a 401), so it is relayed — with
// no credential in it — and this service simply does not go where it points.
//
// It is internal/gateway's own upstreamClient (gateway.go), respelled here for
// the reason carriesAny is: the package does not export it. The compression
// half is the same rule for the same reason — the transport must not ask for an
// encoding on its own, because an answer the service relays is an answer it has
// to be able to read (the usage-limit scan) and to scrub.
func upstreamClient(c *http.Client) *http.Client {
	var out http.Client
	if c != nil {
		out = *c
	} else {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.DisableCompression = true
		out.Transport = t
		out.Timeout = upstreamTimeout
	}
	out.Jar = nil
	out.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &out
}

// upstreamKindAnthropic is gateway.Upstream's kind for Anthropic's own API
// with a key: the tier-B path. It is spelled out here rather than taken from
// the package because internal/gateway exports no constant for it — the one it
// does export is the subscription kind, and this package must never name that
// one: the test that keeps the subscription path to a single file refuses any
// file outside its list that mentions it.
const upstreamKindAnthropic = "anthropic"

// headerPrefixFugaro is the prefix of this contract's own headers. Every one
// of them is dropped before a call goes upstream: they carry the run's
// identity token and the job it claims, and Anthropic has no use for either.
// The job token in Authorization is already on the gateway's deny-list; these
// two are not, which is why they are dropped here by prefix rather than by
// name — a header this contract gains later is dropped by this line too.
const headerPrefixFugaro = "X-Fugaro-"

// redactedValue is what a scrubbed value is replaced with in a body. It is the
// gateway's own marker over an answer (route.go's `redacted`), spelled here
// because the package does not export it; the two are the same string and a
// body scrubbed by either reads the same way.
const redactedValue = "[redacted]"

// call is one call being answered: what it asks for, and its body read once so
// that an attempt can be repeated.
type call struct {
	path   string
	query  string
	body   []byte
	header http.Header
	// requested is whether the client asked for an event stream. It is the
	// gateway's own fallback (IsSSE) for an answer whose content type says
	// nothing, and it is read from the request the client built.
	requested bool
	// scrubs are the caller's own tokens: the run identity this service
	// verified, and the job's ID token it read from Authorization. Neither is
	// ever sent upstream, and the query of a call is dropped whole when it
	// carries either. They live for the length of one call and are written
	// nowhere.
	scrubs []string
	// probed is every tier this call made a probe on (C1.12), in the order
	// the probes were made and once per tier: what the answer carries to the
	// run (HeaderProbe), which settles each probe by it — notional on tier A,
	// money on any other tier (C1.16, the plan's rule for a probe). It is the
	// ledger input only: the names a probe was made on are the service's own
	// record (C1.13's audit line) and are not carried here.
	probed []string
}

// readCall reads the call the service was handed, and refuses a body larger
// than the gateway's own bound: the two sit in one path, so a body one of them
// accepts and the other refuses would be a request that works against one and
// not the other.
//
// The caller's own tokens — the run identity this service verified and the
// job's ID token it read from Authorization — are scrubbed out of the BODY
// here, and out of the headers before the call goes upstream (send). A token in
// a header other than Authorization, or pasted into the request body, is a
// token in Anthropic's logs: the gateway's own deny-list covers the one header
// it knows, and a rule that covered only that header would leave every other
// one, and the body, carrying it.
func readCall(r *http.Request, scrubs []string) (*call, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, gateway.MaxRequestBytes+1))
	if err != nil {
		return nil, errors.New("credsvc: the call's body could not be read")
	}
	if int64(len(body)) > gateway.MaxRequestBytes {
		return nil, fmt.Errorf("credsvc: the call's body is larger than the %d bytes this service accepts", int64(gateway.MaxRequestBytes))
	}
	for _, tok := range scrubs {
		if tok == "" {
			continue
		}
		// Whole bytes only: a token split by a JSON escape or re-encoded
		// character is not a token this can find, and a body edit that tried
		// would be guessing at the caller's encoding.
		body = bytes.ReplaceAll(body, []byte(tok), []byte(redactedValue))
	}
	h := r.Header.Clone()
	for k := range h {
		if strings.HasPrefix(http.CanonicalHeaderKey(k), headerPrefixFugaro) {
			delete(h, k)
		}
	}
	return &call{
		path:      r.URL.Path,
		query:     r.URL.RawQuery,
		body:      body,
		header:    h,
		requested: strings.Contains(r.Header.Get("Accept"), "text/event-stream"),
		scrubs:    scrubs,
	}, nil
}

// answer is one attempt's outcome: the upstream's response, and the credential
// it carried. The caller must close the body.
type answer struct {
	resp *http.Response
	// credential is the value that was sent. It is what the answer is redacted
	// with before it reaches the client, and it is never written anywhere else.
	credential string
}

// send makes one upstream call with one credential, using C1.8's helpers to
// build the headers: the subscription path for a tier-A credential (whose
// destination rule, D133, is inside OAuthHeaders and is therefore a check that
// runs and not a comment), the key path for a tier-B one.
//
// The caller's own tokens are never sent upstream: the job token in
// Authorization is dropped by the gateway's own deny-list, the run identity by
// the scrub below, and this contract's headers when the call was read.
func (s *Server) send(ctx context.Context, cl *call, e Entry) (*answer, error) {
	dest, err := s.destination(cl)
	if err != nil {
		return nil, err
	}
	scrub := ""
	if len(cl.scrubs) > 0 {
		scrub = cl.scrubs[0]
	}
	up := gateway.Upstream{Kind: upstreamKindAnthropic, APIKey: e.Value()}
	var hdr http.Header
	var key string
	if e.Tier == creds.TierA {
		// Tier A: the subscription upstream, whose destination check lives in
		// the helper. BaseURL is "" — Anthropic's real address — and the check
		// runs on it, on the URL this call really goes to.
		up = gateway.Upstream{APIKey: e.Value()}
		hdr, key, err = gateway.OAuthHeaders(up, dest, cl.header, scrub)
	} else {
		hdr, key, err = gateway.UpstreamHeaders(up, cl.header, scrub)
	}
	if err != nil {
		// The message names the rule, never a value: every error these two
		// helpers return is written that way (C1.8).
		return nil, err
	}
	// Every header that carries one of the caller's own tokens is dropped, not
	// only the Authorization header the gateway's deny-list knows: the scrub
	// above is over the one header, and a token in any other header of the call
	// is the same token reaching Anthropic's logs. gateway.RedactHeaders is the
	// gateway's own rule over a header set, exported for exactly this (C1.8),
	// and it deletes the header rather than editing its value, which is the
	// direction that cannot half-remove a token.
	for _, tok := range cl.scrubs {
		gateway.RedactHeaders(hdr, tok)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, dest, bytes.NewReader(cl.body))
	if err != nil {
		return nil, errors.New("credsvc: the upstream request could not be built")
	}
	req.Header = hdr
	req.ContentLength = int64(len(cl.body))
	resp, err := s.cfg.Client.Do(req)
	if err != nil {
		// The transport's own error is not carried: it quotes the URL, and a
		// URL of this call is the upstream's, which carries no credential —
		// but it also carries a request id and a host, and the rule here is
		// that the service's refusals are its own sentences.
		return nil, errors.New("credsvc: the upstream could not be reached")
	}
	return &answer{resp: resp, credential: key}, nil
}

// destination is where an attempt goes: the upstream's own base, the call's
// path, and the call's query unless that query carries one of the caller's own
// tokens.
//
// The query is dropped whole rather than edited, and it is dropped when it
// carries either token the caller presented — the run identity or the job's
// ID token — in any case or percent-encoded. That is the gateway's own rule
// over its own token (upstreamQuery, carriesToken), which C1.8's helpers do
// not cover and which this service therefore has to: a token that reached the
// query of a call to Anthropic is a token in Anthropic's logs.
func (s *Server) destination(cl *call) (string, error) {
	base := gateway.AnthropicBase(gateway.Upstream{})
	if base == "" {
		return "", errors.New("credsvc: the upstream has no address")
	}
	u := base + cl.path
	if cl.query != "" && !carriesAny(cl.query, cl.scrubs) {
		u += "?" + cl.query
	}
	return u, nil
}

// carriesAny reports whether v holds any of the tokens, in any case or
// percent-encoded, with a bad escape left as it is so it cannot hide one. It
// is internal/gateway's carriesToken over a list, respelled because the
// package does not export it.
func carriesAny(v string, tokens []string) bool {
	lv := strings.ToLower(v)
	lu := strings.ToLower(lenientUnescape(lv))
	for _, tok := range tokens {
		if tok == "" {
			continue
		}
		t := strings.ToLower(tok)
		if strings.Contains(lv, t) || strings.Contains(lu, t) {
			return true
		}
	}
	return false
}

// lenientUnescape decodes every valid %XX (and "+") and keeps the rest. It is
// internal/gateway's own function over the same input, respelled here for the
// reason carriesAny is.
func lenientUnescape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]):
			b.WriteByte(unhex(s[i+1])<<4 | unhex(s[i+2]))
			i += 2
		case c == '+':
			b.WriteByte(' ')
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case c >= 'a':
		return c - 'a' + 10
	case c >= 'A':
		return c - 'A' + 10
	}
	return c - '0'
}

// A usage-limit answer, in the two shapes this release can see.
//
// U3 is open: Check 90 (2026-10-21) observed no usage limit and provoked none,
// so which status, error type and headers carry one for subscription traffic is
// still unknown. What is known is where to look, and F15 says it plainly:
// detection is done on the HTTP response — the status, the error type and the
// reset headers — and never by parsing Claude Code's text.
//
// The two shapes:
//
//   - a 429 status. It is decisive on its own: the status is the authority and
//     the body is a hint, so the body is not read at all (reading it would mean
//     holding an answer this service must stream). The reset comes from the
//     headers, and a 429 that names none is the backed-off case C1.9 already
//     handles.
//   - a 2xx whose *stream* carries an error event naming rate_limit_error.
//     This is the shape a streaming call meets, and it is why the qualifier
//     "only before the first byte" in the retry rule is a real case rather than
//     a dead one: the answer has already begun, so the credential is marked and
//     the call is NOT retried.
//
// Neither shape reads a credential's value, and neither carries one into a
// mark: a mark is a state and a time.
const rateLimitEventType = "rate_limit_error"

// usageLimitStatus reports whether an answer's status is a usage limit.
func usageLimitStatus(status int) bool { return status == http.StatusTooManyRequests }

// usageLimitScanner looks for a usage-limit error event in a streamed answer.
// It is bounded: it keeps only the last usageLimitTail bytes, so a stream of
// any length is scanned with a constant amount of memory, and it keeps them
// because an event can straddle two chunks.
type usageLimitScanner struct {
	tail []byte
	seen bool
}

// usageLimitTail is how much of the stream is held back to join to the next
// chunk. It is far larger than one error event (a few hundred bytes) and small
// enough to be irrelevant beside a stream's buffer.
const usageLimitTail = 4 << 10

// rateLimitEvent is the error event, in the two spellings a JSON encoder
// produces for it: compact, which is what Anthropic's own stream uses, and
// with a space after the colon.
var rateLimitEvents = [][]byte{
	[]byte(`"type":"` + rateLimitEventType + `"`),
	[]byte(`"type": "` + rateLimitEventType + `"`),
}

// scan feeds one chunk of the streamed answer to the scanner. It never fails
// and never reports: what it learns is read after the stream ends (Found), so
// that nothing it does can slow the answer down or change it.
func (u *usageLimitScanner) scan(chunk []byte) {
	if u.seen {
		return
	}
	hay := make([]byte, 0, len(u.tail)+len(chunk))
	hay = append(hay, u.tail...)
	hay = append(hay, chunk...)
	for _, ev := range rateLimitEvents {
		if bytes.Contains(hay, ev) {
			u.seen = true
			break
		}
	}
	if len(hay) > usageLimitTail {
		hay = hay[len(hay)-usageLimitTail:]
	}
	u.tail = append(u.tail[:0], hay...)
}

// Found reports whether a usage-limit event was seen in the stream.
func (u *usageLimitScanner) Found() bool { return u.seen }

// usageLimitReset reads the reset a usage-limit answer named, and the zero time
// when it named none — which is the backed-off case (C1.9's Exhaust).
//
// The headers are the ones a rate-limited HTTP answer carries: Retry-After
// (delta-seconds, or an HTTP date), and Anthropic's own unified rate-limit
// resets, which are RFC 3339 instants. Which of them carries a *subscription*
// usage limit is U3's open question, so this reads all of them and takes the
// LATEST: a credential held for longer than it needed to be costs a wait, and
// the wait-or-switch rule is exactly what a wait is for, while a credential
// released before the upstream was ready to serve it costs another 429 and,
// under D142's first control, the whole window. Holding is the fail-closed
// direction, and a hold may only ever be extended (C1.9's Exhaust).
//
// The reset headers are read from the answer, never from the body: the body of
// a streamed answer is on its way to the client.
func usageLimitReset(h http.Header, now time.Time) time.Time {
	var out time.Time
	keep := func(t time.Time) {
		if t.After(now) && t.After(out) {
			out = t
		}
	}
	if v := h.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs > 0 {
			keep(now.Add(time.Duration(secs) * time.Second))
		} else if t, err := http.ParseTime(v); err == nil {
			keep(t)
		}
	}
	for _, name := range []string{
		"Anthropic-Ratelimit-Unified-5h-Reset",
		"Anthropic-Ratelimit-Unified-7d-Reset",
	} {
		if v := h.Get(name); v != "" {
			if t, err := time.Parse(time.RFC3339, strings.TrimSpace(v)); err == nil {
				keep(t)
			}
		}
	}
	return out
}

// unauthorizedStatus reports whether an answer says the credential is dead.
// Only 401 is D139's mark: a 403 is Anthropic refusing a request (a model the
// key may not use, a region it may not serve), and marking a credential broken
// for it would take a working token out of rotation until an operator cleared
// it.
func unauthorizedStatus(status int) bool { return status == http.StatusUnauthorized }

// answerIsStream reports whether an upstream answer is an event stream, by its
// media type, falling back to what the client asked for (C1.8's IsSSE, which
// is the gateway's own rule).
func answerIsStream(resp *http.Response, requested bool) bool {
	return gateway.IsSSE(resp.Header, requested)
}

// dropAnswerHeaders are the headers of an upstream answer that never reach the
// client. They are internal/gateway's own lists (hopByHop,
// dropResponseHeaders), respelled here because the package exports neither:
// they identify the organization behind the account, set state on this
// service's origin, or describe a hop the client is not part of.
var dropAnswerHeaders = map[string]bool{
	"Connection": true, "Keep-Alive": true, "Proxy-Connection": true, "Proxy-Authenticate": true,
	"Te": true, "Trailer": true, "Transfer-Encoding": true, "Upgrade": true,
	"Anthropic-Organization-Id": true, "Set-Cookie": true, "Set-Cookie2": true,
	"Content-Length": true,
}

// copyAnswerHeaders copies an upstream answer's headers to the client's, less
// the ones above. The credential is redacted out of what is copied
// (gateway.RedactHeaders) because an upstream that echoed the caller's token
// back — in a header, an error, a request id — must not have it echoed on to
// the client: the no-echo rule is over the whole answer and not only the body.
func copyAnswerHeaders(dst, src http.Header, credential string) {
	for k, vs := range src {
		ck := http.CanonicalHeaderKey(k)
		if dropAnswerHeaders[ck] {
			continue
		}
		dst[ck] = append([]string(nil), vs...)
	}
	if credential != "" {
		gateway.RedactHeaders(dst, credential)
	}
}
