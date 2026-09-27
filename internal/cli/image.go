package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	cmd.AddCommand(newImageBuildCmd(), newImageRenderCmd(), newImageSelftestCmd())
	return cmd
}

// newImageSelftestCmd is `fugaro image selftest`: it reads a
// image.SelftestSpec as JSON on stdin, runs image.Selftest in the current
// environment (the image itself), and prints the resulting image.Report as
// JSON on stdout. It is hidden because `fugaro image build --local` is the
// only intended caller, running it inside the freshly built image.
func newImageSelftestCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "selftest",
		Short:  "Smoke-test the image this runs in (used by image build --local)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var spec image.SelftestSpec
			dec := json.NewDecoder(cmd.InOrStdin())
			dec.DisallowUnknownFields()
			if err := dec.Decode(&spec); err != nil {
				return fmt.Errorf("reading the selftest spec from stdin: %w", err)
			}
			rep := image.Selftest(cmd.Context(), spec, cmd.ErrOrStderr())
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(rep); err != nil {
				return err
			}
			if !rep.Passed {
				return &ExitError{Code: ExitUserError, Err: errors.New("smoke test failed")}
			}
			return nil
		},
	}
}

type imageBuildOptions struct {
	repo, workflow, base, tag, platform string
	local, noSmoke, asJSON              bool
}

func newImageBuildCmd() *cobra.Command {
	var o imageBuildOptions
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build the derived image; --local builds it with local Docker and smoke-tests it",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runImageBuild(cmd, o) },
	}
	f := cmd.Flags()
	f.BoolVar(&o.local, "local", false, "build with the local Docker daemon from the checkout you are in")
	f.StringVar(&o.repo, "repo", "", "repository to build with Cloud Build (not available yet)")
	f.StringVar(&o.workflow, "workflow", "", "workflow to build; optional when fugaro.yaml defines one")
	f.StringVar(&o.base, "base", "", "base image (default: the published base matching this fugaro version)")
	f.StringVar(&o.tag, "tag", "", "tag for the built image (default fugaro-<dir>-<workflow>:local)")
	f.StringVar(&o.platform, "platform", "linux/amd64", "image platform; Cloud Run runs linux/amd64")
	f.BoolVar(&o.noSmoke, "no-smoke", false, "skip the smoke test in the built image")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	return cmd
}

func runImageBuild(cmd *cobra.Command, o imageBuildOptions) error {
	ctx := cmd.Context()
	if !o.local {
		return &ExitError{Code: ExitUserError, Err: errors.New("building images with Cloud Build needs the GCP backend, which is not implemented yet (milestone M4); use --local to build with your local Docker")}
	}
	if o.repo != "" {
		return &ExitError{Code: ExitUserError, Err: errors.New("--repo applies to Cloud Build; --local builds the checkout you are in")}
	}
	root, cfg, name, err := loadCheckout(ctx, o.workflow)
	if err != nil {
		return err
	}
	base := o.base
	if base == "" {
		if base, err = image.BaseRef(cfg.Workflows[name].Base, Version); err != nil {
			return &ExitError{Code: ExitUserError, Err: err}
		}
	}
	tag := o.tag
	if tag == "" {
		tag = image.DefaultTag(root, name)
	}
	if err := image.DockerAvailable(ctx, nil); err != nil {
		return &ExitError{Code: ExitUserError, Err: err}
	}
	res, buildErr := image.BuildLocal(ctx, image.LocalOptions{
		Root: root, Config: cfg, Workflow: name, Base: base, Tag: tag, Platform: o.platform,
		Version: Version, Smoke: !o.noSmoke, Env: os.Environ(), Log: cmd.ErrOrStderr(),
	})
	if buildErr != nil {
		res.Error = buildErr.Error()
	}
	if err := printImageResult(cmd.OutOrStdout(), res, o.asJSON); err != nil {
		return err
	}
	switch {
	case buildErr != nil:
		return &ExitError{Code: ExitUserError, Err: buildErr}
	case res.Smoke != nil && !res.Smoke.Passed:
		return &ExitError{Code: ExitUserError, Err: fmt.Errorf("%s built, but its smoke test failed", res.Image)}
	}
	return nil
}

func printImageResult(w io.Writer, res *image.LocalResult, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	if res.Error != "" {
		return nil // main prints the error itself
	}
	source := res.Dockerfile
	if source == "generated" {
		source = "the generated Dockerfile"
	}
	fmt.Fprintf(w, "built %s from %s, FROM %s, commit %s, origin %s\n", res.Image, source, res.Base, res.Commit, res.Origin)
	if res.Smoke == nil {
		return nil
	}
	for _, c := range res.Smoke.Checks {
		status := "ok  "
		if !c.OK {
			status = "FAIL"
		}
		fmt.Fprintf(w, "  %s %-16s %s\n", status, c.Name, c.Detail)
	}
	return nil
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
		problems = append(config.Check(cfg, root), computeProblems(cfg)...)
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
