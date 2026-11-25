package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/rigor"
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
//
// The budget question's suggestion and its unit are read from T1's constants
// rather than written into the sentence (D2211, D2212): the help a person reads
// before a first run names the same figures the question takes and the alert is
// created with, whatever those are.
var setupLong = `setup plans the installation's shared resources (the runs bucket, registries,
custom roles, the scheduler account, log isolation, an optional budget) with
Terraform, importing what the bootstrap already made, and applies the plan it
showed once a person at the terminal confirms it. It
then writes the project config from the installation's outputs. With no
project config yet, --name, --gcp-project and --region say what to create:
projects/<name>.yaml.

It enables the Cloud Resource Manager API when it is disabled (setup reads
the project's number through it before Terraform can enable it), creates
the Terraform state bucket first when it doesn't exist, and offers to remove
project Viewers' read access to the runs bucket, each after its own
confirmation. --plan-only applies nothing and confirms nothing: what the plan needs made first
(the state bucket, the API) takes a person at the terminal, or the run stops
and says so. A plan that would delete or replace anything is
refused unless --allow-delete names the address.

A new project config grants the operator and admin roles to you, the
authenticated user (user:<your email> from the Application Default
Credentials), so that fugaro run works; --operator or --admin replaces that,
an existing config is never changed by it, and with a service
account's credentials it stops and names the two flags.

A first run creates the GCP project the installation
lives in — or adopts one that is already there and holds nothing of Fugaro's,
using it as if it had created it (the owner's ruling of 2026-11-22) — and that
project is Fugaro's, and managed, from its creation. On a
first run it asks its questions once, in this order, each with a suggestion
Enter takes, and writes the local config, so later runs ask nothing: the new
GCP project's ID (--gcp-project; a project named after this one,
fugaro-<name>, which Enter takes; never gcloud's default project), where the
installation lives (--location; US, whose region is us-east5 and which Enter
takes, or EU, whose region is europe-west1; a region typed in full answers the
same question and is --region's own form, and the two given together must
agree), the project's name (--name; from the repository's
owner, else this directory's), the project's rigor mode (--rigor; how much
review every run of it gets — poc, startup, pro, the built-in default that is
listed first, or critical — and the one question with no suggestion, so Enter
is not an answer there), the project's monthly budget alert (--budget; the
amount Cloud Billing warns the owner at when the month's spend reaches it — an
alert, not a cap on spending: what stops a run is the gateway's cost cap — in
whole units of ` + managed.DefaultBudgetCurrency + `, which Enter takes as ` + strconv.FormatInt(managed.DefaultBudgetAmount, 10) + `) and whether a
repository the build can read is
baked into the image its job runs (--bake-repos; yes, which makes jobs start
faster; asked only where it can be answered, so a run with a project config
keeps that project's answer). Every question has its flag, so only the
confirmations below need a terminal at all: without one, or with
--json, the run names every missing flag in one
error — the bake switch and the budget need no flag there, because an absent
key means the same as a written yes and a budget nobody answered takes its own
default. An ID that already exists and holds anything of Fugaro's is refused
before anything is asked or changed ("<id> already exists and holds something
of Fugaro's: fugaro setup only adopts a project that holds nothing of
Fugaro's, so pick another id"), and so is one that already holds a Fugaro
installation, with the
command that uses that installation from this machine instead: fugaro update
--gcp-project <id> --region <region>, which writes the local config from the
installation and applies nothing. A project that already exists and holds
nothing of Fugaro's is adopted: the run uses it as if it had created it. The
other ID a first run goes on with is a project an
earlier run of fugaro setup made Fugaro's and did not finish (it carries the
label fugaro_created, true or adopted, from that run): the rerun resumes it. The budget
section of a local config written from an installation (rtdb_url,
firebase_project, firebase_api_key, token_signer: none a secret) is filled
from the Firebase root's outputs, read only. A --name given for an
installation that already exists must be the installation's own name. With
several project configs, --name of a project that has none is a first run of
that name, and a checkout whose origin repository exactly one project lists
selects that project.

Before the first billable image build of a GitHub repository, setup asks GitHub
(as the App, with the App's key read from Secret Manager into memory only)
whether the App is installed on the repository with the permissions Fugaro
requires (Contents: Write, Pull requests: Write, Issues: Read and write,
Metadata: Read): if
not, the build is left for you with each missing permission and its fix and
nothing is billed. A repository the local config does not list is asked about
first, at the start, before any plan.

setup runs as a converge of stages (preflight, installation, the Firebase
backend — the budget database, which it adopts or creates, see
--firebase-project — the images, the secrets, the plugin wiring and the
repository, and on a first run the project — the one
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
image under a release-looking tag passes them. Then the diff; a person at the
terminal confirms it there (nothing is typed), and a run with no terminal
prints the line and writes nothing. Exit codes: 0 written or already present
and safe, 1 a check failed or nothing was written, 2 a build record could not
be read from the cloud (missing credentials included). A coding agent's
session may run it: it edits only a local file.

setup --firebase-project <gcp-project-id> turns the shared budget on: it
builds the budget backend in the installation's own GCP project. The value is
the installation's own GCP project id (the default; the flag is only needed to
turn the shared budget on), and any other id is refused. The stage runs with
and without the flag, and setup and update run the same one: with none it
adopts the database the installation's Firebase state names where that
database is the installation's own project's, or creates one in the
installation's own project where this credential may make the project's IAM
changes and the project has billing (the new database starts in observe, with
no mode chosen); where neither is this run's — the credential may not, the
project has no billing, or nothing answers the confirmation creating one costs
(a coding agent's session, --json, a pipe) — the stage changes nothing, says
in one line who must run the command or that billing must be linked, and the
rest of the run carries on. An installation whose database this machine
already records keeps what it has. The stage creates nothing and links nothing
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
(fugaro report) if the project has none: it goes in the installation's own
region where Firestore offers it, and otherwise in the place's own default
region — the mapping's answer, which the step's own plan row prints before
anything is created — and its location is permanent (chosen once, never changed
or deleted by Fugaro), so the run prints where and creates it, asking nobody
(nothing is typed, and a coding agent's session, --json and a pipe are no
different); a database that already exists is adopted as it is, wherever it is,
when it is empty or carries the Fugaro mark — the plan row says where, and names
it as outside the installation's place when it is — and a database that holds
data and carries no mark, or that is not in native mode, is refused; it also deploys
deny-everything Firestore rules and the Fugaro mark. --plan-only stops after the installation's plan. Each repository then
needs fugaro setup --repo to pick up the jobs' environment.

setup --repo [PATH] onboards the repository of the checkout at PATH (default:
the current directory), whose fugaro.yaml says what it needs, into the
installation fugaro setup applied. It imports what the bootstrap made for it
(checking each resource's marks and live grants first), creates the rest,
and deploys each workflow's job once its secrets are stored and its image is
built. It offers each workflow's first image build (billable: the person at the
terminal confirms it there), then deploys the built image and unpauses the daily check. It
prints the fugaro secrets set commands still needed, and adds the
repository to the local config. A repository the local config does not list
takes the same opt-in as the converge's stages (below): the confirmation of a
person at your own terminal, or --onboard-repo.

One confirmation per run, in a project Fugaro owns. A project is Fugaro's own
when this run created it (a first run) or its runs bucket carries Fugaro's
mark (fugaro/project.json naming this project and this GCP project, in a bucket
named fugaro-runs-<gcp project> that belongs to that project; read-only check:
a custom runs bucket never qualifies). There, at a terminal, the ordinary steps (the Terraform applies of the
installation, the Firebase root, the history job and the repository, the state
bucket, the Cloud Resource Manager API, the Viewer-read removal, image copies
and base-image pins that only add tags, the Firestore marks and rules, the
local config) share one
review screen and one confirmation, taken by the person at the terminal and
shown when the first of them has something to change (nothing is typed, so a
rerun with nothing to do shows nothing). Each step
still prints its own exact plan. It never covers creating a project, linking
billing, a Cloud Build, replacing
a registry tag (--replace-image), an unlisted repository or a secret. Those are
their own steps: the build, the tag and onboarding an unlisted repository are
covered by the run's own confirmation in a project whose gate stands in for it,
and taken by the person at the terminal otherwise (D12's answer of 2026-10-20
keeps them one class in either door), and a project's
creation, the billing link and a secret value keep a
confirmation of their own, taken with the person at the terminal. The Firestore
database's location is no step at all: where the run creates the database it
prints where, and that the location is permanent, and creates it. The state bucket and the API enable are fixed, announced, free steps; every
Terraform plan is shown as it runs and is covered only if it is creates and
in-place updates of the installation's own resource kinds, granting only the
modules' roles to the listed launchers, operators and budget admins (the review
prints them, with where each came from) or to Fugaro's own service accounts:
its attributes are the modules' (images in this project's registry, jobs of
this project, logs to this project, private buckets), and a Firebase project
other than the installation's is covered only if verified as Fugaro's; any other
plan (a destroy or replace, an IAM binding or policy, an unlisted member, a
widened role) is not covered by the run's confirmation: it is shown and then
applied, and left for you where nobody is at the terminal. The confirmation is
per run and never stored. Any other project
(one an earlier run created and this one resumed, unmarked, or a mark that
fails to read) keeps its own confirmation at each step, taken at your own
terminal; --json, a pipe and a coding agent never give one; --plan-only shows
the review and applies nothing.

What that confirmation covers: the ordinary steps, which are the installation's and the
Firebase root's Terraform applies, the images' copy, the local config's
writes, the plugin wiring, a repository's steps — onboarding one the project
does not list included — a billable first image build and replacing a tag in
your registry (--replace-image KIND names the intent). D12's answer of
2026-10-20 is what makes those last three ordinary: they are covered in either
door, while a repository whose origin git config rewrites, or whose host is not
a provider's own, takes the confirmation of a person at the terminal whatever
the run was given. What
it still never covers: creating a project or linking billing, and a secret.
With no terminal, or with --json, each of those is left for you, with the
one line to run: a first run that reaches the billing question is refused at
once, in the sentence naming --link-billing, before any account of yours is
read. In a coding agent's environment (CLAUDECODE, CLAUDE_CODE_ENTRYPOINT,
CLAUDE_CODE_REMOTE, CURSOR_AGENT or AI_AGENT is set) setup
applies nothing and says to run it in your own terminal
window; --plan-only and the read-only checks still work, and so does setup
--anchor, which edits only the checkout's fugaro.yaml. That is a mitigation,
not a barrier: an agent can unset its own environment. CLAUDE_CODE_SSE_PORT is
read the same way and is the one case that is not an agent's: the Claude Code
IDE extension sets it in a person's own terminal too, so on its own it counts
as a coding agent's session only where standard input is not a real terminal,
and with a real terminal it types the same confirmations as any other — one
line says the confirmation was accepted from an IDE terminal. Set beside any
of the markers above it is an agent's session like theirs, and a pipe and
--json behave exactly as they did. A run that writes this
machine's local config from an installation that already exists (fugaro
update on a new machine) changes nothing in the cloud in the same run (the
local config and, in a checkout, the plugin installed in your own Claude Code
at user scope are all it writes).

In a checkout of a repository of this project, the converge's secrets stage
asks, at hidden prompts, for the secrets its jobs mount (the git credential,
the Claude credential agent.auth names, none for vertex, the workflows' own),
and skips what is already stored. A model provider's key is not one of them:
since 0.27.0 it is entered in the credentials service's bundle, one entry per
repository, and the stage names the command that writes this repository's
entry (fugaro secrets set NAME --repo owner/name) instead of prompting for a
value it would refuse to store. It
prompts only in your own terminal: never with
--json (--json never prompts; the stage then exits 1 with the one-line
fugaro secrets set commands), never when stdin, stdout or stderr is not a
terminal (a pipe is never read), and never when a coding agent's variable is set (CLAUDECODE
and the like). CLAUDE_CODE_SSE_PORT is set by the Claude Code IDE extension in
VS Code and JetBrains terminals as well, so it alone is not a coding agent when
standard input is a real terminal: a person's own IDE terminal prompts exactly
as any other terminal does, and no stored secret is read back to it; with
another marker beside it, or no terminal, it is refused like any other. A pasted private key may have at most 120 lines. It creates each
secret's container, before the repository stage, with the labels
setup --repo's Terraform imports (fugaro, fugaro_repo, fugaro_secret).

A first run creates its GCP project; --create-project says so explicitly and
changes nothing else. --gcp-project <id> is the new project's ID: the run
creates it and adds Firebase to it, as you, at your own terminal, where being
at it is what confirms the step; --link-billing <account> then links that
billing account (it may incur charges), at your own terminal too. Each is its
own confirmation, taken by the person at the terminal and never by --json, and the ID
is never the gcloud default project. Without a billing link, setup pauses at
billing (above) and the rerun resumes the project it created. The project is labelled fugaro_created=true at its creation, and the
run that created it marks it Fugaro-managed (its runs bucket's
fugaro/managed.json and the label fugaro_managed=true) once it has applied the
installation; --managed with --budget, --budget-currency and --billing-account
also records the monthly budget the managed apply needs. A creation that a
later run finished is marked by that run only where the person at your own
terminal confirms it: the fugaro_created=true label anyone may set says which
project to ask about, and never marks one by itself (--json
and a coding agent's session leave it for fugaro managed
flag). Marking any other project the owner runs an installation in is fugaro
managed flag's. An ID that already exists and was not created by fugaro
setup, or that is pending deletion, is refused.

The plugin stage installs or updates the Fugaro plugin in your own Claude Code,
at user scope, through the claude CLI, and writes nothing in the checkout: the
merge of the marketplace and the plugin into .claude/settings.json is deleted
(D949); see update-skills. The repository stage runs setup --repo for the
checkout, after the secrets, when its origin's default branch (origin/HEAD,
main or master, as last fetched) has a
fugaro.yaml naming this project (else it is skipped and says why); it asks
once for the GitHub App's ID of a GitHub repository (or take --github-app-id)
and keeps its own confirmations, the first image build's included.

In a checkout whose repository the local config does not list yet, the converge
onboards it at the gate, before any stage plans, and asks nothing: the note the
gate prints names the repository, its host and the checkout's path, a person at
the terminal reads it and the repository is onboarded with nothing typed. A run
that cannot confirm it (--json, a pipe, a
coding agent's environment) leaves the repository as it is for the rest of the
run — no later stage asks for it again — and says how to enrol it later. Run
outside a checkout there is nothing to ask about, so setup prints the one line
saying how a repository is enrolled later: setup --repo, in a checkout of it.

Both stages act only on a repository the project's local config already lists,
or one you opt in: a repository not listed yet is onboarded when you opt it in
— the confirmation of a person at your own terminal (naming the checkout's
path), or
--onboard-repo owner/name, which must equal the origin (setup --repo takes both
too) — or by the run's own confirmation, which covers onboarding one whose origin git
config does not rewrite and whose host is a provider's own (D12's answer of
2026-10-20). --json, a pipe and a coding
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
	}, {
		name:  "check",
		owner: "fugaro update",
		hint:  "it reports what a converge that has already run would do, and a first run has nothing to report: fugaro setup --check has no installation to read",
	}, {
		name:  "ask",
		owner: "fugaro update",
		hint:  "it asks for the confirmations a managed project's gate stands in for, and only fugaro update opens that gate: fugaro setup confirms each of its own steps with the person at the terminal already, whatever the project's marks say",
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
// needs no terminal for it — and the name the refusal prints when the run
// cannot proceed without an answer.
//
// what is the question in a sentence, and it is what a run that could not read
// an answer says it had no answer for (noAnswer) and what the value entry names
// where it reads for itself; label and hint are what the question looks like on
// the screen — the bold label and, after it, what the question says about the
// shape of the answer — so a prompt is one line and the two halves of it are
// the row's (askedLine).
//
// missing is what a run that cannot ask is refused with when nothing else
// answers the question.
type wizardQuestion struct {
	what    string // the question, in one line, for the refusals about it
	label   string // the label the prompt shows (a prefix, where the label is built from the run)
	hint    string // what the prompt says about the answer after the label, or ""
	flag    string // the flag that answers it
	missing string // what MissingInputsError names, exactly as firstRunMissing builds it
}

// askedLine is the question as a person reads it, up to the colon their answer
// is typed after: the label, and what the row says about the shape of the
// answer. The suggestion Enter takes is not part of it — that is the run's, and
// it is drawn after this (wizardAsk) — so what is here is what the table
// decides, which is what a test can hold a prompt to.
func (q wizardQuestion) askedLine() string {
	if q.hint == "" {
		return q.label
	}
	return q.label + ": " + q.hint
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
		// The place question (LP4 of docs/location-preference; D3080, D3081,
		// D3086). It is the wizard's region question in a person's words and
		// not a second question (F2): the answer is us, eu, or a region typed
		// in full, --region answers the same question precisely, and the
		// region stays the one setting the installation stores (D3080). The
		// suggestion is US, which is today's default region; the region itself
		// is not in the row, because a place answer names one region and the
		// mapping's own rows say which (R7).
		what:    "the place the installation lives in (US, EU, or a region typed in full)",
		label:   "Location",
		hint:    "US, EU or a region",
		flag:    "--location",
		missing: "--location <us|eu>",
	},
	{
		what:    "the Fugaro project's name",
		label:   "Fugaro project name",
		flag:    "--name",
		missing: "--name <name>",
	},
	{
		// The rigor question (task W of
		// docs/rigor-command/implementation-plan.md; D900, the owner's ruling
		// of 2026-10-31). It is asked after the name, because the mode is
		// about the project the name settles, and it is the one question of
		// this table with no suggestion: a mode nobody typed is the state the
		// task ends, so Enter is not an answer here (askRigor).
		what:    "the project's rigor mode",
		label:   "Rigor mode",
		flag:    "--rigor",
		missing: "--rigor <mode>",
	},
	{
		// The budget question (T2b of docs/managed-flag-ergonomics; D2210,
		// D2211, D2212). The figure is the project's budget ALERT — the point
		// Cloud Billing warns the owner at — and the label says so, because
		// what stops a run is the gateway's cost cap, a different figure in a
		// different place (internal/infra/managed/defaults.go): a monthly
		// figure read as a cap would be read as a limit this wizard does not
		// set. The suggestion is the default amount, which Enter takes, and
		// the missing is empty on purpose like the bake switch's below: a run
		// that cannot ask takes the same default (D2212), so demanding the flag
		// would refuse a run whose answer is already in hand (askBudget).
		what:    "the monthly amount of the project's budget alert, in whole units of " + managed.DefaultBudgetCurrency,
		label:   "Monthly budget alert",
		hint:    "an alert at a threshold, not a cap on spending, in whole units of " + managed.DefaultBudgetCurrency,
		flag:    "--budget",
		missing: "",
	},
	{
		// The bake switch (IS7 of docs/image-setup-skill/implementation-plan.md;
		// D1901). Its missing is empty on purpose: a run that cannot ask takes
		// this answer itself — an absent key means yes, which is the
		// suggestion the question offers — so demanding the flag would refuse a
		// run whose answer is already in hand (askBake).
		what:    "whether a repository the build can read is baked into the image its job runs",
		label:   "Bake repos into the images for faster job starts?",
		flag:    "--bake-repos",
		missing: "",
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

// askBake is the wizard's bake question (IS7 of
// docs/image-setup-skill/implementation-plan.md; D1901): whether a repository
// the build can read is baked into the image its job runs, asked once, with yes
// as the suggestion.
//
// The answer goes onto --bake-repos's own fields, which is where a scripted run
// answers the same question and where writeConfig reads it from: the run that
// asked it writes bake_repos into the local config it writes, the policy stage
// publishes it in the same run, and a later change goes through `fugaro
// settings set bake_repos` and its own rebuild (noteBakeRebuild).
//
// It asks nothing where nobody can answer (canAsk: --json, a pipe) and
// nothing on a run that does not create the
// installation — a run with a project config, `fugaro update` too — which is
// the owner's rule for a managed project: there the value is the flag's or the
// published node's, and a question nobody asked for would replace the project's
// answer with this machine's. --create-project's field is what says the run
// creates, and settleCreation above has already set it.
//
// The question is asked through the same helper as the three above it (prompt),
// so an input that ends here stops the phase at this question like any other,
// and a rerun resumes at it: the row's empty missing is not a licence to answer
// on nobody's behalf, and this question has no second shape.
func (s *wizardStage) askBake() error {
	r, o := s.r, s.r.o
	if o.bakeReposChanged || o.configOnly || !r.canAsk() || !o.createProject {
		return nil
	}
	q := questionFor("--bake-repos")
	s.a.asked = append(s.a.asked, q.label)
	var yes bool
	if _, err := r.askValue(wizardAsk{label: q.label, def: "yes", show: "Y/n"}, valueSpec{what: q.what, flag: q.flag}, func(v string) error {
		var verr error
		yes, verr = bakeAnswer(v)
		return verr
	}); err != nil {
		return err
	}
	o.bakeRepos, o.bakeReposChanged = yes, true
	return nil
}

