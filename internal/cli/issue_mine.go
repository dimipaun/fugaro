package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	calleraccess "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/installrec"
	"github.com/dimipaun/fugaro/internal/issues"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// mine.go is `fugaro issue mine`: the issues this machine's user reported,
// read from the shared store the project keeps (F7), with what this machine
// already knows about them when the store cannot be read (F9).
//
// It reads GitHub's issue state nowhere (D1784): what a resolved issue shows
// is the release that fixed it, which the shared record carries, and the
// version comparison against this machine's own — a `mine` that called
// GitHub would be a `mine` that needs a token, and the record is the thing
// the store already holds.
//
// It is a listing and not a read-only command: it uploads this machine's own
// pending issues through SyncPending, which is a write to the shared store
// under the verified credential's name, and it files the offline snapshot and
// the identity cache it read with. What it never does is write an issue's
// *state*: no status, no dismissal, no resolved flag, no GitHub (D1784).
//
// The rules it follows are the plan's: the retry suggestion is this
// machine's own command and never the shared record's (F8), other reporters
// are counted and never named (F15), and every string that came from the
// store goes through the terminal pass before a person sees it (F11).

// mineNow is the clock the command reads. It is a seam because the snapshot's
// as-of line, the 30-day rule and the row order all read one instant, and a
// test that pins them cannot wait for the real one.
var mineNow = time.Now

// mineCaller is the credential lookup the identity is resolved through:
// `access.Caller` in the shipped binary (aliased, because this package has a
// type of that name), a test's own function in a test, so no test reaches
// Google's userinfo endpoint.
var mineCaller = func(ctx context.Context) (string, error) { return calleraccess.Caller(ctx) }

// mineProject resolves the project this run reads the issues of: the same
// selection updateRetryBucket makes (selectNamed, so no project header and no
// note, and the same creating argument, which writes nothing — a machine with
// no config for the project answers "none" here rather than a refusal).
//
// It reads the checkout and the local config and nothing else — no credential,
// no network — which is why a coding agent's session may call it and still
// reach no shared store (F10): what a session must not do is read the bucket or
// upload, and the project's own name is a local file.
var mineProject = func(ctx context.Context) (*localcfg.Config, error) {
	co, err := checkoutProject(ctx, "")
	if err != nil {
		return nil, err
	}
	_, lc, err := selectNamed(cloudOptions{}, co, true, "")
	if err != nil {
		return nil, err
	}
	if lc == nil {
		return nil, errors.New("no project with a runs bucket is selected")
	}
	return lc, nil
}

// mineOpen opens a project's shared store. It is a seam for the reason
// updateRetryBucket is one: a hermetic test cannot reach gs://, and an open
// that fails is a line this command prints and then carries on without, never
// an error that stops a listing (F9).
var mineOpen = func(ctx context.Context, lc *localcfg.Config) (*blobx.Bucket, error) {
	return blobx.Open(ctx, lc.BucketURL())
}

