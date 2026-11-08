// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/ee/coordinator"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// This file is M8's list_tasks: the project's recent runs, newest first.
//
// **The listing is the project's own runs and nothing else** (spec.md §5). It
// walks the repositories of ProjectRuns and never the bucket's whole tree, so
// a run of another repository in the installation's bucket cannot appear in an
// answer however recent it is — the fence is the list of slugs and not a
// filter applied to a wider read.
//
// **The window and the count are both bounded.** A window is what keeps one
// call's cost proportional to recent work rather than to everything a
// repository ever ran — the same thirty days the coordinator's own tick reads
// (coordinator.RunsWindow) — and the count is what keeps an answer a client
// reads. Both are the caller's to narrow and neither is the caller's to widen
// past what the project answers with: a count over the ceiling is refused, and
// so is a `since` before the window, because the bucket read behind the answer
// is an offset into the run IDs and a time before them would walk the whole
// history (runstore.ListRunIDs) with no number of runs to say so.

// listTasksArgsSchema is the schema of list_tasks: which of the project's
// repositories to list, how many runs, and how far back.
var listTasksArgsSchema = json.RawMessage(`{"type":"object","properties":{"repo":{"type":"string","description":"One of this project's repositories, as a slug; every one of them when absent"},"limit":{"type":"integer","minimum":1,"maximum":100,"description":"How many runs to answer, newest first (default 20)"},"since":{"type":"string","description":"Only runs minted at or after this RFC 3339 time, never earlier than the last 30 days (default: the last 30 days)"}},"additionalProperties":false}`)

// listTasksArgs is what list_tasks reads out of a call's arguments.
type listTasksArgs struct {
	Repo  string `json:"repo"`
	Limit int    `json:"limit"`
	Since string `json:"since"`
}

const (
	// defaultListLimit is how many runs list_tasks answers when a caller names
	// no number, and maxListLimit is the most it will answer. The ceiling is
	// there for the reads behind the answer: one run's record is read per row,
	// so the count is the number of bucket reads one call makes.
	defaultListLimit = 20
	maxListLimit     = 100
)

// listTasks is the list_tasks tool: the project's recent runs, newest first.
func (p *ProjectRuns) listTasks(ctx context.Context, call Call) (any, error) {
	var a listTasksArgs
	if err := args(call.Arguments, &a); err != nil {
		return nil, err
	}
	slugs := p.slugs
	if a.Repo != "" {
		if !slices.Contains(p.slugs, a.Repo) {
			return nil, fmt.Errorf("no repository %q in this project: list_tasks answers this project's own runs", safetext.Field(a.Repo))
		}
		slugs = []string{a.Repo}
	}
	limit := defaultListLimit
	switch {
	case a.Limit < 0 || a.Limit > maxListLimit:
		return nil, fmt.Errorf("limit %d is outside 1..%d", a.Limit, maxListLimit)
	case a.Limit > 0:
		limit = a.Limit
	}
	now := p.now().UTC()
	since := now.Add(-coordinator.RunsWindow)
	if a.Since != "" {
		at, err := time.Parse(time.RFC3339, a.Since)
		if err != nil {
			return nil, fmt.Errorf("since %q is not an RFC 3339 time", safetext.Field(a.Since))
		}
		// A since before the window is refused rather than clamped onto it: the
		// answer carries no window of its own, so a caller whose `since` had
		// been narrowed under it could not tell the rows from everything it
		// asked for. The refusal is also what teaches the bound.
		if at.Before(since) {
			return nil, fmt.Errorf("since %s is earlier than the last %d days list_tasks answers over",
				safetext.Field(a.Since), coordinator.RunsWindow/(24*time.Hour))
		}
		since = at
	}
	var found []listed
	for _, slug := range slugs {
		ids, err := runstore.ListRunIDs(ctx, p.bucket, slug, since)
		if err != nil {
			return nil, fmt.Errorf("listing the runs of %s: %w", slug, err)
		}
		for _, id := range ids {
			at, err := runstore.RunTime(id)
			if err != nil {
				continue
			}
			found = append(found, listed{slug: slug, runID: id, at: at})
		}
	}
	slices.SortFunc(found, func(a, b listed) int {
		if c := b.at.Compare(a.at); c != 0 {
			return c
		}
		if c := strings.Compare(a.slug, b.slug); c != 0 {
			return c
		}
		return strings.Compare(a.runID, b.runID)
	})
	if len(found) > limit {
		found = found[:limit]
	}
	rows := make([]taskStatus, 0, len(found))
	for _, l := range found {
		status, err := p.status(ctx, l.slug, l.runID)
		switch {
		case err == nil:
			rows = append(rows, status)
		case unreadableRecord(err):
			// One run's own object is not the project's listing: a record that
			// does not decode costs its row its status and nothing else, which
			// is what internal/cli/runs.go does with the same failure
			// (corruptObject: "that is the run's problem, not the listing's").
			// Answering the whole list with a refusal because one run of it
			// wrote a bad object would make the other runs unreachable.
			rows = append(rows, taskStatus{RunID: l.runID, Repo: l.slug, Unreadable: true})
		default:
			return nil, err
		}
	}
	return answerJSON(rows)
}

// unreadableRecord reports whether err is a run's own record that cannot be
// used: one that does not decode, or one past its read cap (runstore's own
// MaxRecordBytes, and the only two failures ReadRecord's own read produces).
//
// A failure that is neither — the bucket, the network — is not the run's and
// still fails the call, which is the distinction internal/cli/runs.go draws
// between an unusable object and a read that failed.
func unreadableRecord(err error) bool {
	var syntax *json.SyntaxError
	var typ *json.UnmarshalTypeError
	return errors.As(err, &syntax) || errors.As(err, &typ) ||
		errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, runstore.ErrTooLarge)
}

// listed is one of the project's runs as the listing found it, before its
// record is read.
type listed struct {
	slug  string
	runID string
	at    time.Time
}
