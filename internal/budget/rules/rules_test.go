package rules

import (
	"bytes"
	"encoding/json"
	"maps"
	"math/rand"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/coordpath"
	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runstore"
)

const goldenPath = "testdata/rules.golden.json"

// parentChain matches one run of .parent() calls. deeperBy lengthens every
// run by exactly k, so a rule copied k locations deeper compares equal only
// when each of its chains climbs exactly k further -- not merely "some" -- and
// a chain that stops one parent() short still differs.
var parentChain = regexp.MustCompile(`(?:\.parent\(\))+`)

func deeperBy(rule string, k int) string {
	extra := strings.Repeat(".parent()", k)
	return parentChain.ReplaceAllStringFunc(rule, func(run string) string { return run + extra })
}

// shallowerByOne drops one parent() from every run: the rule a by-tier leaf
// would carry if its chains reached one location short of the node they mean.
func shallowerByOne(rule string) string {
	return parentChain.ReplaceAllStringFunc(rule, func(run string) string { return strings.TrimSuffix(run, ".parent()") })
}

// TestRulesGolden pins the generated rules. Regenerate on purpose with
// UPDATE_GOLDEN=1 go test ./internal/budget/rules and review the diff: it is
// security code.
func TestRulesGolden(t *testing.T) {
	got, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("generated rules differ from %s (UPDATE_GOLDEN=1 regenerates; review the diff)", goldenPath)
	}
	again, _ := Generate()
	if !bytes.Equal(got, again) {
		t.Fatal("Generate is not deterministic")
	}
}

// rulesTree decodes the generated rules into maps (order is irrelevant here).
func rulesTree(t *testing.T) map[string]any {
	t.Helper()
	b, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("generated rules are not JSON: %v", err)
	}
	if len(doc) != 1 || doc["rules"] == nil {
		t.Fatalf("top level must be exactly {\"rules\": ...}, got %v", keys(doc))
	}
	return doc["rules"]
}

func keys[V any](m map[string]V) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

// walk visits every location of the rule tree with its path and attributes.
func walk(n map[string]any, path []string, fn func(path []string, attrs map[string]any)) {
	attrs := map[string]any{}
	for k, v := range n {
		if strings.HasPrefix(k, ".") {
			attrs[k] = v
		}
	}
	fn(path, attrs)
	for k, v := range n {
		if m, ok := v.(map[string]any); ok && !strings.HasPrefix(k, ".") {
			walk(m, append(append([]string(nil), path...), k), fn)
		}
	}
}

// macro words that must not survive generation.
var macroWord = regexp.MustCompile(`\b[A-Z][A-Z_]{2,}\b|\b[OND]\(`)

func TestGeneratedRulesParse(t *testing.T) {
	rules := rulesTree(t)
	count := 0
	walk(rules, nil, func(path []string, attrs map[string]any) {
		for k, v := range attrs {
			switch k {
			case ".read", ".write", ".validate":
			default:
				t.Errorf("/%s: unexpected attribute %s", strings.Join(path, "/"), k)
			}
			switch e := v.(type) {
			case bool:
			case string:
				count++
				if strings.TrimSpace(e) == "" {
					t.Errorf("/%s %s is empty", strings.Join(path, "/"), k)
				}
				if strings.Count(e, "(") != strings.Count(e, ")") || strings.Count(e, "'")%2 != 0 {
					t.Errorf("/%s %s has unbalanced parentheses or quotes: %s", strings.Join(path, "/"), k, e)
				}
			default:
				t.Errorf("/%s %s is %T", strings.Join(path, "/"), k, v)
			}
		}
	})
	if count < 50 {
		t.Fatalf("only %d rule expressions generated", count)
	}
	// Default deny at the root: no read or write grant above the locations.
	if rules[".read"] != false || rules[".write"] != false {
		t.Fatalf("root must deny by default, got read=%v write=%v", rules[".read"], rules[".write"])
	}
}

// TestMacrosExpandOnce: no macro text survives, and expansion is stable (the
// output holds nothing the expander would expand again).
func TestMacrosExpandOnce(t *testing.T) {
	walk(rulesTree(t), nil, func(path []string, attrs map[string]any) {
		for k, v := range attrs {
			s, ok := v.(string)
			if !ok {
				continue
			}
			if m := macroWord.FindString(s); m != "" {
				t.Errorf("/%s %s still holds the macro %q", strings.Join(path, "/"), k, m)
			}
		}
	})
	for _, tc := range []struct {
		in    string
		depth int
	}{
		{"NEW", 3}, {"RUN", 2}, {"D(spend/TODAY/runs/$slug/$run/reserved)", 4}, {"COUNTERS_FOLLOW", 6}, {"RUNDELTA", 5},
	} {
		once, err := expand(tc.in, tc.depth)
		if err != nil {
			t.Fatal(err)
		}
		twice, err := expand(once, tc.depth)
		if err != nil {
			t.Fatal(err)
		}
		if once != twice {
			t.Errorf("%s: expanding the expansion changes it:\n%s\n%s", tc.in, once, twice)
		}
	}
	if _, err := expand("NOSUCHMACRO", 1); err == nil {
		t.Error("an unknown macro must fail generation, not pass through")
	}
	if _, err := expand("N(a/b'c)", 1); err == nil {
		t.Error("a quote in a path segment must fail generation")
	}
}

// TestNewDataChainsReachTheRoot: N() and D() climb exactly as many parents as
// the location is deep, or they would read the wrong node.
func TestNewDataChainsReachTheRoot(t *testing.T) {
	for depth := 1; depth <= 6; depth++ {
		s, err := expand("N(x)", depth)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Count(s, ".parent()"); got != 2*depth {
			t.Errorf("depth %d: %d parent() calls in %s, want %d", depth, got, s, 2*depth)
		}
	}
}

// TestWritesOnlyAtNumericLeaves pins the structural security property of the
// ledgers and counters: no .write above a leaf, so a whole-node write or a
// delete of a field is denied (validation does not run on deletes).
func TestWritesOnlyAtNumericLeaves(t *testing.T) {
	rules := rulesTree(t)
	guarded := map[string]bool{"runs": true, "spend": true}
	walk(rules, nil, func(path []string, attrs map[string]any) {
		if len(path) == 0 {
			return
		}
		w, has := attrs[".write"]
		where := "/" + strings.Join(path, "/")
		switch path[0] {
		case "config", "fugaro":
			if has && w != false {
				t.Errorf("%s must never be writable by a run: .write = %v", where, w)
			}
		case "runs", "spend":
			if !guarded[path[0]] {
				return
			}
			leaf := path[len(path)-1]
			isLeaf := map[string]bool{"reserved": true, "released": true, "spent": true, "notional": true, "overrun": true, "tokens": true,
				"exp": true, "counted": true, "calls": true, "micros": true, "in": true, "out": true, "cr": true, "cw": true,
				"rep": true, "tlo": true, "thi": true}[leaf]
			if has && !isLeaf {
				t.Errorf("%s has a .write above a leaf: %v", where, w)
			}
			if isLeaf {
				ws, _ := w.(string)
				if !strings.Contains(ws, "newData.exists()") {
					t.Errorf("%s: a leaf .write must refuse deletes (newData.exists()): %v", where, w)
				}
				if _, ok := attrs[".validate"]; !ok {
					t.Errorf("%s: a leaf needs a .validate", where)
				}
			}
		}
	})
}

// TestPerRunOK pins the lease's per-run comparison (design D6): one boolean
// reading all three sides, so that a side that is not set does not bound the
// run -- the global cap is the fallback of a repository that has none, the
// default is the fallback of a global one -- and the both-unset branch is
// false, so the rules deny (fail closed).
func TestPerRunOK(t *testing.T) {
	ok, err := perRunOK(4) // the depth of runs/$slug/$run/reserved
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ok, ": false)") {
		t.Errorf("no false branch: with no per-run cap anywhere the rules must deny: %s", ok)
	}
	if !strings.Contains(ok, ": true)") {
		t.Errorf("no true branch: a repository with a cap of its own and no global one must pass: %s", ok)
	}
	for _, side := range []string{
		"root.child('config/caps/repos').child($slug).child('perRunMicros')",
		"root.child('config/caps/defaults/repoPerRunMicros')",
		"root.child('config/caps/global/perRunMicros')",
	} {
		if !strings.Contains(ok, side) {
			t.Errorf("the comparison does not read %s: %s", side, ok)
		}
	}
	if strings.Count(ok, "(") != strings.Count(ok, ")") || strings.Count(ok, "'")%2 != 0 {
		t.Errorf("unbalanced parentheses or quotes: %s", ok)
	}
	if m := macroWord.FindString(ok); m != "" {
		t.Errorf("the expansion still holds the macro %q: %s", m, ok)
	}
	twice, err := expand(ok, 4)
	if err != nil || twice != ok {
		t.Errorf("expanding the expansion changes it (%v):\n%s\n%s", err, ok, twice)
	}

	// It is the whole enforce branch of the lease's reserved validator: the
	// rules compare the per-run caps nowhere else.
	var validate string
	walk(rulesTree(t), nil, func(path []string, attrs map[string]any) {
		if strings.Join(path, "/") == "runs/$slug/$run/reserved" {
			validate, _ = attrs[".validate"].(string)
		}
	})
	if validate == "" {
		t.Fatal("the lease's reserved validator was not found")
	}
	if !strings.Contains(validate, "(!(root.child('config/mode').val() == 'enforce') || "+ok+")") {
		t.Errorf("the reserved validator's enforce branch is not perRunOK:\n%s", validate)
	}
}

// TestGoMirrorOfThePerRunBranches: the intended branches of the per-run
// comparison, written out in Go and checked against budget.Caps.PerRun for
// every combination of the three sides and a range of amounts.
//
// It does not read the generated expression, so it does not prove the rules
// agree with the client: TestPerRunOK pins the expression's shape, and the
// emulator suite (TestRulesParity and the directed rows) proves the
// agreement. This one runs everywhere, without the emulator, and makes a
// change of intent visible.
func TestGoMirrorOfThePerRunBranches(t *testing.T) {
	m := func(v int64) *budget.Micros { x := budget.Micros(v); return &x }
	// mirror is perRunOK read as prose: the repository's own cap, else the
	// default, and the global one where it is set; with neither side set the
	// comparison is false (the rules deny).
	mirror := func(repo, def, global *budget.Micros, delta budget.Micros) bool {
		if repo != nil || def != nil {
			own := repo
			if own == nil {
				own = def
			}
			return delta <= *own && (global == nil || delta <= *global)
		}
		return global != nil && delta <= *global
	}
	for _, sides := range []struct {
		name          string
		repo, def, gl *budget.Micros
	}{
		{"none", nil, nil, nil},
		{"repo", m(30), nil, nil},
		{"default", nil, m(10), nil},
		{"global", nil, nil, m(20)},
		{"repo+default", m(30), m(10), nil},
		{"repo+global", m(30), nil, m(20)},
		{"default+global", nil, m(10), m(20)},
		{"repo+default+global", m(30), m(10), m(20)},
	} {
		t.Run(sides.name, func(t *testing.T) {
			c := budget.Caps{}
			if sides.repo != nil {
				c.Repo = &budget.RepoCaps{PerRunMicros: sides.repo}
			}
			if sides.def != nil {
				c.Defaults = &budget.DefaultCaps{RepoPerRunMicros: sides.def}
			}
			if sides.gl != nil {
				c.Global = &budget.GlobalCaps{PerRunMicros: sides.gl}
			}
			cap, ok := c.PerRun()
			for _, delta := range []budget.Micros{0, 5, 10, 20, 30, 50} {
				if got, want := mirror(sides.repo, sides.def, sides.gl, delta), ok && delta <= cap; got != want {
					t.Errorf("delta %d: the rules' branches say %v, Caps.PerRun %d %v says %v", delta, got, cap, ok, want)
				}
			}
		})
	}
}

// TestGoMirrorOfTheDayKeys: the expression the rules use for TODAY is the same
// arithmetic as budget.Day, checked with a Go mirror around the UTC midnight
// (the emulator has no settable clock, so the boundary cannot be exercised
// there; TestTodayExpression runs the live half).
func TestGoMirrorOfTheDayKeys(t *testing.T) {
	g, _ := Generate()
	if !strings.Contains(string(g), `('' + ((now - now % 86400000) / 86400000))`) {
		t.Fatal("the TODAY expression changed: update the mirror below with it")
	}
	mirror := func(nowMs float64) string { // '' + ((now - now % 86400000) / 86400000)
		r := nowMs - float64(int64(nowMs)%86400000)
		return trimInt(r / 86400000)
	}
	for _, at := range []time.Time{
		time.Date(2026, 10, 2, 23, 59, 59, 999_000_000, time.UTC),
		time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 3, 0, 0, 0, 1_000_000, time.UTC),
		time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2030, 12, 31, 23, 59, 59, 999_000_000, time.UTC),
	} {
		if got, want := mirror(float64(at.UnixMilli())), budget.DayKey(budget.Day(at)); got != want {
			t.Errorf("%s: rules say %q, budget.Day says %q", at, got, want)
		}
	}
}

func trimInt(f float64) string {
	return strings.TrimSuffix(strings.TrimSuffix(formatFloat(f), ".0"), ".")
}

