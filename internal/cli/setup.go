package cli

import (
	"github.com/spf13/cobra"
)

// setup.go is `fugaro setup`: the first door of the converge, and the one
// that asks (task W1.2 of docs/setup-wizard/implementation-plan.md; the
// wizard's questions and its real stage set are W1.3's).
//
// setup and update are two commands over one engine — init.go's
// newInitCmdWith, which is the constructor both are built by — because they
// are two moments with different answers, not two spellings of one: setup
// stands an installation up or adopts one and may create a GCP project, a
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
Terraform, adopting what the bootstrap already made, and applies the plan it
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
an existing or adopted config is never changed by it, and with a service
account's credentials it stops and names the two flags.

On a first run in a terminal it asks, once, for the project's name, the GCP
project's ID and the region, each with a suggestion (the name from the
repository's owner; the ID from GOOGLE_CLOUD_PROJECT or the credentials' quota
project, never gcloud's default project; us-east5) and writes the local config,
so later runs ask nothing; without a terminal, or with --non-interactive,
--yes or --json, it names every missing flag in one error. Where the
installation already exists and this machine has no local config for it,
setup adopts it: it writes the config from the installation and applies
nothing, and says which roles to ask an owner for. There the name is asked
after the GCP project and the region and defaults to the installation's own
(from the runs bucket's marker), not the directory's, and the budget section
(rtdb_url, firebase_project, firebase_api_key, token_signer: none a secret) is
filled from the Firebase root's outputs, read only; --name must be the
installation's name. With several project configs, --name of a project that
has none is a first run of that name, and a checkout whose origin repository
exactly one project lists selects that project.

Before the first billable image build of a GitHub repository, setup asks GitHub
(as the App, with the App's key read from Secret Manager into memory only)
whether the App is installed on the repository with the permissions runs ask
for (Contents: Write, Pull requests: Write, Issues: Read, Metadata: Read): if
not, the build is left for you with each missing permission and its fix and
nothing is billed. A repository the local config does not list is asked about
first, at the start, before any plan.

setup runs as a converge of stages (preflight, installation, with --firebase
the Firebase backend and the images, the secrets, the plugin wiring and the
repository), each with its own confirmation, in order,
and stops at the first that fails or needs you; a rerun resumes, and one
with nothing to do says "No changes" and exits 0. Exit codes: 0 done (or
only planned), 1 a refusal, or a step left for you (see left_for_you in
--json), 2 a cloud failure; --json prints the stages, left_for_you and the
failed stage on every outcome.

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

--forget is the rollback: it turns log isolation and registry cleanup off
with a guarded apply, then removes every address from Terraform's state,
destroying nothing else.

setup --firebase <firebase-project-id> builds the project's budget backend in a
Firebase project you created and linked to billing. It may be the project's
own GCP project (one project for everything) or a project of its own (setup
creates a project or links billing only with --create-project and
--link-billing, and refuses one that is missing, has no billing, or whose
database holds data and no Fugaro mark). It runs three applies, each
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
refuses to adopt a
database that holds data or is in another location; it also deploys
deny-everything Firestore rules and the Fugaro mark. --plan-only stops after the installation's plan. Each repository then
needs fugaro setup --repo to pick up the jobs' environment.

setup --repo [PATH] onboards the repository of the checkout at PATH (default:
the current directory), whose fugaro.yaml says what it needs, into the
installation fugaro setup applied. It adopts what the bootstrap made for it
(checking each resource's marks and live grants first), creates the rest,
and deploys each workflow's job once its secrets are stored and its image is
built. It offers each workflow's first image build (billable: the project's name
typed at a real terminal, never --yes), then deploys the built image and unpauses the daily check. It
prints the fugaro secrets set commands still needed, and adds the
repository to the local config. A repository the local config does not list
takes the same opt-in as the converge's stages (below): its owner/name typed at
your own terminal, or --onboard-repo.

One confirmation per run, in a project Fugaro owns. A project is Fugaro's own
when this run created it (--create-project) or its runs bucket carries Fugaro's
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
a registry tag (--replace-image), an unlisted repository or a secret: each
keeps its own typed confirmation. The state bucket and the API enable are fixed, announced, free steps; every
Terraform plan is shown as it runs and is covered only if it is creates and
in-place updates of the installation's own resource kinds, granting only the
modules' roles to the listed launchers, operators and budget admins (the review
prints them, with where each came from) or to Fugaro's own service accounts:
its attributes are the modules' (images in this project's registry, jobs of
this project, logs to this project, private buckets), and a Firebase project
other than the installation's is covered only if verified as Fugaro's; any other
plan (a destroy or replace, an IAM binding or policy, an unlisted member, a
widened role) stops that step, which asks its own typed name. A wrong name applies nothing.
The confirmation is per run and never stored. Any other project (adopted,
unmarked, or a mark that fails to read) keeps a typed name at each step;
--yes covers the ordinary steps as ever and takes no review; --non-interactive,
--json, a pipe and a coding agent never take it; --plan-only shows the review
and applies nothing.

