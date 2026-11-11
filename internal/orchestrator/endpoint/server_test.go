package endpoint

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/tier"
)

// The daemon's own time and state in these tests: one fixed server stamp and a
// state the release writes and reads, so a case is about the endpoint's rules
// and never about a document that happens to be invalid.
var (
	tickedAt   = time.Date(2026, 11, 11, 17, 0, 0, 0, time.UTC)
	endpointID = "widgets"
)

// endpointFixture is one endpoint under test with the daemon's half of the
// seam: the snapshot it answers with -- the only thing the endpoint can read --
// the count of how often that snapshot was read, and the log it wrote.
type endpointFixture struct {
	t   *testing.T
	srv *Server

	mu     sync.Mutex
	state  orchestrator.State
	at     time.Time
	ticked bool
	reads  int

	log logBuffer
}

// logBuffer collects what the endpoint reported. It is guarded because the
// stream writes from the handler's goroutine while the test reads.
type logBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// newFixture starts one endpoint serving testState, already ticked: a case that
// wants the before-the-first-tick answer untick's it.
func newFixture(t *testing.T) *endpointFixture {
	t.Helper()
	f := &endpointFixture{t: t, state: testState("wi-1"), at: tickedAt, ticked: true}
	srv, err := Start(context.Background(), Options{ID: endpointID, Snapshot: f.snapshot, Log: f.logf})
	if err != nil {
		t.Fatalf("starting the endpoint: %v", err)
	}
	f.srv = srv
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Close(ctx); err != nil {
			t.Errorf("closing the endpoint: %v", err)
		}
	})
	return f
}

// testState is a state this release writes and reads: one queued work item, so
// the document the endpoint serves is the document the node would carry.
func testState(item string) orchestrator.State {
	return orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		Rev:           1,
		AgentID:       "widgets",
		Role:          orchestrator.RoleOrchestrator,
		Goal:          "land the local endpoint",
		WorkItems: []orchestrator.WorkItem{{
			ID:        item,
			Title:     "the endpoint answers the local scope",
			Repo:      "acme/widgets",
			Recipe:    "default",
			RigorTier: string(tier.Small),
			Brief:     "serve the state the daemon left",
			Phase:     orchestrator.PhaseQueued,
		}},
	}
}

// logf is what the endpoint reports to: one line per error, the way the daemon
// hands them to its own output.
func (f *endpointFixture) logf(err error) {
	if err == nil {
		return
	}
	_, _ = f.log.Write([]byte(err.Error() + "\n"))
}

func (f *endpointFixture) snapshot() (orchestrator.State, time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	return f.state, f.at, f.ticked
}

// tick is one cycle landing: the state the daemon left and the server time it
// read it at.
func (f *endpointFixture) tick(s orchestrator.State, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state, f.at, f.ticked = s, at, true
}

func (f *endpointFixture) untick() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ticked = false
}

func (f *endpointFixture) snapshotReads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

// current is the state the endpoint is serving now, read the way the endpoint
// reads it: a stream's handler is asking for it from its own goroutine while a
// case is asking here.
func (f *endpointFixture) current() orchestrator.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

// bearer is the header a caller with the token sends.
func (f *endpointFixture) bearer() map[string]string {
	return map[string]string{authorizationHeader: "Bearer " + f.srv.Token()}
}

// do calls the handler directly, which is how every rule about a refusal is
// tested: the check happens before anything is written, so no socket is needed
// to prove it. A host of "" is a request that carried no Host at all.
func (f *endpointFixture) do(method, path, query, host string, headers map[string]string) *httptest.ResponseRecorder {
	f.t.Helper()
	target := "http://" + f.srv.Addr() + path
	if query != "" {
		target += "?" + query
	}
	r := httptest.NewRequest(method, target, nil)
	if host == "" {
		r.Host = ""
	} else {
		r.Host = host
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	f.srv.ServeHTTP(rec, r)
	return rec
}

// document is what the endpoint owes a reader: the node's own document, written
// by the node's own encoder.
func (f *endpointFixture) document(s orchestrator.State) string {
	f.t.Helper()
	data, err := orchestrator.EncodeState(s)
	if err != nil {
		f.t.Fatalf("encoding the state: %v", err)
	}
	return string(data)
}

// stream opens the SSE route over a real connection: the handler writes to the
// socket until the reader goes away, which a recorder cannot express. The
// caller cancels ctx to close it.
func (f *endpointFixture) stream(ctx context.Context, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+f.srv.Addr()+routeStream, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return (&http.Client{}).Do(req)
}

// readEvent reads one server-sent event: its id and its data.
func readEvent(t *testing.T, r *bufio.Reader) (id, data string) {
	t.Helper()
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("reading an event: %v", err)
		}
		line = strings.TrimSuffix(line, "\n")
		switch {
		case line == "":
			return id, data
		case strings.HasPrefix(line, "id: "):
			id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		}
	}
}

