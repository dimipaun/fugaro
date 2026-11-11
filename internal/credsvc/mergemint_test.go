package credsvc

// The merge mint's own tests (mergemint.go, E16, D2049). They are the GitHub
// read mint's tests with the class and the gate changed, and the properties they
// exist to hold are the ones the route's design turns on:
//
//   - the class: the route admits the installation's orchestrator daemon and
//     NOBODY else — not a run, not a job, not a person, and not any of them
//     carrying the audience a plain `gcloud auth print-identity-token` mints
//     (TestMergeMintRefusesRunCaller, TestMergeMintRefusesPersonReadCaller);
//   - the binding: the token is minted for one head and refused when the pull
//     request's head has moved (TestMergeMintBoundToHeadSHA, G2);
//   - the gate: no token for a base whose rules the App may bypass, and none
//     when that list cannot be read (TestMergeMintRefusesBypassActor, G14);
//   - the bound: one repository, one hour, mergeMintPerRepo mints
//     (TestMergeMintRateLimited);
//   - the reach: the token handed over is mergeTokenPermissions' and never the
//     gate's wider read set (TestMergeMintNeverReturnsABroaderToken);
//   - the record: exactly one line per act, and none for a caller that was never
//     verified (TestMergeMintAuditsEveryMint, C1.13).
//
// Every one of them is a mutation proof and is written as such: dropping the
// class, the head binding, the bypass read or the permission scoping turns one
// of them red, and the fake GitHub below records the requests the service
// actually made, so "the service reached GitHub zero times" is a claim a test
// reads rather than assumes.
//
// Everything here is hermetic (Convention 9): the GitHub API is a fake on
// loopback reached through a transport that refuses every other destination, and
// the App key and every token are built at run time (Convention 2).

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
)

// mergeMintPath is the merge mint's path, spelled as the contract spells it. It
// is a literal on purpose: the tests below hold the constant to it, so a rename
// of either cannot quietly move the route.
const mergeMintPath = "/v1/github/merge-token"

// testOrchestratorSA is the installation's orchestrator daemon's account: a
// service account address, which is what every entry of the class must be. It
// is a name and not a credential.
const testOrchestratorSA = "fugaro-orchestrator@aurora-dev.iam.gserviceaccount.com"

// mergeHead is a commit-shaped head: forty hex digits derived from a number
// rather than written down, so no fixture in this file looks like an object
// name it is not.
func mergeHead(n int) string { return fmt.Sprintf("%040x", n) }

// mergeMintBody is a merge mint's call body: the repository, the pull request,
// the head the call is bound to and the App ID. It is built from the service's
// own request type, so a rename of a field is a compile error here rather than a
// test that quietly sends a body the route would refuse.
func mergeMintBody(repo string, number int, head, appID string) string {
	b, err := json.Marshal(MergeMintRequest{Repository: repo, Number: number, HeadSHA: head, AppID: appID})
	if err != nil {
		panic("credsvc test: building a merge mint body: " + err.Error())
	}
	return string(b)
}

// mergeMintLiteralBody is the same body spelled NAME BY NAME, the way the read
// route's own helper spells its two (githubReadBody): the four field names are
// the contract the daemon's client writes (internal/creds/mergemint.go), and a
// body built from the struct would follow a renamed tag rather than hold it.
func mergeMintLiteralBody(repo string, number int, head, appID string) string {
	return `{"repository":"` + repo + `","number":` + fmt.Sprint(number) + `,"head_sha":"` + head + `","app_id":"` + appID + `"}`
}

// ---------------------------------------------------------------------------
// The fake GitHub, with the gate's two reads
// ---------------------------------------------------------------------------

// mergeFake stands in for api.github.com for the merge mint. It is the mint
// fixture's fake (ghFake) with the gate's two reads added, and it is a fake of
// its own rather than that one widened because ghFake fails the test on any
// request but the installation lookup and the mint — which is exactly the
// property the run's own mint needs and exactly the wrong one here.
//
// It answers the calls the route makes: the installation lookup and the token
// mint (twice: once for the gate's read token, once for the answer), the pull
// request, the rulesets list, one per-id ruleset read per listed ruleset, and
// the base's classic branch protection. What it answers with is the test's: the
// head the pull request is at, the base it targets, the rulesets that cover it,
// the Apps the classic protection lets bypass it, and the status any of the
// reads answers with when a test wants one to fail.
//
// The two ruleset reads are answered the way GitHub answers them, and that
// difference is the point of having both: the list answers a SUMMARY of each
// ruleset — an id and its metadata — and the per-id read is the only one that
// carries conditions, bypass_actors and current_user_can_bypass.
type mergeFake struct {
	t   *testing.T
	srv *httptest.Server

	mu   sync.Mutex
	seen []ghSeen

	// head and base are what the pull request read answers: the commit the pull
	// request is at, and the branch it targets.
	head, base string
	// rulesets is what the rulesets read answers, verbatim: one ruleset object
	// per entry, which the list answers as a summary of and the per-id read
	// answers whole.
	rulesets []map[string]any
	// rulesNext, when set, is the Link header the rulesets read answers with: the
	// header GitHub pages a list with, and the one the gate must refuse on rather
	// than read the page as the whole list.
	rulesNext string
	// protection is the classic branch protection read's bypass list: the Apps it
	// names. An empty one is answered as GitHub answers a branch with no classic
	// protection — 404 — and protectionStatus overrides that answer when a test
	// wants the read to fail another way.
	protection       []map[string]any
	protectionStatus int
	// pullStatus, rulesStatus and rulesetStatus, when not 2xx, are what those
	// reads answer with instead: the pull request, the rulesets list, and one
	// per-id ruleset read.
	pullStatus, rulesStatus, rulesetStatus int
	// mintStatus, when not 2xx, is what the token mint answers with.
	mintStatus int
	// token and expires are the token the mint answers with and its life.
	token   string
	expires time.Time
}

func newMergeFake(t *testing.T) *mergeFake {
	t.Helper()
	f := &mergeFake{
		t:       t,
		head:    mergeHead(1),
		base:    "main",
		token:   fakeCred(t, "merge-token"),
		expires: ghExpiry,
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *mergeFake) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		f.t.Errorf("reading the request body: %v", err)
	}
	f.mu.Lock()
	f.seen = append(f.seen, ghSeen{method: r.Method, path: r.URL.Path, body: string(body), auth: r.Header.Get("Authorization")})
	head, base, rulesets := f.head, f.base, f.rulesets
	pullStatus, rulesStatus, rulesetStatus, mintStatus := f.pullStatus, f.rulesStatus, f.rulesetStatus, f.mintStatus
	rulesNext := f.rulesNext
	protection, protectionStatus := f.protection, f.protectionStatus
	token, expires := f.token, f.expires
	f.mu.Unlock()

	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/installation"):
		f.write(w, http.StatusOK, map[string]any{"id": 777})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/access_tokens"):
		if mintStatus != 0 && mintStatus/100 != 2 {
			w.WriteHeader(mintStatus)
			return
		}
		f.write(w, http.StatusCreated, map[string]any{
			"token":      token,
			"expires_at": expires.UTC().Format(time.RFC3339),
		})
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pulls/"):
		if pullStatus != 0 && pullStatus/100 != 2 {
			w.WriteHeader(pullStatus)
			return
		}
		f.write(w, http.StatusOK, map[string]any{
			"head": map[string]any{"sha": head},
			"base": map[string]any{"ref": base},
		})
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/rulesets"):
		if rulesStatus != 0 && rulesStatus/100 != 2 {
			w.WriteHeader(rulesStatus)
			return
		}
		if rulesNext != "" {
			w.Header().Set("Link", rulesNext)
		}
		// GitHub's list answers a summary of each ruleset and not the ruleset:
		// no conditions, no bypass_actors, no current_user_can_bypass. A gate
		// that read its rules out of this answer reads every ruleset as naming
		// nobody.
		summaries := make([]map[string]any, 0, len(rulesets))
		for i, rs := range rulesets {
			summaries = append(summaries, mergeRulesetSummary(i+1, rs))
		}
		f.write(w, http.StatusOK, summaries)
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/rulesets/"):
		if rulesetStatus != 0 && rulesetStatus/100 != 2 {
			w.WriteHeader(rulesetStatus)
			return
		}
		id, ok := mergeRulesetID(r.URL.Path)
		if !ok || id < 1 || id > len(rulesets) {
			// GitHub's own answer for a ruleset that is not there.
			w.WriteHeader(http.StatusNotFound)
			return
		}
		f.write(w, http.StatusOK, mergeRulesetDetail(rulesets[id-1]))
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/protection"):
		if protectionStatus != 0 && protectionStatus/100 != 2 {
			w.WriteHeader(protectionStatus)
			return
		}
		if len(protection) == 0 {
			// GitHub's own answer for a branch with no classic branch protection.
			w.WriteHeader(http.StatusNotFound)
			return
		}
		f.write(w, http.StatusOK, map[string]any{
			"required_pull_request_reviews": map[string]any{
				"bypass_pull_request_allowances": map[string]any{"apps": protection},
			},
		})
	default:
		f.t.Errorf("the fake GitHub was asked for %s %s, and the merge mint reads the installation, the pull request, the rulesets and each of them by id, the branch protection and the two mints and nothing else", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *mergeFake) write(w http.ResponseWriter, status int, body any) {
	raw, err := json.Marshal(body)
	if err != nil {
		f.t.Errorf("encoding the fake's answer: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

// requests is what the fake was asked, in order.
func (f *mergeFake) requests() []ghSeen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ghSeen(nil), f.seen...)
}

// count is how many requests the fake saw.
func (f *mergeFake) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.seen)
}

