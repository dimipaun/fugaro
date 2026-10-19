package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"gopkg.in/yaml.v3"
)

// The tests below pin the wording this feature's security cost is stated in —
// SECURITY.md's residual for a project job's Claude account token (plan task 3,
// design docs/design/project-named-oauth-tokens.md §6.1, owner ruling D8), and
// the operator guide this feature ships with (plan task 4,
// docs/project-claude-accounts.md). The wording is the feature's deliverable
// here — the carrier is a static mount, so what a reader is told about it is
// the thing a later change must keep true — and the tests hold it the way
// docs_security_test.go and docs_named_oauth_tokens_test.go hold the rest of
// the file: the sentences, tied to the code that makes them facts rather than
// to the text alone.
//
// Task 4's own red step is TestDocsProjectClaudeAccountsGcpSetupRollout below:
// delete the rollout sentence from docs/gcp-setup.md and it fails naming it.

// TestDocsProjectNamedOAuthTokensSecurityMdResidual: SECURITY.md carries all
// five things D8 names — no launcher read, every run of an account's job holds
// its token, a hostile run can use it during its own run, the account is the
// blast radius, and the carrier is a stopgap pending the credentials/security
// service — plus the case the default account adds: a profile that lists
// `default` gives that token to every run of its no-account job, whatever the
// run's own agent.auth is, documented rather than refused.
//
// Mutation (verified, run and restored): delete the "A hostile run can use the
// token during its own run" sentence — this fails naming it. The same holds
// for any of the other sentences below, which is the point: a residual quietly
// dropped from SECURITY.md is a claim the document no longer makes.
func TestDocsProjectNamedOAuthTokensSecurityMdResidual(t *testing.T) {
	data, err := os.ReadFile("../../SECURITY.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		// The bullet and its subject.
		"A project job holds its Claude account's OAuth token, and every run of that job holds it too.",
		"gcp.ProjectJobName",
		"internal/infra/projectjobs.go",
		"claude-oauth-token-<name>",
		"CLAUDE_CODE_OAUTH_TOKEN",
		"it is there from the start of every run of that job and no run can decline it",
		// D8's five, one by one.
		"no launcher reads it",
		"which only the job and build accounts hold",
		"A hostile run can use the token during its own run, and after it",
		"The account is the blast radius, not the repository",
		"This carrier is a stopgap",
		// The blast-radius rule, named where a reader can check it.
		"claude_accounts.<name>.allow_data_to",
		"accounts.<name>.allow_repos",
		"decides **which runs** are given that token",
		"neither of them scopes **what the token can do**",
		// The default account's case: documented, not refused.
		"A profile that lists `default` gives that token to every run of its no-account job, whatever that run's `agent.auth` is",
		"documented rather than refused",
		// Where the D4 pair is actually refused, and the api-key runs that are
		// admitted here all the same: an api-key run whose models go through
		// the gateway mounts no Anthropic key and needs none
		// (agent.BuildEnv's api-key arm), so "never API-key ones" is not this
		// document's to say — and the refusal is not `fugaro validate`'s,
		// which reads no policy node at all.
		"`projectpolicy.validProfileAccounts`",
		"`fugaro validate` reads no policy node and is not one of the three",
		"or api-key runs under the gateway",
		"`agent.BuildEnv`",
		"fugaro doctor",
		// The swap's own destination, so the stopgap names what replaces it.
		"docs/credentials-service/README.md",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("SECURITY.md no longer says %q: design project-named-oauth-tokens.md §6.1 (D8) is what this feature's cost is stated in", want)
		}
	}
	// The residual is a residual, not a claim that the token is safe: the
	// document must not say a run cannot read it, and it must not promise that
	// nothing ever holds it before the service lands (that sentence is the
	// stopgap's, about the swap, and it says "after which").
	for _, banned := range []string{
		"a run cannot read the token",
		"no job holds the token",
		"the token cannot be copied",
	} {
		if strings.Contains(doc, banned) {
			t.Errorf("SECURITY.md says %q, which is not true until the credentials service replaces the mount: the token is in the job's container from the start of every run of it", banned)
		}
	}
}

