package cli

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// `fugaro image map` is the repo-to-image map's own command (D1906): the
// project setting that says which catalog entry each of its repositories runs.
// It writes this machine's project config, one step up from `fugaro image add`
// and `fugaro image rm`, which write the catalog the map names entries of — and
// `fugaro update` publishes both to the project's policy node (image_catalog,
// repo_images), which is what a job reads. There is no publish and no build here
// for the same reason there is none there: the node is fugaro update's (the
// HARD RULE).
//
// The map is written and never read here. What it says is published state, and
// printing this machine's copy of it as the project's would be a listing that
// differs from the node whenever a teammate wrote last: `fugaro project policy
// show` prints the node, and `fugaro image ls` prints the catalog the map's
// entries are chosen from.

func newImageMapCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "map",
		Short: "Map a repository to an image catalog entry (the project's repo_images)",
	}
	cmd.AddCommand(newImageMapSetCmd())
	return cmd
}

func newImageMapSetCmd() *cobra.Command {
	var o cloudOptions
	cmd := &cobra.Command{
		Use:   "set <repo> <entry>",
		Short: "Map a repository to an image catalog entry, in this machine's project config",
		Long: "Map one of the project's repositories to an image catalog entry, in this\n" +
			"machine's project config: the copy fugaro update publishes to the project's\n" +
			"policy node (repo_images). From there the repository's jobs run that entry's\n" +
			"image, so the mapping is a project setting and not a per-repository one — a\n" +
			"rerun of the skill that proposed it changes one line here rather than one\n" +
			"file in every repository.\n\n" +
			"The repository is named as provider/owner/name (github/acme/app), or as\n" +
			"owner/name for one the project's local config lists (fugaro update --repo\n" +
			"onboards one), or as a path inside its checkout; the entry is one this\n" +
			"machine's catalog names (fugaro image add writes one). Setting a repository\n" +
			"that is already mapped replaces its entry. The file's map is the project's\n" +
			"whole map, and it is what fugaro update publishes: a file that holds none\n" +
			"of its own takes the map the project publishes into it first — a name the\n" +
			"migration cannot place is dropped with its line — so a mapping written\n" +
			"here adds to the project's map rather than replacing it.\n\n" +
			"This write is fugaro update's to publish: it lands in this machine's project\n" +
			"config, and a coding agent's session may make it like any other (D3425).",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runImageMapSet(cmd, o, args[0], args[1])
		},
	}
	addCloudFlags(cmd, &o)
	return cmd
}

