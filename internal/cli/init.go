package cli

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	// The managed-mode package of internal/managed, aliased because this file
	// already imports internal/infra/managed as managed (the creation fact's
	// object): managedmode is the package of the managed apply path, and the
	// state bucket below is its first caller in the CLI (Task 3.1's apply is
	// the next).
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/mirror"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/preflight"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
)

// stdinIsTerminal reports whether in is a terminal, where a confirmation
// can be typed. Tests replace it.
var stdinIsTerminal = func(in io.Reader) bool {
	f, ok := in.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

type initOptions struct {
	// strictCreate and created are fugaro restore's, and nothing else sets
	// them: no flag reaches either (restore.go builds the child with
	// newInitCmdWith). strictCreate makes the creation FAIL on a project
	// that already exists, even one an earlier run of fugaro setup created
	// and a plain first run would resume; created, when set, is
	// filled with what this run did, for the caller to check.
	strictCreate                        bool
	created                             *initCreation
	cloud                               cloudOptions
	schedulerRegion                     string
	runsBucket, stateBucket             string
	baseImages                          []string
	baseKinds                           []string // --base: base kinds to mirror now
	imageSource                         string   // --image-source: a fork's own release registry/owner
	expectDigests                       []string // --expect-digest KIND=sha256:...: the digest a release tag must resolve to
	replaceImages                       []string // --replace-image KIND: kinds (history) whose tag in your registry may be replaced, each an ordinary covered step (askCovered: D12's answer of 2026-10-20 covers it in both doors, and a terminal without --yes types the project's name); a base kind is refused there, since nothing is copied for it any more
	launchers, operators                []string
	budget                              int64
	budgetCurrency, billingAccount      string
	alertEmail                          string
	noLogIsolation                      bool
	registryCleanup                     string
	planOnly, printVars, configOnly     bool
	publishConfig                       bool // --publish-config: publish the shared config to the runs bucket and stop
	anchor                              bool // --anchor: write the checkout's gcp_project line, after its checks, and stop
	forget, yes, asJSON                 bool
	nonInteractive                      bool
	provisionBatch                      bool   // --provision-batch: provision the Batch backend's installation-level root (owner-run, typed confirmation)
	provisionCredentials                bool   // --provision-credentials: turn the credentials service on in the installation root (owner-run, typed confirmation)
	coordinator                         bool   // --coordinator: opt the installation in to the coordinator's aggregation job (owner-run, at a terminal, never --yes)
	onboardRepo                         string // --onboard-repo owner/name: the explicit opt-in to onboard the checkout's repository
	allowFork                           bool   // --allow-fork: the plugin-wiring stage may move a fork's marketplace ref
	allowDelete                         []string
	launchersChanged, operatorsChanged  bool
	alertEmailChanged, baseImageChanged bool

	// --firebase-project names a separate Firebase project to build the budget
	// backend in; empty means the installation's own GCP project, which is the
	// default (firebaseProject, D7). --budget-mode seeds the mode and
	// --budget-admin adds admins to the owners and editors.
	firebase            string
	budgetMode          string
	budgetAdmins        []string
	budgetAdminsChanged bool

	// --create-project creates the GCP project --gcp-project names (and adds
	// Firebase to it), behind its own typed confirmation; --link-billing
	// links that billing account to it, behind a second one. --yes covers
	// neither (internal/initflow, init_project.go).
	createProject                    bool
	displayName, parent, linkBilling string

	// --managed is a creation of a managed project (Task 4.2, design D68's
	// spelling of the command): the monthly budget amount is required, with
	// no default (D64), and it is written into the project's creation fact
	// so a later run and a restore can read what was chosen
	// (internal/infra/managed, MarkerVersionBudget). It goes with
	// --create-project and nothing else — a project that already exists is
	// made managed by the owner's own command instead, `fugaro managed flag`
	// (D58; Task 6.1), never by init — and it removes no confirmation: a
	// managed creation is a creation with more guards, not fewer.
	managed bool

	// name is the project's name, for a project config fugaro setup
	// creates; with one, it must be that config's name.
	name string

	// --repo: onboard the repository of a checkout.
	repo, noBuild, allowJobDelete bool
	// retire is --retire: run the retirement a config change needs, behind
	// two typed confirmations --yes cannot cover (init_retire.go).
	retire      bool
	githubAppID string
	checkApp    bool // --check-github-app

	// door is the front door this run is: "setup" or "update". It is set by
	// the constructor that built the command (setup.go, update.go) and read
	// only by loopProgram, so that every line the converge loop prints names
	// the command the user is actually running rather than one that no longer
	// exists.
	door string
}

// convergeDoor is one of the two front doors of the converge (setup.go,
// update.go): everything about a door that is not the converge itself. The
// engine, the flags and the run are shared — the doors differ in what they
// say, what they are called and what they may create.
type convergeDoor struct {
	name string // the command's name, as a user types it: setup or update
	use  string // cobra's Use, without the program
	// short and long are the door's own help. The retired command's Long was
	// one text; W1.2 splits it, because a command that creates and a command
	// that re-converges cannot share one description.
	short, long string
	// foreign are the flags this door declares but does not take: the other
	// door's. They are declared (so the flag set is one flag set and a
	// converge is driven the same way from either door), hidden (so the help
	// does not offer what the door refuses) and refused by name when a run
	// actually passes one.
	foreign []foreignFlag
}

// takes is whether this door takes the named flag: the foreign list, for the
// help a door has to word for itself. A usage string that says what a flag
// does may not name one the door refuses — setup's --non-interactive told the
// reader what --forget does on the door that rejects --forget by name
// (TestNoDoorsHelpOffersAFlagItRefuses).
func (d convergeDoor) takes(name string) bool {
	for _, ff := range d.foreign {
		if ff.name == name {
			return false
		}
	}
	return true
}

// passedForeign is the first foreign flag the run actually passed, if any: the
// one decision that comes before anything is read, prompted for or applied.
func (d convergeDoor) passedForeign(cmd *cobra.Command) (foreignFlag, bool) {
	for _, ff := range d.foreign {
		if cmd.Flags().Changed(ff.name) {
			return ff, true
		}
	}
	return foreignFlag{}, false
}

// foreignFlag is a flag one door declares and the other takes, with the door
// that owns it and why. The refusal it drives is a sentence that names both —
// `fugaro update does not take --create-project: that is fugaro setup's (...)`
// — because cobra's own answer to a flag a command does not have is "unknown
// flag", which leaves the user to guess which of the two commands to run.
type foreignFlag struct {
	name  string
	owner string // the door that takes it, as the refusal names it
	hint  string // why it is that door's
}

// loopProgram is the command every line the converge loop prints names
// (initflow's Options.Program, task W1.1): the front door this run is. The
// loop is behind both doors and cannot know which one is running (O2), so the
// door's name is passed in here — and a fix that reads "rerun fugaro" is one
// nobody can act on, which is why it is set and not left empty.
//
// The doors are the only constructors that set the name. A hand-built
// initOptions — a test's — has no door, and then the loop prints its own bare
// default rather than a command that does not exist.
func (o *initOptions) loopProgram() string {
	if o.door == "" {
		return ""
	}
	return "fugaro " + o.door
}

// doorName is the verb the engine's own lines name — a left-for-you command
// line a stage builds, the refusal a step prints — where loopProgram names it
// for the loop's lines. The reason is the same one: setup and update run this
// one engine, and a line that named one door to a user of the other is the
// failure loop_program_test.go exists for. The verb used to be `init`, which
// is a tombstone now; no line of this package says it any more (task W2.1).
//
// A run with no door is a hand-built initOptions — a test's, never a user's,
// because the two constructors are the only things that set it (newSetupCmdWith
// and newUpdateCmdWith). Such a run is named `setup`, the door every run of
// this engine began as, rather than a command nobody can type.
func (o *initOptions) doorName() string {
	if o.door == "" {
		return "setup"
	}
	return o.door
}

// doorProgram is doorName with the program in front, for the sentences that
// spell a whole command line.
func (o *initOptions) doorProgram() string { return "fugaro " + o.doorName() }

// enrolling is whether this run may enrol a repository the local config does
// not list by asking for it: with the typed owner/name, or with --onboard-repo.
// `setup` may, in both of its modes — it owns that question (D5) — and `update`
// may only in its --repo mode, where the user named the checkout to converge.
// `update`'s converge never asks: no typed owner/name, no --onboard-repo, and a
// checkout whose repository the config does not list is left with the line that
// names `fugaro setup`. That is its asking, not its covering — `--yes` covers
// onboarding an unlisted repository in either door's converge (D12's answer of
// 2026-10-20, authState/onboardCovered), so this is what a run that gave no such
// confirmation falls back on (W5.1).
func (o *initOptions) enrolling() bool {
	return o.door != updateDoor.name || o.repo
}

// initCreation is what a run of setup --create-project did, as the run itself
// knows it (not inferred from the cloud afterwards): the project it created,
// whether the project was already there when it was read, and how much of the
// mark it wrote.
//
// The mark is TWO writes (Task 3.0) and both are kept apart here, because the
// half state is real and its recovery differs: Fact is the creation fact in
// the runs bucket, written with managed.WriteMarker's create-only
// precondition, so a fact someone else put there first is never this run's;
// Marked is both halves — Fact and the fugaro_managed label on the project —
// which is what the read requires and so the only thing that makes the
// project managed. A run that wrote Fact and not the label (the label needs
// resourcemanager.projects.update, which the credentials init runs on may
// lack) has created a project the read answers "not managed" for, and the
// owner-run label write is its way back.
type initCreation struct {
	Created string
	Existed bool
	Fact    bool // the creation fact is in the runs bucket: this run wrote it
	Marked  bool // and the project carries the label: both halves, so managed
}

// newInitCmdWith is the shared converge-command constructor: the command both
// front doors are built by (newSetupCmdWith, newUpdateCmdWith), over options
// the caller holds. fugaro restore's child sets strictCreate and created on
// them before the flags are parsed into the same value, and runs the setup
// door.

// retiredFirebaseFlag is the flag --firebase-project replaced (D7), written
// once: the SetFlagErrorFunc below refuses that spelling by name, and the test
// that pins the rename (init_firebase_test.go's TestOldFirebaseFlagIsGone)
// reads it from here. A retired name that a sentence and a scan each spell out
// separately is two names that drift.
const retiredFirebaseFlag = "firebase"

func newInitCmdWith(o *initOptions, d convergeDoor) *cobra.Command {
	o.door = d.name
	cmd := &cobra.Command{
		Use:   d.use,
		Short: d.short,
		Long:  d.long,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			o.launchersChanged, o.operatorsChanged = f.Changed("launcher"), f.Changed("operator")
			o.alertEmailChanged, o.baseImageChanged = f.Changed("alert-email"), f.Changed("base-image")
			o.budgetAdminsChanged = f.Changed("budget-admin")
			r := newInitRun(cmd, o)
			// A flag that belongs to the other door: refused here, by name,
			// before anything is read, prompted for or applied, so a script
			// that re-converges with a creating flag gets a sentence naming the
			// door that takes it and not a converge that half-happened.
			//
			// It is the first decision the run makes, and it is made on the run
			// rather than before the run exists: building the run reads
			// nothing, and a refusal is an outcome like any other — the check
			// below prints the one --json object for it, as it does for every
			// other refusal that stops a run before a stage
			// (TestInitJSONOnARefusalBeforeAnyStage). Deciding it before the
			// run existed left --json printing nothing at all for this one
			// path, so a machine consumer got a document or an empty stdout
			// depending on which refusal fired.
			ff, foreign := d.passedForeign(cmd)
			var err error
			switch {
			case foreign:
				err = userErr("fugaro %s does not take --%s: that is %s's (%s)", d.name, ff.name, ff.owner, ff.hint)
			case o.repo:
				err = runInitRepo(r, args)
			case len(args) > 0:
				err = userErr("a checkout path is for --repo; fugaro %s takes no argument", d.name)
			default:
				err = runInit(r)
			}
			if err != nil && o.asJSON && !r.printed {
				// A refusal before any stage ran: --json still prints one
				// object, with the reason (the exit code is the error's).
				r.res.Error = err.Error()
				_ = r.printResult()
			}
			return err
		},
	}
	// --firebase is gone, not aliased (D2's reasoning applies to a flag too,
	// D7): a script that still passes it gets a sentence naming the flag that
	// replaced it instead of pflag's bare "unknown flag: --firebase", which
	// says what changed but not what to type. Only that one spelling is
	// answered — the comparison is on pflag's whole message, so --firebase-db
	// and every other unknown flag keep cobra's own error, as the comment
	// above says.
	//
	// This refusal prints no --json object, and that is the one place it
	// differs from the refusals the run makes (the door's foreign flag, check,
	// the missing inputs above): it is raised by pflag while the flags are
	// parsed, before there is a run to answer with, so it is an unknown-flag
	// error like any other in that respect — `fugaro setup --json
	// --no-such-flag` prints the same bare sentence. Its exit code is the
	// user's (ExitUserError), which is the half a script can act on, and
	// TestOldFirebaseFlagIsGone holds the two spellings to the same shape so
	// neither drifts into a document the other does not print.
	cmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		if err.Error() == "unknown flag: --"+retiredFirebaseFlag {
			return userErr("--%s is --firebase-project now: pass --firebase-project <gcp-project-id> to build the budget backend in the installation's own GCP project, the only project Fugaro uses for it (fugaro %s --help)", retiredFirebaseFlag, c.Name())
		}
		return err
	})
	addCloudFlags(cmd, &o.cloud)
	f := cmd.Flags()
	f.StringVar(&o.name, "name", "", "the project's name: for a project config "+d.name+" creates, and what names an installation that has no name yet (with a config, it must be its name; an installation's name never changes; with several project configs it names the one to create or use; on a machine with no config for an installation that exists, it defaults to the installation's)")
	f.StringVar(&o.schedulerRegion, "scheduler-region", "", "Cloud Scheduler region of the daily image checks (default: the region, or the nearest one Scheduler offers)")
	f.StringVar(&o.runsBucket, "runs-bucket", "", "the runs bucket (default: the project config's, else fugaro-runs-<gcp-project>)")
	f.StringVar(&o.stateBucket, "state-bucket", "", "the Terraform state bucket (default: the project config's, else fugaro-tfstate-<gcp-project>)")
	f.StringArrayVar(&o.baseImages, "base-image", nil, "a base image the image checks run and builds start from, recorded in the local config under its base kind (repeatable; KIND=IMAGE, or just IMAGE when its repository is named fugaro-<kind>; the kinds you don't name keep theirs)")
	f.StringSliceVar(&o.baseKinds, "base", nil, "copy the release's base image (the only kind: base) into the project's registry now, besides what the checkout's fugaro.yaml needs (comma-separated or repeated)")
	f.StringVar(&o.imageSource, "image-source", "", "the registry and owner the release images come from: a base kind is resolved and signature-verified there and pinned through the installation's remote repository, a history image is copied from it (default ghcr.io/dimipaun; a fork names its own, such as ghcr.io/acme)")
	f.StringArrayVar(&o.expectDigests, "expect-digest", nil, "pin the digest of a release image, KIND=sha256:<hex> (KIND is base or history; repeatable): the digest of the source tag's manifest (the index), obtained out of band such as from the release notes. A base kind is cross-checked against it: its digest is verified against the release's cosign signature either way, and the two must agree. For history it is the trust anchor: without it "+d.name+" trusts what the release tag in ghcr.io resolves to now, which is whoever can write that tag")
	f.StringArrayVar(&o.replaceImages, "replace-image", nil, "KIND (history; repeatable): let the converge replace that image's tag in your registry when it names another image, a release tag or history:latest (never done otherwise, so a hand-pushed history:latest needs it once). A base kind is refused: it is pinned by digest through the remote repository, so nothing of it is ever copied over. It names the intent only: replacing is an ordinary covered step, so --yes covers it in either door and a terminal without --yes types the project's name (a pipe, --json, --non-interactive and a coding agent's session confirm nothing)")
	f.StringArrayVar(&o.launchers, "launcher", nil, "an IAM member who launches and watches runs (repeatable; default: the local config's, and for a new config you, the authenticated user)")
	f.StringArrayVar(&o.operators, "operator", nil, "an IAM member who onboards repositories (repeatable; default: the local config's, and for a new config you, the authenticated user)")
	f.Int64Var(&o.budget, "budget", 0, "a monthly budget on the project, in whole units of --budget-currency")
	f.StringVar(&o.budgetCurrency, "budget-currency", "", "the budget's currency, such as USD")
	f.StringVar(&o.billingAccount, "billing-account", "", "the billing account the budget is on")
	f.StringVar(&o.alertEmail, "alert-email", "", "where failed image checks and rebuilds are reported (default: the local config's)")
	f.BoolVar(&o.noLogIsolation, "no-log-isolation", false, "leave Fugaro job logs in _Default instead of their own log bucket")
	f.StringVar(&o.registryCleanup, "registry-cleanup", "", "Artifact Registry cleanup: dry-run (the default), on or off")
	f.StringVar(&o.firebase, "firebase-project", "", "turn the shared budget on: the value is the installation's own GCP project id (the default; the flag is only needed to turn the shared budget on), and any other id is refused. It is the flag that asks for the backend — a run without it builds none — and that backend is three confirmed applies: the database's rules and mark, the Firestore history database (permanent us-east5 location: typed where it is a first-time question, covered by --yes where the run re-converges an installation that is already there) and the config")
	f.StringVar(&o.budgetMode, "budget-mode", "", "with --firebase-project: off, observe or enforce: the jobs' budget mode in the project config, and for observe and enforce the project-wide mode in the database (an absent one is seeded observe)")
	f.StringArrayVar(&o.budgetAdmins, "budget-admin", nil, "with --firebase-project: an IAM member who may change caps and kill switches besides the GCP project's owners and editors (repeatable; default: the local config's terraform.budget_admins)")
	f.BoolVar(&o.createProject, "create-project", false, "create the GCP project --gcp-project names and add Firebase to it, as you, after you type its ID at a terminal (never with --yes, --non-interactive or --json). A first run of "+d.name+" always creates its project, so the flag only says so explicitly. Fugaro only starts fresh: an ID that already exists is refused, unless an earlier run of "+d.name+" created it (the resume). Billing is not linked unless --link-billing is also given; without it "+d.name+" prints the one gcloud command that links it")
	f.StringVar(&o.displayName, "display-name", "", "on a first run, which creates the project: its display name (default: its ID)")
	f.StringVar(&o.parent, "parent", "", "on a first run, which creates the project: organizations/<number> or folders/<number> to create it under (default: no parent; an organization is never guessed)")
	f.BoolVar(&o.managed, "managed", false, "on a first run, which creates the project: record its monthly budget in the creation fact. Every project "+d.name+" creates is Fugaro-managed (its runs bucket's creation fact and the fugaro_managed label); --managed is what records the budget amount the managed apply needs. The monthly budget amount is required — --budget, --budget-currency and --billing-account together, with no default and no suggestion taken for you — and the amount and the currency are recorded in the project's creation fact (gs://fugaro-runs-<gcp-project>/fugaro/managed.json), where a later run and a restore read what was chosen. It removes no confirmation: the project's creation still types its ID, and the installation's ordinary steps share the run's one review. Flagging a project that already exists as managed is fugaro managed flag's, run by the owner at a terminal, and never "+d.name+"'s")
	f.StringVar(&o.linkBilling, "link-billing", "", "on a first run, which creates the project: link this billing account (an ID such as 0123AB-4567CD-89EF01) to the project, after you type the account's ID at a terminal. This may incur charges on that account; never with --yes. It never changes a project's existing billing link")
	f.BoolVar(&o.planOnly, "plan-only", false, "stop after showing the plan (never takes --yes: it creates nothing on a flag's word)")
	f.BoolVar(&o.printVars, "print-vars", false, "print the Terraform variables and exit, with no cloud calls and no Terraform (ungated: no discovery, and with --repo no readiness gates)")
	f.BoolVar(&o.configOnly, "config-only", false, "only write the local config, from the installation's outputs (else the flags)")
	f.BoolVar(&o.publishConfig, "publish-config", false, "publish the shared config to the runs bucket and stop ("+d.name+" does it after it writes the local config)")
	f.BoolVar(&o.anchor, "anchor", false, "in a checkout of an onboarded repository, only write the gcp_project line into its fugaro.yaml, after checking that every workflow's job image was submitted by fugaro "+gcpProjectFieldSince+" or later and built FROM a release base image of "+gcpProjectFieldSince+" or later, and that no base image the local config sets is a development or older one (no Terraform, no publish, no image copy; the diff, then --yes writes or a terminal asks)")
	f.BoolVar(&o.forget, "forget", false, "roll back: turn log isolation and registry cleanup off, then remove every address from Terraform's state. An installation that holds the managed deployer's fence has no rollback and this is refused there: the state removal would leave the seven scoped roles and their seven conditioned bindings live in the project, accounted for by nothing, and taking the project out of managed mode does not remove them (docs/design/managed-projects.md §10.2, \"Rolling back\")")
	f.StringArrayVar(&o.allowDelete, "allow-delete", nil, "a resource address the plan may delete or replace (repeatable)")
	f.BoolVar(&o.provisionBatch, "provision-batch", false, "after the installation's ordinary steps, provision the Batch compute backend into a state of its own (design docs/design/compute-backend.md §8.1 task 6): the Compute and Batch APIs, the run accounts' IAM, the launcher's, reaper's and preload accounts and roles, a Cloud NAT and router, and a preloaded boot image rebuilt on a schedule. It is money (a billable build, a NAT's hours) and permanent, so --yes never covers it and it is typed at a real terminal; nothing runs it by default, and no run can use the backend until the enable task. It excludes --repo and the flags that only read or only stop (--forget, --print-vars, --config-only, --publish-config, --anchor)")
	// D12's answer of 2026-10-20 (docs/setup-wizard/reconciliation.md §5, and
	// §5 of docs/coordinator/reconciliation.md): --yes is total over the
	// ordinary steps a door owns, and the one step that moves between the doors
	// is the Firestore database's location — covered in `update`, kept typed in
	// `setup`, the door that asks for it as a first-time question. The door
	// that takes --forget is `update`.
	//
	// Both doors print this one flag set, so both halves of the sentence have
	// to be the reader's own door: a line naming the other one tells half its
	// readers to switch commands for nothing
	// (TestNoDoorsFlagHelpNamesTheOtherDoor).
	covers := "a billable first image build, replacing a registry tag and onboarding an unlisted repository (on a provider's own host, with an origin git config that does not rewrite it)"
	never := "creating a project, linking a billing account, the Firestore database's location, or a secret value"
	if d.takes("forget") {
		// update is also the door that runs the checkout's own steps, whose
		// cloud half is the image refresh: every one of its billable builds —
		// a rebuild of the workflow images this checkout already has — is
		// asked for by itself, and --yes answers each of them, the same
		// answer the door gives a repository's first build
		// (internal/cli/image_refresh_steps.go, askBuild).
		covers = "the Firestore database's location, a rebuild of the checkout's workflow images, " + covers
		// The location is one of this door's covered steps, so it is not in
		// this door's never list; the two that cannot arise here at all are
		// still true of --yes wherever they can (they are the other door's
		// flags, refused by name here).
		never = "creating a project, linking a billing account, or a secret value"
	}
	// The agent clause is about what leaves this machine, and about how far
	// such a session actually gets: the writes on this machine's own files are
	// the only ones an apply there may make (the checkout's pin and plugin
	// wiring; the checkout stage is LocalUnderAgent for exactly those two),
	// and they are reached only when every stage before the checkout is done —
	// the loop stops at the first stage that needs the user and blocks the
	// rest. The plugin stage runs before the checkout on the same pin and is
	// not LocalUnderAgent, so a stale plugin stops the run at the plugin
	// (`fugaro update-skills`) with the checkout's local steps never reached
	// (TestUpdateAgentSessionStopsAtThePluginBeforeTheCheckout). The help has
	// to say that rather than promise a session a write it may not get to.
	f.BoolVar(&o.yes, "yes", false, "confirm the ordinary steps without asking (only after reading what they do). It covers every ordinary step this door has — "+covers+" — and it never covers "+never+": each of those is typed at a real terminal, and no door relaxes them. Under a coding agent's environment variable (CLAUDECODE and the like) "+d.name+" changes nothing outside this machine, --yes included, and the steps it will not run are listed under left_for_you (exit 1): the exceptions are "+d.name+" --anchor, which only edits the checkout's fugaro.yaml and honours --yes there, and the checkout's own local steps (the .claude/settings.json wiring and pin), whose every write is on this machine's own files — and those are reached only where every stage before the checkout is done, because the first stage that needs you stops the run and blocks the rest: the plugin stage runs before the checkout on the same pin and is not local-under-agent, so a session whose plugin is stale stops there (`left for you`, exit 1) and the checkout is untouched. It fails closed rather than promising a write it may never reach, and none of it is a barrier: it is a mitigation — an agent that unsets its own variables is not stopped, and the real controls are the typed confirmations and the skills' lint")
	f.StringVar(&o.onboardRepo, "onboard-repo", "", "owner/name of the checkout's origin repository (compared case-insensitively, without .git): the one opt-in that needs no terminal to wire and onboard a repository the project does not list yet, on github.com or bitbucket.org with an origin git config does not rewrite (--yes also covers onboarding such a repository, and neither it nor this flag is honoured in a coding agent's session; any other origin takes the typed confirmation)")
	f.BoolVar(&o.provisionCredentials, "provision-credentials", false, "after the installation's ordinary steps, turn ON the credentials service in the installation root (docs/credentials-service, D146): a Cloud Run service (its own account, one Secret Manager bundle secret) running this release's image pinned by digest through the installation's remote repository. It is the identity the REPOSITORY jobs' model credentials move to: every workflow job of every repository root re-applied after this drops its model credential — in every agent.auth mode, oauth and api-key as much as cascade — and its account's accessor on it, and gets an invoker grant on the service instead. It does NOT reach the shared project jobs, which keep mounting their provider keys and CLAUDE_CODE_OAUTH_TOKEN with their grants (C3.9 moves those). And it is wired from this run on: the service's address is recorded in the project's config and put on every per-repository launch, so a cascade run takes its model credential from the service and a GitHub run has its installation token minted there rather than in the job. A repository job in another agent.auth mode has no model credential to use and nothing yet to replace it (that half is not in this release), so a project that opts in runs its workflows with agent.auth: cascade; and the bundle has to hold the GitHub App's key (fugaro creds set github-app-key) before any GitHub run of the installation can mint. It needs the installation's Firebase rules (fugaro "+d.name+" --firebase-project) at the credentials generation first. --yes never covers it and it is typed at a real terminal, and a coding agent's session is refused. It excludes --repo and the flags that only read or only stop (--forget, --print-vars, --config-only, --publish-config, --anchor), and it does NOT exclude --provision-batch: they are two applies of different roots, each with its own typed confirmation, and naming both means both")
	f.BoolVar(&o.coordinator, "coordinator", false, "with --firebase-project, opt the installation in to the coordinator's aggregation job (docs/coordinator, P5): the fugaro-coordinator service account, its roles/firebasedatabase.admin on the Firebase project (IAM cannot scope it to the coordinator's subtree; the job's own code does), roles/storage.objectViewer on the runs bucket, and — once the history image exists and the local config records a GitHub App ID — the Cloud Run job that holds the App key, its accessor on that one secret and a Scheduler job that runs it every 15 minutes. Nothing creates any of it by default. The opt-in is recorded in the local config (terraform.coordinator) once the apply has created the account, so later runs keep it. --yes never covers it, it is confirmed at a real terminal, and a coding agent's session is refused. It excludes --repo and the flags that only read or only stop (--forget, --print-vars, --config-only, --publish-config, --anchor)")
	f.BoolVar(&o.allowFork, "allow-fork", false, "let the plugin-wiring stage move the ref of a fugaro marketplace in .claude/settings.json that names a repository other than dimipaun/fugaro (a fork you host); never done otherwise")
	// The flags --non-interactive only stops from prompting, worded for this
	// door: --forget is fugaro update's, and setup's help must not describe
	// what a flag setup refuses does.
	stops := "--config-only"
	if d.takes("forget") {
		stops = "--forget and --config-only"
	}
	f.BoolVar(&o.nonInteractive, "non-interactive", false, "never prompt, and never read stdin: a step that needs you is listed under left_for_you (exit 1), and applying needs --yes, else the run only plans (a fresh state bucket needs its typed confirmation even then, so that plan exits 1); --yes never covers the typed-only steps (see --yes). With "+stops+" it only stops them prompting: their confirmations then need --yes")
	f.BoolVar(&o.asJSON, "json", false, "print the result as JSON on stdout (progress goes to stderr)")
	f.BoolVar(&o.repo, "repo", false, "onboard the repository of the checkout at PATH (default: the current directory) instead of the installation")
	f.BoolVar(&o.checkApp, "check-github-app", false, "let the GitHub App pre-check read the App's private key from Secret Manager with your own credentials (held in memory only, for one signed token; refused in a coding agent's session; GODEBUG=http2debug would print the token, so unset it); without it "+d.name+" checks only with the key you typed in this run")
	f.StringVar(&o.githubAppID, "github-app-id", "", "the GitHub App's ID, for a GitHub repository (not a secret; recorded in the local config; "+d.name+" asks once at a terminal, and --non-interactive needs it)")
	f.BoolVar(&o.noBuild, "no-build", false, "with --repo: don't offer the first image builds")
	f.BoolVar(&o.allowJobDelete, "allow-job-delete", false, "with --repo: lower the jobs' deletion protection, for offboarding")
	f.BoolVar(&o.retire, "retire", false, "with --repo: run the retirement this config change needs (a workflow or a named account removed, a secret no longer mounted): remove the protected addresses from Terraform's state (destroying nothing), apply, then delete the orphaned Cloud Run jobs through the API. The project's name is typed twice at a real terminal, and --yes, --non-interactive, --json, a pipe and a coding agent cover neither. Excludes --plan-only, --print-vars and --forget, which read or forget rather than apply")
	// The other door's flags are declared above and hidden here: a user
	// reading this door's help must not be offered a flag it refuses, and
	// --non-interactive's own list of what a run needs must not gain one.
	for _, ff := range d.foreign {
		_ = f.MarkHidden(ff.name)
	}
	return cmd
}