// rigorQuestionLead is the heading above the four modes. It is the question's
// own label followed by what the answer decides, so the table that follows
// reads as the answer's own list rather than as a paragraph about it: the one
// thing a person has to know — that Enter is not an answer here, unlike every
// other question of this wizard — is said where Enter is pressed and not up
// front (askRigor's wizardAsk.empty).
const rigorQuestionLead = "Rigor mode, which sets how much review each run gets:"

// rigorChoice is one line of the rigor question: the mode, what review it runs
// in short words, and the wizard's own note about what it costs. Everything
// else the line says is read from the two tables that hold it — the review a
// mode runs by default (rigor.DefaultLevels) and the rounds it spends
// (rigor.DefaultRounds) — so the question cannot describe a mode as anything
// other than what a run of it does.
type rigorChoice struct {
	mode rigor.Rigor
	note string
}

// rigorChoices is the question's list, and `pro` first is the whole of what
// "the recommended answer" is: it is rigor.Default, the mode every project that
// says nothing already runs (D19), so the recommendation asks nobody to change
// what they have. Hotfix is not in the list and is not writable: it is a
// per-run mode (D12, D834), refused by name where it is typed or passed.
var rigorChoices = []rigorChoice{
	{mode: rigor.Pro, note: "(default)"},
	{mode: rigor.Startup, note: "less spend"},
	{mode: rigor.POC, note: "cheapest"},
	{mode: rigor.Critical, note: "most review"},
}

