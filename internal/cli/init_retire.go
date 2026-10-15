package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"google.golang.org/api/googleapi"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
)

// This file is the retirement: the one flow in fugaro that deletes a cloud
// resource fugaro itself created and that Terraform can no longer delete
// (docs/design/config-transitions.md §3.2, §3.3, §4.2).
//
// It is a destructive flow and it is written to be read as one. Three rules
// hold everywhere below, and every guard the design names is kept:
//
//   - Nothing leaves the state, and nothing is deleted, without the project's
//     name typed at a real terminal -- twice. askTyped is the primitive, and
//     ask is never used here: --yes satisfies ask, --json, a pipe,
//     --non-interactive and a coding agent's session satisfy neither.
//   - The state removal is the narrow one Removals computes: the protected
//     addresses the state still holds, and nothing else. An unprotected
//     address stays in the state for the apply to delete -- taking it out
//     would leave it alive in GCP with nothing managing it.
//   - A job is deleted through the Cloud Run API only after its own labels
//     say it is this repository's and this variant's, and only after its
//     delete's long-running operation reached a terminal state.

// retireWait bounds the wait for a job delete's operation. Both are
// variables so a test can shorten the wait rather than sleep through it.
var (
	retireDeletePolls = 30
	retireDeletePoll  = 2 * time.Second
)

// readRetirements is the retirement set of design §3.1a, read before the
// plan: the state's addresses, the labelled jobs and containers Orphans
// finds in the cloud, their union, and the state's own values filled into
// the rows the state holds. It is read-only, so it runs under --plan-only
// too, and it prints the summary whenever the set is non-empty.
//
// A state that cannot be read is an error, not an empty state: the
// difference between "nothing retired" and "could not look" is the whole
// point of the summary, and a run that guessed would silently skip a
// transition (the same rule the Firebase flow's state read follows).
func (r *initRun) readRetirements(ctx context.Context, c *infra.Clients, t *tf.TF, spec infra.RepoSpec) ([]infra.Retirement, error) {
	st, err := t.ShowState(ctx)
	if err != nil {
		return nil, remote(err)
	}
	r.retireState = st.Addresses()
	orphans, notes, warnings, err := infra.Orphans(ctx, c, spec, r.retireState)
	if err != nil {
		return nil, initErr(err)
	}
	for _, w := range warnings {
		r.warn(w)
	}
	r.retireNotes = notes
	if len(notes) > 0 {
		r.printRetireNotes(notes)
	}
	rets := infra.RetireSet(orphans, infra.Retirements(r.retireState, spec))
	// The union's rows carry what the cloud answered; the state fills the
	// rest (Logical, SecretID, Job, Region, Protected), never guessing.
	infra.FillFromState(st, rets)
	r.retireSet = rets
	if len(rets) == 0 {
		return nil, nil
	}
	if err := r.printRetirements(spec, rets); err != nil {
		return nil, err
	}
	return rets, nil
}

// printRetireNotes is the one informational line an already-released kept
// container gets (design §3.1a's convergence rule): it is not a retirement,
// so it must not re-open the gate or re-suggest --retire on every later run,
// but the user is told the container is still there.
func (r *initRun) printRetireNotes(notes []infra.Retirement) {
	var names []string
	for _, n := range notes {
		names = append(names, n.Logical)
	}
	slices.Sort(names)
	fmt.Fprintf(r.w, "note: %s %s no longer mounted by fugaro.yaml and no longer in Terraform's state; %s\n",
		strings.Join(names, ", "), plural(len(names), "is", "are"),
		"the secret and its value stay in Secret Manager, unmanaged")
}

// retireGoingAway splits the set by how each address goes: the jobs the
// Cloud Run API deletes after the apply, and everything else, which the
// plan deletes. Kept rows (a secret container, the registry) are in
// neither: nothing deletes them.
func retireGoingAway(rets []infra.Retirement) (api, apply, kept []infra.Retirement) {
	for _, r := range rets {
		switch {
		case r.Kept != "":
			kept = append(kept, r)
		case r.Job != "":
			api = append(api, r)
		default:
			apply = append(apply, r)
		}
	}
	return api, apply, kept
}

