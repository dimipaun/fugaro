package runner

import (
	"fmt"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/verify"
)

// Report renders the run report posted to the PR and stored as report.md.
func Report(rec *runstore.Record, location string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### Fugaro run `%s`\n\n", rec.RunID)
	if rec.Outcome == runstore.OutcomeReady {
		b.WriteString("**Outcome:** ready for review\n\n")
	} else {
		fmt.Fprintf(&b, "**Outcome:** draft — %s\n\n", rec.Reason)
	}
	if len(rec.Stages) > 0 {
		b.WriteString("| Stage | Duration |\n|---|---|\n")
		for _, s := range rec.Stages {
			d := time.Duration(s.DurationS * float64(time.Second)).Round(time.Second)
			fmt.Fprintf(&b, "| %s | %s |\n", s.Name, d)
		}
		b.WriteString("\n")
	}
	if len(rec.Reviews) > 0 {
		parts := make([]string, 0, len(rec.Reviews))
		for _, r := range rec.Reviews {
			p := fmt.Sprintf("round %d: %s", r.Round, r.Verdict)
			if r.Verdict != "ship" {
				p += " (" + Plural(r.Findings, "finding") + ")"
			}
			parts = append(parts, p)
		}
		fmt.Fprintf(&b, "**Reviews:** %s\n\n", strings.Join(parts, "; "))
	}
	b.WriteString(testsLine(rec))
	fmt.Fprintf(&b, "**Cost:** $%.2f\n\n", rec.CostUSD)
	fmt.Fprintf(&b, "Transcripts and verify records: `%s` in the runs bucket.\n", location)
	return b.String()
}

func testsLine(rec *runstore.Record) string {
	var last *verify.Record
	for i := range rec.Verify {
		if rec.Verify[i].Kind == verify.KindTest && rec.Verify[i].HeadSHA == rec.HeadSHA {
			last = &rec.Verify[i]
		}
	}
	if last == nil {
		return "**Tests:** no recorded test run on the final commit\n\n"
	}
	status := "passed"
	if !last.Passed {
		status = "failed"
	}
	sha := last.HeadSHA
	if len(sha) > 7 {
		sha = sha[:7]
	}
	s := fmt.Sprintf("**Tests:** %s on %s (%s, %s", status, sha, Plural(last.Tests, "test"), Plural(last.Failures, "failure"))
	if len(last.Flaky) > 0 {
		s += ", flaky: " + strings.Join(last.Flaky, ", ")
	}
	return s + ")\n\n"
}
