package credsvc

// The label route's own tests (labelroute.go, T3, D2439, F17, F18, F26). They
// are the merge mint's tests with the act changed, and the properties they exist
// to hold are the ones the route's design turns on:
//
//   - the names: only `fugaro:running`, `fugaro:failed` and a `spec:` name of
//     R3's shape are accepted, and the ASCII check happens BEFORE the lowering
//     (TestLabelRouteRefusesForeignNames, F26, R3);
//   - the removals: `remove` takes the two status names alone, so a stolen
//     daemon identity cannot take a spec label a person added off a pull
//     request (TestLabelRouteRemovesOnlyStatusNames, F23, F26);
//   - the text: the colour and the description are D2437's constants and the
//     caller cannot name them at all (TestLabelRouteEnsureUsesFixedText);
//   - the corroboration: a write lands only on the pull request whose head
//     branch is exactly `fugaro/<run id>`, whose head repository is the base
//     repository and whose author is Fugaro's own identity
//     (TestLabelRouteCorroboratesPullRequest, F17, O3);
//   - the class: the orchestrator daemon and nobody else, whatever audience the
//     token carries (TestLabelRouteRefusesEveryOtherClass);
//   - the reach: the token the route mints carries exactly
//     `{pull_requests: write, metadata: read}`, is cached until near expiry and
//     never reaches a caller (TestLabelRoutePermissionSet, U2, D2439);
//   - the bound: 60 writes and 240 reads an hour per repository, counted apart,
//     which is what a tick every 30 seconds needs
//     (TestLabelRouteRateSizedForTick, F22);
//   - the path: one spelling, shared with the client that will call it
//     (TestLabelRoutePathMatchesClient).
//
// Every one of them is a mutation proof and is written as such: admitting
// another class, widening the permission set, dropping the name check or
// skipping the head check turns one of them red, and the fake GitHub below
// records the requests the service actually made, so "the service reached
// GitHub zero times" is a claim a test reads rather than assumes.
//
// Everything here is hermetic (Convention 9): the GitHub API is a fake on
// loopback reached through the transport that refuses every other destination
// (ghTransport), and the App key and every token are built at run time
// (Convention 2).

import (
	"encoding/json"
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
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
)

// labelPath is the label route's path, spelled as the contract spells it. It is
// a literal on purpose: the tests below hold the constant to it, so a rename of
// either cannot quietly move the route.
const labelPath = "/v1/github/labels"

// testAppSlug is the App's own slug, which is how the fake GitHub spells
// Fugaro's identity: REST spells a bot account `<slug>[bot]` (github's own
// botSuffix), and a pull request whose author is that login and whose head is
// not a fork is the one PRHead.Self is true for.
const testAppSlug = "fugaro-aurora"

// labelAppAuthor is that identity as a pull request's author.
const labelAppAuthor = testAppSlug + "[bot]"

// The three labels of D2437, spelled as the wire carries them. They are
// literals here and not the service's constants, so a colour or a sentence
// changed in the code is a test failure and not a test that follows it.
const (
	wantRunningName = "fugaro:running"
	wantFailedName  = "fugaro:failed"
	wantSpecName    = "spec:forge-labels"
)

// ---------------------------------------------------------------------------
// The fake GitHub
// ---------------------------------------------------------------------------

// labelPR is one pull request the fake answers for: what the open listing
// carries, and what the two reads of a write carry.
type labelPR struct {
	number int
	state  string // "open" or "closed"
	merged bool
	labels []string
	head   string // head.ref: the branch the pull request is on
	repo   string // head.repo.full_name: the repository it is from
	author string // user.login
}

// labelFake stands in for api.github.com for the label route: the App's own
// identity, the installation and its token, the open pull request listing, one
// pull request by number, and the three label endpoints. It records every
// request, so a test can read what the service really asked for — and the
// transport that reaches it refuses every other host.
type labelFake struct {
	t   *testing.T
	srv *httptest.Server

	mu   sync.Mutex
	seen []ghSeen

	// slug is what GET /app answers: the App's own identity.
	slug string
	// token and expires are what the installation mint answers with.
	token   string
	expires time.Time
	// open is what the open listing answers, in order.
	open []labelPR
	// pull is what a pull request read answers: the head branch, the head
	// repository and the author the write is corroborated against.
	pull labelPR
	// pullStatus and mintStatus, when not 2xx, are what those calls answer with
	// instead: the pull request read failing, and the mint failing.
	pullStatus, mintStatus int
	// labels are the label definitions the fake holds, by name: what an ensure
	// of a name already there answers with. It is a set of names and not the
	// text, because the text a second creation carries is what the route must
	// NOT converge (D2440).
	known map[string]bool
}

