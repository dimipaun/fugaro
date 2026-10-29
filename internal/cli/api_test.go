package cli

import (
	"context"
	"strings"
	"testing"
)

// TestCloudBudgetDBRefusesAnotherProjectsDatabase: the opened handle is the
// one thing a moved command gets, so the check the core's own paths make — the
// database at budget.rtdb_url is THIS project's — has to hold behind the
// handle too, or a command across the boundary could write another
// installation's budget.
func TestCloudBudgetDBRefusesAnotherProjectsDatabase(t *testing.T) {
	f := newLaunchBudget(t, "observe")
	env := memEnv(t, f.cloudFixture)
	c := &Cloud{env: env}

	if _, err := c.BudgetDB(context.Background()); err != nil {
		t.Fatalf("the project's own budget database was refused: %v", err)
	}
	f.db.Set("fugaro/project", "their-project")
	_, err := c.BudgetDB(context.Background())
	if err == nil {
		t.Fatal("Cloud.BudgetDB opened another project's budget database")
	}
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "belongs to project") {
		t.Errorf("the refusal = %v (exit %d), want the user error that names the wrong project", err, ExitCode(err))
	}
}

// TestCloudFirestoreNeedsABudgetBlock: the core's own reader checks the
// project's Firebase fields before it reaches for a client, so the handle must
// too. A project config with no budget block is what a project that never ran
// `fugaro update --firebase-project` has, and it is a refusal rather than a
// panic.
func TestCloudFirestoreNeedsABudgetBlock(t *testing.T) {
	cases := []struct {
		name string
		env  func(t *testing.T) *cloudEnv
	}{
		{"no budget block", func(t *testing.T) *cloudEnv {
			env := memEnv(t, newCloudFixture(t))
			env.lc.Budget = nil
			return env
		}},
		{"a budget block with no Firebase project", func(t *testing.T) *cloudEnv {
			f := newLaunchBudget(t, "observe")
			env := memEnv(t, f.cloudFixture)
			env.lc.Budget.FirebaseProject = ""
			return env
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := tc.env(t)
			c := &Cloud{env: env}
			fs, err := c.Firestore(context.Background())
			if err == nil {
				t.Fatalf("Cloud.Firestore opened a client for a project with %s", tc.name)
			}
			if fs != nil {
				t.Error("Cloud.Firestore returned a client beside its error")
			}
			if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "no spend history") {
				t.Errorf("the refusal = %v (exit %d), want the user error that says there is no history", err, ExitCode(err))
			}
		})
	}
}
