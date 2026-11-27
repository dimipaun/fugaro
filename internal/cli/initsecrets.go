package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"golang.org/x/term"
	"google.golang.org/api/googleapi"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
)

// The secrets stage (stage 7, design §3.5): the credentials only the person
// at the keyboard may hold, for the checkout init runs in. Its rules:
//
//   - values come only from a hidden prompt on a real terminal. A stdin that
//     is not a terminal is never read, not even a pipe (an agent could be
//     holding the other end); the stage then leaves the user the one-line
//     fugaro secrets set commands, which keep their own pipe and file forms.
//   - --yes, --non-interactive and --json never reach it: with any of them,
//     it prompts for nothing and reads nothing.
//   - nothing is prompted when a coding agent's environment is present
//     (agentMarkers), or when stdin, stdout or stderr is not a terminal (a
//     redirected stderr would send the prompt to a file and make the user
//     type blind). This is a mitigation, not a barrier: an agent that already
//     holds a secret's bytes, or drives a pty, can get around any gate here.
//     The real controls are that the skills never touch a value (their
//     forbidden-instruction lint) and that the user types it.
//   - the secrets asked for are the ones the repository's jobs mount
//     (infra.SecretMounts, the function that mounts them), read from the
//     checkout's fugaro.yaml: the git credential, the Claude credential the
//     agent.auth names (none for vertex) and the workflows' own. A model
//     provider's key is not one of them: since 0.27.0 it is entered in the
//     credentials service's bundle, one entry per repository (KS2, D1670
//     reversed), and what the stage says about it is where it is entered
//     (providerKeyNotTakenNotice), never a prompt for a value it would refuse
//     to store.
//   - a secret that already has a version is skipped, and is never read,
//     overwritten or rotated here (that is fugaro secrets set).
//   - a value lives in memory only from the prompt to the store call, is
//     registered with the loop's redaction while it does, and is never in an
//     output, the result JSON, an error, a log or an argument or variable of
//     a subprocess (the stage starts none).
//
// It runs before the repository stage, so the repository's containers do
// not exist yet: the store call creates the secret with the same labels
// update --repo's Terraform adopts (the roots import an existing secret), so
// the first image build finds its secrets stored.

// maxHeldSecrets is the redaction slots the engine keeps for values a stage
// is holding (the secrets stage holds one at a time: the value and each line of a
// multi-line one).
const maxHeldSecrets = 128

// maxPEMLines is the most lines a pasted PEM may have: each long enough line
// takes a redaction slot, and the whole value takes one (a 4096-bit RSA key is
// about 52 lines).
const maxPEMLines = 120

// secretStore is what the stage needs of Secret Manager: metadata and
// storing a value; it has no read of a value.
type secretStore interface {
	List(ctx context.Context, labels map[string]string) ([]gcp.SecretInfo, error)
	Set(ctx context.Context, id string, value []byte, labels map[string]string) (string, error)
}

// selfCommand is how every command init prints names the binary: the one
// rule is the program as the user ran it (os.Args[0], so a bare "fugaro" from
// the PATH stays "fugaro", and a path stays the path), as one shell word
// through quoteWord; plain "fugaro" when that cannot be one line. Tests
// replace it.
var selfCommand = func() string {
	arg := os.Args[0]
	if arg == "" || strings.ContainsAny(arg, "\r\n\x00") {
		return "fugaro"
	}
	return quoteWord(arg)
}

// heldValues is the strings a run registers to keep one value out of its
// output: the value itself, and — because a redactor that works line by line
// can never match a value spanning two of them — each of its lines that is
// long enough to be one. A short line is one the CLI has already decided not
// to treat as a secret (minSecretBytes), and it is the same answer the
// converge's slots give, so `fugaro update` and a command's own run hold one
// value the same way.
func heldValues(value []byte) []string {
	vals := []string{string(value)}
	if bytes.Contains(value, []byte("\n")) {
		for _, l := range bytes.Split(value, []byte("\n")) {
			if len(l) >= minSecretBytes {
				vals = append(vals, string(l))
			}
		}
	}
	return vals
}

// hold registers value with the loop's redaction until release: the whole
// value and each of its lines (a PEM's body lines are as secret as the whole).
// It fails closed: when a slot is missing for any of them it registers
// nothing and returns an error, so a value is never held in part. The error
// does not carry the value.
func (e *initEngine) hold(value []byte) (release func(), err error) {
	vals := heldValues(value)
	var free []int
	for i := range e.held {
		if e.held[i] == "" {
			free = append(free, i)
		}
	}
	if len(free) < len(vals) {
		return nil, userErr("the value has more lines than can be kept out of the output (at most %d); nothing was stored", maxPEMLines)
	}
	taken := free[:len(vals)]
	for n, v := range vals {
		e.held[taken[n]] = v
	}
	return func() {
		for _, i := range taken {
			e.held[i] = ""
		}
	}, nil
}

// agentMarkers are the environment variables a coding agent's session sets.
// With any of them present the stage never prompts: the person typing must be
// at their own terminal, not behind an agent. CLAUDE_CODE_SSE_PORT is also set
// by the Claude Code IDE extension in the integrated terminals of VS Code and
// JetBrains, so a person's own IDE terminal is one of them only without a real
// terminal: agentMarker classifies it (agentenv.Session), and only that case
// is a person's own IDE terminal rather than an agent's session.
//
// The list lives in internal/agentenv because the owner-run live
// credentials-rules check in internal/e2e refuses on it too
// (credentials-service plan F33), and one list cannot drift from the other.
var agentMarkers = agentenv.Markers

// agentMarker is the marker that makes getenv a coding agent's session for a
// run with (or without) a real terminal on its input, or "" when it is a
// person's: an agent-set marker is one whatever the terminal, and the IDE
// extension's marker alone is one only without a terminal
// (agentenv.Session). It is the one classification every marker reader in this
// package asks, so a person's own IDE terminal is treated as a person's
// everywhere and an agent's tool call — which has no terminal — is refused
// everywhere.
//
// It is this package's one seam to agentenv and holds no rule of its own: the
// call sites have the same two facts (os.Getenv, and whether the run's input
// is a terminal) and hand them over in one order, and the name is the one a
// source scan requires a case for at every agent-marker site of init
// (agentSitesInInit, managed_test.go). agentenv's own two questions keep their
// own names, which is why this is not called Marker: Session is the session
// this run is in, Marker is the first variable set. A caller that only wants
// the yes/no asks this for "", and the accepted IDE terminal — the one session
// that is a person's with a marker set — is agentenv.IDETerminal, asked where
// the line that says so is printed.
func agentMarker(getenv func(string) string, stdinTerminal bool) string {
	return agentenv.Session(getenv, stdinTerminal)
}

// ideNotice is the one line a run prints when it accepts a confirmation from a
// person's own IDE terminal (CLAUDE_CODE_SSE_PORT alone, at a real terminal)
// instead of refusing it. It says only what changed: which marker was read, and
// that the session is a person's IDE terminal rather than a coding agent's.
const ideNotice = "note: this is the Claude Code IDE extension's own terminal (" + agentenv.IDEMarker + " is set), not a coding agent's session, so the confirmation is accepted"

// writerIsTerminal reports whether w is a terminal. Tests replace it.
var writerIsTerminal = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// secretsStage is stage 7.
type secretsStage struct {
	e *initEngine
	// open connects to Secret Manager; tests replace it.
	open func(ctx context.Context) (secretStore, error)
	// out is where prompts go (stderr, as fugaro secrets set does).
	out func() io.Writer

	tg      *secretTarget
	origin  originInfo // the checkout's origin, for the hostile-checkout gate
	root    string     // the checkout's top
	skip    string
	checked bool
	missing []string // names with no stored value
	stored  []string // names stored in this run, for Verify

	// node is the project's published policy, read once when the stage
	// resolves through the run's own memoized read (initRun.policy) and nil
	// when no node was read. It is the source SD17 answers with: a machine
	// whose local config holds no project_secrets.bitbucket-token entry reads
	// the allow-list the owner published instead of being handed a
	// per-repository token, and this stage's store decision and the plan's
	// mount decision (infra.ProjectSecretCandidateFromNode) are the one
	// answer.
	node *projectpolicy.Policy

	// list is the update door's work (U3a): one target per enrolled
	// repository, in canonical order, each carrying what its jobs mount and
	// where the stage left it. It is nil for setup, whose one checkout is tg:
	// the door is what decides which of the two the stage has, and every
	// method that reads one repository asks for its target (U3a, R18).
	list []*secretTarget
	// lines are what the stage reported about each repository, in the same
	// order, and linesReported is the once-per-run guard: Check, Plan and
	// Apply all call checkList, and the run's own record must not hold the
	// same line three times.
	lines         []initflow.RepoLine
	linesReported bool
}

