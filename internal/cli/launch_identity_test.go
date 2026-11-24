package cli

// A launch asks the installation's credentials service for its run's ticket
// (LM6 of docs/launcher-does-not-mint): these tests hold every way in to that
// ask, to R7's bridge, to the fix a refusal carries, to LM8's gate and to the
// resident service's own ask.
//
// The service is a TLS server and the URL the installation's config carries is
// its own, because the loader holds credentials_service.url to https with a
// host and nothing else: a test cannot point an installation at a plain server,
// and the config the launch reads is a real one. The transport that trusts this
// server's certificate is installed as the process's for the length of the test
// — the call's own client is built from http.DefaultTransport (slipClient) —
// and the identity mint is stubbed, because no test may run gcloud.
//
// The resident half is the one test that mints nothing at all: a container
// presents the token of the account it runs as, which it reads from the
// metadata server, so that test stands a stub metadata server up over the env
// var the mint reads (GCE_METADATA_HOST) and lets residentIdentity run for real.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

const (
	// ticketValue is what the fixture's service issues where its ticket route
	// mints. It is one printable value within the bounds a run reads it back
	// under, and it is not a credential: nothing signs it and no running
	// installation would take one.
	ticketValue = "fake-ticket-not-a-real-credential-0000001"
	// ticketIdentity is what the stubbed mint answers: the launcher's own
	// Google ID token as this test has it. It is not a credential either.
	ticketIdentity = "fake-launcher-identity-not-a-real-credential"
	// residentTicketIdentity is what the stub metadata server answers: the
	// token of the account the resident service runs as. Neither is it.
	residentTicketIdentity = "fake-resident-service-identity-not-a-real-credential"
	// ticketRun is the run these tests launch.
	ticketRun = "20261124-090000-abcd"
)

// releaseIdentity is a base image whose runner knows the identity claim
// (identitySince): the job image a launch that holds a ticket needs, and the
// image every launch of these tests but the gate's own must be built from
// (identityGate).
var releaseIdentity = "ghcr.io/dimipaun/fugaro-base:" + identitySince

// ticketAnswer is one route's answer: a status and a body. A status of zero is
// a 200.
type ticketAnswer struct {
	Status int
	Body   string
}

// ticketAsked is one call the service received: the path, the credential it was
// presented and the body it carried.
type ticketAsked struct {
	Path, Authorization, Body string
}

// launchService is an installation's credentials service as a launch test has
// it.
type launchService struct {
	*httptest.Server
	answers map[string]ticketAnswer

	mu    sync.Mutex
	calls []ticketAsked
}

// newLaunchService starts one and returns it. A route the answers do not name
// is answered 404, which is what a service of the release behind answers an
// unknown path — C1 pins that answer as R7's third trigger, so the zero answers
// map is a service with no ticket route at all.
func newLaunchService(t *testing.T, answers map[string]ticketAnswer) *launchService {
	t.Helper()
	s := &launchService{answers: answers}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.Close)

	prev := http.DefaultTransport
	http.DefaultTransport = s.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = prev })
	return s
}

// serviceURL is the address this installation's config carries for the service:
// the same server, over the https the loader holds the block to.
func (s *launchService) serviceURL() string {
	return strings.Replace(s.URL, "http://", "https://", 1)
}

func (s *launchService) asked() []ticketAsked {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ticketAsked(nil), s.calls...)
}

func (s *launchService) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.calls = append(s.calls, ticketAsked{Path: r.URL.Path, Authorization: r.Header.Get("Authorization"), Body: string(body)})
	ans, known := s.answers[r.URL.Path]
	s.mu.Unlock()
	if !known {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if ans.Status != 0 {
		w.WriteHeader(ans.Status)
	}
	_, _ = io.WriteString(w, ans.Body)
}

// ticketAnswers is what the service answers where its ticket route mints.
func ticketAnswers(expires time.Time) map[string]ticketAnswer {
	return map[string]ticketAnswer{
		identitywire.TicketPath: {Body: `{"ticket":"` + ticketValue + `","expires_at":"` + expires.UTC().Format(time.RFC3339) + `"}`},
	}
}

