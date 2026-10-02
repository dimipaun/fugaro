package budget_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

const (
	slug        = "aurora-app"
	runID       = "20261002-120000-ab12"
	otherRun    = "20261002-120500-cd34"
	signerEmail = "fugaro-token-signer@aurora-fp.iam.gserviceaccount.com"
	apiKey      = "AIzaSyFakeWebApiKeyForTests000000000"
	rb          = "dev@example.invalid"
)

// noon is the database's clock in most tests.
var noon = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func today() int64 { return budget.Day(noon) }

// fixture is a run's whole backend: a database seeded as `fugaro init
// --firebase` leaves it, and the Firebase Auth fakes.
type fixture struct {
	t     *testing.T
	db    *gcpfake.RTDB
	iam   *gcpfake.IAMCredentials
	itk   *gcpfake.IdentityToolkit
	cfg   budget.Config
	halts chan budget.Halt

	mu      sync.Mutex
	secrets []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, halts: make(chan budget.Halt, 8)}
	f.db = gcpfake.NewRTDB(t)
	f.db.SetClock(func() time.Time { return noon })
	f.iam = gcpfake.NewIAMCredentials(t)
	f.iam.AddSigner(signerEmail)
	f.itk = gcpfake.NewIdentityToolkit(t, f.iam, apiKey, "aurora-fp")
	f.db.Set("fugaro/project", "aurora")
	f.db.Set(budget.PathMode, "enforce")
	f.db.Set(budget.PathLimits, budget.Limits{MaxReserveMicros: ptr(5_000_000)})
	f.db.Set(budget.PathCapsGlobal, budget.GlobalCaps{DailyMicros: ptr(150_000_000), PerRunMicros: ptr(20_000_000)})
	f.db.Set(budget.PathCapsRepo(slug), budget.RepoCaps{DailyMicros: ptr(60_000_000), PerRunMicros: ptr(20_000_000), Repo: "acme/app"})
	f.cfg = budget.Config{
		RTDBURL: f.db.URL, APIKey: apiKey, IdentityURL: f.itk.URL, SecureTokenURL: f.itk.URL,
		Slug: slug, Run: runID, Deadline: time.Now().Add(2 * time.Hour),
		Grace: 600 * time.Millisecond, HeartbeatEvery: 40 * time.Millisecond, RetryEvery: 20 * time.Millisecond,
		CapRetryWait: 30 * time.Millisecond, StaleBackoff: 2 * time.Millisecond, KillRestart: 100 * time.Millisecond,
		StreamBackoffMin: 10 * time.Millisecond, StreamBackoffMax: 50 * time.Millisecond, StreamIdle: 5 * time.Second,
		Register: f.register,
		OnHalt:   func(h budget.Halt) { f.halts <- h },
	}
	return f
}

func ptr(m budget.Micros) *budget.Micros { return &m }

func (f *fixture) register(s string) { f.mu.Lock(); f.secrets = append(f.secrets, s); f.mu.Unlock() }

func (f *fixture) registered() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.secrets...)
}

// customToken mints the run's token as the launcher does.
func (f *fixture) customToken(run string) string {
	f.t.Helper()
	signer, err := token.NewIAMSigner(signerEmail, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "ya29.launcher"}), token.WithIAMEndpoint(f.iam.URL))
	if err != nil {
		f.t.Fatal(err)
	}
	now := time.Now()
	tok, err := token.MintAt(context.Background(), signer, token.Claims{Slug: slug, Run: run, FX: token.ExpiryFX(now, 2*time.Hour), FP: "aurora", RB: rb}, now)
	if err != nil {
		f.t.Fatal(err)
	}
	return tok
}

// open exchanges the run's token and returns the session.
func (f *fixture) open() *budget.Session {
	f.t.Helper()
	s, err := budget.Open(context.Background(), f.cfg, f.customToken(f.cfg.Run))
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

// started opens the session, admits the run as an api-key run and starts it.
func (f *fixture) started(entry budget.AgentEntry) *budget.Session {
	f.t.Helper()
	s := f.open()
	h, err := s.Admit(context.Background(), budget.AdmitOptions{Gateway: true, LocalEnforce: f.cfg.LocalEnforce, PolicyCap: f.cfg.PolicyCap})
	if err != nil || h != nil {
		f.t.Fatalf("admit: %+v, %v", h, err)
	}
	if entry.Repo == "" {
		entry.Repo = "acme/app"
	}
	if err := s.Start(context.Background(), entry); err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { s.Finish(context.Background(), "succeeded") })
	return s
}

// num reads an integer at path (0 when absent).
func (f *fixture) num(path string) int64 {
	f.t.Helper()
	switch v := f.db.Value(path).(type) {
	case nil:
		return 0
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			f.t.Fatalf("%s = %v: %v", path, v, err)
		}
		return n
	default:
		f.t.Fatalf("%s = %#v, not a number", path, v)
	}
	return 0
}

func (f *fixture) day(d int64) string    { return budget.DayKey(d) }
func (f *fixture) run(leaf string) int64 { return f.num(budget.PathRun(slug, runID) + "/" + leaf) }
func (f *fixture) share(d int64, leaf string) int64 {
	return f.num(budget.PathSpendRun(d, slug, runID) + "/" + leaf)
}
func (f *fixture) repo(d int64, leaf string) int64 {
	return f.num(budget.PathSpendRepo(d, slug) + "/" + leaf)
}
func (f *fixture) global(d int64, leaf string) int64 {
	return f.num(budget.PathSpendGlobal(d) + "/" + leaf)
}

// waitFor polls cond until it holds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (f *fixture) nextHalt(d time.Duration) (budget.Halt, bool) {
	select {
	case h := <-f.halts:
		return h, true
	case <-time.After(d):
		return budget.Halt{}, false
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func tokenExpired() error { return token.ErrTokenExpired }
