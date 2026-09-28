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

// Logging is a stateful fake of Cloud Logging's entries.list, for the one
// filter shape Fugaro sends: a Cloud Run job execution's entries, optionally
// from a timestamp on. Any other filter fails the test.
//
// Entries are keyed by job and short execution name, as the real
// resource.labels.job_name and labels."run.googleapis.com/execution_name"
// are.
type Logging struct {
	*Server
	mu      sync.Mutex
	entries map[execKey][]LogEntry
	nextID  int
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
	l := &Logging{entries: map[execKey][]LogEntry{}}
	l.Server = newServer(t, l.handle)
	return l
}

// logKey keys an execution named in full or by its short name. A short name
// is <job>-<suffix>, so the job is everything before the last dash.
func logKey(execution string) execKey {
	if id, ok := backend.ParseExecution(execution); ok {
		return execKey{id.Job, id.Name}
	}
	job := execution
	if i := strings.LastIndex(execution, "-"); i > 0 {
		job = execution[:i]
	}
	return execKey{job, execution}
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

var logFilterRE = regexp.MustCompile(`^resource\.type="cloud_run_job" AND resource\.labels\.job_name="([^"]+)" AND labels\."run\.googleapis\.com/execution_name"="([^"]+)"(?: AND timestamp>="([^"]+)")?$`)

func (l *Logging) handle(w http.ResponseWriter, r *http.Request, body []byte) {
	if r.Method != http.MethodPost || r.URL.Path != "/v2/entries:list" {
		l.unhandled(w, r)
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
	m := logFilterRE.FindStringSubmatch(req.Filter)
	var since time.Time
	if m != nil && m[3] != "" {
		t, err := time.Parse(time.RFC3339Nano, m[3])
		if err != nil {
			m = nil
		}
		since = t
	}
	if m == nil || len(req.ResourceNames) != 1 || !strings.HasPrefix(req.ResourceNames[0], "projects/") ||
		(req.OrderBy != "" && req.OrderBy != "timestamp asc") {
		l.failf("gcpfake: entries.list request the fake cannot answer: resourceNames=%q filter=%q orderBy=%q", req.ResourceNames, req.Filter, req.OrderBy)
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "unsupported request")
		return
	}
	k := execKey{m[1], m[2]}

	l.mu.Lock()
	var match []LogEntry
	for _, e := range l.entries[k] {
		if !e.Time.Before(since) {
			match = append(match, e)
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
			"logName":   req.ResourceNames[0] + "/logs/run.googleapis.com%2Fstdout",
			"resource":  map[string]any{"type": "cloud_run_job", "labels": map[string]string{"job_name": k.job}},
			"labels":    map[string]string{"run.googleapis.com/execution_name": k.short},
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
