package cli

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/routing"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/settings"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

type modelsUseOptions struct {
	layer string
}

func newModelsUseCmd() *cobra.Command {
	var o modelsUseOptions
	cmd := &cobra.Command{
		Use:   "use [<repo>] <role|key> <id>",
		Short: "Point a role or a model key at a model, in a repository or in the project layer",
		Long: "Write the key that makes a role, a level, a recipe or the project's own model\n" +
			"run what is named, and nothing else in the file: every comment, every other\n" +
			"byte and the key order are kept.\n\n" +
			"A role name is the shorthand for agent.models.<role> — coder, reviewer,\n" +
			"background, vision and diagnose — and a key may be named in full\n" +
			"(agent.model, agent.recipe, agent.levels.l2.model, agent.levels.l2.effort).\n" +
			"--layer project, the default for a model key, writes this machine's project\n" +
			"layer file, where the key is the layer.defaults.agent.<same> one and\n" +
			"applies to every repository of the project — a model is the project's\n" +
			"choice (D3237), so agent.model, agent.models.* and agent.levels.<l>.model\n" +
			"are stated there and nowhere else, and a write of one into a repository\n" +
			"(its name, or --layer repo) is refused. --layer repo writes the\n" +
			"repository's own fugaro.yaml in the checkout's working tree for the owner\n" +
			"to review and commit, which is where agent.recipe and a level's effort\n" +
			"still live. layer.claude_models, the Claude models a project's routes may\n" +
			"serve, is written as provider=anthropic/<id> at --layer project alone.\n\n" +
			"A switch is judged the way a launch judges it: a route of the project config\n" +
			"must claim the model and its allow_data_to must name the repository (every\n" +
			"repository the project onboards, for a project-layer write), the model must\n" +
			"be priced, budget.allowed_models must admit it, and the repository's own\n" +
			"agent.auth and budget must support a provider model. A failure is a refusal\n" +
			"with nothing written, and the message names the `fugaro models add` that would\n" +
			"fix a missing claim or price.\n\n" +
			"The command never commits, pushes or opens a pull request — a repository's\n" +
			"change reaches runs when the owner's own branch is merged to the base branch,\n" +
			"which is what every run reads.",
		Args: cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runModelsUse(cmd, args, &o)
		},
	}
	cmd.Flags().StringVar(&o.layer, "layer", "", "where the write lands: project (this machine's project layer file, the default for a model key) or repo (a repository's own fugaro.yaml, for agent.recipe and a level's effort); local is no layer these keys have")
	return cmd
}