// printRigorChoices prints the four modes as the table the question is answered
// from: the mode, its review and its rounds from the tables that hold them, and
// its note — the columns padded, so the review and the note line up and a
// person reads one down each column.
func (r *initRun) printRigorChoices() {
	type row struct{ mode, review, note string }
	var rows []row
	for _, c := range rigorChoices {
		levels, err := rigor.DefaultLevels(c.mode)
		if err != nil {
			// The level table's answer for a mode it does not hold. This list
			// names four modes the table has, so a line is never missing for
			// this reason; a line whose review said nothing would be worse
			// than no line, which is why the mode is skipped rather than
			// described by a guess (DefaultLevels refuses to guess too).
			continue
		}
		rows = append(rows, row{
			mode:   string(c.mode),
			review: rigorReviewWords(levels) + ", " + rigorRoundWords(rigor.DefaultRounds(c.mode)),
			note:   c.note,
		})
	}
	modePad, reviewPad := 0, 0
	for _, row := range rows {
		modePad, reviewPad = max(modePad, len(row.mode)), max(reviewPad, len(row.review))
	}
	for _, row := range rows {
		fmt.Fprintf(r.w, "  %-*s  %-*s  %s\n", modePad, row.mode, reviewPad, row.review, row.note)
	}
}

// rigorRoundWords is a mode's loop counts as the table's own column, "3+3
// rounds": the two counts are the first-line and the senior rounds a run of the
// mode spends, and the one number a reader compares across the four modes is
// how much of each it buys.
func rigorRoundWords(r rigor.Rounds) string {
	return fmt.Sprintf("%d+%d rounds", r.Inner, r.Senior)
}

