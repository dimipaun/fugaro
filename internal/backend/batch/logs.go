package batch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	logging "google.golang.org/api/logging/v2"

	"github.com/dimipaun/fugaro/internal/backend"
)

const (
	logPageSize = 1000
	// createdMargin is how far before the job's createTime a read without
	// Since starts, for clock skew between Batch and the VM.
	createdMargin = time.Minute
	// logMaxSpan is how long after its start a log URL's time range runs.
	logMaxSpan = 24 * time.Hour
)

// Logs streams one job's task logs, oldest first, to fn.
//
// The filter is the form Google documents for one Batch job's task logs --
// labels.job_uid="<uid>" AND logName="projects/<p>/logs/batch_task_logs"
// (design §2.3, §1.5) -- so this reads the job first to learn its UID: one
// extra jobs.get per call. The alternative, resource.labels.job_id, is
// UNVERIFIED (the resource list says only "The identifier of the Job", which
// may be the name or the UID): Check 57a step 10 settles it and may allow
// the cheaper form later (design §9).
//
// Without q.Since the read starts shortly before the job was created. With
// q.Follow it does not follow yet: Task 4 owns that, together with the
// installation's sink arm, and a follow that quietly read once would look
// like a run that had gone silent.
func (b *Backend) Logs(ctx context.Context, q backend.LogQuery, fn func(backend.LogEntry) error) error {
	id, err := b.canonical(q.Execution)
	if err != nil {
		return err
	}
	full := b.codec.Format(id)
	j, err := b.batch.Projects.Locations.Jobs.Get(full).Context(ctx).Do()
	if err != nil {
		return apiError("reading job "+full+" for its logs", err)
	}
	if j.Uid == "" {
		return fmt.Errorf("reading the logs of %s: Batch reported no UID for the job, which is what its task logs are selected by", full)
	}
	if q.Follow {
		return fmt.Errorf("following the logs of %s is not implemented for the Batch backend yet (design compute-backend.md, Task 4); read them without --follow", full)
	}
	since := q.Since
	if since.IsZero() {
		created, err := parseTime(j.CreateTime)
		if err != nil {
			b.o.Warn(fmt.Sprintf("job %s: createTime: %v", full, err))
		}
		if !created.IsZero() {
			since = created.Add(-createdMargin)
		}
	}
	return b.readLogs(ctx, id, j.Uid, since, fn)
}

// readLogs reads every entry of the job's task logs at or after from (all of
// them when from is zero), following page tokens.
func (b *Backend) readLogs(ctx context.Context, id backend.ExecID, uid string, from time.Time, fn func(backend.LogEntry) error) error {
	filter := jobFilter(id, uid)
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
			return fmt.Errorf("reading the logs of %s: %w", id.Job, err)
		}
		for _, e := range resp.Entries {
			le, err := toLogEntry(e)
			if err != nil {
				return fmt.Errorf("reading the logs of %s: %w", id.Job, err)
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
// else textPayload, and Fields is the whole JSON payload. It is gcp's, which
// is the same mapping: the entries a Batch VM writes are the same structured
// lines the runner writes anywhere.
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

// jobFilter selects one Batch job's task logs, by its UID (design §2.3).
func jobFilter(id backend.ExecID, uid string) string {
	return `labels.job_uid=` + strconv.Quote(uid) +
		` AND logName=` + strconv.Quote("projects/"+id.GCPProject+"/logs/batch_task_logs")
}

// logURL is where to read a job's logs in the console: a Logs Explorer query
// for this job's task logs, scoped to the installation's log view when there
// is one. Batch has no console link of its own to hand out (Cloud Run's
// executions carry a logUri), and without the UID the query cannot select
// one job, so that case answers "" rather than a link that opens the whole
// project's logs.
func (b *Backend) logURL(id backend.ExecID, uid string, created time.Time) string {
	if uid == "" {
		return ""
	}
	u := "https://console.cloud.google.com/logs/query;query=" + url.PathEscape(jobFilter(id, uid))
	if b.o.LogView != "" {
		u += ";storageScope=storage," + url.PathEscape(b.o.LogView)
	}
	if !created.IsZero() {
		from := created.Add(-createdMargin).UTC()
		u += ";timeRange=" + from.Format(time.RFC3339) + "/" + from.Add(logMaxSpan).Format(time.RFC3339)
	}
	return u + "?project=" + url.QueryEscape(b.o.GCPProject)
}
