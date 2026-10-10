package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
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
			failed := fanOutLayer(ctx, cmd.OutOrStdout(), env, l, gen)
			warnOldImages(ctx, cmd.ErrOrStderr(), env, l)
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
		// prev is parsed from old (the object as it is in the bucket right
		// now), never aliased to l (the new layer this call is about to
		// write): comparing l to itself would always show zero executable
		// changes and silently defeat the whole --executable-changes gate.
		op, invalid := config.ParseProjectLayer(old, config.LayerAnchor{})
		switch {
		case string(old) == string(l.Raw):
			fmt.Fprintln(w, "note: the project already has this exact layer; writing it again")
			prev = op
		case len(invalid) > 0:
			// The old object is untrusted and unvalidated: never echo it
			// (it may hold a credential a hand edit put there), and never
			// diff against it. prev stays nil, so every executable key of
			// l counts as changed (decision L7's fail-closed default).
			fmt.Fprintf(w, "replacing the project layer (sha256 %s -> %s): the published layer is invalid (%d problem(s)); every executable key counts as changed\n", config.LayerSum(old), l.SHA256, len(invalid))
		default:
			prev = op
			fmt.Fprintf(w, "replacing the project layer (sha256 %s -> %s):\n%s", config.LayerSum(old), l.SHA256, layerDiffText(string(old), string(l.Raw)))
		}
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
		return 0, operatorWriteErr("nothing was published", "gs://"+b.GCSName, config.LayerKey, err)
	case err != nil:
		return 0, remote(fmt.Errorf("nothing was published: writing %s: %w", config.LayerKey, err))
	}
	return gen, nil
}

// diffMaxCells caps lineDiff's longest-common-subsequence table (len(a's
// lines) * len(b's lines) int entries): two 64 KiB objects of short lines
// would need gigabytes. Past the cap, the banner says so instead of
// building the table.
const diffMaxCells = 1_000_000

// diffText is the banner's line diff of a project layer's old and new
// text, or a note instead of one too large to build safely.
func layerDiffText(a, b string) string {
	x, y := splitLines(a), splitLines(b)
	if n := len(x) * len(y); n > diffMaxCells {
		return fmt.Sprintf("  (the diff is skipped: %d and %d lines would need a table of %d cells)\n", len(x), len(y), n)
	}
	return printableLines(lineDiff(a, b))
}

