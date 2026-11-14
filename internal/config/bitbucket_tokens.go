package config

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// The three access token types Bitbucket Cloud issues (spec §"Token record",
// R1). The set is closed at three (R8): a fourth is a version bump of the
// record, not a new spelling of one.
const (
	// BitbucketTokenTypeRepo is a repository access token: it reaches the one
	// repository its coverage names and nothing else.
	BitbucketTokenTypeRepo = "repo"
	// BitbucketTokenTypeProject is a project access token (Bitbucket
	// Premium): it reaches every repository of one project, whichever
	// repositories those are today (spec §"Matching tokens to a job").
	BitbucketTokenTypeProject = "project"
	// BitbucketTokenTypeWorkspace is a workspace access token (Bitbucket
	// Premium): it reaches every repository of one workspace.
	BitbucketTokenTypeWorkspace = "workspace"
)

// BitbucketTokenTypes are the three types a record may state, narrowest
// first: the order the selector ranks candidates by (D2355) and the order a
// refusal lists them in, so the two cannot read differently.
var BitbucketTokenTypes = []string{BitbucketTokenTypeRepo, BitbucketTokenTypeProject, BitbucketTokenTypeWorkspace}

// BitbucketToken is one entry of the local config's bitbucket_tokens block
// (D2350, D2351, R1): a typed Bitbucket access token the credentials
// service's proxy may serve, described by what it reaches. The token's
// value is not here and never is — it is one entry of the service's bundle,
// written by `fugaro creds set` (D506) — and neither is the token's identity
// or the result of its last check: R12 puts both in the creds state store,
// so a hand-edited config cannot state an identity a run would trust.
//
// Type is stored although the coverage states it (R1): it is what the
// add-time probe asks (which call to make, T7) and what a listing prints,
// and a record whose halves disagree is refused rather than read as either.
// ProjectUUID and ProjectKey belong to a project token alone: the UUID is the
// stable identifier a repository's project is matched by (R2) and the key is
// kept for display, because a project key can be renamed and a UUID cannot.
// Expires is the one expiry source a typed token has (R6) — no secret_expires
// entry is read for it, so the two can never disagree.
type BitbucketToken struct {
	// Name is the name the token is spoken of by: the bundle key
	// bitbucket-token--<name> is spelled from it (T1, R13) and every audit
	// line carries it (R5). It is held to the account-name rules, so it can
	// never be the doubled-dash spelling of a key family itself.
	Name string `yaml:"name" json:"name"`
	// Type is one of BitbucketTokenTypes.
	Type string `yaml:"type,omitempty" json:"type,omitempty"`
	// Coverage is what the token reaches, in the forms R1 pairs with Type: a
	// repository (workspace/name, "*" allowed inside the second segment), a
	// whole workspace (workspace/*), or the workspace slug of a project
	// token.
	Coverage []string `yaml:"coverage,omitempty" json:"coverage,omitempty"`
	// ProjectUUID is a project token's project, as Bitbucket's API spells it
	// (braces included). It is what R2's lookup compares, and an empty one
	// matches no repository.
	ProjectUUID string `yaml:"project_uuid,omitempty" json:"project_uuid,omitempty"`
	// ProjectKey is the same project's key, kept for display only: it is what
	// an operator reads in Bitbucket's own UI, and it is never matched.
	ProjectKey string `yaml:"project_key,omitempty" json:"project_key,omitempty"`
	// Expires is the day the token stops working, YYYY-MM-DD
	// (localcfg.SecretExpiryLayout, whose layout this is validated by where
	// the date is typed). Empty means the operator has recorded none.
	Expires string `yaml:"expires,omitempty" json:"expires,omitempty"`
}

