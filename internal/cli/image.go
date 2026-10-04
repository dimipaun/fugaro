package cli

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"
)

func newImageCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "image", Short: "Build and inspect a workflow's derived container image"}
	cmd.AddCommand(newImageBuildCmd(), newImageRenderCmd(), newImageSelftestCmd(), newImageGitCredentialCmd(),
		newImageGateCmd(), newImageRecordCmd(), newImageCheckCmd(), newImageStatusCmd())
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
			"as the repository's build account (cloning with its bitbucket-token, or\n" +
			"a read-only token its GitHub App mints) and pushes it to the repository's\n" +
			"own registry, which fugaro init --repo creates. It pushes a candidate,\n" +
			"smoke-tests it without network, and only then points latest at it and\n" +
			"records what it was built from, unless a newer build is already\n" +
			"recorded. Run it from the repository's checkout: the checkout's\n" +
			"fugaro.yaml and origin say what to build.\n\n" +
			"A Cloud Build is billable, so it asks for the project's name typed at a real\n" +
			"terminal: --json, a pipe and a coding agent's session never confirm it (it\n" +
			"prints the one line to run in your own terminal window and exits 1).\n" +
			"--local builds with your own Docker and costs nothing in the cloud.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runImageBuild(cmd, o) },
	}
	f := cmd.Flags()
	f.BoolVar(&o.local, "local", false, "build with the local Docker daemon from the checkout you are in")
	f.StringVar(&o.repo, "repo", "", "owner/name of the repository to build with Cloud Build (default: the checkout's origin, which it must match)")
	f.StringVar(&o.workflow, "workflow", "", "workflow to build; optional when fugaro.yaml defines one")
	f.StringVar(&o.base, "base", "", "base image (default: for a Cloud Build build the local config's base_images entry for the workflow's base, else the published base matching this fugaro version)")
	f.StringVar(&o.tag, "tag", "", "tag for the built image (default fugaro-<dir>-<workflow>:local)")
	f.StringVar(&o.platform, "platform", "linux/amd64", "image platform; Cloud Run runs linux/amd64")
	f.BoolVar(&o.noSmoke, "no-smoke", false, "skip the smoke test of the built image (on Cloud Build, latest is then promoted unsmoked)")
	f.BoolVar(&o.noWait, "no-wait", false, "submit the Cloud Build build and return without waiting for it")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output (--local only: a Cloud Build is never submitted with --json, nobody can type its confirmation)")
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
	for _, w := range lc.Warnings() {
		fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: %s\n", w)
	}
	_, cfg, name, err := loadCheckout(ctx, o.workflow)
	if err != nil {
		return err
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
	base := o.base
	if base == "" {
		base = lc.BaseImage(cfg.Workflows[name].Base)
	}
	if base == "" {
		if base, err = image.BaseRef(cfg.Workflows[name].Base, Version); err != nil {
			return userErr("%v", err)
		}
	}
	// Every name comes from the repository's spec, as fugaro init --repo
	// creates them: the build account, the registry (refused when its host
	// names another GCP project than the config's own) and the provider
	// credential. The spec's check job needs a base image, which this
	// build doesn't use; the one the build uses stands in for a kind the
	// local config has none for.
	specLC := *lc
	specLC.BaseImages = maps.Clone(lc.BaseImages)
	if specLC.BaseImages == nil {
		specLC.BaseImages = map[string]string{}
	}
	for _, w := range cfg.Workflows {
		specLC.BaseImages[w.Base] = cmp.Or(specLC.BaseImages[w.Base], base)
	}
	rs, err := infra.Repo(infra.Inputs{LC: &specLC, Repo: repo, Cfg: cfg, RepoURL: repoURL})
	if err != nil {
		return userErr("%v", err)
	}
	if note := buildRecordNote(lc); note != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: %s\n", note)
	}
	spec, err := cloudBuildSpec(rs, cfg, name, base, lc.Build.MachineType, lc.RecordBucketURL())
	if err != nil {
		return err
	}
	spec.NoSmoke = o.noSmoke
	// A Cloud Build is billable: the project's name typed at a real
	// terminal, never --json, a pipe or a coding agent's session. A run that
	// can't take it is refused here, before any cloud call.
	conds := initflow.Conditions{Terminal: stdinIsTerminal(cmd.InOrStdin()), JSON: o.asJSON, Agent: agentMarker(os.Getenv)}
	if !initflow.CanConfirm(initflow.Typed, conds) {
		return buildNotConfirmable(conds, repo, name)
	}
	b, err := gcp.NewBuilder(ctx, env.gcp, lc.BuildRegion())
	if err != nil {
		return remote(err)
	}
	// A push to a missing registry would fail only at the build's end. An
	// operator who may submit builds need not be able to read the
	// registry: then the build goes ahead, and a missing registry fails it.
	switch exists, err := b.RegistryExists(ctx, rs.RegistryPath); {
	case errors.Is(err, gcp.ErrRegistryUnchecked):
		fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: could not check that the image registry %s exists (%s); submitting the build anyway: if the registry is missing, the build fails when it pushes\n", rs.RegistryPath, oneLine(err.Error()))
	case err != nil:
		return remote(err)
	case !exists:
		return userErr("project %s has no image registry %s for %s yet, so the build would have nowhere to push. fugaro init --repo creates it: run that from this checkout first", lc.GCPProject, rs.RegistryPath, repo)
	}
	if err := confirmBuild(cmd, lc.Name, lc.GCPProject, cloudBuildBanner(repo, name, lc.Build.MachineType, rs.BuildServiceAccountEmail, rs.RegistryPath)); err != nil {
		return err
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
			return remote(waitErr)
		}
	}
	if o.noWait {
		fmt.Fprintf(cmd.OutOrStdout(), "submitted Cloud Build build %s of %s; log: %s\n", oneLine(res.ID), oneLine(res.Image), oneLine(res.LogURL))
		return nil
	}
	if res.Digest == "" {
		// SUCCESS covers the promotion, so the tag is there; only the
		// digest report is missing.
		fmt.Fprintf(cmd.OutOrStdout(), "built %s, digest unknown: Cloud Build reported no pushed image (Cloud Build build %s)\n", oneLine(res.Image), oneLine(res.ID))
		return nil
	}
	if res.Superseded {
		fmt.Fprintf(cmd.OutOrStdout(), "built %s@%s (Cloud Build build %s), but a newer build is already recorded, so latest and the record stay as they were\n",
			oneLine(res.Image), oneLine(res.Digest), oneLine(res.ID))
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "built %s@%s (Cloud Build build %s)\n", oneLine(strings.TrimSuffix(res.Image, ":latest")), oneLine(res.Digest), oneLine(res.ID))
	return nil
}

