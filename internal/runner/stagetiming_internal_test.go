package runner

import (
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway"
)

// TestStageTimingReadsGatewayUnderLock pins that stageTiming takes r.mu
// around the reads of r.gw and r.lastStage (cost.go). Those two are written
// from the gateway's own goroutines — endGatewayStage, closeGateway,
// startGateway — while the run's stage goroutine calls stageTiming, so an
// unlocked read is a data race by construction.
//
// A concurrent writer under -race would catch it most runs, but it is a
// race by luck; the held lock makes it certain: if the reads are not under
// r.mu, stageTiming returns while this test holds the lock.
func TestStageTimingReadsGatewayUnderLock(t *testing.T) {
	r := &run{cfg: &config.Config{}, d: Deps{Now: time.Now}}

	// The concurrent writer the lock is for: -race fails the test if any
	// read of gw or lastStage escapes the lock.
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		gw := &gateway.Server{}
		for {
			select {
			case <-stop:
				return
			default:
			}
			r.mu.Lock()
			r.gw, r.lastStage = gw, &gateway.StageReport{}
			r.mu.Unlock()
			r.mu.Lock()
			r.gw, r.lastStage = nil, nil
			r.mu.Unlock()
		}
	}()

	r.mu.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = r.stageTiming("implement", time.Now(), 1, 0)
	}()
	select {
	case <-done:
		r.mu.Unlock()
		close(stop)
		wg.Wait()
		t.Fatal("stageTiming returned while r.mu was held: it reads r.gw or r.lastStage without the lock")
	case <-time.After(200 * time.Millisecond):
		// Still blocked on the lock, as it must be.
	}
	r.mu.Unlock()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		close(stop)
		wg.Wait()
		t.Fatal("stageTiming never returned after r.mu was released")
	}
	close(stop)
	wg.Wait()
}
