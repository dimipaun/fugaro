package pool_test

import (
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/pool"
)

// TestRunVisitsEveryIndexOnce: the work is the caller's indices, each exactly
// once, whatever the width — a driver that dropped one would leave a row of a
// listing zeroed and a read that was never made.
func TestRunVisitsEveryIndexOnce(t *testing.T) {
	const n = 50
	seen := make([]int, n)
	pool.Run(n, 4, func(i int) { seen[i]++ })
	for i, c := range seen {
		if c != 1 {
			t.Fatalf("index %d ran %d times, want once", i, c)
		}
	}
}

// TestRunStaysWithinItsWidth is the bound itself: Run is what a caller says a
// service tolerates, so a driver that ignored workers and started one
// goroutine per index would ask github.com for every lookup of a plan at once,
// and would still pass the test above.
func TestRunStaysWithinItsWidth(t *testing.T) {
	const (
		n       = 40
		workers = 3
	)
	var mu sync.Mutex
	open, peak := 0, 0
	pool.Run(n, workers, func(int) {
		mu.Lock()
		open++
		if open > peak {
			peak = open
		}
		mu.Unlock()
		time.Sleep(time.Millisecond)
		mu.Lock()
		open--
		mu.Unlock()
	})
	if peak <= 1 || peak > workers {
		t.Errorf("ran %d of %d at once, want more than one and at most %d: the width is the caller's bound on the service the work reaches", peak, n, workers)
	}
}

// TestRunWiderThanTheWork: fewer indices than workers is that many goroutines
// and no waiting for the ones that have nothing to take.
func TestRunWiderThanTheWork(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		pool.Run(0, 32, func(int) { t.Error("nothing to run, and it ran") })
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run(nil work) did not return: the goroutines are waiting for indices that were never sent")
	}
}
