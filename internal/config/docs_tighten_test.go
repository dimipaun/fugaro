package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestTightenDocNamesTheKeysTheRuleJudges keeps B4's sentence in
// docs/layered-config/spec.md §4 equal to tightenRule: the keys it lists as
// judged are the ones the merge drops a looser value of, and the two that keep
// the replace rule are named as such and not listed there.
//
// A key in the list the rule replaces tells a reader a repository's value can
// be dropped when it never is, and the other way around hides a drop: the
// sentence is the only place the rule is written for a reader (the product doc
// carries no review-depth list).
//
// Mutation (move `agent.recipe` back into the list, or drop one of the judged
// keys out of it): this test fails on the key, not on a restated copy of the
// list.
func TestTightenDocNamesTheKeysTheRuleJudges(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "layered-config", "spec.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	i := strings.Index(doc, "**Review depth and spend tighten only.**")
	if i < 0 {
		t.Fatal("docs/layered-config/spec.md no longer states the tighten-only rule")
	}
	para := doc[i:]
	if j := strings.Index(para, "\n\n"); j >= 0 {
		para = para[:j]
	}
	const open, close = "spends — ", " — the scalar rule above holds"
	a, b := strings.Index(para, open), strings.Index(para, close)
	if a < 0 || b < a {
		t.Fatalf("docs/layered-config/spec.md's sentence moved: it no longer lists the judged keys between %q and %q", open, close)
	}
	list, rest := para[a+len(open):b], para[b:]

	keyRE := regexp.MustCompile("`([A-Za-z0-9_.]+)`")
	var judged []string
	for _, m := range keyRE.FindAllStringSubmatch(list, -1) {
		judged = append(judged, m[1])
		if tightenRule(m[1]) == tightenReplace {
			t.Errorf("docs/layered-config/spec.md lists `%s` among the judged keys, but tightenRule replaces it: the value is never dropped", m[1])
		}
	}
	if len(judged) == 0 {
		t.Fatal("docs/layered-config/spec.md's sentence lists no judged key: the guard checked nothing")
	}
	for _, k := range []string{"agent.recipe", "agent.workspace_beat"} {
		if tightenRule(k) != tightenReplace {
			t.Errorf("tightenRule(%q) is not the replace rule, so %q belongs in the doc's list of judged keys", k, k)
		}
		if !strings.Contains(rest, "`"+k+"`") {
			t.Errorf("docs/layered-config/spec.md never names `%s` where it states the replace rule", k)
		}
		for _, j := range judged {
			if j == k {
				t.Errorf("docs/layered-config/spec.md lists `%s` among the judged keys; it keeps the replace rule", k)
			}
		}
	}
}