// TestDocsProjectNamedOAuthTokensAccountJobFacts ties the paragraph's claims to
// the code that decides them, so a change to the behavior — not only to the
// wording — fails this test: the mount's secret name is derived from the
// account (ClaudeAccountSecret), which job holds which account's token is
// ProjectJobTokenAccount's answer (the default account riding the no-account
// job, and no token there for a profile that does not list it), and a job's
// name is a function of the (profile, provider, account) triple and of no
// mount — which is why SECURITY.md's stopgap can say the swap renames no job.
func TestDocsProjectNamedOAuthTokensAccountJobFacts(t *testing.T) {
	if got := config.ClaudeAccountSecret(config.DefaultClaudeAccount); got != config.ClaudeOAuthToken {
		t.Fatalf("ClaudeAccountSecret(%q) = %q, want the unsuffixed %q SECURITY.md's paragraph names",
			config.DefaultClaudeAccount, got, config.ClaudeOAuthToken)
	}
	if got := config.ClaudeAccountSecret("acme-personal"); got != "claude-oauth-token-acme-personal" {
		t.Fatalf("ClaudeAccountSecret(acme-personal) = %q, want the claude-oauth-token-<name> shape SECURITY.md's paragraph names", got)
	}
	// The default account rides the profile's no-account job when the profile
	// lists it, and no job of that profile holds a token when it does not.
	listsDefault := config.JobProfile{ClaudeAccounts: []string{config.DefaultClaudeAccount, "acme-personal"}}
	for _, tc := range []struct {
		name    string
		pr      config.JobProfile
		account string
		want    string
		serves  bool
	}{
		{"the no-account job of a profile listing default", listsDefault, "", config.DefaultClaudeAccount, true},
		{"a named account's own job", listsDefault, "acme-personal", "acme-personal", true},
		{"the no-account job of a profile listing only named accounts", config.JobProfile{ClaudeAccounts: []string{"acme-personal"}}, "", "", false},
		{"an account the profile does not list", listsDefault, "acme-work", "", false},
	} {
		if got, serves := config.ProjectJobTokenAccount(tc.pr, tc.account); got != tc.want || serves != tc.serves {
			t.Errorf("%s: ProjectJobTokenAccount = (%q, %v), want (%q, %v)", tc.name, got, serves, tc.want, tc.serves)
		}
	}
	// A job's name depends on the (profile, provider, account) triple and not
	// on what it mounts: the account job and the no-account job of one profile
	// are different names, which is what makes the mount a thing a job holds
	// rather than a thing a run chooses.
	noAccount := gcp.ProjectJobName("standard", "github", "")
	named := gcp.ProjectJobName("standard", "github", "acme-personal")
	if noAccount == named {
		t.Fatalf("gcp.ProjectJobName gives one name (%q) to a profile's no-account and account jobs: SECURITY.md's paragraph and its stopgap both rest on the account being part of the job's own name", noAccount)
	}
	if !strings.Contains(noAccount, "fugaro-p-standard-github") || !strings.Contains(named, "acme") {
		t.Fatalf("gcp.ProjectJobName(standard, github, ...) = %q / %q, want the profile and the account in the readable part of the name", noAccount, named)
	}
}

// The operator guide's own path: the page plan task 4 writes, kept out of the
// site's hand-kept guide list until 0.9.0 ships.
const projectClaudeAccountsDoc = "docs/project-claude-accounts.md"

