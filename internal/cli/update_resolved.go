package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/installrec"
	"github.com/dimipaun/fugaro/internal/issueresolve"
	"github.com/dimipaun/fugaro/internal/issues"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// update_resolved.go is the resolved-issues step of `fugaro update` (T-RU5):
// after a run that installed this build's release, it records the two releases
// the update spans, runs the resolver pass (internal/issues, T-RU4) and prints
// what this release fixes about the project's filed issues.
//
// It is the step the hard rule protects, so it fails nothing. Every way it can
// go wrong — no project selected, a bucket that will not open, a record that
// cannot be read or written, an issue store this machine cannot read, a pass
// that GitHub or the shared store refused — is a line not printed rather than
// a failed update, and none of it is an error a person has to act on. It runs
// no command, reads no stdin and waits for nobody; the one thing it spends is
// the pass's own bounded time (internal/issues: 20 seconds, at most 20
// requests).
//
// What it prints is the local record's own text and never GitHub's (F16a): a
// summary is the reporter's words from the store, through safetext, and a
// command is a command this build has checked against its own tree (D1614).

const (
	// resolvedSummaryLead and resolvedSummaryTail bracket the count of the
	// issues this update is the first to report, and the list follows them:
	// `This update fixes 2 of your project's issues: #0007 (summary), …`.
	resolvedSummaryLead = "This update fixes "
	resolvedSummaryTail = " of your project's issues: "
	// resolvedAlsoHead opens the block of fixes an earlier pass never got to
	// report (F6): an issue whose milestone is at or below the release the
	// last update spanned was fixed before this window, and a machine that
	// met GitHub while it was unreachable is why nobody was told.
	resolvedAlsoHead = "Also fixed since the last pass:"
	// resolvedFixedLead opens the line that names the command one fixed issue
	// failed on (spec step 6). resolvedRerun is the half that is a
	// suggestion, and only a command from this machine's own file gets it
	// (O4): a command that came from the shared record — which every launcher
	// can write — is shown as what failed and nothing more.
	resolvedFixedLead = " is fixed. The command that failed was '"
	resolvedRerun     = "'. You may want to try it again."
	resolvedShared    = "' (from the shared record)."
	// resolvedMayChange is the note both wordings end with: the command is a
	// command, and commands change things.
	resolvedMayChange = " It may change things."
)

// updateResolvedRuns reports whether this run is one that resolves issues at
// all (F17). It is the update door's own step, and it is about a run that
// brought the installation up to this release: a run that applied nothing —
// --plan-only, --print-vars, --forget, --config-only, --publish-config,
// --anchor, and a --non-interactive run without --yes — resolved nothing and
// has no release to record, and --repo converges a checkout's repository
// rather than the installation (updateRetryOffers is the list, plus the
// door). Half of these can no longer arrive here at all — runInit returns
// before the converge for --print-vars, --forget, --config-only,
// --publish-config and --anchor, and --repo never enters it — and the check
// is kept whole anyway: it is the one place the modes are named, and a later
// change that moves one of them back under the converge must not silently
// give it a cloud write.
func updateResolvedRuns(o *initOptions) bool {
	if o.doorName() != updateDoor.name || o.repo {
		return false
	}
	return updateRetryOffers(o)
}

// updateResolveDeps is the pass's wiring on the update door (T-RU4): the
// project's own store, a reader over GitHub's API, this machine's cursor and
// the session's own marker. It is a seam because the tests of this step answer
// for GitHub rather than reaching it, and it mirrors the report's own wiring
// (runs_digest.go's digestResolveDeps), so both passes a machine runs are the
// same pass over the same store.
var updateResolveDeps = func(cmd *cobra.Command, b *blobx.Bucket) issues.ResolveDeps {
	return issues.ResolveDeps{
		Bucket:      b,
		Reader:      issueresolve.NewReader(nil, issueresolve.DefaultBaseURL),
		Cursor:      issueresolve.LoadCursor(os.Getenv),
		AgentMarker: func() string { return agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())) },
		Now:         time.Now,
	}
}

// resolvedIssueJSON is one entry of the --json document's `resolved_issues`
// (U4): the additive field the console, the mobile app and the history read,
// so its names are fixed and nothing here reuses one. hit_by is the names as
// data — the same names the printed line joins into a sentence — and it is
// omitted when the store named nobody.
type resolvedIssueJSON struct {
	Number  int      `json:"number"`
	Summary string   `json:"summary"`
	Release string   `json:"release"`
	URL     string   `json:"url"`
	HitBy   []string `json:"hit_by,omitempty"`
}

