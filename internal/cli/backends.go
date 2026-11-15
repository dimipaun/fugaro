package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	crm "google.golang.org/api/cloudresourcemanager/v1"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/yamledit"
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
			return userErr("unknown backends subcommand; want ls or use (see fugaro backends --help)")
		},
	}
	cmd.AddCommand(newBackendsLsCmd(), newBackendsUseCmd())
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
cloud-batch is ACCEPTED: fugaro doctor reports the Batch checks for an
installation that selects it -- the Compute and Batch APIs, the preloaded boot
image and the socket gid it records beside it, the network and subnetwork a
run's VMs are placed in, the log isolation, the run accounts and their grants,
the price table and the reaper -- and FAILS until every one of them passes.
What doctor does is report: fugaro run reads none of those checks, so it does
not hold a launch back, and an installation that selects the name before it is
provisioned runs anyway (its output stays in _Default and its VMs are left to
the job's own duration). Run fugaro doctor first, rather than reading a green
one as a fence around the launcher.

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

// newBackendsUseCmd is `fugaro backends use <name>`: the command that selects
// the backend, and the one line `fugaro setup --provision-batch` ends with
// points here. The stage writes the batch block and applies the compute root
// and does NOT select the backend -- every launch of the installation would
// move onto VMs, and the reaper deletes there -- so selecting it is this
// command, at the owner's own hands.
//
// The write is one key of the local project config, through the same door
// `project routing` writes through (yamledit, then projectConfigEditLocal:
// every comment and every other byte of the file is kept, the write is atomic
// and the file is backed up first), and it asks nothing (the no-prompt policy:
// no y/N, no --yes).
//
// What fences it, and what does not:
//
//   - selecting cloud-batch is refused until the installation has passed the
//     Batch half of `fugaro doctor` -- the same checks, read here, and the
//     block's own absence with them (backendsUseGate). Without that refusal
//     this command would be a way to put every launch of an installation onto
//     VMs that no compute root provisioned, which is the state the gate exists
//     to close (design compute-backend.md §4.4, §8.1 task 9).
//   - selecting it is a WIDENING of this installation (a run's job gets a VM,
//     and on that VM a Docker daemon, which the design's §1 says is root on its
//     host), so it is the coding-agent refusal's class (settingsEdit.Typed):
//     no coding agent's session writes it, and the owner's own terminal reads
//     what it widens and writes it. Selecting cloud-run is a narrowing and is
//     the ordinary write.
//   - neither the gate nor the widening holds a LAUNCH back: `fugaro run` reads
//     no doctor check, so an installation whose selection and provisioning have
//     drifted apart elsewhere still launches. This command cannot drift: the
//     selection it writes is one the checks passed for.
func newBackendsUseCmd() *cobra.Command {
	var o backendsUseOptions
	cmd := &cobra.Command{
		Use:   "use <cloud-run|cloud-batch>",
		Short: "Select the compute backend this project runs its jobs on",
		Long: `Select the backend this project's runs are launched on, by writing the
backend: key of the selected project's own config:

  cloud-run    Cloud Run jobs (the default, and the one every installation
               runs on today)
  cloud-batch  Cloud Batch jobs, one VM per run (the VM backend)

The installation must already be provisioned for cloud-batch: its config has to
carry a batch block -- the block fugaro setup --provision-batch writes, which
names the machine type, the boot image, the network, the subnetwork and the
account a run's VMs are built from -- and fugaro doctor's Batch checks have to
pass. Selecting the name before either is refused, because a launch on an
installation that is not provisioned there fails at create: nothing was
written, and the refusal names what doctor reports and what fixes it.

Selecting cloud-run is never refused: it is the default, and it is the
direction back.

The switch is one key of one file: every comment and every other byte of the
project config is kept, the old file is backed up, and the write is atomic.
Nothing is asked and nothing confirmed -- the checks above are what stands in
front of the change -- and a coding agent's session does not write cloud-batch
at all (run it yourself; it is the same fence the provisioning step carries).

The change reaches the next run at once, and every launch of the installation
follows it: on cloud-batch a run's job is a VM with a Docker daemon, so a
workflow that needs Docker can run, and that VM and its job are the reaper's to
sweep.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBackendsUse(cmd, args[0], o)
		},
	}
	// The settings family's two aiming flags (settingsAimFlags): the write lands
	// in the config of the project they name rather than in whichever one the
	// machine's environment would have picked, which is what a live check's
	// `--project fugaro` needs and what a machine with several installations
	// needs. They are the family's, not --gcp-project/--region: nothing here
	// reads a region or a GCP project from the command line, and a flag that
	// looked like it did would be one an owner would use to aim a write it does
	// not aim.
	settingsAimFlags(cmd, &o.project, &o.config)
	return cmd
}

func runBackendsUse(cmd *cobra.Command, name string, o backendsUseOptions) error {
	if !slices.Contains(selectableBackends, name) {
		return userErr("backend %s: this installation may run on %s, and nothing else (fugaro backends ls lists them)", pluginwire.Printable(name), strings.Join(selectableBackends, " or "))
	}
	sel, lc, err := settingsAimedLocalFile(settingsWriteOptions{project: o.project, config: o.config}, "fugaro backends use")
	if err != nil {
		return err
	}
	// A selection the config already makes is not written again: the effective
	// name is what a launch reads (a config that names none runs on cloud-run),
	// so the answer here is that there is nothing to select -- and no redundant
	// key is written into the file to say it.
	if lc.Backend() == name {
		fmt.Fprintf(cmd.OutOrStdout(), "this installation already runs on %s: %s is unchanged\n", name, sel.Path)
		return nil
	}
	if name == backend.CloudBatch {
		if err := backendsUseGate(cmd.Context(), lc); err != nil {
			return err
		}
	}
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: sel.Path})
	if err != nil {
		return err
	}
	next, err := yamledit.SetScalar(old, []string{"backend"}, name)
	if next, err = workspaceEditResult(sel.Path, next, err); err != nil {
		return err
	}
	e := settingsEdit{
		Old:     old,
		New:     next,
		What:    backendsUseWhat(sel.Path, name),
		Command: "fugaro backends use " + name,
	}
	if name == backend.CloudBatch {
		// The widening: a run's job is a VM of its own with a Docker daemon on
		// it, which the design's §1 is about (that daemon is root on the host
		// running this installation's code), and this selection is what moves
		// every launch of the installation onto it.
		e.Typed = true
		e.TypedWhy = fmt.Sprintf("this moves every launch of %s onto Cloud Batch: a run's job is a Compute Engine VM, one per run, with the Docker daemon of a Container-Optimized OS boot image available to it through a bind-mounted socket -- so a run that asks for Docker gets root on the host that runs this installation's code (design compute-backend.md §1) -- and the reaper is what sweeps the VMs and jobs this installation leaves behind (nothing else does)", lc.Name)
	}
	if err := projectConfigEditLocal(cmd, lc, sel, e); err != nil {
		return err
	}
	if yamledit.Changed(old, next) {
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n", backendsUseAfter(name, lc.Name))
	}
	return nil
}

// backendsUseOptions are `fugaro backends use`'s flags: the settings family's
// two aiming flags (settingsAimFlags) and nothing else. There is no --yes: this
// write asks nothing, and the checks in front of it are what stands in place of
// a question.
type backendsUseOptions struct {
	project string
	config  string
}

// backendsUseWhat is the sentence the write prints before it writes. It names
// the file and the backend, and not the effective "from": a config that carries
// no backend: key runs on cloud-run, and printing that as the value the file
// held would be a value the file never carried.
func backendsUseWhat(path, to string) string {
	return fmt.Sprintf("point %s at %s: its runs are launched on %s from the next run on", path, to, to)
}

// backendsUseAfter is what the owner reads once the selection is written: where
// every run of the installation goes from now on, and the command that puts it
// back.
func backendsUseAfter(name, project string) string {
	if name == backend.CloudRun {
		return fmt.Sprintf("every run of %s is launched on Cloud Run from now on (fugaro backends use %s moves it back to Cloud Batch)", project, backend.CloudBatch)
	}
	return fmt.Sprintf("every run of %s is launched on Cloud Batch from now on: one VM per run, with a Docker daemon, and that VM and its job are the reaper's to sweep (fugaro backends use %s moves it back to Cloud Run)", project, backend.CloudRun)
}

// backendsUseGate refuses selecting cloud-batch for an installation that has
// not passed the Batch half of `fugaro doctor`. It reads the checks themselves
// (doctorBatchChecks) and not a second derivation of the same facts, at the
// strictness doctor's own gate fails at -- every failing check counts, warning
// or not, because this very command is the installation selecting the backend
// -- plus the one thing doctor answers differently here: a config that carries
// no batch block at all is refused whatever the checks say, since
// doctorBatchChecks is silent about a block that is not there and that silence
// is not a pass.
func backendsUseGate(ctx context.Context, lc *localcfg.Config) error {
	if lc.Batch == nil {
		return userErr("refusing to select %s: this installation's config carries no batch block, so nothing on this machine knows the machine type, the boot image, the network or the account a run's VMs would be built from, and every launch on it would fail at create. Provision it first: fugaro setup --provision-batch writes the block and applies the compute root, then repeat this command", backend.CloudBatch)
	}
	c, err := crm.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.ResourceManager)...)
	if err != nil {
		return remote(err)
	}
	fails := doctorRefusals(doctorBatchChecks(ctx, lc, c), true)
	if len(fails) == 0 {
		return nil
	}
	// The first few, in doctor's own words and with each one's own fix: a
	// refusal carrying fifteen paragraphs is unreadable, and the command that
	// prints them all is one line for the owner to run.
	const shown = 3
	named := make([]string, 0, shown)
	for _, f := range fails[:min(shown, len(fails))] {
		named = append(named, f.ID+": "+f.Problem+" (fix: "+f.Fix+")")
	}
	more := ""
	if len(fails) > shown {
		more = fmt.Sprintf(", and %d more", len(fails)-shown)
	}
	return userErr("refusing to select %s: %d of fugaro doctor's Batch checks do not pass for this installation -- %s%s. Selecting the backend before they pass would move every launch onto VMs nothing provisioned for it, so nothing was written. Run fugaro doctor to see every one of them, fix what each one names, and repeat this command", backend.CloudBatch, len(fails), strings.Join(named, "; "), more)
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
		note = backendSelectionNote(err)
		fmt.Fprintf(cmd.ErrOrStderr(), "note: %s\n", note)
		return nil, note, nil
	}
	return lc, "", announce(o, sel, lc)
}

// backendSelectionNote is the note the operator (stderr) and a --json caller
// (backendsListDoc.Note) both read when no project could be selected. Its text
// goes through pluginwire.Printable for the same reason the project name does:
// it carries a selection failure's own message, and that message can quote a
// name out of a file this process did not write — a local config or a shared
// config a stranger can write — so a control character in one must not reach
// the terminal (or, through the --json document, a caller's own printer) raw.
func backendSelectionNote(err error) string {
	return pluginwire.Printable(fmt.Sprintf("no project config could be selected, so which backend this project uses is unknown (the list below is every backend a project may name) (%v)", err))
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
