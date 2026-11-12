package runner_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The keep-warm beat's other half (DS1.5, D1934): the runner's own tests drive
// the beat directly, and what none of them can say is whether a *run's* end
// ends it. That is one line -- the first statement of Run's deferred cleanup,
// which is the only place the run waits for the goroutine that posts to the
// credentials service -- so it is read here from the service's side of a whole
// run: a request the service is holding when the run ends is a request of a
// finished run, and only the run's own end gives it up.
//
// The run is the fixture's cascade run, whose gateway sends its calls to the
// installation's credentials service: the stand-in that answers the model
// routes answers the slip route too, and the job's environment carries the
// slip a launcher would have granted it. The cadence is the session's (the
// runner's slipBeatEvery reads it), which the fixture tunes to 30 ms, so this
// waits milliseconds rather than the 15 s a run really keeps.
func TestKeepWarmBeatStopsWhenTheRunEnds(t *testing.T) {
	const slipID = "fake-slip-0f1e2d3c-not-a-credential"

	svc := newSlipStandIn("fake-claim-secret-not-a-credential")
	b := newCascade(t, cascadeConfig(t), &cascadeService{})
	model := b.fake.Func
	b.fake.Func = func(r *http.Request, body []byte) anthropicfake.Reply {
		if r.URL.Path != credsvc.SlipClaimPath {
			return model(r, body)
		}
		return svc.answer(r)
	}
	b.deps.Env = append(filterEnv(b.deps.Env, runner.SlipEnv), runner.SlipEnv+"="+slipID)

	var st []int
	rec, err := b.run(t, heldBeat(svc, calling(&st, implement("feature"), call{sonnet, 4000})), review("ship", 0))
	if err != nil || rec == nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("the fixture's run did not finish: rec = %+v, err = %v", rec, err)
	}
	claims, beats := svc.counted()
	if claims != 1 {
		t.Fatalf("the credentials service was asked to claim %d times, want the run's one", claims)
	}
	if beats == 0 {
		t.Fatalf("the run sent no keep-warm request over its own %s: this test reads nothing about the beat", bkHeartbeatEvery)
	}
	// The request the service held was still in flight when the run ended, and
	// Run has now returned: the run gave it up rather than returning with it
	// open. The fake closes Ended for a held request whose context ended while
	// it was holding, so this reads the giving up itself -- the run's own
	// cancellation reaching the service -- and not a line the run wrote about
	// it: a run that walked away from the request would leave the service
	// holding it, and this would time out.
	select {
	case <-b.fake.Ended(heldBeatIndex(t, b.fake)):
	case <-time.After(10 * time.Second):
		t.Error("the run returned with its keep-warm request still in flight at the service: it walked away from the request instead of giving it up")
	}
	// And it says nothing about a cold start while doing it: the beat ends
	// because the run is over, so there is no next call to pay one, and the
	// warning for a beat that did not land is for the service's failures
	// rather than the run's own end.
	if strings.Contains(b.logs.String(), "pays a cold start") {
		t.Errorf("the run's own end is reported as the credentials service going cold:\n%s", b.logs.String())
	}

	// Nothing of the run may still be asking the service to read its slip
	// back. The window is several of the fixture's intervals, so a beat that
	// is still ticking lands in it.
	settled := beats
	time.Sleep(10 * bkHeartbeatEvery)
	if _, got := svc.counted(); got != settled {
		t.Errorf("the run sent %d keep-warm requests after it ended (had %d): the beat outlives the run", got, settled)
	}
}

// heldBeatIndex is the index, in the fake's own record, of the keep-warm
// request the service is holding: the last beat the fake saw, which is the one
// in flight when the run ends, because the beat's goroutine is inside it and
// sends no later one. A beat is a call on the claim route that presents the
// claim secret; the claim itself presents none (D1941).
func heldBeatIndex(t *testing.T, f *anthropicfake.Fake) int {
	t.Helper()
	seen := f.Seen()
	for i := len(seen) - 1; i >= 0; i-- {
		if seen[i].URL.Path == credsvc.SlipClaimPath && seen[i].Header.Get(credsvc.HeaderSlipClaim) != "" {
			return i
		}
	}
	t.Fatalf("the fake saw no keep-warm request, so the run's beat never reached the service")
	return -1
}

// heldBeat holds a stage open until the run's keep-warm request is in flight
// and the service is holding it. The beat ticks on the session's cadence, which
// this fixture sets in milliseconds, but a run that finished before the first
// tick would leave the test above reading no beat at all.
func heldBeat(svc *slipStandIn, inner step) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, err := inner(t, ctx, req)
		waitUntil(t, "the run's first keep-warm request", func() bool { _, beats := svc.counted(); return beats > 0 })
		// The channel is taken from the arming call rather than read off the
		// stand-in: the service nils the field as it closes it, so a reader
		// that fetched the field itself could read nil and wait on it forever.
		held := svc.hold()
		<-held
		return res, err
	}
}

// slipStandIn is the installation's credentials service as this file needs it:
// the claim route, answering the claim that takes the slip and every beat after
// it, with each call counted so the run's own beat is read here rather than
// from the run's internals. It is a reply function rather than a server of its
// own because the fixture's upstream answers every path, and a cascade run
// sends its model calls to the same host.
type slipStandIn struct {
	secret string

	mu     sync.Mutex
	claims int
	beats  int
	// armed is set by hold: the next beat, and every one after it, is answered
	// with nothing at all, which leaves that request in flight.
	armed bool
	// held is closed once such a beat is in flight, so the test can let the run
	// end with a request of its own open at the service.
	held chan struct{}
}

func newSlipStandIn(secret string) *slipStandIn {
	return &slipStandIn{secret: secret, held: make(chan struct{})}
}

// hold arms the hold: from here the service answers a beat by not answering.
// It hands back the channel such a beat closes, read under the same lock that
// arms it, so the caller waits on the channel that is really closed rather
// than on the field the service clears as it closes it.
func (s *slipStandIn) hold() chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.armed = true
	return s.held
}

// answer is the service's reply to one call on the claim route: the claim's own
// reading when the call presents no secret, and the reading form -- no secret
// again -- when it presents one, because a runner that replayed its claim would
// otherwise be handed a second one (D1941).
func (s *slipStandIn) answer(r *http.Request) anthropicfake.Reply {
	s.mu.Lock()
	claim := r.Header.Get(credsvc.HeaderSlipClaim) == ""
	var body []byte
	if claim {
		s.claims++
		body, _ = json.Marshal(credsvc.SlipClaimResponse{ClaimSecret: s.secret, ExpiresAt: time.Now().Add(24 * time.Hour)})
	} else {
		s.beats++
		body, _ = json.Marshal(credsvc.SlipClaimResponse{ExpiresAt: time.Now().Add(24 * time.Hour)})
	}
	held := !claim && s.armed
	if held && s.held != nil {
		close(s.held)
		s.held = nil
	}
	s.mu.Unlock()
	return anthropicfake.Reply{
		Header: http.Header{"Content-Type": []string{"application/json"}},
		Body:   string(body),
		Hold:   held,
	}
}

// counted is how many calls took the slip and how many presented it: the run's
// claim, and the keep-warm requests that followed it.
func (s *slipStandIn) counted() (claims, beats int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.claims, s.beats
}