func newLabelFake(t *testing.T) *labelFake {
	t.Helper()
	f := &labelFake{
		t:       t,
		slug:    testAppSlug,
		token:   fakeCred(t, "label-token"),
		expires: ghExpiry,
		known:   map[string]bool{},
	}
	f.pull = labelPR{
		number: 7, state: "open", head: "fugaro/run", repo: testRepoPath,
		author: labelAppAuthor, labels: []string{wantRunningName},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// repoPath is one path under the repository the fixture's inventory holds.
func (f *labelFake) repoPath(suffix string) string { return "/repos/" + testRepoPath + suffix }

func (f *labelFake) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		f.t.Errorf("reading the request body: %v", err)
	}
	f.mu.Lock()
	f.seen = append(f.seen, ghSeen{method: r.Method, path: r.URL.Path, body: string(body), auth: r.Header.Get("Authorization")})
	slug, token, expires := f.slug, f.token, f.expires
	open, pull := f.open, f.pull
	pullStatus, mintStatus := f.pullStatus, f.mintStatus
	known := map[string]bool{}
	for name, there := range f.known {
		known[name] = there
	}
	f.mu.Unlock()

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/app":
		f.write(w, http.StatusOK, map[string]any{"slug": slug, "id": 99})
	case r.Method == http.MethodGet && r.URL.Path == f.repoPath("/installation"):
		f.write(w, http.StatusOK, map[string]any{"id": 777})
	case r.Method == http.MethodPost && r.URL.Path == "/app/installations/777/access_tokens":
		if mintStatus != 0 && mintStatus/100 != 2 {
			w.WriteHeader(mintStatus)
			return
		}
		f.write(w, http.StatusCreated, map[string]any{
			"token":      token,
			"expires_at": expires.UTC().Format(time.RFC3339),
		})
	case r.Method == http.MethodGet && r.URL.Path == f.repoPath("/pulls"):
		out := make([]map[string]any, 0, len(open))
		for _, pr := range open {
			out = append(out, map[string]any{"number": pr.number, "labels": labelNames(pr.labels)})
		}
		f.write(w, http.StatusOK, out)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, f.repoPath("/pulls/")):
		if pullStatus != 0 && pullStatus/100 != 2 {
			w.WriteHeader(pullStatus)
			return
		}
		number, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, f.repoPath("/pulls/")))
		if err != nil {
			f.t.Errorf("the fake was asked for pull request %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		f.write(w, http.StatusOK, map[string]any{
			"number": number, "state": pull.state, "merged": pull.merged,
			"title": "a pull request", "html_url": "https://github.com/" + testRepoPath + "/pull/" + strconv.Itoa(number),
			"user":               map[string]any{"login": pull.author, "id": 4242},
			"head":               map[string]any{"ref": pull.head, "sha": strings.Repeat("a", 40), "repo": map[string]any{"full_name": pull.repo}},
			"base":               map[string]any{"ref": "main"},
			"labels":             labelNames(pull.labels),
			"author_association": "NONE",
		})
	case r.Method == http.MethodPost && r.URL.Path == f.repoPath("/labels"):
		var sent struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(body, &sent)
		if known[sent.Name] {
			// GitHub's own answer to the second creation of a label.
			f.write(w, http.StatusUnprocessableEntity, map[string]any{
				"message": "Validation Failed",
				"errors":  []map[string]any{{"resource": "Label", "code": "already_exists", "field": "name"}},
			})
			return
		}
		f.mu.Lock()
		f.known[sent.Name] = true
		f.mu.Unlock()
		f.write(w, http.StatusCreated, map[string]any{"name": sent.Name})
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, f.repoPath("/issues/")):
		f.write(w, http.StatusOK, labelNames(nil))
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, f.repoPath("/issues/")):
		f.write(w, http.StatusOK, labelNames(nil))
	default:
		f.t.Errorf("the fake GitHub was asked for %s %s, and the label route reads the App, the installation, the pull requests and their labels and writes a label definition and a pull request's labels and nothing else", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

// labelNames is a set of label names as the host spells it on the wire.
func labelNames(names []string) []map[string]any {
	out := make([]map[string]any, 0, len(names))
	for _, name := range names {
		out = append(out, map[string]any{"name": name})
	}
	return out
}

func (f *labelFake) write(w http.ResponseWriter, status int, body any) {
	raw, err := json.Marshal(body)
	if err != nil {
		f.t.Errorf("encoding the fake's answer: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

// requests is what the fake was asked, in order.
func (f *labelFake) requests() []ghSeen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ghSeen(nil), f.seen...)
}

// count is how many requests the fake saw.
func (f *labelFake) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.seen)
}

// writes is the label requests the fake saw: the creations and the additions and
// removals on a pull request, and nothing else it was asked for.
func (f *labelFake) writes() []ghSeen {
	var out []ghSeen
	for _, seen := range f.requests() {
		switch {
		case seen.method == http.MethodPost && strings.HasSuffix(seen.path, "/labels"):
			out = append(out, seen)
		case seen.method == http.MethodDelete:
			out = append(out, seen)
		}
	}
	return out
}

// mints is the body of every installation token the fake minted, in order.
func (f *labelFake) mints(t *testing.T) []map[string]any {
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

// labelRouteSvc is the whole fixture for a label route test: a service whose bundle
// holds the App key, whose inventory holds the GitHub repository, whose classes
// are all six of the caller-class matrix AND the orchestrator daemon, and the
// fake GitHub behind it.
//
// The six are configured and not omitted because the route's whole admission is
// its class: a fixture that held only the daemon's own account could not tell
// "refused by the class" from "refused because nobody was configured", which is
// the same reason the matrix configures the launcher (DS1.2).
func labelRouteSvc(t *testing.T) (*svc, *labelFake) {
	t.Helper()
	return labelRouteSvcWith(t, nil)
}

// labelRouteSvcWith is labelRouteSvc with the service's own configuration under the
// test's control, for the cases that need one wired differently: an audit log
// that cannot write, an inventory holding no repository.
func labelRouteSvcWith(t *testing.T, mutate func(*svc, *Config)) (*svc, *labelFake) {
	t.Helper()
	gh := newLabelFake(t)
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
			t.Fatalf("the label route's classes cannot be configured together: %v", err)
		}
		cfg.Auth, s.auth = auth, auth
		cfg.ReviewAccounts = []ReviewJobAccount{{Email: testReviewSA, Account: testReviewAccount}}
		cfg.Repos = map[string]string{githubSlug(t, testRepoPath): testRepo}
		cfg.Client = &http.Client{Transport: ghTransport{srv: gh.srv}}
		s.src.docs = [][]byte{mintBundleDoc(t)}
		if mutate != nil {
			mutate(s, cfg)
		}
	})
	// The fake's pull request is the one this fixture's run opened, which is
	// what makes a plain write of the fixture correborated: the run id the call
	// carries and the branch the fake reports are one run's.
	gh.pull.head = labelRunBranch(s.run)
	gh.pull.repo = testRepoPath
	gh.pull.author = labelAppAuthor
	return s, gh
}

