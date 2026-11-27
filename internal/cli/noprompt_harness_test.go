package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/mailbox/keychain"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// noprompt_harness_test.go is the closed-stdin harness of the no-prompt policy
// (docs/no-prompt-policy/implementation-plan.md, T2b). Its constraint is the
// plan's first one — "no command waits on closed stdin" — and the shape of the
// check is the plan's: walk NewRootCmd, run every leaf with stdin a closed pipe
// and a short deadline, against a fake, and name every leaf that still asks.
//
// What a closed pipe proves, and what it does not. A pipe whose write end is
// closed is not a terminal and delivers EOF to the first read, so a prompt site
// that refuses when it cannot see a terminal returns at once — and still prints
// the question it will not read an answer to, which is what the second half of
// this harness fails on. A site that reads, gets EOF and asks again instead of
// giving up never returns at all, which is what the deadline fails on. Between
// the two, no leaf may wait and no leaf may ask: the question is the red signal
// today, and the deadline is the one that a prompt answering EOF by asking
// again trips.
//
// Every leaf must reach its own body, and every leaf must be classified. A leaf
// that stops at its own argument or flag check — cobra's usage error for a
// command whose Args validator is declared on it, the command's own refusal for
// one that reads `len(args)` or a flag inside its body — has proved nothing
// about what the command does, and a bare run that ends there is the one way
// this harness could have called a leaf quiet without ever asking it anything.
// Two checks answer it. The run's wrapper marks the leaves whose RunE ran, and
// a leaf that did not get there fails; and every leaf of the tree has to be in
// closedStdinFixtures or in closedStdinNoFixture, so the leaves the body check
// cannot tell apart — a command that entered its body and stopped at the
// argument check written there — are a decision someone wrote down rather than
// a count that went up.
//
// The fixture is the arguments and, where the leaf is here to reach a question,
// the sentence it must still print: without that sentence a fixture whose
// arguments had merely stopped the leaf short would pass for a quiet one, which
// is a green answer for the wrong reason.
//
// The world every leaf runs in is the hermetic installation this package
// already builds — newBudgetFixture's temporary XDG, the local config naming
// project aurora, the RTDB fake and a file:// runs bucket — rebuilt for each
// leaf, so that a run which writes cannot decide what the next one finds. Three
// things are added to it. The outbound network is refused at once, by the same
// guard the ee CLI mains use (HTTPS_PROXY to a closed loopback port, which Go
// bypasses for loopback, so the fakes still answer); the ambient run's own state
// directory is cleared, or `fugaro verify` would run this repository's real
// build command instead of refusing; and no stream is a terminal and no agent
// marker is set, so what a leaf does about the closed stdin is the only thing
// the harness reads.
//
// The list this file exists to hold — closedStdinPrompts — is the allow-list the
// plan gives the lint of T1: a leaf that prints a question today is named there
// with the task of the plan that removes it, a leaf that prints one and is not
// named fails, and a named leaf that no longer prints one fails too, so the
// entry is deleted by the commit that removes the prompt rather than left to
// rot. It is the harness's one debt list, and reaching empty is the goal.
//
// closedStdinUnreached is the other half of the same ledger, and it is not a
// list of leaves that passed: it names the leaves whose question this world
// cannot drive them to — the write that asks only when it drops an approval, the
// verb whose confirmation sits behind a GCP read the fake does not serve — with
// the task that removes the ask and the source line that ask lives on. The line
// is read, so the entry cannot outlive the ask it is about: the task that
// deletes the confirmation in that file fails here until it deletes the entry
// too. Nothing in the list is claimed to be quiet, which is why the harness
// says so out loud rather than leaving a reader to assume a bare run settled it.

// closedStdinDeadline is how long a leaf may take with its stdin closed before
// the harness calls it a wait. It is accounts_add_login_test's and askWithin's
// own bound for the same question, against the package's ten-minute timeout,
// and it is generous on purpose: every leaf returns in milliseconds, and the
// one that does not is the failure this file is about.
const closedStdinDeadline = 10 * time.Second

// closedStdinQuestion matches what a run prints when it asks a person
// something. There are two readings in it, and between them they cover every
// prompt the tree writes.
//
// The ⚠ CONFIRM banner is the first: every typed step prints it before it reads
// the project's name (confirmTyped, confirmTypedStrict,
// confirmRemoval, the initflow steps, managed apply). It is matched anywhere,
// because it is a line of its own and the question comes after it.
//
// The second reading is the prompt line itself, and what every prompt in this
// tree has in common is that its line ends in its own colon and a space. That
// is the whole of it — the typed confirmations ("Type %s to confirm: "), the
// setup wizard's answers (promptChoice's "%s [%s]: " and its " (or %s: %s)"
// form, and the plain "%s: " when there is no default: the GCP project, the
// billing account, the GitHub App ID, the region, the onboarding question),
// initsecrets' "Paste the private key … then press Enter: ", pair's lower-case
// "type that code: ", the y/N questions ("Write this to %s? [y/N]: "), and
// readValue's value prompt ("Type the billing account ID: ") — and matching
// the shape rather than the wording is what keeps a prompt this file has never
// heard of from passing green. `(?m)` is what makes the `$` the end of a line
// rather than the end of the run: a run goes on after it asks — with the
// refusal an ended stdin earns, with the error cobra prints — and without the
// flag the only prompt caught was the last thing a run printed.
//
// The y/N question is read a second time, unanchored, because the
// print-then-refuse ordering it guards against puts the refusal on the
// prompt's own line. The three prints of the bracketed form are its only
// writers, so it is a question wherever it appears.
//
// Reading a line that ends in ": " as a question is deliberately generous: no
// leaf of the clean tree prints one (TestEveryCommandRunsWithClosedStdin is
// what says so), and a false positive is a failure that names the run and
// prints its output, which is a sentence away from being read.
var closedStdinQuestion = regexp.MustCompile(`(?m)⚠ CONFIRM|: $|\[y/N\]:`)

// cliConfirmQuestion matches a confirmation's own question and nothing else:
// the ⚠ CONFIRM banner every typed step prints, the bracketed y/N under it, and
// the `Type <name> to confirm: ` line beside the banner. It is the reading the
// per-command ...AsksNothing tests make, and it is closedStdinQuestion's
// question half on purpose: a command this task covers may still print a VALUE
// prompt — the token entry `accounts add` reads off the owner's own terminal
// (readValue, D1540) — and the harness's generous `: $` reading would call that
// entry a question. What the pattern leaves out is exactly what a terminal may
// still be asked for. A run that passes the assertion below passes the
// harness's own question check on the same output.
var cliConfirmQuestion = regexp.MustCompile(`⚠ CONFIRM|\[y/N\]:|to confirm: `)

// assertNoPrompt fails a ...AsksNothing test whose run asked for a
// confirmation. What replaced every one of those questions is the sentence
// naming the change (D1540), and the one question a command may still raise is
// --ask's, asked by askConfirm where somebody is there to answer it (D1544):
// a confirmation question in a run this test drove is a prompt nobody is
// waiting for.
func assertNoPrompt(t *testing.T, out string) {
	t.Helper()
	if q := cliConfirmQuestion.FindString(out); q != "" {
		t.Errorf("the run asked %q, and no command asks for a confirmation any more (D1540):\n%s", q, out)
	}
}

// closedStdinFixture is one leaf's run: the arguments that put it on the path
// where it would ask somebody something, and the sentence the run must still
// print, which is what says the fixture really reached that path. Without the
// second the harness would call a leaf quiet that its own arguments had merely
// stopped before, which is a green answer for the wrong reason.
type closedStdinFixture struct {
	args    []string
	reaches string
	// seed is the state the leaf needs beyond the shared world — a kill switch
	// to resume, a bucket to name. Nil is the ordinary installation.
	seed func(t *testing.T, f *budgetFixture)
}