// mintBodies is the body of every access_tokens call the fake answered, in
// order: the gate's read token first and the token the caller is handed last.
// It fails the test when the fake was asked for anything else, because the
// order of these calls is what "the gate's token never leaves this process"
// means.
func (f *mergeFake) mintBodies(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, seen := range f.requests() {
		if seen.method == http.MethodPost && strings.HasSuffix(seen.path, "/access_tokens") {
			var sent map[string]any
			if err := json.Unmarshal([]byte(seen.body), &sent); err != nil {
				t.Fatalf("a mint's body is not the object this contract writes: %v (%q)", err, seen.body)
			}
			out = append(out, sent)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// The fixture
// ---------------------------------------------------------------------------

// mergeMintSvc is the whole fixture for a merge mint test: a service whose
// bundle holds the App key, whose inventory holds the GitHub repository, whose
// orchestrator list is the test's, and the fake GitHub behind it.
func mergeMintSvc(t *testing.T) (*svc, *mergeFake) {
	t.Helper()
	return mergeMintSvcWith(t, nil)
}

// mergeMintSvcWith is mergeMintSvc with the service's own configuration under
// the test's control, for the cases that need one wired differently: an audit
// log that cannot write, an inventory holding no repository.
func mergeMintSvcWith(t *testing.T, mutate func(*svc, *Config)) (*svc, *mergeFake) {
	t.Helper()
	gh := newMergeFake(t)
	s := newSvcWith(t, func(s *svc, cfg *Config) {
		ac := s.auth.cfg
		ac.OrchestratorAccounts = []string{testOrchestratorSA}
		ac.PersonAccounts = []string{testPersonAccount}
		a, err := NewAuthenticator(ac)
		if err != nil {
			t.Fatalf("NewAuthenticator: %v", err)
		}
		cfg.Auth, s.auth = a, a
		cfg.Repos = map[string]string{githubSlug(t, testRepoPath): testRepo}
		cfg.Client = &http.Client{Transport: ghTransport{srv: gh.srv}}
		s.src.docs = [][]byte{mintBundleDoc(t)}
		if mutate != nil {
			mutate(s, cfg)
		}
	})
	return s, gh
}

// orchestratorToken is the Google ID token the daemon presents: the same token
// shape a person presents (a Google ID token Cloud Run IAM admitted, naming an
// email and no run), with the orchestrator account's address in it. What makes
// the two classes different is the list the address is looked up in and the
// audience, and neither is this helper's business.
func (s *svc) orchestratorToken(mutate ...func(map[string]any)) string {
	return s.personToken(testOrchestratorSA, mutate...)
}

// callMergeMint sends one merge mint for repo at head as the caller holding tok
// and returns the answer. It is the shape every test below starts from, whether
// the call is admitted or refused.
func (s *svc) callMergeMint(t *testing.T, repo string, number int, head, appID, tok string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	return s.callPerson(t, mergeMintPath, mergeMintBody(repo, number, head, appID), tok, mutate...)
}

// mintMerge is callMergeMint as the daemon: the fixture's own orchestrator
// account, the fixture's own repository, and a pull request number that is the
// test's.
func (s *svc) mintMerge(t *testing.T, number int, head string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	return s.callMergeMint(t, testRepoPath, number, head, testAppID, s.orchestratorToken(), mutate...)
}

// ---------------------------------------------------------------------------
// The class
// ---------------------------------------------------------------------------

// TestMergeMintRefusesRunCaller is the red step of E16 and the class's first
// half: a RUN's credential opens nothing here.
//
// The three tokens below are the credentials a run holds or can be minted for
// it — the run's own Firebase identity token, the job account's Google ID token
// (which is what a run's container presents to this service on every other
// route), and that same token carrying the audience a plain `gcloud auth
// print-identity-token` mints — and every one of them is refused by the class
// with the wire's own `unverified_job_identity`, reaches no GitHub call, and
// writes no audit line, because a caller this service never verified has no
// name for a line to carry.
//
// It is the step that fails while the route accepts a run's token: the mutant
// this test is written for is the one that admits the job accounts to the merge
// mint (which is what "the daemon is just another job" would look like), and it
// is red for it on the first token, before any of the gate's facts are reached.
func TestMergeMintRefusesRunCaller(t *testing.T) {
	s, gh := mergeMintSvc(t)
	for _, tc := range []struct {
		name string
		tok  string
	}{
		{"the run's own identity token", s.runToken()},
		{"the job account's own ID token", s.jobToken()},
		{"the job account's token for gcloud's audience", s.jobToken(func(c map[string]any) { c["aud"] = GcloudAudience })},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := s.callMergeMint(t, testRepoPath, 7, mergeHead(1), testAppID, tc.tok)
			if resp.StatusCode != StatusUnverified {
				t.Fatalf("the merge mint admitted %s: HTTP %d %s", tc.name, resp.StatusCode, body)
			}
			if e := refusal(t, resp.StatusCode, body); e.Error != ErrorUnverifiedJobIdentity {
				t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorUnverifiedJobIdentity, e.Message)
			}
		})
	}
	if n := gh.count(); n != 0 {
		t.Errorf("the service reached GitHub %d times for callers it refused, want none", n)
	}
	if got := s.audit.String(); got != "" {
		t.Errorf("a refusal taken before the caller was verified wrote an audit line: %s", got)
	}
}