// initRun is one run of the converge, whichever front door it is (task
// W2.1: the verb it used to name was `init`, which is a tombstone now).
type initRun struct {
	cmd *cobra.Command
	o   *initOptions
	w   io.Writer // the human-readable account: stdout, or stderr with --json
	in  *bufio.Reader
	// gcpProject is the GCP project ID every Google call uses;
	// projectName is the project's name, shown and typed to confirm.
	gcpProject, projectName string
	res                     initResult

	// update --firebase-project: the Firebase root's outputs once known (the history
	// job's environment), a change to the local config the run makes, the
	// text appended to the installation apply's confirmation, whether the
	// history job's missing image has been said, and whether the
	// coordinator's missing App ID has.
	fb               *infra.FirebaseOutputs
	mutate           func(*localcfg.Config)
	installLabel     string
	historyNoted     bool
	coordinatorNoted bool
	printed          bool // the --json result has been printed
	anchorNoted      bool // update --repo's pointer at --anchor has been said
	// notedOpenProviders is the once-per-run guard on the note about the
	// providers every repository may use: the converge's repository stage
	// runs the repo engine too, and the note belongs once.
	notedOpenProviders bool

	// buildsLeft is the workflows whose first image build was not offered
	// because this run cannot take a typed confirmation (--yes, --json, a pipe
	// or a coding agent): the run ends needs-you, with the typed route.
	buildsLeft []string
	// buildHold is why those builds were not offered at all: the GitHub App's
	// pre-check failed (its problem and fix, one line); "" when they were
	// offered and not typed.
	buildHold string
	// appKeyMem is the GitHub App's key the user typed at this run's secrets
	// stage, held in memory for the pre-check and cleared when the run ends.
	appKeyMem []byte

	// resumedCreation is whether the project stage resumed a creation an
	// earlier run of fugaro setup made: the project was there, and it carries
	// infra.CreatedLabelKey, which only that run's create request writes
	// (projectStage.read). markCreated marks it as it marks a creation.
	resumedCreation bool

	// resumeConfirmed is whether the person typed the resumed project's ID to
	// have it marked managed (confirmResumedMark).
	resumeConfirmed bool

	// billingUnlisted is whether the wizard's billing question found no
	// account to offer: the list could not be read (the API off on the quota
	// project, a missing permission) or it was empty. The project stage says
	// so before the creation's confirmation (billingHeadsUp) and goes on.
	billingUnlisted bool

	// projectExisted is whether the project this run installs into was
	// already there when the project stage read it (projectStage.read). It
	// is the fact markCreated's --managed warning needs and r.res.Created
	// cannot give: an empty Created says this run created no project, which
	// is what resuming an earlier run's creation leaves behind — and also what --plan-only, a declined
	// or unaskable typed confirmation, an environment refusal and a failure
	// before the project stage leave behind.
	projectExisted bool

	// review is the run's one confirmation, set only by the converge
	// (init_review.go).
	review *runReview

	// managed is true once this run read that the project it installs into
	// is managed (managedProject): a guard refusal's text is then the managed
	// one. Only true is remembered; false and an error are read again.
	managed bool

	// policy is the project's published policy, read at most once per run
	// through the operator's own credentials (policyReadOnce.read). Readiness's
	// D11 gate turns on whether one exists.
	policy policyReadOnce
	// The retirement (init_retire.go): what the run read before its first
	// plan -- the state's addresses, the retirement set of design §3.1a and
	// the notes Orphans returned -- and whether its flow ran and applied, so
	// the API delete is attempted once, after the last apply.
	retireState   []string
	retireSet     []infra.Retirement
	retireNotes   []infra.Retirement
	retired       bool     // the flow was entered (a later plan must not re-enter it)
	retireApplied bool     // it applied: the jobs it removed from state still need deleting
	retireRemoved []string // what the state removal took out: what a stop before the API delete owes (M-B)
	retireClean   bool     // the resume's plan changed nothing: no apply is left, only the API delete
}

// initResult is what --json prints.
type initResult struct {
	Project    string                     `json:"project"`
	GCPProject string                     `json:"gcp_project"`
	Repo       string                     `json:"repo,omitempty"`
	Workdir    string                     `json:"workdir,omitempty"`
	Changes    *infra.PlanCounts          `json:"changes,omitempty"`
	Applied    bool                       `json:"applied"`
	Outputs    *infra.InstallationOutputs `json:"outputs,omitempty"`
	Config     string                     `json:"config,omitempty"`
	Backup     string                     `json:"backup,omitempty"`
	Forgotten  bool                       `json:"forgotten,omitempty"`
	Deleted    []string                   `json:"deleted,omitempty"`
	Enabled    []string                   `json:"enabled,omitempty"`
	Created    string                     `json:"created_project,omitempty"`
	Undelete   string                     `json:"undelete,omitempty"`
	Builds     []string                   `json:"builds,omitempty"`
	Missing    []string                   `json:"missing,omitempty"`
	Warnings   []string                   `json:"warnings,omitempty"`
	// Retire is the retirement set the run predicted -- the summary's own
	// rows, with the state's and the labels' fields filled in -- so --json
	// carries the same thing the run printed (design §3.2). It is set by
	// every run that reads the state, --plan-only and --retire alike.
	Retire []infra.Retirement `json:"retire,omitempty"`
	// Stages and LeftForYou are the converge's account (initflow): each
	// stage's state, and what only the user can do.
	Stages     []initflow.StageResult `json:"stages,omitempty"`
	LeftForYou []initflow.Left        `json:"left_for_you"`
	// Failed is the stage the converge stopped at, with the one-line fix;
	// Note says why a run only planned.
	Failed *initflow.Failure `json:"failed,omitempty"`
	Note   string            `json:"note,omitempty"`
	// Error is why a run refused before any stage ran (missing inputs, bad
	// flags), so --json prints an object on every outcome.
	Error string `json:"error,omitempty"`
	// Firebase and RTDBURL are set by update --firebase-project.
	Firebase string `json:"firebase_project,omitempty"`
	RTDBURL  string `json:"rtdb_url,omitempty"`
	// Anchor is set by update --anchor.
	Anchor *anchorResult `json:"anchor,omitempty"`
}

func newInitRun(cmd *cobra.Command, o *initOptions) *initRun {
	r := &initRun{cmd: cmd, o: o, w: cmd.OutOrStdout(), in: bufio.NewReader(cmd.InOrStdin())}
	if o.asJSON {
		r.w = cmd.ErrOrStderr()
	}
	return r
}

func runInit(r *initRun) error {
	cmd, o := r.cmd, r.o
	defer func() { clear(r.appKeyMem) }()
	if o.checkApp {
		if err := refuseAppKeyReadInAgent(os.Getenv); err != nil {
			return err
		}
	}
	// --plan-only creates and changes nothing: --yes confirms nothing under
	// it, in any path (the converge, --firebase-project, --repo, the embedded repo
	// engine). What a plan needs made first takes the typed confirmation.
	if o.planOnly {
		o.yes = false
	}
	if err := o.check(); err != nil {
		return err
	}
	if o.coordinator {
		if err := r.refuseCoordinatorUnattended(); err != nil {
			return err
		}
	}
	r.warnTempXDG()
	if !o.printVars {
		// Refused before anything else: every later step talks to Google.
		if err := refuseHTTP2Debug(os.Getenv); err != nil {
			return err
		}
	}
	if o.anchor {
		// A local file's edit behind its own checks: no inputs to gather,
		// no Terraform, no publish.
		return r.runAnchor(cmd.Context())
	}
	if err := r.gatherInputs(cmd.Context()); err != nil {
		return err
	}
	lc, path, old, err := loadInitConfig(cmd.Context(), o)
	if err != nil {
		return err
	}
	r.setProject(lc)
	if err := checkFirebaseProject(o.firebase, lc.GCPProject); err != nil {
		return err
	}
	r.noteOpenProviders(lc)
	if o.publishConfig {
		if old == nil {
			return userErr("--publish-config publishes an existing project config, and there is none: run fugaro setup first")
		}
		if m := agentMarker(os.Getenv); m != "" {
			return userErr("--publish-config writes to the cloud: %s", initflow.AgentRefusal(m))
		}
		var why []string
		written, err := publishSharedWarn(cmd.Context(), lc, func(m string) { why = append(why, m); r.warn(m) })
		if err != nil {
			return remote(err)
		}
		if !written {
			// An explicit publish request that publishes nothing is a failure.
			reason := "it was skipped"
			if len(why) > 0 {
				reason = why[len(why)-1]
			} else if fakeEndpointsOnGS(lc, lc.BucketURL()) {
				reason = "the local config's endpoints are fakes, so the real bucket is not opened"
			}
			return userErr("nothing was published: %s", reason)
		}
		fmt.Fprintf(r.w, "published the shared config to %s/%s\n", lc.BucketURL(), infra.SharedConfigObject)
		return r.printResult()
	}
	spec, err := installOptions(o, lc)
	if err != nil {
		return err
	}
	if o.printVars {
		data, err := infra.InstallationVars(spec)
		if err != nil {
			return err
		}
		// The printed variables are the installation as it STANDS, so the keys
		// this spec never carries are carried forward from the workdir — the
		// same carry a real run makes (prepare). Without it, --print-vars on
		// an installation that has opted in (or that uses managed projects,
		// whose fence the spec never carries either) prints a document that
		// drops them, which is a reading of it the owner cannot trust. It is
		// read, never written: --print-vars changes nothing, and a missing or
		// unreadable workdir is "no key", not an error to print a warning
		// about (this path touches no cloud and no Terraform by design).
		if dir, derr := infra.InstallationWorkdir(os.Getenv, lc.GCPProject); derr == nil {
			if prev, perr := infra.ReadWorkdirVars(dir, "installation"); perr == nil {
				if data, err = infra.CarriedCredentialsService(data, prev); err != nil {
					return err
				}
				if data, err = infra.CarriedManagedDeployerScoped(data, prev); err != nil {
					return err
				}
			}
		}
		// Printed without a cloud call, so without discovery: stdout stays
		// the tfvars alone.
		fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+printVarsUngatedInstallation)
		if o.firebase != "" {
			if data, err = r.firebaseVars(data, spec, lc); err != nil {
				return err
			}
			fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+printVarsUngatedFirebase)
		}
		_, err = cmd.OutOrStdout().Write(data)
		return err
	}

	e := &initEngine{r: r, lc: lc, spec: spec, path: path, old: old}
	if !o.forget && !o.configOnly {
		// The default and --firebase-project modes: the converge loop over the
		// engines (init_stages.go), whose own outcome records the creation
		// fact before it prints the result (markCreated, called from
		// outcome), whatever the loop's outcome — which the run still
		// returns.
		return e.converge(cmd.Context())
	}

	// Both records of a managed installation are asked HERE, at the door,
	// before the setup below prepares the workdir — because that preparation is
	// a deletion: it rebuilds the tree afresh, and the first thing in the tree
	// it deletes is the installation's tfvars (Workdir.PrevVars is that same
	// read, taken by PrepareWorkdir itself). A refused run must change nothing
	// on disk, so the refusal may not be decided from anything the preparation
	// has already taken.
	//
	// The installation's own document is read first, since it is local and free
	// (installationFenceRecorded). The project's live creation fact needs the
	// clients and the Resource Manager readiness setup builds, so it is read
	// through setupCloud — the cloud half of setup, done once, whose work the
	// later setup reuses — and still before anything touches the workdir. The
	// fact is the record a document cannot outlive: label and marker are the
	// project's and the runs bucket's, and any later converge writes the key
	// back into the document from them.
	ctx := cmd.Context()
	if o.forget {
		recorded, err := installationFenceRecorded(lc)
		if err != nil {
			return err
		}
		if recorded {
			return managedForgetRefusal(spec.Project, holdsFenceRecorded, fmt.Sprintf("The installation's own tfvars say so — %s = true, which every rewrite carries forward", infra.ManagedDeployerScopedKey))
		}
		if err := e.setupCloud(ctx); err != nil {
			return err
		}
		marked, err := r.managedScope(ctx, e.c)
		if err != nil {
			return err
		}
		if marked {
			return managedForgetRefusal(spec.Project, holdsFlaggedManaged, fmt.Sprintf("The project's own creation fact says so — the %s label on %s, with the runs bucket's %s", managed.ProjectLabelKey, spec.Project, managed.MarkerObject))
		}
	}

	// 1. The environment, 2. the workdir, the clients and Cloud Resource
	// Manager.
	if err := e.setup(ctx); err != nil {
		return err
	}
	c, t, wd := e.c, e.t, e.wd
	if o.forget {
		err = r.forget(ctx, c, t, wd, spec)
	} else {
		err = r.configOnly(ctx, c, t, wd, lc, spec, path, old)
	}
	if err != nil {
		return err
	}
	return r.printResult()
}

// creation is the project this run created, or the one it resumed and whose
// mark the person confirmed by typing its ID (resumeConfirmed): the project
// markCreated marks. "" when it did neither.
func (r *initRun) creation() string {
	if r.res.Created != "" {
		return r.res.Created
	}
	if r.resumedCreation && r.resumeConfirmed {
		return r.gcpProject
	}
	return ""
}

// confirmResumedMark is the typed confirmation a resumed creation is marked
// managed behind. fugaro_created=true says an earlier run of fugaro setup
// created the project, but it is a label anyone with
// resourcemanager.projects.update can set, so it is never enough on its own:
// the person running setup types the project's ID at their own terminal, the
// bar fugaro managed flag keeps (canType: a real terminal, no --json, no
// --non-interactive, no coding agent's session; --yes never stands in for it).
// Where it cannot be asked, or is not typed, nothing is marked and the run
// says how to mark it.
func (r *initRun) confirmResumedMark() bool {
	id := r.gcpProject
	later := fmt.Sprintf("project %s carries %s=%s (an earlier run of fugaro setup created it) and this run finished its setup, but it is not marked as managed: marking it takes its ID typed at your own terminal. Rerun fugaro setup there, or run fugaro managed flag %s --budget N --budget-currency CCC",
		id, infra.CreatedLabelKey, infra.CreatedLabelValue, id)
	if !r.canType() {
		r.warn(later)
		return false
	}
	fmt.Fprintf(r.w, "⚠ CONFIRM: this MARKS project %s as Fugaro-managed (gs://fugaro-runs-%s/%s and the label %s=%s): its applies then stop asking for the typed confirmation, and the deterministic gate, the inventory and the owner's delete list stand in for it. It carries %s=%s, which says fugaro setup created it.\n",
		id, id, managed.MarkerObject, managed.ProjectLabelKey, managed.ProjectLabelValue, infra.CreatedLabelKey, infra.CreatedLabelValue)
	fmt.Fprintf(r.w, "Type %s to mark it: ", id)
	line, err := r.in.ReadString('\n')
	fmt.Fprintln(r.w)
	if (err != nil && !errors.Is(err, io.EOF)) || strings.TrimSpace(line) != id {
		r.warn(later)
		return false
	}
	return true
}

