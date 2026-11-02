package runner

// This file is the independent review's verdict artifact: the versioned
// document a review job writes (docs/cloud-senior-review spec §3, D884), the
// markdown rendered from it, the signing client that gets its digest signed by
// the credentials service (D892(a), O3) and the four checks a coordinator
// makes before it reads the verdict (spec §6).
//
// It holds NO key. The signature is the credentials service's, because this
// job is the one that runs a test from the head under review (D885): a key
// here would share an OS user and the /proc residual with the account's OAuth
// token. What the run sends is the digest of the artifact's exact bytes --
// never the reviewer's prose, which is what makes the digest the thing to sign
// and redaction a step that must happen BEFORE it (spec §3).
//
// The artifact's verdict is this shape's own three words (ship, fix first,
// reject), and it shares no reader and no producer with the in-run pair
// (verdict.go, D890/F23): the coding run's own review rounds keep
// VerdictSchema and ParseVerdict, and reviewmode.go branches on the job rather
// than teaching either format the other's vocabulary.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// EnvReviewJob marks the execution of the independent review job: the launcher
// sets it on the review job's own run (the variant CSR7 deploys, launched by
// CSR2), and its PRESENCE is the whole fact.
//
// It is an environment marker rather than a flag, a task field or a recipe
// step because none of those is available to this task and each would be a
// worse answer: a flag would be a second command line for the launcher to keep
// in step with the job it started, a task field is a repository's to write and
// this is the installation's own fact about which job is running, and a step
// name is the shape the review stage already has for every mode (review-only
// recipes run today) so it cannot tell the two apart. The job's own name
// cannot either: this file may not read the platform's report of the execution
// in a way the review job's variant controls, and jobName is what the service
// checks anyway. So the fact is written where the launcher already writes the
// job's other inputs (backend.LaunchSpec.Env, as the credentials service's own
// URL is), and a run that is not the review job does not carry it -- which is
// what makes the coding run's behaviour byte-for-byte what it was.
const EnvReviewJob = "FUGARO_REVIEW_JOB"

// The artifact's own names. The sidecar's is derived from the object's so the
// pair is obvious in a bucket listing, and neither is a field of the artifact:
// a signature inside the object would change the bytes it covers (spec §3).
const (
	ReviewArtifactName = "review.json"
	ReviewSignName     = "review.json.sig"
	ReviewMarkdownName = "review.md"
)

// ReviewArtifactVersion is the shape's version (spec §3): a new field is
// additive and keeps it, removing or retyping one bumps it, and a field whose
// meaning must change gets a new name.
const ReviewArtifactVersion = 1

// ReviewArtifactSchema is the JSON Schema the review job's stage is asked for
// with --json-schema (D890, F23). It is the reviewer's own half of the
// artifact: what the model writes. The provenance -- schema_version,
// review_run, created_at, repository, pr, head_sha, base and the reviewer
// block -- is the runner's and is never taken from an answer, so it is not
// asked for here.
//
// It is a Go constant rather than a read of schemas/review.schema.json because
// the flag takes the schema inline and the job image carries no schemas/
// directory; schemas_test.go's TestReviewArtifactSchemaMatchesStruct pins the
// two together, so a field added to one and not the other fails there rather
// than silently at a reviewer's parser.
const ReviewArtifactSchema = `{"type":"object","additionalProperties":false,"required":["verdict","overview","strengths","issues","security","performance","test_coverage"],"properties":{"verdict":{"type":"string","enum":["ship","fix first","reject"]},"overview":{"type":"string"},"strengths":{"type":"array","items":{"type":"string"}},"issues":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["severity","class","file","summary","fix","evidence"],"properties":{"severity":{"type":"string","enum":["blocker","major","minor"]},"class":{"type":"string","enum":["correctness","performance","security","data_loss","maintainability","cosmetic"]},"file":{"type":"string"},"line":{"type":"integer","minimum":1},"summary":{"type":"string"},"fix":{"type":"string"},"evidence":{"type":"string"},"likelihood":{"type":"integer","minimum":1,"maximum":5},"impact":{"type":"integer","minimum":1,"maximum":5},"fix_cost":{"type":"integer","minimum":1,"maximum":5}}}},"security":{"type":"string"},"performance":{"type":"string"},"test_coverage":{"type":"string"}}}`

// The vocabularies the artifact's own reader closes, each the set the shape
// that already exists carries: severity is the in-job finding's
// (docs/in-job-review/spec.md §6), class is the closed set VerdictSchema
// names, observed is the four words §3 fixes for a mutation check, and the
// verdict is this artifact's own three.
var (
	reviewVerdicts = []string{"ship", "fix first", "reject"}
	reviewSeverity = []string{"blocker", "major", "minor"}
	reviewClasses  = []string{"correctness", "performance", "security", "data_loss", "maintainability", "cosmetic"}
	reviewObserved = []string{"pass", "fail", "error", "timeout"}
)

