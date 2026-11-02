package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

type modelsUseOptions struct {
	yes bool
}

func newModelsUseCmd() *cobra.Command {
	var o modelsUseOptions
	cmd := &cobra.Command{
		Use:   "use <repo> <coder|reviewer|background> <id>",
		Short: "Point one repository's own config at a model, in its working tree",
		Long: "Prepare the change to a repository's own fugaro.yaml that makes one role run\n" +
			"a model, in the checkout's working tree for the owner to review and commit.\n\n" +
			"The switch is judged the way a launch judges it: a route of the project config\n" +
			"must claim the model and its allow_data_to must name the repository, the model\n" +
			"must be priced, budget.allowed_models must admit it, and the repository's own\n" +
			"agent.auth and budget must support a provider model. A failure is a refusal\n" +
			"with nothing written, and the message names the `fugaro models add` that would\n" +
			"fix a missing claim or price.\n\n" +
			"Only agent.models.<role> changes: every comment and every other byte of the\n" +
			"file is kept. The command never commits, pushes or opens a pull request — the\n" +
			"change reaches runs when the owner's own branch is merged to the base branch,\n" +
			"which is what every run reads.",
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runModelsUse(cmd, args[0], args[1], args[2], &o)
		},
	}
	cmd.Flags().BoolVar(&o.yes, "yes", false, "confirm the write without a prompt")
	return cmd
}

func runModelsUse(cmd *cobra.Command, repo, role, id string, o *modelsUseOptions) error {
	if !slices.Contains(modelsRoleNames, role) {
		return userErr("role %s: a repository pins %s", oneLine(role), strings.Join(modelsRoleNames, ", "))
	}
	if err := modelsUseRefuseID(id); err != nil {
		return err
	}
	_, lc, err := settingsSelection()
	if err != nil {
		return err
	}
	ctx := context.Background()
	path, err := modelsUseFile(ctx, repo, lc.Name)
	if err != nil {
		return err
	}
	target := settingsTarget{kind: settingsDocRepo, path: path, project: lc.Name}
	old, err := settingsReadDocument(target)
	if err != nil {
		return err
	}
	cfg, ps := config.Parse(old)
	if len(ps) > 0 {
		return userErr("%s: %s", path, ps[0].String())
	}
	table, _, err := priceTable(lc, budgetNow())
	if err != nil {
		return err
	}
	if err := modelsUseCheck(lc, cfg, path, repo, role, id, table); err != nil {
		return err
	}

	next, err := yamledit.SetScalar(old, []string{"agent", "models", role}, id)
	if err != nil {
		if err, ok := modelsSpliceRefusal(path, err); ok {
			return err
		}
		return userErr("agent.models.%s: %v", role, err)
	}
	if err := settingsReparse(target, next); err != nil {
		return err
	}
	after, ps := config.Parse(next)
	if len(ps) > 0 {
		return userErr("%s would not read back after the edit: %s", path, ps[0].String())
	}
	if err := modelsUseJudge(ctx, lc, cfg, after, path, repo, role, id); err != nil {
		return err
	}

	what := fmt.Sprintf("point %s's %s at %s", repo, role, id)
	command := fmt.Sprintf("fugaro models use %s %s %s", repo, role, id)
	if err := applySettingsEdit(cmd, lc, settingsEdit{
		Path:    path,
		Old:     old,
		New:     next,
		What:    what,
		Kind:    settingsDocRepo,
		Command: command,
		Yes:     o.yes,
	}); err != nil {
		return err
	}
	if yamledit.Changed(old, next) {
		fmt.Fprintf(cmd.OutOrStdout(), "the change is in %s's working tree and nowhere else: review it, commit it on your own branch and merge that to the base branch — every run reads the base branch, and this command never commits or pushes\n", oneLine(repo))
	}
	return nil
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
// than trusted: modelsUseCheck has already ruled on allow_data_to for the
// repository this file belongs to, and a line about another checkout's
// repository is a refusal no launch of this file would make (providerProblems
// drops the same lines for its own "the repository is unknown here" state).
func modelsUseJudge(ctx context.Context, lc *localcfg.Config, before, after *config.Config, path, repo, role, id string) error {
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
		return userErr("%s would not launch with %s pinned for %s: %s", path, config.CodeSpan(id), role, p.String())
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

// modelsUseFile is the fugaro.yaml the switch lands in: the checkout the working
// directory is in when it is that repository's, else the checkout this machine
// remembers for it (spec §6). A repository with neither has nowhere to put the
// change, which is a refusal and not an edit somewhere else.
func modelsUseFile(ctx context.Context, repo, project string) (string, error) {
	if !settingsRepoRE.MatchString(repo) {
		return "", userErr("repository %s: a repository is owner/name", oneLine(repo))
	}
	if _, here := checkoutOriginAt(""); here != "" && strings.EqualFold(here, repo) {
		if root, err := gitRead(ctx, ".", "rev-parse", "--show-toplevel"); err == nil {
			return filepath.Join(root, "fugaro.yaml"), nil
		}
	}
	return settingsRepoFile(ctx, repo, project)
}

// modelsUseCheck is the launch's own judgement of one pin, made before the file
// changes rather than at the next run (spec §6): the model is claimed, the route
// opens to this repository, the model is priced, the allow-list admits it, and
// the repository's own auth and budget support a provider model. The order is
// the order a reader can act in — the fix that has to happen first is the one
// named.
func modelsUseCheck(lc *localcfg.Config, cfg *config.Config, path, repo, role, id string, table *pricing.Table) error {
	name, p, claimed := config.ProviderFor(lc.Providers, id)
	// A Claude model reaches Anthropic with no route at all, in either spelling
	// a pin may carry (isClaudeModel), so the missing-route refusal is only for
	// a vendor/model ID a route has not opened: telling the owner to open a
	// route for a Claude ID would name a fix that does not exist (spec §6, §8).
	claude := isClaudeModel(id)
	if !claimed && !claude && strings.Contains(id, "/") {
		return userErr("no route of the project config serves %s, so no run of %s could reach it: the owner opens one with `fugaro models add %s`, and a repository names a model the owner has opened and nothing more", config.CodeSpan(id), config.CodeSpan(repo), id)
	}
	if claimed && !p.AllowsData(repo) {
		return userErr("%s is served by route %s, whose allow_data_to does not name %s: the owner adds the repository to providers.%s.allow_data_to in the project config before its code may be sent there", config.CodeSpan(id), name, config.CodeSpan(repo), name)
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
		return userErr("budget.allowed_models of the project config does not name %s, so no run of %s could use it: the owner names it there, or adds the model with `fugaro models add %s`", config.CodeSpan(id), config.CodeSpan(repo), id)
	}
	agent := cfg.Agent
	switch role {
	case modelsRoleBackground:
		agent.Models.Background = id
	case string(config.RoleReviewer):
		agent.Models.Reviewer = id
	default:
		agent.Models.Coder = id
	}
	if ps := config.CheckProviderAuth(agent, lc.Providers); len(ps) > 0 {
		return userErr("%s would not launch with %s pinned for %s: %s", path, config.CodeSpan(id), role, ps[0].String())
	}
	mode := policy.Merge(ceilingLayer(lc), runner.FileLayer(cfg)).Mode
	if ps := config.CheckProviderGateway(agent, config.EffectiveBackground(agent, lc.Providers), lc.Providers, mode); len(ps) > 0 {
		return userErr("%s would not launch with %s pinned for %s: %s", path, config.CodeSpan(id), role, ps[0].String())
	}
	return nil
}