// closedStdinFixtures is the per-leaf fixture. A leaf named here runs with these
// arguments; a leaf that is not named runs bare and is named in
// closedStdinNoFixture, which is the other half of the same classification:
// every leaf of the tree is in one of the two, and the run fails on the leaf
// that is in neither.
//
// The block below is the leaves whose own arguments are what stands between a
// bare run and the command's work — cobra's usage error, not the command's
// answer. What each run is here to prove is the same thing in every case: the
// leaf's body, with a closed stdin, asks nothing and returns. `reaches` is
// pinned only where a question is what the fixture is for (closedStdinPrompts),
// because there the sentence is the proof the run got to the ask; elsewhere the
// proof is the body having run at all, which the harness checks for every leaf.
var closedStdinFixtures = map[string]closedStdinFixture{
	// The backend name is the argument the command takes, and cloud-run is the
	// one that reaches the body without a gate: the selection this world's
	// config already makes (no backend: key reads as cloud-run) is answered
	// with the "already runs on" line rather than a write, which is the whole
	// of what the leaf does with a closed stdin.
	"fugaro backends use": {args: []string{"cloud-run"}, reaches: "already runs on cloud-run"},
	// The caps are unset in the fixture's database, so the run sets one from
	// nothing — the loosening T3's owner-session fence is about. It asks nothing
	// now, so the sentence is the write itself rather than a banner.
	"fugaro budget set": {args: []string{"--global", "--per-run", "50"}, reaches: "global caps per-run: not set -> $50"},
	// The project-wide kill switch, which a repository's own is not: the
	// fixture drives --all, and T3 took the ask away, so what the run says is
	// the kill and its undo.
	"fugaro budget kill": {args: []string{"--all"}, reaches: "Running runs halt within seconds"},
	// A resume moves a switch, so the fixture's database is killed first: the
	// move is the subject of this run.
	"fugaro budget resume": {
		args:    []string{"--all"},
		reaches: "resumed (by someone@example.com)",
		seed: func(t *testing.T, f *budgetFixture) {
			f.db.Set(budget.PathKillGlobal, map[string]any{"on": true, "by": "boss@example.com", "at": int64(1)})
		},
	},
	// The raise is the fourth writing leaf of the budget family: a run
	// reference, one figure and the reason are the whole of what stands
	// between a bare run and its body. This world's bucket holds no such run,
	// so what the body answers here is its own refusal rather than the write —
	// which is still the body, reached with the stdin closed and asking
	// nothing, and the whole of what a bare run cannot get to.
	"fugaro budget chain raise": {args: []string{"run-1", "--max-usd", "6", "--reason", "one more round"}},
	// The write used to ask before it rewrote the project's claude_accounts
	// block; it asks nothing now (D1540) and the diff of that block is the proof
	// this fixture reaches it.
	"fugaro accounts add": {args: []string{"team-a", "--repos", "acme/app"}, reaches: "claude_accounts"},
	// The two allow-list pairs are the field that block holds, and the sentence
	// each one prints is the only proof that the run reached its write rather
	// than a refusal: what these commands edit is which repository may spend a
	// credential, so a fixture that stopped short of the write for this reason
	// would be silent exactly where it matters. allow declares an account the
	// world does not have — the first form of that pair — so it needs no seed;
	// deny removes a repository the seed gives the account; and both secrets
	// fixtures need the entry, the allow one with a repository it does not name
	// so the addition is a change and not the "no change" answer.
	"fugaro accounts allow": {
		args:    []string{"team-a", "acme/web"},
		reaches: "add acme/web to claude_accounts.team-a.allow_data_to",
	},
	"fugaro accounts deny": {
		args:    []string{"team-a", "acme/web"},
		reaches: "remove acme/web from claude_accounts.team-a.allow_data_to",
		seed:    closedStdinAccountAllowListSeed,
	},
	"fugaro secrets allow": {
		args:    []string{"bitbucket-token", "acme/web"},
		reaches: "add acme/web to project_secrets.bitbucket-token.allow_data_to",
		seed:    closedStdinProjectSecretAllowListSeed("acme/other"),
	},
	"fugaro secrets deny": {
		args:    []string{"bitbucket-token", "acme/web"},
		reaches: "remove acme/web from project_secrets.bitbucket-token.allow_data_to",
		seed:    closedStdinProjectSecretAllowListSeed("acme/web"),
	},
	// The pool writes are the same kind of edit, and they reach it the same
	// way: the seed gives the world a credential to list and a pool to remove,
	// so each runs to its own sentence rather than stopping at the block's own
	// "no credential of this name is defined here".
	"fugaro accounts pool add": {
		args:    []string{"claude-pool", "team-a", "team-b"},
		reaches: "declare pools.claude-pool.members = [team-a, team-b]",
		seed:    closedStdinPoolSeed,
	},
	"fugaro accounts pool rm": {
		args:    []string{"claude-pool"},
		reaches: "remove pools.claude-pool",
		seed:    closedStdinPoolSeed,
	},
	// The two alias writes are one edit of the block and one converge, and the
	// sentence each fixture reads is the diff line of the entry that moved: the
	// set's is the new target, the rm's the entry on its way out. Both need the
	// seed for the reason the pool writes do — an alias stood for by no account
	// is the resolve's refusal, and one no document defines is the rm's own —
	// and neither writes a key, which is what keeps the rm off its
	// "a key still names it" refusal.
	"fugaro accounts alias set": {
		args:    []string{"reviewer", "team-b"},
		reaches: "reviewer: team-b",
		seed:    closedStdinAccountAliasSeed,
	},
	"fugaro accounts alias rm": {
		args:    []string{"reviewer"},
		reaches: "reviewer: team-a",
		seed:    closedStdinAccountAliasSeed,
	},
	// The switch rewrites every key naming an account, and the sentence its
	// fixture reads is the first line of its own list — the key as the file
	// spells it and the move it takes, printed before the write. The seed
	// declares the two accounts and points the default at the first, so the run
	// rewrites a key rather than answering "no change: no key of this config
	// names team-a"; and no repository's own list and no profile names either
	// account, which is what keeps the run off the subset rule the write is
	// judged by.
	"fugaro accounts switch": {
		args:    []string{"team-a", "team-b"},
		reaches: "default_claude_account: team-a -> team-b",
		seed:    closedStdinAccountSwitchSeed,
	},
	// The settings write used to ask for the project's name over the document it
	// edits, and what it edits here is the world's own project config. It asks
	// nothing now (D1540), and the sentence naming the change is the proof this
	// fixture reaches the write. The key is a real one: an unknown key stops at
	// the registry's nearest-name refusal, and a fixture that stopped there would
	// call the leaf quiet without the write having begun. The unset needs a value
	// to remove, which its seed sets.
	// The craft and repository writes of docs/config-ownership A2 are the same
	// kind of edit as the pool and alias writes above, and each fixture is what
	// stands between its run and a refusal rather than a write: the removal needs
	// a craft to take away, and the repository write needs the entry the list
	// lives in, provider and all. Each sentence is the diff line or the naming of
	// the key that was written, which is the proof the run reached the write with
	// the stdin closed and asked nothing. `accounts craft set` and `accounts grade
	// set` are the other way round — MG4 retired the key each one wrote, so the
	// body they reach answers with the refusal and names the command that owns the
	// write (docs/model-credentials-routing) with nothing read on the way, which is
	// why neither needs a seed: the refusal is the whole of what their body does,
	// and it is printed on the error rather than on this stream, so the two
	// fixtures pin the arguments that reach the body and no sentence.
	"fugaro accounts craft set": {
		args: []string{"coder", "--access", "team-b"},
	},
	"fugaro accounts craft rm": {
		args:    []string{"coder"},
		reaches: "remove crafts.coder",
		seed:    closedStdinCraftSeed,
	},
	"fugaro accounts grade set": {
		args: []string{"strong", "--access", "vertex", "--layer", "project"},
	},
	"fugaro accounts repo add": {
		args:    []string{"acme/app", "team-a"},
		reaches: "repos.acme/app.claude_accounts gains it",
		seed:    closedStdinRepoAddSeed,
	},
	// The cascade's own write (K1): one member of the credentials cascade into
	// this installation's own config, which is the carrier the project's
	// published node is filled from. The member is a wait, which is neither of
	// the two directions D142 widens, so what stands between the bare run and
	// the write is the flag itself and not the agent session's refusal (D2617);
	// the sentence is the member as the file spells it.
	"fugaro accounts cascade set": {
		args:    []string{"--backoff-cap", "1h"},
		reaches: "write the credentials cascade: backoff.cap = 1h",
	},
	"fugaro settings set":   {args: []string{"base_branch", "main"}, reaches: "write base_branch in"},
	"fugaro settings unset": {args: []string{"base_branch"}, reaches: "remove base_branch from", seed: closedStdinSetBaseBranch},
	// The two workflow writes are repository-scoped, so their fixture's world
	// is a checkout of a repository of the world's own project, whose file
	// already defines the workflows they work on — and what they write there is
	// a block of commands, so the flag the executable-key refusal asks for is
	// part of the fixture. The sentence naming the block is the proof the run
	// reached the write rather than the "not a checkout" refusal.
	"fugaro workflow add": {
		args:    []string{"docs", "--set", "commands.build=make docs", "--set", "commands.test=make test", "--executable-changes"},
		reaches: "create workflows.docs in",
		seed:    closedStdinWorkflowSeed(""),
	},
	"fugaro workflow rm": {
		args:    []string{"docs", "--executable-changes"},
		reaches: "remove workflows.docs from",
		seed:    closedStdinWorkflowSeed(workflowDocs),
	},
	// The two clone_urls writes are the settings group's kind: each reaches a
	// block of the world's own project config, and the sentence naming the
	// entry it changes is the proof the run reached the write rather than the
	// refusal a project with no config file of its own meets. rm takes an entry
	// away, so its seed writes one first.
	"fugaro project clone-url set": {
		args:    []string{"github/acme/app", "https://github.com/acme/app"},
		reaches: "write clone_urls[github/acme/app] = https://github.com/acme/app in",
	},
	"fugaro project clone-url rm": {
		args:    []string{"github/acme/app"},
		reaches: "remove clone_urls[github/acme/app] from",
		seed:    closedStdinCloneURLSeed,
	},
	// The three binaries writes are the same kind over another block of the same
	// file (K2 of docs/config-ownership). The allow states the block itself, so
	// its fixture needs no seed; the deny and the lowering max-bytes take a
	// member away from a block the seed writes first, whose entry the deny names
	// and whose cap the fixture lowers — a removal from a file that states no
	// block is this command's own refusal, and a sentence naming the change is
	// the proof each run reached its write rather than one of them.
	"fugaro project binaries allow": {
		args:    []string{"brand/*.png"},
		reaches: "add brand/*.png to binaries.allow in",
	},
	"fugaro project binaries deny": {
		args:    []string{"brand/*.png"},
		reaches: "remove brand/*.png from binaries.allow in",
		seed:    closedStdinBinariesSeed,
	},
	"fugaro project binaries max-bytes": {
		args:    []string{"1048576"},
		reaches: "set binaries.max_bytes to 1048576 in",
		seed:    closedStdinBinariesSeed,
	},
	// watch is the one leaf whose contract is not to return: it follows the
	// project until it is interrupted. --once is its own one snapshot, which is
	// the run this harness can hold to a deadline.
	"fugaro watch": {args: []string{"--once"}, reaches: "counted"},
	// The mailbox consumer is the same shape — it serves the pairing until it is
	// interrupted — and --once is its own one pass. The seed is what makes that
	// pass a pass rather than the startup refusal a project with no mailbox
	// block gets: the world's config names one pairing and a fake keychain holds
	// its key, so the run takes the lease and reports the empty inbox it read.
	"fugaro mailbox": {
		args:    []string{"--once"},
		reaches: "read 0, delivered 0, duplicates 0, refused 0, replied 0",
		seed:    closedStdinMailboxSeed,
	},
	// The sweeper is the consumer's other half over the same world, and the one
	// leaf of the pair that writes nothing at all: `--dry-run` is the plan
	// rather than the work, so what it reaches is the line naming the mailbox it
	// walked and the prompts D118's TTL left behind. It reads no keychain, so
	// the seed is here for the mailbox block the world would otherwise have
	// none of — a project with no mailbox refuses before it walks anything.
	"fugaro mailbox sweep": {
		args:    []string{"--dry-run"},
		reaches: "0 stored prompt(s) past the TTL",
		seed:    closedStdinMailboxSeed,
	},
	// The daemon is the same shape and answers it the same way: it drives the
	// project until it is stopped, and --once is its own one cycle — the lease
	// taken for it and released with it. In this world the node holds no work
	// and no run, so the cycle it reaches is the empty one it prints.
	"fugaro orchestrator run": {args: []string{"--once"}, reaches: "one cycle as asked (--once)"},

	// The arguments the rest of the tree's leaves require. Each is the shallowest
	// set that reaches the command's body; what the body answers in this world is
	// the leaf's own business, and what the harness reads is that it answers with
	// its stdin closed and asks nothing.
	"fugaro accounts clear":       {args: []string{"team-a"}},
	"fugaro accounts rm":          {args: []string{"team-a"}},
	"fugaro allow-delete":         {args: []string{"someone@example.com"}},
	"fugaro artifacts get":        {args: []string{"run-1"}},
	"fugaro artifacts ls":         {args: []string{"run-1"}},
	"fugaro budget preview":       {args: []string{"--recipe", "r", "--runs", "1"}},
	"fugaro budget history":       {args: []string{"--sweep"}},
	"fugaro budget reconcile":     {args: []string{"--from", "2026-10-12", "--to", "2026-11-16", "--all"}},
	"fugaro cancel":               {args: []string{"run-1"}},
	"fugaro tell":                 {args: []string{"run-1", "check the flaky test"}},
	"fugaro creds set":            {args: []string{"NAME"}},
	"fugaro diagnose":             {args: []string{"run-1"}},
	"fugaro estimate":             {args: []string{"--run", "run-1", "--model", "claude-opus-5-5"}},
	"fugaro exec":                 {args: []string{"--bucket", "file:///nonexistent"}},
	"fugaro followups":            {args: []string{"run-1"}},
	"fugaro image add":            {args: []string{"base"}},
	"fugaro image gate":           {args: []string{"--record", "r", "--slug", "s", "--bucket", "file:///nonexistent"}},
	"fugaro image git-credential": {args: []string{"--out", "git-cred"}},
	"fugaro image map set":        {args: []string{"acme/app", "base"}, reaches: "runs the catalog entry"},
	"fugaro image promote":        {args: []string{"base"}},
	"fugaro image record":         {args: []string{"--in", "r", "--digest-from", "a", "--base-digest-from", "b", "--image", "i", "--base", "ba", "--build-id", "x", "--slug", "s", "--bucket", "file:///nonexistent"}},
	"fugaro image rm":             {args: []string{"base"}},
	// show and dismiss are the two leaves whose argument is an issue's name, and
	// this world saved none: the reference is a well-formed local ID the store
	// does not hold, so the run reaches the command's own body and its "no issue"
	// answer, which is the whole of what a closed stdin can be shown here. The
	// same reference in both is the point — neither leaf may ask a question of a
	// missing issue.
	"fugaro issue dismiss":  {args: []string{"local-update-20261107183012-1"}},
	"fugaro issue show":     {args: []string{"local-update-20261107183012-1"}},
	"fugaro logs":           {args: []string{"run-1"}},
	"fugaro managed apply":  {args: []string{"root"}},
	"fugaro managed flag":   {args: []string{"proj-1234"}},
	"fugaro managed unflag": {args: []string{"proj-1234"}},
	"fugaro models add":     {args: []string{"claude-x"}},
	"fugaro models rm":      {args: []string{"claude-x"}},
	"fugaro models search":  {args: []string{"x"}},
	"fugaro models use":     {args: []string{"a", "b", "c"}},
	// The two writers D2613 adds reach the write itself, in the world's own
	// project config: the sentence naming what the entry carries now is the
	// proof the run got past the selection and the splice, which is the part of
	// a write a closed stdin could ever have stopped. Both work on an entry the
	// file already carries — a provider is not one without the model IDs it
	// serves, and no flag here writes that list — so both are seeded, and the
	// set changes a leaf the seed leaves out.
	"fugaro models provider set": {
		args:    []string{"acme", "--price-policy", "highest"},
		reaches: "set providers.acme in",
		seed:    closedStdinSetProvider,
	},
	"fugaro models provider rm": {args: []string{"acme"}, reaches: "remove the provider acme from", seed: closedStdinSetProvider},
	"fugaro models price set": {
		args:    []string{"claude-sonnet-5-5", "--input-per-m", "3", "--output-per-m", "15"},
		reaches: "set model_prices.claude-sonnet-5-5 in",
	},
	"fugaro pair revoke": {args: []string{"--uid", "uid-1"}},
	"fugaro project profile add": {args: []string{"standard", "--provider", "github", "--cpu", "2",
		"--memory", "4Gi", "--timeout-class", "6h"}},
	"fugaro project profile rm": {args: []string{"standard"}},
	// The world's config defines no profiles, so this fixture stops at the
	// command's own refusal — which is still its body, and the whole of what a
	// bare run cannot reach.
	"fugaro project profile set":  {args: []string{"standard", "cpu", "4"}},
	"fugaro project repos add":    {args: []string{"acme/app"}},
	"fugaro project repos rm":     {args: []string{"acme/app"}},
	"fugaro project repos status": {args: []string{"github/acme/app"}},
	"fugaro project routing set":  {args: []string{"claude", "--via", "direct"}},
	// The table's two writes, over a world whose config carries no
	// model_routing block: `set` appends the entry, and `rm` takes away the one
	// its seed declares. Both are the local config's write path — the fence is
	// applySettingsEdit's, and this world has no agent marker — so what the
	// closed stdin has to show is that neither waits for a person to answer,
	// and the sentence each is read by is the write's own.
	"fugaro routing set": {args: []string{"claude-*", "anthropic"}, reaches: "set the routing entry `claude-*`"},
	"fugaro routing rm": {
		args:    []string{"zzz-*"},
		reaches: "remove the routing entry `zzz-*`",
		seed:    closedStdinRoutingSeed,
	},
	"fugaro recipes publish":  {args: []string{"slug"}},
	"fugaro recipes rm":       {args: []string{"slug"}},
	"fugaro recipes show":     {args: []string{"slug"}},
	"fugaro recipes validate": {args: []string{"slug"}},
	"fugaro restore":          {args: []string{"proj-2", "root"}},
	"fugaro review show":      {args: []string{"run-1"}},
	"fugaro run":              {args: []string{"do something"}},
	"fugaro escalation show":  {args: []string{"wi-1"}},
	"fugaro runs show":        {args: []string{"run-1"}},
	"fugaro secrets rm":       {args: []string{"NAME"}},
	"fugaro secrets set":      {args: []string{"NAME"}},
	// The intake's two families take the same NAME as the pair above, and their
	// bodies are reached the same way: the name is the whole of what stands
	// between a bare run and the command's own body, which is where the
	// refusal this world's project selection produces comes from.
	"fugaro secrets shared rm":  {args: []string{"NAME"}},
	"fugaro secrets shared set": {args: []string{"NAME"}},
	"fugaro secrets user rm":    {args: []string{"NAME"}},
	// The pool family's four service calls reach their bodies the same way: the
	// pool is the whole of what stands between a bare run and the project
	// selection's refusal, which comes before any token is read.
	"fugaro accounts pool contribute": {args: []string{"--to", "team"}},
	"fugaro accounts pool designate":  {args: []string{"team"}},
	"fugaro accounts pool rm-member":  {args: []string{"team", "--member", "alice"}},
	"fugaro accounts pool withdraw":   {args: []string{"team", "--member", "alice"}},
	"fugaro secrets user set":         {args: []string{"NAME"}},
	// The rigor write reaches its own edit through the layer file the seed writes:
	// the mode it sets ranks above what the file resolves to, so the fixture is the
	// ordinary write and not the sensitive one, and the sentence naming the key is
	// the proof the run got past the vocabulary and the file.
	"fugaro rigor set": {
		args:    []string{"critical"},
		reaches: "set defaults.rigor.default to critical in",
		seed:    closedStdinRigorLayerSeed,
	},
	// The floor's two leaves, driven to the directions that take a level away — a
	// removal and a lowering, which are the plan's SEC step and the direction a
	// confirmation used to stand in front of — and the seed is what puts a floor
	// in the world for them to take one out of. Neither may print a question
	// here: no command waits on stdin any more (D1540, D1552, D1555), and the
	// sentence naming the change is the proof this fixture reaches the write.
	"fugaro rigor floor set":   {args: []string{"pro", "l2"}, reaches: "set review_floor.pro from l2, l3 to l2 in", seed: closedStdinRigorFloorSeed},
	"fugaro rigor floor unset": {args: []string{"pro"}, reaches: "remove review_floor.pro (l2, l3) in", seed: closedStdinRigorFloorSeed},
	// The security writer's two leaves, over the same layer file `rigor set`'s
	// seed writes with one row of the list: the add writes a second row and the
	// removal takes one away — the lowering direction, whose fence is the marker
	// and not a question (M4 of docs/config-ownership). This world sets no
	// marker, so both reach their write and ask nothing, which is what the
	// sentence naming the row and the key is the proof of.
	"fugaro rigor security add": {
		args:    []string{"--name", "payments", "--match", "internal/payments/**", "--category", "guards"},
		reaches: "add the security path payments to security.patterns in",
		seed:    closedStdinSecurityLayerSeed,
	},
	"fugaro rigor security rm": {
		args:    []string{"--name", "billing"},
		reaches: "remove the security path billing from security.patterns in",
		seed:    closedStdinSecurityLayerSeed,
	},
	"fugaro settings get": {args: []string{"a.b"}},
	"fugaro stats runs":   {args: []string{"--project", "proj-1234"}},
	"fugaro user add":     {args: []string{"someone@example.com"}},
	"fugaro user grant":   {args: []string{"someone@example.com", "--role", "operator", "--scope", "project"}},
	"fugaro user revoke":  {args: []string{"someone@example.com", "--role", "operator", "--scope", "project"}},
	// Offboarding takes the member's address as its argument, and the body it
	// then reaches refuses here: this world selects no project, so there is no
	// roster to read and no intake to call. The write and the sweep are driven
	// by offboard_test.go's own installation rather than by this world, which
	// holds no roster.
	"fugaro user offboard":     {args: []string{"someone@example.com"}},
	"fugaro user rm":           {args: []string{"someone@example.com"}},
	"fugaro verify":            {args: []string{"build"}},
	"fugaro workspace add":     {args: []string{"ws"}},
	"fugaro workspace cap set": {args: []string{"--workspace", "ws", "--monthly", "50"}},
	"fugaro workspace login":   {args: []string{"ws"}},
	"fugaro workspace rm":      {args: []string{"ws"}},
	"fugaro workspace share":   {args: []string{"anthropic-api-key", "--workspace", "ws", "--projects", "acme"}},
	"fugaro workspace show":    {args: []string{"ws"}},
	"fugaro workspace unshare": {args: []string{"anthropic-api-key", "--workspace", "ws"}},
	// The workspace family's own writes (I1, D2802): each takes the workspace's
	// name or a member project's on the command line, so a bare run would stop
	// at the argument check and prove nothing. This world declares no workspace
	// of its own and holds no roster, so every run below reaches the body and
	// the refusal it makes there, which is the whole of what a closed stdin has
	// to be walked through -- none of them asks a person anything.
	"fugaro workspace init":              {args: []string{"ws"}},
	"fugaro workspace domains add":       {args: []string{"example.com", "--workspace", "ws"}},
	"fugaro workspace domains rm":        {args: []string{"example.com", "--workspace", "ws"}},
	"fugaro workspace project add":       {args: []string{"acme", "--workspace", "ws"}},
	"fugaro workspace project reconcile": {args: []string{"acme"}},
	"fugaro workspace project rm":        {args: []string{"acme"}},
	"fugaro workspace user add":          {args: []string{"someone@example.com", "--workspace", "ws"}},
	"fugaro workspace user grant":        {args: []string{"someone@example.com", "acme", "viewer"}},
	"fugaro workspace user revoke":       {args: []string{"someone@example.com", "acme", "viewer"}},
	"fugaro workspace user rm":           {args: []string{"someone@example.com", "--workspace", "ws"}},
}

