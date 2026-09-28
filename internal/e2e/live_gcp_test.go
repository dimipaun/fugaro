//go:build live

// The live end to end: the built fugaro CLI, with the real local config,
// launches one run on the sandbox's Cloud Run job and follows it to its PR
// (docs/gcp-live-checklist.md). It never runs in CI: it needs the `live`
// build tag, Application Default Credentials, the real local config and the
// sandbox's repository access token. Run it only after the bootstrap
// (docs/gcp-bootstrap.md) has been applied:
//
//	FUGARO_BITBUCKET_TOKEN="$(cat ~/.config/fugaro-bb-token)" \
//	  go test -tags live -p 1 -timeout 45m -run 'TestLive' -v ./internal/e2e/
//
// Guardrails, enforced before any call:
//   - The local config must name liveProject, liveRegion, a fugaro-runs-*
//     runs bucket and no endpoint or bucket_url override; the only
//     repository is liveRepo.
//   - Spend is capped before the launch: the sandbox job must have at most
//     1 CPU, 2Gi and a 30m task timeout, and the sandbox's fugaro.yaml at
//     most a $2 agent budget and a 20m total timeout. The test polls for at
//     most 30m.
//   - Cleanup is registered before the launch: it cancels the execution if
//     it is still going, declines the run's PR and deletes its fugaro/<id>
//     branch through the Bitbucket API, and deletes runs/<slug>/<id>/.
//   - FUGARO_BITBUCKET_TOKEN comes from the environment, is used only for
//     the cap check and the cleanup, never reaches the CLI's environment
//     and is never logged. Google credentials are ADC only.
//
// A -timeout abort skips t.Cleanup: run -run TestLiveGCPCleanup to sweep.
package e2e

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"
	"gocloud.dev/gcerrors"
	"google.golang.org/api/option"
	run "google.golang.org/api/run/v2"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/lock"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The one project, region and repository this test may touch.
const (
	liveProject      = "edge-devel-dimi"
	liveRegion       = "us-east5"
	liveRepo         = "edgeappinc/fugarosandbox"
	liveProvider     = "bitbucket"
	liveWorkflow     = "web"
	liveBaseBranch   = "master"
	liveBucketPrefix = "fugaro-runs-"
	livePrefix       = "fugaro-live-"
	liveBatchPrefix  = "live-"
)

// Spend caps checked before the launch.
const (
	liveMaxCPU       = 1
	liveMaxMemGiB    = 2
	liveMaxJobTime   = 30 * time.Minute
	liveMaxBudgetUSD = 2
	liveMaxTotal     = 20 * time.Minute
	livePollEvery    = 20 * time.Second
	livePollFor      = 30 * time.Minute
)

const liveTask = "Add a test to test.js checking that 2 + 2 is 4. Commit it, run fugaro verify test, and write pr.md."

type liveRig struct {
	t      *testing.T
	cfg    string // the local config's path
	lc     *localcfg.Config
	slug   string
	stamp  string
	token  string // the sandbox token: cleanup and the cap check only
	fugaro string
	bb     *httpjson.Client
	bucket *blobx.Bucket
	be     *gcp.Backend
}

func liveFact(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Log("FACT: " + fmt.Sprintf(format, args...))
}

