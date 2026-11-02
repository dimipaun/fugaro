package runner

// These are CSR3's own tests: the independent review's verdict artifact, its
// signature, and the reader a coordinator verifies it with
// (docs/cloud-senior-review: D884, D886, D890, D892(a), O3, O4, F23).
//
// They are in-package because the artifact's writer is a run's own code path
// (r.writeReviewArtifact) and the run's identity -- the token the credentials
// service verifies a sign call with -- cannot be made up (gitRunIdentity,
// gateway_service_internal_test.go's own reason for being in this package).
// The sign endpoint is reached through the ReviewSigner seam, so a test
// satisfies it with a fake service holding a key made at run time under
// t.TempDir(); the one test that is about the client itself
// (TestReviewArtifactIsSignedByTheCredentialsService) runs the real
// ServiceSigner against a stand-in service and the real metadata fake.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

const (
	// reviewJobPR is the pull request the review job reviews: a number, and
	// the record's own link to it.
	reviewJobPR = 1470
	// reviewJobHead is the head the review read, 40 lowercase hex. It is a
	// commit these fixtures name, never a commit that exists.
	reviewJobHead = "3f1c0a9d2b7e4c5f8a6d1e0b9c8a7f6e5d4c3b2a"
	// reviewJobOldHead is another commit: the head an earlier review of the
	// SAME pull request read. A verdict about it is a replay for the current
	// head even though its signature is valid (spec §6).
	reviewJobOldHead = "b7e2d4f6a8c0e1b3d5f7091a2c4e6b8d0f1a3c5e"
)

// reviewSignerFunc is a ReviewSigner made of a function: what a test satisfies
// the seam with.
type reviewSignerFunc func(ctx context.Context, digest string) (string, error)

func (f reviewSignerFunc) Sign(ctx context.Context, digest string) (string, error) {
	return f(ctx, digest)
}

// setReviewSigner puts a fake service in front of the run's own signer, for
// the tests that are not about the client (the seam reviewSigner reads first).
func setReviewSigner(t *testing.T, f func(*run) (ReviewSigner, error)) {
	t.Helper()
	prev := reviewSignerSeam
	reviewSignerSeam = f
	t.Cleanup(func() { reviewSignerSeam = prev })
}

// clearReviewSigner hands the run its own ServiceSigner back, which is what
// the test about the client runs against a stand-in service.
func clearReviewSigner(t *testing.T) {
	t.Helper()
	prev := reviewSignerSeam
	reviewSignerSeam = nil
	t.Cleanup(func() { reviewSignerSeam = prev })
}

// reviewSignCall is one call the stand-in service answered, as the contract's
// own fields: the path, the job's identity, the run's identity, the job the
// caller claims, and the body.
type reviewSignCall struct {
	path, auth, run, job, body string
}

// reviewService is the credentials service's review routes as a test meets
// them: one key, made at run time and kept in a t.TempDir() file, a sign route
// that frames with the two identities IT verified (never with anything the
// call sent), and a verifier that re-frames from its own copy of those two.
//
// The frame is spelled here the way C3.9e spells it -- v1\n<slug>\n<run>\n
// <digest> -- because this file stands in for the service's own code; the real
// signer never builds it, and neither does the real verifier (D1073, D892(a)).
type reviewService struct {
	t        *testing.T
	keyPath  string
	key      []byte
	slug     string
	run      string
	srv      *httptest.Server
	mu       sync.Mutex
	recorded []reviewSignCall
}