// closedStdinNoFixture is the other half of the classification, and the reason
// every leaf of the tree has to be in one of the two: a leaf with no fixture
// runs bare, and a bare run of a command that takes arguments stops at its own
// argument check — cobra's usage error for the ones whose Args validator is
// declared on the command, the command's own refusal for the ones that read
// `len(args)` or a flag in its body — which says nothing about what the command
// does with them. Left unlisted, that is a green run for a leaf nobody drove.
//
// So a leaf is fixtured or it is named here with what its bare run is: the
// leaves whose whole work is the argument-free path, and the few whose own
// checks stop the bare run before that work and which this world has no
// arguments for. Nothing here is claimed to be quiet either — the question
// check and the deadline hold every leaf in this list exactly as they hold a
// fixtured one, which is what makes the list safe to keep.
//
// The commands that carry subcommands and a body of their own are here too,
// and for the same reason there is no fixture for them: a parent's own body is
// what runs when no subcommand matches, `fugaro settings` bare is the whole of
// that command's read, and the four that print their usage have no argument
// that would do more. `fugaro review` and `fugaro pair` refuse from inside
// their bodies, in sentences of their own, which the run's own error carries.
var closedStdinNoFixture = map[string]string{
	"fugaro ab":                "refuses: the edition is not in this build",
	"fugaro apply":             "owes nothing in this world, which holds no record: the whole of what a bare run does is the answer of a run with nothing to apply, and it reaches its own body to print it",
	"fugaro accounts":          "is a name for the twelve subcommands beside it: a bare run prints its own usage and returns",
	"fugaro accounts alias":    "is a name for set and rm: a bare run prints its own usage and returns",
	"fugaro accounts cascade":  "is a name for set: a bare run prints its own usage and returns",
	"fugaro accounts craft":    "is a name for set and rm: a bare run prints its own usage and returns",
	"fugaro accounts grade":    "is a name for set: a bare run prints its own usage and returns",
	"fugaro accounts repo":     "is a name for add: a bare run prints its own usage and returns",
	"fugaro accounts ls":       "selects no project and names the two ways to (--repo, an origin remote)",
	"fugaro accounts pool":     "is a name for ls, add, rm, designate, contribute, withdraw and rm-member: a bare run prints its own usage and returns",
	"fugaro accounts pool ls":  "reads the selected project's own config, which this world's has no pools in, and prints that none are defined",
	"fugaro backends":          "is a name for ls and use: a bare run prints its own usage and returns",
	"fugaro backends ls":       "lists every backend a project may name, which needs no project",
	"fugaro backlog add":       "refuses from inside its body: a bare run names --title, which is the one line it files an item from",
	"fugaro backlog index":     "reads docs/backlog, which this world has no checkout to hold",
	"fugaro backlog list":      "prints the no-project-config refusal",
	"fugaro budget prices":     "prints the built-in price list with no project selected",
	"fugaro budget show":       "prints the no-project-config refusal",
	"fugaro config example":    "prints the example fugaro.yaml",
	"fugaro config init":       "refuses outside a git checkout, which is what the world is",
	"fugaro config layer":      "refuses with no checkout and no selected project config",
	"fugaro config list":       "prints that this machine holds no installation",
	"fugaro config publish":    "refuses with no fugaro.yaml to publish",
	"fugaro config show":       "refuses outside a git checkout, which is what the world is",
	"fugaro coordinator":       "refuses: the edition is not in this build",
	"fugaro craft ls":          "prints the five crafts with no project selected, on the default credential",
	"fugaro credentials serve": "refuses without FUGARO_PROJECT in its own environment",
	"fugaro creds":             "is a name for set and ls: a bare run prints its own usage and returns",
	"fugaro creds ls":          "prints the no-project-config refusal",
	"fugaro doctor":            "runs its whole check and reports what this world lacks",
	"fugaro graph":             "refuses: the edition is not in this build",
	"fugaro image build":       "refuses before the build: the budget database a build's lease is taken from is not in this world",
	"fugaro image check":       "prints the no-project-config refusal",
	"fugaro image ls":          "prints the no-project-config refusal",
	"fugaro image refresh":     "refuses before its steps: the two cloud steps that ask need a real terminal",
	"fugaro image render":      "refuses outside a git checkout, which is what the world is",
	"fugaro image selftest":    "reads its spec from the closed stdin and returns on the EOF",
	"fugaro image status":      "prints the no-project-config refusal",
	"fugaro image survey":      "prints the no-project-config refusal",
	"fugaro intake serve":      "refuses without FUGARO_GCP_PROJECT in its own environment",
	"fugaro issue list":        "reads this machine's issue folder, which holds nothing in this world, and prints no row",
	"fugaro issue submit":      "sweeps the issues that are waiting, and this world's issue folder holds none: it prints that nothing is pending",
	"fugaro issue mine":        "reads no store here: this world's credential lookup is refused before the bucket is opened, so it prints the notes of a machine whose shared state could not be read and no row",
	"fugaro issue sync":        "uploads this machine's issue folder, which holds nothing in this world: the store it uploads to opens, and the run prints that nothing is pending",
	"fugaro managed status":    "prints the no-project-config refusal",
	"fugaro models ls":         "prints the built-in list with no project selected",
	// The orchestrator family reads and writes the state its project's daemon
	// keeps. state and events are driven to their read here — the fixture's
	// database holds no node yet, which is the empty case both are written for
	// — and patch refuses its missing -f before it selects anything, because
	// the document it applies is the whole of what it has to be handed. ls
	// takes no argument and its bare run is the whole command: it lists the
	// node the fixture's database holds (none) and the cached briefs of the
	// machine it runs on, which this harness does not fixture.
	"fugaro orchestrator ls":     "lists what this world's database and this machine's cache directory hold: no orchestrator node here, and the cache files are the machine's own",
	"fugaro orchestrator state":  "reads project aurora's state node out of the fixture's database, which holds none yet, and prints what an empty state is",
	"fugaro orchestrator events": "reads project aurora's audit node out of the fixture's database, which holds none yet: there are no events to print",
	"fugaro orchestrator patch":  "refuses its missing -f: a patch document is what it applies, and the closed stdin is no document",
	"fugaro orchestrator import": "refuses its missing -f: a dump is one patch document, and the closed stdin is no document",
	// The resident service's own entry point, hidden like the credentials
	// service's and the intake's: what it would read is its three names in the
	// environment, which the installation module sets on the container and
	// this world leaves unset (P5.1c).
	"fugaro orchestrator serve": "refuses without FUGARO_PROJECT in its own environment",
	// The escalation family reads that same node for the ladder's ledger (E13).
	// ls takes no argument, and its bare run is the whole command: the fixture's
	// database holds no work item, so what it says is that no pull request has
	// entered the ladder. show needs the item it is about, so it is fixtured
	// below with one this world does not hold — the read's own refusal rather
	// than cobra's usage error — and the closed stdin asks it nothing either way.
	"fugaro escalation ls": "reads project aurora's state node out of the fixture's database, which holds no work item, and says no pull request has entered the ladder",
	// The login unit's three commands read this machine's own home and nothing
	// else, and this world's home has no unit: install is refused before its
	// typed confirmation (a unit runs code at every login, and the word install
	// is not something a closed pipe can type), and uninstall and status answer
	// about a machine that has none.
	"fugaro orchestrator install":   "refuses before its ask: the typed confirmation of a unit that runs code at every login needs a real terminal",
	"fugaro orchestrator status":    "reports this machine's login unit, and this world's home has none: it says so and returns",
	"fugaro orchestrator uninstall": "says there is nothing to remove: this world's home has no login unit",
	"fugaro pair":                   "refuses from inside its body: the world's project has no budget.firebase_api_key for the phone to exchange the token with",
	"fugaro pair ls":                "prints the no-project-config refusal",
	"fugaro plan graph":             "refuses with no graph artifact under docs/plans",
	"fugaro plan status":            "reads the repository's plan and stream files and reports what is missing",
	"fugaro project clone-url ls":   "reads the selected project's own config, which this world's gives no repository a URL in, and prints that it gives none",
	"fugaro project policy publish": "refuses before its ask: the deployed rules here carry no policy marker",
	"fugaro project policy show":    "prints that this installation has published no policy",
	"fugaro project profile ls":     "lists this world's config, which sets no profiles block, and says that it defines none",
	"fugaro project profile show":   "refuses from inside its body: this world's config defines no standard profile for it to show",
	"fugaro project repos ls":       "prints the no-project-config refusal",
	"fugaro project repos set":      "prints the no-project-config refusal: this world selects no project, and the selection is read before either of the command's two shapes is",
	"fugaro project routing show":   "prints this world's config's routing line: no switch is written there, which reads as the direct route",
	"fugaro project routing unset":  "finds no routing block in this world's config, so the remove has nothing to remove and prints that there is no change",
	"fugaro project show":           "prints the no-project-config refusal",
	"fugaro project sync":           "refuses before its write: the deployed rules this world serves carry no policy marker",
	"fugaro reaper":                 "prints the no-project-config refusal",
	"fugaro recipes ls":             "prints the no-project-config refusal",
	"fugaro report":                 "refuses: the edition is not in this build",
	"fugaro review":                 "refuses from inside its body: --pr is required, and a bare run names it rather than launching a review",
	"fugaro rigor floor show":       "reads the selected project's own config and prints the floor it carries, which is the whole of what a bare run does",
	"fugaro routing ls":             "prints this world's project's routing table — empty, since the config writes no model_routing block — and one row per model it can be served on, which is the whole of what a bare run does",
	"fugaro runs digest":            "prints the no-project-config refusal",
	"fugaro runs ls":                "prints the no-project-config refusal",
	"fugaro runs reindex":           "prints the no-project-config refusal",
	"fugaro runs reports":           "refuses from inside its body: it writes the project's stored reports only as the project's reports job, and a bare run says so and names fugaro runs digest",
	"fugaro runs stats":             "prints the no-project-config refusal",
	"fugaro runs summary":           "prints the no-project-config refusal",
	"fugaro runs watch":             "prints the no-project-config refusal",
	"fugaro secrets":                "is a name for set, ls and rm: a bare run prints its own usage and returns",
	"fugaro secrets ls":             "prints the no-project-config refusal",
	"fugaro secrets shared":         "is a name for set, ls and rm: a bare run prints its own usage and returns",
	"fugaro secrets shared ls":      "prints the no-project-config refusal",
	"fugaro secrets user":           "is a name for set, ls and rm: a bare run prints its own usage and returns",
	"fugaro secrets user ls":        "prints the no-project-config refusal",
	"fugaro security paths":         "refuses from inside its body: it answers only for the checkout it stands in, and this world's directory names no origin repository",
	"fugaro settings":               "prints the registry's every row with no project selected, which is the whole of what a bare run does",
	"fugaro seats":                  "reads the selected project's records for an advisory estimate, and this world selects no project",
	"fugaro setup":                  "refuses on this world's runs bucket name before the wizard's first step",
	// The diagnosis takes the newest logged command that failed when it is
	// given no run ID, and this world's machine has logged none: the bare run
	// reaches the command's own "nothing to explain" refusal. Any other run
	// would need a log and a source tree, which are fixtures of their own
	// (troubleshoot_test.go); what this harness asks of the leaf is the
	// policy's own question, and a bare run answers it as well as a fixtured
	// one, because the command reads no stdin on any path.
	"fugaro troubleshoot":  "refuses from inside its body: this world's machine has logged no failed command, and a bare run takes the newest one",
	"fugaro update":        "refuses on this world's runs bucket name before its first stage",
	"fugaro update-skills": "reports that a development build has no release to pin the plugin to",
	"fugaro user ls":       "prints the no-project-config refusal",
	"fugaro validate":      "refuses with no fugaro.yaml in the world",
	"fugaro version":       "prints the version",
	"fugaro voice serve":   "refuses without FUGARO_FIREBASE_PROJECT in its own environment",
	"fugaro workspace ls":  "prints that no config on this machine names a workspace",
	// The workspace family's own reads and its two member-side writes (I1): a
	// bare run is the whole of what each of them does with no argument -- a
	// reading of the selected config, or the refusal for a machine whose config
	// is not a workspace's own and names no member to link or unlink.
	"fugaro workspace project link":   "refuses from inside its body: this world's config names no workspace, and linking a member is its owner's own act on the config that names one",
	"fugaro workspace project unlink": "refuses from inside its body for the reason a link does: this world's config names no workspace to unlink",
	"fugaro workspace project save":   "refuses from inside its body: the selected project declares no member table, no cap and no shared credential, so it is not a workspace's own",
	"fugaro workspace project ls":     "reads the member table of the selected config, which this world's declares none of, and prints that refusal",
	"fugaro workspace project status": "prints the same refusal a bare ls does: the selected config declares no member table",
	"fugaro workspace user ls":        "refuses from inside its body: --workspace is required, and a member belongs to one workspace",
	"fugaro workspace domains ls":     "refuses from inside its body: --workspace is required, and the domain list belongs to one workspace",
}

