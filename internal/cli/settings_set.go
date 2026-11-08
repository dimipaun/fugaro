package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/safetext"
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
	"workspace":   "fugaro workspace",
}

// settingsGenericKeys is the allowlist of spec §4.4: the exact keys `settings
// set` writes, and the whole of them. It is an allowlist and not a list of the
// families to refuse because the registry grows: a key a later task adds is
// refused here until someone decides, deliberately, that the generic path owns
// it, where a denylist would hand every new row to `settings set` by omission.
//
// The names are spec §4.4's, and where they are not, the decision that put
// them here is named beside them. The project layer's own block is spelled with
// the `layer.` prefix (SD14), so §4.4's `default_profile` is
// `layer.default_profile` in the registry and its `defaults.git.*` is
// `layer.defaults.git.*`; the registry's spelling is the one listed, because a
// name the registry does not hold is refused before this list is reached.
//
// The four budget keys are one block and the generic path's whole (D1179):
// §4.4's list names two of them, the audit (docs/settings-commands/audit.md:81)
// finds the other two are user settings too, and until D1179 this list refused
// them on purpose — a key the read prints, the local config writes and no
// command owned. What `fugaro budget set` writes is the budget **database**'s
// caps under its own ETag-guarded write; it owns no registry row, so naming it
// the owner of these four would refuse two keys this path writes today.
//
// The wildcard blocks this design manages are listed by their registry pattern
// (D1161). A person writes `workflows.web.commands.test`; settingsWriteLookup
// matches it against the `workflows.*.commands.test` row below and keeps the
// key as written, which is the path the edit lands on and the name the document
// and the diff carry. Nothing enumerates a wildcard block's concrete spellings,
// which is what lets one written key create a block the file does not hold —
// D1161's stated cost, and the reason the layer a write lands in is still the
// row's own Layers.
var settingsGenericKeys = map[string]bool{
	"budget.per_run_usd":       true,
	"budget.max_run_tokens":    true,
	"budget.allowed_models":    true,
	"budget.unreachable_grace": true,
	"budget.mode":              true,
	"budget.per_day_usd":       true,
	"base_branch":              true,
	"max_parallel":             true,
	"update_check":             true,
	"commit_name":              true,
	"commit_email":             true,
	"layer.default_profile":    true,
	// D873's account choice (spec §11.1). They are here by name because they
	// are not §4.4's list: §4.4 predates them, and a registry row no list
	// carries is a key nobody can write.
	"default_claude_account":  true,
	"claude_account_auto":     true,
	"stages.*.claude_account": true,
	// A repository's workflows.* (37 rows) and the project layer's profiles.*
	// (36), in the registry's own order.
	"workflows.*.profile":                            true,
	"workflows.*.job_profile":                        true,
	"workflows.*.base":                               true,
	"workflows.*.checkout":                           true,
	"workflows.*.image.node":                         true,
	"workflows.*.image.jdk":                          true,
	"workflows.*.image.apt":                          true,
	"workflows.*.image.setup":                        true,
	"workflows.*.image.skip_build_scripts":           true,
	"workflows.*.image.tools":                        true,
	"workflows.*.image.key_files":                    true,
	"workflows.*.dockerfile":                         true,
	"workflows.*.commands.build":                     true,
	"workflows.*.commands.test":                      true,
	"workflows.*.commands.lint":                      true,
	"workflows.*.commands.fix":                       true,
	"workflows.*.commands.render":                    true,
	"workflows.*.commands.probe":                     true,
	"workflows.*.commands.rerun_failed":              true,
	"workflows.*.commands.reports":                   true,
	"workflows.*.cache":                              true,
	"workflows.*.secrets":                            true,
	"workflows.*.resources.cpu":                      true,
	"workflows.*.resources.memory":                   true,
	"workflows.*.timeouts.total":                     true,
	"workflows.*.timeouts.stage":                     true,
	"workflows.*.timeouts.verify":                    true,
	"workflows.*.timeouts.render":                    true,
	"workflows.*.timeouts.finalize_reserve":          true,
	"workflows.*.rebuild.check":                      true,
	"workflows.*.rebuild.max_age":                    true,
	"workflows.*.rebuild.lockfiles":                  true,
	"workflows.*.rebuild.base":                       true,
	"workflows.*.rebuild.paths":                      true,
	"workflows.*.inputs.max_files":                   true,
	"workflows.*.inputs.max_bytes":                   true,
	"workflows.*.inputs.max_total_bytes":             true,
	"layer.profiles.*.description":                   true,
	"layer.profiles.*.base":                          true,
	"layer.profiles.*.image.node":                    true,
	"layer.profiles.*.image.jdk":                     true,
	"layer.profiles.*.image.apt":                     true,
	"layer.profiles.*.image.setup":                   true,
	"layer.profiles.*.image.skip_build_scripts":      true,
	"layer.profiles.*.image.tools":                   true,
	"layer.profiles.*.image.key_files":               true,
	"layer.profiles.*.commands.build":                true,
	"layer.profiles.*.commands.test":                 true,
	"layer.profiles.*.commands.lint":                 true,
	"layer.profiles.*.commands.fix":                  true,
	"layer.profiles.*.commands.render":               true,
	"layer.profiles.*.commands.probe":                true,
	"layer.profiles.*.commands.rerun_failed.command": true,
	"layer.profiles.*.commands.rerun_failed.each":    true,
	"layer.profiles.*.commands.reports":              true,
	"layer.profiles.*.cache.*.key":                   true,
	"layer.profiles.*.cache.*.paths":                 true,
	"layer.profiles.*.resources.cpu":                 true,
	"layer.profiles.*.resources.memory":              true,
	"layer.profiles.*.timeouts.total":                true,
	"layer.profiles.*.timeouts.stage":                true,
	"layer.profiles.*.timeouts.verify":               true,
	"layer.profiles.*.timeouts.render":               true,
	"layer.profiles.*.timeouts.finalize_reserve":     true,
	"layer.profiles.*.rebuild.check":                 true,
	"layer.profiles.*.rebuild.max_age":               true,
	"layer.profiles.*.rebuild.lockfiles":             true,
	"layer.profiles.*.rebuild.base":                  true,
	"layer.profiles.*.rebuild.paths":                 true,
	"layer.profiles.*.inputs.max_files":              true,
	"layer.profiles.*.inputs.max_bytes":              true,
	"layer.profiles.*.inputs.max_total_bytes":        true,
	"layer.profiles.*.checkout":                      true,
	// The layer's ab.* is the third block D1161 names. Its rows are the models
	// family's today, and the family refusal is asked before this list, so
	// listing them changes no answer: they are here because the block is this
	// design's to write once the owner column names the generic writer (D1178),
	// and a block listed in two places is the drift D1161's own cost names.
	"layer.ab.rate":              true,
	"layer.ab.max_pairs_per_day": true,
	"layer.ab.challengers":       true,
	"layer.ab.judge_model":       true,
	"layer.ab.exchange_rate":     true,
}

