package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/settings"
)

// The STATE vocabulary is closed (D851): a consumer branches on the column,
// which is the only reason it exists. The words are the ones the rigor design
// uses too, so `settings`, `rigor show`, `config layer` and `doctor` say the
// same thing about the same state.
const (
	stateOK      = "ok"
	stateStale   = "stale "
	stateUnread  = "unread: "
	stateUnknown = "unknown (no checkout on this machine)"
)

// setting is one row of the read: a key in one scope, the value a run gets,
// the layer that decided it, the built-in default and the state of the read.
type setting struct {
	Key     string
	Scope   string
	Value   string
	Wins    string
	Default string // the DEFAULT column as it prints
	// DefaultValue and DefaultUnset are the two halves the JSON document
	// keeps apart: a consumer branches on which one is set.
	DefaultValue string
	DefaultUnset string
	Source       string
	State        string
	// Sensitive rows carry the set/unset fact and never the value.
	Sensitive bool
	Levels    []level
}

// level is one layer that sets a key, for --why: the value it holds and
// whether it won.
type level struct {
	Layer   string
	Value   string
	Outcome string
}

// matches is the --grep test: the pattern, unanchored, against the key and
// then against each rendered column (spec §2.6).
func (s setting) matches(re *regexp.Regexp) bool {
	for _, text := range []string{s.Key, s.Value, s.Wins, s.Default, s.State, s.Scope} {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

// settingsRead is one run of the read: the documents it read and the clock it
// read them at. Everything in here is offline; the cloud layers are S2's.
type settingsRead struct {
	project string
	now     time.Time

	local     *localcfg.Config
	localPath string

	layer    *config.ProjectLayer
	layerAge time.Duration

	repos []repoRead
}

// repoRead is one repository scope: its fugaro.yaml resolved, or the reason it
// was not.
type repoRead struct {
	name   string
	config *config.Config
	res    *config.Resolution
	state  string
}

// readSettings reads the offline layers: the local config, the project layer's
// cache, this machine's remembered checkouts and the environment. It reads no
// bucket and no database, so it cannot be refused by one.
func readSettings(_ context.Context, o settingsOptions, now time.Time) (*settingsRead, error) {
	r := &settingsRead{now: now}
	in := localcfg.SelectInput{
		Project:    os.Getenv("FUGARO_PROJECT"),
		EnvProject: os.Getenv("FUGARO_PROJECT"),
		EnvConfig:  os.Getenv("FUGARO_CONFIG"),
		Getenv:     os.Getenv,
	}
	sel, lc, err := localcfg.Select(in)
	if err != nil {
		return nil, userErr("%w", err)
	}
	r.local, r.localPath = lc, sel.Path
	r.project = sel.Name
	if lc != nil && lc.Name != "" {
		r.project = lc.Name
	}
	if r.project == "" {
		return nil, userErr("no project is selected: set $FUGARO_PROJECT or $FUGARO_CONFIG, or run fugaro setup")
	}
	r.readLayerCache()
	if err := r.readRepos(o); err != nil {
		return nil, err
	}
	return r, nil
}

// readLayerCache reads the project layer the last cloud read cached. A cache
// that is absent leaves the layer unread rather than empty: the layer's rows
// then print their defaults, and nothing claims the layer says otherwise.
func (r *settingsRead) readLayerCache() {
	e, ok := localcfg.LoadLayerCache(os.Getenv, r.project)
	if !ok {
		return
	}
	l, ps := config.ParseProjectLayer([]byte(e.YAML), config.LayerAnchor{Project: r.project, GCPProject: e.GCPProject})
	if len(ps) > 0 {
		return
	}
	r.layer = l
	r.layerAge = r.now.Sub(e.CheckedAt)
}

// readRepos resolves the repositories this read covers: the one --repo names,
// or every one --all-repos names, each from the checkout this machine
// remembers for it. A repository with no checkout on this machine is
// `unknown (no checkout on this machine)`: its fugaro.yaml was not read, and
// its row is not guessed.
func (r *settingsRead) readRepos(o settingsOptions) error {
	if o.repo == "" && !o.allRepos {
		return nil
	}
	checkouts, err := localcfg.LoadCheckouts(os.Getenv)
	if err != nil {
		checkouts = localcfg.EmptyCheckouts()
	}
	remembered := map[string]string{}
	for _, c := range checkouts.ProjectCheckouts(r.project) {
		remembered[localcfg.CheckoutIdentity{Provider: c.Provider, Repo: c.Repo}.Key()] = checkoutPath(c)
	}
	names := []string{}
	if o.repo != "" {
		names = append(names, o.repo)
	} else {
		for _, c := range checkouts.ProjectCheckouts(r.project) {
			names = append(names, c.Repo)
		}
		if r.local != nil {
			for name := range r.local.Repos {
				if !slices.Contains(names, name) {
					names = append(names, name)
				}
			}
		}
		slices.Sort(names)
	}
	for _, name := range names {
		r.repos = append(r.repos, r.readRepo(name, remembered[checkoutKeyFor(name, remembered)]))
	}
	return nil
}

// readRepo resolves one repository scope from the checkout this machine
// remembers for it. The remembered path is the checkout's directory, so the
// file read is the fugaro.yaml inside it. A checkout the machine remembers but
// cannot read is `unread: <reason>`, not `unknown`: the two say different
// things, and a reader that cannot tell them apart would take a broken
// checkout for one that was never made.
func (r *settingsRead) readRepo(name, dir string) repoRead {
	rr := repoRead{name: name, state: stateUnknown}
	if dir == "" {
		return rr
	}
	data, err := readFugaroYAML(filepath.Join(dir, "fugaro.yaml"))
	if err != nil {
		rr.state = stateUnread + err.Error()
		return rr
	}
	cfg, res, ps := config.Resolve(data, r.layer)
	if len(ps) > 0 {
		rr.state = stateUnread + ps[0].Message
		return rr
	}
	rr.config, rr.res, rr.state = cfg, res, stateOK
	return rr
}

// checkoutKeyFor is the remembered entry's key for a repository named by its
// owner/name: the document is keyed "<provider>/<owner>/<name>", and the
// provider is the one the entry itself carries, so a lookup tries the key of
// every entry whose owner/name is the one asked for.
func checkoutKeyFor(name string, remembered map[string]string) string {
	for key := range remembered {
		if strings.HasSuffix(key, "/"+name) {
			return key
		}
	}
	return ""
}

// checkoutPath is the path a remembered repository was last seen in.
func checkoutPath(c localcfg.CheckoutRepo) string {
	if len(c.Paths) == 0 {
		return ""
	}
	return c.Paths[len(c.Paths)-1].Path
}

// rows builds the read's rows: one per (key, scope). The project scope is
// always read; a repository scope is added for each repository the flags name.
func (r *settingsRead) rows(keys map[string]bool, o settingsOptions) []setting {
	out := []setting{}
	for _, row := range settings.Rows() {
		if keys != nil && !keys[row.Key] {
			continue
		}
		if !layerSelected(row, o.layer) {
			continue
		}
		out = append(out, r.projectRow(row))
		for _, repo := range r.repos {
			out = append(out, r.repoRow(row, repo))
		}
	}
	return out
}

// layerSelected reports whether --layer keeps a row: the flag restricts the
// layers read, and a row none of whose layers is read is left out.
func layerSelected(row settings.Row, want []string) bool {
	if len(want) == 0 {
		return true
	}
	for _, l := range want {
		if l == settings.LayerDefault || slices.Contains(row.Layers, l) {
			return true
		}
	}
	return false
}

// projectRow is the key at the project scope: the layer file, the local config
// or the environment, whichever decides.
func (r *settingsRead) projectRow(row settings.Row) setting {
	s := setting{
		Key:          row.Key,
		Scope:        "project",
		Wins:         settings.LayerDefault,
		Default:      defaultText(row),
		DefaultValue: row.Default.Value,
		DefaultUnset: row.Default.Unset,
		Source:       row.Default.Const,
		State:        stateOK,
		Sensitive:    row.Sensitive,
	}
	switch {
	case strings.HasPrefix(row.Key, "layer."):
		if r.layer != nil {
			if v, ok := valueAt(r.layer, strings.TrimPrefix(row.Key, "layer.")); ok {
				s.Value, s.Wins = v, settings.LayerProject
				if r.layerAge > 0 {
					s.State = stateStale + humanAge(r.layerAge)
				}
			}
		}
	case row.Key == "update_check":
		s.Value, s.Wins = "true", settings.LayerEnv
		if r.local != nil {
			s.Value = fmt.Sprintf("%t", r.local.UpdateCheckEnabled())
			if r.local.UpdateCheck != nil {
				s.Wins = settings.LayerLocal
			}
		}
	case slices.Contains(row.Layers, settings.LayerLocal) && r.local != nil:
		if v, ok := valueAt(r.local, row.Key); ok {
			s.Value, s.Wins = v, settings.LayerLocal
		}
	}
	if s.Wins == settings.LayerDefault {
		// A key no layer sets prints the default value, never a blank: a
		// row whose default is a sentence prints the sentence, because
		// there is no value to print and a blank would read as "off".
		s.Value = s.DefaultValue
		if s.Value == "" {
			s.Value = s.DefaultUnset
		}
	}
	if row.Sensitive {
		s.Value = sensitiveText(s.Value, s.Wins)
	}
	if s.Wins != settings.LayerDefault {
		s.Levels = append(s.Levels, level{Layer: s.Wins, Value: s.Value, Outcome: "wins"})
	}
	return s
}

// repoRow is the key at a repository scope. A key of fugaro.yaml is resolved
// through config.Resolve, so the layer that decided it is the resolution's own
// answer and not a second ordering written here; every other key is the
// project scope's, because no repository file carries it.
func (r *settingsRead) repoRow(row settings.Row, repo repoRead) setting {
	s := r.projectRow(row)
	s.Scope = "repo:" + repo.name
	s.Levels = nil
	if repo.state != stateOK {
		s.State = repo.state
		return s
	}
	if !isYAMLKey(row) || repo.config == nil {
		return s
	}
	v, ok := valueAt(repo.config, row.Key)
	if !ok {
		return s
	}
	s.Value = v
	s.Wins = settings.LayerDefault
	if src, ok := repo.res.Sources[row.Key]; ok {
		s.Wins = src
	}
	if s.Wins == settings.LayerDefault {
		s.Value = s.Default
	}
	if row.Sensitive {
		s.Value = sensitiveText(v, s.Wins)
	}
	if s.Wins != settings.LayerDefault {
		s.Levels = append(s.Levels, level{Layer: s.Wins, Value: s.Value, Outcome: "wins"})
	}
	return s
}

// isYAMLKey reports whether a row is a fugaro.yaml key: only config.Scopes'
// layers belong to a repository file.
func isYAMLKey(row settings.Row) bool {
	for _, l := range row.Layers {
		switch l {
		case settings.LayerRepo, settings.LayerProfile, settings.LayerFlag:
			return true
		}
	}
	return false
}

// sensitiveText is what a sensitive row prints in place of its value: the
// set/unset fact, and nothing a consumer could mistake for the value.
func sensitiveText(value, wins string) string {
	if wins == settings.LayerDefault || value == "" {
		return "unset"
	}
	return "set"
}

// defaultText is the DEFAULT column: the value with the constant that holds
// it, or the "unset means" sentence.
func defaultText(row settings.Row) string {
	if row.Default.Unset != "" {
		return row.Default.Unset
	}
	if row.Default.Const != "" {
		return row.Default.Value + " (" + row.Default.Const + ")"
	}
	return row.Default.Value
}

// humanAge renders a cache's age the way the state column prints it.
func humanAge(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

// valueAt reads a dotted path out of a decoded document and renders it the way
// the read prints it. "*" matches any map key or list index, and the first one
// in sorted order is used, so two reads of one document agree. It is the one
// reader every offline layer goes through: the registry's keys are the
// documents' own yaml paths, so no layer needs a reader of its own (F6).
func valueAt(doc any, path string) (string, bool) {
	v := reflect.ValueOf(doc)
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return "", false
		}
		v = v.Elem()
	}
	for _, part := range strings.Split(path, ".") {
		v = stepInto(v, part)
		if !v.IsValid() {
			return "", false
		}
	}
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return "", false
		}
		v = v.Elem()
	}
	return renderValue(v), true
}

