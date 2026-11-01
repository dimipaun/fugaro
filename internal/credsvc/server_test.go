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
	"strconv"
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

// testBudgetDoc is the budget block the fixture's policy documents publish: the
// ceiling an operator writes, carrying the model allow-list the service now
// requires. A project that publishes none is refused fail-closed (the owner's
// ruling of 2026-10-22, request.go), so a fixture whose calls are forwarded is
// a fixture whose policy publishes a list — and testPolicyDocNoAllowList is the
// one written for the tests about the refusal itself. (Other fixtures' own
// policy documents publish none too — sharedJobsPolicyDoc, say — but those are
// documents of another node, written for tests that never serve a call.)
var testBudgetDoc = `{"mode": "enforce", "per_run_usd": 5, "allowed_models": ["` + testModel + `"]}`

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
	return testPolicyDocAccounts(fallback, `"default": {"allow_repos": ["`+testRepo+`", "`+testOtherRepo+`"]},
			"`+testAccount+`": {"allow_repos": ["`+testRepo+`", "`+testOtherRepo+`"]}`)
}

// testPolicyDocAccounts is the same document with the shared node's accounts
// block written out, for the tests that need an account offered to one
// repository and not to another: the block is one leg of O4's admission, and a
// repository's own node naming an account the shared node does not offer it is
// exactly the case the intersection exists for.
func testPolicyDocAccounts(fallback, accounts string) string {
	return `{
		"version": 4,
		"budget": ` + testBudgetDoc + `,
		"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"],
		"accounts": {
			` + accounts + `
		},
		"credentials": {"fallback": "` + fallback + `"}
	}`
}

// testPolicyDocAllowRepos is the same document with the project's own top-level
// allow-list written out. It is the list `project repos rm` removes a
// repository from — the command takes the repository out of the allow-list and
// does not touch its child — so a test that needs the gap that command leaves
// needs a document where the child and the accounts still name the repository
// and the project does not.
func testPolicyDocAllowRepos(allowRepos, accounts string) string {
	return `{
		"version": 4,
		"budget": ` + testBudgetDoc + `,
		"allow_repos": ` + allowRepos + `,
		"accounts": {
			` + accounts + `
		},
		"credentials": {"fallback": "auto"}
	}`
}

// testPolicyDocNoCredentials is the same document with the `credentials` block
// left out. That is a policy Decode accepts — the block is optional, and an
// installation that writes none gets the spec's protective defaults — and it
// decodes with Policy.Credentials nil, which is the shape settingsOf must read
// without ever reaching into the block.
func testPolicyDocNoCredentials() string {
	return `{
		"version": 4,
		"budget": ` + testBudgetDoc + `,
		"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"],
		"accounts": {
			"default": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]},
			"` + testAccount + `": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]}
		}
	}`
}

// testPolicyDocNoAllowList is the same document with no budget.allowed_models
// at all: a project that publishes no model allow-list. It is the one policy
// this service refuses to serve a Messages call under (the owner's ruling of
// 2026-10-22), so it is written out rather than built by dropping a field from
// testBudgetDoc: the absent key is the case, and a document that spelled it as
// an empty list would be a different one.
func testPolicyDocNoAllowList() string {
	return `{
		"version": 4,
		"budget": {"mode": "enforce", "per_run_usd": 5},
		"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"],
		"accounts": {
			"default": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]},
			"` + testAccount + `": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]}
		},
		"credentials": {"fallback": "auto"}
	}`
}