// TestDocsProjectClaudeAccountsOperatorGuide: the operator guide says what an
// operator has to be told, in the one place the rest of this repository can
// point at. Every item below is a fact of tasks 1 to 3 rather than a
// preference, and each is named where the operator meets it:
//
//   - what a named account *is* (name, project-wide secret, job, one token per
//     job, the policy list);
//   - how a token is minted (claude setup-token, in the operator's own
//     terminal) and stored project-wide, and that a repository copy does not
//     reach a shared job;
//   - the two declarations (claude_accounts, and the profile's
//     claude_accounts field) and the rules publish enforces (D3's subset rule,
//     exact allow_repos, D4's no API key beside them);
//   - what `fugaro init` deploys, and the version-3 rollout;
//   - how a run selects an account, and how both refusals read;
//   - rotation, revocation, and per-account lists;
//   - the stopgap carrier and the security service that replaces it;
//   - the doctor lines, and where this release stops.
//
// Mutation (verified, run and restored): delete the "A repository copy does not
// reach a project job" sentence — this fails naming it; the same holds for the
// runner's "independently of the launcher that started it" clause and for the
// GitHub limit in §11, each of which is a fact an operator acts on.
func TestDocsProjectClaudeAccountsOperatorGuide(t *testing.T) {
	doc := readDoc(t, projectClaudeAccountsDoc)
	for _, want := range []string{
		// Which release this is, and where the reader is (the two scopes the
		// owner requires on every setup command).
		"**0.9.0, unreleased.**",
		"The released Fugaro is 0.8.6",
		"**per installation**",
		"**per repository checkout**",
		// What a named account is.
		"`claude_accounts.<name>` in the project's local config",
		"one Cloud Run job and one job service account per\n  `(profile, provider, account)`",
		"`accounts.<name>.allow_repos`",
		"no launcher ever reads a token",
		// Minting and storing.
		"claude setup-token",
		"fugaro secrets set claude-oauth-token-<name>",
		"fugaro secrets set claude-oauth-token --project-wide",
		"**A repository copy does not reach a project job.**",
		"stored **project-wide**",
		"fugaro secrets ls --project-wide",
		// The declarations and the rules.
		"profiles.<p>.claude_accounts",
		"allow_data_to",
		"**The subset rule (D3).**",
		// Which way the rule bites: every listed account covers every admitted
		// repository (the intersection), not one of them (the union), and the
		// refusal is the code's own.
		"**intersection, not a union**",
		"`projectpolicy`'s `validProfileAccounts`",
		"**its own exact\n  `allow_repos`**",
		"**No Anthropic API key beside the accounts (D4).**",
		// What init deploys, and the rollout.
		"`fugaro init --repo .`",
		"fugaro project policy publish --delete",
		// Selection.
		"oauth_token: acme-team",
		"fugaro run --token acme-team",
		"fugaro accounts ls",
		"`ACCOUNT` column",
		// The refusals, both doors.
		"no budget token was minted and Cloud Run was not called",
		"The run stops before the agent starts, independently of the launcher",
		// Rotation and revocation.
		`version = "latest"`,
		"gcloud secrets versions disable",
		"Fugaro never deletes a secret or a\nversion",
		// The stopgap and its replacement.
		"projectAccountMount",
		"credentials-service/README.md",
		"replaces this carrier",
		// The doctor lines.
		"`project-account-stored`",
		"`claude-accounts-stored`",
		"`oauth-token-allowed`",
		// Where this release stops: the released CLI, and what a shared job
		// does and does not carry (see
		// TestDocsProjectClaudeAccountsSharedJobLimitsAreTrue for the same
		// claims tied to the code that makes them true).
		"**0.8.6 does not read any of it.**",
		"**A GitHub run on a shared job reaches its provider, and it is still not a per-repository job.**",
		"`FUGARO_GITHUB_APP_ID`",
		"`projectJobGitHubAppID`",
		"`cloneShipped`",
		"#which-runs-work-on-a-shared-job-today",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s no longer says %q: the operator guide is plan task 4's deliverable, and this is a fact of tasks 1 to 3 rather than a preference", projectClaudeAccountsDoc, want)
		}
	}
	// The page states the versions the code states, so a version bump cannot
	// leave it describing another release's node.
	if projectpolicy.Version != 3 {
		t.Fatalf("projectpolicy.Version = %d, and the guide describes version 3", projectpolicy.Version)
	}
	if rel, ok := projectpolicy.VersionRelease(3); !ok || rel != "0.9.0" {
		t.Fatalf("projectpolicy.VersionRelease(3) = (%q, %v), and the guide labels the page 0.9.0", rel, ok)
	}
	if !strings.Contains(doc, "policy version 3") {
		t.Errorf("%s does not name policy version 3, which is the node the release it documents writes", projectClaudeAccountsDoc)
	}
	// The secret names the guide tells an operator to store are the ones
	// config derives, and the one account it says is reserved is.
	// The secret every storing command names is ClaudeAccountSecret's own
	// shape, with the account name left as the page's placeholder: the guide
	// never writes a real name into a command, and the prefix it does write is
	// the one config derives.
	named := config.ClaudeAccountSecret("acme-team")
	if !strings.HasPrefix(named, config.ClaudeOAuthTokenPrefix) || !strings.Contains(doc, config.ClaudeOAuthTokenPrefix+"<name>") {
		t.Errorf("%s does not show %q, the shape ClaudeAccountSecret derives (%q for acme-team)",
			projectClaudeAccountsDoc, config.ClaudeOAuthTokenPrefix+"<name>", named)
	}
	if got := config.ClaudeAccountSecret(config.DefaultClaudeAccount); got != config.ClaudeOAuthToken {
		t.Errorf("ClaudeAccountSecret(default) = %q, and the guide calls the default account's token the unsuffixed %q", got, config.ClaudeOAuthToken)
	}
	if config.DefaultClaudeAccount != "default" || !strings.Contains(doc, "`default`") {
		t.Errorf("the guide's `default` account is not config.DefaultClaudeAccount (%q)", config.DefaultClaudeAccount)
	}
	// One job per (profile, provider, account) is ProjectJobAccounts' own
	// answer, the no-account job included: the guide's count is not a second
	// copy of the rule.
	if got := len(config.ProjectJobAccounts(config.JobProfile{ClaudeAccounts: []string{"default", "acme-team"}})); got != 2 {
		t.Errorf("ProjectJobAccounts(default, acme-team) has %d entries, and the guide describes one job per entry (the default account riding the no-account job)", got)
	}
	// And the three doctor check ids the guide names are the ones the checks
	// carry, read from the source that writes them rather than from the guide.
	doctor := readDoc(t, "internal/cli/doctor.go")
	for _, id := range []string{"project-account-stored", "claude-accounts-stored", "oauth-token-allowed"} {
		if !strings.Contains(doctor, id) {
			t.Errorf("the guide names the doctor check %q, and internal/cli/doctor.go no longer carries it", id)
		}
	}
}

