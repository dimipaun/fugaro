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
// A state that cannot be read is a warning on a run without --retire, and a
// failure with it. The ordinary run reads the state for one thing only -- to
// explain a retirement it is predicting -- so a state it cannot read narrows
// what it can see instead of stopping it, which is exactly the rule design
// §3.1a gives the label calls: a false negative, never a false positive, and
// never silent. The summary above all may not claim "nothing retired" when
// the run could not look. --retire is the difference that matters: it removes
// addresses from the state, so it must not guess at the set, and a state it
// cannot read stops it. An abandoned run is not a degradation either.
func (r *initRun) readRetirements(ctx context.Context, c *infra.Clients, t *tf.TF, spec infra.RepoSpec) ([]infra.Retirement, error) {
	st, err := t.ShowState(ctx)
	if err != nil {
		if r.o.retire || ctx.Err() != nil {
			return nil, remote(err)
		}
		r.warn(fmt.Sprintf("could not read %s's Terraform state (%v); a retirement cannot be predicted, so an address the state holds and the configuration no longer produces will not be named. Read it by hand with terraform show -json in the repository root's workdir", spec.Name, err))
		st = nil
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
	r.res.Retire = rets
	if err := r.printRetirements(spec, rets); err != nil {
		return nil, err
	}
	return rets, nil
}

// printRetireNotes is the one informational line an already-released kept
// container gets (design §3.1a's convergence rule): it is not a retirement,
// so it must not re-open the gate or re-suggest --retire on every later run,
// but the user is told the container is still there -- by logical name and,
// when the label list gave it, by Secret Manager ID, so a rerun after a
// failed delete still says which container holds the value.
func (r *initRun) printRetireNotes(notes []infra.Retirement) {
	var names []string
	for _, n := range notes {
		if n.SecretID != "" {
			names = append(names, n.Logical+" ("+n.SecretID+")")
			continue
		}
		names = append(names, n.Logical)
	}
	slices.Sort(names)
	fmt.Fprintf(r.w, "note: %s %s no longer mounted by fugaro.yaml and no longer in Terraform's state; %s\n",
		strings.Join(names, ", "), plural(len(names), "is", "are"),
		"the secret and its value stay in Secret Manager, unmanaged")
}

// retireGoingAway splits the set by what actually happens to each address
// (infra.RetireSplit): the jobs the Cloud Run API deletes after the apply,
// everything else, which the plan deletes, and the rows nothing deletes. A
// job the state still holds with deletion_protection = false is in the
// apply group, not the API's: the plan deletes it.
func (r *initRun) retireGoingAway(spec infra.RepoSpec, rets []infra.Retirement) (api, apply, kept []infra.Retirement) {
	return infra.RetireSplit(r.retireState, spec, rets)
}

// retireAPI is that split's api group alone, sorted by address: the deletes
// retireDelete makes, in a fixed order whatever the state's own order was.
func (r *initRun) retireAPI(spec infra.RepoSpec, rets []infra.Retirement) []infra.Retirement {
	api, _, _ := r.retireGoingAway(spec, rets)
	slices.SortFunc(api, func(a, b infra.Retirement) int { return strings.Compare(a.Address, b.Address) })
	return api
}

// printRetirements is the ⚠ RETIRE summary of design §3.2: what goes, why,
// what is kept, the order of operations, and the one command that runs it.
// It is printed before the plan, so it carries none of the plan's counts --
// those are the second confirmation's (retirePlan).
func (r *initRun) printRetirements(spec infra.RepoSpec, rets []infra.Retirement) error {
	api, apply, kept := r.retireGoingAway(spec, rets)
	gone := len(api) + len(apply)
	fmt.Fprintf(r.w, "⚠ This change retires %d %s (project %s, GCP project %s, repository %s).\n",
		len(rets), plural(len(rets), "address", "addresses"), r.projectName, r.gcpProject, spec.Name)
	if len(kept) > 0 {
		fmt.Fprintf(r.w, "  %d go away; fugaro deletes no %s.\n",
			gone, plural(len(kept), "secret", "secret or image"))
	} else {
		fmt.Fprintf(r.w, "  %d go away.\n", gone)
	}
	fmt.Fprintf(r.w, "\nWhy: %s\n", reasons(rets))
	fmt.Fprintf(r.w, "\nGoing away (%d, predicted from the state and the labels) — %d the plan will delete, %d deleted through the Cloud Run API after it:\n",
		gone, len(apply), len(api))
	for _, ret := range slices.Concat(apply, api) {
		fmt.Fprintf(r.w, "  %-72s %s: %s\n", ret.Address, ret.Kind, ret.Cause)
	}
	if removed := r.retireRemovals(spec, rets); len(removed) > 0 {
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
				fmt.Fprintf(r.w, "  %-32s leaves Terraform's state, unmanaged; Secret Manager is untouched\n", k.Logical)
			case k.Logical != "":
				fmt.Fprintf(r.w, "  %-32s leaves Terraform's state, unmanaged; Secret Manager is untouched (its Secret Manager ID could not be read)\n", k.Logical)
			default:
				fmt.Fprintf(r.w, "  %s\n", k.Address)
			}
		}
		fmt.Fprintln(r.w, "  Nothing here deletes a container, so one that still exists keeps its value. A live container carrying this repository's labels is adopted back into the plan instead of being retired at all, so these are the ones the label list did not find: check them with `fugaro secrets ls`.")
	}
	fmt.Fprintf(r.w, "\nOrder of operations:\n  1. remove %d %s from Terraform's state without destroying %s: the addresses no apply can delete (a secret container's prevent_destroy, a job whose deletion_protection the state says is on). Nothing in the cloud is deleted by this step.\n",
		len(r.retireRemovals(spec, rets)), plural(len(r.retireRemovals(spec, rets)), "address", "addresses"), plural(len(r.retireRemovals(spec, rets)), "it", "them"))
	fmt.Fprintln(r.w, "  2. plan, show the plan's own counts and its full delete list, and ask again — the project's name typed at a real terminal, never --yes.")
	fmt.Fprintf(r.w, "  3. apply, then delete the %d orphaned Cloud Run %s through the API.\n", len(api), plural(len(api), "job", "jobs"))
	fmt.Fprintln(r.w, "\nNext command:\n  fugaro init --repo . --retire")
	return nil
}

