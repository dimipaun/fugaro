package gateway

// The runner's loopback forwarder (B1.6, D495, G5, G11): the second listener
// this package serves, beside the budget gateway, and the half of the Bitbucket
// proxy that runs inside the job. A proxied repository's run holds no Bitbucket
// token at all (G1, the flip B1.13 part two makes real): git and the runner's
// Bitbucket client are pointed at this listener, and the credentials service's
// own route (internal/credsvc's Bitbucket proxy, B1.5) classifies the call,
// decides whether the run may make it, and forwards it with the token the
// bundle holds.
//
// Five things are the whole of its design.
//
//   - It holds no credential, and that is structural rather than a rule: there
//     is no field of GitForwardOptions a Bitbucket token could travel in, and
//     the three values a forward writes are the job's Google ID token, the
//     run's own identity and the job's name (G5). The caller's Authorization
//     and Proxy-Authorization are dropped whatever they hold, and so is every
//     X-Fugaro- header, by prefix: a caller that sent the run identity itself,
//     or a job name of its own, cannot have it arrive as this job's answer —
//     the forwarder writes those three over whatever arrived.
//   - It mints the job's ID token per request and reads the run's identity per
//     request (G11, D495). That is the whole reason a forwarder exists rather
//     than the run identity written into git's configuration (the rejected
//     alternative, U1): the run's identity token is exchanged and refreshed
//     while the run is alive, a git command outlives one, and nothing here is
//     ever handed to git to keep.
//   - It decides nothing. The method, the escaped path and the query travel as
//     the caller spelled them, and the path is the ESCAPED one on purpose: the
//     service's classification is the only reading of a path in this design,
//     and it refuses a spelling whose meaning depends on who decodes it — a
//     reading this listener cannot do for it, because it would have to agree
//     with it byte for byte about every escape (G13). What it does check is
//     that the path is its own route's: a listener that relayed any path would
//     present this job's identity to the credentials service's every route.
//   - Both directions stream. The body is handed to the service as it arrives
//     and the answer as it is answered; nothing is read to measure or buffer
//     it. A proxied repository pushes its pack through here (Check 190's 33
//     MiB push is what the h2c listener, the service's streaming and this
//     listening exist for).
//   - It writes no audit line and no line per request: one audit line per
//     proxied request is the service's (G7), and this listener knows nothing
//     the line would want. What it logs is a forward it could not make, as a
//     class and the transport's own cause (forwardCause) — never the path, the
//     headers or a token.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// GitProxyPrefix is this package's copy of the credentials service's Bitbucket
// proxy route (credsvc.BitbucketProxyPrefix, B1.5). It cannot be imported: the
// service imports this package and the dependency cannot run the other way, so
// the string is spelled here a second time on purpose and the contract test
// (TestGitForwardMirrorsCredsvc) is what holds the two spellings together. It
// is also the whole of the forwarder's URL path, which is what a runner hands
// git as url.<URL>.insteadOf, so a rewritten fetch arrives with the route's
// prefix already on it and the service reads the path it would have read from
// the wire.
const GitProxyPrefix = "/v1/bitbucket/proxy/"

// The classes a forward that did not reach the service is reported with: the
// caller is answered the class, and the run's log line carries it beside the
// cause, which is never a token and never the caller's own text.
const (
	reasonNoRunIdentity = "no_run_identity"
	reasonNoJobIdentity = "no_job_identity"
	reasonTarget        = "target"
	reasonUnreachable   = "unreachable"
)

// GitForwardOptions configure the forwarder.
type GitForwardOptions struct {
	// Service is the credentials service's own URL: a scheme and a host, https
	// or http on a loopback address (checkBaseURL, the rule the model path's
	// service upstream is held to). It has no default — it is the
	// installation's own service.
	Service string
	// Identity is how every forward identifies itself to the service. It is
	// the same type the model path's service upstream presents, and the same
	// rules hold it (validate): the service verifies both identities on every
	// call, and one reading of what an identity is keeps the two paths from
	// drifting apart.
	Identity Service
	// Log is where a forward that could not be made is reported. nil discards.
	Log *slog.Logger
}

// validate refuses a forwarder that could not present an identity or could not
// reach a service: there is nothing to fall back to (G5, D143), so this is
// refused at the start rather than once per git command.
func (o GitForwardOptions) validate() error {
	if o.Service == "" {
		return errors.New("gateway: the git forwarder has no service URL")
	}
	if err := checkBaseURL(o.Service); err != nil {
		return err
	}
	if o.Identity.IDToken == nil {
		return errors.New("gateway: the git forwarder has no job identity (the ID token Cloud Run IAM admits)")
	}
	if o.Identity.RunIDToken == nil {
		return errors.New("gateway: the git forwarder has no run identity")
	}
	if !validJobName(o.Identity.Job) {
		return errors.New("gateway: the git forwarder reports no Cloud Run job name")
	}
	return nil
}

