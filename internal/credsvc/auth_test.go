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
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
)

// The identities in these tests are built at run time: an RSA key generated
// here, a token signed with it, and claim values that are test names. No
// literal in this file is, or looks like, a credential (Convention 2).
const (
	testProject  = "aurora"     // fp: the Fugaro project
	testFirebase = "aurora-dev" // aud/iss of the run's ID token
	testAudience = "https://creds-aurora-dev-uc.a.run.app"
	testJobSA    = "fugaro-job@aurora-dev.iam.gserviceaccount.com"
	testOtherSA  = "another-job@aurora-dev.iam.gserviceaccount.com"
	testKid      = "test-kid-1"
	testOtherKid = "test-kid-2"
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

// staticKeys is a KeySource over a fixed set: the shape of a cached key set,
// with the same "not here" answer the real one gives.
type staticKeys map[string]*rsa.PublicKey

func (s staticKeys) Key(_ context.Context, kid string) (*rsa.PublicKey, error) {
	k, ok := s[kid]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownKeyID, kid)
	}
	return k, nil
}

// fakeOutcomes is the service's reader of the write-once outcomes. It also
// keeps the days it was asked about, so a test can see how far the liveness
// scan went; agents is what a run's own token may rewrite, and nothing here
// reads it.
type fakeOutcomes struct {
	outcomes map[string]bool
	agents   map[string]bool
	asked    []int64
	err      error
}

func (f *fakeOutcomes) OutcomeExists(_ context.Context, day int64, slug, run string) (bool, error) {
	f.asked = append(f.asked, day)
	if f.err != nil {
		return false, f.err
	}
	return f.outcomes[fmt.Sprintf("%d/%s/%s", day, slug, run)], nil
}

// fixture is one installation's service, with the clock and the state under
// the test's control.
type fixture struct {
	t        *testing.T
	now      time.Time
	slug     string
	run      string
	job      string
	outcomes *fakeOutcomes
	shared   map[string]bool
	auth     *Authenticator
	keys     staticKeys
}

// testNow is the instant every test's clock stands at.
func testNow() time.Time { return time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC) }

func newFixture(t *testing.T) *fixture {
	t.Helper()
	now := testNow()
	f := &fixture{
		t:        t,
		now:      now,
		slug:     "aurora-app",
		run:      now.Format("20060102-150405") + "-ab12",
		job:      "fugaro-aurora-app-review",
		outcomes: &fakeOutcomes{outcomes: map[string]bool{}, agents: map[string]bool{}},
		shared:   map[string]bool{},
		keys:     staticKeys{testKid: &testKey().PublicKey},
	}
	var err error
	f.auth, err = NewAuthenticator(AuthConfig{
		Project:         testProject,
		FirebaseProject: testFirebase,
		Audience:        testAudience,
		JobAccounts:     []string{testJobSA},
		Keys:            f.keys,
		Outcomes:        f.outcomes,
		SharedJob:       func(job string) bool { return f.shared[job] },
		Now:             func() time.Time { return f.now },
	})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	return f
}

