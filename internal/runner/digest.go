package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/digest"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/report"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/stats"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/verify"
)

// The digest step is the report run's own work (task-governance plan T3.2,
// D320). A run-digest recipe's first step is this one (recipe.UsesDigest), and
// the runner -- never a model -- writes the run reports the repository is
// missing into docs/fugaro-reviews/, commits them, and leaves the recipe's one
// senior review to decide the pull request.
//
// What it may read is the launcher's manifest (task.DigestRuns, T3.6): the run
// IDs of this repository's records the job is allowed to read. Every ID is
// checked against a run ID's own shape before it names anything, and every
// read goes through this run's own store, so each object is under
// runs/<this repository's slug>/. On a project job, whose bucket condition
// reaches every repository's runs/ (G2), that check is the only guard there
// is, so it comes first: every ID in the manifest is checked before the first
// record is read, and one ID of any other shape refuses the step with nothing
// read. There is no listing: a list is a bucket-level call the job's
// grant cannot hold (design bucket-iam.md V7).
//
// Nothing here is a model call, and no word a record holds reaches a written
// report: the files are internal/digest's deterministic output (G1, G3), so no
// task text, address or log excerpt can travel into a possibly public
// repository through this step.

// The step's own names. The report layout itself (D321) is internal/digest's,
// which both writers of the same tree call: a report this step commits must be
// the one `fugaro runs digest` regenerates byte for byte.
const (
	// digestStageName is the stage the step records on the run. It is a stage
	// name like "provision": the record's own stage enum carries no digest
	// step, and the stage timings' names are free strings.
	digestStageName = "digest"
	// digestRecipeName is the catalog recipe a report run's record names: the
	// launcher writes the manifest for it (internal/cli's digestManifest) and
	// nothing else, which is how an open report run is told from a run that
	// merely opened a pull request.
	digestRecipeName = "run-digest"
)

// The step's bounds, all of them so one report run cannot spend its context on
// another repository's history.
const (
	// digestCatchUpDays is how many closed days the step reports at once. The
	// report run is launched by the user's own routine (R8, no Fugaro-side
	// scheduler), so a missed day must be filled by the next run: the
	// launcher's manifest window covers these days plus the month a monthly
	// report needs (internal/cli's digestManifestWindow).
	digestCatchUpDays = 7
	// digestOpenPRs bounds how many of the manifest's records the step asks
	// the provider about when it looks for an open report run. The launcher
	// refuses a launch while one report run is in flight, so at most a few
	// unmerged reports are in the window; the bound is the repeat of a bound
	// that failed, not a promise about the count.
	digestOpenPRs = 5
	// digestMaxPR bounds a pull-request number read from a record: a record is
	// untrusted input, and a number outside this is not one this provider
	// serves (internal/digest caps its own the same way).
	digestMaxPR = 1 << 30
)

// digestStep is the report run's first step: it writes the reports no period
// in the tree holds yet and commits them. It records its own stage on the
// record, so a report run's figures show what the step cost (nothing but the
// reads it made) beside its review's.
func (r *run) digestStep(ctx context.Context) error {
	started := r.d.Now()
	written, err := r.writeDueReports(ctx)
	if err == nil && len(written) > 0 {
		// The commit the step just made carries the verified test run the
		// pull request's readiness is decided on (outcome.go's Decide), made
		// the way the check step's own autofix commit makes one (gate.go's
		// Fixed branch): no stage of this recipe could make it, since the
		// digest step takes implement's place and no model writes a report,
		// and without it every report run's pull request would be a draft
		// whose reason names a test run no step of the recipe was asked for.
		r.verifyOnce(ctx, verify.KindTest, "the digest step's reports")
	}
	r.rec.Stages = append(r.rec.Stages, runstore.StageTiming{
		Name: digestStageName, StartedAt: started.UTC(), DurationS: r.d.Now().Sub(started).Seconds(),
	})
	r.save(ctx)
	if err != nil {
		return err
	}
	if len(written) == 0 {
		// Every period is already in the tree, and another report run has the
		// rest in an open pull request: nothing to write, nothing to review,
		// and no pull request to open. The run ends here, the way a run whose
		// check gate found nothing to fix does.
		r.endWithoutPR(ctx, "every report period is already in the tree: nothing to write")
	}
	return nil
}

// digestFinalize is the digest step's last word over the pull request, called
// at finalize before the branch is pushed or the pull request is marked ready
// (T3.2's call). T3.3 fills it: the run's diff against its base must be the
// reports the step generated, byte for byte -- a report a review or a fix round
// changed is reverted to the generated bytes, a file anywhere else is removed,
// and a check that cannot be made leaves the pull request a draft with the
// reason. Nothing of that is decided here yet, so every run returns nil.
func (r *run) digestFinalize(context.Context) error { return nil }

