package budget_test

// The workspace scope of `fugaro budget kill` (W8, D1302) at the layer that
// holds the read rather than the command: the stop of a workspace is
// budget.ReadKills against the workspace's own database — the pair
// internal/credsvc/server.go:454 asks as the workspace service's stop flag — so
// this file pins what that read answers for the switch the CLI writes.
//
// The three things it holds are the three the spec's stop rests on (§6, §8,
// §9): the answer the service refuses a call on, the fact that the switch is
// the workspace's own database and not a member's (which is why a stop can be
// written without writing a member at all), and the fail-closed decode that
// makes it a stop nobody can talk their way out of.

import (
	"context"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// wsKillDB is one database with a client of it: the workspace's own, then a
// member's, in the same call.
func wsKillDB(t *testing.T) (*gcpfake.RTDB, *rtdb.Client) {
	t.Helper()
	db := gcpfake.NewRTDB(t)
	client, err := rtdb.New(db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	return db, client
}

func TestWorkspaceStopIsWhatTheWorkspaceServiceReads(t *testing.T) {
	ctx := context.Background()
	workspace, wsClient := wsKillDB(t)
	member, memberClient := wsKillDB(t)

	res, err := budget.SetKill(ctx, wsClient, budget.PathKillGlobal, true, "operator@example.com", "runaway spend", budget.KillHooks{})
	if err != nil || !res.Written {
		t.Fatalf("the workspace kill: %+v, %v", res, err)
	}

	// The read the service makes on every shared-credential call, and the
	// answer it refuses on: the workspace's own global switch.
	kills, err := budget.ReadKills(ctx, wsClient, "workspace-alpha")
	if err != nil {
		t.Fatal(err)
	}
	on, which := kills.On()
	if !on || which != "global" {
		t.Errorf("the workspace read is (on=%v, which=%q), want its global switch on: this is the pair the service stops on", on, which)
	}
	if kills.Global == nil || kills.Global.By != "operator@example.com" || kills.Global.Reason != "runaway spend" {
		t.Errorf("the switch reads back as %+v, want who and why recorded", kills.Global)
	}

	// The same call against a member's own database answers off: the stop is
	// not a member's switch, which is why writing it writes no member (spec §9)
	// and why a member's runs that never reach the workspace keep running.
	if kills, err := budget.ReadKills(ctx, memberClient, "member-a"); err != nil {
		t.Fatal(err)
	} else if on, which := kills.On(); on {
		t.Errorf("a member's database reads (on=%v, which=%q) after the workspace kill, want off: a workspace reached into a member", on, which)
	}
	if member.Value(budget.PathKillGlobal) != nil || member.Value(budget.PathKillRepo("member-a")) != nil {
		t.Error("the workspace kill wrote a member's database")
	}

	// A switch nobody can read must not be one nobody honours (killNode): a
	// node at the same path that is not a switch at all is still a stop, so the
	// stop does not depend on what any writer left behind (spec §8).
	workspace.Set(budget.PathKillGlobal, "stop")
	kills, err = budget.ReadKills(ctx, wsClient, "workspace-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if on, _ := kills.On(); !on {
		t.Error("an unreadable switch at the workspace's kill path reads as off: a stop anyone can defeat by writing junk is not a stop")
	}
}
