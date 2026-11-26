package cli

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

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
// test injects a host.
var enrolStarterHosts stateHostFor = stateHostsFor("")

// enrolOpenStarter opens a pull request that adds the starter fugaro.yaml when
// the checkout has none, and answers its URL ("" when the checkout has one).
// The branch is never the default one and nothing is written into the checkout:
// the enrolment goes on once the pull request is merged. Nothing is opened when
// the project publishes no layer for the file to resolve against.
func enrolOpenStarter(ctx context.Context, lc *localcfg.Config, ownerName, checkout string, entry localcfg.Repo, known bool) (string, error) {
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
	host, err := enrolStarterHosts(provider)
	if err != nil {
		return "", err
	}
	url, err := host.Publish(ctx, statePublish{
		Repo:    ownerName,
		File:    "fugaro.yaml",
		Branch:  "fugaro/starter-fugaro-yaml",
		Bytes:   []byte(text),
		Subject: "Add the starter fugaro.yaml",
		Body:    "The minimal fugaro.yaml for project " + lc.Name + ", opened by `fugaro project repos add`.",
	})
	if err != nil {
		return "", userErr("opening the pull request for %s's fugaro.yaml: %v", pluginwire.Printable(ownerName), err)
	}
	return url, nil
}
