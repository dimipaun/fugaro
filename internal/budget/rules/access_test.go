package rules

// The grants node's half of the rules fence (RS2, D1585 as amended, F28): no
// run token and no signed-in user reads config/admin/access, and the node sits
// where the document already denies every client rather than under
// config/policy/shared, whose `.read: TOKEN` is what would let each run's agent
// read the whole roster.

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// governingPaths returns every generated location that governs one node: the
// literal ancestors, the node itself, and every location that reaches it through
// a wildcard segment. The last are the ones a literal walk cannot see and that
// matter most here: `config/admin/$key` is a sibling of the node and the parent
// of everything beside it at once, so one `.read` there re-opens the family the
// config/admin rule closes while every literal path stays clean.
//
// It walks rulesTree itself rather than using generatedPaths, because that
// helper resolves `$slug` to the slug a run's token carries — what its other
// callers want — and that resolution would leave a `$slug` wildcard sibling
// invisible to this walk.
func governingPaths(t *testing.T, node string) map[string]map[string]any {
	t.Helper()
	segs := strings.Split(node, "/")
	out := map[string]map[string]any{}
	walk(rulesTree(t), nil, func(path []string, attrs map[string]any) {
		if len(path) == 0 || len(path) > len(segs) {
			return
		}
		for i, s := range path {
			if s != segs[i] && !strings.HasPrefix(s, "$") {
				return
			}
		}
		out[strings.Join(path, "/")] = attrs
	})
	return out
}

// TestNoRunTokenReadsTheAccessNode: the role and scope pairs live at
// config/admin/access, under the config/admin node whose `.read` and `.write`
// are the booleans false. RTDB rules cascade from the shallowest location that
// carries one, so the nearest generated ancestor is what governs the node, and
// a boolean false there denies every client identity at once — a run's token
// (TOKEN), a signed-in user's (USER) and a phone's alike. The test is written
// against access.PathNode rather than a literal so the Go constant and the
// generated document cannot drift: move the node to config/policy/shared and
// the ancestor's read becomes the TOKEN expression this test refuses.
//
// This is the half the emulator cannot state in the job that runs this file
// (the emulator suite is behind the `firebase` build tag); it proves the
// absence of a grant, which is what makes the denial true for every client at
// once. The read and the conditional write of the node are internal/access's
// own tests.
//
// RS2 adds no `config/admin/access` entry to the template: its Files line makes
// the entry conditional on the existing config/admin rule not already denying
// the node, and that rule — `.read: false, .write: false`, cascading to every
// child — already does. This test is what decides that, and what fails if a
// later edit moves the node or opens the subtree, whether along the node's
// literal path or through a wildcard segment that reaches it.
//
// The migration marker beside the node (access.PathMigrated) is held to the
// same proof rather than assumed to inherit it. It is a second name under the
// same subtree, so a later edit can move or re-open one without the other, and
// what it decides is a security answer of its own (RS6c): a client that could
// write it would turn an installation whose roster is empty into one whose
// lists have never been migrated, and the CLI reads that second state as
// "restrict nothing" (internal/cli's accessJudge). A client that could read it
// learns which installations hold a roster at all, which is the same disclosure
// reading the node is.
func TestNoRunTokenReadsTheAccessNode(t *testing.T) {
	for _, p := range []struct {
		field string
		path  string
		want  string
	}{
		{"access.PathNode", access.PathNode, "config/admin/access"},
		{"access.PathMigrated", access.PathMigrated, "config/admin/access_migrated"},
	} {
		if p.path != p.want {
			t.Fatalf("%s = %q, want %s: sitting under the subtree the document denies is the security property (F28)", p.field, p.path, p.want)
		}
		if p.path == projectpolicy.PathShared() || strings.HasPrefix(p.path, projectpolicy.PathShared()+"/") {
			t.Fatalf("%s = %q is under %s, whose `.read: TOKEN` lets every run's agent read the whole roster (F28)", p.field, p.path, projectpolicy.PathShared())
		}
	}

	paths := generatedPaths(t)
	for _, node := range []string{access.PathNode, access.PathMigrated} {
		assertNoClientReachesTheFencedPath(t, paths, node)
	}

	// The contrast that makes the placement matter: config/policy/shared, where
	// the first recommendation would have put the node, is readable by any token
	// of the project. Pinned here so a reader sees the two rules side by side.
	shared, ok := paths[projectpolicy.PathShared()]
	if !ok {
		t.Fatalf("the template does not generate /%s", projectpolicy.PathShared())
	}
	if r, _ := shared[".read"].(string); !strings.Contains(r, "auth.token.fp") {
		t.Errorf("/%s .read = %v, want the TOKEN grant that makes it the wrong home for the roster (F28)", projectpolicy.PathShared(), shared[".read"])
	}
}

// assertNoClientReachesTheFencedPath holds one path under the denied subtree to
// the whole proof: the nearest generated ancestor denies every client, and
// nothing that governs the path — an ancestor, the path itself, or a location
// that reaches it through a wildcard segment — grants a client read or write.
func assertNoClientReachesTheFencedPath(t *testing.T, paths map[string]map[string]any, node string) {
	t.Helper()
	// The nearest generated ancestor: config/admin. Every location between it
	// and the path carries no rule of its own, so this is the rule the database
	// evaluates for a read of it.
	segs := strings.Split(node, "/")
	ancestor := ""
	var attrs map[string]any
	for i := len(segs) - 1; i >= 1; i-- {
		if a, ok := paths[strings.Join(segs[:i], "/")]; ok {
			ancestor, attrs = strings.Join(segs[:i], "/"), a
			break
		}
	}
	if ancestor != projectpolicy.PathAdmin() {
		t.Fatalf("the nearest generated ancestor of %s is %q, want %q: it must sit under the node the document denies to every client", node, ancestor, projectpolicy.PathAdmin())
	}
	for _, a := range []string{".read", ".write"} {
		v, ok := attrs[a]
		if !ok || v != false {
			t.Errorf("/%s %s = %v, want false: a boolean false denies every client — a run token and a signed-in user included — so nothing reads or writes the grants", ancestor, a, v)
		}
	}

	// And nothing that governs the path grants a client read or write: an
	// ancestor or the path itself, where a later edit that added an explicit
	// child grant would re-open the family the ancestor closes, and every
	// location that reaches the path through a wildcard segment. Every value
	// that is not the boolean false counts, because the two shapes a grant is
	// written in are an expression (a string) and the boolean true; a walk that
	// looked only for strings would pass a `{ ".read": true }` added here.
	gov := governingPaths(t, node)
	govPaths := make([]string, 0, len(gov))
	for p := range gov {
		govPaths = append(govPaths, p)
	}
	slices.Sort(govPaths)
	for _, p := range govPaths {
		for _, attr := range []string{".read", ".write"} {
			if v, has := gov[p][attr]; has && v != false {
				t.Errorf("/%s %s = %v: no client token may read or write %s", p, attr, v, node)
			}
		}
	}
}
