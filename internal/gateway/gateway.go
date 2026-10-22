// Package gateway is the per-run budget gateway: a loopback proxy between
// Claude Code and the model API that holds the real credential, allows
// only the stage's pinned models and the request shapes the price table
// can bound, reserves each call's worst case against the run's cap before
// sending it, and settles the call from the usage the response reports.
//
// It never rewrites a body, never buffers a stream, never retries, and
// never logs a header or a body. Money is integer micro-dollars throughout.
//
// The retry is the gateway's and not the forwarder's: the helpers this
// package exports (export.go) are reused by the credentials service
// (internal/credsvc), which forwards a run's calls to one of several
// credentials and retries a call on the next one when a credential answers a
// usage limit -- once per credential, and only before the first byte of the
// answer has reached the client, so nothing a client has already seen is
// replayed. The in-runner gateway itself still never retries: the service's
// retry happens inside the one client request the gateway reserved for.
//
// A halt's Detail (a Refusal's, or HaltExternal's) is shown to the agent in
// the 403 every later call gets, as one line clipped to 300 bytes. The
// gateway does not redact it: callers must keep it free of secrets.
package gateway

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/pricing"
)

// Mode is how the gateway treats the run's cap.
type Mode string

const (
	// Observe accounts for every call and logs the ones Enforce would
	// refuse, but refuses nothing for money.
	Observe Mode = "observe"
	// Enforce refuses a call whose worst case doesn't fit under the cap,
	// and halts the run.
	Enforce Mode = "enforce"
)

// Upstream is where calls go and with what credential.
type Upstream struct {
	Kind string // "anthropic", "vertex", KindOAuth (tier A, oauth.go) or KindService (C1.15, service.go)
	// BaseURL overrides the real API's base for tests: "" is
	// https://api.anthropic.com, or the Vertex host of each call's
	// location. KindService requires it: it is the credentials service's own
	// URL, which has no default. It must be https, or http on a loopback
	// address.
	BaseURL string
	APIKey  string             // anthropic: the real key; KindOAuth: the subscription token, sent as its bearer token and only to Anthropic's own endpoint
	Token   oauth2.TokenSource // vertex: called per request (it caches)
	// VertexProject is the only GCP project a Vertex path may name.
	VertexProject string
	// VertexLocations are the locations a Vertex path may name:
	// CLOUD_ML_REGION plus every VERTEX_REGION_* value.
	VertexLocations []string
	// Service, with KindService, is how this job identifies itself to the
	// credentials service (C1.15, service.go). It is required for that kind
	// and must be nil for every other: the credential a service run's calls
	// are made with is the service's own, never one of the job's, and
	// validate refuses a service upstream that carries an in-job credential
	// (D143).
	Service *Service
}

// Options configure a gateway.
type Options struct {
	Upstream Upstream
	// Routes send the models they claim to a provider, each with its own
	// credential; every other model goes to Upstream. Only with the
	// anthropic upstream.
	Routes []Route
	Prices *pricing.Table
	Mode   Mode
	Cap    pricing.Micros // enforce: > 0; observe: 0 accounts only
	Log    *slog.Logger   // the runner's, which redacts; nil discards
	Client *http.Client   // nil: a default with no overall timeout (streams are long)
	// Lease, when set, is where the gateway's budget comes from: it holds
	// only what the lease granted, asks for more when a call doesn't fit,
	// and releases what is unused at Close. Cap is then ignored (the lease
	// owns every cap), and Mode only decides whether a cap refusal may
	// halt the run (enforce) or never does (observe). nil is M9a's static
	// Cap, unchanged.
	Lease Lease
	// EndStageWait bounds how long EndStage waits for a stage's calls in
	// flight; 0 is 30 s. Tests shorten it.
	EndStageWait time.Duration
	// OnServiceAnswer, when set, is called once for every answer the
	// credentials service gives (KindService, C1.15): the tier a Messages
	// call settles by (C1.16's ledger hook), the credential's name a run
	// records (C1.19) and the switch it logs, and the typed exhausted answer
	// the runner waits on (C1.17).
	//
	// It is called from concurrent calls, with no lock of this package's
	// held, so it must be safe for that; it is never called for an answer
	// from another upstream kind. A call it is not told about is the
	// answer's own fault (an answer this gateway could not read halts the
	// run instead) — never the hook's.
	OnServiceAnswer func(ServiceAnswer)
}

