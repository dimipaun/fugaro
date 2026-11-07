// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

import (
	graphcli "github.com/dimipaun/fugaro/ee/graph/cli"
	fcli "github.com/dimipaun/fugaro/internal/cli"
)

func init() {
	// The graph's command joins the tree the released CLI builds: its `all`
	// scope is the coordinator's stored view, so it is a paid command and lives
	// in ee/ (D1362, F26), and an untagged build answers the core's stub under
	// this name instead. The setter refuses a second call, and this package
	// panics on any error rather than keep the stub (§5.5).
	if err := fcli.RegisterCommand("graph", graphcli.NewGraphCmd); err != nil {
		panic("ee/register: " + err.Error())
	}
}