// runImageMapSet writes one mapping into this machine's project config, backing
// the file up first (userSave, the rule every writer of this file follows).
func runImageMapSet(cmd *cobra.Command, o cloudOptions, repo, entry string) error {
	ctx := cmd.Context()
	// The read of the project config is the operator's own and may fetch the
	// shared one, so the GODEBUG that prints requests with their credentials is
	// refused before anything is read (the rule every such command keeps).
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	if !config.ValidCatalogName(entry) {
		return userErr("the entry name %s: %s", pluginwire.Printable(entry), config.CatalogNameMessage)
	}
	path, onDisk, old, err := catalogConfig(ctx, o)
	if err != nil {
		return err
	}
	// The map is published with the catalog, from whichever machine wrote it
	// (D1906): what a job of every repository runs is the project's, so it is
	// the project admin's to write.
	if err := accessGateProject(ctx, onDisk, nil, "nothing was written", "writing the project's image map"); err != nil {
		return err
	}
	// The published node is read once: it is where the allow-list that enrols a
	// key lives, and the map a file with none of its own states through (R2, R6).
	node, _, err := projectPolicyRead(ctx, onDisk)
	if err != nil {
		return err
	}
	// The key written is provider/owner/name — the name a job's own repository is
	// looked up by (D3370) — and the argument is read by R6: a path resolves
	// through its checkout, provider/owner/name is taken as written, and
	// owner/name takes its provider from the project. A key no job of the project
	// reaches is refused before the file is touched, because the mistake it
	// catches (a misspelt name, a provider this installation does not serve)
	// leaves nothing behind to notice.
	key, err := imageMapKey(ctx, o, onDisk, node, repo)
	if err != nil {
		return err
	}
	switch {
	case onDisk.ImageCatalog == nil:
		// Nothing here can tell whether the entry exists; the published catalog
		// does, and a map naming an entry no catalog describes is a job on an
		// image nothing built. It is a note and not a refusal because this
		// machine's own config is not the project's whole catalog: it is the
		// file the publisher reads, so the entry is written where it is missing.
		fmt.Fprintf(cmd.ErrOrStderr(), "note: this machine's project config names no image catalog, so the entry %s is not checked against one here: a job of %s runs that entry's image once fugaro update has published it, and fugaro image ls lists the entries this project has\n",
			pluginwire.Printable(entry), pluginwire.Printable(key))
	default:
		if !slices.ContainsFunc(onDisk.ImageCatalog.Entries, func(e config.Entry) bool { return e.Name == entry }) {
			return userErr("this project's image catalog has no entry named %s, so the mapping would put %s on an image nothing describes: fugaro image add %s writes the entry, and fugaro image ls lists the entries the project has",
				pluginwire.Printable(entry), pluginwire.Printable(key), quoteWord(entry))
		}
	}
	next, dropped := imageMapWith(ctx, onDisk, node, key, entry)
	// A file with no map of its own states the project's map from here on: under
	// the keep rule (D1906) the publisher takes this file's member over the
	// published one, so a file that stayed silent would take a map the project
	// already holds with it. The write meets that map first for exactly this
	// reason (imageMapWith, R13): what the project serves today is carried into
	// the file and the mapping is added to it, a name the migration cannot place
	// excepted. The note is printed before the write, because that is when the
	// operator can still change their mind.
	if onDisk.RepoImages == nil && onDisk.ImageMap == nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "note: this machine's project config names no repo-to-image map of its own, so this file states the project's map from here on: the map the project serves today is carried into it, and this mapping is added to that")
	}
	// A name the migration could not place is answered with its line: the write
	// carries the map the project serves today (R13), and a key nothing enrolled
	// has no image to carry over, so it is dropped and said rather than silently
	// lost.
	for _, line := range dropped {
		fmt.Fprintln(cmd.ErrOrStderr(), line)
	}
	// Whether the mapping puts a repository on an image whose copy still
	// carries another repository's checkout: the map decides who runs an
	// entry's image and the copy decides whose source is in it, so this write
	// is the only moment before jobs run on that image when the two can be
	// seen apart (noteBakedExposure). The map read is the file's after this
	// write, by the member the project serves (R2) — the file's map wins over
	// the node's, so the node cannot change the answer here. The read is best
	// effort — a machine that cannot reach the runs bucket still writes the
	// mapping, and the note says what it could not check — and the rebuild it
	// names is the entry's next build, which composes the copy for the map
	// this write leaves.
	noteBakedExposure(ctx, cmd.ErrOrStderr(), onDisk, configImageMap(next, nil), entry)
	if err := imageCatalogGate(onDisk, "the map's entry for "+pluginwire.Printable(key), cmd.ErrOrStderr()); err != nil {
		return err
	}
	if err := userSave(cmd.OutOrStdout(), path, old, next); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "the repository %s runs the catalog entry %s from now on: the mapping is in this machine's project config, and fugaro update publishes it (repo_images)\n",
		pluginwire.Printable(key), pluginwire.Printable(entry))
	return nil
}

