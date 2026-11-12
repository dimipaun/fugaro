// The daemon's triage seams over a real host, a run's own storage and O3's
// audit log (O14b).
//
// The rule (O14a) is pure code that decides and does nothing. This file is the
// other half: it gathers the rule's three readings from where they live, and
// carries out the two things an outcome can ask for that are not a state write
// — landing the pull request it judged, and stopping the run it judged — plus
// the one line of audit every outcome leaves.
//
// Two properties matter more than the plumbing. The findings are read from the
// run's own storage, the runner's finalize artifact under the run's prefix, and
// never from a path in the daemon's working directory: the run writes its own
// checkout, so a findings file found there would be the run's account of itself
// rather than evidence about it. And every reading that cannot be made is left
// as the rule's own unreadable input rather than turned into a reason to act:
// the merge is the one outcome that lands work without a person, and a triage
// that cannot see does not merge, and does not cancel the run it cannot judge.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// triageFindingsObject is the runner's finalize artifact the rule's third
// input comes from (BF1, U10), under the run's own prefix in the runs bucket.
//
// BF1 has not landed, so this name and the shape below are this slice's own:
// they follow the plan's description of that artifact rather than a file that
// exists, and the reader fails closed on anything that is not exactly it. The
// name is deliberately not the record's: a run's record is the runner's account
// of how the run went, and this file is the open findings it ended with.
const triageFindingsObject = "findings.json"

// triageFindingsVersion is the one version of that artifact this reader knows.
// Another version is a file written by a runner this daemon has not been
// taught, which is unattributable rather than guessable.
const triageFindingsVersion = 1

// triageFindingsDoc is the artifact as the runner writes it. Nothing here is a
// summary and nothing is a sentence: the rule reads a severity, the file the
// finding is in and the key that identifies it across rounds, and the rest of
// what a review said stays in the run's own record.
type triageFindingsDoc struct {
	Version int `json:"version"`
	// Run is which run wrote the file. A document that does not name the run
	// it was read for is not that run's artifact, however well formed, so a
	// file copied, shared or left over from another run is unattributable.
	Run      string                 `json:"run"`
	Findings []triageFindingsRecord `json:"findings"`
}

// triageFindingsRecord is one open finding in that artifact.
type triageFindingsRecord struct {
	Severity string `json:"severity"`
	Path     string `json:"path"`
	Key      string `json:"key"`
}

// orchestratorTriage is the daemon's three triage seams (TriageReader,
// TriageActor, TriageLog), over the runs bucket, the project's database and the
// host the project's provider settings name.
//
// It is one value rather than three because the three share exactly what a
// cycle has to hand it once: which bucket the runs are in, which database the
// log is appended to, which orchestrator id the log belongs to, and how a
// provider is opened. The opened map is the one piece of state: a token cache
// lives as long as the provider holding it, so a seam that opened a provider
// per call would mint a credential per call and never reuse one.
type orchestratorTriage struct {
	bucket *blob.Bucket
	db     *rtdb.Client
	id     string
	// env is the daemon's own environment, read for the one per-repository
	// answer this seam needs: which host a repository is on. Nothing else here
	// comes from it.
	env *cloudEnv
	// kind is the kind open is given when the environment cannot tell which
	// host a repository is on, and it is what a test sets to open a fake; open
	// may ignore it (the file-backed test provider does).
	kind string
	open gitprov.Opener
	warn io.Writer
	// opened is one provider per repository, and warned is one warning per
	// repository about opening it. A cycle runs on one goroutine, so neither
	// needs a lock.
	opened map[string]gitprov.Provider
	warned map[string]bool
}

// orchestratorTriageSeams builds the daemon's triage seams, which are what the
// daemon's own tick builder is handed — one call, so nothing else in that file
// changes for this step.
//
// The host is opened lazily, per repository, at the first run a triage needs it
// for, and never here: a project whose provider settings are incomplete is a
// daemon that still ticks, folds and starts runs, and whose runs are triaged
// without a host (which is the rule's recommend-only) rather than a daemon that
// refuses to start.
func orchestratorTriageSeams(d *orchestratorDaemon) orchestrator.TriageSeams {
	_, open, err := OpenProvider(os.Environ(), func(w string) {
		fmt.Fprintf(d.warn, "orchestrator %s: %s\n", safetext.Field(d.id), safetext.OneLine(w))
	})
	if err != nil {
		// A daemon passes no --provider, so the one error this can return — a
		// provider kind the flag named and the flag does not accept — cannot
		// happen here. A tick with no seams triages nothing at all, which is
		// the direction a host that cannot be built has to fail in.
		fmt.Fprintf(d.warn, "orchestrator %s: the triage has no host, so no run is triaged: %s\n",
			safetext.Field(d.id), safetext.OneLine(err.Error()))
		return orchestrator.TriageSeams{}
	}
	return (&orchestratorTriage{
		bucket: d.env.bucket.Bucket, db: d.db, id: d.id, env: d.env, open: open, warn: d.warn,
		opened: map[string]gitprov.Provider{}, warned: map[string]bool{},
	}).seams()
}