// closedStdinPrompts is the allow-list: the leaves that still print a question
// with a closed stdin, each with the task of the plan that removes it. Every
// entry was a promise to delete it in that task's commit, and the run's own
// check held the promise — a leaf that stops asking failed until its entry was
// gone.
//
// It is EMPTY, and that is the policy's first constraint as this harness can see
// it: no leaf of the tree asks anybody anything when nobody is there. The asks
// that are still in the tree are the ones this world cannot drive its leaf to,
// and they are named in closedStdinUnreached instead — a list of leaves that
// were proved quiet is not what is left to fix. The map stays, empty, because
// the check below reads it in both directions: the next leaf that grows a prompt
// fails here until somebody names the task that removes it.
var closedStdinPrompts = map[string]string{}

// closedStdinUnreachedAsk is one ask this world cannot drive its leaf to: the
// file the ask is written in, the line that has to survive for the entry to be
// live, the task of the plan that removes the ask, and why the harness stops
// short of it.
type closedStdinUnreachedAsk struct {
	file   string
	marker string
	task   string
	why    string
}

// closedStdinUnreached names the leaves whose question is out of this harness's
// reach, and it is the honest half of the ledger: nothing here is proved quiet.
// Each entry is a promise of the same kind as closedStdinPrompts' — the task
// named removes the ask, and the commit that removes it has to delete this entry
// too, or the marker check below fails.
var closedStdinUnreached = map[string]closedStdinUnreachedAsk{
	// The refresh's own two cloud steps — the base pin and the daily image check
	// job's config — ask from inside the run, and reaching one needs the jobs and
	// the registry this world does not fake. Its billable builds asked here too
	// until T4 removed the build's confirmation: what is left of this entry is
	// T7's, which is the task that removes the two asks above it.
	"fugaro image refresh": {
		file: "image_refresh_steps.go", marker: "r.confirm(",
		task: "T7", why: "asks from the refresh's own plan, which needs the jobs this world does not fake",
	},
	// The published policy's own two writes ask before they replace rules
	// nothing can read back after them; both stand behind a deployed-rules read.
	"fugaro project policy publish": {
		file: "project_cmd.go", marker: "confirmRemoval(cmd, lc, o.yes,",
		task: "T7", why: "asks after a deployed-rules read, which this world refuses",
	},
	// update and setup ask from their stages: the policy step's loosening, the
	// layer step, and the wizard's own confirmations. The workspace step asked
	// here through T15, which removed its last ask — the membership is the
	// owner's own session now and the removal asks nothing at all
	// (TestWorkspaceRemovalAsksNothingAtATerminal) — so what is left of this
	// entry is the policy stage's typed loosening, which T12 removes with the
	// class system the call is written in.
	"fugaro update": {
		file: "init_policy_migrate.go", marker: "confirmTypedStrict(cmd, m.lc, why, what)",
		task: "T12", why: "asks from its stages, which need an installation this world does not build",
	},
	"fugaro setup": {
		file: "init.go", marker: "func (r *initRun) ask(what string) (bool, error) {",
		task: "T7", why: "asks from the wizard's steps, which need the cloud fixture to reach",
	},
}

