package batch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
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

// jobUIDRE is the charset a job's UID may have before it goes into a log
// filter. The filter is built by string concatenation, so a UID carrying a
// quote, a space or a backslash could add clauses of its own -- or read
// another job's lines -- and this is the check that stops it. The real
// UIDs are decimal strings; letters, `_` and `-` are allowed because the
// exact shape is not a fact this design verifies (Check 57a step 10 reads
// one raw entry), and a value outside the set is refused rather than
// escaped: an unexpected UID is a reason to stop, not to guess.
var jobUIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// checkJobUID refuses a UID the log filter must not be built from. An empty
// one is its own case: without a UID the read cannot select one job's task
// logs, and reading the whole project's batch_task_logs instead would be
// worse than failing.
func checkJobUID(uid string) error {
	if uid == "" {
		return errors.New("Batch reported no UID for the job, which is what its task logs are selected by")
	}
	if !jobUIDRE.MatchString(uid) {
		return fmt.Errorf("Batch reported a job UID this build will not put in a log filter: %d characters, and not all of [A-Za-z0-9_-]", len(uid))
	}
	return nil
}

// Logs streams one job's task logs, oldest first, to fn. With q.Follow it
// keeps polling until the job has ended and no new entry has arrived for
// LogSettle, or until ctx is done (it then returns ctx.Err()).
//
// The filter is the form Google documents for one Batch job's task logs --
// labels.job_uid="<uid>" AND logName="projects/<p>/logs/batch_task_logs"
// (design §2.3, §1.5) -- so this reads the job first to learn its UID: one
// extra jobs.get per call, and follow keeps that UID rather than reading it
// again on every poll (design §2.3). The alternative,
// resource.labels.job_id, is UNVERIFIED (the resource list says only "The
// identifier of the Job", which may be the name or the UID): Check 57a step
// 10 settles it and may allow the cheaper form later (design §9).
//
// Without q.Since the read starts shortly before the job was created.
//
// Reading the logs of a job Batch no longer has is an error, not an empty
// result: the UID is what selects its lines and only the job object reports
// it, so with the job gone there is nothing to filter by. (Cloud Run's own
// filter is built from the execution's name and survives a forgotten
// execution; Batch's cannot.) Deleting a finished job is the reaper's,
// Task 5's; whether a deleted job's lines stay readable some other way is
// Check 57a step 10's question, through the resource-based filter it settles
// (design §2.3).
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
	uid := j.Uid
	if err := checkJobUID(uid); err != nil {
		return fmt.Errorf("reading the logs of %s: %w", full, err)
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
	if !q.Follow {
		return b.readLogs(ctx, id, uid, since, fn)
	}
	// The follow loop is backend.Follow, which gcp's Logs follows through
	// too: the lookback, the dedup and the settle are one implementation, not
	// two that must agree. Only the read (by UID) and the state call differ.
	return backend.Follow(ctx, backend.FollowOptions{
		Since:  since,
		Poll:   q.Poll,
		Settle: b.o.LogSettle,
		Read: func(ctx context.Context, from time.Time, fn func(backend.LogEntry) error) error {
			return b.readLogs(ctx, id, uid, from, fn)
		},
		State: func(ctx context.Context) (terminal, gone bool, err error) {
			ex, err := b.Execution(ctx, full)
			// A job the backend has forgotten has finished.
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
// uid must have passed checkJobUID: the filter is built by concatenation,
// and an unchecked UID could carry clauses of its own.
func jobFilter(id backend.ExecID, uid string) string {
	return `labels.job_uid=` + strconv.Quote(uid) +
		` AND logName=` + strconv.Quote("projects/"+id.GCPProject+"/logs/batch_task_logs")
}

// logURL is where to read a job's logs in the console: a Logs Explorer query
// for this job's task logs, scoped to the installation's log view when there
// is one. Batch has no console link of its own to hand out (Cloud Run's
// executions carry a logUri), and without the UID the query cannot select
// one job, so that case answers "" rather than a link that opens the whole
// project's logs. The query is URL-escaped, so a UID that checkJobUID would
// refuse cannot add a clause to the link either; it simply opens a query
// that matches nothing.
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
