package credsvc

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The service reads the outcomes as its own account: the database client is
// the Getter the outcome reader is built over, and this says so at compile
// time rather than in a comment.
var _ Getter = (*rtdb.Client)(nil)

// The identities in these tests are built at run time: an RSA key generated
// here, a token signed with it, and claim values that are test names. No
// literal in this file is, or looks like, a credential (Convention 2).
const (
	testProject  = "aurora"     // fp: the Fugaro project
	testFirebase = "aurora-dev" // aud/iss of the run's ID token
	testAudience = "https://creds-aurora-dev-uc.a.run.app"
	testJobSA    = "fugaro-job@aurora-dev.iam.gserviceaccount.com"
	testOtherSA  = "another-job@aurora-dev.iam.gserviceaccount.com"
	// The account of a *shared* project job (gcp.ProjectServiceAccountID): the
	// class B5.1 serves a coding run of a profile's repositories in.
	testSharedSA = "fugaro-p-profile-one-anthropic-acct-one@aurora-dev.iam.gserviceaccount.com"
	// testSharedProfile is the profile that account's row names, and
	// testSharedRepo the one repository the profile is bound to in these
	// fixtures: the class's binding is "the identity's repository, and it is
	// one of the profile's" (G21), so a row carries both beside the account.
	testSharedProfile = "profile-one"
	testSharedRepo    = "github/acme/aurora"
	testKid           = "test-kid-1"
	testOtherKid      = "test-kid-2"
	// The key id a Google ID token names. Google's own certs and Firebase's
	// are different key sets, so the caller's token is signed by a key the run
	// token's set does not hold.
	testJobKid = "google-kid"
	// branchRun is the run id the branch-claim tests stand on: fb names a
	// branch by a run id, and the ordering rule compares run ids as strings.
	branchRun = "20991231-000000-abcd"
)

// testKey is the key every token here is signed with, generated once: a
// 2048-bit keygen is the slowest thing in this package's tests.
var testKey = sync.OnceValue(func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("credsvc test: generating a key: " + err.Error())
	}
	return k
})

// signJWT returns a compact RS256 JWT. It is the test's own signer, so a test
// can hand the verifier a token signed by a key it does not know.
func signJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	return signJWTAlg(t, key, kid, "RS256", claims)
}

// signJWTAlg is signJWT with the header's algorithm under the test's control,
// so a test can present a token that says it is signed another way.
func signJWTAlg(t *testing.T, key *rsa.PrivateKey, kid, alg string, claims map[string]any) string {
	t.Helper()
	head, err := json.Marshal(map[string]any{"alg": alg, "kid": kid, "typ": "JWT"})
	if err != nil {
		t.Fatalf("marshalling the header: %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshalling the claims: %v", err)
	}
	signing := base64.RawURLEncoding.EncodeToString(head) + "." + base64.RawURLEncoding.EncodeToString(payload)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// signRawJWT signs a token whose header and payload segments are the test's own
// JSON text, so a test can present a segment the encoder would never produce: a
// header that is one object and then another, say. Everything else is signJWT.
func signRawJWT(t *testing.T, key *rsa.PrivateKey, head, payload string) string {
	t.Helper()
	signing := base64.RawURLEncoding.EncodeToString([]byte(head)) + "." + base64.RawURLEncoding.EncodeToString([]byte(payload))
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// staticKeys is a KeySource over a fixed set: the shape of a cached key set,
// with the same "not here" answer the real one gives.
type staticKeys map[string]*rsa.PublicKey

func (s staticKeys) Key(_ context.Context, kid string) (*rsa.PublicKey, error) {
	k, ok := s[kid]
	if !ok {
		// No kid in the error, exactly as the real source does it: the kid is
		// the caller's own text.
		return nil, fmt.Errorf("%w: the token names a key id Google does not publish", ErrUnknownKeyID)
	}
	return k, nil
}

// errKeys is a KeySource that cannot answer at all: the shape of Google's key
// endpoint being unreachable.
type errKeys struct{ err error }

func (e errKeys) Key(context.Context, string) (*rsa.PublicKey, error) { return nil, e.err }

// fakeDB is the installation's state as the service's reader sees it: the
// nodes the database holds, keyed by the path the reader asks for. Modelling
// it at the path level is what lets a test put an entry on agents/<slug>/<run>
// — the node the run's own token may rewrite — and watch the service not look
// at it, which is the W2 property. Every path asked for is recorded, so a test
// can see both which node was read and how far the liveness scan went.
type fakeDB struct {
	nodes map[string]bool
	paths []string
	err   error
}

func (d *fakeDB) Get(_ context.Context, path string, out any) (bool, error) {
	d.paths = append(d.paths, path)
	if d.err != nil {
		return false, d.err
	}
	found := d.nodes[path]
	if found {
		if raw, ok := out.(*json.RawMessage); ok {
			*raw = json.RawMessage(`{"status":"done"}`)
		}
	}
	return found, nil
}

// set writes a node, as the runner (an outcome) or the run's own token (a
// registry entry) would.
func (d *fakeDB) set(path string) { d.nodes[path] = true }

// daysAsked is the days the liveness scan asked about, in the order it asked,
// read back out of the paths it read.
func (d *fakeDB) daysAsked() []int64 {
	var days []int64
	for _, p := range d.paths {
		rest, ok := strings.CutPrefix(p, "outcomes/")
		if !ok {
			continue
		}
		if i := strings.IndexByte(rest, '/'); i > 0 {
			if day, err := strconv.ParseInt(rest[:i], 10, 64); err == nil {
				days = append(days, day)
			}
		}
	}
	return days
}

// fixture is one installation's service, with the clock and the state under
// the test's control.
type fixture struct {
	t       *testing.T
	now     time.Time
	slug    string
	run     string
	job     string
	db      *fakeDB
	auth    *Authenticator
	keys    staticKeys
	jobKeys staticKeys
}

// testNow is the instant every test's clock stands at.
func testNow() time.Time { return time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC) }

func newFixture(t *testing.T) *fixture { return newFixtureWith(t, nil) }

// newFixtureWith is newFixture with the service's configuration under the
// test's control, for the tests that need one wired differently: an account
// list in another spelling, a key source that cannot answer.
func newFixtureWith(t *testing.T, mutate func(*AuthConfig)) *fixture {
	t.Helper()
	now := testNow()
	f := &fixture{
		t:       t,
		now:     now,
		slug:    "aurora-app",
		run:     now.Format("20060102-150405") + "-ab12",
		job:     "fugaro-aurora-app-review",
		db:      &fakeDB{nodes: map[string]bool{}},
		keys:    staticKeys{testKid: &testKey().PublicKey},
		jobKeys: staticKeys{testJobKid: &testKey().PublicKey},
	}
	cfg := AuthConfig{
		Project:           testProject,
		FirebaseProject:   testFirebase,
		Audience:          testAudience,
		JobAccounts:       []string{testJobSA},
		Keys:              f.keys,
		JobKeys:           f.jobKeys,
		Outcomes:          RTDBOutcomes{DB: f.db},
		SharedJobAccounts: []SharedJobAccount{sharedRow()},
		Now:               func() time.Time { return f.now },
	}
	if mutate != nil {
		mutate(&cfg)
	}
	var err error
	f.auth, err = NewAuthenticator(cfg)
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	return f
}

// runClaims is the claim set of the run's Firebase ID token: what
// token.Exchange's ID token carries for uid r~<slug>~<run>.
//
// The shape is the one the repository already models as a real ID token
// (internal/gcpfake's IdentityToolkit.idToken, and the rules harness's
// unsignedToken): iss is the securetoken URL of the project, aud is the
// project id, sub and user_id are the uid, and the custom token's claims
// (fs, fr, fx, fp, rb) arrive at the TOP level — Firebase lifts them out of
// the custom token's "claims" object, which is exactly what
// token.sameIdentity checks an exchanged token for. The verifier reads them
// there; a test that put them under "claims" would be testing a token
// Firebase never issues.
// sharedRow is the fixture's row for the shared class (B5.1): the account a
// caller of that class is verified as, the profile it belongs to and the one
// repository that profile is bound to. The email is a derivation of a (profile,
// provider, account) triple and the row carries the two facts the class's
// pairing needs beside it.
func sharedRow() SharedJobAccount {
	return SharedJobAccount{Email: testSharedSA, Profile: testSharedProfile, Repos: []string{testSharedRepo}}
}

func (f *fixture) runClaims(mutate ...func(map[string]any)) map[string]any {
	c := map[string]any{
		"iss":       "https://securetoken.google.com/" + testFirebase,
		"aud":       testFirebase,
		"sub":       "r~" + f.slug + "~" + f.run,
		"user_id":   "r~" + f.slug + "~" + f.run,
		"auth_time": f.now.Add(-time.Minute).Unix(),
		"iat":       f.now.Add(-time.Minute).Unix(),
		"exp":       f.now.Add(30 * time.Minute).Unix(),
		"firebase":  map[string]any{"identities": map[string]any{}, "sign_in_provider": "custom"},
		"fs":        f.slug,
		"fr":        f.run,
		"fp":        testProject,
		"fx":        f.now.Add(2 * time.Hour).UnixMilli(),
		"rb":        "operator@example.invalid",
	}
	for _, m := range mutate {
		m(c)
	}
	return c
}

func (f *fixture) runToken(mutate ...func(map[string]any)) string {
	return signJWT(f.t, testKey(), testKid, f.runClaims(mutate...))
}

// jobClaims is the claim set of the Google ID token Cloud Run IAM admitted.
func (f *fixture) jobClaims(mutate ...func(map[string]any)) map[string]any {
	c := map[string]any{
		"iss":            "https://accounts.google.com",
		"aud":            testAudience,
		"sub":            "1234567890",
		"email":          testJobSA,
		"email_verified": true,
		"iat":            f.now.Add(-time.Minute).Unix(),
		"exp":            f.now.Add(30 * time.Minute).Unix(),
	}
	for _, m := range mutate {
		m(c)
	}
	return c
}

func (f *fixture) jobToken(mutate ...func(map[string]any)) string {
	return signJWT(f.t, testKey(), testJobKid, f.jobClaims(mutate...))
}

func (f *fixture) verify(runToken, jobToken, job string) (Identity, error) {
	f.t.Helper()
	return f.auth.Verify(context.Background(), Request{RunIDToken: runToken, JobIDToken: jobToken, Job: job})
}

// verifyOK is the fixture's happy path, for the tests that mutate one thing.
func (f *fixture) verifyOK(mutate ...func(map[string]any)) Identity {
	f.t.Helper()
	id, err := f.verify(f.runToken(mutate...), f.jobToken(), f.job)
	if err != nil {
		f.t.Fatalf("Verify refused a live run: %v", err)
	}
	return id
}

func TestVerifyAcceptsLiveRun(t *testing.T) {
	f := newFixture(t)
	id := f.verifyOK()

	if id.Slug != f.slug || id.Run != f.run {
		t.Errorf("identity = %s/%s, want %s/%s", id.Slug, id.Run, f.slug, f.run)
	}
	if id.Project != testProject {
		t.Errorf("project = %q, want %q", id.Project, testProject)
	}
	if want := f.now.Add(2 * time.Hour); !id.FX.Equal(want) {
		t.Errorf("fx = %v, want %v", id.FX, want)
	}
	if id.Launcher != "operator@example.invalid" {
		t.Errorf("launcher = %q, want the rb claim", id.Launcher)
	}
	if id.Job != f.job || id.JobAccount != testJobSA {
		t.Errorf("job = %q/%q, want %q/%q", id.Job, id.JobAccount, f.job, testJobSA)
	}
	// The liveness check asked about the run's days: no outcome is written for a
	// run that has not ended. The first day is the one above today, because the
	// day an outcome is written under is the database's clock, which may be
	// ahead of this service's (review nit).
	if asked := f.db.daysAsked(); len(asked) == 0 {
		t.Fatal("the liveness check asked about no day at all")
	} else if want := budget.Day(f.now) + 1; asked[0] != want {
		t.Errorf("the first day asked about = %d, want the day above today (%d)", asked[0], want)
	}
}

// TestAuthBranchMustMatchBranchRE: the branch claim (fb, G6) is the branch the
// identity may push, and a value that is not a Fugaro branch is refused
// whatever else the token says. Before the check existed the claim was read by
// nobody, so fb = main was accepted.
//
// The refusal does not repeat the value: an fb that is not a Fugaro branch is
// caller text of unbounded length, like the kid, and the package's no-echo
// rule holds for it too.
func TestAuthBranchMustMatchBranchRE(t *testing.T) {
	f := newFixture(t)
	f.run = branchRun
	for _, fb := range []string{
		"main",
		"fugaro/20991231-000000-abcd-extra",
		"fugaro/2026-10-20-090000-abcd",
		"not-fugaro/20991231-000000-abcd",
		"Fugaro/20991231-000000-abcd",
	} {
		_, err := f.verify(f.runToken(func(c map[string]any) { c["fb"] = fb }), f.jobToken(), f.job)
		if !errors.Is(err, ErrUnverified) {
			t.Fatalf("fb %q: err = %v, want ErrUnverified", fb, err)
		}
		if strings.Contains(err.Error(), fb) {
			t.Errorf("fb %q: the refusal echoes the branch: %v", fb, err)
		}
	}

	// A claim that is present but is not a string is refused rather than read
	// as "no branch", the rule fpr is held to as well.
	for name, fb := range map[string]any{
		"a number":  3,
		"an object": map[string]any{"fugaro": "20991231-000000-abcd"},
		"null":      nil,
		"an array":  []any{"fugaro/20991231-000000-abcd"},
	} {
		_, err := f.verify(f.runToken(func(c map[string]any) { c["fb"] = fb }), f.jobToken(), f.job)
		if !errors.Is(err, ErrUnverified) {
			t.Errorf("fb %s: err = %v, want ErrUnverified", name, err)
		}
	}
}

// TestAuthBranchOwnOrEarlierRun: fb binds the identity to a branch, and the
// binding is one a run can actually have — its own branch, or the branch of a
// run that sorts before it (a follow-up continues the branch its predecessor
// pushed). A branch of a LATER run, and a branch that is not a Fugaro branch at
// all, are refused: the first cannot have been inherited, and the second is not
// one of this installation's.
func TestAuthBranchOwnOrEarlierRun(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  string
		fb   string
		ok   bool
	}{
		{"its own branch", "20991231-000000-abcd", "fugaro/20991231-000000-abcd", true},
		{"the branch an earlier run created", "20991231-120000-abcd", "fugaro/20991231-000000-abcd", true},
		{"the branch of a later run", "20991231-000000-abcd", "fugaro/20991231-120000-abcd", false},
		{"a branch that is not a Fugaro branch", "20991231-000000-abcd", "release/1.x", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.run = tc.run
			id, err := f.verify(f.runToken(func(c map[string]any) { c["fb"] = tc.fb }), f.jobToken(), f.job)
			if tc.ok {
				if err != nil {
					t.Fatalf("Verify refused %s: %v", tc.fb, err)
				}
				if id.Branch != tc.fb {
					t.Errorf("branch = %q, want %q", id.Branch, tc.fb)
				}
				return
			}
			if !errors.Is(err, ErrUnverified) {
				t.Fatalf("err = %v, want ErrUnverified", err)
			}
		})
	}
}

