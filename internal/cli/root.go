// Package cli implements the fugaro command-line interface.
package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/template"

	"github.com/spf13/cobra"
)

// Version is set at build time with
// -ldflags "-X github.com/dimipaun/fugaro/internal/cli.Version=<version>".
var Version = "dev"

// Exit codes are part of the CLI contract (design §9.1).
const (
	ExitOK          = 0
	ExitUserError   = 1
	ExitRemoteError = 2
)

// ExitError carries a specific process exit code out of a command.
type ExitError struct {
	Code int   // Code is the process exit code (design §9.1).
	Err  error // Err is the underlying error, or nil if Code alone explains the exit.
}

// Error implements error. When Err is nil, it falls back to a message naming
// the exit code, so a bare &ExitError{Code: ...} still prints something useful.
func (e *ExitError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit code %d", e.Code)
	}
	return e.Err.Error()
}

// Unwrap exposes Err so errors.As and errors.Is see through an ExitError.
func (e *ExitError) Unwrap() error { return e.Err }

// ExitCode maps an error returned by a command to a process exit code.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return ExitUserError
}

// versionTemplate renders the version line: the Version variable and a
// newline, which is exactly what `fugaro version` prints. It is the one
// definition of that line. Cobra renders it for the root's `--version` and
// `-v` flags (its own Version-field machinery), and newVersionCmd renders the
// same template through writeVersion, so the spellings cannot drift apart.
//
// Cobra reads this through text/template rather than through a Go function,
// which is why the line is a template here and not a format string.
//
// It calls versionTemplateFuncs' function rather than printing {{.Version}}:
// cobra renders a version template with the command as its data, and the
// command's Version field holds the value the variable had when NewRootCmd
// built the tree. The function reads the variable when the line is printed,
// which is the variable's value at the only moment that matters -- the build
// sets it with -ldflags before NewRootCmd runs, and the package's own tests
// move it (useVersion, withVersion, releaseBuild, newImagesRigFor) to check
// what a given version prints.
const versionTemplate = "{{fugaroVersion}}\n"

// versionTemplateFuncs are the functions versionTemplate uses. Cobra keeps its
// template functions in a package-level map with no per-command way to add to
// it, so the same map is registered with cobra (for the flag cobra answers)
// and handed to writeVersion's own template (for the subcommand).
var versionTemplateFuncs = template.FuncMap{
	"fugaroVersion": func() string { return Version },
}

func init() { cobra.AddTemplateFuncs(versionTemplateFuncs) }

// writeVersion prints the version line to w, by rendering root's own version
// template with the root command as its data -- the same two steps cobra takes
// for `fugaro --version` (cobra.go's tmpl: template.Must(Parse(text)) then
// Execute(w, cmd)) -- so `fugaro version` and `fugaro --version` are one
// implementation rather than two copies of one line.
func writeVersion(root *cobra.Command, w io.Writer) error {
	t := template.New("version")
	t.Funcs(versionTemplateFuncs)
	template.Must(t.Parse(root.VersionTemplate()))
	return t.Execute(w, root)
}

// NormalizeVersionArgs is argv with a LEADING `-v` or `--version` reduced to
// `--version` alone, so that the flag answers and nothing else is reached.
//
// Cobra resolves the command before it parses any flag -- ExecuteC calls Find,
// which reads the argv through stripFlags -- and at that moment the root's
// version flag does not exist yet, because InitDefaultVersionFlag adds it
// inside execute. stripFlags therefore reads a `--version` it does not know as
// a flag that takes a value and swallows the token after it: `fugaro --version
// run run` resolves `run` and fails there on `unknown flag: --version`,
// `fugaro --version junk logs` resolves `logs`, and `fugaro -v logs logs`
// resolves `logs` and fails on `unknown shorthand flag: 'v'`. The argv
// boundary is the one place that can tell the flag from what follows it,
// which is why cmd/fugaro/main.go and the tests' execute helpers call this
// before cobra parses anything -- the same reason they call
// NormalizeRepoArgs there.
//
// Only a leading flag is rewritten: the version flag is the root's own, like
// the built-in `-h`, so `fugaro run --version` stays the unknown flag it is,
// and `-h` still wins when it comes first.
func NormalizeVersionArgs(args []string) []string {
	if len(args) > 0 && (args[0] == "--version" || args[0] == "-v") {
		return []string{"--version"}
	}
	return args
}

// NormalizeArgs is argv with the rewrites the argv boundary owes cobra: the
// bare --repo (NormalizeRepoArgs) and a leading version flag
// (NormalizeVersionArgs).
func NormalizeArgs(args []string) []string {
	return NormalizeVersionArgs(NormalizeRepoArgs(args))
}

