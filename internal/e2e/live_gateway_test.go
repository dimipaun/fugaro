//go:build live && docker

// The live gateway check (docs/gcp-live-checklist.md, check 20): a real
// `fugaro exec` in a locally built base image, with the budget on, drives
// the real Claude Code through the gateway to the real Anthropic API. It
// spends real money (about $0.30, bounded by the $2.00 run cap and a
// second run capped at $0.002), so it never runs in CI and refuses to run
// unless the person at the terminal opts in:
//
//	FUGARO_LIVE_BASE_IMAGE=<a base image built from this branch> \
//	FUGARO_LIVE_ANTHROPIC_API_KEY=<your own key> FUGARO_LIVE_SPEND_OK=1 \
//	  go test -tags 'live docker' -timeout 60m -run TestLiveGateway -v ./internal/e2e/
//
// There is no GCP in it: the bucket is a file:// directory, the git
// provider is the file-backed fake and the repository is a local bare
// remote. The key reaches the container through the environment of the
// docker process only (never argv), is never logged, and the test fails if
// it turns up in the run's logs, bucket or provider state.
//
// Every answer is logged as a FACT: line, to paste into the checklist.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const (
	lgwCoder      = "claude-sonnet-5"
	lgwReviewer   = "claude-sonnet-5-5"
	lgwBackground = "claude-haiku-4-5"
	lgwMaxOutput  = 4000

	// lgwHaltGrace is the runner's grace for an agent to exit by itself
	// after the gateway refuses it (internal/runner's haltGrace).
	lgwHaltGrace = 60 * time.Second

	lgwRun1 = "20260930-120000-a1b2"
	lgwRun2 = "20260930-120100-c3d4"
)

const lgwConfig = `version: 1
project: aurora
git:
  provider: github
  base_branch: main
agent:
  auth: api-key
  model: ` + lgwCoder + `
  models:
    coder: ` + lgwCoder + `
    reviewer: ` + lgwReviewer + `
    background: ` + lgwBackground + `
  max_output_tokens:
    coder: 4000
    reviewer: 4000
  review_rounds: 1
  review: .fugaro/review.md
workflows:
  app:
    base: web-node
    commands:
      build: sh build.sh
      test: sh test.sh
    timeouts: { total: 25m, stage: 10m, verify: 1m, finalize_reserve: 1m }
`

// lgwReview makes the review ask for exactly one fix, which the task's own
// wording ("no comment lines") provokes, so the fix stage resumes
// implement's session.
const lgwReview = `Review the branch's diff against its base.

This repository's rule: every shell script must begin with a comment line
that says what the script does. A script without one is a finding, and the
verdict is "changes". If every script has one, the verdict is "ship".
`

const lgwTask = `Do these things in order, keeping every command short:

1. Open logo.png with the Read tool and write one sentence about its colour to notes.txt.
2. Create greet.sh, a shell script that prints hello. Put no comment lines in it.
3. Ask a subagent (the Task tool) to check that greet.sh prints hello, and wait for its answer.
4. Run "fugaro verify test" once, commit your work, and write a short pull request title and body to $FUGARO_STATE_DIR/pr.md.
`

// lgwCall is one "model call" log line of the gateway.
type lgwCall struct {
	Time            time.Time `json:"time"`
	Stage           string    `json:"stage"`
	Model           string    `json:"model"`
	ServingModel    string    `json:"serving_model"`
	Status          int       `json:"status"`
	In              int64     `json:"in"`
	CacheWrite5m    int64     `json:"cache_write_5m"`
	CacheWrite1h    int64     `json:"cache_write_1h"`
	CacheRead       int64     `json:"cache_read"`
	Out             int64     `json:"out"`
	ReservedMicros  int64     `json:"reserved_micros"`
	ChargedMicros   int64     `json:"charged_micros"`
	PricedAs        string    `json:"priced_as"`
	MaxTokens       int64     `json:"max_tokens"`
	ToolTypes       string    `json:"tool_types"`
	AgentID         string    `json:"agent_id"`
	SessionID       string    `json:"session_id"`
	Message         string    `json:"message"`
	StageFinishedAt time.Time `json:"-"`
}

func (c lgwCall) tokens() int64 {
	return c.In + c.CacheWrite5m + c.CacheWrite1h + c.CacheRead + c.Out
}

