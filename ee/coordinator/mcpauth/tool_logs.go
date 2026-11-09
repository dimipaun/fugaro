// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// This file is M8's stream_logs: the progress one run recorded, from the run's
// own artefacts (D932) and from nowhere else.
//
// **The material is the run's own stage transcripts**, transcripts/<stage>-<n>
// .jsonl under the run's own prefix: the runner writes one per stage attempt,
// the stage's own stream as it arrived, and it is the same material the
// console and a diagnosis read. Nothing here reaches Cloud Logging: the whole
// of the front door's read is the runs bucket, and a line this tool answers
// with is a line some run of this project wrote into its own object.
//
// **The run's session file is never served** (M-g, finding of 2026-11-04).
// session/<id>.jsonl is a Claude Code session, recorded by the runner
// (internal/runner/session.go), and it can hold a credential the agent
// printed — a log line, an echoed environment, a command it ran. It is the one
// artefact of a run that must not reach a caller who may hold nothing but a
// bearer token, so it is not named anywhere in this file: what is served is
// the progress record, and the session file is not part of it.
//
// **What is served is redacted one line at a time** (internal/safetext): a
// transcript's bytes are the run's, and a line of them can carry a control, an
// escape sequence or a bidi override that would rewrite what a client, or a
// person reading the client, sees. The transcript is not trusted to be
// well-formed JSON either — it is copied from a process's stdout — so the
// redaction is on the line and not on any field of an event parsed out of it.

// stageRE is the alphabet the runner writes a stage's name in: the stages the
// runner runs are bootstrap, plan, implement, review, review_first, fix,
// check, provision, render, writeback and finalize. A name that is not one is
// never used to name an object, which is the rule runstore applies to a
// session ID and for the same reason: a record is writable by the run's own
// agent, and a value it wrote is a value that could name its way out of the
// run's own prefix.
var stageRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// transcriptRE is one transcript's leaf as the runner writes it:
// transcripts/<stage>-<n>.jsonl, n the stage's own attempt number
// (internal/runner's stageN, the same count a diagnosis names them by).
var transcriptRE = regexp.MustCompile(`^([a-z][a-z0-9_]*)-[0-9]{1,3}\.jsonl$`)

const (
	// transcriptsPrefix is the leaf a run's stage transcripts live under,
	// inside the run's own prefix.
	transcriptsPrefix = "transcripts/"
	// maxLogTranscripts, maxLogLines and maxLogLineRunes bound one call. A
	// transcript is a stage's whole stream and is capped at 32 MiB
	// (runstore.MaxTranscriptBytes), so what a caller gets is a tail — of the
	// run's transcripts and of each one's lines — and what a call costs is the
	// cap rather than what the run wrote: the run's own agent writes objects
	// under its own prefix, and without the count a run that wrote a hundred
	// transcripts would make one read of it a hundred bucket reads. The oldest
	// transcripts and the oldest lines are the ones left out.
	maxLogTranscripts = 20
	maxLogLines       = 200
	maxLogLineRunes   = 2000
)

// logStage is one stage's recorded lines, under the stage's own name.
type logStage struct {
	Stage string   `json:"stage"`
	Lines []string `json:"lines"`
}

// runLogs is one run's recorded progress: the stages the run recorded, in the
// order it recorded them.
type runLogs struct {
	RunID  string     `json:"run_id"`
	Repo   string     `json:"repo"`
	Stages []logStage `json:"stages"`
	// Truncated is set when the answer is a tail: the run recorded more lines
	// than maxLogLines, wrote more transcripts than maxLogTranscripts, or wrote
	// one this call could not read. What is left out is the older material, and
	// OmittedTranscripts says how much of it is whole transcripts.
	Truncated bool `json:"truncated,omitempty"`
	// OmittedTranscripts is how many of the run's own transcripts this answer
	// read nothing out of — the oldest ones the cap left out, and any the call
	// could not read — so that a caller can tell a cut of the lines from a cut
	// of the transcripts. It is non-zero only where Truncated is set.
	OmittedTranscripts int `json:"omitted_transcripts,omitempty"`
}