// writeDueReports writes the due periods' files and the index, and commits
// them, returning the paths it wrote.
func (r *run) writeDueReports(ctx context.Context) ([]string, error) {
	ids := r.spec.DigestRuns
	if len(ids) == 0 {
		return nil, errors.New("this task carries no digest_runs manifest, so the digest step has no record it may read: a report run is launched by `fugaro run --recipe run-digest`, which writes the manifest")
	}
	recs, dropped, err := r.digestRecords(ctx, ids)
	if err != nil {
		return nil, err
	}
	if dropped > 0 {
		// The count is the step's own: which records were left out is never a
		// word of theirs and never a name of theirs in a file (G1).
		r.d.Log.Warn("records the manifest named are not ones the reports count", "dropped", dropped, "read", len(recs))
	}
	rows, err := r.digestRows()
	if err != nil {
		return nil, err
	}
	base := "origin/" + r.diffBase()
	open, err := r.digestOpenBranches(ctx, recs)
	if err != nil {
		return nil, err
	}

	var written, keys []string
	for _, p := range digestDuePeriods(r.d.Now()) {
		rel := digest.Dir + "/" + digest.PeriodPath(p.Key)
		held, err := r.digestHeld(ctx, base, rel, open)
		if err != nil {
			return nil, err
		}
		if held {
			continue
		}
		body, summary, ok, err := r.digestReport(p, recs, rows)
		if err != nil {
			return nil, err
		}
		if !ok {
			r.d.Log.Info("a period is due but cannot be reported yet; it is left to the next report run",
				"period", p.Key, "dim", string(p.Dim))
			continue
		}
		if err := r.writeDigestFile(rel, body); err != nil {
			return nil, err
		}
		rows = digest.WithRow(rows, p, summary)
		written, keys = append(written, rel), append(keys, p.Key)
	}
	if len(written) == 0 {
		return nil, nil
	}
	if err := r.writeDigestFile(digest.Dir+"/"+digest.IndexName, []byte(digest.IndexMarkdown(rows))); err != nil {
		return nil, err
	}
	// The runner's own commit, in the runner's voice: the reports are its work
	// and not the model's, and the message names the periods it wrote.
	sort.Strings(keys)
	if _, err := r.repo.CommitAll(ctx, "fugaro: run digest "+strings.Join(keys, ", ")); err != nil {
		return nil, fmt.Errorf("committing the reports: %w", err)
	}
	return written, nil
}

// digestRecords reads every record the manifest names: each one by name, under
// this run's own repository prefix, and never a listing.
//
// Every ID is checked before any record is read, so an ID that is not a run
// ID's shape refuses the step with nothing read, wherever it sits. It
// becomes part of an object name (runstore.Store.Sibling), so an ID carrying a
// path, another repository's slug or a scheme is exactly the read this step
// must not make: on a project job the bucket condition does not isolate
// repositories (G2), and the manifest is the launcher's word rather than a
// guarantee this step can check. A record that is not there, or does not
// decode, is one the reports do not count and the count says how many; a read
// that failed for any other reason fails the step, because a bucket that
// cannot be read is not a repository with no runs (G5).
func (r *run) digestRecords(ctx context.Context, ids []string) ([]runstore.Record, int, error) {
	if r.d.Store == nil {
		return nil, 0, errors.New("this run has no runs bucket, so the digest step cannot read a record")
	}
	for _, id := range ids {
		if !task.ValidRunID(id) {
			return nil, 0, fmt.Errorf("the digest step's manifest names %q, which is not a run ID: a report run reads only the records of its own repository (runs/%s/), so the run is refused before any record is read",
				safetext.OneLine(safetext.Strip(id)), r.d.Store.Slug())
		}
	}
	recs := make([]runstore.Record, 0, len(ids))
	dropped := 0
	for _, id := range ids {
		rec, err := r.d.Store.Sibling(id).ReadRecord(ctx)
		var syntax *json.SyntaxError
		var types *json.UnmarshalTypeError
		switch {
		case errors.Is(err, runstore.ErrNotFound), errors.As(err, &syntax), errors.As(err, &types):
			// A run that never wrote a record, or one whose record is not a
			// record: neither is a figure a report may carry.
			dropped++
			continue
		case err != nil:
			return nil, 0, fmt.Errorf("reading the record of run %s: %w", id, err)
		}
		if !task.ValidRunID(rec.RunID) || rec.FinishedAt == nil {
			// The report's own rule: a record whose run ID is not a run ID,
			// or that has not finished, is not one of any period's runs, so
			// it is counted as one the reports read past rather than handed
			// to them to skip again (internal/digest's Build skips it too).
			dropped++
			continue
		}
		recs = append(recs, *rec)
	}
	return recs, dropped, nil
}

