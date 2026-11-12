// The reader of the endpoint a daemon serves: the local scope's own half of
// D1359 (CG13c of docs/cli-output-console-graph/implementation-plan-graph.md).
//
// It is the one thing in this package that holds the token and decides which
// document a reader draws, and those are the same decision: a registry file is
// a file on this machine that any process of this user could have written, so
// the only file this reader trusts is one a live endpoint proves (Probe's
// handshake, CG13b), and the only request it makes carries the credential in
// the one Authorization header it belongs in. The token is never in a URL,
// never in an error and never in a document: an error text or a log line built
// from one is the leak the whole design is written against (F23).
//
// A machine with no daemon has no run directory at all, and that is the
// ordinary answer -- Lookup's false, no error -- because the caller then reads
// the orchestrator's node and its cache file exactly as it did before the
// endpoint existed (T5(a), amended 2026-11-11).

package endpoint

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// maxDocument bounds one answer and one event -- the whole of an event, however
// many data lines it is joined from: a state document is JSON, and the peer that
// sends it is a local process whose length a reader must not trust with its own
// memory. It is far above a state's size and far below what would matter to the
// machine.
const maxDocument = 4 << 20

// refusalReason is the reason an endpoint refused with, as one line, and the
// sentence a peer that is not this endpoint gets: the answer is a document this
// package wrote (`{"error": ...}`, server.go's refuse), so it is decoded rather
// than printed, and anything else is refused without being repeated.
func refusalReason(body io.Reader) string {
	raw, err := io.ReadAll(io.LimitReader(body, maxDocument))
	if err != nil {
		return "the endpoint gave no reason"
	}
	var refusal struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &refusal); err != nil || refusal.Error == "" {
		return "the endpoint gave no reason this release reads"
	}
	return safetext.OneLine(refusal.Error)
}

// tickAt is the server time of the tick an answer came from (X-Fugaro-Tick):
// the endpoint's own stamp of the cycle that produced the document, and the age
// a scope draws (D1230). An answer without one is not this release's endpoint,
// and a document with no time beside it is refused rather than dated from this
// machine's clock.
func tickAt(h http.Header) (time.Time, error) {
	raw := h.Get(headerTick)
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("endpoint: the answer carries no tick this release reads: %q", safetext.OneLine(raw))
	}
	return at.UTC(), nil
}

// Client is a reader of the local endpoint, placed the way the registry is
// placed on this machine: the state directory localcfg.UserDir resolves from
// the environment and the platform.
type Client struct {
	// Getenv reads the environment the registry's directory is placed from, and
	// nil is this process's own (os.Getenv). It is a field because the run
	// directory is a per-user fact and a test may not read a developer's own
	// (localcfg.UserDir's rule).
	Getenv func(string) string
	// GOOS is the platform the state directory is placed by, and empty is
	// runtime.GOOS. It is a parameter for the reason UserDir takes one.
	GOOS string
	// HTTP is the client every request goes through, and nil is one with no
	// timeout of its own: every request here carries a context, which is what
	// bounds it, and a timeout on the client would end the stream at it -- the
	// stream is as long as its caller's context and nothing else. The handshake
	// is the one request whose bound is not the caller's: the client handed to
	// Probe carries handshakeTimeout of its own when the caller supplied none.
	//
	// What a client supplied here is read for is its transport: this package
	// builds the request's client from it, so its redirect policy is not the
	// caller's to set (noRedirect, below, is every request's) and its timeout is
	// not relied on -- the request's own context is the bound.
	HTTP *http.Client
}

// getenv and goos are the two environment facts of the placement, with the
// process's own for a Client built from the field names alone (the CLI does).
func (c Client) getenv() func(string) string {
	if c.Getenv != nil {
		return c.Getenv
	}
	return os.Getenv
}

func (c Client) goos() string {
	if c.GOOS != "" {
		return c.GOOS
	}
	return runtime.GOOS
}

// noRedirect is the redirect policy of every client this package builds for a
// request that carries the token. The endpoint serves one route at the literal
// loopback address the registry file names, so an answer that points somewhere
// else is not this endpoint's -- and Go drops the Authorization header on a
// redirect to another host but keeps it on another port of the same host, which
// is exactly where a second process of this machine can stand. The token is the
// one thing that must reach no address the handshake did not prove.
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// http is the client the read and the stream go through: one this package builds
// from the caller's transport, with the redirect policy above. The caller's
// client is read and never written to -- a copy carries its transport and
// nothing else of it is relied on.
func (c Client) http() *http.Client {
	hc := &http.Client{CheckRedirect: noRedirect}
	if c.HTTP != nil {
		hc.Transport = c.HTTP.Transport
	}
	return hc
}

