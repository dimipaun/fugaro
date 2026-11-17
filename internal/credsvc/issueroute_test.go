package credsvc

// The issue route's own tests (issueroute.go, B3, D2718, D2439, C1.13, F22,
// F39). They are the label route's tests with the acts changed, and the
// properties they exist to hold are the ones the route's design turns on:
//
//   - the token: the daemon asks for the act and the SERVICE performs it, so
//     the installation token is minted for this service's own use, appears in
//     no answer, no audit line and no log line, and every call this route makes
//     to GitHub carries it (TestIssueRouteNeverReturnsAToken, D2439);
//   - the reach: the token this route mints carries exactly `{issues: write,
//     metadata: read}` — NOT the label route's `pull_requests: write`, which
//     cannot touch an issue at all — is cached until near expiry, and is held
//     in a cache of this route's own (TestIssueRouteMintsIssuesScopeOnly, F39);
//   - the repository: a name this installation's GITHUB inventory does not hold
//     is refused, the one it holds only on Bitbucket included, because this
//     route has no act that host could answer
//     (TestIssueRouteRefusesForeignRepo, D2718);
//   - the class: the orchestrator daemon and nobody else, whatever audience the
//     token carries (TestIssueRouteRefusesEveryOtherClass);
//   - the text: a title of at most 256 characters and a body of at most 8000,
//     counted in characters and not in bytes, which is what a daemon carrying
//     an author's own writing needs (TestIssueRouteRefusesOversizedIssue,
//     D2718);
//   - the record: every call that reaches a verified caller writes exactly one
//     audit line, served or refused, naming the daemon, the repository, the App
//     key and the act (TestIssueRouteAuditsEveryCall, C1.13);
//   - the rate: 240 reads and 60 creations an hour for ONE repository, counted
//     apart, at the label route's own numbers
//     (TestIssueRouteRateLimitedPerRepo, F22);
//   - the path: one spelling, shared with the client that will call it
//     (TestIssueRoutePathMatchesClient).
//
// Every one of them is a mutation proof and is written as such: minting the
// label route's set, reusing the label route's cache, admitting another class,
// counting bytes instead of characters or dropping the audit line turns one of
// them red — and the fake GitHub below records the requests the service
// actually made, so "the service reached GitHub zero times" is a claim a test
// reads rather than assumes.
//
// Everything here is hermetic (Convention 9): the GitHub API is a fake on
// loopback reached through the transport that refuses every other destination
// (ghTransport), and the App key and every token are built at run time
// (Convention 2).

