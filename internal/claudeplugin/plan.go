package claudeplugin

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// Input is what Decide decides from.
type Input struct {
	Root      string    // the checkout's top directory
	Want      string    // this fugaro's release version, X.Y.Z
	Repo      string    // the marketplace repository: pluginwire.Repo, or a fork allowed with --allow-fork
	Markets   []Market  // claude plugin marketplace list --json
	Installed []Install // claude plugin list --json
}

// Plan is the change calls to make, in order (none: nothing to do), and the
// plugin version that applied to the checkout before them ("" for none).
type Plan struct {
	Calls  [][]string
	Before string
}

// OtherMarketError: Claude Code's fugaro marketplace on this machine is not
// the repository the checkout's pin names. fugaro never replaces it.
type OtherMarketError struct{ Got, Want string }

func (e *OtherMarketError) Error() string {
	return fmt.Sprintf("Claude Code's %q marketplace on this machine is %s, not %s, and fugaro upgrade never replaces it: if it is not one you trust, remove it in Claude Code with /plugin marketplace remove %s, then rerun",
		pluginwire.Marketplace, pluginwire.Printable(e.Got), e.Want, pluginwire.Marketplace)
}

// Applying is the installs of the plugin that apply to root: user scope, and
// project or local scope recorded for root itself. A managed install is left
// out (fugaro cannot update one), and so is another checkout's.
func Applying(root string, installed []Install) []Install {
	var out []Install
	for _, in := range installed {
		if in.ID != pluginwire.PluginID {
			continue
		}
		switch in.Scope {
		case "user":
			out = append(out, in)
		case "project", "local":
			if in.ProjectPath != "" && samePath(in.ProjectPath, root) {
				out = append(out, in)
			}
		}
	}
	return out
}

// Effective is the plugin version that applies to root: a project or local
// install's over the user one (as pluginwire's installState prefers), ""
// when none applies.
func Effective(root string, installed []Install) string {
	v := ""
	for _, in := range Applying(root, installed) {
		if v == "" || in.Scope != "user" {
			v = in.Version
		}
	}
	return v
}

// Decide plans the change calls for one checkout:
//   - a fugaro marketplace from another source: OtherMarketError;
//   - the marketplace there and every applying install at Want: none;
//   - otherwise the marketplace added (missing) or updated, then the plugin
//     installed at user scope when no install applies, or each stale scope
//     updated (local, project, user).
//
// Every call it returns passes Allowed.
func Decide(in Input) (Plan, error) {
	if !ValidRepo(in.Repo) {
		return Plan{}, fmt.Errorf("the marketplace repository %s is not a GitHub owner/name", pluginwire.Printable(in.Repo))
	}
	var market *Market
	for i := range in.Markets {
		if in.Markets[i].Name == pluginwire.Marketplace {
			market = &in.Markets[i]
		}
	}
	if market != nil && (market.Source != "github" || !strings.EqualFold(market.Repo, in.Repo)) {
		got := market.Repo
		if market.Source != "github" {
			got = strings.TrimSpace(market.Source + " source " + market.Repo)
		}
		return Plan{}, &OtherMarketError{Got: got, Want: in.Repo}
	}
	applying := Applying(in.Root, in.Installed)
	p := Plan{Before: Effective(in.Root, in.Installed)}
	var stale []string
	for _, a := range applying {
		if a.Version != in.Want && !slices.Contains(stale, a.Scope) {
			stale = append(stale, a.Scope)
		}
	}
	if market != nil && len(applying) > 0 && len(stale) == 0 {
		return p, nil
	}
	if market == nil {
		p.Calls = append(p.Calls, addMarket(in.Repo))
	} else {
		p.Calls = append(p.Calls, slices.Clone(updateMarket))
	}
	if len(applying) == 0 {
		p.Calls = append(p.Calls, slices.Clone(installUser))
	}
	for _, s := range scopes {
		if slices.Contains(stale, s) {
			p.Calls = append(p.Calls, updatePlugin(s))
		}
	}
	return p, nil
}

// Compare orders two X.Y.Z versions (a leading v allowed, no leading zeros);
// ok is false when either is not one.
func Compare(a, b string) (c int, ok bool) {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	if !oka || !okb {
		return 0, false
	}
	return slices.Compare(pa[:], pb[:]), true
}

func parseVersion(s string) ([3]int, bool) {
	var v [3]int
	parts := strings.Split(strings.TrimPrefix(s, "v"), ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || strconv.Itoa(n) != p {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

// samePath reports whether a and b name the same directory, through
// symbolic links where both resolve (macOS's /var is /private/var).
func samePath(a, b string) bool {
	ra, ea := filepath.EvalSymlinks(a)
	rb, eb := filepath.EvalSymlinks(b)
	if ea != nil || eb != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}
