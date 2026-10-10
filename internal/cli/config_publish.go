package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/task"
)

// layeredSince is the first fugaro release whose runner reads a task's
// project layer and fugaro.yaml's profile keys. It belongs with
// recipesSince in internal/cli/recipes_skew.go (layered-config plan Task
// 11, not yet merged); this task only needs it for warnOldImages, so it is
// defined here for now. Task 11 should reuse this definition rather than
// redeclare it.
const layeredSince = "0.6.0"

func newConfigPublishCmd() *cobra.Command {
	var (
		o          cloudOptions
		executable bool
	)
	cmd := &cobra.Command{
		Use:   "publish FILE",
		Short: "Publish the project layer: defaults and profiles for every repository of the project (fugaro/project-layer.yaml in the runs bucket)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if m := agentMarker(os.Getenv); m != "" {
				return userErr("nothing was published: fugaro config publish writes to the cloud and changes what every repository of the project runs: %s", initflow.AgentRefusal(m))
			}
			data, err := readLayerFile(args[0])
			if err != nil {
				return userErr("nothing was published: %v", err)
			}
			env, err := openCloud(ctx, o)
			if err != nil {
				return err
			}
			defer env.Close()
			if note := projectRecipesNote(env.lc); note != "" {
				return userErr("nothing was published: %s", strings.Replace(note, "project recipes need", "the project layer needs", 1))
			}
			if fakeEndpointsOnGS(env.lc, env.lc.BucketURL()) {
				return userErr("nothing was published: the project's storage endpoint is a fake")
			}
			l, ps := config.ParseProjectLayer(data, config.LayerAnchor{Project: env.lc.Name, GCPProject: env.lc.GCPProject})
			if len(ps) > 0 {
				return userErr("nothing was published: %s is invalid: %s", args[0], pluginwire.Printable(layerProblemsText(ps)))
			}
			gen, err := publishLayer(ctx, cmd.ErrOrStderr(), env.bucket, l, executable)
			if err != nil {
				return err
			}
			bucket := "fugaro-runs-" + env.lc.GCPProject
			_ = localcfg.SaveLayerCache(os.Getenv, env.lc.Name, localcfg.SharedCacheEntry{GCPProject: env.lc.GCPProject, Bucket: bucket, Generation: gen, CheckedAt: time.Now(), YAML: string(data)})
			fmt.Fprintf(cmd.OutOrStdout(), "published the project layer of %s to gs://%s/%s (generation %d, sha256 %s)\n", env.lc.Name, bucket, config.LayerKey, gen, l.SHA256)
			failed := fanOutLayer(ctx, cmd.OutOrStdout(), env, l)
			warnOldImages(ctx, cmd.ErrOrStderr(), env)
			if failed > 0 {
				return remote(fmt.Errorf("the project layer is published, but %d repository copy(ies) failed (listed above); run fugaro config publish again", failed))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&executable, "executable-changes", false, "allow a change to a profile's commands, image.apt or image.setup (they run as shell in every repository that takes the profile)")
	addCloudFlags(cmd, &o)
	return cmd
}

// layerPublishRace is a test seam between publish's read of the existing
// object and its conditional write.
var layerPublishRace = func(ctx context.Context, b *blobx.Bucket) {}

// publishLayer writes l to config.LayerKey, showing on w what it replaces
// and every executable change, and only if the object is still the one it
// read (or still absent): a concurrent publisher is refused, never
// overwritten. An executable change needs executable (decision L7). It
// returns the new generation.
func publishLayer(ctx context.Context, w io.Writer, b *blobx.Bucket, l *config.ProjectLayer, executable bool) (int64, error) {
	old, gen, rerr := b.ReadMaxStrict(ctx, config.LayerKey, config.LayerMaxBytes)
	var prev *config.ProjectLayer
	switch {
	case rerr == nil:
		if string(old) == string(l.Raw) {
			fmt.Fprintln(w, "note: the project already has this exact layer; writing it again")
		} else {
			fmt.Fprintf(w, "replacing the project layer (sha256 %s -> %s):\n%s", config.LayerSum(old), l.SHA256, printableLines(lineDiff(string(old), string(l.Raw))))
		}
		// An unparseable old object compares as empty: every executable
		// key of the new one counts as changed.
		prev, _ = config.ParseProjectLayer(old, config.LayerAnchor{})
	case errors.Is(rerr, blobx.ErrNotExist):
	case errors.Is(rerr, blobx.ErrTooLarge):
		return 0, userErr("nothing was published: the existing %s is over the %d KiB limit: delete it by hand, then run this again", config.LayerKey, config.LayerMaxBytes>>10)
	default:
		return 0, remote(fmt.Errorf("nothing was published: reading %s before replacing it: %w", config.LayerKey, rerr))
	}
	if changes := executableChanges(prev, l); len(changes) > 0 {
		fmt.Fprintln(w, "=== EXECUTABLE CHANGES: these run as shell in every repository that takes the profile ===")
		for _, c := range changes {
			fmt.Fprintln(w, "  "+pluginwire.Printable(c))
		}
		if !executable {
			return 0, userErr("nothing was published: the layer changes commands or image steps (shown above); review them, then run again with --executable-changes")
		}
	}
	layerPublishRace(ctx, b)
	var err error
	if rerr == nil {
		gen, err = b.ReplaceIfType(ctx, config.LayerKey, l.Raw, "application/yaml", gen, old)
	} else {
		gen, err = b.Create(ctx, config.LayerKey, l.Raw, "application/yaml")
	}
	switch {
	case errors.Is(err, blobx.ErrConflict), errors.Is(err, blobx.ErrExists):
		return 0, userErr("nothing was published: another publisher changed %s while this ran; look at it (fugaro config layer) and run this again", config.LayerKey)
	case errors.Is(err, blobx.ErrForbidden):
		return 0, operatorWriteErr("gs://"+b.GCSName, config.LayerKey, err)
	case err != nil:
		return 0, remote(fmt.Errorf("nothing was published: writing %s: %w", config.LayerKey, err))
	}
	return gen, nil
}

