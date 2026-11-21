package credsvc

// WS8: the workspace's cap where the money is actually spent — the paid-call
// path of the service that serves a workspace's members
// (docs/workspaces/spec.md §5, D1301 as amended, F10, F18, R15).
//
// The meter itself is W7's and its tests are its own (workspace_cap_test.go).
// What these tests hold is the bracket around ONE upstream call: the room is
// taken out of the workspace's month before the request goes out — the probe
// included, since a probe is a call the workspace pays too — the answer settles
// it on the usage the response reported, a settle that cannot be written
// refuses the call, a model the table cannot price is refused, a call at the
// ceiling is refused with the workspace's own marker and the headroom beside
// it, a subscription token's call reserves nothing at all, and an installation
// (a scope with no meter) is untouched by every one of them.
//
// The workspace's own scope is WS2's and does not exist in this tree yet
// (scope_stub.go), so the fixture below answers every call with an
// installation's scope carrying the workspace's meter — which is exactly what
// R1's parameterisation is for: one code path serves both deployments, and the
// meter is what differs. The member the calls are spent for is the caller's own
// verified project claim (testProject), and the record's own member table — what
// the front door reads to resolve a caller to a member — is not consulted here
// at all: the meter is handed the member the identity carries, and the fence
// below proves it reads nothing else.
//
// Every value here is a name or a dollar amount; no literal is, or looks like,
// a credential.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// wsCapUSD is the ceiling the fixture's workspace states, in dollars: large
// enough that no ordinary call of these tests comes near it, so the tests about
// the ceiling are the ones that choose their own.
const wsCapUSD = 100

// wsMeterScope is an installation's scope with the workspace's meter over it:
// the scope the workspace's own deployment of this service answers with (W6,
// R1), built here because the real one is WS2's. It is the ONLY difference
// between the two deployments at this level of the code — everything else the
// service does with a call is the installation's own behavior either way.
type wsMeterScope struct {
	installationScope
	meter *WorkspaceMeter
}

func (sc wsMeterScope) Meter() *WorkspaceMeter { return sc.meter }

// wsMeterResolver answers every call of the fixture with a metered scope, by
// resolving it the way the fixture's installation does and putting the
// workspace's meter on the answer.
//
// The server is read through a function because the fixture wires the resolver
// BEFORE it builds the server (newSvcWith's mutate): the resolution itself
// happens per call, long after NewServer has run, and the server a scope's
// readers are bound to is the one it built.
type wsMeterResolver struct {
	srv   func() *Server
	meter *WorkspaceMeter
}

func (r *wsMeterResolver) Resolve(ctx context.Context, req *http.Request) (Scope, error) {
	sc, err := installationResolver{s: r.srv()}.Resolve(ctx, req)
	if err != nil {
		return nil, err
	}
	base, ok := sc.(installationScope)
	if !ok {
		return sc, nil
	}
	return wsMeterScope{installationScope: base, meter: r.meter}, nil
}

// wsMeterWatch is the client's transport with the workspace's month read
// immediately before every upstream call goes out. It is the only vantage point
// from which "reserved BEFORE the call" is a fact a test can observe: what the
// month holds at the moment of the request is what the call was charged before
// it was made, and the answer's own settle is not visible from anywhere else
// until the call is over.
type wsMeterWatch struct {
	next  http.RoundTripper
	meter *WorkspaceMeter

	mu   sync.Mutex
	sent []budget.WorkspaceRoom
	err  error
}

func (w *wsMeterWatch) RoundTrip(r *http.Request) (*http.Response, error) {
	room, err := w.meter.Room(r.Context())
	w.mu.Lock()
	w.sent = append(w.sent, room)
	if err != nil {
		w.err = err
	}
	w.mu.Unlock()
	return w.next.RoundTrip(r)
}

// reservedAt is the room the workspace held when upstream request i went out.
func (w *wsMeterWatch) reservedAt(t *testing.T, i int) budget.WorkspaceRoom {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		t.Fatalf("the workspace's month could not be read at request %d: %v", i, w.err)
	}
	if i >= len(w.sent) {
		t.Fatalf("the upstream saw %d requests and the test asks for request %d", len(w.sent), i)
	}
	return w.sent[i]
}