// newLiveRig checks the guardrails and opens the bucket, the backend and
// the Bitbucket client. It makes no remote call but reads.
func newLiveRig(t *testing.T, needToken bool) *liveRig {
	t.Helper()
	t.Setenv("GOOGLE_SDK_GO_LOGGING_LEVEL", "")
	for _, v := range []string{"GOOGLE_CLOUD_PROJECT", "CLOUDSDK_CORE_PROJECT"} {
		if p := os.Getenv(v); p != "" && p != liveProject {
			t.Fatalf("%s=%s: the live tests run only against %s", v, p, liveProject)
		}
	}
	path, err := localcfg.Path(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Load(path)
	if err != nil {
		t.Fatalf("the live tests need the real local config: %v", err)
	}
	switch {
	case lc.Project != liveProject:
		t.Fatalf("local config %s names project %q; the live tests run only against %s", path, lc.Project, liveProject)
	case lc.Region != liveRegion:
		t.Fatalf("local config %s names region %q; the live tests run only in %s", path, lc.Region, liveRegion)
	case lc.Bucket != "" && lc.Bucket != "gs://"+lc.RunsBucket:
		t.Fatalf("local config %s sets bucket_url %q; the live tests need runs_bucket only", path, lc.Bucket)
	case !strings.HasPrefix(lc.RunsBucket, liveBucketPrefix):
		t.Fatalf("local config %s names runs bucket %q; the live tests need a %s* bucket", path, lc.RunsBucket, liveBucketPrefix)
	case lc.Endpoints != (localcfg.Endpoints{}):
		t.Fatalf("local config %s overrides API endpoints; the live tests talk only to Google", path)
	}
	if r, ok := lc.Repos[liveRepo]; !ok || (r.Provider != "" && r.Provider != liveProvider) {
		t.Fatalf("local config %s does not onboard %s (provider %s)", path, liveRepo, liveProvider)
	}
	token := os.Getenv("FUGARO_BITBUCKET_TOKEN")
	if needToken && token == "" {
		t.Fatal("FUGARO_BITBUCKET_TOKEN (the sandbox's repository access token) is needed to clean up the run's PR and branch")
	}
	var rnd [2]byte
	if _, err := crand.Read(rnd[:]); err != nil {
		t.Fatal(err)
	}
	r := &liveRig{t: t, cfg: path, lc: lc, slug: mustSlug(liveProvider, liveRepo), token: token,
		stamp: time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(rnd[:])}
	if token != "" {
		r.bb = &httpjson.Client{BaseURL: "https://api.bitbucket.org/2.0", Header: http.Header{"Accept": {"application/json"}},
			Auth: func(context.Context) (string, error) { return "Bearer " + token, nil }}
	}
	ctx := context.Background()
	if r.bucket, err = blobx.Open(ctx, "gs://"+lc.RunsBucket); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.bucket.Close() })
	if r.be, err = gcp.New(ctx, gcp.Options{Project: liveProject, Region: liveRegion, Warn: func(m string) { t.Log("backend warning: " + m) }}); err != nil {
		t.Fatal(err)
	}
	return r
}

// scrub removes the token from s, for anything logged.
func (r *liveRig) scrub(s string) string {
	if r.token == "" {
		return s
	}
	return strings.ReplaceAll(s, r.token, "[TOKEN]")
}

func (r *liveRig) repoPath(suffix string) string { return "/repositories/" + liveRepo + suffix }

// cli runs the built fugaro with the real local config, outside any
// checkout. Its environment holds no provider or model credential.
func (r *liveRig) cli(args ...string) (string, error) {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.fugaro, args...)
	cmd.Env = append(withoutEnv(os.Environ(), "FUGARO_CONFIG", "FUGARO_BITBUCKET_TOKEN", "ANTHROPIC_API_KEY",
		"CLAUDE_CODE_OAUTH_TOKEN", "GOOGLE_SDK_GO_LOGGING_LEVEL"), "FUGARO_CONFIG="+r.cfg)
	cmd.Dir = r.t.TempDir()
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		r.t.Logf("fugaro %s: %v\nstderr:\n%s", strings.Join(args, " "), err, r.scrub(stderr.String()))
	}
	return stdout.String(), err
}

