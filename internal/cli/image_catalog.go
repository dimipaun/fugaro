package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/catalog"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// catalogEntryRow is one entry of `fugaro image ls` (D1143): the name, the
// tags a repository's needs: is matched against, whether it is the entry a
// job that names nothing runs, what it is built from, and the digest the last
// build of it recorded ("" when nothing built it yet).
type catalogEntryRow struct {
	Name        string   `json:"name"`
	Tags        []string `json:"tags,omitempty"`
	Default     bool     `json:"default,omitempty"`
	Source      string   `json:"source"`
	Description string   `json:"description,omitempty"`
	Digest      string   `json:"digest,omitempty"`
}

// catalogLsOut is fugaro image ls --json.
type catalogLsOut struct {
	Project string            `json:"project"`
	Entries []catalogEntryRow `json:"entries"`
}

// sharedRow is one entry of `fugaro image ls --shared`: the C8c design's
// columns, read from the shared-image lock.
type sharedRow struct {
	Key          string   `json:"key"`
	Image        string   `json:"image"`
	Digest       string   `json:"digest,omitempty"`
	BaseRef      string   `json:"base_ref,omitempty"`
	Repositories []string `json:"repositories,omitempty"`
	Jobs         []string `json:"jobs,omitempty"`
}

// catalogSharedOut is fugaro image ls --shared --json.
type catalogSharedOut struct {
	Project string      `json:"project"`
	Images  []sharedRow `json:"images"`
}

func newImageLsCmd() *cobra.Command {
	var (
		o      cloudOptions
		shared bool
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the project's image catalog entries, or the shared-image lock's with --shared",
		Long: "List what this project's image catalog names: each entry's name, the tags it\n" +
			"provides, which one is the default, what it is built from and the digest the\n" +
			"last build of it recorded.\n\n" +
			"The catalog is the project's; the installation's shared images are a different\n" +
			"inventory, and --shared prints those instead: each image's key, reference and\n" +
			"digest, and the repositories and jobs on it. The two are one command with a\n" +
			"flag, never two ls commands, so there is no question of which one to run.\n\n" +
			"A project with no catalog of its own lists nothing: fugaro image add writes\n" +
			"an entry, and fugaro update publishes the catalog to the project's policy\n" +
			"node (image_catalog), which is what a job reads.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runImageLs(cmd, o, shared, asJSON) },
	}
	cmd.Flags().BoolVar(&shared, "shared", false, "list the installation's shared images instead of the catalog")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o)
	return cmd
}

// runImageLs lists the catalog or the lock.
func runImageLs(cmd *cobra.Command, o cloudOptions, shared, asJSON bool) error {
	ctx := cmd.Context()
	env, err := openCloud(ctx, o)
	if err != nil {
		return err
	}
	defer env.Close()
	records, err := env.recordBucket(ctx)
	if err != nil {
		return remote(err)
	}
	if shared {
		return runImageLsShared(cmd, env, records, asJSON)
	}
	return runImageLsCatalog(cmd, env, records, asJSON)
}

func runImageLsCatalog(cmd *cobra.Command, env *cloudEnv, records *blobx.Bucket, asJSON bool) error {
	ctx := cmd.Context()
	// A node this machine cannot read is not a refusal for a listing: it shows
	// the catalog this machine's configuration holds, which is what it would
	// publish.
	cat, _ := effectiveCatalog(ctx, env)
	out := catalogLsOut{Project: env.lc.Name, Entries: []catalogEntryRow{}}
	if cat != nil {
		for _, e := range cat.Entries {
			out.Entries = append(out.Entries, catalogEntryRow{
				Name: e.Name, Tags: e.Tags, Default: e.Default,
				Source: catalogSourceLine(e.Source), Description: e.Description,
				Digest: catalogEntryDigest(ctx, env, records, e.Name),
			})
		}
	}
	w := cmd.OutOrStdout()
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	if len(out.Entries) == 0 {
		fmt.Fprintln(w, "this project's image catalog is empty: fugaro image add <name> --tags <tags> --dockerfile <owner/name:path> adds an entry, and fugaro update publishes it")
		return nil
	}
	for _, r := range out.Entries {
		line := fmt.Sprintf("%s  %s  [%s]  %s",
			oneLine(r.Name), catalogDefaultWord(r.Default), oneLine(strings.Join(r.Tags, " ")), oneLine(r.Source))
		if r.Digest != "" {
			line += "  " + oneLine(r.Digest)
		} else {
			line += "  (not built yet)"
		}
		if r.Description != "" {
			line += "  " + oneLine(r.Description)
		}
		fmt.Fprintln(w, line)
	}
	return nil
}