import (
	"encoding/json"
	"io"
	"log/slog"
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

// issuePath is the issue route's path, spelled as the contract spells it. It is
// a literal on purpose: the tests below hold the constant to it, so a rename of
// either cannot quietly move the route.
const issuePath = "/v1/github/issues"

// issueAppSlug is the App's own slug, which is how the fake GitHub spells
// Fugaro's identity, and issueAppAuthor is that identity as an issue's author:
// REST spells a bot account `<slug>[bot]` (github's own botSuffix).
const (
	issueAppSlug   = "fugaro-aurora"
	issueAppAuthor = issueAppSlug + "[bot]"
)

// The eight fields of the contract's issue (creds.Issue), spelled here rather
// than derived from the struct: the shape a caller reads is the contract, so a
// field added to the answer — which is how a token would reach one — is a
// failure and not a test that follows it.
var issueAnswerFields = []string{"number", "title", "body", "url", "state", "author_login", "author_id", "app_slug"}

// The numbers the fixture's fake GitHub uses, spelled here so the assertions
// below state them rather than read them back out of the fake.
const (
	issueMirroredNumber = 12 // an issue this installation's App opened
	issuePullNumber     = 7  // the host lists a pull request as an issue
	issuePersonNumber   = 9  // a person's own issue
	issueCreatedNumber  = 31 // what a creation answers with
)

// ---------------------------------------------------------------------------
// The fake GitHub
// ---------------------------------------------------------------------------

// issueItem is one issue the fake answers for, in the fields the adapter reads.
type issueItem struct {
	number  int
	title   string
	body    string
	state   string
	login   string
	userID  int64
	appSlug string
	// pr makes the item a pull request, which is what the host's issue
	// endpoints report with a `pull_request` field and what an item is never
	// (D2720): the listing must drop it and the single read must refuse it.
	pr bool
}

// wire is the item as REST reports it.
func (i issueItem) wire() map[string]any {
	out := map[string]any{
		"number": i.number, "title": i.title, "body": i.body, "state": i.state,
		"html_url": "https://github.com/" + testRepoPath + "/issues/" + strconv.Itoa(i.number),
		"user":     nil,
	}
	if i.login != "" {
		out["user"] = map[string]any{"login": i.login, "id": i.userID}
	}
	if i.appSlug != "" {
		out["performed_via_github_app"] = map[string]any{"slug": i.appSlug}
	}
	if i.pr {
		out["pull_request"] = map[string]any{"url": "https://api.github.com/repos/" + testRepoPath + "/pulls/" + strconv.Itoa(i.number)}
	}
	return out
}

// issueFake stands in for api.github.com for the issue route: the installation
// and the token the service mints for its own acts, the open issue listing, one
// issue by number and the one creation. It records every request, so a test can
// read what the service really asked for — and the transport that reaches it
// refuses every other host.
//
// It answers for ANY repository under /repos/, because the rate limit is per
// repository and a test of that has to see a second repository served while the
// first is over its rate.
type issueFake struct {
	t   *testing.T
	srv *httptest.Server

	mu   sync.Mutex
	seen []ghSeen
	// listQ is the query of every listing the fake answered, in order, which
	// is how a test holds the adapter to asking GitHub for the OPEN ones.
	listQ []string

	// slug is what GET /app answers: the App's own identity.
	slug string
	// token and expires are what the installation mint answers with.
	token   string
	expires time.Time
	// installation is the id the installation lookup answers.
	installation int64
	// open is what the open listing answers, in the host's order.
	open []issueItem
	// one is what a single read answers, with the number it was asked for.
	one issueItem
	// created is what a creation answers with: the issue GitHub made, whose
	// number is what the item's external id is written from (D2728).
	created issueItem
	// mintStatus, listStatus, readStatus and createStatus, when not 2xx, are
	// what those calls answer with instead: the mint failing, the listing
	// failing, the single read failing and the creation failing.
	mintStatus, listStatus, readStatus, createStatus int
}

func newIssueFake(t *testing.T) *issueFake {
	t.Helper()
	f := &issueFake{
		t:            t,
		slug:         issueAppSlug,
		token:        fakeCred(t, "issue-token"),
		expires:      ghExpiry,
		installation: 777,
		open: []issueItem{
			{number: issueMirroredNumber, title: "the mirror's item", body: "the item's own text",
				state: "open", login: issueAppAuthor, userID: 900001, appSlug: issueAppSlug},
			{number: issuePullNumber, title: "a pull request the host lists too", state: "open", pr: true},
			{number: issuePersonNumber, title: "a person's issue", state: "open", login: "octocat", userID: 1},
		},
		one: issueItem{number: issuePersonNumber, title: "a person's issue", body: "what a person wrote",
			state: "closed", login: "octocat", userID: 1},
		created: issueItem{number: issueCreatedNumber, title: "a mirrored item", body: "the item's text",
			state: "open", login: issueAppAuthor, userID: 900001, appSlug: issueAppSlug},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// splitRepoPath cuts one repository path into the repository it names and the
// rest of the path, so the fake answers for whichever repository a call is for.
func splitRepoPath(path string) (repo, rest string, ok bool) {
	rest, ok = strings.CutPrefix(path, "/repos/")
	if !ok {
		return "", "", false
	}
	owner, tail, ok := strings.Cut(rest, "/")
	if !ok {
		return "", "", false
	}
	name, rest, ok := strings.Cut(tail, "/")
	if !ok {
		return "", "", false
	}
	return owner + "/" + name, rest, true
}

func (f *issueFake) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		f.t.Errorf("reading the request body: %v", err)
	}
	f.mu.Lock()
	f.seen = append(f.seen, ghSeen{method: r.Method, path: r.URL.Path, body: string(body), auth: r.Header.Get("Authorization")})
	slug, token, expires, installation := f.slug, f.token, f.expires, f.installation
	open, one, created := f.open, f.one, f.created
	mintStatus, listStatus, readStatus, createStatus := f.mintStatus, f.listStatus, f.readStatus, f.createStatus
	f.mu.Unlock()

	// The App's own identity and the installation mint are answered first and
	// on their own: neither path is under a repository, and both are the
	// service's own calls — a caller of this route never sees either.
	if r.Method == http.MethodGet && r.URL.Path == "/app" {
		f.write(w, http.StatusOK, map[string]any{"slug": slug, "id": 99})
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/app/installations/"+strconv.FormatInt(installation, 10)+"/access_tokens" {
		if mintStatus != 0 && mintStatus/100 != 2 {
			w.WriteHeader(mintStatus)
			return
		}
		f.write(w, http.StatusCreated, map[string]any{
			"token":      token,
			"expires_at": expires.UTC().Format(time.RFC3339),
		})
		return
	}

	repo, rest, ok := splitRepoPath(r.URL.Path)
	if !ok {
		f.t.Errorf("the fake GitHub was asked for %s %s, and the issue route reads a repository's installation and its issues and nothing else", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	switch {
	case r.Method == http.MethodGet && rest == "installation":
		f.write(w, http.StatusOK, map[string]any{"id": installation})
	case r.Method == http.MethodGet && rest == "issues":
		f.mu.Lock()
		f.listQ = append(f.listQ, r.URL.RawQuery)
		f.mu.Unlock()
		if listStatus != 0 && listStatus/100 != 2 {
			w.WriteHeader(listStatus)
			return
		}
		out := make([]map[string]any, 0, len(open))
		for _, item := range open {
			out = append(out, item.wire())
		}
		f.write(w, http.StatusOK, out)
	case r.Method == http.MethodGet && strings.HasPrefix(rest, "issues/"):
		if readStatus != 0 && readStatus/100 != 2 {
			w.WriteHeader(readStatus)
			return
		}
		number, err := strconv.Atoi(strings.TrimPrefix(rest, "issues/"))
		if err != nil {
			f.t.Errorf("the fake was asked for issue %q", rest)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		item := one
		item.number = number
		f.write(w, http.StatusOK, item.wire())
	case r.Method == http.MethodPost && rest == "issues":
		if createStatus != 0 && createStatus/100 != 2 {
			w.WriteHeader(createStatus)
			return
		}
		f.write(w, http.StatusCreated, created.wire())
	default:
		f.t.Errorf("the fake GitHub was asked for %s %s of %s, and the issue route reads an installation, reads the open issues, reads one and creates one and does nothing else", r.Method, rest, repo)
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *issueFake) write(w http.ResponseWriter, status int, body any) {
	raw, err := json.Marshal(body)
	if err != nil {
		f.t.Errorf("encoding the fake's answer: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

// requests is what the fake was asked, in order.
func (f *issueFake) requests() []ghSeen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ghSeen(nil), f.seen...)
}

// count is how many requests the fake saw.
func (f *issueFake) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.seen)
}

// issues are the requests the fake saw for an issue endpoint of any repository:
// the two reads and the one creation.
func (f *issueFake) issues() []ghSeen {
	var out []ghSeen
	for _, seen := range f.requests() {
		if strings.Contains(seen.path, "/issues") {
			out = append(out, seen)
		}
	}
	return out
}

// writes are the creations the fake saw. A route that refused a call before the
// act must have none.
func (f *issueFake) writes() []ghSeen {
	var out []ghSeen
	for _, seen := range f.requests() {
		if seen.method == http.MethodPost && strings.HasSuffix(seen.path, "/issues") {
			out = append(out, seen)
		}
	}
	return out
}

// opened is the last creation's body, as the title and the body GitHub was
// asked to open an issue with.
func (f *issueFake) opened(t *testing.T) (title, text string) {
	t.Helper()
	wrote := f.writes()
	if len(wrote) == 0 {
		t.Fatal("the route created no issue, and this test reads what it asked GitHub to open")
	}
	var sent struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal([]byte(wrote[len(wrote)-1].body), &sent); err != nil {
		t.Fatalf("a creation's body is not the object this adapter writes: %v (%q)", err, wrote[len(wrote)-1].body)
	}
	return sent.Title, sent.Body
}

// mints is the body of every installation token the fake minted, in order.
func (f *issueFake) mints(t *testing.T) []map[string]any {
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

// issueRouteSvc is the whole fixture for an issue route test: a service whose
// bundle holds the App key, whose inventory holds this installation's GitHub
// repositories, whose classes are all six of the caller-class matrix AND the
// orchestrator daemon, and the fake GitHub behind it.
//
// The six are configured and not omitted because the route's whole admission is
// its class: a fixture that held only the daemon's own account could not tell
// "refused by the class" from "refused because nobody was configured".
func issueRouteSvc(t *testing.T) (*svc, *issueFake) {
	t.Helper()
	return issueRouteSvcWith(t, nil)
}

// issueRouteSvcWith is issueRouteSvc with the service's own configuration, the
// bundle it reads and the fake GitHub under the test's control, for the cases
// that need one wired differently: an audit log that cannot write, a bundle
// with no App key, a GitHub that refuses an act.
func issueRouteSvcWith(t *testing.T, mutate func(t *testing.T, s *svc, cfg *Config, gh *issueFake)) (*svc, *issueFake) {
	t.Helper()
	gh := newIssueFake(t)
	s := newSvcWith(t, func(s *svc, cfg *Config) {
		auth, err := NewAuthenticator(AuthConfig{
			Project:              testProject,
			FirebaseProject:      testFirebase,
			Audience:             testAudience,
			JobAccounts:          []string{testJobSA},
			Keys:                 s.keys,
			JobKeys:              s.jobKeys,
			Outcomes:             RTDBOutcomes{DB: s.db},
			ReviewJobAccounts:    []ReviewJobAccount{{Email: testReviewSA, Account: testReviewAccount}},
			VerifierAccounts:     []string{testVerifierSA},
			PersonAccounts:       []string{testPersonAccount},
			CoordinatorAccount:   testCoordinatorAccount,
			LauncherAccounts:     []string{testLauncherAccount},
			OrchestratorAccounts: []string{testOrchestratorSA},
			Now:                  func() time.Time { return s.now },
		})
		if err != nil {
			t.Fatalf("the issue route's classes cannot be configured together: %v", err)
		}
		cfg.Auth, s.auth = auth, auth
		cfg.ReviewAccounts = []ReviewJobAccount{{Email: testReviewSA, Account: testReviewAccount}}
		// The inventory holds two GitHub repositories and one Bitbucket one:
		// the second GitHub repository is what "the rate is per repository"
		// needs, and the Bitbucket one is a repository this installation holds
		// and this route still does not serve (D2718).
		cfg.Repos = map[string]string{
			githubSlug(t, testRepoPath):         testRepo,
			githubSlug(t, testOtherRepoPath):    testOtherRepo,
			bitbucketSlug(t, testBitbucketRepo): testCanonical(t, testBitbucketRepo),
		}
		cfg.Client = &http.Client{Transport: ghTransport{srv: gh.srv}}
		s.src.docs = [][]byte{mintBundleDoc(t)}
		if mutate != nil {
			mutate(t, s, cfg, gh)
		}
	})
	return s, gh
}

// issueBody is one issue call's body as the wire carries it: the fields the
// daemon writes, spelled here rather than marshalled from the service's own
// struct, so a rename on either side is a test failure.
func issueBody(repo, action string, number int, title, text string) string {
	body := `{"repository":` + strconv.Quote(repo) + `,"action":"` + action + `","app_id":"` + testAppID + `"`
	if number != 0 {
		body += `,"number":` + strconv.Itoa(number)
	}
	if title != "" {
		body += `,"title":` + strconv.Quote(title)
	}
	if text != "" {
		body += `,"body":` + strconv.Quote(text)
	}
	return body + "}"
}

// issueCall sends one issue call as the daemon and reads the answer.
func (s *svc) issueCall(t *testing.T, body string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	return s.callPerson(t, issuePath, body, s.orchestratorToken(), mutate...)
}

// issueRead is one of the two reads, as the daemon makes it.
func (s *svc) issueRead(t *testing.T, action string, number int, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	return s.issueCall(t, issueBody(testRepoPath, action, number, "", ""), mutate...)
}

// issueOpen is one creation, as the daemon makes it.
func (s *svc) issueOpen(t *testing.T, title, text string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	return s.issueCall(t, issueBody(testRepoPath, creds.IssueActionOpen, 0, title, text), mutate...)
}

// issueAnswer reads a successful call's answer strictly (Convention 6: the two
// ends of this contract are this repository's code).
func issueAnswer(t *testing.T, body []byte) creds.IssueAnswer {
	t.Helper()
	var ans creds.IssueAnswer
	if err := decodeStrict(body, &ans); err != nil {
		t.Fatalf("the issue route's answer is not one this contract can read: %v (%s)", err, body)
	}
	return ans
}

// ---------------------------------------------------------------------------
// The token
// ---------------------------------------------------------------------------

// TestIssueRouteNeverReturnsAToken: the daemon names the act and the service
// performs it with a token it minted for itself, so a caller reads the issue
// and nothing else — no token, no permission set, and no App key (D2439,
// D2718).
func TestIssueRouteNeverReturnsAToken(t *testing.T) {
	s, gh := issueRouteSvc(t)

	// The listing: what the host lists, in the host's order, minus the pull
	// requests the host's issue endpoints report as issues.
	resp, body := s.issueRead(t, creds.IssueActionList, 0)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the listing was refused: HTTP %d %s", resp.StatusCode, body)
	}
	ans := issueAnswer(t, body)
	if len(ans.Issues) != 2 {
		t.Fatalf("the listing answered %d issues, want the two open ones and not the pull request the host also lists: %+v", len(ans.Issues), ans.Issues)
	}
	if got := []int{ans.Issues[0].Number, ans.Issues[1].Number}; got[0] != issueMirroredNumber || got[1] != issuePersonNumber {
		t.Errorf("the listing answers %v, want the host's own order [%d %d]", got, issueMirroredNumber, issuePersonNumber)
	}
	mirrored := ans.Issues[0]
	for _, field := range []struct{ what, got, want string }{
		{"the title", mirrored.Title, "the mirror's item"},
		{"the body", mirrored.Body, "the item's own text"},
		{"the state", mirrored.State, "open"},
		{"the url", mirrored.URL, "https://github.com/" + testRepoPath + "/issues/" + strconv.Itoa(issueMirroredNumber)},
		{"the author", mirrored.AuthorLogin, issueAppAuthor},
		{"the author id", mirrored.AuthorID, "900001"},
		{"the app slug", mirrored.AppSlug, issueAppSlug},
	} {
		if field.got != field.want {
			t.Errorf("the listing's first issue carries %s = %q, want %q: D2728's marker is read from the answer", field.what, field.got, field.want)
		}
	}
	if got := ans.Issues[1]; got.AuthorLogin != "octocat" || got.AppSlug != "" {
		t.Errorf("a person's own issue carries author %q and app slug %q, want the person's login and no slug at all", got.AuthorLogin, got.AppSlug)
	}
	if q := gh.listQ; len(q) != 1 || !strings.Contains(q[0], "state=open") {
		t.Errorf("the listing was asked for with %v, want the OPEN ones: an item is never a closed issue the sync has to guess about (D2720)", q)
	}

	// A single read: an issue by number, a closed one included, which is what
	// keeps a caller from reading "the read worked" as "the issue is still
	// open".
	resp, body = s.issueRead(t, creds.IssueActionGet, issuePersonNumber)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the issue read was refused: HTTP %d %s", resp.StatusCode, body)
	}
	read := issueAnswer(t, body)
	if len(read.Issues) != 1 || read.Issues[0].Number != issuePersonNumber || read.Issues[0].State != "closed" {
		t.Fatalf("the read answered %+v, want the one issue it named, closed as the host reports it", read.Issues)
	}
	if read.Issues[0].Body != "what a person wrote" {
		t.Errorf("the read answered the body %q, want the issue's own text: a brief is built from it", read.Issues[0].Body)
	}

	// A creation: the number GitHub answered with is what the item's external
	// id is written from (D2728), so it is on the answer.
	resp, body = s.issueOpen(t, "a mirrored item", "the item's text")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the creation was refused: HTTP %d %s", resp.StatusCode, body)
	}
	made := issueAnswer(t, body)
	if len(made.Issues) != 1 || made.Issues[0].Number != issueCreatedNumber {
		t.Fatalf("the creation answered %+v, want the issue GitHub made, number %d", made.Issues, issueCreatedNumber)
	}
	if title, text := gh.opened(t); title != "a mirrored item" || text != "the item's text" {
		t.Errorf("the creation asked GitHub to open %q / %q, want the item's own title and body", title, text)
	}

	// The answer's shape is the contract's and nothing else: one listing key,
	// and eight fields to an issue. A token, a permission set or a credential
	// name added anywhere here fails this.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("the answer is not one JSON object: %v (%s)", err, body)
	}
	if len(raw) != 1 {
		t.Errorf("the answer carries %d fields, want the one listing and nothing else: %s", len(raw), body)
	}
	if _, ok := raw["issues"]; !ok {
		t.Errorf("the answer carries no issues: %s", body)
	}
	var shaped map[string][]map[string]any
	if err := json.Unmarshal(body, &shaped); err != nil {
		t.Fatalf("the answer's issues are not objects: %v", err)
	}
	if len(shaped["issues"]) != 1 {
		t.Fatalf("the creation answered %d issues, want the one it made", len(shaped["issues"]))
	}
	if got := len(shaped["issues"][0]); got != len(issueAnswerFields) {
		t.Errorf("the created issue carries %d fields, want the contract's %d (%v): %v", got, len(issueAnswerFields), issueAnswerFields, shaped["issues"][0])
	}
	for _, field := range issueAnswerFields {
		if _, ok := shaped["issues"][0][field]; !ok {
			t.Errorf("the created issue carries no %q field: %v", field, shaped["issues"][0])
		}
	}

	// And the credential is nowhere a caller or an operator can read it. Every
	// call this route makes to GitHub carries the mint instead: the acts are
	// the SERVICE's, so a caller never holds a token to do them with.
	for _, where := range []struct{ what, text string }{
		{"the answer", string(body)},
		{"an audit line", s.audit.String()},
		{"the service's log", s.logs.String()},
	} {
		if strings.Contains(where.text, gh.token) {
			t.Errorf("%s carries the installation token", where.what)
		}
		if strings.Contains(where.text, pemCore(t)) {
			t.Errorf("%s carries the App key the token was minted with", where.what)
		}
	}
	issues := gh.issues()
	if len(issues) == 0 {
		t.Fatal("the service performed the acts without asking GitHub for anything")
	}
	for _, seen := range issues {
		if seen.auth != "Bearer "+gh.token {
			t.Errorf("a call for %s went out with %q, want the token this service minted for itself", seen.path, seen.auth)
		}
	}
	// The mint itself is signed as the App, and it is not the token: the two
	// are different credentials, and the one that reaches a repository's issues
	// is the service's own installation token.
	for _, seen := range gh.requests() {
		if seen.method == http.MethodPost && strings.HasSuffix(seen.path, "/access_tokens") {
			if seen.auth == "Bearer "+gh.token {
				t.Error("the mint was signed with the installation token it was minting")
			}
			if !strings.HasPrefix(seen.auth, "Bearer ") || seen.auth == "Bearer " {
				t.Errorf("the mint went out with %q, want the App's own JWT", seen.auth)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// The repository
// ---------------------------------------------------------------------------

// TestIssueRouteRefusesForeignRepo: the acts are this installation's, on the
// repositories its own GitHub inventory holds. A name it does not hold — and a
// name it holds on Bitbucket alone, because this route has no act that host
// could answer — is refused, and the refusal reaches no credential and no
// GitHub (D2718).
func TestIssueRouteRefusesForeignRepo(t *testing.T) {
	for _, tc := range []struct{ name, repo string }{
		{name: "a repository this installation does not hold at all", repo: "acme/nowhere"},
		{name: "a repository under another owner", repo: "someone-else/aurora"},
		{name: "a repository this installation holds only on Bitbucket", repo: testBitbucketRepo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, gh := issueRouteSvc(t)
			resp, body := s.issueCall(t, issueBody(tc.repo, creds.IssueActionList, 0, "", ""))
			if resp.StatusCode != StatusRequestRefused {
				t.Fatalf("a call for %s was answered %d, want %d: %s", tc.repo, resp.StatusCode, StatusRequestRefused, body)
			}
			if e := refusal(t, resp.StatusCode, body); e.Error != ErrorRequestRefused {
				t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorRequestRefused, e.Message)
			} else if !strings.Contains(e.Message, tc.repo) {
				t.Errorf("the refusal does not name the repository it refused: %s", e.Message)
			}
			if got := gh.count(); got != 0 {
				t.Errorf("the refused call reached GitHub %d times, want none: %v", got, gh.requests())
			}
			lines := s.auditLines(t)
			if len(lines) != 1 {
				t.Fatalf("the refused call wrote %d audit lines, want exactly one: %v", len(lines), lines)
			}
			if got := lines[0]["status"]; got != ErrorRequestRefused {
				t.Errorf("the refusal's audit line carries status %v, want %q", got, ErrorRequestRefused)
			}
			if got := lines[0]["repository"]; got != "" {
				t.Errorf("the refusal's audit line names the repository %v, and the repository is what it refused: a line must not name a repository this installation does not hold", got)
			}
		})
	}

	// The same fixture serves the repository the installation does hold: a test
	// that only refused callers would pass on a route that refused everyone.
	s, _ := issueRouteSvc(t)
	resp, body := s.issueRead(t, creds.IssueActionList, 0)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the listing of this installation's own repository was refused: HTTP %d %s", resp.StatusCode, body)
	}
	if got := len(issueAnswer(t, body).Issues); got != 2 {
		t.Errorf("the listing answered %d issues, want the two open ones", got)
	}
}

// ---------------------------------------------------------------------------
// The class
// ---------------------------------------------------------------------------

// TestIssueRouteRefusesEveryOtherClass: the orchestrator daemon is the one
// class this route admits, and the audience a token carries does not change it.
// A refusal taken before the caller is verified writes no audit line, because
// nothing is verified yet and there is no caller for a line to name (C1.13).
func TestIssueRouteRefusesEveryOtherClass(t *testing.T) {
	s, gh := issueRouteSvc(t)
	callers := []struct {
		name  string
		token func(aud string) string
	}{
		{name: "job", token: func(aud string) string { return s.jobToken(audienceClaim(aud)) }},
		{name: "review", token: func(aud string) string { return s.reviewJobToken(audienceClaim(aud)) }},
		{name: "verifier", token: func(aud string) string { return verifierToken(t, s, testVerifierSA, audienceClaim(aud)) }},
		{name: "person", token: func(aud string) string { return s.personToken(testPersonAccount, audienceClaim(aud)) }},
		{name: "coordinator", token: func(aud string) string { return s.personToken(testCoordinatorAccount, audienceClaim(aud)) }},
		{name: "launcher", token: func(aud string) string { return s.personToken(testLauncherAccount, audienceClaim(aud)) }},
	}
	for _, aud := range []struct{ name, aud string }{
		{name: "the service's own audience"},
		{name: "gcloud's default audience", aud: GcloudAudience},
	} {
		t.Run(aud.name, func(t *testing.T) {
			for _, caller := range callers {
				t.Run(caller.name, func(t *testing.T) {
					before, lines := gh.count(), len(s.auditLines(t))
					resp, body := s.issueCall(t, issueBody(testRepoPath, creds.IssueActionList, 0, "", ""), func(r *http.Request) {
						r.Header.Set("Authorization", "Bearer "+caller.token(aud.aud))
					})
					if resp.StatusCode != StatusUnverified {
						t.Fatalf("a %s caller was answered %d on the issue route, want %d: %s", caller.name, resp.StatusCode, StatusUnverified, body)
					}
					if e := refusal(t, resp.StatusCode, body); e.Error != ErrorUnverifiedJobIdentity {
						t.Errorf("the %s caller's refusal is %q, want %q: %s", caller.name, e.Error, ErrorUnverifiedJobIdentity, e.Message)
					}
					if got := len(s.auditLines(t)); got != lines {
						t.Errorf("a refused %s caller wrote %d audit lines, want none: nothing is verified yet", caller.name, got-lines)
					}
					if after := gh.count(); after != before {
						t.Errorf("a refused %s caller reached GitHub %d times, want none", caller.name, after-before)
					}
				})
			}
		})
	}

	// The daemon's own token, on the same fixture, is served: the class test
	// that only refused callers would pass on a route that refused everyone.
	resp, body := s.issueRead(t, creds.IssueActionList, 0)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the orchestrator daemon was refused on the issue route: HTTP %d %s", resp.StatusCode, body)
	}
	if got := s.auditLines(t); len(got) == 0 {
		t.Fatal("the served call wrote no audit line")
	}
}

// ---------------------------------------------------------------------------
// The text
// ---------------------------------------------------------------------------

// TestIssueRouteRefusesOversizedIssue: what a caller may put into an issue is
// bounded HERE, by this service, before the text is anywhere near GitHub — a
// title of 256 characters and a body of 8000, counted in characters and not in
// bytes, which is what makes the bound one a person's own writing can meet
// (D2718).
func TestIssueRouteRefusesOversizedIssue(t *testing.T) {
	// A title of exactly 256 characters and one of 257, and the same pair for
	// the body at 8000: the served ones are the ends the bound admits, so a
	// bound lowered by one fails here rather than passing on a shorter text.
	t.Run("the longest title the route opens", func(t *testing.T) {
		s, gh := issueRouteSvc(t)
		title := strings.Repeat("t", issueMaxTitleRunes)
		resp, body := s.issueOpen(t, title, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("a title of %d characters was refused: HTTP %d %s", issueMaxTitleRunes, resp.StatusCode, body)
		}
		if got, _ := gh.opened(t); got != title {
			t.Errorf("the creation asked GitHub for a title of %d characters, want the one the caller sent", len(got))
		}
	})
	t.Run("a title one character too long", func(t *testing.T) {
		s, gh := issueRouteSvc(t)
		resp, body := s.issueOpen(t, strings.Repeat("t", issueMaxTitleRunes+1), "")
		if resp.StatusCode != StatusRequestRefused {
			t.Fatalf("a title of %d characters was answered %d, want %d: %s", issueMaxTitleRunes+1, resp.StatusCode, StatusRequestRefused, body)
		}
		if e := refusal(t, resp.StatusCode, body); e.Error != ErrorRequestRefused {
			t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorRequestRefused, e.Message)
		}
		if got := len(gh.writes()); got != 0 {
			t.Errorf("the refused creation reached GitHub %d times, want none: the bound is checked before the act", got)
		}
		if got := len(s.auditLines(t)); got != 1 {
			t.Errorf("the refused creation wrote %d audit lines, want exactly one", got)
		}
	})

	// The count is CHARACTERS: a title of 256 multi-byte characters is 512
	// bytes and is served, which is the whole point of D2718's bound — a byte
	// count would refuse a title a person can read and GitHub accepts.
	t.Run("a title of 256 characters that is more than 256 bytes", func(t *testing.T) {
		s, gh := issueRouteSvc(t)
		title := strings.Repeat("é", issueMaxTitleRunes)
		if len(title) <= issueMaxTitleRunes {
			t.Fatalf("the title is %d bytes, and this test needs one longer than %d", len(title), issueMaxTitleRunes)
		}
		resp, body := s.issueOpen(t, title, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("a title of %d characters (%d bytes) was refused: HTTP %d %s", issueMaxTitleRunes, len(title), resp.StatusCode, body)
		}
		if got, _ := gh.opened(t); got != title {
			t.Errorf("the creation asked GitHub for %q, want the title the caller sent", got)
		}
	})

	t.Run("the longest body the route opens", func(t *testing.T) {
		s, gh := issueRouteSvc(t)
		text := strings.Repeat("b", issueMaxBodyRunes)
		resp, body := s.issueOpen(t, "a mirrored item", text)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("a body of %d characters was refused: HTTP %d %s", issueMaxBodyRunes, resp.StatusCode, body)
		}
		if _, got := gh.opened(t); got != text {
			t.Errorf("the creation asked GitHub for a body of %d characters, want the one the caller sent", len(got))
		}
	})
	t.Run("a body one character too long", func(t *testing.T) {
		s, gh := issueRouteSvc(t)
		resp, body := s.issueOpen(t, "a mirrored item", strings.Repeat("b", issueMaxBodyRunes+1))
		if resp.StatusCode != StatusRequestRefused {
			t.Fatalf("a body of %d characters was answered %d, want %d: %s", issueMaxBodyRunes+1, resp.StatusCode, StatusRequestRefused, body)
		}
		if got := len(gh.writes()); got != 0 {
			t.Errorf("the refused creation reached GitHub %d times, want none", got)
		}
	})

	// A body is not required — an item with nothing to say is a legitimate
	// issue — and a title is: GitHub refuses an issue with none, so a route
	// that sent one would spend a creation to learn what its own check knows.
	t.Run("a title the route cannot open an issue with", func(t *testing.T) {
		s, gh := issueRouteSvc(t)
		resp, body := s.issueOpen(t, "", "the item's text")
		if resp.StatusCode != StatusRequestRefused {
			t.Fatalf("a creation naming no title was answered %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
		}
		if got := len(gh.writes()); got != 0 {
			t.Errorf("a creation naming no title reached GitHub %d times, want none", got)
		}
	})
	t.Run("an item with nothing to say", func(t *testing.T) {
		s, gh := issueRouteSvc(t)
		resp, body := s.issueOpen(t, "an item with no text", "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("a creation with an empty body was refused: HTTP %d %s", resp.StatusCode, body)
		}
		if _, got := gh.opened(t); got != "" {
			t.Errorf("the creation asked GitHub for the body %q, want none", got)
		}
	})
}

