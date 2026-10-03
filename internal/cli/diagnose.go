package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/logtail"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/verify"
)

const (
	// agentMessageBytes caps the agent's final message in a diagnosis.
	agentMessageBytes = 4 << 10
	// diagnoseLogLines is how many of the newest log lines a diagnosis keeps.
	diagnoseLogLines = 30
)

// Diagnosis is what diagnose gathers about one run. Every string in it
// that comes from the run is redacted.
type Diagnosis struct {
	Project      string           `json:"project"` // the Fugaro project
	Row          runview.Row      `json:"row"`
	Halt         *runstore.Halt   `json:"halt,omitempty"`     // why a halted run was halted
	Verify       []verify.Record  `json:"verify,omitempty"`   // result.json's verify records
	Failed       []string         `json:"failed,omitempty"`   // the last test record's failed tests
	Flaky        []string         `json:"flaky,omitempty"`    // the last test record's flaky tests
	Findings     []runner.Finding `json:"findings,omitempty"` // the last review's findings
	AgentMessage string           `json:"agent_message,omitempty"`
	LogTail      []string         `json:"log_tail,omitempty"`
	ReportPath   string           `json:"report_path"`
	// DraftNote is a line about the run's draft PR when it needs one: left
	// stale by a run that died, or an ordinary PR marked [DRAFT] because
	// the host has no drafts. Computed against the time of the call.
	DraftNote string `json:"draft_note,omitempty"`
	// FollowUp is what a follow-up acted on: the record's block, else the
	// PR and previous run its task names. CommentsPath is where the
	// comments it was given are stored. Both are absent for a first run.
	// Not to be confused with row.follow_up, the row's bool saying
	// whether the run is a follow-up at all.
	FollowUp     *runstore.FollowUp `json:"follow_up,omitempty"`
	CommentsPath string             `json:"comments_path,omitempty"`
	// Route is the model spend by provider route (a Claude call has none);
	// ModelBy is the spend by model. Both are the run's own record.
	Route   map[string]float64 `json:"route_by,omitempty"`
	ModelBy map[string]float64 `json:"model_by,omitempty"`
	// Reported is what providers said the run's calls cost, beside Charge,
	// what the table said (the charge always settles). Zero when no
	// provider reported one.
	Reported float64 `json:"reported_usd,omitempty"`
	Charge   float64 `json:"charge_usd,omitempty"`
	// PinWarnings are the model prices the run's cap counted on a guess:
	// an unverified placeholder, or cache rates left at 0.
	PinWarnings []string `json:"pin_warnings,omitempty"`
}

type diagnoseOptions struct {
	cloud  cloudOptions
	asJSON bool
}