// TestAuthIdentityWithoutBranchClaim: the claims are additive, so an identity
// minted before the launcher wrote them — and a review or artifact run, which
// the launcher mints without fb — is admitted with both empty rather than
// refused. The proxy refuses such an identity's pushes later (B1.4).
//
// fpr is read on its own (a review run carries it and no fb), and a value this
// service cannot read as one to four positive pull requests is refused: a
// claim that is present but malformed is not a claim to treat as absent.
func TestAuthIdentityWithoutBranchClaim(t *testing.T) {
	f := newFixture(t)
	f.run = branchRun
	if id := f.verifyOK(); id.Branch != "" || len(id.PRs) != 0 {
		t.Fatalf("identity without the claims = %q/%v, want empty", id.Branch, id.PRs)
	}

	id := f.verifyOK(func(c map[string]any) { c["fpr"] = []int{3, 4} })
	if id.Branch != "" || len(id.PRs) != 2 || id.PRs[0] != 3 || id.PRs[1] != 4 {
		t.Fatalf("review identity = %q/%v, want empty/[3 4]", id.Branch, id.PRs)
	}

	for name, fpr := range map[string]any{
		"five pull requests":  []int{1, 2, 3, 4, 5},
		"a zero pull request": []int{0},
		"not an array":        "3",
		"not a number":        []any{"3"},
	} {
		_, err := f.verify(f.runToken(func(c map[string]any) { c["fpr"] = fpr }), f.jobToken(), f.job)
		if !errors.Is(err, ErrUnverified) {
			t.Errorf("fpr %s: err = %v, want ErrUnverified", name, err)
		}
	}
}

// An outcome present means the run has ended, whatever its own registry entry
// says — and the scan covers every day from the launch day to today, because
// the outcome is written on the day the run ended.
func TestVerifyRefusesRunWithOutcome(t *testing.T) {
	t.Run("the outcome is written today", func(t *testing.T) {
		f := newFixture(t)
		f.db.set(budget.PathOutcome(budget.Day(f.now), f.slug, f.run))
		_, err := f.verify(f.runToken(), f.jobToken(), f.job)
		if !errors.Is(err, ErrUnverified) {
			t.Fatalf("err = %v, want ErrUnverified", err)
		}
		if !strings.Contains(err.Error(), "ended") {
			t.Errorf("err = %q, want it to say the run has ended", err)
		}
	})

	// A run that launched yesterday and ended yesterday: today's day holds no
	// outcome, so the scan has to reach the launch day to find it. A check
	// that looked at today only would admit this run.
	t.Run("the run launched yesterday and the outcome is the launch day's", func(t *testing.T) {
		f := newFixture(t)
		launch := f.now.Add(-20 * time.Hour) // the previous UTC day
		f.run = launch.Format("20060102-150405") + "-cd34"
		f.db.set(budget.PathOutcome(budget.Day(launch), f.slug, f.run))
		_, err := f.verify(f.runToken(), f.jobToken(), f.job)
		if !errors.Is(err, ErrUnverified) {
			t.Fatalf("err = %v, want ErrUnverified", err)
		}
		// Every day of the window was asked about: the day above today, then
		// today, then the launch day (where the outcome was found).
		want := []int64{budget.Day(f.now) + 1, budget.Day(f.now), budget.Day(launch)}
		if asked := f.db.daysAsked(); len(asked) != len(want) ||
			asked[0] != want[0] || asked[1] != want[1] || asked[2] != want[2] {
			t.Errorf("days asked = %v, want %v", asked, want)
		}
	})

	// The day above this service's today, which is the one a scan clamped to
	// today loses: the day an outcome is written under is the database's, whose
	// clock may be ahead of this service's, so a run that ended moments before
	// the database's midnight writes its outcome under a day this clock still
	// calls tomorrow. A scan that stopped at today admits it (review nit).
	t.Run("the outcome is dated the day above today", func(t *testing.T) {
		f := newFixture(t)
		tomorrow := budget.Day(f.now) + 1
		f.db.set(budget.PathOutcome(tomorrow, f.slug, f.run))
		_, err := f.verify(f.runToken(), f.jobToken(), f.job)
		if !errors.Is(err, ErrUnverified) {
			t.Fatalf("err = %v, want ErrUnverified: the run's outcome is dated the database's tomorrow", err)
		}
		if asked := f.db.daysAsked(); len(asked) == 0 || asked[0] != tomorrow {
			t.Errorf("days asked = %v, want the day above today (%d) first", asked, tomorrow)
		}
	})
}

// agents/<slug>/<run> proves nothing: the rules give the run's own token
// .write on it, so a hostile agent can rewrite or recreate it (review finding
// W2). The fixture's database holds both nodes, so these subtests can see
// which one the service asked for: the registry entry decides nothing in
// either direction, and the reader never touches it.
func TestVerifyIgnoresAgentsNode(t *testing.T) {
	t.Run("a live looking entry does not revive an ended run", func(t *testing.T) {
		f := newFixture(t)
		f.db.set(budget.PathAgent(f.slug, f.run))
		f.db.set(budget.PathOutcome(budget.Day(f.now), f.slug, f.run))
		if _, err := f.verify(f.runToken(), f.jobToken(), f.job); !errors.Is(err, ErrUnverified) {
			t.Fatalf("err = %v, want ErrUnverified", err)
		}
		assertNoAgentRead(t, f)
	})

	// The other direction, which is the one a hostile agent would use: an
	// entry it wrote itself does not end the run it is in, because the service
	// does not read the node at all.
	t.Run("a live looking entry does not end a live run", func(t *testing.T) {
		f := newFixture(t)
		f.db.set(budget.PathAgent(f.slug, f.run))
		f.verifyOK()
		assertNoAgentRead(t, f)
	})
}

// assertNoAgentRead is the W2 property at the level the service decides it:
// the database holds agents/<slug>/<run>, and the liveness check must have
// read something (so this pins a check that ran) and none of it that node.
func assertNoAgentRead(t *testing.T, f *fixture) {
	t.Helper()
	if len(f.db.paths) == 0 {
		t.Fatal("the liveness check read nothing at all, so it pinned nothing")
	}
	for _, path := range f.db.paths {
		if strings.HasPrefix(path, "agents/") {
			t.Fatalf("the liveness check read %q: the run's own token may rewrite that node, so it proves nothing (W2)", path)
		}
	}
}

// The run identity names one Fugaro project and was issued for one Firebase
// project: either one being another's is a refusal.
func TestVerifyRefusesOtherProject(t *testing.T) {
	t.Run("another Fugaro project in fp", func(t *testing.T) {
		f := newFixture(t)
		_, err := f.verify(f.runToken(func(c map[string]any) { c["fp"] = "someone-elses-project" }), f.jobToken(), f.job)
		if !errors.Is(err, ErrUnverified) {
			t.Fatalf("err = %v, want ErrUnverified", err)
		}
	})

	// aud and iss are checked one at a time, so each is pinned on its own: a
	// token that names the right issuer but was minted for another audience
	// (a replay from another service's Firebase project) must be refused, and
	// so must one that names our audience but another issuer.
	cases := map[string]func(map[string]any){
		"another audience, our issuer": func(c map[string]any) { c["aud"] = "other-firebase" },
		"our audience, another issuer": func(c map[string]any) {
			c["iss"] = "https://securetoken.google.com/other-firebase"
		},
		"both another project's": func(c map[string]any) {
			c["aud"] = "other-firebase"
			c["iss"] = "https://securetoken.google.com/other-firebase"
		},
	}
	for name, mutate := range cases {
		t.Run("an ID token from another Firebase project: "+name, func(t *testing.T) {
			f := newFixture(t)
			if _, err := f.verify(f.runToken(mutate), f.jobToken(), f.job); !errors.Is(err, ErrUnverified) {
				t.Fatalf("err = %v, want ErrUnverified", err)
			}
		})
	}
}

// The uid names the run and the claims must agree with it: a token for one
// run may never be read as another's, and a token whose uid is not a run at
// all is nobody's.
func TestVerifyRefusesAnIdentityThatNamesTwoRuns(t *testing.T) {
	cases := map[string]func(map[string]any){
		"fr names another run": func(c map[string]any) { c["fr"] = "20261020-090000-ffff" },
		"fs names another repo": func(c map[string]any) {
			c["fs"] = "another-repo"
		},
		"a uid that is not a run": func(c map[string]any) {
			c["sub"], c["user_id"] = "a-person-uid", "a-person-uid"
		},
		"two different uids": func(c map[string]any) { c["sub"] = "r~another-repo~20261020-090000-ffff" },
		// Both claims are required, and neither is a fallback for the other:
		// a token that names the run in one of them and nothing in the other
		// is refused rather than read from the claim that is left.
		"no sub":     func(c map[string]any) { delete(c, "sub") },
		"no user_id": func(c map[string]any) { delete(c, "user_id") },
		// The Firebase uid is sub, and it is the uid the rules honour
		// (auth.uid): a token whose sub names one run, whose user_id names
		// another and whose fs/fr agree with sub must not be read as sub's
		// run, because the two halves of the token disagree about who is
		// calling.
		"a uid that disagrees with sub, and claims that agree with sub": func(c map[string]any) {
			c["sub"] = "r~another-repo~20261020-090000-ffff"
			c["fs"], c["fr"] = "another-repo", "20261020-090000-ffff"
		},
		// The prefix is what makes the uid a run's: without it, a token whose
		// uid is slug~run shaped is still nobody's.
		"no r~ prefix on the uid": func(c map[string]any) {
			uid := c["fs"].(string) + "~" + c["fr"].(string)
			c["sub"], c["user_id"] = uid, uid
		},
		"no fx": func(c map[string]any) { delete(c, "fx") },
		// The slug and the run id become a database path and a log line: a
		// separator or a control character hiding in either is not one.
		"a slug that is not a path segment": func(c map[string]any) {
			uid := "r~a/b~" + c["fr"].(string)
			c["sub"], c["user_id"], c["fs"] = uid, uid, "a/b"
		},
		"a run id with a newline in it": func(c map[string]any) {
			uid := "r~" + c["fs"].(string) + "~run\nid"
			c["sub"], c["user_id"], c["fr"] = uid, uid, "run\nid"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			if _, err := f.verify(f.runToken(mutate), f.jobToken(), f.job); !errors.Is(err, ErrUnverified) {
				t.Fatalf("err = %v, want ErrUnverified", err)
			}
		})
	}
}

// A token the service cannot read, or reads and finds out of date, is not an
// identity: the shape, the algorithm and the two ends of the token's life are
// checked before any claim is believed.
func TestVerifyRefusesTokensThatAreNotUsable(t *testing.T) {
	oversized := "e30." + strings.Repeat("a", maxTokenBytes) + ".sig"
	runCases := map[string]func(*fixture) string{
		"no token at all":             func(*fixture) string { return "" },
		"not a JWT":                   func(*fixture) string { return "not.a.jwt" },
		"a token past the size bound": func(*fixture) string { return oversized },
		"another algorithm": func(f *fixture) string {
			return signJWTAlg(t, testKey(), testKid, "HS256", f.runClaims())
		},
		"an expired ID token": func(f *fixture) string {
			return f.runToken(func(c map[string]any) { c["exp"] = f.now.Add(-time.Minute).Unix() })
		},
		"no exp at all": func(f *fixture) string {
			return f.runToken(func(c map[string]any) { delete(c, "exp") })
		},
		"issued in the future": func(f *fixture) string {
			return f.runToken(func(c map[string]any) { c["iat"] = f.now.Add(time.Hour).Unix() })
		},
	}
	for name, tok := range runCases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			if _, err := f.verify(tok(f), f.jobToken(), f.job); !errors.Is(err, ErrUnverified) {
				t.Fatalf("err = %v, want ErrUnverified", err)
			}
		})
	}

	jobCases := map[string]func(*fixture) string{
		"no token at all":             func(*fixture) string { return "" },
		"not a JWT":                   func(*fixture) string { return "not.a.jwt" },
		"a token past the size bound": func(*fixture) string { return oversized },
		"no exp at all": func(f *fixture) string {
			return f.jobToken(func(c map[string]any) { delete(c, "exp") })
		},
	}
	for name, tok := range jobCases {
		t.Run("the caller's "+name, func(t *testing.T) {
			f := newFixture(t)
			if _, err := f.verify(f.runToken(), tok(f), f.job); !errors.Is(err, ErrUnverifiedJob) {
				t.Fatalf("err = %v, want ErrUnverifiedJob", err)
			}
		})
	}
}

// fx is the identity's own deadline: past it, the rules no longer honour the
// identity either, and until an outcome is written it is the only bound.
func TestVerifyRefusesExpiredFX(t *testing.T) {
	f := newFixture(t)
	_, err := f.verify(f.runToken(func(c map[string]any) { c["fx"] = f.now.Add(-time.Second).UnixMilli() }), f.jobToken(), f.job)
	if !errors.Is(err, ErrUnverified) {
		t.Fatalf("err = %v, want ErrUnverified", err)
	}
	if !strings.Contains(err.Error(), "fx") {
		t.Errorf("err = %q, want it to name fx", err)
	}

	// The boundary itself: fx is the last instant the identity is good for, so
	// fx == now is over (`!fx.After(now)` refuses it) and a millisecond later
	// is not. Both directions, because a check written `fx.Before(now)` would
	// admit the run for one instant longer than the minter's contract says.
	t.Run("fx exactly now is over", func(t *testing.T) {
		f := newFixture(t)
		_, err := f.verify(f.runToken(func(c map[string]any) { c["fx"] = f.now.UnixMilli() }), f.jobToken(), f.job)
		if !errors.Is(err, ErrUnverified) || !strings.Contains(err.Error(), "fx") {
			t.Fatalf("err = %v, want the fx refusal at the boundary", err)
		}
	})
	t.Run("fx a millisecond ahead is not", func(t *testing.T) {
		f := newFixture(t)
		f.verifyOK(func(c map[string]any) { c["fx"] = f.now.Add(time.Millisecond).UnixMilli() })
	})
}

