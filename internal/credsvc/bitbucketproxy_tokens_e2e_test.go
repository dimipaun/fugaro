package credsvc

// The real token store under the proxy route, end to end (I1).
//
// Every test here runs the WHOLE chain and nothing of it is stubbed: the
// published node is read from a fake database through the service's own policy
// reader (RTDBPolicy), the bundle from the fixture's own source, the tokens'
// statuses from a real creds.RTDBStore over the same database, and the tokens
// themselves travel to a fake bitbucket.org (bitbucketproxy_test.go's
// proxyUpstream). What is asked is what the acceptance states: the narrowest
// covering token serves each repository, a repository none covers is refused
// naming it, a hold on one token does not stop another, an installation that
// holds only the legacy key behaves as it did, and the node's identity set
// reaches the Bitbucket adapter.
//
// The service is configured the way a deployment is — Config.Selector is left
// nil, so NewServer builds the store itself (server.go) — and that is the point
// of these tests: what T8 and T9 pinned with a store a test set is asked again
// here with the store this build wires.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/bbtokens"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The installation these tests serve: four repositories of two workspaces, so
// that every rank of the selection has one that only it reaches.
//
//   - testBitbucketRepo is covered by every token, and by the repository token
//     most narrowly.
//   - proxySiblingRepo is the project token's, whose lookup answers the project
//     its record names.
//   - proxyTokenRepoThird is covered by the project token's workspace pattern
//     too, and the lookup answers ANOTHER project for it — so the workspace
//     token, the next rank down, serves it.
//   - testElsewhereRepo is of the other workspace: none of workspace-a's typed
//     tokens reaches it, and only the legacy record's own coverage does.
const proxyTokenRepoThird = testWorkspace + "/repo-c"

// The two projects the fake Bitbucket answers with, and the three accounts of
// the identity set below. They are placeholders and no credential.
const (
	proxyTokenProjectUUID      = "{00000000-0000-4000-8000-0000000000a1}"
	proxyTokenOtherProjectUUID = "{00000000-0000-4000-8000-0000000000b2}"

	proxyTokenIdentityA        = "{00000000-0000-4000-8000-0000000000c3}"
	proxyTokenIdentityB        = "{00000000-0000-4000-8000-0000000000d4}"
	proxyTokenIdentityOutsider = "{00000000-0000-4000-8000-0000000000e5}"
)

// proxyTokenRepos is every repository the installation admits, in the order the
// node and the tests name them.
func proxyTokenRepos() []string {
	return []string{testBitbucketRepo, proxySiblingRepo, proxyTokenRepoThird, testElsewhereRepo}
}

// ---------------------------------------------------------------------------
// The published node and the bundle
// ---------------------------------------------------------------------------

// proxyTokenEntry is one entry of the node's key, in the node's own spelling.
func proxyTokenEntry(name, kind string, coverage []string, projectUUID, identity string) projectpolicy.BitbucketTokenNode {
	return projectpolicy.BitbucketTokenNode{
		Name: name, Type: kind, Coverage: coverage, ProjectUUID: projectUUID, Identity: identity,
	}
}

// proxyTokenNode is the project's published shared node: the shared coding
// class's own document (the enrollment, the block and the profile admitting all
// four repositories, as proxySharedCodingPolicyDoc's does for its two) with the
// node's bitbucket_tokens key beside the block when the test names any entry
// (R5). A project that names none publishes no key at all, which is the node
// every installation of the release behind holds.
func proxyTokenNode(t *testing.T, entries ...projectpolicy.BitbucketTokenNode) string {
	t.Helper()
	quoted := make([]string, 0, len(proxyTokenRepos()))
	for _, repo := range proxyTokenRepos() {
		quoted = append(quoted, `"`+testCanonical(t, repo)+`"`)
	}
	both := `[` + strings.Join(quoted, ", ") + `]`
	body := `{"provider": "bitbucket", "cpu": 2, "memory": "8Gi", "timeout_class": "6h",
			"allow_repos": ` + both + `}`
	profiles := `"` + projectpolicy.DefaultProfileName + `": ` + body
	if sharedCodingProfile != projectpolicy.DefaultProfileName {
		profiles += `, "` + sharedCodingProfile + `": ` + body
	}
	doc := `{
		"version": 5,
		"budget": ` + testBudgetDoc + `,
		"allow_repos": ` + both + `,
		"accounts": {
			"default": {"allow_repos": ` + both + `},
			"` + testReviewAccount + `": {"allow_repos": ` + both + `}
		},
		"profiles": {` + profiles + `},
		"credentials": {"fallback": "auto"},
		"bitbucket_token": {"allow_repos": ` + both + `, "proxy": true}`
	if len(entries) == 0 {
		return doc + "\n\t}"
	}
	raw, err := json.Marshal(projectpolicy.BitbucketTokens{Entries: entries})
	if err != nil {
		t.Fatalf("the test's own token entries are not JSON: %v", err)
	}
	return doc + `,
		"bitbucket_tokens": ` + string(raw) + `
	}`
}