// ReviewIssue is one issue a review asks to be fixed. It is the in-run
// Finding's shape plus the three fields only an independent review carries
// (F12): the line the reviewer read, the fix it suggests, and the evidence it
// read. The three anchored axes are carried too (D884): they are what a mode's
// threshold table reads, and an independent review a mode cannot read would be
// a verdict nobody could act on.
type ReviewIssue struct {
	Severity string `json:"severity"`
	Class    string `json:"class"`
	File     string `json:"file"`
	// Line is a claim about the head the review read, which is why HeadSHA is
	// recorded beside it: a reader that finds the file moved still knows which
	// commit the line meant.
	Line    int    `json:"line,omitempty"`
	Summary string `json:"summary"`
	Fix     string `json:"fix"`
	// Evidence is what the reviewer read, quoted: the diff hunk, the call
	// path, the test it ran.
	Evidence   string `json:"evidence"`
	Likelihood int    `json:"likelihood,omitempty"`
	Impact     int    `json:"impact,omitempty"`
	FixCost    int    `json:"fix_cost,omitempty"`
}

// ReviewMutationCheck is one mutant the runner applied, ran and reverted
// (D885, spec §3). It is written by the runner and never by the reviewer's
// prose: Applied and Reverted are the runner's own statements about what it
// did to the worktree (CSR4b fills this; a review with no checks carries []).
//
// Observed is a closed four-word vocabulary and a fifth word is refused for
// the reason the verdict refuses a fourth: a reader routes on this word
// without parsing prose. "fail" is the good outcome -- the mutant was killed.
type ReviewMutationCheck struct {
	File     string `json:"file"`
	Test     string `json:"test"`
	Applied  string `json:"applied"`
	Observed string `json:"observed"`
	Reverted bool   `json:"reverted"`
}

// ReviewReviewer is who wrote the review: the account the launcher named, the
// model the review stage ran, and its level's effort. It is provenance the
// coordinator reads beside the object, and the same three facts the run record
// carries for every stage.
type ReviewReviewer struct {
	Account string `json:"account"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
}

// ReviewArtifact is the independent review's verdict, versioned and
// self-describing. Its first four fields after the version are what the
// signature binds (with head_sha): review_run, repository, pr and head_sha
// make it a statement about ONE review of ONE pull request at ONE commit, and
// the verifier's four checks compare exactly those (spec §6).
type ReviewArtifact struct {
	SchemaVersion int `json:"schema_version"`
	// ReviewRun is the review run's own id, minted by the launcher after the
	// coding run's job ended: it is the nonce the coordinator checks, and it is
	// one of the two claims the service frames a signature over.
	ReviewRun string `json:"review_run"`
	// CreatedAt is the artifact's own clock, RFC 3339 in UTC, written by the
	// writer at the moment it writes: a reader holding only this object -- a
	// copy in a fix round's brief, a comment, a later audit -- can say when the
	// review was performed without the record travelling with it. It is the
	// writer's claim and is not trusted alone: the coordinator reads it beside
	// the record's own timestamps, and the signature binds it with everything
	// else.
	CreatedAt time.Time `json:"created_at"`
	// Repository is the run's own slug, the spelling its path uses
	// (runs/<slug>/<review-run>/artifacts/), so the coordinator compares like
	// with like.
	Repository   string         `json:"repository"`
	PR           int            `json:"pr"`
	HeadSHA      string         `json:"head_sha"`
	Base         string         `json:"base"`
	Reviewer     ReviewReviewer `json:"reviewer"`
	Verdict      string         `json:"verdict"`
	Overview     string         `json:"overview"`
	Strengths    []string       `json:"strengths"`
	Issues       []ReviewIssue  `json:"issues"`
	Security     string         `json:"security"`
	Performance  string         `json:"performance"`
	TestCoverage string         `json:"test_coverage"`
	// MutationChecks is the runner's own list, [] for a review that ran none.
	MutationChecks []ReviewMutationCheck `json:"mutation_checks"`
}

// reviewAnswer is what the reviewer is asked for: the artifact's model-written
// fields and nothing else. The provenance is the runner's.
type reviewAnswer struct {
	Verdict      string        `json:"verdict"`
	Overview     string        `json:"overview"`
	Strengths    []string      `json:"strengths"`
	Issues       []ReviewIssue `json:"issues"`
	Security     string        `json:"security"`
	Performance  string        `json:"performance"`
	TestCoverage string        `json:"test_coverage"`
}

// ReviewSigner is the credentials service's sign route as this package uses
// it: one call, the artifact's digest, and the signature the service makes
// (C3.9e, D892(a)). It is an interface so a test satisfies it without a
// service — this task needs no credentials service running.
type ReviewSigner interface {
	Sign(ctx context.Context, digest string) (string, error)
}

// SignatureVerifier is the credentials service's verify route as the
// coordinator uses it (C3.9f): whether signature is this service's over
// digest, framed with the identities of the run the call was made under. It
// re-frames from its own copy of those claims and never from anything the
// artifact carries, which is what makes a lifted signature useless.
type SignatureVerifier interface {
	Verify(ctx context.Context, digest, signature string) error
}

// ReviewExpectation is what the coordinator knows independently of the
// artifact: the review run it launched (the nonce), the repository and pull
// request it asked about, and the head the pull request has NOW. The four
// checks compare these against the artifact's own four fields, and a mismatch
// is a refusal and never a ship (spec §6).
type ReviewExpectation struct {
	ReviewRun  string
	Repository string
	PR         int
	HeadSHA    string
}

// ErrReviewArtifact is the artifact's own family: what a reader returns when
// the object is not the versioned shape it reads.
var ErrReviewArtifact = errors.New("the review artifact")

// ErrReviewSignature is the signature's family: what the signer returns when
// the service did not sign, and what a coordinator's verifier returns when the
// signature does not hold. A failed sign call ends the run StatusFailed with
// the service's reason and never a ship with an unsigned artifact (O4).
var ErrReviewSignature = errors.New("the review artifact's signature")

// reviewSignTimeout bounds one sign call, the mint's own figure for the same
// reason: the service is one small call away and a run must not hang on it.
const reviewSignTimeout = 15 * time.Second

// maxReviewSignAnswerBytes bounds the sign route's answer: two small strings.
const maxReviewSignAnswerBytes = 4 << 10

// reviewSignAnswer is the sign route's answer (C3.9e's SignResponse). The
// algorithm is read and checked rather than assumed: a service that answered
// under a weaker one would be a signature this build must not store.
type reviewSignAnswer struct {
	Alg       string `json:"alg"`
	Signature string `json:"signature"`
}

// reviewSignatureRE is the shape of a signature this code stores: lowercase
// hex, the one line the sidecar holds. It is checked on the answer and again
// on the bytes written, so nothing but hex can reach the object.
var reviewSignatureRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// reviewServiceErrorCode is the wire code of a refused answer (C3.9e's own
// shape, the mint's serviceErrorCode).
var reviewServiceErrorCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,64}$`)

