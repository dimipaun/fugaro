package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/task"
)

func newImageCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "image", Short: "Build and inspect a workflow's derived container image"}
	cmd.AddCommand(newImageBuildCmd(), newImageRenderCmd(), newImageSelftestCmd(), newImageGitCredentialCmd(),
		newImageGateCmd(), newImageRecordCmd())
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
			"fugaro.yaml and origin say what to build.",
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
	f.BoolVar(&o.noSmoke, "no-smoke", false, "skip the smoke test of the built image (on Cloud Build, latest is then promoted unsmoked)")
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
		base = lc.BaseImage
	}
	if base == "" {
		if base, err = image.BaseRef(cfg.Workflows[name].Base, Version); err != nil {
			return userErr("%v", err)
		}
	}
	// Every name comes from the repository's spec, as fugaro init --repo
	// creates them: the build account, the registry (refused when its host
	// names another project than --project's) and the provider
	// credential. The spec's check job needs a base image, which this
	// build doesn't use; the one the build uses stands in.
	specLC := *lc
	specLC.BaseImage = cmp.Or(specLC.BaseImage, base)
	rs, err := infra.Repo(infra.Inputs{LC: &specLC, Repo: repo, Cfg: cfg, RepoURL: repoURL})
	if err != nil {
		return userErr("%v", err)
	}
	ws, ok := rs.Workflows[name]
	if !ok {
		return userErr("fugaro.yaml has no workflow %q", name)
	}
	spec := gcp.BuildSpec{
		Slug: rs.Slug, GitProvider: rs.Provider, RepoURL: rs.RepoURL, BaseBranch: cmp.Or(rs.BaseBranch, "main"), Workflow: name, Base: base,
		Image:       gcp.ImageName(rs.RegistryPath, rs.Slug, name),
		GitSecretID: rs.Secrets[ws.GitSecret], GitUser: rs.GitUser, GitHubAppID: rs.GitHubAppID,
		ServiceAccount: rs.BuildServiceAccountEmail, MachineType: lc.Build.MachineType,
		WorkflowSecrets: cfg.Workflows[name].Secrets,
		// The record always goes to the runs bucket on GCS, whatever
		// bucket_url says for local runs.
		Bucket: "gs://" + lc.RunsBucket, NoSmoke: o.noSmoke,
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
		return userErr("project %s has no image registry %s for %s yet, so the build would have nowhere to push. fugaro init --repo creates it: run that from this checkout first", lc.Project, rs.RegistryPath, repo)
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
	tree := imagecheck.Dir{Root: root}
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

// gateState is what the gate read, next to the record as gate.json, so
// the record step writes only over that same object.
type gateState struct {
	Exists     bool   `json:"exists"`
	Generation int64  `json:"generation"`
	Previous   []byte `json:"previous,omitempty"`
}

// buildNameRE is what a slug or workflow in a record key may be.
var buildNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// buildRecordKey is the record's key for slug and ours' workflow.
func buildRecordKey(slug string, ours *imagecheck.Record) (string, error) {
	if !buildNameRE.MatchString(slug) || !buildNameRE.MatchString(ours.Workflow) {
		return "", userErr("the slug %q or workflow %q is not a name", slug, ours.Workflow)
	}
	return imagecheck.RecordKey(slug, ours.Workflow), nil
}

// readBuildRecord reads the record file the render step wrote.
func readBuildRecord(path string) (*imagecheck.Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, userErr("%v", err)
	}
	rec, err := imagecheck.ParseRecord(data)
	if err != nil {
		return nil, userErr("%s: %v", path, err)
	}
	return rec, nil
}

// newImageGateCmd is the build's gate step: `fugaro image gate`. It reads
// the current record and, when that is newer than the one this build
// would write, writes superseded next to --record, so the promote, record
// and untag steps do nothing. Otherwise it writes gate.json there, the
// generation it read, which the record step writes against.
func newImageGateCmd() *cobra.Command {
	var record, slug, bucket string
	cmd := &cobra.Command{
		Use:    "gate",
		Short:  "Stop an image build from promoting over a newer one (a Cloud Build step)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			ours, err := readBuildRecord(record)
			if err != nil {
				return err
			}
			key, err := buildRecordKey(slug, ours)
			if err != nil {
				return err
			}
			b, err := blobx.Open(ctx, bucket)
			if err != nil {
				return userErr("opening the bucket %s: %v", bucket, err)
			}
			defer b.Close()
			dir := filepath.Dir(record)
			cur, gen, err := b.Read(ctx, key)
			state := gateState{Exists: err == nil, Generation: gen, Previous: cur}
			switch {
			case errors.Is(err, blobx.ErrNotExist):
				fmt.Fprintf(cmd.OutOrStdout(), "no record at %s yet: promoting\n", key)
			case err != nil:
				return remote(fmt.Errorf("reading %s: %w", key, err))
			default:
				curRec, perr := imagecheck.ParseRecord(cur)
				switch {
				case perr != nil:
					fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: the record at %s is unreadable (%v); replacing it\n", key, perr)
				case curRec.NewerThan(ours):
					if err := os.WriteFile(filepath.Join(dir, "superseded"), nil, 0o644); err != nil {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "superseded: %s records commit %s (committed %s, built %s), newer than this build's %s (committed %s): not promoting\n",
						key, oneLine(curRec.SourceCommit), curRec.SourceCommitTime.Format(time.RFC3339), curRec.BuiltAt.Format(time.RFC3339),
						oneLine(ours.SourceCommit), ours.SourceCommitTime.Format(time.RFC3339))
					return nil
				default:
					fmt.Fprintf(cmd.OutOrStdout(), "%s records commit %s (committed %s), not newer: promoting\n",
						key, oneLine(curRec.SourceCommit), curRec.SourceCommitTime.Format(time.RFC3339))
				}
			}
			data, err := json.Marshal(state)
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dir, "gate.json"), data, 0o644)
		},
	}
	f := cmd.Flags()
	f.StringVar(&record, "record", "", "the record this build would write (render's record.json); superseded and gate.json go next to it")
	f.StringVar(&slug, "slug", "", "the repository's storage slug")
	f.StringVar(&bucket, "bucket", "", "the runs bucket, as a gocloud URL (gs://…)")
	for _, name := range []string{"record", "slug", "bucket"} {
		_ = cmd.MarkFlagRequired(name)
	}
	return cmd
}