// BitbucketTokenPath is where one record's problems are reported: under the
// record's own name when it has one, so a refusal names the entry an operator
// edits (ValidateCredentials' rule), and under its place in the list when the
// name itself is what is wrong with it.
func BitbucketTokenPath(tokens []BitbucketToken, i int) string {
	if i >= 0 && i < len(tokens) && tokens[i].Name != "" {
		return "bitbucket_tokens." + pluginwire.Printable(tokens[i].Name)
	}
	return fmt.Sprintf("bitbucket_tokens[%d]", i)
}

// ValidateBitbucketTokens reports every rule the local config's
// bitbucket_tokens block breaks, the same shape as ValidateProjectSecrets and
// ValidateCredentials. It is asked at load (localcfg.validate) and therefore by
// `fugaro validate`, before anything derives the node's key from the records or
// writes a bundle entry for one (T3, T11): R1's agreement of type and coverage
// is the whole point of asking here rather than at the first proxied call,
// because a record whose two halves disagree would be probed as one type and
// selected as another.
func ValidateBitbucketTokens(tokens []BitbucketToken) []Problem {
	var ps []Problem
	seen := map[string]bool{}
	for i := range tokens {
		tok := tokens[i]
		at := BitbucketTokenPath(tokens, i)
		if msg := bitbucketTokenNameProblem(tok.Name); msg != "" {
			ps = append(ps, Problem{Path: at, Message: msg})
		} else if seen[tok.Name] {
			ps = append(ps, Problem{Path: at, Message: "this name is defined twice; a Bitbucket token is named once per block, because one bundle key bitbucket-token--<name> cannot stand for two records"})
		}
		seen[tok.Name] = true
		ps = append(ps, bitbucketTokenCoverageProblems(tok, at)...)
		ps = append(ps, bitbucketTokenProjectProblems(tok, at)...)
	}
	return ps
}

// bitbucketTokenNameProblem is the name rule, spelled once for a record and
// for the refusal a duplicate name carries.
//
// The name reaches a bundle key (bitbucket-token--<name>, R13) and every
// audit line (R5), so it is held to ClaudeAccountNameRE — the rule a name a
// bundle key is spelled from already keeps. That rule cannot produce a
// doubled dash, which is the whole reason the family is spelled with one:
// nothing has to be reserved, and a name can never be the document key of
// another token written out by hand.
func bitbucketTokenNameProblem(name string) string {
	if name == "" {
		return "name is required: it is the name the token is stored and spoken of by, and the bundle key and every audit line are spelled from it"
	}
	if !ClaudeAccountNameRE.MatchString(name) {
		return fmt.Sprintf("name %q is not a token name: it must match %s, the rule a name the bundle key bitbucket-token--<name> is spelled from keeps (a doubled dash would name that key itself rather than a token)", pluginwire.Printable(name), ClaudeAccountNameRE)
	}
	return ""
}

// bitbucketTokenCoverageProblems is R1: the type is one of the three, the
// coverage says what the token reaches, and the two state the same fact. The
// type is checked first and on its own, so a record with no type is refused
// once with the sentence that names the three rather than once per coverage
// entry against a type nothing stated.
func bitbucketTokenCoverageProblems(tok BitbucketToken, at string) []Problem {
	var ps []Problem
	if !slices.Contains(BitbucketTokenTypes, tok.Type) {
		ps = append(ps, Problem{Path: at + ".type", Message: fmt.Sprintf("type must be one of %s: it says which call the token reaches, and coverage is read as the same fact", strings.Join(BitbucketTokenTypes, ", "))})
	}
	if len(tok.Coverage) == 0 {
		ps = append(ps, Problem{Path: at + ".coverage", Message: "coverage is required: it is what a request is matched against, and it never widens allow_data_to (D2354)"})
		return ps
	}
	for _, entry := range tok.Coverage {
		form, msg := bitbucketCoverageForm(entry)
		if msg != "" {
			ps = append(ps, Problem{Path: at + ".coverage", Message: msg})
			continue
		}
		if tok.Type != "" && form != tok.Type {
			ps = append(ps, Problem{Path: at + ".coverage", Message: fmt.Sprintf("%q is the coverage of a %s token and this record's type is %s: the two state one fact, and %s (R1)", pluginwire.Printable(entry), form, pluginwire.Printable(tok.Type), bitbucketCoverageRule(form))})
		}
	}
	return ps
}

