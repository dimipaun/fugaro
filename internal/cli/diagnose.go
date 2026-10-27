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
	"github.com/dimipaun/fugaro/internal/rigor"
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
	// diagnoseStages bounds the stages a diagnosis prints. It sits above the
	// loop's own maximum -- 1 + 2*F + R*(2 + 2*F) stages, at most 87 under
	// config.MaxFirstLineRounds and recipe.MaxReviewRounds, plus the provision
	// and render stages (docs/design/generic-tool.md §3.1) -- because the
	// identity Stages documents holds only for the whole list: a bound that
	// bit a real run would make sum(stages[].model_usd) + model_by's
	// unattributed entry miss row.cost.model_usd by the rounds it dropped. It
	// guards against the record a hand can edit, not against the loop.
	diagnoseStages = 128
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
	// Tolerated is the findings the run's resolved mode let through: the
	// record's own block (T3.4), each entry's score, the mode that tolerated
	// it, the label the score derives and the reviewer's clipped line for it.
	// It is a different fact from Findings above, which is the last review
	// whether or not the mode blocked it. Absent when the record carries no
	// block, which is every run that tolerated nothing, and every record
	// written before the field existed.
	Tolerated    *runstore.ToleratedFindings `json:"tolerated,omitempty"`
	AgentMessage string                      `json:"agent_message,omitempty"`
	LogTail      []string         `json:"log_tail,omitempty"`
	ReportPath   string           `json:"report_path"`
	// ArtifactPaths are where the run's artifacts live in the runs bucket:
	// the run prefix followed by artifacts/<name>, the same shape ReportPath
	// has, so `gcloud storage cp` can take one directly. The record's own
	// list, never a bucket listing (design artifact-runs.md §9), and every
	// path is redacted and escaped before it is printed.
	ArtifactPaths []string `json:"artifact_paths,omitempty"`
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
	// Stages is the run's per-stage record (runstore.StageTiming): what each
	// stage cost in model dollars and, when something counted them, how many
	// model calls and sub-agent tool calls it made. In the record's own order
	// and bounded at diagnoseStages, with every string redacted. Calls and
	// Agents stay pointers, so a stage nobody counted is absent and a stage
	// counted at zero is not: the two are different facts. Absent when the
	// record has no stages, and when there is no record at all.
	//
	// The stages are the run's whole model spend but for what settled after
	// its stage's wait ran out, which is in ModelBy["unattributed"]: exactly,
	// sum(stages[].model_usd) + model_by["unattributed"] ==
	// row.cost.model_usd.
	Stages []runstore.StageTiming `json:"stages,omitempty"`
	// Reported is what providers said the run's calls cost, beside Charge,
	// what the table said for the provider routes' calls (the charge
	// always settles; Claude's spend is not in it). Zero when no provider
	// reported one. ReportedWarning is set when the two differ by more than
	// reportedGapPct percent.
	Reported        float64 `json:"reported_usd,omitempty"`
	Charge          float64 `json:"charge_usd,omitempty"`
	ReportedWarning string  `json:"reported_warning,omitempty"`
	// PinWarnings are the model prices the run's cap counted on a guess:
	// an unverified placeholder, or cache rates left at 0.
	PinWarnings []string `json:"pin_warnings,omitempty"`
	// Tolerated is the findings the run's resolved mode let through
	// (runstore.ToleratedFindings), in the reviewer's order, bounded again on
	// read and with every title redacted. Absent -- never an empty block --
	// for a run that tolerated nothing, and for every record written before
	// the field existed.
	Tolerated *runstore.ToleratedFindings `json:"tolerated,omitempty"`
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
	l, err := viewLaunch(ctx, env, s, id)
	var refused error // an execution checkExecution won't follow
	switch {
	case corruptObject(err):
		l, err = nil, nil // the row says which object is unreadable
	case errors.Is(err, errExecutionGone):
		// The row and the report are still the run's own; only the logs,
		// which nothing can tie to the run any more, are left out.
		fmt.Fprintf(cmd.ErrOrStderr(), "note: %v\n", err)
		l, err = nil, nil
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
		// An artifact run has no pull request, so its deliverable is named
		// here: one bucket path per artifact the record lists, in the
		// record's own order. A record that predates the field lists none,
		// and a run that opens a pull request never has any.
		for _, a := range rec.Artifacts {
			d.ArtifactPaths = append(d.ArtifactPaths, s.Prefix()+runstore.ArtifactPrefix+red(a.Name))
		}
		d.Stages, d.Tolerated = stageTimingsOf(rec.Stages, red), toleratedOf(rec.Tolerated, red)
		d.Verify = rec.Verify
		for i := range d.Verify {
			v := &d.Verify[i]
			v.Warning, v.Failed, v.Flaky = red(v.Warning), redactAll(v.Failed, red), redactAll(v.Flaky, red)
		}
		if t, ok := lastTest(d.Verify); ok {
			d.Failed, d.Flaky = t.Failed, t.Flaky
		}
		if n := seniorRound(rec.Reviews); n > 0 {
			if res, ok := transcriptResult(ctx, s, "review-"+strconv.Itoa(n)); ok {
				for _, f := range runner.ParseVerdict(res).Findings {
					// The score travels with the finding when the transcript
					// carries one (D3's four optional properties) and stays
					// absent when it does not, so a transcript from before the
					// score existed renders exactly as it did.
					d.Findings = append(d.Findings, runner.Finding{
						Severity: red(f.Severity), File: red(f.File), Summary: red(f.Summary),
						Likelihood: f.Likelihood, Impact: f.Impact, FixCost: f.FixCost, Class: red(f.Class),
					})
				}
			}
		}
		d.Tolerated = toleratedOf(rec.Tolerated, red)
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
		emptyViewHint(warn, env, l.LogURL)
	}
	for i := max(0, seen-diagnoseLogLines); i < seen; i++ {
		d.LogTail = append(d.LogTail, tail[i%diagnoseLogLines])
	}
	return d, nil
}

