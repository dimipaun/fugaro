package credsvc

// The mint's tests (C2.1, D145): one installation token per call, for the
// calling job's OWN repository, with the run's own permissions, audited by
// name, and refused in every direction it cannot be served — including the one
// that matters most, a bundle that holds no App key.
//
// Everything here is hermetic (Convention 9): the GitHub API is a fake on
// loopback reached through a transport that refuses every other destination,
// and the App key and the token are built at run time (Convention 2: no
// secret-shaped literal in a test file).

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
)

// testAppID is the App ID a mint call carries. It is not a secret — the
// platform already passes it to a job as an environment variable — and it is
// not a credential: nothing here is signed with it.
const testAppID = "1234"

// ghExpiry is the instant the fake GitHub says its token expires: an hour
// after the fixture's own clock (testNow), so github's minimum-validity rule
// (thirty minutes) is satisfied and the answer is comparable exactly.
var ghExpiry = testNow().Add(time.Hour)

// testAppKey is a real GitHub App private key for these tests: an RSA key
// generated for the run and encoded as PKCS#1 PEM, which is the form GitHub
// issues and the form github.ParsePrivateKey reads. It is generated rather
// than written down, so no test file holds a key (Convention 2), and it is
// generated once because a 2048-bit keygen is the slowest thing here.
var testAppKey = sync.OnceValue(func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("credsvc test: generating an App key: " + err.Error())
	}
	return k
})

// appKeyPEM is that key in the PEM form the bundle holds.
func appKeyPEM(t *testing.T) string {
	t.Helper()
	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(testAppKey()),
	}))
}

// pemCore is the first line of a PEM's base64 body: the part of a private key
// that has no newline in it. A check for "the App key is in this text" has to
// use it rather than the whole PEM, because every text this test reads — an
// answer body, an audit line — is JSON, where a multi-line value's newlines are
// escaped and the PEM's own text therefore never appears. A 64-character line
// of a freshly generated key is distinctive enough that a match is the key and
// not a coincidence.
func pemCore(t *testing.T) string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(appKeyPEM(t)), "\n")
	if len(lines) < 3 {
		t.Fatalf("the App key's PEM is not a PEM: %q", appKeyPEM(t))
	}
	return lines[1]
}

// mintBundleDoc is the installation's bundle with the GitHub App key in it:
// the fixture's own document (the model credentials) plus a private key. The
// key is written as a YAML block scalar, which is how a multi-line PEM reads
// in a document an operator wrote by hand.
func mintBundleDoc(t *testing.T) []byte {
	t.Helper()
	indented := "  " + strings.ReplaceAll(strings.TrimRight(appKeyPEM(t), "\n"), "\n", "\n  ")
	return append(testBundleDoc(t), []byte(config.GitHubAppKeySecret+": |\n"+indented+"\n")...)
}

// ---------------------------------------------------------------------------
// The fake GitHub
// ---------------------------------------------------------------------------

// ghSeen is one request the fake GitHub answered.
type ghSeen struct {
	method string
	path   string
	body   string
	auth   string
}

// ghFake stands in for api.github.com: it answers the two calls
// github.MintInstallationToken makes — the installation lookup and the token
// mint — and records what it was asked, so a test can read the repository, the
// permissions and the App ID the service sent. Any other request fails the
// test, because a mint makes these two and nothing else.
type ghFake struct {
	t   *testing.T
	srv *httptest.Server

	mu sync.Mutex
	// seen is every request the fake answered, in order.
	seen []ghSeen
	// status is the status the token call answers with. 0 is 201.
	status int
	// failBody is the body the token call answers with when status is not 2xx.
	failBody string
	// installation is the id the installation lookup answers. 0 is 777.
	installation int64
	// token is the token the token call answers with.
	token string
	// expires is the instant the token call says the token expires.
	expires time.Time
}

func newGHFake(t *testing.T) *ghFake {
	t.Helper()
	f := &ghFake{t: t, token: fakeCred(t, "installation-token"), expires: ghExpiry}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *ghFake) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		f.t.Errorf("reading the request body: %v", err)
	}
	f.mu.Lock()
	f.seen = append(f.seen, ghSeen{method: r.Method, path: r.URL.Path, body: string(body), auth: r.Header.Get("Authorization")})
	status, failBody, installation, token, expires := f.status, f.failBody, f.installation, f.token, f.expires
	f.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/installation"):
		if installation == 0 {
			installation = 777
		}
		f.write(w, http.StatusOK, map[string]any{"id": installation})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/access_tokens"):
		if status != 0 && status/100 != 2 {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, failBody)
			return
		}
		f.write(w, http.StatusCreated, map[string]any{
			"token":      token,
			"expires_at": expires.UTC().Format(time.RFC3339),
		})
	default:
		f.t.Errorf("the fake GitHub was asked for %s %s, and a mint makes two calls and no others", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *ghFake) write(w http.ResponseWriter, status int, body any) {
	raw, err := json.Marshal(body)
	if err != nil {
		f.t.Errorf("encoding the fake's answer: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

// requests is what the fake was asked, in order.
func (f *ghFake) requests() []ghSeen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ghSeen(nil), f.seen...)
}

// count is how many requests the fake saw.
func (f *ghFake) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.seen)
}

// both is the installation lookup and the mint, in that order, and it fails
// the test if the fake saw anything else.
func (f *ghFake) both(t *testing.T) (lookup, mint ghSeen) {
	t.Helper()
	seen := f.requests()
	if len(seen) != 2 {
		t.Fatalf("the fake GitHub saw %d requests, want the installation lookup and the mint: %v", len(seen), seen)
	}
	return seen[0], seen[1]
}

