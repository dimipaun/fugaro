package cli

import (
	"os"
	"strings"
	"testing"
)

// TestDocsWarnAboutAProjectWideGitCredential: `fugaro secrets set
// bitbucket-token --project-wide` (or github-app-key) stores one credential
// for the whole installation, and every repository the providers/lists
// reach mounts it. The security surface and the setup guide must both say
// so where a reader meets the command, since the reach is invisible in the
// repository's own fugaro.yaml — and the setup guide must not tell a reader
// that a second installation of the same App narrows it, because
// installations of one App share its key (design project-secrets.md §3,
// plan follow-up 1).
//
// The sentences wrap in the files, so the assertions are made against the
// whitespace-normalized text.
func TestDocsWarnAboutAProjectWideGitCredential(t *testing.T) {
	cases := []struct {
		path string
		want []string
	}{
		{"../../SECURITY.md", []string{
			"A project-wide git credential is not narrow.",
			"`fugaro secrets set bitbucket-token --project-wide` (or `github-app-key`) stores one workspace token, or one App key, for the whole installation",
			"the App key mints tokens for every repository the App is installed on",
			"Use it only where that reach is what you mean.",
		}},
		{"../../docs/gcp-setup.md", []string{
			"A project-wide git credential reaches other repositories:",
			"the App key mints tokens for every repository the App is installed on",
			"Store the git credential per repository unless that is the reach you mean",
			"give each repository its own App if it matters",
			"installations of one App share its key",
		}},
	}
	for _, tc := range cases {
		data, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		doc := strings.Join(strings.Fields(string(data)), " ")
		for _, want := range tc.want {
			if !strings.Contains(doc, strings.Join(strings.Fields(want), " ")) {
				t.Errorf("%s does not say %q", tc.path, want)
			}
		}
		// The old wording told a reader that a second installation of the
		// same App helps; it does not (one key, every installation).
		if strings.Contains(doc, "its own App installation") {
			t.Errorf("%s still says \"its own App installation\": installations of one App share its key", tc.path)
		}
	}
}