// runModelsUse is the whole of the command: the name a person typed is resolved
// into the registry row the write goes through and the layer it lands in, the
// value is judged the way a launch would judge it, and the one key is written.
//
// The layer is read from the key's own spelling where it has one
// (`layer.defaults.agent.model` is a project-layer key and nothing else) and
// from --layer otherwise, and a layer the key does not have is refused by the
// resolver naming the layers it does have (contract §6, T3): no models key has
// a local layer, so `--layer local` is always that refusal.
func runModelsUse(cmd *cobra.Command, args []string, o *modelsUseOptions) error {
	repo, name, id := "", args[0], args[1]
	if len(args) == 3 {
		repo, name, id = args[0], args[1], args[2]
	}
	// A key named with the layer's own spelling names its layer, so the layer
	// is read off the name before the name is reduced to the repository's
	// spelling of the same key.
	layered := strings.HasPrefix(name, "layer.")
	base, kind, ok := modelsUseBase(name)
	if !ok {
		return userErr("%s: models use writes a role name (%s) or one of %s", oneLine(name), strings.Join(modelsRoleNames, ", "), strings.Join(modelsUseKeyNames(), ", "))
	}
	baseRow, found := settings.Lookup(base)
	if !found {
		return userErr("%s: no setting has that name", base)
	}
	// A lifted key has one home (R15): the project layer. So the default is the
	// project for it — a repository's own file is no longer a layer it is stated
	// in, and the narrowest layer it may be set in is the project's — and no
	// write of one lands in a repository, whether the repository is named
	// positionally or `--layer repo` is spelled: a write release A's runs drop
	// is the trap R15 refuses and F23 raised.
	lifted, isLifted := liftedKeyAt(base)
	layer := o.layer
	if layer == "" {
		switch {
		case layered || strings.HasPrefix(base, "layer."), isLifted:
			layer = settingsLayerProject
		default:
			layer = settingsDefaultLayer(baseRow)
		}
	}
	if isLifted && (layer == settingsLayerRepo || repo != "") {
		return userErr("%s", lifted.Why)
	}
	// The project layer spells every key it carries with the `layer.` prefix, so
	// a project-layer write of agent.models.coder resolves through the
	// layer.defaults.agent.models.coder row: its Layers are the project's alone
	// — which is what refuses a wrong --layer with the layers the key has — and
	// its key path is the one inside the layer's own defaults: block.
	row := baseRow
	if !strings.HasPrefix(base, "layer.") && layer == settingsLayerProject {
		row, found = settings.Lookup("layer.defaults." + base)
		if !found {
			return userErr("%s: the project layer has no key for it", base)
		}
	}

	sel, lc, err := settingsSelection()
	if err != nil {
		return err
	}
	ctx := context.Background()
	opts := settingsWriteOptions{layer: layer}
	if layer == settingsLayerRepo {
		repo, err = modelsUseRepo(repo, name, id)
		if err != nil {
			return err
		}
		// The checkout the working directory is in is the file to edit when it
		// is that repository's; the repository is named to the resolver only
		// when it is not, so the remembered checkout is what the write lands
		// in (settingsRepoFile's two steps, which this command reads the same
		// way a --repo owner/name write does).
		if _, here := checkoutOriginAt(""); here == "" || !strings.EqualFold(here, repo) {
			opts.repo = repo
		}
	} else if repo != "" {
		return userErr("%s names a repository, so it goes with --layer repo: a --layer %s write is the whole project's (`fugaro models use %s %s --layer %s`)", repo, layer, name, id, layer)
	}
	target, err := settingsResolveTarget(ctx, row, opts, lc, sel)
	if err != nil {
		return err
	}
	old, err := settingsReadDocument(target)
	if err != nil {
		return err
	}
	before, launch, err := modelsUseParse(lc, target, old)
	if err != nil {
		return err
	}
	table, _, err := priceTable(lc, budgetNow())
	if err != nil {
		return err
	}
	scope := modelsUseScope{repo: repo}
	if target.kind == settingsDocLayer {
		scope.repo, scope.repos = "", slices.Sorted(maps.Keys(lc.Repos))
	} else {
		scope.repos = []string{repo}
	}
	if err := modelsUseCheck(lc, launch, target, scope, kind, base, id, table); err != nil {
		return err
	}

	next, err := modelsUseEdit(old, target, kind, base, id)
	if err != nil {
		return err
	}
	if !yamledit.Changed(old, next) {
		fmt.Fprintf(cmd.OutOrStdout(), "%s: %s already reads %s\n", target.path, base, config.CodeSpan(id))
		return nil
	}
	// The role the write needs, asked once the document has answered that there
	// is a write to make: the layer the write lands in is the surface, so a
	// project-layer write is the project's own guardrails and takes admin at
	// project scope, while a repository's file takes admin at a scope covering
	// it (settingsAccessWrite, finding F19). It stands where `settings set`
	// asks it, before the refusals that read the edited document, so the answer
	// a caller without the role gets is about the role and not about the value.
	command := modelsUseCommand(target, scope, base, id)
	if err := settingsAccessWrite(ctx, lc, opts, target, command); err != nil {
		return err
	}
	if err := settingsReparse(target, next); err != nil {
		return err
	}
	// Only a repository's own file is held against validate's rules: the layer
	// file is no fugaro.yaml, and its own key is judged by modelsUseCheck.
	if before != nil {
		after, _, err := modelsUseParse(lc, target, next)
		if err != nil {
			return err
		}
		if err := modelsUseJudge(ctx, lc, before, after, target.path, repo, base, id); err != nil {
			return err
		}
	}
	edit := settingsEdit{
		Path:    target.path,
		Old:     old,
		New:     next,
		What:    modelsUseWhat(target, scope, kind, base, id),
		Kind:    target.kind,
		Command: command,
	}
	// layer.claude_models is a widening (D2617): it hands a route the ability to
	// serve a Claude model to every repository that route lists, which is the
	// one direction a layer write may take that no repository's own file can
	// take back. Typed is what makes the write path refuse it in a coding
	// agent's session whatever the gate says, and the sentence beside it is what
	// a person reads instead of the prompt that used to be asked (D1540).
	if kind == modelsUseClaim {
		edit.Typed = true
		edit.TypedWhy = fmt.Sprintf("claude_models.%s widens what route %s may serve: every repository the route lists may run a Claude model on the project's account, which is what the accounts and the routes bound", modelsUseClaimProvider(id), modelsUseClaimProvider(id))
	}
	if err := settingsLayerGate(cmd, target, settingsWriteOptions{layer: layer}, row.Key, old, next); err != nil {
		return err
	}
	if err := applySettingsEdit(cmd, lc, edit); err != nil {
		return err
	}
	if target.kind == settingsDocLayer {
		// The write and the publish are one act for a person (D2602): a layer
		// key is served from the published layer, so the stages that publish it
		// run here, in process, before the command returns. What those stages
		// are is the hook's own reading of the store, and the store is the one
		// this write landed in — never the row's (contract §3).
		return runAfterWrite(ctx, cmd, afterWriteChange{Key: row.Key, Store: settings.StoreProject})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "the change is in %s's working tree and nowhere else: review it, commit it on your own branch and merge that to the base branch — every run reads the base branch, and this command never commits or pushes\n", oneLine(repo))
	return nil
}

