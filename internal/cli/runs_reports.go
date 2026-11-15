package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/digest"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// fugaro runs reports --job -- one run of the project's own reports job (E1).
//
// It is the writer of the stored reports: the same files D321's layout names,
// under reports/<slug>/<YYYY>/<key>.md of the runs bucket rather than in a
// repository's docs tree, which is the one prefix the project's reports
// account may write and the only write it holds. The bytes are
// internal/digest's own writer, so a stored report and the one fugaro runs
// digest commits for the same repository and period are the same file (F2).
//
// Four properties shape it:
//
//   - Its whole assignment is FUGARO_REPORTS_JOB_SPEC. The job has no checkout
//     and no local config on its machine, so there is nothing here to fall
//     back to and nothing reads one: a run without the document refuses to
//     start rather than writing reports somewhere nobody named.
//   - Closed periods, from its own clock. The document names the period kinds
//     the installation closes; which period of each kind has closed is the one
//     thing a nightly run cannot be handed at plan time, so each kind's most
//     recently closed period is computed here, by the rule the terminal
//     command reads (digestPeriod).
//   - A rerun is harmless. Each report is compared with the stored one before
//     it is written: equal means the object is left exactly as it is, which is
//     what makes a re-fired schedule or a second run of one night a no-op.
//   - Nothing in a report is a person or a task's own words (G1, D323): the
//     flags, recipes and figures are internal/digest's closed vocabulary.
//
// It writes the repository reports and no aggregate: the project's own
// document beside them is R8's, a later run of the same job.

// reportsJobSpecEnv is where the project's reports job reads its assignment:
// the JSON document internal/infra renders into the job's environment
// (ReportsJobDocEnv). The two halves spell the shape once each and the round
// trip in runs_reports_test.go keeps them in step, as the catalog check's own
// pair does.
const reportsJobSpecEnv = "FUGARO_REPORTS_JOB_SPEC"

// reportsNotInstalledReason is the one command that fixes a job this build
// cannot run, said the way a job's descriptions elsewhere say it.
const reportsNotInstalledReason = "run fugaro update"

// reportsJobDoc is the assignment FUGARO_REPORTS_JOB_SPEC carries as this
// command reads it: internal/infra.ReportsJobDoc, rendered into the job's
// environment by the installation.
type reportsJobDoc struct {
	Version      int      `json:"version"`
	Project      string   `json:"project"`
	Bucket       string   `json:"bucket"`
	Repositories []string `json:"repositories"`
	Periods      []string `json:"periods"`
}

// reportsOpenBucket opens the runs bucket. Tests replace it, as they replace
// the catalog check's own seam.
var reportsOpenBucket = blobx.Open

type reportsOptions struct {
	job bool
}

func newRunsReportsCmd() *cobra.Command {
	var o reportsOptions
	cmd := &cobra.Command{
		Use:   "reports",
		Short: "Write every repository's closed-period reports into the runs bucket (the project's reports job)",
		Long: "Write the project's stored reports: one report per repository and closed period,\n" +
			"under reports/<slug>/<YYYY>/<key>.md of the runs bucket -- D321's layout, in the\n" +
			"bucket rather than in a repository's docs tree.\n\n" +
			"With --job, as the project's reports job runs it, the whole assignment is the JSON\n" +
			"document in " + reportsJobSpecEnv + ", which the installation renders: the project,\n" +
			"the runs bucket, the repository slugs to report and the period kinds to close. The\n" +
			"job has no checkout and no local config, so nothing here reads either, and without\n" +
			"the document the command refuses to start rather than writing reports somewhere\n" +
			"nobody named. It refuses too outside a Cloud Run job, which is where it runs.\n\n" +
			"Each kind's most recently closed period is taken from this run's own clock (a day\n" +
			"is yesterday, the week is the one that ended on Sunday, the month and the year are\n" +
			"the ones that ended yesterday): which periods have closed is the one thing a nightly\n" +
			"run cannot be handed in advance. A rerun compares every report with the stored one\n" +
			"first -- equal means the object is left as it is, so a re-fired schedule writes\n" +
			"nothing the second time, and a period whose records changed is overwritten.\n\n" +
			"The bytes are internal/digest's own writer, so a stored report is the file fugaro\n" +
			"runs digest writes for the same repository and period. Each report's pattern rules\n" +
			"read the series already stored -- the reports before it -- and a yearly report is\n" +
			"the stored monthly ones of that year added up, because the run records are kept 90\n" +
			"days.\n\n" +
			"Nothing in a report is a person or a task's own words (G1, D323). The command\n" +
			"writes with the job's own credentials and nothing else: no git credential, no model\n" +
			"credential, no secret, and reports/ is the only prefix it may write. The project's\n" +
			"own aggregate is a later step of the job's and is not written here.\n\n" +
			"Without --job there is nothing to report: a person reads or writes one repository's\n" +
			"report of one period in a checkout with fugaro runs digest.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !o.job {
				return userErr("fugaro runs reports writes the project's stored reports and runs only as the project's reports job (fugaro runs reports --job); a person writes one repository's report into its own docs tree with fugaro runs digest")
			}
			return runRunsReports(cmd)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&o.job, "job", false, "run as the project's reports job (reads "+reportsJobSpecEnv+")")
	return cmd
}

