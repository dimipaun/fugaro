package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// fugaro upgrade (docs/design/upgrade.md): each checkout brought up to this
// binary in five steps, pin, plugin, cloud, rules and policy, stopping that
// checkout at the first step that fails; every checkout is tried, one summary
// ends the run, and the exit code is the worst.

type upgradeOptions struct {
	check, local, yes, allowFork bool
	// refresh holds the cloud step's flags, passed on to runImageRefresh.
	refresh refreshOptions
}

// stepState is a step's outcome. The words are the output's, and the upgrade
// skill's reference explains each.
type stepState string

const (
	stepCurrent stepState = "current" // nothing to do
	stepDone    stepState = "done"    // it changed something
	stepStale   stepState = "stale"   // --check: it would change something
	stepSkipped stepState = "skipped" // not run here, by rule; the reason says why
	stepFailed  stepState = "failed"  // the checkout stops here
	stepNotRun  stepState = "not run" // after a failed step
)

var stepStates = []stepState{stepCurrent, stepDone, stepStale, stepSkipped, stepFailed, stepNotRun}

type stepResult struct {
	name   string // set by upgradeCheckout
	state  stepState
	reason string // one line, safe to print
	code   int    // a failed step's exit code
}

// upgradeCtx is what a step sees of one checkout.
type upgradeCtx struct {
	cmd   *cobra.Command
	w     io.Writer
	o     upgradeOptions
	loc   pluginwire.Location // the checkout's top and its settings file
	agent string              // the coding agent's marker, "" outside one
}

type upgradeStep struct {
	name string
	run  func(ctx context.Context, u *upgradeCtx) stepResult
}

// upgradeSteps run in this order for each checkout.
var upgradeSteps = []upgradeStep{{"pin", pinStep}, {"plugin", pluginStep}, {"cloud", cloudStep}, {"rules", rulesStep}, {"policy", policyStep}}

type checkoutResult struct {
	path  string // as given
	root  string // the checkout's top, "" when there is none
	dup   bool   // the same checkout as an earlier path
	steps []stepResult
}

