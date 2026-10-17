package projectpolicy

import (
	"fmt"
	"strconv"
	"strings"
)

// Node paths of the project policy, relative to the database root, without a
// leading slash and already key-escaped (the policy half of the paths
// internal/budget/model.go holds for the caps). Callers pass the raw slug.
//
// The rules template spells the same paths as JSON literals; the test helpers
// policyPaths and generatedPaths, and TestTemplatePolicyPathsAgreeWithProjectpolicy,
// in internal/budget/rules keep the two from drifting. That is also where the
// escaping is compared with budget.Key itself (TestPolicyPathsEscapeASlugLikeBudgetKey):
// this package does not import internal/budget.
const (
	pathRulesMarker = "config/limits/policyRules"
	pathShared      = "config/policy/shared"
	pathRepos       = "config/policy/repos/"
	pathAdmin       = "config/admin"
)

// PathShared is the node whose existence *is* project-centric mode (§5): the
// runner tests this child, because a read of config/policy itself is denied by
// the per-child rules (an RTDB read grant cascades to children, so a read on
// the parent would make every child readable by every run token).
func PathShared() string { return pathShared }

// PathRepo is one repository's policy child. Only the token whose slug is
// $slug may read it, the way config/caps/repos/$slug works.
func PathRepo(slug string) string { return pathRepos + seg("slug", slug) }

// PathRepos is the parent of the per-repository children, without a trailing
// slash. A read of it is what lists the slugs a policy has children for — a
// read of the *parent* is denied by the rules (they are per child, and a read
// grant cascades, so a read on the parent would expose every child), which is
// why this is for the operator's own credentials (the CLI's
// `policy publish --delete`, which removes the children it wrote) and not for
// a run token.
func PathRepos() string { return strings.TrimSuffix(pathRepos, "/") }

// PathRulesMarker is the node recording which generation of the security
// rules is deployed (RulesVersion). It sits beside the largest lease under
// config/limits, which every config/limits writer must leave alone: the node
// is read-modify-written, never replaced, so a marker an older CLI does not
// know survives (internal/cli's budget set, TestSetMaxReserveKeepsOtherLimitKeys).
func PathRulesMarker() string { return pathRulesMarker }

// PathAdmin is the admin-only node (§5): the rules deny client reads and
// writes, but launchers hold roles/firebasedatabase.viewer and an IAM read
// bypasses the rules (12a item 7), so nothing secret-adjacent may live there.
func PathAdmin() string { return pathAdmin }

// seg escapes one slug for a path and panics on an empty one: an empty segment
// would collapse the path onto a different node. Slugs are validated long
// before a path is built, so an empty one is a programming error. The escaping
// is budget.Key's (internal/budget/rules's
// TestPolicyPathsEscapeASlugLikeBudgetKey compares the two), so a slug is spelled
// the same way in config/caps/repos/$slug and here, and the rules'
// auth.token.fs claim (budget.Key of the slug) compares equal.
func seg(what, v string) string {
	if v == "" {
		panic("projectpolicy: empty " + what + " in a database path")
	}
	return escKey(v)
}

// escKey escapes one key for RTDB — a path segment, or the member name of an
// object the node holds (a model ID under model_prices, see Prices). RTDB
// forbids . $ # [ ] / and ASCII control characters in keys; each becomes %XX
// (uppercase hex), and % itself is escaped so that no two inputs collide and
// unescKey inverts escKey. It is byte-for-byte budget.Key
// (internal/budget/model.go), which cannot be imported here.
func escKey(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '%' || c == '.' || c == '$' || c == '#' || c == '[' || c == ']' || c == '/' || c < 0x20 || c == 0x7f {
			b = append(b, fmt.Sprintf("%%%02X", c)...)
			continue
		}
		b = append(b, c)
	}
	return string(b)
}

// unescKey inverts escKey. It is what reads a key this package wrote (a
// model_prices member name) back into the value it was made from. A key
// without an escape in it is returned unchanged, so a node written by hand
// with a real model ID or slug in it is still read as that: only "%" starts an
// escape, and escKey escapes every "%" it meets.
func unescKey(s string) (string, error) {
	var b []byte
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			b = append(b, s[i])
			continue
		}
		if i+3 > len(s) {
			return "", fmt.Errorf("the key ends in a truncated escape")
		}
		v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
		if err != nil {
			return "", fmt.Errorf("the key has a bad escape %q", s[i:i+3])
		}
		b = append(b, byte(v))
		i += 2
	}
	return string(b), nil
}
