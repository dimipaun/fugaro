package runner

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/repourl"
	"github.com/dimipaun/fugaro/internal/task"
)

// ContextRepoRoot is the directory a run's read-only context repositories are
// cloned into, as a sibling of the work directory.
//
// It is a sibling and never a path inside the work directory, and that is the
// whole of what keeps a context repository out of the run's own work: the work
// directory is the run's checkout, so `git add -A` (CommitAll, which the digest
// step and finalize both call) would commit a checkout inside it, the branch
// diff and the pull request would carry its paths, and Claude Code would load a
// CLAUDE.md of one as this repository's instructions. Nothing in this run
// removes the directory afterwards either, so a path outside the tree is the
// only layout that holds for the whole run.
//
// It is beside BakedRepoRoot rather than under it: a baked checkout is one a
// shared image carries for the repositories the project's map serves, and a
// context repository is one this run reads for one run alone (D613) — neither
// is a path of the project's own map.
const ContextRepoRoot = "context"

// ContextRepoDir is where a run whose work directory is workDir reads the
// context repository repo, in task.CanonicalRepo's "owner/name" form: the
// repository's own path under ContextRepoRoot, so two runs of one repository
// read the same path and the prompt can name it before the clone is made.
func ContextRepoDir(workDir, repo string) string {
	return filepath.Join(filepath.Dir(workDir), ContextRepoRoot, filepath.FromSlash(repo))
}

// contextClone is one context repository to clone: the repository, the URL it
// is cloned from, and the read-only token the credentials service minted for
// the App installation it is under. It is what the mechanics below take, so
// they can be exercised without a service and without a GitHub host.
type contextClone struct {
	repo  string
	url   string
	token string
}

// cloneContextRepos clones the read-only context repositories this run's task
// names (D613, plan task C5c).
//
// The credential is the installation's credentials service and never the job's
// own App key: the service mints one token per App installation, scoped to
// exactly the repositories the run's identity names (C5b), and it re-admits
// every one of them against the project's policy, so a run cannot read further
// than its launch granted. A run that names a context repository and has no
// service to ask is refused rather than served by the key the job may hold —
// that key is the write credential, and the in-job mint path is not extended
// (C5a).
func (r *run) cloneContextRepos(ctx context.Context) error {
	if len(r.spec.ContextRepos) == 0 {
		return nil
	}
	if r.providerKind != gitprov.KindGitHub {
		return fmt.Errorf("this run's task names the read-only context repositories %s and this run's repository is on the %s provider: a read-only context repository is a GitHub repository until the credential path serves another provider's, and the launch that records one refuses a repository that is not GitHub's",
			strings.Join(r.spec.ContextRepos, ", "), r.providerKind)
	}
	src, err := r.serviceTokens(ctx)
	if err != nil {
		return err
	}
	if src == nil {
		return fmt.Errorf("this run's task names the read-only context repositories %s and this job's environment names no credentials service (%s): the read-only token a context repository is cloned with is the service's mint, and the job's own App key is the run's write credential, so the run reads none of them rather than one with a credential that writes",
			strings.Join(r.spec.ContextRepos, ", "), providers.EnvCredentialsServiceURL)
	}
	tokens, err := src.ContextTokens(ctx, r.spec.ContextRepos)
	if err != nil {
		return fmt.Errorf("minting the read-only credentials for this run's context repositories: %w", err)
	}
	for _, t := range tokens {
		r.addSecret(t.Token)
	}
	plan, err := r.contextClonePlan(tokens)
	if err != nil {
		return err
	}
	return r.cloneContexts(ctx, plan)
}

// contextClonePlan is the clones the service's answer asks for: one per
// repository the task names, each with the token that names it.
//
// A repository the answer names no token for stops the run: the service serves
// the repositories the run's identity names and refuses any other, so an answer
// that serves fewer is a mint that did not cover this run's list, and cloning
// the rest would be reading a repository the run's launch did not grant.
func (r *run) contextClonePlan(tokens []github.ContextToken) ([]contextClone, error) {
	byRepo := make(map[string]string)
	for _, t := range tokens {
		for _, repo := range t.Repositories {
			byRepo[repo] = t.Token
		}
	}
	plan := make([]contextClone, 0, len(r.spec.ContextRepos))
	for _, repo := range r.spec.ContextRepos {
		canonical, err := task.CanonicalRepo(repo)
		if err != nil {
			return nil, fmt.Errorf("this run's task names %q as a read-only context repository: %w", repo, err)
		}
		token, ok := byRepo[canonical]
		if !ok {
			return nil, fmt.Errorf("the credentials service minted no token naming the read-only context repository %s: the run reads none of them rather than one it holds no read-only token for, which is what the service's own refusal would leave it with anyway",
				canonical)
		}
		// The clone URL is the one this repository's canonical name builds.
		// The policy's clone_urls is read for the run's OWN repository and not
		// for this one: there the deployer writes a per-repository job's remote
		// from that block, so the runner has to compute the same bytes, and
		// repourl refuses an entry that is not this repository on this
		// provider's host either way.
		url, err := repourl.CloneURL(gitprov.KindGitHub, canonical, "")
		if err != nil {
			return nil, fmt.Errorf("the clone URL of the read-only context repository %s: %w", canonical, err)
		}
		plan = append(plan, contextClone{repo: canonical, url: url, token: token})
	}
	return plan, nil
}

