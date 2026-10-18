package projectpolicy

import (
	"encoding/json"
	"fmt"
	"sort"
)

// CloneURLs is the clone_urls block of a policy: the clone URL the project
// gives a repository, keyed by the canonical repository name ("<provider>/
// <owner>/<name>", Canonical).
//
// It is a map in Go and an object on the node, but its *keys* are not
// canonical names on the node, for the reason Prices gives for model IDs: RTDB
// refuses a key containing "." or "/" — the write is rejected whole, with a 400
// and nothing stored — and every canonical name carries two slashes
// (projectpolicy.Canonical's own spelling, and the spelling allow_repos and
// task.Slug use). So the block is stored under escKey(canonical) and unescKey
// reads it back, exactly as model_prices is stored under escKey(model ID). The
// decoded map, which is what every reader in Go sees — the runner's
// p.CloneURL(canonical) lookup, the CLI's diff and `policy show` — keeps the
// canonical spelling.
//
// The escape lives in MarshalJSON and UnmarshalJSON rather than in a step a
// writer has to remember, so every encoding of a Policy is a document RTDB
// accepts and the two directions cannot drift apart. A key with no escape in it
// is read as it is, so a node written by hand where a person spells a name
// without a slash stays readable; the escaped form is only what this package
// writes.
//
// What a value may be is not this type's business but the node's: validCloneURLs
// holds every entry to repourl.CloneURL — this repository, on this provider's
// host, with no credentials — because the URL is where a run clones from, while
// its slug, its locks, its cache and its budget session all name the repository
// the run's own task.json does.
type CloneURLs map[string]string

// MarshalJSON writes the block as the node holds it: one member per repository,
// under escKey(canonical). It is a value receiver, so a nil CloneURLs marshals
// to an empty object exactly as a nil map does.
func (cs CloneURLs) MarshalJSON() ([]byte, error) {
	out := make(map[string]string, len(cs))
	for canonical, url := range cs {
		out[escKey(canonical)] = url
	}
	return json.Marshal(out)
}

// UnmarshalJSON reads the block off a node: one member per repository,
// unescaped back into the canonical name. The member name is in the error, so a
// refusal names the entry rather than only the key.
//
// The block's own keys are held to a rule of their own, exactly as model_prices
// is (Prices.UnmarshalJSON): one repository may be named once, whether it is
// repeated under one key or written under two spellings of its name
// (checkNoDuplicateNames, then the unescaped canonical names). The two
// spellings are not hypothetical — the escape is only what this package writes,
// and a key with no escape in it is read as it is, so a hand-written node
// carrying "github/acme/app" beside the "github%2Facme%2Fapp" this package
// writes folds both members onto one canonical repository. Which of the two
// URLs survived would then be whichever member the map's iteration happened to
// see last: a node whose *validation* (validCloneURLs, entry by entry) and
// whose run's clone URL both depend on Go's map order, so the same bytes are
// accepted on one read and refused on the next. A repository named twice is
// refused rather than read, and the keys are walked in sorted order so the
// refusal names the same pair every time.
func (cs *CloneURLs) UnmarshalJSON(raw []byte) error {
	var entries map[string]string
	if err := json.Unmarshal(raw, &entries); err != nil {
		return err
	}
	names, err := objectMemberNames(raw)
	if err != nil {
		return err
	}
	if err := checkNoDuplicateNames("clone_urls", names); err != nil {
		return fmt.Errorf("projectpolicy: %w", err)
	}
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys) // a stable order, so the refusal below names the same pair every time
	out := make(CloneURLs, len(entries))
	spelled := make(map[string]string, len(entries)) // canonical name -> the key it was written under
	for _, key := range keys {
		canonical, err := unescKey(key)
		if err != nil {
			return fmt.Errorf("%q: %w", key, err)
		}
		if first, dup := spelled[canonical]; dup {
			return fmt.Errorf("%q and %q are both the repository %q, and a repository may be named once: the clone URL a run uses would otherwise depend on the order the block's keys are in", first, key, canonical)
		}
		spelled[canonical] = key
		out[canonical] = entries[key]
	}
	*cs = out
	return nil
}