// A token signed by a key that is not in Google's set is not a run's identity,
// whatever its claims say.
func TestVerifyRefusesUnknownKeyID(t *testing.T) {
	f := newFixture(t)
	tok := signJWT(t, testKey(), testOtherKid, f.runClaims())
	_, err := f.verify(tok, f.jobToken(), f.job)
	if !errors.Is(err, ErrUnverified) || !errors.Is(err, ErrUnknownKeyID) {
		t.Fatalf("err = %v, want ErrUnverified wrapping ErrUnknownKeyID", err)
	}
}

// The key set is Google's, and the signature is checked against it: a token
// that claims a known kid but was signed by another key is refused.
func TestVerifyRefusesAForgedSignature(t *testing.T) {
	f := newFixture(t)
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	tok := signJWT(t, other, testKid, f.runClaims())
	if _, err := f.verify(tok, f.jobToken(), f.job); !errors.Is(err, ErrUnverified) {
		t.Fatalf("err = %v, want ErrUnverified", err)
	}
}

// The shared class (C3.9's coding half, B5.1, G21): a run in a shared project
// job is served, and the class — with the profile its job serves — is read from
// the caller's *verified* account and never from X-Fugaro-Run-Job.
//
// The class is the account's for the reason the gate before it was: one service
// account is dedicated to one Cloud Run job, so the account is the job; the
// header is text the caller writes (a launcher may override any environment
// variable, internal/runner's jobName), so a class read off it would be one a
// caller could choose. Both directions are pinned below: the shared account is
// answered as the shared class however the header reads, and a per-repository
// account is answered as a repository caller however the header reads.
//
// What the class is SERVED for is not decided here and is not a fact of the
// account: the pairing binds the run's own repository and asks whether the
// profile is one of the row's (pairShared, server_test's coding tests).
func TestVerifyAnswersTheSharedJobClass(t *testing.T) {
	shared := func(c map[string]any) { c["email"] = testSharedSA }

	// The shared job's account, presenting a *per-repository* job name in the
	// header: the class is the account's, and the header decides nothing.
	f := newFixture(t)
	id, err := f.verify(f.runToken(), f.jobToken(shared), f.job)
	if err != nil {
		t.Fatalf("Verify refused a shared project job's account: %v", err)
	}
	if id.JobClass != ClassShared {
		t.Errorf("JobClass = %q, want %q", id.JobClass, ClassShared)
	}
	if id.Profile != testSharedProfile {
		t.Errorf("Profile = %q, want the profile the row names, %q", id.Profile, testSharedProfile)
	}
	if id.JobAccount != testSharedSA {
		t.Errorf("JobAccount = %q, want the configured spelling of the account, %q", id.JobAccount, testSharedSA)
	}
	// The run's own identity IS read, and after the account: the class is a
	// fact about the caller, and the run is checked under it like any other
	// admitted caller's.
	if paths := f.db.paths; len(paths) == 0 {
		t.Error("the run identity was never looked at, so the caller was not admitted at all")
	}

	// The same account with no header, and with a header that is not one clean
	// value: the refusal is the run's own placement rule and not the class's,
	// which is the order the two checks are in — this caller's class is the
	// account's, so a header no job of this installation could carry is what
	// refuses it.
	for name, job := range map[string]string{
		"a shared caller names no job":              "",
		"a shared caller's job has a newline in it": "fugaro-aurora\nX-Evil: 1",
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			_, err := f.verify(f.runToken(), f.jobToken(shared), job)
			if !errors.Is(err, ErrUnverified) || errors.Is(err, ErrUnverifiedJob) {
				t.Fatalf("err = %v, want ErrUnverified and not the class's refusal: the class is read from the account and this caller is refused by its header", err)
			}
			if !strings.Contains(err.Error(), "Cloud Run job name") {
				t.Errorf("err = %q, want the placement rule's own sentence", err)
			}
		})
	}

	// The other direction: an installation per-repository job account is
	// admitted even when the header names a shared project job. The header is
	// the caller's claim — recorded, never the gate — and the account is what
	// says which job the run is in.
	f2 := newFixture(t)
	if _, err := f2.verify(f2.runToken(), f2.jobToken(), "fugaro-p-profile-one-anthropic-acct-one-0123456"); err != nil {
		t.Fatalf("Verify refused a run whose caller is an admitted job account: %v", err)
	}

	// A run that names no job, or one that is not a Cloud Run job name of this
	// installation, still cannot be placed: the name goes into the run's own
	// record (C1.13) and its audit line, so it is held to the job-name rule —
	// every job this installation can have is internal/backend/gcp's sanitized
	// [a-z0-9-] at most 49 bytes (review finding m3).
	unplaceable := map[string]string{
		"no job at all":                        "",
		"a newline in the job name":            "fugaro-aurora\nX-Evil: 1",
		"a job name past the job-name bound":   strings.Repeat("j", maxJobNameBytes+1),
		"a job name past the header bound":     strings.Repeat("j", maxHeaderTokenBytes+1),
		"a job name with an upper-case letter": "Fugaro-Aurora-Review",
		"a job name with a quote in it":        `fugaro-aurora-review"`,
		"a job name with a semicolon in it":    "fugaro-aurora-review;rm -rf /",
		"a job name starting with a digit":     "1fugaro-aurora-review",
		"a job name ending in a hyphen":        "fugaro-aurora-review-",
		"a job name with an underscore":        "fugaro_aurora_review",
	}
	for name, job := range unplaceable {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			_, err := f.verify(f.runToken(), f.jobToken(), job)
			if !errors.Is(err, ErrUnverified) {
				t.Fatalf("err = %v, want ErrUnverified for a run that cannot be placed", err)
			}
			// The refusal names the rule and never the value the caller chose.
			if job != "" && strings.Contains(err.Error(), job) {
				t.Errorf("err = %q repeats the job name the caller presented", err)
			}
			if paths := f.db.paths; len(paths) != 0 {
				t.Errorf("paths read = %v, want none: an unplaceable run is refused before its identity is checked", paths)
			}
		})
	}
}

// TestClassIsDecidedFromTheVerifiedEmailNotTheHeader: the class of a caller is
// the *verified* email's, never X-Fugaro-Run-Job (spec §2.1). One service
// account is dedicated to one Cloud Run job, so the account is the job; the
// header is text the caller writes — a launcher may override any environment
// variable — so a class read off it would be one a caller could choose. Both
// directions are pinned: the header is recorded on the identity and decides
// nothing, whichever class presents whichever job name.
func TestClassIsDecidedFromTheVerifiedEmailNotTheHeader(t *testing.T) {
	review := func(c *AuthConfig) {
		c.ReviewJobAccounts = []ReviewJobAccount{{Email: testReviewSA, Account: testAccount}}
	}
	reviewer := func(c map[string]any) { c["email"] = testReviewSA }
	reviewRun := func(c map[string]any) { c["fpr"] = []any{7} }
	repoJobName := "fugaro-aurora-app-web"
	reviewJobName := ReviewJobName("standard", "github", testAccount)

	// The review job's account, reporting a per-repository job's name: the
	// class is still the review one.
	f := newFixtureWith(t, review)
	id, err := f.verify(f.runToken(reviewRun), f.jobToken(reviewer), repoJobName)
	if err != nil {
		t.Fatalf("Verify refused a review caller reporting another job's name: %v", err)
	}
	if id.JobClass != ClassReview {
		t.Errorf("class = %q, want %q: the header is not the class", id.JobClass, ClassReview)
	}
	if id.Job != repoJobName {
		t.Errorf("job = %q, want the name the caller reported: it is recorded and not decided from", id.Job)
	}
	if id.JobAccount != testReviewSA {
		t.Errorf("job account = %q, want the verified account %q", id.JobAccount, testReviewSA)
	}

	// And the other way: a per-repository job's account reporting the review
	// job's own name is still the repository class, and is not held to the
	// review claim rule.
	g := newFixtureWith(t, review)
	id, err = g.verify(g.runToken(), g.jobToken(), reviewJobName)
	if err != nil {
		t.Fatalf("Verify refused a repository caller reporting the review job's name: %v", err)
	}
	if id.JobClass != ClassRepository {
		t.Errorf("class = %q, want %q: the header is not the class", id.JobClass, ClassRepository)
	}

	// A deployment whose input puts one account in both sets is refused rather
	// than admitted: the two classes do not have the same routes, and the
	// refusal is the safe answer.
	t.Run("an account in both sets", func(t *testing.T) {
		h := newFixtureWith(t, func(c *AuthConfig) {
			review(c)
			c.ReviewJobAccounts = []ReviewJobAccount{{Email: testJobSA, Account: testAccount}}
		})
		_, err := h.verify(h.runToken(reviewRun), h.jobToken(), h.job)
		if !errors.Is(err, ErrUnverifiedJob) {
			t.Fatalf("err = %v, want ErrUnverifiedJob", err)
		}
		if !strings.Contains(err.Error(), "both") {
			t.Errorf("err = %q, want it to say the account is in both sets", err)
		}
	})
}

// The owner's answer of 2026-10-19: the IAM bit says an authorised identity
// called, the email says which. A token minted for another principal that
// holds run.invoker on the service is refused.
func TestVerifyRefusesForeignIDTokenEmail(t *testing.T) {
	cases := map[string]func(map[string]any){
		"another job service account": func(c map[string]any) { c["email"] = testOtherSA },
		"a person, not a service account": func(c map[string]any) {
			c["email"] = "someone@example.invalid"
		},
		"an unverified email": func(c map[string]any) { c["email_verified"] = false },
		// The claim must be there and be true: an ID token that says nothing
		// about the email's verification is refused rather than read as
		// verified (a missing claim is not a yes).
		"no email_verified claim at all": func(c map[string]any) { delete(c, "email_verified") },
		"no email at all":                func(c map[string]any) { delete(c, "email") },
		"another audience":               func(c map[string]any) { c["aud"] = "https://another-service.example.invalid" },
		"an expired token": func(c map[string]any) {
			c["exp"] = testNow().Add(-time.Minute).Unix()
		},
		"another issuer": func(c map[string]any) { c["iss"] = "https://accounts.example.invalid" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			_, err := f.verify(f.runToken(), f.jobToken(mutate), f.job)
			if !errors.Is(err, ErrUnverifiedJob) {
				t.Fatalf("err = %v, want ErrUnverifiedJob", err)
			}
		})
	}
}

// The caller's ID token is signed by Google, and the service checks that for
// itself rather than trusting its ingress to have done it: a claim set is not
// an identity. A token signed by a key Google does not publish, one that names
// a key id Google does not publish, and one that says it is signed another way
// are all refused.
func TestVerifyRefusesAForgedJobIDToken(t *testing.T) {
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	cases := map[string]func(*fixture) string{
		"another key, a published kid": func(f *fixture) string {
			return signJWT(t, other, testJobKid, f.jobClaims())
		},
		"a kid Google does not publish": func(f *fixture) string {
			return signJWT(t, testKey(), testOtherKid, f.jobClaims())
		},
		"another algorithm": func(f *fixture) string {
			return signJWTAlg(t, testKey(), testJobKid, "none", f.jobClaims())
		},
	}
	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			if _, err := f.verify(f.runToken(), tok(f), f.job); !errors.Is(err, ErrUnverifiedJob) {
				t.Fatalf("err = %v, want ErrUnverifiedJob", err)
			}
		})
	}
}

func TestVerifyAcceptsJobSAIDTokenEmail(t *testing.T) {
	f := newFixture(t)
	// The installation's own job service account, verified and for the right
	// audience: accepted, and the identity carries who called.
	id, err := f.verify(f.runToken(), f.jobToken(), f.job)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if id.JobAccount != testJobSA {
		t.Errorf("job account = %q, want %q", id.JobAccount, testJobSA)
	}
}

// Nothing the service refuses with may carry the token it was presented: a
// refusal is answered to the caller and written to a log.
//
// Both refusal paths are exercised with the *other* half valid, so each one
// really is the refusal that produced the error: Verify checks the caller
// first, and a run token paired with a bad job token is refused before
// verifyRun ever sees it (a regression that echoed the run token inside
// verifyRun would have kept the old version of this test green).
func TestVerifyErrorsNeverCarryTheToken(t *testing.T) {
	t.Run("the run identity's refusal", func(t *testing.T) {
		f := newFixture(t)
		runTok := f.runToken(func(c map[string]any) { c["fx"] = f.now.Add(-time.Second).UnixMilli() })
		_, err := f.verify(runTok, f.jobToken(), f.job)
		if !errors.Is(err, ErrUnverified) || errors.Is(err, ErrUnverifiedJob) {
			t.Fatalf("err = %v, want the run identity refused by verifyRun", err)
		}
		assertCarriesNoToken(t, err, runTok)
	})

	t.Run("the caller's refusal", func(t *testing.T) {
		f := newFixture(t)
		jobTok := f.jobToken(func(c map[string]any) { c["email"] = testOtherSA })
		_, err := f.verify(f.runToken(), jobTok, f.job)
		if !errors.Is(err, ErrUnverifiedJob) {
			t.Fatalf("err = %v, want the caller refused by verifyJob", err)
		}
		assertCarriesNoToken(t, err, jobTok)
	})

	// The refusals that happen before any claim is read — a token that is not
	// a JWT, and one whose signature does not verify — are the other half of
	// the same rule: they are answered to a caller who is holding the token, so
	// they are exactly where repeating it would be worst.
	t.Run("a refusal before any claim is read", func(t *testing.T) {
		other, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatalf("generating a key: %v", err)
		}
		notJWT := "not-a-jwt." + strings.Repeat("A", 40)
		cases := map[string]struct {
			run, job string
			want     error
		}{
			"a run token that is not a JWT": {run: notJWT, want: ErrUnverified},
			"a run token signed by another key": {
				run:  signJWT(t, other, testKid, newFixture(t).runClaims()),
				want: ErrUnverified,
			},
			"a job token that is not a JWT": {job: notJWT, want: ErrUnverifiedJob},
			"a job token signed by another key": {
				job:  signJWT(t, other, testJobKid, newFixture(t).jobClaims()),
				want: ErrUnverifiedJob,
			},
		}
		for name, c := range cases {
			t.Run(name, func(t *testing.T) {
				f := newFixture(t)
				run, job := c.run, c.job
				if run == "" {
					run = f.runToken()
				}
				if job == "" {
					job = f.jobToken()
				}
				_, err := f.verify(run, job, f.job)
				if !errors.Is(err, c.want) {
					t.Fatalf("err = %v, want %v", err, c.want)
				}
				assertCarriesNoToken(t, err, run)
				assertCarriesNoToken(t, err, job)
			})
		}
	})

	// The key id inside a token is the caller's text like any other, and it
	// can be 8 KiB of the caller's choosing: a refusal says the kid is not one
	// Google publishes and does not repeat it.
	hostile := "kid-" + strings.Repeat("A", 4096) + "\nX-Evil: 1"
	f := newFixture(t)
	_, err := f.verify(signJWT(t, testKey(), hostile, f.runClaims()), f.jobToken(), f.job)
	if !errors.Is(err, ErrUnknownKeyID) {
		t.Fatalf("err = %v, want ErrUnknownKeyID for a kid Google does not publish", err)
	}
	for _, fragment := range []string{hostile, "AAAA", "X-Evil"} {
		if strings.Contains(err.Error(), fragment) {
			t.Fatalf("err = %q carries the key id the caller chose", err)
		}
	}
}