// proxyTokenBundle is the installation's bundle: the fixture's own document
// plus one entry per token named, and the legacy Bitbucket entry first when the
// test asks for it. The values are returned under the document keys they are in
// the bundle under, so a test holds what bitbucket.org was shown to the token
// that served a call without either value being written down.
func proxyTokenBundle(t *testing.T, legacy bool, names ...string) (doc []byte, values map[string]string) {
	t.Helper()
	values = map[string]string{}
	doc = testBundleDoc(t)
	if legacy {
		value := fakeCred(t, "bitbucket")
		values[config.BitbucketSecret] = value
		doc = append(doc, []byte(config.BitbucketSecret+": "+value+"\n")...)
	}
	for _, name := range names {
		key := config.BitbucketTokenKey(name)
		value := fakeCred(t, name)
		values[key] = value
		doc = append(doc, []byte(key+": "+value+"\n")...)
	}
	return doc, values
}

// ---------------------------------------------------------------------------
// The installation under test
// ---------------------------------------------------------------------------

// proxyTokensWorld is one installation, its fake bitbucket.org, and the bundle
// values its tokens carry.
type proxyTokensWorld struct {
	t      *testing.T
	s      *svc
	up     *proxyUpstream
	store  *creds.RTDBStore
	values map[string]string
}

// proxyTokensWorldOf builds the installation: a shared project job's caller of
// any of the four repositories, the published node the test wrote, the bundle,
// a fake database holding that node, and the service wired from it the way a
// deployment is.
//
// Config.Selector is deliberately left nil: NewServer builds the store from the
// state store it is handed (server.go), and the state store here is a real
// creds.RTDBStore over the fake database — so a service that failed to wire one
// would serve every call with the bundle's legacy entry alone and every
// assertion below about a typed token would fail.
//
// projects is what the fake's project lookup answers one repository of, keyed by
// the repository itself, and the fake answers nothing else.
func proxyTokensWorldOf(t *testing.T, node string, doc []byte, values map[string]string, projects map[string]string) *proxyTokensWorld {
	t.Helper()
	up := newProxyUpstream(t)
	db := gcpfake.NewRTDB(t)
	db.Set(projectpolicy.PathShared(), json.RawMessage(node))
	client, err := rtdb.New(db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatalf("the fake database's client: %v", err)
	}
	repos := map[string]string{}
	canonicals := make([]string, 0, len(proxyTokenRepos()))
	for _, repo := range proxyTokenRepos() {
		canonicals = append(canonicals, testCanonical(t, repo))
		repos[bitbucketSlug(t, repo)] = testCanonical(t, repo)
	}
	store := &creds.RTDBStore{DB: client}
	s := newSvcWith(t, func(s *svc, cfg *Config) {
		cfg.Policy = RTDBPolicy{DB: client}
		cfg.State = store
		cfg.Repos = repos
		cfg.SharedAccounts = []SharedJobAccount{{
			Email: testSharedSA, Profile: sharedCodingProfile, Repos: canonicals,
		}}
		cfg.Client = up.client()
		s.src.docs = [][]byte{doc}
	})
	// The fixture's clock stands at a date in the past, and this route's own
	// halves read two clocks: the selector lapses a hold against the PROCESS's
	// (select.go's lapsed) while the service dates one against its own (s.now).
	// A hold this test plants at the fixture's ground would therefore be lapsed
	// before it was written. Moving the fixture's clock to the process's own —
	// before any token is minted or any call is made — is what makes the two
	// agree, so the mark a 429 leaves is a mark the next request meets.
	s.now = time.Now().UTC().Truncate(time.Second)

	w := &proxyTokensWorld{t: t, s: s, up: up, store: store, values: values}
	w.answers(projects)
	return w
}

