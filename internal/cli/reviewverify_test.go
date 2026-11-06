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
	// The API root override this read already obeyed (providers.FromEnv read
	// the same variable): the stand-in is what an enterprise root stands in
	// for, and the value is not a credential.
	t.Setenv(providers.EnvGitHubAPIURL, srv.URL)
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
