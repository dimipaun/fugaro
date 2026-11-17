// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package coordinator

import (
	"context"
	"errors"
	"fmt"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// This file is D3 of docs/forge-authorship-images/implementation-plan.md: the
// settle of one change set — the step that flips a set's pull requests ready
// together once every member has shipped — and D4: the set's advisory merge
// order, which the report of a settle states for the reader who sees the whole
// set at once and every member's pull request states for the reader who sees
// one of them (D615).
//
// A change set is one piece of work spanning several repositories: a run per
// repository, launched together, each opening its own pull request (D613), and
// each recording what it found about itself in its own run prefix (D2). A
// member's run never flips its OWN pull request ready (D614): it records what
// it earned as the verdict on its member record and leaves the pull request a
// draft, because a set's pull requests are meant to be read together — a set
// whose first pull request is merged while its second is still being written is
// exactly the split the mechanism exists to prevent. This step lifts the hold,
// and it lives in ee/ because it is Subscription Edition behaviour over a core
// mechanism (D616).
//
// Three properties are the part rather than niceties around it:
//
//   - It is ALL OR NOTHING, and it is idempotent. Nothing is written until
//     every member has been read and found shippable, so a set with one member
//     still working is left exactly as it was; and because a member whose pull
//     request is already ready counts as ready, a settle that flipped two of
//     three and then met a failure is FINISHED by the next settle rather than
//     undone. Every member has a row in the report whether or not anything was
//     written, so the report of a settle that changed nothing is the answer to
//     "why is this set not ready".
//   - It RE-READS what it acts on (F15). A member's records are a run's word
//     about itself, and the state they name may have moved on since: this step
//     reads the pull request's live head and live draft flag from the host, and
//     refuses a member whose live head is not the commit its verdict was
//     recorded on. A settle that trusted the record for the head too would flip
//     a pull request whose last commit nothing tested.
//   - It is a COORDINATION AID, not a security control (T5). Every fact it
//     reads is written by a run of the repository it is about — the runs of one
//     repository share their prefixes, so a later run can rewrite an earlier
//     one's records — and nothing here authorizes anything. What a settle can
//     reach is bounded by the credential it acts with (D641's settle-only
//     token: the members' repositories, pull requests write alone), so a forged
//     `ship` can make a set's pull requests ready and nothing else.
//
// Nothing here reads the runs bucket or opens a provider: the reads and the two
// writes are injected, so the decision is testable without a cloud, and the CLI
// command (ee/coordinator/cli) is where the credentials and the environment
// live — the same split P2's Project and P5's Sweeper keep.

// MemberReads is what a settle reads about one member run, all of it under that
// run's own prefix in the runs bucket.
type MemberReads struct {
	// Task is the member run's task, whose change set the step checks the set
	// it is settling against (D642): membership is fixed at the set's launch,
	// and every member's task carries the same list.
	Task *task.Spec
	// Member is the run's changeset.json — the set it recorded itself in, the
	// pull request it opened, the commit its verdict is about, whether a
	// clean-tree test passed there, and the verdict it ended on (D2). Nil when
	// the run recorded none: a run that ended before it had a pull request to
	// record, or one whose write failed.
	//
	// The commit and its test are read from HERE and not from Run, because the
	// run that wrote this record is not always the run the set was launched
	// from: a follow-up to a member's pull request pushes to the member's
	// branch, keeps its own commit under its own run id, and answers on the
	// MEMBER's record (D2). A step that compared the pull request against the
	// launch run's commit would refuse such a member forever, and one already
	// settled would be pushed back into draft with nothing able to settle it
	// again (a follow-up holds the member's pull request the way its own run
	// did).
	Member *runstore.ChangeSetMember
	// Run is the run's result.json, read for the pull request the member
	// opened: its number is what the live read is made with.
	Run *runstore.Record
}

// SettleProvider is what a settle needs of one member repository's git
// provider: the pull request read live, the draft flip, and the call that
// applies what a ready pull request carries. It is deliberately narrower than
// gitprov.Provider — a settle step that could push a branch, open a pull
// request or post a comment would hold reach its token does not have and its
// job does not need — and gitprov.Provider satisfies it, which is what the
// command passes.
type SettleProvider interface {
	// PullRequest reads pull request number as the host has it NOW: the state,
	// the draft flag and the head. This is the read F15 is about.
	PullRequest(ctx context.Context, number int) (gitprov.PRInfo, error)
	// UpdatePR moves the pull request out of draft (PRUpdate.Draft false) and
	// changes nothing else: no title, no body, and no field of the status
	// section the run wrote.
	UpdatePR(ctx context.Context, number int, u gitprov.PRUpdate) (gitprov.PR, error)
	// ApplyReady applies what a ready pull request carries. It is called with
	// no reviewer and no label — a settle holds no member repository's
	// configuration to know them by — which the provider reads as a
	// confirmation that the pull request is open, and which changes nothing on
	// the host.
	ApplyReady(ctx context.Context, number int, reviewers, labels []string) error
}

// SettleMemberResult is one member's row of a settle report.
type SettleMemberResult struct {
	Repo  string `json:"repo"`
	RunID string `json:"run_id"`
	// Flipped is whether THIS settle made the member's pull request ready. A
	// member that was already ready is not flipped and is ready all the same.
	Flipped bool `json:"flipped,omitempty"`
	// Why is why the member is not ready, "" when it is. A sentence of this
	// package, and for a read or a write that failed, that sentence followed by
	// the failure's own text — which the reader that returned it has already
	// redacted (Settler.Member).
	Why string `json:"why,omitempty"`
	// Note is something about a member that IS ready and that its row would
	// otherwise not say: the pull request went ready, and something a ready
	// pull request carries could not be confirmed afterwards. It never makes
	// the set unsettled, the way the runner's own note about the same call does
	// not fail a run (internal/runner/prflow.go, applyReady).
	Note string `json:"note,omitempty"`
}

// SettleReport is what one settle of one change set did.
type SettleReport struct {
	SetID string `json:"set_id"`
	// MergeOrder is the set's advisory merge order (D4, D615): every member's
	// repository, in the order the set lists them, which is the order the
	// launch named them — contracts and shared libraries first, then the
	// consumers that need them. It is the order every member's pull request
	// states in its own body (gitprov.ChangeSetOrder), stated here for the
	// reader who sees the whole set at once: the report is the coordinator's
	// view of a set, and a person deciding what to merge reads the order off it
	// rather than off one member's body.
	//
	// Nothing is derived to state it — the member list IS the order
	// (task.ChangeSet.Members is in launch order, and every member lists the
	// same set, D642) — and nothing acts on it: this step makes a set's pull
	// requests ready together and never orders, blocks or reorders a merge
	// (D615). One entry per member, in Members' own order.
	MergeOrder []string `json:"merge_order"`
	// Settled is whether every member's pull request is ready now. A settled
	// report means the set's pull requests are all open, all out of draft, and
	// all at a commit their own tests passed on.
	Settled bool `json:"settled"`
	// Flipped is how many pull requests THIS settle made ready, 0 on a settle
	// that found the set already settled.
	Flipped int `json:"flipped"`
	// Members is one row per member, in the order the set lists them.
	Members []SettleMemberResult `json:"members"`
}

// Settler settles one change set. Its whole dependency list is the reads and
// the writes it makes, injected: no credential of its own, no store, no clock
// and no advice.
type Settler struct {
	// Member reads one member run's records. Required, and any error it
	// returns must already have had the credential values removed from its text
	// (agent.Redact): this step holds no credential and cannot know what one
	// is, and its report is printed.
	Member func(ctx context.Context, repo, runID string) (MemberReads, error)
	// Open opens one member repository's provider, with the settle-only token
	// for that member's repositories installed on it. Required.
	//
	// It is opened once per member that reaches the host, and no member's host
	// is opened until every member's records say ship: a settle of a set with
	// one member still working mints nothing.
	Open func(ctx context.Context, repo string) (SettleProvider, error)
}

// settleFlip is what the write phase needs about one member the read found
// shippable: the provider to write through, the pull request's number, and
// whether the pull request is already ready.
type settleFlip struct {
	prov   SettleProvider
	number int
	ready  bool
}

// Settle reads every member of set and, only when every one of them has
// shipped, makes every member's pull request ready.
//
// A member is shippable when all of these hold, and the first that does not is
// the row's Why:
//
//   - its task lists this set, member for member (D642: the settle acts only
//     when every member lists the same set, so a member of another set — or of
//     a set launched with a different membership — is reported rather than
//     settled from a list this step would have to guess at);
//   - its own record names this set, this run and this repository;
//   - its recorded verdict is runstore.VerdictShip, the word a member run
//     writes when nothing must change before its pull request is merged (D2) —
//     which a later attempt at the same pull request clears, so a member whose
//     follow-up failed does not settle on a `ship` nobody stands by;
//   - it has a pull request, the host says that pull request is open, and its
//     live head is the commit the member's verdict was recorded on (F15);
//   - that commit has a passing test record on a clean tree, as the run that
//     recorded the verdict read it.
//
// The reads are two passes and the writes come after both. Every member's
// records are read first, and a set one of them holds back is one no host is
// asked about at all; then every remaining member's pull request is read live,
// and nothing is written until every one of them has answered. So a set with a
// member still working costs one bucket read per member and no credential at
// all — which is what a caller settling every set it knows wants.
//
// The write is then one pull request at a time, and a failure part way leaves
// the ones already flipped ready: they are ready, which is where the set is
// going, and the next settle finishes the rest.
//
// An error is returned only for what makes the question unanswerable — no set,
// or a set with no members — and never for a member: a member's failure is a
// row, so one unreadable repository does not hide the state of the others.
func (s *Settler) Settle(ctx context.Context, set *task.ChangeSet) (SettleReport, error) {
	if set == nil {
		return SettleReport{}, errors.New("coordinator: settling needs a change set")
	}
	if len(set.Members) == 0 {
		return SettleReport{}, fmt.Errorf("coordinator: change set %s lists no member", set.ID)
	}
	if s.Member == nil || s.Open == nil {
		panic("coordinator: Settler needs a Member reader and an Open")
	}
	report := SettleReport{SetID: set.ID, MergeOrder: mergeOrder(set), Settled: true, Members: make([]SettleMemberResult, len(set.Members))}
	reads := make([]MemberReads, len(set.Members))
	held := false
	for i, member := range set.Members {
		report.Members[i] = SettleMemberResult{Repo: member.Repo, RunID: member.RunID}
		r, err := s.Member(ctx, member.Repo, member.RunID)
		switch {
		case err != nil:
			report.Members[i].Why = "its records could not be read: " + err.Error()
		default:
			reads[i] = r
			report.Members[i].Why = heldByRecords(set, member, r)
		}
		if report.Members[i].Why != "" {
			held = true
		}
	}
	if held {
		report.Settled = false
		return report, nil
	}
	// todo[i] is what member i needs to be flipped. The live reads happen for
	// every member before any of them is written to: the set goes ready
	// together or not at all, and a pull request a member's own tests no longer
	// cover is exactly the member this pass is for (F15).
	todo := make([]*settleFlip, len(set.Members))
	for i, member := range set.Members {
		flip, err := s.live(ctx, member, reads[i])
		if err != nil {
			report.Members[i].Why = err.Error()
			report.Settled = false
			continue
		}
		todo[i] = flip
	}
	if !report.Settled {
		return report, nil
	}
	for i, flip := range todo {
		if flip.ready {
			continue
		}
		note, err := flipPR(ctx, flip)
		if err != nil {
			report.Members[i].Why = "its pull request could not be made ready: " + err.Error()
			report.Settled = false
			continue
		}
		report.Members[i].Flipped, report.Members[i].Note = true, note
		report.Flipped++
	}
	return report, nil
}

// live opens one member's repository and reads its pull request as the host has
// it now, returning what the write phase needs. The error is the row's Why, and
// it is nil for a member the settle may flip.
func (s *Settler) live(ctx context.Context, member task.ChangeSetMember, reads MemberReads) (*settleFlip, error) {
	number := reads.Run.PR.Number
	prov, err := s.Open(ctx, member.Repo)
	if err != nil {
		return nil, fmt.Errorf("its repository could not be opened: %w", err)
	}
	info, err := prov.PullRequest(ctx, number)
	if err != nil {
		return nil, fmt.Errorf("its pull request could not be read: %w", err)
	}
	if why := heldByPR(reads, info); why != "" {
		return nil, errors.New(why)
	}
	return &settleFlip{prov: prov, number: number, ready: !info.Draft}, nil
}

// heldByRecords is why the records of one member hold it back, "" when they do
// not. It is the half of the decision that needs nothing from the host: the set
// every member lists (D642) and the member's own word about itself (D2).
func heldByRecords(set *task.ChangeSet, member task.ChangeSetMember, reads MemberReads) string {
	switch {
	case reads.Task == nil:
		return "its task could not be read"
	case reads.Task.ChangeSet == nil:
		return "its task lists no change set"
	case !listsSameSet(set, reads.Task.ChangeSet):
		return "its task lists another change set"
	}
	switch {
	case reads.Member == nil:
		return "it has not recorded its pull request in the set"
	case reads.Member.SetID != set.ID || reads.Member.Repo != member.Repo || reads.Member.RunID != member.RunID:
		return "its record names another member of the set"
	case reads.Member.Verdict != runstore.VerdictShip:
		return "it has not shipped"
	case reads.Member.HeadSHA == "":
		return "its verdict records no commit"
	}
	switch {
	case reads.Run == nil:
		return "its run record could not be read"
	case reads.Run.PR == nil || reads.Run.PR.Number <= 0:
		return "it has no pull request"
	}
	return ""
}

// heldByPR is why the LIVE state of one member's pull request holds it back, ""
// when it does not (F15). The state and the head are what the host answers now;
// the commit they are compared against, and the test that passed there, are the
// ones the record's verdict was written with — a record is a run's word about a
// commit, and a settle that took the head from somewhere else would flip a pull
// request whose last commit nothing tested. The head is compared as a commit (a
// provider may answer with an abbreviated one).
func heldByPR(reads MemberReads, live gitprov.PRInfo) string {
	switch {
	case live.State != gitprov.PROpen:
		if live.State == "" {
			return "its pull request is not open"
		}
		return "its pull request is " + string(live.State)
	case !gitprov.SameCommit(live.HeadSHA, reads.Member.HeadSHA):
		return "its pull request has moved on from the commit its tests ran on"
	case !reads.Member.Verified:
		return "its tests did not pass on the commit its pull request is at"
	}
	return ""
}

// flipPR makes one member's pull request ready: UpdatePR with the draft flag
// false, then the call that applies what a ready pull request carries. The
// second is a confirmation here — this step asks for no reviewer and no label —
// and its failure is NOT the flip's: the pull request is ready either way, and a
// ready pull request is what the caller asked for, so it is returned as a note
// (the rule the runner's own note about the same call keeps).
func flipPR(ctx context.Context, flip *settleFlip) (note string, err error) {
	ready := false
	if _, err := flip.prov.UpdatePR(ctx, flip.number, gitprov.PRUpdate{Draft: &ready}); err != nil {
		return "", err
	}
	if err := flip.prov.ApplyReady(ctx, flip.number, nil, nil); err != nil {
		return "the pull request is ready, but what a ready pull request carries could not be confirmed: " + err.Error(), nil
	}
	return "", nil
}

// mergeOrder is the set's advisory merge order as a report states it (D4): each
// member's repository, in the order the set lists them. It is read from the set
// the caller settled and never from a member's own records, because the order
// is the launch's: membership is fixed when a set is launched (D642), so the
// list the settle acts on is the list the launch wrote.
func mergeOrder(set *task.ChangeSet) []string {
	order := make([]string, 0, len(set.Members))
	for _, m := range set.Members {
		order = append(order, m.Repo)
	}
	return order
}

// listsSameSet reports whether the set a member's task carries is the set being
// settled: the same id and the same members, in the order the launcher wrote
// them into every member's task (D642). A member whose task lists another set,
// or the same id with a different membership, is not settled from: the set a
// settle acts on is the one every member agrees about, and a list this step
// would have to pick between is a list it must not pick from.
func listsSameSet(set, from *task.ChangeSet) bool {
	if from == nil || from.ID != set.ID || len(from.Members) != len(set.Members) {
		return false
	}
	for i, m := range set.Members {
		if from.Members[i] != m {
			return false
		}
	}
	return true
}
