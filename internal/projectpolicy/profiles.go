package projectpolicy

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
)

// DefaultProfileName is the profile a workflow that names none runs on
// (design project-centric §4.1): config.DefaultJobProfile, named here as a
// reader of a policy reads it.
const DefaultProfileName = config.DefaultJobProfile

// Profile is one project job's shape (design §4.1): the unit shared by every
// repository admitted to it. Cloud Run cannot vary the image, the service
// account, cpu, memory or the task timeout per execution, so a profile names
// them once and one job and one job service account is deployed per (profile,
// provider, account) — `fugaro update`, from the local config's own profiles.
//
// It embeds config.JobProfile rather than respelling its fields, the way
// Provider embeds config.ModelProvider: the operator's config file declares
// the same document (localcfg.Config.Profiles), and a second declaration
// would drift — a key the file sets and the node does not, or the other way.
// The embedded carrier's json tags are what the node decodes.
type Profile struct{ config.JobProfile }

// ValidProfileName checks a profile name: 1 to 40 of a-z, 0-9 and '-',
// starting and ending with a letter or digit (config.ProjectNameRE). It is
// the same rule the operator's config file holds its own profile names to, so
// a name this refuses can never be defined in a policy and a selection naming
// it would be refused at run start instead of where it was written.
func ValidProfileName(name string) error {
	if name == "" {
		return fmt.Errorf("a profile name must not be empty")
	}
	if !config.ProjectNameRE.MatchString(name) {
		return fmt.Errorf("profile name %q must be 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit", name)
	}
	return nil
}

// ProfileFor is the policy's profile of that name, with "" meaning the
// default. It fails closed: a policy that does not define the profile a
// workflow selected halts the run rather than running it on anything else
// (§5(3), the Task 1.3 path). The error lists the profiles the policy does
// define, so an operator reads one line.
//
// Three callers apply it. The runner calls it at run start (internal/runner's
// checkJobProfile), which is the plan's fail-closed half: a workflow naming a
// profile the published policy does not define halts the run. The job layout
// calls it where the job a run is launched as is built, which is where the
// profile's cpu, memory, timeout and secrets are applied (internal/infra's
// ProjectJobs, the function `fugaro update` deploys one job per (profile,
// provider, account) from). And the launcher calls it to compute that job's
// own name (internal/cli's launchProjectJobFor) — the name the runner matches
// CLOUD_RUN_JOB against, so the two sides resolve the same profile.
func (p *Policy) ProfileFor(name string) (Profile, error) {
	if name == "" {
		name = DefaultProfileName
	}
	if pr, ok := p.Profiles[name]; ok {
		return pr, nil
	}
	defined := p.ProfileNames()
	if len(defined) == 0 {
		return Profile{}, fmt.Errorf("projectpolicy: the policy defines no job profile, so %q cannot run: the default is %s", name, DefaultProfileName)
	}
	return Profile{}, fmt.Errorf("projectpolicy: the policy does not define the job profile %q; it defines %s (the default is %s)",
		name, strings.Join(defined, ", "), DefaultProfileName)
}

// ProfileAllowRepos is the profile's own allow_repos — the project's when the
// profile names none — and **it is not a gate on its own**. A profile's list
// may be wider than the project's spell for spell (the owner writes a `*` on
// one profile and enrolls three repositories in the project), and the design's
// rule is that such a list "enrolls nothing extra": the run is held to the
// project's allow_repos *and* the profile's (§4.3, §4.1). Only Admits holds
// both gates, and it is the only thing a caller deciding whether a repository
// may run may use — reading this list alone would admit every repository the
// project denies while the profile's own list is the wider of the two.
//
// The name says what it returns for that reason: it used to be called
// Admission, which read like "the admission this profile has" and was the
// fail-open a review of this block found (Admits was right, the accessor under
// that name was a trap for the next caller). An empty result admits nothing —
// never everything — so a profile that lists no repository and a project that
// lists none are both deny-all (design §4.3).
//
// It answers one question a caller may legitimately ask: what does this
// profile name, for a message or a listing. Whether a repository is admitted
// is Admits'.
func (p *Policy) ProfileAllowRepos(profile string) ([]string, error) {
	pr, err := p.ProfileFor(profile)
	if err != nil {
		return nil, err
	}
	if len(pr.AllowRepos) > 0 {
		return pr.AllowRepos, nil
	}
	return p.AllowRepos, nil
}