// seams is the three seams as the tick carries them.
func (t *orchestratorTriage) seams() orchestrator.TriageSeams {
	return orchestrator.TriageSeams{Read: t, Act: t, Log: t}
}

// TriageInput gathers the rule's input for one run (TriageReader): the record
// the runner wrote, the pull request and its checks as the host answers them,
// and the run's own findings artifact.
//
// The run's storage is found by the run id alone, so this needs no repository
// and no provider to locate: a run whose task is not in the bucket at all is a
// record nobody can read, which the rule turns into recommend-only rather than
// an error that ends the cycle.
func (t *orchestratorTriage) TriageInput(ctx context.Context, target orchestrator.TriageTarget) (orchestrator.TriageInput, error) {
	store, err := t.storeOf(ctx, target.RunID)
	switch {
	case errors.Is(err, runstore.ErrNotFound):
		return orchestrator.TriageInput{}, nil
	case err != nil:
		return orchestrator.TriageInput{}, err
	}
	rec, err := store.ReadRecord(ctx)
	switch {
	case errors.Is(err, runstore.ErrNotFound):
		rec = nil
	case err != nil:
		return orchestrator.TriageInput{}, err
	}
	prov, err := t.provider(ctx, repoOf(rec, target))
	if err != nil {
		// No provider is a host this cycle did not reach, and the rule reads
		// that as the merge gate's refusal rather than as evidence about the
		// run: nothing is merged, and nothing is cancelled on it either.
		return GatherTriageInput(ctx, store, nil, rec)
	}
	return GatherTriageInput(ctx, store, prov, rec)
}

// Merge lands the pull request the rule judged (TriageActor), at the head the
// rule read and at no other.
//
// The provider's own call carries the expected head (E8), so a branch that
// moved between the read and this call is refused by the host rather than
// merged at whatever head it now has, and no admin or bypass flag is sent. The
// daemon marks nothing ready here: the rule only ever says MergeAsIs about a
// pull request it read as open and not a draft, so a draft pull request is
// never this call's.
func (t *orchestratorTriage) Merge(ctx context.Context, target orchestrator.TriageTarget, pr gitprov.PRInfo) error {
	prov, err := t.provider(ctx, repoOf(nil, target))
	if err != nil {
		return err
	}
	return prov.Merge(ctx, pr.Number, pr.HeadSHA)
}

// Cancel stops the run the rule judged (TriageActor), through the run's own
// cancel path: the marker `fugaro cancel` writes into the run's storage, which
// the runner's own watch reads before it finalizes a draft pull request.
//
// The marker and nothing else is deliberate. `fugaro cancel` writes the marker,
// waits out the run's finalize reserve and the runner's own reaction time, and
// only then stops the execution — minutes, on a command a person runs once. A
// daemon's cycle is a tick that must come back, and stopping the execution
// first would kill the container before the runner finalizes the draft pull
// request that says what the run did, which is the one thing this path exists
// to avoid.
func (t *orchestratorTriage) Cancel(ctx context.Context, target orchestrator.TriageTarget) error {
	store, err := t.storeOf(ctx, target.RunID)
	if err != nil {
		return err
	}
	return store.RequestCancel(ctx)
}

// Triaged reports whether the run's triage line is already in O3's log
// (TriageLog). It is the whole of the fence: a run whose line is there has been
// judged and acted on, and the next cycle leaves it alone however long it
// lives.
func (t *orchestratorTriage) Triaged(ctx context.Context, run string) (bool, error) {
	path, err := orchestrator.PathAudit(t.id)
	if err != nil {
		return false, err
	}
	var e orchestrator.Entry
	found, err := t.db.Get(ctx, path+"/"+orchestrator.TriageAuditID(run), &e)
	if err != nil {
		return false, err
	}
	return found, nil
}

// Append writes one line of triage (TriageLog) under the id its subject and its
// transition derive, so a retry is a write to the same key rather than a second
// line.
func (t *orchestratorTriage) Append(ctx context.Context, e orchestrator.Entry) error {
	log, err := orchestrator.NewAudit(t.db, t.id, t.db.ServerNow)
	if err != nil {
		return err
	}
	_, err = log.Append(ctx, e)
	return err
}