func newDiagnoseCmd() *cobra.Command {
	var o diagnoseOptions
	cmd := &cobra.Command{
		Use:   "diagnose RUN",
		Short: "Explain how a run went: status, tests, review findings, the agent's last word, logs",
		Long: "Gather what is known about RUN (<repo-slug>/<run-id>, or a bare run ID): its\n" +
			"status and reason, the last test run's failed and flaky tests, the last\n" +
			"review's findings, the agent's final message, the newest log lines, its cost\n" +
			"and its pull request. Everything printed is redacted of the credentials in\n" +
			"this environment, on top of the runner's own redaction.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runDiagnose(cmd, &o, args[0]) },
	}
	cmd.Flags().BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func runDiagnose(cmd *cobra.Command, o *diagnoseOptions, ref string) error {
	ctx := cmd.Context()
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()
	// Not locateLaunched: a run that never launched still has a row that
	// explains it; it just has no logs.
	slug, id, err := locateRun(ctx, env, ref)
	if err != nil {
		return err
	}
	s := runstore.Open(env.bucket.Bucket, slug, id)
	l, err := ownerLaunch(ctx, env, s, id)
	var refused error // an execution checkExecution won't follow
	switch {
	case corruptObject(err):
		l, err = nil, nil // the row says which object is unreadable
	case errors.Is(err, errNotFollowing):
		// The row says why, as in ls; exit 1 afterwards, since the run's
		// problem is one the user sees and may act on (--region).
		l, refused, err = nil, err, nil
	}
	if err != nil {
		return err
	}
	if l != nil && l.Execution == "" {
		l = nil
	}
	d, err := diagnose(ctx, env, s, l, slug+"/"+id, cliSecrets(os.Getenv), cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	d.Project = env.lc.Name
	if err := printDiagnosis(cmd.OutOrStdout(), d, o.asJSON); err != nil {
		return err
	}
	if refused != nil {
		return &ExitError{Code: ExitUserError, Err: errors.Unwrap(refused)}
	}
	return nil
}

// diagnose gathers run's diagnosis; l is its launch, nil when it never
// launched (no logs then). A failed log read is a warning: the rest of the
// diagnosis is still worth having.
func diagnose(ctx context.Context, env *cloudEnv, s *runstore.Store, l *runstore.Launch, run string, secrets []string, warn io.Writer) (*Diagnosis, error) {
	red := agent.RedactFunc(secrets)
	rows, err := loadRows(ctx, env, lsFilter{runRef: run, warn: warn}, time.Now())
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, remote(fmt.Errorf("run %s: expected one row, got %d", run, len(rows)))
	}
	d := &Diagnosis{Row: rows[0], ReportPath: s.Prefix() + "report.md"}
	d.DraftNote = draftNote(d.Row, time.Now())
	d.Row.Reason = red(d.Row.Reason)
	if h := d.Row.Halt; h != nil {
		c := *h
		c.Detail = red(c.Detail)
		d.Row.Halt, d.Halt = &c, &c
	}

	rec, err := absent(s.ReadRecord(ctx))
	if corruptObject(err) {
		rec, err = nil, nil // the row already says so
	}
	if err != nil {
		return nil, remote(err)
	}
	d.FollowUp = followUpOf(d.Row, rec, red)
	routeOf(d, rec, env.lc, red)
	if d.FollowUp != nil {
		d.CommentsPath = s.Prefix() + "comments.json"
	}
	if rec != nil {
		d.Verify = rec.Verify
		for i := range d.Verify {
			v := &d.Verify[i]
			v.Warning, v.Failed, v.Flaky = red(v.Warning), redactAll(v.Failed, red), redactAll(v.Flaky, red)
		}
		if t, ok := lastTest(d.Verify); ok {
			d.Failed, d.Flaky = t.Failed, t.Flaky
		}
		if n := len(rec.Reviews); n > 0 {
			if res, ok := transcriptResult(ctx, s, "review-"+strconv.Itoa(n)); ok {
				for _, f := range runner.ParseVerdict(res).Findings {
					d.Findings = append(d.Findings, runner.Finding{Severity: red(f.Severity), File: red(f.File), Summary: red(f.Summary)})
				}
			}
		}
		d.AgentMessage = logtail.Clip(red(agentMessage(ctx, s, rec)), agentMessageBytes)
	}

	if l == nil {
		return d, nil
	}
	// A ring of the newest lines: the read runs oldest first.
	tail := make([]string, diagnoseLogLines)
	seen := 0
	err = env.be.Logs(ctx, logQuery(l, false), func(e backend.LogEntry) error {
		tail[seen%diagnoseLogLines] = humanLogLine(redactEntry(e, red))
		seen++
		return nil
	})
	if err != nil {
		// Deliberately not exit 2, unlike every other GCP API error: the
		// log tail is the one optional part of a diagnosis. The row, tests,
		// findings and agent message all come from the bucket and matter
		// most when a run failed, which is also when Cloud Logging may be
		// unreachable or lack the caller's permission. fugaro logs, whose
		// whole job is the logs, still fails with exit 2.
		fmt.Fprintf(warn, "warning: reading the logs: %s\n", oneLine(red(err.Error())))
	} else if seen == 0 {
		emptyViewHint(warn, env)
	}
	for i := max(0, seen-diagnoseLogLines); i < seen; i++ {
		d.LogTail = append(d.LogTail, tail[i%diagnoseLogLines])
	}
	return d, nil
}

// routeOf fills d's route, reported-vs-charge and pin warnings from the
// record's cost: the models the run paid for are its pins. The warnings use
// the price table with the project's overrides, as the run's cap did.
func routeOf(d *Diagnosis, rec *runstore.Record, lc *localcfg.Config, red func(string) string) {
	if rec == nil || rec.Cost == nil {
		return
	}
	c := rec.Cost
	d.Route, d.ModelBy = redactKeys(c.RouteBy, red), redactKeys(c.ModelBy, red)
	if c.ReportedUSD > 0 {
		d.Reported, d.Charge = c.ReportedUSD, c.ModelUSD
	}
	prices := pricing.Embedded()
	if lc != nil {
		if ov, err := lc.Overrides(); err == nil {
			if t, err := prices.With(ov); err == nil {
				prices = t
			}
		}
	}
	for _, id := range slices.Sorted(maps.Keys(c.ModelBy)) {
		for _, w := range config.PinWarnings(config.Agent{Model: id}, prices) {
			// The one model fills every role; say it once.
			if m := red(w.Message); !slices.Contains(d.PinWarnings, m) {
				d.PinWarnings = append(d.PinWarnings, m)
			}
		}
	}
}

func redactKeys(m map[string]float64, red func(string) string) map[string]float64 {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]float64, len(m))
	for k, v := range m {
		out[red(k)] += v
	}
	return out
}

