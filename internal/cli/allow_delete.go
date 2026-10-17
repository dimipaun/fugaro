package cli

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// fugaro allow-delete (design docs/design/managed-projects.md, decisions D72
// and D74) is the owner's answer to "may this project ever delete or replace
// this resource?": a list of Terraform addresses, at the root of the runs
// bucket, that the commands which apply a plan read before they apply it —
// instead of taking the answer from their own command line, which belongs to
// whatever session is applying.
//
// It is run by the owner, with the owner's own credentials, and it is the
// only writer of the object. No credential a run holds has a write path to
// the bucket's root (the job, build and launcher grants are all conditioned
// to a prefix below it), so a coding agent's session cannot record its own
// approval — and this command refuses outright when one is present, because a
// session that holds the operator's credentials could otherwise just ask.

// newAllowDeleteCmd is `fugaro allow-delete [ADDRESS...]`.
func newAllowDeleteCmd() *cobra.Command {
	var o cloudOptions
	cmd := &cobra.Command{
		Use:   "allow-delete [ADDRESS...]",
		Short: "Approve the Terraform addresses a run may delete or replace (the project's owner)",
		Long: `Record which Terraform addresses the project's runs may delete or replace,
in a managed project's fugaro/allow-delete.json at the root of the runs
bucket. A command that applies a plan refuses a delete or a replacement whose
address is not on that list, and takes the list from the object alone: an
--allow-delete passed to the applying command itself is refused, not honoured.

Run it in your own terminal, as an operator, with your own credentials: the
object sits where only an operator may write, and a coding agent's session is
refused (it holds those credentials and could otherwise approve its own
deletes). Pass the addresses to approve, and they replace the list — a shorter
list refuses more, so dropping an address is how a delete is refused again.
With no addresses the command prints the list and writes nothing; it never
empties the list, because an empty list is what an absent one already means.

An address is the Terraform address a plan reports, as in
google_cloud_run_v2_job.web or module.web.google_storage_bucket.data, and
nothing else is recorded — no note and no reason: the object is written by
this command alone, and the object's own version history is the record of who
approved what and when.`,
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
	_, lc, err := selectProject(ctx, o)
	if err != nil {
		return err
	}
	bucket := lc.RunsBucketName()
	if bucket == "" {
		return userErr("project %s names no runs bucket (set runs_bucket, or a gs:// bucket_url): the delete list lives at gs://<runs bucket>/%s", lc.Name, managed.AllowDeleteObject)
	}
	for _, a := range addresses {
		if a == "" {
			return userErr("an empty address: every argument names a Terraform address to approve, as in google_cloud_run_v2_job.web")
		}
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
		return remote(err)
	}
	w := cmd.OutOrStdout()
	if len(addresses) == 0 {
		list, err := managed.AllowDelete(ctx, c.Storage, bucket, lc.GCPProject, number)
		if err != nil {
			return remote(err)
		}
		if len(list) == 0 {
			fmt.Fprintf(w, "gs://%s/%s allows no address: every delete and replacement in %s is refused (no list, an empty one, or one this run cannot read)\n",
				bucket, managed.AllowDeleteObject, lc.Name)
			return nil
		}
		fmt.Fprintf(w, "gs://%s/%s allows %d address(es) in %s:\n", bucket, managed.AllowDeleteObject, len(list), lc.Name)
		for _, a := range list {
			fmt.Fprintln(w, a)
		}
		return nil
	}
	// Sorted and deduplicated, so one approval has one encoding: two people
	// adding the same two addresses in either order write the same object.
	approved := slices.Compact(slices.Sorted(slices.Values(addresses)))
	if err := managed.WriteDeleteList(ctx, c.Storage, bucket, lc.GCPProject, number, approved); err != nil {
		return allowDeleteWriteErr(lc, bucket, err)
	}
	fmt.Fprintf(w, "wrote gs://%s/%s: %s may now delete or replace %d address(es):\n", bucket, managed.AllowDeleteObject, lc.Name, len(approved))
	for _, a := range approved {
		fmt.Fprintln(w, a)
	}
	return nil
}

// allowDeleteWriteErr is what the owner can do about a write that did not
// happen: the wrong project named (this package's own refusal), credentials
// the bucket's root refuses, or the cloud's own answer.
func allowDeleteWriteErr(lc *localcfg.Config, bucket string, err error) error {
	switch {
	case errors.Is(err, managed.ErrRefused):
		return userErr("nothing was written for %s: the delete list goes into the project's own runs bucket, at gs://%s/%s, and that bucket is not %s's: check gcp_project and runs_bucket in the project config, or run fugaro project check: %v",
			lc.Name, bucket, managed.AllowDeleteObject, lc.GCPProject, err)
	case isAccessDenied(err):
		return userErr("nothing was written for %s: gs://%s/%s is at the runs bucket's root, where only an operator's own credential may write (since 0.7.0 a launcher writes runs/ only, and no run account may write the bucket's root at all): run this as an operator, or ask one to approve the addresses: %v",
			lc.Name, bucket, managed.AllowDeleteObject, err)
	default:
		return remote(fmt.Errorf("writing %s in gs://%s for %s: %w", managed.AllowDeleteObject, bucket, lc.Name, err))
	}
}