// modelsUseRepo is the repository a --layer repo write is for: the argument, or
// the origin of the checkout the command runs in when none is given. A machine
// with neither has nowhere to put the write, which is a refusal and not an edit
// somewhere else.
func modelsUseRepo(repo, name, id string) (string, error) {
	if repo == "" {
		if _, here := checkoutOriginAt(""); here != "" {
			repo = here
		}
	}
	if repo == "" {
		return "", userErr("a --layer repo write names the repository: `fugaro models use owner/name %s %s`, or run it from that repository's checkout", name, id)
	}
	if !settingsRepoRE.MatchString(repo) {
		return "", userErr("repository %s: a repository is owner/name", oneLine(repo))
	}
	return repo, nil
}

// modelsUseKind is what the value written to a key is.
type modelsUseKind int

const (
	// modelsUseModel is a model ID, the shape every agent.models.<role> and
	// every agent.levels.<level>.model holds.
	modelsUseModel modelsUseKind = iota
	// modelsUseRecipe is a recipe name, the shape agent.recipe holds.
	modelsUseRecipe
	// modelsUseEffort is a reasoning effort, one of config.Efforts.
	modelsUseEffort
	// modelsUseClaim is one entry of the layer's claude_models block, spelled
	// provider=anthropic/<id>.
	modelsUseClaim
)

// modelsUseBase is the key as a repository's own fugaro.yaml spells it, and
// what the value written to it is. false: a name this command does not write,
// which is a refusal rather than an edit of a key something else owns.
//
// A role name is read off the registry rather than restated, so a fifth slot
// lands here with its row and a name no row answers for is refused. The
// project layer's own spelling of the same key is accepted as written, because
// it is the spelling a refusal prints and the one the writer registry holds.
func modelsUseBase(name string) (string, modelsUseKind, bool) {
	switch name {
	case "claude_models", "layer.claude_models":
		return "layer.claude_models", modelsUseClaim, true
	}
	if !strings.Contains(name, ".") {
		if _, ok := settings.Lookup("agent.models." + name); ok {
			return "agent.models." + name, modelsUseModel, true
		}
		return "", 0, false
	}
	key := name
	if strings.HasPrefix(key, "layer.") {
		if !strings.HasPrefix(key, "layer.defaults.agent.") {
			return "", 0, false
		}
		key = strings.TrimPrefix(key, "layer.defaults.")
	}
	switch {
	case key == "agent.model":
		return key, modelsUseModel, true
	case key == "agent.recipe":
		return key, modelsUseRecipe, true
	case settingsPatternMatch("agent.models.*", key):
		return key, modelsUseModel, true
	case settingsPatternMatch("agent.levels.*.model", key):
		return key, modelsUseModel, true
	case settingsPatternMatch("agent.levels.*.effort", key):
		return key, modelsUseEffort, true
	}
	return "", 0, false
}

// modelsUseKeyNames is the keys `models use` writes besides the roles, in the
// repository's own spelling, for the refusal that names what a person could
// have asked for. The level keys are spelled by their pattern's own shape, so
// the list does not grow with a level the registry gains.
func modelsUseKeyNames() []string {
	return []string{"agent.model", "agent.recipe", "agent.levels.<l1|l2|l3>.model", "agent.levels.<l1|l2|l3>.effort"}
}

// modelsUseScope is whose run a write is for: the one repository a --layer repo
// write lands in, or every repository the project onboards, for a --layer
// project one. The two are the same judgement over a different noun — the
// route has to open to this set — and a refusal names the members it misses.
type modelsUseScope struct {
	repo  string
	repos []string
}