// ServiceSigner is the credentials service as the artifact's signer: the
// service's URL from the job's environment, the job's Google ID token minted
// from the metadata server for that URL, and the run's own Firebase ID token.
// It holds no key and can compute no MAC -- the service is the signer
// (D892(a)).
type ServiceSigner struct {
	URL string
	// HTTP is the client a test hands in; nil means this code builds one.
	HTTP *http.Client
	// IDToken mints the job's identity token for the service's own URL.
	IDToken func(ctx context.Context) (string, error)
	// RunIDToken is the run's own Firebase ID token, read at call time: the
	// session refreshes it under the run.
	RunIDToken func() string
	// Job is the Cloud Run job this run reports it is in. It is the caller's
	// claim and decides nothing (the service decides from the verified
	// service-account email), and the service checks its shape.
	Job string
}

var _ ReviewSigner = ServiceSigner{}

// Sign posts one digest and returns the signature the service answered, or the
// service's own refusal.
//
// Nothing here can put a value in an error: the two identities are never
// formatted into one, and the answer is read for the status, the error code
// and the algorithm alone. The message beside that code is the service's text
// -- written for an operator's log and not for a run -- so it is dropped
// rather than relayed, exactly as the mint's refusals are.
func (s ServiceSigner) Sign(ctx context.Context, digest string) (string, error) {
	if s.URL == "" || s.IDToken == nil || s.RunIDToken == nil {
		return "", fmt.Errorf("%w: this run has no credentials service to sign at", ErrReviewSignature)
	}
	// The URL is checked before anything is sent to it, and for the reason the
	// mint checks its own: this call presents the run's Firebase ID token,
	// which is a bearer credential for the whole run.
	if err := checkReviewServiceURL(s.URL); err != nil {
		return "", err
	}
	run := s.RunIDToken()
	if run == "" {
		return "", fmt.Errorf("%w: the run's own identity token is not available, and a call under the job's identity alone is not one the service verifies", ErrReviewSignature)
	}
	id, err := s.IDToken(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: the job's identity token could not be minted: %v", ErrReviewSignature, err)
	}
	if id == "" {
		return "", fmt.Errorf("%w: the metadata server answered with no identity token", ErrReviewSignature)
	}
	for _, v := range []string{run, id} {
		if !printableToken(v) {
			return "", fmt.Errorf("%w: an identity token is not one header value", ErrReviewSignature)
		}
	}
	if !reviewDigestRE.MatchString(digest) {
		return "", fmt.Errorf("%w: the digest is not the 64 lowercase hex characters of a SHA-256", ErrReviewSignature)
	}
	body, err := json.Marshal(map[string]string{"digest": digest})
	if err != nil {
		return "", fmt.Errorf("%w: the sign call's request could not be built", ErrReviewSignature)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(s.URL, "/")+credsvc.SignPath, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("%w: the sign call's request could not be built", ErrReviewSignature)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+id)
	req.Header.Set(credsvc.HeaderRunIdentity, run)
	req.Header.Set(credsvc.HeaderRunJob, s.Job)
	resp, err := reviewSignClient(s.HTTP).Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: the credentials service could not be reached", ErrReviewSignature)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxReviewSignAnswerBytes+1))
	if err != nil {
		return "", fmt.Errorf("%w: the service's answer could not be read", ErrReviewSignature)
	}
	if len(data) > maxReviewSignAnswerBytes {
		return "", fmt.Errorf("%w: the service's answer is larger than a sign call's", ErrReviewSignature)
	}
	if resp.StatusCode != http.StatusOK {
		// The service's REASON is its own, and O4 is what makes it the run's:
		// the sentence below carries the code and the status, and the run's
		// failure arm records it as the reason the review did not happen.
		return "", fmt.Errorf("%w: the service answered %d%s", ErrReviewSignature, resp.StatusCode, reviewRefusalCode(data))
	}
	var ans reviewSignAnswer
	if err := json.Unmarshal(data, &ans); err != nil {
		return "", fmt.Errorf("%w: the service's answer is not a sign call's", ErrReviewSignature)
	}
	// The algorithm is checked by NAME before the signature is stored: an
	// answer naming something else is a signature this build cannot honor, and
	// storing it would be storing an artifact whose integrity claim is a word
	// nobody verified.
	if ans.Alg != reviewSignAlgorithm {
		return "", fmt.Errorf("%w: the service signed under %q, and this code stores %s only", ErrReviewSignature, ans.Alg, reviewSignAlgorithm)
	}
	if !reviewSignatureRE.MatchString(ans.Signature) {
		return "", fmt.Errorf("%w: the service's answer carries no signature of the shape this code stores", ErrReviewSignature)
	}
	return ans.Signature, nil
}

