package cli

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// reviewCLI runs the review command through a real root, so the persistent
// setup, the flags and the cloud open are the tree's own. The command family's
// one registration line lives in internal/cli/root.go, which this task's brief
// leaves alone (see the PR body), so the test adds it here rather than relying
// on a root that would not resolve `fugaro review` yet.
func reviewCLI(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := NewRootCmd()
	root.AddCommand(newReviewCmd())
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(NormalizeArgs(append([]string{"review"}, args...)))
	err := root.Execute()
	return out.String(), errOut.String(), err
}

// newReviewFixture is the installation a review is launched against: the
// project whose standard profile serves acme-personal and the default account,
// with both accounts' review-only variants deployed beside their coding jobs,
// the base image at the release the variant ships in (reviewJobSince, which
// every review launch is gated on), and the credentials service opted in.
//
// E7's second half — the service admitting a project-scoped caller — is not a
// fact of this tree at all, so the tests that have to reach the launch set it
// themselves (reviewJobAdmitted).
func newReviewFixture(t *testing.T) *projectJobFixture {
	t.Helper()
	f := newProjectJobServingFixture(t)
	setBaseImage(t, reviewJobSince)
	f.appendConfig(t, "credentials_service: { url: "+credentialsServiceFixtureURL+" }\n")
	for _, account := range []string{"acme-personal", ""} {
		job := credsvc.ReviewJobName("standard", "github", infra.ReviewVariantAccount(account))
		f.run.AddProjectJob(job, "standard", "github", infra.ReviewVariantAccount(account), "2", "4Gi")
		f.run.SetJobTimeout(job, projectJobTimeout+backend.TaskTimeoutSlack)
	}
	return f
}

// setBaseImage rewrites the fixture's base image tag, which is what a
// checkout: clone workflow's launches are judged on (checkCloneImageSince).
func setBaseImage(t *testing.T, tag string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old := "fugaro-base:" + sealSince
	if !strings.Contains(string(data), old) {
		t.Fatalf("the fixture's local config no longer names the base image %q:\n%s", old, data)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), old, "fugaro-base:"+tag, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// admitReviewJob is E7's second half, set by the tests that have to reach a
// launch and restored after each: on this tree the deployed service carries no
// review job accounts, so a review launch is refused (reviewJobAdmitted).
func admitReviewJob(t *testing.T) {
	t.Helper()
	reviewJobAdmitted = true
	t.Cleanup(func() { reviewJobAdmitted = false })
}

// storedRunRef is the "<slug>/<run-id>" of the run the fixture's bucket holds:
// the one run a review launch writes.
func storedRunRef(t *testing.T, f *cloudFixture) string {
	t.Helper()
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	slugs, err := runstore.ListSlugs(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	for _, slug := range slugs {
		ids, err := runstore.ListRunIDs(ctx, b, slug, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) > 0 {
			return slug + "/" + ids[0]
		}
	}
	t.Fatal("the launch wrote no run to the bucket")
	return ""
}

// storedTask is the task.json the launcher wrote for one run.
func reviewTaskSpec(t *testing.T, f *cloudFixture, slug, id string) *task.Spec {
	t.Helper()
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	spec, err := runstore.Open(b, slug, id).ReadTask(ctx)
	if err != nil {
		t.Fatalf("reading the stored task of %s/%s: %v", slug, id, err)
	}
	return spec
}

// TestReviewLaunchesOnTheNamedAccount is the command's core: `fugaro review
// --pr N --token NAME` writes one task that names the pull request and the
// account, and starts it on that account's REVIEW-ONLY job — the variant
// beside the shared project job, which is the whole point of the command (the
// reviewer left the coder's container, its mounts and its App key; CSR7), and
// never the shared coding job itself.
func TestReviewLaunchesOnTheNamedAccount(t *testing.T) {
	f := newReviewFixture(t)
	admitReviewJob(t)
	projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)

	if _, _, err := reviewCLI(t, "--pr", "7", "--token", "acme-personal"); err != nil {
		t.Fatalf("a review of PR #7 on acme-personal: %v", err)
	}
	calls := f.run.RunRequests()
	if len(calls) != 1 {
		t.Fatalf("the launch asked Cloud Run for %d executions, want 1", len(calls))
	}
	if want := credsvc.ReviewJobName("standard", "github", "acme-personal"); calls[0].Job != want {
		t.Errorf("the review ran the job %q, want the review-only variant %q", calls[0].Job, want)
	}
	if coding := gcp.ProjectJobName("standard", "github", "acme-personal"); calls[0].Job == coding {
		t.Errorf("the review ran the shared coding job %q: a review is never the coder's own job", coding)
	}
	if got := calls[0].Env[runner.EnvReviewJob]; got != "1" {
		t.Errorf("%s = %q on the review's execution, want \"1\": the marker is what tells the run it is the review job", runner.EnvReviewJob, got)
	}
	slug, id, err := parseRunRef(storedRunRef(t, f.cloudFixture))
	if err != nil {
		t.Fatal(err)
	}
	spec := reviewTaskSpec(t, f.cloudFixture, slug, id)
	switch {
	case spec.Mode != recipe.ModeReview:
		t.Errorf("the stored task's mode is %q, want %q", spec.Mode, recipe.ModeReview)
	case len(spec.ReviewPRs) != 1 || spec.ReviewPRs[0] != 7:
		t.Errorf("the stored task reviews %v, want [7]", spec.ReviewPRs)
	case spec.OAuthToken != "acme-personal":
		t.Errorf("the stored task's account is %q, want acme-personal", spec.OAuthToken)
	}
}

// wantReviewRefused runs the review command with args and wants exit 1 whose
// error carries every want, and no execution started by it: a refusal is a
// launch that did not happen, not one whose job failed.
func wantReviewRefused(t *testing.T, f *cloudFixture, args []string, wants ...string) error {
	t.Helper()
	before := len(f.run.Executions())
	out, errOut, err := reviewCLI(t, args...)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("review %v: out %q err %q, err = %v; want a refusal (exit 1)", args, out, errOut, err)
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("review %v refused with %q, want it to say %q", args, err.Error(), want)
		}
	}
	if after := len(f.run.Executions()); after != before {
		t.Fatalf("review %v refused and still started %d executions", args, after-before)
	}
	return err
}

