package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// minimalFugaroYAML is the minimal fugaro.yaml of a repository of project
// whose installation is in GCP project gcp (decision L12): base_branch
// only when it is not main, profile only when named.
func minimalFugaroYAML(project, gcp, profile, baseBranch string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "version: 1\nproject: %s\ngcp_project: %s\n", project, gcp)
	if baseBranch != "" && baseBranch != "main" {
		fmt.Fprintf(&b, "git:\n  base_branch: %s\n", baseBranch)
	}
	if profile != "" {
		fmt.Fprintf(&b, "profile: %s\n", profile)
	}
	return b.String()
}

// compareExistingMinimalFile is config init's idempotent-or-refuse rule
// (design §12: "exits 0 when the file already says exactly that"): path
// absent means proceed: stop is false. Anything else means RunE should
// return right away, with err (nil for the idempotent case).
func compareExistingMinimalFile(w io.Writer, path, text string) (stop bool, err error) {
	switch old, rerr := os.ReadFile(path); {
	case rerr == nil && string(old) == text:
		fmt.Fprintf(w, "%s is already this minimal file; nothing written\n", path)
		return true, nil
	case rerr == nil:
		return true, userErr("%s exists; fugaro config init never overwrites it (delete it first, or edit it by hand)", path)
	case !errors.Is(rerr, fs.ErrNotExist):
		return true, userErr("%v", rerr)
	default:
		return false, nil
	}
}

// writeMinimalFugaroYAML creates path with text, refusing to replace
// anything already there or to follow a symlink into writing somewhere
// else (O_EXCL|O_NOFOLLOW): a plain os.ReadFile-then-os.WriteFile leaves a
// window, between the existence check and the write, where another
// process (or a symlink an attacker's checkout planted at this path)
// could change what the write actually touches. ErrExist here means
// something appeared in that window. A var so a test can make the race
// happen deterministically, by writing path itself just before calling
// through to the real implementation.
var writeMinimalFugaroYAML = func(path, text string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write([]byte(text)); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func newConfigInitCmd() *cobra.Command {
	var (
		o                   cloudOptions
		profile, baseBranch string
		yes                 bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write the minimal fugaro.yaml of a repository whose project publishes a project layer",
		Long: "Write the minimal fugaro.yaml (version, project, gcp_project) into this checkout, for a project that\n" +
			"publishes a project layer. Non-interactive: without --yes it prints the file and exits 1 (a dry run),\n" +
			"writing nothing. It never overwrites a fugaro.yaml, and writes nothing that does not resolve against\n" +
			"the project layer. Unlike fugaro config publish, it never refuses in a coding-agent session: it writes\n" +
			"a repository file, which still goes through a reviewed pull request, not a published, trusted object.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if profile != "" && !config.ProjectNameRE.MatchString(profile) {
				return userErr("--profile %q is not a profile name", profile)
			}
			if baseBranch != "" && !config.ValidBranchName(baseBranch) {
				return userErr("--base-branch %q is not a plain git branch name", baseBranch)
			}
			root, err := gitRead(ctx, ".", "rev-parse", "--show-toplevel")
			if err != nil {
				return userErr("not inside a git checkout; run this from the repository")
			}
			_, lc, err := selectProject(ctx, o)
			if err != nil {
				return err
			}
			text := minimalFugaroYAML(lc.Name, lc.GCPProject, profile, baseBranch)
			path := filepath.Join(root, "fugaro.yaml")
			// The idempotent-or-refuse check runs before anything
			// networked: a rerun of the same command is idempotent (exit
			// 0) even when the bucket the layer check below needs is
			// unreachable, or the published layer has since changed,
			// design §12's point of a script running this unattended
			// across many repositories.
			if stop, err := compareExistingMinimalFile(cmd.OutOrStdout(), path, text); stop {
				return err
			}
			rf, err := resolveFugaroYAML(ctx, []byte(text), lc, layerOptions{})
			if err != nil {
				return err
			}
			if rf.Layer.Layer == nil {
				return userErr("project %s publishes no project layer, so a minimal fugaro.yaml would not resolve; write a full one with /fugaro:setup", lc.Name)
			}
			if len(rf.Problems) > 0 {
				return userErr("the minimal fugaro.yaml would not resolve against project %s's layer: %s", lc.Name, pluginwire.Printable(layerProblemsText(rf.Problems)))
			}
			// rf.Cfg is non-nil whenever rf.Problems is empty
			// (config.Resolve never returns both); the switch below
			// still checks every field it reads matches what
			// minimalFugaroYAML actually asked for, not just that
			// something resolved: a disagreement here would mean
			// minimalFugaroYAML and config.Resolve mean different
			// things by the same text, and the write must never go
			// ahead on that silently.
			wantBranch := baseBranch
			if wantBranch == "" {
				wantBranch = "main"
			}
			switch {
			case rf.Cfg.Project != lc.Name:
				return userErr("internal error: the minimal fugaro.yaml resolved to project %q, not %q", pluginwire.Printable(rf.Cfg.Project), lc.Name)
			case rf.Cfg.GCPProject != lc.GCPProject:
				return userErr("internal error: the minimal fugaro.yaml resolved to gcp_project %q, not %q", pluginwire.Printable(rf.Cfg.GCPProject), lc.GCPProject)
			case rf.Cfg.Git.BaseBranch != wantBranch:
				return userErr("internal error: the minimal fugaro.yaml resolved to base branch %q, not %q", pluginwire.Printable(rf.Cfg.Git.BaseBranch), wantBranch)
			case profile != "" && rf.Cfg.Workflows[config.ImplicitWorkflow].Profile != profile:
				return userErr("internal error: the minimal fugaro.yaml resolved to profile %q, not %q", pluginwire.Printable(rf.Cfg.Workflows[config.ImplicitWorkflow].Profile), profile)
			}
			if !yes {
				fmt.Fprint(cmd.OutOrStdout(), text)
				return userErr("nothing written: pass --yes to write %s", path)
			}
			switch err := writeMinimalFugaroYAML(path, text); {
			case err == nil:
			case errors.Is(err, fs.ErrExist):
				// A TOCTOU race: something created path between the
				// check above and this write. Re-apply the same
				// idempotent-or-refuse rule instead of either
				// overwriting it or reporting a raw "file exists".
				if stop, err := compareExistingMinimalFile(cmd.OutOrStdout(), path, text); stop {
					return err
				}
				return userErr("%s appeared and then disappeared while being written; try again", path)
			default:
				return userErr("%v", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s: workflow %s from profile %s (resolved sha256 %s)\n", path, config.ImplicitWorkflow, rf.Cfg.Workflows[config.ImplicitWorkflow].Profile, rf.Res.ConfigSHA256[:12])
			return nil
		},
	}
	cmd.Flags().StringVar(&profile, "profile", "", "the project layer's profile to use (default: its default_profile)")
	cmd.Flags().StringVar(&baseBranch, "base-branch", "", "the repository's base branch, when it is not main")
	cmd.Flags().BoolVar(&yes, "yes", false, "write the file (without it, print it and exit 1 without writing)")
	addCloudFlags(cmd, &o)
	return cmd
}
