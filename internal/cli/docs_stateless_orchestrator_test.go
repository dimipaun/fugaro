package cli

// The stateless orchestrator design folder, pinned.
//
// docs/stateless-orchestrator/ is design-only: spec.md (the owner's, verbatim,
// never edited), README.md, reconciliation.md (F1-F23, the decisions
// D1220-D1243) and implementation-plan.md (O0-O19). Nothing in it pins a
// command or a flag. What rots silently is pinned here: the block the decisions
// live in, the one answer that is not the recommendation, the owner questions
// the round-1 security review raised and left open, and the absence of any
// secret-shaped literal.

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	soDir        = "docs/stateless-orchestrator/"
	soReconciled = soDir + "reconciliation.md"
	soPlan       = soDir + "implementation-plan.md"
	soReadme     = soDir + "README.md"
)

func soRead(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile("../../" + filepath.FromSlash(rel))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return string(b)
}

func TestDocsStatelessOrchestratorDecisionsAreWritten(t *testing.T) {
	rec := soRead(t, soReconciled)
	for n := 1220; n <= 1243; n++ {
		id := "D" + strconv.Itoa(n)
		if !regexp.MustCompile(`(?m)^\| `+id+` \|`).MatchString(rec) && !strings.Contains(rec, "- **"+id+".") && !strings.Contains(rec, id+" (") && !strings.Contains(rec, id+", D") && !strings.Contains(rec, id+" are") {
			t.Errorf("%s: %s is not written anywhere", soReconciled, id)
		}
	}
	for _, f := range []string{soReadme, soReconciled, soPlan} {
		if !strings.Contains(soRead(t, f), "D1244-D1249") {
			t.Errorf("%s: the reserved remainder D1244-D1249 is not stated", f)
		}
	}
}

func TestDocsStatelessOrchestratorD1235IsNotTheRecommendation(t *testing.T) {
	rec := soRead(t, soReconciled)
	for _, line := range strings.Split(rec, "\n") {
		if strings.HasPrefix(line, "| D1235 |") {
			if !strings.Contains(line, "NOT THE RECOMMENDATION") {
				t.Errorf("the D1235 answer cell lost 'NOT THE RECOMMENDATION'")
			}
			return
		}
	}
	t.Errorf("%s: no D1235 row", soReconciled)
}

func TestDocsStatelessOrchestratorOpenQuestionsStayOpen(t *testing.T) {
	rec := soRead(t, soReconciled)
	for _, id := range []string{"D1241", "D1242", "D1243"} {
		if !strings.Contains(rec, "**"+id+".") {
			t.Errorf("%s: the open owner question %s is gone from the open tensions", soReconciled, id)
		}
	}
	for _, want := range []string{"mitigation, not a barrier", "ClaimKey()", "bypasses the rules"} {
		if !strings.Contains(rec, want) {
			t.Errorf("%s: the corrected security statement %q is gone", soReconciled, want)
		}
	}
}

func TestDocsStatelessOrchestratorHasNoSecretShapedLiteral(t *testing.T) {
	re := regexp.MustCompile(`AKIA[0-9A-Z]{12,}|ghp_[A-Za-z0-9]{20,}|sk-[A-Za-z0-9]{20,}|-----BEGIN [A-Z ]*PRIVATE KEY`)
	for _, f := range []string{soReadme, soReconciled, soPlan} {
		if m := re.FindString(soRead(t, f)); m != "" {
			t.Errorf("%s: secret-shaped literal %q", f, m)
		}
	}
}
