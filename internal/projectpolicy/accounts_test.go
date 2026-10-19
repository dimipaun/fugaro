package projectpolicy

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// accountsNode is a policy node carrying the accounts and profiles blocks under
// test, with the minimum the decoder otherwise needs. An empty block is left
// out of the document, which is the way a node says it carries none.
func accountsNode(accounts, profiles string) string {
	body := `"version":` + itoa(Version) + `,"budget":{"mode":"enforce"},"allow_repos":["github/acme/app"]`
	if accounts != "" {
		body += `,"accounts":` + accounts
	}
	if profiles != "" {
		body += `,"profiles":` + profiles
	}
	return "{" + body + "}"
}

// TestAccountsBlockStrictDecode: an accounts entry is the gate a token is
// bounded by, so a key inside it that this Fugaro does not read is not a
// dropped field but a rule gone quiet. The cases are the ones the providers
// and profiles blocks are held against, each of which reads as "the list the
// document states" while being a different list: an unknown key
// (DisallowUnknownFields, with the member-spelling check refusing it a second
// time — a name no member answers to is not a spelling of one), a member
// spelled in another case (encoding/json folds case, so "Allow_Repos" answers
// to allow_repos while every check that reads the raw document looks up the
// documented spelling), and a member or a block key written twice (the decoder
// keeps the last one it reads).
//
// Two mutations prove the two mechanisms: taking spelledMember out of the
// member loop (`known := member`) lets the case-folded entry through, and
// taking the strict decode and that loop out together lets the unknown key
// through.
func TestAccountsBlockStrictDecode(t *testing.T) {
	for name, tc := range map[string]struct{ accounts, want string }{
		"an unknown key inside an entry": {
			`{"ops":{"allow_repos":["github/acme/infra"],"allow_repo":["github/acme/infra"]}}`,
			"accounts.ops",
		},
		"a case-folded member": {
			`{"ops":{"Allow_Repos":["github/acme/infra"]}}`,
			"write allow_repos",
		},
		"a member written twice": {
			`{"ops":{"allow_repos":["github/acme/infra"],"allow_repos":["github/acme/app"]}}`,
			"names \"allow_repos\" twice",
		},
		"an account written twice": {
			`{"ops":{"allow_repos":["github/acme/infra"]},"ops":{"allow_repos":["github/acme/app"]}}`,
			"names \"ops\" twice",
		},
		"a block that is not an object":  {`5`, "accounts"},
		"an entry that is not an object": {`{"ops":["github/acme/infra"]}`, "accounts.ops"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := Decode([]byte(accountsNode(tc.accounts, ""))); err == nil {
				t.Fatalf("Decode accepted %s", tc.accounts)
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not say %q: %v", tc.want, err)
			}
		})
	}

	// And the documented spelling decodes, whole, with the block read as a
	// known key rather than filed under UnknownKeys: a reader that dropped it
	// there would enforce no account's allow_repos at all while the profiles
	// beside it still name accounts.
	p, err := decodeNode(t, accountsNode(`{"ops":{"allow_repos":["github/acme/infra","github/acme/app"]}}`, ""))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Accounts["ops"].AllowRepos; len(got) != 2 || got[0] != "github/acme/infra" {
		t.Fatalf("accounts.ops.allow_repos = %v", got)
	}
	if len(p.UnknownKeys) != 0 {
		t.Errorf("the accounts block was tolerated as unknown: %v", p.UnknownKeys)
	}
	if names := p.AccountNames(); len(names) != 1 || names[0] != "ops" {
		t.Errorf("AccountNames = %v", names)
	}
	// An empty block defines nothing and is legal: it is a node that serves no
	// account, not one whose block went missing (which is what a missing entry
	// for a profile's account is refused for below).
	p, err = decodeNode(t, accountsNode(`{}`, ""))
	if err != nil || len(p.Accounts) != 0 {
		t.Fatalf("an empty accounts block: %v, %v", p, err)
	}
	// An unknown top-level key beside it keeps the P8 tolerance.
	p, err = decodeNode(t, `{"version":`+itoa(Version)+`,"budget":{"mode":"enforce"},"future_key":1,"accounts":{"ops":{"allow_repos":["github/acme/app"]}}}`)
	if err != nil || p.Accounts["ops"].AllowRepos[0] != "github/acme/app" {
		t.Fatalf("a node with an unknown top-level key: %v, %v", p, err)
	}
	if len(p.UnknownKeys) != 1 || p.UnknownKeys[0] != "future_key" {
		t.Errorf("UnknownKeys = %v, want the unknown key alone", p.UnknownKeys)
	}
}