// TestDocsProjectClaudeAccountsCommandsExist keeps every command line and flag
// the guide shows real, the way TestDocsProjectLayerCommandsExist does for
// docs/project-layer.md: a command must resolve in the cobra tree and a flag
// must belong to it. The guide is what an operator copies into a terminal, so
// a renamed subcommand or flag fails here rather than at their prompt.
//
// It reads the fenced shell blocks too, not only the inline spans: this page is
// mostly commands, and a command in a block is the copy-pasteable one.
//
// Mutation (verified, run and restored): change the guide's `fugaro run --token
// acme-team` to `--account acme-team` — this fails naming the flag. The same
// holds for a renamed subcommand (`fugaro accounts ls` → `fugaro account ls`).
func TestDocsProjectClaudeAccountsCommandsExist(t *testing.T) {
	doc := readDoc(t, projectClaudeAccountsDoc)
	root := NewRootCmd()
	wordRE := regexp.MustCompile(`^[a-z][a-z-]*$`)
	flagRE := regexp.MustCompile(`--([a-z][a-z-]*)`)

	// The command lines: an inline code span that opens with `fugaro `, and a
	// line of a shell block that is one — a block line may hold several
	// (`brew … && fugaro upgrade`), and a trailing comment is not a command.
	var cmds []string
	for _, m := range regexp.MustCompile("`(fugaro [^`]*)`").FindAllStringSubmatch(doc, -1) {
		cmds = append(cmds, m[1])
	}
	for _, line := range strings.Split(doc, "\n") {
		for _, part := range strings.Split(line, "&&") {
			part = strings.TrimSpace(part)
			if !strings.HasPrefix(part, "fugaro ") {
				continue
			}
			if i := strings.Index(part, " #"); i >= 0 {
				part = part[:i]
			}
			cmds = append(cmds, strings.TrimSpace(part))
		}
	}
	if len(cmds) == 0 {
		t.Fatalf("%s shows no fugaro command line at all, and this test's whole job is to keep the ones it shows real", projectClaudeAccountsDoc)
	}
	for _, text := range cmds {
		// The longest leading run of command-shaped words that actually
		// resolves: a secret NAME is spelled like a subcommand
		// (claude-oauth-token is all lower case and hyphens), so
		// `fugaro secrets set claude-oauth-token` would otherwise be read as a
		// three-word path that resolves to nothing.
		words := strings.Fields(strings.TrimPrefix(text, "fugaro "))
		var path []string
		cmd := root
		for _, f := range words {
			if !wordRE.MatchString(f) {
				break
			}
			next, _, err := root.Find(append(append([]string{}, path...), f))
			if err != nil || next.Name() != f {
				break
			}
			path, cmd = append(path, f), next
		}
		if len(path) == 0 {
			t.Errorf("%s shows `%s`, whose first word is not a command", projectClaudeAccountsDoc, text)
			continue
		}
		for _, fm := range flagRE.FindAllStringSubmatch(text, -1) {
			if !cmdHasFlag(cmd, fm[1]) {
				t.Errorf("%s's `fugaro %s` names --%s, which that command has no such flag", projectClaudeAccountsDoc, strings.Join(path, " "), fm[1])
			}
		}
	}
}

// TestDocsProjectClaudeAccountsNoSecretShapes: the guide tells an operator how
// to store a token, so it is the one document where a pasted value would be
// most at home — and it must carry none. Every command on the page is a
// placeholder (`claude-oauth-token-<name>`), and no string shaped like a
// credential appears anywhere in it. Mutation (verified, run and restored):
// paste a token-shaped literal into the storing step, and this fails naming the
// prefix it matched.
func TestDocsProjectClaudeAccountsNoSecretShapes(t *testing.T) {
	doc := readDoc(t, projectClaudeAccountsDoc)
	for _, key := range []string{"sk-or-", "sk-ant-", "sk-"} {
		if strings.Contains(doc, key) {
			t.Errorf("%s looks like it carries a real value (%q): the page's commands are placeholders, and the value goes in at a prompt", projectClaudeAccountsDoc, key)
		}
	}
	// The placeholder shape itself is what makes the commands copy-pasteable
	// without a value, so it is required rather than merely allowed.
	for _, want := range []string{"claude-oauth-token-<name>", "<token>"} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s does not use the placeholder %s, and its storing step is a command an operator runs as written", projectClaudeAccountsDoc, want)
		}
	}
}