// labelRunBranch is the branch a run's pull request is opened from, spelled the
// way the runner spells it (gitops.RunBranchPrefix), so that the fixture states
// the same fact the route checks.
func labelRunBranch(runID string) string { return "fugaro/" + runID }

// labelBody is one label call's body as the wire carries it: the fields the
// daemon writes, spelled here rather than marshalled from the service's own
// struct, so a rename on either side is a test failure.
func labelBody(repo, action, name string, number int, runID string) string {
	body := `{"repository":` + strconv.Quote(repo) + `,"action":"` + action + `","app_id":"` + testAppID + `"`
	if number != 0 {
		body += `,"number":` + strconv.Itoa(number)
	}
	if name != "" {
		body += `,"name":` + strconv.Quote(name)
	}
	if runID != "" {
		body += `,"run_id":"` + runID + `"`
	}
	return body + "}"
}

// labelCall sends one label call as the daemon and reads the answer.
func (s *svc) labelCall(t *testing.T, body string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	return s.callPerson(t, LabelPath, body, s.orchestratorToken(), mutate...)
}

// labelEnsure is one ensure of name, which is the call whose name check is what
// most of these tests are about.
func (s *svc) labelEnsure(t *testing.T, name string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	return s.labelCall(t, labelBody(testRepoPath, creds.LabelActionEnsure, name, 0, ""), mutate...)
}

// labelWrite is one add or remove of name on the fixture's pull request, as the
// run that opened it.
func (s *svc) labelWrite(t *testing.T, action, name string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	return s.labelCall(t, labelBody(testRepoPath, action, name, 7, s.run), mutate...)
}

// labelAnswer reads a successful call's answer strictly (Convention 6: the two
// ends of this contract are this repository's code).
func labelAnswer(t *testing.T, body []byte) creds.LabelAnswer {
	t.Helper()
	var ans creds.LabelAnswer
	if err := decodeStrict(body, &ans); err != nil {
		t.Fatalf("the label route's answer is not one this contract can read: %v (%s)", err, body)
	}
	return ans
}

// ---------------------------------------------------------------------------
// The names
// ---------------------------------------------------------------------------