// ticketNotServed is the service's own answer for a revision whose identity
// half is not configured (R7's second trigger).
var ticketNotServed = ticketAnswer{Status: http.StatusServiceUnavailable, Body: `{"error":"` + identitywire.RefusalNotServed + `"}`}

// ticketRefused is the answer of a service that admits the caller and refuses
// the ask itself.
var ticketRefused = ticketAnswer{Status: http.StatusForbidden, Body: `{"error":"` + identitywire.RefusalIdentityRefused + `"}`}

// ticketAskOf reads one recorded call as this contract's ask.
func ticketAskOf(t *testing.T, a ticketAsked) identitywire.TicketRequest {
	t.Helper()
	var req identitywire.TicketRequest
	if err := json.Unmarshal([]byte(a.Body), &req); err != nil {
		t.Fatalf("the ask the service received is not this contract's: %v (%q)", err, a.Body)
	}
	return req
}

// ticketLaunchRig is one launch on an installation whose config names a
// credentials service.
type ticketLaunchRig struct {
	f    *launchBudgetFixture
	svc  *launchService
	env  *cloudEnv
	spec *task.Spec
	// minted is how many times the launch minted the identity it presents, and
	// waits is the waits it made between the attempts of an ask nothing
	// answered.
	minted int
	waits  []time.Duration
}

// ticketWorld builds one launch's world: the installation, its job image named
// by the build record at base, and the credentials service at url — with svc
// standing there where the test has a server of its own, and nil where the
// answer is something else (an address nothing answers, an installation with no
// service at all). url is "" for an installation whose config carries no block,
// which is R7's first trigger.
func ticketWorld(t *testing.T, base, url string, svc *launchService) *ticketLaunchRig {
	t.Helper()
	f := newLaunchBudget(t, "enforce")
	writeBuildRecord(t, f.cloudFixture, appSlug, "web", base)
	r := &ticketLaunchRig{f: f, svc: svc}
	if url != "" {
		f.appendConfig(t, "credentials_service:\n  url: "+url+"\n")
	}
	prevIdentity, prevSleep, prevSelf := launchIdentity, ticketSleep, selfCommand
	prevAsk := launchAskTicket
	launchAskTicket = func(ctx context.Context, i *launchIssuer, req identitywire.TicketRequest) (identitywire.TicketResponse, error) {
		return i.askRetry(ctx, req)
	}
	launchIdentity = func(context.Context, string) (string, error) {
		r.minted++
		return ticketIdentity, nil
	}
	ticketSleep = func(_ context.Context, d time.Duration) error {
		r.waits = append(r.waits, d)
		return nil
	}
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() {
		launchIdentity, ticketSleep, selfCommand, launchAskTicket = prevIdentity, prevSleep, prevSelf, prevAsk
	})
	r.env = fileEnv(t, f.cloudFixture)
	r.spec = raceSpec(t, r.env)
	return r
}

// servedTicketWorld is ticketWorld over a service this test stands up, with the
// installation pointed at it.
func servedTicketWorld(t *testing.T, base string, answers map[string]ticketAnswer) *ticketLaunchRig {
	t.Helper()
	svc := newLaunchService(t, answers)
	return ticketWorld(t, base, svc.serviceURL(), svc)
}

// launch runs the fixture's launch and answers the environment its execution
// was handed.
func (r *ticketLaunchRig) launch(t *testing.T) (map[string]string, error) {
	t.Helper()
	var atLaunch map[string]string
	r.f.run.OnRun = func(c gcpfake.RunCall) { atLaunch = c.Env }
	_, err := launchRun(context.Background(), r.env, appSlug, r.spec, time.Now())
	return atLaunch, err
}

// ticketObject is the run's token object in env's bucket, "" when the launch
// wrote none. A launch that holds a ticket writes none at all (R7).
func ticketObject(t *testing.T, env *cloudEnv, run string) string {
	t.Helper()
	data, _, err := env.bucket.Read(context.Background(), token.ObjectKey(appSlug, run))
	if err != nil {
		return ""
	}
	return string(data)
}

// ticketLaunchRecord is the run's launch record and whether one exists, which
// is the write R5's order is about.
func ticketLaunchRecord(t *testing.T, env *cloudEnv, run string) (*runstore.Launch, bool) {
	t.Helper()
	l, err := runstore.Open(env.bucket.Bucket, appSlug, run).ReadLaunch(context.Background())
	return l, err == nil
}

