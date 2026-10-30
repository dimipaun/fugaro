package projectpolicy

import (
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
)

// decodeProfiles is a policy node carrying the profiles block under test, with
// the minimum the decoder otherwise needs.
func decodeProfiles(t *testing.T, profiles string) (*Policy, error) {
	t.Helper()
	raw := `{"version":` + itoa(Version) + `,"budget":{"mode":"enforce"},"allow_repos":["github/acme/app"],"profiles":` + profiles + `}`
	p, found, err := Decode([]byte(raw))
	if err != nil {
		return nil, err
	}
	if !found {
		t.Fatal("Decode found no policy")
	}
	return p, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestProfileForRefusesANamedProfileThatIsNotDefined: the fail-closed half of
// step 1. A policy that does not define the profile a workflow selected must
// refuse the run, never fall back to another profile — the mutation is a
// ProfileFor that returns the default (or a zero Profile) instead of an error.
func TestProfileForRefusesANamedProfileThatIsNotDefined(t *testing.T) {
	p, err := decodeProfiles(t, `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h"}}`)
	if err != nil {
		t.Fatal(err)
	}
	pr, err := p.ProfileFor("standard")
	if err != nil {
		t.Fatalf("ProfileFor(standard) = %v", err)
	}
	if pr.Provider != "github" || pr.CPU != 2 {
		t.Errorf("ProfileFor(standard) = %+v, want the github profile", pr)
	}
	// "" is the default, and the policy defines it.
	if _, err := p.ProfileFor(""); err != nil {
		t.Errorf("ProfileFor(\"\") = %v, want the default profile (%s)", err, DefaultProfileName)
	}
	if _, err := p.ProfileFor("big"); err == nil {
		t.Fatal("ProfileFor(big) succeeded for a profile the policy does not define")
	} else if !strings.Contains(err.Error(), "big") || !strings.Contains(err.Error(), DefaultProfileName) {
		t.Errorf("the refusal names neither the missing profile nor the defined ones: %v", err)
	}
	// A policy with no profiles at all defines nothing, not even the default.
	empty := &Policy{}
	if _, err := empty.ProfileFor(""); err == nil {
		t.Fatal("ProfileFor on a policy with no profiles succeeded")
	}
}

// TestProfileAllowReposFallsBackToTheProjectList: design §4.1/step 3 — an empty
// profile allow_repos means the project's list applies, never everything. The
// mutation is a fallback to ["*"] (or to "admit anything when the list is
// empty"), which would admit every repository to a profile whose owner wrote
// nothing.
func TestProfileAllowReposFallsBackToTheProjectList(t *testing.T) {
	p, err := decodeProfiles(t, `{
		"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","allow_repos":["github/acme/*"]},
		"quiet":{"provider":"github","cpu":1,"memory":"1Gi","timeout_class":"1h"}
	}`)
	if err != nil {
		t.Fatal(err)
	}
	p.AllowRepos = []string{"github/acme/app"}

	// A profile with its own list uses it, and only it.
	own, err := p.ProfileAllowRepos("standard")
	if err != nil {
		t.Fatal(err)
	}
	if len(own) != 1 || own[0] != "github/acme/*" {
		t.Errorf("ProfileAllowRepos(standard) = %v, want the profile's own list", own)
	}
	// A profile without one falls back to the project's — and the project's is
	// exactly one repository, so anything wider is a loosening.
	fallback, err := p.ProfileAllowRepos("quiet")
	if err != nil {
		t.Fatal(err)
	}
	if len(fallback) != 1 || fallback[0] != "github/acme/app" {
		t.Errorf("ProfileAllowRepos(quiet) = %v, want the project's allow_repos", fallback)
	}
	if AdmitRepo(fallback, "github/other/app") {
		t.Error("the fallback admits a repository the project's list does not")
	}
	// An empty project list admits nothing: the fallback is not "everything".
	p.AllowRepos = nil
	none, err := p.ProfileAllowRepos("quiet")
	if err != nil {
		t.Fatal(err)
	}
	if AdmitRepo(none, "github/acme/app") {
		t.Error("an empty project allow_repos admitted a repository through the fallback")
	}
	// An undefined profile is a refusal here too, not an empty admission list.
	if _, err := p.ProfileAllowRepos("nope"); err == nil {
		t.Error("ProfileAllowRepos of an undefined profile succeeded")
	}
}

// TestProfileRefusesAnInvalidResourceForm: step 4 — a policy cannot name a job
// the Cloud Run API will refuse. The forms are internal/backend/gcp's own
// (limits.go), so this test is also the drift guard between the two.
func TestProfileRefusesAnInvalidResourceForm(t *testing.T) {
	base := func(cpu int, memory, timeout string) string {
		fields := `"provider":"github","cpu":` + itoa(cpu) + `,"memory":"` + memory + `","timeout_class":"` + timeout + `"`
		return `{"standard":{` + fields + `}}`
	}
	if _, err := decodeProfiles(t, base(2, "4Gi", "6h")); err != nil {
		t.Fatalf("a valid profile was refused: %v", err)
	}
	for _, tc := range []struct{ what, doc string }{
		{"cpu 3 is not a Cloud Run job choice", base(3, "4Gi", "6h")},
		{"cpu 0", base(0, "4Gi", "6h")},
		{"memory over 32Gi", base(8, "64Gi", "6h")},
		{"memory below 512Mi", base(1, "128Mi", "6h")},
		{"9Gi needs 4 CPUs", base(1, "9Gi", "6h")},
		{"a memory string that is not a quantity", base(2, "lots", "6h")},
		{"a timeout that is not a duration", base(2, "4Gi", "soon")},
		{"a zero timeout", base(2, "4Gi", "0s")},
		{"a timeout past Cloud Run's 168h ceiling", base(2, "4Gi", "200h")},
	} {
		if _, err := decodeProfiles(t, tc.doc); err == nil {
			t.Errorf("%s was accepted", tc.what)
		}
	}
}

// TestDecodeRefusesProfilesWithoutTheDefault: step 1 — a published policy that
// carries a profiles block must define the profile every workflow defaults to.
// The mutation is accepting the block, which would leave those workflows
// selecting a profile nobody defined.
func TestDecodeRefusesProfilesWithoutTheDefault(t *testing.T) {
	if _, err := decodeProfiles(t, `{"big":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h"}}`); err == nil {
		t.Fatalf("a profiles block without %s was accepted", DefaultProfileName)
	} else if !strings.Contains(err.Error(), DefaultProfileName) {
		t.Errorf("the refusal does not name %s: %v", DefaultProfileName, err)
	}
	if _, err := decodeProfiles(t, `{}`); err == nil {
		t.Fatal("an empty profiles block was accepted")
	}
	// No profiles block at all is a phase-1 node: it sets no profile, and the
	// key it leaves out keeps what the job carries (the policy's own contract).
	p, _, err := Decode([]byte(`{"version":` + itoa(Version) + `,"budget":{"mode":"enforce"}}`))
	if err != nil {
		t.Fatalf("a policy without profiles was refused: %v", err)
	}
	if len(p.Profiles) != 0 {
		t.Errorf("profiles = %v, want none", p.Profiles)
	}
}

// TestDecodeRefusesAProfileWithoutAProvider: finding 4 — the provider is
// required, because it decides which repositories a job serves and is part of
// the job's identity.
func TestDecodeRefusesAProfileWithoutAProvider(t *testing.T) {
	for _, doc := range []string{
		`{"standard":{"cpu":2,"memory":"4Gi","timeout_class":"6h"}}`,
		`{"standard":{"provider":"gitlab","cpu":2,"memory":"4Gi","timeout_class":"6h"}}`,
		`{"standard":{"provider":"GITHUB","cpu":2,"memory":"4Gi","timeout_class":"6h"}}`,
	} {
		if _, err := decodeProfiles(t, doc); err == nil {
			t.Errorf("%s was accepted without a provider kind", doc)
		}
	}
}

// TestDecodeRefusesAnAllowRepoPatternOfAnotherProvider: step 3 — a profile's
// allow_repos may only name repositories of its own provider, because a
// pattern of another provider could never match a repository the profile
// serves (task 3.2's job is one provider's).
func TestDecodeRefusesAnAllowRepoPatternOfAnotherProvider(t *testing.T) {
	bad := `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","allow_repos":["bitbucket/acme/app"]}}`
	if _, err := decodeProfiles(t, bad); err == nil {
		t.Fatal("a bitbucket pattern was accepted on a github profile")
	}
	good := `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","allow_repos":["github/acme/*","*"]}}`
	if _, err := decodeProfiles(t, good); err != nil {
		t.Fatalf("a provider-qualified pattern was refused: %v", err)
	}
	// A malformed pattern is refused too, by the allow-list's own rule.
	malformed := `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","allow_repos":["acme/*"]}}`
	if _, err := decodeProfiles(t, malformed); err == nil {
		t.Fatal("a bare owner/name pattern was accepted")
	}
}

// TestDecodeRefusesAMisspeltKeyInsideAProfile: the block is decoded with
// DisallowUnknownFields, and this test is the one that fails if that is
// dropped (the mutation). It matters more here than at the top level: a
// misspelt allow_repo would leave the profile with no list of its own and fall
// back to the project's wider one, silently, on a node an administrator can
// write by hand.
func TestDecodeRefusesAMisspeltKeyInsideAProfile(t *testing.T) {
	for _, doc := range []string{
		`{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","allow_repo":["github/other/*"]}}`,
		`{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout":"6h"}}`,
		`{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","secret":["a"]}}`,
	} {
		if _, err := decodeProfiles(t, doc); err == nil {
			t.Errorf("a misspelt key was accepted: %s", doc)
		}
	}
	// The documented spellings all decode.
	good := `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","secrets":["deploy-key"],"allow_repos":["github/acme/*"]}}`
	p, err := decodeProfiles(t, good)
	if err != nil {
		t.Fatalf("a fully spelled profile was refused: %v", err)
	}
	if len(p.Profiles["standard"].Secrets) != 1 || len(p.Profiles["standard"].AllowRepos) != 1 {
		t.Errorf("the profile decoded as %+v", p.Profiles["standard"])
	}
}

// TestDecodeRefusesAProfileNameThatIsNotAProfileName: an administrator writing
// a node by hand can spell a name no selection could ever use, and a profile
// nothing can select is worse than a refusal — it looks defined. The mutation
// is dropping ValidProfileName from validProfiles.
func TestDecodeRefusesAProfileNameThatIsNotAProfileName(t *testing.T) {
	bad := func(name string) string {
		return `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h"},` +
			`"` + name + `":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h"}}`
	}
	for _, name := range []string{"Bad", "with_underscore", "-leading", "trailing-", "a name"} {
		if _, err := decodeProfiles(t, bad(name)); err == nil {
			t.Errorf("the profile name %q was accepted", name)
		}
	}
	if _, err := decodeProfiles(t, bad("standard-bitbucket")); err != nil {
		t.Errorf("a valid profile name was refused: %v", err)
	}
}

// TestDecodeRefusesAProfileSecretThatIsAValueOrNotAName: a secret entry is a
// NAME, held to config's own rule for one. The mutation is dropping the
// CheckSecretName call, which would publish a pasted API key on a node every
// run token can read — and the refusals below must never echo the entry,
// because the entry may be the value.
func TestDecodeRefusesAProfileSecretThatIsAValueOrNotAName(t *testing.T) {
	secrets := func(list string) string {
		return `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","secrets":` + list + `}}`
	}
	for _, list := range []string{`[""]`, `["Deploy_Key"]`, `["sk-ant-api03-abc"]`, `["deploy-key","deploy-key"]`} {
		if _, err := decodeProfiles(t, secrets(list)); err == nil {
			t.Errorf("the secrets list %s was accepted", list)
		}
	}
	// A value is never echoed back in the refusal: the thing refused may be
	// the key itself.
	if _, err := decodeProfiles(t, secrets(`["sk-ant-api03-abc"]`)); err != nil && strings.Contains(err.Error(), "api03") {
		t.Errorf("the refusal echoes the entry it refused: %v", err)
	}
	if _, err := decodeProfiles(t, secrets(`["deploy-key","git-token"]`)); err != nil {
		t.Fatalf("valid secret names were refused: %v", err)
	}
}

// TestDecodeRefusesAProfileMountingAClaudeOAuthToken: a Claude account is
// person-owned (design D2) and its allow_data_to is a per-repository list a
// shared job cannot honour, so no profile may mount a Claude OAuth token until
// phase 5 decides how an account reaches a shared job. That rule already
// refuses `fugaro update` (internal/infra's projectJobSecrets); it is stated here
// as well so that publish, the deployer and both readers of the node
// (internal/cli's launch and internal/runner) hold a profile to ONE rule.
//
// Without it the two documents could disagree about one profile: publish
// copies the operator's profiles verbatim, so a policy could carry a profile
// mounting the reserved claude-oauth-token whose job `fugaro update` refuses to
// deploy — the launcher would compute the account segment of a job that does
// not exist (the default account on its side, the empty account on the
// deployer's) and call Cloud Run on a 404, and the runner would match no
// candidate of its own and halt every run of that profile with cannotPlaceJob,
// which blames a stale module version instead of the profile. The mutation is
// dropping the refusal: every secrets list below is accepted.
//
// The whole claude-oauth-token-<account> namespace is refused, not only the
// names a claude_accounts block happens to define (config.ClaudeOAuthTokenPrefix
// says why: a name merely shaped like one must be refused wherever a secret
// name is accepted).
// The rule is enforced on the read path as well as at publish and at deploy:
// validProfiles runs inside the decode (policy.go's Decode), so a node
// written by an older CLI, or by hand, is refused by every reader of it — the
// launcher, the runner and `fugaro project policy show` — and not only by the
// command that published it.
//
// The mutation is dropping the isClaudeOAuthSecret call from validProfiles:
// every secrets list below is accepted, on the read path included.
func TestDecodeRefusesAProfileMountingAClaudeOAuthToken(t *testing.T) {
	secrets := func(list string) string {
		return `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","secrets":` + list + `}}`
	}
	for _, list := range []string{
		`["claude-oauth-token"]`,
		`["claude-oauth-token-acme-personal"]`,
		`["claude-oauth-token-nobody"]`,
		`["github-app-key","claude-oauth-token"]`,
	} {
		_, err := decodeProfiles(t, secrets(list))
		if err == nil {
			t.Errorf("the secrets list %s was accepted", list)
			continue
		}
		if !strings.Contains(err.Error(), "person-owned") || !strings.Contains(err.Error(), "phase 5") {
			t.Errorf("the refusal for %s reads %v, want one naming the token person-owned until phase 5", list, err)
		}
	}
	// Every other secret a profile may mount is still its to mount: the rule
	// is the Claude OAuth namespace and nothing else.
	if _, err := decodeProfiles(t, secrets(`["github-app-key","openrouter-api-key"]`)); err != nil {
		t.Fatalf("a profile mounting other project secrets was refused: %v", err)
	}
}

// TestDecodeRefusesAProfileMountingACodexOAuthToken: the Codex harness's OAuth
// credential names are reserved ahead of the harness (config.CodexOAuthToken,
// owner ruling of 2026-10-18). Fugaro does not drive Codex, so nothing mints a
// value for one and `fugaro secrets set` refuses to store one: a profile
// naming it would mount a container that can never have a version, and would
// name the credential the harness reads the day it lands. The whole
// codex-oauth-token-<name> namespace, not only the bare name.
//
// The mutation is dropping the refusal: every secrets list below is accepted.
func TestDecodeRefusesAProfileMountingACodexOAuthToken(t *testing.T) {
	secrets := func(list string) string {
		return `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","secrets":` + list + `}}`
	}
	for _, list := range []string{
		`["codex-oauth-token"]`,
		`["codex-oauth-token-acme"]`,
		`["github-app-key","codex-oauth-token"]`,
	} {
		_, err := decodeProfiles(t, secrets(list))
		if err == nil {
			t.Errorf("the secrets list %s was accepted", list)
			continue
		}
		for _, want := range []string{"reserved for the Codex harness's OAuth credential", "codex-oauth-token"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal for %s does not say %q: %v", list, want, err)
			}
		}
	}
	// A name merely shaped like one is not the namespace: the rule is
	// exactly the two prefixes (config.CodexOAuthSecret).
	if _, err := decodeProfiles(t, secrets(`["my-codex-oauth-token","codex-token"]`)); err != nil {
		t.Fatalf("a name merely shaped like a Codex token was refused: %v", err)
	}
}