// TestMergeMintRefusesPersonReadCaller is the class's second half: a PERSON's
// credential opens nothing here either — not the installation's own people, and
// not the address a person's ambient `gcloud auth print-identity-token` can be
// minted for.
//
// The two halves of the class are aimed at here and they are the reason this
// route has a class of its own rather than a widened person class: the first
// token is a person of this installation (admitted on the read mint, refused
// here), and the last is the ORCHESTRATOR'S OWN ADDRESS carrying gcloud's
// audience — the token a coding-agent session on the daemon's own machine can
// produce for the account the daemon runs as, which the audience check refuses
// before any list is consulted. A mutant that dropped that check, or that
// admitted PersonAccounts, turns one of these cells green-where-it-must-be-red.
func TestMergeMintRefusesPersonReadCaller(t *testing.T) {
	s, gh := mergeMintSvc(t)
	for _, tc := range []struct {
		name string
		tok  string
	}{
		{"a person of this installation", s.personToken(testPersonAccount)},
		{"a person's ambient gcloud token", s.personToken(testPersonAccount, func(c map[string]any) { c["aud"] = GcloudAudience })},
		{"the orchestrator's own address for gcloud's audience", s.orchestratorToken(func(c map[string]any) { c["aud"] = GcloudAudience })},
		{"the coordinator's account", s.personToken(testCoordinatorAccount)},
		{"a stranger", s.personToken(testOtherPerson)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := s.callMergeMint(t, testRepoPath, 7, mergeHead(1), testAppID, tc.tok)
			if resp.StatusCode != StatusUnverified {
				t.Fatalf("the merge mint admitted %s: HTTP %d %s", tc.name, resp.StatusCode, body)
			}
			if e := refusal(t, resp.StatusCode, body); e.Error != ErrorUnverifiedJobIdentity {
				t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorUnverifiedJobIdentity, e.Message)
			}
		})
	}
	if n := gh.count(); n != 0 {
		t.Errorf("the service reached GitHub %d times for callers it refused, want none", n)
	}
}

// TestMergeMintRefusesARunAccountAsItsDaemon is the third half of the class: an
// installation that names one of its own run accounts as the daemon does not
// widen the class, it is refused where the configuration is read.
//
// It is the same rule the person list carries a few lines above, and it is here
// rather than at the route because of where it fails: a job, a shared job's or a
// review job's address on this list would admit that run's own Google ID token
// to the merge mint — the credential C2.2 and F28 exist to keep out of it — and
// the route would have no way to tell it from the daemon's, since it is the same
// token shape, the same audience and the same fold.
//
// The last cell is what the rule must NOT refuse: a verifier and the coordinator
// are classes of other routes, and one address admitted by two routes is not the
// overlap the class rule is about (G4).
func TestMergeMintRefusesARunAccountAsItsDaemon(t *testing.T) {
	f := newFixture(t)
	config := func(daemon ...string) AuthConfig {
		return AuthConfig{
			Project:              testProject,
			FirebaseProject:      testFirebase,
			Audience:             testAudience,
			JobAccounts:          []string{testJobSA},
			SharedJobAccounts:    []string{testSharedSA},
			ReviewJobAccounts:    []ReviewJobAccount{{Email: testReviewSA, Account: testReviewAccount}},
			VerifierAccounts:     []string{testVerifierSA},
			PersonAccounts:       []string{testPersonAccount},
			LauncherAccounts:     []string{testLauncherAccount},
			OrchestratorAccounts: daemon,
			Keys:                 f.keys,
			JobKeys:              f.jobKeys,
			Outcomes:             RTDBOutcomes{DB: f.db},
		}
	}
	for _, tc := range []struct{ name, account string }{
		{"a job of this installation", testJobSA},
		{"a shared project job's account", testSharedSA},
		{"a review job's account", testReviewSA},
		{"a person of this installation", testPersonAccount},
		{"a launcher", testLauncherAccount},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewAuthenticator(config(tc.account))
			if err == nil {
				t.Fatalf("NewAuthenticator admitted %s as the orchestrator daemon", tc.name)
			}
			if !strings.Contains(err.Error(), "orchestrator account") {
				t.Errorf("the configuration error is %q, want it to be about the orchestrator list", err)
			}
			if strings.Contains(err.Error(), tc.account) {
				t.Errorf("the configuration error is %q, and it carries the address it refused", err)
			}
		})
	}

	if _, err := NewAuthenticator(config(testVerifierSA, testCoordinatorAccount)); err != nil {
		t.Fatalf("NewAuthenticator refused a daemon that is also a verifier or the coordinator: %v", err)
	}
}

func TestMergeMintTakesNoRunIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*http.Request)
	}{
		{"presents a run identity", func(r *http.Request) { r.Header.Set(HeaderRunIdentity, "a-run-identity-token") }},
		{"names a job", func(r *http.Request) { r.Header.Set(HeaderRunJob, "fugaro-a-job") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, gh := mergeMintSvc(t)
			resp, body := s.mintMerge(t, 7, mergeHead(1), tc.mutate)
			if resp.StatusCode != StatusUnverified {
				t.Fatalf("the merge mint answered a call carrying a run identity: HTTP %d %s", resp.StatusCode, body)
			}
			if e := refusal(t, resp.StatusCode, body); e.Error != ErrorUnverifiedRunIdentity {
				t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorUnverifiedRunIdentity, e.Message)
			}
			if n := gh.count(); n != 0 {
				t.Errorf("the service reached GitHub %d times for a call it refused, want none", n)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The binding
// ---------------------------------------------------------------------------

// TestMergeMintBoundToHeadSHA: the token is minted for ONE commit.
//
// The three calls below are the same call at three heads: the head the pull
// request is at (admitted, and the answer names the caller's repository and the
// token the service minted), a head the pull request has moved off (refused,
// G2), and a pull request that cannot be read at all (refused the same way,
// because an unreadable head is not the head the call named).
//
// The refusals are `request_refused` and not `state_unavailable`: what is wrong
// is the call, and the daemon that made it is the one that must re-read the
// pull request rather than retry the mint. What makes this a mutation proof is
// the middle cell: a mutant that dropped the comparison mints a token for a head
// nobody reviewed, and the test sees a 200.
func TestMergeMintBoundToHeadSHA(t *testing.T) {
	t.Run("the head the call named", func(t *testing.T) {
		s, gh := mergeMintSvc(t)
		resp, body := s.mintMerge(t, 7, mergeHead(1))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the merge mint refused a call bound to the pull request's own head: HTTP %d %s", resp.StatusCode, body)
		}
		var ans readAnswer
		if err := json.Unmarshal(body, &ans); err != nil {
			t.Fatalf("the answer is not one this contract can read: %v (%d bytes)", err, len(body))
		}
		if ans.Token != gh.token || ans.ExpiresAt == "" || ans.Repository != testRepoPath {
			t.Errorf("the answer does not name the token, its life and the repository: %+v", ans)
		}
	})

	t.Run("a head the pull request has moved off", func(t *testing.T) {
		s, gh := mergeMintSvc(t)
		resp, body := s.mintMerge(t, 7, mergeHead(2))
		if resp.StatusCode != StatusRequestRefused {
			t.Fatalf("the merge mint served a call for a head the pull request is not at: HTTP %d %s", resp.StatusCode, body)
		}
		e := refusal(t, resp.StatusCode, body)
		if e.Error != ErrorRequestRefused {
			t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorRequestRefused, e.Message)
		}
		if !strings.Contains(e.Message, ErrMergeMintHeadMoved.Error()) {
			t.Errorf("the refusal is %q, want it to name the binding it refused (%s)", e.Message, ErrMergeMintHeadMoved.Error())
		}
		// The gate's read token was minted and the pull request was read; no
		// token was minted for the caller, which is the half of this that matters.
		if bodies := gh.mintBodies(t); len(bodies) != 1 {
			t.Errorf("the service minted %d installation tokens for a call it refused, want the gate's read token alone", len(bodies))
		}
		if strings.Contains(string(body), gh.token) {
			t.Errorf("the refusal carries the token GitHub minted: %s", body)
		}
	})

	t.Run("a pull request that cannot be read", func(t *testing.T) {
		s, gh := mergeMintSvc(t)
		gh.pullStatus = http.StatusNotFound
		resp, body := s.mintMerge(t, 7, mergeHead(1))
		if resp.StatusCode != StatusRequestRefused {
			t.Fatalf("the merge mint served a call whose pull request could not be read: HTTP %d %s", resp.StatusCode, body)
		}
		if e := refusal(t, resp.StatusCode, body); !strings.Contains(e.Message, ErrMergeMintHeadMoved.Error()) {
			t.Errorf("the refusal is %q, want the binding's class (%s)", e.Message, ErrMergeMintHeadMoved.Error())
		}
		if strings.Contains(string(body), gh.token) {
			t.Errorf("the refusal carries the token GitHub minted: %s", body)
		}
	})
}

// ---------------------------------------------------------------------------
// The gate
// ---------------------------------------------------------------------------

// mergeRuleset is one repository ruleset as the gate reads it: in force, on
// branches, covering include, with the given bypass actors.
func mergeRuleset(include []string, actors ...map[string]any) map[string]any {
	list := make([]any, 0, len(actors))
	for _, a := range actors {
		list = append(list, a)
	}
	return map[string]any{
		"target":        "branch",
		"enforcement":   "active",
		"conditions":    map[string]any{"ref_name": map[string]any{"include": include}},
		"bypass_actors": list,
	}
}

// mergeRulesetExcluding is mergeRuleset with an exclude list too: the other half
// of GitHub's condition, which takes a ruleset out of a ref's reach.
func mergeRulesetExcluding(include, exclude []string, actors ...map[string]any) map[string]any {
	rs := mergeRuleset(include, actors...)
	rs["conditions"] = map[string]any{"ref_name": map[string]any{"include": include, "exclude": exclude}}
	return rs
}

// mergeAppActor is the App as a bypass actor: GitHub spells an App on a bypass
// list as an Integration actor whose id is the App ID.
func mergeAppActor(appID string) map[string]any {
	return map[string]any{"actor_id": json.Number(appID), "actor_type": "Integration"}
}

// mergeRulesetSummary is one ruleset as the rulesets LIST answers it: an id and
// its metadata, and none of the three fields the gate's rule is read out of.
// It is what GitHub answers, and the fake answering it is what makes a gate that
// read the bypass list out of the list go red.
func mergeRulesetSummary(n int, rs map[string]any) map[string]any {
	return map[string]any{
		"id":          json.Number(strconv.Itoa(n)),
		"name":        "ruleset-" + strconv.Itoa(n),
		"source":      "acme/aurora",
		"source_type": "Repository",
		"enforcement": rs["enforcement"],
	}
}

// mergeRulesetDetail is one ruleset as the per-id read answers it: the whole
// object, which is where conditions, bypass_actors and current_user_can_bypass
// come from. current_user_can_bypass is filled in when the test did not set it,
// because GitHub always answers it for the identity that asked — the App — and
// an App that is on no bypass list is one that may bypass nothing.
func mergeRulesetDetail(rs map[string]any) map[string]any {
	out := make(map[string]any, len(rs)+1)
	for k, v := range rs {
		out[k] = v
	}
	if _, ok := out["current_user_can_bypass"]; !ok {
		out["current_user_can_bypass"] = "never"
	}
	return out
}

// mergeRulesetID is the id in a per-id ruleset read's path, and whether the
// path carries one at all.
func mergeRulesetID(path string) (int, bool) {
	_, tail, ok := strings.Cut(path, "/rulesets/")
	if !ok {
		return 0, false
	}
	id, err := strconv.Atoi(tail)
	if err != nil {
		return 0, false
	}
	return id, true
}

// TestMergeMintRefusesBypassActor is G14: the token never merges past branch
// protection, in either of the two places GitHub keeps a bypass list.
//
// The first cells are the ways the gate refuses — a base whose rules the App may
// bypass, a bypass list that could not be read at all (because an unreadable
// list is not an absent one), the same rule read out of the classic branch
// protection's own list, a protection object that could not be read, and a
// ruleset whose include is a pattern rather than a ref — and every one of them
// answers `state_unavailable`: what is wrong is this installation's
// configuration or GitHub, and the daemon must not merge.
//
// Most of them are about the rulesets read being TWO reads and not one. GitHub's
// list answers a summary of each ruleset — an id and its metadata — and the
// conditions, the bypass list and GitHub's own `current_user_can_bypass` come
// back from the per-id read alone, so a gate that read its rule out of the list
// read every ruleset as naming nobody and served a merge the App may bypass. The
// cells that hold that are the App named on a bypass list (which the summary
// does not carry), a ruleset that hides its list from this token, one GitHub
// says the App may bypass, one whose answer says nothing either way, and one
// whose per-id read fails outright.
//
// The wildcard cell is the one that was wrong: GitHub's ref patterns are fnmatch
// and not literals, so a ruleset whose include is `refs/heads/ma*` covers the
// base, and a gate that read it as "covers nothing" would skip the ruleset and
// never see the App on its bypass list.
//
// The cells after them are the other half, and they are what keeps the refusal
// from being "refuse whenever the repository has rulesets": a ruleset naming
// ANOTHER actor, a disabled one, a ruleset covering another branch, one that
// excludes the base, a classic protection naming another App, and a repository
// with neither are all admitted.
func TestMergeMintRefusesBypassActor(t *testing.T) {
	refusals := []struct {
		name          string
		base          string
		rules         []map[string]any
		apps          []map[string]any
		status        int
		rulesetStatus int
	}{
		{
			name:  "a ruleset naming the App as a bypass actor",
			rules: []map[string]any{mergeRuleset([]string{"refs/heads/main"}, mergeAppActor(testAppID))},
		},
		{
			name:   "a bypass list that cannot be read",
			status: http.StatusInternalServerError,
		},
		{
			name:  "a ruleset whose include is a wildcard over the base",
			rules: []map[string]any{mergeRuleset([]string{"refs/heads/ma*"}, mergeAppActor(testAppID))},
		},
		{
			name:  "a ruleset whose include is a single-character wildcard",
			rules: []map[string]any{mergeRuleset([]string{"refs/heads/mai?"}, mergeAppActor(testAppID))},
		},
		{
			name:  "a ruleset whose include crosses a segment",
			rules: []map[string]any{mergeRuleset([]string{"refs/**"}, mergeAppActor(testAppID))},
		},
		{
			name:  "a ruleset whose include is a wildcard over a base with a slash in it",
			base:  "release/1.0",
			rules: []map[string]any{mergeRuleset([]string{"refs/heads/*"}, mergeAppActor(testAppID))},
		},
		{
			name:  "a ruleset whose exclude is a pattern this code cannot decide",
			rules: []map[string]any{mergeRulesetExcluding([]string{"refs/heads/*"}, []string{"refs/heads/[abc]"}, mergeAppActor(testAppID))},
		},
		{
			// The ruleset is read by id and the list's summary is not the
			// ruleset: this answer carries no bypass list at all, which is what
			// a ruleset the token may not read into answers with, and it is not
			// the empty list it looks like.
			name: "a ruleset that hides its bypass list",
			rules: []map[string]any{func() map[string]any {
				rs := mergeRuleset([]string{"refs/heads/main"})
				delete(rs, "bypass_actors")
				return rs
			}()},
		},
		{
			// GitHub's own reading of the actor, which is answered for the
			// identity that asked — the App — and is not "never" here.
			name: "a ruleset GitHub says the App may bypass",
			rules: []map[string]any{func() map[string]any {
				rs := mergeRuleset([]string{"refs/heads/main"})
				rs["current_user_can_bypass"] = "always"
				return rs
			}()},
		},
		{
			// An answer that says nothing about whether the App may bypass the
			// ruleset is not an answer of "never".
			name: "a ruleset whose answer does not say whether the App may bypass it",
			rules: []map[string]any{func() map[string]any {
				rs := mergeRuleset([]string{"refs/heads/main"})
				rs["current_user_can_bypass"] = ""
				return rs
			}()},
		},
		{
			// A listed ruleset the per-id read cannot reach: the list said it is
			// there, so what it says about the App is unread and not absent.
			name:          "a listed ruleset whose own answer cannot be read",
			rules:         []map[string]any{mergeRuleset([]string{"refs/heads/main"}, mergeAppActor("9999"))},
			rulesetStatus: http.StatusInternalServerError,
		},
		{
			name: "a classic branch protection whose bypass list names the App",
			apps: []map[string]any{{"id": json.Number(testAppID)}},
		},
		{
			name:   "a classic branch protection that cannot be read",
			status: http.StatusForbidden,
			apps:   []map[string]any{{"id": json.Number("9999")}},
		},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			s, gh := mergeMintSvc(t)
			gh.rulesets, gh.protection = tc.rules, tc.apps
			if tc.base != "" {
				gh.base = tc.base
			}
			// The status is the rulesets read's when no classic bypass list is set
			// and the protection read's when one is: the two cells that turn a read
			// into a failure are about the read that carries the list the App is on.
			if tc.apps == nil {
				gh.rulesStatus = tc.status
			} else {
				gh.protectionStatus = tc.status
			}
			gh.rulesetStatus = tc.rulesetStatus
			resp, body := s.mintMerge(t, 7, mergeHead(1))
			if resp.StatusCode != StatusUnavailable {
				t.Fatalf("the merge mint served a base the App may bypass: HTTP %d %s", resp.StatusCode, body)
			}
			e := refusal(t, resp.StatusCode, body)
			if e.Error != ErrorStateUnavailable {
				t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorStateUnavailable, e.Message)
			}
			if !strings.Contains(e.Message, ErrMergeMintBypassActor.Error()) {
				t.Errorf("the refusal is %q, want it to name the bypass class (%s)", e.Message, ErrMergeMintBypassActor.Error())
			}
			if bodies := gh.mintBodies(t); len(bodies) != 1 {
				t.Errorf("the service minted %d installation tokens for a call it refused, want the gate's read token alone", len(bodies))
			}
			if strings.Contains(string(body), gh.token) {
				t.Errorf("the refusal carries the token GitHub minted: %s", body)
			}
		})
	}

	admitted := []struct {
		name  string
		base  string
		rules []map[string]any
		apps  []map[string]any
	}{
		{"a ruleset naming another App", "", []map[string]any{mergeRuleset([]string{"refs/heads/main"}, mergeAppActor("9999"))}, nil},
		{"a disabled ruleset naming the App", "", []map[string]any{func() map[string]any {
			rs := mergeRuleset([]string{"refs/heads/main"}, mergeAppActor(testAppID))
			rs["enforcement"] = "disabled"
			return rs
		}()}, nil},
		{"a ruleset covering another branch", "", []map[string]any{mergeRuleset([]string{"refs/heads/release"}, mergeAppActor(testAppID))}, nil},
		{"a ruleset excluding the base", "", []map[string]any{mergeRulesetExcluding([]string{"refs/heads/*"}, []string{"refs/heads/main"}, mergeAppActor(testAppID))}, nil},
		{"a ruleset whose include is another branch of a base with a slash in it", "release/1.0", []map[string]any{mergeRuleset([]string{"refs/heads/main"}, mergeAppActor(testAppID))}, nil},
		{"a repository with no rulesets", "", nil, nil},
		{"a classic branch protection naming another App", "", nil, []map[string]any{{"id": json.Number("9999")}}},
		{"a classic branch protection naming no App", "", nil, []map[string]any{{"id": json.Number("9999"), "slug": "other-app"}}},
	}
	for _, tc := range admitted {
		t.Run(tc.name, func(t *testing.T) {
			s, gh := mergeMintSvc(t)
			gh.rulesets, gh.protection = tc.rules, tc.apps
			if tc.base != "" {
				gh.base = tc.base
			}
			resp, body := s.mintMerge(t, 7, mergeHead(1))
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("the merge mint refused a base no bypass list binds the App on: HTTP %d %s", resp.StatusCode, body)
			}
		})
	}
}

