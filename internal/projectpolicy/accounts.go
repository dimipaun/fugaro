package projectpolicy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
)

// Account is one Claude account's entry of the policy's accounts block: which
// repositories its token may be used by (design project-named-oauth-tokens
// §4.1, D3). It is the policy's answer to a question a shared job cannot ask
// any other way: the job's profile is shared by every repository admitted to
// it, so the repositories an account's token may reach must be a fact of the
// node every run of that job reads, not of a per-repository document.
//
// It carries the account's own repositories and nothing else — no secret, no
// value. The token's logical name is a pure function of the account's name
// (config.ClaudeAccountSecret), and the node is readable by every run token of
// the job (§5(2)).
type Account struct {
	// AllowRepos are the canonical repositories (Canonical) whose runs may use
	// this account's token. Never empty, never a wildcard: the list is derived
	// from the account's own allow_data_to in the project's local config, and
	// a wildcard would make the subset rule below undecidable.
	AllowRepos []string `json:"allow_repos"`
}

// accountMembers is the member names an accounts entry is spelled with, read
// from the struct's own json tags the way profileMembers is (carrierMembers),
// so a field this type gains is held to its spelling without a second list.
var accountMembers = carrierMembers(reflect.TypeOf(Account{}))

// decodeAccountBlock reads the accounts block off the node, refusing a key
// inside an entry that this Fugaro does not know, for the reason
// decodeProfileBlock gives for the profiles block: a misspelt key here is not
// a dropped field but an enforced one gone quiet. A misspelt allow_repos
// decodes to the nil slice, and what a run then holds is an account whose list
// of repositories is empty — the gate the job's token is bounded by, absent
// while the node appears to state it. Unknown *top-level* keys keep the P8
// tolerance; a newer field inside an entry bumps Version, which is what
// refuses a node this binary cannot read.
//
// The member names are held to the documented spelling too
// (checkMemberNames' duplicate half, plus spelledMember against
// accountMembers): encoding/json folds case, so a strict decode alone would
// read {"allow_repos": [...], "Allow_Repos": null} as whichever of the two the
// document wrote last.
func decodeAccountBlock(raw json.RawMessage) (map[string]Account, error) {
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("projectpolicy: accounts: %w", err)
	}
	// The block's own keys, duplicates included, before the map above has
	// folded them away: a key written twice is read as the last of the two, so
	// a repeated account is one whose list this reader never saw.
	names, err := objectMemberNames(raw)
	if err != nil {
		return nil, fmt.Errorf("projectpolicy: accounts: %w", err)
	}
	if err := checkNoDuplicateNames("accounts", names); err != nil {
		return nil, fmt.Errorf("projectpolicy: %w", err)
	}
	out := make(map[string]Account, len(entries))
	for _, name := range sortedStringKeys(entries) {
		dec := json.NewDecoder(bytes.NewReader(entries[name]))
		dec.DisallowUnknownFields()
		var acct Account
		if err := dec.Decode(&acct); err != nil {
			return nil, fmt.Errorf("projectpolicy: accounts.%s: %w", name, err)
		}
		members, err := objectMemberNames(entries[name])
		if err != nil {
			return nil, fmt.Errorf("projectpolicy: accounts.%s: %w", name, err)
		}
		if err := checkNoDuplicateNames("accounts."+name, members); err != nil {
			return nil, fmt.Errorf("projectpolicy: %w", err)
		}
		if err := checkWrittenAccountFields(name, entries[name]); err != nil {
			return nil, err
		}
		out[name] = acct
	}
	return out, nil
}

// checkWrittenAccountFields applies the account rule that reads the written
// document rather than the decoded value: the spelling of an entry's members.
// It is decodeAccountBlock's twin of checkWrittenProfileFields, in the same
// shape and for the same reason — a name that differs only in case is matched
// by encoding/json and by nothing else here, so a misspelt allow_repos leaves
// the nil slice, and what a run then holds is an account whose list of
// repositories is empty while the node appears to state it.
//
// An entry this function cannot read as an object is an error, exactly as it is
// in checkWrittenProfileFields, and never a check that quietly does not run: a
// spelling check skipped for the entry it was written for is the check a
// case-folded or misspelt member walks past, which is the whole failure the
// check exists to stop. Through Decode the error is unreachable — the strict
// decode above refuses anything that is not an object first — and it is
// returned anyway, because a check that depends on another one running first is
// a check a later change to that one can silently disable.
func checkWrittenAccountFields(name string, raw json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("projectpolicy: accounts.%s: %w", name, err)
	}
	for _, member := range memberNames(fields) {
		known := spelledMember(accountMembers, member)
		if known == member {
			continue
		}
		if known == "" {
			// Unreachable through Decode: the strict decode above refuses a
			// name no member answers to. Kept so this function holds on its
			// own too.
			return fmt.Errorf("projectpolicy: accounts.%s: unknown field %q", name, member)
		}
		return fmt.Errorf("projectpolicy: accounts.%s.%s: an account entry's members are spelled as the policy spells them — write %s. A name that differs only in case is read by the decoder and not by this document's own checks, so the list it names would be the one the decoder read", name, member, known)
	}
	return nil
}

