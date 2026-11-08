// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// This file is M8's reach and its two record-reading tools: get_task_status
// and get_result. The run's recorded progress is tool_logs.go's and the
// project's run list is tool_list.go's.
//
// **Every read goes through the project's own list.** A caller names a run,
// and that name is resolved against the repositories this project's own
// configuration lists before it names any object (spec.md §8): a slug it
// carries has to be one of them, a bare identifier has to have a run ID's own
// shape, and a name that is neither is refused. Nothing a caller sends is ever
// used as a key into the bucket as it was sent, so a token cannot read a run
// of another repository in the installation's bucket, and cannot walk out of
// the project's own prefix with the value it passed.
//
// **What the tools answer is redacted, and it is data** (spec.md §5, M-g). A
// handler answers a JSON *string* rather than a struct, and that is the
// redaction's own requirement: the door writes whatever a handler returns
// through answerText, so a struct would be marshalled after this file is done
// with it and a character that rewrites what its reader sees would be encoded
// by encoding/json, which escapes the controls and not the bidi overrides.
// Every string a run wrote goes through internal/safetext here instead, and
// no answer is a sentence this project speaks about a run: each value is
// carried under its own key and nothing is composed out of it.

// slugRE is the alphabet the runs tree is keyed by, task.Slug's own: it is
// spelled here as well because the one it is read from is unexported
// (internal/runstore/list.go), and the reach below is where a project whose
// list holds something that is not a slug is a wiring mistake rather than a
// call that fails for whoever asks next. With no "." allowed, a slug cannot be
// "." or ".." either.
var slugRE = regexp.MustCompile(`^[a-z0-9-]+$`)

// ProjectRuns is the reach M8's four read tools read through: the runs bucket
// the installation already owns (M4's, whose read the front door's own account
// holds) and the repositories this project is made of.
//
// **The repositories are the fence.** An installation's runs bucket holds a
// run of every repository somebody launched from its project, and a project is
// the repositories its own published list names — the same list the
// coordinator reconciles, derived through task.Slug because that is what the
// runs tree is keyed by. Every read below walks that list and nothing else,
// so a run of another repository in the same bucket is not named, not read and
// not reachable by the identifier it is named by. That is spec.md §5's "must
// not list another project's runs", held by construction rather than by a
// filter a later writer has to remember.
type ProjectRuns struct {
	bucket *blob.Bucket
	slugs  []string
	// now is the clock the list's default window is taken against, so a test
	// reads a list of a fixed day rather than of the day it runs (the seam
	// Server.now is).
	now func() time.Time
}

