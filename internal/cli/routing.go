package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/craft"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/routing"
)

// `fugaro routing` is the project's table, read and edited (RT5 of
// docs/model-credentials-routing/implementation-plan.md, D3172, D3182).
//
// The table is the project's own key in the installation's config
// (localcfg.Config.ModelRouting, RT2), and this family is the owner's hand over
// it after the migration wrote the first one (MG2): `ls` answers which entry a
// model a craft uses goes out on and why, `set` writes an entry and `rm` takes
// one out.
//
// **What refuses, and where the rule lives.** The checks are RT3's
// (routing.Problems) and this file asks them rather than judging a table
// itself: a write that would route a model a run can call to nothing, a chain of
// only the launcher's pool, a source naming a pool the project does not declare,
// a source that cannot reach the model — every one of them is the answer of that
// one function, and a second reading of the rules here would be the drift the
// table exists to prevent. What this file owns is the arguments, the block's
// write and the rendering, and nothing else.
//
// **Why the writes are local config and not the policy.** The table reaches a
// run only through the published policy (RT4), which `fugaro update` publishes;
// that is the migration's subject and not this command's, so `set` writes the
// file and says nothing about the cloud. A write here is refused in a coding
// agent's session exactly as a cloud write is, by the one fence every local
// config write goes through (applySettingsEdit, SD9), because the file decides
// how every run of the project spends.

func newRoutingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "routing",
		Short: "Show and edit the project's model routing table",
		Long: `Show and edit where this project's models are reached.

The table is one list of entries. Each entry matches a model or a pattern
(` + "`claude-*`" + `, an exact ID) and names an ordered chain of credential sources:
the launcher's pool, a pool the project declares, the project's Anthropic key, a
provider key or Vertex. A call goes out on the first usable source of the chain,
and a model no entry matches is refused rather than served by whatever
credential is around.

The table is this project's: it lives in this machine's own config, under the
` + "`model_routing:`" + ` key, and a repository's ` + "`fugaro.yaml`" + ` has no such key.

` + "`fugaro routing ls`" + ` prints the table and, for every model a craft of this
project runs, the entry that wins and why. ` + "`fugaro routing set`" + ` writes one
entry — replacing the entry with the same pattern, where it stands — and
` + "`fugaro routing rm`" + ` takes one out. Both write nothing while the table they
would leave is one the checks refuse, and every refusal names the fix.

` + "`ls`" + ` is offline: it reads this machine's files and reaches no bucket, no
database and no host.`,
	}
	cmd.AddCommand(newRoutingLsCmd(), newRoutingSetCmd(), newRoutingRmCmd())
	return cmd
}

// routingLsDocVersion is the document's first version, and the number a member
// added later keeps: schemas/routing-ls.schema.json pins it.
const routingLsDocVersion = 1

type routingLsOptions struct {
	cloud cloudOptions
	json  bool
}

