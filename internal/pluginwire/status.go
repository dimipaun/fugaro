package pluginwire

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// State is one of the staleness states of design section 4.4.
type State string

const (
	OK               State = "ok"
	Outdated         State = "outdated"
	Newer            State = "newer"
	Unpinned         State = "unpinned"
	Foreign          State = "foreign"
	NotWired         State = "not wired"
	InstalledDiffers State = "installed differs"
	NotInstalled     State = "not installed"
	CannotCompare    State = "cannot compare"
)

// Severity is how loudly a state is reported.
type Severity int

const (
	SeverityNone    Severity = iota // ok, or not evaluated
	SeverityInfo                    // not installed, cannot compare: never a failure
	SeverityWarning                 // everything else
)

// Severity of the state.
func (s State) Severity() Severity {
	switch s {
	case "", OK:
		return SeverityNone
	case NotInstalled, CannotCompare:
		return SeverityInfo
	}
	return SeverityWarning
}

// Fix is the one-line remedy for the state.
func (s State) Fix() string {
	switch s {
	case Outdated, NotWired, Unpinned:
		return "run fugaro update-skills"
	case Newer:
		return "upgrade fugaro (the pin is never moved down)"
	case Foreign:
		return "check that the marketplace repository is the one you intend; fugaro never rewrites it"
	case InstalledDiffers:
		return "in Claude Code run /plugin marketplace update fugaro"
	case NotInstalled:
		return "open Claude Code in this folder in a new session and trust it; if the /fugaro: skills are not listed (for example the folder was already trusted), run /plugin marketplace add dimipaun/fugaro and /plugin install fugaro@fugaro in Claude Code, then restart the session"
	}
	return ""
}

// Report is how the project's wiring compares with the binary. Pin compares
// the settings file with the binary; Install, evaluated only when Pin is a
// release tag, compares the plugin Claude Code installed with the pin.
type Report struct {
	Pin     State  `json:"pin"`
	Install State  `json:"install,omitempty"`
	Binary  string `json:"binary,omitempty"`    // the binary's version, "dev" for a dev build
	Ref     string `json:"ref,omitempty"`       // the marketplace ref in settings
	Repo    string `json:"repo,omitempty"`      // the marketplace repository in settings
	Version string `json:"installed,omitempty"` // the installed plugin version, when readable
	Detail  string `json:"detail,omitempty"`    // a sentence for the person
}

// States are the report's states that are not ok, in order.
func (r Report) States() []State {
	var out []State
	for _, s := range []State{r.Pin, r.Install} {
		if s != "" && s != OK {
			out = append(out, s)
		}
	}
	return out
}

// Worst is the highest severity among the report's states.
func (r Report) Worst() Severity {
	w := SeverityNone
	for _, s := range r.States() {
		w = max(w, s.Severity())
	}
	return w
}

// DefaultInstalledPlugins is where Claude Code records installed plugins:
// <config dir>/plugins/installed_plugins.json. An empty result means the
// directory is unknown.
func DefaultInstalledPlugins() string {
	d := userConfigDir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "plugins", "installed_plugins.json")
}

// Status compares the wiring in the settings file at settingsPath with a
// binary of the given version and, when installedPlugins is readable, with
// the installed plugin. It reads two small files, never writes, never uses
// the network and never fails: what it cannot read becomes a state.
func Status(settingsPath, version, installedPlugins string) Report {
	r := Report{Binary: version}
	if r.Binary == "" {
		r.Binary = "dev"
	}
	pin, repo, wired, detail := readPin(settingsPath)
	r.Ref, r.Repo = pin, repo
	if !wired {
		r.Pin, r.Detail = NotWired, detail
		return r
	}
	root := filepath.Dir(filepath.Dir(settingsPath))
	pinV, pinIsTag := parseTag(pin)
	switch {
	case !strings.EqualFold(repo, Repo):
		r.Pin = Foreign
		r.Detail = fmt.Sprintf("the %q marketplace names the repository %s, not %s", Marketplace, repo, Repo)
	case pin == "":
		r.Pin = Unpinned
		r.Detail = fmt.Sprintf("the %q marketplace has no ref, so it follows the default branch", Marketplace)
	case !pinIsTag:
		r.Pin = Unpinned
		r.Detail = fmt.Sprintf("the %q marketplace ref %q is not a release tag vX.Y.Z", Marketplace, pin)
	default:
		bin, err := Tag(version)
		if err != nil {
			r.Pin = CannotCompare
			r.Detail = err.Error()
			break
		}
		binV, _ := parseSemver(bin)
		switch c := pinV.cmp(binV); {
		case c == 0:
			r.Pin = OK
		case c < 0:
			r.Pin = Outdated
			r.Detail = fmt.Sprintf("the plugin is pinned to %s, this fugaro is %s", pinV, binV)
		default:
			r.Pin = Newer
			r.Detail = fmt.Sprintf("the plugin is pinned to %s, newer than this fugaro %s", pinV, binV)
		}
	}
	if pinIsTag && r.Pin != Foreign {
		r.Install, r.Version = installState(installedPlugins, root, pinV)
	}
	return r
}

