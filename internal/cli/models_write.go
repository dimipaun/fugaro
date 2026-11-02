package cli

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// This file is the shared half of the three models writes: the selected
// document, the strict read-back of the edited bytes, the refusal a splice
// cannot make, and the read of which repositories pin a model (D780, D789,
// D790). `models add` and `models rm` edit the selected local project config
// and nothing else; `models use` edits a repository's own fugaro.yaml in its
// working tree. All three go through applySettingsEdit, so the diff, the
// confirmation, the coding-agent-session fence, the backup and the atomic
// write are that file's one copy of each (settings plan F4).

// modelsLocalFile is the selected local project config and the file it lives
// in: what `models add` and `models rm` edit. A project selected from a layer
// and with no file of its own has nothing to edit, which is a refusal and not
// an empty edit.
func modelsLocalFile() (localcfg.Selection, *localcfg.Config, error) {
	sel, lc, err := settingsSelection()
	if err != nil {
		return sel, nil, err
	}
	if sel.Path == "" {
		return sel, nil, userErr("no local project config file is selected: models add and models rm edit the selected project's own config file, and this project has none; run fugaro setup, or pass --config")
	}
	return sel, lc, nil
}

// modelsEditLocal is one edit of the selected local project config, checked and
// written. The bytes are the caller's: it read Old and produced New.
func modelsEditLocal(cmd *cobra.Command, lc *localcfg.Config, sel localcfg.Selection, old, next []byte, what, command string, yes bool) error {
	if yamledit.Changed(old, next) {
		after, err := modelsReparseLocal(sel.Path, next)
		if err != nil {
			return err
		}
		// The price table is built from the edited bytes, with the builder every
		// pricing surface uses: a claim the catalog refuses, or a model_prices
		// entry the compile cannot express, is refused here rather than written
		// into a file that would refuse every launch (design D5).
		if _, _, err := priceTable(after, budgetNow()); err != nil {
			return userErr("the project config this would write does not price its models: %v", err)
		}
	}
	return applySettingsEdit(cmd, lc, settingsEdit{
		Path:    sel.Path,
		Old:     old,
		New:     next,
		What:    what,
		Kind:    settingsDocLocal,
		Command: command,
		Yes:     yes,
	})
}

// modelsReparseLocal holds the edited bytes against the strict loader the
// command's own read used, and answers the config they decode to.
func modelsReparseLocal(path string, next []byte) (*localcfg.Config, error) {
	after, err := localcfg.Parse(next)
	if err != nil {
		return nil, userErr("%s would not read back after the edit: %v", path, err)
	}
	return after, nil
}

// modelsSpliceRefusal is the refusal yamledit itself makes, in the owner's
// words: the editor will not splice into a flow collection, a mapping that
// carries a merge key or a tab-indented block, and a models write must never
// fall back to a re-marshalled document, which would drop the comments and the
// key order of everything it does not touch (D790). The second result is false
// for an error this package did not produce.
func modelsSpliceRefusal(path string, err error) (error, bool) {
	reason := yamledit.ReasonOf(err)
	if reason == "" {
		return err, false
	}
	return userErr("%s cannot be changed by one edit (%s), and this command keeps every comment and key order it does not touch, so it wrote nothing: write the entry in by hand, or put that block in block style — one item per line — and repeat the command", path, reason), true
}

// modelsRefuseVariant is the refusal a ":" variant gets from every models
// write, in one sentence: the suffix is another price and another routing, and
// no file these commands carry holds one.
func modelsRefuseVariant(id string) error {
	return userErr("%s is a variant (a \":\" suffix is another price and routing): name the plain vendor/model ID, which is what a config carries", config.CodeSpan(id))
}

// modelsRoleBackground is the third key agent.models has. It is a literal and
// not a config.Role: background has no launch role of its own (ModelFor folds
// it into the coder's, and EffectiveBackground is what a background request
// actually reaches), so it is named here as the one key the file writes.
const modelsRoleBackground = "background"

// modelsRoleNames are the three keys agent.models has, in the order every
// surface lists them: what `models use` may name (spec §6).
var modelsRoleNames = []string{string(config.RoleCoder), string(config.RoleReviewer), modelsRoleBackground}

// modelsRoleModel is the model a fugaro.yaml pins for one of those keys.
// ModelFor folds background into the coder — reading a file's own
// agent.models.background through it would report a coder pin a second time —
// so the background key is read directly.
func modelsRoleModel(a config.Agent, role string) string {
	if role == modelsRoleBackground {
		return a.Models.Background
	}
	return a.ModelFor(config.Role(role))
}

// modelsPin is one repository and role whose file names a model.
type modelsPin struct {
	Repo string
	Role string
}

// modelsModelPinned is the repositories whose fugaro.yaml pins id in one of the
// three roles, and the repositories this machine cannot read: one with no
// checkout here, one whose file does not parse, one whose origin it cannot
// trust. `models add` reads the first to decide whether to name `models use` as
// the way to use the model, and `models rm` warns about both.
func modelsModelPinned(lc *localcfg.Config, id string) (pins []modelsPin, unseen []string) {
	checkouts, err := localcfg.LoadCheckouts(os.Getenv)
	if err != nil {
		checkouts = localcfg.EmptyCheckouts()
	}
	remembered := map[string]string{}
	for _, c := range checkouts.ProjectCheckouts(lc.Name) {
		remembered[localcfg.CheckoutIdentity{Provider: c.Provider, Repo: c.Repo}.Key()] = checkoutPath(c)
	}
	layer := modelsLayer(lc.Name)
	for _, name := range slices.Sorted(maps.Keys(lc.Repos)) {
		dir := remembered[checkoutKeyFor(name, remembered)]
		if dir == "" {
			unseen = append(unseen, name)
			continue
		}
		data, err := readFugaroYAML(filepath.Join(dir, "fugaro.yaml"))
		if err != nil {
			unseen = append(unseen, name)
			continue
		}
		cfg, _, ps := config.Resolve(data, layer)
		if len(ps) > 0 {
			unseen = append(unseen, name)
			continue
		}
		for _, role := range modelsRoleNames {
			if modelsRoleModel(cfg.Agent, role) == id {
				pins = append(pins, modelsPin{Repo: name, Role: role})
			}
		}
	}
	return pins, unseen
}

// sortedRepoNames is the project's recorded repositories, sorted: the order
// every surface names them in, so two reads of one config cannot disagree.
func sortedRepoNames(lc *localcfg.Config) []string {
	return slices.Sorted(maps.Keys(lc.Repos))
}

// modelsPinsSentence is those pins in words, one clause per repository and
// role, so two surfaces that have to name the same break describe it the same
// way.
func modelsPinsSentence(pins []modelsPin) string {
	parts := make([]string, 0, len(pins))
	for _, p := range pins {
		parts = append(parts, fmt.Sprintf("%s (%s)", p.Repo, p.Role))
	}
	return strings.Join(parts, ", ")
}