// checkCaps refuses to launch unless the sandbox job and its fugaro.yaml
// are within the spend caps.
func (r *liveRig) checkCaps(ctx context.Context) {
	t := r.t
	t.Helper()
	svc, err := run.NewService(ctx, option.WithQuotaProject(liveProject))
	if err != nil {
		t.Fatal(err)
	}
	job := "projects/" + liveProject + "/locations/" + liveRegion + "/jobs/" + gcp.JobName(r.slug, liveWorkflow)
	j, err := svc.Projects.Locations.Jobs.Get(job).Context(ctx).Do()
	if err != nil {
		t.Fatalf("reading the sandbox job %s: %v", job, err)
	}
	if j.Template == nil || j.Template.Template == nil || len(j.Template.Template.Containers) == 0 || j.Template.Template.Containers[0].Resources == nil {
		t.Fatalf("job %s has no container resources", job)
	}
	limits := j.Template.Template.Containers[0].Resources.Limits
	cpu, cerr := liveCPU(limits["cpu"])
	mem, merr := backend.MemoryGiB(limits["memory"])
	timeout, terr := time.ParseDuration(j.Template.Template.Timeout)
	if err := errors.Join(cerr, merr, terr); err != nil {
		t.Fatalf("job %s limits %v, timeout %q: %v", job, limits, j.Template.Template.Timeout, err)
	}
	if cpu > liveMaxCPU || mem > liveMaxMemGiB || timeout > liveMaxJobTime || j.Template.Template.MaxRetries != 0 {
		t.Fatalf("job %s is %g CPU / %gGiB / %s / maxRetries %d: above the live caps (%d CPU / %dGi / %s / 0)",
			job, cpu, mem, timeout, j.Template.Template.MaxRetries, liveMaxCPU, liveMaxMemGiB, liveMaxJobTime)
	}
	liveFact(t, "sandbox job %s: cpu=%q memory=%q timeout=%s maxRetries=0", job, limits["cpu"], limits["memory"], timeout)

	// The agent's budget and the run's total timeout come from the
	// sandbox's fugaro.yaml on its base branch.
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.bitbucket.org/2.0"+r.repoPath("/src/"+liveBaseBranch+"/fugaro.yaml"), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(r.scrub(err.Error()))
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reading the sandbox's fugaro.yaml: HTTP %d", resp.StatusCode)
	}
	c, problems := config.Parse(data)
	if len(problems) > 0 {
		t.Fatalf("the sandbox's fugaro.yaml: %v", problems)
	}
	w, ok := c.Workflows[liveWorkflow]
	if !ok {
		t.Fatalf("the sandbox's fugaro.yaml has no %s workflow", liveWorkflow)
	}
	if c.Agent.MaxBudgetUSD > liveMaxBudgetUSD || w.Timeouts.Total.Duration > liveMaxTotal || len(c.Git.PR.Reviewers) > 0 {
		t.Fatalf("the sandbox's fugaro.yaml allows $%g / %s / %d reviewers: above the live caps ($%d / %s / none)",
			c.Agent.MaxBudgetUSD, w.Timeouts.Total.Duration, len(c.Git.PR.Reviewers), liveMaxBudgetUSD, liveMaxTotal)
	}
	liveFact(t, "sandbox fugaro.yaml: max_budget_usd=%g total=%s, no reviewers", c.Agent.MaxBudgetUSD, w.Timeouts.Total.Duration)
}

// liveCPU parses a Cloud Run CPU limit: "1", "0.5" or "1000m".
func liveCPU(s string) (float64, error) {
	num, div := s, 1.0
	if n, ok := strings.CutSuffix(s, "m"); ok {
		num, div = n, 1000
	}
	v, err := strconv.ParseFloat(num, 64)
	if err != nil || !(v > 0) {
		return 0, fmt.Errorf("CPU limit %q", s)
	}
	return v / div, nil
}

