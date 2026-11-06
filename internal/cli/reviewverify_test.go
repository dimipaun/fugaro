package cli

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
)

const (
	verifierAccount = "fugaro-verifier@proj-1234.iam.gserviceaccount.com"
	verifierService = "https://credentials-abc.a.run.app"
)

// verifierJWT is a token whose payload names email: the launcher reads the email
// claim and nothing else, so the header and signature are placeholders.
func verifierJWT(email string) string {
	enc := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	payload, _ := json.Marshal(map[string]string{"email": email})
	return enc([]byte(`{"alg":"none"}`)) + "." + enc(payload) + ".sig"
}

// iamStandIn is a stand-in IAM Credentials server: it records each call and
// answers what its status and token say.
type iamStandIn struct {
	mu     sync.Mutex
	paths  []string
	bodies []map[string]any
	auth   []string
	status int
	email  string
}

func startIAMStandIn(t *testing.T, status int, email string) *iamStandIn {
	t.Helper()
	s := &iamStandIn{status: status, email: email}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		s.mu.Lock()
		s.paths = append(s.paths, r.URL.EscapedPath())
		s.bodies = append(s.bodies, body)
		s.auth = append(s.auth, r.Header.Get("Authorization"))
		s.mu.Unlock()
		w.WriteHeader(s.status)
		if s.status/100 == 2 {
			_ = json.NewEncoder(w).Encode(map[string]string{"token": verifierJWT(s.email)})
		} else {
			_, _ = w.Write([]byte(`{"error":{"message":"denied"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	prev := impersonationEndpoint
	impersonationEndpoint = srv.URL
	t.Cleanup(func() { impersonationEndpoint = prev })
	return s
}

func (s *iamStandIn) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.paths)
}

// verifierEnv is a cloudEnv whose published policy names verifiers, and whose
// operator credential is a fixed token: the launcher's own, never the verifier's.
func verifierEnv(t *testing.T, published *projectpolicy.Policy) *cloudEnv {
	t.Helper()
	prev := managedCaller
	managedCaller = func(context.Context, *localcfg.Config) (string, oauth2.TokenSource, error) {
		return "user:operator@example.com", oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "operator-access"}), nil
	}
	t.Cleanup(func() { managedCaller = prev })
	env := &cloudEnv{lc: &localcfg.Config{}}
	env.policy.done = true
	if published != nil {
		env.policy.p, env.policy.found = published, true
	}
	return env
}

func policyNaming(accounts ...string) *projectpolicy.Policy {
	return &projectpolicy.Policy{Version: projectpolicy.Version, VerifierAccounts: accounts}
}

// The red step: the launcher mints the token as the installation's verifier.
// Mutation (run, restored): mint with the person's own credential instead of
// calling the impersonation endpoint, and the stand-in sees no call.
func TestReviewVerifierIdentityImpersonatesTheInstallationsVerifier(t *testing.T) {
	iam := startIAMStandIn(t, http.StatusOK, verifierAccount)
	env := verifierEnv(t, policyNaming(verifierAccount))

	tok, err := reviewVerifierIdentity(t.Context(), env, verifierService)
	if err != nil {
		t.Fatalf("the launcher could not mint the verifier identity: %v", err)
	}
	if tok != verifierJWT(verifierAccount) {
		t.Errorf("the token is not the one the impersonation endpoint minted")
	}
	if iam.calls() != 1 {
		t.Fatalf("%d impersonation calls, want 1", iam.calls())
	}
	if want := "/v1/projects/-/serviceAccounts/" + verifierAccount + ":generateIdToken"; !strings.EqualFold(iam.paths[0], want) && iam.paths[0] != strings.Replace(want, "@", "%40", 1) {
		t.Errorf("the call went to %s, want the published account's generateIdToken", iam.paths[0])
	}
	if iam.bodies[0]["audience"] != verifierService {
		t.Errorf("the audience is %v, want the service's own URL %s and no other", iam.bodies[0]["audience"], verifierService)
	}
	if iam.auth[0] != "Bearer operator-access" {
		t.Errorf("the call carried %q, want the launcher person's own credential", iam.auth[0])
	}
}

// Mutation (run, restored): take the account from the local config instead of
// the published node, or tolerate an empty list, and these fail.
func TestReviewVerifierIdentityRefusesWithNoVerifierNamed(t *testing.T) {
	for name, pol := range map[string]*projectpolicy.Policy{
		"an empty published list": policyNaming(),
		"no published policy":     nil,
	} {
		t.Run(name, func(t *testing.T) {
			iam := startIAMStandIn(t, http.StatusOK, verifierAccount)
			env := verifierEnv(t, pol)
			// The local config names one: it must not be asked.
			ver := []string{verifierAccount}
			env.lc.VerifierAccounts = &ver
			_, err := reviewVerifierIdentity(t.Context(), env, verifierService)
			if err == nil || !strings.Contains(err.Error(), "names no verifier") {
				t.Fatalf("err = %v, want the refusal that names no verifier", err)
			}
			if iam.calls() != 0 {
				t.Errorf("the impersonation endpoint was called %d times for an installation that names no verifier", iam.calls())
			}
		})
	}
}

// Mutation (run, restored): pick the first of two names and this fails.
func TestReviewVerifierIdentityRefusesTwoNames(t *testing.T) {
	iam := startIAMStandIn(t, http.StatusOK, verifierAccount)
	env := verifierEnv(t, policyNaming(verifierAccount, "other-verifier@proj-1234.iam.gserviceaccount.com"))
	_, err := reviewVerifierIdentity(t.Context(), env, verifierService)
	if err == nil || !strings.Contains(err.Error(), "2 verifier accounts") {
		t.Fatalf("err = %v, want the two-name refusal", err)
	}
	if iam.calls() != 0 {
		t.Errorf("the launcher chose between two names: %d calls", iam.calls())
	}
}

// Mutation (run, restored): accept a token whose email is another account and
// this fails.
func TestReviewVerifierIdentityRefusesATokenForAnotherAccount(t *testing.T) {
	startIAMStandIn(t, http.StatusOK, "someone-else@proj-1234.iam.gserviceaccount.com")
	env := verifierEnv(t, policyNaming(verifierAccount))
	tok, err := reviewVerifierIdentity(t.Context(), env, verifierService)
	if err == nil || !strings.Contains(err.Error(), "not for the verifier account") {
		t.Fatalf("err = %v, want the mismatched-token refusal", err)
	}
	if tok != "" {
		t.Errorf("a token for another identity was returned as a credential")
	}
}

// A launcher who is not one of the installation's operators holds no
// token-creator on the verifier account: IAM answers 403, and the refusal says
// who may verify and what the fix is, never that the installation is broken.
func TestReviewVerifierIdentityRefusesWhenTheImpersonationIsDenied(t *testing.T) {
	startIAMStandIn(t, http.StatusForbidden, "")
	env := verifierEnv(t, policyNaming(verifierAccount))
	_, err := reviewVerifierIdentity(t.Context(), env, verifierService)
	if err == nil {
		t.Fatal("a denied impersonation minted a token")
	}
	for _, want := range []string{"operators", "ask the owner"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
}

// The identity and the reader are one path: the fix brief's verify call carries
// the impersonated token, and the service's answer is the reader's own.
func TestFixBriefVerifiesWithTheImpersonatedIdentity(t *testing.T) {
	startIAMStandIn(t, http.StatusOK, verifierAccount)
	var gotAuth string
	var mu sync.Mutex
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]bool{"valid": true})
	}))
	t.Cleanup(svc.Close)
	env := verifierEnv(t, policyNaming(verifierAccount))
	env.lc.CredentialsService = &localcfg.CredentialsService{URL: svc.URL}

	prev := reviewVerifierSeam
	reviewVerifierSeam = nil
	t.Cleanup(func() { reviewVerifierSeam = prev })
	v, err := reviewVerifier(t.Context(), env, "acme-personal", "20261106-000000-abcd", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("artifact"))
	// The stand-in's service URL is http on a loopback address, which the
	// reader accepts; the token it mints is for that audience.
	if err := v.Verify(t.Context(), hex.EncodeToString(sum[:]), strings.Repeat("a", 64)); err != nil {
		t.Fatalf("the verify call failed: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotAuth != "Bearer "+verifierJWT(verifierAccount) {
		t.Errorf("the verify call carried %q, want the impersonated verifier token", gotAuth)
	}
	var _ runner.SignatureVerifier = v
}

// The published address goes into a URL path: one that is not a service account
// address, or carries a byte that could change the path, is refused before any
// call. Mutation (run, restored): drop the suffix and byte check and these fail.
func TestReviewVerifierIdentityRefusesAnAddressThatIsNotAServiceAccount(t *testing.T) {
	for _, bad := range []string{
		"x@y.iam.gserviceaccount.com/../..",
		"a%2Fb@y.iam.gserviceaccount.com",
		"person@example.com",
		"a?b@y.iam.gserviceaccount.com",
	} {
		t.Run(bad, func(t *testing.T) {
			iam := startIAMStandIn(t, http.StatusOK, bad)
			env := verifierEnv(t, policyNaming(bad))
			_, err := reviewVerifierIdentity(t.Context(), env, verifierService)
			if err == nil || !strings.Contains(err.Error(), "is not a service account address") {
				t.Fatalf("err = %v, want the not-a-service-account refusal", err)
			}
			if iam.calls() != 0 {
				t.Errorf("%d impersonation calls for an address that is not a service account", iam.calls())
			}
		})
	}
}

// reviewHeadAppID is the GitHub App ID the fixtures' project records for
// acme/app, and reviewHeadSHA the head their stand-in forge reports. Neither
// is a credential: the App ID is the number the platform passes to a job as
// FUGARO_GITHUB_APP_ID, and the SHA is a commit's name.
const (
	reviewHeadAppID = "123456"
	reviewHeadSHA   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	// reviewHeadPersonToken is what the caller's own Google ID token stands in
	// as: no test mints a real credential.
	reviewHeadPersonToken = "fake-" + "caller-identity-token-not-a-real-credential"
)

// readMintStandIn is a stand-in credentials service's GitHub read mint: it
// records what each call carried and answers what the case set. It is a
// stand-in rather than a fake of the client, so the call the launcher makes is
// the real one -- the path, the body and the identity are the wire's.
type readMintStandIn struct {
	url string

	mu     sync.Mutex
	paths  []string
	bodies []map[string]string
	auth   []string

	// token is the value the answer carries, names the repository the answer
	// names ("" answers the one the call named), and status a refusal of the
	// mint (0 is an answer).
	token  string
	names  string
	status int
}

func startReadMintStandIn(t *testing.T) *readMintStandIn {
	t.Helper()
	s := &readMintStandIn{token: "fake-" + t.Name() + "-read-token-not-a-real-credential"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]string
		_ = json.Unmarshal(raw, &body)
		s.mu.Lock()
		s.paths = append(s.paths, r.URL.Path)
		s.bodies = append(s.bodies, body)
		s.auth = append(s.auth, r.Header.Get("Authorization"))
		token, names, status := s.token, s.names, s.status
		s.mu.Unlock()
		if status != 0 && status/100 != 2 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"unverified_caller","message":"refused"}`))
			return
		}
		if names == "" {
			names = body["repository"]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      token,
			"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano),
			"repository": names,
		})
	}))
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

