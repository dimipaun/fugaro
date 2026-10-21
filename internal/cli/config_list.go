package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// configListDoc is `fugaro config list --json`: the installations this
// machine has, as a document.
//
// It is the read the v1 design's D13 names — "fugaro config list --json" —
// and it closes that design's gap 5 ("nothing enumerates installations",
// docs/design/desktop-console.md §4.3), which the console's installation
// picker waits on (console v2 plan V2.1.1; design F1/F2 and D31). The
// enumeration is localcfg's own: Projects(getenv), ProjectPath, LoadProject.
//
// **The document is a named set of fields and it is deliberately NOT a
// marshalled localcfg.Config.** That struct carries the owner's providers,
// their base URLs, the named Claude accounts, the reserved project secrets,
// the Terraform block and the whole repository list — none of which the
// console needs, and all of which would silently join this document the day
// a field is added to the struct. The console is one field away from a
// credential dump here, so the fields are spelled out in the types below and
// config_list_test.go fails if a field outside this set reaches the output.
type configListDoc struct {
	Installations []configListInstallation `json:"installations"`
	// Warnings are the lines a human listing prints before its table: an
	// installation whose config could not be read is still listed (its name
	// is a fact of the directory), with its fields empty, and the warning
	// says why. Never a silent omission — the picker must not offer a name
	// whose data is missing without saying so.
	Warnings []string `json:"warnings"`
}

// configListInstallation is ONE installation. Every field is one the console
// needs and no more (V2.1.1): the project name, the GCP project, the region,
// whether a shared config was merged, and when the shared copy was last
// checked.
type configListInstallation struct {
	// Name is the project name, i.e. projects/<name>.yaml's name, which the
	// file must agree with (LoadProject refuses one that does not).
	Name string `json:"name"`
	// GCPProject is the installation's GCP project ID, "" when its config
	// could not be read (the warning says why).
	GCPProject string `json:"gcp_project"`
	// Region is the installation's region, "" when its config could not be
	// read.
	Region string `json:"region"`
	// SharedMerged says this machine holds the installation's published
	// shared config: a cache entry naming this installation's GCP project
	// exists under $XDG_CACHE_HOME/fugaro/shared-config/<name>.json. That
	// entry is the published copy MergeShared merges from and the same-
	// installation test (SameInstallation) is what decides it belongs here —
	// the cache is keyed by the project name, so the name half of that test
	// is the file's own name and the GCP project half is compared here.
	// False means no shared config was read on this machine, never that the
	// installation publishes none.
	SharedMerged bool `json:"shared_merged"`
	// SharedCheckedAt is when that copy was last checked against the runs
	// bucket, null when SharedMerged is false. It is the cache's own
	// CheckedAt: how fresh the copy is read off it with SharedFreshFor and
	// SharedOfflineFor, which are the constants the reader (fetchSharedConfig)
	// uses, so this document never claims a freshness of its own.
	SharedCheckedAt *time.Time `json:"shared_checked_at"`
}

func newConfigListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the Fugaro installations this machine has",
		Long: "List the project configs under $XDG_CONFIG_HOME/fugaro/projects (else\n" +
			"~/.config/fugaro/projects), with the GCP project and region of each and what\n" +
			"is known about its published shared config.\n\n" +
			"This is a local read: it opens no bucket and needs no project selected, so it\n" +
			"works outside a checkout and with no config at all (an empty list).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			doc := buildConfigList(getenvOS, time.Now())
			return printConfigList(cmd.OutOrStdout(), doc, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	return cmd
}

// buildConfigList is the document: every project config localcfg.Projects
// names, in its order, with what can be read of each without opening a
// bucket. now is the clock the shared cache's freshness is read against.
func buildConfigList(getenv func(string) string, now time.Time) configListDoc {
	// Both are seeded: an empty array is a fact ("there are none", "there
	// were no warnings") and a null is not, which is the console's own
	// availability rule the console's JSON contract states (§1).
	doc := configListDoc{Installations: []configListInstallation{}, Warnings: []string{}}
	names, err := localcfg.Projects(getenv)
	if err != nil {
		// A projects directory that cannot be read is not "no
		// installations": say so rather than print an empty list.
		doc.Warnings = append(doc.Warnings, fmt.Sprintf("listing the project configs failed: %v", err))
		return doc
	}
	for _, name := range names {
		inst := configListInstallation{Name: name}
		lc, path, err := localcfg.LoadProject(getenv, name)
		if err != nil {
			doc.Warnings = append(doc.Warnings, fmt.Sprintf("%s could not be read: %v", path, err))
			doc.Installations = append(doc.Installations, inst)
			continue
		}
		inst.GCPProject, inst.Region = lc.GCPProject, lc.Region
		if e, ok := localcfg.LoadSharedCache(getenv, name); ok && e.GCPProject == lc.GCPProject {
			inst.SharedMerged = true
			checked := e.CheckedAt
			inst.SharedCheckedAt = &checked
			if !e.Fresh(now) {
				// Not a field of its own: the copy is still the one this
				// machine has, and how old it is follows from CheckedAt and
				// the constants the reader uses (SharedFreshFor,
				// SharedOfflineFor). The human listing says it in words.
				doc.Warnings = append(doc.Warnings, fmt.Sprintf("%s's shared config was last checked %s ago, over the %s the reader refreshes it within",
					name, now.Sub(e.CheckedAt).Round(time.Second), localcfg.SharedFreshFor))
			}
		}
		doc.Installations = append(doc.Installations, inst)
	}
	return doc
}

func printConfigList(w io.Writer, doc configListDoc, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	}
	if len(doc.Installations) == 0 {
		fmt.Fprintln(w, "no Fugaro installations on this machine; run `fugaro setup` to set one up")
	}
	for _, n := range doc.Warnings {
		fmt.Fprintln(w, "warning: "+pluginwire.Printable(n))
	}
	if len(doc.Installations) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tGCP PROJECT\tREGION\tSHARED\tSHARED CHECKED")
	for _, inst := range doc.Installations {
		shared, checked := "no", "-"
		if inst.SharedMerged {
			shared = "yes"
			if inst.SharedCheckedAt != nil {
				checked = time.Since(*inst.SharedCheckedAt).Round(time.Second).String() + " ago"
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			pluginwire.Printable(inst.Name), pluginwire.Printable(inst.GCPProject),
			pluginwire.Printable(inst.Region), shared, checked)
	}
	return tw.Flush()
}