// TestDecodeRefusesAProfileNameWrittenTwice: profiles are keyed by their exact
// spelling and looked up by it (ProfileFor, and every reader of a selection
// through it), so encoding/json keeping the last of a repeated key would drop
// the first profile whole and leave which profile a run lands on — and every
// gate that profile carries, its allow_repos and its secrets among them —
// depending on the order the document's keys were written in. The providers
// block already holds this rule (validProviders, checkNoDuplicateNames); this
// is the same rule for the block beside it.
//
// The mutation is dropping the checkNoDuplicateNames call from
// decodeProfileBlock: the first document below decodes to the second profile.
func TestDecodeRefusesAProfileNameWrittenTwice(t *testing.T) {
	const one = `{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","allow_repos":["github/acme/app"]}`
	const second = `{"provider":"bitbucket","cpu":2,"memory":"4Gi","timeout_class":"6h"}`
	doc := `{"standard":` + one + `,"standard":` + second + `}`
	_, err := decodeProfiles(t, doc)
	if err == nil {
		t.Fatal("a profile name written twice was accepted")
	}
	for _, want := range []string{"profiles", `"standard"`, "twice"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	// A second name is not a duplicate: the same two profiles under two names
	// decode, so this rule claims only the repeat.
	two := `{"standard":` + one + `,"big":` + second + `}`
	p, err := decodeProfiles(t, two)
	if err != nil {
		t.Fatalf("two distinct profile names were refused: %v", err)
	}
	if len(p.Profiles) != 2 {
		t.Errorf("profiles = %v, want both", p.ProfileNames())
	}
}

// TestDecodeRefusesAProfileMemberWrittenTwice: a profile whose member is
// written twice folds onto one field, and encoding/json keeps the last value it
// reads — so a second "provider" picks the provider axis of the job the launch
// selects, and a second "secrets" is the list the job mounts. Both are decided
// by the order the operator happened to write the keys in, which is exactly the
// silence TestDecodeRefusesAProfileNameWrittenTwice and the top-level and
// providers-block checks refuse beside this one; checkWrittenProfileFields
// applies the same rule to a profile's own members, and its missing call is the
// mutation (before it, every document below decodes to a working profile).
func TestDecodeRefusesAProfileMemberWrittenTwice(t *testing.T) {
	const full = `"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h"`
	for _, tc := range []struct{ what, doc string }{
		// The provider axis of the job's name, and the profile the run holds the
		// repository to: written twice, the second one wins.
		{"provider", `{"standard":{` + full + `,"provider":"bitbucket"}}`},
		// The secrets the shared job mounts: written twice, the job mounts the
		// second list and the first operator's list is silently dropped.
		{"secrets", `{"standard":{` + full + `,"secrets":["deploy-key"],"secrets":["db-password"]}}`},
		// The admission list of a secret-bearing profile: written twice, the
		// list that admits fewer repositories is not the one that applies.
		{"allow_repos", `{"standard":{` + full + `,"allow_repos":["github/acme/app"],"allow_repos":["github/acme/*"]}}`},
		{"cpu", `{"standard":{` + full + `,"cpu":4}}`},
	} {
		_, err := decodeProfiles(t, tc.doc)
		if err == nil {
			t.Errorf("a profile with %s written twice was accepted", tc.what)
			continue
		}
		for _, want := range []string{"profiles.standard", "twice"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal for %s does not say %q: %v", tc.what, want, err)
			}
		}
	}
	// A profile that writes each of its members once still decodes, so the rule
	// claims the repeat and not the block (and not a member's presence beside
	// another).
	if _, err := decodeProfiles(t, `{"standard":{`+full+`,"secrets":["deploy-key"],"allow_repos":["github/acme/app"]}}`); err != nil {
		t.Errorf("a profile with each member once was refused: %v", err)
	}
}

