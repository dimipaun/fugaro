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

// enrolWriteStarter writes the starter fugaro.yaml into the checkout when it
// has none, the way `fugaro config init` does: a repository file left in the
// working tree to review and commit, never pushed. It reports whether it wrote.
// Nothing is written when the project publishes no layer for the file to
// resolve against.
func enrolWriteStarter(ctx context.Context, w io.Writer, lc *localcfg.Config, ownerName, checkout string, entry localcfg.Repo, known bool) (bool, error) {
	path := filepath.Join(checkout, "fugaro.yaml")
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	provider := ""
	if known && entry.Provider != "" {
		provider = entry.Provider
	} else if p, ok := providerFromOrigin(ctx); ok {
		provider = p
	}
	if provider == "" {
		return false, nil
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
		return false, err
	}
	if rf.Layer.Layer == nil {
		return false, userErr("%s has no fugaro.yaml and project %s publishes no layer for a starter to resolve against: write one with /fugaro:setup, or use `fugaro project repos add %s --allow-list-only`", pluginwire.Printable(ownerName), pluginwire.Printable(lc.Name), ownerName)
	}
	if len(rf.Problems) > 0 {
		return false, userErr("the starter fugaro.yaml would not resolve against project %s's layer: %s", pluginwire.Printable(lc.Name), pluginwire.Printable(layerProblemsText(rf.Problems)))
	}
	if err := writeMinimalFugaroYAML(path, text); err != nil {
		return false, userErr("%v", err)
	}
	fmt.Fprintf(w, "fugaro.yaml written for %s\n", pluginwire.Printable(ownerName))
	return true, nil
}