// cloudBuildBanner is what a Cloud Build confirmation says it does: the one
// text fugaro image build and fugaro init's first build share.
func cloudBuildBanner(repo, workflow, machineType, account, registry string) string {
	return fmt.Sprintf("submits a Cloud Build for %s/%s on %s, as %s (billable per build-minute: a 13-minute build on E2_HIGHCPU_8 is about $0.21); it builds, smoke-tests and promotes the image into %s and records it",
		repo, workflow, machineType, account, registry)
}

// buildNotConfirmable is the one-line refusal of a Cloud Build that cannot be
// confirmed under conds: in a coding agent's session the agent refusal, else
// the route to the typed confirmation.
func buildNotConfirmable(conds initflow.Conditions, repo, workflow string) error {
	if conds.Agent != "" {
		return userErr("%s", initflow.AgentRefusal(conds.Agent))
	}
	return userErr("a Cloud Build is billable, so its confirmation (the project's name) is typed at a real terminal: --json, a pipe and a coding agent never give it; run fugaro image build --repo %s --workflow %s in your own terminal window", repo, workflow)
}

// confirmBuild shows the ⚠ CONFIRM banner on stderr and reads the project's
// name from the terminal; anything else declines.
func confirmBuild(cmd *cobra.Command, project, gcpProject, what string) error {
	w := cmd.ErrOrStderr()
	fmt.Fprintf(w, "⚠ CONFIRM (project %s, GCP project %s): %s\n", project, gcpProject, what)
	fmt.Fprintf(w, "Type %s to submit the build: ", project)
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return userErr("reading the confirmation: %v", err)
	}
	if strings.TrimSpace(line) != project {
		return userErr("not confirmed (the project's name was not typed); no build was submitted")
	}
	return nil
}

