package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// `fugaro project ls`: what this machine has, as a document.
//
// It is the read the desktop console's first run is built on
// (docs/design/desktop-console.md D13, §3.4 rule 7 and §12 item 8): the
// console must start with no configuration and list the installations on
// this machine, each with the repositories its project allows and the local
// checkout of each where one is known — and it never reads
// ~/.config/fugaro itself, so this command is where all of it is read.
//
// Two local sources, and no third:
//
//   - the project configs (localcfg.Projects, ProjectPath, LoadProject: the
//     same loader and the same validation every other command uses, never a
//     second parser), for the name, the GCP project, the region and the
//     installation's name;
//   - the remembered checkouts (localcfg.LoadCheckouts: a machine-local
//     state file beside them, never a key in a project config), for the
//     repositories and where they are checked out.
//
// It is read-only and offline in the strict sense: it opens no bucket, calls
// no API, takes no project selection and needs no credentials, so it works
// outside a checkout, with no config at all (an empty list) and with no
// network. And it writes nothing — the checkouts it reads are re-validated
// here, never refreshed, so a stale entry is reported rather than repaired.

// projectListVersion is the version of the document `project ls --json`
// prints. A reader that knows this number knows the shape below; a field
// added to either type is a change to the shape, and
// project_ls_test.go's golden keys fail on one.
const projectListVersion = 1

// projectListDoc is the document. Every slice is seeded: an empty array is a
// fact ("there are none"), and a null is not.
type projectListDoc struct {
	Version int `json:"version"`
	// Projects is every project config on this machine, in localcfg.Projects'
	// order (sorted by name), including the ones that could not be read.
	Projects []projectListEntry `json:"projects"`
	// Warnings are the lines a human listing prints before its table: the
	// state file could not be read, or a project config could not be listed
	// at all. A project config that could not be *parsed* is not here — it is
	// a project, and its own Error field says so.
	Warnings []string `json:"warnings"`
}

// projectListEntry is ONE installation: what the console shows for it.
//
// The fields are spelled out rather than marshalled from localcfg.Config,
// for the reason configListDoc gives (the struct carries the owner's
// providers, secrets, accounts and the whole repository list, and the
// console is one field away from a credential dump): a named set is what the
// console's contract can be checked against.
type projectListEntry struct {
	// Name is the project name: projects/<name>.yaml's file name, which is
	// what `--project` takes. It is known for every config that is there at
	// all, including one that could not be read.
	Name string `json:"name"`
	// Installation is the installation's name as the config carries it
	// (localcfg.Config.Name, the field an installation's own name never
	// changes from), "" when the config could not be read. LoadProject
	// requires it to equal Name, so on a config that loaded the two agree;
	// they are separate fields because the file name is a fact of the
	// directory and the name inside it is a fact of the file.
	Installation string `json:"installation"`
	// GCPProject and Region are the installation's GCP project ID and
	// region, "" when the config could not be read.
	GCPProject string `json:"gcp_project"`
	Region     string `json:"region"`
	// Error is why this config could not be read, bounded and made inert
	// (pluginwire.Printable): a message the parser built can quote the file,
	// and the file is one this process did not write. A config that fails to
	// parse is listed with it and never aborts the listing.
	Error string `json:"error,omitempty"`
	// Checkouts are the repositories this machine remembers a checkout of
	// for this project, in identity order.
	Checkouts []projectCheckout `json:"checkouts"`
}

// projectCheckout is one repository's remembered checkouts.
type projectCheckout struct {
	Provider string `json:"provider"`
	// Repo is the repository as its git origin names it (owner/name).
	Repo string `json:"repo"`
	// Paths is every remembered checkout of it, oldest first. Both strings
	// go through pluginwire.Printable: they come from a file on this machine
	// that this process did not write.
	Paths []projectCheckoutPath `json:"paths"`
}

// projectCheckoutPath is ONE remembered checkout, re-validated on this read.
type projectCheckoutPath struct {
	// Path is the absolute path that was recorded, made printable and
	// length-bounded.
	Path string `json:"path"`
	// Valid is whether the path is still a checkout of this project and this
	// repository (see validateCheckoutPath).
	Valid bool `json:"valid"`
	// Reason is the code for why it is not, "" when Valid. A code and not a
	// sentence: the console renders it, and a sentence built here would be a
	// claim about a directory this command only looked at once.
	Reason string `json:"reason,omitempty"`
}

