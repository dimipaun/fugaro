package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// This file is issue #3686's second half: retiring a shared project job.
//
// A project job is one instance of modules/project per (profile, provider,
// account), its for_each key IS the job's Cloud Run name, and the module
// protects the job from deletion. So the job of a triple the configuration no
// longer declares is an instance nothing can delete: an apply let through
// removes its launchers' grants and then fails on the deletion protection,
// leaving a job nobody can launch. That is why internal/infra's
// RefuseProjectJobDeletes stops the whole update on such a plan, and why the
// retirement has to happen BEFORE the plan that would carry it:
//
//   - the addresses of an instance this run retires leave Terraform's state,
//     which destroys nothing (infra.Removals: the job's own address is the
//     protected one, and only it);
//   - the job itself is deleted through the Cloud Run API, under the labels
//     its own state row carries (infra.ProjectJobStateLabels);
//   - the apply then deletes what the instance owned beside it — its service
//     account and its grants — from a plan whose deletes are the
//     retirement's own addresses and nothing else (infra.RetireAllowDelete).
//
// # An instance a run is on is left exactly as it is
//
// Deleting a job cancels the execution on it, and the run that dies with it
// is a person's work going away with no report anywhere (infra.InFlightJobs'
// own rule). So the question is asked before anything is deleted, and an
// instance the answer keeps — an execution that has not finished, an
// execution list that could not be read, a delete this operator may not make,
// a listing that could not be read at all — is not taken apart in any part:
// its job, its service account, its grants and its state rows all stay, and
// the job goes back into the document this run plans, whole, out of the
// record the machine holds of the installation (carryKeptProjectJobs). The
// plan then produces no address of it, and the next update retires it once
// the execution has finished. That is the same shape the project copies use
// (update_retire_copies.go's G4): the state and the record are not what makes
// a job findable — its own labels are — and what the retirement deletes is
// only ever what it has left whole behind it.
//
// Nothing this step does fails an update by itself. A listing that could not
// be read deletes nothing this run and the next one asks again; a delete this
// operator may not make is a warning naming the gcloud command that finishes
// it; the state removal is the one half a failure is not forgiven on, and that
// is Terraform's own refusal to follow (RefuseProjectJobDeletes names the
// address it refuses).
//
// # Why the plan is not the source, and the document is
//
// What this run stops managing is read from the STATE and from the document
// the plan itself reads (the workdir's terraform.tfvars.json, every carry
// included): a job the config stopped composing but a carry kept is one the
// plan still produces, and a retirement asking the config instead would
// delete what a release is keeping (infra.ProjectJobRetirements).

// retiresProjectJobs is whether this door retires a project job at all: the
// upgrade and the account switch, which are the two ways a config stops
// declaring a job's triple — the owner edits the profile, or the switch moves
// its account. Both converge the same installation root, and both leave the
// installation with nothing half-done, which is what the retirement is for.
func retiresProjectJobs(door string) bool {
	return door == updateDoor.name || door == accountsSwitchDoor
}