// settingsGenericPrefixes are the blocks spec §4.4 hands to the generic path
// whole. A prefix covers the block's scalars and string lists: no entry here
// carries a varying segment, and settingsWriteLookup reaches a wildcard
// registry row only for a spelling settingsGenericKeys names, so a prefix is
// never the reason a map entry resolves.
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
	project string
	config  string
	layer   string
	repo    string
	yes     bool
	// ask is --ask, the symmetric half of --yes (the owner's rule of
	// 2026-11-04): in a project whose marks say it is managed, where this
	// command would ask nothing, it asks the write's own question instead — and
	// never one for a widening. Unmanaged, it says so and changes nothing;
	// with no terminal, in a managed project, it refuses rather than proceeds
	// (managed_ask.go is the whole of it). It is refused beside --yes.
	ask    bool
	add    string
	remove string
	// addEntry and removeEntry are the entry pair (D1162). Each --add-entry
	// occurrence is one `field=value` of the one entry the command writes, and
	// --remove-entry takes the one value that addresses the entry: a map
	// member's name, or an object list's key field value.
	addEntry    []string
	removeEntry []string
	dryRun      bool
	jsonOut     bool
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
	layer   string
	kind    settingsDocKind
	path    string
	keyPath []string
	anchor  config.LayerAnchor
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
	settingsAimFlags(cmd, &o.project, &o.config)
	f.StringVar(&o.repo, "repo", "", "read this repository's fugaro.yaml (owner/name, or a path inside its checkout)")
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

A string-list key takes --add V or --remove V instead of a value. A map or an
object list takes --add-entry and --remove-entry instead: --add-entry carries
one entry, one field=value per member the entry has, and --remove-entry carries
the one value the entry is addressed by — a map member's name, or an object
list's key field value, which is how an entry of an object list is named and
never its position. One entry per command: several entries are several
invocations. A map's entry is one member, so --add-entry edits the members of a
map the file already holds, and a map the loader wants whole cannot be built
one member at a time. A list emptied by --remove or --remove-entry is refused:
an absent key and an empty list mean different things to the loader, so use
unset to remove the key.

Loosening a ceiling key (budget.*, governance.*, the agent caps
agent.max_budget_usd and agent.max_run_tokens with the layer's own spellings),
adding a name to a followup.* list or turning one of those keys on, is a
widening, and a widening is the owner's own step: nothing is asked for it at
your own terminal, and a coding agent's session is refused it with the command
to run yourself (D1552). Writing outside a repository is refused in a coding
agent's session, --yes included: --yes never lifts that fence.

--dry-run says what the edit would do — the key, the layer, the file, the old
and the new value, and whether it raises a limit — and writes nothing: no
confirmation, no backup, no change to any file. It is answered with no
terminal, and with --json it prints the versioned document
schemas/settings-write.schema.json describes, which is what a caller that draws
an edit reads. --dry-run takes neither --yes (the two answer different
questions) nor a bare --json.

Every write ends with one line: next: fugaro update.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSettingsSet(cmd, args, o)
		},
	}
	f := cmd.Flags()
	settingsAimFlags(cmd, &o.project, &o.config)
	f.StringVar(&o.layer, "layer", "", "the layer to write ("+strings.Join(settingsWriteLayers, ", ")+")")
	f.StringVar(&o.repo, "repo", "", "the repository whose fugaro.yaml to edit (owner/name, or a path inside its checkout)")
	f.BoolVar(&o.yes, "yes", false, "confirm the write without a prompt")
	f.BoolVar(&o.ask, "ask", false, "in a project whose marks say it is managed, ask the confirmation this write would otherwise get (the y/N) instead of writing on the gate's verdict: a managed project asks nothing by default, and this is the flag that puts the question back — a widening is the owner's own step and is asked nothing with or without it (D1552). Refused where nobody can answer them (a coding agent's session, a pipe, --non-interactive); a no-op with a sentence in a project that is not managed; refused beside --yes")
	f.StringVar(&o.add, "add", "", "add an item to a string-list key")
	f.StringVar(&o.remove, "remove", "", "remove an item from a string-list key")
	f.StringArrayVar(&o.addEntry, "add-entry", nil, "add one entry to a map or an object list (field=value, once per member the entry has)")
	f.StringArrayVar(&o.removeEntry, "remove-entry", nil, "remove one entry from a map or an object list (the member's name, or the key field's value)")
	f.BoolVar(&o.dryRun, "dry-run", false, "print what the edit would do and write nothing")
	f.BoolVar(&o.jsonOut, "json", false, "print the dry run's document (with --dry-run)")
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

Removing a ceiling key is the owner's own step: nothing is asked for it at your
own terminal, and a coding agent's session is refused it, with the command to
run yourself (D1552).

--dry-run says what the removal would do — the key, the layer, the file, the
value it takes away, and whether it raises a limit — and writes nothing: no
confirmation, no backup, no change to any file. It is answered with no
terminal, and with --json it prints the versioned document
schemas/settings-write.schema.json describes. --dry-run takes neither --yes
(the two answer different questions) nor a bare --json.

Every write ends with one line: next: fugaro update.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSettingsUnset(cmd, args, o)
		},
	}
	f := cmd.Flags()
	settingsAimFlags(cmd, &o.project, &o.config)
	f.StringVar(&o.layer, "layer", "", "the layer to write ("+strings.Join(settingsWriteLayers, ", ")+")")
	f.StringVar(&o.repo, "repo", "", "the repository whose fugaro.yaml to edit (owner/name, or a path inside its checkout)")
	f.BoolVar(&o.yes, "yes", false, "confirm the write without a prompt")
	f.BoolVar(&o.ask, "ask", false, "in a project whose marks say it is managed, ask the confirmation this removal would otherwise get instead of writing on the gate's verdict: a managed project asks nothing by default, and this is the flag that puts the question back. Refused where nobody can answer it (a coding agent's session, a pipe, --non-interactive); a no-op with a sentence in a project that is not managed; refused beside --yes, --dry-run and --json")
	f.BoolVar(&o.dryRun, "dry-run", false, "print what the edit would do and write nothing")
	f.BoolVar(&o.jsonOut, "json", false, "print the dry run's document (with --dry-run)")
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
	return settingsWriteRowHinted(key, false)
}

// settingsWriteListRow is settingsWriteRow for an --add or a --remove: the same
// row and the same refusals, and a hint narrowed to the keys that take a list,
// because a scalar answered `--add` with itself is a name the person has to
// come back from.
func settingsWriteListRow(key string) (settings.Row, error) {
	return settingsWriteRowHinted(key, true)
}

// settingsWriteRowHinted is settingsWriteRow with list saying whether the write
// is an --add or a --remove. list reaches no refusal but the unknown-key one:
// which rows this path writes is the same question either way.
func settingsWriteRowHinted(key string, list bool) (settings.Row, error) {
	row, registry, ok := settingsWriteLookup(key)
	if !ok {
		return settings.Row{}, userErr("%s: no setting has that name; the nearest are %s",
			key, strings.Join(nearestSettings(key, settingsWriteHintRows(list)), ", "))
	}
	if row.Sensitive {
		return settings.Row{}, userErr("%s is a sensitive setting, and the generic path never writes one: its value would be printed in the diff this command shows (spec §2.3)", key)
	}
	if family, ok := settingsFamilyCommands[row.Owner]; ok {
		return settings.Row{}, userErr("%s is written by %s, not by the generic path: settings set writes no route, price, rigor mode, floor, account, job profile, credential, clone URL or workspace key", key, family)
	}
	if strings.Contains(key, "*") {
		return settings.Row{}, userErr("%s varies with a name this command does not take: a key with a * is a map entry, and a map has no generic form", key)
	}
	if !settingsGenericKey(registry) {
		return settings.Row{}, userErr("%s is not a key the generic path writes: settings set writes spec §4.4's settings (the budget scalars, base_branch, max_parallel, update_check, watch.*, commit_name, commit_email, agent.*, git.*, followup.*, governance.*, default_profile, credentials.*, clone_urls.* and the layer's defaults.*), the account choice (default_claude_account, claude_account_auto, stages.<stage>.claude_account) and a repository's workflows.<name>.* and the project layer's profiles.<name>.*, and refuses the installation's own plumbing — an endpoint, a terraform switch, a generation counter, name or runs_bucket — however it is spelled", key)
	}
	return row, nil
}

