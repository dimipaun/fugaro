package cli

import (
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/task"
)

// `fugaro image map` is the repo-to-image map's own command (D1906): the
// project setting that says which catalog entry each of its repositories runs.
// It writes this machine's project config, one step up from `fugaro image add`
// and `fugaro image rm`, which write the catalog the map names entries of — and
// `fugaro update` publishes both to the project's policy node (image_catalog,
// image_map), which is what a job reads. There is no publish and no build here
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
		Short: "Map a repository to an image catalog entry (the project's image_map)",
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
			"policy node (image_map). From there the repository's jobs run that entry's\n" +
			"image, so the mapping is a project setting and not a per-repository one — a\n" +
			"rerun of the skill that proposed it changes one line here rather than one\n" +
			"file in every repository.\n\n" +
			"The repository is one the project's local config lists (fugaro update --repo\n" +
			"onboards one), as owner/name or as a path inside its checkout, and the entry\n" +
			"is one this machine's catalog names (fugaro image add writes one). Setting a\n" +
			"repository that is already mapped replaces its entry; the file's map is the\n" +
			"project's whole map, so a map published from another machine is replaced by\n" +
			"this file's rather than added to.\n\n" +
			"This write is a person's own: fugaro update publishes it, and a coding\n" +
			"agent's session is refused it, in a managed project by that project's gate.",
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
	// The key written is the canonical form of the repository — the name a job's
	// own repository is looked up by (D1906) — and the value is resolved the way
	// every --repo is: owner/name passes through, a path becomes the owner/name
	// of its checkout's origin, and anything else is refused before the file is
	// touched.
	resolved, err := repoArg(ctx, o, repo)
	if err != nil {
		return err
	}
	canon, err := task.CanonicalRepo(resolved)
	if err != nil {
		return userErr("the repository %s: %v", pluginwire.Printable(repo), err)
	}
	// A mapping whose key names no repository of this project is a line no
	// lookup reaches: the map is read for a job's own repository, and a job only
	// runs for a repository the project has. It is refused here rather than
	// written and published, because the mistake it catches (a misspelt name)
	// leaves nothing behind to notice.
	if _, known := onDisk.RepoFor(canon); !known {
		return userErr("the repository %s is not one of project %s's repositories in the local config, so no job of it would ever read this mapping: fugaro update --repo in its checkout onboards it, and fugaro project repos ls lists what the project has",
			pluginwire.Printable(canon), pluginwire.Printable(onDisk.Name))
	}
	switch {
	case onDisk.ImageCatalog == nil:
		// Nothing here can tell whether the entry exists; the published catalog
		// does, and a map naming an entry no catalog describes is a job on an
		// image nothing built. It is a note and not a refusal because this
		// machine's own config is not the project's whole catalog: it is the
		// file the publisher reads, so the entry is written where it is missing.
		fmt.Fprintf(cmd.ErrOrStderr(), "note: this machine's project config names no image catalog, so the entry %s is not checked against one here: a job of %s runs that entry's image once fugaro update has published it, and fugaro image ls lists the entries this project has\n",
			pluginwire.Printable(entry), pluginwire.Printable(canon))
	default:
		if !slices.ContainsFunc(onDisk.ImageCatalog.Entries, func(e config.Entry) bool { return e.Name == entry }) {
			return userErr("this project's image catalog has no entry named %s, so the mapping would put %s on an image nothing describes: fugaro image add %s writes the entry, and fugaro image ls lists the entries the project has",
				pluginwire.Printable(entry), pluginwire.Printable(canon), quoteWord(entry))
		}
	}
	next := imageMapWith(onDisk, canon, entry)
	// The document the managed gate judges, if it is asked at all: the bytes
	// this command is about to write, taken here so the verdict and the write
	// are the same artifact (managed spec §4.1's hashing rule).
	doc, err := next.Marshal()
	if err != nil {
		return err
	}
	// A coding agent's session is refused before anything is printed and before
	// the file is written: in a managed project by the gate, in the gate's own
	// words and with the record of the refusal, and everywhere else by this
	// command's own sentence (image_map_managed.go). The marker is read once
	// here and passed, so the session the gate records and the session the
	// refusal is scoped to are one reading of the same facts.
	marker := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin()))
	if marker != "" {
		if err := imageMapGate(ctx, onDisk, doc, marker); err != nil {
			return err
		}
		return imageMapSession(marker, canon, entry)
	}
	// A file with no map of its own states the whole map when one entry is
	// written: under the keep rule (D1906) the publisher takes this file's map
	// over the published one, so the first mapping written here replaces a map
	// the node holds rather than adding to it. The note is printed before the
	// write, because that is when the operator can still change their mind.
	if onDisk.ImageMap == nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "note: this machine's project config names no repo-to-image map, so this mapping is written as the project's whole map: if the project already has one published, it is replaced by this file's rather than added to")
	}
	// Whether the mapping puts a repository on an image whose copy still
	// carries another repository's checkout: the map decides who runs an
	// entry's image and the copy decides whose source is in it, so this write
	// is the only moment before jobs run on that image when the two can be
	// seen apart (noteBakedExposure). The read is best effort — a machine that
	// cannot reach the runs bucket still writes the mapping, and the note says
	// what it could not check — and the rebuild it names is the entry's next
	// build, which composes the copy for the map this write leaves.
	noteBakedExposure(ctx, cmd.ErrOrStderr(), onDisk, next, entry)
	if err := imageCatalogGate(onDisk, "the map's entry for "+pluginwire.Printable(canon), cmd.ErrOrStderr()); err != nil {
		return err
	}
	if err := userSave(cmd.OutOrStdout(), path, old, next); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "the repository %s runs the catalog entry %s from now on: the mapping is in this machine's project config, and fugaro update publishes it (image_map)\n",
		pluginwire.Printable(canon), pluginwire.Printable(entry))
	return nil
}

// imageMapWith applies one mapping to a copy of the map the config holds and
// returns the config that writes it. The copy is what makes a refused write
// leave the file it came from alone, as catalogWith's copy does for the
// catalog: the two writes land in the same file and a command that aliased
// either would change the config it validated.
func imageMapWith(lc *localcfg.Config, repo, entry string) *localcfg.Config {
	m := make(map[string]string, len(lc.ImageMap)+1)
	maps.Copy(m, lc.ImageMap)
	m[repo] = entry
	next := *lc
	next.ImageMap = m
	return &next
}

// imageMapCommand is the command as a reader types it, for the sentences that
// point at it: a refusal names the command a person runs and never a flag, a
// variable or a mark (D1023).
func imageMapCommand(repo, entry string) string {
	return "fugaro image map set " + quoteWord(repo) + " " + quoteWord(entry)
}
