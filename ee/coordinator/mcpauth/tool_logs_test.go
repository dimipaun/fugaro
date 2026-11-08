// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// testLogLine is a line of progress one of the fixture's runs recorded: the
// text of an event in its own transcript under transcripts/, which is the
// material stream_logs answers with (D932) and never a line from anywhere
// else.
const testLogLine = "the run's own recorded progress, written into its transcript by a test"

// transcriptLine is one recorded line of a stage's transcript, in the shape the
// runner stores them: claude's own stream-json, one event per line.
func transcriptLine(text string) []byte {
	return []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"` + text + `"}]}}` + "\n")
}

// TestLogsComeFromTheRunsOwnArtefacts is D932: the answer is the run's own
// recorded progress, read out of the run's own objects under its prefix, and
// no line of it comes from anywhere else (Cloud Logging is reached by no code
// here at all).
//
// Both the record's own stage list and a run that died before its record named
// a stage are read: the second is the case stream_logs exists for, and the
// files under the run's own transcripts/ prefix are what name its stages then.
//
// The read is bounded too: a run that wrote more transcripts than one call
// reads answers the newest of them and says how many it left out, because the
// run's own agent writes that prefix and the cost of a call is not its to
// choose.
func TestLogsComeFromTheRunsOwnArtefacts(t *testing.T) {
	f := newRunsFixture(t)
	f.seedRun(testSlug, testRunID, &runstore.Record{
		Status: runstore.StatusRunning,
		Stages: []runstore.StageTiming{
			{Name: "plan", StartedAt: testNow, DurationS: 30},
			{Name: "implement", StartedAt: testNow, DurationS: 12},
		},
	}, map[string][]byte{
		"transcripts/plan-1.jsonl":      transcriptLine("a plan line"),
		"transcripts/implement-1.jsonl": transcriptLine(testLogLine),
		// A stage the record has not caught up with: its transcript is the
		// run's own progress all the same, and it follows the ones the record
		// names.
		"transcripts/fix-1.jsonl": transcriptLine("a line of the stage the record has not reached yet"),
	})
	f.seedRun(testSlug, testDeadRunID, &runstore.Record{Status: runstore.StatusRunning}, map[string][]byte{
		"transcripts/plan-1.jsonl": transcriptLine("a line of the run that died before its record named the stage"),
	})

	var logs runLogs
	f.answer("stream_logs", `{"run_id":"`+testRunID+`"}`, &logs)
	if logs.RunID != testRunID || logs.Repo != testSlug {
		t.Errorf("stream_logs answered %+v; want the run it was asked for", logs)
	}
	got := logLines(logs)
	if !strings.Contains(got, testLogLine) {
		t.Fatalf("stream_logs answered %q; want the line the run's own transcripts/implement-1.jsonl recorded", got)
	}
	if !strings.Contains(got, "a line of the stage the record has not reached yet") {
		t.Errorf("stream_logs answered %q; want every transcript the run wrote, not only the stages its record has caught up with", got)
	}
	if names := stageNames(logs); !slices.Equal(names, []string{"plan", "implement", "fix"}) {
		t.Errorf("stream_logs answered the stages %v; want each transcript under the name the run wrote it as, in the order its record names them", names)
	}

	logs = runLogs{}
	f.answer("stream_logs", `{"run_id":"`+testDeadRunID+`"}`, &logs)
	if len(logs.Stages) != 1 || logs.Stages[0].Stage != "plan" ||
		!strings.Contains(logLines(logs), "the run that died before its record named the stage") {
		t.Errorf("stream_logs answered %+v for a run whose record names no stage; want the transcripts it wrote", logs)
	}

	// And what one call reads is bounded: the run's own agent writes under its
	// own prefix, so a run that wrote more transcripts than maxLogTranscripts
	// gets the newest of them and an answer that says how many it left out,
	// rather than a read whose cost the run chooses.
	const extra = 3
	timings := make([]runstore.StageTiming, 0, maxLogTranscripts+extra)
	files := map[string][]byte{}
	for i := 1; i <= maxLogTranscripts+extra; i++ {
		stage := fmt.Sprintf("s%d", i)
		timings = append(timings, runstore.StageTiming{Name: stage, StartedAt: testNow})
		files["transcripts/"+stage+"-1.jsonl"] = transcriptLine("a line of " + stage)
	}
	f.seedRun(testSlug, testCappedRunID, &runstore.Record{Status: runstore.StatusSucceeded, Stages: timings}, files)

	logs = runLogs{}
	f.answer("stream_logs", `{"run_id":"`+testCappedRunID+`"}`, &logs)
	stages := stageNames(logs)
	if len(stages) != maxLogTranscripts || stages[0] != fmt.Sprintf("s%d", extra+1) || stages[len(stages)-1] != fmt.Sprintf("s%d", maxLogTranscripts+extra) {
		t.Errorf("stream_logs answered the stages %v for a run with %d transcripts; want the newest %d, the run's own tail", stages, maxLogTranscripts+extra, maxLogTranscripts)
	}
	if !logs.Truncated || logs.OmittedTranscripts != extra {
		t.Errorf("stream_logs answered truncated=%v omitted_transcripts=%d; want the cut recorded as %d transcripts left out", logs.Truncated, logs.OmittedTranscripts, extra)
	}
	if strings.Contains(logLines(logs), "a line of s1\n") {
		t.Errorf("stream_logs read a transcript the cap leaves out: %s", logLines(logs))
	}

	// A transcript this call cannot read is left out and recorded rather than
	// failing the question: a stage's stream over runstore.MaxTranscriptBytes
	// (32 MiB) is a size the run's own agent chose, and what the caller asked
	// for is the progress the other stages recorded.
	f.seedRun(testSlug, testBigTranscriptRunID, &runstore.Record{
		Status: runstore.StatusRunning,
		Stages: []runstore.StageTiming{{Name: "plan", StartedAt: testNow}, {Name: "implement", StartedAt: testNow}},
	}, map[string][]byte{
		"transcripts/plan-1.jsonl":      transcriptLine("a line of the stage that fits"),
		"transcripts/implement-1.jsonl": append(bytes.Repeat([]byte("x"), runstore.MaxTranscriptBytes+1), '\n'),
	})

	logs = runLogs{}
	f.answer("stream_logs", `{"run_id":"`+testBigTranscriptRunID+`"}`, &logs)
	if !strings.Contains(logLines(logs), "a line of the stage that fits") {
		t.Errorf("stream_logs answered %+v for a run with one transcript over the store's read cap; want the stage it could read", logs)
	}
	if !logs.Truncated || logs.OmittedTranscripts != 1 {
		t.Errorf("stream_logs answered truncated=%v omitted_transcripts=%d with one transcript over runstore.MaxTranscriptBytes; want the one it could not read recorded", logs.Truncated, logs.OmittedTranscripts)
	}
}

// TestLogsOfARunThatRecordedNothingAreAnEmptyList: a run between its task and
// its first stage has recorded no progress, and the answer says so as an empty
// list rather than as a null — the rule runstore's own shapes are written by,
// where an empty array is a fact ("this run recorded no stage") and a null is
// not (runstore.ChangedFiles' own doc, and the same change's list_tasks, which
// answers [] for the project with no run in its window).
func TestLogsOfARunThatRecordedNothingAreAnEmptyList(t *testing.T) {
	f := newRunsFixture(t)
	f.seedRun(testSlug, testRunID, &runstore.Record{Status: runstore.StatusRunning}, nil)

	text := f.answer("stream_logs", `{"run_id":"`+testRunID+`"}`, &runLogs{})
	if !strings.Contains(text, `"stages":[]`) {
		t.Errorf("stream_logs answered %s for a run that recorded no progress; want an empty list, which is the fact, rather than a null", text)
	}
}

// TestLogsRecordTheLinesTheyDrop: the answer is a tail of the run's recorded
// lines, and it says it is a tail whenever a line was dropped. The drop
// happens as the lines arrive so that the answer's cost is not the run's
// output's, which leaves the count at the end unable to see it: a run whose
// non-empty lines are an exact multiple of maxLogLines ends with the buffer
// holding exactly maxLogLines, so only the drop itself says a line was lost.
func TestLogsRecordTheLinesTheyDrop(t *testing.T) {
	f := newRunsFixture(t)
	var transcript strings.Builder
	for i := 1; i <= 2*maxLogLines; i++ {
		transcript.Write(transcriptLine(fmt.Sprintf("line %04d", i)))
	}
	f.seedRun(testSlug, testExactRunID, &runstore.Record{
		Status: runstore.StatusRunning,
		Stages: []runstore.StageTiming{{Name: "plan", StartedAt: testNow}},
	}, map[string][]byte{"transcripts/plan-1.jsonl": []byte(transcript.String())})

	var logs runLogs
	f.answer("stream_logs", `{"run_id":"`+testExactRunID+`"}`, &logs)
	got := logLines(logs)
	if n := strings.Count(got, "\n"); n != maxLogLines || !strings.Contains(got, "line 0201") ||
		!strings.Contains(got, "line 0400") || strings.Contains(got, "line 0200") {
		t.Errorf("stream_logs answered %d line(s) that are not the last %d of the %d the run recorded: %q", n, maxLogLines, 2*maxLogLines, got)
	}
	if !logs.Truncated {
		t.Errorf("stream_logs answered the run's last %d lines of %d without recording the cut: a caller cannot tell this answer from one that dropped nothing", maxLogLines, 2*maxLogLines)
	}
	if logs.OmittedTranscripts != 0 {
		t.Errorf("stream_logs answered omitted_transcripts=%d with every transcript read: that field counts whole transcripts, not lines", logs.OmittedTranscripts)
	}
}

// TestLogsAreRedactedAndTheRawTranscriptIsNeverServed is M-g's finding, and
// the reason stream_logs exists in this shape: the run's own session file can
// hold a credential the agent printed (internal/runner/session.go), so it is
// never served, and what is served — the recorded progress — is redacted
// through internal/safetext, so a line the run wrote cannot rewrite what its
// reader sees.
func TestLogsAreRedactedAndTheRawTranscriptIsNeverServed(t *testing.T) {
	f := newRunsFixture(t)
	const uuid = "9b2f0c1d-4e5a-4b6c-8d7e-1f2a3b4c5d6e"
	const sessionLine = `{"type":"assistant","text":"a token the agent printed: ` + testSeededToken + `"}`
	f.seedRun(testSlug, testRunID, &runstore.Record{
		Status: runstore.StatusRunning,
		Stages: []runstore.StageTiming{{Name: "plan", StartedAt: testNow}},
	}, map[string][]byte{
		// The recorded line is deliberately not well-formed JSON: a
		// transcript is the run's own bytes and is not trusted to be, so a
		// raw escape byte in one is escaped like the bidi override that a
		// JSON encoder would have written raw.
		"transcripts/plan-1.jsonl":   transcriptLine("a recorded line\x1b[31m with an escape and a ‮ reversal"),
		"session/session.json":       []byte(`{"version":1,"id":"` + uuid + `","head_sha":"deadbeef","pushed":true,"workdir":"/work","bytes":123}`),
		"session/" + uuid + ".jsonl": []byte(sessionLine),
	})

	var logs runLogs
	text := f.answer("stream_logs", `{"run_id":"`+testRunID+`"}`, &logs)
	got := logLines(logs)
	if !strings.Contains(got, "a recorded line") {
		t.Fatalf("stream_logs answered %q; want the run's own recorded progress", got)
	}
	if strings.Contains(text, testSeededToken) {
		t.Errorf("stream_logs served the run's session file, which is never served: %s", text)
	}
	if strings.ContainsAny(got, "\x1b‮") || strings.ContainsAny(text, "\x1b‮") {
		t.Errorf("a recorded line reached the answer unredacted: %q", text)
	}
}

// stageNames is one answer's stages, in the order it answered them.
func stageNames(logs runLogs) []string {
	out := make([]string, 0, len(logs.Stages))
	for _, s := range logs.Stages {
		out = append(out, s.Stage)
	}
	return out
}

// logLines is one answer's recorded lines, in stage order, as one string to
// read an assertion against.
func logLines(logs runLogs) string {
	var b strings.Builder
	for _, s := range logs.Stages {
		for _, line := range s.Lines {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	return b.String()
}
