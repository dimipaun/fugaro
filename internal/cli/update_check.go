package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// update_check.go is `fugaro update --check`, and the step vocabulary the
// checkout's own writes are described in.
//
// `fugaro upgrade` was a second front door onto one upgrade, and U4 of
// docs/plugin-user-scope/implementation-plan.md deletes it (D950: not even a
// tombstone): the name is cobra's unknown command, and what it had that
// `fugaro update` did not is `--check` (D948) — read-only, no claude call, no
// cloud call, printing what each of the checkout's four steps would do and
// exiting 1 when anything the local files can see is stale. It replaced
// `--plan-only` for the job the skills need, because a stage's plan may read
// the cloud (the images stage reads the bucket) and this mode promises it does
// not (spec §5).
//
// The steps are the converge's own, and each names the stage that runs it
// (init_stages.go): plugin is the plugin stage's, cloud is the checkout
// stage's, and rules and policy are the two whose answer lives in the user's
// project — the deployed security rules generation and the published policy
// node — and is therefore skipped under --check with the note saying so
// (rulesCheck, policyCheck). The step vocabulary (stepState, stepResult,
// stepCtx) is the stages' too: the checkout stage prints a step with printStep
// and reports a step's failure with the step's own reason.

// stepOptions are the door's flags a step sees: the two modes, and the cloud
// step's own, which are its flags on both doors (the checkout stage's
// refreshOptions are built from them, init_checkout_upgrade.go).
type stepOptions struct {
	// check is the read-only mode: every step answers and nothing is written.
	check bool
	// yes is --yes, passed on to the cloud step's refresh.
	yes bool
	// refresh holds the cloud step's flags, passed on to runImageRefresh.
	refresh refreshOptions
}

// stepState is a step's outcome. The words are the output's.
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
	name   string // set by the caller that knows which step it ran
	state  stepState
	reason string // one line, safe to print
	code   int    // a failed step's exit code
}

// stepCtx is what a step sees of one checkout.
type stepCtx struct {
	cmd   *cobra.Command
	w     io.Writer
	o     stepOptions
	loc   pluginwire.Location // the checkout's top and its settings file
	agent string              // the coding agent's marker, "" outside one

	// detail is where a step's prose goes: the log always, the terminal only
	// under -v, and w itself where the run has no log to write (detailOut). A
	// step's own line, the confirmations it asks and everything a person has
	// to read stay on w.
	detail io.Writer
}

// detailOut is u.detail with the fallback of the run that has no log: a step
// built outside the converge (a helper of a test, `fugaro update --check`,
// update-skills) writes its prose where it writes everything else.
func (u *stepCtx) detailOut() io.Writer {
	if u.detail == nil {
		return u.w
	}
	return u.detail
}

type checkStep struct {
	name string
	run  func(ctx context.Context, u *stepCtx) stepResult
}

// checkSteps are the four questions `fugaro update --check` answers, in the
// order the converge's stages run them: the plugin first (the plugin stage),
// then the checkout's cloud half, then the two facts of the user's project.
var checkSteps = []checkStep{{"plugin", pluginStep}, {"cloud", cloudStep}, {"rules", rulesCheck}, {"policy", policyCheck}}

// updateCheckReads are the flags `fugaro update --check` reads: the cloud
// step's own, which its read of the local files answers. Every other flag of
// the door belongs to the converge --check does not run, and a run that passed
// one would have it ignored rather than honoured — so it is refused by name
// before the check reads anything (refuseConvergeFlags).
var updateCheckReads = map[string]bool{
	"check": true, "config": true, "project": true, "gcp-project": true, "region": true,
}

// refuseConvergeFlags is the flag half of the mode: --check runs none of the
// converge, so the door's own flags are refused rather than silently dropped.
// The checkout is the argument (`fugaro update --check [PATH]`), which is why
// --repo is on the refused side: it says the run onboards the repository of the
// checkout at PATH, and this run onboards nothing.
func refuseConvergeFlags(cmd *cobra.Command) error {
	inherited := map[string]bool{}
	cmd.InheritedFlags().VisitAll(func(f *pflag.Flag) { inherited[f.Name] = true })
	var refused string
	// VisitAll walks the flags in name order, so the flag the refusal names is
	// the same one whatever a script passed beside it.
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if refused != "" || !f.Changed || inherited[f.Name] || updateCheckReads[f.Name] {
			return
		}
		refused = f.Name
	})
	if refused == "" {
		return nil
	}
	return userErr("--%s is the converge's, and --check runs none of it: fugaro update --check reads the checkout's own files, prints what each of its steps would do and writes nothing. Pass --check alone (with the checkout as PATH, and the cloud step's own flags if the deployment is set apart), or drop --check to run the converge", refused)
}