// digestDuePeriods is the periods a report run writes at now: the seven closed
// days the catch-up covers, and the week, month or year that closed since the
// last one. A day with no run of the repository still gets its file, which is
// how a reader tells "no runs that day" from "no report run that day" (G5).
//
// The coarser periods are due on the day after they ended: the week on a
// Monday (yesterday is its Sunday), the month on the 1st, and the year on 1
// January. A 1st that is a Monday closes a week and a month at once, and both
// are written.
func digestDuePeriods(now time.Time) []digest.Period {
	today := now.UTC()
	type due struct {
		dim  report.Dim
		date string
	}
	var dues []due
	for i := 1; i <= digestCatchUpDays; i++ {
		dues = append(dues, due{report.ByDay, today.AddDate(0, 0, -i).Format("2006-01-02")})
	}
	yesterday := today.AddDate(0, 0, -1).Format("2006-01-02")
	if today.Weekday() == time.Monday {
		dues = append(dues, due{report.ByWeek, yesterday})
	}
	if today.Day() == 1 {
		dues = append(dues, due{report.ByMonth, yesterday})
		if today.Month() == time.January {
			dues = append(dues, due{report.ByYear, yesterday})
		}
	}
	var out []digest.Period
	for _, d := range dues {
		// A date this step formatted is a date internal/digest's own clock
		// reads; one it cannot name is a period no report is written for.
		p, err := digest.NewPeriod(d.dim, d.date)
		if err != nil {
			continue
		}
		out = append(out, p)
	}
	return out
}

// digestOpenBranches is the branches of this repository's report runs whose
// pull request is still open: what a report already written but not merged
// sits on. A run is one of them when its record names the catalog's report
// recipe and a pull request of its own, and that pull request is open; the
// branch is the run's own fugaro/<run-id> and is fetched so its tree can be
// read. Everything about the record is untrusted input, so the number is
// bounded and the branch name is checked against the shape every run branch
// has before it reaches a refspec.
func (r *run) digestOpenBranches(ctx context.Context, recs []runstore.Record) ([]string, error) {
	var out []string
	asked := 0
	for i := range recs {
		rec := &recs[i]
		if asked >= digestOpenPRs {
			break
		}
		if rec.PR == nil || rec.PR.Number < 1 || rec.PR.Number > digestMaxPR {
			continue
		}
		if rec.Recipe == nil {
			continue
		}
		if name, ok := stats.ShippedName(rec.Recipe.Name, rec.Recipe.Source); !ok || name != digestRecipeName {
			continue
		}
		if r.provider == nil {
			return nil, errors.New("this run has no git provider, so the digest step cannot tell an open report from a merged one")
		}
		asked++
		pr, err := r.provider.PullRequest(ctx, rec.PR.Number)
		if err != nil {
			return nil, fmt.Errorf("reading pull request #%d of a report run: %w", rec.PR.Number, err)
		}
		if pr.State != gitprov.PROpen || !task.BranchRE.MatchString(pr.SourceBranch) {
			continue
		}
		if err := r.repo.FetchBase(ctx, pr.SourceBranch); err != nil {
			return nil, fmt.Errorf("fetching the branch of pull request #%d: %w", rec.PR.Number, err)
		}
		out = append(out, "origin/"+pr.SourceBranch)
	}
	return out, nil
}

// digestHeld reports whether a report file is already somewhere the run must
// not write it over: on the base branch, or on the branch of an open report
// run. An unmerged report is not written twice, which is what keeps two runs
// from opening pull requests that add the same file.
func (r *run) digestHeld(ctx context.Context, base, rel string, open []string) (bool, error) {
	for _, rev := range append([]string{base}, open...) {
		mode, err := r.repo.TreeEntryMode(ctx, rev, rel)
		if err != nil {
			return false, fmt.Errorf("reading the tree of %s: %w", r.redact(rev), err)
		}
		if mode != "" {
			return true, nil
		}
	}
	return false, nil
}