// TestLaunchAsksTheIssuerAndSignsNothing: a launch on an installation whose
// credentials service serves the ticket route asks it, presents the launcher's
// own identity, sends the contract's ask built from the run's own parts, puts
// the ticket on the execution under the variable the run claims it by, and
// writes nothing else at all — with this machine's own signer never asked for a
// token.
func TestLaunchAsksTheIssuerAndSignsNothing(t *testing.T) {
	r := servedTicketWorld(t, releaseIdentity, ticketAnswers(time.Now().Add(30*time.Minute)))
	executed, err := r.launch(t)
	// The red step first, and before the error is judged: a launch that minted
	// locally has nothing to say about the rest of this test, and the one fact
	// that tells the two apart is this machine's own signer never being asked.
	if calls := r.f.iam.SignCalls(); len(calls) != 0 {
		t.Fatalf("the launch minted %d tokens with this machine's own signer (%v), want none: the run's identity is the service's ticket (LM6's red step)", len(calls), calls)
	}
	if err != nil {
		t.Fatalf("the launch the service serves a ticket for: %v", err)
	}

	asked := r.svc.asked()
	if len(asked) != 1 || asked[0].Path != identitywire.TicketPath {
		t.Fatalf("the service was asked %v, want the ticket route once", asked)
	}
	if asked[0].Authorization != "Bearer "+ticketIdentity {
		t.Fatalf("the ask was presented %q, want this launcher's own Google ID token", asked[0].Authorization)
	}
	req := ticketAskOf(t, asked[0])
	if req.Slug != appSlug || req.Run != r.spec.RunID {
		t.Fatalf("the ask names %s/%s, want the run this launch is starting (%s/%s)", req.Slug, req.Run, appSlug, r.spec.RunID)
	}
	if req.Account == "" || !strings.HasSuffix(req.Account, "@proj-1234.iam.gserviceaccount.com") {
		t.Fatalf("the ask names the account %q, want the job account this run's execution runs as", req.Account)
	}
	if req.Branch != "fugaro/"+r.spec.RunID {
		t.Fatalf("the ask names the branch %q, want the run's own fugaro/%s", req.Branch, r.spec.RunID)
	}
	if req.MaxRuntimeSeconds <= 0 {
		t.Fatalf("the ask asks for a runtime of %d seconds, want the window the sealed token would have been minted with", req.MaxRuntimeSeconds)
	}
	if req.Attempt != "" {
		t.Fatalf("the ask names the attempt %q, and a person's launch re-issues none (D3136)", req.Attempt)
	}
	if req.Job != "" {
		t.Fatalf("the ask names the job %q, and a Cloud Run launch creates no Batch job (R18)", req.Job)
	}

	if got := executed[identitywire.TicketEnv]; got != ticketValue {
		t.Fatalf("the execution carries %s=%q, want the ticket the service issued", identitywire.TicketEnv, got)
	}
	if _, ok := executed[runner.BudgetTokenEnv]; ok {
		t.Fatalf("a launch that holds a ticket handed its execution the seal marker %s: a run would have two identities", runner.BudgetTokenEnv)
	}
	if raw := ticketObject(t, r.env, r.spec.RunID); raw != "" {
		t.Fatalf("a launch that holds a ticket wrote a token object (%d bytes): the ticket is the whole of what the run's identity travels on (R7)", len(raw))
	}
	if _, ok := ticketLaunchRecord(t, r.env, r.spec.RunID); !ok {
		t.Fatal("the launch was not recorded at the run's own node")
	}
}