func (s *readMintStandIn) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.paths)
}

// forgeStandIn is a stand-in forge: it answers one pull request's read with the
// head it was given and records the authorization each read carried.
type forgeStandIn struct {
	url string

	mu    sync.Mutex
	paths []string
	auth  []string
	head  string
}

func startForgeStandIn(t *testing.T, head string) *forgeStandIn {
	t.Helper()
	s := &forgeStandIn{head: head}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.paths = append(s.paths, r.URL.Path)
		s.auth = append(s.auth, r.Header.Get("Authorization"))
		head := s.head
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number": 7, "state": "open", "title": "a pull request", "draft": false,
			"html_url": "https://example.invalid/acme/app/pull/7",
			"head":     map[string]any{"sha": head, "ref": "fugaro/fix"},
			"base":     map[string]any{"ref": "main"},
			"user":     map[string]any{"id": 1, "login": "someone"},
		})
	}))
	t.Cleanup(srv.Close)
	// The root is the package's own variable and no environment variable: a
	// stand-in for the forge replaces it for this test alone.
	prev := reviewGitHubAPIURL
	reviewGitHubAPIURL = srv.URL
	t.Cleanup(func() { reviewGitHubAPIURL = prev })
	s.url = srv.URL
	return s
}

func (s *forgeStandIn) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.paths)
}