func formatFloat(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

// ---- The policy nodes (docs/project-centric/spec.md §5, phase 1 task 1.2).
// These assertions run without the emulator, on the generated JSON; the emulator
// suite (TestAllowedPaths, TestDenialTable, build tag firebase) proves the same
// shape against the real evaluator.

// slugForPath stands in for the template's $slug when a policy path is
// compared with the generated rules (the template is JSON and holds the paths
// as literals; the wildcard is the only difference). It, and policyPaths below,
// live here rather than in rules.go: nothing outside these tests builds a
// policy path, and production code that imported internal/projectpolicy only to
// spell paths for a test pulled the policy package into the rules package.
const slugForPath = "x"

// policyPaths returns the policy's node paths as the template spells them, in
// the Go the policy uses: the shared node, one repository's child (with
// slugForPath in place of $slug) and the admin-only node. A rename in
// internal/projectpolicy updates both or fails
// TestTemplatePolicyPathsAgreeWithProjectpolicy (phase 1 task 1.2 step 3).
func policyPaths() (shared, repo, admin string) {
	return projectpolicy.PathShared(), projectpolicy.PathRepo(slugForPath), projectpolicy.PathAdmin()
}

// TestPolicyPathsEscapeASlugLikeBudgetKey: a slug is escaped exactly as
// budget.Key escapes it, so config/policy/repos/$slug and the rules'
// auth.token.fs claim agree — and it is compared with budget.Key itself, not
// with a hand-written copy of its table, so the two cannot drift while the
// copy stays green. The random strings exercise the escapes the table does not
// name.
func TestPolicyPathsEscapeASlugLikeBudgetKey(t *testing.T) {
	for _, slug := range []string{
		"acme-app-0123456789abcdef", "acme-app-0123", "a.b", "a$b", "a#b[c]d", "a/b", "100%", "\x01", "\x7f",
	} {
		if got, want := projectpolicy.PathRepo(slug), "config/policy/repos/"+budget.Key(slug); got != want {
			t.Errorf("PathRepo(%q) = %q, want %q (budget.Key)", slug, got, want)
		}
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 200; i++ {
		b := make([]byte, rng.Intn(24))
		for j := range b {
			b[j] = byte(rng.Intn(256))
		}
		slug := string(b)
		if slug == "" {
			continue
		}
		if got, want := projectpolicy.PathRepo(slug), "config/policy/repos/"+budget.Key(slug); got != want {
			t.Fatalf("PathRepo(%q) = %q, want %q (budget.Key)", slug, got, want)
		}
	}
	if got, want := projectpolicy.PathRepo(slugForPath), "config/policy/repos/"+slugForPath; got != want {
		t.Errorf("PathRepo(%q) = %q, want %q: the model slug the other tests compare with is its own key", slugForPath, got, want)
	}
	if projectpolicy.PathShared() != "config/policy/shared" || projectpolicy.PathAdmin() != "config/admin" {
		t.Errorf("PathShared/PathAdmin = %q/%q", projectpolicy.PathShared(), projectpolicy.PathAdmin())
	}
}

// generatedPaths renders every location of the generated rules as a
// slash-separated path (attributes left out), with the template's $slug
// replaced by slugForPath, so a path built by internal/projectpolicy can be
// compared with what the template generated.
func generatedPaths(t *testing.T) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	walk(rulesTree(t), nil, func(path []string, attrs map[string]any) {
		if len(path) == 0 {
			return
		}
		segs := make([]string, len(path))
		for i, s := range path {
			if s == "$slug" {
				s = slugForPath
			}
			segs[i] = s
		}
		out[strings.Join(segs, "/")] = attrs
	})
	return out
}

// TestTemplatePolicyPathsAgreeWithProjectpolicy: the template holds the policy
// paths as JSON literals and internal/projectpolicy builds them in Go. The
// two cannot drift: a rename updates both or fails here (phase 1 task 1.2
// step 3).
func TestTemplatePolicyPathsAgreeWithProjectpolicy(t *testing.T) {
	shared, repo, admin := policyPaths()
	if shared != "config/policy/shared" || repo != "config/policy/repos/"+slugForPath || admin != "config/admin" {
		t.Fatalf("PolicyPaths = (%q, %q, %q), want config/policy/shared, config/policy/repos/x, config/admin", shared, repo, admin)
	}
	paths := generatedPaths(t)
	// The voice policy node's paths are the same agreement one family over
	// (P5.3.1): internal/projectpolicy builds them in Go, the template spells
	// them, and this package names their parent.
	for _, p := range []string{shared, repo, admin, PathVoice,
		projectpolicy.PathVoiceShared(), projectpolicy.PathVoiceRepos(), projectpolicy.PathVoiceRepo(slugForPath)} {
		if _, ok := paths[p]; !ok {
			t.Errorf("the template does not generate /%s, which internal/projectpolicy names", p)
		}
	}
}

// TestPolicyNodesAreReadPerChild pins §5(2) without the emulator: a TOKEN read
// on config/policy itself would cascade to every child, so each child carries
// its own grant and the parent carries none. config/admin is denied to a
// client entirely.
func TestPolicyNodesAreReadPerChild(t *testing.T) {
	paths := generatedPaths(t)
	shared, repo, admin := policyPaths()

	if attrs, ok := paths["config/policy"]; !ok {
		t.Fatal("the template does not generate /config/policy")
	} else {
		for _, a := range []string{".read", ".write"} {
			if v, ok := attrs[a]; ok {
				t.Errorf("/config/policy has %s = %v: an RTDB read grant on the parent cascades to every child", a, v)
			}
		}
	}
	if attrs, ok := paths["config/policy/repos"]; ok {
		for _, a := range []string{".read", ".write"} {
			if v, ok := attrs[a]; ok {
				t.Errorf("/config/policy/repos has %s = %v: the grant must be per $slug child", a, v)
			}
		}
	} else {
		t.Error("the template does not generate /config/policy/repos")
	}

	// The shared node: any token of the project, and read-only.
	attrs := paths[shared]
	read, _ := attrs[".read"].(string)
	if read == "" {
		t.Errorf("/%s has no .read: every run token must read the shared policy", shared)
	} else {
		if !strings.Contains(read, "auth.token.fp") {
			t.Errorf("/%s .read = %q, want the TOKEN claim check", shared, read)
		}
		if strings.Contains(read, "auth.token.fs") {
			t.Errorf("/%s .read = %q: the shared node is not slug-bound", shared, read)
		}
	}
	if w, ok := attrs[".write"]; !ok || w != false {
		t.Errorf("/%s .write = %v, want false: only IAM writes config/*", shared, w)
	}

	// A repository's node: the token's own slug only, and read-only.
	attrs = paths[repo]
	read, _ = attrs[".read"].(string)
	if !strings.Contains(read, "auth.token.fs == $slug") {
		t.Errorf("/%s .read = %q, want the auth.token.fs == $slug claim (as config/caps/repos/$slug has)", repo, read)
	}
	if w, ok := attrs[".write"]; !ok || w != false {
		t.Errorf("/%s .write = %v, want false", repo, w)
	}

	// The admin node: denied outright, in the rules. The rules are not what
	// keeps it from a launcher (IAM reads bypass them, §12a item 7), so the
	// assertion here is only that no client token gets in.
	attrs = paths[admin]
	if r, ok := attrs[".read"]; !ok || r != false {
		t.Errorf("/%s .read = %v, want false", admin, r)
	}
	if w, ok := attrs[".write"]; !ok || w != false {
		t.Errorf("/%s .write = %v, want false", admin, w)
	}

	// Nothing anywhere under config is writable by a client token, policy
	// included: admin writes are IAM-only.
	for p, a := range paths {
		if p != "config" && !strings.HasPrefix(p, "config/") {
			continue
		}
		if w, ok := a[".write"]; ok && w != false {
			t.Errorf("/%s .write = %v, want false under config", p, w)
		}
	}
}

// TestVoicePolicyNodesAreReadPerChild pins §4.3 of the voice design (mobile
// plan task P5.3.1) without the emulator: the voice subtree is two children of
// config/policy/voice, each carrying config/policy's own grant for its level,
// and the parent carries none — because a TOKEN read there would cascade to
// every project's allow-list at once.
//
// It is the parity test as well, from the other side: the template spells the
// three paths as JSON literals and internal/projectpolicy builds them in Go
// (PathVoiceShared, PathVoiceRepo, PathVoiceRepos), so a rename updates both or
// fails here. The parent has no function of its own — PathVoice is this
// package's, and it is the path the template spells.
//
// What the emulator adds is the refusal of a *token*: a run token for one slug
// reads its own node and is refused its sibling's, and no client writes either.
// This proves the document says so.
func TestVoicePolicyNodesAreReadPerChild(t *testing.T) {
	paths := generatedPaths(t)
	shared := projectpolicy.PathVoiceShared()
	repo := projectpolicy.PathVoiceRepo(slugForPath)
	repos := projectpolicy.PathVoiceRepos()
	if PathVoice != "config/policy/voice" || shared != PathVoice+"/shared" ||
		repos != PathVoice+"/repos" || repo != repos+"/"+slugForPath {
		t.Fatalf("the voice paths disagree: PathVoice = %q, shared = %q, repos = %q, repo = %q", PathVoice, shared, repos, repo)
	}
	for _, p := range []string{PathVoice, shared, repos, repo} {
		if _, ok := paths[p]; !ok {
			t.Errorf("the template does not generate /%s, which this package and internal/projectpolicy name", p)
		}
	}

	// The parent: no grant at all, of its own.
	if attrs, ok := paths[PathVoice]; !ok {
		t.Fatalf("the template does not generate /%s", PathVoice)
	} else {
		for _, a := range []string{".read", ".write"} {
			if v, ok := attrs[a]; ok {
				t.Errorf("/%s has %s = %v: an RTDB read grant on the parent cascades to every project's voice policy", PathVoice, a, v)
			}
		}
	}
	// The children: the policy's own grants, field for field, and read-only.
	for _, c := range []struct {
		p, want string
	}{{shared, "auth.token.fp"}, {repo, "auth.token.fs == $slug"}} {
		attrs, ok := paths[c.p]
		if !ok {
			continue
		}
		read, _ := attrs[".read"].(string)
		if !strings.Contains(read, c.want) {
			t.Errorf("/%s .read = %q, want the %s claim (config/policy's own grant for that level)", c.p, read, c.want)
		}
		if strings.Contains(read, "auth.token.mu") {
			t.Errorf("/%s .read = %q carries USER: the phone holds no read of config/policy/**, which is why the switch's project-policy state is the hop's answer", c.p, read)
		}
		if w, ok := attrs[".write"]; !ok || w != false {
			t.Errorf("/%s .write = %v, want false: only an operator's IAM credential writes the voice policy", c.p, w)
		}
	}
	// The shared node is not slug-bound, and a project's node is: the two
	// grants are the policy's, copied, and not one widened grant.
	if attrs := paths[shared]; strings.Contains(attrs[".read"].(string), "auth.token.fs") {
		t.Errorf("/%s .read = %v: the shared node is not slug-bound", shared, attrs[".read"])
	}
	if attrs := paths[repos]; len(attrs) != 0 {
		t.Errorf("/%s carries %v: the grant must be per $slug child", repos, attrs)
	}
}

// TestLaunchEnvelopeNodesAreReadPerChild: the launch envelope's grants, read out
// of the generated document rather than argued about (mobile plan task P5.1g,
// Q9's answer, the design's section 4.5 E1 and section 4.8). The emulator test
// beside this one (emulator_orchestrator_test.go) proves the same claims against
// a real rules engine; this is the half that runs in the job with no emulator,
// and it is the half a reviewer reads.
//
// Three claims, and each is a fence rather than a description: the two parents
// carry no grant at all (an RTDB read grant cascades, so one on `launch` would
// hand every device's entry to every paired phone and one on `config/policy`
// the published policy beside it); the recipe is read on `USER` and nothing else
// (the installation's named safe recipe is the same value for every paired
// device); and a device's entry is read on `USER` narrowed by `$uid ==
// auth.uid`, which is the whole of what keeps one phone's repository allow-list,
// launch rate and cost ceiling out of another phone's hands. Both children are
// `.write: false`: the values are the owner's, published over IAM.
//
// Hand mutation this fails on: widening the entry's read to `USER` alone,
// putting a grant on either parent, giving either child a `.write`, or dropping
// the `$uid == auth.uid` conjunct.
func TestLaunchEnvelopeNodesAreReadPerChild(t *testing.T) {
	paths := generatedPaths(t)
	recipe := PathLaunchEnvelope + "/recipe"
	devices := PathLaunchEnvelope + "/devices"
	entry := devices + "/$uid"
	for _, p := range []string{PathLaunchEnvelope, recipe, devices, entry} {
		if _, ok := paths[p]; !ok {
			t.Fatalf("the template does not generate /%s, which this package names and the app reads", p)
		}
	}
	for _, p := range []string{PathLaunchEnvelope, devices} {
		for _, a := range []string{".read", ".write"} {
			if v, ok := paths[p][a]; ok {
				t.Errorf("/%s has %s = %v: an RTDB read grant on a parent cascades to the recipe and to every device's entry", p, a, v)
			}
		}
	}
	read, _ := paths[recipe][".read"].(string)
	if !strings.Contains(read, "auth.token.mu") {
		t.Errorf("/%s .read = %q, want the USER claim: every paired device reads the installation's named safe recipe", recipe, read)
	}
	if strings.Contains(read, "$uid") {
		t.Errorf("/%s .read = %q is narrowed by a $uid the recipe is not keyed by", recipe, read)
	}
	if w, ok := paths[recipe][".write"]; !ok || w != false {
		t.Errorf("/%s .write = %v, want false: the operator publishes the recipe over IAM", recipe, w)
	}
	entryRead, _ := paths[entry][".read"].(string)
	if !strings.Contains(entryRead, "auth.token.mu") || !strings.Contains(entryRead, "$uid == auth.uid") {
		t.Errorf("/%s .read = %q, want the USER claim narrowed by $uid == auth.uid: without the narrowing one device reads every other device's allow-list", entry, entryRead)
	}
	if w, ok := paths[entry][".write"]; !ok || w != false {
		t.Errorf("/%s .write = %v, want false: a phone that could write its own entry would bound nothing", entry, w)
	}
}

// TestRunTokenWritesSurviveThePolicyEdit: the §5 edit is additive and confined
// to the config block, and a run token is not only a reader (design §4.3, §12a
// item 12). One row per location a run writes — the list below, checked to be
// exhaustive against the generated rules, so a later edit that adds a
// run-writable location has to add it here too — asserting the grant is still
// there: a config-block edit must not quietly take away a run's ability to
// record its own spend, its own outcome or its registry entry.
func TestRunTokenWritesSurviveThePolicyEdit(t *testing.T) {
	paths := generatedPaths(t)
	// Every location the template grants a run token a write on, as it renders
	// with $slug = slugForPath ($run and $day stay as the template spells them).
	want := []string{
		// runs/<slug>/<run>/ (the run's lifetime ledger)
		"runs/" + slugForPath + "/$run/reserved",
		"runs/" + slugForPath + "/$run/released",
		"runs/" + slugForPath + "/$run/spent",
		"runs/" + slugForPath + "/$run/notional",
		"runs/" + slugForPath + "/$run/overrun",
		"runs/" + slugForPath + "/$run/tokens",
		"runs/" + slugForPath + "/$run/exp",
		// spend/<day>/global/ (the counted, spent, notional and call counters)
		"spend/$day/global/counted",
		"spend/$day/global/spent",
		"spend/$day/global/notional",
		"spend/$day/global/calls",
		// spend/<day>/repos/<slug>/ (the same, per repository)
		"spend/$day/repos/" + slugForPath + "/counted",
		"spend/$day/repos/" + slugForPath + "/spent",
		"spend/$day/repos/" + slugForPath + "/notional",
		"spend/$day/repos/" + slugForPath + "/calls",
		// spend/<day>/repos/<slug>/byModel/<model>/ (the per-model counters)
		"spend/$day/repos/" + slugForPath + "/byModel/$model/micros",
		"spend/$day/repos/" + slugForPath + "/byModel/$model/in",
		"spend/$day/repos/" + slugForPath + "/byModel/$model/out",
		"spend/$day/repos/" + slugForPath + "/byModel/$model/cr",
		"spend/$day/repos/" + slugForPath + "/byModel/$model/cw",
		// the four spend leaves a stage report writes per model (D2648,
		// D2650): granted exactly as micros is
		"spend/$day/repos/" + slugForPath + "/byModel/$model/notional",
		"spend/$day/repos/" + slugForPath + "/byModel/$model/rep",
		"spend/$day/repos/" + slugForPath + "/byModel/$model/tlo",
		"spend/$day/repos/" + slugForPath + "/byModel/$model/thi",
		// the by-tier split of real spend (C1.3 rule 3, D140): tier B and
		// tier C under each of the four $spent locations above
		"runs/" + slugForPath + "/$run/byTier/$tier/spent",
		"spend/$day/global/byTier/$tier/spent",
		"spend/$day/repos/" + slugForPath + "/byTier/$tier/spent",
		"spend/$day/runs/" + slugForPath + "/$run/byTier/$tier/spent",
		// spend/<day>/runs/<slug>/<run>/ (the run's share of the day)
		"spend/$day/runs/" + slugForPath + "/$run/reserved",
		"spend/$day/runs/" + slugForPath + "/$run/released",
		"spend/$day/runs/" + slugForPath + "/$run/spent",
		// agents/<slug>/<run> (the registry entry) and its outcome
		"agents/" + slugForPath + "/$run",
		"outcomes/$day/" + slugForPath + "/$run",
	}
	for _, p := range want {
		attrs, ok := paths[p]
		if !ok {
			t.Errorf("/%s: the rules no longer generate this location", p)
			continue
		}
		w, ok := attrs[".write"].(string)
		if !ok || !strings.Contains(w, "auth.token") {
			t.Errorf("/%s .write = %v: a run token's write is not granted any more", p, attrs[".write"])
		}
	}

	// The list is exhaustive: every location under the run-token trees whose
	// .write is a granted expression is one of the rows above. A later edit
	// that adds a run-writable location fails here until it is listed.
	inList := map[string]bool{}
	for _, p := range want {
		inList[p] = true
	}
	for p, attrs := range paths {
		if _, ok := attrs[".write"].(string); !ok {
			continue
		}
		root := p
		if i := strings.IndexByte(p, '/'); i >= 0 {
			root = p[:i]
		}
		switch root {
		case "runs", "spend", "agents", "outcomes":
			if !inList[p] {
				t.Errorf("/%s is writable by a run token but is not one of this test's rows: add it", p)
			}
		case "mailbox":
			// Not a run token's tree, and named here so that the sweep has no
			// root it skips: the mailbox's writes are exactly four, and a
			// fifth is a new grant to a phone or to a consumer (D113, D113b).
			if _, listed := mailboxWrites()[p]; !listed {
				t.Errorf("/%s is writable under the mailbox: a write there is one of the four mailboxWrites() lists, and this one is not", p)
			}
		case "pairing":
			// D165's family (P6.4): the one write a pairing session has, and
			// TestPairingWritesAreExactlyTheOne is what pins it.
			if _, listed := pairingWrites()[p]; !listed {
				t.Errorf("/%s is writable under pairing: a write there is the one pairingWrites() lists, and this one is not", p)
			}
		}
	}
}

// mailboxWrites is the mailbox's grant list: each writable location with the
// fragments its predicate must carry. The phone's are MAILBOX (the user claim,
// and $mb naming the token's own uid), the consumer's are CONSUMER (mc naming
// the mailbox it serves), and all four require newData.exists(), because
// validation does not run on a delete and a delete of a message is therefore
// denied. The two message nodes and the ack also require !data.exists():
// a message, and its ack, are written once, so neither writer can rewrite
// history (the phone a sent message, the consumer an outbox message or its
// ack). The heartbeat is the one node that is overwritten by design: it is
// the consumer's latest liveness, not a record. TestMailboxWritesAreExactlyTheFour
// pins the list against the generated rules and
// TestRunTokenWritesSurviveThePolicyEdit's sweep fails on any mailbox write
// that is not in it.
func mailboxWrites() map[string][]string {
	phone := []string{"auth.token.mu != null", "auth.token.mu == auth.uid", "$mb == auth.uid", "newData.exists()"}
	consumer := []string{"auth.token.mc != null", "auth.token.mc == $mb", "newData.exists()"}
	once := func(f []string) []string { return append(slices.Clone(f), "!data.exists()") }
	return map[string][]string{
		"mailbox/$mb/inbox/$msg":        once(phone),
		"mailbox/$mb/outbox/$msg/acked": once(phone),
		"mailbox/$mb/outbox/$msg":       once(consumer),
		"mailbox/$mb/heartbeat":         consumer,
	}
}

// pairingWrites is D165's grant list (P6.4): the one write a pairing session
// has, with the fragments its predicate must carry — the pu claim, $uid naming
// the token's own uid, newData.exists() (a delete is not a write, and a delete
// of the request would let a session bind a second key) and !data.exists()
// (the request is written ONCE: a fingerprint the operator confirms must not be
// swappable afterwards). The delivery node is the operator's write over IAM and
// has none.
func pairingWrites() map[string][]string {
	return map[string][]string{
		"pairing/$uid/request": {"auth.token.pu != null", "auth.token.pu == auth.uid", "$uid == auth.uid", "newData.exists()", "!data.exists()"},
	}
}

// TestPairingWritesAreExactlyTheOne pins D165's family without the emulator:
// one write, for the pairing uid, on its own request node, once. The emulator
// proves what each identity can reach through it (emulator_pairing_test.go);
// this proves the list did not grow, which is the half a later edit would
// change quietly — a second grant here is a second way into a mailbox.
func TestPairingWritesAreExactlyTheOne(t *testing.T) {
	paths := generatedPaths(t)
	want := pairingWrites()
	for p, frags := range want {
		attrs, ok := paths[p]
		if !ok {
			t.Errorf("the template does not generate /%s", p)
			continue
		}
		w, _ := attrs[".write"].(string)
		if w == "" {
			t.Errorf("/%s carries no .write: the pairing session's one write is gone", p)
			continue
		}
		for _, f := range frags {
			if !strings.Contains(w, f) {
				t.Errorf("/%s .write = %q, want it to carry %q", p, w, f)
			}
		}
	}

	// No second grant anywhere in the family, and the delivery node is
	// explicitly denied to every writer: it is written over IAM, which bypasses
	// the rules, so a client grant there would be a client that can rewrite
	// what the phone was told to trust.
	for p, attrs := range paths {
		if p != "pairing" && !strings.HasPrefix(p, "pairing/") {
			continue
		}
		if w, ok := attrs[".write"].(string); ok {
			if _, listed := want[p]; !listed {
				t.Errorf("/%s is writable (%s) and is not the pairing session's one write", p, w)
			}
		}
	}
	if attrs, ok := paths["pairing/$uid/delivery"]; !ok {
		t.Error("the template does not generate /pairing/$uid/delivery")
	} else {
		if w, ok := attrs[".write"]; !ok || w != false {
			t.Errorf("/pairing/$uid/delivery .write = %v, want false", w)
		}
		r, _ := attrs[".read"].(string)
		for _, f := range []string{"auth.token.pu != null", "auth.token.pu == auth.uid", "$uid == auth.uid"} {
			if !strings.Contains(r, f) {
				t.Errorf("/pairing/$uid/delivery .read = %q, want it to carry %q", r, f)
			}
		}
	}

	// The request node is nobody's to read, the pairing uid included: the
	// operator reads the key over IAM when they check the fingerprint (P6.6).
	for _, p := range []string{"pairing", "pairing/$uid", "pairing/$uid/request"} {
		attrs, ok := paths[p]
		if !ok {
			t.Errorf("the template does not generate /%s", p)
			continue
		}
		if r, ok := attrs[".read"]; ok && r != false {
			t.Errorf("/%s .read = %v: an RTDB grant on a parent cascades to its children", p, r)
		}
	}
}

// TestDeviceNodesAreClientDenied pins D166's node from the rules' side: the
// allowlist is written over IAM by `fugaro pair` (P6.6) and by nothing else, so
// no client token — a phone's, a consumer's, a run's or a pairing session's —
// holds a grant on it or on any child of it. The rules are not what keeps it
// from a launcher, which reads it through roles/firebasedatabase.viewer
// whatever they say (G13); what they keep is a *client* out, which is the
// difference between an account that can un-revoke itself and one that cannot.
func TestDeviceNodesAreClientDenied(t *testing.T) {
	paths := generatedPaths(t)
	// The path internal/budget/rules names and the CLI will read (PathDevices).
	if PathDevices != "config/admin/devices" {
		t.Fatalf("PathDevices = %q, want config/admin/devices", PathDevices)
	}
	node := PathDevices + "/$uid"
	attrs, ok := paths[node]
	if !ok {
		t.Fatalf("the template does not generate /%s, which USER's device conjunct reads", node)
	}
	for _, a := range []string{".read", ".write"} {
		if v, ok := attrs[a]; !ok || v != false {
			t.Errorf("/%s %s = %v, want false: no client token may touch the allowlist", node, a, v)
		}
	}
	if r, ok := attrs[".read"].(string); ok {
		t.Errorf("/%s .read = %q: the node is written and read over IAM only", node, r)
	}
	// And its sibling `config/admin/push/$uid` — the registration and the
	// ledger the sender writes (the push-notifications design §3.4) —
	// has no node of its own here, which is a decision rather than an omission:
	// it inherits `config/admin`'s own refusal, and a node written out is the
	// only way a client could be granted one. P5.2.2's registration kind does
	// not touch it, and if a later generation does, this is the diff that says
	// so.
	if v, has := paths["config/admin"][".read"]; !has || v != false {
		t.Errorf("/config/admin .read = %v, want false: the push store is denied by inheritance from this node and carries no rule of its own", paths["config/admin"][".read"])
	}
	if v, has := paths["config/admin"][".write"]; !has || v != false {
		t.Errorf("/config/admin .write = %v, want false", paths["config/admin"][".write"])
	}
	for p := range paths {
		if p == "config/admin/push" || strings.HasPrefix(p, "config/admin/push/") {
			t.Errorf("/%s is written out in the template: the push store is refused by /config/admin's own denial, and a node of its own is the only way to grant a client one", p)
		}
	}
}

// TestDeviceNodeCarriesNoShapeRule is P2's fourth claim — the plan's red step
// calls it "the document moved" — and the one its three emulator cases cannot
// reach: reconciliation §F29 reads the node as `{ ".read": false, ".write":
// false }` with **no `.validate` over it, no `$other` beneath it, and nothing in
// the document naming a child of it**, and that absence, not the fields, is what
// makes the administered email and the role (D222, D225) children whose arrival
// changes nothing. No rule reads one, so an entry carrying them is admitted
// exactly as a bare one is, an installation whose document predates them needs
// no redeploy, and the deployed document takes no generation.
//
// It reads testdata/rules.golden.json rather than Generate(), and it pins the
// node's own attributes, every node on the way to it — the root and `config`
// included, because a rule on either, or on a wildcard spelling such as
// `config/admin/$x` that covers the entry just as the literal path does,
// constrains the entry's shape as much as one on `config/admin` does — every
// path beneath it and every occurrence of its path anywhere in the document,
// because the byte
// comparison in TestRulesGolden is satisfied by regenerating the golden — which
// is exactly how a shape rule arrives: whoever writes it runs UPDATE_GOLDEN=1 in
// the same change, and the diff is then the only place the generation would be
// seen. TestDeviceNodesAreClientDenied reads Generate() and so catches the
// change before it is regenerated; neither reading subsumes the other, and this
// one is the fence that survives the regeneration. A rule over this node, or one
// that reads a child of it, is a generation of its own (§F29's "what would force
// a generation later", D225's later half), so it has to fail here as well.
//
// The occurrences are read as spellings rather than as text: adjacent string
// literals are joined back up before the search, so the `devices` segment cut in
// two and put back together is seen, and a literal naming the path counts as a
// mention of it wherever it is built with a `+`, so a segment stood in for by a
// `$` capture is seen too. That last reading is anchored on `config/admin` and
// not on `config`, because a capture holds one segment and a rule that builds
// some other `config/…` path names neither the node nor a child of it: this
// fence may not accuse one of that. It is a fence over spellings and not a
// parser — a path assembled from pieces that never sit next to each other in the
// expression would still slip past it — but a reader that gets past it has to
// name the node somehow else, and TestUserClaimRequiresTheDeviceNode holds the
// document's one allowed reach to its exact text.
//
// Hand mutation this fails on, each with the golden regenerated — or edited
// outright, which is the same thing to a test that reads the golden — so that
// the byte comparison cannot object: `".read": "USER"` or `".write": "USER"` on
// the `$uid` entry (the attribute set), a `".validate"` beside them or on
// `config/admin/devices` above it (the same, and it is why every node on the way
// to the entry is read here at all: a rule on the parent constrains every entry
// under it), that `".validate"` on `config` or on the root instead, or on a
// `config/admin/$x` that covers the entry (the way there, which follows a
// wildcard segment as it follows the literal spelling), a `$other` child beneath
// the node (the paths), a USER that also required `child('role').exists()` (the
// occurrences), and a reader spelled around the entry — `root.child(
// 'config/admin/devices/' + auth.uid + '/role')`, one `child()` call and a
// concatenation, one call per segment, `root.child('config').child('admin')
// .child('devices').child(auth.uid).child('role')`, or the `devices` segment
// itself cut in two and put back together, `'config/admin/dev' + 'ices'`, or
// stood in for by a capture, `'config/admin/' + $k`: a test that matched the
// literal path, or a `.child(` *after* the entry's own chain, sees none of them
// (the occurrences again).
//
// The two predicates and the USER are caught twice over:
// TestDeviceNodesAreClientDenied and TestUserClaimRequiresTheDeviceNode hold the
// document to the expression each expects to find, and a regenerated golden does
// not break them. Everything else in that list is caught by nothing else, which
// is what this test is for.
func TestDeviceNodeCarriesNoShapeRule(t *testing.T) {
	raw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s is not JSON: %v", goldenPath, err)
	}
	node := PathDevices + "/$uid"
	segments := strings.Split(node, "/")
	// The one reach the document is allowed on the node: the conjunct that asks
	// whether the entry exists. It is taken out of an expression whole, and what
	// is left must name no part of the path — every spelling of the path carries
	// the `devices` segment, so a reader written one `.child()` per segment is as
	// visible here as the literal one.
	entry := "root.child('" + PathDevices + "').child(auth.uid).exists()"
	devices := segments[len(segments)-2]
	// A literal can be cut in two around the word (`'config/admin/dev' +
	// 'ices'`), so the pieces are put back together before the search, and a
	// capture can stand in for a whole segment (`'config/admin/' + $k`), so a
	// literal naming the path counts as a mention of it wherever it is built with
	// a `+`. Anchored on `config/admin` and not on `config`: a `$` variable holds
	// exactly one segment, so a capture can only stand in for `admin` or
	// `devices`, and a rule that builds some other `config/…` path — a per-repo
	// cap or kill lookup — names neither the node nor a child of it.
	split := regexp.MustCompile(`'\s*\+\s*'`)
	captured := regexp.MustCompile(`'[^']*config/admin[^']*'\s*\+|\+\s*'[^']*config/admin[^']*'`)
	// How far a path reaches towards the node: a wildcard segment matches any one
	// of the node's, so `config/admin/$x` covers the entry as surely as the
	// literal path does, and a rule there constrains its shape for the same
	// reason one on `config/admin/devices` does.
	reach := func(p string) (int, bool) {
		var given []string
		if p != "" {
			given = strings.Split(p, "/")
		}
		for i, s := range given {
			if i == len(segments) {
				break
			}
			if s != segments[i] && !strings.HasPrefix(s, "$") {
				return 0, false
			}
		}
		return len(given), true
	}
	var found bool
	var beneath, occurrences []string
	walk(doc["rules"], nil, func(path []string, attrs map[string]any) {
		p := strings.Join(path, "/")
		n, covers := reach(p)
		switch {
		case covers && n == len(segments):
			found = true
			if len(attrs) != 2 || attrs[".read"] != false || attrs[".write"] != false {
				t.Errorf("/%s carries %v, want exactly {\".read\": false, \".write\": false}: a rule over the node is a rules generation (§F29), and it is the first one that reads a child of it that D225's later half is for", p, attrs)
			}
		case covers && n < len(segments):
			for a, v := range attrs {
				if (a != ".read" && a != ".write") || v != false {
					t.Errorf("/%s carries %s = %v: the way to the entry is IAM-only, and a rule anywhere on it — a `.validate` here, or under a wildcard that covers the entry — is a rule over the entry's shape and moves the document (§F29)", p, a, v)
				}
			}
		case covers:
			beneath = append(beneath, "/"+p)
		}
		for a, v := range attrs {
			expr, ok := v.(string)
			if !ok {
				continue
			}
			rest := split.ReplaceAllString(strings.ReplaceAll(expr, entry, ""), "")
			if strings.Contains(rest, devices) || captured.MatchString(rest) {
				occurrences = append(occurrences, "/"+p+" "+a)
			}
		}
	})
	if !found {
		t.Fatalf("%s carries no /%s: the deployed document denies the node USER's device conjunct reads", goldenPath, node)
	}
	if len(beneath) > 0 {
		t.Errorf("%s names %s beneath /%s: nothing in the document may name a child of the node, or the entry stops being one whose fields are optional", goldenPath, strings.Join(beneath, ", "), node)
	}
	if len(occurrences) > 0 {
		t.Errorf("%s names %q outside the conjunct that asks whether the entry exists, in %s: only %s may mention the path — this reading joins literals split around a `+` and follows a `$` capture joined to one, so the whole segment need not be written out — because a rule that reads a child of the node is D225's later half and a rules generation of its own (§F29)", goldenPath, PathDevices, strings.Join(occurrences, ", "), entry)
	}
}