// stageTimingsOf is the record's stages for the document: the record's own
// order, bounded at diagnoseStages, and redacted like every other string here.
// nil for a record with none, so the key is absent rather than an empty list.
func stageTimingsOf(stages []runstore.StageTiming, red func(string) string) []runstore.StageTiming {
	if len(stages) == 0 {
		return nil
	}
	out := make([]runstore.StageTiming, 0, min(len(stages), diagnoseStages))
	for _, s := range stages {
		if len(out) == diagnoseStages {
			break
		}
		s.Name, s.Model = red(s.Name), red(s.Model)
		out = append(out, s)
	}
	return out
}

// toleratedOf is the record's tolerated findings for the document: bounded
// again on read, because the record is an object a hand can edit and the
// write-side bound (runstore.NewTolerated) is not one a reader can count on,
// and with every string redacted -- the enums too, which the write path
// validates to its vocabulary and a hand-edited record need not. nil for a run
// that tolerated nothing -- no block, or a block that lists none -- so the key
// is absent.
func toleratedOf(t *runstore.ToleratedFindings, red func(string) string) *runstore.ToleratedFindings {
	if t == nil || len(t.Findings) == 0 {
		return nil
	}
	out := &runstore.ToleratedFindings{Findings: make([]runstore.ToleratedFinding, 0, min(len(t.Findings), runstore.MaxTolerated))}
	for _, f := range t.Findings {
		if len(out.Findings) == runstore.MaxTolerated {
			break
		}
		f.Title = red(f.Title)
		f.Mode, f.Label, f.Class = rigor.Mode(red(string(f.Mode))), rigor.Label(red(string(f.Label))), rigor.Class(red(string(f.Class)))
		out.Findings = append(out.Findings, f)
	}
	// The block's total is how many findings the mode really tolerated, and a
	// record claiming fewer than it lists is not one to print as written.
	out.Total = max(t.Total, len(out.Findings))
	return out
}

// seniorRound is the round of the last senior review (not a first-line one,
// whose transcripts are named review_first-N), 0 when there is none. The
// senior transcript is review-<round>.
func seniorRound(rs []runstore.ReviewSummary) int {
	for i := len(rs) - 1; i >= 0; i-- {
		if rs[i].Tier != runstore.TierFirst {
			return rs[i].Round
		}
	}
	return 0
}