// Admits reports whether a canonical repository (Canonical) may run on that
// profile, and is the check a runner makes before it clones or mints a token
// (design §4.1). Three gates, all of which must open:
//
//   - the profile's provider: one job serves one provider (the provider is part
//     of the job's slug and the git credential it holds), so a repository of
//     another provider is never admitted to it, however the lists below are
//     spelled. Without this gate a "*/acme/*" pattern — which admits every
//     provider's repositories by design — or a project allow_repos that
//     enrolled a repository of another provider would let a github job clone a
//     bitbucket repository;
//   - the project's allow_repos: "there is never a state in which a shared job
//     serves a repository that has not been allowed" (§4.3). A profile's list
//     can only narrow the enrollment, never widen it, however it is spelled;
//   - the profile's own allow_repos, when it names one (§4.1): a secret that
//     must stay away from some repositories lives on a profile they are not
//     allowed on, which is the whole meaning of a profile's list.
//
// An undefined profile is an error, not a refusal: the caller reports it as
// the fail-closed halt it is (ProfileFor), rather than as an unenrolled
// repository. A repository of another provider is a refusal, not an error: it
// is not admitted to this profile, and a profile is one of several the same
// repository may be run on.
//
// Admits is the only gate: ProfileAllowRepos answers "what does this profile
// name", and reading it as the verdict admits every repository the project's
// list denies while the profile's list is the wider one.
//
// Admits has one caller outside tests, and it is the one that counts:
// AdmissionRefusal, which the run's own admission check makes on a project job
// — internal/runner's admissionCheck, before any git credential exists and
// before the lock — so a repository this profile does not admit never runs on
// its job. A run in a per-repository job is held to the project's allow_repos
// alone, because the job it is in carries no selected profile: that is a baked
// workflow, or a policy that defines no profiles, and internal/cli's
// run_allowlist.go is the launcher's advisory twin of that one gate — while
// the launcher's twin of *this* gate is profileAdmissionRefusal in the same
// file, called where the launcher decides which job a launch runs on
// (launchProjectJobFor), which is where a launch's profile is known.
// ProfileAllowRepos answers the narrower question a message or a listing asks
// ("what does this profile name"), never the verdict. A profile's list is
// validated strictly where it is published (validProfiles) because this is the
// gate it becomes.
func (p *Policy) Admits(profile, canonical string) (bool, error) {
	pr, err := p.ProfileFor(profile)
	if err != nil {
		return false, err
	}
	if providerOf(canonical) != pr.Provider {
		return false, nil
	}
	list := p.AllowRepos
	if len(pr.AllowRepos) > 0 {
		list = pr.AllowRepos
	}
	return AdmitRepo(p.AllowRepos, canonical) && AdmitRepo(list, canonical), nil
}

// AdmissionRefusal is the sentence refusing a repository on a profile, and ""
// when the profile admits it. It is the profile half of the admission the
// runner makes on a project job (design §4.1, plan task 3.3 step 4) and the
// sentence the launcher's advisory twin prints (§4.3), worded once for both:
// the two must refuse the same repository for the same reason, and a second
// wording would be a second rule the moment either changed.
//
// err is ProfileFor's fail-closed error for a profile the policy does not
// define, never a refusal: the caller reports it as the halt it is. The
// refusal itself answers both ways a profile says no, because they need
// different fixes and a reader must be able to tell which happened: the
// repository is of another provider than the profile serves (nothing but a
// different profile can admit it), or it is not on the lists the profile
// admits through (which the owner edits).
//
// The verdict is Admits', and only Admits': this function reads which gate
// closed, never re-decides it.
func (p *Policy) AdmissionRefusal(profile, canonical string) (string, error) {
	ok, err := p.Admits(profile, canonical)
	if err != nil {
		return "", err
	}
	if ok {
		return "", nil
	}
	pr, err := p.ProfileFor(profile)
	if err != nil {
		return "", err
	}
	if got := providerOf(canonical); got != pr.Provider {
		return fmt.Sprintf("the job profile %q serves %s, and %s is a %s repository: one job serves one provider (the provider is part of the job's name and of the git credential the job holds), so this repository is not admitted to that profile however its allow_repos is spelled",
			profile, pr.Provider, canonical, got), nil
	}
	if !AdmitRepo(p.AllowRepos, canonical) {
		return NotAdmitted(p.AllowRepos, canonical), nil
	}
	list, err := p.ProfileAllowRepos(profile)
	if err != nil {
		return "", err
	}
	// The list is the profile's own here, and not empty: the project's list
	// admitted this repository above, and a profile that names no list falls
	// back to the project's (ProfileAllowRepos), which admits it.
	return fmt.Sprintf("the job profile %q does not admit %s: its allow_repos is %s, and a profile's own list can only narrow the project's enrollment, never widen it. The project's owner adds the repository to the profile's list, or removes the profile's list so the project's applies, and publishes the policy again",
		profile, canonical, strings.Join(list, ", ")), nil
}

