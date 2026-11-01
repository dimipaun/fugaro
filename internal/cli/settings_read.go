package cli

import (
	"context"
	"encoding/json"
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
	stateUnknown = "unknown (no checkout on this machine)"
)

// staleState is the cached copy's state, with its age, and unreadState is the
// state of a layer that was not read, with the reason. The two that carry text
// are built here rather than concatenated at each use: a constant holding a
// trailing space would need a second value beside it for every reader that
// wants the word alone, and one `+` with an empty reason would put the space in
// the document.
func staleState(d time.Duration) string { return "stale " + humanAge(d) }
func unreadState(reason string) string  { return "unread: " + reason }

// updateCheckKey is the one key the environment sets (spec §2.2: env overrides
// local, and today only update_check has both). FUGARO_NO_UPDATE_CHECK is the
// variable updatewarn.go reads to turn the check off, so the read names the
// same one.
const updateCheckKey = "update_check"

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

// settingsNow is the clock the read times its layers with. A seam, like
// doctorNow and lsNow: a test pins a duration exactly rather than racing one.
var settingsNow = time.Now

// settingsCloudLayers is the read's cloud half: the shared config's refresh,
// the published policy node and the Realtime Database caps (spec §2.7). It is
// the one place a read leaves this machine, and it is empty until S2 fills it.
// --offline is the gate in front of it, so the flag's promise — no bucket, no
// database — is a property of the read and not of a comment.
var settingsCloudLayers = func(_ context.Context, _ *settingsRead, _ settingsOptions) error { return nil }

// settingsRead is one run of the read: the documents it read, the clock it
// read them at, and what each of them cost.
type settingsRead struct {
	project string
	now     time.Time

	local     *localcfg.Config
	localPath string
	localMS   int64

	layer    *config.ProjectLayer
	layerAge time.Duration
	layerMS  int64

	repos []repoRead
}

// repoRead is one repository scope: its fugaro.yaml resolved, or the reason it
// was not.
type repoRead struct {
	name   string
	config *config.Config
	res    *config.Resolution
	state  string
	ms     int64
}

// readMS is what a layer's read cost, as the document's duration_ms. A layer
// that was read reports at least 1ms: every read here is of a local file, which
// finishes inside a millisecond, and truncating one to 0 would make "read, and
// fast" indistinguishable from "never read", which is the one thing 0 is
// reserved for.
func readMS(d time.Duration) int64 {
	if d < time.Millisecond {
		return 1
	}
	return d.Milliseconds()
}

// readSettings reads the offline layers: the local config, the project layer's
// cache, this machine's remembered checkouts and the environment. It reads no
// bucket and no database, so it cannot be refused by one; with --offline it
// does not reach for one either, because the flag skips settingsCloudLayers,
// the read's only cloud call.
func readSettings(ctx context.Context, o settingsOptions, now time.Time) (*settingsRead, error) {
	r := &settingsRead{now: now}
	in := localcfg.SelectInput{
		Project:    os.Getenv("FUGARO_PROJECT"),
		EnvProject: os.Getenv("FUGARO_PROJECT"),
		EnvConfig:  os.Getenv("FUGARO_CONFIG"),
		Getenv:     os.Getenv,
	}
	t := settingsNow()
	sel, lc, err := localcfg.Select(in)
	r.localMS = readMS(settingsNow().Sub(t))
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
	t = settingsNow()
	r.readLayerCache()
	r.layerMS = readMS(settingsNow().Sub(t))
	if err := r.readRepos(o); err != nil {
		return nil, err
	}
	if !o.offline {
		if err := settingsCloudLayers(ctx, r, o); err != nil {
			return nil, err
		}
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
		// One name, one row: two providers remembering one owner/name would
		// otherwise list the repository twice, and both rows would read the
		// same checkout.
		slices.Sort(names)
		names = slices.Compact(names)
	}
	for _, name := range names {
		t := settingsNow()
		rr := r.readRepo(name, remembered[checkoutKeyFor(name, remembered)])
		rr.ms = readMS(settingsNow().Sub(t))
		r.repos = append(r.repos, rr)
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
		rr.state = unreadState(err.Error())
		return rr
	}
	cfg, res, ps := config.Resolve(data, r.layer)
	if len(ps) > 0 {
		// String, not Message: the message alone says "is "", but the
		// project layer given is …" with nothing saying which key of the
		// file it is about.
		rr.state = unreadState(ps[0].String())
		return rr
	}
	rr.config, rr.res, rr.state = cfg, res, stateOK
	return rr
}