// TestDecodeRefusesAnExplicitlyEmptyProfileAllowRepos: a written empty list is
// not the same statement as an absent one, and the node cannot carry the
// difference (the carrier's omitempty): read as "the project's list applies",
// an operator who wrote [] meaning "this profile admits nobody" would get every
// repository the project enrolls. The mutation is accepting the empty list.
func TestDecodeRefusesAnExplicitlyEmptyProfileAllowRepos(t *testing.T) {
	for _, doc := range []string{
		`{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","allow_repos":[]}}`,
		`{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","allow_repos":null}}`,
	} {
		if _, err := decodeProfiles(t, doc); err == nil {
			t.Errorf("an explicitly empty allow_repos was accepted: %s", doc)
		}
	}
	// And the key left out is the way a profile says the project's list
	// applies — with a project list that admits nothing, that is deny-all.
	p, err := decodeProfiles(t, `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h"}}`)
	if err != nil {
		t.Fatal(err)
	}
	p.AllowRepos = nil
	if ok, err := p.Admits("standard", "github/acme/app"); err != nil || ok {
		t.Errorf("Admits = %v, %v; want false with no project allow-list", ok, err)
	}
}

// TestDecodeRefusesACaseVariantProfileMember: encoding/json matches a member
// to its field case-insensitively, so "Allow_Repos": [] decoded into AllowRepos
// while the written-document check — which looks the name up as written, in
// fields["allow_repos"] — never saw it, and the profile fell back to the
// project's allow_repos instead of being refused. The guard is the one #402
// added for provider entries (carrierMembers/spelledMember), applied to the
// profile's members; the mutation is dropping it from checkWrittenProfileFields,
// which lets the first two documents through.
func TestDecodeRefusesACaseVariantProfileMember(t *testing.T) {
	for _, tc := range []struct{ what, doc string }{
		{"an empty allow_repos spelled Allow_Repos", `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","Allow_Repos":[]}}`},
		{"a null allow_repos spelled ALLOW_REPOS", `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","ALLOW_REPOS":null}}`},
		{"a provider spelled Provider", `{"standard":{"Provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h"}}`},
		{"a timeout spelled Timeout_Class", `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","Timeout_Class":"6h"}}`},
		// encoding/json folds case the way strings.EqualFold does, so the long
		// s (U+017F) matches 's' and the Kelvin sign (U+212A) matches 'k':
		// neither is a byte-for-byte spelling the written-document checks see.
		{"an empty allow_repos spelled with a long s", "{\"standard\":{\"provider\":\"github\",\"cpu\":2,\"memory\":\"4Gi\",\"timeout_class\":\"6h\",\"allow_repo\u017f\":[]}}"},
		{"secrets spelled with a long s", "{\"standard\":{\"provider\":\"github\",\"cpu\":2,\"memory\":\"4Gi\",\"timeout_class\":\"6h\",\"\u017fecrets\":[\"deploy-key\"]}}"},
	} {
		p, err := decodeProfiles(t, tc.doc)
		if err == nil {
			t.Errorf("%s was accepted: %+v", tc.what, p.Profiles["standard"])
			continue
		}
		if !strings.Contains(err.Error(), "spelled as fugaro.yaml spells them") {
			t.Errorf("the refusal for %s does not say the spelling is wrong: %v", tc.what, err)
		}
	}
	// The refusal names the member the document should have written, so an
	// operator reads the fix rather than the mistake.
	_, err := decodeProfiles(t, `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","Allow_Repos":["github/acme/app"]}}`)
	if err == nil || !strings.Contains(err.Error(), "allow_repos") {
		t.Errorf("the refusal does not name the documented spelling: %v", err)
	}
	// And the documented spellings still decode, so the guard is not a blanket
	// refusal of the block.
	if _, err := decodeProfiles(t, `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","secrets":["deploy-key"],"allow_repos":["github/acme/*"]}}`); err != nil {
		t.Errorf("a correctly spelled profile was refused: %v", err)
	}
}