// lgwResult is what a stage's result event reported.
type lgwResult struct {
	Cost       float64
	Usage      int64
	ModelUsage int64
}

type lgwOutcome struct {
	rec       runstore.Record
	exit      int
	logs      []byte
	bucket    string
	calls     []lgwCall
	finished  map[string]time.Time // stage name -> its "stage finished" line
	results   map[string]lgwResult // stage name -> result event
	tiers     map[string]bool      // usage.service_tier seen in the transcripts
	geos      map[string]bool      // usage.inference_geo seen in the transcripts
	provider  fake.State
	secretHit []string // where the key was found; never the key itself
	// safeLogs is the run's output with the key blotted out: the only
	// form of it that is ever printed.
	safeLogs string
}

func TestLiveGateway(t *testing.T) {
	base := os.Getenv("FUGARO_LIVE_BASE_IMAGE")
	key := os.Getenv("FUGARO_LIVE_ANTHROPIC_API_KEY")
	if base == "" || key == "" || os.Getenv("FUGARO_LIVE_SPEND_OK") != "1" {
		t.Skip("check 20 spends real money on the Anthropic API: set FUGARO_LIVE_BASE_IMAGE, FUGARO_LIVE_ANTHROPIC_API_KEY (your own key) and FUGARO_LIVE_SPEND_OK=1 (docs/gcp-live-checklist.md)")
	}
	testutil.RequireDocker(t)
	testutil.IsolateGit(t)

	files := map[string]string{
		"fugaro.yaml":           lgwConfig,
		".fugaro/review.md":     lgwReview,
		"build.sh":              "#!/bin/sh\nexit 0\n",
		"test.sh":               "#!/bin/sh\nexit 0\n",
		"README.md":             "A tiny repository for the live gateway check.\n",
		"logo.png":              lgwPNG(t),
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:9"}}` + "\n",
		".gitignore":            "build/\n",
	}
	remote := testutil.NewRemote(t, files)

	// The first run: the pins, the cost, the tokens, the secret.
	o1 := lgwExec(t, base, key, remote, lgwRun1, "2.00", lgwTask)
	t.Logf("FACT: run 1 ended %s (outcome %s, exit %d), reason %q", o1.rec.Status, o1.rec.Outcome, o1.exit, o1.rec.Reason)
	if o1.rec.Status == runstore.StatusInfraError || o1.rec.Status == runstore.StatusHalted {
		t.Fatalf("run 1 ended %s: %s\n%s", o1.rec.Status, o1.rec.Reason, testutil.Tail(o1.safeLogs))
	}
	lgwCheckRouting(t, o1)
	lgwCheckPins(t, o1)
	lgwCheckCost(t, o1)
	lgwCheckOutput(t, o1)
	lgwCheckTokens(t, o1)
	lgwCheckImage(t, o1)

	// The second run: a cap below any call's worst case halts at the first.
	o2 := lgwExec(t, base, key, remote, lgwRun2, "0.002", lgwTask)
	lgwCheckHalt(t, o2)

	for i, o := range []lgwOutcome{o1, o2} {
		if len(o.secretHit) > 0 {
			t.Errorf("run %d: the planted real key is in %s (A-N5)", i+1, strings.Join(o.secretHit, ", "))
		}
	}
	if len(o1.secretHit)+len(o2.secretHit) == 0 {
		t.Logf("FACT: the real key is in no log, transcript, bucket object or provider state of either run")
	}
}

