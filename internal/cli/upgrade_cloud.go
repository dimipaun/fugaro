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
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runner"
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
func resolveCloudRepo(ctx context.Context, root string, co cloudOptions) (rf resolvedFile, lc *localcfg.Config, lcPath, repo, host string, v *cloudVerdict) {
	co.stderr = func() io.Writer { return io.Discard } // the caller prints the per-checkout line
	failed := func(repo string, err error) *cloudVerdict {
		return &cloudVerdict{state: cloudFailed, repo: repo, reason: oneLine(err.Error())}
	}
	if releaseVersion() == "" {
		return resolvedFile{}, nil, "", "", "", &cloudVerdict{state: cloudNotHere, reason: "a development build has no release base image to move to"}
	}
	if _, err := os.Stat(filepath.Join(root, "fugaro.yaml")); errors.Is(err, os.ErrNotExist) {
		return resolvedFile{}, nil, "", "", "", &cloudVerdict{state: cloudNotHere, reason: "no fugaro.yaml yet, so no job image to refresh"}
	}
	_, rf, err := loadCheckoutResolved(ctx, root, nil, layerOptions{Lenient: true, Offline: true})
	if err != nil {
		return resolvedFile{}, nil, "", "", "", failed("", err)
	}
	repo, err = checkoutRepo(ctx, root)
	if err != nil {
		return resolvedFile{}, nil, "", "", "", failed("", err)
	}
	lc, lcPath, _, err = loadRepoConfig(ctx, &initOptions{cloud: co}, root)
	if err != nil {
		var se *localcfg.SelectError
		if checkoutNamesProject(ctx, root) && (errors.As(err, &se) || errors.Is(err, localcfg.ErrMissing)) {
			return resolvedFile{}, nil, "", repo, "", &cloudVerdict{state: cloudNotHere, repo: repo, reason: "no local project config selects this checkout (" + oneLine(err.Error()) + "): the cloud step is for the operator who set the project up"}
		}
		return resolvedFile{}, nil, "", "", "", failed(repo, err)
	}
	oi, ok := readOrigin(ctx, root)
	if !ok {
		return resolvedFile{}, nil, "", "", "", failed(repo, errors.New("cannot read the checkout's origin"))
	}
	if !repoKnown(lc, oi) {
		return resolvedFile{}, nil, "", repo, "", &cloudVerdict{state: cloudNotHere, repo: repo, reason: fmt.Sprintf("%s is not onboarded to project %s, so it has no job image yet", pluginwire.Printable(repo), lc.Name)}
	}
	host, err = infra.RegistryHost(lc)
	if err != nil {
		return resolvedFile{}, nil, "", "", "", failed(repo, err)
	}
	return rf, lc, lcPath, repo, host, nil
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
	rf, lc, lcPath, repo, host, v := resolveCloudRepo(ctx, root, co)
	if v != nil {
		return *v
	}
	cfg := rf.Cfg
	ver := releaseVersion()
	set := map[string]bool{}
	for _, w := range cfg.Workflows {
		set[w.BaseKind()] = true
	}
	var custom, stale []string
	for _, k := range slices.Sorted(maps.Keys(set)) {
		cur := lc.BaseImage(k)
		if cur != "" && !isManaged(cur, host, k, baseOwner(lc)) {
			custom = append(custom, k)
			continue
		}
		// currentness, not a value: the pin is a digest through the remote
		// repository and cannot be computed offline (design §5.4's B3). An
		// installation whose entry is the OLD shape at this very version —
		// the copy an earlier CLI made — is stale, and must be, or the
		// migration would silently never happen on the path most users take.
		if _, current, err := refreshBase(lc, k, ver); err != nil {
			return cloudVerdict{state: cloudFailed, repo: repo, reason: oneLine(err.Error())}
		} else if !current {
			was := "none recorded"
			if cur != "" {
				was = pluginwire.Printable(cur)
			}
			stale = append(stale, fmt.Sprintf("%s: %s; this release pins it by digest through %s/%s", k, was, lc.GCPProject, infra.GhcrRegistry))
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

// checkJobCopyCaveat is --check's note on the job's config copy
// (jobConfigCopyNote): --check makes no cloud call, so, unlike a real cloud
// step, it can never tell whether the deployed job's copy of the local
// project config is stale in either direction (docs/multi-model.md "Then run
// fugaro update --repo"). It is printed whenever a real cloud step would run
// (cloudStep), whatever the local config currently holds: an owner who just
// removed every provider locally still needs to know the job might still
// carry the old ones, which --check cannot see either.
const checkJobCopyCaveat = "fugaro upgrade --check makes no cloud call, so it cannot tell whether the deployed job's copy of this local project config (providers, model_prices, budget settings, the region's compute price override and the GitHub App ID) still matches, or is stale, in either direction; run fugaro upgrade without --check to find out, or just run fugaro update --repo . in your own terminal to refresh it (never run automatically)"

// jobConfigCopyTimeout bounds the cloud work jobConfigCopyNote does (opening
// the runs bucket to check the project's name, then reading each existing
// workflow's job): the same risk layerBucketTimeout (layer_resolve.go)
// guards against for the project layer's own bucket read. A package var so
// a test can shorten it.
var jobConfigCopyTimeout = 30 * time.Second

// jobConfigCopyNote is the cloud step's extra report, after a real refresh:
// whether any of the repository's deployed workflow jobs carries a copy of
// the local project config that no longer matches what this local config
// would write now. A version upgrade never refreshes that copy; only
// fugaro update --repo does, and fugaro upgrade never runs it itself (its
// Terraform confirmations stay the operator's). It compares every env var
// infra.Workflow sets from the local config (spec.Env, minus
// infra.PlatformEnv's installation-identity keys, which legitimately vary by
// deploy and are not what fugaro update --repo refreshes from an edited local
// config): providers and model_prices (compared by field, since they carry
// JSON, decoded and reflect.DeepEqual'd so key order or "0.14" vs "0.140" is
// never a false difference) and every other key by exact match (the budget
// mode, cap, token limit and allow-list, the RTDB URL, the Firebase API key,
// budget grace, the region's compute price override and the GitHub App ID)
// — so a newly added local-config-derived env var is covered automatically,
// never missed the way this once was.
//
// This local-config-derived half of spec.Env is the same for every workflow
// of one repository (it depends on the local config and the repository, not
// on a workflow's own fugaro.yaml entry), but which workflows have a
// deployed job at all, and what each one's job happened to be deployed
// with, can differ: a workflow just added to fugaro.yaml has no job yet,
// while an older sibling's job may already be stale. Checking only one
// workflow (review follow-up, PR #324: the alphabetically-first one) missed
// a stale sibling whenever that one workflow's job did not exist yet. Every
// workflow with a job is checked, in sorted order, and every distinct way
// any of them differs is reported, deduplicated; a workflow with no job yet
// is skipped, like the single-workflow case always was.
//
// Only each workflow's default-account job: infra.Workflow always builds
// the config.DefaultClaudeAccount variant (a repository may also deploy a
// named-account job per docs/design/named-oauth-tokens.md, a separate job
// this does not read). The same fugaro update --repo that refreshes the
// default one also refreshes every named-account job, so running it fixes
// both; this note just never checks the named ones itself.
//
// "" is nothing to say: no workflow has a job yet, resolveCloudRepo finds
// nothing to check here at all (no fugaro.yaml, a teammate checkout, a
// development build, a repository not onboarded: U9's cloudNotHere skips), or
// every existing job's copy agrees **and no mount can have drifted under it**,
// in which case the note is silent, exactly as the cloud step itself is in
// those cases. The last one is narrower than "the copies agree" (code review,
// minor: the mount clause below was added to the mount-only branch and this
// paragraph kept the old, exhaustive wording): a project-scope name in the
// repository's job one is created or deleted for after its apply changes what
// that job mounts without changing a byte of its env, so jobs whose env
// matches are still a reason to speak. Everything else is
// not silent, resolveCloudRepo's own cloudFailed included (a bad fugaro.yaml
// or origin, an unreadable local config): it says it could not check, with
// resolveCloudRepo's own reason (already one line and safe to print, the
// same text the cloud step's "cloud: failed:" line would show) for
// cloudFailed, and a fixed, never-the-error's-own-text reason for a real
// error past that point (the checkout's origin, a workflow's spec, the
// cloud connection or a job read itself — any of which could carry a URL
// or, from an underlying client, a token).
func jobConfigCopyNote(ctx context.Context, root string, co cloudOptions) string {
	rf, lc, _, repo, _, v := resolveCloudRepo(ctx, root, co)
	if v != nil {
		if v.state == cloudFailed {
			return couldNotCheckJobConfigCopy(v.reason)
		}
		return "" // a U9 skip (cloudNotHere): nothing to check here
	}
	cfg := rf.Cfg
	names := slices.Sorted(maps.Keys(cfg.Workflows))
	if len(names) == 0 {
		return ""
	}
	repoURL, err := checkoutURL(ctx, root)
	if err != nil {
		return couldNotCheckJobConfigCopy("cannot read the checkout's origin")
	}
	// jobConfigCopyNote is an advisory extra on an otherwise-successful
	// fugaro upgrade, not the thing the operator ran for (the same risk
	// layerBucketTimeout guards against for the project layer's own bucket
	// read): a flaky or offline network must not hang the whole command in
	// a client retry loop just to report on something optional.
	cctx, cancel := context.WithTimeout(ctx, jobConfigCopyTimeout)
	defer cancel()
	env, err := openCloudFor(cctx, lc)
	if err != nil {
		return couldNotCheckJobConfigCopy("cannot connect to the cloud")
	}
	defer env.Close()
	be, ok := env.be.(interface {
		JobFullEnv(ctx context.Context, job string) (map[string]string, bool, error)
	})
	if !ok {
		return ""
	}
	var now map[string]string
	var diffs []string
	seen := map[string]bool{}
	projectScopes := map[string]bool{}
	anyJob := false
	for _, name := range names {
		spec, err := infra.Workflow(infra.Inputs{LC: lc, Repo: repo, Cfg: cfg, FileSetsBaseBranch: fileSetsBaseBranch(rf), RepoURL: repoURL}, name)
		if err != nil {
			return couldNotCheckJobConfigCopy("cannot compute the repository's spec from the local config")
		}
		now = spec.Env // the same local-config-derived half for every workflow
		// The mount half, which no env diff can see: the names this job
		// mounts that this repository may also use from the project's
		// container (infra.ProjectSecretCandidate, the one answer about
		// scope). A project-scope copy of one of these changes the
		// container the job mounts, and that is not in the job's env at
		// all (FUGARO_SECRET_ENVS carries variable names, not IDs).
		for logical := range spec.SecretIDs {
			if _, ok := infra.ProjectSecretCandidate(lc, logical, repo); ok {
				projectScopes[logical] = true
			}
		}
		jobEnv, exists, err := be.JobFullEnv(cctx, spec.Job)
		if err != nil {
			return couldNotCheckJobConfigCopy("reading the deployed job failed")
		}
		if !exists {
			continue
		}
		anyJob = true
		for _, d := range jobConfigDiffs(jobEnv, now) {
			if !seen[d] {
				seen[d] = true
				diffs = append(diffs, d)
			}
		}
	}
	if !anyJob {
		return "" // no workflow has a deployed job here yet
	}
	mount := jobConfigCopyMountNote(slices.Sorted(maps.Keys(projectScopes)))
	// The credentials clause (C1.20) is orthogonal to both of the halves
	// below: it is about the workflow's own auth mode and the two owner-run
	// steps it needs, not about anything the deployed copy carries. It is
	// therefore the LAST clause of every shape this note takes, and it is a
	// reason to speak on its own — a cascade repository whose env copy
	// matches still has something to be told, which is why the silent branch
	// below is reached only when all three clauses are "".
	creds := jobConfigCopyCredentialsNote(cfg)
	if len(diffs) == 0 {
		// The mount-only case: nothing the diff can see has drifted, but
		// the mounts are the half it never sees, so the note is still
		// owed a reader who created or deleted a project secret since
		// the last update --repo. Nobody holds such a name, though: then
		// there is nothing this note could tell them and it stays
		// silent, exactly as it did before the mounts had a clause.
		if mount == "" && creds == "" {
			return ""
		}
		if mount == "" {
			return "the deployed job's environment matches the local project config, but " + creds
		}
		return "the deployed job's environment matches the local project config, but the mounts are not in that copy: " + mount + joinClause(creds)
	}
	stale := fmt.Sprintf("the deployed job's copy of the local project config is STALE (%s): a version upgrade never refreshes it",
		strings.Join(diffs, "; "))
	// The policy half (plan task 1.6 step 5): with a policy published, the
	// keys it governs are read from the node at run time, so drift in them is
	// no longer a reason to re-run update --repo — and saying which keys still
	// are is the difference between a note the operator acts on and one they
	// learn to ignore.
	if p := jobConfigCopyPolicyNote(cctx, lc); p != "" {
		return stale + p + joinClause(mount) + joinClause(creds)
	}
	tail := fmt.Sprintf("; runs on %s use the old copy until you run fugaro update --repo . in your own terminal (never run automatically)",
		pluginwire.Printable(repo))
	return stale + tail + joinClause(mount) + joinClause(creds)
}

// joinClause is one "; "-separated clause, or nothing when the clause is ""
// (so a caller never emits a trailing "; ").
func joinClause(s string) string {
	if s == "" {
		return ""
	}
	return "; " + s
}

// jobConfigCopyMountNote is the drift note's mount clause (plan Task 2.3
// step 2): after D2 a name stored at project scope changes what a job
// mounts when the project's copy is created or deleted, so the mounts are
// the half of the job's configuration the policy does not carry and the
// note has to keep sending the operator to update --repo for. Phase 3 moves
// the mounts to a shared profile, which is the release that removes the
// step, and saying so here is what keeps this note from reading as a
// permanent tax.
//
// names are the mounted logical secrets this repository may use from the
// project's container (infra.ProjectSecretCandidate), sorted, and it is the
// only thing the clause is about: with none of them, no project secret can
// change a mount of this repository's job, so there is no reason to send
// anybody to update --repo for it and the clause is "" (the note is silent in
// the mount-only case then). The caller joins it with "; " after the STALE
// sentence, or prints it alone.
//
// Both halves of the question are stated, because a reader who has just
// rotated a value is the one most likely to re-run a step that changes
// nothing: creating or deleting a project secret is what needs the re-run,
// a new version or a rotated value is not — the job mounts the container,
// not a copy of the value.
func jobConfigCopyMountNote(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf("what a job mounts is still decided here: a project secret created or deleted after the last `fugaro update --repo .` changes what this repository's job mounts (`fugaro secrets set NAME`, D2's default), and for this repository those names are %s — that is what a re-run is for, while a new version of one of them or a rotated value needs no re-run, because the job mounts the container and not a copy of the value; a run picks a mount change up on the next `fugaro update --repo .` in your own terminal, until phase 3 gives the mounts to a shared profile",
		strings.Join(names, ", "))
}

// jobConfigCopyCredentialsNote is the drift note's credentials clause (plan
// task C1.20): the one thing `fugaro upgrade` has to say about the
// credentials service, and the reason it has to say it here.
//
// `agent.auth: cascade` is a value the setup skill offers and the config
// reader accepts (C1.1) from the release that carries the service, and a
// repository whose file names it is a repository whose jobs must NOT mount a
// model credential: the run asks the installation's service for one instead.
// That is not something a version upgrade does. Two owner-run steps do it —
// the installation's credentials stage, which turns the service on, and each
// repository's own `fugaro update --repo .`, which drops the job's
// model-secret accessors and grants its account an invoker on the service —
// and until they have run, the checkout says `cascade` while its jobs are
// unchanged, exactly the half-state the skill's offer is pinned against
// (C1.19). An upgrade is where the operator learns it: the step names both,
// in the order they have to run.
//
// "" for every other auth: nothing of this is about a workflow that mounts
// its own credential, which is every repository until its file says
// otherwise. The check is the resolved auth (the same defaulting the config
// reader does), so a file that names none is judged by the default rather
// than by the empty string.
func jobConfigCopyCredentialsNote(cfg *config.Config) string {
	auth := cfg.Agent.Auth
	if auth == "" {
		auth = config.DefaultAgentAuth
	}
	if auth != config.AuthCascade {
		return ""
	}
	// --firebase-project is a string flag with no default, so the note names
	// its value; the repository's own file is the only place this side can
	// read one from, and a checkout that records none says what to type.
	return fmt.Sprintf("this repository's workflows run with agent.auth: %s, and that mode's credential comes from the installation's credentials service at call time: a job that is `cascade` must mount no model credential at all, and the two steps that make it so are owner-run. Turn the service on in the installation with `fugaro update --provision-credentials` (a typed confirmation, and it needs the installation's Firebase rules redeployed with `fugaro update --firebase-project %s` first), then run `fugaro update --repo .` in this checkout and in every other repository of the project, which is what drops each job's model-secret accessors and grants its account an invoker on the service. Until both have run, a %s run is refused at launch and the deployed job still mounts the credential it should not", config.AuthCascade, firebaseProjectIDWord(cfg.GCPProject), config.AuthCascade)
}

// jobConfigCopyPolicyNote is the drift note's policy clause: what a
// published policy now governs, and what a job's environment still has to be
// refreshed for. "" when there is nothing to say — no policy published, no
// database to read one from, or a read that failed: the note is advisory,
// and the STALE sentence it would qualify stands on its own.
//
// The keys named as still needing update --repo are the ones a run cannot do
// without and does not read from the node: `providers`, which the node carries
// and a run routes by the job's own copy of (the one cut to this repository's
// allow_data_to, whose key name is the mounted secret — so a provider change
// still reaches every repository through update --repo, and the routing switch
// resolves against the copy the job has), the pointer that makes the policy
// reachable at all (RTDBURLEnv, FirebaseAPIKeyEnv — task 1.4), the mounts
// (a workflow's committed secrets and agent.auth, which come from the
// repository's own fugaro.yaml), and the platform-side settings
// (infra.PlatformEnv: the region's compute price, the GitHub App ID).
func jobConfigCopyPolicyNote(ctx context.Context, lc *localcfg.Config) string {
	_, found, err := projectPolicyRead(ctx, lc)
	if err != nil || !found {
		return ""
	}
	return fmt.Sprintf("; the policy published at %s now governs model_prices, the budget mode, the per-run ceiling, the allowed models, the token limit, the allow-list and the routing switch, and runs read those from the node: drift in them is not a reason to re-run fugaro update --repo. The providers block is the part the node carries that a run does not read from it — a run routes by the copy in its own job's environment, cut to this repository with the key's mount, so a changed provider (its base URL, models, allow_data_to or route_fee_pct) is still a reason to re-run it, as are the keys the policy does not carry (%s and %s, without which a run never finds the policy; the repository's mounted secrets and agent.auth; the region's compute price and the GitHub App ID)",
		projectpolicy.PathShared(), infra.RTDBURLEnv, infra.FirebaseAPIKeyEnv)
}

// couldNotCheckJobConfigCopy is jobConfigCopyNote's note when a real error
// (not "no job yet") stops it from comparing the copies: a fixed, per-step
// reason, never the error's own text, so nothing it might carry (a bucket or
// API URL, a client's wrapped token) can reach the terminal through it.
func couldNotCheckJobConfigCopy(reason string) string {
	return "could not check the job's config copy (" + reason + "); its providers, model_prices, budget and other owner-only settings may be stale without this saying so"
}

// jobConfigDiffs names every way job (the deployed workflow job's whole env)
// differs from now (spec.Env, the same workflow's env computed from the
// local config and the checkout's fugaro.yaml right now) in the fields the
// local config, not the platform, controls: providers and model_prices by
// field (providersDiff, pricesDiff), and every other local-config-derived
// key (scalarConfigDiff) by exact match. Never a value.
func jobConfigDiffs(job, now map[string]string) []string {
	var diffs []string
	if d := providersDiff(job[infra.ModelProvidersEnv], now[infra.ModelProvidersEnv]); d != "" {
		diffs = append(diffs, "providers: "+d)
	}
	if d := pricesDiff(job[infra.ModelPricesEnv], now[infra.ModelPricesEnv]); d != "" {
		diffs = append(diffs, "model_prices: "+d)
	}
	if d := scalarConfigDiff(job, now); d != "" {
		diffs = append(diffs, d)
	}
	return diffs
}

// scalarConfigDiff is mapDiff over every env key infra.Workflow can set from
// the local config, except providers and model_prices (compared by field
// above, since they carry JSON), infra.PlatformEnv's installation-identity
// keys and runner.SecretEnvsVar (excluded: see scalarConfigEnv). Every
// remaining key (the budget mode, cap, token limit and allow-list, the RTDB
// URL, the Firebase API key, budget grace, the region's compute price
// override, the GitHub App ID, and any local-config-derived key
// infra.Workflow adds later) is compared as plain text: none of them
// carries JSON, so a textual difference is a real one.
func scalarConfigDiff(job, now map[string]string) string {
	return mapDiff(scalarConfigEnv(job), scalarConfigEnv(now), func(a, b string) bool { return a == b })
}

// scalarConfigEnv drops infra.PlatformEnv's keys, the two JSON-valued keys
// (providers, model_prices) and runner.SecretEnvsVar from a job's or a
// spec's env. SecretEnvsVar is a derived list of mounted variable NAMES, not
// a local-config value in its own right: it also reflects the repository's
// own fugaro.yaml (its workflow's committed secrets, its agent.auth), which
// fugaro update --repo refreshes from the checkout, not from an edited local
// project config, so comparing it here would flag drift this note is not
// about. A changed local-config provider already shows up directly, through
// providersDiff, with or without this key.
func scalarConfigEnv(env map[string]string) map[string]string {
	out := make(map[string]string, len(env))
	for k, v := range env {
		out[k] = v
	}
	for _, k := range infra.PlatformEnv {
		delete(out, k)
	}
	delete(out, infra.ModelProvidersEnv)
	delete(out, infra.ModelPricesEnv)
	delete(out, runner.SecretEnvsVar)
	return out
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
			fmt.Fprintf(u.w, "note: %s\n", checkJobCopyCaveat)
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
		return stepResult{state: stepSkipped, reason: cloudAgentRefusal(u)}
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
		return stepResult{state: stepCurrent, reason: "no base image to pin and no image to rebuild"}
	}
	return stepResult{state: stepDone, reason: "the repository is on this release's base image"}
}

// cloudAgentRefusal is why the cloud step applies nothing in a coding agent's
// session (U7), in one place: cloudStep returns it as its own skip, and the
// checkout stage of the converge refuses the step with it before the step is
// reached, its plan being local-only under a marker
// (init_checkout_upgrade.go). One refusal, one wording, so the two doors say
// the same thing about the same session.
func cloudAgentRefusal(u *upgradeCtx) string {
	reason := fmt.Sprintf("a coding agent's session (%s is set) never applies cloud changes, by design; in your own terminal window, not through the agent, run %s (it confirms every step itself, each billable build included), or %s to be asked at each step",
		u.agent, u.o.cloudLine(u.loc.Root, true), u.o.cloudLine(u.loc.Root, false))
	if h := initflow.IDEHint(u.agent); h != "" {
		reason += " (" + h + ")"
	}
	return reason
}

// cloudLine is the command that runs the cloud step for root in the user's
// own terminal: this run's flags without --check and --local, with or
// without --yes.
func (o upgradeOptions) cloudLine(root string, yes bool) string {
	c := o
	c.check, c.local, c.yes = false, false, yes
	return c.again(root)
}
