// The long-run triage rule: what the daemon decides about a run that has
// outlived its threshold, as pure code (O14a, BF2; D1244-D1247, G12).
//
// A run can outlive its usefulness without failing. The rule reads three
// things — the run's own record, the pull request's state as the provider
// answers it, and the open findings the runner finalized with — and answers
// one of four outcomes: escalate to a person, recommend only, merge as-is, or
// restart as smaller work. It decides and does nothing else: no clock, no
// store, no provider call and no model. The daemon's tick gathers the three
// readings and acts on the answer (O14b), which is what makes the same run
// triaged twice the same decision, and what lets the whole merge gate be
// tested without a cloud, a repository or a waiting period.
//
// Two properties carry the weight. The four outcomes are tried in one fixed
// order — escalate, then recommend-only, then merge-as-is, then restart — so a
// run two of the readings cover is decided by rule rather than by the order a
// tick happens to check. And an input the rule cannot read is never evidence
// against the run: it takes recommend-only, which merges, cancels and restarts
// nothing, and names the input it could not read. The merge is the one outcome
// that lands work without a person, so it is taken only on positive evidence
// and only for a run that is over.
//
// The rule never opens a file, and it never invents a CI answer: the caller
// reads both (gitprov.Provider.Checks, the runner's own finalize artifact),
// which is why the findings input carries the state of the read that produced
// it rather than an empty list.

package orchestrator