// markCreated records the creation fact in the runs bucket of a project
// this run created (or resumed: creation), and sets up the state bucket a managed apply keeps its
// state in: the object at the bucket's root that no credential a run
// holds can write (internal/infra/managed, design D56/D57; Task 0.1 of
// docs/plans/2026-10-16-managed-projects.md) and D77's dedicated
// fugaro-managed-tfstate-<project> (Task 1.5 (3) of the same plan). A later
// run reads the marker to know the project is Fugaro's own, so it is written
// by the run that knows the fact, on the operator's own credentials, and by
// nothing else: the project stage runs first and the runs bucket does not
// exist until the installation stage has applied, so this is the one place
// and the one moment either step can run from.
//
// The fact is the project this run created, and nothing else (D73: adding
// Firebase to an existing project is not creating one, so update --firebase-project
// writes nothing). It has two halves since Task 3.0 — the object above, and
// the fugaro_managed label on the GCP project itself — and they are written
// here together because they are one fact about one creation: the read
// requires both (managed.readFact), so a run that wrote one and not the other
// has marked nothing and says so rather than reporting a half-mark. This is
// the only writer of either, and it is on the operator-run creating path, so
// neither mark has a path from a coding agent's session or from a managed
// apply.
//
// The write itself refuses anything but that fact
// (managed.WriteMarker, managed.WriteManagedLabel); what is here is the rest: which bucket — the
// project's own, under the name the read of Task 0.2 looks in and no other —
// which project number, and what to say when it cannot be written.
//
// It runs at the end of the converge and never changes its outcome: the
// engine calls it from outcome, before the result is printed, so what it
// warns is in the account a --json consumer reads and not only on stderr. A run that
// created the project and then failed later still records the fact — the
// project exists, and the part of the work that succeeded must not be lost
// — and a write that fails is a warning, never a failure of the run: an
// unmarked project is the fail-closed direction (nothing is skipped for it,
// which is where a project that was not created in this run stays).
//
// The budget a managed fact records is never one the run failed to create:
// the Terraform budget is a resource of the installation root, and the fact
// is written only when r.res.Outputs names the runs bucket, which only
// writeConfig sets, after that root's apply succeeded. An apply the budget
// failed (a currency that is not the billing account's, say) leaves Outputs
// unset, so the project is left unmarked and the warning below says so
// (TestManagedFactIsNotWrittenWhenTheInstallationApplyFails). Later stages
// (images, secrets, the repository) do not carry the budget, so their
// failure does not take back a fact the installation apply made true. The
// figures themselves are checked before anything is created
// (checkCreateProject, managed.Budget.Check).
func (e *initEngine) markCreated(ctx context.Context) {
	r := e.r
	if c := r.o.created; c != nil {
		// projectExisted is the last read's answer, and the stage reads the
		// project again after creating it: it was there before this run only
		// if this run did not create it.
		c.Created, c.Existed = r.res.Created, r.projectExisted && r.res.Created == ""
	}
	// created is the project this converge marks, and it is one of two
	// things. Created is set by the project stage alone, inside its create
	// branch (projectStage.Apply in init_project.go). The other is a resumed
	// creation (the owner's ruling of 2026-10-23: a setup resumed by a later
	// run is managed by default): the project stage read a project carrying
	// fugaro_created=true, the label the creating run's create request writes
	// (infra.CreateProject). That label is forgeable by anyone who may update
	// the project, so it only says which project to ask about: the mark is
	// written after its ID is typed at the person's own terminal
	// (confirmResumedMark), and never under --yes, --non-interactive, --json or
	// in a coding agent's session. It is asked only once the installation's
	// runs bucket is known, since that is where the mark goes. "" means this
	// run neither created nor confirmed a resumed project, and then there is no
	// fact to record: every ordinary run of an existing installation —
	// fugaro's own, belong, the owner's — comes through here with it empty.
	if r.res.Created == "" && r.resumedCreation && !r.resumeConfirmed && e.c != nil && r.res.Outputs != nil && r.res.Outputs.RunsBucket != "" {
		r.resumeConfirmed = r.confirmResumedMark()
	}
	created := r.creation()
	if created == "" {
		// --managed asked for a managed creation and this run created no
		// project, so there is no fact to record — and the owner must not
		// silently get an unmarked project where they asked for a managed
		// one.
		if r.o.managed {
			r.warn(fmt.Sprintf("--managed was not applied: this run created no project (it stopped before creating one, or the creation did not finish), so no creation fact was written and %s is not managed", r.gcpProject))
		}
		return
	}
	if e.c == nil {
		r.warn(fmt.Sprintf("project %s is not marked as managed yet: this run stopped before its installation was set up (at the billing step, or earlier), and the mark goes in the installation's runs bucket. It is marked automatically when you rerun fugaro setup once billing is linked", created))
		return
	}
	if created != r.gcpProject {
		r.warn(fmt.Sprintf("this run created project %s but installs into %s: neither is marked as managed", created, r.gcpProject))
		return
	}
	bucket := ""
	if r.res.Outputs != nil {
		bucket = r.res.Outputs.RunsBucket
	}
	if bucket == "" {
		r.warn(fmt.Sprintf("project %s was created but the runs bucket it would be marked in is not known in this run (the installation did not finish): it is not marked, so nothing is skipped for it until it is", created))
		return
	}
	// The bucket must be the project's own, under the default name, for the
	// same reason init's owner() refuses any other (initEngine.owner in init_review.go): a
	// custom --runs-bucket would take the marker where the read of Task 0.2
	// does not look — owner() reads fugaro-runs-<project>, and Task 0.2's
	// IsManaged locates the bucket the same way. A marker in a custom bucket
	// is the fail-closed direction and still a silently unmanaged project,
	// so this writes only into the name the read knows and says which name
	// it found instead.
	if want := "fugaro-runs-" + created; bucket != want {
		r.warn(fmt.Sprintf("project %s was created but its runs bucket is %s, not %s: it is not marked as managed, because the marker is only read from the default bucket name", created, bucket, want))
		return
	}
	// The number is what makes the marker about this project and not about a
	// bucket of the same name in someone else's project (managed.WriteMarker
	// checks the bucket against it).
	// The amount a managed creation was required to choose, and nothing else:
	// --managed is the one flag that says this creation is managed, and its
	// own check refuses a run that passed it without the trio
	// (init_project.go), so a non-nil budget here is one this run was given
	// and the installation's Terraform budget carries the same figures. A
	// creation that is not managed passes nil and writes the object it
	// always wrote, byte for byte (Task 4.2: the change reaches nobody else).
	var budget *managed.Budget
	if r.o.managed {
		budget = &managed.Budget{Amount: r.o.budget, Currency: r.o.budgetCurrency}
	}
	number, err := infra.ProjectNumber(ctx, e.c, created)
	if err == nil {
		err = managed.WriteMarker(ctx, e.c.Storage, bucket, created, r.gcpProject, number, budget)
	}
	fact := err == nil
	// The first half is on disk from here on, and a caller that acts on what
	// this run did (fugaro restore) has to tell it from the whole mark: the
	// object without the label is a project the read answers "not managed"
	// for, and it is a project an owner-run label away from being complete.
	if fact {
		if c := r.o.created; c != nil {
			c.Fact = true
		}
	}
	// The other half of the same fact, in the same step and on the same
	// values: the fugaro_managed label on the project itself (Task 3.0).
	// Both or neither — a project with the object and no label is not
	// managed to the read (managed.readFact requires both), so a run that
	// wrote one and not the other has not marked the project and must not
	// say it did. The two are one operator-run step, which is the whole
	// reason they are here rather than spread across two: the writers of
	// either are this run and the owner's own `fugaro managed flag` (Task
	// 6.1), both on the operator's own credentials before a person, and
	// neither a coding agent's session (which cannot create a project at
	// all — projectStage.Apply needs a terminal and a typed confirmation)
	// nor `fugaro managed apply`, which only reads, reaches one.
	//
	// The label is written for every managed creation, as it has been since
	// the mark had two halves: while managed projects were off
	// (managedDeployerShipped, false until 0.10.0) this step was skipped, and
	// 0.10.0 is the release that made a project this run created read as
	// managed again.
	if err == nil {
		err = managed.WriteManagedLabel(ctx, e.c.CRM, created, r.gcpProject)
	}
	marked := err == nil
	switch {
	case marked:
		if c := r.o.created; c != nil {
			c.Marked = true
		}
		how := "this run created"
		if r.res.Created == "" {
			how = "this run resumed the creation of"
		}
		fmt.Fprintf(r.w, "recorded that %s project %s: it is Fugaro-managed (gs://%s/%s, labelled %s=%s)\n", how, created, bucket, managed.MarkerObject, managed.ProjectLabelKey, managed.ProjectLabelValue)
	case fact:
		// The half-written mark: the object landed and the label did not.
		// What completes it is an owner's own command, and which one depends
		// on what the object records — the split restore's own refusal draws
		// (restore.go): the flag replaces a fact that records no amount and
		// writes both halves in one command, while the fact a --managed
		// creation writes already records one, no command of ours rewrites a
		// fact that does, and there the label goes on the project itself. The
		// warning this replaced gave both halves to the creating run alone
		// and offered the flag for both states, which for the second refuses.
		r.warn(fmt.Sprintf("project %s was created but not marked as managed (%v): gs://%s/%s holds the creation fact and the project does not carry the label %s=%s, and the read requires both, so nothing is skipped for it. What completes the mark is the owner's own command at their terminal, and which one depends on what the object records: `fugaro managed flag %s --budget N --budget-currency CCC` replaces a creation fact that records no amount, writing the amount and the label in the same command, while a fact that already records one is replaced by no command of ours — for it the label goes on the project itself (gcloud projects update %s --update-labels %s=%s), the backfill docs/design/managed-projects.md carries", created, err, bucket, managed.MarkerObject, managed.ProjectLabelKey, managed.ProjectLabelValue, created, created, managed.ProjectLabelKey, managed.ProjectLabelValue))
	default:
		// Nothing of the fact is there, so the read answers "not managed"
		// for a project no half of it describes. The flag is still the way
		// out: it writes the object where none exists as readily as it
		// replaces the version-1 one (managed.writeFlag), on the owner's own
		// credentials at their terminal, and a later run of ours cannot —
		// this function returns above for every run that creates no project.
		r.warn(fmt.Sprintf("project %s was created but not marked as managed (%v): gs://%s/%s was not written and the project carries no %s=%s label, and the read requires both, so nothing is skipped for it. No later run of ours writes either half — a run that creates no project records nothing — and the mark is completed by the owner's own command at their terminal: `fugaro managed flag %s --budget N --budget-currency CCC` writes the creation fact and the label in the same command", created, err, bucket, managed.MarkerObject, managed.ProjectLabelKey, managed.ProjectLabelValue, created))
	}

	// D77's state bucket, the other half of what a managed project needs
	// before an apply can run under the deployer: the state of the
	// agent-authored root lives in a bucket of its own, and the deployer's
	// read and write are bound there and the operators' read beside them
	// (internal/managed/statebucket.go: OperatorStateRole, without which the
	// apply's inventory read is denied for every operator). It
	// belongs here for the same reason the marker does: it is setup on the
	// operator's own credentials, by the run that creates the project, and no
	// other command in the tree writes it. "The creation" here is every
	// project this run creates and not the --managed one alone — the step is
	// not gated on the flag, for the reason the paragraph below gives for the
	// mark. The region is the installation's, so the managed state sits where
	// the rest of the project's storage does. Like the marker, this never changes
	// the outcome of the run — a project that is marked but has no state
	// bucket is the fail-closed direction (a managed apply finds nowhere to
	// keep its state and says so, rather than writing it somewhere else).
	//
	// It is not gated on the mark above, and a run whose label write failed
	// reaches it: this step is what an apply under the deployer needs, this
	// function is its one writer in the tree, and no later run gets here — a
	// run that creates no project returns before it. Skipping it because the
	// marks are half-written would take with it the one step nothing later can
	// take, on the very paths the warning above sends a person to, which end
	// in a managed apply.
	what := "was created"
	if marked {
		what = "was created and marked as Fugaro-managed"
	}
	if err := managedmode.EnsureManagedStateBucket(ctx, managedmode.InfraStore{Clients: e.c}, created, e.spec.Region, e.spec.Operators); err != nil {
		r.warn(fmt.Sprintf("project %s %s, but its managed state bucket was not set up (%v): a managed apply has nowhere to keep its state until an owner-run command sets it up", created, what, err))
		return
	}
	// The operators are named in the sentence as well as bound: the read the
	// binding exists for is theirs (the whole-project inventory, Task 4.5),
	// and a run that bound them silently would leave the operator who later
	// sees the fallback with nothing to trace it to.
	fmt.Fprintf(r.w, "set up the managed state bucket gs://%s and bound the deployer (%s) and the operators (%s) to it\n",
		managedmode.ManagedStateBucketName(created), managedmode.DeployerStateRole, managedmode.OperatorStateRole)
}

// checkEnv refuses an environment that would point Terraform or the
// Google tools elsewhere (preflight.Environment, shared with fugaro
// doctor), and finds terraform. It prints the local config's warnings once
// it passes.
func (r *initRun) checkEnv(lc *localcfg.Config) (string, error) {
	for _, c := range preflight.Environment(os.Getenv, lc.GCPProject) {
		if !c.OK {
			return "", userErr("%s; %s", c.Problem, c.Fix)
		}
	}
	bin, err := exec.LookPath("terraform")
	if err != nil {
		return "", userErr("%s needs terraform (>= 1.9, < 2) on PATH: %v", r.o.doorProgram(), err)
	}
	for _, w := range lc.Warnings() {
		r.warn(w)
	}
	return bin, nil
}

// terraform prepares the workdir dir for root and a terraform that runs
// there, with the allowlisted environment.
func (r *initRun) terraform(bin, dir, root string) (*infra.Workdir, *tf.TF, error) {
	wd, err := infra.PrepareWorkdir(dir, root)
	if err != nil {
		return nil, nil, userErr("the Terraform workdir: %v", err)
	}
	cache, err := infra.PluginCache(os.Getenv)
	if err != nil {
		return nil, nil, userErr("%v", err)
	}
	env, err := tf.Env(os.Environ(), wd.Dir, cache)
	if err != nil {
		return nil, nil, userErr("%v", err)
	}
	t, err := tf.New(bin, wd.Root, env)
	if err != nil {
		var ee *tf.ExitError
		if errors.As(err, &ee) {
			return nil, nil, remote(err)
		}
		return nil, nil, userErr("%v", err)
	}
	t.Out = r.cmd.ErrOrStderr()
	return wd, t, nil
}

// gcpOptions are the Google API options of lc's project and endpoints.
func gcpOptions(lc *localcfg.Config) gcp.Options {
	return gcp.Options{GCPProject: lc.GCPProject, Region: lc.Region, Endpoints: gcp.Endpoints{
		Run: lc.Endpoints.Run, Logging: lc.Endpoints.Logging, SecretManager: lc.Endpoints.SecretManager, CloudBuild: lc.Endpoints.CloudBuild,
		NoAuth: lc.Endpoints.NoAuth}}
}

// quotaOptions are gcpOptions with quota as the project the calls are
// billed to.
func quotaOptions(lc *localcfg.Config, quota string) gcp.Options {
	o := gcpOptions(lc)
	o.GCPProject = quota
	return o
}

func newInitClients(ctx context.Context, lc *localcfg.Config) (*infra.Clients, error) {
	return newInitClientsFor(ctx, lc, lc.GCPProject)
}

// newInitClientsFor is newInitClients with quota as the quota project of
// the calls.
func newInitClientsFor(ctx context.Context, lc *localcfg.Config, quota string) (*infra.Clients, error) {
	c, err := infra.NewClients(ctx, quotaOptions(lc, quota),
		infra.Endpoints{IAM: lc.Endpoints.IAM, ArtifactRegistry: lc.Endpoints.ArtifactRegistry,
			Storage: lc.Endpoints.Storage, ResourceManager: lc.Endpoints.ResourceManager, Scheduler: lc.Endpoints.CloudScheduler,
			ServiceUsage: lc.Endpoints.ServiceUsage, Billing: lc.Endpoints.CloudBilling, FirebaseDatabase: lc.Endpoints.FirebaseDatabase, APIKeys: lc.Endpoints.APIKeys})
	if err != nil {
		return nil, remote(err)
	}
	return c, nil
}

func (o *initOptions) registryCleanupSet() bool { return o.registryCleanup != "" }

// checkAppID validates --github-app-id where it is given, not only where it
// is used.
func (o *initOptions) checkAppID() error {
	if o.githubAppID != "" && !appIDRE.MatchString(o.githubAppID) {
		return userErr("--github-app-id %q is not a GitHub App ID (1 to 20 digits)", o.githubAppID)
	}
	return nil
}

// checkOnboardRepo validates --onboard-repo. It opts a repository the project
// does not list in to cloud changes, so a coding agent's session never passes
// it: whoever drives from there gets the typed route instead.
func (o *initOptions) checkOnboardRepo() error {
	if o.onboardRepo == "" {
		return nil
	}
	if m := agentMarker(os.Getenv); m != "" {
		return userErr("--onboard-repo opts a repository in to cloud changes and is not accepted in a coding agent's session (%s is set): %s, and type the repository's owner/name at the prompt", m, initflow.AgentAdvice)
	}
	if _, err := task.CanonicalRepo(o.onboardRepo); err != nil {
		return userErr("--onboard-repo %q is not owner/name", o.onboardRepo)
	}
	return nil
}

// firebaseProject is the id of the Firebase project this run works in: the one
// --firebase-project names, and the run's own --gcp-project when the flag names
// a separate one — or, the identity case, the same id again (D7, C2: "one id
// for Google Cloud and Firebase by default", so --firebase-project is not a
// second value at all unless it says so). That is what makes
// checkCreateProject's pairing rule (a created project is its own Firebase
// project) the ordinary path rather than a special case.
//
// It is the *id*, not an ask. Whether the budget backend is in the run at all
// is the flag's own presence (init_stages.go's newFirebaseStage and
// newInstallation2Stage, init_review.go's firebaseVerified), which is why the
// default is not a way to request the backend without naming a project: the
// flag takes a value, and a run that wants the backend names the project it
// goes in — its own, or a separate one. An empty `--firebase-project=` is
// pflag's empty value, which reads as no flag at all, deliberately: a script
// whose variable is unset names no project, and three applies against the
// installation's own project (one of them a Firestore database with a
// permanent location) must not be what an empty string buys.
//
// Every site that resolves the id — the flag's validation here, the pairing
// rule, the Firebase engine and the commands it prints, the review screen —
// reads it from here, so the one default has one definition.
func (o *initOptions) firebaseProject() string {
	return cmp.Or(o.firebase, o.cloud.gcpProject)
}

// checkFirebaseProject refuses a Firebase project other than the
// installation's own GCP project. Fugaro only starts fresh (the owner's ruling
// of 2026-10-23): the budget backend goes in the project fugaro setup created
// for the installation and nowhere else, so --firebase-project, which asks for
// the backend, names that project or is refused before anything is read.
func checkFirebaseProject(fp, gcpProject string) error {
	if fp == "" || gcpProject == "" || fp == gcpProject {
		return nil
	}
	return userErr("--firebase-project %s is not the installation's GCP project %s: Fugaro builds the budget backend only in the project fugaro setup created for the installation, and uses no Firebase project of its own; pass --firebase-project %s", fp, gcpProject, gcpProject)
}

// check refuses flag combinations that mean nothing.
//
// Two features were once refused here before any cloud call, and neither is
// any more: --managed until 0.10.0 (managedDeployerShipped) and
// --provision-batch until 0.9.5, when the compute module's reaper role stopped
// naming a permission Google does not have (batchProvisionShipped). The flags
// themselves stay: they still select what they always selected.
func (o *initOptions) check() error {
	if o.noBuild || o.allowJobDelete || o.retire {
		// The door, not `update`: check() runs for both doors, and neither flag
		// set is foreign to either of them -- setupDoor.foreign is --forget
		// alone. What these three want is the --repo converge, which both doors
		// take and which never reaches check() at all (RunE's `case o.repo`
		// hands it to runInitRepo). A first-run user reading "fugaro update
		// --repo" would be sent to the other door for a command their own would
		// have run.
		return userErr("--no-build, --allow-job-delete and --retire are for %s --repo", o.doorProgram())
	}
	if err := o.checkAppID(); err != nil {
		return err
	}
	if err := o.checkOnboardRepo(); err != nil {
		return err
	}
	if o.onboardRepo != "" {
		if o.forget || o.configOnly || o.printVars {
			return userErr("--onboard-repo opts the checkout's repository in to the converge; it excludes --forget, --config-only and --print-vars")
		}
	}
	if o.githubAppID != "" && (o.forget || o.configOnly || o.printVars) {
		return userErr("--github-app-id is for the repository stage of the converge (or --repo); it has no use with --forget, --config-only and --print-vars")
	}
	n := 0
	for _, b := range []bool{o.planOnly, o.printVars, o.configOnly, o.forget, o.publishConfig, o.anchor} {
		if b {
			n++
		}
	}
	if n > 1 {
		return userErr("--plan-only, --print-vars, --config-only, --forget, --publish-config and --anchor exclude one another")
	}
	if o.anchor {
		set := slices.DeleteFunc(o.installationFlags(), func(f string) bool { return f == "--anchor" })
		for _, f := range []struct {
			name string
			on   bool
		}{{"--onboard-repo", o.onboardRepo != ""}, {"--github-app-id", o.githubAppID != ""}, {"--allow-delete", len(o.allowDelete) > 0},
			{"--allow-fork", o.allowFork}, {"--check-github-app", o.checkApp}} {
			if f.on {
				set = append(set, f.name)
			}
		}
		if len(set) > 0 {
			return userErr("--anchor only writes the gcp_project line of an onboarded repository's checkout; it excludes %s", strings.Join(set, ", "))
		}
	}
	if o.provisionBatch || o.provisionCredentials {
		// Both are applies of the installation's own root, each confirmed by
		// typing the project's name, so a flag that only reads or only stops
		// has no room beside them: a run that named one and printed vars would
		// look like it had provisioned something.
		var set []string
		for _, f := range []struct {
			name string
			on   bool
		}{{"--repo", o.repo}, {"--forget", o.forget}, {"--print-vars", o.printVars},
			{"--config-only", o.configOnly}, {"--publish-config", o.publishConfig}, {"--anchor", o.anchor}} {
			if f.on {
				set = append(set, f.name)
			}
		}
		if len(set) > 0 {
			flags, what := "--provision-batch", "provisions the installation's Batch compute backend"
			if o.provisionCredentials {
				flags, what = "--provision-credentials", "turns the installation's credentials service on"
				if o.provisionBatch {
					flags = "--provision-batch and --provision-credentials"
					what = "provision the installation's Batch compute backend and turn its credentials service on"
				}
			}
			return userErr("%s %s, which is an apply: it excludes %s", flags, what, strings.Join(set, ", "))
		}
	}
	if o.coordinator {
		if err := o.checkCoordinator(); err != nil {
			return err
		}
	}
	if o.forget && len(o.allowDelete) > 0 {
		return userErr("--forget allows exactly the log isolation's deletes; it takes no --allow-delete")
	}
	for _, v := range o.baseImages {
		if _, _, err := localcfg.ParseBaseImageFlag(v); err != nil {
			return userErr("%v", err)
		}
	}
	for _, k := range o.baseKinds {
		if !slices.Contains(config.Bases, k) {
			if slices.Contains(config.LegacyBaseKinds, k) {
				return userErr("--base %q is not a base kind (%s): %s was removed in 0.7.0 (%s)", k, strings.Join(config.Bases, ", "), k, config.MigrationDoc)
			}
			return userErr("--base %q is not a base kind (%s)", k, strings.Join(config.Bases, ", "))
		}
	}
	if _, err := parseExpectDigests(o.expectDigests); err != nil {
		return userErr("%v", err)
	}
	for _, k := range o.replaceImages {
		if k != "history" && !slices.Contains(config.Bases, k) {
			return userErr("--replace-image %q is not an image kind (history, %s)", k, strings.Join(config.Bases, ", "))
		}
	}
	if o.imageSource != "" {
		if err := mirror.ValidSourcePrefix(o.imageSource); err != nil {
			return userErr("--image-source: %v", err)
		}
	}
	// The creation's own flags are checked here only where --create-project
	// says this run creates: whether a run without it creates (a first run of
	// fugaro setup does) is known once its project config was looked for, and
	// the wizard's settleCreation checks them there, either way.
	if o.createProject {
		if err := o.checkCreateProject(); err != nil {
			return err
		}
	}
	if o.firebase == "" && (o.budgetMode != "" || o.budgetAdminsChanged) {
		// Door-relative for the same reason as the --no-build refusal above:
		// --firebase-project is both doors' flag (init_firebase.go's prompts
		// already name the reader's door), so a setup user who passed
		// --budget-mode is told to add it to the run they are in.
		return userErr("--budget-mode and --budget-admin are for %s --firebase-project <gcp-project-id>", o.doorProgram())
	}
	// fp is the Firebase project the run works in: --firebase-project when it
	// is given, the run's own --gcp-project otherwise (firebaseProject, D7).
	if fp := o.firebaseProject(); o.firebase != "" {
		switch {
		case o.forget || o.configOnly:
			return userErr("--firebase-project builds the budget backend; it excludes --forget and --config-only")
		case !infra.ValidProjectID(fp):
			return userErr("--firebase-project %q is not a project ID (6 to 30 of a-z, 0-9 and '-', starting with a letter)", fp)
		case checkFirebaseProject(fp, o.cloud.gcpProject) != nil:
			return checkFirebaseProject(fp, o.cloud.gcpProject)
		case o.budgetMode != "" && !slices.Contains([]string{"off", "observe", "enforce"}, o.budgetMode):
			return userErr("--budget-mode %q must be off, observe or enforce", o.budgetMode)
		}
	}
	if (o.budget != 0 || o.budgetCurrency != "" || o.billingAccount != "") && (o.budget <= 0 || o.budgetCurrency == "" || o.billingAccount == "") {
		return userErr("a budget needs --budget (more than 0), --budget-currency and --billing-account together")
	}
	return nil
}

// setProject records lc's project: its GCP ID for the Google calls, its
// name for the account and the confirmations.
func (r *initRun) setProject(lc *localcfg.Config) {
	r.gcpProject, r.projectName = lc.GCPProject, lc.Name
	r.res.Project, r.res.GCPProject = lc.Name, lc.GCPProject
}

// loadInitConfig selects the project config (localcfg.Select, as the
// converge does: a missing one is to be created) with --gcp-project, --region and
// --runs-bucket applied; without one, it starts a new one,
// projects/<name>.yaml, from --name (else the name that selected it, such
// as the checkout's project:), --gcp-project and --region. A --name that
// is not an existing project's name (even beside one config) is such a first
// run; the selected config's own name is accepted, and a selector that already
// named another project (checkout, --config) refuses a different --name. old is the file's content (nil when there is
// none).
func loadInitConfig(ctx context.Context, o *initOptions) (lc *localcfg.Config, path string, old []byte, err error) {
	co, err := checkoutProject(ctx, "")
	if err != nil {
		return nil, "", nil, err
	}
	sel, lc, err := selectNamed(o.cloud, co, true, o.name)
	if err != nil {
		return nil, "", nil, err
	}
	if err := refuseSharedWrite(sel); err != nil {
		return nil, "", nil, err
	}
	if lc != nil {
		// A config to be created is announced once it exists, below.
		if err := announce(o.cloud, sel, lc); err != nil {
			return nil, "", nil, err
		}
	}
	name := sel.Name
	if o.name != "" && name != "" && o.name != name {
		switch sel.From {
		case "checkout":
			return nil, "", nil, userErr("this checkout belongs to project %s; --name says %s", name, o.name)
		}
		return nil, "", nil, userErr("%s selects project %s; --name says %s (renaming a project isn't supported)", sel.From, name, o.name)
	}
	if lc != nil {
		path = sel.Path
		if old, err = os.ReadFile(path); err != nil {
			return nil, "", nil, userErr("reading the project config: %v", err)
		}
	} else {
		name = cmp.Or(name, o.name)
		if name == "" && o.configOnly {
			// --config-only takes the name from the installation's
			// outputs (nameFromOutputs), so the config starts without one.
			if o.cloud.gcpProject == "" || o.cloud.region == "" {
				return nil, "", nil, userErr("there is no project config yet: pass --gcp-project and --region (the project's name comes from the installation), or --name")
			}
			lc, err = newProjectConfig("pending", o.cloud.gcpProject, o.cloud.region)
			if err != nil {
				return nil, "", nil, userErr("--gcp-project/--region: %v", err)
			}
			lc.Name = ""
			if o.runsBucket != "" {
				lc.RunsBucket = o.runsBucket
			}
			return lc, "", nil, nil
		}
		switch {
		case name == "":
			return nil, "", nil, userErr("there is no project config yet: pass --name <name> (the project's name), --gcp-project and --region to create one")
		case !config.ProjectNameRE.MatchString(name):
			return nil, "", nil, userErr("--name %q is not a project name (1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit)", name)
		case o.cloud.gcpProject == "" || o.cloud.region == "":
			return nil, "", nil, userErr("there is no project config for %s yet: pass --gcp-project and --region to create one", name)
		}
		if path = sel.Path; path == "" {
			if path, err = localcfg.ProjectPath(os.Getenv, name); err != nil {
				return nil, "", nil, userErr("%v", err)
			}
		}
		lc, err = newProjectConfig(name, o.cloud.gcpProject, o.cloud.region)
		if err != nil {
			return nil, "", nil, userErr("--name/--gcp-project/--region: %v", err)
		}
		if err := announce(o.cloud, sel, lc); err != nil {
			return nil, "", nil, err
		}
	}
	if o.runsBucket != "" {
		lc.RunsBucket = o.runsBucket
		if lc.Bucket != "" && lc.Bucket != "gs://"+o.runsBucket {
			return nil, "", nil, userErr("--runs-bucket %s is not the local config's bucket_url %s", o.runsBucket, lc.Bucket)
		}
	}
	return lc, path, old, nil
}

