package rtdb

import (
	"net/http"
	"sync"
	"testing"
	"time"
)

func dateResponse(at time.Time) *http.Response {
	h := http.Header{}
	h.Set("Date", at.UTC().Format(http.TimeFormat))
	return &http.Response{Header: h}
}

// near reports whether got is at or up to two seconds after want (the Date
// header's one-second resolution plus the time the test takes).
func near(got, want time.Time) bool { return !got.Before(want) && got.Before(want.Add(2*time.Second)) }

// TestObserveDropsLateAnswerToOlderRequest: requests run concurrently (a
// run's config read fires several GETs beside the heartbeat and kill-poll
// loops), so the answer to a request sent before midnight can be processed
// after the answer to one sent later. It must not move the clock back into
// the previous day.
func TestObserveDropsLateAnswerToOlderRequest(t *testing.T) {
	c := &Client{}
	sent := time.Now()
	older := time.Date(2026, 10, 2, 23, 50, 0, 0, time.UTC)
	newer := time.Date(2026, 10, 3, 0, 10, 0, 0, time.UTC)

	c.observe(dateResponse(newer), sent.Add(time.Second))
	c.observe(dateResponse(older), sent) // sent first, answered last

	if got, ok := c.ServerNow(); !ok || !near(got, newer) {
		t.Fatalf("ServerNow = %v (ok=%v), want about %v: a late answer to an older request moved the clock", got, ok, newer)
	}
}

// TestObserveFollowsNewerRequestBackwards: the answer to a later-sent
// request wins even when its Date is earlier than the stored one, so one
// skewed Date (a proxy two days ahead) is healed by the next request, and a
// genuine correction of the server's clock takes effect at once.
func TestObserveFollowsNewerRequestBackwards(t *testing.T) {
	c := &Client{}
	sent := time.Now()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	c.observe(dateResponse(now.Add(48*time.Hour)), sent)
	c.observe(dateResponse(now), sent.Add(time.Millisecond))

	if got, ok := c.ServerNow(); !ok || !near(got, now) {
		t.Fatalf("ServerNow = %v (ok=%v), want about %v: a skewed Date pinned the clock", got, ok, now)
	}
}

// TestObserveEqualSendTimesLastWins: two readings whose requests went out at
// the same instant cannot be ordered by send time; the one observed last
// replaces the other, in either direction.
func TestObserveEqualSendTimesLastWins(t *testing.T) {
	sent := time.Now()
	a := time.Date(2026, 10, 2, 23, 59, 0, 0, time.UTC)
	b := a.Add(2 * time.Minute)
	for _, order := range [][2]time.Time{{a, b}, {b, a}} {
		c := &Client{}
		c.observe(dateResponse(order[0]), sent)
		c.observe(dateResponse(order[1]), sent)
		if got, _ := c.ServerNow(); !near(got, order[1]) {
			t.Fatalf("observed %v then %v at one send time: ServerNow = %v, want the last", order[0], order[1], got)
		}
	}
}

// TestObserveConcurrent: many responses observed at once, in no particular
// order, leave the reading of the latest-sent request, whole (its Date with
// its own send time).
func TestObserveConcurrent(t *testing.T) {
	const n = 64
	for round := 0; round < 20; round++ {
		c := &Client{}
		base := time.Now()
		day := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := n - 1; i >= 0; i-- { // started newest first, to invite a late older answer
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				c.observe(dateResponse(day.Add(time.Duration(i)*time.Second)), base.Add(time.Duration(i)*time.Millisecond))
				c.ServerNow()
			}()
		}
		close(start)
		wg.Wait()
		sc := c.clock.Load()
		wantSent := base.Add((n - 1) * time.Millisecond)
		wantServer := day.Add((n - 1) * time.Second)
		if sc == nil || !sc.sent.Equal(wantSent) || !sc.server.Equal(wantServer) {
			t.Fatalf("round %d: stored %+v, want the reading sent at %v with Date %v", round, sc, wantSent, wantServer)
		}
	}
}
