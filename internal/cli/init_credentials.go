package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/mirror"
	"github.com/dimipaun/fugaro/internal/task"
)

// The credentials service's image, and the two names the release publishes it
// under. The service itself is deployed by `fugaro update` on every
// installation (docs/credentials-service/default-on.md, the owner's ruling of
// 2026-11-05): what is left in this file is the resolve the deploy pins
// through -- init_stages.go's pinCredentialsImage, which writes the
// installation root's block and moves an existing one onto this release's
// image -- and the two constants the gates that name the service read.

// resolveCredentialsImage is the service's image, resolved and verified
// exactly as a base kind's pin is: the release's "-pinned" tag, its cosign
// signature, and the digest read back through the installation's remote
// repository with the operator's own reader grant on it. What comes out is the
// reference the tfvars carry, digest-pinned; a tag is never returned.
//
// It takes the run's engine rather than a stage because two paths pin through
// it and both must pin the same way: the deploy that gives an installation its
// first block, and the carry that moves an installation's recorded image
// forward to this release's (init_stages.go's pinCredentialsImage). Neither
// writes a digest this did not return: the release's own signed "-pinned"
// image, read back through the remote repository the job pulls by.
func resolveCredentialsImage(ctx context.Context, e *initEngine) (string, error) {
	ver := releaseVersion()
	if ver == "" {
		return "", userErr("this is a development build, so there is no released credentials service image to pin: the service runs the released image, resolved at its signed %q tag, and a build from a checkout has none; run this stage with a released fugaro", credentialsKind+"-pinned")
	}
	allow := e.images.allow()
	srcHost, srcOwner, _ := strings.Cut(allow[0], "/")
	if srcHost != baseSourceHost {
		return "", userErr("the credentials service's image is pinned through the installation's remote repository, whose upstream is https://%s, so its source must be %s/<owner>, and this run's is %s: pass --image-source %s/<owner>", baseSourceHost, baseSourceHost, allow[0], baseSourceHost)
	}
	digest, err := resolvePinnedBase(ctx, allow[0], credentialsKind, ver)
	if err != nil {
		return "", remote(fmt.Errorf("the credentials service's image: %w", err))
	}
	host, err := infra.RegistryHost(e.lc)
	if err != nil {
		return "", userErr("%v", err)
	}
	m, err := newMirror(ctx, e.lc, allow)
	if err != nil {
		return "", err
	}
	regHost, project := registryParts(host)
	client, err := newARClient(ctx, m)
	if err != nil {
		return "", err
	}
	upstream := srcOwner + "/fugaro-" + credentialsKind
	if err := mirror.VerifyThroughRemote(ctx, client, regHost, project, infra.GhcrRegistry, upstream, digest); err != nil {
		return "", remote(fmt.Errorf("the credentials service's image: %w", err))
	}
	return mirror.RemoteRef(regHost, project, infra.GhcrRegistry, upstream, ver, digest), nil
}

// credentialsKind is the release image's kind: the name the release publishes
// it under (ghcr.io/<owner>/fugaro-credentials) and the "-pinned" tag's own
// word.
const credentialsKind = "credentials"

// credsServiceSince is the release that carries the credentials service, for
// the gates that name what to upgrade to.
const credsServiceSince = "0.10.0"