// TestEveryCommandRunsWithClosedStdin runs every leaf of the released tree with
// its stdin closed and holds it to the policy: it returns, its own body runs, it
// reaches the work its fixture is for, and it asks nothing.
func TestEveryCommandRunsWithClosedStdin(t *testing.T) {
	leaves := closedStdinLeaves(t)
	paths := closedStdinPaths(leaves)
	ran := 0
	for _, leaf := range leaves {
		path := leaf.CommandPath()
		ran++
		t.Run(path, func(t *testing.T) {
			// One world per leaf: a run that writes must not decide what the
			// next leaf finds, whatever order the tree walks in.
			f := closedStdinWorld(t)
			// What the leaf needs beyond the shared world, before it runs: a
			// switch for the resume to move, and nothing at all for most leaves.
			if fixture := closedStdinFixtures[path]; fixture.seed != nil {
				fixture.seed(t, f)
			}
			out, returned, entered, err := closedStdinRun(t, path)
			if !returned {
				t.Fatalf("%s did not return within %s with its stdin closed: a read that waits for a person is a wait nobody can end (it printed %q)", path, closedStdinDeadline, out)
			}
			if !entered {
				t.Fatalf("%s never reached its own body with its stdin closed (%v), so this run says nothing about what the command does: give it the arguments it needs in closedStdinFixtures", path, err)
			}
			if fixture, ok := closedStdinFixtures[path]; ok && fixture.reaches != "" && !strings.Contains(out, fixture.reaches) {
				t.Fatalf("the fixture for %s no longer reaches %q, so it refuses before the work it is here to reach: the run printed %q", path, fixture.reaches, out)
			}
			closedStdinAsked(t, path, out)
		})
	}
	if ran == 0 {
		t.Fatal("the walk found no leaf to run: the harness is reading nothing")
	}
	// A name that must be a leaf whatever else moves, so that a walk which
	// stopped finding the tree fails here rather than passing over nothing.
	if !slices.Contains(paths, "fugaro version") {
		t.Fatal("the walk does not find `fugaro version`, which every build holds: the tree it walks is not the released one")
	}
	// Every leaf is fixtured or exempted, and every exemption is a reason. A
	// leaf in neither is the one way this harness could pass a command it never
	// drove, so it fails here naming the leaf rather than joining the count.
	for _, leaf := range leaves {
		path := leaf.CommandPath()
		_, fixtured := closedStdinFixtures[path]
		why, exempt := closedStdinNoFixture[path]
		if closedStdinRetiredLeaf(leaf) {
			exempt, why = true, closedStdinRetiredWhy
		}
		switch {
		case fixtured && exempt:
			t.Errorf("%s is fixtured and exempt at once: the fixture is what a bare run cannot do, so one of the two is stale", path)
		case !fixtured && !exempt:
			t.Errorf("%s is neither fixtured nor exempt: its bare run stops at whatever arguments it takes, so this run would say nothing about the command. Give it the arguments it needs in closedStdinFixtures, or say in closedStdinNoFixture what its bare run is", path)
		case exempt && why == "":
			t.Errorf("%s is exempt with no reason: an exemption nobody can read is an omission", path)
		}
	}
	// A fixture with no arguments is a bare run wearing a fixture's name, and
	// the classification above would then hold a leaf that is in fact unfixtured.
	for path, fixture := range closedStdinFixtures {
		if len(fixture.args) == 0 {
			t.Errorf("%s has a fixture with no arguments: that is a bare run, so either give it what it needs or move it to closedStdinNoFixture", path)
		}
	}
	for _, named := range [][]string{
		closedStdinNamed(closedStdinFixtures),
		closedStdinNamed(closedStdinNoFixture),
		closedStdinNamed(closedStdinPrompts),
		closedStdinNamed(closedStdinUnreached),
	} {
		for _, path := range named {
			if !slices.Contains(paths, path) {
				t.Errorf("%s is named a leaf and is not one: a fixture, an exemption or a prompt entry that outlives its command is a rule about nothing", path)
			}
		}
	}
	for path := range closedStdinPrompts {
		if fixture, ok := closedStdinFixtures[path]; !ok || fixture.reaches == "" {
			t.Errorf("%s is listed as asking without a fixture that reaches the ask: an entry the harness cannot show is a claim about a run nobody made", path)
		}
	}
	closedStdinUnreachedIsLive(t)
}