func newUpgradeCmd() *cobra.Command {
	var o upgradeOptions
	cmd := &cobra.Command{
		Use:   "upgrade [PATH...]",
		Short: "Bring checkouts up to this fugaro: pin the plugin, install or update it in Claude Code, refresh the repository's images",
		Long: `upgrade brings each checkout (PATH, default the current directory; several are
processed one after the other, each on its own) up to this fugaro, in five
steps, stopping that checkout at the first step that fails:

  pin     what fugaro update-skills does: the Fugaro plugin in the checkout's
          .claude/settings.json pinned to this release's tag, shown as a diff
          and never committed; a fork's marketplace moves only with
          --allow-fork; a file that is not valid JSON is never rewritten; a
          pin newer than this fugaro is never moved down.
  plugin  the plugin installed (user scope) or updated (each install that
          applies to the checkout, at its own scope) in Claude Code through
          the claude CLI on PATH: a fixed set of claude plugin commands, never
          with -y. Without claude it prints the slash commands to type in
          Claude Code. Running Claude Code sessions apply it after a restart.
  cloud   what fugaro image refresh does for the checkout's repository: this
          release's base image, the daily image check job, the builds. It
          asks at each step, or with --yes confirms every step itself, each
          billable build included, with no cap. Skipped where there is
          nothing to refresh from here: no fugaro.yaml, no local project
          config (a teammate), a repository not onboarded. After a real
          refresh it also compares the deployed job's whole copy of the
          local project config (providers, model_prices, budget settings,
          the region's compute price override and the GitHub App ID;
          multi-model.md) with what the local config would write now: a
          version upgrade never refreshes that copy, so a mismatch prints a
          note naming the fields and the one command that does, fugaro update
          --repo . (never run here: its Terraform confirmations stay
          yours). --check makes no cloud call, so it cannot tell and says
          so instead. It checks each workflow's default-account job;
          named-account jobs are updated by the same fugaro update --repo .
  rules   the security rules generation a project policy is read under,
          deployed into the project's Firebase database, the same deploy
          fugaro update's rules stage makes: the plan is printed and the
          project's name typed (--yes confirms it, and is the way past the
          typed confirmation without a terminal). Deployed when the deployed
          rules are absent, older, pre-policy or a different document.
          Skipped where there is no shared budget backend, where the database
          is one this installation may not write (the ownership check every
          writer into it asks), where the deployed marker is newer or hand
          edited, and where the rules or their marker could not be read.
          --check makes no cloud call here either and says it cannot tell.
          It is what fugaro update's rules stage does; both doors run it, and
          this one runs it before the policy step because the policy writer
          refuses a migration the deployed rules would not admit.
  policy  the project's published policy node, if an earlier Fugaro wrote it
          in an older version: migrated in place (never deleted and
          published again), after a diff of what changes, what is kept and
          what is derived from the local config, and the project's name typed
          at a terminal. --yes confirms it, and is the way past the refusal a
          run in flight makes without it. Skipped where there is no local
          project config or the repository is not onboarded, and in a coding
          agent's session, with the command to run in your own terminal. It
          reads the published node, so --check makes no call here either and
          says it cannot tell. This is what fugaro update does for the same
          node; both doors run it.

It never upgrades fugaro itself: brew upgrade dimipaun/tap/fugaro && fugaro
upgrade is the whole sequence.

--local runs pin and plugin only. Under --local and in a coding agent's
session (CLAUDECODE and the like) the cloud, rules and policy steps are
skipped before they read anything, never attempted, with the command to run in
your own terminal, so the exit code is 0 when the local steps succeeded.
--check writes nothing, runs no claude command and makes no cloud call: it
reads the settings file, Claude Code's record of installed plugins and the
local project config, prints what each step would do and exits 1 when anything
is stale (build records are not read, and neither the deployed security rules
nor the published policy node is: the rules and policy steps say they cannot
tell). Without --yes, --local or --check it needs a real terminal. There is no
--json.

Exit codes: 0 every checkout finished or had nothing to do; 1 a refusal, a
failed local step, a cloud step that cannot start from the checkout's files
(a bad fugaro.yaml or origin, an unreadable config; with --check too) or
(with --check) something stale; otherwise a failed cloud step exits with the
refresh's own code, 2 for a cloud call that failed. The summary names each
checkout's steps and, for one that stopped, the line to rerun.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runUpgrade(cmd, o, args) },
	}
	f := cmd.Flags()
	f.BoolVar(&o.check, "check", false, "write nothing, run no claude command, make no cloud call: print what each step would do and exit 1 when anything is stale")
	f.BoolVar(&o.local, "local", false, "run the pin and plugin steps only, and skip the cloud, rules and policy steps (the only mode that does everything it can inside a coding agent's session)")
	f.BoolVar(&o.yes, "yes", false, "confirm every step that asks without a terminal, and without asking: the cloud step's base copy, check-job update and each billable build (with no cap), and the security rules and published policy deployments; never in a coding agent's session")
	f.BoolVar(&o.allowFork, "allow-fork", false, "move the ref of a marketplace repository other than dimipaun/fugaro (a fork you host) to this release's tag, and add it to Claude Code")
	f.StringVar(&o.refresh.repo, "repo", "", "owner/name of the repository; it must be the checkout's origin (one PATH only)")
	f.StringArrayVar(&o.refresh.workflows, "workflow", nil, "a workflow to refresh (repeatable; default: every workflow in fugaro.yaml; one PATH only)")
	f.StringVar(&o.refresh.imageSource, "image-source", "", "the registry and owner the release base images come from: a base kind is resolved and signature-verified there and pinned through the installation's remote repository (default ghcr.io/dimipaun)")
	f.StringArrayVar(&o.refresh.expectDigests, "expect-digest", nil, "cross-check the digest of a base image's release signature, KIND=sha256:<hex> (repeatable; one PATH only)")
	addCloudFlags(cmd, &o.refresh.cloud)
	return cmd
}

func runUpgrade(cmd *cobra.Command, o upgradeOptions, paths []string) error {
	ctx := cmd.Context()
	w := cmd.OutOrStdout()
	if err := o.validate(len(paths)); err != nil {
		return err
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}
	agent := agentMarker(os.Getenv)
	// U11: a development build has no release to pin, install or refresh to,
	// so every step is skipped and the run exits 0; it never needs a terminal.
	if releaseVersion() != "" && !o.check && !o.local && !o.yes && agent == "" && !stdinIsTerminal(cmd.InOrStdin()) {
		return userErr("fugaro upgrade's cloud step asks before each change and each billable build, so it needs a real terminal: %s; or pass --yes to confirm every step, or --local for the pin and plugin steps only", initflow.NoTerminalAdvice)
	}
	fmt.Fprintln(w, upgradeHeader())
	seen := map[string]string{}
	var results []checkoutResult
	for _, p := range paths {
		if releaseVersion() == "" {
			results = append(results, devCheckout(w, p))
			continue
		}
		results = append(results, upgradeCheckout(ctx, cmd, o, p, agent, seen))
	}
	printUpgradeSummary(w, o, results)
	return upgradeExit(results)
}

func upgradeHeader() string {
	if releaseVersion() == "" {
		return fmt.Sprintf("fugaro upgrade with a development build (%s): there is no release to pin, install or refresh to, so every step is skipped", oneLine(Version))
	}
	return fmt.Sprintf("fugaro upgrade with fugaro %s; it never upgrades fugaro itself: brew upgrade dimipaun/tap/fugaro && fugaro upgrade is the whole sequence", Version)
}

// validate is decision U12: flag combinations refused before anything runs.
func (o upgradeOptions) validate(npaths int) error {
	r := o.refresh
	switch {
	case o.check && o.yes:
		return userErr("--check writes nothing, so --yes has nothing to confirm: pass one or the other")
	case o.local && o.yes:
		return userErr("--yes confirms the cloud, rules and policy steps, which --local skips: pass one or the other")
	case o.local && (r.repo != "" || len(r.workflows) > 0 || r.imageSource != "" || len(r.expectDigests) > 0):
		return userErr("--repo, --workflow, --image-source and --expect-digest are the cloud step's, which --local skips")
	case npaths > 1 && (r.repo != "" || len(r.workflows) > 0 || len(r.expectDigests) > 0):
		return userErr("--repo, --workflow and --expect-digest name one repository's things: give them with one PATH")
	}
	return nil
}

// again is the command line that reruns this upgrade for path, every flag
// kept.
func (o upgradeOptions) again(path string) string {
	args := []string{selfCommand(), "upgrade"}
	for _, f := range []struct {
		on   bool
		name string
	}{{o.check, "--check"}, {o.local, "--local"}, {o.yes, "--yes"}, {o.allowFork, "--allow-fork"}} {
		if f.on {
			args = append(args, f.name)
		}
	}
	r := o.refresh
	if r.repo != "" {
		args = append(args, "--repo", quoteWord(r.repo))
	}
	for _, wf := range r.workflows {
		args = append(args, "--workflow", quoteWord(wf))
	}
	for _, f := range []struct{ name, v string }{
		{"config", r.cloud.config}, {"project", r.cloud.project}, {"gcp-project", r.cloud.gcpProject},
		{"region", r.cloud.region}, {"image-source", r.imageSource},
	} {
		if f.v != "" {
			args = append(args, "--"+f.name, quoteWord(f.v))
		}
	}
	for _, d := range r.expectDigests {
		args = append(args, "--expect-digest", quoteWord(d))
	}
	if strings.HasPrefix(path, "-") {
		args = append(args, "--")
	}
	return strings.Join(append(args, quoteWord(path)), " ")
}

// devCheckout is decision U11: a development build skips every step of every
// path, whatever the path is, and the run exits 0.
func devCheckout(w io.Writer, path string) checkoutResult {
	res := checkoutResult{path: path}
	where := path
	if loc, ok := pluginwire.Locate(path); ok {
		res.root, where = loc.Root, loc.Root
	}
	fmt.Fprintf(w, "== %s\n", pluginwire.Printable(where))
	for _, s := range upgradeSteps {
		r := stepResult{name: s.name, state: stepSkipped, reason: "a development build has no release to pin, install or refresh to"}
		res.steps = append(res.steps, r)
		printStep(w, r)
	}
	return res
}

// checkoutKey identifies a checkout for "two paths in the same checkout count
// once": the top of its git checkout (the nearest directory at or above root
// holding .git), symlinks resolved. A subdirectory with its own .claude and
// the top of the same repository are one checkout.
func checkoutKey(root string) string {
	dir, err := filepath.EvalSymlinks(root)
	if err != nil {
		return filepath.Clean(root)
	}
	for d := dir; ; {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		up := filepath.Dir(d)
		if up == d {
			return dir
		}
		d = up
	}
}

func upgradeCheckout(ctx context.Context, cmd *cobra.Command, o upgradeOptions, path, agent string, seen map[string]string) checkoutResult {
	w := cmd.OutOrStdout()
	res := checkoutResult{path: path}
	refuse := func(state stepState, reason string) checkoutResult {
		r := stepResult{name: "checkout", state: state, reason: reason}
		if state == stepFailed {
			r.code = ExitUserError
		}
		res.steps = append(res.steps, r)
		printStep(w, r)
		return res
	}
	loc, ok := pluginwire.Locate(path)
	if !ok {
		fmt.Fprintf(w, "== %s\n", pluginwire.Printable(path))
		return refuse(stepFailed, "not in a git checkout (or it is your home directory)")
	}
	res.root = loc.Root
	fmt.Fprintf(w, "== %s\n", pluginwire.Printable(loc.Root))
	key := checkoutKey(loc.Root)
	if first, dup := seen[key]; dup {
		res.dup = true
		return refuse(stepSkipped, "the same checkout as "+pluginwire.Printable(first))
	}
	seen[key] = loc.Root
	if !fugaroCheckout(loc) {
		return refuse(stepFailed, "not a Fugaro checkout: it has no fugaro.yaml and its .claude/settings.json does not wire the Fugaro plugin (set it up with fugaro setup and /fugaro:setup first)")
	}
	u := &upgradeCtx{cmd: cmd, w: w, o: o, loc: loc, agent: agent}
	for i, s := range upgradeSteps {
		r := s.run(ctx, u)
		r.name = s.name
		res.steps = append(res.steps, r)
		printStep(w, r)
		if r.state == stepFailed {
			for _, rest := range upgradeSteps[i+1:] {
				res.steps = append(res.steps, stepResult{name: rest.name, state: stepNotRun})
			}
			break
		}
	}
	if res.nothingToDo() {
		fmt.Fprintf(w, "nothing to do: %s is current", pluginwire.Printable(loc.Root))
		if res.anySkipped() {
			fmt.Fprint(w, " (apart from what the skipped steps name)")
		}
		fmt.Fprintln(w)
	}
	return res
}

// fugaroCheckout: a checkout Fugaro is set up in, or being set up in. It has
// a fugaro.yaml, or its settings already wire the plugin (init's plugin
// stage runs before /fugaro:setup writes fugaro.yaml).
func fugaroCheckout(loc pluginwire.Location) bool {
	if _, err := os.Lstat(filepath.Join(loc.Root, "fugaro.yaml")); err == nil {
		return true
	}
	return pluginwire.Status(loc.Settings, Version, "").Pin != pluginwire.NotWired
}

func printStep(w io.Writer, r stepResult) {
	if r.reason == "" {
		fmt.Fprintf(w, "%s: %s\n", r.name, r.state)
		return
	}
	fmt.Fprintf(w, "%s: %s: %s\n", r.name, r.state, r.reason)
}

// nothingToDo: every step current or skipped.
func (c checkoutResult) nothingToDo() bool {
	for _, s := range c.steps {
		if s.state != stepCurrent && s.state != stepSkipped {
			return false
		}
	}
	return len(c.steps) > 0
}

func (c checkoutResult) anySkipped() bool {
	for _, s := range c.steps {
		if s.state == stepSkipped {
			return true
		}
	}
	return false
}

func (c checkoutResult) failure() *stepResult {
	for i := range c.steps {
		if c.steps[i].state == stepFailed {
			return &c.steps[i]
		}
	}
	return nil
}

func printUpgradeSummary(w io.Writer, o upgradeOptions, results []checkoutResult) {
	fmt.Fprintln(w, "summary:")
	for _, c := range results {
		where := c.root
		if where == "" {
			where = c.path
		}
		var parts []string
		for _, s := range c.steps {
			parts = append(parts, s.name+" "+string(s.state))
		}
		fmt.Fprintf(w, "  %s: %s\n", pluginwire.Printable(where), strings.Join(parts, ", "))
		if f := c.failure(); f != nil && f.name != "checkout" {
			fmt.Fprintf(w, "    once that is fixed, rerun: %s\n", o.again(c.root))
		}
	}
}

// upgradeExit is the worst exit code over every checkout: a failed step's
// own, or 1 for a stale one (only --check reports stale).
func upgradeExit(results []checkoutResult) error {
	code, failed, stale, checkouts := ExitOK, 0, 0, 0
	for _, c := range results {
		if !c.dup {
			checkouts++
		}
		if c.failure() != nil {
			failed++
		}
		for _, s := range c.steps {
			switch s.state {
			case stepFailed:
				code = max(code, s.code)
			case stepStale:
				stale++
				code = max(code, ExitUserError)
			}
		}
	}
	switch {
	case failed > 0:
		return &ExitError{Code: code, Err: fmt.Errorf("fugaro upgrade stopped in %d of %d checkout(s): see the summary", failed, checkouts)}
	case stale > 0:
		return &ExitError{Code: code, Err: errors.New("something is stale: fugaro upgrade without --check brings it up to date (see the summary)")}
	}
	return nil
}

// pinStep is fugaro update-skills for the checkout, with its rules (never a
// commit; a fork's marketplace moves only with --allow-fork; a file that is
// not valid JSON is never rewritten), and one more (U10): a pin newer than
// this fugaro is never moved down.
func pinStep(_ context.Context, u *upgradeCtx) stepResult {
	settings := u.loc.Settings
	shown := pluginwire.Printable(settings)
	if _, err := pluginwire.Tag(Version); err != nil {
		return stepResult{state: stepSkipped, reason: "a development build has no release tag to pin the plugin to"}
	}
	// U10 for every marketplace and enablement state: Status says Newer only
	// for an enabled dimipaun/fugaro pin, yet Plan rewrites a fork's or a
	// disabled plugin's ref just the same.
	if ref := pluginwire.Status(settings, Version, "").Ref; refAboveBinary(ref) {
		return stepResult{state: stepFailed, code: ExitUserError, reason: fmt.Sprintf("the plugin is pinned to %s, newer than this fugaro %s, and fugaro upgrade never moves a pin down: run brew upgrade dimipaun/tap/fugaro, then fugaro upgrade again",
			strings.TrimPrefix(ref, "v"), strings.TrimPrefix(Version, "v"))}
	}
	ch, err := pluginwire.Plan(settings, Version, u.o.allowFork)
	var fk *pluginwire.ForkError
	switch {
	case errors.As(err, &fk):
		warnUpgradeFork(u, pluginwire.Printable(fk.Repo))
		reason := "the marketplace is the fork " + pluginwire.Printable(fk.Repo) + ": pass --allow-fork if it is yours (only its ref moves)"
		if u.o.check {
			return stepResult{state: stepStale, reason: reason}
		}
		return stepResult{state: stepFailed, code: ExitUserError, reason: reason}
	case err != nil:
		fmt.Fprintf(u.w, "the file was not changed; fix it, or merge this in by hand:\n\n%s", pluginwire.Snippet(Version))
		return stepResult{state: stepFailed, code: ExitUserError, reason: oneLine(err.Error())}
	}
	if ch.Foreign != "" {
		warnUpgradeFork(u, ch.Foreign)
	}
	if ch.Note != "" {
		fmt.Fprintf(u.cmd.ErrOrStderr(), "note: %s\n", multiLine(ch.Note))
	}
	if !ch.Changed {
		return stepResult{state: stepCurrent, reason: "pinned to " + ch.Tag}
	}
	if u.o.check {
		return stepResult{state: stepStale, reason: "the pin moves to " + ch.Tag + " in " + shown}
	}
	fmt.Fprintf(u.w, "%s\n%s%s", shown, ch.Diff(), ch.NoticeText())
	if err := ch.Apply(); err != nil {
		return stepResult{state: stepFailed, code: ExitUserError, reason: oneLine(err.Error())}
	}
	return stepResult{state: stepDone, reason: "pinned to " + ch.Tag + " in " + shown + ": review it with git diff and commit it like any change"}
}

// checkPolicyCaveat is what --check says for the step it cannot answer: whether
// the published policy node is current is a fact of the project's Realtime
// Database and of nothing in the checkout, and reading it there is the cloud
// call the flag promises not to make (cloudStep's checkJobCopyCaveat is the
// same shape for the same reason).
const checkPolicyCaveat = "fugaro upgrade --check makes no cloud call, so it cannot tell whether the published policy node is current or an older Fugaro's; run fugaro upgrade (or fugaro update) without --check to migrate it in place, after the diff and the typed confirmation"

// policyStep is the last step of the sequence, and the same migration `fugaro
// update`'s policy stage makes for the checkout's project
// (init_policy_migrate.go), with the same diff, the same typed confirmation and
// the same refusals — the two front doors of one upgrade, so neither can carry
// a node the other would refuse. It is last because the cloud step's verdicts
// are read first: a real local-file error stops the checkout in every mode,
// --check included, and a checkout with nothing to refresh is skipped, neither
// of which a step of this one's may pre-empt.
//
// The step is skipped, not failed, where there is nothing here to migrate from:
// a teammate's checkout has no local project config, and a repository that is
// not onboarded has no node of this project's to read (resolveCloudRepo's
// cloudNotHere, the same verdict the cloud step uses). Under --check it is
// skipped before anything is read, like the cloud step's own caveat: the node
// lives in the project's Realtime Database, and reading it needs credentials.
func policyStep(ctx context.Context, u *upgradeCtx) stepResult {
	if u.o.check {
		fmt.Fprintf(u.w, "note: %s\n", checkPolicyCaveat)
		return stepResult{state: stepSkipped, reason: "--check makes no cloud call, so the published policy node is not read: run fugaro upgrade without --check to migrate it in place"}
	}
	if u.o.local {
		return stepResult{state: stepSkipped, reason: "--local"}
	}
	if u.agent != "" {
		reason := fmt.Sprintf("a coding agent's session (%s is set) never writes the published policy, by design; in your own terminal window, not through the agent, run %s", u.agent, u.o.cloudLine(u.loc.Root, false))
		if h := initflow.IDEHint(u.agent); h != "" {
			reason += " (" + h + ")"
		}
		return stepResult{state: stepSkipped, reason: reason}
	}
	_, lc, _, _, _, v := resolveCloudRepo(ctx, u.loc.Root, u.o.refresh.cloud)
	if v != nil {
		if v.state == cloudNotHere {
			return stepResult{state: stepSkipped, reason: v.reason}
		}
		return stepResult{state: stepFailed, code: ExitUserError, reason: v.reason}
	}
	if policyNodeUnreachable(lc) {
		return stepResult{state: stepSkipped, reason: policyNoDBReason}
	}
	m, published, err := policyMigrationFor(ctx, lc)
	var notOurs *policyMigrationNotOurs
	if errors.As(err, &notOurs) {
		return stepResult{state: stepSkipped, reason: oneLine(notOurs.reason)}
	}
	if err != nil {
		return stepResult{state: stepFailed, code: ExitUserError, reason: oneLine(err.Error())}
	}
	switch {
	case !published:
		return stepResult{state: stepCurrent, reason: policyNoneDetail()}
	case m == nil:
		return stepResult{state: stepCurrent, reason: fmt.Sprintf("the published policy is version %d", projectpolicy.Version)}
	}
	if err := policyMigrationConfirm(ctx, u.cmd, m, u.o.yes, u.w); err != nil {
		return stepResult{state: stepFailed, code: ExitUserError, reason: oneLine(err.Error())}
	}
	return stepResult{state: stepDone, reason: fmt.Sprintf("the published policy was migrated from version %d to %d in place", m.from, projectpolicy.Version)}
}

// checkRulesCaveat is what --check says for the rules step, for the same reason
// the policy step has one: which generation of the security rules a project's
// database has deployed is a fact of that database and of nothing in the
// checkout, and reading it there is the cloud call the flag promises not to
// make.
const checkRulesCaveat = "fugaro upgrade --check makes no cloud call, so it cannot tell which generation of the security rules the project's database has deployed; run fugaro upgrade (or fugaro update) without --check to deploy the generation this Fugaro reads a project policy under, after the plan and the typed confirmation"

// rulesStep deploys the security rules generation this binary carries, the
// deploy `fugaro update`'s rules stage makes and with its classification, its
// ownership gate and its document, so one installation converges through either
// front door. It stands in front of the policy step on purpose: the policy
// writer's gate (requirePolicyRules) refuses a migration the deployed rules
// would not admit, so without this step an installation whose rules predate the
// policy model met the gate and refused, leaving one `fugaro upgrade` unfinished
// and the owner with a second command — the rule of 2026-10-30 that the whole
// upgrade is one command.
//
// The step is a cloud write, so it is skipped before anything is read wherever
// the policy step is: --check, --local, and a coding agent's session, which
// hands it to the user's own terminal. It is skipped rather than failed where
// there is nothing here to deploy from (resolveCloudRepo's cloudNotHere), and
// where the deployed rules are not this binary's to replace: a newer or
// hand-edited marker, rules or a marker that could not be read, and the
// database the ownership predicate refuses, all say why and let the run go on.
func rulesStep(ctx context.Context, u *upgradeCtx) stepResult {
	if u.o.check {
		fmt.Fprintf(u.w, "note: %s\n", checkRulesCaveat)
		return stepResult{state: stepSkipped, reason: "--check makes no cloud call, so the deployed security rules are not read: run fugaro upgrade without --check to deploy this Fugaro's generation"}
	}
	if u.o.local {
		return stepResult{state: stepSkipped, reason: "--local"}
	}
	if u.agent != "" {
		reason := fmt.Sprintf("a coding agent's session (%s is set) never deploys the security rules, by design; in your own terminal window, not through the agent, run %s", u.agent, u.o.cloudLine(u.loc.Root, false))
		if h := initflow.IDEHint(u.agent); h != "" {
			reason += " (" + h + ")"
		}
		return stepResult{state: stepSkipped, reason: reason}
	}
	_, lc, _, _, _, v := resolveCloudRepo(ctx, u.loc.Root, u.o.refresh.cloud)
	if v != nil {
		if v.state == cloudNotHere {
			return stepResult{state: stepSkipped, reason: v.reason}
		}
		return stepResult{state: stepFailed, code: ExitUserError, reason: v.reason}
	}
	if detail := rulesUnavailable(lc); detail != "" {
		return stepResult{state: stepSkipped, reason: oneLine(detail)}
	}
	db, st, detail, err := rulesState(ctx, lc, "this step neither marks nor writes there", selfCommand()+" update")
	if err != nil {
		return stepResult{state: stepFailed, code: ExitUserError, reason: oneLine(err.Error())}
	}
	switch st {
	case initflow.Skipped:
		return stepResult{state: stepSkipped, reason: oneLine(detail)}
	case initflow.Done:
		return stepResult{state: stepCurrent, reason: oneLine(detail)}
	}
	// Plan asks the ownership predicate again (infra.DB.Plan's own Check), after
	// rulesState asked it: the first answer is the verdict this step reports,
	// the second is the one the write itself is gated on, and the step passes
	// nothing down to it. Asking twice is deliberate — a database marked, or
	// emptied, between the two reads is refused rather than written into, which
	// is what the ruling's "one shared ownership check, asked by every writer
	// into the database" means here — and the update door's rules stage asks it
	// in the same two places.
	acts, warnings, err := db.Plan(ctx)
	if err != nil {
		return stepResult{state: stepFailed, code: ExitUserError, reason: oneLine(err.Error())}
	}
	for _, warning := range warnings {
		fmt.Fprintf(u.w, "note: %s\n", oneLine(warning))
	}
	if len(acts) == 0 {
		// Nothing would be written, so nothing is claimed: the sentence is the
		// classification's own, which is doctor's answer to the same question.
		return stepResult{state: stepCurrent, reason: oneLine(detail)}
	}
	rulesShow(u.w, lc.Budget.FirebaseProject, acts)
	if err := confirmRemoval(u.cmd, lc, u.o.yes, "this deploys the security rules listed, as you (project owner, editor or budget admin), and records which generation is deployed: they are what every project policy is read under, and a project policy written under rules that predate it is a policy nothing reads"); err != nil {
		return stepResult{state: stepFailed, code: ExitUserError, reason: oneLine(err.Error())}
	}
	if err := db.Apply(ctx); err != nil {
		return stepResult{state: stepFailed, code: ExitUserError, reason: oneLine(err.Error())}
	}
	return stepResult{state: stepDone, reason: fmt.Sprintf("deployed the security rules (generation %d) and recorded it", projectpolicy.RulesVersion)}
}

// warnUpgradeFork is update-skills' warning, said whenever the checkout's
// marketplace is a fork, whether or not --allow-fork lets its ref move.
func warnUpgradeFork(u *upgradeCtx, repo string) {
	fmt.Fprintf(u.cmd.ErrOrStderr(), "WARNING: the %q marketplace in %s is %s, not %s. Its skills are not Fugaro's; Claude Code can install that plugin for anyone who trusts the folder. Check it is yours.\n",
		pluginwire.Marketplace, pluginwire.Printable(u.loc.Settings), repo, pluginwire.Repo)
}

// refAboveBinary: ref is a release tag vX.Y.Z newer than this binary's.
func refAboveBinary(ref string) bool {
	bin, err := pluginwire.Tag(Version)
	if err != nil || !pluginwire.ValidTag(ref) {
		return false
	}
	a, b := tagNumbers(ref), tagNumbers(bin)
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

// tagNumbers splits a tag that pluginwire.ValidTag accepts.
func tagNumbers(tag string) [3]int {
	var n [3]int
	for i, p := range strings.SplitN(strings.TrimPrefix(tag, "v"), ".", 3) {
		n[i], _ = strconv.Atoi(p)
	}
	return n
}
