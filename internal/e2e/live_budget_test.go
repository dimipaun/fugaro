//go:build live && docker

// The live budget check (docs/gcp-live-checklist.md, check 21): a real
// `fugaro exec` in a locally built base image, with the budget backend on,
// against the project's real Firebase Realtime Database, Identity Toolkit
// and Secure Token and the real Anthropic API. The harness plays the
// launcher: it mints each run's custom token with the person's own
// Application Default Credentials (signJwt as the token-signer account) and
// leaves it in a file:// bucket. It spends real money (about $0.50, bounded
// by the caps it sets), so it never runs in CI and refuses to run unless the
// person at the terminal opts in:
//
//	FUGARO_LIVE_BASE_IMAGE=<a base image built from this branch> \
//	FUGARO_LIVE_RTDB_URL=https://<fp>-default-rtdb.firebaseio.com \
//	FUGARO_LIVE_FIREBASE_API_KEY=<the restricted web API key> \
//	FUGARO_LIVE_TOKEN_SIGNER=<the token-signer service account email> \
//	FUGARO_LIVE_ANTHROPIC_API_KEY=<your own key> FUGARO_LIVE_SPEND_OK=1 \
//	  go test -tags 'live docker' -timeout 90m -run TestLiveBudget -v ./internal/e2e/
//
// (After `gcloud auth application-default login` with the firebase.database
// and userinfo.email scopes added, and fugaroTokenMinter on the signer.)
//
// Every run uses a scratch repository name, so no real repository's counters
// move. The test does change project-wide nodes for its duration: it sets
// config/mode to enforce and the global caps, and restores both (and removes
// its kill switches, registry entries and counters it can) when it ends. Use
// a project nobody else is launching into, or the scratch Firebase project.
//
// The Anthropic key reaches the container through the environment of the
// docker process only (never argv) and is never logged; the test fails if it
// or a minted custom token turns up in a run's logs, bucket or pushed files.
//
// Every answer is logged as a FACT: line, to paste into the checklist.
package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2/google"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const (
	lbGlobalDaily  = 5_000_000 // micro-dollars: $5.00, far above anything the test spends
	lbRepoDaily    = 5_000_000
	lbPerRun       = 2_000_000
	lbSmallDaily   = 250_000 // the $0.25 repository daily cap of the cap run
	lbTinyPerRun   = 2_000   // $0.002: below any call's worst case
	lbKillWait     = 3 * time.Minute
	lbKillDeadline = 60 * time.Second // how long after the switch the run may take to halt
)

var lbScopes = []string{"https://www.googleapis.com/auth/firebase.database", "https://www.googleapis.com/auth/userinfo.email"}

// lbEnv is what the harness needs to talk to the project.
type lbEnv struct {
	base, rtdbURL, apiKey, signer, anthropicKey, requestedBy string
	db                                                       *rtdb.Client
	signerI                                                  token.Signer
	project                                                  string // /fugaro/project, the fp claim
}

// lbRunSpec is one `fugaro exec`.
type lbRunSpec struct {
	repo      string // scratch repository, e.g. live21-ab12cd/app
	runID     string
	mint      time.Time // when the token is minted (zero: now)
	rtdbURL   string    // FUGARO_RTDB_URL in the container (empty: the project's)
	grace     string    // FUGARO_BUDGET_GRACE
	noToken   bool
	task      string
	withModel bool
}

// lbRun is a started exec.
type lbRun struct {
	spec      lbRunSpec
	slug      string
	custom    string
	dir       string
	cmd       *exec.Cmd
	out       *bytes.Buffer
	done      chan struct{}
	started   time.Time
	remoteDir string
}

type lbResult struct {
	rec        runstore.Record
	exit       int
	logs       []byte
	leases     int
	denials    int // "in a row were denied": a lease give-up
	secretHits []string
	runID      string
}

