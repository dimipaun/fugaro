package runstore

import "github.com/dimipaun/fugaro/internal/task"

// InputFile is one attached input as result.json records it: the same shape
// the task spec carries (task.InputFile), aliased rather than copied so the
// record's type and the schema's object cannot drift from the task's. It is
// the task spec's list, echoed after the inputs were downloaded and verified
// (design artifact-runs.md §4 step 6), so a reader sees exactly what the run
// was given.
type InputFile = task.InputFile

// InputsDir is the object-name segment a run's attached inputs live under,
// and the checkout directory the runner downloads them into.
const InputsDir = "inputs"

// InputKey is the object name of one attached input of the run stored under
// prefix (runstore.Store.Prefix()): the launcher writes it there and the
// runner reads it back from there, so the two spell the leaf once.
func InputKey(prefix, name string) string { return prefix + InputsDir + "/" + name }

// OutcomeArtifacts is the outcome of a run whose deliverable is in the
// bucket: a mode: artifact run that rendered, uploaded and (when the recipe
// asks for one, a later release) probed its outputs. It opens no pull
// request, pushes no branch and is not ready for review (design
// artifact-runs.md §3).
const OutcomeArtifacts Outcome = "artifacts"

// RenderRecord is what a mode: artifact run's render step did, recorded
// into result.json (design §3, D8).
//
// CommandSHA256 is the hash of the string the runner RAN — the
// commands.render text exactly as the runner read it from the repository's
// fugaro.yaml at the run's ref (the runner's in-memory workflow config,
// never a file re-read later) — which is what makes it checkable against
// the repository at the recorded ref: anyone can hash the same text.
// Command is that same string with the redactor applied, so the record
// never holds a secret value. **Whenever the redactor fires the two are
// deliberately not a hash pair**: CommandSHA256 stays the unredacted
// string's hash either way. Recording the hash of the redacted text
// instead would attest only to the record's own consistency, which is
// exactly the property D8 does not want.
//
// Tree is the tree the render ran in. It is always "working" today: the
// checkout the plan stage and the render share, uncommitted work included,
// which is what the design's D8 decides for the render alone (plan P2).
//
// StartedAt is RFC 3339 in UTC, the same shape every other time in a run
// record uses.
type RenderRecord struct {
	Command       string  `json:"command"`
	CommandSHA256 string  `json:"command_sha256"`
	Tree          string  `json:"tree"`
	ExitCode      int     `json:"exit_code"`
	TimedOut      bool    `json:"timed_out,omitempty"`
	DurationS     float64 `json:"duration_s"`
	StartedAt     string  `json:"started_at"`
}
