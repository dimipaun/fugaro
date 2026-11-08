package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/craft"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// `fugaro craft ls` is the effective-craft read of the model-crafts plan's
// MC8 (D1633): one row per craft, the model it runs and the credential or pool
// its calls go out on, with the layer each half came from and whether what it
// names serves anything yet.
//
// Why it ships before anything can write a craft key (D1633). A craft's two
// halves inherit separately, so "which model does the security review run, and
// whose credential does it spend" is a question the documents answer only by
// reading three of them and applying the inheritance rule by hand. This is the
// read that answers it, and it is a read alone -- no craft key is writable
// until MC5 -- which is why it is safe to ship first.
//
// Why it is offline. The layers it resolves are the documents this machine
// already holds: the checkout the command runs in (agent.crafts), the
// installation's own config (crafts, credentials, pools) and the project layer
// file the last cloud read cached (defaults.agent.crafts). It reaches no
// bucket, no database and no host, so it answers with no credentials and no
// cloud at all.
//
// Why the answers are the resolver's and not this command's. The inheritance
// chain, the layer order and the inert rule are internal/craft's (MC2), and a
// command that re-derived any of them would be the second rule that package
// exists to prevent. What this file owns is reading the layers and rendering
// the answer, and nothing else.
//
// It IS one of the reads the desktop console spawns (design §5.1 rule 6), and
// that is a file as much as a row: `checkoutProject` is what records a
// checkout, and this command is on consoleReadPaths, which is what makes
// recordCheckout return before it writes.
func newCraftCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "craft",
		Short: "Show the model and the credential or pool each craft runs on",
		Long: `Show the four crafts: the coder, the first-line review, the senior review
and the security review.

Each craft runs a model and sends its calls out on a credential or a pool, and
the two halves inherit separately: the first-line review follows the coder and
the security review follows the senior review, so a craft that sets only one
half keeps the other half it follows. A craft that sets no access anywhere
resolves to the project's default credential.

Every half names where it came from: the layer that wrote it (repo, local or
project), the craft it was inherited from, or default when nothing set it. A
pool holding two or more Claude OAuth credentials serves nothing until the
project's subscription-spread switch is on, and the row says so.

The read is offline: it reads this checkout's fugaro.yaml, the installation's
own config and the project layer file the last cloud read cached, and reaches
no bucket, no database and no host.`,
	}
	cmd.AddCommand(newCraftLsCmd())
	return cmd
}

// craftLsDocVersion is the document's first version, and the number a member
// added later keeps: schemas/craft-ls.schema.json pins it. The document is
// versioned from its first release because the console reads it (F16).
const craftLsDocVersion = 1

type craftLsOptions struct {
	cloud cloudOptions
	json  bool
}

// newCraftLsCmd is `fugaro craft ls`: the effective-craft read.
func newCraftLsCmd() *cobra.Command {
	var o craftLsOptions
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "Show each craft's effective model and access, and where each came from",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runCraftLs(cmd, &o) },
	}
	cmd.Flags().BoolVar(&o.json, "json", false, "print the versioned document")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

// craftLsDoc is what `fugaro craft ls --json` prints, under
// schemas/craft-ls.schema.json. It is a closed shape in the way every document
// here is: a member added later is a new name, and a member whose meaning has
// to change bumps the version rather than being reused (CLAUDE.md).
type craftLsDoc struct {
	Version int `json:"version"`
	// Project is the selected project's name; absent when no project config
	// is selected, which leaves every craft with the default credential and
	// no model.
	Project string `json:"project,omitempty"`
	// Crafts is one row per craft, in config.CraftNames' order.
	Crafts []craftLsRow `json:"crafts"`
	// Problems are the accesses that name no credential and no pool any layer
	// holds, as internal/craft reports them: the path of the key in the
	// document that wrote it, and what is wrong with it.
	Problems []string `json:"problems,omitempty"`
}

// craftLsRow is one craft's answer: the resolver's own five facts, under the
// names the document spells them with.
type craftLsRow struct {
	Craft string `json:"craft"`
	// Model is the model ID this craft runs, and absent when no layer sets one
	// and the craft it follows sets none either -- a configuration no run can
	// serve, which the row states rather than hiding behind a model no layer
	// wrote.
	Model string `json:"model,omitempty"`
	// ModelFrom is where Model came from: the layer that set it ("repo",
	// "local", "project"), the craft it was inherited from ("inherited from
	// coder"), or "default" when nothing set it.
	ModelFrom string `json:"model_from"`
	// Access is the credential or pool this craft's calls go out on. It is
	// never empty: an access no layer sets is the project's default
	// credential.
	Access string `json:"access"`
	// AccessFrom is where Access came from, as ModelFrom is where Model did.
	AccessFrom string `json:"access_from"`
	// Inert is whether Access names a pool that serves nothing yet: a pool of
	// two or more Claude OAuth credentials while the project's
	// subscription-spread switch is off (D1622).
	Inert bool `json:"inert"`
}