// resolvedKey names one issue as this machine's own file and the shared
// record both know it: the number the store gave it and the fingerprint of
// the bug. The two together are what makes a local file this project's issue
// (F8) — a local file is global to the machine, so project A's #0007 must not
// supply project B's command — and it is the pair `fugaro issue mine` and the
// retry offer both join on.
type resolvedKey struct {
	number      int
	fingerprint string
}

// resolvedFix is one issue the summary lists: what it is, who hit it, and the
// command it failed on when this build can name one.
type resolvedFix struct {
	number  int
	summary string
	release string
	url     string
	hitBy   []string
	hitLine string
	command string
	// local says the command is this machine's own file's, which is what
	// makes the line a suggestion (O4).
	local bool
}

// updateResolvedWindow is the window the summary's list is read against: the
// release this build is, and the one the installation was on before it (F17,
// D1757).
//
// The record is read BEFORE this run advances it, so `release` is the release
// the update before this one installed; when that is below this build's, it is
// the window's lower bound and the window is (X, Y]. When it is not — a re-run
// of the same update, another machine's record already at this release — the
// lower bound is `previous_release`, which is what keeps a re-run printing the
// same list rather than an empty one.
//
// A record that is absent or unreadable is no window and not an error: an
// installation that has never updated with a build that writes one is the
// normal state of the first update after this feature. Both answers leave the
// lower bound empty, so the window holds every release at or below this build
// and nothing is "older than the last pass" — which is the sweep result, with
// the window's own question unanswered rather than guessed at.
func updateResolvedWindow(ctx context.Context, b *blobx.Bucket) issues.Versions {
	v := issues.Versions{Release: Version, Running: Version}
	rec, err := installrec.Read(ctx, b)
	if err != nil {
		return v
	}
	if rec.Release != "" && updatecheck.Newer(Version, rec.Release) {
		v.Previous = rec.Release
		return v
	}
	v.Previous = rec.PreviousRelease
	return v
}

// printResolvedIssues records what this update spans, runs one pass and
// prints what it found, and answers the findings for the --json document.
//
// The order of the three is the point. The record is advanced first so that a
// later reader — the daily report, a teammate's own update — reads the window
// this run made; the pass then meets a store whose rows another machine may
// already have marked, and it is the record, not the pass, that decides which
// fixes a person is told about. Nothing here reads what the pass wrote to
// build the list: the list is the pass's findings plus the shared store's own
// `resolved` rows in the window, which is what makes a second machine's update
// print what the first one already marked (F18).
func (r *initRun) printResolvedIssues(ctx context.Context, b *blobx.Bucket) []resolvedIssueJSON {
	window := updateResolvedWindow(ctx, b)
	agent := agentMarker(os.Getenv, r.stdinTerminal())
	if agent == "" {
		// The installation record is a cloud write, and a coding agent's
		// session makes none (F16d). A write this machine may not make — a
		// launcher's runs bucket is the operator's — is a skip, and a version
		// that is not a release (a dev build) advances nothing: installrec
		// answers both for itself, and neither is worth a line here.
		_, _ = installrec.Advance(ctx, b, Version)
	}
	res, _ := issues.Resolve(ctx, updateResolveDeps(r.cmd, b), Version)
	rows, err := issues.ListShared(ctx, b)
	if err != nil {
		// A store that cannot be read is what the pass already said in its own
		// Skipped; the list is built from what came back and nothing else.
		rows = nil
	}
	news, older, mark := r.resolvedFixes(ctx, b, res, rows, window)
	r.printResolvedList(news, older)
	r.markResolvedPrinted(mark)
	if agent == "" {
		r.markResolvedOffered(mark)
	}
	out := make([]resolvedIssueJSON, 0, len(news)+len(older))
	for _, f := range append(append([]resolvedFix{}, news...), older...) {
		out = append(out, resolvedIssueJSON{
			Number:  f.number,
			Summary: safetext.OneLine(f.summary),
			Release: f.release,
			URL:     f.url,
			HitBy:   f.hitBy,
		})
	}
	return out
}