// TestDecodeRefusesARepeatedProfileMember: encoding/json keeps the last member
// it reads, so a profile's member written twice is decided by the second write
// with nothing left in the decoded value to refuse — the same silence
// checkNoDuplicateNames closes for the document's top level, for
// providers.<name>, for accounts.<name>, for model_prices and, one entry down,
// for a provider's and a price's own members (checkMemberNames).
//
// The profile entry is the carrier that was left out: checkWrittenProfileFields
// reads the entry's members off a map, which has already folded a repeat away, so
// `"cpu": 2, "cpu": 32` was read as 32 and `"allow_repos": ["github/acme/*"]`
// followed by `"allow_repos": ["github/*/*"]` admitted every repository, both
// silently.
//
// Mutation (run, restored): drop the objectMemberNames/checkNoDuplicateNames pair
// from checkWrittenProfileFields — every document below is accepted again.
func TestDecodeRefusesARepeatedProfileMember(t *testing.T) {
	for _, tc := range []struct{ what, doc string }{
		{"cpu twice", `{"standard":{"provider":"github","cpu":2,"cpu":4,"memory":"4Gi","timeout_class":"6h"}}`},
		{"allow_repos twice, the second wider", `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","allow_repos":["github/acme/*"],"allow_repos":["github/*/*"]}}`},
		{"memory twice", `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","memory":"8Gi","timeout_class":"6h"}}`},
	} {
		p, err := decodeProfiles(t, tc.doc)
		if err == nil {
			t.Errorf("%s was accepted, read as %+v: a repeated member is decided by the last write with nothing left in the decoded value to refuse", tc.what, p.Profiles["standard"])
			continue
		}
		for _, want := range []string{"profiles.standard", "twice"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal for %s does not say %q: %v", tc.what, want, err)
			}
		}
	}
	// The rule claims the repeat, not the spelling: each member once still
	// decodes, so a profile is not refused for saying what it means.
	if _, err := decodeProfiles(t, `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","allow_repos":["github/acme/*"]}}`); err != nil {
		t.Errorf("a profile with each member written once was refused: %v", err)
	}
}