func TestLiveBudget(t *testing.T) {
	env := lbEnv{
		base:         os.Getenv("FUGARO_LIVE_BASE_IMAGE"),
		rtdbURL:      os.Getenv("FUGARO_LIVE_RTDB_URL"),
		apiKey:       os.Getenv("FUGARO_LIVE_FIREBASE_API_KEY"),
		signer:       os.Getenv("FUGARO_LIVE_TOKEN_SIGNER"),
		anthropicKey: os.Getenv("FUGARO_LIVE_ANTHROPIC_API_KEY"),
		requestedBy:  os.Getenv("FUGARO_LIVE_REQUESTED_BY"),
	}
	if env.requestedBy == "" {
		env.requestedBy = "live-budget-test"
	}
	if env.base == "" || env.rtdbURL == "" || env.apiKey == "" || env.signer == "" || env.anthropicKey == "" || os.Getenv("FUGARO_LIVE_SPEND_OK") != "1" {
		t.Skip("check 21 spends real money on the Anthropic API and writes the Firebase project's budget nodes: set FUGARO_LIVE_BASE_IMAGE, FUGARO_LIVE_RTDB_URL, FUGARO_LIVE_FIREBASE_API_KEY, FUGARO_LIVE_TOKEN_SIGNER, FUGARO_LIVE_ANTHROPIC_API_KEY (your own key) and FUGARO_LIVE_SPEND_OK=1 (docs/gcp-live-checklist.md)")
	}
	testutil.RequireDocker(t)
	testutil.IsolateGit(t)
	ctx := context.Background()

	lbConnect(t, ctx, &env)
	lbSeed(t, ctx, env)

	files := map[string]string{
		"fugaro.yaml":           lgwConfig,
		".fugaro/review.md":     lgwReview,
		"build.sh":              "#!/bin/sh\nexit 0\n",
		"test.sh":               "#!/bin/sh\nexit 0\n",
		"README.md":             "A tiny repository for the live budget check.\n",
		"logo.png":              lgwPNG(t),
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:9"}}` + "\n",
		".gitignore":            "build/\n",
	}
	remote := testutil.NewRemote(t, files)

	suffix := lbHex(3)
	repoOK, repoCap, repoTiny, repoKill, repoExp, repoDead := "live21-"+suffix+"/ok", "live21-"+suffix+"/cap", "live21-"+suffix+"/tiny", "live21-"+suffix+"/kill", "live21-"+suffix+"/exp", "live21-"+suffix+"/dead"
	for _, r := range []string{repoOK, repoCap, repoTiny, repoKill} {
		lbSetRepoCaps(t, ctx, env, r)
	}
	lbSetRepoCapsExact(t, ctx, env, repoCap, lbSmallDaily, lbPerRun)
	lbSetRepoCapsExact(t, ctx, env, repoTiny, lbRepoDaily, lbTinyPerRun)

	// Free first: the rules and the identity (A2, A4, A12, A13, A15).
	lbProbeIdentity(t, ctx, env, remote)

	// 1. A full run: leases and releases settle (R3, A4, A14, A-F1).
	r1 := lbFinish(t, env, lbStart(t, ctx, env, remote, lbRunSpec{repo: repoOK, task: lgwTask, withModel: true}))
	t.Logf("FACT: run 1 ended %s (outcome %s, exit %d), reason %q, %d leases granted, %d lease give-ups (stale denials)", r1.rec.Status, r1.rec.Outcome, r1.exit, r1.rec.Reason, r1.leases, r1.denials)
	if r1.rec.Status == runstore.StatusInfraError || r1.rec.Status == runstore.StatusHalted {
		t.Fatalf("run 1 ended %s: %s\n%s", r1.rec.Status, r1.rec.Reason, testutil.Tail(string(r1.logs)))
	}
	lbCheckSettled(t, ctx, env, repoOK, r1)

	// 2. The repository's $0.25 daily cap halts a run mid-way (R3, D2).
	r2 := lbFinish(t, env, lbStart(t, ctx, env, remote, lbRunSpec{repo: repoCap, task: lgwTask, withModel: true}))
	lbCheckHalt(t, r2, runstore.HaltRepoDailyCap, "the $0.25 repository daily cap")

	// 3. A per-run cap below any call's worst case halts at the first call.
	r3 := lbFinish(t, env, lbStart(t, ctx, env, remote, lbRunSpec{repo: repoTiny, task: lgwTask, withModel: true}))
	lbCheckHalt(t, r3, runstore.HaltRunCap, "the $0.002 per-run cap")

	// 4. A kill switch written mid-run halts it (R6, A-F2).
	lbKillMidRun(t, ctx, env, remote, repoKill)

	// 5. A token minted more than an hour ago has expired (R5, A12).
	r5 := lbFinish(t, env, lbStart(t, ctx, env, remote, lbRunSpec{repo: repoExp, task: lgwTask, mint: time.Now().Add(-2 * time.Hour)}))
	lbCheckHalt(t, r5, runstore.HaltBudgetTokenExpired, "a token minted 2 h ago")

	// 6. A dead database halts the run when the grace runs out (D14).
	r6 := lbFinish(t, env, lbStart(t, ctx, env, remote, lbRunSpec{repo: repoDead, task: lgwTask, rtdbURL: "http://127.0.0.1:1", grace: "5s"}))
	lbCheckHalt(t, r6, runstore.HaltBudgetUnavailable, "a dead database (D14)")

	for i, r := range []lbResult{r1, r2, r3, r5, r6} {
		if len(r.secretHits) > 0 {
			t.Errorf("run %d: a secret is in %s", i+1, strings.Join(r.secretHits, ", "))
		}
	}
	t.Logf("FACT: neither the Anthropic key nor any minted custom token is in any run's output, bucket or pushed files (when no error above says otherwise)")
	t.Logf("FACT: lease give-ups under no contention (this test runs one run at a time): %d of %d leases in run 1; contention is measured by the checklist's parallel-run step, if run", r1.denials, r1.leases+r1.denials)
}