// checkoutKeyFor is the remembered entry's key for a repository named by its
// owner/name: the document is keyed "<provider>/<owner>/<name>", and the
// provider is the one the entry itself carries, so a lookup tries the key of
// every entry whose owner/name is the one asked for. Two providers can remember
// one owner/name — the same repository cloned from GitHub and from Bitbucket —
// so the keys are sorted and the first is the answer: reading them out of the
// map would give one read a different checkout than the next, and the row would
// print the state of a directory the read did not choose.
func checkoutKeyFor(name string, remembered map[string]string) string {
	keys := make([]string, 0, 1)
	for key := range remembered {
		if strings.HasSuffix(key, "/"+name) {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	slices.Sort(keys)
	return keys[0]
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
					s.State = staleState(r.layerAge)
				}
			}
		}
	case row.Key == updateCheckKey && os.Getenv(noUpdateCheckEnv) == "1":
		// The one key the environment sets, and it is read before the local
		// file because that is the order a run gets (spec §2.2): the switch
		// below would take the local config's update_check otherwise, and the
		// row would say a run checks for updates when its process turns the
		// check off.
		s.Value, s.Wins = "false", settings.LayerEnv
	case slices.Contains(row.Layers, settings.LayerLocal) && r.local != nil:
		if v, ok := valueAt(r.local, row.Key); ok {
			s.Value, s.Wins = v, settings.LayerLocal
		}
	case isYAMLKey(row) && r.layer != nil && r.layer.SetsDefault(row.Key):
		// The layer's defaults: are the project level of a fugaro.yaml key
		// (spec §2.2), so the project scope reports the value a run gets from
		// them rather than the key's built-in default. Two answers from two
		// places: the text says whether the layer writes the key at all
		// (SetsDefault), and the decoded block says what it writes — the
		// fields alone cannot tell `review_rounds: 0` from an unset key.
		//
		// The layer here is the cached copy, so the state is the cache's,
		// exactly as it is for a `layer.` key read from the same file.
		if v, ok := valueAt(r.layer, "defaults."+row.Key); ok {
			s.Value, s.Wins = v, settings.LayerProject
			if r.layerAge > 0 {
				s.State = staleState(r.layerAge)
			}
		}
	}
	if s.Wins == settings.LayerDefault {
		// A key no layer sets prints what its default says, and never a
		// blank: a blank would read as "off".
		s.Value = valueOrDefault(s)
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
	v, path, ok := valueAtPath(repo.config, row.Key)
	if !ok {
		return s
	}
	s.Value = v
	// The resolution is asked about the path the read actually took: the
	// registry names a varying segment with * (workflows.*.commands.test) and
	// Sources is keyed by real workflow names, so looking the key up as
	// written found nothing and the row printed its default while the
	// checkout set the key. Its answer is the level a run resolves the key
	// at — a layer of settings.Layers, or `profile <name>` when one of the
	// layer's profiles supplied the value, the spelling config.SourceProfile
	// holds and WINS prints (spec §2.2).
	s.Wins = repo.res.SourceOf(path)
	if s.Wins == settings.LayerDefault {
		s.Value = valueOrDefault(s)
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

// valueOrDefault is the VALUE a row prints when no layer decides it: the
// default value, or the sentence saying what unset means. It is the one place
// the two are chosen between, so the project and repository scopes cannot
// answer a key differently — the repository scope printed the DEFAULT column's
// own text (`4 (config.DefaultCPU)`) where the project scope printed `4`.
func valueOrDefault(s setting) string {
	if s.DefaultValue != "" {
		return s.DefaultValue
	}
	return s.DefaultUnset
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
// the read prints it. It is the one reader every offline layer goes through:
// the registry's keys are the documents' own yaml paths, so no layer needs a
// reader of its own (F6).
func valueAt(doc any, path string) (string, bool) {
	v, _, ok := valueAtPath(doc, path)
	return v, ok
}

// valueAtPath is valueAt with the path it took. "*" matches any map key or list
// index, and the first one in sorted order is used, so two reads of one
// document agree; the concrete path names the entry the walk stepped into, so a
// reader with a second question about the same value — repoRow asks the
// resolution which layer set it — asks it about the key it read rather than
// about the pattern.
func valueAtPath(doc any, path string) (string, string, bool) {
	v := reflect.ValueOf(doc)
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return "", "", false
		}
		v = v.Elem()
	}
	concrete := ""
	for _, part := range strings.Split(path, ".") {
		var step string
		v, step = stepInto(v, part)
		if !v.IsValid() {
			return "", "", false
		}
		switch {
		case strings.HasPrefix(step, "["):
			concrete += step
		case concrete == "":
			concrete = step
		default:
			concrete += "." + step
		}
	}
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return "", "", false
		}
		v = v.Elem()
	}
	return renderValue(v), concrete, true
}

