package cli

import (
	"context"
	"strings"
	"testing"
)

// TestDecideIsOffByDefault: the decision agent is a fresh child process on the
// person's own Claude login (D1228), so `--decide` is off unless they ask for
// it, and a daemon that was not asked builds no seam at all — a tick with none
// judges nothing however many judgement cases its node carries (O13).
//
// What the flag is wired to is pinned at the seam rather than by running a
// cycle: a daemon started with `--decide` over a node carrying a judgement case
// would make a real call on whoever's login is on this machine, and a test must
// not spend a person's money to prove a wire.
func TestDecideIsOffByDefault(t *testing.T) {
	cmd := newOrchestratorRunCmd()
	flag := cmd.Flags().Lookup("decide")
	if flag == nil {
		t.Fatal("fugaro orchestrator run carries no --decide flag, so no person can ask it to judge anything")
	}
	if flag.DefValue != "false" {
		t.Errorf("--decide defaults to %q, and it is off unless a person asks", flag.DefValue)
	}
	if err := cmd.Flags().Parse([]string{"--decide"}); err != nil {
		t.Fatalf("parsing --decide: %v", err)
	}
	if on, err := cmd.Flags().GetBool("decide"); err != nil || !on {
		t.Fatalf("--decide parses to %v (%v), and the flag is how the agent is asked for", on, err)
	}
	if out, _, err := execute(t, "orchestrator", "run", "--help"); err != nil || !strings.Contains(out, "--decide") {
		t.Errorf("the help of fugaro orchestrator run never names --decide: %q, %v", out, err)
	}

	f := newDaemonFixture(t)
	d, _ := f.daemon(t, "")
	ctx := context.Background()
	if state, err := d.lease.Acquire(ctx); err != nil || !state.Held {
		t.Fatalf("taking the lease: %+v, %v", state, err)
	}
	off, err := d.newTick(ctx)
	if err != nil {
		t.Fatalf("building the tick of a daemon started without --decide: %v", err)
	}
	if off.Decide.Set() {
		t.Error("a daemon started without --decide built decide seams, so a cycle of it could make a model call nobody asked for")
	}

	d.decide = true
	on, err := d.newTick(ctx)
	if err != nil {
		t.Fatalf("building the tick of a daemon started with --decide: %v", err)
	}
	if !on.Decide.Set() {
		t.Error("--decide left the tick with no agent or no log, so it could reach no judgement however many cases its node carries")
	}
}