// handshakeClient is the client the handshake goes through, and it is the one
// request whose client this package builds even when the caller supplied none:
// Probe hands a nil to handshake, which would build one with handshake's own
// timeout and no redirect policy of its own, so the policy is stated here rather
// than hoped for. The timeout is handshake's own, for the reason it exists -- a
// process that accepts the connection and never answers -- and Lookup bounds the
// call with the same constant either way.
func (c Client) handshakeClient() *http.Client {
	if c.HTTP != nil {
		return c.http()
	}
	return &http.Client{Timeout: handshakeTimeout, CheckRedirect: noRedirect}
}

// Live is one live endpoint: the address it answers at and the credential this
// reader presents there. Lookup hands it back and Get and Stream are the only
// things that accept it, so no document in this package is read through a
// registry file the handshake did not prove.
//
// Its one field is unexported and String and GoString are how it renders — the
// two verbs a `%v`, a `%+v`, a `%s` and a `%#v` reach — so the value cannot
// carry the token into a log line, an error or a rendering of whatever holds it:
// what a caller can do with a Live is make a request. The address in String is
// not a secret -- every local process can reach the port -- and the credential
// is.
type Live struct {
	reg Registry
}

// String is what a caller may print or log: which endpoint, and never the
// credential it answers with.
func (l Live) String() string { return "the local endpoint at " + l.reg.Addr }

// GoString is String for the verb that would otherwise print the struct: `%#v`
// does not consult String, it prints the fields, and the field here is the
// Registry the token came out of. A value that cannot carry the credential into
// a log line has to hold for the verb that prints the most, not only for the one
// String is asked for.
func (l Live) GoString() string { return l.String() }

// Lookup reads id's registry file and decides whether a live endpoint is behind
// it: the file, the handshake, and the removal of a file that fails both of
// CG13b's checks, which is Probe's rule and the one place a reader removes a
// registry file.
//
// The false is "no endpoint here" and never a failure of the caller's read: a
// machine that has never run a daemon has no run directory, and a registry file
// a dead daemon left behind is removed and answered the same way, so the node
// and the cache below this are read exactly as a machine without the endpoint
// reads them. The error is for a file that is there and is not one this release
// wrote (a widened mode, a symlink, a document that does not decode): this
// reader cannot judge it, so it neither trusts it nor takes it away.
//
// The handshake is bounded by handshakeTimeout and not by the caller's context.
// A caller watching passes the watch's own context, which outlives any single
// lookup, and Probe holds the run directory's lock across the handshake: a
// process that accepts the connection and never answers -- the one that took a
// dead daemon's port -- would otherwise hold this read, and that lock, until the
// watch ended, and the next daemon's Hold would refuse over a file nobody is
// serving. What the timeout buys is the lock, which is why it is here and not
// only in the client Probe is handed.
func (c Client) Lookup(ctx context.Context, id string) (Live, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	reg, live, err := Probe(ctx, c.getenv(), c.goos(), id, c.handshakeClient())
	if err != nil {
		return Live{}, false, err
	}
	if !live {
		return Live{}, false, nil
	}
	return Live{reg: reg}, true, nil
}

// Get reads the endpoint's state once: the document the daemon's last tick left
// and the server time of that tick.
//
// The document is decoded by the node's own decoder, which is the whole of
// D1359's "the endpoint adds no shape": one decoder serves the node, the
// endpoint and the cache, and a `schema_version` this release does not read is
// refused here exactly as it is refused at the node.
func (c Client) Get(ctx context.Context, live Live) (orchestrator.State, time.Time, error) {
	resp, err := c.open(ctx, live, routeState)
	if err != nil {
		return orchestrator.State{}, time.Time{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxDocument))
	if err != nil {
		return orchestrator.State{}, time.Time{}, fmt.Errorf("endpoint: reading the document at %s: %w", live.reg.Addr, err)
	}
	at, err := tickAt(resp.Header)
	if err != nil {
		return orchestrator.State{}, time.Time{}, err
	}
	state, err := orchestrator.DecodeState(raw)
	if err != nil {
		return orchestrator.State{}, time.Time{}, fmt.Errorf("endpoint: the document at %s is not one this release reads: %w", live.reg.Addr, err)
	}
	return state, at, nil
}

