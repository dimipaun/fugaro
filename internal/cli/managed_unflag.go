package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// fugaro managed unflag (D1020, spec §10) is the command the owner revokes
// managed mode with. `fugaro managed` registered two subcommands before it —
// apply and flag — and the undo flag printed was two manual writes in two
// tools, one of them gcloud's own label update that the design of record marks
// UNVERIFIED: an owner who wanted out did a deletion by hand and a write no
// test can run, and a project whose object was gone and whose label was not
// (or the reverse) read as broken to every later command.
//
// What it removes is BOTH marks, because both are what a managed project is
// (read.go's read requires the pair): the creation fact
// gs://<runs bucket>/fugaro/managed.json in the project's own runs bucket, and
// the fugaro_managed=true label on the GCP project itself. Either one alone
// makes the project unmanaged — the read is the conjunction — so a project
// that is already half-removed converges in this one command: which half was
// already gone is said rather than failed on, which is exactly the state the
// manual removal leaves.
//
// What it does NOT remove is the deployer fence (D75), and the command says so
// before it writes. The seven scoped roles and their conditioned bindings stay
// in the project and in the installation, `fugaro update --forget` stays
// refused for a fenced installation, and this command makes no IAM call at
// all. That is the whole reason revocation is a command of ours and not a
// teardown: a teardown would be the IAM-widening door the design closes.
//
// How it refuses is `fugaro managed flag`'s own two checks, called and not
// repeated (reconciliation F4): a coding agent's session is refused before
// anything is read — the mark is what turns the typed confirmation off, so a
// session that could remove the mark could remove it mid-command and keep
// applying ungated — and a Fugaro run's own process is refused beside it,
// because a run credential holds the operator's credentials and could revoke
// the marks its own project is managed by. The consent is the exact GCP project
// ID as the argument itself (D1541, the flag's own rule): nothing stands in for
// naming the project, so this command registers no --yes and no
// --non-interactive, has no bulk form, no wildcard and no prefix, and takes
// exactly one project ID.
//
// What it is not, stated rather than implied: it is not a boundary against a
// session that goes around this program. A coding agent's session holds the
// operator's credentials and can delete this object with gcloud storage rm or
// take the label off with a Resource Manager call (SECURITY.md records the
// wider Owner/Editor bypass). The refusals below stop an agent that uses the
// program.

