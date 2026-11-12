package credsvc

// MC7's tests: a pool whose strategy is round-robin is served by a rotation
// through its usable members, and a pool whose strategy is not is served
// exactly as it was (docs/model-crafts/implementation-plan.md, "MC7 —
// round-robin"; reconciliation F6, F7, D154, D1625, D1632).
//
// Everything here is hermetic (Convention 9): the upstream is
// internal/gateway/anthropicfake on loopback, the state is a map or a small
// in-memory database, and no test in this file is, or looks like, a credential
// (Convention 2) — every value is built at run time by fakeCred.
//
// The counter's own concurrency is the one thing that cannot be read off the
// service fixture: the fixture's store advances a plain field under a mutex, so
// a selection that read and wrote it without a precondition would look no
// different from one that used a compare-and-set. That test drives
// creds.RTDBStore over a database of its own instead (casDB), which fails a
// write whose ETag moved — the mutation the plan asks for (replace the
// compare-and-set with a plain write) turns it red.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// rrAccount is the third named Claude account a rotation of three reaches,
// beside the two pool_test.go's bundle holds. Three members is the shortest
// pool that tells a rotation from the cascade's own rule at all: a tied set of
// two is already alternated by the counter the rule breaks its tie with, so a
// two-member pool would be a test that passes for the wrong reason.
const rrAccount = "carol"

// rrSvc is poolSvc's fixture with a fourth Claude account in the bundle, in the
// project node and in the repository's own admissions, which is what a pool of
// three named accounts needs: a member the repository is not admitted to is a
// member the cascade never offers (O4).
func rrSvc(t *testing.T, block string) *svc {
	t.Helper()
	return newSvcWith(t, func(s *svc, _ *Config) {
		s.src.docs = [][]byte{poolBundleDoc(t, rrAccount)}
		s.policy.shared = decodeTestPolicy(t, poolNode(block, rrAccount))
		s.policy.admissions[s.slug] = decodeTestChild(t,
			testChildDoc(config.DefaultClaudeAccount, testAccount, testPoolAccount, rrAccount))
	})
}

// rrBlock is the node's own block for a round-robin pool over the named
// accounts: the credentials' settings, the pool with the strategy written, and
// the coder craft that goes out on it. The subscription switch is on because
// every member is a Claude OAuth subscription and a pool of two or more is
// inert without it (D1622) — that rule is pool_test.go's to pin, and this file
// must not be reading a refusal where it means to read a rotation.
func rrBlock(settings string, members ...string) string {
	entries := make([]string, 0, len(members))
	for _, m := range members {
		entries = append(entries, `{"name": "`+m+`", "type": "`+config.CredentialTypeClaudeOAuth+`"}`)
	}
	return `"credentials": {` + settings + `},
		` + poolCraftBlockOf(config.PoolStrategyRoundRobin, strings.Join(entries, ", "), members...) + `,
		"subscription_spread": true`
}

// rrCalls makes one call per distinct run and answers the credentials they were
// served by, in order. Each run is a run of the project that has never called
// before — the state a rotation spreads over — and the upstream answers every
// one of them, so a test reads the whole rotation rather than its first step.
func rrCalls(t *testing.T, s *svc, n int) []string {
	t.Helper()
	s.up.Func = func(*http.Request, []byte) anthropicfake.Reply {
		return anthropicfake.MessageOK(testModel, testUsage)
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		s.run = fmt.Sprintf("20261020-0900%02d-ab12", i)
		resp, body := s.call(t, callBody(fmt.Sprintf("round-robin-%d", i)))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("run %d's call is %d, want a served call: %s", i, resp.StatusCode, body)
		}
		out = append(out, resp.Header.Get(HeaderCredential))
	}
	return out
}

// rrWant is the one assertion every test here makes: which credential each run
// was served by, in the order the runs called.
func rrWant(t *testing.T, got []string, why string, want ...string) {
	t.Helper()
	if strings.Join(got, ", ") != strings.Join(want, ", ") {
		t.Errorf("the runs were served by %v, want %v: %s", got, want, why)
	}
}

// TestRoundRobinSpreadsAcrossRuns is MC7's red step, and it is written so that
// it is red: a pool of three subscriptions, the FIRST of which carries an
// exhaustion that has already lapsed (with the window control off, so its reset
// is the whole of the hold).
//
// The cascade's own rule orders a usable set by how long ago each member was
// exhausted, and a member that has never been exhausted is the oldest of all —
// so before this task the lapsed member was never reached at all and the runs
// alternated over the other two (bob, carol, bob), whatever order the owner
// listed the pool in. A rotation takes the listed order from wherever the
// counter stands, so the three runs take the three members in turn: the spread
// the spec asks for ("share the load across them", §2), and the reason its own
// words are "what round-robin starts from".
func TestRoundRobinSpreadsAcrossRuns(t *testing.T) {
	s := rrSvc(t, rrBlock(`"fallback": "auto", "stop_after_exhaustion_per_window": false, "concurrency_per_token": 0`,
		testAccount, testPoolAccount, rrAccount))
	s.hold(testAccount, s.now.Add(-2*time.Hour), s.now.Add(-time.Hour))

	got := rrCalls(t, s, 3)
	rrWant(t, got, "the pool's strategy is round-robin, so three runs take its three usable members in the order the owner listed them, and never all of them the member that was exhausted longest ago",
		testAccount, testPoolAccount, rrAccount)

	if n := s.up.Count(); n != 3 {
		t.Errorf("the upstream saw %d requests, want one per run: a rotation selects one member per run and tries nothing else", n)
	}
}