// reach is the half of a refusal that says whose run the write is for. The two
// sentences it completes are otherwise the same, and a project-layer one has to
// say that it is not one repository's but every repository's — a default a
// project sets is what the repositories that name nothing of their own run with.
func (s modelsUseScope) reach() string {
	if s.repo != "" {
		return "no run of " + config.CodeSpan(s.repo) + " could"
	}
	return "no run of this project could"
}

// modelsUseLaunch is what the launch's own rules read a write against: the
// agent block the document gives a run, the budget mode that run starts with,
// and the document itself. They travel together because they are one reading of
// one document, and because the project layer answers the first two from a
// block of its own.
type modelsUseLaunch struct {
	agent config.Agent
	mode  string
	// doc is that document as a configuration, for the rules that read more of
	// it than the agent block: the repository's own parsed file, or the project
	// layer's own, whose defaults.rigor is the mode its review levels resolve
	// with and whose project is the one whose floor applies. A layer file is no
	// fugaro.yaml, so the one the level rules read is built here, and the block
	// they judge is the write's own (modelsUseAgent) rather than this copy's.
	doc *config.Config
}

// modelsUseParse reads the document a write is about, in the reader that
// document has, and answers with the launch that document gives.
//
// A repository's own file is its agent: block, and the mode the project's
// ceiling merged with its own budget decides. The project layer has no
// config.Config — no workflows, no budget, no repository — and the agent block
// it gives a run is its defaults.agent: that is the block a repository naming
// no agent of its own runs with, which is why the checks read it here. A
// project-layer default is written once and inherited by every repository that
// names nothing, so a default no launch would take is refused at the write
// rather than at the launch of each of them. Its mode is the ceiling alone for
// the same reason: the layer carries no budget, and a repository that names
// none starts on the ceiling.
//
// The *config.Config is the repository's own file, nil for the layer: the
// rules modelsUseJudge holds a file against read a whole fugaro.yaml, and the
// layer file is not one. The launch's own doc is that same file for a
// repository, and the layer built above for a layer: a rule that judges a key
// of either document reads it there.
func modelsUseParse(lc *localcfg.Config, target settingsTarget, data []byte) (*config.Config, modelsUseLaunch, error) {
	if target.kind == settingsDocLayer {
		l, ps := config.ParseProjectLayer(data, target.anchor)
		if len(ps) > 0 {
			return nil, modelsUseLaunch{}, userErr("%s: %s", target.path, ps[0].String())
		}
		agent, err := modelsUseLayerAgent(l)
		if err != nil {
			return nil, modelsUseLaunch{}, err
		}
		return nil, modelsUseLaunch{
			agent: agent,
			mode:  policy.Merge(ceilingLayer(lc)).Mode,
			doc:   &config.Config{Layer: l, Project: lc.Name, Agent: agent},
		}, nil
	}
	cfg, ps := config.Parse(data)
	if len(ps) > 0 {
		return nil, modelsUseLaunch{}, userErr("%s: %s", target.path, ps[0].String())
	}
	return cfg, modelsUseLaunch{agent: cfg.Agent, mode: policy.Merge(ceilingLayer(lc), runner.FileLayer(cfg)).Mode, doc: cfg}, nil
}

// modelsUseLayerAgent is the agent block the project layer gives a repository
// that names none of its own: defaults.agent, decoded through the same strict
// reader the resolver decodes the merged tree with, so the checks below read
// the block a run reads rather than a second reading of it that could drift
// from the first.
//
// Auth carries the default the rest of Fugaro applies to a block that names
// none (config.DefaultAgentAuth), which is what a repository file naming no
// auth reads as: of everything applyDefaults fills in, it is the one field the
// two checks below read.
func modelsUseLayerAgent(l *config.ProjectLayer) (config.Agent, error) {
	data, err := yaml.Marshal(l.Defaults.Agent)
	if err != nil {
		return config.Agent{}, userErr("the project layer's defaults.agent: %v", err)
	}
	var agent config.Agent
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&agent); err != nil {
		return config.Agent{}, userErr("the project layer's defaults.agent: %v", err)
	}
	if agent.Auth == "" {
		agent.Auth = config.DefaultAgentAuth
	}
	return agent, nil
}