func newReviewService(t *testing.T) *reviewService {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "review-signing.key")
	if err := os.WriteFile(keyPath, []byte(hex.EncodeToString(key)), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &reviewService{t: t, keyPath: keyPath, key: key, slug: svcSlug, run: svcRunID}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.recorded = append(s.recorded, reviewSignCall{
			path: r.URL.Path, auth: r.Header.Get("Authorization"),
			run: r.Header.Get(credsvc.HeaderRunIdentity), job: r.Header.Get(credsvc.HeaderRunJob),
			body: string(body),
		})
		s.mu.Unlock()
		var req struct {
			Digest string `json:"digest"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("the stand-in service was sent a body that is not a sign call: %s", body)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]string{
			"alg": "HMAC-SHA-256", "signature": s.sign(req.Digest),
		}); err != nil {
			t.Errorf("the stand-in service could not answer: %v", err)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

// sign is the signature the service makes for one digest: HMAC-SHA-256 under
// the key it alone holds, over the frame built from the identities it verified.
// The key is read back out of the file rather than used from memory, so the
// two ends of these fixtures really are the file's key and not a value one of
// them happens to hold.
func (s *reviewService) sign(digest string) string {
	data, err := os.ReadFile(s.keyPath)
	if err != nil {
		s.t.Fatal(err)
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		s.t.Fatal(err)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("v1\n" + s.slug + "\n" + s.run + "\n" + digest))
	return hex.EncodeToString(mac.Sum(nil))
}

// signer is what the seam is set to: the service's own answer, without an
// HTTP round trip, for the tests that are not about the client.
func (s *reviewService) signer(*run) (ReviewSigner, error) {
	return reviewSignerFunc(func(_ context.Context, digest string) (string, error) {
		return s.sign(digest), nil
	}), nil
}

// verifier is the service's verify route standing in for C3.9f: it recomputes
// its frame from its OWN copy of the two claims and answers whether signature
// is the MAC of it under the key it holds.
func (s *reviewService) verifier() SignatureVerifier {
	return reviewVerifier{key: s.key, slug: s.slug, run: s.run}
}

func (s *reviewService) calls() []reviewSignCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]reviewSignCall(nil), s.recorded...)
}

// reviewVerifier is C3.9f as a test stands in for it.
type reviewVerifier struct {
	key       []byte
	slug, run string
}

func (v reviewVerifier) Verify(_ context.Context, digest, signature string) error {
	mac := hmac.New(sha256.New, v.key)
	mac.Write([]byte("v1\n" + v.slug + "\n" + v.run + "\n" + digest))
	if !hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(signature)) {
		return errors.New("the signature is not this service's over that digest")
	}
	return nil
}

// reviewJobRun is the review job's run as its artifact writer meets it: one
// pull request resolved at one head (checkoutReviewHeads' own answer, held
// here because the writer reads no provider), a store to upload into, a state
// directory to stage in, the run's own clock, and the environment of a job the
// launcher marked as the review job (EnvReviewJob). Its signer is a fake
// service's, which the returned reviewService is.
func reviewJobRun(t *testing.T, edit func(*run)) (*run, *reviewService) {
	t.Helper()
	bucket := memblob.OpenBucket(nil)
	t.Cleanup(func() { _ = bucket.Close() })
	svc := newReviewService(t)
	setReviewSigner(t, svc.signer)
	now := time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC)
	r := svcRun(t, config.AuthCascade, func(r *run) {
		r.d.Now = func() time.Time { return now }
		r.d.Store = runstore.Open(bucket, svcSlug, svcRunID)
		r.d.StateDir = t.TempDir()
		r.d.WorkDir = t.TempDir()
		r.d.Env = []string{EnvReviewJob + "=1"}
		r.rec = &runstore.Record{Version: 1, RunID: svcRunID, Repo: "acme/app", Stage: "review"}
		// The review stage's own level, which is what the artifact's reviewer
		// block records: the model written into the level's own config block
		// with the run's own setter (stageModel reads it there) and the
		// effort in the resolved set (reviewEffort reads it there), so the
		// artifact carries the two answers the stage itself runs on rather
		// than a second pair written beside them.
		r.setLevelModel(policy.LevelL2, "claude-opus-5-5")
		r.levels = config.Levels{L2: config.ResolvedLevel{On: true, Model: "claude-opus-5-5", Effort: "high"}}
		r.review = true
		r.reviewHeads = []reviewHead{{Number: reviewJobPR, SHA: reviewJobHead, Base: "main"}}
		r.spec = &task.Spec{Version: 1, RunID: svcRunID, Repo: "acme/app", Ref: "main", Workflow: "app",
			Task: "review the pull request", OAuthToken: "team-a", ReviewPRs: []int{reviewJobPR}}
		if edit != nil {
			edit(r)
		}
	})
	return r, svc
}

// reviewAnswerOf is a reviewer's whole answer: the artifact's model-written
// fields, and none of the provenance the writer fills.
func reviewAnswerOf(verdict, overview string, issues ...ReviewIssue) reviewAnswer {
	return reviewAnswer{
		Verdict: verdict, Overview: overview,
		Strengths:    []string{"the change is small and the tests name what they pin"},
		Issues:       issues,
		Security:     "no new credential is read and none is logged",
		Performance:  "one extra pass over the diff and no extra index",
		TestCoverage: "the refusal arms are covered and the timeout arm is not",
	}
}

// reviewResult is the structured output a reviewer answering the artifact's
// schema produces.
func reviewResult(t *testing.T, a reviewAnswer) agent.Result {
	t.Helper()
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	return agent.Result{Structured: data}
}

// reviewArtifactFixture is a whole, valid artifact: what a review job writes
// and what a reader reads back. The verify tests build from it, because
// verification is about the bytes rather than about how they were written.
func reviewArtifactFixture(t *testing.T) ReviewArtifact {
	t.Helper()
	return ReviewArtifact{
		SchemaVersion: ReviewArtifactVersion,
		ReviewRun:     svcRunID,
		CreatedAt:     time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC),
		Repository:    svcSlug,
		PR:            reviewJobPR,
		HeadSHA:       reviewJobHead,
		Base:          "main",
		Reviewer:      ReviewReviewer{Account: "team-a", Model: "claude-opus-5-5", Effort: "high"},
		Verdict:       "fix first",
		Overview:      "the review's own words about the whole change",
		Strengths:     []string{"the refusal arms are tested"},
		Issues: []ReviewIssue{{
			Severity: "blocker", Class: "security", File: "internal/creds/select.go", Line: 155,
			Summary: "the read is made before the account is checked", Fix: "check the account first",
			Evidence: "select.go:155 reads before the switch", Likelihood: 5, Impact: 4, FixCost: 2,
		}},
		Security:     "the new read is behind the account check",
		Performance:  "no extra index",
		TestCoverage: "the timeout arm is not covered",
		MutationChecks: []ReviewMutationCheck{{
			File: "internal/creds/select.go", Test: "TestSelectRefusesAnotherAccount",
			Applied: "swapped the two statements", Observed: "fail", Reverted: true,
		}},
	}
}

// reviewWant is what the coordinator knows independently of the artifact: the
// four things its four checks compare against (D892(a), spec §6).
func reviewWant() ReviewExpectation {
	return ReviewExpectation{ReviewRun: svcRunID, Repository: svcSlug, PR: reviewJobPR, HeadSHA: reviewJobHead}
}

// reviewObject reads one uploaded object back out of the run's own store, so a
// test asserts on the bytes that were signed rather than on the record's
// description of them.
func reviewObject(t *testing.T, r *run, name string) []byte {
	t.Helper()
	data, err := r.d.Store.ReadFile(context.Background(), runstore.ArtifactPrefix+name)
	if err != nil {
		t.Fatalf("reading %s back out of the run's own store: %v", name, err)
	}
	return data
}

// signedReviewArtifact is an artifact's exact bytes and the signature the
// service makes over their digest, without a run: the pair the verify tests
// hand to the verifier.
func signedReviewArtifact(t *testing.T, svc *reviewService, a ReviewArtifact) (data []byte, sig string) {
	t.Helper()
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	return data, svc.sign(sha256Hex(string(data)))
}

// reviewSignatureLine is the sidecar's whole shape: one line of lowercase hex
// and nothing else.
var reviewSignatureLine = regexp.MustCompile(`^[0-9a-f]{64}\n$`)

func TestReviewArtifactRoundTrips(t *testing.T) {
	r, _ := reviewJobRun(t, nil)
	if !r.reviewJob() {
		t.Fatalf("a run whose environment carries %s is not read as the review job", EnvReviewJob)
	}
	issue := ReviewIssue{
		Severity: "blocker", Class: "security",
		File: "internal/creds/select.go", Line: 155,
		Summary:  "the new read is made before the account is checked",
		Fix:      "check the account first, then read the key",
		Evidence: "select.go:155-162 reads before the switch",
		// The three anchored axes the in-job mode table reads (D884).
		Likelihood: 5, Impact: 4, FixCost: 2,
	}
	res := reviewResult(t, reviewAnswerOf("fix first", "the direction is right and one read is in the wrong order", issue))
	if err := r.writeReviewArtifact(context.Background(), res); err != nil {
		t.Fatalf("the review job could not write its artifact: %v", err)
	}

	data := reviewObject(t, r, ReviewArtifactName)
	art, err := DecodeReviewArtifact(data)
	if err != nil {
		t.Fatalf("the artifact this run wrote does not read back: %v", err)
	}
	if art.SchemaVersion != ReviewArtifactVersion {
		t.Errorf("schema_version = %d, want %d", art.SchemaVersion, ReviewArtifactVersion)
	}
	if art.ReviewRun != svcRunID {
		t.Errorf("review_run = %q, want the run's own id %q", art.ReviewRun, svcRunID)
	}
	if art.Repository != svcSlug {
		t.Errorf("repository = %q, want the run's own slug %q", art.Repository, svcSlug)
	}
	if art.PR != reviewJobPR || art.HeadSHA != reviewJobHead || art.Base != "main" {
		t.Errorf("the artifact names #%d at %s against %s, want #%d at %s against main",
			art.PR, art.HeadSHA, art.Base, reviewJobPR, reviewJobHead)
	}
	if want := (ReviewReviewer{Account: "team-a", Model: "claude-opus-5-5", Effort: "high"}); art.Reviewer != want {
		t.Errorf("reviewer = %+v, want %+v", art.Reviewer, want)
	}
	if art.Verdict != "fix first" {
		t.Errorf("verdict = %q, want the reviewer's own word", art.Verdict)
	}
	if len(art.Issues) != 1 || art.Issues[0] != issue {
		t.Errorf("issues = %+v, want the one issue the reviewer wrote (%+v)", art.Issues, issue)
	}
	if art.Overview == "" || len(art.Strengths) == 0 || art.Security == "" || art.Performance == "" || art.TestCoverage == "" {
		t.Errorf("a section the reviewer wrote is missing from the artifact: %+v", art)
	}
	if art.MutationChecks == nil {
		t.Error("mutation_checks is null rather than the empty list a review with no checks carries")
	}
	// The bytes that were signed are the bytes a reader reproduces: the writer
	// and the reader are one shape, field for field, so nothing a reader drops
	// can hide inside the signed bytes.
	again, err := json.Marshal(art)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, data) {
		t.Errorf("the artifact does not round-trip byte for byte:\n wrote %s\n read  %s", data, again)
	}
}

func TestReviewArtifactRefusesAFourthVerdictWord(t *testing.T) {
	r, _ := reviewJobRun(t, nil)
	res := reviewResult(t, reviewAnswerOf("approve", "looks good to me"))
	err := r.writeReviewArtifact(context.Background(), res)
	if err == nil {
		t.Fatal("a fourth verdict word was accepted as a review")
	}
	if !strings.Contains(err.Error(), "verdict") {
		t.Errorf("the refusal does not name the field it refused: %v", err)
	}
	// The other reader refuses it too, and for the record's own reason: an
	// artifact whose verdict is not one of the three is refused whether it came
	// from a reviewer or out of the bucket.
	art := reviewArtifactFixture(t)
	for _, word := range []string{"approve", "changes", "ship it", ""} {
		art.Verdict = word
		data, merr := json.Marshal(art)
		if merr != nil {
			t.Fatal(merr)
		}
		if _, derr := DecodeReviewArtifact(data); derr == nil {
			t.Errorf("the reader accepted verdict %q", word)
		}
	}
	for _, word := range []string{"ship", "fix first", "reject"} {
		art.Verdict = word
		data, merr := json.Marshal(art)
		if merr != nil {
			t.Fatal(merr)
		}
		if _, derr := DecodeReviewArtifact(data); derr != nil {
			t.Errorf("the reader refused its own verdict word %q: %v", word, derr)
		}
	}
}

func TestReviewArtifactTruncatedIsAFailure(t *testing.T) {
	r, _ := reviewJobRun(t, nil)
	out, err := json.Marshal(reviewAnswerOf("ship", "everything checked out"))
	if err != nil {
		t.Fatal(err)
	}
	// D886's failure: the model's output stopped, so the JSON is cut mid-object.
	// It is a failed review and never a short ship.
	if err := r.writeReviewArtifact(context.Background(), agent.Result{Structured: out[:len(out)/2]}); err == nil {
		t.Fatal("a clipped answer was accepted as an artifact")
	}
	// A result that is valid JSON but missing a field is the same failure: an
	// answer that carries no test_coverage stopped before it wrote one.
	var partial map[string]any
	if err := json.Unmarshal(out, &partial); err != nil {
		t.Fatal(err)
	}
	delete(partial, "test_coverage")
	short, err := json.Marshal(partial)
	if err != nil {
		t.Fatal(err)
	}
	err = r.writeReviewArtifact(context.Background(), agent.Result{Structured: short})
	if err == nil {
		t.Fatal("an answer missing a field was accepted as an artifact")
	}
	if !strings.Contains(err.Error(), "test_coverage") {
		t.Errorf("the refusal does not name the field the answer stopped before: %v", err)
	}
	// Nothing was written on either path: a failed review is not a partial one.
	if len(r.rec.Artifacts) != 0 || len(r.rec.Reviews) != 0 {
		t.Errorf("a failed review wrote to the record: %+v", r.rec)
	}
}

func TestReviewArtifactCarriesItsOwnClock(t *testing.T) {
	r, _ := reviewJobRun(t, nil)
	if err := r.writeReviewArtifact(context.Background(),
		reviewResult(t, reviewAnswerOf("reject", "the direction is wrong for this repository"))); err != nil {
		t.Fatal(err)
	}
	data := reviewObject(t, r, ReviewArtifactName)
	art, err := DecodeReviewArtifact(data)
	if err != nil {
		t.Fatal(err)
	}
	now := r.d.Now()
	if !art.CreatedAt.Equal(now) {
		t.Errorf("created_at = %s, want the writer's own clock %s", art.CreatedAt, now)
	}
	if art.CreatedAt.Location() != time.UTC {
		t.Errorf("created_at = %s, want it in UTC: a reader holding a copy has no run record to compare it against", art.CreatedAt)
	}
	// A reader holding only the object says when the review was performed: the
	// field is in the bytes, in RFC 3339, without the record travelling with it.
	if want := `"created_at":"2026-11-02T09:30:00Z"`; !strings.Contains(string(data), want) {
		t.Errorf("the artifact's bytes carry no %s:\n%s", want, data)
	}
	// And the field is held to that one shape: a timestamp a reader cannot
	// place is refused rather than carried.
	bad := bytes.Replace(data, []byte(`"created_at":"2026-11-02T09:30:00Z"`), []byte(`"created_at":"yesterday"`), 1)
	if _, err := DecodeReviewArtifact(bad); err == nil {
		t.Error("the reader accepted created_at: yesterday")
	}
}

func TestReviewMarkdownMatchesTheJSON(t *testing.T) {
	r, _ := reviewJobRun(t, nil)
	issue := ReviewIssue{
		Severity: "major", Class: "correctness",
		File: "internal/runner/reviewmode.go", Line: 620,
		Summary:  "the halt is checked after the verdict is parsed",
		Fix:      "check the halt first, as the other branches do",
		Evidence: "reviewmode.go:620 reads the verdict before haltValue",
	}
	if err := r.writeReviewArtifact(context.Background(),
		reviewResult(t, reviewAnswerOf("fix first", "one ordering is wrong and the rest is sound", issue))); err != nil {
		t.Fatal(err)
	}
	art, err := DecodeReviewArtifact(reviewObject(t, r, ReviewArtifactName))
	if err != nil {
		t.Fatal(err)
	}
	md := string(reviewObject(t, r, ReviewMarkdownName))

	// Section for section, in the artifact's own words: the markdown is
	// generated from the JSON and is never a second telling of it, so every
	// section's text is the field's text.
	for _, s := range []struct{ heading, text string }{
		{"Overview", art.Overview},
		{"Strengths", art.Strengths[0]},
		{"Security", art.Security},
		{"Performance", art.Performance},
		{"Test coverage", art.TestCoverage},
	} {
		if !strings.Contains(md, "## "+s.heading) {
			t.Errorf("review.md has no %q section:\n%s", s.heading, md)
		}
		if !strings.Contains(md, s.text) {
			t.Errorf("review.md's %q section does not carry the artifact's own text %q:\n%s", s.heading, s.text, md)
		}
	}
	if !strings.Contains(md, "## Issues to flag") {
		t.Errorf("review.md has no issues section:\n%s", md)
	}
	for _, part := range []string{issue.Summary, issue.Fix, issue.Evidence, issue.File, issue.Severity, issue.Class} {
		if !strings.Contains(md, part) {
			t.Errorf("review.md's issues section drops %q:\n%s", part, md)
		}
	}
	for _, part := range []string{art.Verdict, art.HeadSHA, art.Base, "#1470"} {
		if !strings.Contains(md, part) {
			t.Errorf("review.md does not name %q:\n%s", part, md)
		}
	}
	if !strings.Contains(md, "## Verdict") {
		t.Errorf("review.md has no verdict section:\n%s", md)
	}
}

func TestReviewRecordCarriesTheIndependentTier(t *testing.T) {
	for _, tc := range []struct{ verdict, record string }{
		{"ship", "ship"},
		// The artifact's other two words are not ships, and the record has no
		// word of its own for reject: O4 keeps the record's two words and puts
		// the new value on tier, which is display-only.
		{"fix first", "changes"},
		{"reject", "changes"},
	} {
		r, _ := reviewJobRun(t, nil)
		res := reviewResult(t, reviewAnswerOf(tc.verdict, "the review's own words about the change",
			ReviewIssue{Severity: "minor", Class: "cosmetic", File: "a.go", Line: 1,
				Summary: "a summary", Fix: "a fix", Evidence: "the evidence"}))
		if err := r.writeReviewArtifact(context.Background(), res); err != nil {
			t.Fatalf("verdict %q: %v", tc.verdict, err)
		}
		if len(r.rec.Reviews) != 1 {
			t.Fatalf("verdict %q: the record carries %d reviews[] entries, want exactly 1", tc.verdict, len(r.rec.Reviews))
		}
		entry := r.rec.Reviews[0]
		if entry.Tier != runstore.TierIndependent {
			t.Errorf("verdict %q: tier = %q, want %q", tc.verdict, entry.Tier, runstore.TierIndependent)
		}
		if entry.Verdict != tc.record {
			t.Errorf("verdict %q: the record's verdict = %q, want %q", tc.verdict, entry.Verdict, tc.record)
		}
		if entry.Round != 1 || entry.Findings != 1 || entry.Head != reviewJobHead {
			t.Errorf("verdict %q: the entry is %+v, want round 1, one finding and the head it read", tc.verdict, entry)
		}
		if r.rec.PR == nil || r.rec.PR.Number != reviewJobPR {
			t.Errorf("verdict %q: the record's pr link is %+v, want #%d", tc.verdict, r.rec.PR, reviewJobPR)
		}
		if r.rec.HeadSHA != reviewJobHead {
			t.Errorf("verdict %q: the record's head_sha = %q, want %q", tc.verdict, r.rec.HeadSHA, reviewJobHead)
		}
		if r.rec.Status != runstore.StatusSucceeded || r.rec.Outcome != runstore.OutcomeReviewed {
			t.Errorf("verdict %q: the run ended %s/%s, want succeeded/reviewed", tc.verdict, r.rec.Status, r.rec.Outcome)
		}
		// The artifact's three-word vocabulary never reaches the record: a
		// reader of reviews[] compares against the two words it has always had.
		rec, err := json.Marshal(r.rec)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(rec, []byte(`"tier":"independent"`)) {
			t.Errorf("verdict %q: the record does not name the independent tier: %s", tc.verdict, rec)
		}
		for _, word := range []string{"fix first", "reject"} {
			if bytes.Contains(rec, []byte(`"`+word+`"`)) {
				t.Errorf("verdict %q: the record carries the artifact's word %q: %s", tc.verdict, word, rec)
			}
		}
	}
}

func TestReviewRunFailsWhenTheSignCallFails(t *testing.T) {
	r, _ := reviewJobRun(t, nil)
	setReviewSigner(t, func(*run) (ReviewSigner, error) {
		return reviewSignerFunc(func(context.Context, string) (string, error) {
			return "", errors.New("the review artifact could not be signed: the service answered 403, refused: unverified_run_identity")
		}), nil
	})
	res := reviewResult(t, reviewAnswerOf("ship", "nothing to flag"))
	r.finishReviewJob(context.Background(), res)
	if r.rec.Status != runstore.StatusFailed || r.rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("a run whose sign call failed ended %s/%s, want failed/none", r.rec.Status, r.rec.Outcome)
	}
	if !strings.Contains(r.rec.Reason, "refused: unverified_run_identity") {
		t.Errorf("the run's reason does not carry the service's own refusal: %q", r.rec.Reason)
	}
	if len(r.rec.Artifacts) != 0 || len(r.rec.Reviews) != 0 {
		t.Errorf("a run that could not sign stored an artifact or a review entry: %+v", r.rec)
	}
	if _, err := r.d.Store.ReadFile(context.Background(), runstore.ArtifactPrefix+ReviewArtifactName); err == nil {
		t.Error("the run stored review.json although nothing signed it")
	}
}

func TestReviewJobStageIsNotParsedAsAnInRunVerdict(t *testing.T) {
	job, _ := reviewJobRun(t, nil)
	res := reviewResult(t, reviewAnswerOf("ship", "nothing to flag"))

	// The stage's own request: the review job is asked for the artifact's
	// shape, never for the in-run pair (D890, F23).
	if got := job.reviewStageRequest("a prompt").JSONSchema; got != ReviewArtifactSchema {
		t.Errorf("the review job's stage is asked for %s, want the artifact's own schema", got)
	}
	for _, word := range []string{"ship", "fix first", "reject"} {
		if !strings.Contains(ReviewArtifactSchema, `"`+word+`"`) {
			t.Errorf("the artifact's schema does not name its own verdict word %q", word)
		}
	}
	// The other way round: a run that is not the review job -- today's
	// `fugaro run --pr N --recipe review-only`, and a coding run's own review
	// rounds -- passes VerdictSchema exactly as it does today.
	coding, _ := reviewJobRun(t, func(r *run) { r.d.Env = []string{} })
	if coding.reviewJob() {
		t.Fatal("a run whose environment does not carry EnvReviewJob is read as the review job")
	}
	if got := coding.reviewStageRequest("a prompt").JSONSchema; got != VerdictSchema {
		t.Errorf("a coding run's review stage is asked for %s, want VerdictSchema", got)
	}

	// And the reader the review job's result must never reach (F23). The two
	// formats share the key `verdict` and nothing else, so an artifact handed
	// to the in-run reader is either refused -- its `fix first` is not one of
	// the two words that reader has, so it answers a synthesized blocker about
	// the parser, which is a code finding nobody wrote -- or, worse, read as a
	// PASS with no findings at all, because the artifact carries no `findings`
	// key. Both answers are about a shape the reader was never meant to see,
	// which is why reviewMode's review-job arm decodes with the artifact's
	// reader instead (D890).
	refused := reviewResult(t, reviewAnswerOf("fix first", "one read is in the wrong order",
		ReviewIssue{Severity: "major", Class: "security", File: "a.go", Line: 1,
			Summary: "a summary", Fix: "a fix", Evidence: "the evidence"}))
	if v := ParseVerdict(refused); len(v.Findings) != 1 || !strings.Contains(v.Findings[0].Summary, "no parseable verdict") {
		t.Fatalf("ParseVerdict on the artifact's shape answered %+v, want its own blocking sentence about the parser", v)
	}
	if v := ParseVerdict(res); v.Verdict != "ship" || len(v.Findings) != 0 {
		t.Fatalf("the in-run reader read an artifact as %+v: it read %d findings, and this test's premise is that an artifact fed to it is a pass with none. If that changed, reviewMode's review-job arm is what still keeps the two apart -- and this test needs to say so again", v, len(v.Findings))
	}
	art, err := decodeReviewAnswer(res.Structured)
	if err != nil {
		t.Fatalf("the artifact's own reader refused the reviewer's answer: %v", err)
	}
	if art.Verdict != "ship" || len(art.Issues) != 0 {
		t.Errorf("the artifact's reader read %+v, want the reviewer's own ship and no issues", art)
	}
}

func TestUnreadableReviewArtifactEndsTheRunInvalid(t *testing.T) {
	r, _ := reviewJobRun(t, nil)
	// A result with no artifact in it: the reviewer answered in the in-run
	// format, or wrote prose. ParseVerdict would read the fenced block (its own
	// fallback); the artifact's reader has none, because the review job's
	// result IS the artifact (spec §3).
	res := agent.Result{Text: "I reviewed the diff.\n\n```json\n{\"verdict\":\"ship\",\"findings\":[]}\n```\n"}
	r.finishReviewJob(context.Background(), res)
	if r.rec.Status != runstore.StatusFailed || r.rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("a run whose artifact could not be read ended %s/%s, want failed/none", r.rec.Status, r.rec.Outcome)
	}
	if r.rec.Reason == "" {
		t.Fatal("the run ended invalid with no reason")
	}
	// The reason is the decoder's own and never a sentence about the parser: a
	// blocker finding invented by a parser, stored beside real findings, is a
	// code finding nobody wrote (spec §3).
	if strings.Contains(r.rec.Reason, "review produced no parseable verdict") {
		t.Errorf("the run's reason is ParseVerdict's synthesized sentence: %q", r.rec.Reason)
	}
	if len(r.rec.Reviews) != 0 || len(r.rec.Artifacts) != 0 {
		t.Errorf("a run whose artifact could not be read recorded %+v / %+v", r.rec.Reviews, r.rec.Artifacts)
	}
}