// credentialsBundleReady is whether the installation's credentials service
// holds at least one MODEL credential: the readiness half of the switch
// (infra.CredentialsServiceServes, docs/credentials-service/default-on.md
// §1.5), read once for the run and recorded per repository by
// writeRepoConfig.
//
// It is the service's OWN loader and never a second reading of the secret: the
// same store `fugaro creds set` writes through (openBundleStore), the same
// newest-version read, and the same credsvc.ParseBundle the running service
// parses with, so "the bundle holds a model credential" means here exactly
// what it means to the process that has to hand one back. The document's own
// entries answer it and not the container's existence: a container with no
// version at all, one whose newest version is DISABLED, and one that parses
// with no KindModel entry are three ways of holding nothing.
//
// A read that FAILS is not a reading, which is why it is not an answer either
// (the review of 2026-11-07's third round). The three states that answer are
// facts about the bundle itself, true for every reader: no version at all, a
// newest version that is DISABLED (the state `creds set`'s rotation leaves the
// version it replaced, and the service reads `latest`), and a document that
// parses without a KindModel entry. An error is none of those — a container
// this machine's identity may not read, a version it may not access, a listing
// that failed, a document that does not parse — and the identity that reads
// here is the operator's own, never the service's: the answer a failed read
// would write is a claim about every other machine of the installation, since
// the record travels in the local config that `fugaro update` publishes
// (internal/localcfg/shared.go). Written false there, one refused read would
// switch the service off for every teammate's `fugaro run` — including the ones
// whose jobs had their model mounts dropped by the last apply and have nothing
// else to run on — and every one of them halts at the service's door (D143) on
// the word of a machine that read nothing. So a failed read leaves the record
// as the last reading wrote it, warns what failed, and fails no run.
//
// Nothing here reads the BUDGET backend or the node: the switch's other half,
// the repository's agent.auth, is read where the spec is built, from the
// checkout's own fugaro.yaml (infra.CredentialsServiceServes).
//
// The second answer, record, is whether there is a reading to write down at
// all, and it is false wherever the bundle was not read: a coding agent's
// session and a Fugaro run's own process, where it is not opened — its
// document holds every model credential the service issues, and `fugaro creds
// ls/set` make the same refusal before their first call (credsOwnerOnly,
// internal/cli/creds.go) — and every failed read above. The callers leave each
// entry's own record as the person's last run wrote it then. Answering false
// in either of those states instead would be a write no reading produced: it
// would switch a repository off, or keep it off, on a session's word alone,
// and an agent running `fugaro update` — or an operator whose identity lost
// the bundle's accessor role — would silently rewrite the switch of every
// repository this machine serves.
//
// Both answers are memoized, and that is part of the rule above rather than an
// optimization: one run asks twice — the installation door's backfill, then the
// repository stage's record (init.go's install and repoEngine) — and the second
// answer is a property of the READING, not of the question. Memoizing the
// reading alone would leave the second door answering "the bundle holds no
// model credential" out of a memo the first door wrote as "there was no
// reading": a false no read produced, written into the entry and published for
// the installation, which is the one write this function exists to refuse.
func (r *initRun) credentialsBundleReady(ctx context.Context, lc *localcfg.Config) (ready, record bool) {
	if m := agentMarker(os.Getenv, r.stdinTerminal()); m != "" {
		r.warn(fmt.Sprintf("the credentials service's bundle was not read (%s is set, so this is a coding agent's session) and the switch's record is left as this machine last wrote it: the bundle holds every model credential the service issues, and a person's own %s reads it", m, selfCommand()))
		return false, false
	}
	if m := managedFlagRunMarker(os.Getenv); m != "" {
		r.warn(fmt.Sprintf("the credentials service's bundle was not read (%s is set, so this process is a Fugaro run's) and the switch's record is left as this machine last wrote it: the bundle holds every model credential the service issues, and a person's own %s reads it", m, selfCommand()))
		return false, false
	}
	if r.credsRead {
		return r.credsReady, r.credsRecord
	}
	r.credsRead = true
	// answer is the three states that ARE readings, and it memoizes the switch
	// with them: the reading is spent, so every later door of this run reads it
	// here rather than deciding again from a memo that answered nothing.
	answer := func(holds bool) (bool, bool) {
		r.credsReady, r.credsRecord = holds, true
		return holds, true
	}
	if lc == nil || lc.CredentialsService == nil {
		// No service, nothing to serve a run with — and no read to spend:
		// the block's absence is the answer (the installation could not
		// deploy the service, or its owner removed it).
		return answer(false)
	}
	unreadable := func(err error) (bool, bool) {
		r.warn(fmt.Sprintf("the credentials service's bundle was not read (%s) and the switch's record is left as the last reading wrote it: nothing was read, so there is no answer to write, and a false published here would switch the service off for every machine of this installation", oneLineCLI(err.Error())))
		return false, false
	}
	store, err := openBundleStore(ctx, lc)
	if err != nil {
		return unreadable(err)
	}
	if err := store.check(ctx); err != nil {
		return unreadable(err)
	}
	meta, err := store.newest(ctx)
	if errors.Is(err, errNoBundleVersion) {
		// An empty container is a state and not a failure: the service was
		// deployed and nothing has been stored in it yet, which is exactly
		// the installation a fresh deploy leaves behind.
		return answer(false)
	}
	if err != nil {
		return unreadable(err)
	}
	if meta.state == "DISABLED" {
		return answer(false)
	}
	doc, err := store.access(ctx, meta.number)
	if err != nil {
		return unreadable(err)
	}
	b, err := credsvc.ParseBundle(doc.raw)
	if err != nil {
		return unreadable(err)
	}
	return answer(len(b.Credentials()) > 0)
}