// digestReport is one period's file: its body, the summary block it carries
// and whether the period can be reported at all.
//
// A day, week or month report is built from the records the manifest named. A
// year has no records of its own -- the bucket keeps 90 days of them (F24) --
// so it is its twelve monthly reports added up, and a year whose months are
// not committed yet is left to the next report run rather than written as a
// year of nothing.
func (r *run) digestReport(p digest.Period, recs []runstore.Record, rows []digest.Row) ([]byte, digest.Summary, bool, error) {
	if p.Dim == report.ByYear {
		months := digest.MonthsOf(rows, p.Key)
		if len(months) == 0 {
			return nil, digest.Summary{}, false, nil
		}
		cur := digest.RollUp(p.Key, months)
		if cur.Period == "" {
			return nil, digest.Summary{}, false, nil
		}
		return digest.FileBody(digest.YearMarkdown(p, months, cur), rows, p, cur), cur, true, nil
	}
	d := digest.Build(recs, p)
	cur := d.Summary()
	return digest.FileBody(digest.Markdown(d), rows, p, cur), cur, true, nil
}

// digestPath is one of this step's own paths under the checkout, with every
// component of it checked for a symbolic link first.
//
// The path is the step's own (digest.Dir and the layout inside it), but the
// tree it sits in is the base branch's, and anyone who can commit to that
// branch chooses what the components are: a docs/fugaro-reviews that is a link
// to the repository root, or a README.md inside it that is a link to the root
// README, would make a write land outside the report folder -- on a file the
// pull request then shows as changed, and over a file no report run may touch.
// A component that is a link is refused, and the step fails rather than writing
// anywhere else. The last component is what the caller's own open refuses with
// O_NOFOLLOW (writeDigestFile), so a link planted between this check and that
// open is caught too; the components above it are what only this walk sees.
func (r *run) digestPath(rel string) (string, error) {
	path := r.d.WorkDir
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." {
			continue
		}
		path = filepath.Join(path, part)
		fi, err := os.Lstat(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			// What is not there yet is the step's to create.
			return filepath.Join(r.d.WorkDir, filepath.FromSlash(rel)), nil
		case err != nil:
			return "", fmt.Errorf("reading %s: %w", rel, err)
		case fi.Mode()&os.ModeSymlink != 0:
			return "", fmt.Errorf("%s is a symbolic link, and the digest step neither reads nor writes through one: the report folder is the repository's to shape, but a link there would put a generated report somewhere no report run may write", rel)
		}
	}
	return filepath.Join(r.d.WorkDir, filepath.FromSlash(rel)), nil
}

// writeDigestFile writes one generated file into the checkout. rel is one of
// this file's own paths, never a name a record or a file chose.
func (r *run) writeDigestFile(rel string, body []byte) error {
	path, err := r.digestPath(rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(rel), err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|oNoFollow, 0o644)
	if err != nil {
		return fmt.Errorf("writing %s: %w", rel, err)
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		return fmt.Errorf("writing %s: %w", rel, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", rel, err)
	}
	return nil
}

// digestRows reads the reports the checkout holds, oldest period first. A
// report is a file a person may edit by hand, so nothing it holds is trusted:
// a file whose block does not decode, is not one of this report's shapes, or
// names another period than its own path does is a row with no summary, and it
// is named on the log by its path -- a path this step checked against its own
// shape, never anything the file holds (G1). A markdown file whose name is not
// D321's is a report renamed out of the layout: no row at all, logged the same
// way, so a report does not leave the index without a word.
func (r *run) digestRows() ([]digest.Row, error) {
	dir, err := r.digestPath(digest.Dir)
	if err != nil {
		return nil, err
	}
	years, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", digest.Dir, err)
	}
	skip := func(rel string) {
		r.d.Log.Warn("a file under the report folder is not a report this run wrote; it is left out of the index's figures",
			"path", safetext.OneLine(rel))
	}
	var rows []digest.Row
	var names []string
	for _, y := range years {
		if !y.IsDir() {
			if y.Name() != digest.IndexName {
				names = append(names, y.Name())
			}
			continue
		}
		if !digest.IsYearDir(y.Name()) {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(dir, y.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading %s/%s: %w", digest.Dir, y.Name(), err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				names = append(names, y.Name()+"/"+e.Name())
			}
		}
	}
	for _, rel := range names {
		dim, key, ok := digest.ReportName(rel)
		if !ok {
			if strings.HasSuffix(rel, ".md") {
				skip(rel)
			}
			continue
		}
		row := digest.Row{Dim: dim, Key: key, Rel: rel}
		path, err := r.digestPath(digest.Dir + "/" + rel)
		if err != nil {
			return nil, err
		}
		s, read := digest.ReadRow(path, key)
		if read {
			row.Summary, row.Read = s, true
		} else {
			skip(rel)
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	return rows, nil
}
