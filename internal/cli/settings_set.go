package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

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

// settingsGenericKeys is the allowlist of spec §4.4: the exact keys `settings
// set` writes, and the whole of them. It is an allowlist and not a list of the
// families to refuse because the registry grows: a key a later task adds is
// refused here until someone decides, deliberately, that the generic path owns
// it, where a denylist would hand every new row to `settings set` by omission.
//
// The names are spec §4.4's. The project layer's own block is spelled with the
// `layer.` prefix (SD14), so §4.4's `default_profile` is `layer.default_profile`
// in the registry and its `defaults.git.*` is `layer.defaults.git.*`; the
// registry's spelling is the one listed, because a name the registry does not
// hold is refused before this list is reached. §4.4's list omits `budget.mode`
// and `budget.per_day_usd`, which the registry holds as scalars of the same
// block the audit (docs/settings-commands/audit.md:81) names as user settings
// and which no family writes; they are here, and nothing else is.
var settingsGenericKeys = map[string]bool{
	"budget.mode":              true,
	"budget.per_run_usd":       true,
	"budget.per_day_usd":       true,
	"budget.max_run_tokens":    true,
	"budget.allowed_models":    true,
	"budget.unreachable_grace": true,
	"base_branch":              true,
	"max_parallel":             true,
	"update_check":             true,
	"commit_name":              true,
	"commit_email":             true,
	"layer.default_profile":    true,
}

// settingsGenericPrefixes are the blocks spec §4.4 hands to the generic path
// whole. A prefix covers the block's scalars and string lists; a key with a
// varying segment is still refused below, so no entry here can reach a map.
// `credentials.` and `clone_urls.` are §4.4's own names for the blocks §5.6's
// family adds: they are listed now so the generic path does not refuse a name
// the spec hands it once that family lands.
var settingsGenericPrefixes = []string{
	"agent.", "git.", "followup.", "governance.", "watch.",
	"layer.defaults.", "layer.governance.",
	"credentials.", "clone_urls.",
}

