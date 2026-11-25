package cli

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/intake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/secretname"
)

// The retireProjectCopies / removeRetiredCopiesFromState pair is D3325's second
// half (IS13): the project-wide copy of a workflow secret — the container
// `fugaro secrets set NAME --project-wide` made, which D3323 made the only copy
// a run read — leaves the installation once its value is in the fugaro_p_<name>
// grammar and the configuration no longer produces a container for it.
//
// # Why a state removal is part of it
//
// The module's `google_secret_manager_secret.project` is a for_each over
// var.project_secrets and carries prevent_destroy: a key dropped from the
// variable is not a plan that deletes a container, it is a plan terraform
// refuses before Fugaro's own guard runs (U5, N3). So the instance leaves
// Terraform's state first, which destroys nothing, and the plan is then a plan
// about the resources that are left. The copy's live container is kept while
// a deployed job still names it (G4) and deleted by the update whose own read
// finds none: an in-flight run of the release behind mounts the copy its
// template names, and nothing this step does may take that away (CLAUDE.md's
// HARD RULE about an update while runs are in flight).
//
// # The two halves, and why they are where they are
//
// The mark and the delete run BEFORE the installation's discovery, and that
// order is the whole mechanism rather than a preference: discovery unions every
// correctly-labelled project-wide container back into for_each so a config edit
// can never shrink it (N3), and the label that says "workflow secret's project
// copy" is the one it adopts by. Dropping that label — LabelRetired takes its
// place, which carries the same name — is what lets the plan drop the key at
// all; a run that removed the instance from the state without the mark would
// have discovery put it straight back, and the apply would re-adopt its row.
//
// The state removal then runs after terraform is initialised and before the
// first plan, because that is the mutation the plan depends on. Nothing between
// the two halves plans anything.
//
// # Inert until IS6, and why that is a switch
//
// What a release may retire is what that release drops, and nothing else: the
// set comes from IS5's record — the names whose value is in the grammar, the
// same for every operator of the installation — gated on
// infra.ProjectCopiesDropped, the one bit IS6 turns when it removes the
// workflow_secrets loop from projectSecretContainers. While this release still
// derives those containers the bit is off, the set is empty, and this step does
// nothing on any installation, whatever an operator's own config holds and
// whatever the cloud looks like.
//
// The run then subtracts the names ITS OWN plan still produces
// (infra.UnplannedCopies, against the config before discovery and against the
// plan after it): a copy the configuration wants is not retired out from under
// it. That subtraction is one installation's own answer and never a reason for
// another operator's update to fail — discovery adopts a container wearing the
// retirement mark for a name its config plans (discover.go's projectSecrets),
// and the module's labels map writes the copy's own label back on the apply.
// What it never does is run ahead of the record: the names it may retire are
// the ones the project record and the grammar both know, so IS6 finds every
// mount, grant and copy it has to drop named in the record already.
//
// # Nothing here fails an update
//
// Every state this step cannot act in is a warning and the converge goes on:
// a database it cannot read, a container another writer made, a label it could
// not write, a job listing it could not make (nothing is deleted then), a state
// it could not read. The fail-closed direction is the same one on both halves —
// a copy that was not retired keeps its container, its label and its state row,
// and the next update asks again — and never a reason to leave an installation
// half converged (the HARD RULE). The one exception is a state removal that
// failed: the plan that follows is then refused by prevent_destroy, which is
// terraform's own refusal and names the same address the warning does.
//
// A coding agent's session is refused like every other cloud write
// (agentMarker): the mark and the delete are changes outside this machine, and
// what a session gets instead is the one command to run at a person's own
// terminal. It is asked AFTER the retired set is computed — which is reads
// only — so the warning appears when there is something to do and never on the
// sessions that have nothing to retire.
func (r *initRun) retireProjectCopies(ctx context.Context, c *infra.Clients, lc *localcfg.Config, spec infra.InstallationSpec) []infra.ProjectCopy {
	// The upgrade's own step, never a plan-only run: --plan-only changes
	// nothing outside this machine, and both halves of this step do.
	if lc == nil || r.o.doorName() != updateDoor.name || r.o.planOnly {
		return nil
	}
	copied, svc := r.workflowCopiesInGrammar(ctx, lc)
	copies := infra.RetiredProjectCopies(copied, lc.Name, infra.ProjectCopiesDropped)
	// A name the run's own configuration still produces a container for is not
	// this run's to retire, whatever the record holds: it is the same
	// subtraction the state removal makes below, against the config's own half
	// of spec.ProjectSecrets rather than the plan discovery unions it with.
	copies = infra.UnplannedCopies(copies, spec.ProjectSecrets)
	if len(copies) == 0 {
		return nil
	}
	if agentMarker(os.Getenv, r.stdinTerminal()) != "" && !r.gateUnderway() {
		r.warn(fmt.Sprintf("retired workflow-secret copies were left alone: a coding agent's session makes no cloud writes. Run `%s update` in your own terminal", selfCommand()))
		return nil
	}
	// Whether a deployed job still names one, which is the only thing that
	// keeps its container (G4). A read that could not be made answers no IDs
	// WITH a warning, and nothing is deleted then.
	mounted, why := infra.DeployedSecretIDs(ctx, c, spec)
	if why != "" {
		r.warn(why)
	}
	var retired []infra.ProjectCopy
	seen, marked, deleted, kept := 0, 0, 0, 0
	for _, cp := range copies {
		info, found, err := svc.Container(ctx, cp.ID)
		if err != nil {
			r.warn(fmt.Sprintf("workflow secret %s: its project copy could not be read (%s), so it stays", cp.Name, safetext.FieldErr(err)))
			continue
		}
		if !found {
			// The container is gone — an earlier run deleted it and stopped
			// before the state removal — and only the state row is left: the
			// removal below is owed exactly as much.
			retired = append(retired, cp)
			continue
		}
		seen++
		alreadyRetired := info.Labels[gcp.LabelRetired] == cp.Name
		if !alreadyRetired && !infra.HasMarks(info.Labels, projectCopyMarks(lc.Name, cp.Name)) {
			r.warn(fmt.Sprintf("workflow secret %s: %s is not this installation's project copy (it carries %s), so it was left alone", cp.Name, cp.ID, projectCopyMarks(lc.Name, cp.Name)))
			continue
		}
		if !alreadyRetired {
			labels := maps.Clone(info.Labels)
			delete(labels, gcp.LabelSecret)
			labels[gcp.LabelRetired] = cp.Name
			if err := svc.SetLabels(ctx, cp.ID, labels); err != nil {
				r.warn(fmt.Sprintf("workflow secret %s: its project copy could not be marked retired (%s), so the plan still produces it", cp.Name, safetext.FieldErr(err)))
				continue
			}
			marked++
		}
		// Marked, so the state removal below is safe, whatever happens to the
		// container now. A copy an EARLIER update already marked is still ours
		// to delete — it was kept alive then because a job mounted it, or
		// because the delete failed — which is what makes the delete converge
		// over the updates an in-flight run spans (G4).
		retired = append(retired, cp)
		if why != "" || mounted[cp.ID] {
			kept++
			continue
		}
		if err := svc.Delete(ctx, cp.ID); err != nil {
			r.warn(fmt.Sprintf("workflow secret %s: its retired project copy could not be deleted (%s); a later update retries", cp.Name, safetext.FieldErr(err)))
			continue
		}
		deleted++
	}
	if seen > 0 {
		fmt.Fprintf(r.w, "retired workflow-secret copies: %d marked, %d deleted (%d left alive)\n", marked, deleted, kept)
	}
	return retired
}

