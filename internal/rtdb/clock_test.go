package rtdb

import (
	"net/http"
	"testing"
	"time"
)

func dateResponse(at time.Time) *http.Response {
	h := http.Header{}
	h.Set("Date", at.UTC().Format(http.TimeFormat))
	return &http.Response{Header: h}
}

// TestObserveDoesNotRegress reproduces the race behind the flaky
// TestLeaseAcrossMidnight (internal/budget/session_test.go): a run makes
// several concurrent requests to the database (readConfig alone fires seven
// GETs, alongside the heartbeat and kill-poll loops), so an older response
// can be processed after a newer one already advanced the clock past
// midnight. observe must not let that late, stale response roll the clock
// back into the previous day.
func TestObserveDoesNotRegress(t *testing.T) {
	c := &Client{}
	older := time.Date(2026, 10, 2, 23, 50, 0, 0, time.UTC)
	newer := time.Date(2026, 10, 3, 0, 10, 0, 0, time.UTC)

	c.observe(dateResponse(newer))
	c.observe(dateResponse(older)) // a request sent before midnight, answered late

	got, ok := c.ServerNow()
	if !ok || got.Before(newer) {
		t.Fatalf("ServerNow = %v (ok=%v), a stale response rolled the clock back behind %v", got, ok, newer)
	}
}
