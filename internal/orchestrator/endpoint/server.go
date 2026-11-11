// Package endpoint is the local endpoint a daemon serves: one loopback
// listener, one token, and the daemon's own state document (CG13a of
// docs/cli-output-console-graph/implementation-plan-graph.md).
//
// It is the first of the three slices B5 splits the endpoint into, and it is
// **inert**: nothing outside its own tests starts one (CG13b does, at the
// daemon's start-up, and writes the registry file that tells a reader where to
// connect). What is here is the server and the token rules D1359 answers with,
// so the seven acceptance tests of that decision can be run against the thing
// itself rather than against a daemon.
//
// The rules, each one a named test in server_test.go:
//
//   - **One listener on `127.0.0.1:0`, never a fixed port** and never another
//     address (TestEndpointBindsLoopbackPortZero). Port 0 is the kernel's
//     choice recorded rather than a port this process hoped was free: a fixed
//     port would be the one two daemons fight over, and a second address would
//     be one every machine on the network could reach.
//   - **The token on every request, the stream included**
//     (TestTokenOnEveryRequestIncludingTheStream), in one `Authorization:
//     Bearer` header, compared in constant time (TestConstantTimeTokenCompare,
//     token.go).
//   - **A `Host` that is not the literal address, refused before the token is
//     read** (TestRefusesAHostOtherThanTheLiteralAddress). A loopback bind is
//     reachable by every local process, and by a browser page through DNS
//     rebinding: a name that is not `127.0.0.1:<port>` is that page, whatever
//     credential it carries.
//   - **No CORS header, on an answer or a refusal**, and `OPTIONS` refused
//     (TestNoCorsHeaders, TestGeneralOptionsIsRefusedLikeAnyOtherRequest): a
//     page that cannot read an answer cannot use one, and the endpoint has
//     nothing an origin is allowed to ask for. Refused by these rules and not by
//     the standard library's own answer to `OPTIONS *`, which is served in front
//     of the handler and would have made `*` the one target whose Host was never
//     looked at.
//   - **Read-only** (TestEndpointIsReadOnly): `GET` and nothing else, and no
//     route a state change could travel on. The server holds no Store, no Node,
//     no launcher and no filesystem -- the only thing it can reach is the
//     snapshot function it was built with -- so this is a property of what it
//     holds rather than a rule it remembers (F23's "what a hostile local
//     process of the same user can do").
//   - **A caller before the first tick gets `503` and a reason, not an exit**
//     (TestNoSnapshotBeforeTheFirstTickIsAState, H3): the daemon serves its
//     endpoint from before its first cycle, and a reader that arrives then must
//     be told the difference between "no state yet" and "gone".
//   - **A `?token=` is refused, and that refusal never prints the query**
//     (TestTokenInTheQueryStringIsRefused, TestTheRefusalNeverCarriesTheToken),
//     so a credential a caller put in a URL cannot reach a log -- nor, before
//     the token is read, can any other word the caller chose: a refusal made
//     before that check reports the reason and nothing of the request
//     (TestAPreTokenRefusalLogsNoneOfTheCallersText), because the URL of a
//     request nobody has authenticated is a string a local process can make as
//     long as it likes.
//
// What answers is the node's own document and nothing else. `GET /state` is
// the state exactly as `orchestrator.EncodeState` writes it -- byte for byte
// what `orchestrator/<id>/state` carries -- so one decoder serves the node and
// the endpoint, and a `schema_version` this release does not read is refused in
// both by the same code (D1359's "the endpoint adds no shape"). The cached brief
// is not a third: it is `orchestrator.BriefCache`, a shape of its own with a
// version of its own -- a brief and the time it was written, never a state --
// read by its own decoder. What the endpoint adds beyond the node is freshness and two
// facts of its own, and both travel beside the document rather than inside it:
// the server time of the tick in `X-Fugaro-Tick`, and, for the handshake that
// proves which process holds this port (CG13b), the id and the start-time nonce
// in `X-Fugaro-Orchestrator` and `X-Fugaro-Nonce`. The handshake is answered to
// a caller that proved the token and to no refusal at all
// (TestTheRefusalCarriesNoIdentityHeaders): a local process with no credential
// learns nothing about what holds the port.
//
// This package is core and stays core: the daemon is core and may not import
// ee/, and nothing here needs to.
package endpoint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The two routes, and there is no third: the state once, and the state as it
// moves. A path is compared whole, so `/state/` and `/state/anything` are not
// the stream's neighbours but unknown routes.
const (
	routeState  = "/state"
	routeStream = "/state/stream"
)

