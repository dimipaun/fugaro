package issues

import (
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
)

// IssueStore is the local issues folder the recording seam works through: the
// *Store a command opens, and a spy in a test. It is an interface because what
// one verdict reads and what it writes are the promises this seam makes — a
// finding that is only this machine's writes nothing, and a run's finding is
// saved once — and a promise about a store is only a test while the store can
// be watched (acceptance 1, F26).
type IssueStore interface {
	// List reads every issue in the folder.
	List() ([]Issue, error)
	// Save writes one, under the name its number and stem give it.
	Save(Issue) error
}

// RunMeta is what the caller knows about the run a verdict came from, and the
// two stores the finding could reach. The record's header is built from the
// first; the second is here to be left alone.
type RunMeta struct {
	// Stem is the failed command's run stem (D1609). It names the record until
	// the issue is uploaded and its number from then on (D1723).
	Stem string
	// Reporter is who ran the command: the member string the credential
	// answered with. The header keeps it and the post never does (D1722).
	Reporter string
	// Created is when the diagnosis ran, from the caller's clock. It is the
	// issue's own time, never the upload's (F25).
	Created time.Time
	// Command is the failed command's argv, as the command log recorded it.
	// Record redacts it (D1732): a command's arguments are where a path, a
	// project name or a credential ends up, and the header is what the whole
	// team reads.
	Command []string
	// Logs are the short excerpts the collector cut from the failed command's
	// log. Record redacts them like the rest of the body (F5).
	Logs []string
	// FugaroVersion is the version that failed, as the reporter's own binary
	// spelled it, and OS the platform it failed on.
	FugaroVersion string
	OS            string
	// Store is the local issues folder the record is written to and looked up
	// in.
	Store IssueStore
	// Shared is the project's shared store. Record never reads it and never
	// writes it: the upload is the caller's own next step and the duplicate
	// matching happens inside it, so a verdict is recorded with no cloud call,
	// no claim and no fingerprint lookup (F26, D1725). The caller holds the
	// bucket anyway, for the sync that follows, and it is in the seam so that
	// promise is a test rather than a reading of this file.
	Shared *blobx.Bucket
}

// Saved is what recording a verdict leaves to print: the record that was
// written and the one line that names it, or — for a finding that is only this
// machine's — the fix steps and nothing else.
//
// At most one of Line and FixSteps is ever set: a `bug` and an `unclear`
// finding are saved and named, and a `configuration` or
// `fixed_in_newer_release` finding is answered here and now, with nothing
// written down (acceptance 1, F1, F6).
type Saved struct {
	// Issue is the record the verdict was saved as; the zero Issue when the
	// outcome saved nothing.
	Issue Issue
	// Line is what the terminal prints for a saved issue, empty when there is
	// nothing to file.
	Line string
	// FixSteps is what fixes the finding on this machine. It is the agent's own
	// answer, printed by the caller and written down nowhere: the record has no
	// member for it, and it is the whole answer for the two outcomes that save
	// nothing.
	FixSteps string
}

// Record is the recording seam: it turns one verdict into the one thing that
// is done with it. A finding that is this machine's alone — a wrong setting, or
// a bug a newer release already fixes — is answered with its fix steps and
// nothing is written; a bug and an unclear finding are redacted, saved as an
// issue, and named by the line that files them (acceptance 1, F6).
//
// It writes locally and only locally: no upload, no claim, no duplicate
// lookup, no GitHub. The caller's next step is the sync, and matching happens
// inside the upload (F26, D1725).
//
// A run whose finding is already saved is not saved twice. The record that is
// there is named and left as it is: a log diagnosed again would otherwise put a
// second file under the same stem, and, once the first was uploaded and
// renamed, a numberless one beside it — two issues of one run, one of them
// invisible to the number the team reads.
func Record(v Verdict, run RunMeta, secrets []string) (Saved, error) {
	if err := v.Validate(); err != nil {
		return Saved{}, err
	}
	class, saved := v.Saved()
	if !saved {
		return Saved{FixSteps: v.FixSteps}, nil
	}
	name := localPrefix + run.Stem
	if existing, ok, err := recorded(run.Store, name); err != nil {
		return Saved{}, err
	} else if ok {
		return Saved{Issue: existing, Line: savedLine(existing)}, nil
	}
	i := Issue{
		LocalID:       name,
		Run:           run.Stem,
		Reporter:      run.Reporter,
		Created:       run.Created,
		FailedCommand: Redact(RedactCommand(run.Command), secrets),
		Class:         class,
		FugaroVersion: run.FugaroVersion,
		OS:            run.OS,
		Status:        StatusPending,
		Fingerprint:   Fingerprint(v.Signature),
		Summary:       Redact(v.Summary, secrets),
		Diagnosis:     Redact(v.Diagnosis, secrets),
		Steps:         Redact(v.Steps, secrets),
		Logs:          redactAll(run.Logs, secrets),
	}
	if err := run.Store.Save(i); err != nil {
		return Saved{}, err
	}
	return Saved{Issue: i, Line: savedLine(i)}, nil
}

// recorded is the record this run's finding is already saved as, if it is. The
// lookup is the local folder's own listing, and a folder that cannot be read is
// an error rather than an empty one: a verdict that failed to find its own
// record would be saved a second time.
func recorded(store IssueStore, localID string) (Issue, bool, error) {
	all, err := store.List()
	if err != nil {
		return Issue{}, false, err
	}
	for _, i := range all {
		if i.LocalID == localID {
			return i, true, nil
		}
	}
	return Issue{}, false, nil
}

// savedLine is the one line a saved issue is named by (spec §1): the local ID
// until the issue has been uploaded and its number from then on, each with the
// argument that files it. The number is spelled `#0007` and the argument `7`,
// because the listing prints the first and the command takes the second (F23).
func savedLine(i Issue) string {
	name, ref := i.LocalID, i.LocalID
	if i.Number > 0 {
		name, ref = fmt.Sprintf("#%04d", i.Number), fmt.Sprintf("%d", i.Number)
	}
	return fmt.Sprintf("Saved issue %s. Run 'fugaro issue submit %s' to file it on GitHub.", name, ref)
}

// redactAll is the gate over the log excerpts: every piece of an issue passes
// it before it is written down (F5).
func redactAll(texts []string, secrets []string) []string {
	if texts == nil {
		return nil
	}
	out := make([]string, len(texts))
	for idx, text := range texts {
		out[idx] = Redact(text, secrets)
	}
	return out
}