// answers makes the fake bitbucket.org answer this installation's two calls:
// the project lookup's repository document and the git advertisement. Every
// other path is a 404, so a call this fixture does not answer is not silently
// served.
func (w *proxyTokensWorld) answers(projects map[string]string) {
	w.up.answer = func(r *http.Request) proxyAnswer {
		switch {
		case strings.HasPrefix(r.URL.Path, "/2.0/repositories/"):
			repo := strings.TrimPrefix(r.URL.Path, "/2.0/repositories/")
			uuid, ok := projects[repo]
			if !ok {
				return proxyAnswer{status: http.StatusNotFound}
			}
			return proxyAnswer{status: http.StatusOK, body: []byte(`{"project":{"uuid":"` + uuid + `"}}`)}
		case strings.HasSuffix(r.URL.Path, "/info/refs"):
			return proxyAnswer{status: http.StatusOK, body: []byte("001e# service=git-upload-pack\n0000")}
		default:
			return proxyAnswer{status: http.StatusNotFound}
		}
	}
}

// hold makes the fake answer one repository's fetch with a 429 naming the wait,
// as bitbucket.org's rate limit does.
func (w *proxyTokensWorld) hold(repo string, retryAfter int) {
	w.up.mu.Lock()
	defer w.up.mu.Unlock()
	previous := w.up.answer
	w.up.answer = func(r *http.Request) proxyAnswer {
		if strings.HasSuffix(r.URL.Path, "/info/refs") && proxyTokenGitRepo(r.URL.Path) == repo {
			h := http.Header{}
			h.Set("Retry-After", strconv.Itoa(retryAfter))
			return proxyAnswer{status: http.StatusTooManyRequests, header: h}
		}
		return previous(r)
	}
}

// proxyTokenGitRepo reads the repository a git path names, out of the path the
// service built for bitbucket.org ("/workspace-a/repo-b.git/info/refs").
func proxyTokenGitRepo(path string) string {
	rest, ok := strings.CutSuffix(strings.TrimPrefix(path, "/"), ".git/info/refs")
	if !ok {
		return ""
	}
	return rest
}

// proxyTokenFetchPath is one repository's git advertisement, as the runner's own
// remote spells it.
func proxyTokenFetchPath(repo string) string {
	return proxyPath(repo + ".git/info/refs?service=git-upload-pack")
}

// fetch sends one fetch of repo as the shared project job's own caller and
// answers the upstream call that served it and the one audit line it left (G7).
// It fails the test when the fetch was not answered 200, or when it made no
// call or more than one: a fetch is served by exactly one upstream call, and a
// project lookup, where one is owed, is counted apart (lookups).
func (w *proxyTokensWorld) fetch(repo string) (proxySeen, map[string]any) {
	w.t.Helper()
	before, lines := w.up.count(), len(w.s.auditLines(w.t))
	resp, ans := w.s.proxySharedCall(w.t, http.MethodGet, proxyTokenFetchPath(repo), nil, bitbucketSlug(w.t, repo))
	if resp.StatusCode != http.StatusOK {
		w.t.Fatalf("the fetch of %s answered %d, want 200 (%s)", repo, resp.StatusCode, ans)
	}
	after := w.s.auditLines(w.t)
	if len(after) != lines+1 {
		w.t.Fatalf("the fetch of %s wrote %d audit lines, want exactly 1", repo, len(after)-lines)
	}
	git := w.gitCalls(before)
	if len(git) != 1 {
		w.t.Fatalf("the fetch of %s reached the upstream %d times, want exactly 1: %+v", repo, len(git), git)
	}
	return git[0], after[len(after)-1]
}

// refuse sends one fetch of repo and answers the refusal it earned and the
// audit line that carries it: the status, the wire error's type and a sentence
// the refusal's own words have to contain. The caller's answer names the class
// and never the repository, and the values of the bundle reach neither the
// answer nor the service's log (G5, C1.13).
func (w *proxyTokensWorld) refuse(repo string, wantStatus int, wantType, wantSentence string) map[string]any {
	w.t.Helper()
	lines := len(w.s.auditLines(w.t))
	resp, ans := w.s.proxySharedCall(w.t, http.MethodGet, proxyTokenFetchPath(repo), nil, bitbucketSlug(w.t, repo))
	if resp.StatusCode != wantStatus {
		w.t.Fatalf("the fetch of %s answered %d, want %d (%s)", repo, resp.StatusCode, wantStatus, ans)
	}
	e := refusal(w.t, resp.StatusCode, ans)
	if e.Error != wantType {
		w.t.Errorf("the refusal's type = %q, want %q", e.Error, wantType)
	}
	if !strings.Contains(e.Message, wantSentence) {
		w.t.Errorf("the refusal's sentence = %q, and it does not contain %q", e.Message, wantSentence)
	}
	for key, value := range w.values {
		for what, text := range map[string]string{
			"the refusal's answer": string(ans),
			"the service's log":    w.s.logs.String(),
		} {
			if strings.Contains(text, value) {
				w.t.Errorf("%s carries the value of the bundle's %s entry", what, key)
			}
		}
	}
	after := w.s.auditLines(w.t)
	if len(after) != lines+1 {
		w.t.Fatalf("the refused fetch of %s wrote %d audit lines, want exactly 1", repo, len(after)-lines)
	}
	return after[len(after)-1]
}