func parseTag(ref string) (semver, bool) {
	if !tagRE.MatchString(ref) {
		return semver{}, false
	}
	return parseSemver(ref)
}

// readPin reads the marketplace ref and repository and whether the plugin is
// wired (a github marketplace entry and the plugin enabled). A file that
// cannot be read or parsed is not wired, with the reason in detail.
func readPin(path string) (ref, repo string, wired bool, detail string) {
	if err := checkPath(path); err != nil {
		return "", "", false, err.Error()
	}
	data, _, exists, err := readFile(path, maxSettingsBytes)
	if err != nil {
		return "", "", false, err.Error()
	}
	if !exists {
		return "", "", false, path + " does not exist"
	}
	top, err := parseObject(data)
	if err != nil {
		return "", "", false, fmt.Sprintf("%s is not usable settings: %v", path, err)
	}
	markets, _, err := objectAt(top, "extraKnownMarketplaces")
	if err != nil {
		return "", "", false, err.Error()
	}
	entry, present, err := objectAt(markets, Marketplace)
	if err != nil || !present {
		return "", "", false, "no " + Marketplace + " marketplace in " + path
	}
	malformed := "malformed source of the " + Marketplace + " marketplace in " + path
	source, hasSource, err := objectAt(entry, "source")
	if err != nil || !hasSource {
		return "", "", false, malformed
	}
	switch kind := stringAt(source, "source"); {
	case kind == "":
		return "", "", false, malformed
	case kind != "github":
		// Not ours to compare: shown as foreign with the kind as the repo.
		repo = Printable(kind) + " source"
	default:
		if repo = Printable(stringAt(source, "repo")); repo == "" {
			return "", "", false, malformed
		}
	}
	ref = Printable(stringAt(source, "ref"))
	plugins, _, _ := objectAt(top, "enabledPlugins")
	if raw, ok := plugins.get(PluginID); !ok || string(raw) != "true" {
		if ok && string(raw) == "false" {
			return ref, repo, false, "the plugin " + PluginID + " is disabled by the repository (enabledPlugins is false in " + path + ")"
		}
		return ref, repo, false, "the plugin " + PluginID + " is not enabled in " + path
	}
	return ref, repo, true, ""
}

// installState compares the installed plugin with the pin. The file's format
// is observed, not documented: anything unexpected is cannot compare, and
// never an error.
func installState(path, root string, pin semver) (State, string) {
	if path == "" {
		return CannotCompare, ""
	}
	data, _, exists, err := readFile(path, 4<<20)
	if err != nil {
		return CannotCompare, ""
	}
	if !exists {
		return NotInstalled, ""
	}
	var file struct {
		Plugins map[string]json.RawMessage `json:"plugins"`
	}
	if json.Unmarshal(data, &file) != nil || file.Plugins == nil {
		return CannotCompare, ""
	}
	raw, ok := file.Plugins[PluginID]
	if !ok {
		return NotInstalled, ""
	}
	type install struct {
		Scope       string `json:"scope"`
		Version     string `json:"version"`
		ProjectPath string `json:"projectPath"`
	}
	var entries []install
	if json.Unmarshal(raw, &entries) != nil {
		var one install
		if json.Unmarshal(raw, &one) != nil {
			return CannotCompare, ""
		}
		entries = []install{one}
	}
	var got *install
	for i, e := range entries {
		applies := e.Scope == "user" || (e.ProjectPath != "" && sameDir(e.ProjectPath, root))
		if !applies {
			continue
		}
		if got == nil || e.Scope != "user" {
			got = &entries[i]
		}
	}
	if got == nil {
		return NotInstalled, ""
	}
	v, ok := parseSemver(got.Version)
	if !ok {
		return CannotCompare, ""
	}
	if v != pin {
		return InstalledDiffers, v.String()
	}
	return OK, v.String()
}