// TestUserClaimRequiresTheDeviceNode: D166's conjunct, in every read and write
// USER or MAILBOX reaches. The property is the one an operator relies on when
// they revoke a phone — deleting one child takes every family away at once —
// so it is asserted on the claim itself and on the rendered expressions, not
// only on the mailbox (an account that lost its device node must not go on
// reading the caps).
func TestUserClaimRequiresTheDeviceNode(t *testing.T) {
	conjunct := "root.child('config/admin/devices').child(auth.uid).exists()"
	if !strings.Contains(userClaim, conjunct) {
		t.Fatalf("userClaim = %q, want D166's conjunct %q", userClaim, conjunct)
	}

	paths := generatedPaths(t)
	// Every location whose grant is the user claim's, in either form: the
	// eight dashboard families carry USER, and the mailbox's two carry MAILBOX,
	// which is the same claim plus `$mb == auth.uid`.
	var n int
	for p, attrs := range paths {
		for _, a := range []string{".read", ".write"} {
			expr, ok := attrs[a].(string)
			if !ok {
				continue
			}
			if !strings.Contains(expr, "auth.token.mu") {
				continue
			}
			n++
			if !strings.Contains(expr, conjunct) {
				t.Errorf("/%s %s = %q carries the user claim without the device conjunct: revoking a phone would not revoke this", p, a, expr)
			}
		}
	}
	// The sweep would pass vacuously if the claim had stopped being used at
	// all; the eight read families and the mailbox's grants are the ones that
	// must be there.
	if n < 10 {
		t.Errorf("only %d grants carry the user claim: the sweep above proved nothing", n)
	}
}

// TestMailboxWritesAreExactlyTheFour pins the mailbox's grant list without an
// emulator: D113's two phone writes (its own inbox message, the ack on a
// message it spoke), D113b's two consumer writes (the outbox message, the
// heartbeat), and nothing else. The emulator proves what each identity can
// reach through them (emulator_mailbox_test.go, plan task P2.2); this proves
// the list did not grow, which is the half a later edit would change quietly.
func TestMailboxWritesAreExactlyTheFour(t *testing.T) {
	paths := generatedPaths(t)
	want := mailboxWrites()
	for p, frags := range want {
		attrs, ok := paths[p]
		if !ok {
			t.Errorf("the template does not generate /%s", p)
			continue
		}
		w, _ := attrs[".write"].(string)
		if w == "" {
			t.Errorf("/%s carries no .write: one of the mailbox's four writes is gone", p)
			continue
		}
		for _, f := range frags {
			if !strings.Contains(w, f) {
				t.Errorf("/%s .write = %q, want it to carry %q", p, w, f)
			}
		}
	}

	// No fifth: every writable location under the mailbox is one of the four.
	// TestRunTokenWritesSurviveThePolicyEdit's sweep fails on any of them
	// that is not listed here, so a new grant has to be argued for in both.
	for p, attrs := range paths {
		if p != "mailbox" && !strings.HasPrefix(p, "mailbox/") {
			continue
		}
		if w, ok := attrs[".write"].(string); ok {
			if _, listed := want[p]; !listed {
				t.Errorf("/%s is writable (%s) and is not one of the mailbox's four writes", p, w)
			}
		}
	}

	// The read side, which is the other half of the fence: the phone reads its
	// own outbox and heartbeat, and reads no inbox — its own included, since
	// the consumer collects what the phone wrote (§6.4).
	for _, p := range []string{"mailbox/$mb/outbox", "mailbox/$mb/heartbeat"} {
		attrs, ok := paths[p]
		if !ok {
			t.Errorf("the template does not generate /%s", p)
			continue
		}
		r, _ := attrs[".read"].(string)
		for _, f := range []string{"auth.token.mu == auth.uid", "$mb == auth.uid"} {
			if !strings.Contains(r, f) {
				t.Errorf("/%s .read = %q, want it to carry %q", p, r, f)
			}
		}
	}
	for _, p := range []string{"mailbox", "mailbox/$mb", "mailbox/$mb/inbox", "mailbox/$mb/inbox/$msg"} {
		attrs, ok := paths[p]
		if !ok {
			t.Errorf("the template does not generate /%s", p)
			continue
		}
		if r, ok := attrs[".read"]; ok && r != false {
			t.Errorf("/%s .read = %v: an RTDB grant on a parent cascades to its children", p, r)
		}
	}
}

// ---- The mailbox and the shared schema (mobile plan P2.1, design m9 §6.4).

// schemaPath is the shared schema, from this package's directory: the two
// artifacts are separate (the schema is the phone's, the rules are the
// database's) and nothing but a test can hold them together.
const schemaPath = "../../../shared/mailbox-schema/index.js"

// schemaCatalog reads shared/mailbox-schema/index.js and returns its exported
// constants by name, and the string literals of its two field lists. It is a
// deliberately small reader of a deliberately simple file: every export in it
// is `export const NAME = <number, Number.MAX_SAFE_INTEGER, or a frozen array
// of strings>;`, so a value this cannot parse is a failure rather than a
// silently unchecked number.
func schemaCatalog(t *testing.T) (consts map[string]int64, fields, required, kinds []string) {
	t.Helper()
	b, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("reading the shared schema: %v", err)
	}
	consts = map[string]int64{}
	for _, m := range regexp.MustCompile(`(?m)^export const ([A-Z_]+) = ([^;]+);`).FindAllStringSubmatch(string(b), -1) {
		name, expr := m[1], strings.TrimSpace(m[2])
		switch {
		case regexp.MustCompile(`^\d+$`).MatchString(expr):
			n, err := strconv.ParseInt(expr, 10, 64)
			if err != nil {
				t.Fatalf("%s = %s: %v", name, expr, err)
			}
			consts[name] = n
		case expr == "Number.MAX_SAFE_INTEGER":
			consts[name] = 1<<53 - 1
		case strings.HasPrefix(expr, "Object.freeze({"):
			// A frozen map of names (REFUSALS): no bound in it to compare.
		case strings.HasPrefix(expr, "Object.freeze(["):
			consts[name] = 0
			var got []string
			for _, s := range regexp.MustCompile(`'([^']*)'`).FindAllStringSubmatch(expr, -1) {
				got = append(got, s[1])
			}
			switch name {
			case "FIELDS":
				fields = got
			case "REQUIRED_FIELDS":
				required = got
			case "KINDS":
				kinds = got
			}
		default:
			t.Fatalf("shared/mailbox-schema exports %s = %s, which this reader does not understand: teach it, so the number stays checked", name, expr)
		}
	}
	if len(consts) == 0 || len(fields) == 0 || len(required) == 0 || len(kinds) == 0 {
		t.Fatalf("shared/mailbox-schema: read %d constants, %d fields, %d required fields and %d kinds", len(consts), len(fields), len(required), len(kinds))
	}
	return consts, fields, required, kinds
}

// rulesOnlyFields are the fields the rules gate that the shared schema does not
// name — the one licence this test gives the rules to lead the schema by, and
// it is a named field rather than a blanket tolerance. `sig` is the whole of
// it: mobile plan task J5r (O6) makes the document accept a signed message
// before the phone can write one, and J5b is the task that adds the field to
// shared/mailbox-schema. A second entry here is a decision, which is what the
// map's shape is for.
var rulesOnlyFields = map[string]bool{"sig": true}

// withoutTag is the schema's required-field list minus the tag, which the
// message's own validator states beside it as TAGOK rather than in the list
// (J5r): `mac` is the schema's name for a tag it does not yet call one.
func withoutTag(required []string) []string {
	out := make([]string, 0, len(required))
	for _, f := range required {
		if f != "mac" {
			out = append(out, f)
		}
	}
	return out
}

