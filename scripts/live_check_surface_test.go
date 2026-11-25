package scripts_test

// The strict fakes the live checks' tests drive (scripts/live-checks/lib/) must
// refuse a command or a flag the real CLI does not take: that is the whole point
// of them, and a wrong assumption an agent made about a flag is exactly what
// they exist to catch. The table the fake `fugaro` checks against is generated
// here, from `cli.NewRootCmd()` itself, so it is never a second-hand guess:
// `scripts/live-checks/lib/fugaro-surface.tsv`.
//
// Regenerate it after a CLI change, from the repository root:
//
//	go test ./scripts -run TestLiveCheckCLISurface -update-live-check-surface
//
// and the test fails, naming this command, until the table matches the tree.

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var updateLiveCheckSurface = flag.Bool("update-live-check-surface", false,
	"rewrite scripts/live-checks/lib/fugaro-surface.tsv from the CLI's own command tree")

const liveCheckSurfacePath = "live-checks/lib/fugaro-surface.tsv"

// liveCheckSurface is the whole of what the strict fake `fugaro` accepts: every
// command path (canonical and by alias), and, per command, every flag it takes
// with its arity. `bool` flags may be written bare; `value` ones are followed by
// the value they take, which is what lets the verifier walk an argv without
// re-reading the check's own argument rules.
//
// A flag row keeps the path in the second field, beside a command row's, and
// writes the flag's own name last. The order is deliberate: with the name in
// the second field, a live `init` subcommand's flag list would spell the
// retired verb's pattern -- `<word> init`, then a flag -- and the census that
// scans the live tree for it (internal/cli/docs_live_init_census_test.go,
// initNameRE) would read this generated table as a mention of the retired
// command. The arity between the path and the name keeps it out of that.
func liveCheckSurface() []string {
	root := cli.NewRootCmd()
	var lines []string
	var walk func(c *cobra.Command, path []string)
	walk = func(c *cobra.Command, path []string) {
		// A hidden command is not one this CLI offers. The retired tombstone
		// (`fugaro`, then the old verb) is the only one, and the real binary
		// exits 1 on it rather than running anything, so the fake refuses it
		// as an unknown command -- which is what a check that typed the old
		// name would meet.
		if c.Hidden {
			return
		}
		p := append(append([]string{}, path...), c.Name())
		// Paths are relative to the root, so `fugaro accounts pool add` is
		// `accounts pool add` here: it is what a fake's argv carries. The root's
		// own flags sit under the empty path.
		rel := p[1:]
		if len(path) > 0 {
			lines = append(lines, "cmd\t"+strings.Join(rel, " "))
			for _, alias := range c.Aliases {
				withAlias := append(append([]string{}, rel[:len(rel)-1]...), alias)
				lines = append(lines, "cmd\t"+strings.Join(withAlias, " "))
			}
		}
		c.Flags().VisitAll(func(f *pflag.Flag) {
			arity := "value"
			if f.NoOptDefVal != "" {
				arity = "bool"
			}
			shorthand := ""
			if f.Shorthand != "" {
				shorthand = "-" + f.Shorthand
			}
			lines = append(lines, fmt.Sprintf("flag\t%s\t%s\t%s\t--%s",
				strings.Join(rel, " "), arity, shorthand, f.Name))
		})
		for _, sub := range c.Commands() {
			walk(sub, p)
		}
	}
	walk(root, nil)
	// `help` is cobra's own subcommand and `--help`/`-h` its own flag, both
	// added at execution rather than at construction: the real `fugaro run
	// --help` works, so the fake must take it too. `--version`/`-v` resolve at
	// the root (cli.NormalizeVersionArgs reads a leading one before cobra).
	lines = append(lines, "cmd\thelp")
	for _, path := range append([]string{""}, surfaceCommandPaths(lines)...) {
		lines = append(lines, "flag\t"+path+"\tbool\t-h\t--help")
	}
	lines = append(lines, "flag\t\tbool\t-v\t--version")
	sort.Strings(lines)
	return lines
}

func surfaceCommandPaths(lines []string) []string {
	var paths []string
	for _, line := range lines {
		if fields := strings.Split(line, "\t"); fields[0] == "cmd" {
			paths = append(paths, fields[1])
		}
	}
	return paths
}

func surfaceFileText() string {
	return "# fugaro's own command surface, generated from cli.NewRootCmd().\n" +
		"# Regenerate: go test ./scripts -run TestLiveCheckCLISurface -update-live-check-surface\n" +
		strings.Join(liveCheckSurface(), "\n") + "\n"
}

func TestLiveCheckCLISurface(t *testing.T) {
	path := filepath.Join(testutil.ModuleRoot(), "scripts", liveCheckSurfacePath)
	want := surfaceFileText()
	if *updateLiveCheckSurface {
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v\nregenerate it: go test ./scripts -run TestLiveCheckCLISurface -update-live-check-surface", path, err)
	}
	if string(got) != want {
		t.Fatalf("%s is not the CLI's own command surface: the CLI changed and the table did not.\n"+
			"regenerate it: go test ./scripts -run TestLiveCheckCLISurface -update-live-check-surface", path)
	}
}

// TestLiveCheckAllowListsAreReal pins the other fakes' short allow-lists
// (scripts/live-checks/lib/allow/<check>.<tool>) to the tools they name: every
// `fugaro` line must be a command and a flag the CLI really has, so a check
// cannot assert a flag that does not exist the way check631 did with
// `fugaro accounts add --project`.
func TestLiveCheckAllowListsAreReal(t *testing.T) {
	commands := map[string]map[string]bool{}
	for _, line := range liveCheckSurface() {
		fields := strings.Split(line, "\t")
		switch fields[0] {
		case "cmd":
			if commands[fields[1]] == nil {
				commands[fields[1]] = map[string]bool{}
			}
		case "flag":
			if commands[fields[1]] == nil {
				commands[fields[1]] = map[string]bool{}
			}
			commands[fields[1]][fields[2]] = true
			if fields[4] != "" {
				commands[fields[1]][fields[4]] = true
			}
		}
	}
	for _, file := range liveCheckAllowLists(t, "fugaro") {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			fields := strings.SplitN(line, "\t", 3)
			command := strings.TrimSpace(fields[0])
			if command == "fugaro" {
				command = ""
			}
			flags, known := commands[command]
			if !known {
				t.Errorf("%s:%d: names fugaro command %q, which the CLI does not have", file, i+1, command)
				continue
			}
			if len(fields) < 2 {
				continue
			}
			for _, f := range strings.Fields(fields[1]) {
				if !flags[f] {
					t.Errorf("%s:%d: %s takes no flag %s", file, i+1, command, f)
				}
			}
		}
	}
}

// TestLiveCheckStrictLib drives the harness itself: the strict fakes' verifiers
// and the shared --preflight. It is one bash test, run here so `go test ./scripts`
// covers it the way it covers every check's own test.
func TestLiveCheckStrictLib(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not installed")
	}
	path := filepath.Join(testutil.ModuleRoot(), "scripts", "live-checks", "lib", "strict_test.sh")
	cmd := exec.Command("bash", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed: %v\n%s", path, err, out)
	}
}

func liveCheckAllowLists(t *testing.T, tool string) []string {
	t.Helper()
	dir := filepath.Join(testutil.ModuleRoot(), "scripts", "live-checks", "lib", "allow")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read %s: %v", dir, err)
	}
	var paths []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), "."+tool) {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(paths)
	return paths
}
