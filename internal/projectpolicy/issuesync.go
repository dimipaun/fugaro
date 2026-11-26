package projectpolicy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/dimipaun/fugaro/internal/task"
)

// IssueSync is the project's issue adapter (D2733, D2719, D2720, D2742): the
// repositories whose open issues the orchestrator's daemon ingests into the
// project's backlog, and whether an item of the project with no spec of its own
// opens an issue in return.
//
// It is a top-level key of the published node, and F46's correction is why it
// is not a member of a block: a block of this node decodes strictly
// (DisallowUnknownFields) and the node is re-read while runs are in flight (the
// credentials service reads it per model call), so a member added inside one
// would make a reader of the release behind refuse the WHOLE node — and that
// refusal stops the runs an update promises to leave alone. Every unknown
// top-level key is listed in Policy.UnknownKeys and tolerated (P8), so a reader
// of the release behind reads every key it knows and goes on, which is the same
// argument F35 settled for the switch beside it.
//
// It is not a `localcfg` key either, which is the other half of F46: any
// machine of the project may hold the daemon's lease (D2700), so the adapter is
// a property of the project rather than of the machine that happens to run it —
// and the local config already carries an unrelated, local-only `issues` block,
// the nudge for Fugaro's own product bugs (T-IS15).
//
// The pointer on Policy is what tells a written block from silence: a project
// that configures no adapter writes no member at all, so its node is byte for
// byte what it was before this key existed.
type IssueSync struct {
	GitHub *IssueSyncGitHub `json:"github,omitempty"`
}

// IssueSyncGitHub is the GitHub half of the adapter: the repositories whose
// issues are ingested, and the mirror switch.
//
// Mirror is a plain bool and not a pointer, which is the one place this block
// differs from the switches beside it on Policy (BakeRepos, Triage,
// OrchestratorEnabled): each of those defaults to true, so its absent member
// and its written `true` are one statement and the pointer is what keeps a
// written `false` from reading as silence. Here the default is false and the
// zero value is false, so an absent member and a written `false` are one
// statement already — and the writer spells it once, by writing no member.
//
// Turning it on is the widening direction (D2719): an unapproved, spec-less
// item with no external id then opens an issue in the owner's repository. That
// is a decision for the write's own door to fence, not this shape's.
type IssueSyncGitHub struct {
	// Repos are the repositories whose open issues are ingested, each as the
	// canonical repository name (owner/name, task.CanonicalRepo). The spelling
	// is held to the canonical form because the adapter lists and opens issues
	// by it: an entry in another case, or with a trailing ".git", is a
	// repository no lookup reaches, silently — the rule the repository-to-image
	// map (`repo_images`) holds its keys to, for the same reason.
	Repos []string `json:"repos,omitempty"`
	// Mirror is whether an unapproved, spec-less item with no external id opens
	// an issue of its own (D2719). False is the default and means it does not:
	// the backlog stays internal until the owner says otherwise.
	Mirror bool `json:"mirror,omitempty"`
}

// MirrorOn reports whether an item of this project with no spec and no external
// id opens an issue of its own. A nil block — a project that configures no
// adapter, or one whose policy carries no issue_sync member — answers false,
// so the reader never has to tell "no adapter" from "mirror off".
func (g *IssueSyncGitHub) MirrorOn() bool {
	return g != nil && g.Mirror
}

// SetsNothing reports whether the block decides nothing: it names no repository
// and does not turn the mirror on. A block with no repository has nothing to
// sync — the daemon walks the repositories this block names and no others — and
// the mirror switch is a decision about the items of those repositories, so a
// block naming neither is no block at all. That is what keeps the node of a
// project that writes an empty one byte for byte what it was, the promise every
// optional member of this node keeps.
func (b *IssueSync) SetsNothing() bool {
	return b == nil || b.GitHub == nil || (len(b.GitHub.Repos) == 0 && !b.GitHub.Mirror)
}

