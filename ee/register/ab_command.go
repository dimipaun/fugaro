// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

import (
	abcli "github.com/dimipaun/fugaro/ee/ab/cli"
	fcli "github.com/dimipaun/fugaro/internal/cli"
)

func init() {
	// The A/B results command joins the tree the released CLI builds (D570,
	// T1); an untagged build registers nothing, so its `ab` is the core's stub
	// rather than the command (D522). The setter refuses a second call and this
	// package panics on any error rather than keep the stub (§5.5).
	if err := fcli.RegisterCommand("ab", abcli.NewABCmd); err != nil {
		panic("ee/register: " + err.Error())
	}
}
