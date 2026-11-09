package access

import "errors"

// A lapse is a licence state, and it is not a role rewrite (D1582, F12): when a
// subscription lapses the Subscription surfaces — the console, the app and the
// commands that live in ee/ — lose launching and every configuration change,
// keep reading and cancelling, and every assignment stays exactly as it was. The
// MIT core CLI is unaffected, because its commands ask Allowed, which no licence
// state reaches; the kill switch stays reachable for the same reason, being a
// core command (`fugaro budget kill`) that a lapse never gates (R4, T12).
//
// This file is the core half of that. It holds no licence code: the shape a
// surface asks its question through (Limit), the seam the licence state fills
// (SetSubscriptionLimit) and the one decision that combines the two
// (SubscriptionAllowed). Which actions a lapse keeps is the licence's answer and
// lives in ee/ (installed from ee/register), the only caller of the setter — the rule
// every moved feature's seam obeys (design ee-boundary-moves.md §5.5, roles.md
// D694).

// Limit is what a lapsed Subscription surface keeps: given one action, it
// answers whether the surface still offers it while a subscription is lapsed.
//
// It answers nothing else. Whether the subscription has in fact lapsed is the
// input's (Input.Lapse), and what a caller holds is the role rules' — a limit
// that keeps an action never grants it, so an empty grant list stays empty
// however permissive the limit is. There is no zero value a reader falls back
// on: the seam is never nil, and a caller that has no limit to install does not
// install one.
type Limit func(action Action) bool

// subscriptionLimit is the installed limit. The core's own keeps every action,
// because an untagged build has no licence — after a lapse the MIT core CLI is
// unaffected (D1582) — and it is deliberately not nil: SubscriptionAllowed calls
// it whenever the input lapsed, so the default has to answer.
var subscriptionLimit Limit = keepEveryAction

// subscriptionLimitSet records that a caller installed a limit, which only
// SetSubscriptionLimit does.
var subscriptionLimitSet bool

// SetSubscriptionLimit installs the limit the Subscription surfaces ask through
// SubscriptionAllowed, refusing a second call: what a surface keeps is decided
// once, by ee/register, and a limit that could be replaced after it would be a
// way to hand the paid features back in a binary whose subscription has lapsed.
// A nil limit is refused for the same reason the core default is not nil — the
// reader calls it, and a nil one is a panic rather than a decision.
func SetSubscriptionLimit(l Limit) error {
	if subscriptionLimitSet {
		return errors.New("access: the subscription limit is already set")
	}
	if l == nil {
		return errors.New("access: the subscription limit needs a function")
	}
	subscriptionLimit, subscriptionLimitSet = l, true
	return nil
}

// keepEveryAction is the untagged build's limit: a Subscription surface asks it
// and is refused nothing, so a build that links no ee/ answers on these paths
// exactly as it did before there was a seam. It is the licence half of "the MIT
// core is unaffected" — the surface half is that no core command asks through
// here at all.
func keepEveryAction(Action) bool { return true }

// SubscriptionAllowed reports whether caller may do action on a Subscription
// surface: Allowed's answer, with the licence state's limit on top while the
// input lapsed. The limit is consulted only then — a valid subscription is not a
// surface with a shorter action list — and it only ever refuses, so the role
// rules still decide what a caller holds and a limit that kept an action grants
// it to nobody.
//
// Nothing in the core asks this. The MIT CLI's own commands ask Allowed, which
// is what makes a lapse stop the Subscription surfaces and nothing else
// (D1582); the surfaces that do ask are ee/'s, through the limit ee/register
// installed.
func SubscriptionAllowed(in Input, caller string, action Action, repo string) bool {
	if in.Lapse && !subscriptionLimit(action) {
		return false
	}
	return Allowed(in, caller, action, repo)
}