func newSecretsStage(e *initEngine) *secretsStage {
	return &secretsStage{e: e,
		open: func(ctx context.Context) (secretStore, error) {
			sm, err := gcp.NewSecrets(ctx, gcpOptions(e.lc))
			if err != nil {
				return nil, remote(err)
			}
			return sm, nil
		},
		out: func() io.Writer { return e.r.cmd.ErrOrStderr() },
	}
}

// secretTarget is one repository whose secrets the stage takes: the setup
// door's checkout, or one enrolled repository of the update door's list.
type secretTarget struct {
	repo, slug, label, provider string
	wanted                      []string // every secret its jobs mount, in order
	noConfig                    bool     // no fugaro.yaml here: only the git credential is known

	// The update door's per-repository state (U3a). They are empty for the
	// checkout target, whose state the stage's own fields carry.
	unread string // the reader could not read this repository: its one-line reason
	skip   string // the stage does not apply to this repository, and why
	// enrol is a repository this project's own record does not name -- not
	// this machine's config and not the published policy's allow-list -- and
	// which this door does not enrol (D5, D3350). It is left for the user,
	// with the command that enrols it.
	enrol bool
	// missing, repoLevel, providerKeys and checked mirror the stage's own
	// fields for this repository alone: what find() left, the names D2313
	// drops, the provider keys F21 leaves a line for, and whether the listing
	// ran.
	missing      []string
	repoLevel    []string
	providerKeys []string
	checked      bool
	// detail is the line the stage reports about this repository (state is
	// what it did for it).
	detail string
	state  initflow.RepoState
}

// read is whether this target is one the stage could work on: the reader read
// it, the stage applies to it, and the project's record names it.
func (t *secretTarget) read() bool {
	return t != nil && t.unread == "" && t.skip == "" && !t.enrol
}

func (s *secretsStage) Name() string { return initflow.Secrets }

// canPrompt is whether this run may show a hidden prompt: stdin, stdout and
// stderr are terminals, no coding agent's environment is present, and
// neither --non-interactive nor --yes (which never supplies a secret, and is
// the flag of an unattended run) nor --json (the output of a caller that reads
// it: a hard rule, --json never prompts) is set.
//
// The update door adds one condition, and only it (D3347, R18): a value is
// asked for there only under --ask, on any project, so without the flag the
// stage leaves its one line per missing name and never prompts — which is what
// makes `fugaro update` ask nothing by default. `setup` keeps the rule above
// whole: its own steps are confirmed by the person at the terminal already, so
// its value entry is not behind the flag.
func (s *secretsStage) canPrompt() bool {
	o, cmd := s.e.r.o, s.e.r.cmd
	if s.listDoor() && !o.ask {
		return false
	}
	return !o.nonInteractive && !o.yes && !o.asJSON && agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())) == "" &&
		stdinIsTerminal(cmd.InOrStdin()) && writerIsTerminal(cmd.ErrOrStderr()) && writerIsTerminal(cmd.OutOrStdout())
}

// allowedHarnessOAuthMounts is mounts with every harness OAuth credential
// this stage may not store dropped — never prompted for, never stored. Two
// families, for two different reasons:
//
//   - a claude-oauth-token-<name> entry this repository is not a configured,
//     allowed account for (the rule this filter has always enforced);
//   - any Codex name at all — codex-oauth-token and the whole
//     codex-oauth-token-<name> namespace — because the harness's OAuth
//     credential is reserved ahead of the harness itself (config.CodexOAuthToken,
//     owner ruling of 2026-10-18): Fugaro does not drive Codex, so no account
//     and no provider of it exists to allow one, and a value stored here would
//     be the token the harness reads the day it lands;
//   - a name that is the conventional key name of a configured provider and
//     not that provider's own secret (config.ReservedProviderSecret): the
//     name belongs to that provider's key (the convention of 2026-10-18), so a
//     workflow's value stored under it would become the provider's credential.
//
// config.Validate (internal/config/validate.go) already refuses a workflow
// from ever declaring a secret of the first two shapes, and
// infra.SecretMounts refuses one that is a provider's key, so in practice
// mounts (SecretMounts's output, which includes a workflow's own declared
// secrets) never carry any of them once a checkout's fugaro.yaml parses at
// all; this is the second, independent layer that drops them regardless,
// whatever path they arrived by (a bypassed or future-weakened Validate, a
// cached config, an older local config, or any other caller), matching
// secretsSet's own refusals of the same shapes.
func allowedHarnessOAuthMounts(lc *localcfg.Config, repo string, mounts []infra.SecretMount) []infra.SecretMount {
	out := make([]infra.SecretMount, 0, len(mounts))
	for _, m := range mounts {
		if strings.HasPrefix(m.Logical, config.ClaudeOAuthTokenPrefix) && !claudeOAuthAccountAllowed(lc.ClaudeAccounts, m.Logical, repo) {
			continue
		}
		if config.CodexOAuthSecret(m.Logical) {
			continue
		}
		if p, reserved := config.ReservedProviderSecret(lc.Providers, m.Logical); reserved && lc.Providers[p].Secret != m.Logical {
			// What is dropped here is the name reserved for that provider's
			// key while the provider's key is stored under another one. A
			// provider's *own* key mount (SecretMounts built it from p.Secret,
			// with the runner's variable) survives this filter -- it is a
			// provider's own key and not a name reserved for one, which is not
			// what the filter is about -- and resolve() drops it later, where
			// the KS2 answer for a provider key is said
			// (providerKeyNotTakenNotice): the store is the credentials
			// service's bundle's entry, not a container of its own.
			continue
		}
		out = append(out, m)
	}
	return out
}

// listDoor is whether this run's door reads the project's repositories rather
// than the working directory's checkout: `fugaro update`'s converge does, and
// `setup` keeps its checkout target (U3a, R18). The branch is on the door and
// not on a stage flag, because one engine serves both and only the door knows
// which list is the run's.
func (s *secretsStage) listDoor() bool { return s.e.r.o.doorName() == updateDoor.name }

// listTargets are the repositories this run works on: the update door's
// enrolled list, or the one checkout the setup door resolved. A caller that
// must not guess which of the two a run has asks this.
func (s *secretsStage) listTargets() []*secretTarget {
	if s.list != nil {
		return s.list
	}
	if s.tg != nil {
		return []*secretTarget{s.tg}
	}
	return nil
}

// singleCheckout is whether this run takes a value the way `fugaro update`
// always has: the list it read is one repository, that repository is the
// checkout the run was started in and one the stage can work on, and a person
// is at this terminal to answer. The door's own line means something only
// where this holds -- a `fugaro update` that asks for nothing is a step that
// leaves itself -- and the store decision is the checkout door's own
// (D2313, D3347).
//
// C1's stand-in answers the update door with the checkout the run was started
// in, so this is every `update` in a checkout and nothing else for now (R21).
// It is a stopgap in one direction only: once I1 connects the real reader the
// list is the project's enrolled repositories, and a list of many is left with
// the command that stores each name -- asking there is `--ask`'s, which U3b
// gave the door (R18): canPrompt above turns it on, and a list of many is the
// reader work I1 still owes.
func (s *secretsStage) singleCheckout(ctx context.Context, f *os.File) bool {
	if len(s.list) != 1 || !s.tg.read() || !s.canPrompt() || f == nil {
		return false
	}
	oi, ok := readOrigin(ctx, ".")
	if !ok || !sameRepo(oi.Repo, s.tg.repo) || oi.Host != providerHosts[s.tg.provider] {
		return false
	}
	s.origin = oi
	return true
}

// resolve finds the repositories the stage works on and what their jobs mount.
//
// The update door reads the project's every enrolled repository through
// e.repos -- the same list from every directory, each read at its own default
// branch -- and keeps one target per view (U3a, R18). The setup door keeps its
// checkout: it applies only to a checkout of a repository of this project,
// found as fugaro secrets set finds one, and skip says why it does not.
func (s *secretsStage) resolve(ctx context.Context) {
	if s.tg != nil || s.skip != "" || s.list != nil {
		return
	}
	if s.listDoor() {
		s.resolveList(ctx)
		return
	}
	s.resolveCheckout(ctx)
}