// provider is the host for one repository, opened at most once per daemon: the
// credential a real provider mints is cached inside it, and a provider opened
// per cycle would mint one per cycle and hold none.
func (t *orchestratorTriage) provider(ctx context.Context, repo string) (gitprov.Provider, error) {
	if repo == "" {
		return nil, errors.New("no repository to open a provider for")
	}
	if t.open == nil {
		return nil, errors.New("no code host is configured for the triage")
	}
	if p, ok := t.opened[repo]; ok {
		return p, nil
	}
	kind := t.kind
	if t.env != nil {
		if k := t.env.repoProvider(repo, nil); k != "" {
			kind = k
		}
	}
	p, _, err := t.open(ctx, kind, repo)
	if err != nil {
		t.warnOnce(repo, err)
		return nil, err
	}
	if p == nil {
		return nil, fmt.Errorf("no provider for %s", repo)
	}
	t.opened[repo] = p
	return p, nil
}

// warnOnce says why a repository's provider could not be opened, once. The
// secrets the opener hands back are dropped rather than kept for redaction:
// this daemon never prints a request or a response body, so there is no text
// for a credential to be redacted out of.
func (t *orchestratorTriage) warnOnce(repo string, err error) {
	if t.warn == nil || t.warned[repo] {
		return
	}
	t.warned[repo] = true
	fmt.Fprintf(t.warn, "orchestrator %s: no provider for %s, so its runs are triaged without a host: %s\n",
		safetext.Field(t.id), safetext.Field(repo), safetext.OneLine(err.Error()))
}

// storeOf is the run's own storage, found by the run id alone.
func (t *orchestratorTriage) storeOf(ctx context.Context, runID string) (*runstore.Store, error) {
	slug, run, err := runstore.Locate(ctx, t.bucket, runID)
	if err != nil {
		return nil, err
	}
	return runstore.Open(t.bucket, slug, run), nil
}

// repoOf is the repository a run was for: the record's own, which is what the
// host and the storage agree on, and the item's only when there is no record.
func repoOf(rec *runstore.Record, target orchestrator.TriageTarget) string {
	if rec != nil && rec.Repo != "" {
		return rec.Repo
	}
	return target.Item.Repo
}

// GatherTriageInput is the one reading of the rule's input: the run's record as
// it is stored, the pull request and the checks at its head as the host answers
// them, and the run's own findings artifact. It has no side effect at all,
// which is what lets `fugaro runs watch` print exactly what the daemon decides
// (O14c) from the same function rather than from a second implementation of it.
//
// Every reading that cannot be made is left as the rule's own unreadable input:
// a host that will not answer leaves no pull request and no CI answer, and a
// findings file that is absent, too large, malformed, of another version or
// attributed to another run is unattributable. A read that fails is never an
// empty answer — "there are no findings" and "nobody read the file" are
// different facts, and only one of them may merge.
//
// prov may be nil, for a caller with no host to ask: that is the same fail
// closed answer as a read that failed.
func GatherTriageInput(ctx context.Context, store *runstore.Store, prov gitprov.Provider, rec *runstore.Record) (orchestrator.TriageInput, error) {
	in := orchestrator.TriageInput{Record: rec}
	in.Findings = triageFindingsRead(ctx, store, runOf(rec))
	if prov != nil && rec != nil && rec.PR != nil && rec.PR.Number > 0 {
		pr, err := prov.PullRequest(ctx, rec.PR.Number)
		if err == nil {
			in.PR = &pr
			// The checks are read at the head the pull request is at, and
			// never at the head the record remembers: the CI answer and the
			// findings both have to describe the commit the gate is judging.
			if runs, err := prov.Checks(ctx, pr.Number, pr.HeadSHA); err == nil {
				in.CI = gitprov.ChecksState(runs)
			}
		}
	}
	return in, nil
}

// triageFindingsRead reads the run's own findings artifact, from the run's own
// storage and from nowhere else, and says which of the three facts it found:
// the artifact, no artifact, or a file it will not attribute to this run.
func triageFindingsRead(ctx context.Context, store *runstore.Store, runID string) orchestrator.Findings {
	raw, err := store.ReadFile(ctx, triageFindingsObject)
	if errors.Is(err, runstore.ErrNotFound) {
		return orchestrator.Findings{Read: orchestrator.FindingsAbsent}
	}
	if err != nil {
		return orchestrator.Findings{Read: orchestrator.FindingsUnattributable}
	}
	var doc triageFindingsDoc
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return orchestrator.Findings{Read: orchestrator.FindingsUnattributable}
	}
	if doc.Version != triageFindingsVersion || doc.Run != runID {
		return orchestrator.Findings{Read: orchestrator.FindingsUnattributable}
	}
	list := make([]orchestrator.Finding, 0, len(doc.Findings))
	for _, f := range doc.Findings {
		list = append(list, orchestrator.Finding{
			Severity: orchestrator.Severity(f.Severity), Path: f.Path, Key: f.Key,
		})
	}
	return orchestrator.Findings{Read: orchestrator.FindingsReadable, List: list}
}

// runOf is the run a record names, which is what the findings artifact has to be
// attributed to. A record with no id is a file this reader cannot attribute
// anything to.
func runOf(rec *runstore.Record) string {
	if rec == nil {
		return ""
	}
	return rec.RunID
}