// retireRemovals is Removals over the state this run read: the protected
// addresses the state still holds, minus the jobs the API deletes, which are
// removed from the state too (that is what makes the plan able to run) and
// are listed under "going away" rather than here.
func (r *initRun) retireRemovals(spec infra.RepoSpec, rets []infra.Retirement) []string {
	api, _, _ := r.retireGoingAway(spec, rets)
	inAPI := map[string]bool{}
	for _, ret := range api {
		inAPI[ret.Address] = true
	}
	var out []string
	for _, a := range infra.Removals(r.retireState, rets) {
		if !inAPI[a] {
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
	// 0. The one refusal that has to come before anything else: a protected,
	//    state-held job the API delete cannot finish. Refusing here is what
	//    keeps the two rules that matter -- nothing is removed from the state
	//    that no later step can delete, and no job is left alive in GCP with
	//    nothing managing it and nothing said.
	if err := retireBlockers(spec, r.retireState, rets); err != nil {
		return err
	}
	if opts.AllowJobDelete {
		// The ordinary plan's own banner (planRepo), which the retirement's
		// path replaces: --allow-job-delete keeps its meaning here.
		fmt.Fprintln(r.w, "⚠ --allow-job-delete: this plan lowers the deletion protection of "+spec.Name+"'s jobs, for offboarding")
	}

	// 1. The prediction, confirmed. Nothing has left the state yet, so this
	//    banner is the only place the kept secrets and the job delete are
	//    named before they happen.
	if err := r.retireConfirm(rets, spec, "nothing was removed from Terraform's state and nothing was deleted"); err != nil {
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
	// From here on the state and the cloud disagree until the run finishes,
	// so every failure below names what is left over rather than stopping
	// quietly (design §6, Review Focus 6).
	leftover := func(err error) error {
		r.retireResidue(spec, rets, removed)
		return err
	}

	// 3. The plan that could not run before, and the guard that always runs.
	changed, err := t.Plan(ctx, infra.PlanFile)
	if err != nil {
		return leftover(remote(err))
	}
	if !changed {
		// A clean plan is not the end of a retirement that still has jobs to
		// delete through the API: the delete is a destructive step no plan
		// shows, so the second confirmation is asked here too (and a run
		// that cannot ask it -- --yes, a pipe, an agent -- is refused, not
		// silently obeyed).
		fmt.Fprintf(r.w, "No changes: %s matches the plan.\n", spec.Name)
		if err := r.retireConfirm(rets, spec, retireUndone("nothing was applied and no job was deleted", removed)); err != nil {
			return leftover(err)
		}
		r.retireApplied = true
		return nil
	}
	plan, err := t.Show(ctx, infra.PlanFile)
	if err != nil {
		return leftover(remote(err))
	}
	counts := infra.CountPlan(plan)
	r.res.Changes = &counts
	// 4. The real plan, in front of the user, and a second typed
	//    confirmation that --yes, --json, a pipe and an agent cannot give.
	fmt.Fprint(r.w, tf.Summary(plan))
	fmt.Fprintln(r.w, im.Bindings.String())
	if err := guard(plan, retireAllowDelete(plan, rets, r.o.allowDelete)); err != nil {
		return leftover(err)
	}
	fmt.Fprintf(r.w, "the plan above is the retirement's: %s. Deleting:\n", counts.String())
	fmt.Fprint(r.w, deleteList(plan))
	if err := r.retireConfirm(rets, spec, retireUndone("the plan was not applied and no job was deleted", removed)); err != nil {
		return leftover(err)
	}
	if err := t.Apply(ctx, infra.PlanFile); err != nil {
		return leftover(remote(err))
	}
	r.res.Applied = true
	r.retireApplied = true
	return nil
}

// retireUndone is what a confirmation that was declined, or could not be
// asked at all, leaves behind -- in the words of what actually happened. The
// addresses that had already left the state by then are named, because the
// user has to know that the state is no longer the whole record of the
// project, and a message that said "nothing was removed" here would be
// false: by the second confirmation the removal has happened (I1).
func retireUndone(action string, removed []string) string {
	if len(removed) == 0 {
		return action + "; nothing had left Terraform's state and nothing was destroyed"
	}
	return fmt.Sprintf("%s; %d %s had already left Terraform's state (nothing was destroyed) and %s out of it: %s. Rerun %s to finish",
		action, len(removed), plural(len(removed), "address", "addresses"),
		plural(len(removed), "stays", "stay"), strings.Join(removed, ", "), retireNextCommand)
}

// retireBlockers refuses a retirement the run cannot finish, before anything
// leaves the state. The case is a Cloud Run job the state holds with its
// deletion_protection still on: no apply can delete it (its resource has
// left the configuration) and the API delete is what finishes it, so the
// state removal is only safe when that call can actually be made. The one
// thing that stops it is a state whose own values carry no `name`: the API
// call has nothing to call with, and guessing a name is forbidden (design
// §3.1). Removing the address anyway would leave the job alive in GCP with
// nothing managing it and nothing said, so the run refuses (exit 1), names
// the address, and removes nothing (I3).
func retireBlockers(spec infra.RepoSpec, stateAddrs []string, rets []infra.Retirement) error {
	inState := make(map[string]bool, len(stateAddrs))
	for _, a := range stateAddrs {
		inState[a] = true
	}
	var unnameable []infra.Retirement
	for _, ret := range rets {
		if ret.Kept != "" || !ret.Protected || !inState[ret.Address] || ret.Job != "" {
			continue
		}
		// Only a job the API delete finishes: JobDeleteLabels refuses every
		// other kind, including a job of a variant that is still deployed.
		if _, ok := infra.JobDeleteLabels(spec, ret); !ok {
			continue
		}
		unnameable = append(unnameable, ret)
	}
	if len(unnameable) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("nothing was removed from Terraform's state and nothing was applied: this retirement cannot be finished.")
	for _, ret := range unnameable {
		region := ret.Region
		if region == "" {
			region = spec.Region
		}
		fmt.Fprintf(&b, "\n  %s (%s): the state's own values carry no name for it, and fugaro never guesses one. Find it by its labels, then delete it by hand: %s",
			ret.Address, region, gcloudJobList(spec.GCPProject, region, infra.OrphanLabels(spec)))
	}
	b.WriteString("\nThen rerun " + retireNextCommand + ": with the job gone, the state removal, the plan and the apply can run.")
	return userErr("%s", b.String())
}

// retireResidue is the account a retirement owes when it stops after the
// state removal: those addresses are alive in GCP with nothing managing
// them, and the API delete is the step that has not happened. Naming them is
// the whole of design §6's rule -- the state and the cloud may not be left
// disagreeing silently -- and it is printed before the error the run returns,
// so the user reads what is left over, not only what went wrong.
func (r *initRun) retireResidue(spec infra.RepoSpec, rets []infra.Retirement, removed []string) {
	if len(removed) == 0 {
		return
	}
	fmt.Fprintf(r.w, "the run stopped after the state removal, so %s alive in GCP with nothing managing %s (nothing was destroyed):\n",
		plural(len(removed), "this address is", "these addresses are"), plural(len(removed), "it", "them"))
	for _, a := range removed {
		fmt.Fprintf(r.w, "  %s\n", a)
	}
	for _, ret := range rets {
		if ret.Job == "" || !slices.Contains(removed, ret.Address) {
			continue
		}
		region := ret.Region
		if region == "" {
			region = spec.Region
		}
		fmt.Fprintf(r.w, "  the Cloud Run job %s (%s) still has to be deleted through the API: %s\n",
			ret.Job, region, gcloudJobDelete(spec.GCPProject, region, ret.Job))
	}
	fmt.Fprintf(r.w, "Rerun %s: %s plans and applies what is left, then deletes the orphaned jobs. It is safe to rerun (%s).\n",
		retireNextCommand, retireNextCommand, "the addresses already out of the state are not removed twice")
}

// retireConfirm is one of the retirement's two typed confirmations. It is
// askTyped and never ask: ask is covered by --yes, and a retirement that
// --yes could confirm is exactly what the design forbids. reachable=false
// means the run cannot ask at all (--yes, --non-interactive, --json, a pipe,
// a coding agent), and then nothing is removed and nothing is deleted.
func (r *initRun) retireConfirm(rets []infra.Retirement, spec infra.RepoSpec, undone string) error {
	what := r.retireWhat(spec, rets)
	confirmed, reachable, err := r.askTyped(what)
	if err != nil {
		return err
	}
	if !reachable {
		if m := agentMarker(os.Getenv); m != "" {
			return &initflow.AgentError{Marker: m}
		}
		return r.notConfirmed(undone)
	}
	if !confirmed {
		return r.notConfirmed(undone)
	}
	fmt.Fprintln(r.w, "  confirmed")
	return nil
}

// retireWhat is the banner both confirmations print: the secrets that stay
// behind, by logical name and Secret Manager ID, and the jobs the API
// deletes -- the things the user is consenting to that no plan will show.
func (r *initRun) retireWhat(spec infra.RepoSpec, rets []infra.Retirement) string {
	_, _, kept := r.retireGoingAway(spec, rets)
	api := r.retireAPI(spec, rets)
	var b strings.Builder
	// No counts of the plan's own: before the state removal no plan exists
	// at all, and the plan's real counts are the second confirmation's to
	// show. What is named here is what fugaro knows now: the addresses, the
	// state removal and the API deletes.
	fmt.Fprintf(&b, "retire %d %s of %s: remove %d from Terraform's state (destroying nothing), then apply, then delete %d Cloud Run %s through the API.",
		len(rets), plural(len(rets), "address", "addresses"), r.gcpProject,
		len(infra.Removals(r.retireState, rets)), len(api), plural(len(api), "job", "jobs"))
	for _, a := range api {
		fmt.Fprintf(&b, " Delete %s (%s, %s).", a.Job, a.Region, a.Address)
	}
	if secrets := keptSecrets(kept); len(secrets) > 0 {
		b.WriteString(" Fugaro deletes no secret, so these leave Terraform's state and stay in Secret Manager if they are still there:")
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
	return preventDestroyAddress(err) != "" || preventDestroyText(err)
}

// preventDestroyText is whether the failed plan carries Terraform's
// prevent_destroy refusal at all, address or not.
func preventDestroyText(err error) bool {
	var xe *tf.ExitError
	if !errors.As(err, &xe) {
		return false
	}
	if xe.Command != "plan" {
		// Only the plan's own failure is re-framed: the same words out of
		// another command are that command's failure, not a plan abort.
		return false
	}
	return strings.Contains(collapseSpace(xe.Stderr), preventDestroyMarker)
}

// preventDestroyMarker is the clause Terraform's refusal ends with, matched
// whitespace-normalized because Terraform wraps the message to the terminal
// width.
const preventDestroyMarker = "has lifecycle.prevent_destroy set, but the plan calls for this resource to be destroyed"

// preventDestroyAddress is the resource address Terraform names in front of
// that clause: the plan's own words, pulled out so the refusal can name the
// resource instead of asking the user to read Terraform's text.
func preventDestroyAddress(err error) string {
	var xe *tf.ExitError
	if !errors.As(err, &xe) || xe.Command != "plan" {
		return ""
	}
	msg := collapseSpace(xe.Stderr)
	i := strings.Index(msg, preventDestroyMarker)
	if i < 0 {
		return ""
	}
	words := strings.Fields(msg[:i])
	for j := len(words) - 1; j >= 0; j-- {
		if words[j] == "Resource" {
			return strings.Join(words[j+1:], " ")
		}
	}
	return ""
}

// collapseSpace folds every run of whitespace, newlines included, to one
// space: the message Terraform wraps is matched as one line.
func collapseSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// retireAborted is what a plan aborted on prevent_destroy gets instead of
// Terraform's text: the reason, the address, and -- only when it is the
// address's own answer -- the next command (design §3.3).
//
// --retire is named only when the aborting address is one of the retirements
// this run read: for any other address the state removal would not touch it,
// the same plan would abort in the same place, and pointing at --retire would
// be advice that cannot work. The configuration is what holds that
// prevent_destroy, so the two ways out are named instead: take the block out
// of the module, or put back the configuration change that made Terraform
// want to destroy the resource (M5).
func retireAborted(rets []infra.Retirement, addr string) error {
	where := ""
	if addr != "" {
		where = "\n  " + addr
	}
	if slices.ContainsFunc(rets, func(r infra.Retirement) bool { return r.Address == addr }) && addr != "" {
		return userErr("the plan was refused: %s carries lifecycle.prevent_destroy, so Terraform will not plan its destruction, and this change retires it.%s\nRerun with --retire: it removes the protected addresses from Terraform's state (destroys nothing), applies, and deletes the orphaned jobs through the Cloud Run API:\n  %s", addr, where, retireNextCommand)
	}
	return userErr("the plan was refused: %s carries lifecycle.prevent_destroy, so Terraform will not plan its destruction.%s\nfugaro read no retirement for this address, so --retire would remove nothing here and the same plan would abort in the same place. The block is in the configuration, and only you can say which side is wrong:\n  - Terraform is holding a resource that should go: take the lifecycle.prevent_destroy block out of the module for that address (or restore the configuration that used to produce it), then rerun.\n  - the resource should stay: revert the configuration change that asked for its destruction, then rerun.", nonEmpty(addr, "a resource"), where)
}

// nonEmpty is s, or fallback for the empty string, in a message that has to
// read as a sentence whether or not the address was found.
func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// retireDelete is the run's last step: the jobs the state removal left
// unmanaged, deleted through the Cloud Run API under the operator's own
// credentials. Each one is deleted only after its labels say it is this
// repository's job, and the delete's operation is polled to a terminal state
// before the run reports anything as done.
func (r *initRun) retireDelete(ctx context.Context, c *infra.Clients, spec infra.RepoSpec) error {
	var firstErr error
	// Only the rows the API delete is what finishes: a job the state still
	// holds and the plan deleted is not one of them, and calling the API on
	// it would be a second delete of a resource the apply already took.
	api := r.retireAPI(spec, r.retireSet)
	for _, ret := range api {
		want, ok := infra.JobDeleteLabels(spec, ret)
		if !ok {
			continue
		}
		region := ret.Region
		if region == "" {
			region = spec.Region
		}
		if ret.Job == "" {
			// A job address the state holds whose own `name` could not be
			// read: the state removal took the address out and Protected
			// kept it there for that reason, so no apply can delete it
			// either, and the job is alive in GCP with nothing managing it.
			// The API delete needs a name and guessing one is forbidden
			// (design §3.1), so this is a leftover the run names and the
			// user finishes by hand -- never a silent success (§4.2c).
			err := fmt.Errorf("its name could not be read from Terraform's state, so the API delete has no name to call: %s", gcloudJobList(spec.GCPProject, region, want))
			fmt.Fprintf(r.w, "apply ok, job delete impossible: the Cloud Run job behind %s (%s) is alive in GCP with nothing managing it: %v\n", ret.Address, region, err)
			r.retireFailed(err)
			if firstErr == nil {
				firstErr = userErr("the Cloud Run job behind %s (%s) was removed from Terraform's state and is alive in GCP with nothing managing it: %v",
					ret.Address, region, err)
			}
			continue
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

// gcloudJobList is the command that finds a job whose name the state could
// not give: its own labels are what identify it, and they are the same set
// the API delete would have checked.
func gcloudJobList(project, region string, want map[string]string) string {
	var filter []string
	for _, k := range slices.Sorted(maps.Keys(want)) {
		filter = append(filter, "labels."+k+"="+want[k])
	}
	return "gcloud run jobs list --project " + project + " --region " + region +
		" --filter='" + strings.Join(filter, " AND ") + "'"
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
