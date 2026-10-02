package budget

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The halt reasons a session raises on its own. The rest (run_cap,
// repo_daily_cap, ...) come from Evaluate and are the lease's refusals.
const (
	ReasonBudgetUnavailable  Reason = "budget_unavailable"
	ReasonBudgetTokenExpired Reason = "budget_token_expired"
)

// Halt is a cause to stop the run that the session found by itself: a kill
// switch, the grace running out, a revoked identity. Detail is shown to
// people (the report) and, through the gateway, to the agent: it never holds
// a credential.
type Halt struct {
	Reason Reason
	Scope  Scope
	Detail string
}

// ErrGraceExpired is returned by Open and Admit when the backend stayed
// unreachable for the whole grace (D14) before the run began.
var ErrGraceExpired = errors.New("budget: the backend was unreachable for the whole grace period")

// ErrPermissionDenied is returned at bootstrap when the database refuses the
// run's credential: not an outage, a wrong database or a rules mismatch.
var ErrPermissionDenied = errors.New("budget: the database refused the run's credential")

// Config configures a run's budget session. Slug and Run are the repository
// slug and run id as the launcher put them in the token (they must be valid
// database keys as they are).
type Config struct {
	RTDBURL string
	APIKey  string
	// IdentityURL and SecureTokenURL are Identity Toolkit and Secure Token
	// (tests); empty means Google's. HTTP serves both and the database.
	IdentityURL, SecureTokenURL string
	HTTP                        *http.Client

	Slug, Run string
	// Deadline is when the run must be over; it is written once as the
	// ledger's exp.
	Deadline time.Time

	// The run's own limits from the policy merge (M9a.1). PolicyCap is the
	// effective per-run cap (0: none) and CommittedDaily the committed
	// per_day_usd, applied to the repository's day counter alone (R1).
	// LocalEnforce is whether the merged mode is enforce: then both bite
	// whatever the project's mode says; otherwise they are advisory.
	PolicyCap, CommittedDaily Micros
	LocalEnforce              bool

	// Grace is the D14 window (zero: DefaultGrace); HeartbeatEvery the
	// registry and usage cadence (zero: 15 s).
	Grace          time.Duration
	HeartbeatEvery time.Duration
	// Now is the local clock, used until the database has told its own
	// (zero: time.Now).
	Now func() time.Time

	// Register receives every secret the session creates before use.
	Register func(string)
	Log      *slog.Logger
	// OnHalt is told, once, the cause to stop the run. It runs on the
	// session's own goroutines and must not block.
	OnHalt func(Halt)

	// Test knobs; zero is production.
	RetryEvery   time.Duration // wait between bootstrap attempts (5 s)
	CapRetryWait time.Duration // before halting on a daily cap (20 s)
	StaleBackoff time.Duration // base wait between stale retries (40 ms)
	KillRestart  time.Duration // before reopening a cancelled kill stream (30 s)
	// StreamBackoff and StreamIdle tune the database streams (1 s..30 s, 90 s).
	StreamBackoffMin, StreamBackoffMax, StreamIdle time.Duration
}

// maxStale is how many times in a row a lease or report write may be denied
// while the local predicate says it should pass (a stale read) before the
// backend counts as unavailable.
const maxStale = 8

// Session is a run's connection to the budget backend: its Firebase
// identity, the database client, the registry entry, the kill watch and the
// lease. Create one with Open.
type Session struct {
	cfg    Config
	db     *rtdb.Client
	tok    *token.Session
	claims token.Claims
	grace  *Grace
	log    *slog.Logger

	// wmu serialises this run's read-modify-write cycles on its own
	// ledgers: a lease, a release and a usage report never interleave.
	wmu sync.Mutex

	mu       sync.Mutex
	entry    AgentEntry
	haltOnce sync.Once
	halted   bool
	used     func() Micros
	leased   map[int64]bool // days this run holds a lease on
	granted  Micros         // everything the leases granted
	released Micros         // everything released
	unrel    Micros         // a release that failed, to try again at Finish
	pend     pending
	started  bool
	stopped  bool
	exp      bool // exp written
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	caps     Caps // the last config read, for the heartbeat's limits

	advised       map[Reason]bool // observe-mode advisories already logged
	notionalTotal Micros          // an oauth run's notional so far
}

// Open exchanges the run's custom token for its Firebase identity and
// returns the session. An unreachable backend is retried until the grace runs
// out (ErrGraceExpired); a token minted more than an hour ago is
// token.ErrTokenExpired; anything else is not an outage and returns at once.
// The custom token is registered with cfg.Register before its first use.
func Open(ctx context.Context, cfg Config, custom string) (_ *Session, rerr error) {
	if cfg.Slug == "" || cfg.Run == "" || Key(cfg.Slug) != cfg.Slug || Key(cfg.Run) != cfg.Run {
		return nil, fmt.Errorf("budget: the repository slug %q or run id %q is not a valid database key", cfg.Slug, cfg.Run)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.HeartbeatEvery <= 0 {
		cfg.HeartbeatEvery = 15 * time.Second
	}
	if cfg.RetryEvery <= 0 {
		cfg.RetryEvery = 5 * time.Second
	}
	if cfg.CapRetryWait <= 0 {
		cfg.CapRetryWait = 20 * time.Second
	}
	if cfg.StaleBackoff <= 0 {
		cfg.StaleBackoff = 40 * time.Millisecond
	}
	if cfg.KillRestart <= 0 {
		cfg.KillRestart = 30 * time.Second
	}
	log := cfg.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	g := NewGrace(cfg.Grace)
	s := &Session{cfg: cfg, grace: g, log: log, leased: map[int64]bool{}}
	defer func() {
		if rerr != nil {
			g.Stop()
		}
	}()
	tcfg := token.Config{
		APIKey: cfg.APIKey, IdentityURL: cfg.IdentityURL, SecureTokenURL: cfg.SecureTokenURL, HTTP: cfg.HTTP,
		Now: cfg.Now, Register: cfg.Register,
		OnResult: func(op string, err error) {
			switch {
			case err == nil:
				g.OK(op)
			case errors.Is(err, token.ErrUnavailable):
				g.Fail(op, err)
			}
		},
	}
	for {
		tok, err := token.Exchange(ctx, tcfg, custom)
		if err == nil {
			s.tok = tok
			break
		}
		if !errors.Is(err, token.ErrUnavailable) {
			return nil, err
		}
		if werr := s.wait(ctx, cfg.RetryEvery); werr != nil {
			return nil, werr
		}
	}
	s.claims = s.tok.Claims()
	if s.claims.Slug != cfg.Slug || s.claims.Run != cfg.Run {
		return nil, token.ErrTokenMismatch
	}
	var opts []rtdb.Option
	if cfg.HTTP != nil {
		opts = append(opts, rtdb.WithHTTPClient(cfg.HTTP))
	}
	if cfg.StreamBackoffMax > 0 {
		opts = append(opts, rtdb.WithStreamBackoff(cfg.StreamBackoffMin, cfg.StreamBackoffMax))
	}
	if cfg.StreamIdle > 0 {
		opts = append(opts, rtdb.WithStreamIdleTimeout(cfg.StreamIdle))
	}
	db, err := rtdb.New(cfg.RTDBURL, rtdb.Auth{IDToken: s.tok.Token}, opts...)
	if err != nil {
		return nil, err
	}
	s.db = db
	return s, nil
}

// wait sleeps d, or returns early with ErrGraceExpired when the grace runs
// out or the context error when it ends.
func (s *Session) wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-s.grace.Expired():
		return s.graceErr()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Session) graceErr() error {
	src, err := s.grace.Cause()
	return fmt.Errorf("%w (%s: %v)", ErrGraceExpired, src, err)
}

// RequestedBy is the launcher's identity, the token's rb claim.
func (s *Session) RequestedBy() string { return s.claims.RB }

// Grace is the session's outage clock.
func (s *Session) Grace() *Grace { return s.grace }

// now is the database's clock when it has told one, else the local clock.
func (s *Session) now() time.Time {
	if t, ok := s.db.ServerNow(); ok {
		return t
	}
	return s.cfg.Now().UTC()
}

// Now exposes the session's clock (the database's day starts when its clock
// says so).
func (s *Session) Now() time.Time { return s.now() }

// outage reports whether err means the backend could not be reached or
// understood, as opposed to the run having been told no. Everything that is
// not a refusal is fail-closed: a context error is the caller leaving and is
// never an outage.
func outage(err error) bool {
	return err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

// dbSources are the outage clocks of calls to the database. Any call that
// succeeds proves the database reachable, and so ends all of them: a lease
// that failed once must not keep a clock running that only another lease
// could stop, or a long quiet gap (verify, an agent that gave up) on a
// healthy backend would halt the run. A total outage fails every source and
// still halts at the window. The Firebase Auth sources (exchange, refresh)
// are a different backend and are not cleared by the database.
var dbSources = []string{"config", "registry", "lease", "report", "heartbeat", "kill-poll"}

// dbOK records a successful call to the database. It does not end the
// kill-poll clock: only a successful kill read does (checkKills), so a
// kill read that keeps failing cannot be hidden by the heartbeat's writes.
func (s *Session) dbOK() {
	for _, k := range dbSources {
		if k == "kill-poll" {
			continue
		}
		s.grace.OK(k)
	}
}

// streamFailed is a kill stream's error. SSE surviving Cloud Run egress is
// unverified (A-F2), and the heartbeat's REST poll backstops the stream, so a
// stream error counts toward the grace only while the poll fails as well.
func (s *Session) streamFailed(source string, err error) {
	if s.grace.FailingSource("kill-poll") {
		s.grace.Fail(source, err)
		return
	}
	s.log.Warn("budget: a kill stream failed; the poll covers it", "source", source, "error", err.Error())
}

// observe reports a backend call's outcome to the grace and returns err.
func (s *Session) observe(source string, err error) error {
	switch {
	case err == nil:
		s.dbOK()
	case outage(err):
		s.grace.Fail(source, err)
	}
	return err
}

// retryBoot repeats fn while the backend is unreachable, until the grace
// runs out. It is for the steps before the run starts, where waiting is the
// only thing to do. A refusal of the run's credential is not an outage.
func (s *Session) retryBoot(ctx context.Context, source string, fn func() error) error {
	for {
		err := fn()
		if err == nil {
			s.dbOK()
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, rtdb.ErrPermission) {
			return fmt.Errorf("%w: %v", ErrPermissionDenied, err)
		}
		if !outage(err) {
			return err
		}
		s.grace.Fail(source, err)
		if werr := s.wait(ctx, s.cfg.RetryEvery); werr != nil {
			return werr
		}
	}
}

// Snapshot is everything under /config a run may read, as one reading.
type Snapshot struct {
	Caps  Caps
	Kills Kills
}

// readConfig reads the run's slice of /config: the nodes rules let it read.
func (s *Session) readConfig(ctx context.Context) (Snapshot, error) {
	var (
		sn  Snapshot
		g   GlobalCaps
		d   DefaultCaps
		rc  RepoCaps
		lim Limits
		mod string
		kg  killNode
		kr  killNode
	)
	var gg, gd, gr, gl, gm, gkg, gkr bool
	jobs := []struct {
		path string
		out  any
		got  *bool
	}{
		{PathCapsGlobal, &g, &gg}, {PathCapsDefaults, &d, &gd}, {PathCapsRepo(s.cfg.Slug), &rc, &gr},
		{PathLimits, &lim, &gl}, {PathMode, &mod, &gm}, {PathKillGlobal, &kg, &gkg}, {PathKillRepo(s.cfg.Slug), &kr, &gkr},
	}
	errs := make([]error, len(jobs))
	var wg sync.WaitGroup
	for i := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			found, err := s.db.Get(ctx, jobs[i].path, jobs[i].out)
			*jobs[i].got = found
			errs[i] = err
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return sn, firstClassified(errs)
	}
	if gg {
		sn.Caps.Global = &g
	}
	if gd {
		sn.Caps.Defaults = &d
	}
	if gr {
		sn.Caps.Repo = &rc
	}
	if gl {
		sn.Caps.Limits = &lim
	}
	if gm {
		sn.Caps.Mode = mod
	}
	if gkg {
		sn.Kills.Global = &kg.Kill
	}
	if gkr {
		sn.Kills.Repo = &kr.Kill
	}
	s.mu.Lock()
	s.caps = sn.Caps
	s.mu.Unlock()
	return sn, nil
}

// firstClassified picks the error that decides how a failed multi-read is
// treated: a cancelled context first, then a refusal, then any other.
func firstClassified(errs []error) error {
	var first error
	for _, e := range errs {
		if e == nil {
			continue
		}
		if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
			return e
		}
		if errors.Is(e, rtdb.ErrPermission) {
			return e
		}
		if first == nil {
			first = e
		}
	}
	return first
}

