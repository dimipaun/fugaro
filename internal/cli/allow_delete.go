package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// fugaro allow-delete (design docs/design/managed-projects.md, decisions D72
// and D74) is the owner's answer to "may this project ever delete or replace
// this resource?": a list of Terraform addresses, at the root of the runs
// bucket, that is the whole of an approval in a managed project — the object
// the managed apply reads its allow-list from, rather than its own command
// line, which belongs to whatever session is applying.
//
// It is run by the owner, with the owner's own credentials, and it is
// Fugaro's only writer of the object. What that buys, and what it does not — the plan's step
// 4, stated rather than implied:
//
//   - Against a run's own credentials it is a boundary. No credential a run
//     holds has a write path to the bucket's root (the job, build and launcher
//     grants are all conditioned to a prefix below it), and by D75's answer
//     the same holds for the deployer service account the managed apply
//     impersonates (Task 1.5). An apply through Fugaro's own path therefore
//     cannot approve its own delete, and the gate that reads this object
//     (tf.Guard, fed by managed.GuardAllowList) refuses a delete or a
//     replacement whose address the owner did not name.
//   - Against a coding agent's session it is a speed bump, not a boundary.
//     That session runs under the OPERATOR's credentials: it can write this
//     object directly — gcloud storage cp — and it can unset the marker in its
//     own environment and run this command itself, since the marker check
//     below reads an environment the same process controls. SECURITY.md
//     already records the wider fact this sits inside: a project Owner or
//     Editor, or anyone with a project-level storage role, bypasses the
//     bucket's own policy regardless. The refusal below stops an agent that
//     goes through the program; it does not stop one that goes around it, and
//     nothing here claims otherwise.
//
// And the apply that reads it is built: `fugaro managed apply` takes the
// addresses from this object through managed.GuardAllowList and registers no
// flag of its own for them (internal/cli/managed_apply.go), so an applying
// command cannot overrule the owner. the converge still honours --allow-delete
// on its own command line, where the addresses go to tf.Guard directly.

// newAllowDeleteCmd is `fugaro allow-delete [ADDRESS...]`.
func newAllowDeleteCmd() *cobra.Command {
	var o cloudOptions
	cmd := &cobra.Command{
		Use:   "allow-delete [ADDRESS...]",
		Short: "Record the Terraform addresses a run may delete or replace (the project's owner)",
		Long: `Record which Terraform addresses the project's owner approves for deletion or
replacement, in a managed project's fugaro/allow-delete.json at the root of
the runs bucket. An approval lives in that object and nowhere else: this
command is Fugaro's only writer of it, with the operator's own credentials.

An address is the Terraform address a plan reports, as in
google_cloud_run_v2_job.web or module.web.google_storage_bucket.data, and
nothing else is recorded — no note and no reason: the object's own version
history is the record of who approved what and when.

Run it in your own terminal, as an operator, with your own credentials. Pass
the addresses to approve, and they replace the list: an invocation that leaves
an address out removes that approval, so the command names the entries it
would drop and asks for the project's name before writing such a list. With no
addresses the command prints the list and writes nothing; it never empties the
list, because an empty list is what an absent one already means.

What the object is for: a run's own credentials cannot write it, and the
deployer account a managed apply impersonates will not be able to (D75, Task
1.5), so a managed apply can take its allow-list from the owner's object
instead of from the command line of whatever session is applying: fugaro
managed apply reads this object and takes no address flag of its own, so the
session that applies cannot widen the list. (the converge still honours its own
--allow-delete on its own command line, where the addresses go to tf.Guard
directly.) What this does not do: a coding agent's session holds the
operator's credentials, so it can write this object directly (gcloud storage
cp) or unset its agent marker and run this command itself. The refusal below is a speed bump against
an agent that uses the program, not a boundary against one that goes around it
(SECURITY.md records the wider Owner/Editor bypass).

On a project that is not managed, this writes an object nothing reads: the
check below is that the bucket is the project's own runs bucket, which every
installation has, not that the project is Fugaro-managed.

The list is read fail-closed: an absent object, an empty one, one naming
another project, one whose fields are not exactly the three this command
writes, or one this package cannot read is no addresses — and no addresses
refuses every delete and replacement rather than permitting them. The write
is fail-closed too: if the current list cannot be read, nothing is written,
and the write is conditioned on the object being unchanged since that read,
so a concurrent write is never silently overwritten (run the command again).`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return allowDelete(cmd, o, args)
		},
	}
	addCloudFlags(cmd, &o)
	return cmd
}

