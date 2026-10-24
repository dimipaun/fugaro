package cli

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/spf13/cobra"
)

// setup.go is `fugaro setup`: the first door of the converge, and the one
// that asks (task W1.2 of docs/setup-wizard/implementation-plan.md; the
// wizard's questions and its real stage set are W1.3's).
//
// setup and update are two commands over one engine — init.go's
// newInitCmdWith, which is the constructor both are built by — because they
// are two moments with different answers, not two spellings of one: setup
// stands a fresh installation up and creates its GCP project, may create a
// billing link and a Batch backend, and update re-converges what is already
// there after a CLI upgrade and creates nothing. The flag sets are where that
// shows: every flag is declared on both doors (so a converge is driven the
// same way from either), and the ones that only make sense on one are hidden
// on the other and refused by name (see foreignFlag).

// setupLong is `fugaro setup --help`. It is the converge's own description
// moved out of init.go (W1.2 splits the retired command's Long between the
// two doors) with the command it names being the one it is; W1.3 grows the
// wizard's questions into it.
const setupLong = `setup plans the installation's shared resources (the runs bucket, registries,
custom roles, the scheduler account, log isolation, an optional budget) with
Terraform, importing what the bootstrap already made, and applies the plan it
showed once you confirm by typing the project's name (or pass --yes). It
then writes the project config from the installation's outputs. With no
project config yet, --name, --gcp-project and --region say what to create:
projects/<name>.yaml.

It enables the Cloud Resource Manager API when it is disabled (setup reads
the project's number through it before Terraform can enable it), creates
the Terraform state bucket first when it doesn't exist, and offers to remove
project Viewers' read access to the runs bucket, each after its own
confirmation. --plan-only never takes --yes: what the plan needs made first
(the state bucket, the API) takes the project's name typed at a terminal, or
the run stops and says so. A plan that would delete or replace anything is
refused unless --allow-delete names the address.

A new project config grants the launcher and operator roles to you, the
authenticated user (user:<your email> from the Application Default
Credentials), so that fugaro run works; --launcher or --operator replaces that,
an existing config is never changed by it, and with a service
account's credentials it stops and names the two flags.

Fugaro only starts fresh: a first run creates the GCP project the installation
lives in, and that project is Fugaro's, and managed, from its creation. On a
first run it asks its questions once, in this order, each with a suggestion
Enter takes, and writes the local config, so later runs ask nothing: the new
GCP project's ID (--gcp-project; a project named after this one,
fugaro-<name>, which Enter takes; never gcloud's default project), the region
(--region; us-east5) and the project's name (--name; from the repository's
owner, else this directory's). Every question has its flag, so only the
confirmations below need a terminal at all: without one, or with
--non-interactive, --yes or --json, the run names every missing flag in one
error. An ID that already exists is refused before anything is asked or
changed ("Fugaro only starts fresh: <id> already exists; pick another id"),
and so is one that already holds a Fugaro installation, with the command that
uses that installation from this machine instead: fugaro update --gcp-project
<id> --region <region>, which writes the local config from the installation
and applies nothing. The one taken ID a first run goes on with is a project an
earlier run of fugaro setup created and did not finish (it carries the label
fugaro_created=true from its creation): the rerun resumes it. The budget
section of a local config written from an installation (rtdb_url,
firebase_project, firebase_api_key, token_signer: none a secret) is filled
from the Firebase root's outputs, read only. A --name given for an
installation that already exists must be the installation's own name. With
several project configs, --name of a project that has none is a first run of
that name, and a checkout whose origin repository exactly one project lists
selects that project.

Before the first billable image build of a GitHub repository, setup asks GitHub
(as the App, with the App's key read from Secret Manager into memory only)
whether the App is installed on the repository with the permissions runs ask
for (Contents: Write, Pull requests: Write, Issues: Read, Metadata: Read): if
not, the build is left for you with each missing permission and its fix and
nothing is billed. A repository the local config does not list is asked about
first, at the start, before any plan.

setup runs as a converge of stages (preflight, installation, with
--firebase-project the Firebase backend and the images, the secrets, the
plugin wiring and the repository, and on a first run the project — the one
stage that creates the GCP project, and the one that refuses an ID that
already exists), each with its own confirmation, in order, and stops at the
first that fails or needs you; a rerun resumes, and one with nothing to do
says "No changes" and exits 0. Exit codes: 0 done (or only planned), 1 a
refusal, or a step left for you (see left_for_you in --json), 2 a cloud
failure; --json prints the stages, left_for_you and the failed stage on every
outcome. Billing not linked is a pause, not a failure: the project stage is
billing-pending, the stages that need billing are waiting-for-billing (the
plugin wiring still runs), the local config is written with setup_pending:
billing, the run exits 1 with one block to forward to whoever administers
billing (the gcloud billing projects link line for the new project, the
billing account's id theirs to supply, and the rerun line), and the rerun
resumes the project it created.

setup --anchor, run in the checkout of a repository the project lists, writes
only the gcp_project: line of its fugaro.yaml (the line teammates find the
shared config from), with no Terraform, no discovery, no publish and no image
copy; plain fugaro setup writes the same line as its last stage, after the whole
converge. Before it writes anything it checks, and reports together (one
line per workflow, with its fix in order), that every workflow's build record
in the runs bucket exists and was submitted by a fugaro release at or after
the one that added the field, and that the base image the image was built
FROM (the record's base_ref, whose fugaro binary the image holds) is such a
release too: a missing record, a record that does not say its base, an older
or development CLI, or an older, development or hand-pushed base fails. It
also checks that each base image the local config sets for those workflows is
this release's pin through the remote repository, or a release image an
earlier fugaro copied, at or after that release, since the next build starts
from it (a development or hand-pushed one is never replaced by fugaro setup
--base or fugaro image refresh: remove its base_images entry first). The fix
it names is: that removal when needed, fugaro image refresh --repo
<owner/name> --workflow <name> in the checkout (it verifies the release's
base image and pins it by digest, points the daily image check job at it and
rebuilds the image), then fugaro setup --anchor. The image checks trust the build records (written by
the builds, unsigned: the same trust as the shared config), and a hand-pushed
image under a release-looking tag passes them. Then the diff; --yes writes, a terminal asks, and otherwise it
prints the line and writes nothing. Exit codes: 0 written or already present
and safe, 1 a check failed or nothing was written, 2 a build record could not
be read from the cloud (missing credentials included). A coding agent's
session may run it, --yes included: it edits only a local file.

setup --firebase-project <gcp-project-id> turns the shared budget on: it
builds the budget backend in the installation's own GCP project. The value is
the installation's own GCP project id (the default; the flag is only needed to
turn the shared budget on), and any other id is refused. A setup without it
builds no backend. It creates nothing and links nothing
itself (the project and its billing link are the first run's), and it refuses
a project that has no billing, or whose database holds data and no Fugaro
mark. It runs three applies, each
with its own plan and confirmation: the installation (the history account),
the Firebase root (the database, a restricted sign-in key, the token signer,
and the budget admins: the GCP project's owners and editors who are users or
groups, plus --budget-admin), and the installation again (the history job,
once its image exists). Between the last two it deploys the database's rules,
mark, project name, mode (--budget-mode; an absent one is seeded observe) and
largest lease, after a confirmation of their own, then writes the local
config. The same step creates the Firestore database for the spend history
(fugaro report) if the project has none: its location, us-east5, is permanent
(chosen once, never changed or deleted by Fugaro), so setup asks you to type
the location to confirm it, at a real terminal (--yes, --non-interactive,
--json and a pipe never confirm it: the step is then left for you), and it
refuses a database that holds data or is in another location; it also deploys
deny-everything Firestore rules and the Fugaro mark. --plan-only stops after the installation's plan. Each repository then
needs fugaro setup --repo to pick up the jobs' environment.

setup --repo [PATH] onboards the repository of the checkout at PATH (default:
the current directory), whose fugaro.yaml says what it needs, into the
installation fugaro setup applied. It imports what the bootstrap made for it
(checking each resource's marks and live grants first), creates the rest,
and deploys each workflow's job once its secrets are stored and its image is
built. It offers each workflow's first image build (billable: --yes covers it, and
at a terminal without --yes the project's name is typed), then deploys the built image and unpauses the daily check. It
prints the fugaro secrets set commands still needed, and adds the
repository to the local config. A repository the local config does not list
takes the same opt-in as the converge's stages (below): its owner/name typed at
your own terminal, or --onboard-repo.

One confirmation per run, in a project Fugaro owns. A project is Fugaro's own
when this run created it (a first run) or its runs bucket carries Fugaro's
mark (fugaro/project.json naming this project and this GCP project, in a bucket
named fugaro-runs-<gcp project> that belongs to that project; read-only check:
a custom runs bucket never qualifies). There, at a terminal, the ordinary steps (the Terraform applies of the
installation, the Firebase root, the history job and the repository, the state
bucket, the Cloud Resource Manager API, the Viewer-read removal, image copies
and base-image pins that only add tags, the Firestore marks and rules, the
local config) share one
review screen and ONE typed project name, asked when the first of them has
something to change (so a rerun with nothing to do asks nothing). Each step
still prints its own exact plan. It never covers creating a project, linking
billing, the Firestore database's permanent location, a Cloud Build, replacing
a registry tag (--replace-image), an unlisted repository or a secret. Those are
their own steps: the build, the tag and onboarding an unlisted repository are
covered by --yes in either door (D12's answer of 2026-10-20), and a project's
creation, the billing link, the location and a secret value keep a typed
confirmation of their own. The state bucket and the API enable are fixed, announced, free steps; every
Terraform plan is shown as it runs and is covered only if it is creates and
in-place updates of the installation's own resource kinds, granting only the
modules' roles to the listed launchers, operators and budget admins (the review
prints them, with where each came from) or to Fugaro's own service accounts:
its attributes are the modules' (images in this project's registry, jobs of
this project, logs to this project, private buckets), and a Firebase project
other than the installation's is covered only if verified as Fugaro's; any other
plan (a destroy or replace, an IAM binding or policy, an unlisted member, a
widened role) stops that step, which asks its own typed name. A wrong name applies nothing.
The confirmation is per run and never stored. Any other project (one an
earlier run created and this one resumed, unmarked, or a mark that fails to
read) keeps a typed name at each step;
--yes covers the ordinary steps as ever and takes no review; --non-interactive,
--json, a pipe and a coding agent never take it; --plan-only shows the review
and applies nothing.

What --yes covers: the ordinary steps, which are the installation's and the
Firebase root's Terraform applies, the images' copy, the local config's
writes, the plugin wiring, a repository's steps — onboarding one the project
does not list included — a billable first image build and replacing a tag in
your registry (--replace-image KIND names the intent). D12's answer of
2026-10-20 is what makes those last three ordinary: they are covered in either
door, while a repository whose origin git config rewrites, or whose host is not
a provider's own, takes the typed owner/name whatever the run was given. What
--yes still never covers: creating a project or linking billing, a secret, and
the Firestore database's permanent location, this door's first-time question.
With --yes, --non-interactive, --json or a pipe each of those is left for you,
with the one line to run. In a coding agent's environment (CLAUDECODE, CLAUDE_CODE_ENTRYPOINT,
CLAUDE_CODE_SSE_PORT, CLAUDE_CODE_REMOTE, CURSOR_AGENT or AI_AGENT is set) setup
applies nothing, --yes included, and says to run it in your own terminal
window; --plan-only and the read-only checks still work, and so does setup
--anchor (with --yes), which edits only the checkout's fugaro.yaml. That is a mitigation,
not a barrier: an agent can unset its own environment. A run that writes this
machine's local config from an installation that already exists (fugaro
update on a new machine) changes nothing in the cloud in the same run (the
local config and, in a checkout, the plugin wiring in .claude/settings.json
are the only files it writes).

In a checkout of a repository of this project, the converge's secrets stage
asks, at hidden prompts, for the secrets its jobs mount (the git credential,
the Claude credential agent.auth names, none for vertex, the allowed model
providers' keys, the workflows' own), and skips what is already stored. It
prompts only in your own terminal: never with --yes, --non-interactive or
--json (--json never prompts; the stage then exits 1 with the one-line
fugaro secrets set commands), never when stdin, stdout or stderr is not a
terminal (a pipe is never read), and never when a coding agent's variable is set (CLAUDECODE
and the like; CLAUDE_CODE_SSE_PORT is also set by the Claude Code IDE
extension in VS Code and JetBrains terminals, so a person's own IDE terminal
can be refused: its refusal tells you what to do about it). A pasted private key may have at most 120 lines. It creates each
secret's container, before the repository stage, with the labels
setup --repo's Terraform imports (fugaro, fugaro_repo, fugaro_secret).

A first run creates its GCP project; --create-project says so explicitly and
changes nothing else. --gcp-project <id> is the new project's ID: the run
creates it and adds Firebase to it, as you, after you type its ID at a
terminal; --link-billing <account> then links that billing account (it may
incur charges), after you type the account's ID. Each is its own typed
confirmation, never given by --yes, --non-interactive or --json, and the ID is
never the gcloud default project. Without a billing link, setup pauses at
billing (above) and the rerun resumes the project it created. The project is labelled fugaro_created=true at its creation, and the
run that created it marks it Fugaro-managed (its runs bucket's
fugaro/managed.json and the label fugaro_managed=true) once it has applied the
installation; --managed with --budget, --budget-currency and --billing-account
also records the monthly budget the managed apply needs. A creation that a
later run finished is marked by that run only after you type the project's ID
at your own terminal: the fugaro_created=true label anyone may set says which
project to ask about, and never marks one by itself (--yes, --json,
--non-interactive and a coding agent's session leave it for fugaro managed
flag). Marking any other project the owner runs an installation in is fugaro
managed flag's. An ID that already exists and was not created by fugaro
setup, or that is pending deletion, is refused.

The plugin-wiring stage merges the Fugaro marketplace and plugin into the
checkout's .claude/settings.json, pinned to this release's tag, after showing
the diff and asking (--yes confirms it); see update-skills. The repository
stage runs setup --repo for the checkout, after the secrets, when its
origin's default branch (origin/HEAD, main or master, as last fetched) has a
fugaro.yaml naming this project (else it is skipped and says why); it asks
once for the GitHub App's ID of a GitHub repository (or take --github-app-id)
and keeps its own confirmations, the first image build's included.

In a checkout whose repository the local config does not list yet, the converge
asks first whether to onboard it ("Onboard owner/name?", Enter takes yes, before
any plan): no leaves the repository as it is for the rest of the run — no later
stage asks for it again — and says how to enrol it later. The question
authorizes nothing — yes goes on to the same typed owner/name every unlisted
repository needs — and a run that cannot ask (--yes, --non-interactive, --json,
a pipe, a coding agent's environment) asks nothing and is unchanged. Run outside
a checkout there is nothing to ask about, so setup prints the one line saying
how a repository is enrolled later: setup --repo, in a checkout of it.

Both stages act only on a repository the project's local config already lists,
or one you opt in: a repository not listed yet is onboarded when you opt it in
— its owner/name typed at your own terminal (naming the checkout's path), or
--onboard-repo owner/name, which must equal the origin (setup --repo takes both
too) — or by the run's own --yes, which covers onboarding one whose origin git
config does not rewrite and whose host is a provider's own (D12's answer of
2026-10-20). --non-interactive without --yes, --json, a pipe and a coding
agent's environment confirm none of it (the flag is refused there), so a cloned
third-party repository cannot onboard itself. The secrets stage is behind the
same gate.`

