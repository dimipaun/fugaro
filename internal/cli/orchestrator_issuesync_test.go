package cli

// The daemon's issue-sync wiring, end to end (B4, D2718–D2720, D2728, D2742).
//
// A whole tick of the real daemon runs here — the seams `newTick` builds, the
// installation's own credentials service reached over HTTP, and the repository
// behind it — so what is pinned is what a running daemon does to the standing
// backlog when a project publishes `issue_sync`: an issue no item carries
// becomes a queued, unapproved item holding a reference and a body's hash and
// never the body (D2720), an item with no issue of its own is mirrored out
// (D2719), the repository is read at most once per cadence however often the
// daemon wakes (D2742), and a repository the installation cannot sign for is one
// line per cadence rather than a silent nothing.
//
// The service is a fake because a test mints no real credential
// (orchestratorIDToken, F19 and O2): what a fake service cannot prove is that the
// installation's daemon account is admitted by the service's own orchestrator
// class, which is the half the plan leaves to the live path (Check 640). What it
// does prove is the half that is the daemon's: the token the mint hands back is
// the one presented on every call of the route, and the App ID every call names
// is the one the installation records.
//
// The repository behind the route is B4's own Fake (internal/issuesync), which is
// the one thing here that models GitHub: the route's acts are served over it, and
// an issue it opens is in the next listing it answers — the fact the mirror's
// adoption turns on. The installation is the label tests' own
// (orchestrator_labels_test.go): the same fixture, the same one repository in its
// config, and the same stand-in for the daemon's mint. What is new here is the
// route behind the URL.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/issuesync"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// issueRepo is the repository these tests sync: the one the fixture's config
// names, which is the label tests' own (labelRepo), and the only repository an
// installation of these tests records.
const issueRepo = labelRepo

// issueAppID is the App the installation records for it. It is the fake
// repository's own App, because that is what makes the issues the fake opened
// this installation's own (AppOpened, D2728) — and what a client that named
// another App, or none, would stop being true of.
const issueAppID = issuesync.FakeAppID

// issueNow is the reading every cycle of these tests is stamped with, which is
// the fixture's database clock: fixed, so that a cadence is asserted by moving
// it rather than by waiting.
var issueNow = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

// issueService is the installation's credentials service as a daemon reaches it:
// the issue route's own path and shapes (creds.IssuePath, creds.IssueRequest,
// creds.IssueAnswer) over one repository, so one tick exercises the whole path —
// the daemon's seams, the client's calls and the route's acts — with neither
// GitHub nor a service behind it.
type issueService struct {
	srv  *httptest.Server
	host *issuesync.Fake

	// mu guards the two records below, which the server's own goroutines write
	// and the test's reads.
	mu sync.Mutex
	// presented is every ID token the service was presented with, in order: the
	// daemon's own identity, which is what the route's orchestrator class is
	// gated on.
	presented []string
	// apps is the App ID each repository's calls named.
	apps map[string]string
}

