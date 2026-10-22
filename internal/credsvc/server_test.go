package credsvc

// The service's own tests (C1.11): one installation wired from fakes, and the
// calls a run's in-runner gateway would make to it.
//
// Everything here is hermetic (Convention 9): the upstream is
// internal/gateway/anthropicfake, the bundle comes from a scripted source, the
// policy from a decoded document, the state from a map, and no test opens a
// socket to anything but 127.0.0.1. Nothing in this file is, or looks like, a
// credential: every value is built at run time by fakeCred (Convention 2).

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
)

const (
	// testRepoPath is the repository the fixture's installation serves, and
	// testRepo its canonical spelling — the form the published policy's
	// allow-lists hold.
	testRepoPath = "acme/aurora"
	// testOtherRepoPath is a second repository of the same project, for the
	// tests that need a job of one repository to be distinguishable from a
	// run of another.
	testOtherRepoPath = "acme/borealis"
	// testAccount is the second Claude account the fixture's bundle holds, by
	// the name the policy's accounts block spells it with.
	testAccount = "alice"
	// testModel is the model the fake upstream answers as.
	testModel = "claude-sonnet-5-5"
)

// testRepo and testOtherRepo are the canonical spellings of the two
// repositories.
var (
	testRepo      = "github/" + testRepoPath
	testOtherRepo = "github/" + testOtherRepoPath
)

// testUsage is the usage every fake answer reports. It is small and fixed: no
// test here reads a ledger.
var testUsage = pricing.Usage{Input: 10, Output: 10}

// testSlug is the storage slug of a repository: the identity a run token
// carries, derived the one way the runner derives it.
func testSlug(t *testing.T, repo string) string {
	t.Helper()
	slug, err := task.Slug("github", repo)
	if err != nil {
		t.Fatalf("task.Slug(%q): %v", repo, err)
	}
	return slug
}

// testPolicyDoc is the project's published policy as an operator writes it:
// the project admits both repositories, both accounts are offered to both, and
// the cascade is on (`fallback: auto`, which Decode accepts only beside a
// budget that bounds the spend).
//
// It is decoded rather than built by hand on purpose — Decode is the reader
// the service deploys, and a hand-built policy is a policy no operator could
// publish.
func testPolicyDoc() string { return testPolicyDocWith("auto") }

// testPolicyDocWith is the same document with the cascade's own switch written
// out: `fallback: auto` spends the project's key, `oauth-only` does not, and
// the two are the whole of D152's difference.
func testPolicyDocWith(fallback string) string {
	return `{
		"version": 3,
		"budget": {"mode": "enforce", "per_run_usd": 5},
		"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"],
		"accounts": {
			"default": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]},
			"` + testAccount + `": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]}
		},
		"credentials": {"fallback": "` + fallback + `"}
	}`
}

func decodeTestPolicy(t *testing.T, doc string) *projectpolicy.Policy {
	t.Helper()
	p, found, err := projectpolicy.Decode([]byte(doc))
	if err != nil {
		t.Fatalf("the test's own policy document is not one Decode accepts: %v", err)
	}
	if !found {
		t.Fatal("the test's own policy document decoded as absent")
	}
	return p
}

// testChildDoc is one repository's own node: the accounts an administrator
// published for it (C1.2, O4).
func testChildDoc(accounts ...string) string {
	quoted := make([]string, 0, len(accounts))
	for _, a := range accounts {
		quoted = append(quoted, `"`+a+`"`)
	}
	return `{"credentials": {"accounts": [` + strings.Join(quoted, ", ") + `]}}`
}

func decodeTestChild(t *testing.T, doc string) *projectpolicy.RepoPolicy {
	t.Helper()
	rp, found, err := projectpolicy.DecodeRepo([]byte(doc))
	if err != nil {
		t.Fatalf("the test's own repository node is not one DecodeRepo accepts: %v", err)
	}
	if !found {
		t.Fatal("the test's own repository node decoded as absent")
	}
	return rp
}

// testBundleDoc is the installation's bundle: the default account's token,
// alice's, and the project's paid key.
func testBundleDoc(t *testing.T) []byte {
	return []byte(config.ClaudeOAuthToken + ": " + fakeCred(t, "default") + "\n" +
		config.ClaudeOAuthTokenPrefix + testAccount + ": " + fakeCred(t, testAccount) + "\n" +
		config.AnthropicSecret + ": " + fakeCred(t, "paid") + "\n")
}

// ---------------------------------------------------------------------------
// The fakes
// ---------------------------------------------------------------------------

// fakePolicy is the published policy as the service reads it: the project's
// shared node, and one repository's own node by slug. It records what was
// read, because "the admission is not consulted at all when no tier-A
// credential is in play" is a claim a test has to be able to check.
type fakePolicy struct {
	shared      *projectpolicy.Policy
	sharedFound bool
	sharedErr   error

	admissions map[string]*projectpolicy.RepoPolicy
	admErr     error

	mu             sync.Mutex
	sharedReads    int
	admissionReads []string
}

func (p *fakePolicy) Shared(context.Context) (*projectpolicy.Policy, bool, error) {
	p.mu.Lock()
	p.sharedReads++
	p.mu.Unlock()
	return p.shared, p.sharedFound, p.sharedErr
}

