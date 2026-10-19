package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// newBackendsCmd is `fugaro backends`: which compute backend an installation
// runs its jobs on (design docs/design/compute-backend.md §4.4, plan
// 2026-10-19-compute-backend.md Task 8 step 3, P7: the design asks for the
// command "when a second backend exists", and a second backend is what Task 8
// adds).
//
// It is a read of the project config and of the seam's own names, and it calls
// no cloud API: the question "which backend does this project use?" is answered
// by localcfg, offline, which is the whole reason the design wanted a command
// rather than a doctor line.
func newBackendsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "backends",
		Aliases: []string{"backend"},
		Short:   "List the compute backends an installation can run its jobs on",
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return userErr("unknown backends subcommand; want ls (see fugaro backends --help)")
		},
	}
	cmd.AddCommand(newBackendsLsCmd())
	return cmd
}

// selectableBackends are the backends a project config may name, in the order
// `fugaro backends ls` prints them: exactly the two names the seam defines
// (internal/backend/backend.go). They are written as the seam's constants
// rather than as strings, and the opener map that registry opens from is held
// against this list by TestBackendsLsListsTheOpenerMapsNames, so a name the
// seam can open and this command does not print -- or the reverse -- fails
// there.
//
// backend.Local is deliberately not here. It is the invocation's own platform
// (backend.LocalPlatform's name), not a backend a project selects: internal/
// localcfg refuses it as a `backend:` value ("not a known backend"), and
// openCloudFor builds no opener for it.
var selectableBackends = []string{backend.CloudRun, backend.CloudBatch}

type backendsOptions struct {
	cloud  cloudOptions
	asJSON bool
}

func (o *backendsOptions) addFlags(cmd *cobra.Command) {
	addCloudFlags(cmd, &o.cloud)
	cmd.Flags().BoolVar(&o.asJSON, "json", false, "print machine-readable output")
}

// backendsListDoc is fugaro backends ls --json's shape: the names a project
// config may select, in the order the command prints them, and which one this
// project selected. Backend is "" and Complete is false when no project config
// could be selected, the same two fields accountsListDoc carries for the same
// reason (review finding, task 9b round 2): a --json caller must be able to
// tell "this project names no backend, so it runs on the default" from
// "selection failed and nothing here is authoritative".
type backendsListDoc struct {
	Backends []backendsEntry `json:"backends"`
	// Project and Backend are the selected project and the name its config
	// selects (localcfg.Config.Backend, which answers backend.CloudRun for a
	// config that names none), both "" when nothing was selected.
	Project string `json:"project,omitempty"`
	Backend string `json:"backend,omitempty"`
	// Complete is false when selection failed for a reason other than an
	// explicit --config/--project/$FUGARO_CONFIG/$FUGARO_PROJECT.
	Complete bool   `json:"complete"`
	Note     string `json:"note,omitempty"` // the selection failure, when Complete is false
}

type backendsEntry struct {
	Name string `json:"name"`
	// InUse is true for the name the project's own config selects. At most one
	// entry has it, and with no project selected none does.
	InUse bool `json:"in_use"`
}

func newBackendsLsCmd() *cobra.Command {
	var o backendsOptions
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the compute backends this fugaro knows and which one the project uses",
		Long: `List the compute backends a project config's backend: key may name, and
which one the selected project uses:

  cloud-run    Cloud Run jobs (the default, and the one every installation
               runs on today)
  cloud-batch  Cloud Batch jobs, one VM per run (the VM backend)

A config that names no backend runs on cloud-run. A config that names
cloud-batch is refused by this build ("not enabled yet"): the backend and its
opener are built and tested, and nothing selects them until Fugaro has passed
the live checklist for it.

This is a local, read-only lookup: it reads the project config and the backend
names internal/backend defines, and it calls no cloud API. With --json it
prints one object instead of two lines, carrying the list, the selected project
and the name its config selects.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return backendsLs(cmd, o)
		},
	}
	o.addFlags(cmd)
	return cmd
}

// backendsConfig is the project config selected for fugaro backends ls, nil
// when none is: the same offline selection internal/cli/accounts.go's
// accountsConfig makes, for the same reason -- it must never fall back to the
// shared config published to the runs bucket (selectProject's sharedOK path),
// because that would be a network call for a command billed as offline, and a
// backend is not a fact the shared config carries anyway.
//
// note is "" when lc is authoritative, and names the selection failure
// otherwise, for the caller to carry into backendsListDoc, so a --json caller
// can tell "this project selects the default backend" from "couldn't tell".
func backendsConfig(cmd *cobra.Command, o cloudOptions) (lc *localcfg.Config, note string, err error) {
	ctx := cmd.Context()
	co, err := checkoutProject(ctx, "")
	if err != nil {
		return nil, "", err
	}
	sel, lc, err := selectFrom(o, co, false)
	if err != nil {
		explicit := o.config != "" || o.project != "" || os.Getenv("FUGARO_PROJECT") != "" || os.Getenv("FUGARO_CONFIG") != ""
		if explicit {
			return nil, "", err
		}
		note = fmt.Sprintf("no project config could be selected, so which backend this project uses is unknown (the list below is every backend a project may name) (%v)", err)
		fmt.Fprintf(cmd.ErrOrStderr(), "note: %s\n", note)
		return nil, note, nil
	}
	return lc, "", announce(o, sel, lc)
}

func backendsLs(cmd *cobra.Command, o backendsOptions) error {
	lc, note, err := backendsConfig(cmd, o.cloud)
	if err != nil {
		return err
	}
	project, inUse := "", ""
	if lc != nil {
		project, inUse = lc.Name, lc.Backend()
	}
	return printBackends(cmd.OutOrStdout(), project, inUse, note, o.asJSON)
}

// printBackends writes the list. The project's own backend is marked rather
// than moved, so the two names always print in the same order and a reader
// (or a --json caller) comparing two projects sees the same table twice.
func printBackends(w io.Writer, project, inUse, note string, asJSON bool) error {
	if asJSON {
		doc := backendsListDoc{Project: project, Backend: inUse, Complete: note == "", Note: note}
		for _, n := range selectableBackends {
			doc.Backends = append(doc.Backends, backendsEntry{Name: n, InUse: n == inUse})
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	}
	for _, n := range selectableBackends {
		line := pluginwire.Printable(n)
		if n == inUse {
			line += "\t" + pluginwire.Printable(project) + "'s backend"
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}
