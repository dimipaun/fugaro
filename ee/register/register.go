// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See ee/LICENSE for the terms.

// Package register is the one place the seams the moved enterprise features
// fill are set. cmd/fugaro/ee_on.go blank-imports it under `//go:build ee`, so
// an untagged build links none of it into the binary and has no route here at
// all (design docs/design/ee-boundary-moves.md §5.5). The one untagged build
// that does compile this package is its own test binary — `go test ./ee/...`
// carries no tag — and that is how TestTaggedBuildReportsEE checks the edition
// a tagged build reports, not a claim that nothing here runs untagged.
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
// The edition and the coordinator's two seams are what is set here today. The
// seams still waiting for their feature to move keep the core's non-nil
// defaults, so a tagged build behaves exactly as an untagged one does on those
// paths until the task that owns them lands (G10) — and registering them now
// would mean the core exporting its own defaults, which is new API and belongs
// to the tasks that move the features.
package register

import "github.com/dimipaun/fugaro/internal/cli"

func init() {
	if err := cli.SetEditionEE(); err != nil {
		panic("ee/register: " + err.Error())
	}
}
