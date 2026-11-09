// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

// The lapse limit (RS9 of docs/roles-scopes/implementation-plan.md) at the one
// point that sets it (design docs/design/ee-boundary-moves.md §5.5): which
// actions a lapsed subscription keeps, what a Subscription surface answers
// because of it, and the set-once rule that keeps a later caller from handing
// the paid features back. The limit and its installation are one file here
// because only this directory's non-test files may call a seam setter, so there
// is no other place the licence's answer could be installed from; ee/licence is
// where the key state itself lives, and its import list is pinned offline.

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/access"
)

const (
	lapseCaller = "user:alice@example.com"
	lapseRepo   = "github/acme/app"
)

// lapseInput is one stored pair with the repository the policy admits: the
// caller holds the role at project scope, so nothing here turns on a pattern.
func lapseInput(t *testing.T, role access.Role) access.Input {
	t.Helper()
	s, err := access.ParseScope("project")
	if err != nil {
		t.Fatalf("ParseScope(project): %v", err)
	}
	return access.Input{
		Grants:     []access.Grant{{User: lapseCaller, Role: role, Scope: s}},
		AllowRepos: []string{lapseRepo},
	}
}

// TestLapseLimitKeepsReadingAndCancel: the whole table, by action name. Reading
// and cancelling survive a lapse (D1587, D436/T12); launching, a repository's
// settings, the project's settings do not. The last row is the fail-closed half
// — the action list is the core's and may grow there, and a name this file has
// never heard of stays refused until a licensing decision keeps it.
func TestLapseLimitKeepsReadingAndCancel(t *testing.T) {
	for _, c := range []struct {
		action access.Action
		kept   bool
	}{
		{access.ActionViewRepo, true},
		{access.ActionViewProject, true},
		{access.ActionCancel, true},
		{access.ActionLaunch, false},
		{access.ActionManageRepo, false},
		{access.ActionManageProject, false},
		{access.ActionHandoffAgent, false},
		{access.Action("an-action-the-core-does-not-know"), false},
	} {
		if got := lapseLimit(c.action); got != c.kept {
			t.Errorf("lapseLimit(%q) = %v, want %v: a lapsed subscription keeps reading and cancelling and refuses the rest (D1582, D1587)", c.action, got, c.kept)
		}
	}
}

// TestLapseLimitsSubscriptionSurfacesOnly: with the limit installed, a lapsed
// input offers reading and cancelling on a Subscription surface and nothing
// else, while the core's own gate answers the same input exactly as it does
// without a lapse — a licence stops the Subscription surfaces and touches
// neither the assignments nor the MIT CLI (D1582, D1587). The last two cases are
// the other half of the seam: a valid subscription is not a surface with a
// shorter list, and a limit that keeps an action grants it to nobody.
func TestLapseLimitsSubscriptionSurfacesOnly(t *testing.T) {
	lapsed := lapseInput(t, access.RoleAdmin)
	lapsed.Lapse = true

	for _, c := range []struct {
		action access.Action
		repo   string
		kept   bool
	}{
		{access.ActionViewRepo, lapseRepo, true},
		{access.ActionViewProject, "", true},
		{access.ActionCancel, lapseRepo, true},
		{access.ActionLaunch, lapseRepo, false},
		{access.ActionManageRepo, lapseRepo, false},
		{access.ActionManageProject, "", false},
		{access.ActionHandoffAgent, "", false},
	} {
		if got := access.SubscriptionAllowed(lapsed, lapseCaller, c.action, c.repo); got != c.kept {
			t.Errorf("a Subscription surface answers %v for %s doing %s while the subscription lapsed, want %v: a lapse keeps reading and cancelling and refuses launching and every change (D1582, D1587)", got, lapseCaller, c.action, c.kept)
		}
	}

	// The core's own gate, on the same lapsed input and with the limit
	// installed: unchanged. A core command asks this, and a licence may not stop
	// it (D1582).
	for _, c := range []struct {
		action access.Action
		repo   string
	}{
		{access.ActionLaunch, lapseRepo},
		{access.ActionCancel, lapseRepo},
		{access.ActionManageRepo, lapseRepo},
		{access.ActionManageProject, ""},
	} {
		if !access.Allowed(lapsed, lapseCaller, c.action, c.repo) {
			t.Errorf("the core gate refuses %s for an admin while the subscription lapsed: the MIT CLI asks Allowed and no licence state reaches it, so a lapse may not stop a core command (D1582)", c.action)
		}
	}

	valid := lapseInput(t, access.RoleAdmin)
	if !access.SubscriptionAllowed(valid, lapseCaller, access.ActionLaunch, lapseRepo) {
		t.Error("a Subscription surface refuses a launch for an admin while the subscription is valid: the limit is consulted only while the input lapsed, and a valid subscription is not a surface with a shorter action list")
	}

	viewer := lapseInput(t, access.RoleViewer)
	viewer.Lapse = true
	if access.SubscriptionAllowed(viewer, lapseCaller, access.ActionLaunch, lapseRepo) {
		t.Error("a Subscription surface lets a viewer launch while the subscription lapsed: the limit only ever refuses — the role rules still answer, so keeping an action cannot grant it")
	}
	if access.SubscriptionAllowed(viewer, lapseCaller, access.ActionCancel, lapseRepo) {
		t.Error("a Subscription surface lets a viewer cancel while the subscription lapsed: cancelling takes an operator (D1587), and a lapse keeps it for the people who held it rather than handing it to everyone")
	}
}

// TestLapseLimitIsSetOnce: the seam is filled once, by this package, and a
// second installation is refused — a limit installed after the build's own would
// be a way to hand every paid surface back in a lapsed binary (design §5.5).
func TestLapseLimitIsSetOnce(t *testing.T) {
	if err := access.SetSubscriptionLimit(func(access.Action) bool { return true }); err == nil {
		t.Error("a second access.SetSubscriptionLimit was accepted: the tagged binary's limit is the licence's, and a later caller must not be able to replace it")
	}
	if err := access.SetSubscriptionLimit(nil); err == nil {
		t.Error("access.SetSubscriptionLimit(nil) was accepted after the seam was set: a nil limit is a panic in the reader rather than a decision")
	}
}