// NewRootCmd builds the fugaro command tree: the released commands, then what
// this build registered for them and the stubs for the paid names it carries
// no code for (extend.go). With nothing registered the tree is exactly the
// released one.
//
// The managed fence's classification is applied last, over the finished tree
// rather than over the core one: what a build shows for a paid name is a
// runnable command like any other, and a table written before the stubs were
// added would leave every one of them unclassified (root.go's annotation).
func NewRootCmd() *cobra.Command {
	root := newCoreRoot()
	applyExtensions(root)
	annotateManaged(root)
	return root
}

// managedFencedCommands are the commands that may not write to a project that
// is not managed (D2217), keyed by cobra's own command path under the root
// (consoleReadPaths' shape in project.go), each with the opening clause its
// refusal prints: what it declines and what it is. The refusal itself is
// managed_require.go's, which reads the clause from the annotation this table
// becomes, and it names the one command that flags an existing project managed
// (D1541).
//
// Fenced means one thing and one thing only: this command writes to the GCP
// project — an object in its runs bucket, a node of its database, a secret, a
// job or a run in it, a resource of its own — so an unmanaged project is not
// one it may touch. A command that writes only this machine's files, or only
// the checkout's, is not fenced whatever it changes: the cloud work its change
// implies is `fugaro update`'s, and update is fenced where it stands. A command
// that only reads is not fenced (A5): it is what tells an owner their project
// is unmanaged.
//
// The annotation is applied to the tree rather than written at each command's
// own constructor so that the answer to "which commands refuse an unmanaged
// project" is one table rather than a search: a command added here is annotated
// by being added, and the sentence a command refuses with is declarative text
// beside the list of commands that say it. The table is held complete both ways
// by TestEveryWritingCommandIsClassified, which walks the tree.
//
// Two commands are missing from it on purpose, and each says so in its own
// exemption below: `setup`'s creating run, which is the command that MAKES a
// project managed, and `update`, which stops on an unmanaged installation in
// its own engine (T3) rather than through the annotation. `fugaro apply` is
// here: it opens no cloud client of its own — it goes straight to the init
// engine — so its fence is called where its own run starts.
var managedFencedCommands = map[string]string{
	"accounts add":                "nothing was added: an account's secret and the job that serves it live in the project",
	"accounts clear":              "nothing was cleared: the marks this clears are the project's own record of which account a run used",
	"accounts rm":                 "nothing was removed: an account's secret and the job that served it live in the project",
	"allow-delete":                "nothing was recorded: the delete list is a leaf of the policy the project publishes",
	"apply":                       "nothing was applied: apply runs the follow-up steps a management command saved against the project",
	"backlog add":                 "nothing was filed: an item is written into the project's own orchestrator state",
	"budget chain raise":          "nothing was raised: the raise rewrites a run's stored task in the project's runs bucket",
	"budget kill":                 "nothing was halted: the kill switch is written into the project's own budget database",
	"budget resume":               "nothing was resumed: the kill switch is cleared in the project's own budget database",
	"budget set":                  "nothing was set: the caps, the lease limit and the mode live in the project's own budget database",
	"cancel":                      "nothing was cancelled: a run's record lives in the project's runs bucket",
	"config publish":              "nothing was published: the project layer lives in the project's runs bucket",
	"creds set":                   "nothing was stored: the credentials bundle is a Secret Manager secret of the project",
	"image build":                 "no build was submitted: a build spends the project's Cloud Build quota and writes its record into the project",
	"image refresh":               "nothing was refreshed: a refresh pins, rebuilds and re-points the project's own images",
	"issue dismiss":               "nothing was dismissed: an issue the project's store carries is set aside there as well as here, so the row that says so is written into the project",
	"issue sync":                  "nothing was uploaded: the project's issue store is in the project's runs bucket",
	"managed apply":               "nothing was applied: a managed apply changes the project's own Terraform root",
	"orchestrator import":         "nothing was loaded: the graph is written into the project's own orchestrator state",
	"orchestrator patch":          "nothing was patched: the state node is the project's own",
	"orchestrator run":            "no daemon was started: it takes a lease on the project and writes its state",
	"pair":                        "nothing was paired: the pairing record is written into the project's own database",
	"pair revoke":                 "nothing was revoked: the pairing record is dropped from the project's own database",
	"project policy publish":      "nothing was published: the policy node is the project's own",
	"project repos add":           "nothing was enrolled: the enrolment converges the repository's cloud resources in the project",
	"project repos rm":            "nothing was un-enrolled: the enrolment's jobs and its allow-list entry live in the project",
	"project sync":                "nothing was added: the offer is written into the project's published allow-list, which is what admits a repository to it",
	"recipes publish":             "nothing was published: a recipe is written into the project's runs bucket",
	"recipes rm":                  "nothing was removed: a recipe is removed from the project's runs bucket",
	"reaper":                      "nothing was swept: the reaper deletes the project's leaked jobs and VMs",
	"restore":                     "nothing was restored: a restore rebuilds a managed project",
	"review":                      "no review was launched: it runs as a job of the project",
	"run":                         "no run was launched: a run spends the project's budget and writes into its runs bucket",
	"runs digest":                 "nothing was resolved: the digest's issue pass writes the project's own store",
	"runs reindex":                "nothing was reindexed: the reset writes the project's own index and starts its history job",
	"secrets rm":                  "nothing was withdrawn: the container is a Secret Manager secret of the project",
	"secrets set":                 "nothing was stored: the value is a new version of a Secret Manager secret of the project",
	"secrets shared rm":           "nothing was removed: the secret is a Secret Manager secret of the project",
	"secrets shared set":          "nothing was stored: the value is a Secret Manager secret of the project",
	"secrets user rm":             "nothing was removed: the secret is a Secret Manager secret of the project",
	"secrets user set":            "nothing was stored: the value is a Secret Manager secret of the project",
	"user grant":                  "nothing was granted: the grant is written into the project's own grants node",
	"user revoke":                 "nothing was revoked: the grant is dropped from the project's own grants node",
	"workspace domains add":       "nothing was written: the allowed domains are a node of the workspace's own project",
	"workspace domains rm":        "nothing was written: the allowed domains are a node of the workspace's own project",
	"workspace init":              "nothing was created: the folder, the owner binding and the roster are created in the workspace's own project",
	"workspace project add":       "nothing was declared: a member's row is written into the member project's own cloud",
	"workspace project link":      "nothing was linked: linking moves the project under the workspace's folder and files the change in the member's own store",
	"workspace project reconcile": "nothing was written: the change lands in the member's own roster",
	"workspace project rm":        "nothing was dropped: a member's row is dropped from the member project's own cloud",
	"workspace project unlink":    "nothing was unlinked: unlinking moves the project back and drops the workspace's binding on it",
	"workspace user add":          "nothing was added: the row is written into the workspace's own roster and the member's own mirror",
	"workspace user grant":        "nothing was granted: the row is written into the member's own grants node",
	"workspace user revoke":       "nothing was revoked: the row is dropped from the member's own grants node",
	"workspace user rm":           "nothing was removed: the row is dropped from the workspace's own roster and the member's own mirror",
}