// reviewSignAlgorithm is the construction the service's answer must name
// (C3.9e's signAlgorithm). It is spelled here rather than imported because
// that constant is the service's own and this is the reader's half of the same
// contract; a service that changed it would be refused here, which is the
// point of checking it.
const reviewSignAlgorithm = "HMAC-SHA-256"

// reviewDigestRE is the shape of a digest a sign call carries, the service's
// own rule: the lowercase hex of the 32 bytes of a SHA-256.
var reviewDigestRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// checkReviewServiceURL refuses a credentials service URL this code will not
// send the run's identity to: https, or http on a loopback address. It is the
// rule the mint holds its own URL to (github.checkServiceURL) and the
// gateway's checkBaseURL before it, respelled here because this package
// reaches that rule neither through github nor through the gateway.
func checkReviewServiceURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%w: the credentials service URL must be a scheme and a host only", ErrReviewSignature)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if ip := net.ParseIP(u.Hostname()); (ip != nil && ip.IsLoopback()) || u.Hostname() == "localhost" {
			return nil
		}
	}
	return fmt.Errorf("%w: the credentials service URL must be https, or http on a loopback address", ErrReviewSignature)
}

// reviewSignClient is the client a sign call is made with: no redirects, no
// cookie jar, no compression of its own and a bounded wait, the mint's own
// rules (serviceMintClient) for the same two reasons -- the run's identity is
// in a header Go will not strip across hosts, and a followed hop would hand it
// to whatever host the Location named.
func reviewSignClient(c *http.Client) *http.Client {
	var out http.Client
	if c != nil {
		out = *c
	} else {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.DisableCompression = true
		out.Transport = t
		out.Timeout = reviewSignTimeout
	}
	out.Jar = nil
	out.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &out
}

// reviewRefusalCode is the wire error code of a refused answer, as
// ", refused: code", or "" when the answer carries none this contract could
// have written. Only the code is read: the message beside it is the service's
// text and is never relayed into a run's log or error.
func reviewRefusalCode(data []byte) string {
	var refusal struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &refusal) != nil || !reviewServiceErrorCode.MatchString(refusal.Error) {
		return ""
	}
	return ", refused: " + refusal.Error
}

// reviewSignerSeam, when a test sets it, is what reviewSigner returns instead
// of the run's own ServiceSigner. It exists because the sign route is a
// service this task must not need running: the tests satisfy the interface
// with a fake holding a key made at run time, and the one test that is about
// the client clears it.
var reviewSignerSeam func(*run) (ReviewSigner, error)

// reviewSigner is the run's signer: the credentials service from the job's
// environment, the metadata server's identity for its URL, the run's own
// identity, and the job's name read the way the rest of the run reads it
// (jobName, from the platform's own report of the execution).
func (r *run) reviewSigner() (ReviewSigner, error) {
	if reviewSignerSeam != nil {
		return reviewSignerSeam(r)
	}
	url := envLookup(r.d.Env, credentialsServiceURLEnv)
	if url == "" {
		return nil, fmt.Errorf("%w: this job has no credentials service (%s is not set), and the artifact's signature is the service's to make (D892(a))", ErrReviewSignature, credentialsServiceURLEnv)
	}
	job, err := r.jobName()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrReviewSignature, r.redact(err.Error()))
	}
	return ServiceSigner{
		URL:        url,
		IDToken:    func(ctx context.Context) (string, error) { return metadataIDToken(ctx, r.d.Env, url) },
		RunIDToken: r.runIDToken,
		Job:        job,
	}, nil
}

// ReviewKeyValue is one field of an artifact as a reader compares it: the
// check the verifier makes and the field it read. It is exported because the
// coordinator's own refusal names it.
type ReviewKeyValue struct {
	Check string
	Want  string
	Got   string
}

// VerifyReviewArtifact is the verifier's half of D892(a): it reads the
// artifact, checks the signature BEFORE the verdict is read at all, and then
// makes the four checks the signature alone does not (spec §6) --
//
//   - review_run is the review run the caller launched (the nonce), so a
//     signature lifted from one review is not a verdict about another;
//   - repository and pr are that pull request's, so an artifact of another
//     pull request cannot be passed off as this one's;
//   - head_sha equals the pull request's current head, so a validly signed
//     verdict from an earlier review of the SAME pull request -- a replay --
//     is refused rather than acted on.
//
// An artifact that does not verify is never a ship, and neither is one whose
// sidecar is absent: a missing signature is a refusal and not a default. The
// signature check is the service's (SignatureVerifier), because the frame it
// covers is the service's own construction from the two identities it verified
// (D1073) and a second spelling here would be a check nobody's key is under.
func VerifyReviewArtifact(ctx context.Context, data []byte, signature string, want ReviewExpectation, v SignatureVerifier) (ReviewArtifact, error) {
	var art ReviewArtifact
	if strings.TrimSpace(signature) == "" {
		return art, fmt.Errorf("%w: the artifact has no signature beside it (%s is absent), and an unsigned verdict is not one to act on", ErrReviewSignature, ReviewSignName)
	}
	if v == nil {
		return art, fmt.Errorf("%w: no credentials service is available to verify with, and a signature nothing checks is no signature", ErrReviewSignature)
	}
	art, err := DecodeReviewArtifact(data)
	if err != nil {
		return ReviewArtifact{}, err
	}
	digest := sha256Hex(string(data))
	if err := v.Verify(ctx, digest, strings.TrimSpace(signature)); err != nil {
		return ReviewArtifact{}, fmt.Errorf("%w: %v", ErrReviewSignature, err)
	}
	for _, c := range []ReviewKeyValue{
		{Check: "review_run", Want: want.ReviewRun, Got: art.ReviewRun},
		{Check: "repository", Want: want.Repository, Got: art.Repository},
		{Check: "pr", Want: fmt.Sprint(want.PR), Got: fmt.Sprint(art.PR)},
		{Check: "head_sha", Want: want.HeadSHA, Got: art.HeadSHA},
	} {
		if c.Want != c.Got {
			return ReviewArtifact{}, fmt.Errorf("%w: the artifact's %s is %q and this review's is %q: a signature over another review, another pull request or an earlier head is a valid signature over the wrong object", ErrReviewArtifact, c.Check, c.Got, c.Want)
		}
	}
	return art, nil
}

