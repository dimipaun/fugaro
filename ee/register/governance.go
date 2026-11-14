// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

import (
	"github.com/dimipaun/fugaro/ee/governance"
	"github.com/dimipaun/fugaro/internal/digest"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/runner"
)

func init() {
	// The governance pair, both halves in one file: the policy the run's
	// layers are merged under, and the stage tripwire that reads it. One half
	// alone is worse than neither — a tagged binary would carry the policy and
	// not the stage enforcement, or the reverse, while looking to any check
	// that asks only whether a hook is set exactly like a fully wired one
	// (G10, design §5.5). Each setter refuses a second call and this package
	// panics on any error rather than keep the core's default.
	if err := policy.SetGovernance(policy.GovernanceFuncs{
		Defaults: governance.Defaults,
		Tighten:  governance.Tighten,
	}); err != nil {
		panic("ee/register: " + err.Error())
	}
	if err := runner.SetStage(runner.StageFuncs{
		Timeout:  governance.Timeout,
		TimedOut: governance.TimedOut,
	}); err != nil {
		panic("ee/register: " + err.Error())
	}
	// The gateway's own half of the same governance (T7.2): the per-stage
	// budgets, the review ratio and the fan-out stop a run's gateway enforces
	// as its calls are made. It is registered here with the pair above because
	// it is the same policy read at a second place — the stage hook bounds a
	// stage by time, the gateway bounds it by spend, and a tagged binary that
	// carried one and not the other would enforce half the block a repository
	// wrote.
	if err := runner.SetGatewayStageLimit(governance.StageLimit); err != nil {
		panic("ee/register: " + err.Error())
	}
	// The tier calibration's way into the report (T8.3, design §5.4). It is
	// not a half of the pair above: the digest's section slot is a report's
	// own, and an untagged build printing a report without a calibration
	// section is the whole of what it does with the governance it does not
	// have.
	if err := digest.SetSections(governance.Sections); err != nil {
		panic("ee/register: " + err.Error())
	}
}