// ---------------------------------------------------------------------------
// The record
// ---------------------------------------------------------------------------

// TestIssueRouteAuditsEveryCall: C1.13's line, once per call that reaches a
// verified caller — served or refused, and whoever refused it. The line names
// the daemon, the repository the act was for, the App key the act was performed
// with and the act, and it names no run, no job and no tier: the daemon holds
// no run identity and is not a job of this installation.
func TestIssueRouteAuditsEveryCall(t *testing.T) {
	// The line names the repository the way the route resolved it and every
	// line of this service does: the storage slug, which is what a run token
	// and an outcome node are keyed by, and not the owner/name the caller sent.
	routeSlug := githubSlug(t, testRepoPath)
	var auditFailed bool
	for _, tc := range []struct {
		name string
		// mutate wires the service this case needs, where it needs one wired
		// differently: a stopped service, a bundle with no App key, a GitHub
		// that refuses an act.
		mutate func(t *testing.T, s *svc, cfg *Config, gh *issueFake)
		// after runs once the fixture is standing, for the one case that spends
		// the route's own rate: the server is built after mutate, so a case
		// that reaches it does so here.
		after func(t *testing.T, s *svc)
		call  func(t *testing.T, s *svc) (*http.Response, []byte)
		// code is the HTTP answer, and status the word the line carries: the
		// refusal's own class, or "ok" for an act that was performed.
		code   int
		status string
		// repo is the repository the line names, empty when the call was
		// refused before one was resolved.
		repo string
		// reaches is true where the call reaches GitHub: the acts that were
		// performed, and the refusals whose cause is GitHub's answer or the
		// mint's — the only refusals allowed to have got that far. Every other
		// refusal is taken before any credential is read and before the act.
		reaches bool
	}{
		{
			name: "a listing that was served",
			call: func(t *testing.T, s *svc) (*http.Response, []byte) { return s.issueRead(t, creds.IssueActionList, 0) },
			code: http.StatusOK, status: StatusOK, repo: routeSlug, reaches: true,
		},
		{
			name: "a read that was served",
			call: func(t *testing.T, s *svc) (*http.Response, []byte) {
				return s.issueRead(t, creds.IssueActionGet, issuePersonNumber)
			},
			code: http.StatusOK, status: StatusOK, repo: routeSlug, reaches: true,
		},
		{
			name: "a creation that was served",
			call: func(t *testing.T, s *svc) (*http.Response, []byte) {
				return s.issueOpen(t, "a mirrored item", "the item's text")
			},
			code: http.StatusOK, status: StatusOK, repo: routeSlug, reaches: true,
		},
		{
			name: "a body that is not one JSON object of this route's fields",
			call: func(t *testing.T, s *svc) (*http.Response, []byte) { return s.issueCall(t, `[]`) },
			code: StatusRequestRefused, status: ErrorRequestRefused,
		},
		{
			name: "a call naming no act",
			call: func(t *testing.T, s *svc) (*http.Response, []byte) {
				return s.issueCall(t, `{"repository":`+strconv.Quote(testRepoPath)+`,"app_id":"`+testAppID+`"}`)
			},
			code: StatusRequestRefused, status: ErrorRequestRefused,
		},
		{
			name: "a call naming an act this route does not perform",
			call: func(t *testing.T, s *svc) (*http.Response, []byte) {
				// There is no close act (D2727): closing is GitHub's own work
				// on the `Fixes #N` a merged run's pull request carries.
				return s.issueCall(t, issueBody(testRepoPath, "close", issuePersonNumber, "", ""))
			},
			code: StatusRequestRefused, status: ErrorRequestRefused,
		},
		{
			name: "a call naming no repository",
			call: func(t *testing.T, s *svc) (*http.Response, []byte) {
				return s.issueCall(t, issueBody("aurora", creds.IssueActionList, 0, "", ""))
			},
			code: StatusRequestRefused, status: ErrorRequestRefused,
		},
		{
			name: "a call naming a repository this installation does not hold",
			call: func(t *testing.T, s *svc) (*http.Response, []byte) {
				return s.issueCall(t, issueBody("acme/nowhere", creds.IssueActionList, 0, "", ""))
			},
			code: StatusRequestRefused, status: ErrorRequestRefused,
		},
		{
			name: "a call naming no App ID",
			call: func(t *testing.T, s *svc) (*http.Response, []byte) {
				return s.issueCall(t, `{"repository":`+strconv.Quote(testRepoPath)+`,"action":"`+creds.IssueActionList+`"}`)
			},
			code: StatusRequestRefused, status: ErrorRequestRefused,
		},
		{
			name: "a call whose App ID is not one",
			call: func(t *testing.T, s *svc) (*http.Response, []byte) {
				return s.issueCall(t, `{"repository":`+strconv.Quote(testRepoPath)+`,"action":"`+creds.IssueActionList+`","app_id":"not-a-number"}`)
			},
			code: StatusRequestRefused, status: ErrorRequestRefused,
		},
		{
			name: "a creation with a title the route does not open",
			call: func(t *testing.T, s *svc) (*http.Response, []byte) {
				return s.issueOpen(t, strings.Repeat("t", issueMaxTitleRunes+1), "")
			},
			code: StatusRequestRefused, status: ErrorRequestRefused,
		},
		{
			name: "the daemon presenting a run identity",
			call: func(t *testing.T, s *svc) (*http.Response, []byte) {
				return s.issueRead(t, creds.IssueActionList, 0, func(r *http.Request) {
					r.Header.Set(HeaderRunIdentity, "a-run-identity-token")
				})
			},
			code: StatusUnverified, status: ErrorUnverifiedRunIdentity,
		},
		{
			// The other half of the same refusal: a daemon names no job
			// either, and one that named only this is turned away under the
			// same name, with the job sentence rather than the run one.
			name: "the daemon naming a run job",
			call: func(t *testing.T, s *svc) (*http.Response, []byte) {
				return s.issueRead(t, creds.IssueActionList, 0, func(r *http.Request) {
					r.Header.Set(HeaderRunJob, "fugaro-a-job")
				})
			},
			code: StatusUnverified, status: ErrorUnverifiedRunIdentity,
		},
		{
			name:   "the operator's stop switch",
			mutate: func(_ *testing.T, s *svc, _ *Config, _ *issueFake) { s.kills.global = true },
			call:   func(t *testing.T, s *svc) (*http.Response, []byte) { return s.issueRead(t, creds.IssueActionList, 0) },
			code:   StatusUnavailable, status: ErrorStateUnavailable,
		},
		{
			name: "a bundle holding no App key to sign a token with",
			mutate: func(t *testing.T, s *svc, _ *Config, _ *issueFake) {
				s.src.docs = [][]byte{testBundleDoc(t)}
			},
			call: func(t *testing.T, s *svc) (*http.Response, []byte) { return s.issueRead(t, creds.IssueActionList, 0) },
			code: StatusUnavailable, status: ErrorStateUnavailable, repo: routeSlug,
		},
		{
			name:   "a mint GitHub refuses",
			mutate: func(_ *testing.T, _ *svc, _ *Config, gh *issueFake) { gh.mintStatus = http.StatusUnauthorized },
			call:   func(t *testing.T, s *svc) (*http.Response, []byte) { return s.issueRead(t, creds.IssueActionList, 0) },
			code:   StatusUnavailable, status: ErrorStateUnavailable, repo: routeSlug, reaches: true,
		},
		{
			name:   "a listing GitHub refuses",
			mutate: func(_ *testing.T, _ *svc, _ *Config, gh *issueFake) { gh.listStatus = http.StatusInternalServerError },
			call:   func(t *testing.T, s *svc) (*http.Response, []byte) { return s.issueRead(t, creds.IssueActionList, 0) },
			code:   StatusUnavailable, status: ErrorStateUnavailable, repo: routeSlug, reaches: true,
		},
		{
			name:   "a creation GitHub refuses",
			mutate: func(_ *testing.T, _ *svc, _ *Config, gh *issueFake) { gh.createStatus = http.StatusUnprocessableEntity },
			call:   func(t *testing.T, s *svc) (*http.Response, []byte) { return s.issueOpen(t, "a mirrored item", "") },
			code:   StatusUnavailable, status: ErrorStateUnavailable, repo: routeSlug, reaches: true,
		},
		{
			name: "a repository over its read rate",
			after: func(t *testing.T, s *svc) {
				// The window is spent here rather than through 240 calls: what
				// this case reads is the LINE a rate-limited call writes, and
				// the rate test below is where the window is spent through the
				// route, so this spends the same counter (this route's own
				// reads) without the 240 round trips.
				w := s.server.issues.reads
				slug := githubSlug(t, testRepoPath)
				for i := 0; i < issueReadsPerRepo; i++ {
					if !w.allow(slug) {
						t.Fatalf("the route's own window refused read %d of the %d this rate allows", i+1, issueReadsPerRepo)
					}
				}
			},
			call: func(t *testing.T, s *svc) (*http.Response, []byte) { return s.issueRead(t, creds.IssueActionList, 0) },
			code: StatusUnavailable, status: ErrorStateUnavailable, repo: routeSlug,
		},
		{
			name: "an act the auditor could not record",
			mutate: func(t *testing.T, s *svc, cfg *Config, _ *issueFake) {
				cfg.AuditLog = &failFirstHandler{inner: slog.NewJSONHandler(s.audit, nil), failed: &auditFailed}
			},
			call: func(t *testing.T, s *svc) (*http.Response, []byte) { return s.issueRead(t, creds.IssueActionList, 0) },
			code: StatusUnavailable, status: ErrorStateUnavailable, repo: routeSlug, reaches: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, gh := issueRouteSvcWith(t, tc.mutate)
			if tc.after != nil {
				tc.after(t, s)
			}
			before := gh.count()
			resp, body := tc.call(t, s)
			if resp.StatusCode != tc.code {
				t.Fatalf("the call was answered %d, want %d: %s", resp.StatusCode, tc.code, body)
			}
			if tc.code != http.StatusOK {
				if e := refusal(t, resp.StatusCode, body); e.Error != tc.status {
					t.Errorf("the refusal is %q, want %q: %s", e.Error, tc.status, e.Message)
				}
			}
			// A refusal taken before the act reaches GitHub not once: the rate,
			// the bundle, the switch and every check on the call itself are
			// answered by this service alone.
			if tc.reaches && gh.count() == before {
				t.Errorf("the call reached GitHub not once, and this case performs an act or is refused BY it: %v", gh.requests())
			}
			if !tc.reaches && gh.count() != before {
				t.Errorf("the refused call reached GitHub %d times, want none: %v", gh.count()-before, gh.requests())
			}
			lines := s.auditLines(t)
			if len(lines) != 1 {
				t.Fatalf("the call wrote %d audit lines, want exactly one, served or refused: %v", len(lines), lines)
			}
			line := lines[0]
			checkAuditShape(t, line)
			for field, want := range map[string]string{
				"caller":     testOrchestratorSA,
				"credential": config.GitHubAppKeySecret,
				"purpose":    PurposeIssue,
				"status":     tc.status,
				"repository": tc.repo,
				"run":        "",
				"job":        "",
				"tier":       "",
			} {
				if got := line[field]; got != want {
					t.Errorf("the issue route's line: %s = %v, want %q", field, got, want)
				}
			}
			if strings.Contains(s.audit.String(), gh.token) {
				t.Error("an audit line carries the installation token")
			}
		})
	}

	// A caller the class gate refused writes no line at all: nothing was
	// verified, so there is no caller for a line to name. That refusal is
	// TestIssueRouteRefusesEveryOtherClass's, and this is the other half of it.
	s, _ := issueRouteSvc(t)
	resp, body := s.issueCall(t, issueBody(testRepoPath, creds.IssueActionList, 0, "", ""), func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+s.jobToken())
	})
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the issue route admitted a job's token: HTTP %d %s", resp.StatusCode, body)
	}
	if got := len(s.auditLines(t)); got != 0 {
		t.Errorf("a refusal taken before the caller was verified wrote %d audit lines, want none", got)
	}
}