// DefaultGeos are the usage.inference_geo values a default request is
// served with (absent counts as ""). Any other is priced as a surprise.
var DefaultGeos = []string{"", "global"}

// MaxRequestBytes is the largest request body the gateway reads (the
// API's own limit); a larger one is refused with 413.
const MaxRequestBytes = 32 << 20

// endStageWait bounds how long EndStage waits for the stage's calls.
var endStageWait = 30 * time.Second

// Stage is what the gateway allows while a stage runs.
type Stage struct {
	Name            string // implement, review, fix
	Model           string // the role's model (main agent and subagents)
	Background      string // the background model; "" when unset
	MaxOutputTokens int64  // bounds max_tokens on Model's requests only; 0: none
}

// StageReport is what a stage's calls cost and what they tried that the
// gateway refused.
type StageReport struct {
	Calls int // calls sent upstream
	// Used is the money this stage's calls settled: tiers B and C, and
	// every call of a run with no cascade, which has no tier at all.
	Used pricing.Micros
	// Notional is what tier-A calls settled this stage: the run's
	// subscription usage, which is reported for visibility and counted
	// against no cap (C1.16, D140). It is disjoint from Used, and the
	// stage's whole model figure is their sum.
	Notional pricing.Micros
	// ByTier is the settled money by credential tier, keys "b" and "c"
	// (C1.16, D140): the per-call split the by-tier counters are written
	// from, and the closed set the database rules allow. A tier-A call is
	// Notional and is in no key here, and a call that did not go through
	// the service is in none either: it is real money but no tier.
	ByTier        map[string]pricing.Micros
	ByModel       map[string]pricing.Micros // by serving model
	ByRoute       map[string]pricing.Micros // by provider route (Claude calls have none)
	Reported      pricing.Micros            // what providers said their calls cost: for comparison, never charged
	Unreconciled  pricing.Micros            // charged from reservations, not from reported usage
	Overrun       pricing.Micros            // charged above the calls' reservations
	WouldHalt     int                       // observe: calls enforce would have refused
	UsageUnparsed int                       // 2xx calls settled at their reservation
	Tokens        int64                     // reported tokens: input, cache writes, cache reads and output
	Violations    []string                  // what the stage asked for that isn't allowed, first first
	Waited        int                       // calls told to retry (429) because in-flight calls held the budget
}

// Halt says why the gateway stopped forwarding calls.
type Halt struct {
	Reason string // "run_cap"; with a lease also repo_daily_cap, global_daily_cap, no_cap, kill_switch
	Scope  string // "run", "repo" or "global"; "" is "run"
	Detail string // "run cap $20.00 reached ($19.84 spent, $1.30 needed)"; with nothing spent, "cannot hold a call that needs up to"
	At     time.Time
}

// Ledger is the run's money: what the cap grants, what settled calls
// used, and what calls in flight hold in reserve.
//
// Used is the money a mixed run paid — tiers B and C — and Notional is the
// tier-A part of what its model calls cost, which is what the run's
// subscription spent rather than the run (C1.16, D140). They are disjoint:
// a reader that wants the run's whole model figure adds them, and one that
// wants what it owes reads Used. A run with no cascade has no tiers, so its
// Notional is zero and Used is exactly what it always was.
type Ledger struct{ Granted, Used, Notional, Reserved pricing.Micros }

