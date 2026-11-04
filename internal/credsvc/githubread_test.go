package credsvc

// The GitHub read mint's own tests (githubread.go). They are the Bitbucket read
// mint's tests with the credential changed, and the two properties they exist to
// hold are the two the route's design turns on:
//
//   - the permissions a person's token carries are the coordinator's read-only
//     set and nothing else, asserted against the mint body GitHub was actually
//     sent rather than against the constant the service passed in;
//   - the route admits the installation's people and NOBODY else — not a job,
//     not a verifier, not the coordinator.
//
// Both are mutation proofs and are written as such: TestGitHubReadAsksForNoWrite
// fails for a mutant that adds any write permission to the set, and
// TestGitHubReadAdmitsOnlyAPerson fails for a mutant that lets another class
// through the person lookup. The cross-class matrix (callerclass_test.go) aims
// every class at this route as well.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/task"
)

// githubReadPath is the read mint's path, spelled as the contract spells it. It
// is a literal on purpose: the tests below hold the constant to it, so a rename
// of either cannot quietly move the route.
const githubReadPath = "/v1/github/read-token"

// githubReadBody is a GitHub read call's body: the one repository the token is
// asked for and the App ID the mint signs as.
func githubReadBody(repo, appID string) string {
	return `{"repository":"` + repo + `","app_id":"` + appID + `"}`
}

// githubSlug is the slug the installation's inventory keys one repository by,
// derived rather than spelled: it is the derivation the service itself resolves
// a repository through (task.Slug), so a test that spelled it could pass while
// the service looked somewhere else.
func githubSlug(t *testing.T, repo string) string {
	t.Helper()
	slug, err := task.Slug(gitprov.KindGitHub, repo)
	if err != nil {
		t.Fatalf("slug of %s: %v", repo, err)
	}
	return slug
}

// githubReadSvc is the whole fixture for a read mint test: a service whose
// bundle holds the App key, whose inventory holds the GitHub repository, whose
// people list is the test's, and the fake GitHub behind it.
func githubReadSvc(t *testing.T, people ...string) (*svc, *ghFake) {
	t.Helper()
	return githubReadSvcWith(t, nil, people...)
}

// githubReadSvcWith is githubReadSvc with the service's own configuration under
// the test's control, for the cases that need one wired differently: an audit
// log that cannot write, an inventory holding a second repository.
func githubReadSvcWith(t *testing.T, mutate func(*svc, *Config), people ...string) (*svc, *ghFake) {
	t.Helper()
	gh := newGHFake(t)
	s := newSvcWith(t, func(s *svc, cfg *Config) {
		ac := s.auth.cfg
		ac.PersonAccounts = people
		a, err := NewAuthenticator(ac)
		if err != nil {
			t.Fatalf("NewAuthenticator: %v", err)
		}
		cfg.Auth = a
		cfg.Repos = map[string]string{githubSlug(t, testRepoPath): testRepo}
		cfg.Client = &http.Client{Transport: ghTransport{srv: gh.srv}}
		s.src.docs = [][]byte{mintBundleDoc(t)}
		if mutate != nil {
			mutate(s, cfg)
		}
	})
	return s, gh
}

// readGitHub sends one read mint for repo as person and returns the answer. It
// is the shape every test below starts from, whether the call is admitted or
// refused.
func (s *svc) readGitHub(t *testing.T, repo, appID, person string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	return s.callPerson(t, githubReadPath, githubReadBody(repo, appID), s.personToken(person), mutate...)
}