// cloneContexts clones every planned context repository into its own directory
// under ContextRepoRoot: a blobless clone (gitops.CloneBlobless — the history,
// no file contents until the checkout needs them), checked out at the remote's
// default branch, with the read-only token and nothing else of this run in the
// environment, and with the checkout's push URL disabled before anything else
// touches it.
//
// Read-only is the three together: the clone's own credential is the read-only
// token, scoped to the repository's host and to no other URL (CredentialVars),
// the push URL of the checkout reaches no repository (gitops.DisablePush), and
// no runner push, checkpoint or commit names a path under ContextRepoRoot.
// What the agent holds is unchanged — it is not given the token at all — so
// nothing here widens what the run's own agent can reach (SECURITY.md's "What
// one run's agent can reach").
func (r *run) cloneContexts(ctx context.Context, plan []contextClone) error {
	for _, c := range plan {
		dir := ContextRepoDir(r.d.WorkDir, c.repo)
		vars, err := contextCloneVars(c)
		if err != nil {
			return fmt.Errorf("building the credentials of the read-only context repository %s: %w", c.repo, err)
		}
		env := gitops.WithVars(gitops.IdentityEnvFor(r.identity), vars)
		repo, err := contextCloneAt(ctx, dir, c.url, env)
		if err != nil {
			return fmt.Errorf("cloning the read-only context repository %s: %w", c.repo, err)
		}
		if err := repo.DisablePush(ctx); err != nil {
			return fmt.Errorf("disabling the push URL of the read-only context repository %s: %w", c.repo, err)
		}
		if err := repo.CheckoutRef(ctx, "HEAD"); err != nil {
			return fmt.Errorf("checking out the read-only context repository %s: %w", c.repo, err)
		}
		r.d.Log.Info("read-only context repository cloned", "repo", c.repo, "dir", dir)
	}
	return nil
}

// contextCloneAt makes dir hold the read-only checkout of url and returns it:
// a blobless clone when dir holds none, and the checkout already there when it
// does.
//
// A checkout at dir is not the anomaly it would be in a container nobody has
// run in: bootstrap can run twice on one container — the restart claimRecord
// carries on from, and the earlier run of a container openCheckout tolerates —
// and `git clone` refuses a directory that holds anything, so a second
// bootstrap of a run that names a context repository would die on its own
// clone. What is reopened is a checkout of this URL and no other: a directory
// at that path holding a checkout of something else is not the repository this
// run was launched to read, and reading it would be reading whatever put it
// there.
func contextCloneAt(ctx context.Context, dir, url string, env []string) (*gitops.Repo, error) {
	existing, err := gitops.Open(dir, env)
	if err != nil {
		return gitops.CloneBlobless(ctx, dir, url, "", env, modelCredentialVars...)
	}
	existing.StripEnv = modelCredentialVars
	origin, err := existing.OriginURL(ctx)
	if err != nil {
		return nil, err
	}
	if origin != url {
		return nil, fmt.Errorf("%s holds a checkout of %s and this run reads the context repository %s: nothing is read from a checkout this run did not make", dir, origin, url)
	}
	return existing, nil
}

// contextRepoLines is what the agent's system prompt says about this run's
// context repositories: for each, the path it is checked out at and the
// repository that path holds, so the prompt names a directory the agent can
// open and a repository a person can recognise.
//
// The path is computed and not remembered from the clone, because the clone and
// the prompt have to agree on it and the one rule for it is ContextRepoDir: a
// prompt built from a second copy of the layout would point at a directory the
// clone did not make. A name that is not a canonical repository is skipped —
// cloneContextRepos refuses such a task before any stage runs, so a run that
// reaches a prompt holds no such name.
func (r *run) contextRepoLines() []string {
	out := make([]string, 0, len(r.spec.ContextRepos))
	for _, repo := range r.spec.ContextRepos {
		canonical, err := task.CanonicalRepo(repo)
		if err != nil {
			continue
		}
		out = append(out, fmt.Sprintf("%s (%s)", ContextRepoDir(r.d.WorkDir, canonical), canonical))
	}
	return out
}

// contextCloneVars are the git credentials one context clone is made with: the
// read-only token, scoped to the host the repository is on, as the credential
// helper gitops.CredentialVars installs (it resets every other helper
// configured for that URL, so a checkout's own configuration answers for
// nothing here). A URL with no http(s) host carries no credentials and needs
// none: CredentialVars refuses such a base URL rather than scoping a token to
// every host git talks to.
func contextCloneVars(c contextClone) (map[string]string, error) {
	base := gitops.CredentialURL(c.url)
	if base == "" {
		return nil, nil
	}
	return gitops.CredentialVars(base, github.GitUsername, c.token)
}