// TestProfileAdmitsAlsoRequiresTheProjectList: both gates, and the project's is
// not optional. §4.3: "there is never a state in which a shared job serves a
// repository that has not been allowed" — so a profile whose own list is wider
// than the project's enrolls nothing extra. The mutation is an Admits that
// reads the profile's list alone (Admission), which the design's own words
// about a secret staying away from some repositories make look sufficient.
func TestProfileAdmitsAlsoRequiresTheProjectList(t *testing.T) {
	p, err := decodeProfiles(t, `{
		"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h"},
		"wide":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","allow_repos":["github/*/*"]},
		"narrow":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","allow_repos":["github/acme/*"]}
	}`)
	if err != nil {
		t.Fatal(err)
	}
	p.AllowRepos = []string{"github/acme/app"}
	for _, tc := range []struct {
		profile, repo string
		want          bool
	}{
		{"standard", "github/acme/app", true},   // the project's list, unchanged
		{"standard", "github/other/app", false}, // and only it
		{"wide", "github/acme/app", true},       // the profile's list is wider, the project's gate holds
		{"wide", "github/other/app", false},     // which is the point
		{"narrow", "github/acme/app", true},
		{"narrow", "github/acme/other", false}, // the profile narrows the project's list
	} {
		ok, err := p.Admits(tc.profile, tc.repo)
		if err != nil {
			t.Fatalf("Admits(%s, %s) = %v", tc.profile, tc.repo, err)
		}
		if ok != tc.want {
			t.Errorf("Admits(%s, %s) = %v, want %v", tc.profile, tc.repo, ok, tc.want)
		}
	}
	// An undefined profile is an error (the fail-closed halt), never a plain
	// "not admitted": the two have different messages and different fixes.
	if _, err := p.Admits("nope", "github/acme/app"); err == nil {
		t.Error("Admits of an undefined profile returned a verdict")
	}
}