// resolveList is the update door's resolve: one target per enrolled
// repository, in the canonical order e.repos answers, with the reader's own
// answer for a repository it could not read and the stage's for one it does
// not apply to. A run whose reader found no repository at all is skipped, as
// the checkout door is outside a checkout.
//
// The hostile-checkout gate is not asked (D3350): the list is the project's
// own, an admit-only repository in it is enrolled, and a stage of `update`
// plans nothing from a directory. It still guards `setup --repo` and
// `--onboard-repo`, which are the doors that act on a checkout a person made.
func (s *secretsStage) resolveList(ctx context.Context) {
	lc := s.e.lc
	views, _ := s.e.repos(ctx)
	if len(views) == 0 {
		s.skip = "no repository of this project was read: run " + s.e.r.o.doorProgram() + " in a checkout to take one's secrets"
		return
	}
	// The published policy, read once before the targets are filled: a name's
	// scope (projectScope) reads the node, and the checkout door reads it at
	// the same point for the same reason (SD17).
	p, found, err := s.e.r.policyMemo().read(ctx, lc)
	s.node = repoPolicyInput(lc, p, found, err)
	for _, v := range views {
		tg := &secretTarget{repo: v.Repo, provider: v.Provider}
		switch {
		case !v.Read():
			// The reader's own reason, sanitised by the view: a stage reports
			// an unread repository and never guesses at it (R13).
			tg.unread = v.Unread
		default:
			local, _ := localRepo(lc, v.Repo)
			tg.skip = s.fillTarget(tg, v.Repo, local, v.Cfg)
			switch {
			case tg.skip != "":
			case !s.enrolledRepo(tg):
				tg.enrol = true
			default:
				// The two names the stage does not take are dropped once the
				// node is in hand, as they are for the checkout: D2313's
				// repository-level names and a configured provider's own key.
				s.scopeTarget(tg)
			}
		}
		s.list = append(s.list, tg)
	}
	if len(s.list) == 1 {
		// One repository keeps the stage's own single-target fields the way the
		// setup door fills them, so a line, a scope question or a test that
		// reads the stage and not the list answers about that repository.
		s.tg = s.list[0]
	}
}

// resolveCheckout is the setup door's resolve: the working directory's
// checkout, its local-config entry and the fugaro.yaml of its default branch
// (not the working tree's, which whoever made the checkout could have
// changed). A fugaro.yaml naming another project, or a checkout the project
// does not know, is skipped, so a container is never created for an unrelated
// checkout.
func (s *secretsStage) resolveCheckout(ctx context.Context) {
	lc := s.e.lc
	root, err := gitRead(ctx, ".", "rev-parse", "--show-toplevel")
	oi, ok := readOrigin(ctx, root)
	if err != nil || !ok {
		s.skip = "not in a checkout of a repository: run " + s.e.r.o.doorProgram() + " there to take its secrets"
		return
	}
	repo := oi.Repo
	local, known := localRepo(lc, repo)
	cfg := defaultBranchConfig(ctx, root)
	if cfg != nil && cfg.Project != lc.Name {
		s.skip = "this checkout's fugaro.yaml names another project (or none): its secrets are not taken here"
		return
	}
	if !known && cfg == nil {
		s.skip = "this checkout is not a repository of this project yet: onboard it first"
		return
	}
	tg := &secretTarget{repo: repo}
	if reason := s.fillTarget(tg, repo, local, cfg); reason != "" {
		s.skip = reason
		return
	}
	s.tg, s.origin, s.root = tg, oi, root
	// The published policy, read here because the node is what decides where a
	// name is stored for a repository this machine's local config does not
	// carry an entry for (SD17). It is the run's own memoized read -- the
	// repository stage reads the same node later for the spec's
	// bitbucket_proxied fact -- so this moves that read earlier rather than
	// adding one, and a read that fails passes nil, exactly as
	// repoPolicyInput gives the repository stage: a node the operator cannot
	// reach must not be taken for one that admits nobody, and the repository
	// stage stops the run on the same error.
	p, found, err := s.e.r.policyMemo().read(ctx, lc)
	s.node = repoPolicyInput(lc, p, found, err)
	s.scopeTarget(s.tg)
}

// localRepo is this machine's local-config entry for a repository, and whether
// it lists it at all (the /-spelling and the clone URL's ".git" are one
// repository, as everywhere else).
func localRepo(lc *localcfg.Config, repo string) (localcfg.Repo, bool) {
	want, err := task.CanonicalRepo(repo)
	if err != nil {
		return localcfg.Repo{}, false
	}
	for name, r := range lc.Repos {
		if c, err := task.CanonicalRepo(name); err == nil && c == want {
			return r, true
		}
	}
	return localcfg.Repo{}, false
}

// fillTarget fills one repository's target from its local-config entry and the
// fugaro.yaml of its default branch (cfg, nil when it holds none): the provider
// the two agree on (never the origin's host), the repository's slug and label,
// and every name its jobs mount. It answers the one-line reason the repository
// cannot be worked out at all, and "" when it can.
//
// It does not do the scope filter: a name's project scope reads the published
// node, which resolveList reads before it fills the targets and
// resolveCheckout reads right after. Both call scopeTarget once the node is in
// hand.
func (s *secretsStage) fillTarget(tg *secretTarget, repo string, local localcfg.Repo, cfg *config.Config) string {
	lc := s.e.lc
	provider := local.Provider
	if cfg != nil {
		switch {
		case provider != "" && cfg.Git.Provider != "" && provider != cfg.Git.Provider:
			return "the local config and its fugaro.yaml disagree on the git provider; make them agree"
		case provider == "":
			provider = cfg.Git.Provider
		}
	}
	if _, ok := infra.GitSecret(provider); !ok {
		return "its git provider is not github or bitbucket"
	}
	slug, err := task.Slug(provider, repo)
	if err != nil {
		return "it has no usable repository name"
	}
	label, err := gcp.RepoLabel(slug)
	if err != nil {
		return "it has no usable repository name"
	}
	tg.slug, tg.label, tg.provider = slug, label, provider
	gitSecret, _ := infra.GitSecret(provider) // the provider is github or bitbucket here
	tg.wanted = []string{gitSecret}
	if cfg == nil {
		tg.noConfig = true
	} else {
		// The Anthropic key is asked for only when a run actually needs it:
		// with the gateway on and every pin served by a provider the run never
		// calls Anthropic, so init does not prompt for a value and does not
		// store one (design docs/project-centric/spec.md §6.1). The rule is
		// needsAnthropicKey (config.AnthropicKeyNeeded over the effective
		// mode), the one validate and update --repo ask too.
		needsKey := needsAnthropicKey(lc, cfg, repo)
		// Whether any run of this project holds the unsuffixed default
		// account: when none does, the bare token is a name nothing of the
		// owner's mounts, and asking for it here would store the one
		// repository copy no project-wide copy can replace (the name is
		// deliberately outside D2's sweep). Readiness drops the same mount and
		// SecretCommands prints no step for it, so the three readers answer
		// one way.
		runsDefault := lc.RunsOnDefaultClaudeAccount()
		for _, name := range slices.Sorted(maps.Keys(cfg.Workflows)) {
			// The default account only: Task 5 extends this to every name
			// config.AllowedClaudeAccounts(lc.ClaudeAccounts, repo) allows,
			// so init also prompts for each named account's own token.
			mounts, err := infra.SecretMounts(gitSecret, cfg, name, config.DefaultClaudeAccount, cfg.Workflows[name], lc, repo)
			if err != nil {
				return "the secrets its fugaro.yaml declares cannot be worked out: " + oneLine(err.Error())
			}
			for _, m := range allowedHarnessOAuthMounts(lc, repo, mounts) {
				if m.Logical == config.ClaudeOAuthToken && !runsDefault {
					continue
				}
				if m.Logical == config.AnthropicSecret && !needsKey {
					// No call of this run goes to Anthropic, so there is
					// nothing to ask for: the run never calls it (design
					// docs/project-centric/spec.md §6.1). The container is
					// still planned, so the owner can store a key later with
					// `fugaro secrets set`, and a run that needs one without
					// it is refused with that command.
					continue
				}
				if !slices.Contains(tg.wanted, m.Logical) {
					tg.wanted = append(tg.wanted, m.Logical)
				}
			}
		}
		// Every named Claude account this repository may choose (beyond the
		// bare default, which SecretMounts above already asked for when
		// agent.auth is oauth) gets its own entry, so init asks for every
		// account the owner has allowed, not just the one SecretMounts
		// already knows about per workflow; the line it leaves for one of
		// them is that account's own `fugaro accounts add` command (Left),
		// not the default's mint-then-store pair (design
		// docs/named-oauth-tokens/spec.md §3 T3, §4). A name the credentials
		// block declares a key credential is no such account
		// (config.ClaudeAccountTokenNames): it has no
		// claude-oauth-token-<name>, and the command the entry would leave the
		// owner is one `accounts add` refuses for it — a stage waiting on a
		// value nothing could store, under a name no command of the line
		// writes.
		if cfg.Agent.Auth == "oauth" {
			accounts := config.ClaudeAccountTokenNames(lc.Credentials,
				config.AllowedClaudeAccounts(lc.ClaudeAccounts, repo))
			for _, n := range accounts {
				if n == config.DefaultClaudeAccount {
					continue
				}
				if secret := config.ClaudeAccountSecret(n); !slices.Contains(tg.wanted, secret) {
					tg.wanted = append(tg.wanted, secret)
				}
			}
		}
	}
	return ""
}

