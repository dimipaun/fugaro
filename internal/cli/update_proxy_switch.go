package cli

// update_proxy_switch.go is S6 of docs/shared-jobs-default: the step of
// `fugaro update` that turns the credentials service's Bitbucket proxy on for a
// project that should have it and whose local config does not say so yet (D2306,
// R7).
//
// The switch is one member of the local config —
// project_secrets.bitbucket-token.proxy, the one `fugaro secrets set
// bitbucket-token --proxy` writes (D493) — and the repositories the credential
// reaches are the same
// entry's allow_data_to. Both are the file's, and the published node's
// bitbucket_token block is DERIVED from them (projectpolicy.DeriveBitbucketToken,
// T2.1 as amended by D493): the block is a proxied run's whole admission, since
// the job that run starts holds no Bitbucket token at all.
//
// So the flip writes the file and the policy stage of the SAME run publishes the
// node it implies, and that is why the decision lives here and is taken by the
// policy stage's plan rather than by a step of its own:
//
//   - The local config and the published node may never disagree for
//     resolveBitbucketProxied (internal/infra/spec.go): an entry that is there
//     while the node does not carry it is a run-stopping refusal at the
//     repository stage, and the repository stage runs BEFORE the policy stage in
//     the one update. A step that wrote the switch ahead of the node would leave
//     the run that wrote it refusing its own work.
//   - Nothing after the policy stage reads the node's bitbucket_token block, so
//     the write is the run's last word on it: the block is published with the
//     node's own write (the policy stage's ETag, its rules gate and its diff),
//     and the file follows it in the same apply.
//   - A stage of its own is not available: the policy stage's place in
//     initStages is fixed (init_stages.go — the order the tests pin), and the
//     other half of this step, the drift comparison that publishes a member no
//     migration carries, is that stage's.

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// bitbucketProxySwitch is the flip this update would make: the local config as
// the run's own reads would hold it with the switch written, and the
// repositories the switch names that the entry did not name already.
type bitbucketProxySwitch struct {
	// lc is the config the policy stage's plan is built from, so the node it
	// publishes carries the block the file is about to hold. It is a copy:
	// nothing this run's other stages read moves under them.
	lc *localcfg.Config
	// add are the project's Bitbucket repositories the entry does not already
	// cover (owner/name, the spelling allow_data_to holds them), which is what
	// the file edit adds — never a repository an entry's own pattern already
	// admits, so a workspace-wide entry is not rewritten into a list of slugs.
	add []string
	// workspaces are the Bitbucket workspaces those repositories are in, one
	// after D2314's refusal, and the name the ask for a token suggests.
	workspaces []string
}

// bitbucketProxyFlip is the flip's cheap half: whether this update would turn
// the switch on, and the project's Bitbucket repositories. It reads the local
// config and this binary alone — no store, no bundle — so the update door's
// in-flight line can ask it before any stage runs (update_in_flight.go).
//
// Every condition here is one the write would be wrong without:
//
//   - the update door, which is the only door this step belongs to. `fugaro
//     setup` and the composite doors reach the policy stage without being the
//     upgrade, and a door that turned an installation's proxy on as a side
//     effect of installing something else would be a decision nobody asked it
//     for. --plan-only applies nothing and so turns nothing on.
//   - a project whose node this run can reach at all (policyNodeUnreachable):
//     the switch is only half of the state, and the node that must carry the
//     other half is one this run would publish.
//   - the credentials service, deployed and recorded with an address. D2306's
//     step is the proxy "with the credentials service update already deploys":
//     the service is what holds the token and mints the request, and an
//     installation without one has nowhere for a proxied run to fetch its git
//     credential from.
//   - a runner at proxySince. A proxied run's job holds no Bitbucket token and
//     reaches the service through its own forwarder, which no runner older than
//     that release has (recipes_skew.go); the pin `fugaro update` installs is
//     this release's base image, so a binary that is not a release, or one
//     older than the proxy, turns nothing on. The launch gate would refuse every
//     run of the repository such a flip produced (proxyGate) — the flip must not
//     be the thing that makes a project unrunnable.
//   - the project's own Bitbucket repositories, which are what the entry's list
//     and the node's allow_repos are derived from. A project that has none has
//     nothing to proxy.
//   - and the entry not answering for the switch itself: an entry that forwards
//     and already covers every one of them is a project this update leaves
//     exactly as it is, and an entry that states OFF is the operator's own no,
//     which this step never turns on (the owner's ruling of 2026-11-18: only an
//     ABSENT field is one the update may answer, D2314's amendment). A stated
//     true that does not cover every repository is still widened below.
func (e *initEngine) bitbucketProxyFlip() (repos []string, turn bool) {
	o, lc := e.r.o, e.lc
	if lc == nil || o == nil || o.doorName() != updateDoor.name || o.planOnly || policyNodeUnreachable(lc) {
		return nil, false
	}
	if lc.CredentialsService == nil || lc.CredentialsService.URL == "" {
		return nil, false
	}
	if v := releaseVersion(); v == "" || newer(proxySince, v) {
		return nil, false
	}
	repos = bitbucketProxyRepos(lc)
	if len(repos) == 0 {
		return nil, false
	}
	// The entry's own answer about the switch ends it, and the two answers are
	// not the same thing (config.ProjectSecret.Proxy's three states): a stated
	// false is the operator's own no, which no step of this update may flip, and
	// a stated true that covers every repository is the project already on it.
	// Only an entry that states NOTHING is one this step answers for.
	if entry, ok := lc.ProjectSecrets[config.BitbucketSecret]; ok && entry.ProxySet() {
		if !entry.ProxyOn() || bitbucketProxyCovers(entry, repos) {
			return nil, false
		}
	}
	return repos, true
}

