package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
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
			"publishes a project layer. Non-interactive: without --yes it prints the file and writes nothing. It never\n" +
			"overwrites a fugaro.yaml, and writes nothing that does not resolve against the project layer.",
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
			rf, err := resolveFugaroYAML(ctx, []byte(text), lc, layerOptions{})
			if err != nil {
				return err
			}
			if rf.Layer.Layer == nil {
				return userErr("project %s publishes no project layer, so a minimal fugaro.yaml would not resolve; write a full one with /fugaro:setup", lc.Name)
			}
			if len(rf.Problems) > 0 {
				return userErr("the minimal fugaro.yaml would not resolve against project %s's layer: %s", lc.Name, layerProblemsText(rf.Problems))
			}
			path := filepath.Join(root, "fugaro.yaml")
			switch old, err := os.ReadFile(path); {
			case err == nil && string(old) == text:
				fmt.Fprintf(cmd.OutOrStdout(), "%s is already this minimal file; nothing written\n", path)
				return nil
			case err == nil:
				return userErr("%s exists; fugaro config init never overwrites it (delete it first, or edit it by hand)", path)
			case !errors.Is(err, fs.ErrNotExist):
				return userErr("%v", err)
			}
			if !yes {
				fmt.Fprint(cmd.OutOrStdout(), text)
				return userErr("nothing written: pass --yes to write %s", path)
			}
			if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
				return userErr("%v", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s: workflow %s from profile %s (resolved sha256 %s)\n", path, config.ImplicitWorkflow, rf.Cfg.Workflows[config.ImplicitWorkflow].Profile, rf.Res.ConfigSHA256[:12])
			return nil
		},
	}
	cmd.Flags().StringVar(&profile, "profile", "", "the project layer's profile to use (default: its default_profile)")
	cmd.Flags().StringVar(&baseBranch, "base-branch", "", "the repository's base branch, when it is not main")
	cmd.Flags().BoolVar(&yes, "yes", false, "write the file (without it, print it and write nothing)")
	addCloudFlags(cmd, &o)
	return cmd
}
