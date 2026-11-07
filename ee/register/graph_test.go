// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

// The graph command's seam, at the one point that sets it (design
// docs/design/ee-boundary-moves.md §5.5): the name the tagged tree answers to,
// and the set-once rule that keeps a second registration from replacing it.
// The command's own tests live beside it in ee/graph/cli; what is here is the
// wiring no package below can see — ee/graph/cli may not register itself (only
// this package may call cli.RegisterCommand) and its own test may not import
// this package (ee/register imports it, so that import is a cycle).

import (
	"strings"
	"testing"

	graphcli "github.com/dimipaun/fugaro/ee/graph/cli"
	"github.com/dimipaun/fugaro/internal/cli"
)

// TestGraphRegisteredOnce: a build that compiled this package answers `fugaro
// graph` with the graph's own command, carrying the flags the console's argv
// row is built from, and the name is taken for good.
//
// The name is the whole of the first half: a tagged build shows no paid stub
// (applyExtensions skips one under the ee edition), so a `graph` the tree
// answers to is the registered command and never the core's stand-in. What the
// command prints is ee/graph/cli's own tests'.
func TestGraphRegisteredOnce(t *testing.T) {
	root := cli.NewRootCmd()
	c, _, err := root.Find([]string{"graph"})
	if err != nil || c == nil || c.Name() != "graph" {
		t.Fatalf("the tagged tree has no graph command: %v", err)
	}
	// The two flags the console's argv row spells (--source and --json) are
	// part of what the name means: a registration that landed a different
	// command under it would answer with neither.
	for _, flag := range []string{"source", "json"} {
		if c.Flags().Lookup(flag) == nil {
			t.Errorf("the tagged tree's graph has no --%s flag", flag)
		}
	}

	out, err := execute(t, "graph")
	if err == nil {
		t.Fatal("graph ran with no project selected")
	}
	if strings.Contains(err.Error(), "part of Fugaro Subscription Edition") {
		t.Fatalf("the tagged tree answers the untagged build's stub for graph (%q): ee/register registered nothing", err)
	}
	if out != "" {
		t.Errorf("the refused graph wrote %q to stdout, want nothing", out)
	}

	// The name is taken: a second registration of it is refused, which is the
	// set-once half of the seam (§5.5).
	if err := cli.RegisterCommand("graph", graphcli.NewGraphCmd); err == nil {
		t.Error("a second registration of graph was accepted: the tagged tree's command could be replaced")
	}
}