// wsDenyNthDB refuses the n-th write through the meter's own database and lets
// every other one through. It is how a test fails a SETTLE and nothing before
// it: a metered call's first write is its reservation and its second is its
// settle, so denying the second is the write the service must refuse the call
// on.
type wsDenyNthDB struct {
	budget.WorkspaceSpendDB
	mu     sync.Mutex
	n      int
	writes int
}

func (d *wsDenyNthDB) PutIfMatch(ctx context.Context, path, etag string, v any) error {
	d.mu.Lock()
	d.writes++
	deny := d.writes == d.n
	d.mu.Unlock()
	if deny {
		return errors.New("the workspace's database refused the write")
	}
	return d.WorkspaceSpendDB.PutIfMatch(ctx, path, etag, v)
}

// wsMeterFixture is the fixture's service with a workspace scope over it: the
// workspace's own database (fenced, so a read or a write outside the
// workspace's own tree is refused and recorded), the meter the service uses,
// and the transport that watched what the month held at each upstream call.
//
// The database is named `meterDB` rather than `db`, which the fixture already
// uses for the authority's own store: the two are different databases with
// different owners, and a shadowed name is a trap for the next reader.
type wsMeterFixture struct {
	*svc
	meterDB *gcpfake.RTDB
	meter   *WorkspaceMeter
	fenced  *wsFencedDB
	watch   *wsMeterWatch
}

// newWSMeterSvc is the fixture with a workspace scope: capUSD is the ceiling
// the workspace's record states, wrap (optional) is a wrapper around the
// workspace's own database — the way a test makes its writes fail — and mutate
// is the fixture's own hook, run last.
func newWSMeterSvc(t *testing.T, capUSD float64, wrap func(budget.WorkspaceSpendDB) budget.WorkspaceSpendDB, mutate func(*svc, *Config)) *wsMeterFixture {
	t.Helper()
	db, meterDB, fenced := wsFenced(t)
	wsSeedRecord(t, db, capUSD)
	if wrap != nil {
		meterDB = wrap(meterDB)
	}
	m := wsMeter(t, meterDB)
	watch := &wsMeterWatch{meter: m}
	f := &wsMeterFixture{meterDB: db, meter: m, fenced: fenced, watch: watch}
	f.svc = newSvcWith(t, func(sv *svc, cfg *Config) {
		cfg.Resolver = &wsMeterResolver{srv: func() *Server { return sv.server }, meter: m}
		watch.next = cfg.Client.Transport
		cfg.Client = &http.Client{Transport: watch}
		if mutate != nil {
			mutate(sv, cfg)
		}
	})
	return f
}

// wsRow is the embedded table's row for a model, which is what the meter prices
// a call from.
func wsRow(t *testing.T, model string) pricing.Model {
	t.Helper()
	row, ok := pricing.Embedded().Lookup(model)
	if !ok {
		t.Fatalf("the embedded table has no row for %s", model)
	}
	return row
}

// wsWorst is a body's worst case as the table states it, built here from the
// body and the model rather than read off the service: the figure a test
// asserts is the table's own bound for that request, which is the figure the
// meter must have reserved.
func wsWorst(t *testing.T, model string, body []byte, maxTokens int64) pricing.Micros {
	t.Helper()
	return wsRow(t, model).WorstCase(pricing.Request{BodyBytes: int64(len(body)), MaxTokens: maxTokens})
}

// wsRoom is the workspace's figure as the meter reads it now.
func (f *wsMeterFixture) wsRoom(t *testing.T) budget.WorkspaceRoom {
	t.Helper()
	room, err := f.meter.Room(context.Background())
	if err != nil {
		t.Fatalf("reading the workspace's room: %v", err)
	}
	return room
}

// wsPaidOnly is the mutation that leaves a repository admitted no shared
// account at all, so the call it makes is served by the project's own paid key
// (the O4 path TestServiceNeverGivesTierAToUnadmittedRepository pins) and every
// call the run makes is one the workspace's meter brackets.
func wsPaidOnly(s *svc, _ *Config) { delete(s.policy.admissions, s.slug) }

