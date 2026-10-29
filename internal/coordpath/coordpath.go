// Package coordpath holds the paths of the coordinator's database subtree.
//
// The subtree itself is the coordinator's, but the paths are the core's: the
// project rules grant a read on PathRoot and the rules' tests assert it, and
// the doctor check that reads the generation marker those rules were generated
// with names the same prefix. All three are core and cannot follow the
// coordinator's library out of it. D195 (answered 2026-10-21) is the decision
// the paths come from: a new top-level `coordinator/` subtree, project-wide
// facts under `coordinator/project/` and per-repository facts under
// `coordinator/repos/<slug>/`, every document keyed by a deterministic ID.
//
// The library imports this package rather than carrying a second copy of the
// names, so the subtree has one definition and the rules cannot drift from the
// code that reads them.
package coordpath

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"

	"github.com/dimipaun/fugaro/internal/budget"
)

// The subtree's own paths. PathRoot is the prefix the rules grant a read on,
// and nothing the coordinator writes may sit outside it.
const (
	// PathRoot is the coordinator's top-level subtree.
	PathRoot = "coordinator"
	// PathProject is where the project-wide facts live.
	PathProject = PathRoot + "/project"
	// PathRepos is the parent of one child per repository, keyed by the
	// repository's slug as budget.Key escapes it.
	PathRepos = PathRoot + "/repos"
)

// PathRepo is one repository's node: the parent of every document about it.
func PathRepo(slug string) string { return PathRepos + "/" + budget.Key(slug) }

// PathProjectDocument is one project-wide document, and PathRepoDocument one
// repository's. id comes from DocumentID.
func PathProjectDocument(id string) string { return PathProject + "/" + id }

// PathRepoDocument is one repository's document, under that repository.
func PathRepoDocument(slug, id string) string { return PathRepo(slug) + "/" + id }

// DocumentID is the database key of one coordinator document, derived from the
// parts that describe the thing it is about and from nothing else. No clock,
// no counter and no random source goes in: the same thing described twice is
// the same key, which is how "the same event delivered twice is harmless" is
// satisfied without an Idempotency-Key header (finding #17) and why the same
// report twice writes one document rather than two.
//
// The parts are length-prefixed before hashing, so ("a", "bc") and ("ab", "c")
// are different documents. The answer is lower-case hex: a Realtime Database
// key may not hold a dot, a dollar, a hash, a bracket or a slash, and hex makes
// each of those unrepresentable rather than escaped — the same reason
// budget.Key exists, for keys that have to stay readable.
func DocumentID(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(strconv.Itoa(len(p))))
		h.Write([]byte{':'})
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}