// TestAccountAllowReposExactNonEmpty: an account's list is the answer to "may
// this token be used by this repository" (D3), and each mutation below is a
// way of losing that answer rather than stating it: an empty or null list
// admits nothing (and a profile may not name such an account at all), a
// wildcard admits everything the profile admits at a stroke — which is the
// opposite of a per-repository decision, and is not what the derivation can
// even write — and an entry that is not a canonical provider-qualified
// repository names a repository nothing matches.
func TestAccountAllowReposExactNonEmpty(t *testing.T) {
	for name, tc := range map[string]struct{ accounts, want string }{
		"an empty list":                   {`{"ops":{"allow_repos":[]}}`, "accounts.ops.allow_repos"},
		"a null list":                     {`{"ops":{"allow_repos":null}}`, "accounts.ops.allow_repos"},
		"a missing list":                  {`{"ops":{}}`, "accounts.ops.allow_repos"},
		"a wildcard":                      {`{"ops":{"allow_repos":["github/acme/*"]}}`, "carries a wildcard"},
		"the every-repository wildcard":   {`{"ops":{"allow_repos":["*"]}}`, "carries a wildcard"},
		"a wildcard inside a segment":     {`{"ops":{"allow_repos":["github/acme/app-*"]}}`, "carries a wildcard"},
		"no provider":                     {`{"ops":{"allow_repos":["acme/app"]}}`, "must name a provider"},
		"an unknown provider":             {`{"ops":{"allow_repos":["gitlab/acme/app"]}}`, "is not a git provider kind"},
		"a repository written twice":      {`{"ops":{"allow_repos":["github/acme/app","github/acme/app"]}}`, "listed twice"},
		"an account name that is not one": {`{"Ops":{"allow_repos":["github/acme/app"]}}`, "account name"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := Decode([]byte(accountsNode(tc.accounts, ""))); err == nil {
				t.Fatalf("Decode accepted %s", tc.accounts)
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not say %q: %v", tc.want, err)
			}
		})
	}
	// The default account is a legal KEY here (D5): a profile lists it as
	// "default", and its entry is derived from project_secrets. It is
	// reserved in claude_accounts, not in the policy.
	if _, _, err := Decode([]byte(accountsNode(`{"default":{"allow_repos":["github/acme/app"]}}`, ""))); err != nil {
		t.Fatalf("an accounts entry for the default account was refused: %v", err)
	}
}

// decodeNode decodes a policy node that must decode.
func decodeNode(t *testing.T, raw string) (*Policy, error) {
	t.Helper()
	p, found, err := Decode([]byte(raw))
	if err != nil {
		return nil, err
	}
	if !found {
		t.Fatal("Decode found no policy")
	}
	return p, nil
}

// coveredAccounts is an accounts block whose one account covers the profile
// below it: the shape every refusal test breaks one field of.
const coveredAccounts = `{"ops":{"allow_repos":["github/acme/app","github/acme/tools"]}}`

// coveredProfile is a profile that serves that account over exactly the
// repositories it covers.
const coveredProfile = `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h",` +
	`"claude_accounts":["ops"],"allow_repos":["github/acme/app","github/acme/tools"]}}`