// gitCalls is every git advertisement the fake was asked since a mark, and
// lookups every repository document — the two kinds of call this fixture's
// installation makes.
func (w *proxyTokensWorld) gitCalls(since int) []proxySeen {
	return w.since(since, func(s proxySeen) bool { return strings.HasSuffix(s.path, "/info/refs") })
}

// lookups is every repository document the fake was asked since a mark: the
// project lookup's own call, made with the candidate project token and with no
// other (R2).
func (w *proxyTokensWorld) lookups(since int) []proxySeen {
	return w.since(since, func(s proxySeen) bool { return strings.HasPrefix(s.path, "/2.0/repositories/") })
}

func (w *proxyTokensWorld) since(since int, keep func(proxySeen) bool) []proxySeen {
	var out []proxySeen
	for _, call := range w.up.calls()[since:] {
		if keep(call) {
			out = append(out, call)
		}
	}
	return out
}

// mark is the status the real store holds for one token, and whether it holds
// one at all.
func (w *proxyTokensWorld) mark(name string) (creds.TokenStatus, bool) {
	w.t.Helper()
	all, err := w.store.TokenStatuses(context.Background())
	if err != nil {
		w.t.Fatalf("reading the tokens' statuses: %v", err)
	}
	st, ok := all[name]
	return st, ok
}

// plant puts a status on one token, as the store holds it after the answer the
// mark comes from.
func (w *proxyTokensWorld) plant(name string, st creds.TokenStatus) {
	w.t.Helper()
	if _, err := w.store.UpdateTokenStatus(context.Background(), name, func(creds.TokenStatus, bool) (creds.TokenStatus, error) {
		return st, nil
	}); err != nil {
		w.t.Fatalf("planting the status of %s: %v", name, err)
	}
}

// served asserts that one fetch was served with the token named, by value and
// by document key: the value bitbucket.org was shown, and the name this
// service's own audit line carries (R9 — the line names the token and never its
// value).
func (w *proxyTokensWorld) served(repo, name string, call proxySeen, line map[string]any) {
	w.t.Helper()
	if want := proxyGitAuth(w.values[name]); call.auth != want {
		w.t.Errorf("the fetch of %s was shown %q, want the token %s", repo, call.auth, name)
	}
	if got := auditField(line, "credential"); got != name {
		w.t.Errorf("the fetch of %s left the audit credential %q, want the token's document key %s", repo, got, name)
	}
}

// noTokenTrails asserts that a refusal names no token: its line carries the
// route's own constant (the credential a refusal taken before a token was
// chosen names, R9) and never one of the tokens the bundle holds.
func (w *proxyTokensWorld) noTokenTrails(line map[string]any, names ...string) {
	w.t.Helper()
	if got := auditField(line, "credential"); got != config.BitbucketSecret {
		w.t.Errorf("the refusal's audit credential = %q, want the route's own constant %q: no token served the call", got, config.BitbucketSecret)
	}
	for _, name := range names {
		if strings.Contains(fmt.Sprint(line), name) {
			w.t.Errorf("the refusal's line names the token %s, and no token was chosen: %v", name, line)
		}
	}
}

// ---------------------------------------------------------------------------
// The three tokens, on one installation
// ---------------------------------------------------------------------------

