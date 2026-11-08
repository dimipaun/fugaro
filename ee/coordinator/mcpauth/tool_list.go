// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"context"
	"encoding/json"
	"fmt"
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
// past what the project answers with.

// listTasksArgsSchema is the schema of list_tasks: which of the project's
// repositories to list, how many runs, and how far back.
var listTasksArgsSchema = json.RawMessage(`{"type":"object","properties":{"repo":{"type":"string","description":"One of this project's repositories, as a slug; every one of them when absent"},"limit":{"type":"integer","minimum":1,"maximum":100,"description":"How many runs to answer, newest first (default 20)"},"since":{"type":"string","description":"Only runs minted at or after this RFC 3339 time (default: the last 30 days)"}},"additionalProperties":false}`)

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
	since := p.now().UTC().Add(-coordinator.RunsWindow)
	if a.Since != "" {
		at, err := time.Parse(time.RFC3339, a.Since)
		if err != nil {
			return nil, fmt.Errorf("since %q is not an RFC 3339 time", safetext.Field(a.Since))
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
		if err != nil {
			return nil, err
		}
		rows = append(rows, status)
	}
	return answerJSON(rows)
}

// listed is one of the project's runs as the listing found it, before its
// record is read.
type listed struct {
	slug  string
	runID string
	at    time.Time
}