// enrolledRepo is whether the project's own record names this repository:
// this machine's local config, an origin the run's own confirmation has
// already covered (the gate's own answer), or the published policy's
// allow-list -- the admits I1's reader expands into the list, which count as
// enrolled for this stage's writes (D3350, R20).
//
// The list door trusts e.repos: once the reader fills the list, every view in
// it is enrolled by construction. This is what tells the working directory's
// checkout, which C1's stand-in still answers the UPDATE door with, from one
// the project has: the door re-converges what is enrolled (D5) and enrols
// none, so such a repository is left for the user with the command that does.
func (s *secretsStage) enrolledRepo(tg *secretTarget) bool {
	o := originOf(tg)
	if a, err := s.e.authState(o); err == nil && a == authOK {
		return true
	}
	return s.node != nil && projectpolicy.AdmitRepo(s.node.AllowRepos, repoSortKey(tg.repo))
}

// originOf is the origin a list-door target's repository stands for: the
// owner/name the reader answered, and the host that provider's repositories
// live on. The door's gate reads nothing else of it -- there is no checkout
// here to read.
func originOf(tg *secretTarget) originInfo {
	return originInfo{Repo: tg.repo, Host: providerHosts[tg.provider]}
}

// scopeTarget drops from one repository's wanted names the two the stage does
// not take, once the published node is in hand.
//
// A repository-level secret (D2313) is one no shared job mounts, so it is not
// a name this stage may take. The ruling closed the stores that would write
// one — both repository-scope forms of `fugaro secrets set` answer its notice
// and write nothing — and the only store a prompt here could write is that
// same repository container, through this stage's own Secret Manager write
// rather than the command's: leaving the name in would make one prompt the way
// around the ruling, and find() would then count the copy as covering the name
// and never ask again, so the stage's "stored" would be the last word over a
// name no shared job carries a variable for. The value a repository's OWN job
// still mounts (a repository a profile does not admit yet keeps one until M2
// retires it) is the one already in the container, which the ruling leaves
// where it is: what it may not get is a new value, and that is what this
// drops.
//
// The commands that would store one answer the notice too, so there is no line
// to leave either. The name is therefore dropped here, once, before anything
// asks: what a person is told about it is the ruling's own sentence, which
// Check and Plan print (config.RepoSecretsIgnoredNotice).
//
// A name this repository may mount the project's copy of (projectScope) is
// kept: that copy IS mounted, and the stage's job for such a name is
// unchanged. That is why this sits after the node is read and not beside the
// mounts it filters: projectScope reads the node.
func (s *secretsStage) scopeTarget(tg *secretTarget) {
	lc := s.e.lc
	kept := make([]string, 0, len(tg.wanted))
	for _, name := range tg.wanted {
		// A configured model provider's own key is one of the names an
		// api-key job carries (infra.SecretMounts), and since KS2 (D1670
		// reversed by D2975) it is entered in the credentials service's
		// bundle, one entry per repository (D3008), rather than stored in a
		// container: take() refuses the name before reading anything. A want
		// left in here is one no run could ever satisfy -- find() counts
		// containers, so the entry that command writes never cleared it, and
		// every `fugaro setup` and `fugaro update` at a terminal ended on the
		// refusal. It is dropped here, once, before anything asks, and what a
		// person is told about it is where the key is entered
		// (providerKeyNotTakenNotice).
		if _, provider := config.ProviderBySecret(lc.Providers, name); provider {
			tg.providerKeys = append(tg.providerKeys, name)
			continue
		}
		if repoSecretsDisabledName(lc, name) && !s.scopeIn(tg, name) {
			tg.repoLevel = append(tg.repoLevel, name)
			continue
		}
		kept = append(kept, name)
	}
	tg.wanted = kept
}

// defaultBranchConfig is the fugaro.yaml of the checkout's default branch
// (origin/HEAD, main or master, as last fetched), read from git; nil with none
// or one that does not parse.
func defaultBranchConfig(ctx context.Context, root string) *config.Config {
	return defaultBranchParse(ctx, root).cfg
}

// authorize is the hostile-checkout gate (init_repo_gate.go) in front of the
// stage's cloud calls and prompts: a repository the local config does not
// list takes a value for nobody until it is opted in, by --onboard-repo or by
// typing its owner/name at the user's own terminal. A checkout whose default
// branch names this project is no proof of anything.
// The update door asks nothing (D3350): e.repos is the project's own list and
// an admit-only repository in it is enrolled, so every repository the stage
// plans for is one the project already admits and a container may be created
// for it. The gate is the checkout door's, and the guard below is the whole of
// that rule: a list-door run reaches this too, under --ask, where
// singleCheckout let Apply fall through (U3b), and the gate would stop the run
// on a repository the project's own list admits.
func (s *secretsStage) authorize(env initflow.Env) error {
	if s.listDoor() {
		return nil
	}
	switch a, err := s.e.authState(s.origin); {
	case err != nil:
		return err
	case a == authNeeded && !s.e.r.o.enrolling():
		// A door that does not enrol a repository by asking: one line naming
		// the door that does, and never --onboard-repo, which is not this
		// run's to offer (D5). Without this arm the secrets stage would offer
		// the flag for a step its door does not ask about, while the plugin
		// and repository stages of the same run name `fugaro setup` for the
		// same repository (W5.1).
		return &initflow.NeedsYouError{Left: enrolLeft(s.e.r.o.doorName())}
	case a == authNeeded || (a == authAsk && !env.Interactive):
		return &initflow.NeedsYouError{Left: onboardLeft(s.origin, s.e.r.o.doorName())}
	case a == authAsk:
		ok, err := s.e.confirmRepo(s.root, s.origin)
		if err != nil {
			return err
		}
		if !ok {
			return &initflow.NeedsYouError{Left: onboardLeft(s.origin, s.e.r.o.doorName())}
		}
	}
	return nil
}

// projectCandidate is the one function this stage asks where a name belongs:
// infra.ProjectSecretCandidateFromNode, with the repository's provider and the
// published node the stage read when it resolved. Both the store decision
// (projectScope) and the "is it covered" check (find) read it, so the stage can
// never store a value somewhere the check above would not look for it, and the
// plan's own mount decision is the same answer.
func (s *secretsStage) projectCandidate(name string) (string, bool) {
	return s.candidateIn(s.tg, name)
}

// candidateIn is projectCandidate for one repository of the run's own list:
// the update door asks it once per repository, so a name the node admits for
// one of them and not another is answered for the repository in hand.
func (s *secretsStage) candidateIn(tg *secretTarget, name string) (string, bool) {
	return infra.ProjectSecretCandidateFromNode(s.e.lc, name, tg.repo, tg.provider, s.node)
}

// projectScope is where this stage stores name: the project's own container
// when the plan may mount a project copy for *this repository*, this
// repository's own otherwise.
//
// It is projectCandidate, the one function that answers eligibility, and the
// same answer find() reads to call a name covered -- so the stage can never
// store a value somewhere the check above would not look for it. The
// repository is the argument that matters (code review, high): a repo-unaware
// test -- "project_secrets lists someone, so the name has a project-wide
// meaning" -- called the name project-scope for a repository the list does not
// name, and `init` on that repository then wrote its value over the shared copy
// another repository mounts, a copy it may not mount itself, and Verify failed.
// "Absent or empty means nobody" decides, and it decides for the repository in
// hand.
//
// The published node is the second source, and it is the one SD17 adds: with
// no project_secrets.bitbucket-token entry in this machine's local config, the
// allow-list the owner published is what says whether this repository may
// mount the project's copy of the token. It answers for that one name alone,
// for a Bitbucket repository, and only where the local config holds no entry
// of its own (infra.ProjectSecretCandidateFromNode).
func (s *secretsStage) projectScope(name string) bool {
	return s.scopeIn(s.tg, name)
}

// scopeIn is projectScope for one repository of the run's own list.
func (s *secretsStage) scopeIn(tg *secretTarget, name string) bool {
	_, ok := s.candidateIn(tg, name)
	return ok
}

// projectScopeLocal is the same question asked of the local config alone, which
// is what Left() spells its command from: the line it leaves is a `fugaro
// secrets set` invocation, and that command's scope decision is its own
// (projectScopeName, the local config's) -- the node is read by update --repo,
// which is the run this stage is part of, and not by `secrets set`. A line
// spelled from projectScope would then name a command that stores somewhere
// else than the sentence above it, and on a machine whose node admits the
// repository with no project value stored yet it would store a repository copy
// that overrides the project one forever.
func (s *secretsStage) projectScopeLocal(name string) bool {
	return s.scopeLocalIn(s.tg, name)
}

// scopeLocalIn is projectScopeLocal for one repository of the run's own list.
func (s *secretsStage) scopeLocalIn(tg *secretTarget, name string) bool {
	_, ok := infra.ProjectSecretCandidate(s.e.lc, name, tg.repo)
	return ok
}