// TestDocsProjectClaudeAccountsGcpSetupRollout is plan task 4's own red step:
// docs/gcp-setup.md is the cloud runbook an operator follows, and the rollout
// of the account axis is a step of it — a 0.9.0 CLI publishes a node an older
// runner refuses, so the upgrade order is what an operator who skipped this
// would otherwise find out at a failed run. The section also carries the two
// other things task 4 lists for this file: the profile field, and the
// project-wide storage step.
//
// Mutation (verified, run and restored): delete the "Republish the policy, per
// installation" step from the section — this fails naming it. The same holds
// for the profile field's sentence and the project-wide storage step.
func TestDocsProjectClaudeAccountsGcpSetupRollout(t *testing.T) {
	doc := readDoc(t, "docs/gcp-setup.md")
	for _, want := range []string{
		"## Claude accounts a project job serves (0.9.0)",
		"**It is not in the released CLI — 0.8.6 does not read any of it**",
		"(project-claude-accounts.md)",
		// The profile field.
		"`profiles.<profile>.claude_accounts` in the project's local config",
		"One job per (profile, provider, account) is what `fugaro init` deploys.",
		// The project-wide storage step.
		"**The project-wide storage step.**",
		"the job mounts the **project-wide** copy",
		"fugaro secrets set claude-oauth-token-<name>",
		"only when `project_secrets.claude-oauth-token` allow-lists at least one repository",
		// The Version 3 rollout, in order.
		"**The Version 3 rollout.**",
		"brew upgrade dimipaun/tap/fugaro && fugaro upgrade",
		"fugaro project policy publish --delete",
		"fugaro image refresh",
		"a project job runs the installation's base image, which can lag the CLI that published the policy",
		// And the one-line pointer in the optional pieces.
		"**Named Claude accounts on a shared job (0.9.0):**",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/gcp-setup.md no longer says %q: the version-3 rollout, the profile field and the project-wide storage step are plan task 4's own additions to the operator runbook", want)
		}
	}
	// The runbook must not still send an operator to the repository copy for a
	// token a shared job mounts: the default account's scope is stated with the
	// condition the CLI actually applies (projectWideAccepted).
	if strings.Contains(doc, "the only scope that name has by default") {
		t.Errorf("docs/gcp-setup.md still says claude-oauth-token's repository copy is \"the only scope that name has by default\", and the CLI stores that name project-wide whenever project_secrets lists it (internal/cli/secrets.go's projectWideAccepted)")
	}
}

