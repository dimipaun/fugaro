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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
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
	// The account of a *shared* project job (gcp.ProjectServiceAccountID):
	// one of the accounts Global Constraint 6's gate refuses.
	testSharedSA = "fugaro-p-profile-one-anthropic-acct-one@aurora-dev.iam.gserviceaccount.com"
	testKid      = "test-kid-1"
	testOtherKid = "test-kid-2"
	// The key id a Google ID token names. Google's own certs and Firebase's
	// are different key sets, so the caller's token is signed by a key the run
	// token's set does not hold.
	testJobKid = "google-kid"
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
		// No kid in the error, exactly as the real source does it: the kid is
		// the caller's own text.
		return nil, fmt.Errorf("%w: the token names a key id Google does not publish", ErrUnknownKeyID)
	}
	return k, nil
}

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

func newFixture(t *testing.T) *fixture {
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
	var err error
	f.auth, err = NewAuthenticator(AuthConfig{
		Project:           testProject,
		FirebaseProject:   testFirebase,
		Audience:          testAudience,
		JobAccounts:       []string{testJobSA},
		Keys:              f.keys,
		JobKeys:           f.jobKeys,
		Outcomes:          RTDBOutcomes{DB: f.db},
		SharedJobAccounts: []string{testSharedSA},
		Now:               func() time.Time { return f.now },
	})
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
	// The liveness check asked about the run's launch day: no outcome is
	// written for a run that has not ended.
	if asked := f.db.daysAsked(); len(asked) == 0 {
		t.Fatal("the liveness check asked about no day at all")
	} else if want := budget.Day(f.now); asked[0] != want {
		t.Errorf("the first day asked about = %d, want today (%d)", asked[0], want)
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
		// Both days were asked about: today first, then the launch day.
		if want, asked := []int64{budget.Day(f.now), budget.Day(launch)}, f.db.daysAsked(); len(asked) != 2 ||
			asked[0] != want[0] || asked[1] != want[1] {
			t.Errorf("days asked = %v, want %v", asked, want)
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

// C3.9's gate: until the phase-3 shared-job rules are on main, the service
// admits runs in per-repository jobs only.
// Global Constraint 6: per-repository jobs only until the phase-3 shared-job
// rules are on main (C3.9), so a run in a shared project job is refused whole.
//
// The gate is the caller's *verified* account and never X-Fugaro-Run-Job. One
// service account is dedicated to one Cloud Run job, so the account is the job;
// the header is text the caller writes (a launcher may override any environment
// variable, internal/runner's jobName), so a gate read off it would be one a
// caller could satisfy by writing a header. Both directions are pinned below:
// the shared account is refused however the header reads, and an admitted
// account is admitted however the header reads.
func TestVerifyRefusesSharedJobRun(t *testing.T) {
	shared := func(c map[string]any) { c["email"] = testSharedSA }

	// The shared job's account, presenting a *per-repository* job name in the
	// header: refused all the same. A forged header cannot buy admission.
	f := newFixture(t)
	_, err := f.verify(f.runToken(), f.jobToken(shared), f.job)
	if !errors.Is(err, ErrUnverifiedJob) {
		t.Fatalf("err = %v, want ErrUnverifiedJob: the caller is the one refused", err)
	}
	if !strings.Contains(err.Error(), "shared") {
		t.Errorf("err = %q, want it to say the job is shared", err)
	}
	// Neither the account nor the job name the caller presented is repeated.
	for _, fragment := range []string{testSharedSA, "fugaro-p-", f.job} {
		if strings.Contains(err.Error(), fragment) {
			t.Errorf("err = %q repeats what the caller presented", err)
		}
	}
	// And the run's own identity is not read at all: a caller this service
	// does not admit is refused before the run is checked.
	if paths := f.db.paths; len(paths) != 0 {
		t.Errorf("paths read = %v, want none: the caller is refused before the run is looked at", paths)
	}

	// The same account with no header, and with a header that is not one clean
	// value: the refusal is the shared job's, not the header's, which is the
	// order the two checks are in.
	for name, job := range map[string]string{
		"a shared caller names no job":              "",
		"a shared caller's job has a newline in it": "fugaro-aurora\nX-Evil: 1",
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			_, err := f.verify(f.runToken(), f.jobToken(shared), job)
			if !errors.Is(err, ErrUnverifiedJob) {
				t.Fatalf("err = %v, want ErrUnverifiedJob", err)
			}
			if !strings.Contains(err.Error(), "shared") {
				t.Errorf("err = %q, want the shared job's refusal: it is decided from the caller, not the header", err)
			}
		})
	}

	// The other direction: an installation per-repository job account is
	// admitted even when the header names a shared project job. The header is
	// the caller's claim — recorded, never the gate — and the account is what
	// says which job the run is in.
	f2 := newFixture(t)
	if _, err := f2.verify(f2.runToken(), f2.jobToken(), "fugaro-p-profile-one-anthropic-acct-one-0123456789ab"); err != nil {
		t.Fatalf("Verify refused a run whose caller is an admitted job account: %v", err)
	}

	// A run that names no job, or one that is not a single clean header value,
	// still cannot be placed: the name goes into the run's own record (C1.13),
	// so it is held to the header rule.
	unplaceable := map[string]string{
		"no job at all":             "",
		"a newline in the job name": "fugaro-aurora\nX-Evil: 1",
		"a job name past the bound": strings.Repeat("j", maxHeaderTokenBytes+1),
	}
	for name, job := range unplaceable {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
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
	if strings.Contains(err.Error(), tok) || strings.Contains(err.Error(), tok[:24]) {
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
	// covers the launch day and every day since, and the run is admitted. One
	// day more is the refusal TestVerifyRefusesARunIDItCannotPlace pins, and
	// the pair is what says the bound is where the constant is rather than
	// anywhere either side of it.
	t.Run("the oldest launch it scans", func(t *testing.T) {
		f := newFixture(t)
		today := budget.Day(f.now)
		f.run = f.now.AddDate(0, 0, -maxRunDays).Format("20060102-150405") + "-ee55"
		f.verifyOK()
		days := f.db.daysAsked()
		if len(days) != maxRunDays+1 {
			t.Fatalf("days scanned = %v, want %d: the launch day and every day since", days, maxRunDays+1)
		}
		if days[0] != today || days[len(days)-1] != today-maxRunDays {
			t.Errorf("days scanned = %v, want %d down to %d", days, today, today-maxRunDays)
		}
	})

	// A run id dated ahead of this service's clock is placed at today and not
	// in the future: a run id is whoever minted it's text, and this service's
	// clock being behind is not evidence that the run has not started. So a run
	// dated tomorrow whose outcome is written today is refused — with the clamp
	// gone the scan runs zero days and the run is admitted.
	t.Run("a launch day ahead of today", func(t *testing.T) {
		f := newFixture(t)
		today := budget.Day(f.now)
		f.run = f.now.AddDate(0, 0, 1).Format("20060102-150405") + "-ee55"
		f.db.set(budget.PathOutcome(today, f.slug, f.run))
		_, err := f.verify(f.runToken(), f.jobToken(), f.job)
		if !errors.Is(err, ErrUnverified) {
			t.Fatalf("err = %v, want a refusal: the run's outcome is written today", err)
		}
		if days := f.db.daysAsked(); len(days) != 1 || days[0] != today {
			t.Errorf("days scanned = %v, want only today (%d)", days, today)
		}
	})
}

// The construction refuses a service that cannot check what it must check.
func TestNewAuthenticatorRefusesAnIncompleteConfig(t *testing.T) {
	base := AuthConfig{
		Project:           testProject,
		FirebaseProject:   testFirebase,
		Audience:          testAudience,
		JobAccounts:       []string{testJobSA},
		Keys:              staticKeys{testKid: &testKey().PublicKey},
		JobKeys:           staticKeys{testJobKid: &testKey().PublicKey},
		Outcomes:          RTDBOutcomes{DB: &fakeDB{}},
		SharedJobAccounts: []string{testSharedSA},
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
			c.SharedJobAccounts = []string{testSharedSA, ""}
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
	// An empty shared set is not an incomplete configuration: under Global
	// Constraint 6 an installation with no shared project jobs has none, and
	// the set is what the published policy's profiles say.
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
	// A segment budget.PathOutcome would panic on is refused, not panicked on.
	if _, err := (RTDBOutcomes{DB: fake}).OutcomeExists(context.Background(), day, "", run); err == nil {
		t.Error("an empty slug was accepted as a path segment")
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
