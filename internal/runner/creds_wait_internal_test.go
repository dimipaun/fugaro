package runner

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestCredsSleepEndsWithTheWait: the sleep is the reset the credentials
// service named, minutes or hours away, and it must not outlive the wait that
// started it. waitForCredential's select returns on a halt (a kill switch, a
// cap, the backend's grace) or on the run's own context, neither of which the
// sleep hears, so a sleep left running is a goroutine and a timer left behind
// for the rest of the job. Its context is the caller's to end, and the sleep
// ends on it.
//
// The parent here is context.Background, which nothing ever cancels: a sleep
// that waited on it would still be waiting when the test gives up.
func TestCredsSleepEndsWithTheWait(t *testing.T) {
	ended := make(chan struct{})
	SetCredsWait(t, func(ctx context.Context, d time.Duration) error {
		<-ctx.Done() // a real sleep: only its own context ends it
		close(ended)
		return ctx.Err()
	})
	done, stop := credsSleep(context.Background(), time.Hour)
	stop() // the wait is over — a halt, or the run's context
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the sleep outlived the wait that started it: its goroutine and timer are still running")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("the ended sleep returned %v, want the context error it was cancelled with", err)
	}
}