// TestMergeMintReadsGitHubsLinkHeader pins the reading the paged-list refusal
// rests on, including the direction it fails in: a header this code cannot read
// is answered as a next page, so a list whose paging it did not understand is
// refused rather than read as complete.
func TestMergeMintReadsGitHubsLinkHeader(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header string
		want   bool
	}{
		{"no header at all", "", false},
		{"a next page", `<https://api.github.com/repositories/1/rulesets?page=2>; rel="next"`, true},
		{"a next page beside the last one", `<https://api.github.com/repositories/1/rulesets?page=2>; rel="next", <https://api.github.com/repositories/1/rulesets?page=4>; rel="last"`, true},
		{"the last page of a list, with no next", `<https://api.github.com/repositories/1/rulesets?page=4>; rel="last", <https://api.github.com/repositories/1/rulesets?page=3>; rel="prev"`, false},
		{"a header this code cannot read", `not a link header at all`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := linkHasNext(tc.header); got != tc.want {
				t.Errorf("linkHasNext(%q) = %v, want %v", tc.header, got, tc.want)
			}
		})
	}
}

// TestMergeMintRefusesAFullRulesetPage: GitHub pages the rulesets list, and the
// gate reads one page of GitHub's own maximum. A list that is longer than that
// page — GitHub says so with a Link header naming the next one — is refused
// rather than read as the whole list, because the ruleset that names the App may
// be on the page this gate did not read.
func TestMergeMintRefusesAFullRulesetPage(t *testing.T) {
	t.Run("a list GitHub says has a next page", func(t *testing.T) {
		s, gh := mergeMintSvc(t)
		gh.rulesets = []map[string]any{mergeRuleset([]string{"refs/heads/release"}, mergeAppActor("9999"))}
		gh.rulesNext = `<https://api.github.com/repositories/1/rulesets?page=2>; rel="next", <https://api.github.com/repositories/1/rulesets?page=4>; rel="last"`
		resp, body := s.mintMerge(t, 7, mergeHead(1))
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("the merge mint read one page of a paged list as the whole list: HTTP %d %s", resp.StatusCode, body)
		}
		if e := refusal(t, resp.StatusCode, body); !strings.Contains(e.Message, ErrMergeMintBypassActor.Error()) {
			t.Errorf("the refusal is %q, want the bypass class (%s)", e.Message, ErrMergeMintBypassActor.Error())
		}
	})

	t.Run("a page that comes back full", func(t *testing.T) {
		s, gh := mergeMintSvc(t)
		rulesets := make([]map[string]any, 0, mergeRulesetPage)
		for i := 0; i < mergeRulesetPage; i++ {
			rulesets = append(rulesets, mergeRuleset([]string{"refs/heads/release"}, mergeAppActor("9999")))
		}
		gh.rulesets = rulesets
		resp, body := s.mintMerge(t, 7, mergeHead(1))
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("the merge mint read a full page as the whole list: HTTP %d %s", resp.StatusCode, body)
		}
		if e := refusal(t, resp.StatusCode, body); !strings.Contains(e.Message, ErrMergeMintBypassActor.Error()) {
			t.Errorf("the refusal is %q, want the bypass class (%s)", e.Message, ErrMergeMintBypassActor.Error())
		}
	})
}

// ---------------------------------------------------------------------------
// The bound
// ---------------------------------------------------------------------------

// TestMergeMintRateLimited: one repository, one hour, mergeMintPerRepo mints.
//
// The bound is what stops a daemon looping on this route, so the test reads both
// of its halves: the mergeMintPerRepo'th call in a window is served and the one
// after it is refused — with the refused call reaching GitHub not at all, which
// is the ordering the handler's own comment states (the rate is checked before
// the bundle) — and a call made after the window has passed is served again,
// which is what says the bound is a window and not a lifetime cap.
//
// The refused call's audit line is written and its status is the refusal's: a
// rate-limited mint is an act of this service like any other.
func TestMergeMintRateLimited(t *testing.T) {
	s, gh := mergeMintSvc(t)
	for i := 0; i < mergeMintPerRepo; i++ {
		resp, body := s.mintMerge(t, i+1, mergeHead(1))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the merge mint refused mint %d of %d in the window: HTTP %d %s", i+1, mergeMintPerRepo, resp.StatusCode, body)
		}
	}
	before := gh.count()
	resp, body := s.mintMerge(t, 99, mergeHead(1))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the merge mint served a %dth call for one repository in an hour: HTTP %d %s", mergeMintPerRepo+1, resp.StatusCode, body)
	}
	e := refusal(t, resp.StatusCode, body)
	if e.Error != ErrorStateUnavailable {
		t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorStateUnavailable, e.Message)
	}
	if !strings.Contains(e.Message, ErrMergeMintRateLimited.Error()) {
		t.Errorf("the refusal is %q, want it to name the rate it refused on (%s)", e.Message, ErrMergeMintRateLimited.Error())
	}
	if after := gh.count(); after != before {
		t.Errorf("the rate-limited call reached GitHub %d times, want none: the rate is checked before the credential is read", after-before)
	}

	// The window passes: the same repository is served again. The caller's own ID
	// token is minted before the clock moves, because it has a life too, and so is
	// the token the fake GitHub hands out — an installation token GitHub mints at
	// the new instant is one this service reads as live.
	tok := s.orchestratorToken(func(c map[string]any) { c["exp"] = s.now.Add(4 * time.Hour).Unix() })
	s.fixture.now = s.now.Add(mergeMintWindow + time.Minute)
	gh.expires = s.now.Add(time.Hour)
	resp, body = s.callMergeMint(t, testRepoPath, 100, mergeHead(1), testAppID, tok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the merge mint refused a call made after the window passed: HTTP %d %s", resp.StatusCode, body)
	}
}

