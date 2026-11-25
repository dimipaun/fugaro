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

// ProjectCopiesDropped is whether THIS RELEASE still derives a project-wide
// container per `workflow_secrets` entry. It is off, and the release that
// removes that loop from projectSecretContainers (IS6) turns it on in the same
// commit: retirement is decided by what the release drops, so IS13 ships inert
// on every installation — no local config, no cloud state and no record can
// retire a copy while the release that produced the container is the one
// running.
//
// It is a variable rather than a constant for that one release to flip and for
// the tests that pin both sides of it. Nothing else may write it.
var ProjectCopiesDropped = false

// RetiredProjectCopies is the project copies D3325 retires, once the release
// has stopped deriving them (ProjectCopiesDropped): every name whose value the
// installation already holds in the fugaro_p_<name> grammar — copied, IS5's
// project record written after the copy. The answer is the record's, so it is
// the same for every operator of an installation and never a reading of one
// operator's local config; that is what keeps a copy this run does not retire
// as it was, and what keeps the next operator's `fugaro update` from meeting a
// container Fugaro retired out from under its own config.
//
// The one thing the set is NOT asked to be is what the run then acts on: a name
// the run's OWN plan still produces a container for is subtracted before the
// mark, the delete or the state removal (UnplannedCopies), because the release
// did not drop it here.
//
// The container ID is derived and never read: it is the installation's own name
// for the copy (gcp.ProjectSecretID), so a container that is not under it is
// not this copy and nothing here touches one.
func RetiredProjectCopies(copied []string, fugaroProject string, dropped bool) []ProjectCopy {
	if !dropped {
		return nil
	}
	var out []ProjectCopy
	for _, name := range slices.Sorted(slices.Values(copied)) {
		out = append(out, ProjectCopy{Name: name, ID: gcp.ProjectSecretID(fugaroProject, name)})
	}
	return out
}

// IsRetiredCopy is whether a live project-wide container carries the mark
// retirement leaves: this installation's labels with the logical name on
// LabelRetired and no LabelSecret, which is what takes the container out of the
// union discovery adopts a live container by (N3). Discovery reads it to ADOPT
// such a container when the configuration plans its name again — the module's
// labels map writes LabelSecret back on the apply, so the copy is un-retired by
// the run that still wants it, rather than refused as a foreign container.
func IsRetiredCopy(labels map[string]string, fugaroProject, logical string) bool {
	return labels[gcp.LabelManaged] == gcp.ManagedValue &&
		labels[gcp.LabelProject] == fugaroProject &&
		labels[gcp.LabelRetired] == logical
}

// UnplannedCopies is the retired set minus the names a plan still produces a
// container for: a copy the run's own configuration (before discovery) or the
// plan it is about to apply (after it) holds is left exactly as it is. Asked
// twice, against the two plans a run has — the config's own names, which is the
// half a person edits, and discovery's union of it with every live container,
// which is the half an apply would create. A name that appears in the second
// and not the first is one whose mark could not be written: discovery adopted
// it back by its own label, the plan produces it, and its state row must stay.
func UnplannedCopies(copies []ProjectCopy, planned map[string]string) []ProjectCopy {
	var out []ProjectCopy
	for _, cp := range copies {
		if _, produced := planned[cp.Name]; produced {
			continue
		}
		out = append(out, cp)
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