func TestReviewArtifactIsSignedByTheCredentialsService(t *testing.T) {
	svc := newReviewService(t)
	meta := gitMetadataFake(t, gitServiceIDToken)
	identity := gitRunIdentity(t)
	r, _ := reviewJobRun(t, func(r *run) {
		r.d.Env = []string{
			EnvReviewJob + "=1",
			credentialsServiceURLEnv + "=" + svc.srv.URL,
			metadataHostEnv + "=" + strings.TrimPrefix(meta.URL, "http://"),
		}
		r.ident.tok = identity
	})
	// The run's own signer, not the seam's fake: this is the test about the
	// client, so it goes through serviceMintClient's rules and the headers.
	clearReviewSigner(t)

	res := reviewResult(t, reviewAnswerOf("fix first", "one issue, and it is worth fixing before this lands",
		ReviewIssue{Severity: "major", Class: "maintainability", File: "a.go", Line: 3,
			Summary: "a summary", Fix: "a fix", Evidence: "the evidence"}))
	if err := r.writeReviewArtifact(context.Background(), res); err != nil {
		t.Fatalf("the review job could not write its signed artifact: %v", err)
	}

	calls := svc.calls()
	if len(calls) != 1 {
		t.Fatalf("the run made %d sign calls, want exactly 1", len(calls))
	}
	call := calls[0]
	if call.path != credsvc.SignPath {
		t.Errorf("the sign call went to %s, want %s", call.path, credsvc.SignPath)
	}
	if want := "Bearer " + gitServiceIDToken; call.auth != want {
		t.Errorf("the sign call's job identity is %q, want the job's own ID token", call.auth)
	}
	if want := identity.Token(); call.run != want || want == "" {
		t.Errorf("the sign call's run identity is %q, want the run's own token", call.run)
	}
	if call.job != svcJob {
		t.Errorf("the sign call reports job %q, want %q", call.job, svcJob)
	}

	data := reviewObject(t, r, ReviewArtifactName)
	sig := reviewObject(t, r, ReviewSignName)
	// The digest of the object's exact bytes is what travels; the reviewer's
	// prose never does.
	digest := sha256Hex(string(data))
	if want := `{"digest":"` + digest + `"}`; call.body != want {
		t.Errorf("the sign call's body is %s, want only the digest %s", call.body, want)
	}
	// The signature is a sidecar object and never a field, so the bytes that
	// were signed are exactly the bytes that are stored.
	if bytes.Contains(data, []byte("signature")) {
		t.Errorf("the artifact carries a signature field: %s", data)
	}
	if !reviewSignatureLine.Match(sig) {
		t.Errorf("the sidecar is not one line of lowercase hex: %q", sig)
	}
	if want := svc.sign(digest) + "\n"; string(sig) != want {
		t.Errorf("the sidecar holds %q, want the service's own answer", sig)
	}
	want := []string{ReviewArtifactName, ReviewSignName, ReviewMarkdownName}
	got := make([]string, 0, len(r.rec.Artifacts))
	for _, a := range r.rec.Artifacts {
		got = append(got, a.Name)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("the record's artifacts are %v, want %v", got, want)
	}

	// The verifier's half: the service's own signature over those bytes
	// verifies, and one made under another account's key does not.
	if _, err := VerifyReviewArtifact(context.Background(), data, string(sig), reviewWant(), svc.verifier()); err != nil {
		t.Errorf("the artifact the run wrote does not verify: %v", err)
	}
	other := newReviewService(t)
	if _, err := VerifyReviewArtifact(context.Background(), data, string(sig), reviewWant(), other.verifier()); err == nil {
		t.Error("a signature made under another account's key verified")
	}
}