// printRetirements is the ⚠ RETIRE summary of design §3.2: what goes, why,
// what is kept, the order of operations, and the one command that runs it.
// It is printed before the plan, so it carries none of the plan's counts --
// those are the second confirmation's (retirePlan).
func (r *initRun) printRetirements(spec infra.RepoSpec, rets []infra.Retirement) error {
	api, apply, kept := retireGoingAway(rets)
	gone := len(api) + len(apply)
	fmt.Fprintf(r.w, "⚠ This change retires %d %s (project %s, GCP project %s, repository %s).\n",
		len(rets), plural(len(rets), "address", "addresses"), r.projectName, r.gcpProject, spec.Name)
	if secrets := keptSecrets(kept); len(secrets) > 0 {
		fmt.Fprintf(r.w, "  %d go away; %d %s %s in Secret Manager %s.\n",
			gone, len(secrets), plural(len(secrets), "secret", "secrets"), plural(len(secrets), "stays", "stay"),
			plural(len(secrets), "with its value", "with their values"))
	} else {
		fmt.Fprintf(r.w, "  %d go away.\n", gone)
	}
	fmt.Fprintf(r.w, "\nWhy: %s\n", reasons(rets))
	fmt.Fprintf(r.w, "\nGoing away (%d, predicted from the state and the labels) — %d the plan will delete, %d deleted through the Cloud Run API after it:\n",
		gone, len(apply), len(api))
	for _, ret := range slices.Concat(apply, api) {
		fmt.Fprintf(r.w, "  %-72s %s: %s\n", ret.Address, ret.Kind, ret.Cause)
	}
	if removed := r.retireRemovals(rets); len(removed) > 0 {
		fmt.Fprintf(r.w, "\nRemoved from Terraform's state, and never deleted (%d):\n", len(removed))
		for _, a := range removed {
			fmt.Fprintf(r.w, "  %s\n", a)
		}
	}
	if len(kept) > 0 {
		fmt.Fprintln(r.w, "\nKEPT — Fugaro never deletes a secret or a secret version:")
		for _, k := range kept {
			switch {
			case k.SecretID != "":
				fmt.Fprintf(r.w, "  %-32s %s stays in Secret Manager, unmanaged, with its value\n", k.Logical, k.SecretID)
			case k.Logical != "":
				fmt.Fprintf(r.w, "  %-32s stays in Secret Manager, unmanaged, with its value (its Secret Manager ID could not be read)\n", k.Logical)
			default:
				fmt.Fprintf(r.w, "  %s\n", k.Address)
			}
		}
		fmt.Fprintln(r.w, "  Their containers carry the fugaro labels, so re-adding one later re-adopts it.")
	}
	fmt.Fprintf(r.w, "\nOrder of operations:\n  1. remove %d %s from Terraform's state without destroying %s: the addresses no apply can delete (a secret container's prevent_destroy, a job whose deletion_protection the state says is on). Nothing in the cloud is deleted by this step.\n",
		len(r.retireRemovals(rets)), plural(len(r.retireRemovals(rets)), "address", "addresses"), plural(len(r.retireRemovals(rets)), "it", "them"))
	fmt.Fprintln(r.w, "  2. plan, show the plan's own counts and its full delete list, and ask again — the project's name typed at a real terminal, never --yes.")
	fmt.Fprintf(r.w, "  3. apply, then delete the %d orphaned Cloud Run %s through the API.\n", len(api), plural(len(api), "job", "jobs"))
	fmt.Fprintln(r.w, "\nNext command:\n  fugaro init --repo . --retire")
	return nil
}

// retireRemovals is Removals over the state this run read: the protected
// addresses the state still holds, minus the jobs the API deletes, which are
// removed from the state too (that is what makes the plan able to run) and
// are listed under "going away" rather than here.
func (r *initRun) retireRemovals(rets []infra.Retirement) []string {
	api := map[string]bool{}
	for _, ret := range rets {
		if ret.Job != "" && ret.Kept == "" {
			api[ret.Address] = true
		}
	}
	var out []string
	for _, a := range infra.Removals(r.retireState, rets) {
		if !api[a] {
			out = append(out, a)
		}
	}
	return out
}

