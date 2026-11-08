// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

import (
	eeorch "github.com/dimipaun/fugaro/ee/orchestrator"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

func init() {
	// The orchestrator's shared half — a handoff between developers, and the
	// agent the project owns rather than a person — is what the core's handoff
	// seam is for: an untagged build carries the seam and its refusal and no
	// handoff at all (D1222, F14). The setter refuses a second call, and this
	// package panics on any error rather than keep the core's default (§5.5).
	if err := orchestrator.SetHandoff(orchestrator.HandoffFuncs{Transfer: eeorch.Transfer}); err != nil {
		panic("ee/register: " + err.Error())
	}
}