// validAccounts checks the accounts block, entry by entry: each key an account
// name, and each value a non-empty list of exact, provider-qualified
// repositories.
//
// Every rule here is about the account's list being the *answer* to "may this
// token be used by this repository", and each one is a way of losing that
// answer rather than stating it:
//
//   - an empty list admits nothing, and a block that publishes one is an
//     account a profile cannot legally name (the subset rule) — the honest
//     spelling of a token that may be used nowhere is not listing the account
//     at all;
//   - "*" admits everything the profile admits, at a stroke, which is the
//     opposite of what an account's list is for: it is the per-repository
//     decision (D3), and the derivation cannot even write one (publish
//     qualifies each allow_data_to slug, and no slug carries a wildcard);
//   - an entry that is not a canonical, provider-qualified repository name is
//     a repository nothing matches: MatchRepo compares segment against
//     segment, and a two-segment "acme/app" would name no repository this
//     reader could look up.
func validAccounts(accounts map[string]Account) error {
	for _, name := range sortedStringKeys(accounts) {
		at := fmt.Sprintf("projectpolicy: accounts.%s", name)
		if msg := config.ValidClaudeAccountRef(name); msg != "" {
			return fmt.Errorf("%s: %s", at, msg)
		}
		acct := accounts[name]
		if len(acct.AllowRepos) == 0 {
			return fmt.Errorf("%s.allow_repos: an account must name the repositories its token may be used by, and this list is empty (a written null reads the same way): a profile may serve only repositories its accounts cover, so an account list that admits nothing is one no profile can list. Publish derives it from claude_accounts.%s.allow_data_to in the project's local config — which must be non-empty for the account's project-wide copy to exist at all", at, name)
		}
		seen := map[string]bool{}
		for i, repo := range acct.AllowRepos {
			if err := ValidPattern(repo); err != nil {
				return fmt.Errorf("%s.allow_repos[%d]: %w", at, i, err)
			}
			if strings.Contains(repo, "*") {
				return fmt.Errorf("%s.allow_repos[%d]: %q carries a wildcard, and an account's list is exact: it is the per-repository decision of which runs may use that token (D3), and a wildcard makes the profile-side subset rule undecidable. Write the repositories themselves, or publish the account's list from claude_accounts.%s.allow_data_to", at, i, repo, name)
			}
			if seen[repo] {
				return fmt.Errorf("%s.allow_repos[%d]: %q is listed twice; list each repository once", at, i, repo)
			}
			seen[repo] = true
		}
	}
	return nil
}

