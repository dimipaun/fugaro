package budget_test

// The two readers of a stop, and which caller gets which (kill.go).
//
// The fail-closed decode — a switch node that does not decode is ON — is the
// rule that makes an unreadable switch one nobody can write away, and it is
// asserted beside the workspace stop in workspace_kill_test.go. What is here is
// the other distinction: a caller that has a repository reads the pair, and a
// caller that has none reads the global switch alone.
//
// It is here because the second one was a panic rather than an answer. The
// credentials service's person-facing routes name a person and no repository —
// the two git reads and the read mint — so they ask for the global switch only,
// and budget.ReadKills builds the repository's path out of a slug that
// PathKillRepo refuses when it is empty. Every other test in this package and in
// credsvc drove that read through a fake that takes any string, which is why
// nothing caught it.

import (
	"context"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

func readKillClient(t *testing.T) (*rtdb.Client, *gcpfake.RTDB) {
	t.Helper()
	db := gcpfake.NewRTDB(t)
	client, err := rtdb.New(db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	return client, db
}

func TestReadGlobalKillIsTheGlobalSwitchAlone(t *testing.T) {
	ctx := context.Background()
	client, db := readKillClient(t)

	// A repository's switch is not the person's business: the read that names
	// no repository reads no repository node, and says off while one is on.
	db.Set(budget.PathKillRepo("acme/app"), budget.Kill{On: true, By: "operator@example.com", Reason: "runaway spend"})
	kills, err := budget.ReadGlobalKill(ctx, client)
	if err != nil {
		t.Fatalf("the global read: %v", err)
	}
	if on, which := kills.On(); on {
		t.Errorf("the global read is (on=%v, which=%q) while only a repository's switch is on, want off: the person-facing read is the global switch alone", on, which)
	}
	if kills.Repo != nil {
		t.Errorf("the global read returned a repository switch (%+v)", kills.Repo)
	}

	// And the global one is what it answers on, which is the stop the routes
	// refuse every call for.
	db.Set(budget.PathKillGlobal, budget.Kill{On: true, By: "operator@example.com"})
	kills, err = budget.ReadGlobalKill(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if on, which := kills.On(); !on || which != "global" {
		t.Errorf("the global read is (on=%v, which=%q), want the global switch on", on, which)
	}

	// The fail-closed decode is the same rule here as in the pair's reader, and
	// it has to be: this is the read the person-facing routes refuse on, so a
	// node at the path that is not a switch at all must still stop them, or the
	// stop would be one anyone can defeat by writing junk (spec §8).
	db.Set(budget.PathKillGlobal, "stop")
	kills, err = budget.ReadGlobalKill(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if on, _ := kills.On(); !on {
		t.Error("a switch nobody can read at the global path reads as off through ReadGlobalKill: the person-facing routes would go on issuing credentials under a stop")
	}
}

// TestReadKillsWithNoSlugIsAProgrammingError holds the pair's reader to its
// contract, because answering an empty slug with the global switch alone is the
// tempting fix and ReadGlobalKill exists precisely so that it does not happen
// here. A caller that has a repository and lost its slug would otherwise read as
// if the repository's switch were off, and the stop is the one control that must
// not fail open.
func TestReadKillsWithNoSlugIsAProgrammingError(t *testing.T) {
	ctx := context.Background()
	client, db := readKillClient(t)
	db.Set(budget.PathKillRepo("acme/app"), budget.Kill{On: true, By: "operator@example.com"})

	defer func() {
		if r := recover(); r == nil {
			t.Error("ReadKills with no slug answered instead of panicking: a caller that has a repository and lost its slug reads as if only the global switch existed, and the repository's own switch goes unread")
		}
	}()
	kills, err := budget.ReadKills(ctx, client, "")
	if err != nil {
		t.Fatalf("ReadKills with no slug returned %v rather than panicking", err)
	}
	on, which := kills.On()
	t.Errorf("ReadKills with no slug = (on=%v, which=%q), want a panic", on, which)
}
