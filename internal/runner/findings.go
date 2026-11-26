// The open-findings artifact the runner writes at finalize, beside the run's
// record (BF1, U10).
//
// It is what the long-run triage and the escalation ladder read a run's
// findings from, and it is the only reading of them they take: a file inside
// the checkout is the run's own writing, and a run that could describe away its
// own findings would be judged on its account of itself. The shape is the
// reader's, not this writer's (internal/cli's triageFindingsDoc): a version, the
// run the file belongs to, and one severity, path and key per open finding.
//
// The findings are the deciding review round's — the round the run ended on, the
// same one the record's tolerated, accepted and deferred blocks are read from —
// with the words the review wrote for them and the key the record's own triage
// entries carry (findingKey). A round a later fix resolved is not the round the
// run ends on, so what is here is what was still open when the run stopped.

package runner

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// FindingsName is the object under the run's own prefix that the open findings
// are written to, and the name every reader looks for (internal/cli's
// triageFindingsObject).
const FindingsName = "findings.json"

// findingsVersion is the one version of the artifact this release writes. A
// reader that was taught another version refuses the file rather than guessing
// at it, so a change to the shape below is a change to this number.
const findingsVersion = 1

// findingsArtifact is the artifact as it is stored. Nothing here is a summary
// and nothing is a sentence: the reader wants a severity, the file the finding
// is in, and the key that identifies it across rounds.
type findingsArtifact struct {
	Version int `json:"version"`
	// Run is which run wrote the file, and it is this run's own id: a reader
	// refuses a file that does not name the run it was read for, so a file
	// copied, shared or left over from another run is not this run's artifact.
	Run      string          `json:"run"`
	Findings []findingsEntry `json:"findings"`
}

// findingsEntry is one open finding in that artifact.
type findingsEntry struct {
	Severity string `json:"severity"`
	Path     string `json:"path"`
	Key      string `json:"key"`
}

// WriteFindings writes one run's open-findings artifact under its own prefix.
//
// It is written once, by the run that owns the prefix, and it is written whole
// whether or not there is a finding to report: an absent file is a run nobody
// could read and an empty list is a run that ended with none, and the two are
// different facts to the rule that reads this.
func WriteFindings(ctx context.Context, store *runstore.Store, runID string, findings []Finding) error {
	data, err := encodeFindings(runID, findings)
	if err != nil {
		return err
	}
	return store.PutFile(ctx, FindingsName, data, "application/json")
}

// encodeFindings is the artifact's bytes. The list is empty rather than null
// when there is nothing to report, so a reader may range over it.
func encodeFindings(runID string, findings []Finding) ([]byte, error) {
	doc := findingsArtifact{
		Version: findingsVersion, Run: runID,
		Findings: make([]findingsEntry, 0, len(findings)),
	}
	for _, f := range findings {
		doc.Findings = append(doc.Findings, findingsEntry{
			Severity: f.Severity, Path: f.File, Key: findingKey(f),
		})
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("encoding the open findings: %w", err)
	}
	return append(data, '\n'), nil
}

// writeFindings writes this run's artifact, best effort: the record is the
// run's own account and this file is a reading of it, so a write that fails
// costs the readers a reading they take as absent — which is the fail-closed
// answer, never a run ended over an artifact.
func (r *run) writeFindings(ctx context.Context) {
	if r.d.Store == nil {
		return
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordWriteTimeout)
	defer cancel()
	if err := WriteFindings(wctx, r.d.Store, r.rec.RunID, r.lastSenior.Findings); err != nil {
		r.d.Log.Warn("writing the run's open findings failed", "err", err)
	}
}
