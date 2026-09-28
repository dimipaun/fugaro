package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/logtail"
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
	Row          runview.Row      `json:"row"`
	Verify       []verify.Record  `json:"verify,omitempty"`   // result.json's verify records
	Failed       []string         `json:"failed,omitempty"`   // the last test record's failed tests
	Flaky        []string         `json:"flaky,omitempty"`    // the last test record's flaky tests
	Findings     []runner.Finding `json:"findings,omitempty"` // the last review's findings
	AgentMessage string           `json:"agent_message,omitempty"`
	LogTail      []string         `json:"log_tail,omitempty"`
	ReportPath   string           `json:"report_path"`
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
	// explains it (C-M11); it just has no logs.
	slug, id, err := locateRun(ctx, env, ref)
	if err != nil {
		return err
	}
	s := runstore.Open(env.bucket.Bucket, slug, id)
	l, err := ownerLaunch(ctx, env, s, id)
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
	return printDiagnosis(cmd.OutOrStdout(), d, o.asJSON)
}

// diagnose gathers run's diagnosis; l is its launch, nil when it never
// launched (no logs then). A failed log read is a warning: the rest of the
// diagnosis is still worth having.
func diagnose(ctx context.Context, env *cloudEnv, s *runstore.Store, l *runstore.Launch, run string, secrets []string, warn io.Writer) (*Diagnosis, error) {
	red := func(s string) string { return agent.Redact(s, secrets) }
	rows, err := loadRows(ctx, env, lsFilter{runRef: run, warn: warn}, time.Now())
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, remote(fmt.Errorf("run %s: expected one row, got %d", run, len(rows)))
	}
	d := &Diagnosis{Row: rows[0], ReportPath: s.Prefix() + "report.md"}
	d.Row.Reason = red(d.Row.Reason)

	rec, err := absent(s.ReadRecord(ctx))
	if err != nil {
		return nil, remote(err)
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
		tail[seen%diagnoseLogLines] = humanLogLine(redactEntry(e, secrets))
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
	}
	for i := max(0, seen-diagnoseLogLines); i < seen; i++ {
		d.LogTail = append(d.LogTail, tail[i%diagnoseLogLines])
	}
	return d, nil
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
	// Every string from the run goes through oneLine or multiLine (I1).
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
	fmt.Fprintf(&b, "%s\n", oneLine(strings.ReplaceAll(runner.CostLine(r.Cost), "**", "")))
	if r.PRURL != "" {
		fmt.Fprintf(&b, "PR:       %s\n", oneLine(r.PRURL))
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

// indent prefixes every line of s with two spaces.
func indent(s string) string {
	return "  " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n  ")
}