// closedStdinUnreachedIsLive holds the unreached ledger to the source it is
// about: the ask each entry names has to still be written where the entry says,
// so the commit that removes the confirmation fails here until it removes the
// entry beside it.
func closedStdinUnreachedIsLive(t *testing.T) {
	t.Helper()
	for path, ask := range closedStdinUnreached {
		source, err := os.ReadFile(filepath.Join(closedStdinSourceDir, ask.file))
		if err != nil {
			t.Errorf("%s is listed with an ask in %s, which cannot be read here: %v", path, ask.file, err)
			continue
		}
		if ask.task == "" || ask.why == "" {
			t.Errorf("%s is listed as unreachable with no task or no reason: the entry is the whole of what a reader has to go on", path)
		}
		if !strings.Contains(string(source), ask.marker) {
			t.Errorf("%s is listed as asking somewhere this harness cannot reach, and %s no longer holds %q: the ask it names is gone, so the entry goes with it", path, ask.file, ask.marker)
		}
	}
}

// closedStdinAsked holds one leaf's output to the policy's other half: a
// question printed with a closed stdin is a prompt nobody can answer, and it is
// either named in closedStdinPrompts or it is a failure. The reverse is checked
// here too, so the list cannot keep an entry whose prompt the task that owns it
// has removed.
func closedStdinAsked(t *testing.T, path, out string) {
	t.Helper()
	question := closedStdinQuestion.FindString(out)
	if question == "" {
		if owner, listed := closedStdinPrompts[path]; listed {
			t.Errorf("%s no longer prints a question with its stdin closed, so its closedStdinPrompts entry (%s) is stale: delete it in this commit", path, owner)
		}
		return
	}
	if _, listed := closedStdinPrompts[path]; listed {
		return
	}
	t.Errorf("%s printed a question with its stdin closed (%q): a closed stdin is nobody to answer, so the run asks a person who is not there\n%s", path, question, out)
}

// closedStdinRetiredLeaf reports the tree's one tombstone (retired.go). It is a
// leaf like any other — the harness runs it and holds it to the deadline and to
// the question check — but it is exempt from the classification by this rule
// rather than by its path, because its path is the one string the live-tree
// census (docs_live_init_census_test.go, W0.2) allows in the files that retire
// the command and nowhere else, and this is not one of them. Both the sentence
// it prints and the way it names itself say what it is.
func closedStdinRetiredLeaf(leaf *cobra.Command) bool {
	return leaf.Hidden && strings.HasPrefix(leaf.Short, "Retired:")
}

// closedStdinRetiredWhy is that exemption's reason, beside the same list's rule.
const closedStdinRetiredWhy = "is the tombstone: it prints the two commands that replaced it and exits non-zero"

// closedStdinSetBaseBranch gives the world's project config a value for the
// setting the unset's fixture removes: an unset of a key no document sets has
// nothing to confirm and says so, which would be the wrong reason for a green
// run.
func closedStdinSetBaseBranch(t *testing.T, f *budgetFixture) {
	t.Helper()
	f.appendConfig(t, "base_branch: main\n")
}

// closedStdinSetProvider gives the world's project config the provider route
// the removal takes away. Without it the leaf stops at "this project has no
// provider named acme" and the fixture's sentence would never be reached — a
// removal of an entry that is not there is a refusal, not a write.
func closedStdinSetProvider(t *testing.T, f *budgetFixture) {
	t.Helper()
	f.appendConfig(t, "providers:\n  acme:\n    kind: anthropic-compat\n    base_url: https://api.example.com\n    auth: bearer\n    secret: acme-key\n    models: [acme/model]\n")
}

// closedStdinCloneURLSeed gives the world's own project config the entry the
// clone_urls removal takes away, beside one it leaves. Without the first the
// leaf would stop at "this project gives no clone URL for it" and would say
// nothing about the write; without the second the removal would be the last
// entry's, which the command refuses for the reason its own refusal gives.
func closedStdinCloneURLSeed(t *testing.T, f *budgetFixture) {
	t.Helper()
	f.appendConfig(t, "clone_urls:\n  github/acme/app: https://github.com/acme/app\n  github/acme/other: https://github.com/acme/other.git\n")
}

// closedStdinBinariesSeed gives the world the binaries block the two narrowing
// writes take a member out of: the pattern the deny removes, and a cap above
// the one the fixture writes, so the write is the lowering direction — the one
// a session may make and the one whose sentence names the member (D2617).
func closedStdinBinariesSeed(t *testing.T, f *budgetFixture) {
	t.Helper()
	f.appendConfig(t, "binaries:\n  allow:\n    - brand/*.png\n  max_bytes: 2097152\n")
}