// newProjectConfig is the first project config of a name: built as a
// value, so nothing a flag holds can become YAML syntax, and then run
// through the same parse and validation as a file.
func newProjectConfig(name, gcpProject, region string) (*localcfg.Config, error) {
	data, err := (&localcfg.Config{
		Version: 1, Name: name, GCPProject: gcpProject, Region: region, RunsBucket: "fugaro-runs-" + gcpProject,
	}).Marshal()
	if err != nil {
		return nil, err
	}
	return localcfg.Parse(data)
}

// installOptions is the installation's spec from the local config and the
// flags. Discovery sets whether the legacy registry is adopted.
func installOptions(o *initOptions, lc *localcfg.Config) (infra.InstallationSpec, error) {
	opts := infra.InstallOptions{
		StateBucket:     o.stateBucket,
		AlertEmail:      o.alertEmail,
		RegistryCleanup: cmp.Or(o.registryCleanup, lc.Terraform.RegistryCleanup),
		NoLogIsolation:  o.noLogIsolation || lc.Terraform.NoLogIsolation,
	}
	if o.launchersChanged {
		opts.Launchers = append([]string{}, o.launchers...)
	}
	if o.operatorsChanged {
		opts.Operators = append([]string{}, o.operators...)
	}
	// The installation carries the history account (and, once it can, the
	// sweeper's job) from the first --firebase-project on, and a plain fugaro update
	// keeps them while the config records the Firebase project.
	opts.BudgetBackend = o.firebase != "" || lc.Terraform.BudgetBackend || (lc.Budget != nil && lc.Budget.FirebaseProject != "")
	// The coordinator is the owner's opt-in and nothing else's: this run's
	// --coordinator, or a local config that recorded an earlier one.
	opts.Coordinator = o.coordinator || lc.Terraform.Coordinator
	if o.budget > 0 {
		opts.Budget = &infra.Budget{BillingAccount: o.billingAccount, Amount: o.budget, CurrencyCode: o.budgetCurrency}
	}
	spec, err := infra.Installation(lc, opts)
	if err != nil {
		return infra.InstallationSpec{}, initErr(err)
	}
	if o.managed {
		// The managed creation's one rule about the state bucket, checked on
		// the RESOLVED name: the flag and the project config are both inputs to
		// it (infra.Installation's firstOf), so the check is here rather than on
		// --state-bucket alone.
		if err := checkManagedStateBucket(spec); err != nil {
			return infra.InstallationSpec{}, err
		}
	}
	return spec, nil
}

// checkManagedStateBucket refuses a managed creation whose installation state
// would live anywhere but gs://fugaro-tfstate-<gcp-project>.
//
// Why a managed creation may not choose its state bucket (Task 4.5, design
// D59): the managed apply reads what the installation's own Terraform accounts
// for out of that bucket BY ITS DERIVED NAME — managedAccounted lists
// fugaro-tfstate-<gcp_project> and D77's fugaro-managed-tfstate-<gcp_project>,
// and neither is taken from a flag or from the project's config, because a
// bucket a caller could name is a bucket this program would believe instead of
// the project's own. The whole-project inventory compares that read against
// what the project holds, and a state it cannot read is CauseStateUnread: every
// managed apply of the project would fall back to the typed confirmation it
// was created to be free of, and an agent's run would stop there rather than
// apply. So a managed project whose state sits in a bucket the read does not
// name is a project whose managed case never happens, and the run that would
// create one is refused here, before anything is created, rather than left to
// be discovered by an apply that always asks.
//
// The refusal covers both spellings of the same decision — --state-bucket on
// the command line and terraform.state_bucket in the project's config — because
// it reads the resolved spec, which is the value the creation would actually
// use, and it says so: the fix is where the value comes from, and not
// "create the project unmanaged instead". An unmanaged creation resolves
// terraform.state_bucket from the same config, so its state would sit in the
// same bucket, and the managed flag of it is refused for it (managedFlag's own
// check, added with this one) — a run told to take the long way round would
// arrive at the same refusal one command later, with a project created in the
// meantime. It is the rule the neighbouring managed paths keep: `fugaro managed
// flag` refuses a project whose runs bucket or whose state bucket is not the
// derived name, and `fugaro restore` refuses to restore into a state bucket
// that is not this project's own.
func checkManagedStateBucket(spec infra.InstallationSpec) error {
	want := managedmode.InstallationsStateBucket(spec.Project)
	if spec.StateBucket == want {
		return nil
	}
	return userErr("a managed project's Terraform state must live in gs://%s, and this creation would put it in gs://%s: the managed apply reads the installation's state out of that bucket by its derived name — the name is a function of the GCP project and is never taken from a flag or from the project's config — so state kept anywhere else reads as state that could not be read at all, and every managed apply of %s would fall back to the typed confirmation the project was created to be free of. Fix it where the value comes from: drop --state-bucket, and terraform.state_bucket from the project config, which is what a creation that passes no flag resolves (fugaro managed flag refuses a project whose config records another bucket, for the same reason)",
		want, spec.StateBucket, spec.FugaroProject)
}

// initErr maps an error of infra: a *infra.UserError (a refusal, an
// ownership check) is exit 1, anything else (an API or Terraform failure)
// exit 2.
func initErr(err error) error {
	var ee *ExitError
	var ue *infra.UserError
	switch {
	case err == nil || errors.As(err, &ee):
		return err
	case errors.As(err, &ue):
		return &ExitError{Code: ExitUserError, Err: err}
	}
	return remote(err)
}

func (r *initRun) warn(msg string) {
	r.res.Warnings = append(r.res.Warnings, msg)
	fmt.Fprintln(r.w, "warning: "+msg)
}

// canConfirmTyped is initflow.CanConfirm, the one rule of who may give a
// money or permanent confirmation. A package variable only so a test that is
// about something else can type through --yes: the tests of the rule itself
// (the matrix) use the real one.
var canConfirmTyped = initflow.CanConfirm

// conditions is what this run says about who is there.
func (r *initRun) conditions() initflow.Conditions {
	o := r.o
	return initflow.Conditions{Yes: o.yes && !o.planOnly, NonInteractive: o.nonInteractive, JSON: o.asJSON,
		Terminal: r.cmd != nil && stdinIsTerminal(r.cmd.InOrStdin()), Agent: agentMarker(os.Getenv)}
}

// confirm shows the ⚠ CONFIRM banner for what, and returns nil once it is
// confirmed: by --yes, or by the project's name typed at a terminal. Without a
// terminal and without --yes it refuses; undone says what that leaves. A
// coding agent's session confirms nothing, --yes included.
func (r *initRun) confirm(what, undone string) error {
	ok, err := r.ask(what)
	if err != nil {
		return err
	}
	if !ok {
		return r.notConfirmed(undone)
	}
	return nil
}

// notConfirmed is the refusal of a step that was not confirmed.
func (r *initRun) notConfirmed(undone string) error {
	if r.o.nonInteractive || !stdinIsTerminal(r.cmd.InOrStdin()) {
		return userErr("%s%s; %s", initflow.NeedsTerminal("the typed confirmation of this step"), r.yesRoute(), undone)
	}
	return userErr("not confirmed (the project's name was not typed); %s", undone)
}

// yesRoute is what follows "needs a real terminal" when a step cannot be
// confirmed: --yes, except under --plan-only, which never takes it.
func (r *initRun) yesRoute() string {
	if r.o.planOnly {
		return " (--plan-only never takes --yes: it creates nothing)"
	}
	return ", or pass --yes once you have read what it does"
}

// ask shows the banner and reports whether the step is confirmed; without
// a terminal and without --yes it isn't. In a coding agent's session it is
// an *initflow.AgentError before anything is shown as confirmed: nothing a
// run applies is confirmed there.
func (r *initRun) ask(what string) (bool, error) {
	if m := agentMarker(os.Getenv); m != "" {
		return false, &initflow.AgentError{Marker: m}
	}
	fmt.Fprintf(r.w, "⚠ CONFIRM (project %s, GCP project %s): %s\n", r.projectName, r.gcpProject, what)
	if r.o.yes && !r.o.planOnly {
		fmt.Fprintln(r.w, "  confirmed by --yes")
		return true, nil
	}
	if r.o.nonInteractive || !stdinIsTerminal(r.cmd.InOrStdin()) {
		return false, nil
	}
	return r.typed("Type " + r.projectName + " to apply to GCP project " + r.gcpProject + ": ")
}

// typed reads one line at the terminal after prompt and reports whether it is
// the project's name.
func (r *initRun) typed(prompt string) (bool, error) {
	fmt.Fprint(r.w, prompt)
	// A blank line is never an answer, but a stray newline is common right
	// after a hidden paste (a pasted secret plus Enter leaves one in the
	// terminal's input): skip up to three of them instead of failing a
	// confirmation the person is typing correctly. Only the exact project
	// name confirms.
	for blanks := 0; ; blanks++ {
		line, err := r.in.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return false, userErr("reading the confirmation: %v", err)
		}
		if answer := strings.TrimSpace(line); answer != "" || err != nil || blanks >= 3 {
			return answer == r.projectName, nil
		}
	}
}

// stepClass is the confirmation class of the one money-or-permanent step this
// door decides differently: the Firestore database's location. It is
// initflow.Typed in `setup`, which asks for it as a first-time question, and
// initflow.YesOrTyped in `update`, which meets it as a step of a stage it owns
// — the owner's answer of 2026-10-20 (reconciliation §5, D12), and the one of
// the four call sites that moves in one front door rather than two.
//
// The other three carry no door: a billable first image build and replacing a
// registry tag are YesOrTyped in **both** doors (askCovered), and creating the
// project and linking the billing account are Typed in both, `update` refusing
// their flags outright, so no class here can reach them (init_project.go).
//
// A run with no door is a hand-built options value, a test's: it is `setup`
// (doorName), and the location stays Typed.
func (r *initRun) stepClass() initflow.ConfirmClass {
	if r.o.door == updateDoor.name {
		return initflow.YesOrTyped
	}
	return initflow.Typed
}

// stepConfirmation is how a money-or-permanent step of this door is confirmed
// now: nothing here can confirm it, --yes did, or the person types the step's
// own confirmation.
type stepConfirmation int

const (
	stepLeave stepConfirmation = iota // nothing was asked: the caller leaves the step
	stepByYes                         // --yes confirmed it: show no prompt
	stepTyped                         // the person types this step's own confirmation
)

// stepConfirmation says which of the three this run is at, for a step of the
// class. It is one function because it is one rule: who is there, and then the
// class. The two are read in this order and not the other way round, so that
// the class cannot widen what a terminal may do: a person who can type the
// step's own confirmation types it, whatever the class (canConfirmTyped(Typed)),
// and only a run that is not such a person — no terminal, a pipe, --json,
// --non-interactive, a coding agent's session, or --yes — falls through to
// --yes, which confirms a YesOrTyped step and nothing else. --plan-only is not
// such a person either and --yes is not given it (r.conditions().Yes), so it
// applies nothing whatever it is passed.
func (r *initRun) stepConfirmation(class initflow.ConfirmClass) stepConfirmation {
	if canConfirmTyped(initflow.Typed, r.conditions()) {
		return stepTyped
	}
	yes := r.conditions()
	yes.Terminal, yes.NonInteractive, yes.JSON = false, false, false
	if class == initflow.YesOrTyped && canConfirmTyped(initflow.YesOrTyped, yes) {
		return stepByYes
	}
	return stepLeave
}

// neverConfirmsBuild is the parenthetical that follows "typed at a real
// terminal" in the warning a run that could not confirm a billable image build
// prints: which ways of running do not give that confirmation. It names --yes
// as the way that does, because D12's answer of 2026-10-20 covers the build in
// BOTH front doors (askCovered) — a line that told the reader "--yes never
// confirms it" would name the one flag that does. The steps that stay Typed
// write their own line, at their own call site, where "--yes never confirms it"
// is true.
//
// agent is the run's own reading of the coding-agent marker, and it is why this
// is a function: a `--repo` run has no agent gate in front of the build (the
// converge plans read-only instead, so it never prints this), so an agent's
// session does reach the warning — and there --yes is the flag that session
// refuses, so naming it would be a recipe for getting past the check, in text
// the agent reads (AgentAdvice). The clause is chosen by the marker, as
// convergeOptIn's is.
func neverConfirmsBuild(agent bool) string {
	if agent {
		return "no terminal, --json, a pipe and a coding agent never confirm it"
	}
	return "no terminal, --json, a pipe and a coding agent never confirm it, and --yes does"
}

// askClass is the confirmation of a step that is money or permanent, of the
// class: the project's name typed at a real terminal, or --yes for a YesOrTyped
// step. It shows the banner, and reachable says whether it could be asked at
// all; when it could not, nothing was asked and the caller leaves the step for
// the user.
func (r *initRun) askClass(class initflow.ConfirmClass, what string) (confirmed, reachable bool, err error) {
	fmt.Fprintf(r.w, "⚠ CONFIRM (project %s, GCP project %s): %s\n", r.projectName, r.gcpProject, what)
	switch r.stepConfirmation(class) {
	case stepLeave:
		return false, false, nil
	case stepByYes:
		fmt.Fprintln(r.w, "  confirmed by --yes")
		return true, true, nil
	}
	ok, err := r.typed("Type " + r.projectName + " to confirm: ")
	return ok, true, err
}

// askTyped is askClass for a step that stays Typed in both front doors: the
// project's name typed at a real terminal, never --yes, --non-interactive,
// --json, a pipe or a coding agent.
func (r *initRun) askTyped(what string) (confirmed, reachable bool, err error) {
	return r.askClass(initflow.Typed, what)
}

// askCovered is askClass for a step D12's answer of 2026-10-20 covers in BOTH
// front doors — a billable first image build and replacing a registry tag,
// where the class is YesOrTyped whatever door the reader is at. The door is
// not asked: only three things stay typed (creating the project, linking the
// billing account, and the Firestore location in `setup`), and neither of
// these is one of them.
func (r *initRun) askCovered(what string) (confirmed, reachable bool, err error) {
	return r.askClass(initflow.YesOrTyped, what)
}

// resourceManagerRetries are the waits between reads of the project's
// number after Cloud Resource Manager was enabled, while the enable
// propagates: a few minutes in all. Tests replace them.
var resourceManagerRetries = []time.Duration{5 * time.Second, 10 * time.Second, 15 * time.Second, 30 * time.Second,
	30 * time.Second, 30 * time.Second, 30 * time.Second, 30 * time.Second}

// resourceManager makes sure the project's number can be read, which
// discovery and the state bucket check need before any apply. When the
// Cloud Resource Manager API is disabled (a fresh project: Terraform would
// enable it only in the apply), it offers to enable it through Service
// Usage, after its own confirmation (typed, under --plan-only: never --yes). Without a
// terminal and without --yes it refuses with the gcloud command that does
// it. Once enabled, it rereads the number until the enable propagates.
func (r *initRun) resourceManager(ctx context.Context, c *infra.Clients) error {
	_, err := infra.ProjectNumber(ctx, c, r.gcpProject)
	var sd *infra.ServiceDisabledError
	if !errors.As(err, &sd) {
		return initErr(err)
	}
	command := "gcloud services enable " + infra.ServiceResourceManager + " --project " + quoteWord(r.gcpProject)
	ok, err := r.askOrdinary("enables the Cloud Resource Manager API ("+infra.ServiceResourceManager+"), which is disabled: "+r.o.doorProgram()+" reads the project's number through it before Terraform enables it (free, and nothing fugaro does disables it again)", "")
	if err != nil {
		return err
	}
	if !ok {
		why := initflow.NeedsTerminal("the typed confirmation of this step") + r.yesRoute() + ", or"
		if !r.o.nonInteractive && stdinIsTerminal(r.cmd.InOrStdin()) {
			why = "not confirmed (the project's name was not typed):"
		}
		return userErr("%s enable the Cloud Resource Manager API yourself with %s and rerun; "+r.o.doorProgram()+" needs it to read the project's number. Nothing was enabled or applied",
			why, command)
	}
	stderr := r.cmd.ErrOrStderr()
	fmt.Fprintf(stderr, "enabling %s in %s...\n", infra.ServiceResourceManager, r.gcpProject)
	if err := infra.EnableService(ctx, c, r.gcpProject, infra.ServiceResourceManager); err != nil {
		return remote(fmt.Errorf("%w; enable it with %s and rerun", err, command))
	}
	r.res.Enabled = append(r.res.Enabled, infra.ServiceResourceManager)
	fmt.Fprintf(r.w, "enabled %s in %s\n", infra.ServiceResourceManager, r.gcpProject)
	for i := 0; ; i++ {
		_, err := infra.ProjectNumber(ctx, c, r.gcpProject)
		if !errors.As(err, &sd) {
			return initErr(err)
		}
		if i == len(resourceManagerRetries) {
			break
		}
		wait := resourceManagerRetries[i]
		fmt.Fprintf(stderr, "waiting for the Cloud Resource Manager API to answer while the enable propagates (%d of %d, next try in %s)\n", i+1, len(resourceManagerRetries), wait)
		select {
		case <-ctx.Done():
			return remote(ctx.Err())
		case <-time.After(wait):
		}
	}
	return remote(fmt.Errorf("%s was enabled in %s but still answers SERVICE_DISABLED: the enable is still propagating; rerun "+r.o.doorProgram()+" in a few minutes", infra.ServiceResourceManager, r.gcpProject))
}

// guard is tf.Guard at init's call sites, and the one place a managed
// project's refusal TEXT differs from every other project's (Task 3.2 of
// docs/plans/2026-10-16-managed-projects.md).
//
// What it approves and refuses is tf.Guard's decision over allowDelete, the
// list today's call site has always handed it (--allow-delete, plus what init
// derives itself: the hardening addresses for the installation root, the log
// isolation's own deletes for --forget), in every project, managed or not. The
// owner's delete list (fugaro/allow-delete.json) is not read here and approves
// nothing here: D72/D74 scope it to a managed apply of the project's
// agent-authored root, and these are fugaro's own roots. A plan with no delete
// is admitted without reading anything, as before.
//
// Only the text of a refusal turns on the project, and the project is read
// only once the plan is refused (managedProject). Outside a managed project it
// is today's text, byte for byte: Guard's sentence naming --allow-delete, and a
// hint for each refused delete that flags left out of this run would keep,
// infra.DeleteHints' lines. In a managed project those are an instruction to
// pass a flag, in text the coding agent's session it refuses reads, so neither
// is printed: the refusal ends with initManagedDeleteAdvice, which names no
// flag and sends the reader to the owner — and only when some refused change is
// one an approval could admit (tf.Listable). A refusal made only of what no
// approval clears (an object moved here from another address, an action the
// guard does not know, a change with no actions) ends at what it refused.
//
// When whether the project is managed cannot be read, the refusal is neither
// mode's advice: it says the read failed and names no way past it — never the
// unmanaged text, whose flag is what the read exists to withhold.
func (r *initRun) guard(ctx context.Context, c *infra.Clients, plan *tf.Plan, allowDelete []string) error {
	if err := infra.RefuseProjectJobDeletes(plan); err != nil {
		return userErr("%v", err)
	}
	err := tf.Guard(plan, allowDelete)
	if err == nil {
		return nil
	}
	isManaged, readErr := r.managedProject(ctx, c)
	switch {
	case readErr != nil:
		return userErr("%s\nwhether %s is a managed project could not be read (%s), so this refusal names no way past it: say which address is refused to the person who owns the project",
			tf.GuardWith(plan, allowDelete, ""), safetext.Field(r.projectName), safetext.Field(readErr.Error()))
	case isManaged:
		advice := ""
		if len(tf.Listable(plan, allowDelete)) > 0 {
			advice = initManagedDeleteAdvice
		}
		return userErr("%s", tf.GuardWith(plan, allowDelete, advice))
	}
	msg := err.Error()
	for _, h := range infra.DeleteHints(plan, allowDelete) {
		msg += "\nhint: " + h
	}
	return userErr("%s", msg)
}

// initManagedDeleteAdvice is the sentence a managed project's init refusal ends
// with when the plan deletes or replaces something an approval could admit. It
// names no flag and no agent marker (Task 3.2's negative test), and it does not
// point at the owner's delete list either: that list approves deletes in the
// project's agent-authored root only (D72/D74), never in fugaro's own roots,
// so naming it here would send the owner to write an approval that changes
// nothing.
const initManagedDeleteAdvice = "This is a managed project: deleting or replacing fugaro's own resources in it is its owner's decision, made by the owner and not by this session, so say which address is refused to the person who owns the project and stop"

// isManagedFact is managed.IsManaged; a variable only so a test can make the
// read fail in a way the fake APIs do not (an error that is neither a 403/404,
// which the read answers as not managed, nor a 5xx, which the client retries).
var isManagedFact = managed.IsManaged

// managedProject is whether the project this run installs into is managed: the
// creation fact of Task 0.2. Two things turn on it — which text a guard refusal
// ends with, and whether the installation gets the managed deployer's scoped
// roles (managedScope writes the module's managed_deployer_scoped from it) —
// and it is read for both the same way, once.
//
// It fails closed: an error reading the fact is returned, never read as "not
// managed", because not managed is the mode whose refusal names the flag; and
// a run with no clients to read with is that error too. Only a managed answer
// is kept for the rest of the run (r.managed): a run that creates a managed
// project is managed from the moment its project stage created it, before the
// marker is written at the end of the converge (markCreated) and without
// reading it, and an answer of "not managed" or an error is read again by the
// next refusal rather than remembered.
func (r *initRun) managedProject(ctx context.Context, c *infra.Clients) (bool, error) {
	if r.managed {
		return true, nil
	}
	if c := r.creation(); r.o.managed && c != "" && c == r.gcpProject {
		return true, nil
	}
	if c == nil {
		return false, errors.New("no Google client to read the creation fact with")
	}
	ok, _, err := isManagedFact(ctx, c.Storage, c.CRM, r.projectName, r.gcpProject)
	if err != nil {
		return false, err
	}
	if ok {
		r.managed = true
	}
	return ok, nil
}

// managedScope is whether the installation uses managed projects, decided from
// the project's own creation fact: the module's managed_deployer_scoped, which
// is what creates the managed deployer's seven scoped roles and their seven
// conditioned bindings. installRoot is the path that plans the installation
// root with it, and it writes the variable into the tfvars the plan is made
// from. (--forget reads it here too, but only to refuse: it never plans the
// installation root with the answer any more — an installation that holds the
// fence is refused whole by the door, before a plan exists and before the
// workdir is prepared, managedForgetRefusal — and this read, made there and not
// inside the rollback, is what answers for the fence a project's marks no
// longer admit to; the door's other read, of the installation's own tfvars, is
// installationFenceRecorded.)
//
// A failure is the run's own and not a refusal: there is no plan to refuse
// yet, and a run that cannot tell whether the variable belongs on or off plans
// nothing rather than guessing. (The guard's read-error refusal, which is this
// same fact read at the point where a plan exists to refuse, is elsewhere.) The
// text the read failed with is a bounded field: it is a service's words about
// an object in the operator's project, and the operator is who has to act on
// it.
func (r *initRun) managedScope(ctx context.Context, c *infra.Clients) (bool, error) {
	ok, err := r.managedProject(ctx, c)
	if err != nil {
		return false, initErr(fmt.Errorf("whether %s is a managed project could not be read while this installation was being planned: %s", safetext.Field(r.projectName), safetext.Field(err.Error())))
	}
	return ok, nil
}

