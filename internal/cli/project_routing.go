package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// `fugaro project routing`: the Claude routing switch of the operator's own
// config, the one key that decides where a run's Claude calls go (spec §5, row
// G8). `via: openrouter` and `via: auto` send the Claude IDs the switch names to
// the provider entry that claims them — a third party — and only the entry's own
// allow_data_to decides which repositories' code may be sent there (D1b). That
// is why this family is a security surface and not only a settings one: a
// mistake here is a data-egress change, and the command refuses the one shape
// the publisher refuses, one step earlier and in its own words.
//
// The publisher's silence rules are the other half, and they are shared rather
// than restated: routingRoutesClaude and routingTurnsTheCascadeOn are the rules
// `fugaro project policy publish` refuses a silent node by (project_cmd.go), and
// the rename-free pair the node-level predicates delegate to.

func newProjectRoutingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "routing",
		Short: "The project's Claude routing switch: where a run's Claude calls go",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newProjectRoutingShowCmd(), newProjectRoutingSetCmd(), newProjectRoutingUnsetCmd())
	return cmd
}

func newProjectRoutingShowCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show [--json]",
		Short: "Show the route this project's Claude calls take",
		Long: "Show the Claude routing switch this project's own config carries.\n\n" +
			"via: cascade keeps every call going to Anthropic under the ID a pin names and\n" +
			"lets the credentials service decide which credential carries it; via:\n" +
			"openrouter and via: auto send the Claude IDs the switch lists to the provider\n" +
			"entry that claims them, and only that entry's allow_data_to decides which\n" +
			"repositories' code may be sent there; via: direct, and no switch at all, route\n" +
			"nothing and every call goes to Anthropic.\n\n" +
			"The read is offline: it opens this machine's project config and nothing else.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runProjectRoutingShow(cmd, jsonOut) },
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print machine-readable output")
	return cmd
}

type projectRoutingSetOptions struct {
	via    string
	models string
	yes    bool
}

func newProjectRoutingSetCmd() *cobra.Command {
	var o projectRoutingSetOptions
	cmd := &cobra.Command{
		Use:   "set claude --via direct|cascade|openrouter|auto [--models ...]",
		Short: "Set the route this project's Claude calls take",
		Long: "Set the Claude routing switch in this project's own config.\n\n" +
			"--models names the Claude IDs the switch carries, comma-separated; a route\n" +
			"(openrouter, auto) needs an explicit list, because the list is the opt-in and\n" +
			"an empty one would opt every Claude model in (D1a/D1b). cascade takes no list\n" +
			"at all — it decides which credential carries a call, never where the call goes\n" +
			"(O3) — and direct reads one as inert, so both drop it: a route that goes away\n" +
			"with it is what setting the switch is.\n\n" +
			"via: cascade keeps every call going to Anthropic and hands the credential\n" +
			"choice to the credentials service: an exhausted shared subscription token is\n" +
			"replaced by the next usable credential, and with credentials.fallback: auto\n" +
			"that reaches the paid tiers.\n\n" +
			"Sending the switch to a route is a data-egress change and is the widening of\n" +
			"this block: a coding agent's session writes it to no document, whatever --yes\n" +
			"says, and a narrowing (direct, cascade, an unset) is the owner's ordinary\n" +
			"write.\n\n" +
			"The change reaches a run through `fugaro update`, which publishes the policy\n" +
			"node every run reads its route from. Only the keys it writes are rewritten:\n" +
			"every comment and every other key of the file is kept.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runProjectRoutingSet(cmd, args[0], &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.via, "via", "", "the route: direct, cascade, openrouter or auto")
	f.StringVar(&o.models, "models", "", "the Claude IDs a route carries, comma-separated (required for openrouter and auto)")
	f.BoolVar(&o.yes, "yes", false, "confirm the write without a prompt")
	return cmd
}

func newProjectRoutingUnsetCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "unset",
		Short: "Remove the routing switch, when it carries nothing",
		Long: "Remove the Claude routing switch from this project's own config.\n\n" +
			"A switch that is already inert is what goes: via: direct routes nothing, so\n" +
			"removing it says what the file already said, and every call goes to Anthropic.\n" +
			"The removal is refused while the switch carries the cascade or a route, and\n" +
			"that refusal is the publisher's own rule about a silent node, made here: a\n" +
			"block that disappears from the file would turn the cascade, or a route, off by\n" +
			"omission on the next publish — the cascade is what hands a run the project's\n" +
			"other credentials, and a route that goes away is a Claude call that stops being\n" +
			"sent where it was put. Write the switch off on purpose instead\n" +
			"(`fugaro project routing set claude --via direct`), which keeps the block and\n" +
			"says what it means.\n\n" +
			"The change reaches a run through `fugaro update`. Only the key it removes is\n" +
			"rewritten: every comment and every other key of the file is kept.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runProjectRoutingUnset(cmd, yes) },
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm the write without a prompt")
	return cmd
}