// bitbucketProxyRepos is the project's own Bitbucket repositories, in the
// spelling the entry's allow_data_to holds a repository (owner/name, lower-cased
// by task.CanonicalRepo — the fold the config's own matcher compares with), in
// the order the config enrols them.
//
// A repository this config cannot name as owner/name is left out rather than
// refused: the policy stage's own readers are what report a config a node cannot
// be built from (usedProviders skips one for the same reason), and a flip that
// refused here would fail an update over an entry another step already judges.
func bitbucketProxyRepos(lc *localcfg.Config) []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(lc.Repos)) {
		if lc.Repos[name].Provider != gitprov.KindBitbucket {
			continue
		}
		slug, err := task.CanonicalRepo(name)
		if err != nil {
			continue
		}
		out = append(out, slug)
	}
	return out
}

// bitbucketProxyCovers reports whether the entry already forwards for every one
// of repos, read with the config's own rule (ProjectSecret.AllowsData: case-folded,
// and "*" inside a segment) rather than by comparing strings: an entry written
// "workspace-a/*" is the documented way to serve a whole workspace, and a project
// whose Bitbucket repositories are all inside it is one this update has nothing
// to turn on.
func bitbucketProxyCovers(entry config.ProjectSecret, repos []string) bool {
	for _, repo := range repos {
		if !entry.AllowsData(repo) {
			return false
		}
	}
	return true
}

// bitbucketProxyWorkspaces is the workspaces repos are in, sorted: the owner
// segment of a slug, which is the scope a Bitbucket access token is granted for
// (D462) and the thing the credentials service's proxy holds one of (D2314).
func bitbucketProxyWorkspaces(repos []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, repo := range repos {
		owner, _, _ := strings.Cut(repo, "/")
		if owner == "" || seen[owner] {
			continue
		}
		seen[owner] = true
		out = append(out, owner)
	}
	slices.Sort(out)
	return out
}