// resolvedFixes is the list, split into the fixes this update is the first to
// report and the ones an earlier pass left unsaid, with the command each one's
// line may name.
//
// Two sources, joined by number. The pass's findings are the issues this run
// asked GitHub about and found closed as completed at or below this build —
// already marked `resolved` in the store, unless this is a coding agent's
// session. The store's own `resolved` rows in the window are the ones another
// machine's update marked and this run never saw as `submitted`: without them
// the second machine to update would print nothing at all (F18).
//
// Which of the two lists a fix goes in is the window's answer, not the
// source's: a milestone at or below the release the last update spanned was
// fixed before this window, and a pass that met GitHub offline is why it went
// unsaid until now (F6).
func (r *initRun) resolvedFixes(ctx context.Context, b *blobx.Bucket, res issues.Result, rows []issues.SharedRow, v issues.Versions) (news, older []resolvedFix, mark []issues.Issue) {
	byNumber := make(map[int]issues.SharedRow, len(rows))
	for _, row := range rows {
		byNumber[row.Number] = row
	}
	local, localRead := r.resolvedLocal()
	localOut := agentMarker(os.Getenv, r.stdinTerminal()) != ""

	seen := make(map[int]bool, len(res.Fixed))
	for _, f := range res.Fixed {
		seen[f.Number] = true
		fix := resolvedFix{
			number: f.Number, summary: f.Summary, release: f.Release,
			url: f.URL, hitBy: f.HitBy, hitLine: f.HitByLine,
		}
		if i, ok := r.resolvedCommand(&fix, byNumber[f.Number], local, localRead, localOut); ok {
			mark = append(mark, i)
		}
		if v.Holds(f.Release) {
			news = append(news, fix)
		} else {
			older = append(older, fix)
		}
	}
	for _, row := range rows {
		if seen[row.Number] || row.Status != issues.StatusResolved || !v.Holds(row.ResolvedIn) {
			continue
		}
		names, err := issues.Reporters(ctx, b, row.Number)
		if err != nil {
			names = nil
		}
		hitBy, line := issues.HitBy(names)
		fix := resolvedFix{
			number: row.Number, summary: row.Summary, release: row.ResolvedIn,
			url: row.URL, hitBy: hitBy, hitLine: line,
		}
		if i, ok := r.resolvedCommand(&fix, row, local, localRead, localOut); ok {
			mark = append(mark, i)
		}
		news = append(news, fix)
	}
	sort.Slice(news, func(a, c int) bool { return news[a].number < news[c].number })
	sort.Slice(older, func(a, c int) bool { return older[a].number < older[c].number })
	return news, older, mark
}

// resolvedLocal is this machine's own issue files, by the pair that names one
// project's issue (F8). A store that cannot be read answers none: the list is
// a list of issues, and one this machine cannot look up is one whose command
// comes from the shared record or from nowhere.
func (r *initRun) resolvedLocal() (map[resolvedKey]issues.Issue, bool) {
	store, err := openIssueStore()
	if err != nil {
		return nil, false
	}
	files, err := store.List()
	if err != nil {
		return nil, false
	}
	out := make(map[resolvedKey]issues.Issue, len(files))
	for _, i := range files {
		if i.Number == 0 || i.Fingerprint == "" {
			continue
		}
		out[resolvedKey{i.Number, i.Fingerprint}] = i
	}
	return out, true
}

// resolvedCommand fills in the command fix's line may name, and answers this
// machine's own record when it was the one that supplied it, so the caller can
// mark it as offered.
//
// This machine's own file comes first (D1755), and its command is the one the
// person actually ran: the local file is written by this machine and by
// nothing else. The shared record's is the fallback, and it is the text a
// launcher — who can write the record — chose, so it is re-redacted, checked
// against this build's own command tree (retryCommand, D1614) and labelled as
// where it came from, and it is never shown in a coding agent's session
// (F16d): a session's output is read by an agent, and this is a command line
// an agent might take as an instruction.
func (r *initRun) resolvedCommand(fix *resolvedFix, row issues.SharedRow, local map[resolvedKey]issues.Issue, localRead, localOut bool) (issues.Issue, bool) {
	if localRead {
		if i, ok := local[resolvedKey{fix.number, row.Fingerprint}]; ok {
			if command, ok := retryCommand(i.FailedCommand, r.cmd.Root()); ok {
				fix.command, fix.local = command, true
				return i, true
			}
		}
	}
	if localOut {
		return issues.Issue{}, false
	}
	if command, ok := retryCommand(issues.Redact(row.FailedCommand, nil), r.cmd.Root()); ok {
		fix.command = command
	}
	return issues.Issue{}, false
}

