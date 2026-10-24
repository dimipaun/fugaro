package credsvc

// The probe's tests (C1.12): whether a backed-off credential is asked before
// real work is routed to it, what each answer writes, and what happens when the
// asking itself goes wrong.
//
// Everything here is hermetic (Convention 9): the upstream is
// internal/gateway/anthropicfake on loopback, the policy is a decoded document,
// the state and the probe claims are maps, and no test opens a socket to
// anything but 127.0.0.1. Nothing in this file is, or looks like, a credential:
// every value is built at run time by fakeCred (Convention 2).
//
// The fixture runs under `fallback: oauth-only` with one admitted account, so a
// call the cascade cannot serve is answered with the typed wait and never with
// the paid key, and the credential the tests back off is the only one that
// could serve the call.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
)

// fakeProbes is the database's conditional write as the probe's lease uses it:
// a node map, a version per node, and the compare-and-set the real client
// implements — a write under a version that has moved is refused, which is the
// whole of what "one probe at a time" is built from. Values are stored and
// decoded as JSON, as the real client stores them.
type fakeProbes struct {
	mu     sync.Mutex
	nodes  map[string][]byte
	etag   map[string]int
	reads  int
	writes int
	err    error
	// stale makes every compare-and-set fail, whatever it read first: the node
	// a real database refuses the write under is one another writer keeps
	// moving, and this is that writer.
	stale bool
}

func newFakeProbes() *fakeProbes {
	return &fakeProbes{nodes: map[string][]byte{}, etag: map[string]int{}}
}

// errStaleVersion is what a write under a version that moved answers. The probe
// does not read it (it retries, and then gives up, which is the fail-closed
// answer either way), so it only has to be an error.
var errStaleVersion = errStaleVersionError{}

type errStaleVersionError struct{}

func (errStaleVersionError) Error() string { return "the node moved under the write" }

func (p *fakeProbes) GetETag(_ context.Context, path string, out any) (string, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return "", false, p.err
	}
	p.reads++
	b, ok := p.nodes[path]
	if !ok {
		return "", false, nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return "", false, err
	}
	return strconv.Itoa(p.etag[path]), true, nil
}

