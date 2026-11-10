package budget

import (
	"fmt"
	"sort"
)

// The reconciliation alarm: the workspace's own count against a member's own
// ledger, per member (docs/workspaces/spec.md §5, D1301's amendment). The meter
// is the basis now, so this comparison is a CHECK rather than the figure: a
// disagreement beyond a stated bound is REPORTED — never silently averaged, and
// never silently trusted in either direction — because what it makes visible is
// a member's under-reporting, which is exactly what the cap no longer depends
// on. Neither figure is adjusted by it: the service's count stays the figure
// the cap is measured against, and the member's ledger stays authoritative for
// the member's own caps.
//
// It is a pure function of what was read, so the reading is the caller's: the
// service (or the history job on its schedule) reads its own month out of the
// workspace's database and each member's ledger out of the member's own, and
// hands both here. Nothing in this file does I/O, and nothing in it decides
// which node of a member's database its ledger is.
//
// The bound is a NUMBER and not an adjective. Two figures of one workspace
// differ legitimately by the calls in flight: a call the service has taken room
// for and settled may not be in the member's ledger yet, and the other way
// round, so the reservations in flight are part of the bound — each such call
// can account for at most its own amount. What is left is the slack the caller
// states (`WorkspaceReconcileInput.Slack`), which is where a deployment puts
// whatever rounding it wants to tolerate; zero is exact agreement.

// WorkspaceLedger is one member's side of the comparison: what the member's own
// ledger holds for the month, or the reason it could not be read.
//
// It carries no database URL and no bucket, and neither can be missing from it.
// A member's database is derived from the verified `iss` its own tokens carry
// (spec §5, finding 6), and the bucket read the old rollup used is gone (O1's
// answer): an entry that carried either would be an entry an owner could splice
// another member's state into.
type WorkspaceLedger struct {
	// Member is the member's project name, as the workspace's record names it.
	Member string
	// Spent is what the member's own ledger counted for the month: the calls
	// the workspace's credentials paid for, as the member recorded them.
	Spent Micros
	// Unbound: the workspace's table does not bind this member — a job service
	// account, a Firebase project or an exchange URL is missing from its row,
	// so the member cannot be resolved as a caller and its ledger cannot be
	// read at all. Such a member is reported rather than counted as zero: a
	// zero would read as a member that spent nothing.
	Unbound bool
	// Unreadable is why the member's ledger could not be read, when it could
	// not be. An unreadable member is reported for the same reason an unbound
	// one is: the comparison has nothing to compare for it.
	Unreadable error
}

// WorkspaceReconcileInput is everything the alarm reads.
type WorkspaceReconcileInput struct {
	// Name is the workspace, and Month the month compared: the same month the
	// service's count is keyed by.
	Name  string
	Month string
	// Spend is the service's own count for the month, as
	// ReadWorkspaceSpend returns it. Its reservations in flight widen the
	// bound: they are the disagreement a healthy workspace has.
	Spend WorkspaceSpend
	// Slack is what the caller tolerates beyond the calls in flight, in µ$,
	// for the two sides' rounding. Zero is exact agreement.
	Slack Micros
	// Ledger is each member's own figure, or why it is not one. A member the
	// service counted and this list does not carry at all is reported as
	// unreadable: its ledger was not read, and a comparison that skipped it
	// would be a member trusted silently.
	Ledger []WorkspaceLedger
}

// Why a member is in an alarm. They are the three ways a comparison can fail to
// hold, and each is reported as itself: a disagreement is not a missing member
// and an unbound member is not a member that spent nothing.
const (
	// AlarmDisagrees: the two figures differ by more than the bound.
	AlarmDisagrees = "disagrees"
	// AlarmUnbound: the workspace's table does not bind the member, so it
	// cannot be resolved as a caller and its ledger cannot be read.
	AlarmUnbound = "unbound"
	// AlarmUnreadable: the member's ledger could not be read, or was not read
	// at all.
	AlarmUnreadable = "unreadable"
)

