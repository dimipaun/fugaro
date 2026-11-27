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
		"`workflows.<n>.secrets` **can** be shared this way, through the operator's own `workflow_secrets.<name>` entry",
		"A **shared project job** never mounts one at all",
		// IS11, D2313: a workflow's own `secrets:` entry is a project secret
		// like any other name now, and a repository-level copy of one is
		// refused. The section said the opposite — a name with no project-wide
		// meaning "keeps the repository scope it had" — which is the pre-flip
		// wording.
		"A workflow's own `secrets:` entry is a project secret too",
	} {
		if !strings.Contains(doc, strings.Join(strings.Fields(want), " ")) {
			t.Errorf("SECURITY.md does not say %q", want)
		}
	}
	// The sentence that read a workflow's own secret out of the default may not
	// come back: the store it describes is the one the flip refuses.
	if strings.Contains(doc, "A name that has no project-wide meaning") {
		t.Error("SECURITY.md says a workflow's own secret keeps the repository scope it had: the flip makes it a project secret, and `fugaro secrets set --repo` refuses one")
	}
	// And it is no longer a claim that a workflow's own secret is never
	// shared: the block that shares one exists, so the sentence that denied it
	// is the pre-feature wording (W6, D1272) and may not come back.
	if strings.Contains(doc, "`workflows.<n>.secrets` cannot be shared this way at all") {
		t.Error("SECURITY.md denies that a workflow's own secret is shared this way: the operator's workflow_secrets block is what shares one, and a security page that says otherwise is a reach a reader believes")
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
// where the API-root lock stands: unchecked in 0.12.1, D474's in 0.14.0 (D1983
// corrected the page from 0.13.0), and the operator step it owes (D1984),
// and what the D490 pointer says now that the proxy is built and its flip has
// filled: the proxy keeps the token out of the run's own git traffic, and the
// job's mount, with the accessor behind it, is gone on a repository the
// published node admits with `proxy: true`, which is what
// `config.ProxiedGitSecretNames` names `bitbucket-token` for on this tree
// (the 2026-10-27 amendment to plan T1.1, B1.10, and B1.13 part two).
// Both readings of that flip are pinned below and they have to agree: the
// paragraph that still described the list as empty was the pre-flip one, and
// S7 of docs/shared-jobs-default is the sweep that brought the page and these
// pins in step with the code.
// The sentences wrap in the files, so the assertions are made against the
// whitespace-normalized text.
//
// Mutations (verified, run and restored): the Data Center paragraph's
// "in 0.12.1 a run sends the token to Bitbucket Cloud's API root and there is
// no list yet" put back → FAIL, on both new pins — the claim was the reverse of
// F7, since `providers.FromEnv` passes `FUGARO_BITBUCKET_API_URL` unchecked and
// a launcher can override the job's environment; the "is 0.13.0's (D474,
// T2.8)" clause deleted → FAIL (and the clause now reads 0.14.0's, the D1983
// correction of 2026-11-10, with the D1983 pins beside it); the D490 pointer
// sentence deleted → FAIL,
// which is the one that says the page describes a token in the job and the
// built proxy is what keeps it out of the run's own git traffic while the
// mount stays until the flip. The base floor's move into step 4
// is a placement fix and is deliberately unpinned: the fact is F3's, and pinning
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
		// The variables-table row is what an operator reads when they set the
		// variable, and until D1983 (2026-11-10) it did not name the list the
		// runner holds that value to (T2.3).
		"Optional API root (default `https://api.bitbucket.org/2.0`). Any other root must also be listed in the installation's `bitbucket_api_roots`, published through the policy (`docs/project-layer.md`), or a cloud run is refused",
		// The third D1983 correction: the 2026-10-15 ruling that Bitbucket
		// stays on the per-repository path keeps its shared-job half, and the
		// half the flip overtakes is amended by date rather than rewritten.
		"**Amended 2026-11-10 (D1983): on a proxied repository the job loses its `bitbucket-token` with the flip.**",
		// The sentence carries its cross-reference in the right direction: the
		// section it names is below it on the page, as every other reference to
		// it in this file says. Its list clause names `bitbucket-token`, as it
		// has since B1.13 part two filled the list: the paragraph that still
		// read `[]string{}` described the state before that flip, and a page
		// whose two readings of one list disagree is the one a reader takes the
		// weaker half of.
		"whose list `config.ProxiedGitSecretNames` names `bitbucket-token`, so \"keeps its own job and its own `bitbucket-token`\" describes the pre-flip state and every repository the published node does not admit with `proxy: true` — on a proxied one the run reads no token and the service puts the credential on the wire (\"What the agent gets\" below)",
		// And the honest half of the API-root story in 0.12.1: the override is
		// passed unchecked, a launcher can point it at a host of their own (F7),
		// and the lock is 0.14.0's -- not 0.13.0's, which is what the sentence
		// said until D1983 (2026-11-10) landed the three corrections BW-T2.8
		// and B1.13 part one owed this page. The paragraph said the opposite
		// until the review of this round, which is the direction a security page
		// must not drift in.
		"**In 0.12.1 there is no such list and no check: a launcher can override the job's environment on `jobs.run`, `FUGARO_BITBUCKET_API_URL` among it, and `providers.FromEnv` passes that value unchecked as the API root, so a launcher can have the run send the token to a host of their own (F7).**",
		"The lock that refuses any root but `https://api.bitbucket.org/2.0` outside a local invocation, except one the owner allow-lists in the policy, is 0.14.0's (D474, BW-T2.8):",
		// The operator's own step for that lock (D1984, plan task T2.11's step
		// 5): an installation with a non-default root lists it and republishes
		// before it upgrades its runners, and the refusal is named where a
		// reader can find the code that makes it.
		"an installation with a non-default root lists it in `bitbucket_api_roots` and republishes before it upgrades its runners (D1984, `docs/secrets-brokering/release-0.14.0-operators.md`)",
		// One author on every repository (F8, D472).
		"every enrolled repository's pull requests and comments carry the same author",
		// The live check (D468 = no).
		"Not yet live-checked with a workspace or project token (Check 180).",
		// The pointer sentence the 2026-10-27 amendment added (D490), as
		// B1.10 moved its fact: the page describes the token reaching the
		// job, the one design that keeps it out of the job is built, and
		// every reading above is now scoped to a repository the published
		// node does not proxy -- the run reads no token only where that
		// node admits the repository with `proxy: true`.
		"A proxy that keeps the token out of the **run's own git traffic** is designed in [docs/secrets-brokering](secrets-brokering/README.md) and **built** (D490, \"What the agent gets\" below): on a repository the published node admits with `proxy: true` the runner reads no token, the run's own git traffic carries none, and the service puts the credential on the wire",
		// The other half, which moved with B1.13 part two: the mount -- and
		// the accessor behind it -- are gone for a proxied repository, so no
		// reach above is a read of the runner's `/proc` there.
		"and the job's mount and the accessor behind it are gone with the flip, whose list `config.ProxiedGitSecretNames` names `bitbucket-token`, so on such a repository no reach above is a read of the runner's `/proc`",
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

// TestDocsStateBitbucketSharedJobIsolation (plan T2.6's phase-3 half, 2026-11-16):
// a shared Bitbucket job's isolation, which is the opposite of what the same
// pages said until D460 and D2302. The owner ruled on 2026-10-15 that a
// Bitbucket credential cannot be scoped to one project's repositories, so the
// phase-3 pages then read "Bitbucket stays on the per-repository path" and
// "phase 3's shared project jobs serve GitHub repositories"; the owner ruled on
// 2026-10-27 that Bitbucket repositories run in shared project jobs too (D460),
// and on 2026-11-13 that such a job holds no token at all (D2302), the
// credentials service's Bitbucket proxy serving it and pairing the run to the
// repositories the job's own profile admits (`internal/credsvc`'s `pairShared`,
// `internal/gitproxy/rules.go` behind it).
//
// The pin is written twice over for the same reason T1.1's is: the page an
// operator reads (docs/git-providers.md) and the security record (SECURITY.md)
// have to say the same thing, and a reader who found the model on one and the
// old ruling on the other would take the weaker reading.
//
// What it deliberately does NOT pin is a claim about the tree's own behaviour,
// and that half changed with S7 of docs/shared-jobs-default. Until then
// `internal/infra`'s `sharedJobProviders` admitted `github` alone, so no shared
// job served a Bitbucket profile and both halves — the model and the fact that
// it had not shipped — were pinned, because a page that told a reader the move
// had shipped would be the lie this test exists to prevent. I1 landed the
// one-line change (the list names `bitbucket` beside `github`, and
// `TestSharedJobProviderIsTheProvidersList` pins it), so the "not shipped" half
// is now the false claim: the page has to say the move is what serves a
// Bitbucket profile, and the sentence that said otherwise may not come back.
// The sentences wrap in the files, so the assertions are made against the
// whitespace-normalized text.
func TestDocsStateBitbucketSharedJobIsolation(t *testing.T) {
	providers := strings.Join(strings.Fields(readDoc(t, "docs/git-providers.md")), " ")
	sec := strings.Join(strings.Fields(readDoc(t, "SECURITY.md")), " ")

	// The model, in both documents: the job holds no token, and the proxy
	// serves it by pairing the run to the profile's repositories.
	for _, doc := range []struct {
		path string
		text string
	}{{"docs/git-providers.md", providers}, {"SECURITY.md", sec}} {
		for _, want := range []string{
			"A shared Bitbucket job holds no token at all",
			"the credentials service's Bitbucket proxy",
			"pairs the verified run identity to the repositories the job's own profile admits",
		} {
			if !strings.Contains(doc.text, strings.Join(strings.Fields(want), " ")) {
				t.Errorf("%s does not say %q: D2302 settled that a shared Bitbucket job holds nothing and the proxy serves it, and the page must not read as the project token moving into the job", doc.path, want)
			}
		}
	}

	// docs/git-providers.md is the page that carried the ruling the owner has
	// since moved, so it carries the amendment, the credential that is now the
	// service's, the shape of the refusal a profile that mounts the token meets,
	// and — the half that followed I1 — that the move is what serves a Bitbucket
	// profile: the provider list the two sides read admits Bitbucket beside
	// GitHub, and a Bitbucket repository's launch reaches the shared job.
	for _, want := range []string{
		"**Amended 2026-11-16 (D460, D2302): a shared Bitbucket job holds no token, and the credentials service's Bitbucket proxy serves it.**",
		"no new Fugaro-issued token and no mounted `bitbucket-token`",
		"`projectpolicy.CheckSharedJobProfile`",
		"asked by the deployer and by `fugaro project policy publish`",
		"`internal/infra`'s `sharedJobProviders` admits `bitbucket` beside `github`",
		"a Bitbucket repository's launch reaches a shared job",
		// And the half that keeps the claim honest: a *baked* workflow still
		// keeps a job of its own, for the image rather than for access (D2304's
		// landing condition, S10). A page that said the move had shipped whole
		// would be the overclaim this test exists to prevent.
		"and the workflow is a `clone` one — a `baked` one keeps its own job for the image rather than for access",
	} {
		if !strings.Contains(providers, strings.Join(strings.Fields(want), " ")) {
			t.Errorf("docs/git-providers.md does not say %q: it is the page that carried the 2026-10-15 ruling, and T2.6's amendment plus I1's landed provider list is where a reader learns what replaced it", want)
		}
	}
	// The reason the old ruling gave is gone rather than left standing
	// beside the amendment: "the shared job ... has no Bitbucket credential to
	// carry" is the sentence D2302 replaced.
	if strings.Contains(providers, "the shared job the phase-3 design assumed has no Bitbucket credential to carry") {
		t.Error("docs/git-providers.md still says the phase-3 design's shared job has no Bitbucket credential to carry: D460 put Bitbucket repositories on shared jobs and D2302 said what the job holds — nothing, with the proxy serving it")
	}
	// And the "not shipped" wording is I1's own leftover: the list it names
	// admits Bitbucket on this tree, so neither page may tell a reader the
	// move is still owed. Each spelling this sentence had is listed, because
	// the lie is one clause and it migrates to whichever paragraph survives —
	// and it is asked of both documents rather than of the page alone,
	// because SECURITY.md's own paragraph about the shared job ended with the
	// same clause, which is the misreading the whole-function rule above
	// exists to prevent: a reader who found the model on one page and the old
	// ruling on the other takes the weaker reading.
	for _, doc := range []struct {
		path string
		text string
	}{{"docs/git-providers.md", providers}, {"SECURITY.md", sec}} {
		for _, wrong := range []string{
			"still admits `github` alone",
			"no shared job serves a Bitbucket profile today",
			"every Bitbucket repository is still on the per-repository path",
			"every Bitbucket repository is on the per-repository path until I1 lands",
		} {
			if strings.Contains(doc.text, wrong) {
				t.Errorf("%s still says %q: I1 landed the one-line change, so a shared job serves a Bitbucket profile and the page may not read as if the move were owed", doc.path, wrong)
			}
		}
	}

	// SECURITY.md's half is the isolation stated plainly (F12), in the section
	// a reader meets the proxy in: the boundary is the profile, the runs share
	// the profile's service account while the bucket condition on it is the
	// same whole-prefix one every project job account carries ("Known limits"
	// below says why: a profile separates secrets, not bucket objects), and the
	// flip that would put a real credential in the job is not this model's —
	// there is none to put there.
	for _, want := range []string{
		"**The profile is the boundary, and the job holds no Bitbucket credential at all**",
		"its service account is the profile's, so every run the profile serves shares it, while the condition on that account is the one whole-prefix condition every project job account carries, whatever its profile, because a profile separates secrets and not bucket objects",
		"there is no Bitbucket credential in the job for the flip to take away",
	} {
		if !strings.Contains(sec, strings.Join(strings.Fields(want), " ")) {
			t.Errorf("SECURITY.md does not say %q: F12 is the isolation of a shared Bitbucket job, and D2302 changed what it is", want)
		}
	}
}

// The three Bitbucket token types as docs/git-providers.md states them (T14 of
// docs/bitbucket-token-types/implementation-plan.md, D2350, D2355). SECURITY.md's
// own pins for the same round live in docs_security_test.go's
// TestSecurityMdStatesTheBitbucketGitProxy, where the section they belong to is
// read; the runbook's live in docs_bitbucket_runbook_test.go.
//
// Every sentence pinned here is one the acceptance names: the proxy accepts any
// mix of the three types, the scopes are D470's and the name D472's cited and
// not restated, coverage never widens allow_data_to, the narrowest covering
// token serves each request and one run can carry several identities, a failing
// token is marked and shown with the reach of each type, a run can read which
// repositories each token reaches, and the Premium and 25-token facts only as
// far as they are verified (U8).
const bitbucketTokenTypesSection = "#### Any mix of the three token types, served one request at a time"

// TestDocsStateTheBitbucketTokenTypesAndReach: docs/git-providers.md is the page
// an operator reads before they store a typed token, so it has to say what the
// proxy accepts, what a token of each type reaches, what a failure costs and
// what is verified about the limits.
//
// Mutations (verified, run and restored): the coverage paragraph's "A repository
// the allow-list does not admit is not served because some token happens to
// cover it (D2354)." deleted -> FAIL on that pin alone, which is the sentence
// that keeps a reader from taking a token's coverage for a permission; the
// limit paragraph's "**What a lower plan may create is UNVERIFIED**" cut back
// to the Premium sentence alone -> FAIL, which is U8's own rule -- the page may
// state the Premium fact and the 25-token limit and nothing about the lower
// plans it has not verified; the mark paragraph's name-and-clearing sentence
// deleted -> FAIL, and that is the shape a later edit drifts into, because the
// other order of the same facts reads as if storing a fresh token under a
// broken name brings it back; and, from the review of this round, the
// not-landed sentence's "and **no non-test code on this release writes it into
// the published node**" softened to "and it is derived by the plan's
// publisher" -> FAIL on that pin alone, which is the one saying the documented
// flow is an order to use once T12 lands and not a description of what a store
// does on this tree.
//
// Mutation (verified, run and restored), from the second review, which blocked
// the pull request once T12 had landed: the two paragraphs put back as the
// not-landed reading -- "**the publisher, which is not on this tree**", "a
// typed token stored today is in the bundle and in your config and **serves no
// request**" and "**that line is owed with T12**" -- fails this on the pins
// that replace them (the stage that writes the key, the token that serves
// nothing until the update after the store, and the release notes' line). The
// ordering pin -- "a repository token before a project token before a workspace
// token", its own entry rather than a tail of the reach sentence -- fails
// together with the reach pin when that phrase is reordered: the ranking is one
// phrase in two sentences, and the entry is what holds it when the reach
// sentence around it is rewritten.
func TestDocsStateTheBitbucketTokenTypesAndReach(t *testing.T) {
	page := flattenDoc(readDoc(t, "docs/git-providers.md"))
	if !strings.Contains(page, bitbucketTokenTypesSection) {
		t.Fatalf("docs/git-providers.md has no `%s` section: the three token types are an operator's own choice of credential, and the page they read to store one is where they are stated", bitbucketTokenTypesSection)
	}
	section := flattenDoc(docSection(t, "docs/git-providers.md", page, bitbucketTokenTypesSection))
	for _, want := range []string{
		// Any mix, and the narrowest covering one per request (D2350, D2355).
		"**The credentials service's Bitbucket proxy accepts any mix of the three token types — repository, project and workspace — and several of them at once** (D2350, D2355",
		"every request is served by the **narrowest token that covers the repository it names**",
		// The command, the name family, and the check that writes nothing.
		"**Store one with `fugaro creds set bitbucket-token--<name>`.**",
		"a token named `ci` never collides with the model account `ci`",
		"**checks the token against Bitbucket before it writes anything**",
		"the write scopes are reported as **not verified** and the first real use is what tries them",
		// D470 and D472 cited and not restated (R10): the page above carries
		// the scope list and the naming advice, and this section must point at
		// it rather than write a second copy that can drift from it.
		"The scopes to tick are **D470's** and the token's name is **D472's**: the page above publishes that list and that advice, and this section cites both rather than writing a second list of them.",
		// The reach of each type, and the rule that the narrowest is also the
		// smallest reach.
		"A **repository** token reaches the one repository it was created for.",
		"A **project** token reaches every repository of its Bitbucket project",
		"A **workspace** token reaches every repository of its workspace, present and future.",
		"the **narrowest covering token** is also the smallest reach: a repository token before a project token before a workspace token",
		// The ordering on its own, pinned apart from the sentence above: which
		// token serves a request that several of them cover is the rule an
		// operator picks a type by, and the ranking is the whole of it.
		"a repository token before a project token before a workspace token",
		"a request for a repository none of them covers is refused **naming the repository**, never answered by a wider token that does not admit it.",
		// One run, several identities, and the one-token path left as it was.
		"Because the token is chosen per request, one run can fetch and push one repository through a repository token and another through a project token.",
		"carry **more than one identity** (D472)",
		"**The one-token path above keeps its one author** for the repositories that token serves, because it is one token",
		// Coverage never widens the allow-list (D2354).
		"**Coverage never widens the allow-list.**",
		"`project_secrets.bitbucket-token.allow_data_to` (the operator's own list, on the node as `allow_repos`) is still what decides which jobs may reach Bitbucket through the service at all",
		"A repository the allow-list does not admit is not served because some token happens to cover it (D2354).",
		// A failing token is marked, the request says which one, and the mark
		// is a limit of this release rather than something a command clears.
		"A 401 that a read of a repository the token covers confirms marks **that one token** broken; a 429 marks it throttled until `Retry-After`; a 403 or a 404 marks nothing.",
		"The next request is served by the narrowest **usable** token, which can be a wider one",
		"the service's own audit line carries the token that served the call (`credential`)",
		"(`status`: `broken` or `exhausted`)",
		"**Storing a working token under a name a failure already marked broken does not put that name back in service**",
		// What the node publishes, and the skew the new top-level key brings to
		// a CLI of the release behind (R5).
		"`fugaro update` publishes the node's `bitbucket_tokens` key: each token's **name, type and coverage patterns**",
		"never a value and never a secret version, so every run of the repository can read which repositories each token reaches",
		"a **CLI of the previous release refuses an unknown top-level key**",
		// What an operator runs about the skew, cited where the page states it,
		// and the row that says what the publish does to a run already in
		// flight (T12). The step is the release that carries the publisher's
		// (`### For operators`, v0.26.0); v0.25.0's notes carry the same rule
		// as a known limit, because that release's own `fugaro update` cannot
		// write the key -- the review found the section attributing the step to
		// v0.25.0's `### For operators` section, which does not carry it.
		"**upgrade the CLI before the `fugaro update` that publishes a typed Bitbucket token's coverage**",
		"it is the `For operators` step of the release that carries the publisher ([releases/v0.26.0.md](releases/v0.26.0.md))",
		"the same rule is v0.25.0's, recorded under that release's `Known limits` heading ([releases/v0.25.0.md](releases/v0.25.0.md))",
		"[`docs/design/update-in-flight.md:62`](design/update-in-flight.md), that page's `bitbucket_tokens` half",
		// U8's line: the verified facts, and the unverified one named as
		// unverified rather than answered either way.
		"a **project** or **workspace** access token is a Bitbucket **Premium** feature",
		"Bitbucket allows **25 access tokens per repository, per project and per workspace**, and that limit cannot be raised.",
		"**What a lower plan may create is UNVERIFIED**",
		"**nothing here has been run against Bitbucket with a project or workspace token**",
		// The order an operator has to take, because the coverage reaches a
		// run only through the published policy: the first update moves the
		// service, the store writes the record, the second update publishes the
		// coverage (T12, which is in this release -- the review that blocked
		// the pull request found the page still saying the publisher was not on
		// the tree, and the second-review pins below are the landed reading).
		"**And one thing makes a typed token serve any request: the publisher, and the publisher is `fugaro update`.**",
		"`projectpolicy.DeriveBitbucketTokens` derives the node's `bitbucket_tokens` key from those records and from the identities the state store holds for them",
		"(`buildProjectPolicy`, `internal/cli/init_policy_publish.go`; `fugaro project policy publish` is the same write from `internal/cli/project_cmd.go`)",
		"**The order the feature is used in is `fugaro update`, then the store, then `fugaro update` again**",
		"the first update is what puts a service able to read the token family in place, and the second is what publishes the coverage",
		"A typed token stored between the two is in the bundle and in your config and **serves no request** until that second update",
		"a project that records no typed token derives no key at all, so an installation holding the legacy `bitbucket-token` alone converges with no step and no read of the store (R3)",
		// The published-node paragraph, now the present tense of what the
		// update writes.
		"**Publishing the coverage, and the skew it brings.**",
	} {
		if !strings.Contains(section, strings.Join(strings.Fields(want), " ")) {
			t.Errorf("the `%s` section of docs/git-providers.md does not say %q: an operator stores a Bitbucket token from this page, and every fact here is one they act on before they do", bitbucketTokenTypesSection, want)
		}
	}
}
