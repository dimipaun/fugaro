package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// The daemon's label wiring, end to end (T7, docs/forge-labels).
//
// A whole tick of the real daemon runs here — the seams `newTick` builds, the
// installation's own credentials service reached over HTTP, and the fake host
// behind it — so what is pinned is what a running daemon does to the pull
// requests of the runs it launched: a running item's labelled with its status
// and the spec it came from, a failed one's relabelled, a merged one cleared,
// a retry's first pull request left as it ended, and a cycle with nothing to
// change writing nothing at all.
//
// The service is a fake because a test cannot stand up the installation's own
// Cloud Run revision: what a fake service cannot prove is that the
// installation's daemon account is admitted by the service's own orchestrator
// class, which is the live half of F19 and O2 — the class itself, its env
// plumbing and the Terraform that renders it are pinned in internal/credsvc,
// internal/cli's credentials_serve_test.go and internal/infra/tf.

// The three runs these tests work with, and the label names they are about.
// The names are the syncer's own (internal/orchestrator's labelRunning and
// labelFailed) spelled out rather than reached for: a test that read them from
// the package under test would pin nothing.
const (
	labelRunA = "20261116-120000-a1b2"
	labelRunB = "20261116-120001-c3d4"
	labelRunC = "20261116-120002-e5f6"

	labelRunning = "fugaro:running"
	labelFailed  = "fugaro:failed"
	labelSpec    = "spec:forge-labels"
)

// labelRepo is the repository the fixture's config names and every item of
// these tests is on, and labelAppID is the App the installation records for it:
// a number and not a secret, and the one thing a label call names beyond the
// act (the service signs its own token as that App).
const (
	labelRepo  = "acme/app"
	labelAppID = "123456"
)

// labelIdentity is the ID token the daemon presents to the service. Nothing
// mints it and nothing outside these tests reads it: the service that verifies
// one is not running here.
const labelIdentity = "the-daemon-own-identity"

// withLabelIdentity stands in for the one process this path reaches that a test
// must not run: `gcloud auth print-identity-token`, which is how the daemon
// obtains its own identity by impersonating the installation's orchestrator
// account (orchestrator_identity.go). Everything else of that seam is real — the
// argv it builds, the account it composes and the refusal it writes — so what a
// test replaces is the child process and not the identity (orchestrator_
// identity_test.go pins the rest).
func withLabelIdentity(t *testing.T) {
	t.Helper()
	prev := orchestratorServiceIdentity
	orchestratorServiceIdentity = func(context.Context, string, string) (string, error) { return labelIdentity, nil }
	t.Cleanup(func() { orchestratorServiceIdentity = prev })
}

// labelService is the installation's credentials service as a daemon reaches
// it: the route's own path and shapes (creds.LabelPath, creds.LabelRequest,
// creds.LabelAnswer) over the fake hosts a test seeded, so one tick exercises
// the whole path — the daemon's seams, the client's own calls and the route's
// five acts — with neither a forge nor a service behind it.
type labelService struct {
	srv   *httptest.Server
	hosts map[string]*fake.Provider

	// mu guards the two records below, which the server's own goroutines write
	// and the test's reads.
	mu sync.Mutex
	// presented is every ID token the service was presented with, in order:
	// the daemon's own identity, which is what the route's orchestrator class
	// is gated on.
	presented []string
	// apps is the App ID each repository's calls named.
	apps map[string]string
}