// TestEveryPaidCallIsReserved is WS8's red step. A call the workspace's own
// credentials pay for takes the room for its worst case out of the workspace's
// month BEFORE the request goes out, and the probe the cascade owes the
// backed-off paid key is such a call: before the wiring this test is red on the
// FIRST upstream request, the probe's, which went out with the month holding
// nothing at all.
//
// The two requests of the run are checked one by one — the probe's reservation
// is the one-token probe's own worst case, not the run's request's — and the
// figure the month ends at is the probe's worst case (a probe's answer is never
// read for usage) plus the run's call settled on the usage its response
// reported.
func TestEveryPaidCallIsReserved(t *testing.T) {
	f := newWSMeterSvc(t, wsCapUSD, nil, wsPaidOnly)
	// The paid key's name is the bundle's and never the test's: a mark planted
	// under a name no credential has would make this test pass by never being
	// eligible.
	paid := f.tierBName(t)
	// Backed off with its wait passed, which is the probe C1.12 owes it: the
	// probe comes before any real work reaches the key.
	f.backedOff(paid, f.now.Add(-time.Hour), creds.DefaultBackoffInitial)
	f.up.Script = []anthropicfake.Reply{
		anthropicfake.MessageOK(testModel, testUsage),
		anthropicfake.MessageOK(testModel, testUsage),
	}

	resp, body := f.call(t, callBody("ws8-paid"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call answered %d %s, want it served by the project's paid key", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderTier); got != TierB {
		t.Fatalf("%s = %q, want %q: this test is about the paid path", HeaderTier, got, TierB)
	}
	if n := f.up.Count(); n != 2 {
		t.Fatalf("the upstream saw %d requests, want the probe and the run's call", n)
	}

	probeWorst := wsWorst(t, testModel, probeBody(MessagesPath, testModel), probeMaxTokens)
	callWorst := wsWorst(t, testModel, []byte(callBody("ws8-paid")), 16)
	if got := f.watch.reservedAt(t, 0); got.Reserved != probeWorst {
		t.Fatalf("at the probe the month held %v of room, want the probe's own worst case %v: a probe is a call the workspace pays for and must be reserved before it goes out",
			got.Reserved.USD(), probeWorst.USD())
	}
	if got := f.watch.reservedAt(t, 1); got.Reserved != callWorst {
		t.Fatalf("at the run's call the month held %v, want the request's worst case %v", got.Reserved.USD(), callWorst.USD())
	}

	room := f.wsRoom(t)
	want := probeWorst + wsRow(t, testModel).Rates.Cost(testUsage)
	if room.Settled != want {
		t.Errorf("the month holds %v of settled spend, want %v (the probe at its worst case plus the call at its usage)", room.Settled.USD(), want.USD())
	}
	if room.Reserved != 0 {
		t.Errorf("the month still holds %v of room after both calls settled, want none", room.Reserved.USD())
	}
	if got, want := resp.Header.Get(gateway.WorkspaceHeadroomHeader), strconv.FormatInt(int64(room.Headroom), 10); got != want {
		t.Errorf("the answer carries %s = %q, want %q: every answer of a workspace's scope carries the room the settle left", gateway.WorkspaceHeadroomHeader, got, want)
	}
}

// A call whose cost cannot be recorded is refused rather than served: the
// settle is a write the workspace's own database can fail, and a call served
// through it would spend against a cap the workspace cannot see (D1301's
// fail-closed rule, F18).
//
// The refusal is the workspace's own and says so: the marker names the settle
// (gateway.WorkspaceRefusalSettle), so a run's record can say the workspace
// refused it rather than recording the same state_unavailable an ordinary
// outage writes.
func TestAFailedSettleRefuses(t *testing.T) {
	// The reservation is the first write of a metered call and the settle the
	// second, so this fails the settle alone: a call that never got its room
	// would be a different test (and a different refusal).
	f := newWSMeterSvc(t, wsCapUSD, func(db budget.WorkspaceSpendDB) budget.WorkspaceSpendDB {
		return &wsDenyNthDB{WorkspaceSpendDB: db, n: 2}
	}, wsPaidOnly)

	resp, body := f.call(t, callBody("ws8-settle"))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the call answered %d %s, want the workspace's refusal %d: a call whose cost cannot be recorded is not served", resp.StatusCode, body, StatusUnavailable)
	}
	e := refusal(t, resp.StatusCode, body)
	if e.Error != ErrorStateUnavailable {
		t.Errorf("the refusal is %q, want %q", e.Error, ErrorStateUnavailable)
	}
	if got, ok := gateway.WorkspaceRefusal(resp.Header); !ok || got != gateway.WorkspaceRefusalSettle {
		t.Errorf("%s = %q (read: %v), want %q: the refusal names the settle it could not write",
			gateway.WorkspaceRefusalHeader, resp.Header.Get(gateway.WorkspaceRefusalHeader), ok, gateway.WorkspaceRefusalSettle)
	}
	// The reservation stands: the settle that failed released nothing, so the
	// room is held until a hold that old cannot still be in flight.
	if room := f.wsRoom(t); room.Reserved == 0 {
		t.Error("the month released the room of a call that was made and could not be recorded")
	}
}