// proxySwitch is the flip's whole decision: the cheap half above, then the two
// gates the switch itself is judged by — one workspace per project (D2314), and
// a workspace token in the credentials bundle for the proxy to forward (R7) —
// and the config copy the policy stage plans and publishes from.
//
// It is asked by the stage's plan (init_policy_migrate.go), which is where the
// node is built, and it is asked again after forget(): the installation stage's
// apply may have written the credentials_service block this run's own outputs
// just produced, and a flip decided before that write would be one the file's
// service block does not cover. The only thing that survives a forget() is the
// warning the bundle gate prints once (proxySwitchSays), because it is about
// this run's session rather than about a read that has since been superseded.
//
// A nil switch with a nil error is "this update turns nothing on", whichever of
// the gates answered: the door, the service, the runner pin, a project already
// on it, or a bundle that holds no token to forward.
func (s *policyMigrateStage) proxySwitch(ctx context.Context) (*bitbucketProxySwitch, error) {
	repos, turn := s.e.bitbucketProxyFlip()
	if !turn {
		return nil, nil
	}
	workspaces := bitbucketProxyWorkspaces(repos)
	if len(workspaces) > 1 {
		return nil, refuseSecondBitbucketWorkspace(s.e.lc, repos, workspaces)
	}
	if s.proxySwitchForwardsNothing(ctx, workspaces) {
		return nil, nil
	}
	entry, _ := s.e.lc.ProjectSecrets[config.BitbucketSecret]
	var add []string
	for _, repo := range repos {
		if !entry.AllowsData(repo) && !slices.Contains(entry.AllowDataTo, repo) {
			add = append(add, repo)
		}
	}
	sw := &bitbucketProxySwitch{lc: bitbucketProxyConfig(s.e.lc, entry, add), add: add, workspaces: workspaces}
	// The flip is a write that would carry the node's block, and the block is
	// derived: a config the derivation refuses is one this step does not make,
	// because the write it asks for is one the publish half would refuse (the
	// state a node already at this version leaves alone otherwise —
	// TestUpdateCurrentNodeWithUnderivableBitbucketDoesNotFail). The entry's own
	// problem is reported rather than raised, once: the runs of the project keep
	// the mounted credential they have, and no step of this update is owed
	// another command because of it.
	if _, err := bitbucketProxyBlock(sw.lc); err != nil {
		s.proxySwitchSays(fmt.Sprintf("this update leaves the Bitbucket proxy switch off: the local config's %s entry cannot be published as it stands, and the block a proxied run is admitted by is derived from it — %s. `%s project policy publish` refuses the same config with the same sentence, and nothing was written",
			config.BitbucketSecret, oneLineCLI(err.Error()), selfCommand()))
		return nil, nil
	}
	return sw, nil
}

// bitbucketProxyBlock derives the node's bitbucket_token block from lc, which is
// the whole of this step's own admission: the block a proxied run is admitted by
// is the derivation and nothing else (DeriveBitbucketToken), so a config it
// refuses is one the flip may not write — and the publish's own drift row is the
// same call on the same config, which is why it is one function rather than two
// spellings of the derivation.
func bitbucketProxyBlock(lc *localcfg.Config) (*projectpolicy.BitbucketToken, error) {
	return projectpolicy.DeriveBitbucketToken(policyProfiles(lc), lc.ProjectSecrets)
}

// proxySwitchForwardsNothing is R7's gate: the proxy forwards the workspace
// token the credentials service's bundle holds, so a project whose bundle holds
// none has nothing for the flip to forward — and a proxied run has no fallback
// at all (D143: the job holds no Bitbucket token, and that is the whole point of
// the proxy). The flip is held back instead of written, and the ask is printed
// once.
//
// Nothing here takes a value. The bundle is the owner's own (C1.25, T2): what
// stores a token is `fugaro creds set bitbucket-token--<name>` at the owner's
// own terminal, which verifies it against Bitbucket before it writes anything,
// and it is refused outright in a coding agent's session and in a run's own
// process — the two sessions this step runs in when it is not the owner's. The
// prompt R7 asks for is therefore the one the no-prompt policy sanctions in
// place of a question (D1540/F14, docs/no-prompt-policy): a line naming the
// command that holds the value, printed with what was left alone, and no wait
// on any stream.
//
// An unreadable bundle holds it back too, and the two no-readings are the same
// answer for opposite reasons to the flip's drop (bundleBitbucketToken,
// update_proxied.go, which goes on under a marker and refuses where the read
// failed): the drop is the step whose going on is safe, and this is the step
// whose going on is not — nothing is written, nothing is published and the next
// update of the owner's own session asks again.
func (s *policyMigrateStage) proxySwitchForwardsNothing(ctx context.Context, workspaces []string) bool {
	r := s.e.r
	held, read := r.bundleHoldsBitbucketToken(ctx, s.e.lc)
	if !read {
		s.proxySwitchSays("this update leaves the Bitbucket proxy switch off: the credentials service's bundle was not read, and whether it holds a Bitbucket token is what turning the switch on is judged by (R7, and the warning above says why the read did not happen). Nothing was written. A person whose identity may read the bundle answers it at their own terminal, and this command is the one to run after it")
		return true
	}
	if held {
		return false
	}
	s.proxySwitchSays(fmt.Sprintf("this update leaves the Bitbucket proxy switch off: the credentials service's bundle holds no Bitbucket token for project %s, and a proxied run of one of its repositories fetches its git credential from the service with nothing left to fall back to (D143). Nothing was written. A person stores that credential at their own terminal, which checks it against Bitbucket and writes nothing where it does not answer for the workspace, and this command is the one to run after it:\n  %s creds set %s\n(%s doctor --check-bitbucket-token reads what the bundle holds today)",
		s.e.lc.Name, selfCommand(), bitbucketProxyTokenName(workspaces), selfCommand()))
	return true
}