// newManagedUnflagCmd is `fugaro managed unflag PROJECT_ID`.
func newManagedUnflagCmd() *cobra.Command {
	var o cloudOptions
	cmd := &cobra.Command{
		Use:   "unflag PROJECT_ID",
		Short: "Remove both marks that make a GCP project managed (the project's owner)",
		Long: `Remove the marks that make GCP project PROJECT_ID a managed project.

A managed project carries two marks, written together by the run that created
it (fugaro setup --create-project --managed) or by fugaro managed flag: the
creation fact gs://<runs bucket>/fugaro/managed.json in that project's own runs
bucket, and the label fugaro_managed=true on the GCP project itself. The read
requires both, so the project is managed only while the two are there — and
unflagging removes both, in one command, so an owner never has to do the
deletion and the label write by hand.

A project that is already half-removed — the state a manual removal leaves,
with the object gone and the label there, or the reverse — is converged rather
than refused: which half was already gone is said, and the other is removed.
After this command the project is not managed, and its applies take the typed
confirmation again.

What it does not remove is the deployer fence. The seven scoped roles and their
conditioned bindings stay in the project and in the installation, and fugaro
update --forget stays refused for a fenced installation. This command makes no
IAM call at all; undoing the fence is not revocation and is not offered here
(docs/design/managed-projects.md §10.2).

Run it in your own terminal, as an operator, with your own credentials. The
permission you give is the PROJECT_ID argument itself: it names the one project
whose marks come off, and nothing stands in for naming it — not --yes, not
--non-interactive, not a pipe. A coding agent's session is refused, and a
Fugaro run's own process is refused.

PROJECT_ID must be the exact GCP project ID of the installation selected by
--project, --config or the checkout: one invocation names one project, and
there is no bulk form, no wildcard and no prefix.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return managedUnflag(cmd, o, args[0])
		},
	}
	addCloudFlags(cmd, &o)
	return cmd
}

// managedUnflag is newManagedUnflagCmd's body. The order is the flag's, from
// the other side: who is running, which project it is, that the project's own
// runs bucket is the one the read uses — and only then what the command does
// and does not do, the typed confirmation, and the two writes.
func managedUnflag(cmd *cobra.Command, o cloudOptions, id string) error {
	ctx := cmd.Context()
	// A credential leak first, before anything opens the cloud: GODEBUG is
	// read before main runs and cannot be unset in time (refuseHTTP2Debug).
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	// The refusal the whole shape rests on, before a single read, and the same
	// function `fugaro managed flag` calls: a coding agent's session removes no
	// mark, whatever its arguments. It holds the operator's credentials and
	// could do this itself, so this is a mitigation and not a barrier — but it
	// is the property D1020's answer is built on, and it carries a test.
	if m := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())); m != "" {
		return userErr("nothing was removed: fugaro managed unflag takes the owner's own marks off a GCP project with the operator's credentials, so it is run by a person: %s", initflow.AgentRefusal(m))
	}
	// And a run's own process, whatever its agent's markers, through the same
	// check the flag uses: a run credential may not revoke the marks its own
	// project is managed by, and a run's variables are set by the platform
	// rather than by the agent.
	if m := managedFlagRunMarker(os.Getenv); m != "" {
		return userErr("nothing was removed: fugaro managed unflag is run by a person at their own terminal, and this process is a Fugaro run's (%s is set)", m)
	}
	// One project, named exactly, and by a name a shell cannot expand into
	// several: the argument is a GCP project ID and the wildcard and list forms
	// are not ones. The equality with the selected installation's own
	// gcp_project, below, is the other half.
	if !config.GCPProjectRE.MatchString(id) {
		return userErr("%q is not a GCP project ID: this command unflags one project, by its exact ID, with no wildcard, no prefix and no list", safetext.Field(id))
	}
	_, lc, err := selectProject(ctx, o)
	if err != nil {
		return err
	}
	for _, w := range lc.Warnings() {
		fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: %s\n", safetext.Field(w))
	}
	if id != lc.GCPProject {
		return userErr("nothing was removed: the project this command would unflag is the one the argument names, and %s is not the installation selected here: Fugaro project %s is installed in %s. Name %s, or select its installation with --project or --config",
			safetext.Field(id), safetext.Field(lc.Name), safetext.Field(lc.GCPProject), safetext.Field(lc.GCPProject))
	}
	// The runs bucket's default name and no other, the rule the flag keeps and
	// for the same reason: the managed read looks for the marker at
	// fugaro-runs-<gcp_project> and never at a name from the config, so a
	// project whose config names another bucket is one whose marker this
	// command would not be removing. It is a refusal and not a guess at which
	// bucket the owner meant.
	bucket := "fugaro-runs-" + lc.GCPProject
	if got := lc.RunsBucketName(); got != bucket {
		return userErr("nothing was removed: project %s names the runs bucket %q, and a managed project's marker lives in %s: the managed read locates the project's runs bucket by that name, so a removal aimed at another bucket would leave the marker this project is managed by exactly where it is. Check runs_bucket in the project config",
			safetext.Field(lc.Name), safetext.Field(got), safetext.Field(bucket))
	}
	c, err := newInitClients(ctx, lc)
	if err != nil {
		return err
	}
	// The number is what says a bucket name is this project's (a bucket name
	// is global), and it is what the removal proves the bucket with. It is
	// read before the statement above so a project this credential cannot read
	// is refused before anything is said to have been agreed to.
	number, err := infra.ProjectNumber(ctx, c, lc.GCPProject)
	if err != nil {
		return remote(fmt.Errorf("reading the number of GCP project %s: %s", safetext.Field(lc.GCPProject), safetext.Field(err.Error())))
	}
	w := cmd.OutOrStdout()
	// What is about to be removed, what it gives back, and what it leaves in
	// place — all of it before the first write, because the PROJECT_ID argument
	// is the whole of the consent and the owner is entitled to see what they
	// have just agreed to.
	fmt.Fprintf(w, "unflagging GCP project %s removes both marks that make it managed (project %s):\n", safetext.Field(lc.GCPProject), safetext.Field(lc.Name))
	fmt.Fprintf(w, "  the marker object gs://%s/%s in its runs bucket, and the label %s=%s on the project itself — the read requires both, so the project is managed only while the two are there\n",
		safetext.Field(bucket), managed.MarkerObject, managed.ProjectLabelKey, managed.ProjectLabelValue)
	fmt.Fprintf(w, "  it gives back: the typed confirmation for this project's applies. From the next apply on a person is asked again, and a coding agent's session is refused as it was before the project was flagged (D55).\n")
	fmt.Fprintf(w, "  it does not remove: the deployer fence. Its scoped roles and their conditioned bindings stay in the project and in the installation, fugaro update --forget stays refused for a fenced installation, and this command makes no IAM call at all (D75; docs/design/managed-projects.md §10.2).\n")
	fmt.Fprintf(w, "  a half already gone is not a failure: a project whose object or label is already absent is converged by this one command, and which half it was is said below.\n")
	// The object first, in the flag's order and for the flag's reason: it is
	// the authority, and the label is the narrowing mark beside it. Either half
	// alone leaves the project unmanaged, so an error here removes nothing else
	// and a success here already revokes.
	objectRemoved, err := managed.RemoveMarker(ctx, c.Storage, bucket, lc.GCPProject, number)
	if err != nil {
		return managedUnflagErr(lc, bucket, err)
	}
	labelRemoved, err := managed.RemoveManagedLabelForFlag(ctx, c.CRM, lc.GCPProject)
	if err != nil {
		// The object is gone from here on and the label is not, which is the
		// fail-closed direction — either mark alone leaves the project
		// unmanaged — so this is not a state that keeps a control, and it is
		// said rather than reported as if nothing had happened.
		return remote(fmt.Errorf("removed gs://%s/%s, but not the %s label on GCP project %s (%v): the read requires both, so %s is not managed any more, but the label is still on the project and this command could not take it off. Run it again as the account that owns the project",
			safetext.Field(bucket), managed.MarkerObject, managed.ProjectLabelKey, safetext.Field(lc.GCPProject), safetext.FieldErr(err), safetext.Field(lc.Name)))
	}
	fmt.Fprintf(w, "  marker object gs://%s/%s: %s\n", safetext.Field(bucket), managed.MarkerObject,
		removedOr(objectRemoved, "removed", "was not there"))
	fmt.Fprintf(w, "  label %s on GCP project %s: %s\n", managed.ProjectLabelKey, safetext.Field(lc.GCPProject),
		removedOr(labelRemoved, "removed", "was not the managed mark, left as it is"))
	// Read back through the managed read itself, with its project-number
	// check: the command does not say the project is unmanaged unless the read
	// the apply gate takes says so. It is the same read IsManaged answers with,
	// so what is claimed and what a later command believes are one thing.
	ok, reason, err := managed.IsManaged(ctx, c.Storage, c.CRM, lc.Name, lc.GCPProject)
	if err != nil {
		return remote(fmt.Errorf("reading back GCP project %s after removing both marks: %s", safetext.Field(lc.GCPProject), safetext.Field(err.Error())))
	}
	if ok {
		return remote(fmt.Errorf("both marks were removed from GCP project %s, but it still reads as managed: check it with `gcloud storage cat gs://%s/%s` and `gcloud projects describe %s` as the operator before relying on it",
			safetext.Field(lc.GCPProject), safetext.Field(bucket), managed.MarkerObject, safetext.Field(lc.GCPProject)))
	}
	fmt.Fprintf(w, "read back through the managed read, with the project-number check: %s is not managed\n", safetext.Field(lc.Name))
	if reason != "" {
		fmt.Fprintf(w, "  the read says: %s\n", safetext.Field(reason))
	}
	fmt.Fprintf(w, "unflagged GCP project %s: it is not a managed project any more, and its applies take the typed confirmation again; the deployer fence is untouched\n", safetext.Field(lc.GCPProject))
	return nil
}

