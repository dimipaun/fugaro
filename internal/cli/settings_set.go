package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/settings"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// The layers a write lands in (D845). `default` is never a target — it is a
// level WINS can name, not a store — and neither is `shared`, which stands in
// for a key this machine's own file lacks rather than being a file of its own.
const (
	settingsLayerLocal   = "local"
	settingsLayerProject = "project"
	settingsLayerRepo    = "repo"
)

// settingsWriteLayers is the closed vocabulary --layer accepts on a write, in
// the order a refusal prints it.
var settingsWriteLayers = []string{settingsLayerLocal, settingsLayerProject, settingsLayerRepo}

// settingsFamilyCommands maps a registry Owner to the command family that
// writes its keys (D853). A key whose Owner is in here is refused by the
// generic path and pointed at that family: two paths writing one key is how
// the cross-key checks the families exist for get lost.
var settingsFamilyCommands = map[string]string{
	"models":      "fugaro models",
	"rigor":       "fugaro rigor set",
	"rigor floor": "fugaro rigor floor",
	"accounts":    "fugaro accounts",
	"secrets":     "fugaro secrets",
	"project":     "fugaro project",
}

// settingsWriteOptions are the flags `set` and `unset` share.
type settingsWriteOptions struct {
	layer  string
	repo   string
	yes    bool
	add    string
	remove string
}

// settingsDocKind names the document a write lands in: the local project
// config, the local project layer file, or a repository's fugaro.yaml.
type settingsDocKind int

const (
	settingsDocLocal settingsDocKind = iota
	settingsDocLayer
	settingsDocRepo
)

// settingsTarget is where one write lands: the file, the path inside it, and
// what it takes to read the edited bytes back strictly.
type settingsTarget struct {
	layer      string
	kind       settingsDocKind
	path       string
	keyPath    []string
	insideRepo bool
	anchor     config.LayerAnchor
}

