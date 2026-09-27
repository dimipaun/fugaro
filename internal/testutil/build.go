package testutil

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

var (
	buildOnce sync.Once
	buildDir  string
	buildErr  error
)

func buildBinaries() {
	buildDir, buildErr = os.MkdirTemp("", "fugaro-testbin-")
	if buildErr != nil {
		return
	}
	for name, pkg := range map[string]string{
		"fugaro": "github.com/dimipaun/fugaro/cmd/fugaro",
		"claude": "github.com/dimipaun/fugaro/internal/agent/fakeclaude",
	} {
		out, err := exec.Command("go", "build", "-o", filepath.Join(buildDir, name), pkg).CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("go build %s: %v\n%s", pkg, err, out)
			return
		}
	}
}

// installBinary copies a built binary into a fresh directory of its own.
func installBinary(t *testing.T, name string) string {
	t.Helper()
	buildOnce.Do(buildBinaries)
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	data, err := os.ReadFile(filepath.Join(buildDir, name))
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return dst
}

// BuildFugaro returns the path of a fugaro binary alone in its directory.
func BuildFugaro(t *testing.T) string { return installBinary(t, "fugaro") }

// FakeClaude returns the path of a fake claude binary that replays script
// (see internal/agent/fakeclaude).
func FakeClaude(t *testing.T, script string) string {
	t.Helper()
	bin := installBinary(t, "claude")
	if err := os.WriteFile(filepath.Join(filepath.Dir(bin), "script.json"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	return bin
}

// FakeCall is one recorded invocation of the fake claude.
type FakeCall struct {
	Args   []string `json:"args"`
	Prompt string   `json:"prompt"`
	Env    []string `json:"env"`
	Dir    string   `json:"dir"`
}

// FakeClaudeCalls returns the invocations the fake at bin has recorded.
func FakeClaudeCalls(t *testing.T, bin string) []FakeCall {
	t.Helper()
	f, err := os.Open(filepath.Join(filepath.Dir(bin), "calls.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var calls []FakeCall
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 1<<20), 1<<24)
	for s.Scan() {
		var c FakeCall
		if err := json.Unmarshal(s.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, c)
	}
	return calls
}