// draftNote is diagnose's line about the run's draft PR, "" when there is
// nothing to add. A stale draft is the one a run that died left behind: the
// history sweeper has no git credentials and never edits a PR, so its
// description still says "Running".
func draftNote(row runview.Row, now time.Time) string {
	var notes []string
	if row.StaleDraft && row.PR > 0 {
		last := "never updated"
		if row.PRStatusAt != nil {
			last = "last updated " + age(now.Sub(*row.PRStatusAt)) + " ago"
		}
		notes = append(notes, fmt.Sprintf("draft PR #%d %s; the run may have crashed (its description still says Running; continue with fugaro run --pr %d)", row.PR, last, row.PR))
	}
	if row.DraftFallback && row.PR > 0 {
		notes = append(notes, fmt.Sprintf("PR #%d is an ordinary pull request marked [DRAFT] in its title: the host has no draft pull requests", row.PR))
	}
	return strings.Join(notes, "; ")
}

// followUpOf is a run's follow-up block, redacted: its record's, else
// the pull request and previous run its task names (row's); nil for a
// first run.
func followUpOf(row runview.Row, rec *runstore.Record, red func(string) string) *runstore.FollowUp {
	var fu runstore.FollowUp
	switch {
	case rec != nil && rec.FollowUp != nil:
		fu = *rec.FollowUp
	case row.FollowUp && row.TaskPR > 0:
		fu = runstore.FollowUp{PR: row.TaskPR, PreviousRun: row.PreviousRun}
	default:
		return nil
	}
	fu.PreviousRun, fu.Session, fu.SessionNote = red(fu.PreviousRun), red(fu.Session), red(fu.SessionNote)
	if fu.Authors != nil {
		authors := make(map[string]int, len(fu.Authors))
		for name, n := range fu.Authors {
			authors[red(name)] += n
		}
		fu.Authors = authors
	}
	fu.UntrustedAuthors = redactAll(fu.UntrustedAuthors, red)
	if fu.Omitted != nil {
		omitted := make(map[string]int, len(fu.Omitted))
		for reason, n := range fu.Omitted {
			omitted[red(reason)] += n
		}
		fu.Omitted = omitted
	}
	return &fu
}

// followUpLine is diagnose's one line about a follow-up: the PR and the
// run it continued, the session, and whose comments steered it.
func followUpLine(fu *runstore.FollowUp) string {
	line := fmt.Sprintf("follow-up of PR #%d after %s", fu.PR, fu.PreviousRun)
	if fu.Session == "" {
		return line // not started yet: the task is all there is
	}
	line += "; session " + fu.Session
	if fu.SessionNote != "" {
		line += " (" + fu.SessionNote + ")"
	}
	noun := "comments"
	if fu.Comments == 1 {
		noun = "comment"
	}
	line += fmt.Sprintf("; %d %s", fu.Comments, noun)
	if len(fu.Authors) > 0 {
		names := slices.Sorted(maps.Keys(fu.Authors))
		for i, name := range names {
			names[i] = fmt.Sprintf("%s (%d)", name, fu.Authors[name])
		}
		line += " from " + strings.Join(names, ", ")
	}
	if n := max(fu.UntrustedAuthorCount, len(fu.UntrustedAuthors)); n > 0 {
		names := strings.Join(fu.UntrustedAuthors, ", ")
		if more := n - len(fu.UntrustedAuthors); more > 0 {
			names += fmt.Sprintf(", and %d more", more)
		}
		line += fmt.Sprintf(" (%d untrusted: %s)", n, strings.TrimPrefix(names, ", "))
	}
	return line
}

// lastTest is the last test record of recs.
func lastTest(recs []verify.Record) (verify.Record, bool) {
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].Kind == verify.KindTest {
			return recs[i], true
		}
	}
	return verify.Record{}, false
}

// agentMessage is the result text of the last implement or fix transcript:
// fix-<n> down to fix-1, n counted from rec.Stages, then implement-1.
func agentMessage(ctx context.Context, s *runstore.Store, rec *runstore.Record) string {
	fixes := 0
	for _, st := range rec.Stages {
		if st.Name == "fix" {
			fixes++
		}
	}
	for n := fixes; n >= 1; n-- {
		if res, ok := transcriptResult(ctx, s, "fix-"+strconv.Itoa(n)); ok {
			return res.Text
		}
	}
	if res, ok := transcriptResult(ctx, s, "implement-1"); ok {
		return res.Text
	}
	return ""
}

