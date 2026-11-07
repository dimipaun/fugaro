package runner

import "github.com/dimipaun/fugaro/internal/runstore"

func (r *run) tempWritesThroughTheWriter(st runstore.Status) {
	writeEnd(r.rec, st, runstore.OutcomeNone, "", "")
}
