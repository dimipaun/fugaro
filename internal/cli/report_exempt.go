package cli

// The census of the tree: which commands print a run through the reporter, and
// which do not (CL9, D1354).
//
// The reporter is the account a mutating command gives of its run — one line
// per step and a closing line on the human writer, the run's own output in a
// log beside it (D1351, D1352). A command that prints an account and is not on
// the reporter is a command that prints one by hand, and this file is where
// that is either excused or admitted: the sets below are the answer to "which
// commands print through the reporter", one reason per command that does not,
// so a command added later cannot print an account of its own without a test
// failing here (report_census_test.go).
//
// Three sets, because two would have to lie:
//
//   - reporterCommands: the commands whose run the reporter owns. Each entry
//     is the log name its run is filed under (CL3), which is the command's own
//     name (D1351).
//   - exemptListings: D1354's exemption. A listing's stdout is a reading — a
//     table, a document, a Dockerfile — not an account of steps, so it is not
//     the reporter's: the reporter's shape would put a frame around a document
//     and a closing line under a table (G2's --json documents are the same
//     answer under the same rule).
//   - exemptAnswers: everything else, and each reason says which of two things
//     it is. Most print one answer — a code, a path, a preview, a help screen,
//     a live screen — and there is no run to give an account of. Some print an
//     account of their own, by hand: `upgrade` prints a block per checkout and
//     a summary under it, `project sync` and `workspace add` print what they
//     wrote. Those are the migration's remaining work rather than a design
//     decision, and they are listed here with the reason saying so rather than
//     called listings, which they are not.
//
// The distinction between the last two matters to the next reader: an entry in
// exemptListings is settled, and an entry in exemptAnswers that names a
// hand-rolled account is a command a later task moves onto the reporter.

// reporterCommands are the commands that print their run through the reporter,
// keyed by their path under the root, each holding the name its run's log is
// filed under (D1351: the command's own name, dashes for spaces).
//
// A name here is the whole claim: the log the command opens, the file it is
// written to and the redaction it is filtered with are CL3's, and
// report_census_test.go holds this table to the source both ways — every
// literal name a reporter constructor is called with is a value here, and
// every value here is a literal in the source or one of builtLogNames' two
// builders.
var reporterCommands = map[string]string{
	"accounts add":   "accounts-add",
	"accounts clear": "accounts-clear",
	"accounts rm":    "accounts-rm",
	"allow-delete":   "allow-delete",
	"backlog add":    "backlog-add",
	"backlog index":  "backlog-index",
	"budget kill":    "budget-kill",
	"budget resume":  "budget-resume",
	"budget set":     "budget-set",
	"cancel":         "cancel",
	"config publish": "config-publish",
	"creds set":      "creds-set",
	"image build":    "image-build",
	"image refresh":  "image-refresh",
	"managed apply":  "managed-apply",
	"managed flag":   "managed-flag",
	"managed unflag": "managed-unflag",
	"reaper":         "reaper",
	"restore":        "restore",
	"run":            "run",
	"secrets rm":     "secrets-rm",
	"secrets set":    "secrets-set",
	"setup":          "setup",
	"update":         "update",
}

// builtLogName is one reporter command whose log name is not a literal at the
// constructor call site, and where the source writes it instead.
type builtLogName struct {
	// builder is the function the constructor is called with, which is where
	// the name comes from. It is the whole answer for the kill switch, whose
	// one builder is two commands.
	builder string
	// door is the convergeDoor whose own name field holds the command's name,
	// for the two doors that share one converge: doorName returns the door the
	// run was built for, and the door's name is written in its own spec, not
	// in the builder.
	door string
}

// builtLogNames are the reporter commands whose log name is built at the call
// site rather than written there: one constructor is two commands (`setup` and
// `update` are one converge behind two doors, `budget kill` and `budget resume`
// one switch), so the name a person typed is not a literal any scan can read.
// report_census_test.go holds each entry to the syntax tree: the builder has to
// be the function a constructor is called with, and the name has to be the one
// the builder returns or the door spec writes down -- a comment that says
// "update" is not a name.
var builtLogNames = map[string]builtLogName{
	"budget kill":   {builder: "budgetKillLogName"},
	"budget resume": {builder: "budgetKillLogName"},
	"setup":         {builder: "doorName", door: "setupDoor"},
	"update":        {builder: "doorName", door: "updateDoor"},
}