// NewProjectRuns returns the reach over bucket and the project's own
// repositories, as slugs.
//
// A list with no repository in it is refused rather than admitted: every tool
// over it would answer as if the project had never run anything, which reads
// as a working door with nothing behind it instead of as the wiring mistake it
// is. The slugs are sorted and deduplicated so that two projects with the same
// repositories answer the same order.
func NewProjectRuns(bucket *blob.Bucket, slugs []string) (*ProjectRuns, error) {
	if bucket == nil {
		return nil, errors.New("mcpauth: the read tools need the installation's runs bucket")
	}
	if len(slugs) == 0 {
		return nil, errors.New("mcpauth: the read tools need the project's own repositories, and this project's list names none")
	}
	out := make([]string, 0, len(slugs))
	for _, s := range slugs {
		if !slugRE.MatchString(s) {
			return nil, fmt.Errorf("mcpauth: %q is not a repository slug, so the runs tree holds no run of it", s)
		}
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return &ProjectRuns{bucket: bucket, slugs: out, now: time.Now}, nil
}

// RegisterReadTools registers M8's four read tools on reg, over reach.
//
// The four names are the owner's own (spec.md §5) and their scopes are not
// this call's to choose: Register refuses a name the catalogue does not map,
// and the registry reads the scope it enforces out of that table on every
// call. What a wiring task supplies is the reach and nothing else.
func RegisterReadTools(reg *Registry, reach *ProjectRuns) error {
	if reg == nil || reach == nil {
		return errors.New("mcpauth: the read tools need a registry and the project's own runs")
	}
	for _, t := range []Tool{
		{Name: "get_task_status", InputSchema: runIDArgsSchema, Handler: reach.getTaskStatus},
		{Name: "stream_logs", InputSchema: runIDArgsSchema, Handler: reach.streamLogs},
		{Name: "get_result", InputSchema: runIDArgsSchema, Handler: reach.getResult},
		{Name: "list_tasks", InputSchema: listTasksArgsSchema, Handler: reach.listTasks},
	} {
		if err := reg.Register(t); err != nil {
			return err
		}
	}
	return nil
}

// runIDArgsSchema is the schema of the three tools that take one run: the run
// a caller means, as its own ID or as <repo-slug>/<run-id>.
var runIDArgsSchema = json.RawMessage(`{"type":"object","properties":{"run_id":{"type":"string","description":"The run's ID, or <repo-slug>/<run-id>"}},"required":["run_id"],"additionalProperties":false}`)

// runIDArgs is what those three tools read out of a call's arguments.
type runIDArgs struct {
	RunID string `json:"run_id"`
}

// args decodes one call's arguments into v.
//
// An argument this tool does not know is refused rather than ignored, which is
// the rule every other format in this repository is decoded by: a client that
// misspells a filter and is answered with everything has been answered by a
// tool that did not read what it sent. The refusal quotes the decoder's own
// message, which names the field the caller sent and no value of ours.
func args(raw json.RawMessage, v any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("the arguments are not the ones this tool takes: %w", err)
	}
	return nil
}

// locate resolves the run a caller named to one of the project's own runs.
//
// The rule is spec.md §8's: a name is resolved through the project's own list
// and is never used as a key. A name carrying a slug has to parse as
// <repo-slug>/<run-id> and its slug has to be one of the project's; a name
// without one has to have a run ID's own shape; and the last step builds an
// object name out of the project's own slug and that identifier, never out of
// the text the caller sent.
//
// A run is found by the task.json under its prefix, which is what makes a run
// a run in this tree (runstore.Locate reads the same marker). A run ID that is
// a run of more than one of the project's repositories is refused rather than
// guessed at, and the refusal names no repository: which repositories the
// installation holds is not a stranger's to learn from a refusal.
func (p *ProjectRuns) locate(ctx context.Context, ref string) (*runstore.Store, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, errors.New("a run is needed: pass the run's ID, or <repo-slug>/<run-id>")
	}
	slug, runID := "", ref
	if strings.Contains(ref, "/") {
		var err error
		if slug, runID, err = runstore.ParseRef(ref); err != nil {
			return nil, fmt.Errorf("%q is not a run this project can be asked about: %w", safetext.Field(ref), err)
		}
		if !slices.Contains(p.slugs, slug) {
			return nil, fmt.Errorf("no run %s in this project's repositories: %w", safetext.Field(ref), runstore.ErrNotFound)
		}
	} else if _, err := runstore.RunTime(ref); err != nil {
		return nil, fmt.Errorf("%q is neither a run ID nor <repo-slug>/<run-id>", safetext.Field(ref))
	}
	var found []string
	for _, s := range p.slugs {
		if slug != "" && s != slug {
			continue
		}
		ok, err := p.bucket.Exists(ctx, runstore.Open(p.bucket, s, runID).Prefix()+"task.json")
		if err != nil {
			return nil, fmt.Errorf("looking up the run %s: %w", safetext.Field(ref), err)
		}
		if ok {
			found = append(found, s)
		}
	}
	switch len(found) {
	case 0:
		return nil, fmt.Errorf("no run %s in this project's repositories: %w", safetext.Field(ref), runstore.ErrNotFound)
	case 1:
		return runstore.Open(p.bucket, found[0], runID), nil
	}
	return nil, fmt.Errorf("run ID %s is a run of more than one of this project's repositories; pass <repo-slug>/<run-id>", runID)
}