// find lists the repository's secrets (metadata only) and returns what its
// jobs mount that has no value yet.
//
// A name the project's own config -- or, for the Bitbucket token, the project's
// published policy -- allows this repository to use project-wide
// (projectCandidate: the reserved names through project_secrets, a named Claude
// account's or a model provider's through its own allow_data_to, the token
// through the node's bitbucket_token block) is not missing when the
// installation's copy has a version, whatever the repository's own copy holds:
// mounting resolves the logical name to that copy (infra.ResolveSecretID, a
// repository copy with a version wins), so a project-covered name needs nothing
// typed. Prompting for it anyway would store a repository copy that then wins
// forever -- a silent precedence flip, and exactly the re-entry project-wide
// secrets exist to remove; without a terminal it would block the init on
// NeedsYou.
//
// The project-wide listing is read only when some wanted name could be
// covered by one (the local config has no project_secrets, no allowed Claude
// account and no allowed provider otherwise, which is most installations), so
// it costs an IAM-covered call only where it can change the answer.
func (s *secretsStage) find(ctx context.Context, st secretStore) ([]string, error) {
	return s.findIn(ctx, &secretLister{st: st, project: s.e.lc.Name}, s.tg)
}

// secretLister is one listing of Secret Manager's metadata for a run: every
// repository's names, and the project's own listing read at most once, so the
// update door's fan-out costs one project-wide call however many repositories
// want a project-covered name.
type secretLister struct {
	st         secretStore
	project    string
	projectOut []gcp.SecretInfo
	projectRun bool
}

// projectList is the installation's own containers, read once.
func (l *secretLister) projectList(ctx context.Context) ([]gcp.SecretInfo, error) {
	if !l.projectRun {
		var err error
		if l.projectOut, err = l.st.List(ctx, map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProject: l.project}); err != nil {
			return nil, err
		}
		l.projectRun = true
	}
	return l.projectOut, nil
}

// findIn is find for one repository: the update door's fan-out asks it once
// per repository, over the run's one secretLister.
func (s *secretsStage) findIn(ctx context.Context, l *secretLister, tg *secretTarget) ([]string, error) {
	list, err := l.st.List(ctx, map[string]string{gcp.LabelRepo: tg.label})
	if err != nil {
		return nil, err
	}
	var project []gcp.SecretInfo
	if slices.ContainsFunc(tg.wanted, func(name string) bool {
		return s.scopeIn(tg, name)
	}) {
		if project, err = l.projectList(ctx); err != nil {
			return nil, err
		}
	}
	has := func(list []gcp.SecretInfo, id string) bool {
		return slices.ContainsFunc(list, func(i gcp.SecretInfo) bool { return i.ID == id && i.Latest != "" })
	}
	var out []string
	for _, name := range tg.wanted {
		if has(list, gcp.SecretID(tg.slug, name)) {
			continue
		}
		if id, ok := s.candidateIn(tg, name); ok && has(project, id) {
			continue
		}
		out = append(out, name)
	}
	return out, nil
}

func describeNeeds(names []string) string {
	return "no value stored for " + strings.Join(names, ", ")
}

// providerKeyNotTakenNotice is what the stage says about the names resolve
// dropped because they are a configured model provider's own key
// (config.ProviderBySecret). A provider key has been the credentials service's
// bundle's since KS2 (D1670 reversed by D2975), one entry per repository
// (D3008), and this stage writes Secret Manager values, so the names are not
// its to take. The command it names per name is the one take()'s backstop
// refusal names -- `fugaro secrets set <name> --repo <repo>`, which writes THIS
// repository's entry -- because the bare form stores the project's copy of a
// name this repository's own key is not.
func providerKeyNotTakenNotice(names []string, repo string) string {
	printable := make([]string, 0, len(names))
	cmds := make([]string, 0, len(names))
	for _, name := range names {
		printable = append(printable, pluginwire.Printable(name))
		cmds = append(cmds, "`"+selfCommand()+" secrets set "+pluginwire.Printable(name)+" --repo "+pluginwire.Printable(repo)+"`")
	}
	return fmt.Sprintf(
		"a model provider's key (%s) is entered in the credentials service's bundle, one entry per repository, and not stored by this step, which writes Secret Manager values: enter this repository's own entry with %s in your own terminal, and run it once more for each further repository the key is for (KS2, D1670 reversed, D3008)",
		strings.Join(printable, ", "), strings.Join(cmds, ", "))
}

// Check lists which secrets have no version, by Secret Manager metadata. A
// listing that fails (before the installation exists, for one) is not an
// error here: Apply lists again and says what it needs.
func (s *secretsStage) Check(ctx context.Context) (initflow.Status, error) {
	s.resolve(ctx)
	if s.list != nil {
		return s.checkList(ctx)
	}
	if s.skip != "" {
		return initflow.Status{State: initflow.Skipped, Detail: s.skip}, nil
	}
	// Before any cloud call: a repository the project does not list yet is
	// not asked about, not even for metadata. At a terminal the typed opt-in
	// comes when the stage runs; until then nothing is read.
	switch a, err := s.e.authState(s.origin); {
	case err != nil:
		return initflow.Status{}, err
	case a == authNeeded && !s.e.r.o.enrolling():
		// As authorize: the door that does not ask names the door that does,
		// with the same line the plugin and repository stages leave (W5.1).
		lf := enrolLeft(s.e.r.o.doorName())
		return initflow.Status{State: initflow.NeedsYou, Detail: unknownDetailNotEnrolled(s.e.lc.Name, s.origin, s.e.r.o.doorName()), Left: &lf}, nil
	case a == authNeeded:
		lf := onboardLeft(s.origin, s.e.r.o.doorName())
		return initflow.Status{State: initflow.NeedsYou, Detail: unknownDetail(s.e.lc.Name, s.origin, convergeOptIn(s.e.lc, s.origin)), Left: &lf}, nil
	case a == authAsk:
		s.checked = false
		return initflow.Status{State: initflow.Todo, Detail: "which secrets are stored is read after you confirm the repository " + pluginwire.Printable(s.origin.typed())}, nil
	}
	st, err := s.open(ctx)
	if err == nil {
		s.missing, err = s.find(ctx, st)
	}
	if err != nil {
		s.checked = false
		return initflow.Status{State: initflow.Todo, Detail: "which secrets are stored is read when it runs"}, nil
	}
	s.checked = true
	// The names the stage does not take (resolve dropped them) are said in the
	// ruling's own sentence (D2313) and with the command that enters this
	// repository's entry (KS2, a configured provider's own key): no line is
	// left for either, so this is the whole of what a person learns about them
	// here, and the only place the remedy is named.
	note := s.targetNote(s.tg)
	if len(s.missing) == 0 {
		return initflow.Status{State: initflow.Done, Detail: "the secrets the jobs mount are stored" + note}, nil
	}
	detail := describeNeeds(s.missing) + note
	if !s.canPrompt() {
		if m := agentMarker(os.Getenv, s.e.r.stdinTerminal()); m != "" {
			detail += "; a coding agent's session is present (" + m + " is set), so " + initflow.NeedsTerminal("typing the values") + "; run " + selfCommand() + " secrets set in your own terminal window"
			if h := initflow.IDEHint(m); h != "" {
				detail += " (" + h + ")"
			}
		} else {
			detail += "; " + initflow.NeedsTerminal("typing the values")
		}
		lf := s.Left()
		return initflow.Status{State: initflow.NeedsYou, Detail: detail, Left: &lf}, nil
	}
	return initflow.Status{State: initflow.Todo, Detail: detail + "; asks for each at a hidden prompt"}, nil
}