// lgwPNG is a small solid-colour PNG for the agent to look at.
func lgwPNG(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := range 32 {
		for x := range 32 {
			img.Set(x, y, color.RGBA{R: 0x2a, G: 0x6f, B: 0xdb, A: 0xff})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// lgwExec runs fugaro exec in the base image, against a file:// bucket and
// the fake provider, with the budget enforcing the given per-run cap.
func lgwExec(t *testing.T, base, key, remote, runID, capUSD, taskText string) lgwOutcome {
	t.Helper()
	run := t.TempDir()
	if err := os.MkdirAll(filepath.Join(run, "bucket"), 0o755); err != nil {
		t.Fatal(err)
	}
	spec, err := json.Marshal(map[string]any{"version": 1, "run_id": runID, "repo": "acme/app", "ref": "main", "task": taskText})
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, run, map[string]string{
		"task.json": string(spec),
		// The bare remote belongs to the host user and git in the
		// container runs as uid 1000.
		"gitconfig": "[safe]\n\tdirectory = *\n",
	})
	remoteDir := filepath.Dir(remote)
	testutil.ShareWithContainer(t, base, run)
	testutil.ShareWithContainer(t, base, remoteDir)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	// "-e ANTHROPIC_API_KEY" with no value takes it from the docker
	// process's environment, so the key is never in argv.
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm",
		"-v", remoteDir+":"+remoteDir, "-v", run+":/mnt/run",
		"-e", "ANTHROPIC_API_KEY",
		"-e", "FUGARO_BUDGET_MODE=enforce", "-e", "FUGARO_MAX_RUN_USD="+capUSD,
		// The repository's .claude/settings.json points ANTHROPIC_BASE_URL
		// at a dead port. The refusal that would stop the run steps aside,
		// so the managed settings' precedence is what is measured.
		"-e", "FUGARO_TEST_ALLOW_ROUTING_SETTINGS=1",
		"-e", "GIT_CONFIG_GLOBAL=/mnt/run/gitconfig",
		base, "fugaro", "exec",
		"--bucket", "file:///mnt/run/bucket?no_tmp_dir=true", "--task-file", "/mnt/run/task.json",
		"--remote", remote, "--provider", "fake", "--provider-state", "/mnt/run/provider.json",
		"--cancel-poll", "5s")
	cmd.Env = append(os.Environ(), "ANTHROPIC_API_KEY="+key)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	exit := 0
	if cmd.ProcessState != nil {
		exit = cmd.ProcessState.ExitCode()
	}
	logs := append(append([]byte{}, stdout.Bytes()...), stderr.Bytes()...)
	if runErr != nil && cmd.ProcessState == nil {
		t.Fatalf("docker run did not start: %v", runErr)
	}

	o := lgwOutcome{exit: exit, logs: logs, bucket: filepath.Join(run, "bucket"),
		finished: map[string]time.Time{}, results: map[string]lgwResult{}, tiers: map[string]bool{}, geos: map[string]bool{}}
	data, err := os.ReadFile(filepath.Join(o.bucket, "runs", acmeSlug, runID, "result.json"))
	if err != nil {
		t.Fatalf("run %s left no result.json (exit %d): %v\n%s", runID, exit, err, testutil.Tail(strings.ReplaceAll(string(logs), key, "[the API key]")))
	}
	if err := json.Unmarshal(data, &o.rec); err != nil {
		t.Fatal(err)
	}
	if o.provider, err = fake.Load(filepath.Join(run, "provider.json")); err != nil {
		t.Logf("no provider state: %v", err)
	}
	o.safeLogs = strings.ReplaceAll(string(logs), key, "[the API key]")
	lgwParseLogs(t, &o)
	lgwParseTranscripts(t, &o, runID)
	// The bare remote is where the agent's branch lands: it is scanned too.
	o.secretHit = lgwScanForKey(t, key, o, run, remoteDir)
	return o
}

func lgwParseLogs(t *testing.T, o *lgwOutcome) {
	t.Helper()
	for _, line := range bytes.Split(o.logs, []byte("\n")) {
		var c lgwCall
		if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &c) != nil {
			continue
		}
		switch c.Message {
		case "model call":
			o.calls = append(o.calls, c)
		case "stage finished":
			o.finished[c.Stage] = c.Time
		}
	}
}