// modelsUseEdit is the one key written: the value at the path the resolver
// named, or one entry added to the layer's claude_models block.
func modelsUseEdit(old []byte, target settingsTarget, kind modelsUseKind, base, id string) ([]byte, error) {
	if kind == modelsUseClaim {
		provider, entry, err := modelsUseClaimParts(id)
		if err != nil {
			return nil, err
		}
		next, err := yamledit.AddListItem(old, append(slices.Clone(target.keyPath), provider), entry)
		if err != nil {
			if err, ok := modelsSpliceRefusal(target.path, err); ok {
				return nil, err
			}
			return nil, userErr("%s: %v", base, err)
		}
		return next, nil
	}
	next, err := yamledit.SetScalar(old, target.keyPath, id)
	if err != nil {
		if err, ok := modelsSpliceRefusal(target.path, err); ok {
			return nil, err
		}
		return nil, userErr("%s: %v", base, err)
	}
	return next, nil
}

// modelsUseClaimParts splits the one spelling a claude_models entry is written
// with: the route's name, an `=`, and the explicit anthropic/<id> the route
// gains. The two halves are separate fields of the block — the name is the map
// key and the ID is the item — so a value that carries one and not the other is
// a value no edit can place.
func modelsUseClaimParts(value string) (provider, entry string, err error) {
	provider, entry, ok := strings.Cut(value, "=")
	if !ok || provider == "" || entry == "" {
		return "", "", userErr("%s: write a claude_models entry as provider=anthropic/<id>, the route's name and the model it gains (for example openrouter=anthropic/claude-sonnet-5-5)", config.CodeSpan(value))
	}
	return provider, entry, nil
}

// modelsUseClaimProvider is the route's name in a claim's value, for the
// sentences written before the value is validated.
func modelsUseClaimProvider(value string) string {
	provider, _, _ := strings.Cut(value, "=")
	return provider
}

// modelsUseCheck is the launch's own judgement of one write, made before the
// file changes rather than at the next run (spec §6). The order is the order a
// reader can act in — the fix that has to happen first is the one named.
func modelsUseCheck(lc *localcfg.Config, launch modelsUseLaunch, target settingsTarget, scope modelsUseScope, kind modelsUseKind, base, id string, table *pricing.Table) error {
	switch kind {
	case modelsUseEffort:
		if !config.ValidEffort(id) {
			return userErr("%s: %s is not a reasoning effort; one of %s", base, config.CodeSpan(id), strings.Join(config.Efforts, ", "))
		}
	case modelsUseRecipe:
		if !recipe.NameRE.MatchString(id) {
			return userErr("%s: %s is not a recipe name, which is 1 to 40 of a-z, 0-9 and '-': name a recipe of .fugaro/recipes/, of the project's, or one of the catalog's", base, config.CodeSpan(id))
		}
	case modelsUseClaim:
		if err := modelsUseClaimCheck(lc, id); err != nil {
			return err
		}
	default:
		if err := modelsUseModelCheck(lc, launch, target, scope, base, id, table); err != nil {
			return err
		}
	}
	return modelsUseLevelCheck(lc, launch, target, base, id)
}

// modelsUseLevelCheck is the review levels the document gives a run, judged over
// the agent block as this write leaves it (D725): a level whose model a route
// serves and whose effort is set is refused at every launch — a provider stage
// runs with thinking off, so the effort would look applied and do nothing — and
// the effort is a key this command writes. The levels are resolved by
// config.ResolveLevels through validate's own reviewLevelProblems, so the two
// surfaces cannot disagree about a level (L3.1), and it runs at both layers: a
// repository's own file, and the project layer's defaults.agent, which is the
// block every repository naming none of its own runs with.
//
// Only a level this write changes is a refusal, exactly as in modelsUseJudge: a
// document that already refuses is its owner's to fix, and a switch that has
// nothing to do with that problem refusing on its behalf would be this command's
// second, wrong verdict on the same document.
func modelsUseLevelCheck(lc *localcfg.Config, launch modelsUseLaunch, target settingsTarget, base, id string) error {
	was := map[string]bool{}
	for _, p := range modelsUseLevelProblems(lc, launch, target, launch.agent) {
		was[p.String()] = true
	}
	for _, p := range modelsUseLevelProblems(lc, launch, target, modelsUseAgent(launch.agent, base, id)) {
		if was[p.String()] {
			continue
		}
		return userErr("%s would not launch with %s pinned for %s: %s", target.path, config.CodeSpan(id), base, p.String())
	}
	return nil
}