// ghTransport sends the service's GitHub calls to the fake and refuses every
// other destination. The service builds https://api.github.com itself — no
// caller and no configuration can name another host — so the destination is
// asserted here rather than assumed, and a test whose service reached
// anywhere else fails instead of leaving the process.
type ghTransport struct{ srv *httptest.Server }

func (h ghTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "api.github.com" {
		return nil, fmt.Errorf("the service sent a mint request to %s, and a mint reaches api.github.com alone", r.URL)
	}
	base, err := url.Parse(h.srv.URL)
	if err != nil {
		return nil, err
	}
	c := r.Clone(r.Context())
	c.URL.Scheme, c.URL.Host = base.Scheme, base.Host
	c.Host = base.Host
	return http.DefaultTransport.RoundTrip(c)
}

// ---------------------------------------------------------------------------
// The fixture
// ---------------------------------------------------------------------------

// newMintSvc is newSvc with a fake GitHub behind the service's client: the
// service's GitHub calls reach the fake, and every other destination is
// refused (ghTransport). mutate is newSvcWith's own hook, applied after the
// client is in place.
func newMintSvc(t *testing.T, mutate func(*svc, *Config)) (*svc, *ghFake) {
	t.Helper()
	gh := newGHFake(t)
	s := newSvcWith(t, func(sv *svc, cfg *Config) {
		cfg.Client = &http.Client{Transport: ghTransport{srv: gh.srv}}
		if mutate != nil {
			mutate(sv, cfg)
		}
	})
	return s, gh
}

// mintSvc is the whole fixture for a mint test: a service whose bundle holds
// the App key, and the fake GitHub behind it.
func mintSvc(t *testing.T, mutate func(*svc, *Config)) (*svc, *ghFake) {
	t.Helper()
	return newMintSvc(t, func(s *svc, cfg *Config) {
		s.src.docs = [][]byte{mintBundleDoc(t)}
		if mutate != nil {
			mutate(s, cfg)
		}
	})
}

// mintBody is a mint call's body, as the runner builds it.
func mintBody(appID string) string { return `{"app_id":"` + appID + `"}` }

// mintCall sends one mint call as the fixture's run and reads the answer.
func (s *svc) mintCall(t *testing.T, body string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	return s.mintTokens(t, body, s.runToken(), s.jobToken(), s.job, mutate...)
}

// mintTokens is mintCall with the two identities and the reported job name
// given rather than minted, for the calls that must present one the service
// refuses.
func (s *svc) mintTokens(t *testing.T, body, runTok, jobTok, job string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	resp := s.postTokens(t, MintPath, body, runTok, jobTok, job, mutate...)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	return resp, b
}

// mintAnswer reads a successful mint's answer strictly (Convention 6: the two
// ends of this contract are this repository's code).
func mintAnswer(t *testing.T, body []byte) MintResponse {
	t.Helper()
	var ans MintResponse
	if err := decodeStrict(body, &ans); err != nil {
		t.Fatalf("the mint's answer is not one this contract can read: %v (%s)", err, body)
	}
	return ans
}

// ---------------------------------------------------------------------------
// The mint
// ---------------------------------------------------------------------------

