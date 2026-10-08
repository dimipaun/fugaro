package cli

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// checkStaleYesClaims fails on a line naming fugaro image refresh that
// still says --yes does not exist (it does since 2026-10-08).
func checkStaleYesClaims(t *testing.T, path, line string) {
	t.Helper()
	if !strings.Contains(line, "fugaro image refresh") {
		return
	}
	for _, stale := range []string{"no --yes", "has no --yes", "cannot be scripted", "cannot run in CI"} {
		if strings.Contains(line, stale) {
			t.Errorf("%s: a line naming fugaro image refresh says %q: %s", path, stale, line)
		}
	}
}

// The design doc is scanned for the same stale claims (not for flags: it
// legitimately names the old init sequence and the flags refresh lacks).
func TestDesignDocNoStaleYesClaims(t *testing.T) {
	data, err := os.ReadFile("../../docs/design/image-refresh.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		checkStaleYesClaims(t, "docs/design/image-refresh.md", line)
	}
}

// TestDocsNameImageRefresh: the operator docs give the one command, not the
// four-step sequence, and every flag they give it is real.
func TestDocsNameImageRefresh(t *testing.T) {
	cmd := newImageRefreshCmd()
	if cmd.Name() != "refresh" {
		t.Fatalf("command is named %q, the docs say refresh", cmd.Name())
	}
	var found bool
	for _, c := range newImageCmd().Commands() {
		if c.Name() == "refresh" {
			found = true
		}
	}
	if !found {
		t.Error("refresh is not registered under image")
	}
	for _, path := range []string{"../../docs/gcp-setup.md", "../../docs/recipes.md", "../../docs/release.md", "../../.claude/skills/new-release/SKILL.md",
		"../../plugin/skills/working/reference/followup.md"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		doc := string(data)
		if !strings.Contains(doc, "fugaro image refresh") {
			t.Errorf("%s never names fugaro image refresh", path)
		}
		if strings.Contains(doc, "from outside the checkout") {
			t.Errorf("%s still gives the init --base from outside the checkout sequence", path)
		}
		for _, line := range strings.Split(doc, "\n") {
			if !strings.Contains(line, "fugaro image refresh") {
				continue
			}
			checkStaleYesClaims(t, path, line)
			// Every --flag token of the paragraph (gcp-setup.md) or sentence about the command: a flag of
			// another command the paragraph names, or one the command
			// deliberately lacks, is listed; any other must exist on it.
			scan := line
			if !strings.HasPrefix(line, "**Moving a repository") {
				// Outside the full description, only the sentence that
				// names the command: the rest of the line may be about
				// other commands and their flags.
				_, rest, _ := strings.Cut(line, "fugaro image refresh")
				scan, _, _ = strings.Cut(rest, ". ")
			}
			for _, f := range strings.Fields(scan) {
				i := strings.Index(f, "--")
				if i < 0 {
					continue
				}
				name := strings.TrimRight(strings.Trim(f[i+2:], "`"), "`,.;:)*")
				name, _, _ = strings.Cut(name, "=")
				switch name {
				case "":
				case "json", "plan-only":
					if cmd.Flags().Lookup(name) != nil {
						t.Errorf("%s: the docs say fugaro image refresh has no --%s, but it does", path, name)
					}
				case "yes":
					// 2026-10-08 decision: --yes exists (it is the one way
					// to run this command unattended), so the docs are
					// right to name it; the inverse check (json,
					// plan-only) is what catches a flag that does not
					// exist.
					if cmd.Flags().Lookup(name) == nil {
						t.Errorf("%s: the docs say fugaro image refresh has --%s, but it doesn't", path, name)
					}
				case "anchor", "base", "base-image":
				default:
					if cmd.Flags().Lookup(name) == nil {
						t.Errorf("%s: fugaro image refresh has no flag --%s", path, name)
					}
				}
			}
		}
	}
}

// TestDocsRefreshStepNumbers: gcp-setup.md numbers the refresh steps as the
// command does (1 preflight, 2 base, 3 check job, 4 builds), and the numbers
// in its prose match the "(N)" of the command's help and its stop message.
func TestDocsRefreshStepNumbers(t *testing.T) {
	long := newImageRefreshCmd().Long
	data, err := os.ReadFile("../../docs/gcp-setup.md")
	if err != nil {
		t.Fatal(err)
	}
	var para string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "**Moving a repository") {
			para = line
		}
	}
	if para == "" {
		t.Fatal("gcp-setup.md has no refresh paragraph")
	}
	// Prose "(N) The <name> step" against the help's "(N)" and refreshStepNames.
	for n, name := range map[int]string{2: "base", 3: "check-job", 4: "build"} {
		if !strings.Contains(long, fmt.Sprintf("(%d) ", n)) {
			t.Errorf("the command's help has no (%d)", n)
		}
		if want := refreshStepNames[n-1]; !strings.HasPrefix(want, strings.ReplaceAll(name, "-", " ")) {
			t.Errorf("step %d is %q in the command, the test expects %q", n, want, name)
		}
		if want := fmt.Sprintf("(%d) The %s step", n, name); !strings.Contains(para, want) {
			t.Errorf("gcp-setup.md lacks %q", want)
		}
	}
	for _, wrong := range []string{"(1) The", "(5) The"} {
		if strings.Contains(para, wrong) {
			t.Errorf("gcp-setup.md numbers a refresh step %q", wrong)
		}
	}
	if want := "job update (step 3)"; !strings.Contains(para, want) {
		t.Errorf("gcp-setup.md status line lacks %q", want)
	}
	if !strings.Contains(long, "(3) points the repository's daily image check job") {
		t.Error("the help no longer numbers the check job step 3")
	}
}