// newIssueMineCmd builds `fugaro issue mine`: the issues this machine's user
// reported, whichever machine reported them.
func newIssueMineCmd() *cobra.Command {
	var asJSON, all bool
	cmd := &cobra.Command{
		Use:   "mine",
		Short: "List the issues this user reported, from the project's store",
		Long: "List the issues this machine's user reported to the project.\n\n" +
			"The rows are the issues this user created and the ones their report was\n" +
			"merged into, read from the shared store the project's runs bucket keeps:\n" +
			"the number and the summary, where the issue stands, when it was hit,\n" +
			"submitted, resolved or dismissed, the link to the GitHub issue once it\n" +
			"has one, the release that fixed it and whether this machine is on it.\n" +
			"Other people who hit the issue are counted, never named.\n\n" +
			"A resolved issue this machine has not been updated to prints the one\n" +
			"command that fixes that — `fugaro update`. For one this machine already\n" +
			"has, the row offers the command this machine's own issue file recorded,\n" +
			"and nothing is run: the row is a listing.\n\n" +
			"An issue finished more than 30 days ago is hidden; --all shows it. With\n" +
			"the shared store unreadable — no network, no project selected — the rows\n" +
			"are the last successful read this machine cached, or the local files\n" +
			"alone, and the listing says which. In a coding agent's session only those\n" +
			"two are read and nothing is uploaded.\n\n" +
			"--json prints one versioned document, whose shape\n" +
			"schemas/issue-mine.schema.json describes. GitHub is read for nothing here:\n" +
			"the release a record names is what says an issue is fixed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runIssueMine(cmd, asJSON, all) },
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	cmd.Flags().BoolVar(&all, "all", false, "show the issues finished more than 30 days ago")
	return cmd
}

// runIssueMine is the one function behind `issue mine` and `issue list
// --mine` (F12): both print the same rows from the same read.
func runIssueMine(cmd *cobra.Command, asJSON, all bool) error {
	ctx := cmd.Context()
	now := mineNow()
	store, err := openIssueStore()
	if err != nil {
		return err
	}
	agent := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin()))
	var notes []string
	if agent != "" {
		notes = append(notes, "note: this is a coding agent's session: only this machine's own issues and the last shared read are shown, and nothing is uploaded")
	}
	deps := issues.Deps{
		Lookup: mineCaller,
		// A coding agent's session asks the credential nothing (F10). What it
		// may read is the cache, which is display data either way.
		NoLookup: agent != "",
		Getenv:   os.Getenv,
		GOOS:     runtime.GOOS,
	}
	id, err := issues.ResolveIdentity(ctx, deps)
	if err != nil {
		notes = append(notes, "note: who \"me\" is could not be resolved ("+oneLine(err.Error())+"), so the project's store cannot be read; the rows are this machine's own")
	}

	var bucket *blobx.Bucket
	installation := ""
	// The project is resolved whether or not the store is read: the name it
	// gives is what this machine's own records and snapshots are filed under
	// (F8b), and an offline run needs it to find the last read it cached (F9).
	lc, projectErr := mineProject(ctx)
	if projectErr != nil {
		notes = append(notes, "note: the project's runs bucket could not be found ("+oneLine(projectErr.Error())+"), so the project's issues cannot be read; the rows are this machine's own")
	} else {
		installation = issues.Digest(lc.BucketURL())
		// The store stamps this on every record it writes, which is what keeps
		// one project's issue out of another project's store (F8b).
		store.Installation = installation
	}
	// A coding agent's session opens no bucket (F10): what it may read of the
	// project is the snapshot, which is a file, and who "me" is there is either
	// cached or nobody.
	if projectErr == nil && agent == "" && id.Digest != "" {
		b, openErr := mineOpen(ctx, lc)
		if openErr != nil {
			notes = append(notes, "note: the project's shared store could not be opened ("+oneLine(openErr.Error())+")")
		} else {
			bucket = b
		}
	}

	source := mineSourceLocal
	var shared issues.MineShared
	if bucket != nil {
		defer func() { _ = bucket.Close() }()
		sync, syncErr := issues.SyncPending(ctx, store, bucket, installation, deps)
		if syncErr != nil {
			notes = append(notes, "note: this machine's pending issues could not be uploaded: "+oneLine(syncErr.Error()))
		}
		for _, n := range sync.Notes {
			notes = append(notes, "note: "+oneLine(n))
		}
		shared, syncErr = issues.ListMine(ctx, bucket, id, now)
		if syncErr != nil {
			notes = append(notes, "note: the project's issues could not be read ("+oneLine(syncErr.Error())+")")
		} else {
			source = mineSourceShared
		}
	}
	if source != mineSourceShared {
		// Offline, or with no store at all: the last successful read stands in,
		// and the rows this machine's own files hold are read live either way
		// (F9). Which of the two a reader is looking at is a line, because an
		// old row and a current one look the same.
		snap, ok := mineSnapshot(installation)
		if ok {
			shared = mineSnapshotShared(snap)
			source = mineSourceSnapshot
			notes = append(notes, "offline: shared state as of "+snap.AsOf.UTC().Format(time.RFC3339))
		} else {
			notes = append(notes, "no snapshot of the project's issues is cached, so these are this machine's own local issues only")
		}
	}

	allLocal, err := store.List()
	if err != nil {
		return userErr("nothing was listed: %v", err)
	}
	locals := mineLocals(allLocal, installation)
	installed := mineInstalled(ctx, bucket)
	// Only a live read can have been cut short: the snapshot is one run's own
	// rows, and a listing that stands in for no read makes no claim about the
	// store's size.
	truncated := shared.Truncated && source == mineSourceShared
	rows, hidden := issues.BuildRows(issues.MineInput{
		Local:     locals,
		Shared:    shared,
		Version:   Version,
		Installed: installed,
		Now:       now,
		All:       all,
	})
	// The snapshot is this machine's copy of the last successful read, and it
	// is written from the moments those rows are the answer: a read that failed
	// leaves the one before it where it is.
	if source == mineSourceShared {
		mineWriteSnapshot(installation, rows, shared.AsOf)
	}

	out := cmd.OutOrStdout()
	if asJSON {
		for _, n := range notes {
			fmt.Fprintln(cmd.ErrOrStderr(), n)
		}
		return writeJSON(out, mineDocument{
			Format:    1,
			Source:    source,
			AsOf:      mineAsOf(shared.AsOf, now),
			Installed: installed,
			Hidden:    hidden,
			Truncated: truncated,
			Issues:    mineJSONRows(rows),
		})
	}
	for _, n := range notes {
		fmt.Fprintln(out, n)
	}
	printMineRows(out, rows, allLocal, installation, cmd.Root())
	if hidden > 0 {
		fmt.Fprintf(out, "%d finished issue(s) older than 30 days are hidden; --all shows them.\n", hidden)
	}
	if truncated {
		fmt.Fprintln(out, "the project's listing was cut short, so some issues may be missing.")
	}
	return nil
}

