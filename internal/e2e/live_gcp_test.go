//go:build live

// The live end to end: the built fugaro CLI, with the real local config,
// launches one run on the sandbox's Cloud Run job and follows it to its PR
// (docs/gcp-live-checklist.md). It never runs in CI: it needs the `live`
// build tag, Application Default Credentials, the real local config and the
// sandbox's repository access token. Run it only after the checklist's
// setup (docs/gcp-setup.md) has been applied:
//
//	FUGARO_LIVE_PROJECT=<project> FUGARO_LIVE_REPO=<owner/name> \
//	FUGARO_BITBUCKET_TOKEN="$(cat <token file>)" \
//	  go test -tags live -p 1 -timeout 45m -run 'TestLive' -v ./internal/e2e/
//
// Guardrails, enforced before any call:
//   - FUGARO_LIVE_PROJECT and FUGARO_LIVE_REPO name the target (liveTarget),
//     and the local config must name the same project, onboard the same
//     repository, a fugaro-runs-* runs bucket and no endpoint or bucket_url
//     override; the only repository touched is liveRepo, in the local
//     config's region.
//   - Spend is capped before the launch: the sandbox job must have at most
//     1 CPU, 2Gi and a 30m task timeout, and the sandbox's fugaro.yaml at
//     most a $2 agent budget and a 20m total timeout. The run itself is
//     launched with --total-timeout 15m. The test polls for at most 30m.
//   - Cleanup is registered before the launch: it cancels the execution if
//     it is still going, declines the run's PR and deletes its fugaro/<id>
//     branch through the Bitbucket API, and deletes runs/<slug>/<id>/.
//   - FUGARO_BITBUCKET_TOKEN comes from the environment, is used only for
//     the cap check and the cleanup, never reaches the CLI's environment
//     and is never logged. Google credentials are ADC only.
//
// TestLiveSandboxFollowUp (checklist check 19) adds a follow-up of the
// run's PR. It pauses for the person running it to post review comments
// on that PR by hand, and needs their account ID in followup.trusted of
// the sandbox's fugaro.yaml on its base branch beforehand; it holds no
// credential but the sandbox's token, sends every provider request to the
// sandbox repository only (sandboxPath), and never posts a comment, pushes,
// or changes the repository's files or configuration: it declines its own
// PR, and its cleanup deletes that PR's branch. It runs only with
// FUGARO_LIVE_FOLLOWUP=1; run it with -timeout 100m.
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
	"path"
	"regexp"
	"slices"
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
	"github.com/dimipaun/fugaro/internal/followup"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/lock"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The sandbox's provider, workflow and branch, and the names this test creates.
const (
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

// liveTotalTimeout is the run's --total-timeout: below the sandbox's own
// timeouts.total, so it is a real override, which the execution's task
// timeout must carry (plus backend.TaskTimeoutSlack).
const liveTotalTimeout = 15 * time.Minute

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
	base   *config.Config // the sandbox's fugaro.yaml on its base branch, once checkCaps has read it
}

// The project, region and sandbox repository the live tests may touch. The
// project and repository come from FUGARO_LIVE_PROJECT and FUGARO_LIVE_REPO
// (for example my-fugaro-dev and acme/fugaro-sandbox), and the region from
// the local config; liveTarget sets them, and the guard then requires the
// local config to name the same project and onboard the same repository:
// two independent statements of the target, so neither a stray environment
// nor a stray config file alone can aim the tests elsewhere.
var liveProject, liveRegion, liveRepo string

var (
	liveProjectRE = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
	liveRepoRE    = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
)

// liveTarget reads FUGARO_LIVE_PROJECT and FUGARO_LIVE_REPO, and fails the
// test unless both are set and well formed.
func liveTarget(t *testing.T) {
	t.Helper()
	liveProject, liveRepo = os.Getenv("FUGARO_LIVE_PROJECT"), os.Getenv("FUGARO_LIVE_REPO")
	switch {
	case liveProject == "" || liveRepo == "":
		t.Fatal("set FUGARO_LIVE_PROJECT (the live-test GCP project) and FUGARO_LIVE_REPO (the sandbox repository, owner/name); the local config must name the same ones")
	case !liveProjectRE.MatchString(liveProject):
		t.Fatalf("FUGARO_LIVE_PROJECT=%q is not a GCP project ID", liveProject)
	case !liveRepoRE.MatchString(liveRepo):
		t.Fatalf("FUGARO_LIVE_REPO=%q is not owner/name", liveRepo)
	}
}