// ---------------------------------------------------------------------------
// The reach
// ---------------------------------------------------------------------------

// TestIssueRouteMintsIssuesScopeOnly: the token this route mints for its own
// acts carries exactly `{issues: write, metadata: read}` — D2718's whole answer
// — and it is cached in a cache of this route's own, because a token minted for
// the label route's `pull_requests: write` cannot touch an issue at all (F39).
func TestIssueRouteMintsIssuesScopeOnly(t *testing.T) {
	s, gh := issueRouteSvc(t)

	// Three acts: two reads and a creation.
	for _, call := range []func(t *testing.T, s *svc) (*http.Response, []byte){
		func(t *testing.T, s *svc) (*http.Response, []byte) { return s.issueRead(t, creds.IssueActionList, 0) },
		func(t *testing.T, s *svc) (*http.Response, []byte) {
			return s.issueRead(t, creds.IssueActionGet, issueMirroredNumber)
		},
		func(t *testing.T, s *svc) (*http.Response, []byte) { return s.issueOpen(t, "a mirrored item", "") },
	} {
		resp, body := call(t, s)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("an issue act was refused: HTTP %d %s", resp.StatusCode, body)
		}
		if strings.Contains(string(body), gh.token) {
			t.Error("the answer carries the installation token")
		}
	}

	mints := gh.mints(t)
	if len(mints) != 1 {
		t.Fatalf("three issue calls minted %d installation tokens, want one: the token is cached until near expiry", len(mints))
	}
	perms, ok := mints[0]["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("the mint's body carries no permission set: %v", mints[0])
	}
	want := map[string]string{"issues": "write", "metadata": "read"}
	if len(perms) != len(want) {
		t.Errorf("the mint asks for %v, want exactly %v", perms, want)
	}
	for noun, level := range want {
		if got, _ := perms[noun].(string); got != level {
			t.Errorf("the mint asks for %s: %v, want %q", noun, perms[noun], level)
		}
	}
	// The label route's set is what this route must NOT mint: it cannot touch
	// an issue at all, and a token carrying it would be spent for a refusal
	// rather than for an act (D2718, F39).
	for _, noun := range []string{"pull_requests", "contents", "administration"} {
		if _, ok := perms[noun]; ok {
			t.Errorf("the mint asks for %s, which no act of this route needs", noun)
		}
	}
	label := labelTokenPermissions()
	same := len(perms) == len(label)
	for noun, level := range label {
		if got, _ := perms[noun].(string); got != level {
			same = false
		}
	}
	if same {
		t.Errorf("this route mints the label route's own set (%v), and a `pull_requests: write` token cannot touch an issue at all (F39)", label)
	}

	// The cache is this route's own. The label route's is untouched by every
	// act above, and the reverse holds too: one cache keyed by the repository
	// and the App alone would serve one route's act with the other's token.
	if held := s.server.labels.tokens.held; len(held) != 0 {
		t.Errorf("the issue route minted into the label route's cache: %v", held)
	}
	if held := s.server.issues.tokens.held; len(held) != 1 {
		t.Errorf("the issue route's own cache holds %d tokens, want the one it minted", len(held))
	}

	// The clock moves to where the cached token is inside the client's validity
	// margin: the next call mints again. The caller's own ID token is minted
	// before the clock moves, because it has a life too.
	tok := s.orchestratorToken(func(c map[string]any) { c["exp"] = s.now.Add(4 * time.Hour).Unix() })
	s.fixture.now = s.now.Add(59 * time.Minute)
	gh.expires = s.now.Add(time.Hour)
	resp, body := s.callPerson(t, issuePath, issueBody(testRepoPath, creds.IssueActionList, 0, "", ""), tok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the listing after the clock moved was refused: HTTP %d %s", resp.StatusCode, body)
	}
	if got := len(gh.mints(t)); got != 2 {
		t.Errorf("the route minted %d tokens, want a second one once the cached one is near its expiry", got)
	}
}

