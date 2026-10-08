package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// cloudState is the cloud step's state as read from local files only.
type cloudState int

const (
	cloudCurrent cloudState = iota // every kind's base_images entry is this release's base, or a newer copy
	cloudStale                     // a kind's entry is missing or an older release's
	cloudBlocked                   // a custom base: the refresh would stop with its one-line fix
	cloudNotHere                   // by U9 only: no fugaro.yaml, a teammate checkout (no local config), not onboarded, a development build
	cloudFailed                    // anything else that stops the refresh (a bad fugaro.yaml or origin, an unreadable config, a registry or base error): a failure, not a skip
)

type cloudVerdict struct {
	state  cloudState
	repo   string
	reason string // one line, safe to print
}

// cloudCheck is the cloud step's state from fugaro.yaml and the local
// project config alone: never a bucket, the registry or a job, and no
// credentials (fugaro upgrade --check, and the skips of the cloud step). It
// cannot see a build that failed after the base moved: build records live in
// the bucket. It prints nothing; rerun is the command line a blocked reason
// tells the user to run again ("" is the bare fugaro upgrade).
func cloudCheck(ctx context.Context, root string, co cloudOptions, rerun string) cloudVerdict {
	if rerun == "" {
		rerun = selfCommand() + " upgrade"
	}
	co.stderr = func() io.Writer { return io.Discard } // the caller prints the per-checkout line
	failed := func(repo string, err error) cloudVerdict {
		return cloudVerdict{state: cloudFailed, repo: repo, reason: oneLine(err.Error())}
	}
	ver := releaseVersion()
	if ver == "" {
		return cloudVerdict{state: cloudNotHere, reason: "a development build has no release base image to move to"}
	}
	if _, err := os.Stat(filepath.Join(root, "fugaro.yaml")); errors.Is(err, os.ErrNotExist) {
		return cloudVerdict{state: cloudNotHere, reason: "no fugaro.yaml yet, so no job image to refresh"}
	}
	_, cfg, err := loadCheckoutConfigAt(ctx, root)
	if err != nil {
		return failed("", err)
	}
	repo, err := checkoutRepo(ctx, root)
	if err != nil {
		return failed("", err)
	}
	lc, lcPath, _, err := loadRepoConfig(ctx, &initOptions{cloud: co}, root)
	if err != nil {
		var se *localcfg.SelectError
		if checkoutNamesProject(ctx, root) && (errors.As(err, &se) || errors.Is(err, localcfg.ErrMissing)) {
			return cloudVerdict{state: cloudNotHere, repo: repo, reason: "no local project config selects this checkout (" + oneLine(err.Error()) + "): the cloud step is for the operator who set the project up"}
		}
		return failed(repo, err)
	}
	oi, ok := readOrigin(ctx, root)
	if !ok {
		return failed(repo, errors.New("cannot read the checkout's origin"))
	}
	if !repoKnown(lc, oi) {
		return cloudVerdict{state: cloudNotHere, repo: repo, reason: fmt.Sprintf("%s is not onboarded to project %s, so it has no job image yet", pluginwire.Printable(repo), lc.Name)}
	}
	host, err := infra.RegistryHost(lc)
	if err != nil {
		return failed(repo, err)
	}
	set := map[string]bool{}
	for _, w := range cfg.Workflows {
		set[w.Base] = true
	}
	var custom, stale []string
	for _, k := range slices.Sorted(maps.Keys(set)) {
		cur := lc.BaseImage(k)
		if cur != "" && !isManaged(cur, host, k) {
			custom = append(custom, k)
			continue
		}
		want, err := refreshBase(lc, k, ver)
		if err != nil {
			return failed(repo, err)
		}
		if cur != want {
			was := "none recorded"
			if cur != "" {
				was = pluginwire.Printable(cur)
			}
			stale = append(stale, fmt.Sprintf("%s: %s, this release's is %s", k, was, want))
		}
	}
	switch {
	case len(custom) > 0:
		return cloudVerdict{state: cloudBlocked, repo: repo, reason: oneLine(customBaseRefusal(lc, lcPath, custom, rerun).Error())}
	case len(stale) > 0:
		return cloudVerdict{state: cloudStale, repo: repo, reason: "base images to move: " + strings.Join(stale, "; ")}
	}
	return cloudVerdict{state: cloudCurrent, repo: repo, reason: "the local config records this release's base image for every workflow's kind (--check reads no build record: a build that failed after the base moved shows only when fugaro upgrade runs the cloud step)"}
}