// TestDocsProjectClaudeAccountsSharedJobLimitsAreTrue: the guide's §11 limits,
// and the two other pages that state the same thing about a shared job, say
// what the code does — checked here rather than trusted, because §11 is where
// an operator decides whether a repository can move onto a shared job at all.
//
// The claim this test was written for: the guide said a project job's
// environment "carries no GitHub App ID", so a GitHub run on a shared job
// "stops at the provider". That was true when projectJobEnv was the whole of a
// project job's environment, and it stopped being true when the owner's ruling
// of 2026-10-17 (plan task 3.5 step 4) added the App's ID to every github
// project job — projectJobGitHubAppID resolves it from the installation's local
// config and ProjectJobs sets providers.EnvGitHubAppID, which is exactly the
// variable internal/gitprov/providers' github arm refuses to build without.
// docs/project-layer.md and docs/git-providers.md carried the same stale
// sentence, and a reader sent to either for the detail would have been told the
// opposite of the guide.
//
// What is *still* true is the other half, and it is pinned too: a shared job
// carries no FUGARO_CHECKOUT, so the run clones in full and installs no mise
// tools, and it carries none of the per-repository settings
// docs/project-layer.md's table lists. And no live check has been run for a
// shared job end to end (the plan's own, task 3.7), so the guide must not claim
// one has.
//
// Mutation (verified, run and restored): put "carries no GitHub App ID" back
// into the guide's §11 — this fails naming it. Flipping cloneShipped to false
// fails it too, which is the other half: the pages say a clone workflow reaches
// a shared job in this release, and the constant is what makes that true.
func TestDocsProjectClaudeAccountsSharedJobLimitsAreTrue(t *testing.T) {
	guide := readDoc(t, projectClaudeAccountsDoc)
	layer := readDoc(t, "docs/project-layer.md")
	providers := readDoc(t, "docs/git-providers.md")
	// The stale sentence, in the three shapes it was written in, and the
	// "skipped" doctor line the check never prints.
	for _, tc := range []struct{ path, doc, banned string }{
		{projectClaudeAccountsDoc, guide, "carries no GitHub App ID"},
		{projectClaudeAccountsDoc, guide, "stops at the provider"},
		{projectClaudeAccountsDoc, guide, "reports `skipped`"},
		{"docs/project-layer.md", layer, "GitHub repositories stop at the provider"},
		{"docs/project-layer.md", layer, "None, end to end, in this release"},
		{"docs/git-providers.md", providers, "It is not in the tree yet"},
	} {
		if strings.Contains(tc.doc, tc.banned) {
			t.Errorf("%s still says %q: a job of a github profile carries the App's ID in its environment (internal/infra/projectjobs.go's projectJobGitHubAppID sets providers.EnvGitHubAppID on every one of them), and doctor prints no line at all when the installation serves no account job", tc.path, tc.banned)
		}
	}
	// Each page states the true half, and names the symbol an operator can read
	// it out of.
	for _, tc := range []struct {
		path string
		doc  string
		want []string
	}{
		{projectClaudeAccountsDoc, guide, []string{
			"**A GitHub run on a shared job reaches its provider, and it is still not a per-repository job.**",
			"`FUGARO_GITHUB_APP_ID`",
			"`projectJobGitHubAppID`",
			"was not in the released\n  0.8.x, where a clone launch is refused",
			"no live check has proved one end to end on a shared",
			"the check that would (the plan's own, `docs/plans/2026-10-14-project-centric.md` Task 3.7)\n  has not been run",
			// The other half: what the job does not carry.
			"no `FUGARO_CHECKOUT` (a full clone rather than a blobless one, and no `mise install`)",
			"only the profile's project-wide ones",
		}},
		{"docs/project-layer.md", layer, []string{
			"**The provider stop is gone (`projectJobGitHubAppID`).**",
			"`FUGARO_GITHUB_APP_ID`",
			"internal/infra/projectjobs.go",
		}},
		{"docs/git-providers.md", providers, []string{
			"`projectJobGitHubAppID`",
			"is refused at `fugaro init` rather than deployed as a job whose every run would halt when it opened its provider",
		}},
	} {
		for _, want := range tc.want {
			if !strings.Contains(tc.doc, want) {
				t.Errorf("%s does not say %q: §11 is where an operator decides whether a repository can move onto a shared job", tc.path, want)
			}
		}
	}
	// The code ties. The App's ID is set by ProjectJobs, from a function that
	// resolves it out of the local config, and the pages may only say so while
	// both are there.
	jobs := readDoc(t, "internal/infra/projectjobs.go")
	for _, want := range []string{"func projectJobGitHubAppID(", "providers.EnvGitHubAppID", "env[providers.EnvGitHubAppID] = appID"} {
		if !strings.Contains(jobs, want) {
			t.Errorf("docs/project-claude-accounts.md tells an operator a shared job carries the App's ID, and internal/infra/projectjobs.go no longer has %q", want)
		}
	}
	// The provider is built from it and refuses without it: that refusal is why
	// the ID's absence was a stop, and why its presence is not.
	gp := readDoc(t, "internal/gitprov/providers/providers.go")
	if !strings.Contains(gp, "EnvGitHubAppID") || !strings.Contains(gp, "the github provider needs the GitHub App's ID") {
		t.Errorf("the pages say the github provider is built from the App's ID (providers.EnvGitHubAppID), and internal/gitprov/providers/providers.go no longer refuses without it")
	}
	// The other half of the same sentence: the pages say a shared job carries
	// no FUGARO_CHECKOUT, so the run clones in full and installs no mise tools
	// (internal/runner's cloneJob, which is false without it). If a project job
	// ever sets it, that is a real change to what a shared job does and both
	// pages have to be rewritten with it.
	if strings.Contains(jobs, "runner.CheckoutEnv") {
		t.Errorf("docs/project-claude-accounts.md and docs/project-layer.md say a shared job carries no FUGARO_CHECKOUT, and internal/infra/projectjobs.go now sets runner.CheckoutEnv on one: what the run does at bootstrap changed with it")
	}
	// And `checkout: clone` really is shipped in this release, which is what
	// makes the pages' "reaches a shared job" a statement about this tree.
	if !cloneShipped {
		t.Errorf("docs/project-claude-accounts.md and docs/project-layer.md say a checkout: clone launch reaches a shared job in this release, and cloneShipped is false")
	}
	for _, want := range []string{"cloneShipped", "cloneSince"} {
		if !strings.Contains(readDoc(t, "internal/cli/recipes_skew.go"), want) {
			t.Errorf("the pages name %s for the clone gate, and internal/cli/recipes_skew.go no longer carries it", want)
		}
	}
}