// bitbucketCoverageRule is the coverage form one type is written with, as the
// clause a refusal ends in. It reads the TYPE rather than the entry, so a
// message always names the form the operator's own type asks for — the half
// they did not write — and never repeats the entry back at them.
func bitbucketCoverageRule(typ string) string {
	switch typ {
	case BitbucketTokenTypeRepo:
		return "a repository token's coverage is one repository as workspace/name"
	case BitbucketTokenTypeWorkspace:
		return "a workspace token's coverage is its workspace as workspace/*"
	case BitbucketTokenTypeProject:
		return "a project token's coverage is its workspace slug alone, beside the project's UUID or key"
	}
	return ""
}

// bitbucketCoverageForm is the token type one coverage entry states on its
// own, and the sentence saying why the entry states none when it is no
// coverage form at all. One function answers both because the form IS the
// type: a second reader that decided them apart could admit an entry for one
// type and match it as another, which is the failure R1 exists to refuse.
//
// The two-segment half is held to project_secrets' own alphabet
// (invalidAllowDataToEntry), for its reason and one more: the owner segment
// must be literal, so "*/api" — every workspace's repository — is not a
// coverage any token can state, and neither is the bare "*".
func bitbucketCoverageForm(entry string) (typ, msg string) {
	if entry == "" {
		return "", "a coverage entry names what the token reaches, and this one is empty"
	}
	segs := strings.Split(entry, "/")
	if len(segs) == 1 {
		if strings.Contains(entry, "*") || !repoPatternSegmentRE.MatchString(entry) {
			return "", fmt.Sprintf("%q is not a coverage entry: a project token's is its workspace slug alone, as in \"myworkspace\"", pluginwire.Printable(entry))
		}
		return BitbucketTokenTypeProject, ""
	}
	if msg := invalidAllowDataToEntry(entry); msg != "" {
		return "", fmt.Sprintf("%q is not a coverage entry: %s", pluginwire.Printable(entry), msg)
	}
	if segs[1] == "*" {
		return BitbucketTokenTypeWorkspace, ""
	}
	return BitbucketTokenTypeRepo, ""
}

// bitbucketTokenProjectProblems holds the project members to the one type
// that has a project. A project token needs one — its UUID, or its key for an
// operator who has only that (the add-time check resolves the key to the UUID
// and keeps it for display, T11) — and covers one project, so its coverage is
// the workspace slug alone: a second entry would claim a project's token for
// a workspace its UUID does not name. A repository or workspace token that
// carries either member is a record whose halves disagree, and it is refused
// rather than read as a project token with a misleading type.
func bitbucketTokenProjectProblems(tok BitbucketToken, at string) []Problem {
	var ps []Problem
	if tok.Type == BitbucketTokenTypeProject {
		if tok.ProjectUUID == "" && tok.ProjectKey == "" {
			ps = append(ps, Problem{Path: at + ".project_uuid", Message: "a project token names its project: set project_uuid — the stable identifier a repository's project is matched by, looked up with this token itself (R2) — or project_key for an operator who has only the key, which the add-time check resolves to the UUID"})
		}
		if len(tok.Coverage) != 1 {
			ps = append(ps, Problem{Path: at + ".coverage", Message: "a project token covers one project, so its coverage holds that project's workspace slug and nothing else"})
		}
		return ps
	}
	for _, m := range []struct{ field, value string }{
		{"project_uuid", tok.ProjectUUID},
		{"project_key", tok.ProjectKey},
	} {
		if m.value != "" {
			ps = append(ps, Problem{Path: at + "." + m.field, Message: fmt.Sprintf("only a project token names a project, and this record's type is %s: %s", pluginwire.Printable(tok.Type), bitbucketCoverageRule(tok.Type))})
		}
	}
	return ps
}