// TestMailboxRulesAgreeWithTheSharedSchema pins, without the emulator, the part
// of §6.4's "the two artifacts cannot disagree" that can be pinned in Go: the
// four bounds the rules apply to a message are the schema's own numbers, and
// the fields the rules accept are the schema's field set. Both directions
// matter, and the direction that matters more is the one that would refuse a
// message the phone accepted: a bound the rules apply tighter than the schema
// (at held to MaxAmount, an id to something the schema allows longer) fails
// here.
//
// P6.3 grew the schema and P6.4 (this change) grew the template with it: `mac`
// is required on every message and on the heartbeat, `agent`, `kind`,
// `options`, `reply_to` and the action record are the fields a message may
// carry beside it, and the (kind, field) table is what says which kind may
// carry which. So the two lists below are the schema's own, and the per-node
// loop asserts the rules gate exactly them — a field the rules gate that the
// schema does not name fails there too, because the rules may gate fewer
// fields than the schema, never one it does not have.
//
// P5.2.1 grew the schema again and P5.2.2 is the generation that grew the
// template with it: `token`, `platform` and `prefs` are the registration kind's,
// the kind enumeration carries the fifth kind, and the fields are gated on the
// message's own tag. Until generation 6 the rules gated none of them — a
// registration message was refused at the database, which was fail-closed
// rather than wrong, because no writer of one exists before the consumer
// (P5.2.4) and the app's registration path (P5.2.8). The gap was pinned below
// as exactly those three fields; P5.2.2 closes it, and the loop now fails on any
// field of the schema the rules do not gate.
//
// What this test cannot pin is the rest of the schema's judgement, and §6.4
// says where the rules are looser rather than claiming they are the same: the
// id's key rule (no `%`, no control character, no lone surrogate — all of
// which the schema refuses) is the schema's alone, `text`'s control characters
// with it, and so is the token's: the schema refuses a control character in one
// and states no pattern of its own (a real token's alphabet would fail any
// class it could name), so TOKENOK checks the bound and non-emptiness and the
// characters stay the schema's. The characters the
// schema states as a pattern (a tag, a head SHA, an identity, a target, a
// registration's quiet-hours zone) are checked at the database too, and
// TestMailboxRulesCheckTheSchemasCharacters pins them. The emulator cases
// (emulator_mailbox_test.go, emulator_pairing_test.go) prove what the rules do
// refuse.
func TestMailboxRulesAgreeWithTheSharedSchema(t *testing.T) {
	consts, fields, required, kinds := schemaCatalog(t)
	if got, want := MaxKeyLength, consts["ID_MAX"]; int64(got) != want {
		t.Errorf("MaxKeyLength = %d, the schema's ID_MAX = %d", got, want)
	}
	if got, want := MaxString, consts["SENDER_MAX"]; int64(got) != want {
		t.Errorf("MaxString = %d, the schema's SENDER_MAX = %d", got, want)
	}
	if got, want := MaxText, consts["TEXT_MAX"]; int64(got) != want {
		t.Errorf("MaxText = %d, the schema's TEXT_MAX = %d", got, want)
	}
	if got, want := MaxTimestamp, consts["AT_MAX"]; got != want {
		t.Errorf("MaxTimestamp = %d, the schema's AT_MAX = %d", got, want)
	}
	// The bounds P6.4 adds, each one a number the schema owns: a rules bound
	// tighter than the schema's would refuse a message the phone accepted, and
	// the two artifacts are separate files with nothing but this test holding
	// them together.
	for _, c := range []struct {
		what string
		got  int
		want int64
	}{
		{"MaxMacLength", MaxMacLength, consts["MAC_MAX"]},
		{"MaxAgentLength", MaxAgentLength, consts["AGENT_MAX"]},
		{"MaxAgentNameLength", MaxAgentNameLength, consts["AGENT_NAME_MAX"]},
		{"MaxAgents", MaxAgents, consts["AGENTS_MAX"]},
		{"MaxOptions", MaxOptions, consts["OPTIONS_MAX"]},
		{"MaxOptionLength", MaxOptionLength, consts["OPTION_MAX"]},
		{"MaxTargetLength", MaxTargetLength, consts["TARGET_MAX"]},
		{"MinShaLength", MinShaLength, consts["SHA_MIN"]},
		{"MaxShaLength", MaxShaLength, consts["SHA_MAX"]},
		// P5.2.2's three, the registration's own: the token, the quiet-hours
		// zone and the largest minute of the local day.
		{"MaxTokenLength", MaxTokenLength, consts["TOKEN_MAX"]},
		{"MaxZoneLength", MaxZoneLength, consts["ZONE_MAX"]},
		{"MaxMinute", MaxMinute, consts["MINUTE_MAX"]},
	} {
		if int64(c.got) != c.want {
			t.Errorf("%s = %d, the schema's own bound = %d", c.what, c.got, c.want)
		}
	}
	if want := []string{"id", "sender", "text", "at", "mac"}; !slices.Equal(required, want) {
		t.Errorf("the schema's REQUIRED_FIELDS = %v, want %v", required, want)
	}
	if want := []string{"id", "sender", "text", "at", "mac", "acked", "agent", "kind", "options", "reply_to", "action", "token", "platform", "prefs"}; !slices.Equal(fields, want) {
		t.Errorf("the schema's FIELDS = %v, want %v", fields, want)
	}

	// The generated mailbox applies those numbers, in the fields the schema
	// names. The heartbeat's `at` is the fourth, and its `boot` is a sender-like
	// string: the schema does not model the heartbeat, so only the bound is
	// shared.
	paths := generatedPaths(t)
	for _, c := range []struct {
		path string
		frag string
	}{
		{`mailbox/$mb/inbox/$msg`, `$msg.length <= ` + strconv.Itoa(MaxKeyLength)},
		{`mailbox/$mb/inbox/$msg/id`, `newData.val().length <= ` + strconv.Itoa(MaxKeyLength)},
		{`mailbox/$mb/inbox/$msg/sender`, `newData.val().length <= ` + strconv.Itoa(MaxString)},
		{`mailbox/$mb/inbox/$msg/text`, `newData.val().length <= ` + strconv.Itoa(MaxText)},
		{`mailbox/$mb/inbox/$msg/at`, `newData.val() <= ` + strconv.FormatInt(MaxTimestamp, 10)},
		{`mailbox/$mb/inbox/$msg/mac`, `newData.val().length == ` + strconv.Itoa(MaxMacLength)},
		{`mailbox/$mb/inbox/$msg/agent`, `newData.val().length <= ` + strconv.Itoa(MaxAgentLength)},
		{`mailbox/$mb/inbox/$msg/options/$i`, `newData.val().length <= ` + strconv.Itoa(MaxOptionLength)},
		{`mailbox/$mb/inbox/$msg/options/$i`, `$i.matches(` + indexPattern(MaxOptions) + `)`},
		{`mailbox/$mb/inbox/$msg/action/target/run`, `newData.val().length <= ` + strconv.Itoa(MaxTargetLength)},
		{`mailbox/$mb/outbox/$msg`, `$msg.length <= ` + strconv.Itoa(MaxKeyLength)},
		{`mailbox/$mb/outbox/$msg/text`, `newData.val().length <= ` + strconv.Itoa(MaxText)},
		{`mailbox/$mb/outbox/$msg/at`, `newData.val() <= ` + strconv.FormatInt(MaxTimestamp, 10)},
		{`mailbox/$mb/outbox/$msg/mac`, `newData.val().length == ` + strconv.Itoa(MaxMacLength)},
		{`mailbox/$mb/heartbeat/at`, `newData.val() <= ` + strconv.FormatInt(MaxTimestamp, 10)},
		{`mailbox/$mb/heartbeat/mac`, `newData.val().length == ` + strconv.Itoa(MaxMacLength)},
		{`mailbox/$mb/heartbeat/agents/$i`, `$i.matches(` + indexPattern(MaxAgents) + `)`},
		{`mailbox/$mb/heartbeat/agents/$i/id`, `newData.val().length <= ` + strconv.Itoa(MaxAgentLength)},
		{`mailbox/$mb/heartbeat/agents/$i/name`, `newData.val().length <= ` + strconv.Itoa(MaxAgentNameLength)},
	} {
		attrs, ok := paths[c.path]
		if !ok {
			t.Errorf("the template does not generate /%s", c.path)
			continue
		}
		v, _ := attrs[".validate"].(string)
		if !strings.Contains(v, c.frag) {
			t.Errorf("/%s .validate = %q, want it to carry the schema's own bound (%q)", c.path, v, c.frag)
		}
	}

	// The kind enumeration is the schema's KINDS, in the schema's own order, on
	// both messages: a word the schema does not name but the rules accept is a
	// kind the consumer has to guess at, and a word the schema names and the
	// rules refuse is a message the phone can send that the database refuses —
	// the direction §6.4 refuses for a bound, asked of a vocabulary. P5.2.2 adds
	// the registration word to it.
	enum := "newData.isString() && (newData.val() == '" + strings.Join(kinds, "' || newData.val() == '") + "')"
	for _, p := range []string{`mailbox/$mb/inbox/$msg/kind`, `mailbox/$mb/outbox/$msg/kind`} {
		attrs, ok := paths[p]
		if !ok {
			t.Errorf("the template does not generate /%s", p)
			continue
		}
		if v, _ := attrs[".validate"].(string); v != enum {
			t.Errorf("/%s .validate = %q, want the schema's KINDS in its own order (%q)", p, v, enum)
		}
	}

	// The required fields, from the schema's own list rather than from a literal
	// here: `hasChildren([...])` is what refuses a message with a required field
	// missing, and a tag is required on every message of every kind and on the
	// heartbeat. The
	// heartbeat's list is three of the four (it has no `id`), which the schema
	// states separately (HEARTBEAT_REQUIRED).
	//
	// The tag itself is the one required field the list no longer names: J5r
	// (O6) replaces the `mac` member with TAGOK, which requires exactly one of
	// `mac` and `sig`, because a phone paired the new way writes a signature and
	// has no MAC to offer. The schema still calls it `mac`, so the list this
	// builds is the schema's minus that one word, and the tag clause is asserted
	// beside it: the requirement moved, it did not weaken.
	msgRequired := "newData.hasChildren(['" + strings.Join(withoutTag(required), "', '") + "']) && " + exactlyOneTag("newData")
	for _, p := range []string{`mailbox/$mb/inbox/$msg`, `mailbox/$mb/outbox/$msg`} {
		attrs, ok := paths[p]
		if !ok {
			t.Errorf("the template does not generate /%s", p)
			continue
		}
		v, _ := attrs[".validate"].(string)
		if !strings.Contains(v, msgRequired) {
			t.Errorf("/%s .validate = %q, want the schema's required fields with the tag clause in place of `mac` (%q)", p, v, msgRequired)
		}
	}
	if attrs, ok := paths[`mailbox/$mb/heartbeat`]; !ok {
		t.Error("the template does not generate /mailbox/$mb/heartbeat")
	} else if v, _ := attrs[".validate"].(string); !strings.Contains(v, "newData.hasChildren(['at', 'boot']) && "+exactlyOneTag("newData")) {
		t.Errorf("/mailbox/$mb/heartbeat .validate = %q, want the tag required beside at and boot", v)
	}

	// The action record's own required set and the merge's head SHA. Not read
	// from the schema (ACTION_REQUIRED is not exported and this reader does not
	// follow a local constant), so these are pins: a record without its
	// confirmation is the shape G11 calls prose, and a merge without the head it
	// read back is one the consumer cannot check. J5r (O10) moves the
	// confirmation out of that list for `launch` alone — which modes need a
	// read-back is the gate's decision, not this document's — and gives the verb
	// its `mode`, so the two clauses are pinned beside the merge's own.
	for _, p := range []string{`mailbox/$mb/inbox/$msg/action`, `mailbox/$mb/outbox/$msg/action`} {
		attrs, ok := paths[p]
		if !ok {
			t.Errorf("the template does not generate /%s", p)
			continue
		}
		v, _ := attrs[".validate"].(string)
		for _, frag := range []string{
			"newData.hasChildren(['id', 'verb', 'target'])",
			"newData.hasChildren(['confirmation'])",
			"newData.child('verb').val() == 'merge' ? newData.child('head_sha').exists() : true",
			"newData.child('verb').val() == 'launch' ? newData.child('mode').exists() :",
			"== 'action'",
		} {
			if !strings.Contains(v, frag) {
				t.Errorf("/%s .validate = %q, want it to carry %q", p, v, frag)
			}
		}
	}

	// Every node of the subtree refuses an unknown field. `$other` with
	// `.validate: false` is the subtree idiom's whole point (F21): without it a
	// field the schema does not name is stored rather than refused, and the
	// field set would stop being the version.
	for _, p := range []string{
		`mailbox/$mb/inbox/$msg`, `mailbox/$mb/outbox/$msg`, `mailbox/$mb/heartbeat`,
		`mailbox/$mb/inbox/$msg/action`, `mailbox/$mb/inbox/$msg/action/target`,
		`mailbox/$mb/inbox/$msg/action/confirmation`, `mailbox/$mb/heartbeat/agents/$i`,
		`mailbox/$mb/outbox/$msg/action`, `pairing/$uid/request`,
	} {
		attrs, ok := paths[p+"/$other"]
		if !ok {
			t.Errorf("/%s has no $other child: an unknown field would be stored, not refused", p)
			continue
		}
		if v, ok := attrs[".validate"]; !ok || v != false {
			t.Errorf("/%s/$other .validate = %v, want false", p, v)
		}
	}

	// The fields, per node: the schema's own set, and every one of them a field
	// the schema names. An inbox message has the required P6.3 fields plus the
	// optional ones (`acked` is the phone's ack of a message it spoke, which
	// lives on an outbox message), and an outbox message has all of them. The
	// registration's three (P5.2.2) are on both: the app writes its registration
	// into its own inbox, and the consumer's messages travel the other way
	// through the same document. `sig` (J5r, O6) is on both as well, and it is
	// the one field here the schema does not name yet.
	gated := map[string]bool{}
	for _, c := range []struct {
		path string
		want []string
	}{
		{`mailbox/$mb/inbox/$msg`, []string{"id", "sender", "text", "at", "mac", "sig", "agent", "kind", "options", "reply_to", "action", "token", "platform", "prefs"}},
		{`mailbox/$mb/outbox/$msg`, []string{"id", "sender", "text", "at", "mac", "sig", "acked", "agent", "kind", "options", "reply_to", "action", "token", "platform", "prefs"}},
	} {
		if _, ok := paths[c.path]; !ok {
			t.Errorf("the template does not generate /%s", c.path)
			continue
		}
		var got []string
		for p := range paths {
			name, ok := strings.CutPrefix(p, c.path+"/")
			if !ok || name == "$other" || strings.Contains(name, "/") {
				continue
			}
			got = append(got, name)
		}
		slices.Sort(got)
		want := slices.Clone(c.want)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("/%s holds %v, the shared schema's field set for it is %v", c.path, got, want)
		}
		for _, field := range got {
			gated[field] = true
			if !slices.Contains(fields, field) && !rulesOnlyFields[field] {
				t.Errorf("/%s gates %q, which the shared schema does not name: the rules may gate fewer fields than the schema, never one it does not have", c.path, field)
			}
		}
	}

	// The fields the schema names and the rules do not gate: none, since P5.2.2
	// closed P5.2.1's three. Pinned rather than tolerated, so that the licence
	// to lag by one generation — which is what P5.2.1 took, and what the rules
	// generation exists to make explicit — is not a licence to lag by any field.
	var ungated []string
	for _, field := range fields {
		if !gated[field] {
			ungated = append(ungated, field)
		}
	}
	slices.Sort(ungated)
	if len(ungated) != 0 {
		t.Errorf("the rules do not gate %v, which the shared schema names: every field of the schema is a field the rules gate since generation 6", ungated)
	}
}

// registrationFieldValidators is what each field node of a registration
// carries, in the macros' own spelling: the message's kind read from the field
// (`PARENTKIND`), the field's own bound, and — on the three the schema makes
// required or optional of that kind — the tag of the message it sits on
// (`PARENTMACOK`, or `PARENTSIGOK` for a phone that signs; mobile plan task
// P5.2.2, and J5r for the second scheme). Mobile plan task P5.2.2.
//
// Built through expand rather than written out, registrationRequiredClause's
// reason: the macro writes the expression, and a second spelling of it here
// would be a second thing to keep in step. What it adds to the field-set
// assertion above is the *gate*: that one counts a field as gated when its
// node exists, so a validator that lost `PARENTKIND == 'registration' && ` —
// or negated it — would leave every field of the schema accounted for while a
// token, a platform or a preference record travelled on an ordinary message of
// any kind. Each entry is compared as a whole, so a change to a bound, to an
// arm or to the conjunction fails here and not only in the golden.
var registrationFieldValidators = []struct{ field, expr string }{
	{"token", "PARENTKIND == 'registration' && TOKENOK && (PARENTMACOK || PARENTSIGOK)"},
	{"platform", "PARENTKIND == 'registration' && PLATFORMOK && (PARENTMACOK || PARENTSIGOK)"},
	{"prefs", "PARENTKIND == 'registration' && newData.hasChildren(['kill_switch', 'cap_reached', 'consumer_offline', 'cap_near', 'prompt']) && (PARENTMACOK || PARENTSIGOK)"},
	{"prefs/quiet_hours", "newData.hasChildren(['zone', 'start', 'end'])"},
	{"prefs/quiet_hours/zone", "ZONEOK"},
	{"prefs/quiet_hours/start", "MINUTEOK"},
	{"prefs/quiet_hours/end", "MINUTEOK && newData.val() != newData.parent().child('start').val()"},
}

// TestRegistrationFieldValidatorsCarryTheKindGate reads the generated document
// the way the emulator will: field by field, on both messages, against the
// expression the macros expand to.
func TestRegistrationFieldValidatorsCarryTheKindGate(t *testing.T) {
	paths := generatedPaths(t)
	for _, dir := range []string{"inbox", "outbox"} {
		for _, f := range registrationFieldValidators {
			p := "mailbox/$mb/" + dir + "/$msg/" + f.field
			attrs, ok := paths[p]
			if !ok {
				t.Errorf("the template does not generate /%s", p)
				continue
			}
			// The depth render expands at: one per key below the root.
			want, err := expand(f.expr, len(strings.Split(p, "/")))
			if err != nil {
				t.Fatalf("/%s: %v", p, err)
			}
			if got, isStr := attrs[".validate"].(string); !isStr || got != want {
				t.Errorf("/%s .validate = %v, want %q: the kind gate on a field is what refuses it on a message that is not a registration", p, attrs[".validate"], want)
			}
		}
	}
}

// TestUserReadGrantsAreExactlyTheNamedFamilies: the read grants D112's answer
// adds, and nothing else. The user claim reads the eight expressions a run
// token already carried (each now `… || USER`), the two nodes of its own
// mailbox, — since coordinator plan task P3 — the coordinator's subtree, — since
// O2 of the stateless orchestrator plan — the orchestrator's, and — since
// mobile identity-pairing task J1 (F30, D2797, O3) — the single node
// `outcomes/$day/$slug/$run`, granted to `USER` alone so that the app can name
// a run that is still going without a run's own read widening with it.
// A grant anywhere else, on a family tension (b1) struck or on a parent that
// would cascade to one, fails here as well as in the golden.
//
// F11's property is the half this pins from the other side: those families are
// the only ones a signed-in user reaches, so an account with no mu claim reads
// nothing (admitted nowhere), and no family of the run's own slices — agents
// above all, and the parents and siblings of the one outcome node J1 opened —
// was widened by this change.
func TestUserReadGrantsAreExactlyTheNamedFamilies(t *testing.T) {
	paths := generatedPaths(t)
	// The ten families granted on a family-wide expression — the eight a run
	// token already carried, the coordinator's and the orchestrator's — the two
	// mailbox nodes, which are scoped to the mailbox the claim owns, and the one
	// outcome node of J1: a single location, reached by the app with the day,
	// the slug and the run it already holds from its spend read.
	// generatedPaths spells $slug as slugForPath (it is the one wildcard the
	// tests substitute), so the keys below do too.
	// The ninth is the coordinator's subtree (coordinator plan task P3): the
	// one family here that is granted on the root, so that the whole prefix is
	// one grant rather than a list (D195). The tenth is the stateless
	// orchestrator's (O2, D1220), granted on the root for the same reason and
	// with a `.write: false` beside it: the person reads the state over the
	// claim, and writes it over IAM, which no rule here binds.
	//
	// The launch envelope's two nodes are the last pair (mobile plan task
	// P5.1g, Q9 and Q11): the installation's named safe recipe, which every
	// paired device reads, and the device's own entry, which is the same claim
	// narrowed by `$uid == auth.uid` so that one phone reads no other phone's
	// allow-list. They are here rather than among the mailbox's two because
	// they are not mailbox-scoped: the entry is keyed by the Firebase Auth uid
	// the device node already is (P6.4), not by a mailbox.
	want := map[string]bool{
		"config/caps/global":                       true,
		"config/caps/defaults":                     true,
		"config/caps/repos/" + slugForPath:         true,
		"config/kill/global":                       true,
		"config/kill/repos/" + slugForPath:         true,
		"spend/$day/global":                        true,
		"spend/$day/repos/" + slugForPath:          true,
		"spend/$day/runs/" + slugForPath + "/$run": true,
		"outcomes/$day/" + slugForPath + "/$run":   true,
		"mailbox/$mb/outbox":                       true,
		"mailbox/$mb/heartbeat":                    true,
		"coordinator":                              true,
		PathOrchestrator:                           true,
		PathLaunchEnvelope + "/recipe":             true,
		PathLaunchEnvelope + "/devices/$uid":       true,
	}
	got := map[string]string{}
	for p, attrs := range paths {
		if r, ok := attrs[".read"].(string); ok && strings.Contains(r, "auth.token.mu") {
			got[p] = r
		}
	}
	for p := range want {
		r, ok := got[p]
		if !ok {
			t.Errorf("/%s does not carry the user claim's read grant: the app cannot read a family the owner granted it", p)
			continue
		}
		if strings.HasPrefix(p, "mailbox/") {
			if !strings.Contains(r, "$mb == auth.uid") {
				t.Errorf("/%s .read = %q, want the mailbox-scoped predicate ($mb == auth.uid)", p, r)
			}
			continue
		}
		if strings.Contains(r, "$mb") {
			t.Errorf("/%s .read = %q carries $mb: the dashboard families are the whole family, not one mailbox's", p, r)
		}
	}
	for p, r := range got {
		if !want[p] {
			t.Errorf("/%s carries the user claim's read grant (%s) and is not one of the families tension (b1) names", p, r)
		}
	}
}