// Server is a running gateway.
type Server struct {
	o      Options
	log    *slog.Logger
	token  string
	url    string
	client *http.Client
	srv    *http.Server
	ln     net.Listener

	base       context.Context // ends every upstream request when cancelled
	cancelBase context.CancelFunc
	handlers   sync.WaitGroup // every request being handled
	closeOnce  sync.Once
	closeErr   error

	haltCh chan Halt

	// hctx ends when HaltExternal runs: it cancels the upstream calls in
	// flight. A halt the gateway decides itself never does: calls already
	// holding a reservation finish.
	hctx  context.Context
	hstop context.CancelFunc
	// gctx is the lease's context: it ends only when Close's own wait
	// runs out, so a grant already in flight isn't cut off and lost.
	gctx  context.Context
	gstop context.CancelFunc

	grants sync.WaitGroup // the top-up goroutine, so Close sees its grant land

	countSlots chan struct{} // bounds the token counts in flight

	// mu guards everything below: one mutex, one ledger.
	mu       sync.Mutex
	used     pricing.Micros
	reserved pricing.Micros
	overrun  pricing.Micros // all calls', for the invariant check
	// notional is what tier-A calls settled this run: the subscription's
	// own usage, kept apart from used because it is not money and no cap
	// counts it (C1.16, D140).
	notional pricing.Micros
	// lastCacheRead, lastCacheKnown and lastModel are the last settled
	// call's cache-read tokens, whether its usage was read at all, and its
	// serving model: what the next answer's switch is estimated from
	// (F24). A call whose usage could not be read leaves lastCacheKnown
	// false, so the estimate that follows says "unmeasured" rather than
	// zero.
	lastCacheRead  int64
	lastModel      string
	lastCacheKnown bool
	halt           *Halt
	haltMsg        string
	wouldHalted    bool           // observe: enforce would have halted by now
	granted        pricing.Micros // lease: the sum of what the lease granted, less what was released
	flight         *topUp         // lease: the top-up in flight, if any
	stage          *stageState
	onLedger       func(Ledger, pricing.Micros) // tests: called after every ledger change
}

// stageState is a running stage; its calls hold it until they settle.
type stageState struct {
	st    Stage
	calls sync.WaitGroup
	rep   StageReport // guarded by Server.mu
}

