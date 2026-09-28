package gcpfake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"testing"
)

// Build is a fake of the Cloud Build v1 calls Fugaro makes: builds create
// (in a location) and builds get. It never runs anything: every build
// finishes at once with Outcome.
type Build struct {
	*Server

	// Outcome is the status every build reports when read back; empty
	// means SUCCESS. Set it before the build is read.
	Outcome string
	// FailGets makes the next FailGets build reads answer FailGetCode
	// (default 503), so a test can exercise the caller's retries.
	FailGets, FailGetCode int
	// NoResults makes a SUCCESS build report no pushed images.
	NoResults bool

	mu     sync.Mutex
	builds map[string]map[string]any // request bodies, by build ID
	last   map[string]any
	n      int
}

var (
	buildsPathRE = regexp.MustCompile(`^/v1/projects/([^/]+)/locations/([^/]+)/builds$`)
	buildPathRE  = regexp.MustCompile(`^/v1/projects/([^/]+)/locations/([^/]+)/builds/([^/:]+)$`)
)

// NewBuild starts a Cloud Build fake that lives until the test ends.
func NewBuild(t *testing.T) *Build {
	t.Helper()
	f := &Build{builds: map[string]map[string]any{}}
	f.Server = newServer(t, f.handle)
	return f
}

// Last returns the most recent build request, decoded, or nil.
func (f *Build) Last() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last
}

func (f *Build) handle(w http.ResponseWriter, r *http.Request, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := r.URL.Path
	switch {
	case r.Method == http.MethodPost && buildsPathRE.MatchString(p):
		m := buildsPathRE.FindStringSubmatch(p)
		var req map[string]any
		err := json.Unmarshal(body, &req)
		if steps, _ := req["steps"].([]any); err != nil || len(steps) == 0 {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "want a build with steps")
			return
		}
		f.n++
		id := fmt.Sprintf("b%04d", f.n)
		f.builds[id] = req
		f.last = req
		writeJSON(w, http.StatusOK, map[string]any{
			"name": "projects/" + m[1] + "/locations/" + m[2] + "/operations/op-" + id,
			"metadata": map[string]any{
				"@type": "type.googleapis.com/google.devtools.cloudbuild.v1.BuildOperationMetadata",
				"build": map[string]any{"id": id, "status": "QUEUED", "logUrl": logURL(m[1], m[2], id)},
			},
		})
	case r.Method == http.MethodGet && buildPathRE.MatchString(p):
		m := buildPathRE.FindStringSubmatch(p)
		req := f.builds[m[3]]
		if f.FailGets > 0 {
			f.FailGets--
			code := f.FailGetCode
			if code == 0 {
				code = http.StatusServiceUnavailable
			}
			writeError(w, code, "UNAVAILABLE", "try again")
			return
		}
		if req == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "build "+m[3]+" not found")
			return
		}
		status := f.Outcome
		if status == "" {
			status = "SUCCESS"
		}
		out := map[string]any{"id": m[3], "status": status, "logUrl": logURL(m[1], m[2], m[3])}
		if status == "SUCCESS" && !f.NoResults {
			name := ""
			if subs, ok := req["substitutions"].(map[string]any); ok {
				name, _ = subs["_IMAGE"].(string)
			}
			out["results"] = map[string]any{"images": []any{map[string]any{
				"name": name + ":latest", "digest": "sha256:" + fmt.Sprintf("%064x", f.n),
			}}}
		}
		writeJSON(w, http.StatusOK, out)
	default:
		f.unhandled(w, r)
	}
}

func logURL(project, location, id string) string {
	return "https://console.cloud.google.com/cloud-build/builds;region=" + location + "/" + id + "?project=" + project
}