// makeOnboardingRepoEntry makes the entry of the repository this run is about
// to onboard, before the first stage plans.
//
// The credentials service's job table is composed from the local config by both
// of its writers — the deploy (infra.Credentials) and the refresh every
// installation apply makes (infra.RefreshCredentialsServiceJobAccounts) — and
// the stage that applies the installation root is the third, while a repository
// joins the config in the repository stage, which is the ninth. So without this
// the run that onboards a `cascade` repository applies its root with the switch
// on — mounts dropped, roles/run.invoker granted — into a service whose table
// names no job of it, and every run of that repository then halts at the
// service's door: internal/credsvc admits the callers its table holds and
// refuses the rest, and a `cascade` run has no credential of its own to fall
// back to (D143). The table carries the row in the same run the grant is made
// exactly when the entry is here before that run's installation apply plans.
//
// It carries the provider and the workflows, and no more: those are the two
// fields a row is derived from (CredentialsJobAccounts reads one row per
// workflow of the entry, so an entry with none is a repository whose jobs the
// service refuses just the same), and both are what the checkout's default
// branch states. The rest of the entry — the base branch, the app id, the
// record itself — is the repository door's, which is the one that knows the
// plan it was built with; it writes over this shell in the same run.
//
// It is made only where the gate has authorised the onboarding: a repository
// the user declined, and one this run cannot ask about at all (a coding agent's
// session, a pipe, --json), is left alone. A name the config already lists at
// another host is left alone too: that entry belongs to the other provider's
// repository, and infra.Repo refuses the disagreement rather than switching it
// (spec.go's provider check).
//
// A run that authorises the onboarding and then stops before the repository
// stage leaves the row behind, and that is the direction this table fails in:
// the refresh UNIONS the derived rows with the document's, so an extra row
// binds an account no token is ever minted for, while a missing one is a job
// that holds the invoker grant and is refused.
func (e *initEngine) makeOnboardingRepoEntry() {
	if e.lc == nil || e.repo == nil {
		return
	}
	tg := e.repo.tg
	if tg == nil || tg.cfg == nil || repoNameListed(e.lc, tg.origin) {
		return
	}
	if a, err := e.authState(tg.origin); err != nil || a != authOK {
		return
	}
	if e.lc.Repos == nil {
		e.lc.Repos = map[string]localcfg.Repo{}
	}
	e.lc.Repos[repoConfigKey(e.lc, tg.repo)] = localcfg.Repo{
		Provider:  tg.cfg.Git.Provider,
		Workflows: slices.Sorted(maps.Keys(tg.cfg.Workflows)),
	}
}

