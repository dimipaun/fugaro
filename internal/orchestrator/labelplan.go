package orchestrator

import (
	"strings"

	"github.com/dimipaun/fugaro/internal/task"
)

// The label plan: what the pull requests of the daemon's own runs should carry,
// derived from the node alone (T5, docs/forge-labels).
//
// Nothing here reads or writes a host. The desired set and the difference
// against what one host reports are the rules R2 to R5 and R8 in one place, so
// they are read and pinned without a forge behind them; the write is
// labelsync.go's.

// The labels Fugaro owns: the two status names and the spec namespace
// (D2437). A label outside both is a person's, and this syncer neither writes
// one nor removes one (F23).
const (
	labelRunning = "fugaro:running"
	labelFailed  = "fugaro:failed"

	labelNamespaceSpec = "spec:"
)

// The label table (D2437): the colour and the description each label is
// created with. The credentials service applies the same table itself when it
// creates a label — its route carries a name and no label text, so a stolen
// daemon identity cannot write either (F26) — and these are what this side
// hands the adapter it holds, so the two sides agree on what a label looks
// like.
const (
	labelRunningColour      = "1D76DB"
	labelRunningDescription = "A Fugaro job is working on this pull request"
	labelFailedColour       = "D73A4A"
	labelFailedDescription  = "The last Fugaro job on this pull request failed"
	labelSpecColour         = "6E7781"
	// labelSpecDescription is the spec labels' text before the plan's name,
	// which is what tells one spec's labels from another's. The two together
	// are well inside GitHub's hundred characters (F20).
	labelSpecDescription = "Pull request for spec "
)

// statusLabels is the two names a removal may write and never a third (F23),
// in the order the table lists them.
var statusLabels = []string{labelRunning, labelFailed}

// labelItem is one pull request the syncer is responsible for: the repository
// the item is on, the pull request the run behind it opened, that run's id, and
// what the item's phase and spec want on it.
//
// The run id travels beside the number because every write sends it: the
// credentials service refuses a write whose pull request's head branch is not
// exactly `fugaro/<run id>` (F17, O3), which is what keeps a run that named
// somebody else's pull request from having it relabelled. It is the run that
// opened the pull request — the one the item recorded — and never the cycle
// that writes.
type labelItem struct {
	repo   string
	number int
	runID  string
	phase  Phase
	spec   string
}

// phaseToLabels is the status label an item's phase wants on the pull request
// of the run behind it (R4): running while that run is in flight, failed when
// it ended failed, and neither for any other phase.
//
// The five phases outside those two carry neither because none of them reads
// as a job in flight: queued has no run at all, review is set only by a patch
// and an item sits in it with no job running, and blocked, done and cancelled
// are endings. A pull request whose run ended well therefore carries no status
// label, which is the spec's own answer: success is already signalled by the
// draft becoming ready.
func phaseToLabels(p Phase) []string {
	switch p {
	case PhaseRunning:
		return []string{labelRunning}
	case PhaseFailed:
		return []string{labelFailed}
	}
	return nil
}

// specLabel is R3 for a spec name as the node stores it: the label built from
// the name and whether one can be built at all.
//
// The shape is checked first and the fold is second, never the other way
// round: the pattern's class is ASCII, so a name carrying a character outside
// it is refused rather than folded into one that passes. The Kelvin sign is
// the case that matters — it lower-cases to an ASCII "k" — so a name folded
// before it was checked would be read as another name than the one written
// (task.ASCIILower, gitprov.OwnedLabel).
//
// A name that does not pass is skipped with one line in the log and no label
// (F7): the spec name is text a session or a person wrote into a patch, and
// the one thing it must never become is a label name Fugaro did not choose.
func specLabel(name string) (string, bool) {
	if !specInRE.MatchString(name) {
		return "", false
	}
	return labelNamespaceSpec + task.ASCIILower(name), true
}

// desiredLabels is everything one item's pull request should carry: the status
// label its phase wants and the spec label of the plan it came from (R2). An
// item with no spec, or with a name no label can be built from, wants its
// status label alone.
func desiredLabels(it labelItem) []string {
	out := phaseToLabels(it.phase)
	if name, ok := specLabel(it.spec); ok {
		out = append(out, name)
	}
	return out
}

// labelDiff is the difference between the labels a pull request wants and the
// ones it carries: the wanted names it does not have, and the status names it
// has and does not want.
//
// Removal is bounded to the two status names and never to a namespace: a spec
// label a person added to the same pull request is not Fugaro's to take away,
// and a difference taken over the whole of both namespaces would remove it
// (F23). Names are compared the way the host compares them.
func labelDiff(want, have []string) (add, remove []string) {
	for _, name := range want {
		if !carriesLabel(have, name) {
			add = append(add, name)
		}
	}
	for _, name := range statusLabels {
		if carriesLabel(have, name) && !carriesLabel(want, name) {
			remove = append(remove, name)
		}
	}
	return add, remove
}

// statusesOn is the status labels have carries, in the table's order: what a
// merged or closed pull request is cleared of, and nothing else (R4, R8).
func statusesOn(have []string) []string {
	_, remove := labelDiff(nil, have)
	return remove
}

// carriesLabel reports whether list holds name, compared the way the host
// compares label names: case-insensitively (F23).
func carriesLabel(list []string, name string) bool {
	for _, l := range list {
		if labelEqual(l, name) {
			return true
		}
	}
	return false
}

// labelEqual is two label names compared the way the host compares them.
//
// The fold is written out rather than taken from strings.EqualFold, because
// that one is Unicode's: it maps characters that only look like ASCII onto it
// — the Kelvin sign onto "k", the long s onto "s" — so a label a person made,
// which the host never matches against one of ours, could be read here as ours
// and removed. gitprov.OwnedLabel folds the same way for the same reason, and
// the length test first is what keeps the byte walk below in range.
func labelEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if foldASCII(a[i]) != foldASCII(b[i]) {
			return false
		}
	}
	return true
}

// foldASCII is one byte lower-cased, over A–Z and nothing else.
func foldASCII(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

// labelText is the colour and description a name of the syncer's own is
// created with (D2437). It answers nothing for a name that is not one of them,
// which cannot happen: the only names this syncer writes are the two status
// ones and a spec label (TestPhaseToLabels pins it).
func labelText(name string) (colour, description string) {
	if spec, ok := strings.CutPrefix(name, labelNamespaceSpec); ok {
		return labelSpecColour, labelSpecDescription + spec
	}
	switch name {
	case labelRunning:
		return labelRunningColour, labelRunningDescription
	case labelFailed:
		return labelFailedColour, labelFailedDescription
	}
	return "", ""
}

// isSpecLabel reports whether name is one of the syncer's own spec labels.
// The names it is asked about are its own spelling — desiredLabels built them
// from the constant above — so the prefix is compared as written and not
// folded.
func isSpecLabel(name string) bool { return strings.HasPrefix(name, labelNamespaceSpec) }