// answerJSON renders a tool's answer: the document, made safe for whoever
// reads it.
//
// Every string a run wrote was put through safetext where it was read, and the
// whole document is passed through it again here, because the one thing this
// function can promise regardless of which field a later writer adds is that
// no answer leaves this package carrying a character that rewrites what its
// reader sees (M-g).
func answerJSON(v any) (any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encoding the answer: %w", err)
	}
	return safetext.MultiLine(string(data)), nil
}

// taskStatus is one run as the read tools answer it: the run's own record and
// the two facts this project knows about it (the repository it is a run of and
// its ID), and nothing else.
//
// The vocabulary is the run record's own — status, stage, outcome, reason and
// the failure class are the record's fields under the record's names — so a
// caller reading an answer and a reader of result.json read one shape.
type taskStatus struct {
	RunID string `json:"run_id"`
	Repo  string `json:"repo"`
	// Status is the record's status, or running for a run that has written no
	// record yet: a run whose record is missing is one that is in flight,
	// which is the direction that reports work rather than hiding it (the
	// rule runview applies to the same case).
	Status       string `json:"status"`
	Stage        string `json:"stage,omitempty"`
	Outcome      string `json:"outcome,omitempty"`
	Reason       string `json:"reason,omitempty"`
	FailureClass string `json:"failure_class,omitempty"`
	Branch       string `json:"branch,omitempty"`
	// PR is the pull request the run opened or updated, number and URL as the
	// record holds them.
	PR         *runstore.PRRef `json:"pr,omitempty"`
	StartedAt  string          `json:"started_at,omitempty"`
	FinishedAt string          `json:"finished_at,omitempty"`
	CostUSD    float64         `json:"cost_usd,omitempty"`
}

// statusOf is one run's record as the tools answer it. Every string the run
// wrote is made safe here, once, rather than at each site that answers it.
func statusOf(slug, runID string, rec *runstore.Record) taskStatus {
	st := taskStatus{
		RunID:        runID,
		Repo:         slug,
		Status:       safetext.OneLine(string(rec.Status)),
		Stage:        safetext.OneLine(rec.Stage),
		Outcome:      safetext.OneLine(string(rec.Outcome)),
		Reason:       safetext.OneLine(rec.Reason),
		FailureClass: safetext.OneLine(string(rec.FailureClass)),
		Branch:       safetext.OneLine(rec.Branch),
		CostUSD:      rec.CostUSD,
	}
	if !rec.StartedAt.IsZero() {
		st.StartedAt = rec.StartedAt.UTC().Format(time.RFC3339)
	}
	if rec.FinishedAt != nil {
		st.FinishedAt = rec.FinishedAt.UTC().Format(time.RFC3339)
	}
	if rec.PR != nil {
		st.PR = &runstore.PRRef{Number: rec.PR.Number, URL: safetext.OneLine(rec.PR.URL)}
	}
	return st
}

// status is one of the project's own runs as a run: its record, or the one
// fact a run with no record yet has.
//
// A record that exists and cannot be read is a refusal, and not an answer of
// "running": the run's state is not this package's to invent, and an answer
// that turned a read failure into a status is one a caller cannot tell from
// the truth.
func (p *ProjectRuns) status(ctx context.Context, slug, runID string) (taskStatus, error) {
	rec, err := runstore.Open(p.bucket, slug, runID).ReadRecord(ctx)
	switch {
	case errors.Is(err, runstore.ErrNotFound):
		return taskStatus{RunID: runID, Repo: slug, Status: string(runstore.StatusRunning)}, nil
	case err != nil:
		return taskStatus{}, fmt.Errorf("reading the record of run %s: %w", runID, err)
	}
	return statusOf(slug, runID, rec), nil
}

// getTaskStatus is the get_task_status tool: the state of one of the project's
// own runs, from its own run record.
func (p *ProjectRuns) getTaskStatus(ctx context.Context, call Call) (any, error) {
	var a runIDArgs
	if err := args(call.Arguments, &a); err != nil {
		return nil, err
	}
	st, err := p.locate(ctx, a.RunID)
	if err != nil {
		return nil, err
	}
	status, err := p.status(ctx, st.Slug(), st.RunID())
	if err != nil {
		return nil, err
	}
	return answerJSON(status)
}