// assertCarriesNoToken pins the no-echo invariant on one refusal: neither the
// token nor any head of it may appear in the error text.
func assertCarriesNoToken(t *testing.T, err error, tok string) {
	t.Helper()
	head := tok
	if len(head) > 24 {
		head = head[:24]
	}
	if strings.Contains(err.Error(), tok) || strings.Contains(err.Error(), head) {
		t.Fatalf("err = %q carries the token", err)
	}
}

// A liveness check the service cannot complete is a refusal, not an
// admission: the outcome is the one thing that ends a run's identity.
func TestVerifyRefusesWhenTheOutcomeCannotBeRead(t *testing.T) {
	f := newFixture(t)
	f.db.err = errors.New("the database is unreachable")
	_, err := f.verify(f.runToken(), f.jobToken(), f.job)
	if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrUnverified) {
		t.Fatalf("err = %v, want ErrUnavailable and not ErrUnverified", err)
	}
}

// A key source the service cannot read is a service failure, not a refusal of
// the caller (review finding m5): the caller's ID token is checked against
// Google's own published certificates, and a set that could not be read is
// ErrUnavailable — which the wire answers as state_unavailable, a halt — rather
// than ErrUnverifiedJob, which would tell a job whose account is configured
// that its identity was refused.
func TestVerifyReportsAJobKeyOutageAsUnavailable(t *testing.T) {
	f := newFixtureWith(t, func(c *AuthConfig) { c.JobKeys = errKeys{ErrUnavailable} })
	_, err := f.verify(f.runToken(), f.jobToken(), f.job)
	if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrUnverifiedJob) {
		t.Fatalf("err = %v, want ErrUnavailable and not ErrUnverifiedJob", err)
	}
	// The run's own key source is the other half of the same rule.
	f2 := newFixtureWith(t, func(c *AuthConfig) { c.Keys = errKeys{ErrUnavailable} })
	_, err = f2.verify(f2.runToken(), f2.jobToken(), f2.job)
	if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrUnverified) {
		t.Fatalf("err = %v, want ErrUnavailable and not ErrUnverified", err)
	}
}

// A presented token is bounded before it is parsed. The tokens below are well
// formed, signed by a key the service knows, and valid in every claim: they are
// refused for their size alone, and the bound is what makes that a refusal
// rather than a token this service parses 8 KiB of caller-chosen claims out of
// (the review's mutation survivor: with the bound gone, this token is admitted).
func TestVerifyRefusesATokenPastTheSizeBound(t *testing.T) {
	padding := func(c map[string]any) { c["pad"] = strings.Repeat("p", maxTokenBytes) }

	t.Run("the run identity", func(t *testing.T) {
		f := newFixture(t)
		tok := f.runToken(padding)
		if len(tok) <= maxTokenBytes {
			t.Fatalf("the token is %d bytes, want more than %d: the test would be vacuous", len(tok), maxTokenBytes)
		}
		_, err := f.verify(tok, f.jobToken(), f.job)
		if !errors.Is(err, ErrUnverified) {
			t.Fatalf("err = %v, want ErrUnverified", err)
		}
		if paths := f.db.paths; len(paths) != 0 {
			t.Errorf("paths read = %v, want none: an oversized token is refused before its claims are read", paths)
		}
	})

	t.Run("the caller's identity", func(t *testing.T) {
		f := newFixture(t)
		tok := f.jobToken(padding)
		if len(tok) <= maxTokenBytes {
			t.Fatalf("the token is %d bytes, want more than %d: the test would be vacuous", len(tok), maxTokenBytes)
		}
		_, err := f.verify(f.runToken(), tok, f.job)
		if !errors.Is(err, ErrUnverifiedJob) {
			t.Fatalf("err = %v, want ErrUnverifiedJob", err)
		}
	})
}

// A JWT segment is one JSON object and nothing after it. The tokens below are
// signed with the test's own key and are otherwise the tokens this service
// admits, so read half-way they are a live run: what is refused is the second
// JSON value, which the decoder would otherwise drop without a word.
func TestVerifyRefusesASegmentWithTrailingBytes(t *testing.T) {
	f := newFixture(t)
	f.verifyOK() // the same claims, without the trailing value, are admitted
	claims, err := json.Marshal(f.runClaims())
	if err != nil {
		t.Fatalf("marshalling the claims: %v", err)
	}
	head := `{"alg":"RS256","kid":"` + testKid + `","typ":"JWT"}`

	t.Run("trailing bytes after the claims", func(t *testing.T) {
		// A closing brace or bracket after the value is the trailer that slips
		// past a decoder's More(): More() answers "is the next byte '}' or ']'",
		// not "is anything left" (review finding on the strict-decode rule).
		for _, trailer := range []string{`{"extra":1}`, "}", "]", "} }"} {
			t.Run(trailer, func(t *testing.T) {
				f := newFixture(t)
				tok := signRawJWT(t, testKey(), head, string(claims)+trailer)
				if _, err := f.verify(tok, f.jobToken(), f.job); !errors.Is(err, ErrUnverified) {
					t.Fatalf("err = %v, want ErrUnverified", err)
				}
			})
		}
	})

	t.Run("trailing bytes after the header", func(t *testing.T) {
		for _, trailer := range []string{`{"extra":1}`, "}", "]", "} }"} {
			t.Run(trailer, func(t *testing.T) {
				f := newFixture(t)
				tok := signRawJWT(t, testKey(), head+trailer, string(claims))
				if _, err := f.verify(tok, f.jobToken(), f.job); !errors.Is(err, ErrUnverified) {
					t.Fatalf("err = %v, want ErrUnverified", err)
				}
			})
		}
	})
}

// The caller's ID token is held to its own validity window, as the run
// identity's is: one issued in the future and one that is not valid yet are
// refused, and one a clock a little ahead of ours issued is not — the bound is
// the clock skew, not the instant.
func TestVerifyRefusesAnIDTokenThatIsNotValidYet(t *testing.T) {
	cases := map[string]func(map[string]any){
		"issued in the future": func(c map[string]any) { c["iat"] = testNow().Add(time.Hour).Unix() },
		"not valid yet":        func(c map[string]any) { c["nbf"] = testNow().Add(time.Hour).Unix() },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			_, err := f.verify(f.runToken(), f.jobToken(mutate), f.job)
			if !errors.Is(err, ErrUnverifiedJob) {
				t.Fatalf("err = %v, want ErrUnverifiedJob", err)
			}
		})
	}

	// Within the skew, and with no nbf at all (Google's ID tokens carry none):
	// admitted. This is what says the check is a skew and not "iat must be in
	// the past".
	t.Run("within the clock skew", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.verify(f.runToken(), f.jobToken(func(c map[string]any) {
			c["iat"] = testNow().Add(clockSkew / 2).Unix()
			c["nbf"] = testNow().Add(-time.Minute).Unix()
		}), f.job); err != nil {
			t.Fatalf("Verify refused a token inside the clock skew: %v", err)
		}
	})
}

// The identity carries the *configured* spelling of the caller's account, not
// the claim's: JobAccount is the audit line's "who" (C1.13), and a claim that
// differs from the configuration only in case must not put the caller's own
// text there (review nit).
func TestVerifyCarriesTheConfiguredAccountSpelling(t *testing.T) {
	const configured = "Fugaro-Job@Aurora-Dev.iam.gserviceaccount.com"
	f := newFixtureWith(t, func(c *AuthConfig) { c.JobAccounts = []string{configured} })
	id, err := f.verify(f.runToken(), f.jobToken(), f.job)
	if err != nil {
		t.Fatalf("Verify refused the account's own address in another case: %v", err)
	}
	if id.JobAccount != configured {
		t.Errorf("job account = %q, want the configured spelling %q", id.JobAccount, configured)
	}
}

// The fold that matches an account is ASCII-only. Go's strings.ToLower folds by
// Unicode rules, so U+212A KELVIN SIGN becomes 'k' and an address spelled with
// it would be matched against the ASCII account it is not (review nit). The
// configured account here is the ASCII one, and the claim spells it with the
// Kelvin sign.
func TestVerifyRefusesANonASCIIEmail(t *testing.T) {
	const ascii = "kjob@aurora-dev.iam.gserviceaccount.com"
	f := newFixtureWith(t, func(c *AuthConfig) { c.JobAccounts = []string{ascii} })
	_, err := f.verify(f.runToken(), f.jobToken(func(c map[string]any) {
		c["email"] = "Kjob@aurora-dev.iam.gserviceaccount.com"
	}), f.job)
	if !errors.Is(err, ErrUnverifiedJob) {
		t.Fatalf("err = %v, want ErrUnverifiedJob: a Unicode fold is not an account match", err)
	}
	if strings.Contains(err.Error(), "job@") {
		t.Errorf("err = %q repeats the address the caller presented", err)
	}
}

// A run id that names no launch time cannot be checked day by day, so it is
// refused; and one claiming a launch older than any run can be is refused
// rather than scanned.
//
// The rule is internal/task's ValidRunID (^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$),
// the rule the launcher's NewRunID mints to and internal/runstore reads with,
// and the ids below are the ones a laxer rule would let through: fifteen bytes
// that happen to parse as a timestamp are not a run id, and a run id this
// repository never minted is not a run.
func TestVerifyRefusesARunIDItCannotPlace(t *testing.T) {
	unplaceable := map[string]string{
		"a run id too short to hold a time": "not-a-run-id",
		"a run id whose head is not a time": "abcdefgh-ijklmn-ee55",
		// A timestamp and then a sixteenth byte: the head parses, the id is
		// not a run id.
		"a run id whose head is a time and whose tail is not": "20261020-090000X",
		"a run id whose tail is not four hex digits":          "20261020-090000-xyz",
		"a run id whose tail is not lower-case hex":           "20261020-090000-AB12",
		"a run id with no tail at all":                        "20261020-090000-",
	}
	for name, run := range unplaceable {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.run = run
			_, err := f.verify(f.runToken(), f.jobToken(), f.job)
			if !errors.Is(err, ErrUnverified) {
				t.Fatalf("err = %v, want ErrUnverified", err)
			}
			if paths := f.db.paths; len(paths) != 0 {
				t.Errorf("paths read = %v, want none: a run whose launch day is unknown is refused, not scanned", paths)
			}
		})
	}

	t.Run("an implausibly old launch", func(t *testing.T) {
		f := newFixture(t)
		f.run = f.now.AddDate(0, 0, -maxRunDays-1).Format("20060102-150405") + "-ee55"
		_, err := f.verify(f.runToken(), f.jobToken(), f.job)
		if !errors.Is(err, ErrUnverified) {
			t.Fatalf("err = %v, want ErrUnverified", err)
		}
		if paths := f.db.paths; len(paths) != 0 {
			t.Errorf("paths read = %v, want none: the scan is bounded before it starts", paths)
		}
	})
}