// modelsUseLevelProblems is one agent block as the level rules find it, over the
// document the launch reads: a repository's own parsed file, or the project
// layer's own, whose defaults.rigor is the mode its levels resolve with and
// whose project is the one whose floor applies. The document is copied rather
// than written into, because the caller's own reading of it — the pre-edit one
// included — is what the diff above and modelsUseJudge read.
func modelsUseLevelProblems(lc *localcfg.Config, launch modelsUseLaunch, target settingsTarget, agent config.Agent) []config.Problem {
	cfg := *launch.doc
	cfg.Agent = agent
	problems, _ := reviewLevelProblems(filepath.Dir(target.path), &cfg, lc, nil, lc.Providers, nil)
	return problems
}

// modelsUseClaimCheck judges one claude_models entry: the route it names is one
// the project defines — a layer adds a Claude model to a route the owner has
// already opened and never creates one (config.ApplyClaudeClaims, P8) — and the
// entry is one explicit anthropic/<id> that route may carry.
func modelsUseClaimCheck(lc *localcfg.Config, value string) error {
	provider, entry, err := modelsUseClaimParts(value)
	if err != nil {
		return err
	}
	if _, known := lc.Providers[provider]; !known {
		names := slices.Sorted(maps.Keys(lc.Providers))
		instead := "this project defines no route yet"
		if len(names) > 0 {
			instead = "this project defines " + strings.Join(names, ", ")
		}
		return userErr("claude_models.%s: the route %s is not defined here — %s. No command creates a route: providers.%s is a hand edit of the project config (base_url, auth, secret and allow_data_to are the owner's), and `fugaro models add` adds a model to a route that exists and refuses a Claude ID (D785); open the route by hand and run this again; nothing was written",
			config.CodeSpan(provider), config.CodeSpan(provider), instead, provider)
	}
	if msg := config.CheckClaudeClaim(entry); msg != "" {
		return userErr("claude_models.%s: %s", config.CodeSpan(provider), msg)
	}
	if msg := config.CheckModelPattern(entry); msg != "" {
		return userErr("claude_models.%s: %s", config.CodeSpan(provider), msg)
	}
	return nil
}

// modelsUseModelCheck is the judgement of one model pin: the model is claimed
// by a route, that route opens to every repository the write serves, the model
// is priced, the allow-list admits it, and the agent block the document gives a
// run supports a provider model — a repository's own block, or the project
// layer's defaults.agent, which is what a repository naming none of its own
// runs with.
func modelsUseModelCheck(lc *localcfg.Config, launch modelsUseLaunch, target settingsTarget, scope modelsUseScope, base, id string, table *pricing.Table) error {
	if err := modelsUseRefuseID(id); err != nil {
		return err
	}
	if err := modelsUseTableCheck(lc, scope, id); err != nil {
		return err
	}
	name, p, claimed := config.ProviderFor(lc.Providers, id)
	// A Claude model reaches Anthropic with no route at all, in either spelling
	// a pin may carry (isClaudeModel), so the missing-route refusal is only for
	// a vendor/model ID a route has not opened: telling the owner to open a
	// route for a Claude ID would name a fix that does not exist (spec §6, §8).
	claude := isClaudeModel(id)
	if !claimed && !claude && strings.Contains(id, "/") {
		return userErr("no route of the project config serves %s, so %s reach it: the owner opens one with `fugaro models add %s`, and a repository names a model the owner has opened and nothing more", config.CodeSpan(id), scope.reach(), id)
	}
	if claimed {
		missing := []string{}
		for _, repo := range scope.repos {
			if !p.AllowsData(repo) {
				missing = append(missing, repo)
			}
		}
		if len(missing) > 0 {
			if scope.repo == "" {
				return userErr("%s is served by route %s, whose allow_data_to does not name every repository this project onboards: it misses %s. A project-layer default is what every repository of the project runs with, so a run of one the route does not list is refused at the launch — add the missing repositories to providers.%s.allow_data_to in the project config, or set the key in the repositories that may use it; nothing was written",
					config.CodeSpan(id), name, strings.Join(missing, ", "), name)
			}
			return userErr("%s is served by route %s, whose allow_data_to does not name %s: the owner adds the repository to providers.%s.allow_data_to in the project config before its code may be sent there", config.CodeSpan(id), name, config.CodeSpan(scope.repo), name)
		}
	}
	if _, priced := table.Lookup(id); !priced {
		why := table.Refusal(id)
		if why == "" {
			why = "the built-in price catalog does not price it for this route"
		}
		switch {
		case claimed:
			return userErr("%s has no price a run can use: %s. The owner gives it one with `fugaro models add %s --price-in <dollars per million tokens> --price-out <dollars per million tokens>`, which writes a model_prices entry in the project config", config.CodeSpan(id), why, id)
		case claude:
			return userErr("%s is a Claude model, which goes to Anthropic with no route, but the price table has no row for that spelling: %s. Pin the ID the table prices, such as claude-sonnet-5-5, or have a route claim and price this spelling", config.CodeSpan(id), why)
		default:
			return userErr("%s has no price a run can use: %s. The owner prices it in the project config before the repository may pin it", config.CodeSpan(id), why)
		}
	}
	if lc.Budget != nil && len(lc.Budget.AllowedModels) > 0 && !slices.Contains(lc.Budget.AllowedModels, id) {
		return userErr("budget.allowed_models of the project config does not name %s, so %s use it: the owner names it there, or adds the model with `fugaro models add %s`", config.CodeSpan(id), scope.reach(), id)
	}
	agent := modelsUseAgent(launch.agent, base, id)
	if ps := config.CheckProviderAuth(agent, lc.Providers); len(ps) > 0 {
		return userErr("%s would not launch with %s pinned for %s: %s", target.path, config.CodeSpan(id), base, ps[0].String())
	}
	if ps := config.CheckProviderGateway(agent, config.EffectiveBackground(agent, lc.Providers), lc.Providers, launch.mode); len(ps) > 0 {
		return userErr("%s would not launch with %s pinned for %s: %s", target.path, config.CodeSpan(id), base, ps[0].String())
	}
	return nil
}