// The three sources a listing can have, and the string the --json document
// carries for each (F12).
const (
	mineSourceShared   = "shared"
	mineSourceSnapshot = "snapshot"
	mineSourceLocal    = "local"
)

// mineSnapshot is the last successful read this machine cached for this
// installation, and whether there is one: a listing with no project selected
// has no name to look one up under.
func mineSnapshot(installation string) (issues.Snapshot, bool) {
	if installation == "" {
		return issues.Snapshot{}, false
	}
	dir, err := localcfg.UserDir(os.Getenv, runtime.GOOS, localcfg.DirCache)
	if err != nil {
		return issues.Snapshot{}, false
	}
	snap, err := issues.ReadSnapshot(dir, installation)
	if err != nil || snap.AsOf.IsZero() {
		return issues.Snapshot{}, false
	}
	return snap, true
}

// mineSnapshotShared is the snapshot as the row model takes it: the same rows
// the read would have answered with, so the ordering, the hiding, the version
// comparison and the printing are one code path whichever source a run has.
//
// A row with no number is left out: it was this machine's own file, and the
// listing reads that file live — the file is what changed since the snapshot
// was written. The display status is turned back into the record's own terms,
// because that is what the row model derives the display from, and a
// regression is the one of them that is not a status (F2).
func mineSnapshotShared(snap issues.Snapshot) issues.MineShared {
	out := issues.MineShared{AsOf: snap.AsOf}
	for _, r := range snap.Rows {
		number, err := strconv.Atoi(r.Number)
		if err != nil || number == 0 {
			continue
		}
		row := issues.MineSharedIssue{
			Number:      number,
			Summary:     r.Summary,
			HitAt:       r.HitAt,
			SubmittedAt: r.SubmittedAt,
			ResolvedAt:  r.ResolvedAt,
			DismissedAt: r.DismissedAt,
			URL:         r.URL,
			ResolvedIn:  r.ResolvedIn,
			OthersHit:   r.OthersHit,
		}
		row.Status, row.Regression = snapshotDisplay(r.Display)
		out.Issues = append(out.Issues, row)
	}
	return out
}

