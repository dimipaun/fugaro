package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

type modelsRmOptions struct {
	yes bool
}

func newModelsRmCmd() *cobra.Command {
	var o modelsRmOptions
	cmd := &cobra.Command{
		Use:   "rm <id>",
		Short: "Stop letting this project run a model: drop its claim, price and allow-list entry",
		Long: "Remove a model from this project's own config.\n\n" +
			"The model's exact entry is dropped from the route's models, its model_prices\n" +
			"entry and its budget.allowed_models entry. A pattern that claims the model\n" +
			"is left, and the command says so: the model stays usable until the route's\n" +
			"own list stops covering it.\n\n" +
			"A repository that pins the model is not refused (D783). It is a warning:\n" +
			"the repositories this machine can read that pin it are named, with the role\n" +
			"each names it in, and so are the repositories it cannot read. The next run\n" +
			"of each one fails at launch with no provider for the model, until the pin is\n" +
			"changed with `fugaro models use` or the model is added back.\n\n" +
			"Only the keys it changes are rewritten: every comment and every other key of\n" +
			"the file is kept. The change reaches a run through `fugaro update`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runModelsRm(cmd, args[0], &o) },
	}
	cmd.Flags().BoolVar(&o.yes, "yes", false, "confirm the write without a prompt")
	return cmd
}

func runModelsRm(cmd *cobra.Command, id string, o *modelsRmOptions) error {
	if err := modelsRmRefuseID(id); err != nil {
		return err
	}
	sel, lc, err := modelsLocalFile()
	if err != nil {
		return err
	}
	if err := modelsRmRefuseEmptyingAllow(lc, id); err != nil {
		return err
	}
	old, err := settingsReadDocument(settingsTarget{kind: settingsDocLocal, path: sel.Path})
	if err != nil {
		return err
	}

	next := old
	claim, err := modelsRmClaim(sel.Path, lc, id, &next)
	if err != nil {
		return err
	}
	if err := modelsRmPrice(sel.Path, lc, id, &next); err != nil {
		return err
	}
	if err := modelsRmAllow(sel.Path, lc, id, &next); err != nil {
		return err
	}

	changed := yamledit.Changed(old, next)
	// What the file would carry after the removal is read from the bytes the
	// write lands, never reasoned about from the edit this command made: the
	// warning's own sentence — no provider serves the model — is true of a config
	// the model is gone from, and only that config can say whether it is.
	var after *localcfg.Config
	if changed {
		if after, err = modelsReparseLocal(sel.Path, next); err != nil {
			return err
		}
	}

	what := fmt.Sprintf("remove %s from the project config", id)
	if claim.route != "" {
		what += fmt.Sprintf(" (route %s claims it no longer)", claim.route)
	}
	if err := modelsEditLocal(cmd, lc, sel, old, next, what, "fugaro models rm "+id, o.yes); err != nil {
		return err
	}
	// The warning is about the break this removal makes, so it follows the write:
	// a refused one — a coding agent's session, a file the edit cannot splice —
	// left every pin where it was, and a warning would send the owner to repair
	// a break nothing caused. A removal that removed nothing (an ID of neither
	// the config nor its price table) makes no break either, and modelsRmServes
	// answers for the rest: a model a route still serves is not one the next run
	// fails without.
	if changed {
		modelsRmWarn(cmd, lc, id, modelsRmServes(after, id))
	}
	out := cmd.OutOrStdout()
	if changed && claim.by != "" {
		fmt.Fprintf(out, "%s is still claimed by %s on route %s: a pattern is not this command's to remove\n", oneLine(id), oneLine(claim.by), oneLine(claim.route))
	}
	if claim.route == "" && claim.by == "" {
		fmt.Fprintf(out, "%s is not a model of this project's config or of its price table: nothing to remove\n", oneLine(id))
	}
	return nil
}

// modelsRmRefuseID refuses an ID the config cannot carry: a command that cannot
// remove an alias or a variant tells the owner so instead of reporting nothing
// to remove for a key it never looked for. A Claude ID is the owner's routing
// switch, not a claim, so there is nothing here to drop (D785).
func modelsRmRefuseID(id string) error {
	switch {
	case isClaudeModel(id):
		return userErr("%s is a Claude model: Claude models go direct to Anthropic, or down the route the owner's routing.claude switch names, and no provider route claims one, so there is nothing here to remove. Change routing.claude in the project config instead", config.CodeSpan(id))
	case strings.Contains(id, ":"):
		return modelsRefuseVariant(id)
	}
	return nil
}

// modelsRmRefuseEmptyingAllow is the one refusal `rm` makes about the file
// itself: budget.allowed_models is never left as an empty list, which the
// strict loader refuses (spec §5), so the last entry is refused here with the
// reason rather than written into a file the next command would not read.
func modelsRmRefuseEmptyingAllow(lc *localcfg.Config, id string) error {
	if lc.Budget == nil || len(lc.Budget.AllowedModels) != 1 || lc.Budget.AllowedModels[0] != id {
		return nil
	}
	return userErr("budget.allowed_models holds %s and nothing else, and an empty list is not a value this config can carry (the loader refuses it: nothing could tell \"no model is allowed\" from \"not decided yet\"): add the models the project should allow, or remove the budget.allowed_models key entirely, and run this again", config.CodeSpan(id))
}