// DecodeReviewArtifact reads one artifact. It is strict and it closes every
// vocabulary the shape has: an unknown field, a version this reader does not
// read, a missing required field, a verdict that is not one of the three, or a
// severity, class or observed word outside its set is refused rather than
// passed on. A reader that accepted any of these would be a coordinator acting
// on a word nobody defined.
func DecodeReviewArtifact(data []byte) (ReviewArtifact, error) {
	var art ReviewArtifact
	if len(bytes.TrimSpace(data)) == 0 {
		return art, fmt.Errorf("%w: the object is empty", ErrReviewArtifact)
	}
	if err := decodeReviewStrict(data, &art); err != nil {
		return art, fmt.Errorf("%w: %v", ErrReviewArtifact, err)
	}
	if art.SchemaVersion != ReviewArtifactVersion {
		return art, fmt.Errorf("%w: this object is schema_version %d and this reader reads %d", ErrReviewArtifact, art.SchemaVersion, ReviewArtifactVersion)
	}
	for _, f := range []struct {
		name, value string
	}{
		{"review_run", art.ReviewRun},
		{"repository", art.Repository},
		{"head_sha", art.HeadSHA},
		{"base", art.Base},
		{"verdict", art.Verdict},
		{"overview", art.Overview},
		{"security", art.Security},
		{"performance", art.Performance},
		{"test_coverage", art.TestCoverage},
		{"reviewer.account", art.Reviewer.Account},
		{"reviewer.model", art.Reviewer.Model},
	} {
		if strings.TrimSpace(f.value) == "" {
			return art, fmt.Errorf("%w: the artifact carries no %s", ErrReviewArtifact, f.name)
		}
	}
	if art.CreatedAt.IsZero() {
		return art, fmt.Errorf("%w: the artifact carries no created_at", ErrReviewArtifact)
	}
	if art.CreatedAt.Location() != time.UTC {
		return art, fmt.Errorf("%w: created_at is not in UTC, and a reader of a copy has no run record to compare a local timestamp against", ErrReviewArtifact)
	}
	if !slices.Contains(reviewVerdicts, art.Verdict) {
		return art, fmt.Errorf("%w: verdict is %q, and this shape's own words are %s", ErrReviewArtifact, art.Verdict, strings.Join(reviewVerdicts, ", "))
	}
	if art.PR <= 0 {
		return art, fmt.Errorf("%w: the artifact names no pull request", ErrReviewArtifact)
	}
	if art.MutationChecks == nil {
		art.MutationChecks = []ReviewMutationCheck{}
	}
	for i, m := range art.MutationChecks {
		if !slices.Contains(reviewObserved, m.Observed) {
			return art, fmt.Errorf("%w: mutation_checks[%d].observed is %q, and the four words are %s", ErrReviewArtifact, i, m.Observed, strings.Join(reviewObserved, ", "))
		}
	}
	if art.Strengths == nil {
		art.Strengths = []string{}
	}
	if art.Issues == nil {
		art.Issues = []ReviewIssue{}
	}
	for i, is := range art.Issues {
		if err := checkReviewIssue(is); err != nil {
			return art, fmt.Errorf("%w: issues[%d]: %v", ErrReviewArtifact, i, err)
		}
	}
	return art, nil
}

// checkReviewIssue holds one issue to the vocabularies and the required fields
// the shape has, the same rules whether it arrived in an answer or in a stored
// object.
func checkReviewIssue(is ReviewIssue) error {
	switch {
	case is.Severity == "":
		return errors.New("no severity")
	case !slices.Contains(reviewSeverity, is.Severity):
		return fmt.Errorf("severity is %q, and the three words are %s", is.Severity, strings.Join(reviewSeverity, ", "))
	case !slices.Contains(reviewClasses, is.Class):
		return fmt.Errorf("class is %q, and the closed set is %s", is.Class, strings.Join(reviewClasses, ", "))
	}
	for _, f := range []struct{ name, value string }{
		{"file", is.File}, {"summary", is.Summary}, {"fix", is.Fix}, {"evidence", is.Evidence},
	} {
		if strings.TrimSpace(f.value) == "" {
			return fmt.Errorf("no %s", f.name)
		}
	}
	if is.Line < 0 {
		return errors.New("line is negative")
	}
	for _, axis := range []struct {
		name string
		v    int
	}{{"likelihood", is.Likelihood}, {"impact", is.Impact}, {"fix_cost", is.FixCost}} {
		if axis.v < 0 || axis.v > 5 {
			return fmt.Errorf("%s is %d, and an anchored axis is 1 to 5", axis.name, axis.v)
		}
	}
	return nil
}