// validProfileAccounts holds one profile that lists Claude accounts to the
// rules of D3 and D4. It is the gate's definition (design
// project-named-oauth-tokens §4.1, plan Task 1): everything a run of an
// account's job is bounded by is decided between this profile and the accounts
// block, and every rule below is a way for the pair to disagree.
//
//   - Every listed account is an account the policy defines. An account's
//     policy list is what says which repositories its token may be used by, so
//     a profile naming an account with no entry names a token whose
//     repositories are undecidable — and, on the job side, a job whose
//     account segment the deployer cannot compute. Each account once: a
//     repeated entry is the same account twice, and the two would have to
//     agree.
//   - The subset rule (D3): the profile writes its own allow_repos, exactly,
//     and every account it lists covers every repository on that list. A
//     profile's list may only narrow what its accounts cover: the account is
//     the credential, and a repository the account does not cover is one whose
//     run the token must never reach. An absent list means the project's
//     (§4.1), which no account's list is required to cover, so it cannot be
//     decided and is refused; a wildcard makes the same question undecidable,
//     which is why both ends are exact.
//   - No Anthropic API key beside the accounts (D4): an account's job holds
//     its token, and a key mounted on the same profile would be a credential
//     no OAuth run of that job can use — and would make "which credential does
//     this job serve" unanswerable from the profile.
//
// The secrets refusal (isClaudeOAuthSecret) is deliberately NOT relaxed here:
// this field is the permission, and a Claude OAuth token stays something an
// operator never lists among a profile's mounts (§4.1).
func validProfileAccounts(at string, pr Profile, accounts map[string]Account) error {
	if len(pr.ClaudeAccounts) == 0 {
		// A profile that lists no account serves none — the state of every
		// profile today. Its secrets are then the only credential it mounts,
		// and the rules below are about a list that is not there.
		return nil
	}
	seen := make(map[string]bool, len(pr.ClaudeAccounts))
	for i, ref := range pr.ClaudeAccounts {
		if msg := config.ValidClaudeAccountRef(ref); msg != "" {
			return fmt.Errorf("%s.claude_accounts[%d]: %s", at, i, msg)
		}
		if _, ok := accounts[ref]; !ok {
			return fmt.Errorf("%s.claude_accounts[%d]: the policy defines no account %q: accounts.<name> is what says which repositories that account's token may be used by, and a profile may serve only an account the policy defines (one job is deployed per (profile, provider, account), so this entry names a job whose account nothing describes). Publish derives the block from the project's local config — claude_accounts.%s.allow_data_to, or project_secrets for the default account — so this node was hand-written, or published from a config whose account was removed since",
				at, i, ref, ref)
		}
		if seen[ref] {
			return fmt.Errorf("%s.claude_accounts[%d]: an account is listed twice; list each account once", at, i)
		}
		seen[ref] = true
	}
	// D4: no Anthropic API key beside the accounts, in either spelling. The
	// entry is not echoed — it may be the value itself (the same reason
	// CheckSecretName's refusal is not).
	for i, secret := range pr.Secrets {
		if config.MountsAnthropicKey(secret) {
			return fmt.Errorf("%s.secrets[%d]: a profile that lists claude_accounts may not also mount an Anthropic API key (D4): an account job holds its account's token, and a key beside it is a credential no OAuth run of that job can use — and which credential the job serves would stop being answerable from the profile. Drop the entry, or the claude_accounts list", at, i)
		}
	}
	// D3: the profile's own list, exact, and covered by every account.
	if len(pr.AllowRepos) == 0 {
		return fmt.Errorf("%s.claude_accounts: a profile that lists accounts must write its own allow_repos: an absent list means the project's (%s.allow_repos is not written), and no account is required to cover the project's whole enrollment — so which repositories this profile admits is undecidable against the accounts it serves. Write the repositories this profile admits, exactly, with no wildcard",
			at, at)
	}
	for i, pattern := range pr.AllowRepos {
		if strings.Contains(pattern, "*") {
			return fmt.Errorf("%s.allow_repos[%d]: %q carries a wildcard, and a profile that lists accounts must write exact repositories: whether an account covers this profile is decided repository by repository (an account's own allow_repos may hold no wildcard either), and a wildcard makes the subset rule undecidable. Write the repositories themselves", at, i, pattern)
		}
	}
	for _, ref := range pr.ClaudeAccounts {
		acct := accounts[ref]
		for _, repo := range pr.AllowRepos {
			if !AdmitRepo(acct.AllowRepos, repo) {
				return fmt.Errorf("%s.claude_accounts: the profile admits %s, and the account %q does not: accounts.%s.allow_repos is %s and profiles.%s.allow_repos is %s, so %s is on the profile's list and not on the account's. An account's list is derived from the project's local config (claude_accounts.%s.allow_data_to, or project_secrets for the default account) — add the repository there and publish again, or take it off the profile's allow_repos",
					at, repo, ref, ref, strings.Join(acct.AllowRepos, ", "), profileNameOf(at), strings.Join(pr.AllowRepos, ", "), repo, ref)
			}
		}
	}
	return nil
}

// profileNameOf is the profile name in a path this package spells
// "projectpolicy: profiles.<name>" (and "...profiles.<name>...."), so a refusal
// can name the profile and the keys it is about without a second parameter
// carrying the same string.
func profileNameOf(at string) string {
	name := strings.TrimPrefix(at, "projectpolicy: profiles.")
	if i := strings.IndexByte(name, '.'); i >= 0 {
		name = name[:i]
	}
	return name
}

