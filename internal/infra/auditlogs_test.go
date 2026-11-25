package infra

// WU12's Cloud Audit Logs read, against the logging fake: the Admin Activity
// filter the drift cross-check sends, the SetIamPolicy entries that filter
// answers, one change per binding a call ADDED, and the window it was given.
//
// The fake encodes Google's documentation rather than an observation, as every
// surface in internal/gcpfake does; what this test pins is that Fugaro's side of
// the read is the shape that documentation describes -- a filter the service can
// answer, and a reader that takes the caller, the added bindings and the entry's
// own id out of it.

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// The read drift's cross-check is written against (C2's SetIamPolicyLog), pinned
// here for the reason the folder surface's satisfaction is pinned in
// folder_test.go: the interface is what lets I1 hand *AuditLogs in with no
// adapter, and a method signature that moved would fail this line rather than a
// command in internal/cli.
var _ workspace.SetIamPolicyLog = (*AuditLogs)(nil)

// TestAuditLogsReadsSetIamPolicy: one project's Admin Activity log is read for
// the SetIamPolicy calls in a window, and each binding a call added comes back
// as a change named by the entry it was read from. A REMOVE delta is not a
// change -- a removed binding is an absence and not a binding -- an entry before
// the window is not returned, and another project's log is not reached at all.
func TestAuditLogsReadsSetIamPolicy(t *testing.T) {
	f := newCloud(t)
	logs := NewAuditLogs(f.c.Logging)
	at := time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)

	f.logs.AddAudit("proj-1234", gcpfake.AuditEntry{
		Time: at, Caller: "owner@corp.example", InsertID: "wu12-change-1",
		Deltas: []gcpfake.BindingDelta{
			{Action: "REMOVE", Role: "roles/viewer", Member: "user:gone@corp.example"},
			{Action: "ADD", Role: "roles/editor", Member: "user:mallory@corp.example"},
			{Action: "ADD", Role: "roles/owner", Member: "serviceAccount:ci@proj-1234.iam.gserviceaccount.com"},
		},
	})
	f.logs.AddAudit("proj-1234", gcpfake.AuditEntry{
		Time: at.Add(-72 * time.Hour), Caller: "owner@corp.example", InsertID: "wu12-change-old",
		Deltas: []gcpfake.BindingDelta{{Action: "ADD", Role: "roles/owner", Member: "user:old@corp.example"}},
	})
	f.logs.AddAudit("proj-other", gcpfake.AuditEntry{
		Time: at, Caller: "owner@corp.example", InsertID: "wu12-change-other",
		Deltas: []gcpfake.BindingDelta{{Action: "ADD", Role: "roles/owner", Member: "user:elsewhere@corp.example"}},
	})

	got, err := logs.SetIamPolicyChanges(context.Background(), "proj-1234", at.Add(-time.Hour))
	if err != nil {
		t.Fatalf("SetIamPolicyChanges: %v", err)
	}
	want := []workspace.SetIamPolicyChange{
		{Project: "proj-1234", Principal: "owner@corp.example", Member: "user:mallory@corp.example", Role: "roles/editor", Entry: "wu12-change-1", At: at},
		{Project: "proj-1234", Principal: "owner@corp.example", Member: "serviceAccount:ci@proj-1234.iam.gserviceaccount.com", Role: "roles/owner", Entry: "wu12-change-1", At: at},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SetIamPolicyChanges:\n got %+v\nwant %+v", got, want)
	}

	// A project whose log carries nothing this shape answers an empty read and
	// not an error: it is the ordinary state of a project nobody has changed a
	// policy in.
	empty, err := logs.SetIamPolicyChanges(context.Background(), "proj-quiet", time.Time{})
	if err != nil || len(empty) != 0 {
		t.Fatalf("SetIamPolicyChanges on a quiet project = %+v, %v, want nothing and no error", empty, err)
	}

	// A name that is not a project's is refused before a filter exists, with the
	// caller's own error rather than the service's: the read is made by a person
	// with the workspace's own credentials and a bad name is theirs to fix.
	var userErr *UserError
	if _, err := logs.SetIamPolicyChanges(context.Background(), "Not_A_Project", time.Time{}); !errors.As(err, &userErr) {
		t.Fatalf("err = %v, want a UserError naming the project", err)
	}
}