func TestReviewArtifactRedactsBeforeTheDigest(t *testing.T) {
	secret := "fixture-review-secret-" + strings.ReplaceAll(t.Name(), "/", "-")
	r, _ := reviewJobRun(t, nil)
	r.addSecret(secret)
	issue := ReviewIssue{Severity: "major", Class: "security", File: "a.go", Line: 1,
		Summary: "the value is written to the log", Fix: "redact it", Evidence: "see the error path"}
	res := reviewResult(t, reviewAnswerOf("fix first", "the diff prints "+secret+" on the error path", issue))
	if err := r.writeReviewArtifact(context.Background(), res); err != nil {
		t.Fatal(err)
	}
	data := reviewObject(t, r, ReviewArtifactName)
	md := reviewObject(t, r, ReviewMarkdownName)
	// The redaction is the run's, before the digest: the signature covers the
	// redacted bytes, so a redaction applied at publish time would change those
	// bytes and break every signature over them (spec §3).
	if bytes.Contains(data, []byte(secret)) || bytes.Contains(md, []byte(secret)) {
		t.Error("a secret the run holds reached the artifact or the markdown")
	}
	if !bytes.Contains(data, []byte("[REDACTED]")) {
		t.Errorf("the artifact carries no redaction mark where the secret was:\n%s", data)
	}
}