// removeRetiredCopiesFromState takes each retired copy's instance of
// google_secret_manager_secret.project out of Terraform's state, before the
// plan that would otherwise be refused by the module's prevent_destroy (U5).
// Nothing is destroyed: the container is where it was, and the apply below is
// simply about the addresses that are left.
//
// Only the addresses the state still holds are named, one call each
// (infra.RemoveFromState), so a second door into this root — `fugaro update
// --firebase-project` runs the installation root three times — costs nothing
// after the first.
//
// planned is the plan the apply is about to make (spec.ProjectSecrets, after
// discovery): a retired name it still produces a container for — the
// configuration plans the name again, or the mark could not be written and
// discovery adopted the container back by its own label — keeps its state row,
// or the apply would create a container the state no longer knows about.
func (r *initRun) removeRetiredCopiesFromState(ctx context.Context, t *tf.TF, copies []infra.ProjectCopy, planned map[string]string) {
	copies = infra.UnplannedCopies(copies, planned)
	if len(copies) == 0 {
		return
	}
	st, err := t.ShowState(ctx)
	if err != nil {
		r.warn(fmt.Sprintf("the retired workflow-secret copies are still in Terraform's state, so this plan refuses to drop them: the state could not be read (%s)", safetext.FieldErr(err)))
		return
	}
	removed, gone, err := infra.RemoveFromState(ctx, t.StateRm, infra.ProjectCopyRemovals(st.Addresses(), copies))
	if err != nil {
		r.warn(fmt.Sprintf("a retired workflow-secret copy is still in Terraform's state, so this plan refuses to drop it: %s", safetext.FieldErr(err)))
		return
	}
	if len(removed) == 0 {
		return
	}
	fmt.Fprintf(r.w, "removed %d retired workflow-secret %s from Terraform's state; nothing was destroyed\n", len(removed), plural(len(removed), "copy", "copies"))
	for _, a := range gone {
		r.warn(a + " was already out of Terraform's state")
	}
}

