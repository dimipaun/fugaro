package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// configShowDoc is fugaro config show --json: the hook a script uses to
// check a repository resolves as intended (docs/design/layered-config.md
// §12).
type configShowDoc struct {
	ProjectLayer *validateLayer    `json:"project_layer"` // null: none applies
	ConfigSHA256 string            `json:"config_sha256"`
	Values       []configShowValue `json:"values"`
	Notes        []string          `json:"notes,omitempty"`
}

// configShowValue is one resolved key, with its source: default, project,
// profile <name> or repo.
type configShowValue struct {
	Path   string `json:"path"`
	Value  any    `json:"value"`
	Source string `json:"source"`
}

func newConfigShowCmd() *cobra.Command {
	var (
		workflow string
		asJSON   bool
		o        layerOptions
	)
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print the checkout's fugaro.yaml resolved over the project layer, with where each value comes from",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, rf, err := loadCheckoutResolved(cmd.Context(), "", nil, o)
			if err != nil {
				return err
			}
			values, err := showValues(rf.Cfg, rf.Res, workflow)
			if err != nil {
				return userErr("%v", err)
			}
			doc := configShowDoc{ConfigSHA256: rf.Res.ConfigSHA256, Values: values}
			if l := rf.Layer.Layer; l != nil {
				doc.ProjectLayer = &validateLayer{Where: rf.Layer.Where, Generation: rf.Layer.Generation, SHA256: l.SHA256}
			}
			if rf.Layer.Note != "" {
				doc.Notes = append(doc.Notes, rf.Layer.Note)
			}
			return printConfigShow(cmd.OutOrStdout(), doc, rf.Layer.CheckedAt, asJSON)
		},
	}
	cmd.Flags().StringVar(&workflow, "workflow", "", "show only this workflow's keys besides the top-level ones")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	cmd.Flags().StringVar(&o.File, "project-layer", "", "resolve against this project layer file instead of the published one")
	cmd.Flags().BoolVar(&o.Offline, "offline", false, "never read the runs bucket: resolve against the cached project layer, if any")
	return cmd
}

// showValues flattens cfg into its keys, each with its value and source,
// sorted by path; workflow, when set, keeps only that workflow's keys
// besides the top-level ones. A list is one key.
func showValues(cfg *config.Config, res *config.Resolution, workflow string) ([]configShowValue, error) {
	if workflow != "" {
		if _, ok := cfg.Workflows[workflow]; !ok {
			return nil, fmt.Errorf("fugaro.yaml has no workflow %q", workflow)
		}
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var tree map[string]any
	if err := yaml.Unmarshal(data, &tree); err != nil {
		return nil, err
	}
	var out []configShowValue
	var walk func(path string, v any)
	walk = func(path string, v any) {
		if m, ok := v.(map[string]any); ok && len(m) > 0 {
			for k, e := range m {
				walk(strings.TrimPrefix(path+"."+k, "."), e)
			}
			return
		}
		if parts := strings.SplitN(path, ".", 3); workflow != "" && len(parts) >= 2 && parts[0] == "workflows" && parts[1] != workflow {
			return
		}
		out = append(out, configShowValue{Path: path, Value: v, Source: res.SourceOf(path)})
	}
	walk("", tree)
	// project, gcp_project, profile and every workflow's profile are
	// yaml:"...,omitempty" (config.go): the merge round-trip above leaves
	// them out of tree entirely when they resolve to "", the same way the
	// file itself would, so walk never visits their path. config show's
	// whole purpose is to say where a value came from even when that
	// value is empty (the hook a script uses to check a repository
	// resolves as intended, above) — a path missing here would read as
	// "doesn't exist" rather than "resolves to the default", so each one
	// not already present gets a row naming its real source.
	have := make(map[string]bool, len(out))
	for _, v := range out {
		have[v.Path] = true
	}
	ensure := func(path string) {
		if !have[path] {
			out = append(out, configShowValue{Path: path, Value: "", Source: res.SourceOf(path)})
		}
	}
	ensure("project")
	ensure("gcp_project")
	ensure("profile")
	for name := range cfg.Workflows {
		if workflow == "" || name == workflow {
			ensure("workflows." + name + ".profile")
		}
	}
	slices.SortFunc(out, func(a, b configShowValue) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

func printConfigShow(w io.Writer, doc configShowDoc, checked time.Time, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	}
	if l := doc.ProjectLayer; l != nil {
		fmt.Fprintf(w, "project layer: %s, generation %d, sha256 %s, read %s ago\n", pluginwire.Printable(l.Where), l.Generation, l.SHA256, time.Since(checked).Round(time.Second))
	} else {
		fmt.Fprintln(w, "project layer: none applies")
	}
	for _, n := range doc.Notes {
		fmt.Fprintln(w, "note: "+pluginwire.Printable(n))
	}
	fmt.Fprintf(w, "resolved sha256: %s\n\n", doc.ConfigSHA256)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tVALUE\tSOURCE")
	for _, v := range doc.Values {
		val, _ := json.Marshal(v.Value)
		fmt.Fprintf(tw, "%s\t%s\t%s\n", v.Path, pluginwire.Printable(string(val)), v.Source)
	}
	return tw.Flush()
}

func newConfigLayerCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "layer",
		Short: "Print the project layer published for this checkout's project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := gitRead(cmd.Context(), ".", "rev-parse", "--show-toplevel")
			if err != nil {
				return userErr("not inside a git checkout; run this from the repository")
			}
			data, err := readFugaroYAML(root + "/fugaro.yaml")
			if err != nil {
				return userErr("%v", err)
			}
			fl, err := findLayer(cmd.Context(), getenvOS, data, selectedProjectConfig(cmd.Context()), layerOptions{}, time.Now())
			if err != nil {
				return err
			}
			l := fl.Layer
			if l == nil {
				msg := "no project layer applies to this checkout (its fugaro.yaml needs gcp_project:, and the project must publish one with fugaro config publish)"
				if fl.Note != "" {
					msg += "; " + fl.Note
				}
				return userErr("%s", msg)
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{"where": fl.Where, "generation": fl.Generation, "sha256": l.SHA256, "yaml": string(l.Raw)})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "# %s, generation %d, sha256 %s\n%s", pluginwire.Printable(fl.Where), fl.Generation, l.SHA256, printableLines(string(l.Raw)))
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	return cmd
}
