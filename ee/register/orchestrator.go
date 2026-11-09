// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

import (
	eeorch "github.com/dimipaun/fugaro/ee/orchestrator"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

func init() {
	// The orchestrator's shared half — a handoff between developers, the agent
	// the project owns rather than a person, and how many work items one
	// approval covers — is what the core's seams are for: an untagged build
	// carries the seams and their refusals and none of the features (D1222,
	// F14). The setters refuse a second call, and this package panics on any
	// error rather than keep the core's default (§5.5).
	if err := orchestrator.SetHandoff(orchestrator.HandoffFuncs{Transfer: eeorch.Transfer}); err != nil {
		panic("ee/register: " + err.Error())
	}
	if err := orchestrator.SetBatch(orchestrator.BatchFuncs{Size: eeorch.BatchSize}); err != nil {
		panic("ee/register: " + err.Error())
	}
}