What --yes covers: the ordinary steps, which are the installation's and the
Firebase root's Terraform applies, the images' copy, the local config's
writes, the plugin wiring and a known repository's steps. It never covers
creating a project or linking billing, a secret, a repository the project does
not list, the Firestore database's permanent location, a billable first image
build, or replacing a tag in your registry (--replace-image KIND names the
intent; the project's name is typed at a real terminal): with --yes,
--non-interactive, --json or a pipe each of those is left for you, with the one
line to run. In a coding agent's environment (CLAUDECODE, CLAUDE_CODE_ENTRYPOINT,
CLAUDE_CODE_SSE_PORT, CLAUDE_CODE_REMOTE, CURSOR_AGENT or AI_AGENT is set) setup
applies nothing, --yes included, and says to run it in your own terminal
window; --plan-only and the read-only checks still work, and so does setup
--anchor (with --yes), which edits only the checkout's fugaro.yaml. That is a mitigation,
not a barrier: an agent can unset its own environment. A run that adopted an
existing installation changes nothing in the cloud in the same run (the local
config and, in a checkout, the plugin wiring in .claude/settings.json are the
only files it writes).

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
setup --repo's Terraform adopts (fugaro, fugaro_repo, fugaro_secret).
--forget removes the repository from
Terraform's state, destroying nothing.

--create-project --gcp-project <id> creates that GCP project and adds Firebase
to it, as you, after you type its ID at a terminal; --link-billing <account>
then links that billing account (it may incur charges), after you type the
account's ID. Each is its own typed confirmation, never given by --yes,
--non-interactive or --json, and the ID is never the gcloud default project.
Without --link-billing, setup prints the one gcloud command that links billing
and stops. An existing project you can read is adopted; one that is held by
someone else, or pending deletion, is refused; its existing billing link is
never changed.

The plugin-wiring stage merges the Fugaro marketplace and plugin into the
checkout's .claude/settings.json, pinned to this release's tag, after showing
the diff and asking (--yes confirms it); see update-skills. The repository
stage runs setup --repo for the checkout, after the secrets, when its
origin's default branch (origin/HEAD, main or master, as last fetched) has a
fugaro.yaml naming this project (else it is skipped and says why); it asks
once for the GitHub App's ID of a GitHub repository (or take --github-app-id)
and keeps its own confirmations, the first image build's included.

Both stages act only on a repository the project's local config already lists,
or one you opt in: a repository not listed yet needs its owner/name typed at
your own terminal (naming the checkout's path), or --onboard-repo owner/name,
which must equal the origin (setup --repo takes both too); --yes,
--non-interactive and a coding agent's environment never cover it (the flag is
refused there), so a cloned third-party repository cannot onboard itself. The
secrets stage is behind the same gate.`

// setupDoor is the first door: the wizard, and the only door that creates.
var setupDoor = convergeDoor{
	name:  "setup",
	use:   "setup [--repo [PATH]]",
	short: "Stand up or adopt the installation's, or a repository's, cloud resources through Terraform",
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
