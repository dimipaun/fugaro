package runner

import (
	"sync"
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestHaltCancelExclusive: whichever of haltNow and markCancelled is
// recorded first wins, and never both.
func TestHaltCancelExclusive(t *testing.T) {
	for i := 0; i < 2000; i++ {
		r := &run{}
		var haltWon, cancelWon bool
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); haltWon = r.haltNow(runstore.Halt{Reason: runstore.HaltRunCap}) }()
		go func() { defer wg.Done(); cancelWon = r.markCancelled() }()
		wg.Wait()
		if haltWon == cancelWon {
			t.Fatalf("iteration %d: halt won %v, cancel won %v", i, haltWon, cancelWon)
		}
		if (r.halt != nil) != haltWon || r.cancelled != cancelWon || (haltWon && r.failReason == "") {
			t.Fatalf("iteration %d: state halt=%v cancelled=%v reason=%q", i, r.halt, r.cancelled, r.failReason)
		}
		if r.haltNow(runstore.Halt{Reason: runstore.HaltTokenCap}) {
			t.Fatal("a second halt was recorded")
		}
	}
}