// bitbucketProxyTokenName is the token the ask names, in the spelling the store
// takes it in: the family's key with the workspace the project's repositories
// are in, which is the name that token is a workspace token of and the one the
// node's and the bundle's own audit lines then speak (R5). A workspace slug no
// name of the family may hold — the family's own rule, ValidBitbucketTokenName —
// is spelled as the family's placeholder, so the line is the command to run
// rather than a key the store would refuse.
func bitbucketProxyTokenName(workspaces []string) string {
	if len(workspaces) > 0 {
		if key := config.BitbucketTokenKey(workspaces[0]); key != "" {
			return key
		}
	}
	return config.BitbucketTokenPrefix + "<name>"
}

// proxySwitchSays prints the gate's line once per run. The line is a warning
// rather than a refusal of the update (initRun.warn: it reaches the person, and
// --json carries it in the warnings of the object on stdout while the text goes
// to stderr): the whole upgrade is this one command, and a project whose bundle
// holds no token yet is one this update converges everything else for.
func (s *policyMigrateStage) proxySwitchSays(msg string) {
	if s.swSaid {
		return
	}
	s.swSaid = true
	s.e.r.warn(msg)
}

// refuseSecondBitbucketWorkspace is D2314: one Bitbucket access token reaches
// the repositories of one workspace, and the proxy forwards that one token, so a
// project whose Bitbucket repositories span two workspaces is one the credentials
// service cannot serve. The refusal names the second workspace and a repository
// of each, because the fix is the operator's own: a repository in another
// workspace is another project's, or it runs on the per-repository job the
// provider admits without the proxy.
//
// It is the flip's refusal and not a rule over every project: it is raised where
// this update would turn the switch on or widen it, so a project that is already
// forwarding for the workspaces it names is left exactly as it is (a project
// already on it is unchanged), and so is an installation whose credentials
// service is not deployed or whose runner predates the proxy — neither of those
// runs the proxy at all, and a refusal there would stop an update over a state
// this step is not about.
func refuseSecondBitbucketWorkspace(lc *localcfg.Config, repos, workspaces []string) error {
	first, second := workspaces[0], workspaces[1]
	return userErr("project %s has Bitbucket repositories in two workspaces, %s and %s (%s and %s), and the credentials service's Bitbucket proxy forwards one workspace token per project (D2314): the token it holds reaches %s's repositories alone, so a run of %s would have nothing to fetch its git credential with. Give %s its own project, or leave it on a per-repository job without the proxy; nothing was written",
		lc.Name, first, second, bitbucketProxyRepoIn(repos, first), bitbucketProxyRepoIn(repos, second), first, bitbucketProxyRepoIn(repos, second), bitbucketProxyRepoIn(repos, second))
}

// bitbucketProxyRepoIn is one of repos that is in workspace, for a sentence that
// has to name a repository rather than a workspace: the first in the order the
// config enrols them, which is stable between runs of one config.
func bitbucketProxyRepoIn(repos []string, workspace string) string {
	for _, repo := range repos {
		if owner, _, _ := strings.Cut(repo, "/"); owner == workspace {
			return repo
		}
	}
	return workspace
}

// bitbucketProxyConfig is the local config as this run's own reads hold it with
// the switch written: the Bitbucket token's entry forwarding, with the
// repositories it did not cover added to allow_data_to. It is a copy — the maps
// and the slices this run's other stages read are the same ones they had — so
// that the node the policy stage publishes carries the block the file is about
// to hold, and nothing else about the config moves.
//
// A config with no entry at all gets one: the two states that publish no block
// are an entry that is absent and one whose list is empty (D493), and the flip
// is precisely the step that gives the entry both halves.
func bitbucketProxyConfig(lc *localcfg.Config, entry config.ProjectSecret, add []string) *localcfg.Config {
	next := *lc
	next.ProjectSecrets = make(map[string]config.ProjectSecret, len(lc.ProjectSecrets)+1)
	maps.Copy(next.ProjectSecrets, lc.ProjectSecrets)
	entry.AllowDataTo = append(slices.Clone(entry.AllowDataTo), add...)
	entry = entry.WithProxy(true)
	next.ProjectSecrets[config.BitbucketSecret] = entry
	return &next
}