// TestRoundRobinSkipsBackedOffMember is the spec's "skip any that fail" (§2):
// the member a mark holds is not in the rotation at all, so the turn that would
// have been its goes to the member after it — the rotation is over the pool's
// USABLE members and not over its list.
//
// The pool's first member is backed off, and the second carries an exhaustion
// that has already lapsed: the cascade's own rule would then read the second as
// the most recently exhausted of the two that can serve and answer EVERY run
// with the third, which is the answer this test is written against (and the
// reason it is red before this task). The rotation covers the two usable
// members in the order the owner wrote them, and the backed-off one is never
// served: a backed-off mark is never usable however long its wait has been —
// the probe is what clears it (C1.12), and a probe is not a selection.
func TestRoundRobinSkipsBackedOffMember(t *testing.T) {
	s := rrSvc(t, rrBlock(`"fallback": "auto", "stop_after_exhaustion_per_window": false, "concurrency_per_token": 0`,
		testAccount, testPoolAccount, rrAccount))
	s.backedOff(testAccount, s.now.Add(-time.Hour), creds.DefaultBackoffInitial)
	s.hold(testPoolAccount, s.now.Add(-2*time.Hour), s.now.Add(-time.Hour))

	got := rrCalls(t, s, 3)
	rrWant(t, got, "a member a mark holds is skipped rather than given a turn, so the runs cover the members that can serve them in the pool's order",
		testPoolAccount, rrAccount, testPoolAccount)

	for _, name := range got {
		if name == testAccount {
			t.Fatalf("a run was served by %q, which is backed off and must not be selected at all", name)
		}
	}
	if n := s.up.Count(); n != 3 {
		t.Errorf("the upstream saw %d requests, want one per run: a skipped member is not probed and not tried", n)
	}
}

// TestRoundRobinNeverMovesAStickyRun is D154 and D874 on this path: the
// credential a run was given is the one it keeps, whatever the rotation has
// moved on to for the project's other runs — the reason is the prompt cache,
// and it is why the choice is per run and never per call.
//
// The second run's call is what puts the counter past the first run's turn: a
// service that rotated on every call would answer the third call with the third
// member, and one that rotated per run answers it with the first run's own. The
// counter is read at the end for the same rule from the other side — three
// calls consumed two turns, so the run that was already placed consumed none.
func TestRoundRobinNeverMovesAStickyRun(t *testing.T) {
	s := rrSvc(t, rrBlock(`"fallback": "auto", "concurrency_per_token": 0`,
		testAccount, testPoolAccount, rrAccount))
	s.up.Func = func(*http.Request, []byte) anthropicfake.Reply {
		return anthropicfake.MessageOK(testModel, testUsage)
	}

	s.run = "20261020-090000-ab12"
	resp, body := s.call(t, callBody("sticky-first"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the first run's first call is %d, want a served call: %s", resp.StatusCode, body)
	}
	first := resp.Header.Get(HeaderCredential)
	rrWant(t, []string{first}, "a run with no credential yet takes the pool's first member, which is where the rotation starts",
		testAccount)

	s.run = "20261020-090001-ab12"
	resp, body = s.call(t, callBody("sticky-other"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the second run's call is %d, want a served call: %s", resp.StatusCode, body)
	}
	rrWant(t, []string{resp.Header.Get(HeaderCredential)}, "another run takes the next turn of the rotation",
		testPoolAccount)

	s.run = "20261020-090000-ab12"
	resp, body = s.call(t, callBody("sticky-again"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the first run's second call is %d, want a served call: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderCredential); got != first {
		t.Errorf("the run's second call was served by %q, want %q: a run keeps the credential it was placed on and the rotation never moves it (D154, D874)",
			got, first)
	}

	s.state.mu.Lock()
	turns := s.state.rr
	s.state.mu.Unlock()
	if turns != 2 {
		t.Errorf("the counter advanced %d times over three calls, want 2 — one turn per run and none for the call of a run that already had a credential",
			turns)
	}
}

// TestRoundRobinCompareAndSetUnderConcurrentSelectors is D1625's other half:
// one counter per project, written with compare-and-set, so that several
// selections running at once take different members rather than the same one.
//
// The selectors here are separate instances over ONE store — the shape of two
// service instances serving one project — and they run under -race. Each run
// takes one turn, so a counter that loses an update hands two runs the same
// member and one member none: the counts are asserted exactly, which is what
// makes a plain write (the plan's mutation) fail this test rather than pass it
// quietly.
func TestRoundRobinCompareAndSetUnderConcurrentSelectors(t *testing.T) {
	const (
		runs    = 12
		members = 3
	)
	store := &creds.RTDBStore{DB: newCASDB()}
	now := testNow()
	cands := []creds.Credential{
		{Name: testAccount, Tier: creds.TierA},
		{Name: testPoolAccount, Tier: creds.TierA},
		{Name: rrAccount, Tier: creds.TierA},
	}
	var (
		mu     sync.Mutex
		counts = map[string]int{}
		failed []string
	)
	var wg sync.WaitGroup
	for i := 0; i < runs; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sel := &creds.Selector{
				Store:    store,
				Settings: creds.Settings{Fallback: creds.Auto, ConcurrencyPerToken: intPtr(0)},
				Now:      func() time.Time { return now },
				Strategy: creds.StrategyRoundRobin,
			}
			c, wait, err := sel.Select(context.Background(),
				creds.Run{Slug: "aurora-app", ID: fmt.Sprintf("20261020-0900%02d-ab12", i)}, cands)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed = append(failed, fmt.Sprintf("run %d: %v (wait %s)", i, err, wait))
				return
			}
			counts[c.Name]++
		}(i)
	}
	wg.Wait()

	if len(failed) > 0 {
		t.Fatalf("%d of %d selections failed, and a rotation that cannot read its own counter must fail closed: %s",
			len(failed), runs, strings.Join(failed, "; "))
	}
	if len(counts) != members {
		t.Fatalf("the runs were served by %d of the pool's %d members (%v): a rotation reaches every one of them",
			len(counts), members, counts)
	}
	for _, c := range cands {
		if got := counts[c.Name]; got != runs/members {
			t.Errorf("%q served %d of the %d runs, want %d: two selections that read the same counter value are two runs on one member and none on another",
				c.Name, got, runs, runs/members)
		}
	}
}