// ---------------------------------------------------------------------------
// The reach
// ---------------------------------------------------------------------------

// TestMergeMintNeverReturnsABroaderToken is the route's permission proof: what
// the caller is handed is mergeTokenPermissions' set and nothing wider, asserted
// field by field against the bodies the service actually sent GitHub.
//
// It reads both mints, because the route makes two and only one of them is the
// caller's: the FIRST is the gate's read token — which must carry no write of
// any kind, and which never leaves this process — and the LAST is the token the
// caller is handed. A mutant that handed over the gate's token, that widened the
// answer's set to include administration, or that let a caller name its own
// permissions fails here, because the comparison is against the constant's own
// contents and not against a subset.
func TestMergeMintNeverReturnsABroaderToken(t *testing.T) {
	s, gh := mergeMintSvc(t)
	resp, body := s.mintMerge(t, 7, mergeHead(1))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the merge mint refused an admitted daemon: HTTP %d %s", resp.StatusCode, body)
	}
	bodies := gh.mintBodies(t)
	if len(bodies) != 2 {
		t.Fatalf("the service minted %d installation tokens, want the gate's read token and the caller's", len(bodies))
	}
	gate, answer := bodies[0], bodies[1]

	perms := func(t *testing.T, sent map[string]any) map[string]string {
		t.Helper()
		raw, ok := sent["permissions"].(map[string]any)
		if !ok {
			t.Fatalf("a mint's body carries no permissions object: %v", sent)
		}
		out := make(map[string]string, len(raw))
		for noun, level := range raw {
			out[noun] = fmt.Sprint(level)
		}
		return out
	}

	gatePerms := perms(t, gate)
	for noun, level := range gatePerms {
		if strings.Contains(level, "write") {
			t.Errorf("the gate's read token was minted with a write: %s=%q", noun, level)
		}
	}
	if gatePerms["administration"] != "read" {
		t.Errorf("the gate's read token asked for administration=%q, want read: a repository's rulesets are read under that noun and no narrower one reaches them", gatePerms["administration"])
	}

	answerPerms := perms(t, answer)
	want := mergeTokenPermissions()
	if len(answerPerms) != len(want) {
		t.Fatalf("the mint asked for %d permissions %v, want the merge set's %d %v: this route mints one set and adds no reach of its own",
			len(answerPerms), answerPerms, len(want), want)
	}
	for noun, level := range want {
		if got := answerPerms[noun]; got != level {
			t.Errorf("the mint asked for %s=%q, want %q", noun, got, level)
		}
	}
	if _, ok := answerPerms["administration"]; ok {
		t.Errorf("the token handed to the caller carries administration: %v", answerPerms)
	}

	// Both mints are scoped to the one repository the call named, and no field
	// of the call can move that: the body's own contract has no repository list
	// (readMergeMintRequest).
	owner, name, _ := strings.Cut(testRepoPath, "/")
	for i, sent := range []map[string]any{gate, answer} {
		repos, _ := sent["repositories"].([]any)
		if len(repos) != 1 || fmt.Sprint(repos[0]) != name {
			t.Errorf("mint %d names repositories %v, want exactly [%s]", i, sent["repositories"], name)
		}
	}
	// The gate's three reads are the pull request, the rulesets of that same
	// repository and the base's classic branch protection, and the token that
	// read them is the one that never leaves.
	var paths []string
	for _, seen := range gh.requests() {
		if seen.method == http.MethodGet && !strings.HasSuffix(seen.path, "/installation") {
			paths = append(paths, seen.path)
		}
	}
	wantPaths := []string{
		"/repos/" + owner + "/" + name + "/pulls/7",
		"/repos/" + owner + "/" + name + "/rulesets",
		"/repos/" + owner + "/" + name + "/branches/main/protection",
	}
	if strings.Join(paths, ",") != strings.Join(wantPaths, ",") {
		t.Errorf("the gate read %v, want %v", paths, wantPaths)
	}
}

// ---------------------------------------------------------------------------
// The record
// ---------------------------------------------------------------------------

// TestMergeMintAuditsEveryMint: exactly one line per act, and a line for every
// act — the mint's, the refusal's, and the one that could not be written at all.
//
// The four calls below are one of each: a mint (one line, naming the daemon, the
// repository's slug, the App key and the merge purpose, with no run and no job
// because the caller is neither), a call the class refused (no line at all: the
// caller was never verified and has no name for one), a call the gate refused
// (one line, with the refusal's own status), and a mint whose line could not be
// written — which hands the token to nobody (D143), because a GitHub
// installation token cannot be unminted and an issuance nobody recorded is not
// one this service makes.
func TestMergeMintAuditsEveryMint(t *testing.T) {
	s, gh := mergeMintSvc(t)
	if resp, body := s.mintMerge(t, 7, mergeHead(1)); resp.StatusCode != http.StatusOK {
		t.Fatalf("the merge mint refused an admitted daemon: HTTP %d %s", resp.StatusCode, body)
	}
	lines := auditLines(t, s.audit)
	if len(lines) != 1 {
		t.Fatalf("the merge mint wrote %d audit lines, want exactly 1: %v", len(lines), lines)
	}
	line := lines[0]
	checkAuditShape(t, line)
	for field, want := range map[string]string{
		"caller":     testOrchestratorSA,
		"repository": githubSlug(t, testRepoPath),
		"credential": config.GitHubAppKeySecret,
		"purpose":    PurposeMergeMint,
		"run":        "",
		"job":        "",
		"tier":       "",
		"status":     StatusOK,
	} {
		if got := line[field]; got != want {
			t.Errorf("the merge mint's line: %s = %v, want %q", field, got, want)
		}
	}
	for _, where := range []struct{ what, text string }{
		{"the audit line", s.audit.String()},
		{"the service's log", s.logs.String()},
	} {
		if strings.Contains(where.text, gh.token) {
			t.Errorf("%s carries the token GitHub minted", where.what)
		}
		if strings.Contains(where.text, pemCore(t)) {
			t.Errorf("%s carries the App key", where.what)
		}
	}

	// A caller the class refused: no line, because it was never verified.
	if resp, _ := s.callMergeMint(t, testRepoPath, 7, mergeHead(1), testAppID, s.jobToken()); resp.StatusCode != StatusUnverified {
		t.Fatalf("the merge mint admitted a job's token: HTTP %d", resp.StatusCode)
	}
	if n := len(auditLines(t, s.audit)); n != 1 {
		t.Errorf("a refusal taken before the caller was verified wrote a line: %d lines, want the mint's alone", n)
	}

	// A caller the gate refused: one line, with the refusal's status.
	if resp, _ := s.mintMerge(t, 7, mergeHead(2)); resp.StatusCode != StatusRequestRefused {
		t.Fatalf("the merge mint served a call for a head the pull request is not at: HTTP %d", resp.StatusCode)
	}
	lines = auditLines(t, s.audit)
	if len(lines) != 2 {
		t.Fatalf("the gate's refusal wrote %d lines in total, want the mint's and the refusal's", len(lines))
	}
	if got := lines[1]["status"]; got != ErrorRequestRefused {
		t.Errorf("the refused merge mint's line status = %v, want %q", got, ErrorRequestRefused)
	}
	if got := lines[1]["purpose"]; got != PurposeMergeMint {
		t.Errorf("the refused merge mint's line purpose = %v, want %q", got, PurposeMergeMint)
	}
}