func TestEndpointBindsLoopbackPortZero(t *testing.T) {
	first := newFixture(t)
	second := newFixture(t)

	host, port, err := net.SplitHostPort(first.srv.Addr())
	if err != nil {
		t.Fatalf("the address %q is not host:port: %v", first.srv.Addr(), err)
	}
	if host != "127.0.0.1" {
		t.Fatalf("the endpoint listens on %q: a bind that is not the loopback literal is one every machine on the network reaches", host)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n == 0 {
		t.Fatalf("the port is %q: the kernel's own choice is what the endpoint records, never the 0 it asked for", port)
	}
	conn, err := net.Dial("tcp", first.srv.Addr())
	if err != nil {
		t.Fatalf("dialling the endpoint's own address: %v", err)
	}
	_ = conn.Close()
	if first.srv.Addr() == second.srv.Addr() {
		t.Fatalf("two endpoints were given one address: port 0 is the kernel's choice, not a value two runs share")
	}
	for _, srv := range []*Server{first.srv, second.srv} {
		raw, err := hex.DecodeString(srv.Token())
		if err != nil {
			t.Fatalf("the token is not hex: %v", err)
		}
		if len(raw) != tokenBytes {
			t.Fatalf("the token is %d bytes, want %d", len(raw), tokenBytes)
		}
	}
	if first.srv.Token() == second.srv.Token() {
		t.Fatalf("two endpoints minted the same token")
	}
}

func TestTokenOnEveryRequestIncludingTheStream(t *testing.T) {
	f := newFixture(t)
	wrong, err := mint()
	if err != nil {
		t.Fatalf("minting a second token: %v", err)
	}

	refusals := map[string]map[string]string{
		"no token":                    nil,
		"another token":               {authorizationHeader: "Bearer " + wrong},
		"another scheme":              {authorizationHeader: "Token " + f.srv.Token()},
		"the token with no scheme":    {authorizationHeader: f.srv.Token()},
		"the token in another header": {"X-Api-Key": f.srv.Token()},
	}
	for name, headers := range refusals {
		for _, route := range []string{routeState, routeStream} {
			rec := f.do(http.MethodGet, route, "", f.srv.Addr(), headers)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s on %s: %d, want %d", name, route, rec.Code, http.StatusUnauthorized)
			}
		}
	}

	// Two Authorization headers are two answers to one question, and a server
	// that read the first would answer differently depending on a proxy's
	// order.
	two := httptest.NewRequest(http.MethodGet, "http://"+f.srv.Addr()+routeState, nil)
	two.Header.Add(authorizationHeader, "Bearer "+f.srv.Token())
	two.Header.Add(authorizationHeader, "Bearer "+wrong)
	rec := httptest.NewRecorder()
	f.srv.ServeHTTP(rec, two)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("two Authorization headers: %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	answer := f.do(http.MethodGet, routeState, "", f.srv.Addr(), f.bearer())
	if answer.Code != http.StatusOK {
		t.Fatalf("the token the endpoint minted: %d, want %d", answer.Code, http.StatusOK)
	}
	if got := answer.Body.String(); got != f.document(f.current()) {
		t.Errorf("the answer is not the node's own document:\n got %s\nwant %s", got, f.document(f.current()))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := f.stream(ctx, f.bearer())
	if err != nil {
		t.Fatalf("opening the stream with the token: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the stream with the token: %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if _, data := readEvent(t, bufio.NewReader(resp.Body)); data != f.document(f.current()) {
		t.Errorf("the stream's first event is not the node's own document: %s", data)
	}
}

func TestRefusesAHostOtherThanTheLiteralAddress(t *testing.T) {
	f := newFixture(t)
	_, port, err := net.SplitHostPort(f.srv.Addr())
	if err != nil {
		t.Fatalf("the address %q is not host:port: %v", f.srv.Addr(), err)
	}
	other, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("the port %q is not a number: %v", port, err)
	}
	hosts := []string{
		"localhost:" + port,
		"[::1]:" + port,
		"127.0.0.1:" + strconv.Itoa(other+1),
		"127.0.0.1",
		"evil.example",
		"evil.example:" + port,
		"",
	}
	for _, host := range hosts {
		for _, route := range []string{routeState, routeStream} {
			rec := f.do(http.MethodGet, route, "", host, f.bearer())
			if rec.Code != http.StatusForbidden {
				t.Errorf("Host %q on %s: %d, want %d", host, route, rec.Code, http.StatusForbidden)
			}
		}
	}
	// Before the token, which is the order the rule is: a page reaching the
	// port through a name of its own is refused whatever credential it
	// presents, and the refusal must not tell it that a credential was the
	// thing it lacked.
	rec := f.do(http.MethodGet, routeState, "", "localhost:"+port, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a rebinding Host with no token: %d, want %d: the Host is checked before the token", rec.Code, http.StatusForbidden)
	}
	if got := f.do(http.MethodGet, routeState, "", f.srv.Addr(), f.bearer()); got.Code != http.StatusOK {
		t.Fatalf("the literal address: %d, want %d", got.Code, http.StatusOK)
	}
}

func TestNoCorsHeaders(t *testing.T) {
	f := newFixture(t)
	answers := map[string]*httptest.ResponseRecorder{
		"an answer":        f.do(http.MethodGet, routeState, "", f.srv.Addr(), f.bearer()),
		"a refusal":        f.do(http.MethodGet, routeState, "", f.srv.Addr(), nil),
		"a Host refusal":   f.do(http.MethodGet, routeState, "", "localhost:1", f.bearer()),
		"an unknown route": f.do(http.MethodGet, "/state/other", "", f.srv.Addr(), f.bearer()),
		"a query refusal":  f.do(http.MethodGet, routeState, "token=x", f.srv.Addr(), f.bearer()),
		"a preflight":      f.do(http.MethodOptions, routeState, "", f.srv.Addr(), map[string]string{"Origin": "https://evil.example", "Access-Control-Request-Method": "GET"}),
	}
	for name, rec := range answers {
		for header := range rec.Header() {
			if strings.HasPrefix(http.CanonicalHeaderKey(header), "Access-Control-") {
				t.Errorf("%s carries %s: no origin is served by this endpoint, so no answer may name one", name, header)
			}
		}
	}
	if got := answers["a preflight"].Code; got != http.StatusMethodNotAllowed {
		t.Errorf("an OPTIONS preflight: %d, want %d", got, http.StatusMethodNotAllowed)
	}
}

func TestEndpointIsReadOnly(t *testing.T) {
	f := newFixture(t)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions} {
		for _, route := range []string{routeState, routeStream} {
			rec := f.do(method, route, "", f.srv.Addr(), f.bearer())
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: %d, want %d", method, route, rec.Code, http.StatusMethodNotAllowed)
			}
		}
	}
	// The state is what the endpoint can reach and not what it promises: no
	// request but a GET reaches the snapshot at all, which is the mutation this
	// case is written against -- a second route that took a write would have to
	// read something to obey it.
	if got := f.snapshotReads(); got != 0 {
		t.Fatalf("a method that is not GET reached the snapshot %d times", got)
	}
	if got := f.do(http.MethodGet, routeState, "", f.srv.Addr(), f.bearer()); got.Code != http.StatusOK {
		t.Fatalf("the endpoint stopped serving GET after refusals: %d", got.Code)
	}
}

