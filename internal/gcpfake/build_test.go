package gcpfake

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fake's build step, like the real template, bakes in the commit the
// render step read: a build step that does not pass it as FUGARO_COMMIT
// fails, and the one it passes is logged.
func TestBuildFakePinsTheRenderedCommit(t *testing.T) {
	f := NewBuild(t)
	submit := func(script string) string {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"steps": []any{
			map[string]any{"id": "render"},
			map[string]any{"id": "build", "args": []any{"-c", script}},
		}})
		resp, err := http.Post(f.URL+"/v1/projects/p/locations/r/builds", "application/json", strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		var op struct {
			Metadata struct{ Build struct{ ID string } }
		}
		if err := json.Unmarshal(data, &op); err != nil || op.Metadata.Build.ID == "" {
			t.Fatalf("%s: %v", data, err)
		}
		return op.Metadata.Build.ID
	}
	f.Steps = map[string]func(string) error{"render": func(dir string) error {
		return os.WriteFile(filepath.Join(dir, "source-commit"), []byte("c0ffee\n"), 0o644)
	}}
	pinned := submit(`docker build --build-arg "FUGARO_COMMIT=$$commit" .`)
	if f.runs[pinned].status != "SUCCESS" || !strings.Contains(strings.Join(f.Log(pinned), "\n"), "build: commit c0ffee") {
		t.Errorf("pinned build: %s, log %v", f.runs[pinned].status, f.Log(pinned))
	}
	unpinned := submit(`docker build .`)
	if f.runs[unpinned].status != "FAILURE" {
		t.Errorf("a build that does not pin the commit ended %s", f.runs[unpinned].status)
	}
}
