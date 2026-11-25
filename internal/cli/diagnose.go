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
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"gocloud.dev/blob"

	"github.com/spf13/cobra"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/logtail"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runmsg"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/task"
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
	// and render stages (docs/generic-tool/spec.md §3.1) -- because the
	// identity Stages documents holds only for the whole list: a bound that
	// bit a real run would make sum(stages[].model_usd) + model_by's
	// unattributed entry miss row.cost.model_usd by the rounds it dropped. It
	// guards against the record a hand can edit, not against the loop.
	diagnoseStages = 128
	// diagnoseReviews bounds the review rounds a diagnosis prints. It sits
	// above the loop's own maximum -- F + R*(1+F) rounds on the senior level
	// and as many again on the adversarial one, 77 at most under
	// config.MaxFirstLineRounds and recipe.MaxReviewRounds -- so it guards
	// against the record a hand can edit and never against the loop. A list
	// longer than this prints the first diagnoseReviews rounds and then the
	// line "… at most 96 review rounds are printed", which says the list is
	// cut without claiming how many rounds it left out: the count is not one
	// the reader of a cut list can know.
	diagnoseReviews = 96
	// diagnoseSwitches bounds the switch log a diagnosis prints. It sits above
	// any honest run's log -- the gateway settles at most one switch per call,
	// so a log longer than the stages it was settled in is already a record a
	// hand wrote -- and it guards against the record a hand can edit, not
	// against the loop. Unlike the stages, no identity here holds only for the
	// whole list: a switch is a record of what the run did, and no figure on
	// this screen is summed from one.
	diagnoseSwitches = 128
)

// Diagnosis is what diagnose gathers about one run. Every string in it
// that comes from the run is redacted.
type Diagnosis struct {
	Project string      `json:"project"` // the Fugaro project
	Row     runview.Row `json:"row"`
	// FailureClass is why the run did not end succeeded, as one value of the
	// closed vocabulary of docs/failure-classes/design.md §3 — the same value
	// the row carries, on the diagnosis's own shape because diagnose is the
	// command a person runs to be told why, and D1460 is that a reader of this
	// document is never sent into the row for it. Absent for a run that did
	// not fail, and for a row the read side ended itself — a preemption, a
	// lost execution, or an `error` for a run it could not follow — where the
	// row's own account of it is `derived`, or that sentence
	// (runview.Row.FailureClass).
	FailureClass failure.Class  `json:"failure_class,omitempty"`
	Halt         *runstore.Halt `json:"halt,omitempty"` // why a halted run was halted
	// ChainLimits is the chain the run belongs to and what is left of its
	// limits (D1055), filled in for a halted run: a halt's first question is
	// how much is left, which is what this block answers. Embedded, so the
	// members are the ones `runs show --json` prints and the two documents
	// cannot spell them differently (D1056). Absent -- never an empty block --
	// for a run that did not halt, and for one whose chain could not be read,
	// where the warning says so and the rest of the diagnosis stands.
	*ChainLimits
	// Messages is the run's message channel (docs/run-messages F13, F18-F20):
	// the block the run's record carries, cross-checked against the message
	// objects still under tokens/<slug>/<run>/messages/, so a message the
	// record never mentions is shown as one the run never read. Absent -- never
	// an empty block -- for a run with neither a block nor an object there,
	// which is every run whose runner predates the channel. The fates are the
	// run's own account of itself and are labelled as such: the record is
	// written by the run, whose agent can rewrite it (F20), and the objects are
	// the one part of a message it cannot write (D2941).
	Messages     *MessagesDoc     `json:"messages,omitempty"`
	Verify       []verify.Record  `json:"verify,omitempty"`   // result.json's verify records
	Failed       []string         `json:"failed,omitempty"`   // the last test record's failed tests
	Flaky        []string         `json:"flaky,omitempty"`    // the last test record's flaky tests
	Findings     []runner.Finding `json:"findings,omitempty"` // the last review's findings
	AgentMessage string           `json:"agent_message,omitempty"`
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
	// Switches is the record's switch log (runstore.Record.Switches, C1.16,
	// D140): every call the credentials service moved off the credential it was
	// on, as the credential it left, the one that answered and the service's
	// reason word, with the cache-miss estimate the gateway priced beside it
	// (F24, runstore.Switch.CacheMissUSD). In the record's own order, which is
	// the order the gateway settled the calls in, bounded at diagnoseSwitches
	// and with every string redacted: from, to and reason are names and words
	// the service wrote, which a hand can edit like any other byte of the
	// record. Absent -- never an empty list -- for a run whose calls the service
	// never moved, which is every run whose record predates the member.
	//
	// It is the log the console's Run screen draws a fallback from (D3188),
	// which is why it is here rather than left in the record: the screen reads
	// this document and no other, and the console adds no reader of its own.
	Switches []runstore.Switch `json:"switches,omitempty"`
	// Retries is the record's own count of attempts the run threw away and
	// ran again because the model's stream was closed before its response
	// finished (runstore.Record.Retries, issue #1827): why a stage's share of
	// the run's duration and cost is larger than one pass over it. Zero for a
	// run that retried nothing, which is every run whose record predates the
	// field, so the key is absent rather than a zero.
	Retries int `json:"retries,omitempty"`
	// Reviews is the record's review rounds (runstore.ReviewSummary): the
	// round, the tier, the verdict and the findings count, and what reviewed
	// the round -- the model, the reasoning effort and the commit it read
	// (CLI 0.16.0), which is the pair a reader needs to tell which model
	// reviewed which head. In the record's own order, bounded at
	// diagnoseReviews (the bound the record's own writer does not have) and
	// with every string redacted, Model and Effort included: they are the
	// record's bytes, which a hand can edit. Absent when the record has no
	// reviews, and when there is no record at all.
	Reviews []runstore.ReviewSummary `json:"reviews,omitempty"`
	// ReviewLevels is what each review level's loop spent against the cap the
	// run's plan resolved for it (docs/review-levels-ab-jobmax
	// implementation-plan.md V1.3): one entry per level the run's plan holds or
	// whose tier its reviews carry, in level order, each with the rounds that
	// level's own loop reached and its cap.
	//
	// It is the figures the Run screen's iterations bar is drawn from, and they
	// are the same figures `fugaro diagnose` prints: the cap is the record's own
	// plan block, written by the runner, and the rounds are the highest round
	// the record's reviews carry for that level -- each level's loop counts from
	// 1, so its own highest round is what it spent. Cap is a pointer because a
	// cap the record does not carry is not a zero: a record written before the
	// plan block existed has none, and a bar drawn against a guessed cap would
	// state a bound no layer set (V1.3's "a cap it cannot resolve prints null,
	// never a guess").
	//
	// Absent -- never an empty list -- for a record that carries no plan block
	// and no review at any level, and for a run with no record at all.
	ReviewLevels []ReviewLevel `json:"review_levels,omitempty"`
	// Triage is the record's triage rounds (runstore.TriageRecord, D1846,
	// D1850) as a diagnosis reads them: one entry per round the stage ran, in
	// the record's own order and bounded again on read, with the review round
	// it judged, the mode, level, model and account it ran under, whether its
	// call failed, why it failed (Cause), and its placements counted by source.
	// The entries themselves are the record's and stay in it — this is the
	// shape a reader needs to see which rounds a judge weighed and which ones
	// the fail-safe decided, and a diagnosis that dropped a failed round would
	// be showing a run whose findings look judged (D1846). Absent — never an
	// empty list — for a run that triaged nothing, and for every record written
	// before the field existed.
	Triage []TriageRound `json:"triage,omitempty"`
	// Reported is what providers said the run's calls cost, beside Charge,
	// what the run charged the provider routes' calls -- the table's price
	// with the fee, or the report where it floors an unpriced tier's charge.
	// Claude's spend is not in it. Zero when no provider reported one.
	//
	// TableLoUSD and TableHiUSD are what the table would have charged the calls
	// a provider reported a cost for, at the tier's cheapest and dearest ends,
	// and UnreportedCalls is how many of the run's settled calls carried no
	// report at all (D2650, runstore.Cost's own fields). Together they are the
	// band a report is read against: under D2640 a complete call settles on the
	// report, so a report compared with Charge agrees with it by construction
	// (F16). Zero for a record written before the fields existed, and for a run
	// whose calls reported no cost -- and zero is what says this record has no
	// band, so its report is compared with the charge instead.
	Reported float64 `json:"reported_usd,omitempty"`
	Charge   float64 `json:"charge_usd,omitempty"`
	// ReportedWarning is set when the report and the charge differ by more than
	// reportedGapPct, for a record with no band. It is empty for a record that
	// has one, whose comparison is BandWarning's: the console renders this
	// field, so a sentence left in it would describe a comparison that record
	// never made.
	ReportedWarning string `json:"reported_warning,omitempty"`
	// BandWarning is set when the report falls more than reportedGapPct above
	// TableHiUSD or below TableLoUSD: the provider billed outside the band the
	// table prices those calls in, in either direction (D2650, F17).
	BandWarning     string  `json:"band_warning,omitempty"`
	TableLoUSD      float64 `json:"table_lo_usd,omitempty"`
	TableHiUSD      float64 `json:"table_hi_usd,omitempty"`
	UnreportedCalls int     `json:"unreported_calls,omitempty"`
	// PinWarnings are the model prices the run's cap counted on a guess:
	// an unverified placeholder, or cache rates left at 0.
	PinWarnings []string `json:"pin_warnings,omitempty"`
	// Tolerated is the findings the run's resolved mode let through
	// (runstore.ToleratedFindings), in the reviewer's order, bounded again on
	// read and with every title redacted. Absent -- never an empty block --
	// for a run that tolerated nothing, and for every record written before
	// the field existed.
	Tolerated *runstore.ToleratedFindings `json:"tolerated,omitempty"`
	// Ruling is the owner's ruling this run worked from (OD1,
	// runstore.Record.OwnerRuled, fugaro run --owner-ruled): the answer to
	// the findings a run on this pull request left, which the brief of this
	// run carries verbatim — the ruling its own launch was given, or the one
	// the run before it worked from, which a follow-up launched without one
	// inherits. The record's own text, redacted and clipped on
	// read like every other field of it, and absent for a run that worked
	// from no ruling — every run that is not answering a decision.
	Ruling string `json:"owner_ruled,omitempty"`
	// Checkpoint is the report a governance halt left (T6.1, D329, G7): why a
	// tripwire stopped the run, and what a person deciding whether to continue
	// it needs to know. The record's own block, with every string redacted and
	// every text field clipped, and absent -- never an empty block -- for a run
	// that took no governance halt: a budget or credential halt carries none,
	// and neither does a record written before the field existed.
	Checkpoint *runstore.Checkpoint `json:"checkpoint,omitempty"`
}