// TestLabelRouteRefusesForeignNames is F26's and R3's red step: the route
// performs an act on the three names Fugaro owns and on nothing else, and the
// ASCII check happens before the lowering.
//
// The table's last four rows are what makes this more than a list of allowed
// words. `fugaro:queued` is a status the daemon's phase mapping produces but
// never a label (R4), `spec:` alone is a name T1's OwnedLabel calls Fugaro's own
// and this route must still refuse (the shape is what decides, not the
// namespace), the over-long spec name is R3's bound (44 characters, so that
// `spec:` plus it fits GitHub's 50), and `spec:forge-<Kelvin>labels` is a name
// that PASSES R3's shape if it is lowered first — the Kelvin sign lower-cases to
// `k` — and that the ASCII check refuses. A mutant that lowered before checking
// turns that row green-where-it-must-be-red, and one that dropped the shape
// check serves the two rows before it.
//
// The audit is read here too: every call of the table, served or refused, writes
// exactly one line, which is C1.13's rule for a caller this service HAS verified.
func TestLabelRouteRefusesForeignNames(t *testing.T) {
	s, gh := labelRouteSvc(t)
	for _, tc := range []struct {
		name string
		want string
		ok   bool
	}{
		{name: wantRunningName, want: wantRunningName, ok: true},
		{name: wantFailedName, want: wantFailedName, ok: true},
		{name: "FUGARO:RUNNING", want: wantRunningName, ok: true},
		{name: wantSpecName, want: wantSpecName, ok: true},
		{name: "SPEC:Forge-Labels", want: wantSpecName, ok: true},
		{name: "spec:0", want: "spec:0", ok: true},
		{name: "spec:" + strings.Repeat("a", 44), want: "spec:" + strings.Repeat("a", 44), ok: true},
		{name: "bug"},
		{name: "fugaro:queued"},
		{name: "spec:"},
		{name: "spec:-leading-dash"},
		{name: "spec:under_score"},
		{name: "spec:forge-labels "},
		{name: "spec:" + strings.Repeat("a", 45)},
		{name: "spec:forge-Klabels"},
		{name: "fugaro:running\n"},
		{name: ""},
	} {
		t.Run(strconv.Quote(tc.name), func(t *testing.T) {
			before, wrote, lines := gh.count(), len(gh.writes()), len(s.auditLines(t))
			resp, body := s.labelEnsure(t, tc.name)
			if got := len(s.auditLines(t)); got != lines+1 {
				t.Errorf("the call wrote %d audit lines, want exactly one", got-lines)
			}
			if !tc.ok {
				if resp.StatusCode != StatusRequestRefused {
					t.Fatalf("the route served the name %q: HTTP %d %s", tc.name, resp.StatusCode, body)
				}
				if e := refusal(t, resp.StatusCode, body); e.Error != ErrorRequestRefused {
					t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorRequestRefused, e.Message)
				}
				if after := gh.count(); after != before {
					t.Errorf("the refused name %q reached GitHub %d times, want none", tc.name, after-before)
				}
				return
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("the route refused the name %q: HTTP %d %s", tc.name, resp.StatusCode, body)
			}
			labelAnswer(t, body)
			created := gh.writes()
			if len(created) != wrote+1 {
				t.Fatalf("the call wrote %d label definitions, want one", len(created)-wrote)
			}
			last := created[len(created)-1]
			var sent struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal([]byte(last.body), &sent); err != nil {
				t.Fatalf("the creation's body is not JSON: %v (%q)", err, last.body)
			}
			if sent.Name != tc.want {
				t.Errorf("the route created the label %q, want %q: the name is canonicalised before it is written", sent.Name, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The removals
// ---------------------------------------------------------------------------

// TestLabelRouteRemovesOnlyStatusNames is F23's and F26's red step: `remove`
// takes the two status names and nothing else, so a stolen daemon identity (F18)
// cannot take a `spec:` label a person added off a pull request, and a `spec:`
// label the daemon itself added is removed by the same rule.
//
// The mutant is the one that checks a name with the same function `add` uses:
// every removal below would then be served, and the spec row's DELETE is the
// request the fake would have seen.
func TestLabelRouteRemovesOnlyStatusNames(t *testing.T) {
	s, gh := labelRouteSvc(t)
	for _, tc := range []struct {
		name string
		ok   bool
	}{
		{name: wantRunningName, ok: true},
		{name: wantFailedName, ok: true},
		{name: "FUGARO:FAILED", ok: true},
		{name: wantSpecName},
		{name: "spec:0"},
		{name: "bug"},
	} {
		t.Run(strconv.Quote(tc.name), func(t *testing.T) {
			before := len(gh.writes())
			resp, body := s.labelWrite(t, creds.LabelActionRemove, tc.name)
			if !tc.ok {
				if resp.StatusCode != StatusRequestRefused {
					t.Fatalf("the route removed %q: HTTP %d %s", tc.name, resp.StatusCode, body)
				}
				if after := len(gh.writes()); after != before {
					t.Errorf("the refused removal of %q reached GitHub %d times, want none", tc.name, after-before)
				}
				return
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("the route refused to remove %q: HTTP %d %s", tc.name, resp.StatusCode, body)
			}
			wrote := gh.writes()
			if len(wrote) != before+1 {
				t.Fatalf("the removal wrote %d label calls, want one", len(wrote)-before)
			}
			last := wrote[len(wrote)-1]
			want := gh.repoPath("/issues/7/labels/" + strings.ToLower(tc.name))
			if last.method != http.MethodDelete || last.path != want {
				t.Errorf("the removal was %s %s, want DELETE %s", last.method, last.path, want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The text
// ---------------------------------------------------------------------------

// TestLabelRouteEnsureUsesFixedText is D2437's red step: the colour and the
// description a label is created with are this service's constants, and the
// caller names neither — the request carries a name and the strict decoder
// refuses a body that carries anything else (F26).
//
// The three sentences are written out here rather than read from the code, so a
// changed colour or a reworded description is a test failure: they are what an
// operator and a person reading a pull request see, and SECURITY.md states their
// reach in the same words (T6).
func TestLabelRouteEnsureUsesFixedText(t *testing.T) {
	s, gh := labelRouteSvc(t)
	for _, tc := range []struct{ name, colour, text string }{
		{name: wantRunningName, colour: "1D76DB", text: "A Fugaro job is working on this pull request"},
		{name: wantFailedName, colour: "D73A4A", text: "The last Fugaro job on this pull request failed"},
		{name: wantSpecName, colour: "6E7781", text: "Pull request for spec forge-labels"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(gh.writes())
			resp, body := s.labelEnsure(t, tc.name)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("the route refused to ensure %q: HTTP %d %s", tc.name, resp.StatusCode, body)
			}
			wrote := gh.writes()
			if len(wrote) != before+1 {
				t.Fatalf("the ensure wrote %d label calls, want one", len(wrote)-before)
			}
			var sent struct {
				Name        string `json:"name"`
				Color       string `json:"color"`
				Description string `json:"description"`
			}
			if err := json.Unmarshal([]byte(wrote[before].body), &sent); err != nil {
				t.Fatalf("the creation's body is not JSON: %v (%q)", err, wrote[before].body)
			}
			if sent.Color != tc.colour || sent.Description != tc.text {
				t.Errorf("the label %q was created as colour %q description %q, want %q and %q",
					sent.Name, sent.Color, sent.Description, tc.colour, tc.text)
			}
		})
	}

	// A second ensure of a name that is already there is success and not a
	// second creation: the host answers 422 already_exists, which T1's adapter
	// reads as done (D2440).
	resp, body := s.labelEnsure(t, wantRunningName)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the second ensure of a label that exists was refused: HTTP %d %s", resp.StatusCode, body)
	}

	// The caller cannot name the text at all: a body carrying a colour or a
	// description is refused by the strict decoder rather than read for the
	// fields it knows (Convention 6), which is what makes F26 a property of the
	// contract and not of a check inside the handler.
	for _, extra := range []string{`"color":"FF0000"`, `"description":"anything at all"`} {
		raw := `{"repository":"` + testRepoPath + `","action":"ensure","app_id":"` + testAppID + `","name":"` + wantRunningName + `",` + extra + `}`
		before := len(gh.writes())
		resp, body := s.labelCall(t, raw)
		if resp.StatusCode != StatusRequestRefused {
			t.Fatalf("a body carrying %s was served: HTTP %d %s", extra, resp.StatusCode, body)
		}
		if after := len(gh.writes()); after != before {
			t.Errorf("a body carrying %s reached GitHub", extra)
		}
	}
}

// ---------------------------------------------------------------------------
// The corroboration
// ---------------------------------------------------------------------------

// TestLabelRouteCorroboratesPullRequest is F17's red step, and it is the whole
// of why a write carries a run id: a hostile run can write another pull
// request's number into its own record (the triage code refuses to trust that
// record for the same reason), so the daemon sends the id of the run that opened
// the pull request out of its OWN intents and the service refuses the write
// unless the pull request's head branch is exactly `fugaro/<run id>` — plus the
// two facts that keep a fork and another account's pull request out.
//
// The admitted rows come first and the refused ones after, and every refused row
// asserts that no label write reached GitHub: the corroboration is before the
// write and not beside it.
//
// The merged row is the one that is NOT refused: clearing a merged pull
// request's status labels is exactly what the syncer reads it for (R5, R8), so
// the route corroborates the head and does not care about the state.
func TestLabelRouteCorroboratesPullRequest(t *testing.T) {
	branch := func(runID string) string { return "fugaro/" + runID }
	for _, tc := range []struct {
		name   string
		head   string
		repo   string
		author string
		state  string
		merged bool
		ok     bool
	}{
		{name: "the run's own pull request", head: branch("RUN"), repo: testRepoPath, author: labelAppAuthor, state: "open", ok: true},
		{name: "a merged one is corroborated all the same", head: branch("RUN"), repo: testRepoPath, author: labelAppAuthor, state: "closed", merged: true, ok: true},
		{name: "another run's pull request", head: branch("OTHER"), repo: testRepoPath, author: labelAppAuthor, state: "open"},
		{name: "a branch that merely begins with the run's", head: branch("RUN") + "-again", repo: testRepoPath, author: labelAppAuthor, state: "open"},
		{name: "a branch that is not a run's at all", head: "main", repo: testRepoPath, author: labelAppAuthor, state: "open"},
		{name: "a fork carrying the run's branch name", head: branch("RUN"), repo: "someone/aurora", author: labelAppAuthor, state: "open"},
		{name: "a deleted fork", head: branch("RUN"), repo: "", author: labelAppAuthor, state: "open"},
		{name: "an author that is not Fugaro", head: branch("RUN"), repo: testRepoPath, author: "a-person", state: "open"},
		{name: "Fugaro's identity without the bot suffix", head: branch("RUN"), repo: testRepoPath, author: testAppSlug, state: "open"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, gh := labelRouteSvc(t)
			gh.pull = labelPR{
				number: 7, state: tc.state, merged: tc.merged, head: strings.Replace(tc.head, "RUN", s.run, 1),
				repo: tc.repo, author: tc.author,
			}
			before := len(gh.writes())
			resp, body := s.labelWrite(t, creds.LabelActionAdd, wantRunningName)
			if !tc.ok {
				if resp.StatusCode != StatusRequestRefused {
					t.Fatalf("the write landed on a pull request the run id does not name: HTTP %d %s", resp.StatusCode, body)
				}
				if e := refusal(t, resp.StatusCode, body); e.Error != ErrorRequestRefused {
					t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorRequestRefused, e.Message)
				}
				if after := len(gh.writes()); after != before {
					t.Errorf("the refused write reached GitHub %d times, want none", after-before)
				}
				return
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("the write on the run's own pull request was refused: HTTP %d %s", resp.StatusCode, body)
			}
			wrote := gh.writes()
			if len(wrote) != before+1 {
				t.Fatalf("the write made %d label calls, want one", len(wrote)-before)
			}
			if got := wrote[before].path; got != gh.repoPath("/issues/7/labels") {
				t.Errorf("the write went to %s, want %s", got, gh.repoPath("/issues/7/labels"))
			}
		})
	}

	// A pull request the service cannot read is an outage and not a bad call: a
	// GitHub that answers 404 or 500 is not a caller that named the wrong run,
	// and the two answers are different ones.
	t.Run("a pull request the service cannot read", func(t *testing.T) {
		s, gh := labelRouteSvc(t)
		gh.pullStatus = http.StatusInternalServerError
		resp, body := s.labelWrite(t, creds.LabelActionAdd, wantRunningName)
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("a pull request read that failed was answered %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
		}
		if e := refusal(t, resp.StatusCode, body); e.Error != ErrorStateUnavailable {
			t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorStateUnavailable, e.Message)
		}
	})
}

// ---------------------------------------------------------------------------
// The class
// ---------------------------------------------------------------------------

// TestLabelRouteRefusesEveryOtherClass is the class's red step: the daemon and
// nobody else. It presents the six classes of the caller-class matrix — a job, a
// review job, a verifier, a person, the coordinator and a launcher — each with
// the audience this service's own URL gives it AND with the one a plain `gcloud
// auth print-identity-token` mints, and the daemon's own token last.
//
// A refusal here is the class's and has NO audit line, which is this service's
// rule for every class refusal: nothing is verified yet, so there is no caller
// for a line to name. Every one of them reaches GitHub zero times — a mutant
// that admitted a class would have to reach the fake to write anything, and the
// fake's own count is what says it did not.
func TestLabelRouteRefusesEveryOtherClass(t *testing.T) {
	s, gh := labelRouteSvc(t)
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
					resp, body := s.callPerson(t, LabelPath, labelBody(testRepoPath, creds.LabelActionEnsure, wantRunningName, 0, ""), caller.token(aud.aud))
					if resp.StatusCode != StatusUnverified {
						t.Fatalf("a %s caller was answered %d on the label route, want %d: %s", caller.name, resp.StatusCode, StatusUnverified, body)
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
	resp, body := s.labelEnsure(t, wantRunningName)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the orchestrator daemon was refused on the label route: HTTP %d %s", resp.StatusCode, body)
	}
	if got := s.auditLines(t); len(got) == 0 {
		t.Fatal("the served call wrote no audit line")
	}
}

// ---------------------------------------------------------------------------
// The reach
// ---------------------------------------------------------------------------

// TestLabelRoutePermissionSet is U2's red step: the token the route mints for
// its own writes carries exactly `{pull_requests: write, metadata: read}` — the
// narrowest set GitHub's label endpoints need — and it never leaves this
// process.
//
// Three things are read from the same two calls. The mint's own body is held to
// the set field by field, so a widened set is a failure and not a subset. The
// number of mints is held to one, which is the cache: a write reads the pull
// request twice (PRInfo.SourceBranch and PRHead.Self, F17) and both reads are
// made with ONE token. And the token's value is searched for in the answer, in
// the audit lines and in the service's own log, which is what "no token leaves
// this service" means for the one route that exists so that none does (D2439,
// F5).
//
// The last step moves the service's clock to within the API client's own
// validity margin of the token's expiry and makes a third call: the cache is
// held until NEAR expiry, so that call mints again. A mutant that cached the
// token for its whole life fails there, and one that never cached fails on the
// second call.
func TestLabelRoutePermissionSet(t *testing.T) {
	s, gh := labelRouteSvc(t)
	resp, body := s.labelWrite(t, creds.LabelActionAdd, wantRunningName)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the label write was refused: HTTP %d %s", resp.StatusCode, body)
	}
	resp, body = s.labelWrite(t, creds.LabelActionRemove, wantFailedName)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the label removal was refused: HTTP %d %s", resp.StatusCode, body)
	}
	mints := gh.mints(t)
	if len(mints) != 1 {
		t.Fatalf("two label calls minted %d installation tokens, want one: the token is cached until near expiry", len(mints))
	}
	perms, ok := mints[0]["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("the mint's body carries no permission set: %v", mints[0])
	}
	want := map[string]string{"pull_requests": "write", "metadata": "read"}
	if len(perms) != len(want) {
		t.Errorf("the mint asks for %v, want exactly %v", perms, want)
	}
	for noun, level := range want {
		if got, _ := perms[noun].(string); got != level {
			t.Errorf("the mint asks for %s: %v, want %q", noun, perms[noun], level)
		}
	}

	// The token is a credential and it is nowhere a caller or an operator can
	// read it.
	if strings.Contains(string(body), gh.token) {
		t.Error("the answer carries the installation token")
	}
	if strings.Contains(s.audit.String(), gh.token) {
		t.Error("an audit line carries the installation token")
	}
	if strings.Contains(s.logs.String(), gh.token) {
		t.Error("the service's log carries the installation token")
	}

	// The token is minted with the label set and never the run's: the provider
	// the route performs its calls through is built with the bundle's key — the
	// one read that names the App needs it (GET /app, which is what makes
	// PRHead.Self decidable) — and its own in-job mint is replaced before any
	// call is made. A provider left with the key's own token source would mint
	// the run's set here instead.
	if _, ok := perms["contents"]; ok {
		t.Error("the mint asks for contents, which the run's own set carries and a label write does not need")
	}

	// The clock moves to where the cached token is inside the client's validity
	// margin: the next call mints again.
	tok := s.orchestratorToken(func(c map[string]any) { c["exp"] = s.now.Add(4 * time.Hour).Unix() })
	s.fixture.now = s.now.Add(59 * time.Minute)
	gh.expires = s.now.Add(time.Hour)
	resp, body = s.callPerson(t, LabelPath, labelBody(testRepoPath, creds.LabelActionAdd, wantRunningName, 7, s.run), tok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the label write after the clock moved was refused: HTTP %d %s", resp.StatusCode, body)
	}
	if got := len(gh.mints(t)); got != 2 {
		t.Errorf("the route minted %d tokens, want a second one once the cached one is near its expiry", got)
	}
}

// TestLabelRouteWithoutItsOwnToken: the route performs its acts with a token it
// mints itself and has no second way to reach GitHub, so a mint GitHub refuses
// is a refusal of the act — a state_unavailable the daemon reports — and not a
// call made with some other credential or an act reported as done.
//
// The ensure is the call that reaches the mint first, because it corroborates
// nothing: a write would have to read the pull request before it wrote, and the
// read is what a missing token would fail on.
func TestLabelRouteWithoutItsOwnToken(t *testing.T) {
	s, gh := labelRouteSvc(t)
	gh.mintStatus = http.StatusUnauthorized
	resp, body := s.labelEnsure(t, wantRunningName)
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("a call whose mint was refused was answered %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorStateUnavailable {
		t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorStateUnavailable, e.Message)
	}
	if got := len(gh.writes()); got != 0 {
		t.Errorf("a call whose mint was refused wrote %d labels, want none", got)
	}
	if got := len(s.auditLines(t)); got != 1 {
		t.Errorf("the refused call wrote %d audit lines, want exactly one", got)
	}
}

// ---------------------------------------------------------------------------
// The bound
// ---------------------------------------------------------------------------

// labelTick is how often the orchestrator's daemon ticks (F22): the number the
// two rate limits below are sized for, written here as the fact they are sized
// against rather than read from the code.
const labelTick = 30 * time.Second

// TestLabelRouteRateSizedForTick is F22's red step: the route states its own
// limits as numbers — 60 writes and 240 reads an hour per repository, counted
// apart — and they hold a tick every 30 seconds that writes only on a
// difference.
//
// The arithmetic first, because it is what the numbers are FOR: an hour is 120
// ticks, a tick reads the open listing once (and one pull request by number for
// a pull request the listing did not show, R5), and it writes only where a
// label differs — so 240 reads an hour is two a tick and 60 writes is one every
// other tick, which is more than a daemon that writes differences needs and far
// less than a daemon looping could spend.
//
// Then the two windows are driven to their edge, each with the other's calls in
// between: the reads do not spend the write budget and the writes do not spend
// the read budget (the two are counted apart), and a call of either kind made
// after the hour has passed is served again (the bound is a window and not a
// lifetime cap).
func TestLabelRouteRateSizedForTick(t *testing.T) {
	const (
		writesPerHour = 60
		readsPerHour  = 240
		window        = time.Hour
	)
	if ticks := int(window / labelTick); readsPerHour < 2*ticks {
		t.Errorf("the read limit is %d an hour, and %d ticks an hour read twice each", readsPerHour, ticks)
	}
	if ticks := int(window / labelTick); writesPerHour < ticks/2 {
		t.Errorf("the write limit is %d an hour, and %d ticks an hour write on every other tick", writesPerHour, ticks)
	}

	s, gh := labelRouteSvc(t)
	write := func(t *testing.T) (*http.Response, []byte) {
		t.Helper()
		return s.labelWrite(t, creds.LabelActionAdd, wantRunningName)
	}
	read := func(t *testing.T) (*http.Response, []byte) {
		t.Helper()
		return s.labelCall(t, labelBody(testRepoPath, creds.LabelActionList, "", 0, ""))
	}

	// The read budget, spent with a write in the middle of it: the write is
	// served, so the two are counted apart.
	for i := 0; i < readsPerHour; i++ {
		resp, body := read(t)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the route refused read %d of %d in the window: HTTP %d %s", i+1, readsPerHour, resp.StatusCode, body)
		}
		if i == readsPerHour/2 {
			if resp, body := write(t); resp.StatusCode != http.StatusOK {
				t.Fatalf("a write in the middle of the read budget was refused: HTTP %d %s", resp.StatusCode, body)
			}
		}
	}
	before := gh.count()
	resp, body := read(t)
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the route served a %dth read for one repository in an hour: HTTP %d %s", readsPerHour+1, resp.StatusCode, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorStateUnavailable {
		t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorStateUnavailable, e.Message)
	}
	if after := gh.count(); after != before {
		t.Errorf("the rate-limited read reached GitHub %d times, want none: the rate is checked before the credential is read", after-before)
	}

	// The write budget, spent while the reads are exhausted: the reads being
	// over their rate does not refuse a write.
	for i := 0; i < writesPerHour-1; i++ {
		resp, body := write(t)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the route refused write %d of %d in the window: HTTP %d %s", i+2, writesPerHour, resp.StatusCode, body)
		}
	}
	resp, body = write(t)
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the route served a %dth write for one repository in an hour: HTTP %d %s", writesPerHour+1, resp.StatusCode, body)
	}

	// The window passes: both are served again. The caller's own ID token is
	// minted before the clock moves, because it has a life too, and so is the
	// token the fake GitHub hands out — a token GitHub mints at the new instant
	// is one this service reads as live.
	tok := s.orchestratorToken(func(c map[string]any) { c["exp"] = s.now.Add(4 * time.Hour).Unix() })
	s.fixture.now = s.now.Add(window + time.Minute)
	gh.expires = s.now.Add(time.Hour)
	resp, body = s.callPerson(t, LabelPath, labelBody(testRepoPath, creds.LabelActionList, "", 0, ""), tok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the route refused a read made after the window passed: HTTP %d %s", resp.StatusCode, body)
	}
	resp, body = s.callPerson(t, LabelPath, labelBody(testRepoPath, creds.LabelActionAdd, wantRunningName, 7, s.run), tok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the route refused a write made after the window passed: HTTP %d %s", resp.StatusCode, body)
	}
}

