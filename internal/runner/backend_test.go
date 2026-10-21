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
	bkSignerEmail = "fugaro-token-signer@aurora-fp.iam.gserviceaccount.com"
	bkAPIKey      = "AIzaSyFakeWebApiKeyForTests000000000"
	bkRB          = "dev@example.invalid"
)

// bk is a run against the budget backend's fakes: a database seeded as
// fugaro update --firebase leaves it, Firebase Auth, and the run's token waiting
// in the bucket where the launcher put it.
type bk struct {
	*gw
	tb     *testing.T
	db     *gcpfake.RTDB
	iam    *gcpfake.IAMCredentials
	itk    *gcpfake.IdentityToolkit
	bucket *blobx.Bucket
	// auths counts the git credential requests the fixture's provider has
	// received (countAuths). Nil until a test — admissionRun always does —
	// installs the counter.
	auths *int

	// key is the launch's seal key: the token object is sealed under it and
	// the job's marker carries it (Backend.TokenMarked, Backend.TokenKey), as
	// a launcher that seals leaves both (D15). A test of an unmarked job
	// clears the two fields itself.
	key []byte

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
	b.db.Set(budget.PathCapsRepo(repoSlug), budget.RepoCaps{DailyMicros: micros(60_000_000), PerRunMicros: micros(20_000_000), Repo: "acme/app"})
	b.bucket = withBucket(g.harness)
	b.key = sealKeyFor(0x5a)
	g.deps.Backend = runner.Backend{
		RTDBURL: b.db.URL, APIKey: bkAPIKey,
		TokenMarked: true, TokenKey: b.key,
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

// mint leaves the run's token in the bucket, sealed under the fixture's key,
// as fugaro run does at launch.
func (b *bk) mint(at time.Time) {
	b.tb.Helper()
	b.mintSealed(repoSlug, runID, at)
}

// mintSealed mints slug/run's token at at and leaves it sealed under b.key.
func (b *bk) mintSealed(slug, run string, at time.Time) {
	b.tb.Helper()
	tok := b.mintToken(slug, run, at)
	if err := token.PutObject(context.Background(), b.bucket, slug, run, tok, b.key); err != nil {
		b.tb.Fatal(err)
	}
}

// mintToken is slug/run's custom token, minted at at and left nowhere.
func (b *bk) mintToken(slug, run string, at time.Time) string {
	b.tb.Helper()
	signer, err := token.NewIAMSigner(bkSignerEmail, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "ya29.launcher"}), token.WithIAMEndpoint(b.iam.URL))
	if err != nil {
		b.tb.Fatal(err)
	}
	tok, err := token.MintAt(context.Background(), signer, token.Claims{Slug: slug, Run: run, FX: token.ExpiryFX(at, 3*time.Hour), FP: "aurora", RB: bkRB}, at)
	if err != nil {
		b.tb.Fatal(err)
	}
	return tok
}

// mintPlain is slug/run's custom token minted now, for a test that plants a
// plaintext object Fugaro itself never writes.
func mintPlain(t *testing.T, b *bk, slug, run string) string {
	t.Helper()
	return b.mintToken(slug, run, time.Now())
}

// unmarked makes the fixture's job one no sealing launcher started: no
// marker, no key (every local run, and every launch that minted nothing).
func (b *bk) unmarked() { b.deps.Backend.TokenMarked, b.deps.Backend.TokenKey = false, nil }

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

func (b *bk) runLeaf(leaf string) int64 { return b.num(budget.PathRun(repoSlug, runID) + "/" + leaf) }
func (b *bk) repoLeaf(leaf string) int64 {
	return b.num(budget.PathSpendRepo(b.day(), repoSlug) + "/" + leaf)
}
func (b *bk) globalLeaf(leaf string) int64 {
	return b.num(budget.PathSpendGlobal(b.day()) + "/" + leaf)
}

// tokenObjectKept reports that the run's token object is still in the
// bucket: the take is read-only (the job's grant on tokens/ has no delete,
// D16), so a run never removes it; the lifecycle rule and the launcher do.
func (b *bk) tokenObjectKept() bool {
	ok, err := b.bucket.Exists(context.Background(), token.ObjectKey(repoSlug, runID))
	return err == nil && ok
}

func (b *bk) tokenObjectGone() bool {
	ok, err := b.bucket.Exists(context.Background(), token.ObjectKey(repoSlug, runID))
	return err == nil && !ok
}

func (b *bk) outcome() map[string]any {
	m, _ := b.db.Value(budget.PathOutcome(b.day(), repoSlug, runID)).(map[string]any)
	return m
}

func (b *bk) entry() map[string]any {
	m, _ := b.db.Value(budget.PathAgent(repoSlug, runID)).(map[string]any)
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