// DeriveAccounts is the policy's accounts block, derived from the operator's
// local config: the one anchor, one edit rule (D3, D5). It is publish's
// function, and the deployer's — `fugaro update` validates the profiles it is
// about to deploy against the same block (internal/infra's ProjectJobs), so
// the document that gates a run and the jobs that run are built from one
// answer.
//
// The translation is spelled out because the two documents spell a repository
// differently. config's claude_accounts.<name>.allow_data_to holds owner/name
// slugs, which the local config's own rules hold to a repository with no
// provider (a named account is one credential per person or team, and the
// plugins that read it are per-repository). A policy's allow_repos entry is
// provider-qualified (Canonical), because the run that reads it has to decide
// a repository of one provider on a job that serves exactly one. So each slug
// is qualified with the provider of the profile(s) naming the account, through
// Canonical — the one function the matcher and the allow-list are built from.
//
// Only accounts some profile names are published. An account no profile lists
// is one no job mounts, and a block entry for it would be a list of
// repositories published on the readable node for no reason (§5(2): the node
// is read by every run).
//
// The default account (D5) is the one case where the source is not
// claude_accounts: "default" is reserved there (it is the unsuffixed
// claude-oauth-token every repository already uses), so its list comes from
// the project_secrets entry that already decides whether its project-wide copy
// exists at all.
//
// Every refusal names the profile that lists the account and the block the
// operator edits (design §4.4 rows 1 and 2): this runs at publish and at
// `fugaro update`, and both readers are the operator, at a terminal.
func DeriveAccounts(profiles map[string]Profile, accounts map[string]config.ClaudeAccount, projectSecrets map[string]config.ProjectSecret) (map[string]Account, error) {
	named := map[string][]string{} // account name → the profiles naming it, sorted
	for _, name := range profileMapNames(profiles) {
		for _, ref := range profiles[name].ClaudeAccounts {
			named[ref] = append(named[ref], name)
		}
	}
	if len(named) == 0 {
		return nil, nil
	}
	out := make(map[string]Account, len(named))
	for _, ref := range sortedStringKeys(named) {
		if msg := config.ValidClaudeAccountRef(ref); msg != "" {
			return nil, fmt.Errorf("projectpolicy: profiles.%s.claude_accounts: %s", named[ref][0], msg)
		}
		slugs, source, err := accountSlugs(ref, accounts, projectSecrets, named[ref][0])
		if err != nil {
			return nil, err
		}
		if len(slugs) == 0 {
			return nil, fmt.Errorf("projectpolicy: profiles.%s.claude_accounts: the account %q may be used by no repository, and an account's policy list cannot be empty: %s must name at least one repository (owner/name), which is what lets the account's project-wide copy exist and what accounts.%s.allow_repos is derived from — add one, or drop the account from the profile",
				named[ref][0], ref, source, ref)
		}
		var list []string
		for _, profileName := range named[ref] {
			provider := profiles[profileName].Provider
			// A profile that names an account without naming a provider has
			// no provider to qualify the account's repositories with, and it
			// is refused for the field it is missing rather than for the
			// slug: this runs before the document is judged (see
			// providerRequired), and Canonical('', slug) would otherwise
			// report the repository as the problem.
			if provider == "" {
				return nil, providerRequired("projectpolicy: profiles." + profileName)
			}
			for _, slug := range slugs {
				canonical, err := Canonical(provider, slug)
				if err != nil {
					return nil, fmt.Errorf("projectpolicy: profiles.%s.claude_accounts: %s holds %q, which is not a repository: %v — an account's allow_repos entry is a provider-qualified repository name (the account's list is qualified with the profile's provider, %s here)", profileName, source, slug, err, provider)
				}
				if !slices.Contains(list, canonical) {
					list = append(list, canonical)
				}
			}
		}
		slices.Sort(list)
		out[ref] = Account{AllowRepos: list}
	}
	return out, nil
}

// accountSlugs is the owner/name list an account's policy entries are derived
// from, and the name of the block it came from (for a refusal to name). The
// default account reads project_secrets — the entry that stores the reserved
// claude-oauth-token project-wide — and every other account reads its own
// claude_accounts entry. A missing one is a refusal, not an empty list: the
// two states have different fixes, and the operator reads which one it is.
func accountSlugs(ref string, accounts map[string]config.ClaudeAccount, projectSecrets map[string]config.ProjectSecret, profileName string) ([]string, string, error) {
	if ref == config.DefaultClaudeAccount {
		secret := config.ClaudeAccountSecret(config.DefaultClaudeAccount)
		entry, ok := projectSecrets[secret]
		if !ok {
			return nil, "", fmt.Errorf("projectpolicy: profiles.%s.claude_accounts: the default account (%q) is not stored project-wide in the project's local config: its token is %s, and a project job mounts it only when project_secrets carries a %s entry whose allow_data_to lists the repositories that may use it — add `project_secrets: {%s: {allow_data_to: [owner/name]}}` there, or drop the account from the profile",
				profileName, ref, secret, secret, secret)
		}
		return entry.AllowDataTo, "project_secrets." + secret + ".allow_data_to", nil
	}
	acct, ok := accounts[ref]
	if !ok {
		return nil, "", fmt.Errorf("projectpolicy: profiles.%s.claude_accounts: the local config defines no claude_accounts entry for %q: an account's policy list is derived from its own allow_data_to there, so a profile naming an account the file does not define names a token whose repositories are undecidable — define it as `claude_accounts: {%s: {allow_data_to: [owner/name]}}`, or drop the account from the profile",
			profileName, ref, ref)
	}
	return acct.AllowDataTo, "claude_accounts." + ref + ".allow_data_to", nil
}

// AccountNames is the policy's accounts block keys, sorted: what a reader
// lists (`policy show`, D10) and what the refusals below name.
func (p *Policy) AccountNames() []string { return sortedStringKeys(p.Accounts) }