import (
	"slices"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/orchestrator/sensitivity"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// TriageKind is which of the rule's outcomes was reached.
type TriageKind string

const (
	// NotDue is the zero TriageKind: the run has not reached the caller's
	// threshold, so the rule read nothing and decided nothing. It is not one
	// of the four outcomes, and a caller that treats it as one would act on a
	// run nobody judged.
	NotDue TriageKind = ""
	// Escalate cancels the run and raises a decision for a person: a finding
	// read as critical, or a high one in a security path. It is tried first
	// because it acts on a finding the rule did read.
	Escalate TriageKind = "escalate"
	// RecommendOnly acts on nothing — it merges nothing, cancels nothing and
	// restarts nothing — and names the input it could not read, or the gate
	// that refused.
	RecommendOnly TriageKind = "recommend-only"
	// MergeAsIs lands the work without a person, and only on the whole of
	// D1245's evidence.
	MergeAsIs TriageKind = "merge-as-is"
	// Restart cancels the run and files smaller work: it produced nothing
	// usable, or it is not moving, or a finding is looping.
	Restart TriageKind = "restart"
)

// triageOutcomeOrder is the fixed order the four outcomes are tried in: the
// first that matches is the one taken (D1244). It is the order the two
// readings that act are kept behind the fail-closed one by, and it is data
// rather than a chain of ifs so that the order itself is what the tests pin.
var triageOutcomeOrder = []TriageKind{Escalate, RecommendOnly, MergeAsIs, Restart}

// TriageReason is why the rule reached the outcome it did. It is a closed code
// and never a sentence: the audit line carries it verbatim and the print shows
// it, so it holds no path, no finding text and no model (D1247, O3).
type TriageReason string

const (
	// ReasonCriticalFinding is a finding the rule read as critical.
	ReasonCriticalFinding TriageReason = "critical_finding"
	// ReasonSecurityHighFinding is a high finding whose own file the path
	// classifier does not call Routine.
	ReasonSecurityHighFinding TriageReason = "security_high_finding"
	// ReasonFindingsAbsent is a findings file that is not there at all —
	// which is every run until BF1 lands (U10).
	ReasonFindingsAbsent TriageReason = "findings_absent"
	// ReasonFindingsUnattributable is findings the rule cannot attribute to
	// the runner's own finalize artifact for this run: a file inside the
	// checkout is the run's own writing and never evidence about it.
	ReasonFindingsUnattributable TriageReason = "findings_unattributable"
	// ReasonFindingsUnreadable is a findings input whose read state is not one
	// this release knows, which fails closed the way an absent file does.
	ReasonFindingsUnreadable TriageReason = "findings_unreadable"
	// ReasonUnknownSeverity is a severity outside Severities, which the rule
	// will not read as low.
	ReasonUnknownSeverity TriageReason = "unknown_severity"
	// ReasonRecordUnreadable is no record, or one whose status is outside
	// runstore.Statuses — a record nothing here can place as over or in
	// flight.
	ReasonRecordUnreadable TriageReason = "record_unreadable"
	// ReasonPathsMissing is a record with no changed-path set: the run
	// recorded nothing it changed.
	ReasonPathsMissing TriageReason = "paths_missing"
	// ReasonPathsTruncated is a changed-path set short of the whole diff, and
	// the class is taken over the whole diff.
	ReasonPathsTruncated TriageReason = "paths_truncated"
	// ReasonCIUnknown is a CI read that failed, or answered neither green nor
	// red: a check that has not finished is this answer.
	ReasonCIUnknown TriageReason = "ci_unknown"
	// ReasonMergeRefused is every input read and the merge gate refused: the
	// work sits until a person reads it rather than being restarted on
	// nothing.
	ReasonMergeRefused TriageReason = "merge_refused"
	// ReasonRunOver is D1245's first requirement: the record's status is
	// terminal.
	ReasonRunOver TriageReason = "run_over"
	// ReasonPullRequestReady is the pull request being open, ready for review
	// and not a draft, at the head the run pushed.
	ReasonPullRequestReady TriageReason = "pull_request_ready"
	// ReasonCommitsPushed is a pushed head and a non-empty changed-path set.
	ReasonCommitsPushed TriageReason = "commits_pushed"
	// ReasonCIGreen is a green CI at the head the run pushed.
	ReasonCIGreen TriageReason = "ci_green"
	// ReasonPathsRoutine is every changed path inside the routine allowlist.
	ReasonPathsRoutine TriageReason = "paths_routine"
	// ReasonFindingsClear is no critical finding and none high in a security
	// path.
	ReasonFindingsClear TriageReason = "findings_clear"
	// ReasonNoCommits is a run that pushed nothing, or recorded no path it
	// changed.
	ReasonNoCommits TriageReason = "no_commits"
	// ReasonNoProgress is the stage, the head and the pushed head unchanged
	// since the tick before, on a run still in flight.
	ReasonNoProgress TriageReason = "no_progress"
	// ReasonLoopingFinding is an open finding the run's own rounds placed
	// twice.
	ReasonLoopingFinding TriageReason = "looping_finding"
)

// TriageReasons is the whole vocabulary, grouped by the outcome it explains.
// It is closed: a caller that stores or prints a reason asks KnownTriageReason
// first, so a code nothing can place is never written.
var TriageReasons = []TriageReason{
	ReasonCriticalFinding, ReasonSecurityHighFinding,
	ReasonFindingsAbsent, ReasonFindingsUnattributable, ReasonFindingsUnreadable,
	ReasonUnknownSeverity, ReasonRecordUnreadable, ReasonPathsMissing, ReasonPathsTruncated,
	ReasonCIUnknown, ReasonMergeRefused,
	ReasonRunOver, ReasonPullRequestReady, ReasonCommitsPushed, ReasonCIGreen,
	ReasonPathsRoutine, ReasonFindingsClear,
	ReasonNoCommits, ReasonNoProgress, ReasonLoopingFinding,
}

// KnownTriageReason reports whether r is one of TriageReasons. The zero
// TriageReason — a reason nobody recorded — is not.
func KnownTriageReason(r TriageReason) bool { return slices.Contains(TriageReasons, r) }

// UnreadableInput names the input the rule could not read, which is what a
// print says where a merge would otherwise have been (D1247).
type UnreadableInput string

const (
	// InputRecord is the run's own record.
	InputRecord UnreadableInput = "record"
	// InputFindings is the runner's finalize artifact: the open findings, and
	// the severity of each of them.
	InputFindings UnreadableInput = "findings"
	// InputPaths is the changed-path set the record carries.
	InputPaths UnreadableInput = "changed paths"
	// InputCI is the pull request's CI state, as the provider answered it.
	InputCI UnreadableInput = "ci"
)

// Severity is how serious an open finding is, in the rule's own vocabulary.
// The findings file's own words are mapped onto it by the reader that takes
// them out of the run's storage (O14b), so a word outside this set is a word
// the rule refuses rather than one it reads as low.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
)