// setupDoor is the first door: the wizard, and the only door that creates.
var setupDoor = convergeDoor{
	name:  "setup",
	use:   "setup [--repo [PATH]]",
	short: "Create a fresh GCP project and stand up its installation, or a repository's cloud resources, through Terraform",
	long:  setupLong,
	// --forget is the rollback: it unwinds log isolation and registry cleanup
	// and takes every address out of Terraform's state. A first run has
	// nothing to unwind, so it is fugaro update's flag and setup refuses it
	// rather than quietly converging something else.
	foreign: []foreignFlag{{
		name:  "forget",
		owner: "fugaro update",
		hint:  "it unwinds a converge that has already run, and a first run has nothing to unwind",
	}},
}

// newSetupCmd is `fugaro setup`.
func newSetupCmd() *cobra.Command { return newSetupCmdWith(&initOptions{}) }

// newSetupCmdWith is newSetupCmd over options the caller holds. It is the
// shape restore.go's restoreInitCmd seam points at: fugaro restore creates a
// project through the same command line the user would run, with strictCreate
// set on the options it hands over.
func newSetupCmdWith(o *initOptions) *cobra.Command { return newInitCmdWith(o, setupDoor) }

// setupAlreadySetUp is the one line the wizard prints when the installation is
// already there: this machine has the project config, or the GCP project's
// runs bucket carries the mark of an installation this machine has no config
// for. Either way `setup` keeps it and converges as `update` does, rather than
// offering to create a second installation in one GCP project — that offer is
// what the retired command's ambiguity was (D11).
const setupAlreadySetUp = "this installation is already set up"