// lbConnect builds the clients and reads /fugaro/project.
func lbConnect(t *testing.T, ctx context.Context, env *lbEnv) {
	t.Helper()
	ts, err := google.DefaultTokenSource(ctx, lbScopes...)
	if err != nil {
		t.Fatalf("no Application Default Credentials with the Firebase scopes (gcloud auth application-default login --scopes=...): %v", err)
	}
	env.db, err = rtdb.New(env.rtdbURL, rtdb.Auth{Source: ts})
	if err != nil {
		t.Fatal(err)
	}
	var project string
	found, err := env.db.Get(ctx, budget.PathProject, &project)
	if err != nil {
		t.Fatalf("A2: the database refused the person's ADC token: %v", err)
	}
	if !found || project == "" {
		t.Fatalf("/fugaro/project is not set: the database was not initialised by fugaro init --firebase")
	}
	env.project = project
	t.Logf("FACT: A2 the RTDB REST API accepted the person's Application Default Credentials (scopes %v); /fugaro/project = %q", lbScopes, project)

	cts, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		t.Fatalf("no Application Default Credentials for signJwt: %v", err)
	}
	env.signerI, err = token.NewIAMSigner(env.signer, cts)
	if err != nil {
		t.Fatal(err)
	}
}

// lbSeed sets the project-wide nodes the checks need and restores them when
// the test ends.
func lbSeed(t *testing.T, ctx context.Context, env lbEnv) {
	t.Helper()
	var oldMode, oldGlobal any
	_, err := env.db.Get(ctx, budget.PathMode, &oldMode)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.db.Get(ctx, budget.PathCapsGlobal, &oldGlobal); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c := context.Background()
		err := env.db.Patch(c, "config", map[string]any{"mode": oldMode, "caps/global": oldGlobal})
		if err != nil {
			t.Errorf("restoring config/mode and config/caps/global (was mode %v, global %v) failed: %v", oldMode, oldGlobal, err)
		}
	})
	g := budget.GlobalCaps{DailyMicros: lbMicros(lbGlobalDaily), PerRunMicros: lbMicros(lbPerRun)}
	if err := env.db.Patch(ctx, "config", map[string]any{"mode": budget.ModeEnforce, "caps/global": g}); err != nil {
		t.Fatalf("setting config/mode and the global caps: %v", err)
	}
	t.Logf("FACT: the test set config/mode=enforce and global caps (daily $%.2f, per run $%.2f) for its duration; it restores mode %v and caps %v", float64(lbGlobalDaily)/1e6, float64(lbPerRun)/1e6, oldMode, oldGlobal)
}