// rigorReviewWords is what review a mode runs, in the table's short words, read
// off the mode's own row of the level table (rigor/levels.go): level 1 is the
// first-line review, level 2 the senior review that decides readiness, level 3
// the adversarial pass after it.
func rigorReviewWords(l rigor.Levels) string {
	switch {
	case l.L3 == rigor.LevelOn:
		return "senior + adversarial"
	case l.L2 == rigor.LevelOn:
		return "senior review"
	case l.L1 == rigor.LevelOn:
		return "first-line review only"
	}
	return "no review at all"
}

// rigorAnswerModes is the list a near miss is refused with: the modes this
// question accepts, in the order it prints them (rigorChoices).
//
// It is not rigor.RigorList(), which is the whole vocabulary with hotfix in it:
// hotfix is the one value this question refuses a line below, so a refusal that
// named it would send the reader to a second refusal. Taking the list off the
// table the question prints is also what keeps the two from drifting — a mode
// added to the question is in its refusal, and one taken out is not.
func rigorAnswerModes() string {
	modes := make([]string, 0, len(rigorChoices))
	for _, c := range rigorChoices {
		modes = append(modes, string(c.mode))
	}
	return strings.Join(modes, ", ")
}

// setupRigorMode is the rigor question's own reading of an answer: the mode it
// names, or the refusal the closed vocabulary gives everything else.
//
// hotfix is in the vocabulary and not in this answer: it is off the axis and an
// operator's own per-run mode, so no layer sets it, and the refusal is D834's
// sentence — the one `fugaro rigor set` gives it (rigorHotfixRefusal, rigor.go)
// rather than a second spelling of it.
func setupRigorMode(v string) (rigor.Rigor, error) {
	mode := rigor.Rigor(strings.TrimSpace(v))
	if !rigor.Known(mode) {
		return "", errors.New("not a rigor mode: use " + rigorAnswerModes())
	}
	if mode == rigor.Hotfix {
		return "", errors.New(rigorHotfixRefusal)
	}
	return mode, nil
}

