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
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runner"
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
		Long: `Show the crafts: the coder, the first-line review, the senior review,
the security review and the merge judge.

Each craft runs a model and sends its calls out on a credential or a pool, and
the two halves inherit separately: the first-line review and the merge judge
follow the coder, and the security review follows the senior review, so a craft
that sets only one half keeps the other half it follows. A craft that sets no
access anywhere resolves to the project's default credential.

Every half names where it came from: the layer that wrote it (repo, local or
project), the craft it was inherited from, or default when nothing set it. A
pool holding two or more Claude OAuth credentials serves nothing until the
project's subscription-spread switch is on — the switch this machine's own
config writes — and the row says so.

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
	// Servable is whether a run can serve Model: the project's allow-list
	// admits it, a route claims it and a price row covers it -- MC15's verdict
	// (D1665), asked here so a reader sees the refusal before a run pays for
	// it at the gateway. A craft that names no model is servable by this
	// answer and changes no verdict (F17): it is served by the role slot below
	// it, and no model of its own is missing.
	Servable bool `json:"model_servable"`
	// ModelProblem is why not, in one sentence: the first problem the checks
	// report about this craft's model, in their own order. Absent when
	// Servable is true.
	ModelProblem string `json:"model_problem,omitempty"`
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
	files := craftLsFiles{lc: lc}
	if data, ok := checkoutFugaroYAML(cmd); ok {
		files.repo = true
		files.data = data
		if agent, ok := config.AgentOf(data); ok {
			files.agent = agent
		} else {
			files.repo = false
		}
	}
	files.layer = modelsLayer(project)
	eff, ps := craft.Resolve(craftLsLayers(files), craftLsSpread(lc))
	serves := craftLsServes(cmd, files)
	doc := craftLsDoc{Version: craftLsDocVersion, Project: project}
	for _, name := range config.CraftNames {
		e := eff[name]
		why := serves.modelProblem(name, e.Model)
		doc.Crafts = append(doc.Crafts, craftLsRow{
			Craft: name, Model: e.Model, ModelFrom: e.ModelFrom,
			Access: e.Access, AccessFrom: e.AccessFrom, Inert: e.Inert,
			Servable: why == "", ModelProblem: why,
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

// craftLsFiles are the three documents the read resolves, read once for the
// whole command: the installation's own config, the checkout the command runs
// in, and the project layer file the last cloud read cached. The checks below
// and the resolver read the same three, which is why they are read here and
// handed down rather than each read where it is used.
type craftLsFiles struct {
	lc *localcfg.Config
	// data is the checkout's own fugaro.yaml and agent its agent block, both
	// empty when this directory is no checkout or its file is one this build
	// cannot read: the two commands that judge a model need the file's bytes
	// and the resolver needs the block.
	data  []byte
	agent config.Agent
	repo  bool
	// layer is the cached project layer file, nil when none applies or the
	// copy this machine holds is not one this build can parse.
	layer *config.ProjectLayer
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

// craftLsSpread is the project's subscription-spread switch as this machine
// knows it: the installation's own config, which is where the owner writes it
// (`fugaro settings set subscription_spread`) and the value the publish that
// carries it to the policy node reads (localcfg's SubscriptionSpreadEffective).
// An unset key is off, the field's own default and the answer a run reads of a
// node that carries no member; the one case this offline read cannot see is a
// node another machine turned on, whose member this machine holds no copy of.
//
// It is read here because the SERVES column states what a run will do with the
// access, and a run reads this switch: resolving with a hardcoded false printed
// `no: the subscription-spread switch is off` on the machine whose config had
// just turned it on, for the pool the switch exists to serve.
func craftLsSpread(lc *localcfg.Config) bool {
	return lc != nil && lc.SubscriptionSpread != nil && *lc.SubscriptionSpread
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
func craftLsLayers(f craftLsFiles) []craft.Layer {
	var layers []craft.Layer
	if f.repo {
		layers = append(layers, craft.Layer{Name: craft.SourceRepo, Crafts: f.agent.Crafts})
	}
	if f.lc != nil {
		layers = append(layers, craft.Layer{
			Name:        craft.SourceLocal,
			Crafts:      f.lc.Crafts,
			Credentials: f.lc.Credentials,
			Pools:       f.lc.Pools,
			KeyPrefix:   craftLocalKeyPrefix,
		})
	}
	if f.layer != nil {
		layers = append(layers, craft.Layer{
			Name:      craft.SourceProject,
			Crafts:    f.layer.Defaults.Agent.Crafts,
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
	// craftNodeKeyPrefix is where the published policy node writes the crafts.
	// It is the installation config's own path because the node is that config
	// published (a launch resolves the node, a `fugaro craft ls` the config),
	// so a problem about a model the node wrote names the key the operator
	// edits in the document they have.
	craftNodeKeyPrefix = craftLocalKeyPrefix
)

// checkoutFugaroYAML is the working directory's checkout's fugaro.yaml, and
// whether this directory is a checkout whose file this machine can read at all.
//
// The bytes are read whole rather than only as far as the agent block, because
// the model checks below judge the file as a run would (config.Resolve over the
// project layer), and a file it cannot read, a directory that is no checkout
// and a checkout with no fugaro.yaml each contribute no layer rather than a
// guess.
func checkoutFugaroYAML(cmd *cobra.Command) ([]byte, bool) {
	co, err := resolveCheckout(cmd.Context(), "")
	if err != nil || co == nil {
		return nil, false
	}
	data, err := readFugaroYAML(filepath.Join(co.Root, "fugaro.yaml"))
	if err != nil {
		return nil, false
	}
	return data, true
}

// craftLsServe is the facts MC15's checks answer a craft's model with, read
// once for the whole command: the pins the checkout writes, the allow-list a
// run of it is under, the routes its runs carry and the price table its model
// calls are charged from.
//
// None of them is a rule of this file's. The allow-list is validate's own merge
// (policy.Merge of the project's ceiling and the file in hand), the routes are
// the run's own table (layerRoutes then runner.ProvidersFor, the entries
// FUGARO_MODEL_PROVIDERS is written from) and the prices are priceTableFor's --
// the one table `validate`, `diagnose` and `budget prices` price with, so this
// row cannot disagree with the refusal a launch would report (MC15, D1665).
type craftLsServe struct {
	agent   config.Agent
	e       policy.Effective
	ceiling []string
	routing *config.Routing
	routes  map[string]config.ModelProvider
	prices  *pricing.Table
	// priced is whether a price row is required of a craft's model at all: a
	// run prices its calls whenever it has a gateway, and the one shape of a
	// run without one is agent.auth oauth (D3026, R12). It is validate's own
	// gate — budgetProblems' caller, on the auth alone — and the auth it reads
	// is the resolved one: a run's auth is what the project answers with, not
	// what one document of the two says. The mode was the second condition
	// here, so a craft under an off mode was never priced (AB4, D3020): every
	// run is accounted for now, and the price of a craft's model is what says
	// a run of that craft can be.
	priced bool
}

// craftLsServes assembles them from the documents the read already resolved.
// A fact this machine does not hold -- no project config, no checkout file, a
// price table the rules refuse -- is not asked, and the row then states the
// verdict the remaining checks give rather than a guess at the missing one.
func craftLsServes(cmd *cobra.Command, f craftLsFiles) craftLsServe {
	s := craftLsServe{agent: f.agent}
	if s.agent.Auth == "" {
		s.agent.Auth = config.DefaultAgentAuth
	}
	if f.lc == nil {
		return s
	}
	ceiling := ceilingLayer(f.lc)
	var file policy.Layer
	if f.repo {
		// The file as a run resolves it: over the project layer file, whose
		// defaults this checkout's own config is judged with (config.Resolve,
		// the runner's own read). A file the rules refuse leaves the ceiling
		// alone and judges no craft by a config no run would start on.
		if cfg, _, ps := config.Resolve(f.data, f.layer); len(ps) == 0 {
			ceiling, file = runCeiling(f.lc, cfg), runner.FileLayer(cfg)
			// And the agent a run reads with it: the resolved one, never the
			// checkout's own block. The project layer's defaults.agent.auth is
			// the project's answer, and it is what the price gate below turns
			// on — a file naming api-key under a project whose auth is oauth
			// would otherwise price a craft no run charges for.
			s.agent = cfg.Agent
		}
	}
	s.e = policy.Merge(ceiling, file)
	s.ceiling = ceiling.AllowedModels
	s.routing = f.lc.Routing
	// The routes are the repository's own: an entry whose allow_data_to does
	// not name it is not one this repository's runs carry, which is the table
	// the run judges a model against. An unknown repository (a checkout with
	// no origin) is the reading the run's own rules give it, "".
	repo, err := originRepo(cmd.Context())
	if err != nil {
		repo = ""
	}
	_, claimed, _ := layerRoutes(f.lc, f.layer, repo)
	s.routes = runner.ProvidersFor(claimed, repo)
	if ov, err := f.lc.Overrides(); err == nil {
		if t, err := priceTableFor(f.lc.Providers, ov, budgetNow()); err == nil {
			s.prices = t
			s.priced = s.agent.Auth != "oauth"
		}
	}
	return s
}

// modelProblem is the row's answer: MC15's verdict for one craft's model, as
// the one sentence naming what is missing, and "" when a run can serve it.
//
// One sentence and not all of them, because a row is a summary and a reader who
// mends the first is given the next on the next read -- and the first is the
// checks' own order: the allow-list, then the route (both config.CheckAllowed's),
// then the price (config.CheckPins'). The checks judge every pin of the run as
// well as the craft, so the answer is the problem reported at this craft's own
// key.
func (s craftLsServe) modelProblem(craft, model string) string {
	m := config.CraftModel{Craft: craft, Model: model, Path: craftLsModelKey(craft)}
	ps := config.CheckAllowed(s.agent, s.e, s.ceiling, s.routing, s.routes, m)
	if s.priced {
		ps = append(ps, config.CheckPins(s.agent, s.prices, m)...)
	}
	for _, p := range ps {
		if p.Path == m.Path {
			return p.Message
		}
	}
	return ""
}

// craftLsModelKey is the key the checks above are asked about: the
// repository's own agent.crafts.<craft>.model. The row carries the sentence and
// not the path -- which document wrote the model is what model_from says -- so
// one key serves for all three documents, and it is also what makes the answer
// this row's to find among the pins' own problems.
func craftLsModelKey(craft string) string { return "agent.crafts." + craft + ".model" }

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
