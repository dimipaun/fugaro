package projectpolicy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
)

// BitbucketToken is the policy's bitbucket_token block (D473, D474, T2.1 as
// amended by docs/secrets-brokering D493): the repositories the project's
// Bitbucket credential may reach, and whether the credentials service
// forwards it for them.
//
// It is derived from project_secrets.bitbucket-token.allow_data_to in the
// project's local config, and it is derived whether or not a profile mounts
// the token: a proxied run is launched in a job that does not hold the
// credential at all, so the block is that job's admission source and not a
// statement about a mount (D493; the credentials service's own check reads
// it, G3).
//
// It carries no value and no secret name: the node is readable by every run
// token of the job, and a prompt-injected agent can take that token through
// /proc (§5(2)).
type BitbucketToken struct {
	// AllowRepos are the canonical repositories (Canonical) the token may
	// reach, provider-qualified and matched with MatchRepo. An entry may carry
	// dc73's segment wildcards ("bitbucket/workspace-a/*", D464), which is how
	// one local-config entry serves a whole workspace; the owner segment is
	// always literal, and neither "*" nor "bitbucket/*/*" is accepted however
	// the node was written (validBitbucketTokenBlock).
	AllowRepos []string `json:"allow_repos"`
	// Proxy is the operator's per-installation switch (D493, G3): true when
	// the credentials service forwards this token for the repositories above
	// instead of mounting it on their jobs. It is the operator's, never a
	// profile's, because it is one decision for the installation.
	Proxy bool `json:"proxy,omitempty"`
}

// bitbucketTokenMembers is the member names the block is spelled with, read
// from the struct's own json tags the way profileMembers is (carrierMembers),
// so a field this type gains is held to its spelling without a second list.
var bitbucketTokenMembers = carrierMembers(reflect.TypeOf(BitbucketToken{}))

// SharedBitbucketProfile reports whether a profile is a Bitbucket shared job:
// it serves the bitbucket provider and mounts the Bitbucket token (D473).
// The provider half is what makes the mount meaningful — a github profile
// mounting the token is refused where the profiles block is judged
// (validProfiles) — and both halves are read off the carrier
// `fugaro update` deploys a job from, so the deployer and the node agree
// about which profile is one.
func SharedBitbucketProfile(p config.JobProfile) bool {
	return p.Provider == "bitbucket" && slices.Contains(p.Secrets, config.BitbucketSecret)
}