// TestLaunchAsksBeforeItWritesTheRun is R5: the ask is made before this launch
// takes its claim, before it writes anything that names the run id where another
// launcher reads, and before any object of the run exists.
func TestLaunchAsksBeforeItWritesTheRun(t *testing.T) {
	r := servedTicketWorld(t, releaseIdentity, ticketAnswers(time.Now().Add(30*time.Minute)))
	var askedAt, objectAt int
	var recordAt bool
	prev := launchHooks.beforeClaim
	launchHooks.beforeClaim = func() {
		askedAt = len(r.svc.asked())
		objectAt = len(ticketObject(t, r.env, r.spec.RunID))
		_, recordAt = ticketLaunchRecord(t, r.env, r.spec.RunID)
	}
	t.Cleanup(func() { launchHooks.beforeClaim = prev })

	if _, err := r.launch(t); err != nil {
		t.Fatalf("the launch: %v", err)
	}
	if askedAt != 1 {
		t.Fatalf("the service had been asked %d time(s) by the moment the run's claim was taken, want the ask already made (R5)", askedAt)
	}
	if objectAt != 0 {
		t.Fatalf("a token object of the run existed (%d bytes) when the claim was taken", objectAt)
	}
	if recordAt {
		t.Fatal("the run's launch record existed when the claim was taken, and the ask is what has to precede it (R5)")
	}
	if _, ok := ticketLaunchRecord(t, r.env, r.spec.RunID); !ok {
		t.Fatal("the launch was not recorded at the run's own node")
	}
}

// TestLaunchBridgesOnlyOnTheThreeAnswers: R7's three answers — the service's own
// "this revision holds no issuer", a service of the release behind answering an
// unknown path, and an installation with no service block at all — each end in
// the local mint this command made before the issuer existed, with the sealed
// object and the old marker on the execution. Every other answer is a refusal,
// and a refusal is never a bridge.
func TestLaunchBridgesOnlyOnTheThreeAnswers(t *testing.T) {
	cases := []struct {
		name         string
		answers      map[string]ticketAnswer
		wantsRefusal bool
	}{
		{name: "the service holds no issuer", answers: map[string]ticketAnswer{identitywire.TicketPath: ticketNotServed}},
		{name: "the service is a release behind", answers: map[string]ticketAnswer{}},
		{name: "no service is configured", answers: nil},
		{name: "a refusal is not a bridge", answers: map[string]ticketAnswer{identitywire.TicketPath: ticketRefused}, wantsRefusal: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var r *ticketLaunchRig
			if tc.answers == nil {
				// An installation with no service block at all: R7's first
				// answer, and the one the loader holds to the block's absence.
				r = ticketWorld(t, releaseIdentity, "", nil)
			} else {
				r = servedTicketWorld(t, releaseIdentity, tc.answers)
			}
			executed, err := r.launch(t)
			if tc.wantsRefusal {
				if err == nil {
					t.Fatal("a launch the service refused went through: a refusal under a served route is never a local mint")
				}
				if n := len(r.f.iam.SignCalls()); n != 0 {
					t.Fatalf("a refused ask signed %d tokens locally, want none (R7)", n)
				}
				if n := len(r.f.run.Executions()); n != 0 {
					t.Fatalf("a refused launch started %d execution(s)", n)
				}
				return
			}
			if err != nil {
				t.Fatalf("the launch this answer must bridge for: %v", err)
			}
			if n := len(r.f.iam.SignCalls()); n != 1 {
				t.Fatalf("the bridge minted %d tokens with this machine's own signer, want the run's one", n)
			}
			if _, ok := executed[runner.BudgetTokenEnv]; !ok {
				t.Fatalf("a bridged launch handed its execution no seal marker, so its run has no identity at all")
			}
			if got := executed[identitywire.TicketEnv]; got != "" {
				t.Fatalf("a bridged launch handed its execution %s=%q, and there is no ticket on this answer", identitywire.TicketEnv, got)
			}
			if raw := ticketObject(t, r.env, r.spec.RunID); raw == "" {
				t.Fatal("a bridged launch wrote no token object: the local mint is the whole of what it falls back to")
			}
			if r.svc == nil {
				return
			}
			if asked := r.svc.asked(); len(asked) != 1 || asked[0].Path != identitywire.TicketPath {
				t.Fatalf("the service was asked %v, want the ticket route asked before the bridge took over", asked)
			}
		})
	}
}