// wizardQuestion is one question the wizard asks, in the order it asks it: the
// flag that answers the same question without a terminal — so a scripted run
// needs no terminal for anything but the typed confirmations — and the name
// `--non-interactive` prints when the run cannot proceed without an answer.
//
// missing is what a run that cannot ask is refused with when nothing else
// answers the question.
type wizardQuestion struct {
	what    string // the question, in one line
	label   string // the label the prompt shows (a prefix, where the label is built from the run)
	flag    string // the flag that answers it
	missing string // what MissingInputsError names, exactly as firstRunMissing builds it
}

// setupQuestions is the wizard's question list, and TestEveryWizardQuestionHasAFlag
// holds the run to it in both directions: every label below is a prompt a first
// run prints when nothing else answers it and none is printed when its own flag
// does, and the missing names are the ones a run with no terminal prints — so a
// question added without a flag, or with a flag nobody can pass, fails there
// rather than in a user's terminal.
//
// The table is the phase's own list, in the plan's sense (W3.5): wizardStages
// below is the one stage that asks these questions, and it asks them in this
// order, none of them twice, all of them before anything is applied. A question
// the phase asks prints its row's label, so the row and the prompt cannot
// drift apart (the ID, region and project answers) — and the rows a later task
// adds are asked by the same stage, in the position they are listed in.
//
// The repository question (W3.4) is not a row of this table: it is
// about the checkout, so it is asked where the checkout is read — at the start
// of the converge, by the gate (init_repo_gate.go's asksOnboard) — and a first
// run with no checkout would have no label to print and no flag to answer it.
var setupQuestions = []wizardQuestion{
	{
		what:    "the GCP project's ID",
		label:   "GCP project ID",
		flag:    "--gcp-project",
		missing: "--gcp-project <id>",
	},
	{
		what:    "the region the installation's jobs and the scheduler run in",
		label:   "Region",
		flag:    "--region",
		missing: "--region <region>",
	},
	{
		what:    "the Fugaro project's name",
		label:   "Fugaro project name",
		flag:    "--name",
		missing: "--name <name>",
	},
}