func newIssueService(t *testing.T, host *issuesync.Fake) *issueService {
	t.Helper()
	s := &issueService{host: host, apps: map[string]string{}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

// serve is one issue call as the route serves it: the act is performed on the
// repository behind the fake, and the answer is the route's own shape.
func (s *issueService) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != creds.IssuePath {
		http.Error(w, "no such route", http.StatusNotFound)
		return
	}
	var req creds.IssueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "the call is not one this route reads", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.presented = append(s.presented, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	s.apps[req.Repository] = req.AppID
	s.mu.Unlock()
	if req.Repository != s.host.Repo {
		http.Error(w, "the repository is not one of this installation's", http.StatusNotFound)
		return
	}
	ctx := r.Context()
	var (
		ans creds.IssueAnswer
		err error
	)
	switch req.Action {
	case creds.IssueActionList:
		var open []issuesync.Issue
		open, err = s.host.ListOpen(ctx)
		for _, i := range open {
			ans.Issues = append(ans.Issues, routeIssue(i))
		}
	case creds.IssueActionOpen:
		var opened issuesync.Issue
		opened, err = s.host.Open(ctx, req.Title, req.Body)
		ans.Issues = append(ans.Issues, routeIssue(opened))
	default:
		// The daemon's sync makes these two acts and no other: `get` is the
		// brief's read and belongs to the launcher (B9), not to this turn.
		http.Error(w, "the call names no act this daemon makes", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ans)
}

// routeIssue is one issue of the repository behind the route as the route
// answers it (creds.Issue): the same fields under the service's own names. The
// translation is the test's and not the daemon's — what the daemon adapts is
// creds.Issue to the sync's (orchestrator_issuesync.go's issueOf) — and it is
// what makes the answer travel the wire as JSON rather than as a struct.
func routeIssue(i issuesync.Issue) creds.Issue {
	return creds.Issue{
		Number: i.Number, Title: i.Title, Body: i.Body, State: i.State,
		AuthorLogin: i.AuthorLogin, AppSlug: i.AppSlug, AppID: i.AppID,
	}
}

// identities is every token the service was presented with.
func (s *issueService) identities() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.presented...)
}

// appID is the App ID the calls for one repository named.
func (s *issueService) appID(repo string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.apps[repo]
}

// issueFixture is one installation whose daemon reaches a fake credentials
// service: the daemon fixture's own cloud, one repository behind the route, and
// the service the issues are read and opened through.
type issueFixture struct {
	*daemonFixture
	service *issueService
	host    *issuesync.Fake
	// mint is the stand-in for the daemon's own gcloud mint.
	mint *orchestratorIdentityMint
}

func newIssueFixture(t *testing.T) *issueFixture {
	t.Helper()
	host := issuesync.NewFake(issueRepo)
	f := &issueFixture{
		daemonFixture: newDaemonFixture(t),
		service:       newIssueService(t, host),
		host:          host,
		mint:          withLabelIdentity(t),
	}
	f.db.SetClock(func() time.Time { return issueNow })
	return f
}

// daemon is the fixture's daemon with the installation's own records naming the
// service: the URL its client is built with and the App ID it signs as, both of
// which a real installation has in its local config and neither of which the
// fixture's config file carries (the file's URL is an https shape, and the fake
// service is loopback http, which is the one other shape the client's check
// admits).
func (f *issueFixture) daemon(t *testing.T) (*orchestratorDaemon, *strings.Builder) {
	t.Helper()
	d, out := f.daemonFixture.daemon(t, "")
	d.lc.CredentialsService = &localcfg.CredentialsService{URL: f.service.srv.URL}
	repo := d.lc.Repos[issueRepo]
	repo.GitHubAppID = issueAppID
	d.lc.Repos[issueRepo] = repo
	holdOrchestratorWorkdir(t, d.lc.GCPProject, true)
	return d, out
}

// open takes the lease and builds the daemon's one tick, which is the tick every
// cycle of a test then runs: the tick's seams carry the sync's own cadence, so a
// tick rebuilt per cycle would read every repository every time.
func (f *issueFixture) open(t *testing.T, d *orchestratorDaemon) {
	t.Helper()
	ctx := context.Background()
	if state, err := d.lease.Acquire(ctx); err != nil || !state.Held {
		t.Fatalf("taking the lease: %+v, %v", state, err)
	}
	tick, err := d.newTick(ctx)
	if err != nil {
		t.Fatalf("building the daemon's tick: %v", err)
	}
	d.tick = tick
}

// step runs one cycle of that tick and fails the test if the cycle itself did
// not finish: an issue the sync could not read is one line in the log and never
// a failed cycle (D2742).
func (f *issueFixture) step(t *testing.T, d *orchestratorDaemon) {
	t.Helper()
	if _, err := d.tick.Step(context.Background()); err != nil {
		t.Fatalf("the tick failed: %v", err)
	}
}

// publishIssueSync publishes the project's policy with the adapter B4 reads: the
// one repository this fixture's installation records, and the mirror switch.
func publishIssueSync(t *testing.T, f *issueFixture, mirror bool) {
	t.Helper()
	publishPolicy(t, f.launchBudgetFixture, fmt.Sprintf(
		`{"version":%d,"issue_sync":{"github":{"repos":["%s"],"mirror":%t}}}`,
		projectpolicy.Version, issueRepo, mirror))
}

// backlog is the standing backlog's node as it now stands, read through the
// daemon's own store: the node the ingest writes and the cycle does not work.
func (f *issueFixture) backlog(t *testing.T, d *orchestratorDaemon) orchestrator.State {
	t.Helper()
	state, _, err := f.backlogStore(t, d).Load(context.Background())
	if err != nil {
		t.Fatalf("reading the standing backlog: %v", err)
	}
	return state
}

// backlogStore is that node's store, built the way the daemon's own seams build
// it (orchestratorIssueSeams).
func (f *issueFixture) backlogStore(t *testing.T, d *orchestratorDaemon) *orchestrator.Node {
	t.Helper()
	node, err := orchestrator.NewNode(orchestrator.NodeOptions{
		DB: d.db, AgentID: d.id, Role: orchestrator.RoleOrchestrator,
		Shard: orchestrator.ShardBacklog, Lease: d.lease,
	})
	if err != nil {
		t.Fatal(err)
	}
	return node
}

// seedBacklog writes the standing backlog's node the way a `fugaro backlog add`
// would have left it: the document is encoded and read back through the node's
// own rules, so a state no release would write cannot pin this test.
func (f *issueFixture) seedBacklog(t *testing.T, d *orchestratorDaemon, s orchestrator.State) {
	t.Helper()
	raw, err := orchestrator.EncodeState(s)
	if err != nil {
		t.Fatalf("the test's backlog is not one this release writes: %v", err)
	}
	if _, err := orchestrator.DecodeState(raw); err != nil {
		t.Fatalf("the test's backlog does not read back: %v", err)
	}
	path, err := orchestrator.PathBacklog(d.id)
	if err != nil {
		t.Fatal(err)
	}
	f.db.Set(path, json.RawMessage(raw))
}

// itemByExternal is the item one external id names, and whether the node carries
// one: the dedupe key is what the ingest is keyed on (D2728).
func itemByExternal(s orchestrator.State, external string) (orchestrator.WorkItem, bool) {
	for _, w := range s.WorkItems {
		if w.External == external {
			return w, true
		}
	}
	return orchestrator.WorkItem{}, false
}

// itemOf is one item by its own id.
func itemOf(s orchestrator.State, id string) (orchestrator.WorkItem, bool) {
	for _, w := range s.WorkItems {
		if w.ID == id {
			return w, true
		}
	}
	return orchestrator.WorkItem{}, false
}

// bodyHashOf is one body's hash the way an item stores it: the plain SHA-256 in
// lower-case hex, computed here rather than read from the code under test.
func bodyHashOf(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// TestTickIngestsAnIssueThroughTheRoute is B4's ingest end to end: an open issue
// of a repository `issue_sync` names, which no item of the project carries the
// external id of, becomes a queued, unapproved item of the standing backlog
// holding the id, the title and the body's SHA-256 — never the body (D2720) —
// and the repository is read at most once per cadence, whatever wakes the tick
// (D2742).
func TestTickIngestsAnIssueThroughTheRoute(t *testing.T) {
	f := newIssueFixture(t)
	const body = "it fell over when I pressed the button\n"
	f.host.Add(issuesync.Issue{Number: 1, Title: "the widget is broken", Body: body, AuthorLogin: "someone"})
	publishIssueSync(t, f, false)
	d, out := f.daemon(t)
	f.open(t, d)

	f.step(t, d)
	state := f.backlog(t, d)
	w, ok := itemByExternal(state, "github:acme/app#1")
	if !ok {
		t.Fatalf("the ingest left the backlog with %d item(s), none of them the issue's: %+v", len(state.WorkItems), state.WorkItems)
	}
	if w.Phase != orchestrator.PhaseQueued {
		t.Errorf("the ingested item is %q, want queued: an issue is work nobody has approved", w.Phase)
	}
	if w.Approved != nil {
		t.Errorf("the ingested item is approved at %v, want unapproved: filing an issue is not approving work", w.Approved)
	}
	if w.Repo != issueRepo {
		t.Errorf("the ingested item is on %q, want the repository the issue is in (%s)", w.Repo, issueRepo)
	}
	if w.Title != "the widget is broken" {
		t.Errorf("the ingested item is titled %q, want the issue's own title", w.Title)
	}
	if w.BodySHA256 != bodyHashOf(body) {
		t.Errorf("the ingested item carries body hash %q, want the issue's body hash %q", w.BodySHA256, bodyHashOf(body))
	}
	if raw := fmt.Sprint(f.db.Value(mustPath(t, d))); strings.Contains(raw, "pressed the button") {
		t.Errorf("the node carries the issue's body, and an item holds a hash and never the text (D2720):\n%s", raw)
	}

	// The daemon presented its own identity and named the App this installation
	// records for the repository, which is the whole of what the service's
	// orchestrator class and its mint are gated on.
	for _, id := range f.service.identities() {
		if id != labelIdentity {
			t.Errorf("the service was presented with %q, want the daemon's own identity", id)
		}
	}
	if got := f.service.appID(issueRepo); got != issueAppID {
		t.Errorf("the calls named App ID %q, want the one this installation records (%s)", got, issueAppID)
	}
	if strings.Contains(out.String(), "could not") {
		t.Errorf("the daemon reported a failure over a repository it read:\n%s", out.String())
	}

	// The cycle after it is the same daemon inside the same minute: the cadence
	// is per repository (D2742), so the listing is not read again and nothing is
	// written.
	f.step(t, d)
	if n := f.host.Lists(); n != 1 {
		t.Errorf("the repository was read %d times in two cycles of the same minute, want once", n)
	}

	// Past the cadence it is read again, and ingests nothing: the issue is here
	// already, which is the dedupe the external id is for.
	f.db.SetClock(func() time.Time { return issueNow.Add(issuesync.Cadence + time.Second) })
	f.step(t, d)
	if n := f.host.Lists(); n != 2 {
		t.Errorf("the repository was read %d times after the cadence passed, want twice", n)
	}
	if again := f.backlog(t, d); len(again.WorkItems) != len(state.WorkItems) {
		t.Errorf("the second turn left %d item(s), want the %d the first wrote: the issue is already an item",
			len(again.WorkItems), len(state.WorkItems))
	}
}

// mustPath is the standing backlog's node path, for the one assertion that reads
// the document as a person would rather than through the store.
func mustPath(t *testing.T, d *orchestratorDaemon) string {
	t.Helper()
	path, err := orchestrator.PathBacklog(d.id)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// TestTickMirrorsAnItemThroughTheRoute is D2719 end to end: an unapproved item
// with no spec and no external id opens an issue of its own, the issue carries
// the item's marker and nothing of the item's brief, and the item takes the
// external id and the body hash of the issue that was opened for it.
//
// The turn after it meets that issue in its own listing and does neither of the
// two wrong things: it does not ingest it as a second item — it is this
// installation's App that opened it, and it carries the marker (D2728) — and it
// does not open a second issue for the item.
func TestTickMirrorsAnItemThroughTheRoute(t *testing.T) {
	f := newIssueFixture(t)
	publishIssueSync(t, f, true)
	d, _ := f.daemon(t)
	f.seedBacklog(t, d, orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		Rev:           1,
		AgentID:       "aurora",
		Role:          orchestrator.RoleOrchestrator,
		WorkItems: []orchestrator.WorkItem{{
			ID: "wi-1", Title: "the widget", Repo: issueRepo,
			Recipe: "default", RigorTier: "small", Brief: "do the thing",
			Phase: orchestrator.PhaseQueued,
		}},
	})
	f.open(t, d)

	f.step(t, d)
	opened := f.host.Opened()
	if len(opened) != 1 {
		t.Fatalf("the mirror opened %d issue(s), want one for the item", len(opened))
	}
	if !strings.Contains(opened[0].Body, issuesync.Marker("wi-1")) {
		t.Errorf("the issue the mirror opened carries %q, want the item's marker", opened[0].Body)
	}
	if strings.Contains(opened[0].Body, "do the thing") {
		t.Errorf("the issue the mirror opened carries the item's brief, and a brief is not text anybody asked to publish: %q", opened[0].Body)
	}
	state := f.backlog(t, d)
	w, ok := itemOf(state, "wi-1")
	if !ok {
		t.Fatalf("the backlog lost the item the mirror wrote back to")
	}
	if want := issuesync.ExternalID(issueRepo, opened[0].Number); w.External != want {
		t.Errorf("the item carries %q, want the issue the mirror opened (%q)", w.External, want)
	}
	if w.BodySHA256 != bodyHashOf(opened[0].Body) {
		t.Errorf("the item carries body hash %q, want the hash of the issue the mirror opened", w.BodySHA256)
	}
	if len(state.WorkItems) != 1 {
		t.Errorf("the backlog carries %d item(s), want the one the mirror wrote back to: the issue the App opened is that item's other face",
			len(state.WorkItems))
	}

	// The next turn reads the mirror's own issue and leaves both alone.
	f.db.SetClock(func() time.Time { return issueNow.Add(issuesync.Cadence + time.Second) })
	f.step(t, d)
	if n := f.host.Opens(); n != 1 {
		t.Errorf("the mirror opened %d issues in two turns, want one: the issue it opened is the item's own", n)
	}
	if again := f.backlog(t, d); len(again.WorkItems) != 1 {
		t.Errorf("the second turn left %d item(s), want one: an issue this installation's App opened is not ingested (D2728)",
			len(again.WorkItems))
	}
}

// TestIssueSeamsFollowTheInstallationAndThePolicy is what the wiring turns on:
// the seams are built from the project's published `issue_sync` and from the
// installation's own records, and any of them missing is no seams at all —
// which is the state every installation that has not opted in is in, and the
// state a tick with no seams ingests nothing in and says nothing about.
//
// The last case is the one that is a line and not a silence: a project that
// names a repository this installation records no GitHub App for is a repository
// the credentials service could not sign a call for, and the daemon says so once
// per cadence rather than reading nothing quietly.
func TestIssueSeamsFollowTheInstallationAndThePolicy(t *testing.T) {
	t.Run("no policy", func(t *testing.T) {
		f := newIssueFixture(t)
		d, _ := f.daemon(t)
		seams, err := d.orchestratorIssueSeams(context.Background())
		if err != nil {
			t.Fatalf("building the seams: %v", err)
		}
		if seams.Set() {
			t.Errorf("a project that published no policy has issue seams: %+v", seams)
		}
	})

	t.Run("no orchestrator block", func(t *testing.T) {
		f := newIssueFixture(t)
		publishIssueSync(t, f, false)
		d, _ := f.daemon(t)
		holdOrchestratorWorkdir(t, d.lc.GCPProject, false)
		seams, err := d.orchestratorIssueSeams(context.Background())
		if err != nil {
			t.Fatalf("building the seams: %v", err)
		}
		if seams.Set() {
			t.Errorf("an installation that holds no orchestrator block has issue seams: %+v", seams)
		}
	})

	t.Run("no credentials service", func(t *testing.T) {
		f := newIssueFixture(t)
		publishIssueSync(t, f, false)
		d, _ := f.daemon(t)
		d.lc.CredentialsService = nil
		seams, err := d.orchestratorIssueSeams(context.Background())
		if err != nil {
			t.Fatalf("building the seams: %v", err)
		}
		if seams.Set() {
			t.Errorf("an installation with no credentials service has issue seams: %+v", seams)
		}
	})

	t.Run("a policy naming a repository", func(t *testing.T) {
		f := newIssueFixture(t)
		publishIssueSync(t, f, true)
		d, _ := f.daemon(t)
		seams, err := d.orchestratorIssueSeams(context.Background())
		if err != nil {
			t.Fatalf("building the seams: %v", err)
		}
		if !seams.Set() {
			t.Fatalf("a project that published issue_sync has no issue seams: %+v", seams)
		}
		runner, ok := seams.Sync.(*issuesync.Runner)
		if !ok {
			t.Fatalf("the sync is %T, want the runner the wiring builds", seams.Sync)
		}
		if _, ok := runner.Adapters[issueRepo]; !ok {
			t.Errorf("the runner carries adapters for %v, want one for the repository the policy names", adapterRepos(runner))
		}
		if got := runner.AppIDs[issueRepo]; got != issueAppID {
			t.Errorf("the runner holds App ID %q for %s, want the one this installation records (%s)", got, issueRepo, issueAppID)
		}
		if !runner.Mirror {
			t.Errorf("the runner's mirror is off over a policy that wrote it on")
		}
	})

	t.Run("a repository the installation cannot sign for", func(t *testing.T) {
		f := newIssueFixture(t)
		// A repository this installation records no provider for, which is the
		// one shape a call could not be signed for: the service mints its token
		// as the App the installation recorded, and there is none.
		publishPolicy(t, f.launchBudgetFixture, fmt.Sprintf(
			`{"version":%d,"issue_sync":{"github":{"repos":["acme/lib"]}}}`, projectpolicy.Version))
		d, out := f.daemon(t)
		f.open(t, d)
		f.step(t, d)

		if n := f.host.Lists(); n != 0 {
			t.Errorf("the repository behind the route was read %d time(s) for a call that could not be signed", n)
		}
		if !strings.Contains(out.String(), "acme/lib") {
			t.Errorf("the daemon did not say which repository it could not read:\n%s", out.String())
		}
		if raw := f.db.Value(mustPath(t, d)); raw != nil {
			t.Errorf("a turn that read nothing wrote the backlog: %v", raw)
		}
	})
}

// adapterRepos is the repositories a runner carries an adapter for, in order, so
// that a failed assertion says what it found rather than only that it failed.
func adapterRepos(r *issuesync.Runner) []string {
	out := make([]string, 0, len(r.Adapters))
	for repo := range r.Adapters {
		out = append(out, repo)
	}
	return out
}