// TestLaunchRefusesWithTheFixWhenIssuerUnreachable: an installation whose
// service does not answer is not an installation without one. The launch makes
// a bounded retry (R1) and then fails with the reason and the fix — and it
// signs nothing, writes nothing and starts nothing on the way.
func TestLaunchRefusesWithTheFixWhenIssuerUnreachable(t *testing.T) {
	// The address is the installation's own and nothing answers it: the mint is
	// stubbed so that what fails is the call and not this machine's gcloud.
	r := ticketWorld(t, releaseIdentity, "https://127.0.0.1:1", nil)
	executed, err := r.launch(t)
	if err == nil {
		t.Fatal("a launch on an installation whose service does not answer went through")
	}
	if got := ExitCode(err); got != ExitRemoteError {
		t.Fatalf("exit %d for %v, want %d: a service that did not answer is not an operator's mistake", got, err, ExitRemoteError)
	}
	for _, want := range []string{"credentials service could not be reached", "fugaro doctor", "fugaro update"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal %q does not name %q", err, want)
		}
	}
	// The retry is made and bounded: one attempt, then the waits ticketWaits
	// names (R1, thirty seconds in all).
	if len(r.waits) != len(ticketWaits) {
		t.Fatalf("the launch waited %v between its attempts, want the %d waits of a bounded retry", r.waits, len(ticketWaits))
	}
	if r.minted != len(ticketWaits)+1 {
		t.Fatalf("the launch minted the identity it presents %d time(s), want one per attempt (%d)", r.minted, len(ticketWaits)+1)
	}
	if n := len(r.f.iam.SignCalls()); n != 0 {
		t.Fatalf("an unreachable service signed %d tokens locally, want none: a retry that exhausted is not a bridge (R7)", n)
	}
	if n := len(r.f.run.Executions()); n != 0 {
		t.Fatalf("the launch started %d execution(s) with no ticket and no mint", n)
	}
	if executed != nil {
		t.Fatalf("the launch handed an execution an environment: %v", executed)
	}
	if raw := ticketObject(t, r.env, r.spec.RunID); raw != "" {
		t.Fatal("a refused launch wrote a token object")
	}
	if _, ok := ticketLaunchRecord(t, r.env, r.spec.RunID); ok {
		t.Fatal("a refused launch wrote the run's launch record")
	}
}

// TestLaunchRefusalNamesAnotherCallersRun: the one refusal a person acts on by
// launching something else — the run already holds an identity record this ask
// cannot take — is told with the fix, and it is a refusal and not a bridge.
func TestLaunchRefusalNamesAnotherCallersRun(t *testing.T) {
	r := servedTicketWorld(t, releaseIdentity, map[string]ticketAnswer{identitywire.TicketPath: ticketRefused})
	executed, err := r.launch(t)
	if err == nil {
		t.Fatal("a launch the service refused for the run's own record went through")
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Fatalf("exit %d for %v, want %d: the fix is a run id nobody holds", got, err, ExitUserError)
	}
	for _, want := range []string{identitywire.RefusalIdentityRefused, "another launcher's record for the same run", task.ShortRun(ticketRun), "fugaro doctor"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal %q does not name %q", err, want)
		}
	}
	if n := len(r.f.iam.SignCalls()); n != 0 {
		t.Fatalf("a refused ask signed %d tokens locally, want none", n)
	}
	if n := len(r.f.run.Executions()); n != 0 {
		t.Fatalf("a refused launch started %d execution(s)", n)
	}
	if executed != nil {
		t.Fatalf("a refused launch handed an execution an environment: %v", executed)
	}
}

// TestBridgeWithoutTheRowRefusesWithTheFix: the bridge's own failure. An
// installation whose service answers "not served" falls back to the local mint,
// and an installation whose service issues run tickets has no fugaroTokenMinter
// row left for the people (LM14): what the operator meets is a release skew, and
// the refusal says so rather than leaving IAM's own error in its place.
func TestBridgeWithoutTheRowRefusesWithTheFix(t *testing.T) {
	r := servedTicketWorld(t, releaseIdentity, map[string]ticketAnswer{identitywire.TicketPath: ticketNotServed})
	r.f.iam.Refuse(403, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "Permission 'iam.serviceAccounts.signJwt' denied")
	executed, err := r.launch(t)
	if err == nil {
		t.Fatal("a bridged launch minted without the role the local mint needs")
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Fatalf("exit %d for %v, want %d", got, err, ExitUserError)
	}
	for _, want := range []string{"fugaro update", "fugaroTokenMinter", testSigner} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal %q does not name %q", err, want)
		}
	}
	if n := len(r.f.run.Executions()); n != 0 {
		t.Fatalf("a launch whose local mint was refused started %d execution(s)", n)
	}
	if executed != nil {
		t.Fatalf("a refused launch handed an execution an environment: %v", executed)
	}
}