// rulesMethods are the methods the Realtime Database rules language has: the
// RuleDataSnapshot methods (val, child, parent, hasChild, hasChildren, exists,
// getPriority, isNumber, isString, isBoolean) and the string methods
// (contains, beginsWith, endsWith, replace, toLowerCase, toUpperCase,
// matches). A method outside the list is not a rule the database runs — the
// deploy refuses the whole document ("No such method/property"), which is
// what a `numChildren()` in P6.4's first draft did: the document type-checked
// here and failed only against the emulator, which was not run.
var rulesMethods = []string{
	"val", "child", "parent", "hasChild", "hasChildren", "exists", "getPriority",
	"isNumber", "isString", "isBoolean",
	"contains", "beginsWith", "endsWith", "replace", "toLowerCase", "toUpperCase", "matches",
}

// TestGeneratedRulesUseOnlyRulesLanguageMethods is the check that does not need
// the emulator: every method a generated expression calls is one the rules
// language has. It is not a parser; it is the cheap half that catches a method
// borrowed from the client SDK (DataSnapshot.numChildren, forEach, size).
func TestGeneratedRulesUseOnlyRulesLanguageMethods(t *testing.T) {
	call := regexp.MustCompile(`\.([A-Za-z_]+)\(`)
	walk(rulesTree(t), nil, func(path []string, attrs map[string]any) {
		for k, v := range attrs {
			s, ok := v.(string)
			if !ok {
				continue
			}
			// Regex literals are not calls: drop them before scanning.
			s = regexLiteral.ReplaceAllString(s, "/re/")
			for _, m := range call.FindAllStringSubmatch(s, -1) {
				if !slices.Contains(rulesMethods, m[1]) {
					t.Errorf("/%s %s calls %s(), which the rules language does not have: the deploy refuses the document", strings.Join(path, "/"), k, m[1])
				}
			}
		}
	})
}

// regexLiteral matches a rules regex literal as the generator writes one.
var regexLiteral = regexp.MustCompile(`/\^[^ ]*\$/`)

// schemaPatterns reads the shared schema's character patterns, by name, as the
// literal source of each (`/^...$/`).
func schemaPatterns(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("reading the shared schema: %v", err)
	}
	out := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^const ([A-Z_]+_PATTERN) = (/.*/);$`).FindAllStringSubmatch(string(b), -1) {
		out[m[1]] = m[2]
	}
	return out
}

// TestMailboxRulesCheckTheSchemasCharacters: the rules language does have
// `matches()`, so every field whose character set the schema states as a
// pattern carries the same pattern at the database — the tag and the head SHA
// are hex, an agent id and a boot id are identities, a run target and a repo
// target are the schema's run and repo shapes, and a registration's quiet-hours
// zone is the schema's IANA name and not an offset. A list (options, agents) is
// a list: present means a child at an index below its bound, and a scalar in
// its place is refused. And a pairing uid is the `p~` uid P6.6 mints.
func TestMailboxRulesCheckTheSchemasCharacters(t *testing.T) {
	pats := schemaPatterns(t)
	for _, c := range []struct{ name, got string }{
		{"HEX_PATTERN", patternHex},
		{"IDENTITY_PATTERN", patternIdentity},
		{"RUN_PATTERN", patternRun},
		{"REPO_PATTERN", patternRepo},
		{"ZONE_PATTERN", patternZone},
	} {
		if want, ok := pats[c.name]; !ok {
			t.Errorf("the shared schema no longer defines %s: the rules' copy has nothing to agree with", c.name)
		} else if c.got != want {
			t.Errorf("the rules' %s = %s, the shared schema's = %s", c.name, c.got, want)
		}
	}
	paths := generatedPaths(t)
	for _, dir := range []string{"inbox", "outbox"} {
		m := `mailbox/$mb/` + dir + `/$msg`
		for _, c := range []struct{ path, frag string }{
			{m + `/mac`, ".matches(" + patternHex + ")"},
			// J5r's signature: the other scheme's tag, at its own length and in
			// its own character set (unpadded base64url), and the launch's mode
			// as a bounded identity string. The signature has no pattern in the
			// schema yet — J5b is the task that gives it one — so these are the
			// rules' own numbers, pinned here rather than read from the schema.
			{m + `/sig`, ".matches(" + patternSignature + ")"},
			{m + `/sig`, "newData.val().length == " + strconv.Itoa(MaxSigLength)},
			{m + `/action/mode`, "newData.parent().child('verb').val() == 'launch'"},
			{m + `/action/mode`, "newData.val().length <= " + strconv.Itoa(MaxModeLength)},
			{m + `/agent`, ".matches(" + patternIdentity + ")"},
			{m + `/action/head_sha`, ".matches(" + patternHex + ")"},
			{m + `/action/target/run`, ".matches(" + patternRun + ")"},
			{m + `/action/target/repo`, ".matches(" + patternRepo + ")"},
			{m + `/options`, "newData.hasChildren()"},
			{m + `/options/$i`, "$i.matches(" + indexPattern(MaxOptions) + ")"},
			// P5.2.2's registration: the token's bound and no character class of
			// its own (the schema states none either), the two platforms, the
			// zone's pattern and the two ends of the quiet-hours window as
			// minutes that differ, and — on each of the three fields — the
			// message's own tag, re-read one node up.
			{m + `/token`, "newData.val().length <= " + strconv.Itoa(MaxTokenLength)},
			{m + `/token`, "newData.parent().child('mac').val().matches(" + patternHex + ")"},
			{m + `/platform`, "newData.val() == 'android'"},
			{m + `/prefs`, "newData.hasChildren(['kill_switch', 'cap_reached', 'consumer_offline', 'cap_near', 'prompt'])"},
			{m + `/prefs/quiet_hours/zone`, ".matches(" + patternZone + ")"},
			{m + `/prefs/quiet_hours/zone`, "newData.val().length <= " + strconv.Itoa(MaxZoneLength)},
			{m + `/prefs/quiet_hours/start`, "newData.val() <= " + strconv.Itoa(MaxMinute)},
			{m + `/prefs/quiet_hours/end`, "newData.val() != newData.parent().child('start').val()"},
		} {
			wantFrag(t, paths, c.path, c.frag)
		}
	}
	for _, c := range []struct{ path, frag string }{
		{`mailbox/$mb/heartbeat/mac`, ".matches(" + patternHex + ")"},
		{`mailbox/$mb/heartbeat/sig`, ".matches(" + patternSignature + ")"},
		{`mailbox/$mb/heartbeat/sig`, "newData.val().length == " + strconv.Itoa(MaxSigLength)},
		{`mailbox/$mb/heartbeat/boot`, ".matches(" + patternIdentity + ")"},
		{`mailbox/$mb/heartbeat/boot`, "newData.val().length <= " + strconv.Itoa(MaxBootLength)},
		{`mailbox/$mb/heartbeat/agents`, "newData.hasChildren()"},
		{`mailbox/$mb/heartbeat/agents/$i`, "$i.matches(" + indexPattern(MaxAgents) + ")"},
		{`mailbox/$mb/heartbeat/agents/$i/id`, ".matches(" + patternIdentity + ")"},
		{`mailbox/$mb/heartbeat/agents/$i/name`, "newData.val().length > 0"},
	} {
		wantFrag(t, paths, c.path, c.frag)
	}
	consts, _, _, _ := schemaCatalog(t)
	if int64(MaxBootLength) != consts["BOOT_MAX"] {
		t.Errorf("MaxBootLength = %d, the schema's BOOT_MAX = %d", MaxBootLength, consts["BOOT_MAX"])
	}
	if !strings.Contains(pairingClaim, "auth.uid.beginsWith('p~')") {
		t.Errorf("pairingClaim = %q, want it bound to the p~ uid P6.6 mints", pairingClaim)
	}
}

// TestIndexPattern: the index pattern admits exactly 0..n-1, so a list keyed
// by its indexes holds at most n entries.
func TestIndexPattern(t *testing.T) {
	for _, n := range []int{1, 8, 32} {
		lit := indexPattern(n)
		re := regexp.MustCompile(strings.TrimSuffix(strings.TrimPrefix(lit, "/"), "/"))
		for i := -1; i <= n+1; i++ {
			if got, want := re.MatchString(strconv.Itoa(i)), i >= 0 && i < n; got != want {
				t.Errorf("indexPattern(%d) matches %d = %v, want %v", n, i, got, want)
			}
		}
		for _, s := range []string{"", "00", "01", "a", "1a", " 1"} {
			if re.MatchString(s) {
				t.Errorf("indexPattern(%d) matches %q", n, s)
			}
		}
	}
}

func wantFrag(t *testing.T, paths map[string]map[string]any, path, frag string) {
	t.Helper()
	attrs, ok := paths[path]
	if !ok {
		t.Errorf("the template does not generate /%s", path)
		return
	}
	if v, _ := attrs[".validate"].(string); !strings.Contains(v, frag) {
		t.Errorf("/%s .validate = %q, want it to carry %q", path, v, frag)
	}
}

// The three arms TARGETOK's ternary is written from, in the template's own
// spelling, and the verb read they hang off. They are consts rather than typed
// twice because TestConfirmTargetsCarryOneShape and the named rule change below
// both read them, and a second spelling would be a second thing to keep in
// step.
const (
	targetVerb   = "newData.parent().child('verb').val()"
	targetMerge  = "(newData.hasChildren(['repo', 'pr']) && !newData.child('run').exists())"
	targetCancel = "(newData.hasChildren(['run']) && !newData.child('repo').exists() && !newData.child('pr').exists())"
	targetLaunch = "(newData.hasChildren(['repo']) && !newData.child('run').exists() && !newData.child('pr').exists())"
)

// TestConfirmTargetsCarryOneShape pins TARGETOK's per-verb arms, and the
// `confirm` arm in particular.
//
// A `confirm` is the verb that authorises a destructive action (D169, G11), and
// its target resolves to a run or to a repository and a pull request. Both
// shapes are legal; a target carrying *both* is not, because "which one did
// they mean?" is a question the consumer would have to answer by guessing. An
// arm written as an either-shape test — `hasChildren(['run']) ||
// hasChildren(['repo', 'pr'])` — answers yes to `{run, repo, pr}` and to
// `{run, repo}`, and the emulator accepts both. The negative half of each arm
// is what refuses them, so each arm is pinned whole. The launch arm (J5r, O10)
// is the fourth and the narrowest: a start names a repository and neither a run
// nor a pull request.
//
// This is the static half: `emulator_pairing_test.go` writes the two both-shapes
// targets and watches the database refuse them, and
// `emulator_mailbox_test.go` writes a launch whose target carries a run or a
// pull request and watches the same.
func TestConfirmTargetsCarryOneShape(t *testing.T) {
	paths := generatedPaths(t)
	for _, dir := range []string{"inbox", "outbox"} {
		p := `mailbox/$mb/` + dir + `/$msg/action/target`
		for _, c := range []struct{ verb, frag string }{
			{"merge", "== 'merge' ? " + targetMerge},
			{"cancel", "== 'cancel' ? " + targetCancel},
			{"launch", "== 'launch' ? " + targetLaunch},
			{"confirm", "(" + targetCancel + " || " + targetMerge + ")"},
		} {
			wantFrag(t, paths, p, c.frag)
			t.Logf("/%s: the %s arm carries %s", p, c.verb, c.frag)
		}
	}
}

// ---- C1.3: the credentials rules -------------------------------------------
//
// The rules task of the credentials-service plan (`docs/credentials-service/
// implementation-plan.md`, task C1.3) adds three things to the template and two
// constants to `marker.go`. What is pinned here is the half the emulator
// cannot state: the shape of the generated document, that every location the
// rules carried before the change still carries byte-for-byte the same rule,
// and that the credentials generation is a marker of its own rather than a
// `RulesVersion` bump.
//
// The emulator half — what a run token may and may not do under the new nodes —
// is `creds_emulator_test.go` (build tag firebase).

// credsRoot is /creds, the cascade's shared state (D136, O1). The service's own
// account is its only writer; the node is explicit in the template rather than
// left to the root's default deny, so the deployed document says so where a
// reader looks for it.
const credsRoot = "creds"

// TestCredsIsDeniedByAnExplicitNode: the template carries an explicit /creds
// whose .read and .write are the booleans false, and nothing under it carries a
// grant. The root's default deny already refuses every client (which is what
// C0.6 pinned), so this changes no behaviour — it makes the denial visible in
// the generated document, where an edit that opened /creds would have to touch
// it.
//
// The denial is checked over **every** path the document generates under
// /creds, not over a hand-kept list of the ones a reader would name. The
// runtime half of this suite (creds_emulator_test.go) is behind the `firebase`
// build tag and does not run in the job that runs this file, so a grant opened
// anywhere in the family — a wildcard child such as `creds/leases/$r` carrying
// a TOKEN read, which the fixed list would not name — has to be caught here,
// where every `go test` sees it. TestRulesPreexistingPathsUnchanged is the
// second half of the same guard: it refuses a `creds/…` path that appears out
// of nowhere (isNewCredsPath).
//
// Hand mutation this fails on: dropping the node (the location is then absent,
// not denied by name), turning either attribute into an expression, or adding
// any node under /creds with a .read or a .write that is not false.
func TestCredsIsDeniedByAnExplicitNode(t *testing.T) {
	paths := generatedPaths(t)
	attrs, ok := paths[credsRoot]
	if !ok {
		t.Fatalf("the template does not generate /%s: the cascade's state must be denied by name, not only by the root", credsRoot)
	}
	for _, a := range []string{".read", ".write"} {
		v, ok := attrs[a]
		if !ok {
			t.Errorf("/%s carries no %s: an inherited deny is not the same document as an explicit one", credsRoot, a)
			continue
		}
		if v != false {
			t.Errorf("/%s %s = %#v, want false: no client token may reach the cascade's state (O1)", credsRoot, a, v)
		}
	}
	// Nothing under it grants anything either: a child grant would re-open the
	// family the explicit node closes, and the walk is over the document, so a
	// node this file has never heard of — the ones C1.9 will add — is checked
	// like the rest.
	for p, a := range paths {
		if p != credsRoot && !strings.HasPrefix(p, credsRoot+"/") {
			continue
		}
		for _, attr := range []string{".read", ".write"} {
			if v, has := a[attr]; has && v != false {
				t.Errorf("/%s %s = %#v, want false: no client token may reach the cascade's state (O1)", p, attr, v)
			}
		}
	}
}

// TestBuildLeaseFamilyIsTheWritersOwnName (T8, D1543, F22): the document names
// the build lease's family, and names it the key space internal/budget's own
// builders write into. There is an emulator test for the same family
// (buildlease_emulator_test.go) and it only runs under the firebase tag; this
// is the half that runs everywhere, and it is the half a rename breaks. A
// family the document carries under another name, or not at all, is one the
// CLI's writer writes with no rule over it — and that looks green, because the
// root refuses everything and nothing here reads the name.
//
// Hand mutation this fails on: renaming either side of the pair (they no longer
// agree), moving the family under runs/ (a lease becomes a run every record
// reader sees), and dropping the template's node or one of its two attributes
// (the family is then refused only by the root, which a later grant under it
// may widen without meeting a rule).
func TestBuildLeaseFamilyIsTheWritersOwnName(t *testing.T) {
	if PathBuildLeases != budget.BuildLeasesRoot {
		t.Fatalf("the rules name the build lease family /%s and internal/budget writes /%s, so a lease the CLI takes lives under a family no rule names", PathBuildLeases, budget.BuildLeasesRoot)
	}
	for _, p := range []string{budget.PathBuildLeases("aurora"), budget.PathBuildLease("aurora", "b1")} {
		if !strings.HasPrefix(p, PathBuildLeases+"/") {
			t.Errorf("internal/budget writes /%s, outside the family the rules name (/%s)", p, PathBuildLeases)
		}
	}
	// Keyed by its own id under its own family, never under runs/: a lease that
	// sits where a run sits is one every record reader mistakes for a run, and
	// the two families share no path. The run family's own root is read off a
	// run's path, so this test does not spell it a second time.
	runRoot := strings.SplitN(budget.PathRun("aurora", "b1"), "/", 2)[0]
	for _, p := range []string{budget.PathBuildLeases("aurora"), budget.PathBuildLease("aurora", "b1")} {
		if p == runRoot || strings.HasPrefix(p, runRoot+"/") {
			t.Errorf("internal/budget writes the build lease at /%s, inside the run family /%s: a lease there is read as a run (F22)", p, runRoot)
		}
	}

	// The template names the family, and names it closed: its writers are IAM
	// (the CLI's own credentials, the catalog-check job's account), so no client
	// token is admitted, and an inherited refusal is one a later grant can widen
	// without meeting an explicit node.
	var tmpl struct {
		Rules map[string]any `json:"rules"`
	}
	if err := json.Unmarshal([]byte(source), &tmpl); err != nil {
		t.Fatalf("the rules template is not JSON: %v", err)
	}
	node, ok := tmpl.Rules[PathBuildLeases].(map[string]any)
	if !ok {
		t.Fatalf("the template carries no top-level /%s subtree: F22 gives the build lease a key space of its own, and a family the rules never name is one no reader of this file can see", PathBuildLeases)
	}
	for _, attr := range []string{".read", ".write"} {
		if v, has := node[attr]; !has || v != false {
			t.Errorf("the template's /%s carries %s = %v, want an explicit false", PathBuildLeases, attr, v)
		}
	}

	// And the generated document — the one a deployment carries — still holds
	// it, with nothing beneath that grants anything: a child grant would
	// re-open what the family's node closes.
	paths := generatedPaths(t)
	generated, ok := paths[PathBuildLeases]
	if !ok {
		t.Fatalf("the template names /%s and the generated rules do not", PathBuildLeases)
	}
	for _, attr := range []string{".read", ".write"} {
		if v, has := generated[attr]; !has || v != false {
			t.Errorf("generated /%s %s = %#v, want an explicit false", PathBuildLeases, attr, v)
		}
	}
	for p, a := range paths {
		if !strings.HasPrefix(p, PathBuildLeases+"/") {
			continue
		}
		for _, attr := range []string{".read", ".write"} {
			if v, has := a[attr]; has && v != false {
				t.Errorf("/%s %s = %#v, want false: no client token may reach the build leases", p, attr, v)
			}
		}
	}
}