// TestReviewRefusesAnAccountTheRepoMayNotUse is the F4 allow-list rule the
// other launches already carry (claudeAccountMembership), reused here: --token
// names an account the repository's claude_accounts does not list, so the
// review is refused before anything is written. A review is launched by hand
// more often than most runs, which is exactly why the rule is not relaxed for
// it: --token here is the operator naming the credential a job will run on.
func TestReviewRefusesAnAccountTheRepoMayNotUse(t *testing.T) {
	f := newReviewFixture(t)
	admitReviewJob(t)
	projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)

	err := wantReviewRefused(t, f.cloudFixture, []string{"--pr", "7", "--token", "acme-stranger"}, "is not one acme/app may use")
	if !strings.Contains(err.Error(), "acme-stranger") {
		t.Errorf("the refusal does not name the account asked for: %q", err.Error())
	}
}

// TestReviewRefusesAnExhaustedAccount is D882: an account the credentials
// service has marked holds no review at all — the launch refuses with the
// reset time the upstream reported, rather than starting a job whose reviewer
// is refused a minute later and whose run is then retried by hand.
func TestReviewRefusesAnExhaustedAccount(t *testing.T) {
	f := newReviewFixture(t)
	admitReviewJob(t)
	projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)
	until := time.Now().UTC().Truncate(time.Second).Add(3 * time.Hour)
	f.db.Set(credsMarkPath("acme-personal"), map[string]any{
		"state": "exhausted", "until": until.Format(time.RFC3339Nano)})

	wantReviewRefused(t, f.cloudFixture, []string{"--pr", "7", "--token", "acme-personal"},
		"the Claude account acme-personal is exhausted until "+until.Format("2006-01-02 15:04Z"),
		"no review is started on it")
}