// bundleHoldsBitbucketToken answers whether the credentials service's bundle
// holds a Bitbucket token the proxy could forward — the legacy entry or any
// record of the named family (D2350, T15) — and whether there was a reading to
// answer from at all. It is bundleBitbucketToken's question with the family
// added, and it is readCredentialsBundle's own reading, memoized there: the
// flip and the drop's gate answer from one document, never two.
//
// It reads the two facts that reading sets and not the readiness question
// beside it (credentialsBundleReady's len(names) > 0, which is about the MODEL
// credentials a run needs the service to serve): a bundle holding the workspace
// token alone answers this question yes and that one no, and the flip is judged
// by this one. A reading that sets both facts false is a reading all the same —
// the bundle was read and holds no Bitbucket token, an empty container and a
// DISABLED newest version among them — and it is the case that holds the flip
// back.
func (r *initRun) bundleHoldsBitbucketToken(ctx context.Context, lc *localcfg.Config) (held, read bool) {
	if _, read := r.readCredentialsBundle(ctx, lc); !read {
		return false, false
	}
	return r.credsBitbucket || r.credsBitbucketTyped, true
}

// write is the flip's file half: the switch and the repositories it names, in
// the operator's own config, through the very edits `fugaro secrets set
// bitbucket-token --proxy` and `fugaro secrets allow` make (yamledit, so the
// comments and the order the
// operator gave the file survive; secretsPolicyAddRepos carries the flow-style
// fallback the documentation's own spelling needs), backed up and replaced
// atomically.
//
// It runs after the node's write and never before it. The order is the one
// direction that has no instant of disagreement: an entry that is absent is never
// a disagreement for resolveBitbucketProxied (spec.go), so the file written
// second is the state the node is already in, while a file written first would
// leave a present entry beside a node that does not carry it — the run-stopping
// refusal the repository stage of this very run makes.
func (sw *bitbucketProxySwitch) write(e *initEngine, w io.Writer) error {
	path := e.path
	if path == "" {
		return userErr("the Bitbucket proxy switch cannot be written: this installation has no local config file (a shared config has none), and the published node's %s block is derived from it", bitbucketTokenKey)
	}
	old, err := os.ReadFile(path)
	if err != nil {
		return userErr("turning the Bitbucket proxy on in %s: %v", path, err)
	}
	out, err := secretsPolicyAddRepos(old, secretsBlockProject, config.BitbucketSecret, sw.add)
	if err != nil {
		return userErr("turning the Bitbucket proxy on in %s: %v", path, err)
	}
	if out, err = yamledit.SetScalar(out, []string{secretsBlockProject, config.BitbucketSecret, "proxy"}, "true"); err != nil {
		return userErr("turning the Bitbucket proxy on in %s: %v", path, err)
	}
	if !yamledit.Changed(old, out) {
		return nil
	}
	after, err := localcfg.Parse(out)
	if err != nil {
		return userErr("the local config this would write does not read back: %v", err)
	}
	// The block's own rules, on the bytes this writes and on the one entry it
	// touches — the sentences `secrets set --proxy` refuses with, so the file and
	// the command cannot part company about what they accept. The publish derived
	// the same entry a moment ago and would have refused it first, which is why
	// this is a read-back rather than a second gate.
	if problems := secretsPolicyProblems(secretsBlockProject, config.BitbucketSecret, after); len(problems) > 0 {
		return userErr("%s", problems[0].Message)
	}
	backup, err := backupFile(path, old, time.Now())
	if err != nil {
		return userErr("backing up %s before turning the Bitbucket proxy on: %v", path, err)
	}
	if err := writeFileAtomic(path, out); err != nil {
		return userErr("turning the Bitbucket proxy on in %s: %v", path, err)
	}
	if len(sw.add) > 0 {
		fmt.Fprintf(w, "turned the credentials service's Bitbucket proxy on for %s: project_secrets.%s.proxy is true and its allow_data_to now names %s, in %s (backup at %s)\n",
			strings.Join(sw.workspaces, ", "), config.BitbucketSecret, strings.Join(sw.add, ", "), path, backup)
	} else {
		fmt.Fprintf(w, "turned the credentials service's Bitbucket proxy on for %s: project_secrets.%s.proxy is true, in %s (backup at %s)\n",
			strings.Join(sw.workspaces, ", "), config.BitbucketSecret, path, backup)
	}
	// The file is the truth this machine's later reads go through, and this run
	// holds a config of its own: the entries and the generation the stage's own
	// writes just put there are adopted, so that no reader of the rest of this
	// run answers from the file as it was.
	e.lc.ProjectSecrets = after.ProjectSecrets
	if after.PolicyGeneration != "" {
		e.lc.PolicyGeneration = after.PolicyGeneration
	}
	return nil
}