// readReportsJobEnv reads the job's assignment, refusing a document that is
// not there or does not carry what a run needs: a job that cannot tell what to
// report says so once, with the one command that fixes it (the catalog check's
// own read, and its reason).
func readReportsJobEnv() (reportsJobDoc, error) {
	var doc reportsJobDoc
	raw := os.Getenv(reportsJobSpecEnv)
	if raw == "" {
		return doc, userErr("%s is not set: fugaro runs reports --job runs in the project's reports job, which fugaro update sets up", reportsJobSpecEnv)
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return doc, userErr("%s: %v", reportsJobSpecEnv, err)
	}
	if doc.Version < 1 || doc.Project == "" || doc.Bucket == "" || len(doc.Repositories) == 0 || len(doc.Periods) == 0 {
		return doc, userErr("%s is incomplete: %s", reportsJobSpecEnv, reportsNotInstalledReason)
	}
	// The bucket names the URL this run opens, and a name of another shape is
	// not one an installation rendered (gcp.ValidBucketName is the check
	// internal/infra makes when it renders the document).
	if !gcp.ValidBucketName(doc.Bucket) {
		return doc, userErr("%s names the runs bucket %q, which has a byte outside a-z, 0-9, '.', '_' and '-': %s", reportsJobSpecEnv, pluginwire.Printable(doc.Bucket), reportsNotInstalledReason)
	}
	for _, slug := range doc.Repositories {
		// A slug is the one path segment of every key this run writes, so what
		// is refused here is a value that is not one: an empty one, or one
		// carrying a separator, which would address a prefix the document did
		// not name (a slug itself is task.Slug's to derive, and the job is
		// handed the result rather than deriving one).
		if slug == "" || slug == "." || slug == ".." || strings.ContainsAny(slug, `/\`) {
			return doc, userErr("%s names %q, which is not one repository's prefix: %s", reportsJobSpecEnv, pluginwire.Printable(slug), reportsNotInstalledReason)
		}
	}
	return doc, nil
}

// reportsPeriods is the closed periods one run writes: each kind the document
// names, at the most recently closed period of that kind at now.
//
// A kind this build does not know is the whole run's refusal rather than a
// period it skips: the document is rendered by the installation's own plan,
// and a period silently left out would be a night that reported nothing with
// no line saying so.
func reportsPeriods(kinds []string, now time.Time) ([]digest.Period, error) {
	periods := make([]digest.Period, 0, len(kinds))
	for _, kind := range kinds {
		dim, err := digest.ParseDim(kind)
		if err != nil || !dim.IsPeriod() {
			return nil, userErr("%s: %q is not a period kind (day, week, month or year): %s", reportsJobSpecEnv, pluginwire.Printable(kind), reportsNotInstalledReason)
		}
		// The terminal command's own rule for "the most recent closed
		// period", so a job and a person cannot disagree about which one it is.
		p, err := digestPeriod(dim, "", now)
		if err != nil {
			return nil, err
		}
		periods = append(periods, p)
	}
	return periods, nil
}

func runRunsReports(cmd *cobra.Command) error {
	// The job is the one writer of reports/ in the project, and this is the
	// command it runs: a copy of it started anywhere else would write the
	// project's series with whatever credentials its caller holds, which is
	// why the job's own environment is part of the command (the catalog
	// check's own gate, refuseJobSubmission, for the same reason).
	if os.Getenv("CLOUD_RUN_JOB") == "" && os.Getenv("CLOUD_RUN_EXECUTION") == "" {
		return userErr("fugaro runs reports --job writes the project's stored reports and runs only as the project's reports job (CLOUD_RUN_JOB is not set here); a person reads one repository's report in a checkout with fugaro runs digest")
	}
	doc, err := readReportsJobEnv()
	if err != nil {
		return err
	}
	periods, err := reportsPeriods(doc.Periods, digestNow().UTC())
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	bucket, err := reportsOpenBucket(ctx, "gs://"+doc.Bucket)
	if err != nil {
		return remote(err)
	}
	defer bucket.Close()

	out := cmd.OutOrStdout()
	for _, p := range periods {
		for _, slug := range doc.Repositories {
			if err := reportsWriteRepository(ctx, bucket, slug, p, out); err != nil {
				return remote(fmt.Errorf("the report of %s for %s: %w", slug, p.Key, err))
			}
		}
	}
	return nil
}

// reportsWriteRepository writes one repository's report of one period into the
// bucket, building it the way the terminal command builds its own.
//
// The stored series is re-read for every period rather than once for the run:
// a yearly report is that year's monthly reports added up, and on 1 January
// the December month this same run writes a few periods earlier is one of
// them. Reading it per period is what makes the year's report the months that
// are stored by the time it is written, whatever order the document listed.
func reportsWriteRepository(ctx context.Context, b *blobx.Bucket, slug string, p digest.Period, out io.Writer) error {
	store := digest.NewStore(b.Bucket, slug)
	// No committed rows: this machine has no checkout, and the bucket is the
	// project's whole series.
	rows, err := store.Series(ctx, nil)
	if err != nil {
		return fmt.Errorf("reading the stored reports under %s: %w", store.Prefix(), err)
	}

	var (
		cur  digest.Summary
		head []byte
	)
	if p.Dim == digest.ByYear {
		months := digest.MonthsOf(rows, p.Key)
		if len(months) == 0 {
			fmt.Fprintf(out, "no report for %s: no monthly report of %s is stored yet, and a yearly one is its twelve months added up\n", slug, p.Key)
			return nil
		}
		cur = digest.RollUp(p.Key, months)
		if cur.Period == "" {
			fmt.Fprintf(out, "no report for %s: the stored monthly reports of %s are not ones this release wrote, so there is no year to roll up\n", slug, p.Key)
			return nil
		}
		head = digest.YearMarkdown(p, months, cur)
	} else {
		recs, truncated, err := reportsRecords(ctx, b, slug)
		if err != nil {
			return err
		}
		if truncated {
			fmt.Fprintf(out, "warning: %s's runs bucket listing holds more than %d runs; %s's report reads its newest %d\n", slug, digestMaxRuns, p.Key, digestMaxRuns)
		}
		d := digest.Build(recs, p)
		cur = d.Summary()
		head = digest.Markdown(d)
	}
	body := digest.FileBody(head, rows, p, cur)
	return reportsPut(ctx, b, store.Object(p.Key), body, out)
}

// reportsRecords reads one repository's runs the way the terminal command
// reads them -- the runs bucket's listing under the repository's prefix, then
// one result.json per run -- and hands back the ones that are this
// repository's own.
//
// The read is the terminal one's (digestRecords) with one difference, and it
// is the whole of G2 here: a person's command knows the repository's
// owner/name, and this one is handed a slug and nothing else. A record's own
// repo field is a word the record chose, so a record is this repository's only
// if it derives the very slug whose prefix it was found under -- the same
// derivation the launcher wrote that prefix with (task.Slug) -- and one that
// derives another slug, or none at all, is dropped before anything of it is
// built into a report.
func reportsRecords(ctx context.Context, b *blobx.Bucket, slug string) ([]runstore.Record, bool, error) {
	ids, err := runstore.ListRunIDs(ctx, b.Bucket, slug, time.Time{})
	if err != nil {
		return nil, false, remote(err)
	}
	truncated := len(ids) > digestMaxRuns
	if truncated {
		ids = ids[:digestMaxRuns]
	}
	recs := make([]runstore.Record, len(ids))
	found := make([]bool, len(ids))
	errs := make([]error, len(ids))
	parallel(len(ids), func(i int) {
		rec, err := absent(runstore.Open(b.Bucket, slug, ids[i]).ReadRecord(ctx))
		switch {
		case err == nil && rec != nil:
			recs[i], found[i] = *rec, true
		case err == nil:
			// No result.json: the run has not finished, so it is in no period
			// (G4).
		case corruptObject(err):
			// A record nobody can read is one run's problem, not the report's.
		default:
			errs[i] = err
		}
	})
	if err := errors.Join(errs...); err != nil {
		return nil, false, remote(fmt.Errorf("reading %s's run records: %w", slug, err))
	}
	out := make([]runstore.Record, 0, len(ids))
	for i, ok := range found {
		if ok && reportsOwnRecord(recs[i].Repo, slug) {
			out = append(out, recs[i])
		}
	}
	return out, truncated, nil
}

// reportsOwnRecord is whether a record's own repository is the one whose
// prefix it was found under: the record derives the slug, which is this
// build's own derivation of a provider and a name together.
func reportsOwnRecord(recRepo, slug string) bool {
	for _, provider := range config.Providers {
		if got, err := task.Slug(provider, recRepo); err == nil && got == slug {
			return true
		}
	}
	return false
}

// reportsPut writes one report, comparing it with the stored one first: an
// object that is there and equal is left exactly as it is, one that differs is
// overwritten, and one that is not there is written. The comparison is what
// makes a re-fired schedule harmless -- a report is a pure function of the
// records and the stored series, so a second run of one night computes the
// bytes the first one wrote.
func reportsPut(ctx context.Context, b *blobx.Bucket, key string, body []byte, out io.Writer) error {
	stored, _, err := b.Read(ctx, key)
	switch {
	case err == nil && bytes.Equal(stored, body):
		fmt.Fprintf(out, "unchanged %s\n", key)
		return nil
	case err != nil && !errors.Is(err, blobx.ErrNotExist):
		return fmt.Errorf("reading %s: %w", key, err)
	}
	if err := b.Put(ctx, key, body, "text/markdown"); err != nil {
		return fmt.Errorf("writing %s: %w", key, err)
	}
	fmt.Fprintf(out, "wrote %s\n", key)
	return nil
}
