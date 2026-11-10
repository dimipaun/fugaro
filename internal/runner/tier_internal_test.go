package runner

import (
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// TestRecordExpectedScopeCopiesTheTaskList (plan T8.3, spec §3): the scope the
// record carries is a copy of the task's and not an alias of it. The spec is
// the caller's own object — a launcher's, read again after the record is
// written — so a record sharing its backing array would change under a later
// write to the task, and the digest's drift flag would then compare a run's
// changed paths against a scope nobody declared.
//
// The copy is pinned here rather than through a whole run: the record a
// harness reads back has been through the store's own JSON round-trip, which
// copies the list whatever recordExpectedScope does, so a test that drove a run
// could not tell a copy from an alias. TestRunRecordCarriesTheTaskExpectedScope
// is that driven test, and it pins the echo.
func TestRecordExpectedScopeCopiesTheTaskList(t *testing.T) {
	scope := []string{"internal/digest/**", "docs/*.md"}
	r := &run{spec: &task.Spec{Version: 1, ExpectedScope: scope}, rec: &runstore.Record{Version: 1}}
	r.recordExpectedScope()
	if !slices.Equal(r.rec.ExpectedScope, scope) {
		t.Fatalf("the record's expected scope = %v, want the task's %v", r.rec.ExpectedScope, scope)
	}
	scope[0] = "internal/runner/**"
	if r.rec.ExpectedScope[0] != "internal/digest/**" {
		t.Errorf("the record's scope = %v once the task's list was written: the record shares the task's slice", r.rec.ExpectedScope)
	}

	// A task that declares no scope writes none, and the field stays absent
	// rather than becoming an empty list.
	empty := &run{spec: &task.Spec{Version: 1}, rec: &runstore.Record{Version: 1}}
	empty.recordExpectedScope()
	if empty.rec.ExpectedScope != nil {
		t.Errorf("a task that declared no scope recorded %v, want the field absent", empty.rec.ExpectedScope)
	}
}
