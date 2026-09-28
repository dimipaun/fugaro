package runner

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
)

// Past timeouts.total, writeback's uploads, its lock release and the final
// record must all fit in the task timeout's slack, with the container's
// startup (StartedAt comes after Cloud Run's task start) to spare.
func TestWritebackFitsTheTaskTimeoutSlack(t *testing.T) {
	if sum := writebackGrace + releaseDeferredTimeout + recordWriteTimeout + startupMargin; sum > backend.TaskTimeoutSlack {
		t.Fatalf("writeback grace %v + lock release %v + final record %v + startup %v = %v, past the task timeout's slack %v",
			writebackGrace, releaseDeferredTimeout, recordWriteTimeout, startupMargin, sum, backend.TaskTimeoutSlack)
	}
	if writebackGrace < writebackFloor {
		t.Fatalf("writeback grace %v is below its floor %v", writebackGrace, writebackFloor)
	}
}