// imageMapKey reads `image map set`'s <repo> argument (R6) and answers the key
// the write stores.
//
// R6's order: a value that names a directory is a checkout path, and resolves
// the way every --repo does; three or more segments whose first is a provider
// this installation serves are provider/owner/name, taken as written; anything
// else is owner/name, and its provider comes from repoImageKey's sources (the
// local config's entry, the allow-list, the checkout of that same repository).
//
// The key is then held to the enrolled set (R6): the local config's entry at
// that provider, or an allow_repos entry that admits it. A key nothing enrols
// reaches no job — the map is read for a job's own repository, and a job only
// runs for a repository the project has — so it is refused rather than written
// and published.
func imageMapKey(ctx context.Context, o cloudOptions, lc *localcfg.Config, node *projectpolicy.Policy, arg string) (string, error) {
	provider, repo := "", arg
	if !imageMapArgIsDir(arg) {
		if p, rest, ok := imageMapArgProviderKey(arg); ok {
			provider, repo = p, rest
		}
	}
	resolved, err := repoArg(ctx, o, repo)
	if err != nil {
		return "", err
	}
	key, err := repoImageKey(ctx, lc, node, resolved, provider)
	if err != nil {
		// R6's two refusals are one to the reader — a repository the project does
		// not run and a provider this installation does not serve are both a key
		// no job reaches — so the sentence names the form of a key and the command
		// that enrols a repository rather than the source that could not answer.
		return "", imageMapUnenrolled(resolved)
	}
	if !imageKeyEnrolled(lc, node, key) {
		return "", imageMapUnenrolled(key)
	}
	return key, nil
}

// imageMapArgIsDir reports whether the argument names a directory, which R6
// reads as a checkout path before anything else. repoArg resolves it from there,
// so a path is only ever detected here: the value it resolves to is the
// repository, not the directory.
func imageMapArgIsDir(arg string) bool {
	fi, err := os.Stat(arg)
	return err == nil && fi.IsDir()
}

// imageMapArgProviderKey splits provider/owner/name when the argument is written
// that way: three or more segments whose first is a provider this installation
// serves (R6). A deeper path is kept whole — a repository may nest, and
// bitbucket/acme/group/app names one — and a first segment that is not a served
// provider is left to repoArg, which reads it as owner/name.
func imageMapArgProviderKey(arg string) (provider, repo string, ok bool) {
	parts := strings.Split(arg, "/")
	if len(parts) < 3 || !servedProvider(parts[0]) {
		return "", "", false
	}
	return parts[0], strings.Join(parts[1:], "/"), true
}

// imageKeyEnrolled reports whether a key is one the project runs: R6's set, the
// local config's entry at that provider or an allow_repos entry that admits the
// key (wildcards included, R1).
func imageKeyEnrolled(lc *localcfg.Config, node *projectpolicy.Policy, key string) bool {
	provider, repo, ok := strings.Cut(key, "/")
	if !ok {
		return false
	}
	if r, listed := lc.RepoFor(repo); listed && r.Provider == provider {
		return true
	}
	var allow []string
	if node != nil {
		allow = node.AllowRepos
	}
	return projectpolicy.AdmitRepo(allow, key)
}

// imageMapUnenrolled is the refusal of an argument that names no repository this
// project runs at a provider this installation serves: an unknown provider and
// an unenrolled repository are one mistake to the reader, and one sentence names
// both halves of the fix — the form a key takes, and the command that enrols a
// repository.
func imageMapUnenrolled(name string) error {
	return userErr("the repository %s is not enrolled at a provider this installation serves: name it as provider/owner/name (github/acme/app), or enroll it with 'fugaro project repos add'", pluginwire.Printable(name))
}

// imageMapWith applies one mapping to a copy of the map the project serves and
// returns the config that writes it, with the lines of any name the migration
// dropped.
//
// The map the write is applied to is the effective one, migrated into the file
// first (R13): a file still holding the legacy member would otherwise lose every
// mapping it has the moment the first repo_images key is written, because the
// file's new member wins over the node's under the keep rule (R2). The copy is
// what makes a refused write leave the file it came from alone, as catalogWith's
// copy does for the catalog: the two writes land in the same file and a command
// that aliased either would change the config it validated.
func imageMapWith(ctx context.Context, lc *localcfg.Config, node *projectpolicy.Policy, key, entry string) (*localcfg.Config, []string) {
	images, dropped := migrateImageMap(ctx, lc, node)
	m := make(map[string]string, len(images)+1)
	maps.Copy(m, images)
	m[key] = entry
	next := *lc
	next.RepoImages = m
	// The file states the map in one member from here on: the old member is what
	// the migration read, and leaving it beside the new one would name one
	// repository twice to a reader that still holds it (R2).
	next.ImageMap = nil
	return &next, dropped
}