// TestLaunchWithATicketRefusesAnOlderImage: LM8's gate. A run that carries a
// ticket and no sealed object stops at bootstrap with no identity at all on a
// runner that does not know the claim, so the launch is refused — after the ask,
// because the ticket is what makes the gate apply (R7) and what a run on the
// bridge is not judged by.
func TestLaunchWithATicketRefusesAnOlderImage(t *testing.T) {
	// The fixture's own build record is a release older than the claim.
	r := servedTicketWorld(t, releasePolicy, ticketAnswers(time.Now().Add(30*time.Minute)))
	executed, err := r.launch(t)
	if err == nil {
		t.Fatal("a launch holding a ticket was admitted on a runner that knows no claim: it would stop at bootstrap with no identity")
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Fatalf("exit %d for %v, want %d: the fix is the operator's own fugaro update", got, err, ExitUserError)
	}
	for _, want := range []string{identitySince, "fugaro update"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal %q does not name %q", err, want)
		}
	}
	if asked := r.svc.asked(); len(asked) != 1 {
		t.Fatalf("the service was asked %v, want the ask made before the gate judges the image", asked)
	}
	if n := len(r.f.run.Executions()); n != 0 {
		t.Fatalf("a refused launch started %d execution(s)", n)
	}
	if _, ok := ticketLaunchRecord(t, r.env, r.spec.RunID); ok {
		t.Fatal("a refused launch wrote the run's launch record")
	}
	if executed != nil {
		t.Fatalf("a refused launch handed an execution an environment: %v", executed)
	}
}

// ticketMetadata is the stub metadata server a resident launch reads its own
// account's token from, and how many times it was read.
type ticketMetadata struct {
	mu    sync.Mutex
	reads int
}

// ticketMetadataServer stands in for the platform's metadata server over the
// env var the mint reads (buildIdentityToken's GCE_METADATA_HOST), so a test
// exercises residentIdentity for real without leaving the process.
func ticketMetadataServer(t *testing.T) *ticketMetadata {
	t.Helper()
	m := &ticketMetadata{}
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Metadata-Flavor") != "Google" || r.URL.Path != buildMetadataIdentityPath {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		m.mu.Lock()
		m.reads++
		m.mu.Unlock()
		_, _ = io.WriteString(w, residentTicketIdentity)
	}))
	t.Cleanup(svc.Close)
	t.Setenv(buildMetadataHostEnv, strings.TrimPrefix(svc.URL, "http://"))
	return m
}

// publishTicketShared writes the installation's shared config into the
// fixture's runs bucket with the credentials service's URL in it: the document a
// container reads the URL out of, since the config such a service holds carries
// no block of its own (orchestrator_serve.go).
//
// The document is the published shape the fleet's own tests use (validShared,
// which ParseShared holds to the registry, the region and the identity of the
// installation that wrote it), read back under the fixture's project name and
// with the runs bucket the anchor will look for: the fixture's config names one
// that is no installation's (`unused-bucket`), because its bucket is a
// directory and no build writes there.
func publishTicketShared(t *testing.T, f *cloudFixture, url string) {
	t.Helper()
	lc, err := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.Replace(auroraShared(), "runs_bucket: fugaro-runs-proj-1234", "runs_bucket: "+lc.RunsBucketName(), 1)
	if doc == auroraShared() {
		t.Fatal("the published document no longer names the runs bucket this test rewrites")
	}
	doc += "credentials_service: { url: " + url + " }\n"
	writeBucketFile(t, f, infra.SharedConfigObject, doc)
}