// checkLive's two bounds, from the inside: the oldest launch day it still
// scans, and a launch day ahead of this service's own clock.
func TestVerifyBoundsTheLaunchDayScan(t *testing.T) {
	// Exactly maxRunDays back is still a run this service places: the scan
	// covers the launch day and every day since, plus the day below the launch
	// day and the day above today (checkLive's two widenings), and the run is
	// admitted. One day more is the refusal TestVerifyRefusesARunIDItCannotPlace
	// pins, and the pair is what says the bound is where the constant is rather
	// than anywhere either side of it.
	t.Run("the oldest launch it scans", func(t *testing.T) {
		f := newFixture(t)
		today := budget.Day(f.now)
		f.run = f.now.AddDate(0, 0, -maxRunDays).Format("20060102-150405") + "-ee55"
		f.verifyOK()
		days := f.db.daysAsked()
		if len(days) != maxRunDays+3 {
			t.Fatalf("days scanned = %v, want %d: the launch day, every day since, the day below the launch day and the day above today", days, maxRunDays+3)
		}
		if days[0] != today+1 || days[len(days)-1] != today-maxRunDays-1 {
			t.Errorf("days scanned = %v, want %d down to %d", days, today+1, today-maxRunDays-1)
		}
	})

	// A run id dated ahead of this service's clock is placed at today and not
	// in the future: a run id is whoever minted it's text, and this service's
	// clock being behind is not evidence that the run has not started. The
	// window widens rather than narrowing to one day: the day an outcome is
	// written under is the database's (the rules' DAYOK allows today or
	// yesterday), so both of the days an outcome can be on are read.
	t.Run("a launch day ahead of today", func(t *testing.T) {
		f := newFixture(t)
		today := budget.Day(f.now)
		f.run = f.now.AddDate(0, 0, 1).Format("20060102-150405") + "-ee55"
		f.db.set(budget.PathOutcome(today, f.slug, f.run))
		_, err := f.verify(f.runToken(), f.jobToken(), f.job)
		if !errors.Is(err, ErrUnverified) {
			t.Fatalf("err = %v, want a refusal: the run's outcome is written today", err)
		}
		// The scan starts at the day above today — the database's clock may be
		// ahead of ours — and reaches the launch day below it.
		if days := f.db.daysAsked(); len(days) == 0 || days[0] != today+1 {
			t.Errorf("days scanned = %v, want the day above today (%d) first", days, today+1)
		}
	})

	// The same day, written a day early: a run id minted today whose outcome is
	// under yesterday. That is what a launcher whose clock is ahead of the rules
	// server's produces — the outcome is written under the *server's* day
	// (DAYOK's today or yesterday), so an outcome can be a day behind the id
	// that wrote it just as it can be a day ahead of it. A clamp that only
	// widens when the launch day is ahead of ours reads one of those two days
	// and admits a run that has ended. The window is the merged one — the day
	// above today at the top (the database's clock can be ahead of ours), and
	// the day before today at the bottom, whatever the launch day is.
	t.Run("a launch day of today, an outcome yesterday", func(t *testing.T) {
		f := newFixture(t)
		today := budget.Day(f.now)
		f.run = f.now.Format("20060102-150405") + "-ee55"
		f.db.set(budget.PathOutcome(today-1, f.slug, f.run))
		_, err := f.verify(f.runToken(), f.jobToken(), f.job)
		if !errors.Is(err, ErrUnverified) {
			t.Fatalf("err = %v, want a refusal: the run's outcome is written yesterday", err)
		}
		if days := f.db.daysAsked(); len(days) != 3 || days[0] != today+1 || days[1] != today || days[2] != today-1 {
			t.Errorf("days scanned = %v, want %d, %d and %d", days, today+1, today, today-1)
		}
	})

	// The other day of that window, which is the one a clamp to today loses
	// (review finding m1): a run id dated ahead of our clock, whose outcome is
	// on the day our clock calls yesterday. The run has ended, and a scan of
	// today alone admits it.
	t.Run("a launch day ahead of today, an outcome yesterday", func(t *testing.T) {
		f := newFixture(t)
		today := budget.Day(f.now)
		f.run = f.now.AddDate(0, 0, 1).Format("20060102-150405") + "-ee55"
		f.db.set(budget.PathOutcome(today-1, f.slug, f.run))
		_, err := f.verify(f.runToken(), f.jobToken(), f.job)
		if !errors.Is(err, ErrUnverified) {
			t.Fatalf("err = %v, want a refusal: the run's outcome is written yesterday", err)
		}
		if days := f.db.daysAsked(); len(days) != 3 || days[0] != today+1 || days[1] != today || days[2] != today-1 {
			t.Errorf("days scanned = %v, want %d, %d and %d", days, today+1, today, today-1)
		}
	})

	// The day BELOW the launch day, which the outcome's own day can be: DAYOK
	// bounds an outcome to the database's today or yesterday, and the day it is
	// written under is the database's clock rather than the run's launch day --
	// so a run launched yesterday whose outcome is written as the database's
	// "yesterday" a further day behind this clock lands on the day below its
	// launch day. The run has ended, and a scan that stops at the launch day
	// (the launch bound) admits it: this is the write-once revocation W2 rests
	// on, so the day is read.
	//
	// The launch day is strictly before today on purpose. With a launch day of
	// today the clamp above already lowers the bound to today-1 -- the day the
	// outcome is planted on -- so the pre-fix scan reads it too and the subtest
	// proves nothing (the review round that found it). A launch day the clamp
	// leaves alone is the only shape that tells the two scans apart: the
	// outcome sits on today-2, which the pre-fix scan (down to the launch day)
	// never reads.
	t.Run("an outcome on the day below the launch day", func(t *testing.T) {
		f := newFixture(t)
		today := budget.Day(f.now)
		f.run = f.now.AddDate(0, 0, -1).Format("20060102-150405") + "-ee55"
		f.db.set(budget.PathOutcome(today-2, f.slug, f.run))
		_, err := f.verify(f.runToken(), f.jobToken(), f.job)
		if !errors.Is(err, ErrUnverified) {
			t.Fatalf("err = %v, want a refusal: the run's outcome is on the day below its launch day", err)
		}
		if days := f.db.daysAsked(); len(days) != 4 || days[0] != today+1 || days[3] != today-2 {
			t.Errorf("days scanned = %v, want %d down to %d", days, today+1, today-2)
		}
	})
}

// The construction refuses a service that cannot check what it must check.
// TestReviewAccountRefusalsNameTheAxisAndNotAnAccount is AK9's (SEC): four
// sentences refuse a review job account this service cannot serve a call by,
// and each of them says ACCOUNT AXIS and never "a Claude account". The
// distinction is not a preference: since AK4 the axis's values include
// config.AnthropicKeyAccount — the project's own Anthropic API key, which is
// not an account — so a refusal telling an operator that what serves a review
// call is a Claude account sends them to the accounts list for a fault that is
// in the axis, and the refusal is what they have to act on.
//
// The four are the two AuthConfig refusals and the two the service makes with
// the same table (NewServer): the same rows are configured twice in a deployment
// — the verifier's own set and the service's — so the two files must refuse in
// the same words and cannot be fixed one at a time.
func TestReviewAccountRefusalsNameTheAxisAndNotAnAccount(t *testing.T) {
	base := AuthConfig{
		Project:         testProject,
		FirebaseProject: testFirebase,
		Audience:        testAudience,
		JobAccounts:     []string{testJobSA},
		Keys:            staticKeys{testKid: &testKey().PublicKey},
		JobKeys:         staticKeys{testJobKid: &testKey().PublicKey},
		Outcomes:        RTDBOutcomes{DB: &fakeDB{}},
	}
	for _, tc := range []struct {
		name  string
		rows  []ReviewJobAccount
		wants string
	}{
		{
			name:  "a review job account that names nothing",
			rows:  []ReviewJobAccount{{Email: testReviewSA, Account: ""}},
			wants: "no value on the account axis",
		},
		{
			name: "one review job account bound to two values",
			rows: []ReviewJobAccount{
				{Email: testReviewSA, Account: testReviewAccount},
				{Email: testReviewSA, Account: config.AnthropicKeyAccount},
			},
			wants: "bound to two values of the account axis",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := base
			auth.ReviewJobAccounts = tc.rows
			_, err := NewAuthenticator(auth)
			if err == nil {
				t.Fatal("NewAuthenticator accepted a review job account it cannot serve a call by")
			}
			assertAxisSentence(t, "internal/credsvc/auth.go", err.Error(), tc.wants)
		})
	}

	// The service's own table is configured beside the verifier's, and the
	// repository table is what NewServer requires before it reads the review
	// rows at all: a service that admits a review caller without it could pair
	// no call with a run.
	var captured *Config
	newSvcWith(t, func(s *svc, cfg *Config) {
		captured = cfg
		cfg.Repos = map[string]string{s.slug: testRepo}
	})
	if captured == nil {
		t.Fatal("the fixture handed the test no configuration")
	}
	for _, tc := range []struct {
		name  string
		rows  []ReviewJobAccount
		wants string
	}{
		{
			name:  "a review job account that names nothing",
			rows:  []ReviewJobAccount{{Email: testReviewSA, Account: ""}},
			wants: "no value on the account axis",
		},
		{
			name: "one review job account bound to two values",
			rows: []ReviewJobAccount{
				{Email: testReviewSA, Account: testReviewAccount},
				{Email: testReviewSA, Account: config.AnthropicKeyAccount},
			},
			wants: "bound to two values of the account axis",
		},
	} {
		t.Run("NewServer: "+tc.name, func(t *testing.T) {
			c := *captured
			c.ReviewAccounts = tc.rows
			_, err := NewServer(c)
			if err == nil {
				t.Fatal("NewServer accepted a review job account it cannot serve a call by")
			}
			assertAxisSentence(t, "internal/credsvc/server.go", err.Error(), tc.wants)
		})
	}
}

// assertAxisSentence holds one refusal sentence to AK9's rule: it names the
// axis, and it does not say that what serves a review call is a Claude account.
// The second half is the one that catches a copy left behind, which is the
// mutation this task's own plan names.
func assertAxisSentence(t *testing.T, file, sentence, wants string) {
	t.Helper()
	if !strings.Contains(sentence, wants) {
		t.Errorf("%s's refusal does not name the axis: %q", file, sentence)
	}
	if strings.Contains(sentence, "Claude account") {
		t.Errorf("%s's refusal says a review call is served by a Claude account: %q", file, sentence)
	}
}

func TestNewAuthenticatorRefusesAnIncompleteConfig(t *testing.T) {
	base := AuthConfig{
		Project:           testProject,
		FirebaseProject:   testFirebase,
		Audience:          testAudience,
		JobAccounts:       []string{testJobSA},
		Keys:              staticKeys{testKid: &testKey().PublicKey},
		JobKeys:           staticKeys{testJobKid: &testKey().PublicKey},
		Outcomes:          RTDBOutcomes{DB: &fakeDB{}},
		SharedJobAccounts: []SharedJobAccount{sharedRow()},
	}
	cases := map[string]func(*AuthConfig){
		"no project":          func(c *AuthConfig) { c.Project = "" },
		"no firebase project": func(c *AuthConfig) { c.FirebaseProject = "" },
		"no audience":         func(c *AuthConfig) { c.Audience = "" },
		"no job accounts":     func(c *AuthConfig) { c.JobAccounts = nil },
		"no keys":             func(c *AuthConfig) { c.Keys = nil },
		"no job keys":         func(c *AuthConfig) { c.JobKeys = nil },
		"no outcomes":         func(c *AuthConfig) { c.Outcomes = nil },
		"an empty shared job account": func(c *AuthConfig) {
			c.SharedJobAccounts = []SharedJobAccount{sharedRow(), {Email: ""}}
		},
		// The accounts are matched by an ASCII fold, so an address that is not
		// ASCII could never be matched — verifyJob refuses a non-ASCII claim
		// rather than folding it — and two entries that are one address in two
		// cases make the spelling the audit line carries arbitrary. Both are
		// configuration errors rather than gates that silently admit nobody.
		"a job account that is not ASCII": func(c *AuthConfig) {
			c.JobAccounts = []string{"Kob@aurora-dev.iam.gserviceaccount.com"}
		},
		"two job accounts that differ only in case": func(c *AuthConfig) {
			c.JobAccounts = []string{testJobSA, strings.ToUpper(testJobSA)}
		},
		"a shared account that is not ASCII": func(c *AuthConfig) {
			c.SharedJobAccounts = []SharedJobAccount{{Email: "Kob@aurora-dev.iam.gserviceaccount.com", Profile: testSharedProfile, Repos: []string{testSharedRepo}}}
		},
		"a job account with a space in it": func(c *AuthConfig) {
			c.JobAccounts = []string{"fugaro job@aurora-dev.iam.gserviceaccount.com"}
		},
		// A shared job's row names the profile the class's binding is made
		// against (B5.1, G21), and a row without one would answer a class
		// whose pairing could name no profile at all — a configuration that
		// binds nothing rather than one that refuses.
		"a shared account that names no profile": func(c *AuthConfig) {
			c.SharedJobAccounts = []SharedJobAccount{{Email: testSharedSA}}
		},
		"one shared account bound to two profiles": func(c *AuthConfig) {
			c.SharedJobAccounts = []SharedJobAccount{
				sharedRow(),
				{Email: testSharedSA, Profile: "another-profile", Repos: []string{testSharedRepo}},
			}
		},
		// A review job account names a value on the account axis, and the
		// branch that refuses an empty one had no case until the axis grew its
		// second value (AK4's config.AnthropicKeyAccount, which is not an
		// account): the empty string is a credential no bundle holds, so a
		// pairing that carried it could only refuse.
		"a review job account that names nothing on the axis": func(c *AuthConfig) {
			c.ReviewJobAccounts = []ReviewJobAccount{{Email: testReviewSA, Account: ""}}
		},
		"one review job account bound to two values of the axis": func(c *AuthConfig) {
			c.ReviewJobAccounts = []ReviewJobAccount{
				{Email: testReviewSA, Account: testReviewAccount},
				{Email: testReviewSA, Account: config.AnthropicKeyAccount},
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := base
			mutate(&cfg)
			if _, err := NewAuthenticator(cfg); err == nil {
				t.Fatal("NewAuthenticator accepted an incomplete configuration")
			}
		})
	}
	if _, err := NewAuthenticator(base); err != nil {
		t.Fatalf("NewAuthenticator refused a complete configuration: %v", err)
	}
	// An empty shared set is not an incomplete configuration: an installation
	// with no shared project job has none, and the set is the deployment's own
	// document (FUGARO_SHARED_JOB_ACCOUNTS) — empty being the fail-closed
	// state in which the class is refused whole, which is what every
	// installation whose module renders it nowhere starts in.
	noShared := base
	noShared.SharedJobAccounts = nil
	if _, err := NewAuthenticator(noShared); err != nil {
		t.Fatalf("NewAuthenticator refused a configuration with no shared project jobs: %v", err)
	}
}

// The real outcome reader reads the write-once path the writer wrote — the
// rules' RUN && DAYOK && !data.exists() node — and nothing else. A reader
// pointed at the wrong path is a liveness check that always says "live".
func TestRTDBOutcomesReadsTheWriteOncePath(t *testing.T) {
	day := budget.Day(testNow())
	slug, run := "aurora-app", "20261020-090000-ab12"
	fake := &fakeGetter{found: true}
	exists, err := RTDBOutcomes{DB: fake}.OutcomeExists(context.Background(), day, slug, run)
	if err != nil {
		t.Fatalf("OutcomeExists: %v", err)
	}
	if !exists {
		t.Error("exists = false, want the reader's found")
	}
	if want := budget.PathOutcome(day, slug, run); fake.path != want {
		t.Errorf("path = %q, want %q", fake.path, want)
	}
	if !strings.HasPrefix(fake.path, "outcomes/") || strings.Contains(fake.path, "agents/") {
		t.Errorf("path = %q, want the outcome node and never the registry entry", fake.path)
	}

	// A reader the database refuses is an error, not "no outcome": the caller
	// must not read it as a live run.
	fake.err = errors.New("the database is unreachable")
	if _, err := (RTDBOutcomes{DB: fake}).OutcomeExists(context.Background(), day, slug, run); err == nil {
		t.Error("a failed read was reported as an answer")
	}
	// A segment budget.PathOutcome would panic on is refused, not panicked on,
	// and the read is never made: both halves of the uid are held to
	// token.ValidSegment, because both become part of the path. A guard on one
	// of them is a path a caller can steer.
	for name, segments := range map[string][2]string{
		"an empty slug":              {"", run},
		"an empty run id":            {slug, ""},
		"a slug with a separator":    {"aurora/app", run},
		"a run id with a tilde":      {slug, "r~run"},
		"a slug with a control byte": {"aurora\x00app", run},
		"a slug with a space":        {"aurora app", run},
	} {
		t.Run(name, func(t *testing.T) {
			g := &fakeGetter{found: true}
			if _, err := (RTDBOutcomes{DB: g}).OutcomeExists(context.Background(), day, segments[0], segments[1]); err == nil {
				t.Fatal("a segment that is not a usable path segment was accepted")
			}
			if g.path != "" {
				t.Errorf("path read = %q, want none: a segment that cannot be a path is refused, not escaped into one", g.path)
			}
		})
	}
}