// managedExemptCommands are the commands the fence does not apply to, keyed the
// same way, each with the one-line reason it is exempt (D2217's exemptions,
// D2219, D3160, R8 and A5).
//
// The reasons are three kinds, and the difference between them matters to the
// next reader. "It writes nothing in the project" is a settled answer about a
// command that changes this machine's files, the checkout's files or a
// repository on the forge, and about a command that only reads (A5): the cloud
// work its change implies is a later `fugaro update`, which is fenced. "It runs
// on the project's own machinery" is a settled answer about a command a Fugaro
// job, service or Cloud Build step runs on itself, under the installation's own
// account: the run that launched it was fenced, and an unmanaged project has
// none of that machinery to run. The named exemptions — setup's creating run,
// managed flag, managed unflag, user offboard, the four in-run commands — are
// decisions, and each reason names the one it is.
var managedExemptCommands = map[string]string{
	"ab":                         "a paid name: the stub this build shows refuses in one line and runs nothing, and the Subscription Edition's own command is not this table's to classify",
	"accounts":                   "a parent command: it prints its own help, and the reading is accounts ls beside it",
	"accounts alias":             "a parent command: it prints its own help, and the work is in set and rm beside it",
	"accounts alias rm":          "it writes this machine's project config, and nothing in the project",
	"accounts alias set":         "it writes this machine's project config, and nothing in the project",
	"accounts allow":             "it writes an allow-list leaf of this machine's project config, and nothing in the project",
	"accounts cascade":           "a parent command: it prints its own help, and the work is in set beside it",
	"accounts cascade set":       "it writes this installation's own config, and nothing in the project",
	"accounts craft":             "a parent command: it prints its own help, and the work is in set and rm beside it",
	"accounts craft rm":          "it writes this machine's project config, and nothing in the project",
	"accounts craft set":         "it writes this machine's project config, and nothing in the project",
	"accounts deny":              "it writes an allow-list leaf of this machine's project config, and nothing in the project",
	"accounts grade":             "a parent command: it prints its own help, and the work is in set beside it",
	"accounts grade set":         "it writes this machine's project config, and nothing in the project",
	"accounts ls":                "it only reads: a listing of the accounts this installation names",
	"accounts pool":              "a parent command: it prints its own help, and the work is in ls, add and rm beside it",
	"accounts pool add":          "it writes this machine's project config, and nothing in the project",
	"accounts pool ls":           "it only reads: a listing of the pools and their members",
	"accounts pool rm":           "it writes this machine's project config, and nothing in the project",
	"accounts repo":              "a parent command: it prints its own help, and the work is in add beside it",
	"accounts repo add":          "it writes a claude_accounts leaf of this machine's project config, and nothing in the project",
	"accounts switch":            "it rewrites the account choices in this machine's own config, and nothing in the project",
	"artifacts get":              "it only reads: it downloads what a run's record says it delivered",
	"artifacts ls":               "it only reads: a listing of what a run's record says it delivered",
	"backends":                   "a parent command: it prints its own help, and the reading is backends ls beside it",
	"backends ls":                "it only reads: a listing of the backends this fugaro knows",
	"backends use":               "it writes this machine's project config, and nothing in the project",
	"backlog index":              "it writes the checkout's docs/backlog tree, and nothing in the project",
	"backlog list":               "it only reads: a listing of the project's backlog items",
	"budget history":             "the project's own history job runs it: it writes the project's own registry and reads its own runs",
	"budget preview":             "it only reads: an estimate against today's caps, printing a table",
	"budget prices":              "it only reads: the price table is built in, and it contacts nothing",
	"budget reconcile":           "it only reads: a listing of each run's charged, reported and table spend",
	"budget show":                "it only reads: a listing of the caps, counters, kill switches and headroom",
	"config example":             "it only reads: it prints the annotated fugaro.yaml template whole",
	"config init":                "it writes the checkout's fugaro.yaml, and nothing in the project",
	"config layer":               "it only reads: the project layer published for this checkout",
	"config list":                "it only reads: a listing of the installations this machine has",
	"config show":                "it only reads: the checkout's fugaro.yaml resolved over the project layer",
	"coordinator":                "a paid name: the stub this build shows refuses in one line and runs nothing, and the Subscription Edition's own command is not this table's to classify",
	"craft ls":                   "it only reads: a listing of each craft's effective model and the chain it goes out on",
	"credentials serve":          "the credentials service's own image runs it: it serves the project's own credentials",
	"creds":                      "a parent command: it prints its own help, and the work is in set and ls beside it",
	"creds ls":                   "it only reads: the names and metadata of the bundle's credentials, never a value",
	"diagnose":                   "it only reads: how one run went, from its record and its logs",
	"doctor":                     "it only reads: it is the screen that tells an owner their project is not managed",
	"escalation ls":              "it only reads: the ladder's ledger per pull request (D2219: the run's own records)",
	"escalation show":            "it only reads: one pull request's escalation ledger (D2219: the run's own records)",
	"estimate":                   "it only reads: one run's recorded tokens repriced on another model",
	"exec":                       "the container entrypoint runs it: it is the run's own process, launched by a fenced fugaro run (D2219)",
	"followups":                  "the run files its own deferred findings (D2219: the run's own records)",
	"graph":                      "a paid name: the stub this build shows refuses in one line and runs nothing, and the Subscription Edition's own command is not this table's to classify",
	"image add":                  "it writes an image catalog entry in this machine's project config, and nothing in the project",
	"image check":                "locally it only reads, printing the rebuild decision and building nothing; a run of the project's own catalog check job is the same command's --job arm, and that arm records each entry's decision in the project's bucket (D2219: the run's own records)",
	"image gate":                 "a Cloud Build step runs it: it refuses a promotion and writes nothing at all",
	"image git-credential":       "a Cloud Build step runs it: it writes a credential file on the build's own disk",
	"image ls":                   "it only reads: a listing of the project's image catalog entries",
	"image map set":              "it writes this machine's project config, and nothing in the project",
	"image promote":              "it copies a definition into this machine's project config and the checkout's fugaro.yaml",
	"image record":               "a Cloud Build step runs it: it records what the build built, in the project the build runs in",
	"image render":               "it only reads: it prints the Dockerfile that builds the derived image",
	"image rm":                   "it writes this machine's project config, and nothing in the project",
	"image selftest":             "it only reads: a smoke test of the image this process runs in",
	"image status":               "it only reads: a listing of when each workflow's image was built",
	"image survey":               "it only reads: a listing of the repositories and the image signals each carries",
	"init":                       "it is retired: it refuses and names the two doors that replaced it",
	"intake serve":               "the secrets intake's own image runs it: it stores the project's own secrets",
	"issue list":                 "it only reads: a listing of the issues saved on this machine",
	"issue mine":                 "it only reads: a listing of the issues this user reported",
	"issue show":                 "it only reads: one issue's text",
	"issue submit":               "it files a GitHub issue and writes nothing in the project",
	"logs":                       "it only reads: a run's log lines from the project's Cloud Logging",
	"mailbox":                    "the run consumes its own mailbox (D2219: the run's own records)",
	"mailbox sweep":              "the project's own sweep job runs it: it deletes the project's own stored prompts",
	"managed flag":               "it is the command that makes an existing project managed: it is the way out, not a way in",
	"managed status":             "it only reads: a listing of a managed project's marks and its newest audit record",
	"managed unflag":             "a project that is not managed has nothing to unflag (R8)",
	"models add":                 "it writes this machine's project config, and nothing in the project",
	"models ls":                  "it only reads: a listing of the routes, models and repository choices",
	"models price set":           "it writes this machine's project config, and nothing in the project",
	"models provider rm":         "it writes this machine's project config, and nothing in the project",
	"models provider set":        "it writes this machine's project config, and nothing in the project",
	"models rm":                  "it writes this machine's project config, and nothing in the project",
	"models search":              "it only reads: it searches the built-in price catalog and the Claude table",
	"models use":                 "it writes the repository's own fugaro.yaml in its working tree, and nothing in the project",
	"orchestrator events":        "it only reads: the orchestrator's audit log",
	"orchestrator install":       "it writes the login unit on this machine, and nothing in the project",
	"orchestrator ls":            "it only reads: a listing of the installation's orchestrators",
	"orchestrator serve":         "the resident orchestrator's own image runs it: it is the project's own daemon",
	"orchestrator state":         "it only reads: the generated summary of the orchestrator's live node",
	"orchestrator status":        "it only reads: the login unit's file, and whether it is this release's",
	"orchestrator uninstall":     "it removes the login unit from this machine, and nothing in the project",
	"pair ls":                    "it only reads: a listing of the devices paired with this installation",
	"plan graph":                 "it only reads: a plan's tasks, their dependencies and their footprints",
	"plan status":                "it only reads: every plan's tasks joined to the runs working on them",
	"project binaries allow":     "it writes a binaries.allow leaf of this machine's project config, and nothing in the project",
	"project binaries deny":      "it writes a binaries.allow leaf of this machine's project config, and nothing in the project",
	"project binaries max-bytes": "it writes the binaries.max_bytes cap of this machine's project config, and nothing in the project",
	"project clone-url ls":       "it only reads: a listing of the clone URL each repository is given",
	"project clone-url rm":       "it writes this machine's project config, and nothing in the project",
	"project clone-url set":      "it writes this machine's project config, and nothing in the project",
	"project policy show":        "it only reads: the published policy, which is the answer itself",
	"project profile add":        "it writes this machine's project config, and nothing in the project",
	"project profile ls":         "it only reads: a listing of the project's job profiles",
	"project profile rm":         "it writes this machine's project config, and nothing in the project",
	"project profile set":        "it writes this machine's project config, and nothing in the project",
	"project profile show":       "it only reads: one job profile of this project",
	"project repos ls":           "it only reads: the allow-list and the repositories that have run here",
	"project repos set":          "it writes this machine's project config, and nothing in the project",
	"project repos status":       "it only reads: whether a repository can run, row by row",
	"project routing set":        "it writes this machine's project config, and nothing in the project",
	"project routing show":       "it only reads: the route this project's Claude calls take",
	"project routing unset":      "it writes this machine's project config, and nothing in the project",
	"project show":               "it only reads: the effective project, its caps, policy and allow-list",
	"recipes ls":                 "it only reads: a listing of the recipes and which one wins for each name",
	"recipes show":               "it only reads: the recipe a run would get for NAME",
	"recipes validate":           "it only reads: it checks a recipe file and contacts nothing",
	"report":                     "a paid name: the stub this build shows refuses in one line and runs nothing, and the Subscription Edition's own command is not this table's to classify",
	"review show":                "it only reads: a review run's verdict artifact and its markdown",
	"rigor floor set":            "it writes the project layer's own file on this machine, and nothing in the project",
	"rigor floor show":           "it only reads: the review floor this machine's project config sets",
	"rigor floor unset":          "it writes the project layer's own file on this machine, and nothing in the project",
	"rigor security add":         "it writes the project layer's own file or the checkout's fugaro.yaml, and nothing in the project",
	"rigor security rm":          "it writes the project layer's own file or the checkout's fugaro.yaml, and nothing in the project",
	"rigor set":                  "it writes the project layer's own file on this machine, and nothing in the project",
	"routing ls":                 "it only reads: the routing table and the entry each model goes out on",
	"routing rm":                 "it writes this machine's project config, and nothing in the project",
	"routing set":                "it writes this machine's project config, and nothing in the project",
	"runs ls":                    "it only reads: a listing of the runs with their status, stage, cost and PR",
	"runs reports":               "the project's own reports job runs it: it writes the project's own stored reports",
	"runs show":                  "it only reads: one run's chain and what is left of its limits",
	"runs stats":                 "it only reads: run records aggregated by the person who started each run",
	"runs summary":               "it only reads: the window's run count and spend",
	"runs watch":                 "it only reads: the triage the daemon would decide about the window's long runs",
	"seats":                      "it only reads: the estimate a subscription's seat rule would count",
	"secrets":                    "a parent command: it prints its own help, and the work is in set, ls and rm beside it",
	"secrets allow":              "it writes an allow-list leaf of this machine's project config, and nothing in the project",
	"secrets deny":               "it writes an allow-list leaf of this machine's project config, and nothing in the project",
	"secrets ls":                 "it only reads: the secrets' names, scope and versions, never a value",
	"secrets shared":             "a parent command: it prints its own help, and the work is in set, ls and rm beside it",
	"secrets shared ls":          "it only reads: the project-shared secrets' names and dates, never a value",
	"secrets user":               "a parent command: it prints its own help, and the work is in set, ls and rm beside it",
	"secrets user ls":            "it only reads: the caller's own secrets' names and dates, never a value",
	"security paths":             "it only reads: the pattern list in force, and a path's answer against it",
	"settings":                   "it only reads: a row per setting, each with the layer that decided it",
	"settings get":               "it only reads: one setting, what decides it and its default",
	"settings set":               "it writes one of this machine's three config documents, and nothing in the project",
	"settings unset":             "it writes one of this machine's three config documents, and nothing in the project",
	"setup":                      "setup's creating run: it is the command that creates a managed project, and a resumed creation is a creating run too; on an existing installation that is not managed its own engine stops it (T3)",
	"stats runs":                 "it only reads: it aggregates the window's run records and prints the numbers",
	"tell":                       "the run reads its own messages (D2219: the run's own records)",
	"troubleshoot":               "it only reads: the local diagnosis of a command that failed on this machine",
	"update":                     "its own engine stops it on an unmanaged installation, naming fugaro managed flag (T3), so this annotation is not the fence it answers to",
	"update-skills":              "it writes this machine's own Claude Code plugin, and nothing in the project",
	"user add":                   "it writes the installation's operator and admin lists in this machine's config, which fugaro update applies",
	"user ls":                    "it only reads: a listing of the installation's grants and the role held in a repository",
	"user offboard":              "taking access away never waits on marking a project first (D3160)",
	"user rm":                    "it writes the installation's operator and admin lists in this machine's config, which fugaro update applies",
	"validate":                   "it only reads: it checks a fugaro.yaml against the schema and the repository",
	"verify":                     "the run records its own build and test (D2219: the run's own records)",
	"version":                    "it only reads: it prints the version and nothing else",
	"voice serve":                "the voice hop's own image runs it: it serves the project's own calls",
	"watch":                      "it only reads: it draws a live screen of the project's spend and running agents",
	"workflow add":               "it writes a workflows block into the checkout's fugaro.yaml, and nothing in the project",
	"workflow rm":                "it removes a workflows block from the checkout's fugaro.yaml, and nothing in the project",
	"workspace":                  "a parent command: it prints its own help, and the work is in the subcommands beside it",
	"workspace add":              "it writes this machine's project config and hands the cloud work to fugaro update",
	"workspace cap set":          "it writes this machine's project config, and nothing in the project",
	"workspace domains ls":       "it only reads: the domains the workspace admits people from",
	"workspace login":            "it only reads: the sign-in this project's pin asks for",
	"workspace ls":               "it only reads: a listing of the workspaces this machine's configs name",
	"workspace project ls":       "it only reads: one line per member of this workspace",
	"workspace project save":     "it writes a snapshot to a local file and to the state repository's pull request, and nothing in the project",
	"workspace project status":   "it only reads: whether a member can run against this workspace, one row per fact",
	"workspace rm":               "it drops this project's membership key and pin from this machine's config, and nothing in the project",
	"workspace share":            "it writes the workspace owner's own project config, and nothing in the project",
	"workspace show":             "it only reads: one workspace from this machine's own files",
	"workspace unshare":          "it writes the workspace owner's own project config, and nothing in the project",
	"workspace user ls":          "it only reads: the workspace's members and the grants they hold",
}