// runUpdateCheck is the whole of the mode: one checkout, its four steps in
// check mode, and the exit code their verdicts make.
func runUpdateCheck(cmd *cobra.Command, o *initOptions, args []string) error {
	if err := refuseConvergeFlags(cmd); err != nil {
		return err
	}
	ctx := cmd.Context()
	w := cmd.OutOrStdout()
	path := "."
	if len(args) > 0 {
		path = args[0]
	}
	fmt.Fprintln(w, updateCheckHeader())
	loc, ok := pluginwire.Locate(path)
	if !ok {
		fmt.Fprintf(w, "== %s\n", pluginwire.Printable(path))
		return checkRefusal(w, "not in a git checkout (or it is your home directory)")
	}
	fmt.Fprintf(w, "== %s\n", pluginwire.Printable(loc.Root))
	if !fugaroCheckout(loc) {
		return checkRefusal(w, "not a Fugaro checkout: it has no fugaro.yaml (set it up with fugaro setup and /fugaro:setup first)")
	}
	u := &stepCtx{cmd: cmd, w: w, o: stepOptions{check: true, refresh: refreshOptions{
		imageSource: o.imageSource, expectDigests: o.expectDigests, cloud: o.cloud,
		in: cmd.InOrStdin(), out: w}}, loc: loc,
		agent: agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin()))}
	// U11: a development build has no release to install or refresh to, so
	// every step is skipped and the run exits 0.
	if releaseVersion() == "" {
		for _, s := range checkSteps {
			printStep(w, stepResult{name: s.name, state: stepSkipped, reason: "a development build has no release to install or refresh to"})
		}
		return nil
	}
	var results []stepResult
	code, stale, failed := ExitOK, false, false
	for i, s := range checkSteps {
		r := s.run(ctx, u)
		r.name = s.name
		results = append(results, r)
		printStep(w, r)
		if r.state == stepFailed {
			failed = true
			code = max(code, r.code)
			for _, rest := range checkSteps[i+1:] {
				results = append(results, stepResult{name: rest.name, state: stepNotRun})
			}
			break
		}
		if r.state == stepStale {
			stale = true
			code = max(code, ExitUserError)
		}
	}
	switch {
	case failed:
		return &ExitError{Code: code, Err: fmt.Errorf("fugaro update --check stopped at the step named above: nothing was written (%s)", pluginwire.Printable(loc.Root))}
	case stale:
		// The line that does what the check only reports: the same door and
		// the same flags with --check dropped, run at the user's own terminal
		// window, where the converge's confirmations can be answered
		// (cloudLine is that line, and this is what it is for besides the
		// cloud step's own refusals).
		fmt.Fprintf(w, "stale: run %s in your own terminal window to bring %s up to date\n", u.o.cloudLine(loc.Root, false), pluginwire.Printable(loc.Root))
		return &ExitError{Code: code, Err: errors.New("something is stale, and nothing was written: the steps above say what")}
	case allCurrentOrSkipped(results):
		fmt.Fprintf(w, "nothing to do: %s is current", pluginwire.Printable(loc.Root))
		if anySkipped(results) {
			fmt.Fprint(w, " (apart from what the skipped steps name)")
		}
		fmt.Fprintln(w)
	}
	return nil
}

// checkRefusal is the run that never reaches a step: the mode's own line, and
// the user error a script can act on.
func checkRefusal(w io.Writer, reason string) error {
	printStep(w, stepResult{name: "checkout", state: stepFailed, reason: reason, code: ExitUserError})
	return &ExitError{Code: ExitUserError, Err: errors.New("fugaro update --check: " + reason)}
}

func allCurrentOrSkipped(results []stepResult) bool {
	if len(results) == 0 {
		return false
	}
	for _, r := range results {
		if r.state != stepCurrent && r.state != stepSkipped {
			return false
		}
	}
	return true
}

func anySkipped(results []stepResult) bool {
	for _, r := range results {
		if r.state == stepSkipped {
			return true
		}
	}
	return false
}

// updateCheckHeader is the run's first line: what this mode is, which steps it
// is about to answer, and the one command that brings up what it names. It
// never upgrades fugaro itself.
func updateCheckHeader() string {
	if releaseVersion() == "" {
		return fmt.Sprintf("fugaro update --check with a development build (%s): there is no release to install or refresh to, so every step is skipped", oneLine(Version))
	}
	return fmt.Sprintf("fugaro update --check with fugaro %s: it writes nothing, runs no claude command and makes no cloud call, and it never upgrades fugaro itself: brew upgrade dimipaun/tap/fugaro && fugaro update is the whole sequence", Version)
}

