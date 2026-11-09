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
	Ref       string    // the release tag the marketplace must carry, vX.Y.Z
	Repo      string    // the marketplace repository: pluginwire.Repo, or a fork allowed with --allow-fork
	Markets   []Market  // claude plugin marketplace list --json
	Installed []Install // claude plugin list --json
}

// Kind is what a plan call is for, and so what its failure means to the step
// that runs it.
type Kind int

const (
	// Change is the update's own work: a failure fails the step.
	Change Kind = iota
	// Clear clears the declaration Claude Code refuses an add by. Claude Code
	// also refuses a clear of a name it has not declared in user settings,
	// which is the state a clear leaves behind: a failure is printed and the
	// plan goes on to the add.
	Clear
	// Declare declares the marketplace at this release's tag. A failure here is
	// read by the step, not by the plan: claude refusing because the name is
	// declared elsewhere (RefusedByDeclaration) is the one failure fugaro
	// cannot fix, and the plugin cannot be had on that machine; anything else
	// fails the step, after the declaration a Clear removed is put back.
	Declare
)

// Call is one claude invocation, with the kind that says how its failure is
// taken.
type Call struct {
	Args []string
	Kind Kind
}

// Plan is the change calls to make, in order (none: nothing to do), the plugin
// version that applied to the checkout before them ("" for none), and the add
// that puts back the declaration a Clear removes (nil when nothing is cleared):
// a Declare that fails for any reason other than the declaration itself runs it
// first, so a failed add leaves the machine with the marketplace it had.
type Plan struct {
	Calls   []Call
	Before  string
	Restore []string
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

// refNewer reports whether ref, a marketplace's own ref, is a release tag
// strictly newer than want (both strict vX.Y.Z); anything else about it — no
// ref, a branch name, an older tag — is not newer, and the marketplace is
// declared again at this release's tag.
func refNewer(ref, want string) bool {
	if !pluginwire.ValidTag(ref) || !pluginwire.ValidTag(want) {
		return false
	}
	c, _ := Compare(ref, want)
	return c > 0
}

// Decide plans the change calls for one checkout:
//   - a fugaro marketplace from another source: OtherMarketError;
//   - the marketplace pinned at Ref and every applying install at Want: none;
//   - otherwise the marketplace declared at Ref (missing, or there at another
//     ref that is not newer, in which case the declaration it is refused by is
//     cleared first, and Restore is the add that puts it back) and refreshed,
//     then the plugin installed at user scope when no install applies, or each
//     stale scope updated (local, project, user).
//
// Every call it returns passes Allowed.
func Decide(in Input) (Plan, error) {
	if !ValidRepo(in.Repo) {
		return Plan{}, fmt.Errorf("the marketplace repository %s is not a GitHub owner/name", pluginwire.Printable(in.Repo))
	}
	if !pluginwire.ValidTag(in.Ref) {
		return Plan{}, fmt.Errorf("the marketplace ref %s is not a release tag vX.Y.Z", pluginwire.Printable(in.Ref))
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
	// moved: the marketplace is on this machine, but not at this release's
	// tag, so what it serves is not this binary's plugin. Claude Code's add is
	// the call that sets a marketplace's ref (there is no set-ref call, and
	// update refreshes from the source the declaration already has) — but it
	// refuses a name that is already declared from a different source, and the
	// ref counts as part of the source, so every marketplace a previous fugaro
	// declared with no ref is one: the add of 2026-11-09's failure, which
	// claude answered with "already added from a different source ... remove
	// that marketplace first". Its own message is the route this step takes:
	// clear the declaration at user scope, then declare the name there at this
	// release's tag. A ref strictly newer than this binary's is never lowered
	// (U10): a newer fugaro wrote it, and the plugin from it is a note, not a
	// failure.
	moved := market != nil && market.Ref != in.Ref && !refNewer(market.Ref, in.Ref)
	applying := Applying(in.Root, in.Installed)
	p := Plan{Before: Effective(in.Root, in.Installed)}
	var stale []string
	for _, a := range applying {
		if a.Version != in.Want && !slices.Contains(stale, a.Scope) {
			stale = append(stale, a.Scope)
		}
	}
	if !moved && market != nil && len(applying) > 0 && len(stale) == 0 {
		return p, nil
	}
	if market == nil || moved {
		if moved {
			p.Calls = append(p.Calls, Call{Args: slices.Clone(removeUser), Kind: Clear})
			p.Restore = addMarket(in.Repo, restoreRef(market.Ref))
		}
		p.Calls = append(p.Calls, Call{Args: addMarket(in.Repo, in.Ref), Kind: Declare})
	}
	if market != nil {
		p.Calls = append(p.Calls, Call{Args: slices.Clone(updateMarket)})
	}
	if len(applying) == 0 {
		p.Calls = append(p.Calls, Call{Args: slices.Clone(installUser)})
	}
	for _, s := range scopes {
		if slices.Contains(stale, s) {
			p.Calls = append(p.Calls, Call{Args: updatePlugin(s)})
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