func liveFact(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Log("FACT: " + fmt.Sprintf(format, args...))
}

// newLiveRig checks the guardrails and opens the bucket, the backend and
// the Bitbucket client. It makes no remote call but reads.
func newLiveRig(t *testing.T, needToken bool) *liveRig {
	t.Helper()
	liveTarget(t)
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
	case lc.Region == "":
		t.Fatalf("local config %s names no region", path)
	case lc.Bucket != "" && lc.Bucket != "gs://"+lc.RunsBucket:
		t.Fatalf("local config %s sets bucket_url %q; the live tests need runs_bucket only", path, lc.Bucket)
	case !strings.HasPrefix(lc.RunsBucket, liveBucketPrefix):
		t.Fatalf("local config %s names runs bucket %q; the live tests need a %s* bucket", path, lc.RunsBucket, liveBucketPrefix)
	case lc.Endpoints != (localcfg.Endpoints{}):
		t.Fatalf("local config %s overrides API endpoints; the live tests talk only to Google", path)
	}
	liveRegion = lc.Region
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
// are within the spend caps. It returns the workflow's timeouts.total.
func (r *liveRig) checkCaps(ctx context.Context) time.Duration {
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
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.bitbucket.org/2.0"+r.sandboxPath(r.repoPath("/src/"+liveBaseBranch+"/fugaro.yaml")), nil)
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
	r.base = c
	return w.Timeouts.Total.Duration
}

// sandboxPath refuses a Bitbucket API path outside the sandbox
// repository, so no request of these tests can reach another one.
func (r *liveRig) sandboxPath(p string) string {
	r.t.Helper()
	// Dot segments or a non-clean path could step out of the sandbox
	// after the prefix check, so they are refused too.
	clean, _, _ := strings.Cut(p, "?")
	if root := "/repositories/" + liveRepo; (clean != root && !strings.HasPrefix(clean, root+"/")) ||
		path.Clean(clean) != clean {
		r.t.Fatalf("refusing a Bitbucket request outside %s: %q", liveRepo, p)
	}
	return p
}

// bbDo is one Bitbucket API request, only ever to the sandbox repository.
// Its error is logged only through errText, which scrubs the token.
func (r *liveRig) bbDo(ctx context.Context, method, path string, in, out any) error {
	r.t.Helper()
	if r.bb == nil {
		return errors.New("no FUGARO_BITBUCKET_TOKEN")
	}
	return r.bb.Do(ctx, method, r.sandboxPath(path), in, out)
}

// fact is liveFact with the token scrubbed.
func (r *liveRig) fact(format string, args ...any) {
	r.t.Helper()
	r.t.Log(r.scrub("FACT: " + fmt.Sprintf(format, args...)))
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
// declines the open PRs of its branch and deletes that branch (a first
// run's fugaro/<id>, a follow-up's PR branch; see branchOf), and deletes
// its objects. Each step is logged; none stops the others.
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
	branch, ok := r.branchOf(ctx, s, id)
	switch {
	case !ok:
		t.Logf("CLEANUP: not touching the branch of run %s: its first run is not a live-test run", id)
	case r.bb == nil:
		t.Errorf("CLEANUP: no FUGARO_BITBUCKET_TOKEN: decline the PR of %s and delete that branch by hand", branch)
	default:
		q := url.Values{"q": {fmt.Sprintf(`source.branch.name=%q AND state="OPEN"`, branch)}}
		var page struct {
			Values []struct{ ID int } `json:"values"`
		}
		if err := r.bbDo(ctx, "GET", r.repoPath("/pullrequests?"+q.Encode()), nil, &page); err != nil {
			t.Errorf("CLEANUP: listing the PRs of %s: %s", branch, r.errText(err))
		}
		for _, pr := range page.Values {
			err := r.bbDo(ctx, "POST", r.repoPath(fmt.Sprintf("/pullrequests/%d/decline", pr.ID)), nil, nil)
			t.Logf("CLEANUP: decline PR #%d: %s", pr.ID, r.errText(err))
		}
		err := r.bbDo(ctx, "DELETE", r.repoPath("/refs/branches/"+url.PathEscape(branch)), nil, nil)
		var se *httpjson.StatusError
		if errors.As(err, &se) && se.Status == http.StatusNotFound {
			err = nil // never pushed
		}
		t.Logf("CLEANUP: delete branch %s: %s", branch, r.errText(err))
	}
	r.deleteTree(ctx, "runs/"+r.slug+"/"+id+"/")
}

