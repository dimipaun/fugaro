// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

// The map's seam, at the one point that sets it (§5.5): the name the tagged
// tree answers to, and the set-once rule that keeps a second registration from
// replacing it. The command's own tests live beside it in ee/orchestrator/cli;
// what is here is the wiring no package below can see — ee/orchestrator/cli may
// not register itself (only this package may call cli.RegisterSubcommand) and
// its own test may not import this package (ee/register imports it, so that
// import is a cycle).

import (
	"slices"
	"testing"

	mapcli "github.com/dimipaun/fugaro/ee/orchestrator/cli"
	"github.com/dimipaun/fugaro/internal/cli"
)

// TestOrchestratorMapIsRegistered: a build that compiled this package answers
// `fugaro orchestrator map` with the map's own command, carrying the flag the
// console's argv row is built from, and the name is taken for good.
//
// It is a subcommand of the core's own family and not a command of its own
// (CG6): the family is the core's, and this adds one member to it rather than
// taking the name over. The core build answers no `orchestrator map` at all,
// which ee/orchestrator/cli's own tests hold; what is here is that the tagged
// tree does.
func TestOrchestratorMapIsRegistered(t *testing.T) {
	root := cli.NewRootCmd()
	parent, _, err := root.Find([]string{"orchestrator"})
	if err != nil || parent == nil {
		t.Fatalf("the tagged tree has no orchestrator family: %v", err)
	}
	c, _, err := root.Find([]string{"orchestrator", "map"})
	if err != nil || c == nil || c.Name() != "map" {
		t.Fatalf("the tagged tree has no orchestrator map command: %v", err)
	}
	if c.Flags().Lookup("json") == nil {
		t.Error("the tagged tree's orchestrator map has no --json flag: it is the shape the console's argv row spells")
	}
	// The family the command joined is the core's own, and its other members are
	// still there: a registration that replaced the parent would leave the tree
	// answering this one name alone.
	var members []string
	for _, child := range parent.Commands() {
		members = append(members, child.Name())
	}
	for _, want := range []string{"ls", "state", "map"} {
		if !slices.Contains(members, want) {
			t.Errorf("the orchestrator family is %v, want it to carry %s", members, want)
		}
	}

	// The name is taken: a second registration of it is refused, which is the
	// set-once half of the seam (§5.5).
	if err := cli.RegisterSubcommand("orchestrator", "map", mapcli.NewMapCmd); err == nil {
		t.Error("a second registration of orchestrator map was accepted: the tagged tree's command could be replaced")
	}
}