// snapshotDisplay is one snapshot row's display status as the record's own
// status and the regression flag: the two values the row model derives a
// display from.
func snapshotDisplay(display string) (issues.MineStatus, bool) {
	switch issues.Display(display) {
	case issues.DisplayRegression:
		return issues.MineStatusResolved, true
	case issues.DisplayPending, issues.DisplaySubmitted, issues.DisplayResolved, issues.DisplayDismissed:
		return issues.MineStatus(display), false
	}
	// A display this build does not know is not one to read as a status: the
	// row is left as the record's `pending`, which is what an issue that is
	// only in the store is until something says otherwise.
	return issues.MineStatusPending, false
}

// mineLocals is this machine's own issue files as the row model takes them:
// the ones that belong to this project.
//
// A file that names another installation is another project's and is never
// read here (F8b). A file that names none is kept: it was saved before there
// was a field to stamp, and dropping it would hide the user's own issue
// rather than attribute it to the wrong project — which is the harm the rule
// exists for.
func mineLocals(all []issues.Issue, installation string) []issues.MineLocal {
	out := make([]issues.MineLocal, 0, len(all))
	for _, i := range all {
		if i.Installation != "" && installation != "" && i.Installation != installation {
			continue
		}
		hit := i.Created
		out = append(out, issues.MineLocal{
			Number:      i.Number,
			LocalID:     i.LocalID,
			Summary:     i.Summary,
			Status:      i.Status,
			Fingerprint: i.Fingerprint,
			HitAt:       &hit,
			SubmittedAt: i.SubmittedAt,
			ResolvedAt:  i.ResolvedAt,
			DismissedAt: i.DismissedAt,
			URL:         i.URL,
			ResolvedIn:  i.ResolvedIn,
			Regression:  i.Regression,
		})
	}
	return out
}

// mineInstalled is the release the installation record names, "" when there is
// nothing to read it from (F5, D1782): a record this machine cannot read is a
// coverage the row says was not checked, never a failure of the listing.
func mineInstalled(ctx context.Context, b *blobx.Bucket) string {
	if b == nil {
		return ""
	}
	rec, err := installrec.Read(ctx, b)
	if err != nil {
		return ""
	}
	return rec.Release
}

// mineWriteSnapshot files the rows of this run for the next one that cannot
// reach the store. It is best effort: a cache that cannot be written is an
// offline run without a snapshot, never an error a listing reports.
//
// The rows it keeps are the ones the store answered for — a row only this
// machine's files hold is read live, from the file, the next time — and the
// command a covered row may offer is left out of it, as the snapshot's own
// shape requires (F8, T-IM5).
func mineWriteSnapshot(installation string, rows []issues.MineRow, asOf time.Time) {
	if installation == "" {
		return
	}
	dir, err := localcfg.UserDir(os.Getenv, runtime.GOOS, localcfg.DirCache)
	if err != nil {
		return
	}
	snap := make([]issues.SnapshotRow, 0, len(rows))
	for _, r := range rows {
		if r.Number == 0 {
			continue
		}
		snap = append(snap, issues.SnapshotRow{
			Number:      strconv.Itoa(r.Number),
			Summary:     r.Summary,
			Display:     string(r.Display),
			HitAt:       r.HitAt,
			SubmittedAt: r.SubmittedAt,
			ResolvedAt:  r.ResolvedAt,
			DismissedAt: r.DismissedAt,
			URL:         r.URL,
			ResolvedIn:  r.ResolvedIn,
			Coverage:    string(r.Coverage),
			OthersHit:   r.OthersHit,
		})
	}
	_ = issues.WriteSnapshot(dir, installation, snap, asOf)
}

// mineDocument is the `mine --json` document: the shape
// schemas/issue-mine.schema.json describes, and the one JSON shape this
// feature gives a reader outside this binary (F12). Its member names are new
// — no record's own field is reused for a different meaning — and every
// member is versioned by `format`.
type mineDocument struct {
	Format int    `json:"format"`
	Source string `json:"source"`
	AsOf   string `json:"as_of"`
	// Installed is the release the installation record names, empty when it
	// could not be read; it is what the coverage of a resolved row was decided
	// with (D1782).
	Installed string `json:"installed"`
	// Hidden counts the finished rows the 30-day rule left out (F13), and
	// Truncated says the store's own listing was cut short.
	Hidden    int          `json:"hidden"`
	Truncated bool         `json:"truncated"`
	Issues    []mineRowDoc `json:"issues"`
}

