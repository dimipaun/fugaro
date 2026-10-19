package projectpolicy

import (
	"encoding/json"
	"fmt"
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
func (cs *CloneURLs) UnmarshalJSON(raw []byte) error {
	var entries map[string]string
	if err := json.Unmarshal(raw, &entries); err != nil {
		return err
	}
	out := make(CloneURLs, len(entries))
	for key, url := range entries {
		canonical, err := unescKey(key)
		if err != nil {
			return fmt.Errorf("%q: %w", key, err)
		}
		out[canonical] = url
	}
	*cs = out
	return nil
}