// removedOr is the one word each half of the statement gets: what happened to
// it. A half that was already gone is a converged project, not a failure, so
// it is said in the same line rather than in a warning of its own.
func removedOr(removed bool, yes, no string) string {
	if removed {
		return yes
	}
	return no
}

// managedUnflagErr is what the owner can do about a removal that did not
// happen: the bucket is not the project's own, the credential may not write
// the bucket's root, or the cloud's own answer. It is managedFlagErr's shape
// from the other side, and the same sentinel and the same access-denied
// reading are matched.
func managedUnflagErr(lc *localcfg.Config, bucket string, err error) error {
	switch {
	case errors.Is(err, managed.ErrAlreadyMarked):
		return userErr("nothing was removed for %s: gs://%s/%s was written by someone else between this command's read and its write, so what is there is not what this command judged. Nothing was taken off %s; run the command again when the writer has stopped, and read what is there with `gcloud storage cat gs://%s/%s` as the operator",
			safetext.Field(lc.Name), safetext.Field(bucket), managed.MarkerObject, safetext.Field(lc.Name), safetext.Field(bucket), managed.MarkerObject)
	case errors.Is(err, managed.ErrRefused):
		return userErr("nothing was removed for %s: the marker is read from the project's own runs bucket, at gs://%s/%s, and that bucket is not %s's: check gcp_project and runs_bucket in the project config, or run fugaro project check: %s",
			safetext.Field(lc.Name), safetext.Field(bucket), managed.MarkerObject, safetext.Field(lc.GCPProject), safetext.Field(err.Error()))
	case isAccessDenied(err):
		return userErr("nothing was removed for %s: gs://%s/%s is at the runs bucket's root, where only an operator's own credential may write (a launcher writes only runs/ and tokens/, and no run account may write the bucket's root at all): run this as an operator: %s",
			safetext.Field(lc.Name), safetext.Field(bucket), managed.MarkerObject, safetext.Field(err.Error()))
	default:
		return remote(fmt.Errorf("removing %s in gs://%s for %s: %w", managed.MarkerObject, safetext.Field(bucket), safetext.Field(lc.Name), safetext.FieldErr(err)))
	}
}