// TestAgentEntryCarriesTheCredentialNameAndWaitingUntil: the run's registry
// entry gains two validated children (C1.3 rule 2). `/agents/$slug/$run` is a
// closed allowlist (`$other` refuses everything else), so a cascade run's write
// of the credential it is using, or of the instant it expects to resume at,
// would be refused until they are named here.
//
// Both are validation-only, like every other field of the entry: the parent's
// `.write: RUN` is the grant, and the child carries the shape. `credential` is
// the credential's *name* and never a value — the rules can only bound it, and
// what makes it a name is that the service answers with one — and
// `waiting_until` is an instant, so it is held to the timestamp bound and not
// to the amount bound.
//
// Hand mutation this fails on: dropping either child (or its $other guard's
// exception), or widening `waiting_until` to AMOUNT, whose $10M bound would
// refuse a clock reading after the year 2286 while accepting a negative one.
func TestAgentEntryCarriesTheCredentialNameAndWaitingUntil(t *testing.T) {
	paths := generatedPaths(t)
	entry := "agents/" + slugForPath + "/$run"
	for _, c := range []struct{ field, want string }{
		{"credential", "newData.isString() && newData.val().length <= 200"},
		{"waiting_until", "newData.isNumber() && newData.val() >= 0 && newData.val() <= 9007199254740991 && newData.val() % 1 == 0"},
	} {
		p := entry + "/" + c.field
		attrs, ok := paths[p]
		if !ok {
			t.Errorf("the template does not generate /%s: a cascade run cannot record its %s", p, c.field)
			continue
		}
		if _, has := attrs[".write"]; has {
			t.Errorf("/%s carries a .write of its own: the entry's fields are validated under the parent's RUN grant", p)
		}
		v, _ := attrs[".validate"].(string)
		if !strings.Contains(v, c.want) {
			t.Errorf("/%s .validate = %q, want it to carry %q", p, v, c.want)
		}
		if !strings.Contains(v, "auth.token.rb != null") {
			t.Errorf("/%s .validate = %q, want RBOK: every field of the entry ties to the launcher's rb claim", p, v)
		}
	}
	// The entry is still a closed allowlist: an unnamed child is refused.
	if vv, ok := paths[entry+"/$other"]; !ok {
		t.Errorf("/%s/$other is gone: the entry must refuse a field the schema does not name", entry)
	} else if vv[".validate"] != false {
		t.Errorf("/%s/$other .validate = %v, want false", entry, vv[".validate"])
	}
}

// byTierPairs are the locations that gain a by-tier split of real spend, each
// paired with the `spent` leaf whose rule it repeats (C1.3 rule 3). Tier A
// stays on the existing `notional` nodes; only B and C are real money, and only
// they are split out.
func byTierPairs() []struct{ leaf, spent string } {
	return []struct{ leaf, spent string }{
		{"runs/" + slugForPath + "/$run/byTier/$tier/spent", "runs/" + slugForPath + "/$run/spent"},
		{"spend/$day/global/byTier/$tier/spent", "spend/$day/global/spent"},
		{"spend/$day/repos/" + slugForPath + "/byTier/$tier/spent", "spend/$day/repos/" + slugForPath + "/spent"},
		{"spend/$day/runs/" + slugForPath + "/$run/byTier/$tier/spent", "spend/$day/runs/" + slugForPath + "/$run/spent"},
	}
}

// TestByTierCountersAreValidatedLikeSpent: each by-tier leaf carries exactly the
// .write and the .validate of the `spent` leaf beside it, and the `$tier`
// wildcard admits only `b` and `c`. "Validated like today's spent" is the whole
// requirement (C1.3): a by-tier counter is the same quantity, split by which
// credential paid for it, so it must be as monotonic, as bounded per write and
// as tied to the run's own ledger as the total is — and a tier that is not b or
// c must not be a third place to write money, which is what keeps tier A
// (notional, never real) out of this family.
//
// Hand mutation this fails on: giving a by-tier leaf a .validate of its own
// (weaker or stronger), dropping the `$tier` test so an arbitrary tier key
// becomes writable, or dropping `newData.exists()` from its write so a delete
// (which validation never checks) could lower the counter.
func TestByTierCountersAreValidatedLikeSpent(t *testing.T) {
	paths := generatedPaths(t)
	for _, p := range byTierPairs() {
		spent, ok := paths[p.spent]
		if !ok {
			t.Fatalf("the template no longer generates /%s", p.spent)
		}
		attrs, ok := paths[p.leaf]
		if !ok {
			t.Errorf("the template does not generate /%s", p.leaf)
			continue
		}
		for _, a := range []string{".write", ".validate"} {
			got, _ := attrs[a].(string)
			want, _ := spent[a].(string)
			if want == "" {
				t.Fatalf("/%s carries no %s to mirror", p.spent, a)
			}
			if got == "" {
				t.Errorf("/%s carries no %s", p.leaf, a)
				continue
			}
			// The leaf sits `depth` locations deeper than the total it
			// repeats (byTier/$tier: two), so every new-data chain must reach
			// the same node through exactly that many more parent() calls.
			// Everything else -- every bound, every conjunct, every absolute
			// path -- is identical, which is what "validated like today's
			// spent" means.
			depth := strings.Count(p.leaf, "/") - strings.Count(p.spent, "/")
			wantHere := deeperBy(want, depth)
			if got != wantHere {
				t.Errorf("/%s %s = %q, want the rule of /%s at exactly %d more parent(): %q", p.leaf, a, got, p.spent, depth, wantHere)
			}
			// The comparison is depth-exact: a leaf whose chains stop one
			// parent() short is a different rule, not an equal one.
			if parentChain.MatchString(want) && shallowerByOne(wantHere) == wantHere {
				t.Errorf("/%s %s: a chain one parent() short compares equal to the right depth", p.leaf, a)
			}
		}
		tier := strings.TrimSuffix(p.leaf, "/spent")
		if v, _ := paths[tier][".validate"].(string); v != "$tier == 'b' || $tier == 'c'" {
			t.Errorf("/%s .validate = %q, want the tier key test", tier, v)
		}
		if _, has := paths[tier][".write"]; has {
			t.Errorf("/%s carries a .write above its leaf: a whole-tier write must be denied", tier)
		}
	}
}

// TestAgentEntryFieldsAreTemplateChildren closes the loop between the Go type
// and the rules: `/agents/$slug/$run` is a closed allowlist, so a field
// budget.AgentEntry marshals that the template does not name is a write every
// run makes and the database refuses. The two new children of this task were
// found exactly that way -- the plan says the template is a closed allowlist,
// "so these must be added here" -- and this test is what makes the next field
// impossible to add on one side only.
//
// Hand mutation this fails on: adding a field to budget.AgentEntry without
// naming it in the template (or the reverse: removing a child the struct still
// writes).
func TestAgentEntryFieldsAreTemplateChildren(t *testing.T) {
	// Every field set, so an omitempty one is marshalled too -- the two rigor
	// children of the rigor-modes plan's T1.7 among them, which is the only
	// reason this test sees them: leave them empty and omitempty drops them
	// from the map, so the template could lose its "mode" or "from" child and
	// only TestRulesGolden would notice.
	entry := budget.AgentEntry{
		Repo: "aurora/web", Workflow: "implement", Title: "t", Stage: "code", Round: 1, Verify: "go",
		Coder: "m", Reviewer: "m", Recipe: "r", Auth: "cascade", PRURL: "https://example.invalid/1",
		Action: "tool Bash", Tokens: 1, StartedAt: 1, StageStartedAt: 1, StageDeadline: 1, UpdatedAt: 1,
		Spent: 1, Halted: "budget", Credential: "a1", WaitingUntil: 2, RequestedBy: "alice@example.invalid",
		Mode: "critical", From: "area",
	}
	b, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	paths := generatedPaths(t)
	parent := "agents/" + slugForPath + "/$run"
	for f := range fields {
		if _, ok := paths[parent+"/"+f]; !ok {
			t.Errorf("budget.AgentEntry marshals %q and the template does not generate /%s: the entry's $other guard refuses the write", f, parent+"/"+f)
		}
	}
	// The interesting two, named so that a rename has to be argued: the
	// cascade's current credential and the instant it expects to resume at.
	for _, f := range []string{"credential", "waiting_until"} {
		if _, ok := fields[f]; !ok {
			t.Errorf("budget.AgentEntry no longer marshals %q", f)
		}
	}
}

// TestRulesPreexistingPathsUnchanged: every location the generation-3 document
// carried (testdata/rules.generation3.json, a frozen copy of the golden as main
// had it) still carries byte-for-byte the same rule. The new document may only
// add; a rule that moved under a path that already existed is a change to what
// every installation's runs may do, and it must be seen and named rather than
// arrive as part of "we added the credentials children" — or, for this branch's
// O2, "we added the orchestrator subtree", whose addition is the top-level
// /orchestrator node and nothing under it, or for its T8, "we added the build
// lease family", whose addition is likewise its own node and nothing under it
// (isNamedAddition) — or, for WS22, "we added the credential route", whose
// addition is the route subtree and one repository's record under it.
//
// Two rules already there are allowed to move, and both are named. One is a
// change: the outcome's status validator gains the run status 'unreviewed'
// (isNamedRuleChange, docs/binary-allow-rule's NV0, D994), the one change of
// that shape because it is what keeps the database true about a status a
// release adds, and the widening is exact — the one clause and nothing else.
// The other is an *addition to a rule*: the node the outcome itself sits at
// gains the paired device's read (isNamedReadGrant, mobile identity-pairing J1,
// F30, D2797, O3), which is a new attribute on a path generation 3 already
// carried and therefore neither an addition path nor a changed validator. It is
// exact in the same way: one attribute, at one named path, holding one named
// expression, with every attribute beside it byte for byte what it was.
//
// Hand mutation this fails on: editing any existing rule (a loosened bound, a
// dropped conjunct) while adding the new nodes, widening the outcome's status
// validator by anything but that one clause (a second word, a bound instead of
// an equality), a read granted at a second path or to an expression other than
// the user claim, or dropping the new node from the template (the addition check
// below).
func TestRulesPreexistingPathsUnchanged(t *testing.T) {
	raw, err := os.ReadFile("testdata/rules.generation3.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	before, after := map[string]map[string]any{}, generatedPaths(t)
	walk(doc["rules"], nil, func(path []string, attrs map[string]any) {
		if len(path) == 0 {
			return
		}
		segs := make([]string, len(path))
		for i, s := range path {
			if s == "$slug" { // generatedPaths spells the model slug as slugForPath
				s = slugForPath
			}
			segs[i] = s
		}
		before[strings.Join(segs, "/")] = attrs
	})
	for p, want := range before {
		got, ok := after[p]
		if !ok {
			t.Errorf("/%s is gone: this change may only add to the rules", p)
			continue
		}
		if !maps.Equal(want, got) {
			if isNamedRuleChange(p, want, got) || isNamedReadGrant(p, want, got) {
				continue
			}
			t.Errorf("/%s changed:\n  was %v\n  now %v", p, want, got)
		}
	}
	// And the addition is the one this task makes: the new paths are exactly
	// the ones isNamedAddition names. Anything else that appears here is a
	// rules change nobody asked for.
	for p := range after {
		if _, ok := before[p]; ok {
			continue
		}
		if !isNamedAddition(p) {
			t.Errorf("/%s is new and is not one of this task's additions", p)
		}
	}
	// And this task's own addition is really there. The loop above proves only
	// that whatever appeared is allowed, and a document that never gained the
	// subtree would pass it, while the whole point of this diff is that it did.
	if _, ok := after[PathOrchestrator]; !ok {
		t.Errorf("the generated rules carry no /%s node: O2's addition to this document is the whole of what this branch changes", PathOrchestrator)
	}
	// E12's node is the same check one level down: the loop above would pass a
	// document that never gained it, because a path that is absent is not a
	// path that is unnamed.
	if _, ok := after[escalationLedgerPath]; !ok {
		t.Errorf("the generated rules carry no /%s node: the reports job's read of the ledger's escalation block is what E12 adds", escalationLedgerPath)
	}
	// And J5r's, per node: an addition loop that only looks at what appeared
	// passes a document that never gained any of them.
	for _, p := range []string{
		"mailbox/$mb/inbox/$msg/sig",
		"mailbox/$mb/outbox/$msg/sig",
		"mailbox/$mb/heartbeat/sig",
		"mailbox/$mb/inbox/$msg/action/mode",
		"mailbox/$mb/outbox/$msg/action/mode",
	} {
		if _, ok := after[p]; !ok {
			t.Errorf("the generated rules carry no /%s node: the tag a device signs with, and the mode a typed launch carries, are the whole of what J5r adds", p)
		}
	}
}

// outcomeStatusPath is the one existing rule this branch changes, spelled as
// generatedPaths spells it (the model slug, not the document's $slug variable).
const outcomeStatusPath = "outcomes/$day/" + slugForPath + "/$run/status"

// unreviewedClause is the one disjunct the widening adds to that rule, with the
// spacing the template writes it in.
const unreviewedClause = " || newData.val() == 'unreviewed'"

// registeredKindClause is the disjunct P5.2.2 adds to the kind enumeration, on
// both messages, with the spacing the template writes it in.
const registeredKindClause = " || newData.val() == 'registration'"

// stopKindClause is the disjunct P5.1g adds to the same enumeration, on both
// messages, with the spacing the template writes it in: the phone's stop
// request, the sixth member of the schema's KINDS. The kind gate on the field
// validators is built from kindOf, which reads the schema's vocabulary, so it
// carries `stop` on its own and the change here is the enumeration alone.
const stopKindClause = " || newData.val() == 'stop'"

// registrationRequiredClause is the conjunct P5.2.2 adds to the message's own
// validator — KINDREQ's registration arm, which is what makes a registration
// carry its token and its platform. Built from kindOf rather than written out,
// because the macro writes that expression and a second spelling of it here
// would be a second thing to keep in step.
var registrationRequiredClause = " && (" + kindOf("newData") + " == 'registration' ? (newData.child('token').exists() && newData.child('platform').exists()) : true)"

// launchVerbClause is the disjunct J5r adds to the action verb enumeration
// (O10), with the spacing the template writes it in: the typed start, the
// fourth member of the mailbox schema's VERBS.
const launchVerbClause = " || newData.val() == 'launch'"

// msgRequired is the message's own hasChildren list as J5r writes it: the four
// fields generation 3 required, and a tag that is exactly one of `mac` and
// `sig` in place of the `mac` member it required. Built from withoutTag and
// exactlyOneTag rather than typed twice, because the template's list and this
// one are the same list and a second spelling would be a second thing to keep
// in step.
var msgRequired = "newData.hasChildren(['" + strings.Join(withoutTag(msgRequiredFields), "', '") + "']) && " + exactlyOneTag("newData")

// msgRequiredFields is the four fields a message carries whatever its kind;
// withoutTag drops `mac` from the list generation 3 required, because a message
// now carries its tag as one of two members and neither is a required one.
var msgRequiredFields = []string{"id", "sender", "text", "at", "mac"}

// actionRequiredWas and actionRequiredNow are the two halves of the one change
// J5r moves on the action node's own validator: a `launch` carries its `mode`
// and needs no `confirmation` (O10 — which modes need a read-back at the gate
// is J5a's, so a plain start needs none of it here), while every other verb
// keeps `confirmation` exactly as generation 3 wrote it.
//
// They are a pair and not a clause insertion, because `confirmation` moves out
// of the hasChildren list as the verb test grows around it: the list, the
// launch arm and the confirmation arm in the new string together have to leave
// the old one byte for byte.
var (
	actionRequiredWas = "newData.hasChildren(['id', 'verb', 'target', 'confirmation']) && (newData.child('verb').val() == 'merge' ? newData.child('head_sha').exists() : true)"
	actionRequiredNow = "newData.hasChildren(['id', 'verb', 'target']) && (newData.child('verb').val() == 'launch' ? newData.child('mode').exists() : (newData.hasChildren(['confirmation']) && (newData.child('verb').val() == 'merge' ? newData.child('head_sha').exists() : true)))"
)

// namedRuleChanges is the whole of what this document's existing rules may
// change: each path, and the edits the change makes to its validator, each one
// a substring the new string carries and the substring — often nothing — that
// it puts in its place. Applying every edit at its path has to leave the
// generation-3 validator byte for byte, so a change that also moved a bound,
// dropped a conjunct or reworded an arm is not one of these, and neither is one
// that edits a string nobody named.
//
// It is a list per path and no longer a single clause, because J5r's change to
// a message is two edits: the tag clause is inserted, and the `mac` member is
// taken out of the hasChildren list beside it.
type ruleChange struct {
	was, now string
}

var namedRuleChanges = map[string][]ruleChange{
	// The outcome's status gains the run status 'unreviewed' (docs/
	// binary-allow-rule's NV0, D994).
	outcomeStatusPath: {{now: unreviewedClause}},
	// The two messages gain the registration kind (mobile plan task P5.2.2):
	// the word in the enumeration, and the required fields on the message —
	// and, for J5r, a tag that is one of `mac` and `sig` in place of the `mac`
	// the list used to require (O6). The same two edits on the outbox, which
	// carries the same shape in the other direction.
	"mailbox/$mb/inbox/$msg": {
		{now: registrationRequiredClause},
		{was: "newData.hasChildren(['id', 'sender', 'text', 'at', 'mac'])", now: msgRequired},
	},
	"mailbox/$mb/outbox/$msg": {
		{now: registrationRequiredClause},
		{was: "newData.hasChildren(['id', 'sender', 'text', 'at', 'mac'])", now: msgRequired},
	},
	"mailbox/$mb/inbox/$msg/kind":  {{now: registeredKindClause + stopKindClause}},
	"mailbox/$mb/outbox/$msg/kind": {{now: registeredKindClause + stopKindClause}},
	// And J5r's two other moves: the typed launch verb, and the action
	// validator that lets a launch carry its mode where every other verb
	// carries its confirmation.
	"mailbox/$mb/inbox/$msg/action/verb":  {{now: launchVerbClause}},
	"mailbox/$mb/outbox/$msg/action/verb": {{now: launchVerbClause}},
	"mailbox/$mb/inbox/$msg/action": {
		{was: actionRequiredWas, now: actionRequiredNow},
	},
	"mailbox/$mb/outbox/$msg/action": {
		{was: actionRequiredWas, now: actionRequiredNow},
	},
	"mailbox/$mb/inbox/$msg/action/target": {
		{was: "(" + targetCancel + " || " + targetMerge + ")", now: "(" + targetVerb + " == 'launch' ? " + targetLaunch + " : (" + targetCancel + " || " + targetMerge + "))"},
	},
	"mailbox/$mb/outbox/$msg/action/target": {
		{was: "(" + targetCancel + " || " + targetMerge + ")", now: "(" + targetVerb + " == 'launch' ? " + targetLaunch + " : (" + targetCancel + " || " + targetMerge + "))"},
	},
	// The heartbeat's tag, written the same way as a message's (O6).
	"mailbox/$mb/heartbeat": {
		{was: "newData.hasChildren(['at', 'boot', 'mac'])", now: "newData.hasChildren(['at', 'boot']) && " + exactlyOneTag("newData")},
	},
}

// TestOutcomeRuleAcceptsEveryRunStatus: the outcome's status validator accepts
// every status a run ends with, and nothing else. The set is a literal
// in the template, because the template is a document and not Go, and
// internal/budget builds its own copy of it from runstore.Statuses; this is the
// pin that holds the two spellings together, and it has to live here: the
// package that derives the set cannot import this one (internal/budget's own
// emulator tests import it, so the import would cycle — MaxEntryTokens is
// duplicated for that same reason and pinned the same way).
//
// Both directions matter. A missing word is a rule that refuses the outcome a
// run of this release writes, which the dashboard shows as an infrastructure
// error for a status that is not one; a word nobody writes is a database
// allowed to carry a status no reader switches on.
//
// Hand mutation this fails on: dropping a word from the template's validator
// (the first loop), or adding one to the template that no record carries (the
// second).
func TestOutcomeRuleAcceptsEveryRunStatus(t *testing.T) {
	attrs, ok := generatedPaths(t)[outcomeStatusPath]
	if !ok {
		t.Fatalf("the template generates no rule at /%s: the outcome's status is unvalidated", outcomeStatusPath)
	}
	rule, ok := attrs[".validate"].(string)
	if !ok {
		t.Fatalf("the rule at /%s carries no .validate: %v", outcomeStatusPath, attrs)
	}
	for _, s := range runstore.Statuses {
		// A run writes its outcome as it ends, so the vocabulary is the terminal
		// statuses — running is the one word an outcome never carries, and the
		// one this rule must refuse (internal/budget's outcomeStatuses reads the
		// same predicate).
		if s.Terminal() {
			if !strings.Contains(rule, "newData.val() == '"+string(s)+"'") {
				t.Errorf("the outcome's status validator does not accept %q, a status a run record ends with: the rules refuse that run's outcome, and it shows as an infrastructure error it is not", s)
			}
			continue
		}
		if strings.Contains(rule, "newData.val() == '"+string(s)+"'") {
			t.Errorf("the outcome's status validator accepts %q, which no run has ended with: a run writes its outcome as it ends, and an outcome the rules refuse leaves it with no figure at all", s)
		}
	}
	for _, m := range outcomeStatusWords.FindAllStringSubmatch(rule, -1) {
		if m[1] == outcomeStatusNone || slices.ContainsFunc(runstore.Statuses, func(s runstore.Status) bool { return string(s) == m[1] }) {
			continue
		}
		t.Errorf("the outcome's status validator accepts %q, which is no run status: the rules would let the database carry a word no reader switches on", m[1])
	}
}

// outcomeStatusWords finds the statuses the outcome's validator compares
// against, in the template's own spelling.
var outcomeStatusWords = regexp.MustCompile(`newData\.val\(\) == '([^']*)'`)

// outcomeStatusNone is the status of a run with no outcome at all, which no
// record writes and the rules accept all the same; internal/budget's
// outcomeStatuses carries it beside runstore.Statuses, which is why it is
// exempt from the second loop rather than missing from it.
const outcomeStatusNone = "none"

// isNamedRuleChange reports whether the change to the rule at p — from want
// (generation 3) to got (what this branch generates) — is this branch's own and
// nothing besides: one of the edits named in namedRuleChanges, on one of the
// paths named there, and every other path as it was.
//
// Two of the paths it names are the outcome's status, which gains the run
// status 'unreviewed' — the label of a run that finished and pushed with no
// review verdict (docs/binary-allow-rule/implementation-plan.md's NV0, D994) —
// and, for J5r, the mailbox: a message and the heartbeat carry a tag that is
// one of `mac` and `sig` (O6), and an inbox action may be a typed launch
// carrying a `mode` (O10). The others are earlier tasks' (P5.2.2's registration
// kind, P5.1g's stop kind).
//
// The outcome's is the one whose reason is worth writing down, because it is
// the one a rules generation would be wrong to carry: the rule is what a client
// writes through, so the word a run's own record now carries is refused by a
// document that does not spell it — internal/budget's Finish would file the
// outcome as an infrastructure error, a fault that never happened, on the page
// a person reads to see what their runs did. Widening the validator is what
// stops that, and RulesVersion moves for a change that opens a family to a
// client or moves what one may reach (internal/projectpolicy/version.go's
// generation-4 note), while this admits one more value under a `.write` that
// does not move. `fugaro update` carries it — its rules stage redeploys a
// document infra.SameJSON reports as different, whatever the marker says
// (internal/infra's DB.Plan) — and doctor's policy-rules-skew already reports
// the installation that has not run it yet.
//
// The check is exact: one of the named paths, the `.validate` attribute of it
// alone, and applying that path's own edits to the new string leaving the
// generation-3 one byte for byte. An edit is a pair — the substring the new
// string carries and the one it puts in its place — so an edit that quietly
// takes out a bound or a conjunct beside the one it names fails here.
func isNamedRuleChange(p string, want, got map[string]any) bool {
	changes, named := namedRuleChanges[p]
	if !named || len(want) != len(got) {
		return false
	}
	if !maps.Equal(withoutAttr(want, ".validate"), withoutAttr(got, ".validate")) {
		return false
	}
	ws, wok := want[".validate"].(string)
	gs, gok := got[".validate"].(string)
	if !wok || !gok {
		return false
	}
	for _, c := range changes {
		if c.now == "" || !strings.Contains(gs, c.now) {
			return false
		}
		gs = strings.Replace(gs, c.now, c.was, 1)
	}
	return gs == ws
}

// outcomeReadPath is the one existing rule J1 *adds* an attribute to, spelled as
// generatedPaths spells it: the outcome node itself, not its status child.
const outcomeReadPath = "outcomes/$day/" + slugForPath + "/$run"

// namedReadGrants is the whole of what this document may add to a rule that
// already existed: each path, and the read expression the addition puts on it.
// J1 (mobile identity-pairing, F30, D2797, O3) is the one entry, and it is what
// lets a paired phone name a run that is still going — a running run is a
// `spend/$day/runs/$slug/$run` entry with no `outcomes` entry beside it. The
// expression is the user claim and nothing else: `RUN || USER` would widen the
// run's own reach with the device's, and `USER` on any parent of this node
// (outcomes, outcomes/$day) would hand the app a list rather than a run.
var namedReadGrants = map[string]string{
	outcomeReadPath: userClaim,
}

// isNamedReadGrant reports whether the change to the rule at p — from want
// (generation 3) to got (what this branch generates) — is one of the named read
// grants and nothing besides: exactly one attribute more than generation 3 had,
// that attribute `.read`, holding byte for byte the named expression, and every
// attribute beside it unchanged.
//
// It is deliberately not isNamedRuleChange's shape: that one requires the
// attribute *count* to be equal, because widening a validator rewrites a string
// that was already there. A read is a new attribute on a node whose other rules
// do not move, so the count is what rises, and the rest is compared as a whole.
func isNamedReadGrant(p string, want, got map[string]any) bool {
	grant, named := namedReadGrants[p]
	if !named || len(got) != len(want)+1 {
		return false
	}
	if r, ok := got[".read"].(string); !ok || r != grant {
		return false
	}
	return maps.Equal(want, withoutAttr(got, ".read"))
}

// withoutAttr is m without one attribute, so the attributes beside it are
// compared on their own: isNamedRuleChange allows the validator to move and
// nothing else about the rule, and isNamedReadGrant allows a read to be added
// and nothing else.
func withoutAttr(m map[string]any, key string) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		if k != key {
			out[k] = v
		}
	}
	return out
}

