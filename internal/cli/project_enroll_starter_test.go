package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

func publishedLayerAt(t *testing.T, r *addRig, text string) {
	t.Helper()
	p := filepath.Join(r.dir, "runs", filepath.FromSlash(config.LayerKey))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A checkout with no fugaro.yaml gets the minimal one `fugaro config init`
// writes, in the working tree and nowhere else, and the enrolment goes on; a
// checkout that has one is left as it is.
func TestReposAddWritesTheStarterFugaroYAML(t *testing.T) {
	t.Run("none", func(t *testing.T) {
		r := enrolRig(t)
		publishedLayerAt(t, r, testProjectLayer)
		file := enrolCheckoutFile(t)
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		out, errOut, err := r.enrol(t, enrolRepo)
		// The starter is uncommitted, so the job step waits for it to reach the
		// default branch: the enrolment's other steps are written all the same.
		if err != nil && !strings.Contains(err.Error(), "git switch") {
			t.Fatalf("project repos add %s: %v\nstdout:\n%s\nstderr:\n%s", enrolRepo, err, out, errOut)
		}
		if want := "fugaro.yaml written for " + enrolRepo + "\n"; !strings.Contains(out, want) {
			t.Errorf("the output lacks %q:\n%s", want, out)
		}
		if got := readFileString(t, file); !strings.HasPrefix(got, minimalAnchored) || strings.Contains(got, "agent") {
			t.Errorf("fugaro.yaml = %q, want the minimal file and no model keys", got)
		}
		if got := readFileString(t, r.cfg); !strings.Contains(got, enrolRepo) {
			t.Errorf("the repository was not enrolled:\n%s", got)
		}
	})

	t.Run("one", func(t *testing.T) {
		r := enrolRig(t)
		publishedLayerAt(t, r, testProjectLayer)
		file := enrolCheckoutFile(t)
		out, errOut, err := r.enrol(t, enrolRepo)
		if err != nil {
			t.Fatalf("project repos add %s: %v\nstdout:\n%s\nstderr:\n%s", enrolRepo, err, out, errOut)
		}
		if strings.Contains(out, "fugaro.yaml written") {
			t.Errorf("a repository with a fugaro.yaml had one written:\n%s", out)
		}
		// The Repository stage adds the gcp_project anchor to any file; nothing else moves.
		if got := strings.Replace(readFileString(t, file), "gcp_project: proj-1234\n", "", 1); got != enrolCheckoutYAML {
			t.Errorf("the repository's own fugaro.yaml changed: %q", got)
		}
	})
}