// exemptListing is one command D1354 keeps off the reporter because its stdout
// is a reading rather than an account of a run.
type exemptListing struct {
	// why is what the command prints instead of an account of steps.
	why string
	// json is whether the command carries a --json flag, which the test checks
	// against the tree rather than trusting: a listing with a document of its
	// own is a contract (G2), and one without prints its reading whole.
	json bool
}

// exemptListings are the commands whose stdout is a listing: a table, a
// document or a screen a person reads whole, never a run's steps. D1354 keeps
// them off the reporter, and D1352 keeps them out of a log — a reading is not
// a run.
var exemptListings = map[string]exemptListing{
	"accounts ls":          {"a listing: the repository's accounts as a table, or a document under --json", true},
	"accounts pool ls":     {"a listing: the pools and the members each one walks, printed whole", false},
	"artifacts ls":         {"a listing: what a run's record says it delivered, as a table or a document", true},
	"backends ls":          {"a listing: the backends this fugaro knows and the one the project uses", true},
	"backlog list":         {"a listing: the backlog's items as the index lists them, table or document", true},
	"budget preview":       {"a listing: a batch's worst-case spend, as a table or a document", true},
	"budget prices":        {"a listing: the effective model price table, which is the answer itself", true},
	"budget show":          {"a listing: the caps, counters, kill switches and headroom, table or document", true},
	"config example":       {"a listing: the annotated fugaro.yaml template, printed whole", false},
	"config layer":         {"a listing: the project layer published for this checkout, table or document", true},
	"config list":          {"a listing: the installations this machine has, as a table or a document", true},
	"config show":          {"a listing: the checkout's resolved fugaro.yaml, with where each value comes from", true},
	"craft ls":             {"a listing: each craft's effective model and access, and where each came from", true},
	"creds ls":             {"a listing: the credential names and metadata the bundle holds, never a value", true},
	"diagnose":             {"a listing: how one run went, as a report or a document under --json", true},
	"doctor":               {"a listing: one screen of checks, which G9 keeps to a single screen", true},
	"image check":          {"a listing: whether each workflow's image needs a rebuild, table or document", true},
	"image ls":             {"a listing: the project's image catalog entries, as a table or a document", true},
	"image render":         {"a listing: the Dockerfile that builds the workflow's image, printed whole", false},
	"image status":         {"a listing: when each workflow's image was built and from what, table or document", true},
	"image survey":         {"a listing: the repositories and the image signals each one carries, read-only", true},
	"issue list":           {"a listing: the issues saved on this machine, as a table or a document", true},
	"issue mine":           {"a listing: the issues this user reported, as a table or a document", true},
	"issue show":           {"a listing: one issue's text, which is the answer the command exists to print", true},
	"logs":                 {"a listing: a run's log lines from Cloud Logging, as text or a document", true},
	"managed status":       {"a listing: a managed project's marks and its newest audit record", true},
	"models ls":            {"a listing: the project's routes, its models and what each repository runs", true},
	"models search":        {"a listing: the models the built-in price catalog and the Claude table know", true},
	"orchestrator events":  {"a listing: the orchestrator's audit log, as a table or a document", true},
	"orchestrator ls":      {"a listing: the installation's orchestrators, as a table or a document", true},
	"orchestrator state":   {"a listing: the generated summary of the orchestrator's live node", true},
	"orchestrator status":  {"a listing: the login unit's file, and whether it is this release's template, printed whole", false},
	"pair ls":              {"a listing: the devices paired with this installation, as a table or a document", true},
	"plan graph":           {"a listing: a plan's tasks, their dependencies and their footprints, as a graph", true},
	"plan status":          {"a listing: every plan's tasks, joined to the runs working on them", true},
	"project clone-url ls": {"a listing: the clone URL each repository of the project is given, table or document", true},
	"project policy show":  {"a listing: the published policy, which is the answer itself", true},
	"project profile ls":   {"a listing: the project's run profiles, as a table or a document", true},
	"project profile show": {"a listing: one run profile, or every one, as a table or a document", true},
	"project routing show": {"a listing: the project's routing posture, as a table or a document", true},
	"project repos ls":     {"a listing: the allow-list and the repositories that have run here, printed whole", false},
	"project show":         {"a listing: the effective project: caps, policy, allow-list and runs in flight", true},
	"recipes ls":           {"a listing: the recipes of this checkout, the project and the catalog, and the winner", true},
	"recipes show":         {"a listing: the recipe a run would get for a name, and the layer it comes from", true},
	"troubleshoot":         {"a listing: the diagnosis of a failed command on this machine, as text or a document under --json", true},
	"recipes validate":     {"a listing: the verdict for a recipe file, as a table or a document", true},
	"review show":          {"a listing: a review run's verdict artifact and its markdown", true},
	"runs ls":              {"a listing: the runs with their status, stage, cost and PR, table or document", true},
	"runs stats":           {"a listing: run records aggregated by the person who started each run", true},
	"runs summary":         {"a listing: the window's run count and spend, without reading a run object", true},
	"seats":                {"a listing: the estimate a subscription's seat rule would count, table or document", true},
	"secrets ls":           {"a listing: the secrets' names, scope and versions, never a value", true},
	"security paths":       {"a listing: the pattern list in force, and a path's answer against it", true},
	"settings":             {"a listing: every setting, what decides it and its default, table or document", true},
	"settings get":         {"a listing: one setting, what decides it and its default, table or document", true},
	"user ls":              {"a listing: the installation's launchers and operators, as a table or a document", true},
	"workspace login":      {"a listing: the sign-in this project's pin asks for, which is one answer", true},
	"workspace ls":         {"a listing: the workspaces this machine's project configs name, table or document", true},
	"workspace show":       {"a listing: one workspace from this machine's own files, as a table or a document", true},
}