// isNamedAddition reports whether p is one of the paths a rules change of this
// branch is allowed to add: the credentials family (C1.3), the coordinator's
// subtree (coordinator plan task P3, D195), the hosted MCP server's own
// (docs/mcp-server/implementation-plan.md's M3, D922), the build lease's
// (docs/no-prompt-policy/implementation-plan.md's T8, D1543, F22), the
// stateless orchestrator's (docs/stateless-orchestrator/implementation-plan.md's
// O2, D1220), the workspace family's (docs/workspaces/implementation-plan.md's
// W3, workspace.go), a registration message's own fields (mobile plan task
// P5.2.2), or the tag a message and the heartbeat carry and the mode a typed
// launch carries (mobile plan task J5r, O6 and O10). Anything else that appears
// in the document is a rules change nobody asked for.
//
// They are equalities and not prefixes, for the reason isNewCredsPath gives
// below: P3 adds the family's own node and nothing under it, so a path *under*
// /coordinator is a change to what the family contains — the shape a grant
// hidden under it takes — and belongs here as a diff a reviewer reads rather
// than as a match this test waves through. M3's and T8's nodes are refused
// whole, so a path under either is the same shape. O2's was the same again
// until E12, and it is the one that matters most: the subtree carried a single
// `.read` at the top and no rule under it, so a `$child` with a grant of its
// own is a widening hidden one level down — and one a `USER` read at the top
// would otherwise make look legitimate. E12 (F27) makes exactly that change,
// deliberately and for the reports job alone, so its five nodes are named in
// isNewEscalationReadPath and the diff is what a reviewer reads.
// isNewSignaturePath reports whether p is one of the five nodes J5r adds to the
// mailbox: the `sig` a message or the heartbeat may carry in place of `mac` and
// the `mode` a typed launch carries (O6 and O10, the mobile identity-pairing
// plan's J5r).
//
// They are equalities and not prefixes, for isNewCredsPath's reason: each is a
// leaf whose parent generation 3 already carried, so a path under one of them
// would be a rule inside a field — a child of `sig`, say, which is a shape the
// message has no room for and a reviewer has to see.
//
// The three tags are here and not in namedRuleChanges for the same reason the
// launch envelope's nodes are in isNewLaunchPath: a `sig` node is a field a
// client writes where nothing was written before, which is an addition, while
// the `mac` member leaving the message's hasChildren list is a change to a rule
// that existed and is named there.
func isNewSignaturePath(p string) bool {
	return p == "mailbox/$mb/inbox/$msg/sig" || p == "mailbox/$mb/outbox/$msg/sig" ||
		p == "mailbox/$mb/heartbeat/sig" || p == "mailbox/$mb/inbox/$msg/action/mode" ||
		p == "mailbox/$mb/outbox/$msg/action/mode"
}

func isNamedAddition(p string) bool {
	return isNewCredsPath(p) || p == coordpath.PathRoot || p == PathMCPAuth ||
		p == PathBuildLeases || p == PathOrchestrator || isNewWorkspacePath(p) || isNewVoicePath(p) ||
		isNewRegistrationPath(p) || isNewEscalationReadPath(p) || isNewSpendAccuracyPath(p) ||
		isNewRoutePath(p) || isNewLaunchPath(p) || isNewSignaturePath(p)
}

// isNewLaunchPath reports whether p is one of the four nodes the launch
// envelope adds under the policy node (mobile plan task P5.1g, the owner's
// answers Q9 and Q11): the envelope's own node, the installation's named safe
// recipe, the devices node, and one paired device's entry.
//
// All four are equalities and not prefixes, for isNewCredsPath's reason: a path
// *under* a device's entry is a change to what that entry contains — the
// repository allow-list, the rate and the cost ceiling — which is the shape a
// grant hidden one level down takes, and it belongs here as a diff a reviewer
// reads rather than as a match this function waves through.
//
// `launch` and `devices` carry no grant of their own and are in the list
// because the template spells them, for isNewVoicePath's reason: a read on
// either would cascade to every device's entry, and naming them here is what
// makes an edit that puts one there a diff this test shows.
func isNewLaunchPath(p string) bool {
	return p == PathLaunchEnvelope || p == PathLaunchEnvelope+"/recipe" ||
		p == PathLaunchEnvelope+"/devices" || p == PathLaunchEnvelope+"/devices/$uid"
}

// escalationLedgerPath is the ladder's ledger node as the rules spell it (E7,
// E12): one work item's escalation block, under the state document of
// orchestrator id `$id`. The report's section is built from it and nothing
// else of the database (F27).
const escalationLedgerPath = PathOrchestrator + "/$id/state/work_items/$item/escalation"

// isNewEscalationReadPath reports whether p is one of the five nodes the
// reports job's read adds under /orchestrator (E12, F27,
// docs/outer-loop-escalation): the four containers that reach the ledger's
// escalation block, and the block itself. The block is the one node that
// carries a grant; the four above it carry none and are in the list because
// the generated document spells them, which is what makes an edit that puts a
// grant on one of them a diff this test shows.
//
// They are equalities and not prefixes, for isNewCredsPath's reason: a path
// *under* the block is a change to what it contains, and the fields a work
// item's escalation carries are not this task's to grant.
func isNewEscalationReadPath(p string) bool {
	for _, path := range []string{
		escalationLedgerPath,
		PathOrchestrator + "/$id",
		PathOrchestrator + "/$id/state",
		PathOrchestrator + "/$id/state/work_items",
		PathOrchestrator + "/$id/state/work_items/$item",
	} {
		if p == path {
			return true
		}
	}
	return false
}

// TestRulesReportsJobReadsEscalationOnly: the reports job's identity reads the
// ladder's ledger escalation block and nothing else of the database (E12,
// F27). The document the test walks is the generated one, so the grant and the
// node it is written on are held together: the block carries one read and no
// write, the four nodes between it and /orchestrator carry no rule of their
// own, the subtree's own rule is where O2 put it, and the claim is the reports
// job's alone — not the phone's `mu`, not a run's `fs`/`fr`, not a pairing
// session's `pu`.
//
// Hand mutation this fails on: dropping the `rj != null` conjunct from the
// claim (the guard asserted below, which is what keeps the rule shut where
// /fugaro/project is absent — the golden is the only other test that notices,
// and its message tells a reader to regenerate it), moving the grant up one
// node (the widening this test exists for — a read on `state` or on
// `work_items` reaches every work item's every field), granting the block to
// `USER` or to a run token, adding a write beside the read, or writing a rule
// for the reports job onto a sibling under /orchestrator.
func TestRulesReportsJobReadsEscalationOnly(t *testing.T) {
	got := map[string]map[string]any{}
	walk(rulesTree(t), nil, func(path []string, attrs map[string]any) {
		got[strings.Join(path, "/")] = attrs
	})

	block, ok := got[escalationLedgerPath]
	if !ok {
		t.Fatalf("the generated rules carry no /%s: E12's grant is not in the document", escalationLedgerPath)
	}
	if len(block) != 1 || block[".read"] != reportsJobClaim {
		t.Errorf("/%s = %v, want exactly the reports job's read (%q) and nothing beside it", escalationLedgerPath, block, reportsJobClaim)
	}

	// The path down to the block carries no grant of its own. This is the half
	// that makes the read narrow: a `.read` on any of these reaches the whole
	// state document, which is what the reports job may not have.
	for _, p := range []string{
		PathOrchestrator + "/$id",
		PathOrchestrator + "/$id/state",
		PathOrchestrator + "/$id/state/work_items",
		PathOrchestrator + "/$id/state/work_items/$item",
	} {
		attrs, ok := got[p]
		if !ok {
			t.Errorf("the generated rules carry no /%s: the grant is written on a path the document does not have", p)
			continue
		}
		if len(attrs) != 0 {
			t.Errorf("/%s = %v, want no rule of its own: a grant here reaches every work item's every field, and the reports job may read the escalation block only", p, attrs)
		}
	}

	// And the subtree's own rule is O2's, unchanged: the person reads it, no
	// client writes it.
	if attrs := got[PathOrchestrator]; len(attrs) != 2 || attrs[".read"] != userClaim || attrs[".write"] != false {
		t.Errorf("/%s = %v, want the `USER` read (%q) and the false write O2 shipped", PathOrchestrator, attrs, userClaim)
	}

	// The null guard is asserted as a conjunct of its own rather than left to
	// the golden, whose message tells a reader to regenerate it. Without it the
	// rule is fail-open: `/fugaro/project` is absent until an installation
	// writes it (the generation-0 state check 82 documents), and the equality
	// alone then compares null to null and admits any signed-in client to every
	// work item's escalation block.
	if !strings.Contains(reportsJobClaim, "auth.token.rj != null && ") {
		t.Errorf("the reports job's read is %q, want the `auth.token.rj != null` conjunct before the project equality: without it the block is readable by any signed-in client wherever /fugaro/project is absent", reportsJobClaim)
	}

	// The claim names no other identity. Every one of these is a token some
	// client of this installation holds, and the ledger is not theirs.
	for _, claim := range []string{
		"auth.token.mu", "auth.token.mc", "auth.token.pu",
		"auth.token.fs", "auth.token.fr", "auth.token.fp", "auth.token.rb",
	} {
		if strings.Contains(reportsJobClaim, claim) {
			t.Errorf("the reports job's read names %s: the escalation block is granted to the reports job's own claim and to no identity that already exists", claim)
		}
	}

	// Nothing else under /orchestrator gained a rule: the whole of E12's
	// widening is the one node above.
	for p, attrs := range got {
		if p == escalationLedgerPath || !strings.HasPrefix(p, PathOrchestrator+"/") {
			continue
		}
		if len(attrs) != 0 {
			t.Errorf("/%s = %v: E12 adds the escalation block's read and no other rule under /%s", p, attrs, PathOrchestrator)
		}
	}
}

// isNewSpendAccuracyPath reports whether p is one of the four day-counter leaves
// a run's stage report writes per model (spend-accuracy D2648 as amended by
// D2650, docs/spend-accuracy/day-leaves.md): the subscription part of the
// model's dollars, the cost the provider reported for the calls that carried
// one, and the table's charge over those same calls at the two ends of the tier
// that served them. They are equalities and not prefixes, for isNewCredsPath's
// reason: a path *under* one of them is a change to what the leaf holds, which
// is the shape a rule hidden one level down takes.
//
// The four are granted exactly as `micros` is, and the emulator case
// (new_leaves_emulator_test.go, build tag firebase) is what proves the bounds
// rather than this list: what this function does is make the diff that adds
// them a diff a reviewer reads.
func isNewSpendAccuracyPath(p string) bool {
	for _, leaf := range []string{"notional", "rep", "tlo", "thi"} {
		if p == "spend/$day/repos/"+slugForPath+"/byModel/$model/"+leaf {
			return true
		}
	}
	return false
}

// isNewRegistrationPath reports whether p is one of the field nodes a
// registration kind adds to a message (mobile plan task P5.2.2): the three the
// schema names, the preference record's own, and the quiet-hours window inside
// it — on both the inbox and the outbox, whose field sets the schema holds
// equal.
//
// They are equalities and not prefixes, for isNewCredsPath's reason: a path
// *under* one of them is a change to what that field contains, which is the
// shape a rule hidden one level down takes, and it belongs here as a diff a
// reviewer reads rather than as a match this function waves through. The two
// `$other` closures are named too — they are rules of their own, and the one
// that would widen the field set if it were dropped.
func isNewRegistrationPath(p string) bool {
	for _, dir := range []string{"inbox", "outbox"} {
		for _, field := range []string{
			"token", "platform",
			"prefs", "prefs/$other",
			"prefs/kill_switch", "prefs/cap_reached", "prefs/consumer_offline",
			"prefs/cap_near", "prefs/prompt",
			"prefs/quiet_hours", "prefs/quiet_hours/$other",
			"prefs/quiet_hours/zone", "prefs/quiet_hours/start", "prefs/quiet_hours/end",
		} {
			if p == "mailbox/$mb/"+dir+"/$msg/"+field {
				return true
			}
		}
	}
	return false
}

// isNewVoicePath reports whether p is one of the four locations the voice
// policy node adds (mobile plan task P5.3.1, the voice-evaluation switch
// design §4.3): the subtree's own node, its two children, and one project's
// child. All four are equalities and not prefixes, for isNewCredsPath's reason
// — the whole subtree is what this task adds, so a path *under* a project's
// node is a change to what it contains, which is the shape a grant hidden one
// level down takes, and it belongs here as a diff a reviewer reads rather than
// as a match this function waves through.
//
// The parent, PathVoice, is in the list because the template spells it: it
// carries no grant of its own — the grants are per child, and a read on the
// parent would cascade to every project's allow-list — and naming it here is
// what makes an edit that puts one there a diff this test shows.
func isNewVoicePath(p string) bool {
	return p == PathVoice || p == projectpolicy.PathVoiceShared() ||
		p == projectpolicy.PathVoiceRepos() || p == projectpolicy.PathVoiceRepo(slugForPath)
}