// Start listens on 127.0.0.1 on a free port and serves until Close. The
// context bounds the start only.
func Start(ctx context.Context, o Options) (*Server, error) {
	if err := o.validate(); err != nil {
		return nil, err
	}
	o.Routes = copyRoutes(o.Routes)
	var tok [32]byte
	if _, err := rand.Read(tok[:]); err != nil {
		return nil, fmt.Errorf("gateway: token: %w", err)
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("gateway: listen: %w", err)
	}
	s := &Server{
		o:      o,
		log:    o.Log,
		token:  hex.EncodeToString(tok[:]),
		url:    "http://" + ln.Addr().String(),
		client: upstreamClient(o.Client),
		ln:     ln,
		haltCh: make(chan Halt, 1),

		countSlots: make(chan struct{}, maxCountsInFlight),
	}
	if s.log == nil {
		s.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if o.Upstream.Kind == KindOAuth {
		// Tier A (oauth.go): the read timeout a subscription call is held to.
		s.client = oauthUpstreamClient(s.client)
	}
	if o.Upstream.Kind == KindService {
		// C1.15 (service.go): the wait for the service's response headers is
		// bounded, and nothing else is. The client's own timeout stays as it
		// is -- none: the call is cancelled by the client that asked for it,
		// by the stage's deadline and by Close, and the service may stream a
		// model answer for minutes -- and a service that is not reachable at
		// all fails the connection instead (D143's halt). What is left
		// unbounded without this is a connection that never answers and
		// never fails either, which would hang the call until the stage's
		// deadline instead of halting the run.
		s.client = serviceUpstreamClient(s.client)
	}
	s.base, s.cancelBase = context.WithCancel(context.WithoutCancel(ctx))
	s.hctx, s.hstop = context.WithCancel(s.base)
	s.gctx, s.gstop = context.WithCancel(context.WithoutCancel(ctx))
	s.srv = &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 30 * time.Second,
		ErrorLog:          log.New(io.Discard, "", 0), // a client's broken connection isn't the run's news
	}
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

func (o Options) validate() error {
	if o.Prices == nil {
		return errors.New("gateway: no price table")
	}
	switch o.Mode {
	case Observe:
		if o.Cap < 0 {
			return fmt.Errorf("gateway: cap %d µ$ is negative", o.Cap)
		}
	case Enforce:
		if o.Cap <= 0 && o.Lease == nil {
			return errors.New("gateway: enforce needs a cap above 0")
		}
	default:
		return fmt.Errorf("gateway: unknown mode %q", o.Mode)
	}
	u := o.Upstream
	if err := checkBaseURL(u.BaseURL); err != nil {
		return err
	}
	if err := validateRoutes(u, o.Routes); err != nil {
		return err
	}
	switch u.Kind {
	case "anthropic":
		// The key is optional: a run whose models all route through
		// providers has none, and a call that needs this upstream without
		// a key is refused by name (missingAnthropicKey) rather than at
		// startup. A stored key is mounted whenever the secret has a
		// version, so this is a fact about data, not about routing.
	case KindOAuth:
		// Tier A (oauth.go). The credential is not optional here, as the
		// Anthropic key is: this kind exists only to attach one, so a
		// gateway without it is a mistake, refused at start rather than at
		// every call. The base must be Anthropic's own endpoint (D133).
		if err := checkOAuthBaseURL(u.BaseURL); err != nil {
			return err
		}
		if u.APIKey == "" {
			return errors.New("gateway: the OAuth upstream has no credential")
		}
	case KindService:
		// C1.15 (service.go). The destination is required and has no
		// default: it is the installation's own credentials service.
		if u.BaseURL == "" {
			return errors.New("gateway: the service upstream has no base URL")
		}
		// D143, structurally: a service run's calls are made with the
		// credential the service attaches, and an in-job credential beside
		// it is a fallback waiting to happen -- the one thing the ruling
		// refuses. There is nothing to fall back with here, so a gateway
		// built with one is a mistake, refused at start rather than the
		// first time the service is unreachable.
		if u.APIKey != "" || u.Token != nil {
			return errors.New("gateway: the service upstream carries a credential of the job's own: a cascade run's calls are made with the credential the service attaches, and never with one the job holds (D143)")
		}
		if u.Service == nil {
			return errors.New("gateway: the service upstream has no identity")
		}
		if u.Service.IDToken == nil {
			return errors.New("gateway: the service upstream has no job identity (the ID token Cloud Run IAM admits)")
		}
		if u.Service.RunIDToken == nil {
			return errors.New("gateway: the service upstream has no run identity")
		}
		if !validJobName(u.Service.Job) {
			return errors.New("gateway: the service upstream reports no Cloud Run job name")
		}
	case "vertex":
		if u.Token == nil {
			return errors.New("gateway: the vertex upstream has no token source")
		}
		if u.VertexProject == "" {
			return errors.New("gateway: the vertex upstream has no GCP project")
		}
		if len(u.VertexLocations) == 0 {
			return errors.New("gateway: the vertex upstream has no locations")
		}
		for _, l := range u.VertexLocations {
			if !locationRE.MatchString(l) {
				return fmt.Errorf("gateway: vertex location %q isn't a location name", l)
			}
		}
	default:
		return fmt.Errorf("gateway: unknown upstream %q", u.Kind)
	}
	return nil
}

// checkBaseURL allows "", https on any host, or http on a loopback host,
// with no path, so the real credential never travels in the clear.
func checkBaseURL(s string) error {
	if s == "" {
		return nil
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("gateway: upstream base %q must be a scheme and a host only", s)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if ip := net.ParseIP(u.Hostname()); (ip != nil && ip.IsLoopback()) || u.Hostname() == "localhost" {
			return nil
		}
	}
	return fmt.Errorf("gateway: upstream base %q must be https, or http on a loopback address", s)
}

// upstreamClient never follows a redirect (it would carry the credential
// elsewhere) and never asks for compression on its own.
func upstreamClient(c *http.Client) *http.Client {
	var out http.Client
	if c != nil {
		out = *c
	} else {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.DisableCompression = true
		out.Transport = t
	}
	out.Jar = nil
	out.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &out
}

// URL is the gateway's base URL, "http://127.0.0.1:<port>".
func (s *Server) URL() string { return s.url }

// Token is the run's gateway token (64 hex characters): the x-api-key an
// anthropic-upstream client must send.
func (s *Server) Token() string { return s.token }

// Halted delivers the halt once, then is closed.
func (s *Server) Halted() <-chan Halt { return s.haltCh }

// Ledger is a snapshot of the run's money. With a lease Granted is what
// the lease granted (less what Close released); with a static cap in
// observe mode it is unbounded (the largest Micros).
func (s *Server) Ledger() Ledger {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ledgerLocked()
}

func (s *Server) ledgerLocked() Ledger {
	if s.o.Lease != nil {
		return Ledger{Granted: s.granted, Used: s.used, Notional: s.notional, Reserved: s.reserved}
	}
	g := pricing.Micros(math.MaxInt64)
	if s.o.Mode == Enforce {
		g = s.o.Cap
	}
	return Ledger{Granted: g, Used: s.used, Notional: s.notional, Reserved: s.reserved}
}

// BeginStage starts allowing st's models. A stage still running is ended
// without waiting.
func (s *Server) BeginStage(st Stage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stage = &stageState{st: st, rep: StageReport{
		ByTier:  map[string]pricing.Micros{},
		ByModel: map[string]pricing.Micros{},
		ByRoute: map[string]pricing.Micros{},
	}}
}

// EndStage stops allowing calls, waits (at most 30 s) for the stage's
// calls in flight to settle, and reports the stage. A call that settles
// later still counts in the Ledger, not in this report.
func (s *Server) EndStage() StageReport {
	s.mu.Lock()
	st := s.stage
	s.stage = nil
	s.mu.Unlock()
	if st == nil {
		return StageReport{ByTier: map[string]pricing.Micros{}, ByModel: map[string]pricing.Micros{}, ByRoute: map[string]pricing.Micros{}}
	}
	done := make(chan struct{})
	go func() { st.calls.Wait(); close(done) }()
	wait := endStageWait
	if s.o.EndStageWait > 0 {
		wait = s.o.EndStageWait
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		s.log.Warn("budget: stage ended with calls still in flight", "stage", st.st.Name)
	}
	rep := s.stageReport(st)
	s.reportStage(rep)
	return rep
}

func (s *Server) stageReport(st *stageState) StageReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	rep := st.rep
	rep.ByTier = make(map[string]pricing.Micros, len(st.rep.ByTier))
	for k, v := range st.rep.ByTier {
		rep.ByTier[k] = v
	}
	rep.ByModel = make(map[string]pricing.Micros, len(st.rep.ByModel))
	for k, v := range st.rep.ByModel {
		rep.ByModel[k] = v
	}
	rep.ByRoute = make(map[string]pricing.Micros, len(st.rep.ByRoute))
	for k, v := range st.rep.ByRoute {
		rep.ByRoute[k] = v
	}
	rep.Violations = append([]string(nil), st.rep.Violations...)
	return rep
}

