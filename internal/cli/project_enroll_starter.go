package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/task"
)

// enrolStarterBranch is the branch the starter's pull request is pushed to and
// opened from: ONE name, so a second run finds the pull request the first one
// opened instead of pushing the same branch again.
const enrolStarterBranch = "fugaro/starter-fugaro-yaml"

// enrolStarterYAML is config init's minimal file plus the git block a provider
// other than github needs; the base branch rule is minimalFugaroYAML's.
func enrolStarterYAML(project, gcp, provider, baseBranch string) string {
	text := minimalFugaroYAML(project, gcp, "", baseBranch)
	if provider == "github" {
		return text
	}
	if strings.Contains(text, "git:\n") {
		return strings.Replace(text, "git:\n", "git:\n  provider: "+provider+"\n", 1)
	}
	return text + "git:\n  provider: " + provider + "\n"
}

// enrolStarterHosts opens the host the starter's pull request goes through: the
// workspace snapshot's own repository-write path (stateHostsFor), a var so a
// test injects a host. Its argument is the token a Bitbucket host pushes with
// and "" for every other provider.
var enrolStarterHosts stateHostFor = stateHostsFor("")

// enrolStarterTokenNotice is the one line printed before the Bitbucket token is
// read: the read is with the person's own credentials, and the value reaches
// nothing but the push of the starter's own pull request. It is
// tokenReadNotice's sentence for this path (the catalog build reads the same
// secret for a different reason).
const enrolStarterTokenNotice = "reads the project's " + config.BitbucketSecret + " secret with your own credentials and holds it in memory only, to push the starter fugaro.yaml; it is never printed, logged, or written to disk"

// enrolStarterToken reads the Bitbucket token a starter's push uses.
//
// It is the installation's own credential and never the operator's shell: the
// repository's own bitbucket-token container first (where `fugaro secrets set
// bitbucket-token --repo <owner/name>` puts a repository access token) and the
// project-wide copy the owner stored and allow-listed second (a workspace or
// project access token, D461) — the precedence an apply gives a job's mount
// (infra.ResolveSecretID), so the token this pushes with is the one the
// repository's own builds clone with. The value is read with the person's own
// credentials, is never printed, logged or written anywhere, and the caller
// holds it for the one push it makes.
//
// No coding agent's session can reach it: enrolOpenStarter refuses one before
// this read (and before any host is opened), so it is a person's own step.
//
// A var so a test drives the Bitbucket path without a project.
var enrolStarterToken = func(ctx context.Context, lc *localcfg.Config, ownerName string) (string, error) {
	slug, err := task.Slug(gitprov.KindBitbucket, ownerName)
	if err != nil {
		return "", userErr("the repository %s: %v", pluginwire.Printable(ownerName), err)
	}
	own := gcp.SecretID(slug, config.BitbucketSecret)
	project, _ := infra.ProjectSecretCandidate(lc, config.BitbucketSecret, ownerName)
	// The repository's own container wins when it holds a version, and the read
	// itself is what answers that: an absent version is ErrNoVersion, and only
	// then is the project-wide copy asked.
	tok, err := readSecretValue(ctx, lc, own)
	if errors.Is(err, gcp.ErrNoVersion) && project != "" {
		tok, err = readSecretValue(ctx, lc, project)
	}
	switch {
	case errors.Is(err, gcp.ErrNoVersion):
		return "", userErr("%s has no fugaro.yaml and this machine holds no Bitbucket token to push the pull request that would add one: store the repository's own with `%s secrets set %s --repo %s`, or one that covers it with `--project-wide`",
			pluginwire.Printable(ownerName), selfCommand(), config.BitbucketSecret, pluginwire.Printable(ownerName))
	case errors.Is(err, gcp.ErrNoAccess):
		return "", userErr("you may not read this project's %s secret (a project owner can), and pushing the starter fugaro.yaml to %s needs it",
			config.BitbucketSecret, pluginwire.Printable(ownerName))
	case err != nil:
		return "", userErr("reading this project's %s secret: %s", config.BitbucketSecret, oneLineCLI(err.Error()))
	}
	return string(tok), nil
}