// setupRigorRefusal is --rigor on a run that is not a first run of `fugaro
// setup`. The mode of a project that already exists is the project's own — it
// is asked once, where the installation is created — so the flag is refused by
// name rather than honoured as a second door to the same key, and the sentence
// names the one command that does change a project's default.
func setupRigorRefusal(answer string) error {
	if answer == "" {
		return nil
	}
	return userErr("--rigor is a first run's flag: this installation already has a project config, so its rigor mode is asked once, when the installation is created, and the one command that changes a project's default is `fugaro rigor set %s`; nothing was asked and nothing was written", pluginwire.Printable(answer))
}

// updateRigorRefusal is --rigor on `fugaro update`, the door that creates
// nothing (setupCreates). The mode is the question of the run that CREATES the
// installation, so this door asks it nowhere and its Layer stage writes no
// answer — a value left on the run's options has no reader there, and the walk
// that would carry it never passes the question (the installation settles the
// name and returns, or --name settles it): it would be spliced into
// `defaults.rigor.default` and published by a run nobody asked. Refused by
// name instead, before anything is read, in the sentence that names the one
// command that does change a project's default.
func updateRigorRefusal(answer string) error {
	if answer == "" {
		return nil
	}
	return userErr("--rigor is a first run's flag: fugaro update re-converges an installation that is already there and creates nothing, so a project's rigor mode is set when `fugaro setup` creates it, and the one command that changes a project's default is `fugaro rigor set %s`; nothing was asked and nothing was written", pluginwire.Printable(answer))
}