// ---------------------------------------------------------------------------
// The path
// ---------------------------------------------------------------------------

// TestLabelRoutePathMatchesClient is the route's own naming test: the path the
// daemon's client will spell (creds.LabelPath, which this package can read and
// which cannot read back), the path this service serves, and the purpose and
// credential routeAct answers for it, all held to one value.
//
// The purpose is held to being a word of its own and not one of the other
// routes': the label route's credential is the App key, which three other routes
// carry, so the purpose is the only thing that tells the acts apart in a line.
// The credential is held to the App key because that is what the route reads.
func TestLabelRoutePathMatchesClient(t *testing.T) {
	if LabelPath != labelPath {
		t.Errorf("the label route's path = %q, want %q", LabelPath, labelPath)
	}
	if creds.LabelPath != LabelPath {
		t.Errorf("the client's label path = %q and the service's = %q, want one path", creds.LabelPath, LabelPath)
	}
	purpose, credential := routeAct(LabelPath)
	if purpose != PurposeLabel || credential != config.GitHubAppKeySecret {
		t.Errorf("routeAct(%s) = (%q, %q), want (%q, %q)", LabelPath, purpose, credential, PurposeLabel, config.GitHubAppKeySecret)
	}
	for _, other := range []string{MintPath, BitbucketReadPath, GitHubReadPath, MergeMintPath} {
		if other, _ := routeAct(other); other == purpose {
			t.Errorf("the label route shares the purpose %q with %s, so a line cannot tell the acts apart", purpose, other)
		}
	}

	s, _ := labelRouteSvc(t)
	resp, body := s.postRaw(t, http.MethodPost, "/v1/github/label")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an unknown path answered %d, want 404: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), LabelPath) {
		t.Errorf("the not-found answer does not name %s among the routes this service serves: %s", LabelPath, body)
	}
}