// ---------------------------------------------------------------------------
// The rate
// ---------------------------------------------------------------------------

// TestIssueRouteRateLimitedPerRepo: 240 reads and 60 creations an hour for ONE
// repository, counted apart — the numbers the label route holds, because the
// two routes are asked by the same daemon on the same tick (F22).
func TestIssueRouteRateLimitedPerRepo(t *testing.T) {
	if issueWritesPerRepo != labelWritesPerRepo || issueReadsPerRepo != labelReadsPerRepo {
		t.Errorf("the issue route's rates are %d writes and %d reads an hour, want the label route's %d and %d: the two routes are asked on one tick",
			issueWritesPerRepo, issueReadsPerRepo, labelWritesPerRepo, labelReadsPerRepo)
	}
	if ticks := int(time.Hour / labelTick); issueReadsPerRepo < 2*ticks {
		t.Errorf("the read limit is %d an hour, and %d ticks an hour read twice each", issueReadsPerRepo, ticks)
	}
	if ticks := int(time.Hour / labelTick); issueWritesPerRepo < ticks/2 {
		t.Errorf("the write limit is %d an hour, and %d ticks an hour write on every other tick", issueWritesPerRepo, ticks)
	}

	s, gh := issueRouteSvc(t)
	read := func(t *testing.T, repo string) (*http.Response, []byte) {
		t.Helper()
		return s.issueCall(t, issueBody(repo, creds.IssueActionList, 0, "", ""))
	}
	write := func(t *testing.T) (*http.Response, []byte) {
		t.Helper()
		return s.issueOpen(t, "a mirrored item", "")
	}

	// The read budget, spent with a creation in the middle of it: the creation
	// is served, so the two are counted apart.
	for i := 0; i < issueReadsPerRepo; i++ {
		resp, body := read(t, testRepoPath)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the route refused read %d of %d in the window: HTTP %d %s", i+1, issueReadsPerRepo, resp.StatusCode, body)
		}
		if i == issueReadsPerRepo/2 {
			if resp, body := write(t); resp.StatusCode != http.StatusOK {
				t.Fatalf("a creation in the middle of the read budget was refused: HTTP %d %s", resp.StatusCode, body)
			}
		}
	}
	before := gh.count()
	resp, body := read(t, testRepoPath)
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the route served a %dth read for one repository in an hour: HTTP %d %s", issueReadsPerRepo+1, resp.StatusCode, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorStateUnavailable {
		t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorStateUnavailable, e.Message)
	}
	if after := gh.count(); after != before {
		t.Errorf("the rate-limited read reached GitHub %d times, want none: the rate is checked before the credential is read", after-before)
	}
	if got := len(s.auditLines(t)); got != issueReadsPerRepo+2 {
		t.Errorf("the calls wrote %d audit lines, want one for each of the %d reads and the creation: a refused call is recorded too",
			got, issueReadsPerRepo+1)
	}

	// The other repository of the same installation is served: the rate is per
	// repository, and one repository's spent budget is not another's.
	resp, body = read(t, testOtherRepoPath)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a read of another repository of the same installation was refused: HTTP %d %s", resp.StatusCode, body)
	}

	// The write budget, spent while the reads are exhausted: the reads being
	// over their rate does not refuse a creation.
	for i := 0; i < issueWritesPerRepo-1; i++ {
		resp, body := write(t)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the route refused creation %d of %d in the window: HTTP %d %s", i+2, issueWritesPerRepo, resp.StatusCode, body)
		}
	}
	resp, body = write(t)
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the route served a %dth creation for one repository in an hour: HTTP %d %s", issueWritesPerRepo+1, resp.StatusCode, body)
	}

	// The window passes: both are served again. The caller's own ID token is
	// minted before the clock moves, because it has a life too, and so is the
	// token the fake GitHub hands out — a token GitHub mints at the new instant
	// is one this service reads as live.
	tok := s.orchestratorToken(func(c map[string]any) { c["exp"] = s.now.Add(4 * time.Hour).Unix() })
	s.fixture.now = s.now.Add(labelRateWindow + time.Minute)
	gh.expires = s.now.Add(time.Hour)
	resp, body = s.callPerson(t, issuePath, issueBody(testRepoPath, creds.IssueActionList, 0, "", ""), tok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the route refused a read made after the window passed: HTTP %d %s", resp.StatusCode, body)
	}
	resp, body = s.callPerson(t, issuePath, issueBody(testRepoPath, creds.IssueActionOpen, 0, "a mirrored item", ""), tok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the route refused a creation made after the window passed: HTTP %d %s", resp.StatusCode, body)
	}
}