// providerOf is the provider a canonical repository names: Canonical spells one
// "github/owner/name", so the first segment is the provider ("", never a
// provider a policy could name, when the string is not canonical at all).
func providerOf(canonical string) string {
	head, _, _ := strings.Cut(canonical, "/")
	return head
}

// providerRequired is the refusal a profile that names no provider gets, in
// the one place the sentence lives. validProfiles below is where the rule is
// stated, but DeriveAccounts reaches an empty provider first on the publish
// path it shares with `fugaro update` (internal/cli/project_cmd.go derives the
// accounts block before the document is judged), so a profile naming
// claude_accounts without a provider would otherwise be refused for the wrong
// thing — the account's slug "is not a repository" — and the operator would
// read a sentence about a repository instead of the field they left out. Both
// callers say this one sentence.
func providerRequired(at string) error {
	return fmt.Errorf("%s.provider: a profile must name the provider it serves; there is no default (a Bitbucket profile never shares a project-wide credential)", at)
}

// validProfiles checks the profiles block of a node, entry by entry: the
// default profile must be defined, every name a profile name, every profile
// pin one provider (required, finding 4), its cpu/memory/timeout be a shape
// Cloud Run accepts, and every allow_repos pattern name a repository of that
// one provider — a pattern of another provider could never match and is
// refused here instead of silently admitting nothing. A secret entry must be a
// name (CheckSecretName), never a Claude OAuth token, which no profile may
// mount (isClaudeOAuthSecret), and never a Codex OAuth token, whose name is
// reserved ahead of the harness that would read it (config.CodexOAuthSecret):
// the rule `fugaro update` deploys by, stated here so that the two cannot
// disagree about one profile.
//
// The accounts block is the fourth thing a profile is judged against, and the
// rules that hold a profile listing accounts to it are validProfileAccounts':
// every listed account is an account the policy defines, the profile writes
// its own exact allow_repos, every account covers every repository that list
// admits, and no Anthropic API key is mounted beside them (design
// project-named-oauth-tokens §4.1, D3 and D4).
func validProfiles(profiles map[string]Profile, accounts map[string]Account) error {
	if len(profiles) == 0 {
		// A block that carries nothing defines nothing — not even the profile
		// every workflow defaults to. Leaving the key out is how a policy says
		// it sets no profile at all.
		return fmt.Errorf("projectpolicy: profiles: a profiles block must define %s; leave the block out to set no profile", DefaultProfileName)
	}
	for _, name := range profileMapNames(profiles) {
		pr := profiles[name]
		at := fmt.Sprintf("projectpolicy: profiles.%s", name)
		if err := ValidProfileName(name); err != nil {
			return fmt.Errorf("%s: %w", at, err)
		}
		// The provider is required (finding 4): it is part of the job's
		// identity (one job per profile, provider and account, task 3.2), it
		// decides which repositories the profile may serve, and it is the
		// credential the job holds — a profile without one describes no job.
		if pr.Provider == "" {
			return providerRequired(at)
		}
		// The kinds Fugaro has a git path for (config.Providers) — the same
		// list fugaro.yaml's git.provider and the local config's
		// repos.<repo>.provider are held to. A kind outside it names no job
		// this code could ever deploy.
		if !slices.Contains(config.Providers, pr.Provider) {
			return fmt.Errorf("%s.provider: %q must be one of %s", at, pr.Provider, strings.Join(config.Providers, ", "))
		}
		if issues := gcp.CheckResources(config.Resources{CPU: pr.CPU, Memory: pr.Memory}); len(issues) > 0 {
			msgs := make([]string, len(issues))
			for i, issue := range issues {
				msgs[i] = issue.Field + ": " + issue.Message
			}
			return fmt.Errorf("%s: %s", at, strings.Join(msgs, "; "))
		}
		if err := validTimeoutClass(at, pr.TimeoutClass); err != nil {
			return err
		}
		// A secret entry is a NAME. It is held to config's own rule for one
		// (CheckSecretName: a lower-case name, and never something that looks
		// like a pasted key), because this document is readable by every run
		// token of the job and printed by `policy show` — a value written
		// here would be a value published. Neither refusal below echoes the
		// entry: the thing being refused may be the value itself.
		seen := make(map[string]bool, len(pr.Secrets))
		for i, secret := range pr.Secrets {
			if msg := config.CheckSecretName(secret); msg != "" {
				return fmt.Errorf("%s.secrets[%d]: %s", at, i, msg)
			}
			// A Claude OAuth token is refused here, and this is where the
			// deployer's refusal is too: ProjectJobs runs CheckProfiles over
			// the local config's profiles before it builds any job, so
			// publish, the deployer and both readers of this node hold a
			// profile to ONE rule rather than to a copy of it each. Without
			// it the documents can disagree about one profile: publish copies
			// the operator's profiles verbatim, so a policy can mount the
			// reserved claude-oauth-token and name a job `fugaro update` never
			// deployed — its account segment is the default
			// account on the launcher's and the runner's side and empty on the
			// deployer's. The launcher then reaches Cloud Run on that job and
			// reads a 404, after the claim and the budget token, and the runner
			// matches no candidate of its own and halts every run of the profile
			// with cannotPlaceJob, which blames a stale module version. Like the
			// two refusals above, it never echoes the entry.
			if isClaudeOAuthSecret(secret) {
				return fmt.Errorf("%s.secrets[%d]: a Claude OAuth token is person-owned (design D2), and its allow_data_to is a per-repository list a shared job cannot honour — a profile is shared by every repository admitted to it — so no profile mounts one until phase 5 decides how an account reaches a shared job. `fugaro update` refuses to deploy the job such a profile names, and this node must not name one either", at, i)
			}
			// The Codex harness's OAuth credential is the same KIND of name,
			// reserved ahead of the harness itself (config.CodexOAuthToken,
			// owner ruling of 2026-10-18): Fugaro does not drive Codex, so
			// nothing mints a value for it and `fugaro secrets set` refuses to
			// store one — a profile naming it would mount a container that can
			// never have a version, and would name the credential the harness
			// reads the day it lands. Refused here by the same rule the
			// deployer holds a profile to, rather than by one of its own.
			if config.CodexOAuthSecret(secret) {
				return fmt.Errorf("%s.secrets[%d]: %s and the names starting %s are reserved for the Codex harness's OAuth credential, which Fugaro does not drive yet: no job mounts one, so no profile may name it", at, i, config.CodexOAuthToken, config.CodexOAuthTokenPrefix)
			}
			if seen[secret] {
				return fmt.Errorf("%s.secrets[%d]: a secret is listed twice; list each secret once", at, i)
			}
			seen[secret] = true
		}
		for i, pattern := range pr.AllowRepos {
			if err := ValidPattern(pattern); err != nil {
				return fmt.Errorf("%s.allow_repos[%d]: %w", at, i, err)
			}
			// A pattern of another provider can never match a repository this
			// profile's job is given (task 3.2 mints one job per provider), so
			// it is refused rather than read as "admits nothing": the owner
			// wrote a list they believe admits something.
			if head := patternProvider(pattern); head != "" && head != pr.Provider {
				return fmt.Errorf("%s.allow_repos[%d]: %q names the %s provider, and this profile serves %s; a profile admits only its own provider's repositories",
					at, i, pattern, head, pr.Provider)
			}
		}
		// The Bitbucket token's two profile-side rules, judged here because
		// they read the mount and the profile's own allow_repos — the same
		// list the pattern rule above has just checked, so a refusal about a
		// pattern is about the pattern and not about the token.
		if err := validBitbucketProfile(at, pr); err != nil {
			return err
		}
		// The accounts the profile serves, judged last: validProfileAccounts'
		// subset rule reads the profile's own allow_repos, and this is the
		// point at which that list has been checked — so a refusal about a
		// pattern is about the pattern, and not about what the accounts cover.
		if err := validProfileAccounts(at, pr, accounts); err != nil {
			return err
		}
	}
	if _, ok := profiles[DefaultProfileName]; !ok {
		return fmt.Errorf("projectpolicy: profiles: %s must be defined: it is the profile every workflow that names none runs on", DefaultProfileName)
	}
	return nil
}