func newLabelService(t *testing.T, hosts ...*fake.Provider) *labelService {
	t.Helper()
	s := &labelService{hosts: map[string]*fake.Provider{}, apps: map[string]string{}}
	for _, h := range hosts {
		s.hosts[h.Repo] = h
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

// serve is one label call as the route serves it: the act is performed on the
// host the call names, and the answer is the route's own shape.
func (s *labelService) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != creds.LabelPath {
		http.Error(w, "no such route", http.StatusNotFound)
		return
	}
	var req creds.LabelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "the call is not one this route reads", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.presented = append(s.presented, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	s.apps[req.Repository] = req.AppID
	s.mu.Unlock()
	host, ok := s.hosts[req.Repository]
	if !ok {
		http.Error(w, "the repository is not one of this installation's", http.StatusNotFound)
		return
	}
	ans, err := s.act(r.Context(), host, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ans)
}

// act is the route's five acts over one fake host, which is what the real
// service does through the host's own adapter: a listing of the open pull
// requests, one pull request's state and labels whatever its state, a label
// created, and one name added to or removed from one pull request.
//
// The write acts are one name each because that is what the route's request
// carries (creds.LabelRequest: "it is one label per call rather than a set"),
// so a set of names is a set of calls here exactly as it is over the wire.
func (s *labelService) act(ctx context.Context, host *fake.Provider, req creds.LabelRequest) (creds.LabelAnswer, error) {
	switch req.Action {
	case creds.LabelActionList:
		open, err := host.ListOpenPRLabels(ctx)
		if err != nil {
			return creds.LabelAnswer{}, err
		}
		numbers := make([]int, 0, len(open))
		for n := range open {
			numbers = append(numbers, n)
		}
		slices.Sort(numbers)
		prs := make([]creds.LabelPullRequest, 0, len(numbers))
		for _, n := range numbers {
			prs = append(prs, creds.LabelPullRequest{Number: n, State: string(gitprov.PROpen), Labels: open[n]})
		}
		return creds.LabelAnswer{PullRequests: prs}, nil
	case creds.LabelActionGet:
		state, labels, err := host.PRLabels(ctx, req.Number)
		if err != nil {
			return creds.LabelAnswer{}, err
		}
		return creds.LabelAnswer{PullRequests: []creds.LabelPullRequest{
			{Number: req.Number, State: string(state), Labels: labels},
		}}, nil
	case creds.LabelActionEnsure:
		return creds.LabelAnswer{}, host.EnsureLabel(ctx, req.Name, "", "")
	case creds.LabelActionAdd:
		return creds.LabelAnswer{}, host.AddLabels(ctx, req.Number, req.RunID, []string{req.Name})
	case creds.LabelActionRemove:
		return creds.LabelAnswer{}, host.RemoveLabels(ctx, req.Number, req.RunID, []string{req.Name})
	}
	return creds.LabelAnswer{}, fmt.Errorf("the call names no act this route performs: %q", req.Action)
}

// identities is every token the service was presented with.
func (s *labelService) identities() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.presented)
}

// appID is the App ID the calls for one repository named.
func (s *labelService) appID(repo string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.apps[repo]
}

// labelFixture is one installation whose daemon reaches a fake credentials
// service: the daemon fixture's own cloud (gcpfake, and a runs bucket under the
// test's own directory), one fake host for the repository the config names, and
// the service that host's labels are written through.
type labelFixture struct {
	*daemonFixture
	service *labelService
	host    *fake.Provider
}

func newLabelFixture(t *testing.T, prs ...fake.PRState) *labelFixture {
	t.Helper()
	host := &fake.Provider{Repo: labelRepo, State: fake.State{PRs: prs}}
	withLabelIdentity(t)
	return &labelFixture{
		daemonFixture: newDaemonFixture(t),
		service:       newLabelService(t, host),
		host:          host,
	}
}

// daemon is the fixture's daemon with the installation's own records naming the
// service: the URL the daemon's client is built with and the App ID it signs
// with, both of which a real installation has in its local config and neither
// of which this fixture's own config file can carry (the file holds the URL to
// an https shape, and the fake service is loopback http, which is the one other
// shape the client's own check admits).
func (f *labelFixture) daemon(t *testing.T) (*orchestratorDaemon, *strings.Builder) {
	t.Helper()
	d, out := f.daemonFixture.daemon(t, "")
	d.lc.CredentialsService = &localcfg.CredentialsService{URL: f.service.srv.URL}
	repo := d.lc.Repos[labelRepo]
	repo.GitHubAppID = labelAppID
	d.lc.Repos[labelRepo] = repo
	return d, out
}