// prepare writes the root's tfvars, imports and backend configuration.
func prepare(wd *infra.Workdir, spec infra.InstallationSpec, im infra.Imports) (map[string]string, error) {
	vars, err := infra.InstallationVars(spec)
	if err != nil {
		return nil, err
	}
	// The keys this spec never carries are carried forward from BOTH documents
	// that can hold them, the older first:
	//
	//   - wd.PrevVars, the snapshot PrepareWorkdir took BEFORE it rebuilt
	//     the tree. It is all an ordinary run has: the tree is fresh, so
	//     no tfvars file is there yet. Never a read of wd.Root at this
	//     point — PrepareWorkdir has already deleted the file it names,
	//     and the read would answer "nothing to carry" for exactly the
	//     installation that opted in.
	//   - wd.Vars, the tfvars the workdir holds NOW. A LATER stage of the
	//     same run reaches prepare again — the compute stage's read of the
	//     installation's outputs, on the same workdir — and the keys the
	//     run's own stages wrote into that document are not in the
	//     snapshot: the credentials stage's opt-in is the one that matters.
	//     Without it, naming --provision-batch beside --provision-credentials
	//     would drop the opt-in at the compute stage's first line, before
	//     its own typed confirmation and therefore for an owner who
	//     declines it as much as for one who accepts.
	now, err := wd.Vars()
	if err != nil {
		return nil, userErr("reading the installation's tfvars: %v", err)
	}
	documents := [][]byte{wd.PrevVars, now}
	if spec.CredentialsService == nil {
		// The credentials service's opt-in is the credentials stage's alone
		// (C1.20): this spec never carries it, and the tfvars are rewritten
		// from the spec on every run, so an installation that has opted in
		// would lose the key here and plan the service's removal — a plan the
		// bundle container's prevent_destroy refuses, so every ordinary run of
		// fugaro update after the opt-in would fail instead of applying. The key
		// is carried forward exactly as the workdir had it, present or absent.
		for _, prev := range documents {
			if vars, err = infra.CarriedCredentialsService(vars, prev); err != nil {
				return nil, userErr("reading the installation's tfvars: %v", err)
			}
		}
	}
	// The other key this spec never carries, and for a sharper reason than the
	// opt-in above: it is the deployer's scope, which is what creates the seven
	// scoped roles and their seven conditioned bindings, and a document that
	// loses it plans all fourteen away. installRoot is the path that plans the
	// installation root with it, deciding it from the project's creation fact
	// (through managedScope), and there this carry is a no-op because the spec
	// it was handed already carries the answer; it is the paths that only
	// re-serialize the root — --config-only, the compute stage's read of the
	// installation's outputs, a new machine's config — that would otherwise write a document
	// that reads as an installation without the fence. --forget is not one of
	// those paths: an installation whose document carries the key never reaches
	// this carry, because the run's door refuses `update --forget` for it
	// before the workdir is prepared (installationFenceRecorded,
	// managedForgetRefusal), and a rollback that does reach here has a document
	// with no key in it to carry. So what this carry keeps is the fourteen of
	// every run that rewrites the root's settings and is allowed to run.
	for _, prev := range documents {
		if vars, err = infra.CarriedManagedDeployerScoped(vars, prev); err != nil {
			return nil, userErr("reading the installation's tfvars: %v", err)
		}
	}
	if err := wd.WriteVars(vars); err != nil {
		return nil, userErr("writing the tfvars: %v", err)
	}
	if err := infra.WriteImports(wd.Root, im); err != nil {
		return nil, userErr("writing the imports: %v", err)
	}
	backend, err := wd.WriteBackend(spec.StateBucket, infra.StatePrefixInstallation)
	if err != nil {
		return nil, userErr("%v", err)
	}
	return backend, nil
}

// install stands up or re-converges the installation: discovery, the state
// bucket, the runs bucket's viewers, then plan, guard, confirm and apply,
// and the local config.
func (r *initRun) install(ctx context.Context, c *infra.Clients, t *tf.TF, wd *infra.Workdir, lc *localcfg.Config, spec infra.InstallationSpec, path string, old []byte) error {
	outs, stop, err := r.installRoot(ctx, c, t, wd, lc, spec)
	if err != nil || stop {
		return err
	}
	// 8. The local config.
	return r.writeConfig(ctx, lc, spec, outs, path, old, r.res.Applied)
}

// installRoot is the installation root's discovery, plan, guard, confirmation
// and apply, and its outputs. stop means the run ends here (--plan-only).
// fugaro update --firebase-project runs it twice more, as the first and third of its
// applies.
func (r *initRun) installRoot(ctx context.Context, c *infra.Clients, t *tf.TF, wd *infra.Workdir, lc *localcfg.Config, spec infra.InstallationSpec) (outs infra.InstallationOutputs, stop bool, err error) {
	if spec, err = r.historyJob(ctx, c, lc, spec); err != nil {
		return outs, false, err
	}
	if spec, err = r.coordinatorJob(ctx, c, lc, spec); err != nil {
		return outs, false, err
	}
	im, err := infra.DiscoverInstallation(ctx, c, spec)
	if err != nil {
		return outs, false, initErr(err)
	}
	spec.AdoptLegacyRegistry = im.AdoptLegacyRegistry
	// The installation module's project-wide secret containers: the config's
	// own eligible names unioned with every one discovery found live, so a
	// config edit can never shrink the module's for_each (N3).
	spec.ProjectSecrets = im.ProjectSecrets
	// Whether this installation uses managed projects, which is what the
	// module's managed_deployer_scoped turns on: the deployer's seven scoped
	// roles and their seven conditioned bindings (managedDeployerShipped,
	// deploy/terraform/gcp/modules/installation/deployer.tf). The fact is the
	// project's own — the creation fact init's guard refusal reads, and the
	// only record of it that outlives one run — so the run that creates a
	// managed project (--managed) and every later run that reaches this
	// function (the setup and update doors, which are the only ones that plan
	// and apply the installation root) write the variable; an installation that
	// never asked for managed writes the same tfvars it wrote before the
	// variable existed. A project the owner flags with `fugaro managed flag`
	// gets the fence here, at the next such run, because that command writes
	// the fact and applies nothing. Read here, and not in installOptions,
	// because the answer is a cloud fact: a plain `fugaro update` of a managed
	// installation that dropped the variable would plan the destruction of the
	// fourteen resources the fence is. (--forget used to be the other path that
	// plans this root and read the fact the same way; it plans nothing here any
	// more, and reads the fact only to refuse, at the run's door — see
	// managedScope and managedForgetRefusal.)
	spec.ManagedDeployerScoped, err = r.managedScope(ctx, c)
	if err != nil {
		return outs, false, err
	}
	for _, n := range im.Notes {
		r.warn(n)
	}
	backend, err := prepare(wd, spec, im)
	if err != nil {
		return outs, false, err
	}

	// 3. The state bucket.
	exists, err := infra.CheckStateBucket(ctx, c, spec.Project, spec.StateBucket)
	if err != nil {
		return outs, false, initErr(err)
	}
	if !exists {
		// `update` creates nothing, and a state bucket that is not there is no
		// installation to re-converge: a first installation is `fugaro setup`'s
		// (D5, D12). Without this the re-converge would create the bucket and
		// then the whole installation — the one thing the door exists not to do.
		if r.o.door == updateDoor.name {
			return outs, false, noInstallationRefusal(spec.StateBucket)
		}
		if err := r.confirmOrdinary(fmt.Sprintf("creates gs://%s in %s with versioning, for Terraform state (cents a month)", spec.StateBucket, spec.Region),
			"nothing was created or applied", ""); err != nil {
			return outs, false, err
		}
		if err := infra.CreateStateBucket(ctx, c, spec.Project, spec.Region, spec.StateBucket); err != nil {
			return outs, false, initErr(err)
		}
		fmt.Fprintf(r.w, "created gs://%s; project Viewers can't read it\n", spec.StateBucket)
	} else {
		// A removal that failed after the bucket was created is retried,
		// since the state holds every name, account and condition.
		p, err := infra.BucketPolicy(ctx, c, spec.StateBucket)
		if err != nil {
			return outs, false, initErr(err)
		}
		if grants := infra.ProjectViewerGrants(p); len(grants) > 0 {
			what := fmt.Sprintf("removes project Viewers' read access to gs://%s, which holds the Terraform state (%s)", spec.StateBucket, strings.Join(grants, ", "))
			if r.o.planOnly {
				r.warn("--plan-only changes no IAM; without it, " + r.o.doorProgram() + " " + what)
			} else {
				if err := r.confirmOrdinary(what, "nothing was applied", ""); err != nil {
					return outs, false, err
				}
				if err := infra.RemoveProjectViewers(ctx, c, spec.StateBucket, p); err != nil {
					return outs, false, initErr(err)
				}
				fmt.Fprintf(r.w, "removed project Viewers' read access to gs://%s\n", spec.StateBucket)
			}
		}
	}

	// 5. Plan, show, guard, summary.
	if err := t.Init(ctx, backend); err != nil {
		return outs, false, remote(err)
	}
	// What the installation already is: its name may not change, and an
	// unnamed one is named only on request. Before any plan.
	var prior infra.InstallationOutputs
	havePrior := false
	if raw, err := t.Output(ctx); err == nil {
		// Outputs that exist but can't be read must not pass for "no
		// installation": the name check below would be skipped.
		o, err := infra.DecodeOutputs(raw)
		switch {
		case errors.Is(err, infra.ErrNoOutputs):
			// A fresh state: no prior installation yet.
		case err != nil:
			return outs, false, remote(fmt.Errorf("reading the installation's outputs: %w", err))
		default:
			prior, havePrior = o, true
		}
	}
	if err := checkInstallationName(r.o.name, lc.Name, prior, havePrior); err != nil {
		return outs, false, err
	}
	// 4. The runs bucket's viewers: an IAM change, so only once the name
	// is known to be the installation's.
	if im.AdoptsRunsBucket() {
		if err := r.runsBucketViewers(ctx, c, spec); err != nil {
			return outs, false, err
		}
	}
	if havePrior && prior.RegistryCleanupDryRun != nil && !*prior.RegistryCleanupDryRun && r.o.registryCleanup == "" {
		// Nothing records a cleanup that was switched on, so say when the
		// default puts it back in dry-run.
		r.warn("registry cleanup is on (or off) in the installation, and this plan puts it back to dry-run: pass --registry-cleanup=on or off to keep it")
	}
	changed, err := t.Plan(ctx, infra.PlanFile)
	if err != nil {
		return outs, false, remote(err)
	}
	if changed {
		plan, err := t.Show(ctx, infra.PlanFile)
		if err != nil {
			return outs, false, remote(err)
		}
		fmt.Fprint(r.w, tf.Summary(plan))
		// The 0.7.0 bucket hardening deletes launchers' old objectAdmin
		// grants; exactly those are allowed, and named (bucket-iam.md H7).
		// A delete is never covered, so the step still asks its own name.
		hardening := infra.HardeningAllowDelete(plan, spec.Launchers, spec.Operators)
		// The 0.9.0 upgrade replaces each launcher's runs/-only grant with
		// the runs/ and tokens/ one (a condition is ForceNew); exactly that
		// replace is allowed, and named (bucket-iam.md H5).
		upgrade := infra.LauncherGrantUpgrade(plan, spec.RunsBucket, spec.Launchers, spec.Operators)
		allowed := append(append(slices.Clone(r.o.allowDelete), hardening...), upgrade...)
		if err := r.guard(ctx, c, plan, allowed); err != nil {
			return outs, false, err
		}
		fmt.Fprint(r.w, infra.HardeningBanner(hardening))
		fmt.Fprint(r.w, infra.LauncherGrantUpgradeBanner(upgrade))
		counts := infra.CountPlan(plan)
		r.res.Changes = &counts
		if r.o.planOnly {
			fmt.Fprintf(r.w, "--plan-only: nothing applied; the plan is %s\n", filepath.Join(wd.Root, infra.PlanFile))
			return outs, true, nil
		}
		// 6. Confirm, 7. apply the plan shown.
		if err := r.confirmOrdinary("applies "+counts.String()+r.installLabel, "nothing was applied", r.notCovered(plan, im.Keys())); err != nil {
			return outs, false, err
		}
		if err := t.Apply(ctx, infra.PlanFile); err != nil {
			return outs, false, remote(err)
		}
		r.res.Applied = true
		if !im.AdoptsRunsBucket() {
			// The apply may just have created the runs bucket, with GCS's
			// project Viewer access; it is ours only if discovery says so.
			after, err := infra.DiscoverInstallation(ctx, c, spec)
			if err != nil {
				return outs, false, initErr(err)
			}
			if after.AdoptsRunsBucket() {
				if err := r.runsBucketViewers(ctx, c, spec); err != nil {
					return outs, false, err
				}
			}
		}
	} else {
		r.res.Changes = &infra.PlanCounts{}
		fmt.Fprintln(r.w, "No changes: the installation matches the plan.")
		if r.o.planOnly {
			return outs, true, nil
		}
	}
	raw, err := t.Output(ctx)
	if err != nil {
		return outs, false, remote(err)
	}
	if outs, err = infra.DecodeOutputs(raw); err != nil {
		return outs, false, remote(err)
	}
	return outs, false, nil
}

// runsBucketViewers offers to remove project Viewers' read access to the
// runs bucket, which discovery found ours (in the project, marked). Under
// --plan-only it only says so; declining is allowed and noted.
func (r *initRun) runsBucketViewers(ctx context.Context, c *infra.Clients, spec infra.InstallationSpec) error {
	p, err := infra.BucketPolicy(ctx, c, spec.RunsBucket)
	if err != nil {
		return initErr(err)
	}
	grants := infra.ProjectViewerGrants(p)
	switch {
	case len(grants) == 0:
		return nil
	case r.o.planOnly:
		r.warn(fmt.Sprintf("--plan-only changes no IAM; without it, "+r.o.doorProgram()+" asks to remove project Viewers' read access to gs://%s (%s)", spec.RunsBucket, strings.Join(grants, ", ")))
		return nil
	}
	ok, err := r.askOrdinary(fmt.Sprintf("removes project Viewers' read access to gs://%s, which holds transcripts and caches (%s)", spec.RunsBucket, strings.Join(grants, ", ")), "")
	if err != nil {
		return err
	}
	if !ok {
		r.warn(fmt.Sprintf("project Viewers can still read gs://%s, transcripts and caches included: the removal was declined; rerun "+r.o.doorProgram()+" to remove it", spec.RunsBucket))
		return nil
	}
	if err := infra.RemoveProjectViewers(ctx, c, spec.RunsBucket, p); err != nil {
		return initErr(err)
	}
	fmt.Fprintf(r.w, "removed project Viewers' read access to gs://%s\n", spec.RunsBucket)
	return nil
}

// checkInstallationName refuses a run that would rename the
// installation, or name one that has no name by accident. The name is the
// state's project_name output, the project config's name: and --name; all
// that are set must agree (design §2.5). An installation that has outputs
// but no project_name (applied before M9a) is named only by --name: the
// name is permanent, so it is never taken silently from a config. With no
// outputs yet there is no installation to disagree with.
func checkInstallationName(flagName, configName string, outs infra.InstallationOutputs, haveOutputs bool) error {
	if !haveOutputs {
		return nil
	}
	if outs.ProjectName == "" {
		if flagName == "" {
			return userErr("the installation has no project name yet; naming it is permanent, so pass --name %s to give it the project config's name (see docs/design/m9-budget-and-dashboard.md §13.1)", configName)
		}
		return nil
	}
	if outs.ProjectName != configName || (flagName != "" && flagName != outs.ProjectName) {
		return userErr("the installation's project name is %s; renaming isn't supported (design §2.5): the project config says %s and --name says %q", outs.ProjectName, configName, flagName)
	}
	return nil
}

// nameFromOutputs completes the project config --config-only starts
// without a name: the file is projects/<project_name>.yaml, the name the
// installation reports. An existing file for that name is the base (its
// repositories are kept) when it belongs to the same GCP project, and
// refused when it doesn't.
func nameFromOutputs(lc *localcfg.Config, outs infra.InstallationOutputs) (*localcfg.Config, string, []byte, error) {
	name := outs.ProjectName
	if name == "" {
		return nil, "", nil, userErr("the installation has no project name yet; an operator runs fugaro setup --name <name> first (see docs/design/m9-budget-and-dashboard.md §13.1)")
	}
	path, err := localcfg.ProjectPath(os.Getenv, name)
	if err != nil {
		return nil, "", nil, userErr("the installation's project name: %v", err)
	}
	existing, _, err := localcfg.LoadProject(os.Getenv, name)
	switch {
	case errors.Is(err, localcfg.ErrMissing):
		next := *lc
		next.Name = name
		return &next, path, nil, nil
	case err != nil:
		return nil, "", nil, userErr("%v", err)
	case existing.GCPProject != lc.GCPProject:
		return nil, "", nil, userErr("%s is project %s's config, for GCP project %s, but --gcp-project says %s; the installation's project name is %s", path, name, existing.GCPProject, lc.GCPProject, name)
	}
	old, err := os.ReadFile(path)
	if err != nil {
		return nil, "", nil, userErr("reading the project config: %v", err)
	}
	return existing, path, old, nil
}

// configOnly writes the local config alone, from the installation's
// outputs when its state is reachable, else from the flags. The outputs
// name the project: a config without a name takes theirs, and one with a
// name must match it.
func (r *initRun) configOnly(ctx context.Context, c *infra.Clients, t *tf.TF, wd *infra.Workdir, lc *localcfg.Config, spec infra.InstallationSpec, path string, old []byte) error {
	outs, err := r.readOutputs(ctx, c, t, wd, spec)
	switch {
	case errors.Is(err, errNoState), errors.Is(err, infra.ErrNoOutputs):
		if lc.Name == "" {
			return userErr("there is no project config and no installation state to take the project's name from (%v): pass --name", err)
		}
		// Nothing applied yet: only then do the flags stand in.
		host, herr := infra.RegistryHost(lc)
		if herr != nil {
			return initErr(herr)
		}
		r.warn(fmt.Sprintf("the installation has no state yet (%v); the local config is written from the flags", err))
		outs = infra.InstallationOutputs{RunsBucket: spec.RunsBucket, RegistryHost: host, LogView: lc.LogView}
	case err != nil:
		// A state bucket that isn't ours, or a state that can't be read, is
		// a failure, not something to work around.
		return initErr(err)
	case lc.Name == "":
		if lc, path, old, err = nameFromOutputs(lc, outs); err != nil {
			return err
		}
		r.setProject(lc)
		printProjectHeader(r.cmd.ErrOrStderr(), lc)
	case outs.ProjectName == "":
		return userErr("the installation has no project name yet; an operator runs fugaro setup --name %s first (see docs/design/m9-budget-and-dashboard.md §13.1)", lc.Name)
	case outs.ProjectName != lc.Name:
		return userErr("the installation's project name is %s; renaming isn't supported (design §2.5): project config %s names it %s", outs.ProjectName, path, lc.Name)
	}
	return r.writeConfig(ctx, lc, spec, outs, path, old, false)
}

// errNoState is a state bucket that doesn't exist yet.
var errNoState = errors.New("no state bucket")

// noInstallationRefusal is the one refusal of a run that looked for an
// installation and found none: the sentence names the state bucket it looked
// for and the door that makes one. It is one function because it is one fact in
// two places — `update`'s converge, which would otherwise create the bucket and
// then the whole installation (installRoot), and the `--repo` engine, which
// reaches the same state bucket from a checkout — and both name `fugaro setup`,
// the door a first installation is (D5, D12).
func noInstallationRefusal(stateBucket string) error {
	return userErr("there is no Terraform state bucket gs://%s, so no installation: run fugaro setup first", stateBucket)
}

func (r *initRun) readOutputs(ctx context.Context, c *infra.Clients, t *tf.TF, wd *infra.Workdir, spec infra.InstallationSpec) (infra.InstallationOutputs, error) {
	exists, err := infra.CheckStateBucket(ctx, c, spec.Project, spec.StateBucket)
	if err != nil {
		return infra.InstallationOutputs{}, err
	}
	if !exists {
		return infra.InstallationOutputs{}, fmt.Errorf("%w gs://%s", errNoState, spec.StateBucket)
	}
	backend, err := prepare(wd, spec, infra.Imports{})
	if err != nil {
		return infra.InstallationOutputs{}, err
	}
	if err := t.Init(ctx, backend); err != nil {
		return infra.InstallationOutputs{}, remote(err)
	}
	raw, err := t.Output(ctx)
	if err != nil {
		return infra.InstallationOutputs{}, remote(err)
	}
	return infra.DecodeOutputs(raw)
}

// writeConfig writes the local config from the installation's outputs and
// the flags, keeping everything else (repos, the legacy registry, the
// endpoints) and dropping build.service_account. It shows the diff first,
// and backs up the file it replaces. A diff is confirmed by the apply's
// confirmation (confirmed), else it asks on its own.
func (r *initRun) writeConfig(ctx context.Context, lc *localcfg.Config, spec infra.InstallationSpec, outs infra.InstallationOutputs, path string, old []byte, confirmed bool) error {
	if outs.ProjectName != "" && outs.ProjectName != lc.Name {
		return userErr("the installation's project name is %s, but the project config names %s; it isn't rewritten (renaming isn't supported, design §2.5)", outs.ProjectName, lc.Name)
	}
	r.res.Outputs = &outs
	next := *lc
	if outs.RunsBucket != "" {
		next.RunsBucket = outs.RunsBucket
	}
	next.RegistryHost = outs.RegistryHost
	next.SetupPending = ""
	next.LogView = outs.LogView
	next.Terraform.StateBucket = spec.StateBucket
	if r.o.launchersChanged {
		next.Terraform.Launchers = slices.Clone(spec.Launchers)
	}
	if r.o.operatorsChanged {
		next.Terraform.Operators = slices.Clone(spec.Operators)
	}
	if r.o.alertEmailChanged {
		next.Terraform.AlertEmail = r.o.alertEmail
	}
	if r.o.baseImageChanged {
		next.BaseImages = maps.Clone(next.BaseImages)
		if next.BaseImages == nil {
			next.BaseImages = map[string]string{}
		}
		for _, v := range r.o.baseImages {
			kind, ref, err := localcfg.ParseBaseImageFlag(v)
			if err != nil {
				return userErr("%v", err)
			}
			next.BaseImages[kind] = ref
			if n := localcfg.BaseImageNameKind(ref); n != kind {
				r.warn(fmt.Sprintf("--base-image %s names an image whose repository is not fugaro-%s: make sure it is the %s base image", v, kind, kind))
			}
		}
	}
	next.Build.ServiceAccount = ""
	// The credentials service, from the installation's own record: the
	// module's output carries the service's URL once the stage that turns it
	// on has filled the audience (init_credentials.go), and this is what puts
	// it in the local config — and so in the published one — for `fugaro run`
	// to hand to a job. An installation whose outputs carry no service (never
	// opted in, or opted out) clears the block: the URL it named is not a
	// service that exists any more, and a launch that carried it would put a
	// dead address on a job.
	//
	// It is set from the OUTPUTS and not from a flag: there is no way for a
	// person to write this value by hand, because there is no way for them to
	// know it (Cloud Run's URL carries the project number).
	next.CredentialsService = credentialsServiceBlock(outs.CredentialsService)
	// The coordinator's opt-in, from the installation's own record as well:
	// an account in the outputs is an installation whose owner opted in, and
	// a config that did not say so would plan its removal on the next run.
	// It is never cleared here; turning the coordinator off is the owner's
	// edit of this key, and the next run's plan shows the removal.
	if outs.CoordinatorServiceAccount != "" {
		next.Terraform.Coordinator = true
	}
	if r.mutate != nil {
		r.mutate(&next)
	}
	switch {
	case r.o.schedulerRegion != "":
		next.SchedulerRegion = r.o.schedulerRegion
	case next.SchedulerRegion == "":
		sr, err := gcp.SchedulerRegion(next.Region)
		if err != nil {
			return userErr("%v", err)
		}
		next.SchedulerRegion = sr
	}
	if err := r.writeLocalConfig(&next, path, old, confirmed); err != nil {
		return err
	}
	if !r.o.planOnly {
		r.publishSharedConfig(ctx, &next)
	}
	return nil
}

// credentialsServiceBlock is the local config's credentials_service block for
// an installation whose outputs carry s: nil when they carry none.
//
// The URL is the one field, and the block is nil rather than empty when the
// installation has no service, because the block's PRESENCE is what `fugaro
// run` reads as "this installation has one" — an empty block would put an
// empty variable on a job, which the runner reads as set (the runner's own
// check is `url == ""`, so an empty value is "no service" there, but the
// provider gate in internal/gitprov reads presence) and which the config
// loader refuses in the first place.
func credentialsServiceBlock(s *infra.CredentialsService) *localcfg.CredentialsService {
	if s == nil || s.URL == "" {
		return nil
	}
	return &localcfg.CredentialsService{URL: s.URL}
}

