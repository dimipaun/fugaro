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
			"`fugaro secrets set bitbucket-token --project-wide` (or `github-app-key`) stores one workspace or project access token, or one App key, for the whole installation",
			"the App key mints tokens for every repository the App is installed on",
			"Use it only where that reach is what you mean.",
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
			"`bitbucket-token` excepted: a repository access token serves one repository, so it is stored per repository by default; the prompt offers it project-wide only through an explicit `project_secrets` allow-list entry, which is what an operator holding one workspace or project access token for many repositories writes",
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

// TestDocsStateTheBitbucketProjectTokenReach (plan T1.1, 0.12.1): an operator
// who holds one Bitbucket workspace or project access token may store it once
// for many repositories through `project_secrets.bitbucket-token` (D461, D462,
// D464), and the allow-list decides only which jobs mount it — not what the
// token reaches. Both documents a reader meets the option on have to say so in
// the same words, together with the rotation order (F3), the second operator's
// machine (D467), the cross-repository credential reach (F7) and review risk
// (F17, D476), that Bitbucket Data Center stays out of scope (D366, T2), the
// single author every repository's pull requests then carry (F8, D472),
// that no one has run the path with such a token yet (D468 = no, Check 180),
// and where the API-root lock stands: unchecked in 0.12.1, D474's in 0.13.0.
// The sentences wrap in the files, so the assertions are made against the
// whitespace-normalized text.
//
// Mutations (verified, run and restored): the Data Center paragraph's
// "in 0.12.1 a run sends the token to Bitbucket Cloud's API root and there is
// no list yet" put back → FAIL, on both new pins — the claim was the reverse of
// F7, since `providers.FromEnv` passes `FUGARO_BITBUCKET_API_URL` unchecked and
// a launcher can override the job's environment; the "is 0.13.0's (D474,
// T2.8)" clause deleted → FAIL. The base floor's move into step 4 is a
// placement fix and is deliberately unpinned: the fact is F3's, and pinning
// where a paragraph sits would fail on the next reordering that keeps it.
func TestDocsStateTheBitbucketProjectTokenReach(t *testing.T) {
	providers := strings.Join(strings.Fields(readDoc(t, "docs/git-providers.md")), " ")
	sec := strings.Join(strings.Fields(readDoc(t, "SECURITY.md")), " ")

	// The reach, word for word in both documents: the allow-list limits which
	// jobs mount the token, and what the token itself reaches is wider — the
	// launchers of an allow-listed repository included (F7, D475), and every
	// other credential of a repository an agent pushes into (review round 2).
	for _, doc := range []struct {
		path string
		text string
	}{{"docs/git-providers.md", providers}, {"SECURITY.md", sec}} {
		for _, want := range []string{
			"Every allow-listed run's agent holds the token's value, and so does every launcher of an allow-listed repository",
			"Through pushes to another repository's base or Fugaro branches, an agent also reaches **every other credential** that repository's runs and builds hold.",
		} {
			if !strings.Contains(doc.text, strings.Join(strings.Fields(want), " ")) {
				t.Errorf("%s does not say %q: the allow-list decides which jobs mount a workspace or project token, and the token's own reach is wider than that", doc.path, want)
			}
		}
	}

	// The option, the steps, and everything a reader has to know before opting
	// in. docs/git-providers.md is the page the operator follows.
	for _, want := range []string{
		// The credential (D461): the narrowest token that covers the set.
		"**workspace access token** or a **project access token** (both Bitbucket Premium) can serve many repositories",
		"Prefer a **project token** when the repositories sit in one Bitbucket project",
		// The steps, in order.
		"`project_secrets: { bitbucket-token: { allow_data_to: [myworkspace/*] } }`",
		"Run `fugaro update`, so the project container exists.",
		"`fugaro secrets set bitbucket-token --project-wide < token-file`",
		"Run each repository's `fugaro update --repo`. It asks nothing: the name is covered.",
		// The second operator's machine (D467).
		"Copy the `project_secrets.bitbucket-token` entry to every operator machine's local config: the shared config never carries it, and without it `fugaro update --repo` asks for a per-repository token.",
		// Rotation (F3).
		"Store the new token as a new version of the same secret, wait for the running executions to end, and then revoke the old token.",
		// Migration off a repository's own copy (F4).
		"wins over the project copy until its versions are disabled **and** `fugaro update --repo` has run with `--allow-delete '<address>'` for each address its refusal names (the job's `google_secret_manager_secret_iam_member.job[\"bitbucket-token\"]`)",
		// The cross-repository path an operator must not trust (F17).
		"never put the token's own account ID in `followup.trusted`",
		// The review risk the owner accepted rather than mitigated (D476).
		"A hostile agent in one repository can open its own malicious pull request in another, and a later review-only run there trusts it.",
		// Data Center (D366, T2).
		"**Bitbucket Data Center is not supported**",
		// And the honest half of the API-root story in 0.12.1: the override is
		// passed unchecked, a launcher can point it at a host of their own (F7),
		// and the lock is 0.13.0's. The paragraph said the opposite until the
		// review of this round, which is the direction a security page must not
		// drift in.
		"**In 0.12.1 there is no such list and no check: a launcher can override the job's environment on `jobs.run`, `FUGARO_BITBUCKET_API_URL` among it, and `providers.FromEnv` passes that value unchecked as the API root, so a launcher can have the run send the token to a host of their own (F7).**",
		"The lock that refuses any root but `https://api.bitbucket.org/2.0` outside a local invocation, except one the owner allow-lists in the policy, is 0.13.0's (D474, T2.8).",
		// One author on every repository (F8, D472).
		"every enrolled repository's pull requests and comments carry the same author",
		// The live check (D468 = no).
		"Not yet live-checked with a workspace or project token (Check 180).",
	} {
		if !strings.Contains(providers, strings.Join(strings.Fields(want), " ")) {
			t.Errorf("docs/git-providers.md does not say %q: it is the page an operator reads to opt in to one Bitbucket token for many repositories", want)
		}
	}

	// SECURITY.md states the option itself, and the D476 risk, where a reader
	// meets the project-scope default and review mode's trust rule.
	for _, want := range []string{
		"an operator who holds one **workspace or project access token** may instead mount it in many repositories at once through the allow-list",
		"A hostile agent in one repository can open its own malicious pull request in another, and a later review-only run there trusts it.",
	} {
		if !strings.Contains(sec, strings.Join(strings.Fields(want), " ")) {
			t.Errorf("SECURITY.md does not say %q: D462 accepted the token's reach on the record, and SECURITY.md is that record", want)
		}
	}
}