// The answer headers the endpoint puts beside the document, and never inside
// it. `X-Fugaro-Tick` is the server time of the tick the document came from --
// the same stamp the cached brief carries, read from the database's clock
// rather than this machine's (D1230) -- and it is what a scope draws an age
// from without decoding anything. `X-Fugaro-Orchestrator` and `X-Fugaro-Nonce`
// are the identity half of CG13b's handshake: a reader that holds the registry
// file connects, presents the token from that file and reads back the id and
// the nonce the file also carries, which is what tells the endpoint it reached
// from the endpoint that wrote the file apart from whatever process holds that
// port now. The nonce is minted here with the token and not by the registry
// writer, because the value it answers with has to be the value the live
// process holds; a nonce a file could mint for itself would prove nothing.
//
// The token itself is never among them: an answer that echoed the credential it
// accepted would put a secret into every proxy log and every curl -v on the
// machine, which is the leak the registry file's mode 0600 exists to prevent
// (F23).
const (
	headerOrchestrator = "X-Fugaro-Orchestrator"
	headerNonce        = "X-Fugaro-Nonce"
	headerTick         = "X-Fugaro-Tick"
)

// streamPoll is how often a stream asks the snapshot whether the state moved.
// The endpoint is the freshness path (D1358's measurement, waived by the
// owner's request of 2026-11-11), so it is a second rather than a tick: a
// daemon's interval is minutes, and a reader that had to wait for the next
// cycle would be reading the cache file with extra steps.
const streamPoll = time.Second

// noTick is the reason a caller is given before the daemon's first cycle has
// finished. It is a state and not an exit (H3): the same endpoint answers with
// a document a moment later, and a reader that treated this as the endpoint
// being gone would drop a daemon that is starting up.
const noTick = "no tick yet: the daemon has not finished its first cycle, so it has no state to serve"

// Options is everything the endpoint is given: who it is and what it may say.
// There is deliberately no third thing -- no store, no node, no launcher, no
// path, no client -- because read-only is what the endpoint can reach and not
// what it promises (F23).
type Options struct {
	// ID is the orchestrator id this endpoint serves: the `<id>` of
	// `orchestrator/<id>`, and what a reader compares against the registry file
	// it read. The daemon's id is the project's name, and an endpoint with no
	// id could not answer the handshake at all.
	ID string
	// Snapshot is the daemon's last tick: the state it left, the server time it
	// read, and whether a cycle has finished at all. The stamp is the database's
	// clock and never this machine's (D1230): it is the age a scope draws.
	//
	// It is called from the request goroutines, so the daemon must hand back a
	// state it is not writing -- the tick's own state, not the one the next
	// cycle is filling -- and the false is a state of the daemon rather than an
	// error: it is what answers 503 until the first cycle lands (H3).
	Snapshot func() (orchestrator.State, time.Time, bool)
	// Log is where a refusal worth knowing about goes, and nil is a server that
	// reports nothing. It is handed an error and never a string, and the error
	// carries the method, the path and the reason -- never a query string and
	// never a header -- so a token cannot reach a log through it.
	Log func(error)
}

// Server is one running endpoint.
type Server struct {
	id    string
	token string
	nonce string
	snap  func() (orchestrator.State, time.Time, bool)
	log   func(error)

	addr string
	srv  *http.Server

	// base is the life of the server, and it is what a stream watches to end:
	// Close cancels it before it waits, so a stream that is quiet -- the normal
	// case, a graph that has not moved -- returns rather than holding the
	// shutdown open until its deadline.
	base   context.Context
	cancel context.CancelFunc

	closeOnce sync.Once
	closeErr  error
}

