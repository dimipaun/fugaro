package cli

import (
	"fmt"

	"os"
	"path/filepath"
	"slices"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

func newImagePromoteCmd() *cobra.Command {
	var o cloudOptions
	cmd := &cobra.Command{
		Use:   "promote <repo> [name]",
		Short: "Copy a repository's image definition into the catalog and point the repository at it",
		Long: "Read the repository's existing image definition, write it into the project's\n" +
			"image catalog as an entry, and point the repository's workflow at that entry\n" +
			"(image_name:), in the checkout you are in. Run it from the repository's own\n" +
			"checkout; the entry is named [name], else the repository's name.\n\n" +
			"Nothing is converted on its own: a repository's image: block or dockerfile:\n" +
			"keeps working untouched until this command is run for it, and fugaro update\n" +
			"never runs it. A definition taken from the workflow's image: block is\n" +
			"rendered and held in the entry (source: inline); a dockerfile: path stays a\n" +
			"pointer at that file (source: dockerfile), so the entry follows it.\n\n" +
			"The change to fugaro.yaml is a file in your working tree, not a commit: it is\n" +
			"yours to review and commit beside whatever else the same change carries.\n\n" +
			"This promote is not the promote step of a Cloud Build (images/derived/\n" +
			"cloudbuild.yaml): that step moves a build's latest tag onto the image it\n" +
			"just built, and this command copies a definition into the catalog. The two\n" +
			"share the word and nothing else.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 2 {
				name = args[1]
			}
			return runImagePromote(cmd, o, args[0], name)
		},
	}
	addCloudFlags(cmd, &o)
	return cmd
}

// runImagePromote is D1147: the definition is read from the checkout, written
// into the catalog, and the repository is repointed at it — never the
// reverse, and never automatically.
func runImagePromote(cmd *cobra.Command, o cloudOptions, repo, name string) error {
	ctx := cmd.Context()
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	root, cfg, err := loadCheckoutConfig(ctx)
	if err != nil {
		return err
	}
	origin, err := originRepo(ctx)
	if err != nil {
		return err
	}
	if origin != repo {
		return userErr("fugaro image promote copies the definition of the repository whose checkout it is run from, and this one is %s, not %s: run it from %s's checkout",
			pluginwire.Printable(origin), pluginwire.Printable(repo), pluginwire.Printable(repo))
	}
	workflow, w, err := cfg.SelectWorkflow("")
	if err != nil {
		return userErr("%v", err)
	}
	if w.Checkout == config.CheckoutClone || w.Checkout == config.CheckoutShared {
		return userErr("the workflow %s of %s runs the installation's own image (checkout: %s) and builds nothing of its own, so there is no definition to copy into the catalog",
			pluginwire.Printable(workflow), pluginwire.Printable(repo), pluginwire.Printable(w.Checkout))
	}
	if w.ImageName != "" {
		return userErr("the workflow %s of %s already names the catalog entry %s: fugaro image ls lists the entries, and fugaro image add writes one nothing is pointed at yet",
			pluginwire.Printable(workflow), pluginwire.Printable(repo), pluginwire.Printable(w.ImageName))
	}
	if w.Dockerfile == "" && w.Image.IsZero() {
		return userErr("the workflow %s of %s sets neither image: nor dockerfile:, so it has no definition of its own to copy: give it one, or write the entry by hand with fugaro image add",
			pluginwire.Printable(workflow), pluginwire.Printable(repo))
	}
	if name == "" {
		if _, n, ok := gitprov.SplitRepo(repo); ok {
			name = n
		}
	}
	e, err := promotedEntry(root, cfg, repo, workflow, w, name)
	if err != nil {
		return err
	}
	path, onDisk, old, err := catalogConfig(ctx, o)
	if err != nil {
		return err
	}
	if onDisk.ImageCatalog == nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "note: this machine's project config names no image catalog, so this entry is written as the project's whole catalog: if the project already has one published, it is replaced by this file's rather than added to")
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
	// The checkout is edited in memory before either file is written: both
	// changes are one change (D1147), and a fugaro.yaml this command cannot
	// read or edit must leave the project config as it was rather than an
	// entry nothing is pointed at. Only the last write can then leave the
	// two halves apart.
	yamlPath := filepath.Join(root, "fugaro.yaml")
	raw, err := readFugaroYAML(yamlPath)
	if err != nil {
		return userErr("%s could not be read to point the workflow %s at the catalog entry %s: %v", pluginwire.Printable(yamlPath), pluginwire.Printable(workflow), pluginwire.Printable(e.Name), err)
	}
	edited, err := setWorkflowImageName(raw, workflow, e.Name)
	if err != nil {
		return userErr("%s could not be edited to point the workflow %s at the catalog entry %s: %v", pluginwire.Printable(yamlPath), pluginwire.Printable(workflow), pluginwire.Printable(e.Name), err)
	}
	if err := userSave(cmd.OutOrStdout(), path, old, next); err != nil {
		return err
	}
	if err := writeFileAtomic(yamlPath, edited); err != nil {
		return userErr("the catalog entry %s is written, but pointing the workflow at it failed: %v", pluginwire.Printable(e.Name), err)
	}
	w2 := cmd.OutOrStdout()
	fmt.Fprintf(w2, "the workflow %s of %s now names the catalog entry %s, in %s: review and commit that change, and fugaro update publishes the catalog\n",
		pluginwire.Printable(workflow), pluginwire.Printable(repo), pluginwire.Printable(e.Name), pluginwire.Printable(yamlPath))
	if len(e.Tags) == 0 {
		fmt.Fprintf(w2, "the entry has no tags, so only this pin picks it: give it tags in the project config (fugaro image ls prints the entries) if other repositories' needs: should choose it\n")
	}
	return nil
}