// transcriptResult is the result event of transcripts/<name>.jsonl, if any.
func transcriptResult(ctx context.Context, s *runstore.Store, name string) (agent.Result, bool) {
	data, err := s.ReadFile(ctx, "transcripts/"+name+".jsonl")
	if err != nil {
		return agent.Result{}, false
	}
	res, found, err := agent.ParseStream(bytes.NewReader(data), nil)
	return res, found && err == nil
}

func redactAll(ss []string, red func(string) string) []string {
	if ss == nil {
		return nil
	}
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = red(s)
	}
	return out
}

// printDiagnosis prints d as JSON, or as a short labelled section per item.
func printDiagnosis(w io.Writer, d *Diagnosis, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(d)
	}
	// Every string from the run goes through oneLine or multiLine, since
	// the run can put terminal controls in it.
	var b strings.Builder
	r := d.Row
	fmt.Fprintf(&b, "Run:      %s\n", oneLine(r.Run))
	status := r.Status
	if r.Stage != "" {
		status += " (stage " + r.Stage + ")"
	}
	fmt.Fprintf(&b, "Status:   %s\n", oneLine(status))
	if r.Reason != "" {
		fmt.Fprintf(&b, "Reason:   %s\n", oneLine(r.Reason))
	}
	if h := d.Halt; h != nil {
		line := fmt.Sprintf("Halted:   %s (%s) at %s", h.Reason, h.Scope, h.At.UTC().Format(time.RFC3339))
		if h.Detail != "" {
			line += ": " + h.Detail
		}
		fmt.Fprintf(&b, "%s\n", oneLine(line))
	}
	fmt.Fprintf(&b, "%s\n", oneLine(strings.ReplaceAll(runner.CostLine(r.Cost), "**", "")))
	if len(d.Route) > 0 {
		fmt.Fprintf(&b, "Route:    %s\n", oneLine(spendList(d.Route)))
	}
	if d.Reported > 0 {
		fmt.Fprintf(&b, "Reported: providers said $%.4f; charged $%.4f (the table price settles)\n", d.Reported, d.Charge)
	}
	for _, w := range d.PinWarnings {
		fmt.Fprintf(&b, "Warning:  %s\n", oneLine(w))
	}
	if r.PRURL != "" {
		fmt.Fprintf(&b, "PR:       %s\n", oneLine(prColumnURL(r)))
	}
	if d.DraftNote != "" {
		fmt.Fprintf(&b, "Draft:    %s\n", oneLine(d.DraftNote))
	}
	if d.FollowUp != nil {
		fmt.Fprintf(&b, "%s\n", oneLine(followUpLine(d.FollowUp)))
	}
	if r.LogURL != "" {
		fmt.Fprintf(&b, "Logs:     %s\n", oneLine(r.LogURL))
	}
	fmt.Fprintf(&b, "Report:   %s\n", oneLine(d.ReportPath))
	if t, ok := lastTest(d.Verify); ok {
		fmt.Fprintf(&b, "\nTests\n  %s\n", oneLine(t.Summary()))
		for _, n := range d.Failed {
			fmt.Fprintf(&b, "  failed: %s\n", oneLine(n))
		}
		for _, n := range d.Flaky {
			fmt.Fprintf(&b, "  flaky:  %s\n", oneLine(n))
		}
	}
	if len(d.Findings) > 0 {
		fmt.Fprintf(&b, "\nFindings (last review)\n")
		for _, f := range d.Findings {
			loc := ""
			if f.File != "" {
				loc = " " + oneLine(f.File) + ":"
			}
			fmt.Fprintf(&b, "  [%s]%s %s\n", oneLine(f.Severity), loc, oneLine(f.Summary))
		}
	}
	if d.AgentMessage != "" {
		fmt.Fprintf(&b, "\nAgent's final message\n%s\n", indent(multiLine(d.AgentMessage)))
	}
	if len(d.LogTail) > 0 {
		fmt.Fprintf(&b, "\nLast %d log lines\n%s\n", len(d.LogTail), indent(multiLine(strings.Join(d.LogTail, "\n"))))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// spendList is "name $x, name $y", biggest first.
func spendList(m map[string]float64) string {
	names := slices.SortedFunc(maps.Keys(m), func(a, b string) int {
		if m[a] != m[b] {
			if m[a] > m[b] {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	})
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = fmt.Sprintf("%s $%.4f", n, m[n])
	}
	return strings.Join(parts, ", ")
}

// prColumnURL is the PR's URL with ls's draft marker.
func prColumnURL(r runview.Row) string {
	col := prColumn(runview.Row{PRURL: r.PRURL, Outcome: r.Outcome, Status: r.Status, FollowUp: r.FollowUp, RecordPR: r.RecordPR, StaleDraft: r.StaleDraft})
	return col
}

// indent prefixes every line of s with two spaces.
func indent(s string) string {
	return "  " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n  ")
}