// Severities is the whole vocabulary, worst first.
var Severities = []Severity{SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow}

// KnownSeverity reports whether s is one of Severities. The zero Severity —
// a finding the reader could not score — is not.
func KnownSeverity(s Severity) bool { return slices.Contains(Severities, s) }

// FindingsRead is how the read of the run's open findings went. It is part of
// the input rather than something the rule infers from an empty list: "there
// are no findings" and "nobody could read the file" are different facts, and
// the second one is never a merge (D1247, U10).
type FindingsRead string

const (
	// FindingsReadable is the run's own finalize artifact, read.
	FindingsReadable FindingsRead = "readable"
	// FindingsAbsent is no artifact for this run: a run that finalized
	// before BF1, or one whose artifact was never written.
	FindingsAbsent FindingsRead = "absent"
	// FindingsUnattributable is a file the reader found but cannot call the
	// runner's own finalize artifact for this run — a file inside the
	// checkout is the run's own writing, and the run can write it.
	FindingsUnattributable FindingsRead = "unattributable"
)

// Finding is one open finding the rule reads: how serious it is, the file it
// is in, and the key that identifies it across rounds. Nothing else — the
// rule reads no summary and no severity word the finding uses about itself.
type Finding struct {
	Severity Severity
	// Path is the file the finding is in, and the only thing that decides
	// whether a high finding is a person's: the classifier is asked about it
	// and never about a word in the finding.
	Path string
	// Key is the finding's identity across rounds (runstore.TriageEntry.Key),
	// which is what a loop is read by.
	Key string
}

// Findings is the run's open findings and the state of the read that produced
// them.
type Findings struct {
	Read FindingsRead
	List []Finding
}

// Progress is an earlier reading of a run that is still in flight: its stage,
// its head and its pushed head, as the tick before this one saw them.
//
// The record carries where a run is and never where it was, so the
// no-progress signal needs this second reading; a caller that has none — a
// first triage — passes none, and one reading is no evidence of a stall.
type Progress struct {
	Stage      string
	Head       string
	PushedHead string
}

// TriageInput is everything the rule reads, and everything it is given: the
// age thresholds belong to the caller (the escalation ladder's per-mode
// table), so the rule knows no hours of its own and takes no clock.
type TriageInput struct {
	// Record is the run's own record. The job's service account writes it, so
	// the paths in it are the run's own account of what it changed and the
	// weakest input here: ChangedFiles.Bounded bounds how many paths there are
	// and how long they are, never whether they are the diff. Corroborating
	// them — the provider's changed files at the judged head — is a read the
	// caller makes before the merge is wired (O14b), and a rule that opened the
	// repository to make it would not be this one.
	Record *runstore.Record
	// PR is the pull request as the provider answered it: the rule reads
	// whether it is open, whether it is a draft, and which head it is at. nil
	// is no pull request, which is never a merge, and a head the provider did
	// not answer is not the run's own.
	PR *gitprov.PRInfo
	// CI is the state of the checks at the commit the run pushed, read by the
	// caller. The rule never invents one, and CIUnknown is never green.
	CI gitprov.CIState
	// Findings is the run's open findings, with the state of the read behind
	// them.
	Findings Findings
	// Progress is what the tick before this one saw of the run, or nil when
	// there is no earlier reading.
	Progress *Progress
	// Elapsed is how long the run has been going, and Threshold the caller's
	// patience with it. The rule reads no clock of its own (D1230).
	Elapsed   time.Duration
	Threshold time.Duration
}

