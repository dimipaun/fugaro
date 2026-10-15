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

// TestSecurityMdStatesTheProjectScopeDefault (plan Task 2.1 step 5): D2
// changes a security default, so SECURITY.md has to say what the default is,
// what it reverses, what "every enrolled repository" means before and after a
// policy is published, the profile as the boundary between repositories in a
// shared job, and that the data-policy gate is unchanged. The sentences wrap
// in the file, so the assertions are made against the whitespace-normalized
// text.
func TestSecurityMdStatesTheProjectScopeDefault(t *testing.T) {
	data, err := os.ReadFile("../../SECURITY.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.Join(strings.Fields(string(data)), " ")
	for _, want := range []string{
		"### Project scope is the default for a name that has one",
		"stores the value once for the whole installation",
		"unless `--repo` is passed, which stores one repository's own copy instead",
		"`--project-wide` is still accepted as a synonym of the default",
		"storing at project scope now *is* the opt-in",
		"**The reach is every enrolled repository.**",
		"every repository the local config knows",
		"every repository that policy admits",
		"Inside a shared job there is no IAM boundary between repositories — the profile is the boundary.",
		"**The data-policy gate is unchanged.**",
		"`workflows.<n>.secrets` cannot be shared this way at all",
	} {
		if !strings.Contains(doc, strings.Join(strings.Fields(want), " ")) {
			t.Errorf("SECURITY.md does not say %q", want)
		}
	}
	// The default is not a claim that every name is shared: a name with no
	// project-wide meaning keeps the repository scope it had.
	if !strings.Contains(doc, "A name that has no project-wide meaning") {
		t.Error("SECURITY.md does not say which names the default leaves alone")
	}
}