// questionFor is the table's row for a question's flag: the label the prompt
// shows, and the name a run that cannot ask is refused with. The wizard's
// questions ask their label through this (wizard_stage.go), so the prompt a
// person answers and the row TestEveryWizardQuestionHasAFlag holds the run to
// are one string. A flag with no row is a programming error and not a run's:
// the zero row's label is the flag itself, which is a label a person can still
// read, and the test above fails on it.
func questionFor(flag string) wizardQuestion {
	for _, q := range setupQuestions {
		if q.flag == flag {
			return q
		}
	}
	return wizardQuestion{what: "the question " + flag + " answers", label: flag, flag: flag}
}

// wizardStages is the wizard's input phase: the stage list one initflow.Run is
// given, and there is exactly one stage in it (wizard_stage.go). It is
// registered here, with the questions it asks (setupQuestions above) and the
// answer the run takes itself (askProjectAnswer below), because this file is
// the wizard's — and it is the first thing a run does, which is what the
// stage's name says (the first slot of the canonical order).
//
// It is deliberately not part of initStages() (init_stages.go): that list is
// the apply phase's, one stage per canonical slot, and the two phases are two
// runs of the loop. A stage under the input phase's name in the apply phase's
// run would be a duplicate name, which initflow.Validate refuses — and would
// ask the questions between applies, which is the shape this task removes.
func wizardStages(ctx context.Context, r *initRun) []initflow.Stage {
	return []initflow.Stage{newWizardStage(ctx, r)}
}