// enterStage returns the running stage with one more call held, or nil.
func (s *Server) enterStage() *stageState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stage != nil {
		s.stage.calls.Add(1)
	}
	return s.stage
}

// violationNow records what a stage asked for that isn't allowed, on the
// running stage: for a path that has no stage handle of its own (the token
// count, which takes none). No stage is running: nothing to record.
func (s *Server) violationNow(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stage != nil {
		s.violationLocked(s.stage, v)
	}
}

// violationLocked is the append violationNow and violation share; the caller
// holds s.mu.
func (s *Server) violationLocked(st *stageState, v string) {
	if !contains(st.rep.Violations, v) {
		st.rep.Violations = append(st.rep.Violations, v)
	}
}

// violation records what a stage asked for that isn't allowed.
func (s *Server) violation(st *stageState, v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.violationLocked(st, v)
}

// Close cancels every upstream request in flight (each settles as its
// state says), then stops listening and waits for the handlers. With a
// lease it then releases what the run granted and didn't use, once.
func (s *Server) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		s.cancelBase()
		if err := s.srv.Shutdown(ctx); err != nil {
			_ = s.srv.Close()
			s.closeErr = fmt.Errorf("gateway: close: %w", err)
		}
		done := make(chan struct{})
		go func() { s.handlers.Wait(); s.grants.Wait(); close(done) }()
		select {
		case <-done:
		case <-ctx.Done():
			s.gstop() // give up on a grant still in flight
			if s.closeErr == nil {
				s.closeErr = fmt.Errorf("gateway: close: %w", ctx.Err())
			}
		}
		defer s.gstop()
		if err := s.releaseUnused(ctx); err != nil && s.closeErr == nil {
			s.closeErr = err
		}
	})
	return s.closeErr
}