// reviewHeadEnv is a cloudEnv whose project records acme/app as a GitHub
// repository with the App ID the read mint is signed as, and whose
// installation publishes the credentials service at url ("" publishes none).
func reviewHeadEnv(t *testing.T, url string) *cloudEnv {
	t.Helper()
	env := &cloudEnv{lc: &localcfg.Config{Repos: map[string]localcfg.Repo{
		"acme/app": {Provider: gitprov.KindGitHub, BaseBranch: "main", GitHubAppID: reviewHeadAppID},
	}}}
	if url != "" {
		env.lc.CredentialsService = &localcfg.CredentialsService{URL: url}
	}
	return env
}

// setReviewReadIdentity replaces the launcher's own ID token mint with a fixed
// value: a person's own credential is not a test's to mint, and no test runs
// gcloud.
func setReviewReadIdentity(t *testing.T) {
	t.Helper()
	prev := reviewReadIdentity
	reviewReadIdentity = func(context.Context, string) (string, error) { return reviewHeadPersonToken, nil }
	t.Cleanup(func() { reviewReadIdentity = prev })
}

// TestReviewHeadReadsThroughTheReadMint is the head read's whole path: the
// launcher presents its own Google ID token to the installation's credentials
// service, the service mints a read-only installation token for the ONE
// repository named and for the project's own App, and the forge's read of the
// pull request carries that token and no other.
//
// The red step: the launcher reads the head with OpenProvider(os.Environ(),
// nil), so it resolves the App ID and the App key out of its own environment --
// which a person's machine holds neither of (C2.2, D145) -- and the
// installation's published service is never asked at all.
func TestReviewHeadReadsThroughTheReadMint(t *testing.T) {
	mint := startReadMintStandIn(t)
	forge := startForgeStandIn(t, reviewHeadSHA)
	setReviewReadIdentity(t)
	// The environment carries a job's App ID, and it is not the App the
	// project's configuration records: the read must name the project's own.
	t.Setenv(providers.EnvGitHubAppID, "999999999")
	env := reviewHeadEnv(t, mint.url)

	head, err := reviewHead(t.Context(), env, "acme/app", 7)
	if err != nil {
		t.Fatalf("reading a pull request's head through the installation's read mint: %v", err)
	}
	if head != reviewHeadSHA {
		t.Errorf("the head read is %q, want the forge's own %q", head, reviewHeadSHA)
	}
	if mint.calls() != 1 {
		t.Fatalf("%d read mint calls, want 1", mint.calls())
	}
	if mint.paths[0] != github.ServiceReadPath {
		t.Errorf("the mint call went to %s, want %s", mint.paths[0], github.ServiceReadPath)
	}
	if got, want := mint.bodies[0]["repository"], "acme/app"; got != want {
		t.Errorf("the mint call names the repository %q, want %q", got, want)
	}
	if got, want := mint.bodies[0]["app_id"], reviewHeadAppID; got != want {
		t.Errorf("the mint call names the App %q, want the project's own %q", got, want)
	}
	if got, want := mint.auth[0], "Bearer "+reviewHeadPersonToken; got != want {
		t.Errorf("the mint call carried %q, want the caller's own identity token", got)
	}
	if forge.calls() != 1 {
		t.Fatalf("%d forge reads, want 1", forge.calls())
	}
	if got, want := forge.paths[0], "/repos/acme/app/pulls/7"; got != want {
		t.Errorf("the forge was read at %s, want %s", got, want)
	}
	if got, want := forge.auth[0], "Bearer "+mint.token; got != want {
		t.Errorf("the forge read carried %q, want the token the service minted and no other", got)
	}
}

