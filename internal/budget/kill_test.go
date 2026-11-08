package budget_test

// ReadKills, the pair of switches the credentials service's stop flag is
// (kill.go, C1.11). The fail-closed decode is the rule that makes an unreadable
// switch ON and is asserted beside the workspace stop in workspace_kill_test.go;
// what is here is the read's one other input, the empty slug.
//
// It is here because it was a panic rather than an answer: the service's
// person-facing routes name a person and no repository — the two git reads and
// the read mint — so they ask for the global switch alone, and PathKillRepo
// panics on an empty slug. Every other test in this package and in credsvc
// drives the read through a fake that takes any string, which is why nothing
// caught it.

import (
	"context"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

func TestReadKillsWithNoSlugIsTheGlobalSwitchAlone(t *testing.T) {
	ctx := context.Background()
	db := gcpfake.NewRTDB(t)
	client, err := rtdb.New(db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatal(err)
	}

	// A repository's switch is not the person's business: the read that names
	// no repository reads no repository node, and says off while one is on.
	db.Set(budget.PathKillRepo("acme/app"), budget.Kill{On: true, By: "operator@example.com", Reason: "runaway spend"})
	kills, err := budget.ReadKills(ctx, client, "")
	if err != nil {
		t.Fatalf("the read with no slug: %v", err)
	}
	if on, which := kills.On(); on {
		t.Errorf("the read with no slug is (on=%v, which=%q) while only a repository's switch is on, want off: the person-facing read is the global switch alone", on, which)
	}
	if kills.Repo != nil {
		t.Errorf("the read with no slug returned a repository switch (%+v)", kills.Repo)
	}

	// And the global one is what it answers on, which is the stop the routes
	// refuse every call for.
	db.Set(budget.PathKillGlobal, budget.Kill{On: true, By: "operator@example.com"})
	kills, err = budget.ReadKills(ctx, client, "")
	if err != nil {
		t.Fatal(err)
	}
	if on, which := kills.On(); !on || which != "global" {
		t.Errorf("the read with no slug is (on=%v, which=%q), want the global switch on", on, which)
	}

	// A run's own read still honours both, so nothing was widened: with the
	// global switch off and the repository's on, the pair is the repository's.
	db.Set(budget.PathKillGlobal, budget.Kill{On: false})
	kills, err = budget.ReadKills(ctx, client, "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if on, which := kills.On(); !on || which != "repo" {
		t.Errorf("the read for acme/app is (on=%v, which=%q), want the repository's switch on", on, which)
	}
}