// branchOf is the branch run id works on, and whether cleanup may touch
// it: a first run's own fugaro/<id>, or a follow-up's branch (its task's),
// which is another run's fugaro/<root>. A follow-up's task is written by
// the CLI but sits in a prefix the sandbox's agent can write, so its
// branch is cleaned up only when it has the Fugaro form and its first run
// is a live-test run too (a live-* batch).
func (r *liveRig) branchOf(ctx context.Context, s *runstore.Store, id string) (string, bool) {
	own := gitops.RunBranchPrefix + id
	spec, err := s.ReadTask(ctx)
	if err != nil || spec.Branch == "" || spec.Branch == own {
		return own, true
	}
	root, ok := task.BranchRunID(spec.Branch)
	if !ok {
		return "", false
	}
	rootTask, err := s.Sibling(root).ReadTask(ctx)
	if err != nil || !strings.HasPrefix(rootTask.Batch, liveBatchPrefix) {
		return "", false
	}
	return spec.Branch, true
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

// waitRun polls ls --batch until run id's row is terminal, for at most
// livePollFor, and returns that row with the batch's rows and totals.
func (r *liveRig) waitRun(batch, id string) (row map[string]any, rows []map[string]any, totals map[string]any) {
	r.t.Helper()
	return r.waitRunFor(batch, id, livePollFor)
}

// waitRunFor is waitRun with its own limit.
func (r *liveRig) waitRunFor(batch, id string, limit time.Duration) (row map[string]any, rows []map[string]any, totals map[string]any) {
	t := r.t
	t.Helper()
	lastState := ""
	start := time.Now()
	for deadline := start.Add(limit); ; time.Sleep(livePollEvery) {
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
			t.Fatalf("run %s did not finish within %s; last row %v", id, limit, row)
		}
	}
	liveFact(t, "run %s ended %v at stage %v after about %s; PR %v", id, row["status"], row["stage"], time.Since(start).Round(time.Second), row["pr_url"])
	return row, rows, totals
}

// checkSession checks that run id saved its Claude Code session
// (session/session.json and its file) and recorded the commit it pushed.
func (r *liveRig) checkSession(ctx context.Context, id string, rec *runstore.Record) {
	t := r.t
	t.Helper()
	m, data, err := runstore.Open(r.bucket.Bucket, r.slug, id).ReadSession(ctx)
	switch {
	case err != nil:
		t.Errorf("run %s saved no usable session: %v", id, err)
	case !m.Pushed || m.HeadSHA != rec.PushedHead || int64(len(data)) != m.Bytes:
		t.Errorf("run %s session.json = %+v with %d bytes; want pushed, head_sha = pushed_head %q, bytes = the file's size", id, *m, len(data), rec.PushedHead)
	default:
		r.fact("run %s saved session %s (%d bytes, head %s, workdir %s)", id, m.ID, m.Bytes, m.HeadSHA, m.WorkDir)
	}
	r.fact("run %s result.json pushed_head=%q head_sha=%q", id, rec.PushedHead, rec.HeadSHA)
	if rec.PushedHead == "" {
		t.Errorf("run %s has no pushed_head", id)
	}
}

