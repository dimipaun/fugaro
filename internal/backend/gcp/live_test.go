//go:build live

// Live checks of the Cloud Run backend against the real resources that the
// M4 bootstrap creates in the live-test project (docs/gcp-live-checklist.md). They
// never run in CI: they need the `live` build tag, Application Default
// Credentials and the real local config. Run them only after the bootstrap
// (docs/gcp-bootstrap.md) has been applied:
//
//	FUGARO_LIVE_PROJECT=<project> FUGARO_LIVE_REPO=<owner/name> \
//	FUGARO_LIVE_JOB_SA=<sandbox job SA email> \
//	  go test -tags live -p 1 -timeout 45m -run 'TestLive' -v ./internal/backend/gcp/
//
// Guardrails, enforced before any call:
//   - FUGARO_LIVE_PROJECT and FUGARO_LIVE_REPO name the target (liveTarget).
//     The local config ($FUGARO_CONFIG or ~/.config/fugaro/config.yaml) must
//     name the same project, onboard the same repository, and name a runs
//     bucket named fugaro-runs-*, no bucket_url override and no endpoint
//     override. Anything else is t.Fatal. The region is the local config's.
//   - Only the liveRepo sandbox is touched. Every object, secret, execution
//     and build a test creates is removed (or cancelled) in a t.Cleanup that
//     is registered before the side effect. Object names carry
//     fugaro-live-<stamp>, and internal/e2e's TestLiveGCPCleanup sweeps
//     whatever a -timeout abort left behind.
//   - Credentials come from ADC only (or, for the prefix-denial check, from
//     impersonating the job's service account with ADC). Nothing reads a
//     credential from argv, and nothing logged holds one.
//   - Spend is capped: jobs.run is overridden to a 120s task timeout, and the
//     job itself must have at most 1 CPU, 2Gi and a 30m task timeout (the
//     sandbox's own fugaro.yaml; jobs.run cannot override resources).
//
// Every observation is logged as a "FACT:" line, ready to paste into the M4
// PR and the runbook.
package gcp

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"gocloud.dev/blob"
	"gocloud.dev/gcerrors"
	cloudbuild "google.golang.org/api/cloudbuild/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/impersonate"
	"google.golang.org/api/iterator"
	logging "google.golang.org/api/logging/v2"
	"google.golang.org/api/option"
	run "google.golang.org/api/run/v2"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/cache"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/lock"
	"github.com/dimipaun/fugaro/internal/task"
)

// The sandbox's provider and workflow, and the names these tests create.
const (
	liveProvider = "bitbucket"
	liveWorkflow = "web"
	// livePrefix starts every name these tests create.
	livePrefix = "fugaro-live-"
	// liveBucketPrefix starts the only runs bucket name accepted.
	liveBucketPrefix = "fugaro-runs-"
)

// Spend caps the sandbox job must satisfy before anything is launched.
const (
	maxJobCPU      = 1
	maxJobMemGiB   = 2
	maxJobTimeout  = 30 * time.Minute
	probeTimeout   = "120s" // jobs.run's task timeout override for the probe execution
	probeBuildTime = "900s"
)