// open takes the lease and builds the daemon's one tick, which is the tick
// every cycle of a test then runs. It is built once and not per cycle because
// that is what the daemon does (start-up, then loop): the tick's seams are what
// carry a daemon's label memory, the hosts it has already built and the run
// numbers it has already read, so a tick rebuilt per cycle would be a daemon
// that forgot everything between them.
func (f *labelFixture) open(t *testing.T, d *orchestratorDaemon) {
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
// not finish: a label failure is one line in the log and never a failed cycle.
func (f *labelFixture) step(t *testing.T, d *orchestratorDaemon) {
	t.Helper()
	if _, err := d.tick.Step(context.Background()); err != nil {
		t.Fatalf("the tick failed: %v", err)
	}
}

// openRun is one run the daemon launched that has opened a pull request: the
// task and the launch the launcher writes, and the record the runner writes as
// it goes, which is where the number is read from (F25). The status is the
// runner's mid-run one, so the tick folds nothing and the items stay exactly as
// a test seeded them.
//
// The three are what makes the run one the tick reads as in flight: launch.json
// is what says the run started — without it the plan's own recovery re-issues
// an attempt nothing started, or abandons it once the claim window has passed,
// and the item is off its run before the labels are derived — and result.json's
// running status is what keeps the attempt open.
func (f *labelFixture) openRun(t *testing.T, runID string, number int) {
	t.Helper()
	ctx := context.Background()
	s := f.runStore(t, runID)
	if err := s.CreateTask(ctx, &task.Spec{Version: 1, RunID: runID, Repo: labelRepo, Ref: "main", Workflow: "web", Task: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteLaunch(ctx, &runstore.Launch{
		Version: 1, RunID: runID, Backend: "cloud-run", Job: "fugaro-web", LaunchedAt: labelNow(),
	}); err != nil {
		t.Fatal(err)
	}
	rec := &runstore.Record{
		Version: 1, RunID: runID, Repo: labelRepo, Status: runstore.StatusRunning,
		PR: &runstore.PRRef{Number: number, URL: fmt.Sprintf("https://github.com/%s/pull/%d", labelRepo, number)},
	}
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
}

// labelItem is one item of a test's node, as a daemon would have written it.
func labelItem(id string, phase orchestrator.Phase, runID, spec string, attempts int) orchestrator.WorkItem {
	return orchestrator.WorkItem{
		ID:        id,
		Title:     "the widget",
		Repo:      labelRepo,
		Recipe:    "default",
		RigorTier: "small",
		Brief:     "do the thing",
		Phase:     phase,
		RunID:     runID,
		Spec:      spec,
		Attempts:  attempts,
	}
}

// labelAttempt is one attempt of an item as the node records it: the run id the
// daemon minted before anything started, and how it ended — nothing while its
// run is in flight.
//
// The digest is the item's own (orchestrator.SpecDigest), which is what the
// daemon commits an attempt against and what it checks an in-flight attempt
// still matches: an attempt recorded under any other digest is one a running
// daemon abandons as committed for a spec the item no longer carries.
func labelAttempt(item orchestrator.WorkItem, attempt int, runID string, outcome orchestrator.Outcome) orchestrator.Intent {
	return orchestrator.Intent{
		ID:         orchestrator.IntentID(item.ID, attempt),
		RunID:      runID,
		SpecDigest: orchestrator.SpecDigest(item),
		ApprovalAt: labelNow(),
		At:         labelNow(),
		Outcome:    outcome,
	}
}

// labelNow is the reading every stamp of a seeded attempt carries. Nothing the
// syncer writes is stamped, so its value only has to be a reading.
func labelNow() time.Time { return time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC) }

// seed writes the node the daemon's next cycle reads: the state a test builds,
// encoded and read back through the document's own rules, so that a node no
// release would write cannot pin this test's behaviour.
func (f *labelFixture) seed(t *testing.T, s orchestrator.State) {
	t.Helper()
	raw, err := orchestrator.EncodeState(s)
	if err != nil {
		t.Fatalf("the test's state is not one this release writes: %v", err)
	}
	if _, err := orchestrator.DecodeState(raw); err != nil {
		t.Fatalf("the test's state does not read back: %v", err)
	}
	f.setState(t, string(raw))
}

// labelPR is one pull request of a test's host: its number, its state (empty is
// open) and the labels it carries.
func labelPR(number int, state gitprov.PRState, labels ...string) fake.PRState {
	return fake.PRState{PR: gitprov.PR{Number: number}, State: state, Labels: labels}
}

// closePR merges or closes one pull request of the fixture's host, as a person
// does. The fake is touched by the tick's own goroutine alone, so no lock is
// taken here.
func (f *labelFixture) closePR(t *testing.T, number int, state gitprov.PRState) {
	t.Helper()
	for i := range f.host.State.PRs {
		if f.host.State.PRs[i].Number == number {
			f.host.State.PRs[i].State = state
			return
		}
	}
	t.Fatalf("the host carries no pull request #%d", number)
}

// labelCalls is one host's label calls as lines a test reads: the op, the pull
// request and the run id, so a test asserts on what was asked rather than on
// the struct it was asked with.
func labelCalls(p *fake.Provider) []string {
	calls := p.Snapshot().LabelCalls
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		switch c.Op {
		case "ListOpenPRLabels":
			out = append(out, "list")
		case "PRLabels":
			out = append(out, fmt.Sprintf("read #%d", c.PR))
		case "EnsureLabel":
			out = append(out, "ensure "+c.Name)
		case "AddLabels":
			out = append(out, fmt.Sprintf("add #%d run=%s %s", c.PR, c.RunID, strings.Join(c.Names, "+")))
		case "RemoveLabels":
			out = append(out, fmt.Sprintf("remove #%d run=%s %s", c.PR, c.RunID, strings.Join(c.Names, "+")))
		default:
			out = append(out, c.Op)
		}
	}
	return out
}

// wantLabelCalls asserts one host's whole label call log, which is the only
// thing that tells a write from a read.
func wantLabelCalls(t *testing.T, p *fake.Provider, want ...string) {
	t.Helper()
	if got := labelCalls(p); !slices.Equal(got, want) {
		t.Errorf("the label calls are\n got %q\nwant %q", got, want)
	}
}

// wantLabelCallsSince asserts the calls one host made after the first from,
// which is how a test pins one cycle's writes without restating every cycle
// before it.
func wantLabelCallsSince(t *testing.T, p *fake.Provider, from int, want ...string) {
	t.Helper()
	got := labelCalls(p)
	if from > len(got) {
		t.Fatalf("the host made %d label call(s), fewer than the %d this test already read", len(got), from)
	}
	if tail := got[from:]; !slices.Equal(tail, want) {
		t.Errorf("the label calls after the first %d are\n got %q\nwant %q", from, tail, want)
	}
}

// prLabels is the labels one pull request of the fixture's host carries.
func prLabels(t *testing.T, p *fake.Provider, number int) []string {
	t.Helper()
	for _, pr := range p.Snapshot().PRs {
		if pr.Number == number {
			return pr.Labels
		}
	}
	t.Fatalf("the host carries no pull request #%d", number)
	return nil
}

// wantPRLabels asserts one pull request's whole label set.
func wantPRLabels(t *testing.T, p *fake.Provider, number int, want ...string) {
	t.Helper()
	if got := prLabels(t, p, number); !slices.Equal(got, want) {
		t.Errorf("pull request #%d carries %q, want %q", number, got, want)
	}
}

// TestTickLabelsOnce is the daemon's whole turn at one repository's labels: the
// running item's pull request gets its status label and the spec label of the
// plan it came from, the failed item's is relabelled, a second unchanged cycle
// writes nothing, and a pull request a person merged is cleared once and then
// left alone.
func TestTickLabelsOnce(t *testing.T) {
	// #7 is the running item's pull request and carries nothing yet; #8 is the
	// failed one's, still carrying `fugaro:running` from when it was running.
	f := newLabelFixture(t, labelPR(7, ""), labelPR(8, "", labelRunning))
	f.openRun(t, labelRunA, 7)
	f.openRun(t, labelRunB, 8)
	running := labelItem("wi-1", orchestrator.PhaseRunning, labelRunA, "forge-labels", 1)
	failed := labelItem("wi-2", orchestrator.PhaseFailed, labelRunB, "", 1)
	f.seed(t, orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		Rev:           1,
		AgentID:       "aurora",
		Role:          orchestrator.RoleOrchestrator,
		WorkItems:     []orchestrator.WorkItem{running, failed},
		Intents: []orchestrator.Intent{
			labelAttempt(running, 1, labelRunA, ""),
			labelAttempt(failed, 1, labelRunB, orchestrator.OutcomeFailed),
		},
	})
	d, out := f.daemon(t)
	f.open(t, d)

	f.step(t, d)
	wantLabelCalls(t, f.host,
		"list",
		"ensure "+labelRunning,
		"ensure "+labelSpec,
		"add #7 run="+labelRunA+" "+labelRunning,
		"add #7 run="+labelRunA+" "+labelSpec,
		"ensure "+labelFailed,
		"add #8 run="+labelRunB+" "+labelFailed,
		"remove #8 run="+labelRunB+" "+labelRunning,
	)
	wantPRLabels(t, f.host, 7, labelRunning, labelSpec)
	wantPRLabels(t, f.host, 8, labelFailed)
	// The daemon presented its own identity, and named the App this
	// installation records for the repository, on every call.
	for _, id := range f.service.identities() {
		if id != labelIdentity {
			t.Errorf("the service was presented with %q, want the daemon's own identity", id)
		}
	}
	if got := f.service.appID(labelRepo); got != labelAppID {
		t.Errorf("the calls named App ID %q, want the one this installation records (%s)", got, labelAppID)
	}
	if strings.Contains(out.String(), "label") {
		t.Errorf("the daemon said something about the labels of a cycle that had nothing to say:\n%s", out.String())
	}

	// The second cycle is the same daemon over the same host: nothing changed,
	// so nothing is ensured and nothing is written — one listing and no more.
	done := len(labelCalls(f.host))
	f.step(t, d)
	wantLabelCallsSince(t, f.host, done, "list")

	// A person merges #7: it leaves the open listing, so the daemon reads it by
	// number and clears both status labels off it. The spec label is what a
	// person filters a plan's pull requests by, and it stays.
	f.closePR(t, 7, gitprov.PRMerged)
	done = len(labelCalls(f.host))
	f.step(t, d)
	wantLabelCallsSince(t, f.host, done,
		"list",
		"read #7",
		"remove #7 run="+labelRunA+" "+labelRunning,
	)
	wantPRLabels(t, f.host, 7, labelSpec)
	wantPRLabels(t, f.host, 8, labelFailed)

	// And it is remembered as closed and clean: the cycle after that reads it
	// no more.
	done = len(labelCalls(f.host))
	f.step(t, d)
	wantLabelCallsSince(t, f.host, done, "list")
}