// TriageRound is one triage round as a diagnosis prints it: the review round
// the judge was asked about, the mode and level in force at it, the model and
// account that served the call where the record names them, whether the call
// failed and the one-line cause the record kept for the failure, and how the
// round's findings were placed, counted by source.
//
// It is not the record's own round (runstore.TriageRound), which carries the
// placements themselves: a diagnosis is the answer to "what happened to this
// run", and each round's answer is the failure and the four counts it was read
// with. The entries stay in the record, which is the run's own document, and
// each one's source is what these counts are — a round with no fail_safe entry
// is a round a judge really answered.
type TriageRound struct {
	Round int          `json:"round"`
	Mode  rigor.Rigor  `json:"mode"`
	Level policy.Level `json:"level"`
	Model string       `json:"model,omitempty"`
	// Account is the Claude account that served the round, empty for a round
	// that ran on the run's own credential.
	Account string `json:"account,omitempty"`
	// Failed is whether the call failed or its answer could not be read: the
	// fail-safe blocks every finding the mode's table blocks (D1846), and the
	// fix round that follows works on those findings, which no judge weighed.
	Failed bool `json:"failed,omitempty"`
	// Skipped is whether the round was not asked at all, because the stage
	// resolved to no Claude account and the run's gateway held no Anthropic
	// key for the model it would run (runstore.TriageRound.Skipped). It is not
	// Failed — nothing failed, the run chose not to ask — and the fail-safe
	// settles such a round the same way, which is why a reader takes the two
	// together.
	Skipped bool `json:"skipped,omitempty"`
	// Cause is why the round failed or was skipped, one line, and absent for a
	// round that neither (runstore.TriageRound.Cause): the call's own first
	// line where it answered nothing shaped like a verdict, `unreadable
	// verdict` where it answered one the reader refused, or the run's own
	// sentence about the account no policy names where the round was skipped.
	Cause string `json:"cause,omitempty"`
	// Sources is the round's placements counted by where they came from. The
	// four sum to the entries the record holds, which is Total unless the
	// record's cap cut the list.
	Sources TriageSources `json:"sources"`
	// Total is how many findings the round read (runstore.TriageRound.Total),
	// the record's own count and never less than the entries it holds.
	Total int `json:"total"`
}