// newRoutingLsCmd is `fugaro routing ls`: the read.
func newRoutingLsCmd() *cobra.Command {
	var o routingLsOptions
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "Show the routing table, and which entry each model a craft uses goes out on",
		Long: `Show this project's routing table, and which entry wins for each model.

The table is printed entry by entry, in the order the project wrote it, with each
entry's chain spelled the way ` + "`fugaro routing set`" + ` takes it.

Below it is one row per model a run of this project can be served on — the five
crafts' resolved models, the four models ` + "`agent.models`" + ` pins and each review
level's ` + "`agent.levels.<level>.model`" + ` the checkout writes — with the entry that
answers it and why that entry won: the only entry that matches, the longest of
several patterns, or an exact ID over the patterns that also match. A model no
entry matches is a run that cannot start, and the row says so.

The read is offline: it reaches no bucket, no database and no host.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runRoutingLs(cmd, &o) },
	}
	cmd.Flags().BoolVar(&o.json, "json", false, "print the versioned document")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

// routingLsDoc is what `fugaro routing ls --json` prints, under
// schemas/routing-ls.schema.json. A member added later is a new name, and a
// member whose meaning changes bumps the version (D784, CLAUDE.md).
type routingLsDoc struct {
	Version int `json:"version"`
	// Project is the selected project's name; absent when no project config is
	// selected, which leaves Entries and Models empty.
	Project string `json:"project,omitempty"`
	// Entries is the table, in the order the project wrote it.
	Entries []routingLsEntry `json:"entries"`
	// Models is one row per model a run can be served on, in the order the
	// crafts resolve.
	Models []routingLsModel `json:"models"`
}

// routingLsEntry is one row of the table: the pattern and its chain.
type routingLsEntry struct {
	Match string `json:"match"`
	// Chain is the sources, first usable one wins, each spelled the way
	// `fugaro routing set` takes it.
	Chain []string `json:"chain"`
	// Sources is the same chain in the table's own members, so a reader that
	// has to tell a pool from a provider reads them apart rather than parsing
	// the spelling above.
	Sources []routingLsSource `json:"sources"`
}

// routingLsSource is one source with its members, and the spelling that
// round-trips through `fugaro routing set`.
type routingLsSource struct {
	Source   string   `json:"source"`
	Kind     string   `json:"kind"`
	Pool     string   `json:"pool,omitempty"`
	Provider string   `json:"provider,omitempty"`
	Model    string   `json:"model,omitempty"`
	Admit    []string `json:"admit,omitempty"`
}

// routingLsModel is one model a run can be served on, the entry that answers it
// and why.
type routingLsModel struct {
	Model string `json:"model"`
	// Crafts are the crafts that run this model; empty for a model only a pin
	// or a review level names.
	Crafts []string `json:"crafts"`
	// Entry is the pattern of the entry that wins, and absent when no entry
	// matches — the model a run cannot start with.
	Entry string `json:"entry,omitempty"`
	// Why names why that entry won, or why none did.
	Why string `json:"why"`
}

// runRoutingLs is the read: it assembles the layers, asks the resolver and
// renders the answer.
func runRoutingLs(cmd *cobra.Command, o *routingLsOptions) error {
	lc, err := craftLsConfig(cmd, o.cloud)
	if err != nil {
		return err
	}
	// Both arrays are written on every run, empty included, so a consumer reads
	// one without a nil check (schemas/routing-ls.schema.json requires them).
	doc := routingLsDoc{Version: routingLsDocVersion, Entries: []routingLsEntry{}, Models: []routingLsModel{}}
	if lc == nil {
		if o.json {
			return writeJSON(cmd.OutOrStdout(), doc)
		}
		return writeRoutingLs(cmd, doc)
	}
	doc.Project = lc.Name
	for _, e := range lc.ModelRouting {
		row := routingLsEntry{Match: e.Match, Chain: []string{}, Sources: []routingLsSource{}}
		for _, s := range e.Chain {
			row.Chain = append(row.Chain, routingSourceString(s))
			row.Sources = append(row.Sources, routingLsSource{
				Source: routingSourceString(s), Kind: string(s.Kind),
				Pool: s.Pool, Provider: s.Provider, Model: s.Model, Admit: s.Admit,
			})
		}
		doc.Entries = append(doc.Entries, row)
	}
	for _, m := range routingModels(cmd, lc) {
		entry, why := routingWinner(lc.ModelRouting, m.model)
		crafts := m.crafts
		if crafts == nil {
			crafts = []string{}
		}
		doc.Models = append(doc.Models, routingLsModel{
			Model: m.model, Crafts: crafts, Entry: entry.Match, Why: why,
		})
	}
	if o.json {
		return writeJSON(cmd.OutOrStdout(), doc)
	}
	return writeRoutingLs(cmd, doc)
}

// writeRoutingLs is the read's text form: the table, then one row per model.
func writeRoutingLs(cmd *cobra.Command, doc routingLsDoc) error {
	w := cmd.OutOrStdout()
	if doc.Project == "" {
		fmt.Fprintln(w, "routing: no project config selected, so there is no table")
		return nil
	}
	fmt.Fprintf(w, "routing for project %s (offline)\n\n", oneLine(doc.Project))
	if len(doc.Entries) == 0 {
		fmt.Fprintln(w, "the project has no table: every model a run can call is refused (write one with `fugaro routing set`)")
	} else {
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "MATCH\tCHAIN")
		for _, e := range doc.Entries {
			fmt.Fprintf(tw, "%s\t%s\n", cellOneLine(e.Match), cellOneLine(strings.Join(e.Chain, " → ")))
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	fmt.Fprintln(w)
	if len(doc.Models) == 0 {
		fmt.Fprintln(w, "no craft, pin or review level of this project names a model, so nothing here is routed")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "MODEL\tCRAFT\tENTRY\tWHY")
	for _, m := range doc.Models {
		entry := m.Entry
		if entry == "" {
			entry = "—"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			cellOneLine(m.Model), cellOneLine(strings.Join(m.Crafts, ",")),
			cellOneLine(entry), cellOneLine(m.Why))
	}
	return tw.Flush()
}

// routingModel is one model a run can be served on, and the crafts that run it.
type routingModel struct {
	model  string
	crafts []string
}

// routingModels is every model a run of this project can be served on, as this
// machine's three documents can name them: the five crafts' resolved models
// (craft.Resolve over this checkout's fugaro.yaml, the installation's config and
// the cached project layer — the same call `fugaro craft ls` makes, so a table
// is never judged against a model list of its own), the four models
// `agent.models` pins (config.Agent.PinnedModels, the models a run under a
// budget charges through the gateway), and each review level's own model. The
// agent those last two are read from is the launch's own resolution —
// config.Resolve, this checkout's file over the layer's defaults.agent, the
// pass craftLsServes reads a craft's model verdict with — never the checkout's
// block alone: a project layer pinning a model, a role or a review level names
// a model every repository of the project runs on, and the table a write is
// refused over has to carry it. Where this directory is no checkout the block
// reads as it does for `fugaro craft ls`, empty.
//
// One list for the whole family: `ls` renders it and `set`/`rm` hand it to the
// checks (routing.Checks.Models), so the table a person reads about and the
// table a write is refused over are the same table about the same models.
//
// The rungs this offline read cannot name are the run's own — a task's own
// grade, a `--model` override, a level a recipe adds — because they are decided
// per launch and not by any file. RT6's `validate` is where a launch's answer is
// judged; this row is what this machine's configuration says.
func routingModels(cmd *cobra.Command, lc *localcfg.Config) []routingModel {
	files := craftLsFiles{lc: lc}
	if data, ok := checkoutFugaroYAML(cmd); ok {
		if agent, ok := config.AgentOf(data); ok {
			files.repo, files.data, files.agent = true, data, agent
		}
	}
	files.layer = modelsLayer(lc.Name)
	eff, _ := craft.Resolve(craftLsLayers(files), craftLsSpread(lc))
	// Read after the layers are assembled: files.agent stays the checkout's own
	// block, which craftLsLayers reads the repo layer's crafts from.
	agent := files.agent
	if files.repo {
		if cfg, _, ps := config.Resolve(files.data, files.layer); len(ps) == 0 {
			agent = cfg.Agent
		}
	}

	var out []routingModel
	add := func(model, craftName string) {
		if model == "" {
			return
		}
		if i := slices.IndexFunc(out, func(m routingModel) bool { return m.model == model }); i >= 0 {
			if craftName != "" && !slices.Contains(out[i].crafts, craftName) {
				out[i].crafts = append(out[i].crafts, craftName)
			}
			return
		}
		m := routingModel{model: model}
		if craftName != "" {
			m.crafts = []string{craftName}
		}
		out = append(out, m)
	}
	for _, name := range config.CraftNames {
		add(eff[name].Model, name)
	}
	for _, model := range agent.PinnedModels() {
		add(model, "")
	}
	for _, l := range []*config.LevelSetting{agent.Levels.L1, agent.Levels.L2, agent.Levels.L3} {
		if l != nil {
			add(l.Model, "")
		}
	}
	return out
}

// routingWinner is the entry a model goes out on and why it won.
//
// The entry is the resolver's answer (RT1) and never this file's: a second
// weighing of the patterns here would be a second answer to D3172. The why is
// this file's, because the resolver answers an entry and not a reason, and it
// counts the entries the model matches to name the rule that decided the call.
func routingWinner(table routing.Table, model string) (routing.Entry, string) {
	entry, err := routing.Resolve(table, model)
	if err != nil {
		if errors.Is(err, routing.ErrNoEntry) {
			return routing.Entry{}, fmt.Sprintf("no entry matches %s, so a run that calls it cannot start (write one with `fugaro routing set %s <source>...`)", model, model)
		}
		// The resolver's other refusal is the twin pattern (D3172): two entries
		// claim this model with one pattern, so neither is the more specific and
		// no entry wins.
		return routing.Entry{}, "two entries claim it with one pattern, so neither is the more specific one"
	}
	matches := 0
	for _, e := range table {
		if config.PatternMatches(e.Match, model) {
			matches++
		}
	}
	switch {
	case matches == 1:
		return entry, "the only entry that matches"
	case strings.HasSuffix(entry.Match, "*"):
		return entry, fmt.Sprintf("the longest of %d patterns that match", matches)
	default:
		return entry, "the exact model ID, over the pattern(s) that also match"
	}
}

// routingSourceString spells a source the way this command's own arguments spell
// it: the kind, the name the kind carries, and `@MODEL` after either for a
// written downgrade. It round-trips through routingParseSource, so what `ls`
// prints is what `set` takes.
func routingSourceString(s routing.Source) string {
	base := string(s.Kind)
	switch s.Kind {
	case routing.KindPool:
		base = "pool:" + s.Pool
	case routing.KindProvider:
		base = "provider:" + s.Provider
	}
	if s.Model != "" {
		base += "@" + s.Model
	}
	return base
}

// routingParseSource is one source of a `routing set` command line: the kind,
// the name it carries, and the model it serves when that is not the model the
// call asked for.
//
// The grammar is the five kinds of routing.Kinds and nothing else, so a source a
// person types and a source the file holds are one shape: `launcher-pool`,
// `pool:NAME`, `anthropic`, `provider:NAME`, `vertex`, and `@MODEL` after any of
// them for a written downgrade, which spec.md requires to be written explicitly
// rather than performed silently.
func routingParseSource(arg string) (routing.Source, error) {
	spec, model, downgraded := strings.Cut(arg, "@")
	if downgraded && model == "" {
		return routing.Source{}, userErr("%s has no model after the %s: write the model this source serves, or drop the %s", config.CodeSpan(arg), config.CodeSpan("@"), config.CodeSpan("@"))
	}
	kind, name, named := strings.Cut(spec, ":")
	switch k := routing.Kind(kind); k {
	case routing.KindLauncherPool, routing.KindAnthropic, routing.KindVertex:
		if named {
			return routing.Source{}, userErr("%s: %s names no pool and no provider, so nothing follows the %s: write it as %s", config.CodeSpan(arg), k, config.CodeSpan(":"), config.CodeSpan(kind))
		}
		return routing.Source{Kind: k, Model: model}, nil
	case routing.KindPool, routing.KindProvider:
		if !named || name == "" {
			return routing.Source{}, userErr("%s names no %s: write %s", config.CodeSpan(arg), kind, config.CodeSpan(kind+":NAME"))
		}
		s := routing.Source{Kind: k, Model: model}
		if k == routing.KindPool {
			s.Pool = name
		} else {
			s.Provider = name
		}
		return s, nil
	}
	return routing.Source{}, userErr("%s is not a credential source: write %s, %s, %s, %s or %s, and %s after any of them for a written downgrade",
		config.CodeSpan(arg),
		config.CodeSpan(string(routing.KindLauncherPool)), config.CodeSpan("pool:NAME"), config.CodeSpan(string(routing.KindAnthropic)),
		config.CodeSpan("provider:NAME"), config.CodeSpan(string(routing.KindVertex)), config.CodeSpan("@MODEL"))
}

func newRoutingSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <pattern> <source>...",
		Short: "Write the routing table entry for a model or pattern",
		Long: `Write one entry of this project's routing table.

PATTERN is the model or the pattern the entry answers: an exact ID, or a prefix
ending in ` + "`*`" + `. The most specific match wins, so ` + "`claude-*`" + ` and an exact
` + "`claude-sonnet-5-5`" + ` can sit beside each other.

Each SOURCE is one credential source of the entry's chain, first usable one wins:

  ` + "`launcher-pool`" + `      the pool the run's launcher designated
  ` + "`pool:NAME`" + `         a pool this project declares
  ` + "`anthropic`" + `          the project's Anthropic key
  ` + "`provider:NAME`" + `     a provider key this project declares, by name
  ` + "`vertex`" + `             the project's Vertex

Add ` + "`@MODEL`" + ` to any of them to write a downgrade explicitly: the source serves
MODEL when the call asked for another one
(` + "`provider:openrouter@claude-sonnet-5`" + `). Nothing is downgraded silently.

The entry replaces the one this pattern already answers, where it stands; a new
pattern is appended to the table. The write is refused — with nothing written —
where the table it would leave is one the checks refuse, and each refusal names
the fix. A source's own ` + "`admit`" + ` list, which says which repositories one of the
project's credentials may serve, is not written here: it is a change of its own.

Only the ` + "`model_routing:`" + ` block of the config is rewritten: every comment and
every other key of the file is kept. The change reaches a run through the
published policy, which ` + "`fugaro update`" + ` publishes.`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error { return runRoutingSet(cmd, args[0], args[1:]) },
	}
	return cmd
}

func runRoutingSet(cmd *cobra.Command, pattern string, args []string) error {
	if strings.TrimSpace(pattern) == "" {
		return userErr("the pattern is empty: write the model or the pattern the entry answers, such as %s", config.CodeSpan("claude-*"))
	}
	chain := make([]routing.Source, 0, len(args))
	for _, arg := range args {
		s, err := routingParseSource(arg)
		if err != nil {
			return err
		}
		chain = append(chain, s)
	}
	what := fmt.Sprintf("set the routing entry %s", config.CodeSpan(pattern))
	// The command a refused write prints is the whole of what a person runs at
	// their own terminal, so it carries the arguments this run was given.
	return routingWrite(cmd, "fugaro routing set "+pattern+" "+strings.Join(args, " "), what, func(table routing.Table) (routing.Table, error) {
		next := make(routing.Table, 0, len(table)+1)
		replaced := false
		for _, e := range table {
			if e.Match == pattern {
				next = append(next, routing.Entry{Match: pattern, Chain: chain})
				replaced = true
				continue
			}
			next = append(next, e)
		}
		if !replaced {
			next = append(next, routing.Entry{Match: pattern, Chain: chain})
		}
		return next, nil
	})
}

func newRoutingRmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm <pattern>",
		Short: "Take one entry out of the routing table",
		Long: `Take one entry out of this project's routing table.

PATTERN is the entry's own match, exactly as ` + "`fugaro routing ls`" + ` prints it: a
model or a prefix ending in ` + "`*`" + `. A pattern the table does not carry is refused
and nothing is written.

The write is refused — with nothing written — where the table it would leave is
one the checks refuse, so removing the last entry that answers a model a run can
call is refused until another entry answers that model, and the refusal says so.

Only the ` + "`model_routing:`" + ` block of the config is rewritten: every comment and
every other key of the file is kept. The change reaches a run through the
published policy, which ` + "`fugaro update`" + ` publishes.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runRoutingRm(cmd, args[0]) },
	}
	return cmd
}