func lgwParseTranscripts(t *testing.T, o *lgwOutcome, runID string) {
	t.Helper()
	dir := filepath.Join(o.bucket, "runs", acmeSlug, runID, "transcripts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Logf("no transcripts: %v", err)
		return
	}
	for _, e := range entries {
		stage, _, _ := strings.Cut(strings.TrimSuffix(e.Name(), ".jsonl"), "-")
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range bytes.Split(data, []byte("\n")) {
			var ev struct {
				Type    string  `json:"type"`
				Cost    float64 `json:"total_cost_usd"`
				Message struct {
					Usage struct {
						Tier string `json:"service_tier"`
						Geo  string `json:"inference_geo"`
					} `json:"usage"`
				} `json:"message"`
			}
			if len(line) == 0 || json.Unmarshal(line, &ev) != nil {
				continue
			}
			switch ev.Type {
			case "assistant":
				o.tiers[ev.Message.Usage.Tier] = true
				o.geos[ev.Message.Usage.Geo] = true
			case "result":
				var res struct {
					Usage struct {
						In            int64 `json:"input_tokens"`
						Out           int64 `json:"output_tokens"`
						CacheCreation int64 `json:"cache_creation_input_tokens"`
						CacheRead     int64 `json:"cache_read_input_tokens"`
					} `json:"usage"`
					ModelUsage map[string]struct {
						In            int64 `json:"inputTokens"`
						Out           int64 `json:"outputTokens"`
						CacheCreation int64 `json:"cacheCreationInputTokens"`
						CacheRead     int64 `json:"cacheReadInputTokens"`
					} `json:"modelUsage"`
				}
				if err := json.Unmarshal(line, &res); err != nil {
					t.Fatalf("transcript %s: %v", e.Name(), err)
				}
				r := o.results[stage]
				r.Cost += ev.Cost
				r.Usage += res.Usage.In + res.Usage.Out + res.Usage.CacheCreation + res.Usage.CacheRead
				for _, m := range res.ModelUsage {
					r.ModelUsage += m.In + m.Out + m.CacheCreation + m.CacheRead
				}
				o.results[stage] = r
			}
		}
	}
}