// TriageOutcome is what the rule decided and everything a print or an audit
// line needs to say why: which outcome, the closed codes behind it, the input
// it could not read, and whether the outcome is taken without a person.
type TriageOutcome struct {
	Kind TriageKind
	// Reasons are the codes behind the outcome: the failing gate's for the
	// two that act, and the evidence itself for a merge.
	Reasons []TriageReason
	// Unreadable names the input the rule could not read, and is set only on
	// RecommendOnly.
	Unreadable UnreadableInput
	// WithoutAPerson is true for the two outcomes taken without asking:
	// MergeAsIs lands work and Restart cancels the run. Escalate cancels and
	// asks, and RecommendOnly acts on nothing.
	WithoutAPerson bool
}

// Due reports whether the rule read the run at all. A run under the threshold
// takes no outcome, which is a fact of its own and not a fifth outcome: a
// caller must not act on a run the rule never judged.
func (o TriageOutcome) Due() bool { return o.Kind != NotDue }

// Triage is the rule.
func Triage(in TriageInput) TriageOutcome {
	if !in.due() {
		return TriageOutcome{}
	}
	for _, kind := range triageOutcomeOrder {
		if out, ok := triageOutcome(kind, in); ok {
			return out
		}
	}
	// Every input read and the merge gate refused: a run whose work is not
	// something code may land waits for a person, and its own record does not
	// ask for a restart (D2030, D2037).
	return recommendOnly(ReasonMergeRefused, "")
}

// due reports whether the run has reached the caller's threshold. A threshold
// the caller did not supply is not one this rule may read as "now": the rule's
// own default has to be that it decides nothing.
func (in TriageInput) due() bool {
	return in.Threshold > 0 && in.Elapsed >= in.Threshold
}

func triageOutcome(kind TriageKind, in TriageInput) (TriageOutcome, bool) {
	switch kind {
	case Escalate:
		return triageEscalate(in)
	case RecommendOnly:
		return triageRecommendOnly(in)
	case MergeAsIs:
		return triageMergeAsIs(in)
	case Restart:
		return triageRestart(in)
	}
	return TriageOutcome{}, false
}

// triageEscalate answers a finding read as critical, or a high one whose own
// file the path classifier does not call Routine (D1245, D1247). It refuses
// when the findings are not readable, which is what keeps a file nobody could
// read from escalating a run: recommend-only is that case's, and it asks a
// person for a different reason.
func triageEscalate(in TriageInput) (TriageOutcome, bool) {
	if _, unreadable := unreadableFindings(in.Findings); unreadable {
		return TriageOutcome{}, false
	}
	for _, f := range in.Findings.List {
		switch f.Severity {
		case SeverityCritical:
			return TriageOutcome{Kind: Escalate, Reasons: []TriageReason{ReasonCriticalFinding}}, true
		case SeverityHigh:
			if !routinePath(f.Path) {
				return TriageOutcome{Kind: Escalate, Reasons: []TriageReason{ReasonSecurityHighFinding}}, true
			}
		}
	}
	return TriageOutcome{}, false
}

// triageRecommendOnly is the fail-closed outcome: it is tried before the two
// outcomes that act on the run, so an input nobody read never merges, cancels
// or restarts anything (D1245, D1247).
func triageRecommendOnly(in TriageInput) (TriageOutcome, bool) {
	if reason, unreadable := unreadableFindings(in.Findings); unreadable {
		return recommendOnly(reason, InputFindings), true
	}
	if !readableRecord(in.Record) {
		return recommendOnly(ReasonRecordUnreadable, InputRecord), true
	}
	paths := in.Record.ChangedFiles.Bounded()
	if paths == nil {
		return recommendOnly(ReasonPathsMissing, InputPaths), true
	}
	if paths.Truncated {
		return recommendOnly(ReasonPathsTruncated, InputPaths), true
	}
	if !knownCI(in.CI) {
		return recommendOnly(ReasonCIUnknown, InputCI), true
	}
	return TriageOutcome{}, false
}