func lbMicros(v int64) *budget.Micros { m := budget.Micros(v); return &m }

func lbSetRepoCaps(t *testing.T, ctx context.Context, env lbEnv, repo string) {
	t.Helper()
	lbSetRepoCapsExact(t, ctx, env, repo, lbRepoDaily, lbPerRun)
}

func lbSlug(t *testing.T, repo string) string {
	t.Helper()
	s, err := task.Slug("fake", repo)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func lbSetRepoCapsExact(t *testing.T, ctx context.Context, env lbEnv, repo string, daily, perRun int64) {
	t.Helper()
	slug := lbSlug(t, repo)
	caps := budget.RepoCaps{DailyMicros: lbMicros(daily), PerRunMicros: lbMicros(perRun), Repo: repo}
	if err := env.db.Patch(ctx, "config/caps/repos", map[string]any{budget.Key(slug): caps}); err != nil {
		t.Fatalf("setting the caps of %s: %v", repo, err)
	}
	t.Cleanup(func() {
		_ = env.db.Patch(context.Background(), "config/caps/repos", map[string]any{budget.Key(slug): nil})
	})
}

func lbHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func lbRunID() string { return time.Now().UTC().Format("20060102-150405") + "-" + lbHex(2) }

// lbMint mints a run's custom token as the launcher would.
func lbMint(t *testing.T, ctx context.Context, env lbEnv, slug, run string, at time.Time) string {
	t.Helper()
	if at.IsZero() {
		at = time.Now()
	}
	claims := token.Claims{Slug: budget.Key(slug), Run: budget.Key(run), FX: token.ExpiryFX(at, 2*time.Hour), FP: env.project, RB: env.requestedBy}
	tok, err := token.MintAt(ctx, env.signerI, claims, at)
	if err != nil {
		t.Fatalf("A12/A15: minting a custom token (signJwt as %s with the person's ADC cloud-platform scope): %v", env.signer, err)
	}
	return tok
}

// lbProbeIdentity is the free part: exchange a probe identity, look at what
// the ID token carries and check the rules deny a write into another run's
// ledger (A4, A12, A13, A15).
func lbProbeIdentity(t *testing.T, ctx context.Context, env lbEnv, remote string) {
	t.Helper()
	slug, run := lbSlug(t, "live21-"+lbHex(3)+"/probe"), lbRunID()
	custom := lbMint(t, ctx, env, slug, run, time.Time{})
	t.Logf("FACT: A12/A15 signJwt as %s with the person's ADC (cloud-platform scope) minted a custom token; the account holds no roles", env.signer)
	sess, err := token.Exchange(ctx, token.Config{APIKey: env.apiKey, Register: func(string) {}}, custom)
	if err != nil {
		t.Fatalf("A12/A15/A-F4: exchanging the custom token (no sign-in provider needs enabling; the restricted web key must work): %v", err)
	}
	c := sess.Claims()
	if c.Slug != budget.Key(slug) || c.Run != budget.Key(run) || c.FP != env.project || c.RB != env.requestedBy || c.FX.IsZero() {
		t.Errorf("A12: the ID token's developer claims are %+v, want fs=%s fr=%s fp=%s rb=%s at the top level", c, budget.Key(slug), budget.Key(run), env.project, env.requestedBy)
	}
	t.Logf("FACT: A12 the ID token carries fs/fr/fx/fp/rb at the top level (uid %s, expires %s, fx %s)", sess.UID(), sess.Expiry().UTC().Format(time.RFC3339), c.FX.UTC().Format(time.RFC3339))
	if err := sess.Refresh(ctx); err != nil {
		t.Errorf("A12: refreshing the ID token with the refresh token failed: %v", err)
	} else {
		t.Logf("FACT: A12 the refresh token produced a new ID token (Secure Token accepted it)")
	}

	runDB, err := rtdb.New(env.rtdbURL, rtdb.Auth{IDToken: sess.Token})
	if err != nil {
		t.Fatal(err)
	}
	otherSlug, otherRun := lbSlug(t, "live21-"+lbHex(3)+"/other"), lbRunID()
	err = runDB.Patch(ctx, "runs", map[string]any{budget.Key(otherSlug) + "/" + otherRun + "/reserved": 1})
	if !errors.Is(err, rtdb.ErrPermission) {
		t.Errorf("A13: a write into another run's ledger returned %v, want permission denied", err)
	} else {
		t.Logf("FACT: A4/A13 the deployed rules denied a write by %s into another repository's run ledger (%v)", sess.UID(), err)
	}
	var cfg any
	if _, err := runDB.Get(ctx, budget.PathMode, &cfg); err != nil {
		t.Errorf("A13: a run identity could not read config/mode: %v", err)
	}
	if err := runDB.Patch(ctx, "config", map[string]any{"mode": "observe"}); !errors.Is(err, rtdb.ErrPermission) {
		t.Errorf("A13: a run identity writing config/mode returned %v, want permission denied", err)
	} else {
		t.Logf("FACT: A13 a run identity cannot write config/mode")
	}
}

// lbStart mints the run's token into a file:// bucket and starts docker.
func lbStart(t *testing.T, ctx context.Context, env lbEnv, remote string, spec lbRunSpec) *lbRun {
	t.Helper()
	slug := lbSlug(t, spec.repo)
	if spec.runID == "" {
		spec.runID = lbRunID()
	}
	run := t.TempDir()
	bucketDir := filepath.Join(run, "bucket")
	if err := os.MkdirAll(bucketDir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &lbRun{spec: spec, slug: slug, dir: run, out: &bytes.Buffer{}, done: make(chan struct{})}
	if !spec.noToken {
		r.custom = lbMint(t, ctx, env, slug, spec.runID, spec.mint)
		b, err := blobx.Open(ctx, "file://"+bucketDir+"?no_tmp_dir=true")
		if err != nil {
			t.Fatal(err)
		}
		if err := token.PutObject(ctx, b, slug, spec.runID, r.custom); err != nil {
			t.Fatal(err)
		}
		_ = b.Close()
	}
	js, err := json.Marshal(map[string]any{"version": 1, "run_id": spec.runID, "repo": spec.repo, "ref": "main", "task": spec.task})
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, run, map[string]string{
		"task.json": string(js),
		"gitconfig": "[safe]\n\tdirectory = *\n",
	})
	remoteDir := filepath.Dir(remote)
	r.remoteDir = remoteDir
	testutil.ShareWithContainer(t, env.base, run)
	testutil.ShareWithContainer(t, env.base, remoteDir)

	url := env.rtdbURL
	if spec.rtdbURL != "" {
		url = spec.rtdbURL
	}
	args := []string{"run", "--rm",
		"-v", remoteDir + ":" + remoteDir, "-v", run + ":/mnt/run",
		"-e", "FUGARO_BUDGET_MODE=enforce", "-e", "FUGARO_MAX_RUN_USD=2.00",
		"-e", "FUGARO_RTDB_URL=" + url, "-e", "FUGARO_FIREBASE_API_KEY=" + env.apiKey,
		"-e", "FUGARO_TEST_ALLOW_ROUTING_SETTINGS=1",
		"-e", "GIT_CONFIG_GLOBAL=/mnt/run/gitconfig"}
	if spec.grace != "" {
		args = append(args, "-e", "FUGARO_BUDGET_GRACE="+spec.grace)
	}
	if spec.withModel {
		// No value: the key comes from the docker process's environment.
		args = append(args, "-e", "ANTHROPIC_API_KEY")
	}
	args = append(args, env.base, "fugaro", "exec",
		"--bucket", "file:///mnt/run/bucket?no_tmp_dir=true", "--task-file", "/mnt/run/task.json",
		"--remote", remote, "--provider", "fake", "--provider-state", "/mnt/run/provider.json",
		"--cancel-poll", "5s")
	cctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	t.Cleanup(cancel)
	r.cmd = exec.CommandContext(cctx, "docker", args...)
	r.cmd.Env = os.Environ()
	if spec.withModel {
		r.cmd.Env = append(r.cmd.Env, "ANTHROPIC_API_KEY="+env.anthropicKey)
	}
	r.cmd.Stdout, r.cmd.Stderr = r.out, r.out
	r.started = time.Now()
	if err := r.cmd.Start(); err != nil {
		t.Fatalf("docker run did not start: %v", err)
	}
	go func() { _ = r.cmd.Wait(); close(r.done) }()
	return r
}

// lbFinish waits for the run, reads its record and scans for secrets.
func lbFinish(t *testing.T, env lbEnv, r *lbRun) lbResult {
	t.Helper()
	<-r.done
	logs := bytes.ReplaceAll(r.out.Bytes(), []byte(env.anthropicKey), []byte("[the API key]"))
	res := lbResult{logs: logs, runID: r.spec.runID}
	if r.cmd.ProcessState != nil {
		res.exit = r.cmd.ProcessState.ExitCode()
	}
	res.leases = bytes.Count(logs, []byte("budget: lease granted"))
	res.denials = bytes.Count(logs, []byte("in a row were denied"))
	data, err := os.ReadFile(filepath.Join(r.dir, "bucket", "runs", r.slug, r.spec.runID, "result.json"))
	if err != nil {
		t.Fatalf("run %s left no result.json (exit %d): %v\n%s", r.spec.runID, res.exit, err, testutil.Tail(string(logs)))
	}
	if err := json.Unmarshal(data, &res.rec); err != nil {
		t.Fatal(err)
	}
	// The raw output, before blotting, is what is scanned.
	raw := lgwOutcome{logs: r.out.Bytes()}
	more := []string{r.remoteDir}
	if env.anthropicKey != "" && r.spec.withModel {
		res.secretHits = append(res.secretHits, lgwScanForKey(t, env.anthropicKey, raw, r.dir, more...)...)
	}
	if r.custom != "" {
		res.secretHits = append(res.secretHits, lgwScanForKey(t, r.custom, raw, r.dir, more...)...)
	}
	return res
}

// lbCheckSettled reads the run's ledger and the day counters after a clean
// run: everything it reserved is either spent or released, and the counters
// agree with the gateway's cost (R3, A14).
func lbCheckSettled(t *testing.T, ctx context.Context, env lbEnv, repo string, r lbResult) {
	t.Helper()
	slug := lbSlug(t, repo)
	run := r.runID
	var led budget.RunLedger
	found, err := env.db.Get(ctx, budget.PathRun(slug, run), &led)
	if err != nil || !found {
		t.Errorf("R3: the run's lifetime ledger is missing (found %v, err %v)", found, err)
		return
	}
	if led.Unsettled() != 0 {
		t.Errorf("R3: the finished run still holds %d micro-dollars (reserved %d, released %d, spent %d)", led.Unsettled(), led.Reserved, led.Released, led.Spent)
	}
	t.Logf("FACT: R3 run ledger after the run: reserved %d, released %d, spent %d, notional %d (micro-dollars), tokens %d", led.Reserved, led.Released, led.Spent, led.Notional, led.Tokens)
	if r.rec.Cost != nil {
		gw := int64(r.rec.Cost.ModelUSD * 1e6)
		if d := int64(led.Spent) - gw; d > 1000 || d < -1000 {
			t.Errorf("R3: the ledger's spent %d differs from the gateway's cost %d micro-dollars", led.Spent, gw)
		}
	}
	for _, day := range []int64{budget.Day(time.Now()), budget.Day(time.Now().Add(-time.Hour))} {
		var c budget.Counters
		ok, err := env.db.Get(ctx, budget.PathSpendRepo(day, slug), &c)
		if err != nil {
			t.Errorf("A14: reading the repository's day counter: %v", err)
			continue
		}
		if ok {
			t.Logf("FACT: A14 the lease writes landed under spend/%d (the plain decimal day the rules computed): repo counter counted %d, spent %d, calls %d, models %v", day, c.Counted, c.Spent, c.Calls, c.Models())
			if c.Counted != c.Spent {
				t.Errorf("R3: the repository's counter still counts %d against %d spent: a release was lost", c.Counted, c.Spent)
			}
			break
		}
	}
	var entry any
	if ok, err := env.db.Get(ctx, budget.PathAgent(slug, run), &entry); err != nil || ok {
		t.Errorf("R7: the run's registry entry should be gone (found %v, err %v)", ok, err)
	} else {
		t.Logf("FACT: the run's registry entry was removed at its end")
	}
}

// lbCheckHalt expects a halted record with the given reason.
func lbCheckHalt(t *testing.T, r lbResult, want runstore.HaltReason, what string) {
	t.Helper()
	if r.rec.Status != runstore.StatusHalted || r.rec.Halt == nil || r.rec.Halt.Reason != want {
		t.Errorf("%s: the run ended %s (halt %+v, reason %q), want halted with %s\n%s", what, r.rec.Status, r.rec.Halt, r.rec.Reason, want, testutil.Tail(string(r.logs)))
		return
	}
	if r.exit != 0 {
		t.Errorf("%s: a halted run exited %d, want 0", what, r.exit)
	}
	t.Logf("FACT: %s halted the run (%s, scope %s), outcome %s, exit %d", what, r.rec.Halt.Reason, r.rec.Halt.Scope, r.rec.Outcome, r.exit)
}

// lbKillMidRun starts a run, waits for its registry entry, writes the
// repository's kill switch and checks the run halts within the deadline.
func lbKillMidRun(t *testing.T, ctx context.Context, env lbEnv, remote, repo string) {
	t.Helper()
	r := lbStart(t, ctx, env, remote, lbRunSpec{repo: repo, task: lgwTask, withModel: true})
	slug := lbSlug(t, repo)
	deadline := time.Now().Add(lbKillWait)
	for {
		var e budget.AgentEntry
		ok, err := env.db.Get(ctx, budget.PathAgent(slug, r.spec.runID), &e)
		if err == nil && ok {
			break
		}
		select {
		case <-r.done:
			t.Fatalf("the run ended before it registered:\n%s", testutil.Tail(r.out.String()))
		case <-time.After(2 * time.Second):
		}
		if time.Now().After(deadline) {
			t.Fatalf("the run did not register within %s", lbKillWait)
		}
	}
	kill := budget.Kill{On: true, By: env.requestedBy, At: time.Now().UnixMilli(), Reason: "check 21"}
	if err := env.db.Patch(ctx, "config/kill/repos", map[string]any{budget.Key(slug): kill}); err != nil {
		t.Fatalf("writing the kill switch: %v", err)
	}
	t.Cleanup(func() {
		_ = env.db.Patch(context.Background(), "config/kill/repos", map[string]any{budget.Key(slug): nil})
	})
	wrote := time.Now()
	res := lbFinish(t, env, r)
	lbCheckHalt(t, res, runstore.HaltKillSwitch, "the repository kill switch")
	if res.rec.Halt != nil {
		took := res.rec.Halt.At.Sub(wrote)
		if took > lbKillDeadline {
			t.Errorf("R6: the run halted %s after the switch, want within %s (the 15 s poll covers a dead stream)", took, lbKillDeadline)
		}
		t.Logf("FACT: A-F2/R6 the kill switch halted the run %s after it was written (stream or poll, both are within the deadline %s); the run ran for %s before that", took.Round(time.Millisecond), lbKillDeadline, wrote.Sub(r.started).Round(time.Second))
	}
	t.Logf("FACT: A-F2 the runner's SSE stream and token refresh ran against the real endpoints from a docker container with default egress for this run's length; a 1 h survival needs the Cloud Run step of the checklist")
}
