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
// session is applying. An approval is of one OBJECT and not of the address: the
// same address carrying another one — another name, a renamed or replaced
// resource, one moved in from elsewhere — is not approved by it, and neither is
// a re-creation under the same name of a type whose state carries the value the
// provider mints per instance (tf.DeleteEntry, tf.Approves, and the package
// comment of internal/infra/tf/identity.go for the seven types that do not,
// which the command's own help also states).
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

  fugaro allow-delete 'module.web.google_storage_bucket.data=type=google_storage_bucket project=proj-1234 name=data id=data-bucket instance=2026-01-02T03:04:05.678Z'

An approval is of one object and not of the address: the same address carrying
another object later — another name, a renamed or replaced resource, one moved
in from elsewhere — is not approved by it, so an entry an earlier apply already
used admits nothing more. The identity is compared character for character with
the one the plan's before view shows, so copy it from the refusal rather than
retyping it. A refusal prints it inside a sentence of its own, so copy the
identity up to and not including the ')' that closes that sentence's
parenthetical — pasting that bracket as well is forgiven, once, since the
sentence put it there; one matched pair of double quotes is stripped the same
way, because a shell or a message may have added them (a quote at one end only
is a character of the identity, and is kept). What is not forgiven is any other
parenthesis: an identity holds none — a value's '(' and ')' are escaped with
everything else this program's printers would touch — so an argument that still
holds one is refused rather than recorded as an entry that would match no
removal. The two ends agree byte for byte because the identity is written in an
escaped form — a space in a value as \x20, a backslash as \\, a parenthesis as
\x28 and \x29, any other byte with no printable form as \xHH, and every byte a
shell reads in a way of its own — a separator, a pipe, a redirection, a
substitution, a quote, a glob, an expansion — as \xHH as well, so that what a
refusal prints holds no byte that can begin, end or continue a command, and is
exactly what a plan shows. Two bytes a shell rewrites are in it all the same,
because the form needs them: the single space between two parts, and the
backslash that introduces an escape. A paste that does not keep those two whole
arrives here as several arguments and is refused rather than recorded — nothing
is written, and no entry is made that would match no removal — but the identity
has to reach this command as one argument, byte for byte as the plan shows it,
for the loop to close. An identity longer than 8192
characters is refused here and by the plan: no refusal prints one, and the plan
refuses the removal rather than matching a printed part of it, so recording it
would be an approval that deletes nothing.