// TestProfileAllowReposMustBeCoveredByAccount — THE RED STEP — is the subset
// rule (D3): a profile's own allow_repos may only narrow what the accounts it
// serves cover, never widen it, because the account is the credential and a
// repository the account does not cover is one the token must never reach. The
// mutation is deleting the coverage loop (the plan's red step): a profile then
// admits a repository the account's list does not, and the run of that
// repository holds a token whose owner said it may not be used there.
//
// The refusal must name both lists and the repository that is in one and not
// the other (design §4.4 row 2), because the fix differs with which list is
// wrong.
func TestProfileAllowReposMustBeCoveredByAccount(t *testing.T) {
	profile := `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h",` +
		`"claude_accounts":["ops"],"allow_repos":["github/acme/app","github/acme/other"]}}`
	_, _, err := Decode([]byte(accountsNode(coveredAccounts, profile)))
	if err == nil {
		t.Fatal("a profile admitting a repository its account does not cover was accepted")
	}
	for _, want := range []string{
		"profiles.standard.claude_accounts",
		"github/acme/other",             // the repository in one list and not the other
		"accounts.ops.allow_repos",      // the account's list, by name
		"profiles.standard.allow_repos", // the profile's list, by name
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	// A subset is covered: the profile's list narrows the account's.
	subset := `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h",` +
		`"claude_accounts":["ops"],"allow_repos":["github/acme/app"]}}`
	if _, err := decodeNode(t, accountsNode(coveredAccounts, subset)); err != nil {
		t.Fatalf("a profile narrower than its account was refused: %v", err)
	}
	// And the covered case itself decodes.
	if _, err := decodeNode(t, accountsNode(coveredAccounts, coveredProfile)); err != nil {
		t.Fatalf("the covered profile was refused: %v", err)
	}
	// Two accounts: EVERY one must cover the profile, not just one of them. A
	// profile served by two accounts is two jobs, and the repository reaches
	// whichever the run selects.
	two := `{"ops":{"allow_repos":["github/acme/app","github/acme/tools"]},"narrow":{"allow_repos":["github/acme/app"]}}`
	profileTwo := `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h",` +
		`"claude_accounts":["narrow","ops"],"allow_repos":["github/acme/app","github/acme/tools"]}}`
	_, _, err = Decode([]byte(accountsNode(two, profileTwo)))
	if err == nil || !strings.Contains(err.Error(), "accounts.narrow.allow_repos") {
		t.Fatalf("a profile admitted by one account and not the other was accepted: %v", err)
	}
}

// TestProfileWithAccountsNeedsOwnExactAllowRepos: a profile that serves an
// account must write its own allow_repos, exactly. An absent list means the
// project's (§4.1), which no account is required to cover — so the subset rule
// cannot be decided against it — and a wildcard makes the same question
// undecidable from the other end. The mutation is reading an absent list as the
// project's and skipping the rule: the profile then admits every repository the
// project enrolls while its accounts cover some of them, which is precisely the
// widening the rule exists to prevent.
func TestProfileWithAccountsNeedsOwnExactAllowRepos(t *testing.T) {
	noList := `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","claude_accounts":["ops"]}}`
	_, _, err := Decode([]byte(accountsNode(coveredAccounts, noList)))
	if err == nil || !strings.Contains(err.Error(), "profiles.standard.claude_accounts") {
		t.Fatalf("a profile serving an account with no allow_repos of its own: %v", err)
	}
	if !strings.Contains(err.Error(), "must write its own allow_repos") {
		t.Errorf("the refusal does not name the missing list: %v", err)
	}

	wildcard := `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h",` +
		`"claude_accounts":["ops"],"allow_repos":["github/acme/*"]}}`
	_, _, err = Decode([]byte(accountsNode(coveredAccounts, wildcard)))
	if err == nil || !strings.Contains(err.Error(), "profiles.standard.allow_repos[0]") {
		t.Fatalf("a profile serving an account with a wildcard list: %v", err)
	}
	if !strings.Contains(err.Error(), "carries a wildcard") {
		t.Errorf("the refusal does not name the wildcard: %v", err)
	}
	// A profile that serves NO account is untouched by both rules: the
	// project's list applies, wildcards and all, which is every profile of
	// this release's predecessor.
	plain := `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","allow_repos":["github/acme/*"]}}`
	if _, err := decodeNode(t, accountsNode(coveredAccounts, plain)); err != nil {
		t.Fatalf("a profile serving no account was refused: %v", err)
	}
}

// TestProfileAccountMustBeInTheAccountsBlock: an account's policy list is what
// says which repositories its token may be used by, so a profile naming an
// account the node does not define names a token whose reach is undecidable —
// and, on the job side, a job whose account segment nothing describes. The
// mutation is skipping the membership check: the node decodes and a run is
// admitted on a profile whose account no rule bounds.
func TestProfileAccountMustBeInTheAccountsBlock(t *testing.T) {
	missing := `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h",` +
		`"claude_accounts":["ghost"],"allow_repos":["github/acme/app"]}}`
	_, _, err := Decode([]byte(accountsNode(coveredAccounts, missing)))
	if err == nil {
		t.Fatal("a profile naming an account the node does not define was accepted")
	}
	for _, want := range []string{"profiles.standard.claude_accounts[0]", `"ghost"`, "accounts.<name>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}

	twice := `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h",` +
		`"claude_accounts":["ops","ops"],"allow_repos":["github/acme/app"]}}`
	if _, _, err := Decode([]byte(accountsNode(coveredAccounts, twice))); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("an account listed twice: %v", err)
	}

	// A malformed name is refused as a name, and the refusal does not echo it
	// (it may be a pasted value).
	bad := `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h",` +
		`"claude_accounts":["Acme Team"],"allow_repos":["github/acme/app"]}}`
	_, _, err = Decode([]byte(accountsNode(coveredAccounts, bad)))
	if err == nil || !strings.Contains(err.Error(), "account name") {
		t.Fatalf("a malformed account name: %v", err)
	}
	if strings.Contains(err.Error(), "Acme Team") {
		t.Errorf("the refusal echoes the malformed name: %v", err)
	}
}

// TestProfileAccountWithAnthropicKeyRefused is D4's no-API-key rule. The
// mutation is dropping the check: the profile publishes, and the account job's
// container then holds a key no OAuth run of it can use — and "which
// credential does this job serve" stops being answerable from the profile.
//
// The entry is the reserved secret's name, which is the spelling a profile can
// carry (config.MountsAnthropicKey documents why the design's other one — a
// provider whose key variable is ANTHROPIC_API_KEY — cannot be written here in
// this release). The variable spelling is refused one rule earlier, by
// CheckSecretName, and the test holds that too: it is refused exactly the same
// way on a profile serving no account, so D4 is not what refuses it, and a
// policy cannot carry an Anthropic key beside a claude_accounts list by that
// route either.
//
// The same entry on a profile that serves NO account is legal: an api-key
// profile is how an installation runs without OAuth at all.
func TestProfileAccountWithAnthropicKeyRefused(t *testing.T) {
	accountsLine := `"claude_accounts":["ops"],"allow_repos":["github/acme/app","github/acme/tools"],`
	profile := `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h",` +
		accountsLine + `"secrets":["` + config.AnthropicSecret + `"]}}`
	_, _, err := Decode([]byte(accountsNode(coveredAccounts, profile)))
	if err == nil {
		t.Fatal("a profile serving accounts and mounting the Anthropic API key was accepted")
	}
	for _, want := range []string{"profiles.standard.secrets[0]", "Anthropic API key", "claude_accounts"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	// The refusal says which of the two to drop, and never echoes the entry
	// (it may be the value itself).
	if !strings.Contains(err.Error(), "Drop the entry") {
		t.Errorf("the refusal names no fix: %v", err)
	}
	if strings.Contains(err.Error(), config.AnthropicSecret) {
		t.Errorf("the refusal echoes the entry: %v", err)
	}
	// The same entry on a profile serving no account is legal.
	plain := `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","secrets":["` + config.AnthropicSecret + `"]}}`
	if _, err := decodeNode(t, accountsNode("", plain)); err != nil {
		t.Fatalf("an api-key profile was refused: %v", err)
	}
	// The variable spelling: refused as a secret NAME, with or without
	// accounts, and never reaching D4's sentence.
	variable := config.ReservedSecrets[config.AnthropicSecret]
	for _, withAccounts := range []bool{true, false} {
		body := `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","secrets":["` + variable + `"]}}`
		if withAccounts {
			body = `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h",` + accountsLine + `"secrets":["` + variable + `"]}}`
		}
		_, _, err := Decode([]byte(accountsNode(coveredAccounts, body)))
		if err == nil {
			t.Fatalf("a profile mounting the variable %s was accepted", variable)
		}
		if !strings.Contains(err.Error(), "must be a secret name") {
			t.Errorf("the refusal of the variable spelling (accounts: %v) is not the name rule's: %v", withAccounts, err)
		}
		if strings.Contains(err.Error(), "Anthropic API key") {
			t.Errorf("the variable spelling reached D4's rule (accounts: %v): %v", withAccounts, err)
		}
	}
	// An ordinary secret beside the accounts is not what D4 refuses.
	withSecret := `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h",` +
		accountsLine + `"secrets":["deploy-key"]}}`
	if _, err := decodeNode(t, accountsNode(coveredAccounts, withSecret)); err != nil {
		t.Fatalf("a profile serving accounts and a git secret was refused: %v", err)
	}
}

// TestSecretsRefusalUnchanged: the new field is a permission of its own, and it
// does not open the old door. A Claude OAuth token listed in a profile's
// secrets: is refused exactly as it was — the token is added by the deployer,
// never by an operator — and listing the account in claude_accounts beside it
// changes nothing. The mutation is relaxing isClaudeOAuthSecret to allow an
// account the profile also lists: a node could then mount a token through
// secrets: and the account axis, and the deployer's job-naming rule would read
// one where the runner reads the other.
func TestSecretsRefusalUnchanged(t *testing.T) {
	for name, secrets := range map[string]string{
		"the default account's token":   `["claude-oauth-token"]`,
		"a named account's token":       `["claude-oauth-token-ops"]`,
		"a name merely shaped like one": `["claude-oauth-token-nobody"]`,
	} {
		t.Run(name, func(t *testing.T) {
			profile := `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h",` +
				`"claude_accounts":["ops"],"allow_repos":["github/acme/app","github/acme/tools"],"secrets":` + secrets + `}}`
			_, _, err := Decode([]byte(accountsNode(coveredAccounts, profile)))
			if err == nil {
				t.Fatalf("a profile mounting %s in secrets: was accepted", secrets)
			}
			for _, want := range []string{"person-owned", "phase 5"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal no longer says %q: %v", want, err)
				}
			}
			// And on its own, with no account listed: the refusal is the
			// secret's, not the pairing's.
			alone := `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","secrets":` + secrets + `}}`
			if _, _, err := Decode([]byte(accountsNode("", alone))); err == nil {
				t.Fatalf("a profile mounting %s in secrets: was accepted", secrets)
			}
		})
	}
}

// TestDeriveAccounts: the policy's accounts block is derived from the
// operator's local config, never hand-written (D3): each account's
// allow_data_to slugs are qualified with the provider of the profile(s) naming
// the account, and only accounts some profile names are published. The
// mutations are the ones that make the derived block disagree with the file it
// came from: leaving an account unqualified (an entry no canonical repository
// matches, so the account covers nothing), publishing every configured account
// (a list of repositories on the readable node for an account no job mounts),
// or reading allow_data_to from the wrong account.
func TestDeriveAccounts(t *testing.T) {
	profiles := map[string]Profile{
		"standard": {JobProfile: config.JobProfile{Provider: "github", ClaudeAccounts: []string{"ops", "default"}}},
		"mirror":   {JobProfile: config.JobProfile{Provider: "github", ClaudeAccounts: []string{"ops"}}},
	}
	accounts := map[string]config.ClaudeAccount{
		"ops":    {AllowDataTo: []string{"acme/tools", "acme/app"}},
		"unused": {AllowDataTo: []string{"acme/other"}},
	}
	secrets := map[string]config.ProjectSecret{
		"claude-oauth-token": {AllowDataTo: []string{"acme/sandbox"}},
	}
	got, err := DeriveAccounts(profiles, accounts, secrets)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("the derived block = %v, want the two accounts some profile names (and not the configured 'unused')", got)
	}
	if want := []string{"github/acme/app", "github/acme/tools"}; !slices.Equal(got["ops"].AllowRepos, want) {
		t.Errorf("ops.allow_repos = %v, want %v (provider-qualified, sorted, deduplicated over both profiles)", got["ops"].AllowRepos, want)
	}
	if want := []string{"github/acme/sandbox"}; !slices.Equal(got["default"].AllowRepos, want) {
		t.Errorf("default.allow_repos = %v, want %v (from project_secrets)", got["default"].AllowRepos, want)
	}
	// The derived block is what the node's own rules accept, so what publish
	// writes is a policy a reader decodes.
	node, _, err := Decode([]byte(accountsNode(`{"ops":{"allow_repos":["github/acme/app","github/acme/tools"]},"default":{"allow_repos":["github/acme/sandbox"]}}`, coveredProfile)))
	if err != nil {
		t.Fatalf("the derived shape was refused by the node's own rules: %v", err)
	}
	if len(node.Accounts) != 2 {
		t.Errorf("the node's accounts = %v", node.AccountNames())
	}

	// A profile naming an account the file does not define is refused, naming
	// the profile and the block the operator edits (design §4.4 row 1). The
	// assertion is the "no entry" sentence and not just the path: an account
	// that resolves to the zero value is refused by the empty-list rule below
	// too, and a test that accepted either would not be pinning this one.
	_, err = DeriveAccounts(map[string]Profile{"standard": {JobProfile: config.JobProfile{Provider: "github", ClaudeAccounts: []string{"ghost"}}}}, accounts, secrets)
	if err == nil || !strings.Contains(err.Error(), "defines no claude_accounts entry for") {
		t.Fatalf("an undefined account: %v", err)
	}
	if !strings.Contains(err.Error(), "profiles.standard.claude_accounts") {
		t.Errorf("the refusal does not name the profile entry: %v", err)
	}
	// The default account without a project_secrets entry: refused, naming the
	// entry the operator adds — and again the sentence, not the block's name
	// alone, which the empty-list refusal also carries.
	_, err = DeriveAccounts(map[string]Profile{"standard": {JobProfile: config.JobProfile{Provider: "github", ClaudeAccounts: []string{"default"}}}}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "is not stored project-wide") {
		t.Fatalf("the default account with no project_secrets entry: %v", err)
	}
	// An account whose allow_data_to is empty may be used by no repository, so
	// its policy list would be empty — which no profile may serve (and which
	// no project-wide container exists for, D6).
	_, err = DeriveAccounts(map[string]Profile{"standard": {JobProfile: config.JobProfile{Provider: "github", ClaudeAccounts: []string{"ops"}}}},
		map[string]config.ClaudeAccount{"ops": {}}, nil)
	if err == nil || !strings.Contains(err.Error(), "may be used by no repository") {
		t.Fatalf("an account with no allow_data_to: %v", err)
	}
	// No profile names an account: the block is absent, not empty.
	got, err = DeriveAccounts(map[string]Profile{"standard": {JobProfile: config.JobProfile{Provider: "github"}}}, accounts, secrets)
	if err != nil || got != nil {
		t.Fatalf("a project serving no account derived %v, %v", got, err)
	}
}

