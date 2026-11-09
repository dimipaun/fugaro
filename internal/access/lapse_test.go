package access

import "testing"

// The lapse input (RS9 of docs/roles-scopes/implementation-plan.md): a
// subscription that lapses is a licence-gate input and never a role rewrite
// (D1582, F12), so the two properties held here are the two a cap written as a
// grant edit breaks — no assignment moves, and the state is read at the moment
// of the decision rather than stamped onto the grants. What a Subscription
// surface keeps while the input lapsed is the licence's own answer and is held
// in ee/ (ee/licence), beside the table it lists; what the core answers is
// Allowed, which no licence state reaches.

// lapseAsks is one ask of the resolver, paired with the repository it names: a
// project-level action takes none.
type lapseAsk struct {
	caller string
	action Action
	repo   string
}

// lapseAsks is a walk over every action the core knows, for the two callers a
// test grants, so an action added later is covered here rather than by a list
// somebody has to remember to extend.
func lapseAsks(callers ...string) []lapseAsk {
	var out []lapseAsk
	for action, rule := range actionRules {
		repo := "github/acme/api"
		if rule.project {
			repo = ""
		}
		for _, caller := range callers {
			out = append(out, lapseAsk{caller: caller, action: action, repo: repo})
		}
	}
	return out
}

// TestLapseKeepsAssignments: a lapse removes nothing. Every caller keeps the
// role it held in every repository, and the MIT core's own gate — Allowed, the
// one every core command asks — answers exactly as it did without the lapse, for
// every action the core knows. A lapse that dropped, added or re-ranked a grant
// would be the role rewrite D1582 refused; a lapse that made Allowed refuse a
// launch would stop the free core the same answer protects.
func TestLapseKeepsAssignments(t *testing.T) {
	in := knows(
		grant(alice, RoleOperator, scope(t, "github/acme/api-*")),
		grant(bob, RoleViewer, scope(t, "all-repos")),
		grant(carol, RoleAdmin, scope(t, "project")),
	)
	lapsed := in
	lapsed.Lapse = true

	for _, c := range []struct{ caller, repo string }{
		{alice, "github/acme/api"},
		{alice, "github/acme/app"},
		{bob, "github/acme/group/nested"},
		{carol, "bitbucket/other/thing"},
		{"user:nobody@example.com", "github/acme/app"},
	} {
		before := Effective(in, c.caller, c.repo)
		if after := Effective(lapsed, c.caller, c.repo); after != before {
			t.Errorf("Effective answers %q for %s in %s while the input lapsed and %q without it: a lapse caps the Subscription surfaces and touches no assignment, so the grants stay the ones that were stored and the rank order is unchanged (D1582, F12)", after, c.caller, c.repo, before)
		}
	}

	for _, ask := range lapseAsks(alice, bob, carol) {
		before := Allowed(in, ask.caller, ask.action, ask.repo)
		if after := Allowed(lapsed, ask.caller, ask.action, ask.repo); after != before {
			t.Errorf("Allowed answers %v for %s doing %s while the input lapsed and %v without it: the core asks Allowed, so a lapse that changed this answer would stop the MIT CLI, which no licence state may do (D1582)", after, ask.caller, ask.action, before)
		}
	}
}

// TestResumeRestoresAssignments: the lapse is an input and not a state this
// package keeps, so a resume has nothing to restore — the same grants, asked
// again with Lapse false, answer what they answered before the lapse began. And
// while it does hold, a core that installed no limit — an untagged build, which
// is the one this package's own tests are — refuses nothing on a Subscription
// surface either: the seam's default keeps every action, which is why a build
// without ee/ behaves on these paths exactly as it did before there was a seam
// (design ee-boundary-moves.md §5.5).
func TestResumeRestoresAssignments(t *testing.T) {
	in := knows(
		grant(alice, RoleOperator, scope(t, "github/acme/api-*")),
		grant(carol, RoleAdmin, scope(t, "project")),
	)
	asks := lapseAsks(alice, carol)

	for _, ask := range asks {
		before := SubscriptionAllowed(in, ask.caller, ask.action, ask.repo)

		lapsed := in
		lapsed.Lapse = true
		if got := SubscriptionAllowed(lapsed, ask.caller, ask.action, ask.repo); got != before {
			t.Errorf("a Subscription surface answers %v for %s doing %s on a lapsed input and %v without one, in a build that installed no limit: the core's own limit keeps every action (the seam's default), and a build without ee/ behaves as it did before there was a seam (design ee-boundary-moves.md §5.5)", got, ask.caller, ask.action, before)
		}

		resumed := lapsed
		resumed.Lapse = false
		if got := SubscriptionAllowed(resumed, ask.caller, ask.action, ask.repo); got != before {
			t.Errorf("a Subscription surface answers %v for %s doing %s after the lapse was lifted and %v before it began: the lapse is read from the input at the moment of the decision and never written into the grants, so a resume has nothing to restore (D1582)", got, ask.caller, ask.action, before)
		}
	}
}