// recordCredentialsServiceReady records, on the ONE repository entry whose root
// this run applies, the switch that root's plan was built with
// (docs/credentials-service/default-on.md, DON8's record, amended by the review
// of 2026-11-07).
//
// The value is the SWITCH and not the bundle reading alone: the auth that
// repository's runs are judged by is folded in, through the one predicate
// (infra.CredentialsServiceServes), so a repository that is served and a
// repository whose root was applied with the switch OFF are two different
// records, which bare bundle readiness could not tell apart. The one state it
// closes is the natural adoption order — an owner fills the bundle, runs
// `fugaro update --repo` while the checkout still says `agent.auth: oauth` (the
// root is planned held off: mounts kept, no roles/run.invoker), and only then
// commits the cascade. Under bare readiness that run recorded true, and the
// next launch, reading the cascade live and the record as "ready", handed the
// job the service's address while its account held no grant to knock with: the
// provider then opens keyless for a mint Cloud Run refuses, and a `cascade` run
// has no credential of its own to fall back to (D143). Folded in, the record
// says what happened — this root's last apply was held off — the launch
// withholds the address, the job keeps the mounts that apply kept, and the run
// after the cascade is committed is the one that applies the root with the
// switch on and records true.
//
// It is the repository door's (repoEngine, init.go), and it runs before the
// repository root's spec is built, so the plan that root makes and the record
// the launch reads are the same answer. Without this the record lags the plan
// by one run — the plan of a held-off root keeps the job's model mounts and
// grants its account no roles/run.invoker, while the record written afterwards
// tells the next launch the service is ready, which is the same broken state.
//
// One entry and not every entry the local config holds, which is what this
// helper did first: the record is read as "this repository's root has been
// applied with the service on", and the ONLY thing that makes the service
// usable to a repository is the roles/run.invoker grant its own root's apply
// makes (modules/workflow/sa.tf's google_cloud_run_v2_service_iam_member.
// credentials; the installation root grants run.invoker to operators, the
// coordinator and the verifier and to no job account). Recording readiness for
// a repository whose root this run does not apply is therefore a record of
// something that did not happen: its launch would be handed the service's
// address while its job accounts hold no grant, and a `cascade` run has no
// credential of its own to fall back to, so it halts at the service's door
// (D143). The installation door (install, init.go) applies no repository root
// at all and so writes no record — which is also why a run outside a checkout,
// whose repository stage is skipped, leaves every entry as it was.
//
// A repository the local config does not hold yet gets its entry from this
// function, provider and nothing else, and that is the onboarding's one-run
// rule (the review of 2026-11-07's fourth round): the spec below reads the
// entry, so an onboarding that created it only in the write after the apply
// deployed a held-off root on its first `fugaro update --repo` — mounts kept,
// no invoker grant — and the next launch, reading the record that write left
// (false), withheld the address too, leaving a `cascade` run with no model
// credential and nothing to swap it for until a second, unannounced update
// in the same checkout. The entry is created here, before infra.Repo reads
// it, so the plan and the record are the same answer on the run that onboards
// — which is what they are on every later run.
//
// The value is written false as well as true: a bundle that is later emptied —
// or a checkout edited back to an auth the service does not serve — switches
// the repository off again on the next run, and that is the fail-safe direction
// (see writeRepoConfig). Recorded and not merely "on": since the review's
// fourth round the field tells "no record" from a recorded false
// (localcfg.Repo.CredentialsServiceReady is a pointer, and
// localcfg.MergeShared carries a published record over a missing one), so this
// write is what lets a switch-off travel to the other machines — and the
// nothing-written case below is what keeps a machine that read nothing from
// erasing theirs.
//
// auth is the repository's agent.auth as the RUN will read it, which is the
// document the spec below is built from and not this working tree's
// (deploymentConfig, repoEngine): the record has to be the answer the plan was
// made with, and a record judged on a working tree the runs never see is the
// same disagreement one edit away.
//
// Nothing is written where the bundle was not read (credentialsBundleReady's
// second answer): in a coding agent's session or in a run's own process, whose
// bundle is a secret they may not open, and on a read that FAILED, whose error
// is an answer about this machine and not about the bundle — the record travels
// in the published config, so a false written from one would switch the service
// off for every machine of the installation. This repository keeps the record
// the last reading left on it either way.
//
// outs is the installation root's own outputs, and it is TWO things here.
//
// It is the first condition of the switch, read from the same source the spec
// beside this reads it from (infra.Inputs.Installation, which withDefaults
// never fills from the local config), so a record cannot say on for a root the
// spec held off. The two part company where the local config is the stale one:
// an owner who takes the service out leaves a config still carrying the block
// and its URL (docs/credentials-service/runbook.md §9), and a `fugaro update
// --repo` in a checkout then reads outputs with no service at all while
// planning that repository's root held off. The bundle's reading alone would
// record true there, and the next launch — which reads the config's block —
// would hand the job the address of a service that no longer exists: mounts
// kept, no roles/run.invoker, and a provider opened keyless for a mint nothing
// answers (D143).
//
// And it is the fallback for the bundle read: on the run that deploys the
// service the config is written by the very step this precedes, so the outputs
// are the only place the service's existence is known. Only the block's
// PRESENCE is read for that — the bundle's container is derived from the
// project and the installation's name (openBundleStore) — so a URL the first
// apply could not know yet is not a reason to answer false.
func (r *initRun) recordCredentialsServiceReady(ctx context.Context, lc *localcfg.Config, outs infra.InstallationOutputs, repo, provider, auth string) {
	if lc == nil {
		return
	}
	key := repoConfigKey(lc, repo)
	entry, ok := lc.Repos[key]
	if !ok {
		// A repository the local config does not hold gets its entry HERE,
		// before the spec below reads it, and not only in the write after the
		// apply (writeRepoConfig, init.go): the spec is built from this
		// value, so a first onboarding whose entry is created afterwards is
		// deployed with the switch off — mounts kept, no invoker grant — and
		// the cascade the owner just committed keeps no address until a
		// SECOND `fugaro update --repo` in the same checkout, which the run
		// neither asks for nor announces. `fugaro update --repo` is the
		// documented onboarding, and CLAUDE.md's HARD RULE is that it is the
		// whole upgrade: one run, the same one main ran.
		//
		// The entry is the shell the write after the apply fills in (provider
		// and nothing else), and it is this map's only use of it that matters
		// before then: infra.Repo reads the KEY (via RepoFor) and, from the
		// rest, the provider — which the checkout's own fugaro.yaml has just
		// been held to (repoEngine resolves the config first) — and the base
		// branch the repository's own file sets. Everything else the spec
		// reads comes from that document and not from repos:.
		entry = localcfg.Repo{Provider: provider}
	}
	installed := outs.CredentialsService != nil
	ready, record := false, true
	if installed {
		// The bundle is read only where the installation HAS the service: the
		// container is the installation's, so a read on an installation
		// without one would be a cloud call about a resource that does not
		// exist — and its answer could not move the record anyway, the plan
		// this run applies being held off by the same outputs.
		ready, record = r.credentialsBundleReady(ctx, credentialsServiceProbe(lc, outs))
	}
	if !record {
		// Nowhere the bundle was not read — a coding agent's session, a run's
		// own process, or a read that failed — is there a reading to write, so
		// the entry keeps the record the person's last run left on it. A failed
		// read is the one that reaches an operator's own run, and the write it
		// would make is the installation-wide one the doc above refuses.
		return
	}
	// The one predicate, not a second copy of its conjunction: installed is
	// read here exactly as the spec reads it, and ready is this run's reading
	// of the bundle — which the probe answers false for, without a read, when
	// no block is known — so the record cannot come out differently from the
	// plan it is the record of.
	entry.CredentialsServiceReady = localcfg.RecordSwitch(infra.CredentialsServiceServes(installed, ready, auth))
	if lc.Repos == nil {
		lc.Repos = map[string]localcfg.Repo{}
	}
	lc.Repos[key] = entry
}