What the identity tells apart, and what it does not, said here rather than left
to be discovered. For the seven managed types whose state carries a value the
provider mints once per instance — a bucket's time_created, a secret's or an
artifact repository's create_time, a Cloud Run job's or an API key's uid, a
service account's unique_id, a bucket object's generation — the identity
carries that value, so an object of one of those types deleted and then created
again under the same name at the same address carries a DIFFERENT identity and
this entry no longer approves its removal. It does not for the seven types that
have no such value: a Cloud Scheduler job, a project service, and the five
*_iam_member types, whose whole state is the grant they carry. For those the
identity is the named object, and a re-creation under the same name at the same
address is approved by the entry that named it. The provider's own id is not
one of the instance values: for every managed type it is derived from the
configured name (an API key's id is projects/P/locations/global/keys/NAME).
If the difference matters for a resource of the second kind, review the list
rather than rely on the entry having been used up.

Nothing else is recorded — no note and no reason: the object's own version
history is the record of who approved what and when, and the identity is what
the approved delete is identified by.

Run it in your own terminal, as an operator, with your own credentials. Pass
the entries to approve, and they replace the list: an invocation that leaves an
entry out removes that approval, so the command names the entries it would drop
and asks for the project's name before writing such a list. With no arguments
the command prints the list — each entry in the same ADDRESS=IDENTITY form —
and writes nothing; it never empties the list, because an empty list is what an
absent one already means. A printed line is one you can paste back as far as its
IDENTITY half goes: the command that recorded the entry escaped it, so the
printers leave it alone and a line inside the listing's own bound carries it
back byte for byte. The address half is the plan's own — not escaped here and
not bounded here — so an address holding a tab, a run of spaces or a byte
outside printable ASCII is printed as the printers rewrite it: that line is not
the entry, and pasting it back records an entry about an address no plan has,
which the plan refuses rather than deleting anything. A line longer than twice
the identity's own bound is printed through the bounded sanitiser instead, cut
and marked as cut: what you read there is visibly a text and not an entry. The
entry itself is unchanged in the object either way.

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
identities, so nothing in it says which object was approved), one whose
identities are an earlier build's (an entry that names the object without the
per-instance value its type has matches no removal the gate identifies, so it
approves nothing — re-approve it from a current refusal), one whose fields
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
// makes the form's one ambiguity fail closed in the case an owner meets: a
// Terraform address may itself contain an "=" (an index key with one in it, as
// in google_x.y["a=b"]), and for such an argument the cut lands inside the
// address, the half after it does not begin with "type=", and the command
// refuses to record an approval that matches nothing. The cut leaves a second
// trace, and it is the one that catches what the first does not: an address a
// plan writes closes every bracket it opens, so an address still inside its
// index key where the identity begins is one the cut landed in, and it is
// refused for that too, whether the half after the cut reads as an identity or
// not.
//
// What is left after both, stated rather than implied because no check here can
// close it: an address whose own text, after its FIRST "=", spells out what an
// identity looks like and closes its bracket before the separator — an index
// key holding "=type=google_storage_bucket project=p name=n id=i", say — is an
// argument this command cannot tell from ADDRESS=IDENTITY. The half after the
// cut begins with "type=", the address it leaves is a closed one, and what is
// recorded is an entry about the TRUNCATED address: no plan has it, so it
// approves nothing and the owner's loop does not close — and the argument holds
// nothing that says which of the two it is, only the plan could. That residual
// is pinned as it stands by
// TestAllowDeleteCommandRefusesAnAddressCutInsideItsIndexKey's second half, and
// the doc and the test change together if it ever starts being refused. The
// alternative — a second argument order, or a flag per address — was not taken:
// the ambiguity is refused where the argument shows it, and the operator is
// told which of the two they are looking at.
//
// The whole argument and each half are trimmed, and the halves are compared
// with a plan's by exact equality: a stray space around either would be an
// approval of nothing. Each strip of a paste's marks is followed by a trim of
// its own for the same reason (pasteMarks.trim): what a strip exposes is
// whitespace the strip was sitting behind.
//
// Trimming an identity cannot lose a character of one: an identity is escaped
// so that the only whitespace in it is the single space between two of its
// parts, with none at either end (tf.identityValue, pinned by
// TestAnIdentityIsOnePrintableLineWithSingleSpaces), so what the trim removes
// here is a shell's or a paste's own space and never the identity's.
//
// Two marks a paste can carry are stripped before that comparison, because both
// are marks a sentence has and a half cannot (pasteMarks): one MATCHED pair of
// double quotes — around the whole argument, when a shell of the owner's own or
// a message someone quoted it in added them, or around the identity alone,
// which this command prints bare (tf.RemoveIdentityText) — and the closing
// bracket of the refusal sentence's own parenthetical, which every refusal that
// prints an identity ends with ("the plan removes type=…"). The pair is looked
// for around the whole argument as well as around the identity, and that is the
// part of it that matters: a pair around the whole argument has one end on each
// half, so taking it off the half after the "=" alone — which is what this did
// — left a quote on the address and recorded an entry about an address no plan
// has, written silently. Only one of each, and each only as a mark: a quote at
// one end alone is a character a half can hold, an entry is compared byte for
// byte, and stripping the ends independently, as this once did, recorded an
// approval of an object the plan does not remove. No one can tell which end of
// a lost pair is the mark and which the character, so a quote left at either
// end of the ADDRESS is refused rather than recorded: an address holds no quote
// at its ends (a plan writes one inside an index key, where the bracket closes
// it), while the identity half keeps the quote it has, because a value in an
// identity may hold one. An identity holds no parenthesis at all
// (tf.identityValue escapes both), so a bracket the trim does not account for
// is not an identity's and the argument is refused rather than recorded. The
// stripped text is then required to begin with "type=" like any other, and to
// have the shape an identity has (identityShape), so the fail-closed direction
// is unchanged: a quoted or bracketed tail that is not an identity is still
// refused.
func approveArgument(arg string) (tf.DeleteEntry, error) {
	marks := pasteMarks{pair: true, bracket: true}
	address, before, cut := strings.Cut(marks.trim(arg), "=")
	address, before = strings.TrimSpace(address), marks.trim(before)
	switch shape := identityShape(before); {
	case !cut || address == "":
		return tf.DeleteEntry{}, userErr("an argument is an address and the identity of the object approved there, written ADDRESS=IDENTITY, as in 'module.web.google_storage_bucket.data=type=google_storage_bucket project=proj-1234 name=data id=data-bucket instance=2026-01-02T03:04:05.678Z': the identity is the one a refused apply printed for the object the address removes, and nothing was written")
	case strings.HasPrefix(address, `"`) || strings.HasSuffix(address, `"`):
		// The other half of the pair pasteMarks.trim strips, when it is not a
		// pair: one quote is all that is left of it, and no address has one at
		// its ends, so what is recorded with it is an entry about an address no
		// plan shows — the fail-closed direction, taken loudly rather than
		// written silently.
		return tf.DeleteEntry{}, userErr("an address holds no quote at either end: a plan writes an address as the path of the resource itself, and a quote is in one only inside an index key (module.web.google_storage_bucket.data[\"key\"]), where the bracket closes it. What is on this argument's address is one end of a pair someone quoted the whole argument with, and this command strips such a pair only when both of its ends are there: copy the argument again with the address bare, or quote the identity alone — nothing was written")
	case strings.LastIndex(address, "[") > strings.LastIndex(address, "]"):
		// The same ambiguity, seen from the address instead of the tail — and
		// the half the "type=" check below does not catch, because an index key
		// can spell "=type=…" itself, which is why this case is asked first. An
		// address a plan writes closes every bracket it opens, so an address
		// whose last bracket is an opening one is one the cut landed inside:
		// half an address, and recording it would write an entry about an
		// address no plan has — the owner reads it as an approval and it clears
		// nothing, silently.
		return tf.DeleteEntry{}, userErr("an address is closed before the '=' that separates it from the identity, and this one is still inside an index key where the identity begins: an address may hold an '=' of its own inside such a key (module.web.google_storage_bucket.data[\"a=b\"]), and this command cuts the argument at its first '=', so the '=' it cut at is inside the address and not the one between the two halves. Copy the argument again with the address whole, or approve the removal from a plan whose address holds no '=' (nothing was written)")
	case !strings.HasPrefix(before, "type="):
		return tf.DeleteEntry{}, userErr("an identity begins with the resource type the plan showed, as in 'type=google_storage_bucket project=… name=… id=…', and the text after the first '=' in this argument does not: the argument is cut at its first '=', so an address whose own text holds an '=' cannot be approved this way (nothing was written; approve it from a plan whose address has none, or rename the resource)")
	case strings.ContainsAny(before, "()"):
		// A refusal prints the identity inside a sentence of its own, "…(the
		// plan removes type=… id=…)", so the copy an owner makes can end with
		// the sentence's own bracket — the one pasteMarks.trim takes off. An
		// identity holds no parenthesis of its own (tf.identityValue escapes
		// both), so one that is left is a text this command cannot read as an
		// identity, and recording it would write an entry the owner reads as an
		// approval and that clears nothing: the loop the delete list works by
		// — refuse, copy, approve — would not close, and would not say so.
		return tf.DeleteEntry{}, userErr("an identity holds no parenthesis: every value in one is escaped (a '(' and a ')' among them, as \\x28 and \\x29), because a refusal prints the identity inside a sentence of its own and the copy ends with that sentence's closing bracket — which is taken off this argument, once. This one still holds a parenthesis after that, so it is not an identity as a refusal printed one: copy the text again, up to and not including that bracket (nothing was written)")
	case len(before) > tf.RemovalIdentityPrintMax:
		// The one thing this command can tell about an identity without the
		// plan: a longer one is refused by the gate whatever the plan shows
		// (tf.RemovalIdentityPrintMax, which is also why no refusal prints
		// one), so recording it would write an approval that clears nothing
		// and read as an approval all the same.
		return tf.DeleteEntry{}, userErr("an identity of %d characters is longer than the %d a plan can print and match (tf.RemovalIdentityPrintMax): no removal is identified by one, so approving it would record an entry that deletes nothing, and nothing was written", len(before), tf.RemovalIdentityPrintMax)
	case shape != "":
		// The other thing this command can tell without the plan: an identity
		// is escaped text (tf.identityValue), so its characters are printable
		// ASCII and its parts are separated by single spaces. A paste that was
		// wrapped, re-indented or typed by hand can carry a character outside
		// that set, and an entry holding one equals no identity a plan shows.
		return tf.DeleteEntry{}, userErr("an identity is printable ASCII with a single space between two of its parts and no other whitespace at all (tf.identityValue writes every byte outside that as an escape, a space inside a value as \\x20), and the text after the first '=' in this argument is not that: %s. A paste that has been wrapped, re-indented or typed by hand can carry such a character, and an entry holding one equals no identity a plan shows — the owner would read it as an approval and it would clear nothing, so the loop the delete list works by would not close. Nothing was written: copy the identity again from the refusal, which prints it as that one line (tf.RemoveIdentityText)", shape)
	}
	return tf.DeleteEntry{Address: address, Before: before}, nil
}