// decodeReviewAnswer reads the reviewer's answer: the model-written fields of
// the artifact, and none of its provenance.
//
// It refuses an answer that stopped early, which is what D886 is about: a
// required key that is absent is a clipped result, and a clipped result is a
// failed review and never a short ship. Go's own decode cannot see the
// difference -- an absent string and an empty one are both "" -- so the keys
// are checked before the struct is read.
//
// It reads the structured output ONLY, and never a fenced block in the text:
// the review job's result IS this shape (D890), and a fallback would be the
// in-run reader's leniency granted to a document that has a signature. A
// result with nothing in it is a failed review, which is what
// TestUnreadableReviewArtifactEndsTheRunInvalid pins.
func readReviewAnswer(res agent.Result) (reviewAnswer, error) {
	if len(bytes.TrimSpace(res.Structured)) == 0 {
		return reviewAnswer{}, fmt.Errorf("%w: the stage's result carries no structured output, and the review job's answer is the artifact's own shape: a result with none is a failed review, never a shorter one", ErrReviewArtifact)
	}
	return decodeReviewAnswer(res.Structured)
}

// decodeReviewAnswer reads an answer's bytes, with the presence check D886
// needs.
func decodeReviewAnswer(data []byte) (reviewAnswer, error) {
	var ans reviewAnswer
	var present map[string]json.RawMessage
	if err := json.Unmarshal(data, &present); err != nil {
		return ans, fmt.Errorf("%w: the answer is not readable JSON: %v", ErrReviewArtifact, err)
	}
	for _, key := range []string{"verdict", "overview", "strengths", "issues", "security", "performance", "test_coverage"} {
		if _, ok := present[key]; !ok {
			return ans, fmt.Errorf("%w: the answer carries no %s, so it stopped before it wrote one: a review whose output was cut short is a failed review and never a shorter one", ErrReviewArtifact, key)
		}
	}
	if err := decodeReviewStrict(data, &ans); err != nil {
		return ans, fmt.Errorf("%w: %v", ErrReviewArtifact, err)
	}
	if !slices.Contains(reviewVerdicts, ans.Verdict) {
		return ans, fmt.Errorf("%w: verdict is %q, and this review's own words are %s", ErrReviewArtifact, ans.Verdict, strings.Join(reviewVerdicts, ", "))
	}
	if strings.TrimSpace(ans.Overview) == "" || strings.TrimSpace(ans.Security) == "" ||
		strings.TrimSpace(ans.Performance) == "" || strings.TrimSpace(ans.TestCoverage) == "" {
		return ans, fmt.Errorf("%w: a section the review's own shape requires is empty", ErrReviewArtifact)
	}
	for i, is := range ans.Issues {
		if err := checkReviewIssue(is); err != nil {
			return ans, fmt.Errorf("%w: issues[%d]: %v", ErrReviewArtifact, i, err)
		}
	}
	return ans, nil
}

// decodeReviewStrict decodes one shape, refusing any field the shape does not
// know: a format every reader here decodes strictly, because an ignored field
// is a word one side wrote and the other silently dropped.
func decodeReviewStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("the object carries more than one JSON value")
	}
	return nil
}

// reviewJob reports whether this run IS the independent review job, which is
// the whole of what reviewmode.go branches on: the stage's schema, the reader
// of its result, and whether the artifact is written at all.
func (r *run) reviewJob() bool {
	return envLookup(r.d.Env, EnvReviewJob) != ""
}

// reviewStageSchema is the JSON Schema review mode's stage is started with:
// the artifact's own shape for the review job, and VerdictSchema for every
// other review stage -- today's `fugaro run --pr N --recipe review-only`, and
// a coding run's review rounds -- so that path is byte-for-byte what it is
// (D890, F23).
func (r *run) reviewStageSchema() string {
	if r.reviewJob() {
		return ReviewArtifactSchema
	}
	return VerdictSchema
}

// reviewStageRequest is the request review mode's stage is started with. It is
// one expression rather than a literal inside reviewMode so the schema the
// stage is asked for is a fact a test reads (TestReviewJobStageIsNotParsedAs
// AnInRunVerdict), and so the two flags that keep the reviewed head's own
// configuration out of the session stay with it (D894(a)).
func (r *run) reviewStageRequest(prompt string) agent.Request {
	return agent.Request{Prompt: prompt, SessionID: agent.NewSessionID(), JSONSchema: r.reviewStageSchema(),
		SettingSources: agent.ReviewSettingSources, StrictMCPConfig: true}
}