// The two key sets are two Google endpoints, and the service must be pointed
// at each one for its own token kind: a source built with no option is the
// run identity's, and the caller's is that same source pointed at Google's
// own certificates.
func TestGoogleKeySourcesAreTheTwoGoogleEndpoints(t *testing.T) {
	if got := NewGoogleKeys().url; got != GoogleKeysURL {
		t.Errorf("a key source with no options reads %q, want the Firebase set %q", got, GoogleKeysURL)
	}
	if GoogleKeysURL == GoogleOAuthCertsURL {
		t.Error("the run identity's key set and the caller's are the same URL: one of them is wrong")
	}
	if got := NewGoogleKeys(WithKeysEndpoint(GoogleOAuthCertsURL)).url; got != GoogleOAuthCertsURL {
		t.Errorf("the caller's key source reads %q, want %q", got, GoogleOAuthCertsURL)
	}
}

// fakeGetter is the one database call RTDBOutcomes makes.
type fakeGetter struct {
	path  string
	found bool
	err   error
}

func (g *fakeGetter) Get(_ context.Context, path string, out any) (bool, error) {
	g.path = path
	if g.err != nil {
		return false, g.err
	}
	if raw, ok := out.(*json.RawMessage); ok && g.found {
		*raw = json.RawMessage(`{"status":"done"}`)
	}
	return g.found, nil
}

// The real key source: Google's published certificates, fetched once and
// cached for the life the response's Cache-Control gives, and for the hour the
// source falls back to when the header says nothing.
//
// What happens for a kid the set does not hold — one read more before the
// refusal, rate-limited — is
// TestGoogleKeysReadAgainOnceForAKidTheSetDoesNotHold's, and the clamps are
// TestCacheLifeTakesEveryDirectiveAtItsWord's; this test only ever asks for
// the kid the server publishes.
func TestGoogleKeysAreCached(t *testing.T) {
	key := testKey()
	pemBytes := selfSignedPEM(t, key)
	var fetches atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		// Two hours: distinct from the source's own default life, so the test
		// can tell "Google's max-age was honoured" from "the default was".
		w.Header().Set("Cache-Control", "public, max-age=7200")
		w.Header().Set("Content-Type", "application/json")
		body, err := json.Marshal(map[string]string{testKid: string(pemBytes)})
		if err != nil {
			t.Errorf("marshalling the key set: %v", err)
			return
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	now := testNow()
	keys := NewGoogleKeys(WithKeysEndpoint(srv.URL), WithKeysClock(func() time.Time { return now }))
	got, err := keys.Key(context.Background(), testKid)
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	if !got.Equal(&key.PublicKey) {
		t.Error("Key returned another key than the published one")
	}
	if _, err := keys.Key(context.Background(), testKid); err != nil {
		t.Fatalf("Key (cached): %v", err)
	}
	if n := fetches.Load(); n != 1 {
		t.Errorf("fetches = %d, want 1: the key set is cached", n)
	}
	// An hour on, the set is still good: the published max-age is what the
	// cache lives by, not the source's own default.
	now = now.Add(time.Hour)
	if _, err := keys.Key(context.Background(), testKid); err != nil {
		t.Fatalf("Key (an hour on): %v", err)
	}
	if n := fetches.Load(); n != 1 {
		t.Errorf("fetches = %d, want 1: the published max-age was not honoured", n)
	}
	// Past the published life the set is fetched again.
	now = now.Add(2 * time.Hour)
	if _, err := keys.Key(context.Background(), testKid); err != nil {
		t.Fatalf("Key (after expiry): %v", err)
	}
	if n := fetches.Load(); n != 2 {
		t.Errorf("fetches = %d, want 2 after the cache expired", n)
	}
}

// TestGoogleKeysReadAgainOnceForAKidTheSetDoesNotHold pins what happens when a
// token names a kid the cached set does not hold — the shape a Google key
// rotation takes. The set is read once more, because the alternative is
// refusing every call in a new run until the cache's life runs out; and the
// extra read is rate-limited, because the kid is the caller's text and a
// caller naming random kids must not be able to make this service read
// Google's endpoint in a loop.
func TestGoogleKeysReadAgainOnceForAKidTheSetDoesNotHold(t *testing.T) {
	key := testKey()
	pemBytes := selfSignedPEM(t, key)
	var mu sync.Mutex
	published := map[string]string{testKid: string(pemBytes)}
	var fetches atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		w.Header().Set("Cache-Control", "public, max-age=7200")
		mu.Lock()
		body, err := json.Marshal(published)
		mu.Unlock()
		if err != nil {
			t.Errorf("marshalling the key set: %v", err)
			return
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	now := testNow()
	keys := NewGoogleKeys(WithKeysEndpoint(srv.URL), WithKeysClock(func() time.Time { return now }))
	if _, err := keys.Key(context.Background(), testKid); err != nil {
		t.Fatalf("Key: %v", err)
	}
	if n := fetches.Load(); n != 1 {
		t.Fatalf("fetches = %d, want 1", n)
	}

	// A kid the cached set does not hold: the set is read once more.
	_, err := keys.Key(context.Background(), testOtherKid)
	if !errors.Is(err, ErrUnknownKeyID) {
		t.Fatalf("err = %v, want ErrUnknownKeyID for a kid Google does not publish", err)
	}
	if n := fetches.Load(); n != 2 {
		t.Errorf("fetches = %d, want 2: an unknown kid reads the set again", n)
	}
	if strings.Contains(err.Error(), testOtherKid) {
		t.Errorf("err = %q repeats the key id the caller chose", err)
	}

	// Within the floor, a second unknown kid does not read again.
	now = now.Add(keyCacheMin - time.Second)
	if _, err := keys.Key(context.Background(), testOtherKid); !errors.Is(err, ErrUnknownKeyID) {
		t.Fatalf("err = %v, want ErrUnknownKeyID", err)
	}
	if n := fetches.Load(); n != 2 {
		t.Errorf("fetches = %d, want 2: the extra read is rate-limited", n)
	}

	// A hostile kid is refused without being repeated, whatever the set says.
	hostile := "kid-" + strings.Repeat("A", 4096) + "\nX-Evil: 1"
	if _, err := keys.Key(context.Background(), hostile); !errors.Is(err, ErrUnknownKeyID) {
		t.Fatalf("err = %v, want ErrUnknownKeyID", err)
	}
	for _, fragment := range []string{hostile, "AAAA", "X-Evil"} {
		if strings.Contains(err.Error(), fragment) {
			t.Fatalf("err = %q carries the key id the caller chose", err)
		}
	}
	if n := fetches.Load(); n != 2 {
		t.Errorf("fetches = %d, want 2: a hostile kid does not read the set either", n)
	}

	// The rotation lands: past the floor, the next read finds the new kid.
	now = now.Add(2 * time.Second)
	mu.Lock()
	published[testOtherKid] = string(pemBytes)
	mu.Unlock()
	got, err := keys.Key(context.Background(), testOtherKid)
	if err != nil {
		t.Fatalf("Key after the rotation: %v", err)
	}
	if !got.Equal(&key.PublicKey) {
		t.Error("Key returned another key than the published one")
	}
	if n := fetches.Load(); n != 3 {
		t.Errorf("fetches = %d, want 3 after the floor passed", n)
	}
}

// TestCacheLifeTakesEveryDirectiveAtItsWord pins the bounds around Google's
// Cache-Control. A response that says the set may not be cached, or that says
// nothing this code reads, must not end up cached for an hour by accident.
func TestCacheLifeTakesEveryDirectiveAtItsWord(t *testing.T) {
	cases := map[string]time.Duration{
		"":                                    keyCacheDefault,
		"private":                             keyCacheDefault,
		"max-age=notanumber":                  keyCacheDefault,
		"public, max-age=3600":                time.Hour,
		"public, max-age=60, must-revalidate": time.Minute,
		"public, max-age=0":                   keyCacheMin,
		"public, max-age=-5":                  keyCacheMin,
		"no-store":                            keyCacheMin,
		"no-cache":                            keyCacheMin,
		// A max-age later in the same header does not overrule a directive
		// that says the set may not be kept: the order of the directives is
		// Google's, not a licence to cache what it said not to.
		"no-store, max-age=3600":          keyCacheMin,
		"max-age=3600, no-store":          keyCacheMin,
		"no-cache, max-age=7200":          keyCacheMin,
		"public, no-store, max-age=86400": keyCacheMin,
		"max-age=86400":                   keyCacheMax,
		"max-age=999999999":               keyCacheMax,
	}
	for cacheControl, want := range cases {
		if got := cacheLife(cacheControl); got != want {
			t.Errorf("cacheLife(%q) = %v, want %v", cacheControl, got, want)
		}
	}
}

// The key source is fail-closed: every way of not having the set is
// ErrUnavailable, which is a refusal of the call and never a fallback (D143).
// A set that cannot be read is not an empty set, and a certificate that cannot
// be read is not skipped — admitting a token because one entry of a broken
// answer was unreadable would be reading that answer as a licence.
func TestGoogleKeysFailClosed(t *testing.T) {
	pemBytes := selfSignedPEM(t, testKey())
	set := func(m map[string]string) []byte {
		body, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshalling the key set: %v", err)
		}
		return body
	}
	cases := map[string]http.HandlerFunc{
		// A status that is not 200, carrying a body that would parse: the
		// status is what says the answer is not the set, and a 403 that looks
		// like one must not be read as one.
		"a non-200 answer": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write(set(map[string]string{testKid: string(pemBytes)}))
		},
		"a body that is not JSON": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("no keys here"))
		},
		"an empty set": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("{}"))
		},
		// One good certificate and one that cannot be read: the good one must
		// not be installed either, because a set read in part is not the set
		// Google published.
		"a certificate that cannot be read": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(set(map[string]string{testKid: string(pemBytes), testOtherKid: "not a certificate"}))
		},
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(handler)
			defer srv.Close()
			keys := NewGoogleKeys(WithKeysEndpoint(srv.URL), WithKeysClock(testNow))
			if _, err := keys.Key(context.Background(), testKid); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("err = %v, want ErrUnavailable", err)
			}
		})
	}

	// An endpoint that cannot be reached at all is the same refusal: a token is
	// never checked against keys this service could not read.
	t.Run("an unreachable endpoint", func(t *testing.T) {
		keys := NewGoogleKeys(WithKeysEndpoint("http://127.0.0.1:0/keys"), WithKeysClock(testNow))
		if _, err := keys.Key(context.Background(), testKid); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable", err)
		}
	})

	// An endpoint that is not a URL at all is the same refusal, and not an
	// untyped error: a key source is one of the two the service refuses a call
	// on, and every caller of Key switches on ErrUnavailable to tell a service
	// failure from a refusal of the token (review nit).
	t.Run("an endpoint that is not a URL", func(t *testing.T) {
		keys := NewGoogleKeys(WithKeysEndpoint("http://[::1]:namedport/keys"), WithKeysClock(testNow))
		if _, err := keys.Key(context.Background(), testKid); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable", err)
		}
	})

	// A set that was read and then goes bad is not a reason to fall back to the
	// copy in hand: once its life is out, the token is refused rather than
	// checked against keys Google may have retired.
	t.Run("a failed read after the cache's life", func(t *testing.T) {
		pemBytes := selfSignedPEM(t, testKey())
		var broken atomic.Bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if broken.Load() {
				http.Error(w, "no", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Cache-Control", "public, max-age=60")
			body, err := json.Marshal(map[string]string{testKid: string(pemBytes)})
			if err != nil {
				t.Errorf("marshalling the key set: %v", err)
				return
			}
			_, _ = w.Write(body)
		}))
		defer srv.Close()

		now := testNow()
		keys := NewGoogleKeys(WithKeysEndpoint(srv.URL), WithKeysClock(func() time.Time { return now }))
		if _, err := keys.Key(context.Background(), testKid); err != nil {
			t.Fatalf("Key: %v", err)
		}
		broken.Store(true)
		now = now.Add(2 * time.Minute)
		if _, err := keys.Key(context.Background(), testKid); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable: an expired set is not a stale fallback", err)
		}
	})
}

// A token that names no key id is refused by the source's own rule, and
// Google is not read at all: there is no kid to look for, so an empty one must
// not make this service call out.
func TestGoogleKeysRefuseAnEmptyKidWithoutReading(t *testing.T) {
	var fetches atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		http.Error(w, "no", http.StatusInternalServerError)
	}))
	defer srv.Close()

	keys := NewGoogleKeys(WithKeysEndpoint(srv.URL), WithKeysClock(testNow))
	if _, err := keys.Key(context.Background(), ""); !errors.Is(err, ErrUnknownKeyID) {
		t.Fatalf("err = %v, want ErrUnknownKeyID", err)
	}
	if n := fetches.Load(); n != 0 {
		t.Errorf("fetches = %d, want 0: an empty kid is refused before the set is read", n)
	}
}

// selfSignedPEM is one published certificate, the shape Google's metadata
// endpoint returns.
func selfSignedPEM(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "securetoken.test"},
		NotBefore:    testNow().Add(-time.Hour),
		NotAfter:     testNow().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating a certificate: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// TestVerifierIsNotGatedByTheJobTables: the verifier class is the ONE class the