// settingsGenericKey reports whether spec §4.4 gives the generic path the key.
func settingsGenericKey(key string) bool {
	if settingsGenericKeys[key] {
		return true
	}
	for _, p := range settingsGenericPrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
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
	// project is the selected project's name: a repository's fugaro.yaml is
	// refused when it is anchored to another one.
	project string
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
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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

// settingsWriteRow finds the row a write names and refuses what the generic
// path never writes: a key marked Sensitive, whose value the diff would print;
// a key a dedicated family owns (D853); a key with a varying segment, which is
// a map entry and has no generic form; and every key spec §4.4 does not hand
// the generic path.
//
// The map-entry refusal comes before the allowlist on purpose: §4.2 step 1
// says a map-valued key has no generic form *always*, so `workflows.*.commands.
// test` is refused as a map entry and not as a key nobody listed.
func settingsWriteRow(key string) (settings.Row, error) {
	row, ok := settings.Lookup(key)
	if !ok {
		return settings.Row{}, userErr("%s: no setting has that name; the nearest are %s",
			key, strings.Join(nearestSettings(key, settings.Rows()), ", "))
	}
	if row.Sensitive {
		return settings.Row{}, userErr("%s is a sensitive setting, and the generic path never writes one: its value would be printed in the diff this command shows (spec §2.3)", key)
	}
	if family, ok := settingsFamilyCommands[row.Owner]; ok {
		return settings.Row{}, userErr("%s is written by %s, not by the generic path: settings set writes no route, price, rigor mode, floor, account, job profile, credential or clone URL", key, family)
	}
	if strings.Contains(key, "*") {
		return settings.Row{}, userErr("%s varies with a name this command does not take: a key with a * is a map entry, and a map has no generic form", key)
	}
	if !settingsGenericKey(key) {
		return settings.Row{}, userErr("%s is not a key the generic path writes: settings set writes spec §4.4's settings (the budget scalars, base_branch, max_parallel, update_check, watch.*, commit_name, commit_email, agent.*, git.*, followup.*, governance.*, default_profile, credentials.*, clone_urls.* and the layer's defaults.*), and refuses the installation's own plumbing — an endpoint, a terraform switch, a generation counter, name or runs_bucket — however it is spelled", key)
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
		return settingsTarget{layer: layer, kind: settingsDocLocal, path: sel.Path, keyPath: strings.Split(row.Key, "."), project: lc.Name}, nil
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
			project: lc.Name,
		}, nil
	default:
		path, err := settingsRepoFile(ctx, o.repo, lc.Name)
		if err != nil {
			return settingsTarget{}, err
		}
		return settingsTarget{layer: layer, kind: settingsDocRepo, path: path, keyPath: strings.Split(row.Key, "."), insideRepo: true, project: lc.Name}, nil
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
//
// It reads a checkout's files through config.ReadRegular, the one reader of
// them (readFugaroYAML and readLayerFile use it too), so a symlink, a FIFO or
// an oversized file is refused rather than written through; the local project
// config stays on os.ReadFile, which is how every other reader of it in this
// package reads the person's own file, symlinked dotfile and all. A
// repository's fugaro.yaml is additionally held to the selected project:
// `--layer repo` writes the selected project's own repository, and a checkout
// of another project's repository is a file this command has no business
// editing.
func settingsReadDocument(t settingsTarget) ([]byte, error) {
	var data []byte
	var err error
	if t.kind == settingsDocLocal {
		data, err = os.ReadFile(t.path)
	} else {
		data, err = config.ReadRegular(t.path, config.LayerMaxBytes)
	}
	if err != nil {
		return nil, userErr("reading %s: %v", t.path, err)
	}
	if t.kind == settingsDocRepo {
		cfg, ps := config.Parse(data)
		if len(ps) > 0 {
			return nil, userErr("%s: %s", t.path, ps[0].String())
		}
		if cfg.Project != t.project {
			return nil, userErr("%s is anchored to project %s, and %s is the selected project: --layer repo writes the selected project's own repository, so nothing was written",
				t.path, cfg.Project, t.project)
		}
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
	case []any:
		// A sequence is the model list, and it is rendered the one way the
		// widening check reads a list — comma-separated members, which is what
		// settingsListSubset splits on. fmt.Sprint's "[a b]" is a form no
		// parser here reads: a comparison against it would call every member
		// absent and so call every write a widening.
		items := make([]string, 0, len(v))
		for _, item := range v {
			items = append(items, fmt.Sprint(item))
		}
		return strings.Join(items, ",")
	default:
		return fmt.Sprint(v)
	}
}

// settingsListGoneOrEmpty reports whether the node at path is now a sequence
// with no items, or is gone: the two shapes the loader tells from a written
// list, and the two an edit that removes the last item can leave behind. The
// caller checks it only when the edit changed the document, so a key that was
// never there and stayed away is not mistaken for an emptied list.
func settingsListGoneOrEmpty(data []byte, path []string) bool {
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
			return true
		}
	}
	if cur == nil {
		// The key stands with nothing after it, which is what the signature
		// writes for a sequence it emptied.
		return true
	}
	s, ok := cur.([]any)
	return ok && len(s) == 0
}

// settingsCeilingKey is the prefix test D846 names: budget.* and governance.*
// are the keys whose loosening crosses the boundary the typed name guards.
// The project layer's own block is the same setting under the `layer.` prefix
// (SD14), so `layer.governance.review_ratio` is typed exactly as
// `governance.review_ratio` is: the file it would edit decides the ceiling of
// every run the project's branches start, which is the fact D846 guards.
func settingsCeilingKey(key string) bool {
	key = strings.TrimPrefix(key, "layer.")
	return strings.HasPrefix(key, "budget.") || strings.HasPrefix(key, "governance.")
}