// TestProxyMixedTokensEndToEnd is this task's acceptance on one installation
// holding a repository, a project and a workspace token, all three beside the
// legacy key: every repository is served by the narrowest token that covers it,
// and the repository none of them covers is refused naming the repository.
func TestProxyMixedTokensEndToEnd(t *testing.T) {
	repoToken := config.BitbucketTokenKey("repo")
	projectToken := config.BitbucketTokenKey("proj")
	workspaceToken := config.BitbucketTokenKey("ws")
	legacyToken := config.BitbucketSecret
	node := proxyTokenNode(t,
		proxyTokenEntry(repoToken, "repo", []string{testCanonical(t, testBitbucketRepo)}, "", ""),
		proxyTokenEntry(projectToken, "project",
			[]string{"bitbucket/" + testWorkspace + "/*"}, proxyTokenProjectUUID, proxyTokenIdentityA),
		proxyTokenEntry(workspaceToken, "workspace",
			[]string{"bitbucket/" + testWorkspace + "/*"}, "", proxyTokenIdentityB),
	)
	doc, values := proxyTokenBundle(t, true, "repo", "proj", "ws")
	projects := map[string]string{
		testBitbucketRepo:   proxyTokenProjectUUID,
		proxySiblingRepo:    proxyTokenProjectUUID,
		proxyTokenRepoThird: proxyTokenOtherProjectUUID,
	}

	t.Run("the narrowest covering token serves each repository", func(t *testing.T) {
		w := proxyTokensWorldOf(t, node, doc, values, projects)

		call, line := w.fetch(testBitbucketRepo)
		w.served(testBitbucketRepo, repoToken, call, line)

		since := w.up.count()
		call, line = w.fetch(proxySiblingRepo)
		w.served(proxySiblingRepo, projectToken, call, line)
		// The lookup is the project token's own call: a record is confirmed
		// with the candidate token itself and never with a wider or a narrower
		// one (R2).
		asked := w.lookups(since)
		if len(asked) != 1 {
			t.Fatalf("the sibling's fetch asked %d repository documents, want the project token's one lookup: %+v", len(asked), asked)
		}
		if want := "/2.0/repositories/" + proxySiblingRepo; asked[0].path != want {
			t.Errorf("the lookup asked for %q, want %q", asked[0].path, want)
		}
		if want := "Bearer " + values[projectToken]; asked[0].auth != want {
			t.Errorf("the lookup was shown %q, want the candidate project token", asked[0].auth)
		}

		since = w.up.count()
		call, line = w.fetch(proxyTokenRepoThird)
		w.served(proxyTokenRepoThird, workspaceToken, call, line)
		asked = w.lookups(since)
		if len(asked) != 1 || asked[0].auth != "Bearer "+values[projectToken] {
			t.Fatalf("the third repository asked %+v, want the project token's one lookup and nothing else", asked)
		}

		since = w.up.count()
		call, line = w.fetch(testElsewhereRepo)
		w.served(testElsewhereRepo, legacyToken, call, line)
		if asked := w.lookups(since); len(asked) != 0 {
			t.Errorf("the other workspace's repository asked %+v, want no lookup: no project token covers it", asked)
		}
	})

	t.Run("the repository token's fetch asks no lookup at all", func(t *testing.T) {
		w := proxyTokensWorldOf(t, node, doc, values, projects)
		call, line := w.fetch(testBitbucketRepo)
		w.served(testBitbucketRepo, repoToken, call, line)
		if asked := w.lookups(0); len(asked) != 0 {
			t.Errorf("the fetch asked %+v, want no lookup for a repository a repository token already serves", asked)
		}
	})

	t.Run("each request is counted against the token that served it", func(t *testing.T) {
		w := proxyTokensWorldOf(t, node, doc, values, projects)
		w.fetch(proxySiblingRepo)
		// The count is written after the answer is the caller's (D2356), so it
		// lands a moment after the call returns.
		waitFor(t, "the served token's request to be counted", func() bool {
			st, ok := w.mark(projectToken)
			return ok && st.Count > 0
		})
		for _, name := range []string{repoToken, workspaceToken, legacyToken} {
			if st, ok := w.mark(name); ok && st.Count > 0 {
				t.Errorf("the token %s was counted for a request it did not serve: %+v", name, st)
			}
		}
	})

	t.Run("a repository no token covers is refused naming it", func(t *testing.T) {
		// The same installation without the legacy entry: the block still
		// admits the other workspace's repository — that is the ENROLLMENT, and
		// it says what a run may reach (D2354) — and no token the bundle holds
		// covers it.
		doc, values := proxyTokenBundle(t, false, "repo", "proj", "ws")
		w := proxyTokensWorldOf(t, node, doc, values, projects)
		// The caller is told the class; the repository it was about is the
		// operator's own line, which is where a refusal names what it refused
		// (G7) and is why the class is what the run is answered with.
		line := w.refuse(testElsewhereRepo, http.StatusBadRequest, ErrorRequestRefused, string(bbtokens.ClassNotCovered))
		w.noTokenTrails(line, repoToken, projectToken, workspaceToken)
		if log := w.s.logs.String(); !strings.Contains(log, testElsewhereRepo) {
			t.Errorf("the refusal's own line does not name the repository it refused: %s", log)
		}
		if calls := w.gitCalls(0); len(calls) != 0 {
			t.Errorf("the upstream saw %d calls, and a refused request forwards no byte", len(calls))
		}
	})
}

