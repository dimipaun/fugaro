package plan

import "github.com/bmatcuk/doublestar/v4"

// Footprint is a task's expected set of paths — what the planner thinks this
// task will touch, before anything has run.
//
// It is **advisory** and it is **a prediction**, and D28's answer is that both
// of those are properties of the document rather than of the reader: every
// footprint this package emits carries Prediction true, so no renderer has to
// remember to label it, and the one thing a footprint must never be read as is
// a measurement. The measured set is a different field of a different document
// — the run's own record of the files it changed (D27), written by the run
// after the fact. A prediction and a measurement that can be confused with each
// other is the mistake this type exists to prevent.
//
// Paths are **glob-shaped**, matched with this repository's own glob semantics —
// `github.com/bmatcuk/doublestar/v4`, the library `internal/imagecheck`
// (`doublestar.Match` over the git tree), `internal/cache/key.go`
// (`doublestar.Glob`), `internal/config/validate.go` and
// `internal/verify/reports.go` (`doublestar.ValidatePattern`) already use. A
// second pattern dialect here would be a second answer to "does this task touch
// that file", which is the one question the footprint is asked.
type Footprint struct {
	// Prediction is always true. It is a field rather than a constant in a
	// renderer because it is part of the document's contract with the
	// console: the label travels with the data it labels.
	Prediction bool `json:"prediction"`
	// Paths are the glob patterns, never null: a task with no expected paths
	// carries an empty array, which is a different statement from "the
	// planner did not say" — and the console renders those two differently.
	Paths []string `json:"paths"`
}

// NewFootprint is the only way this package builds a footprint, and it is why
// Prediction cannot be false in a document this build emits: the value is set
// here, once, rather than by each reader.
func NewFootprint(patterns []string) Footprint {
	if patterns == nil {
		patterns = []string{}
	}
	return Footprint{Prediction: true, Paths: patterns}
}

// Matches reports whether path is one of the paths the footprint predicts. The
// matching is doublestar's, the same call `internal/imagecheck` makes, and the
// error is doublestar's own: a pattern that cannot be compiled does not match
// anything, which is why Validate refuses such a document before a reader ever
// asks it a question.
//
// A path is matched as the repository spells it — a slash-separated path
// relative to the checkout's root, never an absolute one and never a filesystem
// path, which is the other reason this package takes no filesystem walk: the
// console compares two documents, and neither of them has a tree.
func (f Footprint) Matches(path string) bool {
	for _, pattern := range f.Paths {
		if ok, err := doublestar.Match(pattern, path); err == nil && ok {
			return true
		}
	}
	return false
}

// validateFootprint refuses a footprint whose patterns are not globs this
// repository's matcher understands. It is a refusal and not a warning for the
// reason `internal/config/validate.go` refuses the same way: a pattern that
// cannot be compiled silently matches nothing, and a footprint that matches
// nothing reads exactly like a task that touches nothing.
func validateFootprint(node Node) string {
	for _, pattern := range node.Footprint.Paths {
		if !doublestar.ValidatePattern(pattern) {
			return "node " + node.ID + " has a footprint path that is not a glob this repository's matcher understands: " + pattern
		}
	}
	return ""
}