// Start listens on loopback and serves until Close.
//
// It mints the token and the nonce, binds `127.0.0.1:0` and starts serving; the
// context bounds the start only, exactly as the runner's gateway does
// (internal/gateway), so a caller that cancels it after Start has a running
// endpoint, and Close is how one stops.
func Start(ctx context.Context, o Options) (*Server, error) {
	if o.Snapshot == nil {
		return nil, errors.New("endpoint: a server needs the snapshot it serves, and it holds nothing else")
	}
	if o.ID == "" {
		return nil, errors.New("endpoint: a server needs the orchestrator id it answers with, and an endpoint nothing can identify is no handshake")
	}
	token, err := mint()
	if err != nil {
		return nil, err
	}
	nonce, err := mint()
	if err != nil {
		return nil, err
	}
	// The literal, and not "localhost": the name resolves to the loopback
	// address anyway, and a listener created from it would answer to whatever
	// else that name has come to mean on this machine.
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("endpoint: listen: %w", err)
	}
	s := &Server{
		id:    o.ID,
		token: token,
		nonce: nonce,
		snap:  o.Snapshot,
		log:   o.Log,
		addr:  ln.Addr().String(),
	}
	// The endpoint outlives the context that started it -- the daemon's own
	// life is longer than its start-up -- so the server's context is derived
	// from it without its cancellation.
	s.base, s.cancel = context.WithCancel(context.WithoutCancel(ctx))
	s.srv = &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 30 * time.Second,
		// `OPTIONS * HTTP/1.1` is answered by the standard library itself, in
		// front of the handler, with a 200 and no body: a request whose Host is a
		// name of the attacker's choosing would be served by a server that never
		// looked at the Host, and the endpoint's own answer -- refused, read-only
		// -- would not be the one that reached the caller. Turning the general
		// handler off is what makes `*` an ordinary target here, checked like any
		// other (TestGeneralOptionsIsRefusedLikeAnyOtherRequest).
		DisableGeneralOptionsHandler: true,
		// A client's broken connection is not the daemon's news: the endpoint
		// logs what it decides to log through Log and nothing else.
		ErrorLog: log.New(io.Discard, "", 0),
	}
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

// Addr is the address the endpoint listens on: the literal `127.0.0.1` and the
// port the kernel chose. It is what the registry file carries (CG13b) and what
// the Host check compares against, which is why it is the listener's own answer
// rather than a string this package built.
func (s *Server) Addr() string { return s.addr }

// Token is the credential every request must carry. It is the endpoint's own
// secret and this accessor is the one place it leaves the process: the daemon
// writes it into the registry file at mode 0600 and nowhere else -- not into a
// log, not into the document, not into an error.
func (s *Server) Token() string { return s.token }

// Nonce is the start-time value minted beside the token, and the second half of
// the handshake: it is written into the registry file with the token and
// answered in a header, so a file left behind by a dead orchestrator cannot be
// mistaken for one a live process holds.
func (s *Server) Nonce() string { return s.nonce }

// Close stops serving and releases the port. It cancels the streams first and
// then waits for the connections to finish, bounded by ctx: a stream is idle by
// nature -- nothing is written while the state stands -- and waiting for one to
// notice a deadline would make every shutdown as slow as that deadline.
func (s *Server) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		s.cancel()
		s.closeErr = s.srv.Shutdown(ctx)
	})
	return s.closeErr
}

