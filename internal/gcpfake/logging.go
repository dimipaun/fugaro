package gcpfake

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
)

// Logging is a stateful fake of Cloud Logging's entries.list, of a log
// bucket's get (for fugaro update's discovery) and of a project log sink's get
// (`fugaro doctor` reads the installation's sink for the Batch arm of its
// filter). entries.list takes the two
// filter shapes Fugaro sends -- a Cloud Run job execution's entries, and a
// Batch job's task logs by the job's UID (design compute-backend.md §2.3) --
// each optionally from a timestamp on. Any other filter fails the test.
//
// Cloud Run entries are keyed by region, job and short execution name, as
// the real resource.labels.location, resource.labels.job_name and
// labels."run.googleapis.com/execution_name" are; an entry added under a
// short name has no region and matches a query in any. Batch entries are
// keyed by the job's UID, which is what labels.job_uid carries.
type Logging struct {
	*Server
	// Resource, when set, is the one resource name (such as a log view)
	// requests must read; any other resourceNames fails the test. Unset,
	// the fake accepts a single projects/<p>.
	Resource string
	// FailReads makes every entries.list request fail with HTTP 500,
	// to prove a caller reads no log (fugaro logs --url).
	FailReads bool
	mu        sync.Mutex
	entries   map[logExec][]LogEntry
	uidLogs   map[string][]LogEntry // Batch: a job's task logs by its UID
	nextID    int
	buckets   map[string]logBucket // projects/<p>/locations/<l>/buckets/<id>
	sinks     map[string]logSink   // projects/<p>/sinks/<id>
}

type logBucket struct{ state, description string }

// logSink is one project log sink as the fake stores it: a sink is a name, a
// destination and a filter, and nothing Fugaro does changes either (the
// installation's Terraform owns both).
type logSink struct{ destination, filter string }

// AddSink makes the project sink id exist in project, routing to destination
// with filter. `fugaro doctor` reads it for the Batch arm: an installation
// whose sink predates that arm routes only Cloud Run's lines.
func (l *Logging) AddSink(project, id, destination, filter string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sinks == nil {
		l.sinks = map[string]logSink{}
	}
	l.sinks["projects/"+project+"/sinks/"+id] = logSink{destination, filter}
}

var logSinkRE = regexp.MustCompile(`^/v2/(projects/[^/]+/sinks/[^/:]+)$`)

// getSink answers a project log sink's get.
func (l *Logging) getSink(w http.ResponseWriter, name string) {
	l.mu.Lock()
	s, ok := l.sinks[name]
	l.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Sink "+name+" does not exist")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name": name, "destination": s.destination, "filter": s.filter, "writerIdentity": "serviceAccount:fake@fake.iam.gserviceaccount.com",
	})
}

// AddBucket makes the log bucket id exist in project at location, in
// state (ACTIVE, or DELETE_REQUESTED while it is pending deletion), with
// description.
func (l *Logging) AddBucket(project, location, id, state, description string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.buckets == nil {
		l.buckets = map[string]logBucket{}
	}
	l.buckets["projects/"+project+"/locations/"+location+"/buckets/"+id] = logBucket{state, description}
}

var logBucketRE = regexp.MustCompile(`^/v2/(projects/[^/]+/locations/[^/]+/buckets/[^/:]+)$`)

// getBucket answers a log bucket's get.
func (l *Logging) getBucket(w http.ResponseWriter, name string) {
	l.mu.Lock()
	b, ok := l.buckets[name]
	l.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Bucket "+name+" does not exist")
		return
	}
	body := map[string]any{"name": name, "lifecycleState": b.state, "retentionDays": 30}
	if b.description != "" {
		body["description"] = b.description
	}
	writeJSON(w, http.StatusOK, body)
}

// LogEntry is one fake log entry. JSON, when set, is the jsonPayload;
// otherwise Text is the textPayload. An empty InsertID is assigned.
type LogEntry struct {
	Time     time.Time
	Severity string
	InsertID string
	JSON     map[string]any
	Text     string
}

// NewLogging starts a Cloud Logging fake that lives until the test ends.
func NewLogging(t *testing.T) *Logging {
	t.Helper()
	l := &Logging{entries: map[logExec][]LogEntry{}, uidLogs: map[string][]LogEntry{}}
	l.Server = newServer(t, l.handle)
	return l
}

