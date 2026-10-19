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
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// fugaro allow-delete (design docs/design/managed-projects.md, decisions D72
// and D74, and the entries rule of its §6 item 5) is the owner's answer to
// "may this project ever delete or replace this object?": a list at the root
// of the runs bucket whose entries each hold a Terraform address and the
// identity of the object approved there, and which is the whole of an
// approval in a managed project — the object the managed apply reads its
// allow-list from, rather than its own command line, which belongs to whatever
// session is applying. An approval is of one NAMED object and not of the
// address: the same address carrying another one — another name, a renamed or
// replaced resource, one moved in from elsewhere — is not approved by it
// (tf.DeleteEntry, tf.Approves). What that does not tell apart is a
// re-creation under the same name, which the package comment of
// internal/infra/tf/identity.go and the command's own help both state.
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
//     (tf.GuardEntries, fed by managed.GuardAllowList) refuses a delete or a
//     replacement the owner's entries do not approve.
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
// command cannot overrule the owner. fugaro init still honours --allow-delete
// on its own command line, where the addresses go to tf.Guard directly.

// newAllowDeleteCmd is `fugaro allow-delete [ADDRESS=IDENTITY...]`.
func newAllowDeleteCmd() *cobra.Command {
	var o cloudOptions
	cmd := &cobra.Command{
		Use:   "allow-delete [ADDRESS=IDENTITY...]",
		Short: "Record the objects a run may delete or replace, by address (the project's owner)",
		Long: `Record which Terraform addresses the project's owner approves for deletion or
replacement, and which object at each address is approved, in a managed
project's fugaro/allow-delete.json at the root of the runs bucket. An approval
lives in that object and nowhere else: this command is Fugaro's only writer of
it, with the operator's own credentials.

An argument is an address and the identity of the object approved there,
written as ADDRESS=IDENTITY: the Terraform address a plan reports, as in
google_cloud_run_v2_job.web or module.web.google_storage_bucket.data,
followed by the identity a refused managed apply printed for the object that
address removes, as in

  fugaro allow-delete 'module.web.google_storage_bucket.data=type=google_storage_bucket project=proj-1234 name=data id=data-bucket'

An approval is of one object and not of the address: the same address carrying
another object later — another name, a renamed or replaced resource, one moved
in from elsewhere — is not approved by it, so an entry an earlier apply already
used admits nothing more. The identity is compared character for character with
the one the plan's before view shows, so copy it from the refusal rather than
retyping it; it is printed there bare, and one surrounding pair of double
quotes is stripped from it if you paste it quoted anyway, because a shell or a
message may have added them.

What an approval does not tell apart, said here rather than left to be
discovered: for every managed type but google_apikeys_key the provider derives
a resource's id from its configured name, and a plan's before view carries
nothing else, so an object deleted and then re-created from the same
configuration under the same name at the same address carries the same
identity and this entry still approves its removal. The rule is about the
object at the address, not about one instance of a name; if that matters for a
resource, review the list rather than rely on the entry having been used up.

Nothing else is recorded — no note and no reason: the object's own version
history is the record of who approved what and when, and the identity is what
the approved delete is identified by.

Run it in your own terminal, as an operator, with your own credentials. Pass
the entries to approve, and they replace the list: an invocation that leaves an
entry out removes that approval, so the command names the entries it would drop
and asks for the project's name before writing such a list. With no arguments
the command prints the list — each entry in the same ADDRESS=IDENTITY form, so
a line can be pasted back — and writes nothing; it never empties the list,
because an empty list is what an absent one already means.

What the object is for: a run's own credentials cannot write it, and the
deployer account a managed apply impersonates will not be able to (D75, Task
1.5), so a managed apply can take its allow-list from the owner's object
instead of from the command line of whatever session is applying: fugaro
managed apply reads this object and takes no address flag of its own, so the
session that applies cannot widen the list. (fugaro init still honours its own
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
another project, one written by an older Fugaro (its addresses carry no
identities, so nothing in it says which object was approved), one whose fields
are not exactly the ones this command writes, or one this package cannot read
is no entries — and no entries refuses every delete and replacement rather
than permitting them. The write is fail-closed too: if the current list cannot
be read, nothing is written, and the write is conditioned on the object being
unchanged since that read, so a concurrent write is never silently overwritten
(run the command again).`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return allowDelete(cmd, o, args)
		},
	}
	addCloudFlags(cmd, &o)
	return cmd
}

