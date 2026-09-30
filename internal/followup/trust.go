// Package followup decides which pull request comments a follow-up run
// acts on, bounds and redacts them, and builds the follow-up's prompts and
// its comments.json. Every function here but NewNonce, which reads
// crypto/rand, is pure: no I/O, no logging.
package followup

import (
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
)

// Trust is who may steer a follow-up: an author listed in the base
// branch's followup.trusted who is also a collaborator (GitHub's
// author_association; always true on Bitbucket), and never the pull
// request's author, which on a pull request Fugaro opened is Fugaro's own
// identity.
type Trust struct {
	IDs        map[string]bool
	PRAuthorID string // Fugaro's own identity on a PR it opened: always dropped as self
}

// NewTrust is the trust rule for pull request pr under the base branch's
// followup settings f. A nil or empty trusted list trusts nobody.
func NewTrust(f config.Followup, pr gitprov.PRInfo) Trust {
	ids := make(map[string]bool, len(f.Trusted))
	for _, id := range f.Trusted {
		if id != "" {
			ids[id] = true
		}
	}
	return Trust{IDs: ids, PRAuthorID: pr.AuthorID}
}

// Allows reports whether c's author may steer the follow-up. When the
// pull request's author is unknown, nobody may: Fugaro's own identity
// can't then be told apart from a listed ID.
func (t Trust) Allows(c gitprov.Comment) bool {
	if t.PRAuthorID == "" || c.AuthorID == "" || t.isSelf(c) {
		return false
	}
	return t.IDs[c.AuthorID] && c.Collaborator
}

// isSelf reports whether c was posted by Fugaro's identity: the adapter
// says so, or the author is the pull request's author. The second holds
// even when the identity lookup failed, so the repository token's
// identity, which every first run's agent holds, can never steer a
// follow-up.
func (t Trust) isSelf(c gitprov.Comment) bool {
	return c.Self || (t.PRAuthorID != "" && c.AuthorID == t.PRAuthorID)
}
