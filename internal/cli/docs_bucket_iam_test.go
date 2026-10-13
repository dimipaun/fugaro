package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Since 0.7.0 launchers hold objectViewer and a runs/-conditioned objectUser
// on the runs bucket, never objectAdmin (docs/design/bucket-iam.md). The
// sentences that said otherwise are gone from every doc but the hardening's
// own design, the plans and the release notes (not scanned).
func TestDocsLaunchersDoNotHoldObjectAdmin(t *testing.T) {
	stale := []string{
		"Launchers and operators already hold `objectAdmin`",
		"launchers and operators already hold `roles/storage.objectAdmin`",
		"A launcher also gets `roles/storage.objectAdmin`",
		"`fugaroJobRunner` on each workflow job (§3.4), `roles/storage.objectAdmin`",
		"`roles/storage.objectAdmin` on it only to launchers",
		"Anyone holding `roles/storage.objectAdmin` on the runs bucket: launchers and operators",
		"The bucket stays writable by launchers until the hardening lands",
	}
	var files []string
	for _, g := range []string{"../../docs/*.md", "../../docs/design/*.md", "../../README.md", "../../plugin/skills/*/SKILL.md", "../../plugin/skills/*/reference/*.md"} {
		m, _ := filepath.Glob(g)
		files = append(files, m...)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "bucket-iam.md") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range stale {
			if strings.Contains(string(data), s) {
				t.Errorf("%s still says %q", f, s)
			}
		}
	}
	gs, err := os.ReadFile("../../docs/gcp-setup.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"write only `runs/`", "runs-bucket-iam", "add-iam-policy-binding", "DATA_WRITE"} {
		if !strings.Contains(string(gs), want) {
			t.Errorf("docs/gcp-setup.md lacks %q", want)
		}
	}
}