// TestProfileAdmitsOnlyItsOwnProvider: one job serves one provider — the
// provider is part of its slug and the git credential it holds — so a profile
// never admits a repository of another provider, however the two lists are
// spelled. The wildcard is the case that matters: `*` and `*/acme/*` admit
// every provider's repositories by design, and a project's allow_repos may
// enroll repositories of both providers, so without the provider gate a github
// job would clone a bitbucket repository. The mutation is dropping the
// providerOf check from Admits.
func TestProfileAdmitsOnlyItsOwnProvider(t *testing.T) {
	p, err := decodeProfiles(t, `{
		"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","allow_repos":["*"]},
		"bb":{"provider":"bitbucket","cpu":2,"memory":"4Gi","timeout_class":"6h","allow_repos":["*"]}
	}`)
	if err != nil {
		t.Fatal(err)
	}
	// Both providers' repositories are enrolled in the project, which is what
	// the provider gate has to hold against: the project's list is the widest
	// gate, and it is not the one that decides this.
	p.AllowRepos = []string{"github/acme/app", "bitbucket/acme/app"}
	for _, tc := range []struct {
		profile, repo string
		want          bool
	}{
		{"standard", "github/acme/app", true},
		{"standard", "bitbucket/acme/app", false},
		{"bb", "bitbucket/acme/app", true},
		{"bb", "github/acme/app", false},
	} {
		ok, err := p.Admits(tc.profile, tc.repo)
		if err != nil {
			t.Fatalf("Admits(%s, %s) = %v", tc.profile, tc.repo, err)
		}
		if ok != tc.want {
			t.Errorf("Admits(%s, %s) = %v, want %v", tc.profile, tc.repo, ok, tc.want)
		}
	}
	// A profile that names its own list is held to the provider all the same:
	// the gate is not the wildcard's, it is the profile's.
	p, err = decodeProfiles(t, `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","allow_repos":["github/acme/*"]}}`)
	if err != nil {
		t.Fatal(err)
	}
	p.AllowRepos = []string{"bitbucket/acme/app"}
	if ok, err := p.Admits("standard", "bitbucket/acme/app"); err != nil || ok {
		t.Errorf("Admits on a bitbucket repository = %v, %v; want false", ok, err)
	}
	// A repository that is not canonical names no provider, so it matches no
	// profile: fail closed rather than read the whole string as one.
	if ok, err := p.Admits("standard", "acme/app"); err != nil || ok {
		t.Errorf("Admits on a non-canonical repository = %v, %v; want false", ok, err)
	}
}