var liveRunIDRE = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$`)

// cleanupRun undoes one run: cancels its execution if it is still going,
// declines its open PRs and deletes its fugaro/<id> branch, and deletes its
// objects. Each step is logged; none stops the others.
func (r *liveRig) cleanupRun(id string) {
	t := r.t
	if !liveRunIDRE.MatchString(id) {
		t.Errorf("CLEANUP: refusing run %q: not a run ID", id)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	s := runstore.Open(r.bucket.Bucket, r.slug, id)
	var exec string
	if l, err := s.ReadLaunch(ctx); err == nil {
		exec = l.Execution
	}
	if rec, err := s.ReadRecord(ctx); err == nil && rec.Execution != "" {
		exec = rec.Execution
	}
	if exec != "" {
		if x, err := r.be.Execution(ctx, exec); err == nil && !x.State.Terminal() {
			t.Logf("CLEANUP: cancel execution %s: %v", exec, r.be.Cancel(ctx, exec))
		}
	}
	branch := gitops.RunBranchPrefix + id
	if r.bb == nil {
		t.Errorf("CLEANUP: no FUGARO_BITBUCKET_TOKEN: decline the PR of %s and delete that branch by hand", branch)
	} else {
		q := url.Values{"q": {fmt.Sprintf(`source.branch.name=%q AND state="OPEN"`, branch)}}
		var page struct {
			Values []struct{ ID int } `json:"values"`
		}
		if err := r.bb.Do(ctx, "GET", r.repoPath("/pullrequests?"+q.Encode()), nil, &page); err != nil {
			t.Errorf("CLEANUP: listing the PRs of %s: %s", branch, r.scrub(err.Error()))
		}
		for _, pr := range page.Values {
			err := r.bb.Do(ctx, "POST", r.repoPath(fmt.Sprintf("/pullrequests/%d/decline", pr.ID)), nil, nil)
			t.Logf("CLEANUP: decline PR #%d: %s", pr.ID, r.errText(err))
		}
		err := r.bb.Do(ctx, "DELETE", r.repoPath("/refs/branches/"+url.PathEscape(branch)), nil, nil)
		var se *httpjson.StatusError
		if errors.As(err, &se) && se.Status == http.StatusNotFound {
			err = nil // never pushed
		}
		t.Logf("CLEANUP: delete branch %s: %s", branch, r.errText(err))
	}
	r.deleteTree(ctx, "runs/"+r.slug+"/"+id+"/")
}

func (r *liveRig) errText(err error) string {
	if err == nil {
		return "ok"
	}
	return r.scrub(err.Error())
}

// liveDeletable is what deleteTree may remove: one run of the sandbox, or a
// fugaro-live-* prefix.
func (r *liveRig) liveDeletable(prefix string) bool {
	parts := strings.Split(strings.TrimSuffix(prefix, "/"), "/")
	if !strings.HasSuffix(prefix, "/") || len(parts) < 2 || len(parts) > 3 {
		return false
	}
	last := parts[len(parts)-1]
	switch {
	case parts[0] == "runs" && len(parts) == 3 && parts[1] == r.slug && liveRunIDRE.MatchString(last):
		return true
	case (parts[0] == "runs" || parts[0] == "cache" || parts[0] == "locks") && strings.HasPrefix(last, livePrefix):
		return true
	}
	return false
}

func (r *liveRig) deleteTree(ctx context.Context, prefix string) {
	t := r.t
	if !r.liveDeletable(prefix) {
		t.Errorf("CLEANUP: refusing to delete %q", prefix)
		return
	}
	it := r.bucket.List(&blob.ListOptions{Prefix: prefix})
	n := 0
	for {
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Errorf("CLEANUP: listing %s: %v", prefix, err)
			return
		}
		if err := r.bucket.Delete(ctx, obj.Key); err != nil && gcerrors.Code(err) != gcerrors.NotFound {
			t.Errorf("CLEANUP: deleting %s: %v", obj.Key, err)
			continue
		}
		n++
	}
	t.Logf("CLEANUP: deleted %d objects under %s", n, prefix)
}

// lsLive is ls --json of the batch.
func (r *liveRig) lsLive(batch string) (rows []map[string]any, totals map[string]any, err error) {
	out, err := r.cli("ls", "--json", "--repo", liveRepo, "--batch", batch, "--since", "1d")
	if err != nil {
		return nil, nil, err
	}
	var got struct {
		Runs   []map[string]any `json:"runs"`
		Totals map[string]any   `json:"totals"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		return nil, nil, fmt.Errorf("ls --json: %w\n%s", err, out)
	}
	return got.Runs, got.Totals, nil
}