// askRigor is the wizard's rigor question (task W of
// docs/rigor-command/implementation-plan.md; D900): the project's default mode,
// asked once, with no suggestion and no silent default — Enter prints "a value
// is needed" and the question is asked again (initRun.prompt with an empty
// suggestion), which is the state the owner's ruling of 2026-10-31 ends.
//
// It asks nothing where nobody can answer (canAsk: --json, a pipe): the flag
// is in the run's missing list there
// (wizardInputs), so such a run is refused with `--rigor <mode>` rather than
// given the recommendation in the caller's place — a scripted setup is exactly
// where a default nobody typed would be least visible.
//
// Only the run that creates the installation is asked. A project config that
// is already selected never arrives here — ask refuses --rigor with the one
// sentence that names `fugaro rigor set`, and asks nothing — and a run that
// creates nothing (--config-only, --print-vars) has no installation of its own
// to hold a mode and no layer it writes.
//
// The answer is recorded on --rigor's own field, which is where a scripted run
// answers the same question and where the Layer stage reads it from — this
// phase applies nothing.
func (s *wizardStage) askRigor() error {
	r, o := s.r, s.r.o
	if !o.createProject {
		return nil
	}
	if o.rigor != "" {
		// The flag answered the question, and its value is read in the
		// question's own words: a near miss and hotfix are refused here with
		// the sentences they are refused with when they are typed, so the flag
		// is not a looser door to the same key.
		mode, err := setupRigorMode(o.rigor)
		if err != nil {
			// ": " and not " is ": D834's sentence names hotfix itself
			// ("hotfix is not a mode a layer sets"), so the flag's own line
			// cannot complete it with a second "is".
			return userErr("--rigor %s: %v", pluginwire.Printable(o.rigor), err)
		}
		o.rigor = string(mode)
		return nil
	}
	if !r.canAsk() {
		return nil
	}
	q := questionFor("--rigor")
	s.a.asked = append(s.a.asked, q.label)
	fmt.Fprintln(r.w, rigorQuestionLead)
	r.printRigorChoices()
	v, err := r.askValue(wizardAsk{label: q.label, empty: "type one of the modes above (this is the one question with no suggestion)"}, valueSpec{what: q.what, flag: q.flag}, func(v string) error {
		_, verr := setupRigorMode(v)
		return verr
	})
	if err != nil {
		return err
	}
	mode, err := setupRigorMode(v)
	if err != nil {
		// The prompt validated the same value one line above, so this cannot
		// be reached; a mode is stored only where it read back as a mode.
		return err
	}
	o.rigor = string(mode)
	return nil
}

