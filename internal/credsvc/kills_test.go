package credsvc

// RTDBKills is the operator's stop flag: budget.ReadKills, the same pair a run
// honours (server.go, C1.11).
//
// What this file holds is the read the person-facing routes make — the two git
// reads and the read mint call Kills.Stop with no slug, because they name a
// person and no repository — taken against a real database client rather than
// the package's own fake. The difference is not cosmetic: budget.ReadKills
// builds the repository's path out of the slug, and path building refuses an
// empty one, so the real reader panicked on every well-formed call to those
// routes while the fake answered it (review finding of 2026-11-08, fixed in
// budget.ReadKills, whose own test is internal/budget/kill_test.go's).

import (
	"context"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

func TestThePersonFacingReadIsTheGlobalSwitchAlone(t *testing.T) {
	ctx := context.Background()
	db := gcpfake.NewRTDB(t)
	client, err := rtdb.New(db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	kills := RTDBKills{DB: client}

	// A repository's switch is not the person's: the read names no repository,
	// so it reads no repository node and answers off while one is on.
	db.Set(budget.PathKillRepo("acme/app"), budget.Kill{On: true, By: "operator@example.com"})
	on, which, err := kills.Stop(ctx, "")
	if err != nil {
		t.Fatalf("Stop with no slug: %v", err)
	}
	if on {
		t.Errorf("Stop with no slug is on (%q) while only a repository's switch is on, want off: the route names a person and no repository", which)
	}

	// And the global switch is what stops those routes, which is the sentence
	// the caller refuses the call with.
	db.Set(budget.PathKillGlobal, budget.Kill{On: true, By: "operator@example.com"})
	on, which, err = kills.Stop(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if !on || which != "global" {
		t.Errorf("Stop with no slug = (%v, %q), want the global switch on", on, which)
	}
}