// workflowCopiesInGrammar is the names whose value the installation already
// holds in the fugaro_p_<name> grammar — the interface IS13 consumes, and the
// half of it that IS5 wrote. Three facts have to hold, and all three are read
// fresh:
//
//   - the project record holds the name. IS5 writes an entry only after the
//     copy's value is in the grammar, and the record is what IS6 reads to
//     decide which mounts, grants and project copies go (D3325), so nothing
//     is retired ahead of the document that describes it.
//   - the grammar container carries the intake's workflow-secret marker. The
//     marker is written at create and never after (G2), and it is what tells a
//     workflow's own value apart from a project-shared credential of another
//     class stored under the same name — which is the only thing that may
//     retire a project copy.
//   - the container holds an ENABLED version. The record is IS5's report, and
//     this is the container itself: an operator who disabled the version would
//     otherwise lose the value with the copy that still holds it.
//
// The container names are derived (secretname.Project), never listed, so this
// reads two metadata calls per name and no value.
func (r *initRun) workflowCopiesInGrammar(ctx context.Context, lc *localcfg.Config) ([]string, *gcp.Secrets) {
	// No record, no retirement: the two halves of the migration move together
	// (IS5's header), and an installation with no database has no record for a
	// name to be in.
	if lc.Budget == nil || lc.Budget.RTDBURL == "" {
		return nil, nil
	}
	db, err := newBudgetClient(ctx, lc.Budget.RTDBURL, lc.Endpoints.NoAuth)
	if err != nil {
		r.warn(fmt.Sprintf("workflow-secret copies were not retired: the database the record is in could not be opened (%s)", safetext.FieldErr(err)))
		return nil, nil
	}
	if why := budgetDBUnadopted(ctx, lc, infra.NewDB(db, lc.Name, lc.GCPProject, ""), "workflow-secret copies were not retired", selfCommand()+" update"); why != "" {
		r.warn(why)
		return nil, nil
	}
	rec, found, err := creds.ReadProjectSecrets(ctx, db)
	if err != nil {
		r.warn(fmt.Sprintf("workflow-secret copies were not retired: the record could not be read (%s)", safetext.FieldErr(err)))
		return nil, nil
	}
	if !found || len(rec.Secrets) == 0 {
		return nil, nil
	}
	svc, err := gcp.NewSecrets(ctx, gcpOptions(lc))
	if err != nil {
		r.warn(fmt.Sprintf("workflow-secret copies were not retired: %s", safetext.FieldErr(err)))
		return nil, nil
	}
	var out []string
	for _, name := range slices.Sorted(maps.Keys(rec.Secrets)) {
		ok, err := inGrammar(ctx, svc, name)
		if err != nil {
			r.warn(fmt.Sprintf("workflow secret %s: its value in the %s grammar could not be read (%s), so its project copy stays", name, secretname.ProjectPrefix, safetext.FieldErr(err)))
			continue
		}
		if ok {
			out = append(out, name)
		}
	}
	return out, svc
}

// inGrammar is whether one name's value is readable from the grammar: the
// container exists, it is marked as a workflow's own, and it holds an enabled
// version. The two marked facts are the whole of the answer, and a name that
// fails either is not one anything may be retired for.
func inGrammar(ctx context.Context, svc *gcp.Secrets, name string) (bool, error) {
	id, err := secretname.Project(name)
	if err != nil {
		return false, err
	}
	info, found, err := svc.Container(ctx, id)
	if err != nil || !found {
		return false, err
	}
	if info.Labels[intake.LabelWorkflowSecret] != intake.WorkflowSecretMarker {
		return false, nil
	}
	version, err := svc.LatestEnabled(ctx, id)
	if err != nil {
		return false, err
	}
	return version != "", nil
}

// projectCopyMarks is the label set a container must carry to BE the
// installation's project copy of a logical name: the three labels the module's
// own resource writes, which are the marks discovery checks before it adopts a
// project-wide container (discover.go's projectSecrets). Asked before the
// container is relabelled, so a container of a derived ID that is somebody
// else's is left exactly as it is.
func projectCopyMarks(fugaroProject, logical string) map[string]string {
	return map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProject: fugaroProject, gcp.LabelSecret: logical}
}