// projectRoutingDoc is `project routing show --json`: the route a run of this
// project would take, as the read resolves it rather than as the file spells it.
type projectRoutingDoc struct {
	// Path is the file the switch was read from, "" when this machine holds no
	// config file for the selected project.
	Path string `json:"path"`
	// Written is whether the file carries a routing block at all: an absent
	// one reads as the direct route, and a reader that has to tell "said
	// direct" from "said nothing" cannot read that off Via.
	Written bool `json:"written"`
	// Via is the route spelled out, so no consumer branches on the empty
	// string: direct, cascade, openrouter or auto (config.RoutingClaude.ViaValue).
	Via string `json:"via"`
	// Models is the Claude IDs the switch carries, always a list: a switch
	// under a route must name its opt-ins, and every other route carries none.
	Models []string `json:"models"`
}

func projectRoutingDocOf(path string, r *config.Routing) projectRoutingDoc {
	doc := projectRoutingDoc{Path: path, Via: config.RouteDirect, Models: []string{}}
	if r == nil || r.Claude == nil {
		return doc
	}
	doc.Written, doc.Via, doc.Models = true, r.Claude.ViaValue(), nonNil(r.Claude.Models)
	return doc
}

func runProjectRoutingShow(cmd *cobra.Command, jsonOut bool) error {
	sel, lc, err := settingsSelection()
	if err != nil {
		return err
	}
	if jsonOut {
		return writeJSON(cmd.OutOrStdout(), projectRoutingDocOf(sel.Path, lc.Routing))
	}
	fmt.Fprintf(cmd.OutOrStdout(), "routing %s: %s\n", projectProfileWhere(sel.Path), routingText(lc.Routing))
	return nil
}

func runProjectRoutingSet(cmd *cobra.Command, target string, o *projectRoutingSetOptions) error {
	if target != settingsRoutingTarget {
		return userErr("routing has one switch, %s: `fugaro project routing set %s --via ...` sets where this project's Claude calls go (%s writes a provider, its secret and its allow_data_to, and the switch only names which of them carries a call)",
			settingsRoutingTarget, settingsRoutingTarget, "fugaro models add")
	}
	if o.via == "" {
		return userErr("--via is required: name %s, %s, %s or %s (an absent switch is %s, and this command exists to write the switch out)",
			config.RouteDirect, config.RouteCascade, config.RouteOpenRouter, config.RouteAuto, config.RouteDirect)
	}
	modelsSet := cmd.Flags().Changed("models")
	if modelsSet && projectRoutingListless(o.via) {
		return userErr("via: %s takes no models list, and --models names one: the cascade decides which credential carries a call and never where it goes, so it has no route for a list to opt into, and the direct route carries none either; drop --models, or name the route that carries the list (--via %s or %s)",
			o.via, config.RouteOpenRouter, config.RouteAuto)
	}
	sel, lc, err := settingsLocalFile("project routing set")
	if err != nil {
		return err
	}
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: sel.Path})
	if err != nil {
		return err
	}
	have := projectRoutingModels(lc)
	want := have
	switch {
	case modelsSet:
		want = projectProfileList(o.models)
	case projectRoutingListless(o.via):
		// The list a route carried goes with the route: it is the route's own
		// opt-in, and leaving it behind would name models the switch does not
		// send anywhere (D1b's default said out loud).
		want = nil
	}
	next, err := yamledit.SetScalar(old, []string{"routing", "claude", "via"}, o.via)
	if next, err = workspaceEditResult(sel.Path, next, err); err != nil {
		return err
	}
	next, err = settingsSetList(next, []string{"routing", "claude", "models"}, have, want)
	if next, err = workspaceEditResult(sel.Path, next, err); err != nil {
		return err
	}
	e := settingsEdit{
		Old:     old,
		New:     next,
		What:    projectRoutingSetWhat(sel.Path, o.via, want),
		Command: projectRoutingSetCommand(o),
		Yes:     o.yes,
	}
	// A route is where the project's prompts and file excerpts leave for a
	// third party, so it is the widening of this block (D846): the owner's own
	// step, refused in a coding agent's session whatever --yes says and
	// admitted by no plan gate (D1026, D1552). Narrowing — direct, the cascade,
	// an unset — is the ordinary write it looks like.
	if !projectRoutingListless(o.via) {
		e.Typed = true
		e.TypedWhy = fmt.Sprintf("this sends Claude calls to the provider route %s names: the prompts, the file excerpts and the diffs of every run the switch covers leave for that provider instead of Anthropic, and only the entry's own allow_data_to decides which repositories' code may be sent there", o.via)
	}
	if err := projectConfigEditLocal(cmd, lc, sel, e); err != nil {
		return err
	}
	workspaceHandoff(cmd, old, next)
	return nil
}