// checkoutNamesProject: root's fugaro.yaml gives a valid project name, so a
// selection that fails for want of a local config is the teammate's case
// (U9), not a broken fugaro.yaml.
func checkoutNamesProject(ctx context.Context, root string) bool {
	co, err := checkoutProject(ctx, root)
	return err == nil && co != nil && config.ProjectNameRE.MatchString(co.Project)
}

// cloudStep is fugaro image refresh for the checkout's repository, in this
// process (runImageRefresh, decision U16). --check reports cloudCheck's state
// without running the refresh. Under --local and in a coding agent's session
// (U7) the step is skipped before anything is read, so nothing there can
// fail it. Otherwise (a real run, or --check) a real local-file error
// (cloudFailed) stops the checkout: a failure, not a skip; where there is
// nothing to refresh from here (cloudNotHere, U9) the step is skipped.
func cloudStep(ctx context.Context, u *upgradeCtx) stepResult {
	verdict := func() (cloudVerdict, *stepResult) {
		v := cloudCheck(ctx, u.loc.Root, u.o.refresh.cloud, u.o.again(u.loc.Root))
		if v.state == cloudFailed {
			return v, &stepResult{state: stepFailed, code: ExitUserError, reason: v.reason}
		}
		return v, nil
	}
	if u.o.check {
		v, failed := verdict()
		if failed != nil {
			return *failed
		}
		switch v.state {
		case cloudCurrent:
			return stepResult{state: stepCurrent, reason: v.reason}
		case cloudStale, cloudBlocked:
			return stepResult{state: stepStale, reason: v.reason}
		}
		return stepResult{state: stepSkipped, reason: v.reason}
	}
	if u.o.local {
		return stepResult{state: stepSkipped, reason: "--local; for the cloud step, run in your own terminal window: " + u.o.cloudLine(u.loc.Root, false)}
	}
	if u.agent != "" {
		reason := fmt.Sprintf("a coding agent's session (%s is set) never applies cloud changes, by design; in your own terminal window, not through the agent, run %s (it confirms every step itself, each billable build included), or %s to be asked at each step",
			u.agent, u.o.cloudLine(u.loc.Root, true), u.o.cloudLine(u.loc.Root, false))
		if h := initflow.IDEHint(u.agent); h != "" {
			reason += " (" + h + ")"
		}
		return stepResult{state: stepSkipped, reason: reason}
	}
	v, failed := verdict()
	if failed != nil {
		return *failed
	}
	if v.state == cloudNotHere {
		return stepResult{state: stepSkipped, reason: v.reason}
	}
	ro := u.o.refresh
	ro.dir, ro.yes, ro.rerun = u.loc.Root, u.o.yes, u.o.again(u.loc.Root)
	var plan *refreshPlan
	ro.onPlan = func(p *refreshPlan) { plan = p }
	if err := runImageRefresh(u.cmd, ro); err != nil {
		return stepResult{state: stepFailed, code: ExitCode(err), reason: oneLine(err.Error())}
	}
	if plan != nil && plan.current() {
		return stepResult{state: stepCurrent, reason: "no base image to copy and no image to rebuild"}
	}
	return stepResult{state: stepDone, reason: "the repository is on this release's base image"}
}

// cloudLine is the command that runs the cloud step for root in the user's
// own terminal: this run's flags without --check and --local, with or
// without --yes.
func (o upgradeOptions) cloudLine(root string, yes bool) string {
	c := o
	c.check, c.local, c.yes = false, false, yes
	return c.again(root)
}