// runImageLsShared lists the shared-image lock (D1143). An installation with
// no lock has not migrated to shared images, which is not an error: the
// listing says there are none.
func runImageLsShared(cmd *cobra.Command, env *cloudEnv, records *blobx.Bucket, asJSON bool) error {
	lock, err := imagecheck.ReadLock(cmd.Context(), records)
	if err != nil {
		return remote(err)
	}
	out := catalogSharedOut{Project: env.lc.Name, Images: []sharedRow{}}
	if lock != nil {
		for _, key := range slices.Sorted(maps.Keys(lock.Images)) {
			im := lock.Images[key]
			out.Images = append(out.Images, sharedRow{
				Key: key, Image: im.Image, Digest: im.Digest, BaseRef: im.BaseRef,
				Repositories: im.Repositories, Jobs: sharedJobNames(im.Jobs),
			})
		}
	}
	w := cmd.OutOrStdout()
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	if len(out.Images) == 0 {
		fmt.Fprintln(w, "this installation has no shared images: the lock is empty (or was never written), and no job runs one")
		return nil
	}
	for _, r := range out.Images {
		line := fmt.Sprintf("%s  %s", oneLine(r.Key), oneLine(r.Image))
		if r.Digest != "" {
			line += "  " + oneLine(r.Digest)
		}
		if len(r.Repositories) > 0 {
			line += "  repos: " + oneLine(strings.Join(r.Repositories, " "))
		}
		if len(r.Jobs) > 0 {
			line += "  jobs: " + oneLine(strings.Join(r.Jobs, " "))
		}
		fmt.Fprintln(w, line)
	}
	return nil
}

