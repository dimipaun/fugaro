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
	instances map[string][]fbInstance
}

type fbInstance struct{ location, id, typ, state, url string }

var (
	fbInstancesRE = regexp.MustCompile(`^/v1beta/projects/([^/:]+)/locations/-/instances$`)
	fbInstanceRE  = regexp.MustCompile(`^/v1beta/projects/([^/:]+)/locations/([^/:]+)/instances/([^/:]+)$`)
)

// NewFirebaseDB starts the fake; it lives until the test ends.
func NewFirebaseDB(t *testing.T) *FirebaseDB {
	t.Helper()
	f := &FirebaseDB{instances: map[string][]fbInstance{}}
	f.Server = newServer(t, f.handle)
	return f
}

// AddInstance makes the Firebase project have a default database in
// us-central1, active, at url, named db<a+i> by its position.
func (f *FirebaseDB) AddInstance(project, url string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.instances[project] = append(f.instances[project], fbInstance{"us-central1", "db" + string(rune('a'+len(f.instances[project]))), "DEFAULT_DATABASE", "ACTIVE", url})
}

// AddInstanceFull makes the Firebase project have the database id in
// location, of type instType (DEFAULT_DATABASE or USER_DATABASE), in state
// (ACTIVE, DISABLED...), at url.
func (f *FirebaseDB) AddInstanceFull(project, location, id, instType, state, url string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.instances[project] = append(f.instances[project], fbInstance{location, id, instType, state, url})
}

func (i fbInstance) json(project string) map[string]any {
	return map[string]any{"name": "projects/" + project + "/locations/" + i.location + "/instances/" + i.id,
		"project": "projects/" + project, "databaseUrl": i.url, "type": i.typ, "state": i.state}
}

func (f *FirebaseDB) handle(w http.ResponseWriter, r *http.Request, _ []byte) {
	if r.Method == http.MethodGet {
		if m := fbInstanceRE.FindStringSubmatch(r.URL.Path); m != nil {
			f.mu.Lock()
			defer f.mu.Unlock()
			for _, i := range f.instances[m[1]] {
				if i.location == m[2] && i.id == m[3] {
					writeJSON(w, http.StatusOK, i.json(m[1]))
					return
				}
			}
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Database instance "+m[3]+" was not found.")
			return
		}
	}
	m := fbInstancesRE.FindStringSubmatch(r.URL.Path)
	if r.Method != http.MethodGet || m == nil {
		f.unhandled(w, r)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	list := []map[string]any{}
	for _, i := range f.instances[m[1]] {
		list = append(list, i.json(m[1]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"instances": list})
}