// TestGitHubReadMintsAReadOnlyTokenForAPerson: a person's call is admitted, the
// answer carries the token, its life and the repository it was minted for, and
// the mint GitHub was actually asked for is scoped to that one repository and
// carries the App ID the call named.
func TestGitHubReadMintsAReadOnlyTokenForAPerson(t *testing.T) {
	s, gh := githubReadSvc(t, testPersonAccount)

	resp, body := s.readGitHub(t, testRepoPath, testAppID, testPersonAccount)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the GitHub read mint refused an admitted person: HTTP %d %s", resp.StatusCode, body)
	}
	var ans readAnswer
	if err := json.Unmarshal(body, &ans); err != nil {
		t.Fatalf("the answer is not one this contract can read: %v (%d bytes)", err, len(body))
	}
	if ans.Token != gh.token || ans.ExpiresAt == "" || ans.Repository != testRepoPath {
		t.Errorf("the answer does not name the token, its life and the repository: %+v", ans)
	}

	lookup, mint := gh.both(t)
	owner, name, _ := strings.Cut(testRepoPath, "/")
	wantPath := "/repos/" + owner + "/" + name + "/installation"
	if lookup.method != http.MethodGet || lookup.path != wantPath {
		t.Errorf("the installation lookup was %s %s, want GET %s", lookup.method, lookup.path, wantPath)
	}
	if mint.method != http.MethodPost || !strings.HasSuffix(mint.path, "/access_tokens") {
		t.Errorf("the mint was %s %s, want POST .../access_tokens", mint.method, mint.path)
	}
	var sent struct {
		Repositories []string          `json:"repositories"`
		Permissions  map[string]string `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(mint.body), &sent); err != nil {
		t.Fatalf("the mint's body is not the object this contract writes: %v (%q)", err, mint.body)
	}
	if len(sent.Repositories) != 1 || sent.Repositories[0] != name {
		t.Errorf("the mint names repositories %v, want exactly [%s]: a token scoped to another repository is one this read must not have", sent.Repositories, name)
	}
}

// TestGitHubReadAsksForNoWrite is the permission half of the route's mutation
// proof: the set GitHub is asked for is EXACTLY the coordinator's read-only set,
// asserted field by field against the body the service actually sent.
//
// A mutant that granted the person any permission the coordinator's set does not
// hold — a contents write, a pull-requests write, an extra noun — fails here,
// because the comparison is against the constant's own contents and not against
// a subset. The second half of the test says the same thing in the vocabulary an
// operator greps: no value the mint asked for is a write.
func TestGitHubReadAsksForNoWrite(t *testing.T) {
	s, gh := githubReadSvc(t, testPersonAccount)
	if _, body := s.readGitHub(t, testRepoPath, testAppID, testPersonAccount); !strings.Contains(string(body), `"token"`) {
		t.Fatalf("the read mint refused an admitted person: %s", body)
	}
	_, mint := gh.both(t)
	var sent struct {
		Permissions map[string]string `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(mint.body), &sent); err != nil {
		t.Fatalf("the mint's body is not the object this contract writes: %v (%q)", err, mint.body)
	}
	want := github.CoordinatorTokenPermissions()
	if len(sent.Permissions) != len(want) {
		t.Fatalf("the read mint asked for %d permissions %v, want the coordinator's %d %v: this route mints a READ and adds no reach of its own",
			len(sent.Permissions), sent.Permissions, len(want), want)
	}
	for noun, level := range want {
		if got := sent.Permissions[noun]; got != level {
			t.Errorf("the read mint asked for %s=%q, want %q", noun, got, level)
		}
	}
	for noun, level := range sent.Permissions {
		if strings.Contains(level, "write") || strings.Contains(noun, "write") {
			t.Errorf("the read mint asked for a write: %s=%q", noun, level)
		}
	}
}

