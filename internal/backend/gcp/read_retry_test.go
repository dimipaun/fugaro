package gcp

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// refusing answers the first n requests with a quota refusal and passes the
// rest on, so a read sees the 429 the Cloud Run Admin API gives past its
// per-minute read quota.
type refusing struct {
	n      int32
	seen   atomic.Int32
	header string
	next   http.RoundTripper
}

func (r *refusing) RoundTrip(req *http.Request) (*http.Response, error) {
	if r.seen.Add(1) <= r.n {
		h := http.Header{"Content-Type": {"application/json"}}
		if r.header != "" {
			h.Set("Retry-After", r.header)
		}
		return &http.Response{StatusCode: http.StatusTooManyRequests, Status: "429 Too Many Requests", Header: h, Request: req,
			Body: http.NoBody}, nil
	}
	return r.next.RoundTrip(req)
}

func backendThrough(t *testing.T, rt *refusing) *Backend {
	t.Helper()
	fr := gcpfake.NewRun(t)
	fr.Project, fr.Region = "proj-1234", "us-east5"
	rt.next = http.DefaultTransport
	b, err := New(context.Background(), Options{GCPProject: "proj-1234", Region: "us-east5", HTTPClient: &http.Client{Transport: rt},
		Endpoints: Endpoints{Run: fr.URL + "/", Logging: fr.URL + "/", NoAuth: true}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func recordSleeps(t *testing.T) *[]time.Duration {
	t.Helper()
	var slept []time.Duration
	old := sleepFor
	sleepFor = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	t.Cleanup(func() { sleepFor = old })
	return &slept
}

// A read refused for quota is repeated after a wait, and the caller sees the
// answer: a launch that gave up on the first 429 sent the person back to launch
// again by hand, which is the load that refused it.
//
// Mutation caught: returning the first 429.
func TestAReadRefusedForQuotaIsRepeated(t *testing.T) {
	slept := recordSleeps(t)
	rt := &refusing{n: 2}
	b := backendThrough(t, rt)
	if _, err := b.LongestTaskTimeout(context.Background()); err != nil {
		t.Fatalf("LongestTaskTimeout after two refusals: %v", err)
	}
	if got := rt.seen.Load(); got != 3 {
		t.Fatalf("requests = %d, want 3 (two refused, one answered)", got)
	}
	if len(*slept) != 2 || (*slept)[0] < readBackoff[0] || (*slept)[1] < readBackoff[1] {
		t.Fatalf("waits = %v, want growing waits of at least %v", *slept, readBackoff[:2])
	}
}

// A read that stays refused ends with the refusal after a bounded number of
// tries, so a command never waits without end on a quota.
func TestAReadRefusedForQuotaGivesUpAfterTheBackoff(t *testing.T) {
	recordSleeps(t)
	rt := &refusing{n: 1000}
	b := backendThrough(t, rt)
	if _, err := b.LongestTaskTimeout(context.Background()); err == nil {
		t.Fatal("a read refused every time succeeded")
	}
	if got, want := int(rt.seen.Load()), len(readBackoff)+1; got != want {
		t.Fatalf("requests = %d, want %d", got, want)
	}
}

// Retry-After longer than the backoff is honoured up to the cap, and a longer
// one is not: the answer is the error, not a silent minute.
func TestRetryAfterIsHonouredUpToTheCap(t *testing.T) {
	slept := recordSleeps(t)
	b := backendThrough(t, &refusing{n: 1, header: "20"})
	if _, err := b.LongestTaskTimeout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(*slept) != 1 || (*slept)[0] != 20*time.Second {
		t.Fatalf("waits = %v, want [20s]", *slept)
	}
	slept = recordSleeps(t)
	b = backendThrough(t, &refusing{n: 1, header: "600"})
	if _, err := b.LongestTaskTimeout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(*slept) != 1 || (*slept)[0] > readBackoff[0]+time.Second {
		t.Fatalf("waits = %v, want the backoff, not the 600s the header asked for", *slept)
	}
}
