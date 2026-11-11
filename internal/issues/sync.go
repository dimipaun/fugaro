package issues

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/blobx"
)

// SharedIssue reads one issue as the project's store has it (F13): the record
// every teammate reads, which is the truth about an issue once it is uploaded.
// `issue show` prints it in place of this machine's own copy, so a teammate's
// dismissal reaches the person reading the issue instead of the stale file
// their machine wrote.
//
// The record is read through the same strict decode and link check as every
// other read of the store (F28): the prefix is writable by any launcher, and a
// record that fails either is an error here rather than a record printed.
func SharedIssue(ctx context.Context, b *blobx.Bucket, number int) (Issue, error) {
	rec, _, err := readShared(ctx, b, number)
	if err != nil {
		return Issue{}, err
	}
	return rec.Issue, nil
}

// SyncResult is what one `SyncPending` did: how many issues reached the shared
// store, and one line per issue this run left where it was. The lines are for a
// caller to print; nothing here writes to a terminal or an error stream.
type SyncResult struct {
	// Uploaded counts the issues this run put in the shared store.
	Uploaded int
	// Notes are the lines about the issues that stayed local: a file of
	// another installation, an upload with no verified credential, and an
	// upload the store refused. Each one names the issue and why.
	Notes []string
}

// SyncPending uploads the issues this machine saved and has not uploaded, for
// the project `installation` names (a digest of the runs bucket URL). It is the
// one caller of Upload, so the reporter an issue is filed under is the verified
// credential's and never a local file's header (F3).
//
// A file whose `installation` is not the current one is left alone and named in
// one line (F8b): the store is one folder for the whole machine, and a machine
// that serves two projects must never file one project's summary, diagnosis,
// log excerpts and command into the other's store.
//
// A failed upload is a line rather than an error: the issue stays local and the
// next run retries it, and one store that refused must not stop a listing. The
// error is only the store read's own, before anything is uploaded.
func SyncPending(ctx context.Context, store *Store, b *blobx.Bucket, installation string, deps Deps) (SyncResult, error) {
	all, err := store.List()
	if err != nil {
		return SyncResult{}, err
	}
	var result SyncResult
	var pending []Issue
	for _, i := range all {
		if i.Uploaded {
			continue
		}
		if i.Installation != installation {
			result.Notes = append(result.Notes, fmt.Sprintf("%s belongs to another installation; left where it is", i.LocalID))
			continue
		}
		pending = append(pending, i)
	}
	if len(pending) == 0 {
		return result, nil
	}
	// One identity for the whole run, and a verified one: the cache is a
	// display convenience and never names a write (F3), so a machine that
	// cannot reach its credential uploads nothing and says so once.
	id, err := VerifiedOnly(ctx, deps)
	if err != nil {
		result.Notes = append(result.Notes, fmt.Sprintf("%d issue(s) stay local: %v", len(pending), err))
		return result, nil
	}
	for _, i := range pending {
		up, err := Upload(ctx, b, i, id)
		if err != nil {
			result.Notes = append(result.Notes, fmt.Sprintf("%s stays local: %v", i.LocalID, err))
			continue
		}
		if err := store.MarkUploaded(i.LocalID, up.Number, ""); err != nil {
			result.Notes = append(result.Notes, fmt.Sprintf("%s was uploaded as issue %d but its file could not be marked: %v", i.LocalID, up.Number, err))
			continue
		}
		result.Uploaded++
	}
	return result, nil
}