// runCraftLs is the read: it assembles the layers, asks the resolver and
// renders the answer.
func runCraftLs(cmd *cobra.Command, o *craftLsOptions) error {
	lc, err := craftLsConfig(cmd, o.cloud)
	if err != nil {
		return err
	}
	project := ""
	if lc != nil {
		project = lc.Name
	}
	eff, ps := craft.Resolve(craftLsLayers(cmd, lc, project), false)
	doc := craftLsDoc{Version: craftLsDocVersion, Project: project}
	for _, name := range config.CraftNames {
		e := eff[name]
		doc.Crafts = append(doc.Crafts, craftLsRow{
			Craft: name, Model: e.Model, ModelFrom: e.ModelFrom,
			Access: e.Access, AccessFrom: e.AccessFrom, Inert: e.Inert,
		})
	}
	for _, p := range ps {
		doc.Problems = append(doc.Problems, p.String())
	}
	if o.json {
		return writeJSON(cmd.OutOrStdout(), doc)
	}
	return writeCraftLs(cmd, doc)
}

// craftLsConfig is the selected project config, nil when none is selected: the
// crafts then answer with the default credential and no model, which is what a
// machine with no installation has. It is pricesConfig's rule and not
// pricesConfig itself -- that one's note on the same path names prices -- and
// it resolves the checkout through checkoutProject like every other offline
// read, which writes nothing here because this command is on consoleReadPaths.
func craftLsConfig(cmd *cobra.Command, o cloudOptions) (*localcfg.Config, error) {
	co, err := checkoutProject(cmd.Context(), "")
	if err != nil {
		return nil, err
	}
	sel, lc, err := selectFrom(o, co, false)
	if err != nil {
		if o.config != "" || o.project != "" || os.Getenv("FUGARO_PROJECT") != "" || os.Getenv("FUGARO_CONFIG") != "" {
			return nil, err
		}
		return nil, nil
	}
	return lc, announce(o, sel, lc)
}

// craftLsLayers are the layers the resolver reads, narrowest first -- the
// checkout's file, the installation's config, the project layer file -- which
// is the order internal/craft resolves a half in and the order this repository
// resolves a fugaro.yaml key in (config.EffectiveBaseBranch).
//
// The installation's config carries the credentials and the pools as well as
// the crafts, because it is the owner's own answer (the same one the policy
// node publishes): an access names a credential or a pool, and only the owner
// may create either (F12, D1628). The project layer file carries crafts alone,
// and only their models: config.Scopes refuses a craft access there, since
// anyone who can write the runs bucket can write that file.
func craftLsLayers(cmd *cobra.Command, lc *localcfg.Config, project string) []craft.Layer {
	var layers []craft.Layer
	if crafts, ok := checkoutCrafts(cmd); ok {
		layers = append(layers, craft.Layer{Name: craft.SourceRepo, Crafts: crafts})
	}
	if lc != nil {
		layers = append(layers, craft.Layer{
			Name:        craft.SourceLocal,
			Crafts:      lc.Crafts,
			Credentials: lc.Credentials,
			Pools:       lc.Pools,
			KeyPrefix:   craftLocalKeyPrefix,
		})
	}
	if l := modelsLayer(project); l != nil {
		layers = append(layers, craft.Layer{
			Name:      craft.SourceProject,
			Crafts:    l.Defaults.Agent.Crafts,
			KeyPrefix: craftProjectKeyPrefix,
		})
	}
	return layers
}

// The craft keys' own paths in the two documents that write them, neither of
// which is a repository's fugaro.yaml: the installation config writes `crafts`
// and the project layer file `defaults.agent.crafts`. internal/craft carries
// them (craft.Layer.KeyPrefix) so a problem about an access is reported at the
// path of the key in the document that wrote it.
const (
	craftLocalKeyPrefix   = "crafts"
	craftProjectKeyPrefix = "defaults.agent.crafts"
)

// checkoutCrafts is the working directory's checkout's agent block, and
// whether the directory is a checkout whose file this machine can read at all.
//
// The block is read with config.AgentOf rather than by resolving the whole
// file, so a fugaro.yaml this build refuses for something else still says
// which crafts it selects -- and a file it cannot read, a directory that is no
// checkout and a checkout with no fugaro.yaml each contribute no layer rather
// than a guess.
func checkoutCrafts(cmd *cobra.Command) (config.Crafts, bool) {
	co, err := resolveCheckout(cmd.Context(), "")
	if err != nil || co == nil {
		return config.Crafts{}, false
	}
	data, err := readFugaroYAML(filepath.Join(co.Root, "fugaro.yaml"))
	if err != nil {
		return config.Crafts{}, false
	}
	agent, ok := config.AgentOf(data)
	if !ok {
		return config.Crafts{}, false
	}
	return agent.Crafts, true
}

// writeCraftLs is the read's text form: one row per craft, and the problems
// after the table, on stderr, where every other warning of this package goes.
func writeCraftLs(cmd *cobra.Command, doc craftLsDoc) error {
	w := cmd.OutOrStdout()
	if doc.Project == "" {
		fmt.Fprintln(w, "crafts: no project config selected, so the default credential and no model")
	} else {
		fmt.Fprintf(w, "crafts for project %s (offline)\n", oneLine(doc.Project))
	}
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "CRAFT\tMODEL\tMODEL FROM\tACCESS\tACCESS FROM\tSERVES")
	for _, r := range doc.Crafts {
		model := r.Model
		if model == "" {
			model = "—"
		}
		serves := "yes"
		if r.Inert {
			serves = "no: the subscription-spread switch is off"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			cellOneLine(r.Craft), cellOneLine(model), cellOneLine(r.ModelFrom),
			cellOneLine(r.Access), cellOneLine(r.AccessFrom), serves)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, p := range doc.Problems {
		fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: problem: %s\n", oneLine(p))
	}
	return nil
}