// modelsUseTableCheck is the project's routing table's own answer about one pin:
// the model must be one the resolver reaches, because a model no entry of the
// table matches is one no run can send (R1, R2, R22) and a pattern written twice
// is the resolver's own refusal (D3172). The sentence is RT3's, so it names the
// fix `fugaro routing set` writes, and it is the same one `fugaro validate`
// prints and the run's own gate refuses at (chainRoutingGate makes the same
// call).
//
// Only the model's own answer is refused here. The chain problems of an entry —
// an empty chain, a source naming a pool the project does not declare — are the
// table's and are reported once, by validate, at the entry that carries them;
// every model that entry matches meets them, and refusing a pin over one would
// print the same fix from a second place.
//
// A table that does not govern the runs this write is for is not asked either:
// the write's repositories are the scope's own (scope.repos, the set the route
// checks above are made over), and where no source of the table admits one of
// them its models are reached the release-1 way (D3170, O7, R25) — which is what
// the rest of modelsUseModelCheck judges.
func modelsUseTableCheck(lc *localcfg.Config, scope modelsUseScope, id string) error {
	problems, _ := modelRoutingProblems(lc, scope.repos, []string{id})
	for _, p := range problems {
		if p.Code == routing.CodeNoEntry || p.Code == routing.CodeTwinPattern {
			return userErr("model_routing: %s", oneLine(p.Message))
		}
	}
	return nil
}

// modelsUseAgent is the agent block the checks are asked about: a copy with the
// one key's field set to the pin, so the launch's own rules see the file as it
// would read after the edit and not as it reads now.
func modelsUseAgent(agent config.Agent, base, id string) config.Agent {
	switch {
	case base == "agent.model":
		agent.Model = id
	case strings.HasPrefix(base, "agent.models."):
		switch strings.TrimPrefix(base, "agent.models.") {
		case modelsRoleBackground:
			agent.Models.Background = id
		case modelsRoleVision:
			agent.Models.Vision = id
		case "diagnose":
			agent.Models.Diagnose = id
		case string(config.RoleReviewer):
			agent.Models.Reviewer = id
		default:
			agent.Models.Coder = id
		}
	case strings.HasPrefix(base, "agent.levels."):
		parts := strings.Split(base, ".")
		level := parts[2]
		set := func(l *config.LevelSetting) *config.LevelSetting {
			if l == nil {
				return &config.LevelSetting{}
			}
			copied := *l
			return &copied
		}
		switch level {
		case "l1":
			agent.Levels.L1 = set(agent.Levels.L1)
			modelsUseLevelField(agent.Levels.L1, parts[3], id)
		case "l2":
			agent.Levels.L2 = set(agent.Levels.L2)
			modelsUseLevelField(agent.Levels.L2, parts[3], id)
		case "l3":
			agent.Levels.L3 = set(agent.Levels.L3)
			modelsUseLevelField(agent.Levels.L3, parts[3], id)
		}
	}
	return agent
}