// settingsRoutingTarget is the one switch the routing block has: a name rather
// than a free segment, because `routing set <anything>` would read as a second
// switch that nothing writes or reads.
const settingsRoutingTarget = "claude"

// projectRoutingListless reports whether a via carries no models list at all.
// The two that do are the routes (openrouter and auto); everything else is
// refused by the read-back, which is where the four accepted values and their
// spelling live (config.CheckRoutingShape).
func projectRoutingListless(via string) bool {
	return via == config.RouteCascade || via == config.RouteDirect
}

// projectRoutingModels is the list the file's switch carries, which is what a
// list edit compares the wanted values against.
func projectRoutingModels(lc *localcfg.Config) []string {
	if lc.Routing == nil || lc.Routing.Claude == nil {
		return nil
	}
	return lc.Routing.Claude.Models
}

// projectRoutingSetWhat is the write in one sentence, in the words of the route
// it writes: each of the four says something different about where a call goes
// and which credential pays for it, and a reader of the diff is deciding that.
func projectRoutingSetWhat(path, via string, models []string) string {
	switch via {
	case config.RouteCascade:
		return fmt.Sprintf("set routing.claude.via to %s in %s: every Claude call still goes to Anthropic under the ID a pin names, and the credential that carries it is the cascade's — the project's other credentials, and with credentials.fallback: auto the paid tiers", via, path)
	case config.RouteDirect:
		return fmt.Sprintf("set routing.claude.via to %s in %s: no Claude call takes a provider route, so every one goes to Anthropic", via, path)
	default:
		return fmt.Sprintf("set routing.claude.via to %s in %s, carrying %s: a run sends these Claude IDs to the provider entry that claims them, and only the repositories in that entry's allow_data_to may have their code sent there", via, path, strings.Join(nonNil(models), ", "))
	}
}

func projectRoutingSetCommand(o *projectRoutingSetOptions) string {
	cmd := fmt.Sprintf("fugaro project routing set %s --via %s", settingsRoutingTarget, o.via)
	if o.models != "" {
		cmd += " --models " + o.models
	}
	return cmd
}

func runProjectRoutingUnset(cmd *cobra.Command, yes bool) error {
	sel, lc, err := settingsLocalFile("project routing unset")
	if err != nil {
		return err
	}
	if err := projectRoutingRefuseSilence(lc, sel.Path); err != nil {
		return err
	}
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: sel.Path})
	if err != nil {
		return err
	}
	next, err := yamledit.RemoveKeyWithOptions(old, []string{"routing"}, yamledit.RemoveOptions{DropEmptyParents: true})
	if next, err = workspaceEditResult(sel.Path, next, err); err != nil {
		return err
	}
	e := settingsEdit{
		Old:     old,
		New:     next,
		What:    fmt.Sprintf("remove the routing block from %s: the config then sets no Claude switch, which reads as %s, and every Claude call goes to Anthropic (D1b)", sel.Path, config.RouteDirect),
		Command: "fugaro project routing unset",
		Yes:     yes,
	}
	if err := projectConfigEditLocal(cmd, lc, sel, e); err != nil {
		return err
	}
	workspaceHandoff(cmd, old, next)
	return nil
}

// projectRoutingRefuseSilence is the publisher's silence rule at the door that
// writes the block. `fugaro project policy publish` refuses a node it would
// turn a switch off for by building it from a config that is silent about
// routing (buildProjectPolicy, whose two node-level predicates delegate to
// routingTurnsTheCascadeOn and routingRoutesClaude below), and removing a block
// this commands writes is the same silence one step earlier: `unset` is the only
// way this family can say nothing at all.
//
// Both sentences name the write that says it on purpose, because that is what
// the operator almost always means: via: direct keeps the block and routes
// nothing, so the publish still accounts for it and the file says which of the
// two states it is in.
func projectRoutingRefuseSilence(lc *localcfg.Config, path string) error {
	switch {
	case routingTurnsTheCascadeOn(lc.Routing):
		return userErr("the switch at routing.claude in %s turns the credentials cascade on (routing: %s), and removing the block would turn it off by omission: the cascade is what lets a run's Claude calls fall through to the project's other credentials, and with credentials.fallback: auto it also spends the paid tiers. Write it off on purpose — routing: {claude: {via: direct}}, which `fugaro project routing set claude --via direct` writes — or leave the block as it is; nothing was written",
			path, routingText(lc.Routing))
	case routingRoutesClaude(lc.Routing):
		return userErr("the switch at routing.claude in %s carries a route (routing: %s), and removing the block would turn it off by omission: a route that goes away is a Claude call that stops being sent where it was put. Write it off on purpose with `fugaro project routing set claude --via direct`, or leave the block as it is; nothing was written",
			path, routingText(lc.Routing))
	}
	return nil
}