// ServeHTTP is the whole of the endpoint's routing, and the order of these
// checks is the design rather than an accident of writing them down.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The Host check comes first, before the token is looked at: a request
	// whose Host is `localhost`, another name, an IPv6 spelling or nothing at
	// all is a browser that resolved some name to this port (F23), and the
	// credential it carries -- a real one, replayed from a page -- must not
	// decide whether it is served.
	if r.Host != s.addr {
		reason := "this endpoint answers only " + s.addr
		s.refuse(w, http.StatusForbidden, reason)
		s.reportBeforeToken("a Host that is not the endpoint's own address was refused")
		return
	}
	// Every method but GET, OPTIONS included: a preflight is the browser
	// asking what it may send, and the answer is nothing (no CORS header
	// anywhere), so the method is refused as the rest are. Not logged, like the
	// token below: the port is reachable by every local process, and a scan is
	// not news the daemon should be made to write a line about.
	if r.Method != http.MethodGet {
		s.refuse(w, http.StatusMethodNotAllowed, "this endpoint is read-only: only GET is served")
		return
	}
	// The query is refused whole, and before the token: the routes take no
	// parameters, so a query string is never a parameter -- it is a credential
	// in a URL, which is the one place a credential reaches a log (a shell's
	// history, a proxy's access log, this refusal's own text). The reason below
	// is built from the path for exactly that sentence.
	if r.URL.RawQuery != "" {
		s.refuse(w, http.StatusBadRequest, "this endpoint takes no query string: pass the token in the "+authorizationHeader+" header")
		s.reportBeforeToken("a query string was refused")
		return
	}
	// Not logged: every local process can reach this port, and a scan is not
	// news the daemon should be made to write a line about (the runner's
	// gateway answers a bad key the same way).
	if !s.authorised(r) {
		s.refuse(w, http.StatusUnauthorized, "the token is missing or wrong")
		return
	}
	switch r.URL.Path {
	case routeState:
		s.state(w, r)
	case routeStream:
		s.stream(w, r)
	default:
		s.refuse(w, http.StatusNotFound, "no such route: this endpoint serves "+routeState+" and "+routeStream)
	}
}

// authorised reads the one Authorization header and compares it against the
// token in constant time. The header is never echoed, not even into the log a
// wrong one might deserve: an error that quoted the credential it refused would
// be the same leak one step later.
func (s *Server) authorised(r *http.Request) bool {
	got, ok := bearerToken(r)
	return ok && sameToken(got, s.token)
}

// document is the answer both routes start from: the node's own document and
// the server time of the tick it came from.
//
// The encoder is the node's own and not a second one: the answer is byte for
// byte what `orchestrator/<id>/state` carries, so the reader that decodes the
// node decodes this, and a document this release would not write is a document
// it does not serve. The false is a request already answered -- the 503 before
// the first tick, the 500 of a state that could not be written -- and never an
// error to dress up: the endpoint's one answer is a document or a refusal.
func (s *Server) document(w http.ResponseWriter, r *http.Request) ([]byte, time.Time, bool) {
	state, at, ok := s.snap()
	if !ok {
		s.identify(w.Header())
		s.refuse(w, http.StatusServiceUnavailable, noTick)
		return nil, time.Time{}, false
	}
	data, err := orchestrator.EncodeState(state)
	if err != nil {
		s.identify(w.Header())
		s.refuse(w, http.StatusInternalServerError, "the daemon's state could not be written")
		s.report(r, "encoding the state: "+err.Error())
		return nil, time.Time{}, false
	}
	return data, at, true
}

// state answers the last tick once.
func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	data, at, ok := s.document(w, r)
	if !ok {
		return
	}
	h := w.Header()
	s.identify(h)
	h.Set(headerTick, at.UTC().Format(time.RFC3339))
	h.Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// stream answers the state as it moves: one whole document per tick whose state
// is not the one before, which is what makes a lost document cost nothing (the
// next one is the whole state) and a reader need no replay.
//
// Nothing is written while the state stands. The CLI's own stream carries the
// keep-alive that an orphaned child needs (D1360, CG7), and a reader of this
// endpoint is a process that owns its own read loop and its own timeout: the
// endpoint's silence is the fact that nothing has changed, and inventing a
// document to say so would be a document a scope would have to learn to ignore.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	last, at, ok := s.document(w, r)
	if !ok {
		return
	}
	h := w.Header()
	s.identify(h)
	h.Set(headerTick, at.UTC().Format(time.RFC3339))
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	// The state the reader connected for, at once: a stream that waited for the
	// next change would make a scope that just opened show nothing until the
	// orchestrator moved, and a quiet orchestrator is the one a person opens a
	// graph for.
	if err := writeEvent(w, rc, at, last); err != nil {
		s.report(r, "writing the stream: "+err.Error())
		return
	}
	tick := time.NewTicker(streamPoll)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			// The reader is gone. Nothing is logged: a console that was closed
			// is not the daemon's news.
			return
		case <-s.base.Done():
			return
		case <-tick.C:
		}
		state, at, ok := s.snap()
		if !ok {
			continue
		}
		data, err := orchestrator.EncodeState(state)
		if err != nil {
			s.report(r, "encoding the state: "+err.Error())
			return
		}
		if bytes.Equal(data, last) {
			continue
		}
		if err := writeEvent(w, rc, at, data); err != nil {
			s.report(r, "writing the stream: "+err.Error())
			return
		}
		last = data
	}
}