// credentialsServiceProbe is the config credentialsBundleReady is read over:
// lc itself, with the installation's block taken from its own outputs when the
// local config does not carry one yet. That is the run which deploys the
// service — the config is written by a step after the one that reads the
// bundle — and the outputs are the only place the service's existence is known
// there. A nil block leaves the probe as it is: with no service anywhere the
// answer is false without a read, which is what credentialsBundleReady says of
// a config that has no block.
func credentialsServiceProbe(lc *localcfg.Config, outs infra.InstallationOutputs) *localcfg.Config {
	if lc == nil || lc.CredentialsService != nil {
		return lc
	}
	block := credentialsServiceBlock(outs.CredentialsService)
	if block == nil {
		return lc
	}
	withBlock := *lc
	withBlock.CredentialsService = block
	return &withBlock
}

// backfillCredentialsServiceReady records the switch on every repository entry
// whose root has already been applied with the credentials service on — by any
// machine, from any checkout — and whose entry carries no record yet.
//
// It is the upgrade path of docs/credentials-service/default-on.md's DON1, and
// it exists because the record is new in this release: an entry written before
// it carries none, so an installation that opted in under an earlier release is
// indistinguishable from one that never did. Such an installation's roots were
// applied with the service on — their jobs mount no model credential and their
// accounts hold roles/run.invoker — and this release's launcher, which reads
// the record as the switch's second condition, would withhold the service's
// address from a `cascade` run of them: a job with no model mount and no
// address halts at the service's door (D143).
//
// The installation door is where it belongs, and not the repository door,
// because the record has to reach the roots this door does NOT apply: the
// repository stage applies the checkout's own root and writes that entry from
// the plan it builds, while the siblings of the installation are roots the
// next `fugaro update` in THIS checkout never re-applies. Without the backfill
// the upgrade converges one repository per checkout it is run in, which is an
// operator step beside `fugaro update` — and CLAUDE.md's HARD RULE is that
// `fugaro update` is the whole upgrade.
//
// The evidence is the SHARED root's own state, never a guess and never this
// machine's copy of it (appliedRepoCredentialsService, over
// infra.RepoCredentialsServiceApplied): the objects under the repository's
// prefix in the installation's state bucket carry the invoker grant exactly
// when that root's apply made it. It reads the state and not the tfvars a
// workdir holds — which is what the review of 2026-11-07's fourth round found
// — because those are per-machine (<state>/fugaro/terraform/<project>/repos/
// <slug>) and the question is the installation's: a repository whose root was
// applied from another machine, or from another checkout of this one, has no
// document here, so a workdir read would report "never applied" for a root
// that is applied — and the upgrade would converge one checkout at a time, the
// operator step this function exists to remove. The state is also the honest
// half of the pair: the tfvars are what a run HANDED Terraform, so an apply
// that failed leaves a document ahead of the state. A repository whose state
// holds no such grant — never applied anywhere, applied with the service off,
// or forgotten — is left alone: the record vouches for a grant, and none of
// those is evidence of one. That read is made for an entry recorded `false` as
// well as for one with no record (the loop below says why), and it is the only
// thing that repairs a stale false: the record is published whole, so a machine
// whose own apply predates the grant keeps writing its `false` over the `true`
// the machine that made the grant published, and no other command would ever
// correct it (localcfg's mergeRepoRecords). The value is this run's bundle
// reading, the same one the repository door writes, and the bundle reading
// alone: this door fills
// the roots no checkout here applies, so it has neither the installation's
// outputs nor their files to fold in — and does not need them, the grant that
// brought a key here being one an apply made with the switch on, while both
// readers judge those two conditions again, live, on the run that reads the
// record (localcfg.Repo.CredentialsServiceReady).
//
// Nothing is written where the bundle was not read (credentialsBundleReady's
// second answer): in a coding agent's session or in a run's own process, and on
// a read that failed. Every entry keeps the record the person's last run left
// on it, and the convergence happens on that person's next `fugaro update` —
// which is still the one command CLAUDE.md's HARD RULE promises. A failed read
// costs this door a convergence rather than a wrong write: an entry it cannot
// judge keeps the record it had, so an installation whose opt-in predates this
// release converges on the next run that CAN read the bundle — which is the
// same door, the next time it works, and never a second command. A state it
// cannot read is the same shape, one repository at a time.
//
// Both doors that apply the installation root and write the local config call
// it: install (init.go), and initFirebase (init_firebase.go), because
// `fugaro update --firebase-project <id>` runs no installation stage and
// applies that root itself — a door that skipped it would leave this upgrade's
// record to the next plain update, which is the second command the rule above
// forbids.
func (r *initRun) backfillCredentialsServiceReady(ctx context.Context, c *infra.Clients, stateBucket string, lc *localcfg.Config, outs infra.InstallationOutputs) {
	if lc == nil || len(lc.Repos) == 0 {
		return
	}
	var switched []string
	for key := range lc.Repos {
		// A record saying on is a door's judgement of that root
		// (recordCredentialsServiceReady, or a previous backfill), and this one
		// has nothing to add to it. Every other entry is read against the
		// installation's own state: no record at all is an entry written before
		// the field existed, and a recorded false is an apply that ran with the
		// switch off — the emptied bundle, or an auth the service does not
		// serve. Neither is evidence that the grant is absent, and the false in
		// particular is not: the apply that made the grant can be NEWER than
		// it, made on another machine from a bundle filled since, and a false
		// left alone here outranks the true that machine published for every
		// reader of the published file (localcfg's mergeRepoRecords is where
		// that happens) — the installation-wide outage of the switch this read
		// is what keeps `fugaro update` alone able to repair. It costs one
		// state read per such entry per update, which is the price of that
		// repair: the alternative is a record that no door can correct.
		if lc.Repos[key].CredentialsServiceOn() {
			continue
		}
		on, err := appliedRepoCredentialsService(ctx, c, stateBucket, lc, key)
		if err != nil {
			r.warn(fmt.Sprintf("whether %s's repository root carries the credentials service could not be read (%s): its record is left as it is, and a run of it may not be handed the service's address until the next update reads the state", key, oneLineCLI(err.Error())))
			continue
		}
		if on {
			switched = append(switched, key)
		}
	}
	if len(switched) == 0 {
		return
	}
	ready, record := r.credentialsBundleReady(ctx, credentialsServiceProbe(lc, outs))
	if !record {
		// Nowhere the bundle was not read — a coding agent's session, a run's
		// own process, or a read that failed — is there a reading to write, so
		// every entry keeps the record the person's last run left on it and the
		// upgrade converges on that person's next `fugaro update`. The entries
		// this door visits are the ones whose record is not already true, and
		// the value it would write is this run's own reading, so a failed read
		// costs it a convergence and cannot clear a record someone else wrote.
		return
	}
	for _, key := range switched {
		entry := lc.Repos[key]
		entry.CredentialsServiceReady = localcfg.RecordSwitch(ready)
		lc.Repos[key] = entry
	}
}