// keptSecrets are the kept rows that are secrets: the registry is kept too,
// but it is not a secret and the summary says so separately.
func keptSecrets(kept []infra.Retirement) []infra.Retirement {
	var out []infra.Retirement
	for _, k := range kept {
		if k.Logical != "" {
			out = append(out, k)
		}
	}
	return out
}

// reasons are the distinct causes of the set, longest first, as the "Why:"
// line: the config change, in the user's words, deduplicated so fourteen
// addresses do not repeat four sentences fourteen times.
func reasons(rets []infra.Retirement) string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rets {
		if r.Cause != "" && !seen[r.Cause] {
			seen[r.Cause] = true
			out = append(out, r.Cause)
		}
	}
	if len(out) == 0 {
		return "the configuration no longer produces these addresses"
	}
	return strings.Join(out, "; ") + "."
}

// blockingJobs are the retired jobs the ordinary path cannot finish: the
// state still holds them and its own values say deletion_protection = true,
// so no apply can lower the protection (there is no resource left to carry
// the --allow-job-delete variable) and the provider refuses the delete at
// apply time (design §3.3, review B4).
//
// The gate reads the attribute, never "the set contains a job": a job whose
// state says false was already lowered by the user who followed
// docs/gcp-setup.md, and their documented --allow-delete flow must keep
// working.
func blockingJobs(stateAddrs []string, rets []infra.Retirement) []infra.Retirement {
	inState := make(map[string]bool, len(stateAddrs))
	for _, a := range stateAddrs {
		inState[a] = true
	}
	var out []infra.Retirement
	for _, r := range rets {
		if r.Kind != "workflow job" && r.Kind != "check job" {
			continue
		}
		if r.Protected && inState[r.Address] {
			out = append(out, r)
		}
	}
	return out
}

// retireNextCommand is the command the summary and both refusals name.
const retireNextCommand = "fugaro init --repo . --retire"

// retireRefusal is the gate's refusal: the run stops before the apply it
// cannot finish, names the addresses and the one command that can.
func retireRefusal(blocked []infra.Retirement) error {
	var addrs []string
	for _, b := range blocked {
		addrs = append(addrs, b.Address+" ("+b.Job+")")
	}
	return userErr("this change retires %d Cloud Run %s that no apply can delete: the state says deletion_protection = true and the %s left fugaro.yaml, so nothing is left to lower it:\n  %s\nRerun with --retire: it removes %s from Terraform's state first (destroying nothing), applies, then deletes %s through the Cloud Run API:\n  %s",
		len(blocked), plural(len(blocked), "job", "jobs"), plural(len(blocked), "job has", "jobs have"),
		strings.Join(addrs, "\n  "), plural(len(blocked), "it", "them"), plural(len(blocked), "it", "them"), retireNextCommand)
}

