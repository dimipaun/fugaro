package tf

import (
	"slices"
	"strings"
	"testing"
)

// TestScopeMoveReplacesIAMMembersNeverDeletesSecrets reads the one golden plan
// that has a prior state to diff against (scripts/gen-golden-plans.sh, made in
// two real steps). It is design docs/design/project-secrets.md §6 step 4: a
// secret name stops being repo-only and becomes project-shared (the repo copy
// has no enabled version any more, the project copy is resolved), so the
// grants and the jobs' mounted references move to the project's container.
//
// Both halves of that are the point. The container must survive (P2: pruning
// it is a destroy, which prevent_destroy turns into a hard plan failure, and
// P2-extended: nothing may replace it either, since a replacement deletes
// every version first), and the grants must really move — a plan that
// silently kept the old secret_id would show no replacement at all and has to
// fail this test too.
func TestScopeMoveReplacesIAMMembersNeverDeletesSecrets(t *testing.T) {
	p := goldenPlan(t, "repo-scope-move")

	// The grants that move, and the job revisions that mount the new ID.
	expectReplaced := []string{
		`module.repo.google_secret_manager_secret_iam_member.build["bitbucket-token"]`,
		`module.repo.module.workflow["web"].google_secret_manager_secret_iam_member.job["bitbucket-token"]`,
	}
	expectUpdated := []string{
		"module.repo.google_cloud_run_v2_job.check[0]",
		`module.repo.module.workflow["web"].google_cloud_run_v2_job.this[0]`,
	}

	var containers, replaced, updated int
	var gotReplaced, gotUpdated []string
	for _, rc := range p.ResourceChanges {
		switch {
		case strings.HasPrefix(rc.Address, "module.repo.google_secret_manager_secret.this["):
			containers++
			if slices.Contains(rc.Change.Actions, "delete") {
				t.Errorf("%s: actions %v, want no destroy: a scope move keeps the repository's own container (P2)", rc.Address, rc.Change.Actions)
			}
		case rc.Type == "google_secret_manager_secret_iam_member":
			if slices.Contains(rc.Change.Actions, "delete") {
				replaced++
				gotReplaced = append(gotReplaced, rc.Address)
			}
		case rc.Type == "google_cloud_run_v2_job":
			if slices.Contains(rc.Change.Actions, "delete") {
				t.Errorf("%s: actions %v, want a new revision of the same job, never a replace or destroy", rc.Address, rc.Change.Actions)
				continue
			}
			if slices.Equal(rc.Change.Actions, []string{"update"}) {
				updated++
				gotUpdated = append(gotUpdated, rc.Address)
			}
		}
	}
	if containers == 0 {
		t.Fatal("the plan holds no repository secret containers, so it cannot show one surviving")
	}
	slices.Sort(gotReplaced)
	slices.Sort(gotUpdated)
	if !slices.Equal(gotReplaced, expectReplaced) {
		t.Errorf("replaced IAM members %q, want exactly %q", gotReplaced, expectReplaced)
	}
	if !slices.Equal(gotUpdated, expectUpdated) {
		t.Errorf("updated jobs %q, want exactly %q", gotUpdated, expectUpdated)
	}

	// The same plan through the real Guard: the replacements are refused by
	// default (they delete a binding), naming each address, and --allow-delete
	// with exactly them clears it. This proves the plan and the guard agree,
	// rather than re-asserting the guard's own behavior.
	err := Guard(p, nil)
	if err == nil {
		t.Fatal("Guard accepted a plan whose scope move deletes IAM bindings")
	}
	for _, addr := range expectReplaced {
		if !strings.Contains(err.Error(), addr) {
			t.Errorf("Guard's refusal does not name %s: %v", addr, err)
		}
	}
	if err := Guard(p, expectReplaced); err != nil {
		t.Errorf("Guard refused the plan with its replacements named: %v", err)
	}
}