func TestVerifyRefusesAForgedArtifact(t *testing.T) {
	ctx := context.Background()
	svc := newReviewService(t)
	data, sig := signedReviewArtifact(t, svc, reviewArtifactFixture(t))

	// A byte changed in the object: the signature is over the bytes that were
	// signed, so the forgery is a different object and the digest no longer
	// matches it (D892(a)). The object still reads -- the refusal is the
	// signature's and not the reader's.
	forged := bytes.Replace(data, []byte(`"fix first"`), []byte(`"ship"`), 1)
	if bytes.Equal(forged, data) {
		t.Fatal("the fixture does not carry the literal this test changes")
	}
	if _, err := DecodeReviewArtifact(forged); err != nil {
		t.Fatalf("the forged object does not read, so this test would prove the reader: %v", err)
	}
	if _, err := VerifyReviewArtifact(ctx, forged, sig, reviewWant(), svc.verifier()); err == nil {
		t.Error("a forged artifact verified")
	}
	// ...and the honest object still does, so the refusal above is the forgery
	// rather than a verifier that refuses everything.
	if _, err := VerifyReviewArtifact(ctx, data, sig, reviewWant(), svc.verifier()); err != nil {
		t.Errorf("the unforged artifact does not verify: %v", err)
	}
}

func TestVerifyRefusesAnUnsignedArtifact(t *testing.T) {
	ctx := context.Background()
	svc := newReviewService(t)
	data, sig := signedReviewArtifact(t, svc, reviewArtifactFixture(t))

	// A missing sidecar is a refusal and never a default (spec §3).
	for _, missing := range []string{"", "\n", "  "} {
		if _, err := VerifyReviewArtifact(ctx, data, missing, reviewWant(), svc.verifier()); err == nil {
			t.Errorf("an artifact with signature %q verified", missing)
		}
	}
	// A signature that is genuinely the service's, but not over these bytes: a
	// signature lifted from another object is not a verdict about this one. The
	// other object is the same review with one field changed, so the pair
	// differs only in the bytes the digest is over.
	elsewhere := reviewArtifactFixture(t)
	elsewhere.Overview = "the same review of a different change"
	other, otherSig := signedReviewArtifact(t, svc, elsewhere)
	if bytes.Equal(other, data) {
		t.Fatal("editing the fixture did not change its bytes, so this test cannot tell two objects apart")
	}
	if _, err := VerifyReviewArtifact(ctx, data, otherSig, reviewWant(), svc.verifier()); err == nil {
		t.Error("a signature over another object verified")
	}
	if _, err := VerifyReviewArtifact(ctx, other, sig, reviewWant(), svc.verifier()); err == nil {
		t.Error("another object verified under this one's signature")
	}
}

