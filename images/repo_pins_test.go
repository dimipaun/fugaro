package images_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Fugaro's mise.toml pins the Go of go.mod's go line's minor series and the
// Terraform CI tests with, and keeps GOTOOLCHAIN local, as the go base did.
func TestRepositoryMisePins(t *testing.T) {
	mise, err := os.ReadFile("../mise.toml")
	if err != nil {
		t.Fatal(err)
	}
	pin := func(name string) string {
		m := regexp.MustCompile(`(?m)^` + name + ` = "([^"]+)"$`).FindStringSubmatch(string(mise))
		if m == nil {
			t.Fatalf("mise.toml has no %s pin", name)
		}
		return m[1]
	}
	mod, _ := os.ReadFile("../go.mod")
	gm := regexp.MustCompile(`(?m)^go (\d+\.\d+)`).FindStringSubmatch(string(mod))
	if gm == nil || !strings.HasPrefix(pin("go"), gm[1]+".") {
		t.Errorf("mise.toml go %s is not in go.mod's %v series", pin("go"), gm)
	}
	ci, _ := os.ReadFile("../.github/workflows/ci.yml")
	tm := regexp.MustCompile(`terraform_version: ([\d.]+)`).FindStringSubmatch(string(ci))
	if tm == nil || pin("terraform") != tm[1] {
		t.Errorf("mise.toml terraform %s, ci.yml %v", pin("terraform"), tm)
	}
	if !strings.Contains(string(mise), "GOTOOLCHAIN = \"local\"") {
		t.Error("mise.toml does not keep GOTOOLCHAIN local")
	}
}