// sharedJobNames is the lock's job names, sorted and deduplicated: the map is
// keyed by repository, so two repositories may carry the same job name.
func sharedJobNames(jobs map[string]string) []string {
	out := make([]string, 0, len(jobs))
	for _, name := range jobs {
		if name != "" {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// catalogDefaultWord marks the entry a job that names nothing runs (D1146) in
// a listing, padded so the column holds whether or not an entry carries it.
func catalogDefaultWord(def bool) string {
	if def {
		return "default  "
	}
	return "         "
}

// catalogSourceLine is an entry's source as a person reads it in a listing.
func catalogSourceLine(s config.Source) string {
	switch s.Kind {
	case config.SourceDockerfile:
		at := s.Repo + ":" + s.Path
		if s.Branch != "" {
			at += "@" + s.Branch
		}
		return "dockerfile " + at
	case config.SourceInline:
		return "inline"
	case config.SourceImage:
		return "image " + s.Image
	}
	return string(s.Kind)
}

// effectiveCatalog is the catalog the project has: this machine's project
// config when it states one, the published node's otherwise (localcfg's keep
// rule, D1140), with the published node's read error. A node this machine
// cannot read is not a refusal — the catalog it answers with is what a
// listing shows, which is what this machine would publish — but a caller that
// has to choose between two different things says which one it got.
func effectiveCatalog(ctx context.Context, env *cloudEnv) (*config.Catalog, error) {
	published, err := publishedCatalog(ctx, env)
	return env.lc.ImageCatalogEffective(published), err
}

// publishedCatalog is the catalog in the project's published policy node, nil
// when nothing is published, with the error a node this machine cannot read
// is.
func publishedCatalog(ctx context.Context, env *cloudEnv) (*config.Catalog, error) {
	p, found, err := env.policy.read(ctx, env.lc)
	if err != nil {
		return nil, err
	}
	if !found || p == nil {
		return nil, nil
	}
	return p.ImageCatalog, nil
}

// catalogEntryDigest is the digest of the image the last build of entry
// recorded, "" when nothing built it. The record is per repository
// (gcp.CatalogRecordKey): a catalog build runs as the build account of the
// checkout it was submitted from, so an entry built from more than one
// repository has more than one record, and the first repository of the local
// config that has one answers. A record that does not parse is treated as
// absent, the way the daily check treats one.
func catalogEntryDigest(ctx context.Context, env *cloudEnv, records *blobx.Bucket, entry string) string {
	for _, repo := range slices.Sorted(maps.Keys(env.lc.Repos)) {
		slug, err := env.repoSlug(repo, func() *config.Config { return checkoutConfig(ctx, repo) })
		if err != nil {
			continue
		}
		key, err := gcp.CatalogRecordKey(slug, entry)
		if err != nil {
			continue
		}
		data, _, err := records.Read(ctx, key)
		if err != nil {
			continue
		}
		var st imagecheck.CatalogRecordState
		if json.Unmarshal(data, &st) != nil || st.ImageDigest == "" {
			continue
		}
		return st.ImageDigest
	}
	return ""
}

func newImageAddCmd() *cobra.Command {
	var (
		o                   cloudOptions
		tags                []string
		dockerfile, branch  string
		inline, description string
		def                 bool
	)
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add an image catalog entry to this machine's project config",
		Long: "Add a named image to the project's image catalog, in this machine's project\n" +
			"config: the copy fugaro update publishes to the project's policy node\n" +
			"(image_catalog). The entry's source is a Dockerfile in one of the project's\n" +
			"repositories (--dockerfile owner/name:path, with --branch) or a Dockerfile\n" +
			"written out here (--inline <file>), and its tags are what a repository's\n" +
			"needs: is matched against.\n\n" +
			"Nothing is built and nothing is published here: fugaro image build <name>\n" +
			"builds the entry's image, and fugaro update publishes the catalog. --default\n" +
			"marks the entry a job whose repositories name no needs runs; a catalog marks\n" +
			"one entry, or none.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runImageAdd(cmd, o, args[0], tags, dockerfile, branch, inline, description, def)
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&tags, "tags", nil, "the tags the image provides (node, postgres, redis, firebase-emulator, docker); comma-separated or repeated")
	f.StringVar(&dockerfile, "dockerfile", "", "a Dockerfile in one of the project's repositories, as owner/name:path")
	f.StringVar(&branch, "branch", "", "the branch --dockerfile reads (default: the repository's base branch)")
	f.StringVar(&inline, "inline", "", "a file whose contents are the entry's Dockerfile")
	f.StringVar(&description, "description", "", "what the entry is for, printed by fugaro image ls")
	f.BoolVar(&def, "default", false, "the entry a job whose repositories name no needs runs (one entry, or none)")
	addCloudFlags(cmd, &o)
	return cmd
}

// runImageAdd writes the entry into this machine's project config, backing
// the file up first (userSave, the rule every writer of this file follows).
// It never publishes: the node is fugaro update's (the HARD RULE), and an
// entry that reached the cloud from here would be a second publication path
// beside it.
func runImageAdd(cmd *cobra.Command, o cloudOptions, name string, tags []string, dockerfile, branch, inline, description string, def bool) error {
	ctx := cmd.Context()
	// The read of the project config is the operator's own and may fetch the
	// shared one, so the GODEBUG that prints requests with their credentials
	// is refused before anything is read (the rule every such command keeps).
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	e, err := catalogEntryFromFlags(name, tags, dockerfile, branch, inline, description, def)
	if err != nil {
		return err
	}
	path, onDisk, old, err := catalogConfig(ctx, o)
	if err != nil {
		return err
	}
	// A file that names no catalog states the whole catalog when it is
	// written: under the keep rule (D1140) the publisher takes this file's
	// catalog over the published one, so the first entry written here
	// replaces a catalog the node holds rather than adding to it. The note
	// is printed before the write, because that is when the operator can
	// still change their mind.
	if onDisk.ImageCatalog == nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "note: this machine's project config names no image catalog, so this entry is written as the project's whole catalog: if the project already has one published, it is replaced by this file's rather than added to. fugaro image ls prints the entries the project has")
	}
	next, err := catalogWith(onDisk, func(c *config.Catalog) error {
		if slices.ContainsFunc(c.Entries, func(x config.Entry) bool { return x.Name == e.Name }) {
			return userErr("this project's image catalog already has an entry named %s: fugaro image ls lists it, and fugaro image rm %s removes it",
				pluginwire.Printable(e.Name), pluginwire.Printable(e.Name))
		}
		c.Entries = append(c.Entries, e)
		return nil
	})
	if err != nil {
		return err
	}
	if err := imageCatalogGate(onDisk, "the catalog entry "+pluginwire.Printable(e.Name), cmd.ErrOrStderr()); err != nil {
		return err
	}
	if err := userSave(cmd.OutOrStdout(), path, old, next); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "the catalog entry %s is in this machine's project config: fugaro update publishes it, and fugaro image build %s builds its image\n",
		pluginwire.Printable(e.Name), pluginwire.Printable(e.Name))
	return nil
}