// TestProxyOnlyLegacyTokenEndToEnd is the other half of the acceptance: an
// installation that holds only the legacy bitbucket-token, whose node carries
// no bitbucket_tokens key at all, is served exactly as it was before this work
// (R3, F14) — and served by the store this build wires, not by the fallback of
// a service whose store could not be built.
//
// The count is what tells the two apart: the store records one request against
// the token that served the call, and a service with no store wired records
// none and reacts to nothing.
func TestProxyOnlyLegacyTokenEndToEnd(t *testing.T) {
	doc, values := proxyTokenBundle(t, true)
	w := proxyTokensWorldOf(t, proxyTokenNode(t), doc, values, nil)
	legacy := config.BitbucketSecret

	call, line := w.fetch(testBitbucketRepo)
	w.served(testBitbucketRepo, legacy, call, line)
	call, line = w.fetch(testElsewhereRepo)
	w.served(testElsewhereRepo, legacy, call, line)
	if asked := w.lookups(0); len(asked) != 0 {
		t.Errorf("the fetches asked %+v, want no lookup: an untyped record is not a project token", asked)
	}

	waitFor(t, "the legacy token's requests to be counted", func() bool {
		st, ok := w.mark(legacy)
		return ok && st.Count == 2
	})
}

// TestProxyThrottleDoesNotStopOtherTokens is R12 and D2356 on the real store: a
// 429 marks the token it was served with and no other, a held token stops
// serving while the repository token beside it keeps serving, and a request
// with no usable token left is answered 429 with the wait.
func TestProxyThrottleDoesNotStopOtherTokens(t *testing.T) {
	repoToken := config.BitbucketTokenKey("repo")
	workspaceToken := config.BitbucketTokenKey("ws")
	node := proxyTokenNode(t,
		proxyTokenEntry(repoToken, "repo", []string{testCanonical(t, testBitbucketRepo)}, "", ""),
		proxyTokenEntry(workspaceToken, "workspace",
			[]string{"bitbucket/" + testWorkspace + "/*"}, "", proxyTokenIdentityB),
	)
	doc, values := proxyTokenBundle(t, false, "repo", "ws")

	t.Run("the answer marks the token that was served, and no other", func(t *testing.T) {
		w := proxyTokensWorldOf(t, node, doc, values, nil)
		w.hold(proxyTokenRepoThird, 60)

		call, line := w.fetch(testBitbucketRepo)
		w.served(testBitbucketRepo, repoToken, call, line)
		if got := auditField(line, "status"); got != StatusOK {
			t.Errorf("the served fetch's line carries status %q, want %q: it was not the call that was held", got, StatusOK)
		}

		resp, ans := w.s.proxySharedCall(t, http.MethodGet, proxyTokenFetchPath(proxyTokenRepoThird), nil, bitbucketSlug(t, proxyTokenRepoThird))
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("the held fetch answered %d, want bitbucket.org's own 429 (%s)", resp.StatusCode, ans)
		}
		// The mark is written before the answer is relayed (D143's order: the
		// request's own line records it and then the answer goes), so it is in
		// the store by the time the caller holds the answer.
		st, ok := w.mark(workspaceToken)
		if !ok || bbtokens.State(st.State) != bbtokens.StateExhausted {
			t.Fatalf("the store holds %+v for the token the 429 was about, want it exhausted", st)
		}
		if want := w.s.now.Add(60 * time.Second); !st.Until.Equal(want) {
			t.Errorf("the hold ends at %v, want %v: the wait the answer named", st.Until, want)
		}
		if other, ok := w.mark(repoToken); ok && !other.Available() {
			t.Errorf("the repository token is marked %+v, and a 429 answers the token it was served with alone", other)
		}
	})

	t.Run("a held token is skipped and the repository token keeps serving", func(t *testing.T) {
		w := proxyTokensWorldOf(t, node, doc, values, nil)
		// The mark a 429 leaves, written directly so that the wait is this
		// test's own (the fixture's clock and the process's are made to agree in
		// proxyTokensWorldOf).
		w.plant(workspaceToken, creds.TokenStatus{
			Mark: creds.Mark{State: creds.StateExhausted, Until: w.s.now.Add(10 * time.Minute)},
		})

		call, line := w.fetch(testBitbucketRepo)
		w.served(testBitbucketRepo, repoToken, call, line)

		// The third repository is the held token's alone, so nothing is left to
		// serve it: the caller is told to come back, and no byte goes upstream.
		line = w.refuse(proxyTokenRepoThird, http.StatusTooManyRequests, ErrorExhaustedUntil,
			"every Bitbucket token covering the repository is throttled")
		w.noTokenTrails(line, repoToken, workspaceToken)
		if calls := w.gitCalls(0); len(calls) != 1 {
			t.Errorf("the upstream saw %d git calls, want the one fetch the repository token served", len(calls))
		}
	})
}