// settingsTrue is the value a followup key is turned on with: a YAML boolean
// in any of the spellings the config decoder reads as true (`true`, `True`,
// `TRUE`, `yes`, `on`, and the rest), not the one string the command line
// happens to use. It is the decoder's own answer rather than a second table,
// so a spelling the loader reads as true is one this check reads as true.
func settingsTrue(value string) bool {
	var b bool
	return yaml.Unmarshal([]byte(value), &b) == nil && b
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

// settingsCeilingBase is the value a ceiling write is compared against (D846):
// the tighter of what the file holds and what a run gets today, which the
// read's own resolver answers — the shared or published node, the project
// layer's defaults and the local file, whichever decides.
//
// The file's raw value alone is not enough. A first write of a ceiling a layer
// below already set tighter raises what a run gets, and a comparison against
// the raw value would read it as a first write and skip the typed name the
// widening asks for. The effective value alone is not enough either, for the
// mirror reason: a file already tighter than the layer below is loosened by a
// write that does not move what a run gets today, and that file is what a
// later layer inherits. A run gets the tighter of the two, so the tighter of
// the two is what a loosening is measured against. budget.go's clear compares
// the effective caps the same way, for the same reason.
func settingsCeilingBase(ctx context.Context, row settings.Row, raw string) string {
	effective := settingsEffectiveCeiling(ctx, row)
	switch {
	case raw == "":
		// The file holds no ceiling of its own: the layer below is the whole
		// of what a run gets.
		return effective
	case effective == "":
		return raw
	case settingsLoosens(row.Key, effective, raw):
		// raw is the looser of the two, so the effective value is the one a
		// run gets.
		return effective
	default:
		return raw
	}
}

// settingsEffectiveCeiling is the value a run gets for row from the read's own
// resolver, "" when no layer resolves one. The read is the offline one, so a
// command that edits a file never waits on a bucket or a database for it.
//
// That read is enough for the decision it feeds. The merge only tightens
// (internal/policy), so a layer it does not read can hold a value tighter than
// the one answered here and never a looser one: what it returns is at or above
// the ceiling a run really gets. The layers that would matter are read anyway —
// the cached copy of the published project layer is readLayerCache's, and a
// repository's fugaro.yaml tightens only that repository's own runs, so another
// repository's file never decides the file being edited. The direction of the
// residual blindness is the safe one: the caller builds its base from the
// tighter of this value and the file's own, so the base is never looser than
// the file, and a write that raises the file's ceiling is typed whatever this
// read could not see.
func settingsEffectiveCeiling(ctx context.Context, row settings.Row) string {
	r, err := readSettings(ctx, settingsOptions{offline: true}, time.Now())
	if err != nil {
		return ""
	}
	if s := r.projectRow(row); s.Wins != settings.LayerDefault {
		return s.Value
	}
	return ""
}

// settingsLoosens reports whether newValue is looser than oldValue for a
// ceiling key: a larger cap, a weaker mode, or a wider model list. A key no
// layer sets has nothing to loosen, so a first write is never the typed form.
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
// project's own hand, and the sentence saying why. mode is the edit being made:
// set, unset, add or remove. `remove` takes one member out of a list, which
// narrows it, so it is never the typed form and has no case below; `unset`
// always is, for a ceiling, because it hands the decision to a looser layer.
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
	if strings.HasPrefix(key, "followup.") && mode == "set" && settingsTrue(newValue) {
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
	var newData []byte
	switch {
	case o.add != "":
		mode = "add"
		newData, err = yamledit.AddListItem(old, target.keyPath, o.add)
	case o.remove != "":
		mode = "remove"
		newData, err = yamledit.RemoveListItem(old, target.keyPath, o.remove)
	default:
		newData, err = yamledit.SetScalar(old, target.keyPath, value)
	}
	if err != nil {
		return userErr("%s: %v", key, err)
	}
	changed := yamledit.Changed(old, newData)
	if !changed {
		// Nothing to write and nothing to hold against the loader: a
		// document this Fugaro cannot read back is not this edit's doing,
		// and a no-op that refused on it would report the file's own state
		// as the fault of a command that was asked for no change at all.
		fmt.Fprintf(cmd.OutOrStdout(), "%s: %s already reads what was asked for\n", target.path, key)
		return nil
	}
	if mode == "remove" && settingsListGoneOrEmpty(newData, target.keyPath) {
		return userErr("removing %s would leave %s an empty list, and an absent key and an empty list mean different things: remove the key with `fugaro settings unset %s --layer %s` instead", quoteWord(o.remove), key, key, target.layer)
	}
	if err := settingsReparse(target, newData); err != nil {
		return err
	}
	oldValue := settingsValueIn(old, target.keyPath)
	if settingsCeilingKey(key) {
		oldValue = settingsCeilingBase(cmd.Context(), row, oldValue)
	}
	typed, why := settingsTypedWrite(key, oldValue, value, mode)
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
	if !yamledit.Changed(old, newData) {
		// The same order as the set path: a key that is not there is not a
		// write, so a document that does not read back is reported when a
		// write is actually asked for, not when nothing would change.
		fmt.Fprintf(cmd.OutOrStdout(), "nothing to unset: %s is not set in %s\n", key, target.path)
		return nil
	}
	if err := settingsReparse(target, newData); err != nil {
		return err
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