// settleCreation records what this run does about the GCP project, where the
// rest of the run reads it: --create-project's own field. Fugaro only starts
// fresh (the owner's ruling of 2026-10-23), so there is no question to ask: a
// first run of fugaro setup creates its project (the project stage,
// init_project.go, refuses an ID that already exists before anything is
// changed), and the flag only says so explicitly.
//
// A run that cannot act on a creation does not get one: `fugaro update` creates
// nothing (its stage set has no project stage at all, init_stages.go), and
// --config-only and --print-vars write or print from an installation that is
// already there and touch no project. firstRun is whether this run selected no
// project config (wizardStage.creating): a run with one was created already.
//
// Either way the flags that only a creation takes are checked here
// (checkCreateProject): init.go's check() validated them only where
// --create-project was explicit, because whether a run without it creates was
// not known until its project config was looked for. A run that creates is held
// to the creation's rules (the ID's, Google's restricted words among them, and
// the --firebase-project pairing); one that does not refuses --link-billing,
// --managed, --parent and --display-name by name.
func (r *initRun) settleCreation(firstRun bool) error {
	o := r.o
	if firstRun && o.door != updateDoor.name && !o.configOnly && !o.printVars && !o.forget {
		o.createProject = true
	}
	return o.checkCreateProject()
}