// identityShape is why s is not the shape an identity has, or "" when it is.
//
// An identity is escaped text (tf.identityValue): every byte below a printable
// one is written as an escape — a space inside a value as \x20, a newline, a
// tab or an escape byte as its own — and so is every byte above them, so what
// an identity is made of is printable ASCII, and the single space between two
// of its parts is the only whitespace in it, with none at either end of the
// text (the ends of an argument are trimmed before this is asked). A character
// outside that set is one no printer of this program writes for an identity,
// so an identity holding one was built by a paste, a wrapping or a hand, and
// recording it would write an entry that no plan's identity equals.
func identityShape(s string) string {
	if strings.Contains(s, "  ") {
		return "it holds two spaces in a row, and an identity's parts are separated by one single space (a space inside a value is written \\x20)"
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c != ' ' && (c < 0x21 || c > 0x7e) {
			return fmt.Sprintf("it holds \\x%02x at offset %d", c, i)
		}
	}
	return ""
}

// pasteMarks are the marks a paste of a refusal's text can carry that an
// argument does not: the closing bracket of the sentence's own parenthetical,
// which every refusal that prints an identity ends with, and one matched pair
// of double quotes around a value this command printed bare
// (trimOneQuotedPair) — around the whole argument, when a shell of the owner's
// own or a message someone quoted it in added them, or around the identity
// alone, which a refusal prints bare (tf.RemoveIdentityText).
//
// At most one of each is taken off an argument, however many times trim is
// asked: the whole argument and then the identity half. That is what makes a
// second bracket a text this command refuses rather than a mark it strips
// twice — "… id=data-bucket))" would otherwise come out as an identity, and so
// would a pair around the whole argument AND another around the identity
// inside it. No identity holds a parenthesis (tf.identityValue escapes both),
// and an argument that needs two pair strips is an argument this command has
// not read, so the answer to both is the same as to a paste that is not an
// identity at all: refuse it and say so, rather than record an entry that
// clears nothing.
//
// trim is applied to the whole argument as well as to the half after the "=",
// which the pair is the reason for: around the whole argument the pair has one
// end on each half, so a strip that saw only the half after the "=" left a
// quote on the address, and the entry recorded was about an address no plan
// has — written silently (approveArgument). Each strip is followed by a trim of
// its own, because one mark can expose whitespace the next one sits behind: a
// message a terminal wrapped puts a newline between the identity and the
// sentence's bracket, and a trim that this once lacked recorded an identity
// with a space on the end of it.
type pasteMarks struct{ pair, bracket bool }