// ---------------------------------------------------------------------------
// The node's identity set and the adapter
// ---------------------------------------------------------------------------

// TestAdapterOwnIdentitySetEndToEnd: the identity set the node carries (F20, R5)
// reaches the Bitbucket adapter over THIS service's own reader of the node, so
// a comment by any account the project's tokens have is the run's own.
//
// The two halves of the chain have their own tests, and this one joins them: the
// runner reads the set and hands it to the opener
// (internal/runner/bitbucket_identities_test.go,
// TestRunnerPassesTheIdentitySetToTheAdapter, T16) and the adapter compares a
// comment's author against it (internal/gitprov/bitbucket/comments_test.go,
// TestSelfUserAcceptsAnyTokenIdentity, T13). What is asked here is the READ the
// service itself makes: the node is published to a fake database and read back
// through RTDBPolicy — the reader the service serves its own tokens from — so an
// entry a decode dropped, or a key the reader refused whole, is what this fails
// on.
//
// The set is partial when any entry names no identity, which is a repository
// access token's record (R14): the adapter must then still mark the accounts the
// node does name, and must not claim it can tell the run's own comments apart
// (SelfKnown), because a wider token may have written one the set does not name.
func TestAdapterOwnIdentitySetEndToEnd(t *testing.T) {
	db := gcpfake.NewRTDB(t)
	client, err := rtdb.New(db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatalf("the fake database's client: %v", err)
	}
	read := func(t *testing.T, entries ...projectpolicy.BitbucketTokenNode) (projectpolicy.BitbucketTokenIdentities, bool) {
		t.Helper()
		db.Set(projectpolicy.PathShared(), json.RawMessage(proxyTokenNode(t, entries...)))
		p, found, err := RTDBPolicy{DB: client}.Shared(context.Background())
		if err != nil || !found {
			t.Fatalf("the service's own reader of the published node: found=%v err=%v", found, err)
		}
		return proxyTokenIdentities(p)
	}
	tokenA := proxyTokenEntry(config.BitbucketTokenKey("a"), "workspace",
		[]string{"bitbucket/" + testWorkspace + "/*"}, "", proxyTokenIdentityA)
	tokenB := proxyTokenEntry(config.BitbucketTokenKey("b"), "workspace",
		[]string{"bitbucket/" + testWorkspace + "/*"}, "", proxyTokenIdentityB)
	repository := proxyTokenEntry(config.BitbucketTokenKey("repo"), "repo",
		[]string{testCanonical(t, testBitbucketRepo)}, "", "")

	for _, tc := range []struct {
		name      string
		entries   []projectpolicy.BitbucketTokenNode
		want      []string
		wantKnown bool
	}{
		{
			name:      "two workspace tokens and their identities",
			entries:   []projectpolicy.BitbucketTokenNode{tokenA, tokenB},
			want:      []string{proxyTokenIdentityA, proxyTokenIdentityB},
			wantKnown: true,
		},
		{
			name:      "a repository record beside them",
			entries:   []projectpolicy.BitbucketTokenNode{tokenA, tokenB, repository},
			want:      []string{proxyTokenIdentityA, proxyTokenIdentityB},
			wantKnown: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			identities, partial := read(t, tc.entries...)
			if len(identities) != len(tc.want) {
				t.Fatalf("the node's identity set is %v, want %v", identities, tc.want)
			}
			for i, want := range tc.want {
				if identities[i] != want {
					t.Errorf("the node's identity set is %v, want %v", identities, tc.want)
				}
			}
			p, err := bitbucket.New(bitbucket.Options{
				Workspace: testWorkspace, Slug: "repo-a", Token: fakeCred(t, "adapter"),
				BaseURL:       proxyTokenAPI(t).URL + "/2.0",
				OwnIdentities: identities, OwnIdentitiesPartial: partial,
			})
			if err != nil {
				t.Fatalf("opening the Bitbucket adapter: %v", err)
			}
			got, err := p.Comments(context.Background(), 1)
			if err != nil {
				t.Fatalf("listing the pull request's comments: %v", err)
			}
			want := map[string]bool{
				proxyTokenIdentityA:        true,
				proxyTokenIdentityB:        true,
				proxyTokenIdentityOutsider: false,
			}
			seen := map[string]bool{}
			for _, c := range got {
				author, ok := strings.CutPrefix(c.Body, "a comment by ")
				if !ok {
					t.Fatalf("the adapter read a comment this test says nothing about: %q", c.Body)
				}
				if c.SelfKnown != tc.wantKnown {
					t.Errorf("the comment by %s: SelfKnown=%v, want %v", author, c.SelfKnown, tc.wantKnown)
				}
				own, known := want[author]
				if !known {
					t.Fatalf("the adapter read a comment this test says nothing about: %q", c.Body)
				}
				if c.Self != own {
					t.Errorf("the comment by %s is the run's own: %v, want %v", author, c.Self, own)
				}
				seen[author] = true
			}
			if len(seen) != len(want) {
				t.Fatalf("the adapter read %d comments, want the %d the fake answers: %v", len(seen), len(want), seen)
			}
		})
	}
}

