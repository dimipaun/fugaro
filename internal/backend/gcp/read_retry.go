package gcp

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"

	"google.golang.org/api/googleapi"
)

// readBackoff is how long a refused read waits before each retry: a read gets
// len(readBackoff)+1 tries and the longest wait is the sum, about half a
// minute. The Cloud Run Admin API's read quota is per minute and per region, so
// a read refused for it is one a short wait answers, and a command that gave up
// at once (a launch refusing in three seconds on "listing jobs") made the
// person retry by hand, which is the load that refused it.
var readBackoff = []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}

// readWaitCap is the longest wait a Retry-After header may ask for: a server
// that asks for more is answered with the error, not with a silent minute.
const readWaitCap = 30 * time.Second

// sleepFor waits d or until ctx ends. A var so a test does not spend the time.
var sleepFor = func(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// doer is a Cloud Run read call: the generated client's Do.
type doer[T any] interface {
	Do(opts ...googleapi.CallOption) (T, error)
}

// retryRead runs a read and repeats it, with backoff, while the API refuses it
// for quota (429). Only reads go through it: a refused call changed nothing, but
// a launch or a cancel is not repeated behind the caller's back.
func retryRead[T any](ctx context.Context, c doer[T]) (T, error) {
	for i := 0; ; i++ {
		v, err := c.Do()
		var ge *googleapi.Error
		if err == nil || !errors.As(err, &ge) || ge.Code != http.StatusTooManyRequests || i >= len(readBackoff) {
			return v, err
		}
		wait := readBackoff[i] + rand.N(time.Second)
		if s, perr := strconv.Atoi(ge.Header.Get("Retry-After")); perr == nil && s > 0 {
			if ra := time.Duration(s) * time.Second; ra > wait && ra <= readWaitCap {
				wait = ra
			}
		}
		if serr := sleepFor(ctx, wait); serr != nil {
			var zero T
			return zero, serr
		}
	}
}
