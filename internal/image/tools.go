package image

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/images"
)

// Tool is one row of images/base/tools.tsv: a command the base image must
// have (design base-image.md section 6).
type Tool struct {
	Name   string   // a command on PATH, or an absolute path
	Argv   []string // run, and must exit 0; nil means presence only
	Arches []string // GOARCH values it exists on; nil means every one
	Tier   string   // critical | kit | harness
}

var toolTiers = []string{"critical", "kit", "harness"}

// baseTools is the table checkTools reads; tests replace it.
var baseTools = images.BaseTools

// toolTimeout bounds one presence check; a version command is instant.
const toolTimeout = 30 * time.Second

// ParseTools reads tools.tsv: one tool per line, four tab-separated fields
// (name; argv, or "-" for presence only; "all" or a comma list of amd64 and
// arm64; tier). Blank lines and # comments are skipped.
func ParseTools(table string) ([]Tool, error) {
	var tools []Tool
	seen := map[string]bool{}
	for i, line := range strings.Split(table, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 4 {
			return nil, fmt.Errorf("tools.tsv line %d: want 4 tab-separated fields, have %d", i+1, len(f))
		}
		t := Tool{Name: f[0], Tier: f[3]}
		if t.Name == "" || seen[t.Name] {
			return nil, fmt.Errorf("tools.tsv line %d: the name %q is empty or repeated", i+1, t.Name)
		}
		seen[t.Name] = true
		if f[1] != "-" {
			if t.Argv = strings.Fields(f[1]); len(t.Argv) == 0 {
				return nil, fmt.Errorf("tools.tsv line %d: an empty argv; write - for presence only", i+1)
			}
		}
		if f[2] != "all" {
			for _, a := range strings.Split(f[2], ",") {
				if a != "amd64" && a != "arm64" {
					return nil, fmt.Errorf("tools.tsv line %d: architecture %q is not amd64 or arm64", i+1, a)
				}
				t.Arches = append(t.Arches, a)
			}
		}
		if !slices.Contains(toolTiers, t.Tier) {
			return nil, fmt.Errorf("tools.tsv line %d: tier %q is not one of %s", i+1, t.Tier, strings.Join(toolTiers, ", "))
		}
		tools = append(tools, t)
	}
	return tools, nil
}

// checkTools adds a "tool:<name>" check for each tool of tier ("critical",
// or "all" for every tier) that exists on this architecture: the command is
// found, and its argv exits 0 within toolTimeout with HOME set to a fresh
// empty directory, mise offline and no colour. That is presence only: no
// credentials and no provider is ever involved.
func checkTools(ctx context.Context, tier string, add func(string, bool, string, ...any)) {
	tools, err := ParseTools(baseTools)
	if err != nil {
		add("tools", false, "%v", err)
		return
	}
	home, err := os.MkdirTemp("", "fugaro-tools-home-")
	if err != nil {
		add("tools", false, "creating an empty HOME: %v", err)
		return
	}
	defer os.RemoveAll(home)
	for _, t := range tools {
		if (t.Arches != nil && !slices.Contains(t.Arches, runtime.GOARCH)) || (tier == "critical" && t.Tier != "critical") {
			continue
		}
		name := "tool:" + t.Name
		path := t.Name
		if filepath.IsAbs(path) {
			if fi, err := os.Stat(path); err != nil || fi.Mode()&0o111 == 0 {
				add(name, false, "%s is missing or not executable", path)
				continue
			}
		} else if p, err := exec.LookPath(t.Name); err != nil {
			add(name, false, "%s is not on PATH", t.Name)
			continue
		} else {
			path = p
		}
		if t.Argv == nil {
			add(name, true, "%s", path)
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, toolTimeout)
		cmd := exec.CommandContext(cctx, t.Argv[0], t.Argv[1:]...)
		// The last value of a repeated key wins (os/exec).
		cmd.Env = append(os.Environ(), "HOME="+home, "MISE_OFFLINE=1", "NO_COLOR=1")
		out, err := cmd.CombinedOutput()
		cancel()
		first, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
		if err != nil {
			add(name, false, "%s: %v: %s", strings.Join(t.Argv, " "), err, first)
			continue
		}
		add(name, true, "%s", first)
	}
}