// streamLogs is the stream_logs tool: the progress one of the project's own
// runs recorded.
func (p *ProjectRuns) streamLogs(ctx context.Context, call Call) (any, error) {
	var a runIDArgs
	if err := args(call.Arguments, &a); err != nil {
		return nil, err
	}
	st, err := p.locate(ctx, a.RunID)
	if err != nil {
		return nil, err
	}
	names, err := p.transcripts(ctx, st)
	if err != nil {
		return nil, err
	}
	// Stages starts as an empty list rather than as nothing: a run that has
	// recorded no progress has recorded no stage, which is a fact about it, and
	// an answer that spelled it null would read as one with no answer at all
	// (runstore's own shapes are written the same way — ChangedFiles.Paths is
	// never null).
	logs := runLogs{RunID: st.RunID(), Repo: st.Slug(), Stages: []logStage{}}
	// The transcripts read are capped, oldest first: the answer is a tail, and
	// how many objects one call reads is the cap's to say rather than the run's.
	if len(names) > maxLogTranscripts {
		logs.OmittedTranscripts = len(names) - maxLogTranscripts
		logs.Truncated = true
		names = names[len(names)-maxLogTranscripts:]
	}
	var lines []recorded // the last maxLogLines recorded lines, in order
	dropped := false     // whether any line was left out, by either cut below
	for _, name := range names {
		data, err := st.ReadFile(ctx, transcriptsPrefix+name.name+".jsonl")
		// A transcript this call cannot read is left out and recorded, like one
		// the cap leaves out: an agent writes its own stage's stream, so one
		// over runstore.MaxTranscriptBytes (32 MiB) is a size the run chose,
		// and refusing the whole answer for it would lose the progress the
		// other stages recorded — the case this tool is reached for. A
		// diagnosis reads the same failure the same way and answers no result
		// for that stage (internal/cli/diagnose.go:719).
		if errors.Is(err, runstore.ErrNotFound) || errors.Is(err, runstore.ErrTooLarge) {
			logs.OmittedTranscripts++
			logs.Truncated = true
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading the run's own transcript %s: %w", name.name, err)
		}
		for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
			if line == "" {
				continue
			}
			lines = append(lines, recorded{name.stage, safetext.Clip(safetext.OneLine(line), maxLogLineRunes)})
			// The tail is what the answer is, so the lines before the last
			// maxLogLines are dropped as they arrive rather than held to the
			// end: a transcript is a stage's whole stream, and holding two of
			// them to read the last few hundred lines of it would make the
			// answer's cost a function of the run's own output. The drop is
			// what `dropped` is: the count below cannot see it, because the
			// trim leaves the buffer holding exactly maxLogLines.
			if len(lines) == 2*maxLogLines {
				lines = append(lines[:0], lines[maxLogLines:]...)
				dropped = true
			}
		}
	}
	if len(lines) > maxLogLines {
		lines, dropped = lines[len(lines)-maxLogLines:], true
	}
	if dropped {
		logs.Truncated = true
	}
	for _, line := range lines {
		if n := len(logs.Stages); n > 0 && logs.Stages[n-1].Stage == line.stage {
			logs.Stages[n-1].Lines = append(logs.Stages[n-1].Lines, line.text)
			continue
		}
		logs.Stages = append(logs.Stages, logStage{Stage: line.stage, Lines: []string{line.text}})
	}
	return answerJSON(logs)
}

// recorded is one line of a run's own progress: the stage it belongs to and
// the redacted text.
type recorded struct {
	stage string
	text  string
}

// transcript is one transcript of a run: the stage it belongs to and the leaf
// it is stored under.
type transcript struct {
	stage string
	name  string
}

// transcripts names the transcripts a run wrote: every one under the run's own
// prefix, ordered the way the run wrote them.
//
// The set comes from the run's own objects and not from its record, because
// the record is written at checkpoints and lags its own run: a stage whose
// transcript the bucket holds is progress the run recorded, whether or not the
// record has caught up with it. That is also the case this tool is reached
// for — a run that died mid-stage — so nothing may be left out for the
// record's sake.
//
// The order comes from the record's stage timings: each timing is one attempt,
// the n-th timing of a stage is that stage's n-th attempt, and the runner's
// file for it is <stage>-<n>.jsonl. A transcript the record does not name
// keeps the bucket's own order (which is the names'), after the named ones. A
// stage name that is not one the runner writes is skipped rather than used to
// name anything: the record is writable by the run's own agent.
func (p *ProjectRuns) transcripts(ctx context.Context, st *runstore.Store) ([]transcript, error) {
	found, err := p.transcriptKeys(ctx, st)
	if err != nil {
		return nil, err
	}
	order := map[string]int{}
	var named []string
	if rec, err := st.ReadRecord(ctx); err == nil {
		attempts := map[string]int{}
		for _, timing := range rec.Stages {
			if !stageRE.MatchString(timing.Name) {
				continue
			}
			attempts[timing.Name]++
			name := fmt.Sprintf("%s-%d", timing.Name, attempts[timing.Name])
			if _, seen := order[name]; !seen {
				order[name] = len(named)
				named = append(named, name)
			}
		}
	}
	slices.SortStableFunc(found, func(a, b transcript) int {
		ai, aNamed := order[a.name]
		bi, bNamed := order[b.name]
		switch {
		case aNamed && bNamed:
			return ai - bi
		case aNamed:
			return -1
		case bNamed:
			return 1
		}
		return 0
	})
	return found, nil
}

// transcriptKeys lists the transcripts under the run's own prefix, in the
// bucket's own order, which is their names' — so a stage's attempts stay
// together and in order.
func (p *ProjectRuns) transcriptKeys(ctx context.Context, st *runstore.Store) ([]transcript, error) {
	prefix := st.Prefix() + transcriptsPrefix
	it := p.bucket.List(&blob.ListOptions{Prefix: prefix})
	var out []transcript
	for {
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("listing %s: %w", prefix, err)
		}
		if obj.IsDir {
			continue
		}
		leaf := strings.TrimPrefix(obj.Key, prefix)
		m := transcriptRE.FindStringSubmatch(leaf)
		if m == nil {
			continue
		}
		out = append(out, transcript{stage: m[1], name: strings.TrimSuffix(leaf, ".jsonl")})
	}
}
