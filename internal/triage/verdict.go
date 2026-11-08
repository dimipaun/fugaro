// Package triage is the post-review triage step's own vocabulary: the verdict
// a judge answers for one round of findings, the strict reader of that answer,
// and the brief it is asked from (docs/post-review-triage/).
//
// It decides nothing about readiness. The mode's threshold table stays the
// authority and the fallback (D1840): a judge may only add deferrals among the
// findings that table blocks, and the floor — a security or data_loss class,
// an impact of 5 outside Hotfix, an unscored finding and one only the owner can
// settle — is never deferred whatever a verdict says. A judge's answer is
// untrusted data, like every other model's text a run reads: it is bounded
// here, and applied inside the floor by Apply, which is the only caller that
// lets it steer anything.
package triage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Version is the verdict document's version. A reader that does not know the
// number refuses the document rather than reading it as an older one
// (CLAUDE.md: a field never changes meaning), and refusal is the safe
// direction here: a verdict nothing can read blocks every finding (D1846).
const Version = 1

// MaxLine is the longest a verdict's one-line field may be, in bytes: the
// reason, which a follow-up task's brief carries, and the group key, which a
// reader routes on without parsing prose. Bytes rather than runes because the
// limit is about what a later line can hold, and a rune-counted limit is a
// different number of bytes for every script.
const MaxLine = 200

// Word is one finding's verdict: the two words a judge may answer with, and
// nothing else. It is a type rather than a bare string so a caller cannot pass
// a word this vocabulary does not hold to something that routes on it.
type Word string

const (
	// Block is the verdict that the finding is fixed in this patch before the
	// merge: it is handed to the fixer and it holds the pull request a draft.
	Block Word = "block"
	// Defer is the verdict that the finding is filed as a follow-up task and
	// the merge goes ahead without it. A deferred finding never reaches the
	// fixer (F16, acceptance 2).
	Defer Word = "defer"
)

// Verdict is one triage round's answer: the version of the format, and one
// entry per finding of the round the judge read. The findings array is always
// written, empty included, so an absent one is a malformed answer and not an
// empty claim — a round with findings and no entries blocks every one of them
// (Apply's missing-verdict rule, D1846).
type Verdict struct {
	Version  int     `json:"version"`
	Findings []Entry `json:"findings"`
}

// Entry is one finding's verdict: which finding it is about, the word, the
// judge's one-line reason, and the group that ties related deferrals into one
// follow-up task (spec: "group related small findings into one task").
type Entry struct {
	// Index is the finding's position in the round's list, as the brief
	// numbered it (Prompt). It is the entry's key into that list, and a
	// verdict whose index and key disagree about which finding they mean
	// blocks it (D1850).
	Index int `json:"index"`
	// Key is the hash of the finding's file and summary, echoed from the
	// brief: it is what makes a verdict about a finding rather than about a
	// position in a list that a re-review may have reordered (F11, D1850).
	Key string `json:"key"`
	// Verdict is the word: block or defer.
	Verdict Word `json:"verdict"`
	// Reason is the judge's one-line why, which the record keeps and the
	// follow-up task carries (acceptance 3). One line and bounded, because a
	// later reader puts it in a table cell and a task's brief.
	Reason string `json:"reason"`
	// Group is the key that puts a deferral in the same follow-up task as
	// other deferrals, empty for a finding that is its own task (T-TR10).
	Group string `json:"group,omitempty"`
}

// Decode reads one verdict from a judge's answer, strictly: an unknown member
// of the document or of an entry, a version this reader does not know, a
// verdict word outside the two, a missing findings array, a reason that is
// empty, over MaxLine bytes or on more than one line, and an empty key or a
// negative index are all refused. Anything refused here is a call the runner
// records as failed, which blocks every finding (D1846): the refusals are the
// strict direction, so an answer this reader cannot place never becomes a
// deferral.
func Decode(data []byte) (Verdict, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var v Verdict
	if err := dec.Decode(&v); err != nil {
		return Verdict{}, fmt.Errorf("triage: %w", err)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Verdict{}, errors.New("triage: the verdict carries a second document after it")
	}
	if v.Version != Version {
		return Verdict{}, fmt.Errorf("triage: verdict version %d, want %d", v.Version, Version)
	}
	if v.Findings == nil {
		return Verdict{}, errors.New("triage: the verdict carries no findings array")
	}
	for i, e := range v.Findings {
		if e.Verdict != Block && e.Verdict != Defer {
			return Verdict{}, fmt.Errorf("triage: finding %d: verdict %q is not %q or %q", i, e.Verdict, Block, Defer)
		}
		if e.Index < 0 {
			return Verdict{}, fmt.Errorf("triage: finding %d: index %d is not a position in the round", i, e.Index)
		}
		if e.Key == "" {
			return Verdict{}, fmt.Errorf("triage: finding %d: no key, so the verdict names no finding", i)
		}
		if err := checkLine(fmt.Sprintf("finding %d: reason", i), e.Reason, true); err != nil {
			return Verdict{}, err
		}
		if err := checkLine(fmt.Sprintf("finding %d: group", i), e.Group, false); err != nil {
			return Verdict{}, err
		}
	}
	return v, nil
}

// checkLine refuses a one-line field: empty when it is required, over MaxLine
// bytes, or carrying a newline. A reason the judge wrote across two lines is
// refused rather than folded, because a reason a later reader prints in a cell
// and a task's brief is one line by construction and a fold here would hide
// the second line's instruction-as-text from the reader that bounds it.
func checkLine(field, s string, required bool) error {
	if s == "" {
		if required {
			return fmt.Errorf("triage: %s is empty: every verdict says why", field)
		}
		return nil
	}
	if len(s) > MaxLine {
		return fmt.Errorf("triage: %s is %d bytes, over the %d a verdict's line may be", field, len(s), MaxLine)
	}
	if strings.ContainsAny(s, "\r\n") {
		return fmt.Errorf("triage: %s is not one line", field)
	}
	return nil
}
