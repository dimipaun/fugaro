// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

import "github.com/dimipaun/fugaro/internal/access"

func init() {
	// The lapse limit (roles-scopes RS9): the actions a lapsed subscription
	// keeps on the Subscription surfaces, which is the whole of what a lapse
	// does to them — the grants are the caller's input and are never rewritten
	// (D1582, F12). The setter refuses a second call and this package panics on
	// any error rather than keep the core's default, which keeps every action
	// and would be a tagged binary whose licence limits nothing.
	if err := access.SetSubscriptionLimit(lapseLimit); err != nil {
		panic("ee/register: " + err.Error())
	}
}

// lapseLimit is what a lapsed subscription keeps on a Subscription surface
// (D1582; D1587 and D436/T12 as the owner ruled them, which amend the spec's
// "all users are limited to viewer"): reading — the two viewer actions, which is
// where that sentence lands once the MIT core is left out of it — and cancelling
// a run, which the Terms' read-only use keeps (§9.1). Everything a subscription
// is bought for is refused: launching, a repository's or the project's settings,
// and handing an agent to another developer.
//
// The kill switch is not listed because it needs no entry: it is the core
// command `fugaro budget kill`, a MIT safety feature (R4) that a lapse never
// gates, so the console and the app reach it through the CLI exactly as they do
// today.
//
// The switch is spelled out and its default refuses, so an action added to the
// core's table later is refused under a lapse until a licensing decision keeps
// it — the fail-closed half of access's own rule, on the one list where keeping
// an action is a licensing decision rather than a code one.
//
// It is here rather than beside the licence's own code in ee/licence because
// that package's import list is pinned so the key check stays offline
// (TestLicencePackageImportsNoNetwork): internal/access carries the credential
// reader and with it an HTTP client, and the verifier is the one place in this
// directory that may not grow one.
func lapseLimit(action access.Action) bool {
	switch action {
	case access.ActionViewRepo, access.ActionViewProject, access.ActionCancel:
		return true
	default:
		return false
	}
}