// settingsWriteLookup is the row a write names, with the registry's own
// spelling of that key beside it. The two differ for a key whose registry
// spelling carries a varying segment: `stages.*.claude_account` is the row, and
// `stages.review.claude_account` is the key a person writes. The row keeps the
// key as written, because that is the path the edit lands on, and the registry
// spelling is what the allowlist and the row's own sentences name.
//
// Only a wildcard row the allowlist carries is matched, which is how
// `stages.*.claude_account` and the blocks D1161 hands this path — a
// repository's `workflows.*.*` and the project layer's `profiles.*.*` — are
// reachable at all. The registry's other wildcard rows — the project family's
// `repos.*.*`, the models family's `providers.*.*` and `model_prices.*.*`, the
// rigor family's `layer.defaults.rigor.areas.*.*` and the accounts family's
// `claude_accounts.*.allow_data_to` — are other paths' keys: matching one would
// hand back a row this command does not write, and the refusal a person gets
// should be the one about the name, not a row that was never theirs.
func settingsWriteLookup(key string) (settings.Row, string, bool) {
	if row, found := settings.Lookup(key); found {
		return row, key, true
	}
	for _, r := range settings.Rows() {
		if !settingsGenericKeys[r.Key] {
			continue
		}
		if !settingsPatternMatch(r.Key, key) {
			continue
		}
		registry := r.Key
		r.Key = key
		return r, registry, true
	}
	return settings.Row{}, "", false
}

// settingsPatternMatch reports whether key is a spelling of the registry key
// pattern: the same number of dotted segments, equal where the pattern names
// one, and non-empty where it writes *. It answers the spelling and nothing
// else: which patterns may be matched at all is settingsWriteLookup's, and
// that is where the generic allowlist is asked.
func settingsPatternMatch(pattern, key string) bool {
	p, k := strings.Split(pattern, "."), strings.Split(key, ".")
	if len(p) != len(k) {
		return false
	}
	for i := range p {
		switch {
		case p[i] == "*":
			if k[i] == "" {
				return false
			}
		case p[i] != k[i]:
			return false
		}
	}
	return true
}

// settingsHintRow reports whether a write's hint may name row: the rows
// settingsWriteRow accepts, and the whole of them. The read's own hint names
// every registry row, which is right there — a read covers the whole registry —
// but a write covers the rows this command writes, and a hint drawn from
// anything wider sends a person from one refusal to the next. Spec §4.4 puts it
// as "never with a registry row this path refuses", and the allowlist is not
// the whole of that set: it carries blocks whole, so `agent.model` reaches a
// hint through the `agent.` prefix while the models family owns the row and
// `settings set` refuses it. A Sensitive row is refused here for a reason of
// its own — the diff a write shows would print its value — and a key with a
// varying segment is a map entry with no generic form.
//
// The refusals are settingsWriteRow's own, asked in its own order, so the two
// cannot disagree about what this path writes.
func settingsHintRow(row settings.Row) bool {
	if row.Sensitive {
		return false
	}
	if _, family := settingsFamilyCommands[row.Owner]; family {
		return false
	}
	if strings.Contains(row.Key, "*") {
		return false
	}
	return settingsGenericKey(row.Key)
}

// settingsHintListKeys are the string-list keys of the accept set: the rows
// `--add` and `--remove` edit, and so the only rows a hint drawn for those two
// flags may name. The registry carries no key type, so the list stands beside
// the refusal that needs it, and TestSettingsAddHintListKeysAreTheKeysThatTakeAList
// probes every row the write path accepts and holds this table to the
// documents' own loaders: a list key added later fails there, where a stale
// table would leave the hint naming keys --add cannot edit.
var settingsHintListKeys = map[string]bool{
	"budget.allowed_models":        true, // config.Budget.AllowedModels
	"followup.trusted":             true, // config.Followup.Trusted
	"git.pr.labels":                true, // config.PRSettings.Labels
	"git.pr.reviewers":             true, // config.PRSettings.Reviewers
	"layer.defaults.git.pr.labels": true, // config.LayerPR.Labels
}