// writeReviewArtifact writes the review job's three objects and records the
// review, in the order that leaves nothing behind on a refusal:
//
//  1. the reviewer's answer is read, and the artifact is built and REDACTED
//     before anything else happens -- the redacted bytes are the signed bytes
//     (spec §3), so a redaction applied later would break every signature over
//     them;
//  2. the digest of those exact bytes is signed by the credentials service.
//     A sign call that fails ends the run, and NOTHING has been written: no
//     object, no record entry. The service spends its one-signature mark, and
//     the remedy is the designed one -- the head is reviewed again (D892(a),
//     §5.6);
//  3. only then are review.json, review.md and the sidecar uploaded, through
//     the run's own artifact path (uploadArtifacts), and the record is written
//     with exactly one reviews[] entry whose tier is TierIndependent and whose
//     verdict is one of the record's own two words (O4). A write that gets
//     this far ends the run reviewed: the artifact is the job's deliverable,
//     so nothing else is left to do and nothing writes a second ending beside
//     this one.
func (r *run) writeReviewArtifact(ctx context.Context, res agent.Result) error {
	head, err := r.reviewJobHead()
	if err != nil {
		return err
	}
	answer, err := readReviewAnswer(res)
	if err != nil {
		return err
	}
	art := ReviewArtifact{
		SchemaVersion: ReviewArtifactVersion,
		ReviewRun:     r.rec.RunID,
		CreatedAt:     r.d.Now().UTC().Truncate(time.Second),
		Repository:    r.d.Store.Slug(),
		PR:            head.Number,
		HeadSHA:       head.SHA,
		Base:          r.reviewBase(head),
		Reviewer: ReviewReviewer{
			Account: selectedName(r.spec.OAuthToken),
			Model:   r.stageModel("review"),
			Effort:  r.reviewEffort(),
		},
		Verdict:        answer.Verdict,
		Overview:       answer.Overview,
		Strengths:      answer.Strengths,
		Issues:         answer.Issues,
		Security:       answer.Security,
		Performance:    answer.Performance,
		TestCoverage:   answer.TestCoverage,
		MutationChecks: []ReviewMutationCheck{},
	}
	secrets := r.secretList()
	art = redactReviewArtifact(art, secrets)
	data, err := json.Marshal(art)
	if err != nil {
		return fmt.Errorf("%w: the artifact could not be built: %v", ErrReviewArtifact, err)
	}
	md := agent.Redact(string(art.Markdown()), secrets)

	signer, err := r.reviewSigner()
	if err != nil {
		return err
	}
	sig, err := signer.Sign(ctx, sha256Hex(string(data)))
	if err != nil {
		return err
	}
	if !reviewSignatureRE.MatchString(sig) {
		return fmt.Errorf("%w: the service's answer carries no signature of the shape this code stores", ErrReviewSignature)
	}

	set, err := r.stageReviewObjects([][]byte{data, []byte(sig + "\n"), []byte(md)})
	if err != nil {
		return err
	}
	files, err := r.uploadArtifacts(ctx, set)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrReviewArtifact, r.redact(err.Error()))
	}
	r.rec.Artifacts = files
	r.rec.HeadSHA = head.SHA
	// The number IS the link (D1058: a review job on PR N joins the pull
	// request's chain, which the chain's own reader walks by number). The URL
	// stays empty because this path has none to record: the provider answered
	// FetchPRHead, which carries a head and not a pull request, and a second
	// API call at the moment of writing would be a call the artifact does not
	// need and a run can fail on.
	r.rec.PR = &runstore.PRRef{Number: head.Number}
	r.rec.Reviews = []runstore.ReviewSummary{{
		Round: 1, Tier: runstore.TierIndependent,
		Verdict:  r.recordVerdict(art.Verdict),
		Findings: len(art.Issues),
		Head:     head.SHA,
	}}
	// The artifact IS the job's whole deliverable, so writing it is what ends
	// the run reviewed: there is no comment to post and no pull request to
	// open or edit (spec §2), and a caller left to set the ending beside this
	// one would be a second place that can disagree with it. Only a refusal
	// is left to the caller, which is what finishReviewJob routes.
	r.rec.Status, r.rec.Outcome = runstore.StatusSucceeded, runstore.OutcomeReviewed
	r.skipPR = true
	r.storeReviewEnded(ctx)
	return nil
}

// reviewEffort is the effort the artifact's reviewer block records: the review
// stage's own level's, read through the same ladder the stage reads it through
// (reviewStageOf, resolvedLevel) rather than a second answer beside it.
func (r *run) reviewEffort() string {
	s, ok := reviewStageOf("review")
	if !ok {
		return ""
	}
	return resolvedLevel(r.levels, s.level).Effort
}

// recordVerdict is the word the run RECORD carries for an artifact's verdict
// (O4): the record's own two words, unchanged. A review that ships is a ship,
// and both of the artifact's other outcomes are `changes` -- the record has no
// third word and a `reject` must never be filed as a pass. The artifact's own
// word stays in the artifact, which is the authority on the outcome.
func (r *run) recordVerdict(verdict string) string {
	if verdict == "ship" {
		return "ship"
	}
	return "changes"
}

// reviewJobHead is the one pull request the artifact is about. The review job
// reviews exactly one head: its whole shape -- one pr, one head_sha, one
// verdict -- is a statement about one commit, and a review job launched
// against several would have to write several artifacts whose signatures are
// one mark's.
func (r *run) reviewJobHead() (reviewHead, error) {
	if len(r.reviewHeads) != 1 {
		return reviewHead{}, fmt.Errorf("%w: the review job resolved %d pull requests, and its artifact is one verdict about one head", ErrReviewArtifact, len(r.reviewHeads))
	}
	return r.reviewHeads[0], nil
}

