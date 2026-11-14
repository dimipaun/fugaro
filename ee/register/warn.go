// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

// The licence's warnings (plan K3.5): who a key's own `warnings` reaches, and
// when, and how the tagged CLI's licence commands say it.
//
// The policy is the key's and not this build's (spec section 3.2's `warnings`:
// one window per roles-and-`from` pair, defaulted to admins from the first
// moment of the grace and every role a week in). A window is counted from the
// start of the key's grace — the end of its paid term, which is what
// `grace_start` names — and stays open from its instant on, so a state this
// build does not restate decides the rest: a key inside its paid term warns
// nobody (spec section 7.1's "Active: nobody"), and the last window is still
// open after the grace has run out, which is what says a lapsed installation
// out loud.
//
// Two surfaces read these rules and they are the two F6 allows: the field
// `licence status --json` carries, which the console and the app weigh against
// their own reader's role, and the one line the tagged CLI's own commands write
// to standard error. Nothing in the core reads a key, and an untagged build
// warns about nothing.

import (
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/ee/licence"
	"github.com/dimipaun/fugaro/ee/licence/project"
	"github.com/dimipaun/fugaro/internal/access"
)

// licenceWarningTerms are the facts of a key a window is read against: the
// windows the key carries, the end of its paid term they are counted from, the
// grace it carries, and the state the clock puts it in. It is assembled from a
// claim set where one is in hand (`set`, `refresh`) and from the project store's
// View where the reader is not entitled to the claim set — `licence status`
// shows a viewer the state and never the key (D2137) — so the rules below read
// neither shape directly.
type licenceWarningTerms struct {
	Warnings    []licence.WarningV1
	PaidThrough time.Time
	GraceDays   int
	State       licence.State
}

// licenceWarningTermsOfKey reads the terms off a claim set, which `set` and
// `refresh` hold: a key they have just verified and are about to print.
func licenceWarningTermsOfKey(claims licence.Claims, state licence.State) licenceWarningTerms {
	return licenceWarningTerms{
		Warnings:    claims.Warnings,
		PaidThrough: claims.PaidThrough,
		GraceDays:   claims.GraceDays,
		State:       state,
	}
}

// licenceWarningTermsOfView reads the same facts off the project store's View,
// which is the shape `licence status` shows a reader who is not entitled to the
// claim set: the key's policy and its dates and never the key (D2137).
func licenceWarningTermsOfView(view project.View) licenceWarningTerms {
	return licenceWarningTerms{
		Warnings:    view.Warnings,
		PaidThrough: view.PaidThrough,
		GraceDays:   view.GraceDays,
		State:       view.State,
	}
}

// licenceWarnSpan is a count of days as a duration, clamped at the verifier's
// own longest span (ee/licence's MaxIntervalDays, which its graceDuration
// clamps at too). A duration in nanoseconds runs out at about 106,751 days, and
// a claim set carrying an absurd `grace_days` must not be multiplied past that:
// the sum would wrap into a grace that ends years before the term it is counted
// from, and the sentence would name a date in the past as the one to act on.
func licenceWarnSpan(days int) time.Duration {
	if days <= 0 {
		return 0
	}
	if int64(days) > licence.MaxIntervalDays {
		return time.Duration(licence.MaxIntervalDays) * 24 * time.Hour
	}
	return time.Duration(days) * 24 * time.Hour
}

// licenceWarnRole answers the role a tagged CLI's own commands speak for.
//
// They cannot read their caller's role: the licence commands make no grants
// read, and the one that would answer it is a database read every licence
// command would pay for. So they speak for the installation's admin, which is
// the role both of the default policy's windows reach — and a person reading a
// licence command's output on their own machine is the one who installs and
// renews this installation's key (spec section 10, section 11.2: the admins
// hold the key, the seats and the walk-back). The narrower windows are the
// console's and the app's to deliver, and they know their reader: an operator
// who must be told in the last week is told there, with the field below.
const licenceWarnRole = access.RoleAdmin

// licenceWarning is one window as this build reads it: the roles it reaches at
// this build's own resolution, the instant it opens, and the claim it was
// written as.
type licenceWarning struct {
	Roles []access.Role
	Opens time.Time
	From  string
}

// licenceWarnings answers the windows that are open at one instant, in the
// order they open: each window whose form this build can place and whose roles
// it knows.
//
// A window this build cannot place is not a window: a signed key's unknown
// optional shape is ignored rather than fatal (D2146), and the one thing worse
// than a window nobody reads is a licence refused whole over it. A window whose
// roles all belong to another build is the same answer — it tells nobody here.
func licenceWarnings(terms licenceWarningTerms, now time.Time) []licenceWarning {
	if terms.State == licence.Active {
		return nil
	}
	out := make([]licenceWarning, 0, len(terms.Warnings))
	for _, window := range terms.Warnings {
		opens, ok := licenceWarningOpens(terms.PaidThrough, window.From)
		if !ok || now.Before(opens) {
			continue
		}
		roles := make([]access.Role, 0, len(window.Roles))
		for _, name := range window.Roles {
			if role, ok := access.ParseRole(name); ok && !slices.Contains(roles, role) {
				roles = append(roles, role)
			}
		}
		if len(roles) == 0 {
			continue
		}
		out = append(out, licenceWarning{Roles: roles, Opens: opens, From: window.From})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Opens.Before(out[j].Opens) })
	return out
}