// validBitbucketProfile holds a profile that mounts the Bitbucket token to the
// two rules that read the profile alone (T2.1): the mount is a bitbucket
// profile's, and the profile writes its own list, exactly.
//
// The mount is a *shared job's* credential (D473): bitbucket-token is the one
// reserved name a project may store once for a whole workspace or project and
// mount from every repository its entry allow-lists, and the job it lands on
// serves one provider. A github profile mounting it would put a Bitbucket
// credential on a job that clones github repositories and holds the github git
// credential, where nothing could use it — and the mount would name the
// profile as a Bitbucket shared job to a reader of this node
// (SharedBitbucketProfile). It is refused here rather than deployed as a job
// whose second credential is dead weight, and the deployer gets the same rule
// because it holds a profile to these rules through CheckProfiles before it
// builds a job.
//
// The list is the profile's own and it is exact, for the reason
// validProfileAccounts' is: bitbucket_token.allow_repos is what the token may
// reach, and a profile's list is the narrower per-job half of that decision
// (Admits intersects the two). An absent list would fall back to the project's
// wider enrollment, which this token is not bounded by, and a wildcard would
// make the subset rule against the block undecidable — so neither is a state
// this node can express.
func validBitbucketProfile(at string, pr Profile) error {
	i := slices.Index(pr.Secrets, config.BitbucketSecret)
	if i < 0 {
		return nil
	}
	if pr.Provider != "bitbucket" {
		// The sentence is the deployer's own (internal/infra's ProjectJobs
		// refuses `%q is the Bitbucket credential` before it builds any job),
		// worded here for the reason providerRequired's is: the two readers of
		// one profile must not be told different things about it, and the node
		// is the document a policy is published with.
		return fmt.Errorf("%s.secrets[%d]: %q is the Bitbucket credential, and only a bitbucket profile mounts it: this profile serves %s, so its job holds that provider's git credential and could not use the token. Serve bitbucket, or drop the entry",
			at, i, config.BitbucketSecret, pr.Provider)
	}
	if len(pr.AllowRepos) == 0 {
		return fmt.Errorf("%s.secrets[%d]: a profile that mounts %s must write its own allow_repos: an absent list means the project's (%s.allow_repos is not written), which this token is not bounded by. Write the repositories this profile admits, exactly",
			at, i, config.BitbucketSecret, at)
	}
	for j, pattern := range pr.AllowRepos {
		if strings.Contains(pattern, "*") {
			return fmt.Errorf("%s.allow_repos[%d]: %q carries a wildcard, and a profile that mounts %s must write exact repositories: whether this profile admits a repository is decided repository by repository against bitbucket_token.allow_repos, and a wildcard makes that undecidable. Write the repositories themselves",
				at, j, pattern, config.BitbucketSecret)
		}
	}
	return nil
}