// buildRecordNote is a warning when bucket_url names a bucket other than
// the runs bucket, where a build always records its image (the only one
// its account may write), or "".
func buildRecordNote(lc *localcfg.Config) string {
	rec := lc.RecordBucketURL()
	switch {
	case lc.Bucket == "" || lc.Bucket == rec:
		return ""
	case recordReadURL(lc) == rec:
		return fmt.Sprintf("the build records its image in the runs bucket %s (the only bucket its account may write), not in bucket_url %s; ls and image status read it there", rec, lc.Bucket)
	}
	return fmt.Sprintf("the build records its image in the runs bucket %s (the only bucket its account may write), but ls and image status read bucket_url %s, so they won't see it", rec, lc.Bucket)
}

// cloudBuildSpec is the Cloud Build build of workflow name of the
// repository rs, from base, recording to bucket (gs://…, the local
// config's RecordBucketURL): the one spec fugaro image build, fugaro
// init's first build and the daily image check submit.
func cloudBuildSpec(rs infra.RepoSpec, cfg *config.Config, name, base, machineType, bucket string) (gcp.BuildSpec, error) {
	ws, ok := rs.Workflows[name]
	if !ok {
		return gcp.BuildSpec{}, userErr("fugaro.yaml has no workflow %q", name)
	}
	return gcp.BuildSpec{
		Slug: rs.Slug, GitProvider: rs.Provider, RepoURL: rs.RepoURL, BaseBranch: cmp.Or(rs.BaseBranch, "main"), Workflow: name, Base: base,
		Image:       gcp.ImageName(rs.RegistryPath, rs.Slug, name),
		GitSecretID: rs.Secrets[ws.GitSecret], GitUser: rs.GitUser, GitHubAppID: rs.GitHubAppID,
		ServiceAccount: rs.BuildServiceAccountEmail, MachineType: machineType,
		WorkflowSecrets: cfg.Workflows[name].Secrets,
		Bucket:          bucket,
	}, nil
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
	var workflow, cloudOutputs string
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
			if cloudOutputs != "" {
				if err := writeCloudOutputs(cmd.Context(), root, cfg, name, cloudOutputs); err != nil {
					return err
				}
			}
			_, err = cmd.OutOrStdout().Write(data)
			return err
		},
	}
	cmd.Flags().StringVar(&workflow, "workflow", "", "workflow to render; optional when fugaro.yaml defines one")
	cmd.Flags().StringVar(&cloudOutputs, "cloud-outputs", "", "also write, into this directory, what the Cloud Build steps after render need (used by the image build)")
	_ = cmd.Flags().MarkHidden("cloud-outputs")
	return cmd
}