// route is where a request goes.
type route struct {
	count     bool   // a token count: free, forwarded without a reservation
	upstream  string // the upstream URL, query included
	pathModel string // vertex: the model the path names
	// anthropic upstream: the request's path and query, so a pinned
	// provider model can be sent to its own base instead (see withRoute).
	path, query string
	provider    *Route // the provider the model is routed to; nil: Options.Upstream
}

// ServeHTTP routes on URL.Path only; the raw query is forwarded as is.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handlers.Add(1)
	defer s.handlers.Done()
	if r.URL.Path == "/api/hello" && r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	var rt route
	switch s.o.Upstream.Kind {
	case "anthropic", KindOAuth, KindService:
		// Tier A (oauth.go) and the credentials service (C1.15, service.go)
		// serve the same paths, with the same gateway token, as the
		// Anthropic upstream: only the destination and the credential
		// differ. A service upstream's base URL is required and is the
		// service's own, so anthropicBase is the service's base for it --
		// the function is "the base this upstream sends to" and nothing
		// about Anthropic's host is assumed below.
		p := r.URL.Path
		if r.Method != http.MethodPost || (p != "/v1/messages" && p != "/v1/messages/count_tokens") {
			writeError(w, http.StatusNotFound, "not_found_error", "fugaro: the budget gateway doesn't serve "+r.Method+" "+logValue(p))
			return
		}
		if !s.authorized(r) {
			writeError(w, http.StatusUnauthorized, "authentication_error", "fugaro: the gateway token is missing or wrong")
			return
		}
		rt = route{count: p == "/v1/messages/count_tokens", upstream: anthropicBase(s.o.Upstream) + p + s.upstreamQuery(r), path: p, query: s.upstreamQuery(r)}
	case "vertex":
		var ok bool
		var why string
		if rt, ok, why = s.vertexRoute(r); !ok {
			writeError(w, http.StatusNotFound, "not_found_error", "fugaro: "+why)
			return
		}
	}
	if msg, halted := s.haltedMessage(); halted {
		writeHalt(w, msg)
		if !rt.count {
			s.logCall(callLog{stage: s.stageName(), model: rt.pathModel, status: http.StatusForbidden, pricedAs: pricedTable, settled: settledZero,
				sessionID: r.Header.Get("x-claude-code-session-id"), agentID: r.Header.Get("x-claude-code-agent-id")})
		}
		return
	}
	if rt.count {
		s.forwardFree(w, r, rt)
		return
	}
	s.handleMessages(w, r, rt)
}

func (s *Server) authorized(r *http.Request) bool {
	got := r.Header.Values("x-api-key")
	return len(got) == 1 && subtle.ConstantTimeCompare([]byte(got[0]), []byte(s.token)) == 1
}

func (s *Server) stageName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stage == nil {
		return ""
	}
	return s.stage.st.Name
}

func anthropicBase(u Upstream) string {
	if u.BaseURL != "" {
		return trimSlash(u.BaseURL)
	}
	return "https://api.anthropic.com"
}

func query(r *http.Request) string {
	if r.URL.RawQuery == "" {
		return ""
	}
	return "?" + r.URL.RawQuery
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

// writeError answers with an API error body the gateway made itself.
// Only a gateway-made error the client may retry (an upstream failure,
// a busy budget, an unreachable budget backend)
// leaves out x-should-retry: false.
func writeError(w http.ResponseWriter, status int, typ, message string) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	if status != http.StatusBadGateway && status != http.StatusTooManyRequests && status != http.StatusServiceUnavailable {
		h.Set("x-should-retry", "false")
	}
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":%s,"message":%s}}`, jsonString(typ), jsonString(message))
}

func writeHalt(w http.ResponseWriter, msg string) {
	writeError(w, http.StatusForbidden, "permission_error", msg)
}