// triageMergeAsIs is the one outcome that lands work without a person, and it
// takes every part of D1245's evidence: a run that is over, a pull request
// still open, ready for review and at the head the run pushed, commits, green
// CI, the whole changed-path set inside the routine allowlist, and no critical
// finding or high one in a security path. A finished run that reached here has
// no progress left to make, so its stall, if it ever had one, is not a
// restart's (D1244).
//
// The path set it classifies is the record's own account of the diff, and
// D1245's premise that a diff cannot be described away is not enforced here:
// the classifier is asked about a set the run wrote, and Bounded bounds that
// set's size rather than its truth, so a record that left a sensitive path out
// is a merge this gate takes. The corroborating read is the tick's before this
// gate is wired to a merge (O14b), and it is reported as such rather than
// assumed away.
//
// It re-checks the inputs' readability rather than trusting the order it is
// tried in: the order is what the rule is, and a merge gate that fails closed
// on its own is what makes a change to that order a decision instead of an
// accident.
func triageMergeAsIs(in TriageInput) (TriageOutcome, bool) {
	if _, unreadable := unreadableFindings(in.Findings); unreadable {
		return TriageOutcome{}, false
	}
	if !readableRecord(in.Record) {
		return TriageOutcome{}, false
	}
	if in.CI != gitprov.CIGreen {
		return TriageOutcome{}, false
	}
	if in.PR == nil || in.PR.Draft || in.PR.State != gitprov.PROpen {
		return TriageOutcome{}, false
	}
	rec := in.Record
	if !rec.Status.Terminal() {
		return TriageOutcome{}, false
	}
	paths := rec.ChangedFiles.Bounded()
	if paths == nil || paths.Truncated || len(paths.Paths) == 0 || rec.PushedHead == "" {
		return TriageOutcome{}, false
	}
	// The head the gate judges is the head it lands (D2041): the CI answer and
	// the findings both describe the commit the run pushed, so a pull request
	// that moved past it is judged on evidence about another commit, and a
	// merge here would land a diff nothing in this input describes.
	if !gitprov.SameCommit(in.PR.HeadSHA, rec.PushedHead) {
		return TriageOutcome{}, false
	}
	if sensitivity.Classify(paths.Paths) != sensitivity.Routine {
		return TriageOutcome{}, false
	}
	if blocksALanding(in.Findings.List) {
		return TriageOutcome{}, false
	}
	return TriageOutcome{
		Kind: MergeAsIs,
		Reasons: []TriageReason{
			ReasonRunOver, ReasonPullRequestReady, ReasonCommitsPushed,
			ReasonCIGreen, ReasonPathsRoutine, ReasonFindingsClear,
		},
		WithoutAPerson: true,
	}, true
}

// triageRestart answers a long run that produced nothing usable, one that is
// not moving while still in flight, or a finding looping across rounds. It
// cancels the run and files smaller work; it never re-launches the same spec,
// because a spec that made no progress is what a retry would spend itself on
// (D1246).
//
// The no-progress signal is read only while the run is in flight: a finished
// run has no progress left to make, and it is the merge's to judge or the
// gate's to refuse. A finished run's own record is still read for the two
// other signals, because "it pushed nothing" and "the same finding came back
// twice" are facts about a run that has stopped as much as about one that is
// going.
func triageRestart(in TriageInput) (TriageOutcome, bool) {
	if !readableRecord(in.Record) {
		return TriageOutcome{}, false
	}
	rec := in.Record
	paths := rec.ChangedFiles.Bounded()
	if rec.PushedHead == "" || (paths != nil && len(paths.Paths) == 0) {
		return restart(ReasonNoCommits), true
	}
	if !rec.Status.Terminal() && in.stalled() {
		return restart(ReasonNoProgress), true
	}
	if loopingFinding(rec, in.Findings.List) {
		return restart(ReasonLoopingFinding), true
	}
	return TriageOutcome{}, false
}

// stalled reports whether the run is at the stage, the head and the pushed
// head the tick before this one already saw. A caller with no earlier reading
// passes none, and nothing is read from it: one reading is no evidence that a
// run is stuck.
func (in TriageInput) stalled() bool {
	if in.Progress == nil || in.Record == nil {
		return false
	}
	return in.Progress.Stage == in.Record.Stage &&
		in.Progress.Head == in.Record.HeadSHA &&
		in.Progress.PushedHead == in.Record.PushedHead
}