// retirePlan is the retirement's own flow, in place of the ordinary plan:
// the first typed confirmation, the state removal, the plan, the second
// typed confirmation with the plan's own counts and delete list in front of
// the user, and the apply (design §4.2). The API delete is not here: it is
// the run's last step, after the last apply, so the re-plan that deploys
// freshly built images cannot race it.
func (r *initRun) retirePlan(ctx context.Context, c *infra.Clients, t *tf.TF, wd *infra.Workdir, spec infra.RepoSpec, rets []infra.Retirement, opts infra.RepoRootOptions, im infra.Imports) error {
	_ = opts // the retirement changes nothing about the root's variables

	// 1. The prediction, confirmed. Nothing has left the state yet, so this
	//    banner is the only place the kept secrets and the job delete are
	//    named before they happen.
	if err := r.retireConfirm(rets, spec); err != nil {
		return err
	}

	// 2. The protected addresses leave the state -- and only them.
	addrs := infra.Removals(r.retireState, rets)
	removed, gone, err := infra.RemoveFromState(ctx, t.StateRm, addrs)
	if err != nil {
		return remote(err)
	}
	fmt.Fprintf(r.w, "removed %d %s from Terraform's state; nothing was destroyed\n", len(removed), plural(len(removed), "address", "addresses"))
	for _, a := range gone {
		r.warn(a + " was already out of Terraform's state")
	}

	// 3. The plan that could not run before, and the guard that always runs.
	changed, err := t.Plan(ctx, infra.PlanFile)
	if err != nil {
		return remote(err)
	}
	if !changed {
		fmt.Fprintf(r.w, "No changes: %s matches the plan.\n", spec.Name)
		r.retireApplied = true
		return nil
	}
	plan, err := t.Show(ctx, infra.PlanFile)
	if err != nil {
		return remote(err)
	}
	counts := infra.CountPlan(plan)
	r.res.Changes = &counts
	// 4. The real plan, in front of the user, and a second typed
	//    confirmation that --yes, --json, a pipe and an agent cannot give.
	fmt.Fprint(r.w, tf.Summary(plan))
	fmt.Fprintln(r.w, im.Bindings.String())
	if err := guard(plan, retireAllowDelete(plan, rets, r.o.allowDelete)); err != nil {
		return err
	}
	fmt.Fprintf(r.w, "the plan above is the retirement's: %s. Deleting:\n", counts.String())
	fmt.Fprint(r.w, deleteList(plan))
	if err := r.retireConfirm(rets, spec); err != nil {
		return err
	}
	if err := t.Apply(ctx, infra.PlanFile); err != nil {
		return remote(err)
	}
	r.res.Applied = true
	r.retireApplied = true
	return nil
}

// retireConfirm is one of the retirement's two typed confirmations. It is
// askTyped and never ask: ask is covered by --yes, and a retirement that
// --yes could confirm is exactly what the design forbids. reachable=false
// means the run cannot ask at all (--yes, --non-interactive, --json, a pipe,
// a coding agent), and then nothing is removed and nothing is deleted.
func (r *initRun) retireConfirm(rets []infra.Retirement, spec infra.RepoSpec) error {
	what := r.retireWhat(rets)
	confirmed, reachable, err := r.askTyped(what)
	if err != nil {
		return err
	}
	if !reachable {
		if m := agentMarker(os.Getenv); m != "" {
			return &initflow.AgentError{Marker: m}
		}
		return r.notConfirmed("nothing was removed from Terraform's state and nothing was deleted")
	}
	if !confirmed {
		return r.notConfirmed("nothing was removed from Terraform's state and nothing was deleted")
	}
	fmt.Fprintln(r.w, "  confirmed")
	return nil
}

// retireWhat is the banner both confirmations print: the secrets that stay
// behind, by logical name and Secret Manager ID, and the jobs the API
// deletes -- the things the user is consenting to that no plan will show.
func (r *initRun) retireWhat(rets []infra.Retirement) string {
	api, apply, kept := retireGoingAway(rets)
	var b strings.Builder
	fmt.Fprintf(&b, "retire %d %s of %s: remove %d from Terraform's state (destroying nothing), apply %d changes, then delete %d Cloud Run %s through the API.",
		len(rets), plural(len(rets), "address", "addresses"), r.gcpProject,
		len(infra.Removals(r.retireState, rets)), len(apply), len(api), plural(len(api), "job", "jobs"))
	for _, a := range api {
		fmt.Fprintf(&b, " Delete %s (%s, %s).", a.Job, a.Region, a.Address)
	}
	if secrets := keptSecrets(kept); len(secrets) > 0 {
		b.WriteString(" These secrets stay in Secret Manager with their values:")
		for _, s := range secrets {
			if s.SecretID != "" {
				fmt.Fprintf(&b, " %s (%s),", s.Logical, s.SecretID)
			} else {
				fmt.Fprintf(&b, " %s (its Secret Manager ID could not be read),", s.Logical)
			}
		}
		b.WriteString(" unmanaged.")
	}
	return b.String()
}

// retireAllowDelete is the guard's allow-list for a retirement: the plan's
// own deletes of the retirement's addresses, intersected with --allow-delete
// when the user gave one, so naming addresses can only narrow what may be
// destroyed and never widen it (design §3.3, §4.2d).
func retireAllowDelete(plan *tf.Plan, rets []infra.Retirement, flag []string) []string {
	computed := infra.RetireAllowDelete(plan, rets)
	if len(flag) == 0 {
		return computed
	}
	named := make(map[string]bool, len(flag))
	for _, a := range flag {
		named[a] = true
	}
	var out []string
	for _, a := range computed {
		if named[a] {
			out = append(out, a)
		}
	}
	return out
}

