package projectpolicy

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
	// Coverage are the patterns the token may serve, provider-qualified the
	// way allow_repos entries are and matched with MatchRepo (D464).
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

// BitbucketTokenIdentities is the set of account UUIDs Fugaro's own Bitbucket
// tokens have, as the node's bitbucket_tokens key carries them (F20). The
// adapter treats a comment whose author is one of these as the run's own,
// compared by UUID exactly as selfUser compares a user's UUID, so a run served
// by several tokens in turn still knows its own activity from a person's.
//
// The set can be partial and can be empty: a repository token has no identity
// (R14), and a node without the key leaves the adapter as it is today.
type BitbucketTokenIdentities []string
