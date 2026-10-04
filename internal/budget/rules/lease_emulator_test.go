//go:build firebase

package rules

// The runner's lease client against the generated rules: the writes that
// internal/budget makes (leases, releases, usage reports, the registry,
// notional spend, the outcome) are exactly what the rules must accept, and
// the client's reads must see exactly what the rules let a run read. The
// run's identity is the fake Firebase Auth's ID token; the emulator reads its
// claims as it reads an unsigned token's.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/pricing"
)

const leaseSigner = "fugaro-token-signer@aurora-fp.iam.gserviceaccount.com"

type leaseRig struct {
	h   *harness
	iam *gcpfake.IAMCredentials
	itk *gcpfake.IdentityToolkit
}

func newLeaseRig(t *testing.T) *leaseRig {
	h := newHarness(t)
	r := &leaseRig{h: h}
	r.iam = gcpfake.NewIAMCredentials(t)
	r.iam.AddSigner(leaseSigner)
	r.itk = gcpfake.NewIdentityToolkit(t, r.iam, "AIzaSyFakeWebApiKeyForTests000000000", projectID)
	h.set("config/mode", "enforce")
	h.set("config/limits", map[string]any{"maxReserveMicros": 5_000_000})
	h.set("config/caps/global", map[string]any{"dailyMicros": 150_000_000, "perRunMicros": 20_000_000})
	return r
}

func (r *leaseRig) caps(slug string, daily, perRun int64) {
	r.h.set("config/caps/repos/"+slug, map[string]any{"dailyMicros": daily, "perRunMicros": perRun})
}

// session opens a run's session, the way the runner does.
func (r *leaseRig) session(t *testing.T, slug, run string, mutate ...func(*budget.Config)) *budget.Session {
	t.Helper()
	signer, err := token.NewIAMSigner(leaseSigner, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "ya29.l"}), token.WithIAMEndpoint(r.iam.URL))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tok, err := token.MintAt(context.Background(), signer, token.Claims{Slug: slug, Run: run, FX: token.ExpiryFX(now, time.Hour), FP: projectID, RB: "alice@example.invalid"}, now)
	if err != nil {
		t.Fatal(err)
	}
	cfg := budget.Config{
		RTDBURL: emuURL, APIKey: "AIzaSyFakeWebApiKeyForTests000000000", IdentityURL: r.itk.URL, SecureTokenURL: r.itk.URL, HTTP: r.h.hc,
		Slug: slug, Run: run, Deadline: now.Add(time.Hour), Grace: 5 * time.Second,
		HeartbeatEvery: 50 * time.Millisecond, StaleBackoff: 3 * time.Millisecond, CapRetryWait: 50 * time.Millisecond,
		StreamBackoffMin: 10 * time.Millisecond, StreamBackoffMax: 50 * time.Millisecond,
		Register: func(string) {}, OnHalt: func(h budget.Halt) { t.Errorf("unexpected halt: %+v", h) },
	}
	for _, m := range mutate {
		m(&cfg)
	}
	s, err := budget.Open(context.Background(), cfg, tok)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (r *leaseRig) num(t *testing.T, path string) int64 {
	t.Helper()
	var n int64
	if v := r.h.value(path); v != "null" {
		if _, err := fmt.Sscan(v, &n); err != nil {
			t.Fatalf("%s = %s", path, v)
		}
	}
	return n
}