// checkList is the update door's Check: every repository of the list is read
// and gets one line -- what the stage did for it, or why it could not. The
// stage asks for a value only under --ask (U3b); until then a repository with
// a missing name is left with the commands that store it, which is what the
// stage's Left carries.
//
// A repository the reader could not read, one the stage does not apply to and
// one the project does not have are lines and not stops: the door's other
// stages report them the same way, and the run goes on past them. Such a
// repository is drawn before the store is opened and may be the whole list, so
// a run that has no repository to list reads no store at all. The stage is
// needs-you when a repository is missing a name or is not the project's, done
// when none is, and skipped when no repository could be worked on at all.
func (s *secretsStage) checkList(ctx context.Context) (initflow.Status, error) {
	var lines []initflow.RepoLine
	var needs []string
	readable := 0
	var l *secretLister
	for _, tg := range s.list {
		switch {
		case tg.unread != "":
			tg.state, tg.detail = initflow.RepoUnread, tg.unread
		case tg.skip != "":
			tg.state, tg.detail = initflow.RepoSkipped, tg.skip
		case tg.enrol:
			tg.state, tg.detail = initflow.RepoNeedsYou, unknownDetailNotEnrolled(s.e.lc.Name, originOf(tg), s.e.r.o.doorName())
			needs = append(needs, pluginwire.Printable(tg.repo)+": "+tg.detail)
		default:
			if l == nil {
				st, err := s.open(ctx)
				if err != nil {
					// Before the installation exists, or without Secret
					// Manager, the listing cannot run: as the checkout door,
					// this is not the stage's error but what Apply says again.
					return initflow.Status{State: initflow.Todo, Detail: "which secrets are stored is read when it runs"}, nil
				}
				l = &secretLister{st: st, project: s.e.lc.Name}
			}
			readable++
			missing, err := s.findIn(ctx, l, tg)
			if err != nil {
				return initflow.Status{State: initflow.Todo, Detail: "which secrets are stored is read when it runs"}, nil
			}
			tg.missing, tg.checked = missing, true
			note := s.targetNote(tg)
			if len(missing) == 0 {
				tg.state, tg.detail = initflow.RepoDone, "the secrets the jobs mount are stored"+note
			} else {
				tg.state, tg.detail = initflow.RepoNeedsYou, describeNeeds(missing)+note
				needs = append(needs, pluginwire.Printable(tg.repo)+": "+describeNeeds(missing))
			}
		}
		lines = append(lines, newRepoLine(tg.repo, tg.state, tg.detail))
	}
	s.reportList(lines)
	switch {
	case len(needs) > 0 && s.singleCheckout(ctx, s.terminal()):
		// The list's one repository is this checkout and a person is at the
		// terminal, so the value is taken at the prompt, as `fugaro update`
		// has always taken it (R21). A needs-you here is a check the loop does
		// not apply, and the value would never be taken -- the door's own line
		// would leave the run asking itself. What is missing is said by the
		// lines above, one per repository; this is the same sentence the
		// checkout door's check gives for its prompt.
		return initflow.Status{State: initflow.Todo, Detail: strings.Join(needs, "; ") + "; asks for each at a hidden prompt"}, nil
	case len(needs) > 0:
		lf := s.Left()
		return initflow.Status{State: initflow.NeedsYou, Detail: strings.Join(needs, "; "), Left: &lf}, nil
	case readable == 0:
		return initflow.Status{State: initflow.Skipped, Detail: "no enrolled repository is one this stage takes: each line says why"}, nil
	}
	return initflow.Status{State: initflow.Done, Detail: "the secrets the jobs mount are stored"}, nil
}

// targetNote is the one-report note about a repository: what the stage does
// not take for it (D2313's repository-level names, a configured provider's own
// key), and the git credential's own case of a file the reader found none.
func (s *secretsStage) targetNote(tg *secretTarget) string {
	note := ""
	if tg.noConfig {
		note = " (no fugaro.yaml on the default branch as last fetched, so the Claude credential is not assumed; git fetch, then rerun)"
	}
	if len(tg.repoLevel) > 0 {
		note += "; " + config.RepoSecretsIgnoredNotice(tg.repoLevel)
	}
	if len(tg.providerKeys) > 0 {
		note += "; " + providerKeyNotTakenNotice(tg.providerKeys, tg.repo)
	}
	return note
}

// reportList records the stage's lines once per run, whatever the loop asks
// (Check, Plan and Apply all reach it): the run's own record holds one line
// per repository, not three.
func (s *secretsStage) reportList(lines []initflow.RepoLine) {
	if s.linesReported {
		return
	}
	s.linesReported = true
	s.lines = lines
	if len(lines) > 0 {
		s.e.reportRepos(initflow.Secrets, lines)
	}
}

// RepoLines is what the stage reported about each repository, which the loop
// carries into the stage's result and its event (R12, U13).
func (s *secretsStage) RepoLines() []initflow.RepoLine { return slices.Clone(s.lines) }

func (s *secretsStage) Plan(ctx context.Context, _ initflow.Env) (initflow.Plan, error) {
	st, err := s.Check(ctx)
	if err != nil {
		return initflow.Plan{}, err
	}
	return initflow.Plan{Detail: st.Detail, NothingToDo: st.State == initflow.Done}, nil
}

// Left is the one line the user runs in their own terminal: each missing
// secret's command (all the candidates when the listing did not work),
// each on a line of its own. None holds a value: the value is typed at the hidden
// prompt, or the PEM is redirected from a file the user names.
func (s *secretsStage) Left() initflow.Left {
	if s.list != nil {
		return s.leftList()
	}
	if s.tg == nil {
		return initflow.Left{Stage: initflow.Secrets, Kind: initflow.LeftCommand, Text: "in a checkout of the repository, in your own terminal window, run", Commands: []string{selfCommand() + " " + s.e.r.o.doorName()}}
	}
	needs := s.missing
	if !s.checked {
		needs = s.tg.wanted
	}
	return initflow.Left{Stage: initflow.Secrets, Kind: initflow.LeftCommand, Text: s.leftText(), Commands: s.leftCommands(s.tg, needs, nil)}
}

// leftList is the update door's Left: the commands of every readable
// repository's missing names, one line per name and repository. A name several
// repositories want is one line when the command that stores it is the
// project's own copy -- the bare `fugaro secrets set <name>`, which IS4's
// project-wide workflow secret is and which every repository mounting it reads
// -- and one line per repository otherwise, because `secrets set` names one
// repository at a time (R2).
//
// A repository the project does not have is the door's own line, and it comes
// first: `fugaro setup` is the command that enrols it, and the stores above
// are the rest of what the user has to do (enrolledRepo).
func (s *secretsStage) leftList() initflow.Left {
	var cmds []string
	for _, tg := range s.list {
		if !tg.read() {
			continue
		}
		needs := tg.missing
		if !tg.checked {
			needs = tg.wanted
		}
		cmds = s.leftCommands(tg, needs, cmds)
	}
	if s.enrols() {
		lf := enrolLeft(s.e.r.o.doorName())
		lf.Commands = append(lf.Commands, cmds...)
		return lf
	}
	return initflow.Left{Stage: initflow.Secrets, Kind: initflow.LeftCommand, Text: s.leftText(), Commands: cmds}
}

// enrols is whether any repository of the list is one this project's record
// does not name, which is the one line the door leaves for the user itself.
func (s *secretsStage) enrols() bool {
	return slices.ContainsFunc(s.list, func(tg *secretTarget) bool { return tg.enrol })
}

// leftText is what the lines above are for, in one sentence (and, in a coding
// agent's session, the one word that keeps a value out of it).
func (s *secretsStage) leftText() string {
	if agentMarker(os.Getenv, s.e.r.stdinTerminal()) != "" {
		return "run these in your own terminal window, not through a coding agent, one line at a time, in this order"
	}
	return "run these in your own terminal window, one line at a time, in this order"
}

// leftCommands appends the commands that store tg's names to cmds and answers
// the whole slice: one repository of the run's list is one call of this, in the
// canonical order, over the one slice every line is deduplicated in -- a name
// several repositories want is one line when the command that stores it is the
// project's own, and one per repository when `secrets set` has to name one
// (R2).
func (s *secretsStage) leftCommands(tg *secretTarget, needs []string, cmds []string) []string {
	add := func(lines ...string) {
		for _, line := range lines {
			if !slices.Contains(cmds, line) {
				cmds = append(cmds, line)
			}
		}
	}
	for _, name := range needs {
		// The scope the command stores to is projectScopeLocal's, which is the
		// scope `secrets set` itself decides: the line below is that command,
		// and the node this stage read is update --repo's, not `secrets set`'s.
		//
		// A name only the node makes project-wide is the one place the two
		// answers disagree, and `secrets set` cannot store that copy on this
		// machine at all: with no entry of the local config's own,
		// projectWideAccepted refuses it --project-wide, and its bare form
		// writes the repository's own copy -- the copy that then wins over the
		// project's from then on, which is the flip find() refuses to prompt a
		// covered name for. The line left for it is the run's own door instead:
		// at a terminal that door's prompt is take's, which stores the value in
		// the container this stage's own decision picks.
		if s.scopeIn(tg, name) && !s.scopeLocalIn(tg, name) {
			add(selfCommand() + " " + s.e.r.o.doorName())
			continue
		}
		set := selfCommand() + " secrets set " + quoteWord(name)
		if !s.scopeLocalIn(tg, name) {
			// The line is a repository-scope store, and a name D2313 disables
			// never reaches it: no shared job mounts a repository-level secret,
			// so such a name is not one this stage wants at all (resolve drops
			// it, and Check prints the ruling's sentence for it). What is left
			// here has a repository copy a run really mounts.
			set += " --repo " + quoteWord(tg.repo)
		}
		switch {
		case name == config.ClaudeOAuthToken:
			add("claude setup-token", set)
		case strings.HasPrefix(name, config.ClaudeOAuthTokenPrefix):
			// A named account's token is the one command that declares the
			// account, stores the token where the shared job mounts it and
			// converges the installation (spec accounts-add §8): the pair the
			// default account above keeps is `claude setup-token` plus a
			// `secrets set` the owner has to scope by hand, and for a named
			// account both halves are `accounts add`'s own. "default" is
			// reserved, so the command below is the only spelling and the
			// default account has none.
			account := strings.TrimPrefix(name, config.ClaudeOAuthTokenPrefix)
			// The profile whose claude_accounts already lists the account: it
			// leaves that half of the command a no-op. Without it the command
			// stops in a project with several profiles (accountsAddProfile), and
			// it would otherwise take the only one there is — serving the account
			// from a profile that did not, and deploying a job — where storing
			// the token was the whole step.
			profile := config.ClaudeAccountProfile(s.e.lc.Profiles, account)
			if profile == "" {
				// No profile serves the account, and the account is one this
				// config already declares (config.AllowedClaudeAccounts above),
				// so `accounts add` would have nothing to declare and only a
				// profile to choose for itself: the pair that mints the token and
				// stores it in the container such a job would mount is the whole
				// step, as it was before `accounts add` took it over.
				add("claude setup-token", set)
				break
			}
			// --repos is this checkout's own repository, which is the one the
			// command is about: accountsAddRepos defaults it to the origin of
			// whatever checkout the line is pasted into, so the same line run
			// from another checkout would bind the account to that repository
			// instead of this one — and the widening the confirmation is meant
			// to state would be measured against that checkout too, so it would
			// say nothing. It is a union with the list the account already has
			// (accountsAddWhat reads it back from the config the command would
			// write), so naming this repository cannot narrow it.
			add(selfCommand() + " accounts add " + quoteWord(account) +
				" --repos " + quoteWord(tg.repo) + " --profile " + quoteWord(profile))
		case name == multilineSecret:
			add(set + " < PATH-TO-THE-KEY-FILE")
		default:
			add(set)
		}
	}
	return cmds
}