// TestTickLabelsFailedThenRetry is R8 and F21: the pull request of an attempt
// that ended failed is relabelled as failed and left there, and the retry that
// follows is a new run on a new pull request, which gets the running label. A
// syncer that walked only the item's own run id would leave the first pull
// request carrying `fugaro:running` for good.
func TestTickLabelsFailedThenRetry(t *testing.T) {
	f := newLabelFixture(t, labelPR(7, "", labelRunning), labelPR(9, ""))
	f.openRun(t, labelRunA, 7)
	f.openRun(t, labelRunC, 9)
	retried := labelItem("wi-1", orchestrator.PhaseRunning, labelRunC, "", 2)
	f.seed(t, orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		Rev:           1,
		AgentID:       "aurora",
		Role:          orchestrator.RoleOrchestrator,
		WorkItems:     []orchestrator.WorkItem{retried},
		Intents: []orchestrator.Intent{
			labelAttempt(retried, 1, labelRunA, orchestrator.OutcomeFailed),
			labelAttempt(retried, 2, labelRunC, ""),
		},
	})
	d, _ := f.daemon(t)
	f.open(t, d)

	f.step(t, d)
	wantLabelCalls(t, f.host,
		"list",
		"ensure "+labelFailed,
		"add #7 run="+labelRunA+" "+labelFailed,
		"remove #7 run="+labelRunA+" "+labelRunning,
		"ensure "+labelRunning,
		"add #9 run="+labelRunC+" "+labelRunning,
	)
	wantPRLabels(t, f.host, 7, labelFailed)
	wantPRLabels(t, f.host, 9, labelRunning)

	// The attempt that ended is not written again: the item moved on, and the
	// pull request that run left keeps what it ended with.
	done := len(labelCalls(f.host))
	f.step(t, d)
	wantLabelCallsSince(t, f.host, done, "list")
	wantPRLabels(t, f.host, 7, labelFailed)
	wantPRLabels(t, f.host, 9, labelRunning)
}

