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
	if !claimed && strings.Contains(id, "/") {
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
		if claimed {
			return userErr("%s has no price a run can use: %s. The owner gives it one with `fugaro models add %s --price-in <dollars per million tokens> --price-out <dollars per million tokens>`, which writes a model_prices entry in the project config", config.CodeSpan(id), why, id)
		}
		return userErr("%s has no price a run can use: %s. The owner prices it in the project config before the repository may pin it", config.CodeSpan(id), why)
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