// job tables do not touch (C3.9f, D1068), and the two differences between its
// token check and a job's are stated rather than implied: the folded address is
// matched against AuthConfig.VerifierAccounts, and a verifier is not a job of
// this installation's job set at all — not a per-repository one and not a
// shared project one.
//
// So an address that is a shared project job's service account IS admitted
// here, and the same address is admitted by Verify as well — as the shared
// class its own row names (B5.1): the two routes read two lists, and neither
// gate is the other's. The second case is the same claim one list further on:
// an address that is one of the installation's job accounts is admitted as a
// verifier too, because the verify route reads its own list and no job table.
func TestVerifierIsNotGatedByTheJobTables(t *testing.T) {
	for _, tc := range []struct {
		name  string
		email string
	}{
		{"a shared project job's account", testSharedSA},
		{"a per-repository job's account", testJobSA},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixtureWith(t, func(cfg *AuthConfig) {
				cfg.VerifierAccounts = []string{tc.email}
			})
			tok := f.jobToken(func(c map[string]any) { c["email"] = tc.email })
			got, err := f.auth.VerifyVerifier(context.Background(), tok)
			if err != nil {
				t.Fatalf("VerifyVerifier refused a configured verifier: %v", err)
			}
			if got != tc.email {
				t.Errorf("VerifyVerifier answered %q, want the configured spelling %q", got, tc.email)
			}
			// The run path is unmoved by the verifier list: the same account
			// presenting the same token is answered by its own class, which is
			// the shared one for a shared job's account (B5.1) and the
			// repository's for the fixture's own — the two routes read two
			// lists and neither is the other's gate.
			id, err := f.verify(f.runToken(), tok, f.job)
			if err != nil {
				t.Fatalf("the run path refused a caller the verifier list admits: %v", err)
			}
			want := ClassRepository
			if tc.email == testSharedSA {
				want = ClassShared
			}
			if id.JobClass != want {
				t.Errorf("the run path answered the class %q, want %q", id.JobClass, want)
			}
		})
	}

	t.Run("the list is empty by default", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.auth.VerifyVerifier(context.Background(), f.jobToken()); !errors.Is(err, ErrUnverifiedJob) {
			t.Errorf("an installation with no verifier admitted a caller: %v", err)
		}
	})

	t.Run("an address the list does not hold is refused", func(t *testing.T) {
		f := newFixtureWith(t, func(cfg *AuthConfig) { cfg.VerifierAccounts = []string{testVerifierSA} })
		if _, err := f.auth.VerifyVerifier(context.Background(), f.jobToken()); !errors.Is(err, ErrUnverifiedJob) {
			t.Errorf("VerifyVerifier admitted an address outside its list: %v", err)
		}
	})

	t.Run("the address is matched by the same ASCII fold", func(t *testing.T) {
		f := newFixtureWith(t, func(cfg *AuthConfig) { cfg.VerifierAccounts = []string{testVerifierSA} })
		upper := strings.ToUpper(testVerifierSA)
		tok := f.jobToken(func(c map[string]any) { c["email"] = upper })
		got, err := f.auth.VerifyVerifier(context.Background(), tok)
		if err != nil {
			t.Fatalf("VerifyVerifier refused a verifier whose claim is the same address in another case: %v", err)
		}
		if got != testVerifierSA {
			t.Errorf("VerifyVerifier answered %q, want the CONFIGURED spelling %q", got, testVerifierSA)
		}
	})

	t.Run("an address that is not printable ASCII is a configuration error", func(t *testing.T) {
		f := newFixture(t)
		_, err := NewAuthenticator(AuthConfig{
			Project:         testProject,
			FirebaseProject: testFirebase,
			Audience:        testAudience,
			JobAccounts:     []string{testJobSA},
			Keys:            f.keys,
			JobKeys:         f.jobKeys,
			Outcomes:        RTDBOutcomes{DB: f.db},
			// U+212A KELVIN SIGN folds to 'k' under a Unicode fold, which is
			// exactly what the ASCII-only rule is for: an address the issuer
			// let a caller spell must not become one an operator configured.
			VerifierAccounts: []string{"verifierK@aurora-dev.iam.gserviceaccount.com"},
		})
		if err == nil {
			t.Fatal("NewAuthenticator accepted a verifier address that is not printable ASCII")
		}
		if !strings.Contains(err.Error(), "verifier") {
			t.Errorf("the configuration error is %q, want it to be about the verifier list", err)
		}
	})
}

// testCoordinatorAccount is the coordinator's own account (BB5, B5): the one
// address the read mint's second class admits. It has the shape every account
// of this installation has — a service account of the project — which is also
// why the person class can never admit it and why the two lists cannot be read
// as one (G4). Like the other identities here it is a name and not a
// credential.
const testCoordinatorAccount = "fugaro-coordinator@aurora-dev.iam.gserviceaccount.com"

// testOtherCoordinator is an account of the same shape that is NOT this
// installation's coordinator: what a second deployment's coordinator, or the
// coordinator's planned resident service (coordinator P7), would present.
const testOtherCoordinator = "fugaro-coordinator-resident@aurora-dev.iam.gserviceaccount.com"

// coordinatorSvc is the read mint's own fixture with the coordinator class
// wired (BB5): the same repository, the same read consumer and the same fake
// endpoint, plus the one account the second class admits. people is the person
// list, so a test holds one class or both.
func coordinatorSvc(t *testing.T, coordinator string, people ...string) (*svc, *bitbucketFake) {
	t.Helper()
	return bitbucketSvcWith(t, func(s *svc, cfg *Config) {
		ac := s.auth.cfg
		// The people list is restated on the configuration this rebuilds:
		// bitbucketSvcWith wires the read route's classes into cfg.Auth, and the
		// authenticator s.auth still carries is the one this fixture's second
		// list is read off.
		ac.PersonAccounts = people
		ac.CoordinatorAccount = coordinator
		a, err := NewAuthenticator(ac)
		if err != nil {
			t.Fatalf("NewAuthenticator: %v", err)
		}
		cfg.Auth, s.auth = a, a
	}, people...)
}

// TestCoordinatorCallerOnlyOnReadRoute: the coordinator class is the read
// mint's SECOND admission (BB5, G4), and it is the route's like the person
// class's: the coordinator's own token opens the read mint, and the same token
// beside a whole run identity is refused on the model path, which is every
// other caller's route. The class exists for the sweep, which is not a run of
// this installation and holds no run identity either.
func TestCoordinatorCallerOnlyOnReadRoute(t *testing.T) {
	s, _ := coordinatorSvc(t, testCoordinatorAccount)

	resp, body := s.readMint(t, testBitbucketRepo, testCoordinatorAccount)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the read mint refused the coordinator account: HTTP %d %s", resp.StatusCode, body)
	}
	var answer readAnswer
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatalf("the answer is not one this contract can read: %v (%d bytes)", err, len(body))
	}
	if answer.Token == "" || answer.ExpiresAt == "" || answer.Repository != testBitbucketRepo {
		t.Errorf("the answer does not name the token, its life and the repository: %+v", answer)
	}

	resp = s.postTokens(t, MessagesPath, callBody("coordinator-on-the-model-path"), s.runToken(), s.personToken(testCoordinatorAccount), s.job)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the refusal: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the model path admitted the coordinator's account: HTTP %d %s", resp.StatusCode, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorUnverifiedJobIdentity {
		t.Errorf("the refusal is %q, want %q", e.Error, ErrorUnverifiedJobIdentity)
	}
}

// TestCoordinatorCallerRefusedOtherAccounts: the class names ONE account, and
// an address that is not it is refused whether or not it looks like a
// coordinator's. The residency the class is written against is the point: the
// coordinator's planned resident service (coordinator P7) has exactly this
// shape and is not this installation's coordinator account, so it is refused
// with the read consumer never exchanged (G4).
//
// The second case is the other half of the same rule read the other way: an
// installation that admits people and has a coordinator does not admit a
// person through the COORDINATOR's entry, and one whose people list is empty —
// the case's own fixture — names no person at all. The two lookups are two
// lists and neither widens to the other's addresses.
func TestCoordinatorCallerRefusedOtherAccounts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		account string
		people  []string
	}{
		{"another account of the coordinator's own shape", testOtherCoordinator, []string{testPersonAccount}},
		{"an address on neither list", testPersonAccount, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f := coordinatorSvc(t, testCoordinatorAccount, tc.people...)
			resp, body := s.readMint(t, testBitbucketRepo, tc.account)
			if resp.StatusCode != StatusUnverified {
				t.Fatalf("the read mint admitted %s: HTTP %d %s", tc.name, resp.StatusCode, body)
			}
			if n := len(f.seen()); n != 0 {
				t.Errorf("the service exchanged the read consumer %d times for a caller it refused, want none", n)
			}
			// An unverified caller has no name for a line, so the refusal is
			// typed and the audit log stays empty.
			if got := s.audit.String(); got != "" {
				t.Errorf("a refusal taken before the caller was verified wrote an audit line: %s", got)
			}
		})
	}
}

// TestCoordinatorClassEmptyEmailAdmitsNobody: the service running with the
// coordinator OFF admits nobody through this class. An empty account is not a
// wildcard, which is the reading a default could take and this one does not:
// the class is the installation's own coordinator, and an installation that
// has none has named none (BB5, F24).
func TestCoordinatorClassEmptyEmailAdmitsNobody(t *testing.T) {
	s, f := coordinatorSvc(t, "")
	resp, body := s.readMint(t, testBitbucketRepo, testCoordinatorAccount)
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the read mint admitted the coordinator's account with no coordinator configured: HTTP %d %s", resp.StatusCode, body)
	}
	if n := len(f.seen()); n != 0 {
		t.Errorf("the service exchanged the read consumer %d times for a caller it refused, want none", n)
	}
}