// TestReviewRefusesWhileTheCodingRunIsGoing is spec §5's moving-head refusal:
// the review records the head it read, so a pull request whose own coding run
// is still going is refused rather than reviewed against a commit that can move
// under it.
func TestReviewRefusesWhileTheCodingRunIsGoing(t *testing.T) {
	f := newReviewFixture(t)
	admitReviewJob(t)
	projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)
	seedRoot(t, f.cloudFixture, rootID, time.Now().Add(-time.Hour))
	active := wallRunIDAt(0, "000100", "bbbb")
	exec := seedSpec(t, f.cloudFixture, followUpSpec(active, rootID, rootID, 7), true)
	f.run.SetState(exec, backend.StateRunning)
	writeRecord(t, f.cloudFixture, active, &runstore.Record{Version: 1, RunID: active, Repo: "acme/app",
		Execution: exec, Status: runstore.StatusRunning, Stage: "implement", Branch: "fugaro/" + rootID,
		PR: &runstore.PRRef{Number: 7}, StartedAt: time.Now(),
		FollowUp: &runstore.FollowUp{PR: 7, PreviousRun: rootID}})

	wantReviewRefused(t, f.cloudFixture, []string{"--pr", "7", "--token", "acme-personal"},
		active, "is still running", "fugaro runs ls --pr 7 --repo acme/app")
}

// TestReviewShowPrintsTheArtifactAndItsMarkdown is the second verb: what the
// review job wrote, read back as it is — the artifact's own bytes, and beside
// them the same review as markdown. --json prints the artifact alone, because
// its reader checks the sidecar signature over exactly those bytes.
func TestReviewShowPrintsTheArtifactAndItsMarkdown(t *testing.T) {
	f := newCloudFixture(t)
	id := wallRunIDAt(0, "120000", "5eed")
	artifact := `{"version":1,"pr":7,"verdict":"comment"}`
	markdown := "# Review of PR #7\n\nNothing blocking.\n"
	putBuildObject(t, f, "runs/"+appSlug+"/"+id+"/"+runstore.ArtifactPrefix+runner.ReviewArtifactName, []byte(artifact))
	putBuildObject(t, f, "runs/"+appSlug+"/"+id+"/"+runstore.ArtifactPrefix+runner.ReviewMarkdownName, []byte(markdown))
	ref := appSlug + "/" + id

	out, errOut, err := reviewCLI(t, "show", ref)
	if err != nil {
		t.Fatalf("review show %s: %v (%s)", ref, err, errOut)
	}
	if !strings.Contains(out, artifact) || !strings.Contains(out, markdown) {
		t.Errorf("review show printed %q, want the artifact and its markdown", out)
	}

	asJSON, _, err := reviewCLI(t, "show", ref, "--json")
	if err != nil {
		t.Fatalf("review show --json %s: %v", ref, err)
	}
	if asJSON != artifact+"\n" {
		t.Errorf("review show --json printed %q, want the artifact's own bytes alone (%q)", asJSON, artifact+"\n")
	}

	// A run that has no artifact — a review still going, or a run that is not
	// a review at all — says so instead of printing an empty review.
	empty := wallRunIDAt(0, "120100", "6eed")
	seedSpec(t, f, firstRunSpec(empty), false)
	wantReviewRefused(t, f, []string{"show", appSlug + "/" + empty}, "has no review artifact")
}

// TestReviewRefusesWhereTheCredentialsServiceIsOff is E7's first half: the
// review-only job mounts nothing, so its read-only installation token and its
// account's model calls both come from the installation's credentials service,
// and an installation without one has nothing for the job to fetch anything
// from. The refusal names both commands that fix it, in order: the first is a
// fugaro update, which is the whole upgrade.
func TestReviewRefusesWhereTheCredentialsServiceIsOff(t *testing.T) {
	f := newProjectJobServingFixture(t)
	setBaseImage(t, reviewJobSince)
	projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)

	wantReviewRefused(t, f.cloudFixture, []string{"--pr", "7", "--token", "acme-personal"},
		"credentials_service is not published in this project's config",
		"fugaro update --provision-credentials",
		"fugaro creds set github-app-key")
}

// TestReviewRefusesAJobTheCredentialsServiceWillNotAdmit is E7's second half,
// the one no operator command can supply: the deployed service admits a
// repository's own job, and admitting a review caller of a project-scoped job
// is C3.9, which is not built. The refusal has to say so — a sentence that
// named a fugaro update would send the operator looking for one that does not
// exist — and point at the verb that works today.
func TestReviewRefusesAJobTheCredentialsServiceWillNotAdmit(t *testing.T) {
	f := newReviewFixture(t)
	projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)

	wantReviewRefused(t, f.cloudFixture, []string{"--pr", "7", "--token", "acme-personal"},
		"does not admit the review job's own caller", "No fugaro update supplies this half",
		"fugaro run --pr N --recipe review-only")
}