// readState reads the four nodes a lease or release for day is judged
// against.
func (s *Session) readState(ctx context.Context, day int64) (State, error) {
	var st State
	jobs := []struct {
		path string
		out  any
	}{
		{PathRun(s.cfg.Slug, s.cfg.Run), &st.Run},
		{PathSpendRun(day, s.cfg.Slug, s.cfg.Run), &st.DayRun},
		{PathSpendRepo(day, s.cfg.Slug), &st.Repo},
		{PathSpendGlobal(day), &st.Global},
	}
	errs := make([]error, len(jobs))
	var wg sync.WaitGroup
	for i := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = s.db.Get(ctx, jobs[i].path, jobs[i].out)
		}()
	}
	wg.Wait()
	if errors.Join(errs...) != nil {
		return State{}, firstClassified(errs)
	}
	return st, nil
}

// AdmitOptions say what the run needs from the backend.
type AdmitOptions struct {
	// Gateway: the run's model calls are leased (api-key and vertex). An
	// oauth run has no lease and is checked for kill switches only.
	Gateway bool
	// LocalEnforce: the run's merged mode is enforce, whatever the project's.
	LocalEnforce bool
	// PolicyCap is the run's effective per-run cap from the policy merge (0:
	// none).
	PolicyCap Micros
}