// modelsUseLevelField writes one level's own field.
func modelsUseLevelField(l *config.LevelSetting, field, id string) {
	if field == "effort" {
		l.Effort = id
		return
	}
	l.Model = id
}

// modelsUseRefuseID refuses what no pin may carry: a ":" variant and an alias
// are shapes `fugaro validate` refuses in agent.models, and a file written with
// one would refuse every launch of that repository.
func modelsUseRefuseID(id string) error {
	if strings.Contains(id, ":") {
		return modelsRefuseVariant(id)
	}
	if why := config.CheckModelID(id); why != "" {
		return userErr("%s: name a model ID a run may pin", why)
	}
	return nil
}

// modelsUseWhat is the one sentence a write is announced with: what changed,
// and in whose file.
func modelsUseWhat(target settingsTarget, scope modelsUseScope, kind modelsUseKind, base, id string) string {
	if kind == modelsUseClaim {
		provider, entry, _ := modelsUseClaimParts(id)
		return fmt.Sprintf("add %s to claude_models.%s of the project layer", config.CodeSpan(entry), config.CodeSpan(provider))
	}
	if scope.repo == "" {
		return fmt.Sprintf("set the project layer's %s to %s", base, config.CodeSpan(id))
	}
	return fmt.Sprintf("point %s's %s at %s", scope.repo, base, config.CodeSpan(id))
}

// modelsUseCommand is the command line that makes the same write: what the
// refusal a coding agent's session reads names as the thing to run at the
// owner's own terminal.
func modelsUseCommand(target settingsTarget, scope modelsUseScope, base, id string) string {
	if scope.repo == "" {
		return fmt.Sprintf("fugaro models use %s %s --layer project", base, id)
	}
	return fmt.Sprintf("fugaro models use %s %s %s", scope.repo, base, id)
}

// modelsUseJudge holds the edited file against the rules `fugaro validate`
// reports, so the pin this command prepares is one a launch takes whatever those
// rules learn later. Reading them through the same functions is the difference
// between judging a pin and re-stating some of its rules: the file's own
// budget.allowed_models — the tighter of the two lists, and the one a launch
// merges — was the rule the checks here re-stated by omission, and only the
// rules themselves can say how many more there are.
//
// Only a problem the edit introduces is a refusal. A file that already fails
// validate is its owner's to fix, and a switch that has nothing to do with that
// problem refusing on its behalf would be this command's second, wrong verdict
// on the same file; a problem the edit removes is an improvement.
//
// The rules read the repository from the checkout the command runs in, exactly
// as validate reads the file in hand. For a remembered checkout elsewhere that
// is not this file's repository, so their allow_data_to lines are dropped rather
// than trusted: modelsUseModelCheck has already ruled on allow_data_to for the
// repository this file belongs to, and a line about another checkout's
// repository is a refusal no launch of this file would make (providerProblems
// drops the same lines for its own "the repository is unknown here" state).
func modelsUseJudge(ctx context.Context, lc *localcfg.Config, before, after *config.Config, path, repo, base, id string) error {
	was := map[string]bool{}
	for _, p := range modelsUseProblems(ctx, lc, before, path) {
		was[p.String()] = true
	}
	_, here := checkoutOriginAt("")
	for _, p := range modelsUseProblems(ctx, lc, after, path) {
		if was[p.String()] {
			continue
		}
		if !strings.EqualFold(here, repo) && strings.Contains(p.Message, "allow_data_to") {
			continue
		}
		return userErr("%s would not launch with %s pinned for %s: %s", path, config.CodeSpan(id), base, p.String())
	}
	return nil
}

// modelsUseProblems is one file as validate's own rules find it: the budget,
// provider and routing rules, over the same tables validate reads — the owner's
// own routes, with no layer merged, which is what `models use` edits — and with
// the roles the effective recipe leaves. The project config's own error is nil
// here: this command resolved it, or it refused before reading the file.
func modelsUseProblems(ctx context.Context, lc *localcfg.Config, cfg *config.Config, path string) []config.Problem {
	ecfg := withRecipeRoles(filepath.Dir(path), cfg)
	claimed := lc.Providers
	problems, _, _ := budgetProblems(ctx, ecfg, lc, claimed)
	pp, _, _ := providerProblems(ctx, ecfg, lc, claimed)
	rp, _ := routingProblems(ctx, ecfg, lc, claimed, nil)
	return append(append(problems, pp...), rp...)
}