// TestReviewHeadRefusesWithoutACredentialsService is the fail-closed shape
// reviewVerifier already has, one read over: an installation that publishes no
// credentials_service block has no read mint, and the head read refuses rather
// than being served by a credential of this process's own -- which a person's
// machine does not hold anyway.
//
// The environment carries a service the way a JOB's does (a job learns the URL
// as FUGARO_CREDENTIALS_SERVICE_URL) and this read is a person's: the block is
// the only source, so a machine that exports a URL reads the same as one that
// does not (F23) and the exported one is never asked.
func TestReviewHeadRefusesWithoutACredentialsService(t *testing.T) {
	mint := startReadMintStandIn(t)
	forge := startForgeStandIn(t, reviewHeadSHA)
	setReviewReadIdentity(t)
	t.Setenv(providers.EnvCredentialsServiceURL, mint.url)
	env := reviewHeadEnv(t, "")

	_, err := reviewHead(t.Context(), env, "acme/app", 7)
	if err == nil || !strings.Contains(err.Error(), "credentials_service") {
		t.Fatalf("err = %v, want the refusal that names the unpublished credentials service", err)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("the refusal is %v, want a sentence rather than a failure of the cloud", err)
	}
	if mint.calls() != 0 {
		t.Errorf("the read mint in the environment was called %d times for an installation that publishes no service", mint.calls())
	}
	if forge.calls() != 0 {
		t.Errorf("the forge was read %d times for an installation with no read mint", forge.calls())
	}
}