// announceAlreadySetUp is setup's one line for a run whose installation is
// already there: a project config this machine has (e.old is its file), which
// is also what makes the run update's stage set — initStages registers no
// project stage once the config exists, so `setup` converges exactly as
// `fugaro update` does.
//
// It says nothing with --json (stdout is one object) or --print-vars (stdout is
// the variables alone), the two modes whose stdout is a document rather than a
// transcript; the fact is in the result either way.
func (e *initEngine) announceAlreadySetUp() {
	r, o := e.r, e.r.o
	if o.door != setupDoor.name || e.firstRun() || o.asJSON || o.printVars {
		return
	}
	fmt.Fprintf(r.w, "%s%s: keeping project %s, and converging as %s does\n",
		o.programPrefix(), setupAlreadySetUp, pluginwire.Printable(e.lc.Name), "fugaro "+updateDoor.name)
}

// firstRun is whether this converge is a first run: no project config yet,
// or the one a first run of setup wrote when it paused for billing
// (localcfg.Config.SetupPending), which the rerun resumes as a first run.
func (e *initEngine) firstRun() bool {
	return e.old == nil || e.lc.SetupPending != ""
}

// programPrefix is the line's own "fugaro setup: ", or "" for a hand-built
// options value — a test's — whose run has no door to name.
func (o *initOptions) programPrefix() string {
	if p := o.loopProgram(); p != "" {
		return p + ": "
	}
	return ""
}