// catalogEntryFromFlags builds the entry the flags describe, refusing what is
// not exactly one definition: an entry holding two sources would leave which
// one a build reads unstated (validateCatalogSource's rule).
func catalogEntryFromFlags(name string, tags []string, dockerfile, branch, inline, description string, def bool) (config.Entry, error) {
	e := config.Entry{Name: name, Tags: tags, Description: description, Default: def}
	switch {
	case dockerfile != "" && inline != "":
		return config.Entry{}, userErr("--dockerfile and --inline are two definitions for one entry: set one of them")
	case dockerfile != "":
		repo, path, ok := strings.Cut(dockerfile, ":")
		if !ok || repo == "" || path == "" {
			return config.Entry{}, userErr("--dockerfile takes a Dockerfile in one of the project's repositories, as owner/name:path, not %s", pluginwire.Printable(dockerfile))
		}
		e.Source = config.Source{Kind: config.SourceDockerfile, Repo: repo, Path: path, Branch: branch}
	case inline != "":
		if branch != "" {
			return config.Entry{}, userErr("--branch applies to --dockerfile; an inline definition is read from the file you name")
		}
		data, err := os.ReadFile(inline)
		if err != nil {
			return config.Entry{}, userErr("reading %s: %v", pluginwire.Printable(inline), err)
		}
		e.Source = config.Source{Kind: config.SourceInline, Inline: string(data)}
	default:
		return config.Entry{}, userErr("the entry needs a definition: --dockerfile <owner/name:path> for a Dockerfile in one of the project's repositories, or --inline <file> for one written out here")
	}
	if problems := config.ValidateCatalog("image_catalog", config.Catalog{Entries: []config.Entry{e}}); len(problems) > 0 {
		return config.Entry{}, userErr("the entry %s: %s: %s", pluginwire.Printable(e.Name), problems[0].Path, problems[0].Message)
	}
	return e, nil
}

func newImageRmCmd() *cobra.Command {
	var o cloudOptions
	cmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove an image catalog entry from this machine's project config",
		Long: "Remove a named image from the project's image catalog, in this machine's\n" +
			"project config. The removal is refused while a repository's job still runs\n" +
			"the entry — because a workflow names it (image_name:), or because the\n" +
			"tag-cover rule picks it for the tags the workflow needs — since that job\n" +
			"would then be on an image no catalog describes. Point the repository at\n" +
			"another entry first, run fugaro update, and remove the entry after.\n\n" +
			"Nothing is deleted from the registry: the entry's built image stays where\n" +
			"it is, and the next fugaro update publishes the catalog without the entry.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runImageRm(cmd, o, args[0]) },
	}
	addCloudFlags(cmd, &o)
	return cmd
}

// runImageRm removes the entry, refusing while a repository's job runs it.
func runImageRm(cmd *cobra.Command, o cloudOptions, name string) error {
	ctx := cmd.Context()
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	path, onDisk, old, err := catalogConfig(ctx, o)
	if err != nil {
		return err
	}
	cat := onDisk.ImageCatalog
	if cat == nil || !slices.ContainsFunc(cat.Entries, func(e config.Entry) bool { return e.Name == name }) {
		return userErr("this project's image catalog has no entry named %s: fugaro image ls lists the entries it has", pluginwire.Printable(name))
	}
	who, unasked, err := catalogPointedAt(ctx, onDisk, cat, name)
	if err != nil {
		return err
	}
	if who != "" {
		return userErr("the catalog entry %s is still what %s runs, so removing it would leave that job on an image no catalog describes: point it at another entry (image_name:) or change its needs: first, run fugaro update, then remove the entry",
			pluginwire.Printable(name), who)
	}
	if len(unasked) > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "note: cannot tell whether %s still runs %s: this machine has no checkout of them, so the image_name: and needs: of their fugaro.yaml are not read here\n",
			pluginwire.Printable(strings.Join(unasked, ", ")), pluginwire.Printable(name))
	}
	if err := imageCatalogGate(onDisk, "removing the catalog entry "+pluginwire.Printable(name), cmd.ErrOrStderr()); err != nil {
		return err
	}
	next, err := catalogWith(onDisk, func(c *config.Catalog) error {
		c.Entries = slices.DeleteFunc(c.Entries, func(e config.Entry) bool { return e.Name == name })
		return nil
	})
	if err != nil {
		return err
	}
	if err := userSave(cmd.OutOrStdout(), path, old, next); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "the catalog entry %s is out of this machine's project config: its image stays in the registry, and fugaro update publishes the catalog without it\n",
		pluginwire.Printable(name))
	return nil
}