// DeriveBitbucketToken is the policy's bitbucket_token block, derived from the
// operator's local config: the one anchor, one edit rule, as DeriveAccounts is
// for the accounts block.
//
// The translation is spelled out because the two documents spell a repository
// differently. config's project_secrets.bitbucket-token.allow_data_to holds
// owner/name slugs, held to dc73's own rules (an entry may use "*" inside a
// segment, never a wildcard owner and never the bare "*"). A policy's
// allow_repos entry is provider-qualified, because the reader has to decide a
// repository of one provider. So an exact slug goes through Canonical, the one
// function the matcher and the allow-list are built from, and a pattern cannot:
// task.CanonicalRepo refuses "*", so a pattern is qualified here — the
// bitbucket prefix and both segments lower-cased, because a canonical
// repository is lower-case and MatchRepo compares segment against segment
// case-sensitively, so an entry written "workspace-a/API-*" has to derive
// "bitbucket/workspace-a/api-*" to admit what its author meant.
//
// Every derived entry is then held to ValidPattern, the rule the node's own
// reader applies to it, because the two rules are not the same rule: config's
// allow_data_to alphabet is case-folded (repoPatternSegmentRE) and admits a
// ".git" suffix, and a segment longer than MaxPatternLen passes it too. An
// entry ValidPattern refuses would be a node Decode refuses — every run of the
// project halted by a registry entry `fugaro validate` accepted — so it is
// refused here instead, where the message can name the operator's entry.
//
// The block is derived whether or not a profile mounts the token, and nil only
// when there is nothing to derive it from: no project_secrets.bitbucket-token
// entry at all, or one whose list is empty ("shared with nobody", §3). Every
// run reads the node and a proxied run's whole admission is this block, so the
// two states that mean "this project's token reaches no repository" are the
// two that publish no block (D493).
//
// It returns Canonical's and ValidPattern's errors for one entry and
// ValidateProjectSecrets' for the list as a whole, so a config that would
// publish a node this binary cannot read back is refused here instead.
//
// profiles is taken and not read, and that is the amendment rather than an
// oversight: the block is derived whether or not a profile mounts the token,
// and a caller derives every block from the same pair of inputs (publish hands
// the same profiles to DeriveAccounts). Reading them here is what the
// pre-amendment derivation did, and it is the reason a proxied repository with
// no mounting profile had no admission.
func DeriveBitbucketToken(profiles map[string]Profile, secrets map[string]config.ProjectSecret) (*BitbucketToken, error) {
	entry, ok := secrets[config.BitbucketSecret]
	if !ok || len(entry.AllowDataTo) == 0 {
		return nil, nil
	}
	if ps := config.ValidateProjectSecrets(map[string]config.ProjectSecret{config.BitbucketSecret: entry}); len(ps) > 0 {
		return nil, fmt.Errorf("projectpolicy: %s", ps[0].String())
	}
	var list []string
	for _, slug := range entry.AllowDataTo {
		var canonical string
		if strings.Contains(slug, "*") {
			owner, name, _ := strings.Cut(strings.ToLower(slug), "/")
			canonical = "bitbucket/" + owner + "/" + name
		} else {
			c, err := Canonical("bitbucket", slug)
			if err != nil {
				return nil, fmt.Errorf("projectpolicy: project_secrets.%s.allow_data_to holds %q, which is not a repository: %v — an entry here is a repository as owner/name", config.BitbucketSecret, slug, err)
			}
			canonical = c
		}
		if err := ValidPattern(canonical); err != nil {
			return nil, fmt.Errorf("projectpolicy: project_secrets.%s.allow_data_to holds %q, which would publish the policy entry %q, and the node refuses that entry: %v — name the repositories this token reaches, as owner/name, in lower case", config.BitbucketSecret, slug, canonical, err)
		}
		if !slices.Contains(list, canonical) {
			list = append(list, canonical)
		}
	}
	slices.Sort(list)
	return &BitbucketToken{AllowRepos: list, Proxy: entry.Proxy}, nil
}