// TestGitHubReadAdmitsOnlyAPerson is the class half of the route's mutation
// proof: the four callers below are not the installation's people, and each is
// refused by the class and reaches no GitHub call at all.
//
// It is the same property the matrix aims at this route, written here as its own
// case because it is what the route's whole admission is: a mutant that widened
// VerifyPersonCaller — to the coordinator's list, to the job accounts, to the
// verifiers — is refused a token by this test, and the zero GitHub calls say the
// mutant cannot even have got as far as the bundle.
func TestGitHubReadAdmitsOnlyAPerson(t *testing.T) {
	s, gh := githubReadSvc(t, testPersonAccount)
	for _, tc := range []struct {
		name string
		tok  string
	}{
		{"a per-repository job's account", s.jobToken()},
		{"the coordinator's account", s.personToken(testCoordinatorAccount)},
		{"a stranger", s.personToken(testOtherPerson)},
		{"a token of another audience", s.personToken(testPersonAccount, func(c map[string]any) { c["aud"] = "https://elsewhere.invalid" })},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := s.callPerson(t, githubReadPath, githubReadBody(testRepoPath, testAppID), tc.tok)
			if resp.StatusCode != StatusUnverified {
				t.Fatalf("the GitHub read mint admitted %s: HTTP %d %s", tc.name, resp.StatusCode, body)
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

// TestGitHubReadRefusesARepositoryTheInstallationDoesNotHold: the repository the
// route may be asked about is the installation's own inventory's, and one it
// does not hold is refused BY NAME and mints nothing.
func TestGitHubReadRefusesARepositoryTheInstallationDoesNotHold(t *testing.T) {
	s, gh := githubReadSvc(t, testPersonAccount)
	resp, body := s.readGitHub(t, "acme/elsewhere", testAppID, testPersonAccount)
	if resp.StatusCode != StatusRequestRefused {
		t.Fatalf("the read mint answered a call for a repository the installation does not hold: HTTP %d %s", resp.StatusCode, body)
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

// TestGitHubReadBodyIsTwoFieldsAndNoMore: the request's shape is the route's
// widening guard. A body that names a permission set, a second repository or an
// API root is refused by the strict decoder rather than read for the fields this
// contract knows, so "no caller can widen this mint" is a property of the route
// and not a check inside it.
func TestGitHubReadBodyIsTwoFieldsAndNoMore(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"a permission set", githubReadBody(testRepoPath, testAppID)[:len(githubReadBody(testRepoPath, testAppID))-1] + `,"permissions":{"contents":"write"}}`},
		{"a repository list", `{"repository":"` + testRepoPath + `","repositories":["` + testRepoPath + `"],"app_id":"` + testAppID + `"}`},
		{"an api root", `{"repository":"` + testRepoPath + `","app_id":"` + testAppID + `","base_url":"https://elsewhere.invalid"}`},
		{"no app id", `{"repository":"` + testRepoPath + `"}`},
		{"an app id that is not one", githubReadBody(testRepoPath, "not-a-number")},
		{"no repository", `{"app_id":"` + testAppID + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, gh := githubReadSvc(t, testPersonAccount)
			resp, body := s.callPerson(t, githubReadPath, tc.body, s.personToken(testPersonAccount))
			if resp.StatusCode != StatusRequestRefused {
				t.Fatalf("the read mint answered a body that is not this contract's: HTTP %d %s", resp.StatusCode, body)
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

// TestGitHubReadFailsClosedWithNoAppKey: a bundle that holds no App key is a
// refusal and never a fallback to a key the caller may hold (D143). The service
// has nothing to sign with, so the read is refused rather than served the wrong
// credential.
func TestGitHubReadFailsClosedWithNoAppKey(t *testing.T) {
	s, gh := githubReadSvcWith(t, func(s *svc, _ *Config) {
		// The fixture's own document: the model credentials and no App key.
		s.src.docs = [][]byte{testBundleDoc(t)}
	}, testPersonAccount)
	resp, body := s.readGitHub(t, testRepoPath, testAppID, testPersonAccount)
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the read mint minted from a bundle holding no App key: HTTP %d %s", resp.StatusCode, body)
	}
	if e := refusal(t, resp.StatusCode, body); !strings.Contains(e.Message, ErrGitHubReadNoAppKey.Error()) {
		t.Errorf("the refusal is %q, want it to name the missing App key (%s)", e.Message, ErrGitHubReadNoAppKey.Error())
	}
	if n := gh.count(); n != 0 {
		t.Errorf("the service reached GitHub %d times with no App key to sign with, want none", n)
	}
}

// TestGitHubReadIsStoppedByTheGlobalSwitch: the operator's stop switch is read
// BEFORE the bundle, so a stopped service reads no credential at all, and the
// refusal is the state_unavailable one every other route answers with.
func TestGitHubReadIsStoppedByTheGlobalSwitch(t *testing.T) {
	s, gh := githubReadSvc(t, testPersonAccount)
	s.kills.global = true
	resp, body := s.readGitHub(t, testRepoPath, testAppID, testPersonAccount)
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("a stopped service served a read mint: HTTP %d %s", resp.StatusCode, body)
	}
	if n := gh.count(); n != 0 {
		t.Errorf("a stopped service reached GitHub %d times, want none", n)
	}
	lines := auditLines(t, s.audit)
	if len(lines) != 1 {
		t.Fatalf("the stopped read mint wrote %d audit lines, want exactly 1: %v", len(lines), lines)
	}
	if got := lines[0]["purpose"]; got != PurposeGitHubRead {
		t.Errorf("the stopped read mint's audit line purpose = %v, want %q", got, PurposeGitHubRead)
	}
}

// TestGitHubReadTakesNoRunIdentity: the route's caller is a person, who holds no
// run identity. A caller that presents one anyway is refused by name — a whole
// identity beside the token is a call of a shape this route does not have — and
// the refusal is written down like every other one after admission.
func TestGitHubReadTakesNoRunIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*http.Request)
	}{
		{"presents a run identity", func(r *http.Request) { r.Header.Set(HeaderRunIdentity, "a-run-identity-token") }},
		{"names a job", func(r *http.Request) { r.Header.Set(HeaderRunJob, "fugaro-a-job") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, gh := githubReadSvc(t, testPersonAccount)
			resp, body := s.readGitHub(t, testRepoPath, testAppID, testPersonAccount, tc.mutate)
			if resp.StatusCode != StatusUnverified {
				t.Fatalf("the read mint answered a call carrying a run identity: HTTP %d %s", resp.StatusCode, body)
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

// TestGitHubReadAuditsOneLineAndNoCredential: the line names the person, the
// repository's SLUG and the App key, carries no run and no job, and carries no
// value — neither the App key nor the token GitHub minted.
func TestGitHubReadAuditsOneLineAndNoCredential(t *testing.T) {
	s, gh := githubReadSvc(t, testPersonAccount)
	resp, body := s.readGitHub(t, testRepoPath, testAppID, testPersonAccount)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the GitHub read mint refused an admitted person: HTTP %d %s", resp.StatusCode, body)
	}
	lines := auditLines(t, s.audit)
	if len(lines) != 1 {
		t.Fatalf("the read mint wrote %d audit lines, want exactly 1: %v", len(lines), lines)
	}
	line := lines[0]
	for field, want := range map[string]string{
		"caller":     testPersonAccount,
		"repository": githubSlug(t, testRepoPath),
		"credential": config.GitHubAppKeySecret,
		"purpose":    PurposeGitHubRead,
		"run":        "",
		"job":        "",
		"tier":       "",
		"status":     StatusOK,
	} {
		if got := line[field]; got != want {
			t.Errorf("the read mint's line: %s = %v, want %q", field, got, want)
		}
	}
	// The token and the key reach neither the line, the answer's other fields
	// nor the service's own log. pemCore is the one part of a PEM that survives
	// being embedded in JSON.
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
}

// TestGitHubReadRefusesWhenGitHubDoes: GitHub's own refusal is answered with
// this service's sentence and never a byte of GitHub's text — the request that
// failed carried the App's JWT, so an upstream that echoed the request would be
// putting a bearer credential in this service's log.
func TestGitHubReadRefusesWhenGitHubDoes(t *testing.T) {
	s, gh := githubReadSvc(t, testPersonAccount)
	gh.status, gh.failBody = http.StatusUnauthorized, `{"message":"the JWT is not one this App signed"}`
	resp, body := s.readGitHub(t, testRepoPath, testAppID, testPersonAccount)
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the read mint served a call GitHub refused: HTTP %d %s", resp.StatusCode, body)
	}
	e := refusal(t, resp.StatusCode, body)
	if e.Error != ErrorStateUnavailable {
		t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorStateUnavailable, e.Message)
	}
	for _, where := range []struct{ what, text string }{
		{"the refusal", e.Message},
		{"the service's log", s.logs.String()},
	} {
		if strings.Contains(where.text, "the JWT is not one this App signed") {
			t.Errorf("%s repeats GitHub's own text", where.what)
		}
	}
}

// TestGitHubReadUnauditedIssuesNothing: a token GitHub granted whose line cannot
// be written reaches nobody (C1.13, D143). The answer is the refusal, and the
// token appears nowhere in it.
func TestGitHubReadUnauditedIssuesNothing(t *testing.T) {
	// The handler is handed to the SERVICE and not to an auditor built here:
	// the read mint writes through the auditor the service builds for every
	// line it writes (NewServer's NewProbeAuditor), so a handler the service
	// cannot write through is one the mint cannot either.
	s, gh := githubReadSvcWith(t, func(_ *svc, cfg *Config) {
		cfg.AuditLog = &recordHandler{err: errors.New("the log is not recording")}
	}, testPersonAccount)
	resp, body := s.readGitHub(t, testRepoPath, testAppID, testPersonAccount)
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("a read mint whose line could not be written answered %d, want %d: the token is handed to nobody", resp.StatusCode, StatusUnavailable)
	}
	if e := refusal(t, resp.StatusCode, body); !strings.Contains(e.Message, ErrGitHubReadUnaudited.Error()) {
		t.Errorf("the refusal is %q, want the unaudited class (%s)", e.Message, ErrGitHubReadUnaudited.Error())
	}
	if n := gh.count(); n != 2 {
		t.Errorf("GitHub was reached %d times, want two: the token was minted and then withheld", n)
	}
	for _, where := range []struct{ what, text string }{{"the answer", string(body)}, {"the service's log", s.logs.String()}} {
		if strings.Contains(where.text, gh.token) {
			t.Errorf("%s carries the token of a mint that could not be audited", where.what)
		}
	}
}

// TestGitHubReadRouteNamesTheClientPath: routeAct and the route list name the
// read route, so a refusal taken BEFORE the route is branched on — the 404 —
// names it, and the purpose and credential are the ones this route's lines must
// carry. The GitHub read's credential is the App key, which is the mint's own:
// the PURPOSE is what tells the two acts apart, and it is held to being a
// different word from the mint's here.
func TestGitHubReadRouteNamesTheClientPath(t *testing.T) {
	if GitHubReadPath != githubReadPath {
		t.Errorf("the read mint's path = %q, want %q", GitHubReadPath, githubReadPath)
	}
	if github.ServiceReadPath != GitHubReadPath {
		t.Errorf("the client's read path = %q and the service's = %q, want one path", github.ServiceReadPath, GitHubReadPath)
	}
	purpose, credential := routeAct(GitHubReadPath)
	if purpose != PurposeGitHubRead || credential != config.GitHubAppKeySecret {
		t.Errorf("routeAct(%s) = (%q, %q), want (%q, %q)", GitHubReadPath, purpose, credential, PurposeGitHubRead, config.GitHubAppKeySecret)
	}
	for _, other := range []string{MintPath, BitbucketReadPath} {
		if other, _ := routeAct(other); other == purpose {
			t.Errorf("the GitHub read route shares the purpose %q with %s, so a line cannot tell the acts apart", purpose, other)
		}
	}
	if githubReadPath == BitbucketReadPath {
		t.Errorf("the two read routes are the same path %q", githubReadPath)
	}

	s, _ := githubReadSvc(t, testPersonAccount)
	resp, body := s.callPerson(t, "/v1/github/read", "", s.personToken(testPersonAccount))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an unknown path answered %d, want 404: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), GitHubReadPath) {
		t.Errorf("the not-found answer does not name %s among the routes this service serves: %s", GitHubReadPath, body)
	}
}

// TestGitHubReadLineIsTheRelaxedShapeAndStopsThere: the GitHub read's line is a
// Caller with a Repository and no Job and no Run, exactly like the Bitbucket
// read's, and the relaxation reaches this purpose and no other. A job purpose
// handed that shape is refused — which is what keeps the exemption from spreading
// to the purposes a reader greps for a run.
func TestGitHubReadLineIsTheRelaxedShapeAndStopsThere(t *testing.T) {
	if PurposeGitHubRead != "github_read" {
		t.Fatalf("the GitHub read purpose is spelled %q, want %q: it is a word an operator greps a line by", PurposeGitHubRead, "github_read")
	}
	shape := func(purpose string) AuditEvent {
		return AuditEvent{
			Caller:     testPersonAccount,
			Repository: githubSlug(t, testRepoPath),
			Credential: config.GitHubAppKeySecret,
			Purpose:    purpose,
			Status:     StatusOK,
		}
	}
	a, buf := newAudit(t)
	if err := a.Write(t.Context(), shape(PurposeGitHubRead)); err != nil {
		t.Fatalf("Write(GitHub read): %v", err)
	}
	line := auditLines(t, buf)[0]
	checkAuditShape(t, line)
	for field, want := range map[string]string{
		"run":        "",
		"repository": githubSlug(t, testRepoPath),
		"job":        "",
		"caller":     testPersonAccount,
		"credential": config.GitHubAppKeySecret,
		"purpose":    PurposeGitHubRead,
		"tier":       "",
		"status":     StatusOK,
	} {
		if got := line[field]; got != want {
			t.Errorf("the GitHub read line: %s = %v, want %q", field, got, want)
		}
	}
	for _, purpose := range []string{PurposeForward, PurposeProbe, PurposeMint, PurposeReviewSign} {
		t.Run("under the "+purpose+" purpose", func(t *testing.T) {
			a, buf := newAudit(t)
			if err := a.Write(t.Context(), shape(purpose)); err == nil {
				t.Fatalf("Write accepted the read mint's shape under the %s purpose", purpose)
			}
			if field := auditLines(t, buf)[0]["field"]; field != "run" {
				t.Errorf("the refusal names field %v, want the run the line does not name", field)
			}
		})
	}
}

// TestGitHubReadAnswerIsNotCached: the answer carries a credential's value, so
// it is no-store — an intermediary that cached an installation token would be
// handing the next caller a token this service did not mint for it.
func TestGitHubReadAnswerIsNotCached(t *testing.T) {
	s, _ := githubReadSvc(t, testPersonAccount)
	resp, body := s.readGitHub(t, testRepoPath, testAppID, testPersonAccount)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the GitHub read mint refused an admitted person: HTTP %d %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("the read mint's answer is Cache-Control %q, want no-store", got)
	}
	if got := resp.Header.Get("Content-Type"); got != contentTypeJSON {
		t.Errorf("the read mint's answer is Content-Type %q, want %q", got, contentTypeJSON)
	}
}
