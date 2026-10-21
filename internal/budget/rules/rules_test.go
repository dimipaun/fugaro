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
	"github.com/dimipaun/fugaro/internal/projectpolicy"
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
				"exp": true, "counted": true, "calls": true, "micros": true, "in": true, "out": true, "cr": true, "cw": true}[leaf]
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

// ---- The policy nodes (docs/design/project-centric.md §5, phase 1 task 1.2).
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
	for _, p := range []string{shared, repo, admin} {
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
func schemaCatalog(t *testing.T) (consts map[string]int64, fields, required []string) {
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
			}
		default:
			t.Fatalf("shared/mailbox-schema exports %s = %s, which this reader does not understand: teach it, so the number stays checked", name, expr)
		}
	}
	if len(consts) == 0 || len(fields) == 0 || len(required) == 0 {
		t.Fatalf("shared/mailbox-schema: read %d constants, %d fields and %d required fields", len(consts), len(fields), len(required))
	}
	return consts, fields, required
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
// What this test cannot pin is the rest of the schema's judgement, and §6.4
// says where the rules are looser rather than claiming they are the same: the
// id's key rule (no `%`, no control character, no lone surrogate — all of
// which the schema refuses) is the schema's alone, and `text`'s control
// characters with it. The characters the schema states as a pattern (a tag, a
// head SHA, an identity, a target) are checked at the database too, and
// TestMailboxRulesCheckTheSchemasCharacters pins them. The emulator cases
// (emulator_mailbox_test.go, emulator_pairing_test.go) prove what the rules do
// refuse.
func TestMailboxRulesAgreeWithTheSharedSchema(t *testing.T) {
	consts, fields, required := schemaCatalog(t)
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
	} {
		if int64(c.got) != c.want {
			t.Errorf("%s = %d, the schema's own bound = %d", c.what, c.got, c.want)
		}
	}
	if want := []string{"id", "sender", "text", "at", "mac"}; !slices.Equal(required, want) {
		t.Errorf("the schema's REQUIRED_FIELDS = %v, want %v", required, want)
	}
	if want := []string{"id", "sender", "text", "at", "mac", "acked", "agent", "kind", "options", "reply_to", "action"}; !slices.Equal(fields, want) {
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

	// The required fields, from the schema's own list rather than from a literal
	// here: `hasChildren([...])` is what refuses a message with no tag, and a
	// tag is required on every message of every kind and on the heartbeat. The
	// heartbeat's list is three of the four (it has no `id`), which the schema
	// states separately (HEARTBEAT_REQUIRED).
	msgRequired := "newData.hasChildren(['" + strings.Join(required, "', '") + "'])"
	for _, p := range []string{`mailbox/$mb/inbox/$msg`, `mailbox/$mb/outbox/$msg`} {
		attrs, ok := paths[p]
		if !ok {
			t.Errorf("the template does not generate /%s", p)
			continue
		}
		v, _ := attrs[".validate"].(string)
		if !strings.Contains(v, msgRequired) {
			t.Errorf("/%s .validate = %q, want the schema's required fields (%q)", p, v, msgRequired)
		}
	}
	if attrs, ok := paths[`mailbox/$mb/heartbeat`]; !ok {
		t.Error("the template does not generate /mailbox/$mb/heartbeat")
	} else if v, _ := attrs[".validate"].(string); !strings.Contains(v, "newData.hasChildren(['at', 'boot', 'mac'])") {
		t.Errorf("/mailbox/$mb/heartbeat .validate = %q, want the tag required beside at and boot", v)
	}

	// The action record's own required set and the merge's head SHA. Not read
	// from the schema (ACTION_REQUIRED is not exported and this reader does not
	// follow a local constant), so these are pins: a record without its
	// confirmation is the shape G11 calls prose, and a merge without the head it
	// read back is one the consumer cannot check.
	for _, p := range []string{`mailbox/$mb/inbox/$msg/action`, `mailbox/$mb/outbox/$msg/action`} {
		attrs, ok := paths[p]
		if !ok {
			t.Errorf("the template does not generate /%s", p)
			continue
		}
		v, _ := attrs[".validate"].(string)
		for _, frag := range []string{
			"newData.hasChildren(['id', 'verb', 'target', 'confirmation'])",
			"newData.child('verb').val() == 'merge' ? newData.child('head_sha').exists() : true",
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
	// lives on an outbox message), and an outbox message has all of them.
	for _, c := range []struct {
		path string
		want []string
	}{
		{`mailbox/$mb/inbox/$msg`, []string{"id", "sender", "text", "at", "mac", "agent", "kind", "options", "reply_to", "action"}},
		{`mailbox/$mb/outbox/$msg`, []string{"id", "sender", "text", "at", "mac", "acked", "agent", "kind", "options", "reply_to", "action"}},
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
			if !slices.Contains(fields, field) {
				t.Errorf("/%s gates %q, which the shared schema does not name: the rules may gate fewer fields than the schema, never one it does not have", c.path, field)
			}
		}
	}
}

// TestUserReadGrantsAreExactlyTheNamedFamilies: the read grants D112's answer
// adds, and nothing else. The user claim reads the eight expressions a run
// token already carried (each now `… || USER`), the two nodes of its own
// mailbox, and the lease of one job (central-orchestrator D15: the seat is
// what the local orchestrator and the app both have to be able to see, and the
// `fugaro watch` half of P3 renders it) — and a grant anywhere else, on a
// family tension (b1) struck or on a parent that would cascade to one, fails
// here as well as in the golden.
//
// F11's property is the half this pins from the other side: the eight are the
// only families a signed-in user reaches, so an account with no mu claim reads
// nothing (admitted nowhere), and no family of the run's own slices — agents
// and outcomes above all — was widened by this change.
//
// Hand mutation this fails on: granting the orchestrators subtree a read above
// the lease (the whole `orchestrators` family, or a `$job`'s other nodes), or
// granting the lease to anything but the device-allowlisted user claim.
func TestUserReadGrantsAreExactlyTheNamedFamilies(t *testing.T) {
	paths := generatedPaths(t)
	// The eight, the two mailbox nodes, which are scoped to the mailbox the
	// claim owns, and the lease of one job, which is scoped to the job.
	// generatedPaths spells $slug as slugForPath (it is the one wildcard the
	// tests substitute), so the keys below do too.
	want := map[string]bool{
		"config/caps/global":                           true,
		"config/caps/defaults":                         true,
		"config/caps/repos/" + slugForPath:             true,
		"config/kill/global":                           true,
		"config/kill/repos/" + slugForPath:             true,
		"spend/$day/global":                            true,
		"spend/$day/repos/" + slugForPath:              true,
		"spend/$day/runs/" + slugForPath + "/$run":     true,
		"mailbox/$mb/outbox":                           true,
		"mailbox/$mb/heartbeat":                        true,
		"orchestrators/" + slugForPath + "/$job/lease": true,
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
// target are the schema's run and repo shapes. A list (options, agents) is a
// list: present means a child at an index below its bound, and a scalar in
// its place is refused. And a pairing uid is the `p~` uid P6.6 mints.
func TestMailboxRulesCheckTheSchemasCharacters(t *testing.T) {
	pats := schemaPatterns(t)
	for _, c := range []struct{ name, got string }{
		{"HEX_PATTERN", patternHex},
		{"IDENTITY_PATTERN", patternIdentity},
		{"RUN_PATTERN", patternRun},
		{"REPO_PATTERN", patternRepo},
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
			{m + `/agent`, ".matches(" + patternIdentity + ")"},
			{m + `/action/head_sha`, ".matches(" + patternHex + ")"},
			{m + `/action/target/run`, ".matches(" + patternRun + ")"},
			{m + `/action/target/repo`, ".matches(" + patternRepo + ")"},
			{m + `/options`, "newData.hasChildren()"},
			{m + `/options/$i`, "$i.matches(" + indexPattern(MaxOptions) + ")"},
		} {
			wantFrag(t, paths, c.path, c.frag)
		}
	}
	for _, c := range []struct{ path, frag string }{
		{`mailbox/$mb/heartbeat/mac`, ".matches(" + patternHex + ")"},
		{`mailbox/$mb/heartbeat/boot`, ".matches(" + patternIdentity + ")"},
		{`mailbox/$mb/heartbeat/boot`, "newData.val().length <= " + strconv.Itoa(MaxBootLength)},
		{`mailbox/$mb/heartbeat/agents`, "newData.hasChildren()"},
		{`mailbox/$mb/heartbeat/agents/$i`, "$i.matches(" + indexPattern(MaxAgents) + ")"},
		{`mailbox/$mb/heartbeat/agents/$i/id`, ".matches(" + patternIdentity + ")"},
		{`mailbox/$mb/heartbeat/agents/$i/name`, "newData.val().length > 0"},
	} {
		wantFrag(t, paths, c.path, c.frag)
	}
	consts, _, _ := schemaCatalog(t)
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
// is what refuses them, so each arm is pinned whole.
//
// This is the static half: `emulator_pairing_test.go` writes the two both-shapes
// targets and watches the database refuse them.
func TestConfirmTargetsCarryOneShape(t *testing.T) {
	paths := generatedPaths(t)
	for _, dir := range []string{"inbox", "outbox"} {
		p := `mailbox/$mb/` + dir + `/$msg/action/target`
		for _, c := range []struct{ verb, frag string }{
			{"merge", "== 'merge' ? (newData.hasChildren(['repo', 'pr']) && !newData.child('run').exists())"},
			{"cancel", "== 'cancel' ? (newData.hasChildren(['run']) && !newData.child('repo').exists() && !newData.child('pr').exists())"},
			{"confirm", "((newData.hasChildren(['run']) && !newData.child('repo').exists() && !newData.child('pr').exists()) || (newData.hasChildren(['repo', 'pr']) && !newData.child('run').exists()))"},
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
	// Every field set, so an omitempty one is marshalled too.
	entry := budget.AgentEntry{
		Repo: "aurora/web", Workflow: "implement", Title: "t", Stage: "code", Round: 1, Verify: "go",
		Coder: "m", Reviewer: "m", Recipe: "r", Auth: "cascade", PRURL: "https://example.invalid/1",
		Action: "tool Bash", Tokens: 1, StartedAt: 1, StageStartedAt: 1, StageDeadline: 1, UpdatedAt: 1,
		Spent: 1, Halted: "budget", Credential: "a1", WaitingUntil: 2, RequestedBy: "alice@example.invalid",
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
// arrive as part of "we added the credentials children".
//
// Hand mutation this fails on: editing any existing rule (a loosened bound, a
// dropped conjunct) while adding the new nodes.
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
			t.Errorf("/%s changed:\n  was %v\n  now %v", p, want, got)
		}
	}
	// And the additions are the two this document has made since generation 3:
	// the credentials family (C1.3) and the orchestrators lease (central
	// orchestrator P3, which is generation 4). Anything else that appears here
	// is a rules change nobody asked for.
	for p := range after {
		if _, ok := before[p]; ok {
			continue
		}
		if !isNewCredsPath(p) && !isNewLeasePath(p) {
			t.Errorf("/%s is new and is not one of this task's additions", p)
		}
	}
}

// isNewLeasePath reports whether p is one of the nodes the lease family adds:
// the three wildcard levels, the record, and the fields the rules name. An
// explicit set rather than a prefix match, for isNewCredsPath's reason — a path
// *under* the family is a change to what the family contains, and a grant
// hidden under /orchestrators is exactly what a prefix match would wave
// through.
func isNewLeasePath(p string) bool {
	base := "orchestrators/" + slugForPath + "/$job"
	for _, q := range []string{
		"orchestrators",
		"orchestrators/" + slugForPath,
		base,
		base + "/$other",
		base + "/lease",
	} {
		if p == q {
			return true
		}
	}
	for _, f := range []string{"holder", "holder_uid", "generation", "renewed_at", "mode", "pr", "$other"} {
		if p == base+"/lease/"+f {
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
	entry := "agents/" + slugForPath + "/$run/"
	return p == entry+"credential" || p == entry+"waiting_until"
}

// TestRulesVersionUnchanged: the document generation moves for the lease, and
// nothing else does.
//
//   - projectpolicy.RulesVersion is the generation of the whole document, read
//     by the deploy's downgrade refusal, the runner's policy messages and
//     doctor's policy-rules-skew. The lease is a subtree that did not exist,
//     and the seat a holder relies on is only a seat because the deployed rules
//     say so — under a generation-3 document every lease call is refused — so
//     this one moves. It is 4 (central-orchestrator P3).
//   - projectpolicy.PolicyRulesVersion gates the policy nodes, which this
//     change does not touch: no published policy is dropped by the bump and
//     publishing keeps working. It stays 1.
//   - rules.CredsRulesVersion is the credentials family's own generation, at 1,
//     which a cascade launch, the init stage and doctor read (C1.19-C1.21).
//     The credentials children did *not* move the document generation (C1.3's
//     "no RulesVersion bump", marker.go) and this change does not move theirs.
//
// The numbers are asserted rather than referenced so that a bump has to come
// here and say so.
//
// Hand mutation this fails on: bumping any of the four (or lowering
// CredsRulesVersion), which is what a reviewer must see argued for.
func TestRulesVersionUnchanged(t *testing.T) {
	if projectpolicy.RulesVersion != 4 {
		t.Errorf("projectpolicy.RulesVersion = %d, want 4: the lease subtree is a generation of its own", projectpolicy.RulesVersion)
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
