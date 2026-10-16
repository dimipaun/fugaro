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

// ArtifactPrefix is where a run's artifacts live under its own prefix:
// runs/<slug>/<run>/artifacts/<name>. One spelling, so the runner that
// uploads and the CLI that fetches cannot disagree about it (design
// artifact-runs.md §3).
const ArtifactPrefix = "artifacts/"

// ArtifactFile is one file an artifact run delivered: the record's own
// account of it, written so a reader can verify what it downloads (design
// artifact-runs.md §3, §8).
//
// Name is relative to artifacts/ and is the sanitiser's own output
// (internal/runner's sanitiseName): a plain relative path, no `..` element,
// no NUL or newline, no leading dot, at most 200 bytes. It is never a path
// out of the prefix, and the object's key leaf is exactly it.
//
// Bytes and SHA256 are what the upload wrote — the bytes actually in the
// bucket, hashed in the same pass that wrote them (blobx.PutFile) and
// checked against the staged copy's hash, not the hash of a file that was
// read earlier and could have changed since.
//
// ContentType is recorded, never trusted (§8.1): it comes from the name's
// extension or a sniff of the first bytes, and it is what a reader sees,
// not what the run decided the file is.
type ArtifactFile struct {
	Name        string       `json:"name"`
	Bytes       int64        `json:"bytes"`
	SHA256      string       `json:"sha256"`
	ContentType string       `json:"content_type"`
	Probe       *ProbeResult `json:"probe,omitempty"`
}

// ProbeResult is what the probe step said about one artifact (design §3,
// plan Task 5). Warning is "" when the probe passed.
type ProbeResult struct {
	Ran      bool   `json:"ran"`
	ExitCode int    `json:"exit_code"`
	Warning  string `json:"warning,omitempty"`
}

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