// writeEvent is one server-sent event: the tick's server time in `id`, the
// document in `data`.
//
// The time travels in `id` because the document is the node's own and the
// endpoint adds no shape to it (D1359), and an event is the only place SSE has
// for a fact of its own per event -- a header belongs to the response, and a
// field of its own invention would be a second wire format for one document.
// Nothing reads it back: the endpoint answers no `Last-Event-ID` and resumes
// nothing, because a document that is the whole state needs no resumption.
//
// The document goes on one `data:` line: it is marshalled JSON, which escapes
// every newline inside the strings it carries and never emits a bare one, so an
// event is exactly these two lines and the blank line that ends it.
func writeEvent(w http.ResponseWriter, rc *http.ResponseController, at time.Time, data []byte) error {
	var b bytes.Buffer
	fmt.Fprintf(&b, "id: %s\n", at.UTC().Format(time.RFC3339))
	b.WriteString("data: ")
	b.Write(data)
	b.WriteString("\n\n")
	if _, err := w.Write(b.Bytes()); err != nil {
		return err
	}
	return rc.Flush()
}

// identify sets the handshake headers: which orchestrator answers here and the
// nonce it was started with. It is called on every answer a caller that proved
// the token receives, the 503 before the first tick included -- the handshake
// asks whether this process is the one the registry file describes, which is a
// question about the process and not about its state -- and on no refusal at
// all, so a caller with no token learns nothing about what is behind the port
// (TestTheRefusalCarriesNoIdentityHeaders, which holds both halves).
func (s *Server) identify(h http.Header) {
	h.Set(headerOrchestrator, s.id)
	h.Set(headerNonce, s.nonce)
}

// refuse answers a request with a reason. It is a refusal and not an error
// page: no CORS header of any kind (there is no origin this endpoint serves),
// no identity header (see identify), and a body that carries the reason and
// nothing else -- never the query string, never a header value.
func (s *Server) refuse(w http.ResponseWriter, status int, reason string) {
	body, err := json.Marshal(struct {
		Error string `json:"error"`
	}{Error: reason})
	if err != nil {
		// json.Marshal of a struct with one string cannot fail; a refusal that
		// could not be encoded still has to be a refusal.
		body = []byte(`{"error":"refused"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// report hands a refusal worth knowing about to the log, with the method and
// the path. The path and not the request URI: a URI carries the query string,
// which is where a caller's token may be, and the whole point of refusing a
// query is that the credential in it reaches nothing -- this line included.
//
// It is only ever called for a request that has already proved the token, which
// is why it may name the path at all: by then the path is one of the two routes
// the switch above matched, and both are this package's own strings. A refusal
// made before that is reportBeforeToken's, and it says nothing of the caller's.
func (s *Server) report(r *http.Request, why string) {
	if s.log == nil {
		return
	}
	s.log(fmt.Errorf("endpoint: %s %s: %s", r.Method, safetext.OneLine(r.URL.Path), safetext.OneLine(why)))
}

// reportBeforeToken is what a refusal made before the token was read may write:
// the fixed reason and nothing the caller chose.
//
// Before the token, nothing about the request is the endpoint's to trust. The
// request line and the URL are whatever the caller typed -- a local process of
// any user, or a page that reached the port through a name of its own -- and
// both are unbounded, so a line built from them is a line any process on the
// machine can write into this daemon's log at whatever length it likes (500 KB
// in one request, measured). What is worth knowing is that a request of that
// shape arrived, and that is the reason; the operator who wants to know which
// route was asked for has the log of a request that got in, which is a request
// that proved the token. It is also the rule the token and method refusals
// already follow: a scan is not news the daemon should be made to write about,
// and it cannot be made to write with the scanner's own words either.
func (s *Server) reportBeforeToken(why string) {
	if s.log == nil {
		return
	}
	s.log(fmt.Errorf("endpoint: %s", why))
}
