package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/image"
)

func newImageCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "image", Short: "Build and inspect a workflow's derived container image"}
	cmd.AddCommand(newImageRenderCmd())
	return cmd
}

func newImageRenderCmd() *cobra.Command {
	var workflow string
	cmd := &cobra.Command{
		Use:   "render",
		Short: "Print the Dockerfile that builds the workflow's derived image",
		Long: "Print the Dockerfile that builds the workflow's derived image.\n\n" +
			"Unlike other fugaro commands, this one has no --json form: its output\n" +
			"is the Dockerfile itself, not a report about one.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, cfg, name, err := loadCheckout(cmd.Context(), workflow)
			if err != nil {
				return err
			}
			data, repoFile, err := image.Dockerfile(root, cfg, name, Version)
			if err != nil {
				return &ExitError{Code: ExitUserError, Err: err}
			}
			if repoFile != "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: workflow %s uses the repository Dockerfile %s\n", name, repoFile)
			}
			_, err = cmd.OutOrStdout().Write(data)
			return err
		},
	}
	cmd.Flags().StringVar(&workflow, "workflow", "", "workflow to render; optional when fugaro.yaml defines one")
	return cmd
}

// loadCheckout finds the git checkout containing the working directory,
// loads its fugaro.yaml, validates and checks it the way `fugaro validate`
// does, and selects the workflow.
func loadCheckout(ctx context.Context, workflow string) (root string, cfg *config.Config, name string, err error) {
	out, err := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", nil, "", &ExitError{Code: ExitUserError, Err: errors.New("not inside a git checkout; run this from the repository")}
	}
	root = strings.TrimSpace(string(out))
	data, err := os.ReadFile(filepath.Join(root, "fugaro.yaml"))
	if err != nil {
		return "", nil, "", &ExitError{Code: ExitUserError, Err: fmt.Errorf("%w; create it with /fugaro:onboard or fugaro config example", err)}
	}
	cfg, problems := config.Parse(data)
	if cfg != nil {
		problems = config.Check(cfg, root)
	}
	if len(problems) > 0 {
		msgs := make([]string, len(problems))
		for i, p := range problems {
			msgs[i] = p.String()
		}
		return "", nil, "", &ExitError{Code: ExitUserError, Err: fmt.Errorf("fugaro.yaml has %d problem(s), see fugaro validate:\n  %s", len(problems), strings.Join(msgs, "\n  "))}
	}
	if name, _, err = cfg.SelectWorkflow(workflow); err != nil {
		return "", nil, "", &ExitError{Code: ExitUserError, Err: err}
	}
	return root, cfg, name, nil
}