// askBudget is the wizard's budget question (T2b of
// docs/managed-flag-ergonomics; D2210, D2211, D2212): the monthly amount the
// project's budget alert is created with, asked once after the rigor question,
// with the default amount as the suggestion Enter takes.
//
// The answer goes onto --budget's own fields, which is where a scripted run
// answers the same question and where the installation's alert is created from
// (installOptions, init.go), and the currency the amount is in is the default
// one (D2211): the question asks what the owner is warned at, and an amount
// without a currency is one no alert can be created with. The answers file
// fills the same two fields and may answer either on its own (setup_answers.go,
// R4).
//
// It asks nothing where nobody can ask (canAsk: --json, a pipe, --yes, a run
// that never prompts) and nothing on a run that does not create the
// installation — a run with a project config, `fugaro update` too — which is
// askRigor's rule and askBake's: the question is about the project this run
// brings up, and a project that is already there keeps the budget its own. A
// run that cannot ask takes the default itself, which is what D2212 says the
// answer is wherever nobody chose one: nothing waits, and no flag is demanded
// (the row's missing is empty).
//
// The account the alert is on is settled once the billing question has
// answered, because that is where the linked account appears (settleBudget): a
// budget is an alert on a billing account, and the answer this question collects
// must not reach the installation without one.
func (s *wizardStage) askBudget() error {
	r, o := s.r, s.r.o
	if !o.createProject || o.budget > 0 {
		return nil
	}
	if !r.canAsk() {
		o.budget, o.budgetCurrency = managed.DefaultBudgetAmount, managed.DefaultBudgetCurrency
		return nil
	}
	q := questionFor("--budget")
	s.a.asked = append(s.a.asked, q.label)
	v, err := r.askValue(wizardAsk{label: q.label, hint: q.hint, def: strconv.FormatInt(managed.DefaultBudgetAmount, 10)}, valueSpec{what: q.what, flag: q.flag}, func(v string) error {
		_, verr := budgetAnswer(v)
		return verr
	})
	if err != nil {
		return err
	}
	amount, err := budgetAnswer(v)
	if err != nil {
		// The prompt validated the same value one line above, so this cannot be
		// reached; an amount is recorded only where it read back as one.
		return err
	}
	o.budget, o.budgetCurrency = amount, managed.DefaultBudgetCurrency
	return nil
}

// budgetAnswer is the budget question's own reading of an answer: a whole
// number of units, in the range the alert's amount may take — which is the
// marker's own rule (managed.Budget.Check, T1's constants), so a figure the
// question takes is one the installation and the creation fact take as well,
// and no later step refuses what a person just answered. What it does not read
// is a currency: the question asks for the amount and takes the default
// currency (D2211).
func budgetAnswer(v string) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return 0, errors.New("answer a whole number of units, such as " + strconv.FormatInt(managed.DefaultBudgetAmount, 10))
	}
	if err := (managed.Budget{Amount: n, Currency: managed.DefaultBudgetCurrency}).Check(); err != nil {
		return 0, err
	}
	return n, nil
}

// bakeAnswer is the bake question's own reading of an answer: the two things it
// has to say — yes and no, which is how the question is asked — and the
// spellings the same switch is written in elsewhere, since --bake-repos and
// `fugaro settings set bake_repos` both take true and false for it. The
// suggestion arrives here as "yes": prompt put the empty answer's own word in
// its place. Anything else is the error prompt prints before asking again.
func bakeAnswer(v string) (bool, error) {
	switch strings.ToLower(v) {
	case "y", "yes", "true":
		return true, nil
	case "n", "no", "false":
		return false, nil
	}
	return false, errors.New("answer yes or no")
}

