package runner_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"gocloud.dev/blob"
	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const (
	bkSlug        = "acme-app"
	bkSignerEmail = "fugaro-token-signer@aurora-fp.iam.gserviceaccount.com"
	bkAPIKey      = "AIzaSyFakeWebApiKeyForTests000000000"
	bkRB          = "dev@example.invalid"
)

// bk is a run against the budget backend's fakes: a database seeded as
// fugaro init --firebase leaves it, Firebase Auth, and the run's token waiting
// in the bucket where the launcher put it.
type bk struct {
	*gw
	tb     *testing.T
	db     *gcpfake.RTDB
	iam    *gcpfake.IAMCredentials
	itk    *gcpfake.IdentityToolkit
	bucket *blobx.Bucket

	mu      sync.Mutex
	secrets []string
}

// newBK is newGW with the backend: RTDB caps are $20 per run, $60 per
// repository day and $150 per project day, the largest lease $5, and the
// project's mode is mode.
func newBK(t *testing.T, cfg, mode, capUSD string, script ...anthropicfake.Reply) *bk {
	t.Helper()
	g := newGW(t, cfg, mode, capUSD, script...)
	return attachBackend(t, g, mode)
}

func attachBackend(t *testing.T, g *gw, mode string) *bk {
	t.Helper()
	b := &bk{gw: g, tb: t}
	b.db = gcpfake.NewRTDB(t)
	b.iam = gcpfake.NewIAMCredentials(t)
	b.iam.AddSigner(bkSignerEmail)
	b.itk = gcpfake.NewIdentityToolkit(t, b.iam, bkAPIKey, "aurora-fp")
	b.db.Set("fugaro/project", "aurora")
	b.db.Set(budget.PathMode, mode)
	micros := func(v budget.Micros) *budget.Micros { return &v }
	b.db.Set(budget.PathLimits, budget.Limits{MaxReserveMicros: micros(5_000_000)})
	b.db.Set(budget.PathCapsGlobal, budget.GlobalCaps{DailyMicros: micros(150_000_000), PerRunMicros: micros(20_000_000)})
	b.db.Set(budget.PathCapsRepo(bkSlug), budget.RepoCaps{DailyMicros: micros(60_000_000), PerRunMicros: micros(20_000_000), Repo: "acme/app"})
	b.bucket = withBucket(g.harness)
	g.deps.Backend = runner.Backend{
		RTDBURL: b.db.URL, APIKey: bkAPIKey,
		Tune: func(c *budget.Config) {
			c.IdentityURL, c.SecureTokenURL = b.itk.URL, b.itk.URL
			c.HeartbeatEvery, c.RetryEvery, c.CapRetryWait = 30*time.Millisecond, 20*time.Millisecond, 30*time.Millisecond
			c.StaleBackoff, c.KillRestart = 2*time.Millisecond, 100*time.Millisecond
			c.StreamBackoffMin, c.StreamBackoffMax = 10*time.Millisecond, 50*time.Millisecond
			if c.Grace == 0 || c.Grace == budget.DefaultGrace {
				c.Grace = 600 * time.Millisecond
			}
			old := c.Register
			c.Register = func(s string) { b.mu.Lock(); b.secrets = append(b.secrets, s); b.mu.Unlock(); old(s) }
		},
	}
	b.mint(time.Now())
	return b
}

// mint leaves the run's token in the bucket, as fugaro run does at launch.
func (b *bk) mint(at time.Time) {
	b.tb.Helper()
	signer, err := token.NewIAMSigner(bkSignerEmail, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "ya29.launcher"}), token.WithIAMEndpoint(b.iam.URL))
	if err != nil {
		b.tb.Fatal(err)
	}
	tok, err := token.MintAt(context.Background(), signer, token.Claims{Slug: bkSlug, Run: runID, FX: token.ExpiryFX(at, 3*time.Hour), FP: "aurora", RB: bkRB}, at)
	if err != nil {
		b.tb.Fatal(err)
	}
	if err := token.PutObject(context.Background(), b.bucket, bkSlug, runID, tok); err != nil {
		b.tb.Fatal(err)
	}
}

func (b *bk) registered() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.secrets...)
}

func (b *bk) num(path string) int64 {
	b.tb.Helper()
	switch v := b.db.Value(path).(type) {
	case nil:
		return 0
	case json.Number:
		n, _ := v.Int64()
		return n
	}
	b.tb.Fatalf("%s = %#v", path, b.db.Value(path))
	return 0
}

func (b *bk) day() int64 { return budget.Day(time.Now()) }

func (b *bk) runLeaf(leaf string) int64 { return b.num(budget.PathRun(bkSlug, runID) + "/" + leaf) }
func (b *bk) repoLeaf(leaf string) int64 {
	return b.num(budget.PathSpendRepo(b.day(), bkSlug) + "/" + leaf)
}
func (b *bk) globalLeaf(leaf string) int64 {
	return b.num(budget.PathSpendGlobal(b.day()) + "/" + leaf)
}

func (b *bk) tokenObjectGone() bool {
	ok, err := b.bucket.Exists(context.Background(), token.ObjectKey(bkSlug, runID))
	return err == nil && !ok
}

func (b *bk) outcome() map[string]any {
	m, _ := b.db.Value(budget.PathOutcome(b.day(), bkSlug, runID)).(map[string]any)
	return m
}

func (b *bk) entry() map[string]any {
	m, _ := b.db.Value(budget.PathAgent(bkSlug, runID)).(map[string]any)
	return m
}

// waitUntil polls cond.
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// allObjects reads every object of the runs bucket.
func allObjects(t *testing.T, b *blob.Bucket) map[string]string {
	t.Helper()
	out := map[string]string{}
	it := b.List(nil)
	for {
		o, err := it.Next(context.Background())
		if err != nil {
			break
		}
		data, err := b.ReadAll(context.Background(), o.Key)
		if err != nil {
			t.Fatal(err)
		}
		out[o.Key] = string(data)
	}
	return out
}

func killSwitch(on bool) budget.Kill {
	return budget.Kill{On: on, By: "admin@example.invalid", Reason: "runaway spend", At: time.Now().UnixMilli()}
}

// stepWithKill commits work, then turns the switch on and blocks until the
// run stops the stage.
func killMidStage(db *gcpfake.RTDB, path string) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo partial > partial.txt && git add -A && git commit -qm partial")
		db.Set(path, killSwitch(true))
		<-ctx.Done()
		return agent.Result{CostUSD: 0.1}, ctx.Err()
	}
}

var _ = strings.Contains
var _ = testutil.Git