// proxyTokenIdentities is the identity set the node carries, and whether it is
// known to be partial.
//
// It is the runner's own rule, restated here because the READ is the service's
// (RTDBPolicy) while the runner's is T16's
// (internal/runner/projectpolicy.go's bitbucketOwnIdentities): the set is every
// identity the key names, and it is partial when any entry of the key names none
// — a repository access token has no identity to name (R14), so the set is
// missing an account by design and the adapter may not claim to tell the run's
// own comments apart.
func proxyTokenIdentities(p *projectpolicy.Policy) (projectpolicy.BitbucketTokenIdentities, bool) {
	if p == nil || p.BitbucketTokens == nil {
		return nil, false
	}
	var out projectpolicy.BitbucketTokenIdentities
	partial := false
	for _, entry := range p.BitbucketTokens.Entries {
		if entry.Identity == "" {
			partial = true
			continue
		}
		out = append(out, entry.Identity)
	}
	return out, partial
}

// proxyTokenAPI is the Bitbucket API as the adapter's own two reads see it
// (T13): GET /user, which names one of the node's accounts, and the pull
// request's comments, one per account. It fails the test on any other path, so
// an adapter that started asking for something else is not silently served.
func proxyTokenAPI(t *testing.T) *httptest.Server {
	t.Helper()
	authors := []string{proxyTokenIdentityA, proxyTokenIdentityB, proxyTokenIdentityOutsider}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/user"):
			fmt.Fprintf(w, `{"type":"app_user","display_name":"fugaro-bot","uuid":%q}`, proxyTokenIdentityA)
		case strings.Contains(r.URL.Path, "/pullrequests/1/comments"):
			values := make([]string, 0, len(authors))
			for i, uuid := range authors {
				values = append(values, fmt.Sprintf(
					`{"type":"pullrequest_comment","id":%d,"created_on":"2026-11-01T10:%02d:00+00:00",`+
						`"updated_on":"2026-11-01T10:%02d:00+00:00","content":{"type":"rendered","raw":%q,"markup":"markdown"},`+
						`"user":{"type":"app_user","display_name":"fugaro-bot","uuid":%q},"deleted":false,"pending":false,`+
						`"links":{"html":{"href":"https://bitbucket.org/workspace-a/repo-a/pull-requests/1/_/diff#comment-%d"}}}`,
					101+i, i, i, "a comment by "+uuid, uuid, 101+i))
			}
			fmt.Fprintf(w, `{"pagelen":100,"page":1,"values":[%s]}`, strings.Join(values, ","))
		default:
			t.Errorf("the adapter reached %s, which this fixture does not answer", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}