// publishSharedConfig publishes the shared config after the local config is
// written. A failure only warns: the installation works without it.
func (r *initRun) publishSharedConfig(ctx context.Context, lc *localcfg.Config) {
	if m := agentMarker(os.Getenv); m != "" {
		fmt.Fprintf(r.w, "note: the shared config was not published: %s; run "+r.o.doorProgram()+" --publish-config in your own terminal\n", initflow.AgentRefusal(m))
		return
	}
	written, err := publishSharedWarn(ctx, lc, r.warn)
	if err != nil {
		r.warn("could not publish the shared config: " + err.Error() + " (teammates will need fugaro setup until it is published)")
		return
	}
	if written {
		fmt.Fprintf(r.w, "published the shared config to %s/%s\n", lc.BucketURL(), infra.SharedConfigObject)
	}
}

// writeLocalConfig writes next to path, showing the diff first and backing
// up the file it replaces. A diff is confirmed by an apply's confirmation
// (confirmed), else it asks on its own. An unchanged file isn't written.
func (r *initRun) writeLocalConfig(next *localcfg.Config, path string, old []byte, confirmed bool) error {
	if path == "" {
		// A shared selection has no file; never write to an empty path.
		return userErr("there is no local config file to write (a shared config has none): run fugaro setup to create your own local config")
	}
	data, err := next.Marshal()
	if err != nil {
		return err
	}
	if _, err := localcfg.Parse(data); err != nil {
		return userErr("the new local config: %v", err)
	}
	r.res.Config = path
	if old != nil && bytes.Equal(old, data) {
		fmt.Fprintf(r.w, "The local config %s is up to date.\n", path)
		return nil
	}
	fmt.Fprintf(r.w, "The local config %s changes:\n%s", path, lineDiff(string(old), string(data)))
	if !confirmed {
		if err := r.confirmOrdinary("writes the local config "+path+" as shown", "the local config was not changed", ""); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return userErr("%v", err)
	}
	if old != nil {
		backup, err := backupFile(path, old, time.Now())
		if err != nil {
			return userErr("backing up the local config: %v", err)
		}
		r.res.Backup = backup
		fmt.Fprintf(r.w, "backed up the old local config to %s\n", backup)
	}
	if err := writeFileAtomic(path, data); err != nil {
		return userErr("writing the local config: %v", err)
	}
	fmt.Fprintf(r.w, "wrote %s\n", path)
	if old == nil {
		r.noteSecondProject()
	}
	return nil
}

// noteOpenProviders says, once per run, which providers every repository of
// the project may use (internal/config's rule): their keys are mounted into
// every repository's job and every repository may send them code. Before
// this design an absent allow_data_to meant nobody, so an operator who
// never wrote the key is now at the widest setting; this says so at the one
// command that acts on it. A note, not a warning: the owner ruled it valid.
func (r *initRun) noteOpenProviders(lc *localcfg.Config) {
	if r.notedOpenProviders || lc == nil {
		return
	}
	r.notedOpenProviders = true
	w := r.w
	if r.o.printVars {
		w = r.cmd.ErrOrStderr() // stdout stays the tfvars alone
	}
	for _, name := range config.OpenProviders(lc.Providers) {
		fmt.Fprintf(w, "note: provider %s %s\n", name, lc.Providers[name].OpenToEveryRepoReason())
	}
}

// noteSecondProject says, once a new project config is written beside
// another, what that means for commands run outside a checkout.
func (r *initRun) noteSecondProject() {
	if names, err := localcfg.Projects(os.Getenv); err == nil && len(names) > 1 {
		fmt.Fprintln(r.w, "note: two project configs now exist; commands outside a checkout need FUGARO_PROJECT=<name> or --project <name> (a checkout's fugaro.yaml project: selects by itself)")
	}
}

// backupFile writes old next to path as path.bak-<UTC timestamp>, mode
// 0600, never over an existing file.
func backupFile(path string, old []byte, now time.Time) (string, error) {
	base := path + ".bak-" + now.UTC().Format("20060102T150405Z")
	for i := 0; ; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := f.Write(old); err != nil {
			f.Close()
			return "", err
		}
		return name, f.Close()
	}
}