// TestProfileTimeoutUsesFugaroYAMLsOwnDurationSpelling: timeout_class is read
// with config.ParseDuration, the parser timeouts.total is read with, so a day
// count is a spelling both documents accept. The mutation is bare
// time.ParseDuration, which refuses "14d" here while the same installation
// accepts `timeouts: { total: 14d }` — the two documents disagreeing about one
// value's spelling, which the comment used to claim they did not.
func TestProfileTimeoutUsesFugaroYAMLsOwnDurationSpelling(t *testing.T) {
	base := func(timeout string) string {
		return `{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"` + timeout + `"}}`
	}
	// The accepted list ends at the bound, which is Cloud Run's ceiling minus
	// the slack the job's own task timeout adds (backend.TaskTimeoutSlack):
	// 167h58m is 604680 s, and the job is deployed with 604800 — exactly the
	// ceiling. 168h was in this list and is not any more: it deployed at
	// 604920 s, which Cloud Run refuses, so an installation whose profile
	// named it failed its apply and every launch on that profile was refused.
	for _, class := range []string{"90m", "6h", "1d12h", "6d", "167h58m"} {
		if _, err := decodeProfiles(t, base(class)); err != nil {
			t.Errorf("the timeout %q was refused: %v", class, err)
		}
	}
	// A day count is parsed rather than refused as a spelling: 14d is 336h, so
	// it breaks Cloud Run's ceiling — and the refusal says so, instead of
	// claiming the value is not a duration. 168h is here for the reason above,
	// and one second past the bound is the boundary itself.
	for _, class := range []string{"14d", "8d", "168h", "167h58m1s"} {
		_, err := decodeProfiles(t, base(class))
		if err == nil {
			t.Errorf("the timeout %q was accepted, past what a Cloud Run task allows", class)
			continue
		}
		if strings.Contains(err.Error(), "is not a duration") {
			t.Errorf("the timeout %q was refused as a spelling, not for the ceiling: %v", class, err)
		}
		if !strings.Contains(err.Error(), "Cloud Run") {
			t.Errorf("the refusal for %q does not name the ceiling it breaks: %v", class, err)
		}
	}
	// And the claim in validTimeoutClass's comment is checked rather than
	// asserted: this package and internal/config parse one spelling.
	d, err := config.ParseDuration("1d12h")
	if err != nil {
		t.Fatalf("config.ParseDuration(1d12h) = %v", err)
	}
	if d != 36*time.Hour {
		t.Errorf("config.ParseDuration(1d12h) = %s, want 36h", d)
	}
}