// allowDelete is newAllowDeleteCmd's body: print the list, or write it.
func allowDelete(cmd *cobra.Command, o cloudOptions, addresses []string) error {
	ctx := cmd.Context()
	// Before anything is read: the refusal is about who is running, and an
	// agent's session gets no answer at all, not even the list.
	if m := agentMarker(os.Getenv); m != "" {
		return userErr("nothing was written: fugaro allow-delete records the project's own approval of cloud deletes with the operator's credentials, so it is run by a person: %s", initflow.AgentRefusal(m))
	}
	// And before the cloud is opened at all: GODEBUG=http2debug makes Go
	// print HTTP/2 requests with their headers — the OAuth bearer token —
	// to stderr, and GODEBUG is read before main runs, so it cannot be
	// unset in time. Every command that opens the cloud refuses instead
	// (refuseHTTP2Debug), and this one opens the owner's own credentials.
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	_, lc, err := selectProject(ctx, o)
	if err != nil {
		return err
	}
	bucket := lc.RunsBucketName()
	if bucket == "" {
		return userErr("project %s names no runs bucket (set runs_bucket, or a gs:// bucket_url): the delete list lives at gs://<runs bucket>/%s", safetext.Field(lc.Name), managed.AllowDeleteObject)
	}
	// The default name and no other, which is the rule the managed objects
	// already live by: init's owner() refuses any other (init_review.go:183)
	// and markCreated writes the marker only into it, because the managed read
	// locates the project's runs bucket as fugaro-runs-<gcp_project>. A list
	// written into a custom bucket would be read by nobody, so it is refused
	// here rather than recorded where nothing looks for it.
	if want := "fugaro-runs-" + lc.GCPProject; bucket != want {
		return userErr("the delete list needs the default runs bucket name %s (this installation's is %q): the managed read locates the project's runs bucket by that name, so a list written into another bucket would be read by nobody", safetext.Field(want), safetext.Field(bucket))
	}
	approved := make([]string, 0, len(addresses))
	for _, a := range addresses {
		// Trimmed, and tested for emptiness the way the writer tests it: an
		// address is compared with a plan's by exact equality, so a stray
		// space would be an approval of nothing, and a whitespace-only
		// argument is an empty address rather than anything about the bucket.
		a = strings.TrimSpace(a)
		if a == "" {
			return userErr("an empty address: every argument names a Terraform address to approve, as in google_cloud_run_v2_job.web")
		}
		approved = append(approved, a)
	}
	c, err := newInitClients(ctx, lc)
	if err != nil {
		return err
	}
	// The number the read and the write both prove the bucket with, read from
	// the project itself: a bucket name is global, so the number is what says
	// the bucket is this project's.
	number, err := infra.ProjectNumber(ctx, c, lc.GCPProject)
	if err != nil {
		// The API's own text can carry a body: one bounded field, as the
		// apply and the flag print the same read's failure. The chain stays
		// (safetext.FieldErr), so the exit code is still the remote one.
		return remote(safetext.FieldErr(err))
	}
	w := cmd.OutOrStdout()
	// What is approved now, read once and before the write: it is what the
	// print mode prints, what tells the write whether it is dropping an
	// approval the owner gave, and the generation the write is conditioned on.
	// The read is strict: one that fails writes nothing, because a failure
	// read as "nothing approved" would let this write drop every approval
	// without asking.
	state, err := managed.ReadDeleteListForWrite(ctx, c.Storage, bucket, lc.GCPProject, number)
	if err != nil {
		if errors.Is(err, managed.ErrRefused) {
			return allowDeleteWriteErr(lc, bucket, err)
		}
		return remote(fmt.Errorf("nothing was written for %s: the current gs://%s/%s could not be read, and a list written without that read could drop approvals unasked: %w",
			safetext.Field(lc.Name), safetext.Field(bucket), managed.AllowDeleteObject, safetext.FieldErr(err)))
	}
	current := state.Addresses
	if len(approved) == 0 {
		if len(current) == 0 {
			why := "there is no list"
			if state.Unreadable {
				why = "the object there is not a list this command reads"
			}
			fmt.Fprintf(w, "gs://%s/%s allows no address: every delete and replacement in %s is refused (%s)\n",
				safetext.Field(bucket), managed.AllowDeleteObject, safetext.Field(lc.Name), why)
			return nil
		}
		fmt.Fprintf(w, "gs://%s/%s allows %d address(es) in %s:\n", safetext.Field(bucket), managed.AllowDeleteObject, len(current), safetext.Field(lc.Name))
		printAddresses(w, current)
		return nil
	}
	// Sorted and deduplicated, so one approval has one encoding: two people
	// adding the same two addresses in either order write the same object.
	approved = slices.Compact(slices.Sorted(slices.Values(approved)))
	// The write replaces the list, so an invocation that leaves an address out
	// removes the approval it carried — and an approval dropped without the
	// owner noticing is a delete refused later for a reason nobody remembers
	// giving. The entries this write drops are named, and a list that drops any
	// of them is written only after the project's name is typed.
	dropped := droppedFrom(current, approved)
	if len(dropped) > 0 {
		fmt.Fprintf(w, "gs://%s/%s approves %d address(es) now, and this list leaves out %d of them:\n", safetext.Field(bucket), managed.AllowDeleteObject, len(current), len(dropped))
		printAddresses(w, dropped)
	}
	// An object that is there but is not a list this command reads is
	// replaced only on the same typed confirmation: what it held is unknown.
	if state.Unreadable {
		fmt.Fprintf(w, "gs://%s/%s is there but is not a list this command reads: writing replaces it\n", safetext.Field(bucket), managed.AllowDeleteObject)
	}
	if len(dropped) > 0 || state.Unreadable {
		// Strict rather than confirmTyped: this command has no --yes (it
		// registers the cloud flags and nothing else), so a refusal offering
		// one would tell the owner to pass a flag that does not exist. The
		// typed name is the whole of the confirmation here, which is also the
		// right way round: an approval is removed by a person reading what
		// goes, not by a flag typed in advance.
		//
		// The question says what is lost: the approvals named above, or, for
		// an unreadable object, whatever it held — dropped is empty then
		// because nothing could be read, not because nothing is removed.
		what := fmt.Sprintf("this replaces %s's list, removing %d address(es) it approved: every delete or replacement of them is refused again", safetext.Field(lc.Name), len(dropped))
		if state.Unreadable {
			what = fmt.Sprintf("this replaces gs://%s/%s, an object that is not a list this command reads: nothing it held could be read, so any approval it carried for %s is lost and every delete or replacement it allowed is refused again", safetext.Field(bucket), managed.AllowDeleteObject, safetext.Field(lc.Name))
		}
		if err := confirmTypedStrict(cmd, lc,
			"removing an approval is confirmed by typing the project's name at a terminal, and by nothing else", what); err != nil {
			return err
		}
	}
	if err := managed.WriteDeleteList(ctx, c.Storage, bucket, lc.GCPProject, number, approved, state.Generation); err != nil {
		return allowDeleteWriteErr(lc, bucket, err)
	}
	fmt.Fprintf(w, "wrote gs://%s/%s: %s may now delete or replace %d address(es):\n", safetext.Field(bucket), managed.AllowDeleteObject, safetext.Field(lc.Name), len(approved))
	printAddresses(w, approved)
	return nil
}