// deleteList is the plan's deletes, one address a line: what the second
// confirmation puts in front of the user, the real list rather than the
// prediction the summary printed.
func deleteList(plan *tf.Plan) string {
	var b strings.Builder
	for _, rc := range plan.ResourceChanges {
		if slices.Contains(rc.Change.Actions, "delete") {
			fmt.Fprintf(&b, "  delete %s\n", rc.Address)
		}
	}
	return b.String()
}

// preventDestroyAbort is whether a failed plan is Terraform refusing a
// prevent_destroy resource: the §3.3 signature, matched whitespace-
// normalized because Terraform wraps the message to the terminal width.
func preventDestroyAbort(err error) bool {
	var xe *tf.ExitError
	if !errors.As(err, &xe) {
		return false
	}
	return strings.Contains(collapseSpace(xe.Stderr), "has lifecycle.prevent_destroy set, but the plan calls for this resource to be destroyed")
}

// collapseSpace folds every run of whitespace, newlines included, to one
// space: the message Terraform wraps is matched as one line.
func collapseSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// retireAborted is what a plan aborted on prevent_destroy gets instead of
// Terraform's text: the next command, with the reason (design §3.3).
func retireAborted() error {
	return userErr("the plan was refused: a resource this change retires carries lifecycle.prevent_destroy, so Terraform will not plan without it being removed from the state first.\nRerun with --retire: it removes the protected addresses from Terraform's state (destroying nothing), applies, and deletes the orphaned jobs through the Cloud Run API:\n  %s", retireNextCommand)
}

// retireDelete is the run's last step: the jobs the state removal left
// unmanaged, deleted through the Cloud Run API under the operator's own
// credentials. Each one is deleted only after its labels say it is this
// repository's job, and the delete's operation is polled to a terminal state
// before the run reports anything as done.
func (r *initRun) retireDelete(ctx context.Context, c *infra.Clients, spec infra.RepoSpec) error {
	var firstErr error
	for _, ret := range r.retireSet {
		want, ok := infra.JobDeleteLabels(spec, ret)
		if !ok || ret.Job == "" {
			continue
		}
		region := ret.Region
		if region == "" {
			region = spec.Region
		}
		err := deleteOrphanedJob(ctx, c, spec.GCPProject, region, ret.Job, want)
		if err == nil {
			fmt.Fprintf(r.w, "deleted the orphaned Cloud Run job %s (%s, %s)\n", ret.Job, region, ret.Address)
			continue
		}
		var still *stillDeletingError
		outcome := "apply ok, job delete failed"
		if errors.As(err, &still) {
			// Neither success nor failure: the run exits non-zero with the
			// job and the operation named, and a rerun re-attempts without
			// starting a second delete on a job already going away.
			outcome = "apply ok, job delete still running"
		}
		fmt.Fprintf(r.w, "%s: %v\n", outcome, err)
		fmt.Fprintf(r.w, "apply ok, and the job is out of Terraform's state and fugaro.yaml; %s\n", retireNextCommand)
		r.retireFailed(err)
		if firstErr == nil {
			firstErr = userErr("%s for Cloud Run job %s (%s): %v\nRerun %s: it re-attempts the delete, which is safe (%s)",
				outcome, ret.Job, region, err, retireNextCommand, "the job is out of state and the plan is clean")
		}
	}
	return firstErr
}

// retireFailed records a delete that did not finish, so --json says the same
// thing the run printed.
func (r *initRun) retireFailed(err error) {
	r.res.Warnings = append(r.res.Warnings, "the retirement's job delete did not finish: "+err.Error())
}

// stillDeletingError is a job delete whose operation had not reached a
// terminal state when the wait ran out: not success, not failure, and a
// rerun re-attempts it (design §4.2c).
type stillDeletingError struct {
	Job       string
	Operation string
	Waited    time.Duration
}