// stepInto is one path element: a struct's field by yaml tag, a map's value by
// key, or a list's first element. It answers the name the walk took as well as
// the value: the field's tag, the map's key, or the list index.
func stepInto(v reflect.Value, part string) (reflect.Value, string) {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return reflect.Value{}, ""
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Struct:
		t := v.Type()
		for i := range t.NumField() {
			tag := strings.Split(t.Field(i).Tag.Get("yaml"), ",")[0]
			if tag == part {
				return v.Field(i), tag
			}
		}
	case reflect.Map:
		if part == "*" {
			keys := v.MapKeys()
			if len(keys) == 0 {
				return reflect.Value{}, ""
			}
			slices.SortFunc(keys, func(a, b reflect.Value) int {
				return strings.Compare(fmt.Sprint(a.Interface()), fmt.Sprint(b.Interface()))
			})
			return v.MapIndex(keys[0]), fmt.Sprint(keys[0].Interface())
		}
		// A path element is text, so a map is addressable only by a string
		// key: every map the config types carry is one — map[string]Workflow
		// and map[rigor.Mode]float64 alike, and the second is a named string
		// type a comparison of the two types would refuse. A map keyed by
		// anything else is not addressable by a path, and says so by returning
		// the invalid value rather than guessing at a rendering of its key.
		kt := v.Type().Key()
		if kt.Kind() != reflect.String {
			return reflect.Value{}, ""
		}
		key := reflect.ValueOf(part).Convert(kt)
		return v.MapIndex(key), part
	case reflect.Slice, reflect.Array:
		if v.Len() == 0 {
			return reflect.Value{}, ""
		}
		// A list is one key (Resolution.SourceOf's own rule), so the index is
		// written the way a path spells one and the resolution ignores it.
		return v.Index(0), "[0]"
	}
	return reflect.Value{}, ""
}

// renderValue is a leaf as the read prints it: a list joins with ", ", a bool
// is true or false, and everything else is its own text. A map or a struct has
// no text of its own and renders as its JSON: refusing such a key instead is
// not open to the read, because the registry requires a row for every field of
// the config types (TestRegistryCoversEveryConfigField) and four of them are
// maps — layer.claude_models, base_images, review_floor and
// layer.ab.exchange_rate, whose values are lists and numbers. A k=v join of
// those would be ambiguous
// about where one value ends; JSON is not, and it sorts the keys, so two reads
// of one document print the same text.
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
	case reflect.Map, reflect.Struct:
		b, err := json.Marshal(v.Interface())
		if err != nil {
			return ""
		}
		return string(b)
	}
	if s, ok := v.Interface().(fmt.Stringer); ok {
		return s.String()
	}
	return fmt.Sprint(v.Interface())
}