// isNewRoutePath reports whether p is one of the two locations the credential
// route adds (workspace-service task WS22, decisions D2980–D2982, resolutions
// R3 and R4): the subtree's own node and one repository's record. Both are
// equalities and not prefixes, for isNewCredsPath's reason — the record's
// members are the service's address and the holder of each kind, so a path
// *under* a repository's node is a change to what a run reads, and it belongs
// here as a diff a reviewer sees rather than as a match this function waves
// through.
//
// The subtree's own node is in the list because the template spells it and
// because it is the one path a grant on which would reach every repository's
// record: the grants are per repository, and an RTDB read grant cascades down.
//
// The path is spelled here rather than taken from a constant, because the
// contract that owns it is internal/creds (RoutePath, C2's wire contract): its
// import allow-list admits internal/creds, internal/credsvc and internal/cli
// and nothing else (TestCredsIsNotReachableFromARun), so this package may not
// import it, and a second spelling of the path in this package would be a
// second contract. The two agree because internal/creds'
// TestRoutePathIsGrantedInTheDeployedRules walks this document's own golden at
// creds.RoutePath and fails if the rule there is not the one this template
// writes.
func isNewRoutePath(p string) bool {
	return p == routeSubtree || p == routeSubtree+"/"+slugForPath
}

// routeSubtree is the credential route's subtree as the template spells it,
// above one repository's record. The records' own paths are creds.RoutePath's
// (C2's wire contract) and the segment under this subtree is budget.Key of the
// raw slug — the spelling the rule compares a token's `fs` claim with, so a
// path keyed another way is a node no run asks for and no rule admits. It is
// declared here rather than in the emulator test so that the path the shape
// test names and the path the emulator drives are one spelling.
const routeSubtree = "config/policy/route"

// isNewWorkspacePath reports whether p is one of the paths the workspace family
// adds (docs/workspaces/implementation-plan.md W3, workspace.go): the record and
// its four children, the admitted-token hashes, and the meter and the rollup by
// month. Like the credentials family's, every one of them is an equality and
// not a prefix — a path *under* `workspaces/$name` that is not named here is a
// rule hidden under the record, which is the shape a client's read of it takes,
// and it belongs in this test as a diff a reviewer reads rather than as a match
// this function waves through.
//
// The paths are the family's whole output, so a node added to
// workspace.json.tmpl without being named here fails this test — which is the
// point: the workspace's database is the one whose readers are its own service
// and the owner's CLI, and a rule that reached a client there would be a leak
// with a public address.
func isNewWorkspacePath(p string) bool {
	for _, node := range []string{
		WorkspaceRecordPath,
		WorkspaceRecordPath + "/$name",
		WorkspaceRecordPath + "/$name/version",
		WorkspaceRecordPath + "/$name/name",
		WorkspaceRecordPath + "/$name/members",
		WorkspaceRecordPath + "/$name/cap",
		WorkspaceTokenPath,
		WorkspaceTokenPath + "/$hash",
		WorkspaceMeterPath,
		WorkspaceMeterPath + "/$month",
		WorkspaceRollupPath,
		WorkspaceRollupPath + "/$month",
	} {
		if p == node {
			return true
		}
	}
	return false
}

// isNewCredsPath reports whether p is one of the three things C1.3 adds: the
// credentials node, a by-tier counter (or the node above one), or the registry
// entry's two new children. Anything else that appears in the document is a
// rules change nobody asked for.
func isNewCredsPath(p string) bool {
	// The family is one node, denied whole. A path *under* it is not this
	// task's addition but a change to what the family contains — the shape a
	// grant hidden under /creds takes — so it is refused here rather than
	// waved through by a prefix match (review finding M1: a wildcard child
	// with a TOKEN read passed this test and was caught only by the emulator,
	// which does not run in the job that runs this file).
	if p == credsRoot {
		return true
	}
	for _, pair := range byTierPairs() {
		tier := strings.TrimSuffix(pair.leaf, "/spent") // .../byTier/$tier
		if p == pair.leaf || p == tier || p == strings.TrimSuffix(tier, "/$tier") {
			return true
		}
	}
	// `mode` and `from` are the same shape of addition one generation later
	// (rigor-modes plan T1.7, F40 of its reconciliation): the mode a live run
	// resolved and the layer that decided it, which the runner writes into its
	// own registry entry from its record so `fugaro watch` can show them. Both
	// are equalities, for the reason above.
	entry := "agents/" + slugForPath + "/$run/"
	return p == entry+"credential" || p == entry+"waiting_until" ||
		p == entry+"mode" || p == entry+"from"
}

// TestRulesVersionUnchanged: the generations an installation compares, asserted
// here so that a bump has to come to this test and argue for it. The name is
// C1.3's, which left all three where they were and had the credentials children
// carry a marker of their own instead (marker.go); P3 is the part that moves the
// first of them, and it says why in the same place:
//
//   - projectpolicy.RulesVersion is the generation of the whole document, read
//     by the deploy's downgrade refusal, the runner's policy gate and doctor's
//     policy-rules-skew. C1.3 left it at 3 because its change added optional
//     children of nodes runs already write. P3 adds a **grant** — a whole family
//     a signed-in person may read — and a client holding the older marker would
//     not know the family is there, which is the drift the marker exists to
//     report; so the generation moved to 4 and doctor's policy-rules-skew is
//     what tells an installation whose owner has not redeployed yet. P5.3.1
//     moved it to 5 for the same reason one step over: the voice policy node
//     (PathVoice, `config/policy/voice/{shared,repos/$slug}`) is a grant too —
//     a run token reads its own project's node there and did not before — and
//     the generation is what tells an installation at 4 that its deployed
//     document is not this binary's. P5.2.2 moves it to 6 for the same reason
//     in the other direction: the registration kind's fields are what the phone
//     *writes* into its own inbox, so under the document behind the app's every
//     registration is refused at the database with no error — a reach a client
//     holds, which is what the marker records — and the widening folds into the
//     same stage: `fugaro update`'s rules deploy is the migration, and it is
//     the same stage every generation is met through, safe while runs are in
//     flight. SA5.0 moves it to 7 for the registration kind's reason again: the
//     four spend leaves a stage report writes per model (`notional`, `rep`,
//     `tlo` and `thi` under `spend/$day/repos/$slug/byModel/$model`, D2648 as
//     amended by D2650) are a reach a *client* holds, and RTDB applies a
//     multi-path update atomically, so under the document behind the whole
//     stage report is refused rather than one leaf of it. J1 (mobile
//     identity-pairing, F30, D2797, O3) moves it to 8 for P3's reason: the
//     paired device's read of `outcomes/$day/$slug/$run` is a **grant**, one
//     node a signed-in phone may read and did not before, and this time the
//     grant is to `USER` alone — the run token's own read set does not move — so
//     the phone's running-jobs view (J2) names a run that is still going. WS22
//     (workspace-service, D2982, R3 and R4) moves it to 9 for P5.3.1's reason:
//     the credential-route leaf (`config/policy/route/$slug`, which
//     `creds.RoutePath` builds and `TestRoutePathIsGrantedInTheDeployedRules`
//     holds this document to) is a **grant** to a run token too — the run reads
//     its route before its first call, and did not read this leaf before — so a
//     run under the document behind is refused a read the routing depends on.
//     P5.1g moves it to 10 for two of those reasons at once, because the launch
//     envelope is both a reach a client holds and a kind a client writes: the
//     phone's **stop request** is the schema's sixth kind (P5.2.2's reason, one
//     word along — under the document behind, every stop request is refused at
//     the database with no error), and `config/policy/shared/launch/{recipe,
//     devices/$uid}` is a **grant** to `USER` the paired device did not hold
//     (P3's and J1's reason — the app refuses an utterance outside its own
//     allow-list before it signs anything, so a device that cannot read its
//     entry refuses every launch rather than launching unbounded). The rules do
//     not enforce the envelope; the values are the consumer's and the app's
//     (Q9's consequence), and what the generation records is the read and the
//     write the phone needs to take part at all. J5r (mobile identity-pairing,
//     O6 and O10 — the owner's answer of 2026-11-21 that a device signs with
//     Ed25519, amending D171) moves it to 11 for two of those reasons again: a
//     device's `sig` is a **field it writes** into a message and the heartbeat,
//     so under the document behind every message and every heartbeat it signs
//     is refused at the database with no error (P5.2.2's reason, and the
//     registration's fields accept either scheme for the same reason J5b's
//     device node decides which one), and `launch` is a **verb it writes** — a
//     word the older document's enumeration does not carry, so a phone's start
//     is refused there (P5.1g's reason one shape along).
//   - projectpolicy.PolicyRulesVersion gates the policy nodes, which neither
//     change touches. It stays 1.
//   - rules.CredsRulesVersion is the credentials family's own generation, at 1,
//     which a cascade launch, the init stage and doctor read (C1.19-C1.21).
//   - rules.OrchestratorRulesVersion is the orchestrator family's own
//     generation, at 2 (marker.go), which the daemon and the CLI read before
//     they write the state node (O2, O3, O5). It is a marker of its own rather
//     than a bump of the first number for C1.3's reason: a bump drops every
//     published policy on an installation whose owner has not redeployed the
//     rules yet, and nothing gates the subtree on the document generation. E12
//     (F27) moves it to 2 because it adds a rule *under* the subtree — the
//     reports job's read of the escalation block — and generation 1's document
//     refuses that read, so a job running against it reads nothing there: the
//     two documents are not interchangeable, which is what the marker records.
//
// Hand mutation this fails on: bumping any of the four (or lowering one of the
// family versions), which is what a reviewer must see argued for.
func TestRulesVersionUnchanged(t *testing.T) {
	if projectpolicy.RulesVersion != 11 {
		t.Errorf("projectpolicy.RulesVersion = %d, want 11: the generation that carries the device's signature in place of the tag and the typed launch action (mobile identity-pairing tasks J5r, O6 and O10)", projectpolicy.RulesVersion)
	}
	if projectpolicy.PolicyRulesVersion != 1 {
		t.Errorf("projectpolicy.PolicyRulesVersion = %d, want 1: this change does not touch the policy nodes", projectpolicy.PolicyRulesVersion)
	}
	if CredsRulesVersion != 1 {
		t.Errorf("CredsRulesVersion = %d, want 1: the first generation that carries the credentials children", CredsRulesVersion)
	}
	if PathCredsRulesMarker() != "config/limits/credsRules" {
		t.Errorf("PathCredsRulesMarker() = %q, want config/limits/credsRules", PathCredsRulesMarker())
	}
	if PathCredsRulesMarker() == projectpolicy.PathRulesMarker() {
		t.Errorf("the credentials marker is the rules marker: %q", PathCredsRulesMarker())
	}
	if OrchestratorRulesVersion != 2 {
		t.Errorf("OrchestratorRulesVersion = %d, want 2: the generation that adds the reports job's read of the ledger's escalation block under /%s (E12, F27)", OrchestratorRulesVersion, PathOrchestrator)
	}
	if PathOrchestratorRulesMarker() != "config/limits/orchestratorRules" {
		t.Errorf("PathOrchestratorRulesMarker() = %q, want config/limits/orchestratorRules", PathOrchestratorRulesMarker())
	}
	if PathOrchestratorRulesMarker() == projectpolicy.PathRulesMarker() || PathOrchestratorRulesMarker() == PathCredsRulesMarker() {
		t.Errorf("the orchestrator marker is another family's: %q", PathOrchestratorRulesMarker())
	}
}

// TestCredsRulesMarkerIsRunReadable: a `cascade` launch reads the credentials
// marker with the run's own token (C1.19), so the location has to be granted to
// TOKEN. It inherits that from `config/limits`'s own `.read`, which is what
// makes the marker cheap to read and impossible to write from a run: no
// location under /config carries a write a client could satisfy.
//
// Hand mutation this fails on: adding a `.write` under config/limits (the
// marker becomes forgeable by any run of the project), or removing the
// config/limits read grant.
func TestCredsRulesMarkerIsRunReadable(t *testing.T) {
	paths := generatedPaths(t)
	parent, ok := paths["config/limits"]
	if !ok {
		t.Fatal("the template does not generate /config/limits")
	}
	if v, _ := parent[".read"].(string); !strings.Contains(v, "auth.token.fp != null") {
		t.Errorf("/config/limits .read = %v, want the TOKEN grant the marker inherits", parent[".read"])
	}
	for _, p := range []string{PathCredsRulesMarker(), projectpolicy.PathRulesMarker()} {
		if !strings.HasPrefix(p, "config/limits/") {
			t.Errorf("%s is not under /config/limits: it does not inherit the run-token read", p)
		}
	}
}

// TestRunCannotWriteCredsRulesMarker and TestRunCannotWritePolicyRulesMarker
// pin, without the emulator, that no location under /config grants any client a
// write — so neither marker can be forged by a run token. `policyRules` had no
// such pin; this adds it beside the new one.
//
// The markers are the whole reason a run can be refused before it starts: a
// token that could write `credsRules` could make a `cascade` launch pass a gate
// the installation has not met. The emulator proves the refusal with a real
// token (creds_emulator_test.go); this proves the absence of a grant, which is
// what makes it true everywhere at once.
//
// Hand mutation this fails on: granting /config/limits or /config a write (a
// TOKEN write is the shape an "init writes it as its own account" mistake takes
// — the deploy writes it over IAM, which bypasses the rules).
func TestRunCannotWriteCredsRulesMarker(t *testing.T) {
	assertNoConfigWrites(t, PathCredsRulesMarker())
}

func TestRunCannotWritePolicyRulesMarker(t *testing.T) {
	assertNoConfigWrites(t, projectpolicy.PathRulesMarker())
}

func assertNoConfigWrites(t *testing.T, marker string) {
	t.Helper()
	walk(rulesTree(t), nil, func(p []string, attrs map[string]any) {
		if len(p) == 0 || p[0] != "config" {
			return
		}
		if w, has := attrs[".write"]; has && w != false {
			t.Errorf("/%s: .write = %v under /config, where the markers are written over IAM only", strings.Join(p, "/"), w)
		}
	})
	// The marker is data under a location the template carries (config/limits):
	// the run's token reaches it through that node's own read grant, and there
	// is no node of its own to grant anything on.
	parent := marker[:strings.LastIndexByte(marker, '/')]
	if _, ok := generatedPaths(t)[parent]; !ok {
		t.Errorf("%s has no parent location (%s) in the generated rules", marker, parent)
	}
}

// ---- LM5: the issuance record's place (launcher-does-not-mint) --------------
//
// C1 places the issuance record under /creds (identitywire.PathRecord), the
// subtree the document already closes, and its LM5 pins that place here. The
// family's own node denies every client already, so this task generates no rule
// for the record and changes none (F26).

// issuanceSlug and issuanceRun are the pair the record's path is keyed by, of
// the shape a real slug and run id have. They are names in a path and not
// values: nothing under the record's path is read here.
const (
	issuanceSlug = "aurora"
	issuanceRun  = "fb-20261120-041919-8271"
)

// TestRulesKeepTheIssuanceRecordClosed (LM5, R3, F26): the issuance record is
// refused a read and a write to every caller that can hold a token of this
// database — a run's token, a person's, a device's and a caller with no token
// at all — and the document carries no rule of its own for it.
//
// The record is the service's state for one run's identity: who asked, the
// claims the service will sign, the account the claim must come from and the
// digests of the ticket and the nonce (R3). None of it is a client's to read,
// and none of it is a client's to write.
//
// The place is not incidental, which is what F26 found. R3 first put the record
// beside the run's budget record; a run's node grants its own token a read
// (`.read: "RUN"`), and an RTDB grant cascades to every child, so a record
// there would be readable — and its fields writable — by the run it names. The
// contract moved it under /creds, whose node is the booleans false. The
// contrast at the end of this test is that finding held in code, and it is
// this test's red step: pointed at that path instead of the contract's, the
// walk below fails on the run's own grant.
//
// Hand mutation this fails on: adding any node under /creds (both the walk over
// the document's own locations and the governing walk see it), opening either
// attribute of /creds, and moving the record's family out from under it — the
// mutation F26 was, which the contrast refuses a second time.
func TestRulesKeepTheIssuanceRecordClosed(t *testing.T) {
	// The path is the contract's own spelling and not a literal here: C1 owns
	// the record's place, and this is the test its package doc names as the one
	// that pins it, so a move of the record and this proof cannot drift.
	record := identitywire.PathRecord(issuanceSlug, issuanceRun)

	// Every location that governs a read or a write of the record — its
	// ancestors, the path itself, and anything that reaches it through a
	// wildcard segment — carries nothing but the boolean false. That literal is
	// what makes the denial true for every caller at once: RTDB evaluates it
	// before it reads a claim, so it refuses a run's token (the fp, fs and fr
	// claims), a device's (the mu claim the mobile reader is built on), a
	// signed-in person — whom no grant admits on its own — and a caller with no
	// token at all, alike. An expression here would be a grant to the class it
	// names, and the boolean true a grant to everyone.
	gov := governingPaths(t, record)
	for p, attrs := range gov {
		for _, a := range []string{".read", ".write"} {
			if v, has := attrs[a]; has && v != false {
				t.Errorf("/%s %s = %v, want the boolean false: no client token may read or write the issuance record (F26)", p, a, v)
			}
		}
	}

	// The nearest of them is the family's own node, and it carries the denial
	// itself: an inherited deny is a different document from one that says so
	// where a reader of the plan looks for it.
	attrs, ok := gov[credsRoot]
	if !ok {
		t.Fatalf("the nearest generated ancestor of %s is not /%s (%v): the record must sit under the subtree the document closes (R3, F26)", record, credsRoot, keys(gov))
	}
	for _, a := range []string{".read", ".write"} {
		if v, has := attrs[a]; !has || v != false {
			t.Errorf("/%s %s = %v, want an explicit false (F26)", credsRoot, a, v)
		}
	}

	// And the generator spells nothing of the record's family: the record's
	// path is a path and not a location of the document, and the walk above is
	// the whole of what closes it. This is F26's "no rule is generated".
	// TestRulesPreexistingPathsUnchanged is the other half of the pin: a node
	// that appeared under /creds is not an addition any task named
	// (isNewCredsPath).
	if !strings.HasPrefix(record, credsRoot+"/") {
		t.Fatalf("the record's path %q is outside /%s: it must sit under the subtree the document closes (R3, F26)", record, credsRoot)
	}
	family := record[:strings.LastIndex(record, "/")] // creds/issuance/<slug>
	family = family[:strings.LastIndex(family, "/")]  // creds/issuance
	for p := range generatedPaths(t) {
		if p == family || strings.HasPrefix(p, family+"/") {
			t.Errorf("the generated rules carry /%s: the record's family is closed by /%s alone, and a node under it is a rule this task did not ask for (F26)", p, credsRoot)
		}
	}

	// The contrast that makes the placement load-bearing: the path R3 first
	// chose is governed by the run's own node, whose read is the run's token.
	// The walk above comes back with a grant there and with the boolean false
	// at the record's place, and the placement is the whole of the difference.
	beside := budget.PathRun(issuanceSlug, issuanceRun) + "/issuance"
	runGrant := false
	for _, a := range governingPaths(t, beside) {
		if v, has := a[".read"]; has && v != false {
			if r, _ := v.(string); strings.Contains(r, "auth.token.fs") && strings.Contains(r, "auth.token.fr") {
				runGrant = true
			}
		}
	}
	if !runGrant {
		t.Errorf("no location governing /%s carries the run's own read: F26's finding — that a run reads its own node, so a record beside it is a record the run reads — is what makes the record's placement load-bearing", beside)
	}
}
