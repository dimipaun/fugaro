package images_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runBudget(t *testing.T, bytes string, args ...string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	fake := "#!/bin/sh\n[ \"$1 $2\" = \"image inspect\" ] || exit 2\necho " + bytes + "\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", append([]string{"size-budget.sh", "fake-image"}, args...)...)
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestSizeBudget(t *testing.T) {
	if out, err := runBudget(t, "3100000000"); err != nil || !strings.Contains(out, "3100 MB") || strings.Contains(out, "warning") {
		t.Errorf("under: err=%v %s", err, out)
	}
	if out, err := runBudget(t, "3600000000"); err != nil || !strings.Contains(out, "::warning::") {
		t.Errorf("warn: err=%v %s", err, out)
	}
	if out, err := runBudget(t, "4100000000"); err == nil || !strings.Contains(out, "over its budget") {
		t.Errorf("over: err=%v %s", err, out)
	}
	if out, err := runBudget(t, "1500000000", "1000"); err == nil {
		t.Errorf("a custom budget is ignored: %s", out)
	}
}