// printAddresses is a listing of the addresses an owner's object holds, one
// per line, bounded twice.
//
// Each entry is one safetext.Field: these lines go to stdout and not through
// ErrorText, and an address read from the bucket is whatever its writer put
// there (parseDeleteList refuses only blank entries), so an escape sequence, a
// bidi control or a newline in one must not reach the terminal as itself. And
// the listing stops at safetext.FieldListMax entries with the rest counted
// (FieldListNote): the object holds as many addresses as it was written with,
// and the sentence above the listing is this command's own — the count is
// stated there, in full, and what is cut is the listing and never the fact
// that something was cut.
func printAddresses(w io.Writer, addresses []string) {
	shown, omitted := safetext.FieldList(addresses)
	for _, a := range shown {
		fmt.Fprintln(w, a)
	}
	if note := safetext.FieldListNote(omitted); note != "" {
		fmt.Fprintf(w, "(%s)\n", note)
	}
}

// droppedFrom is the addresses current approves that approved does not: the
// approvals a write of approved removes.
func droppedFrom(current, approved []string) []string {
	var out []string
	for _, a := range current {
		if !slices.Contains(approved, a) {
			out = append(out, a)
		}
	}
	return out
}

// allowDeleteWriteErr is what the owner can do about a write that did not
// happen: the wrong project named (this package's own refusal), credentials
// the bucket's root refuses, or the cloud's own answer.
func allowDeleteWriteErr(lc *localcfg.Config, bucket string, err error) error {
	switch {
	// The one refusal of this package's own that is about the list rather
	// than about the bucket: it is over the size the read believes, so the
	// fix is fewer addresses, and this case comes first so the owner is not
	// sent to check gcp_project and runs_bucket, which are fine.
	case errors.Is(err, managed.ErrTooLarge):
		return userErr("nothing was written for %s: the list is over the size this command reads back, so it would be recorded and then read as no addresses — refusing every delete it looks like it approves. Approve fewer addresses (or shorter ones), in gs://%s/%s: %s",
			safetext.Field(lc.Name), safetext.Field(bucket), managed.AllowDeleteObject, safetext.Field(err.Error()))
	case errors.Is(err, managed.ErrRefused):
		return userErr("nothing was written for %s: the delete list goes into the project's own runs bucket, at gs://%s/%s, and that bucket is not %s's: check gcp_project and runs_bucket in the project config, or run fugaro project check: %s",
			safetext.Field(lc.Name), safetext.Field(bucket), managed.AllowDeleteObject, safetext.Field(lc.GCPProject), safetext.Field(err.Error()))
	case errors.Is(err, managed.ErrChanged):
		return userErr("nothing was written for %s: gs://%s/%s changed after this command read it, so another write happened in between: run the command again to see the list as it is now: %s",
			safetext.Field(lc.Name), safetext.Field(bucket), managed.AllowDeleteObject, safetext.Field(err.Error()))
	case isAccessDenied(err):
		return userErr("nothing was written for %s: gs://%s/%s is at the runs bucket's root, where only an operator's own credential may write (a launcher writes only runs/ and tokens/, and no run account may write the bucket's root at all): run this as an operator, or ask one to approve the addresses: %s",
			safetext.Field(lc.Name), safetext.Field(bucket), managed.AllowDeleteObject, safetext.Field(err.Error()))
	default:
		return remote(fmt.Errorf("writing %s in gs://%s for %s: %w", managed.AllowDeleteObject, safetext.Field(bucket), safetext.Field(lc.Name), safetext.FieldErr(err)))
	}
}