func runRoutingRm(cmd *cobra.Command, pattern string) error {
	if strings.TrimSpace(pattern) == "" {
		return userErr("the pattern is empty: write the match of the entry to remove, exactly as %s prints it", config.CodeSpan("fugaro routing ls"))
	}
	what := fmt.Sprintf("remove the routing entry %s", config.CodeSpan(pattern))
	return routingWrite(cmd, "fugaro routing rm "+pattern, what, func(table routing.Table) (routing.Table, error) {
		next := make(routing.Table, 0, len(table))
		found := false
		for _, e := range table {
			if e.Match == pattern {
				found = true
				continue
			}
			next = append(next, e)
		}
		if !found {
			// A removal that removes nothing is a command that did not do what
			// it was asked, and the table's own sentences are about a table and
			// not about an argument: this is this file's refusal, and it is made
			// before the checks so a pattern the table does not carry is not
			// reported as whatever else the unchanged table happens to break.
			return nil, userErr("the table has no entry matching %s, so there is nothing to remove: run %s to see the patterns it carries", config.CodeSpan(pattern), config.CodeSpan("fugaro routing ls"))
		}
		return next, nil
	})
}

// routingWrite is the one write path of both commands: it reads the selected
// project config, asks the checks about the table the edit leaves, and splices
// the block through the one local-config writer every family uses.
//
// The order is the whole of the fence: the checks come before the bytes, so a
// refused table is a refusal with nothing written, and the write itself
// (applySettingsEdit) is what refuses a coding agent's session — the same
// refusal a cloud write gives, because this file decides how every run of the
// project spends. The block is spliced into the bytes the write lands on, so the
// document is edited as it is now rather than as it was when the command's own
// read began.
func routingWrite(cmd *cobra.Command, command, what string, edit func(routing.Table) (routing.Table, error)) error {
	sel, lc, err := settingsLocalFile("fugaro routing set and fugaro routing rm")
	if err != nil {
		return err
	}
	if err := refuseSharedWrite(sel); err != nil {
		return err
	}
	next, err := edit(lc.ModelRouting)
	if err != nil {
		return err
	}
	problems, warnings := routing.Problems(routingChecks(cmd, lc, next))
	for _, w := range warnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: %s\n", oneLine(w.Message))
	}
	if len(problems) > 0 {
		var b strings.Builder
		for _, p := range problems {
			fmt.Fprintf(&b, "\n  %s: %s", p.Path, oneLine(p.Message))
		}
		return userErr("nothing was written: the table this would leave is refused by %d rule(s):%s", len(problems), b.String())
	}
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: sel.Path})
	if err != nil {
		return err
	}
	written, err := localcfg.SetModelRouting(old, next)
	if err != nil {
		if refusal, ok := modelsSpliceRefusal(sel.Path, err); ok {
			return refusal
		}
		return userErr("%s", err)
	}
	if _, err := localcfg.Parse(written); err != nil {
		return userErr("%s would not read back after the edit: %v", sel.Path, err)
	}
	return applySettingsEdit(cmd, lc, settingsEdit{
		Path: sel.Path, Kind: settingsDocLocal,
		Old: old, New: written,
		What: what, Command: command,
	})
}

// routingChecks is the table judged against the project it belongs to: the
// project's own credentials and pools, its provider entries, its budget and the
// models a run of it can be served on.
//
// Every member is the project's and not a repository's, because the table is the
// project's (contract-table.md). Check50Passed is left false: nothing in this
// release records Check 50 as run (reconciliation O3), and the fail-closed
// answer is the one a table carrying a routed Claude must meet.
func routingChecks(cmd *cobra.Command, lc *localcfg.Config, table routing.Table) routing.Checks {
	in := routing.Checks{
		Table:       table,
		Credentials: lc.Credentials,
		Pools:       lc.Pools,
		Providers:   lc.Providers,
	}
	if lc.Budget != nil {
		in.BudgetMode, in.BudgetPerRunUSD = lc.Budget.Mode, lc.Budget.PerRunUSD
	}
	for _, m := range routingModels(cmd, lc) {
		in.Models = append(in.Models, m.model)
	}
	return in
}
