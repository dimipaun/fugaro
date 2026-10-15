package projectpolicy

import "fmt"

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
	pathShared = "config/policy/shared"
	pathRepos  = "config/policy/repos/"
	pathAdmin  = "config/admin"
)

// PathShared is the node whose existence *is* project-centric mode (§5): the
// runner tests this child, because a read of config/policy itself is denied by
// the per-child rules (an RTDB read grant cascades to children, so a read on
// the parent would make every child readable by every run token).
func PathShared() string { return pathShared }

// PathRepo is one repository's policy child. Only the token whose slug is
// $slug may read it, the way config/caps/repos/$slug works.
func PathRepo(slug string) string { return pathRepos + seg("slug", slug) }

// PathAdmin is the admin-only node (§5): the rules deny client reads and
// writes, but launchers hold roles/firebasedatabase.viewer and an IAM read
// bypasses the rules (12a item 7), so nothing secret-adjacent may live there.
func PathAdmin() string { return pathAdmin }

// seg escapes one slug for a path and panics on an empty one: an empty segment
// would collapse the path onto a different node. Slugs are validated long
// before a path is built, so an empty one is a programming error. The escaping
// is budget.Key's (TestPathsEscapeASlug pins the table), so a slug is spelled
// the same way in config/caps/repos/$slug and here, and the rules'
// auth.token.fs claim (budget.Key of the slug) compares equal.
func seg(what, v string) string {
	if v == "" {
		panic("projectpolicy: empty " + what + " in a database path")
	}
	return escKey(v)
}

// escKey escapes one path segment for RTDB. RTDB forbids . $ # [ ] / and ASCII
// control characters in keys; each becomes %XX (uppercase hex), and % itself
// is escaped so that no two inputs collide. It is byte-for-byte budget.Key
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