// TestLeaseClientAgainstTheRules runs one api-key run's whole life.
func TestLeaseClientAgainstTheRules(t *testing.T) {
	r := newLeaseRig(t)
	r.caps("aurora-app", 60_000_000, 20_000_000)
	ctx := context.Background()
	d := budget.Day(time.Now())
	s := r.session(t, "aurora-app", "20261002-120000-ab12")
	if h, err := s.Admit(ctx, budget.AdmitOptions{Gateway: true}); h != nil || err != nil {
		t.Fatalf("admit: %+v, %v", h, err)
	}
	if err := s.Start(ctx, budget.AgentEntry{Repo: "acme/app", Workflow: "app", Title: "Add a feature", Stage: "implement", Auth: "api-key", Coder: "claude-sonnet-5-5"}); err != nil {
		t.Fatalf("the rules refused the registry entry or exp: %v", err)
	}
	got, err := s.Lease().Grant(ctx, 100_000)
	if err != nil {
		t.Fatalf("the rules refused a lease: %v", err)
	}
	if got != 1_000_000 {
		t.Fatalf("lease = %d", got)
	}
	s.SetUsage(func() budget.Micros { return 400_000 })
	rep := gateway.StageReport{Calls: 3, Used: 400_000, Tokens: 12_345, Overrun: 5_000, ByModel: map[string]pricing.Micros{"claude-sonnet-5-5": 300_000, "claude-haiku-4-5": 100_000}}
	if err := s.Lease().Report(ctx, rep); err != nil {
		t.Fatalf("the rules refused a usage report: %v", err)
	}
	if err := s.Lease().Release(ctx, 600_000); err != nil {
		t.Fatalf("the rules refused a release: %v", err)
	}
	// The heartbeat's registry write, in the same request as a usage report.
	s.Update(func(e *budget.AgentEntry) { e.Stage, e.Round = "review", 1 })
	waitFor(t, "a heartbeat to land", func() bool { return r.h.value("agents/aurora-app/20261002-120000-ab12/stage") == `"review"` })
	s.Finish(ctx, "succeeded")

	base := "runs/aurora-app/20261002-120000-ab12/"
	for leaf, want := range map[string]int64{"reserved": 1_000_000, "released": 600_000, "spent": 400_000, "overrun": 5_000, "tokens": 12_345} {
		if got := r.num(t, base+leaf); got != want {
			t.Errorf("run %s = %d, want %d", leaf, got, want)
		}
	}
	day := fmt.Sprintf("spend/%d/", d)
	for _, p := range []string{day + "global/", day + "repos/aurora-app/"} {
		if r.num(t, p+"counted") != 400_000 || r.num(t, p+"spent") != 400_000 || r.num(t, p+"calls") != 3 {
			t.Errorf("%s: counted %d, spent %d, calls %d", p, r.num(t, p+"counted"), r.num(t, p+"spent"), r.num(t, p+"calls"))
		}
	}
	if got := r.num(t, day+"repos/aurora-app/byModel/claude-sonnet-5-5/micros"); got != 300_000 {
		t.Errorf("byModel sonnet = %d", got)
	}
	if r.h.value(fmt.Sprintf("outcomes/%d/aurora-app/20261002-120000-ab12/status", d)) != `"succeeded"` {
		t.Errorf("outcome = %s", r.h.value(fmt.Sprintf("outcomes/%d", d)))
	}
	if r.h.value("agents/aurora-app/20261002-120000-ab12") != "null" {
		t.Errorf("the registry entry survived: %s", r.h.value("agents"))
	}
}

// TestOAuthNotionalAgainstTheRules: the increase-only notional writes.
func TestOAuthNotionalAgainstTheRules(t *testing.T) {
	r := newLeaseRig(t)
	r.caps("aurora-app", 60_000_000, 20_000_000)
	ctx := context.Background()
	s := r.session(t, "aurora-app", "20261002-120001-cd34")
	if h, err := s.Admit(ctx, budget.AdmitOptions{}); h != nil || err != nil {
		t.Fatalf("admit: %+v, %v", h, err)
	}
	if err := s.Start(ctx, budget.AgentEntry{Repo: "acme/app", Auth: "oauth"}); err != nil {
		t.Fatal(err)
	}
	err := s.AddNotional(ctx, 1_250_000, map[string]budget.ModelUse{"claude-sonnet-5-5": {NotionalMicros: 1_000_000, In: 1000, Out: 500, CR: 20, CW: 10}})
	if err != nil {
		t.Fatalf("the rules refused notional spend: %v", err)
	}
	if err := s.AddNotional(ctx, 250_000, nil); err != nil {
		t.Fatal(err)
	}
	s.Finish(ctx, "succeeded")
	d := budget.Day(time.Now())
	if got := r.num(t, "runs/aurora-app/20261002-120001-cd34/notional"); got != 1_500_000 {
		t.Errorf("notional = %d", got)
	}
	if got := r.num(t, fmt.Sprintf("spend/%d/global/notional", d)); got != 1_500_000 {
		t.Errorf("global notional = %d", got)
	}
	// A model's notional dollars land on their own leaf, never mixed into
	// the gateway's billed "micros" (bug: the two used to share a path).
	if got := r.num(t, fmt.Sprintf("spend/%d/repos/aurora-app/byModel/claude-sonnet-5-5/notionalMicros", d)); got != 1_000_000 {
		t.Errorf("byModel notionalMicros = %d", got)
	}
	if got := r.num(t, fmt.Sprintf("spend/%d/repos/aurora-app/byModel/claude-sonnet-5-5/micros", d)); got != 0 {
		t.Errorf("byModel micros must stay 0 for an oauth run: %d", got)
	}
}