// writeCloudOutputs writes, into dir, what the Cloud Build steps after
// render read: selftest.json (the smoke's spec, image.SpecForCloud),
// record.json (the build record's source side, which record completes),
// and source-commit and built-at (the image's labels).
func writeCloudOutputs(ctx context.Context, root string, cfg *config.Config, name, dir string) error {
	git := func(args ...string) (string, error) {
		c := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
		c.WaitDelay = 5 * time.Second
		out, err := c.Output()
		if err != nil {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return strings.TrimSpace(string(out)), nil
	}
	commit, err := git("rev-parse", "HEAD")
	if err != nil {
		return err
	}
	committed, err := git("show", "-s", "--format=%cI", "HEAD")
	if err != nil {
		return err
	}
	commitTime, err := time.Parse(time.RFC3339, committed)
	if err != nil {
		return fmt.Errorf("the commit time %q: %w", committed, err)
	}
	branch, err := git("symbolic-ref", "--short", "HEAD")
	if err != nil {
		branch = cfg.Git.BaseBranch
	}
	origin, err := git("remote", "get-url", "origin")
	if err != nil {
		return userErr("no origin remote in this checkout")
	}
	origin = image.HTTPSOrigin(origin)
	repo, ok := repoFromOrigin(origin)
	if !ok {
		return userErr("origin %s does not name owner/name", gcp.RedactURL(origin))
	}
	// Git's own view of HEAD, as the check reads the branch: blob IDs of
	// what git stores, not of the checkout's bytes.
	tree, err := imagecheck.Open(ctx, root, "HEAD", nil)
	if err != nil {
		return err
	}
	keys, err := imagecheck.KeyFiles(cfg, name, tree)
	if err != nil {
		return userErr("%v", err)
	}
	configHash, err := imagecheck.ImageConfigHash(cfg, name, tree)
	if err != nil {
		return userErr("%v", err)
	}
	spec, err := image.SpecForCloud(cfg, name, commit, origin)
	if err != nil {
		return userErr("%v", err)
	}
	rec := imagecheck.Record{
		Version: imagecheck.RecordVersion, Repo: repo, Workflow: name, BuiltAt: time.Now().UTC().Truncate(time.Second),
		SourceCommit: commit, SourceCommitTime: commitTime.UTC(), BaseBranch: branch,
		KeyFiles: keys, ImageConfigHash: configHash, FugaroVersion: Version, TemplateSalt: gcp.TemplateSalt(Version),
	}
	specJSON, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	recJSON, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	for file, data := range map[string][]byte{
		"selftest.json": specJSON, "record.json": recJSON,
		"source-commit": []byte(commit + "\n"), "built-at": []byte(rec.BuiltAt.Format(time.RFC3339) + "\n"),
	} {
		if err := os.WriteFile(filepath.Join(dir, file), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// loadCheckout finds the git checkout containing the working directory,
// loads its fugaro.yaml, validates and checks it the way `fugaro validate`
// does, and selects the workflow.
func loadCheckout(ctx context.Context, workflow string) (root string, cfg *config.Config, name string, err error) {
	if root, cfg, err = loadCheckoutConfig(ctx); err != nil {
		return "", nil, "", err
	}
	if name, _, err = cfg.SelectWorkflow(workflow); err != nil {
		return "", nil, "", &ExitError{Code: ExitUserError, Err: err}
	}
	return root, cfg, name, nil
}

// loadCheckoutConfig is loadCheckout without selecting a workflow.
func loadCheckoutConfig(ctx context.Context) (root string, cfg *config.Config, err error) {
	return loadCheckoutConfigAt(ctx, "")
}

// loadCheckoutConfigAt is loadCheckoutConfig for the checkout holding dir
// (empty: the current directory).
func loadCheckoutConfigAt(ctx context.Context, dir string) (root string, cfg *config.Config, err error) {
	args := []string{"rev-parse", "--show-toplevel"}
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		if dir != "" {
			return "", nil, &ExitError{Code: ExitUserError, Err: fmt.Errorf("%s is not inside a git checkout; point at the repository's checkout", dir)}
		}
		return "", nil, &ExitError{Code: ExitUserError, Err: errors.New("not inside a git checkout; run this from the repository")}
	}
	root = strings.TrimSpace(string(out))
	data, err := readFugaroYAML(filepath.Join(root, "fugaro.yaml"))
	if err != nil {
		return "", nil, &ExitError{Code: ExitUserError, Err: fmt.Errorf("%w; create it with /fugaro:setup or fugaro config example", err)}
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
		return "", nil, &ExitError{Code: ExitUserError, Err: fmt.Errorf("fugaro.yaml has %d problem(s), see fugaro validate:\n  %s", len(problems), strings.Join(msgs, "\n  "))}
	}
	return root, cfg, nil
}