// ---------------------------------------------------------------------------
// The path
// ---------------------------------------------------------------------------

// TestIssueRoutePathMatchesClient: the path is spelled twice — once by the
// service (IssuePath) and once by the contract the daemon is built on
// (creds.IssuePath), because credsvc imports creds and not the other way round
// — and the two spellings are held to each other here. The route's act is its
// own purpose, so a line can tell an issue act from every other route's, and a
// path this service does not serve is a 404 that names what it does serve.
func TestIssueRoutePathMatchesClient(t *testing.T) {
	if IssuePath != issuePath {
		t.Errorf("the issue route's path = %q, want %q", IssuePath, issuePath)
	}
	if creds.IssuePath != IssuePath {
		t.Errorf("the client's issue path = %q and the service's = %q, want one path", creds.IssuePath, IssuePath)
	}
	purpose, credential := routeAct(IssuePath)
	if purpose != PurposeIssue || credential != config.GitHubAppKeySecret {
		t.Errorf("routeAct(%s) = (%q, %q), want (%q, %q)", IssuePath, purpose, credential, PurposeIssue, config.GitHubAppKeySecret)
	}
	for _, other := range []string{MintPath, BitbucketReadPath, GitHubReadPath, MergeMintPath, LabelPath} {
		if other, _ := routeAct(other); other == purpose {
			t.Errorf("the issue route shares the purpose %q with %s, so a line cannot tell the acts apart", purpose, other)
		}
	}

	s, _ := issueRouteSvc(t)
	resp, body := s.postRaw(t, http.MethodPost, "/v1/github/issue")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an unknown path answered %d, want 404: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), IssuePath) {
		t.Errorf("the not-found answer does not name %s among the routes this service serves: %s", IssuePath, body)
	}
}