// loopingFinding reports whether an open finding is one the run's own triage
// rounds already placed twice (D1246, D2036). The key is the identity, as it
// is for every comparison across rounds, and only an open finding counts: one
// that came back once and was fixed is not a loop.
func loopingFinding(rec *runstore.Record, findings []Finding) bool {
	if rec == nil || rec.Triage == nil {
		return false
	}
	placed := map[string]int{}
	for _, round := range rec.Triage.Rounds {
		keys := map[string]bool{}
		for _, e := range round.Entries {
			if e.Key != "" {
				keys[e.Key] = true
			}
		}
		for k := range keys {
			placed[k]++
		}
	}
	for _, f := range findings {
		if f.Key != "" && placed[f.Key] > 1 {
			return true
		}
	}
	return false
}

// unreadableFindings names why the findings input is not one the rule can
// read, or reports that it is. The read state comes first — an absent file has
// no severity to judge — and then every finding's severity, because a
// severity the rule does not know is not a finding it may clear or escalate.
func unreadableFindings(f Findings) (TriageReason, bool) {
	switch f.Read {
	case FindingsReadable:
	case FindingsAbsent:
		return ReasonFindingsAbsent, true
	case FindingsUnattributable:
		return ReasonFindingsUnattributable, true
	default:
		return ReasonFindingsUnreadable, true
	}
	for _, item := range f.List {
		if !KnownSeverity(item.Severity) {
			return ReasonUnknownSeverity, true
		}
	}
	return "", false
}

// blocksALanding reports whether an open finding is one a person has to read
// before the work lands: a critical finding, or a high one in a path that is
// not Routine. It is the same reading escalate makes, and MergeAsIs asks it
// again so that the merge gate holds whatever order the outcomes are tried in.
func blocksALanding(findings []Finding) bool {
	for _, f := range findings {
		switch f.Severity {
		case SeverityCritical:
			return true
		case SeverityHigh:
			if !routinePath(f.Path) {
				return true
			}
		case SeverityMedium, SeverityLow:
		default:
			return true
		}
	}
	return false
}

// routinePath asks the classifier about one path, which is what a finding's
// own file is: D1245's "security path" is the classifier's answer and never a
// word the finding uses about itself.
func routinePath(p string) bool {
	return sensitivity.Classify([]string{p}) == sensitivity.Routine
}

// readableRecord reports whether the record is one the rule can place. A
// record it was not given, and one whose status is outside runstore.Statuses,
// are both records nothing here can read as over or in flight — and a status
// this code does not know is not one Status.Terminal may answer for.
func readableRecord(rec *runstore.Record) bool {
	return rec != nil && slices.Contains(runstore.Statuses, rec.Status)
}

// knownCI reports whether the CI input is an answer the rule can read. A state
// outside the vocabulary is not one: the rule reads green and red, and every
// other value — a failed read, a check that has not finished, and a value this
// release does not know — is the absent answer.
func knownCI(s gitprov.CIState) bool { return s == gitprov.CIGreen || s == gitprov.CIRed }

// recommendOnly is the outcome that acts on nothing, with the code that says
// why and the input the caller could not read.
func recommendOnly(reason TriageReason, unreadable UnreadableInput) TriageOutcome {
	return TriageOutcome{Kind: RecommendOnly, Reasons: []TriageReason{reason}, Unreadable: unreadable}
}

// restart is the outcome that cancels the run and files smaller work.
func restart(reason TriageReason) TriageOutcome {
	return TriageOutcome{Kind: Restart, Reasons: []TriageReason{reason}, WithoutAPerson: true}
}

// triageTransition is the audit transition every triage is appended under. It
// is a name the log's own rule accepts, which is what EntryID needs.
const triageTransition = "triage"

// TriageAuditID is the audit id of one run's triage, `<run>:triage` (D1247).
// It is derived, so a tick that triages the same run again appends one line
// rather than one per tick, and the line that survives is what the daemon
// decided.
func TriageAuditID(run string) string { return EntryID(run, triageTransition) }
