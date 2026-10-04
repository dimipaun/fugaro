package cli

import (
	"strings"
	"testing"
)

const testCommit = "0123456789abcdef0123456789abcdef01234567"

func withRelease(t *testing.T, version, commit string) {
	t.Helper()
	ov, oc := Version, Commit
	Version, Commit = version, commit
	t.Cleanup(func() { Version, Commit = ov, oc })
}

// A git tag is mutable: the plugin pin and the image tags follow it. The
// binary says which commit it was built from and the one line that checks the
// tag still names it, with no network call of its own.
func TestReleaseLineNamesTheCommitAndTheVerification(t *testing.T) {
	withRelease(t, "0.2.0", testCommit)
	line := releaseLine()
	for _, want := range []string{"v0.2.0", testCommit, "git ls-remote https://github.com/dimipaun/fugaro 'refs/tags/v0.2.0^{}' 'refs/tags/v0.2.0'", "lightweight tag"} {
		if !strings.Contains(line, want) {
			t.Errorf("%q lacks %q", line, want)
		}
	}
	if strings.Contains(line, "\n") {
		t.Error("not one line")
	}
	for name, tc := range map[string][2]string{
		"dev build":      {"dev", testCommit},
		"no commit":      {"0.2.0", "unknown"},
		"hostile commit": {"0.2.0", "abc; rm -rf /"},
		"short commit":   {"0.2.0", "abcdef"},
	} {
		withRelease(t, tc[0], tc[1])
		if got := releaseLine(); got != "" {
			t.Errorf("%s: %q", name, got)
		}
	}
}

func TestUpdateSkillsAndDoctorPluginPrintTheReleaseCommit(t *testing.T) {
	withRelease(t, "0.2.0", testCommit)
	wiringCheckout(t, `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"dimipaun/fugaro","ref":"v0.2.0"}}},"enabledPlugins":{"fugaro@fugaro":true}}`)
	out, _, _ := executeStdin(t, "", "update-skills", "--check")
	if !strings.Contains(out, testCommit) || !strings.Contains(out, "git ls-remote") {
		t.Errorf("update-skills --check:\n%s", out)
	}
	out, _, _ = executeStdin(t, "", "doctor", "--plugin")
	if !strings.Contains(out, testCommit) || !strings.Contains(out, "git ls-remote") {
		t.Errorf("doctor --plugin:\n%s", out)
	}
	out, _, _ = executeStdin(t, "", "doctor", "--plugin", "--json")
	if !strings.Contains(out, `"commit": "`+testCommit+`"`) {
		t.Errorf("doctor --plugin --json:\n%s", out)
	}
	out, _, _ = executeStdin(t, "", "update-skills", "--check", "--json")
	if !strings.Contains(out, `"commit": "`+testCommit+`"`) {
		t.Errorf("update-skills --check --json:\n%s", out)
	}
}