// fixBriefVerify is the credentials service's verify route (C3.9f) as the
// launcher's own client meets it: one key made at run time, the frame the
// service builds for itself (v1\n<repository>\n<run>\n<digest>, spelled here
// because this stands in for the service's own code — the launcher's client
// never builds one), and every call it was sent, recorded so a test can read
// the digest and the account the launcher actually asked about.
type fixBriefVerify struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls []credsvc.VerifyRequest
}

// fixBriefKey is the stand-in service's key, made once for the package: the
// verifier seam below is package-wide, so a sidecar one test wrote has to be
// verifiable in the next test's read of it.
var fixBriefKey = func() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return key
}()

// fixBriefSign is the signature the stand-in service makes over one artifact's
// bytes: HMAC-SHA-256 under its key, over the frame the digest is the last
// field of.
func fixBriefSign(repository, run string, data []byte) string {
	sum := sha256.Sum256(data)
	mac := hmac.New(sha256.New, fixBriefKey)
	mac.Write([]byte("v1\n" + repository + "\n" + run + "\n" + hex.EncodeToString(sum[:])))
	return hex.EncodeToString(mac.Sum(nil))
}

// fixBriefVerifyStart is the package's one stand-in verify service, started on
// first use: the seam is package-wide, so one server has to answer every test
// that reads an artifact another test's fixture signed.
var fixBriefVerifyStart = sync.OnceValue(func() *fixBriefVerify {
	v := &fixBriefVerify{}
	v.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req credsvc.VerifyRequest
		if err := json.Unmarshal(body, &req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		v.mu.Lock()
		v.calls = append(v.calls, req)
		v.mu.Unlock()
		mac := hmac.New(sha256.New, fixBriefKey)
		mac.Write([]byte("v1\n" + req.Repository + "\n" + req.Run + "\n" + req.Digest))
		_ = json.NewEncoder(w).Encode(map[string]bool{
			"valid": hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(req.Signature)),
		})
	}))
	return v
})

func (v *fixBriefVerify) recorded() []credsvc.VerifyRequest {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]credsvc.VerifyRequest(nil), v.calls...)
}

// setFixBriefVerifier points the launcher's verifier at the stand-in service:
// the launcher's own client runs against it, so the call the wire carries is
// the real one and only the identity and the URL are a test's.
func setFixBriefVerifier(t *testing.T) *fixBriefVerify {
	t.Helper()
	svc := fixBriefVerifyStart()
	prev := reviewVerifierSeam
	reviewVerifierSeam = func(_ context.Context, account, run, repository string) (runner.SignatureVerifier, error) {
		return runner.ServiceVerifier{
			URL:     svc.srv.URL,
			IDToken: func(context.Context) (string, error) { return "verifier-id-token", nil },
			Account: account, Run: run, Repository: repository,
		}, nil
	}
	t.Cleanup(func() { reviewVerifierSeam = prev })
	return svc
}

// fixBriefLaunch launches a fix round carrying reviewRef's findings on runID
// and answers the launch's error: the one call every test below makes.
func fixBriefLaunchOn(t *testing.T, f *cloudFixture, runID, reviewRef string) error {
	t.Helper()
	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", runID, "--review-run", reviewRef)
	return err
}

// fixBriefLaunch is fixBriefLaunchOn on the fixture's own follow-up id.
func fixBriefLaunch(t *testing.T, f *cloudFixture, reviewRef string) error {
	t.Helper()
	return fixBriefLaunchOn(t, f, fuID, reviewRef)
}

// fixBriefHead is the head the stand-in forge reports for the fixture's pull
// request: the head every fixture artifact names (fixFirstArtifact's own), so
// the four checks the signature alone does not make hold unless a test breaks
// one of them on purpose.
const fixBriefHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// setFixBriefHead points the launcher's forge read at the fixture's head. The
// read is the pull request's CURRENT head, which no fixture bucket holds, so
// every test that reaches the launch answers it here rather than against a
// host.
func setFixBriefHead(t *testing.T) {
	t.Helper()
	prev := reviewPRHeadSeam
	reviewPRHeadSeam = func(context.Context, *cloudEnv, string, int) (string, error) { return fixBriefHead, nil }
	t.Cleanup(func() { reviewPRHeadSeam = prev })
}