func TestVerifyRefusesAReplayedArtifact(t *testing.T) {
	ctx := context.Background()
	svc := newReviewService(t)
	data, sig := signedReviewArtifact(t, svc, reviewArtifactFixture(t))
	if _, err := VerifyReviewArtifact(ctx, data, sig, reviewWant(), svc.verifier()); err != nil {
		t.Fatalf("the fixture's own artifact does not verify: %v", err)
	}

	// The same review run, a head the pull request has moved past: the
	// signature is valid -- it is this service's, over these bytes and this
	// frame -- and the verdict is about a commit that is no longer the head.
	// This is the check a signature alone does not make.
	stale := reviewWant()
	stale.HeadSHA = reviewJobOldHead
	if _, err := VerifyReviewArtifact(ctx, data, sig, stale, svc.verifier()); err == nil {
		t.Error("a validly signed verdict about an earlier head verified against the current one")
	}

	// An earlier review run's artifact: the service frames its signature over
	// the run it verified, so a signature lifted from one review is not one for
	// another, whatever the object says.
	earlier := reviewArtifactFixture(t)
	earlier.ReviewRun = "20261030-000000-abcd"
	earlier.HeadSHA = reviewJobOldHead
	edata, esig := signedReviewArtifact(t, svc, earlier)
	if _, err := VerifyReviewArtifact(ctx, edata, esig, reviewWant(), svc.verifier()); err == nil {
		t.Error("a verdict of an earlier review run verified as this run's")
	}

	// And an artifact of another repository or another pull request: the same
	// reviewer's own words, the same key, a verdict about something else.
	for _, corrupt := range []func(*ReviewArtifact){
		func(a *ReviewArtifact) { a.Repository = "owner-repo-b-0f0f0f0f0f0f0f0f" },
		func(a *ReviewArtifact) { a.PR = reviewJobPR + 1 },
	} {
		art := reviewArtifactFixture(t)
		corrupt(&art)
		cdata, csig := signedReviewArtifact(t, svc, art)
		if _, err := VerifyReviewArtifact(ctx, cdata, csig, reviewWant(), svc.verifier()); err == nil {
			t.Errorf("an artifact of another repository or pull request verified: %+v", art)
		}
	}
}
