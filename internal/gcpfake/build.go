package gcpfake

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
)

// Build is a fake of the Cloud Build v1 calls Fugaro makes: builds create
// (in a location) and builds get. It also answers Artifact Registry's
// repositories get, for the registries AddRegistry made, since a build's
// submitter checks its registry first, and keeps the registry's tags.
//
// A created build runs at once, as a simulation of cloudbuild.yaml's
// steps by their IDs, in the request's order, each in the build's
// workspace directory (Workspace), which stands for /workspace/out:
//   - build pushes candidate-<id> with a new digest, writes image-digest
//     and reports the digest as its step output
//   - promote tags latest with image-digest's digest (the registry-side
//     retag by digest), or, when the gate's hook left a superseded file,
//     reports "superseded" as its output
//   - untag removes candidate-<id>, or, with UntagDenied, logs the
//     step's warning and goes on, as the script's || echo does
//   - promote, record and untag do nothing once superseded exists
//
// Steps holds a hook per step ID, which runs first; a hook's error, or
// FailStep, ends the build FAILURE at that step.
type Build struct {
	*Server

	// Outcome is the status every build reports when read back; empty
	// means the simulated run's. Set it before the build is read.
	Outcome string
	// FailGets makes the next FailGets build reads answer FailGetCode
	// (default 503), so a test can exercise the caller's retries.
	FailGets, FailGetCode int
	// NoResults makes a SUCCESS build report neither step outputs nor
	// pushed images.
	NoResults bool
	// NoStepOutputs makes a SUCCESS build report its image as a pushed
	// image (results.images), as a build with images: did, and no step
	// outputs.
	NoStepOutputs bool
	// ForbidRegistries makes every repositories get answer 403, as for a
	// caller without read access to the registry.
	ForbidRegistries bool

	// FailStep ends the build FAILURE at this step, before its effect.
	FailStep string
	// Steps are hooks by step ID, run in the build's workspace directory
	// with the fake locked, so a hook must not call the fake.
	Steps map[string]func(dir string) error
	// UntagDenied refuses the candidate tag's delete, as a 403 for a
	// caller with artifactregistry.writer only.
	UntagDenied bool

	mu         sync.Mutex
	builds     map[string]map[string]any // request bodies, by build ID
	runs       map[string]*buildRun      // simulated runs, by build ID
	registries map[string]bool           // projects/<p>/locations/<l>/repositories/<r>
	tags       map[string]string         // <image>:<tag> → digest
	root       string
	last       map[string]any
	n          int
}

// buildRun is the outcome of one simulated build.
type buildRun struct {
	status  string
	outputs []string // base64 step outputs, by step index
	digest  string
	log     []string
}

var (
	buildsPathRE = regexp.MustCompile(`^/v1/projects/([^/]+)/locations/([^/]+)/builds$`)
	buildPathRE  = regexp.MustCompile(`^/v1/projects/([^/]+)/locations/([^/]+)/builds/([^/:]+)$`)
	registryRE   = regexp.MustCompile(`^/v1/(projects/[^/]+/locations/[^/]+/repositories/[^/:]+)$`)
)

// AddRegistry makes the Docker repository id exist in project and location.
func (f *Build) AddRegistry(project, location, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registries["projects/"+project+"/locations/"+location+"/repositories/"+id] = true
}

// NewBuild starts a Cloud Build fake that lives until the test ends.
func NewBuild(t *testing.T) *Build {
	t.Helper()
	f := &Build{builds: map[string]map[string]any{}, runs: map[string]*buildRun{}, registries: map[string]bool{},
		tags: map[string]string{}, root: t.TempDir()}
	f.Server = newServer(t, f.handle)
	return f
}

// Tag returns the digest image:tag points at, or "".
func (f *Build) Tag(image, tag string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tags[image+":"+tag]
}

// SetTag points image:tag at digest.
func (f *Build) SetTag(image, tag, digest string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tags[image+":"+tag] = digest
}

// Workspace is build id's workspace directory (/workspace/out).
func (f *Build) Workspace(id string) string { return filepath.Join(f.root, id) }