// noteBakeRebuild is what a run says once it has written the bake switch: the
// switch is read by the BUILD, so it changes nothing about the images that
// already exist — each one keeps what it was built with until it is rebuilt
// (the daily image check rebuilds one whose inputs changed, and `fugaro image
// build <entry>` rebuilds one now). It is printed for either answer and on
// either route to one — the question above, and --bake-repos on a scripted run
// — because the reader of a switch that costs a rebuild is owed the rebuild,
// and it names the settings route for the same reason: the later change is
// `fugaro settings set bake_repos`, and it needs this same rebuild.
func (r *initRun) noteBakeRebuild() {
	fmt.Fprintf(r.w, "note: the bake switch is what the BUILD reads, so the images that exist keep what they were built with until they are rebuilt (the scheduled image check rebuilds one whose inputs changed, and fugaro image build <name> rebuilds one now); changing it later is fugaro settings set bake_repos <true|false> plus the same rebuild\n")
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
// rest of the run reads it: --create-project's own field. There is no question
// to ask: a first run of fugaro setup makes its project Fugaro's — it creates
// it, or adopts one that is already there and holds nothing of Fugaro's (the
// owner's ruling of 2026-11-22; the project stage, init_project.go, refuses an
// ID that holds anything of Fugaro's before anything is changed), and the flag
// only says so explicitly.
//
// A run that cannot act on a creation does not get one: `fugaro update` creates
// nothing (its stage set has no project stage at all, init_stages.go), and
// --config-only, --print-vars and --publish-config write, print or publish from
// an installation that is already there and touch no project. firstRun is
// whether this run selected no project config (wizardStage.creating): a run
// with one was created already.
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
	if o.setupCreates(firstRun) {
		o.createProject = true
	}
	return o.checkCreateProject()
}

// setupCreates is whether this run stands a new installation up, and therefore
// whether it is the run the wizard's questions about a new project are asked
// on: settleCreation's own condition, named once so the questions the phase
// asks (wizardInputs' missing list, askRigor) and the creation the settle
// records cannot drift apart.
//
// It is not --create-project: that field is what this SETTLES, and a run reads
// it to act. A first run of `fugaro setup` creates, and so does nothing else:
// `fugaro update` never creates, and --config-only, --print-vars,
// --publish-config and --forget work on an installation that is already there
// — writing this machine's config, printing its variables, publishing its
// shared config, or taking it apart — and touch no project.
func (o *initOptions) setupCreates(firstRun bool) bool {
	return firstRun && o.door != updateDoor.name && !o.configOnly && !o.printVars && !o.forget && !o.publishConfig
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

// announceGhHint is setup's one line about `gh`: the admin's run is the one
// moment to say that filing an issue on GitHub needs it, and that everything
// else about issues works without it (spec 4b, F18). It is a recommendation
// and never a requirement — a team member without `gh` still saves issues and
// uploads them to the project's store, and `fugaro issue submit` names the
// install command for whoever wants to file one — so it is printed once, at
// the end of the run, and asks nothing.
//
// It says nothing with --json (stdout is one object) or --print-vars (stdout is
// the variables alone), the two modes whose stdout is a document rather than a
// transcript, and nothing on the update door: an installation that is already
// there is not where the recommendation belongs.
func (e *initEngine) announceGhHint() {
	o := e.r.o
	if o.door != setupDoor.name || o.asJSON || o.printVars {
		return
	}
	fmt.Fprintf(e.r.w, "%sto file an issue on GitHub, install `gh` and run `gh auth login`; it is never required — fugaro saves and uploads issues without it\n",
		o.programPrefix())
}

// firstRun is whether this converge is a first run: no project config yet,
// or the one a first run of setup wrote when it paused for billing
// (localcfg.Config.SetupPending), which the rerun resumes as a first run.
func (e *initEngine) firstRun() bool {
	return e.old == nil || (e.lc != nil && e.lc.SetupPending != "")
}

// programPrefix is the line's own "fugaro setup: ", or "" for a hand-built
// options value — a test's — whose run has no door to name.
func (o *initOptions) programPrefix() string {
	if p := o.loopProgram(); p != "" {
		return p + ": "
	}
	return ""
}

// printRunHeader is the run's own heading, drawn the way every section of the
// CLI is (report.Style.Header): what this run does, and the installation it is
// about. It goes with the first thing the run says — the first question of a
// first run, or the converge's first block on a run that asks nothing — and
// never twice (initRun.headed).
//
// name is the installation this run knows itself by, and suggested is the
// wizard's own first read of it, for the header a run prints before its name
// question has been answered: --name wins, then whatever the wizard found.
//
// It says nothing with --json (stdout is one document) or --print-vars (stdout
// is the variables alone), the two modes whose stdout is not a transcript; the
// fact is in the result either way.
func (r *initRun) printRunHeader(name, suggested string) {
	o := r.o
	if r.headed || o.asJSON || o.printVars {
		return
	}
	r.headed = true
	if name == "" {
		name = suggested
	}
	fmt.Fprintln(r.w, r.qs.Header(o.runTitle(name)))
}

// runTitle is the header's text: the door's own verb, the installation's name
// where this run knows one, and the GCP project it is about. A part this run
// cannot fill in yet is left out rather than guessed: the header is printed
// before the questions that settle it.
func (o *initOptions) runTitle(name string) string {
	verb := "Setting up"
	if o.door == updateDoor.name {
		verb = "Updating"
	}
	switch {
	case name != "" && o.cloud.gcpProject != "":
		return fmt.Sprintf("%s %s (GCP %s)", verb, name, pluginwire.Printable(o.cloud.gcpProject))
	case name != "":
		return verb + " " + name
	case o.cloud.gcpProject != "":
		return fmt.Sprintf("%s (GCP %s)", verb, pluginwire.Printable(o.cloud.gcpProject))
	}
	return verb
}