// TestResidentLauncherAsksTheIssuer: the resident service's own ask. A resident
// pass re-issues an attempt its node recorded, and its launch asks the
// installation's credentials service for that run's ticket with the attempt
// named (D3136), presenting the token of the account it runs as — the metadata
// server's, because a container holds no gcloud and no person — and reaching the
// service at the URL the installation published, which is the one fact its own
// config does not carry.
func TestResidentLauncherAsksTheIssuer(t *testing.T) {
	f := newLauncherFixture(t)
	writeBuildRecord(t, f.cloudFixture, appSlug, "web", releaseIdentity)
	svc := newLaunchService(t, ticketAnswers(time.Now().Add(30*time.Minute)))
	publishTicketShared(t, f.cloudFixture, svc.serviceURL())
	meta := ticketMetadataServer(t)
	// The resident launcher presents its own account's token, which is the
	// metadata server's: the person's mint a command uses is never reached, and
	// a launch that fell back to it would fail here with no gcloud at all.
	prevMint, prevSelf, prevAsk := launchIdentity, selfCommand, launchAskTicket
	launchIdentity = func(context.Context, string) (string, error) {
		t.Error("a resident launch minted the launcher's own identity, and a container holds no gcloud and no person")
		return "", context.Canceled
	}
	selfCommand = func() string { return "fugaro" }
	// The resident's ask is the one under test, so it is the real one here.
	launchAskTicket = func(ctx context.Context, i *launchIssuer, req identitywire.TicketRequest) (identitywire.TicketResponse, error) {
		return i.askRetry(ctx, req)
	}
	t.Cleanup(func() { launchIdentity, selfCommand, launchAskTicket = prevMint, prevSelf, prevAsk })

	const itemID = "p5-1d"
	spec := orchestrator.Spec{ItemID: itemID, Title: "the launcher half", Repo: "acme/app",
		Recipe: "standard", RigorTier: "medium", Brief: "Start the run the node recorded."}
	seedResidentRun(t, f, ticketRun, spec, "dana@example.com")
	state := orchestrator.State{Intents: []orchestrator.Intent{{
		ID: orchestrator.IntentID(itemID, 1), RunID: ticketRun,
	}}}
	pass := residentPass{launch: f.resident(t), state: state}
	var atLaunch map[string]string
	f.run.OnRun = func(c gcpfake.RunCall) { atLaunch = c.Env }

	got, err := pass.Launch(context.Background(), spec, ticketRun)
	if calls := f.iam.SignCalls(); len(calls) != 0 {
		t.Fatalf("the resident launch minted %d tokens with this machine's own signer (%v), want none: the run's identity is the service's ticket", len(calls), calls)
	}
	if err != nil {
		t.Fatalf("the resident launch the service serves a ticket for: %v", err)
	}
	if got != ticketRun {
		t.Fatalf("the resident launcher started %q for the run the node recorded as %q", got, ticketRun)
	}
	if meta.reads == 0 {
		t.Fatal("the resident launch presented no identity from the metadata server: a container has no person's mint")
	}
	asked := svc.asked()
	if len(asked) != 1 || asked[0].Path != identitywire.TicketPath {
		t.Fatalf("the service was asked %v, want the ticket route once", asked)
	}
	if asked[0].Authorization != "Bearer "+residentTicketIdentity {
		t.Fatalf("the resident ask was presented %q, want the token of the account the service runs as", asked[0].Authorization)
	}
	req := ticketAskOf(t, asked[0])
	if req.Attempt != identitywire.AttemptID(orchestrator.IntentID(itemID, 1)) {
		t.Fatalf("the ask names the attempt %q, want the attempt the node recorded (%s)", req.Attempt, orchestrator.IntentID(itemID, 1))
	}
	if len(req.ContextRepos) != 0 {
		t.Fatalf("an orchestrator's ask names the context repositories %v, and D3136 refuses one that names any", req.ContextRepos)
	}
	if req.Slug != appSlug || req.Run != ticketRun {
		t.Fatalf("the ask names %s/%s, want the run the node recorded (%s/%s)", req.Slug, req.Run, appSlug, ticketRun)
	}
	if got := atLaunch[identitywire.TicketEnv]; got != ticketValue {
		t.Fatalf("the execution carries %s=%q, want the ticket the service issued", identitywire.TicketEnv, got)
	}
	if raw := ticketObject(t, fileEnv(t, f.cloudFixture), ticketRun); raw != "" {
		t.Fatal("a resident launch that holds a ticket wrote a token object")
	}
}