// fixBriefSeed is the fixture every test below starts from: a root run on PR 7,
// so the pull request resolves, and a review run on it.
func fixBriefSeed(t *testing.T) (*cloudFixture, string) {
	t.Helper()
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-72*time.Hour))
	setFixBriefHead(t)
	return f, wallRunIDAt(0, "121500", "5eed")
}

// wantFixBriefRefused launches a fix round carrying reviewRef's findings and
// wants exit 1 whose error carries every want and names the review run, with no
// execution started: a refusal is a launch that did not happen.
func wantFixBriefRefused(t *testing.T, f *cloudFixture, reviewRef string, wants ...string) error {
	t.Helper()
	before := len(f.run.Executions())
	err := fixBriefLaunch(t, f, reviewRef)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("fix round on %s: err = %v, want a refusal (exit 1)", reviewRef, err)
	}
	if !strings.Contains(err.Error(), reviewRef) {
		t.Errorf("the refusal does not name the review run %s: %v", reviewRef, err)
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal of %s does not say %q: %v", reviewRef, want, err)
		}
	}
	if after := len(f.run.Executions()); after != before {
		t.Fatalf("the refused launch of %s started %d executions", reviewRef, after-before)
	}
	return err
}

// TestFixBriefRefusesAVerdictWithNoSignature is CSR10's red step: an artifact
// with no sidecar beside it is not a verdict, and a fix round is not launched
// on one. Today the artifact is decoded and its findings rendered with no
// signature read at all, so a review the service never signed becomes a fix
// round's brief.
func TestFixBriefRefusesAVerdictWithNoSignature(t *testing.T) {
	f, reviewID := fixBriefSeed(t)
	putReviewObject(t, f, reviewID, fixFirstArtifact(reviewID))
	putReviewTask(t, f, reviewID, "acme-personal")
	setFixBriefVerifier(t)

	wantFixBriefRefused(t, f, reviewID, "no signature", runner.ReviewSignName)
}

// TestFixBriefRefusesASignatureTheServiceDoesNotOwn: a sidecar nothing signed
// is refused by the service's own answer, and the brief is never built. The
// bytes are an artifact this launch is for in every other way, so the refusal
// is the signature's and not a reader that refuses everything.
func TestFixBriefRefusesASignatureTheServiceDoesNotOwn(t *testing.T) {
	f, reviewID := fixBriefSeed(t)
	data := putReviewObject(t, f, reviewID, fixFirstArtifact(reviewID))
	putReviewTask(t, f, reviewID, "acme-personal")
	setFixBriefVerifier(t)
	putBuildObject(t, f, "runs/"+appSlug+"/"+reviewID+"/"+runstore.ArtifactPrefix+runner.ReviewSignName,
		[]byte(strings.Repeat("c", 64)+"\n"))

	wantFixBriefRefused(t, f, reviewID, "signature")
	if len(data) == 0 {
		t.Fatal("the fixture wrote no artifact")
	}
}

// TestFixBriefRefusesWhenTheServiceIsUnreachable is the fail-closed direction
// that matters most: a verify call that cannot be made is a refusal and never
// "nothing to check", because a brief built from an artifact nobody verified is
// exactly the launch this task exists to stop.
func TestFixBriefRefusesWhenTheServiceIsUnreachable(t *testing.T) {
	f, reviewID := fixBriefSeed(t)
	putReviewArtifact(t, f, reviewID, fixFirstArtifact(reviewID))

	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	prev := reviewVerifierSeam
	reviewVerifierSeam = func(_ context.Context, account, run, repository string) (runner.SignatureVerifier, error) {
		return runner.ServiceVerifier{
			URL:     deadURL,
			IDToken: func(context.Context) (string, error) { return "verifier-id-token", nil },
			Account: account, Run: run, Repository: repository,
		}, nil
	}
	t.Cleanup(func() { reviewVerifierSeam = prev })

	wantFixBriefRefused(t, f, reviewID, "could not be reached")
}

