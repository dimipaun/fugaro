// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See ee/LICENSE for the terms.

// Package register is the one place the seams the moved enterprise features
// fill are set. cmd/fugaro/ee_on.go blank-imports it under `//go:build ee`, so
// an untagged build compiles none of it and has no route here at all (design
// docs/design/ee-boundary-moves.md §5.5).
//
// One file per writer: this one sets the edition (E1.5), and the features that
// move in add their own — coordinator.go (cli.SetLaunchDeclarer and
// cli.RegisterCommand for the coordinator, E2.1), report.go (RegisterCommand for
// the report, and digest.SetSections, E2.2) and governance.go (policy.SetGovernance
// together with runner.SetStage, E2.3, one half alone being a tagged binary that
// enforces half of the governance it registered). Every setter refuses a second
// call and this package panics on whatever error one answers, because a tagged
// binary that quietly kept a core default would carry the code and not the
// enforcement.
//
// The edition is all that is set here today: every other seam's core default
// already is today's behaviour, so a tagged build enforces exactly as an
// untagged one does until the feature that owns the seam moves (G10) — and
// registering them now would mean the core exporting its own defaults, which is
// new API and belongs to the tasks that move the features.
package register

import "github.com/dimipaun/fugaro/internal/cli"

func init() {
	if err := cli.SetEditionEE(); err != nil {
		panic("ee/register: " + err.Error())
	}
}
