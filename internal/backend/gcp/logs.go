package gcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	logging "google.golang.org/api/logging/v2"

	"github.com/dimipaun/fugaro/internal/backend"
)

const (
	logPageSize = 1000
	// createdMargin is how far before the execution's createTime a read
	// without Since starts, for clock skew between Cloud Run and the
	// container.
	createdMargin = time.Minute
)

// Logs streams one execution's log entries, oldest first, to fn. With
// q.Follow it keeps polling until the execution has ended and no new entry
// has arrived for LogSettle, or until ctx is done (it then returns ctx.Err()).
// Without q.Since, the read starts shortly before the execution was created.
func (b *Backend) Logs(ctx context.Context, q backend.LogQuery, fn func(backend.LogEntry) error) error {
	id, err := b.canonical(q.Execution)
	if err != nil {
		return err
	}
	since := q.Since
	if since.IsZero() {
		// Bound the read: without a timestamp Cloud Logging scans the whole
		// retention. Logs can outlive the execution, so a missing one reads
		// unbounded.
		ex, err := b.Execution(ctx, id.String())
		switch {
		case err == nil && !ex.Created.IsZero():
			since = ex.Created.Add(-createdMargin)
		case err != nil && !errors.Is(err, backend.ErrNotFound):
			return err
		}
	}
	if !q.Follow {
		return b.readLogs(ctx, id, since, fn)
	}
	return backend.Follow(ctx, backend.FollowOptions{
		Since:  since,
		Poll:   q.Poll,
		Settle: b.o.LogSettle,
		Read: func(ctx context.Context, from time.Time, fn func(backend.LogEntry) error) error {
			return b.readLogs(ctx, id, from, fn)
		},
		State: func(ctx context.Context) (terminal, gone bool, err error) {
			ex, err := b.Execution(ctx, id.String())
			// An execution the backend has forgotten has finished.
			if errors.Is(err, backend.ErrNotFound) {
				return false, true, nil
			}
			if err != nil {
				return false, false, err
			}
			return ex.State.Terminal(), false, nil
		},
	}, fn)
}

// readLogs reads every entry of the execution at or after from (all of them
// when from is zero), following page tokens.
func (b *Backend) readLogs(ctx context.Context, id backend.ExecID, from time.Time, fn func(backend.LogEntry) error) error {
	filter := executionFilter(id)
	if !from.IsZero() {
		filter += ` AND timestamp>=` + strconv.Quote(from.UTC().Format(time.RFC3339Nano))
	}
	resource := "projects/" + b.o.GCPProject
	if b.o.LogView != "" {
		resource = b.o.LogView
	}
	req := &logging.ListLogEntriesRequest{
		ResourceNames: []string{resource},
		Filter:        filter,
		OrderBy:       "timestamp asc",
		PageSize:      logPageSize,
	}
	for {
		resp, err := b.logs.Entries.List(req).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("reading the logs of %s: %w", id, err)
		}
		for _, e := range resp.Entries {
			le, err := toLogEntry(e)
			if err != nil {
				return fmt.Errorf("reading the logs of %s: %w", id, err)
			}
			if err := fn(le); err != nil {
				return err
			}
		}
		// A page can be empty and still carry a token.
		if resp.NextPageToken == "" {
			return nil
		}
		req.PageToken = resp.NextPageToken
	}
}

// toLogEntry maps a Cloud Logging entry: Message is jsonPayload.message,
// else textPayload, and Fields is the whole JSON payload.
func toLogEntry(e *logging.LogEntry) (backend.LogEntry, error) {
	t, err := parseTime(e.Timestamp)
	if err != nil {
		return backend.LogEntry{}, fmt.Errorf("entry %s: %w", e.InsertId, err)
	}
	le := backend.LogEntry{Time: t, Severity: e.Severity, InsertID: e.InsertId, Message: e.TextPayload}
	if len(e.JsonPayload) > 0 {
		if err := json.Unmarshal(e.JsonPayload, &le.Fields); err != nil {
			return backend.LogEntry{}, fmt.Errorf("entry %s: jsonPayload: %w", e.InsertId, err)
		}
		if m, ok := le.Fields["message"].(string); ok {
			le.Message = m
		}
	}
	return le, nil
}

// executionFilter selects one execution's log entries.
//
// Both clauses are needed, and neither is redundant once a job is shared
// (design §4.1, plan task 3.4 step 5): the job clause keeps the read off every
// other job of the region, and the execution name is what tells the several
// repositories' runs that one project job serves apart. The job alone would
// read another repository's run; the execution name alone would read across
// jobs. What the run's own objects say is checked before this read, never
// here: the caller follows only the execution its own FUGARO_RUN names
// (CheckRunExecution).
func executionFilter(id backend.ExecID) string {
	return `resource.type="cloud_run_job" AND resource.labels.location=` + strconv.Quote(id.Region) +
		` AND resource.labels.job_name=` + strconv.Quote(id.Job) +
		` AND labels."run.googleapis.com/execution_name"=` + strconv.Quote(id.Name)
}

// logMaxSpan is how long after its start a log URL's time range runs: a
// task's longest timeout is 24 hours.
const logMaxSpan = 24 * time.Hour

// logURL is where to read an execution's logs in the console. Cloud Run's
// own link (given) opens a page that reads the project's default logs,
// which hold nothing of a run's under log isolation; with a log view it is
// a Logs Explorer query scoped to the view's storage. A known start
// (created) bounds the time range, shortly before it for clock skew;
// without one Logs Explorer's default range applies.
func (b *Backend) logURL(id backend.ExecID, given string, created time.Time) string {
	if b.o.LogView == "" {
		return given
	}
	u := "https://console.cloud.google.com/logs/query;query=" + url.PathEscape(executionFilter(id)) +
		";storageScope=storage," + url.PathEscape(b.o.LogView)
	if !created.IsZero() {
		from := created.Add(-createdMargin).UTC()
		u += ";timeRange=" + from.Format(time.RFC3339) + "/" + from.Add(logMaxSpan).Format(time.RFC3339)
	}
	return u + "?project=" + url.QueryEscape(b.o.GCPProject)
}