// TestReviewHeadNeverFallsBackToTheEnvironment is the mutation this task
// exists for: the environment carries a job's App ID and a real App key, and
// the installation publishes no service. Keeping OpenProvider(os.Environ(),
// nil) and laying the mint beside it leaves that credential in the read, so
// the head is served by the environment's App rather than refused.
func TestReviewHeadNeverFallsBackToTheEnvironment(t *testing.T) {
	forge := startForgeStandIn(t, reviewHeadSHA)
	setReviewReadIdentity(t)
	t.Setenv(providers.EnvGitHubAppID, "999999999")
	t.Setenv(providers.EnvGitHubAppKey, appKeyPEM(t))
	env := reviewHeadEnv(t, "")

	_, err := reviewHead(t.Context(), env, "acme/app", 7)
	if err == nil || !strings.Contains(err.Error(), "credentials_service") {
		t.Fatalf("err = %v, want the refusal that names the unpublished credentials service", err)
	}
	if forge.calls() != 0 {
		t.Errorf("the head read reached the forge with the environment's App credential: %d reads", forge.calls())
	}
}

// TestReviewHeadRefusesATokenForAnotherRepository: the answer names the
// repository it was minted for, and a token minted for another repository is
// one this read must not use. A misconfigured or hostile answer therefore
// refuses the brief rather than becoming a read of another repository's head.
func TestReviewHeadRefusesATokenForAnotherRepository(t *testing.T) {
	mint := startReadMintStandIn(t)
	mint.names = "acme/other"
	forge := startForgeStandIn(t, reviewHeadSHA)
	setReviewReadIdentity(t)
	env := reviewHeadEnv(t, mint.url)

	_, err := reviewHead(t.Context(), env, "acme/app", 7)
	if err == nil || !strings.Contains(err.Error(), "read mint") {
		t.Fatalf("err = %v, want the refusal that names the read mint", err)
	}
	if forge.calls() != 0 {
		t.Errorf("the forge was read %d times with a token minted for another repository", forge.calls())
	}
}

// TestReviewHeadRefusesWhenTheMintIsDenied: a person the installation does not
// list, or a caller the service does not admit, is answered 403 or 401 by the
// service. The refusal says what it is -- the read mint, and the caller's own
// identity -- and never reads as a broken pull request.
func TestReviewHeadRefusesWhenTheMintIsDenied(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusUnauthorized} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			mint := startReadMintStandIn(t)
			mint.status = status
			forge := startForgeStandIn(t, reviewHeadSHA)
			setReviewReadIdentity(t)
			env := reviewHeadEnv(t, mint.url)

			_, err := reviewHead(t.Context(), env, "acme/app", 7)
			if err == nil {
				t.Fatal("a refused read mint read the head")
			}
			for _, want := range []string{"read mint", "your own Google ID token", "operators"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q: %v", want, err)
				}
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("the refusal is %v, want a sentence rather than a failure of the cloud", err)
			}
			// The forge's own words are the parenthetical at the end of the
			// sentence, so the refusal leads with what it is.
			if !strings.HasPrefix(err.Error(), "the installation's credentials service's read mint") {
				t.Errorf("the refusal does not lead with the read mint: %v", err)
			}
			if forge.calls() != 0 {
				t.Errorf("the forge was read %d times for a mint the service refused", forge.calls())
			}
		})
	}
}

