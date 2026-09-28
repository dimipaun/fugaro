package gcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	logging "google.golang.org/api/logging/v2"

	"github.com/dimipaun/fugaro/internal/backend"
)

const (
	// defaultLogPoll is follow's poll interval when the query sets none.
	defaultLogPoll = 3 * time.Second
	// followLookback is how far before the newest entry already seen each
	// follow poll reads again. Cloud Logging can ingest an entry after a
	// newer one; the lookback catches it, and insert IDs keep it from being
	// emitted twice.
	followLookback = 10 * time.Second
	logPageSize    = 1000
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
	poll := q.Poll
	if poll <= 0 {
		poll = defaultLogPoll
	}
	seen := map[string]time.Time{} // insertId|timestamp → timestamp, within the lookback
	var newest, settleFrom time.Time
	for {
		// Read from the lookback before the newest entry seen, but never
		// from the future: one future-dated entry must not hide the rest.
		from := since
		if !newest.IsZero() {
			if f := minTime(newest, time.Now()).Add(-followLookback); f.After(from) {
				from = f
			}
		}
		fresh := 0
		err := b.readLogs(ctx, id, from, func(e backend.LogEntry) error {
			k := e.InsertID + "|" + e.Time.Format(time.RFC3339Nano)
			if _, dup := seen[k]; dup {
				return nil
			}
			seen[k] = e.Time
			if e.Time.After(newest) {
				newest = e.Time
			}
			fresh++
			return fn(e)
		})
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		// The next read starts no earlier than this one: older keys can't recur.
		for k, t := range seen {
			if t.Before(from) {
				delete(seen, k)
			}
		}

		ex, err := b.Execution(ctx, id.String())
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// An execution the backend has forgotten has finished (C-M12):
		// settle on its logs like any ended one.
		ended := errors.Is(err, backend.ErrNotFound)
		if err != nil && !ended {
			return err
		}
		now := time.Now()
		switch {
		case !ended && !ex.State.Terminal():
			settleFrom = time.Time{}
		case settleFrom.IsZero() || fresh > 0:
			settleFrom = now // wait out Cloud Logging's ingestion lag
		case now.Sub(settleFrom) >= b.o.LogSettle:
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// readLogs reads every entry of the execution at or after from (all of them
// when from is zero), following page tokens.
func (b *Backend) readLogs(ctx context.Context, id backend.ExecID, from time.Time, fn func(backend.LogEntry) error) error {
	filter := `resource.type="cloud_run_job" AND resource.labels.job_name=` + strconv.Quote(id.Job) +
		` AND labels."run.googleapis.com/execution_name"=` + strconv.Quote(id.Name)
	if !from.IsZero() {
		filter += ` AND timestamp>=` + strconv.Quote(from.UTC().Format(time.RFC3339Nano))
	}
	req := &logging.ListLogEntriesRequest{
		ResourceNames: []string{"projects/" + b.o.Project},
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
