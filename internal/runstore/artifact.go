package runstore

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