// The reason codes a remembered path is reported with. They are a closed set
// (validateCheckoutPath returns one of them), and each names the first thing
// that was wrong, in the order a reader would check:
const (
	// checkoutMissing: the path is not a directory that exists any more (it
	// was deleted, or moved; a symlink loop is this too, since the path
	// resolves to nothing).
	checkoutMissing = "missing"
	// checkoutNotACheckout: it exists, but it is not a git checkout's own
	// root (no git toplevel there, no fugaro.yaml in it, or the path is now
	// inside some other checkout).
	checkoutNotACheckout = "not_a_checkout"
	// checkoutUnreadable: it is a checkout with a fugaro.yaml this command
	// cannot read (a file it cannot open, or one whose project: is not a
	// string).
	checkoutUnreadable = "unreadable"
	// checkoutOtherProject: its fugaro.yaml names another project than the
	// one the path was recorded for (or names none).
	checkoutOtherProject = "other_project"
	// checkoutOtherRepository: its origin is another repository than the one
	// the path was recorded for, or an origin git config rewrites.
	checkoutOtherRepository = "other_repository"
)

func newProjectLsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List this machine's Fugaro installations and their known checkouts",
		Long: `List every project config on this machine — the installations — with the GCP
project, the region and the installation's name of each, and, per
installation, the repositories this machine remembers a checkout of and
where each checkout is.

This is a local, read-only, offline read: it opens no bucket and calls no
cloud API, needs no project selected and no credentials, and works outside a
checkout and with no config at all (an empty list). A config that cannot be
parsed is still listed, with the reason in its "error" field, and never stops
the rest of the listing.

The checkouts are remembered by the commands that already run in one (fugaro
init --repo, fugaro run, fugaro validate and every other command that resolves
a checkout's project), in a machine-local state file beside the project
configs. Every path is re-validated on this read: a path that was moved or
deleted, that is no longer a git checkout, or whose fugaro.yaml no longer
names this project and this repository is reported with "valid": false and a
reason, and is never silently dropped here. The paths are local state: they
are never published to the runs bucket, to the shared config or to any log.

With --json it prints one versioned document instead of a table.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return printProjectList(cmd.OutOrStdout(), buildProjectList(cmd.Context(), getenvOS), asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	return cmd
}

// buildProjectList is the document: every project config localcfg names, in
// its order, with what can be read of each without opening a bucket, plus
// the checkouts this machine remembers for it.
func buildProjectList(ctx context.Context, getenv func(string) string) projectListDoc {
	doc := projectListDoc{Version: projectListVersion, Projects: []projectListEntry{}, Warnings: []string{}}
	names, err := localcfg.Projects(getenv)
	if err != nil {
		// A projects directory that cannot be read is not "no
		// installations": say so rather than print an empty list.
		doc.Warnings = append(doc.Warnings, fmt.Sprintf("listing the project configs failed: %v", err))
		return doc
	}
	// A state file that cannot be read is not "no checkouts": the projects
	// are still listed (they are facts of the directory), the warning says
	// why the checkouts are missing, and nothing is written over it.
	known := localcfg.EmptyCheckouts()
	if c, err := localcfg.LoadCheckouts(getenv); err != nil {
		doc.Warnings = append(doc.Warnings, fmt.Sprintf("the remembered checkouts could not be read, so no checkout is listed: %v", err))
	} else {
		known = c
	}
	for _, name := range names {
		entry := projectListEntry{Name: name, Checkouts: []projectCheckout{}}
		lc, _, err := localcfg.LoadProject(getenv, name)
		if err != nil {
			// The loader's own error already names the file; it is made
			// inert and bounded here, not wrapped in a second copy of the
			// path (which would push what the parser said past the bound).
			entry.Error = pluginwire.Printable(err.Error())
			doc.Projects = append(doc.Projects, entry)
			continue
		}
		entry.Installation, entry.GCPProject, entry.Region = lc.Name, lc.GCPProject, lc.Region
		for _, r := range known.ProjectCheckouts(name) {
			pc := projectCheckout{
				Provider: pluginwire.Printable(r.Provider),
				Repo:     pluginwire.Printable(r.Repo),
				Paths:    []projectCheckoutPath{},
			}
			for _, p := range r.Paths {
				valid, reason := validateCheckoutPath(ctx, p.Path, name, localcfg.CheckoutIdentity{Provider: r.Provider, Repo: r.Repo})
				pc.Paths = append(pc.Paths, projectCheckoutPath{Path: pluginwire.Printable(p.Path), Valid: valid, Reason: reason})
			}
			entry.Checkouts = append(entry.Checkouts, pc)
		}
		doc.Projects = append(doc.Projects, entry)
	}
	return doc
}

// validateCheckoutPath re-validates one remembered path, on the read: the
// directory exists, it is a git checkout, and its fugaro.yaml still names
// this project and the same repository. It reports the first thing that is
// wrong as a reason code (the consts above), and does not repair or drop
// anything — the write path is what prunes (localcfg.RecordCheckout), so a
// path that is stale here is still there for the operator to see, and still
// there if the directory comes back.
//
// resolveCheckout and not checkoutProject: this is a read, and reading must
// not record (or re-record, under an identity the checkout has since
// changed to) what it looked at.
func validateCheckoutPath(ctx context.Context, path, project string, id localcfg.CheckoutIdentity) (bool, string) {
	fi, err := os.Stat(path)
	if err != nil || !fi.IsDir() {
		return false, checkoutMissing
	}
	co, err := resolveCheckout(ctx, path)
	if err != nil {
		return false, checkoutUnreadable
	}
	if co == nil || filepath.Clean(path) != co.Root {
		// co.Root is the git toplevel git resolves for path: a path that is
		// not that toplevel is a checkout of nothing (a subdirectory of
		// another checkout, or a directory that has since become one).
		return false, checkoutNotACheckout
	}
	if co.Project != project {
		return false, checkoutOtherProject
	}
	oi, ok := readOrigin(ctx, co.Root)
	if !ok || oi.Rewritten {
		return false, checkoutOtherRepository
	}
	provider := co.Provider
	if !slices.Contains(config.Providers, provider) {
		provider = ""
	}
	if provider == "" {
		provider = providerForHost(oi.Host)
	}
	// sameRepo, not a string compare: owner/name is the same repository
	// whichever case it is written in, and task.CanonicalRepo is the rule
	// the rest of the tree compares repository names by.
	if provider != id.Provider || !sameRepo(oi.Repo, id.Repo) {
		return false, checkoutOtherRepository
	}
	return true, ""
}

func printProjectList(w io.Writer, doc projectListDoc, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	}
	if len(doc.Projects) == 0 {
		fmt.Fprintln(w, "no Fugaro installations on this machine; run `fugaro init` to set one up")
	}
	for _, n := range doc.Warnings {
		fmt.Fprintln(w, "warning: "+pluginwire.Printable(n))
	}
	// The warnings the table's own rows carry are printed before it, not
	// among them: a tabwriter buffers its columns, so a line written into it
	// and a line written beside it would come out in the wrong order.
	for _, p := range doc.Projects {
		if p.Error != "" {
			fmt.Fprintln(w, "warning: "+p.Name+": "+p.Error)
		}
	}
	if len(doc.Projects) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PROJECT\tGCP PROJECT\tREGION\tINSTALLATION\tREPOSITORY\tCHECKOUT\tSTATE")
	for _, p := range doc.Projects {
		if p.Error != "" {
			fmt.Fprintf(tw, "%s\t-\t-\t-\t-\t-\t%s\n", pluginwire.Printable(p.Name), checkoutUnreadable)
			continue
		}
		if len(p.Checkouts) == 0 {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t-\t-\t-\n", pluginwire.Printable(p.Name),
				pluginwire.Printable(p.GCPProject), pluginwire.Printable(p.Region), pluginwire.Printable(p.Installation))
			continue
		}
		for _, c := range p.Checkouts {
			if len(c.Paths) == 0 {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s/%s\t-\t-\n", pluginwire.Printable(p.Name),
					pluginwire.Printable(p.GCPProject), pluginwire.Printable(p.Region), pluginwire.Printable(p.Installation),
					c.Provider, c.Repo)
				continue
			}
			for _, path := range c.Paths {
				state := "ok"
				if !path.Valid {
					state = path.Reason
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s/%s\t%s\t%s\n", pluginwire.Printable(p.Name),
					pluginwire.Printable(p.GCPProject), pluginwire.Printable(p.Region), pluginwire.Printable(p.Installation),
					c.Provider, c.Repo, path.Path, state)
			}
		}
	}
	return tw.Flush()
}