// TestMintForOwnRepositoryOnly is C2.1's red step, and it is the whole point
// of the endpoint: a run gets an installation token for ITS OWN repository and
// for no other.
//
// The caller names no repository — the body has one field, the App ID — so the
// repository is decided by the binding the service already made: the job
// account the caller's Google ID token verified as, whose slug must be the
// slug of the run identity's repository (review finding N1). The three halves
// below are that claim:
//
//   - a run of the repository its job account is bound to mints for it, and
//     GitHub is asked for exactly that repository;
//   - a run identity of ANOTHER repository, presented with a job token the
//     service admits, is refused before GitHub is reached at all;
//   - the same job account, bound by this deployment to the other repository,
//     mints for THAT one, which is the control: the repository comes from the
//     binding table and from nothing the caller sent.
func TestMintForOwnRepositoryOnly(t *testing.T) {
	t.Run("the run's own repository", func(t *testing.T) {
		s, gh := mintSvc(t, nil)
		runTok, jobTok := s.runToken(), s.jobToken()
		resp, body := s.mintTokens(t, mintBody(testAppID), runTok, jobTok, s.job)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the answer is %d, want 200: %s", resp.StatusCode, body)
		}
		ans := mintAnswer(t, body)
		if ans.Token != gh.token || !ans.ExpiresAt.Equal(ghExpiry) {
			t.Errorf("the answer = %+v, want the fake's token expiring %s", ans, ghExpiry)
		}
		if ans.Repository != testRepo {
			t.Errorf("the answer's repository is %q, want %q", ans.Repository, testRepo)
		}
		lookup, mint := gh.both(t)
		if want := "/repos/" + testRepoPath + "/installation"; lookup.path != want {
			t.Errorf("the installation lookup was %s, want %s", lookup.path, want)
		}
		if mint.method != http.MethodPost || mint.path != "/app/installations/777/access_tokens" {
			t.Errorf("the mint was %s %s, want the installation's own token path", mint.method, mint.path)
		}
		if !strings.Contains(mint.body, `"repositories":["aurora"]`) {
			t.Errorf("the mint asked GitHub for %s, want the run's own repository", mint.body)
		}
		// The answer carries a credential's value, so it is no-store and it
		// carries none of the forward path's headers: X-Fugaro-Tier and
		// X-Fugaro-Credential describe a credential that answered an
		// Anthropic call, and a mint has neither.
		if got := resp.Header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("the answer is Cache-Control %q, want no-store: an intermediary must not cache a credential", got)
		}
		for name := range resp.Header {
			if strings.HasPrefix(name, "X-Fugaro-") {
				t.Errorf("the mint's answer carries %s, which describes a model call and not a mint", name)
			}
		}
		// The value hygiene the file's header claims — "the value is written to
		// the answer and to nothing else: no log line, no error, no header" —
		// and the App key's, which is written nowhere at all.
		//
		// A header is held to this by its VALUE and not by its name, which is
		// what the X-Fugaro- loop above cannot do: that loop names the family
		// this service owns, and a value written under a name nobody thought of
		// is the leak worth catching. The body is held to everything but the
		// token, which is the one value the answer exists to carry (and is
		// asserted above, from the parsed answer) — the App key's own PEM
		// included, because a caller that received it would hold what D145
		// exists to keep out of the job.
		//
		// The App key is looked for by pemCore and not by its whole PEM: this
		// text is JSON, and a multi-line value's newlines are escaped in it, so
		// the PEM as written never appears even when the key is right there.
		for _, secret := range []string{gh.token, pemCore(t), fakeCred(t, "default"), fakeCred(t, testAccount), fakeCred(t, "paid"), runTok, jobTok} {
			for name, values := range resp.Header {
				for _, v := range values {
					if strings.Contains(v, secret) {
						t.Errorf("the mint's answer carries a credential value in its %s header", name)
					}
				}
			}
			if strings.Contains(s.logs.String(), secret) {
				t.Errorf("a minted token's own request wrote a credential value to the service's log:\n%s", s.logs.String())
			}
			if strings.Contains(s.audit.String(), secret) {
				t.Error("the mint's audit line carries a credential value")
			}
			if secret != gh.token && strings.Contains(string(body), secret) {
				t.Errorf("the mint's answer body carries %q, and the token is the only value it may carry", secret)
			}
		}
		// The caller's own two tokens never reach GitHub: the App's JWT
		// authenticates the call, and the two identities are this service's to
		// verify and to keep (the rule C1.11's scrub keeps for a model call).
		for _, seen := range []ghSeen{lookup, mint} {
			for _, tok := range []string{runTok, jobTok} {
				if strings.Contains(seen.auth, tok) || strings.Contains(seen.body, tok) {
					t.Errorf("%s %s carried the caller's own token", seen.method, seen.path)
				}
			}
		}
	})

	t.Run("another repository's run is refused", func(t *testing.T) {
		s, gh := mintSvc(t, nil)
		// The fixture's job account is bound to testRepo; this run identity is
		// a well-formed run of the other repository — its uid spells its own
		// slug, so the run token verifies — and the pairing of the two
		// identities is what refuses it (review finding N1).
		otherSlug := testSlug(t, testOtherRepoPath)
		other := s.runToken(func(c map[string]any) {
			c["fs"] = otherSlug
			c["sub"] = "r~" + otherSlug + "~" + s.run
			c["user_id"] = c["sub"]
		})
		resp, body := s.mintTokens(t, mintBody(testAppID), other, s.jobToken(), s.job)
		if resp.StatusCode != StatusUnverified {
			t.Fatalf("the answer is %d, want %d: the run is not a run of the job's repository", resp.StatusCode, StatusUnverified)
		}
		if got := refusal(t, resp.StatusCode, body); got.Error != ErrorUnverifiedRunIdentity {
			t.Errorf("the refusal is %q, want %q", got.Error, ErrorUnverifiedRunIdentity)
		}
		if n := gh.count(); n != 0 {
			t.Errorf("GitHub saw %d requests for a run of another repository, want none", n)
		}
		// And the refusal is recorded (C1.13): the binding is asked after the
		// identity is verified, so the whole identity is known when the call is
		// refused — the run, the repository the RUN belongs to, the job and the
		// account — and the line is the mint the run asked for, because that is
		// the route it called. A mint's line always names the App key, which is
		// the credential a mint issues from and the only one this endpoint has
		// (auditMint), whether or not the bundle was read: the name is the
		// endpoint's, not a value read out of anything.
		lines := mintAuditLines(t, s)
		if len(lines) != 1 {
			t.Fatalf("the refused mint wrote %d audit lines, want exactly 1: %v", len(lines), lines)
		}
		line := lines[0]
		checkAuditShape(t, line)
		for field, want := range map[string]any{
			"purpose":    PurposeMint,
			"credential": config.GitHubAppKeySecret,
			"status":     ErrorUnverifiedRunIdentity,
			"run":        s.run,
			"repository": otherSlug,
			"job":        s.job,
			"caller":     testJobSA,
			"tier":       "",
		} {
			if got := line[field]; got != want {
				t.Errorf("the refused mint's audit line %s = %v, want %q", field, got, want)
			}
		}
	})

	t.Run("the repository is the binding's, not the caller's", func(t *testing.T) {
		// The same admitted job account, bound by this deployment to the other
		// repository, and a run identity of that repository. The token comes
		// back scoped to it — which is the control for the two halves above:
		// the repository is read from the binding table the service was
		// configured with (JobAccount.Repo) and from nothing the caller sent.
		otherSlug := testSlug(t, testOtherRepoPath)
		s, gh := mintSvc(t, func(_ *svc, cfg *Config) {
			cfg.JobAccounts = []JobAccount{{Email: testJobSA, Slug: otherSlug, Repo: testOtherRepo}}
		})
		other := s.runToken(func(c map[string]any) {
			c["fs"] = otherSlug
			c["sub"] = "r~" + otherSlug + "~" + s.run
			c["user_id"] = c["sub"]
		})
		resp, body := s.mintTokens(t, mintBody(testAppID), other, s.jobToken(), s.job)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the answer is %d, want 200: %s", resp.StatusCode, body)
		}
		if ans := mintAnswer(t, body); ans.Repository != testOtherRepo {
			t.Errorf("the answer's repository is %q, want the calling job's own %q", ans.Repository, testOtherRepo)
		}
		lookup, mint := gh.both(t)
		if want := "/repos/" + testOtherRepoPath + "/installation"; lookup.path != want {
			t.Errorf("the installation lookup was %s, want the calling job's own repository", lookup.path)
		}
		if !strings.Contains(mint.body, `"repositories":["borealis"]`) {
			t.Errorf("the mint asked GitHub for %s, want the calling job's own repository", mint.body)
		}
	})
}