// A call at the ceiling is refused before it is made, with the workspace's own
// marker and the room it has left. The two travel together on the refusal: the
// marker is what a run's record carries (O19) and the headroom is what the
// member's own gateway compares its next calls against.
func TestACallAtTheCeilingIsRefused(t *testing.T) {
	// A ceiling below the worst case of any call this fixture can make, with
	// nothing spent against it: the room is what the refused call did not take.
	cap := wsMicros(t, 0.001)
	f := newWSMeterSvc(t, 0.001, nil, wsPaidOnly)

	resp, body := f.call(t, callBody("ws8-ceiling"))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the call answered %d %s, want the workspace's refusal %d", resp.StatusCode, body, StatusUnavailable)
	}
	if n := f.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d requests, want none: the call is refused before it is made", n)
	}
	if got, ok := gateway.WorkspaceRefusal(resp.Header); !ok || got != gateway.WorkspaceRefusalCap {
		t.Errorf("%s = %q (read: %v), want %q", gateway.WorkspaceRefusalHeader, resp.Header.Get(gateway.WorkspaceRefusalHeader), ok, gateway.WorkspaceRefusalCap)
	}
	if got, want := resp.Header.Get(gateway.WorkspaceHeadroomHeader), strconv.FormatInt(int64(cap), 10); got != want {
		t.Errorf("%s = %q, want %q: the refusal carries the room the workspace still has", gateway.WorkspaceHeadroomHeader, got, want)
	}
	if room := f.wsRoom(t); room.Settled != 0 || room.Reserved != 0 {
		t.Errorf("the refused call left the month at %v settled and %v reserved, want nothing of either", room.Settled.USD(), room.Reserved.USD())
	}
}

// A probe the workspace cannot pay for is the RUN's refusal and not the
// credential's: the service owes a backed-off credential a probe (C1.12), the
// meter refuses that probe before it is made, and the run waiting on the call
// is answered the workspace's own refusal rather than the wait the probe was
// owed on.
//
// The distinction is the whole test: the same state — a backed-off credential
// the selection elides — is answered exhausted_until whenever the workspace has
// room, and only the meter's refusal makes it the cap. A run handed the wait
// instead would re-ask, and re-ask again, for a probe the workspace will refuse
// every time, spending its budget on a deterministic refusal (cascade.go's
// forward reads the refusal off the call whatever the probe reports).
func TestAProbeTheMeterRefusesIsTheRunsRefusal(t *testing.T) {
	// A ceiling below the worst case of the smallest call this service makes
	// (probeMaxTokens, one token), with nothing spent against it: the probe
	// cannot be reserved, so it is never made.
	cap := wsMicros(t, 0.001)
	f := newWSMeterSvc(t, 0.001, nil, wsPaidOnly)
	paid := f.tierBName(t)
	f.backedOff(paid, f.now.Add(-time.Hour), creds.DefaultBackoffInitial)

	resp, body := f.call(t, callBody("ws8-probe-cap"))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the call answered %d %s, want the workspace's refusal %d", resp.StatusCode, body, StatusUnavailable)
	}
	e := refusal(t, resp.StatusCode, body)
	if e.Error != ErrorStateUnavailable {
		t.Errorf("the refusal is %q, want %q: a run owed a probe the workspace refuses is answered the refusal and not the wait the probe was owed on",
			e.Error, ErrorStateUnavailable)
	}
	if n := f.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d requests, want none: a probe the workspace cannot pay for is not made", n)
	}
	if got, ok := gateway.WorkspaceRefusal(resp.Header); !ok || got != gateway.WorkspaceRefusalCap {
		t.Errorf("%s = %q (read: %v), want %q", gateway.WorkspaceRefusalHeader, resp.Header.Get(gateway.WorkspaceRefusalHeader), ok, gateway.WorkspaceRefusalCap)
	}
	if got, want := resp.Header.Get(gateway.WorkspaceHeadroomHeader), strconv.FormatInt(int64(cap), 10); got != want {
		t.Errorf("%s = %q, want %q: the refusal carries the room the workspace still has", gateway.WorkspaceHeadroomHeader, got, want)
	}
	if room := f.wsRoom(t); room.Settled != 0 || room.Reserved != 0 {
		t.Errorf("the refused probe left the month at %v settled and %v reserved, want nothing of either", room.Settled.USD(), room.Reserved.USD())
	}
}