// newSettingsGetCmd is `fugaro settings get KEY`: the read narrowed to one key.
// It is the same resolver as `settings`, so a key set nowhere prints its
// default row and exits 0 (D852), and an unknown key exits non-zero with the
// nearest registry names.
func newSettingsGetCmd() *cobra.Command {
	var o settingsOptions
	cmd := &cobra.Command{
		Use:   "get KEY",
		Short: "Show one setting, what decides it, and its default",
		Long: `Show one row of the settings read, for one key.

A key set at no layer prints its default row and exits 0: a state that is
already the answer is not a failure. An unknown key exits non-zero with the
nearest registry names.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return userErr("settings get takes one key")
			}
			return runSettings(cmd, args, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "read this repository's fugaro.yaml (owner/name)")
	f.BoolVar(&o.jsonOut, "json", false, "print the versioned document")
	return cmd
}

// newSettingsSetCmd is `fugaro settings set`: the generic write path.
func newSettingsSetCmd() *cobra.Command {
	var o settingsWriteOptions
	cmd := &cobra.Command{
		Use:   "set KEY VALUE",
		Short: "Write one setting into one of the three documents",
		Long: `Write one setting into the local project config, the local project layer
file, or a repository's fugaro.yaml.

The layers are ` + "`local`" + ` (the local project config), ` + "`project`" + ` (the local
project layer file) and ` + "`repo`" + ` (a checkout's fugaro.yaml); ` + "`default`" + ` is never
a target. With no --layer the narrowest layer the key may be set in is the
target. --layer repo without --repo uses the current directory's checkout and
refuses when the directory is not one.

A string-list key takes --add V or --remove V instead of a value. A list
emptied by --remove is refused: an absent key and an empty list mean different
things to the loader, so use unset to remove the key.

Loosening a ceiling key (budget.*, governance.*) or turning a followup.* key
on asks for the project's name rather than y/N, and --yes never answers that
form. Writing outside a repository is refused in a coding agent's session,
--yes included; a ceiling loosened at --layer repo is refused there too.

Every write ends with one line: next: fugaro update.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSettingsSet(cmd, args, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.layer, "layer", "", "the layer to write ("+strings.Join(settingsWriteLayers, ", ")+")")
	f.StringVar(&o.repo, "repo", "", "the repository whose fugaro.yaml to edit (owner/name)")
	f.BoolVar(&o.yes, "yes", false, "confirm the write without a prompt")
	f.StringVar(&o.add, "add", "", "add an item to a string-list key")
	f.StringVar(&o.remove, "remove", "", "remove an item from a string-list key")
	return cmd
}

// newSettingsUnsetCmd is `fugaro settings unset`: it removes the key from one
// layer so the layer below decides again, and never writes a default in its
// place.
func newSettingsUnsetCmd() *cobra.Command {
	var o settingsWriteOptions
	cmd := &cobra.Command{
		Use:   "unset KEY",
		Short: "Remove one setting from one of the three documents",
		Long: `Remove a key from one layer so the layer below decides again.

It never writes a default in the key's place: a written default is
indistinguishable from a deliberate pin and silently wrong the day the built-in
changes. Nothing to remove is a no-op with a sentence and exit 0, so an
idempotent script does not fail.

Removing a ceiling key asks for the project's name rather than y/N, and --yes
never answers that form.

Every write ends with one line: next: fugaro update.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSettingsUnset(cmd, args, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.layer, "layer", "", "the layer to write ("+strings.Join(settingsWriteLayers, ", ")+")")
	f.StringVar(&o.repo, "repo", "", "the repository whose fugaro.yaml to edit (owner/name)")
	f.BoolVar(&o.yes, "yes", false, "confirm the write without a prompt")
	return cmd
}

// settingsWriteRow finds the row a write names and refuses the two things the
// generic path never writes: a key a dedicated family owns, and a key with a
// varying segment, which is a map entry and has no generic form (D853).
func settingsWriteRow(key string) (settings.Row, error) {
	row, ok := settings.Lookup(key)
	if !ok {
		return settings.Row{}, userErr("%s: no setting has that name; the nearest are %s",
			key, strings.Join(nearestSettings(key, settings.Rows()), ", "))
	}
	if family, ok := settingsFamilyCommands[row.Owner]; ok {
		return settings.Row{}, userErr("%s is written by %s, not by the generic path: settings set writes no route, price, rigor mode, floor, account, job profile, credential or clone URL", key, family)
	}
	if strings.Contains(key, "*") {
		return settings.Row{}, userErr("%s varies with a name this command does not take: a key with a * is a map entry, and a map has no generic form", key)
	}
	return row, nil
}

// settingsDefaultLayer is the target of a write that names no --layer: the
// narrowest layer the key may be set in, so an edit lands as close to the
// repository as the key allows and the narrowest layer is the one that decides.
func settingsDefaultLayer(row settings.Row) string {
	for _, l := range []string{settingsLayerRepo, settingsLayerProject, settingsLayerLocal} {
		if slices.Contains(row.Layers, l) {
			return l
		}
	}
	return ""
}

// settingsSelection selects this machine's project, the way the read does.
func settingsSelection() (localcfg.Selection, *localcfg.Config, error) {
	in := localcfg.SelectInput{
		Project:    os.Getenv("FUGARO_PROJECT"),
		EnvProject: os.Getenv("FUGARO_PROJECT"),
		EnvConfig:  os.Getenv("FUGARO_CONFIG"),
		Getenv:     os.Getenv,
	}
	sel, lc, err := localcfg.Select(in)
	if err != nil {
		return sel, nil, userErr("%w", err)
	}
	if lc == nil || lc.Name == "" {
		return sel, lc, userErr("no project is selected: set $FUGARO_PROJECT or $FUGARO_CONFIG, or run fugaro setup")
	}
	return sel, lc, nil
}

// settingsResolveTarget turns a key and a layer into the file to edit.
func settingsResolveTarget(ctx context.Context, row settings.Row, o settingsWriteOptions, lc *localcfg.Config, sel localcfg.Selection) (settingsTarget, error) {
	layer := o.layer
	if layer == "" {
		layer = settingsDefaultLayer(row)
	}
	if !slices.Contains(settingsWriteLayers, layer) {
		return settingsTarget{}, userErr("--layer %s: a write lands in one of %s (default is never a target)", layer, strings.Join(settingsWriteLayers, ", "))
	}
	if !slices.Contains(row.Layers, layer) {
		return settingsTarget{}, userErr("%s may not be set at --layer %s: it may be set in %s", row.Key, layer, strings.Join(row.Layers, ", "))
	}
	switch layer {
	case settingsLayerLocal:
		if o.repo != "" {
			return settingsTarget{}, userErr("--repo names a repository, so it goes with --layer repo")
		}
		return settingsTarget{layer: layer, kind: settingsDocLocal, path: sel.Path, keyPath: strings.Split(row.Key, ".")}, nil
	case settingsLayerProject:
		if o.repo != "" {
			return settingsTarget{}, userErr("--repo names a repository, so it goes with --layer repo")
		}
		path, err := localcfg.LayerFile(os.Getenv, lc.Name)
		if err != nil {
			return settingsTarget{}, userErr("%v", err)
		}
		if _, err := os.Stat(path); err != nil {
			return settingsTarget{}, userErr("no local project layer file at %s: fugaro update seeds it from the published layer; nothing was written", path)
		}
		return settingsTarget{
			layer:   layer,
			kind:    settingsDocLayer,
			path:    path,
			keyPath: strings.Split(strings.TrimPrefix(row.Key, "layer."), "."),
			anchor:  config.LayerAnchor{Project: lc.Name, GCPProject: lc.GCPProject},
		}, nil
	default:
		path, err := settingsRepoFile(ctx, o.repo, lc.Name)
		if err != nil {
			return settingsTarget{}, err
		}
		return settingsTarget{layer: layer, kind: settingsDocRepo, path: path, keyPath: strings.Split(row.Key, "."), insideRepo: true}, nil
	}
}

// settingsRepoFile is the fugaro.yaml a `--layer repo` write lands in: the
// current directory's checkout when no --repo names one, else the checkout this
// machine remembers for that repository.
func settingsRepoFile(ctx context.Context, repo, project string) (string, error) {
	if repo == "" {
		root, err := gitRead(ctx, ".", "rev-parse", "--show-toplevel")
		if err != nil {
			return "", userErr("--layer repo without --repo uses the checkout in the current directory, and this directory is not one: pass --repo owner/name, or run it from a checkout")
		}
		return filepath.Join(root, "fugaro.yaml"), nil
	}
	if !settingsRepoRE.MatchString(repo) {
		return "", userErr("--repo %s: a repository is owner/name", repo)
	}
	checkouts, err := localcfg.LoadCheckouts(os.Getenv)
	if err != nil {
		return "", userErr("reading this machine's remembered checkouts: %v", err)
	}
	for _, c := range checkouts.ProjectCheckouts(project) {
		if c.Repo != repo || len(c.Paths) == 0 {
			continue
		}
		return filepath.Join(c.Paths[len(c.Paths)-1].Path, "fugaro.yaml"), nil
	}
	return "", userErr("no checkout of %s is remembered on this machine: clone it, or run this from its checkout", repo)
}

// settingsReadDocument reads the file a write would edit, refusing an absent
// one with a message that names what to do rather than creating it: a
// fugaro.yaml or a layer file written from nothing would lack the anchors its
// own loader requires.
func settingsReadDocument(t settingsTarget) ([]byte, error) {
	data, err := os.ReadFile(t.path)
	if err != nil {
		return nil, userErr("reading %s: %v", t.path, err)
	}
	return data, nil
}

// settingsReparse holds the edited bytes against the document's own strict
// loader: a value the loader refuses is refused here, with the loader's message
// (spec §4.2 step 3).
func settingsReparse(t settingsTarget, data []byte) error {
	switch t.kind {
	case settingsDocLocal:
		if _, err := localcfg.Parse(data); err != nil {
			return userErr("%s would not read back after the edit: %v", t.path, err)
		}
	case settingsDocLayer:
		if _, ps := config.ParseProjectLayer(data, t.anchor); len(ps) > 0 {
			return userErr("%s would not read back after the edit: %s", t.path, ps[0].String())
		}
	case settingsDocRepo:
		if _, ps := config.Parse(data); len(ps) > 0 {
			return userErr("%s would not read back after the edit: %s", t.path, ps[0].String())
		}
	}
	return nil
}

// settingsValueIn is the value a document currently holds at path, "" when it
// holds none: what the widening check compares a new value against.
func settingsValueIn(data []byte, path []string) string {
	var tree map[string]any
	if yaml.Unmarshal(data, &tree) != nil {
		return ""
	}
	var cur any = tree
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur, ok = m[p]
		if !ok {
			return ""
		}
	}
	switch v := cur.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case int:
		return strconv.Itoa(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}

// settingsListEmpty reports whether the node at path is a sequence with no
// items: the shape the loader refuses where an absent key and an empty list
// mean different things.
func settingsListEmpty(data []byte, path []string) bool {
	var tree map[string]any
	if yaml.Unmarshal(data, &tree) != nil {
		return false
	}
	var cur any = tree
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		cur, ok = m[p]
		if !ok {
			return false
		}
	}
	s, ok := cur.([]any)
	return ok && len(s) == 0
}

// settingsCeilingKey is the prefix test D846 names: budget.* and governance.*
// are the keys whose loosening crosses the boundary the typed name guards.
func settingsCeilingKey(key string) bool {
	return strings.HasPrefix(key, "budget.") || strings.HasPrefix(key, "governance.")
}

// settingsModeRank orders budget.mode's values loosest first, so a write that
// lowers the rank is a loosening (policy.Merge's order: enforce > observe > off).
func settingsModeRank(mode string) int {
	switch mode {
	case "enforce":
		return 2
	case "observe":
		return 1
	default:
		return 0
	}
}

// settingsLoosens reports whether newValue is looser than oldValue for a
// ceiling key: a larger cap, a weaker mode, or a wider model list. A key the
// document does not set has nothing to loosen, so a first write is never the
// typed form.
func settingsLoosens(key, oldValue, newValue string) bool {
	if oldValue == "" {
		return false
	}
	if key == "budget.mode" {
		return settingsModeRank(newValue) < settingsModeRank(oldValue)
	}
	if key == "budget.allowed_models" {
		return !settingsListSubset(newValue, oldValue)
	}
	oldN, oldErr := strconv.ParseFloat(oldValue, 64)
	newN, newErr := strconv.ParseFloat(newValue, 64)
	if oldErr == nil && newErr == nil {
		return newN > oldN
	}
	// A value that is not a number and not the model list is a form this
	// Fugaro does not know how to compare, so it is treated as a loosening:
	// the typed name on an edit nobody can prove tightens is the safe half of
	// D846's asymmetry.
	return true
}

// settingsListSubset reports whether every member of a appears in b.
func settingsListSubset(a, b string) bool {
	in := map[string]bool{}
	for _, item := range strings.Split(b, ",") {
		in[strings.TrimSpace(item)] = true
	}
	for _, item := range strings.Split(a, ",") {
		if item = strings.TrimSpace(item); item != "" && !in[item] {
			return false
		}
	}
	return true
}

// settingsTypedWrite reports whether this write is one D846 reserves for the
// project's own hand, and the sentence saying why. mode is set, unset, add or
// remove.
func settingsTypedWrite(key, oldValue, newValue, mode string) (bool, string) {
	ceilingWhy := "this loosens a ceiling: the file it would edit decides the ceiling of every run this branch starts, so the project's own name is what confirms it, and --yes does not stand in"
	followupWhy := "this turns a followup key on: a run then acts on comments from people it did not act on before, so the project's own name is what confirms it, and --yes does not stand in"
	if settingsCeilingKey(key) {
		switch mode {
		case "unset":
			return true, "removing a ceiling hands the decision back to a looser layer, so the project's own name is what confirms it, and --yes does not stand in"
		case "add":
			return true, ceilingWhy
		case "set":
			if settingsLoosens(key, oldValue, newValue) {
				return true, ceilingWhy
			}
		}
	}
	if strings.HasPrefix(key, "followup.") && mode == "set" && newValue == "true" {
		return true, followupWhy
	}
	return false, ""
}

// settingsWriteCommand is the command a refusal in a coding agent's session
// prints: the same edit, spelled for a person's own terminal.
func settingsWriteCommand(op, key, value string, o settingsWriteOptions, layer string) string {
	parts := []string{"fugaro", "settings", op, quoteWord(key)}
	switch op {
	case "set":
		switch {
		case o.add != "":
			parts = append(parts, "--add", quoteWord(o.add))
		case o.remove != "":
			parts = append(parts, "--remove", quoteWord(o.remove))
		default:
			parts = append(parts, quoteWord(value))
		}
	}
	parts = append(parts, "--layer", layer)
	if o.repo != "" {
		parts = append(parts, "--repo", quoteWord(o.repo))
	}
	return strings.Join(parts, " ")
}

// runSettingsSet is `settings set`: find the row, find the layer, check the
// value, then the shared write path (spec §4.2).
func runSettingsSet(cmd *cobra.Command, args []string, o settingsWriteOptions) error {
	if o.add != "" && o.remove != "" {
		return userErr("--add and --remove are two different edits: name one")
	}
	list := o.add != "" || o.remove != ""
	var key, value string
	switch {
	case list && len(args) != 1:
		return userErr("settings set KEY --add V or --remove V takes one key")
	case !list && len(args) != 2:
		return userErr("settings set takes a key and a value")
	default:
		key = args[0]
		if !list {
			value = args[1]
		}
	}
	row, err := settingsWriteRow(key)
	if err != nil {
		return err
	}
	sel, lc, err := settingsSelection()
	if err != nil {
		return err
	}
	target, err := settingsResolveTarget(cmd.Context(), row, o, lc, sel)
	if err != nil {
		return err
	}
	old, err := settingsReadDocument(target)
	if err != nil {
		return err
	}
	mode := "set"
	newData, err := yamledit.SetScalar(old, target.keyPath, value)
	switch {
	case o.add != "":
		mode = "add"
		newData, err = yamledit.AddListItem(old, target.keyPath, o.add)
	case o.remove != "":
		mode = "remove"
		newData, err = yamledit.RemoveListItem(old, target.keyPath, o.remove)
	}
	if err != nil {
		return userErr("%s: %v", key, err)
	}
	if mode == "remove" && settingsListEmpty(newData, target.keyPath) {
		return userErr("removing %s would leave %s an empty list, and an absent key and an empty list mean different things: remove the key with `fugaro settings unset %s --layer %s` instead", quoteWord(o.remove), key, key, target.layer)
	}
	if err := settingsReparse(target, newData); err != nil {
		return err
	}
	if !yamledit.Changed(old, newData) {
		fmt.Fprintf(cmd.OutOrStdout(), "%s: %s already reads what was asked for\n", target.path, key)
		return nil
	}
	typed, why := settingsTypedWrite(key, settingsValueIn(old, target.keyPath), value, mode)
	if err := applySettingsEdit(cmd, lc, settingsEdit{
		Path:       target.path,
		Old:        old,
		New:        newData,
		What:       fmt.Sprintf("write %s in %s", key, target.path),
		Typed:      typed,
		TypedWhy:   why,
		InsideRepo: target.insideRepo,
		Command:    settingsWriteCommand("set", key, value, o, target.layer),
		Yes:        o.yes,
	}); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "next: fugaro update")
	return nil
}

// runSettingsUnset is `settings unset`: remove the key from one layer so the
// layer below decides again, never writing a default in its place (spec §4.3).
func runSettingsUnset(cmd *cobra.Command, args []string, o settingsWriteOptions) error {
	if len(args) != 1 {
		return userErr("settings unset takes one key")
	}
	key := args[0]
	row, err := settingsWriteRow(key)
	if err != nil {
		return err
	}
	sel, lc, err := settingsSelection()
	if err != nil {
		return err
	}
	target, err := settingsResolveTarget(cmd.Context(), row, o, lc, sel)
	if err != nil {
		return err
	}
	old, err := settingsReadDocument(target)
	if err != nil {
		return err
	}
	newData, err := yamledit.RemoveKey(old, target.keyPath)
	if err != nil {
		return userErr("%s: %v", key, err)
	}
	if err := settingsReparse(target, newData); err != nil {
		return err
	}
	if !yamledit.Changed(old, newData) {
		fmt.Fprintf(cmd.OutOrStdout(), "nothing to unset: %s is not set in %s\n", key, target.path)
		return nil
	}
	typed, why := settingsTypedWrite(key, settingsValueIn(old, target.keyPath), "", "unset")
	if err := applySettingsEdit(cmd, lc, settingsEdit{
		Path:       target.path,
		Old:        old,
		New:        newData,
		What:       fmt.Sprintf("remove %s from %s", key, target.path),
		Typed:      typed,
		TypedWhy:   why,
		InsideRepo: target.insideRepo,
		Command:    settingsWriteCommand("unset", key, "", o, target.layer),
		Yes:        o.yes,
	}); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "next: fugaro update")
	return nil
}
