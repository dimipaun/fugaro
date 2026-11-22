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
	"bytes"
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
// node's bitbucket_tokens key beside the legacy block when the test names any
// entry (R5). A project that names none publishes no key at all, which is the
// node every installation of the release behind holds.
func proxyTokenNode(t *testing.T, entries ...projectpolicy.BitbucketTokenNode) string {
	t.Helper()
	return proxyTokenNodeDoc(t, true, entries...)
}

// proxyTokenTypedOnlyNode is proxyTokenNode's document without the legacy
// bitbucket_token block: the node of the project that moved to typed tokens
// entirely (R3), whose identity set names every account the run may write as
// because no untyped record serves beside the entries.
func proxyTokenTypedOnlyNode(t *testing.T, entries ...projectpolicy.BitbucketTokenNode) string {
	t.Helper()
	return proxyTokenNodeDoc(t, false, entries...)
}

func proxyTokenNodeDoc(t *testing.T, legacy bool, entries ...projectpolicy.BitbucketTokenNode) string {
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
		"version": 9,
		"budget": ` + testBudgetDoc + `,
		"allow_repos": ` + both + `,
		"accounts": {
			"default": {"allow_repos": ` + both + `},
			"` + testReviewAccount + `": {"allow_repos": ` + both + `}
		},
		"profiles": {` + profiles + `},
		"credentials": {"fallback": "auto"}`
	if legacy {
		doc += `,
		"bitbucket_token": {"allow_repos": ` + both + `, "proxy": true}`
	}
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
	db     *gcpfake.RTDB
	store  *creds.RTDBStore
	values map[string]string
	// clock is the bundle loader's own, so that a test can let the parsed
	// document lapse without moving the service's clock or the process's.
	clock time.Time
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
	var loader *BundleLoader
	s := newSvcWith(t, func(s *svc, cfg *Config) {
		loader = cfg.Bundles
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
	// The run id is moved with the clock and for a second reason: the identity
	// routes date a run by the launch day its id carries and refuse one whose
	// launch is further back than maxRunDays (ticketRunEnded). The fixture's id
	// is built from testNow(), so a world that moved the clock and left the id
	// names a launch that walks out of that window once the wall clock is
	// maxRunDays past testNow(), and every call of this world is then answered
	// as a run that is over. One world's clock and one world's id move together.
	s.run = s.now.Format("20060102-150405") + "-ab12"

	w := &proxyTokensWorld{t: t, s: s, up: up, db: db, store: store, values: values, clock: s.now}
	// The bundle loader's own clock, and not the service's: a test that rotates
	// a value has to let the loader's parsed document lapse (rotate), and doing
	// that by moving the service's clock would be moving every hold and every
	// expiry this installation dates at the same time.
	loader.Now = func() time.Time { return w.clock }
	w.answers(projects)
	return w
}

// answers makes the fake bitbucket.org answer this installation's two calls:
// the project lookup's repository document and the git advertisement. Every
// other path is a 404, so a call this fixture does not answer is not silently
// served.
func (w *proxyTokensWorld) answers(projects map[string]string) {
	w.up.mu.Lock()
	defer w.up.mu.Unlock()
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

// rotate replaces one bundle entry's value, as an operator rotating a token
// does: `creds set` writes the container, and the next request that reads it is
// served the new value. The document is rewritten whole and the loader's clock
// is moved past BundleCacheTTL, because the loader serves the document it
// parsed for that long and no read before it (C1.25's rotation is picked up
// quickly and not sooner) — a test that changed the container and kept asking
// would be asking which token the OLD document holds.
//
// values is updated with the document, so what a test compares bitbucket.org's
// own answers against is the value the bundle holds now.
func (w *proxyTokensWorld) rotate(key, value string) {
	w.t.Helper()
	old, ok := w.values[key]
	if !ok {
		w.t.Fatalf("the bundle holds no %s entry to rotate", key)
	}
	was := w.s.src.docs[0]
	rotated := bytes.Replace(was, []byte(key+": "+old+"\n"), []byte(key+": "+value+"\n"), 1)
	if bytes.Equal(rotated, was) {
		w.t.Fatalf("the bundle's document holds no %s entry, so this rotation changed nothing", key)
	}
	w.values[key] = value
	w.s.src.docs = [][]byte{rotated}
	w.clock = w.clock.Add(BundleCacheTTL + time.Second)
}

// unauthorized makes the fake refuse this installation's every call with 401:
// the git advertisement, and the repository document the route's own
// confirmation of a 401 reads (confirmBitbucket401). Both, because a 401 the
// repository read does not confirm is not a dead token at all.
func (w *proxyTokensWorld) unauthorized() {
	w.up.mu.Lock()
	defer w.up.mu.Unlock()
	w.up.answer = func(*http.Request) proxyAnswer {
		return proxyAnswer{status: http.StatusUnauthorized}
	}
}

// breakStatus writes a token status this release cannot decode under the name
// given, which is what one node the release did not write does to the read: the
// subtree is decoded strictly and whole (creds.RTDBStore.TokenStatuses), so
// this one is enough to fail the read for every token there is.
func (w *proxyTokensWorld) breakStatus(name string) {
	w.db.Set(creds.PathTokenStatus(name), json.RawMessage(`{"state": "available", "unknown_to_this_release": true}`))
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

// TestProxyRotatedValueReplacesTheLookup is the rotation half of the memoized
// lookup: the instance a selection's project answers are kept on is keyed by
// the two documents the selection is made from — the node's records AND the
// bundle's values (documentsKey) — because the lookup HOLDS a credential.
//
// An instance that survived a rotation would present a token's retired value to
// bitbucket.org for as long as its answers live (ProjectLookupTTL), and a
// retired value is answered 403: the lookup reads that as "this token does not
// reach the repository", the selection drops the project token for a wider one
// or, with none left, refuses the call — for ten minutes, with nothing else
// failing and no line saying why. The value is hashed into the key and never
// held in it, so the key is the whole of what a test can ask about: this makes
// the value change and asks what the next request's lookup does.
func TestProxyRotatedValueReplacesTheLookup(t *testing.T) {
	projectToken := config.BitbucketTokenKey("proj")
	node := proxyTokenNode(t, proxyTokenEntry(projectToken, "project",
		[]string{"bitbucket/" + testWorkspace + "/*"}, proxyTokenProjectUUID, proxyTokenIdentityA))
	doc, values := proxyTokenBundle(t, false, "proj")
	w := proxyTokensWorldOf(t, node, doc, values, map[string]string{proxySiblingRepo: proxyTokenProjectUUID})

	call, line := w.fetch(proxySiblingRepo)
	w.served(proxySiblingRepo, projectToken, call, line)
	first := w.lookups(0)
	if len(first) != 1 || first[0].auth != "Bearer "+values[projectToken] {
		t.Fatalf("the fetch asked %+v, want exactly the project lookup, made with the token itself (R2)", first)
	}

	rotated := fakeCred(t, "proj-rotated")
	w.rotate(projectToken, rotated)

	// The answer the OLD instance holds — this repository's project, held for
	// ProjectLookupTTL — must not be served under the new value either: an
	// answer read off a credential that is gone is no answer about the one the
	// next call carries, and the operator's rotation has to take effect at the
	// next request rather than in ten minutes.
	since := w.up.count()
	call, line = w.fetch(proxySiblingRepo)
	w.served(proxySiblingRepo, projectToken, call, line)
	asked := w.lookups(since)
	if len(asked) != 1 {
		t.Fatalf("the fetch after the rotation asked %+v, want exactly one lookup: the instance holding the retired value must not answer for the rotated one", asked)
	}
	if asked[0].auth != "Bearer "+rotated {
		t.Errorf("the lookup after the rotation was shown %q, want the value the bundle holds now", asked[0].auth)
	}
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

// TestProxyLegacyRecordIsNeverMarked: the legacy untyped record takes no mark
// and no confirmation read, and keeps answering as it did before this store was
// wired (R3, T8's legacy-only acceptance).
//
// It is the one record this route may not mark, and the reason is not that it
// is special in what it is: a mark is a state this release has nothing to clear
// with (`creds set` writes an added token's identity and verification time and
// never its state, `accounts clear` touches the model credentials' marks), so a
// token marked broken stays broken until an operator edits the state store by
// hand. Every other record can be put out of the way by republishing the node
// without it; the legacy record is implicit and is therefore a candidate for
// every repository its allow_repos list admits whatever the node says. Marked
// once, an installation holding only it would refuse every Bitbucket call of
// every run with a class no operator step clears.
func TestProxyLegacyRecordIsNeverMarked(t *testing.T) {
	legacy := config.BitbucketSecret

	t.Run("a 401 is answered by bitbucket.org and marks nothing", func(t *testing.T) {
		doc, values := proxyTokenBundle(t, true)
		w := proxyTokensWorldOf(t, proxyTokenNode(t), doc, values, nil)
		w.unauthorized()

		before := w.up.count()
		resp, ans := w.s.proxySharedCall(t, http.MethodGet, proxyTokenFetchPath(testBitbucketRepo), nil, bitbucketSlug(t, testBitbucketRepo))
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("the fetch answered %d, want bitbucket.org's own 401 (%s)", resp.StatusCode, ans)
		}
		if reads := w.lookups(before); len(reads) != 0 {
			t.Errorf("the 401 made %d reads of a repository, want none: a token this route does not mark is not confirmed either", len(reads))
		}
		if st, ok := w.mark(legacy); ok && !statusOf(st).Available() {
			t.Errorf("the legacy record is marked %+v, and nothing in this release clears a mark", st)
		}

		// The call after it is made again, and refused by bitbucket.org rather
		// than by a mark: that is what "keeps working with no step" means for
		// the installation whose only token this is.
		since := w.up.count()
		resp, ans = w.s.proxySharedCall(t, http.MethodGet, proxyTokenFetchPath(testBitbucketRepo), nil, bitbucketSlug(t, testBitbucketRepo))
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("the second fetch answered %d, want bitbucket.org's own 401 again (%s)", resp.StatusCode, ans)
		}
		if calls := w.gitCalls(since); len(calls) != 1 {
			t.Errorf("the second fetch reached the upstream %d times, want exactly 1: nothing this route wrote refuses a call of the legacy record", len(calls))
		}
	})

	t.Run("a 429 is relayed and holds nothing", func(t *testing.T) {
		doc, values := proxyTokenBundle(t, true)
		w := proxyTokensWorldOf(t, proxyTokenNode(t), doc, values, nil)
		w.hold(testBitbucketRepo, 60)

		resp, ans := w.s.proxySharedCall(t, http.MethodGet, proxyTokenFetchPath(testBitbucketRepo), nil, bitbucketSlug(t, testBitbucketRepo))
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("the fetch answered %d, want bitbucket.org's own 429 (%s)", resp.StatusCode, ans)
		}
		st, ok := w.mark(legacy)
		if ok && !statusOf(st).Available() {
			t.Errorf("the legacy record is held %+v, want no hold of any kind", st)
		}

		// A held token is skipped by the selection, so a mark here would turn
		// the next call into this service's own 429 with a Retry-After instead
		// of the call it was: the wait nothing supports is the promise the
		// upstream's own answer alone can make.
		since := w.up.count()
		resp, ans = w.s.proxySharedCall(t, http.MethodGet, proxyTokenFetchPath(testBitbucketRepo), nil, bitbucketSlug(t, testBitbucketRepo))
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("the second fetch answered %d, want bitbucket.org's own 429 (%s)", resp.StatusCode, ans)
		}
		if strings.Contains(string(ans), ErrorExhaustedUntil) {
			t.Errorf("the second fetch was answered this service's own refusal (%s), want the upstream's answer relayed", ans)
		}
		if calls := w.gitCalls(since); len(calls) != 1 {
			t.Errorf("the second fetch reached the upstream %d times, want exactly 1: the legacy record is never held", len(calls))
		}
	})
}

// TestProxyUnreadableStatusIsNotACoverageRefusal: the tokens' statuses are read
// as ONE subtree (creds.RTDBStore.TokenStatuses), and a node this release
// cannot decode fails that read for every token — so the failure is the store's
// and not a repository no token covers. The difference is what the operator is
// sent to look at: token_not_covered points at the node's coverage and is
// answered 400, and a transient database error or one hand-edited node under a
// name no token is would be presented as exactly that, for every repository of
// every run, with nothing retried.
func TestProxyUnreadableStatusIsNotACoverageRefusal(t *testing.T) {
	t.Run("a store that cannot answer is state_unavailable", func(t *testing.T) {
		doc, values := proxyTokenBundle(t, true)
		w := proxyTokensWorldOf(t, proxyTokenNode(t), doc, values, nil)
		w.breakStatus("bitbucket-token--stray")

		line := w.refuse(testBitbucketRepo, http.StatusServiceUnavailable, ErrorStateUnavailable,
			"the Bitbucket token could not be chosen")
		w.noTokenTrails(line)
		if calls := w.gitCalls(0); len(calls) != 0 {
			t.Errorf("the upstream saw %d git calls, and a request no token could be chosen for forwards no byte", len(calls))
		}
	})

	t.Run("a bundle holding no token is refused for the bundle alone", func(t *testing.T) {
		// Whether this installation holds a Bitbucket token at all is the
		// bundle's fact and not the store's, so this answer does not change
		// with the store being unreachable.
		doc, values := proxyTokenBundle(t, false)
		w := proxyTokensWorldOf(t, proxyTokenNode(t), doc, values, nil)
		w.breakStatus("bitbucket-token--stray")

		w.refuse(testBitbucketRepo, http.StatusBadRequest, ErrorRequestRefused, string(bbtokens.ClassNoToken))
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

// TestProxyMarkKeepsTheFactsBesideIt: a mark is the route's half of a token's
// status — the state and the hold — and the facts beside it are the store's own
// and the writers that own them (R12, D2356).
//
// The subtree fails to decode for EVERY token while any one node under it does
// not (creds.RTDBStore.TokenStatuses), so the status a mark is written over is
// one the route may not have been able to read at all — a read that fails is a
// read that answers nothing, and a mark built to carry the identity and the
// verification time from it would write zeroes over both, silently, for a token
// whose own node is perfectly readable. What is asserted here is what the store
// says after a 429: the hold, and the identity, the verification time and the
// count it held before the mark.
func TestProxyMarkKeepsTheFactsBesideIt(t *testing.T) {
	repoToken := config.BitbucketTokenKey("repo")
	node := proxyTokenNode(t, proxyTokenEntry(repoToken, "repo",
		[]string{testCanonical(t, testBitbucketRepo)}, "", ""))
	doc, values := proxyTokenBundle(t, false, "repo")
	w := proxyTokensWorldOf(t, node, doc, values, nil)

	verified := w.s.now.Add(-time.Hour)
	w.plant(repoToken, creds.TokenStatus{
		VerifiedAt: verified,
		Identity:   proxyTokenIdentityA,
		Count:      7,
		Window:     w.s.now,
	})
	w.hold(testBitbucketRepo, 60)

	resp, ans := w.s.proxySharedCall(t, http.MethodGet, proxyTokenFetchPath(testBitbucketRepo), nil, bitbucketSlug(t, testBitbucketRepo))
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("the held fetch answered %d, want bitbucket.org's own 429 (%s)", resp.StatusCode, ans)
	}
	st, ok := w.mark(repoToken)
	if !ok {
		t.Fatalf("the store holds no status for the token the 429 was about")
	}
	if bbtokens.State(st.State) != bbtokens.StateExhausted {
		t.Errorf("the token's state is %q, want the mark the 429 left", st.State)
	}
	if want := w.s.now.Add(60 * time.Second); !st.Until.Equal(want) {
		t.Errorf("the hold ends at %v, want %v: the wait the answer named", st.Until, want)
	}
	if st.Identity != proxyTokenIdentityA {
		t.Errorf("the mark left the token's identity %q, want the store's own %q", st.Identity, proxyTokenIdentityA)
	}
	if !st.VerifiedAt.Equal(verified) {
		t.Errorf("the mark left the token verified at %v, want the store's own %v", st.VerifiedAt, verified)
	}
	// The count is written after the answer is the caller's (D2356), so this
	// reads the store's own 7 where the count has not landed yet and the 7 plus
	// the request just served where it has.
	if st.Count < 7 {
		t.Errorf("the mark reset the token's count to %d, want the store's own 7 and never the zero a fresh status carries", st.Count)
	}
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
// The set is partial when the node cannot name every account the run may write
// as — an entry that names no identity (a repository access token's, R14) or the
// legacy record serving beside the typed ones (R3) — and the adapter must then
// still mark the accounts the node does name, while not claiming it can tell the
// run's own comments apart (SelfKnown), because a wider token may have written
// one the set does not name.
func TestAdapterOwnIdentitySetEndToEnd(t *testing.T) {
	db := gcpfake.NewRTDB(t)
	client, err := rtdb.New(db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatalf("the fake database's client: %v", err)
	}
	read := func(t *testing.T, legacy bool, entries ...projectpolicy.BitbucketTokenNode) (projectpolicy.BitbucketTokenIdentities, bool) {
		t.Helper()
		node := proxyTokenNode(t, entries...)
		if !legacy {
			node = proxyTokenTypedOnlyNode(t, entries...)
		}
		db.Set(projectpolicy.PathShared(), json.RawMessage(node))
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
		legacy    bool
		entries   []projectpolicy.BitbucketTokenNode
		want      []string
		wantKnown bool
	}{
		{
			// The node carries the legacy block beside its entries: the
			// selector may still serve a call with the untyped record, whose
			// account no entry names, so the set is partial (R3, R12).
			name:      "two workspace tokens and their identities",
			legacy:    true,
			entries:   []projectpolicy.BitbucketTokenNode{tokenA, tokenB},
			want:      []string{proxyTokenIdentityA, proxyTokenIdentityB},
			wantKnown: false,
		},
		{
			name:      "a repository record beside them",
			legacy:    true,
			entries:   []projectpolicy.BitbucketTokenNode{tokenA, tokenB, repository},
			want:      []string{proxyTokenIdentityA, proxyTokenIdentityB},
			wantKnown: false,
		},
		{
			// The installation that moved to typed tokens entirely: no legacy
			// record serves beside the entries, so the set names every account
			// the run may write as.
			name:      "typed records alone",
			entries:   []projectpolicy.BitbucketTokenNode{tokenA, tokenB},
			want:      []string{proxyTokenIdentityA, proxyTokenIdentityB},
			wantKnown: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			identities, partial := read(t, tc.legacy, tc.entries...)
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
// It asks the rule's one home (projectpolicy.BitbucketOwnIdentities) for the
// value it read back, so what this test pins is what the runner computes for the
// same node (T16, internal/runner/projectpolicy.go's bitbucketOwnIdentities) and
// not a copy of it that could answer differently.
func proxyTokenIdentities(p *projectpolicy.Policy) (projectpolicy.BitbucketTokenIdentities, bool) {
	if p == nil {
		return projectpolicy.BitbucketOwnIdentities(nil, nil)
	}
	var entries []projectpolicy.BitbucketTokenNode
	if p.BitbucketTokens != nil {
		entries = p.BitbucketTokens.Entries
	}
	return projectpolicy.BitbucketOwnIdentities(entries, p.BitbucketToken)
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