// executableChanges lists, per profile, each executable key
// (config.ExecutableKeys) whose value differs between prev (nil: none) and
// next, as `profile p: commands.test: "old" -> "new"`.
func executableChanges(prev, next *config.ProjectLayer) []string {
	values := func(l *config.ProjectLayer, name string) map[string]string {
		out := map[string]string{}
		if l == nil {
			return out
		}
		p, ok := l.Profiles[name]
		if !ok {
			return out
		}
		out["commands.build"], out["commands.test"] = p.Commands.Build, p.Commands.Test
		if rf := p.Commands.RerunFailed; rf != nil {
			out["commands.rerun_failed"] = rf.Command + " " + rf.Each
		}
		if len(p.Image.Apt) > 0 {
			out["image.apt"] = fmt.Sprintf("%q", p.Image.Apt)
		}
		if len(p.Image.Setup) > 0 {
			out["image.setup"] = fmt.Sprintf("%q", p.Image.Setup)
		}
		return out
	}
	names := map[string]bool{}
	for _, l := range []*config.ProjectLayer{prev, next} {
		if l != nil {
			for n := range l.Profiles {
				names[n] = true
			}
		}
	}
	var out []string
	for _, name := range slices.Sorted(maps.Keys(names)) {
		a, b := values(prev, name), values(next, name)
		for _, k := range []string{"commands.build", "commands.test", "commands.rerun_failed", "image.apt", "image.setup"} {
			if a[k] != b[k] {
				out = append(out, fmt.Sprintf("profile %s: %s: %q -> %q", name, k, a[k], b[k]))
			}
		}
	}
	return out
}

// fanOutLayer copies l's exact bytes to every repository the installation
// config lists (decision L19), reporting each, and returns how many copies
// failed. A repository whose provider neither the installation config nor
// the layer's defaults names has no slug yet; it is skipped with a note,
// and its next image build or init --repo writes its copy.
func fanOutLayer(ctx context.Context, w io.Writer, env *cloudEnv, l *config.ProjectLayer) (failed int) {
	for _, repo := range slices.Sorted(maps.Keys(env.lc.Repos)) {
		provider := env.lc.Repos[repo].Provider
		if provider == "" {
			provider = l.Defaults.Git.Provider
		}
		if provider == "" {
			fmt.Fprintf(w, "  %s: skipped: no provider is known for it yet; its next fugaro image build or fugaro init --repo writes its copy\n", repo)
			continue
		}
		slug, err := task.Slug(provider, repo)
		if err == nil {
			err = writeLayerCopy(ctx, env.bucket, slug, l.Raw)
		}
		if err != nil {
			fmt.Fprintf(w, "  %s: not copied: %s\n", repo, oneLineCLI(err.Error()))
			failed++
			continue
		}
		fmt.Fprintf(w, "  %s: copied to %s\n", repo, config.LayerCopyKey(slug))
	}
	return failed
}

// warnOldImages names every listed workflow whose build record says its job
// image predates layeredSince (decision L14): its launches are refused until
// fugaro image refresh. Best effort: a record it cannot read says nothing.
func warnOldImages(ctx context.Context, w io.Writer, env *cloudEnv) {
	b, err := env.recordBucket(ctx)
	if err != nil {
		return
	}
	for _, repo := range slices.Sorted(maps.Keys(env.lc.Repos)) {
		r := env.lc.Repos[repo]
		slug, err := task.Slug(r.Provider, repo)
		if err != nil {
			continue
		}
		for _, wf := range r.Workflows {
			data, _, err := b.Read(ctx, imagecheck.RecordKey(slug, wf))
			if err != nil {
				continue
			}
			rec, err := imagecheck.ParseRecord(data)
			if err != nil {
				continue
			}
			if m := baseRefReleaseRE.FindStringSubmatch(rec.BaseRef); m != nil && imagePredates(m[2], layeredSince) {
				fmt.Fprintf(w, "warning: %s workflow %s runs a job image built from %s (release %s), older than %s: its launches are refused until you run fugaro image refresh in its checkout\n",
					repo, wf, pluginwire.Printable(rec.BaseRef), m[2], layeredSince)
			}
		}
	}
}