// TestFixBriefReadsTheHeadThroughTheReadMint is the read where it is USED: the
// head a fix round's brief is checked against comes from the forge, read with
// the token the installation's credentials service minted for this person, and
// the artifact's own head_sha is the claim being compared with it.
//
// Both directions are here, because either one alone is passed by a reader that
// is wrong in the other: an accepted head the artifact names, and a forge that
// has moved past the artifact, whose brief is refused. The second is what says
// the value compared is the forge's read and not the artifact's claim against
// itself -- and the mint's own calls are checked in both, so the head is read
// through the service and never from a credential of this process's own.
func TestFixBriefReadsTheHeadThroughTheReadMint(t *testing.T) {
	for _, tc := range []struct {
		name string
		head string
		want string
	}{
		{name: "the head the artifact names", head: fixBriefHead},
		{name: "a head the forge has moved past", head: strings.Repeat("b", len(fixBriefHead)), want: "head_sha"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mint := startReadMintStandIn(t)
			forge := startForgeStandIn(t, tc.head)
			setReviewReadIdentity(t)
			setFixBriefVerifier(t)
			if reviewPRHeadSeam != nil {
				t.Fatal("a head seam is set, so this test would not be the real read")
			}
			f := newCloudFixture(t)
			env := memEnv(t, f)
			reviewID := wallRunIDAt(0, "121500", "5eed")
			ctx := t.Context()
			// The two facts the read needs are the installation's published
			// credentials_service block and the project's own recorded App ID,
			// and neither is in the environment. The block is written by hand
			// because the config's own writers admit an https URL only, and the
			// stand-in service is a test's loopback one.
			env.lc.CredentialsService = &localcfg.CredentialsService{URL: mint.url}
			r := env.lc.Repos["acme/app"]
			r.GitHubAppID = reviewHeadAppID
			env.lc.Repos["acme/app"] = r

			data, err := json.Marshal(storedReviewArtifact(fixFirstArtifact(reviewID)))
			if err != nil {
				t.Fatal(err)
			}
			store := runstore.Open(env.bucket.Bucket, appSlug, reviewID)
			if err := store.PutFile(ctx, runstore.ArtifactPrefix+runner.ReviewArtifactName, data, "application/json"); err != nil {
				t.Fatal(err)
			}
			if err := store.PutFile(ctx, runstore.ArtifactPrefix+runner.ReviewSignName,
				[]byte(fixBriefSign(appSlug, reviewID, data)+"\n"), "text/plain"); err != nil {
				t.Fatal(err)
			}
			spec := firstRunSpec(reviewID)
			spec.OAuthToken = "acme-personal"
			if err := store.CreateTask(ctx, spec); err != nil {
				t.Fatal(err)
			}

			brief, err := reviewFindings(ctx, env, "acme/app", reviewID, 7)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("the brief of %s: %v", reviewID, err)
				}
				if !strings.Contains(brief, "the lock is released before the write") {
					t.Errorf("the brief does not carry the review's findings:\n%s", brief)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("err = %v, want a refusal naming %q", err, tc.want)
				}
			}
			// The head was read through the service in both directions, and for
			// the one repository and the one App this brief is for: a read that
			// reached the forge with anything else is a read of something this
			// launch is not for.
			if mint.calls() != 1 {
				t.Fatalf("the read mint was called %d times, want 1:\n%+v", mint.calls(), mint.bodies)
			}
			if body := mint.bodies[0]; body["repository"] != "acme/app" || body["app_id"] != reviewHeadAppID {
				t.Errorf("the read mint was asked for %+v, want acme/app signed as %s", body, reviewHeadAppID)
			}
			if mint.auth[0] != "Bearer "+reviewHeadPersonToken {
				t.Errorf("the read mint was presented %q, want the caller's own identity token", mint.auth[0])
			}
			if forge.calls() != 1 {
				t.Fatalf("the forge was read %d times, want 1", forge.calls())
			}
			if forge.auth[0] != "Bearer "+mint.token {
				t.Errorf("the forge was read with %q, want the token the read mint answered", forge.auth[0])
			}
		})
	}
}

// The forge API root a head read goes to is the package's own, never one the
// launcher's environment names: a root taken from a shell variable would
// receive the token the installation's service minted for the repository.
func TestReviewHeadIgnoresTheEnvironmentsForgeRoot(t *testing.T) {
	mint := startReadMintStandIn(t)
	forge := startForgeStandIn(t, reviewHeadSHA)
	other := startForgeStandIn(t, reviewHeadSHA)
	// startForgeStandIn pointed the package's root at `other`; put it back at
	// the first stand-in and name the second one in the environment.
	reviewGitHubAPIURL = forge.url
	t.Setenv(providers.EnvGitHubAPIURL, other.url)
	setReviewReadIdentity(t)
	env := reviewHeadEnv(t, mint.url)

	if _, err := reviewHead(t.Context(), env, "acme/app", 7); err != nil {
		t.Fatalf("reading a pull request's head: %v", err)
	}
	if other.calls() != 0 {
		t.Fatalf("the host FUGARO_GITHUB_API_URL names received %d request(s): the minted token went where the environment said", other.calls())
	}
	if forge.calls() != 1 {
		t.Fatalf("%d reads of the package's own root, want 1", forge.calls())
	}
}