// Log returns what build id's simulated steps logged.
func (f *Build) Log(id string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r := f.runs[id]; r != nil {
		return slices.Clone(r.log)
	}
	return nil
}

// run simulates the build of req as id (see Build). f.mu is held.
func (f *Build) run(id string, req map[string]any) *buildRun {
	r := &buildRun{status: "SUCCESS"}
	dir := f.Workspace(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		r.status, r.log = "INTERNAL_ERROR", []string{err.Error()}
		return r
	}
	image := ""
	if subs, ok := req["substitutions"].(map[string]any); ok {
		image, _ = subs["_IMAGE"].(string)
	}
	steps, _ := req["steps"].([]any)
	r.outputs = make([]string, len(steps))
	exists := func(name string) bool { _, err := os.Stat(filepath.Join(dir, name)); return err == nil }
	for i, raw := range steps {
		st, _ := raw.(map[string]any)
		sid, _ := st["id"].(string)
		superseded := exists("superseded")
		if superseded && (sid == "promote" || sid == "record" || sid == "untag") {
			r.log = append(r.log, sid+": superseded, nothing to do")
			if sid == "promote" {
				r.outputs[i] = base64.StdEncoding.EncodeToString([]byte("superseded"))
			}
			continue
		}
		fail := func(msg string) *buildRun {
			r.status = "FAILURE"
			r.log = append(r.log, sid+": "+msg)
			return r
		}
		if sid == f.FailStep {
			return fail("failed")
		}
		if hook := f.Steps[sid]; hook != nil {
			if err := hook(dir); err != nil {
				return fail(err.Error())
			}
		}
		switch sid {
		case "build":
			r.digest = fmt.Sprintf("sha256:%064x", f.n)
			f.tags[image+":candidate-"+id] = r.digest
			if err := os.WriteFile(filepath.Join(dir, "image-digest"), []byte(r.digest+"\n"), 0o644); err != nil {
				return fail(err.Error())
			}
			r.outputs[i] = base64.StdEncoding.EncodeToString([]byte(r.digest))
		case "promote":
			data, err := os.ReadFile(filepath.Join(dir, "image-digest"))
			if err != nil {
				return fail(err.Error())
			}
			f.tags[image+":latest"] = strings.TrimSpace(string(data))
			r.log = append(r.log, "promote: tags add "+image+"@"+strings.TrimSpace(string(data))+" "+image+":latest")
		case "untag":
			if f.UntagDenied {
				r.log = append(r.log, "untag: ERROR: PERMISSION_DENIED: 403", "untag: warning: could not remove candidate-"+id+" (harmless; the cleanup policy clears it)")
				continue
			}
			delete(f.tags, image+":candidate-"+id)
		}
	}
	return r
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
		f.runs[id] = f.run(id, req)
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
		run := f.runs[m[3]]
		status := cmp.Or(f.Outcome, run.status)
		out := map[string]any{"id": m[3], "status": status, "logUrl": logURL(m[1], m[2], m[3]), "steps": req["steps"]}
		if subs, ok := req["substitutions"].(map[string]any); ok {
			out["substitutions"] = subs
		}
		if status == "SUCCESS" && !f.NoResults {
			if f.NoStepOutputs {
				name := ""
				if subs, ok := req["substitutions"].(map[string]any); ok {
					name, _ = subs["_IMAGE"].(string)
				}
				out["results"] = map[string]any{"images": []any{map[string]any{"name": name + ":latest", "digest": run.digest}}}
			} else {
				out["results"] = map[string]any{"buildStepOutputs": run.outputs}
			}
		}
		writeJSON(w, http.StatusOK, out)
	case r.Method == http.MethodGet && registryRE.MatchString(p):
		name := registryRE.FindStringSubmatch(p)[1]
		if f.ForbidRegistries {
			writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "Permission 'artifactregistry.repositories.get' denied")
			return
		}
		if !f.registries[name] {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Requested entity was not found.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": name, "format": "DOCKER"})
	default:
		f.unhandled(w, r)
	}
}

func logURL(project, location, id string) string {
	return "https://console.cloud.google.com/cloud-build/builds;region=" + location + "/" + id + "?project=" + project
}