// enrolOpenStarter opens a pull request that adds the starter fugaro.yaml when
// the checkout has none, and answers its URL ("" when the checkout has one).
// The branch is never the default one and nothing is written into the checkout:
// the enrolment goes on once the pull request is merged. Nothing is opened when
// the project publishes no layer for the file to resolve against.
//
// A pull request already open on enrolStarterBranch is the answer and not an
// error: the second run of the command — before the first pull request was
// merged — finds it, pushes nothing and says which one it is, so a person who
// reruns `repos add` is pointed at the pull request they have rather than at a
// failed push.
func enrolOpenStarter(ctx context.Context, out io.Writer, lc *localcfg.Config, ownerName, checkout string, entry localcfg.Repo, known bool) (string, error) {
	if _, err := os.Lstat(filepath.Join(checkout, "fugaro.yaml")); !errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	provider := ""
	if known && entry.Provider != "" {
		provider = entry.Provider
	} else if p, ok := providerFromOrigin(ctx); ok {
		provider = p
	}
	if provider == "" {
		return "", nil
	}
	// The starter is a person's step and not a coding agent's: it opens a pull
	// request in the operator's own repository, under their own credentials —
	// and, on Bitbucket, with the project's own token — and the one
	// confirmation every other write of `project repos add` takes is never
	// reached on this path, because the enrolment stops until that pull request
	// is merged. Refused here, before the token is read and before a host is
	// opened, so no session reaches either.
	if m := agentMarker(os.Getenv, processTerminal()); m != "" {
		return "", userErr("nothing was written: the starter fugaro.yaml for %s is a pull request in your own repository with your own credentials, which is a person's: %s", pluginwire.Printable(ownerName), initflow.AgentRefusal(m))
	}
	// A Bitbucket host pushes with the installation's own token and has no CLI
	// to lean on, so the read comes before the host is opened; every other
	// provider pushes with the person's own git credentials and reads nothing.
	token := ""
	if provider == gitprov.KindBitbucket {
		fmt.Fprintln(out, enrolStarterTokenNotice)
		var err error
		if token, err = enrolStarterToken(ctx, lc, ownerName); err != nil {
			return "", err
		}
	}
	host, err := enrolStarterHosts(token)
	if err != nil {
		return "", err
	}
	url, err := host.OpenPR(ctx, ownerName, enrolStarterBranch)
	if err != nil {
		return "", userErr("reading %s's open starter pull request: %v", pluginwire.Printable(ownerName), err)
	}
	if url != "" {
		return url, nil
	}
	base := "main"
	if ref := defaultBranchRef(ctx, checkout); strings.HasPrefix(ref, "origin/") {
		base = strings.TrimPrefix(ref, "origin/")
	}
	if !config.ValidBranchName(base) {
		base = "main"
	}
	text := enrolStarterYAML(lc.Name, lc.GCPProject, provider, base)
	rf, err := resolveFugaroYAML(ctx, []byte(text), lc, layerOptions{})
	if err != nil {
		return "", err
	}
	if rf.Layer.Layer == nil {
		return "", userErr("%s has no fugaro.yaml and project %s publishes no layer for a starter to resolve against: write one with /fugaro:setup, or use `fugaro project repos add %s --allow-list-only`", pluginwire.Printable(ownerName), pluginwire.Printable(lc.Name), ownerName)
	}
	if len(rf.Problems) > 0 {
		return "", userErr("the starter fugaro.yaml would not resolve against project %s's layer: %s", pluginwire.Printable(lc.Name), pluginwire.Printable(layerProblemsText(rf.Problems)))
	}
	url, err = host.Publish(ctx, statePublish{
		Repo:    ownerName,
		File:    "fugaro.yaml",
		Branch:  enrolStarterBranch,
		Bytes:   []byte(text),
		Subject: "Add the starter fugaro.yaml",
		Body:    "The minimal fugaro.yaml for project " + lc.Name + ", opened by `fugaro project repos add`.",
	})
	if err != nil {
		return "", userErr("opening the pull request for %s's fugaro.yaml: %v", pluginwire.Printable(ownerName), err)
	}
	return url, nil
}