// issueSyncMembers and issueSyncGitHubMembers are the two blocks' own member
// spelling, read from the structs' json tags (carrierMembers) so a field added
// to either is known without a second list to keep in step.
var (
	issueSyncMembers       = carrierMembers(reflect.TypeOf(IssueSync{}))
	issueSyncGitHubMembers = carrierMembers(reflect.TypeOf(IssueSyncGitHub{}))
)

// decodeIssueSync reads the block off the node.
//
// The document is held to the three rules the strict decode alone cannot make.
// Every member is spelled as the carrier spells it, nested block included
// (checkMemberNames): encoding/json folds a member name by Unicode simple case
// folding before it matches one, so a node writing `Mirror` would set mirror
// and which member this Fugaro read would depend on the order the keys were
// written in. No repository is named twice (checkNoDuplicateNames): a list
// decoded into a slice keeps both entries, and the daemon would then ingest one
// repository's issues twice. And every entry is a canonical repository name
// (validIssueSync), because an entry the adapter cannot look up is a repository
// whose issues are silently never ingested.
func decodeIssueSync(raw json.RawMessage) (*IssueSync, error) {
	if err := checkIssueSyncMembers(raw); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var b IssueSync
	if err := dec.Decode(&b); err != nil {
		return nil, fmt.Errorf("projectpolicy: issue_sync: %w", err)
	}
	if err := validIssueSync(&b); err != nil {
		return nil, err
	}
	// A block that decides nothing is no block (SetsNothing), so the two
	// spellings of "this project configures no adapter" — the member left out
	// and the member written empty — are one answer, and a writer that round
	// trips this policy writes no member for it.
	if b.SetsNothing() {
		return nil, nil
	}
	return &b, nil
}

// checkIssueSyncMembers holds the block's member names, and the names inside
// its github block, to the spellings the carriers write. A member neither
// carrier has at all is refused by the strict decode that follows; this is what
// refuses one that is a modelled member in another case, and one written twice.
func checkIssueSyncMembers(raw json.RawMessage) error {
	names, err := objectMemberNames(raw)
	if err != nil {
		return fmt.Errorf("projectpolicy: issue_sync: %w", err)
	}
	if err := checkMemberNames("issue_sync", names, issueSyncMembers); err != nil {
		return fmt.Errorf("projectpolicy: %w", err)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return fmt.Errorf("projectpolicy: issue_sync: %w", err)
	}
	graw, ok := members["github"]
	if !ok || IsNull(graw) {
		return nil
	}
	gnames, err := objectMemberNames(graw)
	if err != nil {
		return fmt.Errorf("projectpolicy: issue_sync.github: %w", err)
	}
	if err := checkMemberNames("issue_sync.github", gnames, issueSyncGitHubMembers); err != nil {
		return fmt.Errorf("projectpolicy: %w", err)
	}
	return nil
}

// validIssueSync holds the block's own values to their rules: each repository
// is named once and as the canonical owner/name the adapter looks it up by.
//
// The name's rule is task.CanonicalRepo's, which is what the CLI canonicalizes
// a --repo to and what the repository-to-image map (`repo_images`) holds its
// keys' paths to, so a name this node carries and a name a command writes are
// judged by one rule. It is stated as the canonical form rather than as a
// shape: a key in another case is a repository no lookup reaches, and the
// refusal names the spelling that is read.
func validIssueSync(b *IssueSync) error {
	if b == nil || b.GitHub == nil {
		return nil
	}
	if err := checkNoDuplicateNames("issue_sync.github.repos", b.GitHub.Repos); err != nil {
		return fmt.Errorf("projectpolicy: %w", err)
	}
	for i, repo := range b.GitHub.Repos {
		if c, err := task.CanonicalRepo(repo); err != nil || c != repo {
			return fmt.Errorf("projectpolicy: issue_sync.github.repos[%d]: %q is not a repository name as the adapter reads one: an entry is the owner/name whose issues are ingested, written in its canonical form (as acme/app), because a name in another case or with a trailing .git is a repository no lookup reaches", i, repo)
		}
	}
	return nil
}