// WorkspaceAlarmMember is one member the alarm reports.
type WorkspaceAlarmMember struct {
	Member string
	// Service is the service's own count for the member, and Ledger what the
	// member's own ledger holds. Ledger is 0 for an unbound or unreadable
	// member, and Why says so rather than the zero speaking for itself.
	Service Micros
	Ledger  Micros
	// Delta is Ledger − Service: negative is a member that counted less than
	// the service paid for, which is the direction the alarm exists for.
	Delta Micros
	// Why is one of AlarmDisagrees, AlarmUnbound or AlarmUnreadable.
	Why string
	// Unreadable is the read's own error, when there was one.
	Unreadable error
	// Sentence is the report, in the words a person reads: the two figures,
	// the delta and the bound.
	Sentence string
}

// WorkspaceAlarm is what one run of the alarm found. An alarm with no members
// is a workspace whose two figures agree.
type WorkspaceAlarm struct {
	Name  string
	Month string
	// Bound is the number the run reported beyond: the reservations in flight
	// plus the caller's slack.
	Bound    Micros
	Reported []WorkspaceAlarmMember
}

// Clear reports whether there is nothing to report.
func (a WorkspaceAlarm) Clear() bool { return len(a.Reported) == 0 }

// ReconcileWorkspace compares the service's own count for one month against each
// member's own ledger and reports every member whose two figures do not hold.
func ReconcileWorkspace(in WorkspaceReconcileInput) WorkspaceAlarm {
	bound := add(in.Spend.Reserved(), in.Slack)
	alarm := WorkspaceAlarm{Name: in.Name, Month: in.Month, Bound: bound}
	seen := map[string]bool{}
	for _, l := range in.Ledger {
		if seen[l.Member] {
			// A member named twice would be compared twice and reported twice,
			// which is a caller's bug and not a second member; the first entry
			// is the one compared.
			continue
		}
		seen[l.Member] = true
		service := in.Spend.Members[l.Member]
		switch {
		case l.Unbound:
			alarm.Reported = append(alarm.Reported, WorkspaceAlarmMember{
				Member: l.Member, Service: service, Why: AlarmUnbound,
				Sentence: fmt.Sprintf("member %s is not bound by the workspace's table, so it cannot be resolved as a caller and its ledger cannot be read; the workspace's own count for it is %s", l.Member, dollars(service)),
			})
		case l.Unreadable != nil:
			alarm.Reported = append(alarm.Reported, WorkspaceAlarmMember{
				Member: l.Member, Service: service, Why: AlarmUnreadable, Unreadable: l.Unreadable,
				Sentence: fmt.Sprintf("member %s's ledger could not be read (%v); the workspace's own count for it is %s", l.Member, l.Unreadable, dollars(service)),
			})
		default:
			delta := l.Spent - service
			if !beyond(delta, bound) {
				continue
			}
			alarm.Reported = append(alarm.Reported, WorkspaceAlarmMember{
				Member: l.Member, Service: service, Ledger: l.Spent, Delta: delta, Why: AlarmDisagrees,
				Sentence: fmt.Sprintf("member %s's ledger counts %s and the workspace's own count is %s: %s apart, beyond the bound of %s (the calls in flight plus %s of slack)", l.Member, dollars(l.Spent), dollars(service), dollars(abs(delta)), dollars(bound), dollars(in.Slack)),
			})
		}
	}
	// A member the service counted whose ledger was not read at all is a
	// comparison that never happened, and it is reported rather than skipped:
	// the alarm never trusts a member silently, and a member missing from the
	// caller's list is not a member that agrees.
	for member, service := range in.Spend.Members {
		if seen[member] {
			continue
		}
		alarm.Reported = append(alarm.Reported, WorkspaceAlarmMember{
			Member: member, Service: service, Why: AlarmUnreadable,
			Sentence: fmt.Sprintf("member %s's ledger was not read at all, so nothing was compared with the workspace's own count of %s", member, dollars(service)),
		})
	}
	sort.Slice(alarm.Reported, func(i, j int) bool { return alarm.Reported[i].Member < alarm.Reported[j].Member })
	return alarm
}

// beyond reports whether a delta is outside the bound, in either direction: a
// member that counted MORE than the service paid for is reported as well, which
// is what "never silently trusted in either direction" means.
func beyond(delta, bound Micros) bool {
	d := delta
	if d < 0 {
		d = -d
	}
	return d > bound
}

// abs is the magnitude of an amount, for the report's wording.
func abs(m Micros) Micros {
	if m < 0 {
		return -m
	}
	return m
}