// TestDecodeProfileErrorsCarryTheirPrefixOnce: a refusal inside the profiles
// block names its place once ("projectpolicy: profiles.standard..."), not
// behind a second "projectpolicy: profiles:" the caller used to add.
func TestDecodeProfileErrorsCarryTheirPrefixOnce(t *testing.T) {
	for _, doc := range []string{
		`{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","allow_repos":[]}}`,
		`{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","Provider":"github"}}`,
		`{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h","bogus":1}}`,
		`{"standard":5}`,
		`[]`,
	} {
		_, err := decodeProfiles(t, doc)
		if err == nil {
			t.Errorf("%s was accepted", doc)
			continue
		}
		if n := strings.Count(err.Error(), "projectpolicy:"); n != 1 {
			t.Errorf("%s: the error carries the prefix %d times: %v", doc, n, err)
		}
		if !strings.HasPrefix(err.Error(), "projectpolicy: profiles") {
			t.Errorf("%s: the error does not name the profiles block: %v", doc, err)
		}
	}
}

// TestValidProfilesRequireAnOwnExactListForTheBitbucketToken: T2.1 — a profile
// that mounts bitbucket-token is a Bitbucket shared job, and the list it
// admits is the per-repository decision that job's token is bounded by, so the
// profile writes it itself and writes it exactly. An absent list would fall
// back to the project's wider enrollment, and a wildcard would make the subset
// check against bitbucket_token.allow_repos undecidable. The mutation is a
// validProfiles that reads the mount as an ordinary secret name.
func TestValidProfilesRequireAnOwnExactListForTheBitbucketToken(t *testing.T) {
	bbProfile := func(allow string) string {
		return `{"standard":{"provider":"bitbucket","cpu":2,"memory":"4Gi","timeout_class":"6h","secrets":["bitbucket-token"]` + allow + `}}`
	}
	block := `"bitbucket_token":{"allow_repos":["bitbucket/workspace-a/*"]},`
	// A profile mounting the token with no list of its own is refused: the
	// project's allow_repos is not a list the profile's token is bounded by.
	if _, err := decodeBitbucketNode(t, block+`"profiles":`+bbProfile(``)); err == nil {
		t.Fatal("a bitbucket profile mounting bitbucket-token without its own allow_repos was accepted")
	} else if !strings.Contains(err.Error(), "allow_repos") {
		t.Errorf("the refusal does not name allow_repos: %v", err)
	}
	// A wildcard on that list is refused too, in either spelling.
	for _, allow := range []string{
		`,"allow_repos":["bitbucket/workspace-a/*"]`,
		`,"allow_repos":["*"]`,
	} {
		if _, err := decodeBitbucketNode(t, block+`"profiles":`+bbProfile(allow)); err == nil {
			t.Errorf("a bitbucket profile mounting bitbucket-token was accepted with allow_repos %s", allow)
		} else if !strings.Contains(err.Error(), "wildcard") {
			t.Errorf("the refusal for %s does not name the wildcard: %v", allow, err)
		}
	}
	// The exact list within the block's is what the rule asks for.
	if _, err := decodeBitbucketNode(t, block+`"profiles":`+bbProfile(`,"allow_repos":["bitbucket/workspace-a/repo-a"]`)); err != nil {
		t.Fatalf("an exact profile list within the block's was refused: %v", err)
	}
}

// TestValidProfilesRefuseAProfileListBeyondTheBitbucketBlock: T2.1 — the
// profile's list is a subset of bitbucket_token.allow_repos, matched with
// MatchRepo, because the block is what the credentials service will later
// forward the token for (G3) and a profile's list can only narrow it. The
// mutation is a subset check that is skipped when no block is written.
func TestValidProfilesRefuseAProfileListBeyondTheBitbucketBlock(t *testing.T) {
	block := `"bitbucket_token":{"allow_repos":["bitbucket/workspace-a/repo-a"]},`
	mounting := func(allow string) string {
		return block + `"profiles":{"standard":{"provider":"bitbucket","cpu":2,"memory":"4Gi","timeout_class":"6h","secrets":["bitbucket-token"],"allow_repos":[` + allow + `]}}`
	}
	if _, err := decodeBitbucketNode(t, mounting(`"bitbucket/workspace-a/repo-b"`)); err == nil {
		t.Fatal("a profile whose allow_repos is beyond the block's was accepted")
	} else if !strings.Contains(err.Error(), "bitbucket/workspace-a/repo-b") {
		t.Errorf("the refusal does not name the repository: %v", err)
	}
	// A profile mounting the token with a list of its own, and no block at all:
	// nothing bounds it, so it is refused rather than read as bounded by the
	// project's allow_repos.
	if _, err := decodeBitbucketNode(t, `"profiles":{"standard":{"provider":"bitbucket","cpu":2,"memory":"4Gi","timeout_class":"6h","secrets":["bitbucket-token"],"allow_repos":["bitbucket/workspace-a/repo-a"]}}`); err == nil {
		t.Fatal("a profile mounting bitbucket-token was accepted with no bitbucket_token block")
	}
	// The repository itself is the block's, so the subset holds.
	if _, err := decodeBitbucketNode(t, mounting(`"bitbucket/workspace-a/repo-a"`)); err != nil {
		t.Fatalf("a profile list equal to the block's was refused: %v", err)
	}
}