// TestLabelRouteRunBranchPrefixMatchesRunner holds the one string the label
// route spells for itself to the runner's own constant (gitops.RunBranchPrefix).
//
// The corroboration compares a pull request's head branch against the branch the
// run that opened it pushes to, so the prefix is a fact about the RUNNER and not
// about this service — and this service holds no git and must not grow one for a
// string, so the two spellings live in two packages that do not import each
// other and this test is what keeps them one value. The fixture the writes are
// driven through spells the branch with the literal, so a rename on the runner's
// side fails here and in every corroboration test rather than quietly admitting
// a branch no run pushes to.
func TestLabelRouteRunBranchPrefixMatchesRunner(t *testing.T) {
	if labelRunBranchPrefix != gitops.RunBranchPrefix {
		t.Errorf("the label route's run branch prefix = %q and the runner's = %q, want one prefix", labelRunBranchPrefix, gitops.RunBranchPrefix)
	}
	runID := "20260101-000000-ab12"
	if got := labelRunBranch(runID); got != gitops.RunBranchPrefix+runID {
		t.Errorf("the fixture's branch = %q, want %q", got, gitops.RunBranchPrefix+runID)
	}
}

// TestLabelRouteRefusesACallItCannotRead: the body is one JSON object of the
// route's own fields and the fields are the ones its action takes, so a call
// this contract does not have is refused before any credential is read and
// before GitHub is reached.
//
// The repository row is the inventory's own rule (the read mints'): a repository
// this installation does not hold is refused by name, and a name that is not a
// repository at all is refused without echoing what the caller sent
// (readRefusalMessage).
func TestLabelRouteRefusesACallItCannotRead(t *testing.T) {
	s, gh := labelRouteSvc(t)
	run := s.run
	for _, tc := range []struct{ name, body string }{
		{name: "no act at all", body: `{"repository":"` + testRepoPath + `","app_id":"` + testAppID + `"}`},
		{name: "an act the route does not perform", body: labelBody(testRepoPath, "delete", wantRunningName, 7, run)},
		{name: "a number that is not a pull request", body: labelBody(testRepoPath, creds.LabelActionGet, "", 0, "")},
		{name: "a negative number", body: labelBody(testRepoPath, creds.LabelActionAdd, wantRunningName, -1, run)},
		{name: "a write with no run id", body: labelBody(testRepoPath, creds.LabelActionAdd, wantRunningName, 7, "")},
		{name: "a write with a run id that is not one", body: labelBody(testRepoPath, creds.LabelActionAdd, wantRunningName, 7, "not-a-run")},
		{name: "a write with no name", body: labelBody(testRepoPath, creds.LabelActionAdd, "", 7, run)},
		{name: "a list that names a label", body: labelBody(testRepoPath, creds.LabelActionList, wantRunningName, 0, "")},
		{name: "an ensure that names a pull request", body: labelBody(testRepoPath, creds.LabelActionEnsure, wantRunningName, 7, "")},
		{name: "a get that names a label", body: labelBody(testRepoPath, creds.LabelActionGet, wantRunningName, 7, "")},
		{name: "no repository", body: labelBody("", creds.LabelActionList, "", 0, "")},
		{name: "a repository that is not a path", body: labelBody("acme", creds.LabelActionList, "", 0, "")},
		{name: "a repository this installation does not hold", body: labelBody("other/aurora", creds.LabelActionList, "", 0, "")},
		{name: "no App ID", body: `{"repository":"` + testRepoPath + `","action":"list","app_id":""}`},
		{name: "a field the contract does not have", body: `{"repository":"` + testRepoPath + `","action":"list","app_id":"` + testAppID + `","token":"` + fakeCred(t, "stolen") + `"}`},
		{name: "a body that is not an object", body: `[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, lines := gh.count(), len(s.auditLines(t))
			resp, body := s.labelCall(t, tc.body)
			if resp.StatusCode != StatusRequestRefused {
				t.Fatalf("the route served a call it does not read: HTTP %d %s", resp.StatusCode, body)
			}
			if e := refusal(t, resp.StatusCode, body); e.Error != ErrorRequestRefused {
				t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorRequestRefused, e.Message)
			}
			if got := len(s.auditLines(t)); got != lines+1 {
				t.Errorf("the refused call wrote %d audit lines, want exactly one", got-lines)
			}
			if after := gh.count(); after != before {
				t.Errorf("the refused call reached GitHub %d times, want none", after-before)
			}
		})
	}
}

// The label route reads a pull request's labels through the same adapter the
// daemon does (T1), and the set it is held to here is that adapter's own: the
// open listing answers every open pull request with its labels, and the read of
// one answers its state. It is asserted once, in the shape the daemon's syncer
// consumes (R5, R8).
func TestLabelRouteReadsTheAdaptersAnswer(t *testing.T) {
	s, gh := labelRouteSvc(t)
	gh.open = []labelPR{
		{number: 3, labels: []string{wantRunningName, wantSpecName}},
		{number: 5},
	}
	gh.pull = labelPR{number: 5, state: "closed", merged: true, labels: []string{wantFailedName}}

	resp, body := s.labelCall(t, labelBody(testRepoPath, creds.LabelActionList, "", 0, ""))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the open listing was refused: HTTP %d %s", resp.StatusCode, body)
	}
	ans := labelAnswer(t, body)
	if len(ans.PullRequests) != 2 {
		t.Fatalf("the listing answered %d pull requests, want 2: %+v", len(ans.PullRequests), ans.PullRequests)
	}
	if ans.PullRequests[0].Number != 3 || ans.PullRequests[1].Number != 5 {
		t.Errorf("the listing answered %+v, want it ordered by number", ans.PullRequests)
	}
	if got := ans.PullRequests[1].Labels; len(got) != 0 {
		t.Errorf("an open pull request with no labels answered %v, want none", got)
	}
	if got := ans.PullRequests[1].State; got != string(gitprov.PROpen) {
		t.Errorf("the listing answered state %q for an open pull request, want %q", got, gitprov.PROpen)
	}

	resp, body = s.labelCall(t, labelBody(testRepoPath, creds.LabelActionGet, "", 5, ""))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the read of one pull request was refused: HTTP %d %s", resp.StatusCode, body)
	}
	ans = labelAnswer(t, body)
	if len(ans.PullRequests) != 1 {
		t.Fatalf("the read answered %d pull requests, want 1: %+v", len(ans.PullRequests), ans.PullRequests)
	}
	got := ans.PullRequests[0]
	if got.Number != 5 || got.State != string(gitprov.PRMerged) || len(got.Labels) != 1 || got.Labels[0] != wantFailedName {
		t.Errorf("the read answered %+v, want pull request 5, merged, carrying %s", got, wantFailedName)
	}
}