// stepInto is one path element: a struct's field by yaml tag, a map's value by
// key, or a list's first element.
func stepInto(v reflect.Value, part string) reflect.Value {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return reflect.Value{}
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Struct:
		t := v.Type()
		for i := range t.NumField() {
			tag := strings.Split(t.Field(i).Tag.Get("yaml"), ",")[0]
			if tag == part {
				return v.Field(i)
			}
		}
	case reflect.Map:
		if part == "*" {
			keys := v.MapKeys()
			if len(keys) == 0 {
				return reflect.Value{}
			}
			slices.SortFunc(keys, func(a, b reflect.Value) int {
				return strings.Compare(fmt.Sprint(a.Interface()), fmt.Sprint(b.Interface()))
			})
			return v.MapIndex(keys[0])
		}
		key := reflect.ValueOf(part)
		if key.Type() != v.Type().Key() {
			return reflect.Value{}
		}
		return v.MapIndex(key)
	case reflect.Slice, reflect.Array:
		if v.Len() == 0 {
			return reflect.Value{}
		}
		return v.Index(0)
	}
	return reflect.Value{}
}

// renderValue is a leaf as the read prints it: a list joins with ", ", a bool
// is true or false, and everything else is its own text.
func renderValue(v reflect.Value) string {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Slice, reflect.Array:
		parts := make([]string, 0, v.Len())
		for i := range v.Len() {
			parts = append(parts, renderValue(v.Index(i)))
		}
		return strings.Join(parts, ", ")
	case reflect.Map:
		return ""
	}
	if s, ok := v.Interface().(fmt.Stringer); ok {
		return s.String()
	}
	return fmt.Sprint(v.Interface())
}
