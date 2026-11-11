// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

// The `ab` seam, at the one point that sets it (E2.2): the command the tagged
// tree carries under the name. The command's own tests live beside it in
// ee/ab/cli and the rows it prints are ee/report's; what is here is the wiring
// neither of those packages can see — that the name answers with the command
// rather than the stub an untagged build shows (D522, G3).

import (
	"bytes"
	"strings"
	"testing"

	abcli "github.com/dimipaun/fugaro/ee/ab/cli"
	"github.com/dimipaun/fugaro/internal/cli"
)

// TestABCommandRegistered: a build that compiled this package carries the A/B
// command, not the stub an untagged build shows for the name (D522, G3, D570).
// The command answering with its own refusal is what this holds: a call that
// cannot reach a project — an unparseable --since is refused before any
// selection — fails with the command's message, where the stub would answer
// that this build does not include it.
func TestABCommandRegistered(t *testing.T) {
	root := cli.NewRootCmd()
	c, _, err := root.Find([]string{"ab"})
	if err != nil || c == nil || c.Name() != "ab" {
		t.Fatalf("the tagged tree has no ab command: %v", err)
	}
	for _, flag := range []string{"repo", "since", "json"} {
		if c.Flags().Lookup(flag) == nil {
			t.Errorf("the tagged tree's ab has no --%s flag", flag)
		}
	}
	if want := abcli.NewABCmd().Short; c.Short != want {
		t.Errorf("the tagged tree's ab says %q, want the command's own %q", c.Short, want)
	}

	var out bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(cli.NormalizeArgs([]string{"ab", "--since", "bogus"}))
	err = cmd.Execute()
	if err == nil {
		t.Fatal("ab --since bogus was accepted")
	}
	if strings.Contains(err.Error(), "part of Fugaro Subscription Edition") {
		t.Fatalf("the tagged tree answers the untagged build's stub for ab (%q): ee/register registered nothing", err)
	}
	if !strings.Contains(err.Error(), `--since "bogus"`) {
		t.Errorf("ab --since bogus = %v, want the command's own --since refusal", err)
	}

	if out.String() != "" {
		t.Errorf("the refused ab wrote %q to stdout, want nothing", out.String())
	}

	// The name is taken: a second registration of it is refused, which is the
	// set-once half of the seam (§5.5).
	if err := cli.RegisterCommand("ab", abcli.NewABCmd); err == nil {
		t.Error("a second registration of ab was accepted: the tagged tree's command could be replaced")
	}
}