// retireProjectJobs retires the project jobs the document this run applies no
// longer declares and leaves whole the ones no run may be taken off, and
// returns the addresses of the retirements for the plan's own allow-list.
// It runs after terraform is initialised and before the first plan, because
// the state removal is the mutation that plan depends on.
//
// The returned set is every address of the instances this run retires (the
// job, its service account and its grants), which is what the guard and the
// managed gate are given: the plan's deletes at those addresses are this run's
// own, and at no other address may a delete pass. An instance this run keeps
// is not among them, and its rows stay in the state.
func (r *initRun) retireProjectJobs(ctx context.Context, c *infra.Clients, t *tf.TF, wd *infra.Workdir, spec infra.InstallationSpec) ([]infra.Retirement, error) {
	// --plan-only changes nothing anywhere, and both halves of this step do.
	if r.o.planOnly || !retiresProjectJobs(r.o.doorName()) {
		return nil, nil
	}
	// A coding agent's session the gate admitted is admitted to the plan's
	// document, and the job's delete and the state removal are no part of it:
	// the next update in the owner's own terminal retires them.
	if r.gateLeavesTheIAMCall() {
		return nil, nil
	}
	vars, err := wd.Vars()
	if err != nil {
		return nil, userErr("reading the installation's tfvars: %v", err)
	}
	declared, err := infra.DeclaredProjectJobs(vars)
	if err != nil {
		return nil, userErr("%v", err)
	}
	st, err := t.ShowState(ctx)
	if err != nil {
		return nil, remote(err)
	}
	rets, err := infra.ProjectJobRetirements(vars, st)
	if err != nil {
		return nil, userErr("%v", err)
	}
	if len(rets) == 0 {
		return nil, nil
	}
	// A row no update retires by itself stops the whole set: a table entry
	// this program has not decided on, or a resource the project module grew
	// that holds data, is not something an unasked update takes down, and
	// taking the rest of the instance apart around it would be half a
	// retirement. Nothing was removed and nothing was deleted when this fires.
	if stops := infra.ProjectJobRetireStops(rets); len(stops) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "this update does not retire the %d project %s its configuration no longer declares: nothing was removed from Terraform's state and nothing was deleted.\n",
			len(stops), plural(len(stops), "job resource", "job resources"))
		for _, s := range stops {
			fmt.Fprintf(&b, "  %s — %s\n", s.Address, s.Why)
		}
		b.WriteString("The project module is not one this release retires: report it, and keep the job declared until then.")
		return nil, userErr("%s", b.String())
	}
	deleted, kept := r.deleteRetiredProjectJobs(ctx, c, spec, declared, rets)
	// An instance this run keeps stays WHOLE: its rows stay in Terraform's
	// state, no delete at its addresses is admitted to the plan, and its job
	// goes back into the document the plan is made from, out of the record this
	// machine holds (carryKeptProjectJobs). Nothing about it is this run's to
	// take apart, so the plan produces no address of it, and the next update
	// retires it once the execution has finished. Removing its job's row
	// instead — what this step used to do — left the apply below free to
	// destroy the service account and the grants the run in flight reaches the
	// runs bucket and Vertex through; and leaving the row in place without the
	// job in the document is a plan that deletes a protected job, which refuses
	// the whole update.
	rows := infra.ProjectJobRetiredRows(rets, kept)
	if len(kept) > 0 {
		if err := r.carryKeptProjectJobs(wd, kept); err != nil {
			return nil, err
		}
	}
	// The addresses of the instances this run retires leave Terraform's state
	// before the plan, which destroys nothing: the job is already gone (it was
	// deleted above, or somebody deleted it by hand), and what the instance
	// owned beside it is the apply's.
	removed, gone, err := infra.RemoveFromState(ctx, t.StateRm, infra.Removals(st.Addresses(), rows))
	if err != nil {
		r.warn(fmt.Sprintf("the project jobs this update retires stay in Terraform's state: %s", safetext.FieldErr(err)))
		return rows, nil
	}
	if len(removed) > 0 {
		fmt.Fprintf(r.w, "removed %d retired project %s from Terraform's state; nothing was destroyed\n", len(removed), plural(len(removed), "job", "jobs"))
	}
	for _, a := range gone {
		r.warn(a + " was already out of Terraform's state")
	}
	if deleted > 0 || len(kept) > 0 {
		fmt.Fprintf(r.w, "retired project jobs: %d deleted, %d left for a later update\n", deleted, len(kept))
	}
	return rows, nil
}

// carryKeptProjectJobs puts the jobs of the kept instances back into the
// workdir's document, whole, out of the record this machine holds (the tfvars
// the workdir held before this run, infra.Workdir.PrevVars): the plan this run
// makes then produces no address of them, which is what "left exactly as it is"
// means for an instance this run must not take apart.
//
// A name the record does not hold is one nothing can be carried for, and it is
// said in one line rather than passed in silence: this run cannot keep that
// instance whole, so the plan below refuses it, and the update that meets the
// job with no execution on it retires it.
func (r *initRun) carryKeptProjectJobs(wd *infra.Workdir, names []string) error {
	vars, err := wd.Vars()
	if err != nil {
		return userErr("reading the installation's tfvars: %v", err)
	}
	next, carried, err := infra.CarriedProjectJobNames(vars, wd.PrevVars, names)
	if err != nil {
		return userErr("%v", err)
	}
	if len(carried) > 0 {
		if err := wd.WriteVars(next); err != nil {
			return userErr("writing the installation's tfvars: %v", err)
		}
	}
	carriedSet := make(map[string]bool, len(carried))
	for _, n := range carried {
		carriedSet[n] = true
	}
	for _, n := range names {
		if !carriedSet[n] {
			r.warn(fmt.Sprintf("project job %s: this machine's record does not name it, so it cannot be left whole; retry once the run on it has finished", n))
		}
	}
	return nil
}

