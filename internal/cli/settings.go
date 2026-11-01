package cli

import (
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/settings"
)

// settingsRepoRE is what --repo accepts: a repository's owner/name, which is
// the tail of the scope the document prints and the pattern
// schemas/settings.schema.json holds that scope to ($defs.row.scope). A name
// this refuses would be written into a document its own reader rejects.
var settingsRepoRE = regexp.MustCompile(`^[^\s/]+/[^\s/]+$`)

// settingsOptions are the read's flags.
type settingsOptions struct {
	repo     string
	allRepos bool
	layer    []string
	grep     string
	changed  bool
	why      bool
	offline  bool
	jsonOut  bool
}

// newSettingsCmd is `fugaro settings`: the read over the registry. It writes
// nothing — no file, no cache, no cloud object — so it runs anywhere, a coding
// agent's session included (D850).
func newSettingsCmd() *cobra.Command {
	var o settingsOptions
	cmd := &cobra.Command{
		Use:   "settings [KEY|GLOB ...]",
		Short: "Show every setting, what decides it, and its default",
		Long: `Show one row per setting: the key, the value a run gets, the layer that
decided it, the built-in default, and the state of the read.

A bare ` + "`fugaro settings`" + ` prints every key of the registry. Positional arguments
are a key, a prefix or a glob; a name no key matches is refused with the nearest
names. In a glob * is one dotted segment and ** is any number of them, so
` + "`budget.*_usd`" + ` names a key inside budget and ` + "`workflows.**`" + ` is every
workflow key.

The layers are read in this order, loosest first:

  default < project (the project layer's defaults:) < profile < repo < flag
  caps.defaults < caps.repo
  the deployed ceiling < policy (the published node) < project < repo < branch

The ceiling keys — budget.mode, budget.per_run_usd, budget.allowed_models,
governance.* — tighten rather than replace: the smaller cap, the stricter mode
and the intersection of the lists win whatever the layer order. review_floor
and rigor.areas are unions: a lower layer only adds.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSettings(cmd, args, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "read this repository's fugaro.yaml (owner/name)")
	f.BoolVar(&o.allRepos, "all-repos", false, "read every repository this machine remembers")
	f.StringSliceVar(&o.layer, "layer", nil, "read only these layers ("+strings.Join(settings.Layers, ", ")+")")
	f.StringVar(&o.grep, "grep", "", "show only the rows whose key or a rendered column matches this regexp")
	f.BoolVar(&o.changed, "changed", false, "show only the rows a layer decides")
	f.BoolVar(&o.why, "why", false, "show the winning level for each key")
	f.BoolVar(&o.offline, "offline", false, "read the offline layers only")
	f.BoolVar(&o.jsonOut, "json", false, "print the versioned document")
	cmd.AddCommand(newSettingsGetCmd(), newSettingsSetCmd(), newSettingsUnsetCmd())
	return cmd
}

// runSettings is the read: it resolves the documents, builds the rows and
// renders them.
func runSettings(cmd *cobra.Command, args []string, o settingsOptions) error {
	if o.repo != "" && o.allRepos {
		return userErr("--repo and --all-repos are two different reads: name one repository, or every one this machine remembers")
	}
	for _, l := range o.layer {
		if !slices.Contains(settings.Layers, l) {
			return userErr("--layer %s: the layers are %s", l, strings.Join(settings.Layers, ", "))
		}
	}
	// --repo names a repository the way a scope is spelled — `repo:owner/name`,
	// the pattern the document's own schema holds the scope to — so a bare name
	// is refused here rather than matched against whatever remembered checkout
	// ends with it, which would print a row under a scope its reader refuses.
	if o.repo != "" && !settingsRepoRE.MatchString(o.repo) {
		return userErr("--repo %s: a repository is owner/name", o.repo)
	}
	var pattern *regexp.Regexp
	if o.grep != "" {
		var err error
		pattern, err = regexp.Compile(o.grep)
		if err != nil {
			return userErr("--grep: %v", err)
		}
	}
	keys, err := selectSettingsKeys(args)
	if err != nil {
		return err
	}
	now := time.Now()
	r, err := readSettings(cmd.Context(), o, now)
	if err != nil {
		return err
	}
	rows := r.rows(keys, o)
	if o.changed {
		rows = slices.DeleteFunc(rows, func(s setting) bool { return s.Wins == settings.LayerDefault })
	}
	if pattern != nil {
		rows = slices.DeleteFunc(rows, func(s setting) bool { return !s.matches(pattern) })
	}
	if o.jsonOut {
		return writeSettingsJSON(cmd.OutOrStdout(), r, rows, now)
	}
	return writeSettingsTable(cmd.OutOrStdout(), rows, o.why)
}

// selectSettingsKeys resolves the positional arguments against the registry:
// a key, a prefix or a glob. A name no key matches is refused with the nearest
// registry names, so a typo is not an empty table.
func selectSettingsKeys(args []string) (map[string]bool, error) {
	if len(args) == 0 {
		return nil, nil
	}
	all := settings.Rows()
	chosen := map[string]bool{}
	for _, arg := range args {
		matched := false
		for _, row := range all {
			if settingsKeyMatches(row.Key, arg) {
				chosen[row.Key] = true
				matched = true
			}
		}
		if !matched {
			return nil, userErr("%s: no setting has that name; the nearest are %s",
				arg, strings.Join(nearestSettings(arg, all), ", "))
		}
	}
	return chosen, nil
}

// settingsKeyMatches reports whether key answers to the argument: the key
// itself, a prefix of it, or a glob. In a glob * is one dotted segment and **
// is any number of them, which is the spelling a key needs: the registry names
// a varying segment with * itself (workflows.*.commands.test), so one * has to
// address exactly that segment, and a * spanning dots would make one argument
// answer for keys of different depths — `workflows.*` is a key directly under
// workflows, of which there are none, and `workflows.**` is every workflow key.
func settingsKeyMatches(key, arg string) bool {
	if !strings.Contains(arg, "*") {
		return key == arg || strings.HasPrefix(key, arg+".")
	}
	pattern := "^" + regexp.QuoteMeta(arg) + "$"
	pattern = strings.ReplaceAll(pattern, `\*\*`, `.*`)
	pattern = strings.ReplaceAll(pattern, `\*`, `[^.]*`)
	re, err := regexp.Compile(pattern)
	return err == nil && re.MatchString(key)
}

// nearestSettings is the registry names closest to a name nothing matches, by
// edit distance on the key as a whole.
func nearestSettings(arg string, all []settings.Row) []string {
	type scored struct {
		key  string
		dist int
	}
	out := make([]scored, 0, len(all))
	for _, row := range all {
		out = append(out, scored{row.Key, editDistance(arg, row.Key)})
	}
	slices.SortFunc(out, func(a, b scored) int {
		if a.dist != b.dist {
			return a.dist - b.dist
		}
		return strings.Compare(a.key, b.key)
	})
	best := []string{}
	for _, s := range out[:min(3, len(out))] {
		best = append(best, s.key)
	}
	return best
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		copy(prev, cur)
	}
	return prev[len(b)]
}