type liveEnv struct {
	lc     *localcfg.Config
	slug   string
	bucket string // the runs bucket's name
	stamp  string // unique per test: <yyyymmdd-hhmmss>-<4 hex>
	opts   Options
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

// fact logs one observation in the form the runbook pastes.
func fact(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Log("FACT: " + fmt.Sprintf(format, args...))
}

// openLive loads and checks the local config, and refuses to go on unless
// it names exactly the live project, region and a fugaro-runs-* bucket.
func openLive(t *testing.T) *liveEnv {
	t.Helper()
	liveTarget(t)
	// Keep every Google client quiet: at debug level they log request bodies.
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
	r, ok := lc.Repos[liveRepo]
	if !ok || (r.Provider != "" && r.Provider != liveProvider) {
		t.Fatalf("local config %s does not onboard %s (provider %s)", path, liveRepo, liveProvider)
	}
	slug, err := task.Slug(liveProvider, liveRepo)
	if err != nil {
		t.Fatal(err)
	}
	var rnd [2]byte
	if _, err := crand.Read(rnd[:]); err != nil {
		t.Fatal(err)
	}
	return &liveEnv{
		lc: lc, slug: slug, bucket: lc.RunsBucket,
		stamp: time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(rnd[:]),
		opts:  Options{Project: liveProject, Region: liveRegion, Warn: func(m string) { t.Log("backend warning: " + m) }},
	}
}

func (e *liveEnv) name() string { return livePrefix + e.stamp }

func (e *liveEnv) backend(t *testing.T) *Backend {
	t.Helper()
	b, err := New(context.Background(), e.opts)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// openBucket opens the runs bucket with the user's own ADC.
func (e *liveEnv) openBucket(t *testing.T) *blobx.Bucket {
	t.Helper()
	b, err := blobx.Open(context.Background(), "gs://"+e.bucket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

// deletablePrefix is what deleteTree may remove: a fugaro-live-* prefix, or
// one run's prefix in some slug.
var deletablePrefix = regexp.MustCompile(`^((runs|cache|locks)/[a-z0-9._-]+/(fugaro-live-[0-9a-z-]+|[0-9]{8}-[0-9]{6}-[0-9a-f]{4})/|(cache|locks)/fugaro-live-[0-9a-z-]+/)$`)

// deleteTree deletes every object under prefix, with the user's credentials.
func deleteTree(t *testing.T, b *blob.Bucket, prefix string) {
	t.Helper()
	if !deletablePrefix.MatchString(prefix) {
		t.Errorf("CLEANUP: refusing to delete %q: not a live-test prefix", prefix)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	it := b.List(&blob.ListOptions{Prefix: prefix})
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
		if err := b.Delete(ctx, obj.Key); err != nil && gcerrors.Code(err) != gcerrors.NotFound {
			t.Errorf("CLEANUP: deleting %s: %v", obj.Key, err)
			continue
		}
		n++
	}
	t.Logf("CLEANUP: deleted %d objects under gs://…/%s", n, prefix)
}

// httpStatus is the HTTP status of a Google API error, or 0.
func httpStatus(err error) int {
	var ge *googleapi.Error
	if errors.As(err, &ge) {
		return ge.Code
	}
	if errors.Is(err, storage.ErrObjectNotExist) {
		return http.StatusNotFound
	}
	// The storage client's XML read path reports some statuses only in text.
	if err != nil && (strings.Contains(err.Error(), "status code 403") || strings.Contains(err.Error(), "Error 403")) {
		return http.StatusForbidden
	}
	return 0
}

// TestLiveListAndLogs lists executions read-only, records the name form the
// API returns (project ID or number), and reads the newest execution's
// first log entries through the execution_name label filter.
func TestLiveListAndLogs(t *testing.T) {
	e := openLive(t)
	b := e.backend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	raw, err := b.run.Projects.Locations.Jobs.Executions.List(b.location() + "/jobs/-").PageSize(20).Context(ctx).Do()
	if err != nil {
		t.Fatalf("raw executions.list: %v", err)
	}
	for i, x := range raw.Executions {
		id, ok := backend.ParseExecution(x.Name)
		if !ok {
			t.Errorf("the API returned an execution name ParseExecution rejects: %q", x.Name)
			continue
		}
		if i < 3 {
			form := "project ID"
			if _, err := strconv.ParseUint(id.Project, 10, 64); err == nil {
				form = "project NUMBER"
			}
			fact(t, "executions.list returns names with the %s: %s", form, x.Name)
		}
	}
	// Whether the jobs/- listing is sorted by create time across every job,
	// which List's early stop at Since relies on (only an exhaustive
	// listing doesn't). A page with executions of two jobs is needed to tell.
	jobs, sorted := map[string]bool{}, true
	for i, x := range raw.Executions {
		if id, ok := backend.ParseExecution(x.Name); ok {
			jobs[id.Job] = true
		}
		if i > 0 {
			prev, perr := parseTime(raw.Executions[i-1].CreateTime)
			cur, cerr := parseTime(x.CreateTime)
			if perr == nil && cerr == nil && cur.After(prev) {
				sorted = false
			}
		}
	}
	switch {
	case len(jobs) < 2:
		fact(t, "jobs/- ordering across jobs: not shown (the page holds executions of %d job(s); it takes two)", len(jobs))
	case sorted:
		fact(t, "jobs/- ordering across jobs: newest first across %d jobs (global)", len(jobs))
	default:
		t.Errorf("FACT: jobs/- ordering across jobs: NOT newest first across %d jobs; List's early stop at Since cuts off executions", len(jobs))
	}
	got, err := b.List(ctx, backend.ListFilter{Since: time.Now().Add(-30 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range got {
		if !strings.HasPrefix(x.Name, "projects/"+liveProject+"/locations/"+liveRegion+"/jobs/") {
			t.Errorf("backend returned a non-canonical name %q", x.Name)
		}
	}
	fact(t, "List (30d, every fugaro-* job) = %d executions; raw page of every job = %d", len(got), len(raw.Executions))
	if len(got) == 0 {
		t.Log("no Fugaro execution in the last 30 days: the log check needs one (TestLiveExecutionProbe makes one)")
		return
	}
	newest := got[0]
	errStop := errors.New("stop")
	var entries []backend.LogEntry
	err = b.Logs(ctx, backend.LogQuery{Execution: newest.Name}, func(le backend.LogEntry) error {
		entries = append(entries, le)
		if len(entries) >= 10 {
			return errStop
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStop) {
		t.Fatal(err)
	}
	fact(t, `Logs of %s (state %s) with labels."run.googleapis.com/execution_name" = %d entries (first 10 read)`, newest.Name, newest.State, len(entries))
	if len(entries) == 0 {
		t.Fatalf("no log entries for %s: the execution_name label filter matches nothing on real Cloud Run", newest.Name)
	}
	fact(t, "first entry: severity=%s fields=%v", entries[0].Severity, keysOf(entries[0].Fields))
}

func keysOf(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// sandboxJob reads the sandbox job and refuses to go on unless its limits
// are within the spend caps. It logs the forms Cloud Run returns.
func sandboxJob(t *testing.T, ctx context.Context, e *liveEnv, b *Backend) *run.GoogleCloudRunV2Job {
	t.Helper()
	path := b.JobPath(e.slug, liveWorkflow)
	j, err := b.run.Projects.Locations.Jobs.Get(path).Context(ctx).Do()
	if err != nil {
		t.Fatalf("reading the sandbox job %s: %v", path, err)
	}
	if j.Template == nil || j.Template.Template == nil || len(j.Template.Template.Containers) == 0 || j.Template.Template.Containers[0].Resources == nil {
		t.Fatalf("job %s has no container resources", path)
	}
	tt := j.Template.Template
	limits := tt.Containers[0].Resources.Limits
	fact(t, "jobs.get limits: cpu=%q memory=%q; task timeout=%q maxRetries=%d taskCount=%d", limits["cpu"], limits["memory"], tt.Timeout, tt.MaxRetries, j.Template.TaskCount)
	cpu, cerr := parseCPU(limits["cpu"])
	mem, merr := backend.MemoryGiB(limits["memory"])
	if err := errors.Join(cerr, merr); err != nil {
		t.Fatalf("the job's limits don't parse the way the backend parses them: %v", err)
	}
	timeout, err := time.ParseDuration(tt.Timeout)
	if err != nil {
		t.Fatalf("job task timeout %q: %v", tt.Timeout, err)
	}
	if cpu > maxJobCPU || mem > maxJobMemGiB || timeout > maxJobTimeout {
		t.Fatalf("job %s is %g CPU / %gGiB / %s: above the live-test caps (%d CPU / %dGi / %s)", path, cpu, mem, timeout, maxJobCPU, maxJobMemGiB, maxJobTimeout)
	}
	return j
}

// TestLiveExecutionProbe starts one execution of the sandbox job that exits
// at once (its task.json is deliberately invalid, so the runner stops before
// cloning or starting an agent), with a 120s task timeout. It records what
// only a real run shows: the jobs.run operation's metadata type, the CPU and
// memory forms on the execution, what cancelling a finished execution
// returns, and the log label key.
func TestLiveExecutionProbe(t *testing.T) {
	e := openLive(t)
	b := e.backend(t)
	bucket := e.openBucket(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	j := sandboxJob(t, ctx, e, b)

	runID, err := task.NewRunID(time.Now(), crand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	prefix := "runs/" + e.slug + "/" + runID + "/"
	var execName string
	t.Cleanup(func() {
		if execName != "" {
			cctx, ccancel := context.WithTimeout(context.Background(), time.Minute)
			defer ccancel()
			if x, err := b.Execution(cctx, execName); err == nil && !x.State.Terminal() {
				t.Logf("CLEANUP: cancel %s: %v", execName, b.Cancel(cctx, execName))
			}
		}
		deleteTree(t, bucket.Bucket, prefix)
	})
	// Version 999 makes task.Parse fail: the runner reads it and exits. The
	// batch lets TestLiveGCPCleanup find the prefix after an abort.
	probe := fmt.Sprintf(`{"version":999,"run_id":%q,"repo":%q,"ref":"master","task":"live probe: exits at once","batch":"live-probe-%s"}`, runID, liveRepo, e.stamp)
	if err := bucket.WriteAll(ctx, prefix+"task.json", []byte(probe), &blob.WriterOptions{ContentType: "application/json"}); err != nil {
		t.Fatal(err)
	}

	req := &run.GoogleCloudRunV2RunJobRequest{Overrides: &run.GoogleCloudRunV2Overrides{
		TaskCount: 1,
		Timeout:   probeTimeout,
		ContainerOverrides: []*run.GoogleCloudRunV2ContainerOverride{{
			Env: []*run.GoogleCloudRunV2EnvVar{{Name: "FUGARO_RUN", Value: e.slug + "/" + runID}},
		}},
	}}
	op, err := b.run.Projects.Locations.Jobs.Run(j.Name, req).Context(ctx).Do()
	if err != nil {
		t.Fatalf("jobs.run: %v", err)
	}
	var meta struct {
		Type   string `json:"@type"`
		Name   string `json:"name"`
		LogURI string `json:"logUri"`
	}
	if err := json.Unmarshal(op.Metadata, &meta); err != nil {
		t.Fatalf("operation %s metadata: %v", op.Name, err)
	}
	if meta.Name != "" {
		if id, err := b.canonical(meta.Name); err == nil {
			execName = id.String()
		}
	}
	fact(t, "jobs.run operation %s: metadata @type=%q, name=%q, logUri set=%v, done=%v", op.Name, meta.Type, meta.Name, meta.LogURI != "", op.Done)
	if meta.Type != "type.googleapis.com/google.cloud.run.v2.Execution" {
		t.Errorf("jobs.run metadata is %q, not an Execution: Launch parses it as one", meta.Type)
	}
	if execName == "" {
		t.Fatalf("jobs.run metadata names no parseable execution: %q", meta.Name)
	}

	// Wait for it to end: seconds, and never past the 120s override.
	var x backend.Execution
	for deadline := time.Now().Add(5 * time.Minute); ; {
		if x, err = b.Execution(ctx, execName); err != nil {
			t.Fatal(err)
		}
		if x.State.Terminal() || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Second)
	}
	fact(t, "probe execution %s ended %s after %s billed", execName, x.State, x.Billed(time.Now()).Round(time.Second))
	rawx, err := b.run.Projects.Locations.Jobs.Executions.Get(execName).Context(ctx).Do()
	if err != nil {
		t.Fatal(err)
	}
	if rawx.Template != nil && len(rawx.Template.Containers) > 0 && rawx.Template.Containers[0].Resources != nil {
		l := rawx.Template.Containers[0].Resources.Limits
		fact(t, "executions.get limits: cpu=%q memory=%q -> backend CPU=%g MemoryGiB=%g", l["cpu"], l["memory"], x.CPU, x.MemoryGiB)
	} else {
		fact(t, "executions.get has no container limits -> backend CPU=%g MemoryGiB=%g", x.CPU, x.MemoryGiB)
	}
	if x.CPU <= 0 || x.MemoryGiB <= 0 {
		t.Errorf("the backend could not read the execution's limits (CPU %g, memory %g): cost would be unknown", x.CPU, x.MemoryGiB)
	}
	if !x.State.Terminal() {
		t.Fatalf("probe execution still %s after 5 minutes", x.State)
	}

	// Cancelling a finished execution.
	cerr := b.Cancel(ctx, execName)
	var ge *googleapi.Error
	if errors.As(cerr, &ge) {
		fact(t, "cancel of a finished execution -> HTTP %d status=%q message=%q", ge.Code, googleStatus(ge), ge.Message)
	} else {
		fact(t, "cancel of a finished execution -> %v", cerr)
	}
	if after, err := b.Execution(ctx, execName); err == nil {
		fact(t, "after that cancel the execution is %s", after.State)
	}

	// The log label key, read raw, then through the backend's filter.
	e.probeLogs(t, ctx, b, execName)
}

// googleStatus is the canonical status name of a Google API error
// (FAILED_PRECONDITION, …), when the error body carries one.
func googleStatus(ge *googleapi.Error) string {
	var body struct {
		Error struct {
			Status string `json:"status"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(ge.Body), &body) == nil {
		return body.Error.Status
	}
	return ""
}

func (e *liveEnv) probeLogs(t *testing.T, ctx context.Context, b *Backend, execName string) {
	t.Helper()
	id, _ := backend.ParseExecution(execName)
	var labels map[string]string
	var n int
	for deadline := time.Now().Add(3 * time.Minute); time.Now().Before(deadline); time.Sleep(15 * time.Second) {
		resp, err := b.logs.Entries.List(&logging.ListLogEntriesRequest{
			ResourceNames: []string{"projects/" + liveProject},
			Filter: `resource.type="cloud_run_job" AND resource.labels.job_name=` + strconv.Quote(id.Job) +
				` AND timestamp>=` + strconv.Quote(time.Now().Add(-15*time.Minute).UTC().Format(time.RFC3339)),
			OrderBy: "timestamp desc", PageSize: 20,
		}).Context(ctx).Do()
		if err != nil {
			t.Fatalf("raw entries.list: %v", err)
		}
		for _, le := range resp.Entries {
			for k, v := range le.Labels {
				if strings.HasSuffix(k, "execution_name") && v == id.Name {
					labels = le.Labels
				}
			}
		}
		if labels != nil {
			break
		}
	}
	var keys []string
	for k := range labels {
		keys = append(keys, k)
	}
	fact(t, "raw log entry labels of the probe execution: %v", keys)
	if _, ok := labels["run.googleapis.com/execution_name"]; !ok {
		t.Errorf(`no entry of %s carries labels."run.googleapis.com/execution_name" (keys seen: %v)`, execName, keys)
	}
	err := b.Logs(ctx, backend.LogQuery{Execution: execName}, func(backend.LogEntry) error { n++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	fact(t, "backend Logs of the probe execution = %d entries", n)
	if n == 0 {
		t.Errorf("backend Logs found no entries for %s", execName)
	}
}

// impersonatedStorage is a GCS client acting as sa, minted from the user's ADC.
func impersonatedStorage(t *testing.T, ctx context.Context, sa string) *storage.Client {
	t.Helper()
	ts, err := impersonate.CredentialsTokenSource(ctx, impersonate.CredentialsConfig{
		TargetPrincipal: sa,
		Scopes:          []string{"https://www.googleapis.com/auth/devstorage.read_write"},
		Lifetime:        15 * time.Minute,
	})
	if err != nil {
		t.Fatalf("impersonating %s: %v", sa, err)
	}
	if _, err := ts.Token(); err != nil {
		t.Fatalf("minting a token for %s (needs roles/iam.serviceAccountTokenCreator on it and iamcredentials.googleapis.com): %v", sa, err)
	}
	c, err := storage.NewClient(ctx, option.WithTokenSource(ts), option.WithLogger(discardLogger))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestLiveJobSADeniedOutsideItsPrefixes acts as the sandbox job's
// service account and checks that its conditional objectUser binding allows
// its own runs/, cache/ and locks/ prefixes and nothing else: not a slug
// that merely shares the prefix, not another slug, and not a listing.
func TestLiveJobSADeniedOutsideItsPrefixes(t *testing.T) {
	e := openLive(t)
	sa := os.Getenv("FUGARO_LIVE_JOB_SA")
	if sa == "" {
		t.Skip("FUGARO_LIVE_JOB_SA is not set (the sandbox job's service account; needs roles/iam.serviceAccountTokenCreator)")
	}
	want := ServiceAccountID(e.slug, liveWorkflow) + "@" + liveProject + ".iam.gserviceaccount.com"
	if sa != want {
		t.Fatalf("FUGARO_LIVE_JOB_SA=%s is not the sandbox job's service account %s", sa, want)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	user := e.openBucket(t)
	name := e.name()
	own := []string{"runs/" + e.slug + "/" + name + "/", "cache/" + e.slug + "/" + name + "/", "locks/" + e.slug + "/" + name + "/"}
	foreign := []string{"runs/" + e.slug + "-x/" + name + "/", "runs/other/" + name + "/"}
	t.Cleanup(func() {
		for _, p := range append(append([]string(nil), own...), foreign...) {
			deleteTree(t, user.Bucket, p)
		}
	})
	// An object outside the prefixes for the SA to try to read, written with
	// the user's own credentials.
	outsideTask := foreign[1] + "task.json"
	if err := user.WriteAll(ctx, outsideTask, []byte(`{"probe":true}`), nil); err != nil {
		t.Fatal(err)
	}

	sc := impersonatedStorage(t, ctx, sa)
	bkt := sc.Bucket(e.bucket)
	write := func(key string) error {
		w := bkt.Object(key).NewWriter(ctx)
		w.ContentType = "text/plain"
		if _, err := w.Write([]byte("probe " + name + "\n")); err != nil {
			_ = w.Close()
			return err
		}
		return w.Close()
	}
	for _, p := range own {
		key := p + "probe"
		err := write(key)
		fact(t, "job SA write %s -> %s", key, errOrOK(err))
		if err != nil {
			t.Errorf("the job SA may not write its own %s: %v", key, err)
			continue
		}
		derr := bkt.Object(key).Delete(ctx)
		fact(t, "job SA delete %s -> %s", key, errOrOK(derr))
		if derr != nil {
			t.Errorf("the job SA may not delete its own %s: %v", key, derr)
		}
	}
	for _, p := range foreign {
		key := p + "probe"
		err := write(key)
		fact(t, "job SA write %s -> %s", key, errOrOK(err))
		if httpStatus(err) != http.StatusForbidden {
			t.Errorf("the job SA writing %s got %v, want 403", key, err)
		}
	}
	_, rerr := bkt.Object(outsideTask).NewReader(ctx)
	fact(t, "job SA read %s -> %s", outsideTask, errOrOK(rerr))
	if httpStatus(rerr) != http.StatusForbidden {
		t.Errorf("the job SA reading %s got %v, want 403", outsideTask, rerr)
	}
	_, lerr := bkt.Objects(ctx, &storage.Query{Prefix: "runs/"}).Next()
	if errors.Is(lerr, iterator.Done) {
		lerr = nil
	}
	fact(t, "job SA list runs/ -> %s", errOrOK(lerr))
	if httpStatus(lerr) != http.StatusForbidden {
		t.Errorf("the job SA listing runs/ got %v, want 403", lerr)
	}
}

func errOrOK(err error) string {
	if err == nil {
		return "allowed"
	}
	if s := httpStatus(err); s != 0 {
		return fmt.Sprintf("HTTP %d (%v)", s, err)
	}
	return err.Error()
}

// TestLiveSecretsRoundTrip sets, lists and deletes one secret with a random
// value, which is never logged.
func TestLiveSecretsRoundTrip(t *testing.T) {
	e := openLive(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s, err := NewSecrets(ctx, e.opts)
	if err != nil {
		t.Fatal(err)
	}
	id := e.name()
	labels := map[string]string{"fugaro": "managed", "fugaro_live": "true"}
	deleted := false
	t.Cleanup(func() {
		if !deleted {
			t.Logf("CLEANUP: delete secret %s: %v", id, s.Delete(context.Background(), id))
		}
	})
	var raw [24]byte
	if _, err := crand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	value := []byte(hex.EncodeToString(raw[:]))
	ver, err := s.Set(ctx, id, value, labels)
	if err != nil {
		t.Fatal(strings.ReplaceAll(err.Error(), string(value), "[REDACTED]"))
	}
	fact(t, "secrets Set %s -> version %q", id, ver)
	list, err := s.List(ctx, labels)
	if err != nil {
		t.Fatal(err)
	}
	var found *SecretInfo
	for i := range list {
		if list[i].ID == id {
			found = &list[i]
		}
	}
	if found == nil {
		t.Fatalf("List by %v does not show %s", labels, id)
	}
	fact(t, "secrets List by labels shows %s: versions=%d latest=%q labels=%v", id, found.Versions, found.Latest, found.Labels)
	if found.Versions != 1 || found.Latest != ver {
		t.Errorf("listed %+v, want one version %s", *found, ver)
	}
	if err := s.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	deleted = true
}

// TestLiveLockOnGCS runs the lock's create-if-absent, busy, expired-takeover
// and release on the real bucket: the generation-precondition path the
// hermetic fake imitates.
func TestLiveLockOnGCS(t *testing.T) {
	e := openLive(t)
	b := e.openBucket(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	prefix := "locks/" + e.name() + "/"
	key := prefix + "0123456789abcdef"
	t.Cleanup(func() { deleteTree(t, b.Bucket, prefix) })
	now := time.Now()
	first, err := lock.Acquire(ctx, b, key, lock.Holder{RunID: "live-a", ExpiresAt: now.Add(time.Minute)}, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = lock.Acquire(ctx, b, key, lock.Holder{RunID: "live-b", ExpiresAt: now.Add(time.Minute)}, now)
	var busy *lock.BusyError
	fact(t, "second holder while live -> %v", err)
	if !errors.As(err, &busy) || busy.Holder.RunID != "live-a" {
		t.Fatalf("second holder: %v, want BusyError naming live-a", err)
	}
	later := now.Add(2 * time.Minute)
	second, err := lock.Acquire(ctx, b, key, lock.Holder{RunID: "live-b", ExpiresAt: later.Add(time.Minute)}, later)
	if err != nil {
		t.Fatalf("takeover of the expired lock: %v", err)
	}
	fact(t, "takeover of an expired lock with a generation-matched replace -> ok")
	if err := first.Release(ctx); err != nil {
		t.Fatalf("the old holder's release after a takeover: %v", err)
	}
	if ok, err := b.Exists(ctx, key); err != nil || !ok {
		t.Fatalf("the old holder's release removed the new holder's lock (exists=%v, %v)", ok, err)
	}
	fact(t, "the old holder's release after the takeover leaves the new lock in place")
	if err := second.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.Exists(ctx, key); err != nil || ok {
		t.Fatalf("after release the lock exists=%v (%v)", ok, err)
	}
	fact(t, "release deletes the lock with a generation-matched delete")
}

// TestLiveCacheOnGCS saves and restores a 1 MB archive on the real bucket
// and checks that customTime is set, which the cache lifecycle rule needs.
func TestLiveCacheOnGCS(t *testing.T) {
	e := openLive(t)
	b := e.openBucket(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	slug := e.name() // a fugaro-live-* slug of its own: never the sandbox's real cache
	t.Cleanup(func() { deleteTree(t, b.Bucket, "cache/"+slug+"/") })
	src := t.TempDir()
	data := make([]byte, 1<<20)
	if _, err := crand.Read(data); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src+"/blob.bin", data, 0o644); err != nil {
		t.Fatal(err)
	}
	st := &cache.Store{Bucket: b, Slug: slug, Workflow: liveWorkflow}
	saved, err := st.Save(ctx, "k1", []string{src})
	if err != nil || !saved {
		t.Fatalf("Save: saved=%v, %v", saved, err)
	}
	obj := cache.ObjectKey(slug, liveWorkflow, "k1")
	var oa *storage.ObjectAttrs
	attrs, err := b.Attributes(ctx, obj)
	if err != nil || !attrs.As(&oa) {
		t.Fatalf("attributes of %s: %v", obj, err)
	}
	fact(t, "cache Save %s: size=%d contentType=%q customTime=%s", obj, oa.Size, oa.ContentType, oa.CustomTime.Format(time.RFC3339))
	if oa.CustomTime.IsZero() {
		t.Errorf("%s has no customTime: the not-read-in-30-days rule would never delete it", obj)
	}
	dst := t.TempDir()
	hit, err := st.Restore(ctx, "k1", []string{dst})
	if err != nil || !hit {
		t.Fatalf("Restore: hit=%v, %v", hit, err)
	}
	got, err := os.ReadFile(dst + "/blob.bin")
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("restored content differs (%v)", err)
	}
	fact(t, "cache Restore of %s -> identical 1 MiB file", obj)
	if err := b.Delete(ctx, obj); err != nil {
		t.Fatal(err)
	}
}

// TestLiveCloudBuildSecretAndDigest submits one small Cloud Build build, as
// the configured build service account, that does what the derived-image
// build step does and nothing else: it pulls the base image, pins it by its
// RepoDigest, and builds FROM repo@sha256:… with the sandbox-probe workflow
// secret passed as `--secret id=SANDBOX_PROBE,env=SANDBOX_PROBE` and
// mounted with required=true. It pushes nothing. It records whether BuildKit
// in gcr.io/cloud-builders/docker honours env= secrets and how the digest
// FROM resolves (from the local image or the registry).
//
// It also probes the build isolation boundary (design §7.2): repository
// code runs in the Dockerfile's RUN steps, and must not reach the metadata
// server, which would hand it fugaro-build's token (every repository's
// build secrets, and write access to every image). metaProbe tries the
// token endpoint by name and by address from three places:
//   - "step": a Cloud Build step on the cloudbuild network, the control,
//     which must reach it (otherwise the probe proves nothing);
//   - "default": a RUN on BuildKit's default network, as the derived build
//     runs repository code;
//   - "host": a `RUN --network=host`, which a repository's Dockerfile may
//     ask for; BuildKit should refuse the entitlement, and if it doesn't,
//     the host network must still not serve a token.
//
// Both RUN probes must fail. If one reaches the token, the boundary does
// not hold: builds must move to per-repository build service accounts
// (M5), or deny the entitlement explicitly (a BuildKit builder created
// without --allow network.host), before repositories that don't trust each
// other share fugaro-build.
func TestLiveCloudBuildSecretAndDigest(t *testing.T) {
	e := openLive(t)
	if e.lc.BaseImage == "" || e.lc.Build.ServiceAccount == "" {
		t.Fatal("the local config needs base_image and build.service_account (the bootstrap's config step writes build.service_account; set base_image after its base step)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	bld, err := NewBuilder(ctx, e.opts, e.lc.BuildRegion())
	if err != nil {
		t.Fatal(err)
	}
	// The markers the RUN steps echo are split with "" so BuildKit's echo
	// of the command line itself never matches them.
	const script = `set -euo pipefail
base=$$(docker image inspect --format '{{index .RepoDigests 0}}' "$$FUGARO_BASE")
echo "LIVE base-digest $$base"
echo "LIVE docker $$(docker version --format '{{.Server.Version}}')"
ctx=$$(mktemp -d)
printf 'FROM %s\nRUN --mount=type=secret,id=SANDBOX_PROBE,required=true,mode=0444 test -s /run/secrets/SANDBOX_PROBE && echo LIVE secret-"mounted"\n' "$$base" > "$$ctx/Dockerfile"
docker build --progress plain --no-cache --secret id=SANDBOX_PROBE,env=SANDBOX_PROBE "$$ctx" 2>&1 | sed 's/^/LIVE build: /'
pctx=$$(mktemp -d)
printf '%s' "$$META_PROBE" > "$$pctx/probe.sh"
printf 'FROM %s\nCOPY probe.sh /probe.sh\nRUN sh /probe.sh default\n' "$$base" > "$$pctx/Dockerfile.default"
printf 'FROM %s\nCOPY probe.sh /probe.sh\nRUN --network=host sh /probe.sh host\n' "$$base" > "$$pctx/Dockerfile.host"
if ! docker build --progress plain --no-cache -f "$$pctx/Dockerfile.default" "$$pctx" 2>&1 | sed 's/^/LIVE probe: /'; then echo "LIVE meta""data default build-failed"; fi
if ! docker build --progress plain --no-cache -f "$$pctx/Dockerfile.host" "$$pctx" 2>&1 | sed 's/^/LIVE probe: /'; then echo "LIVE meta""data host build-refused"; fi
`
	req := &cloudbuild.Build{
		Steps: []*cloudbuild.BuildStep{
			// Pulls the base, and runs the metadata control on the cloudbuild network.
			{Id: "pull", Name: e.lc.BaseImage, Entrypoint: "sh", Args: []string{"-c", metaProbe, "sh", "step"}},
			{Id: "probe", Name: "gcr.io/cloud-builders/docker", Entrypoint: "bash",
				Env: []string{"DOCKER_BUILDKIT=1", "FUGARO_BASE=" + e.lc.BaseImage, "META_PROBE=" + metaProbe}, SecretEnv: []string{"SANDBOX_PROBE"},
				Args: []string{"-c", script}},
		},
		AvailableSecrets: &cloudbuild.Secrets{SecretManager: []*cloudbuild.SecretManagerSecret{{
			Env: "SANDBOX_PROBE", VersionName: "projects/" + liveProject + "/secrets/" + SecretID(e.slug, "sandbox-probe") + "/versions/latest",
		}}},
		ServiceAccount: "projects/" + liveProject + "/serviceAccounts/" + e.lc.Build.ServiceAccount,
		Options:        &cloudbuild.BuildOptions{Logging: "CLOUD_LOGGING_ONLY"},
		Timeout:        probeBuildTime,
		Tags:           []string{"fugaro-live"},
	}
	var buildID string
	t.Cleanup(func() {
		if buildID == "" {
			return
		}
		cctx, ccancel := context.WithTimeout(context.Background(), time.Minute)
		defer ccancel()
		name := bld.parent() + "/builds/" + buildID
		if bd, err := bld.svc.Projects.Locations.Builds.Get(name).Context(cctx).Do(); err == nil && (bd.Status == "QUEUED" || bd.Status == "WORKING" || bd.Status == "PENDING") {
			_, err := bld.svc.Projects.Locations.Builds.Cancel(name, &cloudbuild.CancelBuildRequest{Name: name, ProjectId: liveProject, Id: buildID}).Context(cctx).Do()
			t.Logf("CLEANUP: cancel build %s: %v", buildID, err)
		}
	})
	op, err := bld.svc.Projects.Locations.Builds.Create(bld.parent(), req).Context(ctx).Do()
	if err != nil {
		t.Fatalf("submitting the probe build: %v", err)
	}
	var md cloudbuild.BuildOperationMetadata
	if err := json.Unmarshal(op.Metadata, &md); err != nil || md.Build == nil || md.Build.Id == "" {
		t.Fatalf("the build operation %s names no build: %v", op.Name, err)
	}
	buildID = md.Build.Id
	res, werr := bld.Wait(ctx, buildID, 10*time.Second)
	fact(t, "probe build %s ended %s (log %s)", buildID, res.Status, res.LogURL)

	lines := buildLogLines(t, ctx, buildID)
	var digest string
	secret, metadata := false, false
	meta := map[string][]string{} // "step", "default", "host" -> each outcome
	for _, l := range lines {
		if m := metaLineRE.FindStringSubmatch(l); m != nil {
			meta[m[1]] = append(meta[m[1]], strings.TrimSpace(m[2]))
			fact(t, "metadata probe from %s: %s", m[1], strings.TrimSpace(m[2]))
			continue
		}
		switch {
		case strings.Contains(l, "LIVE base-digest "):
			digest = strings.TrimSpace(l[strings.Index(l, "LIVE base-digest ")+len("LIVE base-digest "):])
			fact(t, "render-step base %s pinned as %s", e.lc.BaseImage, digest)
		case strings.Contains(l, "LIVE docker "):
			fact(t, "%s", strings.TrimSpace(l[strings.Index(l, "LIVE docker "):]))
		case strings.Contains(l, "LIVE build:") && strings.Contains(l, "LIVE secret-mounted"):
			secret = true
		case strings.Contains(l, "LIVE build:") && (strings.Contains(l, "load metadata for") || strings.Contains(l, "FROM ") || strings.Contains(l, "resolve ")):
			if strings.Contains(l, "load metadata for") {
				metadata = true
			}
			fact(t, "FROM resolution: %s", strings.TrimSpace(l[strings.Index(l, "LIVE build:")+len("LIVE build:"):]))
		}
	}
	fact(t, "BuildKit env= secret mounted with required=true: %v", secret)
	fact(t, "FROM repo@sha256 contacted the registry for metadata: %v", metadata)
	if werr != nil {
		t.Fatalf("probe build failed: %v", werr)
	}
	if !secret {
		t.Error("the build succeeded but never printed LIVE secret-mounted")
	}
	if !regexp.MustCompile(`^[^@\s]+@sha256:[0-9a-f]{64}$`).MatchString(digest) {
		t.Errorf("base digest %q is not repo@sha256:<64 hex>", digest)
	}
	checkMetadataProbes(t, meta)
}

// metaProbe is the metadata probe, a POSIX sh script taking where it runs
// as $1. It prints one "LIVE metadata <where> blocked|REACHABLE <url>" line
// per endpoint, or "no-curl". Every $ is doubled for Cloud Build's
// substitution, which applies to args and env alike.
const metaProbe = `net=$$1
command -v curl >/dev/null 2>&1 || { echo "LIVE meta""data $$net no-curl"; exit 0; }
for u in http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token http://169.254.169.254/computeMetadata/v1/instance/service-accounts/default/token; do
  s=blocked
  if curl -sf --max-time 5 -o /dev/null -H 'Metadata-Flavor: Google' "$$u"; then s=REACHABLE; fi
  echo "LIVE meta""data $$net $$s $$u"
done
`

// metaLineRE matches a metadata probe's result line (not BuildKit's echo of
// the command, which spells the marker split).
var metaLineRE = regexp.MustCompile(`LIVE metadata (step|default|host) (.*)$`)

// checkMetadataProbes fails unless the control reached the token endpoint
// and neither RUN probe did (see TestLiveCloudBuildSecretAndDigest).
func checkMetadataProbes(t *testing.T, meta map[string][]string) {
	t.Helper()
	reachable := func(outcomes []string) bool {
		return slices.ContainsFunc(outcomes, func(o string) bool { return strings.HasPrefix(o, "REACHABLE ") })
	}
	if !reachable(meta["step"]) {
		t.Errorf("the control probe on the cloudbuild network did not reach the metadata server (%q), so the RUN probes prove nothing", meta["step"])
	}
	for _, where := range []string{"default", "host"} {
		got := meta[where]
		switch {
		case len(got) == 0 || slices.Contains(got, "no-curl") || slices.Contains(got, "build-failed"):
			t.Errorf("the %s-network RUN probe did not run (%q)", where, got)
		case reachable(got):
			t.Errorf("BOUNDARY BROKEN: a %s-network RUN step reached the metadata server (%q); repository code can take fugaro-build's token (design §7.2)", where, got)
		}
	}
	fact(t, "metadata server from a RUN step: default network %q, --network=host %q", meta["default"], meta["host"])
}

// buildLogLines reads a Cloud Build build's log lines from Cloud Logging
// (the builds log CLOUD_LOGGING_ONLY), waiting out ingestion.
func buildLogLines(t *testing.T, ctx context.Context, id string) []string {
	t.Helper()
	svc, err := logging.NewService(ctx, option.WithQuotaProject(liveProject), option.WithLogger(discardLogger))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for deadline := time.Now().Add(3 * time.Minute); time.Now().Before(deadline); time.Sleep(15 * time.Second) {
		lines = lines[:0]
		req := &logging.ListLogEntriesRequest{
			ResourceNames: []string{"projects/" + liveProject},
			Filter: `resource.type="build" AND resource.labels.build_id=` + strconv.Quote(id) +
				` AND timestamp>=` + strconv.Quote(time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)),
			OrderBy: "timestamp asc", PageSize: 1000,
		}
		err := svc.Entries.List(req).Pages(ctx, func(p *logging.ListLogEntriesResponse) error {
			for _, le := range p.Entries {
				lines = append(lines, le.TextPayload)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("reading build %s's log: %v", id, err)
		}
		if joined := strings.Join(lines, "\n"); strings.Contains(joined, "LIVE base-digest") && (strings.Contains(joined, "DONE") || strings.Contains(joined, "ERROR")) {
			break
		}
	}
	return lines
}
