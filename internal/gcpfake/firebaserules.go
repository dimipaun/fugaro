package gcpfake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"testing"
)

// FirebaseRules is a fake of the Firebase Rules v1 REST calls init makes to
// deploy Firestore security rules for one project: GET a release, GET a
// ruleset, POST a ruleset, POST a release (409 when it exists). The release
// of the (default) database is "cloud.firestore". Rulesets are immutable.
//
// Hooks: SeedRelease (rules that were there before init), FlakeAfterWrite (the
// next n release reads after a release was created answer as if it did not
// exist yet), Releases/Rulesets/Writes (what happened), Refuse (from Server).
type FirebaseRules struct {
	*Server
	project string

	mu       sync.Mutex
	rulesets map[string]string // id -> single file's content
	release  string            // ruleset name; "" = no release
	writes   int
	flake    int
	flaking  int
	next     int
	reads    int
	failW    int
	seedAt   int
	seedSrc  string
}

// NewFirebaseRules starts the fake for Firebase project project.
func NewFirebaseRules(t *testing.T, project string) *FirebaseRules {
	t.Helper()
	f := &FirebaseRules{project: project, rulesets: map[string]string{}}
	f.Server = newServer(t, f.handle)
	return f
}

// SeedRelease makes the project's Firestore release exist with source.
func (f *FirebaseRules) SeedRelease(source string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.release = f.addRuleset(source)
}

// SeedAfterReads makes a release with source appear (a default one, made by
// something else) once the fake answered n release reads: the n+1st read
// finds it.
func (f *FirebaseRules) SeedAfterReads(n int, source string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seedAt, f.seedSrc = n+1, source
}

// FailWrites makes the next n POST calls answer 500 and change nothing.
func (f *FirebaseRules) FailWrites(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failW = n
}

// FlakeAfterWrite makes the n release reads after the next release creation
// answer 404, as an eventually consistent read could.
func (f *FirebaseRules) FlakeAfterWrite(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flake, f.flaking = n, 0
}

// Source is the Firestore release's rules source ("" and false when there is
// no release).
func (f *FirebaseRules) Source() (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.release == "" {
		return "", false
	}
	return f.rulesets[f.release], true
}

// Rulesets counts the rulesets that exist.
func (f *FirebaseRules) Rulesets() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rulesets)
}

// Writes counts the POST calls that created something.
func (f *FirebaseRules) Writes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes
}

func (f *FirebaseRules) addRuleset(src string) string {
	f.next++
	id := fmt.Sprintf("rs-%d", f.next)
	f.rulesets[id] = src
	return id
}

var (
	rulesReleasePath = regexp.MustCompile(`^/v1/projects/([^/]+)/releases/(cloud\.firestore)$`)
	rulesSetPath     = regexp.MustCompile(`^/v1/projects/([^/]+)/rulesets/([^/]+)$`)
)

func (f *FirebaseRules) handle(w http.ResponseWriter, r *http.Request, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m := rulesReleasePath.FindStringSubmatch(r.URL.Path); m != nil && r.Method == http.MethodGet && m[1] == f.project {
		f.reads++
		if f.seedAt > 0 && f.reads == f.seedAt && f.release == "" {
			f.release = f.addRuleset(f.seedSrc)
		}
		if f.release == "" || f.flaking > 0 {
			if f.flaking > 0 {
				f.flaking--
			}
			writeError(w, 404, "NOT_FOUND", "Requested entity was not found.")
			return
		}
		writeJSON(w, 200, map[string]any{"name": "projects/" + f.project + "/releases/cloud.firestore", "rulesetName": "projects/" + f.project + "/rulesets/" + f.release})
		return
	}
	if m := rulesSetPath.FindStringSubmatch(r.URL.Path); m != nil && r.Method == http.MethodGet && m[1] == f.project {
		src, ok := f.rulesets[m[2]]
		if !ok {
			writeError(w, 404, "NOT_FOUND", "Requested entity was not found.")
			return
		}
		writeJSON(w, 200, map[string]any{"name": "projects/" + f.project + "/rulesets/" + m[2],
			"source": map[string]any{"files": []any{map[string]any{"name": "firestore.rules", "content": src}}}})
		return
	}
	if r.Method == http.MethodPost && f.failW > 0 {
		f.failW--
		writeError(w, 500, "INTERNAL", "injected failure")
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/projects/"+f.project+"/rulesets":
		var in struct {
			Source struct {
				Files []struct{ Name, Content string } `json:"files"`
			} `json:"source"`
		}
		if err := json.Unmarshal(body, &in); err != nil || len(in.Source.Files) != 1 || in.Source.Files[0].Name == "" || in.Source.Files[0].Content == "" {
			writeError(w, 400, "INVALID_ARGUMENT", "a ruleset needs source.files with one named file")
			return
		}
		f.writes++
		id := f.addRuleset(in.Source.Files[0].Content)
		writeJSON(w, 200, map[string]any{"name": "projects/" + f.project + "/rulesets/" + id})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/projects/"+f.project+"/releases":
		var in struct{ Name, RulesetName string }
		if err := json.Unmarshal(body, &in); err != nil || in.Name != "projects/"+f.project+"/releases/cloud.firestore" {
			writeError(w, 400, "INVALID_ARGUMENT", "release name must be projects/<p>/releases/cloud.firestore")
			return
		}
		prefix := "projects/" + f.project + "/rulesets/"
		if len(in.RulesetName) <= len(prefix) || in.RulesetName[:len(prefix)] != prefix {
			writeError(w, 400, "INVALID_ARGUMENT", "rulesetName missing or of another project")
			return
		}
		id := in.RulesetName[len(prefix):]
		if _, ok := f.rulesets[id]; !ok {
			writeError(w, 404, "NOT_FOUND", "ruleset not found")
			return
		}
		if f.release != "" {
			writeError(w, 409, "ALREADY_EXISTS", "Release already exists.")
			return
		}
		f.writes++
		f.release = id
		f.flaking = f.flake
		f.flake = 0
		writeJSON(w, 200, map[string]any{"name": in.Name, "rulesetName": in.RulesetName})
	default:
		f.unhandled(w, r)
	}
}