// settingsWriteHintRows are the registry rows a write's unknown-key refusal may
// name as its nearest: the accept set, narrowed to the list keys when list says
// the write is an --add or a --remove.
func settingsWriteHintRows(list bool) []settings.Row {
	rows := settings.Rows()
	out := make([]settings.Row, 0, len(rows))
	for _, row := range rows {
		if !settingsHintRow(row) {
			continue
		}
		if list && !settingsHintListKeys[row.Key] {
			continue
		}
		out = append(out, row)
	}
	return out
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

// settingsSelection selects this machine's project, the way the read does: the
// checkout's fugaro.yaml names it (localcfg.Select's step 3) where nothing
// before that does, so a machine with several project configs needs no
// --project inside a checkout.
func settingsSelection() (localcfg.Selection, *localcfg.Config, error) {
	return settingsAimedSelection(settingsWriteOptions{})
}

// settingsAimedSelection is settingsSelection for the commands that can aim the
// selection themselves: --config and --project are localcfg.Select's steps 1 and
// 2, so a write aimed at an installation lands in that installation's file
// rather than in whichever one the machine's environment would have picked. The
// families that call settingsSelection (models, accounts, secrets) take neither
// flag, and resolve as before except that, where several project configs leave
// the choice open, they too read the checkout (selectSettingsProject).
func settingsAimedSelection(o settingsWriteOptions) (localcfg.Selection, *localcfg.Config, error) {
	in := localcfg.SelectInput{
		Config:     o.config,
		Project:    o.project,
		EnvProject: os.Getenv("FUGARO_PROJECT"),
		EnvConfig:  os.Getenv("FUGARO_CONFIG"),
		Getenv:     os.Getenv,
	}
	sel, lc, err := selectSettingsProject(context.Background(), in)
	if err != nil {
		return sel, nil, userErr("%w", err)
	}
	if lc == nil || lc.Name == "" {
		return sel, lc, userErr("no project is selected: set $FUGARO_PROJECT or $FUGARO_CONFIG, or run fugaro setup")
	}
	return sel, lc, nil
}

// selectSettingsProject is localcfg.Select, and, where several project configs
// leave the choice open, Select again with the checkout the command runs in:
// its fugaro.yaml names its project (step 3), which is what the refusal
// "several project configs and nothing selects one" tells a person to do. The
// checkout is not asked first, so a checkout of another project still resolves
// to the machine's only project config as these commands always did.
func selectSettingsProject(ctx context.Context, in localcfg.SelectInput) (localcfg.Selection, *localcfg.Config, error) {
	sel, lc, err := localcfg.Select(in)
	var se *localcfg.SelectError
	if err == nil || !errors.As(err, &se) || len(se.Projects) < 2 {
		return sel, lc, err
	}
	co, cerr := resolveCheckout(ctx, "")
	if cerr != nil || co == nil {
		return sel, lc, err
	}
	in.Checkout = co
	in.Origin = func() (string, string) { return checkoutOriginAt("") }
	return localcfg.Select(in)
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
		return settingsTarget{layer: layer, kind: settingsDocRepo, path: path, keyPath: strings.Split(row.Key, "."), project: lc.Name}, nil
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

// settingsEntryKind is the shape of a block --add-entry and --remove-entry
// write: a map, whose members no registry leaf names, or an object list, whose
// entries are mappings.
type settingsEntryKind int

const (
	settingsEntryMap settingsEntryKind = iota
	settingsEntryList
)

// settingsEntryBlock is one row of the table D1163 leaves to this task: how an
// entry of that block is addressed, and whether growing it crosses the boundary
// D846 guards.
type settingsEntryBlock struct {
	kind settingsEntryKind
	// keyField is the field an object-list entry is addressed by (D1163). It is
	// empty for a block whose entries carry no name this pair can address, and
	// that block is refused rather than addressed by position: a position is a
	// fact about the file's own order and not the person's intent.
	keyField string
	// widens says an entry added here hands a run something it did not reach
	// before. A workflow's secrets are mounted into the job's environment, so
	// adding one is the widening D1165 classifies as the typed write.
	widens bool
}

// settingsEntryBlocks is that table, keyed by the registry's own spelling of
// the block. The registry carries no shape, so the table stands beside the
// writes that read it, and it is deliberately short: it is the blocks a
// repository's fugaro.yaml holds whose entries can be addressed at all.
//
// A cache entry's key field is `key` (D1163, spec §2.2), and its value is a
// list of files rather than a scalar — so the keyed lookup this pair writes
// through, which compares the key field's own text, could never find one of
// them, and the row carries no key field. That is a deviation from D1163 and
// from spec §2.2's list, which both name the block as one the pair reaches: an
// entry of it is refused with a sentence naming what D1163 decides and why
// this pair cannot write by it. Addressing one by position is what D1163
// refuses, so nothing else is left.
var settingsEntryBlocks = map[string]settingsEntryBlock{
	"workflows.*.commands.rerun_failed": {kind: settingsEntryMap},
	"workflows.*.secrets":               {kind: settingsEntryList, keyField: "name", widens: true},
	"workflows.*.cache":                 {kind: settingsEntryList},
}

// settingsEntryBlockOrder is the table's own order, and the lookup walks it
// rather than the map: two patterns that matched one key would otherwise make
// the block whichever row the map happened to yield first, and a table read at
// random is not a table.
var settingsEntryBlockOrder = []string{
	"workflows.*.commands.rerun_failed",
	"workflows.*.secrets",
	"workflows.*.cache",
}

// settingsEntryBlockOf is the block a key names, matched the way
// settingsWriteLookup matches the registry's rows: the block's own pattern
// against the key as the command line spelled it, so `workflows.web.secrets`
// reaches the `workflows.*.secrets` row.
func settingsEntryBlockOf(key string) (settingsEntryBlock, bool) {
	for _, pattern := range settingsEntryBlockOrder {
		if settingsPatternMatch(pattern, key) {
			return settingsEntryBlocks[pattern], true
		}
	}
	return settingsEntryBlock{}, false
}

// settingsEntryAddr is the entry an entry write is about, in the old bytes and
// in the new ones alike: the block's path, the field the entry is addressed by
// — empty for a map, whose entry is the one member the write names — and that
// field's value.
type settingsEntryAddr struct {
	path     []string
	keyField string
	keyValue string
}

// settingsEntryIn renders the entry an address names as the entry's own
// members, one `field=value` pair each, sorted by field, and "" when the
// document holds no such entry. It is where an entry write's old and new values
// are read: the entry is what the edit is about and the block around it is not,
// and a value read at the block would print a whole sequence as the change. The
// rendering is for a person and for a window — nothing here compares it, so
// members that are not scalars are printed the way fmt.Sprint prints them.
func settingsEntryIn(data []byte, addr settingsEntryAddr) string {
	var tree map[string]any
	if yaml.Unmarshal(data, &tree) != nil {
		return ""
	}
	var cur any = tree
	for _, p := range addr.path {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur, ok = m[p]
		if !ok {
			return ""
		}
	}
	if addr.keyField == "" {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		v, ok := m[addr.keyValue]
		if !ok {
			return ""
		}
		return addr.keyValue + "=" + fmt.Sprint(v)
	}
	items, ok := cur.([]any)
	if !ok {
		return ""
	}
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok || fmt.Sprint(m[addr.keyField]) != addr.keyValue {
			continue
		}
		fields := make([]string, 0, len(m))
		for field := range m {
			fields = append(fields, field)
		}
		slices.Sort(fields)
		pairs := make([]string, 0, len(fields))
		for _, field := range fields {
			pairs = append(pairs, field+"="+fmt.Sprint(m[field]))
		}
		return strings.Join(pairs, ", ")
	}
	return ""
}

// settingsEntryWrite is one entry edit: the bytes it built, the address the
// entry has in them — so the caller reads the entry's own value from each side
// of the edit — and what the dry run's document says about the entry (D1164):
// the operation that wrote it, the field it is addressed by, that field's
// value, and the members the entry carries beside it.
type settingsEntryWrite struct {
	data        []byte
	addr        settingsEntryAddr
	operation   string
	entryKey    string
	entryValue  string
	entryFields map[string]string
}

// settingsEntryAdd applies one --add-entry: the fields the flags carried, on the
// block the row names (D1162). One entry per invocation (D1167) — a map's entry
// is one member, and an object list's is one mapping, addressed by its key field
// and created when the sequence does not hold it yet — and the entry is
// classified afterwards exactly as a scalar write is, by the shared write path.
func settingsEntryAdd(old []byte, keyPath []string, block settingsEntryBlock, pairs []string) (settingsEntryWrite, error) {
	fields := make([][2]string, 0, len(pairs))
	named := map[string]int{}
	for _, pair := range pairs {
		field, value, ok := strings.Cut(pair, "=")
		if !ok || field == "" {
			return settingsEntryWrite{}, userErr("--add-entry %s: an entry is written as field=value", pair)
		}
		fields = append(fields, [2]string{field, value})
		named[field]++
	}
	if block.kind == settingsEntryMap {
		if len(fields) != 1 {
			return settingsEntryWrite{}, userErr("--add-entry carries one entry, and a map's entry is one member: name one field=value per command")
		}
		data, err := yamledit.SetScalar(old, append(slices.Clone(keyPath), fields[0][0]), fields[0][1])
		if err != nil {
			return settingsEntryWrite{}, err
		}
		return settingsEntryWrite{
			data:       data,
			addr:       settingsEntryAddr{path: keyPath, keyValue: fields[0][0]},
			operation:  "add-entry",
			entryKey:   fields[0][0],
			entryValue: fields[0][1],
		}, nil
	}
	switch {
	case named[block.keyField] == 0:
		return settingsEntryWrite{}, userErr("--add-entry carries one entry, and an entry of this block is one the %s field names: name one %s=value", block.keyField, block.keyField)
	case named[block.keyField] > 1:
		return settingsEntryWrite{}, userErr("--add-entry carries one entry: %s is the field that names it, and two of them are two entries", block.keyField)
	}
	members := make(map[string]string, len(fields))
	keyValue := ""
	for _, field := range fields {
		if field[0] == block.keyField {
			keyValue = field[1]
			continue
		}
		if named[field[0]] > 1 {
			// Two occurrences of one member are two answers to one question,
			// and a map of them would write the second and drop the first
			// without saying so.
			return settingsEntryWrite{}, userErr("--add-entry names %s twice, and one entry carries one %s: name it once", field[0], field[0])
		}
		members[field[0]] = field[1]
	}
	if len(members) == 0 {
		// The upsert writes the members it is handed and nothing else, so an
		// entry carrying none of them would leave the document as it was and
		// the write would report that the file already reads what was asked
		// for — of an entry the file may not hold at all.
		return settingsEntryWrite{}, userErr("--add-entry takes the entry's own members beside the field that names it: name one field=value besides %s", block.keyField)
	}
	data, err := yamledit.UpsertSeqItemByKey(old, keyPath, block.keyField, keyValue, members)
	if err != nil {
		return settingsEntryWrite{}, err
	}
	return settingsEntryWrite{
		data:        data,
		addr:        settingsEntryAddr{path: keyPath, keyField: block.keyField, keyValue: keyValue},
		operation:   "add-entry",
		entryKey:    block.keyField,
		entryValue:  keyValue,
		entryFields: members,
	}, nil
}

// settingsEntryRemove applies one --remove-entry: the entry the value addresses
// leaves the block, whatever else it carried. A map's entry is the member that
// value names, and an object list's is the one entry whose key field carries it
// — the first of them when the file holds the name twice, which is the editor's
// own rule and one entry per invocation (D1167).
func settingsEntryRemove(old []byte, keyPath []string, block settingsEntryBlock, value string) (settingsEntryWrite, error) {
	if block.kind == settingsEntryMap {
		data, err := yamledit.RemoveKey(old, append(slices.Clone(keyPath), value))
		if err != nil {
			return settingsEntryWrite{}, err
		}
		return settingsEntryWrite{
			data:      data,
			addr:      settingsEntryAddr{path: keyPath, keyValue: value},
			operation: "remove-entry",
			entryKey:  value,
		}, nil
	}
	data, err := yamledit.RemoveSeqItemByKey(old, keyPath, block.keyField, value)
	if err != nil {
		return settingsEntryWrite{}, err
	}
	return settingsEntryWrite{
		data:       data,
		addr:       settingsEntryAddr{path: keyPath, keyField: block.keyField, keyValue: value},
		operation:  "remove-entry",
		entryKey:   block.keyField,
		entryValue: value,
	}, nil
}

// settingsCeilingAgentKeys are the agent block's own caps. D846's prefix test
// reaches budget.* and governance.*, and these two are ceilings of the same
// kind under a third name: agent.max_budget_usd is one stage's spend cap and
// agent.max_run_tokens a whole run's token cap, so raising either crosses the
// boundary the widening fence guards exactly as raising budget.per_run_usd
// does.
var settingsCeilingAgentKeys = []string{"agent.max_budget_usd", "agent.max_run_tokens"}

// settingsCeilingKey is the test D846 names: budget.* and governance.* are the
// keys whose loosening crosses the boundary that fence guards, and the
// agent block's caps are the same boundary under their own names.
//
// The project layer's own block is the same setting under the `layer.` prefix
// (SD14), so `layer.governance.review_ratio` is typed exactly as
// `governance.review_ratio` is, and the layer's `defaults.` block is that
// setting one word deeper: `layer.defaults.agent.max_budget_usd` is the cap
// every repository the layer templates runs with. All of them are the same
// fact — the file it would edit decides the ceiling of every run the project's
// branches start — which is what D846 guards.
func settingsCeilingKey(key string) bool {
	key = strings.TrimPrefix(key, "layer.")
	if strings.HasPrefix(key, "budget.") || strings.HasPrefix(key, "governance.") {
		return true
	}
	return slices.Contains(settingsCeilingAgentKeys, strings.TrimPrefix(key, "defaults."))
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
// `settings set` never writes budget.mode — the allowlist refuses it — but this
// comparison has a second caller, the publish door: policyCeilingMembers names
// budget.mode and policyLoosening hands each member to settingsLoosens, so a
// node republished at a weaker mode is a loosening there and one republished at
// a stronger mode is not.
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

// settingsDecisionModelRank orders a decision model by the tier order the
// orchestrator's block is written in (policy.DecisionModels, least capable
// first), and -1 for a name the order does not hold. The block's own writers
// refuse a name outside the order (policy.ValidOrchestrator), so one can only
// come from a node written by hand, and -1 sits below every tier rather than at
// the cheapest one — the two sides of settingsLoosens' comparison then read it
// as D846's asymmetry asks. A new value outside the order is looser than any
// name inside it, so an edit that writes one takes the typed name even though
// nobody can prove it tightens; an old value outside the order is lower than
// every tier, so a node repaired by naming a model the order holds is not a
// loosening. Two values outside the order compare equal, the one pair nothing
// is proven about either way.
//
// The write path never reaches this — the allowlist refuses the block's keys —
// but the publish door does: policyCeilingMembers names a member per class, so
// a node republished with a class moved down the order is a loosening there and
// one moved up is not (D1234, G6).
func settingsDecisionModelRank(name string) int {
	return slices.Index(policy.DecisionModels, name)
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
	if effective == "" {
		// No layer resolves a value, so what a run gets is the key's own
		// built-in default: a number for the agent block's spend cap, and
		// nothing to compare against for every other ceiling key.
		effective = settingsCeilingBuiltIn(row.Key)
	}
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

// settingsCeilingBuiltIn is what a run gets for an agent cap no layer resolves a
// value for. `agent.max_budget_usd`'s default is a number and not a state: a
// repository that writes no budget of its own starts under
// config.DefaultMaxBudgetUSD, which applyDefaults applies and the registry's row
// for the key reads. A base of "" read a write over that default as a first
// write and skipped the fence a raise meets, so an agent session could
// name its own spend cap far above the one it started under, from a file it may
// edit anyway (F2).
//
// Every other ceiling key answers "" there, and that is not the same blind spot.
// `agent.max_run_tokens`'s unset means the deployed ceiling, which this binary
// holds nowhere as a value, so there is no number to compare against; a
// `budget.*` or `governance.*` key no layer sets has no cap to raise, and the
// two writes that take a cap away — 0 and `unset` — are typed on their own. The
// project layer spells the same agent cap one block deeper (SD14), which is the
// `defaults.` prefix and the same number.
func settingsCeilingBuiltIn(key string) string {
	key = strings.TrimPrefix(key, "layer.")
	if strings.TrimPrefix(key, "defaults.") == "agent.max_budget_usd" {
		return strconv.Itoa(config.DefaultMaxBudgetUSD)
	}
	return ""
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
// ceiling key: a larger cap, a cap removed, a weaker mode, a wider model list,
// or a decision model moved down the tier order. A key no layer sets has
// nothing to loosen, so a first write of a cap is never the typed form — a
// first write of 0 is, because 0 is not a cap.
func settingsLoosens(key, oldValue, newValue string) bool {
	if key == "budget.mode" {
		if oldValue == "" {
			return false
		}
		return settingsModeRank(newValue) < settingsModeRank(oldValue)
	}
	if key == "budget.allowed_models" {
		if oldValue == "" {
			return false
		}
		return !settingsListSubset(newValue, oldValue)
	}
	if strings.HasPrefix(key, policyDecisionModelPrefix) {
		return settingsDecisionModelRank(newValue) < settingsDecisionModelRank(oldValue)
	}
	oldN, oldErr := strconv.ParseFloat(oldValue, 64)
	newN, newErr := strconv.ParseFloat(newValue, 64)
	// 0 is the value that sets no cap at all (`config.Budget`: "0 sets none";
	// the governance block's own rule is "0 and null mean unset"), so it is the
	// loosest a ceiling can be written: it hands the cap back to whatever layer
	// is above the file, which is exactly what `unset` does, and `unset` is
	// typed. It is a loosening whatever it replaces, including a value the file
	// does not hold: no cap is looser than any cap.
	if newErr == nil && newN == 0 {
		return true
	}
	// The mirror of that, and the reason this test is not simply "the number
	// rose": over a 0, any cap is tighter, so raising 0 to 3 is not a
	// loosening. Without this, a file already holding 0 would have every later
	// write read as a loosening — including the ones that put a cap back.
	if oldErr == nil && oldN == 0 {
		return false
	}
	if oldValue == "" {
		return false
	}
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
// owner's own hand, and the sentence saying what it widens: the write prints it
// and the dry run carries it as `why`, and it no longer ends by naming a
// question, because a widening asks none (settings_write.go, D1552).
// mode is the edit being made: set, unset, add or remove. `remove` takes one
// member out of a list, which narrows it, so it is never the typed form and has
// no case below; `unset` always is, for a ceiling, because it hands the
// decision to a looser layer.
func settingsTypedWrite(row settings.Row, oldValue, newValue, mode string) (bool, string) {
	key := row.Key
	ceilingWhy := "this raises a limit: the file it would edit decides the ceiling of every run this branch starts"
	followupWhy := "this turns a followup key on: a run then acts on comments from people it did not act on before"
	followupAddWhy := "this widens who is trusted: a run then acts on comments from an account it did not act on before"
	if settingsCeilingKey(key) {
		switch mode {
		case "unset":
			return true, "removing a ceiling hands the decision back to a looser layer"
		case "add":
			return true, ceilingWhy
		case "set":
			if settingsLoosens(key, oldValue, newValue) {
				return true, ceilingWhy
			}
		}
	}
	if strings.HasPrefix(key, "followup.") {
		switch mode {
		case "set":
			if settingsTrue(newValue) {
				return true, followupWhy
			}
		case "add":
			// A name added to a followup list widens who can steer a run that
			// holds the repository's credentials, which is the same widening as
			// turning one of these keys on: `--add` is the typed direction on
			// this block and `--remove` the tightening one, which has no case
			// here for the reason a ceiling's own remove has none. It carries
			// its own sentence all the same: the person reading the prompt is
			// adding an account to a list, and a sentence about a key being
			// turned on names neither the list nor the account.
			return true, followupAddWhy
		}
	}
	// A row's own sensitive direction: turning the key on is the act D846
	// reserves, and the row carries the sentence saying why.
	if row.TypedWhy != "" && mode == "set" && settingsTrue(newValue) {
		return true, row.TypedWhy
	}
	// An entry write is classified here and nowhere else (D1165): the block the
	// entry lands in says whether growing it hands a run something it did not
	// have, because the registry's rows name the block and not its entries. A
	// removal narrows the block and has no case, for the reason `remove` has
	// none.
	if mode == "add-entry" {
		if block, ok := settingsEntryBlockOf(key); ok && block.widens {
			return true, "this hands a run a credential it did not reach before: a workflow's secret is mounted into the job's environment"
		}
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
		case len(o.addEntry) > 0:
			for _, pair := range o.addEntry {
				parts = append(parts, "--add-entry", quoteWord(pair))
			}
		case len(o.removeEntry) > 0:
			parts = append(parts, "--remove-entry", quoteWord(o.removeEntry[0]))
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
	// The aim travels with the command: a person who runs it at their own
	// terminal has the same environment as the session that was refused, so
	// dropping these two would let the edit land in whichever installation
	// that environment selects rather than the one it was aimed at.
	if o.project != "" {
		parts = append(parts, "--project", quoteWord(o.project))
	}
	if o.config != "" {
		parts = append(parts, "--config", quoteWord(o.config))
	}
	return strings.Join(parts, " ")
}

// runSettingsSet is `settings set`: find the row, find the layer, check the
// value, then the shared write path (spec §4.2).
func runSettingsSet(cmd *cobra.Command, args []string, o settingsWriteOptions) error {
	if err := askConflict(askFlags{ask: o.ask, yes: o.yes}); err != nil {
		return err
	}
	if err := settingsDryRunFlags(o); err != nil {
		return err
	}
	var err error
	if o.repo, err = repoArg(cmd.Context(), settingsAim(o.project, o.config), o.repo); err != nil {
		return err
	}
	edits := 0
	for _, named := range []bool{o.add != "", o.remove != "", len(o.addEntry) > 0, len(o.removeEntry) > 0} {
		if named {
			edits++
		}
	}
	if edits > 1 {
		return userErr("--add, --remove, --add-entry and --remove-entry are four different edits: name one")
	}
	// One entry per invocation (D1167) holds for both halves of the pair: a
	// second --remove-entry is a second entry the command would have to take
	// away, and applying only the first would report a removal that did not
	// happen. --remove takes one value by its own signature; --add-entry's
	// second occurrence is refused below, by the entry's own shape.
	if len(o.removeEntry) > 1 {
		return userErr("--remove-entry carries one entry: name the one entry the command takes away, and repeat the command for another")
	}
	list := o.add != "" || o.remove != ""
	entry := len(o.addEntry) > 0 || len(o.removeEntry) > 0
	var key, value string
	switch {
	case (list || entry) && len(args) != 1:
		return userErr("settings set takes one key with --add, --remove, --add-entry or --remove-entry: the value is the edit's own and travels in the flag")
	case !list && !entry && len(args) != 2:
		return userErr("settings set takes a key and a value")
	default:
		key = args[0]
		if !list && !entry {
			value = args[1]
		}
	}
	lookup := settingsWriteRow
	if list {
		lookup = settingsWriteListRow
	}
	row, err := lookup(key)
	if err != nil {
		return err
	}
	sel, lc, err := settingsAimedSelection(o)
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
	// removed is how a removal's own refusal names what it takes away, and
	// entryAddr is the entry an entry write is about, nil for every other write:
	// the two are what the checks below need from the edit. entryDoc is the same
	// write seen by the dry run, which is the only reader of the entry's own
	// members (D1164).
	var removed string
	var entryAddr *settingsEntryAddr
	var entryDoc *settingsEntryWrite
	switch {
	case o.add != "":
		mode = "add"
		newData, err = yamledit.AddListItem(old, target.keyPath, o.add)
	case o.remove != "":
		mode = "remove"
		removed = quoteWord(o.remove)
		newData, err = yamledit.RemoveListItem(old, target.keyPath, o.remove)
	case entry:
		block, ok := settingsEntryBlockOf(key)
		if !ok {
			return userErr("%s takes no entry: --add-entry and --remove-entry write the blocks an entry's own name addresses — a workflow's secrets, whose entries the name field names, and its commands.rerun_failed, whose members name themselves", key)
		}
		if block.kind == settingsEntryList && block.keyField == "" {
			return userErr("%s takes no entry: an entry is addressed by its key field, and this block's key field is `key` — a list of files and not a name — so no entry of it has one of the kind this pair writes (D1163 decides the key field and not the shape of it)", key)
		}
		var written settingsEntryWrite
		if len(o.addEntry) > 0 {
			mode = "add-entry"
			written, err = settingsEntryAdd(old, target.keyPath, block, o.addEntry)
		} else {
			mode = "remove-entry"
			removed = quoteWord(o.removeEntry[0])
			written, err = settingsEntryRemove(old, target.keyPath, block, o.removeEntry[0])
		}
		if err == nil {
			newData, entryAddr, entryDoc = written.data, &written.addr, &written
		}
	default:
		newData, err = yamledit.SetScalar(old, target.keyPath, value)
	}
	if err != nil {
		return userErr("%s: %v", key, err)
	}
	changed := yamledit.Changed(old, newData)
	if !changed && !o.dryRun {
		// Nothing to write and nothing to hold against the loader: a
		// document this Fugaro cannot read back is not this edit's doing,
		// and a no-op that refused on it would report the file's own state
		// as the fault of a command that was asked for no change at all.
		// The dry run is the exception: it answers with a document whatever
		// the edit turns out to be, and `changed` is one of its members.
		fmt.Fprintf(cmd.OutOrStdout(), "%s: %s already reads what was asked for\n", target.path, key)
		return nil
	}
	// A removal that leaves a sequence with no items is refused wherever it
	// happens: the loader tells an absent key from an empty one, and the two
	// mean different things. A map member's removal is not asked, because the
	// loader's own rules for the block answer it and those are the rules that
	// decide.
	listRemoval := mode == "remove" || (entryAddr != nil && entryAddr.keyField != "")
	if changed && removed != "" && listRemoval && settingsListGoneOrEmpty(newData, target.keyPath) {
		return userErr("removing %s would leave %s an empty list, and an absent key and an empty list mean different things: remove the key with `fugaro settings unset %s --layer %s` instead", removed, key, key, target.layer)
	}
	if err := settingsReparse(target, newData); err != nil {
		return err
	}
	oldValue, newValue := settingsValueIn(old, target.keyPath), settingsValueIn(newData, target.keyPath)
	if entryAddr != nil {
		// An entry write's own values are the entry's and not the block's: the
		// member or the mapping the edit is about, never the sequence around it.
		oldValue, newValue = settingsEntryIn(old, *entryAddr), settingsEntryIn(newData, *entryAddr)
	}
	// The widening check compares against the tighter of the file's own value
	// and what a run gets today; the document's `old` is the file's own value,
	// which is what a window shows the operator as the value being changed.
	base := oldValue
	if settingsCeilingKey(key) {
		base = settingsCeilingBase(cmd.Context(), row, oldValue)
	}
	typed, why := settingsTypedWrite(row, base, value, mode)
	if !changed {
		// An edit that writes nothing asks for nothing: the real write prints
		// "already reads what was asked for" and demands no typed name, so the
		// dry run must not classify it as a widening either.
		typed, why = false, ""
	}
	edit := settingsEdit{
		Path:     target.path,
		Old:      old,
		New:      newData,
		What:     fmt.Sprintf("write %s in %s", key, target.path),
		Typed:    typed,
		TypedWhy: why,
		Kind:     target.kind,
		Command:  settingsWriteCommand("set", key, value, o, target.layer),
		Yes:      o.yes,
		Ask:      o.ask,
	}
	if o.dryRun {
		dry := &settingsDryRun{key: key, layer: target.layer, oldValue: oldValue, newValue: newValue, jsonOut: o.jsonOut}
		if entryDoc != nil {
			dry.operation, dry.entryKey, dry.entryValue, dry.entryFields =
				entryDoc.operation, entryDoc.entryKey, entryDoc.entryValue, entryDoc.entryFields
		}
		edit.DryRun = dry
	}
	// The gate is read where this command would ask, and --ask is one more
	// place: the flag has to speak about the marks — a refusal in a managed
	// project's session with no terminal, a sentence anywhere else — so the
	// read it needs is made even where the edit itself would have asked
	// nothing (--yes, or no terminal at all).
	managed, gate, err := settingsManagedStep(cmd.Context(), lc, sel.Path, target, key, typed, why, old, newData, "settings set", settingsWriteOwnTerminal(cmd) || o.ask)
	if err != nil {
		return err
	}
	// --ask, once the marks are read: the sentence it prints, or the refusal
	// that stops the run before a byte is written (managed_ask.go). It is
	// after the gate's own admit and never instead of it.
	if err := askDecide(cmd.OutOrStdout(), askFlags{ask: o.ask, yes: o.yes}, managed.Managed, settingsEditConditions(cmd, edit)); err != nil {
		return err
	}
	edit.Managed = managed
	if err := applySettingsEdit(cmd, lc, edit); err != nil {
		return err
	}
	if o.dryRun {
		return nil
	}
	// The record is written after the write and for a write that happened: a
	// dry run applies nothing, and an edit that reached applySettingsEdit wrote
	// or was refused, so this call is the only one this command makes (D1014).
	if err := gate.recorded(cmd.Context()); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "next: fugaro update")
	return nil
}

// runSettingsUnset is `settings unset`: remove the key from one layer so the
// layer below decides again, never writing a default in its place (spec §4.3).
func runSettingsUnset(cmd *cobra.Command, args []string, o settingsWriteOptions) error {
	if err := askConflict(askFlags{ask: o.ask, yes: o.yes}); err != nil {
		return err
	}
	if err := settingsDryRunFlags(o); err != nil {
		return err
	}
	var err error
	if o.repo, err = repoArg(cmd.Context(), settingsAim(o.project, o.config), o.repo); err != nil {
		return err
	}
	if len(args) != 1 {
		return userErr("settings unset takes one key")
	}
	key := args[0]
	row, err := settingsWriteRow(key)
	if err != nil {
		return err
	}
	sel, lc, err := settingsAimedSelection(o)
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
	if !yamledit.Changed(old, newData) && !o.dryRun {
		// The same order as the set path: a key that is not there is not a
		// write, so a document that does not read back is reported when a
		// write is actually asked for, not when nothing would change. The dry
		// run is the exception: it answers with a document whatever the edit
		// turns out to be, and `changed` is one of its members.
		fmt.Fprintf(cmd.OutOrStdout(), "nothing to unset: %s is not set in %s\n", key, target.path)
		return nil
	}
	if err := settingsReparse(target, newData); err != nil {
		return err
	}
	oldValue := settingsValueIn(old, target.keyPath)
	typed, why := settingsTypedWrite(row, oldValue, "", "unset")
	if !yamledit.Changed(old, newData) {
		typed, why = false, ""
	}
	edit := settingsEdit{
		Path:     target.path,
		Old:      old,
		New:      newData,
		What:     fmt.Sprintf("remove %s from %s", key, target.path),
		Typed:    typed,
		TypedWhy: why,
		Kind:     target.kind,
		Command:  settingsWriteCommand("unset", key, "", o, target.layer),
		Yes:      o.yes,
		Ask:      o.ask,
	}
	if o.dryRun {
		edit.DryRun = &settingsDryRun{key: key, layer: target.layer, oldValue: oldValue, newValue: settingsValueIn(newData, target.keyPath), jsonOut: o.jsonOut}
	}
	// The read and the flag's own sentence: the same pair, in the same order,
	// as the set path above.
	managed, gate, err := settingsManagedStep(cmd.Context(), lc, sel.Path, target, key, typed, why, old, newData, "settings unset", settingsWriteOwnTerminal(cmd) || o.ask)
	if err != nil {
		return err
	}
	if err := askDecide(cmd.OutOrStdout(), askFlags{ask: o.ask, yes: o.yes}, managed.Managed, settingsEditConditions(cmd, edit)); err != nil {
		return err
	}
	edit.Managed = managed
	if err := applySettingsEdit(cmd, lc, edit); err != nil {
		return err
	}
	if o.dryRun {
		return nil
	}
	if err := gate.recorded(cmd.Context()); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "next: fugaro update")
	return nil
}

// settingsDryRunFlags refuses the flag combinations a dry run does not take.
// --dry-run and --yes answer different questions — one says what the edit would
// do, the other answers a write that is about to happen — so the pair is
// refused rather than resolved (D1106); --json is the dry run's own shape, and
// a caller that asked for a document must not silently get a table instead.
func settingsDryRunFlags(o settingsWriteOptions) error {
	if o.dryRun {
		if o.yes {
			return userErr("--dry-run and --yes answer different questions: a dry run says what the edit would do and writes nothing, --yes answers a write; name one")
		}
		if o.ask {
			return userErr("--dry-run and --ask answer different questions: a dry run confirms nothing and writes nothing, --ask is about the confirmations a write is given; name one")
		}
		return nil
	}
	if o.jsonOut {
		return userErr("--json is the dry run's document: pass --dry-run with it, or read the settings with `fugaro settings --json`")
	}
	return nil
}

// settingsDryRun is the classification a dry run answers with: the facts the
// write path has and the shape they are printed in. The classification itself
// is the edit's — Widening and Why are the values of the same settingsTypedWrite
// call that decides whether the typed name is demanded, so a key added to
// settingsCeilingKey or a list rule changed in settingsListSubset moves this
// document with no edit here (D1106) — and this type carries only what that
// call does not.
type settingsDryRun struct {
	key, layer         string
	oldValue, newValue string
	// operation, entryKey, entryValue and entryFields are the entry write's own
	// members (D1164): the operation that wrote the entry, the field it is
	// addressed by, that field's value and the members it carries beside it.
	// They are empty for every other write, and the document leaves an empty
	// member out — a scalar write's document is the shape version 1 always had.
	operation   string
	entryKey    string
	entryValue  string
	entryFields map[string]string
	jsonOut     bool
}

// document is the dry run's document for the edit it describes. changed is the
// write path's own comparison of the two byte strings — the same comparison
// that decides whether a write would happen at all — and the caller does not
// pass it, so a document cannot claim a change the write path did not see.
func (d settingsDryRun) document(e settingsEdit, changed bool) settingsWriteDoc {
	fields := make(map[string]string, len(d.entryFields))
	for field, value := range d.entryFields {
		fields[safetext.Field(field)] = safetext.Field(value)
	}
	return settingsWriteDoc{
		Version:     settingsWriteDocVersion,
		Key:         safetext.Field(d.key),
		Layer:       safetext.Field(d.layer),
		Path:        safetext.Field(e.Path),
		Changed:     changed,
		Widening:    e.Typed,
		Why:         safetext.Field(e.TypedWhy),
		Old:         safetext.Field(d.oldValue),
		New:         safetext.Field(d.newValue),
		Operation:   safetext.Field(d.operation),
		EntryKey:    safetext.Field(d.entryKey),
		EntryValue:  safetext.Field(d.entryValue),
		EntryFields: fields,
	}
}

// settingsWriteDocVersion is the dry run's document version (D848: members are
// only ever added within a version, and removing or retyping one bumps this
// number). schemas/settings-write.schema.json is the contract.
const settingsWriteDocVersion = 1

// settingsWriteDoc is what a dry run prints: what one edit would do, and the
// write path's own classification of it. It is the document a consumer draws a
// confirmation window from (D1106), so every member is one this CLI produced —
// the consumer computes nothing — and every string is printed through
// safetext.Field, because a key, a path and a value are as much the file's and
// the command line's as they are this program's.
//
// Refusal is empty in every document this CLI prints. A refusal is this
// program's own sentence with a non-zero exit and no document at all, which is
// what a consumer branches on (D1106, D1112), so nothing ever sets this member:
// it is written out because the shape is the contract, and a consumer reads it
// by name.
type settingsWriteDoc struct {
	Version  int    `json:"version"`
	Key      string `json:"key"`
	Layer    string `json:"layer"`
	Path     string `json:"path"`
	Changed  bool   `json:"changed"`
	Widening bool   `json:"widening"`
	Why      string `json:"why"`
	Old      string `json:"old"`
	New      string `json:"new"`
	Refusal  string `json:"refusal"`
	// The entry write's own members (D1164). They are optional and only an
	// entry write produces them: version 1 may grow by members a document can
	// leave out, because the settings view the console draws its edit from pins
	// that version and reads the shape it has always read (D848).
	Operation   string            `json:"operation,omitempty"`
	EntryKey    string            `json:"entry_key,omitempty"`
	EntryValue  string            `json:"entry_value,omitempty"`
	EntryFields map[string]string `json:"entry_fields,omitempty"`
}

// writeSettingsWriteDoc prints the dry run's document: the versioned JSON
// document with --json, and the same facts as a table without it.
func writeSettingsWriteDoc(w io.Writer, doc settingsWriteDoc, jsonOut bool) error {
	if jsonOut {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	}
	return writeSettingsWriteTable(w, doc)
}

// writeSettingsWriteTable prints the document as the settings commands' own
// table shape, one row per member and in the document's own order: the two
// shapes carry the same facts or the table is a second contract, and a second
// contract is the one that drifts.
func writeSettingsWriteTable(w io.Writer, doc settingsWriteDoc) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "FIELD\tVALUE")
	for _, row := range [][2]string{
		{"version", strconv.Itoa(doc.Version)},
		{"key", doc.Key},
		{"layer", doc.Layer},
		{"path", doc.Path},
		{"changed", strconv.FormatBool(doc.Changed)},
		{"widening", strconv.FormatBool(doc.Widening)},
		{"why", doc.Why},
		{"old", doc.Old},
		{"new", doc.New},
		{"refusal", doc.Refusal},
		{"operation", doc.Operation},
		{"entry_key", doc.EntryKey},
		{"entry_value", doc.EntryValue},
		{"entry_fields", settingsEntryFieldsString(doc.EntryFields)},
	} {
		fmt.Fprintf(tw, "%s\t%s\n", row[0], row[1])
	}
	return tw.Flush()
}

// settingsEntryFieldsString renders the entry's members the one way a table cell
// carries them: the pairs sorted by field and comma-separated, the same spelling
// the document's own old and new carry an entry in.
func settingsEntryFieldsString(fields map[string]string) string {
	named := make([]string, 0, len(fields))
	for field := range fields {
		named = append(named, field)
	}
	slices.Sort(named)
	pairs := make([]string, 0, len(named))
	for _, field := range named {
		pairs = append(pairs, field+"="+fields[field])
	}
	return strings.Join(pairs, ", ")
}