// printResolvedList is the whole of the step's output, and it says nothing at
// all when there is nothing to say: an update that fixes none of the project's
// issues prints the same bytes it printed before this feature existed, which
// is what keeps a machine that is offline — or that has filed nothing — from
// being told about it.
//
// The order is the spec's: what this update fixes, what an earlier pass left,
// then who hit each one ("next to every fixed issue", so an admin knows whom
// to tell), then the command each failed on.
func (r *initRun) printResolvedList(news, older []resolvedFix) {
	if len(news) == 0 && len(older) == 0 {
		return
	}
	if len(news) > 0 {
		named := make([]string, 0, len(news))
		for _, f := range news {
			named = append(named, fmt.Sprintf("#%04d (%s)", f.number, safetext.OneLine(f.summary)))
		}
		fmt.Fprintf(r.w, "%s%d%s%s.\n", resolvedSummaryLead, len(news), resolvedSummaryTail, strings.Join(named, ", "))
	}
	if len(older) > 0 {
		fmt.Fprintln(r.w, resolvedAlsoHead)
		for _, f := range older {
			fmt.Fprintf(r.w, "#%04d (%s)\n", f.number, safetext.OneLine(f.summary))
		}
	}
	for _, f := range append(append([]resolvedFix{}, news...), older...) {
		if f.hitLine == "" {
			continue
		}
		fmt.Fprintf(r.w, "#%04d: %s\n", f.number, safetext.OneLine(f.hitLine))
	}
	for _, f := range append(append([]resolvedFix{}, news...), older...) {
		if f.command == "" {
			continue
		}
		fmt.Fprintln(r.w, resolvedFixedLine(f.number, f.command, f.local))
	}
}

// resolvedFixedLine is the one line about a fixed issue's command, built here
// because two callers print it and they must not say different things about
// one issue: this step, for the issues the release it installed fixes, and the
// retry offer (update_retry.go, T-IS19), for the issues this machine itself
// filed and has not been told about.
//
// local is what makes the difference (O4): a command this machine recorded is
// the person's own, so the line offers to run it again, while one that came
// from the shared record is a launcher's text — the store is writable by every
// launcher — and is shown as what failed and nothing more.
func resolvedFixedLine(number int, command string, local bool) string {
	if local {
		return fmt.Sprintf("#%04d%s%s%s%s", number, resolvedFixedLead, command, resolvedRerun, resolvedMayChange)
	}
	return fmt.Sprintf("#%04d%s%s%s%s", number, resolvedFixedLead, command, resolvedShared, resolvedMayChange)
}

// markResolvedPrinted is what the run remembers about the lines it has just
// printed: the issues whose command came from this machine's own file, by the
// pair that names one project's issue (F8). The retry offer below the step
// says the same sentence about the same issue, and one run says it once
// (resolvedFixedLine is both lines). The file's own mark is the memory across
// runs; this is the memory inside one, and it is the one that holds where that
// write did not happen — a coding agent's session makes none (F16d), and a
// store that cannot be written leaves it unwritten in a person's run too.
func (r *initRun) markResolvedPrinted(mark []issues.Issue) {
	if len(mark) == 0 {
		return
	}
	if r.o.resolvedPrinted == nil {
		r.o.resolvedPrinted = make(map[resolvedKey]bool, len(mark))
	}
	for _, i := range mark {
		r.o.resolvedPrinted[resolvedKey{i.Number, i.Fingerprint}] = true
	}
}

// markResolvedOffered records that this machine has now been told about the
// issues the summary named its own command for (D1735): the flag is local and
// stays local, so the next update's retry offer does not print the same
// sentence about the same issue again. Best effort, like everything else here —
// a store that cannot be written offers the line again on the next update and
// fails nothing.
func (r *initRun) markResolvedOffered(mark []issues.Issue) {
	if len(mark) == 0 {
		return
	}
	store, err := openIssueStore()
	if err != nil {
		return
	}
	for _, i := range mark {
		i.Notified = true
		_ = store.Save(i)
	}
}
