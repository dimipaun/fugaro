package cli

// The cases of `fugaro report`'s tests that stay with the core (E2.2b, D444,
// T11): they test a symbol the core keeps, not the command, which moved to
// ee/report/cli with its own cases.

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/digest"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// TestReportDimParsing reads the period vocabulary by name. It follows the
// digest's own MIT copy (internal/digest/period.go), because that is the copy
// the core keeps and the one an untagged build spells a period with; the moved
// report's identical copy is held to it by ee/report/period_agree_test.go.
func TestReportDimParsing(t *testing.T) {
	for _, s := range []string{"day", "week", "month", "year", "repo", "model", "person", "mode"} {
		if _, err := digest.ParseDim(s); err != nil {
			t.Error(s, err)
		}
	}
}

// With no_auth and no Firestore endpoint the report must not fall back to the
// real service. newReportFirestore stays in the core for `spendRepos`
// (`internal/cli/project_enroll.go`), the read behind `fugaro project repos
// ls`, which is what this pins.
func TestReportNoAuthNeverReachesRealFirestore(t *testing.T) {
	lc := &localcfg.Config{}
	lc.Endpoints.NoAuth = true
	lc.Budget = &localcfg.Budget{FirebaseProject: "aurora-fp"}
	if _, err := newReportFirestore(context.Background(), lc); err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v", err)
	}
}

func TestInitHelpMentionsFirestoreStep(t *testing.T) {
	out, _, err := execute(t, "setup", "--help")
	if err != nil {
		t.Fatal(err)
	}
	out = strings.Join(strings.Fields(out), " ")
	for _, want := range []string{"Firestore", "us-east5", "permanent", "type the location"} {
		if !strings.Contains(out, want) {
			t.Errorf("fugaro setup --help lacks %q", want)
		}
	}
}
