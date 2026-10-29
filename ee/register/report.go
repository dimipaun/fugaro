// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

import (
	reportcli "github.com/dimipaun/fugaro/ee/report/cli"
	fcli "github.com/dimipaun/fugaro/internal/cli"
)

func init() {
	// The report's command joins the tree the released CLI builds; an untagged
	// build registers nothing, so its `report` is the core's stub rather than
	// the command (D522). The setter refuses a second call and this package
	// panics on any error rather than keep the core stub (§5.5).
	if err := fcli.RegisterCommand("report", reportcli.NewReportCmd); err != nil {
		panic("ee/register: " + err.Error())
	}
}