// TestCheckWrittenAccountFieldsReturnsTheReadError: the member-spelling check
// reads an entry as a map, and an entry it cannot read that way is an error —
// the same error checkWrittenProfileFields returns for the profiles block, and
// never a check that quietly does not run. A spelling check skipped for the
// entry it was written for is the check a case-folded or misspelt member walks
// past, which is the whole failure the check exists to stop, and
// decodeAccountBlock's other guards are not something this one may lean on:
// they are what a later change to that function can remove.
//
// The error is unreachable through Decode (the strict decode refuses anything
// that is not an object first, which TestAccountsBlockStrictDecode's "an entry
// that is not an object" case pins), so the guard is held here, where it can be
// reached, rather than through a document no Parse can produce. The mutation is
// restoring the `err == nil` guard this function was extracted from: the four
// cases below then pass silently.
func TestCheckWrittenAccountFieldsReturnsTheReadError(t *testing.T) {
	for name, raw := range map[string]string{
		"an entry that is not an object": `["github/acme/app"]`,
		"a number":                       `5`,
		"a string":                       `"github/acme/app"`,
		"an object with trailing data":   `{"allow_repos":["github/acme/app"]} junk`,
	} {
		t.Run(name, func(t *testing.T) {
			err := checkWrittenAccountFields("ops", json.RawMessage(raw))
			if err == nil {
				t.Fatalf("%s was read as an entry whose members are spelled correctly: the spelling check did not run", name)
			}
			if !strings.Contains(err.Error(), "accounts.ops") {
				t.Errorf("the refusal does not name the entry it is about: %v", err)
			}
		})
	}

	// The two refusals the check itself makes, unchanged by the extraction: a
	// member spelled in another case, and a name no member answers to.
	for name, tc := range map[string]struct{ raw, want string }{
		"a case-folded member": {`{"Allow_Repos":["github/acme/app"]}`, "write allow_repos"},
		"an unknown member":    {`{"allow_repo":["github/acme/app"]}`, `unknown field "allow_repo"`},
	} {
		t.Run(name, func(t *testing.T) {
			err := checkWrittenAccountFields("ops", json.RawMessage(tc.raw))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("want a refusal naming %q, got %v", tc.want, err)
			}
		})
	}

	// And the documented spelling passes, so the error is the reader's and not
	// a blanket refusal of the block.
	if err := checkWrittenAccountFields("ops", json.RawMessage(`{"allow_repos":["github/acme/app"]}`)); err != nil {
		t.Errorf("the documented spelling was refused: %v", err)
	}
}
