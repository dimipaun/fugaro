package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// fugaro upgrade (docs/design/upgrade.md): each checkout brought up to this
// binary in three steps, pin, plugin and cloud, stopping that checkout at
// the first step that fails; every checkout is tried, one summary ends the
// run, and the exit code is the worst.

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

// stepNotImplemented is a placeholder step's state: the step does not exist
// in this build, so nothing was checked or changed. It is never current and
// never makes a checkout "nothing to do"; it does not change the exit code,
// and the run ends with a "not checked:" line naming those steps. It is not
// in stepStates: Tasks 6 and 7 of docs/plans/2026-10-08-upgrade.md replace
// the placeholders with pluginStep and cloudStep and remove it.
const stepNotImplemented stepState = "not implemented"

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

// upgradeSteps run in this order for each checkout. plugin and cloud are
// placeholders until Tasks 6 and 7 put pluginStep and cloudStep in their
// places.
var upgradeSteps = []upgradeStep{{"pin", pinStep}, {"plugin", notImplementedStep}, {"cloud", notImplementedStep}}

// notImplementedStep stands for a step this build does not have.
func notImplementedStep(context.Context, *upgradeCtx) stepResult {
	return stepResult{state: stepNotImplemented, reason: "skipped in this build, nothing was checked or changed"}
}

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
processed one after the other, each on its own) up to this fugaro, in three
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
          config (a teammate), a repository not onboarded.

It never upgrades fugaro itself: brew upgrade dimipaun/tap/fugaro && fugaro
upgrade is the whole sequence.

This build has the pin step only: plugin and cloud print "not implemented",
check and change nothing, and the run ends with a "not checked:" line naming
them; they do not change the exit code.

--local runs pin and plugin only. In a coding agent's session (CLAUDECODE and
the like) the cloud step is always skipped, never attempted, with the
command to run in your own terminal, and the exit code is 0 when the local
steps succeeded. --check writes nothing, runs no claude command and makes
no cloud call: it reads the settings file, Claude Code's record of installed
plugins and the local project config, prints what each step would do and
exits 1 when anything is stale (build records are not read). Without --yes,
--local or --check it needs a real terminal. There is no --json.

Exit codes: 0 every checkout finished or had nothing to do; 1 a refusal, a
failed local step or (with --check) something stale; 2 a cloud failure. The
summary names each checkout's steps and, for one that stopped, the line to
rerun.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runUpgrade(cmd, o, args) },
	}
	f := cmd.Flags()
	f.BoolVar(&o.check, "check", false, "write nothing, run no claude command, make no cloud call: print what each step would do and exit 1 when anything is stale")
	f.BoolVar(&o.local, "local", false, "run the pin and plugin steps only (the only mode that does everything it can inside a coding agent's session)")
	f.BoolVar(&o.yes, "yes", false, "confirm every step of the cloud step without asking (the base copy, the check-job update and each billable build, with no cap), and without a terminal; never in a coding agent's session")
	f.BoolVar(&o.allowFork, "allow-fork", false, "move the ref of a marketplace repository other than dimipaun/fugaro (a fork you host) to this release's tag, and add it to Claude Code")
	f.StringVar(&o.refresh.repo, "repo", "", "owner/name of the repository; it must be the checkout's origin (one PATH only)")
	f.StringArrayVar(&o.refresh.workflows, "workflow", nil, "a workflow to refresh (repeatable; default: every workflow in fugaro.yaml; one PATH only)")
	f.StringVar(&o.refresh.imageSource, "image-source", "", "the registry and owner the release base images are copied from (default ghcr.io/dimipaun)")
	f.StringArrayVar(&o.refresh.expectDigests, "expect-digest", nil, "pin the digest of a base image copied, KIND=sha256:<hex> (repeatable; one PATH only)")
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
	if !o.check && !o.local && !o.yes && agent == "" && !stdinIsTerminal(cmd.InOrStdin()) {
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
		return userErr("--yes confirms the cloud step, which --local skips: pass one or the other")
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
		return refuse(stepFailed, "not a Fugaro checkout: it has no fugaro.yaml and its .claude/settings.json does not wire the Fugaro plugin (set it up with fugaro init and /fugaro:setup first)")
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
	if missing := res.notImplemented(); len(missing) > 0 {
		if res.failure() == nil {
			fmt.Fprintf(w, "not checked: %s (not implemented in this build)\n", strings.Join(missing, ", "))
		}
	} else if res.nothingToDo() {
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

// notImplemented names the checkout's placeholder steps.
func (c checkoutResult) notImplemented() []string {
	var out []string
	for _, s := range c.steps {
		if s.state == stepNotImplemented {
			out = append(out, s.name)
		}
	}
	return out
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
	var missing []string
	for _, s := range upgradeSteps {
		for _, c := range results {
			if slices.Contains(c.notImplemented(), s.name) {
				missing = append(missing, s.name)
				break
			}
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(w, "not checked: %s: this build does not implement them, so this run says nothing about them and the exit code does not count them\n", strings.Join(missing, ", "))
	}
}

// upgradeExit is the worst exit code over every checkout: a failed step's
// own, or 1 for a stale one (only --check reports stale). A step that is not
// implemented counts for nothing: the summary's "not checked:" line names it.
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
