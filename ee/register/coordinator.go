// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See ee/LICENSE for the terms.

package register

import (
	"github.com/dimipaun/fugaro/ee/coordinator/cli"
	fcli "github.com/dimipaun/fugaro/internal/cli"
)

func init() {
	// The coordinator's command joins the tree the released CLI builds, and
	// the launch declarer is the coordinator's own writer: an untagged build
	// registers neither, so its launcher declares nothing (D535) and its
	// `coordinator` is the core's stub rather than the command. Both setters
	// refuse a second call, and this package panics on any error rather than
	// keep the core default (§5.5).
	if err := fcli.RegisterCommand("coordinator", cli.NewCoordinatorCmd); err != nil {
		panic("ee/register: " + err.Error())
	}
	if err := fcli.SetLaunchDeclarer(cli.Declare); err != nil {
		panic("ee/register: " + err.Error())
	}
}
