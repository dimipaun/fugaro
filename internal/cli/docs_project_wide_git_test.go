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
			"Store the git credential with `--repo acme/webapp` unless that is the reach you mean",
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

// TestSetupSkillStoresTheGitCredentialWithoutRepo: the setup skill's
// user-runs block is the command a user copies, and since D2 the App key's
// default store is the project's own container (plan Task 2.1 step 1). A
// --repo there would store a repository copy instead, which wins over the
// project copy for that repository from then on (design
// project-secrets.md §5) — the very state the default exists to make
// unnecessary, and the one both existing installations are already in for
// the names that matter (§6 step 4a). The CLI's own note at store time says
// which scope each name took, so the skill does not restate it.
//
// bitbucket-token is the other way round, and the test must not read the App
// key's rule onto it (code review, medium): it has no project-wide default at
// all (config.PerRepositorySecret, the owner's ruling of 2026-10-15 — a
// repository access token is issued for one repository), so the command the
// skill gives for it names --repo, and the explicit form is the one that
// works outside a checkout too. A test that forbade `--repo` for every git
// credential failed on exactly that correct line. What is wrong for this
// name is the opposite: the project-wide store, which is refused for it
// unless the operator's own local config allow-lists repositories.
func TestSetupSkillStoresTheGitCredentialWithoutRepo(t *testing.T) {
	skill, err := os.ReadFile("../../plugin/skills/setup/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(skill)
	if !strings.Contains(text, "fugaro secrets set github-app-key < <path-to-pem-file>") {
		t.Error("the setup skill no longer gives the App key as the project-wide default (`fugaro secrets set github-app-key < <path-to-pem-file>`)")
	}
	if strings.Contains(text, "fugaro secrets set github-app-key --repo") {
		t.Error("the setup skill passes --repo to the App key: a repository copy wins over the project copy from then on, and the App key is stored project-wide by default")
	}
	// bitbucket-token is the opposite case, and the App key's rule must not be
	// read onto it (code review, medium): it has no project-wide default at
	// all (config.PerRepositorySecret, the owner's ruling of 2026-10-15 — a
	// repository access token is issued for one repository), so `--repo` is
	// the right spelling for it and the bare command stores the same copy. A
	// test that forbade `--repo` for every git credential failed on exactly
	// that correct line. What is wrong for this name is the other store: the
	// project-wide one, refused for it unless the operator's own local config
	// allow-lists repositories.
	if strings.Contains(text, "secrets set bitbucket-token --project-wide") {
		t.Error("the setup skill stores bitbucket-token project-wide: that store is refused for the name, since a repository access token serves one repository")
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

// TestDocsStateTheProjectScopeDefaultWhereASecretIsStored (plan Task 2.3
// step 3): D2 changed where `fugaro secrets set` puts a value, so every page
// a person meets the command on has to say the default and what it changes —
// the project layer (the scope table's "outside this table" paragraph), the
// multi-model guide (which keys a job mounts, and when a re-run of update
// --repo is what changes that), the dogfooding page (whose own installation
// enrolls three repositories), and the setup skill, where the changed prompt
// wording and doctor's own scope listing are what a person reads before
// recommending a store. The sentences wrap in the files, so the assertions
// are made against the whitespace-normalized text.
func TestDocsStateTheProjectScopeDefaultWhereASecretIsStored(t *testing.T) {
	cases := []struct {
		path string
		want []string
	}{
		{"../../docs/project-layer.md", []string{
			"a secret name is stored **project-wide by default**",
			"`fugaro secrets set NAME` with no flag writes the project's own container",
			"`--repo` is what asks for one repository's own copy instead",
			"warns when a repository copy shadows the project's",
		}},
		{"../../docs/multi-model.md", []string{
			"One key for the whole project, and it is the default.",
			"`--project-wide` is still accepted as a synonym",
			"a project-scope copy created or deleted after a job's apply changes what that job mounts only on the next `fugaro update --repo`",
		}},
		{"../../docs/dogfooding.md", []string{
			"Both are stored once for the project",
			"being stored once is not the same as reaching every job",
			"which `fugaro doctor` lists per name and warns about when it happens",
		}},
		{"../../plugin/skills/setup/SKILL.md", []string{
			"`secret-scope` line says which copy it resolves for every name",
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
	}
}

// TestDocsSayTheInitPromptHasNoRepoFlagAndBitbucketIsPerRepository: the two
// sentences the code review of this PR found wrong (item 1), corrected once
// more in the second review round. `fugaro update --repo`'s secrets stage has no
// --repo of its own to pass (internal/cli/initsecrets.go, the prompt's own
// "press Ctrl-C here" text is the way out), so a page that tells a reader to
// pass one at the prompt describes a flag that does not exist; the resume line
// names the door it resumes, `fugaro update --repo`, because "the run" already
// means an artifact run (internal/runner/halt.go's "start the run again"); and
// `bitbucket-token` is a PerRepositorySecret, which is *not* the same as never
// project-wide (internal/config/project_secrets.go): D2's sweep skips it, so it
// is per-repository by default, but `AllowedForProjectSecret` is asked before
// the sweep in infra.ProjectSecretCandidate, so an explicit
// `project_secrets.bitbucket-token` allow-list entry does store it project-wide.
// The pages have to say that, not "never" — SECURITY.md and gcp-setup.md
// already do.
func TestDocsSayTheInitPromptHasNoRepoFlagAndBitbucketIsPerRepository(t *testing.T) {
	cases := []struct {
		path string
		want []string
	}{
		{"../../plugin/skills/setup/SKILL.md", []string{
			"Setup's prompts (step 9) have no `--repo` to pass",
			"except a per-repository one (`bitbucket-token`, project-wide only through an explicit `project_secrets` allow-list entry)",
			"per-repository by default; project-wide only through that same entry",
			"press Ctrl-C at the prompt and run the command it printed in your own terminal, then start setup again",
			"a covered name is not asked for",
		}},
		{"../../plugin/skills/setup/reference/decisions.md", []string{
			"`bitbucket-token` reaches project scope only through an explicit `project_secrets` entry",
		}},
		{"../../docs/multi-model.md", []string{
			"the prompt has no `--repo` to pass",
			"press Ctrl-C at it, run the command it printed (`fugaro secrets set <name> --repo <owner>/<name>`) in your own terminal, and start `fugaro update --repo` again",
			"`bitbucket-token` excepted: a repository access token serves one repository, so it is stored per repository by default and the prompt offers it project-wide only through an explicit `project_secrets` allow-list entry",
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
		// The wrong sentences, in every spelling this PR gave them: the
		// first two told a reader the update prompt takes `--repo`, the third
		// said bitbucket-token is never project-wide.
		for _, wrong := range []string{
			"store project-wide unless `--repo` is passed",
			"stored project-wide unless you pass `--repo`",
			"never project-wide",
			"no prompt ever offers it project-wide",
		} {
			if strings.Contains(doc, wrong) {
				t.Errorf("%s still tells a reader the update prompt takes `--repo`: %q", tc.path, wrong)
			}
		}
	}
}