// terminal is stdin as a real terminal file, else nil. The stage reads
// nothing from any other stdin, whatever stdinIsTerminal says.
func (s *secretsStage) terminal() *os.File {
	if f, ok := s.e.r.cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		return f
	}
	return nil
}

// smProblem turns a refusal by Secret Manager (permission denied, the API
// not enabled) into what the user does about it: not a failure of the run,
// and never carrying the server's text (it could echo a payload).
func (s *secretsStage) smProblem(err error) *initflow.NeedsYouError {
	var ae *googleapi.Error
	if !errors.As(err, &ae) || ae.Code != 403 {
		return nil
	}
	text := "Secret Manager refused: you need roles/secretmanager.viewer to list, secretmanager.secrets.create to create a secret's container (roles/secretmanager.admin or a custom role) and roles/secretmanager.secretVersionAdder to store a value, on the project's secrets (ask the project owner), then rerun " + selfCommand() + " " + s.e.r.o.doorName() + ""
	if m := strings.ToLower(ae.Message); strings.Contains(m, "has not been used") || strings.Contains(m, "is disabled") || strings.Contains(m, "service_disabled") {
		text = "the Secret Manager API (secretmanager.googleapis.com) is not enabled on the project: enable it (the owner's fugaro setup does), then rerun " + selfCommand() + " " + s.e.r.o.doorName() + ""
	}
	return &initflow.NeedsYouError{Left: initflow.Left{Stage: initflow.Secrets, Kind: initflow.LeftConsole, Text: text}}
}

func (s *secretsStage) apiErr(err error) error {
	if ny := s.smProblem(err); ny != nil {
		return ny
	}
	return remote(err)
}

func (s *secretsStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	s.resolve(ctx)
	if err := s.e.newMachineGuard(initflow.Secrets); err != nil {
		return initflow.Outcome{}, err
	}
	f := s.terminal()
	if s.list != nil && !s.singleCheckout(ctx, f) {
		// The update door asks for no value here: singleCheckout is false
		// without --ask (canPrompt gates it, D3347/R18), where a name it
		// cannot read or store is left with the command that does, and false
		// for a list of many even under the flag until I1's reader lands.
		// Apply therefore re-reads the list and answers the loop with it --
		// nothing to do where every name is stored, and the lines otherwise.
		st, err := s.checkList(ctx)
		if err != nil {
			return initflow.Outcome{}, err
		}
		if st.State == initflow.Done || st.State == initflow.Skipped {
			return initflow.Outcome{Detail: "No changes"}, nil
		}
		return initflow.Outcome{}, &initflow.NeedsYouError{Left: s.Left()}
	}
	if s.skip != "" || s.tg == nil || !env.Interactive || !s.canPrompt() || f == nil {
		return initflow.Outcome{}, &initflow.NeedsYouError{Left: s.Left()}
	}
	if err := s.authorize(env); err != nil {
		return initflow.Outcome{}, err
	}
	st, err := s.open(ctx)
	if err != nil {
		return initflow.Outcome{}, s.apiErr(err)
	}
	needs, err := s.find(ctx, st)
	if err != nil {
		return initflow.Outcome{}, s.apiErr(err)
	}
	s.missing, s.checked = needs, true
	if len(needs) == 0 {
		return initflow.Outcome{Detail: "No changes"}, nil
	}
	w := s.out()
	for _, name := range needs {
		// The prompt is read from the terminal file itself and printed where it
		// always was: the stage's own writer, which is stderr, because what a
		// run prints about a secret is not part of its account. The run's reader
		// cannot put the step's line back for it, so the line in flight is taken
		// down around the whole question and drawn again once the value is in
		// (initRun.asking, G1): the spinner and the prompt are read on the same
		// screen, whichever stream each one goes to.
		if err := s.e.r.asking(func() error { return s.take(ctx, st, f, w, name) }); err != nil {
			return initflow.Outcome{}, err
		}
		s.stored = append(s.stored, name)
	}
	return initflow.Outcome{Changed: true, Detail: "stored " + strings.Join(s.stored, ", ")}, nil
}