// licenceWarningOpens places one window's `from`, which is the start of the
// key's grace (`grace_start`, the end of its paid term) or a whole number of
// days after it (`grace_start+7d`). Anything else is a form this build does not
// know and answers false: the claim is the key's own text, and only the two
// forms the key policy is written in are windows here. A count past the longest
// span a duration holds is one of those: the sum would wrap, and a window that
// opened decades ago is worse than one this build declines to place.
func licenceWarningOpens(paidThrough time.Time, from string) (time.Time, bool) {
	rest, ok := strings.CutPrefix(from, "grace_start")
	if !ok {
		return time.Time{}, false
	}
	if rest == "" {
		return paidThrough, true
	}
	rest, ok = strings.CutPrefix(rest, "+")
	if !ok {
		return time.Time{}, false
	}
	days, ok := strings.CutSuffix(rest, "d")
	if !ok {
		return time.Time{}, false
	}
	n, err := strconv.Atoi(days)
	if err != nil || n < 0 {
		return time.Time{}, false
	}
	if int64(n) > licence.MaxIntervalDays {
		return time.Time{}, false
	}
	return paidThrough.Add(time.Duration(n) * 24 * time.Hour), true
}

// licenceWarnGraceEnd is when the key's grace runs out, counted with the same
// clamp the verifier's own grace arithmetic stops at, so an absurd `grace_days`
// names the longest grace this build can place rather than one wrapped into the
// past. It lands at or before the verifier's own end, which is the safe side: a
// warning never says the grace has ended while the verifier still calls the key
// in grace.
func licenceWarnGraceEnd(terms licenceWarningTerms) time.Time {
	return terms.PaidThrough.Add(licenceWarnSpan(terms.GraceDays))
}

// licenceWarningFor answers the newest window that reaches one role at one
// instant. The newest is what a reader is owed: two windows may reach the same
// role, and the later one is the news that has just changed.
func licenceWarningFor(terms licenceWarningTerms, now time.Time, role access.Role) (licenceWarning, bool) {
	var found licenceWarning
	open := false
	for _, window := range licenceWarnings(terms, now) {
		if !slices.Contains(window.Roles, role) {
			continue
		}
		if !open || !window.Opens.Before(found.Opens) {
			found, open = window, true
		}
	}
	return found, open
}

// licenceWarningMessage is the one sentence a warning says, and it says the
// same thing to every role: the two dates a person acts on and the command that
// acts. Which roles are told is the window's business and is never in the
// sentence, so a product that shows it — the console, the app — shows one text
// to everyone the window reaches.
func licenceWarningMessage(terms licenceWarningTerms) string {
	paid := terms.PaidThrough.UTC().Format(time.RFC3339)
	grace := licenceWarnGraceEnd(terms).UTC().Format(time.RFC3339)
	if terms.State == licence.Expired {
		return fmt.Sprintf("licence: the paid period ended on %s and the grace ended on %s: install a new key with `fugaro licence set`", paid, grace)
	}
	return fmt.Sprintf("licence: the paid period ended on %s and the grace ends on %s: renew it with `fugaro licence refresh` before then", paid, grace)
}

// licenceWarnLine writes the warning a tagged CLI's own command owes, and
// answers whether it wrote one. It goes to the command's standard error and
// never its standard output: what a script reads from `licence status --json`
// is the field below, and a line mixed into it would be a document that no
// longer parses.
func licenceWarnLine(w io.Writer, terms licenceWarningTerms, now time.Time) bool {
	if _, warned := licenceWarningFor(terms, now, licenceWarnRole); !warned {
		return false
	}
	fmt.Fprintln(w, licenceWarningMessage(terms))
	return true
}

// licenceWarningJSON is `licence status --json`'s `warning`: the windows a
// reader outside this process weighs against its own reader's role, and null
// while none is open.
//
// The windows are listed rather than merged, because they do not all reach the
// same roles at the same instant: the default policy reaches admins from the
// first moment of the grace and every role a week in, so one reader is warned
// from the start of the grace and another from that week. A reader answers its
// own question the way this build does — the newest window whose Roles contain
// the reader's role — instead of reading one instant that is only true of some
// of the roles beside it. The order is the key's own, window by window, and a
// reader that shows one banner shows Message, which is the sentence the CLI
// writes and is the same to everyone.
type licenceWarningJSON struct {
	Windows []licenceWarningWindowJSON `json:"windows"`
	Message string                     `json:"message"`
}

// licenceWarningWindowJSON is one open window as a reader outside this process
// sees it: the roles it reaches and the instant it opened.
type licenceWarningWindowJSON struct {
	Opens time.Time `json:"opens"`
	Roles []string  `json:"roles"`
}

// licenceStatusWarning is the field: every window open at this instant, each
// with the roles it reaches and when it opened, and the sentence above. The
// roles are in the key's own order and the windows in the order they open.
func licenceStatusWarning(terms licenceWarningTerms, now time.Time) *licenceWarningJSON {
	windows := licenceWarnings(terms, now)
	if len(windows) == 0 {
		return nil
	}
	out := make([]licenceWarningWindowJSON, 0, len(windows))
	for _, window := range windows {
		roles := make([]string, 0, len(window.Roles))
		for _, role := range window.Roles {
			roles = append(roles, string(role))
		}
		out = append(out, licenceWarningWindowJSON{Opens: window.Opens.UTC(), Roles: roles})
	}
	return &licenceWarningJSON{Windows: out, Message: licenceWarningMessage(terms)}
}