// Stream follows the endpoint's stream: every document the daemon serves
// reaches fn with the server time of the tick it came from. The state at
// connect time is the first one, so a reader that has just read a snapshot and
// a reader that opens a watch see the same thing happen.
//
// It returns when ctx ends, when fn returns an error (returned unchanged: a
// writer that refused is the caller's to report), when the stream ends -- the
// daemon is shutting down -- which is an error, because a stream that stopped is
// not a stream that is following the endpoint any more, and when the peer sends
// an event longer than one document may be.
//
// The last of those is the bound the token does not give this reader: the peer
// here holds the credential by definition, so "who is asking" is no defence
// against "what is sent", and an event is accumulated a line at a time. Nothing
// grows past maxDocument -- one line or a thousand of them joined -- so a peer
// that never ends an event costs this process one document's worth of memory and
// then its refusal, rather than its memory until something kills it.
//
// Nothing here writes a keep-alive: the endpoint is silent while the state
// stands, and the reader that needs one owns its own timer (CG7's stream does).
func (c Client) Stream(ctx context.Context, live Live, fn func(orchestrator.State, time.Time) error) error {
	resp, err := c.open(ctx, live, routeStream)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	// One line of an event, bounded: the document is JSON on one line, and a
	// peer that never ends a line must not be able to make this reader grow.
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), maxDocument)
	var (
		at   time.Time
		data []byte
	)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "id:"):
			raw := strings.TrimSpace(strings.TrimPrefix(line, "id:"))
			tick, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return fmt.Errorf("endpoint: an event at %s carries no tick this release reads: %q", live.reg.Addr, safetext.OneLine(raw))
			}
			at = tick.UTC()
		case strings.HasPrefix(line, "data:"):
			// SSE joins the data lines of one event with a newline, and this
			// endpoint writes exactly one: a second one is joined rather than
			// dropped, so a frame this release did not write is decoded as the
			// event it claims to be and refused by the decoder if it is not one.
			value := strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
			joined := 0
			if data != nil {
				joined = 1
			}
			if len(data)+joined+len(value) > maxDocument {
				return fmt.Errorf("endpoint: an event at %s is longer than the %d bytes one document may be", live.reg.Addr, maxDocument)
			}
			if data == nil {
				data = []byte(value)
			} else {
				data = append(append(data, '\n'), value...)
			}
		case line == "":
			state, tick, ok, err := c.event(live, data, at)
			data, at = nil, time.Time{}
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			if err := fn(state, tick); err != nil {
				return err
			}
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("endpoint: reading the stream at %s: %w", live.reg.Addr, err)
	}
	return fmt.Errorf("endpoint: the stream at %s ended", live.reg.Addr)
}

// event is one server-sent event as a document and a tick, and the refusal of
// an event that is not one: the two lines the endpoint writes, or nothing. A
// blank line with no document before it is the end of nothing at all -- SSE
// sends those while a stream is idle -- and is not a failure, which is the
// false; a document with no tick beside it is, because there is no time to draw
// an age from and this machine's clock is not one.
func (c Client) event(live Live, data []byte, at time.Time) (orchestrator.State, time.Time, bool, error) {
	if data == nil {
		return orchestrator.State{}, time.Time{}, false, nil
	}
	if at.IsZero() {
		return orchestrator.State{}, time.Time{}, false, fmt.Errorf("endpoint: an event at %s carries no tick this release reads", live.reg.Addr)
	}
	state, err := orchestrator.DecodeState(data)
	if err != nil {
		return orchestrator.State{}, time.Time{}, false, fmt.Errorf("endpoint: an event at %s is not a document this release reads: %w", live.reg.Addr, err)
	}
	return state, at, true, nil
}

// open sends one authenticated GET and refuses anything that is not the
// endpoint's own answer. Everything a read must get right happens here once, so
// the read and the stream cannot drift, and the caller closes the body.
//
// The token travels in the one Authorization header and never in the URL: a
// query string is refused by the server before it is looked at, and the URL of
// a request is the one place a credential reaches a log -- a shell's history, a
// proxy's access log, this machine's own process list. The identity headers are
// checked on every answer and not only at Lookup: the handshake proved that the
// process at this address was the daemon when the file was read, and this
// proves it still is at the moment of a read, which is what a port another
// process took over in between fails -- the nonce is minted with the endpoint
// and never reused.
func (c Client) open(ctx context.Context, live Live, route string) (*http.Response, error) {
	if live.reg.Addr == "" {
		return nil, errors.New("endpoint: no live endpoint: a read is made through Lookup's answer and never without one")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+live.reg.Addr+route, nil)
	if err != nil {
		return nil, fmt.Errorf("endpoint: building the request for %s: %w", live.reg.Addr, err)
	}
	req.Header.Set(authorizationHeader, bearerScheme+" "+live.reg.Token)
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("endpoint: reading %s: %w", live.reg.Addr, err)
	}
	if resp.StatusCode != http.StatusOK {
		reason := refusalReason(resp.Body)
		_ = resp.Body.Close()
		return nil, fmt.Errorf("endpoint: %s refused the read: %s", live.reg.Addr, reason)
	}
	if !sameToken(resp.Header.Get(headerOrchestrator), live.reg.ID) || !sameToken(resp.Header.Get(headerNonce), live.reg.Nonce) {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("endpoint: the process answering at %s is not the daemon that wrote this registry file", live.reg.Addr)
	}
	return resp, nil
}
