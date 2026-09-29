package gcpfake

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"net/http"
	"regexp"
	"sync"
	"testing"
)

// ArtifactRegistry is a fake of the Artifact Registry v1 calls discovery
// makes: repositories get (with the repository's labels) and a package's
// tags get.
//
// Known difference from the real API: a tag of a package in a repository
// that doesn't exist answers 404 like any missing tag.
type ArtifactRegistry struct {
	*Server

	mu    sync.Mutex
	repos map[string]map[string]string // projects/<p>/locations/<l>/repositories/<r> → labels
	tags  map[string]string            // <repository>/packages/<pkg>/tags/<tag> → version name
}

var (
	arRepositoryRE = regexp.MustCompile(`^/v1/(projects/[^/]+/locations/[^/]+/repositories/[^/:]+)$`)
	arTagRE        = regexp.MustCompile(`^/v1/(projects/[^/]+/locations/[^/]+/repositories/[^/:]+)/packages/([^:]+)/tags/([^/:]+)$`)
)

// NewArtifactRegistry starts an Artifact Registry fake that lives until
// the test ends.
func NewArtifactRegistry(t *testing.T) *ArtifactRegistry {
	t.Helper()
	f := &ArtifactRegistry{repos: map[string]map[string]string{}, tags: map[string]string{}}
	f.Server = newServer(t, f.handle)
	return f
}

func arRepository(project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/repositories/" + id
}

// AddRepository makes the Docker repository id exist in project and
// location, carrying labels.
func (f *ArtifactRegistry) AddRepository(project, location, id string, labels map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repos[arRepository(project, location, id)] = maps.Clone(labels)
}

// SetTag makes pkg:tag exist in the repository, pointing at a version
// named after the tag.
func (f *ArtifactRegistry) SetTag(project, location, id, pkg, tag string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	repo := arRepository(project, location, id)
	sum := sha256.Sum256([]byte(repo + "/" + pkg + ":" + tag))
	f.tags[repo+"/packages/"+pkg+"/tags/"+tag] = repo + "/packages/" + pkg + "/versions/sha256:" + hex.EncodeToString(sum[:])
}

func (f *ArtifactRegistry) handle(w http.ResponseWriter, r *http.Request, _ []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := r.URL.Path
	switch {
	case r.Method == http.MethodGet && arRepositoryRE.MatchString(p):
		name := arRepositoryRE.FindStringSubmatch(p)[1]
		labels, ok := f.repos[name]
		if !ok {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Requested entity was not found.")
			return
		}
		out := map[string]any{"name": name, "format": "DOCKER"}
		if len(labels) > 0 {
			out["labels"] = maps.Clone(labels)
		}
		writeJSON(w, http.StatusOK, out)
	case r.Method == http.MethodGet && arTagRE.MatchString(p):
		m := arTagRE.FindStringSubmatch(p)
		name := m[1] + "/packages/" + m[2] + "/tags/" + m[3]
		v, ok := f.tags[name]
		if !ok {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Requested entity was not found.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": name, "version": v})
	default:
		f.unhandled(w, r)
	}
}