// R15, D1300: a call on a subscription token reserves nothing. The workspace's
// cap bounds what the workspace's money buys, and a subscription token's calls
// are the donor's own quota — not one micro-dollar of the workspace's month
// moves for them, before or after.
//
// What the call still carries is the headroom: the delivery is about the
// workspace's room and not about this call's cost, and a member's gateway
// compares its next calls against it whether or not the workspace paid for the
// one that carried it.
func TestASubscriptionCallReservesNothing(t *testing.T) {
	f := newWSMeterSvc(t, wsCapUSD, nil, func(s *svc, _ *Config) {
		s.policy.shared = decodeTestPolicy(t, testPolicyDocControls("oauth-only", false, 0))
		s.policy.admissions[s.slug] = decodeTestChild(t, testChildDoc("default"))
	})

	resp, body := f.call(t, callBody("ws8-subscription"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call answered %d %s, want it served by the subscription token", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderTier); got != TierA {
		t.Fatalf("%s = %q, want %q: this test is about the subscription path", HeaderTier, got, TierA)
	}
	if room := f.wsRoom(t); room.Settled != 0 || room.Reserved != 0 {
		t.Errorf("a subscription call moved the workspace's month to %v settled and %v reserved, want nothing of either", room.Settled.USD(), room.Reserved.USD())
	}
	if len(f.fenced.writes) != 0 {
		t.Errorf("a subscription call wrote the workspace's meter %v times, want none", len(f.fenced.writes))
	}
	if got, want := resp.Header.Get(gateway.WorkspaceHeadroomHeader), strconv.FormatInt(int64(wsMicros(t, wsCapUSD)), 10); got != want {
		t.Errorf("%s = %q, want the whole ceiling %q: nothing was spent against it", gateway.WorkspaceHeadroomHeader, got, want)
	}
}

// A reservation whose call can no longer be in flight is not room taken any
// more: a hold older than budget.WorkspaceReservationTTL is released by the
// read, so a service that died with a call in flight does not hold the
// workspace's room to the month's end (or refuse every call after it).
//
// The fixture's month is seeded with a hold that takes the WHOLE ceiling and is
// older than the TTL: the call below fits only because the stale hold is
// released, and the call that fits is served.
func TestAStalledReservationExpires(t *testing.T) {
	f := newWSMeterSvc(t, 1, nil, wsPaidOnly)
	ceiling := wsMicros(t, 1)
	month := budget.PathWorkspaceSpend(wsWorkspace, budget.MonthKey(testNow()))
	f.meterDB.Set(month, map[string]any{
		"month":   budget.MonthKey(testNow()),
		"members": map[string]any{testProject: int64(0)},
		"reservations": map[string]any{
			"stalled-call": map[string]any{
				"member": testProject,
				"amount": int64(ceiling),
				"at":     testNow().Add(-budget.WorkspaceReservationTTL - time.Minute).UnixMilli(),
			},
		},
	})

	resp, body := f.call(t, callBody("ws8-stalled"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call answered %d %s, want it served: a hold older than %s is not room taken", resp.StatusCode, body, budget.WorkspaceReservationTTL)
	}
	room := f.wsRoom(t)
	if room.Settled != wsRow(t, testModel).Rates.Cost(testUsage) {
		t.Errorf("the month holds %v settled, want the call's own usage %v", room.Settled.USD(), wsRow(t, testModel).Rates.Cost(testUsage).USD())
	}
	if room.Reserved != 0 {
		t.Errorf("the month holds %v of room after the call settled, want none", room.Reserved.USD())
	}
	if node := wsSpendNode(t, f.meterDB); node != nil {
		if held, ok := node["reservations"].(map[string]any); ok && len(held) != 0 {
			t.Errorf("the month's node still holds the stalled reservation: %v", held)
		}
	}
}

// A stream cut before its usage was reported settles at the worst case the call
// was reserved at: the answer never said what the call cost, and a call whose
// cost cannot be read is charged what it could have cost rather than nothing.
// It is the fail-closed direction, and it is the gateway's own rule for its own
// calls ("until an outcome is known it is charged its full reservation").
func TestACutStreamSettlesAtTheWorstCase(t *testing.T) {
	f := newWSMeterSvc(t, wsCapUSD, nil, wsPaidOnly)
	// One event, then the connection is dropped: message_start alone reports a
	// placeholder output count and no message_delta ever follows it, so nothing
	// the answer said is a complete pair of counts.
	f.up.Script = []anthropicfake.Reply{{
		Status: http.StatusOK,
		Events: []anthropicfake.Event{
			anthropicfake.StartEvent(testModel, testUsage),
			{Name: "content_block_delta", Data: `{"type":"content_block_delta","index":0}`},
		},
		CutAfter: 1,
	}}

	streamBody := `{"model":"` + testModel + `","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"ws8-cut"}]}`
	resp := f.post(t, MessagesPath, streamBody)
	defer resp.Body.Close()
	// The client's read may fail where the stream was cut, which is the point of
	// this test rather than a failure of it.
	_, _ = io.Copy(io.Discard, resp.Body)

	callWorst := wsWorst(t, testModel, []byte(streamBody), 16)
	waitFor(t, "the cut stream's settle", func() bool { return f.wsRoom(t).Reserved == 0 })
	room := f.wsRoom(t)
	if room.Settled != callWorst {
		t.Errorf("the cut stream settled at %v, want the reservation it held at its worst case %v", room.Settled.USD(), callWorst.USD())
	}
	if room.Reserved != 0 {
		t.Errorf("the cut stream left %v of room held, want none: the settle ends the reservation", room.Reserved.USD())
	}
}

// A response that reports a pricing dimension the table has no price for — a
// service tier that is not the standard one, a speed, an inference geography —
// does not settle the call on the counts it named. The request chooses a speed
// and a geography and the provider chooses the tier it serves from, so a call
// charged at its own model's standard rates would be a call cheaper than it
// really was, and a member's run could spend past the workspace's cap by asking
// for a shape nothing prices.
//
// It settles at its reservation instead, which is the most this meter can
// charge for a call the table cannot price: the gateway charges its own ledger
// the table's maximum rates for such a response, and the meter never settles
// above the room it took. It is the direction the reader already fails in for a
// usage it cannot read at all.
func TestAnUnpricedResponseSettlesAtTheReservation(t *testing.T) {
	f := newWSMeterSvc(t, wsCapUSD, nil, wsPaidOnly)
	f.up.Script = []anthropicfake.Reply{{
		Status: http.StatusOK,
		Body: `{"id":"msg_fake","type":"message","role":"assistant","model":"` + testModel + `",` +
			`"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn",` +
			`"usage":{"input_tokens":10,"output_tokens":10,"service_tier":"priority"}}`,
	}}

	resp, body := f.call(t, callBody("ws8-tier"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call answered %d %s, want it served", resp.StatusCode, body)
	}
	callWorst := wsWorst(t, testModel, []byte(callBody("ws8-tier")), 16)
	room := f.wsRoom(t)
	if room.Settled != callWorst {
		t.Errorf("the call settled at %v, want its reservation %v: the answer reported a tier the table does not price, so the counts it named are not a usage the table can charge",
			room.Settled.USD(), callWorst.USD())
	}
	if room.Reserved != 0 {
		t.Errorf("the month still holds %v of room, want none: the call settled", room.Reserved.USD())
	}
}

// A model the price table cannot price is refused, not guessed at: an unpriced
// call is a call whose cost no cap can see, and the meter would have to reserve
// either nothing (a hole in the cap) or a figure nothing states.
//
// The model is admissible to the project — its own allow-list names it — and
// the embedded table has no row for it: the refusal is the meter's, made on the
// workspace's own account.
func TestAModelWithNoPriceIsRefused(t *testing.T) {
	const unpriced = "claude-opus-4-8"
	if _, ok := pricing.Embedded().Lookup(unpriced); ok {
		t.Fatalf("the embedded table prices %s: this test needs a model no table prices", unpriced)
	}
	f := newWSMeterSvc(t, wsCapUSD, nil, func(s *svc, _ *Config) {
		delete(s.policy.admissions, s.slug)
		s.policy.shared = decodeTestPolicy(t, wsUnpricedPolicyDoc(unpriced))
	})

	resp, body := f.call(t, `{"model":"`+unpriced+`","max_tokens":16,"messages":[{"role":"user","content":"ws8-unpriced"}]}`)
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the call answered %d %s, want the workspace's refusal %d", resp.StatusCode, body, StatusUnavailable)
	}
	if n := f.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d requests, want none: a call the workspace cannot price is not made", n)
	}
	e := refusal(t, resp.StatusCode, body)
	if !strings.Contains(e.Message, unpriced) {
		t.Errorf("the refusal does not name the model it could not price: %s", e.Message)
	}
	if room := f.wsRoom(t); room.Settled != 0 || room.Reserved != 0 {
		t.Errorf("the refused call left the month at %v settled and %v reserved, want nothing of either", room.Settled.USD(), room.Reserved.USD())
	}
}

// wsUnpricedPolicyDoc is the fixture's policy with a model the embedded table
// has no row for: the ordinary allow-list and cascade, and a model admissible
// to the project and impossible for the workspace to price.
func wsUnpricedPolicyDoc(model string) string {
	return `{
		"version": 9,
		"budget": {"mode": "enforce", "per_run_usd": 5, "allowed_models": ["` + model + `"]},
		"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"],
		"accounts": {
			"default": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]},
			"` + testAccount + `": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]}
		},
		"credentials": {"fallback": "auto"}
	}`
}

// The meter holds no write on a member and reads nothing of a member's: the
// whole of what it touches is the workspace's OWN tree — the record its ceiling
// comes from and the month's node the figure lives in — and the fence around
// the database it is handed is what makes that a fact rather than a reading of
// the code. A meter that counted a member's own ledger, or wrote the member's
// own figure, would have to ask for a path outside that tree, which the fence
// refuses and names.
//
// The member's own ledger node is in the database the meter holds, so this is
// the strongest form of the claim the W7 test makes at the meter's own layer:
// the call is served through the service, and the only paths the meter asked
// for are the workspace's own.
func TestTheMeterWritesNothingToAMember(t *testing.T) {
	f := newWSMeterSvc(t, wsCapUSD, nil, wsPaidOnly)
	memberRun := budget.PathRun(f.slug, f.run)
	f.meterDB.Set(memberRun, map[string]any{"spent": int64(wsMicros(t, 90))})

	resp, body := f.call(t, callBody("ws8-member-fence"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call answered %d %s, want it served", resp.StatusCode, body)
	}
	if len(f.fenced.outside) != 0 {
		t.Fatalf("the meter asked for paths outside the workspace's own tree: %v", f.fenced.outside)
	}
	for _, p := range f.fenced.writes {
		if !strings.HasPrefix(p, budget.PathWorkspaceSpend(wsWorkspace, budget.MonthKey(testNow()))) {
			t.Errorf("the meter wrote %s, which is not the workspace's own month", p)
		}
	}
	if got := f.dbValue(memberRun, "spent"); got != strconv.FormatInt(int64(wsMicros(t, 90)), 10) {
		t.Errorf("the member's own ledger now holds %v, want the %v it held: the meter counts the workspace's money and never the member's", got, wsMicros(t, 90))
	}
}

// dbValue is one figure of a node as the database holds it, read as text: the
// fake hands decoded JSON back, whose numbers are not the Go type the test
// wrote, and what this test is about is the figure rather than its spelling.
func (f *wsMeterFixture) dbValue(path, key string) string {
	node, _ := f.meterDB.Value(path).(map[string]any)
	return fmt.Sprint(node[key])
}
