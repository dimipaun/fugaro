package projectpolicy

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/task"
)

// RepoImages is the repo_images block of a policy: the catalog entry each of
// the project's repositories runs, keyed by the repository's canonical name
// ("<provider>/<owner>/<name>", Canonical — the spelling allow_repos patterns
// and task.Slug already use, so one repository has one spelling in this node
// whatever document names it).
//
// It is the successor of ImageMap, and a member of its own rather than the same
// member with new keys (D3371): a key that meant owner/name must not start
// meaning provider/owner/name under the same name, because an older reader
// would then read a provider as an owner and map the wrong repository — it
// sees no map at all here and refuses loudly instead (R4).
//
// It is a map in Go and an object on the node, but its *keys* are not canonical
// names on the node, for the reason CloneURLs gives: RTDB refuses a key
// containing "/" — the write is rejected whole, with a 400 and nothing stored —
// and every canonical name carries two slashes. So the block is stored under
// escKey(canonical) and unescKey reads it back. The decoded map, which is what
// every reader in Go sees, keeps the canonical spelling.
//
// The escape lives in MarshalJSON and UnmarshalJSON rather than in a step a
// writer has to remember, so every encoding of a Policy is a document RTDB
// accepts and the two directions cannot drift apart. A key with no escape in it
// is read as it is, so a node written by hand where a person spells a name
// without a slash stays readable; the escaped form is only what this package
// writes.
type RepoImages map[string]string

// MarshalJSON writes the block as the node holds it: one member per repository,
// under escKey(canonical). It is a value receiver, so a nil RepoImages marshals
// to an empty object exactly as a nil map does — and omitempty then drops the
// member, which is what keeps a project that maps nothing byte for byte what its
// node was before the key existed.
func (m RepoImages) MarshalJSON() ([]byte, error) {
	out := make(map[string]string, len(m))
	for canonical, entry := range m {
		out[escKey(canonical)] = entry
	}
	return json.Marshal(out)
}

// UnmarshalJSON reads the block off a node: one member per repository,
// unescaped back into the canonical name, exactly as CloneURLs reads its own.
//
// The block's keys are held to two rules of their own beside the unescape: no
// member name twice (checkNoDuplicateNames, on the keys as the document spells
// them) and no two keys unescaping to one repository, because the image a
// repository's jobs run would otherwise depend on the order the block's keys
// are in. The two spellings are not hypothetical — the escape is only what this
// package writes, and a key with no escape in it is read as it is — so a
// hand-written node carrying "github/acme/app" beside the escaped form this
// package writes folds both members onto one repository.
//
// A value that is not a string is an error too: the member is a catalog entry's
// name, and a number there would be an image no selection can look up.
func (m *RepoImages) UnmarshalJSON(raw []byte) error {
	names, err := objectMemberNames(raw)
	if err != nil {
		return err
	}
	if err := checkNoDuplicateNames("repo_images", names); err != nil {
		return err
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return err
	}
	out := make(RepoImages, len(entries))
	spelled := make(map[string]string, len(entries)) // canonical name -> the key it was written under
	for _, key := range sortedStringKeys(entries) {
		canonical, err := unescKey(key)
		if err != nil {
			return fmt.Errorf("%q: %w", key, err)
		}
		if first, dup := spelled[canonical]; dup {
			return fmt.Errorf("%q and %q are both the repository %q, and a repository may be mapped once: the image its jobs run would otherwise depend on the order the block's keys are in", first, key, canonical)
		}
		spelled[canonical] = key
		var entry string
		if err := json.Unmarshal(entries[key], &entry); err != nil {
			return fmt.Errorf("%q: %w", canonical, err)
		}
		out[canonical] = entry
	}
	*m = out
	return nil
}

// ValidRepoImages checks the repository-to-image map, entry by entry: each key
// the canonical name of a repository at a provider of task.ValidProvider's
// shape, and each value the name of a catalog entry (config.ValidCatalogName,
// the rule an entry's own name and a workflow's image_name are held to).
//
// The provider is held to its SHAPE here and not to config.Providers, which is
// the whole of R10: a key at a provider this release does not serve is kept and
// skipped by the readers that enumerate keys (an unknown provider serves no
// repository), never refused — a later release that adds a provider would
// otherwise make every older reader of the node refuse it, the credentials
// service among them, and the node is re-read while runs are in flight. The
// writers hold a provider to config.Providers themselves, so nothing of this
// release writes a key outside them.
//
// The key's rule is stated as the canonical form rather than as a shape, for
// ValidImageMap's reason: the value a writer produces is Canonical's output, so
// a key in another case or another form is an entry no lookup reaches — a
// mapping an operator set for github/acme/app that never applies to
// GitHub/Acme/App, silently. A nested old-form key such as acme/team/app reads
// as the provider acme and passes here, which is also R10: it serves nothing,
// because no repository is enrolled at a provider that does not exist, and
// refusing it would refuse a node a later release may have written for real.
func ValidRepoImages(m map[string]string) error {
	for _, key := range sortedStringKeys(m) {
		at := fmt.Sprintf("projectpolicy: repo_images[%s]", key)
		provider, repo, ok := strings.Cut(key, "/")
		if !ok || !task.ValidProvider(provider) || !canonicalRepoPath(repo) {
			return fmt.Errorf("%s: a repo_images key is <provider>/<owner>/<name>, as github/acme/app: it is the spelling a job's repository is looked up by, so a key in another form is one no lookup reaches; write it with fugaro image map set <provider>/<owner>/<name> <entry>", at)
		}
		if !config.ValidCatalogName(m[key]) {
			return fmt.Errorf("%s: %s", at, config.CatalogNameMessage)
		}
	}
	return nil
}

// EffectiveRepoImages is the map the node serves a repository's image from, and
// which member it read it from (R2).
//
// repo_images wins over the legacy image_map, per member and never per key
// (D3372, F16): a node that carries the new member answers it whatever the old
// one holds, and a node that carries only the old one answers that, read as it
// is read today. A per-key fallback to the old member would let a map written by
// a CLI from before L11 revive the collision this lift exists to end.
//
// fromRepoImages says which half the answer is: true when the keys are
// provider/owner/name (RepoImages) and false when they are bare repository
// names (ImageMap). A nil policy, and a node that carries neither member, answer
// (nil, false), which is the same "the project maps nothing" a written empty
// member reads as.
//
// It is one function and not a lookup so that the rule is written once: the
// credentials service reads the node through it (S1, F25), catalog.ImageRequest
// is filled from it, and internal/localcfg's effective reader builds on it
// (T0b), so no two of them can disagree about which member a node serves.
func (p *Policy) EffectiveRepoImages() (images map[string]string, fromRepoImages bool) {
	if p == nil {
		return nil, false
	}
	if len(p.RepoImages) > 0 {
		return p.RepoImages, true
	}
	return p.ImageMap, false
}

// canonicalRepoPath reports whether repo is already the canonical repository
// path: CanonicalRepo's own answer, unchanged. It is the same rule ValidImageMap
// applies to a bare key, stated once for a key that carries a provider in front
// of it.
func canonicalRepoPath(repo string) bool {
	c, err := task.CanonicalRepo(repo)
	return err == nil && c == repo
}