// casDB is the state's database in memory, and it is deliberately the strictest
// one this state can be tested against: a write whose ETag is not the node's is
// refused with rtdb.ErrPrecondition, exactly as the database refuses it, so a
// counter read-and-written without a precondition loses its update here rather
// than silently.
//
// It answers exact nodes only — the marks and the leases are absent in these
// tests, and an absent node is an empty set to the reader (creds.RTDBStore) —
// and it holds one mutex over both maps, which is what makes the CAS itself
// race-free while the writers around it are not.
type casDB struct {
	mu     sync.Mutex
	nodes  map[string]json.RawMessage
	etags  map[string]string
	writes int
}

// casNullEtag is what the database answers for a node that is not there
// (rtdb.Client.GetETag). It is a value a writer can hand back to PutIfMatch,
// which is exactly why it is not the empty string.
const casNullEtag = "null_etag"

func newCASDB() *casDB {
	return &casDB{nodes: map[string]json.RawMessage{}, etags: map[string]string{}}
}

// put writes a node and gives it a new ETag, as any write to it does. The
// caller holds the lock.
func (d *casDB) put(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	d.writes++
	d.nodes[path] = b
	d.etags[path] = "etag-" + strconv.Itoa(d.writes)
	return nil
}

// etagOf is a node's ETag. The caller holds the lock.
func (d *casDB) etagOf(path string) string {
	if e, ok := d.etags[path]; ok {
		return e
	}
	return casNullEtag
}

func (d *casDB) GetETag(_ context.Context, path string, out any) (string, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	raw, ok := d.nodes[path]
	if !ok {
		return casNullEtag, false, nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return "", false, err
	}
	return d.etagOf(path), true, nil
}

func (d *casDB) PutIfMatch(_ context.Context, path, etag string, v any) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if etag == "" {
		// rtdb.Client.PutIfMatch: an empty etag is no precondition, and a
		// writer that handed one in never read the node. Refused here for the
		// reason the database refuses it — a plain write is exactly what this
		// test must not let through.
		return errors.New("rtdb: an empty etag is no precondition")
	}
	if etag != d.etagOf(path) {
		return rtdb.ErrPrecondition
	}
	return d.put(path, v)
}

func (d *casDB) Patch(_ context.Context, root string, updates map[string]any) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for rel, v := range updates {
		path := root + "/" + rel
		if path == creds.PathRoundRobin() {
			// The counter has no unconditional write. D1625's whole point is
			// that two selections advancing it at once must not lose one of the
			// two, and a Patch — which carries no precondition — is the write
			// that loses it. The database the service talks to would accept this
			// one (Patch is unconditional by design), so refusing it here is
			// this fake being stricter than the protocol on purpose: it is what
			// makes the plan's mutation (the compare-and-set replaced by a plain
			// write) fail a test that would otherwise pass whenever the
			// goroutines happened not to collide.
			return fmt.Errorf("the round-robin counter is written without a precondition: %w", rtdb.ErrPrecondition)
		}
		if v == nil {
			delete(d.nodes, path)
			delete(d.etags, path)
			continue
		}
		if err := d.put(path, v); err != nil {
			return err
		}
	}
	return nil
}

// intPtr is a *int for a Settings member the policy writes: the fixture's
// settings are built here rather than decoded, so the helper is what stands in
// for the document.
func intPtr(n int) *int { return &n }