// AddUID stores an entry among the task logs of the Batch job whose UID is
// uid, which is how labels.job_uid selects them. It is Add's Batch
// counterpart: a Batch job's logs are reached by UID, not by a job name,
// because resource.labels.job_id's meaning is UNVERIFIED (design §9,
// Check 57a step 10).
func (l *Logging) AddUID(uid string, e LogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e.InsertID == "" {
		l.nextID++
		e.InsertID = fmt.Sprintf("fake-%06d", l.nextID)
	}
	l.uidLogs[uid] = append(l.uidLogs[uid], e)
}

// logExec is an execution's log key; region is "" for one added by its
// short name.
type logExec struct {
	region string
	execKey
}

// logKey keys an execution named in full or by its short name. A short name
// is <job>-<suffix>, so the job is everything before the last dash.
//
// A full name is parsed with CloudRunCodec, not with backend.ParseExecution:
// these entries are Cloud Run entries, keyed by the Cloud Run resource's
// labels, and a second backend's names (a Batch job is six fields against
// Cloud Run's eight) must not key one. Batch entries are keyed by the job's
// UID instead (AddUID).
func logKey(execution string) logExec {
	if id, ok := backend.CloudRunCodec().Parse(execution); ok {
		return logExec{id.Region, execKey{id.Job, id.Name}}
	}
	job := execution
	if i := strings.LastIndex(execution, "-"); i > 0 {
		job = execution[:i]
	}
	return logExec{"", execKey{job, execution}}
}

// Add stores an entry of execution (a full or short name).
func (l *Logging) Add(execution string, e LogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e.InsertID == "" {
		l.nextID++
		e.InsertID = fmt.Sprintf("fake-%06d", l.nextID)
	}
	k := logKey(execution)
	l.entries[k] = append(l.entries[k], e)
}

// AddJSONLines stores one entry per line of slog JSON output, as Cloud Run
// ingests a container's structured stdout: "time" becomes the timestamp and
// "severity" the severity, and both leave the payload; the rest, "message"
// included, is the jsonPayload. A line that is not a JSON object becomes a
// textPayload entry, and a line without a time is stamped now.
func (l *Logging) AddJSONLines(execution string, data []byte) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		e := LogEntry{Time: time.Now()}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil || obj == nil {
			e.Text = line
			l.Add(execution, e)
			continue
		}
		if s, ok := obj["time"].(string); ok {
			if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
				e.Time = t
				delete(obj, "time")
			}
		}
		if s, ok := obj["severity"].(string); ok {
			e.Severity = s
			delete(obj, "severity")
		}
		e.JSON = obj
		l.Add(execution, e)
	}
}

var logFilterRE = regexp.MustCompile(`^resource\.type="cloud_run_job" AND resource\.labels\.location="([^"]+)" AND resource\.labels\.job_name="([^"]+)" AND labels\."run\.googleapis\.com/execution_name"="([^"]+)"(?: AND timestamp>="([^"]+)")?$`)

// batchLogFilterRE is the shape the Batch backend sends: the job's task logs
// by its UID, the form Google documents for one job (design §2.3).
var batchLogFilterRE = regexp.MustCompile(`^labels\.job_uid="([^"]+)" AND logName="projects/([^/]+)/logs/([^"]+)"(?: AND timestamp>="([^"]+)")?$`)

// logQuery is a parsed entries.list filter: one of the two shapes above.
type logQuery struct {
	// Cloud Run: the monitored resource's labels and the execution label.
	region, job, short string
	// Batch: the job's UID, the log the entries come from, and its project.
	uid, logName, project string
	since                 time.Time
}

// batch reports whether q is the Batch filter shape.
func (q logQuery) batch() bool { return q.uid != "" }

// parseLogFilter parses either filter shape. A timestamp it cannot parse
// makes the filter unanswerable, exactly as the Cloud Run shape's did.
func parseLogFilter(f string) (logQuery, bool) {
	var q logQuery
	var since string
	switch m := logFilterRE.FindStringSubmatch(f); {
	case m != nil:
		q.region, q.job, q.short, since = m[1], m[2], m[3], m[4]
	case batchLogFilterRE.MatchString(f):
		m := batchLogFilterRE.FindStringSubmatch(f)
		q.uid, q.project, q.logName, since = m[1], m[2], m[3], m[4]
	default:
		return logQuery{}, false
	}
	if since != "" {
		t, err := time.Parse(time.RFC3339Nano, since)
		if err != nil {
			return logQuery{}, false
		}
		q.since = t
	}
	return q, true
}

// resourceProject is the project of an entries.list resourceNames entry:
// "projects/<p>" or "projects/<p>/locations/<l>/buckets/<b>/views/<v>".
func resourceProject(name string) string {
	f := strings.Split(name, "/")
	if len(f) < 2 || f[0] != "projects" {
		return ""
	}
	return f[1]
}

