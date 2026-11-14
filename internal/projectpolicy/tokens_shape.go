package projectpolicy

import (
	"fmt"
	"strings"
)

// The node's bitbucket_tokens key (R5) and the identity set the Bitbucket
// adapter compares a comment's author against (F20). Both are declared here
// rather than in the package that holds, checks and forwards a token, so that
// the two readers that want no token handling at all import none of it: the
// runner reads the key once at bootstrap (T16) and the adapter asks whether a
// comment is the run's own (T13), and neither of them should reach the code
// that carries a value.

// BitbucketTokensKey is the node's top-level key carrying each Bitbucket
// token's derived facts (R5). It is a top-level key and not a member of the
// bitbucket_token block, because that block's reader refuses an unknown member
// (validBitbucketTokenBlock) while every unknown TOP-LEVEL key is tolerated by
// a runner of the release behind (P8) and refused by its CLI. That asymmetry is
// the skew the plan states, and it is the same one the image_catalog key set
// (D1156).
const BitbucketTokensKey = "bitbucket_tokens"

// BitbucketTokenNode is one token's derived facts: what the operator stated in
// the local config, plus the identity the creds state store recorded for it. It
// carries no value and no secret version: the node is readable by every run
// token of the job, and a hostile agent reading it learns which repositories a
// token reaches and nothing it could use to take one (F21, R5).
type BitbucketTokenNode struct {
	// Name is the token's document key — bitbucket-token--<name>, or
	// bitbucket-token for the legacy one — and never the bare suffix, so no
	// name can collide with a model account (R5).
	Name string `json:"name"`
	// Type is the record's own type: repo, project or workspace (R1).
	Type string `json:"type"`
	// Coverage are the patterns the token may serve, in the node's spelling:
	// provider-qualified and matched against a canonical repository with
	// MatchRepo, held to ValidBitbucketTokenCoverage below ("bitbucket/acme/app",
	// "bitbucket/acme/*"). It is the same spelling a Record's Coverage is in, so
	// a record read off this node validates as it stands (R5, and the record's
	// own comment for why one spelling and not two).
	Coverage []string `json:"coverage"`
	// Identity is the account UUID GET /2.0/user answered for the token, and
	// it is empty for a repository token, which that endpoint refuses (R14,
	// F20), so the set is partial by design.
	Identity string `json:"identity,omitempty"`
}

// BitbucketTokens is the node's bitbucket_tokens key: one entry per typed
// token, derived from the local config's records alone. A project that names
// none publishes no key at all, so an installation that never stored a typed
// token keeps a node byte for byte what it was before this key existed.
type BitbucketTokens struct {
	Entries []BitbucketTokenNode `json:"entries"`
}

// ValidBitbucketTokenCoverage checks one coverage pattern of a Bitbucket token:
// the reach of ONE credential, in the node's provider-qualified spelling.
//
// It is ValidPattern — the rule this package decodes every allow_repos entry
// with — plus the three narrowings the bitbucket_token block's reader applies
// to an entry of its own list (validBitbucketTokenBlock): the bare "*" that
// reaches every repository of every provider, a first segment that is not
// "bitbucket", a wildcard owner, which would let one credential reach every
// workspace, and more than two wildcards, dc73's own bound on one
// allow_data_to entry. A project's enrollment may be as wide as "*"; a
// credential's reach may not, and that difference is the whole reason these
// narrowings exist rather than ValidPattern alone.
//
// It is exported, and it is here rather than in the package that holds the
// record, because the record and the node must be held to ONE rule: the legacy
// untyped record's coverage IS this block's allow_repos list (R3), so a rule
// the block accepts and the record refuses would drop an installation that
// holds only the legacy key, and one the record accepts and the block refuses
// would publish a node the next run cannot read back.
func ValidBitbucketTokenCoverage(pattern string) error {
	if err := ValidPattern(pattern); err != nil {
		return err
	}
	if pattern == "*" {
		return fmt.Errorf("%q reaches every repository of every provider, and a Bitbucket token reaches one workspace's: name the workspace (\"bitbucket/workspace-a/*\")", pattern)
	}
	segs := strings.Split(pattern, "/")
	if segs[0] != "bitbucket" {
		return fmt.Errorf("%q does not name the bitbucket provider: every pattern of a Bitbucket token is a bitbucket repository (\"bitbucket/workspace-a/repo-a\"), and MatchRepo compares the first segment, so neither another provider nor \"*\" would ever admit one", pattern)
	}
	if strings.Contains(segs[1], "*") {
		return fmt.Errorf("%q has a wildcard owner, and the owner segment must be literal: a wildcard owner would let this one credential reach every workspace, so name the workspace", pattern)
	}
	if n := strings.Count(pattern, "*"); n > maxBitbucketTokenStars {
		return fmt.Errorf("%q has %d \"*\"; at most %d are allowed (a pattern with more is a typo, not an intent)", pattern, n, maxBitbucketTokenStars)
	}
	return nil
}

// BitbucketTokenIdentities is the set of account UUIDs Fugaro's own Bitbucket
// tokens have, as the node's bitbucket_tokens key carries them (F20). The
// adapter treats a comment whose author is one of these as the run's own,
// compared by UUID exactly as selfUser compares a user's UUID, so a run served
// by several tokens in turn still knows its own activity from a person's.
//
// The set can be partial and can be empty: a repository token has no identity
// (R14), and a node without the key leaves the adapter as it is today.
type BitbucketTokenIdentities []string