// TestMergeMintUnauditedIssuesNothing: a token GitHub granted whose line cannot
// be written reaches nobody (C1.13, D143).
func TestMergeMintUnauditedIssuesNothing(t *testing.T) {
	s, gh := mergeMintSvcWith(t, func(_ *svc, cfg *Config) {
		cfg.AuditLog = &recordHandler{err: errors.New("the log is not recording")}
	})
	resp, body := s.mintMerge(t, 7, mergeHead(1))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("a merge mint whose line could not be written answered %d, want %d: the token is handed to nobody", resp.StatusCode, StatusUnavailable)
	}
	if e := refusal(t, resp.StatusCode, body); !strings.Contains(e.Message, ErrMergeMintUnaudited.Error()) {
		t.Errorf("the refusal is %q, want the unaudited class (%s)", e.Message, ErrMergeMintUnaudited.Error())
	}
	if n := gh.count(); n != 7 {
		t.Errorf("GitHub was reached %d times, want seven: the token was minted and then withheld", n)
	}
	for _, where := range []struct{ what, text string }{{"the answer", string(body)}, {"the service's log", s.logs.String()}} {
		if strings.Contains(where.text, gh.token) {
			t.Errorf("%s carries the token of a mint that could not be audited", where.what)
		}
	}
}

// ---------------------------------------------------------------------------
// The call's shape, the route's own rules, and the installation behind it
// ---------------------------------------------------------------------------