// writeFileAtomic replaces path with data (mode 0600) through a rename, so
// a failed write never leaves half a config. A symlinked path is written
// through: its target is replaced and the link stays.
//
// The mode is the POSIX half of what keeps the config to its owner, and the
// config is worth keeping: it can carry a budget's firebase_api_key (the
// Budget block in internal/localcfg), and backupFile's copy of the same bytes
// is 0600 for the same reason. It is not claimed to protect them on Windows:
// Go's os.Chmod there sets the read-only attribute and nothing else, so what
// keeps another local user out of the file is the ACL of the directory it
// sits in. **D12 (file modes) is open** in docs/windows-cli/reconciliation.md,
// the Windows CLI gets its release in **Slice 5**, and docs/windows-cli/README.md
// records that a Windows release must not ship without that decision. The long
// form of this note is at the deployer credential in internal/cli/managed_apply.go.
func writeFileAtomic(path string, data []byte) error {
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".config-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// lineDiff shows the lines of a that b drops (-) and adds (+), in order,
// from their longest common subsequence.
func lineDiff(a, b string) string {
	x, y := splitLines(a), splitLines(b)
	// lcs[i][j] is the LCS length of x[i:] and y[j:].
	lcs := make([][]int, len(x)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(y)+1)
	}
	for i := len(x) - 1; i >= 0; i-- {
		for j := len(y) - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out strings.Builder
	i, j := 0, 0
	for i < len(x) || j < len(y) {
		switch {
		case i < len(x) && j < len(y) && x[i] == y[j]:
			i, j = i+1, j+1
		case j < len(y) && (i == len(x) || lcs[i][j+1] >= lcs[i+1][j]):
			out.WriteString("  + " + y[j] + "\n")
			j++
		default:
			out.WriteString("  - " + x[i] + "\n")
			i++
		}
	}
	return out.String()
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// installationFenceRecorded is whether the installation's own tfvars record the
// managed deployer's fence: the document the workdir holds as this run finds
// it, read before anything prepares that workdir. It is the INSTALLATION's
// record of the fourteen and not the project's mode.
//
// That distinction is the whole point of reading it. Removing the two marks
// that make a project managed (docs/design/managed-projects.md §10.1, "What makes a project
// managed") does not touch the fourteen the installation holds, and this
// document goes on recording them, because prepare's carry never drops the key.
// A rollback decided from the live creation fact alone would therefore let
// exactly the owner the refusal is written for walk past it — marks off,
// `--forget`, and the state removal takes the fence out of Terraform's state
// while it stays live in the project.
//
// The read is of the file on disk, and it is made where it is so that the
// refusal it decides leaves the file where it is: the preparation that follows
// deletes every file of the earlier run, and a refusal that deleted this one
// would leave the next attempt nothing to be refused from. The error it returns
// is the run's, not a refusal: a document that cannot be read is not a document
// that does not record the fence.
func installationFenceRecorded(lc *localcfg.Config) (bool, error) {
	dir, err := infra.InstallationWorkdir(os.Getenv, lc.GCPProject)
	if err != nil {
		return false, userErr("%v", err)
	}
	vars, err := infra.ReadWorkdirVars(dir, "installation")
	if err != nil {
		return false, userErr("%v", err)
	}
	recorded, err := infra.ManagedDeployerScopedRecorded(vars)
	if err != nil {
		return false, userErr("%v", err)
	}
	return recorded, nil
}

// The two subjects managedForgetRefusal's sentence opens with, one per record.
// Each is a format string whose %s is the project, because the sentence names
// the installation in its own clause ("… and proj-1234's installation holds …").
const (
	// holdsFenceRecorded: the installation's own document carries the key.
	holdsFenceRecorded = "%s's installation holds the managed deployer's fence"
	// holdsFlaggedManaged: the project's label and marker are what answered.
	holdsFlaggedManaged = "%s is flagged managed, and the managed deployer's fence belongs to its installation"
)

// managedForgetRefusal is the sentence `fugaro update --forget` stops with for
// an installation that holds the managed deployer's fence: the rollback is not
// that installation's command, and what it names instead is the owner's work.
//
// Why it is refused rather than run, and why no flag gets past it: the
// installation's own resources include the fence — seven scoped roles and their
// seven conditioned bindings, created by the apply that converges a managed
// project — and the rollback's second phase is one `terraform state rm
// module.installation` over every address the installation holds. The fourteen
// would leave Terraform's state with the rest, live in the project and accounted
// for by nothing: the whole-project inventory the managed apply reads before it
// plans compares the live project with what the installation's states hold, so
// every later run of that project would find the fence unaccounted for, and a
// converge could not put it back — its plan creates roles that already exist.
//
// holds says which of the two records answered, in the sentence's own terms,
// because the two know different things. The installation's own document with
// the key in it knows the fence is the installation's (holdsFenceRecorded). The
// project's marks do not: `fugaro managed flag` writes the label and the marker
// without converging anything, so what they say is that the project is flagged
// managed and that its installation is the managed path's — whose apply is the
// one that converges the fence — and not that the fence is live in the state.
// The sentence holds either way: the fence leaves Terraform's state with the
// rest "wherever the installation holds it".
//
// why names the record that said so, since only one of them is always there to
// answer: the installation's own tfvars, read by the door before anything
// prepares the workdir, or the project's two marks, read by the same door
// through setupCloud (Task 3.0's half: a project whose marks have come off still
// holds the fence, and a document that has lost the key still belongs to a
// project whose marks do not come off by themselves).
//
// The next step is deliberately spelled here rather than left to a document,
// and it is deliberately NOT "take the project out of managed mode": that is the
// undo of the mode, and it leaves the fence where it is — this refusal does not
// lift when the marks come off, because what it refuses is the fence's orphaning
// and not the project's mode. What an installation that holds the fence has is
// the owner's own work in the project, which is what the guide's rollback
// section says of everything the rollback leaves.
func managedForgetRefusal(project, holds, why string) error {
	return userErr("fugaro update --forget is the rollback of an installation from before managed projects, and %s — seven scoped roles and their seven conditioned bindings — so this run stops here: the rollback's second phase removes every address of the installation from Terraform's state, and the fence would leave it with them wherever the installation holds it — live in the project with nothing accounting for it (the inventory the managed apply reads before it plans would find it unaccounted on every later run) and no way to put it back, because a converge's plan creates roles that already exist. %s. The fence is written by the apply that creates the fourteen and carried forward by every run that rewrites the installation's settings, so it is the installation's and not a mode of the project: taking the project out of managed mode does not remove it, and no command of ours does. Nothing of the installation was read, planned or forgotten: not the state bucket, not its outputs, not a plan and not a state removal. What is left for an installation that holds the fence is the owner's own work in the project (docs/design/managed-projects.md §10.2, \"Rolling back\")",
		fmt.Sprintf(holds, project), why)
}

// forget is the rollback: a guarded apply that turns the log isolation and
// registry cleanup off, then state rm of everything, each confirmed.
func (r *initRun) forget(ctx context.Context, c *infra.Clients, t *tf.TF, wd *infra.Workdir, spec infra.InstallationSpec) error {
	// No managed installation reaches this function: both of its records are
	// asked at the run's door, before the workdir this one holds was prepared
	// at all, and either of them answering "managed" is a refusal
	// (runInit, installationFenceRecorded, managedScope,
	// managedForgetRefusal). The reason both are asked up there and not here is
	// that the preparation this engine is set up through rebuilds the tree, so
	// anything decided from it here would be decided after the installation's
	// own tfvars — one of the two records — had already been deleted.
	//
	// What is left here is the rollback of an installation that neither record
	// claims, which is the installation from before managed projects that
	// --forget is for.
	exists, err := infra.CheckStateBucket(ctx, c, spec.Project, spec.StateBucket)
	if err != nil {
		return initErr(err)
	}
	if !exists {
		return userErr("there is no state bucket gs://%s, so no installation state to forget", spec.StateBucket)
	}
	repos, err := infra.RepoStates(ctx, c, spec.StateBucket)
	if err != nil {
		return initErr(err)
	}
	if len(repos) > 0 {
		return userErr("the state bucket still holds repository state (%s; only *.tfstate objects count, not locks): run fugaro update --repo --forget for each repository first", strings.Join(repos, ", "))
	}
	fs := infra.ForgetSpec(spec)
	// fs.ManagedDeployerScoped stays the zero value, and nothing is carried
	// back into it: the refusals at the door are the only answer an
	// installation that holds the fence gets from this command, so a run that
	// reaches this line belongs to a project neither record claims, and its
	// document (the one prepare rewrites) carries no key for the carry in
	// prepare to keep.
	backend, err := prepare(wd, fs, infra.Imports{})
	if err != nil {
		return err
	}
	if err := t.Init(ctx, backend); err != nil {
		return remote(err)
	}
	// The rollback keeps everything but the log isolation declared, the
	// adopted legacy registry included (it can't be deleted, and mustn't
	// be), so it takes adopt_legacy_registry from the state's outputs.
	raw, err := t.Output(ctx)
	if err != nil {
		return remote(err)
	}
	outs, err := infra.DecodeOutputs(raw)
	switch {
	case errors.Is(err, infra.ErrNoOutputs):
		return userErr("the state in gs://%s holds no installation, so there is nothing to forget", spec.StateBucket)
	case err != nil:
		return remote(err)
	}
	fs.AdoptLegacyRegistry = outs.LegacyRegistry != ""
	if _, err := prepare(wd, fs, infra.Imports{}); err != nil {
		return err
	}
	changed, err := t.Plan(ctx, infra.PlanFile)
	if err != nil {
		return remote(err)
	}
	if changed {
		plan, err := t.Show(ctx, infra.PlanFile)
		if err != nil {
			return remote(err)
		}
		fmt.Fprint(r.w, tf.Summary(plan))
		if err := infra.CheckForgetPlan(plan); err != nil {
			return initErr(err)
		}
		if err := r.guard(ctx, c, plan, infra.ForgetAllowDelete(plan)); err != nil {
			return err
		}
		counts := infra.CountPlan(plan)
		r.res.Changes = &counts
		if err := r.confirm("rollback step 1 of 2: applies "+counts.String()+": deletes the log isolation (the _Default exclusion, the sink, the view and its grants, the log bucket) and turns registry cleanup off",
			"nothing was applied or forgotten"); err != nil {
			return err
		}
		if err := t.Apply(ctx, infra.PlanFile); err != nil {
			return remote(err)
		}
		r.res.Applied = true
		for _, rc := range plan.ResourceChanges {
			if slices.Contains(rc.Change.Actions, "delete") {
				r.res.Deleted = append(r.res.Deleted, rc.Address)
			}
		}
	} else {
		fmt.Fprintln(r.w, "No changes: log isolation and registry cleanup are already off.")
	}
	if err := r.confirm("rollback step 2 of 2: removes every address from Terraform's state (terraform state rm module.installation); it destroys nothing",
		"the state still holds the installation"); err != nil {
		return err
	}
	if err := t.StateRm(ctx, "module.installation"); err != nil {
		return remote(err)
	}
	r.res.Forgotten = true
	fmt.Fprintln(r.w, "Terraform no longer manages the installation; nothing else was destroyed. Restore the backed-up local config and redeploy each job with the M4 binary.")
	if slices.Contains(r.res.Deleted, infra.LogBucketAddress) {
		r.res.Undelete = infra.LogBucketUndelete(spec.Project)
		r.warn(fmt.Sprintf("the log bucket %s is now pending deletion for 7 days, and its ID can't be reused meanwhile; a migration retried within the week runs this first: %s",
			infra.LogBucket, r.res.Undelete))
	}
	return nil
}

func (r *initRun) printResult() error {
	if !r.o.asJSON {
		return nil
	}
	if r.res.LeftForYou == nil {
		r.res.LeftForYou = []initflow.Left{} // an array, never null
	}
	r.printed = true
	enc := json.NewEncoder(r.cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(r.res)
}

// checkCoordinator refuses the --coordinator opt-in where it cannot be what
// the owner reads and confirms. It follows --provision-credentials: an apply
// of the installation's own root, never covered by --yes, and excluding the
// flags that only read or only stop. It also needs --firebase-project,
// because the account's one database grant is the Firebase root's and the job
// is deployed by that run.
func (o *initOptions) checkCoordinator() error {
	if o.firebase == "" {
		return userErr("--coordinator opts the installation in to the coordinator's aggregation job, whose account the Firebase root grants the database's admin: it needs --firebase-project (fugaro update --firebase-project <id> --coordinator)")
	}
	if o.yes {
		return userErr("--yes never covers --coordinator: the opt-in creates an account that holds the Firebase project's database admin and a job that holds the GitHub App's key, so it is confirmed at a real terminal (run it again without --yes)")
	}
	var set []string
	for _, f := range []struct {
		name string
		on   bool
	}{{"--repo", o.repo}, {"--forget", o.forget}, {"--print-vars", o.printVars},
		{"--config-only", o.configOnly}, {"--publish-config", o.publishConfig}, {"--anchor", o.anchor}} {
		if f.on {
			set = append(set, f.name)
		}
	}
	if len(set) > 0 {
		return userErr("--coordinator turns the coordinator's aggregation job on, which is an apply: it excludes %s", strings.Join(set, ", "))
	}
	return nil
}

// refuseCoordinatorUnattended is the rest of --coordinator's gate, the part
// that reads the environment: a coding agent's session is refused, and so is
// a run with no terminal to confirm at.
func (r *initRun) refuseCoordinatorUnattended() error {
	if m := agentMarker(os.Getenv); m != "" {
		return userErr("--coordinator opts the installation in to the coordinator's aggregation job: %s", initflow.AgentRefusal(m))
	}
	if r.cmd == nil || !stdinIsTerminal(r.cmd.InOrStdin()) {
		return userErr("--coordinator is confirmed at a real terminal: run it from one")
	}
	return nil
}

// checkRepo refuses flag combinations that mean nothing for --repo.
func (o *initOptions) checkRepo() error {
	if o.coordinator {
		return userErr("--coordinator opts the installation in to the coordinator's aggregation job, which is an apply of the installation's own root: it excludes --repo (run fugaro update --firebase-project <id> --coordinator for the installation)")
	}
	if o.provisionCredentials {
		// Like --provision-batch, and for the same reason: this run validates
		// here and never reaches check(). --repo onboards a repository's own
		// root and --provision-credentials applies the installation's, which
		// is why the flag's help says it excludes --repo.
		return userErr("--provision-credentials turns the installation's credentials service on, which is an apply of the installation's own root: it excludes --repo (run fugaro update --provision-credentials for the installation, then fugaro update --repo in each checkout, which is the step that drops the jobs' model-secret accessors and grants their accounts an invoker on the service)")
	}
	if o.provisionBatch {
		// This run validates here and never reaches check(), so the pair has
		// to be refused here as well: --repo onboards a repository's own root
		// and --provision-batch applies the installation's compute root, which
		// is why the flag's help says it excludes --repo. Accepted, it would
		// onboard the repository and drop the provisioning the user named.
		return userErr("--provision-batch provisions the installation's Batch compute backend, which is an apply of the installation's own root: it excludes --repo (run fugaro setup --provision-batch for the installation, then fugaro update --repo for the repository)")
	}
	if o.anchor {
		return userErr("--anchor and --repo exclude one another: --anchor writes the gcp_project line of an onboarded repository's checkout (run %s --anchor there), and %s --repo does not write it", o.doorProgram(), o.doorProgram())
	}
	if err := o.checkAppID(); err != nil {
		return err
	}
	if err := o.checkOnboardRepo(); err != nil {
		return err
	}
	n := 0
	if o.retire && o.planOnly {
		// terraform state rm is a change to the state, and --plan-only's
		// contract is that it creates and changes nothing (design §3.3).
		return userErr("--retire and --plan-only exclude one another: retiring removes addresses from Terraform's state, and --plan-only changes nothing. Run --plan-only first to read the predicted retirement, then --retire to run it")
	}
	if o.retire && o.printVars {
		// --print-vars writes the tfvars and returns before the first cloud
		// call -- no discovery, no state, no plan -- so a --retire that came
		// with it would print the file and drop the retirement silently.
		return userErr("--retire and --print-vars exclude one another: --print-vars writes the tfvars and stops before the state is read, so the retirement would be dropped. Run --print-vars on its own, then --retire")
	}
	for _, b := range []bool{o.planOnly, o.printVars, o.forget, o.retire} {
		if b {
			n++
		}
	}
	if n > 1 {
		return userErr("--plan-only, --print-vars, --forget and --retire exclude one another")
	}
	if o.forget && (len(o.allowDelete) > 0 || o.allowJobDelete || o.noBuild || o.retire) {
		return userErr("--forget only removes the repository from Terraform's state; it takes no --allow-delete, --allow-job-delete, --no-build or --retire")
	}
	if set := o.installationFlags(); len(set) > 0 {
		return userErr("%s set(s) up the installation, not a repository: run fugaro setup with it, then fugaro update --repo", strings.Join(set, ", "))
	}
	return nil
}

// installationFlags are the flags set that only mean something for the
// installation, sorted.
func (o *initOptions) installationFlags() []string {
	installationOnly := map[string]bool{
		"--config-only": o.configOnly, "--publish-config": o.publishConfig, "--anchor": o.anchor, "--budget": o.budget != 0, "--budget-currency": o.budgetCurrency != "",
		"--billing-account": o.billingAccount != "", "--alert-email": o.alertEmailChanged, "--launcher": o.launchersChanged,
		"--operator": o.operatorsChanged, "--base-image": o.baseImageChanged, "--base": len(o.baseKinds) > 0, "--image-source": o.imageSource != "", "--expect-digest": len(o.expectDigests) > 0, "--replace-image": len(o.replaceImages) > 0, "--no-log-isolation": o.noLogIsolation,
		"--registry-cleanup": o.registryCleanup != "", "--runs-bucket": o.runsBucket != "", "--scheduler-region": o.schedulerRegion != "",
		"--firebase-project": o.firebase != "", "--budget-mode": o.budgetMode != "", "--budget-admin": o.budgetAdminsChanged,
		"--create-project": o.createProject, "--managed": o.managed, "--display-name": o.displayName != "", "--parent": o.parent != "", "--link-billing": o.linkBilling != "",
	}
	var set []string
	for _, f := range slices.Sorted(maps.Keys(installationOnly)) {
		if installationOnly[f] {
			set = append(set, f)
		}
	}
	return set
}

// runInitRepo onboards the repository of a checkout: its resources are
// adopted or created, its first images built and its jobs deployed, each
// step confirmed; then the local config records it.
func runInitRepo(r *initRun, args []string) error {
	cmd, o := r.cmd, r.o
	defer func() { clear(r.appKeyMem) }()
	if o.checkApp {
		if err := refuseAppKeyReadInAgent(os.Getenv); err != nil {
			return err
		}
	}
	if err := o.checkRepo(); err != nil {
		return err
	}
	if !o.printVars {
		if err := refuseHTTP2Debug(os.Getenv); err != nil {
			return err
		}
	}
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	return r.repoEngine(cmd.Context(), dir, "", false)
}

// billableBuildLeft is the left-for-you for a billable first image build: the
// step that is money, and the one thing repoEngine leaves when the build's
// typed confirmation cannot be had. It is one function because it is one
// prompt, in two wordings that differ only in which command starts it:
// embedded is the converge's repository stage (the checkout), false is
// fugaro update --repo. Both are LeftPrompt, and that is the point — the person
// types the project's name at the build's own prompt, and there is nothing to
// paste that would do it for them. The confirmation matrix
// (setup_refusals_test.go) calls this too, so a change here moves it there.
func billableBuildLeft(embedded bool, door string) initflow.Left {
	if embedded {
		return promptLeft(initflow.Repository, "in the checkout, type the project's name at the first image build's prompt (it is billable)", door)
	}
	return promptLeft(initflow.Repository, "type the project's name at the first image build's prompt (it is billable)", door, "--repo")
}

// repoEngine is update --repo's engine for the checkout at dir. The converge's
// repository stage runs it too (embedded): then the result is the converge's
// to print, and bin is the terraform its environment check already found.
func (r *initRun) repoEngine(ctx context.Context, dir, bin string, embedded bool) error {
	o, cmd := r.o, r.cmd
	finish := func() error {
		if embedded {
			return nil
		}
		return r.printResult()
	}

	// 1. The checkout, its fugaro.yaml, and the local config.
	if err := o.requireCheckoutProject(ctx, dir); err != nil {
		return err
	}
	root, rf, err := loadCheckoutResolved(ctx, dir, nil, layerOptions{Lenient: true})
	if err != nil {
		return err
	}
	cfg := rf.Cfg
	repo, err := checkoutRepo(ctx, root)
	if err != nil {
		return err
	}
	repoURL, err := checkoutURL(ctx, root)
	if err != nil {
		return err
	}
	lc, path, old, err := loadRepoConfig(ctx, o, dir)
	if err != nil {
		return err
	}
	r.setProject(lc)
	r.noteOpenProviders(lc)
	r.res.Repo = repo
	if !embedded && !o.planOnly && !o.forget && !o.printVars {
		if err := r.gateRepo(ctx, root, lc); err != nil {
			return err
		}
	}
	if !o.forget {
		if err := checkVertexBudget(lc, cfg); err != nil {
			return err
		}
	}
	// A route that sets require_zdr or price_policy lands in the job's
	// FUGARO_MODEL_PROVIDERS, and an older runner refuses an unknown key
	// there at bootstrap: refuse before any cloud effect (Task 4 step 2 of
	// the price-catalog plan).
	if err := r.gateCatalogSwitches(o, repo, lc); err != nil {
		return err
	}
	// agent.auth: cascade is a fugaro.yaml value an older runner's config
	// reader refuses outright, so the same kind of break, judged against the
	// base image the job is built from (Task C1.1's skew constant). The gate
	// is told which layer set the value (rf.Res), since a resolved cascade is
	// not necessarily the repository's file asking for one: the subject of
	// its messages names that layer, never a file that may not carry it.
	if err := r.gateCascadeAuth(o, repo, lc, cfg, rf.Res.SourceOf("agent.auth")); err != nil {
		return err
	}
	// The rigor: block and defaults.rigor are the same kind of break: keys a
	// strict decoder older than rigorSince refuses at bootstrap.
	if err := r.gateRigor(o, repo, lc, cfg); err != nil {
		return err
	}
	// The governance: block, in either file, is the same kind of break, on
	// governanceSince.
	if err := r.gateGovernance(o, repo, lc, cfg); err != nil {
		return err
	}
	// git.author is the same kind of break on gitAuthorSince, in the
	// repository's own file alone: the key is InRepo, so no layer can carry
	// it.
	if err := r.gateGitAuthor(o, repo, lc, cfg); err != nil {
		return err
	}
	if !o.forget {
		w := r.w
		if o.printVars {
			w = cmd.ErrOrStderr() // stdout stays the tfvars alone
		}
		printCeiling(w, lc, cfg)
	}
	in := infra.Inputs{LC: lc, Repo: repo, Cfg: cfg, FileSetsBaseBranch: fileSetsBaseBranch(rf), RepoURL: repoURL, GitHubAppID: o.githubAppID}
	var spec infra.RepoSpec
	if !o.forget {
		// Every name, before any cloud call: a spec that can't be
		// computed (a GitHub repository without its App ID) stops here.
		if spec, err = infra.Repo(in); err != nil {
			return initErr(err)
		}
	}
	rootOpts := infra.RepoRootOptions{AllowJobDelete: o.allowJobDelete}
	if o.printVars {
		data, err := infra.RepoRootVars(spec, rootOpts)
		if err != nil {
			return err
		}
		// Printed without a cloud call, so without discovery or the
		// readiness gates: stdout stays the tfvars alone.
		fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+printVarsUngated)
		_, err = cmd.OutOrStdout().Write(data)
		return err
	}

	if bin == "" {
		if bin, err = r.checkEnv(lc); err != nil {
			return err
		}
	}
	inst, err := installOptions(o, lc)
	if err != nil {
		return err
	}
	c, err := newInitClients(ctx, lc)
	if err != nil {
		return err
	}
	if err := r.resourceManager(ctx, c); err != nil {
		return err
	}
	switch exists, err := infra.CheckStateBucket(ctx, c, lc.GCPProject, inst.StateBucket); {
	case err != nil:
		return initErr(err)
	case !exists:
		return noInstallationRefusal(inst.StateBucket)
	}
	slug, err := task.Slug(cfg.Git.Provider, repo)
	if err != nil {
		return userErr("%v", err)
	}
	rdir, err := infra.RepoWorkdir(os.Getenv, lc.GCPProject, slug)
	if err != nil {
		return userErr("%v", err)
	}
	wd, t, err := r.terraform(bin, rdir, "repo")
	if err != nil {
		return err
	}
	r.res.Workdir = wd.Dir
	backend, err := wd.WriteBackend(inst.StateBucket, infra.RepoStatePrefix(slug))
	if err != nil {
		return userErr("%v", err)
	}
	if o.forget {
		if err := r.forgetRepo(ctx, c, t, backend, repo, inst.StateBucket, slug); err != nil {
			return err
		}
		return finish()
	}

	// 2. The installation, and its outputs.
	switch ok, err := infra.InstallationStateExists(ctx, c, inst.StateBucket); {
	case err != nil:
		return initErr(err)
	case !ok:
		return userErr("gs://%s holds no installation state (%s/): run fugaro setup first", inst.StateBucket, infra.StatePrefixInstallation)
	}
	outs, err := r.installationOutputs(ctx, lc, bin, inst.StateBucket)
	if err != nil {
		return err
	}
	r.res.Outputs = &outs
	in.Installation = outs
	// The published node, read through the operator's own credentials. It is
	// the one source of whether this repository is proxied (G15), so it is
	// what the spec's bitbucket_proxied fact is read from, and what a local
	// project_secrets entry that disagrees with it is refused against
	// (internal/infra's resolveBitbucketProxied). Read here because
	// internal/infra holds no client that could read it and Repo is a pure
	// function of its Inputs.
	//
	// It is the same read planRepo makes for Readiness's D11 gate, and it is
	// memoized, so this moves that read earlier rather than adding one. A
	// read that FAILS is left to planRepo, which stops the run on the same
	// error — except for a node written by an older release, which planRepo
	// counts as present so the policy stage can migrate it: this side passes
	// nothing then too, until that stage has migrated the node. Passing
	// nothing is the one answer that judges no entry — never an empty node,
	// which would read a node the operator cannot reach as "nothing is
	// proxied" and refuse their entry over it.
	//
	// None of it is conditioned on --plan-only: a disagreement is a
	// contradiction in the configuration, not a change to make, and a plan
	// that cannot be applied is not a plan — so the refusal lands before the
	// plan is written, in every mode.
	p, found, err := r.policy.read(ctx, lc)
	in.Policy = repoPolicyInput(lc, p, found, err)
	if err := checkRepoProject(cfg.Project, outs.ProjectName); err != nil {
		return err
	}
	if w := baseProjectWarning(ctx, root, cfg.Git.BaseBranch, outs.ProjectName); w != "" {
		r.warn(w)
	}
	for _, w := range infra.BaseImageWarnings(lc.BaseImages, outs) {
		r.warn(w)
	}
	if spec, err = infra.Repo(in); err != nil {
		return initErr(err)
	}
	if err := t.Init(ctx, backend); err != nil {
		return remote(err)
	}

	// 3–5. Discover, gate, plan, guard, confirm, apply. ready is what the
	// apply just wrote, Readiness's own output: the first build below reads
	// its resolved secret IDs back out of it instead of resolving them
	// again (B1), since the grants the apply just made were fixed for them.
	ready, missing, err := r.planRepo(ctx, c, t, wd, spec, lc, rootOpts, true)
	if err != nil {
		return err
	}
	// ready, not spec: a name Readiness resolved to the project-wide copy
	// has a version the job mounts, and D2 made that the default store (see
	// infra.SecretVersions). Passing the pre-Readiness spec would leave
	// every workflow whose git credential was stored with `fugaro secrets
	// set github-app-key` unbuildable -- no first image build offered, no
	// job ever deployed -- and would print the repository-copy command for a
	// secret that already exists. (github-app-key, not bitbucket-token, is
	// the example: the App key is the one git credential D2's default sweeps
	// project-wide, while bitbucket-token has no project-wide default at all
	// -- config.PerRepositorySecret, the owner's ruling of 2026-10-15. An
	// operator holding one workspace or project access token stores it
	// project-wide by hand, through the allow-list; that is the exception,
	// not the default.)
	versions, err := infra.SecretVersions(ctx, c, ready)
	if err != nil {
		return r.retireLeftover(spec, initErr(err))
	}
	if !o.planOnly && !o.noBuild {
		// 6. The first image builds, then the plan that deploys them.
		names, err := infra.NeedsBuild(ctx, c, ready, versions)
		if err != nil {
			return r.retireLeftover(spec, initErr(err))
		}
		// Only re-resolved (a strict, bucket-reading read: resolveForBuild's
		// own doc comment) when a build is actually about to happen: the
		// common re-run with nothing left to build must not gain a new
		// failure mode (an unreachable project layer bucket) for a result
		// offerBuilds would never use anyway (buildImages returns at once
		// for an empty names).
		var buildCfg *config.Config
		if len(names) > 0 {
			if buildCfg, err = resolveForBuild(ctx, root, lc); err != nil {
				return r.retireLeftover(spec, err)
			}
		}
		built, err := r.offerBuilds(ctx, lc, buildCfg, ready, names)
		if err != nil {
			// The first apply already made the repository's resources, so
			// it joins the local config (and says what it still needs)
			// before the build's failure ends the run.
			r.printMissing(spec, versions, missing, needsAnthropicKey(lc, cfg, spec.Name))
			if cerr := r.writeRepoConfig(ctx, lc, spec, cfg, path, old); cerr != nil {
				r.warn(fmt.Sprintf("the local config was not updated: %v", cerr))
			}
			return r.retireLeftover(spec, err)
		}
		if built > 0 {
			if _, missing, err = r.planRepo(ctx, c, t, wd, spec, lc, rootOpts, false); err != nil {
				return r.retireLeftover(spec, err)
			}
		}
	}

	// 7. What is still missing.
	r.printMissing(spec, versions, missing, needsAnthropicKey(lc, cfg, spec.Name))
	if o.planOnly {
		return finish()
	}

	// 7b. The retirement's last step: the jobs the state removal left
	// unmanaged, deleted through the Cloud Run API after every apply, so the
	// re-plan that deploys freshly built images cannot race the delete.
	// A failed delete does not cost the run its local config (M4): the plan
	// did apply, the state did change, and the config file is the record of
	// what the deployment now is. The error is returned after step 8, so the
	// run still exits nonzero and still says what was left behind.
	var deleteErr error
	if r.retireApplied {
		deleteErr = r.retireDelete(ctx, c, spec)
	}

	// 8. The local config.
	if err := r.writeRepoConfig(ctx, lc, spec, cfg, path, old); err != nil {
		if deleteErr != nil {
			return remote(errors.Join(deleteErr, err))
		}
		return err
	}
	if deleteErr != nil {
		return deleteErr
	}
	if !embedded {
		// The converge's repository stage writes the line itself.
		r.noteAnchor(ctx, root, lc)
	}
	if len(r.buildsLeft) > 0 {
		// Everything else is done; the billable build is the user's to type.
		// (Step 7b already ran: the API delete is attempted before this, so
		// there is no residue left to name here.)
		left := billableBuildLeft(embedded, r.o.doorName())
		if r.buildHold != "" {
			// Not offered: the GitHub App is not ready (appPreCheck), which no
			// typing fixes.
			left = initflow.Left{Stage: initflow.Repository, Kind: initflow.LeftConsole, Text: "the first image build waits for the GitHub App: " + r.buildHold + "; then rerun", Commands: []string{selfCommand() + " " + r.o.doorName()}}
			if !embedded {
				left.Commands = []string{selfCommand() + " " + r.o.doorName() + " --repo"}
			}
		}
		if embedded {
			return &initflow.NeedsYouError{Left: left}
		}
		if err := finish(); err != nil {
			return err
		}
		return userErr("the first image build of %s is billable and needs the project's name typed at a real terminal: %s: %s", strings.Join(r.buildsLeft, ", "), left.Text, left.Commands[0])
	}
	return finish()
}

// offerBuilds is the first image builds: first the GitHub App pre-check, before
// anything billable is offered (an App that is not installed on the
// repository, or lacks a permission a run asks for, would only fail the build
// or the first run, after the charge: the builds are then left, with the
// reason, for the user), then each build's typed confirmation (buildImages).
func (r *initRun) offerBuilds(ctx context.Context, lc *localcfg.Config, cfg *config.Config, spec infra.RepoSpec, names []string) (int, error) {
	if len(names) > 0 {
		hold := r.appPreCheck(ctx, lc, spec)
		// The typed key is not needed again: its byte copy is cleared now, not
		// at the run's end.
		clear(r.appKeyMem)
		r.appKeyMem = nil
		if hold != "" {
			r.buildsLeft, r.buildHold = append(r.buildsLeft, names...), hold
			return 0, nil
		}
	}
	return r.buildImages(ctx, lc, cfg, spec, names)
}

// appPreCheck runs the GitHub App pre-check (githubapp.go) for a GitHub
// repository whose first image build is about to be offered. It returns why
// the build must wait ("" to go on): the App is not installed on the
// repository, or the installation lacks a permission Fugaro's tokens ask for,
// each named with its fix. A check that could not be made (no key to read, no
// access, GitHub unreachable) is a warning that does not claim the App is
// fine, and the build is offered as before.
//
// The secret this check reads is the one live state resolves for
// github-app-key, not the repository's own ID unconditionally (small item 5:
// this is the fifth call site that named the repo-scoped ID, doctor's own
// GitHub App check being the fourth). It is resolved live and compared with
// the build account's own grant first (B1), the identical comparison a
// standalone fugaro image build makes: a grant naming a different ID means
// this check would sign with a key the build's clone would not, a misleading
// diagnostic rather than an access error, and the build is left with that
// reason instead of being offered on a wrong key.
//
// Only behind --check-github-app, though: that is the one path that reaches
// Secret Manager at all (a key typed at this run's own secrets stage is
// signed with from memory), so it is the only one a resolved ID decides
// anything for.
func (r *initRun) appPreCheck(ctx context.Context, lc *localcfg.Config, spec infra.RepoSpec) string {
	if spec.Provider != gitprov.KindGitHub || spec.GitHubAppID == "" {
		return ""
	}
	keyID := spec.Secrets[config.GitHubAppKeySecret]
	if r.o.checkApp {
		id, err := gitSecretID(ctx, lc, spec, "the GitHub App pre-check of "+spec.Name)
		if err != nil {
			// One line, and for a drift the short form of it: oneLineCLI
			// prints through pluginwire.Printable, which would cut the
			// granted ID out of the full refusal (driftError.short).
			why := oneLineCLI(err.Error())
			fix := ""
			var drift *driftError
			if errors.As(err, &drift) {
				why = drift.short()
				fix = "\n  fix: run " + selfCommand() + " " + r.o.doorName() + " --repo " + spec.Name + " in its checkout, so the mounts and the grants agree again"
			}
			fmt.Fprintf(r.w, "GitHub App check for %s was not made: %s%s\n  The first image build is not offered until this is fixed (nothing was billed).\n", spec.Name, why, fix)
			return why
		}
		keyID = id
	}
	c := checkGitHubApp(ctx, lc, spec.Name, spec.GitHubAppID, appKeySource{Mem: r.appKeyMem, Read: r.o.checkApp, SecretID: keyID, Notice: r.w})
	for _, w := range c.Warnings {
		r.warn(w)
	}
	switch c.Verdict {
	case appUnknown:
		r.warn(c.Problem)
	case appFailed:
		fmt.Fprintf(r.w, "GitHub App check for %s failed: %s\n  fix: %s\n  The first image build is not offered until this is fixed (nothing was billed).\n", spec.Name, c.Problem, c.Fix)
		return c.Problem + ": " + c.Fix
	}
	return ""
}

// requireCheckoutProject refuses, before any cloud call, a checkout whose
// fugaro.yaml has no project:, naming what to add when a project config is
// selectable.
func (o *initOptions) requireCheckoutProject(ctx context.Context, dir string) error {
	co, err := checkoutProject(ctx, dir)
	if err != nil || co == nil || co.Project != "" {
		return err
	}
	name := "<name>"
	if _, lc, err := selectFrom(o.cloud, nil, false); err == nil && lc != nil {
		name = lc.Name
	}
	return userErr("fugaro.yaml has no `project:`; add `project: %s` (fugaro config example shows it)", name)
}

// vertexBudgetRefusal is the refusal of a Vertex workflow under a budget
// that enforces: the gateway can't yet be relied on to cap Vertex spend.
const vertexBudgetRefusal = runner.VertexBudgetRefusal

// gateCatalogSwitches is repoEngine's route-switch version gate (price-catalog
// plan Task 4 step 2): refuseCatalogSwitches, unless there is nothing to
// refuse it for — --forget tears the repository down, and --plan-only shows
// the plan without applying it (the same escape planRepo's clone refusal
// leaves). --print-vars is judged like a deploy would be, since the values it
// prints are the ones a deploy would use, and its warnings go to stderr so
// stdout stays the Terraform variables alone.
func (r *initRun) gateCatalogSwitches(o *initOptions, repo string, lc *localcfg.Config) error {
	if o.forget || o.planOnly {
		return nil
	}
	w := r.w
	if o.printVars {
		w = r.cmd.ErrOrStderr()
	}
	return refuseCatalogSwitches(repo, lc, w)
}

// gateCascadeAuth is repoEngine's version gate for agent.auth: cascade
// (Task C1.1's skew): refuseCascadeSkew, unless there is nothing to refuse it
// for — --forget tears the repository down, and --plan-only shows the plan
// without applying it (the same escape gateCatalogSwitches leaves).
// --print-vars is judged like a deploy would be, since the values it prints
// are the ones a deploy would use, and its warnings go to stderr so stdout
// stays the Terraform variables alone.
//
// authSource is the layer the resolved auth came from
// (config.Resolution.SourceOf("agent.auth")): the value is what the gate
// judges, and where it came from is what its messages name, so a layer's
// default is not reported as the repository's own line.
func (r *initRun) gateCascadeAuth(o *initOptions, repo string, lc *localcfg.Config, cfg *config.Config, authSource string) error {
	if o.forget || o.planOnly {
		return nil
	}
	w := r.w
	if o.printVars {
		w = r.cmd.ErrOrStderr()
	}
	auth := ""
	if cfg != nil {
		auth = cfg.Agent.Auth
	}
	return refuseCascadeSkew(repo, auth, authSource, lc.BaseImage(config.BaseKind), w)
}

// gateRigor is fugaro update --repo's gate for rigorSince: refuseRigorSkew,
// with the same escapes as gateCascadeAuth (--forget and --plan-only deploy
// nothing; --print-vars is judged, its warnings on stderr). The repository's
// half is RepoRigorSet rather than RepoRigor != nil: an explicit null
// (`rigor:`) writes the key and decodes to no block, and a runner older than
// rigorSince refuses the key, so a nil-pointer test would deploy a job that
// dies at bootstrap on a line the operator did write.
func (r *initRun) gateRigor(o *initOptions, repo string, lc *localcfg.Config, cfg *config.Config) error {
	if o.forget || o.planOnly || cfg == nil {
		return nil
	}
	w := r.w
	if o.printVars {
		w = r.cmd.ErrOrStderr()
	}
	return refuseRigorSkew(repo, cfg.RepoRigorSet, cfg.Layer.SetsRigor(), lc.BaseImage(config.BaseKind), w)
}

// gateGovernance is fugaro update --repo's gate for governanceSince:
// refuseGovernanceSkew, with gateRigor's escapes and for its reason — the
// repository's half is the key being written (RepoGovernanceSet) rather than
// the block being set, because an explicit null (`governance:`) is a line an
// older runner's KnownFields decoder refuses too.
func (r *initRun) gateGovernance(o *initOptions, repo string, lc *localcfg.Config, cfg *config.Config) error {
	if o.forget || o.planOnly || cfg == nil {
		return nil
	}
	w := r.w
	if o.printVars {
		w = r.cmd.ErrOrStderr()
	}
	return refuseGovernanceSkew(repo, cfg.RepoGovernanceSet, cfg.Layer.SetsGovernance(), lc.BaseImage(config.BaseKind), w)
}

// gateGitAuthor is fugaro update --repo's gate for gitAuthorSince:
// refuseGitAuthorSkew, with gateRigor's escapes and for its reason. The
// repository's half is the key being written (RepoGitAuthorSet) rather than the
// value it holds: a runner older than gitAuthorSince refuses git.author
// whatever it says, so git.author: fugaro and an explicit null are deployed by
// a value test and die at bootstrap. And it is only ever the repository's half:
// config.Scopes gives git.author InRepo alone, so a layer that wrote it was
// refused for being out of scope long before this.
func (r *initRun) gateGitAuthor(o *initOptions, repo string, lc *localcfg.Config, cfg *config.Config) error {
	if o.forget || o.planOnly || cfg == nil {
		return nil
	}
	w := r.w
	if o.printVars {
		w = r.cmd.ErrOrStderr()
	}
	return refuseGitAuthorSkew(repo, cfg.RepoGitAuthorSet, lc.BaseImage(config.BaseKind), w)
}

// checkVertexBudget refuses a repository whose fugaro.yaml authenticates
// the agent through Vertex AI (agent.auth: vertex) under a project budget
// in enforce mode. observe and off are allowed.
func checkVertexBudget(lc *localcfg.Config, cfg *config.Config) error {
	if cfg.Agent.Auth != "vertex" {
		return nil
	}
	// The ceiling's mode and the file's, the stricter of the two, as the
	// runner merges them.
	if policy.Merge(ceilingLayer(lc), runner.FileLayer(cfg)).Mode == policy.ModeEnforce {
		return userErr("%s", vertexBudgetRefusal)
	}
	return nil
}

// printVarsUngated is update --repo --print-vars's warning: the values it
// prints skip discovery and the readiness gates.
const printVarsUngated = "these values are ungated: no discovery or readiness check ran, so every workflow has deploy_job = true (and so does the check), " +
	"its job uses the new image path (which may not be built yet), and its account gets the new display name " +
	"(an adopted bootstrap account would be renamed); adopt a repository through fugaro update --repo, not by applying these"

// printVarsUngatedInstallation is fugaro setup --print-vars's warning: the
// values it prints skip discovery.
const printVarsUngatedInstallation = "these values are ungated: no discovery ran, so adopt_legacy_registry is false even when the bootstrap's legacy registry exists and is ours " +
	"(a plan from these values would try to create it, and nothing imports what exists); set up the installation through fugaro setup, not by applying these"

// loadRepoConfig selects the project config the first run wrote, with
// --gcp-project and --region applied. The checkout that selects it is
// dir's, the one being onboarded, not the working directory's.
func loadRepoConfig(ctx context.Context, o *initOptions, dir string) (lc *localcfg.Config, path string, old []byte, err error) {
	co, err := checkoutProject(ctx, dir)
	if err != nil {
		return nil, "", nil, err
	}
	cloud := o.cloud
	cloud.originDir = dir // the origin that selects is the checkout's, not the working directory's
	sel, lc, err := selectFrom(cloud, co, false)
	if err != nil {
		return nil, "", nil, err
	}
	if err := refuseSharedWrite(sel); err != nil {
		return nil, "", nil, err
	}
	if err := announce(o.cloud, sel, lc); err != nil {
		return nil, "", nil, err
	}
	if o.name != "" && o.name != lc.Name {
		return nil, "", nil, userErr("--name %s is not the selected project %s", o.name, lc.Name)
	}
	if old, err = os.ReadFile(sel.Path); err != nil {
		return nil, "", nil, userErr("reading the project config: %v", err)
	}
	return lc, sel.Path, old, nil
}

// checkoutRepo returns the checkout's owner/name, from its origin.
func checkoutRepo(ctx context.Context, root string) (string, error) {
	cmd := gitCmd(ctx, root, "remote", "get-url", "origin")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", &ExitError{Code: ExitUserError, Err: fmt.Errorf("reading the checkout's origin: %w: %s", err, strings.TrimSpace(stderr.String()))}
	}
	repo, ok := repoFromOrigin(strings.TrimSpace(string(out)))
	if !ok {
		return "", &ExitError{Code: ExitUserError, Err: errors.New("cannot tell the repository (owner/name) from the checkout's origin")}
	}
	return repo, nil
}

// checkoutURL is the checkout's origin as an https URL without
// credentials, which the builds and the daily check clone.
func checkoutURL(ctx context.Context, root string) (string, error) {
	out, err := gitCmd(ctx, root, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", userErr("no origin remote in the checkout %s", root)
	}
	u := image.HTTPSOrigin(strings.TrimSpace(string(out)))
	if !strings.HasPrefix(u, "https://") {
		return "", userErr("origin %s has no https form to clone from", gcp.RedactURL(u))
	}
	return u, nil
}

// repoPolicyInput is infra.Inputs.Policy from the memoized node read: nil when
// no node was read — the read failed, or the config names no database address
// for projectPolicyRead to read (which answers not-found then, the same as a
// project that published nothing) — and an empty node when the read succeeded
// and the project has published no policy. Only the second refuses a local
// entry that proxies a repository the node does not admit: an operator whose
// config has lost budget.rtdb_url has read no node, and the node they cannot
// reach must not be taken for one that proxies nothing.
func repoPolicyInput(lc *localcfg.Config, p *projectpolicy.Policy, found bool, err error) *projectpolicy.Policy {
	switch {
	case err != nil, lc.Budget == nil || lc.Budget.RTDBURL == "":
		return nil
	case !found:
		return &projectpolicy.Policy{}
	}
	return p
}

// installationOutputs reads the installation root's outputs from its
// state, which is the repository root's input. It runs in a workdir of its
// own, leaving the installation's workdir as it is.
func (r *initRun) installationOutputs(ctx context.Context, lc *localcfg.Config, bin, stateBucket string) (infra.InstallationOutputs, error) {
	dir, err := infra.InstallationOutputsWorkdir(os.Getenv, lc.GCPProject)
	if err != nil {
		return infra.InstallationOutputs{}, userErr("%v", err)
	}
	wd, t, err := r.terraform(bin, dir, "installation")
	if err != nil {
		return infra.InstallationOutputs{}, err
	}
	backend, err := wd.WriteBackend(stateBucket, infra.StatePrefixInstallation)
	if err != nil {
		return infra.InstallationOutputs{}, userErr("%v", err)
	}
	if err := t.Init(ctx, backend); err != nil {
		return infra.InstallationOutputs{}, remote(err)
	}
	// The rollback's state rm leaves the root's outputs behind, so outputs
	// alone don't say the installation is managed: its resources do.
	st, err := t.ShowState(ctx)
	if err != nil {
		return infra.InstallationOutputs{}, remote(err)
	}
	if !st.Managed() {
		return infra.InstallationOutputs{}, userErr("the installation's state in gs://%s manages nothing (the installation was forgotten, by fugaro update --forget): run fugaro setup first", stateBucket)
	}
	raw, err := t.Output(ctx)
	if err != nil {
		return infra.InstallationOutputs{}, remote(err)
	}
	outs, err := infra.DecodeOutputs(raw)
	switch {
	case errors.Is(err, infra.ErrNoOutputs):
		return infra.InstallationOutputs{}, userErr("the installation's state in gs://%s has no outputs: run fugaro setup first", stateBucket)
	case err != nil:
		return infra.InstallationOutputs{}, remote(err)
	}
	// Every job carries the project's name, which only the installation's
	// outputs give: setup --name names an installation first.
	if outs.ProjectName == "" {
		return infra.InstallationOutputs{}, userErr("the installation has no project name; an operator runs fugaro setup --name %s first (see docs/design/m9-budget-and-dashboard.md §13.1)", lc.Name)
	}
	// An installation applied before the tag mover role existed doesn't
	// output it; the repository's grant of it would fail at apply.
	if outs.RoleIDs.TagMover == "" {
		return infra.InstallationOutputs{}, userErr("the installation's state in gs://%s predates the tag mover role (its outputs have no role_ids.tag_mover): run fugaro setup first", stateBucket)
	}
	return outs, nil
}

// planRepo is one discover, gate, plan, guard, confirm and apply of the
// repository root. It returns the spec Readiness resolved (ready: what the
// apply below just wrote, whose resolved secret IDs the caller's first
// builds read back instead of resolving them again, B1) and what the
// readiness gates found missing. first says whether this is the run's first
// plan, whose notes are shown.
//
// Before any of that (review finding, 0.7.0 safety fix round 2): a
// checkout: clone workflow's job must never come to exist while the gate
// is shut, because then no fugaro release could run one — so this refused
// outright, before any cloud call, rather than letting Terraform create a
// job checkCloneImageSince would only catch later, at launch, and only if
// the launch still decides clone from this same checkout (run.go's
// checkoutConfig does not, once the job is already deployed). cloneShipped
// has been true since 0.9.0 shipped the flip, so the call below is a no-op
// on every spec today; it and its reason stay for the flip-back the
// constant's comment describes. --plan-only skipped the refusal so the
// plan could still be shown; --print-vars never reaches planRepo at all.
func (r *initRun) planRepo(ctx context.Context, c *infra.Clients, t *tf.TF, wd *infra.Workdir, spec infra.RepoSpec, lc *localcfg.Config, opts infra.RepoRootOptions, first bool) (infra.RepoSpec, []infra.Missing, error) {
	if !r.o.planOnly {
		if err := refuseClonesNotShipped(spec.Name, selfCommand()+" "+r.o.doorName()+" --repo", "deploy", cloneWorkflowNames(spec.Workflows)); err != nil {
			return infra.RepoSpec{}, nil, err
		}
	}
	im, ex, err := infra.DiscoverRepo(ctx, c, spec)
	if err != nil {
		return infra.RepoSpec{}, nil, initErr(err)
	}
	// Whether the project has published a policy decides whether the jobs
	// must be able to read one (D11): the database is read with the
	// operator's own credentials, which only this side holds.
	if ex.PolicyExists, err = r.policy.exists(ctx, lc); err != nil {
		return infra.RepoSpec{}, nil, initErr(err)
	}
	if first {
		for _, n := range im.Notes {
			r.warn(n)
		}
	}
	ready, missing, err := infra.Readiness(ctx, c, spec, ex, r.o.planOnly)
	if err != nil {
		return infra.RepoSpec{}, nil, initErr(err)
	}
	if err := r.checkCheckImageKnowsGitSecretID(ready); err != nil {
		return infra.RepoSpec{}, nil, err
	}
	vars, err := infra.RepoRootVars(ready, opts)
	if err != nil {
		return infra.RepoSpec{}, nil, err
	}
	if err := wd.WriteVars(vars); err != nil {
		return infra.RepoSpec{}, nil, userErr("writing the tfvars: %v", err)
	}
	if err := infra.WriteImports(wd.Root, im); err != nil {
		return infra.RepoSpec{}, nil, userErr("writing the imports: %v", err)
	}
	// The retirement, read once, from the run's first plan (design §3.1a,
	// §4.2): the state, the labels, the union, the summary -- and, with
	// --retire, the whole guarded flow in place of the ordinary plan below.
	// It has to be here, before t.Plan: in the case that matters no plan
	// exists until the protected addresses have left the state.
	// rets outlives the block: a plan that aborts on prevent_destroy is
	// told whether the aborting address is one of them (retireAborted).
	var rets []infra.Retirement
	if first && !r.retired {
		var err error
		rets, err = r.readRetirements(ctx, c, t, ready)
		if err != nil {
			return infra.RepoSpec{}, nil, err
		}
		if len(rets) > 0 {
			r.retired = true
			if r.o.retire {
				if err := r.retirePlan(ctx, c, t, wd, ready, rets, opts, im); err != nil {
					return infra.RepoSpec{}, nil, err
				}
				return ready, missing, nil
			}
			// The gate, and it is the two cases the design names and no
			// more: a job the state still holds whose own values say
			// deletion_protection = true, after its variant left the
			// configuration, cannot be finished by any apply. --plan-only
			// removes nothing and refuses nothing here: it prints the
			// summary and carries on to the read-only plan.
			if blocked := blockingJobs(r.retireState, rets); len(blocked) > 0 && !r.o.planOnly {
				return infra.RepoSpec{}, nil, retireRefusal(blocked)
			}
		}
	}
	changed, err := t.Plan(ctx, infra.PlanFile)
	if err != nil {
		// A plan Terraform refuses on a prevent_destroy resource is the one
		// failure a retirement gets its own words for (design §3.3): the
		// summary was printed above, and Terraform's text stays in the
		// workdir, so nothing is hidden, only re-framed.
		if preventDestroyAbort(err) {
			return infra.RepoSpec{}, nil, retireAborted(rets, preventDestroyAddress(err), wd.Root)
		}
		return infra.RepoSpec{}, nil, remote(err)
	}
	if !changed {
		r.res.Changes = &infra.PlanCounts{}
		fmt.Fprintf(r.w, "No changes: %s matches the plan.\n", spec.Name)
		return ready, missing, nil
	}
	plan, err := t.Show(ctx, infra.PlanFile)
	if err != nil {
		return infra.RepoSpec{}, nil, remote(err)
	}
	if opts.AllowJobDelete {
		fmt.Fprintln(r.w, "⚠ --allow-job-delete: this plan lowers the deletion protection of "+spec.Name+"'s jobs, for offboarding")
	}
	fmt.Fprint(r.w, tf.Summary(plan))
	fmt.Fprintln(r.w, im.Bindings.String())
	if err := r.guard(ctx, c, plan, r.o.allowDelete); err != nil {
		return infra.RepoSpec{}, nil, err
	}
	counts := infra.CountPlan(plan)
	r.res.Changes = &counts
	if r.o.planOnly {
		fmt.Fprintf(r.w, "--plan-only: nothing applied; the plan is %s\n", filepath.Join(wd.Root, infra.PlanFile))
		return ready, missing, nil
	}
	what := "applies " + counts.String() + " to " + spec.Name
	if !first {
		what += ", deploying the images just built"
	}
	if err := r.confirmOrdinary(what, "nothing was applied", r.notCovered(plan, im.Keys())); err != nil {
		return infra.RepoSpec{}, nil, err
	}
	if err := t.Apply(ctx, infra.PlanFile); err != nil {
		return infra.RepoSpec{}, nil, remote(err)
	}
	r.res.Applied = true
	return ready, missing, nil
}

// checkCheckImageKnowsGitSecretID is B2's gate: the daily image check job
// reads its own spec's git_secret_id (infra.CheckJobSpec) to name the secret
// its rebuild's clone reads, and a fugaro older than gitSecretIDSince
// ignores that field and falls back to the repository's own ID -- which,
// once the provider credential resolves from the project copy instead, is a
// secret with no version at all, so every rebuild the check submits would
// fail its clone.
//
// The judge is ready.Check.Image, the check job's own base ref: that job is
// never built from a repository image, so unlike every other gate in
// recipes_skew.go there is no build record to read (B2's own correction --
// an earlier draft judged the record of the image the submitting CLI built,
// which is a different, irrelevant version). It reuses baseRefReleaseRE and
// imagePredates, the same pair baseRefReason already calls for
// gcpProjectFieldSince.
//
// It fires only when the provider credential resolved to the project copy:
// with the repository's own copy resolving, the in-job fallback names the
// right secret anyway. A base ref that is not a release at all (a dev build,
// a hand-pushed image, --image-source without a release tag) is a warning,
// never a refusal: the version is genuinely unknown, the identical
// distinction baseRefReason draws between reasonBaseRefOld and
// reasonBaseRefDev.
func (r *initRun) checkCheckImageKnowsGitSecretID(ready infra.RepoSpec) error {
	logical, ok := infra.GitSecret(ready.Provider)
	if !ok || ready.Check == nil || ready.ProjectSecrets[logical] == "" || ready.Check.Image == "" {
		return nil
	}
	ref := ready.Check.Image
	m := baseRefReleaseRE.FindStringSubmatch(ref)
	switch {
	case m == nil:
		r.warn(fmt.Sprintf("the daily image check job of %s runs %s, which is not a release base image: whether its fugaro knows the resolved secret ID of %s (fugaro %s or later) is not checked, and an older one would rebuild with the repository's own %s, which has no version", ready.Name, oneLine(ref), logical, gitSecretIDSince, logical))
	case imagePredates(m[2], gitSecretIDSince):
		return userErr("the daily image check job of %s runs base image %s, release %s, whose fugaro predates %s's project-wide resolution (fugaro %s or later): its rebuilds would fall back to the repository's own %s, which has no version. Move the base image to a current release (set base_images.base in the local config, or fugaro update --base base), or re-enable a version of the repository's own %s (fugaro secrets set %s --repo %s) so the resolution falls back to it",
			ready.Name, oneLine(ref), m[2], logical, gitSecretIDSince, logical, logical, logical, ready.Name)
	}
	return nil
}

// cloneWorkflowNames are the distinct workflow names among ws whose
// resolved checkout is clone, sorted. ws may hold one WorkflowSpec per
// Claude account variant of the same fugaro.yaml workflow
// (gcp.WorkflowVariant, keyed by the variant, not the plain name), so this
// dedupes by WorkflowSpec.Name rather than the map key.
func cloneWorkflowNames(ws map[string]infra.WorkflowSpec) []string {
	seen := map[string]bool{}
	for _, w := range ws {
		if w.Checkout == config.CheckoutClone {
			seen[w.Name] = true
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// cloudBuilder is the Cloud Build client the builds need. Tests replace
// newCloudBuilder.
type cloudBuilder interface {
	Submit(ctx context.Context, s gcp.BuildSpec) (gcp.BuildResult, error)
	Wait(ctx context.Context, id string, poll time.Duration) (gcp.BuildResult, error)
}

var newCloudBuilder = func(ctx context.Context, lc *localcfg.Config) (cloudBuilder, error) {
	b, err := gcp.NewBuilder(ctx, gcpOptions(lc), lc.BuildRegion())
	if err != nil {
		return nil, err
	}
	return b, nil
}

// submitAndWait submits workflow name's Cloud Build from base and waits for
// it: the one build the converge's first builds and fugaro image refresh run,
// each after its caller's typed confirmation. gitSecretID is the provider
// credential's ID the build's clone reads, which the caller decides: init's
// first builds read it back out of the spec the apply just ran (B1), while a
// standalone image refresh resolves it live and checks it against the build
// account's own grant. It returns the build's ID, also when the wait fails after the submit.
func (r *initRun) submitAndWait(ctx context.Context, b cloudBuilder, lc *localcfg.Config, cfg *config.Config, spec infra.RepoSpec, name, base, gitSecretID string) (string, error) {
	bs, err := cloudBuildSpec(spec, cfg, name, base, lc.Build.MachineType, lc.RecordBucketURL(), gitSecretID)
	if err != nil {
		return "", err
	}
	if cfg.Layer != nil {
		rb, err := openRecordBucket(ctx, lc.RecordBucketURL())
		if err != nil {
			return "", remote(err)
		}
		defer rb.Close()
		if bs.ProjectLayerSHA256, err = prepareLayerCopy(ctx, rb, spec.Slug, cfg.Layer, base); err != nil {
			return "", err
		}
	}
	res, err := b.Submit(ctx, bs)
	switch {
	case errors.Is(err, gcp.ErrBadBuildSpec):
		return "", userErr("%v", err)
	case err != nil:
		return "", remote(err)
	}
	fmt.Fprintf(r.w, "Cloud Build build %s of %s submitted; log: %s\n", oneLine(res.ID), oneLine(res.Image), oneLine(res.LogURL))
	done, err := b.Wait(ctx, res.ID, 0)
	if err != nil {
		// The build was submitted and goes on without us: its ID comes back
		// with the error.
		return res.ID, remote(err)
	}
	fmt.Fprintf(r.w, "built %s/%s (Cloud Build build %s)\n", spec.Name, name, oneLine(done.ID))
	r.res.Builds = append(r.res.Builds, done.ID)
	return done.ID, nil
}

// buildImages offers the first image build of each workflow in names, and
// submits and waits for each one confirmed. It returns how many were built.
func (r *initRun) buildImages(ctx context.Context, lc *localcfg.Config, cfg *config.Config, spec infra.RepoSpec, names []string) (int, error) {
	if len(names) == 0 {
		return 0, nil
	}
	b, err := newCloudBuilder(ctx, lc)
	if err != nil {
		return 0, remote(err)
	}
	built := 0
	for _, name := range names {
		base := lc.BaseImage(cfg.Workflows[name].BaseKind())
		if base == "" {
			if base, err = image.BaseRef(Version); err != nil {
				return built, userErr("%v", err)
			}
		}
		// Billable: the typed confirmation of a real terminal, or --yes —
		// covered in both front doors (askCovered: D12's answer of 2026-10-20
		// keeps only three steps typed, and this is not one of them). A run
		// that cannot take it leaves the build.
		ok, reachable, err := r.askCovered(cloudBuildBanner(spec.Name, name, lc.Build.MachineType, spec.BuildServiceAccountEmail, spec.RegistryPath))
		if err != nil {
			return built, err
		}
		if !reachable {
			r.buildsLeft = append(r.buildsLeft, name)
			r.warn(fmt.Sprintf("the first image build of %s/%s is billable and was not confirmed: it needs the project's name typed at a real terminal ("+neverConfirmsBuild(r.conditions().Agent != "")+"), so its job waits for it: in your own terminal window, run fugaro image build --repo %s --workflow %s (it asks for the project's name too), then "+r.o.doorProgram()+" --repo", spec.Name, name, spec.Name, name))
			continue
		}
		if !ok {
			r.warn(fmt.Sprintf("the first image build of %s/%s was not confirmed (the project's name was not typed), so its job waits for it: in your own terminal window, run fugaro image build --repo %s --workflow %s (it asks for the project's name too), then "+r.o.doorProgram()+" --repo", spec.Name, name, spec.Name, name))
			continue
		}
		if _, err := r.submitAndWait(ctx, b, lc, cfg, spec, name, base, appliedGitSecretID(spec)); err != nil {
			return built, err
		}
		built++
	}
	return built, nil
}

// printMissing prints what the repository still needs: each secret's fugaro
// secrets set command, as the bootstrap printed them, and each workflow's
// other gates.
func (r *initRun) printMissing(spec infra.RepoSpec, versions map[string]bool, missing []infra.Missing, needKey bool) {
	steps := infra.SecretCommands(spec, versions, selfCommand(), quoteWord)
	if !needKey {
		// This run never calls Anthropic, so the key is not missing: it gates
		// nothing (infra.NeedsBuild skips it), the job deploys without it, and
		// nothing about it may be listed as missing -- not here, and not in
		// the --json result, which is the same list. The rule is the one
		// init's own secrets stage asks (needsAnthropicKey).
		steps = slices.DeleteFunc(steps, func(st infra.SecretStep) bool { return st.Optional })
	}
	var other []string
	for _, m := range missing {
		// Secrets are listed as commands below.
		if m.Kind != infra.MissingSecret {
			other = append(other, m.String())
		}
	}
	if len(steps) == 0 && len(other) == 0 {
		return
	}
	fmt.Fprintf(r.w, "Still missing for %s:\n", spec.Name)
	for _, o := range other {
		fmt.Fprintln(r.w, "  "+o)
	}
	var cmds []string
	if len(steps) > 0 {
		fmt.Fprintln(r.w, "  Store each secret with its commands, run in your own terminal window one line at a time; the value comes from stdin or a hidden prompt, never argv:")
		for _, st := range steps {
			fmt.Fprintf(r.w, "  %s: %s\n", st.Name, oneLine(st.What))
			for _, c := range st.Commands {
				fmt.Fprintln(r.w, "    "+c)
				cmds = append(cmds, c)
			}
		}
		fmt.Fprintln(r.w, "  Then rerun "+selfCommand()+" "+r.o.doorName()+" --repo when they are stored.")
	}
	r.res.Missing = append(other, cmds...)
}

// writeRepoConfig adds the repository to the local config's repos, or
// updates its entry, keeping everything else.
func (r *initRun) writeRepoConfig(ctx context.Context, lc *localcfg.Config, spec infra.RepoSpec, cfg *config.Config, path string, old []byte) error {
	next := *lc
	next.Repos = maps.Clone(lc.Repos)
	if next.Repos == nil {
		next.Repos = map[string]localcfg.Repo{}
	}
	key := spec.Name
	if want, err := task.CanonicalRepo(spec.Name); err == nil {
		for _, k := range slices.Sorted(maps.Keys(lc.Repos)) {
			if c, err := task.CanonicalRepo(k); err == nil && c == want {
				key = k
				break
			}
		}
	}
	next.Repos[key] = localcfg.Repo{
		Provider:    spec.Provider,
		BaseBranch:  spec.BaseBranch,
		Workflows:   slices.Sorted(maps.Keys(cfg.Workflows)),
		GitHubAppID: spec.GitHubAppID,
		Vertex:      spec.UsesVertex(),
	}
	if spec.UsesVertex() && !lc.UsesVertex() {
		// The installation enables the API from the recorded repositories,
		// and this is the first.
		r.warn(spec.Name + " authenticates its agent through Vertex AI, which the installation has not enabled: rerun " + r.o.doorProgram() + " once the local config records it, which enables the Vertex AI API")
	}
	if err := r.writeLocalConfig(&next, path, old, r.res.Applied); err != nil {
		return err
	}
	if !r.o.planOnly {
		r.publishSharedConfig(ctx, &next)
	}
	return nil
}

// forgetRepo is the repository's rollback: every address leaves
// Terraform's state, after a confirmation, and nothing is destroyed. The
// emptied state object goes too (the state bucket must be versioned, so it
// can be restored), so the installation's rollback no longer counts the
// repository. It decides from the state's resources, not its outputs
// (state rm leaves those), so a run that stopped after state rm finishes
// on the next one.
func (r *initRun) forgetRepo(ctx context.Context, c *infra.Clients, t *tf.TF, backend map[string]string, repo, stateBucket, slug string) error {
	if err := t.Init(ctx, backend); err != nil {
		return remote(err)
	}
	st, err := t.ShowState(ctx)
	if err != nil {
		return remote(err)
	}
	managed := st.Managed()
	objects, err := infra.RepoStateObjects(ctx, c, stateBucket, slug)
	if err != nil {
		return remote(err)
	}
	where := "gs://" + stateBucket + "/" + infra.RepoStatePrefix(slug) + "/"
	if !managed && len(objects) == 0 {
		return userErr("the state under %s holds nothing of %s, so there is nothing to forget", where, repo)
	}
	if len(objects) > 0 {
		switch versioned, err := infra.StateBucketVersioned(ctx, c, stateBucket); {
		case err != nil:
			return remote(err)
		case !versioned:
			return userErr("gs://%s has no object versioning, so the state object of %s could not be restored once deleted; turn versioning on (gcloud storage buckets update gs://%s --versioning) and rerun; nothing was forgotten",
				stateBucket, repo, stateBucket)
		}
	}
	what := fmt.Sprintf("removes every address of %s from Terraform's state (terraform state rm module.repo), then deletes its state object under %s (kept as a noncurrent version); it destroys nothing", repo, where)
	if !managed {
		what = fmt.Sprintf("the state of %s manages nothing any more (an earlier --forget stopped after state rm): deletes its state object under %s (kept as a noncurrent version); it destroys nothing", repo, where)
	}
	if err := r.confirm(what, "nothing was forgotten"); err != nil {
		return err
	}
	if managed {
		if err := t.StateRm(ctx, "module.repo"); err != nil {
			return remote(err)
		}
	}
	deleted, err := infra.ForgetRepoState(ctx, c, stateBucket, slug)
	if err != nil {
		return remote(fmt.Errorf("%w; Terraform no longer manages %s, so rerun fugaro update --repo --forget to finish", err, repo))
	}
	r.res.Forgotten = true
	r.res.Deleted = deleted
	fmt.Fprintf(r.w, "Terraform no longer manages %s; nothing was destroyed. Its jobs, accounts, secrets and grants stay as they are.\n", repo)
	return nil
}

// printCeiling says what update --repo is about to apply as the repository's
// ceiling (the project config's budget) and, when the checkout's fugaro.yaml
// has a budget block, which of its keys the ceiling would clamp.
func printCeiling(w io.Writer, lc *localcfg.Config, cfg *config.Config) {
	budget := ceilingLayer(lc)
	if !reflect.DeepEqual(budget, policy.Layer{}) {
		fmt.Fprintf(w, "Budget ceiling for %s from project %s: %s\n", cfg.Project, lc.Name, ceilingText(budget))
	}
	// The gate is the file's own policy, governance included: a file whose only
	// policy is a governance: block is one the layer's governance can clamp, and
	// setsPolicy knows only the budget keys. The line above is the budget's
	// alone, though: a project whose layer carries governance and no budget has
	// no ceiling to state, and "none (no cap, ...)" would read as one.
	if !setsPolicy(cfg) && cfg.Governance == nil {
		return
	}
	// The ceiling the merge is held against is the run's own (runCeiling),
	// governance included: the project layer's block with Fugaro's figures
	// filled in, so a governance value this file writes looser than the run's
	// ceiling is one the run clamps and is named here too. The line above
	// stays the budget's — ceilingText says nothing about governance, and a
	// project with no budget has no ceiling to announce.
	for _, ig := range policy.Merge(runCeiling(lc, cfg), runner.FileLayer(cfg)).Ignored {
		p := clampWarning(ig)
		fmt.Fprintf(w, "  fugaro.yaml %s: %s\n", p.Path, p.Message)
	}
}