// validBitbucketProfileRepos is the other half of that rule, and the half that
// needs the node's whole policy: every repository a profile that mounts the
// token admits is one the bitbucket_token block admits, matched with MatchRepo
// — the one matcher the runner, the launcher and the credentials service use.
// A node that carries no block bounds no repository, so a profile mounting the
// token is refused: the token's reach is the block's (publish derives it from
// project_secrets.bitbucket-token.allow_data_to), never the project's
// allow_repos, which is what makes this a refusal rather than a fallback.
//
// The message names the profile, the repository and the block, and never a
// value: there is none in either document, and the operator's remedy is to
// edit the local config and publish again.
func validBitbucketProfileRepos(profiles map[string]Profile, token *BitbucketToken) error {
	for _, name := range profileMapNames(profiles) {
		pr := profiles[name]
		if !slices.Contains(pr.Secrets, config.BitbucketSecret) {
			continue
		}
		at := "projectpolicy: profiles." + name
		for _, repo := range pr.AllowRepos {
			if token == nil {
				return fmt.Errorf("%s.allow_repos: the profile mounts %s, and this policy carries no bitbucket_token block: that block is what the token may reach (publish derives it from project_secrets.%s.allow_data_to in the project's local config), and the project's own allow_repos is not a list this credential is bounded by. Publish a policy that carries the block, or take the entry off the profile",
					at, config.BitbucketSecret, config.BitbucketSecret)
			}
			if !AdmitRepo(token.AllowRepos, repo) {
				return fmt.Errorf("%s.allow_repos: the profile admits %s, and bitbucket_token.allow_repos is %s, which does not: the token's reach is the block's, and a profile's list can only narrow it. Add the repository to project_secrets.%s.allow_data_to in the project's local config and publish again, or take it off the profile's allow_repos",
					at, repo, strings.Join(token.AllowRepos, ", "), config.BitbucketSecret)
			}
		}
	}
	return nil
}