// modelsRmClaimNote is what the claim half of the removal found: the route the
// exact entry sat on, and the pattern that still claims the model if one does.
type modelsRmClaimNote struct {
	route string
	by    string
}

// modelsRmClaim drops the exact entry from its route's models (spec §5). A
// route that claims the model by a pattern only is left alone — the pattern is
// the owner's, and removing it would stop the route serving models they never
// asked about — and the command says the ID stays claimed by it.
func modelsRmClaim(path string, lc *localcfg.Config, id string, next *[]byte) (modelsRmClaimNote, error) {
	for _, name := range sortedProviderNames(lc.Providers) {
		p := lc.Providers[name]
		exact, pattern := false, ""
		for _, pat := range p.Models {
			switch {
			case pat == id:
				exact = true
			case config.PatternMatches(pat, id):
				pattern = pat
			}
		}
		if !exact && pattern == "" {
			continue
		}
		// A pattern that claims the model is a claim: it keeps covering it
		// after this removal, and the sentence saying so is the difference
		// between "removed" and "removed from this route's list".
		note := modelsRmClaimNote{route: name, by: pattern}
		if !exact {
			return note, nil
		}
		if len(p.Models) == 1 {
			return modelsRmClaimNote{}, userErr("providers.%s.models holds %s and nothing else, and a route that serves no model is refused by the loader (\"must list the model IDs the provider serves\"): remove the route, or name the models it should keep serving", name, config.CodeSpan(id))
		}
		out, err := yamledit.RemoveListItem(*next, []string{"providers", name, "models"}, id)
		if err != nil {
			if err, ok := modelsSpliceRefusal(path, err); ok {
				return modelsRmClaimNote{}, err
			}
			return modelsRmClaimNote{}, userErr("providers.%s.models: %v", name, err)
		}
		*next = out
		return note, nil
	}
	return modelsRmClaimNote{}, nil
}

// modelsRmPrice drops the model's model_prices entry, when the file prices it
// by hand.
func modelsRmPrice(path string, lc *localcfg.Config, id string, next *[]byte) error {
	if _, ok := lc.ModelPrices[id]; !ok {
		return nil
	}
	out, err := yamledit.RemoveKeyWithOptions(*next, []string{"model_prices", id}, yamledit.RemoveOptions{DropEmptyParents: true})
	if err != nil {
		if err, ok := modelsSpliceRefusal(path, err); ok {
			return err
		}
		return userErr("model_prices.%s: %v", id, err)
	}
	*next = out
	return nil
}

// modelsRmAllow drops the model's budget.allowed_models entry.
func modelsRmAllow(path string, lc *localcfg.Config, id string, next *[]byte) error {
	if lc.Budget == nil || !slices.Contains(lc.Budget.AllowedModels, id) {
		return nil
	}
	out, err := yamledit.RemoveListItem(*next, []string{"budget", "allowed_models"}, id)
	if err != nil {
		if err, ok := modelsSpliceRefusal(path, err); ok {
			return err
		}
		return userErr("budget.allowed_models: %v", err)
	}
	*next = out
	return nil
}

// modelsRmWarn is the whole of the protection D783 leaves: it names every
// repository and role this machine can see that pins the model, every
// repository it cannot read, and what the next run of each one does. The second
// sentence is the break itself — the run has no provider for the model — and it
// is printed only for a file the model is really gone from (served): a route
// that goes on claiming it, by the pattern this command deliberately leaves, is
// a run that still launches, and a warning saying otherwise would send the owner
// to change a pin that needs no change.
func modelsRmWarn(cmd *cobra.Command, lc *localcfg.Config, id string, served bool) {
	pins, unseen := modelsModelPinned(lc, id)
	if len(pins) == 0 && len(unseen) == 0 {
		return
	}
	out := cmd.OutOrStdout()
	if len(pins) > 0 {
		fmt.Fprintf(out, "warning: %s is pinned by %s\n", oneLine(id), modelsPinsSentence(pins))
		if !served {
			fmt.Fprintf(out, "warning: the next run of %s fails at launch: no provider serves it once the claim is gone. Change the pin with `fugaro models use <repo> coder|reviewer|background %s`, or put the model back with `fugaro models add %s`\n", pluralRepos(pins), oneLine(id), oneLine(id))
		}
	}
	if len(unseen) > 0 {
		fmt.Fprintf(out, "warning: this machine cannot read %s (no checkout here), so a pin there is unknown: run `fugaro models ls` in that checkout to see it\n", strings.Join(unseen, ", "))
	}
}

// modelsRmServes reports whether the file this removal wrote still serves the
// model: a route claims it — by the pattern the removal deliberately leaves, or
// by an exact entry on another route — or its budget.allowed_models goes on
// naming it. The edited config answers, not this command's account of its own
// edit, so the sentence about the next run having no provider is printed only
// where there is none.
func modelsRmServes(after *localcfg.Config, id string) bool {
	if _, _, ok := config.ProviderFor(after.Providers, id); ok {
		return true
	}
	return after.Budget != nil && slices.Contains(after.Budget.AllowedModels, id)
}

// pluralRepos is "that run" for one repository and "those runs" for more, so
// the sentence agrees with the list it just printed.
func pluralRepos(pins []modelsPin) string {
	seen := map[string]bool{}
	for _, p := range pins {
		seen[p.Repo] = true
	}
	if len(seen) == 1 {
		return "that repository"
	}
	return "those repositories"
}