func (e *stillDeletingError) Error() string {
	return fmt.Sprintf("Cloud Run job %s is still deleting: its operation %s is not done after %s", e.Job, e.Operation, e.Waited)
}

// deleteOrphanedJob deletes one Cloud Run job fugaro's configuration no
// longer produces, after checking that it is ours (design §4.2c, Review
// Focus 7):
//
//   - the labels first, and every pair in want must match: the job is out of
//     the state and out of fugaro.yaml, but the cloud is still the record of
//     what it is. A job that is not ours is refused, never deleted, and the
//     refusal is an error: the run exits 1 rather than reporting success;
//   - a 404 is success: a job that is already gone (a resumed run, a hand
//     delete) is done;
//   - the delete returns a long-running operation, which is polled to a
//     terminal state: reporting success on its creation would be the silent
//     disagreement the design forbids. A wait that runs out is
//     *stillDeletingError, a third outcome;
//   - a PERMISSION_DENIED is a named outcome too: the installation's launcher
//     role holds no run.jobs.delete, so this call runs as the operator, whose
//     own credentials may not have it. The error carries the gcloud command
//     that finishes the work by hand.
func deleteOrphanedJob(ctx context.Context, c *infra.Clients, project, region, job string, want map[string]string) error {
	name := "projects/" + project + "/locations/" + region + "/jobs/" + job
	live, err := c.Run.Projects.Locations.Jobs.Get(name).Context(ctx).Do()
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading it (run.jobs.get): %w; delete it by hand with: %s", err, gcloudJobDelete(project, region, job))
	}
	if !infra.HasMarks(live.Labels, want) {
		return fmt.Errorf("it does not carry %s; it is not this repository's job to delete. Check it by hand, and delete it only if it is yours: %s",
			missingLabels(live.Labels, want), gcloudJobDelete(project, region, job))
	}
	op, err := c.Run.Projects.Locations.Jobs.Delete(name).Context(ctx).Do()
	if err != nil {
		if apiCode(err) == http.StatusForbidden {
			return fmt.Errorf("permission denied (run.jobs.delete is in no fugaro role: the installation's launcher holds run.jobs.get and run.jobs.list, not the delete), and this call runs as you: %w. Delete it with: %s",
				err, gcloudJobDelete(project, region, job))
		}
		return fmt.Errorf("the delete was refused: %w; retry with: %s", err, gcloudJobDelete(project, region, job))
	}
	waited := time.Duration(0)
	for !op.Done {
		if waited >= time.Duration(retireDeletePolls)*retireDeletePoll {
			return &stillDeletingError{Job: job, Operation: op.Name, Waited: waited}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retireDeletePoll):
		}
		waited += retireDeletePoll
		next, err := c.Run.Projects.Locations.Operations.Get(op.Name).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("reading its operation %s (run.operations.get): %w; check it with: %s",
				op.Name, err, gcloudJobDelete(project, region, job))
		}
		op = next
	}
	if op.Error != nil {
		return fmt.Errorf("its operation %s failed: %s (code %d); retry with: %s",
			op.Name, op.Error.Message, op.Error.Code, gcloudJobDelete(project, region, job))
	}
	return nil
}

// gcloudJobDelete is the command that finishes by hand what the API delete
// could not: printed in the summary's failure paths and in every refusal, so
// the run never ends with work left and no way to see it through.
func gcloudJobDelete(project, region, job string) string {
	return "gcloud run jobs delete " + job + " --project " + project + " --region " + region
}

// missingLabels is the label that kept a job from being deleted, named: the
// user has to be able to see which one it was.
func missingLabels(have, want map[string]string) string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(want)) {
		if have[k] != want[k] {
			out = append(out, k+"="+want[k]+" (found "+strconv.Quote(have[k])+")")
		}
	}
	return strings.Join(out, ", ")
}

// isNotFound is discovery's own 404 rule: the resource is not there, which
// for a delete is the work already done.
func isNotFound(err error) bool { return apiCode(err) == http.StatusNotFound }

// apiCode is the Google API's own HTTP status of err, or 0 when err is not
// an API error at all.
func apiCode(err error) int {
	var ae *googleapi.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return 0
}

// plural picks the word for n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