// TestRunIdentityRefusedOnReadRoute: the read route takes no run identity, from
// either class it admits. A run identity is a shape of call this route does not
// have (BB4), and BB5's second class does not change that: a caller that
// presents one is refused by name whichever account it is, which is what keeps
// the route from becoming a second way in for a run.
func TestRunIdentityRefusedOnReadRoute(t *testing.T) {
	for _, tc := range []struct {
		name  string
		class string
		extra func(*http.Request)
	}{
		{"the coordinator's account presents a run identity", testCoordinatorAccount, func(r *http.Request) { r.Header.Set(HeaderRunIdentity, "a-run-identity-token") }},
		{"the coordinator's account names a job", testCoordinatorAccount, func(r *http.Request) { r.Header.Set(HeaderRunJob, "fugaro-a-job") }},
		{"a person presents a run identity", testPersonAccount, func(r *http.Request) { r.Header.Set(HeaderRunIdentity, "a-run-identity-token") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f := coordinatorSvc(t, testCoordinatorAccount, testPersonAccount)
			resp, body := s.readMint(t, testBitbucketRepo, tc.class, tc.extra)
			if resp.StatusCode != StatusUnverified {
				t.Fatalf("the read mint answered a call where %s: HTTP %d %s", tc.name, resp.StatusCode, body)
			}
			if e := refusal(t, resp.StatusCode, body); e.Error != ErrorUnverifiedRunIdentity {
				t.Errorf("the refusal is %q, want %q", e.Error, ErrorUnverifiedRunIdentity)
			}
			if n := len(f.seen()); n != 0 {
				t.Errorf("the service exchanged the read consumer %d times for a call it refused, want none", n)
			}
			// The refusal IS recorded: the caller was verified by its own
			// class, so the line names it and the act it asked for, and it
			// carries no run and no job.
			lines := auditLines(t, s.audit)
			if len(lines) != 1 {
				t.Fatalf("the refused call wrote %d audit lines, want exactly 1: %v", len(lines), lines)
			}
			checkAuditShape(t, lines[0])
			for field, want := range map[string]any{
				"caller":     tc.class,
				"purpose":    PurposeBitbucketRead,
				"status":     ErrorUnverifiedRunIdentity,
				"run":        "",
				"job":        "",
				"tier":       "",
				"repository": "",
			} {
				if got := lines[0][field]; got != want {
					t.Errorf("the refusal's audit line %s = %v, want %q", field, got, want)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The launcher class (DS1.2, D1938, F5)
// ---------------------------------------------------------------------------

// testLauncherAccount is one of an installation's launchers: the address a
// Google ID token's email claim carries for a member who may ask the service
// for a slip (D1938). Like every identity in this file it is a name and not a
// credential.
const testLauncherAccount = "launcher-1@example.invalid"

// testLauncherSub is the Google account id a launcher's token carries in its
// sub claim, which launcherToken writes into every token below: the intake
// builds a secret's prefix from this value (R1, R3), so the tests read it out
// and change it rather than spelling a second one.
//
// It is spelled at the width a real account id has — 21 digits, above 2^53
// (internal/infra/tf's identity handling records the same fact of a service
// account's unique_id) — and deliberately not as a short round number: a class
// whose bound on this claim was derived from a 64-bit number would refuse every
// launcher there is, and a fixture of ten digits could not tell that from a
// class that works.
const testLauncherSub = "104329501182774832335"

// launcherFixture is the fixture with the launcher class wired (D1938): the
// addresses the class admits, which is the installation's roster as `update`
// derives it. The person list is left as the fixture has it — empty — so a
// test that wants both classes says so.
func launcherFixture(t *testing.T, launchers ...string) *fixture {
	t.Helper()
	return newFixtureWith(t, func(cfg *AuthConfig) { cfg.LauncherAccounts = launchers })
}

// launcherToken is the Google ID token a launcher presents: the caller's-token
// shape, with the launcher's address, the fixture's numeric sub, and the
// audience a plain `gcloud auth print-identity-token` mints for a user account
// (GcloudAudience) — the only audience a user account can mint at all, which
// is why the class admits it beside the service's own URL.
func (f *fixture) launcherToken(mutate ...func(map[string]any)) string {
	return f.jobToken(append([]func(map[string]any){
		func(c map[string]any) {
			c["email"] = testLauncherAccount
			c["aud"] = GcloudAudience
			// The account id is the launcher's own: jobClaims' default is a job
			// service account's, and this class answers the token's sub.
			c["sub"] = testLauncherSub
		},
	}, mutate...)...)
}

// verifyLauncher is the class's own entry point, called the way the grant
// route and the intake call it.
func (f *fixture) verifyLauncher(tok string) (LauncherCaller, error) {
	return f.auth.VerifyLauncher(context.Background(), tok)
}

// TestLauncherSubIsTheTokenSub: the launcher class's answer is the caller's
// Google account id, and it is the TOKEN's sub claim and nothing else — not
// the address, not a value the deployment configured. The intake derives the
// secret prefix `fugaro_u_<id>_<name>` from exactly this value (R1, R3), so a
// class that answered with anything else would write every secret of a
// member's installation under a prefix that is not theirs.
//
// The refusals are the fail-closed half of the same rule: the grammar the
// intake builds a name from is digits only (R1), so a token whose sub is
// missing, is not a string, carries anything but digits or is wider than a
// Google account id is, is refused rather than carried into a name. The
// audience half is here too, because it is the one fact about the class that
// is not its list: a user account can mint gcloud's own audience and this
// service's URL, and nothing else (D1944 accepts the replay of the first).
func TestLauncherSubIsTheTokenSub(t *testing.T) {
	f := launcherFixture(t, testLauncherAccount)

	ctx := context.Background()
	// The account is answered in the configured spelling and the id is the
	// claim's. The deployment is asked for the address in another case below,
	// which is the fold every list of this package is matched by.
	got, err := f.verifyLauncher(f.launcherToken())
	if err != nil {
		t.Fatalf("VerifyLauncher refused a configured launcher: %v", err)
	}
	if got.Account != testLauncherAccount {
		t.Errorf("the launcher account = %q, want the configured spelling %q", got.Account, testLauncherAccount)
	}
	if got.Sub != testLauncherSub {
		t.Errorf("the launcher sub = %q, want the token's sub claim %q", got.Sub, testLauncherSub)
	}

	// Another account id in the claim is what the class answers with: the sub
	// is the token's and not a value read off the list or the address.
	got, err = f.verifyLauncher(f.launcherToken(func(c map[string]any) { c["sub"] = "9876543210" }))
	if err != nil {
		t.Fatalf("VerifyLauncher refused a launcher whose token names another account id: %v", err)
	}
	if got.Sub != "9876543210" {
		t.Errorf("the launcher sub = %q, want the token's own sub claim %q", got.Sub, "9876543210")
	}

	// The class's audience is gcloud's own client id and this service's URL,
	// and no third one: the token a person's machine mints carries either.
	for name, aud := range map[string]string{
		"the service's own audience": testAudience,
		"gcloud's own audience":      GcloudAudience,
	} {
		tok := f.launcherToken(func(c map[string]any) { c["aud"] = aud })
		if _, err := f.verifyLauncher(tok); err != nil {
			t.Errorf("VerifyLauncher refused a launcher whose token is for %s: %v", name, err)
		}
	}
	if _, err := f.verifyLauncher(f.launcherToken(func(c map[string]any) { c["aud"] = "https://another-service.invalid" })); err == nil {
		t.Error("VerifyLauncher admitted a launcher whose token is for another audience")
	}

	// A sub the prefix grammar could not be built from is refused: the class
	// answers a whole identity or nothing, and never a partial one.
	for name, sub := range map[string]any{
		"absent":                    nil,
		"not a string":              1234567890,
		"empty":                     "",
		"letters":                   "acct-1",
		"a decimal point":           "1234567890.5",
		"a minus sign":              "-1234567890",
		"a leading space":           " 1234567890",
		"wider than any account id": strings.Repeat("1", maxLauncherSubDigits+1),
		"a trailing newline":        testLauncherSub + "\n",
		"a zero-width space":        "1234567890​",
		"an ideographic zero":       "０１",
	} {
		t.Run("a sub that is "+name, func(t *testing.T) {
			_, err := f.verifyLauncher(f.launcherToken(func(c map[string]any) { c["sub"] = sub }))
			if !errors.Is(err, ErrUnverifiedJob) {
				t.Fatalf("a token whose sub is %s was answered %v, want ErrUnverifiedJob: no secret prefix can be derived from it", name, err)
			}
			if err != nil && strings.Contains(err.Error(), testLauncherSub) && sub != testLauncherSub {
				t.Errorf("the refusal is %q, and it carries the account id it refused", err)
			}
		})
	}

	// The address is matched by the same ASCII fold every other list is read
	// by, and the spelling the identity carries is the configured one.
	tok := f.launcherToken(func(c map[string]any) { c["email"] = strings.ToUpper(testLauncherAccount) })
	got, err = f.auth.VerifyLauncher(ctx, tok)
	if err != nil {
		t.Fatalf("VerifyLauncher refused a launcher whose claim is the same address in another case: %v", err)
	}
	if got.Account != testLauncherAccount {
		t.Errorf("the launcher account = %q, want the CONFIGURED spelling %q", got.Account, testLauncherAccount)
	}
}

// TestLauncherRefusedWithoutVerifiedEmail: the launcher class is keyed on the
// verified address a Google ID token names (D1938), so a token that does not
// carry a verified email is refused however well it is otherwise shaped. It is
// the same check every caller class of this service is held to
// (checkCallerToken), asked here because it is the class's own key: the grant
// a slip records and the prefix the intake writes are the member's, and an
// address an issuer did not verify is one no roster entry can be matched
// against.
func TestLauncherRefusedWithoutVerifiedEmail(t *testing.T) {
	f := launcherFixture(t, testLauncherAccount)
	for name, mutate := range map[string]func(map[string]any){
		"the claim is false":         func(c map[string]any) { c["email_verified"] = false },
		"the claim is absent":        func(c map[string]any) { delete(c, "email_verified") },
		"the claim is not a boolean": func(c map[string]any) { c["email_verified"] = "true" },
		"the claim is null":          func(c map[string]any) { c["email_verified"] = nil },
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.verifyLauncher(f.launcherToken(mutate))
			if !errors.Is(err, ErrUnverifiedJob) {
				t.Fatalf("err = %v, want ErrUnverifiedJob: a launcher whose email is not verified is not admitted", err)
			}
			if strings.Contains(err.Error(), testLauncherAccount) {
				t.Errorf("the refusal is %q, and it carries the address it refused", err)
			}
		})
	}

	// The control: the same token with the claim set is admitted, so the four
	// cases above are about the claim and not about the token.
	if _, err := f.verifyLauncher(f.launcherToken()); err != nil {
		t.Fatalf("VerifyLauncher refused a launcher whose email is verified: %v", err)
	}
}

// TestLauncherClassRefusedOnRunAndReadRoutes: the launcher class is admitted
// on the grant route and the intake ONLY (D1938, G13), and this is the whole
// of that restriction read at the four entry points the other routes are
// answered by: the shared parse (Verify — the mint, the sign route, the model
// path and the Bitbucket proxy), the Bitbucket read mint (VerifyReadCaller),
// the GitHub read mint (VerifyPersonCaller) and the verify route
// (VerifyVerifier).
//
// The token is the strongest one the class admits: a configured launcher, for
// this service's own audience, with a verified email and a numeric sub, so a
// refusal below is the class's own and not an audience's or a claim's. Which
// is what makes this a test of the classes and not of one route: a mutant that
// added the launcher list to any of the four lookups turns exactly its own
// case green-where-it-must-be-red.
func TestLauncherClassRefusedOnRunAndReadRoutes(t *testing.T) {
	f := launcherFixture(t, testLauncherAccount)
	ctx := context.Background()
	// The token every route below is presented: the service's own audience,
	// which is what a launcher that mints for the service rather than through
	// gcloud presents.
	tok := f.launcherToken(func(c map[string]any) { c["aud"] = testAudience })

	// The control: the class itself admits the very token the routes refuse.
	if _, err := f.verifyLauncher(tok); err != nil {
		t.Fatalf("VerifyLauncher refused the token the routes below are presented: %v", err)
	}

	for _, route := range []struct {
		name string
		call func() error
	}{
		{
			name: "the run routes (the mint, the sign route, the model path, the Bitbucket proxy)",
			call: func() error { _, err := f.verify(f.runToken(), tok, f.job); return err },
		},
		{
			name: "the Bitbucket read mint",
			call: func() error { _, err := f.auth.VerifyReadCaller(ctx, tok); return err },
		},
		{
			name: "the GitHub read mint",
			call: func() error { _, err := f.auth.VerifyPersonCaller(ctx, tok); return err },
		},
		{
			name: "the verify route",
			call: func() error { _, err := f.auth.VerifyVerifier(ctx, tok); return err },
		},
	} {
		t.Run(route.name, func(t *testing.T) {
			err := route.call()
			if !errors.Is(err, ErrUnverifiedJob) {
				t.Fatalf("err = %v, want ErrUnverifiedJob: a launcher is admitted on the grant route and the intake only", err)
			}
			if strings.Contains(err.Error(), testLauncherAccount) {
				t.Errorf("the refusal is %q, and it carries the address it refused", err)
			}
		})
	}
}

// TestPersonClassStillOperatorsOnly: the launcher class is a NEW list and the
// person list is untouched (D1938), which is the decision's whole point —
// "widen the person class" was the alternative, and it would have handed every
// member the workspace-wide read mints the operators hold. So the two facts
// are pinned together:
//
//   - the read mints stay operators-only: a launcher who is not one of the
//     installation's people is refused by both, and by the GitHub read mint;
//   - the two lists may hold the same address, because they are two classes of
//     DIFFERENT routes and an installation whose operators launch runs has
//     exactly that — the overlap rule NewAuthenticator holds the person list
//     to is about one route's two classes;
//   - and neither list widens into the other's class: an operator who is not a
//     launcher is refused by the launcher class.
func TestPersonClassStillOperatorsOnly(t *testing.T) {
	const operator = "operator-1@example.invalid"
	ctx := context.Background()

	f := newFixtureWith(t, func(cfg *AuthConfig) {
		cfg.LauncherAccounts = []string{testLauncherAccount, operator}
		cfg.PersonAccounts = []string{operator}
	})

	// The operator is admitted by both routes' classes: both audiences, since
	// the person class admits gcloud's own client id and so does the launcher
	// class.
	operatorToken := f.jobToken(func(c map[string]any) { c["email"] = operator })
	if _, err := f.auth.VerifyReadCaller(ctx, operatorToken); err != nil {
		t.Fatalf("the Bitbucket read mint refused an operator: %v", err)
	}
	if _, err := f.auth.VerifyPersonCaller(ctx, operatorToken); err != nil {
		t.Fatalf("the GitHub read mint refused an operator: %v", err)
	}
	if got, err := f.verifyLauncher(operatorToken); err != nil || got.Account != operator {
		t.Fatalf("the launcher class refused an operator who is on the launcher list: %v", err)
	}

	// A launcher who is not one of the installation's people reaches the
	// launcher class and neither read mint.
	launcherToken := f.launcherToken()
	if _, err := f.verifyLauncher(launcherToken); err != nil {
		t.Fatalf("the launcher class refused a configured launcher: %v", err)
	}
	if _, err := f.auth.VerifyReadCaller(ctx, launcherToken); !errors.Is(err, ErrUnverifiedJob) {
		t.Errorf("the Bitbucket read mint answered a launcher who is not an operator with %v, want ErrUnverifiedJob: the read mints stay operators-only", err)
	}
	if _, err := f.auth.VerifyPersonCaller(ctx, launcherToken); !errors.Is(err, ErrUnverifiedJob) {
		t.Errorf("the GitHub read mint answered a launcher who is not an operator with %v, want ErrUnverifiedJob: the read mints stay operators-only", err)
	}

	// The other direction: the person list does not widen into the launcher
	// class either. This installation's launchers are the two above; an
	// operator the roster does not list as one launches nothing.
	g := newFixtureWith(t, func(cfg *AuthConfig) {
		cfg.LauncherAccounts = []string{testLauncherAccount}
		cfg.PersonAccounts = []string{operator}
	})
	if _, err := g.verifyLauncher(g.jobToken(func(c map[string]any) { c["email"] = operator })); !errors.Is(err, ErrUnverifiedJob) {
		t.Errorf("the launcher class admitted a person who is not on the launcher list (%v), want ErrUnverifiedJob", err)
	}
	if _, err := g.verifyLauncher(g.launcherToken()); err != nil {
		t.Errorf("the launcher class refused a configured launcher: %v", err)
	}
}

// TestServiceAccountRefusedAtIntakeAuth: the launcher class is people, and a
// service account is not one (D1938, G13 — "a person and a launcher on the
// claim, a service account at the intake"). Both halves of that are here:
//
//   - a deployment cannot name one: an installation's launchers come from its
//     roster, so an entry of the service-account shape is a deployment that
//     disagrees with itself — the rule the person list is already held to —
//     and it is refused by name, carrying no address;
//   - and a service account's token is refused at the class, however
//     perfectly it is otherwise shaped (the service's own audience, a verified
//     email, a numeric sub): the class is keyed on the roster's addresses, and
//     no service account is on it.
func TestServiceAccountRefusedAtIntakeAuth(t *testing.T) {
	for _, tc := range []struct {
		name string
		sa   string
	}{
		{"a job of this installation", testJobSA},
		{"a shared project job's account", testSharedSA},
		{"the credentials service's own shape, in another project", "some-machine@another-project.iam.gserviceaccount.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			_, err := NewAuthenticator(AuthConfig{
				Project:          testProject,
				FirebaseProject:  testFirebase,
				Audience:         testAudience,
				JobAccounts:      []string{testJobSA},
				Keys:             f.keys,
				JobKeys:          f.jobKeys,
				Outcomes:         RTDBOutcomes{DB: f.db},
				LauncherAccounts: []string{tc.sa},
			})
			if err == nil {
				t.Fatalf("NewAuthenticator admitted %s as a launcher", tc.name)
			}
			if !strings.Contains(err.Error(), "launcher") || !strings.Contains(err.Error(), "service account address") {
				t.Errorf("the configuration error is %q, want it to be about a launcher that is a service account address", err)
			}
			if strings.Contains(err.Error(), tc.sa) {
				t.Errorf("the configuration error is %q, and it carries the address it refused", err)
			}
		})
	}

	// The token half: the service account's own Google ID token, shaped as
	// well as a caller's token can be, is refused by the class.
	f := launcherFixture(t, testLauncherAccount)
	tok := f.jobToken(func(c map[string]any) { c["email"] = testJobSA })
	if _, err := f.verifyLauncher(tok); !errors.Is(err, ErrUnverifiedJob) {
		t.Fatalf("the launcher class answered a service account's token with %v, want ErrUnverifiedJob", err)
	}
}