// deleteRetiredProjectJobs deletes, through the Cloud Run API, every job of
// this installation the document no longer declares and that has no execution
// in flight, and reports how many went and the names of the instances it left.
//
// The set is the CLOUD's, not the state's: a job is findable by the labels
// the project module writes on it, so a job an earlier update could not
// delete — a run in flight then, a call that operator could not make — is one
// this run, or the next, finishes. A job a later config declares again is
// skipped here and kept by the plan, exactly as its state row is.
func (r *initRun) deleteRetiredProjectJobs(ctx context.Context, c *infra.Clients, spec infra.InstallationSpec, declared map[string]map[string]bool, rets []infra.Retirement) (deleted int, kept []string) {
	live, answered, warning := infra.LiveProjectJobs(ctx, c, spec.Project, spec.Region)
	if !answered {
		// Fail closed: "could not look" and "nothing is there" must never be
		// the same answer for a call that deletes a job. Every instance stays,
		// one update older than the run that may be on it, and each of them is
		// left whole rather than taken apart around a job this run cannot read:
		// the state rows stay, and the jobs go back into the document the plan
		// is made from.
		r.warn(warning)
		return 0, infra.ProjectJobInstanceNames(rets)
	}
	for _, j := range live {
		if declared[infra.ProjectJobAxis][j.Name] || declared[infra.ProjectReviewJobAxis][j.Name] {
			continue
		}
		busy, read, why := infra.JobInFlight(ctx, c, spec.Project, j.Region, j.Name)
		if !read {
			r.warn(fmt.Sprintf("project job %s: %s, so it stays and a later update asks again", j.Name, why))
			kept = append(kept, j.Name)
			continue
		}
		if busy {
			r.warn(fmt.Sprintf("project job %s: a run in flight is on it, so it is not destroyed; a later update retires it", j.Name))
			kept = append(kept, j.Name)
			continue
		}
		// The job is deleted under the labels its own STATE ROW carries, never
		// under the ones the listing that found it matched: those are the pairs
		// the listing filtered on, so a delete judged by them is judged by
		// nothing, while a job whose labels have moved off the recorded ones is
		// not the job this run is finishing (infra.HasMarks, in
		// deleteOrphanedJob).
		want := infra.ProjectJobStateLabels(rets, j.Name)
		if want == nil {
			// A state that holds no row for it: an earlier run removed the
			// address and its own delete did not land, so the marks the listing
			// matched by — this installation's own label and the profile — are
			// the whole of what is left to check.
			want = map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProfile: j.Profile}
		}
		if err := deleteOrphanedJob(ctx, c, spec.Project, j.Region, j.Name, want); err != nil {
			r.warn(fmt.Sprintf("project job %s: it could not be deleted (%s), so it stays and a later update retries", j.Name, safetext.FieldErr(err)))
			kept = append(kept, j.Name)
			continue
		}
		deleted++
	}
	return deleted, kept
}

// accountsSwitchJobRefusal is `accounts switch`'s own half of the same rule,
// asked BEFORE the write: a switch takes the FROM account out of every
// profile's list that names it, so the job of each of those profiles' (from)
// triple stops being declared — and a job that cannot be retired leaves the
// config half-switched if the write lands first (the failure issue #3686
// records: nine keys written, then a refusal on two protected jobs, and the
// block put back by hand).
//
// What makes a job one this command may not switch away from is the one thing
// that stops the retirement: an execution still running on it, which deleting
// the job would cancel. Everything else the retirement does by itself. The
// read is of the CLOUD and never of the config: which jobs the installation
// runs is a fact of the installation, and the profile's own list says nothing
// about the job an earlier release deployed for it.
//
// A read that could not be made is the refusing direction too: what cannot be
// seen cannot be promised, and this is the command whose whole point is that
// nothing is half-written.
func accountsSwitchJobRefusal(ctx context.Context, lc *localcfg.Config, keys []accountsSwitchKey, from, command string) error {
	profiles := map[string]bool{}
	for _, k := range keys {
		if k.profile != "" {
			profiles[k.profile] = true
		}
	}
	if len(profiles) == 0 {
		// No profile's list moves: the job axis of this installation is not
		// this switch's business.
		return nil
	}
	c, err := newInitClients(ctx, lc)
	if err != nil {
		return err
	}
	live, answered, warning := infra.LiveProjectJobs(ctx, c, lc.GCPProject, lc.Region)
	if !answered {
		return userErr("refusing to switch %s: this installation's shared jobs could not be read (%s); nothing was written", command, warning)
	}
	for _, j := range live {
		if !profiles[j.Profile] || j.Account != from {
			continue
		}
		busy, read, why := infra.JobInFlight(ctx, c, lc.GCPProject, j.Region, j.Name)
		if read && !busy {
			continue
		}
		if !read {
			return userErr("refusing to switch %s: whether a run is on %s, the shared job of profile %s, could not be read (%s); nothing was written",
				command, j.Name, j.Profile, why)
		}
		return userErr("refusing to switch %s: a run is in flight on %s, the shared job of profile %s; nothing was written. Rerun this once it finishes",
			command, j.Name, j.Profile)
	}
	return nil
}