func TestLiveSandboxRun(t *testing.T) {
	r := newLiveRig(t, true)
	ctx := context.Background()
	r.checkCaps(ctx)
	r.fugaro = testutil.BuildFugaro(t)
	id := newRunID(t)
	batch := liveBatchPrefix + r.stamp
	t.Cleanup(func() { r.cleanupRun(id) })

	out, err := r.cli("run", "--repo", liveRepo, "--run-id", id, "--batch", batch, "--json", liveTask)
	var launch struct{ Run, Status, Execution, Branch string }
	if err != nil || json.Unmarshal([]byte(out), &launch) != nil || launch.Status != "launched" {
		t.Fatalf("run: %s, %v", out, err)
	}
	liveFact(t, "run %s launched execution %s on branch %s", launch.Run, launch.Execution, launch.Branch)
	if launch.Run != r.slug+"/"+id || launch.Branch != gitops.RunBranchPrefix+id {
		t.Fatalf("launch %+v: want run %s/%s on %s%s", launch, r.slug, id, gitops.RunBranchPrefix, id)
	}

	var row map[string]any
	var rows []map[string]any
	var totals map[string]any
	lastState := ""
	start := time.Now()
	for deadline := start.Add(livePollFor); ; time.Sleep(livePollEvery) {
		rs, tt, err := r.lsLive(batch)
		if err == nil {
			rows, totals = rs, tt
			for _, x := range rs {
				if x["run_id"] == id {
					row = x
				}
			}
		}
		if row != nil {
			state := fmt.Sprintf("%v/%v", row["status"], row["stage"])
			if state != lastState {
				t.Logf("%s after %s: %s", id, time.Since(start).Round(time.Second), state)
				lastState = state
			}
			if row["terminal"] == true && row["status"] != "running" {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s did not finish within %s; last row %v", id, livePollFor, row)
		}
	}
	liveFact(t, "run %s ended %v at stage %v after about %s; PR %v", id, row["status"], row["stage"], time.Since(start).Round(time.Second), row["pr_url"])
	prURL, _ := row["pr_url"].(string)
	if (row["status"] != "succeeded" && row["status"] != "failed") || prURL == "" {
		t.Fatalf("row = %v: want succeeded or failed, with a PR", row)
	}

	// Cost (I-6): subscription basis; the total is compute to the cent.
	cost, _ := row["cost"].(map[string]any)
	compute, _ := cost["compute_usd"].(float64)
	total, _ := cost["total_usd"].(float64)
	liveFact(t, "cost %v", cost)
	if cost["model_basis"] != "subscription" || !(compute > 0) || math.Abs(total-math.Round(compute*100)/100) >= 1e-9 {
		t.Errorf("cost = %v: want model_basis subscription, compute_usd > 0 and total_usd = compute_usd to the cent", cost)
	}
	sum := 0.0
	for _, x := range rows {
		c, _ := x["cost"].(map[string]any)
		v, _ := c["total_usd"].(float64)
		sum += v
	}
	tot, _ := totals["total_usd"].(float64)
	liveFact(t, "ls totals %v; sum of %d rows' total_usd = %.4f", totals, len(rows), sum)
	if math.Abs(tot-sum) > 0.01+1e-9 {
		t.Errorf("totals.total_usd %.4f is not within 0.01 of the rows' sum %.4f", tot, sum)
	}

	// logs: a runner line and an agent tool event, waiting out ingestion.
	stage, tool := false, false
	for deadline := time.Now().Add(3 * time.Minute); !(stage && tool) && time.Now().Before(deadline); {
		out, err := r.cli("logs", "--json", id)
		if err == nil {
			for _, line := range strings.Split(out, "\n") {
				var l struct{ Message, Event string }
				if json.Unmarshal([]byte(line), &l) != nil {
					continue
				}
				stage = stage || strings.Contains(l.Message, "stage started")
				tool = tool || l.Event == "tool"
			}
		}
		if !(stage && tool) {
			time.Sleep(15 * time.Second)
		}
	}
	liveFact(t, `logs --json of %s: "stage started"=%v, "event":"tool"=%v`, id, stage, tool)
	if !stage || !tool {
		t.Errorf("logs lack a stage started line (%v) or an agent tool event (%v)", stage, tool)
	}

	diag, err := r.cli("diagnose", "--json", id)
	var d struct {
		Row map[string]any `json:"row"`
	}
	if err != nil || json.Unmarshal([]byte(diag), &d) != nil || d.Row["pr_url"] != prURL {
		t.Errorf("diagnose --json: %s, %v; want PR %s", diag, err, prURL)
	}

	// The objects the run leaves: result.json at writeback, no lock, a cache.
	s := runstore.Open(r.bucket.Bucket, r.slug, id)
	rec, err := s.ReadRecord(ctx)
	if err != nil {
		t.Fatal(err)
	}
	l, lerr := s.ReadLaunch(ctx)
	liveFact(t, "result.json: status=%s stage=%s outcome=%s execution=%s; launch.json execution=%v", rec.Status, rec.Stage, rec.Outcome, rec.Execution, launchExec(l, lerr))
	if rec.Stage != "writeback" {
		t.Errorf("result.json stage = %q, want writeback", rec.Stage)
	}
	if lerr != nil || !backend.SameExecution(rec.Execution, l.Execution) {
		t.Errorf("result.json execution %q vs launch.json %v", rec.Execution, launchExec(l, lerr))
	}
	lockKey := lock.Key(r.slug, rec.Branch)
	if ok, err := r.bucket.Exists(ctx, lockKey); err != nil || ok {
		t.Errorf("lock %s exists=%v (%v) after the run", lockKey, ok, err)
	}
	var archives []string
	it := r.bucket.List(&blob.ListOptions{Prefix: "cache/" + r.slug + "/" + liveWorkflow + "/"})
	for {
		obj, err := it.Next(ctx)
		if err != nil {
			break
		}
		if strings.HasSuffix(obj.Key, ".tar.zst") {
			archives = append(archives, fmt.Sprintf("%s (%d bytes)", obj.Key, obj.Size))
		}
	}
	liveFact(t, "cache archives of %s/%s: %v", r.slug, liveWorkflow, archives)
	if len(archives) == 0 {
		t.Errorf("no cache/%s/%s/*.tar.zst archive after the run", r.slug, liveWorkflow)
	}
}

func launchExec(l *runstore.Launch, err error) string {
	if err != nil {
		return err.Error()
	}
	return l.Execution
}

// TestLiveGCPCleanup sweeps what an aborted live run left: every sandbox run
// whose task.json carries a live-* batch (its execution cancelled, its PR
// declined, its branch and objects deleted), every fugaro-live-* object
// prefix, and every fugaro-live-* secret. Run it on its own (-p 1), never
// alongside other live tests: it removes their objects.
func TestLiveGCPCleanup(t *testing.T) {
	r := newLiveRig(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	ids, err := runstore.ListRunIDs(ctx, r.bucket.Bucket, r.slug, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		data, err := r.bucket.ReadAll(ctx, "runs/"+r.slug+"/"+id+"/task.json")
		if err != nil {
			continue
		}
		var tk struct {
			Batch string `json:"batch"`
		}
		if json.Unmarshal(data, &tk) != nil || !strings.HasPrefix(tk.Batch, liveBatchPrefix) {
			continue
		}
		t.Logf("sweeping run %s (batch %s)", id, tk.Batch)
		r.cleanupRun(id)
	}
	// fugaro-live-* prefixes, one level under each parent the tests use.
	parents := []string{"cache/", "locks/", "runs/" + r.slug + "/", "runs/" + r.slug + "-x/", "runs/other/", "cache/" + r.slug + "/", "locks/" + r.slug + "/"}
	for _, parent := range parents {
		it := r.bucket.List(&blob.ListOptions{Prefix: parent, Delimiter: "/"})
		for {
			obj, err := it.Next(ctx)
			if err != nil {
				break
			}
			if obj.IsDir && strings.HasPrefix(strings.TrimPrefix(obj.Key, parent), livePrefix) {
				r.deleteTree(ctx, obj.Key)
			}
		}
	}
	s, err := gcp.NewSecrets(ctx, gcp.Options{Project: liveProject, Region: liveRegion})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx, map[string]string{"fugaro_live": "true"})
	if err != nil {
		t.Fatal(err)
	}
	for _, sec := range list {
		if strings.HasPrefix(sec.ID, livePrefix) {
			t.Logf("CLEANUP: delete secret %s: %v", sec.ID, s.Delete(ctx, sec.ID))
		}
	}
}