// checkPolicyCaveat is what --check says for the step it cannot answer: whether
// the published policy node is current is a fact of the project's Realtime
// Database and of nothing in the checkout, and reading it there is the cloud
// call the flag promises not to make (checkJobCopyCaveat is the same shape for
// the same reason, in the cloud step).
const checkPolicyCaveat = "fugaro update --check makes no cloud call, so it cannot tell whether the published policy node is current or an older Fugaro's; run fugaro update without --check to migrate it in place, after the diff and the confirmation"

// policyCheck is the policy step's half of --check: the node is not a thing in
// the checkout, so the step says it cannot tell and names the run that can. The
// migrate itself is the converge's policy stage (init_policy_migrate.go): one
// installation converges through one writer of the node, which is U4's whole
// point — `fugaro upgrade` was the second one.
func policyCheck(_ context.Context, u *stepCtx) stepResult {
	fmt.Fprintf(u.w, "note: %s\n", checkPolicyCaveat)
	return stepResult{state: stepSkipped, reason: "--check makes no cloud call, so the published policy node is not read: run fugaro update without --check to migrate it in place"}
}

// checkRulesCaveat is what --check says for the rules step, for the same reason
// the policy step has one: which generation of the security rules a project's
// database has deployed is a fact of that database and of nothing in the
// checkout, and reading it there is the cloud call the flag promises not to
// make.
const checkRulesCaveat = "fugaro update --check makes no cloud call, so it cannot tell which generation of the security rules the project's database has deployed; run fugaro update without --check to deploy the generation this Fugaro reads a project policy under, after the plan"

// rulesCheck is the rules step's half of --check, in the same shape: the
// deployed generation is not a thing in the checkout. The deploy itself is the
// converge's rules stage (init_rules_stage.go), which stands in front of the
// policy stage for the reason it always did — the policy writer's gate refuses
// a migration the deployed rules would not admit.
func rulesCheck(_ context.Context, u *stepCtx) stepResult {
	fmt.Fprintf(u.w, "note: %s\n", checkRulesCaveat)
	return stepResult{state: stepSkipped, reason: "--check makes no cloud call, so the deployed security rules are not read: run fugaro update without --check to deploy this Fugaro's generation"}
}

// fugaroCheckout: a checkout Fugaro is set up in. It has a fugaro.yaml, and
// that is the whole of it — a repository that never opted in has neither file
// and is one Fugaro is not set up in, and a .claude/settings.json whose
// marketplace an older fugaro wired is a leftover of a pin this tree no longer
// writes (D940) and not a second way of being one (U1 of
// docs/plugin-user-scope/implementation-plan.md, reconciliation.md F15).
func fugaroCheckout(loc pluginwire.Location) bool {
	_, err := os.Lstat(filepath.Join(loc.Root, "fugaro.yaml"))
	return err == nil
}

// printStep is one step's line, in the check and in the checkout stage's apply:
// the step's name, its state, and the one line saying why.
func printStep(w io.Writer, r stepResult) {
	if r.reason == "" {
		fmt.Fprintf(w, "%s: %s\n", r.name, r.state)
		return
	}
	fmt.Fprintf(w, "%s: %s: %s\n", r.name, r.state, r.reason)
}

// again is the command line that runs this door for path with the flags that
// name this checkout: the checkout the user is in (--repo, which is what names
// a checkout on this door) and, for a check, the checkout as the argument
// --check takes.
// It is what a step's own refusal and the cloud step's blocked reasons name, so
// the two lines cannot drift from the flags they carry.
//
// The cloud step's flags that a deployment is named with (--project,
// --gcp-project, --region, --config) are kept when they were set, and the
// installation's own are not: --image-source and --expect-digest set up the
// installation rather than a repository, so this door refuses them beside
// --repo (checkRepo, init.go) and beside --check (updateCheckReads). Naming one
// here would print a line that is refused before anything runs.
func (o stepOptions) again(path string) string {
	args := []string{selfCommand(), "update"}
	if o.check {
		args = append(args, "--check")
	}
	for _, f := range []struct{ name, v string }{
		{"config", o.refresh.cloud.config}, {"project", o.refresh.cloud.project},
		{"gcp-project", o.refresh.cloud.gcpProject}, {"region", o.refresh.cloud.region},
	} {
		if f.v != "" {
			args = append(args, "--"+f.name, quoteWord(f.v))
		}
	}
	if !o.check {
		args = append(args, "--repo")
	}
	if strings.HasPrefix(path, "-") {
		args = append(args, "--")
	}
	return strings.Join(append(args, quoteWord(path)), " ")
}