// TestFixBriefVerifiesTheBytesItReads: the digest the service is asked about is
// the SHA-256 of the artifact's STORED bytes, byte for byte. The fixture stores
// the same review under an indented encoding, so an artifact this launcher
// decoded and re-encoded before hashing would ask about bytes nobody signed.
func TestFixBriefVerifiesTheBytesItReads(t *testing.T) {
	f, reviewID := fixBriefSeed(t)
	pretty, err := json.MarshalIndent(storedReviewArtifact(fixFirstArtifact(reviewID)), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	putBuildObject(t, f, "runs/"+appSlug+"/"+reviewID+"/"+runstore.ArtifactPrefix+runner.ReviewArtifactName, pretty)
	putReviewTask(t, f, reviewID, "acme-personal")
	svc := setFixBriefVerifier(t)
	putReviewSignature(t, f, reviewID, appSlug, reviewID, pretty)

	before := len(svc.recorded())
	if err := fixBriefLaunch(t, f, reviewID); err != nil {
		t.Fatalf("a fix round on a signed review: %v", err)
	}
	calls := svc.recorded()[before:]
	if len(calls) != 1 {
		t.Fatalf("the verify route was called %d times, want 1", len(calls))
	}
	sum := sha256.Sum256(pretty)
	if want := hex.EncodeToString(sum[:]); calls[0].Digest != want {
		t.Errorf("the digest the service was asked about is %s, want the SHA-256 of the stored bytes %s", calls[0].Digest, want)
	}
	compact, err := json.Marshal(storedReviewArtifact(fixFirstArtifact(reviewID)))
	if err != nil {
		t.Fatal(err)
	}
	sum = sha256.Sum256(compact)
	if hex.EncodeToString(sum[:]) == calls[0].Digest {
		t.Fatal("the fixture's two encodings are the same bytes, so this test cannot tell a re-encode from the stored object")
	}
}

// TestFixBriefVerifiesADefaultAccountReview is the account's second direction:
// the field a task spells the unsuffixed default with is no field at all, so a
// review run whose task carries no agent.oauth_token has to be verified with
// the call naming config.DefaultClaudeAccount and never the empty string, which
// the service refuses as a bad CALL. A review task the launcher cannot read
// refuses the brief rather than guessing an account.
func TestFixBriefVerifiesADefaultAccountReview(t *testing.T) {
	t.Run("the unsuffixed default", func(t *testing.T) {
		f, reviewID := fixBriefSeed(t)
		data := putReviewObject(t, f, reviewID, fixFirstArtifact(reviewID))
		putReviewTask(t, f, reviewID, "")
		svc := setFixBriefVerifier(t)
		putReviewSignature(t, f, reviewID, appSlug, reviewID, data)

		before := len(svc.recorded())
		if err := fixBriefLaunch(t, f, reviewID); err != nil {
			t.Fatalf("a fix round on a default-account review: %v", err)
		}
		calls := svc.recorded()[before:]
		if len(calls) != 1 {
			t.Fatalf("the verify route was called %d times, want 1", len(calls))
		}
		if got := calls[0].Account; got != config.DefaultClaudeAccount {
			t.Errorf("the verify call names the account %q, want %q (an empty account is refused as a bad call)", got, config.DefaultClaudeAccount)
		}
	})

	t.Run("a named account", func(t *testing.T) {
		f, reviewID := fixBriefSeed(t)
		data := putReviewObject(t, f, reviewID, fixFirstArtifact(reviewID))
		putReviewTask(t, f, reviewID, "acme-personal")
		svc := setFixBriefVerifier(t)
		putReviewSignature(t, f, reviewID, appSlug, reviewID, data)

		before := len(svc.recorded())
		if err := fixBriefLaunch(t, f, reviewID); err != nil {
			t.Fatalf("a fix round on a named-account review: %v", err)
		}
		calls := svc.recorded()[before:]
		if len(calls) != 1 || calls[0].Account != "acme-personal" {
			t.Errorf("the named review's verify call names %v, want acme-personal", calls)
		}
	})

	// A review run whose task cannot be read: the account has one source, and a
	// launcher that guessed one would key the call on something nobody wrote.
	t.Run("a task the launcher cannot read", func(t *testing.T) {
		f, reviewID := fixBriefSeed(t)
		data := putReviewObject(t, f, reviewID, fixFirstArtifact(reviewID))
		setFixBriefVerifier(t)
		putReviewSignature(t, f, reviewID, appSlug, reviewID, data)

		wantFixBriefRefused(t, f, reviewID, "task cannot be read")
	})
}

// TestFixBriefNamesNoProseFromTheArtifact: a refusal quotes the review run and
// the artifact's identity fields and NEVER a field of the review's own prose
// (spec §7). The fixture's prose is distinctive so a leak is unmistakable.
func TestFixBriefNamesNoProseFromTheArtifact(t *testing.T) {
	f, reviewID := fixBriefSeed(t)
	art := fixFirstArtifact(reviewID)
	art.Overview = "the overview nobody should read"
	art.Security = "the security note nobody should read"
	art.Issues[0].Summary = "the summary nobody should read"
	art.Issues[0].Fix = "the fix nobody should read"
	art.Issues[0].Evidence = "the evidence nobody should read"

	// The unsigned case: refused before any verdict is read at all.
	putReviewObject(t, f, reviewID, art)
	putReviewTask(t, f, reviewID, "acme-personal")
	setFixBriefVerifier(t)
	err := wantFixBriefRefused(t, f, reviewID, "no signature")
	for _, prose := range []string{art.Overview, art.Security, art.Issues[0].Summary, art.Issues[0].Fix, art.Issues[0].Evidence} {
		if strings.Contains(err.Error(), prose) {
			t.Errorf("the refusal quotes the review's prose %q: %v", prose, err)
		}
	}

	// And the verdict case: a verified artifact whose verdict asks for no fix
	// quotes the verdict's own word and none of the review's text.
	rejectID := wallRunIDAt(0, "121900", "9eed")
	art = fixFirstArtifact(rejectID)
	art.Verdict = "reject"
	art.Overview = "the overview nobody should read"
	art.Issues[0].Summary = "the summary nobody should read"
	putReviewArtifact(t, f, rejectID, art)
	err = wantFixBriefRefused(t, f, rejectID, "reject")
	for _, prose := range []string{art.Overview, art.Issues[0].Summary} {
		if strings.Contains(err.Error(), prose) {
			t.Errorf("the refusal quotes the review's prose %q: %v", prose, err)
		}
	}
}

// TestFixBriefRefusesAVerdictThatAsksForNoFix is Open item 2's answer of
// 2026-11-05: a verdict the launcher reads is a verdict the launcher acts on,
// and only `fix first` asks for a fix round. A `ship` and a `reject` refuse in
// one sentence that names the verdict word and points at the verb that prints
// the findings anyway.
func TestFixBriefRefusesAVerdictThatAsksForNoFix(t *testing.T) {
	for _, verdict := range []string{"ship", "reject"} {
		t.Run(verdict, func(t *testing.T) {
			f, reviewID := fixBriefSeed(t)
			art := fixFirstArtifact(reviewID)
			art.Verdict = verdict
			putReviewArtifact(t, f, reviewID, art)

			wantFixBriefRefused(t, f, reviewID, verdict, "fugaro review show "+appSlug+"/"+reviewID)
		})
	}
}

// TestFixBriefBuildsTheBriefForFixFirst is the second direction of the red
// step: `fix first` — the one verdict that asks for the work this launch does —
// reaches runner.FixFindings with the artifact's issues, so the refusal above
// is a verdict policy and not a reader that refuses everything.
func TestFixBriefBuildsTheBriefForFixFirst(t *testing.T) {
	f, reviewID := fixBriefSeed(t)
	putReviewArtifact(t, f, reviewID, fixFirstArtifact(reviewID))

	if err := fixBriefLaunch(t, f, reviewID); err != nil {
		t.Fatalf("a fix round on a fix first review: %v", err)
	}
	spec := readSpec(t, f, fuID)
	for _, want := range []string{reviewID, "the lock is released before the write"} {
		if !strings.Contains(spec.Task, want) {
			t.Errorf("the fix round's brief does not carry %q:\n%s", want, spec.Task)
		}
	}
}
