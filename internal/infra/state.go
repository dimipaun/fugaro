package infra

import (
	"slices"
	"strconv"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// projectCopyAddress is the address modules/installation/secrets.tf gives the
// project-wide container of a logical name: one instance of `for_each =
// var.project_secrets`, which is why a key dropped from the variable is an
// instance the state still holds and `prevent_destroy` refuses to plan.
func projectCopyAddress(logical string) string {
	return "module.installation.google_secret_manager_secret.project[" + strconv.Quote(logical) + "]"
}

// ProjectCopy is one of the installation's project-wide copies of a workflow
// secret: the logical name the config knows it by, and the Secret Manager
// container the installation's own naming gives it.
type ProjectCopy struct {
	Name string
	ID   string
}

// RetiredProjectCopies is the project copies D3325 retires: every name whose
// value the installation already holds in the fugaro_p_<name> grammar — copied,
// IS5's project record written after the copy — and that this run's own
// configuration no longer produces a project-wide container for. planned is the
// module's for_each, keyed by logical name (InstallationSpec.ProjectSecrets).
//
// A name the config still plans is not retired, whatever the grammar holds: it
// is what makes this step inert until the release that drops the keys reaches
// the installation (IS6 keeps the copy alive and the run untouched), and it is
// also what keeps the state removal below safe. Removing an instance the same
// plan re-produces would be undone by that plan — discovery's live read unions
// a correctly labelled container back into for_each (N3) and the apply adopts
// its own row back into the state — so the state is only ever touched for a
// name the plan has already stopped producing.
//
// The container ID is derived and never read: it is the installation's own name
// for the copy (gcp.ProjectSecretID), so a container that is not under it is
// not this copy and nothing here touches one.
func RetiredProjectCopies(copied []string, planned map[string]string, fugaroProject string) []ProjectCopy {
	var out []ProjectCopy
	for _, name := range slices.Sorted(slices.Values(copied)) {
		if _, live := planned[name]; live {
			continue
		}
		out = append(out, ProjectCopy{Name: name, ID: gcp.ProjectSecretID(fugaroProject, name)})
	}
	return out
}

// ProjectCopyRemovals is the `terraform state rm` list for a retired set: the
// addresses the state still holds, sorted. An address the state does not hold
// is left out here — RemoveFromState tolerates one, but a run that never names
// it spends no terraform call on it — and one retired copy contributes at most
// one address.
func ProjectCopyRemovals(stateAddrs []string, copies []ProjectCopy) []string {
	held := make(map[string]bool, len(stateAddrs))
	for _, a := range stateAddrs {
		held[a] = true
	}
	var out []string
	for _, cp := range copies {
		if a := projectCopyAddress(cp.Name); held[a] {
			out = append(out, a)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// ProjectCopiesToDelete is the retired copies whose LIVE container this run may
// delete: the ones no deployed job names (G4). mounted is every Secret Manager
// container a deployed Cloud Run job of the installation names (DeployedSecretIDs)
// — this installation's own jobs and every repository's, because a repository's
// job mounts the copy until its own root is re-applied onto the grammar.
//
// Removing an instance from the state destroys nothing, so the container stays
// while anything still reads it: it is marked retired and left alive, out of the
// state, read by the job that names it, and the next update asks the same
// question. Nothing waits for that job to be re-applied — a copy is deleted by
// the update whose own read found nothing naming it, and by no other.
func ProjectCopiesToDelete(copies []ProjectCopy, mounted map[string]bool) []ProjectCopy {
	var out []ProjectCopy
	for _, cp := range copies {
		if mounted[cp.ID] {
			continue
		}
		out = append(out, cp)
	}
	return out
}
