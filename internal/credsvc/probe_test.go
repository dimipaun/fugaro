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
func probeSvc(t *testing.T, mutate func(*svc, *Config)) (*svc, *bytes.Buffer) {
	t.Helper()
	auditLog := &bytes.Buffer{}
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

// backedOff plants the state a probe is owed for: a backed-off mark whose own
// wait has passed, which is what creds answers NeedsProbe for and what the
// selection path probes. since is when the exhaustion that opened it happened,
// and backoff is the wait in force.
func (s *svc) backedOff(name string, since time.Time, backoff time.Duration) creds.Mark {
	m := creds.Mark{
		State:   creds.StateBackedOff,
		Since:   since,
		Backoff: backoff,
		Until:   s.now.Add(-time.Minute),
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	s.state.marks[name] = m
	return m
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

// probeLine is the one audit line a probe writes, and whether there is exactly
// one.
func probeLine(t *testing.T, buf *bytes.Buffer) (map[string]any, bool) {
	t.Helper()
	if buf.Len() == 0 {
		return nil, false
	}
	lines := auditLines(t, buf)
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
	if auditLog.Len() != 0 {
		t.Errorf("a probe nobody made wrote an audit line: %s", auditLog.String())
	}
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
	claim, ok := s.probes.claim(t, "default")
	if !ok {
		t.Fatal("the probe wrote no claim on the credential's slot")
	}
	if want := s.now.Add(probeLeaseTTL); !claim.Until.Equal(want) {
		t.Errorf("the claim stands until %s, want %s: a bound that lapses, not a lock", claim.Until, want)
	}
	if claim.Run != s.run || claim.Slug != s.slug {
		t.Errorf("the claim names %s/%s, want the run that made the probe (%s/%s)", claim.Slug, claim.Run, s.slug, s.run)
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
	if auditLog.Len() != 0 {
		t.Errorf("a probe of a broken credential wrote an audit line: %s", auditLog.String())
	}
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
	if auditLog.Len() != 0 {
		t.Errorf("a probe was audited for a mark this release does not read: %s", auditLog.String())
	}
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