// appliedRepoCredentialsService reports whether the repository root has been
// applied with the installation's credentials service on, read from the
// installation's own copy of that root's state
// (infra.RepoCredentialsServiceApplied: the objects under the repository's
// prefix in the state bucket, whichever machine applied them).
//
// It is the upgrade backfill's reader
// (backfillCredentialsServiceReady), and it is deliberately not
// deployedRepoCredentialsService beside it: that one reads the tfvars a
// workdir holds, which are THIS machine's copy of what a run handed Terraform.
// The question here is the installation's — was that root applied with the
// service on, by anyone — so the answer has to come from the installation's
// own state (the review of 2026-11-07's fourth round).
//
// A repository the local config does not hold, one it cannot name a slug for,
// and a run with no state bucket to read all answer false: none of them names
// a root whose state could carry the grant.
func appliedRepoCredentialsService(ctx context.Context, c *infra.Clients, stateBucket string, lc *localcfg.Config, repo string) (bool, error) {
	if lc == nil || c == nil || stateBucket == "" {
		return false, nil
	}
	entry, ok := lc.RepoFor(repo)
	if !ok {
		return false, nil
	}
	slug, err := task.Slug(entry.Provider, repo)
	if err != nil {
		// A repository whose provider this config cannot name has no state
		// prefix to have written the root's state under: the slug is what
		// names it (repoEngine's own derivation).
		return false, nil
	}
	return infra.RepoCredentialsServiceApplied(ctx, c, stateBucket, slug)
}