// readDigest reads a digest file (sha256:…, or <image>@sha256:…).
func readDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", userErr("%v", err)
	}
	v := strings.TrimSpace(string(data))
	if _, after, ok := strings.Cut(v, "@"); ok {
		v = after
	}
	if !digestRE.MatchString(v) {
		return "", userErr("%s does not hold an image digest (sha256:…)", path)
	}
	return v, nil
}

var digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// newImageRecordCmd is the build's record step: `fugaro image record`. It
// completes render's record with what the build made, and writes it to
// the runs bucket, only over the object the gate read: a record written
// in between (a concurrent build) fails this step instead.
func newImageRecordCmd() *cobra.Command {
	var in, digestFrom, baseDigestFrom, img, base, buildID, slug, bucket string
	cmd := &cobra.Command{
		Use:    "record",
		Short:  "Record what an image build built (a Cloud Build step)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			dir := filepath.Dir(in)
			if _, err := os.Stat(filepath.Join(dir, "superseded")); err == nil {
				fmt.Fprintln(cmd.OutOrStdout(), "superseded: a newer build is recorded; nothing to record")
				return nil
			}
			rec, err := readBuildRecord(in)
			if err != nil {
				return err
			}
			key, err := buildRecordKey(slug, rec)
			if err != nil {
				return err
			}
			if rec.ImageDigest, err = readDigest(digestFrom); err != nil {
				return err
			}
			if rec.BaseDigest, err = readDigest(baseDigestFrom); err != nil {
				return err
			}
			if img == "" || base == "" || buildID == "" {
				return userErr("--image, --base and --build-id are required")
			}
			rec.Image, rec.BaseRef, rec.BuildID, rec.Adopted = img, base, buildID, false
			var state gateState
			data, err := os.ReadFile(filepath.Join(dir, "gate.json"))
			if err == nil {
				err = json.Unmarshal(data, &state)
			}
			if err != nil {
				return userErr("reading what the gate step read (gate.json): %v; the gate must run first", err)
			}
			out, err := json.MarshalIndent(rec, "", "  ")
			if err != nil {
				return err
			}
			b, err := blobx.Open(ctx, bucket)
			if err != nil {
				return userErr("opening the bucket %s: %v", bucket, err)
			}
			defer b.Close()
			if state.Exists {
				_, err = b.ReplaceIf(ctx, key, out, state.Generation, state.Previous)
			} else {
				_, err = b.Create(ctx, key, out, "application/json")
			}
			switch {
			case errors.Is(err, blobx.ErrConflict), errors.Is(err, blobx.ErrExists):
				return remote(fmt.Errorf("%s changed since the gate read it (another build recorded meanwhile); not overwriting it: the next image check compares that record with latest", key))
			case err != nil:
				return remote(fmt.Errorf("writing %s: %w", key, err))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "recorded %s: %s@%s from commit %s\n", key, oneLine(rec.Image), rec.ImageDigest, oneLine(rec.SourceCommit))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&in, "in", "", "render's record.json; gate.json and superseded are read next to it")
	f.StringVar(&digestFrom, "digest-from", "", "file holding the promoted image's digest")
	f.StringVar(&baseDigestFrom, "base-digest-from", "", "file holding the base image's digest (<image>@sha256:…)")
	f.StringVar(&img, "image", "", "the image, without a tag")
	f.StringVar(&base, "base", "", "the base image as the build was given it")
	f.StringVar(&buildID, "build-id", "", "the Cloud Build build ID")
	f.StringVar(&slug, "slug", "", "the repository's storage slug")
	f.StringVar(&bucket, "bucket", "", "the runs bucket, as a gocloud URL (gs://…)")
	for _, name := range []string{"in", "digest-from", "base-digest-from", "slug", "bucket"} {
		_ = cmd.MarkFlagRequired(name)
	}
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