// runClaims is the claim set of the run's Firebase ID token: what
// token.Exchange's ID token carries for uid r~<slug>~<run>.
func (f *fixture) runClaims(mutate ...func(map[string]any)) map[string]any {
	c := map[string]any{
		"iss":     "https://securetoken.google.com/" + testFirebase,
		"aud":     testFirebase,
		"sub":     "r~" + f.slug + "~" + f.run,
		"user_id": "r~" + f.slug + "~" + f.run,
		"iat":     f.now.Add(-time.Minute).Unix(),
		"exp":     f.now.Add(30 * time.Minute).Unix(),
		"fs":      f.slug,
		"fr":      f.run,
		"fp":      testProject,
		"fx":      f.now.Add(2 * time.Hour).UnixMilli(),
		"rb":      "operator@example.invalid",
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
	return signJWT(f.t, testKey(), "google-kid", f.jobClaims(mutate...))
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
	// The liveness check asked about the run's launch day: no outcome is
	// written for a run that has not ended.
	if len(f.outcomes.asked) == 0 {
		t.Fatal("the liveness check asked about no day at all")
	}
	if want := budget.Day(f.now); f.outcomes.asked[0] != want {
		t.Errorf("the first day asked about = %d, want today (%d)", f.outcomes.asked[0], want)
	}
}

// An outcome present means the run has ended, whatever its own registry entry
// says — and the scan covers every day from the launch day to today, because
// the outcome is written on the day the run ended.
func TestVerifyRefusesRunWithOutcome(t *testing.T) {
	t.Run("the outcome is written today", func(t *testing.T) {
		f := newFixture(t)
		f.outcomes.outcomes[fmt.Sprintf("%d/%s/%s", budget.Day(f.now), f.slug, f.run)] = true
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
		f.outcomes.outcomes[fmt.Sprintf("%d/%s/%s", budget.Day(launch), f.slug, f.run)] = true
		_, err := f.verify(f.runToken(), f.jobToken(), f.job)
		if !errors.Is(err, ErrUnverified) {
			t.Fatalf("err = %v, want ErrUnverified", err)
		}
		// Both days were asked about: today first, then the launch day.
		if want := []int64{budget.Day(f.now), budget.Day(launch)}; len(f.outcomes.asked) != 2 ||
			f.outcomes.asked[0] != want[0] || f.outcomes.asked[1] != want[1] {
			t.Errorf("days asked = %v, want %v", f.outcomes.asked, want)
		}
	})
}

// agents/<slug>/<run> proves nothing: the rules give the run's own token
// .write on it, so a hostile agent can rewrite or recreate it (review finding
// W2). A live-looking entry does not revive an ended run, and a missing one
// does not end a live run.
func TestVerifyIgnoresAgentsNode(t *testing.T) {
	t.Run("a live looking entry does not revive an ended run", func(t *testing.T) {
		f := newFixture(t)
		f.outcomes.agents[f.slug+"/"+f.run] = true
		f.outcomes.outcomes[fmt.Sprintf("%d/%s/%s", budget.Day(f.now), f.slug, f.run)] = true
		if _, err := f.verify(f.runToken(), f.jobToken(), f.job); !errors.Is(err, ErrUnverified) {
			t.Fatalf("err = %v, want ErrUnverified", err)
		}
	})

	t.Run("a missing entry does not end a live run", func(t *testing.T) {
		f := newFixture(t)
		delete(f.outcomes.agents, f.slug+"/"+f.run)
		f.verifyOK()
	})
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

// C3.9's gate: until the phase-3 shared-job rules are on main, the service
// admits runs in per-repository jobs only.
func TestVerifyRefusesSharedJobRun(t *testing.T) {
	f := newFixture(t)
	shared := "fugaro-p-profile-one-anthropic-acct-one-0123456789ab"
	f.shared[shared] = true
	_, err := f.verify(f.runToken(), f.jobToken(), shared)
	if !errors.Is(err, ErrUnverified) {
		t.Fatalf("err = %v, want ErrUnverified", err)
	}
	if !strings.Contains(err.Error(), "shared") {
		t.Errorf("err = %q, want it to say the job is shared", err)
	}
	// A run that names no job at all cannot be placed, so it is refused too —
	// and so is a job name that is not one clean header value: it is looked
	// up in the policy and written into an audit line.
	unplaceable := map[string]string{
		"no job at all":             "",
		"a newline in the job name": "fugaro-aurora\nX-Evil: 1",
		"a job name past the bound": strings.Repeat("j", maxHeaderTokenBytes+1),
	}
	for name, job := range unplaceable {
		t.Run(name, func(t *testing.T) {
			if _, err := f.verify(f.runToken(), f.jobToken(), job); !errors.Is(err, ErrUnverified) {
				t.Fatalf("err = %v, want ErrUnverified for a run that cannot be placed", err)
			}
		})
	}
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
		"no email at all":     func(c map[string]any) { delete(c, "email") },
		"another audience":    func(c map[string]any) { c["aud"] = "https://another-service.example.invalid" },
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
func TestVerifyErrorsNeverCarryTheToken(t *testing.T) {
	f := newFixture(t)
	runTok := f.runToken(func(c map[string]any) { c["fx"] = f.now.Add(-time.Second).UnixMilli() })
	jobTok := f.jobToken(func(c map[string]any) { c["email"] = testOtherSA })
	_, err := f.verify(runTok, jobTok, f.job)
	if err == nil {
		t.Fatal("Verify accepted a refused identity")
	}
	for _, tok := range []string{runTok, jobTok} {
		if strings.Contains(err.Error(), tok) || strings.Contains(err.Error(), tok[:24]) {
			t.Fatalf("err = %q carries the token", err)
		}
	}
}

// A liveness check the service cannot complete is a refusal, not an
// admission: the outcome is the one thing that ends a run's identity.
func TestVerifyRefusesWhenTheOutcomeCannotBeRead(t *testing.T) {
	f := newFixture(t)
	f.outcomes.err = errors.New("the database is unreachable")
	_, err := f.verify(f.runToken(), f.jobToken(), f.job)
	if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrUnverified) {
		t.Fatalf("err = %v, want ErrUnavailable and not ErrUnverified", err)
	}
}

// A run id that names no launch time cannot be checked day by day, so it is
// refused; and one claiming a launch older than any run can be is refused
// rather than scanned.
func TestVerifyRefusesARunIDItCannotPlace(t *testing.T) {
	unplaceable := map[string]string{
		"a run id too short to hold a time": "not-a-run-id",
		"a run id whose head is not a time": "abcdefgh-ijklmn-ee55",
	}
	for name, run := range unplaceable {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.run = run
			_, err := f.verify(f.runToken(), f.jobToken(), f.job)
			if !errors.Is(err, ErrUnverified) {
				t.Fatalf("err = %v, want ErrUnverified", err)
			}
			if len(f.outcomes.asked) != 0 {
				t.Errorf("days asked = %v, want none: a run whose launch day is unknown is refused, not scanned", f.outcomes.asked)
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
		if len(f.outcomes.asked) != 0 {
			t.Errorf("days asked = %v, want none: the scan is bounded before it starts", f.outcomes.asked)
		}
	})
}

// The construction refuses a service that cannot check what it must check.
func TestNewAuthenticatorRefusesAnIncompleteConfig(t *testing.T) {
	base := AuthConfig{
		Project:         testProject,
		FirebaseProject: testFirebase,
		Audience:        testAudience,
		JobAccounts:     []string{testJobSA},
		Keys:            staticKeys{testKid: &testKey().PublicKey},
		Outcomes:        &fakeOutcomes{},
		SharedJob:       func(string) bool { return false },
	}
	cases := map[string]func(*AuthConfig){
		"no project":          func(c *AuthConfig) { c.Project = "" },
		"no firebase project": func(c *AuthConfig) { c.FirebaseProject = "" },
		"no audience":         func(c *AuthConfig) { c.Audience = "" },
		"no job accounts":     func(c *AuthConfig) { c.JobAccounts = nil },
		"no keys":             func(c *AuthConfig) { c.Keys = nil },
		"no outcomes":         func(c *AuthConfig) { c.Outcomes = nil },
		"no shared job gate":  func(c *AuthConfig) { c.SharedJob = nil },
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
	// A segment budget.PathOutcome would panic on is refused, not panicked on.
	if _, err := (RTDBOutcomes{DB: fake}).OutcomeExists(context.Background(), day, "", run); err == nil {
		t.Error("an empty slug was accepted as a path segment")
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
// cached, with a kid it does not hold answered as unknown rather than fetched
// again.
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
	if _, err := keys.Key(context.Background(), testOtherKid); !errors.Is(err, ErrUnknownKeyID) {
		t.Fatalf("err = %v, want ErrUnknownKeyID for a kid Google does not publish", err)
	}
	if n := fetches.Load(); n != 1 {
		t.Errorf("fetches = %d, want 1: an unknown kid does not refetch", n)
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