// TestPRReaderCachesPerRun is F25: a run's pull request number is read from the
// run's own record and kept for the daemon's life, because it is fixed once the
// run has opened one; an answer that there is none yet is not kept, because the
// cycle after it has to see a pull request that appeared in between.
func TestPRReaderCachesPerRun(t *testing.T) {
	f := newDaemonFixture(t)
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	write := func(runID string, number int) {
		t.Helper()
		s := runstore.Open(b, appSlug, runID)
		if err := s.CreateTask(ctx, &task.Spec{Version: 1, RunID: runID, Repo: labelRepo, Ref: "main", Workflow: "web", Task: "x"}); err != nil {
			t.Fatal(err)
		}
		rec := &runstore.Record{Version: 1, RunID: runID, Repo: labelRepo, Status: runstore.StatusRunning,
			PR: &runstore.PRRef{Number: number, URL: fmt.Sprintf("https://github.com/%s/pull/%d", labelRepo, number)}}
		if err := s.WriteRecord(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	write(labelRunA, 7)

	r := &orchestratorLabelPRs{bucket: b, numbers: map[string]int{}}
	if number, ok, err := r.PRNumber(ctx, labelRunA); err != nil || !ok || number != 7 {
		t.Fatalf("reading the pull request of a run that opened one: %d, %v, %v", number, ok, err)
	}

	// The record is gone, which a reader that had not read this run answers as
	// no pull request at all: the daemon's own answer is the number it read.
	if err := b.Delete(ctx, "runs/"+appSlug+"/"+labelRunA+"/result.json"); err != nil {
		t.Fatal(err)
	}
	fresh := &orchestratorLabelPRs{bucket: b, numbers: map[string]int{}}
	if _, ok, err := fresh.PRNumber(ctx, labelRunA); err != nil || ok {
		t.Fatalf("a reader that had not read this run answered %v, %v where the record is gone, want no pull request and no error", ok, err)
	}
	if number, ok, err := r.PRNumber(ctx, labelRunA); err != nil || !ok || number != 7 {
		t.Errorf("the daemon read the run's record again and answered %d, %v, %v, want the number it already had", number, ok, err)
	}

	// A run that has not opened one is asked again: the retry's pull request
	// appears between two cycles, and the second has to see it.
	if _, ok, err := r.PRNumber(ctx, labelRunB); err != nil || ok {
		t.Fatalf("reading the pull request of a run that has opened none: %v, %v, want no pull request and no error", ok, err)
	}
	write(labelRunB, 8)
	if number, ok, err := r.PRNumber(ctx, labelRunB); err != nil || !ok || number != 8 {
		t.Errorf("reading a run whose pull request appeared after the first read: %d, %v, %v, want 8", number, ok, err)
	}

	// A run this bucket has nothing for is no pull request and not an error:
	// the item is left unlabelled for the cycle and read again by the next.
	if _, ok, err := r.PRNumber(ctx, labelRunC); err != nil || ok {
		t.Errorf("reading a run that is not in the bucket: %v, %v, want no pull request and no error", ok, err)
	}
}