// allowDelete is newAllowDeleteCmd's body: print the list, or write it.
func allowDelete(cmd *cobra.Command, o cloudOptions, args []string) error {
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
	approved := make([]tf.DeleteEntry, 0, len(args))
	for _, a := range args {
		entry, err := approveArgument(a)
		if err != nil {
			return err
		}
		approved = append(approved, entry)
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
	current := state.Entries
	if len(approved) == 0 {
		if len(current) == 0 {
			why := "there is no list"
			if state.Unreadable {
				why = "the object there is not a list this command reads (one written by an older Fugaro names addresses without the objects approved there, and this version refuses it rather than reading its addresses as approvals of whatever is at them now)"
			}
			fmt.Fprintf(w, "gs://%s/%s allows no entry: every delete and replacement in %s is refused (%s)\n",
				safetext.Field(bucket), managed.AllowDeleteObject, safetext.Field(lc.Name), why)
			return nil
		}
		fmt.Fprintf(w, "gs://%s/%s approves %d entry(ies) in %s, each an address and the object approved there:\n", safetext.Field(bucket), managed.AllowDeleteObject, len(current), safetext.Field(lc.Name))
		printEntries(w, current)
		return nil
	}
	// Sorted and deduplicated, so one approval has one encoding: two people
	// adding the same two entries in either order write the same object. An
	// entry sorts by its address and then by its identity, which is the order
	// the listing above is read in.
	approved = slices.Compact(slices.SortedFunc(slices.Values(approved), compareEntries))
	// The write replaces the list, so an invocation that leaves an entry out
	// removes the approval it carried — and an approval dropped without the
	// owner noticing is a delete refused later for a reason nobody remembers
	// giving. The entries this write drops are named, and a list that drops any
	// of them is written only after the project's name is typed.
	dropped := droppedFrom(current, approved)
	if len(dropped) > 0 {
		fmt.Fprintf(w, "gs://%s/%s approves %d entry(ies) now, and this list leaves out %d of them:\n", safetext.Field(bucket), managed.AllowDeleteObject, len(current), len(dropped))
		printEntries(w, dropped)
	}
	// An object that is there but is not a list this command reads is
	// replaced only on the same typed confirmation: what it held is unknown.
	if state.Unreadable {
		fmt.Fprintf(w, "gs://%s/%s is there but is not a list this command reads (an object written by an older Fugaro, whose addresses carry no identities, is one of these): writing replaces it\n", safetext.Field(bucket), managed.AllowDeleteObject)
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
		what := fmt.Sprintf("this replaces %s's list, removing %d entry(ies) it approved: every delete or replacement of the objects they named is refused again", safetext.Field(lc.Name), len(dropped))
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
	fmt.Fprintf(w, "wrote gs://%s/%s: %s may now delete or replace %d object(s), at the addresses below and no others:\n", safetext.Field(bucket), managed.AllowDeleteObject, safetext.Field(lc.Name), len(approved))
	printEntries(w, approved)
	return nil
}

// approveArgument is one argument of this command as an entry: ADDRESS=IDENTITY,
// cut at the first "=".
//
// The identity is the text a refused managed apply printed for the object a
// removal takes away (tf.RemoveIdentityText), which always begins with the
// resource type — "type=google_storage_bucket project=… name=… id=…" — so the
// half after the first "=" is required to begin with "type=". That is what
// makes the form's one ambiguity fail closed rather than silently: a Terraform
// address may itself contain an "=" (an index key with one in it, as in
// google_x.y["a=b"]), and for such an argument the cut lands inside the
// address, the half after it does not begin with "type=", and the command
// refuses to record an approval that matches nothing. The alternative — a
// second argument order, or a flag per address — was not taken: the ambiguity
// is refused, not guessed, and the operator is told which of the two they are
// looking at.
//
// The whole argument and each half are trimmed, and the halves are compared
// with a plan's by exact equality: a stray space around either would be an
// approval of nothing.
//
// One pair of double quotes around the identity is stripped before that
// comparison, because it is a mark a sentence can have and an identity cannot:
// this command prints the identity bare (tf.RemoveIdentityText), and an owner
// who quotes the whole argument at their shell — or who copies from a message
// in which someone else quoted it — would otherwise record an approval whose
// identity carries two characters the plan never shows, and it would match
// nothing. Only ONE surrounding pair is removed, and the stripped text is then
// required to begin with "type=" like any other: the fail-closed direction is
// unchanged, since a quoted identity that is not an identity is still refused.
func approveArgument(arg string) (tf.DeleteEntry, error) {
	address, before, cut := strings.Cut(strings.TrimSpace(arg), "=")
	address, before = strings.TrimSpace(address), strings.TrimSpace(before)
	before = strings.TrimSuffix(strings.TrimPrefix(before, `"`), `"`)
	switch {
	case !cut || address == "":
		return tf.DeleteEntry{}, userErr("an argument is an address and the identity of the object approved there, written ADDRESS=IDENTITY, as in 'module.web.google_storage_bucket.data=type=google_storage_bucket project=proj-1234 name=data id=data-bucket': the identity is the one a refused apply printed for the object the address removes, and nothing was written")
	case !strings.HasPrefix(before, "type="):
		return tf.DeleteEntry{}, userErr("an identity begins with the resource type the plan showed, as in 'type=google_storage_bucket project=… name=… id=…', and the text after the first '=' in this argument does not: the argument is cut at its first '=', so an address whose own text holds an '=' cannot be approved this way (nothing was written; approve it from a plan whose address has none, or rename the resource)")
	}
	return tf.DeleteEntry{Address: address, Before: before}, nil
}

// printEntries is a listing of the entries an owner's object holds, one per
// line, in the ADDRESS=IDENTITY form this command's arguments take — so a line
// the owner reads is a line they can paste back.
//
// Each line is one safetext.Field: these go to stdout and not through
// ErrorText, and an entry read from the bucket is whatever its writer put there
// (parseDeleteList refuses only blank halves), so an escape sequence, a bidi
// control or a newline in one must not reach the terminal as itself. And the
// listing stops at safetext.FieldListMax entries with the rest counted
// (FieldListNote): the object holds as many entries as it was written with, and
// the sentence above the listing is this command's own — the count is stated
// there, in full, and what is cut is the listing and never the fact that
// something was cut.
func printEntries(w io.Writer, entries []tf.DeleteEntry) {
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		lines = append(lines, e.Address+"="+e.Before)
	}
	shown, omitted := safetext.FieldList(lines)
	for _, l := range shown {
		fmt.Fprintln(w, l)
	}
	if note := safetext.FieldListNote(omitted); note != "" {
		fmt.Fprintf(w, "(%s)\n", note)
	}
}

// compareEntries orders two entries the way this command lists and writes them:
// by address, then by the identity the address is approved for.
func compareEntries(a, b tf.DeleteEntry) int {
	if c := strings.Compare(a.Address, b.Address); c != 0 {
		return c
	}
	return strings.Compare(a.Before, b.Before)
}

// droppedFrom is the entries current approves that approved does not: the
// approvals a write of approved removes.
//
// An entry is dropped when the same address and identity are not both on the
// new list, so re-approving an address for another object — the form the reuse
// rule is answered in, and what an owner does after a resource was replaced —
// is named as the drop it is: the object the earlier entry approved is no
// longer approved by anything.
func droppedFrom(current, approved []tf.DeleteEntry) []tf.DeleteEntry {
	var out []tf.DeleteEntry
	for _, e := range current {
		if !slices.Contains(approved, e) {
			out = append(out, e)
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
		return userErr("nothing was written for %s: gs://%s/%s is at the runs bucket's root, where only an operator's own credential may write (since 0.7.0 a launcher writes runs/ only, and no run account may write the bucket's root at all): run this as an operator, or ask one to approve the entries: %s",
			safetext.Field(lc.Name), safetext.Field(bucket), managed.AllowDeleteObject, safetext.Field(err.Error()))
	default:
		return remote(fmt.Errorf("writing %s in gs://%s for %s: %w", managed.AllowDeleteObject, safetext.Field(bucket), safetext.Field(lc.Name), safetext.FieldErr(err)))
	}
}