// trim is s without the marks this value has left to take off, and with no
// whitespace at either end of what it returns.
func (m *pasteMarks) trim(s string) string {
	for {
		t := strings.TrimSpace(s)
		if m.bracket && strings.HasSuffix(t, ")") {
			m.bracket, s = false, t[:len(t)-1]
			continue
		}
		if m.pair {
			if q := trimOneQuotedPair(t); q != t {
				m.pair, s = false, q
				continue
			}
		}
		return t
	}
}

// trimOneQuotedPair is s without one pair of double quotes around it, and s
// itself when the two ends are not both a quote. The pair is what a shell or a
// message can add around a value this command printed bare; a quote at one end
// only is part of the value, and removing it would record an identity the plan
// never showed — an approval of nothing.
func trimOneQuotedPair(s string) string {
	if len(s) >= 2 && strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) {
		return s[1 : len(s)-1]
	}
	return s
}

// listingLineMax is the longest entry line this listing prints whole: twice the
// identity's own bound (tf.RemovalIdentityPrintMax), which is room for an
// identity inside that bound and an address of the same order beside it. Every
// entry this command WRITES has an identity inside the bound, because
// approveArgument refuses a longer one.
//
// The address half it writes is unbounded, deliberately. The address is the
// plan's own — this command cannot know what a plan will carry — and in a
// managed project the owner's list is the only route to approve a delete at one
// (GuardAllowList refuses a command-line address there), so refusing a long
// address would lock that removal away with nothing left to act on. Nothing
// else bounds it either: the identity half is bounded here (approveArgument)
// and by the rule, the object is bounded as a whole (managed's 64 KiB read
// bound) rather than per entry, and parseDeleteList refuses an entry only for a
// blank half. The identity's bound is no precedent for one: a longer identity
// is refused by the gate whatever the plan shows, so bounding it here only
// repeats a rule that already holds, while an address has no such rule to fall
// back on. A line past this bound is therefore one this command may have
// written, or one another writer left in the object, and it is printed through
// safetext.Field, which cuts it at FieldMax and ends it with its own marker:
// what the owner reads there is visibly a cut text and not the entry.
const listingLineMax = 2 * tf.RemovalIdentityPrintMax