// TriageSources counts one triage round's placements by their source
// (runstore.TriageSource): the judge's own answers, the mode's table deciding
// on its own, the fail-safe a round with no verdict takes, and the cap's own
// deferral (D1846, D1850, D1858). All four are always printed, zeros included:
// a reader comparing two rounds must not have to tell "nothing came from the
// fail-safe" from "the fail-safe is not shown".
type TriageSources struct {
	Agent    int `json:"agent"`
	Table    int `json:"table"`
	FailSafe int `json:"fail_safe"`
	Cap      int `json:"cap"`
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
		Long: "Gather what is known about RUN (a run ID, <repo-slug>/<run-id>, or the last\n" +
			"four or more characters of a run ID): its status and reason, the last test\n" +
			"run's failed and flaky tests, the last\n" +
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
	// explains it; it just has no logs. The reference is what resolveRun
	// accepts — a full run ID, <slug>/<run-id>, or an ending (D1701) — and
	// the row itself keeps naming the run in full, as the details view it is
	// (D1705).
	slug, id, err := resolveRun(ctx, env, ref, resolveRunOpts(cmd, o.asJSON))
	if err != nil {
		return err
	}
	s := runstore.Open(env.bucket.Bucket, slug, id)
	// A diagnosis prints the run's logs and the chain it belongs to, which is
	// the read D1586 scopes to the run's own repository, so the same check
	// `fugaro logs` and `fugaro runs show` make stands here — before the row,
	// the logs or the chain are read at all, and judged at the repository the
	// run's own task names (plan task RS6a).
	if err := requireRunAccess(ctx, env, accesslib.ActionViewRepo, s, "nothing was read"); err != nil {
		return err
	}
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
	// The row's pull request state is a listing's answer, folded from every run
	// of the pull request the listing read; this read holds one run, so a state
	// read from it alone would be a claim about the pull request that this
	// command cannot support (runview.Row.PRState). It is dropped rather than
	// guessed, the way every other field of the row is absent when nothing
	// recorded it.
	d.Row.PRState = ""
	// A paused run's own two facts are not in its records either — the instant
	// the credentials service recorded and the run that resumed it — and the
	// console's Run screen draws them on the row this document carries
	// (contract-pause R15). This read holds no listing to take them from, so it
	// stamps them the way a listing does, over the repository's own window
	// (stampPause).
	d.Row = stampPause(ctx, env, d.Row, func(format string, args ...any) {
		fmt.Fprint(warn, multiLine(fmt.Sprintf(format, args...)))
	})
	d.DraftNote = draftNote(d.Row, time.Now())
	d.Row.Reason, d.FailureClass = red(d.Row.Reason), d.Row.FailureClass
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
	// A halted run's first question is how much of its chain's limits is left
	// (D1055), so a halted run's diagnosis carries the block `runs show` prints.
	// A chain read that fails is a warning here and not a failure: the halt is
	// the run's own, the block is what tells a reader what to do about it, and
	// the rest of the diagnosis is worth having without it.
	if d.Halt != nil {
		lim, cerr := chainLimitsOf(ctx, env, s.Slug(), s.RunID(), warn)
		if cerr != nil {
			fmt.Fprintf(warn, "warning: %v\n", cerr)
		} else {
			d.ChainLimits = lim
		}
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
		d.Switches = switchesOf(rec.Switches, red)
		d.Row.OwnerDecisions = ownerDecisionsOf(rec.OwnerDecisions, red)
		d.Ruling = clipRunes(red(rec.OwnerRuled), task.MaxOwnerRuledBytes)
		d.Retries = rec.Retries
		d.Reviews = reviewsOf(rec.Reviews, red)
		d.ReviewLevels = reviewLevelsOf(rec.Reviews, rec.Plan)
		d.Triage = triageRoundsOf(rec.Triage, red)
		d.Checkpoint = checkpointOf(rec.Checkpoint, red)
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
		d.AgentMessage = logtail.Clip(red(agentMessage(ctx, s, rec)), agentMessageBytes)
	}
	// Outside the record's own block: a run whose record carries no messages
	// block can still have message objects under its prefix — a launcher writes
	// them under its own grant — and those are the messages it never read
	// (F18).
	d.Messages = diagnoseMessages(ctx, env, s, rec, red, warn)

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
		if s.Source != nil {
			// Source is a pointer into the record, so the copy above does not
			// cover it: redacting through it would write into the record's own
			// bytes, and leaving it alone would print a name the rest of this
			// document redacts.
			src := *s.Source
			src.Name = red(src.Name)
			s.Source = &src
		}
		out = append(out, s)
	}
	return out
}

// switchesOf is the record's switch log for the document: the record's own
// order, bounded at diagnoseSwitches, and redacted like every other string here
// -- From, To and Reason are another writer's names and words, not this
// build's. nil for a record with none, so the key is absent rather than an
// empty list.
func switchesOf(switches []runstore.Switch, red func(string) string) []runstore.Switch {
	if len(switches) == 0 {
		return nil
	}
	out := make([]runstore.Switch, 0, min(len(switches), diagnoseSwitches))
	for _, s := range switches {
		if len(out) == diagnoseSwitches {
			break
		}
		s.From, s.To, s.Reason = red(s.From), red(s.To), red(s.Reason)
		out = append(out, s)
	}
	return out
}

