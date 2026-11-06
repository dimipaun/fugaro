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

	"golang.org/x/oauth2"

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