// reportedGapPct is how far above Fugaro's charge (without the route fee) a
// provider's reported cost may be before diagnose warns.
const reportedGapPct = 10

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
		// The provider's charge is the routes' spend; the run's ModelUSD also
		// holds Claude's, and "unattributed" may be a Claude call's.
		// The provider reports the model's price, not Fugaro's route fee, so
		// the comparison is against the charge without the fee.
		var charge, bare float64
		for r, v := range c.RouteBy {
			if r == "unattributed" {
				continue
			}
			charge += v
			fee := 0.0
			if lc != nil {
				fee = lc.Providers[r].RouteFeePct
			}
			m, _ := pricing.FromUSD(v)
			bare += pricing.WithoutFee(m, fee).USD()
		}
		d.Reported, d.Charge = c.ReportedUSD, charge
		// Only a report above the charge is a sign of under-pricing: a lower
		// one may be partial reporting (a cut stream, a call without a cost).
		if d.Reported > bare*(1+reportedGapPct/100.0) {
			// Two answers, because a catalog-priced route has two: the
			// owner's own price, or the built-in catalog's cheapest end
			// while OpenRouter served a dearer endpoint (design §7, D12).
			d.ReportedWarning = fmt.Sprintf("the providers reported $%.4f, more than %d%% above the $%.4f the table charged without the route fee: model_prices may be too low, or a catalog-priced route was charged its cheapest end while a dearer endpoint served the call — set price_policy: highest, or price the model under model_prices (partial reporting is also possible); check model_prices and the account's provider settings",
				d.Reported, reportedGapPct, bare)
		}
	}
	// The table the run charges from, built the same way (the catalog's rows
	// included). A table that cannot be built leaves the pin warnings out
	// (a nil table looks nothing up): they would be about a table no run
	// would use.
	prices, _, err := priceTable(lc, budgetNow())
	if err != nil {
		prices = nil
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

// toleratedOf is the record's tolerated block as the diagnosis carries it: the
// same block, with every string in it redacted. The record is job-writable, so
// a title is the reviewer's own words for a finding and text this process did
// not write — it is clipped to the length the record itself bounds it to
// before it is printed, since a record no writer of this repository produced
// may carry a longer one. The score and the label are printed as recorded: the
// label is derived from the axes when the record is written (D2), so diagnose
// reads back one opinion rather than making a second one.
func toleratedOf(t *runstore.ToleratedFindings, red func(string) string) *runstore.ToleratedFindings {
	if t == nil {
		return nil
	}
	out := &runstore.ToleratedFindings{Total: t.Total, Findings: make([]runstore.ToleratedFinding, 0, len(t.Findings))}
	for _, f := range t.Findings {
		f.Title = clipRunes(red(f.Title), runstore.MaxToleratedTitle)
		f.Label = rigor.Label(red(string(f.Label)))
		f.Mode = rigor.Mode(red(string(f.Mode)))
		f.Class = rigor.Class(red(string(f.Class)))
		out.Findings = append(out.Findings, f)
	}
	return out
}

// scoreCell is a finding's score as diagnose prints it beside the finding's
// own row, "" for one the reviewer left unscored — which is what keeps a
// transcript from before the score existed rendering exactly as it did. Every
// part is named rather than printed as a tuple of numbers: the axes and the
// class are what a reader checks a mode's threshold against (D2, D3), and a
// bare tuple leaves the reader to remember which number was which.
func scoreCell(likelihood, impact, fixCost int, class string) string {
	s := rigor.Score{Likelihood: likelihood, Impact: impact, FixCost: fixCost, Class: rigor.Class(class)}
	if !s.Scored() {
		return ""
	}
	return fmt.Sprintf(" (likelihood %d, impact %d, fix cost %d, class %s)", s.Likelihood, s.Impact, s.FixCost, oneLine(class))
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
	// A status derived at read time carries what was derived in the same
	// parentheses runs ls's table uses, so the one-run read says what happened
	// to the run rather than leaving the reader to notice the reason line
	// (runview.DerivedPreempted). Display only: the row's status stays the
	// vocabulary it was read as.
	if r.Derived != "" {
		status += " (" + r.Derived + ")"
	}
	if r.Stage != "" {
		status += " (stage " + r.Stage + ")"
	}
	fmt.Fprintf(&b, "Status:   %s\n", oneLine(status))
	if r.Reason != "" {
		fmt.Fprintf(&b, "Reason:   %s\n", oneLine(r.Reason))
	}
	if acct := r.OAuthAccount; acct != "" && acct != "default" {
		fmt.Fprintf(&b, "Account:  %s\n", oneLine(clipRunes(acct, maxNameCellRunes)))
	}
	if pl := r.ProjectLayer; pl != nil && validGeneration(pl.Generation) {
		line := fmt.Sprintf("Config:   project layer generation %d (sha256 %s)", pl.Generation, shortSHA(pl.SHA256))
		if !pl.Applied {
			line += ", not applied"
		}
		if r.ConfigSHA256 != "" {
			line += "; resolved sha256 " + shortSHA(r.ConfigSHA256)
		}
		fmt.Fprintf(&b, "%s\n", oneLine(line))
	} else if r.ConfigSHA256 != "" {
		fmt.Fprintf(&b, "Config:   no project layer; resolved sha256 %s\n", oneLine(shortSHA(r.ConfigSHA256)))
	}
	// The mode the run resolved and the layer that decided it (docs/rigor-modes
	// D24), right after the config it was read from: the two lines are the
	// same story, and the revision is where the mode's half of it came from
	// (D25: the base branch, which for a first run is not the branch the rest
	// of the config came from).
	if r.Rigor != nil {
		fmt.Fprintf(&b, "Rigor:    %s\n", oneLine(r.Rigor.Line()))
	}
	if h := d.Halt; h != nil {
		line := fmt.Sprintf("Halted:   %s (%s) at %s", h.Reason, h.Scope, h.At.UTC().Format(time.RFC3339))
		if h.Detail != "" {
			line += ": " + h.Detail
		}
		fmt.Fprintf(&b, "%s\n", oneLine(line))
	}
	for _, ln := range costLines(r.Cost) {
		fmt.Fprintf(&b, "%s\n", ln)
	}
	// The run's prompt-cache token totals, printed only when a gateway
	// recorded one: a run that read nothing from cache and wrote nothing to
	// it -- no gateway, or one whose calls all missed and cached nothing --
	// keeps the block it always had (P12).
	if r.Cost.CacheReadTokens != 0 || r.Cost.CacheWriteTokens != 0 {
		fmt.Fprintf(&b, "Cache:    %d read, %d write (tokens)\n", r.Cost.CacheReadTokens, r.Cost.CacheWriteTokens)
	}
	if r.Cost.CatalogStale {
		// The run charged, and over-counted on purpose: the catalog it
		// priced from was past its own 30 days (design §7, P7).
		fmt.Fprintf(&b, "Catalog:  the built-in provider price catalog was stale, so every model it priced was charged the dearest end of its tier's range (regenerate it with scripts/gen-price-catalog.sh)\n")
	}
	if len(d.Route) > 0 {
		fmt.Fprintf(&b, "Route:    %s\n", oneLine(spendList(d.Route)))
	}
	if d.Reported > 0 {
		fmt.Fprintf(&b, "Reported: providers said $%.4f; charged $%.4f (the table price settles)\n", d.Reported, d.Charge)
	}
	for _, w := range d.PinWarnings {
		fmt.Fprintf(&b, "Warning:  %s\n", oneLine(w))
	}
	if d.ReportedWarning != "" {
		fmt.Fprintf(&b, "Warning:  %s\n", oneLine(d.ReportedWarning))
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
	if len(d.ArtifactPaths) > 0 {
		fmt.Fprintf(&b, "\nArtifacts\n")
		for _, p := range d.ArtifactPaths {
			fmt.Fprintf(&b, "  %s\n", oneLine(p))
		}
	}
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
			fmt.Fprintf(&b, "  [%s]%s%s %s\n", oneLine(f.Severity),
				scoreCell(f.Likelihood, f.Impact, f.FixCost, f.Class), loc, oneLine(f.Summary))
		}
	}
	// The findings the resolved mode let through (T4.1), under the review
	// they came from: what the run left open, and the score and the mode that
	// left it open. One block per mode, in the order the modes appear: a run
	// resolves one mode (D9), so this is one block, and a record carrying two
	// reads as two rather than as whichever happened to be listed first.
	if t := d.Tolerated; t != nil && len(t.Findings) > 0 {
		var modes []string
		byMode := map[string][]runstore.ToleratedFinding{}
		for _, f := range t.Findings {
			m := oneLine(string(f.Mode))
			if _, ok := byMode[m]; !ok {
				modes = append(modes, m)
			}
			byMode[m] = append(byMode[m], f)
		}
		for _, mode := range modes {
			fmt.Fprintf(&b, "\nTolerated by %s\n", mode)
			for _, f := range byMode[mode] {
				fmt.Fprintf(&b, "  [%s]%s %s\n", oneLine(string(f.Label)),
					scoreCell(f.Likelihood, f.Impact, f.FixCost, string(f.Class)), oneLine(f.Title))
			}
		}
		// The record keeps the first runstore.MaxTolerated findings and its
		// total is what the mode really tolerated, so a list shorter than its
		// total says so rather than reading as the whole of it (F19).
		if t.Total > len(t.Findings) {
			fmt.Fprintf(&b, "  showing %d of %d tolerated findings (the record keeps the first %d)\n",
				len(t.Findings), t.Total, runstore.MaxTolerated)
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