// validGitHead reports whether s is a git object id: 40 lowercase hex
// characters, the SHA-1 a `git rev-parse` writes, or 64, the SHA-256 of a
// repository created with --object-format=sha256. It is validSHA256's guard
// with a second length, and it exists for the same reason (runs.go): the
// record is launcher-writable and json.Unmarshal never checks it against the
// schema, so a shortener must reject anything else before slicing, or a
// multibyte rune cut mid-byte would corrupt the terminal output.
func validGitHead(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// shortGitHead shortens a git head for display: 7 characters of a SHA-1 object
// id and 12 of a SHA-256 one, git's own abbreviated length for each. It is not
// shortSHA (runs.go), which shortens a sha256 digest a record states -- an
// artifact's or a layer's -- and answers "(invalid)" for a 40-hex git head,
// which is every head a run records. Anything that is not an object id shows
// as "(invalid)" rather than as a slice of text that is not a head.
func shortGitHead(s string) string {
	if !validGitHead(s) {
		return "(invalid)"
	}
	if len(s) == 40 {
		return s[:7]
	}
	return s[:12]
}

// reviewLine is one review round as the diagnosis's Reviews block prints it:
// the round, the tier that ran it, its verdict and its findings count, and what
// reviewed it -- the model, the effort and the commit it read (CLI 0.16.0).
// Each of the three is printed only when the round recorded it, so a record
// written before the fields existed reads exactly as it always did.
//
// The verdict is the record's own word, but "none" is the one a reviewer's
// missing answer is recorded as (seniorLoop, adversarialLoop), and printing it
// as a verdict a reader could act on would be claiming an answer no model gave.
func reviewLine(r runstore.ReviewSummary) string {
	who := runstore.TierSenior
	switch r.Tier {
	case runstore.TierFirst, runstore.TierAdversarial:
		who = r.Tier
	}
	verdict := r.Verdict
	if verdict == "none" {
		verdict = "no verdict"
	}
	line := fmt.Sprintf("round %d %s: %s, %s", r.Round, who, verdict, runner.Plural(r.Findings, "finding"))
	parts := make([]string, 0, 3)
	if r.Model != "" {
		parts = append(parts, "model "+r.Model)
	}
	if r.Effort != "" {
		parts = append(parts, "effort "+r.Effort)
	}
	if r.Head != "" {
		parts = append(parts, "at "+shortGitHead(r.Head))
	}
	if len(parts) > 0 {
		line += " (recorded: " + strings.Join(parts, ", ") + ")"
	}
	return line
}

// reviewsOf is the record's review rounds for the document: the record's own
// order, bounded at diagnoseReviews, and redacted like every other string here
// -- Model, Effort and Head included, and the tier and the verdict too, which
// the write path validates to its own three words and a hand-edited record need
// not: they are strings on the wire, and the block's own enums are redacted for
// the same reason (toleratedOf). nil for a record with none, so the key is
// absent rather than an empty list.
func reviewsOf(reviews []runstore.ReviewSummary, red func(string) string) []runstore.ReviewSummary {
	if len(reviews) == 0 {
		return nil
	}
	out := make([]runstore.ReviewSummary, 0, min(len(reviews), diagnoseReviews))
	for _, r := range reviews {
		if len(out) == diagnoseReviews {
			break
		}
		r.Tier, r.Verdict = red(r.Tier), red(r.Verdict)
		r.Model, r.Effort, r.Head = red(r.Model), red(r.Effort), red(r.Head)
		out = append(out, r)
	}
	return out
}

// ReviewLevel is one review level's own loop as a diagnosis reads it (V1.3):
// the level, the rounds that level spent, and the cap the run's plan resolved
// for it. Cap is nil where the record carries no cap for the level, which is
// every record written before the plan block existed and a record whose plan
// does not hold the level -- and it is nil rather than a zero because a bound
// nothing set is not a bound of zero.
type ReviewLevel struct {
	Level  policy.Level `json:"level"`
	Rounds int          `json:"rounds"`
	Cap    *int         `json:"cap"`
}

// reviewLevelsOf is the document's per-level figures (V1.3): one entry per
// review level, in level order, whose cap the run's plan holds or whose tier the
// record's reviews carry -- so a running run shows each level at zero rounds
// against its cap, and a record written before the plan block shows what its
// reviews spent with no cap beside it.
//
// Rounds is the highest round that level's reviews carry, and never a count of
// them: each level's loop numbers its own rounds from 1, a first-line loop that
// bounces back to a rejected round restarts at 1 too, and the rounds the loop
// really spent is therefore the highest number it wrote. Independent review
// entries are not this run's own rounds -- another job's reviewer wrote them,
// display-only (runstore.TierIndependent) -- so they are not counted at any
// level; an unknown tier is read as the senior level's, the way reviewLine reads
// one.
func reviewLevelsOf(reviews []runstore.ReviewSummary, plan *runstore.PlanRecord) []ReviewLevel {
	caps := map[policy.Level]int{}
	if plan != nil {
		for _, st := range plan.Steps {
			lv, ok := planKindLevel(st.Kind)
			if !ok {
				continue
			}
			if _, seen := caps[lv]; !seen {
				caps[lv] = st.Rounds
			}
		}
	}
	spent := map[policy.Level]int{}
	for _, r := range reviews {
		lv, ok := reviewTierLevel(r.Tier)
		if !ok {
			continue
		}
		if r.Round > spent[lv] {
			spent[lv] = r.Round
		}
	}
	var out []ReviewLevel
	for _, lv := range []policy.Level{policy.LevelL1, policy.LevelL2, policy.LevelL3} {
		cap, hasCap := caps[lv]
		rounds, hasRounds := spent[lv]
		if !hasCap && !hasRounds {
			continue
		}
		entry := ReviewLevel{Level: lv, Rounds: rounds}
		if hasCap {
			n := cap
			entry.Cap = &n
		}
		out = append(out, entry)
	}
	return out
}

// planKindLevel is which review level a plan step's kind is, false for a step no
// level owns: a rebase step is the runner's own work, and its rounds bound a
// stage rather than a review loop (plan.go's stepLevel, which this mirrors over
// the record's own string).
func planKindLevel(kind string) (policy.Level, bool) {
	switch kind {
	case string(recipe.StepFirstLine):
		return policy.LevelL1, true
	case string(recipe.StepReview):
		return policy.LevelL2, true
	case string(recipe.StepAdversarial):
		return policy.LevelL3, true
	}
	return "", false
}

// reviewTierLevel is which review level a round's tier is, false for a tier no
// level of this run owns. An empty tier is a senior review in a run that had no
// first line (runstore.ReviewSummary.Tier), and any other unknown word is read
// as the senior level's rather than dropped: a hand-edited record still shows
// the rounds it carries, and a round attributed to the wrong level is at worst a
// bar that is too long -- dropping it would hide rounds a person can see in the
// list above.
func reviewTierLevel(tier string) (policy.Level, bool) {
	switch tier {
	case runstore.TierFirst:
		return policy.LevelL1, true
	case runstore.TierAdversarial:
		return policy.LevelL3, true
	case runstore.TierIndependent:
		return "", false
	}
	return policy.LevelL2, true
}

// reviewLevelLine is one level as the diagnosis's Review levels block prints it:
// the rounds the level spent against its cap, and -- where the record carries no
// cap -- the rounds alone with the absent cap said in words, because a reader
// must not take a missing bound for a reached one.
func reviewLevelLine(lv ReviewLevel) string {
	if lv.Cap == nil {
		return fmt.Sprintf("level %s: %s, no cap recorded", lv.Level, runner.Plural(lv.Rounds, "round"))
	}
	return fmt.Sprintf("level %s: %d of %s", lv.Level, lv.Rounds, runner.Plural(*lv.Cap, "round"))
}

// triageFlag is the line a diagnosis carries at the top when any triage round
// failed or was skipped, "" for a run whose rounds all answered and for one that
// triaged nothing: which rounds bought no verdict and what became of each call,
// and what the fail-safe did with the findings — every finding the mode's table
// blocks blocked, which is what the fix round that follows was handed (D1846).
// It is the summary of the block below it, written from the same reading, so the
// two cannot say different things about one round.
//
// Each round's clause is runstore's — TriageFailureClause for a call that went
// out and bought no verdict, TriageSkipClause for a round the run did not ask —
// and each is one shared with the pull request's status section
// (internal/runner's triageStatusLine): a judge that answered nothing, a judge
// whose answer could not be read and a judge that was never asked are three
// different facts, and the two readers of the record must not tell them apart
// differently. The clauses stand in the record's order, so a run that skipped a
// round and failed a later one reads in the order it happened.
func triageFlag(rounds []TriageRound) string {
	var parts []string
	for _, r := range rounds {
		switch {
		case r.Skipped:
			parts = append(parts, runstore.TriageSkipClause(r.Level, r.Round, r.Cause))
		case r.Failed:
			parts = append(parts, runstore.TriageFailureClause(r.Level, r.Round, r.Cause))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("%s; the fail-safe blocked every finding the mode's table blocks",
		strings.Join(parts, "; "))
}

// triageLine is one triage round as the human block prints it: the round it
// judged, the mode and level it ran under, the model and the account that
// answered where the record names them, the findings it read and how they were
// placed, counted by source — and, for a round whose call failed or which was
// not asked at all, the word FAILED or SKIPPED and the cause the record kept
// for it.
//
// All four counts are printed, zeros included, because a reader comparing two
// rounds must not have to tell "nothing came from the fail-safe" from "the
// fail-safe is not shown", and a round nothing was placed on still says how
// many findings it read (Total).
func triageLine(r TriageRound) string {
	where := []string{string(r.Mode), string(r.Level)}
	if r.Model != "" {
		where = append(where, r.Model)
	}
	if r.Account != "" {
		where = append(where, "account "+clipRunes(r.Account, maxNameCellRunes))
	}
	line := fmt.Sprintf("round %d (%s): %s — agent %d, table %d, fail_safe %d, cap %d",
		r.Round, strings.Join(where, ", "), runner.Plural(r.Total, "finding"),
		r.Sources.Agent, r.Sources.Table, r.Sources.FailSafe, r.Sources.Cap)
	switch {
	case r.Skipped:
		line += "; SKIPPED"
	case r.Failed:
		line += "; FAILED"
	default:
		return line
	}
	if r.Cause != "" {
		line += ": " + r.Cause
	}
	return line
}

// triageRoundsOf is the record's triage rounds for the document: the record's
// own order, bounded again on read — the write-side bound is
// runstore.MaxTriageRounds, and the record is an object a hand can edit — and
// with every string redacted, the cause, the account and the model included,
// which are the record's bytes. nil for a record carrying no triage block, so
// the key is absent rather than an empty list, and the rounds are counted here
// and not stored: the counts are a reading of the entries, which are the
// record's own.
//
// The counts are of the entries the record holds, and Total is the record's own
// count of the findings the round read: the two differ where the cap cut a
// round's list to MaxTriageEntries, and a diagnosis of such a round shows the
// counts it can stand behind with the total beside them.
func triageRoundsOf(t *runstore.TriageRecord, red func(string) string) []TriageRound {
	if t == nil || len(t.Rounds) == 0 {
		return nil
	}
	out := make([]TriageRound, 0, min(len(t.Rounds), runstore.MaxTriageRounds))
	for _, r := range t.Rounds {
		if len(out) == runstore.MaxTriageRounds {
			break
		}
		tr := TriageRound{
			Round:   r.Round,
			Mode:    rigor.Rigor(red(string(r.Mode))),
			Level:   policy.Level(red(string(r.Level))),
			Model:   red(r.Model),
			Account: red(r.Account),
			Failed:  r.Failed,
			Skipped: r.Skipped,
			Cause:   red(r.Cause),
			Total:   r.Total,
		}
		for _, e := range r.Entries {
			switch runstore.TriageSource(red(string(e.Source))) {
			case runstore.TriageSourceAgent:
				tr.Sources.Agent++
			case runstore.TriageSourceTable:
				tr.Sources.Table++
			case runstore.TriageSourceFailSafe:
				tr.Sources.FailSafe++
			case runstore.TriageSourceCap:
				tr.Sources.Cap++
			}
		}
		out = append(out, tr)
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
		f.Mode, f.Label, f.Class = rigor.Rigor(red(string(f.Mode))), rigor.Label(red(string(f.Label))), rigor.Class(red(string(f.Class)))
		out.Findings = append(out.Findings, f)
	}
	// The block's total is how many findings the mode really tolerated, and a
	// record claiming fewer than it lists is not one to print as written.
	out.Total = max(t.Total, len(out.Findings))
	return out
}

// ownerDecisionsOf is the pending owner decisions a run's record carries
// (OD1) for the document: every sentence redacted -- the file and the sentence
// are model output -- and bounded again on read, since the record is an object
// a hand can edit and the write-side bound (runstore.NewOwnerDecisions) is not
// one a reader can count on. The total is never less than the decisions it
// lists, so a record claiming fewer than it lists is not printed as written.
func ownerDecisionsOf(od *runstore.OwnerDecisions, red func(string) string) *runstore.OwnerDecisions {
	if od == nil || len(od.Decisions) == 0 {
		return nil
	}
	ds := make([]runstore.OwnerDecision, 0, min(len(od.Decisions), runstore.MaxOwnerDecisions))
	for _, d := range od.Decisions {
		if len(ds) == runstore.MaxOwnerDecisions {
			break
		}
		ds = append(ds, runstore.OwnerDecision{File: red(d.File), Summary: red(d.Summary)})
	}
	out := runstore.NewOwnerDecisions(ds)
	if out == nil {
		return nil
	}
	out.Total = max(od.Total, out.Total, len(out.Decisions))
	return out
}

// ownerBlock is what a run is waiting for its owner to decide (OD1), printed
// under the run's status and above its cost: the count the run's own reason
// names (its halt's where it halted on the decisions),
// then one line per decision with the file and the reviewer's own sentence, in
// the words the pull request's status section and `fugaro runs ls` use. It is
// empty for a run that is waiting on nothing, which is every ordinary run.
func ownerBlock(od *runstore.OwnerDecisions) string {
	if od == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nWaiting on you\n  review is waiting on %s\n", runner.Plural(od.Total, "owner decision"))
	for _, d := range od.Decisions {
		summary := oneLine(clipRunes(d.Summary, maxOwnerDecisionRunes))
		if d.File != "" {
			fmt.Fprintf(&b, "  - `%s`: %s\n", oneLine(d.File), summary)
			continue
		}
		fmt.Fprintf(&b, "  - %s\n", summary)
	}
	if n := od.Total - len(od.Decisions); n > 0 {
		fmt.Fprintf(&b, "  and %s more; the run's record lists %d of them\n", runner.Plural(n, "decision"), len(od.Decisions))
	}
	return b.String()
}

// rulingBlock is the owner's ruling a run was launched with (OD1), printed
// under what the run before it was waiting for: the text the brief carries,
// indented as a block so a multi-line ruling reads as one, and empty for a run
// launched without one. Only carriage returns are turned into newlines here —
// the ruling is a person's own prose, and the diagnosis is where they read it
// back — but every line is still flattened to one terminal line, since the
// record is an object a hand can edit.
func rulingBlock(ruling string) string {
	if strings.TrimSpace(ruling) == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nThe owner ruled (this run works from it)\n")
	for _, line := range strings.Split(strings.ReplaceAll(ruling, "\r\n", "\n"), "\n") {
		fmt.Fprintf(&b, "  %s\n", oneLine(line))
	}
	return b.String()
}

// checkpointOf is the record's checkpoint for the document: every string
// redacted -- the trigger and the runner's own words are values the write path
// set and a hand-edited record need not have, and the three the stopping agent
// answers are model output -- and every text field clipped again on read, since
// the record is an object a hand can edit and the write-side bound
// (runstore.NewCheckpoint) is not one a reader can count on.
//
// nil for a run carrying none, and for the block a hand can write that says
// nothing at all -- `"checkpoint": {}`, which the same hand could put in a
// record that took no governance halt -- so the key is absent and neither
// output prints a block with no report in it. A block that says anything, the
// estimate alone included, is the record's and is printed.
func checkpointOf(cp *runstore.Checkpoint, red func(string) string) *runstore.Checkpoint {
	if cp == nil {
		return nil
	}
	out := *cp
	out.Trigger = red(out.Trigger)
	out.Done = clipRunes(red(out.Done), runstore.MaxCheckpointText)
	out.WhyOver = clipRunes(red(out.WhyOver), runstore.MaxCheckpointText)
	out.Next = clipRunes(red(out.Next), runstore.MaxCheckpointText)
	out.HardInstructions = clipRunes(red(out.HardInstructions), runstore.MaxCheckpointText)
	if out == (runstore.Checkpoint{}) {
		return nil
	}
	return &out
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

// reportedGapPct is how far outside the table's band -- or, for a record that
// carries none, above Fugaro's charge without the route fee -- a provider's
// reported cost may sit before diagnose warns.
const reportedGapPct = 10

// routeOf fills d's route, reported-vs-band and pin warnings from the
// record's cost: the models the run paid for are its pins. The warnings use
// the price table with the project's overrides, as the run's cap did.
func routeOf(d *Diagnosis, rec *runstore.Record, lc *localcfg.Config, red func(string) string) {
	if rec == nil || rec.Cost == nil {
		return
	}
	c := rec.Cost
	d.Route, d.ModelBy = redactKeys(c.RouteBy, red), redactKeys(c.ModelBy, red)
	d.TableLoUSD, d.TableHiUSD, d.UnreportedCalls = c.TableLoUSD, c.TableHiUSD, c.UnreportedCalls
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
		// A record that carries the band is compared with it and not with the
		// charge: D2640 settles a complete call on the report, so a report
		// against the charge agrees with it by construction (F16).
		switch {
		case d.TableHiUSD > 0 && d.Reported > d.TableHiUSD*(1+reportedGapPct/100.0):
			// The one answer left: the catalog already charges its dearest
			// end by default (D2644), so a report above it is the owner's
			// own price being too low, or an endpoint dearer than the whole
			// tier (design §7, D12).
			d.BandWarning = fmt.Sprintf("the providers reported $%.4f, more than %d%% above the $%.4f the table would have charged the calls they reported at the tier's dearest end: model_prices may be too low, or a catalog-priced route served an endpoint dearer than the tier's range — price the model under model_prices; check model_prices and the account's provider settings",
				d.Reported, reportedGapPct, d.TableHiUSD)
		case d.TableLoUSD > 0 && d.Reported < d.TableLoUSD*(1-reportedGapPct/100.0):
			// Below the band the table over-prices those calls, which is the
			// direction an override meant as dollars-per-million leaves
			// (spec §"Likely causes" 2) and the one a provider that
			// under-reports its own bill shows up in (F17): the caps are
			// enforced against the charge, so a low report is the one that
			// hides spend.
			d.BandWarning = fmt.Sprintf("the providers reported $%.4f, more than %d%% below the $%.4f the table would have charged those calls at the tier's cheapest end: model_prices may be too high for the endpoints served, or the provider under-reported what it billed; check model_prices and the account's provider settings",
				d.Reported, reportedGapPct, d.TableLoUSD)
		case d.TableHiUSD == 0 && d.TableLoUSD == 0 && d.Reported > bare*(1+reportedGapPct/100.0):
			// The charge is the comparison only for a record with no band,
			// and only a report above it is a sign of under-pricing: a lower
			// one may be partial reporting (a cut stream, a call without a
			// cost).
			d.ReportedWarning = fmt.Sprintf("the providers reported $%.4f, more than %d%% above the $%.4f the table charged without the route fee: model_prices may be too low, or a catalog-priced route served an endpoint dearer than the end it charged — price the model under model_prices (partial reporting is also possible); check model_prices and the account's provider settings",
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

// reportedCalls is how many of the run's settled calls carried a provider
// report, and how many it settled in all. The record keeps the unreported
// count alone (runstore.Cost), and the gateway's count of each stage's calls
// is the only figure in it the two can be read off: a call that settled after
// its stage's report was taken is in the record's unreported count and in no
// stage's, so the sum can sit below the calls the record itself says carried
// no report, and the larger of the two is the run's settled calls.
func reportedCalls(stages []runstore.StageTiming, unreported int) (reported, settled int) {
	for _, st := range stages {
		if st.Calls != nil {
			settled += *st.Calls
		}
	}
	settled = max(settled, unreported)
	return settled - unreported, settled
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
// fix-<n> down to fix-1, then implement-<m> down to implement-1, each count
// taken from rec.Stages.
//
// The count is the shape the transcripts are named by: one file per attempt,
// transcripts/<stage>-<n>.jsonl, n the stage's own attempt number
// (internal/runner's stageN). A stage can run more than once -- a second
// implement after a recovered failure, a retried implement (issue #1827) --
// so reading implement-1 alone would report an attempt the run threw away as
// the agent's last word. The count from rec.Stages is the same count, since
// every attempt appends one stage timing of its own.
//
// The implement walk keeps implement-1 as its floor: a record can have no
// implement timing while the transcript is there -- written before the stage
// timings were (2026-10-03), or a run killed between the attempt's upload and
// its timing write, which is the dead run diagnose exists for -- and that is
// the transcript such a record has.
func agentMessage(ctx context.Context, s *runstore.Store, rec *runstore.Record) string {
	fixes, implements := 0, 0
	for _, st := range rec.Stages {
		switch st.Name {
		case "fix":
			fixes++
		case "implement":
			implements++
		}
	}
	for n := fixes; n >= 1; n-- {
		if res, ok := transcriptResult(ctx, s, "fix-"+strconv.Itoa(n)); ok {
			return res.Text
		}
	}
	for n := max(implements, 1); n >= 1; n-- {
		if res, ok := transcriptResult(ctx, s, "implement-"+strconv.Itoa(n)); ok {
			return res.Text
		}
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

// diagnoseMessages is what became of every message of this run, as far as the
// two places that hold one can say: the run's own record (F13) and the message
// objects still under tokens/<slug>/<run>/messages/ (F18).
//
// The record is the run's account of itself and the run's own agent can rewrite
// it (F20), so the two are read side by side rather than one trusted: an object
// the record never mentions is a message the run never read, and an undelivered
// message whose object the bucket has reaped is one whose text is gone (F11).
// The message's text and the line that sends it again come from the object and
// the code host, never from anything under runs/ (F19) — a stored report.md is
// as writable by the run as its record.
//
// A failed listing is a warning and not the command's failure: the record's own
// account is still worth having, and the objects are the part this read could
// not reach.
func diagnoseMessages(ctx context.Context, env *cloudEnv, s *runstore.Store, rec *runstore.Record, red func(string) string, warn io.Writer) *MessagesDoc {
	doc := messagesOf(rec, red)
	nums, err := listedMessages(ctx, env.bucket, s.Slug(), s.RunID())
	if err != nil {
		fmt.Fprintf(warn, "warning: %v\n", oneLine(err.Error()))
	}
	if doc == nil && len(nums) == 0 {
		return nil
	}
	if doc == nil {
		doc = &MessagesDoc{Messages: []MessageFate{}}
	}
	at := make(map[int]int, len(doc.Messages))
	for i, f := range doc.Messages {
		at[f.Seq] = i
	}
	listed := make(map[int]bool, len(nums))
	for _, n := range nums {
		listed[n] = true
		if i, ok := at[n]; ok {
			doc.Messages[i].OverCap = n > runmsg.MaxMessages
			continue
		}
		doc.Messages = append(doc.Messages, MessageFate{Seq: n, Recorded: false, OverCap: n > runmsg.MaxMessages})
	}
	slices.SortFunc(doc.Messages, func(a, b MessageFate) int { return a.Seq - b.Seq })
	if len(doc.Messages) > diagnoseMessageFates {
		doc.Messages = doc.Messages[:diagnoseMessageFates]
		doc.Truncated = true
	}
	// Every entry was looked for, so every one carries the answer: the object is
	// there or the bucket has reaped it (F11). Nil -- "not read" -- is what
	// `runs show` leaves, which never looks.
	for i := range doc.Messages {
		doc.Messages[i].Object = ptrBool(listed[doc.Messages[i].Seq])
	}
	// The repository the copy-paste line names is this machine's own name for
	// the run's slug, read off the local config, and the number beside it comes
	// from the code host: neither is read from anything under runs/ (F19).
	repo, named := diagnoseRepoName(env.lc, s.Slug())
	asked, pr, note := false, 0, ""
	for i := range doc.Messages {
		f := &doc.Messages[i]
		switch {
		case f.OverCap:
			// A number no run reads: there is nothing to read here either
			// (F12).
		case f.Recorded && !*f.Object && f.State == runmsg.StateUndelivered:
			// The record says the run ended before a stage could carry it and
			// the bucket has since reaped the object: it is a message whose
			// text is gone (F11).
			f.Expired = true
		case !*f.Object || (f.Recorded && f.State != runmsg.StateUndelivered && f.State != runmsg.StateMissing):
			// A message with no object left, and one the run delivered or
			// refused, needs nothing read: the record is all there is.
		default:
			// A message that never reached the agent — undelivered, never
			// mentioned by the record, or a hole the run walked past that an
			// object filled afterwards — is the one a person wants back.
			m, err := runmsg.Next(ctx, env.bucket, s.Slug(), s.RunID(), f.Seq)
			if err != nil {
				fmt.Fprintf(warn, "warning: %v\n", oneLine(err.Error()))
				continue
			}
			if m == nil {
				// Unreadable rather than absent: the store refuses an object
				// that is not a message this build reads and answers none for
				// one that is gone (F12, rule 6). Either way there is no text.
				continue
			}
			f.Text = red(m.Text)
			if !named {
				f.LineNote = "this machine's config names no repository for " + pluginwire.Printable(s.Slug()) + ", so the line cannot name one either"
				continue
			}
			if !asked {
				pr, note = diagnoseOpenPR(ctx, env, repo, s.RunID(), warn)
				asked = true
			}
			f.Line, f.LineNote = messageResendLine(repo, pr, f.Text), note
		}
	}
	return doc
}

// diagnoseMessageFates bounds the message entries a diagnosis prints. It sits
// just above the channel's own cap (runmsg.MaxMessages) so that the 21st
// message, which no run reads and F12 asks a diagnosis to show as over the cap,
// is still one of them, and a launcher that fills the prefix by hand — the only
// writer of a number that high — cannot make the output unbounded.
const diagnoseMessageFates = runmsg.MaxMessages + 4

// diagnoseRepoName is the repository a copy-paste line names: the repository
// this machine's config lists under the run's own storage slug, in the plain
// owner/name a `--repo` takes and a provider is opened for. accessSlugRepo
// answers the same question with the provider kind in front of it — the
// canonical form its own callers judge a credential by (F27) — so the kind is
// what comes off the front.
func diagnoseRepoName(lc *localcfg.Config, slug string) (string, bool) {
	canonical, ok := accessSlugRepo(lc, slug)
	if !ok {
		return "", false
	}
	i := strings.IndexByte(canonical, '/')
	if i < 0 {
		return "", false
	}
	return canonical[i+1:], true
}

// listedMessages is the message numbers under the run's prefix, lowest first. A
// launcher holds a read grant on tokens/ and can list the prefix a job account
// cannot, which is what lets a diagnosis show a message the run's record never
// mentions (F18).
//
// A name that is not one this channel writes is skipped rather than refused, as
// the store's own listing does: the prefix is writable by every launcher, and
// an object put there by hand must not be able to fail the read.
func listedMessages(ctx context.Context, b *blobx.Bucket, slug, run string) ([]int, error) {
	prefix := runmsg.Prefix(slug, run)
	it := b.List(&blob.ListOptions{Prefix: prefix})
	var out []int
	for {
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("listing %s failed: %s", prefix, err)
		}
		if n, ok := messageNumber(obj.Key); ok {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out, nil
}

// messageNumber is the number a listed object's name carries, or false for a
// name this channel does not write: the four digits and the suffix a message
// object's name is made of (runmsg.Path).
func messageNumber(key string) (int, bool) {
	stem, ok := strings.CutSuffix(path.Base(key), runmsg.Suffix)
	if !ok || len(stem) != 4 {
		return 0, false
	}
	n, err := strconv.Atoi(stem)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// openDiagnoseProvider is OpenProvider, a seam for the one read a diagnosis
// makes of the code host: the pull request of the run's own branch (F19), read
// with the PROCESS ENVIRONMENT's own credential as the image survey's reads
// are. A diagnosis is not the place to resolve a project credential — the line
// it builds is a way to send a message again, and a machine whose shell holds
// no token for the repository prints the line without a number and says why.
var openDiagnoseProvider = OpenProvider

// diagnoseOpenPR is the open pull request of the run's branch, or the reason
// there is no number to print (F19). PullRequestByBranch answers a closed or
// merged pull request too, and only an open one is where a message sent again
// belongs, so anything else is answered as no number rather than printed as one
// the line would then follow up on. Every failure is a note and never an error:
// the diagnosis is about the run, and this is one line of it that could not be
// built.
func diagnoseOpenPR(ctx context.Context, env *cloudEnv, repo, runID string, warn io.Writer) (int, string) {
	co := checkoutConfig(ctx, repo)
	if co == nil {
		co = &config.Config{}
	}
	kind := env.repoProvider(repo, co)
	if kind == "" {
		return 0, fmt.Sprintf("this machine cannot tell which git provider %s is on", pluginwire.Printable(repo))
	}
	_, open, err := openDiagnoseProvider(os.Environ(), func(w string) {
		fmt.Fprintf(warn, "fugaro: warning: %s\n", w)
	})
	if err != nil {
		return 0, "the code host could not be asked: " + oneLine(err.Error())
	}
	p, _, err := open(ctx, kind, repo)
	if err != nil {
		return 0, "the code host could not be asked: " + oneLine(err.Error())
	}
	r, ok := p.(gitprov.PRBranchReader)
	if !ok {
		return 0, fmt.Sprintf("%s cannot read a pull request by its source branch", pluginwire.Printable(kind))
	}
	branch := gitops.RunBranchPrefix + runID
	info, found, err := r.PullRequestByBranch(ctx, branch)
	switch {
	case err != nil:
		return 0, fmt.Sprintf("reading the pull request of %s on branch %s failed: %s", pluginwire.Printable(repo), pluginwire.Printable(branch), oneLine(err.Error()))
	case !found || info.State != gitprov.PROpen:
		return 0, fmt.Sprintf("no open pull request of %s is on branch %s", pluginwire.Printable(repo), pluginwire.Printable(branch))
	}
	return info.Number, ""
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
	// The class is the answer's first sentence (design §8 D9), so it stands
	// directly under the status it describes: the closed vocabulary's name for
	// why the run stopped, the runner's own where the record carries one and the
	// reader's reading of its reason where it does not (runview.ClassOf). A run
	// that did not fail, and a row whose ending is the read side's own — derived,
	// or an `error` for a run it could not follow — has no cause to name and gets
	// no line.
	if d.FailureClass != "" {
		fmt.Fprintf(&b, "Class:    %s\n", oneLine(string(d.FailureClass)))
	}
	if r.Reason != "" {
		fmt.Fprintf(&b, "Reason:   %s\n", oneLine(r.Reason))
	}
	// A triage round whose call failed is said here, at the top, and not only
	// in the block below: it changes what the run's own numbers mean. A fix
	// round that looks like any other was handed the findings the mode's table
	// blocks by the fail-safe, which no judge weighed (D1846), so a reader who
	// stops at the status block must not come away thinking every round of the
	// run was judged. The line names each failed round and its cause, and is
	// the same reading the block below and the pull request's own line are
	// written from.
	if line := triageFlag(d.Triage); line != "" {
		fmt.Fprintf(&b, "Triage:   %s\n", oneLine(line))
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
		// Directly under the halt: what is left of the chain's limits is the
		// question the halt line raises (D1055).
		b.WriteString(chainBlock(d.ChainLimits))
	}
	b.WriteString(checkpointBlock(d.Checkpoint))
	b.WriteString(ownerBlock(d.Row.OwnerDecisions))
	b.WriteString(rulingBlock(d.Ruling))
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
	if len(r.Cost.ServiceTiers) > 0 {
		// A provider answered at a tier the price table has no multiplier
		// for: that call was charged conservatively rather than failing its
		// stage (design §6.1), so the Cost line above counts a figure the
		// reader cannot otherwise explain -- on a provider route nothing
		// else names it, since the provider's own reported cost is recorded
		// and is what a complete call settled on (D2640). The rates the
		// multiple scales are the ones the call was otherwise priced at --
		// the serving model's own, or the table's maximum for a model it
		// doesn't know -- so the line says that and not "its own rates", which
		// would be wrong for a call the table priced at maximum rates, or one
		// the response also priced as a surprise. The multiple is an assumed
		// ceiling and not a published price, so where a provider reported a
		// cost a complete call settled on that figure with the route fee,
		// floored at the row's cheapest end, and only a call the stream cut
		// short keeps the multiple with the report as a floor under it. The
		// record cannot tell the two apart -- it carries the tiers, not the
		// calls -- so the line names both, in the warning's own words and with
		// the fee named, because the Reported line below shows the report
		// without it. The clause is printed only when the record carries a
		// report at all: a Claude call has no route, so a report cannot exist
		// for it, and a provider that sent none floors nothing.
		line := fmt.Sprintf("%s charged at %dx the rates the call was priced at",
			oneLine(strings.Join(r.Cost.ServiceTiers, ", ")), pricing.ConservativeTierMultiplier)
		if d.Reported > 0 {
			line += ", or the provider's reported cost with the route fee: a complete call settled on that figure, floored at the row's cheapest end, and a call the stream cut short was charged the dearer of the multiple and that figure"
		}
		fmt.Fprintf(&b, "Tier:     %s (no published price for the tier)\n", line)
	}
	if r.Cost.CatalogStale {
		// The run charged, and over-counted on purpose: the catalog it
		// priced from was past its own 30 days (design §7, P7).
		fmt.Fprintf(&b, "Catalog:  the built-in provider price catalog was stale, so every model it priced was charged the dearest end of its tier's range (regenerate it with scripts/gen-price-catalog.sh)\n")
	}
	if len(d.Route) > 0 {
		fmt.Fprintf(&b, "Route:    %s\n", oneLine(spendList(d.Route)))
	}
	// The stage the run ran a second time, when there was one (issue #1827):
	// the record's own count, because a stage that was run twice explains a
	// duration and a cost that a reader looking at the stage list would
	// otherwise have to guess at. Nothing else about it is here -- which
	// stage, and what the agent said, are in the log tail above and in the
	// run's stages.
	if d.Retries > 0 {
		fmt.Fprintf(&b, "Retries:  %d (the model's stream was closed before a stage finished, so that stage was run once more)\n", d.Retries)
	}
	// A record that carries the band says so here instead of the Reported
	// line: the report is read against the table's band for the same calls
	// (F16), and the settled figure is what the run was charged beside it.
	// The count says how much of the run the report covers -- the calls the
	// band is over -- so a band that a report sits inside is not read as a
	// whole run's report.
	if d.TableHiUSD > 0 || d.TableLoUSD > 0 {
		reported, settled := reportedCalls(d.Stages, d.UnreportedCalls)
		fmt.Fprintf(&b, "Settled:  $%.4f (reported on %d of %d calls); table band $%.4f-$%.4f\n",
			d.Charge, reported, settled, d.TableLoUSD, d.TableHiUSD)
	} else if d.Reported > 0 {
		fmt.Fprintf(&b, "Reported: providers said $%.4f; charged $%.4f (the table price settles; an unpriced tier's charge is floored by the report, clamped to the call's reservation)\n", d.Reported, d.Charge)
	}
	for _, w := range d.PinWarnings {
		fmt.Fprintf(&b, "Warning:  %s\n", oneLine(w))
	}
	for _, w := range []string{d.ReportedWarning, d.BandWarning} {
		if w != "" {
			fmt.Fprintf(&b, "Warning:  %s\n", oneLine(w))
		}
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
	// The messages the run was sent (F13, F18-F20), under the report path they
	// never reached. A strings.Builder's writes cannot fail, so writeMessages'
	// own failure is one this writer has none of.
	_ = writeMessages(&b, d.Messages)
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
	if len(d.Reviews) > 0 {
		fmt.Fprintf(&b, "\nReviews\n")
		for _, rv := range d.Reviews {
			fmt.Fprintf(&b, "  %s\n", oneLine(reviewLine(rv)))
		}
		// The list is bounded at diagnoseReviews, which sits above the loop's
		// own maximum: a list that reaches the bound is cut, and saying so is
		// the difference between a short list and a complete one (F19).
		if len(d.Reviews) == diagnoseReviews {
			fmt.Fprintf(&b, "  … at most %d review rounds are printed\n", diagnoseReviews)
		}
	}
	// Each level's own loop, under the rounds it spent: the cap the run's plan
	// resolved for it beside the rounds it reached (V1.3). It is the block that
	// answers "how much of its review budget did this run use", and a level
	// whose cap the record does not carry says so in words rather than printing
	// a zero.
	if len(d.ReviewLevels) > 0 {
		fmt.Fprintf(&b, "\nReview levels\n")
		for _, lv := range d.ReviewLevels {
			fmt.Fprintf(&b, "  %s\n", oneLine(reviewLevelLine(lv)))
		}
	}
	// The triage rounds, under the reviews whose rounds they judged (D1846,
	// D1850): which rounds a judge really weighed, and for each one the model
	// and account that answered, how its findings were placed and — where the
	// call failed — the FAILED word and the record's own cause. It is the block
	// that says a round was decided by the fail-safe rather than by a judge,
	// which the findings list below cannot: those findings are the round's
	// either way, and a run whose triage call failed has every one of them that
	// the mode's table blocks in it.
	if len(d.Triage) > 0 {
		fmt.Fprintf(&b, "\nTriage\n")
		for _, tr := range d.Triage {
			fmt.Fprintf(&b, "  %s\n", oneLine(triageLine(tr)))
		}
		// Bounded like the reviews above and for the same reason (F19): the
		// record's own bound is a writer's, and a hand-edited record need
		// never have been bounded at all. A list that reaches the bound is
		// cut, and saying so is the difference between a short list and a
		// complete one.
		if len(d.Triage) == runstore.MaxTriageRounds {
			fmt.Fprintf(&b, "  … at most %d triage rounds are printed\n", runstore.MaxTriageRounds)
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
		for i, mode := range modes {
			fmt.Fprintf(&b, "\nTolerated by %s\n", mode)
			for _, f := range byMode[mode] {
				fmt.Fprintf(&b, "  [%s]%s %s\n", oneLine(string(f.Label)),
					scoreCell(f.Likelihood, f.Impact, f.FixCost, string(f.Class)), oneLine(f.Title))
			}
			// The block's total is how many findings the mode really
			// tolerated, so a list shorter than that says how many it does
			// not show rather than reading as the whole of it (F19). It says
			// nothing about where the rest are: the list is short because it
			// was bounded when it was written and bounded again here, and a
			// hand-edited record — the one this guards against — need never
			// have been bounded at all. It closes the last block, which is
			// the only one it can belong to: the entries keep the reviewer's
			// order and the cut is at the end of that order.
			if i == len(modes)-1 && t.Total > len(t.Findings) {
				fmt.Fprintf(&b, "  … and %d more tolerated findings\n", t.Total-len(t.Findings))
			}
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

// checkpointBlock is a governance halt's checkpoint report (T6.1, D329) as a
// block of the human output, or "" for a run carrying none.
//
// Trigger, Why over and Estimate are the runner's own and always there; Done,
// Next and Hard come from the stopping agent's answer to the checkpoint prompt
// (T7.3), so a checkpoint written before that stage exists has them empty and
// they print as nothing at all -- no label, no empty line, no zero -- rather
// than as a field the run never filled. The trigger is not dropped as a repeat
// of the Halted line above: a checkpoint's own report is the thing a person
// continues from, and it reads whole on its own.
func checkpointBlock(cp *runstore.Checkpoint) string {
	if cp == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nCheckpoint\n")
	// field writes one labelled line, every later line of a multi-line value
	// aligned under the first. The label is padded to the longest one used
	// here, so the values of the block line up. A value's trailing newlines go
	// the way indent gives the agent message's: a line of nothing but the
	// alignment padding is not a line of the report.
	field := func(label, val string) {
		txt := strings.TrimRight(multiLine(val), "\n")
		if txt == "" {
			return
		}
		lines := strings.Split(txt, "\n")
		fmt.Fprintf(&b, "  %-10s%s\n", label+":", lines[0])
		for _, l := range lines[1:] {
			fmt.Fprintf(&b, "  %-10s%s\n", "", l)
		}
	}
	field("Trigger", cp.Trigger)
	field("Why over", cp.WhyOver)
	if cp.EstimateUSD != 0 {
		fmt.Fprintf(&b, "  %-10s$%.4f\n", "Estimate:", cp.EstimateUSD)
	}
	field("Done", cp.Done)
	field("Next", cp.Next)
	field("Hard", cp.HardInstructions)
	return b.String()
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