// closedStdinPoolSeed gives the world the two things a pool write needs to
// reach its own edit: a credential a member may name (two of them, so the
// declaration the fixture asks for is a change) and a pool to remove.
func closedStdinPoolSeed(t *testing.T, f *budgetFixture) {
	t.Helper()
	f.appendConfig(t, "credentials:\n  - name: team-a\n    type: claude-oauth\n  - name: team-b\n    type: claude-oauth\npools:\n  - name: claude-pool\n    members:\n      - team-a\n")
}

// closedStdinRoutingSeed gives the world the entry the routing rm takes away: a
// removal of a pattern the table does not carry is refused before the write,
// which would leave the fixture's sentence unproven. The pattern answers no
// model any document of the world names, so the chain is judged as the table's
// own entry and nothing else, and the table the removal leaves is the empty one
// the set's fixture writes over.
func closedStdinRoutingSeed(t *testing.T, f *budgetFixture) {
	t.Helper()
	f.appendConfig(t, "model_routing:\n  - match: \"zzz-*\"\n    chain:\n      - {kind: anthropic}\n")
}

// closedStdinAccountAllowListSeed gives the world the account the deny's
// fixture narrows: a removal from an account the config does not declare is
// refused before the write, which would leave the fixture's sentence unproven.
// The allow's own fixture needs no seed — declaring the account is that form's
// work.
func closedStdinAccountAllowListSeed(t *testing.T, f *budgetFixture) {
	t.Helper()
	f.appendConfig(t, "claude_accounts:\n  team-a:\n    allow_data_to:\n      - acme/web\n")
}

// closedStdinAccountAliasSeed gives the world the two halves an alias write
// needs: the accounts an alias may stand for, and an alias already declared, so
// the set's fixture moves an entry rather than declaring one and the rm's has
// one to take away. No choice key names the alias — a key naming it is exactly
// what the rm refuses — and the set's target is the second account rather than
// the one the alias already holds, so the diff it reaches is a move.
func closedStdinAccountAliasSeed(t *testing.T, f *budgetFixture) {
	t.Helper()
	f.appendConfig(t, "claude_accounts:\n  team-a: {}\n  team-b: {}\nclaude_account_aliases:\n  reviewer: team-a\n")
}

// closedStdinAccountSwitchSeed gives the world the two things a switch needs
// before it has a key to rewrite: the account it moves onto has to be declared
// (the resolve's own refusal, which would leave the fixture's list unproven),
// and a key has to name the account it moves off — here the default, which is
// the shortest of the five keys and names no repository, so the subset rule the
// write is judged by has nothing to ask about it.
func closedStdinAccountSwitchSeed(t *testing.T, f *budgetFixture) {
	t.Helper()
	f.appendConfig(t, "claude_accounts:\n  team-a: {}\n  team-b: {}\ndefault_claude_account: team-a\n")
}

// closedStdinProjectSecretAllowListSeed is the same for the project secret's
// own list, and it is also the fixture's repository: the deny removes the one
// it names, and the allow adds to a list that does not already hold it, so
// neither run is the "no change" answer for a state the seed had already
// reached. Only the names the project stores project-wide may appear here — the
// loader refuses any other — and bitbucket-token is the one this list is for.
func closedStdinProjectSecretAllowListSeed(repos ...string) func(*testing.T, *budgetFixture) {
	return func(t *testing.T, f *budgetFixture) {
		t.Helper()
		body := "project_secrets:\n  bitbucket-token:\n    allow_data_to:\n"
		for _, repo := range repos {
			body += "      - " + repo + "\n"
		}
		f.appendConfig(t, body)
	}
}

// closedStdinCraftSeed gives the world's own config the craft `craft rm` has to
// take away, with the credential its access names. A craft the config does not
// state is the removal's own refusal before its write, and a fixture that
// stopped there would call the leaf quiet without the write having begun.
func closedStdinCraftSeed(t *testing.T, f *budgetFixture) {
	t.Helper()
	f.appendConfig(t, "credentials:\n  - name: team-a\n    type: claude-oauth\n  - name: team-b\n    type: claude-oauth\ncrafts:\n  coder:\n    access: team-a\n")
}

// closedStdinRepoAddSeed gives the world's own config the account the
// repository write's list is to gain, under the repository the world already
// holds — which carries the provider every such entry needs. The entry is
// rewritten from the flow map the world writes it as into the block form the
// list is spliced into: a list added to a flow collection is the edit's own
// refusal (yamledit), which would stop the fixture short of its write for a
// spelling of the config rather than for anything the command decides. An
// account the config does not declare is the run's one refusal before the
// write, and a second repos key would be a config no reader loads.
func closedStdinRepoAddSeed(t *testing.T, f *budgetFixture) {
	t.Helper()
	f.rewriteConfig(t,
		"  acme/app: { provider: github, base_branch: main, workflows: [web] }\n",
		"  acme/app:\n    provider: github\n    base_branch: main\n    workflows: [web]\n")
	f.appendConfig(t, "claude_accounts:\n  team-a: {}\n")
}

// closedStdinRigorLayerSeed gives the world the file `fugaro rigor set` edits:
// this machine's own project layer file, carrying the world's own anchors
// (project aurora, GCP project proj-1234, which is what the world's config
// names) and the mode the fixture's raise is measured against. Without it the
// command refuses the missing file — D830's step 4, naming `fugaro update` —
// which is a refusal before the write and not the write this fixture is for.
func closedStdinRigorLayerSeed(t *testing.T, _ *budgetFixture) {
	t.Helper()
	settingsManagedLayerFile(t, minimalAnchored+"defaults:\n  rigor:\n    default: pro\n")
}

// closedStdinSecurityLayerSeed gives the world the file `fugaro rigor security`
// edits — this machine's own project layer file, with the world's own anchors
// and a list of one row a person could have written. Without it both leaves
// refuse the missing file (D830's step 4, naming `fugaro update`), which is a
// refusal before the write and not the write these fixtures are for, and the
// removal would have no row of the list to take away.
func closedStdinSecurityLayerSeed(t *testing.T, _ *budgetFixture) {
	t.Helper()
	settingsManagedLayerFile(t, minimalAnchored+"security:\n  patterns:\n    - name: billing\n      match: internal/billing/**\n      category: credentials\n")
}

// closedStdinRigorFloorSeed gives the world's own project config the review
// floor the two floor leaves take a level out of: the removal and the lowering
// are the directions that used to ask for the project's name typed at a
// terminal, and the seed is what puts them within the fixture's reach. Without
// it both runs would report the floor that is not there, which is a no-op before
// the write rather than the write this fixture is for.
func closedStdinRigorFloorSeed(t *testing.T, f *budgetFixture) {
	t.Helper()
	f.appendConfig(t, "review_floor:\n  pro: [l2, l3]\n")
}