// executableChanges lists, per profile, each executable key
// (config.ExecutableKeys) whose stored value differs between prev (nil:
// none published, or an invalid old object) and next, as
// `profile p: commands.test: "old" -> "new"`, plus one line when
// default_profile changes and either profile names has an executable
// setting (decision L7; the owner's ruling on default_profile).
func executableChanges(prev, next *config.ProjectLayer) []string {
	values := func(l *config.ProjectLayer, name string) map[string]string {
		var p config.Profile
		if l != nil {
			// A missing profile reads as the zero Profile{}, exactly what
			// an explicitly empty one decodes to: adding or removing a
			// profile with no executable keys is not a change.
			p = l.Profiles[name]
		}
		out := map[string]string{}
		for _, key := range config.ExecutableKeys {
			executableKeyValues(out, p, key)
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
		keys := map[string]bool{}
		for k := range a {
			keys[k] = true
		}
		for k := range b {
			keys[k] = true
		}
		for _, k := range slices.Sorted(maps.Keys(keys)) {
			if a[k] != b[k] {
				// a[k], b[k] are already display-ready (strconv.Quote or
				// %q, done once in executableKeyValues): not quoted again.
				out = append(out, fmt.Sprintf("profile %s: %s: %s -> %s", name, k, a[k], b[k]))
			}
		}
	}
	if c := defaultProfileChange(prev, next); c != "" {
		out = append(out, c)
	}
	return out
}

// executableKeyValues inserts into out the display label(s) and value(s)
// of profile p's key, an entry of config.ExecutableKeys, with every value
// already quoted for the banner. commands.rerun_failed is two entries
// (its Command and Each quoted separately, never concatenated: two
// different (command, each) pairs could otherwise join into the same
// text, e.g. {"pytest -k", "{id}"} and {"pytest", "-k {id}"}). A key with
// no case here would publish unreviewed: fail closed.
func executableKeyValues(out map[string]string, p config.Profile, key string) {
	switch key {
	case "workflows.*.commands.build":
		out["commands.build"] = strconv.Quote(p.Commands.Build)
	case "workflows.*.commands.test":
		out["commands.test"] = strconv.Quote(p.Commands.Test)
	case "workflows.*.commands.rerun_failed":
		var cmd, each string
		if rf := p.Commands.RerunFailed; rf != nil {
			cmd, each = rf.Command, rf.Each
		}
		out["commands.rerun_failed.command"] = strconv.Quote(cmd)
		out["commands.rerun_failed.each"] = strconv.Quote(each)
	case "workflows.*.image.apt":
		// %q of the whole list, not of each entry, so a reorder or a step
		// split into two (or joined into one) is a change.
		out["image.apt"] = fmt.Sprintf("%q", p.Image.Apt)
	case "workflows.*.image.setup":
		out["image.setup"] = fmt.Sprintf("%q", p.Image.Setup)
	default:
		panic("executableChanges: no comparison for " + key)
	}
}

// defaultProfileChange is the change line for default_profile itself
// (first set, or a -> b), when either the old or the new profile
// HasExecutable: repositories with no workflows: run that profile's
// commands, so changing it changes what they run even though no
// profile's own fields moved.
func defaultProfileChange(prev, next *config.ProjectLayer) string {
	var oldDefault string
	var oldProfile config.Profile
	if prev != nil {
		oldDefault = prev.DefaultProfile
		oldProfile = prev.Profiles[oldDefault]
	}
	newDefault := next.DefaultProfile
	newProfile := next.Profiles[newDefault]
	if oldDefault == newDefault || (!oldProfile.HasExecutable() && !newProfile.HasExecutable()) {
		return ""
	}
	return fmt.Sprintf("default_profile: %q -> %q (repositories without workflows: now run profile %s's commands)", oldDefault, newDefault, newDefault)
}

// fanOutLayer copies l's exact bytes to every repository the installation
// config lists (decision L19), reporting each, and returns how many copies
// failed. A repository whose provider neither the installation config nor
// the layer's defaults names has no slug yet; it is skipped with a note,
// and its next image build or init --repo writes its copy. Before each
// copy it re-reads config.LayerKey's generation: if it is no longer gen
// (another publisher replaced it while this fan-out ran), the remaining
// repositories are not touched with a stale layer; they count as failed,
// and a plain fugaro config publish fans the current object out again.
func fanOutLayer(ctx context.Context, w io.Writer, env *cloudEnv, l *config.ProjectLayer, gen int64) (failed int) {
	repos := slices.Sorted(maps.Keys(env.lc.Repos))
	for i, repo := range repos {
		if _, curGen, err := env.bucket.ReadMaxStrict(ctx, config.LayerKey, config.LayerMaxBytes); err != nil || curGen != gen {
			fmt.Fprintf(w, "  stopped: another publisher changed %s while this fan-out ran; run fugaro config publish again to fan out the current layer (%d of %d repositories not yet copied)\n",
				config.LayerKey, len(repos)-i, len(repos))
			return failed + (len(repos) - i)
		}
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
func warnOldImages(ctx context.Context, w io.Writer, env *cloudEnv, l *config.ProjectLayer) {
	b, err := env.recordBucket(ctx)
	if err != nil {
		return
	}
	for _, repo := range slices.Sorted(maps.Keys(env.lc.Repos)) {
		r := env.lc.Repos[repo]
		provider := r.Provider
		if provider == "" {
			provider = l.Defaults.Git.Provider
		}
		slug, err := task.Slug(provider, repo)
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
