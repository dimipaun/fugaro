package cli

import (
	"fmt"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// doctor_workspace.go is doctor's own line for the one workspace state the
// workspace's root cannot converge: an installation that declares a workspace of
// its own while its Firebase is another project's. `fugaro update` skips the
// workspace's own project there and converges every stage behind it
// (workspace_create.go's declared, and the HARD RULE), so nothing in that run's
// output is left behind for the owner to find later — this check is.
//
// It reads the local config and nothing else: no cloud, no credentials.

// workspaceSplitCheckID is doctor's line about a workspace's own installation
// whose Firebase is another project's.
const workspaceSplitCheckID = "workspace-firebase"

// doctorWorkspaceSplitCheck is that check: nil for every installation that is
// not a workspace's own, and for a workspace's own project whose Firebase is its
// own, which is every installation this root converges.
//
// A warning and not a failure: the workspace's own service account is not
// created and the workspace does not work until the two projects are one, but
// the installation itself converges and every run it launches keeps running —
// and the fix is the owner's own edit plus a command, which doctor performs
// neither of.
func doctorWorkspaceSplitCheck(lc *localcfg.Config) *doctorCheck {
	if !workspaceDeclared(lc) || workspaceSplitFirebase(lc) == "" {
		return nil
	}
	return &doctorCheck{ID: workspaceSplitCheckID, Severity: "warning",
		Problem: fmt.Sprintf("project %s declares a workspace of its own, and its Firebase is another project's: the config records budget.firebase_project %s, so the workspace's database is %s's while the project the workspace is named after — whose own service account and single grant fugaro update creates — is %s. fugaro update skips the workspace's own project there (no service account is created and no grant is made) and converges everything else, so the workspace does not work until the two are one project (docs/workspaces/spec.md §3)", lc.Name, lc.Budget.FirebaseProject, lc.Budget.FirebaseProject, lc.GCPProject),
		Fix: "`fugaro update --firebase-project " + firebaseProjectIDWord(lc.GCPProject) +
			"` makes the two one project and creates the workspace's service account and its grant." + adoptOwnFirebaseProject(lc)}
}