func (p *fakeProbes) PutIfMatch(_ context.Context, path, etag string, v any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	if p.stale {
		return errStaleVersion
	}
	cur := ""
	if _, ok := p.nodes[path]; ok {
		cur = strconv.Itoa(p.etag[path])
	}
	if etag != cur {
		return errStaleVersion
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	p.nodes[path] = b
	p.etag[path]++
	p.writes++
	return nil
}

// hold plants a claim on a credential's probe slot that stands until when, as
// another instance's would: until is in the future for a live claim and in the
// past for one whose instance died mid-probe. A claim already there is replaced,
// which is how a test lapses one.
func (p *fakeProbes) hold(t *testing.T, name string, until time.Time) {
	t.Helper()
	ctx := context.Background()
	path := probeLeasePath(name)
	var cur probeLease
	etag, _, err := p.GetETag(ctx, path, &cur)
	if err != nil {
		t.Fatalf("reading the probe claim: %v", err)
	}
	if err := p.PutIfMatch(ctx, path, etag, probeLease{
		Slug: "another-slug", Run: "another-run", Until: until,
	}); err != nil {
		t.Fatalf("planting a probe claim: %v", err)
	}
}

// claim reads the claim the service wrote on a credential's probe slot.
func (p *fakeProbes) claim(t *testing.T, name string) (probeLease, bool) {
	t.Helper()
	var l probeLease
	_, found, err := p.GetETag(context.Background(), probeLeasePath(name), &l)
	if err != nil {
		t.Fatalf("reading the probe claim: %v", err)
	}
	return l, found
}

// silentHandler is an audit handler that records nothing: Enabled is false at
// every level, which is the shape audit.go refuses (a log that is not recording
// is a failure a caller cannot mistake for a written line).
type silentHandler struct{}

func (silentHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (silentHandler) Handle(context.Context, slog.Record) error { return nil }
func (silentHandler) WithAttrs([]slog.Attr) slog.Handler        { return silentHandler{} }
func (silentHandler) WithGroup(string) slog.Handler             { return silentHandler{} }

// probeSvc is the fixture wired for the probe's tests: oauth-only, one account
// admitted, and the audit lines in a buffer of their own, so a test reads them
// as the objects they are rather than beside the service's own lines.
//
// The clock is the fixture's frozen one, so every instant a probe writes is a
// value a test can name.
func probeSvc(t *testing.T, mutate func(*svc, *Config)) (*svc, *logBuf) {
	t.Helper()
	// logBuf, not a bare bytes.Buffer: the handler writes the audit line from the
	// server's goroutine while a test (waitFor, in the disconnect test) reads it
	// from its own, and the service's own lines already go to one of these.
	auditLog := &logBuf{}
	s := newSvcWith(t, func(sv *svc, cfg *Config) {
		// D142's two controls written off, so what the tests below measure is
		// the probe's own effect on the mark and not the window it opens.
		// TestProbeSuccessInsideTheWindow writes the first one back on.
		sv.policy.shared = decodeTestPolicy(t, testPolicyDocControls("oauth-only", false, 0))
		sv.policy.admissions[sv.slug] = decodeTestChild(t, testChildDoc("default"))
		cfg.AuditLog = slog.NewJSONHandler(auditLog, nil)
		if mutate != nil {
			mutate(sv, cfg)
		}
	})
	return s, auditLog
}

// testPolicyDocBackoff is the probe fixture's policy with the backoff written
// out: what an installation publishes when it wants a backed-off credential
// retried sooner than the probe claim's own TTL. Decode is the reader the
// service deploys, so a value it would refuse (a cap below the initial wait) is
// not one of these.
func testPolicyDocBackoff(initial, cap string) string {
	return `{
		"version": 3,
		"budget": ` + testBudgetDoc + `,
		"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"],
		"accounts": {
			"default": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]},
			"` + testAccount + `": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]}
		},
		"credentials": {"fallback": "oauth-only", "stop_after_exhaustion_per_window": false,
			"backoff": {"initial": "` + initial + `", "cap": "` + cap + `"}}
	}`
}

// backedOff plants the state a probe is owed for: a backed-off mark whose own
// wait has passed, which is what creds answers NeedsProbe for and what the
// selection path probes. since is when the exhaustion that opened it happened,
// and backoff is the wait in force.
func (s *svc) backedOff(name string, since time.Time, backoff time.Duration) creds.Mark {
	return s.backedOffDue(name, since, backoff, s.now.Add(-time.Minute))
}

// backedOffDue plants a backed-off mark whose wait ran out at a chosen instant.
// The instant matters to a test that wants one candidate to be the earliest due:
// firstDue answers the earliest one, and a tie is answered by the order the
// bundle happens to be in, which a test must not rely on to make a claim.
func (s *svc) backedOffDue(name string, since time.Time, backoff time.Duration, until time.Time) creds.Mark {
	m := creds.Mark{
		State:   creds.StateBackedOff,
		Since:   since,
		Backoff: backoff,
		Until:   until,
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	s.state.marks[name] = m
	return m
}

// tierBName is the name of the bundle's paid key, read from the bundle the
// service serves: the document's keys are the platform's own, so no test spells
// one.
func (s *svc) tierBName(t *testing.T) string {
	t.Helper()
	b, err := s.server.cfg.Bundles.Load(context.Background())
	if err != nil {
		t.Fatalf("loading the bundle: %v", err)
	}
	for _, e := range b.Entries() {
		if e.Tier == creds.TierB {
			return e.Name
		}
	}
	t.Fatal("the fixture's bundle holds no tier-B credential")
	return ""
}

// broken plants a mark a probe must never run on: a 401 (D139), which only an
// operator clears (C1.21).
func (s *svc) broken(name string, since time.Time) creds.Mark {
	m := creds.Break(since)
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	s.state.marks[name] = m
	return m
}

// wait reads a response as the typed wait and returns the instant the run was
// told to wait for.
func wait(t *testing.T, resp *http.Response, body []byte) time.Time {
	t.Helper()
	e, ok, err := ReadExhaustedUntil(resp.StatusCode, body)
	if err != nil {
		t.Fatalf("the answer is not one this contract can read: %v", err)
	}
	if !ok {
		t.Fatalf("the answer is not the typed wait: %d %s", resp.StatusCode, body)
	}
	return e.ResetAt
}

// probeLines is every line a buffer holds for a probe's own act: the fixtures'
// audit handler is the service's one audit log, and the answer path writes its
// own lines into it too (C1.13 — an issuance, and the refusal a call that could
// not be served gets). A line whose purpose is PurposeProbe is a probe's, and a
// test about probes reads only those; anything that is not purpose-tagged is
// this file's own bug, not a probe's line.
func probeLines(t *testing.T, buf *logBuf) []map[string]any {
	t.Helper()
	if buf.Len() == 0 {
		return nil
	}
	var out []map[string]any
	for _, line := range auditLines(t, buf) {
		if got, ok := line["purpose"].(string); !ok || got == "" {
			t.Fatalf("an audit line carries no purpose: %v", line)
		}
		if line["purpose"] == PurposeProbe {
			out = append(out, line)
		}
	}
	return out
}

// noProbeLine fails a test that expected no probe to be audited and found one.
func noProbeLine(t *testing.T, buf *logBuf, why string) {
	t.Helper()
	if lines := probeLines(t, buf); len(lines) != 0 {
		t.Errorf("%s, and a probe was audited anyway: %v", why, lines)
	}
}

// probeLine is the one audit line a probe writes, and whether there is exactly
// one.
func probeLine(t *testing.T, buf *logBuf) (map[string]any, bool) {
	t.Helper()
	lines := probeLines(t, buf)
	if len(lines) == 0 {
		return nil, false
	}
	if len(lines) != 1 {
		t.Fatalf("a probe wrote %d audit lines, want 1: %v", len(lines), lines)
	}
	return lines[0], true
}

// ---------------------------------------------------------------------------
// The red step
// ---------------------------------------------------------------------------

// TestProbeClearsOnSuccess is the plan's red step (C1.12): a backed-off
// credential whose wait has passed is asked whether it works before real work is
// routed to it, and a "yes" clears the mark — so the very call that was about to
// be told to wait is served instead.
//
// The strongest assertions here are the fake upstream's own: it saw exactly two
// requests, the probe and then the run's call, and the first of them is the
// minimal one, with nothing of the run's body in it.
func TestProbeClearsOnSuccess(t *testing.T) {
	s, auditLog := probeSvc(t, nil)
	s.backedOff("default", s.now.Add(-time.Hour), creds.DefaultBackoffInitial)
	s.up.Script = []anthropicfake.Reply{
		// The probe's answer, and one no client may ever see: it carries a
		// marker this test looks for in the answer the run is given.
		{Status: http.StatusOK, Body: `{"type":"message","probe_answer":"probe-answer"}`},
		anthropicfake.MessageOK(testModel, testUsage),
	}

	resp, body := s.call(t, callBody("real-work"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call was not served after the probe cleared the credential: %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "msg_fake") || strings.Contains(string(body), "probe-answer") {
		t.Errorf("the run was given an answer that is not the upstream's answer to its own call: %s", body)
	}
	if m, ok := s.state.mark("default"); !ok || !m.Available() {
		t.Errorf("a successful probe left the mark at %+v, want it cleared", m)
	}

	// The probe and the run's own call, in that order.
	if got := s.up.Count(); got != 2 {
		t.Fatalf("the upstream saw %d requests, want the probe and the run's call", got)
	}
	probe, run := string(s.up.Body(0)), string(s.up.Body(1))
	for _, want := range []string{`"model":"` + testModel + `"`, `"max_tokens":1`, `"content":"."`} {
		if !strings.Contains(probe, want) {
			t.Errorf("the probe's body carries no %s: %s", want, probe)
		}
	}
	if strings.Contains(probe, "real-work") {
		t.Errorf("the probe carried the run's own body: %s", probe)
	}
	if !strings.Contains(run, "real-work") {
		t.Errorf("the run's own call did not carry the run's body: %s", run)
	}

	// One audit line, and it is the probe's: the credential by name, the tier,
	// the purpose and the status, with the run and its repository on it.
	line, ok := probeLine(t, auditLog)
	if !ok {
		t.Fatal("a probe wrote no audit line")
	}
	checkAuditShape(t, line)
	for field, want := range map[string]string{
		"credential": "default",
		"tier":       TierA,
		"purpose":    PurposeProbe,
		"status":     StatusOK,
		"run":        s.run,
		"repository": s.slug,
		"job":        s.job,
		"caller":     testJobSA,
	} {
		if got, _ := line[field].(string); got != want {
			t.Errorf("the audit line's %s is %q, want %q", field, got, want)
		}
	}
	// And no value anywhere: not in the audit line, not in the service's own
	// lines, which are in the same buffer.
	if v := fakeCred(t, "default"); v != "" && strings.Contains(s.logs.String(), v) {
		t.Error("the service's own log carries the credential's value")
	}
}

// callDetached makes a call with a context the test owns, and reads nothing of
// the answer: what the test that uses it measures is what the service recorded,
// and a client that hung up mid-call may never see an answer at all.
func (s *svc) callDetached(t *testing.T, ctx context.Context, body string) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.http.URL+MessagesPath, strings.NewReader(body))
	if err != nil {
		t.Fatalf("building the call: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.jobToken())
	req.Header.Set(HeaderRunIdentity, s.runToken())
	req.Header.Set(HeaderRunJob, s.job)
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Client().Do(req)
	if err != nil {
		// The client's own context ended, which is the disconnect this test
		// makes. Anything else is the fixture.
		if ctx.Err() == nil {
			t.Fatalf("the call failed for a reason that is not the disconnect: %v", err)
		}
		return
	}
	resp.Body.Close()
}

// TestProbeSurvivesAClientDisconnect is the probe's own when its caller goes
// away: the run hangs up while the probe is in flight, and the probe's answer
// is still recorded — the mark is cleared and the audit line says the
// credential worked.
//
// The disconnect is made from inside the upstream, at the first request, so it
// lands while the probe is in flight rather than before it: the caller's
// context is cancelled as the probe's request arrives. What the test then reads
// is the state and the audit line, because a client that hung up is owed and
// gets no answer.
//
// Both of the alternatives are wrong in the same direction. A probe left on the
// caller's context is cancelled by the hang-up, and the service then writes the
// credential down as failed — an audit line saying "exhausted" for an answer
// nobody read, and a wait doubled to punish a credential for the client's own
// abandonment.
func TestProbeSurvivesAClientDisconnect(t *testing.T) {
	s, auditLog := probeSvc(t, nil)
	s.backedOff("default", s.now.Add(-time.Hour), creds.DefaultBackoffInitial)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var once sync.Once
	s.up.Func = func(*http.Request, []byte) anthropicfake.Reply {
		// The run hangs up here: this is the probe's request arriving, and the
		// caller's context ends while the service is still reading it. The
		// answer is then held back a moment, so the hang-up has certainly
		// landed by the time it arrives — a probe on the caller's context is
		// aborted the instant the context does, and one that is not cannot be
		// reached by it at all.
		once.Do(cancel)
		time.Sleep(20 * time.Millisecond)
		return anthropicfake.MessageOK(testModel, testUsage)
	}

	s.callDetached(t, ctx, callBody("real-work"))

	// The answer arrives on the state and the audit line rather than on the
	// connection, and it arrives after the call has returned — the client is
	// gone, so nothing here waits for the answer except this.
	waitFor(t, "the probe's answer to be recorded", func() bool { return auditLog.Len() > 0 })

	// The probe alone: the run's own call cannot be sent once its client has
	// gone, and a second request here would mean the service tried anyway.
	if got := s.up.Count(); got != 1 {
		t.Fatalf("the upstream saw %d requests, want the probe alone", got)
	}
	line, ok := probeLine(t, auditLog)
	if !ok {
		t.Fatal("a probe wrote no audit line")
	}
	if got := line["status"]; got != StatusOK {
		t.Errorf("the audit line says %q, want %q: the probe was answered, and a caller's hang-up is not the credential's", got, StatusOK)
	}
	if m, ok := s.state.mark("default"); !ok || !m.Available() {
		t.Errorf("the mark is %+v, want it cleared: the probe's answer was still recorded", m)
	}
}

// TestProbeDoublesOnFailure: a probe that did not work leaves the wait doubled,
// and the run is told the wait its own state now names — the probe's answer is
// never the run's answer, and the call is not sent to a credential that just
// failed to answer.
func TestProbeDoublesOnFailure(t *testing.T) {
	s, auditLog := probeSvc(t, nil)
	since := s.now.Add(-time.Hour)
	s.backedOff("default", since, creds.DefaultBackoffInitial)
	s.up.Script = []anthropicfake.Reply{
		anthropicfake.Error(http.StatusInternalServerError, "api_error", "overloaded"),
	}

	resp, body := s.call(t, callBody("real-work"))
	if resp.StatusCode != StatusExhaustedUntil {
		t.Fatalf("the call answered %d %s, want the typed wait", resp.StatusCode, body)
	}
	if got, want := wait(t, resp, body), s.now.Add(2*creds.DefaultBackoffInitial); !got.Equal(want) {
		t.Errorf("the run was told to wait until %s, want %s", got, want)
	}
	if got := s.up.Count(); got != 1 {
		t.Errorf("the upstream saw %d requests, want the probe alone: the run's call is not sent to a credential that failed its probe", got)
	}

	m, _ := s.state.mark("default")
	if m.State != creds.StateBackedOff {
		t.Errorf("a failed probe left state %q, want %q", m.State, creds.StateBackedOff)
	}
	if m.Backoff != 2*creds.DefaultBackoffInitial {
		t.Errorf("the wait in force is %s, want the first wait doubled (%s)", m.Backoff, 2*creds.DefaultBackoffInitial)
	}
	if want := s.now.Add(2 * creds.DefaultBackoffInitial); !m.Until.Equal(want) {
		t.Errorf("the mark holds until %s, want %s", m.Until, want)
	}
	if !m.Since.Equal(since) {
		t.Errorf("a failed probe moved Since to %s: the backoff belongs to the exhaustion it was set for (%s)", m.Since, since)
	}
	// The credential is held for exactly as long as the run was told to wait.
	if got := wait(t, resp, body); !got.Equal(m.Until) {
		t.Errorf("the run waits until %s and the mark holds until %s", got, m.Until)
	}

	line, ok := probeLine(t, auditLog)
	if !ok {
		t.Fatal("a probe wrote no audit line")
	}
	checkAuditShape(t, line)
	if got := line["status"]; got != ReasonExhausted {
		t.Errorf("the audit line's status is %q, want %q: the credential stays held, for longer", got, ReasonExhausted)
	}
	if got := line["purpose"]; got != PurposeProbe {
		t.Errorf("the audit line's purpose is %q, want %q", got, PurposeProbe)
	}
}

// TestProbeCappedAtWindow: the doubling stops at the cap. A credential already
// at the longest wait a probe can leave it with stays there — it is not doubled
// past the window the plan names.
func TestProbeCappedAtWindow(t *testing.T) {
	s, _ := probeSvc(t, nil)
	s.backedOff("default", s.now.Add(-6*time.Hour), creds.DefaultBackoffCap)
	s.up.Script = []anthropicfake.Reply{
		anthropicfake.Error(http.StatusInternalServerError, "api_error", "overloaded"),
	}

	resp, body := s.call(t, callBody("real-work"))
	if resp.StatusCode != StatusExhaustedUntil {
		t.Fatalf("the call answered %d %s, want the typed wait", resp.StatusCode, body)
	}
	m, _ := s.state.mark("default")
	if m.Backoff != creds.DefaultBackoffCap {
		t.Errorf("the wait in force is %s, want it capped at %s", m.Backoff, creds.DefaultBackoffCap)
	}
	if m.Until.After(s.now.Add(creds.DefaultBackoffCap)) {
		t.Errorf("the mark holds until %s, past the cap's own wait (%s)", m.Until, s.now.Add(creds.DefaultBackoffCap))
	}
	if got := wait(t, resp, body); !got.Equal(m.Until) {
		t.Errorf("the run waits until %s and the mark holds until %s", got, m.Until)
	}
}

// TestOneProbeAtATime: one probe per credential at a time. A live claim on the
// credential's slot is another instance's probe in flight, so this call asks
// nothing — and once the claim has lapsed, the probe is made and the claim it
// leaves is one that lapses on its own rather than a lock.
func TestOneProbeAtATime(t *testing.T) {
	s, auditLog := probeSvc(t, nil)
	s.backedOff("default", s.now.Add(-time.Hour), creds.DefaultBackoffInitial)
	s.probes.hold(t, "default", s.now.Add(time.Minute))
	s.up.Script = []anthropicfake.Reply{
		anthropicfake.MessageOK(testModel, testUsage),
		anthropicfake.MessageOK(testModel, testUsage),
	}

	// Somebody else is probing it now: the call is told to wait, and no request
	// leaves this instance.
	resp, body := s.call(t, callBody("real-work"))
	if resp.StatusCode != StatusExhaustedUntil {
		t.Fatalf("the call answered %d %s, want the typed wait", resp.StatusCode, body)
	}
	if got := s.up.Count(); got != 0 {
		t.Errorf("the upstream saw %d requests while another instance held the credential's probe claim", got)
	}
	noProbeLine(t, auditLog, "a probe nobody made")
	if m, _ := s.state.mark("default"); m.Backoff != creds.DefaultBackoffInitial {
		t.Errorf("the mark moved to %+v while another instance was probing", m)
	}

	// The claim lapses — the instance that held it died mid-probe — and the next
	// call probes, because a credential that can never be probed again is a
	// credential that never comes back.
	s.probes.hold(t, "default", s.now.Add(-time.Second))
	resp, body = s.call(t, callBody("real-work"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call after the lapsed claim answered %d %s, want it served", resp.StatusCode, body)
	}
	if got := s.up.Count(); got != 2 {
		t.Errorf("the upstream saw %d requests, want the probe and the run's call", got)
	}
	// The claim the probe took is let go once the probe is done with it: the
	// node stays (the store has no delete, deliberately) and it says who probed
	// and that they are finished, which is a claim that is not in force. What
	// stands for the whole TTL is only an instance that dies mid-probe and never
	// reaches the release.
	claim, ok := s.probes.claim(t, "default")
	if !ok {
		t.Fatal("the probe wrote no claim on the credential's slot")
	}
	if claim.Until.After(s.now) {
		t.Errorf("the claim stands until %s, want it released when the probe finished (%s)", claim.Until, s.now)
	}
	if claim.Run != s.run || claim.Slug != s.slug {
		t.Errorf("the claim names %s/%s, want the run that made the probe (%s/%s)", claim.Slug, claim.Run, s.slug, s.run)
	}
}

// TestWaitWhenAProbeIsOwedAndNotMade: a probe owed on a credential the
// selection could not hand back is due exactly BECAUSE the credential's own
// wait has passed, so the instant the selection answers with is behind the run
// by construction. A probe that is not made cannot move it, and the run must
// still be told a wait it can act on: the answer carries a short instant in the
// future (waitFloor) rather than the one that has gone.
//
// Left alone, every one of these four answers the run a reset_at in the past,
// the runner resumes at once (C1.17), the state has not moved, and the stage is
// re-run in a tight loop for as long as the condition lasts — which for a claim
// another instance holds is up to the claim's own ten minutes.
//
// The four are the ways a probe that was owed is not made: the slot is another
// instance's, the lease cannot be read at all, the claim keeps losing its
// compare-and-set, and the run's body names no model the service will put in a
// probe. In none of them may anything be sent upstream.
func TestWaitWhenAProbeIsOwedAndNotMade(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		set  func(*testing.T, *svc)
	}{{
		name: "the slot is another instance's",
		body: callBody("real-work"),
		set:  func(t *testing.T, s *svc) { s.probes.hold(t, "default", s.now.Add(time.Minute)) },
	}, {
		name: "the lease cannot be read",
		body: callBody("real-work"),
		set:  func(_ *testing.T, s *svc) { s.probes.err = errors.New("the database is unreachable") },
	}, {
		name: "the claim keeps losing its compare-and-set",
		body: callBody("real-work"),
		set:  func(_ *testing.T, s *svc) { s.probes.stale = true },
	}} {
		// A body that names no model is not a case here any more: the request check
		// (C1.11 follow-up) refuses it with a 400 before the probe is reached.
		t.Run(tc.name, func(t *testing.T) {
			s, auditLog := probeSvc(t, nil)
			s.backedOff("default", s.now.Add(-time.Hour), creds.DefaultBackoffInitial)
			tc.set(t, s)

			resp, body := s.call(t, tc.body)
			if resp.StatusCode != StatusExhaustedUntil {
				t.Fatalf("the call answered %d %s, want the typed wait", resp.StatusCode, body)
			}
			at := wait(t, resp, body)
			if !at.After(s.now) {
				t.Errorf("the run was told to wait until %s, which is not after now (%s): a runner resuming there re-runs the stage at once", at, s.now)
			}
			if want := s.now.Add(waitFloor); !at.Equal(want) {
				t.Errorf("the run was told to wait until %s, want the floor (%s)", at, want)
			}
			if got := s.up.Count(); got != 0 {
				t.Errorf("the upstream saw %d requests, want none: no probe was made", got)
			}
			noProbeLine(t, auditLog, "a probe nobody made")
		})
	}
}

// TestTheProbesOwnWorkIsBoundedUnderItsClaim pins the relationship the two
// bounds exist for: the claim is what keeps a second instance off the
// credential, and it is a bound rather than a lock, so a probe that is still
// running when its claim lapses is a probe another instance may start beside
// it. The call and the recording together must therefore end inside the claim.
//
// It reads the bounds off the contexts the probe itself would run on rather
// than adding the two constants up: a sum is green whatever budget the
// recording is actually given, which is exactly the way this can go wrong (the
// recording given the call's five minutes puts the whole of the work back at
// the claim's ten).
func TestTheProbesOwnWorkIsBoundedUnderItsClaim(t *testing.T) {
	// slack is how much of a budget may have run between building the context
	// and reading its deadline. It is far below the gap between the two budgets,
	// so the measurements below still tell them apart.
	const slack = 30 * time.Second
	parent, cancel := context.WithCancel(context.Background())
	callCtx, callDone := probeCallContext(parent)
	defer callDone()
	recordCtx, recordDone := probeRecordContext(parent)
	defer recordDone()

	// Neither is the caller's: the client hanging up is not the probe's answer.
	cancel()
	if err := callCtx.Err(); err != nil {
		t.Errorf("the probe's call runs on the caller's context: %v", err)
	}
	if err := recordCtx.Err(); err != nil {
		t.Errorf("the probe's recording runs on the caller's context: %v", err)
	}

	// Each phase is bounded by its own budget, and nothing else's.
	if got := remaining(t, callCtx); got > probeBudget || got <= probeBudget-slack {
		t.Errorf("the probe's call is bounded by %s, want %s", got, probeBudget)
	}
	if got := remaining(t, recordCtx); got > probeRecordBudget || got <= probeRecordBudget-slack {
		t.Errorf("the probe's recording is bounded by %s, want %s of its own, not the call's %s", got, probeRecordBudget, probeBudget)
	}

	// And the two together end inside the claim.
	if got := probeBudget + probeRecordBudget; got >= probeLeaseTTL {
		t.Errorf("a probe's own work may run for %s, want under the %s its claim stands for: past that, two instances probe the same credential together", got, probeLeaseTTL)
	}
}

// remaining is how long a context has left before its own deadline.
func remaining(t *testing.T, ctx context.Context) time.Duration {
	t.Helper()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("the context has no deadline of its own")
	}
	return time.Until(deadline)
}

// TestProbeClaimDoesNotBlockTheNextProbe is the release, as an installation
// experiences it. A policy may set a first backoff shorter than the probe
// claim's own TTL (projectpolicy accepts a minute), so a credential can be due
// for its second probe long before the claim written by its first one would
// have lapsed. If the probe held the claim for the whole TTL, that second probe
// would be refused — and the run would be answered the wait in force, which is
// already in the past by then, so a runner that acts on the typed wait re-runs
// the stage in a loop until the claim expires.
func TestProbeClaimDoesNotBlockTheNextProbe(t *testing.T) {
	s, _ := probeSvc(t, func(sv *svc, _ *Config) {
		sv.policy.shared = decodeTestPolicy(t, testPolicyDocBackoff("1m", "5h"))
	})
	since := s.now.Add(-time.Hour)
	s.backedOff("default", since, time.Minute)
	s.up.Script = []anthropicfake.Reply{
		anthropicfake.Error(http.StatusInternalServerError, "api_error", "overloaded"),
		anthropicfake.MessageOK(testModel, testUsage),
		anthropicfake.MessageOK(testModel, testUsage),
	}

	// The first call probes, the probe fails, and the wait doubles — to two
	// minutes, which is well inside the claim's ten.
	resp, body := s.call(t, callBody("real-work"))
	if resp.StatusCode != StatusExhaustedUntil {
		t.Fatalf("the first call answered %d %s, want the typed wait", resp.StatusCode, body)
	}
	if m, _ := s.state.mark("default"); m.Backoff != 2*time.Minute {
		t.Fatalf("the wait in force is %s, want the first wait doubled (2m)", m.Backoff)
	}

	// The two minutes pass. The second call probes again — the credential's own
	// slot is free the moment its first probe finished — and this probe works,
	// so the call is served rather than answered a wait that is behind it.
	s.backedOff("default", since, time.Minute)
	resp, body = s.call(t, callBody("real-work"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the second call answered %d %s, want it served: the first probe's claim must not refuse the next probe", resp.StatusCode, body)
	}
	if got := s.up.Count(); got != 3 {
		t.Errorf("the upstream saw %d requests, want two probes and the run's call", got)
	}
	if m, _ := s.state.mark("default"); !m.Available() {
		t.Errorf("the second probe's success left the mark at %+v, want it cleared", m)
	}
}

// TestProbeOnAUsageLimit: a probe answered by a usage limit is the exhaustion
// the answer reports, and the reset it names is the wait the run is given. The
// credential is not asked again in this call, and the audit line says the
// credential stayed held.
func TestProbeOnAUsageLimit(t *testing.T) {
	s, auditLog := probeSvc(t, nil)
	s.backedOff("default", s.now.Add(-time.Hour), creds.DefaultBackoffInitial)
	reset := s.now.Add(10 * time.Minute)
	reply := anthropicfake.Error(http.StatusTooManyRequests, "rate_limit_error", "usage limit reached")
	reply.Header = http.Header{"Retry-After": []string{"600"}}
	s.up.Script = []anthropicfake.Reply{reply}

	resp, body := s.call(t, callBody("real-work"))
	if resp.StatusCode != StatusExhaustedUntil {
		t.Fatalf("the call answered %d %s, want the typed wait", resp.StatusCode, body)
	}
	if got := wait(t, resp, body); !got.Equal(reset) {
		t.Errorf("the run was told to wait until %s, want the reset the probe reported (%s)", got, reset)
	}
	if got := s.up.Count(); got != 1 {
		t.Errorf("the upstream saw %d requests, want the probe alone", got)
	}

	m, _ := s.state.mark("default")
	if m.State != creds.StateExhausted {
		t.Errorf("a probe answered 429 left state %q, want %q", m.State, creds.StateExhausted)
	}
	if !m.Until.Equal(reset) {
		t.Errorf("the mark holds until %s, want the reported reset (%s)", m.Until, reset)
	}
	if !m.Since.Equal(s.now) {
		t.Errorf("the mark is dated %s, want the instant the exhaustion was observed (%s)", m.Since, s.now)
	}

	line, ok := probeLine(t, auditLog)
	if !ok {
		t.Fatal("a probe wrote no audit line")
	}
	checkAuditShape(t, line)
	if got := line["status"]; got != ReasonExhausted {
		t.Errorf("the audit line's status is %q, want %q", got, ReasonExhausted)
	}
}

// TestProbeOnAUsageLimitWithNoReset: the other half of the same arm. A usage
// limit that names no reset time is C1.9's backoff — a credential exhausted
// without saying when it comes back — so the wait doubles from the wait in
// force, and Since moves, because the credential WAS exhausted again (unlike a
// 5xx, which is the same exhaustion reported once more).
func TestProbeOnAUsageLimitWithNoReset(t *testing.T) {
	s, auditLog := probeSvc(t, nil)
	s.backedOff("default", s.now.Add(-time.Hour), creds.DefaultBackoffInitial)
	s.up.Script = []anthropicfake.Reply{
		anthropicfake.Error(http.StatusTooManyRequests, "rate_limit_error", "usage limit reached"),
	}

	resp, body := s.call(t, callBody("real-work"))
	if resp.StatusCode != StatusExhaustedUntil {
		t.Fatalf("the call answered %d %s, want the typed wait", resp.StatusCode, body)
	}
	m, _ := s.state.mark("default")
	if m.State != creds.StateBackedOff {
		t.Errorf("a 429 with no reset left state %q, want %q: it is the backed-off case", m.State, creds.StateBackedOff)
	}
	if m.Backoff != 2*creds.DefaultBackoffInitial {
		t.Errorf("the wait in force is %s, want the first wait doubled (%s)", m.Backoff, 2*creds.DefaultBackoffInitial)
	}
	if want := s.now.Add(2 * creds.DefaultBackoffInitial); !m.Until.Equal(want) {
		t.Errorf("the mark holds until %s, want %s", m.Until, want)
	}
	if !m.Since.Equal(s.now) {
		t.Errorf("the mark is dated %s, want the instant of this exhaustion (%s): the credential was exhausted again", m.Since, s.now)
	}
	if got := wait(t, resp, body); !got.Equal(m.Until) {
		t.Errorf("the run waits until %s and the mark holds until %s", got, m.Until)
	}
	line, ok := probeLine(t, auditLog)
	if !ok {
		t.Fatal("a probe wrote no audit line")
	}
	if got := line["status"]; got != ReasonExhausted {
		t.Errorf("the audit line's status is %q, want %q", got, ReasonExhausted)
	}
}

// TestNoProbeOnThePaidKeyUnderOAuthOnly is D152 on the probe's own path: under
// `oauth-only` the paid tier is not in the cascade, so a probe must not reach it
// either — a probe on the paid key is a real billable call, and a successful one
// would clear its mark and hand the next selection a usable paid key under a
// policy that says never. The fixture's bundle holds the project's key
// (`testBundleDoc`) and the policy admits only the shared account, and the paid
// key's mark is deliberately the more overdue of the two, so a probe that chose
// by overdue-ness alone would choose it.
func TestNoProbeOnThePaidKeyUnderOAuthOnly(t *testing.T) {
	s, auditLog := probeSvc(t, nil)
	// The paid key's name is the bundle's, not the test's: the document's keys
	// are the platform's own (config.AnthropicSecret), so it is read off the
	// bundle the service serves rather than spelled here — a mark planted under
	// a name no credential has would make this test pass by never being
	// eligible in the first place.
	paidName := s.tierBName(t)
	// The paid key is planted as the EARLIEST due of the two, so a probe that
	// only consults the marks — one that did not ask the policy which tiers it
	// admits — picks it. A tie would be answered by the bundle's order, and the
	// test would pass without ever exercising the filter.
	paid := s.backedOffDue(paidName, s.now.Add(-time.Hour), creds.DefaultBackoffInitial, s.now.Add(-30*time.Minute))
	s.backedOff("default", s.now.Add(-time.Minute), creds.DefaultBackoffInitial)
	// One reply more than the correct path needs, so that a service that probes
	// the paid key as well is still able to answer the run: the assertions below
	// then say what went wrong instead of the fixture running out of script.
	s.up.Script = []anthropicfake.Reply{
		anthropicfake.MessageOK(testModel, testUsage),
		anthropicfake.MessageOK(testModel, testUsage),
		anthropicfake.MessageOK(testModel, testUsage),
	}

	resp, body := s.call(t, callBody("real-work"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call answered %d %s, want it served by the shared credential", resp.StatusCode, body)
	}
	seen := s.up.Seen()
	// Every request and every header of it, not request zero's Authorization: a
	// service that probed the paid key would spend it and then probe the shared
	// one too, so the evidence is not only in the first request — and a tier-B
	// credential is attached as x-api-key (forward.go), so the bearer header
	// alone says nothing about the key this test is named for.
	for _, r := range seen {
		for k, vs := range r.Header {
			for _, v := range vs {
				if strings.Contains(v, fakeCred(t, "paid")) {
					t.Fatalf("a request carried the project's paid key in %s, under a policy that never spends it", k)
				}
			}
		}
	}
	if len(seen) != 2 {
		t.Fatalf("the upstream saw %d requests, want a probe and the run's call", len(seen))
	}
	auth := seen[0].Header.Get("Authorization")
	if !strings.Contains(auth, fakeCred(t, "default")) {
		t.Error("the probe did not carry the shared credential the policy admits")
	}
	if m, ok := s.state.mark(paidName); !ok || m != paid {
		t.Errorf("the paid key's mark is %+v, want it untouched (%+v)", m, paid)
	}
	line, ok := probeLine(t, auditLog)
	if !ok {
		t.Fatal("a probe wrote no audit line")
	}
	if got := line["credential"]; got != "default" {
		t.Errorf("the audit line names %q, want the shared credential that was probed", got)
	}
}

// TestProbeCandidatesFollowThePolicy pins probeCandidates' two answers directly,
// because the selection path can only show the one that is in force: the paid
// tier is offered a probe under `fallback: auto` and withdrawn under
// `oauth-only`, and a Settings built without a fallback (the zero value) is the
// withdrawing one.
func TestProbeCandidatesFollowThePolicy(t *testing.T) {
	cands := []creds.Credential{
		{Name: "default", Tier: creds.TierA},
		{Name: "paid", Tier: creds.TierB},
	}
	for _, tc := range []struct {
		name string
		set  creds.Settings
		want int
	}{
		{"oauth-only", creds.Settings{Fallback: creds.OAuthOnly}, 1},
		{"no fallback written", creds.Settings{}, 1},
		{"auto", creds.Settings{Fallback: creds.Auto}, 2},
	} {
		got := probeCandidates(cands, tc.set)
		if len(got) != tc.want {
			t.Errorf("%s: %d candidates are open to a probe, want %d", tc.name, len(got), tc.want)
		}
		for _, c := range got {
			if c.Tier != creds.TierA && !tc.set.Fallback.Spends() {
				t.Errorf("%s: %q of tier %s is open to a probe under a policy that does not spend", tc.name, c.Name, c.Tier)
			}
		}
	}
}

// TestNoProbeOnBroken: a broken credential is never probed. A 401 is the
// operator's to clear (C1.21) and a probe asks a different question — whether a
// wait is over — so nothing about it is asked here, nothing is sent upstream,
// and the mark is untouched.
func TestNoProbeOnBroken(t *testing.T) {
	s, auditLog := probeSvc(t, nil)
	broken := s.broken("default", s.now.Add(-time.Hour))
	s.up.Script = []anthropicfake.Reply{anthropicfake.MessageOK(testModel, testUsage)}

	resp, body := s.call(t, callBody("real-work"))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the call answered %d %s, want the refusal a broken credential leaves", resp.StatusCode, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorStateUnavailable {
		t.Errorf("the refusal is %q, want %q", e.Error, ErrorStateUnavailable)
	}
	if got := s.up.Count(); got != 0 {
		t.Errorf("the upstream saw %d requests for a broken credential", got)
	}
	noProbeLine(t, auditLog, "a probe of a broken credential")
	if m, _ := s.state.mark("default"); m.State != broken.State || !m.Since.Equal(broken.Since) {
		t.Errorf("the broken mark moved to %+v, want %+v", m, broken)
	}
}

// TestProbeOnA401MarksBroken: an answer that says the token is dead is D139's
// mark, not a doubled wait. A credential whose probe was refused is out until an
// operator replaces the token (C1.21), and the audit line says so.
func TestProbeOnA401MarksBroken(t *testing.T) {
	s, auditLog := probeSvc(t, nil)
	s.backedOff("default", s.now.Add(-time.Hour), creds.DefaultBackoffInitial)
	s.up.Script = []anthropicfake.Reply{
		anthropicfake.Error(http.StatusUnauthorized, "authentication_error", "this token is not accepted"),
	}

	resp, body := s.call(t, callBody("real-work"))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the call answered %d %s, want a refusal", resp.StatusCode, body)
	}
	m, _ := s.state.mark("default")
	if m.State != creds.StateBroken {
		t.Errorf("a 401 on a probe left state %q, want %q", m.State, creds.StateBroken)
	}
	if got := s.up.Count(); got != 1 {
		t.Errorf("the upstream saw %d requests, want the probe alone", got)
	}
	line, ok := probeLine(t, auditLog)
	if !ok {
		t.Fatal("a probe wrote no audit line")
	}
	if got := line["status"]; got != ReasonBroken {
		t.Errorf("the audit line's status is %q, want %q", got, ReasonBroken)
	}
}

// TestNoProbeOnAnUnreadableMark: a mark carrying a state this release does not
// read is a hold it will not take off (C1.9's Known), and a probe is not its
// question — NeedsProbe is false for it. The credential is not asked, nothing
// moves, and the node is left for the operator.
func TestNoProbeOnAnUnreadableMark(t *testing.T) {
	s, auditLog := probeSvc(t, nil)
	unknown := creds.Mark{
		State: creds.State("hibernating"),
		Since: s.now.Add(-time.Hour),
		Until: s.now.Add(-time.Minute),
	}
	s.state.mu.Lock()
	s.state.marks["default"] = unknown
	s.state.mu.Unlock()
	s.up.Script = []anthropicfake.Reply{anthropicfake.MessageOK(testModel, testUsage)}

	_, _ = s.call(t, callBody("real-work"))
	if got := s.up.Count(); got != 0 {
		t.Errorf("the upstream saw %d requests for a mark this release does not read", got)
	}
	noProbeLine(t, auditLog, "a probe of a mark this release does not read")
	if m, _ := s.state.mark("default"); m != unknown {
		t.Errorf("the mark is %+v, want the node this release does not read left alone (%+v)", m, unknown)
	}
}

// TestProbeFailsClosedWhenItCannotBeAudited: an act this service could not audit
// is not made (D143). The probe itself is out of this service's hands by the
// time its line is written, so what is withheld is its EFFECT: the mark is left
// exactly as it was and the run is answered the wait it was going to be answered
// — a credential is not put back in rotation by a probe nobody recorded.
func TestProbeFailsClosedWhenItCannotBeAudited(t *testing.T) {
	s, _ := probeSvc(t, func(_ *svc, cfg *Config) {
		cfg.AuditLog = silentHandler{}
	})
	before := s.backedOff("default", s.now.Add(-time.Hour), creds.DefaultBackoffInitial)
	s.up.Script = []anthropicfake.Reply{anthropicfake.MessageOK(testModel, testUsage)}

	resp, body := s.call(t, callBody("real-work"))
	if resp.StatusCode != StatusExhaustedUntil {
		t.Fatalf("the call answered %d %s, want the typed wait", resp.StatusCode, body)
	}
	if m, _ := s.state.mark("default"); m != before {
		t.Errorf("the mark is %+v, want it untouched: the probe's result could not be audited (%+v)", m, before)
	}
	if !strings.Contains(s.logs.String(), "could not be audited") {
		t.Errorf("the service's log says nothing about the line it could not write: %s", s.logs.String())
	}
}

// TestProbeCarriesNoneOfTheRunsHeaders: a probe is this service's own call. The
// two headers below are ones the tier-A allow-list does forward from a run, so
// the contrast is real: the run's own call carries them, and the probe beside it
// carries the service's own set and nothing else.
func TestProbeCarriesNoneOfTheRunsHeaders(t *testing.T) {
	s, _ := probeSvc(t, nil)
	s.backedOff("default", s.now.Add(-time.Hour), creds.DefaultBackoffInitial)
	s.up.Script = []anthropicfake.Reply{
		anthropicfake.MessageOK(testModel, testUsage),
		anthropicfake.MessageOK(testModel, testUsage),
	}

	resp, body := s.call(t, callBody("real-work"), func(r *http.Request) {
		r.Header.Set("Anthropic-Beta", "probe-test-beta")
		r.Header.Set("X-App", "probe-test-app")
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call answered %d %s, want it served after the probe", resp.StatusCode, body)
	}
	seen := s.up.Seen()
	if len(seen) != 2 {
		t.Fatalf("the upstream saw %d requests, want the probe and the run's call", len(seen))
	}
	for _, name := range []string{"Anthropic-Beta", "X-App"} {
		if got := seen[0].Header.Get(name); got != "" {
			t.Errorf("the probe carried the run's %s header (%q)", name, got)
		}
		if got := seen[1].Header.Get(name); got == "" {
			t.Errorf("the run's own call did not carry its %s header", name)
		}
	}
	if got := seen[0].Header.Get("Anthropic-Version"); got != probeVersion {
		t.Errorf("the probe asked with Anthropic-Version %q, want %q: Check 90's one mandatory header", got, probeVersion)
	}
	if got := seen[0].Header.Get("Authorization"); got == "" {
		t.Error("the probe carried no credential at all")
	}
	if got := seen[0].Header.Get(HeaderRunIdentity); got != "" {
		t.Error("the probe carried the run's identity token")
	}
}

// TestProbeOnTheCountTokensPath: a probe asks on the endpoint the run is about
// to use, and the count-tokens API takes no max_tokens — so the body a probe
// builds for it is the other shape, and the run's own call is still the one that
// follows it.
func TestProbeOnTheCountTokensPath(t *testing.T) {
	s, _ := probeSvc(t, nil)
	s.backedOff("default", s.now.Add(-time.Hour), creds.DefaultBackoffInitial)
	s.up.Script = []anthropicfake.Reply{
		{Status: http.StatusOK, Body: `{"input_tokens":1}`},
		{Status: http.StatusOK, Body: `{"input_tokens":7}`},
	}

	resp, body := s.callPath(t, CountTokensPath,
		`{"model":"`+testModel+`","messages":[{"role":"user","content":"count-this"}]}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"input_tokens":7`) {
		t.Fatalf("the call answered %d %s, want the upstream's answer to the run's own call", resp.StatusCode, body)
	}
	if got := s.up.Count(); got != 2 {
		t.Fatalf("the upstream saw %d requests, want the probe and the run's call", got)
	}
	probe := string(s.up.Body(0))
	if strings.Contains(probe, "max_tokens") {
		t.Errorf("the probe sent max_tokens to the count-tokens endpoint: %s", probe)
	}
	if strings.Contains(probe, "count-this") {
		t.Errorf("the probe carried the run's own body: %s", probe)
	}
	if !strings.Contains(probe, `"model":"`+testModel+`"`) {
		t.Errorf("the probe does not ask about the model the run named: %s", probe)
	}
}

// TestProbeSuccessInsideTheWindow: a successful probe does not put a shared
// token back in rotation inside the window its exhaustion opened (D142's first
// control, which ProbeSucceeded applies and which the probe must hand it the
// right tier for). The mark becomes an exhaustion dated from the exhaustion that
// opened the window, the run is told to wait for the window's end, and the
// credential serves nothing until then.
func TestProbeSuccessInsideTheWindow(t *testing.T) {
	s, auditLog := probeSvc(t, func(sv *svc, _ *Config) {
		sv.policy.shared = decodeTestPolicy(t, testPolicyDocControls("oauth-only", true, 0))
	})
	since := s.now.Add(-time.Hour)
	s.backedOff("default", since, creds.DefaultBackoffInitial)
	s.up.Script = []anthropicfake.Reply{anthropicfake.MessageOK(testModel, testUsage)}

	resp, body := s.call(t, callBody("real-work"))
	if resp.StatusCode != StatusExhaustedUntil {
		t.Fatalf("the call answered %d %s, want the typed wait: the window holds the token", resp.StatusCode, body)
	}
	if got := s.up.Count(); got != 1 {
		t.Errorf("the upstream saw %d requests, want the probe alone", got)
	}
	m, _ := s.state.mark("default")
	if m.State != creds.StateExhausted {
		t.Errorf("a successful probe left state %q, want %q", m.State, creds.StateExhausted)
	}
	if !m.Since.Equal(since) {
		t.Errorf("the window is measured from %s, want the exhaustion that opened it (%s)", m.Since, since)
	}
	if want := since.Add(creds.DefaultBackoffCap); !m.Until.Equal(want) {
		t.Errorf("the mark holds until %s, want the window's end (%s)", m.Until, want)
	}
	if got := wait(t, resp, body); !got.Equal(m.Until) {
		t.Errorf("the run waits until %s and the mark holds until %s", got, m.Until)
	}
	line, ok := probeLine(t, auditLog)
	if !ok {
		t.Fatal("a probe wrote no audit line")
	}
	if got := line["status"]; got != StatusOK {
		t.Errorf("the audit line's status is %q, want %q: the probe worked, the control is what holds the token", got, StatusOK)
	}
}

// TestScrubRemovesWhatTheLoaderHolds is the arming audit.go insists on: the
// service's redaction is the bundle loader's, so a credential's value cannot
// reach an audit line even where a caller hands one instead of a name.
func TestScrubRemovesWhatTheLoaderHolds(t *testing.T) {
	s, auditLog := probeSvc(t, nil)
	loader := s.server.cfg.Bundles
	if _, err := loader.Load(context.Background()); err != nil {
		t.Fatalf("loading the bundle: %v", err)
	}
	value := fakeCred(t, "default")
	if got := loader.Scrub("credential=" + value + ";"); strings.Contains(got, value) {
		t.Errorf("the loader's scrub left a credential's value in %q", got)
	}
	if got := loader.Scrub("a name that is not a value"); got != "a name that is not a value" {
		t.Errorf("the scrub rewrote text that holds no value: %q", got)
	}
	// And the auditor the service built is armed with it: a value handed where a
	// name belongs is removed before any rule sees it, so the line an operator
	// reads carries the redaction and never the value. This is the one place a
	// value could have reached an audit line, and it is reached on purpose.
	id := Identity{Slug: s.slug, Run: s.run, Job: s.job, JobAccount: testJobSA}
	if err := s.server.audit.Write(context.Background(), id.Audit(AuditEvent{
		Credential: value, Tier: TierA, Purpose: PurposeProbe, Status: StatusOK,
	})); err != nil {
		t.Fatalf("the auditor refused a line it should have redacted rather than refused: %v", err)
	}
	if strings.Contains(auditLog.String(), value) {
		t.Errorf("the audit line carries the credential's value: %s", auditLog.String())
	}
	line, ok := probeLine(t, auditLog)
	if !ok {
		t.Fatal("the auditor wrote no line")
	}
	checkAuditShape(t, line)
	if got := line["credential"]; got != redactedValue {
		t.Errorf("the credential field is %q, want the redaction %q", got, redactedValue)
	}
}

// TestAScrubBeforeTheFirstLoadRemovesNothing pins the OTHER branch of Scrub:
// the one the loader answers when it has read no document yet (l.cached is nil),
// which is reachable for an audit line and not only off the end of a call's
// own path. The auditor every line of this service is written through is armed
// with this loader (NewProbeAuditor), so every refusal answered between the
// identity check and the first bundle read — here, a policy that cannot be read
// — is scrubbed against no values at all before the line is written.
//
// Two things are held here and not left to the comment on that branch: the
// scrub returns its input unchanged when the loader holds nothing (it neither
// refuses the text nor invents a redaction for a value it cannot know), and the
// line written through it still lands — the pre-load refusal writes its one
// audit line, and the bundle was never read, which is what makes the branch the
// one that answered. A scrub that started refusing pre-load text, or redacting
// it, would show up as a call whose refusal left no line at all.
//
// Mutation (run, restore): make the b == nil branch return redactedValue
// instead of s, or refuse the text, and the line this call writes is mangled or
// missing and this test is red.
func TestAScrubBeforeTheFirstLoadRemovesNothing(t *testing.T) {
	s, auditLog := auditSvc(t, func(s *svc, _ *Config) {
		s.policy.sharedErr = errors.New("the shared node could not be read")
	})
	loader := s.server.cfg.Bundles
	value := fakeCred(t, "default")
	if got := loader.Scrub("credential=" + value + ";"); got != "credential="+value+";" {
		t.Errorf("a scrub before the first load rewrote text it holds no value to remove: %q", got)
	}

	resp, body := s.call(t, callBody("pre-load-refusal"))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
	}
	if n := s.src.readCount(); n != 0 {
		t.Errorf("the bundle was read %d times, want 0: this refusal is the one answered before any document is in the cache", n)
	}
	lines := forwardLines(t, auditLog)
	if len(lines) != 1 {
		t.Fatalf("the pre-load refusal wrote %d audit lines, want exactly 1: %v", len(lines), lines)
	}
	checkAuditShape(t, lines[0])
	if got := lines[0]["status"]; got != ErrorStateUnavailable {
		t.Errorf("the pre-load refusal's audit line status is %v, want %q", got, ErrorStateUnavailable)
	}
}

// TestNewProbeAuditorRequiresALoader: the arming is not a caller's to skip. An
// auditor with no loader has nothing to redact with, so it does not exist.
func TestNewProbeAuditorRequiresALoader(t *testing.T) {
	if _, err := NewProbeAuditor(slog.NewJSONHandler(&bytes.Buffer{}, nil), nil); err == nil {
		t.Fatal("an auditor was built with no bundle loader to redact with")
	}
	if _, err := NewProbeAuditor(nil, &BundleLoader{}); err == nil {
		t.Fatal("an auditor was built with no handler")
	}
	a, err := NewProbeAuditor(slog.NewJSONHandler(&bytes.Buffer{}, nil), &BundleLoader{})
	if err != nil || a == nil {
		t.Fatalf("a loader and a handler built no auditor: %v", err)
	}
}

// TestServiceNeedsAProbeLeaseAndAnAuditHandler: the two things the probe cannot
// run without are required at construction, because a service that cannot probe
// is a service whose backed-off credentials never come back — ProbeSucceeded is
// the only transition off that mark (C1.9).
func TestServiceNeedsAProbeLeaseAndAnAuditHandler(t *testing.T) {
	var captured *Config
	newSvcWith(t, func(_ *svc, cfg *Config) { captured = cfg })
	if captured == nil {
		t.Fatal("the fixture handed the test no configuration")
	}
	for _, miss := range []string{"probes", "audit"} {
		c := *captured
		switch miss {
		case "probes":
			c.Probes = nil
		case "audit":
			c.AuditLog = nil
		}
		if _, err := NewServer(c); err == nil {
			t.Errorf("NewServer accepted a configuration with no %s", miss)
		}
	}
	// And the fixture's own configuration, which has both, is one it accepts.
	if _, err := NewServer(*captured); err != nil {
		t.Errorf("NewServer refused the fixture's own configuration: %v", err)
	}
}

// ---------------------------------------------------------------------------
// The probe's own pieces
// ---------------------------------------------------------------------------

// TestProbeCallIsBuiltByTheService pins the fields of the minimal call: the
// run's path, the run's model, this service's headers, and the caller's tokens
// carried as scrubs — so nothing of the caller's reaches the upstream even if a
// later change copied a header.
func TestProbeCallIsBuiltByTheService(t *testing.T) {
	cl := &call{
		path:   MessagesPath,
		body:   []byte(callBody("real-work")),
		header: http.Header{"Anthropic-Beta": []string{"run-set-this"}},
		scrubs: []string{"run-identity-token", "job-id-token"},
	}
	got, ok := probeCall(cl)
	if !ok {
		t.Fatal("a Messages call with a model in it built no probe")
	}
	if got.path != MessagesPath {
		t.Errorf("the probe asks on %q, want the run's own path %q", got.path, MessagesPath)
	}
	if string(got.body) == string(cl.body) || !strings.Contains(string(got.body), testModel) {
		t.Errorf("the probe's body is not the minimal one for the run's model: %s", got.body)
	}
	if got.header.Get("Anthropic-Beta") != "" {
		t.Error("the probe copied the run's headers")
	}
	if len(got.scrubs) != 2 {
		t.Errorf("the probe carries %d scrubs, want the caller's two tokens", len(got.scrubs))
	}
	// A body with no model, and a path this service does not serve: no probe,
	// and so no answer that is not the run's.
	if _, ok := probeCall(&call{path: MessagesPath, body: []byte(`{"max_tokens":1}`)}); ok {
		t.Error("a call with no model built a probe")
	}
	if _, ok := probeCall(&call{path: "/v1/complete", body: []byte(callBody("x"))}); ok {
		t.Error("a call on a path this service does not serve built a probe")
	}
}

// TestAProbedCallsAnswerCarriesTheTiersItsProbesWereMadeOn is the probe's other
// half (C1.16): the probe's call was made, so it is the run's to account for,
// and the run can only account for it if the answer says which tier it was made
// on. A probe is accounted notional on tier A and money on any other tier, so
// the tier is the whole of what the run needs — and it is the tier the audit
// line above carries, never the name: which credential was probed is this
// service's own record.
//
// The header is on the answer the run is given, and only there: a probe's own
// answer is closed unread (probeOne), so a run's ledger learns of the probe
// from the call it was actually waiting for.
func TestAProbedCallsAnswerCarriesTheTiersItsProbesWereMadeOn(t *testing.T) {
	s, _ := probeSvc(t, nil)
	s.backedOff("default", s.now.Add(-time.Hour), creds.DefaultBackoffInitial)
	s.up.Script = []anthropicfake.Reply{
		{Status: http.StatusOK, Body: `{"type":"message","probe_answer":"probe-answer"}`},
		anthropicfake.MessageOK(testModel, testUsage),
		anthropicfake.MessageOK(testModel, testUsage),
	}

	resp, body := s.call(t, callBody("real-work"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call was not served after its probe: %d %s", resp.StatusCode, body)
	}
	// The header is on the run's own answer, and not on the probe's — which
	// this service has already closed.
	if !strings.Contains(string(body), "msg_fake") {
		t.Fatalf("the answer is not the upstream's answer to the run's own call: %s", body)
	}
	tiers, ok, err := ProbedTiers(resp.Header)
	if err != nil {
		t.Fatalf("the answer's probe header is not one this contract reads: %v", err)
	}
	if !ok {
		t.Fatal("the answer of a call whose credential was probed carries no probe header")
	}
	if len(tiers) != 1 || tiers[0] != TierA {
		t.Errorf("the answer names the probed tiers %v, want the one tier the probe was made on", tiers)
	}
	if v := resp.Header.Get(HeaderProbe); v != TierA {
		t.Errorf("the header says %q, want the tier alone: %q", v, TierA)
	}

	// The next call is served by the cleared credential, needs no probe, and so
	// carries no probe header at all: a header written for a probe that was not
	// made would be a run charged for a call nobody made.
	next, _ := s.call(t, callBody("real-work-again"))
	if tiers, ok, err := ProbedTiers(next.Header); err != nil || ok {
		t.Errorf("a call that made no probe carries %v (%v), want no probe header", tiers, err)
	}
}