func TestLiveSandboxRun(t *testing.T) {
	r := newLiveRig(t, true)
	ctx := context.Background()
	if total := r.checkCaps(ctx); liveTotalTimeout >= total {
		t.Fatalf("--total-timeout %s is not below the sandbox's timeouts.total %s, so it would not test an override", liveTotalTimeout, total)
	}
	r.fugaro = testutil.BuildFugaro(t)
	id := newRunID(t)
	batch := liveBatchPrefix + r.stamp
	t.Cleanup(func() { r.cleanupRun(id) })

	out, err := r.cli("run", "--repo", liveRepo, "--run-id", id, "--batch", batch, "--total-timeout", liveTotalTimeout.String(), "--json", liveTask)
	var launch struct{ Run, Status, Execution, Branch string }
	if err != nil || json.Unmarshal([]byte(out), &launch) != nil || launch.Status != "launched" {
		t.Fatalf("run: %s, %v", out, err)
	}
	liveFact(t, "run %s launched execution %s on branch %s", launch.Run, launch.Execution, launch.Branch)
	if launch.Run != r.slug+"/"+id || launch.Branch != gitops.RunBranchPrefix+id {
		t.Fatalf("launch %+v: want run %s/%s on %s%s", launch, r.slug, id, gitops.RunBranchPrefix, id)
	}
	r.checkExecutionTimeout(ctx, launch.Execution)

	row, rows, totals := r.waitRun(batch, id)
	prURL, _ := row["pr_url"].(string)
	if (row["status"] != "succeeded" && row["status"] != "failed") || prURL == "" {
		t.Fatalf("row = %v: want succeeded or failed, with a PR", row)
	}

	// Cost: subscription basis; the total is compute to the cent.
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
	// Every run saves its session, so its PR's first follow-up can resume it.
	r.checkSession(ctx, id, rec)
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

// The follow-up check's task, and how long it waits for the person running
// it to post the review comments (FUGARO_LIVE_COMMENT_WAIT overrides it).
const (
	liveFollowUpTask   = "Also add a line to the README saying the follow-up ran."
	liveCommentWait    = 20 * time.Minute
	liveMaxCommentPage = 20
	// liveRefusedPollFor bounds the wait for the follow-up of the declined
	// PR, which ends at bootstrap. With two runs of livePollFor and the
	// comment wait, the worst case stays under the documented -timeout
	// 100m (30 + 20 + 30 + 10 minutes, plus the build).
	liveRefusedPollFor = 10 * time.Minute
)

// liveLaunch is run --json's output.
type liveLaunch struct {
	Run         string `json:"run"`
	Status      string `json:"status"`
	Execution   string `json:"execution"`
	Branch      string `json:"branch"`
	PR          int    `json:"pr"`
	PreviousRun string `json:"previous_run"`
}

// launch runs fugaro run with args and requires a new launch.
func (r *liveRig) launch(args ...string) liveLaunch {
	r.t.Helper()
	out, err := r.cli(append([]string{"run"}, args...)...)
	var l liveLaunch
	if err != nil || json.Unmarshal([]byte(out), &l) != nil || l.Status != "launched" {
		r.t.Fatalf("run %s: %s, %v", strings.Join(args, " "), out, err)
	}
	r.fact("run %s launched execution %s on branch %s (pr %d, previous run %q)", l.Run, l.Execution, l.Branch, l.PR, l.PreviousRun)
	return l
}

// liveComment is a Bitbucket pull request comment, as far as these tests
// read one. Bodies are never logged: anyone who can comment wrote them.
type liveComment struct {
	ID      int `json:"id"`
	Content struct {
		Raw  string `json:"raw"`
		HTML string `json:"html"`
	} `json:"content"`
	User *struct {
		AccountID   string `json:"account_id"`
		DisplayName string `json:"display_name"`
	} `json:"user"`
	Inline *struct {
		Path string `json:"path"`
	} `json:"inline"`
	Parent *struct {
		ID int `json:"id"`
	} `json:"parent"`
	Deleted bool `json:"deleted"`
	Pending bool `json:"pending"`
}

func (c liveComment) author() string {
	if c.User == nil {
		return ""
	}
	return c.User.AccountID
}

// comments lists pull request n's comments with the repository token
// (read only), following the listing's next links only within the
// sandbox repository.
func (r *liveRig) comments(ctx context.Context, n int) ([]liveComment, error) {
	var all []liveComment
	p := r.repoPath(fmt.Sprintf("/pullrequests/%d/comments?pagelen=100", n))
	for page := 0; p != ""; page++ {
		if page == liveMaxCommentPage {
			return nil, fmt.Errorf("PR #%d has more than %d pages of comments", n, liveMaxCommentPage)
		}
		var body struct {
			Values []liveComment `json:"values"`
			Next   string        `json:"next"`
		}
		if err := r.bbDo(ctx, "GET", p, nil, &body); err != nil {
			return nil, err
		}
		all = append(all, body.Values...)
		p = ""
		if body.Next != "" {
			next, err := r.bb.PagePath(body.Next)
			if err != nil {
				return nil, err
			}
			p = next
		}
	}
	return all, nil
}

// commentIDs is the sorted IDs of cs.
func commentIDs(cs []liveComment) []int {
	ids := make([]int, len(cs))
	for i, c := range cs {
		ids[i] = c.ID
	}
	slices.Sort(ids)
	return ids
}

// waitForComments waits, for at most wait, until pull request n has both
// an inline comment and a general one (not a reply) by someone other than
// the PR's author, who is Fugaro's own identity. It never posts anything.
func (r *liveRig) waitForComments(ctx context.Context, n int, prAuthor string, wait time.Duration) (inline, general liveComment) {
	t := r.t
	t.Helper()
	for deadline := time.Now().Add(wait); ; time.Sleep(livePollEvery) {
		cs, err := r.comments(ctx, n)
		if err != nil {
			t.Logf("listing PR #%d's comments: %s", n, r.errText(err))
		}
		var gotInline, gotGeneral bool
		for _, c := range cs {
			if c.Deleted || c.Pending || c.author() == "" || c.author() == prAuthor {
				continue
			}
			switch {
			case c.Inline != nil && !gotInline:
				inline, gotInline = c, true
			case c.Inline == nil && c.Parent == nil && !gotGeneral:
				general, gotGeneral = c, true
			}
		}
		if gotInline && gotGeneral {
			return inline, general
		}
		if time.Now().After(deadline) {
			t.Fatalf("PR #%d got no inline and general comment by someone other than its author within %s (inline %v, general %v); set FUGARO_LIVE_COMMENT_WAIT for longer", n, wait, gotInline, gotGeneral)
		}
	}
}

// openPRs is the IDs of the open pull requests from branch.
func (r *liveRig) openPRs(ctx context.Context, branch string) []int {
	r.t.Helper()
	q := url.Values{"q": {fmt.Sprintf(`source.branch.name=%q AND state="OPEN"`, branch)}}
	var page struct {
		Values []struct{ ID int } `json:"values"`
	}
	if err := r.bbDo(ctx, "GET", r.repoPath("/pullrequests?"+q.Encode()), nil, &page); err != nil {
		r.t.Fatalf("listing the open PRs of %s: %s", branch, r.errText(err))
	}
	ids := make([]int, len(page.Values))
	for i, v := range page.Values {
		ids[i] = v.ID
	}
	return ids
}

// jsonInt reads a JSON number decoded into an any.
func jsonInt(v any) int {
	f, _ := v.(float64)
	return int(f)
}

// TestLiveSandboxFollowUp is check 19: a first run, a follow-up acting on
// review comments the person running the test posts by hand, and a
// follow-up refused because its PR was declined. It holds no credential
// but the sandbox's repository access token, and never posts a comment,
// pushes, or changes the repository's files or configuration (it declines
// its own PR, and its cleanup deletes the PR's branch): the comments come from the person, and the
// trusted account ID from the sandbox's fugaro.yaml on its base branch,
// which that person edits beforehand. It runs only with
// FUGARO_LIVE_FOLLOWUP=1, so a plain -run TestLive never waits for a
// person. Run it with -timeout 100m.
func TestLiveSandboxFollowUp(t *testing.T) {
	if os.Getenv("FUGARO_LIVE_FOLLOWUP") != "1" {
		t.Skip("check 19 pauses for a person to post review comments: set FUGARO_LIVE_FOLLOWUP=1 to run it (docs/gcp-live-checklist.md)")
	}
	r := newLiveRig(t, true)
	ctx := context.Background()
	wait := liveCommentWait
	if v := os.Getenv("FUGARO_LIVE_COMMENT_WAIT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			t.Fatalf("FUGARO_LIVE_COMMENT_WAIT=%q is not a positive duration", v)
		}
		wait = d
	}
	// Everything that can fail for free fails before the first launch.
	if total := r.checkCaps(ctx); liveTotalTimeout >= total {
		t.Fatalf("--total-timeout %s is not below the sandbox's timeouts.total %s", liveTotalTimeout, total)
	}
	trusted := r.base.Followup.Trusted
	if len(trusted) == 0 {
		t.Fatalf("the sandbox's fugaro.yaml on %s trusts nobody: add your Bitbucket account_id under followup.trusted there first (docs/gcp-live-checklist.md, check 19), then rerun", liveBaseBranch)
	}
	r.fact("sandbox fugaro.yaml: followup.trusted lists %d account IDs, allow_public=%v", len(trusted), r.base.Followup.AllowPublic)
	var repo struct {
		IsPrivate *bool `json:"is_private"`
	}
	if err := r.bbDo(ctx, "GET", r.repoPath(""), nil, &repo); err != nil || repo.IsPrivate == nil {
		t.Fatalf("reading the sandbox repository: %s (is_private %v)", r.errText(err), repo.IsPrivate)
	}
	r.fact("sandbox repository is_private=%v", *repo.IsPrivate)
	if !*repo.IsPrivate {
		t.Fatalf("the sandbox repository is public: the follow-up check runs only on a private one")
	}

	r.fugaro = testutil.BuildFugaro(t)
	batch := liveBatchPrefix + r.stamp
	total := "--total-timeout=" + liveTotalTimeout.String()

	// 1. The first run, whose PR the follow-ups continue.
	id := newRunID(t)
	t.Cleanup(func() { r.cleanupRun(id) })
	branch := gitops.RunBranchPrefix + id
	if l := r.launch("--repo", liveRepo, "--run-id", id, "--batch", batch, total, "--json", liveTask); l.Branch != branch {
		t.Fatalf("launch %+v: want branch %s", l, branch)
	}
	row, _, _ := r.waitRun(batch, id)
	prURL, _ := row["pr_url"].(string)
	n := jsonInt(row["pr"])
	if (row["status"] != "succeeded" && row["status"] != "failed") || prURL == "" || n <= 0 {
		t.Fatalf("row = %v: want succeeded or failed, with a PR", row)
	}

	// 2. The person posts the comments; the test only reads.
	var pr struct {
		Author struct {
			AccountID string `json:"account_id"`
		} `json:"author"`
	}
	if err := r.bbDo(ctx, "GET", r.repoPath(fmt.Sprintf("/pullrequests/%d", n)), nil, &pr); err != nil || pr.Author.AccountID == "" {
		t.Fatalf("reading PR #%d: %s (author.account_id %q)", n, r.errText(err), pr.Author.AccountID)
	}
	r.fact("PR #%d author.account_id=%q (the repository token's identity)", n, pr.Author.AccountID)
	t.Logf("ACTION: post one inline and one general review comment on %s in the Bitbucket UI now (waiting up to %s)", prURL, wait)
	inline, general := r.waitForComments(ctx, n, pr.Author.AccountID, wait)
	for _, c := range []liveComment{inline, general} {
		kind := "general"
		if c.Inline != nil {
			kind = "inline on " + c.Inline.Path
		}
		r.fact("comment %d (%s) by account_id=%q display_name=%q", c.ID, kind, c.author(), c.User.DisplayName)
		if !slices.Contains(trusted, c.author()) {
			t.Fatalf("add %s to followup.trusted in the sandbox's fugaro.yaml on %s, then rerun", c.author(), liveBaseBranch)
		}
	}

	// 3. The follow-up, on the same branch and PR.
	fid := newRunID(t)
	t.Cleanup(func() { r.cleanupRun(fid) })
	fl := r.launch("--repo", liveRepo, "--pr", strconv.Itoa(n), "--run-id", fid, "--batch", batch, total, "--json", liveFollowUpTask)
	if fl.Branch != branch || fl.PR != n || fl.PreviousRun != id {
		t.Fatalf("follow-up launch %+v: want branch %s, pr %d, previous run %s", fl, branch, n, id)
	}
	frow, _, _ := r.waitRun(batch, fid)
	if (frow["status"] != "succeeded" && frow["status"] != "failed") || frow["pr_url"] != prURL {
		t.Errorf("follow-up row = %v: want succeeded or failed, on %s", frow, prURL)
	}

	// One PR, with one report per run, each carrying its run's marker.
	if open := r.openPRs(ctx, branch); len(open) != 1 || open[0] != n {
		t.Errorf("open PRs from %s = %v, want exactly #%d", branch, open, n)
	}
	cs, err := r.comments(ctx, n)
	if err != nil {
		t.Fatalf("listing PR #%d's comments: %s", n, r.errText(err))
	}
	reports := map[string]liveComment{}
	for _, c := range cs {
		if run, ok := gitprov.FugaroRun(c.Content.Raw); ok && strings.HasPrefix(strings.TrimSpace(c.Content.Raw), "### Fugaro run") {
			if _, dup := reports[run]; dup {
				t.Errorf("run %s posted more than one report on PR #%d", run, n)
			}
			reports[run] = c
		}
	}
	for _, run := range []string{id, fid} {
		c, ok := reports[run]
		if !ok {
			t.Errorf("PR #%d has no report of run %s", n, run)
			continue
		}
		cost := ""
		for _, l := range strings.Split(c.Content.Raw, "\n") {
			if strings.HasPrefix(l, "**Cost:**") {
				cost = l
			}
		}
		r.fact("report of %s: comment %d, marker in the raw text %v, the rendered HTML shows %q as text %v; %s", run, c.ID,
			strings.Contains(c.Content.Raw, gitprov.ReportMarker(run)), "fugaro:report", strings.Contains(c.Content.HTML, "fugaro:report"), cost)
	}
	if c, ok := reports[fid]; ok {
		for _, who := range []liveComment{inline, general} {
			if name := followup.MarkdownName(who.User.DisplayName); !strings.Contains(c.Content.Raw, name) || !strings.Contains(c.Content.Raw, "**Comments used:**") {
				t.Errorf("the follow-up's report does not name %s as a trusted author", name)
			}
		}
	}

	// What the follow-up got, and what it recorded.
	fs := runstore.Open(r.bucket.Bucket, r.slug, fid)
	data, err := fs.ReadFile(ctx, "comments.json")
	var snap followup.Snapshot
	if err == nil {
		err = json.Unmarshal(data, &snap)
	}
	if err != nil {
		t.Errorf("the follow-up's comments.json: %v", err)
	}
	var sawInline, sawGeneral bool
	for _, c := range snap.Comments {
		sawInline = sawInline || (c.Kind == string(gitprov.CommentInline) && c.AuthorID == inline.author())
		sawGeneral = sawGeneral || (c.Kind == string(gitprov.CommentGeneral) && c.AuthorID == general.author())
		if strings.Contains(c.Body, "fugaro:report") || strings.Contains(c.Body, "### Fugaro run") {
			t.Errorf("comments.json holds a Fugaro report (a %s comment by %q)", c.Kind, c.Author)
		}
	}
	r.fact("comments.json: %d comments, authors %v, omitted %v, since %s, fetched_at %s", len(snap.Comments), snap.Authors, snap.Omitted, snap.Since, snap.Fetched)
	if !sawInline || !sawGeneral {
		t.Errorf("comments.json lacks the inline (%v) or the general (%v) comment", sawInline, sawGeneral)
	}
	frec, err := fs.ReadRecord(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fu := frec.FollowUp; fu == nil {
		t.Errorf("the follow-up's result.json has no follow_up block")
	} else {
		r.fact("follow-up result.json: status=%s outcome=%s follow_up session=%q note=%q comments=%d authors=%v untrusted=%v omitted=%v",
			frec.Status, frec.Outcome, fu.Session, fu.SessionNote, fu.Comments, fu.Authors, fu.UntrustedAuthors, fu.Omitted)
		if fu.PR != n || fu.PreviousRun != id || fu.Comments < 2 {
			t.Errorf("follow_up = %+v: want pr %d, previous_run %s and at least 2 comments", *fu, n, id)
		}
		if fu.Session != "resumed" {
			t.Errorf("the follow-up started a fresh session (%s); it should have resumed %s's", fu.SessionNote, id)
		}
	}
	r.checkSession(ctx, fid, frec)
	if ok, err := r.bucket.Exists(ctx, lock.Key(r.slug, branch)); err != nil || ok {
		t.Errorf("the lock of %s exists=%v (%v) after the follow-up", branch, ok, err)
	}
	out, err := r.cli("ls", "--json", "--repo", liveRepo, "--pr", strconv.Itoa(n))
	var ls struct {
		Runs   []map[string]any `json:"runs"`
		Totals map[string]any   `json:"totals"`
	}
	if err != nil || json.Unmarshal([]byte(out), &ls) != nil {
		t.Errorf("ls --pr %d: %s, %v", n, out, err)
	}
	var lsIDs []string
	for _, x := range ls.Runs {
		s, _ := x["run_id"].(string)
		lsIDs = append(lsIDs, s)
	}
	slices.Sort(lsIDs)
	r.fact("ls --pr %d: runs %v, totals %v", n, lsIDs, ls.Totals)
	if want := []string{id, fid}; !slices.Equal(lsIDs, want) || jsonInt(ls.Totals["runs"]) != 2 {
		t.Errorf("ls --pr %d lists %v (totals %v), want %v", n, lsIDs, ls.Totals, want)
	}
	diag, err := r.cli("diagnose", "--json", fid)
	var d struct {
		FollowUp map[string]any `json:"follow_up"`
	}
	if err != nil || json.Unmarshal([]byte(diag), &d) != nil || d.FollowUp == nil {
		t.Errorf("diagnose --json %s: %v, no follow_up block", fid, err)
	}
	r.fact("diagnose --json %s: follow_up %v", fid, d.FollowUp)

	// 4. A follow-up of a declined PR: the CLI can't see the PR's state,
	// so it launches, and the runner refuses at bootstrap, touching
	// nothing. The branch is kept for it.
	if err := r.bbDo(ctx, "POST", r.repoPath(fmt.Sprintf("/pullrequests/%d/decline", n)), nil, nil); err != nil {
		t.Fatalf("declining PR #%d: %s", n, r.errText(err))
	}
	if err := r.bbDo(ctx, "GET", r.repoPath("/refs/branches/"+url.PathEscape(branch)), nil, nil); err != nil {
		t.Fatalf("branch %s is gone after declining PR #%d: %s", branch, n, r.errText(err))
	}
	before, err := r.comments(ctx, n)
	if err != nil {
		t.Fatalf("listing PR #%d's comments: %s", n, r.errText(err))
	}
	rid := newRunID(t)
	t.Cleanup(func() { r.cleanupRun(rid) })
	r.launch("--repo", liveRepo, "--pr", strconv.Itoa(n), "--run-id", rid, "--batch", batch, total, "--json")
	rrow, _, _ := r.waitRunFor(batch, rid, liveRefusedPollFor)
	reason, _ := rrow["reason"].(string)
	r.fact("follow-up of the declined PR #%d: status %v, reason %q", n, rrow["status"], reason)
	if rrow["status"] != "infra_error" || !strings.Contains(reason, fmt.Sprintf("PR #%d is closed", n)) {
		t.Errorf("the follow-up of declined PR #%d = %v, want infra_error %q", n, rrow, fmt.Sprintf("PR #%d is closed", n))
	}
	after, err := r.comments(ctx, n)
	if err != nil {
		t.Fatalf("listing PR #%d's comments: %s", n, r.errText(err))
	}
	if !slices.Equal(commentIDs(before), commentIDs(after)) {
		t.Errorf("PR #%d's comments changed during the refused follow-up: %v, then %v", n, commentIDs(before), commentIDs(after))
	}
}

// checkExecutionTimeout checks that the execution's task timeout is the
// run's --total-timeout plus backend.TaskTimeoutSlack, not the job's own.
func (r *liveRig) checkExecutionTimeout(ctx context.Context, execution string) {
	t := r.t
	t.Helper()
	svc, err := run.NewService(ctx, option.WithQuotaProject(liveProject))
	if err != nil {
		t.Fatal(err)
	}
	name := execution
	if !strings.HasPrefix(name, "projects/") {
		name = "projects/" + liveProject + "/locations/" + liveRegion + "/jobs/" + gcp.JobName(r.slug, liveWorkflow) + "/executions/" + name
	}
	x, err := svc.Projects.Locations.Jobs.Executions.Get(name).Context(ctx).Do()
	if err != nil {
		t.Errorf("reading execution %s: %v", name, err)
		return
	}
	want := liveTotalTimeout + backend.TaskTimeoutSlack
	var raw string
	if x.Template != nil {
		raw = x.Template.Timeout
	}
	got, perr := time.ParseDuration(raw)
	liveFact(t, "execution %s: task timeout %q with --total-timeout %s (want %s: the override plus the %s slack)", name, raw, liveTotalTimeout, want, backend.TaskTimeoutSlack)
	if perr != nil || got != want {
		t.Errorf("execution %s has task timeout %q, want %s (--total-timeout %s + %s)", name, raw, want, liveTotalTimeout, backend.TaskTimeoutSlack)
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
// declined, its branch and objects deleted; for a follow-up, the branch
// is its PR's, as branchOf allows), every fugaro-live-* object
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