// TestDocsProjectClaudeAccountsStayOutOfSiteNav: the page documents a release
// that is not cut, and the site's hub is the public list of guides. A new
// docs/*.md file is not published by itself — website/content/*.md are the
// generated documents and GUIDES in website/src/pages/docs.astro is the only
// hand-kept list — so keeping the guide off the site is a fact of that list,
// and it is what this test holds. When 0.9.0 ships, the entry and the page's
// own "unreleased" label move together, in the change that cuts the release.
//
// Mutation (verified, run and restored): add a `project-claude-accounts.md`
// entry to GUIDES — this fails naming it.
func TestDocsProjectClaudeAccountsStayOutOfSiteNav(t *testing.T) {
	page := readDoc(t, "website/src/pages/docs.astro")
	if strings.Contains(page, "project-claude-accounts") {
		t.Errorf("website/src/pages/docs.astro lists %s: the page documents 0.9.0, which is not released, and the hub is the public list of guides", projectClaudeAccountsDoc)
	}
	doc := readDoc(t, projectClaudeAccountsDoc)
	for _, want := range []string{
		"kept out\nof the docs hub on the site until the release is cut",
		"**0.9.0, unreleased.**",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s does not say %q: a page that is not in the hub has to say so itself, or a reader takes it for the released CLI's", projectClaudeAccountsDoc, want)
		}
	}
}

// localConfigFragments merges a page's local-config yaml blocks into one
// config, the way a reader following the page end to end would: its blocks are
// edits to one file (each says so above itself), and a block that names none of
// these key-roots — the guide's per-repository fugaro.yaml fragment, say —
// contributes nothing.
func localConfigFragments(t *testing.T, path string) (map[string]projectpolicy.Profile, map[string]config.ClaudeAccount, map[string]config.ProjectSecret) {
	t.Helper()
	blocks := fencedYAML(t, path)
	profiles := map[string]projectpolicy.Profile{}
	accounts := map[string]config.ClaudeAccount{}
	secrets := map[string]config.ProjectSecret{}
	for _, block := range blocks {
		var frag struct {
			ClaudeAccounts map[string]config.ClaudeAccount `yaml:"claude_accounts"`
			ProjectSecrets map[string]config.ProjectSecret `yaml:"project_secrets"`
			Profiles       map[string]config.JobProfile    `yaml:"profiles"`
		}
		if err := yaml.Unmarshal([]byte(block), &frag); err != nil {
			t.Fatalf("%s: a yaml block does not parse: %v\n%s", path, err, block)
		}
		for name, a := range frag.ClaudeAccounts {
			accounts[name] = a
		}
		for name, s := range frag.ProjectSecrets {
			secrets[name] = s
		}
		for name, pr := range frag.Profiles {
			// The three keys the page does not state, supplied here: a profile
			// is held to the resources and the timeout class it deploys with,
			// and the examples' subject is their accounts. Every other value
			// validated below is the document's own text.
			pr.CPU, pr.Memory, pr.TimeoutClass = 2, "4Gi", "6h"
			profiles[name] = projectpolicy.Profile{JobProfile: pr}
		}
	}
	return profiles, accounts, secrets
}

// fencedYAML is a document's fenced ```yaml blocks, verbatim.
func fencedYAML(t *testing.T, path string) []string {
	t.Helper()
	doc := readDoc(t, path)
	var out []string
	for _, m := range regexp.MustCompile("(?s)```yaml\n(.*?)```").FindAllStringSubmatch(doc, -1) {
		out = append(out, m[1])
	}
	if len(out) == 0 {
		t.Fatalf("%s has no yaml block: this test's extraction is looking at the wrong document", path)
	}
	return out
}