// deployedRepoCredentialsService reports whether the repository root this
// machine last applied carries the installation's credentials service: the
// tfvars the repository door wrote into the repository's own workdir
// (infra.RepoWorkdir, ReadWorkdirVars, infra.RepoCredentialsServiceDeployed).
//
// It answers the one question neither the local config nor the repository's
// fugaro.yaml can, and it has ONE reader: a launch that cannot read the run's
// own document at all — `fugaro run --repo X` from a directory that holds no
// checkout of X, so there is no file to read and no ref to read one out of
// (run.go's launchFileConfig) — which has no agent.auth to judge the switch's
// third condition on. The root's own apply already made that decision: it
// dropped the job's model mounts and granted its account roles/run.invoker
// exactly when this key is there, and a job is handed the service's address to
// match the containers it runs (run.go's credentialsServiceLaunchEnv call
// site).
//
// The upgrade backfill used to read this too, and does not any more: it asks
// about roots another machine or another checkout applied, and those wrote no
// document into THIS machine's workdir (appliedRepoCredentialsService, over
// the installation's own state). A launch keeps the workdir read because it is
// the one this machine can afford: the state bucket is the operator's — it is
// created so that project Viewers cannot read it — and a developer launching a
// run holds no grant on it.
//
// A repository the local config does not hold, one it cannot name a slug for,
// and one whose root was applied with the service off (the key null, which is
// what repoCredentialsService writes for every other answer) all answer false,
// and so does a repository this machine holds no tfvars for: "no document" and
// "a document that says off" are deliberately the same answer, because neither
// is evidence that a job's account may call the service. A document that is
// there and cannot be read is the error, which no caller may read as a yes.
//
// The residual it leaves: the tfvars are what the run handed Terraform, not
// the state, so an apply that failed or was refused leaves a document ahead of
// the state — a root whose document says "on" while the job kept its mounts
// would be handed an address it cannot use. The authoritative read is the
// Cloud Run service's IAM policy (doctor_brokering.go's
// doctorCredentialsServiceDeployed), which costs a permission the operator
// holds by role and a developer launching a run need not; the backfill reads
// the state, which is the same fact one step earlier (the apply's own
// addresses) at a permission this machine already spends on every repository
// root it plans.
func deployedRepoCredentialsService(lc *localcfg.Config, repo string) (bool, error) {
	if lc == nil || lc.GCPProject == "" {
		return false, nil
	}
	entry, ok := lc.RepoFor(repo)
	if !ok {
		return false, nil
	}
	slug, err := task.Slug(entry.Provider, repo)
	if err != nil {
		// A repository whose provider this config cannot name has no
		// repository root's workdir to have written a document into: the
		// slug is what names it (repoEngine's own derivation).
		return false, nil
	}
	dir, err := infra.RepoWorkdir(os.Getenv, lc.GCPProject, slug)
	if err != nil {
		return false, nil
	}
	vars, err := infra.ReadWorkdirVars(dir, "repo")
	if err != nil {
		return false, err
	}
	return infra.RepoCredentialsServiceDeployed(vars)
}

