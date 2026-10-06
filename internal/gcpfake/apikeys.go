package gcpfake

import (
	"net/http"
	"regexp"
	"sync"
	"testing"
)

// APIKeys is a fake of the API Keys v2 call Fugaro makes: reading one key
// (projects/<p>/locations/global/keys/<id>).
type APIKeys struct {
	*Server

	mu   sync.Mutex
	keys map[string]map[string]any // projects/<p>/locations/global/keys/<id> → fields
}

var apiKeyRE = regexp.MustCompile(`^/v2/(projects/[^/]+/locations/global/keys/[^/:]+)$`)

// NewAPIKeys starts the fake; it lives until the test ends.
func NewAPIKeys(t *testing.T) *APIKeys {
	t.Helper()
	f := &APIKeys{keys: map[string]map[string]any{}}
	f.Server = newServer(t, f.handle)
	return f
}

// AddKey makes the key id exist in project with displayName, restricted to
// the API targets (services). otherRestriction adds a restriction of
// another kind (a browser one); deleted makes it a soft-deleted key, which
// the API still serves, with a deleteTime.
func (f *APIKeys) AddKey(project, id, displayName string, targets []string, otherRestriction bool, deleted bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name := "projects/" + project + "/locations/global/keys/" + id
	restrictions := map[string]any{}
	if len(targets) > 0 {
		var ts []map[string]string
		for _, s := range targets {
			ts = append(ts, map[string]string{"service": s})
		}
		restrictions["apiTargets"] = ts
	}
	if otherRestriction {
		restrictions["browserKeyRestrictions"] = map[string]any{"allowedReferrers": []string{"https://example.com/*"}}
	}
	k := map[string]any{"name": name, "uid": id, "displayName": displayName, "createTime": "2026-01-01T00:00:00Z",
		"etag": "W/\"fake\"", "restrictions": restrictions}
	if deleted {
		k["deleteTime"] = "2026-02-01T00:00:00Z"
	}
	f.keys[name] = k
}

func (f *APIKeys) handle(w http.ResponseWriter, r *http.Request, _ []byte) {
	m := apiKeyRE.FindStringSubmatch(r.URL.Path)
	if r.Method != http.MethodGet || m == nil {
		f.unhandled(w, r)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	k := f.keys[m[1]]
	if k == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Key "+m[1]+" was not found.")
		return
	}
	writeJSON(w, http.StatusOK, k)
}
