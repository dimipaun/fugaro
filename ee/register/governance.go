// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

import (
	"github.com/dimipaun/fugaro/ee/governance"
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
}