// lgwScanForKey looks for the real key in the run's logs, every file of its
// bucket and the provider state, and every file of the directories in
// more (the bare remote, which holds what the agent pushed, objects
// included), and returns where it found it, never the key itself.
func lgwScanForKey(t *testing.T, key string, o lgwOutcome, run string, more ...string) []string {
	t.Helper()
	var hits []string
	needle := []byte(key)
	if bytes.Contains(o.logs, needle) {
		hits = append(hits, "the run's output")
	}
	for _, root := range append([]string{run}, more...) {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if bytes.Contains(data, needle) {
				rel, _ := filepath.Rel(root, p)
				hits = append(hits, filepath.Base(root)+"/"+rel)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Git compresses its objects, so a key in a pushed file is not in the
	// bytes on disk: ask git what every branch of the remote holds. The
	// key goes to git on stdin, never in argv.
	for _, root := range more {
		for _, repo := range lgwBareRepos(root) {
			refs, err := exec.Command("git", "-C", repo, "for-each-ref", "--format=%(refname)", "refs/heads").Output()
			if err != nil {
				t.Fatalf("listing the branches of %s: %v", repo, err)
			}
			for _, ref := range strings.Fields(string(refs)) {
				cmd := exec.Command("git", "-C", repo, "grep", "-I", "-l", "-F", "-f", "-", ref)
				cmd.Stdin = strings.NewReader(key + "\n")
				out, err := cmd.Output()
				var ee *exec.ExitError
				if err != nil && !(errors.As(err, &ee) && ee.ExitCode() == 1) {
					t.Fatalf("searching %s %s: %v", filepath.Base(repo), ref, err)
				}
				if len(bytes.TrimSpace(out)) > 0 {
					hits = append(hits, filepath.Base(repo)+" "+ref+" (a pushed file)")
				}
			}
		}
	}
	return hits
}

// lgwBareRepos are the bare repositories directly under dir.
func lgwBareRepos(dir string) []string {
	var out []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(e.Name(), ".git") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// A6, A-N5, A-N7: the run reached the model through the gateway, although the
// repository's own settings pointed Claude Code at a dead port.
func lgwCheckRouting(t *testing.T, o lgwOutcome) {
	t.Helper()
	if len(o.calls) == 0 {
		t.Errorf("A6: the gateway logged no model call; the repository's settings may have won\n%s", testutil.Tail(o.safeLogs))
		return
	}
	if bytes.Contains(o.logs, []byte("ECONNREFUSED")) || bytes.Contains(o.logs, []byte("connection refused")) {
		t.Errorf("A6: the run hit a refused connection: Claude Code used the repository's ANTHROPIC_BASE_URL")
	}
	t.Logf("FACT: A6, A-N5 and A-N7: Claude Code read /etc/claude-code/managed-settings.json, its env beat the repository's ANTHROPIC_BASE_URL=http://127.0.0.1:9, and it accepted a plain http loopback base URL: the gateway logged %d calls", len(o.calls))
}

// A-N6: every call used the model pinned for its role in its stage.
func lgwCheckPins(t *testing.T, o lgwOutcome) {
	t.Helper()
	role := map[string]string{"implement": lgwCoder, "fix": lgwCoder, "review": lgwReviewer}
	seen := map[string]map[string]int{}
	for _, c := range o.calls {
		if seen[c.Stage] == nil {
			seen[c.Stage] = map[string]int{}
		}
		seen[c.Stage][c.Model]++
		want, known := role[c.Stage]
		if !known {
			t.Errorf("A-N6: a call in stage %q, which has no role", c.Stage)
		} else if c.Model != want && c.Model != lgwBackground {
			t.Errorf("A-N6: stage %s called %s; its pins are %s and %s", c.Stage, c.Model, want, lgwBackground)
		}
		if c.PricedAs != "table" {
			t.Errorf("A-N6: a call to %s in stage %s was priced as %q, not from the table (served by %q): the table lacks the model Anthropic reports, so every call is charged at the highest rates", c.Model, c.Stage, c.PricedAs, c.ServingModel)
		}
		if c.Status == 400 || c.Status == 403 || c.Status == 404 {
			t.Errorf("A-N6: the gateway refused a call in stage %s with status %d (a violation)", c.Stage, c.Status)
		}
	}
	for stage, want := range role {
		if n, ran := seen[stage]; ran && n[want] == 0 {
			t.Errorf("A-N6: stage %s never called its role model %s: %v", stage, want, n)
		}
	}
	if strings.Contains(o.rec.Reason, "not pinned") {
		t.Errorf("A-N6: the run failed on a pin: %s", o.rec.Reason)
	}
	for _, stage := range sortedKeys(seen) {
		t.Logf("FACT: A-N6 stage %s calls by model: %v (no violations)", stage, seen[stage])
	}
	agents := map[string]bool{}
	for _, c := range o.calls {
		if c.AgentID != "" {
			agents[c.AgentID] = true
		}
	}
	t.Logf("FACT: A-N6 distinct x-claude-code-agent-id values seen: %d (more than one means a subagent made calls)", len(agents))
	if _, ok := seen["fix"]; !ok {
		t.Errorf("the review did not ask for a fix, so the resumed fix stage was not exercised: adjust the task and run again")
	}
}

// A10, A11: the gateway's settled cost and Claude Code's total agree.
func lgwCheckCost(t *testing.T, o lgwOutcome) {
	t.Helper()
	var claude float64
	for _, r := range o.results {
		claude += r.Cost
	}
	c := o.rec.Cost
	if c == nil {
		t.Errorf("A10: the record has no cost block")
		return
	}
	if c.ModelSource != "gateway" {
		t.Errorf("A10: model_source = %q, want gateway", c.ModelSource)
	}
	gw := c.ModelUSD
	if larger := max(claude, gw); larger == 0 || abs(claude-gw) > 0.05*larger {
		t.Errorf("A10/A11: the gateway settled $%.4f and Claude Code's total_cost_usd sums to $%.4f: more than 5%% apart", gw, claude)
	}
	t.Logf("FACT: A10/A11 gateway cost $%.4f, Claude Code total_cost_usd $%.4f, by model %v, unreconciled $%.4f, usage_unparsed %d", gw, claude, c.ModelBy, c.Unreconciled, c.UsageUnparsed)
	t.Logf("FACT: the run's real spend was $%.2f against a $2.00 cap (expected about $0.30)", gw)
}

// A9, Anthropic side: the max_tokens of every call stayed within each
// role's max_output_tokens (the call log carries what Claude Code asked
// for), and so did what came back; the largest of each is recorded.
func lgwCheckOutput(t *testing.T, o lgwOutcome) {
	t.Helper()
	maxOut, maxAsked := map[string]int64{}, map[string]int64{}
	for _, c := range o.calls {
		maxOut[c.Model] = max(maxOut[c.Model], c.Out)
		maxAsked[c.Model] = max(maxAsked[c.Model], c.MaxTokens)
	}
	for _, m := range sortedKeys(maxOut) {
		if m != lgwBackground && maxOut[m] > lgwMaxOutput {
			t.Errorf("A9: model %s produced %d output tokens in one call, above its role's %d", m, maxOut[m], lgwMaxOutput)
		}
		if m != lgwBackground && maxAsked[m] > lgwMaxOutput {
			t.Errorf("A9: a call to %s asked for max_tokens %d, above its role's %d", m, maxAsked[m], lgwMaxOutput)
		}
		t.Logf("FACT: A9 model %s: largest max_tokens asked %d, largest output of one call %d tokens (role limit %d; the background model chooses its own)", m, maxAsked[m], maxOut[m], lgwMaxOutput)
	}
}

// A-N2: per stage, the result events' token counts against the gateway's.
func lgwCheckTokens(t *testing.T, o lgwOutcome) {
	t.Helper()
	gw := map[string]int64{}
	for _, c := range o.calls {
		gw[c.Stage] += c.tokens()
	}
	for _, stage := range sortedKeys(gw) {
		r := o.results[stage]
		claude := max(r.Usage, r.ModelUsage)
		t.Logf("FACT: A-N2 stage %s: result usage %d tokens, result modelUsage %d tokens, gateway %d tokens", stage, r.Usage, r.ModelUsage, gw[stage])
		if float64(claude) < 0.95*float64(gw[stage]) {
			t.Errorf("A-N2: stage %s: the result event counts %d tokens, more than 5%% below the gateway's %d: an oauth run's token cap would under-count", stage, claude, gw[stage])
		}
	}
}

// A-N8, A-N9: the image call's reservation covers its charge, and the
// service tier and geography Anthropic reported.
func lgwCheckImage(t *testing.T, o lgwOutcome) {
	t.Helper()
	worst := 0.0
	for _, c := range o.calls {
		if c.ReservedMicros > 0 && c.ChargedMicros > c.ReservedMicros {
			t.Errorf("A-N9: stage %s charged %d micro-dollars above its reservation of %d (the image's estimate may be too low)", c.Stage, c.ChargedMicros, c.ReservedMicros)
		}
		if c.ReservedMicros > 0 {
			worst = max(worst, float64(c.ChargedMicros)/float64(c.ReservedMicros))
		}
	}
	t.Logf("FACT: A-N9 across %d calls, including the one that read logo.png, the largest charge was %.1f%% of its reservation", len(o.calls), worst*100)
	t.Logf("FACT: A-N8 usage.service_tier seen: %v; usage.inference_geo seen: %v", sortedKeys(o.tiers), sortedKeys(o.geos))
	types := map[string]bool{}
	for _, c := range o.calls {
		for _, ty := range strings.Split(c.ToolTypes, ",") {
			if ty != "" {
				types[ty] = true
			}
		}
	}
	for ty := range types {
		if ty != "custom" {
			t.Errorf("A-N8: Claude Code sent a tool of type %q, which the gateway refuses", ty)
		}
	}
	t.Logf("FACT: A-N8 tools[].type seen in the gateway's call log: %v (the run ended %s)", sortedKeys(types), o.rec.Status)
}

// A-N1, R8: a cap below any call's worst case halts at the first call, and
// the agent exits by itself within the grace.
func lgwCheckHalt(t *testing.T, o lgwOutcome) {
	t.Helper()
	if o.rec.Status != runstore.StatusHalted || o.rec.Halt == nil || o.rec.Halt.Reason != runstore.HaltRunCap {
		t.Errorf("R8: with a $0.002 cap the run ended %s (halt %+v), want halted with run_cap", o.rec.Status, o.rec.Halt)
		return
	}
	if o.exit != 0 {
		t.Errorf("R8: a halted run exited %d, want 0", o.exit)
	}
	t.Logf("FACT: R8 the run ended halted (%s), outcome %s, exit %d, %d calls reached the model", o.rec.Halt.Reason, o.rec.Outcome, o.exit, len(o.calls))
	fin, ok := o.finished["implement"]
	if !ok {
		t.Errorf("A-N1: no \"stage finished\" line for implement")
		return
	}
	took := fin.Sub(o.rec.Halt.At)
	if took >= lgwHaltGrace {
		t.Errorf("A-N1: claude took %s after the 403 to end the stage; the grace is %s, so the runner had to kill it", took, lgwHaltGrace)
	}
	t.Logf("FACT: A-N1 claude exited %s after the gateway's 403 (grace %s)", took.Round(time.Millisecond), lgwHaltGrace)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return slices.Clip(keys)
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