// promotedEntry is the catalog entry the repository's definition becomes. A
// dockerfile: path is copied as a pointer at that file — the entry then
// follows the file, and the catalog build reads it from the repository, as
// the spec's §Where Dockerfiles live describes — and an image: block is
// rendered through the engine's own template and held in the entry, which is
// what "copies the definition" means for a definition that exists only as
// configuration.
func promotedEntry(root string, cfg *config.Config, repo, workflow string, w config.Workflow, name string) (config.Entry, error) {
	e := config.Entry{Name: name, Description: "the image " + repo + "'s workflow " + workflow + " had before it was promoted"}
	if w.Dockerfile != "" {
		e.Source = config.Source{Kind: config.SourceDockerfile, Repo: repo, Path: w.Dockerfile, Branch: cfg.Git.BaseBranch}
	} else {
		data, _, err := image.Dockerfile(root, cfg, workflow, Version)
		if err != nil {
			return config.Entry{}, userErr("reading the definition of the workflow %s: %v", pluginwire.Printable(workflow), err)
		}
		e.Source = config.Source{Kind: config.SourceInline, Inline: string(data)}
	}
	if problems := config.ValidateCatalog("image_catalog", config.Catalog{Entries: []config.Entry{e}}); len(problems) > 0 {
		return config.Entry{}, userErr("the entry %s: %s: %s", pluginwire.Printable(e.Name), problems[0].Path, problems[0].Message)
	}
	return e, nil
}

// setWorkflowImageName sets workflows.<workflow>.image_name in a checkout's
// fugaro.yaml. The document is edited as a node tree rather than marshalled
// from a config: a re-marshal would drop every comment the file carries and
// reorder its keys, and this command's whole change is one line.
func setWorkflowImageName(raw []byte, workflow, entry string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("it is not a YAML document this command can edit")
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("its root is not a mapping")
	}
	flows := yamlMappingValue(doc.Content[0], "workflows")
	if flows == nil || flows.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("it has no workflows mapping")
	}
	w := yamlMappingValue(flows, workflow)
	if w == nil || w.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("it has no workflow %s", pluginwire.Printable(workflow))
	}
	yamlSetMappingValue(w, "image_name", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: entry})
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, fmt.Errorf("it could not be written back as YAML")
	}
	return out, nil
}

// yamlMappingValue is the node a mapping holds under key, nil when it holds
// none.
func yamlMappingValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// yamlSetMappingValue sets key to value in a mapping, replacing the value
// where the key is already there and appending both where it is not.
func yamlSetMappingValue(m *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = value
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}