// take prompts for one secret, validates it and stores it. The value is
// held only inside this call, with core dumps off while it is.
func (s *secretsStage) take(ctx context.Context, st secretStore, f *os.File, w io.Writer, name string) error {
	if strings.HasPrefix(name, config.ClaudeOAuthTokenPrefix) && !claudeOAuthAccountAllowed(s.e.lc.ClaudeAccounts, name, s.tg.repo) {
		// The resolve()-time filter above already keeps such a name out of
		// tg.wanted; this is the independent backstop that never reads or
		// stores a value for one even if that filter were ever wrong or
		// skipped (defense in depth, review finding).
		return claudeOAuthAccountRefusal(name, s.tg.repo)
	}
	if config.CodexOAuthSecret(name) {
		// The same independent backstop for the Codex harness's reserved
		// names (config.CodexOAuthToken, owner ruling of 2026-10-18):
		// allowedHarnessOAuthMounts keeps them out of tg.wanted, and this
		// refuses one even if that filter were ever wrong or skipped, before
		// any value is read.
		return codexOAuthRefusal(name)
	}
	if p, reserved := config.ReservedProviderSecret(s.e.lc.Providers, name); reserved && s.e.lc.Providers[p].Secret != name {
		// And the same for the name reserved for a configured provider's key
		// while the provider's key is stored under another one
		// (config.ReservedProviderSecret): the value would become that
		// provider's credential the day the key is renamed to the convention.
		return providerConventionRefusal(p, name)
	}
	if p, provider := config.ProviderBySecret(s.e.lc.Providers, name); provider {
		// A configured provider's key has been the credentials service's
		// bundle's since KS2 (R2, D1670 reversed by D2975), and this stage
		// writes a container of whatever name it is handed. Taking one here
		// would make a per-repository copy the service never reads — the one
		// writer left that still makes a container for a provider key — and it
		// would read the value at this prompt where every writer of the bundle's
		// entry reads it from stdin. So it is refused, before a value is read,
		// with the command that writes this repository's own entry named
		// (Left() would hand the bare one, which stores the project's copy of a
		// name this repository's own key is not). resolve() drops every such
		// name before anything asks, and says the same command for it
		// (providerKeyNotTakenNotice); this is the independent backstop that
		// never stores one even if that drop were ever wrong or skipped, and it
		// is why the two must name the same remedy.
		return providerKeyInitRefusal(p, name, s.tg.repo)
	}
	defer noCoreDumps()()
	var (
		value []byte
		err   error
	)
	// The prompt is where a person learns the blast radius (plan Task 2.1
	// step 4): a name this repository may mount project-wide is stored
	// project-wide -- D2's default for `secrets set <name>` with no flag.
	//
	// The way out names a command, never a flag of this one (code review,
	// low): the stage has no --repo, `fugaro update --repo` never runs it, and
	// the command Left() hands the user for the same name is the bare one.
	// `secrets set <name> --repo <owner/name>` is the command that stores this
	// repository's own copy, and it is run in the user's own terminal like
	// every other store -- here, before answering, since a value typed at
	// this prompt goes to the project's container.
	//
	// The reach is projectReach's answer, the same sentence `secrets set`'s own
	// note uses (code review, low): a fixed "every enrolled repository" is false
	// of a name whose project_secrets entry lists repositories, or whose
	// provider's allow_data_to does, and the reach is exactly what decides
	// whether storing the shared copy is safe. The prompt fires only where the
	// name has a project-wide meaning for this repository (projectScope), so
	// projectReach's "no repository can mount it" is not reachable here — which
	// holds for the new block too (workflow_secrets), and only because both
	// sides read it: projectScope asks ProjectSecretCandidate, and projectReach
	// asks projectSecretAllowed, whose workflow_secrets source names the entry's
	// own repositories. The two must move together: with the candidate reading
	// the block and the reach not, this prompt told a person storing a workflow
	// secret that no repository can mount it. The published node is the same
	// rule read the other way (SD17): projectScope takes it as a source, so
	// projectReach is handed it too, and the sentence names the node's list
	// where this machine's local config carries none.
	if s.projectScope(name) {
		// The way out is a repository-scope store, and for a repository-level
		// secret there is no such store left to offer (D2313): the command it
		// would name answers the notice and changes nothing, so it would hand a
		// person a step that does nothing. What it names instead is the store
		// that works and does store the same thing answering here stores — the
		// project's copy, in the user's own terminal rather than at this prompt
		// — which is the bare form Left() hands the same name for the same
		// reason.
		wayOut := fmt.Sprintf("for this repository's own copy instead, press Ctrl-C here and run '%s secrets set %s --repo %s' in your own terminal, then start this again",
			selfCommand(), quoteWord(name), quoteWord(s.tg.repo))
		if repoSecretsDisabledName(s.e.lc, name) {
			wayOut = fmt.Sprintf("its repository-level copy is disabled for now (D2313), so this project's copy is the only one it is stored in: answer here, or press Ctrl-C here and run '%s secrets set %s' in your own terminal, then start this again",
				selfCommand(), quoteWord(name))
		}
		fmt.Fprintf(w, "%s for %s on %s (stored project-wide in project %s's Secret Manager, where %s; %s):\n",
			name, pluginwire.Printable(s.origin.Repo), pluginwire.Printable(s.origin.Host), pluginwire.Printable(s.e.lc.GCPProject), projectReach(s.e.lc, name, s.node), wayOut)
	} else {
		fmt.Fprintf(w, "%s for %s on %s (stored in project %s's Secret Manager, in this repository's own copy):\n", name, pluginwire.Printable(s.origin.Repo), pluginwire.Printable(s.origin.Host), pluginwire.Printable(s.e.lc.GCPProject))
	}
	if isClaudeOAuthSecret(name) {
		fmt.Fprintln(w, name+": run claude setup-token in your own terminal first, then paste the token it prints")
	}
	if name == multilineSecret {
		value, err = readHiddenPEM(ctx, f, w, name)
	} else {
		if value, err = readHidden(ctx, f, w, name); err == nil {
			err = validateSecret(value, false)
		}
	}
	if err != nil {
		clear(value)
		return err
	}
	defer clear(value)
	release, err := s.e.hold(value)
	if err != nil {
		return err
	}
	defer release()

	// The same scope decision the prompt above announced, from the same
	// function, so the two cannot disagree.
	id := gcp.SecretID(s.tg.slug, name)
	labels := map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: s.tg.label, gcp.LabelSecret: name}
	if s.projectScope(name) {
		id = gcp.ProjectSecretID(s.e.lc.Name, name)
		labels = map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProject: s.e.lc.Name, gcp.LabelSecret: name}
	}
	if _, err := st.Set(ctx, id, value, labels); err != nil {
		if ny := s.smProblem(err); ny != nil {
			return ny
		}
		// A server may echo what it received, raw, base64 or JSON-escaped.
		esc, _ := json.Marshal(string(value))
		msg := agent.Redact(err.Error(), []string{string(value), base64.StdEncoding.EncodeToString(value), strings.Trim(string(esc), `"`)})
		msg = fmt.Sprintf("storing %s failed: %s", name, oneLine(msg))
		if errors.Is(err, gcp.ErrForeignSecret) {
			return userErr("%s; refusing to add a version to it", msg)
		}
		return remote(errors.New(msg))
	}
	if name == multilineSecret {
		// Typed just now: the App pre-check may use it from memory, with no
		// Secret Manager read (cleared when the run ends).
		clear(s.e.r.appKeyMem)
		s.e.r.appKeyMem = append([]byte(nil), value...)
	}
	fmt.Fprintf(w, "stored %s\n", name)
	return nil
}

// Verify lists again and checks each secret this run stored has a version.
func (s *secretsStage) Verify(ctx context.Context) error {
	if len(s.stored) == 0 {
		return nil
	}
	st, err := s.open(ctx)
	if err != nil {
		return s.apiErr(err)
	}
	needs, err := s.find(ctx, st)
	if err != nil {
		return s.apiErr(err)
	}
	if len(needs) > 0 {
		return remote(errors.New("stored " + strings.Join(s.stored, ", ") + ", but " + describeNeeds(needs)))
	}
	return nil
}

// readHiddenPEM reads a multi-line value (a PEM) pasted at the terminal
// without echo: the terminal goes raw once for the whole paste, before the
// prompt is shown (a line-by-line read would turn echo back on between lines
// and show what has already arrived). It ends at the "-----END" line once
// Enter is pressed. Ctrl-C cancels (with nothing stored) and Ctrl-D ends the
// input with the error "the input ended before the -----END line"; both
// store nothing and restore the terminal, as does a panic. Escape sequences
// (arrow keys, the bracketed-paste markers a terminal may wrap a paste in,
// SS3 keys such as ESC O P) are dropped. More than maxPEMLines lines is
// refused. The result has "\n" line ends and one trailing newline, and
// is validated like any value; the paste's first line must be a -----BEGIN
// line. Errors never quote the value. As readHidden, a cancelled read leaves
// its goroutine blocked, for a process that exits soon after.
func readHiddenPEM(ctx context.Context, f *os.File, prompt io.Writer, name string) ([]byte, error) {
	fd := int(f.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		return nil, userErr("reading the terminal's state: %v", err)
	}
	defer func() { _ = term.Restore(fd, state) }()
	fmt.Fprintf(prompt, "Paste the private key for %s (input hidden), from its -----BEGIN line to its -----END line, then press Enter: ", name)
	type result struct {
		b   []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		var all, line []byte
		buf := make([]byte, 4096)
		fail := func(err error) {
			clear(all)
			clear(line)
			clear(buf)
			done <- result{nil, err}
		}
		defer func() {
			if r := recover(); r != nil {
				_ = term.Restore(fd, state)
				fail(userErr("reading the value from the terminal failed"))
			}
		}()
		esc := 0 // 1 after ESC, 2 inside a CSI sequence, 3 after ESC O (an SS3 key: one more byte)
		lines := 0
		for {
			n, rerr := f.Read(buf)
			for _, c := range buf[:n] {
				switch {
				case esc == 1:
					switch c {
					case '[':
						esc = 2
					case 'O':
						esc = 3
					default:
						esc = 0
					}
					continue
				case esc == 3:
					esc = 0
					continue
				case esc == 2:
					if c >= 0x40 && c <= 0x7e { // the sequence's final byte
						esc = 0
					}
					continue
				case c == 0x1b:
					esc = 1
					continue
				case c == 3:
					fail(errCancelled)
					return
				case c == 4 && len(line) == 0:
					fail(userErr("the input ended before the -----END line"))
					return
				case c == '\r' || c == '\n':
					if len(line) == 0 {
						continue
					}
					if len(all) == 0 && !bytes.HasPrefix(line, []byte("-----BEGIN ")) {
						fail(userErr("the paste does not start with a -----BEGIN line"))
						return
					}
					if lines++; lines > maxPEMLines {
						fail(userErr("the paste has more than %d lines: nothing was stored; use fugaro secrets set github-app-key < PATH-TO-THE-KEY-FILE", maxPEMLines))
						return
					}
					all = append(append(all, line...), '\n')
					end := bytes.HasPrefix(line, []byte("-----END "))
					clear(line)
					line = line[:0]
					if end {
						clear(buf)
						done <- result{all, nil}
						return
					}
				case c == 0x7f || c == 8:
					if len(line) > 0 {
						line[len(line)-1] = 0
						line = line[:len(line)-1]
					}
				case c >= 0x20 || c == '\t':
					line = append(line, c)
				}
				if len(all)+len(line) > maxSecretBytes+3 {
					fail(userErr("the value is over Secret Manager's 64 KiB limit"))
					return
				}
			}
			clear(buf[:n])
			if rerr != nil {
				fail(userErr("reading the value from the terminal: %v", rerr))
				return
			}
		}
	}()
	finish := func() { _ = term.Restore(fd, state); fmt.Fprintln(prompt) }
	select {
	case r := <-done:
		finish()
		if ctx.Err() != nil {
			clear(r.b)
			return nil, errCancelled
		}
		if r.err == nil {
			if r.err = validateSecret(r.b, true); r.err != nil {
				clear(r.b)
			}
		}
		if r.err != nil {
			return nil, r.err
		}
		return r.b, nil
	case <-ctx.Done():
		finish()
		return nil, errCancelled
	}
}