// catalogConfig selects this machine's project config and reads the file
// itself. The document the write is built from is the file's, not the
// selection's copy: the selection has this invocation's --region and
// --gcp-project applied to it, so a config marshalled from it would make an
// ephemeral flag the installation's own on disk (the rule user.go's writer
// states at length).
func catalogConfig(ctx context.Context, o cloudOptions) (string, *localcfg.Config, []byte, error) {
	sel, lc, err := selectProject(ctx, o)
	if err != nil {
		return "", nil, nil, err
	}
	if sel.Path == "" {
		return "", nil, nil, userErr("this machine has no project config of its own: project %s was selected from the shared config, which is published to the runs bucket and has no file here, and the image catalog is written to your own (fugaro setup writes one)", pluginwire.Printable(sel.Name))
	}
	if lc == nil {
		return "", nil, nil, userErr("no project config was read")
	}
	old, err := os.ReadFile(sel.Path)
	if err != nil {
		return "", nil, nil, userErr("reading %s: %v", pluginwire.Printable(sel.Path), err)
	}
	onDisk, err := localcfg.Parse(old)
	if err != nil {
		return "", nil, nil, userErr("the project config %s: %v", pluginwire.Printable(sel.Path), err)
	}
	return sel.Path, onDisk, old, nil
}

// catalogPointedAt names the first job that still runs entry, and the
// repositories whose checkout this machine does not have, whose workflows it
// therefore cannot judge. A job runs the entry the tag-cover rule picks for
// it (catalog.Choose): a workflow that names one pins it, and a workflow that
// needs nothing runs the project's default, so both are pointers at the entry
// and both are refused.
//
// A job the rule already refuses is not one of them: it runs no image at all,
// and a removal cannot leave it on a catalog entry it never had.
func catalogPointedAt(ctx context.Context, lc *localcfg.Config, cat *config.Catalog, entry string) (string, []string, error) {
	env := &cloudEnv{lc: lc}
	var unasked []string
	for _, repo := range slices.Sorted(maps.Keys(lc.Repos)) {
		cfg := checkoutConfig(ctx, repo)
		if cfg == nil {
			unasked = append(unasked, repo)
			continue
		}
		slug := repo
		if s, err := env.repoSlug(repo, func() *config.Config { return cfg }); err == nil {
			slug = s
		}
		// The workflows are the checkout's, not the local config's list of
		// names: the image_name: and needs: this asks about live in the
		// checkout's fugaro.yaml, which is the file the deploy-time job spec
		// is built from, so a stale list in the local config could not answer
		// the question anyway.
		for _, n := range slices.Sorted(maps.Keys(cfg.Workflows)) {
			w := cfg.Workflows[n]
			if !catalog.Applies(*cat, w) {
				continue
			}
			choice, err := catalog.Choose(catalog.Job{
				Catalog: *cat,
				Repos:   []catalog.Repo{{Slug: slug, Pin: w.ImageName, Needs: w.Needs}},
				Backend: catalog.Backend(lc.Backend()),
			})
			if err != nil || choice.Entry.Name != entry {
				continue
			}
			return fmt.Sprintf("the workflow %s of %s", pluginwire.Printable(n), pluginwire.Printable(repo)), unasked, nil
		}
	}
	return "", unasked, nil
}

// catalogWith applies edit to a copy of the catalog the config has and
// returns the config that writes it. The copy is what makes a refused edit
// leave the file it came from alone, and the validation is what keeps the
// catalog the file is about to hold one this CLI reads back.
func catalogWith(lc *localcfg.Config, edit func(*config.Catalog) error) (*localcfg.Config, error) {
	cat := config.Catalog{}
	if lc.ImageCatalog != nil {
		cat = *lc.ImageCatalog
	}
	cat.Entries = slices.Clone(cat.Entries)
	if err := edit(&cat); err != nil {
		return nil, err
	}
	if problems := config.ValidateCatalog("image_catalog", cat); len(problems) > 0 {
		return nil, userErr("the image catalog: %s: %s", problems[0].Path, problems[0].Message)
	}
	next := *lc
	next.ImageCatalog = &cat
	return &next, nil
}

// imageCatalogGate asks the image family's skew gate (D1156) about the
// installation's base image: the image a job that no catalog entry describes
// is built from, and therefore the one whose fugaro has to read the key. It
// is asked where the catalog is written — an entry added, removed or promoted
// — and never where it is only read, and its warning is printed where the
// gate is: the entry itself is written either way.
func imageCatalogGate(lc *localcfg.Config, where string, warn io.Writer) error {
	return imageCatalogSkew(where, lc.BaseImage(config.BaseKind), imageCatalogShipped, warn)
}
