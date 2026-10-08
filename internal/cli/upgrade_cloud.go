package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// cloudState is the cloud step's state as read from local files only.
type cloudState int

const (
	cloudCurrent cloudState = iota // every kind's base_images entry is this release's base, or a newer copy
	cloudStale                     // a kind's entry is missing or an older release's
	cloudBlocked                   // a custom base: the refresh would stop with its one-line fix
	cloudNotHere                   // nothing to refresh from this machine and checkout; the reason says why
)

type cloudVerdict struct {
	state  cloudState
	repo   string
	reason string // one line, safe to print
}

// cloudCheck is the cloud step's state from fugaro.yaml and the local
// project config alone: never a bucket, the registry or a job, and no
// credentials (fugaro upgrade --check, and the skips of the cloud step). It
// cannot see a build that failed after the base moved: build records live in
// the bucket.
func cloudCheck(ctx context.Context, root string, co cloudOptions) cloudVerdict {
	ver := releaseVersion()
	if ver == "" {
		return cloudVerdict{state: cloudNotHere, reason: "a development build has no release base image to move to"}
	}
	if _, err := os.Stat(filepath.Join(root, "fugaro.yaml")); errors.Is(err, os.ErrNotExist) {
		return cloudVerdict{state: cloudNotHere, reason: "no fugaro.yaml yet, so no job image to refresh"}
	}
	_, cfg, err := loadCheckoutConfigAt(ctx, root)
	if err != nil {
		return cloudVerdict{state: cloudNotHere, reason: oneLine(err.Error())}
	}
	repo, err := checkoutRepo(ctx, root)
	if err != nil {
		return cloudVerdict{state: cloudNotHere, reason: oneLine(err.Error())}
	}
	lc, lcPath, _, err := loadRepoConfig(ctx, &initOptions{cloud: co}, root)
	if err != nil {
		return cloudVerdict{state: cloudNotHere, repo: repo, reason: "no local project config selects this checkout (" + oneLine(err.Error()) + "): the cloud step is for the operator who set the project up"}
	}
	if oi, ok := readOrigin(ctx, root); !ok || !repoKnown(lc, oi) {
		return cloudVerdict{state: cloudNotHere, repo: repo, reason: fmt.Sprintf("%s is not onboarded to project %s, so it has no job image yet", pluginwire.Printable(repo), lc.Name)}
	}
	host, err := infra.RegistryHost(lc)
	if err != nil {
		return cloudVerdict{state: cloudNotHere, repo: repo, reason: oneLine(err.Error())}
	}
	set := map[string]bool{}
	for _, w := range cfg.Workflows {
		set[w.Base] = true
	}
	var custom, stale []string
	for _, k := range slices.Sorted(maps.Keys(set)) {
		cur := lc.BaseImage(k)
		if cur != "" && !isManaged(cur, host, k) {
			custom = append(custom, k)
			continue
		}
		want, err := refreshBase(lc, k, ver)
		if err != nil {
			return cloudVerdict{state: cloudNotHere, repo: repo, reason: oneLine(err.Error())}
		}
		if cur != want {
			was := "none recorded"
			if cur != "" {
				was = pluginwire.Printable(cur)
			}
			stale = append(stale, fmt.Sprintf("%s: %s, this release's is %s", k, was, want))
		}
	}
	switch {
	case len(custom) > 0:
		return cloudVerdict{state: cloudBlocked, repo: repo, reason: oneLine(customBaseRefusal(lc, lcPath, custom, selfCommand()+" upgrade").Error())}
	case len(stale) > 0:
		return cloudVerdict{state: cloudStale, repo: repo, reason: "base images to move: " + strings.Join(stale, "; ")}
	}
	return cloudVerdict{state: cloudCurrent, repo: repo, reason: "the local config records this release's base image for every workflow's kind (--check reads no build record: a build that failed after the base moved shows only when fugaro upgrade runs the cloud step)"}
}
