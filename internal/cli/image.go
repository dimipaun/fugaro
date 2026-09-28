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
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/task"
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
	cloud                               cloudOptions
	repo, workflow, base, tag, platform string
	local, noSmoke, noWait, asJSON      bool
}

func newImageBuildCmd() *cobra.Command {
	var o imageBuildOptions
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build the derived image with Cloud Build; --local builds it with local Docker and smoke-tests it",
		Long: "Build the workflow's derived image.\n\n" +
			"Without --local, Cloud Build builds it from the repository's base branch\n" +
			"(cloning with the repository's bitbucket-token secret) and pushes it to\n" +
			"the local config's registry. Run it from the repository's checkout: the\n" +
			"checkout's fugaro.yaml and origin say what to build.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runImageBuild(cmd, o) },
	}
	f := cmd.Flags()
	f.BoolVar(&o.local, "local", false, "build with the local Docker daemon from the checkout you are in")
	f.StringVar(&o.repo, "repo", "", "owner/name of the repository to build with Cloud Build (default: the checkout's origin, which it must match)")
	f.StringVar(&o.workflow, "workflow", "", "workflow to build; optional when fugaro.yaml defines one")
	f.StringVar(&o.base, "base", "", "base image (default: for a Cloud Build build the local config's base_image, else the published base matching this fugaro version)")
	f.StringVar(&o.tag, "tag", "", "tag for the built image (default fugaro-<dir>-<workflow>:local)")
	f.StringVar(&o.platform, "platform", "linux/amd64", "image platform; Cloud Run runs linux/amd64")
	f.BoolVar(&o.noSmoke, "no-smoke", false, "skip the smoke test in the built image (--local)")
	f.BoolVar(&o.noWait, "no-wait", false, "submit the Cloud Build build and return without waiting for it")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func runImageBuild(cmd *cobra.Command, o imageBuildOptions) error {
	ctx := cmd.Context()
	if !o.local {
		return runImageBuildCloud(cmd, o)
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

// runImageBuildCloud submits the derived-image build of the checkout's
// repository and workflow to Cloud Build and, unless --no-wait, waits for
// it. The build clones the base branch itself; only the checkout's
// fugaro.yaml and origin are read here.
func runImageBuildCloud(cmd *cobra.Command, o imageBuildOptions) error {
	ctx := cmd.Context()
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()
	lc := env.lc
	_, cfg, name, err := loadCheckout(ctx, o.workflow)
	if err != nil {
		return err
	}
	if cfg.Git.Provider != "bitbucket" {
		return userErr("Cloud Build images for GitHub repositories need a token-minting step that arrives in M5; use --local")
	}
	switch {
	case lc.Registry == "":
		return userErr("the local config has no registry, the Artifact Registry repository images are pushed to (such as <region>-docker.pkg.dev/<project>/fugaro)")
	case lc.Build.ServiceAccount == "":
		return userErr("the local config has no build.service_account, the service account Cloud Build runs as")
	}
	origin, err := originRepo(ctx)
	if err != nil {
		return err
	}
	repo := o.repo
	if repo == "" {
		repo = origin
	}
	if a, err1 := task.CanonicalRepo(origin); err1 != nil {
		return userErr("%v", err1)
	} else if b, err2 := task.CanonicalRepo(repo); err2 != nil {
		return userErr("%v", err2)
	} else if a != b {
		return userErr("--repo %s is not this checkout's origin (%s); run from %s's checkout", repo, origin, repo)
	}
	repoURL, err := originURL(ctx)
	if err != nil {
		return err
	}
	slug, err := env.repoSlug(repo, func() *config.Config { return cfg })
	if err != nil {
		return err
	}
	branch := "main"
	if r, ok := env.localRepo(repo); ok && r.BaseBranch != "" {
		branch = r.BaseBranch
	} else if cfg.Git.BaseBranch != "" {
		branch = cfg.Git.BaseBranch
	}
	base := o.base
	if base == "" {
		base = lc.BaseImage
	}
	if base == "" {
		if base, err = image.BaseRef(cfg.Workflows[name].Base, Version); err != nil {
			return userErr("%v", err)
		}
	}
	spec := gcp.BuildSpec{
		Slug: slug, RepoURL: repoURL, BaseBranch: branch, Workflow: name, Base: base,
		Image:       gcp.ImageName(lc.Registry, slug, name),
		GitSecretID: gcp.SecretID(slug, "bitbucket-token"), GitUser: bitbucket.GitUsername,
		ServiceAccount: lc.Build.ServiceAccount, MachineType: lc.Build.MachineType,
		WorkflowSecrets: cfg.Workflows[name].Secrets,
	}
	b, err := gcp.NewBuilder(ctx, env.gcp, lc.BuildRegion())
	if err != nil {
		return remote(err)
	}
	res, err := b.Submit(ctx, spec)
	switch {
	case errors.Is(err, gcp.ErrBadBuildSpec):
		return userErr("%v", err)
	case err != nil:
		return remote(err)
	}
	if !o.noWait {
		fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: Cloud Build build %s of %s submitted; log: %s\n", oneLine(res.ID), oneLine(res.Image), oneLine(res.LogURL))
		done, waitErr := b.Wait(ctx, res.ID, 0)
		if done.Image == "" {
			done.Image = res.Image
		}
		if done.LogURL == "" {
			done.LogURL = res.LogURL
		}
		res = done
		if waitErr != nil {
			if o.asJSON {
				if err := printBuildResult(cmd.OutOrStdout(), res); err != nil {
					return err
				}
			}
			return remote(waitErr)
		}
	}
	if o.asJSON {
		return printBuildResult(cmd.OutOrStdout(), res)
	}
	if o.noWait {
		fmt.Fprintf(cmd.OutOrStdout(), "submitted Cloud Build build %s of %s; log: %s\n", oneLine(res.ID), oneLine(res.Image), oneLine(res.LogURL))
		return nil
	}
	if res.Digest == "" {
		// SUCCESS covers the push of images:, so the tag is there; only
		// the pushed-image report is missing.
		fmt.Fprintf(cmd.OutOrStdout(), "built %s, digest unknown: Cloud Build reported no pushed image (Cloud Build build %s)\n", oneLine(res.Image), oneLine(res.ID))
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "built %s@%s (Cloud Build build %s)\n", oneLine(strings.TrimSuffix(res.Image, ":latest")), oneLine(res.Digest), oneLine(res.ID))
	return nil
}

func printBuildResult(w io.Writer, res gcp.BuildResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}

// originURL is the checkout's origin as an https URL without credentials,
// which the Cloud Build clone uses.
func originURL(ctx context.Context) (string, error) {
	c := exec.CommandContext(ctx, "git", "remote", "get-url", "origin")
	c.WaitDelay = 5 * time.Second
	out, err := c.Output()
	if err != nil {
		return "", userErr("no origin remote in this checkout")
	}
	u := image.HTTPSOrigin(strings.TrimSpace(string(out)))
	if !strings.HasPrefix(u, "https://") {
		return "", userErr("origin %s has no https form for Cloud Build to clone", gcp.RedactURL(u))
	}
	return u, nil
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
	fmt.Fprintf(w, "built %s from %s, FROM %s, commit %s, origin %s\n", oneLine(res.Image), oneLine(source), oneLine(res.Base), oneLine(res.Commit), oneLine(res.Origin))
	if res.Smoke == nil {
		return nil
	}
	for _, c := range res.Smoke.Checks {
		status := "ok  "
		if !c.OK {
			status = "FAIL"
		}
		fmt.Fprintf(w, "  %s %-16s %s\n", status, oneLine(c.Name), oneLine(c.Detail))
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
