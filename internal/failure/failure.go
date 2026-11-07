// Package failure is the closed vocabulary a run record's failure_class holds
// (docs/design/failure-classes.md §3): why a run did not end succeeded, as one
// of the nineteen names below, or nothing at all.
//
// It is a package of its own rather than part of internal/runstore because the
// record carries the type: runstore imports this package, so this package may
// not import runstore, and the runner, the reader of a legacy record and the
// digest all need the type without taking the record's dependency weight.
//
// The vocabulary is written once, here. The schema's enum and the report's
// table are both read from Classes(), so a class added to one of them alone is
// a failing test rather than a consumer that prints or draws a value of its
// own, and a value this build does not know reads as Unclassified instead of
// breaking a reader.
package failure

import "slices"

// Class is one cause a run's record can name.
type Class string

// The nineteen classes, in the order the design's table lists them, which is
// also the order the report prints its rows in.
const (
	ReviewNotConverged    Class = "review_not_converged"
	ReviewUnread          Class = "review_unread"
	TestsFailed           Class = "tests_failed"
	UnverifiedFinalCommit Class = "unverified_final_commit"
	NoCommits             Class = "no_commits"
	HaltedBudget          Class = "halted_budget"
	HaltedGovernance      Class = "halted_governance"
	HaltedCredential      Class = "halted_credential"
	HaltedKillSwitch      Class = "halted_kill_switch"
	Cancelled             Class = "cancelled"
	ProviderAuthRefused   Class = "provider_auth_refused"
	TransportFailed       Class = "transport_failed"
	StageErrorUnexplained Class = "stage_error_unexplained"
	StageNoResult         Class = "stage_no_result"
	ConfigInvalid         Class = "config_invalid"
	GitProviderError      Class = "git_provider_error"
	BranchMoved           Class = "branch_moved"
	InfraError            Class = "infra_error"
	// Unclassified is what a reader answers for a record that names no class,
	// and for one whose class this build does not know: the vocabulary's own
	// answer to "there is a cause here and it is not one of mine", which is
	// also what lets the set grow without a format version moving.
	Unclassified Class = "unclassified"
)

// classes is the vocabulary itself, in the design's order. Only Classes() reads
// it, so that no caller can reorder or truncate what the schema and the report
// are built from.
var classes = []Class{
	ReviewNotConverged, ReviewUnread, TestsFailed, UnverifiedFinalCommit,
	NoCommits, HaltedBudget, HaltedGovernance, HaltedCredential,
	HaltedKillSwitch, Cancelled, ProviderAuthRefused, TransportFailed,
	StageErrorUnexplained, StageNoResult, ConfigInvalid, GitProviderError,
	BranchMoved, InfraError, Unclassified,
}

// Classes is the whole vocabulary, in the design's order. The caller gets its
// own copy, because this list is the one place the set is written.
func Classes() []Class { return slices.Clone(classes) }

// Kind is the family a class's cause belongs to: what a reader has to change
// for the class to stop happening.
type Kind string

const (
	KindQuality  Kind = "quality"
	KindBudget   Kind = "budget"
	KindInfra    Kind = "infra"
	KindExternal Kind = "external"
	KindConfig   Kind = "config"
	KindOther    Kind = "other"
)

// Properties is what a class says beyond its name, and it is what the report
// prints beside it so that a reader knows what to do without reading a
// sentence.
//
// Retryable is a claim about the cause, never a promise: it says that running
// the same task again with nothing else changed could end differently.
// NeedsOwner says that a person has to do something first -- raise a cap, read
// a checkpoint, turn a switch off -- before the class can be avoided at all.
type Properties struct {
	Kind       Kind
	Retryable  bool
	NeedsOwner bool
}

// properties is each class's answer, written once. A class with no row here
// would print as a blank or a zero rather than as a wrong figure, so the
// vocabulary's closure is a test rather than a reader's trust.
var properties = map[Class]Properties{
	ReviewNotConverged:    {Kind: KindQuality, Retryable: false, NeedsOwner: true},
	ReviewUnread:          {Kind: KindQuality, Retryable: false, NeedsOwner: true},
	TestsFailed:           {Kind: KindQuality, Retryable: false, NeedsOwner: true},
	UnverifiedFinalCommit: {Kind: KindQuality, Retryable: false, NeedsOwner: true},
	NoCommits:             {Kind: KindQuality, Retryable: false, NeedsOwner: true},
	HaltedBudget:          {Kind: KindBudget, Retryable: true, NeedsOwner: true},
	HaltedGovernance:      {Kind: KindBudget, Retryable: false, NeedsOwner: true},
	HaltedCredential:      {Kind: KindInfra, Retryable: true, NeedsOwner: true},
	HaltedKillSwitch:      {Kind: KindInfra, Retryable: true, NeedsOwner: true},
	Cancelled:             {Kind: KindOther, Retryable: true, NeedsOwner: false},
	ProviderAuthRefused:   {Kind: KindInfra, Retryable: false, NeedsOwner: true},
	TransportFailed:       {Kind: KindInfra, Retryable: true, NeedsOwner: false},
	StageErrorUnexplained: {Kind: KindInfra, Retryable: false, NeedsOwner: true},
	StageNoResult:         {Kind: KindInfra, Retryable: true, NeedsOwner: true},
	ConfigInvalid:         {Kind: KindConfig, Retryable: false, NeedsOwner: true},
	GitProviderError:      {Kind: KindExternal, Retryable: true, NeedsOwner: false},
	BranchMoved:           {Kind: KindExternal, Retryable: true, NeedsOwner: true},
	InfraError:            {Kind: KindInfra, Retryable: true, NeedsOwner: true},
	Unclassified:          {Kind: KindOther, Retryable: true, NeedsOwner: true},
}

// PropertiesOf is what c says beyond its name. ok is false for a value outside
// the vocabulary, which is the answer a record written by a newer build gets.
func PropertiesOf(c Class) (Properties, bool) {
	p, ok := properties[c]
	return p, ok
}