// TestMergeMintBodyNamesTheFieldsTheDaemonWrites: the four field names are the
// contract between this service and the client the daemon holds
// (internal/creds/mergemint.go), and this test pins them ON THIS SIDE and
// literally — the body below is spelled name by name (mergeMintLiteralBody)
// rather than marshalled from MergeMintRequest, which is the read route's own
// reason for spelling its body too (githubReadBody). A renamed tag would
// otherwise compile, pass both packages, and make this service refuse every real
// daemon call with a 400.
//
// The test below it cannot hold this: every body it sends carries a field this
// contract does not have, so each is refused for that field before the four
// names are reached.
func TestMergeMintBodyNamesTheFieldsTheDaemonWrites(t *testing.T) {
	s, gh := mergeMintSvc(t)
	resp, body := s.callPerson(t, mergeMintPath, mergeMintLiteralBody(testRepoPath, 7, mergeHead(1), testAppID), s.orchestratorToken())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the merge mint refused the body its own client writes: HTTP %d %s", resp.StatusCode, body)
	}
	var answer struct {
		Token      string `json:"token"`
		Repository string `json:"repository"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatalf("the answer is not JSON: %v (%s)", err, body)
	}
	if answer.Token != gh.token {
		t.Errorf("the answer carries no token the fake minted: %s", body)
	}
	if answer.Repository != testRepoPath {
		t.Errorf("the answer names repository %q, want the one the body named (%s)", answer.Repository, testRepoPath)
	}
	if bodies := gh.mintBodies(t); len(bodies) != 2 {
		t.Errorf("the service minted %d installation tokens, want the gate's read token and the answer's", len(bodies))
	}
}

// TestMergeMintBodyIsFourFieldsAndNoMore: the request's shape is the route's
// widening guard. A body that names a permission set, a second repository or an
// API root is refused by the strict decoder rather than read for the fields this
// contract knows, so "no caller can widen this mint" is a property of the route
// and not a check inside it.
func TestMergeMintBodyIsFourFieldsAndNoMore(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"a permission set", strings.TrimSuffix(mergeMintBody(testRepoPath, 7, mergeHead(1), testAppID), "}") + `,"permissions":{"contents":"write"}}`},
		{"a repository list", `{"repository":"` + testRepoPath + `","repositories":["` + testRepoPath + `"],"number":7,"head_sha":"` + mergeHead(1) + `","app_id":"` + testAppID + `"}`},
		{"an api root", `{"repository":"` + testRepoPath + `","number":7,"head_sha":"` + mergeHead(1) + `","app_id":"` + testAppID + `","base_url":"https://elsewhere.invalid"}`},
		{"no head", `{"repository":"` + testRepoPath + `","number":7,"app_id":"` + testAppID + `"}`},
		{"a head that is not a commit", mergeMintBody(testRepoPath, 7, "the-head", testAppID)},
		{"no pull request number", mergeMintBody(testRepoPath, 0, mergeHead(1), testAppID)},
		{"no repository", mergeMintBody("", 7, mergeHead(1), testAppID)},
		{"a repository that is not owner/name", mergeMintBody("aurora", 7, mergeHead(1), testAppID)},
		{"an app id that is not one", mergeMintBody(testRepoPath, 7, mergeHead(1), "not-a-number")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, gh := mergeMintSvc(t)
			resp, body := s.callPerson(t, mergeMintPath, tc.body, s.orchestratorToken())
			if resp.StatusCode != StatusRequestRefused {
				t.Fatalf("the merge mint answered a body that is not this contract's: HTTP %d %s", resp.StatusCode, body)
			}
			if e := refusal(t, resp.StatusCode, body); e.Error != ErrorRequestRefused {
				t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorRequestRefused, e.Message)
			}
			if n := gh.count(); n != 0 {
				t.Errorf("the service reached GitHub %d times for a body it refused, want none", n)
			}
		})
	}
}

// TestMergeMintRefusesARepositoryTheInstallationDoesNotHold: the repository the
// route may be asked about is the installation's own inventory's, and one it
// does not hold is refused BY NAME and mints nothing.
func TestMergeMintRefusesARepositoryTheInstallationDoesNotHold(t *testing.T) {
	s, gh := mergeMintSvc(t)
	resp, body := s.callMergeMint(t, "acme/elsewhere", 7, mergeHead(1), testAppID, s.orchestratorToken())
	if resp.StatusCode != StatusRequestRefused {
		t.Fatalf("the merge mint answered a call for a repository the installation does not hold: HTTP %d %s", resp.StatusCode, body)
	}
	e := refusal(t, resp.StatusCode, body)
	if !strings.Contains(e.Message, ErrGitHubReadUnknownRepository.Error()) {
		t.Errorf("the refusal is %q, want it to name the class it refused (%s)", e.Message, ErrGitHubReadUnknownRepository.Error())
	}
	if !strings.Contains(e.Message, "acme/elsewhere") {
		t.Errorf("the refusal does not name the repository an operator has to act on: %s", e.Message)
	}
	if n := gh.count(); n != 0 {
		t.Errorf("the service reached GitHub %d times for a repository it refused, want none", n)
	}
}

// TestMergeMintFailsClosedWithNoAppKey: a bundle that holds no App key is a
// refusal and never a fallback to a key the caller may hold (D143).
func TestMergeMintFailsClosedWithNoAppKey(t *testing.T) {
	s, gh := mergeMintSvcWith(t, func(s *svc, _ *Config) {
		s.src.docs = [][]byte{testBundleDoc(t)}
	})
	resp, body := s.mintMerge(t, 7, mergeHead(1))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the merge mint minted from a bundle holding no App key: HTTP %d %s", resp.StatusCode, body)
	}
	if e := refusal(t, resp.StatusCode, body); !strings.Contains(e.Message, ErrMergeMintNoAppKey.Error()) {
		t.Errorf("the refusal is %q, want it to name the missing App key (%s)", e.Message, ErrMergeMintNoAppKey.Error())
	}
	if n := gh.count(); n != 0 {
		t.Errorf("the service reached GitHub %d times with no App key to sign with, want none", n)
	}
}

// TestMergeMintIsStoppedByTheGlobalSwitch: the operator's stop switch is read
// BEFORE the bundle, so a stopped service reads no credential at all.
func TestMergeMintIsStoppedByTheGlobalSwitch(t *testing.T) {
	s, gh := mergeMintSvc(t)
	s.kills.global = true
	resp, body := s.mintMerge(t, 7, mergeHead(1))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("a stopped service served a merge mint: HTTP %d %s", resp.StatusCode, body)
	}
	if n := gh.count(); n != 0 {
		t.Errorf("a stopped service reached GitHub %d times, want none", n)
	}
	lines := auditLines(t, s.audit)
	if len(lines) != 1 {
		t.Fatalf("the stopped merge mint wrote %d audit lines, want exactly 1: %v", len(lines), lines)
	}
	if got := lines[0]["purpose"]; got != PurposeMergeMint {
		t.Errorf("the stopped merge mint's audit line purpose = %v, want %q", got, PurposeMergeMint)
	}
}

// TestMergeMintRouteNamesTheClientPath: routeAct and the route list name the
// merge route, so a refusal taken BEFORE the route is branched on — the 404 —
// names it, and the purpose and credential are the ones this route's lines must
// carry. The merge mint's credential is the App key, which is the read mint's
// own: the PURPOSE is what tells the two acts apart, and it is held to being a
// different word from the read mint's here.
func TestMergeMintRouteNamesTheClientPath(t *testing.T) {
	if MergeMintPath != mergeMintPath {
		t.Errorf("the merge mint's path = %q, want %q", MergeMintPath, mergeMintPath)
	}
	if creds.MergeMintPath != MergeMintPath {
		t.Errorf("the client's merge path = %q and the service's = %q, want one path", creds.MergeMintPath, MergeMintPath)
	}
	purpose, credential := routeAct(MergeMintPath)
	if purpose != PurposeMergeMint || credential != config.GitHubAppKeySecret {
		t.Errorf("routeAct(%s) = (%q, %q), want (%q, %q)", MergeMintPath, purpose, credential, PurposeMergeMint, config.GitHubAppKeySecret)
	}
	for _, other := range []string{MintPath, BitbucketReadPath, GitHubReadPath} {
		if other, _ := routeAct(other); other == purpose {
			t.Errorf("the merge mint route shares the purpose %q with %s, so a line cannot tell the acts apart", purpose, other)
		}
	}

	s, _ := mergeMintSvc(t)
	resp, body := s.callPerson(t, "/v1/github/merge", "", s.orchestratorToken())
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an unknown path answered %d, want 404: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), MergeMintPath) {
		t.Errorf("the not-found answer does not name %s among the routes this service serves: %s", MergeMintPath, body)
	}
}

// TestMergeMintLineIsTheRelaxedShapeAndStopsThere: the merge mint's line is a
// Caller with a Repository and no Job and no Run, exactly like the two read
// mints', and the relaxation reaches this purpose and no other.
func TestMergeMintLineIsTheRelaxedShapeAndStopsThere(t *testing.T) {
	if PurposeMergeMint != "merge_mint" {
		t.Fatalf("the merge mint purpose is spelled %q, want %q: it is a word an operator greps a line by", PurposeMergeMint, "merge_mint")
	}
	shape := func(purpose string) AuditEvent {
		return AuditEvent{
			Caller:     testOrchestratorSA,
			Repository: githubSlug(t, testRepoPath),
			Credential: config.GitHubAppKeySecret,
			Purpose:    purpose,
			Status:     StatusOK,
		}
	}
	a, buf := newAudit(t)
	if err := a.Write(t.Context(), shape(PurposeMergeMint)); err != nil {
		t.Fatalf("Write(merge mint): %v", err)
	}
	line := auditLines(t, buf)[0]
	checkAuditShape(t, line)
	for field, want := range map[string]string{
		"caller":     testOrchestratorSA,
		"repository": githubSlug(t, testRepoPath),
		"credential": config.GitHubAppKeySecret,
		"purpose":    PurposeMergeMint,
		"run":        "",
		"job":        "",
		"tier":       "",
		"status":     StatusOK,
	} {
		if got := line[field]; got != want {
			t.Errorf("the merge mint line: %s = %v, want %q", field, got, want)
		}
	}
	for _, purpose := range []string{PurposeForward, PurposeProbe, PurposeMint, PurposeReviewSign} {
		t.Run("under the "+purpose+" purpose", func(t *testing.T) {
			a, buf := newAudit(t)
			if err := a.Write(t.Context(), shape(purpose)); err == nil {
				t.Fatalf("Write accepted the merge mint's shape under the %s purpose", purpose)
			}
			if field := auditLines(t, buf)[0]["field"]; field != "run" {
				t.Errorf("the refusal names field %v, want the run the line does not name", field)
			}
		})
	}
}

// TestMergeMintAnswerIsNotCached: the answer carries a credential's value, so it
// is no-store — an intermediary that cached an installation token would be
// handing the next caller a token this service did not mint for it.
func TestMergeMintAnswerIsNotCached(t *testing.T) {
	s, _ := mergeMintSvc(t)
	resp, body := s.mintMerge(t, 7, mergeHead(1))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the merge mint refused an admitted daemon: HTTP %d %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("the merge mint's answer is Cache-Control %q, want no-store", got)
	}
	if got := resp.Header.Get("Content-Type"); got != contentTypeJSON {
		t.Errorf("the merge mint's answer is Content-Type %q, want %q", got, contentTypeJSON)
	}
}

// TestPreviousInstallationConverges pins the SERVICE's half of the update rule
// (HARD RULE, docs/design/update-in-flight.md): a service configured exactly as
// the release before E16 configured it — no orchestrator accounts at all — starts,
// serves every route it served before unchanged, and answers the new route with
// the class's refusal rather than a 404 or a 200.
//
// What it does NOT pin is the installation module's half: the grant that makes a
// daemon's account reachable, and the environment variable that names it, are
// Terraform and are not carried by this change, so an installation converges by
// the route being INERT — it exists in the binary, it refuses every caller, and
// it mints nothing until the installation names its daemon. The PR says so; when
// the grant lands, the env var is what turns this test's second half from "the
// route refuses everybody" into "the route admits the daemon".
func TestPreviousInstallationConverges(t *testing.T) {
	// The previous release's configuration, verbatim: newSvc's own fixture is
	// that configuration, and it names no orchestrator.
	s := newSvc(t)
	if len(s.auth.cfg.OrchestratorAccounts) != 0 {
		t.Fatalf("the fixture names %d orchestrator accounts, and this test is about an installation that names none", len(s.auth.cfg.OrchestratorAccounts))
	}

	// A run in flight is unaffected: the route the run's own calls take answers
	// exactly as it did before the new branch existed in ServeHTTP.
	resp, body := s.call(t, callBody("converge"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a run in flight was answered %d on the model path, want 200: %s", resp.StatusCode, body)
	}

	// The new route exists and is inert: a caller of the shape the class is for —
	// a service account of this project, carrying this service's own audience —
	// is refused, and so is every other caller, because the list that admits one
	// is empty.
	s2, gh := mergeMintSvcWith(t, func(s *svc, cfg *Config) {
		ac := s.auth.cfg
		ac.OrchestratorAccounts = nil
		a, err := NewAuthenticator(ac)
		if err != nil {
			t.Fatalf("NewAuthenticator: %v", err)
		}
		cfg.Auth = a
	})
	resp, body = s2.callMergeMint(t, testRepoPath, 7, mergeHead(1), testAppID, s2.orchestratorToken())
	if resp.StatusCode == http.StatusNotFound {
		t.Fatalf("the merge route does not exist in this binary: %s", body)
	}
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("an installation naming no orchestrator answered the merge mint %d, want the class's refusal: %s", resp.StatusCode, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorUnverifiedJobIdentity {
		t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorUnverifiedJobIdentity, e.Message)
	}
	if n := gh.count(); n != 0 {
		t.Errorf("an installation naming no orchestrator reached GitHub %d times, want none", n)
	}
}