// annotateManaged applies the classification to the tree as cobra annotations:
// the fenced commands get the opening clause their refusal prints, and the
// exempt ones get the reason they are exempt. A path that does not resolve is
// skipped rather than fatal, and the skip is a hole rather than a fallback:
// the annotation IS the fence's trigger (managedFenced reads it and nothing
// else), so a fenced command that lost its annotation is refused by nothing —
// no seam would ask about it — and a command whose clause went missing would
// refuse with managedRequired's generic sentence. What holds the tables
// against the tree is TestEveryManagedCommandResolves, which walks every path
// in both and fails on one that does not resolve.
func annotateManaged(root *cobra.Command) {
	for path, what := range managedFencedCommands {
		annotateManagedPath(root, path, managedRequiresAnnotation, what)
	}
	for path, why := range managedExemptCommands {
		annotateManagedPath(root, path, managedExemptAnnotation, why)
	}
}

// annotateManagedPath writes one annotation on the command path names.
func annotateManagedPath(root *cobra.Command, path, key, value string) {
	c, _, err := root.Find(strings.Fields(path))
	if err != nil || c == nil {
		return
	}
	if c.Annotations == nil {
		c.Annotations = map[string]string{}
	}
	c.Annotations[key] = value
}

// newCoreRoot builds the released tree. It is separate from NewRootCmd so that
// the extensions above are the only difference between the tree the core
// builds and the tree a build with paid commands ships.
func newCoreRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "fugaro",
		Short:         "Fleeting cloud workers for coding agents",
		SilenceUsage:  true,
		SilenceErrors: true,
		// Version is what makes cobra add the root's `--version` flag and
		// answer it, with the template below. Cobra answers it inside execute,
		// after the flag parse and before PersistentPreRun, so `fugaro
		// --version` prints the version and exits 0 without running anything:
		// no checkout read, no local config, no cloud call, no update check.
		// It is a flag of the root alone, like the built-in `-h`.
		//
		// That is not on its own enough to keep the flag from reaching a
		// subcommand, though: cobra resolves the command before it executes
		// one, and the flag does not exist yet when it does, so `fugaro
		// --version run` would resolve `run` and run it. NormalizeVersionArgs
		// is what closes that gap, at the argv boundary.
		//
		// The flag carries no `-v` shorthand: the persistent --verbose below
		// takes it, and cobra gives its version flag a shorthand only when one
		// is free. A leading `fugaro -v` is the version all the same, because
		// NormalizeVersionArgs rewrites it before cobra parses anything.
		Version: Version,
		// The plugin staleness line (design 4.5), the update-check kickoff
		// (docs/gcp-setup.md "Staying up to date") and the required-CLI fence
		// (required_cli.go). No command defines its own PersistentPreRun, which
		// would replace this one — the tree-walk test in required_cli_test.go
		// fails on one. The update-available nag is NOT printed from a
		// PersistentPostRun: cobra's Command.execute returns as soon as RunE
		// errors, skipping PostRun and PersistentPostRun entirely, so most
		// command failures would never carry it. cmd/fugaro/main.go instead
		// calls WarnUpdateAvailable itself, after ExecuteContextC returns,
		// whether or not it errored.
		//
		// It is a PersistentPreRunE because the fence has to refuse: a command
		// below its project's required CLI returns the refusal from here, and
		// cobra's Command.execute propagates a pre-run error exactly as it does
		// a RunE's — main.go prints it and exits 1. The fence is last, so
		// everything above it is what it always was, and so the flags it reads
		// the command's own project from are parsed.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			// A command the desktop console spawns as a read changes nothing
			// on this machine, so it does not write the remembered checkouts
			// either (see consoleReadPaths in project.go). Marked here, once
			// per command, from the command's own path.
			if consoleReadPaths[cmd.CommandPath()] {
				ctx = withConsoleRead(ctx)
			}
			// The command this invocation is about, for every seam that has to
			// ask which command it is serving: the managed fence reads it in
			// openCloudFor and requireManaged, where a helper's own frame knows
			// the config it was handed and nothing about the command running
			// (managed_require.go).
			ctx = withManagedCommand(ctx, cmd)
			cmd.SetContext(withCheckoutParseCache(ctx))
			warnStaleSkills(cmd)
			disabled := updateCheckDisabled(cmd)
			cmd.SetContext(withUpdateCheckDisabled(cmd.Context(), disabled))
			startUpdateCheck(cmd, disabled)
			return fenceCommand(cmd)
		},
	}
	root.SetVersionTemplate(versionTemplate)
	// The persistent --verbose (-v) every subcommand takes (D1353): the
	// documented form is `fugaro update -v`. It is declared here, before
	// cobra's own initializers run inside Execute, so the root's version flag
	// does not claim `-v` first -- and a leading `fugaro -v` is still the
	// version, because NormalizeVersionArgs rewrites it at the argv boundary.
	root.PersistentFlags().BoolP(verboseFlag, "v", false, "print the run's full output as it goes, not one line per step")
	root.AddCommand(
		newVersionCmd(), newValidateCmd(), newConfigCmd(), newVerifyCmd(), newExecCmd(), newImageCmd(),
		newRunCmd(), newFollowUpsCmd(), newRecipesCmd(), newRunsCmd(), newEstimateCmd(), newStatsCmd(), newSeatsCmd(), newArtifactsCmd(), newLogsCmd(), newDiagnoseCmd(), newCancelCmd(), newTellCmd(), newSecretsCmd(), newCredsCmd(), newAccountsCmd(), newBackendsCmd(), newSetupCmd(), newUpdateCmd(), newRetiredInitCmd(), newBudgetCmd(), newWatchCmd(), newUpdateSkillsCmd(), newDoctorCmd(), newProjectCmd(), newSettingsCmd(), newWorkflowCmd(), newRigorCmd(), newAllowDeleteCmd(), newManagedCmd(), newRestoreCmd(), newPlanCmd(), newReaperCmd(), newBacklogCmd(), newIssueCmd(), newPairCmd(), newMailboxCmd(), newUserCmd(), newModelsCmd(), newCraftCmd(), newWorkspaceCmd(), newRoutingCmd(),
		// The independent review's own command. It is registered like any other
		// core command, and its launch still refuses where one of E7's two
		// halves is missing — an installation with no credentials service, or a
		// binary built before the service admitted the review job's own caller
		// (internal/cli/review.go's checkReviewJobAdmitted, true since 0.21.0)
		// — because a name that answers and then refuses is a sentence a person
		// can read, and a name the tree does not hold is a command nobody can
		// type.
		newReviewCmd(),
		// The reader an agent calls instead of guessing what this repository
		// treats as security-sensitive (the security-classification design's
		// SC3, D1512). It reads a file at the base branch and nothing else: no
		// bucket, no database and no host.
		newSecurityCmd(),
		// The reader and the one writer a person or a session has over the
		// project's orchestrator state (the stateless-orchestrator plan's O5,
		// D1223). It reads and writes the node its project's daemon keeps.
		newOrchestratorCmd(),
		// What the escalation ladder has done to the project's stalled pull
		// requests (the outer-loop-escalation plan's E13): the ledger E7 keeps
		// on each work item, read-only. A person's window onto the ladder that
		// otherwise only the daemon acts on.
		newEscalationCmd(),
		// The credentials service's own entry point: hidden, and the only
		// thing the service's image runs (credentials_serve.go, plan task
		// C1.14). It is registered like every other command so `fugaro
		// credentials serve` is what the image's CMD names, and hidden so it
		// is not something an operator is offered.
		newCredentialsCmd(),
		// The secrets intake's own entry point (DS2.1): hidden for the same
		// reason and registered for the same reason, because a Cloud Run
		// service's container sets no command and the image's CMD is what
		// names `fugaro intake serve` (intake_serve.go).
		newIntakeCmd(),
		// The voice hop's own entry point (P5.3.3): hidden for the same reason
		// and registered for the same reason, because a Cloud Run service's
		// container sets no command and the image's CMD is what names
		// `fugaro voice serve` (voice_serve.go).
		newVoiceCmd(),
		// The local diagnosis of a command that failed on this machine: its
		// log, this machine's configuration and the source at this version,
		// read by a read-only agent (docs/diagnose-agent, D1604). Beside
		// `fugaro diagnose`, which explains a cloud run instead.
		newTroubleshootCmd(),
		// The one command that runs the follow-up steps a management command
		// saved for later (docs/composite-commands, D1560): it sits beside the
		// commands whose work it finishes rather than under one of them,
		// because it is the same command for all of them.
		newApplyCmd(),
	)
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the fugaro version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return writeVersion(cmd.Root(), cmd.OutOrStdout())
		},
	}
}