// mineRowDoc is one row of that document. No reporter's name and no digest is
// in it (F15): the count of other people is the only thing said about them.
type mineRowDoc struct {
	Number  int    `json:"number"`
	LocalID string `json:"local_id,omitempty"`
	Summary string `json:"summary"`
	Display string `json:"display"`
	// The four times are the row's own; an absent one is a transition that has
	// not happened.
	HitAt       *time.Time `json:"hit_at,omitempty"`
	SubmittedAt *time.Time `json:"submitted_at,omitempty"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
	DismissedAt *time.Time `json:"dismissed_at,omitempty"`
	URL         string     `json:"url,omitempty"`
	ResolvedIn  string     `json:"resolved_in,omitempty"`
	Coverage    string     `json:"coverage"`
	OthersHit   int        `json:"others_hit"`
}

// mineJSONRows is the listing as the document's rows, in the order they print.
func mineJSONRows(rows []issues.MineRow) []mineRowDoc {
	out := make([]mineRowDoc, 0, len(rows))
	for _, r := range rows {
		out = append(out, mineRowDoc{
			Number:      r.Number,
			LocalID:     r.LocalID,
			Summary:     r.Summary,
			Display:     string(r.Display),
			HitAt:       utc(r.HitAt),
			SubmittedAt: utc(r.SubmittedAt),
			ResolvedAt:  utc(r.ResolvedAt),
			DismissedAt: utc(r.DismissedAt),
			URL:         issueURL(r.URL),
			ResolvedIn:  r.ResolvedIn,
			Coverage:    string(r.Coverage),
			OthersHit:   r.OthersHit,
		})
	}
	return out
}

// mineAsOf is when the listing's state was last known: the shared read's own
// time, and this run's clock for a listing with no read behind it (F9).
func mineAsOf(asOf, now time.Time) string {
	if asOf.IsZero() {
		return now.UTC().Format(time.RFC3339)
	}
	return asOf.UTC().Format(time.RFC3339)
}

// utc is a row's time as the document carries it: UTC, or nothing when the
// transition has not happened.
func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// printMineRows prints the listing for a person: one block per issue, the
// number, the display status and the summary first, then what is known about
// it. Every value that came from a store or a file goes through the terminal
// pass — a teammate's machine wrote the shared record and this machine's own
// files are writable by anything running as the user (F6, F11).
func printMineRows(out io.Writer, rows []issues.MineRow, all []issues.Issue, installation string, root *cobra.Command) {
	for _, r := range rows {
		fmt.Fprintf(out, "%s  %s  %s\n", cellOneLine(mineRef(r)), cellOneLine(string(r.Display)), cellOneLine(r.Summary))
		detail := mineDetails(r, all, installation, root)
		if len(detail) > 0 {
			fmt.Fprintf(out, "    %s\n", strings.Join(detail, "; "))
		}
	}
}

// mineRef is the name a row prints under: the project number once the store
// gave it one, this machine's own name for the issue before that (D1723).
func mineRef(r issues.MineRow) string {
	if r.Number == 0 {
		return r.LocalID
	}
	return fmt.Sprintf("#%04d", r.Number)
}

// mineDetails is the second line of one row: its times, its link, what the
// version comparison said, how many other people hit it, and — for a row this
// machine is already covered on — the command of its own issue file.
func mineDetails(r issues.MineRow, all []issues.Issue, installation string, root *cobra.Command) []string {
	var parts []string
	if r.HitAt != nil {
		parts = append(parts, "hit "+r.HitAt.UTC().Format(time.DateOnly))
	}
	parts = append(parts,
		"submitted "+mineDate(r.SubmittedAt),
		"resolved "+mineDate(r.ResolvedAt),
		"dismissed "+mineDate(r.DismissedAt))
	// The link is printed for the rows that have one to follow: an issue this
	// machine has not filed and one set aside are read from the store's own
	// line, and a URL of any other shape is dropped (F11).
	switch r.Display {
	case issues.DisplaySubmitted, issues.DisplayResolved, issues.DisplayRegression:
		if url := issueURL(r.URL); url != "" {
			parts = append(parts, url)
		}
	}
	if cover := mineCoverage(r); cover != "" {
		parts = append(parts, cover)
	}
	if n := r.OthersHit; n > 0 {
		parts = append(parts, fmt.Sprintf("also hit by %d other(s)", n))
	}
	if command := mineRetry(r, all, installation, root); command != "" {
		parts = append(parts, "retry: "+command+" (commands can change things; nothing is run for you)")
	}
	return parts
}

// mineDate is one of a row's transition times, or "-" when it has not
// happened.
func mineDate(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format(time.DateOnly)
}

// mineCoverage is what the comparison against the fixing release said, in the
// words a person reads (F5, D1782): the release that fixed the issue, the
// version this machine runs when the two differ, the one command that closes
// the gap, and — when the installation's own release could not be read — that
// the installation was not checked (O4).
func mineCoverage(r issues.MineRow) string {
	if r.Coverage == issues.CoverageNone {
		return ""
	}
	// The fixing release came out of a shared record or this machine's own
	// file, so it is another party's bytes on a person's terminal and goes
	// through the pass like every other stored value (F11); this build's own
	// version goes with it, because one line of output is one rule.
	fixes := "fixed in " + oneLine(r.ResolvedIn)
	switch r.Coverage {
	case issues.CoverageCovered:
		return fixes
	case issues.CoverageCLIOnly:
		return fixes + "; installation not checked"
	case issues.CoverageBehind:
		return fmt.Sprintf("%s; you are on %s: run `fugaro update`", fixes, oneLine(Version))
	default:
		// A comparison that cannot be made says which half is missing: a build
		// that is not a release can never be updated to one (D1736), and a
		// record whose fixing release is not a release names nothing to compare.
		if !updatecheck.Valid(Version) {
			return "dev build: cannot compare"
		}
		return fixes + ": not a release this build can compare"
	}
}

// mineRetry is the command a covered row may offer, and "" for every row that
// may not.
//
// It is this machine's own issue file's command and never the shared record's
// (F8): the shared store is writable by every launcher, and a line a person
// pastes is not a place to trust one. The file has to be the same issue — the
// same number, and the same fingerprint whenever the row still knows one — so
// a hand-edited number or a reused local file cannot hand one issue's command
// to another, and it has to belong to this project (F8b). What comes back has
// already been checked against this build's own command tree (D1614), which is
// what makes the suggestion a command the reader can actually run.
//
// A row read from the offline snapshot knows no fingerprint: the snapshot is
// display data only and its own pin refuses the field (T-IM5, snapshot.go).
// Its number and, when one is selected, the project it was filed under are
// then the whole match, which is what O4 needs — an offline run and a coding
// agent's session print the `cli_only` row and the command this machine's file
// recorded with it — and that row's number comes from this machine's own
// cached read of this project's store, where one number is one issue.
//
// Only a covered row offers one, `cli_only` included, because a `behind` row's
// answer is the update and an `unknown` row's is nothing (F5, O4): offering a
// command for an issue that may not be fixed yet would be a suggestion to run
// the thing that just failed.
func mineRetry(r issues.MineRow, all []issues.Issue, installation string, root *cobra.Command) string {
	if r.Coverage != issues.CoverageCovered && r.Coverage != issues.CoverageCLIOnly {
		return ""
	}
	if r.Number == 0 {
		return ""
	}
	for _, i := range all {
		if i.Number != r.Number || i.FailedCommand == "" {
			continue
		}
		if r.Fingerprint != "" && i.Fingerprint != r.Fingerprint {
			continue
		}
		if i.Installation != "" && installation != "" && i.Installation != installation {
			continue
		}
		if command, ok := retryCommand(i.FailedCommand, root); ok {
			return command
		}
	}
	return ""
}
