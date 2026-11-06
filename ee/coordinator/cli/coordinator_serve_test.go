// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"strings"
	"testing"
)

// TestCoordinatorServeIsHidden: the resident service's process is the front
// door's revision entry point and not a command of the coordinator's surface,
// so the generated CLI reference leaves it out — the rule the sweep follows,
// and the rule the credentials service's own serve follows in the core tree.
//
// Both halves are asserted, like the sweep's: the help output (what a reader
// sees) and the command's own Hidden field (what makes it stay that way the day
// the group gains a visible subcommand of its own).
func TestCoordinatorServeIsHidden(t *testing.T) {
	out, _, err := execute(t, "coordinator", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "serve ") {
			t.Fatalf("fugaro coordinator --help lists serve:\n%s", out)
		}
	}
	for _, c := range coordinatorTestRoot().Commands() {
		if c.Name() != "coordinator" {
			continue
		}
		for _, sub := range c.Commands() {
			if sub.Name() == "serve" && !sub.Hidden {
				t.Errorf("fugaro coordinator serve is not hidden")
			}
		}
	}
}