// testPolicyDocControls is the same document with D142's two controls written
// out at their opt-out values, which is the other half of "an absent key is not
// the written value": false is the written "do not stop after one exhaustion",
// and 0 is the written "no cap at all" rather than the absent key's one.
func testPolicyDocControls(fallback string, stops bool, concurrency int) string {
	return `{
		"version": 4,
		"budget": ` + testBudgetDoc + `,
		"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"],
		"accounts": {
			"default": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]},
			"` + testAccount + `": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]}
		},
		"credentials": {"fallback": "` + fallback + `", "stop_after_exhaustion_per_window": ` +
		strconv.FormatBool(stops) + `, "concurrency_per_token": ` + strconv.Itoa(concurrency) + `}
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

// readCount is how many times the switches were read, which is what makes "the
// switch is read before the bundle" a claim a test can check.
func (k *fakeKills) readCount() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.reads
}

// fakeStore is the cascade's shared state in memory, as creds.Store: the
// marks, the placements, the leases and the round-robin counter.
//
// err fails every read and every write, which is the state a call meets when
// the database as a whole cannot be read. updateErr fails only a mark's write
// (UpdateMark), which is the other state a test needs and the one err cannot
// express: a selection that is made and a credential that is spent, whose mark
// cannot be recorded — the refusal the next call pays for (cascade.go's
// markRefusal).
type fakeStore struct {
	mu        sync.Mutex
	marks     map[string]creds.Mark
	current   map[string]string
	leases    map[string]creds.Lease
	rr        int64
	err       error
	updateErr error
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
	if s.updateErr != nil {
		return creds.Mark{}, s.updateErr
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

func (l *logBuf) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Len()
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
	probes *fakeProbes
	logs   *logBuf
	// audit is where C1.13's audit lines go. They are written to their own
	// handler and not to the service's logger (NewAuditor takes the handler),
	// so a test reads the contract's line apart from the service's own lines —
	// which is the property C1.13's file is built on.
	audit *logBuf
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
		kills:  &fakeKills{repos: map[string]bool{}},
		state:  newFakeStore(),
		nodes:  &fakePatcher{nodes: map[string]any{}},
		probes: newFakeProbes(),
		logs:   &logBuf{},
		audit:  &logBuf{},
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
		Probes: s.probes,
		// The audit lines are written as the JSON objects they are (C1.13), so
		// a test that reads one reads it the way an operator's tooling does.
		// They go to the fixture's own audit buffer and NOT to the service's
		// log: the auditor the service builds — for its probe, its answer path
		// and its mint alike, because the service has one auditor and this is
		// the handler it is built on — writes to this handler, and a test that
		// asserts what the service's own logger holds must not be reading audit
		// lines in it. A test that wants the two apart takes a buffer of its
		// own (probeSvc, auditSvc).
		AuditLog: slog.NewJSONHandler(s.audit, nil),
	}
	if mutate != nil {
		mutate(s, &cfg)
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	s.server = srv
	s.http = httptest.NewUnstartedServer(srv)
	// The fixture's server speaks what the deployment's host speaks (B1.0):
	// HTTP/1.1 and HTTP/2 cleartext on one listener, from the standard
	// library's own protocol set, exactly as internal/cli/credentials_serve.go
	// sets it for the Cloud Run service. Every test that does not ask for h2c
	// keeps calling over HTTP/1.1 -- net/http's client chooses HTTP/1.1 for a
	// plain http:// address unless it is told otherwise -- so this changes the
	// server under test and not the tests that were already here.
	//
	// What is deliberately NOT mirrored is the deployment host's HTTP2 config
	// (its concurrent-stream limit and its no-progress write bound): those
	// bound what the hop in front of the process may ask of it, no test here
	// reaches across a connection that far, and the host's own values are
	// pinned where they are set (TestCredentialsServeStillSpeaksHTTP1).
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	s.http.Config.Protocols = &protocols
	s.http.Start()
	t.Cleanup(s.http.Close)
	return s
}

// h2cClient is a caller that speaks HTTP/2 to the fixture's cleartext address
// with prior knowledge: it opens the connection, sends the HTTP/2 preface and
// no TLS handshake at all. That is what Cloud Run's front end does to the
// container once the service's port is named h2c (B1.0, credentials.tf), and
// only HTTP/2 is offered here, so a test using this client cannot be passing
// over HTTP/1.1 by negotiation.
func h2cClient() *http.Client {
	var protocols http.Protocols
	protocols.SetUnencryptedHTTP2(true)
	return &http.Client{Transport: &http.Transport{Protocols: &protocols}}
}

// credsRun is the run this fixture's calls are made for, as creds names it:
// the slug the run token carries and the run id it carries.
func (s *svc) credsRun() creds.Run { return creds.Run{Slug: s.slug, ID: s.run} }

// post sends one call to the service as the fixture's run: a job token in
// Authorization, the run identity in its own header, and the job name the run
// reports.
func (s *svc) post(t *testing.T, path, body string, mutate ...func(*http.Request)) *http.Response {
	t.Helper()
	return s.postTokens(t, path, body, s.runToken(), s.jobToken(), s.job, mutate...)
}

// postTokens is post with the two identities and the reported job name given
// rather than minted, for the calls that must present one the service refuses:
// a run token of another repository, a job token of an account the
// authenticator does not admit, or none at all.
func (s *svc) postTokens(t *testing.T, path, body, runTok, jobTok, job string, mutate ...func(*http.Request)) *http.Response {
	t.Helper()
	return s.postWith(t, s.http.Client(), path, body, runTok, jobTok, job, mutate...)
}

// postH2C is postTokens over the protocol the deployment actually speaks to the
// container: HTTP/2 cleartext, prior knowledge, no TLS (B1.0). The model path's
// streamed answers are what this has to keep whole -- a front end that buffers
// an HTTP/1 body is the failure this whole task exists for.
func (s *svc) postH2C(t *testing.T, path, body string, mutate ...func(*http.Request)) *http.Response {
	t.Helper()
	return s.postWith(t, h2cClient(), path, body, s.runToken(), s.jobToken(), s.job, mutate...)
}

// postWith is the call the two above share, with the client given as well as
// the two identities: what protocol the call goes out over is the test's, and
// nothing else about it is.
func (s *svc) postWith(t *testing.T, client *http.Client, path, body, runTok, jobTok, job string, mutate ...func(*http.Request)) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, s.http.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("building the call: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+jobTok)
	req.Header.Set(HeaderRunIdentity, runTok)
	req.Header.Set(HeaderRunJob, job)
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	for _, m := range mutate {
		m(req)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("the call failed: %v", err)
	}
	return resp
}

// postRaw sends a request the service is expected to refuse before it reads
// anything of the contract's: any method, any path, and no headers at all
// unless the test adds them.
func (s *svc) postRaw(t *testing.T, method, path string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, s.http.URL+path, strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("building the call: %v", err)
	}
	for _, m := range mutate {
		m(req)
	}
	resp, err := s.http.Client().Do(req)
	if err != nil {
		t.Fatalf("the call failed: %v", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	return resp, b
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