// exemptAnswers are the commands that are neither on the reporter nor a
// listing, each with what it prints instead of an account of a run. Most print
// one answer; the ones whose reason names an account printed by hand are the
// commands a later task moves onto the reporter, and the reason says so rather
// than hiding them behind the word "listing".
var exemptAnswers = map[string]string{
	"ab":                     "a paid name this build carries no code for: the stub refuses and prints one line",
	"accounts allow":         "adds repositories to a named Claude account's allow-list, and prints the diff it wrote",
	"accounts deny":          "removes repositories from a named Claude account's allow-list, and prints what it dropped",
	"accounts":               "prints its own help: the reading is the accounts ls beside it",
	"accounts pool":          "prints its own help: the work is in the ls, add and rm subcommands beside it",
	"accounts pool add":      "writes one pool into this machine's project config, and prints the entry it wrote",
	"accounts pool rm":       "removes one pool from this machine's project config, and prints what it dropped",
	"artifacts get":          "downloads a run's artifacts into a directory and prints where they landed",
	"backends":               "prints its own help: the reading is the backends ls beside it",
	"completion bash":        "cobra's own generated script, which cobra writes rather than this package",
	"completion fish":        "cobra's own generated script, which cobra writes rather than this package",
	"completion powershell":  "cobra's own generated script, which cobra writes rather than this package",
	"completion zsh":         "cobra's own generated script, which cobra writes rather than this package",
	"config init":            "writes the minimal fugaro.yaml into the checkout, and prints the file it wrote",
	"coordinator":            "a paid name this build carries no code for: the stub refuses and prints one line",
	"creds":                  "prints its own help: the work is in the set and ls subcommands beside it",
	"followups":              "files a merged run's deferred findings as debt tasks, printing its own account by hand",
	"graph":                  "a paid name this build carries no code for: the stub refuses and prints one line",
	"help":                   "cobra's own help screen, which prints the tree rather than a run's account",
	"image add":              "writes one catalog entry into this machine's project config, and prints the entry",
	"image map set":          "writes one repository-to-image mapping into this machine's project config",
	"image promote":          "copies a repository's image definition into the catalog, printing its own account by hand",
	"image rm":               "removes one catalog entry from this machine's project config, and prints what it dropped",
	"issue dismiss":          "marks one issue as not worth filing, and prints the mark it wrote",
	"issue submit":           "shows the issue it would file on GitHub, and files it under --ask",
	"issue sync":             "uploads this machine's pending issues, printing its own account of the upload by hand",
	"models add":             "writes the claim, the price and the allow-list entry, and prints what it wrote",
	"models rm":              "drops the claim, the price and the allow-list entry, and prints what it dropped",
	"models use":             "writes one repository's own config in its working tree, and prints the line it wrote",
	"orchestrator install":   "writes the login unit that runs the daemon, printing its own account by hand",
	"orchestrator patch":     "applies one typed patch document to the state, and prints the state it wrote",
	"orchestrator run":       "runs the daemon in the foreground and streams its own log, which no step account fits",
	"orchestrator uninstall": "removes the login unit that runs the daemon, and prints what it dropped",
	"pair":                   "prints the code a phone types to pair, which is one answer rather than a run's account",
	"pair revoke":            "revokes one paired device, and prints the device it dropped",
	"project clone-url rm":   "removes one repository's clone URL from this machine's project config, and prints what it dropped",
	"project clone-url set":  "writes one repository's clone URL into this machine's project config, and prints the row it wrote",
	"project policy publish": "publishes the operator's policy, printing its own account of the write by hand",
	"project profile add":    "writes one run profile into this machine's project config, and prints the entry",
	"project profile rm":     "removes one run profile from this machine's project config, and prints what it dropped",
	"project profile set":    "changes one key of one run profile in this machine's project config, and prints the row",
	"project repos set":      "writes one repository's workflow selection or one job profile, and prints the row it wrote",
	"project routing set":    "writes the project's routing posture into this machine's project config, and prints it",
	"project routing unset":  "removes the project's routing posture from this machine's project config, and prints the result",
	"project repos add":      "adds one repository to the policy's allow-list, and prints the entry it wrote",
	"project repos rm":       "removes one repository from the policy's allow-list, and prints what it dropped",
	"project sync":           "offers the installed repositories the allow-list does not admit, printing its own account by hand",
	"recipes publish":        "publishes one recipe to the project, printing its own account of the write by hand",
	"report":                 "a paid name this build carries no code for: the stub refuses and prints one line",
	"rigor set":              "writes the project layer's rigor default, or one area rule, and prints the row it wrote",
	"review":                 "launches a review in a job of its own, and prints the run it started",
	"runs digest":            "writes a closed period's report into the repository's docs tree, and prints where it landed",
	"secrets allow":          "adds repositories to a project secret's allow-list, and prints the diff it wrote",
	"secrets deny":           "removes repositories from a project secret's allow-list, and prints what it dropped",
	"secrets":                "prints its own help: the work is in the set, ls and rm subcommands beside it",
	"settings set":           "writes one setting into one of the three documents, and prints the row it wrote",
	"settings unset":         "removes one setting from one of the three documents, and prints the row it dropped",
	"stats runs":             "publishes per-recipe percentiles over a window, and prints the numbers it published",
	"update-skills":          "pins the Fugaro plugin in the checkout's .claude/settings.json, and prints the pin",
	"upgrade":                "brings checkouts up, printing its own account of each by hand rather than through the reporter",
	"user add":               "adds one person to the installation's launchers or operators, and prints the entry",
	"user rm":                "removes one person from the installation's launchers or operators, and prints what it dropped",
	"validate":               "checks a fugaro.yaml against the schema and the repository, and prints what it found",
	"verify":                 "runs and records the repository's build, test or lint command, which is the record itself",
	"version":                "prints one line, which is the version and nothing else",
	"watch":                  "draws a live screen of the project's spend and running agents, which no step account fits",
	"workflow add":           "writes a workflows block into the repository's fugaro.yaml, and prints the block",
	"workflow rm":            "removes a workflows block from the repository's fugaro.yaml, and prints what it dropped",
	"workspace add":          "joins the selected project to a workspace, printing its own account of the pin by hand",
	"workspace cap set":      "records the workspace's own cap in the local config, and prints the row it wrote",
	"workspace member add":   "declares one member and prints the entry it wrote into the workspace's table",
	"workspace member rm":    "stops declaring one member and prints the entry it dropped from the table",
	"workspace rm":           "leaves a workspace, and prints the membership key and pin it dropped",
}