func (l *Logging) handle(w http.ResponseWriter, r *http.Request, body []byte) {
	if m := logBucketRE.FindStringSubmatch(r.URL.Path); r.Method == http.MethodGet && m != nil {
		l.getBucket(w, m[1])
		return
	}
	if m := logSinkRE.FindStringSubmatch(r.URL.Path); r.Method == http.MethodGet && m != nil {
		l.getSink(w, m[1])
		return
	}
	if r.Method != http.MethodPost || r.URL.Path != "/v2/entries:list" {
		l.unhandled(w, r)
		return
	}
	if l.FailReads {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "gcpfake: FailReads is set")
		return
	}
	var req struct {
		ResourceNames []string `json:"resourceNames"`
		Filter        string   `json:"filter"`
		OrderBy       string   `json:"orderBy"`
		PageSize      int      `json:"pageSize"`
		PageToken     string   `json:"pageToken"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		l.failf("gcpfake: entries.list body: %v", err)
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	q, ok := parseLogFilter(req.Filter)
	wantRes := func() bool {
		if len(req.ResourceNames) != 1 {
			return false
		}
		if l.Resource != "" {
			return req.ResourceNames[0] == l.Resource
		}
		return strings.HasPrefix(req.ResourceNames[0], "projects/") && !strings.Contains(req.ResourceNames[0], "/views/")
	}
	// A Batch filter names the log's project as well, and the real API
	// requires it to be one the request may read: a mismatch is a request
	// Fugaro must never send, so it fails the test. It is asked only after
	// wantRes has established there is exactly one resource name, which is
	// the one it reads.
	wantProject := func() bool {
		if !q.batch() {
			return true
		}
		return resourceProject(req.ResourceNames[0]) == q.project
	}
	if !ok || !wantRes() || !wantProject() ||
		(req.OrderBy != "" && req.OrderBy != "timestamp asc") {
		l.failf("gcpfake: entries.list request the fake cannot answer: resourceNames=%q filter=%q orderBy=%q", req.ResourceNames, req.Filter, req.OrderBy)
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "unsupported request")
		return
	}

	l.mu.Lock()
	var match []LogEntry
	if q.batch() {
		for _, e := range l.uidLogs[q.uid] {
			if !e.Time.Before(q.since) {
				match = append(match, e)
			}
		}
	} else {
		k := execKey{q.job, q.short}
		for _, key := range []logExec{{q.region, k}, {"", k}} {
			for _, e := range l.entries[key] {
				if !e.Time.Before(q.since) {
					match = append(match, e)
				}
			}
		}
	}
	l.mu.Unlock()
	sort.SliceStable(match, func(i, j int) bool { return match[i].Time.Before(match[j].Time) })

	size := req.PageSize
	if size <= 0 {
		size = 50
	}
	start, _ := strconv.Atoi(req.PageToken)
	start = min(max(start, 0), len(match))
	end := min(start+size, len(match))
	out := []any{}
	for _, e := range match[start:end] {
		re := map[string]any{
			"insertId":  e.InsertID,
			"timestamp": e.Time.UTC().Format(time.RFC3339Nano),
		}
		if q.batch() {
			// The real resource's labels are resource_container/location/
			// job_id, and whether job_id is the job's name or its UID is
			// UNVERIFIED (design compute-backend.md §9, Check 57a step 10).
			// The UID is what the fake has: the filter it answers carries
			// the UID and the project and nothing else, and no Fugaro code
			// reads this field -- Logs selects by labels.job_uid.
			re["logName"] = "projects/" + q.project + "/logs/" + q.logName
			re["resource"] = map[string]any{"type": "batch.googleapis.com/Job",
				"labels": map[string]string{"job_id": q.uid, "resource_container": q.project}}
		} else {
			re["logName"] = req.ResourceNames[0] + "/logs/run.googleapis.com%2Fstdout"
			re["resource"] = map[string]any{"type": "cloud_run_job", "labels": map[string]string{"location": q.region, "job_name": q.job}}
			re["labels"] = map[string]string{"run.googleapis.com/execution_name": q.short}
		}
		if e.Severity != "" {
			re["severity"] = e.Severity
		}
		if e.JSON != nil {
			re["jsonPayload"] = e.JSON
		} else {
			re["textPayload"] = e.Text
		}
		out = append(out, re)
	}
	resp := map[string]any{"entries": out}
	if end < len(match) {
		resp["nextPageToken"] = strconv.Itoa(end)
	}
	writeJSON(w, http.StatusOK, resp)
}
