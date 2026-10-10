package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// NodePM is the Node.js package manager a repository uses, detected from its
// lockfile and package.json's packageManager field.
type NodePM struct {
	Name     string   // npm, pnpm or yarn
	Berry    bool     // Yarn 2 or later
	Lockfile string   // repository-relative lockfile path
	Install  string   // the dependency warm-up the derived image runs
	Cache    []string // dependency cache directories: the default cache entry's paths
}

// WarmUp returns the dependency warm-up command: Install, or, when
// skipBuildScripts is set, Install without package lifecycle and build
// scripts (Yarn Berry's --mode=skip-build, the others' --ignore-scripts).
// pnpm fetch runs no scripts, so only its install takes the flag.
func (pm *NodePM) WarmUp(skipBuildScripts bool) string {
	switch {
	case !skipBuildScripts:
		return pm.Install
	case pm.Name == "yarn" && pm.Berry:
		return pm.Install + " --mode=skip-build"
	default:
		return pm.Install + " --ignore-scripts"
	}
}

// lockfiles lists each package manager's lockfile, in detection order.
var lockfiles = []struct{ pm, file string }{
	{"pnpm", "pnpm-lock.yaml"},
	{"yarn", "yarn.lock"},
	{"npm", "package-lock.json"},
	{"npm", "npm-shrinkwrap.json"},
}

// DetectNodePM inspects the checkout at root. package.json's packageManager
// field, when set, decides the tool; otherwise the first lockfile found in
// the order pnpm, yarn, npm does. It returns nil when root has no
// package.json or no lockfile, and an error when packageManager names an
// unsupported tool or one whose lockfile is missing.
func DetectNodePM(root string) (*NodePM, error) {
	data, err := ReadRegular(filepath.Join(root, "package.json"), MaxCheckoutFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("package.json: %w", err)
	}
	var pkg struct {
		PackageManager string `json:"packageManager"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, fmt.Errorf("package.json: %w", err)
	}
	name, version, _ := strings.Cut(pkg.PackageManager, "@")
	if name != "" && !slices.Contains([]string{"npm", "pnpm", "yarn"}, name) {
		return nil, fmt.Errorf("package.json's packageManager %s is not supported; use npm, pnpm or yarn", pkg.PackageManager)
	}
	exists := func(rel string) bool {
		_, err := os.Stat(filepath.Join(root, rel))
		return err == nil
	}
	var pm *NodePM
	for _, lf := range lockfiles {
		if (name == "" || name == lf.pm) && exists(lf.file) {
			pm = &NodePM{Name: lf.pm, Lockfile: lf.file}
			break
		}
	}
	if pm == nil {
		if name != "" {
			return nil, fmt.Errorf("package.json's packageManager is %s, but the repository has no %s lockfile", pkg.PackageManager, name)
		}
		return nil, nil
	}
	switch pm.Name {
	case "pnpm":
		pm.Install = "pnpm fetch && pnpm install --offline --frozen-lockfile"
		pm.Cache = []string{"~/.local/share/pnpm/store"}
	case "npm":
		pm.Install = "npm ci"
		pm.Cache = []string{"~/.npm"}
	case "yarn":
		major, _ := strconv.Atoi(strings.SplitN(version, ".", 2)[0])
		pm.Berry = major >= 2 || (version == "" && exists(".yarnrc.yml"))
		global := false
		if pm.Berry {
			var err error
			if global, err = yarnGlobalCache(root, major); err != nil {
				return nil, fmt.Errorf(".yarnrc.yml: %w", err)
			}
		}
		switch {
		case !pm.Berry:
			pm.Install = "yarn install --frozen-lockfile"
			pm.Cache = []string{"~/.cache/yarn"}
		case global:
			pm.Install = "yarn install --immutable"
			pm.Cache = []string{"~/.yarn/berry/cache"}
		default:
			pm.Install = "yarn install --immutable"
			pm.Cache = []string{".yarn/cache"}
		}
	}
	return pm, nil
}

// yarnGlobalCache reports whether Yarn Berry keeps its cache in the user's
// home: .yarnrc.yml's enableGlobalCache when set, otherwise Yarn's default,
// which is true from Yarn 4 (major 0 means the version is unknown).
// A .yarnrc.yml that exists but is not a regular file within the cap (a
// symlink, say) is an error, not a silent fall back to the default.
func yarnGlobalCache(root string, major int) (bool, error) {
	var rc struct {
		EnableGlobalCache *bool `yaml:"enableGlobalCache"`
	}
	switch data, err := ReadRegular(filepath.Join(root, ".yarnrc.yml"), MaxCheckoutFile); {
	case err == nil:
		_ = yaml.Unmarshal(data, &rc) // invalid YAML falls back to the default
	case !errors.Is(err, fs.ErrNotExist):
		return false, err
	}
	if rc.EnableGlobalCache != nil {
		return *rc.EnableGlobalCache, nil
	}
	return major == 0 || major >= 4, nil
}

// DefaultCache is the cache entry a workflow gets when fugaro.yaml declares
// none (design §5.1). For web-node and the Fugaro base it is the detected
// package manager's cache, keyed by its lockfile; root is the checkout.
// M4's cache restore and write-back apply it.
func DefaultCache(base, root string) ([]CacheEntry, error) {
	if base != "web-node" && base != BaseKind {
		return nil, nil
	}
	pm, err := DetectNodePM(root)
	if err != nil || pm == nil {
		return nil, err
	}
	return []CacheEntry{{Key: []string{pm.Lockfile}, Paths: pm.Cache}}, nil
}