// TestDocsProjectClaudeAccountsExamplesValidate holds the guide's canonical
// example to the rule the guide itself states. §3 presents the account block
// and the profile as the operator's two edits, and §4 says publish and
// `fugaro init` refuse a profile that admits a repository one of the accounts
// it lists does not cover (projectpolicy's validProfileAccounts, D3's subset
// rule). An example that cannot pass that rule is a config an operator copies
// and is refused — and refused in a way that points at the wrong repair, since
// the refusal names the profile's allow_repos first and the account's list
// second.
//
// So the page's local-config blocks are merged into ONE config and put through
// the pair internal/infra's ProjectJobs runs before it deploys a job:
// projectpolicy.DeriveAccounts (the accounts block, derived from
// claude_accounts and project_secrets exactly as publish derives it) and
// projectpolicy.CheckProfiles (the profile rules, the subset rule among them).
//
// Mutation (verified, run and restored): narrow the guide's project_secrets
// entry back to ["acme/webapp"] alone — this fails with validProfileAccounts'
// own refusal, naming the profile's github/acme/tools.
func TestDocsProjectClaudeAccountsExamplesValidate(t *testing.T) {
	profiles, accounts, secrets := localConfigFragments(t, projectClaudeAccountsDoc)
	if len(profiles) == 0 || len(accounts) == 0 {
		t.Fatalf("%s's local-config blocks no longer carry both a claude_accounts and a profiles block: this test validates the pair §3 presents as the operator's two edits", projectClaudeAccountsDoc)
	}
	for name, pr := range profiles {
		if len(pr.ClaudeAccounts) > 0 && len(pr.AllowRepos) == 0 {
			// §4's own rule, checked here so the validation below is reached
			// with a list rather than with the "write your own allow_repos"
			// refusal: an example that lists accounts and no repositories is
			// not the example the page describes.
			t.Errorf("%s's profiles.%s lists claude_accounts and writes no allow_repos, which the page's own subset rule refuses", projectClaudeAccountsDoc, name)
		}
	}
	derived, err := projectpolicy.DeriveAccounts(profiles, accounts, secrets)
	if err != nil {
		t.Fatalf("%s's example is refused where publish derives the accounts block: %v", projectClaudeAccountsDoc, err)
	}
	if err := projectpolicy.CheckProfiles(profiles, derived); err != nil {
		t.Fatalf("%s's example is refused by the rules publish and `fugaro init` apply to a profile: %v", projectClaudeAccountsDoc, err)
	}
	// And the claim that makes it pass, one repository at a time: every
	// repository a profile admits is covered by EVERY account it lists — the
	// intersection §4 states, not the union an earlier revision of the page
	// described.
	for name, pr := range profiles {
		for _, acct := range pr.ClaudeAccounts {
			for _, repo := range pr.AllowRepos {
				if !projectpolicy.AdmitRepo(derived[acct].AllowRepos, repo) {
					t.Errorf("%s's profiles.%s admits %s and its account %q lists %v: every account a profile lists must cover every repository it admits",
						projectClaudeAccountsDoc, name, repo, acct, derived[acct].AllowRepos)
				}
			}
		}
	}
}

// TestDocsProjectNamedOAuthTokensPolicyExampleValidates is the same rule over
// the design's §4.1 block, which is the policy's own shape rather than the
// local config's: its accounts carry allow_repos directly, so it is validated
// as a policy — projectpolicy.CheckProfiles over the block's own accounts —
// which is the rule `fugaro project policy publish` applies to a hand-written
// node (decodeAccountBlock and validProfiles). The block is the one the prose
// below it calls the subset rule's shape, so an example that contradicts that
// prose is a design that would refuse the node it just showed.
//
// Mutation (verified, run and restored): put `ops` back to ["github/acme/infra"]
// alone — this fails with validProfileAccounts' refusal, naming
// github/acme/app.
func TestDocsProjectNamedOAuthTokensPolicyExampleValidates(t *testing.T) {
	const path = "docs/design/project-named-oauth-tokens.md"
	var (
		profiles map[string]projectpolicy.Profile
		accounts map[string]projectpolicy.Account
		blocks   int
	)
	for _, block := range fencedYAML(t, path) {
		var frag struct {
			Accounts map[string]struct {
				AllowRepos []string `yaml:"allow_repos"`
			} `yaml:"accounts"`
			Profiles map[string]config.JobProfile `yaml:"profiles"`
		}
		if err := yaml.Unmarshal([]byte(block), &frag); err != nil {
			t.Fatalf("%s: a yaml block does not parse: %v\n%s", path, err, block)
		}
		if len(frag.Accounts) == 0 || len(frag.Profiles) == 0 {
			continue
		}
		blocks++
		accounts = map[string]projectpolicy.Account{}
		profiles = map[string]projectpolicy.Profile{}
		for name, a := range frag.Accounts {
			accounts[name] = projectpolicy.Account{AllowRepos: a.AllowRepos}
		}
		for name, pr := range frag.Profiles {
			pr.CPU, pr.Memory, pr.TimeoutClass = 2, "4Gi", "6h"
			profiles[name] = projectpolicy.Profile{JobProfile: pr}
		}
	}
	if blocks != 1 {
		t.Fatalf("%s carries %d block(s) with both an accounts and a profiles key, and this test validates the one §4.1 presents", path, blocks)
	}
	listing := 0
	for _, pr := range profiles {
		if len(pr.ClaudeAccounts) > 1 {
			listing++
		}
	}
	if listing == 0 {
		t.Fatalf("%s's §4.1 block no longer shows a profile listing several accounts: the subset rule this test exercises is not reachable from it", path)
	}
	if err := projectpolicy.CheckProfiles(profiles, accounts); err != nil {
		t.Fatalf("%s's §4.1 block is refused by the rule the design's own bullet below it states: %v", path, err)
	}
}