// stageReviewObjects writes the three objects into the run's own staging
// directory, in the order they are recorded, with the discipline every staged
// artifact is written with: private, exclusive, and never through a symlink.
func (r *run) stageReviewObjects(objects [][]byte) (artifactSet, error) {
	dir := filepath.Join(r.d.StateDir, artifactStageDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return artifactSet{}, fmt.Errorf("%w: %v", ErrReviewArtifact, err)
	}
	names := []string{ReviewArtifactName, ReviewSignName, ReviewMarkdownName}
	set := artifactSet{dir: dir, files: make([]staged, 0, len(objects))}
	for i, data := range objects {
		path := filepath.Join(dir, names[i])
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|oNoFollow, 0o600)
		if err != nil {
			return artifactSet{}, fmt.Errorf("%w: %v", ErrReviewArtifact, err)
		}
		if _, err := f.Write(data); err != nil {
			f.Close()
			return artifactSet{}, fmt.Errorf("%w: %v", ErrReviewArtifact, err)
		}
		if err := f.Close(); err != nil {
			return artifactSet{}, fmt.Errorf("%w: %v", ErrReviewArtifact, err)
		}
		set.files = append(set.files, staged{
			name: names[i], path: path, sha256: sha256Hex(string(data)),
			contentType: reviewContentType(names[i]),
		})
	}
	return set, nil
}

// reviewContentType is the recorded content type of one of the three objects.
func reviewContentType(name string) string {
	if name == ReviewMarkdownName {
		return "text/markdown"
	}
	return "application/json"
}

// redactReviewArtifact replaces every secret the run holds in the artifact's
// reviewer prose, before the digest is taken. Severity, class and the line are
// the runner's vocabularies and hold no prose; everything a model wrote does.
func redactReviewArtifact(art ReviewArtifact, secrets []string) ReviewArtifact {
	red := agent.RedactFunc(secrets)
	art.Overview = red(art.Overview)
	art.Security = red(art.Security)
	art.Performance = red(art.Performance)
	art.TestCoverage = red(art.TestCoverage)
	for i := range art.Strengths {
		art.Strengths[i] = red(art.Strengths[i])
	}
	for i := range art.Issues {
		art.Issues[i].Summary = red(art.Issues[i].Summary)
		art.Issues[i].Fix = red(art.Issues[i].Fix)
		art.Issues[i].Evidence = red(art.Issues[i].Evidence)
		art.Issues[i].File = red(art.Issues[i].File)
	}
	for i := range art.MutationChecks {
		art.MutationChecks[i].Applied = red(art.MutationChecks[i].Applied)
	}
	return art
}

// Markdown renders the artifact for a person: the same thing, section for
// section, generated from the fields and never a second telling of them. It
// exists so a reader never has to read JSON to understand a review -- and it
// is never published verbatim: what a coordinator posts is a bounded,
// re-redacted summary of its own (O5, spec §6).
func (a ReviewArtifact) Markdown() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Independent review of #%d at %s\n\n", a.PR, a.HeadSHA)
	fmt.Fprintf(&b, "Verdict: **%s**\n\n", a.Verdict)
	fmt.Fprintf(&b, "Reviewed by %s on %s (%s), against `%s`.\n\n", a.Reviewer.Account, a.Reviewer.Model, a.Reviewer.Effort, a.Base)
	b.WriteString("## Overview\n\n" + a.Overview + "\n\n")
	b.WriteString("## Strengths\n\n")
	for _, s := range a.Strengths {
		b.WriteString("- " + s + "\n")
	}
	b.WriteString("\n## Issues to flag\n\n")
	if len(a.Issues) == 0 {
		b.WriteString("None.\n\n")
	}
	for i, is := range a.Issues {
		fmt.Fprintf(&b, "### %d. %s (%s) — %s", i+1, is.Severity, is.Class, is.File)
		if is.Line > 0 {
			fmt.Fprintf(&b, ":%d", is.Line)
		}
		b.WriteString("\n\n" + is.Summary + "\n\n")
		b.WriteString("**Fix:** " + is.Fix + "\n\n")
		b.WriteString("**Evidence:** " + is.Evidence + "\n\n")
	}
	if len(a.MutationChecks) > 0 {
		b.WriteString("## Mutation checks\n\n")
		for _, m := range a.MutationChecks {
			fmt.Fprintf(&b, "- `%s` in %s: %s (%s)", m.Test, m.File, m.Observed, m.Applied)
			if m.Reverted {
				b.WriteString(", reverted")
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("## Security\n\n" + a.Security + "\n\n")
	b.WriteString("## Performance\n\n" + a.Performance + "\n\n")
	b.WriteString("## Test coverage\n\n" + a.TestCoverage + "\n\n")
	b.WriteString("## Verdict\n\n" + a.Verdict + "\n")
	return []byte(b.String())
}

// finishReviewJob is the review job's end: the artifact is written (and
// signed) or the run FAILS with the reader's or the service's own reason. It
// never synthesizes a finding -- a blocker sentence about a parser, stored
// beside real findings, is a code finding nobody wrote (spec §3) -- and there
// is no loop here to fix what could not be read.
//
// It writes no comment and no pull request on any path: the job writes nothing
// to the pull request, and the coordinator owns every word of what reaches it
// (spec §2, O5).
func (r *run) finishReviewJob(ctx context.Context, res agent.Result) {
	if err := r.writeReviewArtifact(ctx, res); err != nil {
		r.endReviewInvalidVerdict(ctx, r.redact(err.Error()))
	}
}