func (p *fakePolicy) Admissions(_ context.Context, slug string) (*projectpolicy.RepoPolicy, bool, error) {
	p.mu.Lock()
	p.admissionReads = append(p.admissionReads, slug)
	p.mu.Unlock()
	if p.admErr != nil {
		return nil, false, p.admErr
	}
	adm, ok := p.admissions[slug]
	return adm, ok, nil
}

// readAdmissions is the slugs whose own node was read, in order.
func (p *fakePolicy) readAdmissions() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.admissionReads...)
}

// fakeKills is the operator's two stop switches as the service reads them.
type fakeKills struct {
	global bool
	repos  map[string]bool
	err    error
	mu     sync.Mutex
	reads  int
}

func (k *fakeKills) Stop(_ context.Context, slug string) (bool, string, error) {
	k.mu.Lock()
	k.reads++
	k.mu.Unlock()
	switch {
	case k.err != nil:
		return false, "", k.err
	case k.global:
		return true, "global", nil
	case k.repos[slug]:
		return true, "repo", nil
	}
	return false, "", nil
}

// fakeStore is the cascade's shared state in memory, as creds.Store: the
// marks, the placements, the leases and the round-robin counter.
type fakeStore struct {
	mu      sync.Mutex
	marks   map[string]creds.Mark
	current map[string]string
	leases  map[string]creds.Lease
	rr      int64
	err     error
}

func newFakeStore() *fakeStore {
	return &fakeStore{marks: map[string]creds.Mark{}, current: map[string]string{}, leases: map[string]creds.Lease{}}
}

func runKey(run creds.Run) string { return run.Slug + "/" + run.ID }

func (s *fakeStore) Marks(context.Context) (map[string]creds.Mark, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[string]creds.Mark, len(s.marks))
	for k, v := range s.marks {
		out[k] = v
	}
	return out, nil
}

func (s *fakeStore) UpdateMark(_ context.Context, name string, f func(creds.Mark, bool) (creds.Mark, error)) (creds.Mark, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return creds.Mark{}, s.err
	}
	prev, found := s.marks[name]
	next, err := f(prev, found)
	if err != nil {
		return creds.Mark{}, err
	}
	s.marks[name] = next
	return next, nil
}

func (s *fakeStore) Leases(context.Context, time.Time) ([]creds.Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	out := make([]creds.Lease, 0, len(s.leases))
	for _, l := range s.leases {
		out = append(out, l)
	}
	return out, nil
}

func (s *fakeStore) SetPlacement(_ context.Context, run creds.Run, name string, l creds.Lease) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	l.Slug, l.Run = run.Slug, run.ID
	s.current[runKey(run)] = name
	s.leases[runKey(run)] = l
	return nil
}

func (s *fakeStore) Current(_ context.Context, run creds.Run) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return "", false, s.err
	}
	name, ok := s.current[runKey(run)]
	return name, ok, nil
}

func (s *fakeStore) RoundRobin(context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return 0, s.err
	}
	n := s.rr
	s.rr++
	return n, nil
}

func (s *fakeStore) mark(name string) (creds.Mark, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.marks[name]
	return m, ok
}

func (s *fakeStore) placed(run creds.Run) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name, ok := s.current[runKey(run)]
	return name, ok
}

// fakePatcher is the database's Patch as the ended-run cleanup uses it. It
// applies the updates to a node map, so a test can see exactly which nodes
// were removed, and it counts the calls, because one Patch for a run's whole
// placement is the point of the write.
type fakePatcher struct {
	mu    sync.Mutex
	nodes map[string]any
	calls []patchCall
	err   error
}

type patchCall struct {
	root    string
	updates map[string]any
}

func (p *fakePatcher) Patch(_ context.Context, root string, updates map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	p.calls = append(p.calls, patchCall{root: root, updates: updates})
	for k, v := range updates {
		full := k
		if root != "" {
			full = root + "/" + k
		}
		if v == nil {
			delete(p.nodes, full)
			continue
		}
		p.nodes[full] = v
	}
	return nil
}