// isClaudeOAuthSecret reports whether name is a Claude account's OAuth secret:
// the reserved claude-oauth-token (config.ClaudeAccountSecret of the default
// account) or any name in the claude-oauth-token-<account> namespace. The whole
// namespace rather than the names a claude_accounts block happens to define,
// because config.ClaudeOAuthTokenPrefix reserves it for the derived secret IDs
// of accounts: a name merely shaped like one is never a secret of a profile's
// own, and the deployer refuses it by this rule rather than by one of its own
// (internal/infra's ProjectJobs calls CheckProfiles before it builds a job, so
// a profile mounting one never reaches the job it would name).
func isClaudeOAuthSecret(name string) bool {
	return name == config.ClaudeAccountSecret(config.DefaultClaudeAccount) ||
		strings.HasPrefix(name, config.ClaudeOAuthTokenPrefix)
}

// validTimeoutClass checks a profile's task timeout. Cloud Run's own ceiling
// is the authority (gcp.MaxTaskTimeout, 168h) exactly as its cpu and memory
// limits are: a form the API would refuse must be refused where the owner can
// read it.
//
// The figure compared against the ceiling is the one the job is *deployed*
// with, and that is the class plus backend.TaskTimeoutSlack — the job's
// task_timeout_s is the sum (internal/infra/projectjobs.go's TaskTimeoutS,
// and the launch override writes the same sum), so a class at 168h deploys at
// 604920 s against Cloud Run's 604800 and the apply is refused. The bound is
// therefore the ceiling minus the slack, which is what internal/cli's
// parseTotalTimeout takes for the same quantity (R15): a class at the bound
// deploys at exactly the ceiling, and one second more does not.
//
// The spelling is config.ParseDuration's — the one timeouts.total is read with
// in fugaro.yaml, day counts and all — so "14d" is a timeout here because it is
// a timeout there, rather than a form this package refused and that one
// accepted.
func validTimeoutClass(at, class string) error {
	if class == "" {
		return fmt.Errorf("%s.timeout_class: a profile must name its task timeout, for example \"6h\"", at)
	}
	d, err := config.ParseDuration(class)
	if err != nil {
		return fmt.Errorf("%s.timeout_class: %q is not a duration, for example \"90m\", \"6h\" or \"7d\"", at, class)
	}
	if d <= 0 {
		return fmt.Errorf("%s.timeout_class: %q is not a positive duration", at, class)
	}
	if limit := gcp.MaxTaskTimeout - backend.TaskTimeoutSlack; d > limit {
		return fmt.Errorf("%s.timeout_class: %q is longer than a Cloud Run task allows, %s: the job's task timeout is this class plus %s, and Cloud Run's ceiling is %s",
			at, class, limit, backend.TaskTimeoutSlack, gcp.MaxTaskTimeout)
	}
	return nil
}

// patternProvider is the provider a repository pattern names, or "" when the
// pattern names none ("*", which admits every provider's repositories).
func patternProvider(pattern string) string {
	head, _, _ := strings.Cut(pattern, "/")
	if head == "*" {
		return ""
	}
	return head
}

// profileMapNames is a stable order for error messages (a map's range is
// random, so a node with two bad entries would otherwise name a different one
// on each read).
func profileMapNames(m map[string]Profile) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ProfileNames is the policy's profile names, sorted: what a reader lists when
// it reports the profiles a policy defines (the CLI's `project show`) and what
// ProfileFor's refusal names.
func (p *Policy) ProfileNames() []string { return profileMapNames(p.Profiles) }

// sortedProfileNames is a stable order for the strict decode of the profiles
// block (its entries are raw, so a bad entry is reported by name).
func sortedProfileNames(entries map[string]json.RawMessage) []string {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// CheckProfiles is validProfiles for a caller outside this package, which
// needs the same rules and must not carry a second copy of them: infra's
// project-job layout (plan task 3.2) validates the local config's profiles
// with it before it deploys a job, so a profile `fugaro project policy
// publish` would refuse can never be deployed as one nobody can select.
//
// accounts is the policy's accounts block, derived from the same local config
// by the same function publish uses (DeriveAccounts): a caller that passed an
// empty one would make every profile listing an account fail the membership
// rule below, with a sentence about a policy that was never published, on a
// `fugaro update` that has the account in hand.
func CheckProfiles(profiles map[string]Profile, accounts map[string]Account) error {
	return validProfiles(profiles, accounts)
}