// printEntries is a listing of the entries an owner's object holds, one per
// line, in the ADDRESS=IDENTITY form this command's arguments take — so a line
// the owner reads is a line they can paste back, while it is one of the lines
// this listing prints whole. The half that promise holds for is the IDENTITY:
// the command that recorded the entry escaped it (tf.RemovalIdentity.String
// escapes every byte its printers would touch), so the rewrites FieldUncut
// still makes are no-ops on it, and a line this listing prints whole carries
// its identity back byte for byte. Past that bound (listingLineMax) the
// identity does not come back at all: the line is Field's there, cut at
// FieldMax and marked as cut (the paragraph below, on where the line becomes
// Field's), so what the owner reads is a text and not the identity they
// approved.
//
// The address half is not escaped — the address is the plan's own, and
// listingLineMax says why it is not bounded either — so an address holding a
// character the printers rewrite (a tab, a run of spaces, a byte outside
// printable ASCII) is printed as they rewrite it, and a line whose address was
// rewritten is not the entry it was written from. Pasting that line back
// records an entry about an address the plan does not have, which is an
// approval of nothing, refused (loudly and fail-closed) wherever the address is
// matched byte for byte. Stating the cost is the honest end of it, because
// paying it off here is not open to this listing: an address escaped the way an
// identity is could not be read back into an entry at all (this command takes
// its address half as written and never unescapes one), and no printed form of
// such an address is one an owner can paste anyway — a refusal prints the
// address through safetext.Field too. An address narrow enough to need one of
// those characters is one an owner writes into the argument by hand.
//
// Each line is printed as the entry itself (safetext.FieldUncut at or below
// the listing's own line bound, safetext.Field past it): Field is what keeps an
// entry read from the bucket — whatever its writer put there, since
// parseDeleteList refuses only blank halves — from reaching the terminal as an
// escape sequence, a bidi control or a newline of its own, but it also cuts a
// value at its own bound and rewrites the characters it does not expect, and a
// line that is not the entry is a line that approves nothing when it is pasted
// back — which is the difference the halves above are about.
//
// Past the bound the line is Field's: cut at FieldMax, ended with Field's own
// marker. An entry can be that long — listingLineMax says why the address half
// is unbounded — and what this listing offers its reader at that length is a
// visibly cut text rather than an entry, which is the honest answer where a
// whole line cannot be printed; the entry itself is unchanged in the object,
// where the command that wrote it put it.
//
// And the listing stops at safetext.FieldListMax entries with the rest counted
// (FieldListNote): the object holds as many entries as it was written with,
// and the sentence above the listing is this command's own — the count is
// stated there, in full, and what is cut is the listing and never the fact
// that something was cut.
func printEntries(w io.Writer, entries []tf.DeleteEntry) {
	shown := 0
	for _, e := range entries {
		if shown == safetext.FieldListMax {
			break
		}
		shown++
		line := e.Address + "=" + e.Before
		if len(line) > listingLineMax {
			line = safetext.Field(line)
		} else {
			line = safetext.FieldUncut(line)
		}
		fmt.Fprintln(w, line)
	}
	if note := safetext.FieldListNote(len(entries) - shown); note != "" {
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
		return userErr("nothing was written for %s: gs://%s/%s is at the runs bucket's root, where only an operator's own credential may write (a launcher writes only runs/ and tokens/, and no run account may write the bucket's root at all): run this as an operator, or ask one to approve the entries: %s",
			safetext.Field(lc.Name), safetext.Field(bucket), managed.AllowDeleteObject, safetext.Field(err.Error()))
	default:
		return remote(fmt.Errorf("writing %s in gs://%s for %s: %w", managed.AllowDeleteObject, safetext.Field(bucket), safetext.Field(lc.Name), safetext.FieldErr(err)))
	}
}