// Admit reads the caps, the mode and the kill switches and decides whether
// the run may begin: a kill switch halts it, and an enforcing run with no
// cap halts as no_cap. A nil Halt with a nil error means go ahead. It
// retries an unreachable backend until the grace runs out (ErrGraceExpired).
func (s *Session) Admit(ctx context.Context, o AdmitOptions) (*Halt, error) {
	var sn Snapshot
	err := s.retryBoot(ctx, "config", func() (err error) {
		sn, err = s.readConfig(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}
	if h := killHalt(sn.Kills); h != nil {
		return h, nil
	}
	// Rules refuse every lease, and every spend or notional report is bounded
	// by it, when the largest single lease is unset: in every mode and for
	// every auth, oauth included.
	if _, ok := sn.Caps.MaxReserve(); !ok {
		return &Halt{Reason: ReasonNoCap, Scope: ScopeGlobal,
			Detail: "limits.maxReserveMicros is not set in the budget database, so nothing can be leased or reported (an operator runs fugaro init --firebase again)"}, nil
	}
	if !o.Gateway {
		return nil, nil
	}
	if sn.Caps.Enforcing() {
		// The rules compare all three caps on every lease.
		if _, ok := sn.Caps.PerRun(); !ok {
			d := noCap(sn.Caps, "per-run")
			return &Halt{Reason: ReasonNoCap, Scope: d.Scope, Detail: d.Detail}, nil
		}
		if _, ok := sn.Caps.RepoDaily(); !ok {
			return &Halt{Reason: ReasonNoCap, Scope: ScopeRepo, Detail: "no daily cap is set for this repository and there is no default (fugaro budget set --defaults --repo-daily)"}, nil
		}
		if _, ok := sn.Caps.GlobalDaily(); !ok {
			return &Halt{Reason: ReasonNoCap, Scope: ScopeGlobal, Detail: "no global daily cap is set (fugaro budget set --global --daily)"}, nil
		}
	} else if o.LocalEnforce {
		// The database's caps are advisory, but this run enforces: it needs
		// a per-run cap from somewhere.
		if _, ok := sn.Caps.PerRun(); !ok && o.PolicyCap <= 0 {
			return &Halt{Reason: ReasonNoCap, Scope: ScopeRun,
				Detail: "budget.mode is enforce but no per-run cap is set (budget.per_run_usd, or fugaro budget set --defaults --repo-per-run and --global --per-run)"}, nil
		}
	}
	return nil, nil
}

// killHalt is the halt for a switch that is on: the global switch first.
func killHalt(k Kills) *Halt {
	if k.Global != nil && k.Global.On {
		return &Halt{Reason: ReasonKillSwitch, Scope: ScopeGlobal, Detail: "the global kill switch is on" + killNote(clean(*k.Global))}
	}
	if k.Repo != nil && k.Repo.On {
		return &Halt{Reason: ReasonKillSwitch, Scope: ScopeRepo, Detail: "this repository's kill switch is on" + killNote(clean(*k.Repo))}
	}
	return nil
}

// clean strips what must not reach a terminal or a pull request from the
// text an admin typed into a switch.
func clean(k Kill) Kill {
	f := func(s string) string {
		s = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, s)
		s = strings.TrimSpace(s)
		if len(s) > 200 {
			s = strings.ToValidUTF8(s[:200], "")
		}
		return s
	}
	k.By, k.Reason = f(k.By), f(k.Reason)
	return k
}

// halt delivers the first halt cause to OnHalt.
func (s *Session) halt(h Halt) {
	s.haltOnce.Do(func() {
		s.mu.Lock()
		s.halted = true
		s.entry.Halted = string(h.Reason)
		s.mu.Unlock()
		s.log.Warn("budget: halting the run", "reason", string(h.Reason), "scope", string(h.Scope), "detail", h.Detail)
		if s.cfg.OnHalt != nil {
			s.cfg.OnHalt(h)
		}
	})
}

// safely runs fn on the session's goroutine and turns a panic (a path
// builder given an empty segment, say) into a fail-closed halt instead of
// a crash.
func (s *Session) safely(what string, fn func()) {
	defer func() {
		if p := recover(); p != nil {
			s.log.Error("budget: internal error", "where", what, "panic", fmt.Sprint(p))
			s.halt(Halt{Reason: ReasonBudgetUnavailable, Scope: ScopeRun, Detail: "the budget session failed internally (" + what + "); the run is stopped rather than left unbudgeted"})
		}
	}()
	fn()
}

// SetUsage tells the session where the run's settled spend is read from
// (the gateway's ledger).
func (s *Session) SetUsage(used func() Micros) {
	s.mu.Lock()
	s.used = used
	s.mu.Unlock()
}

// Lease is the gateway's budget source for this run.
func (s *Session) Lease() gateway.Lease { return &leaseSource{s: s} }

// GrantedMicros and ReleasedMicros are what the leases granted and gave back
// so far, for the run record.
func (s *Session) GrantedMicros() Micros  { s.mu.Lock(); defer s.mu.Unlock(); return s.granted }
func (s *Session) ReleasedMicros() Micros { s.mu.Lock(); defer s.mu.Unlock(); return s.released }