// TestConcurrentSessionsNeverExceedCaps: many sessions lease and release at
// once against the rules; the counters end exactly at what the runs still
// hold, and never pass a cap on the way.
func TestConcurrentSessionsNeverExceedCaps(t *testing.T) {
	r := newLeaseRig(t)
	r.h.set("config/caps/global", map[string]any{"dailyMicros": 12_000_000, "perRunMicros": 5_000_000})
	for _, slug := range []string{"repo-a", "repo-b"} {
		r.caps(slug, 7_000_000, 5_000_000)
	}
	const runs = 8
	var staleGiveUps atomic.Int64
	ctx := context.Background()
	var wg sync.WaitGroup
	var mu sync.Mutex
	held := map[string]int64{}
	for i := 0; i < runs; i++ {
		slug := []string{"repo-a", "repo-b"}[i%2]
		run := fmt.Sprintf("20261002-1300%02d-ab%02d", i, i)
		s := r.session(t, slug, run, func(c *budget.Config) { c.OnHalt = func(budget.Halt) {} })
		if h, err := s.Admit(ctx, budget.AdmitOptions{Gateway: true}); h != nil || err != nil {
			t.Fatalf("admit: %+v %v", h, err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			var mine int64
			defer func() {
				mu.Lock()
				held[slug+"/"+run] = mine
				mu.Unlock()
			}()
			for j := 0; j < 12; j++ {
				// Eight stale denials in a row under eight clients hammering
				// one counter is the client giving up on that lease (the
				// agent's next call asks again); a refusal is the caps.
				var got budget.Micros
				var err error
				for try := 0; try < 10; try++ {
					if got, err = s.Lease().Grant(ctx, 300_000); err == nil || errors.As(err, new(*gateway.Refusal)) {
						break
					}
					staleGiveUps.Add(1)
				}
				var ref *gateway.Refusal
				switch {
				case errors.As(err, &ref):
					continue
				case err != nil:
					t.Errorf("grant: %v", err)
					return
				}
				mine += int64(got)
				// Keep and spend half, give half back.
				if j%3 == 2 {
					var rerr error
					for try := 0; try < 10; try++ { // a release also loses races
						if rerr = s.Lease().Release(ctx, got/2); rerr == nil {
							break
						}
					}
					if rerr != nil {
						t.Errorf("release: %v", rerr)
						return
					}
					mine -= int64(got / 2)
				}
			}
		}()
	}
	wg.Wait()
	t.Logf("%d lease attempts gave up after 8 stale denials (check 21 records the live rate)", staleGiveUps.Load())
	d := budget.Day(time.Now())
	var total, repoA, repoB int64
	for k, v := range held {
		total += v
		if k[:6] == "repo-a" {
			repoA += v
		} else {
			repoB += v
		}
	}
	if g := r.num(t, fmt.Sprintf("spend/%d/global/counted", d)); g != total || g > 12_000_000 {
		t.Errorf("global counted %d, runs hold %d (cap 12,000,000)", g, total)
	}
	if a, b := r.num(t, fmt.Sprintf("spend/%d/repos/repo-a/counted", d)), r.num(t, fmt.Sprintf("spend/%d/repos/repo-b/counted", d)); a != repoA || b != repoB || a > 7_000_000 || b > 7_000_000 {
		t.Errorf("repo counters %d/%d, runs hold %d/%d (cap 7,000,000 each)", a, b, repoA, repoB)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestKillReachesTheRunThroughTheRules: a run may listen to exactly its two
// switches, and a switch turned on over IAM reaches it through the stream
// (the heartbeat poll is slowed down so only the stream can).
func TestKillReachesTheRunThroughTheRules(t *testing.T) {
	r := newLeaseRig(t)
	r.caps("aurora-app", 60_000_000, 20_000_000)
	ctx := context.Background()
	halts := make(chan budget.Halt, 4)
	s := r.session(t, "aurora-app", "20261002-120002-ef56", func(c *budget.Config) {
		c.HeartbeatEvery = time.Hour
		c.OnHalt = func(h budget.Halt) { halts <- h }
	})
	if h, err := s.Admit(ctx, budget.AdmitOptions{Gateway: true}); h != nil || err != nil {
		t.Fatalf("admit: %+v, %v", h, err)
	}
	if err := s.Start(ctx, budget.AgentEntry{Repo: "acme/app"}); err != nil {
		t.Fatal(err)
	}
	defer s.Finish(ctx, "halted")
	time.Sleep(500 * time.Millisecond) // the streams are listening
	r.h.set("config/kill/repos/aurora-app", map[string]any{"on": true, "by": "admin@example.invalid", "reason": "emulator", "at": time.Now().UnixMilli()})
	select {
	case h := <-halts:
		if h.Reason != budget.ReasonKillSwitch || h.Scope != budget.ScopeRepo {
			t.Fatalf("halt = %+v", h)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the switch never reached the run: the rules may not let it listen")
	}
	if r.h.value("agents/aurora-app/20261002-120002-ef56") == "null" {
		t.Fatal("the registry entry vanished before Finish")
	}
}
