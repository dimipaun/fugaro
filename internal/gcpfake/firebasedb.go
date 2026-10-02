package gcpfake

import (
	"net/http"
	"regexp"
	"sync"
	"testing"
)

// FirebaseDB is a fake of the Realtime Database management call Fugaro
// makes: listing a project's instances (projects/<id>/locations/-/instances).
type FirebaseDB struct {
	*Server

	mu        sync.Mutex
	instances map[string][]string
}

var fbInstancesRE = regexp.MustCompile(`^/v1beta/projects/([^/:]+)/locations/-/instances$`)

// NewFirebaseDB starts the fake; it lives until the test ends.
func NewFirebaseDB(t *testing.T) *FirebaseDB {
	t.Helper()
	f := &FirebaseDB{instances: map[string][]string{}}
	f.Server = newServer(t, f.handle)
	return f
}

// AddInstance makes the Firebase project have a database at url.
func (f *FirebaseDB) AddInstance(project, url string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.instances[project] = append(f.instances[project], url)
}

func (f *FirebaseDB) handle(w http.ResponseWriter, r *http.Request, _ []byte) {
	m := fbInstancesRE.FindStringSubmatch(r.URL.Path)
	if r.Method != http.MethodGet || m == nil {
		f.unhandled(w, r)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	list := []map[string]any{}
	for i, u := range f.instances[m[1]] {
		list = append(list, map[string]any{"name": "projects/" + m[1] + "/locations/us-central1/instances/db" + string(rune('a'+i)),
			"project": "projects/" + m[1], "databaseUrl": u, "type": "DEFAULT_DATABASE", "state": "ACTIVE"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"instances": list})
}