// GitForward is a running forwarder.
type GitForward struct {
	o       GitForwardOptions
	log     *slog.Logger
	service string // the service's URL, with no trailing slash
	url     string // this listener's address with the proxy route's prefix on it
	client  *http.Client
	srv     *http.Server
	ln      net.Listener

	handlers  sync.WaitGroup // every forward being handled
	closeOnce sync.Once
	closeErr  error
}

// StartGitForward listens on 127.0.0.1 on a free port and serves until Close.
// The context bounds the start only.
func StartGitForward(ctx context.Context, o GitForwardOptions) (*GitForward, error) {
	if err := o.validate(); err != nil {
		return nil, err
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("gateway: git forward: listen: %w", err)
	}
	f := &GitForward{
		o:       o,
		log:     o.Log,
		service: strings.TrimSuffix(o.Service, "/"),
		url:     "http://" + ln.Addr().String() + GitProxyPrefix,
		client:  upstreamClient(nil),
		ln:      ln,
	}
	if f.log == nil {
		f.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	f.srv = &http.Server{
		Handler:           f,
		ReadHeaderTimeout: 30 * time.Second,
		ErrorLog:          log.New(io.Discard, "", 0), // a client's broken connection isn't the run's news
	}
	go func() { _ = f.srv.Serve(ln) }()
	return f, nil
}

// URL is the address git and the run's Bitbucket client are pointed at:
// "http://127.0.0.1:<port>" with the proxy route's prefix. It ends in a slash
// and the prefix does too, which is what makes the insteadOf rewrite a textual
// prefix replacement and not a path join.
func (f *GitForward) URL() string { return f.url }

// Close stops the listener and waits for the forwards in flight, bounded by
// ctx. A forward still running when ctx ends is cut short: the run it belonged
// to is over, and the service's answer to it is nobody's (G8's rule for the
// proxy the run stops with).
func (f *GitForward) Close(ctx context.Context) error {
	f.closeOnce.Do(func() {
		if err := f.srv.Shutdown(ctx); err != nil {
			_ = f.srv.Close()
			f.closeErr = fmt.Errorf("gateway: git forward: close: %w", err)
		}
		done := make(chan struct{})
		go func() { f.handlers.Wait(); close(done) }()
		select {
		case <-done:
		case <-ctx.Done():
			if f.closeErr == nil {
				f.closeErr = fmt.Errorf("gateway: git forward: close: %w", ctx.Err())
			}
		}
	})
	return f.closeErr
}

// ServeHTTP forwards one request, or refuses it by name.
func (f *GitForward) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.handlers.Add(1)
	defer f.handlers.Done()
	if !strings.HasPrefix(r.URL.EscapedPath(), GitProxyPrefix) {
		// Not this listener's route. The forwarder serves the proxy route and
		// nothing else: a listener that relayed whatever path it was handed
		// would be one more way to present this job's identity to the
		// credentials service's other routes, and the service's routes are
		// gated for the callers that make them.
		http.Error(w, "fugaro: this listener serves "+GitProxyPrefix+" only", http.StatusNotFound)
		return
	}
	up, ok := f.upstream(w, r)
	if !ok {
		return
	}
	resp, err := f.client.Do(up)
	if err != nil {
		f.fail(w, reasonUnreachable, err)
		return
	}
	defer resp.Body.Close()
	copyForwardAnswer(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// upstream builds the call to the service for one arriving request, or answers
// the caller and reports false.
//
// The request's own context is the upstream's: a git command that went away
// takes its forward with it, and nothing here outlives the caller that asked.
func (f *GitForward) upstream(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	run := f.o.Identity.RunIDToken()
	if run == "" {
		// An unauthenticated call to the service under the job's own Google
		// identity is not a call this forwarder makes: the run's identity is
		// what the service verifies the run with, and there is no second way
		// to be admitted (D143).
		f.fail(w, reasonNoRunIdentity, nil)
		return nil, false
	}
	id, err := f.o.Identity.IDToken(r.Context())
	if err != nil {
		f.fail(w, reasonNoJobIdentity, err)
		return nil, false
	}
	if id == "" {
		f.fail(w, reasonNoJobIdentity, nil)
		return nil, false
	}
	// The escaped path and the raw query: the service's classification is the
	// one reading of a path (G13), and what it must read is what the caller
	// wrote — an encoded separator reaches it as one and is refused there.
	up, err := http.NewRequestWithContext(r.Context(), r.Method, f.service+r.URL.RequestURI(), r.Body)
	if err != nil {
		f.fail(w, reasonTarget, err)
		return nil, false
	}
	up.Header = forwardRequestHeaders(r.Header)
	up.Header.Set("Authorization", "Bearer "+id)
	up.Header.Set(HeaderRunIdentity, run)
	up.Header.Set(HeaderRunJob, f.o.Identity.Job)
	// Never replayed: a retried request would be a second push, and the
	// service's answer to the first is not this listener's to undo.
	up.GetBody = nil
	if r.Body != nil && r.Body != http.NoBody {
		// The caller's own length, which may be unknown: a push streamed in
		// chunked framing stays streamed, and no pack is held here to measure
		// it. A request with no body keeps the zero NewRequest gave it, which
		// is what makes a fetch a bodyless request and not an empty chunked
		// one.
		up.ContentLength = r.ContentLength
	}
	return up, true
}

// dropForwardRequestHeaders are the headers of the caller's request that never
// go to the service.
//
// Authorization is the credential the caller presented, and the job's own ID
// token is written there instead (G5): that header is the one Cloud Run IAM
// reads, so what reaches the service has to be this job's identity and never
// whatever the caller put in it — a caller that minted the job's ID token
// itself gains nothing here, and one that put anything else in the header does
// not get to send it. The X-Fugaro- headers are the contract's own (the model
// path's forward.go drops them from a request too, by the same prefix), plus
// the hop-by-hop headers of
// RFC 9110, which describe this hop and not the next one, plus Host and
// Content-Length, which net/http recomputes from the URL and the body. What is
// left is what git and the REST adapter actually send (Content-Type, Accept,
// User-Agent, Git-Protocol, Accept-Encoding) — headers the protocol needs, none
// of which names a host or carries a credential.
var dropForwardRequestHeaders = map[string]bool{
	"Authorization": true, "Proxy-Authorization": true,
	"Host": true, "Content-Length": true,
	"Connection": true, "Keep-Alive": true, "Proxy-Connection": true, "Proxy-Authenticate": true,
	"Te": true, "Trailer": true, "Transfer-Encoding": true, "Upgrade": true,
}

// forwardRequestHeaders is the header set the service call is made with: the
// caller's, less the ones above and this contract's own by prefix — a header
// this contract gains later is dropped by the prefix rule too, as the service's
// own forwardHeaders drops it before bitbucket.org. Accept-Encoding is kept on
// purpose, unlike on the model path: a fetch's answer is a pack, git asked for
// it compressed and knows what to do with it, and a forwarder that asked for
// identity would put the decompression where the bytes are largest.
func forwardRequestHeaders(in http.Header) http.Header {
	out := make(http.Header, len(in))
	for k, vs := range in {
		ck := http.CanonicalHeaderKey(k)
		if dropForwardRequestHeaders[ck] || strings.HasPrefix(ck, "X-Fugaro-") {
			continue
		}
		out[ck] = append([]string(nil), vs...)
	}
	return out
}

// copyForwardAnswer relays the service's answer headers. The hop-by-hop ones
// describe this hop, and Content-Length is net/http's to write for what this
// listener copies. A redirect's destination is dropped and its status kept, on
// the service's own reasoning for bitbucket.org's answers (B1.5's
// stripRedirect): this listener follows no redirect (upstreamClient), and a
// Location handed back to the caller is a hop the CALLER would make — git
// follows the redirects it is answered, to a host no rule named. A run told 302
// with nowhere to go fails loudly, which is what a redirect nobody may follow
// should do.
func copyForwardAnswer(dst, src http.Header) {
	for k, vs := range src {
		ck := http.CanonicalHeaderKey(k)
		if hopByHop[ck] || ck == "Content-Length" || ck == "Location" || ck == "Content-Location" {
			continue
		}
		dst[ck] = append([]string(nil), vs...)
	}
}

// fail answers a forward this listener could not make. The caller gets a status
// and the class; the run's log gets the class and the cause, which is the
// runner's to read and nobody else's. No token, no header and no path of the
// caller's is in either: the cause is forwardCause's, which is the transport's
// own and not the URL net/http wraps it in.
func (f *GitForward) fail(w http.ResponseWriter, reason string, cause error) {
	attrs := []any{"reason", reason}
	if c := forwardCause(cause); c != nil {
		attrs = append(attrs, "error", c)
	}
	f.log.Error("gateway: a git forward failed", attrs...)
	http.Error(w, "fugaro: this run's git request could not be forwarded to the credentials service ("+reason+")", http.StatusBadGateway)
}

// forwardCause is the cause of a failed forward as the log line may carry it.
// net/http answers every failure of client.Do — and of a request's own build —
// with a *url.Error, whose text is the URL the forward went to with the
// caller's escaped path and query on it: the one way a path could reach a line
// this listener promises never carries one. Under it is the transport's error —
// a dial failure, a TLS failure, a reset — which names the address and no path,
// and is the whole of what a run's operator can act on. Nothing under it (a
// *url.Error that is only a URL) is nil, and the class is then the line. An
// error that is no *url.Error is logged as it is: the other two a forward fails
// with are a run with no identity and a refused mint, and neither carries the
// caller's path.
func forwardCause(err error) error {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return err
	}
	return ue.Err
}