// runResult is one run's outcome as get_result answers it: the record's own
// account of what became of the run, the files it changed and the diff it
// uploaded under its own prefix.
//
// There is no body from a forge in it and no read of one: the pull request is
// the record's line, and the diff is the run's own ab/patch.diff object
// (D932). A token that reached a forge body, a run's log or its session is
// therefore not a token this tool can hand on (§9).
type runResult struct {
	RunID             string          `json:"run_id"`
	Repo              string          `json:"repo"`
	Status            string          `json:"status"`
	Outcome           string          `json:"outcome,omitempty"`
	Reason            string          `json:"reason,omitempty"`
	FailureClass      string          `json:"failure_class,omitempty"`
	PR                *runstore.PRRef `json:"pr,omitempty"`
	ChangedFiles      []string        `json:"changed_files,omitempty"`
	ChangedFilesTotal int             `json:"changed_files_total,omitempty"`
	Patch             string          `json:"patch,omitempty"`
	PatchTruncated    bool            `json:"patch_truncated,omitempty"`
}

// maxPatchBytes bounds the diff one answer carries. A run's own patch object
// is read under the runs bucket's record cap (runstore.MaxRecordBytes), and an
// answer is a summary a client reads rather than a download: past this the
// head of the diff is served and the cut is recorded, as the record records
// the same fact for the object (Record.PatchTruncated).
const maxPatchBytes = 256 << 10

// getResult is the get_result tool: the outcome of one of the project's own
// runs — what became of it, the pull request it is on, the files it changed
// and the diff it uploaded.
func (p *ProjectRuns) getResult(ctx context.Context, call Call) (any, error) {
	var a runIDArgs
	if err := args(call.Arguments, &a); err != nil {
		return nil, err
	}
	st, err := p.locate(ctx, a.RunID)
	if err != nil {
		return nil, err
	}
	status, err := p.status(ctx, st.Slug(), st.RunID())
	if err != nil {
		return nil, err
	}
	res := runResult{
		RunID:        status.RunID,
		Repo:         status.Repo,
		Status:       status.Status,
		Outcome:      status.Outcome,
		Reason:       status.Reason,
		FailureClass: status.FailureClass,
		PR:           status.PR,
	}
	if rec, err := st.ReadRecord(ctx); err == nil && rec.ChangedFiles != nil {
		if cf := rec.ChangedFiles.Bounded(); cf != nil {
			res.ChangedFiles = make([]string, 0, len(cf.Paths))
			for _, path := range cf.Paths {
				res.ChangedFiles = append(res.ChangedFiles, safetext.OneLine(path))
			}
			res.ChangedFilesTotal = cf.Total
		}
	}
	patch, truncated, err := p.patch(ctx, st)
	if err != nil {
		return nil, err
	}
	res.Patch, res.PatchTruncated = patch, truncated
	return answerJSON(res)
}

// patch reads the diff the run uploaded under its own prefix
// (runstore.PatchName): redacted line by line, cut at maxPatchBytes, and
// marked as cut when it was.
//
// The object is read directly rather than through the record's PatchObject
// because the object is the diff: a run killed between uploading it and
// writing the field still has one, and get_result is a tool a caller reaches
// for exactly then. An object that is not there is a run that uploaded no
// diff, which is not an error.
func (p *ProjectRuns) patch(ctx context.Context, st *runstore.Store) (string, bool, error) {
	data, err := st.ReadFile(ctx, runstore.PatchName)
	if errors.Is(err, runstore.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("reading the run's own diff: %w", err)
	}
	truncated := false
	if len(data) > maxPatchBytes {
		data, truncated = data[:maxPatchBytes], true
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	for i, line := range lines {
		lines[i] = safetext.OneLine(line)
	}
	return strings.Join(lines, "\n"), truncated, nil
}