// closedStdinWorkflowSeed makes the world's own directory a checkout of a
// repository of project aurora — the project the world's config selects —
// whose file defines the workflow web and, when extra is not "", the block the
// caller puts there too. The directory is already the world's temporary one, so
// it is the same "the current directory's checkout" a person runs these writes
// in.
func closedStdinWorkflowSeed(extra string) func(*testing.T, *budgetFixture) {
	return func(t *testing.T, _ *budgetFixture) {
		t.Helper()
		dir, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		testutil.Git(t, dir, "init", "-q")
		if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(settingsRepoYAML(extra)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// closedStdinMailboxSeed gives the world the two things the mailbox fixture's
// pass reads before it acts: a pairing in the world's own project config, and
// that pairing's key in a keychain of the test's own. A pass over a project
// whose config names no mailbox refuses at startup without taking a lease or
// reading an inbox, which would leave the fixture proving nothing about the
// loop it exists for. The state directory is the ledger's — one project's own,
// under a directory of the test's — so the pass records what it consumed
// somewhere this machine does not keep its own state.
func closedStdinMailboxSeed(t *testing.T, f *budgetFixture) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	item := "fugaro-mailbox-aurora-harness"
	f.appendConfig(t, "mailbox:\n  keychain_item:\n    \""+mailboxUID+"\": "+item+"\n")
	store := &keychain.Fake{Keys: map[string]string{item: mailboxKeyMaterial}}
	prev := mailboxKeychain
	mailboxKeychain = func() keychain.Store { return store }
	t.Cleanup(func() { mailboxKeychain = prev })
}

// closedStdinWorld is the state every leaf is run in: one hermetic
// installation, no outbound network, no terminal and no agent marker.
func closedStdinWorld(t *testing.T) *budgetFixture {
	t.Helper()
	// A leaf must not be able to write into the checkout the tests run from
	// (fugaro.yaml, .claude/, a layer file): the harness runs in a directory of
	// its own, which is also the honest "no checkout here" case.
	t.Chdir(t.TempDir())
	f := newBudgetFixture(t, "")
	// The same guard the ee CLI mains carry: an outbound request must not leave
	// this machine. Go bypasses the proxy for loopback, so the fixtures' own
	// servers still answer.
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	// The update check is a GitHub request on the command's own stderr: the
	// harness's streams are buffers, so it never starts, and the switch says so
	// rather than leaving it to a stream's type.
	t.Setenv("FUGARO_NO_UPDATE_CHECK", "1")
	// The run this test runs inside names its own state directory, and `fugaro
	// verify` reads it: with it set, the leaf would run this repository's real
	// build command and take minutes, and the harness would be measuring the
	// machine rather than the command.
	t.Setenv("FUGARO_STATE_DIR", "")
	// The login unit's file belongs under the person's own home on macOS —
	// launchd reads $HOME, where Linux reads the XDG directories the fixture
	// already isolates — and this harness drives the uninstall leaf. Without
	// this, a developer who has the daemon installed would have their own unit
	// deleted by a test run, and the leaf would fail on the file it found
	// rather than on the machine having none.
	t.Setenv("HOME", filepath.Join(t.TempDir(), "home"))
	// Which machine this is must not decide what a run does: a coding agent's
	// session refuses writes the closed-stdin case would otherwise reach, and a
	// real terminal is what this harness is the opposite of.
	clearAgentMarkers(t)
	terminal(t, false)
	return f
}

// closedStdinRun runs one leaf with its stdin a closed pipe and reports what it
// printed, whether it returned before the deadline, whether its own body ran at
// all, and what it returned. The output is a syncBuf's because a run that timed
// out is still writing to it.
func closedStdinRun(t *testing.T, path string) (string, bool, bool, error) {
	t.Helper()
	// The leaf's own words, without the program name: cobra is handed the
	// arguments after `fugaro`, and the path's first word is the binary.
	argv := strings.Fields(path)[1:]
	if fixture, ok := closedStdinFixtures[path]; ok {
		argv = append(argv, fixture.args...)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// The write end is closed before the run, so the pipe is at EOF: this is
	// the closed stdin the policy names, and it is an *os.File, which is what
	// makes stdinIsTerminal answer false for the unpatched reason too.
	w.Close()
	defer r.Close()
	out := &syncBuf{}
	// The tree is this run's own, so the wrapper that marks the body is too: a
	// tree shared with the next leaf would carry this one's flags into it.
	root := NewRootCmd()
	var entered atomic.Bool
	leaf := closedStdinLeaf(root, path)
	if leaf == nil {
		t.Fatalf("%s is not in the tree NewRootCmd builds", path)
	}
	if run := leaf.RunE; run != nil {
		leaf.RunE = func(c *cobra.Command, args []string) error {
			entered.Store(true)
			return run(c, args)
		}
	} else if plain := leaf.Run; plain != nil {
		leaf.Run = func(c *cobra.Command, args []string) {
			entered.Store(true)
			plain(c, args)
		}
	} else {
		t.Fatalf("%s has neither Run nor RunE", path)
	}
	root.SetIn(r)
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs(NormalizeArgs(argv))
	done := make(chan struct{})
	var execErr error
	go func() {
		defer close(done)
		execErr = root.Execute()
	}()
	select {
	case <-done:
		return out.String(), true, entered.Load(), execErr
	case <-time.After(closedStdinDeadline):
		// entered is not read here: the run is still going, and the leaf that
		// did not return is the failure.
		return out.String(), false, false, nil
	}
}

// closedStdinLeaf is the leaf of root whose command path is path, or nil.
func closedStdinLeaf(root *cobra.Command, path string) *cobra.Command {
	for _, leaf := range closedStdinLeavesOf(root) {
		if leaf.CommandPath() == path {
			return leaf
		}
	}
	return nil
}

// closedStdinLeaves is every command of the released tree a run can end on:
// the commands with no subcommand, and the commands that carry a subcommand
// tree and a body of their own. The second kind is here because the policy is
// about every command and not only the ones nothing hangs from: cobra runs a
// parent's body when no subcommand matches the arguments, so `fugaro settings`
// bare prints every setting and `fugaro review` bare refuses its missing --pr
// from inside that body — both of them a closed stdin away from the same
// question the leaves are held to. A parent with subcommands and no body of its
// own is a name for its children and runs nothing, so it is not one of these.
//
// Cobra's own `help` and `completion` are not in the tree NewRootCmd builds —
// cobra adds them when a tree is executed — so a leaf here is always a command
// fugaro ships.
func closedStdinLeaves(t *testing.T) []*cobra.Command {
	t.Helper()
	return closedStdinLeavesOf(NewRootCmd())
}

func closedStdinLeavesOf(root *cobra.Command) []*cobra.Command {
	var leaves []*cobra.Command
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if len(c.Commands()) == 0 || c.Run != nil || c.RunE != nil {
			leaves = append(leaves, c)
		}
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	walk(root)
	return leaves
}

// closedStdinPaths is the leaves' own paths, for the checks that a name in one
// of the lists is a command the tree still holds.
func closedStdinPaths(leaves []*cobra.Command) []string {
	paths := make([]string, 0, len(leaves))
	for _, leaf := range leaves {
		paths = append(paths, leaf.CommandPath())
	}
	return paths
}

// closedStdinNamed is one list's keys, as a slice.
func closedStdinNamed[V any](m map[string]V) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	return names
}

// closedStdinSourceDir is where the unreached ledger's files are read from: the
// package itself, which is the working directory `go test` runs this in.
var closedStdinSourceDir = "."

// TestClosedStdinHarnessSeesAQuestion holds the harness's own reading against
// runs whose answer is known, so that a green TestEveryCommandRunsWithClosedStdin
// means the leaves ask nothing rather than that a question went unnoticed. Both
// shapes are here — the banner and the prompt line — beside the refusals that
// must not be read as either.
func TestClosedStdinHarnessSeesAQuestion(t *testing.T) {
	for _, c := range []struct {
		name string
		out  string
		want bool
	}{
		{
			name: "the typed confirmation of a budget raise",
			out:  "config/caps/global: global caps per-run: not set -> $50\n⚠ CONFIRM (project aurora): this loosens the project's budget: runs may spend more than before\n",
			want: true,
		},
		{
			name: "the prompt line on its own",
			out:  "Type aurora to confirm: ",
			want: true,
		},
		{
			name: "a value's prompt",
			out:  "Type the billing account ID: ",
			want: true,
		},
		{
			// The one the anchoring is for: a prompt is a line, and the run
			// goes on after it — with the refusal an ended stdin earns, with a
			// cobra error, with anything. A run was caught when the prompt was
			// the last thing it printed and missed when one line followed it,
			// so this is the case that has to stay green.
			name: "a prompt with the run's refusal below it",
			out:  "Type aurora to confirm: \nthe typed confirmation of this step needs a real terminal: run it in your own terminal window, not through a coding agent or a pipe\n",
			want: true,
		},
		{
			// No banner anywhere in it: the y/N family prints the question and
			// nothing else, so this is the shape that matches on its own.
			name: "the y/N prompt, which prints no banner",
			out:  "Write this to /tmp/x/fugaro.yaml? [y/N]: ",
			want: true,
		},
		{
			// The same ordering hazard for the y/N family: the prompt is on the
			// line the run continues on.
			name: "a y/N prompt the run continues past",
			out:  "Write this to /tmp/x/fugaro.yaml? [y/N]: the confirmation of this write needs a real terminal\n",
			want: true,
		},
		{
			// The wizard's own question, which says neither "Type" nor "[y/N]":
			// promptChoice prints the label with the default it would take in
			// brackets, and a leaf that reached it and read the ended stdin
			// would answer it with the default rather than with a person.
			name: "the setup wizard's answer, with its default in brackets",
			out:  "GCP project ID [aurora]: ",
			want: true,
		},
		{
			// The same line with the second answer promptChoice offers: the
			// brackets are not at the end here, so this is the case that says
			// the reading is the line's ending and not the bracket.
			name: "the wizard's question with the alternative answer after it",
			out:  "GCP project ID [aurora] (or 123456: create that project instead): ",
			want: true,
		},
		{
			// promptChoice with no default prints no bracket at all: the GitHub
			// App ID is asked this way on a repository that has no stored one.
			name: "the wizard's question with no default",
			out:  "GitHub App ID for acme/app (a number, not a secret; on the App's settings page): ",
			want: true,
		},
		{
			// The hidden value prompt, which is a sentence rather than a label.
			name: "the hidden value prompt",
			out:  "Paste the private key for fugaro-app (input hidden), from its -----BEGIN line to its -----END line, then press Enter: ",
			want: true,
		},
		{
			// Pairing's own question, in lower case, on the line after the code
			// the phone shows.
			name: "the pairing code prompt",
			out:  "the phone shows AB-CD-EF\ntype that code: ",
			want: true,
		},
		{
			name: "the refusal that replaces the prompt",
			out:  "the typed confirmation of this step needs a real terminal: run it in your own terminal window, not through a coding agent or a pipe\n",
			want: false,
		},
		{
			name: "a note that merely mentions a prompt",
			out:  "the value is empty; pipe it in on stdin or paste it at the prompt\n",
			want: false,
		},
		{
			// A colon that is not a question: no space, so no prompt. The help
			// cobra and these commands print is full of these, and reading them
			// as questions would make the harness cry wolf on every leaf.
			name: "a heading that ends in a bare colon",
			out:  "Usage:\n  fugaro version [flags]\n\nFlags:\n  -h, --help   help for version\n",
			want: false,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := closedStdinQuestion.MatchString(c.out); got != c.want {
				t.Errorf("the harness reads a question in %q as %v, want %v", c.out, got, c.want)
			}
		})
	}
	// The ledger's files are read from this package's own directory, which the
	// world's t.Chdir moves: this is where that assumption is stated, and the
	// check above it reads before any subtest has moved anything.
	if _, err := os.Stat(filepath.Join(closedStdinSourceDir, "managed_flag.go")); err != nil {
		t.Errorf("the unreached ledger reads this package's files from %q, which does not hold them: %v", closedStdinSourceDir, err)
	}
}
