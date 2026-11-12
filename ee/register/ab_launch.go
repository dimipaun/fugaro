// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

import (
	"github.com/dimipaun/fugaro/ee/ab"
	fcli "github.com/dimipaun/fugaro/internal/cli"
)

func init() {
	// The launch planner is what makes a launch of a released build sample a
	// pair at all: an untagged build registers none and its launches sample
	// nothing (D550), which is the whole of the model A/B feature's core half
	// (A3.0). The setter refuses a second call, and this package panics on any
	// error rather than keep that default (§5.5).
	if err := fcli.RegisterLaunchPlanner(ab.Plan); err != nil {
		panic("ee/register: " + err.Error())
	}
}