func TestTokenInTheQueryStringIsRefused(t *testing.T) {
	f := newFixture(t)
	token := f.srv.Token()
	queries := []string{"token=" + token, "token=", "TOKEN=" + token, "x=1"}
	for _, query := range queries {
		for _, route := range []string{routeState, routeStream} {
			// With the right header as well: the query is never a parameter, so
			// a request that carries one is refused however it authenticated.
			rec := f.do(http.MethodGet, route, query, f.srv.Addr(), f.bearer())
			if rec.Code != http.StatusBadRequest {
				t.Errorf("?%s on %s: %d, want %d", query, route, rec.Code, http.StatusBadRequest)
			}
			if body := rec.Body.String(); strings.Contains(body, token) {
				t.Errorf("the refusal for ?%s carries the token", query)
			}
			if body := rec.Body.String(); strings.Contains(body, query) {
				t.Errorf("the refusal for ?%s prints the query", query)
			}
		}
	}
	// The log is the other place it could reach: it prints the path, which is
	// what tells a person which route was asked for, and never the query.
	written := f.log.String()
	if strings.Contains(written, token) {
		t.Errorf("the log carries the token")
	}
	if !strings.Contains(written, routeState) {
		t.Errorf("the log does not name the refused path: %q", written)
	}
}

func TestNoSnapshotBeforeTheFirstTickIsAState(t *testing.T) {
	f := newFixture(t)
	f.untick()

	answer := f.do(http.MethodGet, routeState, "", f.srv.Addr(), f.bearer())
	if answer.Code != http.StatusServiceUnavailable {
		t.Fatalf("before the first tick: %d, want %d", answer.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(answer.Body.String(), "tick") {
		t.Errorf("the 503 does not say why: %s", answer.Body.String())
	}
	// The handshake is answered even before the first cycle: it asks which
	// process holds the port, which is a question about the endpoint and not
	// about its state -- a reader that took this 503 for a dead orchestrator
	// would delete the registry file of a daemon that is starting up.
	if got := answer.Header().Get(headerOrchestrator); got != endpointID {
		t.Errorf("the 503 names the orchestrator as %q, want %q", got, endpointID)
	}
	if got := answer.Header().Get(headerNonce); got != f.srv.Nonce() || got == "" {
		t.Errorf("the 503 does not carry the nonce the endpoint was started with")
	}
	if got := f.do(http.MethodGet, routeStream, "", f.srv.Addr(), f.bearer()); got.Code != http.StatusServiceUnavailable {
		t.Errorf("the stream before the first tick: %d, want %d", got.Code, http.StatusServiceUnavailable)
	}
	// A state and not an exit: the same endpoint answers with a document once a
	// cycle has landed.
	f.tick(testState("wi-2"), tickedAt.Add(time.Minute))
	after := f.do(http.MethodGet, routeState, "", f.srv.Addr(), f.bearer())
	if after.Code != http.StatusOK {
		t.Fatalf("after the first tick: %d, want %d", after.Code, http.StatusOK)
	}
	if got, want := after.Body.String(), f.document(testState("wi-2")); got != want {
		t.Errorf("the document after the first tick is not the one the tick left:\n got %s\nwant %s", got, want)
	}
	if got := after.Header().Get(headerTick); got != tickedAt.Add(time.Minute).Format(time.RFC3339) {
		t.Errorf("the tick header is %q, want the server time of the tick", got)
	}
}

func TestTheRefusalNeverCarriesTheToken(t *testing.T) {
	f := newFixture(t)
	token := f.srv.Token()
	wrong, err := mint()
	if err != nil {
		t.Fatalf("minting a second token: %v", err)
	}
	attempts := map[string]*httptest.ResponseRecorder{
		"the token in the query":         f.do(http.MethodGet, routeState, "token="+token, f.srv.Addr(), f.bearer()),
		"the token with no scheme":       f.do(http.MethodGet, routeState, "", f.srv.Addr(), map[string]string{authorizationHeader: token}),
		"another scheme carrying it":     f.do(http.MethodGet, routeState, "", f.srv.Addr(), map[string]string{authorizationHeader: "Token " + token}),
		"basic auth carrying it":         f.do(http.MethodGet, routeState, "", f.srv.Addr(), map[string]string{authorizationHeader: "Basic " + token}),
		"a Host that is not the literal": f.do(http.MethodGet, routeState, "", "localhost:1", f.bearer()),
	}
	for name, rec := range attempts {
		if rec.Code < 400 {
			t.Errorf("%s was not refused: %d", name, rec.Code)
		}
		if body := rec.Body.String(); strings.Contains(body, token) {
			t.Errorf("%s: the refusal carries the endpoint's token", name)
		}
		// The credential the caller sent is not echoed either: a refusal that
		// quoted it would put it in whatever reads the answer.
		if body := rec.Body.String(); strings.Contains(body, wrong) {
			t.Errorf("%s: the refusal carries the credential it refused", name)
		}
	}
	if written := f.log.String(); strings.Contains(written, token) || strings.Contains(written, wrong) {
		t.Errorf("the log carries a credential: %q", written)
	}
	// A refusal is a refusal for the route asked for, and not a picture of the
	// request: the method and the path are the whole of what it says.
	rec := f.do(http.MethodGet, "/state/other", "", f.srv.Addr(), f.bearer())
	if rec.Code != http.StatusNotFound {
		t.Errorf("an unknown route: %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// TestTheStreamSendsAWholeDocumentPerMovedTick is the stream route's own
// behaviour, which the token case only reaches: a reader gets the state it
// connected for at once, then one whole document per tick whose state is not
// the one before, and nothing while the state stands.
func TestTheStreamSendsAWholeDocumentPerMovedTick(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := f.stream(ctx, f.bearer())
	if err != nil {
		t.Fatalf("opening the stream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("the stream's content type is %q", got)
	}
	events := bufio.NewReader(resp.Body)
	id, data := readEvent(t, events)
	if data != f.document(f.current()) {
		t.Fatalf("the first event is not the state the reader connected for: %s", data)
	}
	if id != tickedAt.Format(time.RFC3339) {
		t.Errorf("the event's id is %q, want the tick's server time", id)
	}

	// A tick that moved the state is one event, and the wait is bounded by
	// cancelling the stream rather than by a goroutine reading on the test's
	// behalf: a read from another goroutine could not fail the test.
	stop := time.AfterFunc(15*time.Second, cancel)
	defer stop.Stop()
	moved := testState("wi-1")
	moved.Rev = 2
	f.tick(moved, tickedAt.Add(time.Minute))
	if _, data := readEvent(t, events); data != f.document(moved) {
		t.Fatalf("the event after a moved tick is not the new document:\n got %s\nwant %s", data, f.document(moved))
	}
}