// decodeBitbucketTokenBlock reads the block off the node, refusing a member
// this Fugaro does not know and a member name that folds onto another, for the
// reason decodeAccountBlock does it for the accounts block: a misspelt
// allow_repos is not a dropped field but an enforced one gone quiet — the
// nil slice would read as "the token reaches no repository", which is the
// state a hand-written node uses to take the token away from a repository —
// and a misspelt proxy is the operator's forwarder switch left off while the
// node appears to state it. Unknown *top-level* keys keep the P8 tolerance; a
// newer field inside this block bumps Version, which is what refuses a node
// this binary cannot read.
func decodeBitbucketTokenBlock(raw json.RawMessage) (*BitbucketToken, error) {
	names, err := objectMemberNames(raw)
	if err != nil {
		return nil, fmt.Errorf("projectpolicy: bitbucket_token: %w", err)
	}
	if err := checkMemberNames("bitbucket_token", names, bitbucketTokenMembers); err != nil {
		return nil, fmt.Errorf("projectpolicy: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var b BitbucketToken
	if err := dec.Decode(&b); err != nil {
		return nil, fmt.Errorf("projectpolicy: bitbucket_token: %w", err)
	}
	return &b, nil
}

// maxBitbucketTokenStars is dc73's own bound on one allow_data_to entry,
// which this block keeps: at most two wildcards, one per segment in practice.
// internal/config counts the same way (invalidAllowDataToEntry).
const maxBitbucketTokenStars = 2

// validBitbucketTokenBlock holds the block to the rules of D464, whoever wrote
// it: a written block names at least one repository, each of them a bitbucket
// repository, well-formed, with a literal owner segment and at most two
// wildcards.
//
// The narrower rules are the point. MatchRepo would accept "*" and
// "bitbucket/*/*" in an allow_repos list — a project's enrollment may be that
// wide — but this block is one credential's reach, and a wildcard owner is
// every workspace: the widening D464 refuses for project_secrets
// (invalidAllowDataToEntry, whose validation DeriveBitbucketToken applies to
// the operator's own list) is refused here too, because a node is
// hand-writable and the block is what the credentials service will later
// forward the token for (G3).
//
// The two stars are dc73's own bound, and they are counted the way that rule
// counts them: "bitbucket/workspace-a/api-*" is one entry serving a family of
// repositories, while more than two is a typo rather than an intent.
func validBitbucketTokenBlock(b *BitbucketToken) error {
	if len(b.AllowRepos) == 0 {
		return fmt.Errorf("projectpolicy: bitbucket_token.allow_repos: a written bitbucket_token block must name at least one repository: an absent or empty list reads as \"the token reaches no repository\", which is the state a hand-written node uses to take the token away from every repository by accident — remove the whole block to say that, or name the repositories")
	}
	for i, pattern := range b.AllowRepos {
		at := fmt.Sprintf("projectpolicy: bitbucket_token.allow_repos[%d]", i)
		if err := ValidPattern(pattern); err != nil {
			return fmt.Errorf("%s: %w", at, err)
		}
		if pattern == "*" {
			return fmt.Errorf("%s: \"*\" is not accepted: %s reaches one owner's repositories, so name the workspace (\"bitbucket/workspace-a/*\") rather than every repository of every provider", at, config.BitbucketSecret)
		}
		segs := strings.Split(pattern, "/")
		// ValidPattern admits "*" and every provider kind in the first segment;
		// this block admits only the one provider it is the reach of. A
		// "github/…" entry is dead here (MatchRepo compares segment against
		// segment, and the entries it is matched against are bitbucket
		// canonicals) and a "*" first segment reads as "this token may reach
		// repositories of any provider", which is not what a Bitbucket
		// installation's token does. Both are refused rather than left dead.
		if segs[0] != "bitbucket" {
			return fmt.Errorf("%s: %q does not name the bitbucket provider: every entry of this block is a bitbucket repository (\"bitbucket/workspace-a/repo-a\"), and MatchRepo compares the first segment, so neither another provider nor \"*\" would ever admit one", at, pattern)
		}
		if strings.Contains(segs[1], "*") {
			return fmt.Errorf("%s: %q has a wildcard owner, and the owner segment must be literal: a wildcard owner would let this one credential reach every workspace, so name the workspace", at, pattern)
		}
		if n := strings.Count(pattern, "*"); n > maxBitbucketTokenStars {
			return fmt.Errorf("%s: %q has %d \"*\"; at most %d are allowed (a pattern with more is a typo, not an intent)", at, pattern, n, maxBitbucketTokenStars)
		}
	}
	return nil
}

// ValidBitbucketAPIRoots checks the node's owner-allow-listed Bitbucket API
// roots (D474, T2.8): each one an https URL that reaches one host, without a
// query, a fragment or userinfo.
//
// The three refusals are one property from three sides. An http root sends the
// token in clear; a query or a fragment means the string is not the root of an
// API but a request against one, and a root is matched exactly after trimming a
// trailing "/" — so an entry carrying either would match nothing and would look
// like an allow-list with no effect; and userinfo is a credential written into
// the policy node, which every run token can read (§5(2)).
//
// Exported for the one caller outside this package that has to apply it: doctor
// holds the roots a local file lists to it before it sends the bundle's token
// to one, because that file is not where a root is validated — the publish that
// builds the node is — so an entry only it spells would otherwise take the
// credential in clear.
func ValidBitbucketAPIRoots(roots []string) error {
	for i, root := range roots {
		at := fmt.Sprintf("projectpolicy: bitbucket_api_roots[%d]", i)
		u, err := url.Parse(root)
		if err != nil {
			return fmt.Errorf("%s: %q is not a URL", at, root)
		}
		if u.Scheme != "https" {
			return fmt.Errorf("%s: %q must be an https URL: the runner sends the Bitbucket token to this root, and http would send it in clear", at, root)
		}
		if u.Host == "" {
			return fmt.Errorf("%s: %q names no host: an API root is an origin such as \"https://api.bitbucket.org\"", at, root)
		}
		if u.User != nil {
			return fmt.Errorf("%s: %q carries userinfo, and this node is readable by every run token of the project: a credential belongs in a secret, never in the policy", at, root)
		}
		if u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("%s: %q carries a query or a fragment: an API root is the origin the runner builds a request against, and it is matched exactly (after one trailing \"/\"), so this entry would match nothing", at, root)
		}
	}
	return nil
}