// TestMintPermissionsAreTokenPermissions: the token the service mints is a
// RUN's token — the four permissions design §6.1 gives a run, and no more —
// and the service adds none of its own. github.RunTokenPermissions is the
// exported spelling of the set internal/gitprov/github's own provider mints
// with (its test holds the two together), so the in-job path and the service's
// path ask GitHub for the same reach on the same run.
func TestMintPermissionsAreTokenPermissions(t *testing.T) {
	s, gh := mintSvc(t, nil)
	resp, body := s.mintCall(t, mintBody(testAppID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want 200: %s", resp.StatusCode, body)
	}
	_, mint := gh.both(t)
	var sent struct {
		Repositories []string          `json:"repositories"`
		Permissions  map[string]string `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(mint.body), &sent); err != nil {
		t.Fatalf("the mint's body is not the JSON this test reads: %v (%s)", err, mint.body)
	}
	if want := github.RunTokenPermissions(); !maps.Equal(sent.Permissions, want) {
		t.Errorf("the mint asked for %v, want the run's own %v", sent.Permissions, want)
	}
	if len(sent.Repositories) != 1 || sent.Repositories[0] != "aurora" {
		t.Errorf("the mint asked for repositories %v, want the run's own alone", sent.Repositories)
	}
	// The body carries those two fields and nothing else: a third would be
	// this service adding something to the request (a repository_selection, a
	// second scope) that no run asked for.
	var whole map[string]any
	if err := json.Unmarshal([]byte(mint.body), &whole); err != nil {
		t.Fatal(err)
	}
	for k := range whole {
		if k != "repositories" && k != "permissions" {
			t.Errorf("the mint's body carries %q, which this service does not send", k)
		}
	}
}

// TestMintRefusesEndedRun: a run that has ended is refused before the mint,
// exactly as it is refused before a model call — it is the same identity check
// and it runs first — and the ended run's placement is removed by that refusal
// (C1.9's N4 cleanup), because this is another call path whose liveness check
// learns the run is over.
func TestMintRefusesEndedRun(t *testing.T) {
	s, gh := mintSvc(t, nil)
	r := s.credsRun()
	s.nodes.nodes[creds.PathCurrent(r)] = "alice"
	s.db.set(budget.PathOutcome(budget.Day(s.now), r.Slug, r.ID))

	resp, body := s.mintCall(t, mintBody(testAppID))
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the answer is %d, want %d: the run has ended", resp.StatusCode, StatusUnverified)
	}
	if got := refusal(t, resp.StatusCode, body); got.Error != ErrorUnverifiedRunIdentity {
		t.Errorf("the refusal is %q, want %q", got.Error, ErrorUnverifiedRunIdentity)
	}
	if n := gh.count(); n != 0 {
		t.Errorf("GitHub saw %d requests for an ended run, want none", n)
	}
	if _, ok := s.nodes.nodes[creds.PathCurrent(r)]; ok {
		t.Error("the ended run's placement is still there: the mint's refusal did not clean it up")
	}
}

// TestMintAudited: a mint writes C1.13's audit line, through C1.13's auditor
// and not through a spelling of its own — one line per act, an issuance and a
// refusal alike, carrying the contract's fields and no value.
//
// The event is the one C1.13 defines for a mint: PurposeMint, the App key's
// NAME as the credential, and NO tier (the App key is not a model credential
// of the cascade, and C1.13 refuses a tier on a mint rather than recording one
// this service never assigned). The status is C1.13's own vocabulary: StatusOK
// for a token that was issued, and the refusal's wire error type — for every
// refusal this endpoint writes, state_unavailable — for one that was not.
func TestMintAudited(t *testing.T) {
	s, gh := mintSvc(t, nil)
	resp, body := s.mintCall(t, mintBody(testAppID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want 200: %s", resp.StatusCode, body)
	}
	lines := mintAuditLines(t, s)
	if len(lines) != 1 {
		t.Fatalf("the mint wrote %d audit lines, want one per act: %v", len(lines), lines)
	}
	line := lines[0]
	checkAuditShape(t, line)
	for field, want := range map[string]any{
		"purpose":    PurposeMint,
		"credential": config.GitHubAppKeySecret,
		"status":     StatusOK,
		"run":        s.run,
		"repository": s.slug,
		"job":        s.job,
		"caller":     testJobSA,
		// The tier is present and EMPTY: C1.13 writes every field, empty ones
		// included, so a reader does not have to tell "not known" from "not
		// there" — and a tier here would be refused rather than recorded.
		"tier": "",
	} {
		if got := line[field]; got != want {
			t.Errorf("the mint's audit line %s = %v, want %q", field, got, want)
		}
	}
	for _, secret := range []string{gh.token, pemCore(t), fakeCred(t, "default")} {
		if strings.Contains(s.audit.String(), secret) {
			t.Fatal("the audit line carries a credential value")
		}
	}

	// A refusal is audited too, with the refusal's own status and no value
	// either — and the message the refusal carried to the run is not in the
	// line: the line is the contract's fields, and a refusal's sentence is the
	// run's.
	s.audit.b.Reset()
	resp, body = s.mintCall(t, mintBody("not-a-number"))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the answer is %d, want %d", resp.StatusCode, StatusUnavailable)
	}
	lines = mintAuditLines(t, s)
	if len(lines) != 1 {
		t.Fatalf("the refused mint wrote %d audit lines, want one: %v", len(lines), lines)
	}
	line = lines[0]
	checkAuditShape(t, line)
	for field, want := range map[string]any{
		"purpose":    PurposeMint,
		"credential": config.GitHubAppKeySecret,
		"status":     ErrorStateUnavailable,
		"run":        s.run,
		"repository": s.slug,
		"tier":       "",
	} {
		if got := line[field]; got != want {
			t.Errorf("the refusal's audit line %s = %v, want %q", field, got, want)
		}
	}
	for _, secret := range []string{gh.token, pemCore(t), "not-a-number"} {
		if strings.Contains(s.audit.String(), secret) {
			t.Errorf("the refusal's audit line carries %q", secret)
		}
	}
}

// mintAuditLines parses the lines C1.13's auditor wrote for this fixture. They
// are the auditor's own lines and not the service's (the fixture builds the
// auditor on its own handler, server_test.go), so what this reads is the
// contract's line and nothing the service's logger added to it.
func mintAuditLines(t *testing.T, s *svc) []map[string]any {
	t.Helper()
	return parseAuditLines(t, s.audit.String())
}

// TestMintRefusesWhenItCannotBeAudited: C1.13's fail-closed rule is the mint's
// too (D143). A token whose line cannot be written is a token this service does
// not hand to anybody — the answer is a refusal and the token is in no answer,
// no log line and no error — and a mint it REFUSED is answered as the refusal
// it already was, because an act this service will not record is not made
// either way and the refusal is not the act.
//
// The auditor cannot be built unarmed (NewAuditor) and cannot be built without
// a handler, so "cannot be audited" is the two halves C1.13's line() names: a
// handler that fails to write, and a log that does not record at the line's
// level.
func TestMintRefusesWhenItCannotBeAudited(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler slog.Handler
	}{
		{name: "a handler that fails to write", handler: &recordHandler{err: errors.New("the log is not recording")}},
		{name: "a log that does not record the level", handler: &levelOffHandler{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The handler is handed to the SERVICE and not to an auditor built
			// here: the mint writes through the auditor the service builds for
			// every line it writes (NewServer's NewProbeAuditor), so a handler
			// the service cannot write through is one the mint cannot either.
			s, gh := mintSvc(t, func(_ *svc, cfg *Config) { cfg.AuditLog = tc.handler })
			resp, body := s.mintCall(t, mintBody(testAppID))
			if resp.StatusCode != StatusUnavailable {
				t.Fatalf("the answer is %d, want %d: a token this service could not audit is handed to nobody", resp.StatusCode, StatusUnavailable)
			}
			got := refusal(t, resp.StatusCode, body)
			if got.Error != ErrorStateUnavailable || !strings.Contains(got.Message, ErrMintUnaudited.Error()) {
				t.Errorf("the refusal is %+v, want state_unavailable and the unaudited class", got)
			}
			if n := gh.count(); n != 2 {
				t.Errorf("GitHub saw %d requests, want the lookup and the mint: the token was minted and then withheld", n)
			}
			// The token reaches nothing the run or an operator can read: not
			// the answer, not the service's own log.
			if strings.Contains(string(body), gh.token) || strings.Contains(s.logs.String(), gh.token) {
				t.Fatal("the token was handed out by a mint that could not be audited")
			}
			// The operator gets the auditor's own error, which names the field
			// and the class and never a value (C1.13).
			if logs := s.logs.String(); !strings.Contains(logs, "audit line was not written") {
				t.Errorf("the service did not log the audit failure:\n%s", logs)
			}
		})
	}

	// The other half: a refused mint is still refused, and the audit failure is
	// on the service's log rather than in the run's answer.
	t.Run("a refusal is still refused", func(t *testing.T) {
		s, gh := mintSvc(t, func(_ *svc, cfg *Config) {
			cfg.AuditLog = &recordHandler{err: errors.New("the log is not recording")}
		})
		resp, body := s.mintCall(t, mintBody("not-a-number"))
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("the answer is %d, want %d", resp.StatusCode, StatusUnavailable)
		}
		if got := refusal(t, resp.StatusCode, body); got.Error != ErrorStateUnavailable {
			t.Errorf("the refusal is %q, want %q", got.Error, ErrorStateUnavailable)
		}
		if n := gh.count(); n != 0 {
			t.Errorf("GitHub saw %d requests for a body this service cannot read, want none", n)
		}
	})
}

// levelOffHandler is a handler that records nothing: Enabled is false for every
// level, which is the half of "cannot be audited" a level-filtered log is
// (C1.13's line(): slog's own LogAttrs would drop such a record silently).
type levelOffHandler struct{}

func (levelOffHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (levelOffHandler) Handle(context.Context, slog.Record) error { return nil }
func (levelOffHandler) WithAttrs([]slog.Attr) slog.Handler        { return levelOffHandler{} }
func (levelOffHandler) WithGroup(string) slog.Handler             { return levelOffHandler{} }

// TestMintFailsClosedWithNoAppKeyInTheBundle: a bundle that holds no App key
// is a typed refusal and never a fallback. The service has no other key to
// reach — the job's own copy is in the job, and nothing here reads an
// environment or a file — so the shape of this test is the claim: the mint
// refuses, and GitHub is not called at all. A mint that quietly minted from
// somewhere else would be indistinguishable from a working installation, which
// is what D143 forbids.
//
// The refusal is state_unavailable, the refusal a run halts on.
func TestMintFailsClosedWithNoAppKeyInTheBundle(t *testing.T) {
	s, gh := mintSvc(t, func(s *svc, _ *Config) {
		// The installation's bundle as C1.11 knew it: the model credentials.
		s.src.docs = [][]byte{testBundleDoc(t)}
	})
	resp, body := s.mintCall(t, mintBody(testAppID))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the answer is %d, want %d", resp.StatusCode, StatusUnavailable)
	}
	got := refusal(t, resp.StatusCode, body)
	if got.Error != ErrorStateUnavailable {
		t.Errorf("the refusal is %q, want %q", got.Error, ErrorStateUnavailable)
	}
	if !strings.Contains(got.Message, ErrMintNoAppKey.Error()) {
		t.Errorf("the refusal is %q, want the class of a bundle with no App key", got.Message)
	}
	if n := gh.count(); n != 0 {
		t.Errorf("GitHub saw %d requests with no App key to sign with, want none", n)
	}
	for _, secret := range []string{fakeCred(t, "default"), fakeCred(t, "paid"), fakeCred(t, "appkey")} {
		if strings.Contains(string(body), secret) {
			t.Error("the refusal carries a credential value")
		}
	}
}

// TestMintRefusesWhatItCannotSignWith: the App key is looked up by name AND by
// kind, and a value that is not a private key is a refusal rather than a
// panic, a GitHub call or an echoed parse error.
//
// The second case is the collision worth naming: a bundle may hold an ACCOUNT
// named "github-app-key" (claude-oauth-token-github-app-key is a legal named
// account), and a lookup that took that token for the App key would offer an
// OAuth token to a PEM parser. The kind the document records tells them apart.
func TestMintRefusesWhatItCannotSignWith(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  func(*testing.T) []byte
		want error
	}{
		{
			name: "a value that is not a PEM",
			doc: func(t *testing.T) []byte {
				return append(testBundleDoc(t), []byte(config.GitHubAppKeySecret+": "+fakeCred(t, "appkey")+"\n")...)
			},
			want: ErrMintBadAppKey,
		},
		{
			name: "a token under the account prefix that spells the App key's name",
			doc: func(t *testing.T) []byte {
				return append(testBundleDoc(t), []byte(config.ClaudeAccountSecret(config.GitHubAppKeySecret)+": "+fakeCred(t, "appkey")+"\n")...)
			},
			want: ErrMintNoAppKey,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, gh := mintSvc(t, func(s *svc, _ *Config) { s.src.docs = [][]byte{tc.doc(t)} })
			resp, body := s.mintCall(t, mintBody(testAppID))
			if resp.StatusCode != StatusUnavailable {
				t.Fatalf("the answer is %d, want %d", resp.StatusCode, StatusUnavailable)
			}
			got := refusal(t, resp.StatusCode, body)
			if !strings.Contains(got.Message, tc.want.Error()) {
				t.Errorf("the refusal is %q, want the class %q", got.Message, tc.want)
			}
			if n := gh.count(); n != 0 {
				t.Errorf("GitHub saw %d requests, want none", n)
			}
			if strings.Contains(string(body), fakeCred(t, "appkey")) {
				t.Error("the refusal carries the value it refused")
			}
		})
	}
}

// TestMintSignsWithTheKeyAndNotTheAccountNamedAfterIt: the App key and the
// account family are two namespaces that can spell the same string, so a
// bundle may hold both config.GitHubAppKeySecret and an account whose resolved
// name is exactly that (claude-oauth-token-github-app-key -> "github-app-key").
// The mint reads the key by its KIND (Bundle.AppKey), never by a name the two
// can share, so the account's token is never handed to the PEM parser and the
// mint still issues — which is what proves it read the key: a mint that had
// taken the account for the key would have refused with ErrMintBadAppKey
// before reaching GitHub.
func TestMintSignsWithTheKeyAndNotTheAccountNamedAfterIt(t *testing.T) {
	doc := append(mintBundleDoc(t), []byte(config.ClaudeAccountSecret(config.GitHubAppKeySecret)+": "+fakeCred(t, "account")+"\n")...)
	s, gh := mintSvc(t, func(s *svc, _ *Config) { s.src.docs = [][]byte{doc} })
	resp, body := s.mintCall(t, mintBody(testAppID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want 200: %s", resp.StatusCode, body)
	}
	if ans := mintAnswer(t, body); ans.Token != gh.token {
		t.Errorf("the answer = %+v, want the fake's token", ans)
	}
	if n := gh.count(); n != 2 {
		t.Errorf("GitHub saw %d requests, want the installation lookup and the mint", n)
	}
}

// TestMintRefusesABodyItCannotRead: the mint's body is this contract's, and
// this contract is strict (Convention 6). A field the service does not know, a
// missing or malformed App ID, an oversized body and another method are all
// refused before GitHub is reached.
func TestMintRefusesABodyItCannotRead(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		body   string
		status int
		// wantMsg is a phrase the refusal must carry, for the cases whose
		// refusal is only distinguishable by what it says.
		wantMsg string
	}{
		{name: "no app id", method: http.MethodPost, body: `{}`, status: StatusUnavailable},
		{name: "an app id that is not a number", method: http.MethodPost, body: mintBody("abc"), status: StatusUnavailable},
		{name: "an app id over the bound", method: http.MethodPost, body: mintBody(strings.Repeat("9", 21)), status: StatusUnavailable},
		{name: "a repository the caller tried to name", method: http.MethodPost, body: `{"app_id":"` + testAppID + `","repository":"` + testRepoPath + `"}`, status: StatusUnavailable},
		{name: "a body that is not JSON", method: http.MethodPost, body: "app_id=1234", status: StatusUnavailable},
		{
			// A body that is a mint call but for its size: the bound is what
			// refuses it, and the refusal says so. The padding is a literal
			// size rather than maxMintRequestBytes, so this pins the bound
			// instead of moving with it.
			name: "an oversized body", method: http.MethodPost, status: StatusUnavailable,
			body:    `{"app_id":"` + testAppID + `","pad":"` + strings.Repeat("x", 8<<10) + `"}`,
			wantMsg: "larger than",
		},
		{name: "another method", method: http.MethodGet, body: mintBody(testAppID), status: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, gh := mintSvc(t, nil)
			resp, body := s.mintTokens(t, tc.body, s.runToken(), s.jobToken(), s.job, func(r *http.Request) {
				r.Method = tc.method
			})
			if resp.StatusCode != tc.status {
				t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, tc.status, body)
			}
			if n := gh.count(); n != 0 {
				t.Errorf("GitHub saw %d requests for a body this service refuses, want none", n)
			}
			if tc.status == StatusUnavailable {
				got := refusal(t, resp.StatusCode, body)
				if got.Error != ErrorStateUnavailable {
					t.Errorf("the refusal is %q, want %q", got.Error, ErrorStateUnavailable)
				}
				if tc.wantMsg != "" && !strings.Contains(got.Message, tc.wantMsg) {
					t.Errorf("the refusal is %q, want it to carry %q", got.Message, tc.wantMsg)
				}
			}
		})
	}
}

// TestMintRelaysNoGitHubTextAndHaltsTheRun: GitHub's refusal is a refusal the
// run halts on, and it is this service's sentence rather than GitHub's.
// httpjson's own error quotes up to 512 bytes of the answer's body, so relaying
// it would put another service's text — which can echo the request, the
// installation or anything else it chose — in a run's halt message.
func TestMintRelaysNoGitHubTextAndHaltsTheRun(t *testing.T) {
	marker := "the-installation-is-suspended-" + t.Name()
	s, gh := mintSvc(t, nil)
	gh.mu.Lock()
	gh.status, gh.failBody = http.StatusUnauthorized, `{"message":"`+marker+`"}`
	gh.mu.Unlock()

	resp, body := s.mintCall(t, mintBody(testAppID))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the answer is %d, want %d: a mint that could not be made halts the run", resp.StatusCode, StatusUnavailable)
	}
	got := refusal(t, resp.StatusCode, body)
	if got.Error != ErrorStateUnavailable {
		t.Errorf("the refusal is %q, want %q", got.Error, ErrorStateUnavailable)
	}
	if strings.Contains(got.Message, marker) || strings.Contains(got.Message, "401") {
		t.Errorf("the refusal carries GitHub's own text: %q", got.Message)
	}
	// GitHub's own text is in NO line this service writes: not the answer (the
	// two checks above), not the audit line, and not the operator's log line —
	// which is the one that matters most here, because the failed request
	// carried the App's JWT and httpjson's StatusError quotes up to 512 bytes
	// of the answer's body, so an operator's line built from err.Error() would
	// be relaying another service's sentence and could be echoing the request
	// back. The status alone is what this service writes about it.
	if strings.Contains(s.logs.String(), marker) {
		t.Errorf("the operator's log line carries GitHub's own text:\n%s", s.logs.String())
	}
	if strings.Contains(s.audit.String(), marker) {
		t.Error("the audit line carries GitHub's own text")
	}
	// The operator's line is still written, so a broken App is diagnosable —
	// by the status GitHub answered with, which is the thing an operator acts
	// on — and the audit line records the refusal with C1.13's own status for
	// it, which is the wire error type the run halted on and not a word of this
	// file's.
	logs := s.logs.String()
	for _, want := range []string{"could not be minted", "status=401"} {
		if !strings.Contains(logs, want) {
			t.Errorf("a failed mint's log does not name %q:\n%s", want, logs)
		}
	}
	lines := mintAuditLines(t, s)
	if len(lines) != 1 {
		t.Fatalf("a failed mint wrote %d audit lines, want one: %v", len(lines), lines)
	}
	if got := lines[0]["status"]; got != ErrorStateUnavailable {
		t.Errorf("the failed mint's audit line status = %v, want %q", got, ErrorStateUnavailable)
	}
	if got := lines[0]["credential"]; got != config.GitHubAppKeySecret {
		t.Errorf("the failed mint's audit line credential = %v, want %q", got, config.GitHubAppKeySecret)
	}
}

// TestMintIsStoppedByTheKillSwitch: the operator's stop flag is read before the
// bundle and before the mint, so a stopped service issues nothing — an
// installation token included. The run halts on the refusal, as it does for a
// model call.
func TestMintIsStoppedByTheKillSwitch(t *testing.T) {
	// The audit lines go to a buffer of their own, which is the handler the
	// SERVICE is configured with (AuditLog): the mint has no sink of its own
	// (auditMint), so a line for this call that the service's one auditor did
	// not write would be a line this test does not find — and the fixture's
	// default buffer, which nothing is writing through any more, must stay
	// empty for this call.
	mintLog := &logBuf{}
	s, gh := mintSvc(t, func(s *svc, cfg *Config) {
		s.kills.global = true
		cfg.AuditLog = slog.NewJSONHandler(mintLog, nil)
	})
	resp, body := s.mintCall(t, mintBody(testAppID))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the answer is %d, want %d", resp.StatusCode, StatusUnavailable)
	}
	if got := refusal(t, resp.StatusCode, body); got.Error != ErrorStateUnavailable {
		t.Errorf("the refusal is %q, want %q", got.Error, ErrorStateUnavailable)
	}
	if n := gh.count(); n != 0 {
		t.Errorf("GitHub saw %d requests with the kill switch on, want none", n)
	}
	// And the refusal is recorded as what the run asked for: one line, purpose
	// "mint". The stop switches are read before the route is branched on
	// (server.go), so a purpose taken from the branch would be "forward" here
	// and the audit would tell an operator that a model call was refused when
	// the run asked for an installation token.
	lines := auditLines(t, mintLog)
	if len(lines) != 1 {
		t.Fatalf("the stopped mint wrote %d audit lines, want exactly 1: %v", len(lines), lines)
	}
	if other := s.audit.String(); other != "" {
		t.Errorf("the call wrote to a second audit sink, want none: the mint has one writer: %s", other)
	}
	line := lines[0]
	checkAuditShape(t, line)
	for field, want := range map[string]any{
		"purpose":    PurposeMint,
		"status":     ErrorStateUnavailable,
		"run":        s.run,
		"repository": s.slug,
		"job":        s.job,
		"caller":     testJobSA,
		// A mint's line always names the App key — the credential this
		// endpoint issues from and the only one it has (auditMint) — whether
		// the refusal was taken before the bundle was read (this one) or after
		// it: the name is the endpoint's own and not something read out of a
		// bundle or a document.
		"credential": config.GitHubAppKeySecret,
		"tier":       "",
	} {
		if got := line[field]; got != want {
			t.Errorf("the stopped mint's audit line %s = %v, want %q", field, got, want)
		}
	}
}

// TestMintRefusesARepositoryThatIsNotGitHubs: the repository comes from the
// deployment's binding table, and an installation whose jobs are Bitbucket's
// has no GitHub repository to mint for. The refusal is the point: a canonical
// name this code cannot read is a name it must not guess a repository out of,
// because the token it mints is scoped to what those two strings say.
func TestMintRefusesARepositoryThatIsNotGitHubs(t *testing.T) {
	for _, tc := range []struct{ name, repo string }{
		{"another provider", "bitbucket/acme/aurora"},
		{"a canonical name with no provider", testRepoPath},
		{"a canonical name with four segments", "github/acme/nested/aurora"},
		{"a canonical name with an empty repository", "github/acme/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, gh := mintSvc(t, func(s *svc, cfg *Config) {
				cfg.JobAccounts = []JobAccount{{Email: testJobSA, Slug: s.slug, Repo: tc.repo}}
			})
			resp, body := s.mintCall(t, mintBody(testAppID))
			if resp.StatusCode != StatusUnavailable {
				t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
			}
			got := refusal(t, resp.StatusCode, body)
			if !strings.Contains(got.Message, ErrMintRepository.Error()) {
				t.Errorf("the refusal is %q, want the class %q", got.Message, ErrMintRepository)
			}
			if n := gh.count(); n != 0 {
				t.Errorf("GitHub saw %d requests for a repository it does not host, want none", n)
			}
		})
	}
}

// TestTheAppKeyIsNeverACascadeCandidate: the App key is in the bundle and is
// NOT a model credential, so the cascade never sees it. A candidate the
// cascade could select would be an App private key attached to an Anthropic
// call as the caller's credential — the worst thing this service could do with
// it — so this is asserted at the accessor the cascade reads (candidates) and
// end to end: a Messages call against a bundle that holds the App key sends
// the model credential and nothing else.
func TestTheAppKeyIsNeverACascadeCandidate(t *testing.T) {
	s := newSvcWith(t, func(s *svc, _ *Config) { s.src.docs = [][]byte{mintBundleDoc(t)} })
	b, err := ParseBundle(mintBundleDoc(t))
	if err != nil {
		t.Fatalf("the test's own bundle document is not one the loader accepts: %v", err)
	}
	cands, _, err := s.server.candidates(context.Background(), s.slug, testRepo, b)
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	if len(cands) == 0 {
		t.Fatal("the bundle's model credentials did not become candidates at all")
	}
	for _, c := range cands {
		if c.Name == config.GitHubAppKeySecret {
			t.Fatalf("the cascade was offered the App key: %v", c)
		}
	}

	// End to end: the call goes to the model credential, and the App key is
	// nowhere in what reached the upstream.
	resp, body := s.call(t, callBody("app-key-not-a-candidate"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want 200: %s", resp.StatusCode, body)
	}
	req := s.firstUpstream(t)
	for name, vals := range req.Header {
		for _, v := range vals {
			if strings.Contains(v, appKeyPEM(t)) {
				t.Errorf("the upstream was sent the App key in %s", name)
			}
		}
	}
	if got := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer "); !slices.Contains([]string{
		fakeCred(t, "default"), fakeCred(t, testAccount), fakeCred(t, "paid"),
	}, got) {
		t.Errorf("the upstream was called with a credential that is not one of the bundle's model credentials: %q", got)
	}
}
