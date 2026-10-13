package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/pricing"
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

// resolveCloudRepo is the local-only prefix cloudCheck and jobConfigCopyNote
// share: root's fugaro.yaml and the local project config, resolved without a
// bucket, the registry or a job, and no credentials. v is non-nil when
// resolution itself is the answer (cloudFailed, or one of U9's cloudNotHere
// skips: a development build, no fugaro.yaml, a teammate checkout, a
// repository not onboarded); the other return values are only valid when v
// is nil.
//
// loadCheckoutResolved also resolves the checkout's project layer
// (layered-config.md): Offline: true keeps that resolution to the local
// cache (decision L13), never the bucket itself, so an anchored checkout
// with no cached layer leaves it unknown rather than reading one for real;
// the project layer's own values, when cached, still apply normally.
func resolveCloudRepo(ctx context.Context, root string, co cloudOptions) (cfg *config.Config, lc *localcfg.Config, lcPath, repo, host string, v *cloudVerdict) {
	co.stderr = func() io.Writer { return io.Discard } // the caller prints the per-checkout line
	failed := func(repo string, err error) *cloudVerdict {
		return &cloudVerdict{state: cloudFailed, repo: repo, reason: oneLine(err.Error())}
	}
	if releaseVersion() == "" {
		return nil, nil, "", "", "", &cloudVerdict{state: cloudNotHere, reason: "a development build has no release base image to move to"}
	}
	if _, err := os.Stat(filepath.Join(root, "fugaro.yaml")); errors.Is(err, os.ErrNotExist) {
		return nil, nil, "", "", "", &cloudVerdict{state: cloudNotHere, reason: "no fugaro.yaml yet, so no job image to refresh"}
	}
	_, rf, err := loadCheckoutResolved(ctx, root, nil, layerOptions{Lenient: true, Offline: true})
	if err != nil {
		return nil, nil, "", "", "", failed("", err)
	}
	cfg = rf.Cfg
	repo, err = checkoutRepo(ctx, root)
	if err != nil {
		return nil, nil, "", "", "", failed("", err)
	}
	lc, lcPath, _, err = loadRepoConfig(ctx, &initOptions{cloud: co}, root)
	if err != nil {
		var se *localcfg.SelectError
		if checkoutNamesProject(ctx, root) && (errors.As(err, &se) || errors.Is(err, localcfg.ErrMissing)) {
			return nil, nil, "", repo, "", &cloudVerdict{state: cloudNotHere, repo: repo, reason: "no local project config selects this checkout (" + oneLine(err.Error()) + "): the cloud step is for the operator who set the project up"}
		}
		return nil, nil, "", "", "", failed(repo, err)
	}
	oi, ok := readOrigin(ctx, root)
	if !ok {
		return nil, nil, "", "", "", failed(repo, errors.New("cannot read the checkout's origin"))
	}
	if !repoKnown(lc, oi) {
		return nil, nil, "", repo, "", &cloudVerdict{state: cloudNotHere, repo: repo, reason: fmt.Sprintf("%s is not onboarded to project %s, so it has no job image yet", pluginwire.Printable(repo), lc.Name)}
	}
	host, err = infra.RegistryHost(lc)
	if err != nil {
		return nil, nil, "", "", "", failed(repo, err)
	}
	return cfg, lc, lcPath, repo, host, nil
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
	cfg, lc, lcPath, repo, host, v := resolveCloudRepo(ctx, root, co)
	if v != nil {
		return *v
	}
	ver := releaseVersion()
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
			return cloudVerdict{state: cloudFailed, repo: repo, reason: oneLine(err.Error())}
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

// checkJobCopyCaveat is --check's note on the job's config copy (jobConfigCopyNote):
// --check makes no cloud call, so, unlike a real cloud step, it can never
// tell whether the deployed job's copy is stale (docs/multi-model.md "Then
// run fugaro init --repo").
const checkJobCopyCaveat = "fugaro upgrade --check makes no cloud call, so it cannot tell whether the deployed job still carries this local project config's providers and model_prices, or an older copy; run fugaro upgrade without --check to find out, or just run fugaro init --repo . in your own terminal to refresh it (never run automatically)"

// jobConfigCopyNote is the cloud step's extra report, after a real refresh:
// whether the deployed workflow job's copy of the local project config's
// providers and model_prices (FUGARO_MODEL_PROVIDERS, FUGARO_MODEL_PRICES;
// docs/multi-model.md, "Then run fugaro init --repo") still matches what
// this local config would write now. A version upgrade never refreshes that
// copy; only fugaro init --repo does, and fugaro upgrade never runs it
// itself (its Terraform confirmations stay the operator's).
//
// "" is nothing to say: the copies agree, or the job can't be read here (no
// workflow, no job deployed yet, or anything resolveCloudRepo already
// treats as a skip: no fugaro.yaml, a teammate checkout, a development
// build, a repository not onboarded), in which case the note is silent,
// exactly as the cloud step itself is in those cases.
func jobConfigCopyNote(ctx context.Context, root string, co cloudOptions) string {
	cfg, lc, _, repo, _, v := resolveCloudRepo(ctx, root, co)
	if v != nil {
		return ""
	}
	names := slices.Sorted(maps.Keys(cfg.Workflows))
	if len(names) == 0 {
		return ""
	}
	repoURL, err := checkoutURL(ctx, root)
	if err != nil {
		return ""
	}
	spec, err := infra.Workflow(infra.Inputs{LC: lc, Repo: repo, Cfg: cfg, RepoURL: repoURL}, names[0])
	if err != nil {
		return ""
	}
	env, err := openCloudFor(ctx, lc)
	if err != nil {
		return ""
	}
	defer env.Close()
	be, ok := env.be.(interface {
		JobEnv(ctx context.Context, job, name string) (string, bool, error)
	})
	if !ok {
		return ""
	}
	jobProviders, exists, err := be.JobEnv(ctx, spec.Job, infra.ModelProvidersEnv)
	if err != nil || !exists {
		return "" // no job deployed yet here
	}
	jobPrices, _, err := be.JobEnv(ctx, spec.Job, infra.ModelPricesEnv)
	if err != nil {
		return ""
	}
	var diffs []string
	if d := providersDiff(jobProviders, spec.Env[infra.ModelProvidersEnv]); d != "" {
		diffs = append(diffs, "providers: "+d)
	}
	if d := pricesDiff(jobPrices, spec.Env[infra.ModelPricesEnv]); d != "" {
		diffs = append(diffs, "model_prices: "+d)
	}
	if len(diffs) == 0 {
		return ""
	}
	return fmt.Sprintf("the deployed job's copy of the local project config is STALE (%s): a version upgrade never refreshes it; runs on %s use the old copy until you run fugaro init --repo . in your own terminal (never run automatically)",
		strings.Join(diffs, "; "), repo)
}

// providersDiff and pricesDiff name the entries that differ between the
// job's copy of FUGARO_MODEL_PROVIDERS/FUGARO_MODEL_PRICES (job) and what
// this local config would write now (now): added, changed or removed, by
// provider name or model ID alone. Never a value: a provider entry names no
// secret (only the secret's name), but neither function prints any field at
// all, so nothing it carries (prices included) can leak by accident. A side
// that fails to parse (an old or hand-edited copy) is "unreadable" unless
// the two raw strings are byte-identical, since a real difference could not
// be named by field then either.
func providersDiff(job, now string) string {
	jm, jerr := decodeProviders(job)
	nm, nerr := decodeProviders(now)
	if jerr != nil || nerr != nil {
		if job == now {
			return ""
		}
		return "unreadable copy"
	}
	return mapDiff(jm, nm, func(a, b config.ModelProvider) bool { return reflect.DeepEqual(a, b) })
}

func decodeProviders(s string) (map[string]config.ModelProvider, error) {
	m := map[string]config.ModelProvider{}
	if s == "" {
		return m, nil
	}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	return m, nil
}

func pricesDiff(job, now string) string {
	jo, jerr := pricing.ParseOverrides(job)
	no, nerr := pricing.ParseOverrides(now)
	if jerr != nil || nerr != nil {
		if job == now {
			return ""
		}
		return "unreadable copy"
	}
	return mapDiff(jo, no, func(a, b pricing.Rates) bool { return reflect.DeepEqual(a, b) })
}

// mapDiff lists the keys of now that job lacks or disagrees with (added,
// changed) and the keys of job that now lacks (removed), by key alone.
func mapDiff[V any](job, now map[string]V, eq func(a, b V) bool) string {
	var added, changed, removed []string
	for _, k := range slices.Sorted(maps.Keys(now)) {
		v, ok := job[k]
		switch {
		case !ok:
			added = append(added, k)
		case !eq(v, now[k]):
			changed = append(changed, k)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(job)) {
		if _, ok := now[k]; !ok {
			removed = append(removed, k)
		}
	}
	var parts []string
	for _, g := range []struct {
		label string
		names []string
	}{{"added", added}, {"changed", changed}, {"removed", removed}} {
		if len(g.names) > 0 {
			parts = append(parts, g.label+" "+strings.Join(g.names, ", "))
		}
	}
	return strings.Join(parts, "; ")
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
		if v.state != cloudNotHere {
			if _, lc, _, _, _, rv := resolveCloudRepo(ctx, u.loc.Root, u.o.refresh.cloud); rv == nil && (len(lc.Providers) > 0 || len(lc.ModelPrices) > 0) {
				fmt.Fprintf(u.w, "note: %s\n", checkJobCopyCaveat)
			}
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
	if note := jobConfigCopyNote(ctx, u.loc.Root, u.o.refresh.cloud); note != "" {
		fmt.Fprintf(u.w, "note: %s\n", note)
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