// deploymentConfig is the checkout's resolved config as this run DEPLOYS the
// repository root from: the same document with agent.auth taken from the
// fugaro.yaml at the default branch — the file the runs of this repository
// read — where the working tree would answer differently.
//
// The switch (infra.CredentialsServiceServes) is judged on agent.auth, so a
// plan that reads it from another document than the launch does is a job
// deployed for one auth and launched for another. `fugaro update --repo` reads
// the working tree, which an uncommitted edit or a checkout parked on another
// branch can leave saying oauth over a default branch that says cascade; the
// launcher reads the file at the ref the run checks out (internal/cli/run.go's
// launchFileConfig). Worktree oauth over a cascade default branch plans a root
// that keeps the job's model mounts and grants its account no
// roles/run.invoker, and the launch hands that job the service's address
// anyway, which is the halt with no fallback D143 is about. The other
// direction drops the mount and withholds the address, which leaves the job
// with no model credential at all. The two answers have to come from one
// document, and the document is the default branch's: the repository stage
// already holds the working tree to it, byte for byte (init_repo_target.go's
// readFugaroYAML comparison, "the engine reads the working tree: it must be
// the default branch's file"), and it is the ref a run is cut from unless the
// checkout is parked on another branch origin has (run_ref.go's newRunRef). A
// run cut with --ref from a third branch whose own fugaro.yaml says something
// else again is the case this cannot reach: no update can know which branch a
// future run is cut from, and the default branch is the ref a plan can be held
// to.
//
// It is agent.auth alone that moves, and it moves for the whole deployment and
// not only for the switch: agent.auth is also what decides which model
// credential a job mounts and whether it is a Vertex one (infra's SecretMounts
// and WorkflowAccounts), so a deployment planned for one auth and launched for
// another is incoherent on those axes too. Nothing else is taken from the
// default branch — the workflows, the secrets and the base branch this engine
// deploys are the working tree's, as they have always been.
//
// A default branch this checkout cannot answer for — no remote-tracking ref,
// no file at it, a file that does not resolve, or no project layer to resolve
// it over — leaves the config exactly as it was, which is the fallback the
// launcher's own read takes (launchFileConfig). In the converge door the two
// documents are the same file anyway: the repository stage refuses a checkout
// whose fugaro.yaml is not the default branch's before this engine runs.
func deploymentConfig(ctx context.Context, root string, cfg *config.Config) *config.Config {
	if cfg == nil || root == "" {
		return cfg
	}
	db := defaultBranchParse(ctx, root)
	if db.cfg == nil || db.cfg.Agent.Auth == "" || db.cfg.Agent.Auth == cfg.Agent.Auth {
		return cfg
	}
	out := *cfg
	out.Agent.Auth = db.cfg.Agent.Auth
	return &out
}
