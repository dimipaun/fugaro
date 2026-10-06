package localcfg

import (
	"path/filepath"
	"strings"
	"testing"
)

// --name is exact: a different case or a stray space is not a project name,
// and never silently creates a config.
func TestSelectNameIsExact(t *testing.T) {
	xdg := t.TempDir()
	getenv := func(k string) string { return map[string]string{"XDG_CONFIG_HOME": xdg}[k] }
	writeFile(t, filepath.Join(xdg, "fugaro", "projects", "aurora.yaml"), projectYAML("aurora", "aurora-gcp-1"))
	for _, name := range []string{"Aurora", " aurora"} {
		_, _, err := Select(SelectInput{Name: name, Creating: true, Getenv: getenv})
		if err == nil || !strings.Contains(err.Error(), "is not a project name") {
			t.Errorf("--name %q: %v", name, err)
		}
	}
}