// under is the nodes at or below prefix, sorted, so a test can state the whole
// subtree a cleanup left rather than the one key it looked for.
func (p *fakePatcher) under(prefix string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for k := range p.nodes {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

func (p *fakePatcher) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

// logBuf collects the service's own lines. It is the gateway tests' helper of
// the same name, respelled here because the two packages cannot share a test
// file.
type logBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// hostRewriter sends every upstream request to a test server, whatever host
// the service built the URL for. It is what lets the tier-A destination check
// (D133) run on the real destination — the service builds
// https://api.anthropic.com and checks it — while the request itself reaches a
// fake that speaks HTTP on loopback.
type hostRewriter struct {
	base *url.URL
	rt   http.RoundTripper
}

func (h hostRewriter) RoundTrip(r *http.Request) (*http.Response, error) {
	c := r.Clone(r.Context())
	c.URL.Scheme = h.base.Scheme
	c.URL.Host = h.base.Host
	c.Host = h.base.Host
	return h.rt.RoundTrip(c)
}

// ---------------------------------------------------------------------------
// The installation under test
// ---------------------------------------------------------------------------

// svc is one installation's credentials service, wired for a test.
type svc struct {
	*fixture
	server *Server
	http   *httptest.Server
	up     *anthropicfake.Fake
	src    *scriptedSource
	policy *fakePolicy
	kills  *fakeKills
	state  *fakeStore
	nodes  *fakePatcher
	logs   *logBuf
}

func newSvc(t *testing.T) *svc { return newSvcWith(t, nil) }

// newSvcWith is newSvc with the service's configuration under the test's
// control, for the tests that need one wired differently: a policy that admits
// nobody, a switch that is on, a bundle that cannot be read.
//
// The fixture's slug is the slug of testRepo, so the run token the fixture
// mints is a run of the repository the job account is bound to — which is the
// state every test but the binding test wants.
func newSvcWith(t *testing.T, mutate func(*svc, *Config)) *svc {
	t.Helper()
	f := newFixtureWith(t, nil)
	f.slug = testSlug(t, testRepoPath)
	up, upSrv := anthropicfake.New(t, anthropicfake.MessageOK(testModel, testUsage))
	upURL, err := url.Parse(upSrv.URL)
	if err != nil {
		t.Fatalf("the fake upstream's URL is not a URL: %v", err)
	}
	s := &svc{
		fixture: f,
		up:      up,
		src:     &scriptedSource{docs: [][]byte{testBundleDoc(t)}},
		policy: &fakePolicy{
			shared:      decodeTestPolicy(t, testPolicyDoc()),
			sharedFound: true,
			admissions: map[string]*projectpolicy.RepoPolicy{
				f.slug: decodeTestChild(t, testChildDoc("default", testAccount)),
			},
		},
		kills: &fakeKills{repos: map[string]bool{}},
		state: newFakeStore(),
		nodes: &fakePatcher{nodes: map[string]any{}},
		logs:  &logBuf{},
	}
	cfg := Config{
		Auth:    f.auth,
		Bundles: &BundleLoader{Source: s.src, Now: func() time.Time { return f.now }},
		Policy:  s.policy,
		State:   s.state,
		Kills:   s.kills,
		JobAccounts: []JobAccount{
			{Email: testJobSA, Slug: f.slug, Repo: testRepo},
			{Email: testOtherSA, Slug: testSlug(t, testOtherRepoPath), Repo: testOtherRepo},
		},
		Client: &http.Client{Transport: hostRewriter{base: upURL, rt: http.DefaultTransport}},
		Log:    slog.New(slog.NewTextHandler(s.logs, nil)),
		Now:    func() time.Time { return f.now },
		DB:     s.nodes,
	}
	if mutate != nil {
		mutate(s, &cfg)
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	s.server = srv
	s.http = httptest.NewServer(srv)
	t.Cleanup(s.http.Close)
	return s
}

// credsRun is the run this fixture's calls are made for, as creds names it:
// the slug the run token carries and the run id it carries.
func (s *svc) credsRun() creds.Run { return creds.Run{Slug: s.slug, ID: s.run} }

// post sends one call to the service as the fixture's run: a job token in
// Authorization, the run identity in its own header, and the job name the run
// reports.
func (s *svc) post(t *testing.T, path, body string, mutate ...func(*http.Request)) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, s.http.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("building the call: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.jobToken())
	req.Header.Set(HeaderRunIdentity, s.runToken())
	req.Header.Set(HeaderRunJob, s.job)
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	for _, m := range mutate {
		m(req)
	}
	resp, err := s.http.Client().Do(req)
	if err != nil {
		t.Fatalf("the call failed: %v", err)
	}
	return resp
}

// call is post with the answer read whole.
func (s *svc) call(t *testing.T, body string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	return s.callPath(t, MessagesPath, body, mutate...)
}

func (s *svc) callPath(t *testing.T, path, body string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	resp := s.post(t, path, body, mutate...)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	return resp, b
}

// refusal reads an answer as the wire's refusal body, which is what every
// refusal this service writes is.
func refusal(t *testing.T, status int, body []byte) WireError {
	t.Helper()
	e, err := ReadWireError(status, body)
	if err != nil {
		t.Fatalf("the refusal is not one this contract can read: %v", err)
	}
	return e
}

// callBody is a Messages request body with a marker in it, so a test can
// search a log line for text only that call carried.
func callBody(marker string) string {
	return `{"model":"` + testModel + `","max_tokens":16,"messages":[{"role":"user","content":"` + marker + `"}]}`
}

// readEvent reads one SSE event (its lines up to the blank line that ends it).
func readEvent(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	var b strings.Builder
	for {
		line, err := r.ReadString('\n')
		b.WriteString(line)
		if err != nil {
			return b.String()
		}
		if line == "\n" {
			return b.String()
		}
	}
}

// firstUpstream is the request the fake upstream saw first.
func (s *svc) firstUpstream(t *testing.T) *http.Request {
	t.Helper()
	seen := s.up.Seen()
	if len(seen) == 0 {
		t.Fatal("the upstream saw no request at all")
	}
	return seen[0]
}
